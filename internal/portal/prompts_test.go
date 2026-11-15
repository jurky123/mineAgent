package portal

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mineagent/internal/config"
	"mineagent/internal/prompts"
	"mineagent/internal/storage"
	"mineagent/internal/webui"
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

func TestServiceModelGateAndValidation(t *testing.T) {
	m, err := prompts.Init(filepath.Join(t.TempDir(), "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Register(prompts.Service{ID: "qq", DefaultText: func() string { return "rules" }})
	if err := m.SetServiceSettings("qq", prompts.CapabilityOverride{}, "", "saved-model"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Web.DataDir = t.TempDir()
	cfg.Model.BaseURL = ""
	cfg.Model.Name = "fixture-default"
	cfg.Model.Options = []string{"new-model"}
	s := &Server{log: log, web: webui.NewChannel(log, cfg, nil, nil, nil, nil, nil)}
	for _, tc := range []struct {
		admin bool
		model string
		want  int
	}{{false, "injected", 403}, {true, "injected", 400}, {true, "saved-model", 200}, {true, "new-model", 200}, {true, "", 200}} {
		w := httptest.NewRecorder()
		body := `{"scope":"service-settings","id":"qq","enabled":false,"text":"","model":"` + tc.model + `"}`
		s.promptsPost(w, httptest.NewRequest("POST", "/api/prompts", strings.NewReader(body)), &storage.User{Username: "fixture", IsAdmin: tc.admin})
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
	if m.ServiceModel("qq") != "" {
		t.Fatal("reset failed")
	}
}
