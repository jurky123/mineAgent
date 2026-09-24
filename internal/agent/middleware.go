package agent

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/tools"
)

type historyCutoffKey struct{}

func historyCutoffFrom(ctx context.Context) int64 {
	v, _ := ctx.Value(historyCutoffKey{}).(int64)
	return v
}

func (a *Agent) buildMiddlewares(ctx context.Context, cm model.BaseModel[*schema.Message]) ([]adk.ChatModelAgentMiddleware, error) {
	return a.buildMiddlewaresFor(ctx, cm, false)
}

// buildMiddlewaresFor 给 QQ 通道定制摘要指令：MC 侧保留"玩家名/事件"口径，
// QQ 侧按"QQ 对话"口径（之前 QQ 群摘要被 MC 口径带偏，模型被迫解释"没有玩家名"，
// 还出现过 DSML 工具调用残留混进摘要）。isQQ 由 runnerFor 按 Request 是否带
// SystemInstruction 决定（MC 请求两者都为空，走预建 mcRunner）。
func (a *Agent) buildMiddlewaresFor(ctx context.Context, cm model.BaseModel[*schema.Message], isQQ bool) ([]adk.ChatModelAgentMiddleware, error) {
	// 阈值全部可配（见 config.Agent）：越大记得越多、token 越多。
	// 默认摘要 12000 token/80 消息触发，截断兜底 24000 token 留最后 4 轮。
	sumTokens, sumMsgs := a.cfg.SummaryTokens, a.cfg.SummaryMessages
	if sumTokens <= 0 {
		sumTokens = 12000
	}
	if sumMsgs <= 0 {
		sumMsgs = 80
	}
	redTokens, redKeep := a.cfg.ReductionTokens, a.cfg.ReductionKeep
	if redTokens <= 0 {
		redTokens = 24000
	}
	if redKeep <= 0 {
		redKeep = 4
	}
	userInstruction := "请把上面的 Minecraft 服务器聊天记录压缩成简洁的中文摘要，保留玩家名、事件、约定和重要事实，不要遗漏未完成的事项。"
	if isQQ {
		userInstruction = "请把上面的 QQ 对话记录压缩成简洁的中文摘要，保留发言人、请求事项、已做决定和未完成的任务，不要遗漏未完成的事项。只输出摘要正文，不要输出工具调用原文或任何标记语言。"
	}
	summaryMW, err := summarization.New(ctx, &summarization.Config{
		Model: cm,
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   sumTokens,
			ContextMessages: sumMsgs,
		},
		UserInstruction: userInstruction,
		Finalize: func(ctx context.Context, original []*schema.Message, summary *schema.Message) ([]*schema.Message, error) {
			cutoff := historyCutoffFrom(ctx)
			// 会话隔离：摘要必须存到触发这次 run 的会话下，不能写死 MC 主会话。
			sessionKey := tools.SessionFromContext(ctx)
			if sessionKey == "" {
				sessionKey = a.sessionKey
			}
			if cutoff > 0 && summary != nil && summary.Content != "" {
				// 护栏：摘要里混入工具调用残留（DSML/invoke/parameter）说明模型
				// 没按要求输出，只记 warn 不入库——脏摘要比没摘要更坏，
				// 会污染之后每一轮的历史。
				if isQQ && looksLikeToolCall(summary.Content) {
					a.log.Warn("summary looks like tool call, dropped", "session", sessionKey, "chars", len(summary.Content))
				} else if _, err := a.store.SaveSummary(ctx, sessionKey, cutoff, summary.Content, time.Now().UnixMilli()); err != nil {
					a.log.Warn("save summary failed", "err", err, "session", sessionKey)
				} else {
					a.log.Info("summary saved", "session", sessionKey, "upToMessageId", cutoff, "chars", len(summary.Content))
				}
			}
			return summarization.DefaultFinalize(ctx, original, summary)
		},
	})
	if err != nil {
		return nil, err
	}

	reductionMW, err := reduction.New(ctx, &reduction.Config{
		SkipTruncation:            true,
		MaxTokensForClear:         int64(redTokens),
		ClearRetentionSuffixLimit: redKeep,
	})
	if err != nil {
		return nil, err
	}

	return []adk.ChatModelAgentMiddleware{summaryMW, reductionMW}, nil
}

// looksLikeToolCall 识别混入摘要的工具调用残留。
func looksLikeToolCall(s string) bool {
	for _, sub := range []string{"｜｜DSML｜｜", "invoke name=", "parameter name=", "<｜", "calls>"} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
