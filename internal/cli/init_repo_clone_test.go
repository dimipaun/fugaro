package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// TestPlanRepoAcceptsCloneWorkflow: cloneShipped is true since 0.9.0, so
// planRepo deploys a checkout: clone workflow's job the way it deploys any
// other one. That is the whole assertion, and it is the flip's
// enabled-state pin: while cloneShipped was false, planRepo refused at its
// top — before DiscoverRepo, Readiness, Plan and Apply, and so before any
// cloud call — which is what the nil c, t and wd below prove is no longer
// reached. They are nil on purpose: planRepo panics on the way into
// infra.DiscoverRepo with a nil *infra.Clients, and that panic is the
// evidence that nothing refused first. A repository with a baked sibling is
// covered too: the gate never named one, and now there is no gate.
//
// Mutation (run, restore): set cloneShipped back to false in recipes_skew.go
// — planRepo returns refuseClonesNotShipped's error instead of panicking, so
// recover() comes back nil and the test fails. Deleting the gate outright
// instead would fail it the other way, with the panic.
func TestPlanRepoAcceptsCloneWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		workflows map[string]infra.WorkflowSpec
	}{
		{"clone only", map[string]infra.WorkflowSpec{
			"app": {Name: "app", Checkout: config.CheckoutClone},
		}},
		{"clone and a baked sibling", map[string]infra.WorkflowSpec{
			"app": {Name: "app", Checkout: config.CheckoutClone},
			"api": {Name: "api"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &initRun{o: &initOptions{}, w: io.Discard}
			spec := infra.RepoSpec{Name: "acme/app", Workflows: tc.workflows}
			defer func() {
				rec := recover()
				if rec == nil {
					t.Fatal("want a panic reaching infra.DiscoverRepo with a nil *infra.Clients: something refused a checkout: clone workflow before the cloud call, which a shipped gate must not")
				}
				if err, ok := rec.(error); ok && strings.Contains(err.Error(), "checkout: clone's own runner side is not released yet") {
					t.Fatalf("the disabled state's refusal is back: %v", err)
				}
			}()
			_, _, _ = r.planRepo(t.Context(), nil, nil, nil, spec, nil, infra.RepoRootOptions{}, true)
		})
	}
}

// TestPlanRepoPlanOnlySkipsTheCloneRefusal: --plan-only still runs the
// gate's own checks (DiscoverRepo and onward — nil c/t/wd here means
// reaching them panics, which this test relies on to prove the refusal
// was skipped, not that the rest of planRepo also works with nil clients).
// The skip matters only while cloneShipped is false; it is kept because it
// is what the flip-back depends on for a dry run, and because a plan-only
// refusal would be a behavior change nobody asked for.
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
	_, _, _ = r.planRepo(t.Context(), nil, nil, nil, spec, nil, infra.RepoRootOptions{}, true)
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
	_, _, _ = r.planRepo(t.Context(), nil, nil, nil, spec, nil, infra.RepoRootOptions{}, true)
}
