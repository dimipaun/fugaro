package budget_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/cli"
)

// flagRE matches a long flag in a refusal's text.
var flagRE = regexp.MustCompile(`--[a-z][a-z-]*`)

// capMessages are the refusals that tell the operator how to set the cap they
// are missing, and so must name flags `fugaro budget set` really has. Each is
// reached through the code that prints it: Evaluate (a refused lease) and the
// session's admit (a halt at bootstrap).
func capMessages(t *testing.T) map[string]string {
	t.Helper()
	now := noon
	st := budget.State{}
	w := budget.Write{Op: budget.OpLease, Day: budget.Day(now), Amount: 1_000}
	limits := &budget.Limits{MaxReserveMicros: micros(5_000_000)}
	globalPerRun := &budget.GlobalCaps{PerRunMicros: micros(20_000_000)}
	refusal := func(name string, caps budget.Caps) string {
		t.Helper()
		d := budget.Evaluate(st, w, caps, budget.Kills{}, now)
		if d.Allow {
			t.Fatalf("%s: Evaluate allowed the lease, want a refusal naming the flags", name)
		}
		return d.Detail
	}

	msgs := map[string]string{}

	// The repository's daily cap is missing: its per-run cap and the global
	// ones are set, so the daily cap is the first that bites.
	msgs["Evaluate repo daily"] = refusal("repo daily", budget.Caps{
		Mode: budget.ModeEnforce, Limits: limits, Global: globalPerRun,
		Defaults: &budget.DefaultCaps{RepoPerRunMicros: micros(20_000_000)},
	})

	// No per-run cap for the repository, and no default either.
	msgs["Evaluate repo per-run"] = refusal("repo per-run", budget.Caps{
		Mode: budget.ModeEnforce, Limits: limits, Global: globalPerRun,
	})

	// The repository's per-run cap exists but the global one does not.
	msgs["Evaluate global per-run"] = refusal("global per-run", budget.Caps{
		Mode: budget.ModeEnforce, Limits: limits,
		Defaults: &budget.DefaultCaps{RepoPerRunMicros: micros(20_000_000)},
	})

	// Everything per-run is set, only the global daily cap is missing.
	msgs["Evaluate global daily"] = refusal("global daily", budget.Caps{
		Mode: budget.ModeEnforce, Limits: limits, Global: globalPerRun,
		Defaults: &budget.DefaultCaps{RepoPerRunMicros: micros(20_000_000), RepoDailyMicros: micros(60_000_000)},
	})

	// At bootstrap: the repository has no daily cap of its own and the
	// defaults carry only a per-run cap.
	f := newFixture(t)
	f.db.Set(budget.PathCapsRepo(slug), nil)
	f.db.Set(budget.PathCapsDefaults, budget.DefaultCaps{RepoPerRunMicros: ptr(20_000_000)})
	msgs["Admit repo daily"] = admitDetail(t, f, budget.AdmitOptions{Gateway: true})

	// At bootstrap: the database only observes, but the run enforces and no
	// per-run cap exists anywhere.
	f2 := newFixture(t)
	f2.db.Set(budget.PathMode, "observe")
	f2.db.Set(budget.PathCapsRepo(slug), nil)
	f2.db.Set(budget.PathCapsGlobal, nil)
	f2.cfg.LocalEnforce = true
	msgs["Admit local enforce per-run"] = admitDetail(t, f2, budget.AdmitOptions{Gateway: true, LocalEnforce: true})

	return msgs
}

// admitDetail admits a run and returns the no_cap halt's detail.
func admitDetail(t *testing.T, f *fixture, o budget.AdmitOptions) string {
	t.Helper()
	h, err := f.open().Admit(context.Background(), o)
	if err != nil || h == nil || h.Reason != budget.ReasonNoCap {
		t.Fatalf("Admit: %+v, %v (want a no_cap halt)", h, err)
	}
	return h.Detail
}

// TestCapHintsNameRealFlags: a refusal that says how to set a missing cap must
// name flags `fugaro budget set` really has, so the operator can paste what it
// prints. The command is looked up in the real root command, which pins the
// path the hints name too.
func TestCapHintsNameRealFlags(t *testing.T) {
	root := cli.NewRootCmd()
	set, _, err := root.Find([]string{"budget", "set"})
	if err != nil || set.Name() != "set" {
		t.Fatalf("fugaro budget set: %v", err)
	}
	msgs := capMessages(t)
	if len(msgs) != 6 {
		t.Fatalf("collected %d messages, want 6", len(msgs))
	}
	for name, msg := range msgs {
		i := strings.Index(msg, "fugaro budget set")
		if i < 0 {
			t.Errorf("%s: the refusal does not name the command that sets the cap: %q", name, msg)
			continue
		}
		flags := flagRE.FindAllString(msg[i:], -1)
		if len(flags) == 0 {
			t.Errorf("%s: the refusal names no flag: %q", name, msg)
			continue
		}
		for _, f := range flags {
			if set.Flags().Lookup(strings.TrimPrefix(f, "--")) == nil {
				t.Errorf("%s: the refusal names %s, which `fugaro budget set` does not have: %q", name, f, msg)
			}
		}
	}
}

// TestCapHintsHaveNoStaleFlags pins the two flags the fix removed, so a rewrite
// that brings one back fails on the text alone.
func TestCapHintsHaveNoStaleFlags(t *testing.T) {
	for _, stale := range []string{"--repo-daily", "--repo-per-run"} {
		for name, msg := range capMessages(t) {
			if strings.Contains(msg, stale) {
				t.Errorf("%s: %q names %s, which is not a flag of `fugaro budget set`", name, msg, stale)
			}
		}
	}
}

// micros is a cap value.
func micros(m budget.Micros) *budget.Micros { return &m }
