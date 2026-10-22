package assistant_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/storage"
	"github.com/yumi233/maa-control-center/internal/vault"
)

func TestAstrBotSettingsMigrationAndSecretPreservation(t *testing.T) {
	st, err := storage.Open(filepath.Join(t.TempDir(), "settings.db"), "fixture-password-12345", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	v, _ := vault.New(make([]byte, 32))
	s := assistant.SettingsStore{DB: st.DB, Vault: v}
	legacy := assistant.Settings{Creative: true, Channels: []assistant.Channel{
		{Kind: "telegram", Enabled: true, Token: "123:fixture_token", ChatID: "1"},
		{Kind: "qq", TargetType: "private"},
	}}
	if err = s.Save(legacy); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil || len(cfg.Channels) != 3 || cfg.Channels[2].Kind != "astrbot" || cfg.Channels[2].Enabled || !cfg.Creative {
		t.Fatalf("legacy load failed: %v", err)
	}
	cfg.Channels[2] = assistant.Channel{Kind: "astrbot", Enabled: true, Token: "abk_fixture_secret", BaseURL: "http://127.0.0.1:6185", TargetID: "fixture_bot:FriendMessage:fixture-session", AllowPrivateHTTP: true}
	if err = s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, _ := s.Load()
	public := loaded.Public()
	b, _ := json.Marshal(public)
	if strings.Contains(string(b), "fixture_secret") || !public.Channels[2].Configured {
		t.Fatal("AstrBot key exposed or not configured")
	}
	if err = s.Save(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ = s.Load()
	if loaded.Channels[2].Token != "abk_fixture_secret" || loaded.Channels[0].Token != "123:fixture_token" || !loaded.Creative {
		t.Fatal("saved credentials or existing settings changed")
	}
	public = loaded.Public()
	public.Channels[2].Enabled = false
	public.Channels[2].ClearKey = true
	if err = s.Save(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ = s.Load()
	if loaded.Channels[2].Token != "" || loaded.Channels[0].Token != "123:fixture_token" {
		t.Fatal("explicit clear affected other channel")
	}
}

func TestAstrBotUMOValidation(t *testing.T) {
	for _, valid := range []string{"fixture:FriendMessage:session", "fixture:GroupMessage:session:thread"} {
		if err := assistant.ValidateUMO(valid); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []string{"123456", "fixture:FriendMessage:", ":FriendMessage:session", "fixture:OtherMessage:session", "fixture:FriendMessage:session\n", "fixture:FriendMessage:" + strings.Repeat("x", 1024)} {
		if assistant.ValidateUMO(invalid) == nil {
			t.Fatal("invalid UMO accepted")
		}
	}
}
