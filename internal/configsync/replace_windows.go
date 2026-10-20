//go:build windows

package configsync

import (
	"golang.org/x/sys/windows"
	"os"
)

func replace(path string, b []byte) error {
	tmp, e := tempFile(path, b)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	from, e := windows.UTF16PtrFromString(tmp)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
