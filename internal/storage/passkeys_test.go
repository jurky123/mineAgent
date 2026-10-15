package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPasskeyRecoveryAtomicAndSingleUse(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	u, _ := s.EnsureUser(ctx, "owner", 1)
	if err = s.CreatePasskeyGrant(ctx, u.ID, []byte("random-user-handle"), "initial", time.Now().Add(time.Hour).UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitPasskeyEnrollment(ctx, "initial", u.ID, []byte("old-key"), []byte(`{}`), []string{"recovery-one", "recovery-two"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PasskeyGrant(ctx, "initial"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatal("activation reused")
	}
	if err = s.CreateAuthSession(ctx, AuthSession{UserID: u.ID, TokenHash: "old-session", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPasskeySession(ctx, "old-session", []byte("old-key")); err != nil {
		t.Fatal(err)
	}
	// An invalid replacement must roll back revocations and preserve the recovery code.
	other, _ := s.EnsureUser(ctx, "other", 1)
	_ = s.CreatePasskeyGrant(ctx, other.ID, []byte("other-handle"), "other-grant", time.Now().Add(time.Hour).UnixMilli(), false)
	if err = s.CommitPasskeyEnrollment(ctx, "other-grant", other.ID, []byte("other-key"), []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitPasskeyEnrollment(ctx, "recovery-one", u.ID, []byte("other-key"), []byte(`{}`), nil); err == nil {
		t.Fatal("duplicate credential accepted")
	}
	if !s.IsPasskeySession(ctx, "old-session") {
		t.Fatal("failed recovery revoked old session")
	}
	if _, err = s.PasskeyGrant(ctx, "recovery-one"); err != nil {
		t.Fatal("failed recovery consumed code")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.CommitPasskeyEnrollment(ctx, "recovery-one", u.ID, []byte("new-key"), []byte(`{}`), []string{"new-recovery"})
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful enrollments=%d", successes)
	}
	if s.IsPasskeySession(ctx, "old-session") {
		t.Fatal("old session survived recovery")
	}
	for _, code := range []string{"recovery-one", "recovery-two"} {
		if _, err = s.PasskeyGrant(ctx, code); !errors.Is(err, ErrInvalidEnrollment) {
			t.Fatal("old recovery code survived")
		}
	}
	if _, err = s.PasskeyOwner(ctx, []byte("old-key")); err == nil {
		t.Fatal("old passkey survived")
	}
	if _, err = s.PasskeyGrant(ctx, "new-recovery"); err != nil {
		t.Fatal("new code missing")
	}
}
func TestExpiredPasskeyGrant(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	u, _ := s.EnsureUser(ctx, "owner", 1)
	if err = s.CreatePasskeyGrant(ctx, u.ID, []byte("handle"), "expired", time.Now().Add(-time.Hour).UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PasskeyGrant(ctx, "expired"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatal("expired grant accepted")
	}
	if err = s.CommitPasskeyEnrollment(ctx, "expired", u.ID, []byte("key"), []byte(`{}`), nil); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatal("expired grant enrolled")
	}
}

func TestCredentialUpdateDoesNotRegressState(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	u, _ := s.EnsureUser(ctx, "owner", 1)
	_ = s.CreatePasskeyGrant(ctx, u.ID, []byte("handle"), "grant", time.Now().Add(time.Hour).UnixMilli(), false)
	if err = s.CommitPasskeyEnrollment(ctx, "grant", u.ID, []byte("key"), []byte(`{"counter":0}`), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdatePasskeyCredential(ctx, []byte("key"), u.ID, []byte(`{"counter":0}`), []byte(`{"counter":2}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdatePasskeyCredential(ctx, []byte("key"), u.ID, []byte(`{"counter":0}`), []byte(`{"counter":1}`)); err == nil {
		t.Fatal("stale assertion overwrote newer credential state")
	}
}
