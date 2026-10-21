package config

import (
	"strings"
	"testing"
)

func TestBodyValidationRequiresBothIndependentAuthenticatedRoles(t *testing.T) {
	b := DefaultBody()
	if err := b.Validate("", ":0"); err != nil {
		t.Fatal("disabled body changed existing startup", err)
	}
	b.Enabled = true
	b.Mode = "assist"
	b.Token = strings.Repeat("b", 32)
	for _, tc := range []struct {
		name, token, listen string
		valid               bool
	}{{"empty Paper token", "", "127.0.0.1:8765", false}, {"shared token", b.Token, "127.0.0.1:8765", false}, {"public WS", strings.Repeat("p", 32), "0.0.0.0:8765", false}, {"independent loopback", strings.Repeat("p", 32), "127.0.0.1:8765", true}} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Validate(tc.token, tc.listen)
			if (err == nil) != tc.valid {
				t.Fatalf("validation %v", err)
			}
		})
	}
	b.Zones.Mode = "unrestricted"
	if err := b.Validate(strings.Repeat("p", 32), "127.0.0.1:8765"); err != nil {
		t.Fatal("explicit unrestricted activity rejected", err)
	}
	b.Zones.Mode = "typo"
	if b.Validate(strings.Repeat("p", 32), "127.0.0.1:8765") == nil {
		t.Fatal("unknown zone mode accepted")
	}
	b.Zones.Mode = "allowlist"
	b.WorldMappings = []BodyWorld{{"minecraft:overworld", "yard"}, {"minecraft:overworld", "other"}}
	if b.Validate(strings.Repeat("p", 32), "127.0.0.1:8765") == nil {
		t.Fatal("ambiguous world mapping accepted")
	}
}

func TestBodySupportsUnlimitedAndExplicitRunBudgets(t *testing.T) {
	b := DefaultBody()
	if b.MaxSteps != 12 || b.RunTimeoutSec != 0 || b.MaxConsecutiveReads != 3 || b.ReasoningEffort != "medium" {
		t.Fatal("body UX defaults are not bounded")
	}
	b.Enabled, b.Token = true, strings.Repeat("b", 32)
	for _, budget := range []struct {
		steps, seconds int
		valid          bool
	}{{0, 0, true}, {500, 3600, true}, {-1, 0, false}, {0, -1, false}} {
		b.MaxSteps, b.RunTimeoutSec = budget.steps, budget.seconds
		if err := b.Validate(strings.Repeat("p", 32), "127.0.0.1:8765"); (err == nil) != budget.valid {
			t.Fatal("unexpected budget validation", budget, err)
		}
	}
}
