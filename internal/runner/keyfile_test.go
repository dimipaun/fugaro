package runner_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// keyFileYAML keys its cache on README.md and anything under keys/
// (committed), baked/ (ignored, baked into the checkout) and late/
// (ignored, left by the agent).
var keyFileYAML = strings.Replace(cacheYAML, "key: [README.md]", `key: [README.md, "keys/*", "baked/*", "late/*"]`, 1)

// commitToRemote commits what change makes in a clone of h's remote.
func commitToRemote(t *testing.T, h *harness, change func(dir string)) {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	change(seed)
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "key files")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// runBounded runs h and fails if the run takes a minute: a key file must
// never hold the runner until the task timeout.
func runBounded(t *testing.T, h *harness, steps ...step) *runstore.Record {
	t.Helper()
	type result struct {
		rec *runstore.Record
		err error
	}
	done := make(chan result, 1)
	go func() {
		rec, err := h.run(t, steps...)
		done <- result{rec, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", r.rec, r.err)
		}
		return r.rec
	case <-time.After(time.Minute):
		t.Fatal("the run is still going after a minute: a key file holds it")
		return nil
	}
}

// plantAfterImplement is implement, then plant in the checkout: the agent
// leaves a key file behind for writeback.
func plantAfterImplement(h *harness, plant func(t *testing.T, dir string)) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		plant(t, h.deps.WorkDir)
		return res, err
	}
}

func mkfifo(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlinkZero(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", p); err != nil {
		t.Fatal(err)
	}
}

// A key file that is a FIFO or a link to /dev/zero is left out of the
// key, with a warning, at restore (from the committed or baked tree) and
// at writeback (left by the agent); the run is never held by it.
func TestNonRegularKeyFilesNeverHoldTheRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, h *harness) []step
	}{
		{"fifo at restore", func(t *testing.T, h *harness) []step {
			// Git can't store a FIFO: one in an ignored directory baked into
			// the checkout survives the runner's checkout and clean.
			testutil.Git(t, filepath.Dir(h.deps.WorkDir), "clone", "--quiet", h.remote, h.deps.WorkDir)
			mkfifo(t, filepath.Join(h.deps.WorkDir, "baked", "pipe"))
			return []step{implement("feature"), review("ship", 0)}
		}},
		{"dev-zero at restore", func(t *testing.T, h *harness) []step {
			commitToRemote(t, h, func(dir string) { symlinkZero(t, filepath.Join(dir, "keys", "zero")) })
			return []step{implement("feature"), review("ship", 0)}
		}},
		{"fifo at writeback", func(t *testing.T, h *harness) []step {
			return []step{plantAfterImplement(h, func(t *testing.T, dir string) { mkfifo(t, filepath.Join(dir, "late", "x")) }), review("ship", 0)}
		}},
		{"dev-zero at writeback", func(t *testing.T, h *harness) []step {
			return []step{plantAfterImplement(h, func(t *testing.T, dir string) { symlinkZero(t, filepath.Join(dir, "late", "x")) }), review("ship", 0)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, keyFileYAML, nil)
			withBucket(h)
			var logs bytes.Buffer
			h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
			// Ignored, so finalize doesn't commit what the agent leaves.
			commitToRemote(t, h, func(dir string) {
				f, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.WriteString("\nbaked/\nlate/\n"); err != nil {
					t.Fatal(err)
				}
			})
			steps := tc.setup(t, h)
			runBounded(t, h, steps...)
			if !strings.Contains(logs.String(), "cache key file left out") {
				t.Fatalf("no warning about the key file:\n%s", logs.String())
			}
		})
	}
}
