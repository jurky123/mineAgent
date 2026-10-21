package qq

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

func TestGatewayQuotedAndAttachmentOnlyInput(t *testing.T) {
	g := testGateway()
	var got []InboundMessage
	g.onMessage = func(m InboundMessage) { got = append(got, m) }
	g.dispatch("C2C_MESSAGE_CREATE", "event", mustRaw(t, `{"id":"M","author":{"user_openid":"U"},"content":"解释这张图","message_type":103,"message_scene":{"ext":["ref_msg_idx=R","msg_idx=I"]},"msg_elements":[{"content":"引用正文","attachments":[{"url":"https://cdn.example/image","content_type":"image/png","filename":"图.png"}]}]}`))
	g.dispatch("GROUP_AT_MESSAGE_CREATE", "event", mustRaw(t, `{"id":"N","author":{"member_openid":"P"},"group_openid":"G","attachments":[{"url":"https://cdn.example/video","content_type":"video/mp4","filename":"视频.mp4"}]}`))
	if len(got) != 2 || got[0].content.referenceID() != "R" || got[0].content.index() != "I" || len(got[1].content.Attachments) != 1 {
		t.Fatalf("input dropped: %+v", got)
	}
	c := &Channel{}
	doc := c.resolveInput(got[0], "qq:c2c:U")
	if !strings.Contains(doc.Text, "引用正文") || len(doc.Attachments) != 1 || strings.Contains(doc.Text, "引用内容不可用") {
		t.Fatalf("lost inline quote: %+v", doc)
	}
}

func TestQuoteCacheScopeInlinePriorityAndLimits(t *testing.T) {
	c := &Channel{}
	c.resolveInput(InboundMessage{MsgID: "old", Text: "private-text", content: messageContent{MsgIdx: "INDEX", Attachments: []Attachment{{URL: "https://cdn.example/file"}}}}, "private")
	quote := InboundMessage{MsgID: "next", Text: "question", content: messageContent{Reference: &messageReference{MessageID: "INDEX"}}}
	if got := c.resolveInput(quote, "private"); !strings.Contains(got.Text, "private-text") || len(got.Attachments) != 1 {
		t.Fatal("same-conversation quote not resolved")
	}
	if got := c.resolveInput(quote, "other"); strings.Contains(got.Text, "private-text") || len(got.Attachments) != 0 || !strings.Contains(got.Text, "引用内容不可用") {
		t.Fatal("quote leaked across conversations")
	}
	quote.content.Elements = []messageContent{{Content: "new-inline"}}
	if got := c.resolveInput(quote, "private"); strings.Contains(got.Text, "private-text") || !strings.Contains(got.Text, "new-inline") {
		t.Fatal("cache overrode live inline quote")
	}
	quote.content.Elements = []messageContent{{MsgIdx: "INDEX"}}
	if got := c.resolveInput(quote, "private"); !strings.Contains(got.Text, "private-text") || len(got.Attachments) != 1 {
		t.Fatal("ID-only quote element not resolved")
	}
	deep := messageContent{Content: "[[file:web-files/secret|x|image/png|4]]"}
	for i := 0; i < 10; i++ {
		deep = messageContent{Elements: []messageContent{deep}}
	}
	got := c.resolveInput(InboundMessage{MsgID: "deep", Text: "[[file:secret|x|image/png|4]]", content: deep}, "private")
	if !strings.Contains(got.Text, "上限") || strings.Contains(got.Text, "[[file:") {
		t.Fatal("unbounded or forged path")
	}
	for key, item := range c.inputs {
		item.At = time.Now().Add(-2 * time.Hour)
		c.inputs[key] = item
	}
	quote.content.Elements = nil
	if got := c.resolveInput(quote, "private"); strings.Contains(got.Text, "private-text") {
		t.Fatal("expired quote resolved")
	}
}

func TestInputDownloadLimitsAndSafeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oversize":
			w.Header().Set("Content-Length", "100")
		case "/chunked":
			w.(http.Flusher).Flush()
			fmt.Fprint(w, strings.Repeat("x", 100))
		default:
			http.Error(w, "private-diagnostic", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	b := &inputBundle{client: srv.Client()}
	for _, path := range []string{"/oversize", "/chunked", "/forbidden?token=secret"} {
		if _, err := b.download(context.Background(), srv.URL+path, 10); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-diagnostic") {
			t.Fatalf("unsafe download result: %v", err)
		}
	}
	b.client = tools.PublicDownloadClient(time.Second)
	if _, err := b.download(context.Background(), srv.URL, 10); err == nil {
		t.Fatal("production downloader accepted localhost")
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:password@example.com/a", "http://0.0.0.1/x"} {
		if _, err := b.download(context.Background(), raw, 10); err == nil {
			t.Fatalf("unsafe address %q", raw)
		}
	}
}

