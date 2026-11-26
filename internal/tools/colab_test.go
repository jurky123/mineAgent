package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestColabNotebookLifecycle(t *testing.T) {
	w := testWorkspace(t)
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(state, ".config", "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(credentials), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(state, "colab")
	fake := `#!/bin/sh
set -eu
test "$1 $2" = "--auth adc"
shift 2
echo "$*" >> "$HOME/calls"
case "$1" in
 new) test "$4 $5 $6" = "--gpu T4 --high-mem" ;;
 exec) test "$7" = input.ipynb
       printf '%s' '{"cells":[{"outputs":[{"output_type":"error"}]}]}' > input_output.ipynb ;;
 stop) test "$2" = "-s" ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(cli, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	w.cfg.Colab.Binary, w.cfg.Colab.StateDir = cli, state
	if err := w.ensureRoot(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.cfg.Root, "demo.py"), []byte("raise ValueError('test')"), 0600); err != nil {
		t.Fatal(err)
	}
	args := `{"path":"demo.py","gpu":"T4","high_mem":true}`
	tool := w.colabTool()
	if _, err := tool.InvokableRun(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "calls")); !os.IsNotExist(err) {
		t.Fatal("unauthorized caller launched CLI")
	}
	raw, err := tool.InvokableRun(WithQQAdmin(context.Background(), true), args)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK, Released    bool
		Notebook, Error string
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || !got.Released || got.Notebook == "" || !strings.Contains(got.Error, "单元执行失败") {
		t.Fatalf("unexpected result: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(w.cfg.Root, got.Notebook)); err != nil {
		t.Fatal(err)
	}
}
