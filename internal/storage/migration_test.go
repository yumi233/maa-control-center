package storage

import (
	"database/sql"
	"github.com/yumi233/maa-control-center/migrations"
	"golang.org/x/crypto/bcrypt"
	"path/filepath"
	"testing"
)

func TestUpgradePreservesPasswordOwnershipBindingsAndOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(migrations.Initial + migrations.AgentMode); e != nil {
		t.Fatal(e)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("unchanged-old-password"), bcrypt.MinCost)
	_, e = db.Exec("INSERT INTO users(id,username,password_hash) VALUES('admin','admin',?); INSERT INTO accounts(id,name,role) VALUES('primary','existing name','primary'); INSERT INTO device_bindings VALUES('primary','agent','instance','profile'); INSERT INTO notification_outbox(id,event_id,channel,facts,state,created_at,updated_at) VALUES('old','event','telegram','{}','unknown','time','time')", string(hash))
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		st, e := Open(path, "do-not-change-the-old-password", "", "")
		if e != nil {
			t.Fatal(e)
		}
		var got, owner, state, profile string
		st.DB.QueryRow("SELECT password_hash FROM users WHERE id='admin'").Scan(&got)
		if got != string(hash) {
			t.Fatal("upgrade replaced password")
		}
		st.DB.QueryRow("SELECT owner_id FROM accounts WHERE id='primary'").Scan(&owner)
		if owner != "admin" {
			t.Fatal("existing account lost admin ownership")
		}
		st.DB.QueryRow("SELECT state FROM notification_outbox WHERE id='old'").Scan(&state)
		if state != "unknown" {
			t.Fatal("upgrade replayed uncertain notification")
		}
		st.DB.QueryRow("SELECT profile_id FROM device_bindings WHERE account_id='primary'").Scan(&profile)
		if profile != "profile" {
			t.Fatal("binding changed")
		}
		st.DB.Close()
	}
}
