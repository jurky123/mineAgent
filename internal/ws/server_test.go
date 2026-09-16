package ws

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/coder/websocket"

	"mineagent/internal/config"
	"mineagent/internal/protocol"
)

func newTestServer(t *testing.T, token string) (*Server, string, <-chan *protocol.Envelope) {
	t.Helper()
	cfg := config.Default()
	cfg.Token = token
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	msgs := make(chan *protocol.Envelope, 16)
	s := New(cfg, log, func(_ context.Context, _ *Conn, env *protocol.Envelope) {
		msgs <- env
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, "ws://" + ln.Addr().String() + "/ws", msgs
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func send(t *testing.T, c *websocket.Conn, typ string, data any) {
	t.Helper()
	b, err := protocol.Marshal(typ, data)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn, timeout time.Duration) *protocol.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := protocol.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("expected connection to be closed")
	}
}

func TestHandshakeAndDispatch(t *testing.T) {
	_, url, msgs := newTestServer(t, "secret")
	c := dial(t, url)

	send(t, c, protocol.TypeHello, protocol.Hello{
		Protocol:      protocol.Version,
		Role:          protocol.RoleMinecraft,
		Plugin:        "MineAgent",
		PluginVersion: "0.1.0",
		Token:         "secret",
	})
	ack := recv(t, c, 2*time.Second)
	if ack.Type != protocol.TypeHelloAck {
		t.Fatalf("type = %q, want %q", ack.Type, protocol.TypeHelloAck)
	}
	var ha protocol.HelloAck
	if err := ack.Decode(&ha); err != nil {
		t.Fatal(err)
	}
	if ha.Protocol != protocol.Version {
		t.Fatalf("ack protocol = %d", ha.Protocol)
	}

	send(t, c, protocol.TypeChatMessage, protocol.ChatMessage{Player: "Steve", Message: "hi"})
	select {
	case env := <-msgs:
		if env.Type != protocol.TypeChatMessage {
			t.Fatalf("handler got %q", env.Type)
		}
		var cm protocol.ChatMessage
		if err := env.Decode(&cm); err != nil {
			t.Fatal(err)
		}
		if cm.Player != "Steve" || cm.Message != "hi" {
			t.Fatalf("got %+v", cm)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called")
	}
}

func TestServerSendsPing(t *testing.T) {
	s, url, _ := newTestServer(t, "")
	s.pingInterval = 50 * time.Millisecond
	c := dial(t, url)

	send(t, c, protocol.TypeHello, protocol.Hello{Protocol: protocol.Version, Role: protocol.RoleMinecraft, Token: ""})
	if ack := recv(t, c, 2*time.Second); ack.Type != protocol.TypeHelloAck {
		t.Fatalf("type = %q", ack.Type)
	}
	if env := recv(t, c, 2*time.Second); env.Type != protocol.TypePing {
		t.Fatalf("type = %q, want %q", env.Type, protocol.TypePing)
	}
	send(t, c, protocol.TypePong, nil)
}

func TestRejectsBadToken(t *testing.T) {
	_, url, _ := newTestServer(t, "secret")
	c := dial(t, url)
	send(t, c, protocol.TypeHello, protocol.Hello{Protocol: protocol.Version, Role: protocol.RoleMinecraft, Token: "wrong"})
	expectClosed(t, c)
}

func TestRequiresHelloFirst(t *testing.T) {
	_, url, _ := newTestServer(t, "")
	c := dial(t, url)
	send(t, c, protocol.TypeChatMessage, protocol.ChatMessage{Player: "Steve", Message: "hi"})
	expectClosed(t, c)
}

func TestRejectsProtocolMismatch(t *testing.T) {
	_, url, _ := newTestServer(t, "")
	c := dial(t, url)
	send(t, c, protocol.TypeHello, protocol.Hello{Protocol: protocol.Version + 1, Role: protocol.RoleMinecraft})
	expectClosed(t, c)
}
