package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// QQSend 工具：让 QQ 通道的 agent 发版式/图片消息。
// 背景：agent 的纯文本回复走 consume->Reply 自动发送，不需要工具；
// 但 markdown（msg_type=2）和图片（msg_type=7）需要特殊 Target，
// agent 自己发不出来，所以给两个工具。
// 执行路径：工具返回 {"__qq_send":name,"target":...,"text":...} 指令 JSON，
// qqToolGate（cmd/mineagent/qqtools.go）识别后调 sender 发出并记库。
// 这样发送统一走 session fanout（channel.Send），工具本身不碰网络。
// 约束：
//   - receiver 固定为本次对话的 ReplyTarget（gate 从 Request 注入 ctx），
//     agent 只能发回当前会话，不能跨会话发，防滥用。
//   - 图片 path 必须 workspace 内相对路径（gate 调 resolve 校验，media.go 再拦一次）。
//
// MC 通道拿不到这两个工具（main.go 只装给 QQ）。
type QQSend struct{}

func NewQQSend() *QQSend { return &QQSend{} }

func (s *QQSend) Tools() []tool.BaseTool {
	out := []tool.BaseTool{
		&qqSendTool{
			name: "qq_markdown",
			desc: "发一条 markdown 版式消息到当前 QQ 会话（标题/加粗/列表/引用/链接都支持，不要用表格）。content 写 markdown 原文，5000字内。",
			params: map[string]*schema.ParameterInfo{
				"content": {Type: schema.String, Desc: "markdown 原文", Required: true},
			},
		},
		&qqSendTool{
			name: "qq_image",
			desc: "发送 workspace 内图片或动图到当前 QQ 会话，支持 PNG/JPG/GIF/WebP/BMP，保留 GIF 动画；20MB 内。path 使用相对路径。仅管理员可读取和发送 workspace 文件。",
			params: map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "workspace 内相对路径，如 plot.png", Required: true},
				"caption": {Type: schema.String, Desc: "图片说明，可空（v1 只发图不带字）"},
			},
		},
	}
	for _, media := range []struct{ name, desc string }{
		{"qq_file", "发送 workspace 文件（文档、代码、压缩包等，200MB 内）到当前 QQ 会话。仅管理员可用；上传失败如实说明，不声称已发送。"},
		{"qq_video", "发送 workspace MP4 视频到当前 QQ 会话，30MB 内；仅管理员可用。"},
		{"qq_audio", "发送 workspace 语音（SILK/MP3/WAV/OGG）到当前 QQ 会话，20MB 内；仅管理员可用。"},
	} {
		out = append(out, &qqSendTool{name: media.name, desc: media.desc, params: map[string]*schema.ParameterInfo{
			"path":    {Type: schema.String, Desc: "workspace 内相对路径", Required: true},
			"caption": {Type: schema.String, Desc: "可选说明"},
		}})
	}
	out = append(out, &qqSendTool{name: "qq_emoji", desc: "向当前 QQ 会话发送一个 Unicode 表情或简短颜文字；自定义表情包/动图使用 qq_image 发送 GIF。不是频道的表情表态接口。", params: map[string]*schema.ParameterInfo{"content": {Type: schema.String, Desc: "表情，如 👍 或 (｡･ω･｡)", Required: true}}})
	return out
}

type qqSendTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
}

func (t *qqSendTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

// QQSEND_KEY 是工具返回指令 JSON 里的标记键，gate 识别它。
const QQSEND_KEY = "__qq_send"

func (t *qqSendTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	target := QQReplyTargetFromContext(ctx)
	if target == "" {
		return errorJSON("取不到当前会话回执目标，无法发送"), nil
	}
	kind, _, _ := splitQQTarget(target)
	if kind != "c2c" && kind != "group" {
		return errorJSON("回执目标非法，无法发送"), nil
	}
	// 图片路径先验：workspace 内相对路径（gate 里 resolve 再验一次）。
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}
	switch t.name {
	case "qq_markdown":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return errorJSON("content 不能为空"), nil
		}
		if len([]rune(content)) > 5000 {
			return errorJSON("markdown 太长（5000字内），拆短再发"), nil
		}
		out, _ := json.Marshal(map[string]string{
			QQSEND_KEY: t.name,
			"target":   storage.KindMarkdown + target,
			"text":     content,
		})
		return string(out), nil
	case "qq_emoji":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" || len([]rune(content)) > 32 {
			return errorJSON("表情内容须为 1–32 个字符"), nil
		}
		out, _ := json.Marshal(map[string]string{QQSEND_KEY: t.name, "target": "emoji:" + target, "text": content})
		return string(out), nil
	case "qq_image", "qq_file", "qq_video", "qq_audio":
		path, _ := args["path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			return errorJSON("path 不能为空"), nil
		}
		caption, _ := args["caption"].(string)
		prefix := map[string]string{"qq_image": storage.KindImage, "qq_file": storage.KindFile, "qq_video": "video:", "qq_audio": "audio:"}[t.name]
		out, _ := json.Marshal(map[string]string{
			QQSEND_KEY: t.name,
			"target":   prefix + target + ":" + path,
			"text":     caption,
		})
		return string(out), nil
	default:
		return errorJSON("未知工具: " + t.name), nil
	}
}

// WithQQReplyTarget 供 agent.respond/resume 把 Request.ReplyTarget 放进 ctx。
// 注意 tools 包里已有 replyTargetCtxKey（privileged.go，给 qqToolGate 用），
// 这里复用同一个 key，两边互通。
func WithQQReplyTarget(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, replyTargetCtxKey{}, target)
}

// QQReplyTargetFromContext 取 gate 注入的回执目标。
func QQReplyTargetFromContext(ctx context.Context) string {
	v, _ := ctx.Value(replyTargetCtxKey{}).(string)
	return v
}

func splitQQTarget(target string) (kind, id, msgID string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// splitIMTarget 切两段式会话目标 "<c2c|group>:<id>"（企微/网页没有 msgID 段）。
// 只校验前两段，第三段（QQ 的 msgID）有就返回、没有也接受。
func splitIMTarget(target string) (kind, id string, ok bool) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) < 2 || parts[1] == "" {
		return "", "", false
	}
	if parts[0] != "c2c" && parts[0] != "group" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
