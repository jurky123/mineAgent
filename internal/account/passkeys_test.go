package account

import (
	"context"
	"io"
	"log/slog"
	"mineagent/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

func TestPasskeyRejectsLegacySessionAndUntrustedOrigins(t *testing.T) {
	s, err := storage.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	svc := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, []string{"owner"})
	u, _ := s.EnsureUser(ctx, "owner", 1)
	_ = s.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: hashToken("legacy"), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	if svc.Current(ctx, "legacy") != nil {
		t.Fatal("legacy session trusted")
	}
	if _, _, ok := svc.CurrentWithSession(ctx, "legacy"); ok {
		t.Fatal("legacy session trusted by alternate path")
	}
	p, err := NewPasskeys(svc, "example.com", []string{"https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		if !p.allowBegin("127.0.0.1:1234") {
			t.Fatal("premature rate limit")
		}
	}
	if p.allowBegin("127.0.0.1:5678") {
		t.Fatal("source-port change bypassed rate limit")
	}
	if !p.allowBegin("127.0.0.2:1234") {
		t.Fatal("independent source blocked")
	}
	for _, origin := range []string{"http://example.com", "https://evil.example", "https://example.com/path", "https://example.com?x=1"} {
		if _, err := NewPasskeys(svc, "example.com", []string{origin}); err == nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	if _, err := NewPasskeys(svc, "example.com", []string{"https://example.com"}); err != nil {
		t.Fatal(err)
	}
}
