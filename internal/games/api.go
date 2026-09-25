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
	"mineagent/internal/games/chess"
	"mineagent/internal/storage"
)

// Authed 是门户的鉴权中间件签名（portal.requireUser）。
type Authed func(func(w http.ResponseWriter, r *http.Request, u *storage.User)) http.HandlerFunc

// API 是 /api/games/* 的处理器。auth 是门户的鉴权中间件（普通 JSON 接口用）；
// SSE 因为 EventSource 带不了 header，单独用 account 校验 ?token=/cookie。
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
	mux.HandleFunc("/api/games", a.auth(func(w http.ResponseWriter, r *http.Request, u *storage.User) {
		writeJSON(w, http.StatusOK, map[string]any{"games": List()})
	}))
	mux.HandleFunc("/api/games/chess/rooms", a.auth(a.handleRooms))
	mux.HandleFunc("/api/games/chess/rooms/join", a.auth(a.handleJoin))
	mux.HandleFunc("/api/games/chess/room", a.auth(a.handleRoom))
	mux.HandleFunc("/api/games/chess/move", a.auth(a.handleMove))
	mux.HandleFunc("/api/games/chess/resign", a.auth(a.handleResign))
	mux.HandleFunc("/api/games/chess/leave", a.auth(a.handleLeave))
	mux.HandleFunc("/api/games/chess/runs", a.auth(a.handleRuns))
	mux.HandleFunc("/api/games/chess/events", a.handleEvents)
	return mux
}

func (a *API) handleRooms(w http.ResponseWriter, r *http.Request, u *storage.User) {
	switch r.Method {
	case http.MethodGet:
		list := a.mgr.OpenRooms()
		out := make([]map[string]any, 0, len(list))
		for _, room := range list {
			out = append(out, map[string]any{
				"id": room.ID, "host": room.White.Name, "createdAt": room.Created.UnixMilli(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
	case http.MethodPost:
		room := a.mgr.Create(&Player{ID: u.ID, Name: u.Username})
		writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET/POST"})
	}
}

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request, u *storage.User) {
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
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleRoom(w http.ResponseWriter, r *http.Request, u *storage.User) {
	room := a.mgr.RoomOf(u.ID)
	if room == nil {
		writeJSON(w, http.StatusOK, map[string]any{"room": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleMove(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Promotion string `json:"promotion"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数不是合法 JSON"})
		return
	}
	from, ok1 := chess.ParseSquare(strings.ToLower(req.From))
	to, ok2 := chess.ParseSquare(strings.ToLower(req.To))
	if !ok1 || !ok2 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "坐标非法"})
		return
	}
	var promo byte
	if len(req.Promotion) == 1 {
		promo = strings.ToLower(req.Promotion)[0]
	}
	if err := a.mgr.Move(u.ID, from, to, promo); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	room := a.mgr.RoomOf(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"room": room.Snapshot(u.ID)})
}

func (a *API) handleResign(w http.ResponseWriter, r *http.Request, u *storage.User) {
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

func (a *API) handleLeave(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	a.mgr.Leave(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleRuns(w http.ResponseWriter, r *http.Request, u *storage.User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.mgr.store.ListGameRuns(r.Context(), u.ID, "chess", limit)
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
