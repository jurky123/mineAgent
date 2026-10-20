package webui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/storage"
)

func TestFreePricing(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		pricing  map[string]string
		want     bool
	}{
		{"suffix", "preview-free", nil, true},
		{"router", "openrouter/free", nil, true},
		{"contributor is paid", "muse-contributor", nil, false},
		{"embedded free is not proof", "free-model-paid", nil, false},
		{"decimal zero", "model", map[string]string{"prompt": "0.000", "completion": "0e0"}, true},
		{"pricing overrides name", "preview-free", map[string]string{"prompt": "0", "completion": "0.01"}, false},
		{"missing price", "preview-free", map[string]string{"prompt": "0"}, false},
		{"request fee", "model", map[string]string{"prompt": "0", "completion": "0", "request": "0.01"}, false},
		{"cache fee", "model", map[string]string{"prompt": "0", "completion": "0", "input_cache_write": "0.01"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := freePricing(tc.pricing, tc.id); got != tc.want {
				t.Fatalf("free=%v want %v", got, tc.want)
			}
		})
	}
}

func TestModelMetadataAndHTTPFailure(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected generation request")
			w.WriteHeader(500)
			return
		}
		if fail.Load() {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"data":[{"id":"fake"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[
		{"id":"new","name":"Newest","created":200,"context_length":32768,"pricing":{"prompt":"0.0","completion":"0"},"supported_parameters":["tools"]},
		{"id":"new"},{"id":""},
		{"id":"image","architecture":{"output_modalities":["image"]}},
		{"id":"no-tools","supported_parameters":["temperature"]},
		{"id":"expired","expiration_date":"2020-01-01"}
		]}`)
	}))
	defer srv.Close()
	opts, err := fetchModelOptions(context.Background(), srv.URL, "")
	if err != nil || len(opts) != 4 {
		t.Fatalf("options %v error %v", opts, err)
	}
	if !opts[0].Free || opts[0].Created != 200 || opts[0].ContextLength != 32768 || opts[0].Label != "Newest" {
		t.Fatalf("lost metadata: %+v", opts[0])
	}
	for _, m := range opts[1:] {
		if !m.Unavailable || m.UnavailableReason == "" {
			t.Errorf("not excluded: %+v", m)
		}
	}
	fail.Store(true)
	if _, err := fetchModelOptions(context.Background(), srv.URL, ""); err == nil {
		t.Fatal("accepted error response as catalog")
	}
}

func TestCatalogStaleFallbackAndSorting(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"old","created":100},{"id":"new","created":200},{"id":"unknown-free"}]}`)
	}))
	defer srv.Close()
	l := newModelLister(srv.URL, "", t.TempDir(), nil)
	l.probedAt = time.Now() // No generation requests in discovery test.
	c := &Channel{models: l, model: "old", modelBaseURL: srv.URL}
	opts := c.modelCatalog(context.Background())
	if len(opts) != 3 || opts[0].ID != "new" || opts[2].ID != "unknown-free" {
		t.Fatalf("wrong order: %+v", opts)
	}
	fail.Store(true)
	l.mu.Lock()
	l.at = time.Now().Add(-time.Hour)
	l.mu.Unlock()
	opts = c.modelCatalog(context.Background())
	if len(opts) != 3 || !opts[0].Stale || opts[0].ID != "new" {
		t.Fatalf("lost cached options: %+v", opts)
	}
}

func TestProviderFreeOnlyAppliesToConfiguredModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"normal","name":"Free by pricing","pricing":{"prompt":"0","completion":"0"}},{"id":"paid-free","pricing":{"prompt":"0","completion":"0.1"}}]}`)
	}))
	defer srv.Close()
	c := &Channel{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p := config.Provider{ID: "extra", BaseURL: srv.URL, APIKey: "test", FreeOnly: true, Models: []string{"normal", "paid-free", "muse-contributor"}}
	opts := c.providerOptions(context.Background(), p)
	if len(opts) != 1 || opts[0].ID != "extra/normal" || opts[0].Label != "Free by pricing" || !opts[0].Free {
		t.Fatalf("wrong free options: %+v", opts)
	}
}

func waitForProbe(t *testing.T, l *modelLister) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		running := l.probing
		l.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("probe did not finish")
}

func TestProbeRetriesUnavailableAndPersistsTimestamp(t *testing.T) {
	var healthy atomic.Bool
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"1"}}]}`)
	}))
	defer srv.Close()
	l := newModelLister(srv.URL, "test", t.TempDir(), nil)
	l.probeAsync([]string{"recovered"})
	waitForProbe(t, l)
	l.mu.Lock()
	bad := l.unavailable["recovered"]
	first := l.probedAt
	l.mu.Unlock()
	if !bad {
		t.Fatal("503 not marked unavailable")
	}
	b, err := os.ReadFile(l.cachePath())
	if err != nil {
		t.Fatal(err)
	}
	var saved probeCache
	if json.Unmarshal(b, &saved) != nil || saved.ProbedAt != first.UnixMilli() {
		t.Fatal("persisted timestamp is stale")
	}
	healthy.Store(true)
	l.mu.Lock()
	l.probedAt = time.Now().Add(-modelProbeTTL - time.Second)
	l.mu.Unlock()
	l.probeAsync([]string{"recovered"})
	waitForProbe(t, l)
	l.mu.Lock()
	bad = l.unavailable["recovered"]
	good := l.verified["recovered"]
	l.mu.Unlock()
	if posts.Load() != 2 || bad || !good {
		t.Fatalf("did not retry recovered model: posts=%d bad=%v good=%v", posts.Load(), bad, good)
	}
}

