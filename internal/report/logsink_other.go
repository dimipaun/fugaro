//go:build !unix

package report

import "io/fs"

// dirOwnedByUser has no owner to read where the platform does not record one:
// Windows' FileInfo is not a Stat_t and a directory there carries no uid, so
// there is nothing to compare with the user this process runs as.
//
// It answers yes rather than refusing, which is the opposite of what
// internal/image's ownerOf does, and the difference is what the check is for.
// There the check guards a directory a container writes into and refusing
// costs the container's own image build; here it guards the user's own state
// directory, `%LOCALAPPDATA%\fugaro\logs`, which Windows makes per-user by
// ACL — another account cannot write into it, so there is no foreign owner to
// refuse. What is left of the check on this side is the Lstat in logDir: the
// directory is not a symlink and not a file. The mode assertions, which are
// POSIX, are not made here either; logsink_windows_test.go pins the path
// shape only.
func dirOwnedByUser(fs.FileInfo) bool { return true }
