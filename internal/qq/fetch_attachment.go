package qq

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"mineagent/internal/tools"
)

const maxOriginalBytes int64 = 256 << 20
const maxOriginalTotal int64 = 512 << 20

type fetchAttachmentTool struct{}

func (*fetchAttachmentTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "qq_fetch_attachment", Desc: "按当前任务附件编号主动获取原始图片/视频/文件。只按需下载一次，返回实际大小/格式/SHA256；管理员另保存workspace相对路径，可继续处理，普通用户仅本任务临时保留。单件256MB/本任务512MB，链接过期会明确失败；不接受任意URL或路径。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"id": {Type: schema.Integer, Required: true, Desc: "当前QQ消息/引用附件编号，从1开始"}})}, nil
}
func (*fetchAttachmentTool) InvokableRun(ctx context.Context, raw string, _ ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b, _ := ctx.Value(inputBundleKey{}).(*inputBundle)
	var a struct {
		ID int `json:"id"`
	}
	if b == nil || json.Unmarshal([]byte(raw), &a) != nil || a.ID < 1 || a.ID > len(b.files) {
		return `{"error":"附件编号不属于本次任务"}`, nil
	}
	b.fetchMu.Lock()
	defer b.fetchMu.Unlock()
	f := &b.files[a.ID-1]
	if (strings.HasPrefix(strings.ToLower(f.Source.ContentType), "video/") || isVideoFilename(f.Source.Filename)) && strings.HasPrefix(f.MIMEType, "image/") {
		return `{"error":"平台只提供了视频封面，未取得视频原件；请以文件形式发送视频"}`, nil
	}
	if f.Local == "" {
		local, mime, size, hash, err := b.fetchOriginal(ctx, f.Source)
		if err != nil {
			data, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
			return string(data), nil
		}
		f.Local, f.MIMEType, f.Size, f.SHA256 = local, mime, size, hash
		f.Status = "原附件已按需取得；未额外生成视觉输入或解析全文，不要声称已经看完视频/动图"
	}
	if f.Path == "" && b.workspaceRoot != "" {
		r, err := os.Open(filepath.Join(b.root, f.Local))
		if err != nil {
			return `{"error":"任务附件缓存已失效，请重发或引用原消息"}`, nil
		}
		rel, err := b.saveOriginalReader(ctx, a.ID, r, f.MIMEType, maxOriginalBytes)
		r.Close()
		if err != nil {
			data, _ := json.Marshal(map[string]any{"ok": false, "downloaded": true, "error": err.Error()})
			return string(data), nil
		}
		f.Path = rel
		f.FileStatus = "原始字节已按需保存到workspace，任务结束后保留；未转码或替换成关键帧"
	} else if f.Path == "" {
		f.FileStatus = "原始字节已取得，仅本任务临时保留；未授予共享工作区或执行代码权限"
	}
	data, _ := json.Marshal(map[string]any{"ok": true, "name": f.Name, "size": f.Size, "mime": f.MIMEType, "sha256": f.SHA256, "path": f.Path, "temporary": f.Path == "", "fileStatus": f.FileStatus, "analysis": "只确认取得原件，不代表已理解完整视频/动图；预览和源文件是不同内容"})
	return string(data), nil
}

// Stream to a private task file; neither URLs nor original filenames become
// paths or model context. The same safe DNS/redirect client guards each fetch.
func (b *inputBundle) fetchOriginal(ctx context.Context, a inputAttachment) (local, mime string, size int64, hash string, err error) {
	raw := a.URL
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		err = fmt.Errorf("原文件地址未提供或无效，请引用原消息或重新发送")
		return
	}
	limit := min(maxOriginalBytes, maxOriginalTotal-b.fetchedBytes)
	if limit <= 0 {
		err = fmt.Errorf("当前任务原件下载总预算已用完（512MB）")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		err = fmt.Errorf("原件请求无效")
		return
	}
	if b.client == nil {
		b.client = tools.PublicDownloadClient(90 * time.Second)
	}
	res, e := b.client.Do(req)
	if e != nil {
		err = fmt.Errorf("原件下载失败，可能链接过期、地址受限或网络中断，请引用或重发")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		err = fmt.Errorf("原件下载失败（HTTP %d），请引用原消息或重发", res.StatusCode)
		return
	}
	if res.ContentLength > limit {
		err = fmt.Errorf("原件超过下载预算（单件256MB/本任务512MB）")
		return
	}
	if b.root == "" {
		b.root, e = os.MkdirTemp("", "mineagent-qq-input-")
		if e != nil {
			err = fmt.Errorf("无法创建任务附件缓存")
			return
		}
	}
	file, e := os.CreateTemp(b.root, "original-")
	if e != nil {
		err = fmt.Errorf("无法创建原件缓存")
		return
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(file.Name())
		}
	}()
	reader := bufio.NewReader(res.Body)
	head, _ := reader.Peek(512)
	mime = http.DetectContentType(head)
	if (strings.HasPrefix(strings.ToLower(a.ContentType), "video/") || isVideoFilename(a.Filename)) && strings.HasPrefix(mime, "image/") {
		err = fmt.Errorf("平台附件链接仅返回视频封面，没有视频源文件；请以文件形式发送视频原件")
		return
	}

	h := sha256.New()
	size, e = io.Copy(io.MultiWriter(file, h), io.LimitReader(reader, limit+1))
	b.fetchedBytes += size
	if e != nil || size == 0 || size > limit || ctx.Err() != nil {
		err = fmt.Errorf("原件未下载完整或超出预算，未保留部分文件")
		return
	}
	if e = file.Close(); e != nil {
		err = fmt.Errorf("原件缓存保存失败")
		return
	}
	success = true
	hash = fmt.Sprintf("%x", h.Sum(nil))
	local = filepath.Base(file.Name())
	return
}
