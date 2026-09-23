package agent

import (
	"context"
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
	summaryMW, err := summarization.New(ctx, &summarization.Config{
		Model: cm,
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   6000,
			ContextMessages: 40,
		},
		UserInstruction: "请把上面的 Minecraft 服务器聊天记录压缩成简洁的中文摘要，保留玩家名、事件、约定和重要事实，不要遗漏未完成的事项。",
		Finalize: func(ctx context.Context, original []*schema.Message, summary *schema.Message) ([]*schema.Message, error) {
			cutoff := historyCutoffFrom(ctx)
			// 会话隔离：摘要必须存到触发这次 run 的会话下，不能写死 MC 主会话。
			sessionKey := tools.SessionFromContext(ctx)
			if sessionKey == "" {
				sessionKey = a.sessionKey
			}
			if cutoff > 0 && summary != nil && summary.Content != "" {
				if _, err := a.store.SaveSummary(ctx, sessionKey, cutoff, summary.Content, time.Now().UnixMilli()); err != nil {
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
		MaxTokensForClear:         12000,
		ClearRetentionSuffixLimit: 2,
	})
	if err != nil {
		return nil, err
	}

	return []adk.ChatModelAgentMiddleware{summaryMW, reductionMW}, nil
}
