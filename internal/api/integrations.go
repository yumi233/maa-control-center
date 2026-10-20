package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/configsync"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"github.com/yumi233/maa-control-center/internal/rules"
)

type ConfigPlan struct {
	OwnerID      string            `json:"owner_id"`
	CreatedBy    string            `json:"created_by"`
	ApprovedBy   string            `json:"approved_by"`
	ID           string            `json:"id"`
	AccountID    string            `json:"account_id"`
	AgentID      string            `json:"agent_id"`
	InstanceID   string            `json:"instance_id"`
	ProfileID    string            `json:"profile_id"`
	Target       string            `json:"target"`
	BeforeHash   string            `json:"before_hash"`
	Operations   []rules.Operation `json:"operations"`
	Diff         []rules.Diff      `json:"diff"`
	Summary      string            `json:"summary"`
	RulesVersion string            `json:"rules_version"`
	State        string            `json:"state"`
	CreatedAt    string            `json:"created_at"`
	ExpiresAt    string            `json:"expires_at"`
	RollbackOf   string            `json:"rollback_of"`
	AfterHash    string            `json:"after_hash"`
	Receipt      string            `json:"receipt"`
}

func (s *Server) integrationRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/integrations", s.protect(func(w http.ResponseWriter, r *http.Request) {
		v, e := s.settingsFor(r).Load()
		if e != nil {
			problem(w, 503, "settings_unavailable", e.Error())
			return
		}
		send(w, 200, v.Public())
	}))
	m.HandleFunc("PUT /api/v1/integrations", s.protect(func(w http.ResponseWriter, r *http.Request) {
		var v assistant.Settings
		if !decode(w, r, &v) {
			return
		}
		if e := s.settingsFor(r).Save(v); e != nil {
			problem(w, 422, "settings_invalid", e.Error())
			return
		}
		_ = s.Store.Event("info", "", "管理员更新了模型与通知接入设置（凭据已加密保存）")
		send(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/v1/integrations/provider/test", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if !s.rate(r, 20) {
			problem(w, 429, "rate_limit", "模型测试过于频繁")
			return
		}
		cfg, e := s.settingsFor(r).Load()
		if e != nil {
			problem(w, 503, "settings_unavailable", e.Error())
			return
		}
		var v struct {
			OK bool `json:"ok"`
		}
		ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
		defer cancel()
		e = s.LLM.JSON(ctx, cfg.Provider, `Return only JSON {"ok":true}. No tool calls.`, map[string]string{"purpose": "Verify explicitly configured exact model and JSON capability"}, &v)
		if e != nil || !v.OK {
			msg := "模型未确认连接"
			if e != nil {
				msg = e.Error()
			}
			problem(w, 422, "provider_test_failed", msg)
			return
		}
		send(w, 200, map[string]any{"ok": true, "model": cfg.Provider.Model})
	}))
	m.HandleFunc("POST /api/v1/integrations/notifications/test", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if !s.rate(r, 20) {
			problem(w, 429, "rate_limit", "通知测试过于频繁")
			return
		}
		id := token()
		cfg, err := s.settingsFor(r).Load()
		if err != nil {
			problem(w, 503, "settings_unavailable", err.Error())
			return
		}
		enabled := false
		for _, c := range cfg.Channels {
			enabled = enabled || c.Enabled
		}
		if !enabled {
			problem(w, 422, "channel_disabled", "请先启用并保存至少一个通知渠道")
			return
		}
		e := s.Notifications.Queue(notify.Facts{OwnerID: s.currentUser(r).ID, ID: id, Kind: "test", Account: "管理中心", State: "test", Time: time.Now().UTC().Format(time.RFC3339), Detail: "这是一条用户主动发送的连接测试", Source: "通知测试按钮"})
		if e != nil {
			problem(w, 422, "queue_failed", e.Error())
			return
		}
		send(w, 202, map[string]string{"id": id, "message": "已为启用的渠道建立测试通知，请查看送达记录"})
	}))
	m.HandleFunc("GET /api/v1/integrations/notifications", s.protect(s.notificationHistory))
	m.HandleFunc("POST /api/v1/integrations/notifications/{id}/retry", s.protect(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !v.Confirm {
			problem(w, 422, "confirmation_required", "重试可能重复发送，请先核实收件端")
			return
		}
		if e := s.Notifications.RetryFor(r.PathValue("id"), s.currentUser(r).ID); e != nil {
			problem(w, 409, "not_retryable", "该通知无法重试")
			return
		}
		send(w, 202, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/assistant/rules", s.protect(func(w http.ResponseWriter, r *http.Request) { send(w, 200, rules.Catalog) }))
	m.HandleFunc("POST /api/v1/accounts/{id}/assistant/propose", s.protect(s.proposeConfig))
	m.HandleFunc("POST /api/v1/accounts/{id}/config/preview", s.protect(s.previewConfig))
	m.HandleFunc("GET /api/v1/config/plans", s.protect(func(w http.ResponseWriter, r *http.Request) { p, e := s.visiblePlans(r, ""); s.result(w, p, e) }))
	m.HandleFunc("POST /api/v1/config/plans/{id}/approve", s.protect(s.approveConfig))
	m.HandleFunc("POST /api/v1/config/plans/{id}/rollback", s.protect(s.rollbackConfig))
	m.HandleFunc("POST /api/v1/config/plans/{id}/cancel", s.protect(func(w http.ResponseWriter, r *http.Request) {
		ps, e := s.visiblePlans(r, r.PathValue("id"))
		if e != nil || len(ps) != 1 {
			problem(w, 404, "not_found", "方案不存在")
			return
		}
		res, e := s.Store.DB.Exec("UPDATE config_plans SET state='cancelled' WHERE id=? AND state IN ('draft','queued')", r.PathValue("id"))
		if e != nil {
			problem(w, 500, "database_error", "无法取消方案")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			problem(w, 409, "state_conflict", "方案已下发或不支持取消")
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/agent/config/commands", s.agentOnly(s.configCommands))
	m.HandleFunc("POST /api/v1/agent/config/receipts", s.agentOnly(s.configReceipt))
}
func (s *Server) agentOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.AgentToken) < 32 || !equal(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.cfg.AgentToken) || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			problem(w, 401, "unauthorized", "Agent 凭据无效")
			return
		}
		next(w, r)
	}
}
func (s *Server) bound(a *model.Account) (model.Binding, error) {
	var b model.Binding
	b.AccountID = a.ID
	e := s.Store.DB.QueryRow("SELECT agent_id,instance_id,profile_id FROM device_bindings WHERE account_id=?", a.ID).Scan(&b.AgentID, &b.InstanceID, &b.ProfileID)
	if e != nil || !a.Bound || a.Instance == nil || a.Profile == nil {
		return b, fmt.Errorf("请先绑定已发现的 MAA 配置档")
	}
	if !a.AgentOnline || !rules.Supported(a.Instance.Version) {
		return b, fmt.Errorf("需要在线的 MAA 6.20 Agent")
	}
	return b, nil
}
func (s *Server) proposeConfig(w http.ResponseWriter, r *http.Request) {
	if !s.rate(r, 30) {
		problem(w, 429, "rate_limit", "方案生成过于频繁")
		return
	}
	a := s.account(w, r)
	if a == nil {
		return
	}
	if _, e := s.bound(a); e != nil {
		problem(w, 422, "not_ready", e.Error())
		return
	}
	var in struct {
		Intent string `json:"intent"`
		Target string `json:"target"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(strings.TrimSpace(in.Intent)) < 2 || len(in.Intent) > 6000 || (in.Target != "config" && in.Target != "schedule") {
		problem(w, 422, "invalid_intent", "请输入明确需求并选择配置或排班")
		return
	}
	cfg, e := s.settingsFor(r).Load()
	if e != nil {
		problem(w, 503, "settings_unavailable", e.Error())
		return
	}
	if !s.planMu.TryLock() {
		problem(w, 409, "proposal_busy", "已有方案正在生成，请稍后重试")
		return
	}
	defer s.planMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	var p assistant.Proposal
	var snapshot any = a.Profile.Editable
	if in.Target == "schedule" {
		snapshot = rules.ScheduleProjection(a.Profile.Schedule)
	}
	e = s.LLM.JSON(ctx, cfg.Provider, assistant.PlannerSystem, map[string]any{"intent": in.Intent, "target": in.Target, "role": a.Role, "maa_version": a.Instance.Version, "catalog": rules.Catalog, "current": snapshot}, &p)
	if e != nil {
		problem(w, 422, "proposal_failed", e.Error())
		return
	}
	if len(p.Summary) > 2000 {
		problem(w, 422, "proposal_invalid", "模型解释过长")
		return
	}
	s.createConfigPlan(w, r, a, in.Target, p.Operations, p.Summary)
}
func (s *Server) previewConfig(w http.ResponseWriter, r *http.Request) {
	a := s.account(w, r)
	if a == nil {
		return
	}
	var in struct {
		Target     string            `json:"target"`
		Operations []rules.Operation `json:"operations"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.createConfigPlan(w, r, a, in.Target, in.Operations, "用户通过结构化操作建立的差异预览")
}
func (s *Server) createConfigPlan(w http.ResponseWriter, r *http.Request, a *model.Account, target string, ops []rules.Operation, summary string) {
	b, e := s.bound(a)
	if e != nil {
		problem(w, 422, "not_ready", e.Error())
		return
	}
	var result rules.Result
	hash := a.Instance.Fingerprint
	if target == "config" {
		if len(a.Profile.Editable) == 0 {
			problem(w, 422, "upgrade_agent", "请升级 Windows Agent 后重新读取")
			return
		}
		result, e = rules.Apply(rules.Synthetic(a.Profile.Editable), "target", a.Role, a.Instance.Version, ops)
	} else if target == "schedule" {
		hash = a.Profile.ScheduleHash
		result, e = rules.ApplySchedule(a.Profile.Schedule, a.Instance.Version, ops)
	} else {
		e = fmt.Errorf("未知配置目标")
	}
	if e != nil {
		problem(w, 422, "rules_rejected", e.Error())
		return
	}
	if len(hash) != 64 {
		problem(w, 422, "missing_fingerprint", "配置指纹无效")
		return
	}
	p := ConfigPlan{OwnerID: a.OwnerID, CreatedBy: s.currentUser(r).ID, ApprovedBy: "admin", ID: token(), AccountID: a.ID, AgentID: b.AgentID, InstanceID: b.InstanceID, ProfileID: b.ProfileID, Target: target, BeforeHash: hash, Operations: ops, Diff: result.Diff, Summary: summary, RulesVersion: rules.Version, State: "draft", CreatedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)}
	if e = s.insertPlan(p); e != nil {
		problem(w, 500, "database_error", "无法保存预览方案")
		return
	}
	send(w, 201, p)
}
func (s *Server) insertPlan(p ConfigPlan) error {
	if p.OwnerID == "" {
		_ = s.Store.DB.QueryRow("SELECT owner_id FROM accounts WHERE id=?", p.AccountID).Scan(&p.OwnerID)
	}
	if p.CreatedBy == "" {
		p.CreatedBy = "admin"
	}
	if p.ApprovedBy == "" {
		p.ApprovedBy = "admin"
	}

	o, _ := json.Marshal(p.Operations)
	d, _ := json.Marshal(p.Diff)
	res, e := s.Store.DB.Exec("INSERT INTO config_plans(id,account_id,agent_id,instance_id,profile_id,target,before_hash,operations,diff,summary,rules_version,state,created_at,expires_at,rollback_of,owner_id,created_by,approved_by) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM accounts WHERE id=? AND owner_id=?) AND EXISTS (SELECT 1 FROM users WHERE id=? AND disabled=0)", p.ID, p.AccountID, p.AgentID, p.InstanceID, p.ProfileID, p.Target, p.BeforeHash, string(o), string(d), p.Summary, p.RulesVersion, p.State, p.CreatedAt, p.ExpiresAt, p.RollbackOf, p.OwnerID, p.CreatedBy, p.ApprovedBy, p.AccountID, p.OwnerID, p.CreatedBy)
	if e == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			return fmt.Errorf("账号归属或用户权限已变化")
		}
	}
	return e
}
func (s *Server) plans(id string) ([]ConfigPlan, error) { return s.scopedPlans(id, "") }
func (s *Server) scopedPlans(id, owner string) ([]ConfigPlan, error) {
	return s.listPlans(id, owner, false)
}
func (s *Server) listPlans(id, owner string, delivery bool) ([]ConfigPlan, error) {
	query := "SELECT id,account_id,agent_id,instance_id,profile_id,target,before_hash,operations,diff,summary,rules_version,state,created_at,expires_at,rollback_of,after_hash,receipt,owner_id,created_by,approved_by FROM config_plans"
	args := []any{}
	if id != "" {
		query += " WHERE id=?"
		args = append(args, id)
	}
	if owner != "" {
		if id != "" {
			query += " AND"
		} else {
			query += " WHERE"
		}
		query += " owner_id=? AND EXISTS (SELECT 1 FROM accounts a WHERE a.id=config_plans.account_id AND a.owner_id=?)"
		args = append(args, owner, owner)
	}
	if delivery {
		query += " WHERE state IN ('queued','dispatched') AND agent_id=? ORDER BY created_at ASC LIMIT 100"
		args = append(args, s.cfg.AgentID)
	} else {
		query += " ORDER BY created_at DESC LIMIT 100"
	}
	rows, e := s.Store.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ConfigPlan{}
	for rows.Next() {
		var p ConfigPlan
		var o, d string
		if e = rows.Scan(&p.ID, &p.AccountID, &p.AgentID, &p.InstanceID, &p.ProfileID, &p.Target, &p.BeforeHash, &o, &d, &p.Summary, &p.RulesVersion, &p.State, &p.CreatedAt, &p.ExpiresAt, &p.RollbackOf, &p.AfterHash, &p.Receipt, &p.OwnerID, &p.CreatedBy, &p.ApprovedBy); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(o), &p.Operations) != nil || json.Unmarshal([]byte(d), &p.Diff) != nil {
			return nil, fmt.Errorf("invalid stored plan")
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Server) planAccount(p ConfigPlan) (*model.Account, error) {
	a, e := s.Store.Accounts()
	if e != nil {
		return nil, e
	}
	for _, v := range a {
		if v.ID == p.AccountID {
			if v.OwnerID != p.OwnerID || !s.activeUser(p.OwnerID) || !s.mayManage(p.CreatedBy, p.OwnerID) {
				return nil, fmt.Errorf("账号归属或用户权限已变化")
			}
			b, e := s.bound(&v)
			if e != nil {
				return nil, e
			}
			if b.AgentID != p.AgentID || b.InstanceID != p.InstanceID || b.ProfileID != p.ProfileID {
				return nil, fmt.Errorf("账号绑定已变化")
			}
			return &v, nil
		}
	}
	return nil, fmt.Errorf("账号不存在")
}
func (s *Server) approveConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	ps, e := s.visiblePlans(r, r.PathValue("id"))
	if e != nil || len(ps) != 1 {
		problem(w, 404, "not_found", "方案不存在")
		return
	}
	p := ps[0]
	if in.Confirm != p.ID || p.State != "draft" {
		problem(w, 409, "confirmation_required", "请确认当前差异方案")
		return
	}
	expiry, e := time.Parse(time.RFC3339, p.ExpiresAt)
	if e != nil || time.Now().After(expiry) {
		problem(w, 409, "expired", "预览已过期，请重新生成")
		return
	}
	a, e := s.planAccount(p)
	if e != nil {
		problem(w, 409, "binding_conflict", e.Error())
		return
	}
	hash := a.Instance.Fingerprint
	if p.Target == "schedule" {
		hash = a.Profile.ScheduleHash
	}
	if hash != p.BeforeHash || p.RulesVersion != rules.Version {
		problem(w, 409, "fingerprint_conflict", "配置已发生变化，请重新预览")
		return
	}
	if !a.Instance.ProcessVerified || a.Instance.ProcessRunning {
		problem(w, 409, "maa_running", "请先正常退出 MAA，刷新状态后重新预览并确认")
		return
	}
	if !a.Instance.ConfigurationWrite {
		problem(w, 409, "agent_write_disabled", "Windows 端尚未启用受控配置写入")
		return
	}
	res, e := s.Store.DB.Exec("UPDATE config_plans SET state='queued',approved_at=?,approved_by=? WHERE id=? AND state='draft' AND EXISTS (SELECT 1 FROM accounts a WHERE a.id=config_plans.account_id AND a.owner_id=config_plans.owner_id) AND EXISTS (SELECT 1 FROM users WHERE id=? AND disabled=0)", time.Now().UTC().Format(time.RFC3339), s.currentUser(r).ID, p.ID, s.currentUser(r).ID)
	if e != nil {
		problem(w, 500, "database_error", "无法确认方案")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		problem(w, 409, "state_conflict", "方案状态已变化")
		return
	}
	_ = s.Store.Event("info", p.AccountID, "用户已确认配置差异，等待 Windows 端复核与备份")
	send(w, 202, map[string]string{"state": "queued"})
}
func (s *Server) rollbackConfig(w http.ResponseWriter, r *http.Request) {
	ps, e := s.visiblePlans(r, r.PathValue("id"))
	if e != nil || len(ps) != 1 || ps[0].State != "applied" || ps[0].RollbackOf != "" {
		problem(w, 409, "not_rollbackable", "只有已校验的原始修改可以建立回滚预览")
		return
	}
	p := ps[0]
	a, e := s.planAccount(p)
	if e != nil {
		problem(w, 409, "not_ready", e.Error())
		return
	}
	hash := a.Instance.Fingerprint
	if p.Target == "schedule" {
		hash = a.Profile.ScheduleHash
	}
	if hash != p.AfterHash {
		problem(w, 409, "rollback_conflict", "配置已有后续变化，不能覆盖回滚")
		return
	}
	p.CreatedBy = s.currentUser(r).ID
	p.RollbackOf = p.ID
	p.ID = token()
	p.BeforeHash = p.AfterHash
	p.AfterHash = ""
	p.Receipt = ""
	p.State = "draft"
	p.Summary = "恢复此修改前的 Windows 本地备份；需要再次确认"
	p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	p.ExpiresAt = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	for i := range p.Diff {
		p.Diff[i].Before, p.Diff[i].After = p.Diff[i].After, p.Diff[i].Before
	}
	if e = s.insertPlan(p); e != nil {
		problem(w, 500, "database_error", "无法建立回滚预览")
		return
	}
	send(w, 201, p)
}
func (s *Server) configCommands(w http.ResponseWriter, r *http.Request) {
	ps, e := s.listPlans("", "", true)
	if e != nil {
		problem(w, 500, "database_error", "无法读取配置方案")
		return
	}
	out := []configsync.Command{}
	for _, p := range ps {
		if p.AgentID != s.cfg.AgentID || (p.State != "queued" && p.State != "dispatched") {
			continue
		}
		a, e := s.planAccount(p)
		if !s.mayManage(p.ApprovedBy, p.OwnerID) {
			continue
		}
		if e != nil {
			_, _ = s.Store.DB.Exec("UPDATE config_plans SET state='conflict',receipt='绑定或 Agent 状态变化，未继续下发' WHERE id=? AND state='queued'", p.ID)
			continue
		}
		if p.State == "queued" {
			res, e := s.Store.DB.Exec("UPDATE config_plans SET state='dispatched' WHERE id=? AND state='queued'", p.ID)
			if e != nil {
				continue
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				continue
			}
		}
		out = append(out, configsync.Command{ID: p.ID, InstanceID: p.InstanceID, Profile: p.ProfileID, Role: a.Role, Target: p.Target, BeforeHash: p.BeforeHash, RulesVersion: p.RulesVersion, Operations: p.Operations, Diff: p.Diff, ExpiresAt: p.ExpiresAt, RollbackOf: p.RollbackOf})
		if len(out) == 4 {
			break
		}
	}
	send(w, 200, out)
}
func (s *Server) configReceipt(w http.ResponseWriter, r *http.Request) {
	var receipt configsync.Receipt
	if !decode(w, r, &receipt) {
		return
	}
	if !receipt.Valid() {
		problem(w, 422, "invalid_receipt", "回执格式无效")
		return
	}
	ps, e := s.plans(receipt.ID)
	if e != nil || len(ps) != 1 || ps[0].AgentID != s.cfg.AgentID {
		problem(w, 404, "not_found", "方案不存在")
		return
	}
	p := ps[0]
	if p.State != "dispatched" && (p.State != receipt.State || p.Receipt != receipt.Message || p.AfterHash != receipt.AfterHash) {
		problem(w, 409, "receipt_state_conflict", "回执与已保存状态不一致")
		return
	}
	if receipt.State == "applied" && (receipt.BeforeHash != p.BeforeHash || len(receipt.AfterHash) != 64) {
		problem(w, 422, "hash_mismatch", "回执指纹不匹配")
		return
	}
	cfg, e := s.Integrations.ForUser(p.OwnerID).Load()
	if e != nil {
		problem(w, 503, "settings_unavailable", "通知设置暂不可用，操作回执将重试")
		return
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		problem(w, 500, "database_error", "无法保存回执事务")
		return
	}
	defer tx.Rollback()
	result, e := tx.Exec("UPDATE config_plans SET state=?,after_hash=?,receipt=? WHERE id=? AND state='dispatched'", receipt.State, receipt.AfterHash, receipt.Message, p.ID)
	if e != nil {
		problem(w, 500, "database_error", "回执存储失败")
		return
	}
	n, _ := result.RowsAffected()
	e = notify.Enqueue(tx, cfg.Channels, notify.Facts{OwnerID: p.OwnerID, ID: p.ID, Kind: "config", Account: p.AccountID, State: receipt.State, Time: receipt.Time, Detail: receipt.Message, Source: "Windows Agent 备份与重新读取校验"})
	if e != nil {
		problem(w, 500, "notification_queue_failed", "回执与通知事务未提交，Agent 将用同一回执重试")
		return
	}
	if tx.Commit() != nil {
		problem(w, 500, "database_error", "回执事务未确认，将重试")
		return
	}
	if n == 1 {
		_ = s.Store.Event("info", p.AccountID, "配置方案结果："+receipt.Message)
	}
	send(w, 200, map[string]bool{"ok": true})
}
func (s *Server) notificationHistory(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.DB.Query("SELECT id,event_id,channel,state,detail,message,created_at FROM notification_outbox WHERE owner_id=? ORDER BY created_at DESC LIMIT 100", s.currentUser(r).ID)
	if e != nil {
		problem(w, 500, "database_error", "无法读取通知记录")
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, event, channel, state, detail, message, created string
		if rows.Scan(&id, &event, &channel, &state, &detail, &message, &created) != nil {
			problem(w, 500, "database_error", "通知记录无效")
			return
		}
		out = append(out, map[string]string{"id": id, "event_id": event, "channel": channel, "state": state, "detail": detail, "message": message, "created_at": created})
	}
	send(w, 200, out)
}
func (s *Server) Worker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Notifications.Drain(ctx)
		}
	}
}
