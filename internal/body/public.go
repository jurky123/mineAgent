package body

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// PublicSnapshot never includes coordinates, inventory, events or controller identity.
func (s *Service) PublicSnapshot() map[string]any {
	p := s.Profile()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{"enabled": s.Enabled(), "name": s.cfg.Name, "status": p["status"], "activity": p["activity"], "stale": p["stale"], "paused": s.paused || s.state.Paused}
	if !s.Enabled() {
		out["status"], out["activity"] = "disabled", "idle"
	}
	return out
}

func (s *Service) MCInstruction() string { return s.MCInstructionFor(false, "") }

// MCInstructionFor builds the per-request body section. Only the Paper plugin
// can assert CanBody (mineagent.body); it is still checked by body.Service.
func (s *Service) MCInstructionFor(canControl bool, requester string) string {
	b, _ := json.Marshal(s.PublicSnapshot())
	if canControl {
		return "\n身体状态（服务端事实）：" + string(b) +
			"。你的真实游戏身体是 " + s.cfg.Name + "，请求者 " + requester +
			" 已获 mineagent.body 游戏权限，可以直接指挥身体。" +
			"可用工具：body_status、body_observe、body_inventory、body_find_blocks、body_goto、body_look_at、body_stop、body_dig、body_place、body_interact、body_use_item、body_say、body_command。" +
			"要传送到请求者身边，直接用 body_command，command 填 `tp " + requester + "`（Essentials 自身传送；也可 `tppos x y z`）；不要回答做不到、不要要求网页关联、不要用 body_goto 慢慢走。" +
			"先 body_status 确认在线/暂停；tp/tppos 后身体可能因大位移暂停，如果已到达就直接说已到达，并提示在 AI 玩家面板点恢复，不要重试或说失败；动作以工具返回为准；公开聊天回复简短直接，不要编造已执行。"
	}
	return "\n身体公开状态（服务端事实）：" + string(b) + "。启用时你的真实游戏身体是这个玩家；不要回答没有身体或不在游戏世界。未启用时明确说身体未启用。普通玩家只可查 body_profile 的公开状态，不提供身体坐标/背包，不可发起动作。要指挥身体，请在 /agent → AI 玩家关联已验证管理员，或到 /ai/main。只答本次问题，最多80字两行，不引用无关玩家历史。"
}

type publicTool struct{ s *Service }

func (t *publicTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "body_profile", Desc: "查询我自己的游戏身体公开状态：名字、启用、在线、活动与暂停；不含坐标或背包。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}
func (t *publicTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return output(t.s.PublicSnapshot()), nil
}
func (s *Service) PublicTools() []tool.BaseTool { return []tool.BaseTool{&publicTool{s}} }
