package budget_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
)

// TestMaxEntryTokensMatchesRules pins budget.MaxEntryTokens, duplicated
// because budget can't import budget/rules (an import cycle through rules'
// own tests), to the rules generator's bound. A rules change that moves one
// without the other reopens the review-focus-4 freeze: a tokens value the
// rules now refuse but Go would still send.
func TestMaxEntryTokensMatchesRules(t *testing.T) {
	if budget.MaxEntryTokens != rules.MaxTokensPerWrite {
		t.Fatalf("budget.MaxEntryTokens = %d, rules.MaxTokensPerWrite = %d", budget.MaxEntryTokens, rules.MaxTokensPerWrite)
	}
}

// TestRegistryDropsNewKeysOnceWhenRulesRefuse (Review Focus 4): rules
// deployed before 0.7.0 refuse an entry that carries action or tokens
// (their $other). The session drops both for the rest of the run, warns
// once naming fugaro init, and the entry is still written without them.
func TestRegistryDropsNewKeysOnceWhenRulesRefuse(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	f.db.DenyKeys("action", "tokens")
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	waitFor(t, "the fallback warning", func() bool { return strings.Contains(logs.String(), "fugaro init") })
	waitFor(t, "the entry to settle without the new keys", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "bootstrap" && e["action"] == nil && e["tokens"] == nil
	})
	if n := strings.Count(logs.String(), "fugaro init"); n != 1 {
		t.Fatalf("warned %d times, want once: %s", n, logs.String())
	}
	// The coder keeps calling Update with a new action after the drop (every
	// tool call does); the fallback must not warn again, and a dropped key
	// reappearing in the same Update must not collaterally block the stage
	// change it carries (the whole entry is one atomic write).
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens, e.Stage = "tool Bash: go vet", 1300, "review" })
	waitFor(t, "the stage change despite the reappearing keys", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "review"
	})
	if e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any); e["action"] != nil || e["tokens"] != nil {
		t.Fatalf("a later Update brought the dropped keys back: %v", e)
	}
	if n := strings.Count(logs.String(), "fugaro init"); n != 1 {
		t.Fatalf("warned %d times after a later Update, want still once: %s", n, logs.String())
	}
}

// TestCheckNewKeysProbesTheCurrentSnapshot (review focus 4): the probe write
// that decides whether the rules accept action/tokens must carry the run's
// current spent and updatedAt, not the ones Start wrote minutes earlier and
// never touches again. HoldNext pins down the exact request checkNewKeys
// sends, so the assertion can't be satisfied by the very next heartbeat's
// own (already-correct) write quietly papering over a stale probe.
func TestCheckNewKeysProbesTheCurrentSnapshot(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.SetUsage(func() budget.Micros { return 5_000_000 })
	waitFor(t, "the bootstrap entry with no action yet", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["action"] == nil
	})
	var body []byte
	for attempt := 0; ; attempt++ {
		stamped, release := f.db.HoldNext("")
		s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
		select {
		case <-stamped:
		case <-time.After(2 * time.Second):
			t.Fatal("no heartbeat write arrived to hold")
		}
		reqs := f.db.Requests()
		body = reqs[len(reqs)-1].Body
		release()
		if strings.Contains(string(body), `"action"`) {
			break
		}
		if attempt >= 20 {
			t.Fatal("20 heartbeats never carried the action key")
		}
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(body, &patch); err != nil {
		t.Fatalf("decode patch %s: %v", body, err)
	}
	raw, ok := patch[budget.PathAgent(slug, runID)]
	if !ok {
		t.Fatalf("patch has no agent entry: %s", body)
	}
	var e struct {
		Spent     int64 `json:"spent"`
		UpdatedAt int64 `json:"updatedAt"`
		Action    string
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("decode entry %s: %v", raw, err)
	}
	if e.Spent != 5_000_000 {
		t.Fatalf("the probe's spent = %d, want 5000000 (the current snapshot, not Start's stale 0)", e.Spent)
	}
	if staleness := time.Since(time.UnixMilli(e.UpdatedAt)); staleness > 2*time.Second {
		t.Fatalf("the probe's updatedAt is %s old, want a fresh snapshot", staleness)
	}
}

