package rules

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed catalog.json
var Catalog json.RawMessage

const Version = "maa-6.20-r1"

type Fields struct {
	Bool    []string            `json:"bool"`
	Int     map[string][2]int   `json:"int"`
	String  []string            `json:"string"`
	Strings []string            `json:"strings"`
	Enum    map[string][]string `json:"enum"`
}

var catalog struct {
	Tasks    map[string]Fields                `json:"tasks"`
	Plan     Fields                           `json:"plan"`
	Schedule struct{ Fields, Rooms []string } `json:"schedule"`
}

func init() {
	if err := json.Unmarshal(Catalog, &catalog); err != nil {
		panic(err)
	}
}

type Operation struct {
	Scope string `json:"scope"`
	Task  int    `json:"task"`
	Plan  int    `json:"plan"`
	Field string `json:"field"`
	Value any    `json:"value"`
	Shift int    `json:"shift"`
	Room  string `json:"room"`
	Index int    `json:"index"`
}
type Diff struct {
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type Result struct {
	Data []byte `json:"-"`
	Diff []Diff `json:"diff"`
}

func Hash(b []byte) string        { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Supported(v string) bool     { return v == "6.20" || strings.HasPrefix(v, "6.20.") }
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func list(v any) []any            { a, _ := v.([]any); return a }
func has(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func integer(v any) (int, bool) {
	if n, ok := v.(json.Number); ok {
		i, e := n.Int64()
		return int(i), e == nil && int64(int(i)) == i
	}
	if n, ok := v.(int); ok {
		return n, true
	}
	n, ok := v.(float64)
	return int(n), ok && n == float64(int(n))
}
func stringsOK(v any) bool {
	a, ok := v.([]any)
	if !ok || len(a) > 64 {
		return false
	}
	for _, x := range a {
		s, ok := x.(string)
		if !ok || len(s) > 128 || strings.ContainsAny(s, "\x00\r\n") {
			return false
		}
	}
	return true
}
func valid(f Fields, k string, v any) bool {
	if has(f.Bool, k) {
		_, ok := v.(bool)
		return ok
	}
	if r, ok := f.Int[k]; ok {
		n, ok := integer(v)
		return ok && n >= r[0] && n <= r[1]
	}
	if a, ok := f.Enum[k]; ok {
		s, ok := v.(string)
		return ok && has(a, s)
	}
	if has(f.String, k) {
		s, ok := v.(string)
		return ok && len(s) <= 512 && !strings.ContainsAny(s, "\x00\r\n")
	}
	return has(f.Strings, k) && stringsOK(v)
}
func selected(data []byte, profile string) (map[string]any, []any, error) {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return nil, nil, fmt.Errorf("配置 JSON 无效")
	}
	p := object(object(doc["Configurations"])[profile])
	q := list(p["TaskQueue"])
	if len(q) == 0 || len(q) > 128 {
		return nil, nil, fmt.Errorf("配置档或任务队列不受支持")
	}
	return doc, q, nil
}

// Projection excludes paths, startup identifiers, all Gui settings and unknown fields.
func Projection(data []byte, profile string) json.RawMessage {
	_, q, err := selected(data, profile)
	if err != nil {
		return nil
	}
	out := []any{}
	for _, v := range q {
		m := object(v)
		typ, _ := m["TaskType"].(string)
		n := map[string]any{"TaskType": typ}
		if b, ok := m["IsEnable"].(bool); ok {
			n["IsEnable"] = b
		}
		f := catalog.Tasks[typ]
		for k, v := range m {
			if valid(f, k, v) {
				n[k] = v
			}
		}
		if typ == "DepotMaintain" {
			plans := []any{}
			for _, p := range list(m["PlanList"]) {
				np := map[string]any{}
				for k, v := range object(p) {
					if valid(catalog.Plan, k, v) {
						np[k] = v
					}
				}
				plans = append(plans, np)
			}
			n["PlanList"] = plans
		}
		out = append(out, n)
	}
	b, _ := json.Marshal(out)
	return b
}
func Synthetic(projection json.RawMessage) []byte {
	b, _ := json.Marshal(map[string]any{"Configurations": map[string]any{"target": map[string]any{"TaskQueue": projection}}})
	return b
}
func Apply(data []byte, profile, role, version string, ops []Operation) (Result, error) {
	if !Supported(version) {
		return Result{}, fmt.Errorf("未核实的 MAA 版本，仅允许只读")
	}
	if role != "primary" && role != "secondary" {
		return Result{}, fmt.Errorf("未知账号规则")
	}
	if len(ops) == 0 || len(ops) > 64 {
		return Result{}, fmt.Errorf("每次需要 1–64 条明确修改")
	}
	doc, q, err := selected(data, profile)
	if err != nil {
		return Result{}, err
	}
	diff := []Diff{}
	seen := map[string]bool{}
	for _, op := range ops {
		if len(op.Field) > 64 || len(op.Scope) > 32 || len(op.Room) > 32 {
			return Result{}, fmt.Errorf("修改标识超出长度限制")
		}
		if op.Scope == "plan_add" || op.Scope == "plan_remove" || op.Scope == "plan_order" {
			if len(ops) != 1 || op.Task < 0 || op.Task >= len(q) {
				return Result{}, fmt.Errorf("新增、删除或重排库存计划需要单独确认")
			}
			t := object(q[op.Task])
			if t["TaskType"] != "DepotMaintain" {
				return Result{}, fmt.Errorf("仅库存保持支持计划操作")
			}
			a := list(t["PlanList"])
			path := fmt.Sprintf("TaskQueue[%d].PlanList", op.Task)
			switch op.Scope {
			case "plan_add":
				m := object(op.Value)
				if m == nil || len(a) >= 128 {
					return Result{}, fmt.Errorf("新库存计划无效或数量超限")
				}
				for k, v := range m {
					if !valid(catalog.Plan, k, v) {
						return Result{}, fmt.Errorf("新库存计划含未知或无效字段")
					}
				}
				stage, _ := m["Stage"].(string)
				id, _ := m["DropId"].(string)
				if stage == "" || id == "" {
					return Result{}, fmt.Errorf("新计划必须指定关卡与材料 ID")
				}
				if !valid(catalog.Plan, "DropCount", m["DropCount"]) {
					return Result{}, fmt.Errorf("新计划必须指定有效目标数量")
				}
				for _, k := range []string{"UseMedicine", "UseStone"} {
					if _, ok := m[k]; !ok {
						m[k] = false
					}
				}
				for _, k := range []string{"MedicineCount", "StoneCount"} {
					if _, ok := m[k]; !ok {
						m[k] = float64(0)
					}
				}
				t["PlanList"] = append(a, m)
				diff = append(diff, Diff{fmt.Sprintf("%s[%d]", path, len(a)), nil, m})
			case "plan_remove":
				if op.Plan < 0 || op.Plan >= len(a) {
					return Result{}, fmt.Errorf("计划索引无效")
				}
				m := object(a[op.Plan])
				for k, v := range m {
					if !valid(catalog.Plan, k, v) {
						return Result{}, fmt.Errorf("该计划含未知字段，不能自动删除")
					}
				}
				t["PlanList"] = append(a[:op.Plan:op.Plan], a[op.Plan+1:]...)
				diff = append(diff, Diff{fmt.Sprintf("%s[%d]", path, op.Plan), m, nil})
			case "plan_order":
				order := list(op.Value)
				if len(order) != len(a) {
					return Result{}, fmt.Errorf("计划排序必须包含全部原计划")
				}
				out := make([]any, len(a))
				used := map[int]bool{}
				before := []any{}
				for i, v := range order {
					n, ok := integer(v)
					if !ok || n < 0 || n >= len(a) || used[n] {
						return Result{}, fmt.Errorf("计划排序无效")
					}
					used[n] = true
					out[i] = a[n]
					before = append(before, i)
				}
				t["PlanList"] = out
				diff = append(diff, Diff{path + " order", before, order})
			}
			continue
		}
		if op.Scope == "order" {
			if len(ops) != 1 {
				return Result{}, fmt.Errorf("任务排序必须单独确认")
			}
			a := list(op.Value)
			if len(a) != len(q) {
				return Result{}, fmt.Errorf("排序必须包含全部原有任务")
			}
			reordered := make([]any, len(q))
			used := map[int]bool{}
			for i, x := range a {
				n, ok := integer(x)
				if !ok || n < 0 || n >= len(q) || used[n] {
					return Result{}, fmt.Errorf("排序无效")
				}
				used[n] = true
				reordered[i] = q[n]
				if object(q[n])["TaskType"] == "StartUp" && i != 0 {
					return Result{}, fmt.Errorf("开始唤醒必须保持首项")
				}
			}
			before := []any{}
			for i := range q {
				before = append(before, i)
			}
			object(object(doc["Configurations"])[profile])["TaskQueue"] = reordered
			diff = append(diff, Diff{"TaskQueue order", before, a})
			continue
		}
		if op.Scope != "task" && op.Scope != "plan" {
			return Result{}, fmt.Errorf("不允许的修改范围")
		}
		if op.Task < 0 || op.Task >= len(q) {
			return Result{}, fmt.Errorf("任务索引无效")
		}
		target := object(q[op.Task])
		typ, _ := target["TaskType"].(string)
		fields, ok := catalog.Tasks[typ]
		if !ok {
			return Result{}, fmt.Errorf("任务 %s 保持只读", typ)
		}
		path := fmt.Sprintf("TaskQueue[%d].%s", op.Task, op.Field)
		if op.Scope == "plan" {
			if typ != "DepotMaintain" {
				return Result{}, fmt.Errorf("只有库存保持支持计划修改")
			}
			a := list(target["PlanList"])
			if op.Plan < 0 || op.Plan >= len(a) {
				return Result{}, fmt.Errorf("库存计划索引无效")
			}
			target = object(a[op.Plan])
			fields = catalog.Plan
			path = fmt.Sprintf("TaskQueue[%d].PlanList[%d].%s", op.Task, op.Plan, op.Field)
		}
		if target == nil || !valid(fields, op.Field, op.Value) {
			return Result{}, fmt.Errorf("字段 %s 的类型、范围或权限不符合规则", path)
		}
		if seen[path] {
			return Result{}, fmt.Errorf("同一字段不能重复修改")
		}
		seen[path] = true
		if same(target[op.Field], op.Value) {
			continue
		}
		diff = append(diff, Diff{path, target[op.Field], op.Value})
		target[op.Field] = op.Value
	}
	if len(diff) == 0 {
		return Result{}, fmt.Errorf("方案未产生配置变化")
	}
	// Validate the final document, including disabled tasks; enabling a task cannot hide a budget.
	if role == "secondary" {
		for _, v := range list(object(object(doc["Configurations"])[profile])["TaskQueue"]) {
			t := object(v)
			typ := t["TaskType"]
			if typ == "Fight" {
				for _, p := range [][2]string{{"UseMedicine", "MedicineCount"}, {"UseStone", "StoneCount"}} {
					if t[p[0]] == true {
						n, ok := integer(t[p[1]])
						if !ok || n != 0 {
							return Result{}, fmt.Errorf("副账号普通药剂与源石预算必须为 0")
						}
					}
				}
				if t["UseExpiringMedicine"] == true {
					if n, ok := integer(t["MedicineExpireDays"]); ok && n != 2 {
						return Result{}, fmt.Errorf("副账号临期窗口必须为 48 小时")
					}
				}
			}
			if typ == "DepotMaintain" {
				for _, v := range list(t["PlanList"]) {
					p := object(v)
					for _, k := range [][2]string{{"UseMedicine", "MedicineCount"}, {"UseStone", "StoneCount"}} {
						if t[k[0]] == true && p[k[0]] == true {
							n, ok := integer(p[k[1]])
							if !ok || n != 0 {
								return Result{}, fmt.Errorf("副账号库存计划不可使用普通药剂或源石")
							}
						}
					}
				}
			}
			if (typ == "Fight" || typ == "DepotMaintain") && t["IsEnable"] == true && t["UseExpiringMedicine"] != true {
				return Result{}, fmt.Errorf("副账号已启用作战必须保留 48 小时临期药策略")
			}
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		b = append([]byte{0xef, 0xbb, 0xbf}, b...)
	}
	return Result{b, diff}, nil
}

var roomPattern = regexp.MustCompile(`^(control|manufacture|trading|power|meeting|hire|dormitory|processing|training)$`)

func ApplySchedule(data []byte, version string, ops []Operation) (Result, error) {
	if !Supported(version) || len(ops) == 0 || len(ops) > 64 {
		return Result{}, fmt.Errorf("排班修改的版本或数量不受支持")
	}
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return Result{}, fmt.Errorf("排班 JSON 无效")
	}
	plans := list(doc["plans"])
	diff := []Diff{}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Scope != "schedule" || op.Shift < 0 || op.Shift >= len(plans) || !roomPattern.MatchString(op.Room) || !has(catalog.Schedule.Fields, op.Field) {
			return Result{}, fmt.Errorf("仅支持现有房间的干员、候选与跳过设置")
		}
		rooms := list(object(object(plans[op.Shift])["rooms"])[op.Room])
		if op.Index < 0 || op.Index >= len(rooms) {
			return Result{}, fmt.Errorf("排班房间索引无效")
		}
		m := object(rooms[op.Index])
		if m == nil {
			return Result{}, fmt.Errorf("排班房间不受支持")
		}
		if op.Field == "skip" {
			if _, ok := op.Value.(bool); !ok {
				return Result{}, fmt.Errorf("skip 必须为布尔值")
			}
		} else if !stringsOK(op.Value) {
			return Result{}, fmt.Errorf("干员必须是有效的名称列表")
		}
		p := fmt.Sprintf("plans[%d].rooms.%s[%d].%s", op.Shift, op.Room, op.Index, op.Field)
		if seen[p] {
			return Result{}, fmt.Errorf("重复修改")
		}
		seen[p] = true
		if !same(m[op.Field], op.Value) {
			diff = append(diff, Diff{p, m[op.Field], op.Value})
			m[op.Field] = op.Value
		}
	}
	if len(diff) == 0 {
		return Result{}, fmt.Errorf("排班未变化")
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		b = append([]byte{0xef, 0xbb, 0xbf}, b...)
	}
	return Result{b, diff}, err
}

func same(a, b any) bool        { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
func SameDiff(a, b []Diff) bool { return same(a, b) }
func ScheduleProjection(data []byte) any {
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	plans := []any{}
	for _, p := range list(doc["plans"]) {
		rooms := map[string]any{}
		for kind, v := range object(object(p)["rooms"]) {
			if !roomPattern.MatchString(kind) {
				continue
			}
			a := []any{}
			for _, r := range list(v) {
				m := map[string]any{}
				for k, v := range object(r) {
					if has(catalog.Schedule.Fields, k) {
						m[k] = v
					}
				}
				a = append(a, m)
			}
			rooms[kind] = a
		}
		plans = append(plans, map[string]any{"rooms": rooms})
	}
	return map[string]any{"plans": plans}
}
