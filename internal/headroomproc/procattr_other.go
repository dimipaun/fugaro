//go:build !unix

package headroomproc

import "os/exec"

// This half is what headroomproc can promise where the platform has no process
// group, and what it loses is stated rather than implied: only the direct
// child is killed, so a sidecar that starts a daemon of its own leaves it
// behind. The wording and the loss are internal/procgroup's
// (procgroup_other.go), which makes the same promise for the same reason.
func setProcGroup(cmd *exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }

func killGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }
