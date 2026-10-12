package portal

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mineagent/internal/prompts"
	"mineagent/internal/storage"
)

func TestPromptPreviewAndAdministrativeScopes(t *testing.T) {
	m, err := prompts.Init(filepath.Join(t.TempDir(), "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Register(prompts.Service{ID: "web", Name: "网页", DefaultText: func() string { return "private-capability-text" }})
	if err := m.SetServicePersona("web", "tech"); err != nil {
		t.Fatal(err)
	}
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	u := &storage.User{Username: "reader"}
	r := httptest.NewRecorder()
	s.promptsGet(r, u)
	var got struct {
		WebPersona string            `json:"webPersona"`
		Services   []map[string]any  `json:"services"`
		Personas   []prompts.Persona `json:"personas"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WebPersona != "tech" || len(got.Personas) == 0 || got.Personas[0].Text == "" {
		t.Fatal("missing public persona preview or effective channel default")
	}
	if strings.Contains(r.Body.String(), "private-capability-text") || got.Services[0]["global"] != nil {
		t.Fatal("non-admin received capability details")
	}
	payload := `{"scope":"service-settings","id":"web","persona":"teacher","enabled":true,"text":"new-rules"}`
	r = httptest.NewRecorder()
	s.promptsPost(r, httptest.NewRequest("POST", "/api/prompts", strings.NewReader(payload)), u)
	if r.Code != 403 || m.ServicePersona("web") != "tech" {
		t.Fatal("non-admin changed channel settings")
	}
	u.IsAdmin = true
	r = httptest.NewRecorder()
	s.promptsPost(r, httptest.NewRequest("POST", "/api/prompts", strings.NewReader(payload)), u)
	if r.Code != 200 || m.ServicePersona("web") != "teacher" || m.Global("web").Text != "new-rules" {
		t.Fatalf("admin save failed: %s", r.Body.String())
	}
	r = httptest.NewRecorder()
	s.promptsPost(r, httptest.NewRequest("POST", "/api/prompts", strings.NewReader(`{"scope":"service-settings","id":"web"}`)), u)
	if r.Code != 400 || m.Global("web").Text != "new-rules" {
		t.Fatal("incomplete request changed settings")
	}
	u.IsAdmin = false
	r = httptest.NewRecorder()
	s.promptsPost(r, httptest.NewRequest("POST", "/api/prompts", strings.NewReader(`{"scope":"user-persona","persona":"translator"}`)), u)
	if r.Code != 200 || m.UserPersona("reader") != "translator" || m.ServicePersona("web") != "teacher" {
		t.Fatal("personal selection changed global default")
	}
}
