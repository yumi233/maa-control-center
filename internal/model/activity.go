package model

import (
	"strings"
	"time"
)

var TaskNames = map[string]string{"startup": "启动与切号", "depot": "仓库识别", "inventory": "库存保持", "fight": "理智作战", "infrastructure": "基建换班", "recruit": "公开招募", "friends": "访问好友", "mall": "信用收支", "award": "领取奖励"}

// Valid confines agent telemetry to a bounded, factual run interval.
func (v RunActivity) Valid() bool {
	start, e := time.Parse(time.RFC3339Nano, v.StartedAt)
	if e != nil {
		return false
	}
	end, e := time.Parse(time.RFC3339Nano, v.FinishedAt)
	if e != nil || end.Before(start) || end.Sub(start) > 48*time.Hour {
		return false
	}
	inRange := func(s string) bool {
		at, e := time.Parse(time.RFC3339Nano, s)
		return e == nil && !at.Before(start) && !at.After(end)
	}
	reading := func(r *SanityReading) bool {
		return r == nil || (r.Current >= 0 && r.Current <= 10000 && r.Maximum > 0 && r.Maximum <= 10000 && inRange(r.ObservedAt))
	}
	if !reading(v.FirstSanity) || !reading(v.Sanity) || (v.LastBattleAt != "" && !inRange(v.LastBattleAt)) || len(v.Tasks) > len(TaskNames) || len(v.Stages) > 64 || len(v.Drops) > 128 {
		return false
	}
	if len(v.Issues) > 1 || (len(v.Issues) == 1 && v.Issues[0] != "inventory_cache_stale") {
		return false
	}
	if v.FirstSanity != nil && v.Sanity != nil && v.FirstSanity.ObservedAt > v.Sanity.ObservedAt {
		return false
	}
	seen := map[string]bool{}
	for _, t := range v.Tasks {
		if TaskNames[t] == "" || seen[t] {
			return false
		}
		seen[t] = true
	}
	text := func(s string, n int) bool { return s != "" && len(s) <= n && !strings.ContainsAny(s, "\r\n\x00") }
	seen = map[string]bool{}
	for _, s := range v.Stages {
		if !text(s.Stage, 64) || s.Settlements < 1 || s.Settlements > 100000 || seen[s.Stage] {
			return false
		}
		seen[s.Stage] = true
	}
	seen = map[string]bool{}
	for _, d := range v.Drops {
		if !text(d.ID, 128) || !text(d.Name, 128) || d.Quantity < 1 || d.Quantity > 100000000 || seen[d.ID] {
			return false
		}
		seen[d.ID] = true
	}
	return true
}
