package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yumi233/maa-control-center/internal/bot"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
)

func botRequest(h http.Handler, key string, in botCommand) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", "/api/v1/bot/commands", strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func botFixture(t *testing.T) (*Server, http.Handler, *http.Cookie, string, bot.Binding, string) {
	s, h, c, x := executionFixture(t)
	b, key, e := bot.Bind(s.Store.DB, bot.Binding{OwnerID: "admin", AccountID: "secondary", UMO: "fixture:GroupMessage:group", Members: []bot.Member{{UID: "owner", Role: "operator"}, {UID: "reader", Role: "viewer"}}})
	if e != nil {
		t.Fatal(e)
	}
	return s, h, c, x, b, key
}
func botInput(command, message string) botCommand {
	return botCommand{UMO: "fixture:GroupMessage:group", SenderID: "owner", MessageID: message, SentAt: time.Now().UTC().Format(time.RFC3339), Command: command}
}

func TestBotScopePermissionsIdempotencyAndConfirmedStop(t *testing.T) {
	s, h, _, _, _, key := botFixture(t)
	for name, mutate := range map[string]func(*botCommand){"wrong-group": func(c *botCommand) { c.UMO = "fixture:GroupMessage:other" }, "unknown-user": func(c *botCommand) { c.SenderID = "stranger" }, "viewer": func(c *botCommand) { c.SenderID = "reader" }, "other-account": func(c *botCommand) { c.Target = "primary" }, "expired-message": func(c *botCommand) { c.SentAt = time.Now().Add(-time.Hour).Format(time.RFC3339) }, "missing-id": func(c *botCommand) { c.MessageID = "" }, "budget-write": func(c *botCommand) { c.Command = "set_budget" }} {
		t.Run(name, func(t *testing.T) {
			in := botInput("start", name)
			mutate(&in)
			if w := botRequest(h, key, in); w.Code < 400 {
				t.Fatalf("unsafe command accepted: %s", w.Body)
			}
		})
	}
	if w := botRequest(h, strings.Repeat("f", 64), botInput("status", "bad-token")); w.Code != 401 {
		t.Fatal("bad key accepted")
	}
	view := botInput("status", "view")
	view.SenderID = "reader"
	if w := botRequest(h, key, view); w.Code != 200 {
		t.Fatal(w.Body)
	}
	in := botInput("start", "same-message")
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- botRequest(h, key, in) }()
	}
	wg.Wait()
	close(responses)
	id := ""
	for w := range responses {
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		var out botReply
		json.Unmarshal(w.Body.Bytes(), &out)
		if id != "" && id != out.JobID {
			t.Fatal("replayed message created a different job")
		}
		id = out.JobID
	}
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate job", count)
	}
	in.Command = "stop"
	if w := botRequest(h, key, in); w.Code != 409 {
		t.Fatal("message identity can change operation")
	}
	if w := botRequest(h, key, botInput("stop", "cancel-pending")); w.Code != 200 {
		t.Fatal(w.Body)
	}
	var state string
	s.Store.DB.QueryRow("SELECT state FROM jobs WHERE id=?", id).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
	w := botRequest(h, key, botInput("start", "second-run"))
	var out botReply
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.JobID == "" {
		t.Fatal(w.Body)
	}
	agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	if w = botRequest(h, key, botInput("stop", "stop-dispatched")); w.Code != 200 {
		t.Fatal(w.Body)
	}
	var requested bool
	s.Store.DB.QueryRow("SELECT state,cancel_requested FROM jobs WHERE id=?", out.JobID).Scan(&state, &requested)
	if state != "dispatched" || !requested {
		t.Fatal("claimed stopped before agent confirmation", state, requested)
	}
	rec := jobrunner.Receipt{ID: out.JobID, State: "cancelled", Time: time.Now().UTC().Format(time.RFC3339), Detail: "fixture idle confirmation"}
	if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
		t.Fatal(w.Body)
	}
}

