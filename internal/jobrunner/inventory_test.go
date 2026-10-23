package jobrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yumi233/maa-control-center/internal/rules"
)

func TestDailyRunnerIsolatesCacheWithoutChangingBudgets(t *testing.T) {
	r, _, c, root, _ := fixture(t)
	path := filepath.Join(root, "config", "gui.new.json")
	before, _ := os.ReadFile(path)
	source := strings.Replace(string(before), `"TaskType":"Recruit"`, `"TaskType":"DepotMaintain","UpdateDepot":true,"UseMedicine":false,"UseStone":false,"UseExpiringMedicine":true,"PlanList":[{"DropId":"3303","DropCount":60,"Stage":"CA-5"}],"Unused":"kept"`, 1)
	os.WriteFile(path, []byte(source), 0600)
	os.MkdirAll(filepath.Join(root, "data"), 0700)
	cache := filepath.Join(root, "data", "DepotData.json")
	os.WriteFile(cache, []byte(`{"done":true,"data":{"3303":135}}`), 0600)
	if rec := r.Step(c); rec.State != "starting" {
		t.Fatalf("daily prepare failed: %+v", rec)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("new MAA would still load wrong account cache")
	}
	after, _ := os.ReadFile(path)
	for _, want := range []string{`"UseMedicine": false`, `"UseStone": false`, `"UseExpiringMedicine": true`, `"DropCount": 60`, `"Unused": "kept"`} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("changed config field %s", want)
		}
	}
}

func TestInventoryCacheIsolationAcrossAccounts(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "data"), 0700)
	path := filepath.Join(root, "data", "DepotData.json")
	cfg := []byte(`{"Configurations":{"one":{"TaskQueue":[{"TaskType":"DepotMaintain","IsEnable":true,"UpdateDepot":true}]},"two":{"TaskQueue":[{"TaskType":"DepotMaintain","IsEnable":true,"UpdateDepot":true}]}}}`)
	for _, profile := range []string{"one", "two"} {
		cache := []byte(`{"done":true,"data":{"3303":135},"syncTime":"old-account"}`)
		os.WriteFile(path, cache, 0600)
		id := rules.Hash([]byte(profile))
		if err := prepareInventory(root, profile, id, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("shared stale cache still visible to new MAA process")
		}
		before, err := os.ReadFile(filepath.Join(root, "data", "DepotData.maacc-before-"+id+".json"))
		if err != nil || string(before) != string(cache) {
			t.Fatal("original cache not preserved")
		}
	}
}

func TestInventoryIsolationRequiresFreshRecognition(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "data"), 0700)
	path := filepath.Join(root, "data", "DepotData.json")
	os.WriteFile(path, []byte(`{"data":{"item":25}}`), 0600)
	bad := []byte(`{"Configurations":{"one":{"TaskQueue":[{"TaskType":"DepotMaintain","IsEnable":true,"UpdateDepot":false}]}}}`)
	if prepareInventory(root, "one", rules.Hash([]byte("job")), bad) == nil {
		t.Fatal("allowed stock task without account-fresh recognition")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("changed cache for rejected task")
	}
	other := []byte(`{"Configurations":{"one":{"TaskQueue":[{"TaskType":"Recruit","IsEnable":true}]}}}`)
	if err := prepareInventory(root, "one", rules.Hash([]byte("other")), other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("unrelated queue cache modified")
	}
}

func TestInventoryIsolationRefusesSymlinkAndBackupOverwrite(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "data"), 0700)
	cfg := []byte(`{"Configurations":{"one":{"TaskQueue":[{"TaskType":"DepotMaintain","IsEnable":true,"UpdateDepot":true}]}}}`)
	id := rules.Hash([]byte("job"))
	path := filepath.Join(root, "data", "DepotData.json")
	outside := filepath.Join(t.TempDir(), "private.json")
	os.WriteFile(outside, []byte("private"), 0600)
	if os.Symlink(outside, path) == nil {
		if prepareInventory(root, "one", id, cfg) == nil {
			t.Fatal("followed cache symlink")
		}
		os.Remove(path)
	}
	os.WriteFile(path, []byte("current"), 0600)
	bak := filepath.Join(root, "data", "DepotData.maacc-before-"+id+".json")
	os.WriteFile(bak, []byte("original"), 0600)
	if prepareInventory(root, "one", id, cfg) == nil {
		t.Fatal("overwrote existing backup")
	}
	b, _ := os.ReadFile(bak)
	if string(b) != "original" {
		t.Fatal("backup changed")
	}
}
