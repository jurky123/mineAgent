// Package usage 查询 opencode 网关的额度用量（GET {baseURL}/usage）。
// 结果带 60 秒缓存，避免网页和系统命令频繁打网关。
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Window struct {
	Status   string `json:"status"`
	Percent  int    `json:"percent"`
	ResetsAt string `json:"resetsAt"`
}

type Report struct {
	Rolling Window `json:"rolling"`
	Weekly  Window `json:"weekly"`
	Monthly Window `json:"monthly"`
}

type Client struct {
	baseURL string
	apiKey  string

	mu   sync.Mutex
	at   time.Time
	last *Report
}

func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

func (c *Client) Fetch(ctx context.Context) (*Report, error) {
	c.mu.Lock()
	if c.last != nil && time.Since(c.at) < time.Minute {
		r := *c.last
		c.mu.Unlock()
		return &r, nil
	}
	c.mu.Unlock()
	if c.baseURL == "" || c.apiKey == "" {
		return nil, fmt.Errorf("未配置模型 baseURL/apiKey")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("x-opencode-session", "mineagent-usage")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网关返回 %d", res.StatusCode)
	}
	var body struct {
		Usage Report `json:"usage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.last = &body.Usage
	c.at = time.Now()
	c.mu.Unlock()
	return &body.Usage, nil
}

// Text 取额度并格式化成一行中文（给系统命令/界面用）。
func (c *Client) Text(ctx context.Context) (string, error) {
	r, err := c.Fetch(ctx)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}

// Text 把额度格式化成一行中文，供聊天里的 /usage 或界面显示。
func (r *Report) Text() string {
	if r == nil {
		return "取不到额度信息"
	}
	part := func(name string, w Window) string {
		if w.ResetsAt == "" && w.Status == "" {
			return name + " 无数据"
		}
		return fmt.Sprintf("%s %d%%（%s重置）", name, w.Percent, ResetText(w.ResetsAt))
	}
	return "模型额度：" + part("滚动", r.Rolling) + " · " + part("本周", r.Weekly) + " · " + part("本月", r.Monthly)
}

// ResetText 把 ISO 时间转成"x 分钟后 / x 小时后 / x 天后 / 日期"。
func ResetText(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Until(t)
	switch {
	case d <= 0:
		return "即将"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟后", int(d.Minutes())+1)
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时后", int(d.Hours())+1)
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天后", int(d.Hours()/24)+1)
	default:
		return t.Local().Format("1月2日")
	}
}
