package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
	"mineagent/internal/version"
)

const systemInstruction = `你是 Minecraft 服务器「jzk 的服务器」的聊天 AI 助手。
规则：
- 玩家消息格式为 [玩家名] 内容；你只收到服务器公开聊天的最近记录。
- 用简体中文回答，语气轻松友好。
- 回复要短（一般不超过 80 字），适合游戏聊天框阅读，不要使用 Markdown 表格或多级标题。
- 你可以调用工具查询服务器实时信息（在线玩家、玩家详情、TPS/内存、游戏时间、天气）。被问到这些实时问题时必须先调用工具，不要凭猜测回答。
- 传送、给物品、执行服务器命令属于高权限操作，只能应玩家明确请求发起，并且必须等待管理员批准；玩家没有明确要求时绝对不要调用这些工具。
- 不确定的服务器信息不要编造，直接说不知道。`

type Request struct {
	Session          *session.Session
	Player           string
	RequesterID      string
	Query            string
	TriggerMessageID int64
}

type pendingRun struct {
	session     *session.Session
	player      string
	requesterID string
	cpID        string
	interruptID string
	tool        string
	cutoff      int64
}

type Agent struct {
	store     *storage.Store
	log       *slog.Logger
	sessionID string
	replyMode string

	runner    *adk.Runner
	approvals *tools.Approvals
	outcomes  <-chan tools.Outcome
	enabled   bool

	jobs chan Request

	mu      sync.Mutex
	pending map[string]*pendingRun
}

func New(ctx context.Context, cfg config.Config, store *storage.Store, log *slog.Logger,
	agentTools []tool.BaseTool, approvals *tools.Approvals) (*Agent, error) {
	a := &Agent{
		store:     store,
		log:       log,
		sessionID: cfg.Minecraft.SessionID,
		replyMode: cfg.Minecraft.ReplyMode,
		jobs:      make(chan Request, 16),
		pending:   make(map[string]*pendingRun),
		approvals: approvals,
	}
	if a.replyMode == "" {
		a.replyMode = "broadcast"
	}
	if approvals != nil {
		a.outcomes = approvals.Outcomes()
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

	middlewares, err := a.buildMiddlewares(ctx, cm)
	if err != nil {
		return nil, fmt.Errorf("init middlewares: %w", err)
	}

	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "mineagent",
		Description: "Minecraft 服务器聊天助手",
		Instruction: systemInstruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               agentTools,
				ExecuteSequentially: true,
			},
		},
		Handlers:      middlewares,
		MaxIterations: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("init chat model agent: %w", err)
	}

	a.runner = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		CheckPointStore: newCheckpointStore(store),
	})
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
	outcomes := a.outcomes
	if outcomes == nil {
		outcomes = make(chan tools.Outcome)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-a.jobs:
			a.respond(ctx, req)
		case out := <-outcomes:
			a.resume(ctx, out)
		}
	}
}

func (a *Agent) target(player string) string {
	if a.replyMode == "player" {
		return player
	}
	return ""
}

func (a *Agent) respond(ctx context.Context, req Request) {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	rctx = tools.WithRequester(rctx, req.Player)
	rctx = tools.WithRequesterID(rctx, req.RequesterID)
	rctx = tools.WithSession(rctx, a.sessionID)

	msgs, cutoff, err := a.history(rctx, req.TriggerMessageID)
	if err != nil {
		a.log.Error("agent history", "err", err)
		_ = req.Session.Reply(rctx, "抱歉，读取聊天记录失败。", req.Player)
		return
	}
	rctx = context.WithValue(rctx, historyCutoffKey{}, cutoff)

	cpID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	a.log.Info("agent run", "player", req.Player, "query", req.Query, "messages", len(msgs), "trigger", req.TriggerMessageID, "checkpoint", cpID)

	start := time.Now()
	iter := a.runner.Run(rctx, msgs, adk.WithCheckPointID(cpID))
	replied := a.consume(rctx, req, cpID, iter)
	if replied {
		a.log.Info("agent replied", "player", req.Player, "took", time.Since(start).Round(time.Millisecond).String())
	}
}

