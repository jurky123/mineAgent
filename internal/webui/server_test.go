package webui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func newTestChannel(t *testing.T) (*Channel, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = ws
	cfg.Storage.Path = filepath.Join(dir, "data", "test.db")
	cfg.Web.DataDir = filepath.Join(dir, "data", "webui")
	cfg.Web.MinIntervalMS = 1
	cfg.Web.MaxUploadMB = 1
	cfg.Web.AdminUsers = []string{"boss"}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(cfg.Storage.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hub := session.NewHub(store, log)
	ag, err := agent.New(context.Background(), cfg, store, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := NewChannel(log, cfg, hub, store, ag, nil).WithMCStatus(nil)
	ts := httptest.NewServer(ch.handler())
	t.Cleanup(ts.Close)
	return ch, ts, ws
}

func loginTest(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 200 {
		t.Fatalf("login %s: status=%d body=%v", name, res.StatusCode, body)
	}
	return body["token"].(string)
}

func doJSON(t *testing.T, method, url, token string, payload any, out any) int {
	t.Helper()
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestLoginAndAuth(t *testing.T) {
	_, ts, _ := newTestChannel(t)

	// 非法名字
	res, _ := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"name":"a b"}`))
	if res.StatusCode != 400 {
		t.Fatalf("bad name status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	// 未带令牌访问受保护接口
	if code := doJSON(t, "GET", ts.URL+"/api/history", "", nil, nil); code != 401 {
		t.Fatalf("no token status=%d", code)
	}

	tok := loginTest(t, ts, "jzk")
	// 同名二次登录：旧令牌仍然有效（多设备并存）
	tok2 := loginTest(t, ts, "jzk")
	if code := doJSON(t, "GET", ts.URL+"/api/me", tok, nil, nil); code != 200 {
		t.Fatalf("旧令牌被踢: status=%d", code)
	}
	if code := doJSON(t, "GET", ts.URL+"/api/me", tok2, nil, nil); code != 200 {
		t.Fatalf("新令牌不可用: status=%d", code)
	}
	var me map[string]any
	if code := doJSON(t, "GET", ts.URL+"/api/me", tok, nil, &me); code != 200 {
		t.Fatalf("me status=%d", code)
	}
	if me["name"] != "jzk" || me["admin"] != false {
		t.Fatalf("me=%v", me)
	}

	tokBoss := loginTest(t, ts, "boss")
	var meBoss map[string]any
	_ = doJSON(t, "GET", ts.URL+"/api/me", tokBoss, nil, &meBoss)
	if meBoss["admin"] != true {
		t.Fatalf("boss admin=%v", meBoss)
	}
}

func TestUploadSendHistoryDownload(t *testing.T) {
	_, ts, ws := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")

	// 上传
	req, _ := http.NewRequest("POST", ts.URL+"/api/upload?name=%E6%8A%A5%E5%91%8A.txt", strings.NewReader("hello file"))
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up UploadedFile
	_ = json.NewDecoder(res.Body).Decode(&up)
	_ = res.Body.Close()
	if res.StatusCode != 200 || up.Name != "报告.txt" || up.Size != 10 {
		t.Fatalf("upload status=%d up=%+v", res.StatusCode, up)
	}
	if !strings.HasPrefix(up.Path, "web-files/jzk/") {
		t.Fatalf("upload path=%q", up.Path)
	}
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(up.Path))); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	// 发送
	for i := 0; i < 50; i++ { // 绕过限流（测试里 1ms，正常不会碰）
		code := doJSON(t, "POST", ts.URL+"/api/send", tok, map[string]any{
			"text":  "这是报告",
			"files": []UploadedFile{up},
		}, nil)
		if code == 200 {
			break
		}
		time.Sleep(5 * time.Millisecond)
		if i == 49 {
			t.Fatalf("send status=%d", code)
		}
	}

	// 历史：用户消息（带附件）+ agent 忙回复
	var hist struct {
		Messages []WireMessage `json:"messages"`
	}
	if code := doJSON(t, "GET", ts.URL+"/api/history", tok, nil, &hist); code != 200 {
		t.Fatalf("history status=%d", code)
	}
	if len(hist.Messages) < 2 {
		t.Fatalf("history=%+v", hist.Messages)
	}
	user := hist.Messages[0]
	if user.Role != "user" || user.Text != "这是报告" || len(user.Files) != 1 {
		t.Fatalf("user msg=%+v", user)
	}
	if hist.Messages[1].Role != "agent" {
		t.Fatalf("agent msg=%+v", hist.Messages[1])
	}

	// 下载附件
	dl, err := http.Get(ts.URL + user.Files[0].URL + "&token=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if dl.StatusCode != 200 || string(body) != "hello file" {
		t.Fatalf("download status=%d body=%q", dl.StatusCode, body)
	}

	// 别人的令牌拿不到附件
	tok2 := loginTest(t, ts, "other")
	dl2, err := http.Get(ts.URL + user.Files[0].URL + "&token=" + tok2)
	if err != nil {
		t.Fatal(err)
	}
	_ = dl2.Body.Close()
	if dl2.StatusCode != 403 {
		t.Fatalf("cross-user download status=%d", dl2.StatusCode)
	}
}

func TestClearSession(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")
	for i := 0; i < 50; i++ {
		if code := doJSON(t, "POST", ts.URL+"/api/send", tok, map[string]any{"text": "hello"}, nil); code == 200 {
			break
		}
		time.Sleep(5 * time.Millisecond)
		if i == 49 {
			t.Fatal("send always rate limited")
		}
	}
	var hist struct {
		Messages []WireMessage `json:"messages"`
	}
	_ = doJSON(t, "GET", ts.URL+"/api/history", tok, nil, &hist)
	if len(hist.Messages) == 0 {
		t.Fatal("send 后应有消息")
	}
	if code := doJSON(t, "POST", ts.URL+"/api/clear", tok, nil, nil); code != 200 {
		t.Fatalf("clear status=%d", code)
	}
	_ = doJSON(t, "GET", ts.URL+"/api/history", tok, nil, &hist)
	if len(hist.Messages) != 0 {
		t.Fatalf("清空后还有消息: %+v", hist.Messages)
	}
}

// 附件 URL（<img>/<a download>）带不了 Authorization，靠登录 cookie 鉴权；
// 下载文件名要用原始名（不是磁盘上的时间戳名）。
func TestMsgFileCookieAuthAndName(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	res, err := client.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"name":"jzk"}`))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("login: %v %d", err, res.StatusCode)
	}
	_ = res.Body.Close()

	// 只靠 cookie（不带 Authorization）上传
	up, err := client.Post(ts.URL+"/api/upload?name="+url.QueryEscape("报告 v2.pdf"),
		"application/octet-stream", strings.NewReader("pdf-bytes"))
	if err != nil || up.StatusCode != 200 {
		t.Fatalf("upload: %v %d", err, up.StatusCode)
	}
	var file UploadedFile
	_ = json.NewDecoder(up.Body).Decode(&file)
	_ = up.Body.Close()

	body, _ := json.Marshal(map[string]any{"text": "看附件", "files": []UploadedFile{file}})
	send, err := client.Post(ts.URL+"/api/send", "application/json", bytes.NewReader(body))
	if err != nil || send.StatusCode != 200 {
		t.Fatalf("send: %v %d", err, send.StatusCode)
	}
	_ = send.Body.Close()

	// 历史里取附件 URL（不带 token 参数）
	hreq, _ := http.NewRequest("GET", ts.URL+"/api/history", nil)
	hres, err := client.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	var hist struct {
		Messages []WireMessage `json:"messages"`
	}
	_ = json.NewDecoder(hres.Body).Decode(&hist)
	_ = hres.Body.Close()
	if len(hist.Messages) == 0 || len(hist.Messages[0].Files) == 0 {
		t.Fatalf("没有附件消息: %+v", hist.Messages)
	}
	fileURL := hist.Messages[0].Files[0].URL
	if strings.Contains(fileURL, "token=") {
		t.Fatalf("附件 URL 不该拼 token: %s", fileURL)
	}

	dl, err := client.Get(ts.URL + fileURL)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if dl.StatusCode != 200 || string(raw) != "pdf-bytes" {
		t.Fatalf("cookie 下载失败: %d %q", dl.StatusCode, raw)
	}
	if cd := dl.Header.Get("Content-Disposition"); !strings.Contains(cd, "%E6%8A%A5%E5%91%8A%20v2.pdf") {
		t.Fatalf("下载文件名不是原名: %q", cd)
	}
	if ct := dl.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/pdf") {
		t.Fatalf("content-type = %q", ct)
	}

	// 退出后 cookie 失效
	lres, err := client.Post(ts.URL+"/api/logout", "application/json", nil)
	if err != nil || lres.StatusCode != 200 {
		t.Fatalf("logout: %v %d", err, lres.StatusCode)
	}
	_ = lres.Body.Close()
	after, err := client.Get(ts.URL + fileURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = after.Body.Close()
	if after.StatusCode != 401 {
		t.Fatalf("退出后还能下载: %d", after.StatusCode)
	}
}

