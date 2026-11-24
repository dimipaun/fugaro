package routing

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestProblems is RT3's pin on the table's checks: one sub-test per rule the
// checks have, and one for the table a run can be served by.
//
// The fixtures are a project's own configuration and not a repository's — the
// accounts, the key, the pool that holds them, one provider entry and one
// model of each shape a table routes — because that is what a table is judged
// against (contract-table.md). A sub-test that changes one of them says so
// where it changes it.
//
// Every sub-test asks for the *codes* and not for the sentences: a sentence is
// written to be read and reworded, and what a caller acts on is the rule's own
// name (Problem.Code). The two sub-tests that are about *where* a problem is —
// the resolver's answer, and a model no rung reaches — assert the path as well.
func TestProblems(t *testing.T) {
	t.Run("unmatched_craft_model", func(t *testing.T) {
		// The red step: a model a run can call that no entry matches. It is
		// also RT1's pin, because the stub C1 ships answers the table's first
		// entry for every model — against the stub this sub-test finds no
		// problem at all, which is the wrong table passing.
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindPool, Pool: "team"}}}},
			"claude-sonnet-5-5", "deepseek-v4")
		ps := wantProblems(t, in, CodeNoEntry)
		if !strings.Contains(ps[0].Message, "deepseek-v4") {
			t.Errorf("the problem is %q, and it does not name the model no entry matches", ps[0].Message)
		}
		if ps[0].Entry != -1 {
			t.Errorf("the problem names entry %d, and a model no entry matches is no entry's: it is the table's", ps[0].Entry)
		}
	})

	t.Run("most_specific_entry_is_the_one_judged", func(t *testing.T) {
		// A model is judged by the entry the RESOLVER answers with (D3172) and
		// not by the first entry that happens to match: here the exact entry is
		// the broken one, and the prefix entry that precedes it is fine.
		in := checksFixture(Table{
			{Match: "claude-*", Chain: []Source{{Kind: KindPool, Pool: "team"}}},
			{Match: "claude-sonnet-5-5", Chain: []Source{{Kind: KindPool, Pool: "ghost"}}},
		}, "claude-sonnet-5-5")
		ps := wantProblems(t, in, CodeUndeclared)
		if ps[0].Path != "model_routing[1].chain[0]" {
			t.Errorf("the problem is at %s, want model_routing[1].chain[0]: the most specific match is the entry judged", ps[0].Path)
		}
	})

	t.Run("empty_chain", func(t *testing.T) {
		// D3180's other end: an entry that names no source at all. The table's
		// own loader refuses an entry with no match and says nothing about a
		// missing chain, so an entry a run's model resolves to can name nothing —
		// and a chain with no source at all has no reset for the walk to report
		// an exhausted call against (R22: refused at launch, not mid-run).
		in := checksFixture(Table{{Match: "claude-*"}}, "claude-sonnet-5-5")
		ps := wantProblems(t, in, CodeEmptyChain)
		if ps[0].Path != "model_routing[0].chain" {
			t.Errorf("the problem is at %s, want model_routing[0].chain: the entry's own chain is what is empty", ps[0].Path)
		}
	})

	t.Run("launcher_pool_only_chain", func(t *testing.T) {
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindLauncherPool}}}},
			"claude-sonnet-5-5")
		wantProblems(t, in, CodeLauncherPoolOnly)
	})

	t.Run("source_does_not_reach", func(t *testing.T) {
		// A credential of the project for a model it cannot be sent: the
		// project's Anthropic key reaches Anthropic's models and nothing else.
		key := checksFixture(Table{{Match: "deepseek/*", Chain: []Source{{Kind: KindAnthropic}}}},
			"deepseek/deepseek-v4")
		wantProblems(t, key, CodeNoReach)
		// And a pool, whose reach is its members' own: this one holds a
		// provider key that claims deepseek/* and not the Claude model asked
		// for, so no member of it can be sent that model.
		pool := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindPool, Pool: "routed"}}}},
			"claude-sonnet-5-5")
		wantProblems(t, pool, CodeNoReach)
	})

	t.Run("no_mapped_spelling", func(t *testing.T) {
		// A Claude model with no measured route ID, through a provider key: the
		// map holds no spelling for it on that kind, and a spelling derived
		// here would be a call to a model nobody measured (D3175).
		provider := checksFixture(Table{{Match: "claude-sonnet-5", Chain: []Source{{Kind: KindProvider, Provider: "or"}}}},
			"claude-sonnet-5")
		wantProblems(t, provider, CodeNoSpelling)
		// And a Claude model the map does not carry at all, through Vertex —
		// the other half of D3196's pair.
		vertex := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindVertex}}}},
			"claude-nova-9")
		wantProblems(t, vertex, CodeNoSpelling)
	})

	t.Run("claude_through_a_provider_before_check_50", func(t *testing.T) {
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindProvider, Provider: "or"}}}},
			"claude-sonnet-5-5")
		wantProblems(t, in, CodeCheck50)
	})

	t.Run("claude_through_a_pool_before_check_50", func(t *testing.T) {
		// R3 is about a chain that reaches a Claude model through OpenRouter, and
		// a pool is such a chain: a member of kind provider is the route a source
		// of that kind names, and the pool's own rule may put the call on it (a
		// round-robin pool picks a member nobody chose), so the gate is asked of
		// the pool's members too. The model is written as the route's own ID
		// here, because that is the spelling a route claims and the one the
		// provider entry lists.
		in := checksFixture(Table{{Match: "anthropic/*", Chain: []Source{{Kind: KindPool, Pool: "routed"}}}},
			"anthropic/claude-sonnet-5.5")
		wantProblems(t, in, CodeCheck50)
		// And the record is what lifts it, exactly as it does for a provider
		// source: the pool is accepted as it stands.
		in.Check50Passed = true
		wantProblems(t, in)
	})

	t.Run("check_50_passed", func(t *testing.T) {
		// The same table, with the check recorded: R3's gate is the record and
		// not the shape, so the entry is accepted as it stands.
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindProvider, Provider: "or"}}}},
			"claude-sonnet-5-5")
		in.Check50Passed = true
		wantProblems(t, in)
	})

	t.Run("paid_after_pool", func(t *testing.T) {
		chain := []Source{{Kind: KindPool, Pool: "team"}, {Kind: KindAnthropic}}
		// R17 is D152's rule read for a chain: the budget must be enforcing
		// with a ceiling above zero. Observe is not enough (the ceiling would
		// be recorded and not enforced) and a cap of zero is not either.
		for _, tc := range []struct {
			name string
			mode string
			cap  float64
			want []string
		}{
			{"observe_with_a_cap", config.BudgetObserve, 5, []string{CodePaidAfterPool}},
			{"enforce_without_a_cap", config.BudgetEnforce, 0, []string{CodePaidAfterPool}},
			{"no_budget_at_all", "", 0, []string{CodePaidAfterPool}},
			{"enforce_with_a_cap", config.BudgetEnforce, 5, nil},
		} {
			in := checksFixture(Table{{Match: "claude-*", Chain: chain}}, "claude-sonnet-5-5")
			in.BudgetMode, in.BudgetPerRunUSD = tc.mode, tc.cap
			wantProblems(t, in, tc.want...)
		}
	})

	t.Run("undeclared_pool", func(t *testing.T) {
		// Two models resolve to the entry and the pool is missing for both: the
		// problem is about the entry and is reported once.
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindPool, Pool: "ghost"}}}},
			"claude-sonnet-5-5", "claude-opus-5")
		ps := wantProblems(t, in, CodeUndeclared)
		if ps[0].Path != "model_routing[0].chain[0]" {
			t.Errorf("the problem is at %s, want model_routing[0].chain[0]", ps[0].Path)
		}
	})

	t.Run("undeclared_provider", func(t *testing.T) {
		in := checksFixture(Table{{Match: "claude-*", Chain: []Source{{Kind: KindProvider, Provider: "ghost"}}}},
			"claude-sonnet-5-5")
		wantProblems(t, in, CodeUndeclared)
	})

	t.Run("lone_subscription_is_a_warning", func(t *testing.T) {
		in := checksFixture(goodTable(), "claude-sonnet-5-5", "deepseek/deepseek-v4")
		in.BudgetMode, in.BudgetPerRunUSD = config.BudgetEnforce, 10
		in.Credentials = append(in.Credentials, config.Credential{Name: "spare", Type: config.CredentialTypeClaudeOAuth})
		ps, ws := Problems(in)
		if len(ps) != 0 {
			t.Fatalf("Problems refused a table that is fine: %s", problemsText(ps))
		}
		if len(ws) != 1 || ws[0].Code != CodeLoneSubscription {
			t.Fatalf("Problems reported the warnings %s, want exactly one %s: a token in no pool is reported in release 1 and refused in release 2 (X2)", problemsText(ws), CodeLoneSubscription)
		}
		if !strings.Contains(ws[0].Message, "spare") {
			t.Errorf("the warning is %q, and it does not name the token that is in no pool", ws[0].Message)
		}
	})

	t.Run("non_claude_on_vertex", func(t *testing.T) {
		in := checksFixture(Table{{Match: "gemini-*", Chain: []Source{{Kind: KindVertex}}}},
			"gemini-2.5-pro")
		wantProblems(t, in, CodeVertexModel)
	})

	t.Run("an_entry_no_run_model_reaches_is_not_judged", func(t *testing.T) {
		// The per-model rules are about the models a run can call (R22): an
		// entry no rung of this project resolves to is not judged on what it
		// would serve, and the same table with a Gemini model among the rungs
		// is refused by the sub-test above.
		in := checksFixture(Table{
			{Match: "claude-*", Chain: []Source{{Kind: KindPool, Pool: "team"}}},
			{Match: "gemini-*", Chain: []Source{{Kind: KindVertex}}},
		}, "claude-sonnet-5-5")
		wantProblems(t, in)
	})

	t.Run("a_table_a_run_can_be_served_by", func(t *testing.T) {
		// spec.md's own example: the launcher's pool first, then the project's
		// shared pool, then the key for Claude, and a provider key for the
		// models it claims.
		in := checksFixture(goodTable(), "claude-sonnet-5-5", "deepseek/deepseek-v4")
		in.BudgetMode, in.BudgetPerRunUSD = config.BudgetEnforce, 10
		wantProblems(t, in)
	})
}

