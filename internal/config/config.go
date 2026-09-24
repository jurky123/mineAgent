package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Listen       string    `json:"listen"`
	Token        string    `json:"token"`
	LogLevel     string    `json:"logLevel"`
	AllowedRoles []string  `json:"allowedRoles"`
	Minecraft    Minecraft `json:"minecraft"`
	QQ           QQ        `json:"qq"`
	WeCom        WeCom     `json:"wecom"`
	AIBot        AIBot     `json:"aibot"`
	Model        Model     `json:"model"`
	Storage      Storage   `json:"storage"`
	Tools        Tools     `json:"tools"`
	Workspace    Workspace `json:"workspace"`
	Agent        Agent      `json:"agent"`
}

type Minecraft struct {
	Trigger   string `json:"trigger"`
	SessionID string `json:"sessionId"`
	ReplyMode string `json:"replyMode"`
}

// QQ 接官方 Bot（q.qq.com 注册，无封号风险）。
// 留空 appId/appSecret 即禁用 QQ 通道，不影响现有 MC 链路。
type QQ struct {
	AppID   string `json:"appId"`
	Secret  string `json:"appSecret"`
	// OpenAPI 基地址，默认 https://api.bot.qq.com；沙箱联调用得上时再改。
	APIBase string `json:"apiBase"`
	// 能用 workspace 写代码/执行代码的 QQ 身份（C2C 的 user_openid、群里的
	// member_openid，或 union_openid，任意一个命中即可）。留空则谁都不能用，
	// 日志里会打印每次消息的 openid，抄进去即可。
	AdminOpenIDs []string `json:"adminOpenIds"`
	// 同一会话收到消息后回复的最小间隔（毫秒），防刷屏。
	MinIntervalMS int `json:"minIntervalMs"`
	// 每个 QQ 用户最多同时挂起的 MC 高权限审批数，防刷审批。
	MaxPendingPerUser int `json:"maxPendingPerUser"`
}

// WorkspaceRoot 供 qq 通道发本地图片时定位 workspace（config.QQ 不存 root，
// channel 拿的是整个 config，这里直接读 Workspace 段）。
func (c Config) WorkspaceRoot() string {
	if c.Workspace.Root != "" {
		return c.Workspace.Root
	}
	return "workspace"
}

// WeCom 是企业微信自建应用通道（个人可注册，无需认证，无封号风险）。
// 留空 CorpID 即禁用，不影响 QQ/MC 链路。
// 架构：企微服务器 --HTTP回调--> 本机 :WeComPort/wecom（只支持 80/443，
// 微信侧要求 URL 必须 80 或 443 端口）。本机已有腾讯云控制台防火墙，
// 用到时去放行对应端口即可（ufw 未启用）。
// 会话隔离：wecom:c2c:<userid> 按人，wecom:group:<chatid> 按群，
// 与 qq:/minecraft- 会话天然隔离。requester 前缀 wecom:，复用 QQ 同款
// 外部审批路径（privileged.go 的 ExternalRequesterPrefixes）。
type WeCom struct {
	CorpID string `json:"corpId"`
	// 自建应用的 AgentID（数字，配成字符串也行，调 API 时转 int）。
	AgentID int `json:"agentId"`
	// 应用 Secret（应用详情页"查看"获取）。
	Secret string `json:"secret"`
	// 回调 Token / EncodingAESKey（"设置 API 接收"页随机获取）。
	Token       string `json:"token"`
	EncodingAES string `json:"encodingAesKey"`
	// 回调监听端口（微信只允许 80/443，默认 80）。Listen 还是内部 8765 不变，
	// 这个端口是专给企微回调开的 HTTP 入口。
	Port int `json:"port"`
	// 能用 workspace 的企微 userid（企业内明文 userid）。留空则谁都不能用。
	AdminUserIDs []string `json:"adminUserIds"`
	// 同一会话回复最小间隔毫秒（默认 1500，同 QQ）。
	MinIntervalMS int `json:"minIntervalMs"`
	// 每用户最多挂起 MC 审批数（默认 2，同 QQ）。
	MaxPendingPerUser int `json:"maxPendingPerUser"`
}

func DefaultWeCom() WeCom {
	return WeCom{Port: 80, MinIntervalMS: 1500, MaxPendingPerUser: 2}
}

