package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivityUsesBoundedCallbacksAndCurrentDrops(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.Local)
	end := start.Add(40 * time.Minute)
	callback := func(at, body string) string {
		return "[2026-10-22 " + at + "][TRC][Px][Tx] Assistant::append_callback | SubTaskExtraInfo | " + body + "\n"
	}
	core := callback("09:59:00.000", `{"what":"StageDrops","details":{"stage":{"stageCode":"WRONG"},"drops":[{"itemId":"bad","itemName":"bad","quantity":99}]}}`)
	core += callback("10:03:00.000", `{"what":"SanityBeforeStage","details":{"current_sanity":12,"max_sanity":135}}`)
	drop := callback("10:06:00.000", `{"what":"StageDrops","details":{"stage":{"stageCode":"1-7"},"drops":[{"itemId":"rock","itemName":"固源岩","quantity":1}],"stats":[{"itemId":"rock","itemName":"固源岩","quantity":99}]}}`)
	core += drop + drop + callback("10:08:00.000", `{"what":"StageDrops","details":{"stage":{"stageCode":"1-7"},"drops":[{"itemId":"rock","itemName":"固源岩","quantity":2}]}}`)
	core += callback("10:09:00.000", `{"what":"SanityBeforeStage","details":{"current_sanity":0,"max_sanity":135}}`)
	core += callback("10:41:00.000", `{"what":"SanityBeforeStage","details":{"current_sanity":999,"max_sanity":135}}`)
	os.WriteFile(filepath.Join(root, "debug", "asst.log"), []byte(core), 0600)
	gui := "[2026-10-22 09:59:00.000][INF][TaskQueueViewModel]     <2> 早期记录\n[2026-10-22 10:20:00.000][INF][TaskQueueViewModel]     <2> 完成任务: 基建换班\n[2026-10-22 10:21:00.000][INF][TaskQueueViewModel]     <2> 完成任务: 领取奖励\n[2026-10-22 10:22:00.000][INF][TaskQueueViewModel]     <2> 完成任务: 私人身份文字\n"
	os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte(gui), 0600)
	v := Activity(root, start, end)
	if !v.Valid() || !v.LogsComplete || v.FirstSanity.Current != 12 || v.Sanity.Current != 0 || len(v.Drops) != 1 || v.Drops[0].Quantity != 3 || len(v.Stages) != 1 || v.Stages[0].Settlements != 2 || len(v.Tasks) != 2 {
		t.Fatalf("bad report: %+v", v)
	}
	os.Remove(filepath.Join(root, "debug", "gui.log"))
	if Activity(root, start, end).LogsComplete {
		t.Fatal("missing log accepted as complete")
	}
}

func TestActivityRetainedRotationAndLongRun(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "debug")
	os.MkdirAll(dir, 0700)
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.Local)
	callback := func(at, body string) string {
		return "[2026-10-26 " + at + "][TRC] Assistant::append_callback | SubTaskExtraInfo | " + body + "\n"
	}
	reading := callback("10:03:00.000", `{"what":"SanityBeforeStage","details":{"current_sanity":112,"max_sanity":205}}`)
	drop := callback("10:05:00.000", `{"what":"StageDrops","details":{"stage":{"stageCode":"AP-5"},"drops":[{"itemId":"4006","itemName":"采购凭证","quantity":22}]}}`)
	last := callback("10:50:00.000", `{"what":"SanityBeforeStage","details":{"current_sanity":4,"max_sanity":205}}`)
	noise := strings.Repeat("[2026-10-26 10:55:00.000][TRC] unneeded diagnostic detail\n", 180000)
	archive := "[2026-10-26 09:59:00.000][TRC] baseline\n" + reading + drop + last + noise
	os.WriteFile(filepath.Join(dir, "asst.bak.log"), []byte(archive), 0600)
	// Overlap and out-of-order duplicate callbacks must not double count or
	// replace the latest sanity reading with an older one.
	os.WriteFile(filepath.Join(dir, "asst.log"), []byte(drop+reading+"[2026-10-26 10:59:00.000][TRC] final\n"), 0600)
	os.WriteFile(filepath.Join(dir, "gui.bak.log"), []byte("[2026-10-26 09:59:00.000][INF] baseline\n[2026-10-26 10:10:00.000][INF][TaskQueueViewModel] <2> 完成任务: 库存保持\n"), 0600)
	os.WriteFile(filepath.Join(dir, "gui.log"), []byte("[2026-10-26 10:59:00.000][INF][TaskQueueViewModel] <2> 完成任务: 领取奖励\n"), 0600)
	v := Activity(root, start, start.Add(time.Hour))
	if !v.Valid() || !v.LogsComplete || len(v.Stages) != 1 || v.Stages[0].Settlements != 1 || len(v.Drops) != 1 || v.Drops[0].Quantity != 22 || v.FirstSanity.Current != 112 || v.Sanity.Current != 4 || len(v.Tasks) != 2 {
		t.Fatalf("rotation lost or duplicated evidence: %+v", v)
	}
	// A long run without rotation also exceeds the old 8 MiB tail.
	os.WriteFile(filepath.Join(dir, "asst.log"), []byte(archive), 0600)
	os.Remove(filepath.Join(dir, "asst.bak.log"))
	if v = Activity(root, start, start.Add(time.Hour)); !v.LogsComplete || len(v.Stages) != 1 || v.Sanity.Current != 4 {
		t.Fatalf("long current log lost evidence: %+v", v)
	}
	// Missing the portion containing this run's start remains partial.
	os.WriteFile(filepath.Join(dir, "asst.log"), []byte(last), 0600)
	if Activity(root, start, start.Add(time.Hour)).LogsComplete {
		t.Fatal("unavailable retained history reported as complete")
	}
}

