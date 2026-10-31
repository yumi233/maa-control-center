package notify

import (
	"context"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/model"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTelegramProxyPinnedScope(t *testing.T) {
	for _, raw := range []string{"http://proxy.invalid:7890", "http://user:secret@192.168.1.1:7890", "http://169.254.169.254:80", "http://0.0.0.0:80", "http://192.168.1.1:7890/path"} {
		if _, e := TelegramProxyClient(raw); e == nil {
			t.Fatal("unsafe proxy accepted", raw)
		}
	}
	if _, e := TelegramProxyClient("http://192.168.1.1:7890"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	tr := telegramOnly{transportFunc(func(*http.Request) (*http.Response, error) { calls++; return &http.Response{StatusCode: 200}, nil })}
	for _, addr := range []string{"https://api.telegram.org/", "http://api.telegram.org/", "https://api.telegram.org.evil/", "https://192.168.1.1/", "https://api.telegram.org:8443/"} {
		r, _ := http.NewRequest("GET", addr, nil)
		_, e := tr.RoundTrip(r)
		if (addr == "https://api.telegram.org/") != (e == nil) {
			t.Fatal(addr, e)
		}
	}
	if calls != 1 {
		t.Fatal("unexpected egress", calls)
	}
}
func TestRecruitmentProminentFactualMessageAndNoAI(t *testing.T) {
	r := &model.Recruitment{Level: 6, Tags: []string{"高级资深干员"}, ObservedAt: "2026-10-31T02:29:01Z"}
	f := Facts{ID: "recruitment/fixture", Kind: "recruitment", Account: "主号", Recruitment: r}
	s := Service{}
	msg := s.Message(context.Background(), assistant.Settings{Creative: true, Provider: assistant.Provider{APIKey: "fixture", BaseURL: "http://invalid"}}, f)
	for _, x := range []string{"🚨", "6★", "主号", "尚未确认招募获得", "高级资深干员"} {
		if !strings.Contains(msg, x) {
			t.Fatal(msg)
		}
	}
	if accepts(assistant.Channel{AccountIDs: []string{"secondary"}}, Facts{Kind: "recruitment", AccountID: "primary"}) {
		t.Fatal("primary rare alert leaked into secondary QQ group")
	}
}
