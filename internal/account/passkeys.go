package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"mineagent/internal/storage"
)

const ceremonyCookie = "mineagent_ceremony"

type passkeyUser struct {
	user        *storage.User
	handle      []byte
	credentials []webauthn.Credential
	original    map[string][]byte
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.user.Username }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.user.DisplayName }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type ceremony struct {
	session                 webauthn.SessionData
	userID                  int64
	grantHash, kind, origin string
	expires                 time.Time
}
type Passkeys struct {
	service *Service
	web     *webauthn.WebAuthn
	mu      sync.Mutex
	pending map[string]ceremony
	limits  map[string]beginLimit
}

type beginLimit struct {
	started time.Time
	count   int
}

func (p *Passkeys) allowBegin(remote string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, limit := range p.limits {
		if now.Sub(limit.started) >= time.Minute {
			delete(p.limits, key)
		}
	}
	key := stripPort(remote) // Never trust a client-supplied forwarding header.
	limit, exists := p.limits[key]
	if !exists {
		if len(p.limits) >= 2048 {
			return false
		}
		limit.started = now
	}
	if limit.count >= 64 {
		return false
	}
	limit.count++
	p.limits[key] = limit
	return true
}

// NewPasskeys never infers a trusted origin from request headers.
func NewPasskeys(s *Service, rpID string, origins []string) (*Passkeys, error) {
	if rpID == "" || len(origins) == 0 {
		return nil, errors.New("configure a Passkey RP ID and HTTPS origins")
	}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !(u.Hostname() == rpID || strings.HasSuffix(u.Hostname(), "."+rpID)) {
			return nil, errors.New("invalid Passkey origin")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "localhost") {
			return nil, errors.New("Passkey requires HTTPS (localhost allowed for development)")
		}
	}
	w, err := webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "zkun", RPOrigins: origins, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
	if err != nil {
		return nil, err
	}
	return &Passkeys{service: s, web: w, pending: make(map[string]ceremony), limits: make(map[string]beginLimit)}, nil
}

