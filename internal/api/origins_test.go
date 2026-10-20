package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTunnelAndLANOriginsKeepCSRFProtection(t *testing.T) {
	s, h := testServer(t)
	s.cfg.AllowedOrigins = []string{"https://maa.example.test", "https://192.0.2.60:18443"}
	cookie, csrf := loginFixture(t, h)
	for _, origin := range []string{"https://maa.example.test", "https://192.0.2.60:18443", "https://control.example.test"} {
		r := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 423 {
			t.Fatalf("approved origin rejected: %d", w.Code)
		}
		r = httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("additional origin bypassed CSRF token")
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://attacker.example.test")
	r.Header.Set("X-Forwarded-Host", "maa.example.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded host granted origin permission")
	}
}

func TestOriginsRejectWildcardsCredentialsAndPaths(t *testing.T) {
	for _, origin := range []string{"*", "https://*.example.test", "https://example.test/path", "https://example.test?key=value", "https://user:secret@example.test", "http://192.0.2.60:8421", "https://example.test:99999", "null"} {
		if _, err := ParseOrigins([]string{origin}); err == nil {
			t.Fatalf("invalid origin accepted %q", origin)
		}
	}
	v, err := ParseOrigins([]string{" https://EXAMPLE.test/ ", "https://example.test", "http://127.0.0.1:8421"})
	if err != nil || len(v) != 2 || v[0] != "https://example.test" {
		t.Fatal("explicit origins did not normalize")
	}
}
