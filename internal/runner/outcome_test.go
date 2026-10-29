package runner

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The mode's thresholds decide which findings block, inside Decide
// (docs/rigor-modes/implementation-plan.md T3.1; reconciliation.md D4, D7,
// D9). These tests hold the one change in the plan that can make a pull
// request ready that was not ready before, in both directions: what a mode
// tolerates, and that Critical — and every run whose filing cannot carry a
// tolerated finding — tolerates nothing at all.

// optedIn is a repository that configured a rigor: block, which is D9's first
// condition for filing a tolerated finding and therefore for tolerating one.
var optedIn = rigor.Config{RepoBlock: true}

// The scores the table reads, as the reviewer writes them: three anchored
// axes and a class (D2). Risk is likelihood × impact, which is the axis every
// mode's floor is stated against.
var (
	// risk 3: below Startup's floor and below Pro's, so both tolerate it on
	// the risk axis alone. Its fix cost is 1, so it never reaches Pro's escape
	// — that case is rareExpensive, not this one.
	tiny = rigor.Score{Likelihood: 1, Impact: 3, FixCost: 1, Class: rigor.Correctness}
	// risk 4, which Pro blocks at, and the one finding Pro's escape is for:
	// likelihood 1 and fix cost 4 are the edges that sentence names, "at most
	// 1" and "4 or more" (table.go).
	rareExpensive = rigor.Score{Likelihood: 1, Impact: 4, FixCost: 4, Class: rigor.Correctness}
	// risk 4: Pro's floor itself, at a likelihood the escape does not reach.
	small = rigor.Score{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness}
	// risk 9: above every on-axis floor.
	big = rigor.Score{Likelihood: 3, Impact: 3, FixCost: 2, Class: rigor.Correctness}
	// risk 1 with an ordinary class: the same axes as catastrophic below, so
	// the pair of rows says the override reads the class and not the numbers.
	// It is the mildest finding the scale carries, which is what Hotfix's rows
	// use — Hotfix tolerates the set POC does.
	benign = rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}
	// the spec's severity override: risk 1, and catastrophic by its class.
	catastrophic = rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Security}
	// a finding the reviewer left unscored, which is the zero Score (D3, D4).
	unscored = rigor.Score{}
)

