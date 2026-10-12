package rinkujcef

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Handler 是 /rinku-jcef/* 的处理器：把已下载到本地的 JCEF 内核包
// 以和上游相同的路径形状暴露出去，供 Rinku download-mirror 指向。
//
// 路径形状（与上游 release asset 对应）：
//   /rinku-jcef/<tag>/<asset>
// 例如：
//   /rinku-jcef/java-cef-2eb4ca2648bda91d1dfed81e9a37ba92e757aff9/windows_amd64.tar.gz
//   /rinku-jcef/java-cef-2eb4ca2648bda91d1dfed81e9a37ba92e757aff9/windows_amd64.tar.gz.sha256
//
// Rinku 侧配置（config/rinku/rinku.properties）：
//   download-mirror=https://zkun.art/rinku-jcef
//   download-mirror-policy=CONFIGURED_ONLY
//
// 注意：Rinku 会在 mirror 后拼 /<tag>/<asset>，所以 download-mirror
// 只到 /rinku-jcef 这一层，不要带 tag。
type Handler struct {
	// Dir 是本地存放目录，布局为 Dir/<tag>/<asset>。
	Dir string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "只支持 GET/HEAD", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/rinku-jcef/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}
	tag, asset := parts[0], parts[1]
	// 路径形状收紧：tag 与 asset 只允许安全字符，防路径逃逸。
	if !safeSegment(tag) || !safeAsset(asset) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.Dir, tag, asset)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// .sha256 小文件：直接读出，Content-Type 纯文本。
	if strings.HasSuffix(asset, ".sha256") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, path)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func safeSegment(s string) bool {
	if s == "." || s == ".." || strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func safeAsset(s string) bool {
	if !safeSegment(s) {
		return false
	}
	return strings.HasSuffix(s, ".tar.gz") || strings.HasSuffix(s, ".tar.gz.sha256")
}

// VerifySHA256 校验本地文件与同名 .sha256 内容一致（部署自检用）。
func VerifySHA256(dir, tag, asset string) error {
	data, err := os.ReadFile(filepath.Join(dir, tag, asset))
	if err != nil {
		return err
	}
	sumBytes, err := os.ReadFile(filepath.Join(dir, tag, asset+".sha256"))
	if err != nil {
		return err
	}
	want := strings.Fields(strings.TrimSpace(string(sumBytes)))
	if len(want) == 0 {
		return errNoHash
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(want[0]) {
		return errMismatch
	}
	return nil
}

var (
	errNoHash  = errString("sha256 文件为空")
	errMismatch = errString("sha256 校验不一致")
)

type errString string

func (e errString) Error() string { return string(e) }

// CopyWithLimit 带上限的拷贝（运维手动同步包时用，防解压炸弹式的超大包）。
func CopyWithLimit(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	return io.Copy(dst, io.LimitReader(src, limit+1))
}

var _ = time.Second // 占位，保持 import 紧凑（后续加下载超时用）
