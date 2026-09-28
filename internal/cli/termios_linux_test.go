package cli

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlInputQueue = unix.TIOCINQ // FIONREAD
)
