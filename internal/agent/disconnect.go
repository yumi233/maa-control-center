package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A transient reconnect attempt is not a failure. Only the final GUI disconnect
// after the native retries is evidence of an interrupted run, never completion.
var disconnectedLine = regexp.MustCompile(`^\[([0-9-]{10} [0-9:.]{12})\]\[(ERR|INF|WRN)\]\[[^\]]+\]\s+<\d+> 重连失败，连接断开！$`)

func Disconnected(root string, since, now time.Time) bool {
	f, e := os.Open(filepath.Join(root, "debug", "gui.log"))
	if e != nil {
		return false
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return false
	}
	offset := max(int64(0), st.Size()-(256<<10))
	if _, e = f.Seek(offset, 0); e != nil {
		return false
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 128<<10)
	if offset > 0 {
		scan.Scan()
	}
	for scan.Scan() {
		m := disconnectedLine.FindStringSubmatch(strings.TrimSpace(scan.Text()))
		if m == nil {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04:05.000", m[1], time.Local)
		if err == nil && at.After(since) && !at.After(now.Add(2*time.Minute)) {
			return true
		}
	}
	return false
}
