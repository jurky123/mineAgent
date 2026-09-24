package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Message struct {
	ID         int64  `json:"id"`
	SessionID  string `json:"sessionId"`
	Channel    string `json:"channel"`
	AuthorKind string `json:"authorKind"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	Text       string `json:"text"`
	// Target: MC=玩家名(广播为空)；QQ="c2c:<openid>:<msgID>"。
	// 图片/markdown 走 Kind 前缀：见 KindText/KindMarkdown/KindImage。
	Target    string `json:"target,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// QQ 富媒体/版式消息的 Target 前缀。纯文本无前缀（历史兼容）。
// 图片 Target="img:<c2c|group>:<id>:<msgID>:<workspace相对路径>"；
// markdown Target="md:<c2c|group>:<id>:<msgID>"，正文放 Text。
const (
	KindText     = ""
	KindMarkdown = "md:"
	KindImage    = "img:"
)

type Summary struct {
	ID            int64  `json:"id"`
	SessionID     string `json:"sessionId"`
	UpToMessageID int64  `json:"upToMessageId"`
	Text          string `json:"text"`
	CreatedAt     int64  `json:"createdAt"`
}

type AuditEntry struct {
	ID        int64  `json:"id"`
	SessionID string `json:"sessionId"`
	CallID    string `json:"callId"`
	Tool      string `json:"tool"`
	Risk      string `json:"risk"`
	Requester string `json:"requester"`
	Args      string `json:"args"`
	Decision  string `json:"decision"`
	Operator  string `json:"operator"`
	Result    string `json:"result"`
	CreatedAt int64  `json:"createdAt"`
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id  TEXT NOT NULL,
	channel     TEXT NOT NULL,
	author_kind TEXT NOT NULL,
	author_id   TEXT NOT NULL DEFAULT '',
	author_name TEXT NOT NULL DEFAULT '',
	text        TEXT NOT NULL,
	target      TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id);

CREATE TABLE IF NOT EXISTS summaries (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id        TEXT NOT NULL,
	up_to_message_id  INTEGER NOT NULL,
	text              TEXT NOT NULL,
	created_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_summaries_session ON summaries(session_id, id);

CREATE TABLE IF NOT EXISTS identity_links (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	platform      TEXT NOT NULL,
	platform_id   TEXT NOT NULL,
	display_name  TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	UNIQUE(platform, platform_id)
);

CREATE TABLE IF NOT EXISTS tool_audit (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id  TEXT NOT NULL,
	call_id     TEXT NOT NULL DEFAULT '',
	tool        TEXT NOT NULL,
	risk        TEXT NOT NULL DEFAULT '',
	requester   TEXT NOT NULL DEFAULT '',
	args        TEXT NOT NULL DEFAULT '',
	decision    TEXT NOT NULL DEFAULT '',
	operator    TEXT NOT NULL DEFAULT '',
	result      TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_checkpoints (
	id         TEXT PRIMARY KEY,
	data       BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);
`

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Store) EnsureSession(ctx context.Context, id string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(id, created_at, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at`, id, now, now)
	return err
}

func (s *Store) AppendMessage(ctx context.Context, m Message) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages(session_id, channel, author_kind, author_id, author_name, text, target, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		m.SessionID, m.Channel, m.AuthorKind, m.AuthorID, m.AuthorName, m.Text, m.Target, m.CreatedAt)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, m.CreatedAt, m.SessionID); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) RecentMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, channel, author_kind, author_id, author_name, text, target, created_at
		 FROM messages WHERE session_id=? ORDER BY id DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (s *Store) MessagesAfter(ctx context.Context, sessionID string, afterID int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, channel, author_kind, author_id, author_name, text, target, created_at
		 FROM messages WHERE session_id=? AND id>? ORDER BY id ASC LIMIT ?`, sessionID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) MessagesBetween(ctx context.Context, sessionID string, afterID, maxID int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, channel, author_kind, author_id, author_name, text, target, created_at
		 FROM messages WHERE session_id=? AND id>? AND id<=? ORDER BY id DESC LIMIT ?`,
		sessionID, afterID, maxID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (s *Store) CountMessages(ctx context.Context, sessionID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id=?`, sessionID).Scan(&n)
	return n, err
}

func (s *Store) SaveSummary(ctx context.Context, sessionID string, upToMessageID int64, text string, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO summaries(session_id, up_to_message_id, text, created_at) VALUES(?, ?, ?, ?)`,
		sessionID, upToMessageID, text, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) LatestSummary(ctx context.Context, sessionID string) (*Summary, error) {
	var sum Summary
	err := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, up_to_message_id, text, created_at
		 FROM summaries WHERE session_id=? ORDER BY id DESC LIMIT 1`, sessionID).
		Scan(&sum.ID, &sum.SessionID, &sum.UpToMessageID, &sum.Text, &sum.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

func (s *Store) SummaryAtOrBefore(ctx context.Context, sessionID string, maxMessageID int64) (*Summary, error) {
	var sum Summary
	err := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, up_to_message_id, text, created_at
		 FROM summaries WHERE session_id=? AND up_to_message_id<=? ORDER BY id DESC LIMIT 1`,
		sessionID, maxMessageID).
		Scan(&sum.ID, &sum.SessionID, &sum.UpToMessageID, &sum.Text, &sum.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

func (s *Store) UpsertIdentity(ctx context.Context, platform, platformID, displayName string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO identity_links(platform, platform_id, display_name, created_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(platform, platform_id) DO UPDATE SET display_name=excluded.display_name`,
		platform, platformID, displayName, now)
	return err
}

// LinkedMC 查 QQ 身份绑定的 MC 玩家名，未绑定返回 ""。
// QQ 事件里能拿到的身份有好几种（union_openid / user_openid / member_openid），
// 调用方把候选按优先级逐个来查，命中即返回。
func (s *Store) LinkedMC(ctx context.Context, platform, platformID string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx,
		`SELECT display_name FROM identity_links WHERE platform=? AND platform_id=?`,
		platform, platformID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) SaveAudit(ctx context.Context, e AuditEntry) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_audit(session_id, call_id, tool, risk, requester, args, decision, operator, result, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.CallID, e.Tool, e.Risk, e.Requester, e.Args, e.Decision, e.Operator, e.Result, e.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SaveCheckpoint(ctx context.Context, id string, data []byte, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_checkpoints(id, data, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		id, data, now)
	return err
}

func (s *Store) LoadCheckpoint(ctx context.Context, id string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM agent_checkpoints WHERE id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *Store) DeleteCheckpoint(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE id=?`, id)
	return err
}

func (s *Store) ClearCheckpoints(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_checkpoints`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		if err := scanMessage(rows, &m); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func scanMessage(row rowScanner, m *Message) error {
	return row.Scan(&m.ID, &m.SessionID, &m.Channel, &m.AuthorKind, &m.AuthorID, &m.AuthorName, &m.Text, &m.Target, &m.CreatedAt)
}
