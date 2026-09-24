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
	Model        Model     `json:"model"`
	Storage      Storage   `json:"storage"`
	Tools        Tools     `json:"tools"`
	Workspace    Workspace `json:"workspace"`
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
		Storage:      Storage{Path: "data/mineagent.db"},
		Tools:        DefaultTools(),
		Workspace: Workspace{
			Root:           "workspace",
			MaxFileBytes:   65536,
			ExecTimeoutSec: 15,
			MaxOutputBytes: 8192,
			Review:         ExecReview{TimeoutSec: 20},
		},
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
	set(&cfg.Workspace.Root, "MINEAGENT_WORKSPACE")
	set(&cfg.Workspace.Review.BaseURL, "MINEAGENT_REVIEW_BASE_URL")
	set(&cfg.Workspace.Review.APIKey, "MINEAGENT_REVIEW_API_KEY")
	set(&cfg.Workspace.Review.Model, "MINEAGENT_REVIEW_MODEL")
}
