package qq

import "encoding/json"

// 按官方文档 api-v2 的事件结构建模，只取我们用的子集：
// C2C_MESSAGE_CREATE（单聊）、GROUP_AT_MESSAGE_CREATE（群@机器人）。
// 传输 payload 通用结构见 payload.html：{id, op, d, s, t}。
// 收发地址与字段见：
//   - websocket.html（op 0/1/2/6/7/9/10/11，heartbeat/identify/resume）
//   - autogen/event/c2c_message_create.html
//   - autogen/event/group_at_message_create.html

const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
)

// GROUP_AND_C2C_EVENT = 1<<25，只订阅单聊+群@，不要全量群消息（需要额外权限且更吵）。
const intentGroupAndC2C = 1 << 25

type wsPayload struct {
	ID string `json:"id"`
	Op int    `json:"op"`
	D  any    `json:"d"`
	S  *int64 `json:"s"`
	T  string `json:"t"`
}

type helloData struct {
	HeartbeatInterval int64 `json:"heartbeat_interval"`
}

type identifyData struct {
	Token      string `json:"token"`
	Intents    int    `json:"intents"`
	Shard      [2]int `json:"shard"`
	Properties struct {
		OS      string `json:"$os"`
		Browser string `json:"$browser"`
		Device  string `json:"$device"`
	} `json:"properties"`
}

type resumeData struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

type readyData struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"user"`
	Shard [2]int `json:"shard"`
}

type qqUser struct {
	IsYou        bool   `json:"is_you"`
	ID           string `json:"id"`
	Username     string `json:"username"`
	Bot          bool   `json:"bot"`
	UnionOpenID  string `json:"union_openid"`
	UserOpenID   string `json:"user_openid"`
	MemberOpenID string `json:"member_openid"`
	MemberRole   string `json:"member_role"`
}

// C2C 单聊事件 d（c2c_message_create.html）。
type c2cEvent struct {
	messageContent
	ID        string `json:"id"`
	Author    qqUser `json:"author"`
	Timestamp string `json:"timestamp"`
}

type scene struct {
	Source string   `json:"source"`
	Ext    []string `json:"ext"`
}

// 群@机器人事件 d（group_at_message_create.html，content 已去@前缀）。
type groupAtEvent struct {
	messageContent
	ID          string `json:"id"`
	Author      qqUser `json:"author"`
	GroupOpenID string `json:"group_openid"`
	Timestamp   string `json:"timestamp"`
}

// QQ type 103 carries quoted content in msg_elements. Nested elements can
// contain attachments too; a reference ID alone does not grant history access.
type messageContent struct {
	Content      string            `json:"content"`
	MessageType  int               `json:"message_type"`
	MessageScene *scene            `json:"message_scene"`
	MsgIdx       string            `json:"msg_idx"`
	RefMsgIdx    string            `json:"ref_msg_idx"`
	Attachments  []Attachment      `json:"attachments"`
	Elements     []messageContent  `json:"msg_elements"`
	Reference    *messageReference `json:"message_reference"`
	Ark          json.RawMessage   `json:"ark"`
	ArkData      json.RawMessage   `json:"ark_data"`
	Author       *qqUser           `json:"author"`
	Timestamp    string            `json:"timestamp"`
	Mentions     []qqUser          `json:"mentions"`
}

type messageReference struct {
	MessageID string `json:"message_id"`
}

type Attachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
}

// InboundMessage 是网关事件归一化后的内部消息，channel.go 消费它。
type InboundMessage struct {
	// Kind: "c2c", "group_at"（含全量事件中明确@自己）, "group"。
	Kind string
	// MsgID: 平台消息 id，被动回复用（C2C 60分钟/群 5分钟内有效）。
	MsgID string
	// EventID: 最外层事件 id，被动回复 msg_id/event_id 二选一，优先 msg_id。
	EventID string
	// UserOpenID: C2C 回复目标。
	UserOpenID string
	// GroupOpenID: 群回复目标。
	GroupOpenID string
	// MemberOpenID: 群内发言人。
	MemberOpenID string
	// UnionOpenID: 跨应用身份（可能为空），绑定 MC 身份时优先用它。
	UnionOpenID string
	// Username: 昵称（C2C 可能为空）。
	Username string
	// Text is the current sender's caption, separate from quoted material.
	Text    string
	content messageContent
	// MemberRole: 群内角色 member/admin/owner。
	MemberRole string
	// HasOtherMention prevents unsolicited replies to messages addressed to others.
	HasOtherMention bool
}
