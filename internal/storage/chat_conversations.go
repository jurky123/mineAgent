package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const chatConversationSchema = `
CREATE TABLE IF NOT EXISTS qq_conversations (
 scope TEXT NOT NULL, conv TEXT NOT NULL, session_id TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 PRIMARY KEY(scope,conv)
);
CREATE TABLE IF NOT EXISTS qq_active_conversations (
 scope TEXT PRIMARY KEY, conv TEXT NOT NULL
);`

var ErrConversationNotFound = errors.New("会话不存在或不属于当前身份")

type ChatConversation struct {
	Conv      string
	SessionID string
	Title     string
	CreatedAt int64
	UpdatedAt int64
}

func NewConversationID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// copyConversationHistory copies a stable transaction snapshot. Message ids are
// regenerated; summary cutoffs are remapped by position, never reused from the
// source. Approvals, checkpoints, reminders and active tool handles are excluded.
func copyConversationHistory(ctx context.Context, tx *sql.Tx, source, dest string) error {
	if source == dest {
		return fmt.Errorf("fork source equals destination")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(session_id,channel,author_kind,author_id,author_name,text,target,created_at)
 SELECT ?,channel,author_kind,author_id,author_name,text,target,created_at FROM messages WHERE session_id=? ORDER BY id`, dest, source); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `WITH mapping AS (
 SELECT old.id AS old_id,new.id AS new_id FROM
 (SELECT id,ROW_NUMBER() OVER (ORDER BY id) AS pos FROM messages WHERE session_id=?) old
 JOIN (SELECT id,ROW_NUMBER() OVER (ORDER BY id) AS pos FROM messages WHERE session_id=?) new ON old.pos=new.pos
 ) INSERT INTO summaries(session_id,up_to_message_id,text,created_at)
 SELECT ?,COALESCE((SELECT MAX(new_id) FROM mapping WHERE old_id<=s.up_to_message_id),0),s.text,s.created_at
 FROM summaries s WHERE s.session_id=? ORDER BY s.id`, source, dest, dest, source)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_efforts(session_id,effort) SELECT ?,effort FROM session_efforts WHERE session_id=?`, dest, source)
	return err
}

func ensureQQConversation(ctx context.Context, tx *sql.Tx, scope string, now int64) error {
	if !strings.HasPrefix(scope, "qq:c2c:") && !strings.HasPrefix(scope, "qq:group:") {
		return fmt.Errorf("invalid QQ conversation scope")
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO sessions(id,created_at,updated_at) VALUES(?,?,?)`, []any{scope, now, now}},
		{`INSERT OR IGNORE INTO qq_conversations(scope,conv,session_id,title,created_at) VALUES(?,'default',?,'默认对话',?)`, []any{scope, scope, now}},
		{`INSERT OR IGNORE INTO qq_active_conversations(scope,conv) VALUES(?,'default')`, []any{scope}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
}
func activeQQConversation(ctx context.Context, tx *sql.Tx, scope string) (ChatConversation, error) {
	var c ChatConversation
	err := tx.QueryRowContext(ctx, `SELECT c.conv,c.session_id,c.title,c.created_at,s.updated_at FROM qq_conversations c
 JOIN qq_active_conversations a ON a.scope=c.scope AND a.conv=c.conv JOIN sessions s ON s.id=c.session_id WHERE c.scope=?`, scope).Scan(&c.Conv, &c.SessionID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}
func (s *Store) ActiveQQConversation(ctx context.Context, scope string, now int64) (ChatConversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatConversation{}, err
	}
	defer tx.Rollback()
	if err = ensureQQConversation(ctx, tx, scope, now); err != nil {
		return ChatConversation{}, err
	}
	c, err := activeQQConversation(ctx, tx, scope)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) NewQQConversation(ctx context.Context, scope string, fork bool, now int64) (ChatConversation, error) {
	id, err := NewConversationID()
	if err != nil {
		return ChatConversation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatConversation{}, err
	}
	defer tx.Rollback()
	if err = ensureQQConversation(ctx, tx, scope, now); err != nil {
		return ChatConversation{}, err
	}
	source, err := activeQQConversation(ctx, tx, scope)
	if err != nil {
		return ChatConversation{}, err
	}
	c := ChatConversation{Conv: id, SessionID: scope + ":conv:" + id, CreatedAt: now, UpdatedAt: now}
	if fork {
		c.Title = source.Title + "（分支）"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES(?,?,?)`, c.SessionID, now, now); err != nil {
		return c, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_conversations(scope,conv,session_id,title,created_at) VALUES(?,?,?,?,?)`, scope, id, c.SessionID, c.Title, now); err != nil {
		return c, err
	}
	if fork {
		if err = copyConversationHistory(ctx, tx, source.SessionID, c.SessionID); err != nil {
			return c, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_active_conversations SET conv=? WHERE scope=?`, id, scope); err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) ResumeQQConversation(ctx context.Context, scope, id string, now int64) (ChatConversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatConversation{}, err
	}
	defer tx.Rollback()
	if err = ensureQQConversation(ctx, tx, scope, now); err != nil {
		return ChatConversation{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE qq_active_conversations SET conv=? WHERE scope=? AND EXISTS(SELECT 1 FROM qq_conversations WHERE scope=? AND conv=?)`, id, scope, scope, id)
	if err != nil {
		return ChatConversation{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ChatConversation{}, err
	}
	if n != 1 {
		return ChatConversation{}, ErrConversationNotFound
	}
	c, err := activeQQConversation(ctx, tx, scope)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) ListQQConversations(ctx context.Context, scope string, offset int) ([]ChatConversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.conv,c.session_id,c.title,c.created_at,s.updated_at FROM qq_conversations c JOIN sessions s ON s.id=c.session_id WHERE c.scope=? ORDER BY s.updated_at DESC,c.conv LIMIT 21 OFFSET ?`, scope, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []ChatConversation
	for rows.Next() {
		var c ChatConversation
		if err = rows.Scan(&c.Conv, &c.SessionID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}
func (s *Store) TitleQQConversation(ctx context.Context, key, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE qq_conversations SET title=? WHERE session_id=? AND title=''`, title, key)
	return err
}
func (s *Store) QQConversationScope(ctx context.Context, key string) (string, string, error) {
	var scope, id string
	err := s.db.QueryRowContext(ctx, `SELECT scope,conv FROM qq_conversations WHERE session_id=?`, key).Scan(&scope, &id)
	return scope, id, err
}

// CreateWebConversation validates ownership inside the same transaction as the
// optional history copy. A nil source creates an empty conversation.
func (s *Store) CreateWebConversation(ctx context.Context, uid int64, account, id, title string, source *string, now int64) error {
	if uid <= 0 || id == "" {
		return fmt.Errorf("invalid web conversation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	base := fmt.Sprintf("web:user:%d", uid)
	dest := base + ":" + id
	if source != nil && *source != "" {
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM web_conversations WHERE (user_id=? OR (user_id=0 AND account=?)) AND conv=?`, uid, account, *source).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO web_conversations(account,conv,title,created_at,updated_at,user_id) VALUES(?,?,?,?,?,?)`, account, id, title, now, now, uid); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES(?,?,?)`, dest, now, now); err != nil {
		return err
	}
	if source != nil {
		key := base
		if *source != "" {
			key += ":" + *source
		}
		if err = copyConversationHistory(ctx, tx, key, dest); err != nil {
			return err
		}
	}
	return tx.Commit()
}
