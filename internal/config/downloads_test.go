package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadPublicOrigin(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		valid       bool
	}{
		{"", "", true},
		{"https://download.example.workers.dev", "https://download.example.workers.dev", true},
		{"https://download.example.workers.dev/", "https://download.example.workers.dev", true},
		{"http://download.example.workers.dev", "", false},
		{"https:///download", "", false},
		{"https://user:pass@download.example.workers.dev", "", false},
		{"https://download.example.workers.dev/download", "", false},
		{"https://download.example.workers.dev?target=other", "", false},
		{"https://download.example.workers.dev?", "", false},
		{"https://download.example.workers.dev#secret", "", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data, _ := json.Marshal(map[string]any{"web": map[string]string{"downloadPublicOrigin": tc.input}})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
			if tc.valid && cfg.Web.DownloadPublicOrigin != tc.want {
				t.Fatal("unexpected public origin")
			}
		})
	}
}
