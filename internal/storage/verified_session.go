package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) VerifiedSessionUserByID(ctx context.Context, userID, sessionID int64) (*User, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM auth_sessions WHERE id=? AND user_id=? AND expires_at>?`, sessionID, userID, time.Now().UnixMilli()).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !s.IsVerifiedSession(ctx, hash) {
		return nil, nil
	}
	return s.UserByID(ctx, userID)
}
