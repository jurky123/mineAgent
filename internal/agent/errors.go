package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// Public hints explain the next step without echoing gateway bodies or credentials.
func modelFailureHint(err error) string {
	if errors.Is(err, context.Canceled) {
		return "本次请求已停止；请先检查已有结果，再决定是否重新发送"
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) || strings.Contains(err.Error(), "Client.Timeout") {
		return "等待模型网关响应超时；本次未自动重发，可降低思考强度或切换模型后再试"
	}
	var apiError *openai.APIError
	if errors.As(err, &apiError) {
		switch apiError.HTTPStatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "模型提供商拒绝访问；请管理员检查 API Key 和该模型的访问权限"
		case http.StatusPaymentRequired:
			return "模型提供商额度或余额不足；请检查账户额度，或切换其他模型"
		case http.StatusTooManyRequests:
			return "模型提供商限流或上游暂时无可用容量；请稍后再试或切换模型，本次未自动重发"
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return "模型拒绝了当前请求，可能不支持对话协议、工具、附件或思考参数；请尝试其他模型，或简化输入与设置"
		case http.StatusNotFound:
			return "模型或调用端点不存在，可能已下线；请刷新模型列表并切换模型"
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return "这个模型网关当前不可用，在输入框的模型选择里换一个再发"
		}
		return "模型提供商返回错误；请先检查已有结果，再决定是否重试"
	}
	if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "unavailable") {
		return "这个模型网关当前不可用，在输入框的模型选择里换一个再发"
	}
	return "请先检查已有结果，再决定是否重试"
}
