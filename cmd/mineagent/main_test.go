package main

import "testing"

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
