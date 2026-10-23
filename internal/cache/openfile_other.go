//go:build !unix

package cache

import "os"

// openRootNoFollow is rt.OpenFile with plain O_RDONLY where there is no
// O_NOFOLLOW, and at these two call sites that loses **less about symlinks
// than the Unix half suggests**: the flag is inert on Unix too, because os.Root
// resolves the path itself, and on Windows os.Root is in fact the stricter of
// the two — Go's os.Root opens with O_NOFOLLOW_ANY on that platform and
// reports any symlink as an error (os/root_windows.go's openat), where the
// Unix one follows a link that stays inside the root. So what is dropped here
// is O_NONBLOCK, not the link refusal: a FIFO swapped in after the caller's
// Lstat is not opened non-blocking, and the fstat the caller makes on the
// handle is what refuses it — after an open that may wait for a writer.
//
// The Windows symlink story above is read from os.Root's source in the
// toolchain this build uses, not from a run on Windows: it is one of
// docs/windows-cli/reconciliation.md's U-items, not a fact this repository has
// checked.
func openRootNoFollow(rt *os.Root, name string) (*os.File, error) {
	return rt.OpenFile(name, os.O_RDONLY, 0)
}
