package jobrunner

import (
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/agent"
	"github.com/yumi233/maa-control-center/internal/model"
	"os"
	"path/filepath"
	"time"
)

// Use durable execution intervals, never the mutable currently selected profile.
// This also covers a recognition immediately preceding the final job receipt.
func (r *Runner) Recruitments(root, instance string, since, now time.Time) []model.Recruitment {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := agent.Recruitments(root, instance, since, now)
	files, _ := os.ReadDir(r.StateDir)
	out := []model.Recruitment{}
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(r.StateDir, f.Name()))
		if e != nil || len(b) > 256<<10 {
			continue
		}
		var rec record
		if json.Unmarshal(b, &rec) != nil || rec.Command.InstanceID != instance || rec.Command.Type != "daily" || rec.Started.IsZero() || rec.Started.After(now) || rec.Receipt.State == "expired" {
			continue
		}
		end := now
		if terminal(rec.Receipt.State) {
			end, e = time.Parse(time.RFC3339Nano, rec.Receipt.Time)
			if e != nil {
				continue
			}
		}
		if end.Before(since) {
			continue
		}
		for _, v := range events {
			at, e := time.Parse(time.RFC3339Nano, v.ObservedAt)
			if e != nil || at.Before(rec.Started) || at.After(end) {
				continue
			}
			v.JobID = rec.Command.ID
			v.Profile = rec.Command.Profile
			if v.Valid() {
				out = append(out, v)
			}
		}
	}
	return out
}
