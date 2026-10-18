package portal

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mineagent/internal/storage"
	"mineagent/internal/textcrypt"
)

func (s *Server) handleTextCrypt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if s.tlsDomain != "" && r.TLS == nil {
		writeJSONError(w, 426, "请使用 HTTPS")
		return
	}
	if r.URL.RawQuery != "" {
		writeJSONError(w, 400, "不接受查询参数")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme {
			writeJSONError(w, 403, "不允许跨站请求")
			return
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSONError(w, 403, "不允许跨站请求")
		return
	}
	if r.URL.Path == "/api/textcrypt/key" {
		if r.Method != "POST" {
			writeJSONError(w, 405, "只支持 POST")
			return
		}
		var req struct {
			Group string `json:"group"`
		}
		if !decodeTextCrypt(w, r, &req) {
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || len(auth) > 128 {
			writeJSONError(w, 403, "程序未授权或已停用")
			return
		}
		result, err := s.textKeys.Current(req.Group, strings.TrimPrefix(auth, "Bearer "), time.Now())
		if err != nil {
			s.textCryptError(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	s.requireAdmin(w, r, func(w http.ResponseWriter, r *http.Request, _ *storage.User) {
		switch r.URL.Path {
		case "/api/textcrypt/admin":
			if r.Method == "GET" {
				list, err := s.textKeys.List()
				if err != nil {
					s.textCryptError(w, err)
					return
				}
				writeJSON(w, 200, map[string]any{"groups": list})
				return
			}
			if r.Method != "POST" {
				writeJSONError(w, 405, "只支持 GET / POST")
				return
			}
			var req struct {
				Label string `json:"label"`
			}
			if !decodeTextCrypt(w, r, &req) {
				return
			}
			id, token, err := s.textKeys.Issue(req.Label)
			if err != nil {
				s.textCryptError(w, err)
				return
			}
			writeJSON(w, 201, map[string]any{"group": id, "token": token})
		case "/api/textcrypt/admin/revoke", "/api/textcrypt/admin/delete-key":
			if r.Method != "POST" {
				writeJSONError(w, 405, "只支持 POST")
				return
			}
			var req struct {
				Group string `json:"group"`
				Day   string `json:"day"`
			}
			if !decodeTextCrypt(w, r, &req) {
				return
			}
			var err error
			if strings.HasSuffix(r.URL.Path, "/revoke") {
				err = s.textKeys.Revoke(req.Group)
			} else {
				err = s.textKeys.Delete(req.Group, req.Day)
			}
			if err != nil {
				s.textCryptError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	})
}

func decodeTextCrypt(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		writeJSONError(w, 400, "请求格式无效")
		return false
	}
	return true
}
func (s *Server) textCryptError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, textcrypt.ErrDenied):
		writeJSONError(w, 403, err.Error())
	case errors.Is(err, textcrypt.ErrDeleted):
		writeJSONError(w, 410, err.Error())
	case errors.Is(err, textcrypt.ErrMissing):
		writeJSONError(w, 404, err.Error())
	default:
		s.log.Error("textcrypt vault operation failed")
		writeJSONError(w, 500, "密钥服务暂不可用")
	}
}
