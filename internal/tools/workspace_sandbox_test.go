package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireWorkspaceSandbox(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("Bubblewrap not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, err := newWorkspaceCommand(ctx, t.TempDir(), "true", false)
	if err != nil {
		if strings.Contains(err.Error(), "当前平台") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No permissions to create a new namespace") ||
			strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("host does not allow Bubblewrap namespaces: %s", out)
		}
		t.Fatalf("sandbox startup failed: %s %v", out, err)
	}
}

func TestWorkspaceExecCannotAccessHostFiles(t *testing.T) {
	requireWorkspaceSandbox(t)
	w := testWorkspace(t)
	if err := w.ensureRoot(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-sentinel")
	if err := os.WriteFile(outside, []byte("outside-workspace-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.cfg.Root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"cat " + outside, "echo changed > " + outside, "cat escape", "echo changed > escape"} {
		t.Run(command, func(t *testing.T) {
			out, err := w.exec(context.Background(), map[string]any{"command": command})
			if strings.Contains(out, "outside-workspace-sentinel") {
				t.Fatal("read host file")
			}
			if err == nil {
				var result struct {
					ExitCode int `json:"exitCode"`
				}
				if json.Unmarshal([]byte(out), &result) != nil || result.ExitCode == 0 {
					t.Fatalf("escape succeeded: %s", out)
				}
			}
		})
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "outside-workspace-sentinel" {
		t.Fatalf("host file changed: %q %v", content, err)
	}
	if _, err := w.exec(context.Background(), map[string]any{"command": "echo allowed > inside.txt"}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(w.cfg.Root, "inside.txt")); err != nil || string(content) != "allowed\n" {
		t.Fatalf("workspace write failed: %q %v", content, err)
	}
}

func TestWorkspaceExecFailsClosedWithoutSandbox(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	w := testWorkspace(t)
	_, err := w.exec(context.Background(), map[string]any{"command": "echo unsafe > marker"})
	if err == nil {
		t.Fatal("execution allowed without sandbox")
	}
	if _, err := os.Stat(filepath.Join(w.cfg.Root, "marker")); !os.IsNotExist(err) {
		t.Fatal("unsandboxed command ran")
	}
}

func TestWorkspaceExecOrdinaryCommandsHaveNoHostNetwork(t *testing.T) {
	requireWorkspaceSandbox(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	w := testWorkspace(t)
	port := listener.Addr().(*net.TCPAddr).Port
	command := fmt.Sprintf(`python3 -c 'import socket; socket.create_connection(("127.0.0.1", %d), timeout=0.5)'`, port)
	out, err := w.exec(context.Background(), map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ExitCode int `json:"exitCode"`
	}
	if json.Unmarshal([]byte(out), &result) != nil || result.ExitCode == 0 {
		t.Fatalf("ordinary command reached host network: %s", out)
	}
}

func TestWorkspaceExecBlocksCommandWrappers(t *testing.T) {
	w := testWorkspace(t)
	for _, cmd := range []string{"env rm file", "command rm file", "bash -c 'rm file'", "sh -c 'rm file'"} {
		if _, _, err := w.checkStatic(cmd); err == nil {
			t.Fatalf("wrapper allowed: %s", cmd)
		}
	}
}

func TestBoundedOutputConsumesWithoutGrowing(t *testing.T) {
	out := boundedOutput{limit: 1024}
	chunk := []byte(strings.Repeat("x", 4096))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if n, err := out.Write(chunk); n != len(chunk) || err != nil {
					t.Errorf("write: %d %v", n, err)
				}
			}
		}()
	}
	wg.Wait()
	if out.buf.Len() != 1024 || out.total != 8*100*4096 {
		t.Fatalf("retained=%d total=%d", out.buf.Len(), out.total)
	}
}

func TestWorkspaceExecOutputLimit(t *testing.T) {
	requireWorkspaceSandbox(t)
	w := testWorkspace(t)
	w.cfg.MaxOutputBytes = 5
	out, err := w.exec(context.Background(), map[string]any{"command": `python3 -c 'print("界" * 100000)'`})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exitCode"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	prefix, _, truncated := strings.Cut(result.Output, "\n…")
	if result.ExitCode != 0 || !truncated || len(prefix) > 5 || prefix != "界" {
		t.Fatalf("bad bounded output: %+v", result)
	}
}

func TestWorkspaceExecTimeoutKillsDescendants(t *testing.T) {
	requireWorkspaceSandbox(t)
	w := testWorkspace(t)
	w.cfg.ExecTimeoutSec = 1
	start := time.Now()
	out, err := w.exec(context.Background(), map[string]any{"command": "(sleep 2; echo survived > survivor) & wait"})
	if err == nil && !strings.Contains(out, "超时") {
		t.Fatalf("expected timeout: %s", out)
	}
	if elapsed := time.Since(start); elapsed > 1800*time.Millisecond {
		t.Fatalf("waited for descendant: %s", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(w.cfg.Root, "survivor")); !os.IsNotExist(err) {
		t.Fatal("descendant survived cancellation")
	}
}
