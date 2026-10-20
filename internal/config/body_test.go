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
	b.WorldMappings = []BodyWorld{{"minecraft:overworld", "yard"}, {"minecraft:overworld", "other"}}
	if b.Validate(strings.Repeat("p", 32), "127.0.0.1:8765") == nil {
		t.Fatal("ambiguous world mapping accepted")
	}
}
