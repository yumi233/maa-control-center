package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
)

type Task struct {
	Type                string `json:"TaskType"`
	Enabled             bool   `json:"IsEnable"`
	AccountName         string `json:"AccountName"`
	UseMedicine         bool   `json:"UseMedicine"`
	MedicineCount       *int   `json:"MedicineCount"`
	UseStone            bool   `json:"UseStone"`
	StoneCount          *int   `json:"StoneCount"`
	UseExpiringMedicine bool   `json:"UseExpiringMedicine"`
	Filename            string `json:"Filename"`
	PlanList            []struct {
		Stage         string `json:"Stage"`
		DropID        string `json:"DropId"`
		DropCount     int    `json:"DropCount"`
		UseMedicine   bool   `json:"UseMedicine"`
		MedicineCount int    `json:"MedicineCount"`
		UseStone      bool   `json:"UseStone"`
		StoneCount    int    `json:"StoneCount"`
	} `json:"PlanList"`
}
type config struct {
	Configurations map[string]struct {
		TaskQueue []Task `json:"TaskQueue"`
	} `json:"Configurations"`
}

func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func intp(n int) *int         { return &n }

func ReadBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("configuration changed during read")
	}
	return b, nil
}
func Allowed(path string, roots []string) bool {
	p, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return false
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return false
	}
	for _, root := range roots {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		root, err = filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}
func Parse(data []byte, allowed []string) ([]model.Profile, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("unsupported or damaged MAA configuration")
	}
	if len(c.Configurations) == 0 || len(c.Configurations) > 32 {
		return nil, fmt.Errorf("no supported profiles")
	}
	keys := []string{}
	for k := range c.Configurations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []model.Profile{}
	for _, k := range keys {
		v := c.Configurations[k]
		if len(v.TaskQueue) == 0 {
			continue
		}
		p := model.Profile{ID: k, Name: k, Tasks: []string{}, Inventory: []model.InventoryItem{}, Policy: model.Policy{Source: "MAA gui.new.json / enabled tasks", Verified: true, MedicineBudget: intp(0), StoneBudget: intp(0), ExpiringHours: intp(0)}}
		for _, t := range v.TaskQueue {
			if !t.Enabled {
				continue
			}
			p.Tasks = append(p.Tasks, t.Type)
			if t.Type == "StartUp" {
				p.AccountHint = t.AccountName
			}
			if t.Type == "Fight" {
				if t.UseMedicine {
					if t.MedicineCount == nil {
						p.Policy.MedicineBudget = nil
					} else if p.Policy.MedicineBudget != nil {
						*p.Policy.MedicineBudget += *t.MedicineCount
					}
				}
				if t.UseStone {
					if t.StoneCount == nil {
						p.Policy.StoneBudget = nil
					} else if p.Policy.StoneBudget != nil {
						*p.Policy.StoneBudget += *t.StoneCount
					}
				}
			}
			if (t.Type == "Fight" || t.Type == "DepotMaintain") && t.UseExpiringMedicine {
				p.Policy.ExpiringHours = intp(48)
			}
			if t.Type == "DepotMaintain" {
				for _, plan := range t.PlanList {
					if t.UseMedicine && plan.UseMedicine && p.Policy.MedicineBudget != nil {
						*p.Policy.MedicineBudget += plan.MedicineCount
					}
					if t.UseStone && plan.UseStone && p.Policy.StoneBudget != nil {
						*p.Policy.StoneBudget += plan.StoneCount
					}
					p.Inventory = append(p.Inventory, model.InventoryItem{ID: plan.DropID, Name: plan.DropID, Target: intp(plan.DropCount), Priority: len(p.Inventory) + 1, Stage: plan.Stage, Source: "maa_plan_current_unattributed"})
				}
			}
			if t.Type == "Infrast" && t.Filename != "" && Allowed(t.Filename, allowed) {
				if b, err := ReadBounded(t.Filename, 1<<20); err == nil {
					var doc map[string]json.RawMessage
					if json.Unmarshal(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), &doc) == nil && len(doc["plans"]) > 0 {
						p.Schedule = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
						p.ScheduleHash = hash(b)
					}
				}
			}
		}
		p.Editable = rules.Projection(data, k)
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no supported task queues; configuration remains read-only")
	}
	return out, nil
}

func Discover(root string, extraAllowed []string) model.Instance {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	id := hash([]byte(strings.ToLower(filepath.Clean(abs))))[:16]
	inst := model.Instance{ID: id, Name: filepath.Base(root), State: "unknown", Profiles: []model.Profile{}, Logs: []string{}}
	// Discover only explicit installations, never scan whole disks or MaaEnd.
	if _, err = os.Stat(filepath.Join(root, "MAA.exe")); err != nil {
		inst.State = "error"
		return inst
	}
	file := filepath.Join(root, "config", "gui.new.json")
	data, err := ReadBounded(file, 2<<20)
	if err != nil {
		inst.State = "error"
		return inst
	}
	inst.Fingerprint = hash(data)
	allowed := append([]string{root}, extraAllowed...)
	inst.Profiles, err = Parse(data, allowed)
	if err != nil {
		inst.State = "error"
		return inst
	}
	if names, err := ReadBounded(filepath.Join(root, "resource", "item_index.json"), 8<<20); err == nil {
		var index map[string]struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(bytes.TrimPrefix(names, []byte{0xef, 0xbb, 0xbf}), &index) == nil {
			for pi := range inst.Profiles {
				for ii := range inst.Profiles[pi].Inventory {
					item := &inst.Profiles[pi].Inventory[ii]
					if value, ok := index[item.ID]; ok && value.Name != "" {
						item.Name = value.Name
					}
				}
			}
		}
	}
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$v=(Get-Item -LiteralPath $env:MAACC_DISCOVERY_EXE).VersionInfo.ProductVersion; $running=@(Get-Process -Name MAA -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $env:MAACC_DISCOVERY_EXE }).Count -gt 0; @{version=$v;running=$running} | ConvertTo-Json -Compress`)
		cmd.Env = append(os.Environ(), "MAACC_DISCOVERY_EXE="+filepath.Join(root, "MAA.exe"))
		if b, err := cmd.Output(); err == nil {
			var v struct {
				Version string `json:"version"`
				Running bool   `json:"running"`
			}
			if json.Unmarshal(b, &v) == nil {
				inst.Version = v.Version
				inst.ProcessRunning = v.Running
				inst.ProcessVerified = v.Version != ""
			}
		}
	}
	inst.Logs = Tail(filepath.Join(root, "debug", "gui.log"), 60)
	return inst
}

var urls = regexp.MustCompile(`(?i)https?://[^\s]+`)
var secret = regexp.MustCompile(`(?i)(password|passwd|authorization|bearer|api.?key|access.?token|secret|remotecontrol.*user)`)
var winPath = regexp.MustCompile(`[A-Za-z]:\\[^\r\n]+`)

func Redact(line string) string {
	if secret.MatchString(line) {
		return "[已隐藏可能包含凭据的日志]"
	}
	line = urls.ReplaceAllString(line, "[地址已隐藏]")
	line = winPath.ReplaceAllString(line, "[本地路径已隐藏]")
	if len(line) > 2048 {
		line = line[:2048]
	}
	return strings.ToValidUTF8(line, "")
}
func Tail(path string, count int) []string {
	out := []string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out
	}
	offset := max(int64(0), info.Size()-(64<<10))
	if _, err = f.Seek(offset, 0); err != nil {
		return out
	}
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	start := max(0, len(lines)-count)
	for _, s := range lines[start:] {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, Redact(s))
		}
	}
	return out
}
