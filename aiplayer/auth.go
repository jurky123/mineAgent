package aiplayer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

func passwordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 32*1024, 1, 32)
	return "v1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}
func passwordMatches(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "v1" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[1])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 32*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CreateAdmin is an operator-only CLI operation. Rotating a password revokes all
// sessions immediately; neither registration nor password reset is public.
func (s *Store) CreateAdmin(ctx context.Context, name, password string) error {
	if !identifier.MatchString(name) || len([]rune(password)) < 12 || len(password) > 1024 {
		return errors.New("admin name invalid or password must have 12–1024 characters")
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO admins(id,name,password_hash) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash`, id, name, hash)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE admin_id=(SELECT id FROM admins WHERE name=?)`, name); err != nil {
		return err
	}
	if err = s.audit(tx, Principal{Kind: "operator", ID: name}, "", "", 0, "admin_password_set", ""); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) login(ctx context.Context, name, password string) (string, *Fault) {
	var id, hash string
	err := s.db.QueryRowContext(ctx, `SELECT id,password_hash FROM admins WHERE name=?`, name).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", internalFault(err)
	}
	if hash == "" {
		hash = "v1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	valid := passwordMatches(password, hash)
	if !valid || id == "" {
		return "", fault(401, "unauthorized", "账号或密码不正确")
	}
	raw, err := randomID()
	if err != nil {
		return "", internalFault(err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(hash,admin_id,password_hash,expires_at) VALUES(?,?,?,?)`, digest(raw), id, hash, s.now().Add(12*time.Hour).UnixMilli())
	if err != nil {
		return "", internalFault(err)
	}
	return raw, nil
}
func (s *Store) admin(ctx context.Context, raw string) (Principal, *Fault) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT a.id FROM sessions s JOIN admins a ON a.id=s.admin_id AND a.password_hash=s.password_hash WHERE s.hash=? AND s.expires_at>?`, digest(raw), s.now().UnixMilli()).Scan(&id)
	if err != nil {
		return Principal{}, fault(401, "unauthorized", "请先登录独立门户管理员账号")
	}
	return Principal{Kind: "admin", ID: id}, nil
}

// IssueToken returns the raw credential once to the CLI, which writes it to a
// new owner-only file. Only token IDs and SHA256 digests enter the database.
func (s *Store) IssueToken(ctx context.Context, agent string, expires time.Time) (id, raw string, err error) {
	if !expires.After(s.now()) || expires.After(s.now().Add(366*24*time.Hour)) {
		return "", "", errors.New("token expiry must be within 366 days")
	}
	id, err = randomID()
	if err != nil {
		return
	}
	secret, e := randomID()
	if e != nil {
		return "", "", e
	}
	raw = id + "." + secret
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", "", e
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO ai_tokens(id,agent_id,scope,hash,expires_at) VALUES(?,?,'posts:write:self',?,?)`, id, agent, digest(raw), expires.UnixMilli())
	if err != nil {
		return "", "", err
	}
	err = s.audit(tx, Principal{Kind: "operator", ID: "cli"}, agent, "", 0, "token_issued", id)
	if err != nil {
		return "", "", err
	}
	err = tx.Commit()
	return
}
func (s *Store) RevokeToken(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var agent string
	if err = tx.QueryRow(`SELECT agent_id FROM ai_tokens WHERE id=?`, id).Scan(&agent); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE ai_tokens SET revoked=1 WHERE id=?`, id); err != nil {
		return err
	}
	if err = s.audit(tx, Principal{Kind: "operator", ID: "cli"}, agent, "", 0, "token_revoked", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) machine(ctx context.Context, raw string) (Principal, *Fault) {
	id, _, ok := strings.Cut(raw, ".")
	if !ok || len(raw) > 256 {
		return Principal{}, fault(401, "unauthorized", "写作凭据无效或已失效")
	}
	var agent string
	err := s.db.QueryRowContext(ctx, `SELECT agent_id FROM ai_tokens WHERE id=? AND hash=? AND scope='posts:write:self' AND revoked=0 AND expires_at>?`, id, digest(raw), s.now().UnixMilli()).Scan(&agent)
	if err != nil {
		return Principal{}, fault(401, "unauthorized", "写作凭据无效或已失效")
	}
	return Principal{Kind: "ai", ID: id, AgentID: agent}, nil
}
