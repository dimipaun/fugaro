//go:build !unix

package srctree

import "io/fs"

// ownedByUser has no owner to read where the platform does not record one:
// Windows' FileInfo is not a Stat_t and a directory there carries no uid, so
// there is nothing to compare with the user this process runs as.
//
// It answers yes rather than refusing, the same way internal/report's
// dirOwnedByUser does for the log directory: the cache is
// `%LOCALAPPDATA%\fugaro\source`, which Windows makes per-user by ACL, so
// there is no foreign owner to refuse. What is left of the check on this
// side is the Lstat in checkCacheDir: neither component is a link and
// neither is a plain file.
func ownedByUser(fs.FileInfo) bool { return true }
