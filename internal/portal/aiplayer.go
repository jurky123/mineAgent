package portal

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Only the independent publishing routes are proxied. MineAgent credentials
// never cross this boundary; the content service keeps its own admin identity.
func mountAIPlayer(mux *http.ServeMux, target string) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		for _, path := range aiPlayerPaths {
			mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "AI 玩家服务地址无效", http.StatusServiceUnavailable)
			})
		}
		return
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("Cookie")
			for _, c := range r.In.Cookies() {
				if c.Name == "__Host-aiplayer_admin" || c.Name == "aiplayer_local_admin" {
					r.Out.AddCookie(c)
				}
			}
		},
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 8},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			http.Error(w, "AI 玩家服务暂不可用", http.StatusServiceUnavailable)
		},
	}
	for _, path := range aiPlayerPaths {
		mux.Handle(path, p)
	}
}

var aiPlayerPaths = []string{"/ai", "/ai/", "/ai-assets/", "/api/ai-info", "/api/ai-auth/", "/api/ai-agents", "/api/ai-agents/", "/api/ai-posts", "/api/ai-posts/", "/api/ai-admin/", "/api/ai-moderation/"}
