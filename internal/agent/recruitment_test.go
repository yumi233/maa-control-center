package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecruitmentGuaranteedLevelRotationAndPreservedTags(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	mk := func(what string, level int, tags string, result string) string {
		return `[2026-10-31 10:29:01.605][TRC] Assistant::append_callback | SubTaskExtraInfo | {"taskchain":"Recruit","taskid":24,"uuid":"fixture","what":"` + what + `","details":{"level":` + string(rune('0'+level)) + `,"tags":` + tags + `,"result":` + result + `}}` + "\n"
	}
	tags := `["重装干员","辅助干员","控场","位移","支援机械"]`
	real := mk("RecruitResult", 5, tags, `[{"level":5,"tags":["位移","重装干员"],"opers":[{"level":5,"name":"可颂"}]}]`)
	bad := mk("RecruitResult", 3, tags, `[{"level":3,"tags":["辅助干员"],"opers":[{"level":5,"name":"初雪"}]}]`)
	six := mk("RecruitTagsDetected", 0, `["高级资深干员","近战位","输出","防护","重装干员"]`, `[]`)
	os.WriteFile(filepath.Join(root, "debug/asst.bak.log"), []byte(real+bad), 0600)
	os.WriteFile(filepath.Join(root, "debug/asst.log"), []byte(real+six), 0600)
	since := time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local)
	r := Recruitments(root, "instance", since, since.Add(time.Hour))
	if len(r) != 2 || r[0].Level != 5 || r[1].Level != 6 {
		t.Fatalf("wrong alerts: %+v", r)
	}
	for _, x := range r {
		x.JobID = strings.Repeat("a", 64)
		x.Profile = "Default"
		if !x.Valid() {
			t.Fatal("invalid typed observation", x)
		}
	}
	if len(Recruitments(root, "instance", since.Add(time.Hour), since.Add(2*time.Hour))) != 0 {
		t.Fatal("replayed historical tags")
	}
}
