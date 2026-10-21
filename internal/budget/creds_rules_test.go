package budget_test

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestRegistryHaltsWhenRulesRefuseTheCascadeChildren (C1.17): the deployed
// rules are the artifact a downgrade can take the cascade's fields back out
// of, and C1.3's `credsRules` marker cannot see it — an older Fugaro that
// ran `fugaro init --firebase` redeployed its own document over the newer
// one. What the run meets is a permission refusal on the heartbeat that
// carried `credential` or `waiting_until`, and checkCascadeKeys tells it
// from a blip by writing the same entry twice: as sent, then without the
// two children. Refused with them and accepted without them, the children
// are what the rules do not know, and the run halts named — the redeploy is
// the only fix, and a run that silently dropped the write would show as
// holding no credential at all.
//
// The halt is the whole of the run's answer: the stripped write is not
// followed by another attempt to write the child.
func TestRegistryHaltsWhenRulesRefuseTheCascadeChildren(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		set  func(e *budget.AgentEntry)
	}{
		{"credential", "credential", func(e *budget.AgentEntry) { e.Credential = "claude-shared-account" }},
		{"waiting_until", "waiting_until", func(e *budget.AgentEntry) { e.WaitingUntil = noon.Add(30 * time.Minute).UnixMilli() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			s := f.started(budget.AgentEntry{Stage: "bootstrap"})
			f.db.DenyKeys(tc.key)
			s.Update(tc.set)
			h, ok := f.nextHalt(3 * time.Second)
			if !ok {
				t.Fatal("the run was not halted; a refused cascade write must halt it named")
			}
			if h.Reason != budget.ReasonCredsRulesDenied || h.Scope != budget.ScopeRun {
				t.Fatalf("halt = %+v, want %s/%s", h, budget.ReasonCredsRulesDenied, budget.ScopeRun)
			}
			if !contains(h.Detail, "fugaro init --firebase") {
				t.Errorf("the halt's detail does not name the redeploy: %q", h.Detail)
			}
			// The stripped write landed: the run's entry is there, without
			// the child the rules refused, and it stays that way.
			waitFor(t, "the entry to settle without the refused child", func() bool {
				e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
				return e != nil && e["stage"] == "bootstrap" && e[tc.key] == nil
			})
			time.Sleep(150 * time.Millisecond) // several heartbeats
			if e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any); e[tc.key] != nil {
				t.Fatalf("the child came back after the halt: %v", e)
			}
			if h2, ok := f.nextHalt(100 * time.Millisecond); ok {
				t.Fatalf("a second halt: %+v", h2)
			}
		})
	}
}

// TestRegistryKeepsTheCascadeChildWhenTheRulesAcceptIt: the probe is not a
// blanket drop. The same child the test above refuses is written and kept
// when the rules know it — otherwise that test would pass for a run that
// stripped its credential state on every heartbeat.
func TestRegistryKeepsTheCascadeChildWhenTheRulesAcceptIt(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.Update(func(e *budget.AgentEntry) {
		e.Credential, e.WaitingUntil = "claude-shared-account", noon.Add(30*time.Minute).UnixMilli()
	})
	waitFor(t, "the children to reach the registry", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["credential"] == "claude-shared-account"
	})
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a run whose rules accept the children was halted: %+v", h)
	}
}
