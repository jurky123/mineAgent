package wecom

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

// access_token 管 access_token：
// GET {qyapi}/cgi-bin/gettoken?corpid=..&corpsecret=.. -> {access_token, expires_in}。
// 有效期 7200s，提前 600s 刷新；单飞；失败沿用旧值宽限 300s（与 QQ TokenSource 同构）。
type TokenSource struct {
	corpID  string
	secret  string
	base    string
	log     *slog.Logger
	client  *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
	inflight chan struct{}
}

func NewTokenSource(corpID, secret string, log *slog.Logger) *TokenSource {
	return &TokenSource{
		corpID: corpID, secret: secret,
		base:   "https://qyapi.weixin.qq.com",
		log:    log,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.token != "" && time.Now().Before(t.expires) {
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
				return "", fmt.Errorf("wecom token refresh failed")
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
		if ttl > 700 {
			ttl -= 600
		}
		t.expires = time.Now().Add(time.Duration(ttl) * time.Second)
	} else {
		if t.token != "" && time.Now().Before(t.expires.Add(5*time.Minute)) {
			t.log.Warn("wecom token refresh failed, reusing old token", "err", err)
			tok = t.token
		} else {
			t.log.Error("wecom token refresh failed", "err", err)
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
	url := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s", t.base, t.corpID, t.secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", 0, err
	}
	var out struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("wecom token decode: %w", err)
	}
	if out.AccessToken == "" {
		return "", 0, fmt.Errorf("wecom token failed: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 7200
	}
	return out.AccessToken, out.ExpiresIn, nil
}

// API 是企微 OpenAPI 最小调用面：发应用文本/图片消息。
// 图片需先上传临时素材拿 media_id（3 天有效），见 UploadImage。
type API struct {
	tokens  *TokenSource
	agentID int
	log     *slog.Logger
	client  *http.Client
}

func NewAPI(tokens *TokenSource, agentID int, log *slog.Logger) *API {
	return &API{tokens: tokens, agentID: agentID, log: log, client: &http.Client{Timeout: 15 * time.Second}}
}

type apiErr struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (a *API) postJSON(ctx context.Context, path string, body any, out any) error {
	tok, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://qyapi.weixin.qq.com"+path+"?access_token="+tok, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpResp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("wecom %s: http %d %s", path, httpResp.StatusCode, truncate(data, 200))
	}
	var base apiErr
	if err := json.Unmarshal(data, &base); err != nil {
		return fmt.Errorf("wecom %s decode: %w", path, err)
	}
	if base.ErrCode != 0 {
		return fmt.Errorf("wecom %s failed: errcode=%d errmsg=%s", path, base.ErrCode, base.ErrMsg)
	}
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return nil
}

// SendText 发应用文本消息。toUser=touser（单聊 userid，多人用|隔）；
// chatID 非空时发到应用群聊（需应用创建的群，path=/cgi-bin/appchat/send）。
func (a *API) SendText(ctx context.Context, toUser, chatID, text string) error {
	if chatID != "" {
		return a.postJSON(ctx, "/cgi-bin/appchat/send", map[string]any{
			"chatid": chatID, "msgtype": "text",
			"text": map[string]string{"content": text}, "safe": 0,
		}, &apiErr{})
	}
	return a.postJSON(ctx, "/cgi-bin/message/send", map[string]any{
		"touser": toUser, "agentid": a.agentID, "msgtype": "text",
		"text": map[string]string{"content": text}, "safe": 0,
	}, &apiErr{})
}

// SendImage 发应用图片消息（mediaID 来自 UploadImage，3 天有效）。
func (a *API) SendImage(ctx context.Context, toUser, chatID, mediaID string) error {
	if chatID != "" {
		return a.postJSON(ctx, "/cgi-bin/appchat/send", map[string]any{
			"chatid": chatID, "msgtype": "image",
			"image": map[string]string{"media_id": mediaID}, "safe": 0,
		}, &apiErr{})
	}
	return a.postJSON(ctx, "/cgi-bin/message/send", map[string]any{
		"touser": toUser, "agentid": a.agentID, "msgtype": "image",
		"image": map[string]string{"media_id": mediaID}, "safe": 0,
	}, &apiErr{})
}

// SendMarkdown 发应用 markdown 消息（企微应用消息支持 markdown，同 QQ 口径）。
func (a *API) SendMarkdown(ctx context.Context, toUser, chatID, markdown string) error {
	if chatID != "" {
		return a.postJSON(ctx, "/cgi-bin/appchat/send", map[string]any{
			"chatid": chatID, "msgtype": "markdown",
			"markdown": map[string]string{"content": markdown},
		}, &apiErr{})
	}
	return a.postJSON(ctx, "/cgi-bin/message/send", map[string]any{
		"touser": toUser, "agentid": a.agentID, "msgtype": "markdown",
		"markdown": map[string]string{"content": markdown},
	}, &apiErr{})
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
