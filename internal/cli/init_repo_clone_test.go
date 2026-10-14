package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// TestPlanRepoRefusesCloneWorkflow (0.7.0 safety fix round 2, review
// finding: a plain `fugaro run --repo X --workflow W` launched from outside
// the checkout, or from a stale checkout whose local fugaro.yaml no longer
// says clone, decides clone only from the checkout, never reaching
// checkCloneImageSince's launch-time refusal at all, so it would launch
// against whatever job is actually deployed). Making it impossible for a
// checkout: clone workflow's job to exist in the first place, while
// cloneShipped is false, closes that gap: planRepo refuses before any
// cloud call (DiscoverRepo, Readiness, Plan and Apply are never reached —
// c, t and wd are nil here and would panic if touched), with the same
// guidance checkCloneImageSince gives.
//
// Mutation (run, restore): delete the `if !r.o.planOnly { if err :=
// refuseClonesNotShipped(...); err != nil { return nil, err } }` block at
// the top of planRepo — this test then fails with a nil pointer
// dereference (infra.DiscoverRepo(ctx, nil, spec) is reached and panics on
// the nil *infra.Clients), proving the refusal used to be what stopped
// execution before any cloud call.
func TestPlanRepoRefusesCloneWorkflow(t *testing.T) {
	r := &initRun{o: &initOptions{}, w: io.Discard}
	spec := infra.RepoSpec{Name: "acme/app", Workflows: map[string]infra.WorkflowSpec{
		"app": {Name: "app", Checkout: config.CheckoutClone},
	}}
	_, _, err := r.planRepo(t.Context(), nil, nil, nil, spec, infra.RepoRootOptions{}, true)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a refusal", err)
	}
	for _, want := range []string{
		"fugaro init --repo refuses to deploy acme/app workflow app",
		"checkout: clone's own runner side is not released yet",
		"generic-tool plan Task 17",
		"checkout: baked",
		"fugaro 0.7.1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
}

// TestPlanRepoRefusalNamesOnlyTheCloneWorkflow: a repository with one
// checkout: clone workflow and one baked sibling gets a refusal that names
// only the clone workflow — the baked one is not blamed for anything.
func TestPlanRepoRefusalNamesOnlyTheCloneWorkflow(t *testing.T) {
	r := &initRun{o: &initOptions{}, w: io.Discard}
	spec := infra.RepoSpec{Name: "acme/app", Workflows: map[string]infra.WorkflowSpec{
		"app": {Name: "app", Checkout: config.CheckoutClone},
		"api": {Name: "api"},
	}}
	_, _, err := r.planRepo(t.Context(), nil, nil, nil, spec, infra.RepoRootOptions{}, true)
	if err == nil || !strings.Contains(err.Error(), "workflow app") {
		t.Fatalf("err = %v, want a refusal naming app", err)
	}
	if strings.Contains(err.Error(), "api") {
		t.Errorf("refusal names the baked workflow too: %v", err)
	}
}

// TestPlanRepoPlanOnlySkipsTheCloneRefusal: --plan-only still runs the
// gate's own checks (DiscoverRepo and onward — nil c/t/wd here means
// reaching them panics, which this test relies on to prove the refusal
// was skipped, not that the rest of planRepo also works with nil clients).
func TestPlanRepoPlanOnlySkipsTheCloneRefusal(t *testing.T) {
	r := &initRun{o: &initOptions{planOnly: true}, w: io.Discard}
	spec := infra.RepoSpec{Name: "acme/app", Workflows: map[string]infra.WorkflowSpec{
		"app": {Name: "app", Checkout: config.CheckoutClone},
	}}
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic reaching infra.DiscoverRepo with a nil *infra.Clients: the clone refusal was not skipped")
		}
	}()
	_, _, _ = r.planRepo(t.Context(), nil, nil, nil, spec, infra.RepoRootOptions{}, true)
}

// TestPlanRepoNoCloneWorkflowUnaffected: a repository with no checkout:
// clone workflow at all is never refused by this gate (cloneWorkflowNames
// is empty, refuseClonesNotShipped is a no-op) — same panic-as-proof
// technique as TestPlanRepoPlanOnlySkipsTheCloneRefusal, this time with
// planOnly left false.
func TestPlanRepoNoCloneWorkflowUnaffected(t *testing.T) {
	r := &initRun{o: &initOptions{}, w: io.Discard}
	spec := infra.RepoSpec{Name: "acme/app", Workflows: map[string]infra.WorkflowSpec{
		"app": {Name: "app"},
	}}
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic reaching infra.DiscoverRepo with a nil *infra.Clients: a baked-only repository must not be refused")
		}
	}()
	_, _, _ = r.planRepo(t.Context(), nil, nil, nil, spec, infra.RepoRootOptions{}, true)
}
