package aiplayer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const applicationID = 1095323728
const schema = `
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS agents(id TEXT PRIMARY KEY,name TEXT NOT NULL,display_name TEXT NOT NULL,bio TEXT NOT NULL DEFAULT '',tags TEXT NOT NULL DEFAULT '[]',blocked INTEGER NOT NULL DEFAULT 0,trust_level TEXT NOT NULL DEFAULT 'review',version INTEGER NOT NULL DEFAULT 1,profile_working INTEGER,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS posts(id TEXT PRIMARY KEY,agent_id TEXT NOT NULL REFERENCES agents(id),visibility TEXT NOT NULL DEFAULT 'unpublished',live_revision INTEGER,working_revision INTEGER,version INTEGER NOT NULL DEFAULT 1,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,published_at INTEGER,deleted_at INTEGER,restore_review INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS revisions(id INTEGER PRIMARY KEY,post_id TEXT NOT NULL REFERENCES posts(id),revision INTEGER NOT NULL,title TEXT NOT NULL,body TEXT NOT NULL,tags TEXT NOT NULL,lang TEXT NOT NULL,state TEXT NOT NULL,flags TEXT NOT NULL,created_at INTEGER NOT NULL,reviewer TEXT,reason TEXT,reviewed_at INTEGER,UNIQUE(post_id,revision));
CREATE TABLE IF NOT EXISTS profile_revisions(id INTEGER PRIMARY KEY,agent_id TEXT NOT NULL REFERENCES agents(id),bio TEXT NOT NULL,tags TEXT NOT NULL,state TEXT NOT NULL,flags TEXT NOT NULL,created_at INTEGER NOT NULL,reviewer TEXT,reason TEXT);
CREATE TABLE IF NOT EXISTS ai_tokens(id TEXT PRIMARY KEY,agent_id TEXT NOT NULL REFERENCES agents(id),scope TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,expires_at INTEGER NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS admins(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,admin_id TEXT NOT NULL REFERENCES admins(id),password_hash TEXT NOT NULL,expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS requests(actor TEXT NOT NULL,route TEXT NOT NULL,key TEXT NOT NULL,request_hash TEXT NOT NULL,status INTEGER NOT NULL,response TEXT NOT NULL,expires_at INTEGER NOT NULL,PRIMARY KEY(actor,route,key));
CREATE TABLE IF NOT EXISTS write_quota(agent_id TEXT NOT NULL,kind TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS quota_by_agent ON write_quota(agent_id,created_at);
CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY,actor TEXT NOT NULL,agent_id TEXT NOT NULL,post_id TEXT NOT NULL DEFAULT '',revision_id INTEGER NOT NULL DEFAULT 0,action TEXT NOT NULL,reason TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS public_posts ON posts(visibility,published_at DESC);
CREATE INDEX IF NOT EXISTS author_posts ON posts(agent_id,visibility);
`

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.Mode().IsRegular() {
		return nil, errors.New("database must be a regular file")
	}
	existing := statErr == nil && info.Size() > 0
	if os.IsNotExist(statErr) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	} else if statErr != nil {
		return nil, statErr
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	var id int
	if err = db.QueryRow(`PRAGMA application_id`).Scan(&id); err != nil {
		return fail(err)
	}
	if id != applicationID && (existing || id != 0) {
		return fail(errors.New("refusing database not owned by AI player portal"))
	}
	if _, err = db.Exec(`PRAGMA application_id=1095323728;PRAGMA journal_mode=WAL;PRAGMA busy_timeout=5000;` + schema); err != nil {
		return fail(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		return fail(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(path+suffix, 0600); err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
	}
	return &Store{db: db, now: time.Now}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func encoded(v any) string { b, _ := json.Marshal(v); return string(b) }
func decodeList(raw string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
func (s *Store) Seed(ctx context.Context, agents []AgentConfig) error {
	for _, a := range agents {
		_, err := s.db.ExecContext(ctx, `INSERT INTO agents(id,name,display_name,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, a.ID, a.Name, a.DisplayName, s.now().UnixMilli())
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Backup(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("backup destination must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	f.Close()
	_, err = s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	if err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

type Principal struct {
	Kind    string
	ID      string
	AgentID string
}

func (p Principal) Actor() string {
	if p.Kind == "ai" {
		return "ai:" + p.AgentID
	}
	return p.Kind + ":" + p.ID
}

type mutationResult struct {
	Status int
	Body   json.RawMessage
	Replay bool
}

// A single transaction covers idempotency, quota, state changes and audit. SQLite
// serializes competing versions; a retried request never consumes another quota.
func (s *Store) mutate(ctx context.Context, p Principal, route, key string, input any, work func(*sql.Tx) (any, *Fault)) (mutationResult, *Fault) {
	if len(key) < 8 || len(key) > 128 || strings.ContainsAny(key, "\r\n\t ") {
		return mutationResult{}, fault(400, "invalid_request", "必须提供 8–128 字符的 Idempotency-Key")
	}
	hash := digest(encoded(input))
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mutationResult{}, fault(503, "unavailable", "存储暂不可用")
	}
	defer tx.Rollback()
	var priorHash, body string
	var status int
	err = tx.QueryRowContext(ctx, `SELECT request_hash,status,response FROM requests WHERE actor=? AND route=? AND key=? AND expires_at>?`, p.Actor(), route, key, now).Scan(&priorHash, &status, &body)
	if err == nil {
		if priorHash != hash {
			return mutationResult{}, fault(409, "conflict", "此幂等键已用于不同内容")
		}
		return mutationResult{status, json.RawMessage(body), true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return mutationResult{}, fault(503, "unavailable", "无法读取写入结果")
	}
	result, e := work(tx)
	if e != nil {
		return mutationResult{}, e
	}
	body = encoded(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO requests(actor,route,key,request_hash,status,response,expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(actor,route,key) DO UPDATE SET request_hash=excluded.request_hash,status=excluded.status,response=excluded.response,expires_at=excluded.expires_at`, p.Actor(), route, key, hash, 200, body, now+int64(7*24*time.Hour/time.Millisecond))
	if err != nil {
		return mutationResult{}, fault(503, "unavailable", "无法保存写入结果")
	}
	if err = tx.Commit(); err != nil {
		return mutationResult{}, fault(503, "unavailable", "写入未确认，请使用同一幂等键重试")
	}
	return mutationResult{200, json.RawMessage(body), false}, nil
}
func (s *Store) quota(tx *sql.Tx, agent, kind string) *Fault {
	now := s.now().UnixMilli()
	var minute, hour, day, edits int
	err := tx.QueryRow(`SELECT COALESCE(SUM(created_at>?),0),COALESCE(SUM(kind='create' AND created_at>?),0),COALESCE(SUM(kind='create' AND created_at>?),0),COALESCE(SUM(kind<>'create' AND created_at>?),0) FROM write_quota WHERE agent_id=? AND created_at>?`, now-60000, now-3600000, now-86400000, now-86400000, agent, now-86400000).Scan(&minute, &hour, &day, &edits)
	if err != nil {
		return fault(503, "unavailable", "无法读取配额")
	}
	if minute >= 1 || (kind == "create" && (hour >= 2 || day >= 5)) || (kind != "create" && edits >= 20) {
		e := fault(429, "rate_limited", "达到写入限额，请稍后重试")
		e.RetryAfterMS = 60000
		if kind == "create" && day >= 5 {
			e.RetryAfterMS = 86400000
		} else if kind == "create" && hour >= 2 {
			e.RetryAfterMS = 3600000
		} else if kind != "create" && edits >= 20 {
			e.RetryAfterMS = 86400000
		}
		return e
	}
	if _, err = tx.Exec(`INSERT INTO write_quota(agent_id,kind,created_at) VALUES(?,?,?)`, agent, kind, now); err != nil {
		return fault(503, "unavailable", "无法记录配额")
	}
	return nil
}
func (s *Store) audit(tx *sql.Tx, p Principal, agent, post string, rev int64, action, reason string) error {
	_, err := tx.Exec(`INSERT INTO audit(actor,agent_id,post_id,revision_id,action,reason,created_at) VALUES(?,?,?,?,?,?,?)`, p.Actor(), agent, post, rev, action, reason, s.now().UnixMilli())
	return err
}
func internalFault(err error) *Fault {
	if err != nil {
		return fault(503, "unavailable", "存储操作未完成")
	}
	return nil
}
