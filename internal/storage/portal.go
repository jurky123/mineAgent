package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// User 是 Portal 的统一账号（网页/游戏/未来的其它应用共用）。
// ID 是唯一权威标识；Username 只是登录名，永不变更（改名功能未开放）。
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	IsAdmin     bool   `json:"isAdmin"`
	CreatedAt   int64  `json:"createdAt"`
	LastSeenAt  int64  `json:"lastSeenAt"`
}

// AuthSession 是一条登录会话；只存 token 的 sha256，明文 token 仅存在于客户端。
type AuthSession struct {
	ID         int64
	UserID     int64
	TokenHash  string
	UserAgent  string
	IP         string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}

// Announcement 是门户公告栏的一条公告。
type Announcement struct {
	ID        int64  `json:"id"`
	Text      string `json:"text"`
	Author    string `json:"author"`
	CreatedAt int64  `json:"createdAt"`
}

const portalSchema = `
CREATE TABLE IF NOT EXISTS users (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	username     TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	is_admin     INTEGER NOT NULL DEFAULT 0,
	pin_hash     TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS auth_sessions (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL,
	token_hash   TEXT NOT NULL UNIQUE,
	user_agent   TEXT NOT NULL DEFAULT '',
	ip           TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);

CREATE TABLE IF NOT EXISTS announcements (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	text       TEXT NOT NULL,
	author     TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS game_runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL,
	game_id     TEXT NOT NULL,
	result      TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	metadata    TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_game_runs_user ON game_runs(user_id, game_id, id DESC);

CREATE TABLE IF NOT EXISTS portal_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// migratePortal 建 Portal 相关表，并给 web_conversations 补 user_id 列（幂等）。
func (s *Store) migratePortal(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, portalSchema); err != nil {
		return fmt.Errorf("migrate portal: %w", err)
	}
	has, err := s.hasColumn(ctx, "web_conversations", "user_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE web_conversations ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add web_conversations.user_id: %w", err)
		}
	}
	_, err = s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_web_conversations_user ON web_conversations(user_id, updated_at)`)
	return err
}

func (s *Store) hasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ---------- users ----------

// EnsureUser 按用户名取用户，不存在则创建（名字即账号，无密码）。
func (s *Store) EnsureUser(ctx context.Context, username string, now int64) (*User, error) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO users(username, display_name, created_at, last_seen_at) VALUES(?, ?, ?, ?)`,
		username, username, now, now); err != nil {
		return nil, err
	}
	return s.UserByName(ctx, username)
}

func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, is_admin, created_at, last_seen_at FROM users WHERE username=?`, username))
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, is_admin, created_at, last_seen_at FROM users WHERE id=?`, id))
}

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var isAdmin int
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &isAdmin, &u.CreatedAt, &u.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	return &u, nil
}

// ListUsers 按用户名排序返回全部账号（管理用途）。
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, display_name, is_admin, created_at, last_seen_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var isAdmin int
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &isAdmin, &u.CreatedAt, &u.LastSeenAt); err != nil {
			return nil, err
		}
		u.IsAdmin = isAdmin != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetUserAdmin(ctx context.Context, username string, isAdmin bool) error {
	v := 0
	if isAdmin {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET is_admin=? WHERE username=?`, v, username)
	return err
}

// TouchUser 记录活跃时间（登录/鉴权通过时调用，节流交给调用方）。
func (s *Store) TouchUser(ctx context.Context, id, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_seen_at=? WHERE id=?`, now, id)
	return err
}

// ---------- auth_sessions ----------

func (s *Store) CreateAuthSession(ctx context.Context, sess AuthSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_sessions(user_id, token_hash, user_agent, ip, created_at, expires_at, last_seen_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sess.UserID, sess.TokenHash, sess.UserAgent, sess.IP, sess.CreatedAt, sess.ExpiresAt, sess.CreatedAt)
	return err
}

// AuthSessionUser 校验 token 并返回用户；过期返回 (nil, 0, nil)。
func (s *Store) AuthSessionUser(ctx context.Context, tokenHash string, now int64) (*User, int64, error) {
	var (
		sessID  int64
		userID  int64
		expires int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at FROM auth_sessions WHERE token_hash=?`, tokenHash).
		Scan(&sessID, &userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if expires <= now {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id=?`, sessID)
		return nil, 0, nil
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil || u == nil {
		return nil, 0, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=? WHERE id=?`, now, sessID)
	return u, sessID, nil
}

func (s *Store) DeleteAuthSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash)
	return err
}

