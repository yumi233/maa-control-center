package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/model"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func (s *Server) currentUser(r *http.Request) User {
	id, _, _ := s.session(r)
	var u User
	_ = s.Store.DB.QueryRow("SELECT id,username,role,disabled FROM users WHERE id=? AND disabled=0", id).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled)
	return u
}
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return s.protect(func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r).Role != "admin" {
			problem(w, 403, "admin_required", "仅管理员可以管理用户和配置档分配")
			return
		}
		next(w, r)
	})
}
func (s *Server) settingsFor(r *http.Request) assistant.SettingsStore {
	return s.Integrations.ForUser(s.currentUser(r).ID)
}
func (s *Server) visibleAccounts(r *http.Request) ([]model.Account, error) {
	all, e := s.Store.Accounts()
	if e != nil {
		return nil, e
	}
	u := s.currentUser(r)
	rows, err := s.Store.DB.Query("SELECT account_id,state FROM jobs WHERE state IN ('pending','dispatched','waiting','starting','running','stopping','unknown') ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	states := map[string]string{}
	for rows.Next() {
		var id, state string
		if rows.Scan(&id, &state) == nil {
			if _, ok := states[id]; !ok {
				states[id] = state
			}
		}
	}
	rows.Close()
	for i := range all {
		switch states[all[i].ID] {
		case "pending", "dispatched", "waiting", "starting":
			all[i].Status = "queued"
		case "running":
			all[i].Status = "running"
		case "stopping":
			all[i].Status = "stopping"
		case "unknown":
			all[i].Status = "unknown"
		}
	}
	if u.Role == "admin" {
		return all, nil
	}
	out := []model.Account{}
	for _, a := range all {
		if a.OwnerID == u.ID {
			if a.Instance != nil {
				a.Instance.Profiles = []model.Profile{}
				if a.Profile != nil {
					a.Instance.Profiles = append(a.Instance.Profiles, *a.Profile)
				}
				// Shared process logs and instance schedules do not establish account ownership.
				a.Instance.Logs = []string{}
				a.Instance.Schedule = nil
				if a.Profile != nil && a.Instance.CurrentProfile != a.Profile.ID {
					a.Instance.CurrentProfile = ""
				}
				filtered := []model.NativeTimer{}
				for _, timer := range a.Instance.NativeTimers {
					if a.Profile != nil && timer.ProfileID == a.Profile.ID {
						filtered = append(filtered, timer)
					}
				}
				a.Instance.NativeTimers = filtered
			}
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *Server) visibleEvents(r *http.Request) ([]model.Event, error) {
	all, e := s.Store.Events()
	if e != nil {
		return nil, e
	}
	if s.currentUser(r).Role == "admin" {
		return all, nil
	}
	accounts, e := s.visibleAccounts(r)
	if e != nil {
		return nil, e
	}
	ids := map[string]bool{}
	for _, a := range accounts {
		ids[a.ID] = true
	}
	out := []model.Event{}
	for _, v := range all {
		if ids[v.AccountID] {
			out = append(out, v)
		}
	}
	return out, nil
}
func (s *Server) visibleAgents(r *http.Request) ([]map[string]any, error) {
	if s.currentUser(r).Role == "admin" {
		return s.Store.Discovered()
	}
	// Discovery, host details and unassigned profile names belong to administrators.
	return []map[string]any{}, nil
}
func (s *Server) accountIdle(id string) bool {
	var n int
	e := s.Store.DB.QueryRow("SELECT count(*) FROM config_plans WHERE account_id=? AND state IN ('queued','waiting','dispatched','unknown')", id).Scan(&n)
	if e != nil || n != 0 {
		return false
	}
	e = s.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE account_id=? AND state IN "+activeJobs, id).Scan(&n)
	return e == nil && n == 0
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,47}$`)

func passwordHash(p string) (string, error) {
	if len(p) < 12 || len(p) > 72 {
		return "", fmt.Errorf("密码需为 12–72 字节")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), e
}
func (s *Server) userRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/auth/password", s.protect(s.changePassword))
	m.HandleFunc("GET /api/v1/users", s.adminOnly(func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.Store.DB.Query("SELECT id,username,role,disabled FROM users ORDER BY username")
		if e != nil {
			s.result(w, nil, e)
			return
		}
		defer rows.Close()
		users := []User{}
		for rows.Next() {
			var u User
			if e = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled); e != nil {
				s.result(w, nil, e)
				return
			}
			users = append(users, u)
		}
		s.result(w, users, rows.Err())
	}))
	m.HandleFunc("POST /api/v1/users", s.adminOnly(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Username = strings.TrimSpace(in.Username)
		if !usernamePattern.MatchString(in.Username) || (in.Role != "member" && in.Role != "admin") {
			problem(w, 422, "invalid_user", "用户名需为 3–48 位字母、数字、点、下划线或短横线；请选择有效角色")
			return
		}
		hash, e := passwordHash(in.Password)
		if e != nil {
			problem(w, 422, "invalid_password", e.Error())
			return
		}
		u := User{ID: token(), Username: in.Username, Role: in.Role}
		_, e = s.Store.DB.Exec("INSERT INTO users(id,username,password_hash,role) VALUES(?,?,?,?)", u.ID, u.Username, hash, u.Role)
		if e != nil {
			problem(w, 409, "user_exists", "用户名已存在或无法保存")
			return
		}
		s.audit(r, "user_created", u.ID)
		send(w, 201, u)
	}))
	m.HandleFunc("PUT /api/v1/users/{id}", s.adminOnly(s.updateUser))
	m.HandleFunc("POST /api/v1/accounts", s.protect(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"name"`
			Role    string `json:"role"`
			OwnerID string `json:"owner_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		u := s.currentUser(r)
		in.Name = strings.TrimSpace(in.Name)
		if in.OwnerID == "" {
			in.OwnerID = u.ID
		}
		if u.Role != "admin" && in.OwnerID != u.ID {
			problem(w, 403, "owner_denied", "只能为自己添加账号")
			return
		}
		if len(in.Name) == 0 || len(in.Name) > 120 || (in.Role != "primary" && in.Role != "secondary") || !s.activeUser(in.OwnerID) {
			problem(w, 422, "invalid_account", "请填写账号名称、资源策略和有效所属用户")
			return
		}
		id := token()
		_, e := s.Store.DB.Exec("INSERT INTO accounts(id,name,role,owner_id) VALUES(?,?,?,?)", id, in.Name, in.Role, in.OwnerID)
		if e != nil {
			s.result(w, nil, e)
			return
		}
		s.audit(r, "account_created", id)
		send(w, 201, map[string]string{"id": id})
	}))
	m.HandleFunc("PUT /api/v1/accounts/{id}", s.protect(s.updateAccount))
}
func (s *Server) audit(r *http.Request, action, detail string) {
	_, _ = s.Store.DB.Exec("INSERT INTO audit_logs(user_id,action,detail,created_at) VALUES(?,?,?,?)", s.currentUser(r).ID, action, detail, time.Now().UTC().Format(time.RFC3339))
}
func (s *Server) activeUser(id string) bool {
	var n int
	_ = s.Store.DB.QueryRow("SELECT count(*) FROM users WHERE id=? AND disabled=0", id).Scan(&n)
	return n == 1
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.rateBucket(r, 12, s.currentUser(r).ID) {
		problem(w, 429, "rate_limit", "改密尝试过多，请稍后再试")
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := s.currentUser(r).ID
	var old string
	if s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", id).Scan(&old) != nil || bcrypt.CompareHashAndPassword([]byte(old), []byte(in.Current)) != nil {
		problem(w, 422, "password_mismatch", "当前密码不正确")
		return
	}
	hash, e := passwordHash(in.New)
	if e != nil {
		problem(w, 422, "invalid_password", e.Error())
		return
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE users SET password_hash=? WHERE id=? AND password_hash=? AND disabled=0", hash, id, old)
	if e != nil {
		s.result(w, nil, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		problem(w, 409, "password_changed", "密码或用户状态已变化，请重新登录")
		return
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); e != nil {
		s.result(w, nil, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.result(w, nil, e)
		return
	}
	_, _ = s.Store.DB.Exec("INSERT INTO audit_logs(user_id,action,detail,created_at) VALUES(?,'password_changed','sessions_revoked',?)", id, time.Now().UTC().Format(time.RFC3339))
	http.SetCookie(w, &http.Cookie{Name: "maacc_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"), SameSite: http.SameSiteStrictMode})
	send(w, 200, map[string]string{"message": "密码已修改，所有登录已退出，请使用新密码登录"})
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "admin" && in.Role != "member" {
		problem(w, 422, "invalid_role", "角色无效")
		return
	}
	id := r.PathValue("id")
	actor := s.currentUser(r)
	if id == actor.ID && (in.Disabled || in.Role != "admin") {
		problem(w, 409, "self_lockout", "不能停用或降级当前管理员")
		return
	}
	hash := ""
	var e error
	if in.Password != "" {
		hash, e = passwordHash(in.Password)
		if e != nil {
			problem(w, 422, "invalid_password", e.Error())
			return
		}
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	defer tx.Rollback()
	var old User
	e = tx.QueryRow("SELECT id,username,role,disabled FROM users WHERE id=?", id).Scan(&old.ID, &old.Username, &old.Role, &old.Disabled)
	if e == sql.ErrNoRows {
		problem(w, 404, "not_found", "用户不存在")
		return
	}
	if e != nil {
		s.result(w, nil, e)
		return
	}
	if old.Role == "admin" && !old.Disabled && (in.Role != "admin" || in.Disabled) {
		var n int
		if tx.QueryRow("SELECT count(*) FROM users WHERE role='admin' AND disabled=0").Scan(&n) != nil || n <= 1 {
			problem(w, 409, "last_admin", "必须保留至少一个可登录的管理员")
			return
		}
	}
	if _, e = tx.Exec("UPDATE users SET role=?,disabled=? WHERE id=?", in.Role, in.Disabled, id); e != nil {
		s.result(w, nil, e)
		return
	}
	if hash != "" {
		if _, e = tx.Exec("UPDATE users SET password_hash=? WHERE id=?", hash, id); e != nil {
			s.result(w, nil, e)
			return
		}
	}
	// Password reset, disabling and role changes immediately revoke existing sessions.
	if hash != "" || in.Disabled || in.Role != old.Role {
		if _, e = tx.Exec("UPDATE scheduled_tasks SET enabled=0 WHERE owner_id=?", id); e != nil {
			s.result(w, nil, e)
			return
		}
		if _, e = tx.Exec("UPDATE jobs SET state='cancelled',detail='用户权限变化，未执行' WHERE owner_id=? AND state='pending'", id); e != nil {
			s.result(w, nil, e)
			return
		}
		if in.Disabled {
			if _, e = tx.Exec("UPDATE jobs SET cancel_requested=1 WHERE owner_id=? AND state IN "+activeJobs, id); e != nil {
				s.result(w, nil, e)
				return
			}
		}

		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); e != nil {
			s.result(w, nil, e)
			return
		}
		if _, e = tx.Exec("UPDATE config_plans SET state='cancelled',receipt='用户权限变化，需重新预览确认' WHERE (created_by=? OR approved_by=? OR owner_id=?) AND state IN ('draft','queued')", id, id, id); e != nil {
			s.result(w, nil, e)
			return
		}
	}
	if e = tx.Commit(); e != nil {
		s.result(w, nil, e)
		return
	}
	s.audit(r, "user_updated", id)
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	a := s.account(w, r)
	if a == nil {
		return
	}
	u := s.currentUser(r)
	var in struct {
		Name    string `json:"name"`
		OwnerID string `json:"owner_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.OwnerID == "" {
		in.OwnerID = a.OwnerID
	}
	if len(in.Name) == 0 || len(in.Name) > 120 || !s.activeUser(in.OwnerID) {
		problem(w, 422, "invalid_account", "账号名称或所属用户无效")
		return
	}
	if u.Role != "admin" && in.OwnerID != u.ID {
		problem(w, 403, "owner_denied", "只有管理员可以分配账号")
		return
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		s.result(w, nil, e)
		return
	}
	defer tx.Rollback()
	if in.OwnerID != a.OwnerID {
		var jobCount int
		if tx.QueryRow("SELECT count(*) FROM jobs WHERE account_id=? AND state IN "+activeJobs, a.ID).Scan(&jobCount) != nil || jobCount > 0 {
			problem(w, 409, "account_busy", "此账号有运行或待核实任务，请先处理再转交")
			return
		}
		if _, e = tx.Exec("UPDATE scheduled_tasks SET enabled=0 WHERE account_id=?", a.ID); e != nil {
			s.result(w, nil, e)
			return
		}
		if _, e = tx.Exec("UPDATE bot_bindings SET enabled=0,revision=revision+1 WHERE account_id=?", a.ID); e != nil {
			s.result(w, nil, e)
			return
		}
		if _, e = tx.Exec("UPDATE jobs SET state='cancelled',detail='账号转交，原计划取消' WHERE account_id=? AND state='pending'", a.ID); e != nil {
			s.result(w, nil, e)
			return
		}
		var n int
		if tx.QueryRow("SELECT count(*) FROM config_plans WHERE account_id=? AND state IN ('queued','waiting','dispatched','unknown')", a.ID).Scan(&n) != nil || n != 0 {
			problem(w, 409, "account_busy", "请先取消待执行方案或核实 Windows 处理结果，再转交账号")
			return
		}
		if _, e = tx.Exec("UPDATE config_plans SET state='cancelled',receipt='所属用户变化，需重新预览' WHERE account_id=? AND state='draft'", a.ID); e != nil {
			s.result(w, nil, e)
			return
		}
	}
	res, e := tx.Exec("UPDATE accounts SET name=?,owner_id=? WHERE id=? AND owner_id=?", in.Name, in.OwnerID, a.ID, a.OwnerID)
	if e != nil {
		s.result(w, nil, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		problem(w, 409, "owner_changed", "账号所属用户已变化，请刷新")
		return
	}
	if e = tx.Commit(); e != nil {
		s.result(w, nil, e)
		return
	}
	s.audit(r, "account_updated", a.ID)
	send(w, 200, map[string]bool{"ok": true})
}

func (s *Server) visiblePlans(r *http.Request, id string) ([]ConfigPlan, error) {
	u := s.currentUser(r)
	if u.Role == "admin" {
		return s.plans(id)
	}
	return s.scopedPlans(id, u.ID)
}

func (s *Server) mayManage(id, owner string) bool {
	var n int
	_ = s.Store.DB.QueryRow("SELECT count(*) FROM users WHERE id=? AND disabled=0 AND (role='admin' OR id=?)", id, owner).Scan(&n)
	return n == 1
}
