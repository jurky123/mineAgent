package wechat

import (
	"context"
	"log/slog"
	"time"
)

// Monitor 是收消息长轮询循环：getupdates（35s 长轮询）-> 回调 -> 持久化游标。
// 出错退避重试；errcode=-14（会话超时/凭证失效）会打 ERROR 提示重新扫码登录。
type Monitor struct {
	cli   *Client
	state *State
	path  string
	log   *slog.Logger

	onMessage func(WeixinMessage)

	stopCh  chan struct{}
	stopped chan struct{}
	// 状态写盘节流：每收到消息或每 10s 存一次，避免频繁写文件。
	dirty bool
}

func NewMonitor(cli *Client, state *State, statePath string, log *slog.Logger, onMessage func(WeixinMessage)) *Monitor {
	return &Monitor{cli: cli, state: state, path: statePath, log: log, onMessage: onMessage}
}

func (m *Monitor) Start() {
	m.stopCh = make(chan struct{})
	m.stopped = make(chan struct{})
	go m.loop()
}

func (m *Monitor) Stop() {
	if m.stopCh == nil {
		return
	}
	close(m.stopCh)
	<-m.stopped
}

func (m *Monitor) loop() {
	defer close(m.stopped)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-m.stopCh
		cancel()
	}()

	if err := m.cli.NotifyStart(ctx); err != nil {
		m.log.Warn("wechat notifyStart failed", "err", err)
	}
	defer func() {
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if err := m.cli.NotifyStop(sctx); err != nil {
			m.log.Warn("wechat notifyStop failed", "err", err)
		}
	}()

	backoff := time.Second
	saveTicker := time.NewTicker(10 * time.Second)
	defer saveTicker.Stop()
	for {
		select {
		case <-m.stopCh:
			m.save()
			return
		case <-saveTicker.C:
			m.save()
		default:
		}

		resp, err := m.cli.GetUpdates(ctx, m.state.GetUpdatesBuf)
		if err != nil {
			m.log.Warn("wechat getupdates failed", "err", err, "retryIn", backoff.String())
			if !m.sleep(backoff) {
				return
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		// errcode -14 = 会话超时：token 失效，需重新扫码。
		if resp.ErrCode == -14 {
			m.log.Error("wechat session expired (errcode=-14)，需要重新运行 mineagent --wechat-login 扫码登录", "errmsg", resp.ErrMsg)
			if !m.sleep(30 * time.Second) {
				return
			}
			continue
		}
		if resp.GetUpdatesBuf != "" {
			m.state.GetUpdatesBuf = resp.GetUpdatesBuf
			m.dirty = true
		}
		for i := range resp.Msgs {
			m.handle(resp.Msgs[i])
		}
	}
}

func (m *Monitor) sleep(d time.Duration) bool {
	select {
	case <-m.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func (m *Monitor) handle(msg WeixinMessage) {
	// 只处理用户消息；记录 context_token（回复必须透传）。
	if msg.MessageType != MsgTypeUser {
		return
	}
	if msg.FromUserID == "" {
		return
	}
	if msg.ContextToken != "" {
		m.state.ContextTokens[msg.FromUserID] = msg.ContextToken
		m.dirty = true
	}
	text := ""
	for _, it := range msg.ItemList {
		if it.Type == ItemTypeText && it.TextItem != nil && it.TextItem.Text != "" {
			text += it.TextItem.Text
		}
	}
	if text == "" {
		// 非文本（图片/语音/文件）v1 跳过，日志留痕。
		m.log.Debug("wechat skip non-text message", "from", msg.FromUserID, "items", len(msg.ItemList))
		return
	}
	if m.onMessage != nil {
		m.onMessage(msg)
	}
}

// save 落盘（写失败只记日志，不中断）。
func (m *Monitor) save() {
	if !m.dirty {
		return
	}
	if err := SaveState(m.path, m.state); err != nil {
		m.log.Warn("wechat save state failed", "err", err)
		return
	}
	m.dirty = false
}

// SaveNow 供 channel 主动保存（如登记了新的 context_token）。
func (m *Monitor) SaveNow() { m.save() }
