package blobx

// OpenSource exposes openSource (openfile_unix.go) to the blobx_test package:
// the copy tests open a FIFO the way PutFile does — non-blocking — so the read
// they take is the read PutFile's copy would take.
var OpenSource = openSource

// CopyBounded exposes copyBounded to the blobx_test package. PutFile refuses a
// source that is not a regular file before it copies anything, so the two
// bounds the copy carries — the size it is given plus one byte, and the
// context — are only reachable, with a source that stalls or never ends, at
// the helper itself.
var CopyBounded = copyBounded
