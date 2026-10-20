package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/storage"
)

const testPassword = "example-test-password-only"
const testToken = "example-test-agent-token-not-a-real-credential-0001"

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"), testPassword, "Primary fixture", "Secondary fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	s := New(st, Config{PublicURL: "https://control.example.test", AgentID: "fixture-agent", AgentToken: testToken, Assets: fstest.MapFS{"index.html": {Data: []byte("fixture frontend")}}})
	return s, s.Handler()
}
func call(h http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://control.example.test")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loginFixture(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	w := call(h, "POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`, nil, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var data map[string]string
	json.Unmarshal(w.Body.Bytes(), &data)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie protections missing")
	}
	return cookies[0], data["csrf_token"]
}
func TestAuthenticationCSRFAndReadOnly(t *testing.T) {
	s, h := testServer(t)
	for _, path := range []string{"/api/v1/accounts", "/api/v1/overview", "/api/v1/settings", "/api/v1/events"} {
		if w := call(h, "GET", path, "", nil, ""); w.Code != 401 {
			t.Fatalf("anonymous API returned %d", w.Code)
		}
	}
	cookie, csrf := loginFixture(t, h)
	if w := call(h, "POST", "/api/v1/jobs", `{}`, cookie, ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if w := call(h, "POST", "/api/v1/jobs", `{}`, cookie, csrf); w.Code != 423 {
		t.Fatal("production task dispatch not locked")
	}
	if w := call(h, "PUT", "/api/v1/accounts/secondary/policies", `{}`, cookie, csrf); w.Code != 423 {
		t.Fatal("production config write not locked")
	}
	if w := call(h, "POST", "/maa/remote/getTask", `{"user":"any","device":"any"}`, nil, ""); w.Code != 423 {
		t.Fatal("unpaired MAA got task access")
	}
	w := call(h, "GET", "/api/v1/settings", "", cookie, "")
	if strings.Contains(w.Body.String(), testToken) {
		t.Fatal("agent token leaked to frontend")
	}
	var stored string
	s.Store.DB.QueryRow("SELECT token_hash FROM sessions").Scan(&stored)
	if stored == cookie.Value {
		t.Fatal("plaintext session token stored")
	}
	if w := call(h, "POST", "/api/v1/auth/logout", `{}`, cookie, csrf); w.Code != 200 {
		t.Fatal("logout failed")
	}
	if w := call(h, "GET", "/api/v1/accounts", "", cookie, ""); w.Code != 401 {
		t.Fatal("session remained valid after logout")
	}
}
func TestHeartbeatBindingAndFreshness(t *testing.T) {
	s, h := testServer(t)
	hb := model.Heartbeat{AgentID: "fixture-agent", Hostname: "fixture-host", OS: "windows", Version: "fixture", Instances: []model.Instance{{ID: "shared-instance", Name: "MAA fixture", ProcessRunning: true, State: "unknown", Fingerprint: "fixture-hash", Profiles: []model.Profile{{ID: "a", Name: "Profile A", Policy: model.Policy{Source: "fixture"}, Schedule: json.RawMessage(`{"plans":[{"name":"A"}]}`)}, {ID: "b", Name: "Profile B", Policy: model.Policy{Source: "fixture"}, Schedule: json.RawMessage(`{"plans":[{"name":"B"}]}`)}}}}}
	b, _ := json.Marshal(hb)
	r := httptest.NewRequest("POST", "/api/v1/agent/heartbeat", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("heartbeat: %d %s", w.Code, w.Body.String())
	}
	for _, binding := range []model.Binding{{AccountID: "primary", AgentID: hb.AgentID, InstanceID: "shared-instance", ProfileID: "a"}, {AccountID: "secondary", AgentID: hb.AgentID, InstanceID: "shared-instance", ProfileID: "b"}} {
		if err := s.Store.Bind(binding); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := s.Store.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].Profile.ID == accounts[1].Profile.ID {
		t.Fatal("account profiles mixed")
	}
	for _, a := range accounts {
		if !a.AgentOnline || a.Status != "unknown" {
			t.Fatal("process presence incorrectly treated as running or heartbeat lost")
		}
	}
	cookie, _ := loginFixture(t, h)
	a := call(h, "GET", "/api/v1/accounts/primary/schedule", "", cookie, "")
	bResp := call(h, "GET", "/api/v1/accounts/secondary/schedule", "", cookie, "")
	if a.Body.String() == bResp.Body.String() {
		t.Fatal("independent schedules merged")
	}
	if _, err = s.Store.DB.Exec("UPDATE agents SET last_seen=?", time.Now().Add(-2*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	accounts, _ = s.Store.Accounts()
	for _, a := range accounts {
		if a.AgentOnline || a.Status != "offline" {
			t.Fatal("stale agent still reported online")
		}
	}
}
func TestResourceBudgetPreviewAndRequestValidation(t *testing.T) {
	_, h := testServer(t)
	cookie, csrf := loginFixture(t, h)
	bad := call(h, "POST", "/api/v1/accounts/secondary/policies/preview", `{"medicine_budget":1,"stone_budget":0,"expiring_hours":48}`, cookie, csrf)
	var out struct {
		Valid      bool     `json:"valid"`
		Applicable bool     `json:"applicable"`
		Errors     []string `json:"errors"`
	}
	json.Unmarshal(bad.Body.Bytes(), &out)
	if out.Valid || out.Applicable || len(out.Errors) == 0 {
		t.Fatal("secondary budget bypass")
	}
	good := call(h, "POST", "/api/v1/accounts/secondary/policies/preview", `{"medicine_budget":0,"stone_budget":0,"expiring_hours":48}`, cookie, csrf)
	json.Unmarshal(good.Body.Bytes(), &out)
	if !out.Valid || out.Applicable {
		t.Fatal("preview incorrectly applied")
	}
	if w := call(h, "POST", "/api/v1/accounts/secondary/policies/preview", `{} {}`, cookie, csrf); w.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	if w := call(h, "POST", "/api/v1/agent/heartbeat", `{}`, nil, ""); w.Code != 401 {
		t.Fatal("unpaired agent accepted")
	}
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://attacker.example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign login origin accepted")
	}
}
func TestCrashRecoveryNeverReplaysDispatchedJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	st, err := storage.Open(path, testPassword, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec("INSERT INTO jobs(id,account_id,type,state,idempotency_key,created_at,updated_at) VALUES('job','primary','LinkStart','dispatched','stable-key','now','now')")
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Close()
	st, err = storage.Open(path, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	var state string
	if err = st.DB.QueryRow("SELECT state FROM jobs WHERE id='job'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "unknown" {
		t.Fatal("dispatched job eligible for dangerous replay after restart")
	}
	_, err = st.DB.Exec("INSERT INTO jobs(id,account_id,type,state,idempotency_key,created_at,updated_at) VALUES('duplicate','primary','LinkStart','pending','stable-key','now','now')")
	if err == nil {
		t.Fatal("duplicate idempotency key accepted")
	}
}
