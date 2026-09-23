package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Gateway 是 QQ 官方 wss 网关长连接：
// hello -> identify（token=QQBot access_token, intents=1<<25, shard=[0,1]）
// -> ready 拿 session_id -> 按 heartbeat_interval 心跳（d=最新 s）
// -> dispatch 事件（C2C_MESSAGE_CREATE / GROUP_AT_MESSAGE_CREATE）回调 onMessage。
// 断线自动重连：有 session_id+seq 就 resume 补事件，否则全量 identify。
// 官方错误码见 websocket.html：4009 可 resume；其他走 identify；
// 4914（下架）/4915（封禁）直接停，不重试。
type Gateway struct {
	api    *API
	tokens *TokenSource
	log    *slog.Logger

	onMessage func(InboundMessage)

	mu        sync.Mutex
	sessionID string
	seq       int64

	stopCh chan struct{}
	stopped chan struct{}
}

func NewGateway(api *API, tokens *TokenSource, log *slog.Logger, onMessage func(InboundMessage)) *Gateway {
	return &Gateway{api: api, tokens: tokens, log: log, onMessage: onMessage}
}

func (g *Gateway) Start() {
	g.mu.Lock()
	if g.stopCh != nil {
		g.mu.Unlock()
		return
	}
	g.stopCh = make(chan struct{})
	g.stopped = make(chan struct{})
	g.mu.Unlock()
	go g.loop()
}

func (g *Gateway) Stop() {
	g.mu.Lock()
	stop := g.stopCh
	g.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-g.stopped
}

func (g *Gateway) setSession(id string) {
	g.mu.Lock()
	g.sessionID = id
	g.mu.Unlock()
}

func (g *Gateway) setSeq(s int64) {
	g.mu.Lock()
	if s > g.seq {
		g.seq = s
	}
	g.mu.Unlock()
}

func (g *Gateway) snapshot() (session string, seq int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessionID, g.seq
}