// TestDecideByMode is the headline test: for each mode, what a round whose
// findings it tolerates decides, and what one it does not decides. The
// Critical rows are today's behaviour, held here so a later edit that moves
// them is caught rather than argued about (implementation-plan §5).
func TestDecideByMode(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	fail := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true}
	dirty := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", Passed: true}
	cases := []struct {
		name     string
		records  []verify.Record
		mode     rigor.Mode
		round    int
		verdict  string
		findings []rigor.Score
		ready    bool
		reason   string
	}{
		// Critical tolerates nothing: every row here is Decide's answer
		// before modes existed, reason string included.
		{"critical blocks what every other mode tolerates", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{small, small, small}, false, "review round 2 still has 3 findings"},
		{"critical blocks pro's rare expensive escape", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{rareExpensive}, false, "review round 2 still has 1 finding"},
		{"critical blocks an unscored finding", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{unscored}, false, "review round 2 still has 1 finding"},
		{"critical with nothing left is still a draft", []verify.Record{pass}, rigor.Critical, 2, "changes", nil, false, "review round 2 still has 0 findings"},

		{"startup tolerates below its floor", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{small}, true, "review round 2 left 1 finding, tolerated at startup"},
		{"startup tolerates a whole round", []verify.Record{pass}, rigor.Startup, 3, "changes", []rigor.Score{small, tiny, rareExpensive}, true, "review round 3 left 3 findings, tolerated at startup"},
		{"startup blocks at its floor", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{big}, false, "review round 2 still has 1 finding"},
		{"startup blocks the one finding it does not tolerate", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{small, big}, false, "review round 2 still has 2 findings"},
		{"startup blocks the severity override", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{catastrophic}, false, "review round 2 still has 1 finding"},

		{"pro tolerates below its floor", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{tiny}, true, "review round 2 left 1 finding, tolerated at pro"},
		{"pro tolerates the rare expensive case", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{rareExpensive}, true, "review round 2 left 1 finding, tolerated at pro"},
		{"pro blocks at its floor", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{small}, false, "review round 2 still has 1 finding"},

		// Hotfix's row is empty like POC's — "only findings that make the fix
		// wrong or unsafe block" is the same set once catastrophic is read the
		// way the spec reads it — so a benign finding is tolerated there too.
		// Its row is the only one that is both Tolerated and Deferred (D30),
		// which is why the run-level test beside this one is about Hotfix: the
		// tolerated round makes the pull request ready *and* is the second item
		// the run files.
		{"hotfix tolerates a benign finding", []verify.Record{pass}, rigor.Hotfix, 2, "changes", []rigor.Score{benign}, true, "review round 2 left 1 finding, tolerated at hotfix"},
		{"hotfix blocks the severity override", []verify.Record{pass}, rigor.Hotfix, 2, "changes", []rigor.Score{catastrophic}, false, "review round 2 still has 1 finding"},

		// POC files nothing — its account is its pull request body's own
		// line (D30) — so D9 leaves it nothing to tolerate.
		{"poc files nothing so it tolerates nothing", []verify.Record{pass}, rigor.POC, 2, "changes", []rigor.Score{small}, false, "review round 2 still has 1 finding"},
		// A mode the table does not hold blocks on everything: the zero Mode
		// a record written before the mode existed carries, and a name that
		// arrived unparsed (F3).
		{"a mode the table does not know blocks on everything", []verify.Record{pass}, rigor.Mode("wild"), 2, "changes", []rigor.Score{tiny}, false, "review round 2 still has 1 finding"},

		// The gates Decide already had outrank the mode: a tolerated round
		// is still a draft when the run did not verify it.
		{"a failing test outranks the mode", []verify.Record{fail}, rigor.Startup, 2, "changes", []rigor.Score{small}, false, "tests failing on the final commit"},
		{"no clean test outranks the mode", []verify.Record{dirty}, rigor.Startup, 2, "changes", []rigor.Score{small}, false, ReasonNoVerifiedTest},
		{"no verdict outranks the mode", []verify.Record{pass}, rigor.Startup, 2, "", []rigor.Score{small}, false, "no review verdict"},
		{"a shipped round is ready whatever the mode", []verify.Record{pass}, rigor.Critical, 1, "ship", nil, true, ""},
	}
	for _, tc := range cases {
		var last *runstore.ReviewSummary
		if tc.verdict != "" {
			last = &runstore.ReviewSummary{Round: tc.round, Verdict: tc.verdict, Findings: len(tc.findings)}
		}
		ready, reason := Decide(tc.records, "abc", last, findingsPolicyOf(optedIn, tc.mode, tc.round, tc.findings))
		if ready != tc.ready || reason != tc.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", tc.name, ready, reason, tc.ready, tc.reason)
		}
	}
}

// TestDecideUnscoredFindingBlocksEveryMode is D4: a reviewer that stops
// emitting scores must not turn a strict mode into a lenient one, so a
// finding the table cannot read blocks wherever it appears — including beside
// one the same mode tolerates once it is scored.
func TestDecideUnscoredFindingBlocksEveryMode(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 2}
	for _, mode := range rigor.Modes {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, findingsPolicyOf(optedIn, mode, 2, []rigor.Score{unscored, small}))
		if ready || reason != "review round 2 still has 2 findings" {
			t.Errorf("%s: an unscored finding was tolerated: Decide = %v %q", mode, ready, reason)
		}
	}
}

