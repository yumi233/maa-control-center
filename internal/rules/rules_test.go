package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

const fixture = `{"UnknownRoot":{"large":9007199254740993},"Timers":{"keep":true},"Configurations":{"target":{"Gui":{"password":"private-placeholder"},"TaskQueue":[{"TaskType":"StartUp","IsEnable":true,"AccountName":"private-placeholder"},{"TaskType":"DepotMaintain","IsEnable":true,"UseMedicine":false,"UseStone":false,"UseExpiringMedicine":true,"PlanList":[{"DropId":"3001","Stage":"1-7","DropCount":10,"UseMedicine":false,"MedicineCount":0,"UseStone":false,"StoneCount":0,"UnknownPlan":true}]},{"TaskType":"Recruit","IsEnable":true,"MaxTimes":3,"UnknownTask":{"keep":true}}]},"other":{"TaskQueue":[{"TaskType":"StartUp","AccountName":"other-private"}]}}}`

func TestRulesPreserveUnknownAndAccountIsolation(t *testing.T) {
	result, e := Apply([]byte(fixture), "target", "secondary", "6.20.0", []Operation{{Scope: "plan", Task: 1, Plan: 0, Field: "DropCount", Value: float64(25)}})
	if e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{"9007199254740993", "UnknownPlan", "UnknownTask", "other-private", "private-placeholder", "Timers"} {
		if !strings.Contains(string(result.Data), text) {
			t.Fatalf("lost protected or unknown value %s", text)
		}
	}
	if len(result.Diff) != 1 {
		t.Fatal("unexpected changes")
	}
	p := string(Projection([]byte(fixture), "target"))
	if strings.Contains(p, "private") || strings.Contains(p, "Gui") || strings.Contains(p, "Unknown") {
		t.Fatal("projection exposed private or unknown settings")
	}
	if _, e = Apply(Synthetic(json.RawMessage(p)), "target", "secondary", "6.20.0", []Operation{{Scope: "task", Task: 2, Field: "MaxTimes", Value: float64(4)}}); e != nil {
		t.Fatal(e)
	}
}
func TestRulesRejectBypassAndUnsupportedVersions(t *testing.T) {
	cases := []Operation{{Scope: "task", Task: 0, Field: "AccountName", Value: "attacker"}, {Scope: "task", Task: 1, Field: "Filename", Value: "other.json"}, {Scope: "task", Task: 2, Field: "MaxTimes", Value: 4.5}, {Scope: "task", Task: 2, Field: "IsEnable", Value: "true"}, {Scope: "plan", Task: 1, Plan: 99, Field: "DropCount", Value: 3.0}, {Scope: "task", Task: 1, Field: "UseExpiringMedicine", Value: false}}
	for _, op := range cases {
		if _, e := Apply([]byte(fixture), "target", "secondary", "6.20.0", []Operation{op}); e == nil {
			t.Fatalf("bypass accepted: %+v", op)
		}
	}
	ops := []Operation{{Scope: "task", Task: 1, Field: "UseMedicine", Value: true}, {Scope: "plan", Task: 1, Field: "UseMedicine", Value: true}, {Scope: "plan", Task: 1, Field: "MedicineCount", Value: 1.0}}
	if _, e := Apply([]byte(fixture), "target", "secondary", "6.20.0", ops); e == nil {
		t.Fatal("secondary spend accepted")
	}
	disabled := strings.Replace(fixture, `"TaskType":"DepotMaintain","IsEnable":true`, `"TaskType":"DepotMaintain","IsEnable":false`, 1)
	if _, e := Apply([]byte(disabled), "target", "secondary", "6.20.0", ops); e == nil {
		t.Fatal("disabled secondary spend accepted")
	}
	if _, e := Apply([]byte(fixture), "target", "primary", "6.21.0", []Operation{{Scope: "task", Task: 2, Field: "MaxTimes", Value: 4.0}}); e == nil {
		t.Fatal("unverified version accepted")
	}
	if _, e := Apply([]byte(fixture), "target", "primary", "6.20.0", []Operation{{Scope: "order", Value: []any{1.0, 0.0, 2.0}}}); e == nil {
		t.Fatal("startup moved")
	}
}
func TestScheduleOnlyChangesExistingRoomBusinessFields(t *testing.T) {
	src := []byte(`{"future":9007199254740993,"plans":[{"period":[["08:00","20:00"]],"rooms":{"trading":[{"operators":["A"],"product":"Money","future":true}]}}]}`)
	r, e := ApplySchedule(src, "6.20.0", []Operation{{Scope: "schedule", Room: "trading", Field: "operators", Value: []any{"B", "C"}}})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(r.Data), "9007199254740993") || !strings.Contains(string(r.Data), "Money") {
		t.Fatal("unknown values lost")
	}
	if _, e = ApplySchedule(src, "6.20.0", []Operation{{Scope: "schedule", Room: "trading", Field: "product", Value: "Originium"}}); e == nil {
		t.Fatal("production switch bypass")
	}
}

