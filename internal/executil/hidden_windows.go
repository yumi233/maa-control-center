//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// HideConsole keeps auxiliary console processes off the interactive desktop.
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
