package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDisconnectRequiresFinalFailureInCurrentRun(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "debug"), 0700)
	now := time.Now().In(time.Local).Truncate(time.Second)
	line := func(at time.Time, text string) string {
		return "[" + at.Format("2006-01-02 15:04:05.000") + "][ERR][AsstProxy] <1> " + text + "\n"
	}
	for _, tc := range []struct {
		name, log string
		want      bool
	}{
		{"retry", line(now, "模拟器断开连接，正在重连…… 1"), false},
		{"old", line(now.Add(-time.Hour), "重连失败，连接断开！"), false},
		{"future", line(now.Add(time.Hour), "重连失败，连接断开！"), false},
		{"final", line(now, "重连失败，连接断开！"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.WriteFile(filepath.Join(root, "debug", "gui.log"), []byte(tc.log), 0600)
			if got := Disconnected(root, now.Add(-time.Minute), now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