func TestInventoryAddRemoveAndOrderKeepRules(t *testing.T) {
	add := Operation{Scope: "plan_add", Task: 1, Value: map[string]any{"Stage": "1-7", "DropId": "3002", "DropCount": 25.0}}
	r, e := Apply([]byte(fixture), "target", "secondary", "6.20.0", []Operation{add})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Diff) != 1 || !strings.Contains(string(r.Data), "UnknownPlan") {
		t.Fatal("new plan lost existing plan data")
	}
	remove := Operation{Scope: "plan_remove", Task: 1, Plan: 0}
	if _, e = Apply([]byte(fixture), "target", "secondary", "6.20.0", []Operation{remove}); e == nil {
		t.Fatal("deleted unknown plan fields")
	}
	remove.Plan = 1
	if _, e = Apply(r.Data, "target", "secondary", "6.20.0", []Operation{remove}); e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(r.Data, "target", "secondary", "6.20.0", []Operation{{Scope: "plan_order", Task: 1, Value: []any{1.0, 0.0}}}); e != nil {
		t.Fatal(e)
	}
	dangerous := strings.Replace(fixture, `"UseStone":false`, `"UseStone":true`, 1)
	add.Value = map[string]any{"Stage": "1-7", "DropId": "3002", "DropCount": 25.0, "UseStone": true, "StoneCount": 1.0}
	if _, e = Apply([]byte(dangerous), "target", "secondary", "6.20.0", []Operation{add}); e == nil {
		t.Fatal("new plan bypassed secondary stone policy")
	}
}

func TestNativeTimersAreProfileScopedAndReversible(t *testing.T) {
	src := []byte(`{"future":9007199254740993,"Timers":{"ForceScheduledStart":true,"List":[{"Id":3,"Config":"one","Hour":8,"Minute":0,"IsEnabled":true,"future":true},{"Id":4,"Config":"two","Hour":10,"Minute":0,"IsEnabled":false}]}}`)
	ops := []Operation{{Scope: "timer", Index: 3, Field: "Minute", Value: 15.0}}
	r, e := ApplyTimers(src, "one", "6.20.0", ops)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(r.Data), "9007199254740993") || !strings.Contains(string(r.Data), `"future": true`) {
		t.Fatal("unknown fields lost")
	}
	if _, e = ApplyTimers(src, "two", "6.20.0", ops); e == nil {
		t.Fatal("cross-profile timer write")
	}
	inv, e := Inverse(ops, r.Diff)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := ApplyTimers(r.Data, "one", "6.20.0", inv)
	if e != nil {
		t.Fatal(e)
	}
	if string(TimerProjection(restored.Data, "one")) != string(TimerProjection(src, "one")) {
		t.Fatal("rollback changed original timers")
	}
	if string(TimerProjection(r.Data, "two")) != string(TimerProjection(src, "two")) {
		t.Fatal("other profile timer changed")
	}
}
