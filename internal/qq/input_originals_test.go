package qq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/config"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

func animatedInput(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	first, second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette), image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(0, 0, 1)
	var raw bytes.Buffer
	if err := gif.EncodeAll(&raw, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{3, 7}, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestImageOriginalRetainsAnimationAndSurvivesTaskCleanup(t *testing.T) {
	raw := animatedInput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	workspace := t.TempDir()
	b := &inputBundle{client: server.Client(), workspaceRoot: workspace}
	defer b.cleanup()
	doc := inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: server.URL + "?private=hidden", Filename: "../../spoof.png"}, Source: "引用表情"}}}
	ctx, msg, err := b.prepare(context.Background(), doc, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.UserInputMultiContent) != 2 {
		t.Fatal("visual input was removed")
	}
	img := msg.UserInputMultiContent[1].Image
	visual, err := base64.StdEncoding.DecodeString(*img.Base64Data)
	if err != nil || !bytes.Equal(visual, raw) {
		t.Fatal("visual bytes changed")
	}
	file := b.files[0]
	if !strings.HasSuffix(file.Path, ".gif") || strings.Contains(file.Path, "spoof") || filepath.IsAbs(file.Path) {
		t.Fatal("unsafe path or incorrect sniffed extension", file.Path)
	}
	saved, err := os.ReadFile(filepath.Join(workspace, file.Path))
	if err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("original was transcoded or lost", err)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(saved))
	if err != nil || len(animation.Image) != 2 || animation.Delay[1] != 7 {
		t.Fatal("GIF frames or timing lost")
	}
	for path, mode := range map[string]os.FileMode{filepath.Dir(filepath.Join(workspace, file.Path)): 0700, filepath.Join(workspace, file.Path): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("unsafe original permissions", err)
		}
	}
	text := msg.UserInputMultiContent[0].Text
	if !strings.Contains(text, file.Path) || strings.Contains(text, "private=hidden") {
		t.Fatal("original path missing or source URL leaked")
	}
	meta, err := (&readAttachmentTool{}).InvokableRun(ctx, `{"id":1}`)
	if err != nil || !strings.Contains(meta, file.Path) || !strings.Contains(meta, file.SHA256) {
		t.Fatal("original metadata unavailable", err)
	}
	b.cleanup()
	if _, err := os.Stat(filepath.Join(workspace, file.Path)); err != nil {
		t.Fatal("task cleanup removed persistent original")
	}
	// Resume is memoized and another task cannot overwrite an existing original.
	_, _, err = b.prepare(context.Background(), doc, "owner")
	if err != nil || b.files[0].Path != file.Path {
		t.Fatal("resume duplicated the original")
	}
	other := &inputBundle{workspaceRoot: workspace}
	path, err := other.saveImageOriginal(context.Background(), 1, raw, "image/gif")
	if err != nil || path == file.Path {
		t.Fatal("different tasks share an original path", err)
	}
}

