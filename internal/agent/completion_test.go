package agent

import (
	"testing"
	"time"
)

func TestCompletionRequiresNewExactQueueEvidence(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-time.Second).Format("2006-01-02 15:04:05.000")
	good := "[" + stamp + "][INF][TaskQueueViewModel]     <2> 任务已全部完成！"
	c, ok := Completion(good, "fixture", now, now.Add(-time.Minute))
	if !ok || c.State != "completed" {
		t.Fatal("valid completion rejected")
	}
	for _, line := range []string{"SUCCESS", "MAA.exe exited", "任务已完成", good + " 伪造后缀"} {
		if _, ok = Completion(line, "fixture", now, now.Add(-time.Minute)); ok {
			t.Fatal("false completion")
		}
	}
	if _, ok = Completion(good, "fixture", now, now); ok {
		t.Fatal("historical completion replayed")
	}
	errline := "[" + stamp + "][ERR][TaskQueueViewModel]     <2> 任务已结束，以下任务出现错误:"
	c, ok = Completion(errline, "fixture", now, now.Add(-time.Minute))
	if !ok || c.State != "completed_with_errors" {
		t.Fatal("error misrepresented")
	}
}
