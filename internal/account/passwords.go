package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"mineagent/internal/storage"
)

const argonPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"

var passwordWorkers = make(chan struct{}, 2)

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return argonPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func checkPassword(hash, password string) bool {
	if !strings.HasPrefix(hash, argonPrefix) {
		return false
	}
	fields := strings.Split(strings.TrimPrefix(hash, argonPrefix), "$")
	if len(fields) != 2 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[0])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(fields[1])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

type Passwords struct {
	OnAdminLogin func(string) error
	service      *Service
	origins      []string
	mu           sync.Mutex
	limits       map[string]beginLimit
}

func NewPasswords(s *Service, origins []string) *Passwords {
	return &Passwords{service: s, origins: append([]string(nil), origins...), limits: make(map[string]beginLimit)}
}
func (p *Passwords) allowed(remote, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, l := range p.limits {
		if now.Sub(l.started) >= time.Minute {
			delete(p.limits, key)
		}
	}
	if len(p.limits) > 2048 {
		return false
	}
	for key, max := range map[string]int{"ip:" + stripPort(remote): 32, "user:" + name: 12} {
		l := p.limits[key]
		if l.count >= max {
			return false
		}
		if l.started.IsZero() {
			l.started = now
		}
		l.count++
		p.limits[key] = l
	}
	return true
}
func (p *Passwords) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		authError(w, 405, "只支持 POST")
		return
	}
	origin := r.Header.Get("Origin")
	trusted := false
	for _, o := range p.origins {
		if origin == o && (strings.HasPrefix(o, "https://") || strings.HasPrefix(o, "http://localhost:")) {
			trusted = true
		}
	}
	if !trusted {
		authError(w, 403, "请从本站 HTTPS 页面登录")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		authError(w, 400, "登录参数无效")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !ValidName(req.Name) || !p.service.LoginAllowed(req.Name) {
		authError(w, 400, "账号名无效或不在允许名单内")
		return
	}
	if !p.allowed(r.RemoteAddr, req.Name) {
		authError(w, 429, "尝试较多，请一分钟后重试")
		return
	}
	ctx := r.Context()
	u, err := p.service.store.UserByName(ctx, req.Name)
	if err != nil {
		authError(w, 500, "暂时无法读取账号")
		return
	}
	hash := ""
	var credentials [][]byte
	if u != nil {
		hash, err = p.service.store.PasswordHash(ctx, u.ID)
		if err != nil {
			authError(w, 500, "暂时无法读取账号")
			return
		}
		credentials, err = p.service.store.PasskeyCredentials(ctx, u.ID)
		if err != nil {
			authError(w, 500, "暂时无法读取账号")
			return
		}
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/auth/password/")
	if action == "status" {
		authJSON(w, 200, map[string]any{"needsSetup": hash == "" && len(credentials) == 0, "passkeyOnly": hash == "" && len(credentials) > 0})
		return
	}
	if action != "setup" && action != "login" {
		http.NotFound(w, r)
		return
	}
	if !utf8.ValidString(req.Password) || len(req.Password) > 512 || utf8.RuneCountInString(req.Password) < 12 || utf8.RuneCountInString(req.Password) > 128 {
		authError(w, 400, "密码须为 12–128 个字符")
		return
	}
	select {
	case passwordWorkers <- struct{}{}:
		defer func() { <-passwordWorkers }()
	default:
		authError(w, 429, "登录请求较多，请稍后重试")
		return
	}
	if action == "setup" {
		if req.Password != req.Confirm {
			authError(w, 400, "两次输入的密码不一致")
			return
		}
		if hash != "" || len(credentials) > 0 {
			authError(w, 409, "该账号已受密码或 Passkey 保护，请返回登录")
			return
		}
		encoded, err := hashPassword(req.Password)
		if err != nil {
			authError(w, 500, "暂时无法设置密码")
			return
		}
		if u == nil {
			u, err = p.service.store.EnsureUser(ctx, req.Name, time.Now().UnixMilli())
			if err != nil {
				authError(w, 500, "暂时无法创建账号")
				return
			}
		}
		if err = p.service.store.SetInitialPassword(ctx, u.ID, encoded); err != nil {
			if errors.Is(err, storage.ErrPasswordAlreadySet) {
				authError(w, 409, "账号已设置密码，请返回登录")
			} else {
				authError(w, 500, "暂时无法设置密码")
			}
			return
		}
		hash = encoded
	} else if u == nil || hash == "" || !checkPassword(hash, req.Password) {
		if hash == "" {
			_, _ = hashPassword(req.Password)
		}
		authError(w, 401, "账号或密码不正确")
		return
	}
	user, token, err := p.service.issueVerifiedSession(ctx, u, r.UserAgent(), r.RemoteAddr, func(tokenHash string) error { return p.service.store.MarkPasswordSession(ctx, tokenHash, hash) })
	if err != nil {
		authError(w, 500, "登录失败，请稍后重试")
		return
	}
	if user.IsAdmin && p.OnAdminLogin != nil {
		if err := p.OnAdminLogin(token); err != nil {
			p.service.log.Warn("deployment credential refresh failed")
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "mineagent_token", Value: token, Path: "/", MaxAge: int(SessionTTL.Seconds()), HttpOnly: true, Secure: strings.HasPrefix(origin, "https://"), SameSite: http.SameSiteLaxMode})
	authJSON(w, 200, map[string]any{"id": user.ID, "name": user.Username, "admin": user.IsAdmin, "token": token})
}

// ResetPassword is local-operator only. It returns the account to the user-authorized first-login setup flow.
func (s *Service) ResetPassword(ctx context.Context, name string) error {
	u, err := s.store.UserByName(ctx, name)
	if err != nil {
		return err
	}
	if u == nil {
		return errors.New("account does not exist")
	}
	return s.store.ResetPassword(ctx, u.ID)
}
