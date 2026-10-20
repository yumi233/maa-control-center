package assistant

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/vault"
	"strings"
	"time"
)

type Channel struct {
	PublicOnly       bool   `json:"-"`
	Kind             string `json:"kind"`
	Enabled          bool   `json:"enabled"`
	Token            string `json:"token,omitempty"`
	Configured       bool   `json:"configured"`
	ChatID           string `json:"chat_id"`
	BaseURL          string `json:"base_url,omitempty"`
	TargetType       string `json:"target_type,omitempty"`
	TargetID         string `json:"target_id,omitempty"`
	AllowPrivateHTTP bool   `json:"allow_private_http"`
	ClearKey         bool   `json:"clear_key,omitempty"`
}
type Settings struct {
	Provider Provider  `json:"provider"`
	Channels []Channel `json:"channels"`
	Creative bool      `json:"creative"`
}
type SettingsStore struct {
	DB     *sql.DB
	Vault  *vault.Vault
	UserID string
}

func (s SettingsStore) ForUser(id string) SettingsStore { s.UserID = id; return s }
func (s SettingsStore) key() string {
	if s.UserID == "" || s.UserID == "admin" {
		return "default"
	}
	return s.UserID
}
func (s SettingsStore) Load() (Settings, error) {
	v := Settings{Channels: []Channel{{Kind: "telegram", TargetType: "private"}, {Kind: "qq", TargetType: "private"}}}
	var cipher string
	e := s.DB.QueryRow("SELECT encrypted FROM integration_settings WHERE id=?", s.key()).Scan(&cipher)
	if e == sql.ErrNoRows {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	if s.Vault == nil {
		return v, fmt.Errorf("密钥存储不可用")
	}
	b, e := s.Vault.Open(cipher)
	if e != nil {
		return v, fmt.Errorf("无法解密接入设置，请恢复配套密钥文件")
	}
	e = json.Unmarshal(b, &v)
	if s.restricted() {
		v.Provider.PublicOnly = true
		for i := range v.Channels {
			v.Channels[i].PublicOnly = true
		}
	}
	return v, e
}
func (v Settings) Public() Settings {
	v.Provider.Configured = v.Provider.APIKey != ""
	v.Provider.APIKey = ""
	c := append([]Channel{}, v.Channels...)
	for i := range c {
		c[i].Configured = c[i].Token != ""
		c[i].Token = ""
	}
	v.Channels = c
	return v
}
func (s SettingsStore) restricted() bool {
	if s.UserID == "" {
		return false
	}
	var role string
	return s.DB.QueryRow("SELECT role FROM users WHERE id=? AND disabled=0", s.UserID).Scan(&role) != nil || role != "admin"
}
func (s SettingsStore) Save(v Settings) error {
	if s.restricted() {
		if v.Provider.AllowPrivateHTTP {
			return fmt.Errorf("内网模型地址需要管理员配置")
		}
		for _, c := range v.Channels {
			if c.AllowPrivateHTTP {
				return fmt.Errorf("内网通知地址需要管理员配置")
			}
		}
	}

	v.Channels = append([]Channel{}, v.Channels...)
	old, e := s.Load()
	if e != nil {
		return e
	}
	if s.Vault == nil {
		return fmt.Errorf("密钥存储不可用")
	}
	if v.Provider.APIKey == "" && !v.Provider.ClearKey {
		v.Provider.APIKey = old.Provider.APIKey
	}
	if v.Provider.ClearKey {
		v.Provider.APIKey = ""
	}
	v.Provider.ClearKey = false
	v.Provider.Configured = false
	if len(v.Provider.APIKey) > 4096 || len(v.Provider.Model) > 200 || strings.ContainsAny(v.Provider.APIKey, "\r\n") {
		return fmt.Errorf("模型凭据格式无效")
	}
	if v.Provider.TokenLimitField != "" && v.Provider.TokenLimitField != "max_tokens" && v.Provider.TokenLimitField != "max_completion_tokens" {
		return fmt.Errorf("输出限额参数无效")
	}
	if v.Provider.BaseURL != "" {
		if e = ValidateURL(v.Provider.BaseURL, v.Provider.AllowPrivateHTTP); e != nil {
			return e
		}
	}
	if len(v.Channels) > 2 {
		return fmt.Errorf("仅支持 Telegram 和 QQ 两种渠道")
	}
	seen := map[string]bool{}
	for i := range v.Channels {
		c := &v.Channels[i]
		c.Token = strings.TrimSpace(c.Token)
		c.ChatID = strings.TrimSpace(c.ChatID)
		c.TargetID = strings.TrimSpace(c.TargetID)
		if (c.Kind != "telegram" && c.Kind != "qq") || seen[c.Kind] {
			return fmt.Errorf("通知渠道无效或重复")
		}
		seen[c.Kind] = true
		if c.Token == "" && !c.ClearKey {
			for _, prior := range old.Channels {
				if prior.Kind == c.Kind {
					c.Token = prior.Token
				}
			}
		}
		if c.ClearKey {
			c.Token = ""
		}
		c.ClearKey = false
		c.Configured = false
		if len(c.Token) > 4096 || strings.ContainsAny(c.Token, "\r\n") {
			return fmt.Errorf("通知凭据格式无效")
		}
		if c.Enabled {
			if c.Kind == "telegram" {
				if c.Token == "" || c.ChatID == "" || len(c.ChatID) > 128 {
					return fmt.Errorf("请填写 Telegram Bot Token 与 Chat ID")
				}
			} else {
				if c.TargetType != "private" && c.TargetType != "group" {
					return fmt.Errorf("QQ 目标请选择私聊或群聊")
				}
				if c.TargetID == "" || len(c.TargetID) > 32 {
					return fmt.Errorf("请填写 QQ 目标 ID")
				}
				if e = ValidateURL(c.BaseURL, c.AllowPrivateHTTP); e != nil {
					return e
				}
			}
		}
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	encrypted, e := s.Vault.Seal(b)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO integration_settings(id,encrypted,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET encrypted=excluded.encrypted,updated_at=excluded.updated_at", s.key(), encrypted, time.Now().UTC().Format(time.RFC3339))
	return e
}
