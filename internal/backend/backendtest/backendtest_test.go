package backendtest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
)

// brokenBackend launches and lists correctly but Cancel is a no-op: the
// suite's job is to catch exactly this kind of contract violation.
type brokenBackend struct {
	execs map[string]backend.Execution
	n     int
}

func (b *brokenBackend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	b.n++
	name := fmt.Sprintf("exec-%d", b.n)
	b.execs[name] = backend.Execution{Name: name, State: backend.StatePending}
	return backend.ExecutionRef{Name: name}, nil
}

func (b *brokenBackend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	if e, ok := b.execs[name]; ok {
		return e, nil
	}
	return backend.Execution{}, backend.ErrNotFound
}

func (b *brokenBackend) List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error) {
	var out []backend.Execution
	for _, e := range b.execs {
		if f.ActiveOnly && e.State.Terminal() {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (b *brokenBackend) Logs(ctx context.Context, q backend.LogQuery, fn func(backend.LogEntry) error) error {
	return nil
}

// Cancel is the bug: it reports success without changing the execution's state.
func (b *brokenBackend) Cancel(ctx context.Context, name string) error { return nil }

func (b *brokenBackend) LongestTaskTimeout(ctx context.Context) (time.Duration, error) { return 0, nil }

func (b *brokenBackend) TaskTimeout(ctx context.Context, slug, workflow string) (time.Duration, error) {
	return backend.DefaultTaskTimeout, nil
}

// runBrokenBackendEnv gates TestBrokenBackendHelperProcess: a normal test
// run skips it, so it costs nothing there; TestConformanceCatchesBrokenBackend
// runs it in a subprocess with the variable set.
const runBrokenBackendEnv = "BACKENDTEST_RUN_BROKEN_BACKEND"

// TestBrokenBackendHelperProcess is the subprocess TestConformanceCatchesBrokenBackend
// drives. A failing subtest fails every ancestor test (there is no way to
// absorb it in the parent), so the only way to assert "the suite caught
// this" without failing this package's own `go test` run is to let it fail
// in a child process and check the exit code there.
func TestBrokenBackendHelperProcess(t *testing.T) {
	if os.Getenv(runBrokenBackendEnv) != "1" {
		t.Skip("only runs as TestConformanceCatchesBrokenBackend's subprocess")
	}
	backendtest.Run(t, func(t *testing.T) backend.Backend {
		return &brokenBackend{execs: map[string]backend.Execution{}}
	})
}

func TestConformanceCatchesBrokenBackend(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrokenBackendHelperProcess$")
	cmd.Env = append(os.Environ(), runBrokenBackendEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("backendtest.Run passed a backend whose Cancel does not change the execution's state:\n%s", out)
	}
}