func TestActivityDetectsBattleAfterLastSanity(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	core := "[2026-10-22 10:00:00.000][TRC] baseline\n[2026-10-22 10:01:00.000][TRC] Assistant::append_callback | SubTaskExtraInfo | {\"what\":\"SanityBeforeStage\",\"details\":{\"current_sanity\":20,\"max_sanity\":135}}\n[2026-10-22 10:02:00.000][TRC] Assistant::append_callback | SubTaskStart | {\"what\":\"ProcessTask\",\"details\":{\"task\":\"StartButton2\"}}\n"
	os.WriteFile(filepath.Join(root, "debug", "asst.log"), []byte(core), 0600)
	os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte("[2026-10-22 10:00:00.000][INF] baseline\n"), 0600)
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.Local)
	v := Activity(root, start, start.Add(10*time.Minute))
	if !v.Valid() || v.LastBattleAt == "" || v.LastBattleAt <= v.Sanity.ObservedAt {
		t.Fatal("subsequent battle not tracked")
	}
}

func TestActivityReportsSkippedInventoryWithoutInventingSanity(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	os.WriteFile(filepath.Join(root, "debug", "asst.log"), []byte("[2026-10-23 10:00:00.000][TRC] baseline\n"), 0600)
	gui := "[2026-10-23 10:00:00.000][INF] baseline\n[2026-10-23 10:04:14.124][WRN][TaskQueueViewModel]     <2> #2：技巧概要·卷3 实际 8/60 不足，预检时已跳过的计划无法在运行中添加，重新运行任务即可刷取\n[2026-10-23 10:04:14.128][INF][TaskQueueViewModel]     <2> 完成任务: 库存保持 (仓库识别)\n"
	os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte(gui), 0600)
	start := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	v := Activity(root, start, start.Add(13*time.Minute))
	if !v.Valid() || !v.LogsComplete || len(v.Issues) != 1 || v.Issues[0] != "inventory_cache_stale" || v.Sanity != nil || len(v.Stages) != 0 {
		t.Fatalf("wrong skipped report: %+v", v)
	}
}

func TestAutoDeployFailureIsReportedWithoutInventingSanity(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "debug"), 0700)
	start := time.Date(2026, 10, 31, 8, 0, 0, 0, time.Local)
	line := "[2026-10-31 08:03:45.000][INF] Assistant::append_callback | SubTaskExtraInfo | " + `{"what":"ExceededLimit","taskchain":"Fight","details":{"task":"UsePrts","exec_times":10,"max_times":10}}` + "\n"
	os.WriteFile(filepath.Join(root, "debug/asst.log"), []byte("[2026-10-31 08:00:00.000][INF] baseline\n"+line+line), 0600)
	os.WriteFile(filepath.Join(root, "debug/gui.log"), []byte("[2026-10-31 08:00:00.000][INF] baseline\n"), 0600)
	a := Activity(root, start, start.Add(time.Hour))
	if !a.Valid() || len(a.Issues) != 1 || a.Issues[0] != "auto_deploy_unavailable" || a.Sanity != nil || len(a.Stages) != 0 {
		t.Fatal(a)
	}
}
