package textcrypt

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDailyRotationRetentionDeletionAndRevocation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	v := New(dir)
	id, token, err := v.Issue("test batch")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 10, 18, 15, 59, 59, 0, time.UTC)
	first, err := v.Current(id, token, before)
	if err != nil {
		t.Fatal(err)
	}
	if first.Day != "2026-10-18" || first.ExpiresAt != before.Add(time.Second).UnixMilli() {
		t.Fatal("wrong server timezone boundary")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := v.Current(id, token, before)
			if e != nil || d.Key != first.Key {
				t.Error("concurrent key mismatch")
			}
		}()
	}
	wg.Wait()
	v = New(dir)
	second, err := v.Current(id, token, before.Add(time.Second))
	if err != nil || second.Day != "2026-10-19" || second.Key == first.Key {
		t.Fatal("daily rotation failed")
	}
	list, _ := v.List()
	if len(list) != 1 || len(list[0].Days) != 2 {
		t.Fatal("historical key not retained")
	}
	if err = v.Delete(id, first.Day); err != nil {
		t.Fatal(err)
	}
	list, _ = v.List()
	if len(list[0].Days) != 1 {
		t.Fatal("manual deletion failed")
	}
	if _, err = v.Current(id, token, before); err != ErrDeleted {
		t.Fatal("deleted day's key regenerated")
	}
	if _, err = v.Current(id, token+"invalid", before.Add(time.Second)); err != ErrDenied {
		t.Fatal("invalid token accepted")
	}
	if err = v.Revoke(id); err != nil {
		t.Fatal(err)
	}
	if _, err = v.Current(id, token, before.Add(time.Second)); err != ErrDenied {
		t.Fatal("revoked program fetched key")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if strings.Contains(string(b), token) {
		t.Fatal("plaintext access credential persisted")
	}
	info, _ := os.Stat(filepath.Join(dir, "vault.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("vault file not private")
	}
	info, _ = os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("vault directory not private")
	}
}
