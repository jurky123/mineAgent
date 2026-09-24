package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// 用户（网页端）上传的图片直接作为视觉输入附进消息里：模型本身能看图，
// 就不用再拿 PIL 逐像素"猜"（实测那样又慢又不准，一条图片消息跑几分钟）。
// 每次 run 只给最后一条用户消息附图（避免历史里每张图重复上传，浪费 token）；
// 最多 4 张、单张 4MB。
const (
	maxVisionImages = 4
	maxVisionBytes  = 4 << 20
)

// userMessage 构造一条用户消息：有图片附件且 withVision 时走多模态
// （UserInputMultiContent），否则退化成纯文本。
// 非图片文件保留 [[file:...]] 标记（agent 用 workspace 工具处理）。
func (a *Agent) userMessage(name, text string, withVision bool) *schema.Message {
	plain := func(t string) *schema.Message {
		return schema.UserMessage(fmt.Sprintf("[%s] %s", name, t))
	}
	if !withVision {
		return plain(text)
	}
	clean, files := storage.ParseFileMarkers(text)
	if len(files) == 0 {
		return plain(text)
	}
	var (
		parts    []schema.MessageInputPart
		kept     []string
		attached int
	)
	for _, f := range files {
		if !storage.IsImageMime(f.Mime) || attached >= maxVisionImages {
			kept = append(kept, storage.BuildFileMarker(f))
			continue
		}
		data, mimeType, err := a.readWorkspaceImage(f.Path)
		if err != nil {
			kept = append(kept, storage.BuildFileMarker(f))
			continue
		}
		b64 := data
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: mimeType},
				Detail:            schema.ImageURLDetailAuto,
			},
		})
		attached++
	}
	if attached == 0 {
		return plain(text)
	}
	body := fmt.Sprintf("[%s] %s", name, clean)
	if len(kept) > 0 {
		body += "\n" + strings.Join(kept, "\n")
	}
	body += fmt.Sprintf("\n[用户上传的 %d 张图片已直接附在消息里，你能看到图片内容]", attached)
	head := schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: body}
	return &schema.Message{Role: schema.User, UserInputMultiContent: append([]schema.MessageInputPart{head}, parts...)}
}

// readWorkspaceImage 读 workspace 内图片并 base64，返回 (base64, mime)。
func (a *Agent) readWorkspaceImage(rel string) (string, string, error) {
	if a.workspaceRoot == "" || !storage.SafeWorkspaceRel(rel) {
		return "", "", fmt.Errorf("非法路径")
	}
	full := filepath.Join(a.workspaceRoot, filepath.FromSlash(rel))
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		return "", "", fmt.Errorf("读不到图片")
	}
	if fi.Size() > maxVisionBytes {
		return "", "", fmt.Errorf("图片超过 %dMB", maxVisionBytes>>20)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", "", err
	}
	mimeType := http.DetectContentType(b)
	if !strings.HasPrefix(mimeType, "image/") {
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".webp":
			mimeType = "image/webp"
		case ".avif":
			mimeType = "image/avif"
		default:
			mimeType = "image/jpeg"
		}
	}
	return base64.StdEncoding.EncodeToString(b), mimeType, nil
}
