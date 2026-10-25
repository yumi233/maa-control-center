package jobrunner

import (
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/desktop"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeControl struct {
	s                               desktop.Snapshot
	starts, closes, launches, stops int
}

func (f *fakeControl) Probe(string) (desktop.Snapshot, error) { return f.s, nil }
func (f *fakeControl) CloseIdle(string) error                 { f.closes++; f.s.Running = false; return nil }
func (f *fakeControl) Launch(string) error {
	f.launches++
	f.s = desktop.Snapshot{Running: true, Known: true, Idle: true, PID: 42}
	return nil
}
func (f *fakeControl) Start(string) error { f.starts++; f.s.Idle = false; return nil }
func (f *fakeControl) Stop(string) error  { f.stops++; f.s.Idle = true; return nil }
func fixture(t *testing.T) (*Runner, *fakeControl, Command, string, *time.Time) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "config"), 0700)
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	src := `{"Current":"one","future":9007199254740993,"Configurations":{"one":{"TaskQueue":[{"TaskType":"StartUp","AccountName":"fixture-one","AccountSwitchEnabled":true,"IsEnable":true},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3}]},"two":{"TaskQueue":[{"TaskType":"StartUp","AccountName":"fixture-two","AccountSwitchEnabled":true,"IsEnable":true}]}}}`
	os.WriteFile(filepath.Join(root, "config", "gui.new.json"), []byte(src), 0600)
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	ctrl := &fakeControl{s: desktop.Snapshot{Running: true, Known: true, Idle: true, PID: 42}}
	r := &Runner{Enabled: true, Roots: []string{root}, Roles: map[string]string{"one": "primary", "two": "primary"}, StateDir: filepath.Join(t.TempDir(), "jobs"), LockDir: filepath.Join(t.TempDir(), "locks"), Control: ctrl, Now: func() time.Time { return now }}
	r.Inspect = func(string, []string) model.Instance {
		var d struct{ Current string }
		b, _ := os.ReadFile(filepath.Join(root, "config", "gui.new.json"))
		json.Unmarshal(b, &d)
		return model.Instance{ID: "0123456789abcdef", Version: "6.20.0", ProcessVerified: true, ProcessRunning: ctrl.s.Running, CurrentProfile: d.Current}
	}
	c := Command{ID: rules.Hash([]byte("one")), InstanceID: "0123456789abcdef", Profile: "one", Role: "primary", Type: "daily", ExpiresAt: now.Add(time.Hour).UTC().Format(time.RFC3339), Action: "run"}
	return r, ctrl, c, root, &now
}
func TestLaunchSerialCompletionAndNoDuplicate(t *testing.T) {
	r, ctrl, c, root, now := fixture(t)
	if rec := r.Step(c); rec.State != "starting" {
		t.Fatalf("prepare %+v", rec)
	}
	c2 := c
	c2.ID = rules.Hash([]byte("two"))
	c2.Profile = "two"
	if rec := r.Step(c2); rec.State != "waiting" {
		t.Fatal("shared instance not locked")
	}
	if rec := r.Step(c); rec.State != "running" {
		t.Fatalf("start %+v", rec)
	}
	c.Monitor = true
	r.Step(c)
	if ctrl.starts != 1 {
		t.Fatal("duplicate command clicked start twice")
	}
	*now = now.Add(5 * time.Second)
	ctrl.s.Idle = true
	line := "[" + now.In(time.Local).Format("2006-01-02 15:04:05.000") + "][INF][TaskQueueViewModel] <1> 任务已全部完成！\n"
	os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte(line), 0600)
	if rec := r.Step(c); rec.State != "completed" {
		t.Fatalf("completion %+v", rec)
	}
	r.Step(c)
	if ctrl.starts != 1 {
		t.Fatal("terminal job replayed")
	}
	if rec := r.Step(c2); rec.State != "starting" {
		t.Fatalf("second account never dispatched %+v", rec)
	}
	b, _ := os.ReadFile(filepath.Join(root, "config", "gui.new.json"))
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatal("unrelated number lost")
	}
}
func TestRestartCannotReplayUnknownAndStopNeedsIdle(t *testing.T) {
	r, ctrl, c, _, _ := fixture(t)
	c.Monitor = true
	if rec := r.Step(c); rec.State != "unknown" || ctrl.starts != 0 {
		t.Fatal("missing journal replayed")
	}
	r, ctrl, c, _, _ = fixture(t)
	r.Step(c)
	r.Step(c)
	c.Action = "stop"
	if rec := r.Step(c); rec.State != "stopping" {
		t.Fatal("stop request treated as completed")
	}
	if rec := r.Step(c); rec.State != "cancelled" || ctrl.stops != 1 {
		t.Fatalf("stop confirmation %+v", rec)
	}
	r, ctrl, c, _, _ = fixture(t)
	r.Step(c)
	path := filepath.Join(r.StateDir, c.ID+".json")
	b, _ := os.ReadFile(path)
	var rec record
	json.Unmarshal(b, &rec)
	rec.Stage = "invoking"
	persist(path, rec)
	if rr := r.Step(c); rr.State != "unknown" || ctrl.starts != 0 {
		t.Fatal("uncertain click replayed")
	}
}
func TestBusyRoleAndExpiredNeverStart(t *testing.T) {
	r, ctrl, c, _, now := fixture(t)
	ctrl.s.Idle = false
	if rec := r.Step(c); rec.State != "waiting" {
		t.Fatal("running local MAA not respected")
	}
	ctrl.s.Idle = true
	c.Role = "secondary"
	if rec := r.Step(c); rec.State != "failed" {
		t.Fatal("local role bypass")
	}
	c.Role = "primary"
	*now = now.Add(2 * time.Hour)
	if rec := r.Step(c); rec.State != "expired" || ctrl.starts != 0 {
		t.Fatal("expired execution started")
	}
}

