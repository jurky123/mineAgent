// Package games 是门户的小游戏平台：注册表 + 房间管理 + HTTP API。
//
// v1 只有国际象棋（服务端权威规则见 internal/games/chess）。
// 房间在内存里（重启即清），一局结束把原始记录写进 game_runs（积分/排行榜模型未定）。
package games

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"mineagent/internal/games/chess"
	"mineagent/internal/storage"
)

// Game 注册表条目（门户/大厅展示用）。
type Game struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Icon    string `json:"icon"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Players int    `json:"players"` // 每局人数
}

var registry = []Game{
	{ID: "chess", Name: "国际象棋", Desc: "在线房间对战 · 服务端裁判", Icon: "♞", Path: "/games/chess", Enabled: true, Players: 2},
}

// List 返回全部游戏（含未启用，前端展示"开发中"）。
func List() []Game { return registry }

// Enabled 某个游戏是否可用。
func Enabled(id string) bool {
	for _, g := range registry {
		if g.ID == id {
			return g.Enabled
		}
	}
	return false
}

// ---------- 房间 ----------

const (
	StatusWaiting  = "waiting"
	StatusPlaying  = "playing"
	StatusFinished = "finished"

	waitingTTL  = 30 * time.Minute
	playingTTL  = 2 * time.Hour
	finishedTTL = 30 * time.Minute
)

type Player struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Room 一局棋。White/Black 是玩家，Guest 为空表示还没人加入。
type Room struct {
	ID      string
	White   *Player
	Black   *Player
	Board   *chess.Board
	Status  string
	Result  string // white / black / draw / abort
	Reason  string // checkmate / stalemate / resign / leave / abort
	Moves   []string
	Last    string
	Started time.Time
	Created time.Time
	Updated time.Time
}

func (r *Room) players() []*Player {
	out := make([]*Player, 0, 2)
	if r.White != nil {
		out = append(out, r.White)
	}
	if r.Black != nil {
		out = append(out, r.Black)
	}
	return out
}

func (r *Room) has(p *Player) bool {
	return r.White != nil && r.White.ID == p.ID || r.Black != nil && r.Black.ID == p.ID
}

// colorOf 返回该玩家的颜色（"white"/"black"/""）。
func (r *Room) colorOf(userID int64) string {
	if r.White != nil && r.White.ID == userID {
		return "white"
	}
	if r.Black != nil && r.Black.ID == userID {
		return "black"
	}
	return ""
}

// Snapshot 给前端的房间状态（含"你的合法走法"，只在轮到你时下发）。
func (r *Room) Snapshot(forUser int64) map[string]any {
	m := map[string]any{
		"id":        r.ID,
		"game":      "chess",
		"status":    r.Status,
		"result":    r.Result,
		"reason":    r.Reason,
		"pieces":    r.Board.Pieces(),
		"turn":      r.Board.Turn.String(),
		"inCheck":   r.Board.InCheck(r.Board.Turn),
		"ply":       r.Board.Ply,
		"lastMove":  r.Last,
		"moves":     r.Moves,
		"startedAt": r.Started.UnixMilli(),
		"players": map[string]any{
			"white": playerJSON(r.White),
			"black": playerJSON(r.Black),
		},
		"you": r.colorOf(forUser),
	}
	if r.Status == StatusPlaying && r.colorOf(forUser) != "" {
		want := chess.White
		if r.colorOf(forUser) == "black" {
			want = chess.Black
		}
		if r.Board.Turn == want {
			legal := r.Board.LegalMoves()
			list := make([]string, 0, len(legal))
			for _, mv := range legal {
				list = append(list, mv.String())
			}
			m["legalMoves"] = list
		}
	}
	return m
}

func playerJSON(p *Player) any {
	if p == nil {
		return nil
	}
	return map[string]any{"id": p.ID, "name": p.Name}
}

// Manager 房间与订阅管理（单进程内存态）。
type Manager struct {
	log   *slog.Logger
	store *storage.Store

	mu     sync.Mutex
	rooms  map[string]*Room
	byUser map[int64]string
	subs   map[int64]map[chan []byte]struct{}
	seq    int
}

func NewManager(log *slog.Logger, store *storage.Store) *Manager {
	m := &Manager{
		log:    log,
		store:  store,
		rooms:  make(map[string]*Room),
		byUser: make(map[int64]string),
		subs:   make(map[int64]map[chan []byte]struct{}),
	}
	go m.cleanLoop()
	return m
}

// Create 建房（房主执白，等待对手）。
func (m *Manager) Create(p *Player) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detachLocked(p.ID) // 同时只在一个房间里
	r := &Room{
		ID:      m.newRoomIDLocked(),
		White:   &Player{ID: p.ID, Name: p.Name},
		Board:   chess.Start(),
		Status:  StatusWaiting,
		Created: time.Now(),
		Updated: time.Now(),
	}
	m.rooms[r.ID] = r
	m.byUser[p.ID] = r.ID
	m.log.Info("chess room created", "room", r.ID, "host", p.Name)
	m.publishLocked(r)
	return r
}

// Join 加入房间（执黑），返回错误原因。
func (m *Manager) Join(p *Player, roomID string) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[roomID]
	if r == nil {
		return nil, fmt.Errorf("房间不存在或已解散")
	}
	if r.Status != StatusWaiting {
		return nil, fmt.Errorf("房间已在对局中")
	}
	if r.White != nil && r.White.ID == p.ID {
		return nil, fmt.Errorf("这是你自己的房间")
	}
	m.detachLocked(p.ID)
	r.Black = &Player{ID: p.ID, Name: p.Name}
	r.Status = StatusPlaying
	r.Started = time.Now()
	r.Updated = time.Now()
	m.byUser[p.ID] = r.ID
	m.log.Info("chess room joined", "room", r.ID, "guest", p.Name)
	m.publishLocked(r)
	return r, nil
}

// RoomOf 用户当前所在房间（没有返回 nil）。
func (m *Manager) RoomOf(userID int64) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rooms[m.byUser[userID]]
}

// OpenRooms 等待加入的房间列表（新→旧）。
func (m *Manager) OpenRooms() []*Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Room
	for _, r := range m.rooms {
		if r.Status == StatusWaiting {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Move 走一步（服务端校验）。
func (m *Manager) Move(userID int64, from, to int, promo byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.Status != StatusPlaying {
		return fmt.Errorf("对局还没开始或已结束")
	}
	want := chess.White
	if r.colorOf(userID) == "black" {
		want = chess.Black
	}
	if r.Board.Turn != want {
		return fmt.Errorf("还没轮到你走")
	}
	res, err := r.Board.Play(from, to, promo)
	if err != nil {
		return err
	}
	r.Moves = append(r.Moves, res.Move.String())
	r.Last = res.Move.String()
	r.Updated = time.Now()
	if res.Checkmate {
		m.finishLocked(r, winnerColor(r.Board.Turn), "checkmate")
	} else if res.Stalemate {
		m.finishLocked(r, "draw", "stalemate")
	} else {
		m.publishLocked(r)
	}
	return nil
}

// Resign 认输。
func (m *Manager) Resign(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.Status == StatusWaiting {
		m.leaveLocked(userID)
		return nil
	}
	if r.Status != StatusPlaying {
		return fmt.Errorf("对局已结束")
	}
	other := "white"
	if r.colorOf(userID) == "white" {
		other = "black"
	}
	m.finishLocked(r, other, "resign")
	return nil
}

// Leave 离开：等待中解散房间；对局中算认输（对手还能看到终局棋盘）。
func (m *Manager) Leave(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[m.byUser[userID]]; r != nil && r.Status == StatusPlaying {
		other := "white"
		if r.colorOf(userID) == "white" {
			other = "black"
		}
		m.finishLocked(r, other, "leave")
	}
	m.leaveLocked(userID)
}

// detachLocked 为新房间腾位置：对局中就判对手赢，然后再解除关联。
func (m *Manager) detachLocked(userID int64) {
	if r := m.rooms[m.byUser[userID]]; r != nil && r.Status == StatusPlaying {
		other := "white"
		if r.colorOf(userID) == "white" {
			other = "black"
		}
		m.finishLocked(r, other, "leave")
	}
	m.leaveLocked(userID)
}

func winnerColor(turn chess.Color) string {
	if turn == chess.White {
		return "black" // 轮到的这方被将杀，对方赢
	}
	return "white"
}

// finishLocked 结束对局并落库（调用方持锁）。
func (m *Manager) finishLocked(r *Room, result, reason string) {
	r.Status = StatusFinished
	r.Result = result
	r.Reason = reason
	r.Updated = time.Now()
	for _, p := range r.players() {
		if p == nil || m.store == nil {
			continue
		}
		outcome := "draw"
		if result == "draw" {
			outcome = "draw"
		} else if r.colorOf(p.ID) == result {
			outcome = "win"
		} else {
			outcome = "lose"
		}
		meta, _ := json.Marshal(map[string]any{
			"room": r.ID, "reason": reason, "moves": len(r.Moves),
			"color": r.colorOf(p.ID), "opponent": opponentName(r, p.ID),
		})
		if _, err := m.store.AddGameRun(context.Background(), storage.GameRun{
			UserID: p.ID, GameID: "chess", Result: outcome,
			DurationMS: r.Updated.Sub(r.Started).Milliseconds(),
			Metadata:   string(meta), CreatedAt: time.Now().UnixMilli(),
		}); err != nil {
			m.log.Warn("save chess game run", "room", r.ID, "user", p.Name, "err", err)
		}
	}
	m.publishLocked(r)
	m.log.Info("chess room finished", "room", r.ID, "result", result, "reason", reason, "moves", len(r.Moves))
}

func opponentName(r *Room, userID int64) string {
	if r.White != nil && r.White.ID != userID {
		return r.White.Name
	}
	if r.Black != nil && r.Black.ID != userID {
		return r.Black.Name
	}
	return ""
}

// leaveLocked 清掉用户与房间的关联；等待中的房间直接删。
func (m *Manager) leaveLocked(userID int64) {
	roomID := m.byUser[userID]
	if roomID == "" {
		return
	}
	delete(m.byUser, userID)
	r := m.rooms[roomID]
	if r == nil {
		return
	}
	if r.Status == StatusWaiting {
		delete(m.rooms, roomID)
		m.log.Info("chess room closed", "room", roomID)
		return
	}
	if r.White != nil && r.White.ID == userID {
		r.White = nil
	}
	if r.Black != nil && r.Black.ID == userID {
		r.Black = nil
	}
}

// ---------- 订阅（SSE） ----------

// Subscribe 订阅自己的房间事件。
func (m *Manager) Subscribe(userID int64) chan []byte {
	ch := make(chan []byte, 16)
	m.mu.Lock()
	if m.subs[userID] == nil {
		m.subs[userID] = make(map[chan []byte]struct{})
	}
	m.subs[userID][ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(userID int64, ch chan []byte) {
	m.mu.Lock()
	if set := m.subs[userID]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(m.subs, userID)
		}
	}
	m.mu.Unlock()
}

// publishLocked 把房间快照推给房间里的玩家（调用方持锁）。
func (m *Manager) publishLocked(r *Room) {
	for _, p := range r.players() {
		if p == nil {
			continue
		}
		data, err := json.Marshal(map[string]any{"type": "room", "room": r.Snapshot(p.ID)})
		if err != nil {
			continue
		}
		for ch := range m.subs[p.ID] {
			select {
			case ch <- data:
			default:
			}
		}
	}
}

// newRoomIDLocked 6 位房间码（去掉易混字符）。
func (m *Manager) newRoomIDLocked() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			m.seq++
			return fmt.Sprintf("R%05d", m.seq%100000)
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		id := string(b)
		if m.rooms[id] == nil {
			return id
		}
	}
}

// cleanLoop 定期清理过期房间。
func (m *Manager) cleanLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		m.clean()
	}
}

func (m *Manager) clean() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, r := range m.rooms {
		var ttl time.Duration
		switch r.Status {
		case StatusWaiting:
			ttl = waitingTTL
		case StatusPlaying:
			ttl = playingTTL
		default:
			ttl = finishedTTL
		}
		if now.Sub(r.Updated) > ttl {
			for _, p := range r.players() {
				if p != nil && m.byUser[p.ID] == id {
					delete(m.byUser, p.ID)
				}
			}
			delete(m.rooms, id)
			m.log.Info("chess room expired", "room", id, "status", r.Status)
		}
	}
}
