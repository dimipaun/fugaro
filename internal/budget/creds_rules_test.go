package budget_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestRegistryHaltsWhenRulesRefuseTheCascadeChildren (C1.17): the deployed
// rules are the artifact a downgrade can take the cascade's fields back out
// of, and C1.3's `credsRules` marker cannot see it — an older Fugaro that
// ran `fugaro update --firebase-project` redeployed its own document over the newer
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
			if !contains(h.Detail, "fugaro update --firebase-project") {
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

// TestRegistryCascadeProbeRetriesAfterATransientAcceptance (M11): the probe is
// summoned by a permission refusal on the heartbeat, and the first refusal it
// meets is often a blip — another writer of the same entry, a stale race —
// which its own write, the entry as the heartbeat sent it, settles by being
// accepted. That is no answer about the cascade's children: the entry the
// rules took carried them. The probe must stay armed, because the refusal
// after this one may be the real one; a probe that stood down after the first
// acceptance would leave the rest of the run writing an entry the deployed
// rules silently strip, which is the state the named halt exists to prevent.
//
// The heartbeat hands a refusal to the probe only after the flush has been
// denied maxStale times and its entry-only write once more (lease.go), so
// denying exactly those writes summons the probe and leaves its own write
// accepted — the transient acceptance under test.
func TestRegistryCascadeProbeRetriesAfterATransientAcceptance(t *testing.T) {
	const flushWritesBeforeTheProbe = maxStale + 1 // maxStale denied rounds + the entry-only write
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.Update(func(e *budget.AgentEntry) { e.WaitingUntil = noon.Add(30 * time.Minute).UnixMilli() })
	release := holdCascadeWrite(t, f, "waiting_until")
	f.db.DenyNext(flushWritesBeforeTheProbe)
	release()
	waitFor(t, "the probe's accepted write to land with the child", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["waiting_until"] != nil
	})
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a transient acceptance halted the run: %+v", h)
	}
	// Now the rules really are the ones refusing: the next refusal must still
	// be diagnosed.
	f.db.DenyKeys("waiting_until")
	h, ok := f.nextHalt(3 * time.Second)
	if !ok {
		t.Fatalf("the probe stood down after a transient acceptance: the refusal that followed was never diagnosed (this test denied %d writes, the flush's own, so a change to maxStale would move the probe's write)", flushWritesBeforeTheProbe)
	}
	if h.Reason != budget.ReasonCredsRulesDenied || h.Scope != budget.ScopeRun {
		t.Fatalf("halt = %+v, want %s/%s", h, budget.ReasonCredsRulesDenied, budget.ScopeRun)
	}
}

// TestRegistryCascadeProbeRetriesAfterAnOutage (M11): the probe's own write
// can meet an outage — a 503, or any other failure that is not a permission
// refusal. That says nothing about the children: the rules never answered.
// Caching it as an answer would turn the named halt off for the rest of the
// run just as a transient acceptance would, so the probe stays armed and the
// next refused heartbeat asks again.
func TestRegistryCascadeProbeRetriesAfterAnOutage(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.Update(func(e *budget.AgentEntry) { e.WaitingUntil = noon.Add(30 * time.Minute).UnixMilli() })
	release := holdUntilTheProbeIsSummoned(t, f)
	// The held write — the entry-only write the flush sends before it hands
	// the refusal to the probe — comes back 401, which summons the probe; the
	// outage is set while it is held, so the probe's own write, the very next
	// request the session sends, meets it. A refusal is read when a request
	// arrives, so the held write keeps the 401 it was going to get either way.
	f.db.DenyNext(1)
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	base := rtdbWrites(f)
	release()
	waitFor(t, "the probe's write to meet the outage", func() bool { return rtdbWrites(f) > base })
	f.db.Refuse(0, "", "", "")
	f.db.DenyKeys("waiting_until")
	h, ok := f.nextHalt(3 * time.Second)
	if !ok {
		t.Fatal("the probe stood down after an outage: the refusal that followed was never diagnosed")
	}
	if h.Reason != budget.ReasonCredsRulesDenied || h.Scope != budget.ScopeRun {
		t.Fatalf("halt = %+v, want %s/%s", h, budget.ReasonCredsRulesDenied, budget.ScopeRun)
	}
}

// holdUntilTheProbeIsSummoned holds the write that makes the heartbeat hand a
// refusal to the probe: the entry-only write the flush sends after maxStale
// denied rounds (lease.go). It denies those rounds one held write at a time,
// so the write it returns the release for is that last one — and the probe's
// own write is the next request the session sends.
func holdUntilTheProbeIsSummoned(t *testing.T, f *fixture) (release func()) {
	t.Helper()
	release = holdCascadeWrite(t, f, "waiting_until")
	for i := 0; i < maxStale; i++ {
		f.db.DenyNext(1)
		release()
		release = holdCascadeWrite(t, f, "waiting_until")
	}
	return release
}

// maxStale is internal/budget/session.go's maxStale: how many denied rounds a
// flush makes before it writes the entry on its own and gives up. These tests
// count the flush's writes, so the two move together.
const maxStale = 8

// holdCascadeWrite holds the next heartbeat write that carries the cascade
// child key, and returns the release for it. The entry may not carry the key
// when the hold is registered — a heartbeat can have taken its snapshot
// already — so the loop retries, as TestCheckNewKeysProbesTheCurrentSnapshot
// does.
func holdCascadeWrite(t *testing.T, f *fixture, key string) (release func()) {
	t.Helper()
	for attempt := 0; ; attempt++ {
		stamped, release := f.db.HoldNext("")
		select {
		case <-stamped:
		case <-time.After(2 * time.Second):
			t.Fatal("no heartbeat write arrived to hold")
		}
		if strings.Contains(string(rtdbLastWrite(f)), `"`+key+`"`) {
			return release
		}
		release()
		if attempt >= 20 {
			t.Fatalf("20 heartbeats never carried %s", key)
		}
	}
}

// rtdbLastWrite is the body of the last write request the fake has seen (nil
// when there is none). Reads are skipped: the session's kill watch and grace
// watch read the database too, and neither is what a test holding a write
// means to look at.
func rtdbLastWrite(f *fixture) []byte {
	reqs := f.db.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Method != http.MethodGet {
			return reqs[i].Body
		}
	}
	return nil
}

// rtdbWrites is how many write requests the fake has answered so far.
func rtdbWrites(f *fixture) int {
	n := 0
	for _, r := range f.db.Requests() {
		if r.Method != http.MethodGet {
			n++
		}
	}
	return n
}
