// Package commentary 是"AI 解说"的最小实现：调一次 OpenAI 兼容的 /chat/completions，
// 给一步棋生成一两句中文解说（便宜、快、失败不影响对局）。
package commentary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	log     *slog.Logger
}

func New(baseURL, apiKey, model string, log *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

// Enabled 没配模型时直接禁用（调用方据此隐藏解说入口）。
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" && c.model != "" }

const system = `你是棋局直播间的弹幕。用一句简体中文吐槽/点评刚刚这一步，像直播弹幕那样短、口语、有情绪，
可以玩梗、可以夸张、可以起哄，但不要骂人、不要引战、不要复述局面数据（子力/FEN 之类不要念）。
要求：20 字以内、一句话、不要 Markdown、不要 emoji、不要引号、不要编号，直接输出弹幕本身。
例：「这步有点急啊」「稳如老狗」「白棋要凉」「好家伙，直接弃子」「这下有得看了」。`

// Comment 生成一步的解说。prompt 由各游戏提供（局面 + 这一步）。
func (c *Client) Comment(ctx context.Context, prompt string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("解说未配置模型")
	}
	body, _ := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": prompt},
		},
		"max_tokens":  800, // 网关的 flash 模型会先"思考"，额度给小了正文会是空的
		"temperature": 0.7,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// 网关（opencode zen）要求带 session 头才能路由；和 agent 侧一致
	req.Header.Set("User-Agent", "mineagent/commentary")
	req.Header.Set("x-opencode-session", "mineagent-commentary")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解说响应解析失败: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		msg := "HTTP " + res.Status
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("解说模型失败: %s", msg)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("解说模型没有返回内容")
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	text = strings.Trim(text, `"'“”`)
	if text == "" {
		return "", fmt.Errorf("解说模型返回空")
	}
	return text, nil
}