// goodTable is the table the two "nothing is wrong" sub-tests judge: a Claude
// chain that starts at the launcher's pool and ends at the project's key, and a
// provider key for the models its entry claims.
func goodTable() Table {
	return Table{
		{Match: "claude-*", Chain: []Source{{Kind: KindLauncherPool}, {Kind: KindPool, Pool: "team"}, {Kind: KindAnthropic}}},
		{Match: "deepseek/*", Chain: []Source{{Kind: KindPool, Pool: "routed"}}},
	}
}

// checksFixture is the project a table is judged against: two accounts — one of
// them in the pool team — the project's key, one provider entry claiming one
// Claude route and one vendor's models, and no budget and no Check 50 record
// until a sub-test writes one.
func checksFixture(table Table, models ...string) Checks {
	return Checks{
		Table:  table,
		Models: models,
		Credentials: []config.Credential{
			{Name: "acct", Type: config.CredentialTypeClaudeOAuth},
			{Name: "key", Type: config.CredentialTypeAnthropic},
			{Name: "or", Type: config.CredentialTypeOpenRouter},
		},
		Pools: []config.Pool{
			{Name: "team", Members: []string{"acct"}},
			{Name: "routed", Members: []string{"or"}},
		},
		Providers: map[string]config.ModelProvider{
			"or": {Models: []string{"anthropic/claude-sonnet-5.5", "deepseek/*"}},
		},
	}
}

// wantProblems asserts the refusals are exactly the codes wanted, in order, and
// that nothing was reported as a warning.
func wantProblems(t *testing.T, in Checks, codes ...string) []Problem {
	t.Helper()
	ps, ws := Problems(in)
	if len(ws) != 0 {
		t.Errorf("Problems reported the warnings %s, and this table has none", problemsText(ws))
	}
	if got := codesOf(ps); !slices.Equal(got, codes) {
		t.Fatalf("Problems reported %v, want %v:\n%s", got, codes, problemsText(ps))
	}
	return ps
}

// codesOf is the rules a list of problems names, in order.
func codesOf(ps []Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// problemsText is a list of problems for a failure message: the path and the
// sentence, which is what a person reads.
func problemsText(ps []Problem) string {
	if len(ps) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  ")
		b.WriteString(p.Path)
		b.WriteString(" [")
		b.WriteString(p.Code)
		b.WriteString("]: ")
		b.WriteString(p.Message)
		b.WriteString("\n")
	}
	return b.String()
}
