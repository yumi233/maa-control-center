package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
	"github.com/yumi233/maa-control-center/internal/notify"
)

func TestQueueHealthExpiresBehindUnknownAndRoutesAlertsOnce(t *testing.T) {
	s, h, c, x := executionFixture(t)
	if e := s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{
		{Kind: "telegram", Enabled: true, Token: "fixture-only-token", ChatID: "123"},
		{Kind: "astrbot", Enabled: true, BaseURL: "http://127.0.0.1:6190", Token: "fixture-only-token", TargetType: "umo", TargetID: "fixture:GroupMessage:group", AllowPrivateHTTP: true, AccountIDs: []string{"secondary"}},
	}}); e != nil {
		t.Fatal(e)
	}
	for _, account := range []string{"primary", "secondary", "secondary"} {
		if w := call(h, "POST", "/api/v1/jobs", `{"account_id":"`+account+`","type":"daily"}`, c, x); w.Code != 202 {
			t.Fatal(w.Body)
		}
	}
	s.Store.DB.Exec("UPDATE jobs SET created_at=? WHERE account_id='primary'", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
	w := agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	var commands []jobrunner.Command
	json.Unmarshal(w.Body.Bytes(), &commands)
	if len(commands) != 1 {
		t.Fatal(w.Body)
	}
	id := commands[0].ID
	now := time.Now().UTC().Truncate(time.Second)
	rec := jobrunner.Receipt{ID: id, State: "unknown", Time: now.Format(time.RFC3339), Detail: "模拟器/ADB连接中断，原生重连失败"}
	for range 2 {
		if w := agentRequest(h, "POST", "/api/v1/agent/jobs/receipts", rec); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	var pending []string
	rows, _ := s.Store.DB.Query("SELECT id FROM jobs WHERE state='pending' ORDER BY id")
	for rows.Next() {
		var v string
		rows.Scan(&v)
		pending = append(pending, v)
	}
	rows.Close()
	if len(pending) != 2 {
		t.Fatal(pending)
	}
	s.Store.DB.Exec("UPDATE jobs SET created_at=? WHERE state='pending'", now.Add(-20*time.Minute).Format(time.RFC3339))
	s.Store.DB.Exec("UPDATE jobs SET expires_at=? WHERE id=?", now.Add(-time.Minute).Format(time.RFC3339), pending[0])
	for range 3 {
		if e := s.maintainJobQueue(now); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	s.Store.DB.QueryRow("SELECT state FROM jobs WHERE id=?", pending[0]).Scan(&state)
	if state != "expired" {
		t.Fatal("expired job retained behind safety lock", state)
	}
	var unknown, queued, leaked int
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox WHERE event_id=?", "job/"+id+"/alert/unknown").Scan(&unknown)
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox WHERE event_id=?", "job/"+pending[1]+"/alert/queue-stalled").Scan(&queued)
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox WHERE event_id=? AND channel='astrbot'", "job/"+id+"/alert/unknown").Scan(&leaked)
	if unknown != 1 || queued != 2 || leaked != 0 {
		t.Fatalf("duplicate or misrouted alerts: unknown=%d queue=%d leaked=%d", unknown, queued, leaked)
	}
	w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	json.Unmarshal(w.Body.Bytes(), &commands)
	if len(commands) != 1 || commands[0].ID != id || !commands[0].Monitor {
		t.Fatal("unknown lock was bypassed", w.Body)
	}
	// Releasing the old task must still never dispatch the expired schedule.
	s.Store.DB.Exec("UPDATE jobs SET state='failed' WHERE id=?", id)
	w = agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	json.Unmarshal(w.Body.Bytes(), &commands)
	if len(commands) != 1 || commands[0].ID != pending[1] {
		t.Fatal("stale run replayed", w.Body)
	}
	body := notify.Body(notify.Facts{Kind: "job_alert", State: "queue_stalled", Account: "廖宇涵", Detail: "queue stopped", Time: now.Format(time.RFC3339)}, "")
	if !strings.Contains(body, "理智：未取得结束时实测") || !strings.Contains(body, "队列受阻") {
		t.Fatal(body)
	}
}

func TestHealthyLongRunningQueueDoesNotAlertForNormalSerialization(t *testing.T) {
	s, h, c, x := executionFixture(t)
	for _, a := range []string{"primary", "secondary"} {
		call(h, "POST", "/api/v1/jobs", `{"account_id":"`+a+`","type":"daily"}`, c, x)
	}
	agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
	now := time.Now().UTC().Truncate(time.Second)
	s.Store.DB.Exec("UPDATE jobs SET state='running',updated_at=? WHERE state='dispatched'", now.Format(time.RFC3339))
	s.Store.DB.Exec("UPDATE jobs SET created_at=? WHERE state='pending'", now.Add(-20*time.Minute).Format(time.RFC3339))
	if e := s.maintainJobQueue(now); e != nil {
		t.Fatal(e)
	}
	var n int
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox").Scan(&n)
	if n != 0 {
		t.Fatal("normal shared emulator wait misreported as stuck")
	}
}
