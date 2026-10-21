package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestSkill(t *testing.T, root, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name, "references"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+desc+"\n---\nPRIVATE_BODY"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSharedSkillDiscoveryAndScopedRead(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "anatomy", strings.Repeat("description", 80))
	if err := os.WriteFile(filepath.Join(root, "anatomy/references/test.md"), []byte(strings.Repeat("字", 17000)+"END"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog := SharedSkillCatalog(root)
	if !strings.Contains(catalog, "anatomy") || strings.Contains(catalog, "PRIVATE_BODY") || len(catalog) > 4000 {
		t.Fatal("catalog loads bodies or unbounded descriptions")
	}
	tool := SharedSkillTool(root).(*sharedSkillTool)
	for _, tc := range []struct{ args, want string }{
		{`{"name":"anatomy"}`, "PRIVATE_BODY"},
		{`{"name":"anatomy","path":"references/"}`, "test.md"},
		{`{"name":"anatomy","path":"references/test.md","offset":16000}`, "END"},
		{`{"name":"anatomy","path":"../../secret"}`, "error"},
		{`{"name":"anatomy","path":"/etc/passwd"}`, "error"},
		{`{"name":"missing"}`, "error"},
		{`{"name":"anatomy","offset":-1}`, "error"},
	} {
		out, err := tool.InvokableRun(context.Background(), tc.args)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: %s %v", tc.args, out, err)
		}
	}
	// Hot discovery: no process restart or directory watcher is needed.
	writeTestSkill(t, root, "second", "another workflow")
	if !strings.Contains(SharedSkillCatalog(root), "second") {
		t.Fatal("new skill not discovered")
	}
	var response map[string]any
	out, _ := tool.InvokableRun(context.Background(), `{"name":"anatomy","path":"references/test.md"}`)
	json.Unmarshal([]byte(out), &response)
	if response["more"] != true || response["nextOffset"] != float64(16000) {
		t.Fatal("resource paging not bounded")
	}
}

func TestSharedSkillRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "anatomy", "diagram")
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret-value"), 0600)
	os.Symlink(outside, filepath.Join(root, "anatomy/escape"))
	writeTestSkill(t, root, "other", "other workflow")
	os.Symlink("../other/SKILL.md", filepath.Join(root, "anatomy/other"))
	tool := SharedSkillTool(root).(*sharedSkillTool)
	for _, name := range []string{"escape", "other"} {
		out, err := tool.InvokableRun(context.Background(), `{"name":"anatomy","path":"`+name+`"}`)
		if err != nil || !strings.Contains(out, "error") {
			t.Fatal("symlink escaped bundle")
		}
	}
}