// AIBot 是企业微信「智能机器人（长连接）」通道：
// 管理后台 → 安全与管理 → 管理工具 → 智能机器人 → 创建，
// API 模式选「长连接」，拿 BotID + Secret（长连接专用，与自建应用的 Token/AESKey 不同）。
// 优势：无需公网回调/加解密，机器人主动连 wss://openws.work.weixin.qq.com；
// 可扫码加为联系人、可进内部群被 @。留空 botId/secret 即禁用。
type AIBot struct {
	BotID  string `json:"botId"`
	Secret string `json:"secret"`
	// 能用 workspace 的企微 userid（机器人创建者是超管时是明文 userid）。
	AdminUserIDs []string `json:"adminUserIds"`
	// 同一会话回复最小间隔毫秒（默认 1500，也避开企微 30 条/分钟限频）。
	MinIntervalMS int `json:"minIntervalMs"`
}

func DefaultAIBot() AIBot {
	return AIBot{MinIntervalMS: 1500}
}

// Workspace 是写代码/执行代码工具的沙箱根目录。
// 所有读写/执行都被限制在这个目录内，v1 不做强隔离，靠目录约束+
// 命令硬拦截+受审命令约束+LLM语义审查+超时+输出上限+单并发+审计来防止破坏服务器。
type Workspace struct {
	Root string `json:"root"`
	// 单个文件读写上限（字节）。
	MaxFileBytes int `json:"maxFileBytes"`
	// 单次命令执行超时（秒）。
	ExecTimeoutSec int `json:"execTimeoutSec"`
	// 单次命令输出上限（字节），超出截断。
	MaxOutputBytes int `json:"maxOutputBytes"`
	// 语义审查：curl/wget/pip 这类"本身正当但参数可变坏"的命令，
	// 静态约束通过后送 LLM 二审。留空 model 即复用主模型。
	Review ExecReview `json:"review"`
}

// ExecReview 是 workspace_exec 的 LLM 二审配置。
// enabled=false 则 review 类命令一律拒绝（fail-closed）。
// 注意 config.json 里没写 review 段时 Enabled 为零值 false，但线上老行为是
// "配了主模型就默认开"——EffectiveReviewEnabled 处理这个兼容：只要主模型
// 配了就开，除非显式写 enabled=false。想彻底关就写 enabled=false。
type ExecReview struct {
	Enabled *bool `json:"enabled"`
	// 复用主模型时留空；想用更便宜/更严的模型就填 OpenAI 兼容的 baseURL/apiKey/name。
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
	// 单次审查超时（秒），默认 20。
	TimeoutSec int `json:"timeoutSec"`
}

// EffectiveReviewEnabled 主模型配了就默认开审查，除非显式 enabled=false。
func (c Config) EffectiveReviewEnabled() bool {
	if c.Workspace.Review.Enabled != nil {
		return *c.Workspace.Review.Enabled
	}
	return c.Model.BaseURL != "" && c.Model.Name != ""
}

type Model struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	Name    string `json:"name"`
}

type Storage struct {
	Path string `json:"path"`
}

// Agent 是工作调用长度与上下文的核心旋钮。
// 全是"越大越强、越贵越慢"的权衡，默认值按 2 核/7.5G 小机器 + QQ 5 分钟
// 被动窗口 + flash 级便宜模型标定，想更强就往上拧。
type Agent struct {
	// QQ 通道单轮最大工具调用迭代数（默认 80）。MC 通道保持 8（游戏聊天要短平快）。
	QQMaxIterations int `json:"qqMaxIterations"`
	// 单轮 run 超时秒数（默认 300=5 分钟，卡着 QQ 群被动 5 分钟窗口）。
	// 超时后转后台任务继续跑（见 BackgroundAfterSec），不会直接掐掉。
	RunTimeoutSec int `json:"runTimeoutSec"`
	// 每次 run 喂给模型的历史消息条数（默认 200）。越大记得越多、token 越多。
	HistoryLimit int `json:"historyLimit"`
	// 摘要触发：上下文 token 数（默认 12000）与消息数（默认 80）。
	// 越大摘要越晚触发、单轮上下文越长；越小越早压缩、越省 token。
	SummaryTokens   int `json:"summaryTokens"`
	SummaryMessages int `json:"summaryMessages"`
	// 截断兜底：上下文 token 超过此数时清掉早期轮次（默认 24000），
	// 保留最后 N 轮（默认 4）。比摘要阈值大一倍，防止摘要没赶上时爆上下文。
	ReductionTokens int `json:"reductionTokens"`
	ReductionKeep   int `json:"reductionKeep"`
	// 后台任务：run 超过此秒数还没完（默认 90），先给 QQ 回一条"正在做"，
	// 跑完再主动推结果。0=关闭后台任务（一直等到 RunTimeoutSec）。
	BackgroundAfterSec int `json:"backgroundAfterSec"`
}

