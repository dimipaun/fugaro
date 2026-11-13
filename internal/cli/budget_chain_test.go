package cli

// fugaro budget chain raise: raising a chain that has spent its limit
// (docs/run-limit-carry-over L9, D1061, F22, spec §4). A chain's limit is a
// launcher's number held in the stored task of the run that began the chain
// (D1041, D1046), so the raise is a rewrite of that stored task — never of a
// mid-chain run's, whose figures are what its own launch was left with — with
// the reason recorded in the root run's record beside the policy it ran under.
//
// Every read here is a fake: a file:// bucket and no budget database at all,
// because a raise writes the runs bucket and touches nothing else.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// chainRaiseRig is the three-run chain of runs show's own fixture, on a
// fixture with no budget backend: raising a chain reads the root's stored task
// and its record, and nothing of the project's budget — the per-run cap and the
// daily and project caps are the database's rules and a raise moves none of
// them (spec §4).
func chainRaiseRig(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	seedChain(t, f)
	return f
}

// chainTaskBytes is a run's task.json exactly as the bucket holds it: a
// refused raise has to leave the bytes alone, which is not the same claim as
// leaving the parsed figures alone.
func chainTaskBytes(t *testing.T, f *cloudFixture, id string) string {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, err := runstore.Open(b, appSlug, id).ReadFile(ctx, "task.json")
	if err != nil {
		t.Fatalf("task.json of %s: %v", id, err)
	}
	return string(data)
}

// chainRaise runs the command for the chain's tip and returns its output.
func chainRaise(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"budget", "chain", "raise", showTip, "--repo", "acme/app"}, extra...)
	out, errOut, err := execute(t, args...)
	if err != nil {
		return out, fmt.Errorf("%w (%s)", err, strings.TrimSpace(errOut))
	}
	return out, nil
}

func TestChainRaiseRewritesTheRootTask(t *testing.T) {
	f := chainRaiseRig(t)
	out, err := chainRaise(t, "--max-usd", "6", "--max-time", "90m", "--reason", "the senior review round needs one more pass")
	if err != nil {
		t.Fatalf("budget chain raise: %v", err)
	}
	// The command says what it moved and where the limit is kept: the run the
	// caller named, and the run that began the chain, each named by its ending
	// (spec.md §1) — a person reads runs that way, and `runs show` is where
	// the whole ID comes from.
	for _, want := range []string{task.ShortRun(showRoot), task.ShortRun(showTip), "4 dollars -> 6 dollars", "45m0s -> 1h30m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("the raise's output does not name %q: %s", want, out)
		}
	}

	// The figures land in the stored task of the run that began the chain —
	// the one place a chain's limit lives (D1046).
	spec := chainTask(t, f, showRoot)
	if usd, ok := chainLimitAmount(spec.Overrides, runstore.USD); !ok || usd != 6_000_000 {
		t.Errorf("the root run's --max-usd = %v (%v), want 6 dollars", usd, ok)
	}
	if got, ok := chainLimitAmount(spec.Overrides, runstore.Time); !ok || got != 5400 {
		t.Errorf("the root run's --max-time = %v (%v), want 1h30m0s", got, ok)
	}
	// The dimensions the caller did not name keep the figures the launch that
	// began the chain wrote: a raise moves what it is asked to move.
	if got, ok := chainLimitAmount(spec.Overrides, runstore.Tokens); !ok || got != 800_000 {
		t.Errorf("the root run's --max-tokens = %v (%v), want the 800000 it was launched with", got, ok)
	}
	if got, ok := chainLimitAmount(spec.Overrides, runstore.ReviewRounds); !ok || got != 4 {
		t.Errorf("the root run's --max-review-rounds = %v (%v), want the 4 it was launched with", got, ok)
	}

	// The runs after the root keep their own tasks. Their overrides are what
	// their own launches were left with and never the chain's whole limit, so a
	// raise that wrote one of them would bound the next launch by a figure the
	// chain's own launch never set.
	for _, id := range []string{showMid, showTip} {
		o := chainTask(t, f, id).Overrides
		if o.MaxUSD != nil || o.MaxTokens != nil || o.MaxTime != "" || o.MaxReviewRounds != nil {
			t.Errorf("run %s's stored task was rewritten by the raise: %+v", id, o)
		}
	}
}

