package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// WeComSend 工具：让企微通道的 agent 发版式/图片消息。
// 与 QQSend 同构：返回 {"__wecom_send":name,"target":...,"text":...} 指令 JSON，
// wecomToolGate（cmd/mineagent/wecomtools.go）识别后走 session fanout 发送。
// 约束：target 固定为本次对话 ReplyTarget（agent.respond 注入 ctx），
// agent 只能发回当前会话；图片 path 必须 workspace 内相对路径。
// MC / QQ 通道拿不到这两个工具（main.go 只装给企微）。
type WeComSend struct{}

func NewWeComSend() *WeComSend { return &WeComSend{} }

func (s *WeComSend) Tools() []tool.BaseTool {
	return []tool.BaseTool{
		&wecomSendTool{
			name: "wecom_markdown",
			desc: "发一条 markdown 版式消息到当前企业微信会话（标题/加粗/列表/引用/链接都支持，不要用表格）。content 写 markdown 原文，5000字内。",
			params: map[string]*schema.ParameterInfo{
				"content": {Type: schema.String, Desc: "markdown 原文", Required: true},
			},
		},
		&wecomSendTool{
			name: "wecom_image",
			desc: "发一张 workspace 内的图片到当前企业微信会话。先用 workspace_write/exec 把图做好（jpg/png，10MB内），再调这个发。path 写 workspace 相对路径，如 plot.png。",
			params: map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "workspace 内相对路径，如 plot.png", Required: true},
				"caption": {Type: schema.String, Desc: "图片说明，可空（v1 只发图不带字）"},
			},
		},
	}
}

type wecomSendTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
}

func (t *wecomSendTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

// WECOMSEND_KEY 是工具返回指令 JSON 里的标记键，gate 识别它。
const WECOMSEND_KEY = "__wecom_send"

func (t *wecomSendTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	target := QQReplyTargetFromContext(ctx)
	if target == "" {
		return errorJSON("取不到当前会话回执目标，无法发送"), nil
	}
	if _, _, ok := splitIMTarget(target); !ok {
		return errorJSON("回执目标非法，无法发送"), nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}
	switch t.name {
	case "wecom_markdown":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return errorJSON("content 不能为空"), nil
		}
		if len([]rune(content)) > 5000 {
			return errorJSON("markdown 太长（5000字内），拆短再发"), nil
		}
		out, _ := json.Marshal(map[string]string{
			WECOMSEND_KEY: t.name,
			"target":      storage.KindMarkdown + target,
			"text":        content,
		})
		return string(out), nil
	case "wecom_image":
		path, _ := args["path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			return errorJSON("path 不能为空"), nil
		}
		caption, _ := args["caption"].(string)
		out, _ := json.Marshal(map[string]string{
			WECOMSEND_KEY: t.name,
			"target":      storage.KindImage + target + ":" + path,
			"text":        caption,
		})
		return string(out), nil
	default:
		return errorJSON("未知工具: " + t.name), nil
	}
}
