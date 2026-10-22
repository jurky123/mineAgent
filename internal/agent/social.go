package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"mineagent/internal/dshbridge"
)

// respondSocial runs inside the existing FIFO. In particular, it must never
// fall back to the default tool registry or withSkills (an empty Tools list in
// the ordinary agent path means the default registry, not no tools).
func (a *Agent) respondSocial(ctx context.Context, req Request, msgs []*schema.Message) {
	if !strings.HasPrefix(req.SessionKey, "qq:group:") {
		return
	}
	// Optional banter only needs the recent exchange. Do not repeatedly send
	// long document previews or the whole task history for a one-line reply.
	if len(msgs) > 24 {
		msgs = msgs[len(msgs)-24:]
	}
	compact := make([]*schema.Message, 0, len(msgs))
	for _, msg := range msgs {
		copy := *msg
		text := []rune(copy.Content)
		if len(text) > 1500 {
			copy.Content = string(text[:1500]) + "\n[较长历史内容省略]"
		}
		compact = append(compact, &copy)
	}
	msgs = compact
	instruction := req.SystemInstruction + `
本轮是可选的群聊接话，不是收到指派任务。判断最近几位成员的对话：有贴切、有用或有趣的一两句才接话。已经说完、重复内容、别人互相交谈、指令或需要执行任务时保持安静。不要每句都接，也不要提及你在判断是否接话。
决定不说话时只返回 [[QQ_SILENT]]。决定说话时直接给最终群消息，一般一两句，不输出判断过程或舞台动作。本轮没有任何工具，不能读文件、联网、操作系统或游戏，不能承诺已执行操作；需要这些能力时等对方明确 @ 你请求。`
	var reply string
	var err error
	if a.dsh != nil {
		if route, ok := a.dsh.Route(req.Model, a.modelCfg.Name); ok {
			var messages []dshbridge.Message
			messages, err = dshMessages(msgs)
			if err == nil {
				reply, err = a.dsh.Run(ctx, dshbridge.Request{Route: route, Instruction: instruction, Messages: messages, Tools: []dshbridge.Tool{}, MaxIterations: 1, ReasoningEffort: dshReasoningEffort(route, req.ReasoningEffort, false)}, func(context.Context, string, string) (string, error) {
					return "", fmt.Errorf("tools are unavailable in optional group chat")
				})
			}
			a.finishSocial(ctx, req, reply, err)
			return
		}
	}
	cm, err := a.chatModel(ctx, req.Model, req.ReasoningEffort)
	if err == nil {
		var out *schema.Message
		out, err = cm.Generate(ctx, append([]*schema.Message{schema.SystemMessage(instruction)}, msgs...))
		if out != nil && len(out.ToolCalls) == 0 {
			reply = out.Content
		}
	}
	a.finishSocial(ctx, req, reply, err)
}

func (a *Agent) finishSocial(ctx context.Context, req Request, reply string, err error) {
	if err != nil {
		a.log.Warn("optional QQ chat failed", "err", err)
		return
	}
	reply = strings.TrimSpace(reply)
	if ctx.Err() != nil || reply == "" || strings.Contains(reply, "[[QQ_SILENT]]") || len([]rune(reply)) > 400 {
		return
	}
	if err := req.Session.Reply(ctx, reply, req.ReplyTarget); err != nil {
		a.log.Warn("optional QQ chat delivery failed", "err", err)
	}
}
