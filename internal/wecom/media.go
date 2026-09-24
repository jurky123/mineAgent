package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UploadImage 上传 workspace 内图片为临时素材，返回 media_id（3 天有效）。
// path=/cgi-bin/media/upload?access_token=..&type=image，需 multipart form-data。
// 只收 jpg/png（与 QQ 图片口径一致），上限 10MB（企微应用图片消息要求，
// 超了先让 agent 压缩；QQ 是 20MB，两边不一样，工具描述里写清）。
func (a *API) UploadImage(ctx context.Context, workspaceRoot, relPath string) (string, error) {
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, "../") {
		return "", fmt.Errorf("只允许 workspace 内相对路径")
	}
	full := filepath.Join(workspaceRoot, filepath.Clean("/"+relPath)[1:])
	ext := strings.ToLower(filepath.Ext(full))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		return "", fmt.Errorf("企微图片只支持 jpg/png（%q 不行，转完再发）", ext)
	}
	fi, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("读图片失败: %w", err)
	}
	if fi.Size() > 10<<20 {
		return "", fmt.Errorf("图片 %d 字节超 10MB 上限，先压缩再发", fi.Size())
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("文件为空")
	}
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()

	tok, err := a.tokens.Token(ctx)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("media", filepath.Base(full))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	url := "https://qyapi.weixin.qq.com/cgi-bin/media/upload?access_token=" + tok + "&type=image"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("上传: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	var out struct {
		ErrCode   int    `json:"errcode"`
		ErrMsg    string `json:"errmsg"`
		MediaID   string `json:"media_id"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("上传 decode: %w", err)
	}
	if out.MediaID == "" {
		return "", fmt.Errorf("上传失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	a.log.Info("wecom image uploaded", "file", relPath, "bytes", fi.Size())
	return out.MediaID, nil
}
