package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/model"
)

var beijing = time.FixedZone("Asia/Shanghai", 8*3600)

type DayActivity struct {
	Date      string                `json:"date"`
	Runs      int                   `json:"runs"`
	Completed int                   `json:"completed"`
	Other     int                   `json:"other"`
	Reports   int                   `json:"reports"`
	Partial   int                   `json:"partial"`
	Tasks     map[string]int        `json:"tasks"`
	Stages    []model.StageActivity `json:"stages"`
	Drops     []model.ActivityDrop  `json:"drops"`
}
type Querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// Today is scoped to the immutable job owner/account, using its Beijing finish date.
// Detailed scans are bounded; missing and truncated history is explicitly counted.
func Today(db Querier, user, account, when string) (*DayActivity, error) {
	at, e := time.Parse(time.RFC3339Nano, when)
	if e != nil {
		return nil, e
	}
	local := at.In(beijing)
	begin := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, beijing)
	v := &DayActivity{Date: begin.Format("2006-01-02"), Tasks: map[string]int{}}
	filter := ` FROM jobs j LEFT JOIN job_activity a ON a.job_id=j.id WHERE j.owner_id=? AND j.account_id=? AND j.type='daily' AND j.state IN ('completed','completed_with_errors','cancelled','failed') AND j.updated_at>=? AND j.updated_at<=?`
	args := []any{user, account, begin.UTC().Format(time.RFC3339), at.UTC().Format(time.RFC3339)}
	e = db.QueryRow(`SELECT count(*),coalesce(sum(j.state='completed'),0)`+filter, args...).Scan(&v.Runs, &v.Completed)
	if e != nil {
		return nil, e
	}
	v.Other = v.Runs - v.Completed
	rows, e := db.Query(`SELECT a.document`+filter+` AND a.document IS NOT NULL ORDER BY j.updated_at DESC,j.id LIMIT 100`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	stages := map[string]int{}
	drops := map[string]model.ActivityDrop{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var a model.RunActivity
		if json.Unmarshal([]byte(raw), &a) != nil || !a.Valid() {
			continue
		}
		v.Reports++
		if !a.LogsComplete {
			v.Partial++
		}
		for _, t := range a.Tasks {
			v.Tasks[t]++
		}
		for _, s := range a.Stages {
			stages[s.Stage] += s.Settlements
		}
		for _, d := range a.Drops {
			x := drops[d.ID]
			x.ID = d.ID
			x.Name = d.Name
			x.Quantity += d.Quantity
			drops[d.ID] = x
		}
	}
	for s, n := range stages {
		v.Stages = append(v.Stages, model.StageActivity{Stage: s, Settlements: n})
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
	return v, rows.Err()
}

var taskOrder = []string{"startup", "depot", "inventory", "fight", "infrastructure", "recruit", "friends", "mall", "award"}

func taskText(tasks []string) string {
	known := map[string]bool{}
	for _, t := range tasks {
		known[t] = true
	}
	out := []string{}
	for _, t := range taskOrder {
		if known[t] {
			out = append(out, model.TaskNames[t])
		}
	}
	if len(out) == 0 {
		return "未采集到任务结束记录"
	}
	return strings.Join(out, "、")
}
func stageText(stages []model.StageActivity) string {
	out := []string{}
	for i, s := range stages {
		if i >= 8 {
			out = append(out, "其余见执行记录")
			break
		}
		out = append(out, fmt.Sprintf("%s × %d", short(s.Stage, 32), s.Settlements))
	}
	if len(out) == 0 {
		return "未采集到结算记录"
	}
	return strings.Join(out, "、")
}
func dropText(drops []model.ActivityDrop) string {
	out := []string{}
	for i, d := range drops {
		if i >= 12 {
			out = append(out, "其余省略")
			break
		}
		out = append(out, fmt.Sprintf("%s × %d", short(d.Name, 24), d.Quantity))
	}
	if len(out) == 0 {
		return "未采集到掉落记录"
	}
	return strings.Join(out, "、")
}
func short(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
func localTime(s string) string {
	at, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return "时间未采集"
	}
	return at.In(beijing).Format("01-02 15:04:05") + "（北京时间）"
}

func sanityText(a *model.RunActivity) string {
	if a == nil || a.Sanity == nil {
		return "理智：未采集到实际读数，结束时理智未知。"
	}
	r := a.Sanity
	out := fmt.Sprintf("最近实际理智：%d/%d，%s", r.Current, r.Maximum, localTime(r.ObservedAt))
	observed, e := time.Parse(time.RFC3339Nano, r.ObservedAt)
	end, ee := time.Parse(time.RFC3339Nano, a.FinishedAt)
	battle, be := time.Parse(time.RFC3339Nano, a.LastBattleAt)
	if e != nil || ee != nil || !a.LogsComplete || (!battle.IsZero() && (be != nil || battle.After(observed))) {
		return out + "\n结束时理智：未知（读数后有作战记录或日志不完整）。"
	}
	current := r.Current
	if current < r.Maximum {
		current = min(r.Maximum, current+int(end.Sub(observed)/(6*time.Minute)))
	}
	out += fmt.Sprintf("\n结束时估算：约 %d/%d（仅按每 6 分钟自然恢复，非结束时实测）。", current, r.Maximum)
	if r.Current < r.Maximum {
		out += "\n预计回满：" + localTime(observed.Add(time.Duration(r.Maximum-r.Current)*6*time.Minute).Format(time.RFC3339))
	}
	return out
}

func jobBody(f Facts, intro string) string {
	title := map[string]string{"running": "挂机已开始", "completed": "挂机已完成", "completed_with_errors": "挂机已结束，存在错误", "cancelled": "挂机已停止", "failed": "挂机执行失败", "expired": "挂机未能启动"}[f.State]
	if title == "" {
		title = "挂机状态更新"
	}
	out := "MAA Control Center · " + title + "\n"
	if intro != "" {
		out += intro + "\n"
	}
	out += "账号：" + f.Account + "\n时间：" + localTime(f.Time)
	if f.State == "running" {
		return out + "\nWindows 已调用 MAA 原生开始入口，将在结束后发送理智与当日汇总。\n记录：" + f.ID
	}
	if f.Activity != nil {
		a := f.Activity
		for _, issue := range a.Issues {
			switch issue {
			case "inventory_cache_stale":
				out += "\n需处理：本次仓库识别发现缺货，但旧库存预检已跳过对应刷本计划；队列结束不能视为补货完成。"
			case "auto_deploy_unavailable":
				out += "\n需处理：本轮无法启用代理指挥，反复点击后达到上限。请检查该关代理记录是否已解锁及界面识别；库存保持未完成，不能据此认定理智已消耗。"
			}
		}
		if len(a.Stages) == 0 && a.LogsComplete {
			out += "\n刷本核对：本轮关卡结算 0 次；队列完成不代表已消耗理智。"
		}
		start, _ := time.Parse(time.RFC3339Nano, a.StartedAt)
		end, _ := time.Parse(time.RFC3339Nano, a.FinishedAt)
		out += fmt.Sprintf("\n本次用时：%d 分 %d 秒", int(end.Sub(start)/time.Minute), int(end.Sub(start)/time.Second)%60)
		out += "\n" + sanityText(a) + "\n本次任务结束记录：" + taskText(a.Tasks) + "\n本次关卡结算记录：" + stageText(a.Stages) + "\n本次识别掉落：" + dropText(a.Drops)
		if a.Sanity == nil {
			out += "\n理智缺失原因：本轮未取得作战理智回调，不能判断当前是否已回满。"
		}
		if !a.LogsComplete {
			out += "\n本次日志不完整，以上仅为已采集部分。"
		}
	} else {
		out += "\n" + sanityText(nil) + "\n本次执行明细：未采集（旧版 Agent 或任务未实际启动）。"
	}
	if d := f.Today; d != nil {
		out += fmt.Sprintf("\n\n今天 %s（此账号）：结束 %d 轮，成功 %d 轮，其他结束状态 %d 轮。", d.Date, d.Runs, d.Completed, d.Other)
		tasks := []string{}
		for _, t := range taskOrder {
			if d.Tasks[t] > 0 {
				tasks = append(tasks, fmt.Sprintf("%s %d 轮", model.TaskNames[t], d.Tasks[t]))
			}
		}
		if len(tasks) > 0 {
			out += "\n今日任务结束记录：" + strings.Join(tasks, "、")
		}
		out += "\n今日关卡结算记录：" + stageText(d.Stages) + "\n今日识别掉落：" + dropText(d.Drops)
		if d.Reports < d.Runs || d.Partial > 0 {
			out += fmt.Sprintf("\n明细覆盖 %d/%d 轮，其中 %d 轮日志不完整；未采集部分不计入任务和掉落。", d.Reports, d.Runs, d.Partial)
		}
	}
	out += "\n详情：" + short(f.Detail, 240) + "\n任务结束记录不代表奖励全部领取；理智与物资以游戏实际为准。\n记录：" + f.ID
	return out
}
