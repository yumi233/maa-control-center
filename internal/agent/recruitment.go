package agent

import (
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Recruitments scans the same retained, bounded log streams as activity. A
// candidate pool containing five-star operators alone is never an alert.
func Recruitments(root, instance string, since, now time.Time) []model.Recruitment {
	out := []model.Recruitment{}
	seen := map[string]int{}
	scanActivity(filepath.Join(root, "debug", "asst.log"), 128<<20, since, now, func(line string, at time.Time) {
		if !strings.Contains(line, "Assistant::append_callback | ") {
			return
		}
		n := strings.Index(line, "{")
		if n < 0 {
			return
		}
		var e struct {
			What    string `json:"what"`
			Chain   string `json:"taskchain"`
			TaskID  int    `json:"taskid"`
			UUID    string `json:"uuid"`
			Details struct {
				Level  int                        `json:"level"`
				Tags   []string                   `json:"tags"`
				Result []model.RecruitCombination `json:"result"`
			} `json:"details"`
		}
		if json.Unmarshal([]byte(line[n:]), &e) != nil || e.Chain != "Recruit" || len(e.Details.Tags) != 5 {
			return
		}
		d := e.Details
		if e.What == "RecruitTagsDetected" {
			d.Level = 0
			for _, t := range d.Tags {
				if t == "高级资深干员" {
					d.Level = 6
					break
				}
				if t == "资深干员" {
					d.Level = 5
				}
			}
		} else if e.What != "RecruitResult" {
			return
		}
		if d.Level != 5 && d.Level != 6 {
			return
		}
		sorted := append([]string{}, d.Tags...)
		sort.Strings(sorted)
		// task UUID/id and full tags identify the same recognition across callbacks
		// and rotation. There is no claimed slot number in MAA 6.20's payload.
		key, _ := json.Marshal([]any{instance, e.UUID, e.TaskID, sorted})
		r := model.Recruitment{ID: rules.Hash(key), InstanceID: instance, ObservedAt: at.UTC().Format(time.RFC3339Nano), Level: d.Level, Tags: d.Tags}
		for _, c := range d.Result {
			if c.Level >= 5 && c.Level <= r.Level && len(r.Combinations) < 10 {
				r.Combinations = append(r.Combinations, c)
			}
		}
		if i, ok := seen[r.ID]; ok {
			if len(r.Combinations) > 0 {
				r.ObservedAt = out[i].ObservedAt
				out[i] = r
			}
			return
		}
		seen[r.ID] = len(out)
		out = append(out, r)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ObservedAt < out[j].ObservedAt })
	return out
}
