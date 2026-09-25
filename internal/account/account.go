// Package account 是 Portal 的统一账号层（网页/游戏/未来其它应用共用）。
//
// 设计要点（见 MINE_PORTAL_DESIGN.md §5）：
//   - 名字即账号，无密码；Username 是登录名且不可变，ID 是唯一权威标识；
//   - token 明文只发给客户端，库里只存 sha256；
//   - 管理员权限只以 config.web.adminUsers 为准，users.is_admin 只是落库镜像；
//   - 旧版 tokens.json 在启动时惰性导入（同 token 继续有效，可回滚）。
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
	"unicode"

	"mineagent/internal/storage"
)

const (
	// MaxSessionsPerUser 单账号最多并存登录设备数，超出淘汰最旧的。
	MaxSessionsPerUser = 5
	// SessionTTL 登录有效期（前端 cookie 也是 30 天）。
	SessionTTL = 30 * 24 * time.Hour
	// legacyImportTTL 旧 token 导入后的有效期（旧系统里不过期，给足余量）。
	legacyImportTTL = 180 * 24 * time.Hour
)

type Service struct {
	store      *storage.Store
	log        *slog.Logger
	allowUsers []string
	adminUsers []string
	ttl        time.Duration
}

func New(store *storage.Store, log *slog.Logger, allowUsers, adminUsers []string) *Service {
	return &Service{
		store:      store,
		log:        log,
		allowUsers: allowUsers,
		adminUsers: adminUsers,
		ttl:        SessionTTL,
	}
}

// ValidName 校验账号名：1-24 字符，各语言字母数字与 _ - . ，必须字母数字开头。
func ValidName(name string) bool {
	if name == "" || len([]rune(name)) > 24 {
		return false
	}
	for i, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
		case r == '_' || r == '-' || r == '.':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (s *Service) IsAdminName(name string) bool {
	for _, a := range s.adminUsers {
		if a != "" && a == name {
			return true
		}
	}
	return false
}

// Login 登录（或首次访问即注册）：返回用户与明文 token。
func (s *Service) Login(ctx context.Context, name, userAgent, ip string) (*storage.User, string, error) {
	now := time.Now()
	u, err := s.store.EnsureUser(ctx, name, now.UnixMilli())
	if err != nil {
		return nil, "", err
	}
	if err := s.syncAdmin(ctx, u); err != nil {
		return nil, "", err
	}
	tok, err := newToken()
	if err != nil {
		return nil, "", err
	}
	hash := hashToken(tok)
	if err := s.store.CreateAuthSession(ctx, storage.AuthSession{
		UserID:    u.ID,
		TokenHash: hash,
		UserAgent: truncate(userAgent, 200),
		IP:        stripPort(ip),
		CreatedAt: now.UnixMilli(),
		ExpiresAt: now.Add(s.ttl).UnixMilli(),
	}); err != nil {
		return nil, "", err
	}
	_ = s.evictOldSessions(ctx, u.ID)
	_ = s.store.TouchUser(ctx, u.ID, now.UnixMilli())
	return u, tok, nil
}

// CurrentWithSession 同 Current，但附带会话 ID（账号页"退出所有设备"用）。
func (s *Service) CurrentWithSession(ctx context.Context, token string) (*storage.User, int64, bool) {
	if token == "" {
		return nil, 0, false
	}
	u, sid, err := s.store.AuthSessionUser(ctx, hashToken(token), time.Now().UnixMilli())
	if err != nil || u == nil {
		return nil, 0, false
	}
	_ = s.syncAdmin(ctx, u)
	_ = s.store.TouchUser(ctx, u.ID, time.Now().UnixMilli())
	return u, sid, true
}

// RevokeOtherSessions 注销除当前 token 外的所有登录。
func (s *Service) RevokeOtherSessions(ctx context.Context, token string) (int64, error) {
	_, sid, ok := s.CurrentWithSession(ctx, token)
	if !ok {
		return 0, nil
	}
	u, _, err := s.store.AuthSessionUser(ctx, hashToken(token), time.Now().UnixMilli())
	if err != nil || u == nil {
		return 0, err
	}
	return s.store.DeleteOtherAuthSessions(ctx, u.ID, sid)
}

// Current 校验 token 返回用户；无效/过期返回 nil。
func (s *Service) Current(ctx context.Context, token string) *storage.User {
	if token == "" {
		return nil
	}
	u, _, err := s.store.AuthSessionUser(ctx, hashToken(token), time.Now().UnixMilli())
	if err != nil {
		s.log.Warn("auth session lookup", "err", err)
		return nil
	}
	if u == nil {
		return nil
	}
	_ = s.syncAdmin(ctx, u)
	_ = s.store.TouchUser(ctx, u.ID, time.Now().UnixMilli())
	return u
}

// Logout 注销单个 token。
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteAuthSession(ctx, hashToken(token))
}

