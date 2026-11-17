package qq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemberProfilesAndMentionsStayInCurrentGroup(t *testing.T) {
	api, requests := testAPI(t, okHandler(`{"id":"ok"}`))
	c := &Channel{api: api}
	a := InboundMessage{Kind: "group_at", GroupOpenID: "G", MemberOpenID: "A", Username: "同名", Avatar: "https://example.test/a.png", MemberRole: "owner"}
	b := InboundMessage{Kind: "group", GroupOpenID: "G", MemberOpenID: "B", Username: "同名"}
	outsider := InboundMessage{Kind: "group", GroupOpenID: "OTHER", MemberOpenID: "C", Username: "外群"}
	for _, m := range []InboundMessage{a, b, outsider} {
		c.observeMembers("qq:group:"+m.GroupOpenID, m)
	}
	mc := &memberContext{channel: c, scope: "qq:group:G", target: "group:G:message", self: memberAlias(speakerID(a)), sent: map[string]bool{}}
	ctx := context.WithValue(context.Background(), memberContextKey{}, mc)
	profile, _ := (&memberTool{}).InvokableRun(ctx, `{"user":"self"}`)
	if !strings.Contains(profile, a.Avatar) || !strings.Contains(profile, "owner") || strings.Contains(profile, "MemberOpenID") {
		t.Fatal(profile)
	}
	list, _ := (&memberTool{}).InvokableRun(ctx, `{}`)
	if strings.Contains(list, "外群") || strings.Contains(list, a.Avatar) || !strings.Contains(list, memberAlias(speakerID(b))) {
		t.Fatal(list)
	}
	missing, _ := (&memberTool{}).InvokableRun(ctx, `{"user":"`+memberAlias(speakerID(outsider))+`"}`)
	if !strings.Contains(missing, "error") {
		t.Fatal("cross-group profile leaked")
	}
	tool := &mentionTool{}
	sent, _ := tool.InvokableRun(ctx, `{"user":"`+memberAlias(speakerID(b))+`","text":"请看这里"}`)
	if !strings.Contains(sent, `"ok":true`) || len(*requests) != 1 {
		t.Fatal(sent, len(*requests))
	}
	req := (*requests)[0]
	if req["_path"] != "/v2/groups/G/messages" || req["content"] != "<@!B> 请看这里" || req["msg_type"] != float64(0) {
		t.Fatal(req)
	}
	for _, raw := range []string{`{"user":"` + memberAlias(speakerID(b)) + `","text":"请看这里"}`, `{"user":"` + memberAlias(speakerID(outsider)) + `","text":"越界"}`, `{"user":"self","text":"@everyone hi"}`, `{"user":"self","text":"<@!C> hi"}`} {
		result, _ := tool.InvokableRun(ctx, raw)
		if !strings.Contains(result, "error") {
			t.Fatal(result)
		}
	}
	if len(*requests) != 1 {
		t.Fatal("duplicate/cross-group mention sent")
	}
	mc.scope = "qq:c2c:A"
	if result, _ := tool.InvokableRun(ctx, `{"user":"self","text":"hi"}`); !strings.Contains(result, "error") {
		t.Fatal("private mention allowed")
	}
}