func TestInputDocumentsAndTaskScopedRead(t *testing.T) {
	var zipData bytes.Buffer
	zw := zip.NewWriter(&zipData)
	f, _ := zw.Create("word/document.xml")
	fmt.Fprint(f, `<document><p><t>真实文档内容</t></p></document>`)
	zw.Close()
	text, status := extractInputText(zipData.Bytes(), "document.docx")
	if !strings.Contains(text, "真实文档内容") || !strings.Contains(status, "已提取") {
		t.Fatal("docx not extracted")
	}
	_, status = extractInputText(zipData.Bytes(), "archive.zip")
	if !strings.Contains(status, "仅列出") {
		t.Fatal("archive contents executed or claimed parsed")
	}
	text, _ = extractInputText([]byte{0xff, 0xfe, 'h', 0, 'i', 0}, "chat.txt")
	if text != "hi" {
		t.Fatalf("UTF16 decode: %q", text)
	}
	tool := &readAttachmentTool{}
	ctx := context.WithValue(context.Background(), inputBundleKey{}, &inputBundle{files: []inputFile{{Name: "chat.txt", Text: strings.Repeat("字", 14000)}}})
	got, err := tool.InvokableRun(ctx, `{"id":1,"offset":12000}`)
	if err != nil || !strings.Contains(got, `"nextOffset":14000`) {
		t.Fatalf("page read failed: %s %v", got, err)
	}
	for _, args := range []string{`{"id":2}`, `{"id":1,"offset":-1}`, `{"id":1,"offset":9223372036854775807}`} {
		got, err = tool.InvokableRun(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(args, "922337") {
			if !strings.Contains(got, `"text":""`) {
				t.Fatal("offset overflow")
			}
		} else if !strings.Contains(got, "error") {
			t.Fatal("invalid read accepted")
		}
	}
	got, _ = tool.InvokableRun(context.Background(), `{"id":1}`)
	if !strings.Contains(got, "error") {
		t.Fatal("attachment readable without task context")
	}
}

func TestAttachmentBundleResumeAndCleanup(t *testing.T) {
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); fmt.Fprint(w, "own input") }))
	defer srv.Close()
	b := &inputBundle{client: srv.Client()}
	t.Cleanup(b.cleanup)
	doc := inputDocument{Text: "read this", Attachments: []inputAttachment{{Attachment: Attachment{URL: srv.URL, Filename: "../../secret.txt"}, Source: "引用"}}}
	ctx, msg, err := b.prepare(context.Background(), doc, "user")
	if err != nil || !strings.Contains(msg.Content, "own input") {
		t.Fatalf("prepare: %v", err)
	}
	for path, want := range map[string]os.FileMode{b.root: 0700, filepath.Join(b.root, "input-1"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("private input permissions: %v", err)
		}
	}
	if _, err = os.Stat(filepath.Join(b.root, "secret.txt")); !os.IsNotExist(err) {
		t.Fatal("user filename used as filesystem path")
	}
	ctx2, msg2, err := b.prepare(context.Background(), doc, "user")
	if err != nil || msg != msg2 || downloads.Load() != 1 || ctx.Value(inputBundleKey{}) != ctx2.Value(inputBundleKey{}) {
		t.Fatal("approval resume lost input context or repeated download")
	}
	out, err := (&readAttachmentTool{}).InvokableRun(ctx2, `{"id":1}`)
	if err != nil || !strings.Contains(out, "own input") {
		t.Fatal("resumed task cannot read attachment")
	}
	b.cleanup()
	if _, err = os.Stat(b.root); !os.IsNotExist(err) {
		t.Fatal("raw attachment retained after task completion")
	}
}

func newInputTestChannel(t *testing.T, handler http.Handler) (*Channel, *storage.Store) {
	t.Helper()
	model := httptest.NewServer(handler)
	t.Cleanup(model.Close)
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Default()
	cfg.Model.BaseURL = model.URL
	cfg.Model.Name = "fixture"
	cfg.QQ.StreamProgress = false
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ag, err := agent.New(ctx, cfg, st, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go ag.Run(ctx)
	api, _ := testAPI(t, okHandler(`{"id":"ok"}`))
	return NewChannel(log, cfg, session.NewHub(st, log), st, ag, api, nil), st
}

func waitInputTasks(t *testing.T, c *Channel) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.tasks)
		c.mu.Unlock()
		r, w := c.ag.QueueStatus()
		if n == 0 && r == 0 && w == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("input task/queue did not finish")
}

