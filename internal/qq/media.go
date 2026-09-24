package qq

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 富媒体上传：先拿 file_info 再发 msg_type=7。
// 两条路（见 rich-media.html）：
//  1. URL 上传：文件已在公网，直接 POST .../files {file_type, url}，平台下载转存。
//     agent 生成的图片要先有一个公网 URL——本机没公网文件服务，走不通，
//     所以默认走 2。
//  2. 本地分片上传（推荐）：upload_prepare -> 分片 PUT -> part_finish ->
//     POST .../files {upload_id} 合并 -> file_info -> 发消息。
//     单聊/群聊接口隔离，file_info 不能跨场景用。
//
// file_type：1=图片(png/jpg，软限20MB/硬限200MB)、2=视频(mp4)、3=语音(silk)、4=文件。
// v1 只做图片（agent 画图/截图场景），视频语音以后再加。

const (
	FileImage = 1
	FileVideo = 2
	FileAudio = 3
	FileFile  = 4
)

// maxLocalUpload 是本地分片上传上限：图片硬限 200MB，但 QQ 群文件还有
// "每天容量上限"(40093002)，加上本机只有 2 核 + 内存 7.5G，v1 限 20MB
// （=图片软限制，超了平台会降级成文件卡片，反而不美）。
const maxLocalUpload = 20 << 20

type uploadPrepareResp struct {
	UploadID string `json:"upload_id"`
	BlockSize string `json:"block_size"`
	Parts []struct {
		Index int `json:"index"`
		PresignedURL string `json:"presigned_url"`
		BlockSize string `json:"block_size"`
	} `json:"parts"`
	UploadConfig struct {
		Concurrency int `json:"concurrency"`
		RetryTimeout int `json:"retry_timeout"`
		RetryDelay int `json:"retry_delay"`
	} `json:"upload_config"`
	ErrCode int `json:"err_code"`
	Message string `json:"message"`
}

type uploadFileResp struct {
	FileUUID string `json:"file_uuid"`
	FileInfo string `json:"file_info"`
	TTL int `json:"ttl"`
	ID string `json:"id"`
	ErrCode int `json:"err_code"`
	Message string `json:"message"`
}

// UploadC2CImageURL 单聊 URL 上传图片，返回 file_info。
func (a *API) UploadC2CImageURL(ctx context.Context, userOpenID, url string) (string, error) {
	return a.uploadURL(ctx, "/v2/users/"+userOpenID+"/files", FileImage, url)
}

// UploadGroupImageURL 群 URL 上传图片。
func (a *API) UploadGroupImageURL(ctx context.Context, groupOpenID, url string) (string, error) {
	return a.uploadURL(ctx, "/v2/groups/"+groupOpenID+"/files", FileImage, url)
}

func (a *API) uploadURL(ctx context.Context, path string, fileType int, url string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(url), "http://") && !strings.HasPrefix(strings.ToLower(url), "https://") {
		return "", fmt.Errorf("URL 必须 http(s) 开头")
	}
	var resp uploadFileResp
	if err := a.doJSON(ctx, http.MethodPost, path,
		map[string]any{"file_type": fileType, "url": url, "srv_send_msg": false}, &resp); err != nil {
		return "", err
	}
	if resp.FileInfo == "" {
		return "", fmt.Errorf("上传失败: err_code=%d msg=%s", resp.ErrCode, resp.Message)
	}
	return resp.FileInfo, nil
}

func (a *API) doJSON(ctx context.Context, method, path string, reqBody any, respBody any) error {
	tok, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if reqBody != nil {
		raw, _ := json.Marshal(reqBody)
		body = bytes.NewReader(raw)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, body)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "QQBot "+tok)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qq api %s: http %d %s", path, resp.StatusCode, truncate(raw, 300))
	}
	if respBody != nil {
		if err := json.Unmarshal(raw, respBody); err != nil {
			return fmt.Errorf("qq api %s decode: %w", path, err)
		}
	}
	return nil
}

// UploadC2CLocalImage 单聊本地图片分片上传：读 workspace 内文件 -> 分片 -> 合并。
// path 必须是 workspace 内的相对路径（调用方工具里已 resolve，这里再拦一次绝对路径）。
func (a *API) UploadC2CLocalImage(ctx context.Context, userOpenID, workspaceRoot, relPath string) (string, error) {
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, "../") {
		return "", fmt.Errorf("只允许 workspace 内相对路径")
	}
	full := filepath.Join(workspaceRoot, filepath.Clean("/"+relPath)[1:])
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读图片失败: %w", err)
	}
	return a.uploadLocal(ctx, "c2c", userOpenID, filepath.Base(relPath), data)
}