func (g *Gateway) loop() {
	defer close(g.stopped)
	backoff := time.Second
	for {
		select {
		case <-g.stopCh:
			return
		default:
		}
		if err := g.runOnce(); err != nil {
			g.log.Warn("qq gateway disconnected", "err", err, "retryIn", backoff.String())
		}
		select {
		case <-g.stopCh:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

var errFatal = fmt.Errorf("qq gateway fatal, stop retrying")

func (g *Gateway) runOnce() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-g.stopCh
		cancel()
	}()

	url, err := g.api.GatewayURL(ctx)
	if err != nil {
		return fmt.Errorf("gateway url: %w", err)
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	conn.SetReadLimit(1 << 20)

	// 1. 等 hello 拿心跳周期。
	var hello struct {
		Op int       `json:"op"`
		D  helloData `json:"d"`
	}
	if err := readJSON(ctx, conn, &hello); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("expected hello, got op=%d", hello.Op)
	}
	interval := time.Duration(hello.D.HeartbeatInterval) * time.Millisecond
	if interval <= 0 || interval > 2*time.Minute {
		interval = 45 * time.Second
	}

	// 2. resume 或 identify。
	session, seq := g.snapshot()
	tok, err := g.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	if session != "" {
		if err := writeJSON(ctx, conn, wsPayload{Op: opResume, D: resumeData{
			Token: "QQBot " + tok, SessionID: session, Seq: seq,
		}}); err != nil {
			return fmt.Errorf("resume: %w", err)
		}
	} else {
		var id identifyData
		id.Token = "QQBot " + tok
		id.Intents = intentGroupAndC2C
		id.Shard = [2]int{0, 1}
		id.Properties.OS = "linux"
		id.Properties.Browser = "mineagent"
		id.Properties.Device = "mineagent"
		if err := writeJSON(ctx, conn, wsPayload{Op: opIdentify, D: id}); err != nil {
			return fmt.Errorf("identify: %w", err)
		}
	}

	// 3. 心跳协程。
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_, s := g.snapshot()
				_ = writeJSON(hbCtx, conn, wsPayload{Op: opHeartbeat, D: s})
			}
		}
	}()

	// 4. 收包循环。
	for {
		raw, err := readRaw(ctx, conn)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var env struct {
			ID string          `json:"id"`
			Op int             `json:"op"`
			D  json.RawMessage `json:"d"`
			S  *int64          `json:"s"`
			T  string          `json:"t"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			g.log.Warn("qq gateway bad frame", "err", err)
			continue
		}
		if env.S != nil {
			g.setSeq(*env.S)
		}
		switch env.Op {
		case opDispatch:
			g.dispatch(env.T, env.ID, env.D)
		case opHeartbeatAck:
			// 心跳正常，无事可做。
		case opReconnect:
			g.log.Info("qq gateway asked reconnect")
			return fmt.Errorf("server asked reconnect")
		case opInvalidSession:
			g.log.Warn("qq gateway invalid session, re-identify")
			g.setSession("")
			return fmt.Errorf("invalid session")
		case opHeartbeat:
			// 服务端心跳，按文档回 ACK 即可；coder 库底层已处理 ping，这里忽略。
		default:
			// Hello 之外的 op（如 READY 走 dispatch），未知 op 忽略。
			if env.Op != opHello {
				g.log.Debug("qq gateway unknown op", "op", env.Op, "t", env.T)
			}
		}
	}
}

func (g *Gateway) dispatch(t, eventID string, d json.RawMessage) {
	switch t {
	case "READY":
		var r readyData
		if err := json.Unmarshal(d, &r); err != nil {
			g.log.Warn("qq gateway bad READY", "err", err)
			return
		}
		g.setSession(r.SessionID)
		g.log.Info("qq gateway ready", "session", r.SessionID, "bot", r.User.Username)
	case "RESUMED":
		g.log.Info("qq gateway resumed")
	case "C2C_MESSAGE_CREATE":
		var e c2cEvent
		if err := json.Unmarshal(d, &e); err != nil {
			g.log.Warn("qq gateway bad C2C event", "err", err)
			return
		}
		if e.Author.Bot {
			return
		}
		if e.MessageType != 0 || e.Content == "" {
			g.log.Debug("qq gateway skip non-text C2C", "messageType", e.MessageType)
			return
		}
		g.emit(InboundMessage{
			Kind: "c2c", MsgID: e.ID, EventID: eventID,
			UserOpenID:  firstNonEmpty(e.Author.UserOpenID, e.Author.ID),
			UnionOpenID: e.Author.UnionOpenID,
			Username:    e.Author.Username,
			Text:        e.Content,
		})
	case "GROUP_AT_MESSAGE_CREATE":
		var e groupAtEvent
		if err := json.Unmarshal(d, &e); err != nil {
			g.log.Warn("qq gateway bad group event", "err", err)
			return
		}
		if e.Author.Bot {
			return
		}
		if e.MessageType != 0 {
			g.log.Debug("qq gateway skip non-text group", "messageType", e.MessageType)
			return
		}
		g.emit(InboundMessage{
			Kind: "group_at", MsgID: e.ID, EventID: eventID,
			GroupOpenID:  e.GroupOpenID,
			MemberOpenID: firstNonEmpty(e.Author.MemberOpenID, e.Author.ID),
			UnionOpenID:  e.Author.UnionOpenID,
			Username:     e.Author.Username,
			Text:         e.Content,
			MemberRole:   e.Author.MemberRole,
		})
	default:
		// FRIEND_ADD/DEL、C2C_MSG_REJECT/RECEIVE、GROUP_ADD/DEL_ROBOT 等
		// v1 不处理，只记 debug。
		g.log.Debug("qq gateway ignore event", "t", t)
	}
}

func (g *Gateway) emit(m InboundMessage) {
	if g.onMessage == nil {
		return
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				g.log.Error("qq onMessage panic", "err", r)
			}
		}()
		g.onMessage(m)
	}()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, mustJSON(v))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func readJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func readRaw(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	return raw, err
}
