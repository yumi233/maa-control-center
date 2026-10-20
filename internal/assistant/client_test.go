package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExactModelJSONAndRedactedFailures(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("invalid client request")
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if in["model"] != "exact-fixture-model" {
			t.Error("model substituted")
		}
		if mode == "http" {
			w.WriteHeader(401)
			w.Write([]byte("fixture-secret"))
			return
		}
		model := "exact-fixture-model"
		content := `{"ok":true}`
		finish := "stop"
		if mode == "wrong" {
			model = "other-model"
		}
		if mode == "extra" {
			content = `{"ok":true,"shell":"bad"}`
		}
		if mode == "truncated" {
			finish = "length"
		}
		json.NewEncoder(w).Encode(map[string]any{"model": model, "choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": content}}}})
	}))
	defer server.Close()
	p := Provider{BaseURL: server.URL + "/v1", Model: "exact-fixture-model", APIKey: "fixture-secret", AllowPrivateHTTP: true}
	c := ClientAPI{HTTP: Client(time.Second)}
	for _, m := range []string{"ok", "wrong", "extra", "truncated", "http"} {
		mode = m
		var out struct {
			OK bool `json:"ok"`
		}
		e := c.JSON(context.Background(), p, "JSON fixture", map[string]string{"purpose": "test"}, &out)
		if (m == "ok") != (e == nil) {
			t.Fatalf("%s result %v", m, e)
		}
		if e != nil && strings.Contains(e.Error(), "fixture-secret") {
			t.Fatal("secret leaked")
		}
	}
	for _, u := range []string{"http://169.254.169.254/v1", "https://user:secret@example.test/v1", "http://example.test/v1", "https://example.test/v1?api_key=secret"} {
		if ValidateURL(u, true) == nil {
			t.Fatalf("unsafe URL %s", u)
		}
	}
}
