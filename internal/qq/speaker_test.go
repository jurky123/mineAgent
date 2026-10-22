package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mineagent/internal/storage"
)

func TestQQSpeakerSurvivesNamesQuotesAndPreparedImage(t *testing.T) {
	requests := make(chan string, 4)
	c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		body, _ := json.Marshal(input.Messages)
		requests <- string(body)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(animatedInput(t)) }))
	defer files.Close()
	c.inputClient = files.Client()
	first := InboundMessage{Kind: "group_at", GroupOpenID: "G", MemberOpenID: "Alice", Username: "同名", MsgID: "one", Text: "我是甲"}
	second := first
	second.MemberOpenID = "Bob"
	second.MsgID = "two"
	second.Text = "我是乙，查看图片与引用"
	second.content = messageContent{Attachments: []Attachment{{URL: files.URL, Filename: "pic.gif"}}, Elements: []messageContent{{Content: "甲的引用", Author: &qqUser{MemberOpenID: "Alice", Username: "同名"}}}}
	c.onMessage(first)
	waitInputTasks(t, c)
	c.onMessage(second)
	waitInputTasks(t, c)
	<-requests
	input := <-requests
	a := storage.QQSpeakerLabel(speakerID(first), "同名")
	b := storage.QQSpeakerLabel(speakerID(second), "同名")
	// Decode JSON escaping before checking labels in textual and image input.
	var messages []map[string]any
	if err := json.Unmarshal([]byte(input), &messages); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, m := range messages {
		fmt.Fprint(&text, m["content"])
	}
	if a == b || !strings.Contains(text.String(), a) || !strings.Contains(text.String(), b) || !strings.Contains(text.String(), "引用/聊天记录") {
		t.Fatal("speaker identity lost in multimodal/quoted history", text.String())
	}
	rows, _ := st.RecentMessages(context.Background(), "qq:group:G", 20)
	for _, row := range rows {
		if row.AuthorKind == "player" && row.AuthorID != "qq:member:G:Alice" && row.AuthorID != "qq:member:G:Bob" {
			t.Fatal("conversation stored as speaker", row.AuthorID)
		}
	}
	first.Username = "新名字"
	if speakerID(first) != "qq:member:G:Alice" {
		t.Fatal("rename changed principal")
	}
	other := first
	other.GroupOpenID = "OTHER"
	if speakerID(other) == speakerID(first) {
		t.Fatal("group member identity escaped scope")
	}
}
