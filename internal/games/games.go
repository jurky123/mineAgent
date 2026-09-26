// Package games 是门户的小游戏平台：注册表 + 房间管理 + HTTP API。
//
// 每个游戏 = 一份纯规则实现（internal/games/<id>）+ 一个 Match 适配器
// （match_<id>.go，把规则翻译成房间需要的动作/视图）。
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

	"mineagent/internal/storage"
)

var (
	errBadMove     = fmt.Errorf("坐标非法")
	errNotYourTurn = fmt.Errorf("还没轮到你走")
)

// Match 是一局棋的抽象：谁该走、走一步、给某一方的状态视图。
// 实现见 match_chess.go / match_gomoku.go；规则细节在各游戏包里。
type Match interface {
	// Turn 当前该谁走："white" / "black"。
	Turn() string
	// Play 执行一步（调用方保证 side == Turn()）；payload 是前端的原始 JSON。
	Play(side string, payload json.RawMessage) (PlayOutcome, error)
	// Snapshot 某一方的视图（含该游戏的展示字段）；side 为空表示观战。
	Snapshot(side string) map[string]any
}

// PlayOutcome 是一步走完的结果。
type PlayOutcome struct {
	Move   string // 记谱（展示用，如 e4 / Nf3 / h8）
	Last   string // 最后一步的坐标（前端高亮用，如 e2e4 / h8）
	Over   bool   // 是否终局
	Winner string // "white"/"black"/"draw"（Over 时有效）
	Reason string // checkmate/五连/认输…
}