// UploadGroupLocalImage 群本地图片分片上传。
func (a *API) UploadGroupLocalImage(ctx context.Context, groupOpenID, workspaceRoot, relPath string) (string, error) {
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, "../") {
		return "", fmt.Errorf("只允许 workspace 内相对路径")
	}
	full := filepath.Join(workspaceRoot, filepath.Clean("/"+relPath)[1:])
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读图片失败: %w", err)
	}
	return a.uploadLocal(ctx, "group", groupOpenID, filepath.Base(relPath), data)
}

func checkImageExt(name string) error {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg":
		return nil
	default:
		return fmt.Errorf("图片只支持 png/jpg（%q 不行，gif/webp/bmp 转成 png 再发）", ext)
	}
}

// uploadLocal 分片上传全流程。scene=c2c/group，target=user/group openid。
func (a *API) uploadLocal(ctx context.Context, scene, target, fileName string, data []byte) (string, error) {
	if err := checkImageExt(fileName); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("文件为空")
	}
	if len(data) > maxLocalUpload {
		return "", fmt.Errorf("图片 %d 字节超 20MB 上限，先压缩再发", len(data))
	}
	md5sum := md5.Sum(data)
	sha1sum := sha1.Sum(data)
	head := data
	if len(head) > 10002432 {
		head = head[:10002432]
	}
	md5head := md5.Sum(head)

	prepReq := map[string]string{
		"file_type": strconv.Itoa(FileImage),
		"file_size": strconv.Itoa(len(data)),
		"file_name": fileName,
		"md5":       hex.EncodeToString(md5sum[:]),
		"sha1":      hex.EncodeToString(sha1sum[:]),
		"md5_10m":   hex.EncodeToString(md5head[:]),
	}
	var prep uploadPrepareResp
	var prepPath, finishPath, mergePath string
	if scene == "c2c" {
		prepPath = "/v2/users/" + target + "/upload_prepare"
		finishPath = "/v2/users/" + target + "/upload_part_finish"
		mergePath = "/v2/users/" + target + "/files"
	} else {
		prepPath = "/v2/groups/" + target + "/upload_prepare"
		finishPath = "/v2/groups/" + target + "/upload_part_finish"
		mergePath = "/v2/groups/" + target + "/files"
	}
	if err := a.doJSON(ctx, http.MethodPost, prepPath, prepReq, &prep); err != nil {
		return "", fmt.Errorf("预上传: %w", err)
	}
	if prep.UploadID == "" {
		return "", fmt.Errorf("预上传失败: err_code=%d msg=%s", prep.ErrCode, prep.Message)
	}
	blockSize, err := strconv.Atoi(prep.BlockSize)
	if err != nil || blockSize <= 0 {
		blockSize = 5 << 20
	}
	putClient := &http.Client{Timeout: 60 * time.Second}
	for _, part := range prep.Parts {
		start := part.Index * blockSize
		if start >= len(data) {
			break
		}
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, part.PresignedURL, bytes.NewReader(chunk))
		if err != nil {
			return "", fmt.Errorf("分片 %d: %w", part.Index, err)
		}
		putReq.Header.Set("Content-Type", "application/octet-stream")
		putReq.ContentLength = int64(len(chunk))
		putResp, err := putClient.Do(putReq)
		if err != nil {
			return "", fmt.Errorf("分片 %d 上传: %w", part.Index, err)
		}
		io.Copy(io.Discard, io.LimitReader(putResp.Body, 1<<16))
		putResp.Body.Close()
		if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
			return "", fmt.Errorf("分片 %d PUT http %d", part.Index, putResp.StatusCode)
		}
		// 通知服务端该分片完成（字段见 upload_part_finish.html：
		// upload_id / part_index / block_size / md5）。
		chunkMD5 := md5.Sum(chunk)
		var finishResp struct {
			ErrCode int `json:"err_code"`
			Message string `json:"message"`
		}
		if err := a.doJSON(ctx, http.MethodPost, finishPath,
			map[string]any{
				"upload_id":  prep.UploadID,
				"part_index": part.Index,
				"block_size": strconv.Itoa(len(chunk)),
				"md5":        hex.EncodeToString(chunkMD5[:]),
			}, &finishResp); err != nil {
			return "", fmt.Errorf("分片 %d 确认: %w", part.Index, err)
		}
		if finishResp.ErrCode != 0 {
			return "", fmt.Errorf("分片 %d 确认失败: err_code=%d msg=%s", part.Index, finishResp.ErrCode, finishResp.Message)
		}
	}
	var merged uploadFileResp
	if err := a.doJSON(ctx, http.MethodPost, mergePath,
		map[string]any{"file_type": FileImage, "srv_send_msg": false, "file_name": fileName, "upload_id": prep.UploadID},
		&merged); err != nil {
		return "", fmt.Errorf("合并: %w", err)
	}
	if merged.FileInfo == "" {
		return "", fmt.Errorf("合并失败: err_code=%d msg=%s", merged.ErrCode, merged.Message)
	}
	a.log.Info("qq image uploaded", "scene", scene, "file", fileName, "bytes", len(data), "ttl", merged.TTL)
	return merged.FileInfo, nil
}
