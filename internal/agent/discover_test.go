package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesKeepIndependentSchedulesAndBudgets(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	os.WriteFile(a, []byte(`{"title":"primary schedule","plans":[{"rooms":{"control":[{"operators":["Operator A"],"future_field":true}]}}],"extension":{"preserved":true}}`), 0600)
	os.WriteFile(b, []byte(`{"title":"secondary schedule","plans":[{"rooms":{"control":[{"operators":["Operator B"]}]}}]}`), 0600)
	jsonText := `{"Configurations":{"primary":{"TaskQueue":[{"TaskType":"StartUp","IsEnable":true,"AccountName":"example-main"},{"TaskType":"DepotMaintain","IsEnable":true,"UseMedicine":true,"UseStone":true,"PlanList":[{"DropId":"item","DropCount":25,"UseMedicine":false,"MedicineCount":999}]},{"TaskType":"Infrast","IsEnable":true,"Filename":"` + filepath.ToSlash(a) + `"}]},"secondary":{"TaskQueue":[{"TaskType":"DepotMaintain","IsEnable":true,"UseMedicine":false,"UseStone":false,"UseExpiringMedicine":true,"PlanList":[]},{"TaskType":"Fight","IsEnable":false,"UseMedicine":true,"MedicineCount":100},{"TaskType":"Infrast","IsEnable":true,"Filename":"` + filepath.ToSlash(b) + `"}]}}}`
	// JSON paths on Windows need escaping as well.
	jsonText = strings.ReplaceAll(jsonText, `\`, `\\`)
	beforeA, _ := os.ReadFile(a)
	beforeB, _ := os.ReadFile(b)
	profiles, err := Parse([]byte(jsonText), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatal("profiles were merged")
	}
	if *profiles[0].Policy.MedicineBudget != 0 || *profiles[1].Policy.MedicineBudget != 0 || *profiles[1].Policy.StoneBudget != 0 || *profiles[1].Policy.ExpiringHours != 48 {
		t.Fatal("resource policy changed or disabled task affected policy")
	}
	if !strings.Contains(string(profiles[0].Schedule), "future_field") || !strings.Contains(string(profiles[1].Schedule), "Operator B") {
		t.Fatal("independent schedules or unknown fields lost")
	}
	afterA, _ := os.ReadFile(a)
	afterB, _ := os.ReadFile(b)
	if string(beforeA) != string(afterA) || string(beforeB) != string(afterB) {
		t.Fatal("production source mutated")
	}
	if profiles[0].Inventory[0].Current != nil {
		t.Fatal("invented actual inventory")
	}
}
func TestReadBoundaryAndMalformedConfig(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "schedule.json")
	os.WriteFile(outside, []byte(`{"plans":[]}`), 0600)
	if Allowed(outside, []string{root}) {
		t.Fatal("read escaped allowlist")
	}
	if _, err := Parse([]byte(`{"Configurations":`), []string{root}); err == nil {
		t.Fatal("malformed config accepted")
	}
	if _, err := ReadBounded(outside, 3); err == nil {
		t.Fatal("oversized file accepted")
	}
}
func TestRedaction(t *testing.T) {
	for _, line := range []string{"Authorization: Bearer abc", "password=secret", "access_token=secret", "https://example.invalid/?token=secret"} {
		r := Redact(line)
		if strings.Contains(r, "secret") || strings.Contains(r, "Bearer") || strings.Contains(r, "https://") {
			t.Fatalf("credential not redacted: %q", r)
		}
	}
}