func TestChainRaiseRecordsTheReasonAndTheFigures(t *testing.T) {
	f := chainRaiseRig(t)
	before := time.Now().Add(-time.Minute)
	const reason = "the reviewer asked for one more round on the same branch"
	if _, err := chainRaise(t, "--max-tokens", "1200000", "--max-review-rounds", "6", "--reason", reason); err != nil {
		t.Fatalf("budget chain raise: %v", err)
	}
	rec := showRecord(t, f, showRoot)
	if len(rec.ChainRaises) != 1 {
		t.Fatalf("the root run's record carries %d raises, want 1: %+v", len(rec.ChainRaises), rec.ChainRaises)
	}
	e := rec.ChainRaises[0]
	// Who, when and why (D1061): the launcher's own account, the moment, and
	// the caller's reason, which is the whole of what tells a raise from a
	// rewritten limit.
	if e.By != "someone@example.com" {
		t.Errorf("the raise's by = %q, want the launcher's own account", e.By)
	}
	if e.At.Before(before) || e.At.After(time.Now()) {
		t.Errorf("the raise's at = %v, want the moment it was made", e.At)
	}
	if e.Reason != reason {
		t.Errorf("the raise's reason = %q, want %q", e.Reason, reason)
	}
	want := []runstore.ChainRaiseLimit{
		{Dimension: runstore.Tokens, From: 800_000, To: 1_200_000},
		{Dimension: runstore.ReviewRounds, From: 4, To: 6},
	}
	if !reflect.DeepEqual(e.Limits, want) {
		t.Errorf("the raise's figures = %+v, want %+v (in the order every message about them reads, each in the dimension's own unit)", e.Limits, want)
	}

	// The entry rides the run that began the chain and no other: the chain's
	// limit is that run's, and its record is the chain's own.
	if tip := showRecord(t, f, showTip); len(tip.ChainRaises) != 0 {
		t.Errorf("the tip run's record carries %+v, want none", tip.ChainRaises)
	}

	// The runner's own figures survive the write: the raise adds an entry to
	// the record it read, and rewrites nothing else in it.
	if rec.CostUSD != 1.5 || rec.Status != runstore.StatusFailed || rec.Branch != "fugaro/"+showRoot {
		t.Errorf("the raise rewrote the record around its entry: %+v", rec)
	}

	// A second raise is a second entry, with the figure the first one left:
	// the record is the log of every raise, not one slot for the last one.
	if _, err := chainRaise(t, "--max-tokens", "1500000", "--reason", "and the token figure with it"); err != nil {
		t.Fatalf("the second raise: %v", err)
	}
	rec = showRecord(t, f, showRoot)
	if len(rec.ChainRaises) != 2 {
		t.Fatalf("after a second raise the record carries %d entries, want 2", len(rec.ChainRaises))
	}
	if got := rec.ChainRaises[1].Limits; len(got) != 1 || got[0].From != 1_200_000 || got[0].To != 1_500_000 {
		t.Errorf("the second raise's figures = %+v, want 1200000 -> 1500000: the figure a raise is above is the one in force", got)
	}
}

func TestChainRaiseRefusesAnEmptyReason(t *testing.T) {
	f := chainRaiseRig(t)
	before := chainTaskBytes(t, f, showRoot)
	// A reason is what the raise is recorded with (D1061). Nothing stands in
	// for it: not a flag, not a prompt, and not an empty string that reads as
	// one at a glance.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no --reason at all", []string{"--max-usd", "6"}},
		{"an empty --reason", []string{"--max-usd", "6", "--reason", ""}},
		{"a --reason of spaces", []string{"--max-usd", "6", "--reason", "   "}},
	} {
		_, err := chainRaise(t, tc.args...)
		if err == nil {
			t.Errorf("%s: the raise was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "--reason") {
			t.Errorf("%s: the refusal does not name the flag: %v", tc.name, err)
		}
	}
	if after := chainTaskBytes(t, f, showRoot); after != before {
		t.Errorf("a refused raise rewrote the stored task:\n%s", after)
	}
}

