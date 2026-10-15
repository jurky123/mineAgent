package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const passkeySchema = `
CREATE TABLE IF NOT EXISTS passkey_users (user_id INTEGER PRIMARY KEY REFERENCES users(id), handle BLOB NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS passkey_credentials (id BLOB PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS passkey_grants (hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), expires_at INTEGER NOT NULL, recovery INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS passkey_recovery (hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS passkey_sessions (token_hash TEXT PRIMARY KEY REFERENCES auth_sessions(token_hash) ON DELETE CASCADE, credential_id BLOB NOT NULL REFERENCES passkey_credentials(id) ON DELETE CASCADE);
`

var ErrInvalidEnrollment = errors.New("invalid or expired enrollment grant")

type PasskeyGrant struct {
	UserID   int64
	Recovery bool
}

func (s *Store) CreatePasskeyGrant(ctx context.Context, userID int64, handle []byte, hash string, expires int64, recovery bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !recovery {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkey_credentials WHERE user_id=?`, userID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("account already has a passkey; use recovery")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO passkey_users(user_id,handle) VALUES(?,?)`, userID, handle); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM passkey_grants WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO passkey_grants(hash,user_id,expires_at,recovery) VALUES(?,?,?,?)`, hash, userID, expires, recovery); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PasskeyGrant(ctx context.Context, hash string) (PasskeyGrant, error) {
	var g PasskeyGrant
	err := s.db.QueryRowContext(ctx, `SELECT user_id,recovery FROM passkey_grants WHERE hash=? AND expires_at>? UNION ALL SELECT user_id,1 FROM passkey_recovery WHERE hash=?`, hash, time.Now().UnixMilli(), hash).Scan(&g.UserID, &g.Recovery)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrInvalidEnrollment
	}
	return g, err
}
func (s *Store) PasskeyHandle(ctx context.Context, id int64) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT handle FROM passkey_users WHERE user_id=?`, id).Scan(&b)
	return b, err
}
func (s *Store) PasskeyOwner(ctx context.Context, credential []byte) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM passkey_credentials WHERE id=?`, credential).Scan(&id)
	return id, err
}
func (s *Store) PasskeyCredentials(ctx context.Context, id int64) ([][]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM passkey_credentials WHERE user_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) UpdatePasskeyCredential(ctx context.Context, id []byte, userID int64, previous, data []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE passkey_credentials SET data=? WHERE id=? AND user_id=? AND data=?`, data, id, userID, previous)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("credential no longer exists")
	}
	return err
}

// Enrollment is atomic: a grant can win once, old credentials/sessions are revoked only after verification.
func (s *Store) CommitPasskeyEnrollment(ctx context.Context, grantHash string, userID int64, credentialID, data []byte, recoveryHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var g PasskeyGrant
	err = tx.QueryRowContext(ctx, `SELECT user_id,recovery FROM passkey_grants WHERE hash=? AND expires_at>? UNION ALL SELECT user_id,1 FROM passkey_recovery WHERE hash=?`, grantHash, time.Now().UnixMilli(), grantHash).Scan(&g.UserID, &g.Recovery)
	if err != nil || g.UserID != userID {
		return ErrInvalidEnrollment
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkey_credentials WHERE user_id=?`, userID).Scan(&n); err != nil {
		return err
	}
	if n > 0 && !g.Recovery {
		return ErrInvalidEnrollment
	}
	for _, q := range []string{`DELETE FROM passkey_credentials WHERE user_id=?`, `DELETE FROM passkey_grants WHERE user_id=?`, `DELETE FROM passkey_recovery WHERE user_id=?`, `DELETE FROM auth_sessions WHERE user_id=?`} {
		if _, err = tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO passkey_credentials(id,user_id,data) VALUES(?,?,?)`, credentialID, userID, data); err != nil {
		return err
	}
	for _, hash := range recoveryHashes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO passkey_recovery(hash,user_id) VALUES(?,?)`, hash, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) MarkPasskeySession(ctx context.Context, hash string, credentialID []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO passkey_sessions(token_hash,credential_id) VALUES(?,?)`, hash, credentialID)
	return err
}
func (s *Store) IsPasskeySession(ctx context.Context, hash string) bool {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passkey_sessions p JOIN auth_sessions a ON a.token_hash=p.token_hash JOIN passkey_credentials c ON c.id=p.credential_id WHERE p.token_hash=? AND a.user_id=c.user_id)`, hash).Scan(&ok)
	return err == nil && ok
}
