//go:build unix

package headroomproc

import (
	"os/exec"
	"syscall"
)

// These three are internal/procgroup's own technique, applied to a handle that
// is not Run's: Run owns waiting, draining and reaping the command it started,
// so a non-blocking start/stop pair cannot be built on it without rewriting
// its core. Setpgid makes the sidecar the leader of a new group whose id is
// its own pid, so everything it spawns — including a daemon it leaves behind —
// is signalled with it.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func killGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
