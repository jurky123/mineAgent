package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
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
	SessionKey       string
	Player           string
	RequesterID      string
	// MCRequester 只有 QQ 通道会填：QQ 身份绑定的 MC 玩家名（可能为空=未绑定）。
	// 传给 Gateway.Call 作为发给 MC 插件的 requester；审批/审计用的请求者
	// 仍是 Player（qq:<openid> 形式），见 pendingRun.mcRequester。
	MCRequester      string
	Query            string
	TriggerMessageID int64
	// Tools 为空则用默认集；QQ 通道传入自己的工具集（只读+workspace+MC高权限）。
	// 用 systemInstruction 区分两个通道的人设与约束。
	Tools             []tool.BaseTool
	SystemInstruction string
}

type pendingRun struct {
	session     *session.Session
	sessionKey  string
	player      string
	requesterID string
	mcRequester string
	cpID        string
	runnerKey   string
	interruptID string
	tool        string
	cutoff      int64
	tools       []tool.BaseTool
	instruction string
}

type Agent struct {
	store *storage.Store
	log   *slog.Logger
	// sessionKey/defaultTools/instruction 是 MC 主会话的；QQ 每个会话走
	// Request 里自带的 tools+instruction（见 respond/resume）。
	sessionKey   string
	model        model.BaseModel[*schema.Message]
	defaultTools []tool.BaseTool
	instruction  string
	replyMode    string

	mcRunner  *adk.Runner
	approvals *tools.Approvals
	enabled   bool

	jobs chan Request

	mu      sync.Mutex
	pending map[string]*pendingRun
	// QQ runner 按 (sessionKey, instruction, 工具集指纹) 缓存。
	// 同一会话的工具集是固定的（main.go 装配时确定），指纹只防配错。
	runners map[string]*adk.Runner
}

func New(ctx context.Context, cfg config.Config, store *storage.Store, log *slog.Logger,
	agentTools []tool.BaseTool, approvals *tools.Approvals) (*Agent, error) {
	a := &Agent{
		store:        store,
		log:          log,
		sessionKey:   cfg.Minecraft.SessionID,
		defaultTools: agentTools,
		instruction:  systemInstruction,
		replyMode:    cfg.Minecraft.ReplyMode,
		jobs:         make(chan Request, 64),
		pending:      make(map[string]*pendingRun),
		approvals:    approvals,
		runners:      make(map[string]*adk.Runner),
	}
	if a.replyMode == "" {
		a.replyMode = "broadcast"
	}
	if a.sessionKey == "" {
		a.sessionKey = "minecraft-main"
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
				sessionID: "mineagent-" + a.sessionKey,
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

	mcAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
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

	a.mcRunner = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           mcAgent,
		CheckPointStore: newCheckpointStore(store),
	})
	a.model = cm
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
	var decided <-chan struct{}
	if a.approvals != nil {
		decided = a.approvals.Decided()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-a.jobs:
			a.respond(ctx, req)
		case <-decided:
			a.drainOutcomes(ctx)
		}
	}
}

func (a *Agent) drainOutcomes(ctx context.Context) {
	for _, outcome := range a.approvals.DrainDecided() {
		a.resume(ctx, outcome)
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
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = a.sessionKey
	}
	rctx = tools.WithRequester(rctx, req.Player)
	rctx = tools.WithRequesterID(rctx, req.RequesterID)
	if req.MCRequester != "" {
		rctx = tools.WithMCRequester(rctx, req.MCRequester)
	}
	rctx = tools.WithSession(rctx, sessionKey)

	msgs, cutoff, err := a.history(sessionKey, req.TriggerMessageID)
	if err != nil {
		a.log.Error("agent history", "err", err)
		_ = req.Session.Reply(rctx, "抱歉，读取聊天记录失败。", req.Player)
		return
	}
	rctx = context.WithValue(rctx, historyCutoffKey{}, cutoff)

	cpID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	a.log.Info("agent run", "session", sessionKey, "player", req.Player, "query", req.Query, "messages", len(msgs), "trigger", req.TriggerMessageID, "checkpoint", cpID)

	start := time.Now()
	runner, runnerKey, runErr := a.runnerFor(rctx, req)
	if runErr != nil {
		a.log.Error("agent runner", "err", runErr)
		_ = req.Session.Reply(rctx, "抱歉，助手初始化失败。", req.Player)
		return
	}
	iter := runner.Run(rctx, msgs, adk.WithCheckPointID(cpID))
	replied := a.consume(rctx, req, sessionKey, runnerKey, cpID, iter)
	if replied {
		a.log.Info("agent replied", "session", sessionKey, "player", req.Player, "took", time.Since(start).Round(time.Millisecond).String())
	}
}

