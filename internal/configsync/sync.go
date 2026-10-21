package configsync

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/agent"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
)

type Command struct {
	ProjectionHash string            `json:"projection_hash,omitempty"`
	ID             string            `json:"id"`
	InstanceID     string            `json:"instance_id"`
	Profile        string            `json:"profile"`
	Role           string            `json:"role"`
	Target         string            `json:"target"`
	BeforeHash     string            `json:"before_hash"`
	RulesVersion   string            `json:"rules_version"`
	Operations     []rules.Operation `json:"operations"`
	Diff           []rules.Diff      `json:"diff"`
	ExpiresAt      string            `json:"expires_at"`
	RollbackOf     string            `json:"rollback_of,omitempty"`
}
type Receipt struct {
	RequestHash    string `json:"request_hash,omitempty"`
	ProjectionHash string `json:"projection_hash,omitempty"`
	ID             string `json:"id"`
	State          string `json:"state"`
	Message        string `json:"message"`
	BeforeHash     string `json:"before_hash"`
	AfterHash      string `json:"after_hash"`
	Time           string `json:"time"`
}
type journal struct {
	Command Command `json:"command"`
	Receipt Receipt `json:"receipt"`
}
type Runner struct {
	Enabled        bool
	Roots, Allowed []string
	BackupDir      string
	Roles          map[string]string
	Probe          func(string, []string) model.Instance
	Prepare        func(string) (func(), error)
}

