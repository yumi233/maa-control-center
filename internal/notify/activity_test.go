package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/storage"
	"github.com/yumi233/maa-control-center/internal/vault"
)

func sampleActivity() *model.RunActivity {
	return &model.RunActivity{StartedAt: "2026-10-22T02:00:19Z", FinishedAt: "2026-10-22T02:33:34Z", Tasks: []string{"infrastructure", "recruit", "award"}, Stages: []model.StageActivity{{Stage: "2-10", Settlements: 10}}, Drops: []model.ActivityDrop{{ID: "sugar", Name: "糖", Quantity: 2}}, Sanity: &model.SanityReading{Current: 6, Maximum: 205, ObservedAt: "2026-10-22T02:24:57Z"}, LastBattleAt: "2026-10-22T02:24:43Z", LogsComplete: true}
}

func TestLifecycleMessageSanityLimits(t *testing.T) {
	a := sampleActivity()
	f := Facts{Kind: "job", State: "completed", ID: "job/fixture", Account: "测试账号", Time: a.FinishedAt, Activity: a, Today: &DayActivity{Date: "2026-10-22", Runs: 2, Completed: 2, Reports: 1, Tasks: map[string]int{"award": 1}}}
	msg := Body(f, "")
	for _, s := range []string{"最近实际理智：6/205", "结束时估算：约 7/205", "仅按每 6 分钟自然恢复", "10-23 06:18:57", "2-10 × 10", "明细覆盖 1/2", "今日任务结束记录：领取奖励 1 轮"} {
		if !strings.Contains(msg, s) {
			t.Fatalf("missing %s: %s", s, msg)
		}
	}
	a.LastBattleAt = "2026-10-22T02:30:00Z"
	if strings.Contains(Body(f, ""), "结束时估算") {
		t.Fatal("guessed sanity after later battle")
	}
	a.LastBattleAt = ""
	a.LogsComplete = false
	if strings.Contains(Body(f, ""), "结束时估算") {
		t.Fatal("guessed sanity with incomplete logs")
	}
	f.State = "running"
	if msg = Body(f, ""); !strings.Contains(msg, "挂机已开始") || strings.Contains(msg, "今日识别掉落") {
		t.Fatal(msg)
	}
}

func TestAccountScopeDedupAndDeliveryRecheck(t *testing.T) {
	st, e := storage.Open(filepath.Join(t.TempDir(), "route.db"), "fixture-password-12345", "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	v, _ := vault.New(make([]byte, 32))
	settings := assistant.SettingsStore{DB: st.DB, Vault: v}
	var received []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			UMO     string `json:"umo"`
			Message string `json:"message"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		received = append(received, p.Message)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer receiver.Close()
	c := assistant.Channel{Kind: "astrbot", Enabled: true, Token: "fixture", BaseURL: receiver.URL, AllowPrivateHTTP: true, TargetID: "fixture:GroupMessage:group", AccountIDs: []string{"secondary"}}
	if e = settings.Save(assistant.Settings{Channels: []assistant.Channel{c}}); e != nil {
		t.Fatal(e)
	}
	s := Service{Settings: settings, HTTP: receiver.Client()}
	for _, account := range []string{"primary", "secondary", ""} {
		for _, id := range []string{"job/" + account + "/started", "job/" + account} {
			f := Facts{ID: id, Kind: "job", AccountID: account, Account: account, State: "completed"}
			s.Queue(f)
			s.Queue(f)
		}
	}
	s.Drain(context.Background())
	if len(received) != 2 {
		t.Fatalf("wrong account or duplicate sent: %d", len(received))
	}
	// A queued legacy notification may no longer match the current channel scope.
	c.AccountIDs = nil
	settings.Save(assistant.Settings{Channels: []assistant.Channel{c}})
	s.Queue(Facts{ID: "old-primary", AccountID: "primary"})
	c.AccountIDs = []string{"secondary"}
	settings.Save(assistant.Settings{Channels: []assistant.Channel{c}})
	s.Drain(context.Background())
	if len(received) != 2 {
		t.Fatal("delivery did not recheck scope")
	}
}
