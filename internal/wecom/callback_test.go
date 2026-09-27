package wecom

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallbackFallbackRouting(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewCallbackServer(Config{Port: 8081, Token: "tok"}, log, nil)
	fallbackHit := ""
	s.WithFallback(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	h := s.handler()

	// 非 /wecom 路径 → 门户 fallback
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent || fallbackHit != "/" {
		t.Fatalf("fallback 未生效: code=%d path=%q", rec.Code, fallbackHit)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/games/chess", nil))
	if rec.Code != http.StatusNoContent || fallbackHit != "/games/chess" {
		t.Fatalf("子路径未走 fallback: code=%d path=%q", rec.Code, fallbackHit)
	}

	// /wecom → 企微回调（缺签名应 400，而不是 204）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("/wecom 应走企微回调（400 missing msg_signature），得到 %d", rec.Code)
	}

	// 没设 fallback 时：其它路径维持 404
	s2 := NewCallbackServer(Config{Port: 8082, Token: "tok"}, log, nil)
	rec = httptest.NewRecorder()
	s2.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("没有 fallback 时应 404，得到 %d", rec.Code)
	}
}
