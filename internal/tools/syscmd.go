package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mineagent/internal/storage"
)

// 系统命令：硬编码直回，不走 LLM。
// 这些是 agent 系统层面的东西（帮助/状态/记忆/绑定/身份），真相只有一份，
// 必须由代码实时查出来直接回，不能让模型自由发挥（会编假命令、编假状态）。
// 两条通道都在收消息处先匹配，命中直接回，连 agent 队列都不进：
//   - /help、帮助、命令、你能干什么
//   - /status、状态
//   - /memory、/memory clear、/memory summary、记忆
//   - /bind <MC名>、/unbind、绑定、解绑
//   - /myid、我是谁
// 匹配规则见 MatchSystemCommand；执行见 ExecSystemCommand。
// 注意：MC 通道只支持 help/status/memory/myid（服内说话的就是玩家本人，
// bind/unbind 在 MC 无意义，提示去 QQ 绑）。

// MatchSystemCommand 判断文本是不是系统命令，返回规范命令名：
// help / status / memory / memory_clear / memory_summary / bind / unbind / myid / ""。
// 匹配失败返回 ""，调用方继续走正常 agent 流程。
func MatchSystemCommand(text string) (cmd, arg string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", ""
	}
	// 带 / 前缀的优先按命令解析（QQ 私聊/群 @ 后，MC 是 @agent 后）。
	if strings.HasPrefix(t, "/") {
		fields := strings.Fields(t)
		name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
		rest := ""
		if len(fields) > 1 {
			rest = strings.Join(fields[1:], " ")
		}
		switch name {
		case "help", "帮助":
			return "help", ""
		case "status":
			return "status", ""
		case "memory":
			sub := strings.ToLower(strings.TrimSpace(rest))
			switch sub {
			case "clear":
				return "memory_clear", ""
			case "summary":
				return "memory_summary", ""
			default:
				return "memory", ""
			}
		case "bind":
			return "bind", strings.TrimSpace(rest)
		case "unbind":
			return "unbind", ""
		case "myid":
			return "myid", ""
		}
		return "", ""
	}
	// 中文自然说法（不带 / 也认）。
	switch t {
	case "帮助", "命令", "你能干什么", "有什么功能", "功能":
		return "help", ""
	case "状态", "现在什么情况", "服还活着吗", "服务器状态":
		return "status", ""
	case "记忆", "记得什么", "看看记忆":
		return "memory", ""
	case "解绑":
		return "unbind", ""
	case "我是谁", "我的绑定", "我是管理吗", "我是管理员吗":
		return "myid", ""
	}
	// "绑定 X" / "绑定X"（不带 /）。
	if rest, ok := strings.CutPrefix(t, "绑定"); ok {
		return "bind", strings.TrimSpace(rest)
	}
	return "", ""
}

// SysCtx 是执行系统命令需要的上下文，由通道层组装好传进来。
// 这样 ExecSystemCommand 不依赖任何通道私有类型，MC/QQ 共用。
type SysCtx struct {
	Ctx       context.Context
	Store     *storage.Store
	SessionID string // 当前会话 key（memory/status 读它）
	Channel   string // "minecraft" 或 "qq"
	IsAdmin   bool   // QQ 管理员（workspace 门禁用）；MC 恒 false
	Model     string // 模型名（status 展示）
	// MCStatus 实时查服状态，失败返回 error（status 里写"服没连上"）。
	// QQ 通道传网关调用闭包，MC 通道同样（都是经 gw 调 minecraft_server_status）。
	MCStatus func(ctx context.Context) (string, error)
	// QQStatus 返回 QQ 网关连接状态；MC 通道传 nil（status 里不显示这行）。
	QQStatus func() string
	// QQIDs 当前用户的 openid 候选（union/user/member）；MC 通道为空。
	QQIDs []string
	// Requester 请求者展示名：MC=玩家名，QQ=qq:<openid>。
	Requester string
}

// ExecSystemCommand 执行系统命令，返回直接回复用户的文本。
// 所有分支都是查 store/调网关/拼字符串的确定性逻辑，不调 LLM。
func ExecSystemCommand(s SysCtx, cmd, arg string) string {
	switch cmd {
	case "help":
		return sysHelp(s)
	case "status":
		return sysStatus(s)
	case "memory":
		return sysMemory(s, "info")
	case "memory_summary":
		return sysMemory(s, "summary")
	case "memory_clear":
		return sysMemory(s, "clear")
	case "bind":
		return sysBind(s, arg)
	case "unbind":
		return sysBind(s, "")
	case "myid":
		return sysMyID(s)
	default:
		return ""
	}
}

