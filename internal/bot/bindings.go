// Package bot provisions account-scoped machine credentials, independent of
// browser passwords, agent pairing and AstrBot's notification API key.
package bot

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/assistant"
	"strings"
	"time"
	"unicode"
)

type Member struct {
	UID   string `json:"uid"`
	Label string `json:"label"`
	Role  string `json:"role"`
}
type Binding struct {
	ID        string   `json:"id"`
	OwnerID   string   `json:"owner_id"`
	AccountID string   `json:"account_id"`
	UMO       string   `json:"umo"`
	Members   []Member `json:"members"`
	Revision  int      `json:"revision"`
	Enabled   bool     `json:"enabled"`
}

func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func random() string {
	v := make([]byte, 32)
	if _, e := rand.Read(v); e != nil {
		panic(e)
	}
	return hex.EncodeToString(v)
}
func ValidateMembers(members []Member) error {
	if len(members) == 0 || len(members) > 32 {
		return fmt.Errorf("请添加 1–32 名授权成员")
	}
	seen := map[string]bool{}
	for _, m := range members {
		u := m.UID
		if u == "" || len(u) > 128 || seen[u] || strings.IndexFunc(u, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || len(m.Label) > 120 || strings.ContainsAny(m.Label, "\r\n\x00") || (m.Role != "viewer" && m.Role != "operator") {
			return fmt.Errorf("成员 UID、备注或权限无效，UID 不可重复")
		}
		seen[u] = true
	}
	return nil
}
func Bind(db *sql.DB, b Binding) (Binding, string, error) {
	if assistant.ValidateUMO(b.UMO) != nil {
		return b, "", fmt.Errorf("请填写完整会话 UMO")
	}
	if e := ValidateMembers(b.Members); e != nil {
		return b, "", e
	}
	var count int
	if db.QueryRow("SELECT count(*) FROM accounts a JOIN users u ON u.id=a.owner_id WHERE a.id=? AND a.owner_id=? AND u.disabled=0", b.AccountID, b.OwnerID).Scan(&count) != nil || count != 1 {
		return b, "", fmt.Errorf("账号不属于有效用户")
	}
	b.ID = random()
	b.Enabled = true
	b.Revision = 1
	key := random()
	allowed, _ := json.Marshal(b.Members)
	_, e := db.Exec("INSERT INTO bot_bindings(id,owner_id,account_id,umo,members,token_hash,created_at) VALUES(?,?,?,?,?,?,?)", b.ID, b.OwnerID, b.AccountID, b.UMO, string(allowed), Hash(key), time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return b, "", fmt.Errorf("无法创建接入；如同会话已绑定该账号，请编辑现有成员或先撤销旧接入")
	}
	return b, key, e
}
