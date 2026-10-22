package assistant

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/vault"
	"strings"
	"time"
	"unicode"
)

type Channel struct {
	AccountIDs       []string `json:"account_ids,omitempty"`
	PublicOnly       bool     `json:"-"`
	Kind             string   `json:"kind"`
	Enabled          bool     `json:"enabled"`
	Token            string   `json:"token,omitempty"`
	Configured       bool     `json:"configured"`
	ChatID           string   `json:"chat_id"`
	BaseURL          string   `json:"base_url,omitempty"`
	TargetType       string   `json:"target_type,omitempty"`
	TargetID         string   `json:"target_id,omitempty"`
	AllowPrivateHTTP bool     `json:"allow_private_http"`
	ClearKey         bool     `json:"clear_key,omitempty"`
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
	v := Settings{Channels: []Channel{{Kind: "telegram", TargetType: "private"}, {Kind: "qq", TargetType: "private"}, {Kind: "astrbot"}}}
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
	if e != nil {
		return v, e
	}
	found := false
	for _, c := range v.Channels {
		found = found || c.Kind == "astrbot"
	}
	if !found {
		v.Channels = append(v.Channels, Channel{Kind: "astrbot"})
	}
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
	if len(v.Channels) > 3 {
		return fmt.Errorf("仅支持 Telegram、OneBot 和 AstrBot 三种接入")
	}
	seen := map[string]bool{}
	for i := range v.Channels {
		c := &v.Channels[i]
		c.Token = strings.TrimSpace(c.Token)
		c.ChatID = strings.TrimSpace(c.ChatID)
		c.TargetID = strings.TrimSpace(c.TargetID)
		c.BaseURL = strings.TrimSpace(c.BaseURL)
		if (c.Kind != "telegram" && c.Kind != "qq" && c.Kind != "astrbot") || seen[c.Kind] {
			return fmt.Errorf("通知渠道无效或重复")
		}
		seen[c.Kind] = true
		if len(c.AccountIDs) > 100 {
			return fmt.Errorf("通知账号范围过大")
		}
		accounts := map[string]bool{}
		for _, id := range c.AccountIDs {
			var accountOwner string
			if accounts[id] || s.DB.QueryRow("SELECT owner_id FROM accounts WHERE id=?", id).Scan(&accountOwner) != nil || (s.restricted() && accountOwner != s.UserID) {
				return fmt.Errorf("通知账号无效或无权访问")
			}
			accounts[id] = true
		}
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
			} else if c.Kind == "astrbot" {
				if c.Token == "" {
					return fmt.Errorf("请填写具有 im 权限的 AstrBot API Key")
				}
				if e = ValidateUMO(c.TargetID); e != nil {
					return e
				}
				if e = ValidateURL(c.BaseURL, c.AllowPrivateHTTP); e != nil {
					return e
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

// ValidateUMO accepts an AstrBot private or group session, never a raw QQ number.
func ValidateUMO(umo string) error {
	parts := strings.SplitN(umo, ":", 3)
	if len(umo) > 1024 || len(parts) != 3 || parts[0] == "" || parts[2] == "" ||
		(parts[1] != "FriendMessage" && parts[1] != "GroupMessage") ||
		strings.IndexFunc(umo, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("请填写 /sid 返回的完整 AstrBot UMO（平台:消息类型:会话 ID）")
	}
	return nil
}