func TestOriginalFailureAndVisualLimitAreIndependent(t *testing.T) {
	for _, mode := range []string{"non-admin", "blocked-workspace", "over-visual-limit"} {
		t.Run(mode, func(t *testing.T) {
			raw := animatedInput(t)
			if mode == "over-visual-limit" {
				raw = append(raw, make([]byte, 4<<20)...)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
			defer server.Close()
			b := &inputBundle{client: server.Client()}
			defer b.cleanup()
			if mode != "non-admin" {
				b.workspaceRoot = t.TempDir()
			}
			if mode == "blocked-workspace" {
				b.workspaceRoot = filepath.Join(b.workspaceRoot, "file")
				if err := os.WriteFile(b.workspaceRoot, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, msg, err := b.prepare(context.Background(), inputDocument{Attachments: []inputAttachment{{Attachment: Attachment{URL: server.URL, Filename: "sticker.gif"}}}}, "owner")
			if err != nil {
				t.Fatal(err)
			}
			file := b.files[0]
			if mode == "over-visual-limit" {
				if len(msg.UserInputMultiContent) != 0 || file.Path == "" || !strings.Contains(file.Status, "4MB") {
					t.Fatal("visual limit discarded file copy")
				}
			} else if len(msg.UserInputMultiContent) != 2 || file.Path != "" || file.FileStatus == "" {
				t.Fatal("copy failure or admin gate broke vision")
			}
		})
	}
}

func TestOriginalDirectoryRejectsSymlinkEscape(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "qq-inputs")); err != nil {
		t.Fatal(err)
	}
	b := &inputBundle{workspaceRoot: workspace}
	if path, err := b.saveImageOriginal(context.Background(), 1, animatedInput(t), "image/gif"); err == nil || path != "" {
		t.Fatal("symlink export escaped workspace")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside directory changed")
	}
}

func TestOriginalCanBeProcessedByWorkspaceSandbox(t *testing.T) {
	workspace := t.TempDir()
	b := &inputBundle{workspaceRoot: workspace}
	path, err := b.saveImageOriginal(context.Background(), 1, animatedInput(t), "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.Default().Workspace
	cfg.Root = workspace
	ws := tools.NewWorkspace(cfg, nil, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tl := range ws.Tools() {
		info, _ := tl.Info(context.Background())
		if info.Name != "workspace_exec" {
			continue
		}
		command := "python3 -c 'from PIL import Image; im=Image.open(\"" + path + "\"); print(im.n_frames)'"
		args, _ := json.Marshal(map[string]string{"command": command})
		// The existing administrator gate and real bwrap sandbox remain in place.
		executable := tl.(interface {
			InvokableRun(context.Context, string, ...tool.Option) (string, error)
		})
		out, err := executable.InvokableRun(tools.WithQQAdmin(context.Background(), true), string(args))
		if err != nil || !strings.Contains(out, `"exitCode":0`) || !strings.Contains(out, `2\n`) {
			t.Fatalf("original unavailable to sandbox: %s %v", out, err)
		}
	}
}

func TestQuotedImageGivesAdminBothVisionAndPersistentFile(t *testing.T) {
	raw := animatedInput(t)
	for _, mode := range []string{"c2c", "group", "non-admin"} {
		t.Run(mode, func(t *testing.T) {
			files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
			defer files.Close()
			captured := make(chan []byte, 1)
			c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				captured <- body
				fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"image ready"},"finish_reason":"stop"}]}`)
			}))
			c.workspaceRoot = t.TempDir()
			c.cfg.AdminOpenIDs = []string{"ADMIN"}
			c.inputClient = files.Client()
			sender, event, key := "ADMIN", "C2C_MESSAGE_CREATE", "qq:c2c:ADMIN"
			if mode == "non-admin" {
				sender = "READER"
				key = "qq:c2c:READER"
			}
			payload := map[string]any{"id": "M", "author": map[string]string{"user_openid": sender}, "content": "处理这张动图", "message_type": 103, "msg_elements": []any{map[string]any{"attachments": []Attachment{{URL: files.URL + "?secret=hidden", Filename: "sticker.png"}}}}}
			if mode == "group" {
				event = "GROUP_AT_MESSAGE_CREATE"
				key = "qq:group:G"
				payload["group_openid"] = "G"
				payload["author"] = map[string]string{"member_openid": "ADMIN"}
			}
			encoded, _ := json.Marshal(payload)
			c.gw.dispatch(event, "event", encoded)
			waitInputTasks(t, c)
			var model []byte
			select {
			case model = <-captured:
			default:
				t.Fatal("input not delivered to model")
			}
			if !bytes.Contains(model, []byte("data:image/gif;base64,")) || bytes.Contains(model, []byte("secret=hidden")) {
				t.Fatal("vision missing or download URL leaked")
			}
			rows, err := st.RecentMessages(context.Background(), key, 10)
			if err != nil {
				t.Fatal(err)
			}
			var input string
			for _, m := range rows {
				if m.AuthorKind == "player" {
					input = m.Text
				}
			}
			path := regexp.MustCompile(`qq-inputs/[A-Za-z0-9]+/attachment-1\.gif`).FindString(input)
			if mode == "non-admin" {
				if path != "" {
					t.Fatal("ordinary user acquired a workspace file")
				}
				return
			}
			if path == "" || !bytes.Contains(model, []byte(path)) {
				t.Fatal("original path missing in actual model/history")
			}
			saved, err := os.ReadFile(filepath.Join(c.workspaceRoot, path))
			if err != nil || !bytes.Equal(saved, raw) {
				t.Fatal("completed task did not retain original", err)
			}
		})
	}
}
