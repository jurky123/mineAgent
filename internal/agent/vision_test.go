package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

func writeTinyPNG(t *testing.T, dir, name string) {
	t.Helper()
	// 1x1 红点 PNG
	b, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUserMessageVision(t *testing.T) {
	dir := t.TempDir()
	writeTinyPNG(t, dir, "a.png")
	a := &Agent{workspaceRoot: dir}

	text := "这张图是什么\n" + storage.BuildFileMarker(storage.UploadedFile{
		Path: "a.png", Name: "a.png", Mime: "image/png", Size: 68,
	})
	msg := a.userMessage("jzk", text, true)
	if msg.Role != schema.User {
		t.Fatalf("role = %v", msg.Role)
	}
	if len(msg.UserInputMultiContent) != 2 {
		t.Fatalf("parts = %d: %+v", len(msg.UserInputMultiContent), msg.UserInputMultiContent)
	}
	head := msg.UserInputMultiContent[0]
	if head.Type != schema.ChatMessagePartTypeText || !strings.Contains(head.Text, "[jzk] 这张图是什么") {
		t.Fatalf("head = %+v", head)
	}
	if strings.Contains(head.Text, "[[file:") {
		t.Fatalf("已附图的标记应从文本里去掉: %q", head.Text)
	}
	if !strings.Contains(head.Text, "已直接附在消息里") {
		t.Fatalf("缺少附图提示: %q", head.Text)
	}
	img := msg.UserInputMultiContent[1]
	if img.Type != schema.ChatMessagePartTypeImageURL || img.Image == nil || img.Image.Base64Data == nil {
		t.Fatalf("img part = %+v", img)
	}
	if !strings.HasPrefix(img.Image.MIMEType, "image/") || *img.Image.Base64Data == "" {
		t.Fatalf("img meta = %+v", img.Image)
	}
}

func TestUserMessageVisionOff(t *testing.T) {
	dir := t.TempDir()
	writeTinyPNG(t, dir, "a.png")
	a := &Agent{workspaceRoot: dir}
	text := "看图\n" + storage.BuildFileMarker(storage.UploadedFile{Path: "a.png", Name: "a.png", Mime: "image/png", Size: 68})
	msg := a.userMessage("jzk", text, false)
	if len(msg.UserInputMultiContent) != 0 || !strings.Contains(msg.Content, "[[file:a.png|") {
		t.Fatalf("withVision=false 应保持纯文本: %+v", msg)
	}
}

func TestUserMessageNonImageKeepsMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{workspaceRoot: dir}
	text := "看文件\n" + storage.BuildFileMarker(storage.UploadedFile{Path: "r.txt", Name: "r.txt", Mime: "text/plain", Size: 2})
	msg := a.userMessage("jzk", text, true)
	if len(msg.UserInputMultiContent) != 0 {
		t.Fatalf("非图片不该走多模态: %+v", msg)
	}
	if !strings.Contains(msg.Content, "[[file:r.txt|") {
		t.Fatalf("非图片标记应保留: %q", msg.Content)
	}
}

func TestUserMessageMissingImageFallsBack(t *testing.T) {
	a := &Agent{workspaceRoot: t.TempDir()}
	text := "图\n" + storage.BuildFileMarker(storage.UploadedFile{Path: "nope.png", Name: "nope", Mime: "image/png", Size: 1})
	msg := a.userMessage("jzk", text, true)
	if len(msg.UserInputMultiContent) != 0 || !strings.Contains(msg.Content, "[[file:nope.png|") {
		t.Fatalf("读不到图应回退纯文本并保留标记: %+v", msg)
	}
}

func TestUserMessageRejectsEscape(t *testing.T) {
	a := &Agent{workspaceRoot: t.TempDir()}
	text := "坏\n" + storage.BuildFileMarker(storage.UploadedFile{Path: "../etc/passwd", Name: "p", Mime: "image/png", Size: 1})
	msg := a.userMessage("jzk", text, true)
	if len(msg.UserInputMultiContent) != 0 {
		t.Fatalf("逃逸路径不该附图: %+v", msg)
	}
}