func TestOptionsDoesNotErasePreferenceDuringPartialDiscovery(t *testing.T) {
	l := newModelLister("", "", t.TempDir(), nil)
	c := &Channel{models: l, model: "default", prefs: map[string]accountPrefs{"tester": {Model: "saved-model"}}}
	w := httptest.NewRecorder()
	c.handleOptions(w, httptest.NewRequest("GET", "/api/options", nil), &storage.User{Username: "tester"})
	if w.Code != 200 || c.prefsOf("tester").Model != "saved-model" {
		t.Fatal("GET erased saved model")
	}
}

func TestProbeChecksAgentProtocolWithoutBlacklistingQuotaOrAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Tools) != 1 || request.Tools[0].Type != "function" {
			t.Error("probe must exercise the Agent's tools protocol")
			w.WriteHeader(500)
			return
		}
		switch request.Model {
		case "protocol":
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"Model does not support this protocol."}}`)
		case "tools":
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"error":{"message":"Model does not support tools"}}`)
		case "quota":
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"message":"Upstream unavailable"}}`)
		case "auth":
			w.WriteHeader(401)
		case "invalid-request":
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"max_tokens too small"}}`)
		case "healthy":
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"1"}}]}`)
		default:
			t.Errorf("unexpected probe model: %q", request.Model)
		}
	}))
	defer srv.Close()
	l := newModelLister(srv.URL, "fixture", t.TempDir(), nil)
	l.probeAsync([]string{"protocol", "tools", "quota", "auth", "invalid-request", "healthy"})
	waitForProbe(t, l)
	// Reload persisted probe results, as on a fresh process.
	l = newModelLister(srv.URL, "fixture", l.dataDir, nil)
	if !l.unavailable["protocol"] || !l.unavailable["tools"] || !l.verified["healthy"] {
		t.Fatalf("incompatible models remained selectable: bad=%v verified=%v", l.unavailable, l.verified)
	}
	for _, id := range []string{"quota", "auth", "invalid-request"} {
		if l.unavailable[id] || l.verified[id] {
			t.Errorf("transient/account error incorrectly classified: %s", id)
		}
	}
}

func TestOpenRouterNestedPricingKeepsCatalogAndMissingKeyState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Error("discovery must not generate requests")
		}
		_, _ = io.WriteString(w, `{"data":[
   {"id":"paid","pricing":{"prompt":"0.1","completion":"0.2","overrides":[{"utc_days":["saturday"],"prompt":"0.05"}]},"supported_parameters":["tools"]},
   {"id":"free","created":200,"pricing":{"prompt":"0","completion":"0","overrides":[]},"supported_parameters":["tools"]},
   {"id":"numeric-free","pricing":{"prompt":0,"completion":0},"supported_parameters":["tools"]},
   {"id":"conditional-free","pricing":{"prompt":"0","completion":"0","overrides":[{"utc_days":["saturday"],"prompt":"0.01"}]}},
   {"id":"unknown-free","pricing":{"prompt":"0","completion":"0","future_fee":{"price":"1"}}}
  ],"total_count":5,"links":{}}`)
	}))
	defer srv.Close()
	ctx := context.Background()
	models, err := fetchModelOptions(ctx, srv.URL, "")
	if err != nil || len(models) != 5 {
		t.Fatalf("nested pricing discarded catalog: %v %+v", err, models)
	}
	for i, want := range []bool{false, true, true, false, false} {
		if models[i].Free != want {
			t.Fatalf("free flag for %s = %v", models[i].ID, models[i].Free)
		}
	}
	c := &Channel{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	provider := config.Provider{ID: "openrouter", Label: "OpenRouter", BaseURL: srv.URL, FreeOnly: true}
	options := c.providerOptions(ctx, provider)
	if len(options) != 2 {
		t.Fatalf("free provider catalog: %+v", options)
	}
	for _, option := range options {
		if !option.NeedsKey || option.Provider != "openrouter" || !strings.HasPrefix(option.ID, "openrouter/") {
			t.Fatalf("missing provider metadata: %+v", option)
		}
	}
	// Simulate a fresh process after the operator adds a key; no generation is needed.
	c.providerModels = nil
	provider.APIKey = "test-only-key"
	options = c.providerOptions(ctx, provider)
	for _, option := range options {
		if option.NeedsKey {
			t.Fatal("configured provider remained disabled")
		}
	}
}
