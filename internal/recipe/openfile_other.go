//go:build !unix

package recipe

import "os"

// openNoFollow is os.Open where there is no O_NOFOLLOW, so it is **the weaker
// half of this pair**: a link planted between ReadRepoFile's own Lstat walk
// and this open is followed here, and refused on Unix. What is kept is the
// rest of the guard, which is why the loss is small: the walk refuses a
// symlink for the .fugaro directory, for .fugaro/recipes and for the file
// itself (Windows' Lstat reports a symlink too), the fstat below refuses
// anything that is not a regular file, and the read is bounded by MaxBytes so
// a source that does not end cannot run on. What is lost is the atomicity of
// the walk: O_NOFOLLOW is what makes a swap after it fail rather than read
// through.
//
// This is a stated weakening, not a silent one: the O_NOFOLLOW sites on
// Windows are F1/D12 in docs/windows-cli/reconciliation.md and D12 is open.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
