package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSklandProxyBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("admin authorization leaked")
		}
		if _, e := r.Cookie("maacc_session"); e == nil {
			t.Error("admin cookie leaked")
		}
		if c, e := r.Cookie("__Secure-skland_session"); e != nil || c.Value != "browser" {
			t.Error("binding session missing")
		}
		if r.URL.Path != "/skland/api/status" {
			t.Error("unexpected upstream path")
		}
		io.WriteString(w, `{"confirmed":false}`)
	}))
	defer upstream.Close()
	t.Setenv("MAACC_SKLAND_UPSTREAM", upstream.URL)
	s := &Server{cfg: Config{PublicURL: "https://maa.yumi233.cn"}}
	mux := http.NewServeMux()
	s.sklandRoutes(mux)
	req := httptest.NewRequest("GET", "/skland/api/status", nil)
	req.Header.Set("Authorization", "Bearer admin")
	req.Header.Set("Cookie", "maacc_session=admin; __Secure-skland_session=browser")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "confirmed") {
		t.Fatal(w.Code, w.Body.String())
	}
	req = httptest.NewRequest("POST", "/skland/api/commit", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.invalid")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross origin accepted", w.Code)
	}
	req = httptest.NewRequest("GET", "/skland/api/admin", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("unexpected path exposed", w.Code)
	}
}

func TestSklandTrailingSlashPreservesRequest(t *testing.T) {
	t.Setenv("MAACC_SKLAND_UPSTREAM", "")
	mux := http.NewServeMux()
	s := &Server{}
	s.sklandRoutes(mux)
	req := httptest.NewRequest("GET", "/skland/?r=public-request", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 307 || w.Header().Get("Location") != "/skland?r=public-request" {
		t.Fatal(w.Code, w.Header().Get("Location"))
	}
}

func TestSklandEntrypointDoesNotRedirectLoop(t *testing.T) {
	t.Setenv("MAACC_SKLAND_UPSTREAM", "")
	mux := http.NewServeMux()
	s := &Server{cfg: Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("binding-app")}}}}
	s.sklandRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, path := range []string{"/skland", "/skland/", "/skland/?r=test"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(path, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || string(body) != "binding-app" {
			t.Fatal(path, response.StatusCode, string(body))
		}
	}
}