var safeID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (r *Runner) Execute(c Command) Receipt {
	out := Receipt{ID: c.ID, State: "blocked", Message: "配置写入尚未在 Windows 端启用", Time: time.Now().UTC().Format(time.RFC3339)}
	if !r.Enabled {
		return out
	}
	if !safeID.MatchString(c.ID) || !safeID.MatchString(c.BeforeHash) || c.RulesVersion != rules.Version {
		out.Message = "无效的方案标识或规则版本"
		return out
	}
	if r.BackupDir == "" {
		out.Message = "必须配置独立备份目录"
		return out
	}
	if os.MkdirAll(r.BackupDir, 0700) != nil {
		out.Message = "无法创建备份目录"
		return out
	}
	record := filepath.Join(r.BackupDir, c.ID+".json")
	if b, err := os.ReadFile(record); err == nil {
		var j journal
		if json.Unmarshal(b, &j) == nil && j.Command.ID == c.ID {
			return j.Receipt
		}
		out.State = "unknown"
		out.Message = "已有操作记录损坏，禁止重复写入"
		return out
	}
	expiry, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil || time.Now().After(expiry) {
		out.State = "expired"
		out.Message = "方案已过期，请重新预览"
		return out
	}
	probe := r.Probe
	if probe == nil {
		probe = agent.Discover
	}
	root := ""
	var inst model.Instance
	for _, p := range r.Roots {
		v := probe(p, r.Allowed)
		if v.ID == c.InstanceID {
			root = p
			inst = v
			break
		}
	}
	if root == "" || !inst.ProcessVerified || !rules.Supported(inst.Version) {
		out.Message = "需要已核实的 MAA 6.20 完全退出后重新预览"
		return out
	}
	if r.Roles[c.Profile] != c.Role {
		out.Message = "Windows 本地账号规则与方案不一致"
		return out
	}
	lock := filepath.Join(r.BackupDir, c.InstanceID+".lock")
	if !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(c.InstanceID) {
		out.Message = "实例标识无效"
		return out
	}
	lf, lockErr := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if lockErr != nil {
		out.State = "busy"
		out.Message = "此实例已有配置操作或未清理的中断锁，请先核实"
		return out
	}
	lf.Close()
	defer os.Remove(lock)
	if inst.ProcessRunning {
		if r.Prepare == nil {
			out.State = "busy"
			out.Message = "MAA 尚未退出，等待空闲后应用"
			return out
		}
		resume, e := r.Prepare(root)
		if e != nil {
			out.State = "busy"
			out.Message = e.Error()
			return out
		}
		if resume != nil {
			defer resume()
		}
		inst = probe(root, r.Allowed)
		if !inst.ProcessVerified || inst.ProcessRunning {
			out.State = "busy"
			out.Message = "MAA 退出尚未确认"
			return out
		}
	}
	path := filepath.Join(root, "config", "gui.new.json")
	if c.Target == "schedule" {
		b, e := agent.ReadBounded(path, 2<<20)
		if e != nil {
			out.Message = "无法读取排班引用"
			return out
		}
		var doc map[string]any
		json.Unmarshal(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), &doc)
		configs, _ := doc["Configurations"].(map[string]any)
		p, _ := configs[c.Profile].(map[string]any)
		q, _ := p["TaskQueue"].([]any)
		path = ""
		for _, v := range q {
			m, _ := v.(map[string]any)
			if m["TaskType"] == "Infrast" {
				f, _ := m["Filename"].(string)
				if f != "" {
					if path != "" && path != f {
						out.Message = "存在多个排班文件，暂不支持写入"
						return out
					}
					path = f
				}
			}
		}
		if path == "" || !agent.Allowed(path, append([]string{root}, r.Allowed...)) {
			out.Message = "排班引用不在允许范围"
			return out
		}
		resolved, _ := filepath.EvalSymlinks(path)
		for name, v := range configs {
			if name == c.Profile {
				continue
			}
			other, _ := v.(map[string]any)
			otherQ, _ := other["TaskQueue"].([]any)
			for _, v := range otherQ {
				t, _ := v.(map[string]any)
				f, _ := t["Filename"].(string)
				if f == "" {
					continue
				}
				p, _ := filepath.EvalSymlinks(f)
				if strings.EqualFold(resolved, p) {
					out.Message = "该排班被其他账号共用，请先建立独立排班文件"
					return out
				}
			}
		}
	} else if c.Target != "config" && c.Target != "timers" {
		out.Message = "未知文件目标"
		return out
	}
	// Resolve junctions/symlinks before replacing, never replace the link itself.
	path, err = filepath.EvalSymlinks(path)
	if err != nil || !agent.Allowed(path, append([]string{root}, r.Allowed...)) {
		out.Message = "文件边界检查失败"
		return out
	}
	original, err := agent.ReadBounded(path, 2<<20)
	if err != nil {
		out.Message = "配置读取失败"
		return out
	}
	out.BeforeHash = rules.Hash(original)
	if out.BeforeHash != c.BeforeHash && !((c.Target == "config" || c.Target == "timers") && c.ProjectionHash != "" && rules.Hash(commandProjection(c.Target, original, c.Profile)) == c.ProjectionHash) {
		out.State = "conflict"
		out.Message = "配置已变化，旧方案已拒绝；请重新预览"
		return out
	}
	out.RequestHash = c.BeforeHash
	var next []byte
	if c.RollbackOf != "" {
		if !safeID.MatchString(c.RollbackOf) {
			out.Message = "回滚标识无效"
			return out
		}
		b, e := os.ReadFile(filepath.Join(r.BackupDir, c.RollbackOf+".json"))
		var prior journal
		if e != nil || json.Unmarshal(b, &prior) != nil || prior.Receipt.State != "applied" || prior.Receipt.AfterHash != out.BeforeHash || prior.Command.Profile != c.Profile || prior.Command.InstanceID != c.InstanceID || prior.Command.Target != c.Target {
			out.Message = "备份或回滚指纹不匹配"
			return out
		}
		next, e = agent.ReadBounded(filepath.Join(r.BackupDir, c.RollbackOf+".before"), 2<<20)
		reversed := append([]rules.Diff{}, prior.Command.Diff...)
		for i := range reversed {
			reversed[i].Before, reversed[i].After = reversed[i].After, reversed[i].Before
		}
		if !rules.SameDiff(reversed, c.Diff) {
			out.Message = "回滚差异与已确认方案不一致"
			return out
		}
		if e != nil || rules.Hash(next) != prior.Receipt.BeforeHash {
			out.Message = "备份完整性校验失败"
			return out
		}
	} else {
		var result rules.Result
		if c.Target == "timers" {
			result, err = rules.ApplyTimers(original, c.Profile, inst.Version, c.Operations)
		} else if c.Target == "schedule" {
			result, err = rules.ApplySchedule(original, inst.Version, c.Operations)
		} else {
			result, err = rules.Apply(original, c.Profile, c.Role, inst.Version, c.Operations)
		}
		if err != nil {
			out.State = "rejected"
			out.Message = err.Error()
			return out
		}
		if !rules.SameDiff(result.Diff, c.Diff) {
			out.State = "conflict"
			out.Message = "Windows 实际差异与网页预览不一致，请重新预览"
			return out
		}
		next = result.Data
	}
	backup := filepath.Join(r.BackupDir, c.ID+".before")
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		out.State = "unknown"
		out.Message = "操作已有备份但无最终回执，请人工核实"
		return out
	}
	_, err = f.Write(original)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		out.Message = "备份写入失败"
		return out
	}
	// Write durable intent before mutation. A crash between write and receipt never replays.
	out.State = "unknown"
	out.Message = "已备份；写入结果待核实"
	out.AfterHash = rules.Hash(next)
	if err = save(record, journal{c, out}); err != nil {
		out.Message = "无法保存操作意图，未写入配置"
		return out
	}
	live := probe(root, r.Allowed)
	current, e := agent.ReadBounded(path, 2<<20)
	if !live.ProcessVerified || live.ProcessRunning || e != nil || rules.Hash(current) != out.BeforeHash {
		out.State = "conflict"
		out.Message = "写入前 MAA 状态或配置发生变化"
		_ = save(record, journal{c, out})
		return out
	}
	if err = replace(path, next); err != nil {
		out.Message = "文件替换未确认，请核实备份与原件"
		_ = save(record, journal{c, out})
		return out
	}
	verified, e := agent.ReadBounded(path, 2<<20)
	if e == nil && rules.Hash(verified) == out.AfterHash {
		out.State = "applied"
		if c.Target == "config" || c.Target == "timers" {
			out.ProjectionHash = rules.Hash(commandProjection(c.Target, verified, c.Profile))
		}
		out.Message = "已备份、应用并重新读取校验"
		if c.RollbackOf != "" {
			out.Message = "已恢复备份并重新读取校验"
		}
	}
	out.Time = time.Now().UTC().Format(time.RFC3339)
	if save(record, journal{c, out}) != nil {
		out.State = "unknown"
		out.Message = "配置结果回执未能持久化，请人工核实"
	}
	return out
}
func save(path string, j journal) error {
	b, e := json.MarshalIndent(j, "", "  ")
	if e != nil {
		return e
	}
	return replace(path, b)
}
func tempFile(path string, b []byte) (string, error) {
	f, e := os.CreateTemp(filepath.Dir(path), ".maacc-*")
	if e != nil {
		return "", e
	}
	name := f.Name()
	defer func() {
		f.Close()
		if e != nil {
			os.Remove(name)
		}
	}()
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	return name, e
}
func (r Receipt) Valid() bool {
	states := map[string]bool{"applied": true, "blocked": true, "expired": true, "unknown": true, "conflict": true, "rejected": true}
	_, e := time.Parse(time.RFC3339, r.Time)
	return safeID.MatchString(r.ID) && len(r.Message) <= 1024 && states[r.State] && e == nil && (r.BeforeHash == "" || safeID.MatchString(r.BeforeHash)) && (r.AfterHash == "" || safeID.MatchString(r.AfterHash)) && (r.RequestHash == "" || safeID.MatchString(r.RequestHash)) && (r.ProjectionHash == "" || safeID.MatchString(r.ProjectionHash))
}

// AtomicWrite uses the same durable replacement path for local execution journals.
func AtomicWrite(path string, data []byte) error { return replace(path, data) }

func commandProjection(target string, b []byte, profile string) []byte {
	if target == "timers" {
		return rules.TimerProjection(b, profile)
	}
	return rules.Projection(b, profile)
}
