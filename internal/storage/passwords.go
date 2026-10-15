package storage

import (
	"context"
	"database/sql"
	"errors"
)

const passwordSchema = `
CREATE TABLE IF NOT EXISTS password_accounts(user_id INTEGER PRIMARY KEY REFERENCES users(id), password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS password_sessions(token_hash TEXT PRIMARY KEY REFERENCES auth_sessions(token_hash) ON DELETE CASCADE, password_hash TEXT NOT NULL);
`

var ErrPasswordAlreadySet = errors.New("password already set or account protected by a passkey")

func (s *Store) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM password_accounts WHERE user_id=?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// The first login can claim an unprotected account, as explicitly requested by the owner.
// The transaction ensures two concurrent setup requests cannot overwrite each other.
func (s *Store) SetInitialPassword(ctx context.Context, userID int64, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM password_accounts WHERE user_id=?)+(SELECT COUNT(*) FROM passkey_credentials WHERE user_id=?)`, userID, userID).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrPasswordAlreadySet
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO password_accounts(user_id,password_hash) VALUES(?,?)`, userID, hash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) MarkPasswordSession(ctx context.Context, tokenHash, expectedHash string) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO password_sessions(token_hash,password_hash) SELECT a.token_hash,p.password_hash FROM auth_sessions a JOIN password_accounts p ON p.user_id=a.user_id WHERE a.token_hash=? AND p.password_hash=?`, tokenHash, expectedHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("password changed during login")
	}
	return err
}
func (s *Store) IsVerifiedSession(ctx context.Context, hash string) bool {
	if s.IsPasskeySession(ctx, hash) {
		return true
	}
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM password_sessions p JOIN auth_sessions a ON a.token_hash=p.token_hash JOIN password_accounts c ON c.user_id=a.user_id WHERE p.token_hash=? AND p.password_hash=c.password_hash)`, hash).Scan(&ok)
	return err == nil && ok
}
func (s *Store) ResetPassword(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM password_accounts WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
