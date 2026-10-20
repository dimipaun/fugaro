//go:build !unix

package cli

// oNoFollow is nothing where there is no O_NOFOLLOW, and **O_EXCL alone is
// what refuses a path something is already at**: the flag is what keeps
// writeMinimalFugaroYAML from replacing a file that appeared between its
// existence check and its write. What is lost is the other half of its
// refusal — a symlink planted at the path in that same window is followed
// here, where the Unix half fails the open of it and writes nothing. What is
// kept is the create's own atomicity (O_EXCL is a create that fails if the
// name is taken) and the fact that the caller, not this flag, decides whether
// a path is acceptable: config_init.go refuses a symlink at that path before
// it writes (and Windows' Lstat reports one).
//
// This is a stated weakening, not a silent one: the O_NOFOLLOW sites on
// Windows are F1/D12 in docs/windows-cli/reconciliation.md and D12 is open.
const oNoFollow = 0
