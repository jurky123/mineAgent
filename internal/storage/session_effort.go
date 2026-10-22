package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const sessionEffortSchema = `CREATE TABLE IF NOT EXISTS session_efforts (
 session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 effort TEXT NOT NULL CHECK(effort IN ('low','medium','high'))
);`

func (s *Store) SessionEffort(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT effort FROM session_efforts WHERE session_id=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
func (s *Store) SetSessionEffort(ctx context.Context, key, value string) error {
	switch value {
	case "", "low", "medium", "high":
	default:
		return fmt.Errorf("思考强度只能是 low/medium/high/default")
	}
	if value == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM session_efforts WHERE session_id=?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO session_efforts(session_id,effort) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET effort=excluded.effort`, key, value)
	return err
}
