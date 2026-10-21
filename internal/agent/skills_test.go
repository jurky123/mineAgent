package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mineagent/internal/body"
)

func TestSharedSkillsPreserveBudgetsAndPersonality(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "anatomy"), 0755)
	os.WriteFile(filepath.Join(root, "anatomy/SKILL.md"), []byte("---\nname: anatomy\ndescription: draw diagrams\n---\nBODY_NOT_IN_CATALOG"), 0644)
	a := &Agent{skillsDir: root, sessionKey: "minecraft-main", instruction: "default-personality"}
	for _, tc := range []struct {
		request    Request
		iterations int
	}{
		{Request{SessionKey: "minecraft-main"}, 8},
		{Request{SessionKey: "qq:c2c:U", SystemInstruction: "custom-personality"}, 0},
		{Request{SessionKey: "body:task", BodyPrincipal: &body.Principal{}}, 0},
	} {
		req := a.withSkills(tc.request)
		if len(req.Tools) != 1 || req.MaxIterations != tc.iterations || strings.Contains(req.SystemInstruction, "BODY_NOT_IN_CATALOG") {
			t.Fatalf("changed budget or eagerly loaded skill: %+v", req)
		}
		if tc.request.SystemInstruction != "" && !strings.HasPrefix(req.SystemInstruction, tc.request.SystemInstruction) {
			t.Fatal("custom personality replaced")
		}
	}
}
