package main

import (
	"testing"

	"mineagent/internal/protocol"
)

func TestAuthorizeMessage(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		msgType  string
		attached bool
		want     bool
	}{
		{"attached mc chat", protocol.RoleMinecraft, protocol.TypeChatMessage, true, true},
		{"attached mc tool result", protocol.RoleMinecraft, protocol.TypeToolResult, true, true},
		{"attached mc approval result", protocol.RoleMinecraft, protocol.TypeApprovalResult, true, true},
		{"detached mc chat", protocol.RoleMinecraft, protocol.TypeChatMessage, false, false},
		{"qq chat rejected", protocol.RoleQQ, protocol.TypeChatMessage, true, false},
		{"web chat rejected", protocol.RoleWeb, protocol.TypeChatMessage, true, false},
		{"unknown role rejected", "smoke", protocol.TypeChatMessage, true, false},
		{"unknown type rejected", protocol.RoleMinecraft, "hello", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := authorizeMessage(tc.role, tc.msgType, tc.attached); got != tc.want {
				t.Fatalf("authorizeMessage(%q, %q, %v) = %v, want %v", tc.role, tc.msgType, tc.attached, got, tc.want)
			}
		})
	}
}

func TestMatchTrigger(t *testing.T) {
	tests := []struct {
		text    string
		trigger string
		want    string
		ok      bool
	}{
		{"@agent hello", "@agent", "hello", true},
		{"  @Agent 你好世界  ", "@agent", "你好世界", true},
		{"@agent", "@agent", "", true},
		{"hello @agent", "@agent", "", false},
		{"", "@agent", "", false},
		{"@agentx", "@agent", "x", true},
		{"anything", "", "", false},
	}
	for _, tc := range tests {
		got, ok := matchTrigger(tc.text, tc.trigger)
		if ok != tc.ok || got != tc.want {
			t.Errorf("matchTrigger(%q, %q) = (%q, %v), want (%q, %v)", tc.text, tc.trigger, got, ok, tc.want, tc.ok)
		}
	}
}
