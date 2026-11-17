package qq

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Image originals live inside the existing admin workspace. No client filename
// becomes a path, and OpenRoot prevents an injected symlink escaping that root.
func (b *inputBundle) saveImageOriginal(ctx context.Context, id int, data []byte, mime string) (string, error) {
	return b.saveOriginalReader(ctx, id, bytes.NewReader(data), mime, int64(len(data)))
}
func (b *inputBundle) saveOriginalReader(ctx context.Context, id int, source io.Reader, mime string, limit int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b.workspaceRoot == "" {
		return "", fmt.Errorf("未启用工作区文件处理权限")
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/bmp": ".bmp", "image/tiff": ".tiff", "image/x-icon": ".ico", "video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov", "video/x-matroska": ".mkv", "application/octet-stream": ".bin", "application/pdf": ".pdf", "text/plain": ".txt", "application/zip": ".zip"}[mime]
	if ext == "" {
		ext = ".bin"
	}
	abs, err := filepath.Abs(b.workspaceRoot)
	if err != nil || abs == string(filepath.Separator) {
		return "", fmt.Errorf("工作区目录无效")
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return "", fmt.Errorf("工作区目录不可用")
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return "", fmt.Errorf("工作区目录不可用")
	}
	defer root.Close()
	if err := root.Mkdir("qq-inputs", 0700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("原图目录不可用")
	}
	info, err := root.Lstat("qq-inputs")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("原图目录不可用")
	}
	if b.originalsDir == "" {
		// Use a fresh directory per task, rather than trusting inbound names or ids.
		b.originalsDir = filepath.Join("qq-inputs", rand.Text())
		if err := root.Mkdir(b.originalsDir, 0700); err != nil {
			b.originalsDir = ""
			return "", fmt.Errorf("原图任务目录创建失败")
		}
	}
	rel := filepath.Join(b.originalsDir, fmt.Sprintf("attachment-%d%s", id, ext))
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("原图文件副本创建失败")
	}
	n, writeErr := io.Copy(f, io.LimitReader(source, limit+1))
	if n == 0 || n > limit {
		writeErr = fmt.Errorf("原文件超过可用下载预算")
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil || ctx.Err() != nil {
		_ = root.Remove(rel)
		return "", fmt.Errorf("原图文件副本未保存完整")
	}
	return filepath.ToSlash(rel), nil
}
