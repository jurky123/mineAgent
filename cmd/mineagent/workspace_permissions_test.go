package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/config"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

func TestWorkspaceAdminPermissionsStayWithinChannel(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	channels := []string{"qq", "wecom", "aibot", "wechat", "web"}
	admins := make(map[string][]string)
	for _, ch := range channels {
		admins[ch] = []string{ch + "Admin"}
	}
	ws := tools.NewWorkspace(config.Workspace{Root: t.TempDir()}, admins, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	inner := ws.Tools()[0]
	for _, ch := range channels {
		for _, idChannel := range channels {
			t.Run(ch+"/"+idChannel, func(t *testing.T) {
				id := idChannel + "Admin"
				var gate tool.InvokableTool
				prefix := "wecom:"
				switch ch {
				case "qq":
					prefix = "qq:"
					gate = &qqToolGate{inner: inner, ws: ws, store: store}
				case "web":
					prefix = "web:"
					gate = &webToolGate{inner: inner, ws: ws, store: store}
				default:
					gate = &wecomToolGate{inner: inner, channel: ch, ws: ws, store: store}
				}
				ctx := tools.WithRequester(context.Background(), prefix+id)
				out, err := gate.InvokableRun(ctx, `{}`)
				if err != nil {
					t.Fatal(err)
				}
				allowed := strings.Contains(out, `"entries"`)
				if allowed != (ch == idChannel) {
					t.Fatalf("channel %s / identity %s: allowed=%v, output=%s", ch, id, allowed, out)
				}
			})
		}
	}
	if ws.IsAdmin("unknown", "webAdmin") {
		t.Fatal("unknown channel inherited permissions")
	}
}
