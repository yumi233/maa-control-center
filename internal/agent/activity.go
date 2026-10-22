package agent

import (
	"bufio"
	"encoding/json"
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
	coreOK := scanActivity(filepath.Join(root, "debug", "asst.log"), 8<<20, start, end, func(line string, at time.Time) {
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
			if v.FirstSanity == nil {
				v.FirstSanity = r
			}
			v.Sanity = r
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
	guiOK := scanActivity(filepath.Join(root, "debug", "gui.log"), 1<<20, start, end, func(line string, _ time.Time) {
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

func scanActivity(path string, limit int64, start, end time.Time, accept func(string, time.Time)) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	offset := max(int64(0), st.Size()-limit)
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return false
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 256<<10)
	if offset > 0 {
		s.Scan()
	}
	complete := false
	seen := map[string]bool{}
	for s.Scan() {
		line := s.Text()
		if len(line) < 25 || line[0] != '[' {
			continue
		}
		at, e := time.ParseInLocation("2006-01-02 15:04:05.000", line[1:24], time.Local)
		if e != nil {
			continue
		}
		if !at.After(start) {
			complete = true
		}
		if at.Before(start) || at.After(end) || seen[line] {
			continue
		}
		seen[line] = true
		accept(line, at)
	}
	return complete && s.Err() == nil
}
