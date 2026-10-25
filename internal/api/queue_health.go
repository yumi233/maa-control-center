package api

import (
	"time"

	"github.com/yumi233/maa-control-center/internal/notify"
)

// Expiration is independent of the safety lock: an unknown execution must never
// keep old schedules pending, nor allow them to replay when the lock is released.
func (s *Server) maintainJobQueue(now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	if _, e := s.Store.DB.Exec(`UPDATE jobs SET state='expired',detail='排队已过期，不补跑旧任务',updated_at=?
		WHERE agent_id=? AND state='pending' AND (expires_at='' OR julianday(expires_at) IS NULL OR julianday(expires_at)<=julianday(?))`, stamp, s.cfg.AgentID, stamp); e != nil {
		return e
	}
	// A healthy running queue can take hours. Its refreshed receipts distinguish
	// normal serialization from an unconfirmed or abandoned execution.
	var active, state, updated string
	_ = s.Store.DB.QueryRow("SELECT id,state,updated_at FROM jobs WHERE agent_id=? AND state IN "+activeJobs+" ORDER BY created_at,id LIMIT 1", s.cfg.AgentID).Scan(&active, &state, &updated)
	last, err := time.Parse(time.RFC3339Nano, updated)
	blocked := active == "" || state == "unknown" || err != nil || now.Sub(last) >= 15*time.Minute
	rows, e := s.Store.DB.Query(`SELECT j.id,j.account_id,j.owner_id,a.name,j.state,j.detail,j.updated_at,j.created_at
		FROM jobs j JOIN accounts a ON a.id=j.account_id AND a.owner_id=j.owner_id
		JOIN users u ON u.id=j.owner_id AND u.disabled=0
		WHERE j.agent_id=? AND j.type='daily' AND j.state IN ('unknown','pending','waiting')`, s.cfg.AgentID)
	if e != nil {
		return e
	}
	var alerts []notify.Facts
	for rows.Next() {
		var id, account, owner, name, status, detail, at, created string
		if e = rows.Scan(&id, &account, &owner, &name, &status, &detail, &at, &created); e != nil {
			rows.Close()
			return e
		}
		f := notify.Facts{OwnerID: owner, AccountID: account, Account: name, Kind: "job_alert", ID: "job/" + id + "/alert/unknown", State: "unknown", Time: at, Detail: detail, Source: "Windows任务回执与服务器持久队列"}
		if status == "pending" || status == "waiting" {
			start, parseErr := time.Parse(time.RFC3339Nano, created)
			// waiting is already dispatched but cannot acquire its execution slot.
			// Fresh waiting receipts do not prove that this queue is progressing.
			if (status == "pending" && !blocked) || parseErr != nil || now.Sub(start) < 10*time.Minute {
				continue
			}
			f.ID = "job/" + id + "/alert/queue-stalled"
			f.State, f.Time = "queue_stalled", stamp
			f.Detail = "计划已排队超过10分钟，Windows执行队列未能推进。请检查模拟器连接、Agent在线状态及待核实任务占用；到期后自动跳过，不补跑。"
		}
		alerts = append(alerts, f)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	for _, f := range alerts {
		// Stable event IDs make receipt retries, worker ticks and restarts idempotent.
		if e = s.Notifications.Queue(f); e != nil {
			return e
		}
	}
	return nil
}
