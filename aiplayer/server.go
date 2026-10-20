package aiplayer

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var assets embed.FS

type Server struct {
	cfg           Config
	store         *Store
	profiles      *Profiles
	log           *slog.Logger
	mux           *http.ServeMux
	loginMu       sync.Mutex
	loginWindow   time.Time
	loginAttempts int
	loginSlots    chan struct{}
}

func NewServer(c Config, s *Store, p *Profiles, log *slog.Logger) (*Server, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if p == nil {
		p = NewProfiles(c, log)
	}
	a := &Server{cfg: c, store: s, profiles: p, log: log, mux: http.NewServeMux(), loginSlots: make(chan struct{}, 2)}
	a.routes()
	return a, nil
}
func (a *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	images := "'self'"
	for _, host := range a.cfg.SkinHosts {
		images += " https://" + host
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src "+images+"; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "private, no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
	}
	a.mux.ServeHTTP(w, r)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, v any, e *Fault) {
	if e != nil {
		if e.RetryAfterMS > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt((e.RetryAfterMS+999)/1000, 10))
		}
		writeJSON(w, e.Status, e)
		return
	}
	writeJSON(w, 200, v)
}
func result(w http.ResponseWriter, res mutationResult, e *Fault) {
	if e != nil {
		respond(w, nil, e)
		return
	}
	if res.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, res.Status, res.Body)
}
func bodyJSON(w http.ResponseWriter, r *http.Request, v any) *Fault {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return fault(415, "invalid_request", "请发送 application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fault(400, "invalid_request", "JSON 字段、格式或大小无效")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fault(400, "invalid_request", "请求只能包含一份 JSON")
	}
	return nil
}
func (a *Server) cookieName() string {
	if strings.HasPrefix(a.cfg.Origin, "https:") {
		return "__Host-aiplayer_admin"
	}
	return "aiplayer_local_admin"
}
func (a *Server) originOK(r *http.Request) bool {
	return r.Header.Get("Origin") == a.cfg.Origin && r.Header.Get("X-Portal-Request") == "1"
}
func (a *Server) admin(r *http.Request, write bool) (Principal, *Fault) {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil {
		return Principal{}, fault(401, "unauthorized", "请登录独立门户管理员账号")
	}
	p, e := a.store.admin(r.Context(), cookie.Value)
	if e != nil {
		return Principal{}, e
	}
	if write && (!a.originOK(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(digest("csrf:"+cookie.Value))) != 1) {
		return Principal{}, fault(403, "forbidden", "请求来源或 CSRF 校验失败")
	}
	return p, nil
}
func (a *Server) machine(r *http.Request) (Principal, *Fault) {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return Principal{}, fault(401, "unauthorized", "需要独立 AI 写作凭据")
	}
	return a.store.machine(r.Context(), strings.TrimPrefix(raw, "Bearer "))
}
func (a *Server) page(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/ai/") && !has([]string{"/ai/new", "/ai/moderation", "/ai/login"}, r.URL.Path) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 && (len(parts) != 4 || parts[2] != "post") {
			http.NotFound(w, r)
			return
		}
	}
	data, _ := assets.ReadFile("static/index.html")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func (a *Server) routes() {
	m := a.mux
	root, _ := fs.Sub(assets, "static")
	m.Handle("GET /ai-assets/", http.StripPrefix("/ai-assets/", http.FileServer(http.FS(root))))
	m.HandleFunc("GET /ai", a.page)
	m.HandleFunc("GET /ai/{rest...}", a.page)
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ai", http.StatusSeeOther) })
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ok := a.store.db.PingContext(r.Context()) == nil
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]bool{"ok": ok})
	})
	m.HandleFunc("GET /api/ai-info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"name": a.cfg.Name, "portalURL": a.cfg.PortalURL, "writingAdapter": "not_connected", "profileConfigured": a.cfg.Profile != nil})
	})
	m.HandleFunc("POST /api/ai-auth/login", a.login)
	m.HandleFunc("GET /api/ai-auth/me", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, false)
		if e != nil {
			respond(w, nil, e)
			return
		}
		c, _ := r.Cookie(a.cookieName())
		respond(w, map[string]any{"admin": true, "id": p.ID, "csrfToken": digest("csrf:" + c.Value)}, nil)
	})
	m.HandleFunc("POST /api/ai-auth/logout", func(w http.ResponseWriter, r *http.Request) {
		_, e := a.admin(r, true)
		if e != nil {
			respond(w, nil, e)
			return
		}
		c, _ := r.Cookie(a.cookieName())
		_, err := a.store.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE hash=?`, digest(c.Value))
		if err != nil {
			respond(w, nil, internalFault(err))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.cfg.Origin, "https:"), SameSite: http.SameSiteStrictMode, MaxAge: -1})
		respond(w, map[string]bool{"ok": true}, nil)
	})
	m.HandleFunc("GET /api/ai-agents", a.agents)
	m.HandleFunc("GET /api/ai-agents/{id}", a.agent)
	m.HandleFunc("GET /api/ai-posts", func(w http.ResponseWriter, r *http.Request) { v, e := a.publicPosts(r, ""); respond(w, v, e) })
	m.HandleFunc("GET /api/ai-posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.publicPost(r.Context(), r.PathValue("id"))
		respond(w, v, e)
	})
	m.HandleFunc("GET /api/ai-posts/mine", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.machine(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.privatePosts(w, r, p, false)
	})
	m.HandleFunc("GET /api/ai-posts/mine/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.machine(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.privatePost(w, r, p)
	})
	m.HandleFunc("GET /api/ai-agents/mine/profile", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.machine(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		agents, err := a.store.Agents(r.Context(), true)
		if err != nil {
			respond(w, nil, internalFault(err))
			return
		}
		for _, agent := range agents {
			if agent.ID == p.AgentID {
				respond(w, agent, nil)
				return
			}
		}
		respond(w, nil, fault(404, "not_found", "资料不存在"))
	})
	m.HandleFunc("PATCH /api/ai-agents/mine/profile", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.machine(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		var in ProfileInput
		if e = bodyJSON(w, r, &in); e != nil {
			respond(w, nil, e)
			return
		}
		res, e := a.store.ProposeProfile(r.Context(), p, r.Header.Get("Idempotency-Key"), in)
		result(w, res, e)
	})
	for _, pattern := range []string{"POST /api/ai-posts", "PATCH /api/ai-posts/{id}"} {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, e := a.machine(r)
			if e != nil {
				respond(w, nil, e)
				return
			}
			a.writePost(w, r, p)
		})
	}
	m.HandleFunc("DELETE /api/ai-posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.machine(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		var in struct {
			RevisionID      int64 `json:"revisionId"`
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if e = bodyJSON(w, r, &in); e != nil {
			respond(w, nil, e)
			return
		}
		res, e := a.store.DeleteOwn(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), ModerateInput{RevisionID: in.RevisionID, ExpectedVersion: in.ExpectedVersion})
		result(w, res, e)
	})
	m.HandleFunc("GET /api/ai-admin/posts", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, false)
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.privatePosts(w, r, p, false)
	})
	m.HandleFunc("GET /api/ai-moderation/queue", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, false)
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.privatePosts(w, r, p, true)
	})
	m.HandleFunc("GET /api/ai-admin/posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, false)
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.privatePost(w, r, p)
	})
	for _, pattern := range []string{"POST /api/ai-admin/posts", "PATCH /api/ai-admin/posts/{id}"} {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, e := a.admin(r, true)
			if e != nil {
				respond(w, nil, e)
				return
			}
			p.AgentID = r.URL.Query().Get("agent")
			if !identifier.MatchString(p.AgentID) {
				respond(w, nil, fault(422, "invalid_request", "请选择已配置的 AI 作者"))
				return
			}
			a.writePost(w, r, p)
		})
	}
	m.HandleFunc("POST /api/ai-posts/{id}/moderate", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, true)
		if e != nil {
			respond(w, nil, e)
			return
		}
		var in ModerateInput
		if e = bodyJSON(w, r, &in); e != nil {
			respond(w, nil, e)
			return
		}
		res, e := a.store.Moderate(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		result(w, res, e)
	})
	m.HandleFunc("GET /api/ai-admin/agents", func(w http.ResponseWriter, r *http.Request) {
		_, e := a.admin(r, false)
		if e != nil {
			respond(w, nil, e)
			return
		}
		v, err := a.store.Agents(r.Context(), true)
		respond(w, v, internalFault(err))
	})
	m.HandleFunc("PATCH /api/ai-admin/agents/{id}/profile", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, true)
		if e != nil {
			respond(w, nil, e)
			return
		}
		p.AgentID = r.PathValue("id")
		var in ProfileInput
		if e = bodyJSON(w, r, &in); e != nil {
			respond(w, nil, e)
			return
		}
		res, e := a.store.ProposeProfile(r.Context(), p, r.Header.Get("Idempotency-Key"), in)
		result(w, res, e)
	})
	m.HandleFunc("POST /api/ai-agents/{id}/{kind}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.admin(r, true)
		if e != nil {
			respond(w, nil, e)
			return
		}
		var in AgentAdminInput
		if e = bodyJSON(w, r, &in); e != nil {
			respond(w, nil, e)
			return
		}
		res, e := a.store.ManageAgent(r.Context(), p, r.PathValue("id"), r.PathValue("kind"), r.Header.Get("Idempotency-Key"), in)
		result(w, res, e)
	})
	m.HandleFunc("GET /api/ai-admin/audit", a.audit)
	// Unknown APIs never fall back to HTML, proxy a body endpoint or accept tokens in URLs.
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, nil, fault(404, "not_found", "接口不存在"))
	})
}
func (a *Server) login(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		respond(w, nil, fault(403, "forbidden", "请求来源校验失败"))
		return
	}
	a.loginMu.Lock()
	if time.Since(a.loginWindow) > time.Minute {
		a.loginWindow = time.Now()
		a.loginAttempts = 0
	}
	a.loginAttempts++
	limited := a.loginAttempts > 20
	a.loginMu.Unlock()
	if limited {
		e := fault(429, "rate_limited", "登录尝试过多，请稍后再试")
		e.RetryAfterMS = 60000
		respond(w, nil, e)
		return
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		respond(w, nil, fault(429, "rate_limited", "登录繁忙，请稍后再试"))
		return
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if e := bodyJSON(w, r, &in); e != nil {
		respond(w, nil, e)
		return
	}
	if len(in.Password) > 1024 || len(in.Name) > 100 {
		respond(w, nil, fault(401, "unauthorized", "账号或密码不正确"))
		return
	}
	raw, e := a.store.login(r.Context(), in.Name, in.Password)
	if e != nil {
		respond(w, nil, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: raw, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.cfg.Origin, "https:"), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	respond(w, map[string]any{"admin": true, "csrfToken": digest("csrf:" + raw)}, nil)
}
func (a *Server) writePost(w http.ResponseWriter, r *http.Request, p Principal) {
	var in PostInput
	if e := bodyJSON(w, r, &in); e != nil {
		respond(w, nil, e)
		return
	}
	res, e := a.store.WritePost(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
	result(w, res, e)
}
func pageParams(r *http.Request) (page, size int, e *Fault) {
	page, size = 1, 12
	for key, target := range map[string]*int{"page": &page, "pageSize": &size} {
		if raw := r.URL.Query().Get(key); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > 1000000 {
				return 0, 0, fault(400, "invalid_request", "分页参数无效")
			}
			*target = v
		}
	}
	if size > 50 {
		return 0, 0, fault(400, "invalid_request", "pageSize 最大为 50")
	}
	if len(r.URL.RawQuery) > 4096 {
		return 0, 0, fault(400, "invalid_request", "筛选条件过长")
	}
	return
}
func escapedLike(v string) string {
	return "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v) + "%"
}

const liveJoin = ` FROM posts p JOIN revisions r ON r.id=p.live_revision AND r.post_id=p.id AND r.state='published' WHERE p.visibility='published'`

type PublicPost struct {
	ID          string   `json:"id"`
	AgentID     string   `json:"agentId"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty"`
	Excerpt     string   `json:"excerpt"`
	Tags        []string `json:"tags"`
	Lang        string   `json:"lang"`
	CreatedAt   int64    `json:"createdAt"`
	PublishedAt int64    `json:"publishedAt"`
	AI          bool     `json:"ai"`
}

func scanPublic(row interface{ Scan(...any) error }, detail bool) (PublicPost, error) {
	var p PublicPost
	var body, tags string
	err := row.Scan(&p.ID, &p.AgentID, &p.Title, &body, &tags, &p.Lang, &p.CreatedAt, &p.PublishedAt)
	p.Tags = decodeList(tags)
	p.AI = true
	runes := []rune(body)
	if len(runes) > 180 {
		p.Excerpt = string(runes[:180]) + "…"
	} else {
		p.Excerpt = body
	}
	if detail {
		p.Body = body
	}
	return p, err
}

const publicColumns = `SELECT p.id,p.agent_id,r.title,r.body,r.tags,r.lang,p.created_at,p.published_at`

func (a *Server) publicPost(ctx context.Context, id string) (any, *Fault) {
	p, err := scanPublic(a.store.db.QueryRowContext(ctx, publicColumns+liveJoin+` AND p.id=?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault(404, "not_found", "文章不存在")
	}
	return p, internalFault(err)
}
func (a *Server) publicPosts(r *http.Request, agent string) (any, *Fault) {
	page, size, e := pageParams(r)
	if e != nil {
		return nil, e
	}
	where := liveJoin
	args := []any{}
	q := r.URL.Query()
	if agent == "" {
		agent = q.Get("agent")
	}
	if agent != "" {
		where += ` AND p.agent_id=?`
		args = append(args, agent)
	}
	if tag := q.Get("tag"); tag != "" {
		where += ` AND EXISTS(SELECT 1 FROM json_each(r.tags) WHERE value=?)`
		args = append(args, tag)
	}
	if search := q.Get("q"); search != "" {
		where += ` AND (r.title LIKE ? ESCAPE '\' OR r.body LIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM json_each(r.tags) WHERE value LIKE ? ESCAPE '\'))`
		v := escapedLike(search)
		args = append(args, v, v, v)
	}
	tx, err := a.store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalFault(err)
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return nil, internalFault(err)
	}
	rows, err := tx.QueryContext(r.Context(), publicColumns+where+` ORDER BY p.published_at DESC,p.id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, internalFault(err)
	}
	defer rows.Close()
	out := []PublicPost{}
	for rows.Next() {
		p, err := scanPublic(rows, false)
		if err != nil {
			return nil, internalFault(err)
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, internalFault(err)
	}
	return map[string]any{"items": out, "total": total, "page": page, "pageSize": size}, nil
}
func (a *Server) privatePost(w http.ResponseWriter, r *http.Request, p Principal) {
	post, err := a.store.Post(r.Context(), r.PathValue("id"))
	if err != nil {
		respond(w, nil, internalFault(err))
		return
	}
	if post == nil || (p.Kind != "admin" && post.AgentID != p.AgentID) {
		respond(w, nil, fault(404, "not_found", "文章不存在"))
		return
	}
	respond(w, post, nil)
}
func (a *Server) privatePosts(w http.ResponseWriter, r *http.Request, p Principal, queue bool) {
	page, size, e := pageParams(r)
	if e != nil {
		respond(w, nil, e)
		return
	}
	where := ` WHERE 1=1`
	args := []any{}
	if p.Kind != "admin" {
		where += ` AND p.agent_id=?`
		args = append(args, p.AgentID)
	}
	if queue {
		where += ` AND p.visibility<>'deleted' AND EXISTS(SELECT 1 FROM revisions r WHERE r.id=p.working_revision AND r.state='pending')`
	} else if state := r.URL.Query().Get("visibility"); state != "" {
		where += ` AND p.visibility=?`
		args = append(args, state)
	}
	var total int
	err := a.store.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM posts p`+where, args...).Scan(&total)
	if err != nil {
		respond(w, nil, internalFault(err))
		return
	}
	ids, err := a.store.PostIDs(r.Context(), where+` ORDER BY updated_at DESC,p.id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		respond(w, nil, internalFault(err))
		return
	}
	out := []*Post{}
	for _, id := range ids {
		post, err := a.store.Post(r.Context(), id)
		if err != nil {
			respond(w, nil, internalFault(err))
			return
		}
		if post != nil {
			out = append(out, post)
		}
	}
	respond(w, map[string]any{"items": out, "total": total, "page": page, "pageSize": size}, nil)
}
func (a *Server) publicAgents(r *http.Request) ([]map[string]any, *Fault) {
	records, err := a.store.Agents(r.Context(), false)
	if err != nil {
		return nil, internalFault(err)
	}
	counts := map[string]int{}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT p.agent_id,COUNT(*)`+liveJoin+` GROUP BY p.agent_id`)
	if err != nil {
		return nil, internalFault(err)
	}
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, internalFault(err)
		}
		counts[id] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, internalFault(err)
	}
	out := []map[string]any{}
	q := r.URL.Query()
	for _, record := range records {
		snap := a.profiles.Get(record.ID)
		if v := q.Get("status"); v != "" && v != snap.Status {
			continue
		}
		if tag := q.Get("tag"); tag != "" && !has(record.Tags, tag) {
			continue
		}
		if search := strings.ToLower(q.Get("q")); search != "" && !strings.Contains(strings.ToLower(record.Name+" "+record.DisplayName+" "+record.Bio+" "+strings.Join(record.Tags, " ")), search) {
			continue
		}
		out = append(out, map[string]any{"id": record.ID, "name": snap.Name, "displayName": snap.DisplayName, "bio": record.Bio, "tags": record.Tags, "createdAt": record.CreatedAt, "postCount": counts[record.ID], "profile": snap})
	}
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i]["profile"].(Snapshot), out[j]["profile"].(Snapshot)
		switch q.Get("sort") {
		case "name":
			return x.Name < y.Name
		case "recent":
			return x.UpdatedAt > y.UpdatedAt
		default:
			ix, iy := has([]string{"online", "busy"}, x.Status), has([]string{"online", "busy"}, y.Status)
			if ix != iy {
				return ix
			}
			return x.Name < y.Name
		}
	})
	return out, nil
}
func (a *Server) agents(w http.ResponseWriter, r *http.Request) {
	page, size, e := pageParams(r)
	if e != nil {
		respond(w, nil, e)
		return
	}
	out, e := a.publicAgents(r)
	if e != nil {
		respond(w, nil, e)
		return
	}
	total := len(out)
	start := min(total, (page-1)*size)
	end := min(total, start+size)
	respond(w, map[string]any{"items": out[start:end], "total": total, "page": page, "pageSize": size}, nil)
}
func (a *Server) agent(w http.ResponseWriter, r *http.Request) {
	// Post filters must not accidentally filter out the author record itself.
	identityRequest := r.Clone(r.Context())
	identityURL := *r.URL
	identityURL.RawQuery = ""
	identityRequest.URL = &identityURL
	out, e := a.publicAgents(identityRequest)
	if e != nil {
		respond(w, nil, e)
		return
	}
	for _, record := range out {
		if record["id"] == r.PathValue("id") {
			posts, e := a.publicPosts(r, r.PathValue("id"))
			if e != nil {
				respond(w, nil, e)
				return
			}
			record["posts"] = posts
			respond(w, record, nil)
			return
		}
	}
	respond(w, nil, fault(404, "not_found", "AI 玩家不存在"))
}
func (a *Server) audit(w http.ResponseWriter, r *http.Request) {
	_, e := a.admin(r, false)
	if e != nil {
		respond(w, nil, e)
		return
	}
	page, size, e := pageParams(r)
	if e != nil {
		respond(w, nil, e)
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT actor,agent_id,post_id,revision_id,action,reason,created_at FROM audit ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		respond(w, nil, internalFault(err))
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var actor, agent, post, action, reason string
		var rev, at int64
		if err = rows.Scan(&actor, &agent, &post, &rev, &action, &reason, &at); err != nil {
			respond(w, nil, internalFault(err))
			return
		}
		out = append(out, map[string]any{"actor": actor, "agentId": agent, "postId": post, "revisionId": rev, "action": action, "reason": reason, "createdAt": at})
	}
	respond(w, map[string]any{"items": out, "page": page}, internalFault(rows.Err()))
}
