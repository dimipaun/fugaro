//go:build unix

package image

import (
	"os"
	"syscall"
)

// ownerOf is the uid and gid a path's FileInfo records. ok is false only if
// Sys() is not a Stat_t, which cannot happen on this platform: Unix has one,
// with an owner in it.
//
// It exists as a pair because Windows has no syscall.Stat_t at all — a
// directory has no owner there, so the two ownership checks cannot read one.
// ownership_other.go states what they do instead.
func ownerOf(fi os.FileInfo) (uid, gid uint32, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}

// dirWritableBy reports whether the directory fi, owned by fileUID in group
// fileGID, is writable and searchable by the user uid in group gid: the owner,
// group and mode bits decide, and writing an entry in a directory needs both
// the write and the search bit. It reads the bits rather than writing for the
// callers that are not that user: checkManagedSettingsDir always (the runner
// writes there, not the selftest) and checkMiseCacheDir when the selftest runs
// as anyone but the directory's owner, such as root in the second scan
// container, for whom every directory is writable.
func dirWritableBy(fi os.FileInfo, fileUID, fileGID, uid, gid uint32) bool {
	var bits os.FileMode
	switch {
	case fileUID == uid:
		bits = fi.Mode().Perm() >> 6 & 0o7
	case fileGID == gid:
		bits = fi.Mode().Perm() >> 3 & 0o7
	default:
		bits = fi.Mode().Perm() & 0o7
	}
	return bits&0o3 == 0o3
}
