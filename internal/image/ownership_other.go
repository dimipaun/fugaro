//go:build !unix

package image

import "os"

// ownerOf has no owner to report where the platform does not record one:
// Windows' FileInfo carries neither a uid nor a gid (syscall.Stat_t does not
// exist there), so both ownership checks stop at
// "cannot read the owner of <dir> on this platform" and the check **fails**
// rather than passing.
//
// Why fail and not skip. T1.7 of docs/windows-cli/implementation-plan.md
// sketches the `other` half as "skipped with its reason stated", and D12 of
// docs/windows-cli/reconciliation.md recommends that shape for the POSIX mode
// assertions; the brief this task was run from rules the other way for a guard
// that cannot be enforced ("REFUSE or be documented as weaker ... never
// silently pass"). Refusing is also the smaller change: this package's check
// recorder has one verdict per check — `add(name, ok, detail)` — and no skip,
// so a skip here would either invent a third state or report a green row for
// a check that did not run, which is the silent pass the brief names. And the
// path is not reached in the field either way: these checks only ever run
// inside the built Linux image — `fugaro image selftest` is the binary inside
// that container, and a Windows CLI reaches it by starting the container — so
// a Windows binary standing in the ownership check is not in the place the
// check was written for, and saying so is more honest than a green row.
//
// The Windows answer to "what does a directory's owner mean here" is therefore
// stated and refused, not assumed; D12's ACL alternative stays open.
func ownerOf(fi os.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }

// dirWritableBy is not reached where ownerOf reports no owner: both callers
// return on !ok first. It refuses anyway rather than guessing, so a caller
// added later that forgets the check gets a refusal and not a silent pass.
func dirWritableBy(os.FileInfo, uint32, uint32, uint32, uint32) bool { return false }