// TestDecideToleratesOnlyWhatTheRunCanFile is D9: a mode may not tolerate a
// finding it cannot file, so tolerance is taken only for a repository that
// opted in and a mode whose rigor.Files row files what it tolerates. Every
// other run decides by the count on the record, exactly as it did before
// modes existed.
func TestDecideToleratesOnlyWhatTheRunCanFile(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 1}
	for _, tc := range []struct {
		name      string
		cfg       rigor.Config
		mode      rigor.Mode
		tolerated bool
	}{
		{"a repository that configured a mode", optedIn, rigor.Startup, true},
		{"a repository that configured pro", optedIn, rigor.Pro, true},
		{"a repository that configured hotfix", optedIn, rigor.Hotfix, true},
		{"a project layer that set a rigor default", rigor.Config{Project: rigor.Pro}, rigor.Pro, true},
		{"a repository that configured poc", optedIn, rigor.POC, false},
		{"a repository that configured critical", optedIn, rigor.Critical, false},
		{"a repository that configured nothing", rigor.Config{}, rigor.Pro, false},
	} {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, findingsPolicyOf(tc.cfg, tc.mode, 2, []rigor.Score{tiny}))
		if ready != tc.tolerated {
			t.Errorf("%s: Decide = %v %q, want tolerated %v", tc.name, ready, reason, tc.tolerated)
			continue
		}
		want := "review round 2 still has 1 finding"
		if tc.tolerated {
			want = "review round 2 left 1 finding, tolerated at " + string(tc.mode)
		}
		if reason != want {
			t.Errorf("%s: reason = %q, want %q", tc.name, reason, want)
		}
	}
}

// TestDecideBlocksAPolicyItCannotPlace: a split that is not the round the
// record counts is a policy that cannot decide, and one that cannot decide
// blocks (F3, D4) rather than tolerating a round nothing measured.
func TestDecideBlocksAPolicyItCannotPlace(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 1}
	for _, tc := range []struct {
		name string
		pol  FindingsPolicy
	}{
		{"a split from another round", findingsPolicyOf(optedIn, rigor.Startup, 1, []rigor.Score{tiny})},
		{"a split from fewer findings than the record counts", findingsPolicyOf(optedIn, rigor.Startup, 2, nil)},
	} {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, tc.pol)
		if ready || reason != "review round 2 still has 1 finding" {
			t.Errorf("%s: Decide = %v %q, want false with the count's own reason", tc.name, ready, reason)
		}
	}
}

// TestToleratedRecordKeepsWhatTheModeLetThrough is T3.4 at the runner: the
// record keeps the findings the mode let through — each with the reviewer's
// own words for it, the word for its score and the mode that tolerated it —
// and nothing at all when the mode tolerated none (D9, F19). The blocking
// finding of the same round is not in it, and the titles are the tolerated
// findings' own, which is what pairs the split back with the round.
func TestToleratedRecordKeepsWhatTheModeLetThrough(t *testing.T) {
	findings := []Finding{
		{Severity: "minor", Summary: "the retry loop has no backoff", Likelihood: 1, Impact: 3, FixCost: 2, Class: "correctness"},
		{Severity: "major", Summary: "the handler ignores the error", Likelihood: 4, Impact: 4, FixCost: 2, Class: "correctness"},
		{Severity: "minor", Summary: "a long comment restates the function", Likelihood: 2, Impact: 2, FixCost: 1, Class: "cosmetic"},
	}
	r := &run{lastSenior: seniorRound{Round: 2, Findings: findings}}
	pol := findingsPolicyOf(optedIn, rigor.Startup, 2, scoresOf(findings))
	got := r.toleratedRecord(pol)
	want := []runstore.ToleratedFinding{
		{Title: "the retry loop has no backoff", Label: rigor.Nit, Mode: rigor.Startup, Likelihood: 1, Impact: 3, FixCost: 2, Class: rigor.Correctness},
		{Title: "a long comment restates the function", Label: rigor.Low, Mode: rigor.Startup, Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Cosmetic},
	}
	if got == nil || !reflect.DeepEqual(got.Findings, want) {
		t.Fatalf("toleratedRecord = %+v, want %+v", got, want)
	}
	if got.Total != 2 {
		t.Errorf("total = %d, want the 2 findings the mode tolerated", got.Total)
	}

	// A reviewer's words are agent text on a record the repository can read,
	// so what is stored is redacted like everything else the run publishes.
	const secret = "super-secret-token"
	r.addSecret(secret)
	r.lastSenior.Findings = []Finding{{Summary: "the token " + secret + " is logged", Likelihood: 1, Impact: 1, FixCost: 1, Class: "correctness"}}
	got = r.toleratedRecord(findingsPolicyOf(optedIn, rigor.Startup, 2, scoresOf(r.lastSenior.Findings)))
	if got == nil || strings.Contains(got.Findings[0].Title, secret) {
		t.Fatalf("toleratedRecord left a credential in the record: %+v", got)
	}

	// A mode that tolerates nothing — and every run whose filing cannot carry
	// a tolerated finding — writes no block at all (D9).
	for _, tc := range []struct {
		name string
		cfg  rigor.Config
		mode rigor.Mode
	}{
		{"critical tolerates nothing", optedIn, rigor.Critical},
		{"poc files nothing so it tolerates nothing", optedIn, rigor.POC},
		{"a repository that configured nothing", rigor.Config{}, rigor.Pro},
	} {
		if pol := findingsPolicyOf(tc.cfg, tc.mode, 2, scoresOf(findings)); r.toleratedRecord(pol) != nil {
			t.Errorf("%s: the record kept findings the mode did not tolerate", tc.name)
		}
	}
}

