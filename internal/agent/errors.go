package agent

import (
	"context"
	"errors"
	"net"
	"strings"
)

// Public hints explain the next step without echoing gateway bodies or credentials.
func modelFailureHint(err error) string {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) || strings.Contains(err.Error(), "Client.Timeout") {
		return "等待模型网关响应超时；本次未自动重发，可降低思考强度或切换模型后再试"
	}
	if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "unavailable") {
		return "这个模型网关当前不可用，在输入框的模型选择里换一个再发"
	}
	return "请先检查已有结果，再决定是否重试"
}
