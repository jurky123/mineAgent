package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// QQBind 工具：QQ 用户绑定/解绑 MC 玩家名。
// 绑定后 QQ 发起的 teleport/give/run_command 在 tool.call 里带上绑定的 MC 名，
// 插件按"外部请求"执行（resolveRequester 失败→走控制台身份+审批前置）。
// 数据存 identity_links(platform=qq, platform_id=<openid>, display_name=<MC名>)，
// channel.go 的 UpsertIdentity 留痕行与它是同一张表：绑定命令用 display_name 存 MC 名
// 覆盖昵称留痕——LinkedMC 只读 display_name，所以绑定后查到的就是 MC 名。
// 解绑把 display_name 清空（行保留，避免与留痕逻辑冲突）。
type QQBind struct {
	store *storage.Store
	log   *slog.Logger
}

func NewQQBind(store *storage.Store, log *slog.Logger) *QQBind {
	return &QQBind{store: store, log: log}
}

func (b *QQBind) Tools() []tool.BaseTool {
	return []tool.BaseTool{
		&qqBindTool{b: b, bind: true},
		&qqBindTool{b: b, bind: false},
	}
}

type qqBindTool struct {
	b    *QQBind
	bind bool
}

func (t *qqBindTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	if t.bind {
		return &schema.ToolInfo{
			Name: "qq_bind",
			Desc: "把当前 QQ 用户绑定到一个 MC 玩家名。用户说「绑定 <名字>」时调用，绑定后他才能让助手操作 MC（传送/给物品/执行命令，转游戏内审批）。",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"player": {Type: schema.String, Desc: "MC 玩家名", Required: true},
			}),
		}, nil
	}
	return &schema.ToolInfo{
		Name: "qq_unbind",
		Desc: "解除当前 QQ 用户的 MC 身份绑定。用户说「解绑」时调用。",
	}, nil
}

type qqIdentityCtxKey struct{}

// WithQQIdentity 供 QQ 通道把当前用户的 openid 候选（union/user/member）放进 ctx。
func WithQQIdentity(ctx context.Context, openIDs []string) context.Context {
	return context.WithValue(ctx, qqIdentityCtxKey{}, openIDs)
}

func qqIdentityFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(qqIdentityCtxKey{}).([]string)
	return v
}

func (t *qqBindTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	ids := qqIdentityFromContext(ctx)
	if len(ids) == 0 {
		return errorJSON("取不到 QQ 身份，绑定失败"), nil
	}
	now := time.Now().UnixMilli()
	if !t.bind {
		for _, id := range ids {
			if id == "" {
				continue
			}
			_ = t.b.store.UpsertIdentity(ctx, "qq", id, "", now)
		}
		t.b.log.Info("qq unbind", "ids", len(ids))
		out, _ := json.Marshal(map[string]string{"ok": "true", "message": "已解绑"})
		return string(out), nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}
	name, _ := args["player"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return errorJSON("MC 玩家名不能为空"), nil
	}
	if len(name) > 32 || strings.ContainsAny(name, " \t\n:@") {
		return errorJSON("玩家名不合法（3-16 位字母数字下划线）"), nil
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := t.b.store.UpsertIdentity(ctx, "qq", id, name, now); err != nil {
			return errorJSON("绑定失败：" + err.Error()), nil
		}
	}
	t.b.log.Info("qq bind", "player", name)
	out, _ := json.Marshal(map[string]string{"ok": "true", "message": fmt.Sprintf("已绑定到 MC 玩家 %s，之后请求 MC 操作会转游戏内管理员审批", name)})
	return string(out), nil
}
