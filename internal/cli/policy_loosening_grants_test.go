package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// policy_loosening_grants_test.go is the gate's half of N4: which members are
// the grants a publish hands a run (D2617), and the one widening that keeps its
// typed name beside them (D1542, D1552, reconciliation.md F24).
//
// The members are read one by one, because the gate's decision is this
// comparison and nothing else: `loosening()` is what makes the step one no plan
// gate judges (gateStep's ConvergeUnjudged), what refuses a coding agent's
// session, and what the door asks a typed name for — unless the grants are the
// whole of it, which is the case a grant is published without one.

// TestGrantMembersAreLoosening: every member of the cascade and every grade's
// access is a grant of the gate's own list, so a publish that adds one or
// rewrites it is a step a coding agent's session is refused and a managed
// project's --yes cannot make (D2617). A member this publish REMOVES is not one
// — it takes a grant away — and neither is a grade's model, which decides what
// a rung runs and not whose credential it spends.
func TestGrantMembersAreLoosening(t *testing.T) {
	// One budget block on both sides of every case, so the ceilings this
	// comparison also reads are equal and what is left is the grants.
	budget := projectpolicy.Budget{Mode: policy.ModeEnforce, PerRunUSD: 5, MaxRunTokens: 100}
	none := &projectpolicy.Policy{Budget: budget}
	cascade := func(c projectpolicy.Credentials) *projectpolicy.Policy {
		return &projectpolicy.Policy{Budget: budget, Credentials: &c}
	}
	grades := func(g config.ModelGrades) *projectpolicy.Policy {
		return &projectpolicy.Policy{Budget: budget, ModelGrades: &g}
	}
	grade := func(model, access string) *config.Craft {
		return &config.Craft{Model: model, Access: access}
	}
	yes, four, eight := true, 4, 8
	for _, tc := range []struct {
		name     string
		old, new *projectpolicy.Policy
		want     []string
	}{
		{
			name: "a cascade member the node does not carry",
			old:  none,
			new:  cascade(projectpolicy.Credentials{Fallback: projectpolicy.FallbackAuto}),
			want: []string{"credentials fallback"},
		},
		{
			name: "a cascade member whose value moves",
			old:  cascade(projectpolicy.Credentials{WaitThreshold: "30s"}),
			new:  cascade(projectpolicy.Credentials{WaitThreshold: "45s"}),
			want: []string{"credentials wait_threshold"},
		},
		{
			name: "a backoff member",
			old:  cascade(projectpolicy.Credentials{Backoff: &projectpolicy.Backoff{Initial: "1s", Cap: "30s"}}),
			new:  cascade(projectpolicy.Credentials{Backoff: &projectpolicy.Backoff{Initial: "2s", Cap: "30s"}}),
			want: []string{"credentials backoff.initial"},
		},
		{
			name: "D142's controls",
			old: cascade(projectpolicy.Credentials{
				StopAfterExhaustionPerWindow: &yes, ConcurrencyPerToken: &four,
			}),
			new: cascade(projectpolicy.Credentials{
				StopAfterExhaustionPerWindow: &yes, ConcurrencyPerToken: &eight,
			}),
			want: []string{"credentials concurrency_per_token"},
		},
		{
			name: "a grade's access the node does not carry",
			old:  none,
			new:  grades(config.ModelGrades{Strong: grade("claude-opus-5-5", "acme-pool")}),
			want: []string{"model_grades strong.access"},
		},
		{
			name: "a grade's access that moves",
			old:  grades(config.ModelGrades{Strong: grade("claude-opus-5-5", "acme-main")}),
			new:  grades(config.ModelGrades{Strong: grade("claude-opus-5-5", "acme-pool")}),
			want: []string{"model_grades strong.access"},
		},
		{
			name: "a member the publish removes is no grant",
			old:  cascade(projectpolicy.Credentials{Fallback: projectpolicy.FallbackAuto, WaitThreshold: "45s"}),
			new:  cascade(projectpolicy.Credentials{Fallback: projectpolicy.FallbackAuto}),
		},
		{
			name: "a grade's model is not a grant",
			old:  grades(config.ModelGrades{Strong: grade("claude-sonnet-5", "acme-main")}),
			new:  grades(config.ModelGrades{Strong: grade("claude-opus-5-5", "acme-main")}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &policyMigration{node: tc.old, pub: &policyPublish{pol: tc.new}}
			var got []string
			for _, c := range m.loosening() {
				got = append(got, c.Member)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("loosening() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCeilingRaisedWithAGrantKeepsItsTypedName: the waiver a grant gets is the
// grant and nothing else (D1542, D1552, F24). A publish that raises the
// project's ceiling in the same breath keeps the typed name D846 reserves for
// it, and --yes does not stand in: the widening the name guards is a cap every
// run of the project is admitted by, and the grant beside it is what would
// otherwise let the pair through on the grant's own exemption.
//
// The grant alone is TestUpdatePublishesAnOwnedLocalPoolAskingNothing's case,
// and the two together are what pins the condition: widen the waiver to cover a
// ceiling and this test fails, because the node moves.
func TestCeilingRaisedWithAGrantKeepsItsTypedName(t *testing.T) {
	f := looserPublishFixture(t)
	setConfig(t, f, func(s string) string {
		return s + "pools:\n  - { name: acme-pool, members: [vertex], owner: owner@example.com }\n"
	})
	noTerminal(t)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("a publish that raised the ceiling with a grant went through on --yes alone:\n%s", out)
	}
	if !strings.Contains(out, "CONFIRM") {
		t.Errorf("the refusal is not the typed confirmation:\n%s", out)
	}
	if got := nodeCeiling(t, f); got != 5 {
		t.Errorf("the node's ceiling is %v, want the 5 it carried: the refusal wrote it anyway", got)
	}
	if got := publishedPolicy(t, f).CraftPools; len(got) != 0 {
		t.Errorf("craft_pools = %+v, want the grant unpublished beside the refused ceiling", got)
	}

	// And the name the door asks for is the whole of it: typed, the same
	// publish writes both the ceiling and the grant.
	typedOut, err := typedRig(t, f).apply(t, false)
	if err != nil {
		t.Fatalf("the typed confirmation was not accepted: %v\n%s", err, typedOut)
	}
	if got := nodeCeiling(t, f); got != 7 {
		t.Errorf("the node's ceiling is %v after the typed confirmation, want the file's 7", got)
	}
	if got := publishedPolicy(t, f).CraftPools; len(got) != 1 || got[0].Name != "acme-pool" {
		t.Errorf("craft_pools = %+v, want the grant the same publish carries", got)
	}
}
