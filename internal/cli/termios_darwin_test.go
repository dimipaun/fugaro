package cli

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	// ioctlInputQueue is FIONREAD, _IOR('f', 127, int) in <sys/filio.h>,
	// which x/sys/unix doesn't export for darwin.
	ioctlInputQueue = 0x4004667f
)
