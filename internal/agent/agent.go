package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/version"
)

const systemInstruction = `你是 Minecraft 服务器「jzk 的服务器」的聊天 AI 助手。
规则：
- 玩家消息格式为 [玩家名] 内容；你只收到服务器公开聊天的最近记录。
- 用简体中文回答，语气轻松友好。
- 回复要短（一般不超过 80 字），适合游戏聊天框阅读，不要使用 Markdown 表格或多级标题。
- 不确定的服务器信息不要编造，直接说不知道。`

type Request struct {
	Session *session.Session
	Player  string
	Query   string
}

type Agent struct {
	store     *storage.Store
	log       *slog.Logger
	sessionID string

	runner  *adk.Runner
	enabled bool
	jobs    chan Request
}

func New(ctx context.Context, cfg config.Config, store *storage.Store, log *slog.Logger) (*Agent, error) {
	a := &Agent{
		store:     store,
		log:       log,
		sessionID: cfg.Minecraft.SessionID,
		jobs:      make(chan Request, 16),
	}
	if cfg.Model.BaseURL == "" || cfg.Model.Name == "" {
		log.Warn("model not configured, agent disabled", "hint", "set model.baseURL/model.name/apiKey in config.json or MINEAGENT_MODEL_* env")
		return a, nil
	}

	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: cfg.Model.BaseURL,
		APIKey:  cfg.Model.APIKey,
		Model:   cfg.Model.Name,
		HTTPClient: &http.Client{
			Timeout: 90 * time.Second,
			Transport: &headerTransport{
				base:      http.DefaultTransport,
				userAgent: "MineAgent/" + version.Version,
				sessionID: "mineagent-" + cfg.Minecraft.SessionID,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init chat model: %w", err)
	}

	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "mineagent",
		Description:   "Minecraft 服务器聊天助手",
		Instruction:   systemInstruction,
		Model:         cm,
		MaxIterations: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("init chat model agent: %w", err)
	}

	a.runner = adk.NewRunner(ctx, adk.RunnerConfig{Agent: chatAgent})
	a.enabled = true
	log.Info("agent enabled", "model", cfg.Model.Name, "baseURL", cfg.Model.BaseURL)
	return a, nil
}

func (a *Agent) Enabled() bool { return a.enabled }

func (a *Agent) Submit(req Request) bool {
	if !a.enabled {
		return false
	}
	select {
	case a.jobs <- req:
		return true
	default:
		a.log.Warn("agent queue full, dropping request", "player", req.Player)
		return false
	}
}

func (a *Agent) Run(ctx context.Context) {
	if !a.enabled {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-a.jobs:
			a.respond(ctx, req)
		}
	}
}

func (a *Agent) respond(ctx context.Context, req Request) {
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	msgs, err := a.history(rctx)
	if err != nil {
		a.log.Error("agent history", "err", err)
		_ = req.Session.Reply(rctx, "抱歉，读取聊天记录失败。", req.Player)
		return
	}
	a.log.Info("agent run", "player", req.Player, "query", req.Query, "messages", len(msgs))

	start := time.Now()
	iter := a.runner.Run(rctx, msgs)

	var reply string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			a.log.Error("agent run failed", "err", ev.Err)
			_ = req.Session.Reply(rctx, "抱歉，我暂时无法回答（模型调用失败）。", req.Player)
			return
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		msg, err := ev.Output.MessageOutput.GetMessage()
		if err != nil {
			a.log.Warn("agent message decode", "err", err)
			continue
		}
		if msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			reply = strings.TrimSpace(msg.Content)
		}
	}

	if reply == "" {
		reply = "唔，我没有想好怎么回答。"
	}
	if err := req.Session.Reply(rctx, reply, req.Player); err != nil {
		a.log.Error("agent reply", "err", err)
		return
	}
	a.log.Info("agent replied", "player", req.Player, "took", time.Since(start).Round(time.Millisecond).String(), "chars", len(reply))
}

type headerTransport struct {
	base      http.RoundTripper
	userAgent string
	sessionID string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.userAgent)
	if t.sessionID != "" {
		r.Header.Set("x-opencode-session", t.sessionID)
	}
	return t.base.RoundTrip(r)
}

func (a *Agent) history(ctx context.Context) ([]*schema.Message, error) {
	recent, err := a.store.RecentMessages(ctx, a.sessionID, 60)
	if err != nil {
		return nil, err
	}
	msgs := make([]*schema.Message, 0, len(recent))
	for _, m := range recent {
		switch m.AuthorKind {
		case "player":
			msgs = append(msgs, schema.UserMessage(fmt.Sprintf("[%s] %s", m.AuthorName, m.Text)))
		case "agent":
			msgs = append(msgs, schema.AssistantMessage(m.Text, nil))
		}
	}
	return msgs, nil
}