func TestBotMembersRevisionRevocationAndOwnership(t *testing.T) {
	s, h, c, x, b, key := botFixture(t)
	_, other, csrf := createMember(t, h, c, x, "other")
	body := `{"revision":1,"members":[{"uid":"reader","role":"viewer","label":"reader"}]}`
	if w := call(h, "PUT", "/api/v1/bot/bindings/"+b.ID, body, other, csrf); w.Code < 400 {
		t.Fatal("foreign binding changed")
	}
	if w := call(h, "GET", "/api/v1/bot/bindings", "", other, ""); w.Body.String() != "[]\n" {
		t.Fatal("binding UID list leaked")
	}
	if w := call(h, "PUT", "/api/v1/bot/bindings/"+b.ID, body, c, x); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := call(h, "PUT", "/api/v1/bot/bindings/"+b.ID, body, c, x); w.Code != 409 {
		t.Fatal("stale member edit overwrote changes")
	}
	if w := botRequest(h, key, botInput("status", "removed")); w.Code != 403 {
		t.Fatal("removed operator retained access")
	}
	view := botInput("report", "reader-report")
	view.SenderID = "reader"
	if w := botRequest(h, key, view); w.Code != 200 {
		t.Fatal(w.Body)
	}
	list := call(h, "GET", "/api/v1/bot/bindings", "", c, "")
	if strings.Contains(list.Body.String(), key) || strings.Contains(list.Body.String(), "token_hash") {
		t.Fatal("credential exposed")
	}
	call(h, "DELETE", "/api/v1/bot/bindings/"+b.ID, "", c, x)
	view.MessageID = "revoked"
	if w := botRequest(h, key, view); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
	// New ownership invalidates old grants and releases the active binding scope.
	b, key, e := bot.Bind(s.Store.DB, bot.Binding{OwnerID: "admin", AccountID: "secondary", UMO: b.UMO, Members: []bot.Member{{UID: "owner", Role: "operator"}}})
	if e != nil {
		t.Fatal(e)
	}
	var uid string
	s.Store.DB.QueryRow("SELECT id FROM users WHERE username='other'").Scan(&uid)
	if w := call(h, "PUT", "/api/v1/accounts/secondary", `{"name":"new owner account","owner_id":"`+uid+`"}`, c, x); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := botRequest(h, key, botInput("status", "transfer")); w.Code != 401 {
		t.Fatal("ownership transfer kept token valid")
	}
	var enabled bool
	s.Store.DB.QueryRow("SELECT enabled FROM bot_bindings WHERE id=?", b.ID).Scan(&enabled)
	if enabled {
		t.Fatal("old scope not revoked")
	}
}

func TestBotRequestLedgerFailureRollsBackJob(t *testing.T) {
	s, h, _, _, _, key := botFixture(t)
	s.Store.DB.Exec("CREATE TRIGGER block_bot_ledger BEFORE INSERT ON bot_requests BEGIN SELECT RAISE(ABORT,'fixture failure'); END")
	if w := botRequest(h, key, botInput("start", "ledger-failure")); w.Code < 500 {
		t.Fatal("injected failure accepted")
	}
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("job survived failed request transaction")
	}
}

func TestCreateBotBindingOnlyReturnsSecretOnce(t *testing.T) {
	s, h, c, x := executionFixture(t)
	body := `{"account_id":"secondary","umo":"fixture:GroupMessage:ui","members":[{"uid":"fixture-owner","label":"owner","role":"operator"}]}`
	w := call(h, "POST", "/api/v1/bot/bindings", body, c, x)
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	var result struct {
		Binding bot.Binding `json:"binding"`
		Token   string      `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Token) != 64 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid one-time credential response")
	}
	var stored string
	s.Store.DB.QueryRow("SELECT token_hash FROM bot_bindings WHERE id=?", result.Binding.ID).Scan(&stored)
	if stored == result.Token || stored != bot.Hash(result.Token) {
		t.Fatal("plaintext credential stored")
	}
	list := call(h, "GET", "/api/v1/bot/bindings", "", c, "")
	if strings.Contains(list.Body.String(), result.Token) || strings.Contains(list.Body.String(), stored) {
		t.Fatal("secret or hash exposed in list")
	}
	if w = call(h, "POST", "/api/v1/bot/bindings", body, c, x); w.Code != 422 {
		t.Fatal("duplicate active scope accepted")
	}
}
