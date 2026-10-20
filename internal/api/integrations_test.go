package api

import (
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/configsync"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentProposalReviewDispatchReceiptRollback(t *testing.T) {
	s, h := testServer(t)
	cookie, csrf := loginFixture(t, h)
	source := []byte(`{"Configurations":{"fixture-profile":{"TaskQueue":[{"TaskType":"StartUp","AccountName":"do-not-send-account"},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]}}}`)
	hb := model.Heartbeat{AgentID: "fixture-agent", Hostname: "fixture", Instances: []model.Instance{{ID: "0123456789abcdef", Version: "6.20.0", State: "unknown", ProcessVerified: true, ConfigurationWrite: true, Fingerprint: rules.Hash(source), Profiles: []model.Profile{{ID: "fixture-profile", Editable: rules.Projection(source, "fixture-profile")}}}}}
	if e := s.Store.Heartbeat(hb); e != nil {
		t.Fatal(e)
	}
	if e := s.Store.Bind(model.Binding{AccountID: "primary", AgentID: hb.AgentID, InstanceID: hb.Instances[0].ID, ProfileID: "fixture-profile"}); e != nil {
		t.Fatal(e)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		b, _ := json.Marshal(in)
		if strings.Contains(string(b), "do-not-send-account") || strings.Contains(string(b), "fixture-profile") {
			t.Error("private identity sent to model")
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"summary":"招募改为四次，其他字段保留","operations":[{"scope":"task","task":1,"field":"MaxTimes","value":4}]}`}}}})
	}))
	defer provider.Close()
	s.LLM = assistant.ClientAPI{HTTP: provider.Client()}
	cfg := assistant.Settings{Provider: assistant.Provider{BaseURL: provider.URL, Model: "fixture-model", APIKey: "fixture-model-secret", AllowPrivateHTTP: true}}
	if e := s.Integrations.Save(cfg); e != nil {
		t.Fatal(e)
	}
	w := call(h, "POST", "/api/v1/accounts/primary/assistant/propose", `{"intent":"招募最多四次","target":"config"}`, cookie, csrf)
	if w.Code != 201 {
		t.Fatalf("propose %d %s", w.Code, w.Body.String())
	}
	var p ConfigPlan
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.State != "draft" || len(p.Diff) != 1 {
		t.Fatal("proposal skipped review")
	}
	agentCall := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w = agentCall("GET", "/api/v1/agent/config/commands", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("draft dispatched")
	}
	if w = call(h, "POST", "/api/v1/config/plans/"+p.ID+"/approve", `{"confirm":"wrong"}`, cookie, csrf); w.Code != 409 {
		t.Fatal("missing exact approval accepted")
	}
	if w = call(h, "POST", "/api/v1/config/plans/"+p.ID+"/approve", `{"confirm":"`+p.ID+`"}`, cookie, csrf); w.Code != 202 {
		t.Fatalf("approval %d %s", w.Code, w.Body.String())
	}
	w = agentCall("GET", "/api/v1/agent/config/commands", "")
	var commands []configsync.Command
	json.Unmarshal(w.Body.Bytes(), &commands)
	if len(commands) != 1 || commands[0].Role != "primary" || commands[0].Profile != "fixture-profile" {
		t.Fatal("wrong command binding")
	}
	after := rules.Hash([]byte("applied-fixture"))
	cfg.Channels = []assistant.Channel{{Kind: "qq", Enabled: true, BaseURL: provider.URL, AllowPrivateHTTP: true, TargetType: "private", TargetID: "1"}}
	if e := s.Integrations.Save(cfg); e != nil {
		t.Fatal(e)
	}
	rec := configsync.Receipt{ID: p.ID, State: "applied", BeforeHash: p.BeforeHash, AfterHash: after, Message: "fixture verified", Time: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(rec)
	if _, e := s.Store.DB.Exec("CREATE TRIGGER fixture_outbox_failure BEFORE INSERT ON notification_outbox BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END"); e != nil {
		t.Fatal(e)
	}
	if w = agentCall("POST", "/api/v1/agent/config/receipts", string(b)); w.Code != 500 {
		t.Fatal("failed outbox transaction accepted")
	}
	beforeRetry, _ := s.plans(p.ID)
	if beforeRetry[0].State != "dispatched" {
		t.Fatal("receipt committed without its notification")
	}
	s.Store.DB.Exec("DROP TRIGGER fixture_outbox_failure")
	if w = agentCall("POST", "/api/v1/agent/config/receipts", string(b)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	ps, _ := s.plans(p.ID)
	if ps[0].State != "applied" {
		t.Fatal("receipt lost")
	}
	agentCall("POST", "/api/v1/agent/config/receipts", string(b))
	var outboxCount int
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&outboxCount)
	if outboxCount != 1 {
		t.Fatal("receipt retry duplicated notification")
	}
	hb.Instances[0].Fingerprint = after
	s.Store.Heartbeat(hb)
	w = call(h, "POST", "/api/v1/config/plans/"+p.ID+"/rollback", `{}`, cookie, csrf)
	if w.Code != 201 {
		t.Fatalf("rollback preview %d %s", w.Code, w.Body.String())
	}
	var rb ConfigPlan
	json.Unmarshal(w.Body.Bytes(), &rb)
	if rb.State != "draft" || rb.RollbackOf != p.ID || displayValue(rb.Diff[0].After) != "3" {
		t.Fatal("rollback skipped review or wrong diff")
	}
	get := call(h, "GET", "/api/v1/integrations", "", cookie, "")
	if strings.Contains(get.Body.String(), "fixture-model-secret") {
		t.Fatal("model secret leaked")
	}
	if w = call(h, "GET", "/api/v1/config/plans", "", nil, ""); w.Code != 401 {
		t.Fatal("plan not protected")
	}
	if w = call(h, "POST", "/api/v1/agent/config/receipts", string(b), nil, ""); w.Code != 401 {
		t.Fatal("unpaired receipt accepted")
	}
}
func displayValue(v any) string { b, _ := json.Marshal(v); return string(b) }
