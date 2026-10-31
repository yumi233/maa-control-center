package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/model"
)

var taskDone = regexp.MustCompile(`^\[[^\]]+\]\[INF\]\[TaskQueueViewModel\]\s+<\d+> 完成任务: (.+)$`)
var activityTasks = []struct{ prefix, code string }{
	{"开始唤醒", "startup"}, {"库存保持 (仓库识别)", "depot"}, {"库存保持", "inventory"},
	{"理智作战", "fight"}, {"基建换班", "infrastructure"}, {"自动公招", "recruit"},
	{"访问好友", "friends"}, {"信用收支", "mall"}, {"领取奖励", "award"},
}

// Activity only reads this run's bounded logs. It never queries or operates the game.
func Activity(root string, start, end time.Time) model.RunActivity {
	v := model.RunActivity{StartedAt: start.UTC().Format(time.RFC3339), FinishedAt: end.UTC().Format(time.RFC3339), Tasks: []string{}, Stages: []model.StageActivity{}, Drops: []model.ActivityDrop{}, LogsComplete: true}
	stages := map[string]int{}
	drops := map[string]model.ActivityDrop{}
	tasks := map[string]bool{}
	var lastBattle time.Time
	bounded := true
	coreOK := scanActivity(filepath.Join(root, "debug", "asst.log"), 128<<20, start, end, func(line string, at time.Time) {
		if !strings.Contains(line, "Assistant::append_callback | ") {
			return
		}
		a := strings.Index(line, "{")
		if a < 0 {
			return
		}
		var e struct {
			What    string          `json:"what"`
			Details json.RawMessage `json:"details"`
		}
		if json.Unmarshal([]byte(line[a:]), &e) != nil {
			bounded = false
			return
		}
		switch e.What {
		case "ExceededLimit":
			var d struct {
				Task string `json:"task"`
			}
			if json.Unmarshal(e.Details, &d) == nil && (d.Task == "UsePrts" || strings.HasSuffix(d.Task, "@UsePrts")) {
				found := false
				for _, i := range v.Issues {
					if i == "auto_deploy_unavailable" {
						found = true
					}
				}
				if !found {
					v.Issues = append(v.Issues, "auto_deploy_unavailable")
				}
			}
		case "SanityBeforeStage":
			var d struct {
				Current *int   `json:"current_sanity"`
				Max     int    `json:"max_sanity"`
				Time    string `json:"report_time"`
			}
			if json.Unmarshal(e.Details, &d) != nil || d.Current == nil || *d.Current < 0 || *d.Current > 10000 || d.Max <= 0 || d.Max > 10000 {
				bounded = false
				return
			}
			observed := at
			if t, err := time.ParseInLocation("2006-01-02 15:04:05.000", d.Time, time.Local); err == nil && !t.Before(start) && !t.After(at) {
				observed = t
			}
			r := &model.SanityReading{Current: *d.Current, Maximum: d.Max, ObservedAt: observed.UTC().Format(time.RFC3339)}
			if v.FirstSanity == nil || r.ObservedAt < v.FirstSanity.ObservedAt {
				v.FirstSanity = r
			}
			if v.Sanity == nil || r.ObservedAt >= v.Sanity.ObservedAt {
				v.Sanity = r
			}
		case "StageDrops":
			if at.After(lastBattle) {
				lastBattle = at
			}
			var d struct {
				Stage struct {
					Code string `json:"stageCode"`
				} `json:"stage"`
				Drops []struct {
					ID       string `json:"itemId"`
					Name     string `json:"itemName"`
					Quantity int    `json:"quantity"`
				} `json:"drops"`
			}
			if json.Unmarshal(e.Details, &d) != nil || !safeActivityText(d.Stage.Code, 64) || d.Stage.Code == "" || len(d.Drops) > 128 {
				bounded = false
				return
			}
			if stages[d.Stage.Code] == 0 && len(stages) >= 64 {
				bounded = false
				return
			}
			stages[d.Stage.Code]++
			if at.After(lastBattle) {
				lastBattle = at
			}
			for _, x := range d.Drops {
				if x.Quantity <= 0 || x.Quantity > 10000000 || x.ID == "" || x.Name == "" || !safeActivityText(x.ID, 128) || !safeActivityText(x.Name, 128) {
					continue
				}
				a := drops[x.ID]
				if (a.ID == "" && len(drops) >= 128) || a.Quantity+x.Quantity > 100000000 {
					bounded = false
					continue
				}
				a.ID = x.ID
				a.Name = x.Name
				a.Quantity += x.Quantity
				drops[x.ID] = a
			}
		default:
			if strings.Contains(line, " | SubTaskStart ") {
				var d struct {
					Task string `json:"task"`
				}
				if json.Unmarshal(e.Details, &d) == nil && (d.Task == "StartButton2" || d.Task == "AnnihilationConfirm") && at.After(lastBattle) {
					lastBattle = at
				}
			}
		}
	})
	guiOK := scanActivity(filepath.Join(root, "debug", "gui.log"), 128<<20, start, end, func(line string, _ time.Time) {
		if strings.Contains(line, "[WRN][TaskQueueViewModel]") && strings.Contains(line, "预检时已跳过的计划无法在运行中添加") {
			found := false
			for _, i := range v.Issues {
				if i == "inventory_cache_stale" {
					found = true
				}
			}
			if !found {
				v.Issues = append(v.Issues, "inventory_cache_stale")
			}
		}
		m := taskDone.FindStringSubmatch(line)
		if m == nil {
			return
		}
		for _, known := range activityTasks {
			if m[1] == known.prefix || strings.HasPrefix(m[1], known.prefix+" ") {
				tasks[known.code] = true
				break
			}
		}
	})
	v.LogsComplete = coreOK && guiOK && bounded
	for task := range tasks {
		v.Tasks = append(v.Tasks, task)
	}
	sort.Strings(v.Tasks)
	for stage, n := range stages {
		v.Stages = append(v.Stages, model.StageActivity{Stage: stage, Settlements: n})
	}
	sort.Slice(v.Stages, func(i, j int) bool { return v.Stages[i].Stage < v.Stages[j].Stage })
	for _, d := range drops {
		v.Drops = append(v.Drops, d)
	}
	sort.Slice(v.Drops, func(i, j int) bool {
		if v.Drops[i].Quantity == v.Drops[j].Quantity {
			return v.Drops[i].ID < v.Drops[j].ID
		}
		return v.Drops[i].Quantity > v.Drops[j].Quantity
	})
	if !lastBattle.IsZero() {
		v.LastBattleAt = lastBattle.UTC().Format(time.RFC3339)
	}
	return v
}

