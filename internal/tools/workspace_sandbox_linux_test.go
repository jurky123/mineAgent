//go:build linux

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Also run this test under a temporary service with AmbientCapabilities=
// CAP_NET_BIND_SERVICE, matching production. The parent must retain that
// capability while sandbox children have none.
func TestWorkspaceSandboxDropsInheritedCapabilities(t *testing.T) {
	requireWorkspaceSandbox(t)
	before, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, err := newWorkspaceCommand(ctx, t.TempDir(), `python3 -c 'from pathlib import Path; print("\n".join(s for s in Path("/proc/self/status").read_text().splitlines() if s.startswith(("CapInh:", "CapPrm:", "CapEff:", "CapAmb:", "NoNewPrivs:"))))'`, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox startup: %s %v", out, err)
	}
	for _, name := range []string{"CapInh:", "CapPrm:", "CapEff:", "CapAmb:"} {
		if !strings.Contains(string(out), name+"\t0000000000000000") {
			t.Fatalf("child retained capabilities: %s", out)
		}
	}
	if !strings.Contains(string(out), "NoNewPrivs:\t1") {
		t.Fatalf("child can gain privileges: %s", out)
	}
	after, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(before), "\n") {
		if (strings.HasPrefix(line, "Cap") || strings.HasPrefix(line, "NoNewPrivs:")) && !strings.Contains(string(after), line+"\n") {
			t.Fatalf("parent privileges changed: %s", line)
		}
	}
}

func TestWorkspaceExecFailsClosedWithoutPrivilegeDropper(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("Bubblewrap not installed")
	}
	dir := t.TempDir()
	if err := os.Symlink(bwrap, filepath.Join(dir, "bwrap")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	w := testWorkspace(t)
	if _, err := w.exec(context.Background(), map[string]any{"command": "echo unsafe > marker"}); err == nil || !strings.Contains(err.Error(), "setpriv") {
		t.Fatalf("missing privilege dropper allowed execution: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.cfg.Root, "marker")); !os.IsNotExist(err) {
		t.Fatal("unsandboxed command ran")
	}
}
