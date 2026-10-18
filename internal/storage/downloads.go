package storage

import "context"

const downloadSchema = `
CREATE TABLE IF NOT EXISTS download_files (
 id TEXT PRIMARY KEY,
 token_hash TEXT UNIQUE,
 name TEXT NOT NULL,
 size INTEGER NOT NULL,
 sha256 TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS download_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 file_id TEXT NOT NULL REFERENCES download_files(id),
 ip TEXT NOT NULL,
 started_at INTEGER NOT NULL,
 bytes_sent INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'started'
);
CREATE INDEX IF NOT EXISTS idx_download_events_file ON download_events(file_id, id DESC);
`

type DownloadFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	CreatedAt int64  `json:"createdAt"`
	Enabled   bool   `json:"enabled"`
	Requests  int64  `json:"requests"`
	Completed int64  `json:"completed"`
}

type DownloadEvent struct {
	ID        int64  `json:"id"`
	IP        string `json:"ip"`
	StartedAt int64  `json:"startedAt"`
	BytesSent int64  `json:"bytesSent"`
	Status    string `json:"status"`
}

func (s *Store) CreateDownloadFile(ctx context.Context, f DownloadFile, hash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO download_files(id,token_hash,name,size,sha256,created_at) VALUES(?,?,?,?,?,?)`, f.ID, hash, f.Name, f.Size, f.SHA256, f.CreatedAt)
	return err
}

func (s *Store) DownloadByHash(ctx context.Context, hash string) (DownloadFile, error) {
	var f DownloadFile
	err := s.db.QueryRowContext(ctx, `SELECT id,name,size,sha256,created_at FROM download_files WHERE token_hash=?`, hash).Scan(&f.ID, &f.Name, &f.Size, &f.SHA256, &f.CreatedAt)
	return f, err
}

func (s *Store) ListDownloadFiles(ctx context.Context) ([]DownloadFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.name,f.size,f.sha256,f.created_at,f.token_hash IS NOT NULL,
 (SELECT count(*) FROM download_events e WHERE e.file_id=f.id),
 (SELECT count(*) FROM download_events e WHERE e.file_id=f.id AND e.status='completed')
 FROM download_files f ORDER BY f.created_at DESC,f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []DownloadFile{}
	for rows.Next() {
		var f DownloadFile
		if err := rows.Scan(&f.ID, &f.Name, &f.Size, &f.SHA256, &f.CreatedAt, &f.Enabled, &f.Requests, &f.Completed); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

// SetDownloadLink 撤销旧链接；hash 为空时停用，不删除文件与记录。
func (s *Store) SetDownloadLink(ctx context.Context, id, hash string) (bool, error) {
	var value any
	if hash != "" {
		value = hash
	}
	res, err := s.db.ExecContext(ctx, `UPDATE download_files SET token_hash=? WHERE id=?`, value, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n != 0, err
}

// BeginDownload 在同一条语句里检查链接仍有效并写入记录，避免撤销与下载之间的竞态。
func (s *Store) BeginDownload(ctx context.Context, id, hash, ip string, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO download_events(file_id,ip,started_at)
 SELECT id,?,? FROM download_files WHERE id=? AND token_hash=?`, ip, now, id, hash)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishDownload(ctx context.Context, id, bytes int64, complete bool) error {
	status := "interrupted"
	if complete {
		status = "completed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE download_events SET bytes_sent=?,status=? WHERE id=?`, bytes, status, id)
	return err
}

func (s *Store) DownloadEvents(ctx context.Context, fileID string) ([]DownloadEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,ip,started_at,bytes_sent,status FROM download_events WHERE file_id=? ORDER BY id DESC LIMIT 200`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []DownloadEvent{}
	for rows.Next() {
		var e DownloadEvent
		if err := rows.Scan(&e.ID, &e.IP, &e.StartedAt, &e.BytesSent, &e.Status); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}
