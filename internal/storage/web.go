package storage

import (
	"context"
	"fmt"
)

const webSchema = `
CREATE TABLE IF NOT EXISTS web_conversation_settings(user_id INTEGER NOT NULL, conv TEXT NOT NULL, pinned INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id,conv));
CREATE TABLE IF NOT EXISTS web_run_events(session_id TEXT NOT NULL, call_id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(session_id,call_id));
`

type ConversationSettings struct {
	Pinned   bool `json:"pinned"`
	Archived bool `json:"archived"`
}

func (s *Store) ConversationSettings(ctx context.Context, uid int64) (map[string]ConversationSettings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT conv,pinned,archived FROM web_conversation_settings WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ConversationSettings{}
	for rows.Next() {
		var conv string
		var setting ConversationSettings
		if err = rows.Scan(&conv, &setting.Pinned, &setting.Archived); err != nil {
			return nil, err
		}
		out[conv] = setting
	}
	return out, rows.Err()
}
func (s *Store) SetConversationSettings(ctx context.Context, uid int64, conv string, pinned, archived bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_conversation_settings(user_id,conv,pinned,archived) VALUES(?,?,?,?) ON CONFLICT(user_id,conv) DO UPDATE SET pinned=excluded.pinned,archived=excluded.archived`, uid, conv, pinned, archived)
	return err
}

// SearchWebMessages bounds both the owner scope and result count. SQL wildcard characters are literal.
func (s *Store) SearchWebMessages(ctx context.Context, uid int64, q string) ([]Message, error) {
	prefix := fmt.Sprintf("web:user:%d", uid)
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,channel,author_kind,author_id,author_name,text,target,created_at FROM messages WHERE (session_id=? OR session_id GLOB ?) AND instr(lower(text),lower(?))>0 ORDER BY id DESC LIMIT 100`, prefix, prefix+":*", q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}
func (s *Store) SaveWebRunEvent(ctx context.Context, sessionID, callID, payload string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_run_events(session_id,call_id,payload) VALUES(?,?,?) ON CONFLICT(session_id,call_id) DO UPDATE SET payload=excluded.payload`, sessionID, callID, payload)
	return err
}
func (s *Store) WebRunEvents(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM web_run_events WHERE session_id=? ORDER BY rowid DESC LIMIT 100`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
func (s *Store) RenameAutomaticConversation(ctx context.Context, uid int64, account, conv, oldTitle, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE web_conversations SET title=? WHERE (user_id=? OR (user_id=0 AND account=?)) AND conv=? AND title=?`, title, uid, account, conv, oldTitle)
	return err
}