func TestPlatformAvatarAndAttachmentSizeVariants(t *testing.T) {
	g := testGateway()
	var got InboundMessage
	g.onMessage = func(m InboundMessage) { got = m }
	g.dispatch("GROUP_AT_MESSAGE_CREATE", "e", mustRaw(t, `{"id":"m","group_openid":"G","author":{"member_openid":"A","username":"昵称","avatar":"https://example.test/a.png"},"content":"hello","attachments":[{"url":"https://example.test/v.mp4","size":"8388608","filename":"v.mp4","content_type":"video/mp4"}]}`))
	if got.Avatar != "https://example.test/a.png" || got.content.Attachments[0].Size != 8<<20 {
		t.Fatal("platform fields lost")
	}
	c := &Channel{}
	c.observeMembers("qq:group:G", got)
	mc := &memberContext{channel: c, scope: "qq:group:G", self: memberAlias(speakerID(got))}
	profile, _ := (&memberTool{}).InvokableRun(context.WithValue(context.Background(), memberContextKey{}, mc), `{"user":"self"}`)
	if !strings.Contains(profile, "昵称") || !strings.Contains(profile, "example.test/a.png") {
		t.Fatal(profile)
	}
	for _, size := range []string{`null`, `"unknown"`, `-1`, `1024`} {
		var a Attachment
		if err := json.Unmarshal([]byte(`{"url":"https://example.test/x","size":`+size+`}`), &a); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeferredVideoFetchPreservesOriginalAndDoesNotRedownload(t *testing.T) {
	raw := append([]byte{0, 0, 0, 24}, []byte("ftypmp42synthetic-video-original")...)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(raw) }))
	defer server.Close()
	workspace := t.TempDir()
	b := &inputBundle{client: server.Client(), workspaceRoot: workspace}
	defer b.cleanup()
	ctx, msg, err := b.prepare(context.Background(), inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: server.URL + "?private=secret", Filename: "../../video.mp4", ContentType: "video/mp4", Size: 64 << 20}}}}, "admin")
	if err != nil || calls.Load() != 0 || len(msg.UserInputMultiContent) != 0 || !strings.Contains(msg.Content, "qq_fetch_attachment") {
		t.Fatal("video eagerly downloaded or parsed", err, calls.Load())
	}
	tool := &fetchAttachmentTool{}
	result, err := tool.InvokableRun(ctx, `{"id":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
		SHA  string `json:"sha256"`
		Size int64  `json:"size"`
	}
	if err = json.Unmarshal([]byte(result), &got); err != nil || !got.OK || got.Path == "" {
		t.Fatal(result, err)
	}
	saved, err := os.ReadFile(filepath.Join(workspace, got.Path))
	if err != nil || !bytes.Equal(saved, raw) || got.Size != int64(len(raw)) || got.SHA != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatal("source replaced by preview or corrupt", err)
	}
	if strings.Contains(result, "secret") || strings.Contains(got.Path, "..") || filepath.IsAbs(got.Path) {
		t.Fatal("URL or unsafe name leaked", result)
	}
	_, _ = tool.InvokableRun(ctx, `{"id":1}`)
	if calls.Load() != 1 {
		t.Fatal("download repeated")
	}
	b.cleanup()
	if data, e := os.ReadFile(filepath.Join(workspace, got.Path)); e != nil || !bytes.Equal(data, raw) {
		t.Fatal("persistent source removed")
	}
	other := context.WithValue(context.Background(), inputBundleKey{}, &inputBundle{})
	if result, _ := tool.InvokableRun(other, `{"id":1}`); !strings.Contains(result, "error") {
		t.Fatal("cross-task file access")
	}
}

func TestOriginalFetchNormalUserAndStreamLimit(t *testing.T) {
	raw := []byte("original body")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		w.Write(raw)
	}))
	defer srv.Close()
	b := &inputBundle{client: srv.Client()}
	defer b.cleanup()
	ctx, _, err := b.prepare(context.Background(), inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: srv.URL, Filename: "large.bin", Size: 40 << 20}}}}, "user")
	if err != nil {
		t.Fatal(err)
	}
	result, _ := (&fetchAttachmentTool{}).InvokableRun(ctx, `{"id":1}`)
	if !strings.Contains(result, `"temporary":true`) || !strings.Contains(result, `"path":""`) {
		t.Fatal(result)
	}
	path := b.root
	b.cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("task original not cleaned")
	}
	// The declared size may be false; the body reader still enforces its limit,
	// removes partial files, and never buffers the source in memory.
	b = &inputBundle{client: srv.Client(), fetchedBytes: maxOriginalTotal - 4}
	defer b.cleanup()
	_, _, _, _, err = b.fetchOriginal(context.Background(), inputAttachment{Attachment: Attachment{URL: srv.URL}})
	if err == nil {
		t.Fatal("stream limit bypassed")
	}
	files, _ := os.ReadDir(b.root)
	if len(files) != 0 {
		t.Fatal("partial original retained")
	}
}

func TestOriginalFetchUsesSSRFGuard(t *testing.T) {
	b := &inputBundle{}
	defer b.cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, u := range []string{"http://127.0.0.1:1/private", "file:///etc/passwd", "https://user:password@example.test/x"} {
		if _, _, _, _, err := b.fetchOriginal(ctx, inputAttachment{Attachment: Attachment{URL: u}}); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
}

func TestFetchedCachedOriginalAvoidsSecondNetworkRead(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "small attachment") }))
	defer server.Close()
	b := &inputBundle{client: server.Client(), workspaceRoot: t.TempDir()}
	defer b.cleanup()
	ctx, _, err := b.prepare(context.Background(), inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: server.URL, Filename: "note.txt"}}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	result, _ := (&fetchAttachmentTool{}).InvokableRun(ctx, `{"id":1}`)
	if !strings.Contains(result, `"ok":true`) || calls.Load() != 1 {
		t.Fatal(result, calls.Load())
	}
}

func TestQQAgentCanQueryCurrentProfileAndMentionWithoutAttachments(t *testing.T) {
	var calls atomic.Int32
	c, _ := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		n := calls.Add(1)
		if n == 1 {
			names := map[string]bool{}
			for _, item := range body.Tools {
				fn, _ := item["function"].(map[string]any)
				name, _ := fn["name"].(string)
				names[name] = true
			}
			if !names["qq_member_profile"] || !names["qq_mention"] || !names["qq_fetch_attachment"] {
				t.Error("new tools missing")
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"profile","type":"function","function":{"name":"qq_member_profile","arguments":"{\"user\":\"self\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else if n == 2 {
			raw, _ := json.Marshal(body.Messages)
			if !bytes.Contains(raw, []byte("example.test/avatar.png")) {
				t.Error("task profile context missing")
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"mention","type":"function","function":{"name":"qq_mention","arguments":"{\"user\":\"self\",\"text\":\"你好\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"已发送"},"finish_reason":"stop"}]}`)
		}
	}))
	c.onMessage(InboundMessage{Kind: "group_at", GroupOpenID: "fixture-group", MemberOpenID: "fixture-member", Username: "昵称", Avatar: "https://example.test/avatar.png", MsgID: "fixture-message", Text: "查看我的资料并@我"})
	waitInputTasks(t, c)
	if calls.Load() != 3 {
		t.Fatal("profile/mention pipeline failed", calls.Load())
	}
}

func TestVideoCoverCannotBeReportedAsOriginalVideo(t *testing.T) {
	data := animatedInput(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer s.Close()
	b := &inputBundle{client: s.Client(), workspaceRoot: t.TempDir()}
	defer b.cleanup()
	ctx, _, err := b.prepare(context.Background(), inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: s.URL, Filename: "clip.mp4", ContentType: "video/mp4", Size: 64 << 20}}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	result, _ := (&fetchAttachmentTool{}).InvokableRun(ctx, `{"id":1}`)
	if !strings.Contains(result, "封面") || strings.Contains(result, `"ok":true`) {
		t.Fatal("cover treated as original video", result)
	}
}
