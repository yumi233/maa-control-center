package api

import (
	"fmt"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"github.com/yumi233/maa-control-center/internal/rules"
	"time"
)

func (s *Server) observedCompletions(h model.Heartbeat) error {
	if len(h.Completions) > 20 {
		return fmt.Errorf("too many completions")
	}
	if len(h.Completions) == 0 {
		return nil
	}
	cfg, e := s.Integrations.Load()
	if e != nil {
		return e
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	events := []string{}
	for _, c := range h.Completions {
		if len(c.ID) != 64 || (c.State != "completed" && c.State != "completed_with_errors") {
			return fmt.Errorf("invalid completion")
		}
		t, e := time.Parse(time.RFC3339, c.ObservedAt)
		if e != nil || t.After(time.Now().Add(2*time.Minute)) {
			return fmt.Errorf("invalid completion time")
		}
		found := false
		for _, i := range h.Instances {
			if i.ID == c.InstanceID && rules.Supported(i.Version) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown completion instance")
		}
		title := "日志确认队列已完成；未从日志核实账号归属、奖励或资源消耗"
		if c.State == "completed_with_errors" {
			title = "日志确认队列结束且出现错误；请检查 MAA 日志，不能视为全部成功"
		}
		res, e := tx.Exec("INSERT OR IGNORE INTO observed_completions(id,agent_id,instance_id,observed_at) VALUES(?,?,?,?)", c.ID, h.AgentID, c.InstanceID, c.ObservedAt)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			var managed int
			if e = tx.QueryRow("SELECT count(*) FROM jobs WHERE instance_id=? AND state IN "+activeJobs, c.InstanceID).Scan(&managed); e != nil {
				return e
			}
			if managed == 0 {
				if e = notify.Enqueue(tx, cfg.Channels, notify.Facts{ID: c.ID, Kind: "maa_queue", Account: "MAA 实例 " + c.InstanceID + "（账号归属未核实）", State: c.State, Time: c.ObservedAt, Detail: title, Source: "MAA 6.20 中文 GUI 队列结束日志"}); e != nil {
					return e
				}
			}
			events = append(events, title)
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	for _, title := range events {
		_ = s.Store.Event("info", "", title)
	}
	return nil
}
