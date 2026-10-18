package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

const maxDownloadUpload = 256 << 20

func downloadSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func downloadHash(secret string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
}

func validDownloadSecret(secret string) bool {
	b, err := hex.DecodeString(secret)
	return err == nil && len(b) == 32
}

func (s *Server) downloadDir() string {
	dir := s.cfg.DataDir
	if dir == "" {
		dir = "data/webui"
	}
	return filepath.Join(dir, "private-downloads")
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	// 8766 / HTTP 80 也挂同一 handler，敏感文件与链接只能走 HTTPS。
	if s.tlsDomain != "" && r.TLS == nil {
		writeJSONError(w, http.StatusUpgradeRequired, "请使用 HTTPS 下载或管理文件")
		return
	}
	public := r.URL.Path == "/api/downloads/info" || r.URL.Path == "/api/downloads/file"
	// no-referrer 的原生表单可能发送 Origin: null。领取接口只凭显式分享秘密授权，
	// 不使用登录 cookie；允许该情况，管理员接口仍要求来源匹配。
	if origin := r.Header.Get("Origin"); origin != "" && !(public && origin == "null") {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme {
			writeJSONError(w, http.StatusForbidden, "不允许跨站请求")
			return
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		writeJSONError(w, http.StatusForbidden, "不允许跨站请求")
		return
	}
	if public {
		s.serveDownload(w, r)
		return
	}
	s.requireAdmin(w, r, s.manageDownloads)
}

func (s *Server) manageDownloads(w http.ResponseWriter, r *http.Request, _ *storage.User) {
	switch r.URL.Path {
	case "/api/downloads":
		switch r.Method {
		case http.MethodGet:
			list, err := s.store.ListDownloadFiles(r.Context())
			if err != nil {
				s.downloadError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"files": list})
		case http.MethodPost:
			s.uploadDownload(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET / POST")
		}
	case "/api/downloads/events":
		if r.Method != http.MethodGet {
			writeJSONError(w, 405, "只支持 GET")
			return
		}
		list, err := s.store.DownloadEvents(r.Context(), r.URL.Query().Get("id"))
		if err != nil {
			s.downloadError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"events": list})
	case "/api/downloads/link", "/api/downloads/revoke":
		if r.Method != http.MethodPost {
			writeJSONError(w, 405, "只支持 POST")
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSONError(w, 400, "请求格式无效")
			return
		}
		secret, hash := "", ""
		if strings.HasSuffix(r.URL.Path, "/link") {
			secret = downloadSecret()
			hash = downloadHash(secret)
		}
		ok, err := s.store.SetDownloadLink(r.Context(), req.ID, hash)
		if err != nil {
			s.downloadError(w, err)
			return
		}
		if !ok {
			writeJSONError(w, 404, "文件不存在")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "link": s.downloadLink(secret)})
	default:
		http.NotFound(w, r)
	}
}

// secret 仅在 URL fragment 中，浏览器不会把它放进请求路径、查询或 Referer。
func (s *Server) downloadLink(secret string) string {
	if secret == "" {
		return ""
	}
	return s.cfg.DownloadPublicOrigin + "/download#" + secret
}

func (s *Server) uploadDownload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxDownloadUpload {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "文件不能超过 256 MiB")
		return
	}
	name := webui.SafeUploadName(r.URL.Query().Get("name"))
	if r.URL.Query().Get("name") == "" || len(name) > 240 {
		writeJSONError(w, 400, "请提供有效文件名（最多 240 字节）")
		return
	}
	dir := s.downloadDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		s.downloadError(w, err)
		return
	}
	f, err := os.CreateTemp(dir, ".upload-")
	if err != nil {
		s.downloadError(w, err)
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	hash := sha256.New()
	r.Body = http.MaxBytesReader(w, r.Body, maxDownloadUpload)
	n, err := io.Copy(io.MultiWriter(f, hash), r.Body)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			writeJSONError(w, 413, "文件不能超过 256 MiB")
		} else {
			writeJSONError(w, 400, "上传中断，请重新上传")
		}
		return
	}
	if n == 0 {
		writeJSONError(w, 400, "文件为空")
		return
	}
	if err := f.Sync(); err != nil {
		s.downloadError(w, err)
		return
	}
	if err := f.Close(); err != nil {
		s.downloadError(w, err)
		return
	}
	id, secret := downloadSecret(), downloadSecret()
	final := filepath.Join(dir, id)
	if err := os.Rename(tmp, final); err != nil {
		s.downloadError(w, err)
		return
	}
	file := storage.DownloadFile{ID: id, Name: name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), CreatedAt: time.Now().UnixMilli(), Enabled: true}
	if err := s.store.CreateDownloadFile(r.Context(), file, downloadHash(secret)); err != nil {
		_ = os.Remove(final)
		s.downloadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"file": file, "link": s.downloadLink(secret)})
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, 405, "请从下载页面点击下载")
		return
	}
	// 只接收 POST 表单，不接受 URL 查询中的秘密；页面访问、HEAD、预取不计数。
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, 400, "请求格式无效")
		return
	}
	secret := r.PostForm.Get("key")
	if !validDownloadSecret(secret) {
		writeJSONError(w, 404, "链接无效或已停用")
		return
	}
	hash := downloadHash(secret)
	f, err := s.store.DownloadByHash(r.Context(), hash)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 404, "链接无效或已停用")
		return
	}
	if err != nil {
		s.downloadError(w, err)
		return
	}
	if r.URL.Path == "/api/downloads/info" {
		writeJSON(w, 200, map[string]any{"name": f.Name, "size": f.Size, "sha256": f.SHA256})
		return
	}
	if !validDownloadSecret(f.ID) {
		writeJSONError(w, 404, "文件不可用")
		return
	}
	file, err := os.Open(filepath.Join(s.downloadDir(), f.ID))
	if err != nil {
		writeJSONError(w, 404, "文件不可用，请联系发送者")
		return
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size {
		writeJSONError(w, 409, "文件状态异常，请联系发送者")
		return
	}
	// 只用 TCP 连接来源；客户端可伪造的 X-Forwarded-For / X-Real-IP 不作为证据。
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	event, err := s.store.BeginDownload(r.Context(), f.ID, hash, ip, time.Now().UnixMilli())
	if err != nil {
		s.downloadError(w, err)
		return
	}
	if event == 0 {
		writeJSONError(w, 404, "链接无效或已停用")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	w.Header().Set("Content-Length", fmt.Sprint(f.Size))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	// 不支持 Range / 条件响应，始终完整传输；每个实际领取请求独立计数。
	n, copyErr := io.CopyN(w, file, f.Size)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.FinishDownload(ctx, event, n, copyErr == nil && n == f.Size); err != nil {
		s.log.Error("download event completion failed", "event", event, "error", err)
	}
}

func (s *Server) downloadError(w http.ResponseWriter, err error) {
	s.log.Error("download operation failed", "error", err)
	writeJSONError(w, 500, "文件服务暂不可用，请稍后重试")
}
