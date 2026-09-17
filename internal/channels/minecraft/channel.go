package minecraft

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"log/slog"

	"mineagent/internal/protocol"
	"mineagent/internal/storage"
	"mineagent/internal/ws"
)

var ErrOffline = errors.New("minecraft backend not connected")

type Channel struct {
	log *slog.Logger

	mu   sync.RWMutex
	conn *ws.Conn
}

func NewChannel(log *slog.Logger) *Channel {
	return &Channel{log: log}
}

func (c *Channel) Name() string { return "minecraft" }

func (c *Channel) Attach(conn *ws.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.log.Info("minecraft channel attached", "remote", conn.Remote())
}

func (c *Channel) Detach(conn *ws.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Channel) Attached() *ws.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

func (c *Channel) Send(_ context.Context, msg storage.Message) error {
	return c.SendProtocol(protocol.TypeAgentMessage, protocol.AgentMessage{
		Text:    msg.Text,
		Target:  msg.Target,
		ReplyTo: strconv.FormatInt(msg.ID, 10),
	})
}

func (c *Channel) SendProtocol(typ string, data any) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return ErrOffline
	}
	return conn.Send(typ, data)
}
