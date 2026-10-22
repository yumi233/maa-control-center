package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/bot"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
)

type botCommand struct {
	UMO       string `json:"umo"`
	SenderID  string `json:"sender_id"`
	MessageID string `json:"message_id"`
	SentAt    string `json:"sent_at"`
	Command   string `json:"command"`
	Target    string `json:"target,omitempty"`
	JobID     string `json:"job_id,omitempty"`
}
type botReply struct {
	Message string `json:"message"`
	JobID   string `json:"job_id,omitempty"`
	State   string `json:"state,omitempty"`
}

func (s *Server) botRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/bot/commands", s.botCommand)
	m.HandleFunc("POST /api/v1/bot/bindings", s.protect(func(w http.ResponseWriter, r *http.Request) {
		var b bot.Binding
		if !decode(w, r, &b) {
			return
		}
		a := s.accountID(w, r, b.AccountID)
		if a == nil {
			return
		}
		b.OwnerID = a.OwnerID
		b, key, e := bot.Bind(s.Store.DB, b)
		if e != nil {
			problem(w, 422, "invalid_binding", e.Error())
			return
		}
		s.audit(r, "bot_binding_created", b.ID)
		w.Header().Set("Cache-Control", "no-store")
		send(w, 201, map[string]any{"binding": b, "token": key})
	}))
	m.HandleFunc("GET /api/v1/bot/bindings", s.protect(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		query := "SELECT id,owner_id,account_id,umo,members,enabled,revision FROM bot_bindings"
		args := []any{}
		if u.Role != "admin" {
			query += " WHERE owner_id=?"
			args = append(args, u.ID)
		}
		rows, e := s.Store.DB.Query(query, args...)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		defer rows.Close()
		out := []bot.Binding{}
		for rows.Next() {
			var b bot.Binding
			var raw string
			if e = rows.Scan(&b.ID, &b.OwnerID, &b.AccountID, &b.UMO, &raw, &b.Enabled, &b.Revision); e != nil {
				s.result(w, nil, e)
				return
			}
			json.Unmarshal([]byte(raw), &b.Members)
			out = append(out, b)
		}
		s.result(w, out, rows.Err())
	}))
	m.HandleFunc("PUT /api/v1/bot/bindings/{id}", s.protect(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Members  []bot.Member `json:"members"`
			Revision int          `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		if e := bot.ValidateMembers(in.Members); e != nil {
			problem(w, 422, "invalid_members", e.Error())
			return
		}
		u := s.currentUser(r)
		q := "UPDATE bot_bindings SET members=?,revision=revision+1 WHERE id=? AND revision=? AND enabled=1 AND EXISTS (SELECT 1 FROM accounts a WHERE a.id=bot_bindings.account_id AND a.owner_id=bot_bindings.owner_id)"
		raw, _ := json.Marshal(in.Members)
		args := []any{string(raw), r.PathValue("id"), in.Revision}
		if u.Role != "admin" {
			q += " AND owner_id=?"
			args = append(args, u.ID)
		}
		res, e := s.Store.DB.Exec(q, args...)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			problem(w, 409, "binding_conflict", "绑定不存在、已撤销或已被其他人修改，请刷新")
			return
		}
		s.audit(r, "bot_members_updated", r.PathValue("id"))
		send(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("DELETE /api/v1/bot/bindings/{id}", s.protect(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		q := "UPDATE bot_bindings SET enabled=0 WHERE id=?"
		args := []any{r.PathValue("id")}
		if u.Role != "admin" {
			q += " AND owner_id=?"
			args = append(args, u.ID)
		}
		res, e := s.Store.DB.Exec(q, args...)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			problem(w, 404, "not_found", "绑定不存在")
			return
		}
		s.audit(r, "bot_binding_revoked", r.PathValue("id"))
		send(w, 200, map[string]bool{"ok": true})
	}))
}

func (s *Server) botCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(key) != 64 {
		problem(w, 401, "unauthorized", "群控凭据无效")
		return
	}
	var b bot.Binding
	var allowed string
	e := s.Store.DB.QueryRow("SELECT b.id,b.owner_id,b.account_id,b.umo,b.members FROM bot_bindings b JOIN users u ON u.id=b.owner_id JOIN accounts a ON a.id=b.account_id WHERE b.token_hash=? AND b.enabled=1 AND u.disabled=0 AND a.owner_id=b.owner_id", bot.Hash(key)).Scan(&b.ID, &b.OwnerID, &b.AccountID, &b.UMO, &allowed)
	if e != nil {
		problem(w, 401, "unauthorized", "群控凭据已失效或账号归属已改变")
		return
	}
	if !s.rateBucket(r, 120, b.ID) {
		problem(w, 429, "rate_limit", "群控请求过于频繁")
		return
	}
	var in botCommand
	if !decode(w, r, &in) {
		return
	}
	json.Unmarshal([]byte(allowed), &b.Members)
	role := ""
	for _, m := range b.Members {
		if m.UID == in.SenderID {
			role = m.Role
		}
	}
	sent, e := time.Parse(time.RFC3339, in.SentAt)
	age := time.Since(sent)
	if in.UMO != b.UMO || (role != "viewer" && role != "operator") {
		problem(w, 403, "scope_denied", "此会话或操作者无权控制绑定账号")
		return
	}
	if (in.Command == "start" || in.Command == "stop") && role != "operator" {
		problem(w, 403, "read_only", "你仅有查看权限，不能启停任务")
		return
	}
	if e != nil || age > 5*time.Minute || age < -time.Minute || in.MessageID == "" || len(in.MessageID) > 256 || strings.ContainsAny(in.MessageID, "\r\n\x00") {
		problem(w, 422, "invalid_message", "消息标识或时间无效")
		return
	}
	switch in.Command {
	case "help", "status", "start", "stop", "report", "today", "schedules":
	default:
		problem(w, 422, "unsupported_command", "仅支持状态、开始、停止、汇报、今日、计划与帮助")
		return
	}
	accounts, e := s.Store.Accounts()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	var a *model.Account
	for _, v := range accounts {
		if v.ID == b.AccountID && v.OwnerID == b.OwnerID {
			copy := v
			a = &copy
			break
		}
	}
	if a == nil {
		problem(w, 403, "scope_denied", "绑定账号不可访问")
		return
	}
	if in.Target != "" && in.Target != a.Name && in.Target != a.ID {
		problem(w, 403, "target_denied", "本会话只能操作 "+a.Name)
		return
	}
	var binding model.Binding
	var readyError error
	if in.Command == "start" {
		binding, readyError = s.bound(a)
		if readyError == nil && !a.Instance.TaskExecution {
			readyError = fmt.Errorf("Windows 尚未启用任务执行")
		}
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	defer tx.Rollback()
	var valid int
	if tx.QueryRow("SELECT count(*) FROM bot_bindings b JOIN users u ON u.id=b.owner_id JOIN accounts a ON a.id=b.account_id WHERE b.id=? AND b.enabled=1 AND b.token_hash=? AND b.members=? AND u.disabled=0 AND a.owner_id=b.owner_id", b.ID, bot.Hash(key), allowed).Scan(&valid) != nil || valid != 1 {
		problem(w, 403, "binding_changed", "绑定已变化")
		return
	}
	raw, _ := json.Marshal(struct{ Command, Target, Job, Sender, UMO string }{in.Command, in.Target, in.JobID, in.SenderID, in.UMO})
	hash := bot.Hash(string(raw))
	var previous, oldHash string
	e = tx.QueryRow("SELECT request_hash,response FROM bot_requests WHERE binding_id=? AND message_id=?", b.ID, in.MessageID).Scan(&oldHash, &previous)
	if e == nil {
		if oldHash != hash {
			problem(w, 409, "message_conflict", "同一消息标识不能用于其他操作")
			return
		}
		var out botReply
		if json.Unmarshal([]byte(previous), &out) != nil {
			problem(w, 500, "invalid_record", "请求记录无法核实")
			return
		}
		send(w, 200, out)
		return
	}
	if e != sql.ErrNoRows {
		s.result(w, nil, e)
		return
	}
	if readyError != nil {
		problem(w, 422, "not_ready", readyError.Error())
		return
	}
	reply, e := s.performBotCommand(tx, b, a, binding, in)
	if e != nil {
		problem(w, 409, "command_rejected", e.Error())
		return
	}
	response, _ := json.Marshal(reply)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, e = tx.Exec("INSERT INTO bot_requests(binding_id,message_id,request_hash,response,created_at) VALUES(?,?,?,?,?)", b.ID, in.MessageID, hash, string(response), now); e != nil {
		s.result(w, nil, e)
		return
	}
	if _, e = tx.Exec("INSERT INTO audit_logs(user_id,action,detail,created_at) VALUES(?,?,?,?)", b.OwnerID, "bot/"+in.Command, "binding="+b.ID+" account="+a.ID+" message="+bot.Hash(in.MessageID), now); e != nil {
		s.result(w, nil, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.result(w, nil, e)
		return
	}
	send(w, 200, reply)
}

func (s *Server) performBotCommand(tx *sql.Tx, b bot.Binding, a *model.Account, device model.Binding, in botCommand) (botReply, error) {
	out := botReply{}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	switch in.Command {
	case "help":
		out.Message = "本会话绑定 " + a.Name + "。\n/maa 状态\n/maa 开始 [账号备注]\n/maa 停止 [账号备注]\n/maa 汇报 [任务 ID]\n/maa 今日\n/maa 计划\n仅授权 UID 可操作；开始沿用当前配置与资源预算，停止需等待 Windows 确认。"
	case "start":
		var count int
		if e := tx.QueryRow("SELECT count(*) FROM jobs WHERE account_id=? AND state IN ('pending','dispatched','waiting','starting','running','stopping','unknown')", a.ID).Scan(&count); e != nil {
			return out, e
		}
		if count > 0 {
			return out, fmt.Errorf("%s 已有待执行、运行或待核实任务，请先查看状态", a.Name)
		}
		id := token()
		_, e := tx.Exec("INSERT INTO jobs(id,account_id,type,state,idempotency_key,created_at,updated_at,detail,owner_id,created_by,agent_id,instance_id,profile_id,role,expires_at) VALUES(?,?,'daily','pending',?,?,?,?,?,?,?,?,?,?,?)", id, a.ID, "bot/"+b.ID+"/"+bot.Hash(in.MessageID), stamp, stamp, "群控请求，等待Windows串行执行", a.OwnerID, a.OwnerID, device.AgentID, device.InstanceID, device.ProfileID, a.Role, now.Add(time.Hour).Format(time.RFC3339))
		if e != nil {
			return out, e
		}
		out = botReply{Message: a.Name + " 已加入挂机队列，沿用当前配置和资源预算。开始与结束将另行通知。\n任务：" + id, JobID: id, State: "pending"}
	case "stop":
		var id, state string
		e := tx.QueryRow("SELECT id,state FROM jobs WHERE account_id=? AND owner_id=? AND state IN ('pending','dispatched','waiting','starting','running','stopping','unknown') ORDER BY created_at,id LIMIT 1", a.ID, b.OwnerID).Scan(&id, &state)
		if e == sql.ErrNoRows {
			out.Message = a.Name + " 当前没有可停止的任务。"
			break
		}
		if e != nil {
			return out, e
		}
		next := state
		if state == "pending" {
			next = "cancelled"
		}
		res, e := tx.Exec("UPDATE jobs SET state=?,cancel_requested=1,updated_at=? WHERE id=? AND owner_id=? AND state=?", next, stamp, id, b.OwnerID, state)
		if e != nil {
			return out, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return out, fmt.Errorf("任务已变化，请重新查看状态")
		}
		msg := "已请求停止，等待 Windows 确认；尚不能视为已停止。"
		if next == "cancelled" {
			msg = "已取消尚未开始的任务。"
		}
		out = botReply{Message: a.Name + "：" + msg + "\n任务：" + id, JobID: id, State: next}
	case "status":
		online := "离线"
		if a.AgentOnline {
			online = "在线"
		}
		out.Message = a.Name + " / Windows " + online
		rows, e := tx.Query("SELECT id,state FROM jobs WHERE account_id=? AND owner_id=? AND state IN ('pending','dispatched','waiting','starting','running','stopping','unknown') ORDER BY created_at,id LIMIT 5", a.ID, b.OwnerID)
		if e != nil {
			return out, e
		}
		n := 0
		for rows.Next() {
			var id, state string
			if e = rows.Scan(&id, &state); e != nil {
				rows.Close()
				return out, e
			}
			out.Message += "\n" + state + " / " + id
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if n == 0 {
			out.Message += "\n没有活动任务。"
		}
	case "schedules":
		rows, e := tx.Query("SELECT hour,minute,weekdays,timezone,enabled,next_run_at FROM scheduled_tasks WHERE account_id=? AND owner_id=? ORDER BY hour,minute", a.ID, b.OwnerID)
		if e != nil {
			return out, e
		}
		out.Message = a.Name + " / 定时计划"
		n := 0
		for rows.Next() {
			var h, m int
			var days, zone, next string
			var enabled bool
			if e = rows.Scan(&h, &m, &days, &zone, &enabled, &next); e != nil {
				rows.Close()
				return out, e
			}
			state := "已停用"
			if enabled {
				state = "启用"
			}
			out.Message += fmt.Sprintf("\n%02d:%02d %s / %s / 星期 %s\n下次：%s", h, m, zone, state, days, next)
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if n == 0 {
			out.Message += "\n尚无定时计划。"
		}
	case "report", "today":
		q := "SELECT j.id,j.state,j.detail,j.updated_at,a.document FROM jobs j LEFT JOIN job_activity a ON a.job_id=j.id WHERE j.account_id=? AND j.owner_id=? AND j.type='daily' AND j.state IN ('completed','completed_with_errors','cancelled','failed')"
		args := []any{a.ID, b.OwnerID}
		if in.JobID != "" {
			q += " AND j.id=?"
			args = append(args, in.JobID)
		}
		if in.Command == "today" {
			zone := time.FixedZone("Asia/Shanghai", 8*3600)
			today := now.In(zone)
			begin := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone)
			q += " AND j.updated_at>=?"
			args = append(args, begin.UTC().Format(time.RFC3339))
		}
		q += " ORDER BY j.updated_at DESC,j.id DESC LIMIT 1"
		var id, state, detail, at string
		var document sql.NullString
		e := tx.QueryRow(q, args...).Scan(&id, &state, &detail, &at, &document)
		if e == sql.ErrNoRows {
			out.Message = a.Name + " 没有符合条件的已结束挂机记录。"
			break
		}
		if e != nil {
			return out, e
		}
		f := notify.Facts{OwnerID: b.OwnerID, ID: "job/" + id, Kind: "job", AccountID: a.ID, Account: a.Name, State: state, Time: at, Detail: detail}
		if document.Valid {
			var activity model.RunActivity
			if json.Unmarshal([]byte(document.String), &activity) == nil && activity.Valid() {
				f.Activity = &activity
			}
		}
		when := at
		if in.Command == "today" {
			when = stamp
		}
		f.Today, e = notify.Today(tx, b.OwnerID, a.ID, when)
		if e != nil {
			return out, e
		}
		out = botReply{Message: notify.Body(f, ""), JobID: id, State: state}
	}
	return out, nil
}