func sysHelp(s SysCtx) string {
	var b strings.Builder
	if s.Channel == "qq" {
		b.WriteString("MineAgent 命令（QQ）：\n")
		b.WriteString("/help —— 显示这份帮助\n")
		b.WriteString("/status —— 服状态、连接状态、会话消息数\n")
		b.WriteString("/memory —— 看当前会话记了多少（/memory clear 清空，/memory summary 看摘要）\n")
		b.WriteString("/bind <MC名> —— 绑定 MC 身份（MC 操作审批用）；/unbind 解绑；/myid 看身份\n")
		b.WriteString("直接说话就是聊天；要版式说一声，要图说一声\n")
		if s.IsAdmin {
			b.WriteString("管理员：workspace 写代码跑代码可用（沙箱内，curl/pip 经审查）")
		} else {
			b.WriteString("写代码跑代码仅管理员可用")
		}
	} else {
		b.WriteString("MineAgent 命令（服内，@agent 提问外再加）：\n")
		b.WriteString("@agent /help —— 显示这份帮助\n")
		b.WriteString("@agent /status —— 服状态、在线玩家\n")
		b.WriteString("@agent /memory —— 当前会话记忆概况\n")
		b.WriteString("MC 操作（传送/给物/命令）直接说，管理员批准后执行")
	}
	return b.String()
}

func sysStatus(s SysCtx) string {
	var b strings.Builder
	n, _ := s.Store.CountMessages(s.Ctx, s.SessionID)
	fmt.Fprintf(&b, "会话：%s（%d 条消息）\n", s.SessionID, n)
	if s.Model != "" {
		fmt.Fprintf(&b, "模型：%s\n", s.Model)
	}
	if s.MCStatus != nil {
		out, err := s.MCStatus(s.Ctx)
		if err != nil {
			b.WriteString("MC 服：没连上（" + truncate(err.Error(), 80) + "）\n")
		} else {
			b.WriteString("MC 服：" + truncate(out, 200) + "\n")
		}
	}
	if s.Channel == "qq" && s.QQStatus != nil {
		fmt.Fprintf(&b, "QQ 网关：%s\n", s.QQStatus())
		if s.IsAdmin {
			b.WriteString("你是管理员（workspace 可用）")
		} else {
			b.WriteString("你是普通身份（workspace 不可用）")
		}
	}
	return strings.TrimSpace(b.String())
}

func sysMemory(s SysCtx, action string) string {
	if s.SessionID == "" {
		return "取不到当前会话"
	}
	switch action {
	case "info":
		n, _ := s.Store.CountMessages(s.Ctx, s.SessionID)
		sum, _ := s.Store.LatestSummary(s.Ctx, s.SessionID)
		var b strings.Builder
		fmt.Fprintf(&b, "当前会话 %s：%d 条消息", s.SessionID, n)
		if sum == nil {
			b.WriteString("，暂无摘要")
		} else {
			total, _ := s.Store.CountSummaries(s.Ctx, s.SessionID)
			fmt.Fprintf(&b, "，摘要 %d 份，最新截止到 #%d：%s",
				total, sum.UpToMessageID, truncate(sum.Text, 200))
		}
		return b.String()
	case "summary":
		sum, _ := s.Store.LatestSummary(s.Ctx, s.SessionID)
		if sum == nil {
			return "当前会话暂无摘要（消息还不够多，还没触发压缩）"
		}
		return fmt.Sprintf("最新摘要（截止 #%d）：\n%s", sum.UpToMessageID, sum.Text)
	case "clear":
		if err := s.Store.ClearSession(s.Ctx, s.SessionID); err != nil {
			return "清空失败：" + err.Error()
		}
		return "已清空当前会话的全部消息和摘要，从一张白纸开始聊"
	default:
		return "action 只能是 info|summary|clear"
	}
}

func sysBind(s SysCtx, player string) string {
	if s.Channel != "qq" {
		return "服内说话的本来就是玩家本人，不用绑定，去 QQ 里绑"
	}
	if len(s.QQIDs) == 0 {
		return "取不到 QQ 身份，绑定失败"
	}
	now := time.Now().UnixMilli()
	if player == "" {
		// unbind：display_name 清空（行保留）。
		for _, id := range s.QQIDs {
			if id != "" {
				_ = s.Store.UpsertIdentity(s.Ctx, BindPlatform, id, "", now)
			}
		}
		return "已解绑"
	}
	player = strings.TrimSpace(player)
	if len(player) > 32 || strings.ContainsAny(player, " \t\n:@") {
		return "玩家名不合法（字母数字下划线，不要带空格/@）"
	}
	for _, id := range s.QQIDs {
		if id == "" {
			continue
		}
		if err := s.Store.UpsertIdentity(s.Ctx, BindPlatform, id, player, now); err != nil {
			return "绑定失败：" + err.Error()
		}
	}
	return fmt.Sprintf("已绑定到 MC 玩家 %s，之后请求 MC 操作会转游戏内管理员审批", player)
}

func sysMyID(s SysCtx) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是 %s", s.Requester)
	if s.Channel == "qq" {
		var bound string
		for _, id := range s.QQIDs {
			if id == "" {
				continue
			}
			if name, err := s.Store.LinkedMC(s.Ctx, BindPlatform, id); err == nil && name != "" {
				bound = name
				break
			}
		}
		if bound != "" {
			fmt.Fprintf(&b, "，绑定 MC：%s", bound)
		} else {
			b.WriteString("，未绑定 MC 身份（/bind <MC名> 可绑）")
		}
		if s.IsAdmin {
			b.WriteString("，管理员")
		}
	} else {
		b.WriteString("（服内玩家本人）")
	}
	return b.String()
}