func (s *Store) DeleteAuthSessionByID(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id=?`, id)
	return err
}

// DeleteAuthSessionByIDForUser 注销指定会话（校验归属，防越权）。
func (s *Store) DeleteAuthSessionByIDForUser(ctx context.Context, id, userID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteOtherAuthSessions 注销除当前会话外的全部登录（退出所有设备）。
func (s *Store) DeleteOtherAuthSessions(ctx context.Context, userID, keepID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE user_id=? AND id<>?`, userID, keepID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountConversationsByUser 会话数（Portal 首页 Agent 卡片用）。
func (s *Store) CountConversationsByUser(ctx context.Context, userID int64, account string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM web_conversations WHERE user_id=? OR (user_id=0 AND account=?)`,
		userID, account).Scan(&n)
	return n, err
}

// ImportAuthSession 导入一条历史登录（旧 tokens.json）；已存在（同 hash）返回 false。
func (s *Store) ImportAuthSession(ctx context.Context, sess AuthSession) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO auth_sessions(user_id, token_hash, user_agent, ip, created_at, expires_at, last_seen_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sess.UserID, sess.TokenHash, sess.UserAgent, sess.IP, sess.CreatedAt, sess.ExpiresAt, sess.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) DeleteUserAuthSessions(ctx context.Context, userID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) CountAuthSessions(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_sessions WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

func (s *Store) CleanExpiredAuthSessions(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at<=?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListAuthSessions 用来展示"已登录设备"（不含 token 本身）。
func (s *Store) ListAuthSessions(ctx context.Context, userID int64) ([]AuthSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, '', user_agent, ip, created_at, expires_at, last_seen_at FROM auth_sessions
		 WHERE user_id=? ORDER BY last_seen_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthSession
	for rows.Next() {
		var a AuthSession
		if err := rows.Scan(&a.ID, &a.UserID, &a.TokenHash, &a.UserAgent, &a.IP,
			&a.CreatedAt, &a.ExpiresAt, &a.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GameRun 是一局游戏的原始记录（积分/排行榜模型未定，先只留档）。
type GameRun struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"userId"`
	GameID     string `json:"gameId"`
	Result     string `json:"result"` // win/lose/draw + 原因在 metadata
	DurationMS int64  `json:"durationMs"`
	Metadata   string `json:"metadata,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// AddGameRun 落一条对局记录。
func (s *Store) AddGameRun(ctx context.Context, r GameRun) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO game_runs(user_id, game_id, result, duration_ms, metadata, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		r.UserID, r.GameID, r.Result, r.DurationMS, r.Metadata, r.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListGameRuns 某用户的最近战绩（账号页/大厅展示用）。
func (s *Store) ListGameRuns(ctx context.Context, userID int64, gameID string, limit int) ([]GameRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, game_id, result, duration_ms, metadata, created_at FROM game_runs
		 WHERE user_id=? AND (?='' OR game_id=?) ORDER BY id DESC LIMIT ?`,
		userID, gameID, gameID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameRun
	for rows.Next() {
		var r GameRun
		if err := rows.Scan(&r.ID, &r.UserID, &r.GameID, &r.Result, &r.DurationMS, &r.Metadata, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GameStat 是按游戏聚合的战绩（账号页/大厅展示）。
type GameStat struct {
	GameID string `json:"gameId"`
	Total  int    `json:"total"`
	Wins   int    `json:"wins"`
	Losses int    `json:"losses"`
	Draws  int    `json:"draws"`
}

// GameStats 聚合某用户的全部对局记录（没有记录时返回空切片）。
func (s *Store) GameStats(ctx context.Context, userID int64) ([]GameStat, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT game_id, result, COUNT(*) FROM game_runs WHERE user_id=? GROUP BY game_id, result`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	var out []GameStat
	for rows.Next() {
		var gameID, result string
		var n int
		if err := rows.Scan(&gameID, &result, &n); err != nil {
			return nil, err
		}
		i, ok := idx[gameID]
		if !ok {
			i = len(out)
			idx[gameID] = i
			out = append(out, GameStat{GameID: gameID})
		}
		out[i].Total += n
		switch result {
		case "win":
			out[i].Wins += n
		case "lose":
			out[i].Losses += n
		case "draw":
			out[i].Draws += n
		}
	}
	return out, rows.Err()
}

// ---------- announcements ----------

func (s *Store) AddAnnouncement(ctx context.Context, text, author string, now int64) (*Announcement, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO announcements(text, author, created_at) VALUES(?, ?, ?)`, text, author, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Announcement{ID: id, Text: text, Author: author, CreatedAt: now}, nil
}

func (s *Store) ListAnnouncements(ctx context.Context, limit int) ([]Announcement, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, text, author, created_at FROM announcements ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Text, &a.Author, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAnnouncement(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM announcements WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ---------- portal_meta ----------

func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM portal_meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO portal_meta(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ---------- web_conversations × user_id ----------

// UpsertConversationFor 写入会话行并绑定 user_id（Portal 后 account 仅作展示）。
func (s *Store) UpsertConversationFor(ctx context.Context, userID int64, account, conv, title string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO web_conversations(account, conv, title, created_at, updated_at, user_id) VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(account, conv) DO UPDATE SET
		   user_id=CASE WHEN excluded.user_id <> 0 THEN excluded.user_id ELSE web_conversations.user_id END,
		   title=CASE WHEN excluded.title <> '' THEN excluded.title ELSE web_conversations.title END,
		   updated_at=excluded.updated_at`,
		account, conv, title, now, now, userID)
	return err
}

func (s *Store) SetConversationTitleIfEmptyFor(ctx context.Context, userID int64, account, conv, title string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE web_conversations SET title=?, updated_at=?
		 WHERE account=? AND conv=? AND (title IS NULL OR title='')
		   AND (user_id=0 OR user_id=?)`,
		title, now, account, conv, userID)
	return err
}

// ListConversationsByUser 按 user_id 读会话，兼容迁移前的 account-only 行。
func (s *Store) ListConversationsByUser(ctx context.Context, userID int64, account string) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account, conv, title, created_at, updated_at FROM web_conversations
		 WHERE user_id=? OR (user_id=0 AND account=?)
		 ORDER BY updated_at DESC, created_at DESC`, userID, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.Account, &c.Conv, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ConversationFor(ctx context.Context, userID int64, account, conv string) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRowContext(ctx,
		`SELECT account, conv, title, created_at, updated_at FROM web_conversations
		 WHERE (user_id=? OR (user_id=0 AND account=?)) AND conv=? LIMIT 1`,
		userID, account, conv).Scan(&c.Account, &c.Conv, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) DeleteConversationFor(ctx context.Context, userID int64, account, conv string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM web_conversations WHERE (user_id=? OR (user_id=0 AND account=?)) AND conv=?`,
		userID, account, conv)
	return err
}

// ---------- 迁移辅助 ----------

// WebSessionIDs 返回所有旧的网页会话键（web:c2c:...，消息/摘要/提醒去重合并）。
func (s *Store) WebSessionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id FROM messages WHERE session_id LIKE 'web:c2c:%'
		 UNION SELECT session_id FROM summaries WHERE session_id LIKE 'web:c2c:%'
		 UNION SELECT session_id FROM reminders WHERE session_id LIKE 'web:c2c:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RenameSessionID 把一条会话的全部数据改名（sessions/messages/summaries/reminders 一处不落）。
func (s *Store) RenameSessionID(ctx context.Context, oldID, newID string) error {
	if oldID == newID {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, q := range []string{
		`UPDATE messages SET session_id=? WHERE session_id=?`,
		`UPDATE summaries SET session_id=? WHERE session_id=?`,
		`UPDATE reminders SET session_id=? WHERE session_id=?`,
		`UPDATE tool_audit SET session_id=? WHERE session_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, newID, oldID); err != nil {
			return err
		}
	}
	// sessions 表是主键：老行有就把 created_at 搬过来，没有就留着老行不管（新行由 EnsureSession 补）。
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO sessions(id, created_at, updated_at)
		 SELECT ?, created_at, updated_at FROM sessions WHERE id=?`, newID, oldID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, oldID); err != nil {
		return err
	}
	return tx.Commit()
}

// UnboundConversationAccounts 返回还有未绑定 user_id 会话行的账号名（迁移用）。
func (s *Store) UnboundConversationAccounts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT account FROM web_conversations WHERE user_id=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BindConversationsToUser 把某账号名下的会话行绑到 user_id。
func (s *Store) BindConversationsToUser(ctx context.Context, account string, userID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE web_conversations SET user_id=? WHERE account=? AND user_id=0`, userID, account)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UserFileDir 是上传文件目录名（按用户ID，改名不影响历史文件）。
func UserFileDir(userID int64) string {
	return "u" + strconv.FormatInt(userID, 10)
}

// BackupTo 用 VACUUM INTO 生成一致性的库文件副本（目标文件必须不存在）。
func (s *Store) BackupTo(ctx context.Context, path string) error {
	if strings.ContainsAny(path, `'"`) {
		return fmt.Errorf("backup path 含非法字符")
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO '`+path+`'`)
	return err
}
