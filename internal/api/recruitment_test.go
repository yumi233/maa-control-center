package api

import (
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
	"github.com/yumi233/maa-control-center/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRareRecruitmentDurableDedupAndJobAccountRouting(t *testing.T) {
	s, h, c, x := executionFixture(t)
	if e := s.Integrations.Save(assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "123:fixture", ChatID: "1"}, {Kind: "astrbot", Enabled: true, Token: "fixture", BaseURL: "http://127.0.0.1:6185", AllowPrivateHTTP: true, TargetID: "fixture:GroupMessage:group", AccountIDs: []string{"secondary"}}}}); e != nil {
		t.Fatal(e)
	}
	for _, account := range []string{"primary", "secondary"} {
		if w := call(h, "POST", "/api/v1/jobs", `{"account_id":"`+account+`","type":"daily"}`, c, x); w.Code != 202 {
			t.Fatal(w.Body)
		}
		w := agentRequest(h, "GET", "/api/v1/agent/jobs/commands", nil)
		var cmds []jobrunner.Command
		json.Unmarshal(w.Body.Bytes(), &cmds)
		cmd := cmds[0]
		now := time.Now().UTC()
		s.Store.DB.Exec("UPDATE jobs SET state='running',created_at=? WHERE id=?", now.Add(-time.Minute).Format(time.RFC3339Nano), cmd.ID)
		r := model.Recruitment{ID: strings.Repeat("a", 64), JobID: cmd.ID, InstanceID: cmd.InstanceID, Profile: cmd.Profile, ObservedAt: now.Format(time.RFC3339Nano), Level: 5, Tags: []string{"重装干员", "辅助干员", "控场", "位移", "支援机械"}, Combinations: []model.RecruitCombination{{Level: 5, Tags: []string{"重装干员", "位移"}}}}
		hb := model.Heartbeat{AgentID: "fixture-agent", Recruitments: []model.Recruitment{r}}
		for i := 0; i < 3; i++ {
			if e := s.observedRecruitments(hb); e != nil {
				t.Fatal(e)
			}
		}
		hb.Recruitments[0].Profile = "wrong"
		if s.observedRecruitments(hb) == nil {
			t.Fatal("profile spoof accepted")
		}
		s.Store.DB.Exec("UPDATE jobs SET state='completed' WHERE id=?", cmd.ID)
	}
	var n int
	s.Store.DB.QueryRow("SELECT count(*) FROM notification_outbox WHERE event_id LIKE 'recruitment/%'").Scan(&n)
	if n != 3 {
		t.Fatal("dedup/account routing failed", n)
	}
	s.Store.DB.QueryRow("SELECT count(*) FROM observed_recruitments").Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
}
