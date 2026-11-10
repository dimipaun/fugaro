package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// The unit's binary, and the clean exit that hands the daemon to the new one
// (O8, T7).
//
// A login unit names one stable path (`fugaro orchestrator run --binary
// <path>`, which `fugaro setup` renders into it), and a package manager that
// upgrades Fugaro replaces what is at that path. The daemon that is already
// running is the old binary and stays old: nothing about a `brew upgrade`
// reaches it. So each tick reads the version of the binary at the path the
// unit named — never os.Executable(), which on Linux resolves /proc/self/exe
// to the versioned path the upgrade deleted, so the read would fail after
// every upgrade and, a failed read being no reason to exit, the daemon would
// never restart — through a bounded `--version` call: no network, no model, a
// few milliseconds.
//
// The rule is **differs**, not "newer": a downgrade restarts the daemon too,
// and escalates nothing, because whoever can write that path already owns the
// login unit. A read that fails, or a version this cannot parse, is logged and
// is never a reason to exit: a broken package-manager link must not stop the
// daemon. And the check is skipped only while a Launch is in progress — never
// for a run merely in flight — so a busy queue still upgrades and a tick never
// abandons a start.

// unitBinaryTimeout bounds the `--version` call. It is generous for a process
// that only prints a line, and short enough that a path that hangs — a
// network filesystem, a script waiting on a lock — costs one tick and not the
// daemon.
const unitBinaryTimeout = 5 * time.Second

// unitBinaryVersion is the version the binary at path reports. It is the first
// line of its --version output, which is the one definition of that line
// (versionTemplate); anything else — no output, several lines, a call that
// failed — is an error, because a version this cannot read is a version the
// daemon must not act on.
func unitBinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, unitBinaryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("running %s --version: %w", path, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New("the binary printed no version line")
	}
	if strings.ContainsAny(v, "\n\r") {
		return "", errors.New("the binary printed more than one line for a version")
	}
	return v, nil
}

// binaryMoved reports whether the binary at path has moved on from the version
// this process runs, and is what the daemon hands over on.
func binaryMoved(ctx context.Context, path, running string) (bool, error) {
	v, err := unitBinaryVersion(ctx, path)
	if err != nil {
		return false, err
	}
	return v != running, nil
}
