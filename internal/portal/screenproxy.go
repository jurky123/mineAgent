package portal

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"strings"
)

// screenProxy 把 /display/* 反向代理到 Paper 的浏览器出口代理（本机回环）。
//
// 客户端拿到的屏地址形如 https://<域名>/display/<token>，走 443/HTTPS：
// 内容与令牌都不再明文过公网；Paper 侧代理因此可以只绑 127.0.0.1，
// 不再需要对外放行 8768。
type screenProxy struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

// newScreenProxy target 形如 http://127.0.0.1:8768。
func newScreenProxy(target string) *screenProxy {
	u, err := url.Parse(strings.TrimRight(target, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	p := httputil.NewSingleHostReverseProxy(u)
	// 保持和原始请求一致的路径：/display/... → /display/...（Paper 同路径）。
	original := p.Director
	p.Director = func(req *http.Request) {
		original(req)
		req.Host = u.Host // 上游按回环 Host 校验；域名只用于对外
	}
	return &screenProxy{target: u, proxy: p}
}

func (sp *screenProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 只允许 GET/HEAD（出口代理侧同样限制，这里先挡一层）。
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "只支持 GET/HEAD", http.StatusMethodNotAllowed)
		return
	}
	sp.proxy.ServeHTTP(w, r)
}
