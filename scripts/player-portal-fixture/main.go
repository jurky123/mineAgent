// Isolated same-origin integration fixture. Uses disposable databases and fake
// Node/Paper peers, never production configuration or a Minecraft connection.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mineagent/aiplayer"
	"mineagent/internal/account"
	"mineagent/internal/agent"
	"mineagent/internal/body"
	"mineagent/internal/bodyui"
	"mineagent/internal/config"
	"mineagent/internal/portal"
	"mineagent/internal/protocol"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

type peer struct{ send func(string, any) error }

func (p *peer) SendProtocol(t string, v any) error { return p.send(t, v) }
func wire(t string, v any) *protocol.Envelope      { e, _ := protocol.New(t, v); return e }
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	dir, err := os.MkdirTemp("", "player-portal-fixture-")
	must(err)
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := storage.Open(filepath.Join(dir, "main.db"))
	must(err)
	defer st.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	origin := "http://" + listener.Addr().String()
	cfg := config.Default()
	cfg.Web.DataDir = filepath.Join(dir, "web")
	cfg.Web.AdminUsers = []string{"owner"}
	cfg.Web.PasskeyOrigins = []string{origin}
	cfg.Body.Enabled = true
	cfg.Body.Mode = "assist"
	cfg.Body.ID = "main"
	cfg.Body.Profile.Enabled = true
	readToken := "fixture-read-only-profile-credential"
	sum := sha256.Sum256([]byte(readToken))
	cfg.Body.Profile.TokenHash = hex.EncodeToString(sum[:])
	tokenFile := filepath.Join(dir, "profile.token")
	must(os.WriteFile(tokenFile, []byte(readToken), 0600))
	acct := account.New(st, log, nil, []string{"owner"})
	tokens := map[string]string{}
	for _, name := range []string{"owner", "viewer"} {
		u, e := st.EnsureUser(ctx, name, time.Now().UnixMilli())
		must(e)
		must(st.SetInitialPassword(ctx, u.ID, "fixture-password-hash"))
		token := "fixture-main-verified-" + name
		hash := sha256.Sum256([]byte(token))
		encoded := hex.EncodeToString(hash[:])
		must(st.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: encoded, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}))
		must(st.MarkPasswordSession(ctx, encoded, "fixture-password-hash"))
		tokens[name] = token
	}
	core := body.New(cfg.Body, st, nil, log)
	core.WithAuthorization(acct.RecheckAdminSession)
	var lock sync.Mutex
	cid := ""
	var epoch, seq uint64
	paused := true
	online := true
	heartbeat := true
	controls := 0
	node := &peer{}
	node.send = func(t string, v any) error {
		switch t {
		case protocol.TypeBodyPolicy:
			p := v.(map[string]any)
			lock.Lock()
			cid = p["connectionId"].(string)
			epoch = p["controlEpoch"].(uint64)
			connection, current := cid, epoch
			lock.Unlock()
			core.Handle(node, wire(protocol.TypeBodyPolicyAck, protocol.BodyAck{ConnectionID: connection, ControlEpoch: current, PolicyRevision: 1}))
		case protocol.TypeBodyControl:
			c := v.(protocol.BodyControl)
			lock.Lock()
			epoch = c.ControlEpoch
			paused = c.Action != "resume"
			if c.Action == "disconnect" {
				online = false
			} else if c.Action == "resume" {
				online = true
			}
			controls++
			isPaused := paused
			lock.Unlock()
			core.Handle(node, wire(protocol.TypeBodyControlAck, protocol.BodyAck{ID: c.ID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, PolicyRevision: 1, Paused: isPaused}))
		}
		return nil
	}
	must(core.Attach(node, protocol.Hello{BodyID: cfg.Body.ID, BodyName: cfg.Body.Name, BodyProtocolVersion: 1, TargetMcVersion: cfg.Body.ExpectedMcVersion, Capabilities: []string{"observe", "move"}}))
	publish := func() {
		lock.Lock()
		defer lock.Unlock()
		if !heartbeat {
			return
		}
		seq++
		core.Handle(node, wire(protocol.TypeBodyState, protocol.BodyState{ConnectionID: cid, ControlEpoch: epoch, PolicyRevision: 1, Seq: seq, Name: cfg.Body.Name, Connected: online, Paused: paused, Health: 20, Food: 20, WorldID: "private-fixture-world", Dimension: "minecraft:overworld", Pos: [3]float64{123, 64, 456}}))
	}
	publish()
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"已收到隔离任务。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer model.Close()
	cfg.Model.BaseURL = model.URL
	cfg.Model.APIKey = "fixture"
	cfg.Model.Name = "fixture"
	cfg.Agent.BackgroundAfterSec = 30
	ag, err := agent.New(ctx, cfg, st, log, nil, nil)
	must(err)
	go ag.Run(ctx)
	hub := session.NewHub(st, log)
	ch := webui.NewChannel(log, cfg, acct, hub, st, ag, nil).WithBody(core, 12)
	game := bodyui.New(core, st, hub, ag, 12, origin)
	replies := make(chan map[string]any, 8)
	paper := &peer{send: func(_ string, v any) error {
		raw, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		replies <- m
		return nil
	}}
	game.Attach(paper)
	game.Handle(ctx, paper, wire(bodyui.RequestType, bodyui.Request{ID: body.ID(), UUID: "12345678-1234-1234-1234-123456789abc", Session: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Player: "FixturePlayer", Allowed: true, Seq: 1, Action: "pair"}))
	var gameCode string
	select {
	case reply := <-replies:
		data, _ := reply["data"].(map[string]any)
		gameCode, _ = data["pairCode"].(string)
	case <-time.After(time.Second):
		panic("pair code unavailable")
	}
	contentStore, err := aiplayer.Open(filepath.Join(dir, "content.db"))
	must(err)
	defer contentStore.Close()
	contentCfg := aiplayer.Config{Listen: "127.0.0.1:8890", Database: filepath.Join(dir, "content.db"), Origin: origin, Name: "AI 玩家", Agents: []aiplayer.AgentConfig{{ID: "main", Name: cfg.Body.Name, DisplayName: "主玩家"}, {ID: "other", Name: "OtherBot", DisplayName: "未接入玩家"}}, Profile: &aiplayer.ProfileSource{AgentID: "main", URL: origin + "/api/body/profile", TokenFile: tokenFile}}
	must(contentStore.Seed(ctx, contentCfg.Agents))
	must(contentStore.CreateAdmin(ctx, "admin", "fixture-publishing-password"))
	legacyCfg := contentCfg
	legacyCfg.ForumEnabled = true
	legacy, err := aiplayer.NewServer(legacyCfg, contentStore, nil, log)
	must(err)
	loginRequest := httptest.NewRequest("POST", origin+"/api/ai-auth/login", strings.NewReader(`{"name":"admin","password":"fixture-publishing-password"}`))
	loginRequest.Header.Set("Origin", origin)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("X-Portal-Request", "1")
	loginResult := httptest.NewRecorder()
	legacy.ServeHTTP(loginResult, loginRequest)
	if loginResult.Code != 200 {
		panic("independent fixture login failed")
	}
	publishingCookie := loginResult.Result().Cookies()[0].Value
	contentProfiles := aiplayer.NewProfiles(contentCfg, log)
	app, err := aiplayer.NewServer(contentCfg, contentStore, contentProfiles, log)
	must(err)
	content := httptest.NewServer(app)
	defer content.Close()
	cfg.Web.AIPlayerProxy = content.URL
	site := portal.New(log, cfg, st, acct, ch).WithBody(core).WithBodyUI(game.Handler(acct))
	site.Register(portal.App{ID: "agent", Name: "Agent", Path: "/agent", Enabled: true, Nav: true, HomeRole: "hero"}, portal.App{ID: "minecraft", Name: "Minecraft", Path: "/minecraft", Enabled: true, Nav: true}, portal.App{ID: "aiplayer", Name: "AI 玩家", Path: "/ai", Enabled: true, HomeRole: "hidden"})
	handler := site.Handler()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_fixture/state" {
			if r.URL.Query().Get("mode") == "content-down" {
				content.Close()
				w.WriteHeader(200)
				return
			}
			lock.Lock()
			switch r.URL.Query().Get("mode") {
			case "offline":
				online = false
			case "online":
				online = true
				heartbeat = true
			case "stale":
				heartbeat = false
			}
			count := controls
			lock.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"controls": count})
			return
		}
		handler.ServeHTTP(w, r)
	})}
	go contentProfiles.Run(ctx)
	// Exclusively ephemeral credentials, consumed by the parent test without printing.
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"origin": origin, "adminToken": tokens["owner"], "viewerToken": tokens["viewer"], "gameCode": gameCode, "publishingCookie": publishingCookie})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel(); _ = server.Close() }()
	err = server.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "fixture server failed")
	}
}