// IssueEnrollment is an operator-only CLI operation, never exposed as an HTTP endpoint.
func (s *Service) IssueEnrollment(ctx context.Context, name string, recovery bool) (string, error) {
	if !ValidName(name) || !s.LoginAllowed(name) {
		return "", errors.New("invalid or disallowed account")
	}
	u, err := s.store.UserByName(ctx, name)
	if err != nil {
		return "", err
	}
	if u == nil {
		if recovery {
			return "", errors.New("account does not exist")
		}
		u, err = s.store.EnsureUser(ctx, name, time.Now().UnixMilli())
		if err != nil {
			return "", err
		}
	}
	code, err := newToken()
	if err != nil {
		return "", err
	}
	handle := make([]byte, 32)
	if _, err = rand.Read(handle); err != nil {
		return "", err
	}
	if err = s.store.CreatePasskeyGrant(ctx, u.ID, handle, hashToken(code), time.Now().Add(24*time.Hour).UnixMilli(), recovery); err != nil {
		return "", err
	}
	return code, nil
}
func (p *Passkeys) user(ctx context.Context, id int64) (*passkeyUser, error) {
	u, err := p.service.store.UserByID(ctx, id)
	if err != nil || u == nil {
		return nil, errors.New("unknown user")
	}
	if !p.service.LoginAllowed(u.Username) {
		return nil, errors.New("account not allowed")
	}
	handle, err := p.service.store.PasskeyHandle(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, err := p.service.store.PasskeyCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &passkeyUser{user: u, handle: handle, original: make(map[string][]byte)}
	for _, b := range raw {
		var c webauthn.Credential
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out.credentials = append(out.credentials, c)
		out.original[string(c.ID)] = append([]byte(nil), b...)
	}
	return out, nil
}
func authJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func authError(w http.ResponseWriter, status int, message string) {
	authJSON(w, status, map[string]string{"error": message})
}
func (p *Passkeys) cookie(w http.ResponseWriter, origin, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: value, Path: "/api/auth/passkey", MaxAge: maxAge, HttpOnly: true, Secure: strings.HasPrefix(origin, "https://"), SameSite: http.SameSiteStrictMode})
}
func (p *Passkeys) put(w http.ResponseWriter, c ceremony) error {
	id, err := newToken()
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.pending {
		if time.Now().After(v.expires) {
			delete(p.pending, k)
		}
	}
	if len(p.pending) >= 1024 {
		return errors.New("too many pending ceremonies")
	}
	c.expires = time.Now().Add(5 * time.Minute)
	p.pending[hashToken(id)] = c
	p.cookie(w, c.origin, id, 300)
	return nil
}
func (p *Passkeys) take(w http.ResponseWriter, r *http.Request, kind string) (ceremony, bool) {
	cookie, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	p.mu.Lock()
	c, ok := p.pending[hashToken(cookie.Value)]
	delete(p.pending, hashToken(cookie.Value))
	p.mu.Unlock()
	p.cookie(w, c.origin, "", -1)
	return c, ok && c.kind == kind && time.Now().Before(c.expires) && r.Header.Get("Origin") == c.origin
}
func (p *Passkeys) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		authError(w, 405, "只支持 POST")
		return
	}
	origin := r.Header.Get("Origin")
	trusted := false
	for _, o := range p.web.Config.RPOrigins {
		if origin == o {
			trusted = true
		}
	}
	if !trusted {
		authError(w, 403, "请从本站安全页面重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if strings.HasSuffix(r.URL.Path, "/begin") && !p.allowBegin(r.RemoteAddr) {
		authError(w, 429, "尝试较多，请一分钟后重试")
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/auth/passkey/") {
	case "login/begin":
		options, session, err := p.web.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			authError(w, 500, "暂时无法开始登录")
			return
		}
		if p.put(w, ceremony{session: *session, kind: "login", origin: origin}) != nil {
			authError(w, 429, "请求较多，请稍后重试")
			return
		}
		authJSON(w, 200, options)
	case "login/finish":
		p.finishLogin(w, r)
	case "register/begin":
		p.beginRegistration(w, r, origin)
	case "register/finish":
		p.finishRegistration(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (p *Passkeys) beginRegistration(w http.ResponseWriter, r *http.Request, origin string) {
	var request struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		authError(w, 400, "请输入激活码或恢复码")
		return
	}
	code := strings.TrimSpace(request.Code)
	if len(code) < 32 || len(code) > 128 {
		authError(w, 400, "激活码或恢复码无效或已过期")
		return
	}
	grant, err := p.service.store.PasskeyGrant(r.Context(), hashToken(code))
	if err != nil {
		authError(w, 400, "激活码或恢复码无效或已过期")
		return
	}
	u, err := p.user(r.Context(), grant.UserID)
	if err != nil {
		authError(w, 400, "暂时无法激活此账号")
		return
	}
	if len(u.credentials) > 0 && !grant.Recovery {
		authError(w, 400, "请使用恢复流程")
		return
	}
	var exclusions []protocol.CredentialDescriptor
	for _, c := range u.credentials {
		exclusions = append(exclusions, c.Descriptor())
	}
	options, session, err := p.web.BeginRegistration(u, webauthn.WithExclusions(exclusions))
	if err != nil {
		authError(w, 500, "暂时无法创建 Passkey")
		return
	}
	if p.put(w, ceremony{session: *session, userID: u.user.ID, grantHash: hashToken(code), kind: "register", origin: origin}) != nil {
		authError(w, 429, "请求较多，请稍后重试")
		return
	}
	authJSON(w, 200, map[string]any{"publicKey": options.Response, "name": u.user.DisplayName, "recovery": grant.Recovery})
}
func (p *Passkeys) finishRegistration(w http.ResponseWriter, r *http.Request) {
	c, ok := p.take(w, r, "register")
	if !ok {
		authError(w, 400, "验证已失效，请重新开始")
		return
	}
	u, err := p.user(r.Context(), c.userID)
	if err != nil {
		authError(w, 400, "暂时无法激活此账号")
		return
	}
	credential, err := p.web.FinishRegistration(u, c.session, r)
	if err != nil {
		authError(w, 400, "Passkey 验证失败，请重试")
		return
	}
	codes, hashes := make([]string, 8), make([]string, 8)
	for i := range codes {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			authError(w, 500, "暂时无法生成恢复码")
			return
		}
		codes[i] = hex.EncodeToString(b)
		hashes[i] = hashToken(codes[i])
	}
	data, err := json.Marshal(credential)
	if err != nil {
		authError(w, 500, "暂时无法保存 Passkey")
		return
	}
	if err = p.service.store.CommitPasskeyEnrollment(r.Context(), c.grantHash, u.user.ID, credential.ID, data, hashes); err != nil {
		authError(w, 400, "激活凭据已过期或使用，请重新开始")
		return
	}
	// Do not log in until recovery codes have been presented; normal Passkey login follows.
	authJSON(w, 200, map[string]any{"ok": true, "name": u.user.DisplayName, "recoveryCodes": codes})
}
func (p *Passkeys) finishLogin(w http.ResponseWriter, r *http.Request) {
	c, ok := p.take(w, r, "login")
	if !ok {
		authError(w, 400, "验证已失效，请重新开始")
		return
	}
	var owner *passkeyUser
	credential, err := p.web.FinishDiscoverableLogin(func(rawID, handle []byte) (webauthn.User, error) {
		id, err := p.service.store.PasskeyOwner(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		owner, err = p.user(r.Context(), id)
		if err != nil || !bytes.Equal(owner.handle, handle) {
			return nil, errors.New("invalid credential owner")
		}
		return owner, nil
	}, c.session, r)
	if err != nil || owner == nil || credential == nil || credential.Authenticator.CloneWarning {
		authError(w, 400, "Passkey 登录失败，请重试或使用恢复码")
		return
	}
	data, err := json.Marshal(credential)
	if err != nil {
		authError(w, 500, "登录失败，请稍后重试")
		return
	}
	if err = p.service.store.UpdatePasskeyCredential(r.Context(), credential.ID, owner.user.ID, owner.original[string(credential.ID)], data); err != nil {
		authError(w, 400, "Passkey 已被撤销，请重新登录")
		return
	}
	u, token, err := p.service.issueSession(r.Context(), owner.user, credential.ID, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		authError(w, 500, "登录失败，请稍后重试")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mineagent_token", Value: token, Path: "/", MaxAge: int(SessionTTL.Seconds()), SameSite: http.SameSiteLaxMode, HttpOnly: true, Secure: strings.HasPrefix(c.origin, "https://")})
	authJSON(w, 200, map[string]any{"name": u.Username, "id": u.ID, "admin": u.IsAdmin, "token": token})
}
