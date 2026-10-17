package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"mineagent/internal/storage"
)

type reviewTool string

func (t reviewTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: string(t)}, nil
}
func (t reviewTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return `{}`, nil
}

func TestReviewSearchSettingsAndStopAreOwnerScoped(t *testing.T) {
	c, ts, _ := newTestChannel(t)
	token := loginTest(t, ts, "alice")
	otherToken := loginTest(t, ts, "bob")
	ctx := context.Background()
	alice := c.acct.Current(ctx, token)
	bob := c.acct.Current(ctx, otherToken)
	var ownID, otherID int64
	for _, u := range []*storage.User{alice, bob} {
		key := webSessionKey(u.ID, "abcdef")
		if err := c.store.EnsureSession(ctx, key, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if err := c.store.UpsertConversationFor(ctx, u.ID, u.Username, "abcdef", "活动计划", 1); err != nil {
			t.Fatal(err)
		}
		id, err := c.store.AppendMessage(ctx, storage.Message{SessionID: key, AuthorKind: "player", Text: "讨论 UNO 游戏与活动", CreatedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		if u.ID == alice.ID {
			ownID = id
		} else {
			otherID = id
		}
	}
	var found struct {
		Conversations []struct {
			Conv     string `json:"conv"`
			MatchID  int64  `json:"matchId"`
			Snippet  string `json:"snippet"`
			Pinned   bool   `json:"pinned"`
			Archived bool   `json:"archived"`
		} `json:"conversations"`
	}
	if code := doJSON(t, "GET", ts.URL+"/api/conversations?q=UNO", token, nil, &found); code != 200 || len(found.Conversations) != 1 || found.Conversations[0].MatchID != ownID {
		t.Fatalf("search scope: %d %+v", code, found)
	}
	if code := doJSON(t, "GET", fmt.Sprintf("%s/api/history?conv=abcdef&around=%d", ts.URL, otherID), token, nil, nil); code != 404 {
		t.Fatalf("foreign message around=%d", code)
	}
	if code := doJSON(t, "GET", fmt.Sprintf("%s/api/history?conv=abcdef&around=%d", ts.URL, ownID), token, nil, nil); code != 200 {
		t.Fatalf("owned message around=%d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/api/conversations/settings", token, map[string]any{"conv": "abcdef", "pinned": true, "archived": true}, nil); code != 200 {
		t.Fatal(code)
	}
	settings, err := c.store.ConversationSettings(ctx, bob.ID)
	if err != nil || settings["abcdef"].Pinned {
		t.Fatalf("settings leaked: %v %+v", err, settings)
	}
	var state struct {
		Stopping bool `json:"stopping"`
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.active[webSessionKey(alice.ID, "abcdef")] = cancel
	c.mu.Unlock()
	if code := doJSON(t, "POST", ts.URL+"/api/stop", otherToken, map[string]string{"conv": "abcdef"}, &state); code != 200 || state.Stopping || runCtx.Err() != nil {
		t.Fatal("other user stopped a run")
	}
	if code := doJSON(t, "POST", ts.URL+"/api/conversations/delete", token, map[string]string{"conv": "abcdef"}, nil); code != 409 {
		t.Fatalf("deleted running conversation: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/api/stop", token, map[string]string{"conv": "abcdef"}, &state); code != 200 || !state.Stopping || runCtx.Err() == nil {
		t.Fatal("owner could not cancel")
	}
	if code := doJSON(t, "GET", ts.URL+"/api/stop", token, nil, nil); code != http.StatusMethodNotAllowed {
		t.Fatal(code)
	}
	// Persistent tool metadata survives reconnect and is limited to the owner.
	event := `{"id":"test","name":"current_time","status":"returned"}`
	if err := c.store.SaveWebRunEvent(ctx, webSessionKey(alice.ID, "abcdef"), "test", event); err != nil {
		t.Fatal(err)
	}
	var history map[string]json.RawMessage
	doJSON(t, "GET", ts.URL+"/api/history?conv=abcdef", token, nil, &history)
	if string(history["runEvents"]) != "["+event+"]" {
		t.Fatalf("missing tool history: %s", history["runEvents"])
	}
	doJSON(t, "GET", ts.URL+"/api/history?conv=abcdef", otherToken, nil, &history)
	if string(history["runEvents"]) != "[]" {
		t.Fatalf("foreign tool history: %s", history["runEvents"])
	}
}
func TestReviewCapabilityCatalogMatchesOfferedTools(t *testing.T) {
	c, _, _ := newTestChannel(t)
	c.webTools = []tool.BaseTool{reviewTool("current_time"), reviewTool("web_search"), reviewTool("workspace_exec"), reviewTool("web_file"), reviewTool("minecraft_give"), reviewTool("remind")}
	u := &storage.User{Username: "ordinary"}
	ctx := context.Background()
	for _, tl := range c.toolsForUser(ctx, u) {
		info, _ := tl.Info(ctx)
		if info.Name == "workspace_exec" || info.Name == "web_file" {
			t.Fatal("admin tool exposed")
		}
	}
	byName := map[string]capability{}
	for _, entry := range c.capabilities(ctx, u) {
		byName[entry.Name] = entry
	}
	if byName["web_search"].Status != "available" || byName["workspace_exec"].Status != "admin" || byName["minecraft_give"].Status != "approval" || byName["remind"].Kind == "查询" {
		t.Fatalf("bad capability classifications: %+v", byName)
	}
}
func TestReviewLocalTitlesSkipDisclaimers(t *testing.T) {
	if got := suggestConversationTitle("这是网站体验测试，以下内容完全虚构。周六 Minecraft 聚会安排", "# 周六活动安排\n内容"); got != "周六活动安排" {
		t.Fatal(got)
	}
	if got := suggestConversationTitle("这是网站体验测试。安排 UNO 活动", "当然可以"); got != "安排 UNO 活动" {
		t.Fatal(got)
	}
}
