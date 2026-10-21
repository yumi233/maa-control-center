package configsync

import (
	"bytes"
	"encoding/json"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const instance = "0123456789abcdef"

func syncFixture(t *testing.T) (*Runner, Command, string, []byte) {
	t.Helper()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "config"), 0700)
	path := filepath.Join(root, "config", "gui.new.json")
	src := []byte(`{"future":9007199254740993,"Configurations":{"profile":{"TaskQueue":[{"TaskType":"StartUp","IsEnable":true,"AccountName":"fixture"},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3,"keep":true}]}}}`)
	os.WriteFile(path, src, 0600)
	r := &Runner{Enabled: true, Roots: []string{root}, BackupDir: filepath.Join(t.TempDir(), "backups"), Roles: map[string]string{"profile": "primary"}, Probe: func(string, []string) model.Instance {
		return model.Instance{ID: instance, Version: "6.20.0", ProcessVerified: true}
	}}
	c := Command{ID: rules.Hash([]byte("fixture-command")), InstanceID: instance, Profile: "profile", Role: "primary", Target: "config", BeforeHash: rules.Hash(src), RulesVersion: rules.Version, ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339), Operations: []rules.Operation{{Scope: "task", Task: 1, Field: "MaxTimes", Value: 4.0}}}
	result, e := rules.Apply(src, "profile", "primary", "6.20.0", c.Operations)
	if e != nil {
		t.Fatal(e)
	}
	c.Diff = result.Diff
	return r, c, path, src
}
func TestBackupApplyIdempotencyAndRollback(t *testing.T) {
	r, c, path, original := syncFixture(t)
	receipt := r.Execute(c)
	if receipt.State != "applied" {
		t.Fatalf("apply: %+v", receipt)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("9007199254740993")) {
		t.Fatal("unknown large integer lost")
	}
	backup, _ := os.ReadFile(filepath.Join(r.BackupDir, c.ID+".before"))
	if !bytes.Equal(original, backup) {
		t.Fatal("backup not exact")
	}
	again := r.Execute(c)
	if again.State != "applied" || again.AfterHash != receipt.AfterHash {
		t.Fatal("duplicate not idempotent")
	}
	rollback := c
	rollback.ID = rules.Hash([]byte("rollback"))
	rollback.RollbackOf = c.ID
	rollback.BeforeHash = receipt.AfterHash
	rollback.Diff = reverseDiff(c.Diff)
	rr := r.Execute(rollback)
	if rr.State != "applied" {
		t.Fatalf("rollback: %+v", rr)
	}
	b, _ = os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("rollback not byte exact")
	}
}
func TestConflictsRunningRoleUnknownAndCrashNeverOverwrite(t *testing.T) {
	for _, name := range []string{"changed", "running", "role", "expired", "disabled", "crash"} {
		t.Run(name, func(t *testing.T) {
			r, c, path, src := syncFixture(t)
			switch name {
			case "changed":
				os.WriteFile(path, append(src, ' '), 0600)
			case "running":
				r.Probe = func(string, []string) model.Instance {
					return model.Instance{ID: instance, Version: "6.20.0", ProcessVerified: true, ProcessRunning: true}
				}
			case "role":
				c.Role = "secondary"
			case "expired":
				c.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
			case "disabled":
				r.Enabled = false
			case "crash":
				os.MkdirAll(r.BackupDir, 0700)
				b, _ := json.Marshal(journal{c, Receipt{ID: c.ID, State: "unknown"}})
				os.WriteFile(filepath.Join(r.BackupDir, c.ID+".json"), b, 0600)
			}
			before, _ := os.ReadFile(path)
			rec := r.Execute(c)
			after, _ := os.ReadFile(path)
			if rec.State == "applied" || !bytes.Equal(before, after) {
				t.Fatalf("unsafe overwrite: %s %+v", name, rec)
			}
		})
	}
	r, c, path, _ := syncFixture(t)
	rec := r.Execute(c)
	os.WriteFile(path, []byte("manual-later-change"), 0600)
	rb := c
	rb.ID = rules.Hash([]byte("conflicting-rollback"))
	rb.RollbackOf = c.ID
	rb.BeforeHash = rec.AfterHash
	if r.Execute(rb).State != "conflict" {
		t.Fatal("rollback overwrote later change")
	}
	if !strings.Contains(string(mustRead(path)), "manual") {
		t.Fatal("later edit lost")
	}
}
func mustRead(path string) []byte { b, _ := os.ReadFile(path); return b }
func reverseDiff(d []rules.Diff) []rules.Diff {
	out := append([]rules.Diff{}, d...)
	for i := range out {
		out[i].Before, out[i].After = out[i].After, out[i].Before
	}
	return out
}
func TestDiffMismatchAndInstanceLockRejectWrite(t *testing.T) {
	r, c, path, src := syncFixture(t)
	c.Diff[0].Before = 999.0
	if rec := r.Execute(c); rec.State != "conflict" || !bytes.Equal(mustRead(path), src) {
		t.Fatal("unreviewed local diff applied")
	}
	r, c, path, src = syncFixture(t)
	os.MkdirAll(r.BackupDir, 0700)
	os.WriteFile(filepath.Join(r.BackupDir, instance+".lock"), nil, 0600)
	if rec := r.Execute(c); rec.State != "busy" || !bytes.Equal(mustRead(path), src) {
		t.Fatal("concurrent operation did not wait")
	}
}

func TestIdleReloadMergesRuntimeOnlyChangesAndPreservesBackup(t *testing.T) {
	r, c, path, original := syncFixture(t)
	c.ProjectionHash = rules.Hash(rules.Projection(original, c.Profile))
	running := true
	resumed := false
	r.Probe = func(string, []string) model.Instance {
		return model.Instance{ID: instance, Version: "6.20.0", ProcessVerified: true, ProcessRunning: running}
	}
	r.Prepare = func(string) (func(), error) {
		running = false
		var doc map[string]json.RawMessage
		json.Unmarshal(original, &doc)
		doc["runtime"] = json.RawMessage(`{"counter":1}`)
		b, _ := json.Marshal(doc)
		os.WriteFile(path, b, 0600)
		return func() { resumed = true }, nil
	}
	rec := r.Execute(c)
	if rec.State != "applied" || !resumed || rec.RequestHash != c.BeforeHash || rec.BeforeHash == c.BeforeHash {
		t.Fatalf("reload merge failed %+v", rec)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte(`"runtime"`)) {
		t.Fatal("runtime settings overwritten")
	}
	// Real business edits after preview must still conflict, even with projection fallback.
	r, c, path, original = syncFixture(t)
	c.ProjectionHash = rules.Hash(rules.Projection(original, c.Profile))
	os.WriteFile(path, bytes.ReplaceAll(original, []byte(`"MaxTimes":3`), []byte(`"MaxTimes":9`)), 0600)
	if rec = r.Execute(c); rec.State != "conflict" {
		t.Fatal("stale business values silently overwritten")
	}
}
