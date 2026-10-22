package notify

import (
	"context"
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/storage"
	"github.com/yumi233/maa-control-center/internal/vault"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPersistentDedupDeliveryAndEncryptedCredentials(t *testing.T) {
	st, e := storage.Open(filepath.Join(t.TempDir(), "notify.db"), "fixture-password-12345", "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	v, _ := vault.New(make([]byte, 32))
	settings := assistant.SettingsStore{DB: st.DB, Vault: v}
	var sent atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		sent.Add(1)
		if r.URL.Path == "/bot123:fixture_token/sendMessage" {
			if !strings.Contains(body["text"].(string), "状态：completed_with_errors") {
				t.Error("facts missing")
			}
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		} else if r.URL.Path == "/send_group_msg" {
			if r.Header.Get("Authorization") != "Bearer fixture-qq-token" {
				t.Error("QQ credential missing")
			}
			if _, ok := body["message"].([]any); !ok {
				t.Error("QQ message must be text segments, never CQ")
			}
			w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":2}}`))
		} else {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer receiver.Close()
	cfg := assistant.Settings{Channels: []assistant.Channel{{Kind: "telegram", Enabled: true, Token: "123:fixture_token", ChatID: "100"}, {Kind: "qq", Enabled: true, Token: "fixture-qq-token", BaseURL: receiver.URL, AllowPrivateHTTP: true, TargetType: "group", TargetID: "200"}}}
	if e = settings.Save(cfg); e != nil {
		t.Fatal(e)
	}
	var cipher string
	st.DB.QueryRow("SELECT encrypted FROM integration_settings").Scan(&cipher)
	if strings.Contains(cipher, "fixture") {
		t.Fatal("plaintext credentials stored")
	}
	loaded, _ := settings.Load()
	pub, _ := json.Marshal(loaded.Public())
	if strings.Contains(string(pub), "fixture_token") || strings.Contains(string(pub), "fixture-qq-token") {
		t.Fatal("credential returned by GET")
	}
	s := Service{Settings: settings, TelegramBase: receiver.URL, HTTP: receiver.Client()}
	f := Facts{ID: "fixture-event", Kind: "maa_queue", Account: "unattributed fixture", State: "completed_with_errors", Time: "fixture-time", Detail: "known error", Source: "fixture queue log"}
	if e = s.Queue(f); e != nil {
		t.Fatal(e)
	}
	s.Queue(f)
	s.Drain(context.Background())
	s.Drain(context.Background())
	if sent.Load() != 2 {
		t.Fatalf("dedup failed %d", sent.Load())
	}
	var count int
	st.DB.QueryRow("SELECT count(*) FROM notification_outbox WHERE state='sent'").Scan(&count)
	if count != 2 {
		t.Fatal("delivery receipts missing")
	}
	// Saving blank fields preserves existing keys; explicit deletion removes them.
	p := loaded.Public()
	if e = settings.Save(p); e != nil {
		t.Fatal(e)
	}
	again, _ := settings.Load()
	if again.Channels[0].Token != "123:fixture_token" {
		t.Fatal("blank key erased saved credential")
	}
	p.Provider.ClearKey = true
	p.Channels[0].Enabled = false
	p.Channels[0].ClearKey = true
	settings.Save(p)
	again, _ = settings.Load()
	if again.Channels[0].Token != "" {
		t.Fatal("explicit key clear ignored")
	}
}
func TestNotificationUnknownIsNotRetriedAndModelCannotRewriteFacts(t *testing.T) {
	st, _ := storage.Open(filepath.Join(t.TempDir(), "notify.db"), "fixture-password-12345", "", "")
	defer st.DB.Close()
	v, _ := vault.New(make([]byte, 32))
	settings := assistant.SettingsStore{DB: st.DB, Vault: v}
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(503)
		w.Write([]byte("private channel details must not leak"))
	}))
	defer receiver.Close()
	cfg := assistant.Settings{Channels: []assistant.Channel{{Kind: "qq", Enabled: true, BaseURL: receiver.URL, AllowPrivateHTTP: true, TargetType: "private", TargetID: "1"}}}
	settings.Save(cfg)
	s := Service{Settings: settings, HTTP: receiver.Client()}
	f := Facts{ID: "unknown-event", State: "unknown", Time: "fact-time", Detail: "result unknown", Account: "fixture", Source: "fixture"}
	s.Queue(f)
	s.Drain(context.Background())
	s.Drain(context.Background())
	if hits.Load() != 1 {
		t.Fatal("unknown delivery replayed")
	}
	var id, state, detail string
	st.DB.QueryRow("SELECT id,state,detail FROM notification_outbox").Scan(&id, &state, &detail)
	if state != "unknown" || strings.Contains(detail, "private channel") {
		t.Fatal("wrong failure handling")
	}
	if e := s.Retry(id); e != nil {
		t.Fatal(e)
	}
	s.Drain(context.Background())
	if hits.Load() != 2 {
		t.Fatal("explicit retry missing")
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"tone":"all-success", "state":"completed", "detail":"invented reward"}`}}}})
	}))
	defer model.Close()
	cfg.Creative = true
	cfg.Provider = assistant.Provider{BaseURL: model.URL, Model: "fixture-model", APIKey: "fixture", AllowPrivateHTTP: true}
	s.API = assistant.ClientAPI{HTTP: model.Client()}
	text := s.Message(context.Background(), cfg, f)
	if text != Body(f, "") || strings.Contains(text, "invented") {
		t.Fatal("model overwrote facts")
	}
}

func TestTelegramRejectionsAndPreSendFailures(t *testing.T) {
	for _, test := range []struct {
		code           int
		body, expected string
	}{{401, `{"ok":false,"error_code":401}`, "Token"}, {403, `{"ok":false,"error_code":403}`, "开始对话"}, {400, `{"ok":false,"description":"Bad Request: chat not found private-fixture"}`, "Chat ID"}, {429, `{"ok":false,"error_code":429}`, "频繁"}} {
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.code); w.Write([]byte(test.body)) }))
		service := Service{TelegramBase: receiver.URL, HTTP: receiver.Client()}
		state, e := service.Send(context.Background(), assistant.Channel{Kind: "telegram", Token: "123:fixture_token", ChatID: "1"}, "fixture")
		receiver.Close()
		if state != "failed" || e == nil || !strings.Contains(e.Error(), test.expected) || strings.Contains(e.Error(), "private-fixture") || strings.Contains(e.Error(), "fixture_token") {
			t.Fatalf("unsafe or vague rejection %s %v", state, e)
		}
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	address := receiver.URL
	receiver.Close()
	service := Service{TelegramBase: address}
	state, e := service.Send(context.Background(), assistant.Channel{Kind: "telegram", Token: "123:fixture_token", ChatID: "1"}, "fixture")
	if state != "failed" || e == nil || !strings.Contains(e.Error(), "尚未发送") {
		t.Fatalf("pre-send failure mistaken as delivered/unknown: %s %v", state, e)
	}
}

func TestAstrBotSendContractAndAmbiguousResponses(t *testing.T) {
	for _, tc := range []struct {
		body, state string
		code        int
	}{
		{`{"status":"ok","data":{}}`, "sent", 200},
		{`{"status":"error","message":"private server detail"}`, "failed", 200},
		{`{"ok":true}`, "unknown", 200},
		{`not json`, "unknown", 200},
		{`{"status":"ok"}`, "unknown", 503},
		{`private server detail`, "failed", 403},
	} {
		t.Run(tc.state+tc.body, func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/im/message" || r.Header.Get("Authorization") != "Bearer abk_fixture_secret" {
					t.Error("wrong AstrBot request")
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["umo"] != "fixture:FriendMessage:session" || body["message"] != "plain [CQ:at,qq=1] text" {
					t.Error("wrong message or UMO")
				}
				w.WriteHeader(tc.code)
				w.Write([]byte(tc.body))
			}))
			defer receiver.Close()
			s := Service{HTTP: receiver.Client()}
			state, err := s.Send(context.Background(), assistant.Channel{Kind: "astrbot", Token: "abk_fixture_secret", BaseURL: receiver.URL, AllowPrivateHTTP: true, TargetID: "fixture:FriendMessage:session"}, "plain [CQ:at,qq=1] text")
			if state != tc.state || (state == "sent") != (err == nil) {
				t.Fatalf("%s %v", state, err)
			}
			if err != nil && (strings.Contains(err.Error(), "private server detail") || strings.Contains(err.Error(), "fixture_secret")) {
				t.Fatal("secret or private response leaked")
			}
		})
	}
}

func TestAstrBotUnknownIsNotRetriedAndTestDoesNotCallModel(t *testing.T) {
	st, err := storage.Open(filepath.Join(t.TempDir(), "notify.db"), "fixture-password-12345", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	v, _ := vault.New(make([]byte, 32))
	settings := assistant.SettingsStore{DB: st.DB, Vault: v}
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"status":"indeterminate"}`))
	}))
	defer receiver.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("notification test called a model") }))
	defer model.Close()
	cfg := assistant.Settings{Creative: true, Provider: assistant.Provider{BaseURL: model.URL, Model: "fixture", APIKey: "fixture", AllowPrivateHTTP: true}, Channels: []assistant.Channel{{Kind: "astrbot", Enabled: true, Token: "abk_fixture", BaseURL: receiver.URL, AllowPrivateHTTP: true, TargetID: "fixture:FriendMessage:session"}}}
	if err = settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := Service{Settings: settings, HTTP: receiver.Client(), API: assistant.ClientAPI{HTTP: model.Client()}}
	if err = s.Queue(Facts{ID: "astrbot-test", Kind: "test", State: "test"}); err != nil {
		t.Fatal(err)
	}
	s.Drain(context.Background())
	s.Drain(context.Background())
	var state string
	st.DB.QueryRow("SELECT state FROM notification_outbox WHERE channel='astrbot'").Scan(&state)
	if hits.Load() != 1 || state != "unknown" {
		t.Fatal("ambiguous AstrBot delivery replayed")
	}
}
