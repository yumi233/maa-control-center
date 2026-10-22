package api

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

// Only forward the dedicated public binding API. Administrator API is never proxied.
func (s *Server) sklandRoutes(m *http.ServeMux) {
	// Exact registration prevents ServeMux's automatic slash redirect from looping.
	m.HandleFunc("GET /skland", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Assets == nil {
			http.Error(w, "Binding page unavailable", 503)
			return
		}
		data, err := fs.ReadFile(s.cfg.Assets, "index.html")
		if err != nil {
			http.Error(w, "Binding page unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	m.HandleFunc("GET /skland/{$}", func(w http.ResponseWriter, r *http.Request) {
		target := "/skland"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})
	target := os.Getenv("MAACC_SKLAND_UPSTREAM")
	if target == "" {
		return
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.Transport = &http.Transport{ResponseHeaderTimeout: 55 * time.Second, MaxIdleConnsPerHost: 5}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		problem(w, 503, "skland_unavailable", "森空岛认证服务暂不可用，请稍后重试")
	}
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// Do not disclose MAA sessions or access tokens to the plugin backend.
		var bindingCookies []*http.Cookie
		for _, c := range r.Cookies() {
			if c.Name == "__Secure-skland_session" {
				bindingCookies = append(bindingCookies, c)
			}
		}
		r.Header.Del("Cookie")
		for _, c := range bindingCookies {
			r.AddCookie(c)
		}
		r.Header.Del("Authorization")
		r.Header.Del("X-Forwarded-For")
	}
	for _, route := range []string{"POST /skland/api/session", "GET /skland/api/status", "POST /skland/api/credential", "POST /skland/api/commit", "POST /skland/api/sign", "POST /skland/api/cancel"} {
		m.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			if r.Method != "GET" && r.Header.Get("Origin") != s.cfg.PublicURL {
				problem(w, 403, "origin_invalid", "请从本站绑定页面操作")
				return
			}
			proxy.ServeHTTP(w, r)
		})
	}
	m.HandleFunc("/skland/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "not_found", "接口不存在") })
}

func sklandEnabled() bool { return os.Getenv("MAACC_SKLAND_UPSTREAM") != "" }
