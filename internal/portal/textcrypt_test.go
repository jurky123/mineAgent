package portal

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTextCryptAuthorizationAndCurrentDayOnly(t *testing.T) {
	s, _ := downloadFixture(t)
	call := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.handleTextCrypt(w, r)
		return w
	}
	if call("/api/textcrypt/admin", `{"label":"test"}`, "").Code != 401 {
		t.Fatal("anonymous admin accepted")
	}
	if call("/api/textcrypt/admin", `{"label":"test"}`, "fixture-reader").Code != 403 {
		t.Fatal("non-admin accepted")
	}
	w := call("/api/textcrypt/admin", `{"label":"test"}`, "fixture-admin")
	if w.Code != 201 {
		t.Fatal("batch creation failed")
	}
	var batch struct{ Group, Token string }
	if json.Unmarshal(w.Body.Bytes(), &batch) != nil {
		t.Fatal("bad batch response")
	}
	body := `{"group":"` + batch.Group + `"}`
	if call("/api/textcrypt/key", body, "").Code != 403 {
		t.Fatal("unauthorized key response")
	}
	if call("/api/textcrypt/key", body, "fixture-admin").Code != 403 {
		t.Fatal("site session used as program credential")
	}
	if call("/api/textcrypt/key", `{"group":"`+batch.Group+`","day":"2020-01-01"}`, batch.Token).Code != 400 {
		t.Fatal("historical date accepted")
	}
	w = call("/api/textcrypt/key", body, batch.Token)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("daily key request failed")
	}
	var daily struct{ Day, Key string }
	if json.Unmarshal(w.Body.Bytes(), &daily) != nil || daily.Day == "" || daily.Key == "" {
		t.Fatal("missing daily parameters")
	}
	r := httptest.NewRequest("GET", "/api/textcrypt/admin", nil)
	r.Header.Set("Authorization", "Bearer fixture-admin")
	w = httptest.NewRecorder()
	s.handleTextCrypt(w, r)
	if strings.Contains(w.Body.String(), daily.Key) || strings.Contains(w.Body.String(), batch.Token) {
		t.Fatal("management listing leaked secret")
	}
	if call("/api/textcrypt/admin/revoke", body, "fixture-admin").Code != 200 || call("/api/textcrypt/key", body, batch.Token).Code != 403 {
		t.Fatal("revocation failed")
	}
	s.tlsDomain = "example.com"
	if call("/api/textcrypt/key", body, batch.Token).Code != 426 {
		t.Fatal("plaintext key request accepted")
	}
}
