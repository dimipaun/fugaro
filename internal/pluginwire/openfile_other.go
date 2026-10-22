//go:build !unix

package pluginwire

import "os"

// openNoFollow is os.Open where there is no O_NOFOLLOW, and it is therefore
// **the weaker half of this pair**: a symlink swapped in at the last moment is
// followed here, while the Unix half fails the open of it. What is kept, and
// why the loss is small:
//
//   - the caller reaches this only after checkPath's Lstat of the file and of
//     its .claude directory, which refuses a symlink for either — and Lstat
//     reports a symlink on Windows too (Go asks for the reparse point rather
//     than its target), so those checks are real there rather than vacuous;
//   - the fstat readFile makes on the handle this returns refuses anything
//     that is not a regular file, so a link to a device or a FIFO is refused;
//   - replaceFile, the only writer, writes through checkPath as well and
//     compares what it read with what it is about to overwrite, so a file
//     changed under it is refused rather than overwritten.
//
// What is lost is the atomicity of those checks: between checkPath's Lstat and
// this open, a link planted by another process is followed, where O_NOFOLLOW
// would fail it. O_NONBLOCK is lost with it, and a FIFO swapped in is refused
// by the fstat above rather than opening without blocking, so neither loss
// makes the read hang or write through a link on its own.
//
// This is a stated weakening, not a silent one: the O_NOFOLLOW sites on
// Windows and the mode guarantees there are the subject of D12 in
// docs/windows-cli/reconciliation.md, which the owner has left open — the
// Windows-correct guard (FILE_FLAG_OPEN_REPARSE_POINT through
// golang.org/x/sys/windows) is not in this slice.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
