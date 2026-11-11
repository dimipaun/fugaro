//go:build unix

package diagnoseagent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cmdlog"
)

// What is pinned here is the check the collector makes before it writes into
// the evidence folder, and it cannot be asserted anywhere else: the ownership
// rule reads a uid out of syscall.Stat_t, which Windows has no field for —
// evidence_owner_other.go says what that half does instead of refusing — and a
// link cannot be made there either. The rest of the package's evidence tests
// are platform-neutral and live in evidence_test.go.

// TestAnEvidenceFolderAnotherUserOwnsIsNotWrittenInto: XDG_STATE_HOME is
// attacker-influenced and the folder's names are predictable, so a directory
// another account owns would decide who can read the evidence this diagnosis
// leaves behind — the failing command's own log among it. The folder is really
// there and really usable by this process; it is still refused, and nothing is
// written into it.
func TestAnEvidenceFolderAnotherUserOwnsIsNotWrittenInto(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	base := getenv("XDG_STATE_HOME")
	foreign := filepath.Join(base, "fugaro")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}

	was := euid
	euid = func() int { return was() + 1 }
	t.Cleanup(func() { euid = was })

	_, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("Failed in 1.2s\n")), nil
		},
	})
	if err == nil {
		t.Fatal("Collect = nil error, want a refusal: the folder belongs to another account")
	}
	entries, rerr := os.ReadDir(foreign)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("the evidence was written into another user's directory: %v", entries)
	}
}

// TestALinkAtTheEvidenceFolderIsNotFollowed: the link stays inside the state
// directory — its target is relative, `elsewhere`, and not an absolute path,
// which os.Root refuses on its own — so a link the root would follow is exactly
// the one this check has to refuse. A log is really there at the name it points
// at, and the collection still writes nothing there: a link at `<state>/fugaro`
// decides where the evidence of this run's own failure would be left, and who
// could read it.
func TestALinkAtTheEvidenceFolderIsNotFollowed(t *testing.T) {
	getenv := evidenceEnv(t)
	id := evidenceID()
	base := getenv("XDG_STATE_HOME")
	dir := filepath.Join(base, "elsewhere", "troubleshoot", id.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", filepath.Join(base, "fugaro")); err != nil {
		t.Fatal(err)
	}

	_, err := Collect(context.Background(), id, Options{
		Version: "v0.5.2", Getenv: getenv, GOOS: "linux",
		Open: func(cmdlog.ID) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("Failed in 1.2s\n")), nil
		},
	})
	if err == nil {
		t.Fatal("Collect = nil error, want a refusal: a link at the folder's own name is not written through")
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("the evidence was written through the link: %v", entries)
	}
}
