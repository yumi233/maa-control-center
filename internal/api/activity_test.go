package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
)

func TestRunLifecycleNotificationsAtomicAndReplaySafe(t *testing.T) {
	s, h, c, x := executionFixture(t)
	if e := s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "123:fixture", ChatID: "1"}, {Kind: "astrbot", Enabled: true, Token: "fixture", BaseURL: "http://127.0.0.1:6185", AllowPrivateHTTP: true, TargetID: "fixture:GroupMessage:group", AccountIDs: []string{"secondary"}}}}); e != nil {
		t.Fatal(e)
	}
	for _, account := range []string{"primary", "secondary"} {
		w := call(h, "POST", "/api/v1/jobs", `{"account_id":"`+account+`","type":"daily"}`, c, x)
		if w.Code != 202 {
			t.Fatal(w.Body)
		}
		w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
		var commands []jobrunner.Command
		json.Unmarshal(w.Body.Bytes(), &commands)
		id := commands[0].ID
		now := time.Now().UTC().Truncate(time.Second)
		start := now.Add(-time.Minute)
		s.Store.DB.Exec("UPDATE jobs SET created_at=? WHERE id=?", start.Format(time.RFC3339), id)
		rec := jobrunner.Receipt{ID: id, State: "running", StartedAt: start.Format(time.RFC3339), Time: now.Format(time.RFC3339), Detail: "fixture"}
		for i := 0; i < 2; i++ {
			if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
				t.Fatal(w.Body)
			}
		}
		rec.State = "completed"
		rec.Activity = &model.RunActivity{StartedAt: rec.StartedAt, FinishedAt: rec.Time, Tasks: []string{"award"}, LogsComplete: true}
		for i := 0; i < 2; i++ {
			if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
				t.Fatal(w.Body)
			}
		}
		var reports int
		s.Store.DB.QueryRow("SELECT count(*) FROM job_activity WHERE job_id=?", id).Scan(&reports)
		if reports != 1 {
			t.Fatal("report not persisted once")
		}
	}
	var n int
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&n)
	if n != 6 {
		t.Fatalf("expected primary TG start/end, secondary TG/QQ start/end: %d", n)
	}
	var raw string
	s.Store.DB.QueryRow("SELECT facts FROM notification_outbox WHERE event_id NOT LIKE '%/started' AND channel='astrbot'").Scan(&raw)
	var facts notify.Facts
	json.Unmarshal([]byte(raw), &facts)
	if facts.AccountID != "secondary" || facts.Today == nil || facts.Today.Runs != 1 || facts.Today.Tasks["award"] != 1 {
		t.Fatalf("cross-account summary: %s", raw)
	}
	// Legacy completion backfill supplies telemetry, never sends again.
	s.Store.DB.Exec("DELETE FROM job_activity WHERE job_id=?", strings.TrimPrefix(facts.ID, "job/"))
	rec := jobrunner.Receipt{ID: strings.TrimPrefix(facts.ID, "job/"), State: "completed", Time: facts.Time, StartedAt: facts.StartedAt, Activity: facts.Activity}
	if w := agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
		t.Fatal(w.Body)
	}
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&n)
	if n != 6 {
		t.Fatal("backfill resent notification")
	}
	rec.Time = time.Now().Add(time.Hour).Format(time.RFC3339)
	rec.Activity = nil
	if w := agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 422 {
		t.Fatal("future report accepted")
	}
}

func TestTodayOwnerAndBeijingDateIsolation(t *testing.T) {
	s, _, _, _ := executionFixture(t)
	insert := func(id, owner, account, when string) {
		_, e := s.Store.DB.Exec("INSERT INTO jobs(id,account_id,type,state,idempotency_key,owner_id,created_at,updated_at) VALUES(?,?,'daily','completed',?,?,?,?)", id, account, id, owner, when, when)
		if e != nil {
			t.Fatal(e)
		}
	}
	insert("today", "admin", "secondary", "2026-10-21T16:01:00Z")
	insert("previous-date", "admin", "secondary", "2026-10-21T15:59:59Z")
	insert("other-owner", "other", "secondary", "2026-10-22T01:00:00Z")
	insert("other-account", "admin", "primary", "2026-10-22T01:00:00Z")
	insert("future", "admin", "secondary", "2026-10-22T04:00:00Z")
	d, e := notify.Today(s.Store.DB, "admin", "secondary", "2026-10-22T02:00:00Z")
	if e != nil || d.Runs != 1 || d.Date != "2026-10-22" || d.Reports != 0 {
		t.Fatalf("bad daily scope: %+v %v", d, e)
	}
}

func TestActivityAndStateRollBackIfNotificationCannotQueue(t *testing.T) {
	s, h, c, x := executionFixture(t)
	s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "123:fixture", ChatID: "1"}}})
	call(h, "POST", "/api/v1/jobs", `{"account_id":"secondary","type":"daily"}`, c, x)
	w := agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	var commands []jobrunner.Command
	json.Unmarshal(w.Body.Bytes(), &commands)
	id := commands[0].ID
	now := time.Now().UTC().Format(time.RFC3339)
	s.Store.DB.Exec("CREATE TRIGGER block_outbox BEFORE INSERT ON notification_outbox BEGIN SELECT RAISE(ABORT,'fixture failure'); END")
	rec := jobrunner.Receipt{ID: id, State: "completed", Time: now, StartedAt: now, Activity: &model.RunActivity{StartedAt: now, FinishedAt: now, Tasks: []string{"award"}}}
	if w = agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code < 500 {
		t.Fatal("injected failure accepted", w.Body)
	}
	var state string
	var reports int
	s.Store.DB.QueryRow("SELECT state FROM jobs WHERE id=?", id).Scan(&state)
	s.Store.DB.QueryRow("SELECT count(*) FROM job_activity").Scan(&reports)
	if state != "dispatched" || reports != 0 {
		t.Fatalf("partial commit: %s %d", state, reports)
	}
}
