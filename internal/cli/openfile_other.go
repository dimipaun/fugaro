//go:build !unix

package cli

// oNoFollow is nothing where there is no O_NOFOLLOW, and **the create is what
// refuses a path something is already at**: O_EXCL is a create that fails when
// the name is taken, and that is what keeps writeMinimalFugaroYAML from
// replacing a file that appeared between its existence check and its write.
//
// What is lost is the atomicity of the other half of the refusal. On Unix the
// flag refuses a symlink at the path in the same syscall that creates the
// file; here the create is a separate decision from the check, so a link
// planted in that window is followed and its target created where the Unix
// half writes nothing. (A link whose target already exists is still refused
// here, by O_EXCL, because the target is taken; a *dangling* one is the case
// the flag is for, and it is the case that is lost.)
//
// What is kept is that this is not the only guard: writeMinimalFugaroYAML
// Lstats path first and refuses a symlink already at it on every platform
// (config_init.go), which is what covers a checkout that arrived with the link
// already in it. That Lstat is its own syscall, so the window above is real
// rather than closed — this is a stated weakening, not a silent one. The
// O_NOFOLLOW sites on Windows are F1/D12 in
// docs/windows-cli/reconciliation.md and D12 is open; the Windows behaviour
// described here is reasoned from the API, not run, because no Windows box has
// run this (U3, U5, U6, U8, U10, U11 are unrun).
const oNoFollow = 0