func DefaultAgent() Agent {
	return Agent{
		QQMaxIterations:    80,
		RunTimeoutSec:      300,
		HistoryLimit:       200,
		SummaryTokens:      12000,
		SummaryMessages:    80,
		ReductionTokens:    24000,
		ReductionKeep:      4,
		BackgroundAfterSec: 90,
	}
}

type Tools struct {
	ApprovalTimeoutSeconds int `json:"approvalTimeoutSeconds"`
}

func DefaultTools() Tools {
	return Tools{
		ApprovalTimeoutSeconds: 180,
	}
}

func Default() Config {
	return Config{
		Listen:       "127.0.0.1:8765",
		LogLevel:     "info",
		AllowedRoles: []string{"minecraft"},
		Minecraft:    Minecraft{Trigger: "@agent", SessionID: "minecraft-main", ReplyMode: "broadcast"},
		QQ:           QQ{APIBase: "https://api.bot.qq.com", MinIntervalMS: 1500, MaxPendingPerUser: 2},
		WeCom:        DefaultWeCom(),
		AIBot:        DefaultAIBot(),
		Storage:      Storage{Path: "data/mineagent.db"},
		Tools:        DefaultTools(),
		Workspace: Workspace{
			Root:           "workspace",
			MaxFileBytes:   65536,
			ExecTimeoutSec: 15,
			MaxOutputBytes: 8192,
			Review:         ExecReview{TimeoutSec: 20},
		},
		Agent: DefaultAgent(),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		case !os.IsNotExist(err):
			return cfg, err
		}
	}
	applyEnv(&cfg)
	if cfg.Listen == "" {
		cfg.Listen = Default().Listen
	}
	if cfg.QQ.APIBase == "" {
		cfg.QQ.APIBase = Default().QQ.APIBase
	}
	if cfg.QQ.MinIntervalMS <= 0 {
		cfg.QQ.MinIntervalMS = Default().QQ.MinIntervalMS
	}
	if cfg.QQ.MaxPendingPerUser <= 0 {
		cfg.QQ.MaxPendingPerUser = Default().QQ.MaxPendingPerUser
	}
	dwc := DefaultWeCom()
	if cfg.WeCom.Port <= 0 {
		cfg.WeCom.Port = dwc.Port
	}
	if cfg.WeCom.MinIntervalMS <= 0 {
		cfg.WeCom.MinIntervalMS = dwc.MinIntervalMS
	}
	if cfg.WeCom.MaxPendingPerUser <= 0 {
		cfg.WeCom.MaxPendingPerUser = dwc.MaxPendingPerUser
	}
	dab := DefaultAIBot()
	if cfg.AIBot.MinIntervalMS <= 0 {
		cfg.AIBot.MinIntervalMS = dab.MinIntervalMS
	}
	if cfg.Workspace.Root == "" {
		cfg.Workspace.Root = Default().Workspace.Root
	}
	if cfg.Workspace.MaxFileBytes <= 0 {
		cfg.Workspace.MaxFileBytes = Default().Workspace.MaxFileBytes
	}
	if cfg.Workspace.ExecTimeoutSec <= 0 {
		cfg.Workspace.ExecTimeoutSec = Default().Workspace.ExecTimeoutSec
	}
	if cfg.Workspace.MaxOutputBytes <= 0 {
		cfg.Workspace.MaxOutputBytes = Default().Workspace.MaxOutputBytes
	}
	if cfg.Workspace.Review.TimeoutSec <= 0 {
		cfg.Workspace.Review.TimeoutSec = Default().Workspace.Review.TimeoutSec
	}
	d := DefaultAgent()
	if cfg.Agent.QQMaxIterations <= 0 {
		cfg.Agent.QQMaxIterations = d.QQMaxIterations
	}
	if cfg.Agent.RunTimeoutSec <= 0 {
		cfg.Agent.RunTimeoutSec = d.RunTimeoutSec
	}
	if cfg.Agent.HistoryLimit <= 0 {
		cfg.Agent.HistoryLimit = d.HistoryLimit
	}
	if cfg.Agent.SummaryTokens <= 0 {
		cfg.Agent.SummaryTokens = d.SummaryTokens
	}
	if cfg.Agent.SummaryMessages <= 0 {
		cfg.Agent.SummaryMessages = d.SummaryMessages
	}
	if cfg.Agent.ReductionTokens <= 0 {
		cfg.Agent.ReductionTokens = d.ReductionTokens
	}
	if cfg.Agent.ReductionKeep <= 0 {
		cfg.Agent.ReductionKeep = d.ReductionKeep
	}
	if cfg.Agent.BackgroundAfterSec < 0 {
		cfg.Agent.BackgroundAfterSec = d.BackgroundAfterSec
	}
	return cfg, nil
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (c Config) Redacted() Config {
	if c.Token != "" {
		c.Token = "<set>"
	}
	if c.Model.APIKey != "" {
		c.Model.APIKey = "<set>"
	}
	if c.QQ.Secret != "" {
		c.QQ.Secret = "<set>"
	}
	if c.Workspace.Review.APIKey != "" {
		c.Workspace.Review.APIKey = "<set>"
	}
	if c.WeCom.Secret != "" {
		c.WeCom.Secret = "<set>"
	}
	if c.WeCom.EncodingAES != "" {
		c.WeCom.EncodingAES = "<set>"
	}
	if c.AIBot.Secret != "" {
		c.AIBot.Secret = "<set>"
	}
	return c
}

