package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ReviewRequest 是送给语义审查器的输入：静态层已通过（或转审）的命令原文。
type ReviewRequest struct {
	Command   string // shell 命令原文
	Summary   string // 静态层摘要（哪类受审命令、命中了什么）
	Requester string // qq:<openid> 或 MC 玩家名
	SessionID string // 会话 key
}

// ReviewDecision 是审查结论：Allow=false 一律拒绝执行。
type ReviewDecision struct {
	Allow  bool
	Reason string // 给用户的拒绝理由 / 通过时的备注（截断记审计）
}

// Reviewer 是命令语义审查器接口。exec 在静态 review  verdict 时调用：
// 返回 Allow=false 或 err（超时/解析失败）都 = 拒绝执行（fail-closed）。
type Reviewer interface {
	Review(ctx context.Context, req ReviewRequest) (ReviewDecision, error)
}

type reviewerCtxKey struct{}

// WithReviewer 供装配层（main.go）把审查器放进 ctx；
// exec 优先用 ctx 里的，没有才用 Workspace 自带的（SetReviewer）。
func WithReviewer(ctx context.Context, r Reviewer) context.Context {
	return context.WithValue(ctx, reviewerCtxKey{}, r)
}

func reviewerFromContext(ctx context.Context) Reviewer {
	r, _ := ctx.Value(reviewerCtxKey{}).(Reviewer)
	return r
}

// LLMReviewer 用一个 OpenAI 兼容的 chat completions 接口做二审。
// 默认复用主模型配置（baseURL/apiKey/name），model 留空即复用主模型名；
// 也可单独配更便宜/更严的模型。超时内无结论 = 拒绝（fail-closed）。
type LLMReviewer struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	// sessionID 透传给 opencode-go 网关的 x-opencode-session 头。
	// 没有它网关直接 400 MissingSessionID（线上已复现），所以必填。
	sessionID string
	client    *http.Client
	log       *slog.Logger
}

func NewLLMReviewer(baseURL, apiKey, model string, timeout time.Duration, log *slog.Logger) *LLMReviewer {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &LLMReviewer{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout + 5*time.Second},
		log:     log,
	}
}

// WithSessionID 设置 x-opencode-session 头（opencode-go 网关强制要求）。
func (r *LLMReviewer) WithSessionID(id string) *LLMReviewer {
	r.sessionID = id
	return r
}

const reviewerSystem = `你是服务器沙箱命令的安全审查员。判断一条即将在隔离沙箱（workspace 目录，
超时执行、输出截断、无 tty）里以低权限用户运行的 shell 命令是否可以执行。

允许（满足其一即可，不用苛求"只能装依赖/只能天气"）：
- 从公开 http(s) 源下载文件到 workspace 内并查看/解压/使用
- 给 workspace/.venv 装 Python 包、跑代码/编译/常规文件操作
- 调用公开查询 API（天气、汇率、新闻、文档等只读接口）并把结果存 workspace 再处理
必须拒绝：
- 上传/外传数据（POST/PUT、--data、curl -T、--upload-file、发到外部的 webhook/邮箱、
  把 workspace 外的文件打包外发、curl 结果直接 POST 到别处）
- 安装到系统目录、sudo/提权、改系统服务/防火墙/定时任务
- 访问内网/元数据地址（169.254.169.254、metadata.google.internal、localhost 服务端口探测）
- 钓鱼/短链/明显可疑域名、IP 直连的 http 下载
- 管道给 shell（curl|sh）、eval、反弹 shell、挖矿、扫描、暴力破解
- 读 workspace 之外的敏感文件、写 workspace 之外

只输出 JSON：{"allow":true/false,"reason":"一句话理由，不超过60字"}`

// reviewSchemaNote 要求模型只输出 JSON；解析走宽松路径（见 parseDecision）。
func (r *LLMReviewer) Review(ctx context.Context, req ReviewRequest) (ReviewDecision, error) {
	if r.baseURL == "" || r.model == "" {
		return ReviewDecision{}, fmt.Errorf("reviewer model not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"model": r.model,
		"messages": []map[string]string{
			{"role": "system", "content": reviewerSystem},
			{"role": "user", "content": fmt.Sprintf("请求者：%s\n会话：%s\n静态分类：%s\n命令：\n%s",
				req.Requester, req.SessionID, req.Summary, req.Command)},
		},
		"temperature": 0,
		"max_tokens":  150,
	})
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ReviewDecision{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	}
	if r.sessionID != "" {
		httpReq.Header.Set("x-opencode-session", r.sessionID)
		httpReq.Header.Set("User-Agent", "MineAgent/reviewer")
	}
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return ReviewDecision{}, fmt.Errorf("reviewer call: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return ReviewDecision{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReviewDecision{}, fmt.Errorf("reviewer http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	content, err := extractContent(raw)
	if err != nil {
		return ReviewDecision{}, err
	}
	dec, err := parseDecision(content)
	if err != nil {
		return ReviewDecision{}, err
	}
	r.log.Info("exec reviewed", "allow", dec.Allow, "reason", dec.Reason,
		"requester", req.Requester, "command", truncate(req.Command, 120))
	return dec, nil
}

func extractContent(raw []byte) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("reviewer decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("reviewer error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("reviewer empty choices")
	}
	return out.Choices[0].Message.Content, nil
}

// parseDecision 宽松解析：模型可能包 markdown 代码围栏，先剥掉再找 JSON。
// allow 缺失/非 true 一律按拒绝处理（fail-closed）；reason 截断 200 字。
func parseDecision(content string) (ReviewDecision, error) {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ReviewDecision{}, fmt.Errorf("reviewer bad JSON: %s", truncate(s, 200))
	}
	var dec struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &dec); err != nil {
		return ReviewDecision{}, fmt.Errorf("reviewer bad JSON: %w", err)
	}
	allow := dec.Allow != nil && *dec.Allow
	reason := strings.TrimSpace(dec.Reason)
	if r := []rune(reason); len(r) > 200 {
		reason = string(r[:200])
	}
	if reason == "" {
		if allow {
			reason = "审查通过"
		} else {
			reason = "审查未通过"
		}
	}
	return ReviewDecision{Allow: allow, Reason: reason}, nil
}
