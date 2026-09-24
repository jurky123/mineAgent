package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func testAPI(t *testing.T, handler http.Handler) (*API, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		m["_path"] = r.URL.Path
		m["_method"] = r.Method
		requests = append(requests, m)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	tokens := &TokenSource{token: "tok", apiBase: srv.URL,
		client: srv.Client()}
	tokens.expiresAt = time.Now().Add(time.Hour)
	api := &API{apiBase: srv.URL, tokens: tokens,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: srv.Client()}
	// Token() 直接返回缓存 token，不走网络。
	return api, &requests
}

func okHandler(resp string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	})
}

func TestSendTextAndMarkdown(t *testing.T) {
	api, reqs := testAPI(t, okHandler(`{"id":"mid","timestamp":"2026-01-01T00:00:00+08:00"}`))
	ctx := context.Background()
	if err := api.SendC2C(ctx, "U1", "hi", "MSG1", 1); err != nil {
		t.Fatal(err)
	}
	if err := api.SendC2CMarkdown(ctx, "U1", "# 标题\n正文", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := api.SendGroupMarkdown(ctx, "G1", "# 群标题", "MSG2", 1); err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 3 {
		t.Fatalf("reqs = %d", len(*reqs))
	}
	r0 := (*reqs)[0]
	if r0["_path"] != "/v2/users/U1/messages" || r0["msg_type"] != float64(0) || r0["content"] != "hi" {
		t.Fatalf("r0 = %+v", r0)
	}
	r1 := (*reqs)[1]
	if r1["msg_type"] != float64(2) {
		t.Fatalf("r1 = %+v", r1)
	}
	md, _ := r1["markdown"].(map[string]any)
	if md["content"] != "# 标题\n正文" {
		t.Fatalf("md = %+v", md)
	}
	if _, has := r1["content"]; has {
		t.Fatalf("markdown 请求不应带 content: %+v", r1)
	}
	r2 := (*reqs)[2]
	if r2["_path"] != "/v2/groups/G1/messages" || r2["msg_id"] != "MSG2" {
		t.Fatalf("r2 = %+v", r2)
	}
}

func TestSendError(t *testing.T) {
	api, _ := testAPI(t, okHandler(`{"err_code":40034005,"message":"回复消息msg_id已过期"}`))
	if err := api.SendC2C(context.Background(), "U1", "hi", "OLD", 1); err == nil {
		t.Fatal("want error on err_code != 0")
	} else if !strings.Contains(err.Error(), "40034005") {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadURLValidation(t *testing.T) {
	api, reqs := testAPI(t, okHandler(`{"file_info":"FI123","ttl":300}`))
	ctx := context.Background()
	fi, err := api.UploadC2CImageURL(ctx, "U1", "https://example.com/a.png")
	if err != nil || fi != "FI123" {
		t.Fatalf("fi=%q err=%v", fi, err)
	}
	if (*reqs)[0]["file_type"] != float64(1) {
		t.Fatalf("req = %+v", (*reqs)[0])
	}
	if _, err := api.UploadGroupImageURL(ctx, "G1", "ftp://example.com/a.png"); err == nil {
		t.Fatal("ftp URL should be rejected")
	}
}

func TestUploadLocalValidation(t *testing.T) {
	api, _ := testAPI(t, okHandler(`{}`))
	ctx := context.Background()
	// 绝对路径/逃逸拒绝（还没到网络层）。
	if _, err := api.UploadC2CLocalImage(ctx, "U1", "workspace", "/etc/passwd"); err == nil {
		t.Fatal("abs path should be rejected")
	}
	if _, err := api.UploadGroupLocalImage(ctx, "G1", "workspace", "../secret.png"); err == nil {
		t.Fatal("escape should be rejected")
	}
}

func TestUploadLocalImageFlow(t *testing.T) {
	dir := t.TempDir()
	// 最小合法 PNG（1x1），64 字节。
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xff, 0xff, 0x3f,
		0x00, 0x05, 0xfe, 0x02, 0xfe, 0xdc, 0xcc, 0x59,
		0xe7, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82}
	if err := writeFile(dir+"/t.png", png); err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload_prepare"):
			// block_size 给 1MB，64 字节文件只 1 片。
			// 注意：真实网关 parts 的 index 从 1 开始（不是文档的从 0 开始），
			// 单测用 index:1 覆盖这个分支。
			fmt.Fprintf(w, `{"upload_id":"up1","block_size":"1048576","parts":[{"index":1,"presigned_url":"http://%s/put0","block_size":"68"}],"upload_config":{"concurrency":1,"retry_timeout":300,"retry_delay":1}}`, r.Host)
		case strings.HasSuffix(r.URL.Path, "/upload_part_finish"):
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/files"):
			fmt.Fprint(w, `{"file_info":"FI_LOCAL","ttl":300}`)
		case r.URL.Path == "/put0":
			body, _ := io.ReadAll(r.Body)
			if len(body) != len(png) {
				t.Errorf("chunk = %d bytes, want %d", len(body), len(png))
			}
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	tokens := &TokenSource{token: "tok", apiBase: srv.URL, client: srv.Client()}
	tokens.expiresAt = time.Now().Add(time.Hour)
	api := &API{apiBase: srv.URL, tokens: tokens,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: srv.Client()}

	fi, err := api.UploadC2CLocalImage(context.Background(), "U1", dir, "t.png")
	if err != nil {
		t.Fatal(err)
	}
	if fi != "FI_LOCAL" {
		t.Fatalf("fi = %q", fi)
	}
	// 路径顺序：prepare -> part_finish -> files（PUT 走直连不经过 paths？PUT 也进 paths）。
	joined := strings.Join(paths, ",")
	for _, want := range []string{"upload_prepare", "upload_part_finish", "/files", "/put0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("paths = %v, want %s", paths, want)
		}
	}
	// 非图片后缀拒绝。
	if err := writeFile(dir+"/t.txt", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := api.UploadC2CLocalImage(context.Background(), "U1", dir, "t.txt"); err == nil {
		t.Fatal("txt should be rejected")
	}
}
