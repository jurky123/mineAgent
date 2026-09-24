package aibot

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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
// 实现注记（踩坑记录）：
//   - 企微网关 Wwebsvr 对 Sec-WebSocket-Key 大小写敏感（必须精确 "Sec-WebSocket-Key"），
//     Go 标准库 http.Header.Set 会规范成 "Sec-Websocket-Key" 导致握手 404。
//     所以这里用 gorilla/websocket（它直接写 map，保留精确大小写），
//     不要换回 coder/websocket 的 Dial。
//   - gorilla 的 Conn 不支持并发写，所有写操作统一过 writeMu。
type Client struct {
	botID  string
	secret string
	log    *slog.Logger

	onMessage func(InboundMessage)

	mu      sync.Mutex
	conn    *websocket.Conn
	waiters map[string]chan envelope
	writeMu sync.Mutex

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

// dialer 保持 ALPN 只报 http/1.1（企微网关对 h2 的升级请求也不友好）。
var dialer = websocket.Dialer{
	HandshakeTimeout: 10 * time.Second,
	TLSClientConfig:  &tls.Config{NextProtos: []string{"http/1.1"}},
	NetDial:          (&net.Dialer{Timeout: 10 * time.Second}).Dial,
	ReadBufferSize:   1 << 16,
	WriteBufferSize:  1 << 16,
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

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
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

	// 1. 订阅（失败退出重来；订阅有频率保护，不在这里反复重试）。
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

	// 2. 心跳：30s 一次 ping（协议建议值）。
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
				if _, err := c.call(hbCtx, conn, map[string]any{
					"cmd":     cmdPing,
					"headers": headers{ReqID: newReqID()},
				}); err != nil {
					c.log.Warn("aibot ping failed", "err", err)
					_ = conn.Close()
					return
				}
			}
		}
	}()

	// 3. 收包循环。
	for {
		_, raw, err := conn.ReadMessage()
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
				_ = conn.Close()
			}
		case eventEnterChat:
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

// call 发一条命令并等应答（10 秒超时）。所有写操作都从这里走（gorilla 不支持并发写）。
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
	c.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = conn.WriteMessage(websocket.TextMessage, raw)
	c.writeMu.Unlock()
	if err != nil {
		return envelope{}, err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp, nil
	case <-timer.C:
		return envelope{}, fmt.Errorf("response timeout for %s", reqID)
	case <-ctx.Done():
		return envelope{}, ctx.Err()
	}
}

// Respond 被动回复：透传触发回调的 req_id（24 小时窗口内有效）。
func (c *Client) Respond(ctx context.Context, reqID, content string) error {
	conn := c.current()
	if conn == nil {
		return errors.New("aibot not connected")
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
	conn := c.current()
	if conn == nil {
		return errors.New("aibot not connected")
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

func (c *Client) current() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
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
