// Package backendtest is the conformance suite every backend.Backend
// implementation runs against: "a contributor's pull request is passes
// backendtest" (design m11-setup-and-skills.md §7). It exercises the five
// lifecycle methods and the two timeout methods only; secrets, image
// builds and provisioning are not part of the seam and have no suite here.
package backendtest

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Repo, Slug and Workflow are the launch spec every conformance run uses.
// Factory must return a backend for which a job under Slug and Workflow
// can already launch (as fugaro init would have created it).
const (
	Repo     = "acme/app"
	Slug     = "acme-app"
	Workflow = "web"
)

// Factory builds one backend ready for the suite. Run calls it once per
// subtest, fresh: state from one subtest (a launched execution, say) must
// never leak into the next.
type Factory func(t *testing.T) backend.Backend

func spec() backend.LaunchSpec {
	return backend.LaunchSpec{Repo: backend.RepoRef{Repo: Repo, Slug: Slug}, Workflow: Workflow, RunID: "20260927-100000-abcd"}
}

// Run exercises Launch, Execution, List, Logs, Cancel, LongestTaskTimeout
// and TaskTimeout against the backend factory builds.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("LaunchAndInspect", func(t *testing.T) { testLaunchAndInspect(t, factory) })
	t.Run("ListActiveOnly", func(t *testing.T) { testListActiveOnly(t, factory) })
	t.Run("Cancel", func(t *testing.T) { testCancel(t, factory) })
	t.Run("Logs", func(t *testing.T) { testLogs(t, factory) })
	t.Run("Timeouts", func(t *testing.T) { testTimeouts(t, factory) })
}

func testLaunchAndInspect(t *testing.T, factory Factory) {
	ctx := context.Background()
	b := factory(t)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if ref.Name == "" {
		t.Fatal("Launch returned an empty execution name")
	}
	e, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if e.Name != ref.Name || e.State.Terminal() {
		t.Fatalf("Execution right after Launch = %+v, want a non-terminal state named %s", e, ref.Name)
	}
}

func testListActiveOnly(t *testing.T, factory Factory) {
	ctx := context.Background()
	b := factory(t)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	active, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsExecution(active, ref.Name) {
		t.Fatalf("List(ActiveOnly) after Launch = %+v, want %s in it", active, ref.Name)
	}
}

func testCancel(t *testing.T, factory Factory) {
	ctx := context.Background()
	b := factory(t)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	e, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution after Cancel: %v", err)
	}
	if !e.State.Terminal() {
		t.Fatalf("Execution after Cancel = %+v, want a terminal state", e)
	}
	active, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if containsExecution(active, ref.Name) {
		t.Fatalf("List(ActiveOnly) after Cancel still has %s: %+v", ref.Name, active)
	}
}

func testLogs(t *testing.T, factory Factory) {
	ctx := context.Background()
	b := factory(t)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	var got []backend.LogEntry
	err = b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(e backend.LogEntry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
}

func testTimeouts(t *testing.T, factory Factory) {
	ctx := context.Background()
	b := factory(t)
	if _, err := b.LongestTaskTimeout(ctx); err != nil {
		t.Fatalf("LongestTaskTimeout: %v", err)
	}
	d, err := b.TaskTimeout(ctx, Slug, Workflow)
	if err != nil {
		t.Fatalf("TaskTimeout: %v", err)
	}
	if d <= 0 {
		t.Fatalf("TaskTimeout = %v, want a positive duration", d)
	}
}

func containsExecution(list []backend.Execution, name string) bool {
	for _, e := range list {
		if e.Name == name {
			return true
		}
	}
	return false
}
