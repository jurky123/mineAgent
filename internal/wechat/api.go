package wechat

// 个人微信 ClawBot 协议（直连腾讯 iLink，不跑 OpenClaw）。
//
// 协议出处：官方插件 @tencent-weixin/openclaw-weixin（MIT）的 HTTP JSON 接口。
// 端点（base 默认 https://ilinkai.weixin.qq.com）：
//   - POST ilink/bot/get_bot_qrcode?bot_type=3   -> {qrcode, qrcode_img_content}
//   - GET  ilink/bot/get_qrcode_status?qrcode=.. -> {status, bot_token, ilink_bot_id, baseurl, ilink_user_id, redirect_host}
//   - POST ilink/bot/getupdates                  -> {ret, errcode, msgs, get_updates_buf}（35s 长轮询）
//   - POST ilink/bot/sendmessage                 -> {message_id, ret, errmsg}
//   - POST ilink/bot/getconfig / sendtyping / msg/notifystart / msg/notifystop
//
// 请求头（每次都要）：
//   Content-Type: application/json
//   AuthorizationType: ilink_bot_token
//   Authorization: Bearer <bot_token>（登录后）
//   X-WECHAT-UIN: base64(十进制随机 uint32)
//   iLink-App-Id: bot
//   iLink-App-ClientVersion: <uint32 版本编码>
//
// 所有请求 body 里带 base_info:{channel_version, bot_agent}。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultBaseURL 是 QR 登录固定入口；登录成功后服务端可能下发 baseurl（IDC 重定向）。
	defaultBaseURL = "https://ilinkai.weixin.qq.com"
	// botType 与本插件 build 对应的 bot_type（官方插件用 "3"）。
	botType = "3"
	// appID 对应官方插件 package.json 的 ilink_appid 字段。
	appID = "bot"
	// channelVersion 参与 iLink-App-ClientVersion 与 base_info.channel_version。
	channelVersion = "0.1.0"

	longPollTimeout = 35 * time.Second
	apiTimeout      = 15 * time.Second

	// message_type：1=用户 2=机器人。
	MsgTypeUser = 1
	MsgTypeBot  = 2
	// message_state：2=FINISH。
	MsgStateFinish = 2
	// item 类型：1=文本。
	ItemTypeText = 1
)

// clientVersion 把 "0.1.0" 编码成 uint32（major<<16 | minor<<8 | patch）。
func clientVersion() uint32 {
	parts := strings.Split(channelVersion, ".")
	var nums [3]uint32
	for i := 0; i < len(parts) && i < 3; i++ {
		_, _ = fmt.Sscanf(parts[i], "%d", &nums[i])
	}
	return (nums[0]&0xff)<<16 | (nums[1]&0xff)<<8 | (nums[2] & 0xff)
}

// rndUIN 生成 X-WECHAT-UIN：随机 uint32 的十进制字符串再 base64。
func rndUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", binary.BigEndian.Uint32(b[:]))))
}

type baseInfo struct {
	ChannelVersion string `json:"channel_version"`
	BotAgent       string `json:"bot_agent"`
}

// Client 是 iLink HTTP 客户端（登录 + 收发消息共用）。
type Client struct {
	base  string
	token string
	agent string
	http  *http.Client
}

func NewClient(base, token, botAgent string) *Client {
	if base == "" {
		base = defaultBaseURL
	}
	if botAgent == "" {
		botAgent = "MineAgent/" + channelVersion + " (clawbot-compat)"
	}
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		agent: botAgent,
		// 不用 Client.Timeout：所有请求都走 per-request context 超时，
		// 这样长轮询超时是干净的 context.DeadlineExceeded（好区分正常超时与真错误）。
		http: &http.Client{},
	}
}

func (c *Client) headers() map[string]string {
	h := map[string]string{
		"Content-Type":            "application/json",
		"AuthorizationType":       "ilink_bot_token",
		"X-WECHAT-UIN":            rndUIN(),
		"iLink-App-Id":            appID,
		"iLink-App-ClientVersion": fmt.Sprintf("%d", clientVersion()),
	}
	if c.token != "" {
		h["Authorization"] = "Bearer " + c.token
	}
	return h
}

func (c *Client) baseInfo() baseInfo {
	return baseInfo{ChannelVersion: channelVersion, BotAgent: c.agent}
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any, timeout time.Duration) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = apiTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.base+"/"+strings.TrimLeft(path, "/"), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wechat %s: http %d %s", path, resp.StatusCode, truncate(data, 200))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = apiTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.base+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wechat %s: http %d %s", path, resp.StatusCode, truncate(data, 200))
	}
	return json.Unmarshal(data, out)
}

// ---------------------------------------------------------------------------
// 登录
// ---------------------------------------------------------------------------

type QRCodeResponse struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"` // 二维码内容（URL）
}

type QRStatusResponse struct {
	Status       string `json:"status"` // wait|scaned|confirmed|expired|scaned_but_redirect|need_verifycode|verify_code_blocked|binded_redirect
	BotToken     string `json:"bot_token"`
	ILinkBotID   string `json:"ilink_bot_id"`
	BaseURL      string `json:"baseurl"`
	ILinkUserID  string `json:"ilink_user_id"`
	RedirectHost string `json:"redirect_host"`
}

// FetchQRCode 获取登录二维码。
func (c *Client) FetchQRCode(ctx context.Context, localTokens []string) (QRCodeResponse, error) {
	var out QRCodeResponse
	if localTokens == nil {
		localTokens = []string{}
	}
	err := c.postJSON(ctx, "ilink/bot/get_bot_qrcode?bot_type="+botType,
		map[string]any{"local_token_list": localTokens}, &out, apiTimeout)
	return out, err
}

// PollQRStatus 长轮询扫码状态（35s；客户端超时返回 status=wait 由调用方重试）。
func (c *Client) PollQRStatus(ctx context.Context, qrcode, verifyCode string) (QRStatusResponse, error) {
	path := "ilink/bot/get_qrcode_status?qrcode=" + urlEncode(qrcode)
	if verifyCode != "" {
		path += "&verify_code=" + urlEncode(verifyCode)
	}
	var out QRStatusResponse
	err := c.getJSON(ctx, path, &out, longPollTimeout+5*time.Second)
	if err != nil {
		// 长轮询超时/ctx 取消是正常控制流；真实错误（HTTP 4xx/5xx）返回给上层。
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return QRStatusResponse{Status: "wait"}, nil
		}
		return out, err
	}
	return out, nil
}

// SetBase 登录成功后按服务端下发的 baseurl 切 IDC。
func (c *Client) SetBase(base string) {
	if base == "" {
		return
	}
	c.base = strings.TrimRight(base, "/")
}

func (c *Client) SetToken(tok string) { c.token = tok }
func (c *Client) Base() string        { return c.base }

// ---------------------------------------------------------------------------
// 消息收发
// ---------------------------------------------------------------------------

type MessageItem struct {
	Type     int `json:"type,omitempty"`
	TextItem *struct {
		Text string `json:"text,omitempty"`
	} `json:"text_item,omitempty"`
}

type WeixinMessage struct {
	Seq          int           `json:"seq,omitempty"`
	MessageID    string        `json:"message_id,omitempty"`
	FromUserID   string        `json:"from_user_id,omitempty"`
	ToUserID     string        `json:"to_user_id,omitempty"`
	ClientID     string        `json:"client_id,omitempty"`
	CreateTime   int64         `json:"create_time_ms,omitempty"`
	SessionID    string        `json:"session_id,omitempty"`
	GroupID      string        `json:"group_id,omitempty"`
	MessageType  int           `json:"message_type,omitempty"`
	MessageState int           `json:"message_state,omitempty"`
	ItemList     []MessageItem `json:"item_list,omitempty"`
	ContextToken string        `json:"context_token,omitempty"`
	RunID        string        `json:"run_id,omitempty"`
}

type GetUpdatesResp struct {
	Ret                  int             `json:"ret"`
	ErrCode              int             `json:"errcode"`
	ErrMsg               string          `json:"errmsg"`
	Msgs                 []WeixinMessage `json:"msgs"`
	GetUpdatesBuf        string          `json:"get_updates_buf"`
	LongPollingTimeoutMS int64           `json:"longpolling_timeout_ms"`
}

// GetUpdates 长轮询收消息；客户端超时（无新消息）返回空 resp 供上层直接重试。
func (c *Client) GetUpdates(ctx context.Context, buf string) (GetUpdatesResp, error) {
	var out GetUpdatesResp
	err := c.postJSON(ctx, "ilink/bot/getupdates",
		map[string]any{"get_updates_buf": buf, "base_info": c.baseInfo()},
		&out, longPollTimeout+10*time.Second)
	if err != nil {
		// 长轮询超时（无新消息）是正常控制流：返回空结果让上层直接重试；
		// 其它错误（HTTP 4xx/5xx 等）上抛，由 monitor 退避重试。
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return GetUpdatesResp{GetUpdatesBuf: buf}, nil
		}
		return out, err
	}
	return out, nil
}

type SendMessageResp struct {
	MessageID string `json:"message_id"`
	Ret       int    `json:"ret"`
	ErrMsg    string `json:"errmsg"`
}

// SendText 发文本消息：to=对方 userid，contextToken 透传收到的 context_token。
func (c *Client) SendText(ctx context.Context, to, text, contextToken string) error {
	msg := map[string]any{
		"from_user_id":  "",
		"to_user_id":    to,
		"client_id":     newClientID(),
		"message_type":  MsgTypeBot,
		"message_state": MsgStateFinish,
		"item_list":     []any{map[string]any{"type": ItemTypeText, "text_item": map[string]string{"text": text}}},
	}
	if contextToken != "" {
		msg["context_token"] = contextToken
	}
	var out SendMessageResp
	if err := c.postJSON(ctx, "ilink/bot/sendmessage",
		map[string]any{"msg": msg, "base_info": c.baseInfo()}, &out, apiTimeout); err != nil {
		return err
	}
	if out.Ret != 0 {
		return fmt.Errorf("wechat send failed: ret=%d errmsg=%s", out.Ret, out.ErrMsg)
	}
	return nil
}

// NotifyStart / NotifyStop：告知腾讯本通道上线/下线。
func (c *Client) NotifyStart(ctx context.Context) error {
	return c.postJSON(ctx, "ilink/bot/msg/notifystart",
		map[string]any{"base_info": c.baseInfo()}, nil, apiTimeout)
}

func (c *Client) NotifyStop(ctx context.Context) error {
	return c.postJSON(ctx, "ilink/bot/msg/notifystop",
		map[string]any{"base_info": c.baseInfo()}, nil, apiTimeout)
}

func newClientID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("mineagent-%d-%x", time.Now().UnixMilli(), b)
}

func urlEncode(s string) string {
	// qrcode/verify_code 都是 base64-ish 的 token，做最小转义即可。
	r := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D", "&", "%26", "?", "%3F", " ", "%20")
	return r.Replace(s)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
