package tools

import (
	"context"
	"fmt"
	"mineagent/internal/storage"
	"strings"
)

func ParseEffort(arg string) (string, bool, error) {
	fields := strings.Fields(strings.ToLower(arg))
	if len(fields) == 0 {
		return "", false, nil
	}
	if len(fields) == 1 {
		switch fields[0] {
		case "low", "低":
			return "low", true, nil
		case "medium", "中":
			return "medium", true, nil
		case "high", "高":
			return "high", true, nil
		case "default", "默认":
			return "", true, nil
		}
	}
	return "", false, fmt.Errorf("用法：/effort low|medium|high|default（也可用 低/中/高/默认）；单独 /effort 查看当前值")
}

func EffortCommand(ctx context.Context, store *storage.Store, key, arg, fallback string) (string, error) {
	value, change, err := ParseEffort(arg)
	if err != nil {
		return err.Error(), nil
	}
	if change {
		if err = store.SetSessionEffort(ctx, key, value); err != nil {
			return "", err
		}
	} else {
		value, err = store.SessionEffort(ctx, key)
		if err != nil {
			return "", err
		}
	}
	labels := map[string]string{"low": "低（low）", "medium": "中（medium）", "high": "高（high）"}
	label := labels[value]
	if value == "" {
		label = "默认"
		if labels[fallback] != "" {
			label += "（当前沿用 " + labels[fallback] + "）"
		} else {
			label += "（沿用账号或站点设置）"
		}
	}
	if change {
		return "当前会话思考强度已设为：" + label + "。从下一条新请求开始生效，已提交的任务不变。", nil
	}
	return "当前会话思考强度：" + label + "。\n/effort low|medium|high|default 调整；实际支持情况取决于模型。", nil
}
