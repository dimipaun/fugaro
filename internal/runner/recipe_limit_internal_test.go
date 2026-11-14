package runner

import (
	"strconv"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// opusRow is the price row the report run's review is judged against here: the
// reviewer model Check 136's run reserved against, and the one whose own
// maximum output (128k at $20/M) is what a report run's review cannot reserve.
const opusRow = "claude-opus-5-5"

// runCapUSD is the per-run cap of the sandbox repository that run halted on.
const runCapUSD = 2.0

// TestRunDigestReviewOutputBound: the run-digest recipe bounds its review's
// per-call output, and the bound is the reviewer's alone: the recipe has no
// coder stage at all, and a bound on the coder's slot would be a value no
// stage of a report run ever reads.
func TestRunDigestReviewOutputBound(t *testing.T) {
	a := config.Agent{}
	if capped := applyRecipeOutputLimit(&a, catalogRecipe(t, "run-digest")); capped {
		t.Error("an agent that wrote no limit of its own was reported as capped")
	}
	if a.MaxOutputTokens.Reviewer != runDigestReviewerMaxOutput {
		t.Errorf("reviewer bound = %d, want the recipe's %d", a.MaxOutputTokens.Reviewer, runDigestReviewerMaxOutput)
	}
	if a.MaxOutputTokens.Coder != 0 {
		t.Errorf("coder bound = %d, want the agent's own (none)", a.MaxOutputTokens.Coder)
	}
}

// TestRunDigestReviewOutputBoundOnlyTightens: the recipe caps a larger bound
// and leaves a smaller one alone, so a repository or project that reserved
// less than the recipe's bound for its reviewer keeps its own figure.
func TestRunDigestReviewOutputBoundOnlyTightens(t *testing.T) {
	for _, tc := range []struct {
		written int64
		want    int64
		capped  bool
	}{
		{0, runDigestReviewerMaxOutput, false},
		{16000, 16000, false},
		{runDigestReviewerMaxOutput, runDigestReviewerMaxOutput, false},
		{runDigestReviewerMaxOutput + 1, runDigestReviewerMaxOutput, true},
		{128000, runDigestReviewerMaxOutput, true},
	} {
		t.Run(strconv.FormatInt(tc.written, 10), func(t *testing.T) {
			a := config.Agent{MaxOutputTokens: config.RoleTokens{Coder: 8192, Reviewer: tc.written}}
			capped := applyRecipeOutputLimit(&a, catalogRecipe(t, "run-digest"))
			if a.MaxOutputTokens.Reviewer != tc.want {
				t.Errorf("reviewer bound written as %d = %d, want %d", tc.written, a.MaxOutputTokens.Reviewer, tc.want)
			}
			if capped != tc.capped {
				t.Errorf("reviewer bound written as %d reported capped = %v, want %v", tc.written, capped, tc.capped)
			}
			if a.MaxOutputTokens.Coder != 8192 {
				t.Errorf("reviewer bound written as %d moved the coder's to %d", tc.written, a.MaxOutputTokens.Coder)
			}
		})
	}
}

// TestReviewOutputBoundIsRunDigestOnly: no other recipe of the catalog carries
// the bound, however large the repository's own reviewer slot is, and a
// repository recipe that reuses no name of its own is not judged by it either.
func TestReviewOutputBoundIsRunDigestOnly(t *testing.T) {
	for _, name := range recipe.CatalogNames() {
		if name == "run-digest" {
			continue
		}
		a := config.Agent{MaxOutputTokens: config.RoleTokens{Reviewer: config.MaxOutputTokensLimit}}
		if capped := applyRecipeOutputLimit(&a, catalogRecipe(t, name)); capped {
			t.Errorf("recipe %s capped the reviewer's bound", name)
		}
		if a.MaxOutputTokens.Reviewer != config.MaxOutputTokensLimit {
			t.Errorf("recipe %s moved the reviewer's bound to %d", name, a.MaxOutputTokens.Reviewer)
		}
	}
	a := config.Agent{}
	if applyRecipeOutputLimit(&a, &recipe.Recipe{Name: "my-own"}) {
		t.Error("a recipe of another name capped the reviewer's bound")
	}
	if a.MaxOutputTokens.Reviewer != 0 {
		t.Errorf("a recipe of another name set the reviewer's bound to %d", a.MaxOutputTokens.Reviewer)
	}
}

// TestRunDigestReviewReservationFitsASmallRunCap: with the bound in place the
// review's worst-case reservation is about a third of the $2.00 per-run cap
// Check 136's sandbox repository holds, and the model's own maximum — what the
// reservation was before the bound — is over that cap, which is why the run
// halted with run_cap having spent nothing.
func TestRunDigestReviewReservationFitsASmallRunCap(t *testing.T) {
	a := config.Agent{}
	applyRecipeOutputLimit(&a, catalogRecipe(t, "run-digest"))
	m, ok := pricing.Embedded().Lookup(opusRow)
	if !ok {
		t.Fatalf("the price table holds no %s", opusRow)
	}
	// A review request's body, small enough that what the bound buys is the
	// output half: WorstCase prices the body's bytes plus its 4096-token
	// overhead at the input rate and the reserved output at the output rate.
	const body = 16 << 10
	bound := m.WorstCase(pricing.Request{BodyBytes: body, MaxTokens: a.MaxOutputTokens.Reviewer})
	if bound.USD() >= 1 {
		t.Errorf("the review's worst case at the recipe's bound is $%.2f, want under $1.00", bound.USD())
	}
	unbounded := m.WorstCase(pricing.Request{BodyBytes: body, MaxTokens: m.MaxOutputTokens})
	if unbounded.USD() <= runCapUSD {
		t.Fatalf("the model's own maximum reserves $%.2f, which is not over the $%.2f cap a report run halted on",
			unbounded.USD(), runCapUSD)
	}
}