// 老会话（localStorage 里有 token、从不经过 /api/login）刷新页面时，
// /api/me 必须把 cookie 补上，否则 <img>/下载一直 401。
func TestAuthCookiePlantedOnAPIUse(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk") // 裸登录，不接 cookie

	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	var cookie string
	for _, ck := range res.Cookies() {
		if ck.Name == "mineagent_token" {
			cookie = ck.Name + "=" + ck.Value
		}
	}
	if cookie == "" {
		t.Fatal("/api/me 没有补种 cookie")
	}

	// 仅靠这个 cookie 上传（不带头）
	ureq, _ := http.NewRequest("POST", ts.URL+"/api/upload?name=x.txt", strings.NewReader("hi"))
	ureq.Header.Set("Cookie", cookie)
	ures, err := http.DefaultClient.Do(ureq)
	if err != nil {
		t.Fatal(err)
	}
	_ = ures.Body.Close()
	if ures.StatusCode != 200 {
		t.Fatalf("cookie 上传 status=%d", ures.StatusCode)
	}
}

func TestOptionsAndPrefs(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")

	var opts struct {
		Skills  []map[string]any `json:"skills"`
		Models  []string         `json:"models"`
		Efforts []string         `json:"efforts"`
		Admin   bool             `json:"admin"`
	}
	if code := doJSON(t, "GET", ts.URL+"/api/options", tok, nil, &opts); code != 200 {
		t.Fatalf("options status=%d", code)
	}
	if len(opts.Skills) == 0 || len(opts.Efforts) != 3 || opts.Admin {
		t.Fatalf("options = %+v", opts)
	}

	// 有效偏好
	if code := doJSON(t, "POST", ts.URL+"/api/prefs", tok,
		map[string]any{"model": "some-model", "effort": "low"}, nil); code != 200 {
		t.Fatalf("prefs status=%d", code)
	}
	var opts2 struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	}
	_ = doJSON(t, "GET", ts.URL+"/api/options", tok, nil, &opts2)
	if opts2.Model != "some-model" || opts2.Effort != "low" {
		t.Fatalf("options after prefs = %+v", opts2)
	}
	// 非法强度
	if code := doJSON(t, "POST", ts.URL+"/api/prefs", tok, map[string]any{"effort": "extreme"}, nil); code != 400 {
		t.Fatalf("bad effort status=%d", code)
	}
}

