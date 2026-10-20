package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"github.com/yumi233/maa-control-center/internal/rules"
)

func createMember(t *testing.T, h http.Handler, admin *http.Cookie, csrf, name string) (User, *http.Cookie, string) {
	t.Helper()
	w := call(h, "POST", "/api/v1/users", `{"username":"`+name+`","password":"member-fixture-password","role":"member"}`, admin, csrf)
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var u User
	json.Unmarshal(w.Body.Bytes(), &u)
	w = call(h, "POST", "/api/v1/auth/login", `{"username":"`+name+`","password":"member-fixture-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatalf("member login %s", w.Body)
	}
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	if session["role"] != "member" || session["username"] != name {
		t.Fatal("wrong session identity")
	}
	return u, w.Result().Cookies()[0], session["csrf_token"]
}
func TestMultiUserIsolationAndOwnership(t *testing.T) {
	s, h := testServer(t)
	admin, csrf := loginFixture(t, h)
	alice, ac, ax := createMember(t, h, admin, csrf, "alice")
	bob, bc, bx := createMember(t, h, admin, csrf, "bob")
	source := []byte(`{"Configurations":{"secret-admin":{"TaskQueue":[{"TaskType":"StartUp"},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]},"alice-profile":{"TaskQueue":[{"TaskType":"StartUp"},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]}}}`)
	hb := model.Heartbeat{AgentID: "fixture-agent", Hostname: "secret-host", Instances: []model.Instance{{ID: "fixture-instance", Version: "6.20.0", ProcessVerified: true, ConfigurationWrite: true, Fingerprint: rules.Hash(source), Logs: []string{"secret-admin-log"}, Profiles: []model.Profile{{ID: "secret-admin", Name: "secret-admin-name", AccountHint: "secret-admin-hint", Editable: rules.Projection(source, "secret-admin")}, {ID: "alice-profile", Name: "alice-profile", Editable: rules.Projection(source, "alice-profile")}}}}}
	if e := s.Store.Heartbeat(hb); e != nil {
		t.Fatal(e)
	}
	for _, pair := range []struct{ account, profile string }{{"primary", "secret-admin"}, {"secondary", "alice-profile"}} {
		if e := s.Store.Bind(model.Binding{AccountID: pair.account, AgentID: hb.AgentID, InstanceID: "fixture-instance", ProfileID: pair.profile}); e != nil {
			t.Fatal(e)
		}
	}
	w := call(h, "PUT", "/api/v1/accounts/secondary", `{"name":"Alice MAA","owner_id":"`+alice.ID+`"}`, admin, csrf)
	if w.Code != 200 {
		t.Fatalf("assign %d %s", w.Code, w.Body)
	}
	s.Store.Event("info", "primary", "secret-admin-event")
	s.Store.Event("info", "secondary", "alice-event")
	for _, path := range []string{"/overview", "/accounts", "/events", "/agents", "/accounts/secondary/status", "/accounts/secondary/logs"} {
		w = call(h, "GET", "/api/v1"+path, "", ac, "")
		if w.Code != 200 {
			t.Fatalf("own read %s %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret-admin") || strings.Contains(w.Body.String(), "secret-host") {
			t.Fatalf("cross-user leak at %s: %s", path, w.Body)
		}
	}
	for _, suffix := range []string{"status", "policies", "inventory", "schedule", "logs"} {
		if w = call(h, "GET", "/api/v1/accounts/primary/"+suffix, "", ac, ""); w.Code != 404 {
			t.Fatalf("IDOR read %s %d", suffix, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/users", "/api/v1/accounts/secondary/binding"} {
		method := "GET"
		body := ""
		if strings.Contains(path, "binding") {
			method = "POST"
			body = `{"agent_id":"fixture-agent","instance_id":"fixture-instance","profile_id":"secret-admin"}`
		}
		if w = call(h, method, path, body, ac, ax); w.Code != 403 {
			t.Fatalf("member admin endpoint %d %s", w.Code, w.Body)
		}
	}
	if w = call(h, "PUT", "/api/v1/accounts/secondary", `{"name":"takeover","owner_id":"`+bob.ID+`"}`, ac, ax); w.Code != 403 {
		t.Fatal("member can transfer account")
	}
	if w = call(h, "POST", "/api/v1/accounts", `{"name":"illegal","role":"primary","owner_id":"admin"}`, ac, ax); w.Code != 403 {
		t.Fatal("member can create other user's account")
	}
	w = call(h, "POST", "/api/v1/accounts", `{"name":"Bob account","role":"secondary"}`, bc, bx)
	if w.Code != 201 {
		t.Fatalf("own add %s", w.Body)
	}
	preview := `{"target":"config","operations":[{"scope":"task","task":1,"field":"MaxTimes","value":4}]}`
	w = call(h, "POST", "/api/v1/accounts/primary/config/preview", preview, admin, csrf)
	var private ConfigPlan
	json.Unmarshal(w.Body.Bytes(), &private)
	if w.Code != 201 {
		t.Fatalf("private plan %s", w.Body)
	}
	for _, action := range []string{"approve", "rollback", "cancel"} {
		w = call(h, "POST", "/api/v1/config/plans/"+private.ID+"/"+action, `{"confirm":"`+private.ID+`"}`, ac, ax)
		if w.Code == 200 || w.Code == 201 || w.Code == 202 {
			t.Fatal("member altered another user's plan")
		}
	}
	w = call(h, "POST", "/api/v1/accounts/secondary/config/preview", preview, ac, ax)
	if w.Code != 201 {
		t.Fatalf("member preview %d %s", w.Code, w.Body)
	}
	var own ConfigPlan
	json.Unmarshal(w.Body.Bytes(), &own)
	if own.OwnerID != alice.ID || own.CreatedBy != alice.ID {
		t.Fatal("plan actor/owner missing")
	}
	w = call(h, "GET", "/api/v1/config/plans", "", bc, "")
	if w.Body.String() != "[]\n" {
		t.Fatal("Bob can see plans")
	}
	w = call(h, "GET", "/api/v1/config/plans", "", ac, "")
	if strings.Contains(w.Body.String(), private.ID) || !strings.Contains(w.Body.String(), own.ID) {
		t.Fatal("plan list leaked or lost own plan")
	}
	w = call(h, "PUT", "/api/v1/accounts/secondary", `{"name":"Bob MAA","owner_id":"`+bob.ID+`"}`, admin, csrf)
	if w.Code != 200 {
		t.Fatalf("transfer %s", w.Body)
	}
	w = call(h, "POST", "/api/v1/config/plans/"+own.ID+"/approve", `{"confirm":"`+own.ID+`"}`, ac, ax)
	if w.Code != 404 {
		t.Fatal("former owner retained access")
	}
	ps, _ := s.plans(own.ID)
	if ps[0].State != "cancelled" {
		t.Fatal("transfer retained stale proposal")
	}
	// Unique bindings prevent two logical accounts silently targeting one profile.
	if e := s.Store.Bind(model.Binding{AccountID: "secondary", AgentID: hb.AgentID, InstanceID: "fixture-instance", ProfileID: "secret-admin"}); e == nil {
		t.Fatal("duplicate profile binding allowed")
	}
}

func TestPasswordAndDisabledSessionRevocation(t *testing.T) {
	s, h := testServer(t)
	admin, csrf := loginFixture(t, h)
	alice, c, x := createMember(t, h, admin, csrf, "alice")
	w := call(h, "POST", "/api/v1/auth/password", `{"current_password":"wrong","new_password":"new-fixture-password"}`, c, x)
	if w.Code != 422 {
		t.Fatal("wrong current password accepted")
	}
	w = call(h, "POST", "/api/v1/auth/password", `{"current_password":"member-fixture-password","new_password":"new-fixture-password"}`, c, x)
	if w.Code != 200 {
		t.Fatalf("change password %s", w.Body)
	}
	if w = call(h, "GET", "/api/v1/overview", "", c, ""); w.Code != 401 {
		t.Fatal("old session survived password change")
	}
	if w = call(h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"member-fixture-password"}`, nil, ""); w.Code != 401 {
		t.Fatal("old password accepted")
	}
	w = call(h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"new-fixture-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal("new password rejected")
	}
	c = w.Result().Cookies()[0]
	w = call(h, "PUT", "/api/v1/users/"+alice.ID, `{"role":"member","disabled":true}`, admin, csrf)
	if w.Code != 200 {
		t.Fatalf("disable %s", w.Body)
	}
	if w = call(h, "GET", "/api/v1/overview", "", c, ""); w.Code != 401 {
		t.Fatal("disabled user retained session")
	}
	if w = call(h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"new-fixture-password"}`, nil, ""); w.Code != 401 {
		t.Fatal("disabled user could log in")
	}
	if w = call(h, "PUT", "/api/v1/users/admin", `{"role":"member","disabled":true}`, admin, csrf); w.Code != 409 {
		t.Fatal("last administrator lockout allowed")
	}
	w = call(h, "PUT", "/api/v1/users/"+alice.ID, `{"role":"member","disabled":false,"password":"reset-fixture-password"}`, admin, csrf)
	if w.Code != 200 {
		t.Fatalf("reset %s", w.Body)
	}
	if w = call(h, "POST", "/api/v1/auth/login", `{"username":"alice","password":"reset-fixture-password"}`, nil, ""); w.Code != 200 {
		t.Fatal("admin reset failed")
	}
	var hash string
	s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE id='admin'").Scan(&hash)
	if strings.Contains(hash, testPassword) {
		t.Fatal("plaintext password")
	}
}

func TestPerUserIntegrationsAndNotifications(t *testing.T) {
	s, h := testServer(t)
	admin, csrf := loginFixture(t, h)
	alice, ac, ax := createMember(t, h, admin, csrf, "alice")
	bob, bc, bx := createMember(t, h, admin, csrf, "bob")
	cfg := assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "123:admin_fixture", ChatID: "1"}}}
	if e := s.Integrations.Save(cfg); e != nil {
		t.Fatal(e)
	}
	cfg.Channels[0].Token = "456:alice_fixture"
	cfg.Channels[0].ChatID = "2"
	if e := s.Integrations.ForUser(alice.ID).Save(cfg); e != nil {
		t.Fatal(e)
	}
	for _, c := range []*http.Cookie{ac, bc} {
		w := call(h, "GET", "/api/v1/integrations", "", c, "")
		if strings.Contains(w.Body.String(), "fixture") || strings.Contains(w.Body.String(), `"chat_id":"1"`) {
			t.Fatal("integration secret/target leak")
		}
	}
	if w := call(h, "PUT", "/api/v1/integrations", `{"provider":{"base_url":"http://127.0.0.1:1","allow_private_http":true}}`, ac, ax); w.Code != 422 {
		t.Fatal("member may enable internal network requests")
	}
	for _, id := range []string{"admin", alice.ID, bob.ID} {
		if e := s.Notifications.Queue(notify.Facts{OwnerID: id, ID: "same-event", State: "test"}); e != nil {
			t.Fatal(e)
		}
	}
	var aid string
	s.Store.DB.QueryRow("SELECT id FROM notification_outbox WHERE owner_id=?", alice.ID).Scan(&aid)
	if w := call(h, "POST", "/api/v1/integrations/notifications/"+aid+"/retry", `{"confirm":true}`, bc, bx); w.Code != 409 {
		t.Fatal("cross-user notification retry allowed")
	}
	for _, c := range []*http.Cookie{admin, bc} {
		w := call(h, "GET", "/api/v1/integrations/notifications", "", c, "")
		if strings.Contains(w.Body.String(), aid) {
			t.Fatal("notification history leak")
		}
	}
}
