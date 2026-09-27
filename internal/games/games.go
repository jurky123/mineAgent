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
	"strings"
	"sync"
	"time"

	"mineagent/internal/storage"
)

var (
	errBadMove     = fmt.Errorf("坐标非法")
	errNotYourTurn = fmt.Errorf("还没轮到你走")
	errNoOwnMove   = fmt.Errorf("你还没走过棋，没有可悔的")
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
	// PromptContext 给"AI 解说"的局面描述（现在的局面 + 刚刚这一步）。
	PromptContext() string
	// CanUndo 只读校验：请求者是否有可悔的棋（没走过棋就不行）。
	CanUndo(side string) error
	// Undo 悔棋：撤销到请求者上一手之前（通常 1-2 步），返回剩余记谱/原始着法与最后一步坐标。
	Undo(side string) (moves []string, raws []string, last string, err error)
}

// PlayOutcome 是一步走完的结果。
type PlayOutcome struct {
	Move   string // 记谱（展示用，如 e4 / Nf3 / h8）
	Raw    string // 原始着法（回放用：象棋 UCI e2e4，五子棋坐标 h8）
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

	// undoTTL 悔棋请求的有效期（超时视为自动放弃）。
	undoTTL = 2 * time.Minute
)

// UndoReq 是一条待处理的悔棋请求（需要对手同意）。
type UndoReq struct {
	By string // 请求方 side
	At time.Time
}

