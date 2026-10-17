package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/storage"
)

type deadlineTransport func(*http.Request) (*http.Response, error)

func (f deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Check the deadline the outbound transport actually receives, rather than waiting
// ninety seconds or only asserting a config field. A shorter parent remains authoritative.
func TestModelRequestDeadlineFollowsRunBudget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		budget       int
		parent, want time.Duration
	}{
		{"long reasoning is not cut at ninety seconds", 180, 6 * time.Minute, 3 * time.Minute},
		{"short caller deadline wins", 180, 10 * time.Second, 10 * time.Second},
		{"default budget", 0, 6 * time.Minute, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := config.Default()
			cfg.Model.BaseURL = "https://gateway.invalid/v1"
			cfg.Model.APIKey = "fixture"
			cfg.Model.Name = "test"
			cfg.Agent.RunTimeoutSec = tc.budget
			a, err := New(context.Background(), cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			observed := time.Duration(0)
			a.httpClient.Transport.(*headerTransport).base = deadlineTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("unbounded model request")
				}
				observed = time.Until(deadline)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), tc.parent)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Model.BaseURL+"/chat/completions", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			res, err := a.httpClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if observed < tc.want-2*time.Second || observed > tc.want+time.Second {
				t.Fatalf("outbound deadline %s, want about %s", observed, tc.want)
			}
		})
	}
}
func TestModelTimeoutHintDoesNotEchoGatewayDetails(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("node failed: %w", context.DeadlineExceeded),
		&url.Error{Op: "Post", URL: "https://private.invalid/key", Err: context.DeadlineExceeded},
		errors.New("wrapped Client.Timeout exceeded while awaiting headers"),
	} {
		hint := modelFailureHint(err)
		if !strings.Contains(hint, "超时") || !strings.Contains(hint, "未自动重发") || strings.Contains(hint, "private.invalid") {
			t.Fatalf("bad public hint: %s", hint)
		}
	}
	if !strings.Contains(modelFailureHint(errors.New("HTTP 503 unavailable")), "不可用") {
		t.Fatal("lost unavailable hint")
	}
}
