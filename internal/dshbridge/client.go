// Package dshbridge delegates the agent loop to an existing DSH host. All tools
// execute in MineAgent, with the original request's identity and cancellation.
package dshbridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxFrame = 64 << 20

// RunError carries only a validated public provider error code.
// It does not imply that earlier tools can safely be replayed.
type RunError struct{ Code string }

func (e *RunError) Error() string {
	return fmt.Sprintf("DSH agent failed (%s); completed actions were not rolled back", e.Code)
}

type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
type Config struct {
	URL       string `json:"url"`
	TokenFile string `json:"tokenFile"`
	Route
	// Explicit model selectors may be routed to adapters already registered in DSH.
	Models map[string]Route `json:"models,omitempty"`
}
type Block struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type Request struct {
	ID string `json:"id"`
	Route
	ReasoningEffort string    `json:"reasoningEffort"`
	Instruction     string    `json:"instruction"`
	Messages        []Message `json:"messages"`
	Tools           []Tool    `json:"tools"`
	MaxIterations   int       `json:"maxIterations,omitempty"`
}
type frame struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Text      string          `json:"text"`
	Code      string          `json:"code"`
}
type Client struct {
	cfg   Config
	token string
	http  *http.Client
	mu    sync.Mutex
	fault bool
	gate  chan struct{}
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("DSH bridge requires a loopback HTTP origin")
	}
	port, portErr := strconv.Atoi(u.Port())
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 {
		return nil, errors.New("DSH bridge requires a literal loopback address and port")
	}
	st, err := os.Stat(cfg.TokenFile)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 4096 {
		return nil, errors.New("DSH token file must be private (0600)")
	}
	b, err := os.ReadFile(cfg.TokenFile)
	token := strings.TrimSpace(string(b))
	if err != nil || len(token) < 32 || strings.ContainsAny(token, "\r\n \t") {
		return nil, errors.New("DSH bridge token must contain at least 32 non-whitespace characters")
	}
	if cfg.Provider == "" || cfg.Model == "" {
		return nil, errors.New("DSH provider/model are required")
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &Client{cfg: cfg, token: token, gate: make(chan struct{}, 1), http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("bridge redirects are forbidden") }}}, nil
}
func (c *Client) Route(selector, primary string) (Route, bool) {
	if selector == "" || selector == primary {
		return c.cfg.Route, true
	}
	r, ok := c.cfg.Models[selector]
	return r, ok
}
func (c *Client) post(ctx context.Context, path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFrame {
		return nil, errors.New("DSH bridge request too large")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("DSH bridge unavailable")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("DSH bridge HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// Run never replays a failed request or falls back after actions may have run.
// Cancellation waits for DSH quiescence. Failure to confirm it latches this
// client closed until restart, preventing overlapping uncertain agent loops.
func (c *Client) Run(ctx context.Context, req Request, invoke func(context.Context, string, string) (string, error)) (text string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.gate }()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	c.mu.Lock()
	fault := c.fault
	c.mu.Unlock()
	if fault {
		return "", errors.New("DSH bridge stop unconfirmed; restart required")
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return "", err
	}
	req.ID = hex.EncodeToString(id)
	completed := false
	defer func() {
		if completed {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, e := c.post(stopCtx, "/v1/runs/"+req.ID+"/cancel", struct{}{})
		if e == nil {
			var ack struct {
				Stopped bool `json:"stopped"`
			}
			e = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&ack)
			resp.Body.Close()
			if e == nil && !ack.Stopped {
				e = errors.New("stop not confirmed")
			}
		}
		if e != nil {
			c.mu.Lock()
			c.fault = true
			c.mu.Unlock()
		}
	}()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	resp, err := c.post(runCtx, "/v1/runs", req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		return "", errors.New("invalid DSH bridge response")
	}
	type streamEvent struct {
		frame frame
		err   error
	}
	events := make(chan streamEvent, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), MaxFrame)
		for scanner.Scan() {
			var f frame
			if json.Unmarshal(scanner.Bytes(), &f) != nil {
				select {
				case events <- streamEvent{err: errors.New("invalid DSH bridge frame")}:
				case <-runCtx.Done():
				}
				return
			}
			select {
			case events <- streamEvent{frame: f}:
			case <-runCtx.Done():
				return
			}
		}
		select {
		case events <- streamEvent{err: errors.New("DSH bridge disconnected before completion")}:
		case <-runCtx.Done():
		}
	}()
	type toolResult struct {
		value string
		err   error
	}
	var active <-chan toolResult
	var activeID string
	var worker sync.WaitGroup
	// Keep reading the transport while a tool runs. DSH-side cancellation or a
	// broken stream must cancel local work, including an outstanding approval.
	defer func() { cancelRun(); resp.Body.Close(); worker.Wait(); <-readerDone }()
	seen := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result := <-active:
			active = nil
			if runCtx.Err() != nil {
				return "", runCtx.Err()
			}
			output := struct {
				Value string `json:"value"`
				Error bool   `json:"error"`
			}{Value: result.value, Error: result.err != nil}
			if result.err != nil {
				output.Value = "工具调用失败，请检查输入或权限后决定下一步。"
			}
			ack, e := c.post(runCtx, "/v1/runs/"+req.ID+"/tools/"+activeID, output)
			if e != nil {
				return "", e
			}
			io.Copy(io.Discard, io.LimitReader(ack.Body, 4096))
			ack.Body.Close()
		case event := <-events:
			if event.err != nil {
				return "", event.err
			}
			f := event.frame
			switch f.Type {
			case "ready", "heartbeat":
			case "tool":
				if active != nil || invoke == nil || f.ID == "" || len(f.ID) > 128 || strings.ContainsAny(f.ID, "/?#%\\") || seen[f.ID] {
					return "", errors.New("duplicate, overlapping or invalid DSH tool call")
				}
				seen[f.ID] = true
				activeID = f.ID
				result := make(chan toolResult, 1)
				active = result
				worker.Add(1)
				go func() {
					defer worker.Done()
					if runCtx.Err() != nil {
						result <- toolResult{err: runCtx.Err()}
						return
					}
					value, e := invoke(runCtx, f.Name, string(f.Arguments))
					result <- toolResult{value: value, err: e}
				}()
			case "done":
				if active != nil {
					return "", errors.New("DSH completed before local tool settled")
				}
				completed = true
				return f.Text, nil
			case "error":
				completed = true
				code := f.Code
				if !safeErrorCode(code) {
					code = "UNKNOWN"
				}
				return "", &RunError{Code: code}
			default:
				return "", errors.New("unknown DSH bridge frame")
			}
		}
	}

}

func safeErrorCode(code string) bool {
	switch code {
	case "AUTHENTICATION_ERROR", "RATE_LIMITED", "CONTEXT_WINDOW_EXCEEDED", "UNSUPPORTED_REASONING_EFFORT", "UNSUPPORTED_CONTENT", "MODEL_NOT_FOUND", "INVALID_REQUEST", "UNKNOWN_TOOL", "API_ERROR":
		return true
	}
	return false
}