// TestOutcomeReadsTheScoredFields pins the seam between the reviewer's
// finding and the table: the three anchored axes and the class are read, and
// the free-form severity word beside them is not (D2).
func TestOutcomeReadsTheScoredFields(t *testing.T) {
	got := scoresOf([]Finding{
		{Severity: "blocker", Likelihood: 2, Impact: 3, FixCost: 4, Class: "maintainability", Summary: "x"},
		{Severity: "blocker", Summary: "unscored"},
	})
	want := []rigor.Score{
		{Likelihood: 2, Impact: 3, FixCost: 4, Class: rigor.Maintainability},
		{},
	}
	if len(got) != len(want) {
		t.Fatalf("scoresOf = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scoresOf[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The raise a run's mode takes during the run decides readiness, and names
// itself in the reason (docs/rigor-modes/implementation-plan.md T3.5; D22,
// D19). A raise the run's own diff made — at D22's round moment, or at
// finalize — is a mode the review round the run ends on was not necessarily
// held at, and the reason a reader gets says which mode, which rule and which
// moment. A run whose mode never rose is unchanged, and so is a repository
// that configured no rigor at all.

// The findings these cases split on, as the reviewer writes them: risk 4 is
// below Startup's floor and blocks at Critical, and risk 9 is above every
// on-axis floor (the same two scores as `small` and `big` above).
var (
	riskThree = Finding{Likelihood: 1, Impact: 3, FixCost: 1, Class: "correctness", Summary: "the message names the raw id"}
	riskFour  = Finding{Likelihood: 2, Impact: 2, FixCost: 1, Class: "correctness", Summary: "the handler ignores the error"}
	riskNine  = Finding{Likelihood: 3, Impact: 3, FixCost: 2, Class: "correctness", Summary: "the retry loop never stops"}
)

// raisedBase is a repository whose base branch carries block as its rigor
// configuration, with the rest of a valid fugaro.yaml around it: the mode's
// configuration comes from the base branch (D25), so the block has to be in
// the remote's fugaro.yaml rather than in a layer a test hands the run.
func raisedBase(t *testing.T, block string) string {
	t.Helper()
	testutil.IsolateGit(t)
	const head = "version: 1\nproject: aurora\ngit: { provider: github }\n"
	const tail = "workflows:\n  server: { commands: { build: make, test: make test } }\n"
	return testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": head + block + tail})
}

// raisedCase is one run's inputs: the repository's rigor block, the paths the
// task declared (`fugaro run --path`), the paths the run's own work measured,
// the last senior review round's findings, and the moments after bootstrap to
// resolve the mode at, in D22's order.
type raisedCase struct {
	block    string
	declared []string
	paths    []string
	findings []Finding
	moments  []string
}

// raisedRun is one run whose mode is resolved at D22's first moment from the
// paths the case declares and then at each moment it names, from the paths its
// work measured. Its last senior review is round 2, carrying findings, which is
// the round Decide reads.
func raisedRun(t *testing.T, tc raisedCase) *run {
	t.Helper()
	remote := raisedBase(t, tc.block)
	testutil.IsolateGit(t)
	ctx := context.Background()
	repo, err := gitops.OpenOrClone(ctx, filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Git.BaseBranch = "main"
	r := &run{
		repo: repo, cfg: cfg, spec: &task.Spec{RequestedBy: "person-1", Paths: tc.declared},
		rec: &runstore.Record{
			Version: 1, RunID: "20261024-120000-abcd", Branch: "fugaro/x", BaseBranch: "main",
			Status: runstore.StatusRunning, Outcome: runstore.OutcomeNone,
			Reviews: []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: len(tc.findings)}},
		},
		d: Deps{Log: slog.New(slog.DiscardHandler)},
	}
	r.lastSenior = seniorRound{Round: 2, Findings: tc.findings}
	r.bootstrapRigor(ctx)
	paths := tc.paths
	if len(paths) == 0 {
		paths = []string{"internal/payments/20261024-120000-abcd.go"}
	}
	for _, at := range tc.moments {
		r.recordRigor(at, paths)
	}
	return r
}

// decideOf is Decide's answer for r, asked as finalize's own call site asks it
// (runner.go): the run's last senior review, a passing clean-tree test on the
// commit it ran on, and the policy the run's mode gives.
func decideOf(t *testing.T, r *run) (bool, string) {
	t.Helper()
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	return Decide([]verify.Record{pass}, "abc", seniorReview(r.rec.Reviews), r.findingsPolicy())
}

// TestRaisedModeByAreaKeepsDraft: an area rule raises the run's mode at D22's
// round moment, and the last review round carries a finding the raised mode
// does not tolerate. The pull request stays a draft, and the reason says which
// mode rose, which rule raised it and at which moment — a reader of the record
// can go and look at the rule rather than guess.
func TestRaisedModeByAreaKeepsDraft(t *testing.T) {
	r := raisedRun(t, raisedCase{
		block:    "rigor:\n  default: poc\n  areas:\n    - { name: payments, match: \"internal/payments/**\", mode: critical }\n",
		findings: []Finding{riskFour},
		moments:  []string{runstore.RigorAtRound},
	})
	if got := r.reachedRigor(); got != rigor.Critical {
		t.Fatalf("the run resolved %q; want critical, or the case proves nothing", got)
	}
	ready, reason := decideOf(t, r)
	if ready {
		t.Fatalf("a round the raised mode does not tolerate made the pull request ready: %q", reason)
	}
	const want = "review round 2 still has 1 finding, and the mode was raised to critical by area payments after a review round"
	if reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
}

// TestModeRaisedAtFinalizeLeavesDraft: the mode rises at D22's third moment,
// after every review round, so no round was held at the mode the run ends at
// — and the round the run ends on is one the raised mode tolerates, which is
// the case that would otherwise become ready (T3.5, D22's "too late to buy
// rounds"). The pull request stays a draft and the reason says why.
//
// The second half is the same raise one moment earlier: a raise the last
// review round was held at is a raise that round answered, so a round the
// raised mode tolerates still becomes ready.
func TestModeRaisedAtFinalizeLeavesDraft(t *testing.T) {
	const block = "rigor:\n  default: poc\n  areas:\n    - { name: payments, match: \"internal/payments/**\", mode: startup }\n"
	late := raisedRun(t, raisedCase{block: block, findings: []Finding{riskFour}, moments: []string{runstore.RigorAtFinalize}})
	if got := late.reachedRigor(); got != rigor.Startup {
		t.Fatalf("the run resolved %q; want startup, or the case proves nothing", got)
	}
	ready, reason := decideOf(t, late)
	if ready {
		t.Fatalf("a mode raised at finalize made the pull request ready: %q", reason)
	}
	const want = "the mode was raised to startup by area payments at finalize, after the last review round"
	if reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}

	held := raisedRun(t, raisedCase{block: block, findings: []Finding{riskFour}, moments: []string{runstore.RigorAtRound}})
	if ready, reason := decideOf(t, held); !ready {
		t.Errorf("a raise the last review round was held at left the pull request a draft: %q", reason)
	}
}

// TestRaisedModeByLayerAfterARoundStaysReady: the moment the mode rose decides,
// not the shape of the block that records it. Areas are defaults and not floors
// (docs/project-layer.md §2), so a declared path can start a run *below* the
// repository's own mode; a later moment whose paths match no area then resolves
// that mode, which is a raise the block records with no rule of its own and an
// `at` of round. The round that follows such a raise was held at the raised
// mode, so a round it tolerates is ready — the run is not a draft, and the
// reason does not claim the mode rose at finalize, which is the moment the
// record itself contradicts.
func TestRaisedModeByLayerAfterARoundStaysReady(t *testing.T) {
	base := raisedCase{
		block:    "rigor:\n  default: pro\n  areas:\n    - { name: docs, match: \"docs/**\", mode: poc }\n",
		declared: []string{"docs/readme.md"},
		paths:    []string{"internal/foo.go"},
		moments:  []string{runstore.RigorAtRound, runstore.RigorAtFinalize},
	}

	tolerated := base
	tolerated.findings = []Finding{riskThree}
	r := raisedRun(t, tolerated)
	if got := r.reachedRigor(); got != rigor.Pro {
		t.Fatalf("the run resolved %q; want pro, or the case proves nothing", got)
	}
	if got := r.rec.Rigor.At; got != runstore.RigorAtRound {
		t.Fatalf("the block was changed at %q; want %q, or the case proves nothing", got, runstore.RigorAtRound)
	}
	ready, reason := decideOf(t, r)
	if !ready || reason != "review round 2 left 1 finding, tolerated at pro" {
		t.Errorf("a raise between rounds = %v %q, want the tolerated round the raise bought rounds for", ready, reason)
	}

	blocking := base
	blocking.findings = []Finding{riskNine}
	ready, reason = decideOf(t, raisedRun(t, blocking))
	if ready {
		t.Fatalf("a round the raised mode does not tolerate made the pull request ready: %q", reason)
	}
	const want = "review round 2 still has 1 finding, and the mode was raised to pro after a review round"
	if reason != want {
		t.Errorf("reason = %q, want %q: a raise no rule made names no rule, and its own moment", reason, want)
	}
}

// TestModeNeverRaisedDecideUnchanged is D19 for T3.5: a run whose mode never
// rose decides exactly as it did before the raise acted — the count's own
// reason when the mode blocks, and T3.1's tolerated reason when it does not.
// Both cases resolve their mode at finalize, as every run does.
func TestModeNeverRaisedDecideUnchanged(t *testing.T) {
	const block = "rigor:\n  default: startup\n"
	blocking := raisedRun(t, raisedCase{block: block, findings: []Finding{riskNine}, moments: []string{runstore.RigorAtFinalize}})
	ready, reason := decideOf(t, blocking)
	if ready || reason != "review round 2 still has 1 finding" {
		t.Errorf("a run whose mode never rose = %v %q, want false with the count's own reason", ready, reason)
	}

	tolerated := raisedRun(t, raisedCase{block: block, findings: []Finding{riskFour}, moments: []string{runstore.RigorAtFinalize}})
	ready, reason = decideOf(t, tolerated)
	if !ready || reason != "review round 2 left 1 finding, tolerated at startup" {
		t.Errorf("a tolerated round at a mode that never rose = %v %q, want T3.1's own answer", ready, reason)
	}
}

// TestUnconfiguredRepoDecideUnchanged is the owner's ruling on T3.1, held for
// T3.5: a repository that configured no rigor resolves Pro for its budget
// (D19) but files nothing, so it tolerates nothing and decides by the count —
// today's behaviour, whatever its diff matched.
func TestUnconfiguredRepoDecideUnchanged(t *testing.T) {
	r := raisedRun(t, raisedCase{findings: []Finding{riskFour}, moments: []string{runstore.RigorAtFinalize}})
	if got := r.reachedRigor(); got != rigor.Default {
		t.Fatalf("the run resolved %q; want the default, or the case proves nothing", got)
	}
	ready, reason := decideOf(t, r)
	if ready || reason != "review round 2 still has 1 finding" {
		t.Errorf("an unconfigured repository = %v %q, want false with the count's own reason", ready, reason)
	}
}
