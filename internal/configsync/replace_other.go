//go:build !windows

package configsync

import (
	"os"
	"path/filepath"
)

func replace(path string, b []byte) error {
	tmp, e := tempFile(path, b)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.OpenFile(filepath.Dir(path), os.O_RDONLY, 0)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
