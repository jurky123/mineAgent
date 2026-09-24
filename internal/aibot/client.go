package aibot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// newReqID 生成请求唯一标识（回复命令要透传回调的 req_id，主动命令自己生成）。
func newReqID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(b[:])
}

// Client 是企微智能机器人长连接客户端：
// 连接 -> aibot_subscribe 订阅 -> 30s ping 保活 -> 收 msg/event 回调 -> 按 req_id 回包。
// 断线自动重连（指数退避），收到 disconnected_event（被新连接踢掉）也重连。
//
// 回复关联约定：被动回复必须带触发回调的 req_id（InboundMessage.ReqID），
// 因此 channel 把它编进 target 字符串；req_id 在长连接内有效（回复窗口 24h），
// 失效或需要主动推送时降级用 aibot_send_msg（chatid + chat_type）。
type Client struct {
	botID  string
	secret string
	log    *slog.Logger

	onMessage func(InboundMessage)

	mu      sync.Mutex
	conn    *websocket.Conn
	waiters map[string]chan envelope
	seq     atomic.Uint64

	stopCh  chan struct{}
	stopped chan struct{}
}

func NewClient(botID, secret string, log *slog.Logger, onMessage func(InboundMessage)) *Client {
	return &Client{
		botID:     botID,
		secret:    secret,
		log:       log,
		onMessage: onMessage,
		waiters:   make(map[string]chan envelope),
	}
}

func (c *Client) Start() {
	c.mu.Lock()
	if c.stopCh != nil {
		c.mu.Unlock()
		return
	}
	c.stopCh = make(chan struct{})
	c.stopped = make(chan struct{})
	c.mu.Unlock()
	go c.loop()
}

func (c *Client) Stop() {
	c.mu.Lock()
	stop := c.stopCh
	c.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-c.stopped
}

// Connected 供 /status：当前是否有已订阅的连接。
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

func (c *Client) loop() {
	defer close(c.stopped)
	backoff := time.Second
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}
		err := c.runOnce()
		if err != nil {
			c.log.Warn("aibot disconnected", "err", err, "retryIn", backoff.String())
		}
		select {
		case <-c.stopCh:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

func (c *Client) runOnce() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-c.stopCh
		cancel()
	}()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	conn.SetReadLimit(4 << 20)

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	// 1. 订阅。失败就退出重来（订阅有频率保护，不要在这里反复重试）。
	resp, err := c.call(ctx, conn, map[string]any{
		"cmd":     cmdSubscribe,
		"headers": headers{ReqID: newReqID()},
		"body": map[string]string{
			"bot_id": c.botID,
			"secret": c.secret,
		},
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("subscribe rejected: errcode=%d errmsg=%s", resp.ErrCode, resp.ErrMsg)
	}
	c.log.Info("aibot subscribed", "botId", c.botID)

	// 2. 心跳协程：30s 一次 ping（协议建议值）。
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_, err := c.call(hbCtx, conn, map[string]any{
					"cmd":     cmdPing,
					"headers": headers{ReqID: newReqID()},
				})
				if err != nil {
					c.log.Warn("aibot ping failed", "err", err)
					conn.Close(websocket.StatusPolicyViolation, "ping timeout")
					return
				}
			}
		}
	}()

	// 3. 收包循环。
	for {
		raw, err := readRaw(ctx, conn)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		c.handleFrame(raw)
	}
}

// handleFrame 分发一帧：带 cmd 的按回调处理；纯响应按 req_id 交给等待者。
func (c *Client) handleFrame(raw []byte) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		c.log.Warn("aibot bad frame", "err", err)
		return
	}
	switch env.Cmd {
	case cmdMsgCallback:
		var body msgCallback
		if err := json.Unmarshal(env.Body, &body); err != nil {
			c.log.Warn("aibot bad msg callback", "err", err)
			return
		}
		if body.MsgType != "text" || body.Text == nil || body.Text.Content == "" {
			c.log.Debug("aibot skip non-text", "type", body.MsgType, "from", body.From.UserID)
			return
		}
		kind := "c2c"
		if body.ChatType == "group" {
			kind = "group"
		}
		c.emit(InboundMessage{
			ReqID:  env.Headers.ReqID,
			MsgID:  body.MsgID,
			Kind:   kind,
			UserID: body.From.UserID,
			ChatID: body.ChatID,
			Text:   body.Text.Content,
		})
		return
	case cmdEventCallback:
		var body eventCallback
		if err := json.Unmarshal(env.Body, &body); err != nil {
			c.log.Warn("aibot bad event callback", "err", err)
			return
		}
		switch body.Event.EventType {
		case eventDisconnected:
			// 被新连接踢掉：断开当前连接触发重连（单连接原则）。
			c.log.Warn("aibot kicked by new connection, reconnecting")
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()
			if conn != nil {
				conn.Close(websocket.StatusGoingAway, "kicked")
			}
		case eventEnterChat:
			// 进入会话事件：v1 不回欢迎语（保持安静），只记日志。
			c.log.Info("aibot enter_chat", "userid", userIDOf(body.From))
		default:
			c.log.Debug("aibot event ignored", "type", body.Event.EventType)
		}
		return
	}

	// 响应帧：按 req_id 唤醒等待者（call 里注册）。
	if env.Headers.ReqID != "" {
		c.mu.Lock()
		ch, ok := c.waiters[env.Headers.ReqID]
		if ok {
			delete(c.waiters, env.Headers.ReqID)
		}
		c.mu.Unlock()
		if ok {
			select {
			case ch <- env:
			default:
			}
		}
	}
}

func userIDOf(from *struct {
	UserID string `json:"userid"`
}) string {
	if from == nil {
		return ""
	}
	return from.UserID
}

// call 发一条命令并等应答（10 秒超时）。
func (c *Client) call(ctx context.Context, conn *websocket.Conn, msg map[string]any) (envelope, error) {
	env, _ := msg["headers"].(headers)
	reqID := env.ReqID
	if reqID == "" {
		reqID = newReqID()
		msg["headers"] = headers{ReqID: reqID}
	}
	ch := make(chan envelope, 1)
	c.mu.Lock()
	c.waiters[reqID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, reqID)
		c.mu.Unlock()
	}()

	raw, err := json.Marshal(msg)
	if err != nil {
		return envelope{}, err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, raw); err != nil {
		return envelope{}, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-wctx.Done():
		return envelope{}, fmt.Errorf("response timeout for %s", reqID)
	case <-ctx.Done():
		return envelope{}, ctx.Err()
	}
}

// Respond 被动回复：透传触发回调的 req_id（24 小时窗口内有效）。
func (c *Client) Respond(ctx context.Context, reqID, content string) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("aibot not connected")
	}
	resp, err := c.call(ctx, conn, respondEnvelope(reqID, content))
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("aibot respond failed: errcode=%d errmsg=%s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// SendActive 主动推送（被动窗口失效或定时提醒用）。
// 前置条件：该会话里用户先给机器人发过消息，否则企微会拒。
func (c *Client) SendActive(ctx context.Context, chatID string, chatType uint32, content string) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("aibot not connected")
	}
	resp, err := c.call(ctx, conn, sendEnvelope(chatID, chatType, content))
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("aibot send failed: errcode=%d errmsg=%s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

func readRaw(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, raw, err := conn.Read(rctx)
	return raw, err
}

func (c *Client) emit(m InboundMessage) {
	if c.onMessage == nil {
		return
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				c.log.Error("aibot onMessage panic", "err", r)
			}
		}()
		c.onMessage(m)
	}()
}
