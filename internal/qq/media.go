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
// 图片保留 GIF/WebP 动画；其他类型走相同分片流程。

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
	UploadID  string `json:"upload_id"`
	BlockSize string `json:"block_size"`
	Parts     []struct {
		Index        int    `json:"index"`
		PresignedURL string `json:"presigned_url"`
		BlockSize    string `json:"block_size"`
	} `json:"parts"`
	UploadConfig struct {
		Concurrency  int `json:"concurrency"`
		RetryTimeout int `json:"retry_timeout"`
		RetryDelay   int `json:"retry_delay"`
	} `json:"upload_config"`
	ErrCode int    `json:"err_code"`
	Message string `json:"message"`
}

type uploadFileResp struct {
	FileUUID string `json:"file_uuid"`
	FileInfo string `json:"file_info"`
	TTL      int    `json:"ttl"`
	ID       string `json:"id"`
	ErrCode  int    `json:"err_code"`
	Message  string `json:"message"`
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
	var problem struct {
		Code    int `json:"code"`
		ErrCode int `json:"err_code"`
	}
	_ = json.Unmarshal(raw, &problem)
	code := problem.ErrCode
	if code == 0 {
		code = problem.Code
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 {
		return &APIError{Status: resp.StatusCode, Code: code}
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
func (a *API) UploadC2CLocalImage(ctx context.Context, id, root, rel string) (string, error) {
	return a.UploadLocalMedia(ctx, "c2c", id, root, rel, FileImage)
}
func (a *API) UploadGroupLocalImage(ctx context.Context, id, root, rel string) (string, error) {
	return a.UploadLocalMedia(ctx, "group", id, root, rel, FileImage)
}

// Root.Open prevents traversal and symlink escapes, including during a rename.
// Hashes and HTTP parts stream from the same open file, without buffering a 200MB file.
func (a *API) UploadLocalMedia(ctx context.Context, scene, id, workspaceRoot, rel string, fileType int) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("只允许 workspace 内相对路径")
	}
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("workspace 不可用")
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return "", fmt.Errorf("无法读取 workspace 文件")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("只能上传普通文件")
	}
	return a.uploadSource(ctx, scene, id, filepath.Base(rel), fileType, f, st.Size())
}
func localMediaLimit(kind int) int64 {
	switch kind {
	case FileImage, FileAudio:
		return 20 << 20
	case FileVideo:
		return 30 << 20
	case FileFile:
		return 200 << 20
	}
	return 0
}
func checkMediaExt(name string, kind int) error {
	ext := strings.ToLower(filepath.Ext(name))
	ok := false
	switch kind {
	case FileFile:
		return nil
	case FileImage:
		ok = ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".bmp"
	case FileVideo:
		ok = ext == ".mp4"
	case FileAudio:
		ok = ext == ".silk" || ext == ".mp3" || ext == ".wav" || ext == ".ogg"
	}
	if !ok {
		return fmt.Errorf("不支持的媒体格式 %q，请选择相应文件/媒体工具", ext)
	}
	return nil
}

