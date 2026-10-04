//go:build !unix

package config

import "os"

// openRegular is os.Open where there is no O_NOFOLLOW: the Lstat and the
// SameFile check are the only guards.
func openRegular(path string) (*os.File, error) { return os.Open(path) }
