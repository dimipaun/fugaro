//go:build !unix || aix || (solaris && !illumos)

package endpoint

import "os"

// This half is what the endpoint's lock can promise where the platform has no
// flock to take, and it is stated rather than implied: **no lock is taken**.
// A reader there decides liveness from the handshake alone, and it removes a
// registry file whose lock a live process would have held -- the file a daemon
// leaves between its lock and its listener, and the file of a daemon that is
// alive but not answering. The daemon is macOS and Linux only in v1 (D1232),
// so nothing serves an endpoint on these platforms: the file is here so the
// package builds wherever the CLI does, and so the loss is written down
// instead of being implied by a missing build.
//
// openNoFollow is 0: a platform without O_NOFOLLOW opens the path it is given,
// and the directory the registry sits in -- 0700, this user's -- is what bounds
// that here.
//
// fileOwner cannot say who owns a file, and false is "do not refuse": see
// lock_flock.go.

func lockFile(*os.File) error { return nil }

const openNoFollow = 0

func fileOwner(os.FileInfo) (uint32, bool) { return 0, false }