func safeActivityText(s string, n int) bool {
	return len(s) <= n && !strings.ContainsAny(s, "\r\n\x00")
}

// Read the retained rotation and current file from their beginning, rather than
// a tail that can discard an hour-long run. Both streams share deduplication.
// The per-file cap and scanner limit keep reads bounded; missing coverage stays
// explicitly partial. Never discover arbitrary archives or read private files.
func scanActivity(path string, limit int64, start, end time.Time, accept func(string, time.Time)) bool {
	archived := strings.TrimSuffix(path, filepath.Ext(path)) + ".bak" + filepath.Ext(path)
	seen := map[string]bool{}
	covered, complete := false, true
	for _, source := range []string{archived, path} {
		f, err := os.Open(source)
		if err != nil {
			if source != archived || !errors.Is(err, os.ErrNotExist) {
				complete = false
			}
			continue
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			complete = false
			continue
		}
		size := min(st.Size(), limit)
		if st.Size() > limit {
			complete = false
		}
		scanner := bufio.NewScanner(io.LimitReader(f, size))
		scanner.Buffer(make([]byte, 4096), 256<<10)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) < 25 || line[0] != '[' {
				continue
			}
			at, err := time.ParseInLocation("2006-01-02 15:04:05.000", line[1:24], time.Local)
			if err != nil {
				continue
			}
			if !at.After(start) {
				covered = true
			}
			if at.Before(start) || at.After(end) {
				continue
			}
			// Only relevant callbacks and queue records enter the in-memory set.
			if !strings.Contains(line, "Assistant::append_callback | ") && !strings.Contains(line, "[TaskQueueViewModel]") {
				continue
			}
			if seen[line] {
				continue
			}
			seen[line] = true
			accept(line, at)
		}
		if scanner.Err() != nil {
			complete = false
		}
		f.Close()
	}
	return covered && complete
}
