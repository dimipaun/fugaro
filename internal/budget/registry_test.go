package budget_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

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
