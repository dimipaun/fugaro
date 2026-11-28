package selfupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// EnvDir and EnvSum are what the process that upgrades exports before it
// re-execs the installed binary: where that binary lives, and the sha256
// the runner checks it against before each later stage (F33). EnvDir set is
// also the marker that stops a second upgrade (D3442): the two variables
// are written together, and a process that carries them is one that already
// upgraded.
const (
	EnvDir = "FUGARO_UPGRADED_DIR"
	EnvSum = "FUGARO_UPGRADED_SHA"
)

// Install is what an upgrade left: the binary to re-exec, the directory it
// sits in, and the sha256 of that file. The directory is read-only, and the
// runner re-checks both of its properties before every later stage (F33).
type Install struct {
	Exe string
	Dir string
	Sum string
}

// errChanged is the one line a run ends with when the install an upgrade
// left no longer holds the binary it did (F33).
var errChanged = errors.New("the upgraded fugaro binary changed")

// Ensure upgrades the running binary to want when want is a release above
// have, and returns what to re-exec. An empty Install means there is
// nothing to do: no stamp, a development build, a release not above this
// one (D3449), or a process that already upgraded once (EnvDir is in its
// environment, D3442).
//
// It is VS1's upgrade plus the two things a job needs on top of it: the
// sha256 of what was installed, and the marker the caller exports before it
// re-execs.
func Ensure(ctx context.Context, want, have string) (Install, error) {
	if os.Getenv(EnvDir) != "" {
		return Install{}, nil
	}
	exe, err := upgrade(ctx, want, have)
	if err != nil || exe == "" {
		return Install{}, err
	}
	sum, err := fileSum(exe)
	if err != nil {
		return Install{}, failed(want, errChanged)
	}
	return Install{Exe: exe, Dir: filepath.Dir(exe), Sum: sum}, nil
}

// Check verifies the install an upgrade left: dir holds exactly one entry,
// the regular file `fugaro`, and its sha256 is sum (F33). A run whose
// stages run as one non-root user could have that user's own tool written
// beside the binary — which would shadow it for the stages to come — or its
// own copy written over it, and either is what this refuses. An unset dir
// or sum is a run that never upgraded, and there is nothing to check.
func Check(dir, sum string) error {
	if dir == "" || sum == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		return errChanged
	}
	if entries[0].Name() != binaryName || !entries[0].Type().IsRegular() {
		return errChanged
	}
	got, err := fileSum(filepath.Join(dir, binaryName))
	if err != nil || got != sum {
		return errChanged
	}
	return nil
}

// fileSum is path's sha256 in hex, the digest Check compares and the one a
// caller exports as EnvSum.
func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PointAtForTest sends the fetcher at base instead of the shipped release
// host, for the duration of a test, and returns the function that restores
// it. internal/cli's exec test drives the real fetch, verify and install
// through it; the shipped base is a constant, and nothing a run carries can
// reach this.
func PointAtForTest(base string) (restore func()) {
	old := releaseBase
	releaseBase = base
	return func() { releaseBase = old }
}
