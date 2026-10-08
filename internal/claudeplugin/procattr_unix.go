//go:build unix

package claudeplugin

import (
	"os"
	"os/exec"
	"syscall"
)

// isolate puts cmd in its own process group and makes a cancel kill the
// group: claude spawns git and node children that would otherwise outlive
// the timeout, reparented to init.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
