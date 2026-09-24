package webui

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"mineagent/internal/storage"
)

//go:embed static/index.html
var staticFS embed.FS

// Serve 起 HTTP 服务：静态页 + JSON API + SSE。调用方 go 它。
func (c *Channel) Serve() error {
	srv := &http.Server{
		Addr:              c.cfg.Listen,
		Handler:           c.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	c.srv = srv
	c.log.Info("web ui listening", "addr", c.cfg.Listen, "users", len(c.cfg.Users), "admins", len(c.cfg.AdminUsers))
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// handler 组装路由（测试用 httptest 直接挂它）。
func (c *Channel) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleIndex)
	mux.HandleFunc("/api/login", c.handleLogin)
	mux.HandleFunc("/api/logout", c.handleLogout)
	mux.HandleFunc("/api/me", c.withAuth(c.handleMe))
	mux.HandleFunc("/api/history", c.withAuth(c.handleHistory))
	mux.HandleFunc("/api/clear", c.withAuth(c.handleClear))
	mux.HandleFunc("/api/events", c.handleEvents) // SSE 用 ?token=，自己校验
	mux.HandleFunc("/api/upload", c.withAuth(c.handleUpload))
	mux.HandleFunc("/api/send", c.withAuth(c.handleSend))
	mux.HandleFunc("/api/msgfile", c.handleMsgFile) // <img> 拿不到 header，支持 ?token=
	mux.HandleFunc("/api/options", c.withAuth(c.handleOptions))
	mux.HandleFunc("/api/prefs", c.withAuth(c.handlePrefs))
	mux.HandleFunc("/api/workspace", c.withAuth(c.handleWorkspaceList))
	mux.HandleFunc("/api/workspace/file", c.withAuth(c.handleWorkspaceFile))
	return c.cors(mux)
}

// cors 给未来"嵌到别的网页"留口子：允许跨域带 Authorization（无 Cookie 凭证）。
func (c *Channel) cors(next http.Handler) http.Handler {
	origins := c.cfg.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	allowAll := false
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				for _, o := range origins {
					if strings.EqualFold(o, origin) {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Vary", "Origin")
						break
					}
				}
			}
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		// 允许被其它网页 iframe 嵌入（后续"接入另外的网页"）。
		w.Header().Set("Content-Security-Policy", "frame-ancestors *")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Channel) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// tokenOf 从 Authorization: Bearer / ?token= / 登录 cookie 取令牌。
// cookie 是给 <img src>、<a download> 这类带不了 header 的请求用的
// （登录时种，附件 URL 就不用把 token 拼在地址里）。
func tokenOf(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
		return q
	}
	if ck, err := r.Cookie("mineagent_token"); err == nil {
		return strings.TrimSpace(ck.Value)
	}
	return ""
}

// auth 校验请求令牌（header/query/cookie 任一来源），返回账号名与令牌。
func (c *Channel) auth(r *http.Request) (name, tok string) {
	tok = tokenOf(r)
	if tok == "" {
		return "", ""
	}
	return c.nameByToken(tok), tok
}

// setAuthCookie 补种登录 cookie：老会话的页面只用 Bearer 调 /api/me，
// 从不经过 /api/login，若只在登录时种 cookie，附件（<img>/下载链接）
// 就会一直 401。所以任何一次已认证请求都顺手把 cookie 补上。
func (c *Channel) setAuthCookie(w http.ResponseWriter, r *http.Request, tok string) {
	if tok == "" {
		return
	}
	if ck, err := r.Cookie("mineagent_token"); err == nil && ck.Value == tok {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "mineagent_token", Value: tok, Path: "/", MaxAge: 30 * 24 * 3600,
		SameSite: http.SameSiteLaxMode, HttpOnly: true,
	})
}

// withAuth 用令牌换账号名放 context，失败 401；顺带补种 cookie。
func (c *Channel) withAuth(next func(w http.ResponseWriter, r *http.Request, name string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, tok := c.auth(r)
		if name == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录或令牌已失效，请重新进入"})
			return
		}
		c.setAuthCookie(w, r, tok)
		next(w, r, name)
	}
}

func (c *Channel) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数不是合法 JSON"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if !ValidAccountName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "名字不合法：1-24 个字符，字母/数字/中文/_-，字母数字开头，别带空格和符号"})
		return
	}
	if len(c.cfg.Users) > 0 {
		ok := false
		for _, u := range c.cfg.Users {
			if u == name {
				ok = true
				break
			}
		}
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "这个名字不在允许名单里"})
			return
		}
	}
	tok, err := c.login(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "生成登录令牌失败"})
		return
	}
	// cookie 供 <img>/<a download> 等无法带 Authorization 的请求鉴权。
	http.SetCookie(w, &http.Cookie{
		Name: "mineagent_token", Value: tok, Path: "/", MaxAge: 30 * 24 * 3600,
		SameSite: http.SameSiteLaxMode, HttpOnly: true,
	})
	c.log.Info("web login", "name", name, "admin", IsAdminName(name, c.cfg.AdminUsers))
	writeJSON(w, http.StatusOK, map[string]any{
		"name":  name,
		"token": tok,
		"admin": IsAdminName(name, c.cfg.AdminUsers),
	})
}

// handleLogout 注销当前令牌并清 cookie。
func (c *Channel) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	c.logout(tokenOf(r))
	http.SetCookie(w, &http.Cookie{Name: "mineagent_token", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Channel) handleMe(w http.ResponseWriter, _ *http.Request, name string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":  name,
		"admin": IsAdminName(name, c.cfg.AdminUsers),
	})
}

func (c *Channel) handleHistory(w http.ResponseWriter, r *http.Request, name string) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	var (
		raw []storage.Message
		err error
	)
	if after > 0 {
		raw, err = c.store.MessagesAfter(r.Context(), "web:c2c:"+name, after, 200)
	} else {
		raw, err = c.store.RecentMessages(r.Context(), "web:c2c:"+name, 200)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := make([]WireMessage, 0, len(raw))
	for _, m := range raw {
		msgs = append(msgs, ToWire(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// handleOptions 给 + 菜单：技能、可选模型、思考强度、当前偏好。
func (c *Channel) handleOptions(w http.ResponseWriter, r *http.Request, name string) {
	models := c.availableModels(r.Context())
	prefs := c.prefsOf(name)
	// 偏好里的模型如果已不在列表（网关变了），回退默认。
	if prefs.Model != "" && len(models) > 0 && !containsStr(models, prefs.Model) {
		prefs.Model = ""
		c.setPrefs(name, prefs)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":       c.cfg.Skills,
		"models":       models,
		"efforts":      effortLevels,
		"model":        prefs.Model,
		"effort":       prefs.Effort,
		"defaultModel": c.model,
		"admin":        IsAdminName(name, c.cfg.AdminUsers),
	})
}

// handlePrefs 保存 + 菜单选择（模型/思考强度）。
func (c *Channel) handlePrefs(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	var req struct {
		Model  *string `json:"model"`
		Effort *string `json:"effort"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数不是合法 JSON"})
		return
	}
	prefs := c.prefsOf(name)
	if req.Model != nil {
		m := strings.TrimSpace(*req.Model)
		if m != "" {
			if models := c.availableModels(r.Context()); len(models) > 0 && !containsStr(models, m) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "模型不在可选列表里"})
				return
			}
		}
		prefs.Model = m
	}
	if req.Effort != nil {
		e := strings.TrimSpace(*req.Effort)
		if !validEffort(e) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "思考强度只能是 low/medium/high 或空"})
			return
		}
		prefs.Effort = e
	}
	c.setPrefs(name, prefs)
	c.log.Info("web prefs", "name", name, "model", prefs.Model, "effort", prefs.Effort)
	writeJSON(w, http.StatusOK, map[string]any{"model": prefs.Model, "effort": prefs.Effort})
}

// handleWorkspaceList 列目录（仅管理员，和 workspace_* 工具同一口径）。
func (c *Channel) handleWorkspaceList(w http.ResponseWriter, r *http.Request, name string) {
	if !IsAdminName(name, c.cfg.AdminUsers) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "只有管理员能浏览 workspace（web.adminUsers）"})
		return
	}
	rel := strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	if rel != "" && !SafeWorkspaceRel(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径非法"})
		return
	}
	dir := filepath.Join(c.workspaceRoot, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "目录不存在"})
		return
	}
	type wsEntry struct {
		Name  string `json:"name"`
		Dir   bool   `json:"dir"`
		Size  int64  `json:"size,omitempty"`
		Mtime int64  `json:"mtime,omitempty"`
	}
	out := make([]wsEntry, 0, len(entries))
	for _, e := range entries {
		if len(out) >= 500 {
			break
		}
		item := wsEntry{Name: e.Name(), Dir: e.IsDir()}
		if !e.IsDir() {
			if fi, err := e.Info(); err == nil {
				item.Size = fi.Size()
				item.Mtime = fi.ModTime().UnixMilli()
			}
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": out})
}

// handleWorkspaceFile 下载/预览 workspace 内文件（仅管理员）。
func (c *Channel) handleWorkspaceFile(w http.ResponseWriter, r *http.Request, name string) {
	if !IsAdminName(name, c.cfg.AdminUsers) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "只有管理员能访问 workspace（web.adminUsers）"})
		return
	}
	rel := strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	if rel == "" || !SafeWorkspaceRel(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径非法"})
		return
	}
	full := filepath.Join(c.workspaceRoot, filepath.FromSlash(rel))
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}
	w.Header().Set("Content-Type", MimeForServing(rel))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disp := "attachment"
	if IsImagePath(rel) {
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, urlEscape(filepath.Base(rel))))
	http.ServeFile(w, r, full)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// handleClear 清空当前账号会话（前端"新会话"），并广播 cleared 让其他标签页同步。
func (c *Channel) handleClear(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	if err := c.store.ClearSession(r.Context(), "web:c2c:"+name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.log.Info("web session cleared", "name", name)
	c.publishRaw(name, map[string]any{"type": "cleared"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Channel) handleEvents(w http.ResponseWriter, r *http.Request) {
	name, tok := c.auth(r)
	if name == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	c.setAuthCookie(w, r, tok)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "流式响应不可用"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch := make(chan []byte, 32)
	c.subscribe(name, ch)
	defer c.unsubscribe(name, ch)

	fmt.Fprintf(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleUpload 收一个文件：原始 body + ?name=<文件名>，存到
// workspace/web-files/<账号>/<毫秒时间戳>_<安全名>，返回可供 /api/send 引用的上传结果。
func (c *Channel) handleUpload(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	maxBytes := int64(c.cfg.MaxUploadMB) << 20
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	if r.ContentLength > maxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("文件超过 %dMB 上限", c.cfg.MaxUploadMB)})
		return
	}
	display := SafeUploadName(r.URL.Query().Get("name"))
	relDir := "web-files/" + name
	dir := filepath.Join(c.workspaceRoot, filepath.FromSlash(relDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建上传目录失败"})
		return
	}
	diskName := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), display)
	full := filepath.Join(dir, diskName)
	f, err := os.Create(full)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入失败：" + err.Error()})
		return
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		_ = os.Remove(full)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入失败：" + err.Error()})
		return
	}
	if n > maxBytes {
		_ = os.Remove(full)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("文件超过 %dMB 上限", c.cfg.MaxUploadMB)})
		return
	}
	// 用文件头猜 mime（比扩展名可靠），拿不到再按扩展名。
	mimeType := ""
	if fh, err := os.Open(full); err == nil {
		buf := make([]byte, 512)
		if m, _ := fh.Read(buf); m > 0 {
			mimeType = http.DetectContentType(buf[:m])
		}
		_ = fh.Close()
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		if m := MimeByPath(display); m != "" {
			mimeType = m
		}
	}
	file := UploadedFile{
		Path: relDir + "/" + diskName,
		Name: display,
		Mime: mimeType,
		Size: n,
	}
	c.log.Info("web upload", "user", name, "file", display, "bytes", n, "mime", mimeType)
	writeJSON(w, http.StatusOK, file)
}

// handleSend 收一条消息：text + 已上传文件的引用，转给 HandleUserMessage。
func (c *Channel) handleSend(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	var req struct {
		Text  string         `json:"text"`
		Files []UploadedFile `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数不是合法 JSON"})
		return
	}
	// 文件引用只能是自己上传目录里的（防引用别人/别的目录）。
	ownPrefix := "web-files/" + name + "/"
	files := make([]UploadedFile, 0, len(req.Files))
	for _, f := range req.Files {
		if !SafeWorkspaceRel(f.Path) || !strings.HasPrefix(f.Path, ownPrefix) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "附件引用非法"})
			return
		}
		f.Name = SafeUploadName(f.Name)
		f.Mime = sanitizeMarkerPart(f.Mime)
		if f.Mime == "" {
			f.Mime = MimeByPath(f.Path)
		}
		files = append(files, f)
	}
	if err := c.HandleUserMessage(r.Context(), name, req.Text, files); err != nil {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMsgFile 下载消息附件：
//   - agent 文件消息：路径来自消息 Target（file: 前缀）；
//   - 用户上传：路径来自消息文本标记的第 i 个。
//
// 只允许取自己会话的消息；用户上传还必须落在自己的 web-files/<账号>/ 下。
func (c *Channel) handleMsgFile(w http.ResponseWriter, r *http.Request) {
	name, tok := c.auth(r)
	if name == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	c.setAuthCookie(w, r, tok)
	id, err := strconv.ParseInt(r.URL.Query().Get("m"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数非法"})
		return
	}
	index, _ := strconv.Atoi(r.URL.Query().Get("i"))
	msg, err := c.store.MessageByID(r.Context(), id)
	if err != nil || msg == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "消息不存在"})
		return
	}
	sessionKey := "web:c2c:" + name
	if msg.SessionID != sessionKey {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "不是你的消息"})
		return
	}
	rel := MessageRelPath(*msg, index)
	if rel == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "附件不存在"})
		return
	}
	// 用户上传的消息（无 file: Target）必须在自己账号目录内。
	if !strings.HasPrefix(msg.Target, storage.KindFile) {
		if !strings.HasPrefix(rel, "web-files/"+name+"/") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "附件不属于你"})
			return
		}
	}
	full := filepath.Join(c.workspaceRoot, filepath.FromSlash(rel))
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件已不在"})
		return
	}
	w.Header().Set("Content-Type", MimeForServing(rel))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 中文文件名用 RFC 5987 编码，浏览器下载名才不会乱。
	fileName := MessageFileName(*msg, index)
	disp := "attachment"
	if IsImagePath(rel) {
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, urlEscape(fileName)))
	http.ServeFile(w, r, full)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func urlEscape(s string) string {
	// 轻量实现，避免引 net/url 只为 PathEscape 几行。
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
