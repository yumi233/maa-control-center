package api

import (
	"encoding/json"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func executionFixture(t *testing.T) (*Server, http.Handler, *http.Cookie, string) {
	s, h := testServer(t)
	cookie, csrf := loginFixture(t, h)
	source := []byte(`{"Configurations":{"p":{"TaskQueue":[{"TaskType":"StartUp","IsEnable":true,"AccountName":"fixture","AccountSwitchEnabled":true},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]},"s":{"TaskQueue":[{"TaskType":"StartUp","IsEnable":true,"AccountName":"fixture-two","AccountSwitchEnabled":true},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]}}}`)
	hb := model.Heartbeat{AgentID: "fixture-agent", Instances: []model.Instance{{ID: "0123456789abcdef", Version: "6.20.0", ProcessVerified: true, ConfigurationWrite: true, TaskExecution: true, Fingerprint: rules.Hash(source), Profiles: []model.Profile{{ID: "p", Editable: rules.Projection(source, "p")}, {ID: "s", Editable: rules.Projection(source, "s")}}}}}
	if e := s.Store.Heartbeat(hb); e != nil {
		t.Fatal(e)
	}
	for _, p := range []struct{ a, p string }{{"primary", "p"}, {"secondary", "s"}} {
		if e := s.Store.Bind(model.Binding{AccountID: p.a, AgentID: hb.AgentID, InstanceID: hb.Instances[0].ID, ProfileID: p.p}); e != nil {
			t.Fatal(e)
		}
	}
	return s, h, cookie, csrf
}
func agentRequest(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRealJobsSerialDispatchAndReceipts(t *testing.T) {
	s, h, c, x := executionFixture(t)
	for _, account := range []string{"primary", "secondary"} {
		w := call(h, "POST", "/api/v1/jobs", `{"account_id":"`+account+`","type":"daily"}`, c, x)
		if w.Code != 202 {
			t.Fatalf("queue %d %s", w.Code, w.Body)
		}
	}
	w := agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	var cmds []jobrunner.Command
	json.Unmarshal(w.Body.Bytes(), &cmds)
	if len(cmds) != 1 {
		t.Fatalf("commands %s", w.Body)
	}
	first := cmds[0]
	w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	json.Unmarshal(w.Body.Bytes(), &cmds)
	if len(cmds) != 1 || cmds[0].ID != first.ID {
		t.Fatal("parallel account dispatch")
	}
	rec := jobrunner.Receipt{ID: first.ID, State: "completed", Time: time.Now().UTC().Format(time.RFC3339), Detail: "fixture completion"}
	if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
		t.Fatalf("receipt %s", w.Body)
	}
	w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	json.Unmarshal(w.Body.Bytes(), &cmds)
	if len(cmds) != 1 || cmds[0].ID == first.ID {
		t.Fatal("next account stuck")
	}
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='completed'").Scan(&count)
	if count != 1 {
		t.Fatal("completion not persisted")
	}
}
func TestScheduleCRUDIsolationDueAndMissedOccurrence(t *testing.T) {
	s, h, c, x := executionFixture(t)
	alice, ac, ax := createMember(t, h, c, x, "alice")
	call(h, "PUT", "/api/v1/accounts/secondary", `{"name":"Alice","owner_id":"`+alice.ID+`"}`, c, x)
	w := call(h, "POST", "/api/v1/schedules", `{"account_id":"secondary","name":"alice daily","enabled":true,"hour":9,"minute":0,"weekdays":[1,2,3,4,5,6,0]}`, ac, ax)
	if w.Code != 200 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var plan Schedule
	json.Unmarshal(w.Body.Bytes(), &plan)
	_, bc, bx := createMember(t, h, c, x, "bob")
	if w = call(h, "PUT", "/api/v1/schedules/"+plan.ID, `{"account_id":"secondary","name":"stolen","enabled":true,"hour":9,"minute":0,"weekdays":[1]}`, bc, bx); w.Code != 404 {
		t.Fatalf("IDOR schedule %d", w.Code)
	}
	if w = call(h, "GET", "/api/v1/schedules", "", bc, ""); w.Body.String() != "[]\n" {
		t.Fatal("schedule list leaked")
	}
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	s.Store.DB.Exec("UPDATE scheduled_tasks SET next_run_at=? WHERE id=?", at, plan.ID)
	if e := s.TickSchedules(now); e != nil {
		t.Fatal(e)
	}
	s.TickSchedules(now)
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE schedule_id=?", plan.ID).Scan(&count)
	if count != 1 {
		t.Fatal("same occurrence replayed")
	}
	s.Store.DB.Exec("UPDATE scheduled_tasks SET next_run_at=? WHERE id=?", now.Add(-time.Hour).Format(time.RFC3339), plan.ID)
	if e := s.TickSchedules(now); e != nil {
		t.Fatal(e)
	}
	s.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE schedule_id=? AND state='expired'", plan.ID).Scan(&count)
	if count != 1 {
		t.Fatal("missed occurrence replayed")
	}
}

func TestCheckReceiptsNeverQueueNotifications(t *testing.T) {
	s, h, c, x := executionFixture(t)
	e := s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "fixture-only-token", ChatID: "123"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"check", "daily"} {
		w := call(h, "POST", "/api/v1/jobs", `{"account_id":"primary","type":"`+kind+`"}`, c, x)
		if w.Code != 202 {
			t.Fatal(w.Body)
		}
		w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
		var cmds []jobrunner.Command
		json.Unmarshal(w.Body.Bytes(), &cmds)
		if len(cmds) != 1 {
			t.Fatal(w.Body)
		}
		w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", jobrunner.Receipt{ID: cmds[0].ID, State: "completed", Time: time.Now().UTC().Format(time.RFC3339), Detail: "fixture"})
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		var n int
		s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&n)
		if (kind == "check" && n != 0) || (kind == "daily" && n != 1) {
			t.Fatal("notification policy", kind, n)
		}
	}
}

