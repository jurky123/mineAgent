package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// TokenSource 按官方文档管 access_token：
// POST {apiBase}/app/getAppAccessToken {appId, clientSecret} -> {access_token, expires_in}。
// 有效期内重复获取返回相同值；过期前 60s 内会发新值，老值仍有效 60s。
// 实现：缓存 + 提前 120s 刷新 + 单飞 + 失败重试沿用旧值（宽限 60s）。
type TokenSource struct {
	appID     string
	secret    string
	apiBase   string
	log       *slog.Logger
	client    *http.Client
	mu        sync.Mutex
	token     string
	expiresAt time.Time
	inflight  chan struct{}
}

func NewTokenSource(appID, secret, apiBase string, log *slog.Logger) *TokenSource {
	return &TokenSource{
		appID:   appID,
		secret:  secret,
		apiBase: apiBase,
		log:     log,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   any    `json:"expires_in"`
	Code        int    `json:"code"`
	Message     string `json:"message"`
}

func expiresInSeconds(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		var s int64
		_, _ = fmt.Sscanf(n, "%d", &s)
		return s
	default:
		return 7200
	}
}

// Token 返回可用 token。ctx 只做超时用，不做长期持有。
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.token != "" && time.Now().Before(t.expiresAt) {
		tok := t.token
		t.mu.Unlock()
		return tok, nil
	}
	if t.inflight != nil {
		wait := t.inflight
		t.mu.Unlock()
		select {
		case <-wait:
			t.mu.Lock()
			tok := t.token
			t.mu.Unlock()
			if tok == "" {
				return "", fmt.Errorf("qq token refresh failed")
			}
			return tok, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	t.inflight = make(chan struct{})
	t.mu.Unlock()

	tok, ttl, err := t.fetch(ctx)

	t.mu.Lock()
	if err == nil {
		t.token = tok
		// 提前 120s 刷新；官方文档默认 7200s。
		if ttl > 180 {
			ttl -= 120
		} else if ttl > 60 {
			ttl -= 30
		}
		t.expiresAt = time.Now().Add(time.Duration(ttl) * time.Second)
		t.log.Info("qq token refreshed", "ttl", ttl)
	} else {
		// 刷新失败但旧值还在宽限内（过期后 60s）就沿用，避免一次抖动全断。
		if t.token != "" && time.Now().Before(t.expiresAt.Add(60*time.Second)) {
			t.log.Warn("qq token refresh failed, reusing old token", "err", err)
			tok = t.token
		} else {
			t.log.Error("qq token refresh failed", "err", err)
		}
	}
	close(t.inflight)
	t.inflight = nil
	t.mu.Unlock()

	if err != nil && tok == "" {
		return "", err
	}
	return tok, nil
}

func (t *TokenSource) fetch(ctx context.Context) (string, int64, error) {
	body, _ := json.Marshal(map[string]string{"appId": t.appID, "clientSecret": t.secret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiBase+"/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", 0, err
	}
	var tr tokenResp
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", 0, fmt.Errorf("qq token decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("qq token failed: code=%d msg=%s", tr.Code, tr.Message)
	}
	ttl := expiresInSeconds(tr.ExpiresIn)
	if ttl <= 0 {
		ttl = 7200
	}
	return tr.AccessToken, ttl, nil
}

// API 是 OpenAPI 的最小调用面：取网关地址 + 发 C2C/群消息。
// 只发纯文本（msg_type=0）；markdown/富媒体以后有需要再加。
type API struct {
	apiBase string
	tokens  *TokenSource
	log     *slog.Logger
	client  *http.Client
}

func NewAPI(apiBase string, tokens *TokenSource, log *slog.Logger) *API {
	return &API{
		apiBase: apiBase,
		tokens:  tokens,
		log:     log,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// GatewayURL GET {apiBase}/gateway -> {url}，拿到 wss 接入点。
func (a *API) GatewayURL(ctx context.Context) (string, error) {
	tok, err := a.tokens.Token(ctx)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBase+"/gateway", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "QQBot "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qq gateway: http %d %s", resp.StatusCode, truncate(raw, 200))
	}
	var out struct {
		URL     string `json:"url"`
		ErrCode int    `json:"err_code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("qq gateway decode: %w", err)
	}
	if out.URL == "" {
		return "", fmt.Errorf("qq gateway failed: err_code=%d msg=%s", out.ErrCode, out.Message)
	}
	return out.URL, nil
}

type sendMessageReq struct {
	Content   string       `json:"content,omitempty"`
	MsgType   int          `json:"msg_type"`
	MsgID     string       `json:"msg_id,omitempty"`
	MsgSeq    int          `json:"msg_seq,omitempty"`
	EventID   string       `json:"event_id,omitempty"`
	Markdown  *mdPayload   `json:"markdown,omitempty"`
	Media     *mediaInfo   `json:"media,omitempty"`
}

type mdPayload struct {
	Content string `json:"content"`
}

type mediaInfo struct {
	FileInfo string `json:"file_info"`
}

type sendMessageResp struct {
	ID      string `json:"id"`
	ErrCode int    `json:"err_code"`
	Message string `json:"message"`
}

// SendC2C POST /v2/users/{user_openid}/messages。msgID 为空=主动消息，
// 非空=被动回复（60 分钟内有效、每条最多 4 次）。
func (a *API) SendC2C(ctx context.Context, userOpenID, text, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/users/"+userOpenID+"/messages", sendMessageReq{
		Content: text, MsgType: 0, MsgID: msgID, MsgSeq: msgSeq,
	})
}

// SendGroup POST /v2/groups/{group_openid}/messages。被动 5 分钟内有效、每条 5 次。
func (a *API) SendGroup(ctx context.Context, groupOpenID, text, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/groups/"+groupOpenID+"/messages", sendMessageReq{
		Content: text, MsgType: 0, MsgID: msgID, MsgSeq: msgSeq,
	})
}

// SendC2CMarkdown 发单聊 markdown（msg_type=2，单聊/群聊都无需申请模板）。
func (a *API) SendC2CMarkdown(ctx context.Context, userOpenID, markdown, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/users/"+userOpenID+"/messages", sendMessageReq{
		Markdown: &mdPayload{Content: markdown}, MsgType: 2, MsgID: msgID, MsgSeq: msgSeq,
	})
}

// SendGroupMarkdown 发群 markdown。
func (a *API) SendGroupMarkdown(ctx context.Context, groupOpenID, markdown, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/groups/"+groupOpenID+"/messages", sendMessageReq{
		Markdown: &mdPayload{Content: markdown}, MsgType: 2, MsgID: msgID, MsgSeq: msgSeq,
	})
}

// SendC2CMedia 发单聊富媒体（msg_type=7，fileInfo 来自 UploadC2C*）。
func (a *API) SendC2CMedia(ctx context.Context, userOpenID, fileInfo, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/users/"+userOpenID+"/messages", sendMessageReq{
		Media: &mediaInfo{FileInfo: fileInfo}, MsgType: 7, MsgID: msgID, MsgSeq: msgSeq,
	})
}

// SendGroupMedia 发群富媒体。
func (a *API) SendGroupMedia(ctx context.Context, groupOpenID, fileInfo, msgID string, msgSeq int) error {
	return a.post(ctx, "/v2/groups/"+groupOpenID+"/messages", sendMessageReq{
		Media: &mediaInfo{FileInfo: fileInfo}, MsgType: 7, MsgID: msgID, MsgSeq: msgSeq,
	})
}

func (a *API) post(ctx context.Context, path string, req sendMessageReq) error {	tok, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "QQBot "+tok)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("qq send rate limited: %s", truncate(raw, 200))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qq send: http %d %s", resp.StatusCode, truncate(raw, 200))
	}
	var out sendMessageResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("qq send decode: %w", err)
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("qq send failed: err_code=%d msg=%s", out.ErrCode, out.Message)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
