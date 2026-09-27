package portal

import (
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// EnableTLS 给门户开 HTTPS（Let's Encrypt / autocert）：证书按需自动签发、自动续期，
// 缓存在 cacheDir（重启不重签）。ACME 的 HTTP-01 验证走 80 端口，
// 所以 80 上的 handler 必须用 HTTPHandlerForACME 包一层。
func (s *Server) EnableTLS(domain, cacheDir, email string) {
	s.acme = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(cacheDir),
		Email:      email,
	}
	s.tlsDomain = domain
}

// HTTPHandlerForACME 包装 80 端口的 handler：/.well-known/acme-challenge/ 交给 autocert，
// 其它路径原样透传。没启用 TLS 时直接返回原 handler。
func (s *Server) HTTPHandlerForACME(h http.Handler) http.Handler {
	if s.acme == nil {
		return h
	}
	return s.acme.HTTPHandler(h)
}

// ServeTLS 在 addr（默认 :443）上跑门户；证书来自 autocert（首次握手时申请）。
func (s *Server) ServeTLS(addr string) error {
	if s.acme == nil {
		return errors.New("tls 未启用")
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
		TLSConfig: &tls.Config{
			GetCertificate: s.acme.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.tlsSrv = srv
	s.log.Info("portal https listening", "addr", addr, "domain", s.tlsDomain)
	err := srv.ListenAndServeTLS("", "")
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
