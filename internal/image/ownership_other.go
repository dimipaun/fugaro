//go:build !unix

package image

import "os"

// ownerOf has no owner to report where the platform does not record one:
// Windows' FileInfo carries neither a uid nor a gid (syscall.Stat_t does not
// exist there), so both ownership checks stop at
// "cannot read the owner of <dir> on this platform" and the check **fails**
// rather than passing silently. That is the conservative direction, and it is
// also what actually happens: these checks only ever run inside the built
// Linux image — `fugaro image selftest` is the binary inside that container,
// and a Windows CLI reaches it by starting the container — so a Windows
// binary standing in the ownership check is not in the place the check was
// written for, and saying so is more honest than a green row.
//
// The alternative the plan names (skip the check with a stated reason) is a
// decision, not this task's: D12 in docs/windows-cli/reconciliation.md is
// still open, and refusing is the option that does not need it answered.
func ownerOf(fi os.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }

// dirWritableBy is not reached where ownerOf reports no owner: both callers
// return on !ok first. It refuses anyway rather than guessing, so a caller
// added later that forgets the check gets a refusal and not a silent pass.
func dirWritableBy(os.FileInfo, uint32, uint32, uint32, uint32) bool { return false }
