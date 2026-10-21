package body

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestPublicBodyProjectionNeedsNoOperatorAndContainsNoPrivateState(t *testing.T) {
	s, _, _, _ := setup(t)
	s.state.Pos = [3]float64{123456, 64, 987654}
	s.state.WorldID = "private-world"
	for _, tl := range s.PublicTools() {
		info, _ := tl.Info(context.Background())
		if info.Name != "body_profile" {
			t.Fatal(info.Name)
		}
		text, err := tl.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
		if err != nil || strings.Contains(text, "123456") || strings.Contains(text, "private-world") || strings.Contains(text, "inventory") {
			t.Fatalf("public leak: %s %v", text, err)
		}
		if !strings.Contains(text, s.cfg.Name) {
			t.Fatal("body identity missing")
		}
	}
	s.cfg.Enabled = false
	if s.PublicSnapshot()["enabled"] != false || s.PublicSnapshot()["status"] != "disabled" {
		t.Fatal("disabled body misrepresented")
	}
}