func TestChainRaiseLeavesTheTaskUntouchedOnFailure(t *testing.T) {
	f := chainRaiseRig(t)
	// The run that began the chain is still going: it writes its own
	// result.json from its own snapshot, so an entry recorded in it now is
	// gone at that run's next write — and the raise is refused rather than
	// recorded into a file a run is holding.
	rec := showRecord(t, f, showRoot)
	rec.FinishedAt = nil
	writeRecord(t, f, showRoot, rec)
	before := chainTaskBytes(t, f, showRoot)
	_, err := chainRaise(t, "--max-usd", "6", "--reason", "one more stage")
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("a raise of a chain whose root run is in flight: err = %v, want it refused", err)
	}
	if after := chainTaskBytes(t, f, showRoot); after != before {
		t.Errorf("the refused raise rewrote the stored task:\n%s", after)
	}

	// A figure at or below the one in force is not a raise: this command moves
	// a limit up, and asking for less is what a launch's own --max-* is for.
	finished := showStarted.Add(showElapsed)
	rec.FinishedAt = &finished
	writeRecord(t, f, showRoot, rec)
	if _, err := chainRaise(t, "--max-usd", "4", "--reason", "the same figure"); err == nil {
		t.Error("a raise to the figure already in force was accepted")
	} else if !strings.Contains(err.Error(), "not above") {
		t.Errorf("the refusal does not say the figure is not above the one in force: %v", err)
	}
	if after := chainTaskBytes(t, f, showRoot); after != before {
		t.Errorf("the refused raise rewrote the stored task:\n%s", after)
	}

	// A dimension the chain does not limit has no figure to raise: a chain's
	// limit is what the launch that began it asked for (D1041).
	other := newCloudFixture(t)
	usd := 4.0
	seedShowRun(t, other, showRoot, "", task.Overrides{MaxUSD: &usd}, 1.5, 1)
	seedShowRun(t, other, showMid, showRoot, task.Overrides{}, 0.5, 1)
	before = chainTaskBytes(t, other, showRoot)
	if _, _, err := execute(t, "budget", "chain", "raise", showMid, "--repo", "acme/app", "--max-tokens", "1000000", "--reason", "and a token limit with it"); err == nil {
		t.Error("a raise of a dimension the chain does not limit was accepted")
	} else if !strings.Contains(err.Error(), "bounds no token spend") {
		t.Errorf("the refusal does not say the chain limits no such dimension: %v", err)
	}
	if after := chainTaskBytes(t, other, showRoot); after != before {
		t.Errorf("the refused raise rewrote the stored task:\n%s", after)
	}
}

// TestRunRefusesARaiseShapedFlag is the other half of L9's shape: a launcher
// raises a chain through the stored task and nowhere else. `fugaro run` has no
// flag that raises a limit — its own --max-* may only tighten the ceiling the
// launch resolves (D1043) — and a flag that looks like one is refused as an
// unknown flag rather than quietly meaning something else.
func TestRunRefusesARaiseShapedFlag(t *testing.T) {
	newCloudFixture(t)
	for _, flag := range []string{"--raise", "--raise-chain", "--raise-usd", "--chain-raise", "--reason"} {
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261113-100000-abcd", flag, "9", "A task")
		if err == nil {
			t.Errorf("fugaro run %s was accepted, and a launch cannot raise a chain", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("fugaro run %s: err = %v, want it refused as an unknown flag", flag, err)
		}
	}

	// And the launch's own flag list carries no raise: the figures it writes
	// are the chain's limit at its start, and a figure above one in force is
	// what the raise command is.
	cmd, _, err := NewRootCmd().Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"raise", "raise-chain", "raise-usd", "chain-raise", "reason"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("`fugaro run` carries --%s: a launch states a chain's limit and never raises one", name)
		}
	}
}