func TestManagedCompletionHeartbeatAndReceiptNotifyOnce(t *testing.T) {
	for _, heartbeatFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(heartbeatFirst), func(t *testing.T) {
			s, h, c, x := executionFixture(t)
			if e := s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "fixture-only-token", ChatID: "123"}}}); e != nil {
				t.Fatal(e)
			}
			w := call(h, "POST", "/api/v1/jobs", `{"account_id":"primary","type":"daily"}`, c, x)
			if w.Code != 202 {
				t.Fatal(w.Body)
			}
			w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
			var cmds []jobrunner.Command
			json.Unmarshal(w.Body.Bytes(), &cmds)
			if len(cmds) != 1 {
				t.Fatal(w.Body)
			}
			marker := model.Completion{ID: rules.Hash([]byte("exact-fixture-marker")), InstanceID: cmds[0].InstanceID, State: "completed", ObservedAt: time.Now().UTC().Format(time.RFC3339), Source: "fixture"}
			hb := model.Heartbeat{AgentID: "fixture-agent", Instances: []model.Instance{{ID: marker.InstanceID, Version: "6.20.0"}}, Completions: []model.Completion{marker}}
			if heartbeatFirst {
				if e := s.observedCompletions(hb); e != nil {
					t.Fatal(e)
				}
			}
			rec := jobrunner.Receipt{ID: cmds[0].ID, State: "completed", Time: marker.ObservedAt, Detail: "fixture", Completion: &marker}
			wrong := marker
			wrong.InstanceID = "fedcba9876543210"
			rec.Completion = &wrong
			if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 422 {
				t.Fatal("wrong instance evidence accepted")
			}
			rec.Completion = &marker
			if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
				t.Fatal(w.Body)
			}
			if !heartbeatFirst {
				if e := s.observedCompletions(hb); e != nil {
					t.Fatal(e)
				}
			}
			agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec)
			s.observedCompletions(hb)
			var n int
			s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&n)
			if n != 1 {
				t.Fatal("duplicate notifications", n)
			}
		})
	}
}
