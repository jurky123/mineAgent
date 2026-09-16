package protocol

import (
	"encoding/json"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		data any
	}{
		{"hello", TypeHello, Hello{Protocol: Version, Role: RoleMinecraft, Plugin: "MineAgent", PluginVersion: "0.1.0", ServerVersion: "26.2", Token: "secret"}},
		{"hello_ack", TypeHelloAck, HelloAck{Protocol: Version, Backend: "mineagent", BackendVer: "0.1.0", Time: 123}},
		{"chat.message", TypeChatMessage, ChatMessage{Player: "Steve", UUID: "uuid-1", World: "world", Message: "hi"}},
		{"agent.message", TypeAgentMessage, AgentMessage{Text: "hello", Target: "Steve", ReplyTo: "1"}},
		{"tool.call", TypeToolCall, ToolCall{CallID: "c1", Tool: "minecraft.list_players", Args: json.RawMessage(`{"a":1}`), TimeoutMS: 5000}},
		{"tool.result", TypeToolResult, ToolResult{CallID: "c1", OK: true, Data: json.RawMessage(`{"players":[]}`)}},
		{"tool.result err", TypeToolResult, ToolResult{CallID: "c1", OK: false, Error: "boom"}},
		{"approval.request", TypeApprovalRequest, ApprovalRequest{ApprovalID: "a1", Tool: "minecraft.command", Prompt: "run /tp", TimeoutMS: 60000}},
		{"approval.result", TypeApprovalResult, ApprovalResult{ApprovalID: "a1", Approved: true, Operator: "jzk"}},
		{"ping", TypePing, nil},
		{"pong", TypePong, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Marshal(tc.typ, tc.data)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			env, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if env.V != Version {
				t.Fatalf("version = %d, want %d", env.V, Version)
			}
			if env.Type != tc.typ {
				t.Fatalf("type = %q, want %q", env.Type, tc.typ)
			}
			if tc.data != nil && len(env.Data) == 0 {
				t.Fatal("data missing")
			}
		})
	}
}

func TestDecodeTyped(t *testing.T) {
	b, err := Marshal(TypeChatMessage, ChatMessage{Player: "Steve", Message: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var msg ChatMessage
	if err := env.Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Player != "Steve" || msg.Message != "你好" {
		t.Fatalf("got %+v", msg)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"invalid json", "{"},
		{"wrong version", `{"v":99,"type":"ping"}`},
		{"missing version", `{"type":"ping"}`},
		{"missing type", `{"v":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Unmarshal([]byte(tc.in)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
