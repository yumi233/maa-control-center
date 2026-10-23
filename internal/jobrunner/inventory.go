package jobrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yumi233/maa-control-center/internal/agent"
)

// MAA 6.20 prebuilds DepotMaintain fights from its process-wide DepotData cache,
// before StartUp switches accounts. An empty cache reserves every eligible plan;
// the native UpdateDepot task then recomputes its deficit before each fight.
// Preserve the previous cache beside it, on the same filesystem, while MAA is closed.
func prepareInventory(root, profile, job string, config []byte) error {
	var doc struct {
		Configurations map[string]struct {
			TaskQueue []struct {
				Type        string `json:"TaskType"`
				Enabled     bool   `json:"IsEnable"`
				UpdateDepot bool   `json:"UpdateDepot"`
			}
		}
	}
	if json.Unmarshal(bytes.TrimPrefix(config, []byte{0xef, 0xbb, 0xbf}), &doc) != nil {
		return fmt.Errorf("库存预检配置无效")
	}
	refresh := false
	for _, task := range doc.Configurations[profile].TaskQueue {
		if task.Type == "DepotMaintain" && task.Enabled {
			if !task.UpdateDepot {
				return fmt.Errorf("库存保持须启用本轮仓库识别，禁止沿用未核实账号的共享库存")
			}
			refresh = true
		}
	}
	if !refresh {
		return nil
	}
	path := filepath.Join(root, "data", "DepotData.json")
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !st.Mode().IsRegular() || !agent.Allowed(path, []string{root}) || !idPattern.MatchString(job) {
		return fmt.Errorf("共享库存缓存路径未核实，未启动任务")
	}
	if _, err = agent.ReadBounded(path, 2<<20); err != nil {
		return fmt.Errorf("共享库存缓存无法安全备份")
	}
	backup := filepath.Join(root, "data", "DepotData.maacc-before-"+job+".json")
	if _, err = os.Lstat(backup); !os.IsNotExist(err) {
		return fmt.Errorf("本轮库存缓存备份已存在，禁止覆盖")
	}
	if err = os.Rename(path, backup); err != nil {
		return fmt.Errorf("隔离共享库存缓存失败，未启动任务")
	}
	return nil
}
