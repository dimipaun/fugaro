//go:build !unix

package procgroup

import "os/exec"

// This half is what procgroup can promise where the platform has no process
// group, and what it loses is stated rather than implied: **only the direct
// child is killed**, so a grandchild a command spawned survives the kill and
// is reparented. Windows' equivalent of a process group is a job object
// (windows.CreateJobObject + AssignProcessToJobObject through
// golang.org/x/sys/windows), which is not in this slice: the plan keeps it
// behind U9 in docs/windows-cli/reconciliation.md, which is the check that
// says whether the direct-child kill is enough, and this half is written so
// that answer can replace it without touching Run.
//
// (The wording is internal/claudeplugin/procattr_other.go's, which makes the
// same promise for the same reason.)
//
// Callers that depend on the tree being gone must not read this as that
// promise: internal/verify, internal/watch and the in-job runner all call Run
// to bound a command's lifetime, and on Windows a daemon that command started
// outlives it. Run itself is honest about the other half of it — the output
// drain already gives up after outputDrainTimeout when a child escaped.

// setProcGroup is a no-op where there is no process group: no SysProcAttr
// field is set, so the child runs in the parent's console process.
func setProcGroup(cmd *exec.Cmd) {}

// terminateGroup has no per-process signal to send where there is no process
// group: TerminateProcess is the only way to end the direct child, and it is
// not catchable, so the child gets no chance to clean up and Run's grace
// period has nothing left to wait for. (Process.Signal(os.Interrupt) is not an
// alternative: Windows' os package refuses it for a process with no console.)
// It is the direct child only, as this file's header says.
func terminateGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }

// killGroup kills the direct child, and nothing else: see this file's header
// for what that loses and terminateGroup for why it is a Kill and not a
// signal.
func killGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }
