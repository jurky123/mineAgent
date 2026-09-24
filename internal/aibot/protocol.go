package aibot

import "encoding/json"

// 协议出处：企业微信开发者中心「智能机器人长连接」
// developer.work.weixin.qq.com/document/path/101463
//
// 要点：
//   - WS 地址 wss://openws.work.weixin.qq.com，连上后首帧发 aibot_subscribe
//     （body.bot_id + body.secret），收到 errcode=0 才算订阅成功。
//   - 每个机器人同时只允许一个有效长连接：新连接会踢掉旧连接，
//     旧连接收到 disconnected_event 事件并被断开。
//   - 保活：每 30s 发一次 {"cmd":"ping"}，长时间无心跳服务端主动断开。
//   - 收消息：cmd=aibot_msg_callback（body 带 msgid/aibotid/chatid?/chattype/from.userid/msgtype/text.content）。
//     事件：cmd=aibot_event_callback（body.event.eventtype，如 enter_chat）。
//   - 回消息：aibot_respond_msg，headers.req_id 必须透传触发回调的 req_id；
//     流式用 body.stream{id, finish, content}；普通用 msgtype=markdown 等。
//   - 主动推送：aibot_send_msg，body.chatid（单聊=userid/群聊=chatid）+chat_type(1/2)。
//     前置条件：该会话中用户先给机器人发过消息。
//   - 统一响应：{headers:{req_id}, errcode, errmsg}（成功 0/"ok"）。
//   - 回复窗口：收到回调后 24 小时内可回复；回复+主动推送合计 30 条/分钟、1000 条/小时（按会话）。

const (
	// wsURL 长连接地址（固定）。
	wsURL = "wss://openws.work.weixin.qq.com"

	cmdSubscribe      = "aibot_subscribe"
	cmdMsgCallback    = "aibot_msg_callback"
	cmdEventCallback  = "aibot_event_callback"
	cmdRespondMsg     = "aibot_respond_msg"
	cmdRespondWelcome = "aibot_respond_welcome_msg"
	cmdSendMsg        = "aibot_send_msg"
	cmdPing           = "ping"

	// chat_type：1=单聊（chatid 是 userid），2=群聊（chatid 是群 id）。
	chatTypeSingle = 1
	chatTypeGroup  = 2

	// 进会话事件：可回欢迎语（5 秒内）。
	eventEnterChat     = "enter_chat"
	eventDisconnected  = "disconnected_event"
)

// envelope 是上下行统一信封：cmd + headers.req_id + body；
// 服务端对我们请求的应答额外带 errcode/errmsg。
type envelope struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers headers         `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode int             `json:"errcode"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

type headers struct {
	ReqID string `json:"req_id"`
}

// msgCallback 是 aibot_msg_callback 的 body。
type msgCallback struct {
	MsgID    string `json:"msgid"`
	AIBotID  string `json:"aibotid"`
	ChatID   string `json:"chatid,omitempty"`
	ChatType string `json:"chattype"` // single / group
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string `json:"msgtype"`
	Text    *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
}

// eventCallback 是 aibot_event_callback 的 body。
type eventCallback struct {
	MsgID      string `json:"msgid"`
	CreateTime int64  `json:"create_time"`
	AIBotID    string `json:"aibotid"`
	ChatID     string `json:"chatid,omitempty"`
	ChatType   string `json:"chattype,omitempty"`
	From       *struct {
		UserID string `json:"userid"`
	} `json:"from,omitempty"`
	MsgType string `json:"msgtype"`
	Event   struct {
		EventType string `json:"eventtype"`
	} `json:"event"`
}

// InboundMessage 归一化后的入站消息，channel.go 消费。
type InboundMessage struct {
	// ReqID：回调的 req_id，回复时必须透传（被动回复窗口 24h）。
	ReqID string
	// MsgID：平台消息 id，去重用。
	MsgID string
	// Kind："c2c"（chattype=single）或 "group"。
	Kind string
	// UserID：发送者 userid（机器人创建者是超管时是明文）。
	UserID string
	// ChatID：群聊会话 id（仅群聊有）。
	ChatID string
	// Text：文本内容（非文本消息返回 ""，v1 跳过）。
	Text string
}

// markdownBody 组装 markdown 消息体（回复与主动推送共用）。
func markdownBody(content string) map[string]any {
	return map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": content},
	}
}

// respondEnvelope 组装被动回复：headers.req_id 透传回调 req_id。
func respondEnvelope(reqID, content string) map[string]any {
	return map[string]any{
		"cmd":     cmdRespondMsg,
		"headers": headers{ReqID: reqID},
		"body":    markdownBody(content),
	}
}

// sendEnvelope 组装主动推送：chatid + chat_type 显式指定。
func sendEnvelope(chatID string, chatType uint32, content string) map[string]any {
	body := markdownBody(content)
	body["chatid"] = chatID
	body["chat_type"] = chatType
	return map[string]any{
		"cmd":     cmdSendMsg,
		"headers": headers{ReqID: newReqID()},
		"body":    body,
	}
}