func (a *Agent) consume(ctx context.Context, req Request, cpID string, iter *adk.AsyncIterator[*adk.AgentEvent]) bool {
	var reply string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			a.log.Error("agent run failed", "err", ev.Err)
			_ = req.Session.Reply(ctx, "抱歉，我暂时无法回答（模型调用失败）。", a.target(req.Player))
			return true
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			a.handleInterrupt(ctx, req, cpID, ev.Action.Interrupted)
			return true
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

	if err := a.store.DeleteCheckpoint(ctx, cpID); err != nil {
		a.log.Warn("checkpoint cleanup failed", "err", err, "checkpoint", cpID)
	}
	if reply == "" {
		reply = "唔，我没有想好怎么回答。"
	}
	if err := req.Session.Reply(ctx, reply, a.target(req.Player)); err != nil {
		a.log.Error("agent reply", "err", err)
	}
	return true
}

func (a *Agent) handleInterrupt(ctx context.Context, req Request, cpID string, info *adk.InterruptInfo) {
	notified := false
	for _, ictx := range info.InterruptContexts {
		if !ictx.IsRootCause {
			continue
		}
		ai, ok := ictx.Info.(*tools.ApprovalInfo)
		if !ok || ai == nil {
			continue
		}
		a.mu.Lock()
		a.pending[ai.ApprovalID] = &pendingRun{
			session:     req.Session,
			player:      req.Player,
			requesterID: req.RequesterID,
			cpID:        cpID,
			interruptID: ictx.ID,
			tool:        ai.Tool,
			cutoff:      historyCutoffFrom(ctx),
		}
		a.mu.Unlock()
		a.approvals.Notify(*ai)
		a.log.Info("approval requested", "approvalId", ai.ApprovalID, "tool", ai.Tool, "requester", req.Player)
		notified = true
	}
	if !notified {
		a.log.Warn("interrupt without approval info", "player", req.Player)
		_ = req.Session.Reply(ctx, "这个操作需要人工确认，但审批信息丢失了。", a.target(req.Player))
		return
	}
	_ = req.Session.Reply(ctx, "这个操作需要管理员批准，我已经把请求发到服务器里了。", a.target(req.Player))
}

func (a *Agent) resume(ctx context.Context, out tools.Outcome) {
	a.mu.Lock()
	run, ok := a.pending[out.ApprovalID]
	if ok {
		delete(a.pending, out.ApprovalID)
	}
	a.mu.Unlock()
	if !ok {
		a.log.Warn("approval outcome for unknown run", "approvalId", out.ApprovalID)
		return
	}

	rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	rctx = tools.WithRequester(rctx, run.player)
	rctx = tools.WithRequesterID(rctx, run.requesterID)
	rctx = tools.WithSession(rctx, a.sessionID)
	rctx = context.WithValue(rctx, historyCutoffKey{}, run.cutoff)

	a.log.Info("resuming after approval",
		"approvalId", out.ApprovalID,
		"tool", run.tool,
		"approved", out.Decision.Approved,
		"operator", out.Decision.Operator,
	)
	iter, err := a.runner.ResumeWithParams(rctx, run.cpID, &adk.ResumeParams{
		Targets: map[string]any{run.interruptID: &out.Decision},
	})
	if err != nil {
		a.log.Error("resume failed", "err", err, "checkpoint", run.cpID)
		_ = run.session.Reply(rctx, "恢复执行失败："+err.Error(), a.target(run.player))
		return
	}
	a.consume(rctx, Request{Session: run.session, Player: run.player}, run.cpID, iter)
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

func (a *Agent) history(ctx context.Context, upToID int64) ([]*schema.Message, int64, error) {
	summary, err := a.store.LatestSummary(ctx, a.sessionID)
	if err != nil {
		return nil, 0, err
	}
	var cutoff int64
	msgs := make([]*schema.Message, 0, 64)
	if summary != nil {
		cutoff = summary.UpToMessageID
		msgs = append(msgs, schema.UserMessage("[之前聊天的摘要] "+summary.Text))
	}
	var recent []storage.Message
	if upToID > 0 {
		recent, err = a.store.MessagesBefore(ctx, a.sessionID, upToID, 200)
	} else {
		recent, err = a.store.MessagesAfter(ctx, a.sessionID, cutoff, 200)
	}
	if err != nil {
		return nil, 0, err
	}
	for _, m := range recent {
		switch m.AuthorKind {
		case "player":
			msgs = append(msgs, schema.UserMessage(fmt.Sprintf("[%s] %s", m.AuthorName, m.Text)))
		case "agent":
			msgs = append(msgs, schema.AssistantMessage(m.Text, nil))
		}
		if m.ID > cutoff {
			cutoff = m.ID
		}
	}
	return msgs, cutoff, nil
}
