package wecom

// 归一化后的内部消息，channel.go 消费。字段语义与 QQ InboundMessage 对齐：
// Kind: "c2c"（应用单聊）或 "group"（应用群聊，需应用创建的群才有回调）。
// MsgID: 平台 msgid，被动回复用（5 秒内回包；超时走主动 SendText）。
// UserID: 单聊发送者 userid（企业内明文）；GroupChatID: 群聊 chatid。
// SenderName: 昵称（回调只带 userid，昵称留空，channel 用 userid 展示）。
// Text: 纯文本内容（只要 text 类型；image/voice/video/file 事件 v1 跳过，
// 日志记一条 debug，不进 agent）。
type InboundMessage struct {
	Kind        string
	MsgID       string
	UserID      string
	GroupChatID string
	SenderName  string
	Text        string
}

// 回调明文 XML 结构（自建应用消息回调，document/path/90930）：
// <xml><ToUserName>corpId</ToUserName><FromUserName>userid</FromUserName>
// <CreateTime>..<MsgType>text</MsgType><Content>..</Content>
// <MsgId>..</MsgId><AgentID>..</AgentID></xml>
// 群聊时多 <ChatId>。
type callbackText struct {
	ToUserName   string `xml:"ToUserName"`
	FromUserName string `xml:"FromUserName"`
	CreateTime   int64  `xml:"CreateTime"`
	MsgType      string `xml:"MsgType"`
	Content      string `xml:"Content"`
	MsgID        string `xml:"MsgId"`
	AgentID      string `xml:"AgentID"`
	ChatID       string `xml:"ChatId"`
}
