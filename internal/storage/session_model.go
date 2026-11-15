package storage

import (
	"context"
	"database/sql"
	"errors"
)

const sessionModelSchema = `CREATE TABLE IF NOT EXISTS session_models (
 session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 model TEXT NOT NULL
);`

func (s *Store) SessionModel(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT model FROM session_models WHERE session_id=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
func (s *Store) SetSessionModel(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM session_models WHERE session_id=?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO session_models(session_id,model) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET model=excluded.model`, key, value)
	return err
}