// runnerFor 按通道选 runner：MC 用预建的 mcRunner；QQ 按 (sessionKey,
// instruction, 工具名集合) 懒建并缓存。runner/agent 内无可变 run 状态，
// 同一会话复用是安全的；checkpoint 按 run 粒度存 sqlite，resume 时必须用
// 建它的那个 runner（见 resume，经 pendingRun.runnerKey 取回）。
func (a *Agent) runnerFor(ctx context.Context, req Request) (*adk.Runner, string, error) {
	if len(req.Tools) == 0 && req.SystemInstruction == "" {
		return a.mcRunner, "", nil
	}
	instruction := req.SystemInstruction
	if instruction == "" {
		instruction = a.instruction
	}
	agentTools := req.Tools
	if len(agentTools) == 0 {
		agentTools = a.defaultTools
	}
	names := make([]string, 0, len(agentTools))
	for _, tl := range agentTools {
		info, err := tl.Info(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("tool info: %w", err)
		}
		names = append(names, info.Name)
	}
	key := req.SessionKey + "\x00" + instruction + "\x00" + strings.Join(names, ",")
	a.mu.Lock()
	r, ok := a.runners[key]
	a.mu.Unlock()
	if ok {
		return r, key, nil
	}
	middlewares, err := a.buildMiddlewares(ctx, a.model)
	if err != nil {
		return nil, "", fmt.Errorf("init middlewares: %w", err)
	}
	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "mineagent-qq",
		Description: "QQ 聊天助手",
		Instruction: instruction,
		Model:       a.model,
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
		return nil, "", fmt.Errorf("init qq chat model agent: %w", err)
	}
	r = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		CheckPointStore: newCheckpointStore(a.store),
	})
	a.mu.Lock()
	a.runners[key] = r
	a.mu.Unlock()
	a.log.Info("qq runner created", "session", req.SessionKey, "tools", len(agentTools))
	return r, key, nil
}

func (a *Agent) consume(ctx context.Context, req Request, sessionKey, runnerKey, cpID string, iter *adk.AsyncIterator[*adk.AgentEvent]) bool {
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
			a.handleInterrupt(ctx, req, sessionKey, runnerKey, cpID, ev.Action.Interrupted)
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

func (a *Agent) handleInterrupt(ctx context.Context, req Request, sessionKey, runnerKey, cpID string, info *adk.InterruptInfo) {
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
			sessionKey:  sessionKey,
			player:      req.Player,
			requesterID: req.RequesterID,
			mcRequester: tools.MCRequesterFromContext(ctx),
			cpID:        cpID,
			runnerKey:   runnerKey,
			interruptID: ictx.ID,
			tool:        ai.Tool,
			cutoff:      historyCutoffFrom(ctx),
			tools:       req.Tools,
			instruction: req.SystemInstruction,
		}
		a.mu.Unlock()
		a.approvals.Notify(*ai)
		a.log.Info("approval requested", "session", sessionKey, "approvalId", ai.ApprovalID, "tool", ai.Tool, "requester", req.Player)
		notified = true
	}
	if !notified {
		a.log.Warn("interrupt without approval info", "session", sessionKey, "player", req.Player)
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
	if run.mcRequester != "" {
		rctx = tools.WithMCRequester(rctx, run.mcRequester)
	}
	rctx = tools.WithSession(rctx, run.sessionKey)
	rctx = context.WithValue(rctx, historyCutoffKey{}, run.cutoff)

	a.log.Info("resuming after approval",
		"session", run.sessionKey,
		"approvalId", out.ApprovalID,
		"tool", run.tool,
		"approved", out.Decision.Approved,
		"operator", out.Decision.Operator,
	)
	runner := a.mcRunner
	if run.runnerKey != "" {
		a.mu.Lock()
		r, ok := a.runners[run.runnerKey]
		a.mu.Unlock()
		if !ok {
			a.log.Error("resume runner gone", "approvalId", out.ApprovalID, "runnerKey", run.runnerKey)
			_ = run.session.Reply(rctx, "恢复执行失败：会话已过期，请重新提问。", a.target(run.player))
			return
		}
		runner = r
	}
	iter, err := runner.ResumeWithParams(rctx, run.cpID, &adk.ResumeParams{
		Targets: map[string]any{run.interruptID: &out.Decision},
	})
	if err != nil {
		a.log.Error("resume failed", "err", err, "checkpoint", run.cpID)
		_ = run.session.Reply(rctx, "恢复执行失败："+err.Error(), a.target(run.player))
		return
	}
	a.consume(rctx, Request{Session: run.session, SessionKey: run.sessionKey, Player: run.player, Tools: run.tools, SystemInstruction: run.instruction}, run.sessionKey, run.runnerKey, run.cpID, iter)
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

// history 只读属于 sessionKey 自己的消息+摘要，会话隔离就靠它。
// QQ 侧 AuthorKind 也是 "player"，展示时用 [QQ名] 前缀（调用方 Ingest 时拼好）。
func (a *Agent) history(sessionKey string, upToID int64) ([]*schema.Message, int64, error) {
	ctx := context.Background()
	var (
		summary *storage.Summary
		err     error
	)
	if upToID > 0 {
		summary, err = a.store.SummaryAtOrBefore(ctx, sessionKey, upToID)
	} else {
		summary, err = a.store.LatestSummary(ctx, sessionKey)
	}
	if err != nil {
		return nil, 0, err
	}
	var cutoff int64
	msgs := make([]*schema.Message, 0, 64)
	if summary != nil {
		cutoff = summary.UpToMessageID
		msgs = append(msgs, schema.UserMessage("[之前聊天的摘要] "+summary.Text))
	}
	maxID := upToID
	if maxID <= 0 {
		maxID = math.MaxInt64
	}
	recent, err := a.store.MessagesBetween(ctx, sessionKey, cutoff, maxID, 200)
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