func applyEnv(cfg *Config) {
	set := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	set(&cfg.Listen, "MINEAGENT_LISTEN")
	set(&cfg.Token, "MINEAGENT_TOKEN")
	set(&cfg.Model.APIKey, "MINEAGENT_MODEL_API_KEY")
	set(&cfg.Model.BaseURL, "MINEAGENT_MODEL_BASE_URL")
	set(&cfg.Model.Name, "MINEAGENT_MODEL_NAME")
	set(&cfg.Minecraft.Trigger, "MINEAGENT_TRIGGER")
	set(&cfg.QQ.AppID, "MINEAGENT_QQ_APPID")
	set(&cfg.QQ.Secret, "MINEAGENT_QQ_SECRET")
	set(&cfg.WeCom.CorpID, "MINEAGENT_WECOM_CORPID")
	set(&cfg.WeCom.Secret, "MINEAGENT_WECOM_SECRET")
	set(&cfg.WeCom.Token, "MINEAGENT_WECOM_TOKEN")
	set(&cfg.WeCom.EncodingAES, "MINEAGENT_WECOM_AESKEY")
	set(&cfg.AIBot.BotID, "MINEAGENT_AIBOT_BOTID")
	set(&cfg.AIBot.Secret, "MINEAGENT_AIBOT_SECRET")
	set(&cfg.Workspace.Root, "MINEAGENT_WORKSPACE")
	set(&cfg.Workspace.Review.BaseURL, "MINEAGENT_REVIEW_BASE_URL")
	set(&cfg.Workspace.Review.APIKey, "MINEAGENT_REVIEW_API_KEY")
	set(&cfg.Workspace.Review.Model, "MINEAGENT_REVIEW_MODEL")
	setInt(&cfg.Agent.QQMaxIterations, "MINEAGENT_QQ_MAX_ITERATIONS")
	setInt(&cfg.Agent.RunTimeoutSec, "MINEAGENT_RUN_TIMEOUT_SEC")
	setInt(&cfg.Agent.HistoryLimit, "MINEAGENT_HISTORY_LIMIT")
	setInt(&cfg.Agent.BackgroundAfterSec, "MINEAGENT_BACKGROUND_AFTER_SEC")
	setInt(&cfg.Workspace.ExecTimeoutSec, "MINEAGENT_EXEC_TIMEOUT_SEC")
	setInt(&cfg.WeCom.AgentID, "MINEAGENT_WECOM_AGENTID")
	setInt(&cfg.WeCom.Port, "MINEAGENT_WECOM_PORT")
}

func setInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			*dst = n
		}
	}
}
