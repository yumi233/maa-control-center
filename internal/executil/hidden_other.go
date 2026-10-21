//go:build !windows

package executil

import "os/exec"

func HideConsole(*exec.Cmd) {}
