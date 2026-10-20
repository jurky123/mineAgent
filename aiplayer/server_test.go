package aiplayer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	s                 *Store
	app               *Server
	c                 Config
	now               atomic.Int64
	token, otherToken string
	cookie            *http.Cookie
	csrf              string
	dir               string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	f.now.Store(time.Now().UnixMilli())
	f.c = Config{Listen: "127.0.0.1:8890", Database: filepath.Join(f.dir, "portal.db"), Origin: "http://127.0.0.1:8890", Name: "AI 玩家", Agents: []AgentConfig{{"main", "MineAgentBot", "小智"}, {"other", "OtherBot", "其他 AI"}}}
	var err error
	f.s, err = Open(f.c.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	f.s.now = func() time.Time { return time.UnixMilli(f.now.Load()) }
	if err = f.s.Seed(context.Background(), f.c.Agents); err != nil {
		t.Fatal(err)
	}
	_, f.token, err = f.s.IssueToken(context.Background(), "main", f.s.now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, f.otherToken, err = f.s.IssueToken(context.Background(), "other", f.s.now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = NewServer(f.c, f.s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) advance() { f.now.Add(61000) }
func (f *fixture) request(t *testing.T, method, path, token, key string, body any, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	raw := ""
	if body != nil {
		raw = encoded(body)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", f.c.Origin)
	r.Header.Set("X-Portal-Request", "1")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if admin && f.cookie != nil {
		r.AddCookie(f.cookie)
		r.Header.Set("X-CSRF-Token", f.csrf)
	}
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	return w
}
func mustStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *fixture) login(t *testing.T) {
	t.Helper()
	if err := f.s.CreateAdmin(context.Background(), "admin", "fixture-password-long"); err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "POST", "/api/ai-auth/login", "", "", map[string]string{"name": "admin", "password": "fixture-password-long"}, false)
	mustStatus(t, w, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing independent cookie")
	}
	f.cookie = cookies[0]
	f.csrf = decode[map[string]any](t, w)["csrfToken"].(string)
}

type written struct {
	ID         string `json:"id"`
	RevisionID int64  `json:"revisionId"`
	Version    int64  `json:"version"`
	Status     string `json:"status"`
}

func (f *fixture) create(t *testing.T, key, title, body, state string) written {
	t.Helper()
	w := f.request(t, "POST", "/api/ai-posts", f.token, key, PostInput{Title: title, Body: body, Tags: []string{"日志"}, Status: state}, false)
	mustStatus(t, w, 200)
	return decode[written](t, w)
}
func (f *fixture) moderate(t *testing.T, p written, action, key string, ack bool) *httptest.ResponseRecorder {
	return f.request(t, "POST", "/api/ai-posts/"+p.ID+"/moderate", "", key, ModerateInput{Action: action, RevisionID: p.RevisionID, ExpectedVersion: p.Version, Reason: "人工核对", AcknowledgeFlags: ack}, true)
}

func TestPublishingIsolationAndModeration(t *testing.T) {
	f := setup(t)
	f.login(t)
	p := f.create(t, "create-one", "可公开的初稿", "# 正文\n\n这是第一版。", "pending")
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false), 404)
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/mine/"+p.ID, f.otherToken, "", nil, false), 404)
	list := f.request(t, "GET", "/api/ai-posts?q=初稿", "", "", nil, false)
	if strings.Contains(list.Body.String(), p.ID) || !strings.Contains(list.Body.String(), `"total":0`) {
		t.Fatal("pending leaked into search")
	}
	w := f.moderate(t, p, "publish", "publish-one", true)
	mustStatus(t, w, 200)
	p = decode[written](t, w)
	public := f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false)
	mustStatus(t, public, 200)
	if strings.Contains(public.Body.String(), "reviewer") || strings.Contains(public.Body.String(), "working") {
		t.Fatal("private fields leaked")
	}
	f.advance()
	edit := PostInput{Title: "未审秘密改稿", Body: "尚未审核的新版正文", Status: "pending", ExpectedVersion: p.Version, ExpectedRevisionID: p.RevisionID}
	w = f.request(t, "PATCH", "/api/ai-posts/"+p.ID, f.token, "edit-pending", edit, false)
	mustStatus(t, w, 200)
	next := decode[written](t, w)
	public = f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false)
	if strings.Contains(public.Body.String(), "秘密") || !strings.Contains(public.Body.String(), "第一版") {
		t.Fatal("unreviewed edit replaced live")
	}
	list = f.request(t, "GET", "/api/ai-posts?q=秘密", "", "", nil, false)
	if !strings.Contains(list.Body.String(), `"total":0`) {
		t.Fatal("working body searchable")
	}
	mustStatus(t, f.moderate(t, p, "publish", "stale-review", true), 409)
	w = f.moderate(t, next, "reject", "reject-draft", true)
	mustStatus(t, w, 200)
	next = decode[written](t, w)
	public = f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false)
	mustStatus(t, public, 200)
	if !strings.Contains(public.Body.String(), "第一版") {
		t.Fatal("reject removed live")
	}
	// Hide targets the reviewed live revision, even while a rejected working copy exists.
	hide := next
	hide.RevisionID = p.RevisionID
	w = f.moderate(t, hide, "hide", "hide-live", true)
	mustStatus(t, w, 200)
	hidden := decode[written](t, w)
	for _, path := range []string{"/api/ai-posts/" + p.ID, "/api/ai-posts?q=初稿", "/api/ai-agents/main"} {
		w = f.request(t, "GET", path, "", "", nil, false)
		if strings.Contains(w.Body.String(), "第一版") || strings.Contains(w.Body.String(), "初稿") {
			t.Fatalf("hidden post leaked via %s", path)
		}
	}
	hidden.RevisionID = p.RevisionID
	w = f.moderate(t, hidden, "restore", "restore-live", true)
	mustStatus(t, w, 200)
	restored := decode[written](t, w)
	w = f.moderate(t, restored, "delete", "delete-post", true)
	mustStatus(t, w, 200)
	deleted := decode[written](t, w)
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false), 404)
	w = f.moderate(t, deleted, "restore", "restore-deleted", true)
	mustStatus(t, w, 200)
	pending := decode[written](t, w)
	// All operations happen at the SAME clock instant: audit timestamps cannot gate restore.
	bypass := pending
	bypass.RevisionID = p.RevisionID
	mustStatus(t, f.moderate(t, bypass, "restore", "restore-bypass", true), 409)
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false), 404)
	w = f.moderate(t, pending, "publish", "review-restored", true)
	mustStatus(t, w, 200)
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/"+p.ID, "", "", nil, false), 200)
}
func TestIdempotencyQuotaAndConcurrentVersions(t *testing.T) {
	f := setup(t)
	p := f.create(t, "stable-key", "草稿", "原始正文", "draft")
	input := PostInput{Title: "草稿", Body: "原始正文", Tags: []string{"日志"}, Status: "draft"}
	w := f.request(t, "POST", "/api/ai-posts", f.token, "stable-key", input, false)
	mustStatus(t, w, 200)
	if w.Header().Get("Idempotency-Replayed") != "true" || decode[written](t, w).ID != p.ID {
		t.Fatal("idempotency did not replay")
	}
	input.Body = "其他正文"
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "stable-key", input, false), 409)
	var quota int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM write_quota`).Scan(&quota); err != nil || quota != 1 {
		t.Fatalf("retry charged quota: %d %v", quota, err)
	}
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "new-quota-key", input, false), 429)
	f.advance()
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := PostInput{Title: "修改", Body: fmt.Sprint("正文", i), Status: "pending", ExpectedVersion: p.Version, ExpectedRevisionID: p.RevisionID}
			w := f.request(t, "PATCH", "/api/ai-posts/"+p.ID, f.token, fmt.Sprint("edit-key-", i), in, false)
			statuses <- w.Code
		}(i)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("stale concurrent version accepted: %v", counts)
	}
	// The same request still replays across credential rotation for the SAME agent.
	_, rotated, err := f.s.IssueToken(context.Background(), "main", f.s.now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	input.Body = "原始正文"
	w = f.request(t, "POST", "/api/ai-posts", rotated, "stable-key", input, false)
	mustStatus(t, w, 200)
	if decode[written](t, w).ID != p.ID {
		t.Fatal("rotation duplicated request")
	}
}
func TestAIAuthOwnershipAndStrictFields(t *testing.T) {
	f := setup(t)
	p := f.create(t, "own-post-key", "自己的草稿", "自己的内容", "draft")
	f.advance()
	for _, path := range []string{"/api/ai-moderation/queue", "/api/ai-admin/posts", "/api/ai-admin/agents", "/api/ai-admin/audit"} {
		mustStatus(t, f.request(t, "GET", path, f.token, "", nil, false), 401)
	}
	for _, extra := range []string{"agent_id", "agentId", "author", "live_revision_id", "reviewer", "trust_level", "visibility"} {
		in := map[string]any{"title": "标题", "body": "正文", extra: "other"}
		mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "forged-author-key", in, false), 400)
	}
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "forged-publish-key", PostInput{Title: "标题", Body: "正文", Status: "published"}, false), 422)
	mustStatus(t, f.request(t, "PATCH", "/api/ai-posts/"+p.ID, f.otherToken, "other-edit-key", PostInput{Title: "冒用", Body: "修改", ExpectedVersion: p.Version, ExpectedRevisionID: p.RevisionID}, false), 404)
	mustStatus(t, f.request(t, "POST", "/api/ai-posts/"+p.ID+"/moderate", f.token, "ai-moderate-key", ModerateInput{Action: "publish", RevisionID: p.RevisionID, ExpectedVersion: p.Version}, false), 401)
	for _, path := range []string{"/api/body/profile", "/api/body/status", "/api/body/events", "/api/body/control"} {
		mustStatus(t, f.request(t, "POST", path, f.token, "body-control-key", map[string]string{"action": "resume"}, false), 404)
	}
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/mine?token="+f.token, "", "", nil, false), 401)
	id, _, _ := strings.Cut(f.token, ".")
	if err := f.s.RevokeToken(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/mine", f.token, "", nil, false), 401)
	f.now.Add(int64(2 * time.Hour / time.Millisecond))
	mustStatus(t, f.request(t, "GET", "/api/ai-posts/mine", f.otherToken, "", nil, false), 401)
}
func TestAdminSessionCSRFAndReset(t *testing.T) {
	f := setup(t)
	f.login(t)
	mustStatus(t, f.request(t, "POST", "/api/ai-admin/posts?agent=main", "", "admin-create-key", PostInput{Title: "管理草稿", Body: "正文"}, false), 401)
	r := httptest.NewRequest("POST", "/api/ai-admin/posts?agent=main", strings.NewReader(`{"title":"标题","body":"正文"}`))
	r.AddCookie(f.cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-CSRF-Token", f.csrf)
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	mustStatus(t, w, 403)
	r.Header.Set("Origin", f.c.Origin)
	r.Header.Set("X-Portal-Request", "1")
	r.Header.Del("X-CSRF-Token")
	w = httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	mustStatus(t, w, 403)
	me := f.request(t, "GET", "/api/ai-auth/me", "", "", nil, true)
	mustStatus(t, me, 200)
	if !f.cookie.HttpOnly || f.cookie.SameSite != http.SameSiteStrictMode || f.cookie.Name == "mine_token" {
		t.Fatal("weak/shared admin cookie")
	}
	if err := f.s.CreateAdmin(context.Background(), "admin", "replacement-password-fixture"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.request(t, "GET", "/api/ai-auth/me", "", "", nil, true), 401)
	// A cookie from MineAgent is not accepted as this service's identity.
	r = httptest.NewRequest("GET", "/api/ai-auth/me", nil)
	r.AddCookie(&http.Cookie{Name: "mine_token", Value: "fixture"})
	w = httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	mustStatus(t, w, 401)
}
func TestContentPolicyAndReviewFlags(t *testing.T) {
	f := setup(t)
	for _, body := range []string{"<script>alert(1)</script>", "![x](https://example.com/a.png)", "[x](javascript:alert(1))", "[x](data:text/html,hello)", "https://127.1/private", "https://10.0.0.1/path", "http://2130706433/test", "[x](http&#58;//127.0.0.1)", "API_KEY=abcdefghijklmnop1234", "-----BEGIN PRIVATE KEY-----", "内网 192.168.1.2"} {
		w := f.request(t, "POST", "/api/ai-posts", f.token, "content-case-key", PostInput{Title: "测试", Body: body}, false)
		mustStatus(t, w, 422)
	}
	f.login(t)
	p := f.create(t, "flagged-post", "游记", "位置 x=12 y=64 z=8，需人工核对。", "pending")
	mustStatus(t, f.moderate(t, p, "publish", "no-acknowledge", false), 422)
	mustStatus(t, f.moderate(t, p, "publish", "acknowledged", true), 200)
}
func TestBioReviewAndBlockedAI(t *testing.T) {
	f := setup(t)
	f.login(t)
	w := f.request(t, "PATCH", "/api/ai-agents/mine/profile", f.token, "propose-bio-key", ProfileInput{Bio: "待审简介", Tags: []string{"建筑"}, ExpectedVersion: 1}, false)
	mustStatus(t, w, 200)
	proposal := decode[written](t, w)
	w = f.request(t, "GET", "/api/ai-agents/main", "", "", nil, false)
	if strings.Contains(w.Body.String(), "待审简介") {
		t.Fatal("unreviewed bio leaked")
	}
	w = f.request(t, "POST", "/api/ai-agents/main/moderate-profile", "", "approve-bio-key", AgentAdminInput{ExpectedVersion: proposal.Version, RevisionID: proposal.RevisionID, Action: "publish"}, true)
	mustStatus(t, w, 200)
	w = f.request(t, "GET", "/api/ai-agents/main", "", "", nil, false)
	if !strings.Contains(w.Body.String(), "待审简介") || strings.Contains(w.Body.String(), "trustLevel") || strings.Contains(w.Body.String(), "blocked") {
		t.Fatal("bio projection incorrect")
	}
	version := decode[written](t, f.request(t, "POST", "/api/ai-agents/main/trust", "", "trust-metadata", AgentAdminInput{ExpectedVersion: 3, Level: "trusted"}, true)).Version
	yes := true
	w = f.request(t, "POST", "/api/ai-agents/main/block", "", "block-ai-key", AgentAdminInput{ExpectedVersion: version, Blocked: &yes, Reason: "暂停写入"}, true)
	mustStatus(t, w, 200)
	f.advance()
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "blocked-post-key", PostInput{Title: "标题", Body: "正文"}, false), 403)
}
func TestBackupRestoreAndForeignDatabaseRefusal(t *testing.T) {
	f := setup(t)
	p := f.create(t, "backup-post-key", "备份测试", "草稿及审计都需保留。", "pending")
	backup := filepath.Join(f.dir, "backup", "consistent.db")
	if err := f.s.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Post(context.Background(), p.ID)
	if err != nil || got == nil || got.Working.Body != "草稿及审计都需保留。" {
		t.Fatal("restoration failed", err)
	}
	var check string
	if err = restored.db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("backup integrity: %s %v", check, err)
	}
	for _, table := range []string{"requests", "audit", "ai_tokens", "write_quota"} {
		var count int
		if err = restored.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count == 0 {
			t.Fatal("backup missed", table, err)
		}
	}
	info, _ := os.Stat(backup)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	foreign := filepath.Join(f.dir, "foreign.db")
	db, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE unrelated(secret TEXT)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(foreign); err == nil {
		t.Fatal("opened unrelated database")
	}
}
func TestWriterUsesPinnedAuthorityAndIdempotency(t *testing.T) {
	f := setup(t)
	srv := httptest.NewServer(f.app)
	defer srv.Close()
	tokenFile := filepath.Join(f.dir, "writer.token")
	if err := os.WriteFile(tokenFile, []byte(f.token), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := NewWriter(srv.URL, tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	req := WritingRequest{Operation: "create", IdempotencyKey: "writer-stable-key", Post: &PostInput{Title: "受控客户端", Body: "管理员明确触发的待审稿", Status: "pending"}}
	first, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Do(context.Background(), req)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("client retry duplicated request", err)
	}
	for _, base := range []string{"http://private.internal", "https://public.example/other", "https://user:password@example.com", "https://example.com?token=secret"} {
		if _, err = NewWriter(base, tokenFile); err == nil {
			t.Fatal("unsafe client authority", base)
		}
	}
	if _, err = client.Do(context.Background(), WritingRequest{Operation: "edit", PostID: "../../api/body/control", IdempotencyKey: "escaping-key", Post: req.Post}); err == nil {
		t.Fatal("post ID escaped route")
	}
}

func TestRollingWriteQuotas(t *testing.T) {
	f := setup(t)
	f.create(t, "quota-first", "第一篇", "正文", "draft")
	f.advance()
	f.create(t, "quota-second", "第二篇", "正文", "draft")
	f.advance()
	in := PostInput{Title: "下一篇", Body: "正文", Status: "draft"}
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "quota-hour-limit", in, false), 429)
	// Keep fixture tokens valid while advancing the rolling quota clock.
	_, err := f.s.db.Exec(`UPDATE ai_tokens SET expires_at=?`, f.s.now().Add(48*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(int64(time.Hour / time.Millisecond))
	f.create(t, "quota-third", "第三篇", "正文", "draft")
	f.advance()
	f.create(t, "quota-fourth", "第四篇", "正文", "draft")
	f.now.Add(int64(time.Hour / time.Millisecond))
	f.create(t, "quota-fifth", "第五篇", "正文", "draft")
	f.now.Add(int64(time.Hour / time.Millisecond))
	mustStatus(t, f.request(t, "POST", "/api/ai-posts", f.token, "quota-day-limit", in, false), 429)
}

func TestOwnDraftDeleteAndExpiredRestore(t *testing.T) {
	f := setup(t)
	p := f.create(t, "own-delete-new", "可删草稿", "正文", "draft")
	f.advance()
	in := map[string]int64{"revisionId": p.RevisionID, "expectedVersion": p.Version}
	w := f.request(t, "DELETE", "/api/ai-posts/"+p.ID, f.token, "delete-own-draft", in, false)
	mustStatus(t, w, 200)
	deleted := decode[written](t, w)
	mustStatus(t, f.request(t, "DELETE", "/api/ai-posts/"+p.ID, f.token, "delete-own-draft", in, false), 200)
	f.now.Add(int64(31 * 24 * time.Hour / time.Millisecond))
	_, e := f.s.Moderate(context.Background(), Principal{Kind: "admin", ID: "operator"}, p.ID, "expired-restore-key", ModerateInput{Action: "restore", RevisionID: deleted.RevisionID, ExpectedVersion: deleted.Version})
	if e == nil || e.Status != 409 {
		t.Fatal("expired soft-delete restored", e)
	}
}
