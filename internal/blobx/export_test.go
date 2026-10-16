package blobx

// CopyBounded exposes copyBounded to the blobx_test package. PutFile refuses a
// source that is not a regular file before it copies anything, so the two
// bounds the copy carries — the size it is given plus one byte, and the
// context — are only reachable, with a source that stalls or never ends, at
// the helper itself.
var CopyBounded = copyBounded

// SetLinkFile replaces the hard link placeVerified places a download with, and
// returns the function that puts os.Link back. It is how a test reaches the
// fallback for a filesystem without hard links — an O_EXCL create plus a copy,
// which carries the same no-replace promise — without such a filesystem.
func SetLinkFile(fn func(oldname, newname string) error) (restore func()) {
	old := linkFile
	linkFile = fn
	return func() { linkFile = old }
}
