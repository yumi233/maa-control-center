package jobrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecruitmentUsesDurableAccountInterval(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	line := `[2026-10-31 10:29:01.605][TRC] Assistant::append_callback | SubTaskExtraInfo | {"taskchain":"Recruit","taskid":24,"uuid":"fixture","what":"RecruitResult","details":{"level":5,"tags":["重装干员","辅助干员","控场","位移","支援机械"],"result":[{"level":5,"tags":["重装干员","位移"]}]}}` + "\n"
	os.WriteFile(filepath.Join(root, "debug/asst.log"), []byte(line), 0600)
	start := time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local)
	for i, x := range []struct {
		profile, state string
		start, end     time.Time
	}{{"Default", "completed", start.Add(-2 * time.Hour), start.Add(-time.Hour)}, {"secondary", "completed", start, start.Add(time.Hour)}, {"Default", "expired", start, start.Add(time.Hour)}} {
		id := strings.Repeat(string(rune('a'+i)), 64)
		r := record{Command: Command{ID: id, InstanceID: "instance", Profile: x.profile, Type: "daily"}, Started: x.start, Receipt: Receipt{State: x.state, Time: x.end.UTC().Format(time.RFC3339)}}
		b, _ := json.Marshal(r)
		os.WriteFile(filepath.Join(dir, id+".json"), b, 0600)
	}
	runner := Runner{StateDir: dir}
	v := runner.Recruitments(root, "instance", start, start.Add(time.Hour))
	if len(v) != 1 || v[0].Profile != "secondary" || v[0].JobID != strings.Repeat("b", 64) {
		t.Fatal("wrong account or expired replay", v)
	}
	if len(runner.Recruitments(root, "instance", start.Add(time.Hour), start.Add(2*time.Hour))) != 0 {
		t.Fatal("upgrade watermark ignored")
	}
}
