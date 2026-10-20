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
