package ws

import (
	"context"
	"io"
	"log/slog"
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBodyRoleRequiresIndependentTokenAndExplicitEnablement(t *testing.T) {
	for _, tc := range []struct {
		name, role, token string
		roles             []string
		enabled           bool
		valid             bool
	}{
		{"valid body", protocol.RoleBody, strings.Repeat("b", 32), []string{protocol.RoleMinecraft, protocol.RoleBody}, true, true},
		{"paper token cannot authenticate body", protocol.RoleBody, strings.Repeat("p", 32), []string{protocol.RoleMinecraft, protocol.RoleBody}, true, false},
		{"body token cannot authenticate paper", protocol.RoleMinecraft, strings.Repeat("b", 32), []string{protocol.RoleMinecraft, protocol.RoleBody}, true, false},
		{"implicit allow all does not enable body", protocol.RoleBody, strings.Repeat("b", 32), nil, true, false},
		{"feature off", protocol.RoleBody, strings.Repeat("b", 32), []string{protocol.RoleBody}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Token = strings.Repeat("p", 32)
			cfg.Body.Enabled = tc.enabled
			cfg.Body.Mode = "observe"
			cfg.Body.Token = strings.Repeat("b", 32)
			cfg.AllowedRoles = tc.roles
			s := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
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
			c := dial(t, "ws://"+ln.Addr().String()+"/ws")
			send(t, c, protocol.TypeHello, protocol.Hello{Protocol: 1, Role: tc.role, Token: tc.token, BodyID: cfg.Body.ID, BodyName: cfg.Body.Name, BodyProtocolVersion: 1, TargetMcVersion: "26.2"})
			if tc.valid {
				if recv(t, c, time.Second).Type != protocol.TypeHelloAck {
					t.Fatal("missing acknowledgement")
				}
			} else {
				expectClosed(t, c)
			}
		})
	}
}
