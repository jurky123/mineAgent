package prompts

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestUserStylePersistenceScopeAndRollback(t *testing.T) {
	m := testManager(t)
	style := UserStyle{Length: "brief", Language: "en"}
	if err := m.SetUserSettings("alice", "tech", style); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Resolve("minecraft", "alice", "caps"), "你是熟悉 Minecraft") {
		t.Fatal("web persona leaked into Minecraft")
	}
	if !strings.Contains(m.Resolve("web", "alice", "caps"), "默认使用英语") || strings.Contains(m.Resolve("minecraft", "alice", "caps"), "默认使用英语") || strings.Contains(m.Resolve("web", "bob", "caps"), "默认使用英语") {
		t.Fatal("style not web/user scoped")
	}
	reloaded := New(m.path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.UserStyle("alice") != style || reloaded.UserPersona("alice") != "tech" {
		t.Fatal("settings not persisted")
	}
	if err := m.SetUserSettings("alice", "default", UserStyle{Length: "invalid"}); err == nil || m.UserPersona("alice") != "tech" {
		t.Fatal("invalid preference changed settings")
	}
	m.path = t.TempDir() // replacing a directory with the snapshot must fail
	if err := m.SetUserSettings("alice", "teacher", UserStyle{}); err == nil || m.UserStyle("alice") != style || m.UserPersona("alice") != "tech" {
		t.Fatal("write failure changed effective settings")
	}
}
func TestPreviousDefaultsUpgradeAndCustomExamplesSurvive(t *testing.T) {
	m := testManager(t)
	old := previousBuiltinPersonas[1]
	custom := previousBuiltinPersonas[3]
	custom.Example = "管理员自己的例子"
	data, _ := json.Marshal(snapshot{Personas: []Persona{old, custom}})
	if err := os.WriteFile(m.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	got, _ := m.PersonaByID(old.ID)
	if got.Name == old.Name || got.Example == "" {
		t.Fatal("previous default not upgraded")
	}
	got, _ = m.PersonaByID(custom.ID)
	if got.Example != custom.Example {
		t.Fatal("custom example overwritten")
	}
}