// Game 注册表条目。
type Game struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Icon    string `json:"icon"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Players int    `json:"players"`

	FirstSide string       `json:"-"` // 房主执哪一方（象棋白先、五子棋黑先）
	NewMatch  func() Match `json:"-"` // 新一局（函数字段不能进 JSON）
}

var registry = []Game{
	{
		ID: "chess", Name: "国际象棋", Desc: "经典双人对战 · 服务端裁判", Icon: "chess-knight",
		Path: "/games/chess", Enabled: true, Players: 2,
		FirstSide: "white", NewMatch: func() Match { return newChessMatch() },
	},
	{
		ID: "gomoku", Name: "五子棋", Desc: "15 路棋盘 · 先连五者胜", Icon: "gomoku",
		Path: "/games/gomoku", Enabled: true, Players: 2,
		FirstSide: "black", NewMatch: func() Match { return newGomokuMatch() },
	},
}

// List 返回全部游戏（含未启用，前端展示"开发中"）。
func List() []Game { return registry }

// ByID 按 id 取游戏。
func ByID(id string) (Game, bool) {
	for _, g := range registry {
		if g.ID == id {
			return g, true
		}
	}
	return Game{}, false
}

// Enabled 某个游戏是否可用。
func Enabled(id string) bool {
	g, ok := ByID(id)
	return ok && g.Enabled
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

// Room 一局棋（任意游戏）。Sides 是 "white"/"black" 两方，未就位为 nil。
type Room struct {
	ID     string
	GameID string
	Sides  map[string]*Player
	First  string // 先手方（房主）
	Match  Match

	Status   string
	Result   string // white / black / draw
	Reason   string
	Moves    []string
	LastMove string
	Started  time.Time
	Created  time.Time
	Updated  time.Time
}

func (r *Room) players() []*Player {
	out := make([]*Player, 0, 2)
	for _, side := range []string{"white", "black"} {
		if p := r.Sides[side]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

// sideOf 返回该玩家执哪方（"" = 不在房间里）。
func (r *Room) sideOf(userID int64) string {
	for side, p := range r.Sides {
		if p != nil && p.ID == userID {
			return side
		}
	}
	return ""
}

// Snapshot 给前端的房间状态（游戏细节来自 Match.Snapshot）。
func (r *Room) Snapshot(forUser int64) map[string]any {
	side := r.sideOf(forUser)
	m := map[string]any{
		"id":        r.ID,
		"game":      r.GameID,
		"status":    r.Status,
		"result":    r.Result,
		"reason":    r.Reason,
		"turn":      r.Match.Turn(),
		"moves":     r.Moves,
		"lastMove":  r.LastMove,
		"startedAt": r.Started.UnixMilli(),
		"players": map[string]any{
			"white": playerJSON(r.Sides["white"]),
			"black": playerJSON(r.Sides["black"]),
		},
		"you": side,
	}
	for k, v := range r.Match.Snapshot(side) {
		m[k] = v
	}
	// 约定：走法提示只在真正对局中下发（等待/终局没有"能走哪里"这回事）。
	if r.Status != StatusPlaying {
		delete(m, "legalMoves")
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

// Create 建房（房主执先手，等待对手）。
func (m *Manager) Create(gameID string, p *Player) (*Room, error) {
	g, ok := ByID(gameID)
	if !ok || !g.Enabled {
		return nil, fmt.Errorf("游戏不存在或未开放")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detachLocked(p.ID) // 同时只在一个房间里
	r := &Room{
		ID:      m.newRoomIDLocked(),
		GameID:  gameID,
		Sides:   map[string]*Player{g.FirstSide: {ID: p.ID, Name: p.Name}},
		First:   g.FirstSide,
		Match:   g.NewMatch(),
		Status:  StatusWaiting,
		Created: time.Now(),
		Updated: time.Now(),
	}
	m.rooms[r.ID] = r
	m.byUser[p.ID] = r.ID
	m.log.Info("game room created", "game", gameID, "room", r.ID, "host", p.Name)
	m.publishLocked(r)
	return r, nil
}

// Join 加入房间（执后手）。
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
	if host := r.Sides[r.First]; host != nil && host.ID == p.ID {
		return nil, fmt.Errorf("这是你自己的房间")
	}
	other := "black"
	if r.First == "black" {
		other = "white"
	}
	m.detachLocked(p.ID)
	r.Sides[other] = &Player{ID: p.ID, Name: p.Name}
	r.Status = StatusPlaying
	r.Started = time.Now()
	r.Updated = time.Now()
	m.byUser[p.ID] = r.ID
	m.log.Info("game room joined", "game", r.GameID, "room", r.ID, "guest", p.Name)
	m.publishLocked(r)
	return r, nil
}

// RoomOf 用户当前所在房间（没有返回 nil）。
func (m *Manager) RoomOf(userID int64) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rooms[m.byUser[userID]]
}

// OpenRooms 某游戏等待加入的房间（新→旧）；gameID 为空 = 全部。
func (m *Manager) OpenRooms(gameID string) []*Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Room
	for _, r := range m.rooms {
		if r.Status != StatusWaiting {
			continue
		}
		if gameID != "" && r.GameID != gameID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Move 走一步（服务端校验：轮次/合法性由 Match 负责）。
func (m *Manager) Move(userID int64, payload json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.Status != StatusPlaying {
		return fmt.Errorf("对局还没开始或已结束")
	}
	side := r.sideOf(userID)
	if r.Match.Turn() != side {
		return fmt.Errorf("还没轮到你走")
	}
	out, err := r.Match.Play(side, payload)
	if err != nil {
		return err
	}
	r.Moves = append(r.Moves, out.Move)
	r.LastMove = out.Last
	r.Updated = time.Now()
	if out.Over {
		winner := out.Winner
		if winner == "" {
			winner = "draw"
		}
		m.finishLocked(r, winner, out.Reason)
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
	if r.sideOf(userID) == "white" {
		other = "black"
	}
	m.finishLocked(r, other, "resign")
	return nil
}

// Leave 离开：等待中解散房间；对局中算认输（对手还能看到终局）。
func (m *Manager) Leave(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[m.byUser[userID]]; r != nil && r.Status == StatusPlaying {
		other := "white"
		if r.sideOf(userID) == "white" {
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
		if r.sideOf(userID) == "white" {
			other = "black"
		}
		m.finishLocked(r, other, "leave")
	}
	m.leaveLocked(userID)
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
		if result != "draw" {
			if r.sideOf(p.ID) == result {
				outcome = "win"
			} else {
				outcome = "lose"
			}
		}
		meta, _ := json.Marshal(map[string]any{
			"room": r.ID, "reason": reason, "moves": len(r.Moves),
			"side": r.sideOf(p.ID), "opponent": opponentName(r, p.ID),
		})
		if _, err := m.store.AddGameRun(context.Background(), storage.GameRun{
			UserID: p.ID, GameID: r.GameID, Result: outcome,
			DurationMS: r.Updated.Sub(r.Started).Milliseconds(),
			Metadata:   string(meta), CreatedAt: time.Now().UnixMilli(),
		}); err != nil {
			m.log.Warn("save game run", "game", r.GameID, "room", r.ID, "user", p.Name, "err", err)
		}
	}
	m.publishLocked(r)
	m.log.Info("game room finished", "game", r.GameID, "room", r.ID, "result", result, "reason", reason, "moves", len(r.Moves))
}

func opponentName(r *Room, userID int64) string {
	for _, p := range r.players() {
		if p.ID != userID {
			return p.Name
		}
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
		m.log.Info("game room closed", "game", r.GameID, "room", roomID)
		return
	}
	for side, p := range r.Sides {
		if p != nil && p.ID == userID {
			r.Sides[side] = nil
		}
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
			m.log.Info("game room expired", "game", r.GameID, "room", id, "status", r.Status)
		}
	}
}