func (u *UndoReq) fresh(now time.Time) bool { return u != nil && now.Sub(u.At) < undoTTL }

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
	RawMoves []string // 与 Moves 一一对应的原始着法（回放用）
	LastMove string
	UndoReq  *UndoReq
	SwapReq  *UndoReq // 换边请求（与悔棋同样的 2 分钟有效期）
	DrawReq  *UndoReq // 提和请求（同样 2 分钟有效）

	// 解说：ply -> 文案（ply = 第几手，从 1 开始）；commenting 防重复生成。
	Comments   map[int]string
	commenting map[int]bool
	// CommentaryEnabled 服务端是否配置了解说模型（前端据此显示/隐藏解说面板）。
	CommentaryEnabled bool
	Started           time.Time
	Created           time.Time
	Updated           time.Time
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
		"first":     r.First,
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
	// 解说（ply -> 文案），便于刷新/重连后还能看到
	m["commentary"] = r.CommentaryEnabled
	if len(r.Comments) > 0 {
		m["comments"] = r.Comments
	}
	// 待处理的悔棋 / 换边请求（超时的不下发）
	if r.Status == StatusPlaying {
		now := time.Now()
		if r.UndoReq.fresh(now) {
			m["undoReq"] = map[string]any{"by": r.UndoReq.By, "at": r.UndoReq.At.UnixMilli()}
		}
		if r.SwapReq.fresh(now) {
			m["swapReq"] = map[string]any{"by": r.SwapReq.By, "at": r.SwapReq.At.UnixMilli()}
		}
		if r.DrawReq.fresh(now) {
			m["drawReq"] = map[string]any{"by": r.DrawReq.By, "at": r.DrawReq.At.UnixMilli()}
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

	mu          sync.Mutex
	rooms       map[string]*Room
	byUser      map[int64]string
	subs        map[int64]map[chan []byte]struct{}
	seq         int
	commentator func(ctx context.Context, prompt string) (string, error)
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
// commentator 由 main 注入（internal/commentary）；nil = 关闭解说。
func (m *Manager) SetCommentator(fn func(ctx context.Context, prompt string) (string, error)) {
	m.commentator = fn
}

func (m *Manager) CommentaryEnabled() bool { return m.commentator != nil }

// RequestComment 为"最后一手"生成解说：已有就直接返回，正在生成返回 pending。
func (m *Manager) RequestComment(ctx context.Context, userID int64) (text string, pending bool, err error) {
	m.mu.Lock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		m.mu.Unlock()
		return "", false, fmt.Errorf("你不在任何房间里")
	}
	if m.commentator == nil {
		m.mu.Unlock()
		return "", false, fmt.Errorf("解说未启用")
	}
	ply := len(r.Moves)
	if ply == 0 {
		m.mu.Unlock()
		return "", false, fmt.Errorf("还没有可解说的棋")
	}
	if r.Comments == nil {
		r.Comments = map[int]string{}
	}
	if r.commenting == nil {
		r.commenting = map[int]bool{}
	}
	if v, ok := r.Comments[ply]; ok {
		m.mu.Unlock()
		return v, false, nil
	}
	if r.commenting[ply] {
		m.mu.Unlock()
		return "", true, nil
	}
	r.commenting[ply] = true
	roomID := r.ID
	prompt := r.Match.PromptContext()
	// 带上之前的解说，让解说员延续同一套叙事（"agent 历史进程"）
	if len(r.Comments) > 0 {
		keys := make([]int, 0, len(r.Comments))
		for k := range r.Comments {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		if len(keys) > 6 {
			keys = keys[len(keys)-6:]
		}
		var prev strings.Builder
		for _, k := range keys {
			prev.WriteString(fmt.Sprintf("第%d手：%s\n", k, r.Comments[k]))
		}
		prompt += "\n你之前的解说（保持连贯、不要重复）：\n" + prev.String()
	}
	side := r.sideOf(userID)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
	defer cancel()
	comment, genErr := m.commentator(ctx, prompt)

	m.mu.Lock()
	defer m.mu.Unlock()
	r = m.rooms[roomID]
	if r == nil {
		return "", false, genErr
	}
	delete(r.commenting, ply)
	if genErr != nil {
		m.log.Warn("commentary failed", "game", r.GameID, "room", r.ID, "ply", ply, "err", genErr)
		return "", false, genErr
	}
	r.Comments[ply] = comment
	r.Updated = time.Now()
	m.log.Info("commentary ready", "game", r.GameID, "room", r.ID, "ply", ply, "by", side)
	m.publishEventLocked(r, map[string]any{"type": "comment", "ply": ply, "text": comment})
	return comment, false, nil
}

// publishEventLocked 给房间里的玩家推一条自定义 SSE 事件（调用方持锁）。
func (m *Manager) publishEventLocked(r *Room, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, p := range r.players() {
		if p == nil {
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

func (m *Manager) Create(gameID string, p *Player) (*Room, error) {
	g, ok := ByID(gameID)
	if !ok || !g.Enabled {
		return nil, fmt.Errorf("游戏不存在或未开放")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detachLocked(p.ID) // 同时只在一个房间里
	r := &Room{
		ID:                m.newRoomIDLocked(),
		GameID:            gameID,
		Sides:             map[string]*Player{g.FirstSide: {ID: p.ID, Name: p.Name}},
		First:             g.FirstSide,
		Match:             g.NewMatch(),
		Status:            StatusWaiting,
		Created:           time.Now(),
		Updated:           time.Now(),
		CommentaryEnabled: m.commentator != nil,
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
	if r.sideOf(p.ID) != "" {
		return nil, fmt.Errorf("这是你自己的房间")
	}
	// 后手方取"空着的那一侧"（房主等待中可能换过边，不一定是 First 侧）
	other := ""
	for _, side := range []string{"white", "black"} {
		if r.Sides[side] == nil {
			other = side
			break
		}
	}
	if other == "" {
		return nil, fmt.Errorf("房间已满")
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

// ActiveRoom 是给运维看的房间摘要（部署前检查有没有人正在下棋）。
type ActiveRoom struct {
	ID      string `json:"id"`
	Game    string `json:"game"`
	Status  string `json:"status"`
	Players int    `json:"players"`
	Moves   int    `json:"moves"`
	Updated int64  `json:"updatedAt"`
}

// ActiveRooms 返回全部未结束的房间（等待 + 对局中）。
func (m *Manager) ActiveRooms() []ActiveRoom {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ActiveRoom
	for _, r := range m.rooms {
		if r.Status == StatusFinished {
			continue
		}
		out = append(out, ActiveRoom{
			ID: r.ID, Game: r.GameID, Status: r.Status,
			Players: len(r.players()), Moves: len(r.Moves),
			Updated: r.Updated.UnixMilli(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
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
	r.RawMoves = append(r.RawMoves, out.Raw)
	r.LastMove = out.Last
	r.UndoReq = nil // 走了新的一步，之前的悔棋/换边/提和请求作废
	r.SwapReq = nil
	r.DrawReq = nil
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

// UndoRequest 请求悔棋（等对手同意）。
func (m *Manager) UndoRequest(userID int64) error {
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
	if side == "" {
		return fmt.Errorf("你不在这个房间里")
	}
	if len(r.Moves) == 0 {
		return fmt.Errorf("还没有可悔的棋")
	}
	if err := r.Match.CanUndo(side); err != nil {
		return err // 例如"你还没走过棋"
	}
	if r.UndoReq != nil && r.UndoReq.fresh(time.Now()) && r.UndoReq.By == side {
		return fmt.Errorf("已经发过请求了，等对手回应")
	}
	r.UndoReq = &UndoReq{By: side, At: time.Now()}
	r.Updated = time.Now()
	m.log.Info("undo requested", "game", r.GameID, "room", r.ID, "by", side)
	m.publishLocked(r)
	return nil
}

// UndoRespond 回应悔棋请求：只有对手能同意/拒绝；同意则回退棋局。
func (m *Manager) UndoRespond(userID int64, accept bool) error {
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
	if r.UndoReq == nil || !r.UndoReq.fresh(time.Now()) {
		r.UndoReq = nil
		return fmt.Errorf("没有待处理的悔棋请求")
	}
	if r.UndoReq.By == side {
		return fmt.Errorf("这是你自己发的请求")
	}
	requester := r.UndoReq.By
	r.UndoReq = nil
	if accept {
		moves, raws, last, err := r.Match.Undo(requester)
		if err != nil {
			return err
		}
		r.Moves = moves
		r.RawMoves = raws
		r.LastMove = last
		m.log.Info("undo accepted", "game", r.GameID, "room", r.ID, "by", requester, "moves", len(moves))
	} else {
		m.log.Info("undo declined", "game", r.GameID, "room", r.ID, "by", requester)
	}
	r.Updated = time.Now()
	m.publishLocked(r)
	return nil
}

// SwapRequest 请求换边：等待中直接换；对局中需要对手同意。
func (m *Manager) SwapRequest(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	side := r.sideOf(userID)
	if side == "" {
		return fmt.Errorf("你不在这个房间里")
	}
	if r.Status == StatusWaiting {
		m.swapSidesLocked(r)
		m.log.Info("sides swapped (waiting)", "game", r.GameID, "room", r.ID)
		m.publishLocked(r)
		return nil
	}
	if r.Status != StatusPlaying {
		return fmt.Errorf("对局已结束")
	}
	if r.SwapReq != nil && r.SwapReq.fresh(time.Now()) && r.SwapReq.By == side {
		return fmt.Errorf("已经发过换边请求了，等对手回应")
	}
	r.SwapReq = &UndoReq{By: side, At: time.Now()}
	r.Updated = time.Now()
	m.log.Info("swap requested", "game", r.GameID, "room", r.ID, "by", side)
	m.publishLocked(r)
	return nil
}

// SwapRespond 回应换边请求：只有对手能同意/拒绝。
func (m *Manager) SwapRespond(userID int64, accept bool) error {
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
	if r.SwapReq == nil || !r.SwapReq.fresh(time.Now()) {
		r.SwapReq = nil
		return fmt.Errorf("没有待处理的换边请求")
	}
	if r.SwapReq.By == side {
		return fmt.Errorf("这是你自己发的请求")
	}
	requester := r.SwapReq.By
	r.SwapReq = nil
	if accept {
		m.swapSidesLocked(r)
		m.log.Info("swap accepted", "game", r.GameID, "room", r.ID, "by", requester)
	} else {
		m.log.Info("swap declined", "game", r.GameID, "room", r.ID, "by", requester)
	}
	r.Updated = time.Now()
	m.publishLocked(r)
	return nil
}

// SwapCancel 撤回自己的换边请求。
func (m *Manager) SwapCancel(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.SwapReq != nil && r.SwapReq.By == r.sideOf(userID) {
		r.SwapReq = nil
		r.Updated = time.Now()
		m.publishLocked(r)
	}
	return nil
}

// DrawRequest 提议和棋（对局中，需对方同意）。
func (m *Manager) DrawRequest(userID int64) error {
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
	if r.DrawReq != nil && r.DrawReq.fresh(time.Now()) && r.DrawReq.By == side {
		return fmt.Errorf("已经提过和棋了，等对手回应")
	}
	r.DrawReq = &UndoReq{By: side, At: time.Now()}
	r.Updated = time.Now()
	m.log.Info("draw offered", "game", r.GameID, "room", r.ID, "by", side)
	m.publishLocked(r)
	return nil
}

// DrawRespond 回应提和：只有对手能同意/拒绝；同意即本局和棋。
func (m *Manager) DrawRespond(userID int64, accept bool) error {
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
	if r.DrawReq == nil || !r.DrawReq.fresh(time.Now()) {
		r.DrawReq = nil
		return fmt.Errorf("没有待处理的提和")
	}
	if r.DrawReq.By == side {
		return fmt.Errorf("这是你自己提的和棋")
	}
	requester := r.DrawReq.By
	r.DrawReq = nil
	r.Updated = time.Now()
	if accept {
		m.log.Info("draw agreed", "game", r.GameID, "room", r.ID, "by", requester)
		m.finishLocked(r, "draw", "agreement")
		return nil
	}
	m.log.Info("draw declined", "game", r.GameID, "room", r.ID, "by", requester)
	m.publishLocked(r)
	return nil
}

// DrawCancel 撤回自己的提和。
func (m *Manager) DrawCancel(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.DrawReq != nil && r.DrawReq.By == r.sideOf(userID) {
		r.DrawReq = nil
		r.Updated = time.Now()
		m.publishLocked(r)
	}
	return nil
}

// swapSidesLocked 交换两侧玩家（棋局状态不变，轮到谁走就换成另一个人走）。
func (m *Manager) swapSidesLocked(r *Room) {
	w, bl := r.Sides["white"], r.Sides["black"]
	r.Sides["white"], r.Sides["black"] = bl, w
}

// UndoCancel 撤回自己的悔棋请求（请求方用）。
func (m *Manager) UndoCancel(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[m.byUser[userID]]
	if r == nil {
		return fmt.Errorf("你不在任何房间里")
	}
	if r.UndoReq != nil && r.UndoReq.By == r.sideOf(userID) {
		r.UndoReq = nil
		r.Updated = time.Now()
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
	r.UndoReq = nil
	r.SwapReq = nil
	r.DrawReq = nil
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
	r.UndoReq = nil
	r.SwapReq = nil
	r.DrawReq = nil
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
			"movelist": append([]string(nil), r.RawMoves...), // 回放用（象棋 UCI / 五子棋坐标）
			"display":  append([]string(nil), r.Moves...),    // 展示用（SAN / 坐标）
			"comments": cloneIntMap(r.Comments),              // AI 解说（回放页可显示）
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

func cloneIntMap(in map[int]string) map[int]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
