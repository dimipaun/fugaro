//go:build unix

package cli

import "syscall"

// oNoFollow is O_NOFOLLOW where the platform has it, for the creates in this
// package that must not write through a symlink planted at the path they
// write: internal/cli/config_init.go's writeMinimalFugaroYAML is the one
// (O_WRONLY|O_CREATE|O_EXCL|oNoFollow).
//
// Where the create also passes O_EXCL — and that is every site here — this
// flag adds nothing observable on this platform: POSIX says an O_CREAT|O_EXCL
// open fails on a symlink as well as on an existing file, dangling or not, so
// O_EXCL alone refuses the link. It is kept because it is what the call site
// always passed, and because a future create without O_EXCL would want it.
// The refusal itself is pinned by
// internal/cli/config_cmd_test.go's TestConfigInitWriteRefusesASymlinkedPath,
// whose mutation is the whole O_EXCL create (not this flag alone: setting
// oNoFollow to 0 leaves that test passing).
//
// It is a pair because Windows has no O_NOFOLLOW; see openfile_other.go for
// what that half keeps and loses. (internal/runner/openfile_unix.go's
// oNoFollow is the same pair, one package over: the runner cannot import it.)
const oNoFollow = syscall.O_NOFOLLOW
