package jobrunner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yumi233/maa-control-center/internal/agent"
	"github.com/yumi233/maa-control-center/internal/configsync"
	"github.com/yumi233/maa-control-center/internal/desktop"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/rules"
)

type Command struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	Profile    string `json:"profile"`
	Role       string `json:"role"`
	Type       string `json:"type"`
	ExpiresAt  string `json:"expires_at"`
	Action     string `json:"action"`
	Monitor    bool   `json:"monitor"`
}
type Receipt struct {
	StartedAt  string             `json:"started_at,omitempty"`
	Activity   *model.RunActivity `json:"activity,omitempty"`
	Completion *model.Completion  `json:"completion,omitempty"`
	ID         string             `json:"id"`
	State      string             `json:"state"`
	Detail     string             `json:"detail"`
	Time       string             `json:"time"`
}
type Controller interface {
	Probe(string) (desktop.Snapshot, error)
	CloseIdle(string) error
	Launch(string) error
	Start(string) error
	Stop(string) error
}
type record struct {
	Command  Command   `json:"command"`
	Stage    string    `json:"stage"`
	Started  time.Time `json:"started"`
	PID      int       `json:"pid"`
	Stopping bool      `json:"stopping"`
	Receipt  Receipt   `json:"receipt"`
}
type Runner struct {
	Enabled           bool
	Roots, Allowed    []string
	Roles             map[string]string
	StateDir, LockDir string
	Control           Controller
	Inspect           func(string, []string) model.Instance
	Now               func() time.Time
	mu                sync.Mutex
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func terminal(s string) bool {
	switch s {
	case "completed", "completed_with_errors", "failed", "cancelled", "unknown", "expired":
		return true
	}
	return false
}
func (r *Runner) Step(c Command) Receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	out := Receipt{ID: c.ID, State: "failed", Time: now.UTC().Format(time.RFC3339), Detail: "任务控制尚未在 Windows 启用"}
	if !r.Enabled {
		return out
	}
	if !idPattern.MatchString(c.ID) || !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(c.InstanceID) || (c.Type != "daily" && c.Type != "check") || r.Roles[c.Profile] != c.Role {
		out.Detail = "任务标识、类型或本地账号权限无效"
		return out
	}
	if r.StateDir == "" || r.LockDir == "" || r.Control == nil {
		out.Detail = "缺少执行状态目录或控制器"
		return out
	}
	if os.MkdirAll(r.StateDir, 0700) != nil || os.MkdirAll(r.LockDir, 0700) != nil {
		out.Detail = "无法创建执行记录目录"
		return out
	}
	path := filepath.Join(r.StateDir, c.ID+".json")
	var rec record
	exists := false
	if b, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(b, &rec) != nil || rec.Command.ID != c.ID {
			out.State = "unknown"
			out.Detail = "执行日志损坏，禁止重复启动"
			return out
		}
		exists = true
	}
	inspect := r.Inspect
	if inspect == nil {
		inspect = agent.Discover
	}
	root := ""
	var inst model.Instance
	for _, p := range r.Roots {
		v := inspect(p, r.Allowed)
		if v.ID == c.InstanceID {
			root = p
			inst = v
			break
		}
	}
	if root == "" || !inst.ProcessVerified || !rules.Supported(inst.Version) {
		out.Detail = "需要已核实的 MAA 6.20 安装"
		return out
	}
	lock := filepath.Join(r.LockDir, c.InstanceID+".lock")
	save := func(state, detail string) Receipt {
		out.State = state
		out.Detail = detail
		if c.Type == "daily" && (rec.Stage == "running" || rec.Stage == "finished" || rec.Stopping) && !rec.Started.IsZero() && rec.PID != 0 {
			out.StartedAt = rec.Started.UTC().Format(time.RFC3339)
			if terminal(state) && state != "unknown" {
				v := agent.Activity(root, rec.Started, now)
				out.Activity = &v
			}
		}
		rec.Command = c
		rec.Receipt = out
		if e := persist(path, rec); e != nil {
			out.State = "unknown"
			out.Detail = "执行记录写入失败，请核实，禁止重发"
		}
		if terminal(out.State) && out.State != "unknown" {
			release(lock, c.ID)
		}
		return out
	}
	if exists && terminal(rec.Receipt.State) && c.Action != "release" {
		return rec.Receipt
	}
	if c.Action == "release" {
		s, e := r.Control.Probe(root)
		if e != nil || !s.Known || (s.Running && !s.Idle) {
			out.State = "unknown"
			out.Detail = "MAA 尚未确认空闲，不能解除占用"
			return out
		}
		release(lock, c.ID)
		rec.Stage = "finished"
		return save("failed", "用户核实后已解除占用；不会自动重发")
	}
	if !exists && c.Monitor {
		rec.Stage = "unknown"
		return save("unknown", "服务器或 Agent 重启后没有可核实的启动记录，禁止自动重复执行")
	}
	if !exists {
		expiry, e := time.Parse(time.RFC3339, c.ExpiresAt)
		if e != nil || now.After(expiry) {
			out.State = "expired"
			out.Detail = "排队任务已过期，不补跑旧任务"
			return out
		}
		if c.Action == "stop" {
			out.State = "cancelled"
			out.Detail = "尚未启动的任务已取消"
			return out
		}
		s, e := r.Control.Probe(root)
		if e != nil || !s.Known || (s.Running && !s.Idle) {
			out.State = "waiting"
			out.Detail = "等待 MAA 空闲和可操作的当前用户窗口"
			return out
		}
		f, e := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			out.State = "waiting"
			out.Detail = "等待同一 MAA 的配置操作或其他账号任务完成"
			return out
		}
		_, we := f.WriteString(c.ID)
		ce := f.Close()
		if we != nil || ce != nil {
			out.State = "unknown"
			out.Detail = "互斥锁写入未确认"
			return out
		}
		rec = record{Command: c, Stage: "preparing", Started: now}
		if persist(path, rec) != nil {
			out.State = "unknown"
			out.Detail = "无法保存执行意图，未启动任务"
			return out
		}
		if s.Running {
			if e = r.Control.CloseIdle(root); e != nil {
				return save("unknown", "MAA 正常退出未确认，未启动新任务")
			}
		}
		// The saved file is read only after the old process flushed its own settings.
		file := filepath.Join(root, "config", "gui.new.json")
		b, e := agent.ReadBounded(file, 2<<20)
		if e != nil {
			return save("failed", "读取配置失败")
		}
		if e = rules.ValidateRun(b, c.Profile, c.Role, inst.Version); e != nil {
			_ = r.Control.Launch(root)
			return save("failed", e.Error())
		}
		if e = backup(filepath.Join(r.StateDir, c.ID+".before"), b); e != nil {
			return save("unknown", "启动前配置备份失败")
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), &doc) != nil {
			return save("failed", "配置 JSON 无效")
		}
		selection, _ := json.Marshal(c.Profile)
		doc["Current"] = selection
		next, _ := json.MarshalIndent(doc, "", "  ")
		if bytes.HasPrefix(b, []byte{0xef, 0xbb, 0xbf}) {
			next = append([]byte{0xef, 0xbb, 0xbf}, next...)
		}
		if e = configsync.AtomicWrite(file, next); e != nil {
			return save("unknown", "配置档切换结果未确认")
		}
		rec.Stage = "launching"
		if persist(path, rec) != nil {
			return save("unknown", "启动意图未能持久化")
		}
		if e = r.Control.Launch(root); e != nil {
			return save("failed", "MAA 启动失败，请检查 Windows 会话")
		}
		return save("starting", "已选择对应配置档，正在等待 MAA 界面就绪")
	}
	if rec.Stage == "preparing" {
		return save("unknown", "准备过程曾中断，未自动重新启动")
	}
	if inst.CurrentProfile != c.Profile {
		return save("unknown", "当前配置档被其他操作或原有定时切换，停止归属推断；请核实")
	}
	s, e := r.Control.Probe(root)
	if e != nil || !s.Known {
		out.State = rec.Receipt.State
		out.Detail = "MAA 窗口暂不可核实，将继续等待"
		return out
	}
	if rec.Stage == "launching" {
		if c.Action == "stop" {
			rec.Stage = "finished"
			return save("cancelled", "已取消；MAA 只打开了界面，未开始任务")
		}
		if !s.Running || !s.Idle {
			if now.Sub(rec.Started) > 3*time.Minute {
				return save("unknown", "MAA 启动后未确认空闲界面，未自动点击开始")
			}
			return save("starting", "等待 MAA 初始化完成")
		}
		if c.Type == "check" {
			rec.Stage = "finished"
			return save("completed", "已核对配置档切换与MAA空闲界面，未调用游戏任务开始入口")
		}
		rec.Stage = "invoking"
		rec.PID = s.PID
		rec.Started = now
		if persist(path, rec) != nil {
			return save("unknown", "无法保存开始意图，未点击执行")
		}
		if e = r.Control.Start(root); e != nil {
			return save("unknown", "开始请求结果未确认，请核实 MAA；不会自动重试")
		}
		rec.Stage = "running"
		return save("running", "已调用 MAA 原生开始按钮，等待实际队列结果")
	}
	if rec.Stage == "invoking" {
		return save("unknown", "开始操作曾中断，禁止重复点击开始")
	}
	if agent.NativeScheduledSince(root, rec.Started) {
		return save("unknown", "MAA 原有定时介入了本次运行，请核对任务；不会重复启动")
	}
	if !s.Running {
		markers := agent.Completions(root, c.InstanceID, rec.Started)
		if len(markers) > 0 {
			last := markers[len(markers)-1]
			rec.Stage = "finished"
			out.Completion = &last
			return save(last.State, "本次队列完成日志已确认，随后MAA退出；游戏奖励以实际为准")
		}
	}
	if !s.Running {
		return save("unknown", "MAA 进程退出但未取得确定完成回执，请核实")
	}
	if rec.PID != 0 && s.PID != rec.PID {
		return save("unknown", "MAA 进程已更换，不能沿用旧执行归属")
	}
	if c.Action == "stop" && !rec.Stopping {
		rec.Stopping = true
		if persist(path, rec) != nil {
			return save("unknown", "无法记录停止意图")
		}
		if e = r.Control.Stop(root); e != nil {
			return save("unknown", "停止请求未确认")
		}
		return save("stopping", "已请求 MAA 停止，等待实际空闲确认")
	}
	if rec.Stopping {
		if s.Idle {
			rec.Stage = "finished"
			return save("cancelled", "MAA 已确认空闲，任务已停止")
		}
		return save("stopping", "等待 MAA 完成停止")
	}
	if s.Idle {
		markers := agent.Completions(root, c.InstanceID, rec.Started)
		if len(markers) > 0 {
			last := markers[len(markers)-1]
			rec.Stage = "finished"
			out.Completion = &last
			return save(last.State, "MAA 队列日志与空闲状态共同确认结束；奖励和资源消耗仍以游戏为准")
		}
		if now.Sub(rec.Started) > 2*time.Minute {
			return save("unknown", "未取得本次队列完成日志，请检查 MAA；不会自动重跑")
		}
	}
	return save("running", "MAA 正在处理本次队列")
}
func persist(path string, rec record) error {
	b, e := json.Marshal(rec)
	if e != nil {
		return e
	}
	return configsync.AtomicWrite(path, b)
}
func release(path, id string) {
	b, e := os.ReadFile(path)
	if e == nil && strings.TrimSpace(string(b)) == id {
		_ = os.Remove(path)
	}
}
func backup(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func (r Receipt) Valid() bool {
	if !idPattern.MatchString(r.ID) || len(r.Detail) > 2000 {
		return false
	}
	_, e := time.Parse(time.RFC3339, r.Time)
	end, _ := time.Parse(time.RFC3339Nano, r.Time)
	if r.StartedAt != "" {
		start, err := time.Parse(time.RFC3339Nano, r.StartedAt)
		if err != nil || start.After(end) || end.Sub(start) > 48*time.Hour {
			return false
		}
	}
	if a := r.Activity; a != nil {
		if !terminal(r.State) || r.State == "unknown" || r.StartedAt == "" || a.StartedAt != r.StartedAt || a.FinishedAt != r.Time || !a.Valid() {
			return false
		}
	}
	if c := r.Completion; c != nil {
		at, ce := time.Parse(time.RFC3339, c.ObservedAt)
		if ce != nil || at.After(time.Now().Add(2*time.Minute)) || !idPattern.MatchString(c.ID) || !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(c.InstanceID) || c.State != r.State || (r.State != "completed" && r.State != "completed_with_errors") {
			return false
		}
	}
	return e == nil && (terminal(r.State) || r.State == "waiting" || r.State == "starting" || r.State == "running" || r.State == "stopping")
}
