package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type rpcTool struct {
	gw     *Gateway
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
}

func (t *rpcTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

func (t *rpcTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if argumentsInJSON == "" {
		argumentsInJSON = "{}"
	}
	if !json.Valid([]byte(argumentsInJSON)) {
		return "", fmt.Errorf("invalid arguments: %s", argumentsInJSON)
	}
	out, err := t.gw.Call(ctx, t.name, json.RawMessage(argumentsInJSON))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(payload), nil
	}
	return out, nil
}

// ReadOnly 返回只读工具：MC 查询 + 网页搜索/抓取/时间（所有通道共用，无需审批）。
func ReadOnly(gw *Gateway) []tool.BaseTool {
	base := []tool.BaseTool{
		&rpcTool{
			gw:   gw,
			name: "minecraft_list_players",
			desc: "列出当前服务器在线的玩家，包含所在世界和延迟（ping）。回答“谁在线/有几个人”必须调用此工具。",
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_player_info",
			desc: "查询某位在线玩家的详细信息：所在世界、坐标、血量、饥饿值、游戏模式、延迟。",
			params: map[string]*schema.ParameterInfo{
				"player": {Type: schema.String, Desc: "玩家名（不含 @）", Required: true},
			},
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_server_status",
			desc: "查询服务器运行状态：TPS、在线人数、内存占用、服务器版本。回答“卡不卡/服务器状态”必须调用此工具。",
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_world_time",
			desc: "查询各世界当前游戏时间（白天/黑夜/具体时刻）。",
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_weather",
			desc: "查询各世界当前天气：是否下雨、是否雷暴。",
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_world_info",
			desc: "查询各世界的详细信息：名称、环境（主世界/地狱/末地/自定义）、难度、玩家数、区块数、游戏时间。回答“有哪些世界/世界情况”必须调用此工具。",
		},
		&rpcTool{
			gw:   gw,
			name: "minecraft_plugin_list",
			desc: "列出服务器安装的插件及版本、是否启用。回答“装了什么插件/有没有XX插件”必须调用此工具。",
		},
	}
	return append(base, webTools()...)
}
