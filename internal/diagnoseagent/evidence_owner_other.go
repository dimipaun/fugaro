//go:build !unix

package diagnoseagent

import "io/fs"

// ownedByUser has no owner to read where the platform does not record one:
// Windows' FileInfo is not a Stat_t and a directory there carries no uid, so
// there is nothing to compare with the user this process runs as.
//
// It answers yes rather than refusing, as internal/report's dirOwnedByUser and
// internal/cmdlog's ownedByUser do for their own directories: the evidence
// folder is `%LOCALAPPDATA%\fugaro\troubleshoot`, which Windows makes per-user
// by ACL — another account cannot write into it, so there is no foreign owner
// to refuse. What is left of the check on this side is the Lstat in
// checkEvidenceDir: no component is a link and no component is a plain file.
func ownedByUser(fs.FileInfo) bool { return true }