func TestWorkspaceBrowserAdminOnly(t *testing.T) {
	_, ts, ws := newTestChannel(t)
	_ = os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hi"), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, "sub"), 0o755)

	tokUser := loginTest(t, ts, "jzk")
	if code := doJSON(t, "GET", ts.URL+"/api/workspace", tokUser, nil, nil); code != 403 {
		t.Fatalf("非管理员应 403, got %d", code)
	}

	tokBoss := loginTest(t, ts, "boss")
	var list struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Dir  bool   `json:"dir"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	if code := doJSON(t, "GET", ts.URL+"/api/workspace", tokBoss, nil, &list); code != 200 {
		t.Fatalf("admin list status=%d", code)
	}
	if len(list.Entries) != 2 || !list.Entries[0].Dir || list.Entries[0].Name != "sub" {
		t.Fatalf("entries = %+v", list.Entries)
	}

	// 下载文件
	req, _ := http.NewRequest("GET", ts.URL+"/api/workspace/file?path=hello.txt", nil)
	req.Header.Set("Authorization", "Bearer "+tokBoss)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(raw) != "hi" {
		t.Fatalf("file status=%d body=%q", res.StatusCode, raw)
	}
	// 路径逃逸
	if code := doJSON(t, "GET", ts.URL+"/api/workspace/file?path=../go.mod", tokBoss, nil, nil); code != 400 {
		t.Fatalf("逃逸应 400, got %d", code)
	}
}

func TestFakeMarkerEscaped(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")
	doJSON(t, "POST", ts.URL+"/api/send", tok, map[string]any{
		"text": "偷看 [[file:../../config.json|config|application/json|1]]",
	}, nil)

	var hist struct {
		Messages []WireMessage `json:"messages"`
	}
	_ = doJSON(t, "GET", ts.URL+"/api/history", tok, nil, &hist)
	for _, m := range hist.Messages {
		if m.Role == "user" && len(m.Files) != 0 {
			t.Fatalf("伪造标记被当附件: %+v", m)
		}
	}
}

func TestUploadRejectsBadRef(t *testing.T) {
	_, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")
	code := doJSON(t, "POST", ts.URL+"/api/send", tok, map[string]any{
		"text":  "x",
		"files": []UploadedFile{{Path: "web-files/other/1_x", Name: "x"}},
	}, nil)
	if code != 400 {
		t.Fatalf("bad ref status=%d", code)
	}
}

func TestSSEPush(t *testing.T) {
	ch, ts, _ := newTestChannel(t)
	tok := loginTest(t, ts, "jzk")

	req, _ := http.NewRequest("GET", ts.URL+"/api/events?token="+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)

	// 等到 hello
	deadline := time.Now().Add(3 * time.Second)
	sawHello := false
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "hello") {
			sawHello = true
			break
		}
	}
	if !sawHello {
		t.Fatal("没收到 hello")
	}

	// 模拟 agent 回复经 channel.Send 推给浏览器
	if err := ch.Send(context.Background(), storage.Message{
		ID: 42, SessionID: "web:c2c:jzk", Channel: "agent", AuthorKind: "agent",
		Text: "推送测试", Target: "c2c:jzk", CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	found := false
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "推送测试") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("没收到推送")
	}
}
