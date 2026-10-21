package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/yumi233/maa-control-center/internal/jobrunner"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"github.com/yumi233/maa-control-center/internal/scheduler"
)

type Schedule struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	OwnerID   string `json:"owner_id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	Weekdays  []int  `json:"weekdays"`
	Timezone  string `json:"timezone"`
	NextRunAt string `json:"next_run_at"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
type Job struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	OwnerID         string `json:"owner_id"`
	CreatedBy       string `json:"created_by"`
	AgentID         string `json:"agent_id"`
	InstanceID      string `json:"instance_id"`
	ProfileID       string `json:"profile_id"`
	Role            string `json:"role"`
	Type            string `json:"type"`
	State           string `json:"state"`
	Detail          string `json:"detail"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	ExpiresAt       string `json:"expires_at"`
	ScheduleID      string `json:"schedule_id"`
	CancelRequested int    `json:"cancel_requested"`
	Progress        int    `json:"progress"`
}

const activeJobs = "('dispatched','waiting','starting','running','stopping','unknown')"

func (s *Server) executionRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/schedules", s.protect(func(w http.ResponseWriter, r *http.Request) { v, e := s.schedules(r); s.result(w, v, e) }))
	m.HandleFunc("POST /api/v1/schedules", s.protect(s.saveSchedule))
	m.HandleFunc("PUT /api/v1/schedules/{id}", s.protect(s.saveSchedule))
	m.HandleFunc("DELETE /api/v1/schedules/{id}", s.protect(func(w http.ResponseWriter, r *http.Request) {
		schedules, e := s.schedules(r)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		for _, v := range schedules {
			if v.ID == r.PathValue("id") {
				_, e = s.Store.DB.Exec("DELETE FROM scheduled_tasks WHERE id=?", v.ID)
				s.result(w, map[string]bool{"ok": true}, e)
				return
			}
		}
		problem(w, 404, "not_found", "定时计划不存在")
	}))
	m.HandleFunc("GET /api/v1/jobs", s.protect(func(w http.ResponseWriter, r *http.Request) { v, e := s.jobsFor(r); s.result(w, v, e) }))
	m.HandleFunc("GET /api/v1/jobs/{id}", s.protect(func(w http.ResponseWriter, r *http.Request) {
		j := s.jobFor(w, r)
		if j != nil {
			send(w, 200, j)
		}
	}))
	m.HandleFunc("POST /api/v1/jobs", s.protect(s.createJob))
	m.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.protect(func(w http.ResponseWriter, r *http.Request) {
		j := s.jobFor(w, r)
		if j == nil {
			return
		}
		state := j.State
		if state == "pending" {
			state = "cancelled"
		} else if state == "completed" || state == "failed" || state == "cancelled" || state == "expired" || state == "completed_with_errors" {
			problem(w, 409, "finished", "任务已结束")
			return
		}
		_, e := s.Store.DB.Exec("UPDATE jobs SET state=?,cancel_requested=1,updated_at=? WHERE id=? AND state=?", state, time.Now().UTC().Format(time.RFC3339), j.ID, j.State)
		s.result(w, map[string]string{"state": state}, e)
	}))
	m.HandleFunc("POST /api/v1/jobs/{id}/resolve", s.protect(func(w http.ResponseWriter, r *http.Request) {
		j := s.jobFor(w, r)
		if j == nil {
			return
		}
		var in struct {
			Confirm string `json:"confirm"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Confirm != j.ID || j.State != "unknown" {
			problem(w, 409, "confirmation_required", "仅可核实并释放结果待确认的任务")
			return
		}
		_, e := s.Store.DB.Exec("UPDATE jobs SET cancel_requested=2,updated_at=? WHERE id=? AND state='unknown'", time.Now().UTC().Format(time.RFC3339), j.ID)
		s.result(w, map[string]string{"message": "已请求 Windows 核实空闲并释放；不会重跑"}, e)
	}))
	m.HandleFunc("GET /api/v1/agent/jobs/commands", s.agentOnly(s.jobCommands))
	m.HandleFunc("POST /api/v1/agent/jobs/receipts", s.agentOnly(s.jobReceipt))
}
func (s *Server) schedules(r *http.Request) ([]Schedule, error) {
	u := s.currentUser(r)
	q := "SELECT t.id,t.account_id,t.owner_id,t.name,t.enabled,t.hour,t.minute,t.weekdays,t.timezone,t.next_run_at,t.created_at,t.updated_at FROM scheduled_tasks t JOIN accounts a ON a.id=t.account_id"
	args := []any{}
	if u.Role != "admin" {
		q += " WHERE t.owner_id=? AND a.owner_id=?"
		args = append(args, u.ID, u.ID)
	}
	q += " ORDER BY t.next_run_at,t.id"
	rows, e := s.Store.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var v Schedule
		var days string
		if e = rows.Scan(&v.ID, &v.AccountID, &v.OwnerID, &v.Name, &v.Enabled, &v.Hour, &v.Minute, &days, &v.Timezone, &v.NextRunAt, &v.CreatedAt, &v.UpdatedAt); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(days), &v.Weekdays) != nil {
			return nil, fmt.Errorf("invalid schedule")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var v Schedule
	if !decode(w, r, &v) {
		return
	}
	a := s.accountID(w, r, v.AccountID)
	if a == nil {
		return
	}
	if len(v.Name) == 0 || len(v.Name) > 120 {
		problem(w, 422, "invalid_schedule", "请填写计划名称")
		return
	}
	if _, e := s.bound(a); e != nil {
		problem(w, 422, "not_ready", e.Error())
		return
	}
	if a.Instance == nil || !a.Instance.TaskExecution {
		problem(w, 422, "agent_upgrade", "请先启用新版 Windows 任务控制")
		return
	}
	v.Timezone = "Asia/Shanghai"
	next, e := scheduler.Next(time.Now(), v.Hour, v.Minute, v.Weekdays, v.Timezone)
	if e != nil {
		problem(w, 422, "invalid_schedule", e.Error())
		return
	}
	v.ID = r.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339)
	days, _ := json.Marshal(v.Weekdays)
	if v.ID == "" {
		var count int
		_ = s.Store.DB.QueryRow("SELECT count(*) FROM scheduled_tasks WHERE owner_id=?", a.OwnerID).Scan(&count)
		if count >= 64 {
			problem(w, 422, "schedule_limit", "每用户最多64条计划")
			return
		}
		v.ID = token()
		v.CreatedAt = now
		_, e = s.Store.DB.Exec("INSERT INTO scheduled_tasks(id,account_id,owner_id,name,enabled,hour,minute,weekdays,timezone,next_run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, a.ID, a.OwnerID, v.Name, v.Enabled, v.Hour, v.Minute, string(days), v.Timezone, next.Format(time.RFC3339), now, now)
	} else {
		all, err := s.schedules(r)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		found := false
		for _, x := range all {
			if x.ID == v.ID && x.AccountID == a.ID {
				found = true
				v.CreatedAt = x.CreatedAt
			}
		}
		if !found {
			problem(w, 404, "not_found", "定时计划不存在，不能改绑到其他账号")
			return
		}
		_, e = s.Store.DB.Exec("UPDATE scheduled_tasks SET name=?,enabled=?,hour=?,minute=?,weekdays=?,next_run_at=?,updated_at=? WHERE id=? AND owner_id=?", v.Name, v.Enabled, v.Hour, v.Minute, string(days), next.Format(time.RFC3339), now, v.ID, a.OwnerID)
	}
	if e != nil {
		s.result(w, nil, e)
		return
	}
	v.OwnerID = a.OwnerID
	v.NextRunAt = next.Format(time.RFC3339)
	v.UpdatedAt = now
	s.audit(r, "schedule_saved", v.ID)
	send(w, 200, v)
}
func (s *Server) accountID(w http.ResponseWriter, r *http.Request, id string) *model.Account {
	old := r.PathValue("id")
	r.SetPathValue("id", id)
	a := s.account(w, r)
	r.SetPathValue("id", old)
	return a
}
func (s *Server) jobs(owner string) ([]Job, error) {
	q := "SELECT j.id,j.account_id,j.owner_id,j.created_by,j.agent_id,j.instance_id,j.profile_id,j.role,j.type,j.state,j.detail,j.created_at,j.updated_at,j.expires_at,j.schedule_id,j.cancel_requested FROM jobs j"
	args := []any{}
	if owner != "" {
		q += " JOIN accounts a ON a.id=j.account_id WHERE j.owner_id=? AND a.owner_id=?"
		args = append(args, owner, owner)
	}
	q += " ORDER BY j.created_at DESC,j.id LIMIT 200"
	rows, e := s.Store.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		if e = rows.Scan(&j.ID, &j.AccountID, &j.OwnerID, &j.CreatedBy, &j.AgentID, &j.InstanceID, &j.ProfileID, &j.Role, &j.Type, &j.State, &j.Detail, &j.CreatedAt, &j.UpdatedAt, &j.ExpiresAt, &j.ScheduleID, &j.CancelRequested); e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Server) jobsFor(r *http.Request) ([]Job, error) {
	u := s.currentUser(r)
	if u.Role == "admin" {
		return s.jobs("")
	}
	return s.jobs(u.ID)
}
func (s *Server) jobFor(w http.ResponseWriter, r *http.Request) *Job {
	all, e := s.jobsFor(r)
	if e != nil {
		s.result(w, nil, e)
		return nil
	}
	for _, j := range all {
		if j.ID == r.PathValue("id") {
			return &j
		}
	}
	problem(w, 404, "not_found", "任务不存在")
	return nil
}
func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		Type      string `json:"type"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Type != "daily" && in.Type != "check" {
		problem(w, 422, "invalid_type", "请选择执行当前配置中已启用的日常任务")
		return
	}
	a := s.accountID(w, r, in.AccountID)
	if a == nil {
		return
	}
	if !s.rateBucket(r, 30, s.currentUser(r).ID) {
		problem(w, 429, "rate_limit", "请求过于频繁")
		return
	}
	if _, e := s.bound(a); e != nil {
		problem(w, 422, "not_ready", e.Error())
		return
	}
	if !a.Instance.TaskExecution {
		problem(w, 422, "agent_upgrade", "Windows Agent 尚未启用任务执行")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = token()
	}
	if len(key) < 16 || len(key) > 128 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(key) {
		problem(w, 422, "invalid_key", "请求标识无效")
		return
	}
	id, e := s.enqueueJob(a, s.currentUser(r).ID, "manual/"+s.currentUser(r).ID+"/"+key, "", time.Now(), false, in.Type)
	if e != nil {
		problem(w, 409, "job_conflict", e.Error())
		return
	}
	send(w, 202, map[string]string{"id": id, "state": "pending", "message": "已加入真实任务队列，同一Windows设备串行执行"})
}
func (s *Server) enqueueJob(a *model.Account, creator, key, schedule string, at time.Time, missed bool, kind string) (string, error) {
	var old, account string
	e := s.Store.DB.QueryRow("SELECT id,account_id FROM jobs WHERE idempotency_key=?", key).Scan(&old, &account)
	if e == nil {
		if account != a.ID {
			return "", fmt.Errorf("请求标识已用于其他账号")
		}
		return old, nil
	}
	b, e := s.bound(a)
	if e != nil {
		return "", e
	}
	now := at.UTC().Format(time.RFC3339)
	state, detail := "pending", "等待Windows串行执行"
	if missed {
		state = "expired"
		detail = "服务恢复时计划已超出10分钟宽限，不补跑旧任务"
	}
	id := token()
	_, e = s.Store.DB.Exec("INSERT INTO jobs(id,account_id,type,state,idempotency_key,created_at,updated_at,detail,owner_id,created_by,agent_id,instance_id,profile_id,role,schedule_id,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", id, a.ID, kind, state, key, now, now, detail, a.OwnerID, creator, b.AgentID, b.InstanceID, b.ProfileID, a.Role, schedule, at.Add(time.Hour).UTC().Format(time.RFC3339))
	return id, e
}

// TickSchedules claims each occurrence transactionally; advancing next_run_at never replays missed days.
func (s *Server) TickSchedules(now time.Time) error {
	rows, e := s.Store.DB.Query("SELECT id,account_id,owner_id,name,hour,minute,weekdays,timezone,next_run_at FROM scheduled_tasks WHERE enabled=1 AND next_run_at<=? ORDER BY next_run_at,id LIMIT 128", now.UTC().Format(time.RFC3339))
	if e != nil {
		return e
	}
	type due struct {
		id, account, owner, name, days, zone, next string
		hour, minute                               int
	}
	all := []due{}
	for rows.Next() {
		var v due
		if e = rows.Scan(&v.id, &v.account, &v.owner, &v.name, &v.hour, &v.minute, &v.days, &v.zone, &v.next); e != nil {
			rows.Close()
			return e
		}
		all = append(all, v)
	}
	rows.Close()
	accounts, e := s.Store.Accounts()
	if e != nil {
		return e
	}
	for _, d := range all {
		var a *model.Account
		for _, v := range accounts {
			if v.ID == d.account {
				copy := v
				a = &copy
				break
			}
		}
		if a == nil || a.OwnerID != d.owner || !s.activeUser(d.owner) {
			_, _ = s.Store.DB.Exec("UPDATE scheduled_tasks SET enabled=0 WHERE id=?", d.id)
			continue
		}
		var days []int
		if json.Unmarshal([]byte(d.days), &days) != nil {
			return fmt.Errorf("invalid schedule")
		}
		next, e := scheduler.Next(now, d.hour, d.minute, days, d.zone)
		if e != nil {
			return e
		}
		at, e := time.Parse(time.RFC3339, d.next)
		if e != nil {
			return e
		}
		// Offline devices retain a pending occurrence until its expiry; bindings come from the known snapshot.
		var b model.Binding
		e = s.Store.DB.QueryRow("SELECT agent_id,instance_id,profile_id FROM device_bindings WHERE account_id=?", a.ID).Scan(&b.AgentID, &b.InstanceID, &b.ProfileID)
		if e != nil {
			continue
		}
		tx, e := s.Store.DB.Begin()
		if e != nil {
			return e
		}
		res, e := tx.Exec("UPDATE scheduled_tasks SET next_run_at=?,updated_at=? WHERE id=? AND enabled=1 AND next_run_at=?", next.Format(time.RFC3339), now.UTC().Format(time.RFC3339), d.id, d.next)
		if e != nil {
			tx.Rollback()
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			tx.Rollback()
			continue
		}
		state, detail := "pending", "定时计划已到期，等待Windows执行"
		if now.Sub(at) > 10*time.Minute {
			state = "expired"
			detail = "错过计划时间超过10分钟，不补跑旧任务"
		}
		stamp := now.UTC().Format(time.RFC3339)
		_, e = tx.Exec("INSERT OR IGNORE INTO jobs(id,account_id,type,state,idempotency_key,created_at,updated_at,detail,owner_id,created_by,agent_id,instance_id,profile_id,role,schedule_id,expires_at) VALUES(?,?,'daily',?,?,?,?,?,?,?,?,?,?,?,?,?)", token(), a.ID, state, "schedule/"+d.id+"/"+d.next, stamp, stamp, detail, a.OwnerID, a.OwnerID, b.AgentID, b.InstanceID, b.ProfileID, a.Role, d.id, at.Add(time.Hour).UTC().Format(time.RFC3339))
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (s *Server) jobCommands(w http.ResponseWriter, r *http.Request) {
	// One active execution per Windows Agent also serializes shared emulators across MAA installations.
	var active string
	_ = s.Store.DB.QueryRow("SELECT id FROM jobs WHERE agent_id=? AND state IN "+activeJobs+" ORDER BY created_at,id LIMIT 1", s.cfg.AgentID).Scan(&active)
	if active == "" {
		var configBusy int
		_ = s.Store.DB.QueryRow("SELECT count(*) FROM config_plans WHERE agent_id=? AND state IN ('queued','waiting','dispatched')", s.cfg.AgentID).Scan(&configBusy)
		if configBusy > 0 {
			send(w, 200, []jobrunner.Command{})
			return
		}
		rows, e := s.Store.DB.Query("SELECT id,account_id,owner_id,created_by,instance_id,profile_id,expires_at FROM jobs WHERE agent_id=? AND state='pending' ORDER BY created_at,id LIMIT 64", s.cfg.AgentID)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		type candidate struct{ id, account, owner, creator, instance, profile, expiry string }
		candidates := []candidate{}
		for rows.Next() {
			var c candidate
			if rows.Scan(&c.id, &c.account, &c.owner, &c.creator, &c.instance, &c.profile, &c.expiry) == nil {
				candidates = append(candidates, c)
			}
		}
		rows.Close()
		for _, c := range candidates {
			expiry, e := time.Parse(time.RFC3339, c.expiry)
			var b model.Binding
			var owner string
			_ = s.Store.DB.QueryRow("SELECT a.owner_id,b.agent_id,b.instance_id,b.profile_id FROM accounts a JOIN device_bindings b ON b.account_id=a.id WHERE a.id=?", c.account).Scan(&owner, &b.AgentID, &b.InstanceID, &b.ProfileID)
			if e != nil || time.Now().After(expiry) || owner != c.owner || !s.activeUser(c.owner) || !s.mayManage(c.creator, c.owner) || b.AgentID != s.cfg.AgentID || b.InstanceID != c.instance || b.ProfileID != c.profile {
				_, _ = s.Store.DB.Exec("UPDATE jobs SET state='expired',detail='排队已过期或账号权限/绑定变化' WHERE id=? AND state='pending'", c.id)
				continue
			}
			res, e := s.Store.DB.Exec("UPDATE jobs SET state='dispatched',updated_at=? WHERE id=? AND state='pending' AND NOT EXISTS (SELECT 1 FROM jobs WHERE agent_id=? AND state IN "+activeJobs+")", time.Now().UTC().Format(time.RFC3339), c.id, s.cfg.AgentID)
			if e == nil {
				n, _ := res.RowsAffected()
				if n == 1 {
					active = c.id
				}
			}
			break
		}
	}
	if active == "" {
		send(w, 200, []jobrunner.Command{})
		return
	}
	var c jobrunner.Command
	var state string
	var cancel int
	e := s.Store.DB.QueryRow("SELECT id,instance_id,profile_id,role,type,expires_at,state,cancel_requested FROM jobs WHERE id=?", active).Scan(&c.ID, &c.InstanceID, &c.Profile, &c.Role, &c.Type, &c.ExpiresAt, &state, &cancel)
	if e != nil {
		s.result(w, nil, e)
		return
	}
	c.Action = "run"
	if cancel == 1 {
		c.Action = "stop"
	}
	if cancel == 2 {
		c.Action = "release"
	}
	c.Monitor = state == "unknown"
	send(w, 200, []jobrunner.Command{c})
}
func (s *Server) jobReceipt(w http.ResponseWriter, r *http.Request) {
	var receipt jobrunner.Receipt
	if !decode(w, r, &receipt) {
		return
	}
	if !receipt.Valid() {
		problem(w, 422, "invalid_receipt", "任务回执无效")
		return
	}
	var j Job
	e := s.Store.DB.QueryRow("SELECT id,account_id,owner_id,state,type,instance_id FROM jobs WHERE id=? AND agent_id=?", receipt.ID, s.cfg.AgentID).Scan(&j.ID, &j.AccountID, &j.OwnerID, &j.State, &j.Type, &j.InstanceID)
	if e != nil {
		problem(w, 404, "not_found", "任务不存在")
		return
	}
	if c := receipt.Completion; c != nil && (c.InstanceID != j.InstanceID || j.Type == "check") {
		problem(w, 422, "invalid_completion", "队列完成证据不属于此任务")
		return
	}
	if j.State == "pending" || j.State == "cancelled" || j.State == "completed" || j.State == "completed_with_errors" || j.State == "failed" || j.State == "expired" {
		if j.State == receipt.State {
			send(w, 200, map[string]bool{"ok": true})
			return
		}
		problem(w, 409, "receipt_conflict", "任务已结束或未下发")
		return
	}
	terminal := receipt.State == "completed" || receipt.State == "completed_with_errors" || receipt.State == "failed" || receipt.State == "cancelled" || receipt.State == "expired"
	cfg, e := s.Integrations.ForUser(j.OwnerID).Load()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE jobs SET state=?,detail=?,updated_at=? WHERE id=? AND state=?", receipt.State, receipt.Detail, receipt.Time, j.ID, j.State)
	if e != nil {
		s.result(w, nil, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		problem(w, 409, "receipt_conflict", "任务状态已变化")
		return
	}
	if terminal && j.Type != "check" {
		// The same exact GUI marker may reach heartbeat before or after this receipt.
		// Claim it in this transaction so the generic watcher cannot notify twice.
		if c := receipt.Completion; c != nil {
			if _, e = tx.Exec("INSERT OR IGNORE INTO observed_completions(id,agent_id,instance_id,observed_at) VALUES(?,?,?,?)", c.ID, s.cfg.AgentID, c.InstanceID, c.ObservedAt); e != nil {
				s.result(w, nil, e)
				return
			}
		}
		var name string
		_ = tx.QueryRow("SELECT name FROM accounts WHERE id=?", j.AccountID).Scan(&name)
		if e = notify.Enqueue(tx, cfg.Channels, notify.Facts{OwnerID: j.OwnerID, ID: "job/" + j.ID, Kind: "job", Account: name, State: receipt.State, Time: receipt.Time, Detail: receipt.Detail, Source: "Windows原生任务入口、对应配置档、队列日志与窗口状态"}); e != nil {
			s.result(w, nil, e)
			return
		}
	}
	if e = tx.Commit(); e != nil {
		s.result(w, nil, e)
		return
	}
	if terminal || receipt.State != j.State {
		_ = s.Store.Event("info", j.AccountID, receipt.Detail)
	}
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) executionBusy(instance string) bool {
	var n int
	e := s.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE instance_id=? AND state IN "+activeJobs, instance).Scan(&n)
	return e != nil || n > 0
}