// ByName 取用户（不存在返回 nil，不创建）。
func (s *Service) ByName(ctx context.Context, name string) (*storage.User, error) {
	return s.store.UserByName(ctx, name)
}

// ByID 取用户。
func (s *Service) ByID(ctx context.Context, id int64) (*storage.User, error) {
	return s.store.UserByID(ctx, id)
}

// syncAdmin 把 config 管理员名单镜像到 users.is_admin（config 是唯一权威）。
func (s *Service) syncAdmin(ctx context.Context, u *storage.User) error {
	want := s.IsAdminName(u.Username)
	if u.IsAdmin == want {
		return nil
	}
	if err := s.store.SetUserAdmin(ctx, u.Username, want); err != nil {
		return err
	}
	u.IsAdmin = want
	return nil
}

// evictOldSessions 超出设备上限时淘汰最久未用的会话。
func (s *Service) evictOldSessions(ctx context.Context, userID int64) error {
	sessions, err := s.store.ListAuthSessions(ctx, userID)
	if err != nil || len(sessions) <= MaxSessionsPerUser {
		return err
	}
	// ListAuthSessions 按 last_seen_at DESC，末尾是最旧的。
	for _, a := range sessions[MaxSessionsPerUser:] {
		_ = s.store.DeleteAuthSessionByID(ctx, a.ID)
	}
	return nil
}

// ImportLegacyTokens 把旧版 data/webui/tokens.json 导入 users/auth_sessions。
// 明文 token 原样入库（只存 hash），旧浏览器不用重新登录；可重复执行（幂等）。
// 导入后保留原文件（回滚旧版本时仍可用）。
func (s *Service) ImportLegacyTokens(ctx context.Context, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err != nil {
		var old map[string]string
		if json.Unmarshal(b, &old) != nil {
			s.log.Warn("legacy tokens 解析失败，跳过导入", "path", path)
			return
		}
		m = make(map[string][]string, len(old))
		for k, v := range old {
			m[k] = []string{v}
		}
	}
	now := time.Now()
	imported := 0
	for name, list := range m {
		if !ValidName(name) || len(list) == 0 {
			continue
		}
		if len(s.allowUsers) > 0 && !contains(s.allowUsers, name) {
			continue
		}
		u, err := s.store.EnsureUser(ctx, name, now.UnixMilli())
		if err != nil {
			s.log.Warn("legacy tokens 建档失败", "name", name, "err", err)
			continue
		}
		_ = s.syncAdmin(ctx, u)
		for _, tok := range list {
			if strings.TrimSpace(tok) == "" {
				continue
			}
			ok, err := s.store.ImportAuthSession(ctx, storage.AuthSession{
				UserID:    u.ID,
				TokenHash: hashToken(tok),
				UserAgent: "legacy-tokens.json",
				CreatedAt: now.UnixMilli(),
				ExpiresAt: now.Add(legacyImportTTL).UnixMilli(),
			})
			if err != nil {
				s.log.Warn("legacy token 导入失败", "name", name, "err", err)
				continue
			}
			if ok {
				imported++
			}
		}
	}
	if imported > 0 {
		s.log.Info("旧版 tokens.json 已导入账号系统", "count", imported, "path", path)
	}
}

// LoginAllowed 名单校验（web.users 非空时只允许名单内名字）。
func (s *Service) LoginAllowed(name string) bool {
	if len(s.allowUsers) == 0 {
		return true
	}
	return contains(s.allowUsers, name)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// stripPort 去掉 RemoteAddr 的端口（IPv6 形如 [::1]:1234）。
func stripPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
