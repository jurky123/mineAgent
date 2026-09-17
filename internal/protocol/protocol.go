package protocol

import (
	"encoding/json"
	"errors"
	"time"
)

const Version = 1

const (
	RoleMinecraft = "minecraft"
	RoleQQ        = "qq"
	RoleWeb       = "web"
)

const (
	TypeHello          = "hello"
	TypeChatMessage    = "chat.message"
	TypeToolResult     = "tool.result"
	TypeApprovalResult = "approval.result"
	TypePong           = "pong"

	TypeHelloAck        = "hello_ack"
	TypeAgentMessage    = "agent.message"
	TypeToolCall        = "tool.call"
	TypeApprovalRequest = "approval.request"
	TypePing            = "ping"
)

type Envelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	TS   int64           `json:"ts,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type Hello struct {
	Protocol      int    `json:"protocol"`
	Role          string `json:"role"`
	Plugin        string `json:"plugin,omitempty"`
	PluginVersion string `json:"pluginVersion,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	Token         string `json:"token,omitempty"`
}

type HelloAck struct {
	Protocol   int    `json:"protocol"`
	Backend    string `json:"backend"`
	BackendVer string `json:"backendVersion"`
	Time       int64  `json:"time"`
}

type ChatMessage struct {
	Player  string `json:"player"`
	UUID    string `json:"uuid,omitempty"`
	World   string `json:"world,omitempty"`
	Message string `json:"message"`
}

type AgentMessage struct {
	Text    string `json:"text"`
	Target  string `json:"target,omitempty"`
	ReplyTo string `json:"replyTo,omitempty"`
}

type ToolCall struct {
	CallID        string          `json:"callId"`
	Tool          string          `json:"tool"`
	Args          json.RawMessage `json:"args,omitempty"`
	Requester     string          `json:"requester,omitempty"`
	RequesterUUID string          `json:"requesterUuid,omitempty"`
	TimeoutMS     int64           `json:"timeoutMs,omitempty"`
}

type ToolResult struct {
	CallID string          `json:"callId"`
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type ApprovalRequest struct {
	ApprovalID string          `json:"approvalId"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args,omitempty"`
	Requester  string          `json:"requester,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	TimeoutMS  int64           `json:"timeoutMs,omitempty"`
}

type ApprovalResult struct {
	ApprovalID string `json:"approvalId"`
	Approved   bool   `json:"approved"`
	Operator   string `json:"operator,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func New(typ string, data any) (*Envelope, error) {
	env := &Envelope{V: Version, Type: typ, TS: time.Now().UnixMilli()}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		env.Data = raw
	}
	return env, nil
}

func Marshal(typ string, data any) ([]byte, error) {
	env, err := New(typ, data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

func Unmarshal(b []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	if env.V != Version {
		return nil, errors.New("protocol: unsupported version")
	}
	if env.Type == "" {
		return nil, errors.New("protocol: missing type")
	}
	return &env, nil
}

func (e *Envelope) Decode(dst any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, dst)
}
