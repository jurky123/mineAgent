package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/config"
	"mineagent/internal/storage"
)

// WorkspaceTools 返回写代码/执行代码四件套：ls / read / write / exec。
// 沙箱约束（v1，不做容器强隔离，靠这几条防破坏）：
//   - 根目录钳制：所有路径必须落在 cfg.Workspace.Root 内，.. / 绝对路径 /
//     符号链接逃逸全部拒绝（resolve 后再校验一次）。
//   - 读写上限：单文件 MaxFileBytes，超出拒绝；ls 单次最多 200 条。
//   - 执行：工作目录=Root，超时 ExecTimeoutSec，输出上限 MaxOutputBytes，
//     全局单并发（同一时间只跑一个命令），危险命令黑名单前置拦截。
//   - 权限：仅 QQ adminOpenIDs 可用。MC 通道默认不可用（allowMC=false 时
//     直接拒绝并留审计）；QQ 非管理员调用直接拒绝并留审计。
//   - 审计：每次调用都写 tool_audit（成功/拒绝/失败全留痕）。
type Workspace struct {
	cfg   config.Workspace
	admins map[string]bool
	store *storage.Store
	log   *slog.Logger
	reviewer Reviewer

	execMu sync.Mutex
}

func NewWorkspace(cfg config.Workspace, adminOpenIDs []string, store *storage.Store, log *slog.Logger) *Workspace {
	admins := make(map[string]bool, len(adminOpenIDs))
	for _, id := range adminOpenIDs {
		if id != "" {
			admins[id] = true
		}
	}
	return &Workspace{cfg: cfg, admins: admins, store: store, log: log}
}

// SetReviewer 装配默认审查器（main.go 在模型就绪后调用）。
// ctx 里单独放的（WithReviewer）优先于这个。
func (w *Workspace) SetReviewer(r Reviewer) {
	w.reviewer = r
}

// IsAdmin 供 main.go 装配时判断：QQ 消息的 union/user/member openid 任一命中即管理员。
func (w *Workspace) IsAdmin(openIDs ...string) bool {
	for _, id := range openIDs {
		if id != "" && w.admins[id] {
			return true
		}
	}
	return false
}

// Tools 返回给 QQ 通道的四件套。checkAdmin 由调用方（main.go 的 QQ 工具包装）
// 在 ctx 里放好，见 WithQQAdmin。
func (w *Workspace) Tools() []tool.BaseTool {
	return []tool.BaseTool{
		&wsTool{w: w, name: "workspace_ls",
			desc: "列出 workspace 沙箱目录内的文件（只读）。path 为相对路径，缺省列根目录。",
			params: map[string]*schema.ParameterInfo{
				"path": {Type: schema.String, Desc: "相对路径，缺省为根目录"},
			}, fn: w.ls},
		&wsTool{w: w, name: "workspace_read",
			desc: "读取 workspace 内一个文本文件（只读）。写文件前必须先读。",
			params: map[string]*schema.ParameterInfo{
				"path": {Type: schema.String, Desc: "相对路径，如 main.py", Required: true},
			}, fn: w.read},
		&wsTool{w: w, name: "workspace_write",
			desc: "在 workspace 内新建或覆盖写一个文本文件。覆盖前必须先 read 确认内容，不要覆盖重要文件。",
			params: map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "相对路径，如 main.py", Required: true},
				"content": {Type: schema.String, Desc: "完整文件内容", Required: true},
			}, fn: w.write},
		&wsTool{w: w, name: "workspace_exec",
			desc: "在 workspace 内执行一条 shell 命令（工作目录即沙箱根）。适合跑 python3/$VENV_BIN/python、$VENV_BIN/pip install 装包、curl/wget 从公开 http(s) 下载到 workspace 内、go 构建和小脚本。一次只做一件事，有超时和输出上限。rm/sudo/ssh/docker 等危险命令直接拒绝；curl/wget/pip 会先过静态约束再送 LLM 语义审查，审查不通过或审查器不可用则拒绝。",
			params: map[string]*schema.ParameterInfo{
				"command": {Type: schema.String, Desc: "shell 命令，如 $VENV_BIN/pip install requests 或 curl -s --max-time 10 https://example.com/data.json -o data.json", Required: true},
			}, fn: w.exec},
	}
}

type wsTool struct {
	w      *Workspace
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
	fn     func(ctx context.Context, args map[string]any) (string, error)
}

func (t *wsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

func (t *wsTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		t.w.audit(ctx, t.name, argsJSON, "bad_args", "", err.Error())
		return errorJSON("参数不是合法 JSON"), nil
	}
	if !qqAdminFromContext(ctx) {
		msg := "只有管理员能用写代码/执行代码工具"
		t.w.audit(ctx, t.name, argsJSON, "admin_denied", "", msg)
		return errorJSON(msg), nil
	}
	out, err := t.fn(ctx, args)
	if err != nil {
		t.w.audit(ctx, t.name, argsJSON, "error", "", err.Error())
		return errorJSON(err.Error()), nil
	}
	t.w.audit(ctx, t.name, argsJSON, "ok", "", truncate(out, 500))
	return out, nil
}

