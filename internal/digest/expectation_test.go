package digest

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestScopeDriftFlagOnly (task-governance T8.3, D332, G8): the scope a task
// expected against the paths the run really changed, as a report line and
// nothing else. A run whose changed paths are all inside the scope it declared
// raises nothing; one path outside it raises the flag; and a run that declared
// no scope, or changed nothing, raises nothing either, because a flag raised
// from a set that is not there says nothing. The flag changes no outcome: the
// row is the row it would have been (G8).
func TestScopeDriftFlagOnly(t *testing.T) {
	p := week43(t)
	flagOf := func(rec runstore.Record) []string {
		var out []string
		for _, f := range Build([]runstore.Record{rec}, p).Runs[0].Flags {
			out = append(out, f.String())
		}
		return out
	}
	// drifted is one run of the period that declared scope and changed the
	// paths it is given.
	drifted := func(scope, changed []string) runstore.Record {
		rec := run(t, "20261021-090000-abcd", "2026-10-21T09:00:00Z", 0.10, 0,
			runstore.StageTiming{Name: "implement", Calls: counted(1), Agents: counted(0)})
		rec.ExpectedScope = scope
		rec.ChangedFiles = runstore.NewChangedFiles(changed)
		return rec
	}

	for _, tc := range []struct {
		name    string
		scope   []string
		changed []string
		want    bool
	}{
		{"every changed path is in the scope", []string{"internal/digest/**", "docs/*.md"}, []string{"internal/digest/digest.go", "docs/x.md"}, false},
		{"one changed path is outside it", []string{"internal/digest/**"}, []string{"internal/digest/digest.go", "internal/runner/runner.go"}, true},
		{"every changed path is outside it", []string{"docs/**"}, []string{"internal/runner/runner.go"}, true},
		{"the scope names a directory and the path is a file under it", []string{"internal/digest/**"}, []string{"internal/digest/a/b.go"}, false},
		{"the scope is a bare directory name", []string{"internal/digest"}, []string{"internal/digest/digest.go"}, true},
		{"the run changed nothing", []string{"internal/digest/**"}, nil, false},
		{"the task declared no scope", nil, []string{"internal/runner/runner.go"}, false},
		{"the task declared an empty scope", []string{}, []string{"internal/runner/runner.go"}, false},
		{"the task declared only names the matcher cannot read", []string{"[", "  "}, []string{"internal/runner/runner.go"}, false},
	} {
		got := flagOf(drifted(tc.scope, tc.changed))
		if tc.want {
			if len(got) != 1 || got[0] != FlagScopeDrift {
				t.Errorf("%s = %v, want %s", tc.name, got, FlagScopeDrift)
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%s = %v, want no flag", tc.name, got)
		}
	}

	// A record is untrusted input, so the scope read back is bounded the way
	// the task parser bounds it: entries past the task's own cap are not read
	// at all, and what the reader does with the list is what it does with one
	// a task could have declared. The kept patterns name the digest's own
	// package, the entry past the cap names docs, and the run changed a docs
	// path: a reader that went on reading would hold a pattern no task could
	// have declared and report the path as in scope, so the flag is raised
	// here because the only pattern that covers the path was never read.
	over := make([]string, 0, task.MaxDeclaredPaths+1)
	for range task.MaxDeclaredPaths {
		over = append(over, "internal/digest/**")
	}
	over = append(over, "docs/**")
	if got := flagOf(drifted(over, []string{"docs/x.md"})); len(got) != 1 || got[0] != FlagScopeDrift {
		t.Errorf("a changed path covered only by an entry past the task's own cap = %v, want %s: the scope read back is bounded to what a task could have declared", got, FlagScopeDrift)
	}

	// A truncated changed-file set can only hide drift, never invent it: the
	// paths the record kept are all inside the scope, and the flag rests on a
	// path that is in the set and outside the scope whatever the rest held.
	truncated := drifted([]string{"internal/digest/**"}, []string{"internal/digest/digest.go"})
	truncated.ChangedFiles.Truncated = true
	truncated.ChangedFiles.Total = 3
	if got := flagOf(truncated); len(got) != 0 {
		t.Errorf("a truncated set whose kept paths are all in scope raised %v", got)
	}

	// The flag is report only (G8): the row's own figures and status are the
	// figures and status of the same run without a scope.
	flagged := drifted([]string{"internal/digest/**"}, []string{"internal/runner/runner.go"})
	plain := drifted(nil, []string{"internal/runner/runner.go"})
	a, b := Build([]runstore.Record{flagged}, p).Runs[0], Build([]runstore.Record{plain}, p).Runs[0]
	if a.Status != b.Status || a.Billed != b.Billed || a.PR != b.PR || a.Reviews != b.Reviews || len(a.Flags) != 1 {
		t.Errorf("the scope-drift row = %+v, want the plain run's row plus the one flag (%+v)", a, b)
	}
}

// TestDigestCarriesThePeriodsTierExpectations (task-governance T8.3): the
// per-run figures a tier calibration reads, carried by the digest and rendered
// by none of it. Every run of the period that carries a tier is here — the
// report's own rows are a selection a size cap trims, and a calibration reads a
// window — the governance-halted runs are marked rather than dropped (leaving
// them out is the hook's policy, and the mark is what lets it), and an unknown
// duration is absent and never zero (G5).
func TestDigestCarriesThePeriodsTierExpectations(t *testing.T) {
	p := week43(t)
	tiered := func(id, finished, start string, tr tier.Tier, usd float64, minutes int64, halt runstore.HaltReason) runstore.Record {
		rec := run(t, id, finished, 1.00, 0)
		rec.Tier = &runstore.TierRecord{Tier: tr, ExpectedUSD: usd, ExpectedMinutes: minutes}
		if start != "" {
			rec.StartedAt = at(t, start)
		}
		if halt != "" {
			rec.Status = runstore.StatusHalted
			rec.Halt = &runstore.Halt{Reason: halt, Scope: "run", At: at(t, finished)}
		}
		return rec
	}

	recs := []runstore.Record{
		tiered("20261021-090000-0001", "2026-10-21T09:00:00Z", "2026-10-21T08:30:00Z", tier.Small, 1, 30, ""),
		tiered("20261022-090000-0002", "2026-10-22T09:00:00Z", "2026-10-22T08:00:00Z", tier.Medium, 3, 90, runstore.HaltStageBudget),
		tiered("20261023-090000-0003", "2026-10-23T09:00:00Z", "", tier.Large, 8, 240, ""),
		// An untiered run, and one that finished outside the period: neither is
		// a figure a calibration of this period reads.
		run(t, "20261024-090000-0004", "2026-10-24T09:00:00Z", 1.00, 0),
		run(t, "20261101-090000-0005", "2026-11-01T09:00:00Z", 1.00, 0),
	}
	got := Build(recs, p).Expectations
	if len(got) != 3 {
		t.Fatalf("the digest carries %d expectations, want the period's three tiered runs: %+v", len(got), got)
	}
	// The order is the runs' own finish time, whatever order the records
	// arrived in, so a hook reading a window off the digest is a pure function
	// of it (G3).
	for i, want := range []string{"2026-10-21T09:00:00Z", "2026-10-22T09:00:00Z", "2026-10-23T09:00:00Z"} {
		if got[i].Finished.Format(time.RFC3339) != want {
			t.Errorf("expectation %d finished at %s, want %s", i, got[i].Finished.Format(time.RFC3339), want)
		}
	}
	if first := got[0]; first.Tier != string(tier.Small) || first.ExpectedUSD != 1_000_000 || first.ExpectedMinutes != 30 ||
		first.Billed != 1_000_000 || first.Duration != 30*time.Minute || !first.DurationKnown || first.Governance {
		t.Errorf("the first expectation = %+v, want the small run's own figures", first)
	}
	if halted := got[1]; !halted.Governance {
		t.Errorf("the stage_budget-halted run is not marked as governance-halted: %+v", halted)
	}
	if unknown := got[2]; unknown.DurationKnown || unknown.Duration != 0 {
		t.Errorf("a run whose record carries no start has a duration: %+v", unknown)
	}

	// A tier the vocabulary does not know is a block no runner resolved, so the
	// run is not one a calibration reads.
	bogus := tiered("20261025-090000-0006", "2026-10-25T09:00:00Z", "2026-10-25T08:00:00Z", tier.Tier("huge"), 1, 30, "")
	if n := len(Build([]runstore.Record{bogus}, p).Expectations); n != 0 {
		t.Errorf("a record with an unknown tier carries %d expectations, want none", n)
	}
}