// A second refusal with the new keys already dropped is a genuinely bad
// credential, not old rules: it must not be mistaken for the same fallback.
func TestRegistryKeepsNewKeysWhenRulesAccept(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	waitFor(t, "a heartbeat to carry the new keys", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["action"] == "tool Bash: go test"
	})
	if strings.Contains(logs.String(), "fugaro init") {
		t.Fatalf("warned even though the rules accepted the keys: %s", logs.String())
	}
	if e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any); e["tokens"] != json.Number("1200") {
		t.Fatalf("tokens = %v", e["tokens"])
	}
}

// TestRegistryRecoversFromATransientDoubleRefusal (review focus 4): a
// credential or network blip (DenyNext) denies two writes in a row right
// when action/tokens first appear, at the same time the rules genuinely
// don't have the keys yet (DenyKeys, permanent). The old code mistook the
// blip for "refused twice, must be a bad credential" and froze every later
// heartbeat without ever dropping the keys or warning. The fixed code keeps
// trying every beat until it gets an unambiguous answer, and warns exactly
// once once it does.
func TestRegistryRecoversFromATransientDoubleRefusal(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	f.db.DenyKeys("action", "tokens") // permanent: the rules predate these keys
	f.db.DenyNext(2)                  // transient: the next two writes, whatever they are
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	waitFor(t, "the entry to settle without the new keys, past the blip", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["stage"] == "bootstrap" && e["action"] == nil && e["tokens"] == nil
	})
	waitFor(t, "the fallback warning", func() bool { return strings.Contains(logs.String(), "fugaro init") })
	if n := strings.Count(logs.String(), "fugaro init"); n != 1 {
		t.Fatalf("warned %d times, want once: %s", n, logs.String())
	}
}

// TestRegistryReArmsAfterRulesStopAcceptingTheNewKeys (review focus 4):
// checkNewKeys only ever probes once it has a definite answer, so once it
// has found the rules accept the keys it never looks again. If the rules
// are then redeployed older (an operator reran an old fugaro init, or
// rolled back), only heartbeat's own fallback — spotting a permission
// refusal on a beat whose entry still carries the keys — can notice and
// re-arm the probe; checkNewKeys's own per-beat retry (pinned by
// TestRegistryRecoversFromATransientDoubleRefusal) never fires again on its
// own once newKeysChecked is true.
func TestRegistryReArmsAfterRulesStopAcceptingTheNewKeys(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	waitFor(t, "the rules to accept the keys the first time", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["action"] == "tool Bash: go test"
	})
	f.db.DenyKeys("action", "tokens") // the rules are downgraded mid-run
	waitFor(t, "the entry to settle without the new keys again", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["action"] == nil && e["tokens"] == nil
	})
	waitFor(t, "the fallback warning", func() bool { return strings.Contains(logs.String(), "fugaro init") })
}

// TestRegistryOutageDoesNotDropNewKeys (review focus 4): an outage (503, not
// a permission refusal) while the entry carries action/tokens must never be
// mistaken for old rules: the keys stay, and there is no "predate 0.7.0"
// warning, however long the outage lasts.
func TestRegistryOutageDoesNotDropNewKeys(t *testing.T) {
	f := newFixture(t)
	logs := captureLog(f)
	s := f.started(budget.AgentEntry{Stage: "bootstrap"})
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	s.Update(func(e *budget.AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	time.Sleep(150 * time.Millisecond) // several heartbeats, all outed; well under the 600ms grace
	f.db.Refuse(0, "", "", "")
	waitFor(t, "the action and tokens to finally reach the registry once the outage lifts", func() bool {
		e, _ := f.db.Value(budget.PathAgent(slug, runID)).(map[string]any)
		return e != nil && e["action"] == "tool Bash: go test"
	})
	if strings.Contains(logs.String(), "fugaro init") {
		t.Fatalf("an outage was mistaken for old rules refusing the keys: %s", logs.String())
	}
}