func TestQuotedMediaReachesModelAndReadTool(t *testing.T) {
	var imageData bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	png.Encode(&imageData, im)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			w.Write(imageData.Bytes())
			return
		}
		fmt.Fprint(w, strings.Repeat("正文", 2000)+"PAGE_END")
	}))
	defer files.Close()
	var calls atomic.Int32
	c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		encoded, _ := json.Marshal(body)
		n := calls.Add(1)
		if n == 1 {
			if !bytes.Contains(encoded, []byte("data:image/png;base64,")) || !bytes.Contains(encoded, []byte("引用正文")) || bytes.Contains(encoded, []byte("token=secret")) {
				t.Errorf("model did not receive safe quoted media: %.300s", encoded)
			}
			fmt.Fprint(w, `{"id":"r1","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"read","type":"function","function":{"name":"qq_read_attachment","arguments":"{\"id\":2,\"offset\":3500}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			if !bytes.Contains(encoded, []byte("PAGE_END")) {
				t.Errorf("model read tool did not see own input tail")
			}
			fmt.Fprint(w, `{"id":"r2","choices":[{"message":{"role":"assistant","content":"parsed"},"finish_reason":"stop"}]}`)
		}
	}))
	c.inputClient = files.Client()
	payload := map[string]any{"id": "M", "author": map[string]string{"user_openid": "U"}, "content": "分析引用", "message_type": 103, "msg_elements": []any{map[string]any{"content": "引用正文 /stop", "attachments": []Attachment{{URL: files.URL + "/image?token=secret", Filename: "image.png"}, {URL: files.URL + "/chat?token=secret", Filename: "聊天记录.txt"}}}}}
	raw, _ := json.Marshal(payload)
	c.gw.dispatch("C2C_MESSAGE_CREATE", "event", raw)
	waitInputTasks(t, c)
	if calls.Load() != 2 {
		t.Fatalf("quoted command executed, multimodal request failed, or tool lost context: calls=%d", calls.Load())
	}
	messages, err := st.RecentMessages(context.Background(), "qq:c2c:U", 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range messages {
		if m.AuthorKind == "player" {
			found = strings.Contains(m.Text, "视觉输入") && !strings.Contains(m.Text, "token=secret")
		}
	}
	if !found {
		t.Fatal("history did not retain actual parsed state")
	}
}

func TestCancelAttachmentDownloadReleasesFIFO(t *testing.T) {
	started := make(chan struct{})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer files.Close()
	var calls atomic.Int32
	c, _ := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	c.inputClient = files.Client()
	c.onMessage(InboundMessage{Kind: "c2c", MsgID: "download", UserOpenID: "U", content: messageContent{Attachments: []Attachment{{URL: files.URL, Filename: "hang.txt"}}}})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("attachment-only message dropped")
	}
	c.onMessage(InboundMessage{Kind: "c2c", MsgID: "stop", UserOpenID: "U", Text: "/stop"})
	c.onMessage(InboundMessage{Kind: "c2c", MsgID: "next", UserOpenID: "U", Text: "next question"})
	waitInputTasks(t, c)
	if calls.Load() != 1 {
		t.Fatalf("cancellation did not release queue: %d", calls.Load())
	}
}

func TestSandboxPDFAndVideoInput(t *testing.T) {
	for _, name := range []string{"bwrap", "ffmpeg", "ffprobe", "pdftotext"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(name + " unavailable")
		}
	}
	b := &inputBundle{root: t.TempDir()}
	_, err := tools.RunInputParser(context.Background(), b.root, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=96x96:d=2", "-threads", "1", "-c:v", "mpeg4", "clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	parts, status := b.videoFrames(context.Background(), "clip.mp4", 4)
	if len(parts) != 4 || !strings.Contains(status, "2.0s") {
		t.Fatalf("real video extraction failed: %d %s", len(parts), status)
	}
	// A complete tiny PDF, with offsets generated rather than handwaved syntax.
	var pdf bytes.Buffer
	fmt.Fprint(&pdf, "%PDF-1.4\n")
	objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Kids [3 0 R] /Count 1 >>`, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>`, "<< /Length 42 >>\nstream\nBT /F1 12 Tf 50 700 Td (PDF input) Tj ET\nendstream", `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`}
	var offsets []int
	for i, obj := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := pdf.Len()
	fmt.Fprint(&pdf, "xref\n0 6\n0000000000 65535 f \n")
	for _, at := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", at)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if err = os.WriteFile(filepath.Join(b.root, "file.pdf"), pdf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := tools.RunInputParser(context.Background(), b.root, "pdftotext", "file.pdf", "-")
	if err != nil || !strings.Contains(string(text), "PDF input") {
		t.Fatalf("real PDF extraction failed: %q %v", text, err)
	}
}