func TestCheckSwitchesProfileWithoutStartingGame(t *testing.T) {
	r, ctrl, c, root, _ := fixture(t)
	c.Type = "check"
	c.Profile = "two"
	if rec := r.Step(c); rec.State != "starting" {
		t.Fatal(rec)
	}
	if rec := r.Step(c); rec.State != "completed" {
		t.Fatal(rec)
	}
	if ctrl.starts != 0 || ctrl.closes != 1 || ctrl.launches != 1 {
		t.Fatal("check invoked game or failed reload")
	}
	var doc struct{ Current string }
	b, _ := os.ReadFile(filepath.Join(root, "config", "gui.new.json"))
	json.Unmarshal(b, &doc)
	if doc.Current != "two" {
		t.Fatal("wrong profile")
	}
	r.Step(c)
	if ctrl.starts != 0 {
		t.Fatal("terminal check started game")
	}
}

func TestFinalDisconnectRetainsLockUntilVerifiedIdleRelease(t *testing.T) {
	r, ctrl, c, root, now := fixture(t)
	r.Step(c)
	r.Step(c)
	*now = now.Add(10 * time.Second)
	line := "[" + now.In(time.Local).Format("2006-01-02 15:04:05.000") + "][ERR][TaskQueueViewModel] <2> 重连失败，连接断开！\n"
	os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte(line), 0600)
	if rec := r.Step(c); rec.State != "unknown" || !strings.Contains(rec.Detail, "ADB连接中断") {
		t.Fatal("final disconnect was not reported", rec)
	}
	r.Step(c)
	if ctrl.starts != 1 {
		t.Fatal("disconnected run replayed")
	}
	lock := filepath.Join(r.LockDir, c.InstanceID+".lock")
	if _, e := os.Stat(lock); e != nil {
		t.Fatal("unknown run lost safety lock", e)
	}
	c.Action = "release"
	if rec := r.Step(c); rec.State != "unknown" {
		t.Fatal("busy instance was released", rec)
	}
	if _, e := os.Stat(lock); e != nil {
		t.Fatal("busy release removed lock")
	}
	ctrl.s.Idle = true
	if rec := r.Step(c); rec.State != "failed" {
		t.Fatal("verified idle release failed", rec)
	}
	if _, e := os.Stat(lock); !os.IsNotExist(e) {
		t.Fatal("verified idle lock retained", e)
	}
	if ctrl.starts != 1 {
		t.Fatal("release started another run")
	}
}