func checkImageExt(name string) error { return checkMediaExt(name, FileImage) }
func (a *API) uploadLocal(ctx context.Context, scene, target, fileName string, data []byte) (string, error) {
	return a.uploadSource(ctx, scene, target, fileName, FileImage, bytes.NewReader(data), int64(len(data)))
}
func (a *API) uploadSource(ctx context.Context, scene, target, fileName string, fileType int, source io.ReaderAt, size int64) (string, error) {
	if scene != "c2c" && scene != "group" {
		return "", fmt.Errorf("无效的发送场景")
	}
	if err := checkMediaExt(fileName, fileType); err != nil {
		return "", err
	}
	limit := localMediaLimit(fileType)
	if size <= 0 || limit == 0 || size > limit {
		return "", fmt.Errorf("文件为空或超过当前媒体上限（%dMB）", limit>>20)
	}
	md5hash, sha1hash := md5.New(), sha1.New()
	if _, err := io.Copy(io.MultiWriter(md5hash, sha1hash), io.NewSectionReader(source, 0, size)); err != nil {
		return "", fmt.Errorf("读取文件校验失败")
	}
	headHash := md5.New()
	if _, err := io.Copy(headHash, io.NewSectionReader(source, 0, min(size, 10002432))); err != nil {
		return "", fmt.Errorf("读取文件校验失败")
	}
	prepReq := map[string]string{
		"file_type": strconv.Itoa(fileType),
		"file_size": strconv.FormatInt(size, 10),
		"file_name": fileName,
		"md5":       hex.EncodeToString(md5hash.Sum(nil)),
		"sha1":      hex.EncodeToString(sha1hash.Sum(nil)),
		"md5_10m":   hex.EncodeToString(headHash.Sum(nil)),
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
	// Validate complete, unique part coverage before sending or merging any data.
	expected := (size + int64(blockSize) - 1) / int64(blockSize)
	if int64(len(prep.Parts)) != expected {
		return "", fmt.Errorf("预上传分片数量不完整")
	}
	base := 1
	for _, part := range prep.Parts {
		if part.Index == 0 {
			base = 0
		}
	}
	seen := map[int]bool{}
	for _, part := range prep.Parts {
		n := part.Index - base
		if n < 0 || int64(n) >= expected || seen[n] || part.PresignedURL == "" {
			return "", fmt.Errorf("预上传分片无效")
		}
		seen[n] = true
	}
	for _, part := range prep.Parts {
		seq := part.Index - base
		if seq < 0 {
			seq = 0
		}
		start := int64(seq) * int64(blockSize)
		length := min(int64(blockSize), size-start)
		chunk := io.NewSectionReader(source, start, length)
		putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, part.PresignedURL, chunk)
		if err != nil {
			return "", fmt.Errorf("分片 %d 地址无效", part.Index)
		}
		putReq.Header.Set("Content-Type", "application/octet-stream")
		putReq.ContentLength = length
		putResp, err := putClient.Do(putReq)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("分片 %d 网络失败，未确认上传", part.Index)
		}
		io.Copy(io.Discard, io.LimitReader(putResp.Body, 1<<16))
		putResp.Body.Close()
		if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
			return "", fmt.Errorf("分片 %d PUT http %d", part.Index, putResp.StatusCode)
		}
		// 通知服务端该分片完成（字段见 upload_part_finish.html：
		// upload_id / part_index / block_size / md5）。
		chunkHash := md5.New()
		if _, err := io.Copy(chunkHash, io.NewSectionReader(source, start, length)); err != nil {
			return "", fmt.Errorf("分片读取失败")
		}
		var finishResp struct {
			ErrCode int    `json:"err_code"`
			Message string `json:"message"`
		}
		if err := a.doJSON(ctx, http.MethodPost, finishPath,
			map[string]any{
				"upload_id":  prep.UploadID,
				"part_index": part.Index,
				"block_size": strconv.FormatInt(length, 10),
				"md5":        hex.EncodeToString(chunkHash.Sum(nil)),
			}, &finishResp); err != nil {
			return "", fmt.Errorf("分片 %d 确认: %w", part.Index, err)
		}
		if finishResp.ErrCode != 0 {
			return "", fmt.Errorf("分片 %d 确认失败: err_code=%d msg=%s", part.Index, finishResp.ErrCode, finishResp.Message)
		}
	}
	var merged uploadFileResp
	if err := a.doJSON(ctx, http.MethodPost, mergePath,
		map[string]any{"file_type": fileType, "srv_send_msg": false, "file_name": fileName, "upload_id": prep.UploadID},
		&merged); err != nil {
		return "", fmt.Errorf("合并: %w", err)
	}
	if merged.FileInfo == "" {
		return "", fmt.Errorf("合并失败: err_code=%d msg=%s", merged.ErrCode, merged.Message)
	}
	a.log.Info("qq media uploaded", "scene", scene, "file", fileName, "bytes", size, "ttl", merged.TTL)
	return merged.FileInfo, nil
}
