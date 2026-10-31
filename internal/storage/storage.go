package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/migrations"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(path, password, primary, secondary string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	if _, err = db.Exec("PRAGMA busy_timeout=5000;" + migrations.Initial); err != nil {
		return fail(err)
	}
	if _, err = db.Exec(migrations.AgentMode); err != nil {
		return fail(err)
	}
	var migrated int
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=3").Scan(&migrated); err != nil {
		return fail(err)
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(migrations.MultiUser); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=4").Scan(&migrated); err != nil {
		return fail(err)
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(migrations.Execution); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=5").Scan(&migrated); err != nil {
		return fail(err)
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(migrations.Activity); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=6").Scan(&migrated); err != nil {
		return fail(err)
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(migrations.BotControl); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=7").Scan(&migrated); err != nil {
		return fail(err)
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(migrations.Recruitment); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if _, err = db.Exec("UPDATE notification_outbox SET state='unknown',detail='服务重启，发送结果待核实；可人工重试' WHERE state='sending'"); err != nil {
		return fail(err)
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			return fail(err)
		}
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
		return fail(err)
	}
	if count == 0 {
		if len(password) < 12 || len(password) > 72 {
			return fail(fmt.Errorf("first start requires MAACC_ADMIN_PASSWORD with 12–72 bytes"))
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fail(err)
		}
		if _, err = db.Exec("INSERT INTO users(id,username,password_hash) VALUES('admin','admin',?)", string(hash)); err != nil {
			return fail(err)
		}
	}
	if primary == "" {
		primary = "主账号"
	}
	if secondary == "" {
		secondary = "副账号"
	}
	for _, a := range []struct{ id, name, role string }{{"primary", primary, "primary"}, {"secondary", secondary, "secondary"}} {
		if _, err = db.Exec("INSERT OR IGNORE INTO accounts(id,name,role) VALUES(?,?,?)", a.id, a.name, a.role); err != nil {
			return fail(err)
		}
	}
	if _, err = db.Exec("UPDATE jobs SET state='unknown', detail='服务重启后需要人工核实；不会自动重发',updated_at=? WHERE state IN ('dispatched','waiting','starting','running','stopping')", now()); err != nil {
		return fail(err)
	}
	return &Store{db}, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
func (s *Store) Event(level, account, message string) error {
	_, err := s.DB.Exec("INSERT INTO events(time,level,account_id,message) VALUES(?,?,?,?)", now(), level, account, message)
	return err
}
func (s *Store) Events() ([]model.Event, error) {
	rows, err := s.DB.Query("SELECT id,time,level,account_id,message FROM events ORDER BY id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		if err = rows.Scan(&e.ID, &e.Time, &e.Level, &e.AccountID, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Heartbeat(h model.Heartbeat) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prior int
	if err = tx.QueryRow("SELECT count(*) FROM agents WHERE id=?", h.AgentID).Scan(&prior); err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO agents(id,hostname,os,version,last_seen) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname,os=excluded.os,version=excluded.version,last_seen=excluded.last_seen", h.AgentID, h.Hostname, h.OS, h.Version, time.Now().Unix())
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM maa_instances WHERE agent_id=?", h.AgentID); err != nil {
		return err
	}
	for _, inst := range h.Instances {
		data, err := json.Marshal(inst)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO maa_instances(agent_id,id,snapshot) VALUES(?,?,?)", h.AgentID, inst.ID, string(data)); err != nil {
			return err
		}
	}
	if prior == 0 {
		if _, err = tx.Exec("INSERT INTO events(time,level,message) VALUES(?,'info',?)", now(), "Windows Agent 已连接；已接收只读发现结果"); err != nil {
			return err
		}
	}
	// Keep the new application's own history bounded.
	if _, err = tx.Exec("DELETE FROM events WHERE id < (SELECT coalesce(max(id),0)-5000 FROM events)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Bind(b model.Binding) error {
	var snapshot string
	if err := s.DB.QueryRow("SELECT snapshot FROM maa_instances WHERE agent_id=? AND id=?", b.AgentID, b.InstanceID).Scan(&snapshot); err != nil {
		return fmt.Errorf("instance not discovered")
	}
	var inst model.Instance
	if err := json.Unmarshal([]byte(snapshot), &inst); err != nil {
		return err
	}
	found := false
	for _, p := range inst.Profiles {
		if p.ID == b.ProfileID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("profile not discovered")
	}
	res, err := s.DB.Exec("INSERT INTO device_bindings(account_id,agent_id,instance_id,profile_id) SELECT ?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM config_plans WHERE account_id=? AND state IN ('queued','dispatched','unknown')) ON CONFLICT(account_id) DO UPDATE SET agent_id=excluded.agent_id,instance_id=excluded.instance_id,profile_id=excluded.profile_id", b.AccountID, b.AgentID, b.InstanceID, b.ProfileID, b.AccountID)
	if err == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			return fmt.Errorf("account busy")
		}
	}
	return err
}

func (s *Store) Accounts() ([]model.Account, error) {
	rows, err := s.DB.Query(`SELECT a.id,a.name,a.role,a.owner_id,b.profile_id,ag.last_seen,m.snapshot
 FROM accounts a LEFT JOIN device_bindings b ON b.account_id=a.id
 LEFT JOIN agents ag ON ag.id=b.agent_id
 LEFT JOIN maa_instances m ON m.agent_id=b.agent_id AND m.id=b.instance_id ORDER BY a.role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Account{}
	for rows.Next() {
		var a model.Account
		var pid, snap sql.NullString
		var seen sql.NullInt64
		if err = rows.Scan(&a.ID, &a.Name, &a.Role, &a.OwnerID, &pid, &seen, &snap); err != nil {
			return nil, err
		}
		a.Status = "unknown"
		a.Policy.Source = "not_imported"
		if snap.Valid {
			var inst model.Instance
			if err = json.Unmarshal([]byte(snap.String), &inst); err != nil {
				return nil, err
			}
			a.Instance = &inst
			for _, p := range inst.Profiles {
				if p.ID == pid.String {
					pCopy := p
					a.Profile = &pCopy
					a.Bound = true
					a.Policy = p.Policy
				}
			}
		}
		if seen.Valid {
			a.LastSeen = time.Unix(seen.Int64, 0).UTC().Format(time.RFC3339)
			a.AgentOnline = time.Now().Unix()-seen.Int64 < 65
		}
		if a.Bound && (!a.AgentOnline || (a.Instance != nil && !a.Instance.ProcessRunning)) {
			a.Status = "offline"
		}
		if a.Instance != nil && a.Instance.State == "error" {
			a.Status = "error"
		}
		// A running process is deliberately not evidence of an active game task.
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Discovered() ([]map[string]any, error) {
	rows, err := s.DB.Query("SELECT a.id,a.hostname,a.os,a.version,a.last_seen,m.snapshot FROM agents a LEFT JOIN maa_instances m ON a.id=m.agent_id ORDER BY a.id,m.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, host, osName, version string
		var seen int64
		var raw sql.NullString
		if err = rows.Scan(&id, &host, &osName, &version, &seen, &raw); err != nil {
			return nil, err
		}
		var inst any
		if raw.Valid {
			if err = json.Unmarshal([]byte(raw.String), &inst); err != nil {
				return nil, err
			}
		}
		out = append(out, map[string]any{"agent_id": id, "hostname": host, "os": osName, "version": version, "last_seen": time.Unix(seen, 0).UTC().Format(time.RFC3339), "online": time.Now().Unix()-seen < 65, "instance": inst})
	}
	return out, rows.Err()
}
