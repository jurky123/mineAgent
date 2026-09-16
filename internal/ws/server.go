package ws

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/version"
)

type MessageHandler func(ctx context.Context, c *Conn, env *protocol.Envelope)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	handler MessageHandler

	onConnect    func(*Conn)
	onDisconnect func(*Conn)

	httpSrv *http.Server

	pingInterval     time.Duration
	readTimeout      time.Duration
	writeTimeout     time.Duration
	handshakeTimeout time.Duration

	mu    sync.Mutex
	conns map[*Conn]struct{}
}

func New(cfg config.Config, log *slog.Logger, handler MessageHandler) *Server {
	s := &Server{
		cfg:              cfg,
		log:              log,
		handler:          handler,
		pingInterval:     20 * time.Second,
		readTimeout:      60 * time.Second,
		writeTimeout:     10 * time.Second,
		handshakeTimeout: 5 * time.Second,
		conns:            make(map[*Conn]struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/ws", s.handleWS)
	s.httpSrv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) OnConnect(fn func(*Conn)) {
	s.onConnect = fn
}

func (s *Server) OnDisconnect(fn func(*Conn)) {
	s.onDisconnect = fn
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.log.Info("websocket listening", "addr", ln.Addr().String(), "token", s.cfg.Token != "")
	return s.Serve(ln)
}

func (s *Server) Serve(ln net.Listener) error {
	err := s.httpSrv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for c := range s.conns {
		c.close(websocket.StatusGoingAway, "server shutting down")
	}
	s.mu.Unlock()
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) addConn(c *Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeConn(c *Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	sock, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("websocket accept failed", "remote", r.RemoteAddr, "err", err)
		return
	}
	sock.SetReadLimit(1 << 20)

	c := &Conn{server: s, sock: sock, remote: r.RemoteAddr, lastSeen: time.Now()}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer c.close(websocket.StatusInternalError, "handler exit")

	hello, err := c.expectHello(ctx)
	if err != nil {
		s.log.Warn("handshake failed", "remote", r.RemoteAddr, "err", err)
		return
	}
	c.role = hello.Role
	if c.role == "" {
		c.role = protocol.RoleMinecraft
	}
	c.plugin = hello.Plugin
	c.pluginVersion = hello.PluginVersion
	c.serverVersion = hello.ServerVersion

	if err := c.Send(protocol.TypeHelloAck, protocol.HelloAck{
		Protocol:   protocol.Version,
		Backend:    "mineagent",
		BackendVer: version.Version,
		Time:       time.Now().UnixMilli(),
	}); err != nil {
		s.log.Warn("hello_ack failed", "remote", c.remote, "err", err)
		return
	}

	s.addConn(c)
	defer s.removeConn(c)
	if s.onConnect != nil {
		s.onConnect(c)
	}
	defer func() {
		if s.onDisconnect != nil {
			s.onDisconnect(c)
		}
	}()
	s.log.Info("plugin connected",
		"remote", c.remote,
		"role", c.role,
		"plugin", c.plugin,
		"pluginVersion", c.pluginVersion,
		"serverVersion", c.serverVersion,
	)

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go c.pingLoop(pingCtx)

	for {
		env, err := c.readEnvelope(ctx)
		if err != nil {
			if isNormalClose(err) {
				s.log.Info("plugin disconnected", "remote", c.remote, "role", c.role)
			} else {
				s.log.Warn("connection broken", "remote", c.remote, "role", c.role, "err", err)
			}
			return
		}
		switch env.Type {
		case protocol.TypePong:
			c.touch()
		case protocol.TypePing:
			_ = c.Send(protocol.TypePong, nil)
		case protocol.TypeHello:
			s.log.Warn("duplicate hello ignored", "remote", c.remote)
		default:
			if s.handler != nil {
				s.handler(ctx, c, env)
			}
		}
	}
}

type Conn struct {
	server        *Server
	sock          *websocket.Conn
	remote        string
	role          string
	plugin        string
	pluginVersion string
	serverVersion string

	writeMu   sync.Mutex
	mu        sync.Mutex
	lastSeen  time.Time
	closeOnce sync.Once
}

func (c *Conn) Remote() string        { return c.remote }
func (c *Conn) RemoteRole() string    { return c.role }
func (c *Conn) Plugin() string        { return c.plugin }
func (c *Conn) ServerVersion() string { return c.serverVersion }

func (c *Conn) Send(typ string, data any) error {
	b, err := protocol.Marshal(typ, data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.server.writeTimeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.sock.Write(ctx, websocket.MessageText, b)
}

func (c *Conn) touch() {
	c.mu.Lock()
	c.lastSeen = time.Now()
	c.mu.Unlock()
}

func (c *Conn) idleFor() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastSeen)
}

func (c *Conn) readEnvelope(ctx context.Context) (*protocol.Envelope, error) {
	rctx, cancel := context.WithTimeout(ctx, c.server.readTimeout)
	defer cancel()
	_, data, err := c.sock.Read(rctx)
	if err != nil {
		return nil, err
	}
	c.touch()
	return protocol.Unmarshal(data)
}

func (c *Conn) expectHello(ctx context.Context) (protocol.Hello, error) {
	var hello protocol.Hello
	rctx, cancel := context.WithTimeout(ctx, c.server.handshakeTimeout)
	defer cancel()
	_, data, err := c.sock.Read(rctx)
	if err != nil {
		return hello, err
	}
	c.touch()
	env, err := protocol.Unmarshal(data)
	if err != nil {
		c.close(websocket.StatusPolicyViolation, "malformed hello")
		return hello, err
	}
	if env.Type != protocol.TypeHello {
		err := fmt.Errorf("expected %s, got %s", protocol.TypeHello, env.Type)
		c.close(websocket.StatusPolicyViolation, "hello required first")
		return hello, err
	}
	if err := env.Decode(&hello); err != nil {
		c.close(websocket.StatusPolicyViolation, "malformed hello payload")
		return hello, err
	}
	if hello.Protocol != protocol.Version {
		err := fmt.Errorf("unsupported protocol %d", hello.Protocol)
		c.close(websocket.StatusPolicyViolation, "unsupported protocol version")
		return hello, err
	}
	if token := c.server.cfg.Token; token != "" {
		if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) != 1 {
			err := errors.New("invalid token")
			c.close(websocket.StatusPolicyViolation, "invalid token")
			return hello, err
		}
	}
	return hello, nil
}

func (c *Conn) pingLoop(ctx context.Context) {
	t := time.NewTicker(c.server.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if c.idleFor() > 2*c.server.readTimeout {
				c.close(websocket.StatusPolicyViolation, "heartbeat timeout")
				return
			}
			if err := c.Send(protocol.TypePing, nil); err != nil {
				return
			}
		}
	}
}

func (c *Conn) close(status websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		_ = c.sock.Close(status, reason)
	})
}

func isNormalClose(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	default:
		return false
	}
}
