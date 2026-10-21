//go:build unix

package procgroup

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts cmd in a process group of its own: Setpgid makes the
// command the leader of a new group whose id is its own pid, so everything it
// spawns — including the daemons it leaves behind — is in that group and can
// be signalled as one.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup asks cmd's whole process group to stop: SIGTERM to the
// negative pid, which is every process in it. It is the first half of Run's
// escalation, taken when ctx ends, before killGroup.
func terminateGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// killGroup SIGKILLs cmd's whole process group, which cannot be caught: it is
// what Run calls once the grace period has passed, and again whatever
// happened, to reap whatever the command left behind.
func killGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
