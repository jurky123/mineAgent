package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// WebSend 工具：让网页通道的 agent 把 workspace 内的文件/图片发到网页会话。
// 与 QQSend/WeComSend 同构：返回 {"__web_send":name,"target":...,"text":...} 指令 JSON，
// webToolGate（cmd/mineagent/webtools.go）识别后走 session fanout 发送。
// 约束：target 固定为本次对话 ReplyTarget（agent.respond 注入 ctx），
// agent 只能发回当前会话；path 必须 workspace 内相对路径。
// 图片（png/jpg/gif/webp）在网页里内联显示，其它类型给下载卡片。
type WebSend struct{}

func NewWebSend() *WebSend { return &WebSend{} }

func (s *WebSend) Tools() []tool.BaseTool {
	return []tool.BaseTool{
		&webSendTool{
			name: "web_file",
			desc: "把一个 workspace 内的文件发到当前网页会话。图片（png/jpg/gif/webp）会内联显示，其它文件给下载链接。20MB内。先用 workspace_write/exec 把文件做好，path 写 workspace 相对路径，caption 可空。",
			params: map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "workspace 内相对路径，如 report.pdf", Required: true},
				"caption": {Type: schema.String, Desc: "展示名/说明，可空（默认用文件名）"},
			},
		},
	}
}

type webSendTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
}

func (t *webSendTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

// WEBSEND_KEY 是工具返回指令 JSON 里的标记键，gate 识别它。
const WEBSEND_KEY = "__web_send"

func (t *webSendTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	target := QQReplyTargetFromContext(ctx)
	if target == "" {
		return errorJSON("取不到当前会话回执目标，无法发送"), nil
	}
	// 网页只有单聊（按账号隔离），group 目标一律拒绝。
	if kind, _, ok := splitIMTarget(target); !ok || kind != "c2c" {
		return errorJSON("回执目标非法，无法发送"), nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}
	path, _ := args["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return errorJSON("path 不能为空"), nil
	}
	caption, _ := args["caption"].(string)
	out, _ := json.Marshal(map[string]string{
		WEBSEND_KEY: t.name,
		"target":    storage.KindFile + target + ":" + path,
		"text":      caption,
	})
	return string(out), nil
}
