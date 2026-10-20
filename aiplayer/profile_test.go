package aiplayer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProfileProjectionFreshnessAndFailures(t *testing.T) {
	now := time.Now()
	code := 200
	version := 1
	status := "online"
	updated := now.UnixMilli()
	stale := false
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/body/profile" || r.Header.Get("Authorization") != "Bearer profile-fixture-credential" {
			t.Error("profile contract/authority violated")
		}
		if code != 200 {
			if code == 429 {
				w.Header().Set("Retry-After", "37")
			}
			w.WriteHeader(code)
			return
		}
		writeJSON(w, 200, map[string]any{"profileVersion": version, "id": "main", "name": "MineAgentBot", "displayName": "小智", "status": status, "activity": "digging", "dimension": "minecraft:overworld", "onlineSince": now.UnixMilli(), "updatedAt": updated, "stale": stale, "capabilities": []string{"move", "execute_arbitrary_command"}, "skin": "https://tracker.example/avatar", "position": map[string]int{"x": 99}, "inventory": []string{"secret"}, "lastEvent": "private chat"})
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "profile.token")
	if err := os.WriteFile(path, []byte("profile-fixture-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Agents: []AgentConfig{{"main", "FallbackBot", "默认名"}}, Profile: &ProfileSource{AgentID: "main", URL: upstream.URL + "/api/body/profile", TokenFile: path}}
	p := NewProfiles(c, nil)
	p.now = func() time.Time { return now }
	delay, pause := p.poll(context.Background())
	if delay != 5*time.Second || pause {
		t.Fatal("healthy polling policy", delay, pause)
	}
	s := p.Get("main")
	if s.Status != "online" || s.Stale || s.Skin != nil || len(s.Capabilities) != 1 || s.Capabilities[0] != "move" {
		t.Fatalf("bad projection: %+v", s)
	}
	if raw := encoded(s); strings.Contains(raw, "position") || strings.Contains(raw, "inventory") || strings.Contains(raw, "lastEvent") || strings.Contains(raw, "credential") {
		t.Fatal("private fields leaked")
	}
	for i := 0; i < 20; i++ {
		_ = p.Get("main")
	}
	if calls != 1 {
		t.Fatal("public reads called upstream")
	}
	code = 503
	delay, pause = p.poll(context.Background())
	if delay != 0 || pause || p.Get("main").Status != "unknown" || p.Get("main").UpdatedAt != updated {
		t.Fatal("transport failure confused with offline or lost source time")
	}
	code = 200
	status = "offline"
	_, _ = p.poll(context.Background())
	s = p.Get("main")
	if s.Status != "offline" || s.OnlineSince != nil || s.Stale {
		t.Fatal("explicit offline lost", s)
	}
	now = now.Add(11 * time.Second)
	if p.Get("main").Status != "unknown" {
		t.Fatal("stale offline shown as current")
	}
	updated = now.UnixMilli()
	status = "busy"
	_, _ = p.poll(context.Background())
	if p.Get("main").Status != "busy" {
		t.Fatal("did not recover")
	}
	code = 429
	delay, pause = p.poll(context.Background())
	if delay != 37*time.Second || pause || p.Get("main").Status != "unknown" {
		t.Fatal("Retry-After ignored")
	}
	code = 401
	_, pause = p.poll(context.Background())
	if !pause || p.Get("main").Status != "unknown" {
		t.Fatal("credential rejection did not pause")
	}
	code = 200
	version = 2
	_, _ = p.poll(context.Background())
	if p.Get("main").Status != "unknown" {
		t.Fatal("unsupported version accepted")
	}
	version = 1
	stale = true
	_, _ = p.poll(context.Background())
	if !p.Get("main").Stale || p.Get("main").Status != "unknown" {
		t.Fatal("upstream stale ignored")
	}
}
func TestProfileRedirectDoesNotForwardCredential(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/steal", 302) }))
	defer source.Close()
	token := filepath.Join(t.TempDir(), "profile.token")
	if err := os.WriteFile(token, []byte("fixture-read-only-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	p := NewProfiles(Config{Profile: &ProfileSource{AgentID: "main", URL: source.URL + "/api/body/profile", TokenFile: token}}, nil)
	_, _ = p.poll(context.Background())
	if forwarded.Load() != 0 {
		t.Fatal("redirect forwarded Authorization")
	}
}
func TestConfigurationPinsProfileAndPrivateFiles(t *testing.T) {
	f := setup(t)
	for _, source := range []ProfileSource{{"main", "http://10.0.0.1/api/body/profile", "token"}, {"main", "https://example.com/api/body/status", "token"}, {"main", "https://example.com/api/body/profile?token=value", "token"}, {"other-missing", "https://example.com/api/body/profile", "token"}} {
		c := f.c
		c.Profile = &source
		if c.Validate() == nil {
			t.Fatal("unsafe profile source accepted")
		}
	}
	path := filepath.Join(f.dir, "token")
	if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(path); err == nil {
		t.Fatal("public credential file accepted")
	}
}
func TestPublicHandlerLatencyFixedDataset(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	tx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("公开测试正文。", 256)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("fixture-%04d", i)
		stamp := f.now.Load() + int64(i)
		if _, err = tx.Exec(`INSERT INTO posts(id,agent_id,visibility,created_at,updated_at,published_at) VALUES(?,'main','published',?,?,?)`, id, stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		res, err := tx.Exec(`INSERT INTO revisions(post_id,revision,title,body,tags,lang,state,flags,created_at) VALUES(?,1,?,?,'["日志"]','zh-CN','published','[]',?)`, id, "公开测试 "+id, body, stamp)
		if err != nil {
			t.Fatal(err)
		}
		rev, _ := res.LastInsertId()
		if _, err = tx.Exec(`UPDATE posts SET live_revision=? WHERE id=?`, rev, id); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Warm the SQLite page cache. Measurement includes handler, SQL, projection and
	// JSON encoding, excluding TCP/TLS and upstream polling; eight concurrent readers.
	for i := 0; i < 10; i++ {
		mustStatus(t, f.request(t, "GET", "/api/ai-posts", "", "", nil, false), 200)
	}
	var wg sync.WaitGroup
	latencies := make(chan time.Duration, 320)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				path := "/api/ai-posts?pageSize=12"
				if i%2 == 0 {
					path = "/api/ai-agents"
				}
				start := time.Now()
				w := f.request(t, "GET", path, "", "", nil, false)
				if w.Code != 200 {
					t.Errorf("latency run status %d", w.Code)
				}
				latencies <- time.Since(start)
			}
		}()
	}
	wg.Wait()
	close(latencies)
	all := []time.Duration{}
	for d := range latencies {
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	p95 := all[(len(all)*95+99)/100-1]
	t.Logf("dataset=1000 posts (~%d bytes/body), agents=2, concurrency=8, requests=%d, P95=%s (HTTP handler/SQLite/JSON, cached, no network/upstream)", len(body), len(all), p95)
	if p95 >= 300*time.Millisecond {
		t.Fatalf("P95 %s exceeds 300ms target", p95)
	}
}
