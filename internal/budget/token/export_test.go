package token

import "io"

// SetKeyReader replaces the reader NewKey draws from, for the test's
// duration; restore puts crypto/rand back.
func SetKeyReader(r io.Reader) (restore func()) {
	prev := keyReader
	keyReader = r
	return func() { keyReader = prev }
}

// SetDeviceUIDReader does the same for the device uid deviceNewUID draws: a
// uid is a device's name and a claim, so a test has to be able to hold the
// source still — and to hand it one that cannot fill the buffer.
func SetDeviceUIDReader(r io.Reader) (restore func()) {
	prev := deviceUIDReader
	deviceUIDReader = r
	return func() { deviceUIDReader = prev }
}
