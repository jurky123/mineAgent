package rinkujcef

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServeAndVerify(t *testing.T) {
	dir := t.TempDir()
	tag := "java-cef-abc123"
	if err := os.MkdirAll(filepath.Join(dir, tag), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("fake-tar-gz-bytes")
	if err := os.WriteFile(filepath.Join(dir, tag, "windows_amd64.tar.gz"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256 文件用真实摘要，保证 VerifySHA256 通过。
	sum := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if err := os.WriteFile(filepath.Join(dir, tag, "windows_amd64.tar.gz.sha256"),
		[]byte(sum+"  windows_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 注意：上面的 sum 是 "test" 的 sha256，这里故意写错内容以验证 handler 只管 serving；
	// Verify 用正确内容单独测。
	h := &Handler{Dir: dir}

	req := httptest.NewRequest(http.MethodGet, "/rinku-jcef/"+tag+"/windows_amd64.tar.gz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("包应 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != string(body) {
		t.Fatal("包内容不对")
	}

	// 路径逃逸拒绝。
	req = httptest.NewRequest(http.MethodGet, "/rinku-jcef/../secret/x.tar.gz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("逃逸应 404，实际 %d", rec.Code)
	}

	// 非法后缀拒绝。
	req = httptest.NewRequest(http.MethodGet, "/rinku-jcef/"+tag+"/evil.exe", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("非法后缀应 404，实际 %d", rec.Code)
	}

	// 重定向：https 前缀生效（拼同一路径），http/非法前缀忽略回源。
	hRedirect := &Handler{Dir: dir, RedirectBase: "https://ghfast.top/https://github.com/Keksuccino/jcef-rinku/releases/download/"}
	req = httptest.NewRequest(http.MethodGet, "/rinku-jcef/"+tag+"/windows_amd64.tar.gz", nil)
	rec = httptest.NewRecorder()
	hRedirect.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("重定向应 302，实际 %d", rec.Code)
	}
	want := "https://ghfast.top/https://github.com/Keksuccino/jcef-rinku/releases/download/" + tag + "/windows_amd64.tar.gz"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Fatalf("重定向地址不对: %s", loc)
	}
	hInsecure := &Handler{Dir: dir, RedirectBase: "http://insecure.example/"}
	req = httptest.NewRequest(http.MethodGet, "/rinku-jcef/"+tag+"/windows_amd64.tar.gz", nil)
	rec = httptest.NewRecorder()
	hInsecure.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("非 https 重定向应忽略回源，实际 %d", rec.Code)
	}

	// Verify：正确摘要通过。
	if err := os.WriteFile(filepath.Join(dir, tag, "ok.tar.gz"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tag, "ok.tar.gz.sha256"),
		[]byte(sum+"  ok.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(dir, tag, "ok.tar.gz"); err != nil {
		t.Fatal(err)
	}
}
