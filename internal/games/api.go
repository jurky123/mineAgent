package games

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/storage"
)

// Authed 是门户的鉴权中间件签名（portal.requireUser）。
type Authed func(func(w http.ResponseWriter, r *http.Request, u *storage.User)) http.HandlerFunc

// API 是 /api/games/* 的处理器（按游戏 id 分发，不针对某个具体游戏）。
// auth 是门户的鉴权中间件（普通 JSON 接口用）；SSE 因为 EventSource 带不了 header，
// 单独用 account 校验 ?token=/cookie。
type API struct {
	log  *slog.Logger
	mgr  *Manager
	acct *account.Service
	auth Authed
}

func NewAPI(log *slog.Logger, mgr *Manager, acct *account.Service, auth Authed) *API {
	return &API{log: log, mgr: mgr, acct: acct, auth: auth}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/games", a.auth(a.handleList))
	mux.HandleFunc("/api/games/", a.handleDispatch) // /api/games/<id>/<action>
	return mux
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request, u *storage.User) {
	writeJSON(w, http.StatusOK, map[string]any{"games": List()})
}

// handleDispatch 解析 /api/games/<id>/<action...> 并把请求交给带鉴权的子处理器。
func (a *API) handleDispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/games/")
	if rest == "events" { // 通用房间事件流（不分游戏）
		a.handleEvents(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if _, ok := ByID(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "游戏不存在"})
		return
	}
	switch action {
	case "rooms":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleRooms(w, r, u, id) })(w, r)
	case "rooms/join":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleJoin(w, r, u, id) })(w, r)
	case "room":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleRoom(w, r, u, id) })(w, r)
	case "move":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleMove(w, r, u, id) })(w, r)
	case "resign":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleResign(w, r, u, id) })(w, r)
	case "leave":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleLeave(w, r, u, id) })(w, r)
	case "runs":
		a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) { a.handleRuns(w, r, u, id) })(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知操作"})
	}
}

func (a *API) handleRooms(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	switch r.Method {
	case http.MethodGet:
		list := a.mgr.OpenRooms(gameID)
		out := make([]map[string]any, 0, len(list))
		for _, room := range list {
			host := room.Sides[room.First]
			name := ""
			if host != nil {
				name = host.Name
			}
			out = append(out, map[string]any{
				"id": room.ID, "host": name, "createdAt": room.Created.UnixMilli(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
	case http.MethodPost:
		room, err := a.mgr.Create(gameID, &Player{ID: u.ID, Name: u.Username})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET/POST"})
	}
}

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	var req struct {
		Room string `json:"room"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数不是合法 JSON"})
		return
	}
	room, err := a.mgr.Join(&Player{ID: u.ID, Name: u.Username}, strings.ToUpper(strings.TrimSpace(req.Room)))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if room.GameID != gameID {
		// 加入了另一个游戏的房间：如实返回，让前端跳转
		writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID), "redirect": "/games/" + room.GameID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleRoom(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	room := a.mgr.RoomOf(u.ID)
	if room == nil || room.GameID != gameID {
		writeJSON(w, http.StatusOK, map[string]any{"room": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleMove(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体读取失败"})
		return
	}
	if err := a.mgr.Move(u.ID, json.RawMessage(body)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	room := a.mgr.RoomOf(u.ID)
	if room == nil {
		writeJSON(w, http.StatusOK, map[string]any{"room": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleResign(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	if err := a.mgr.Resign(u.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	room := a.mgr.RoomOf(u.ID)
	if room == nil {
		writeJSON(w, http.StatusOK, map[string]any{"room": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleLeave(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	a.mgr.Leave(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleRuns(w http.ResponseWriter, r *http.Request, u *storage.User, gameID string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.mgr.store.ListGameRuns(r.Context(), u.ID, gameID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if list == nil {
		list = []storage.GameRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list})
}

// handleEvents SSE：房间状态变化实时推送（EventSource 用 ?token= 鉴权）。
func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	u := a.acct.Current(r.Context(), tokenOf(r))
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "未登录"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "流式响应不可用"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	ch := a.mgr.Subscribe(u.ID)
	defer a.mgr.Unsubscribe(u.ID, ch)
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "event: room\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// tokenOf 从 Authorization / ?token= / cookie 取登录令牌（与 webui 同一套来源）。
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
