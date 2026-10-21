package agent

import (
	"bufio"
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Only newly observed, exact Chinese GUI completion messages are recognized.
// Process exit and generic SUCCESS are never completion evidence.
var completionLine = regexp.MustCompile(`^\[([0-9-]{10} [0-9:.]{12})\]\[(INF|ERR)\]\[TaskQueueViewModel\]\s+<\d+> (任务已全部完成！|任务已结束，以下任务出现错误:|任务已完成，但出现错误！)$`)

func Completion(line, instance string, now, since time.Time) (model.Completion, bool) {
	m := completionLine.FindStringSubmatch(line)
	if m == nil {
		return model.Completion{}, false
	}
	t, e := time.ParseInLocation("2006-01-02 15:04:05.000", m[1], time.Local)
	if e != nil || !t.After(since) || t.After(now.Add(2*time.Minute)) {
		return model.Completion{}, false
	}
	state := "completed"
	if m[2] == "ERR" || m[3] != "任务已全部完成！" {
		state = "completed_with_errors"
	}
	return model.Completion{ID: rules.Hash([]byte(instance + "/" + line)), InstanceID: instance, State: state, ObservedAt: t.UTC().Format(time.RFC3339), Source: "MAA 6.20 中文 GUI 队列结束日志"}, true
}
func WatchSince(path string) (time.Time, error) {
	b, e := os.ReadFile(path)
	if e == nil {
		var v struct {
			Since time.Time `json:"since"`
		}
		if json.Unmarshal(b, &v) != nil || v.Since.IsZero() {
			return time.Time{}, os.ErrInvalid
		}
		return v.Since, nil
	}
	if !os.IsNotExist(e) {
		return time.Time{}, e
	}
	t := time.Now()
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return t, e
	}
	b, _ = json.Marshal(map[string]time.Time{"since": t})
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return t, e
	}
	_, e = f.Write(b)
	f.Close()
	return t, e
}
func Completions(root, instance string, since time.Time) []model.Completion {
	out := []model.Completion{}
	f, e := os.Open(filepath.Join(root, "debug", "gui.log"))
	if e != nil {
		return out
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return out
	}
	offset := st.Size() - (256 << 10)
	if offset > 0 {
		f.Seek(offset, 0)
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 128<<10)
	if offset > 0 {
		scan.Scan()
	}
	for scan.Scan() {
		if c, ok := Completion(strings.TrimSpace(scan.Text()), instance, time.Now(), since); ok {
			out = append(out, c)
			if len(out) > 20 {
				out = out[1:]
			}
		}
	}
	return out
}

// NativeScheduledSince detects the GUI's own force/switch timers taking over a web run.
func NativeScheduledSince(root string, since time.Time) bool {
	f, e := os.Open(filepath.Join(root, "debug", "gui.log"))
	if e != nil {
		return false
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return false
	}
	offset := max(int64(0), st.Size()-(256<<10))
	f.Seek(offset, 0)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 128<<10)
	if offset > 0 {
		scan.Scan()
	}
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 25 || !strings.Contains(line, "[TaskQueueViewModel]") || (!strings.Contains(line, "Scheduled start:") && !strings.Contains(line, "Scheduled configuration change:")) {
			continue
		}
		end := strings.Index(line, "]")
		if end < 2 {
			continue
		}
		at, e := time.ParseInLocation("2006-01-02 15:04:05.000", line[1:end], time.Local)
		if e == nil && at.After(since) {
			return true
		}
	}
	return false
}
