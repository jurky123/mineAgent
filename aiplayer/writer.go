package aiplayer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Writer is a controlled client, not a new LLM loop. Its URL and credential file
// are pinned at construction by the operator, never by a model's tool arguments.
type Writer struct {
	base, tokenFile string
	client          *http.Client
}

func NewWriter(base, tokenFile string) (*Writer, error) {
	u, err := url.Parse(base)
	if err != nil || !safeServiceURL(base) || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || tokenFile == "" {
		return nil, errors.New("writer requires a pinned origin and private credential file")
	}
	return &Writer{strings.TrimRight(base, "/"), tokenFile, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type WritingRequest struct {
	Operation       string        `json:"operation"`
	PostID          string        `json:"postId,omitempty"`
	IdempotencyKey  string        `json:"idempotencyKey,omitempty"`
	Post            *PostInput    `json:"post,omitempty"`
	Profile         *ProfileInput `json:"profile,omitempty"`
	RevisionID      int64         `json:"revisionId,omitempty"`
	ExpectedVersion int64         `json:"expectedVersion,omitempty"`
}

func (w *Writer) Do(ctx context.Context, in WritingRequest) (json.RawMessage, error) {
	method, path := http.MethodGet, ""
	var payload any
	switch in.Operation {
	case "list":
		path = "/api/ai-posts/mine"
	case "get":
		path = "/api/ai-posts/mine/" + in.PostID
	case "read-profile":
		path = "/api/ai-agents/mine/profile"
	case "profile":
		method = http.MethodPatch
		path = "/api/ai-agents/mine/profile"
		if in.Profile == nil {
			return nil, errors.New("profile required")
		}
		payload = in.Profile
	case "create":
		method = http.MethodPost
		path = "/api/ai-posts"
		if in.Post == nil {
			return nil, errors.New("post required")
		}
		payload = in.Post
	case "edit":
		method = http.MethodPatch
		path = "/api/ai-posts/" + in.PostID
		if in.Post == nil {
			return nil, errors.New("post required")
		}
		payload = in.Post
	case "delete":
		method = http.MethodDelete
		path = "/api/ai-posts/" + in.PostID
		payload = map[string]int64{"revisionId": in.RevisionID, "expectedVersion": in.ExpectedVersion}
	default:
		return nil, errors.New("unsupported writing operation")
	}
	if has([]string{"get", "edit", "delete"}, in.Operation) && !identifier.MatchString(in.PostID) {
		return nil, errors.New("invalid post ID")
	}
	if method != http.MethodGet && (len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128) {
		return nil, errors.New("persist an Idempotency-Key before submitting a write")
	}
	token, err := readPrivate(w.tokenFile)
	if err != nil {
		return nil, err
	}
	var data []byte
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, w.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("cannot construct writing request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		req.Header.Set("Idempotency-Key", in.IdempotencyKey)
	}
	res, err := w.client.Do(req)
	if err != nil {
		return nil, errors.New("writing result unconfirmed; inspect or retry with the SAME idempotency key")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	if err != nil || !json.Valid(raw) {
		return nil, errors.New("invalid/unconfirmed writing response; retain the SAME idempotency key")
	}
	if res.StatusCode != http.StatusOK {
		var f Fault
		if json.Unmarshal(raw, &f) != nil || !has([]string{"rate_limited", "forbidden", "not_found", "invalid_content", "needs_review", "conflict", "unauthorized", "invalid_request", "unavailable"}, f.Code) {
			return nil, errors.New("writing request rejected")
		}
		f.Status = res.StatusCode
		return nil, &f
	}
	return raw, nil
}