type qqAdminCtxKey struct{}

func WithQQAdmin(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, qqAdminCtxKey{}, isAdmin)
}

func qqAdminFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(qqAdminCtxKey{}).(bool)
	return v
}

// resolve 把用户给的相对路径钳制在 Root 内：拒绝绝对路径、.. 逃逸，
// resolve 符号链接后再校验一次（防软链指到 /etc 这类地方）。
func (w *Workspace) resolve(userPath string) (string, error) {
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("只允许 workspace 内的相对路径")
	}
	// 先对"用户输入本身"做逃逸检查：Clean 不拼 / 前缀，这样 ../ 会保留下来。
	if up := filepath.Clean(userPath); up == ".." || strings.HasPrefix(up, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径不能逃出 workspace")
	}
	clean := filepath.Clean(filepath.Join(string(filepath.Separator), userPath))
	// Clean 后形如 /a/b；去掉前导 / 再拼到 Root 下。
	rel := strings.TrimPrefix(clean, string(filepath.Separator))
	// Root 可能还不存在（单测 TempDir/ws）：先建出来，保证 EvalSymlinks 可用。
	if err := w.ensureRoot(); err != nil {
		return "", fmt.Errorf("workspace 不可用: %v", err)
	}
	abs := filepath.Join(w.cfg.Root, rel)
	// 目录本身可能不存在，先对已存在部分做 EvalSymlinks。
	base := abs
	for {
		if _, err := os.Lstat(base); err == nil {
			break
		}
		parent := filepath.Dir(base)
		if parent == base {
			break
		}
		base = parent
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %v", err)
	}
	rootResolved, err := filepath.EvalSymlinks(w.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("workspace 不可用: %v", err)
	}
	// abs 中 base 部分换成 resolved 后的，再整体校验。
	rest, err := filepath.Rel(base, abs)
	if err != nil {
		return "", fmt.Errorf("路径非法")
	}
	resolved := filepath.Join(resolvedBase, rest)
	if resolved != rootResolved && !strings.HasPrefix(resolved, rootResolved+string(filepath.Separator)) {
		return "", fmt.Errorf("路径不能逃出 workspace")
	}
	return abs, nil
}

func (w *Workspace) ensureRoot() error {
	return os.MkdirAll(w.cfg.Root, 0o755)
}

func (w *Workspace) ls(_ context.Context, args map[string]any) (string, error) {
	if err := w.ensureRoot(); err != nil {
		return "", err
	}
	sub, _ := args["path"].(string)
	dir, err := w.resolve(sub)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读目录失败: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	type item struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
		Size int64  `json:"size,omitempty"`
	}
	out := make([]item, 0, len(entries))
	for _, e := range entries {
		if len(out) >= 200 {
			break
		}
		it := item{Name: e.Name(), Dir: e.IsDir()}
		if !e.IsDir() {
			if fi, err := e.Info(); err == nil {
				it.Size = fi.Size()
			}
		}
		out = append(out, it)
	}
	raw, _ := json.Marshal(map[string]any{"path": sub, "entries": out, "truncated": len(entries) > 200})
	return string(raw), nil
}

func (w *Workspace) read(_ context.Context, args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("缺少参数: path")
	}
	full, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("读文件失败: %v", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("这是目录，用 workspace_ls 看它")
	}
	if fi.Size() > int64(w.cfg.MaxFileBytes) {
		return "", fmt.Errorf("文件太大（%d 字节，上限 %d），不读", fi.Size(), w.cfg.MaxFileBytes)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读文件失败: %v", err)
	}
	out, _ := json.Marshal(map[string]any{"path": p, "content": string(raw)})
	return string(out), nil
}

func (w *Workspace) write(_ context.Context, args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("缺少参数: path")
	}
	if len(content) > w.cfg.MaxFileBytes {
		return "", fmt.Errorf("内容太大（%d 字节，上限 %d），拆小再写", len(content), w.cfg.MaxFileBytes)
	}
	if err := w.ensureRoot(); err != nil {
		return "", err
	}
	full, err := w.resolve(p)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(full); err == nil && fi.IsDir() {
		return "", fmt.Errorf("目标是目录，不能覆盖写")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("建目录失败: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写文件失败: %v", err)
	}
	w.log.Info("workspace write", "path", p, "bytes", len(content))
	out, _ := json.Marshal(map[string]any{"path": p, "bytes": len(content)})
	return string(out), nil
}

func (w *Workspace) audit(ctx context.Context, toolName, argsJSON, decision, operator, result string) {
	entry := storage.AuditEntry{
		SessionID: sessionFromContext(ctx),
		Tool:      toolName,
		Risk:      "workspace",
		Requester: RequesterFromContext(ctx),
		Args:      truncate(argsJSON, 1000),
		Decision:  decision,
		Operator:  operator,
		Result:    truncate(result, 1000),
		CreatedAt: time.Now().UnixMilli(),
	}
	if _, err := w.store.SaveAudit(ctx, entry); err != nil {
		w.log.Warn("audit write failed", "err", err, "tool", toolName)
	}
}
