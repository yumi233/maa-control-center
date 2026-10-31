package api

import (
	"database/sql"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"time"
)

func (s *Server) observedRecruitments(h model.Heartbeat) error {
	if len(h.Recruitments) > 20 {
		return fmt.Errorf("too many recruitment observations")
	}
	for _, r := range h.Recruitments {
		if !r.Valid() {
			return fmt.Errorf("invalid recruitment observation")
		}
		at, _ := time.Parse(time.RFC3339Nano, r.ObservedAt)
		if at.After(time.Now().Add(2*time.Minute)) || at.Before(time.Now().Add(-48*time.Hour)) {
			return fmt.Errorf("invalid recruitment time")
		}
		var account, owner, name, created, updated, state string
		err := s.Store.DB.QueryRow(`SELECT j.account_id,j.owner_id,a.name,j.created_at,j.updated_at,j.state FROM jobs j JOIN accounts a ON a.id=j.account_id WHERE j.id=? AND j.agent_id=? AND j.instance_id=? AND j.profile_id=? AND j.type='daily'`, r.JobID, h.AgentID, r.InstanceID, r.Profile).Scan(&account, &owner, &name, &created, &updated, &state)
		// A delayed heartbeat can precede the running receipt. Do not acknowledge
		// and discard it: the agent retries after that receipt reaches the server.
		if err == sql.ErrNoRows {
			return fmt.Errorf("recruitment job not found")
		}
		if err != nil {
			return err
		}
		start, e := time.Parse(time.RFC3339Nano, created)
		if e != nil || at.Before(start) {
			return fmt.Errorf("recruitment outside job")
		}
		switch state {
		case "running", "stopping", "unknown":
		case "completed", "completed_with_errors", "failed", "cancelled":
			end, e := time.Parse(time.RFC3339Nano, updated)
			if e != nil || at.After(end) {
				return fmt.Errorf("recruitment outside terminal job")
			}
		default:
			return fmt.Errorf("recruitment job has not started")
		}
		cfg, err := s.Integrations.ForUser(owner).Load()
		if err != nil {
			return err
		}
		tx, err := s.Store.DB.Begin()
		if err != nil {
			return err
		}
		id := "recruitment/" + r.JobID + "/" + r.ID
		result, err := tx.Exec("INSERT OR IGNORE INTO observed_recruitments(id,job_id,observed_at) VALUES(?,?,?)", id, r.JobID, r.ObservedAt)
		if err == nil {
			n, _ := result.RowsAffected()
			if n == 1 {
				err = notify.Enqueue(tx, cfg.Channels, notify.Facts{ID: id, OwnerID: owner, AccountID: account, Account: name, Kind: "recruitment", State: "rare_tags", Time: r.ObservedAt, Recruitment: &r, Source: "MAA公开招募标签回调，非获得干员回执"})
			}
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
