package routing_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

// The resolver (RT1): which entry of the table a model is routed by. D3172 is
// the whole rule — the most specific match wins, an exact id over a prefix and a
// longer prefix over a shorter one — and these tests pin it at the resolution
// and not at the config, because every side that walks a chain (CS1), checks a
// table (RT3) and migrates an installation (MG1) reads this one answer.

// pooled is an entry whose chain names its pattern, so a test can tell WHICH
// entry came back and not only which pattern matched: a resolver that picked the
// right row and returned the wrong chain would send a call to a source nobody
// wrote, which is the failure this answer exists to prevent.
func pooled(match string) routing.Entry {
	return routing.Entry{Match: match, Chain: []routing.Source{{Kind: routing.KindPool, Pool: match}}}
}

// TestResolveMostSpecificWins is RT1's red step: against the stub C1 ships, which
// answers the table's first entry for every model, the exact-id case below gets
// `claude-*`'s chain and fails.
func TestResolveMostSpecificWins(t *testing.T) {
	if routing.Resolve == nil {
		t.Fatal("no resolver is assigned: the contract's stub still stands, and every chain walk would follow the table's first entry")
	}

	table := routing.Table{
		pooled("claude-*"),
		pooled("claude-sonnet-*"),
		pooled("claude-sonnet-5-5"),
		pooled("deepseek-*"),
	}

	for _, tc := range []struct {
		what  string
		model string
		want  string
	}{
		// An exact id beats every prefix that claims it, including the longer
		// one: "claude-sonnet-*" is a narrower claim than "claude-*" and the id
		// is narrower than both.
		{"an exact id beats a prefix", "claude-sonnet-5-5", "claude-sonnet-5-5"},
		// Two prefixes both claim it and the longer one wins: a model one
		// release newer than the id above, which no entry names exactly.
		{"a longer prefix beats a shorter one", "claude-sonnet-5-6-preview", "claude-sonnet-*"},
		// The short prefix is the only claim on this model, so it wins by
		// default — and it is the entry the stub would have answered anyway,
		// which is the one case where the stub was not wrong.
		{"the only pattern that claims the model", "claude-haiku-4-5", "claude-*"},
		// A model another branch of the table claims is not read through the
		// first entry's pattern: the resolver weighs matches, it does not
		// return the table in order.
		{"another branch of the table", "deepseek-v4.1", "deepseek-*"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, err := routing.Resolve(table, tc.model)
			if err != nil {
				t.Fatalf("resolving %q: %v", tc.model, err)
			}
			if want := pooled(tc.want); !reflect.DeepEqual(got, want) {
				t.Errorf("%q resolves to %+v, and the most specific match is %+v", tc.model, got, want)
			}
		})
	}
}

// TestResolveRefusesTwinPatterns pins the one tie D3172 refuses: two entries
// written with the same pattern claim a model equally, so no chain is the more
// specific one and answering would serve whichever the project wrote first.
func TestResolveRefusesTwinPatterns(t *testing.T) {
	first := pooled("claude-*")
	second := routing.Entry{Match: "claude-*", Chain: []routing.Source{{Kind: routing.KindAnthropic}}}
	other := pooled("deepseek-*")

	t.Run("a model the twins claim is refused", func(t *testing.T) {
		got, err := routing.Resolve(routing.Table{first, second, other}, "claude-sonnet-5-5")
		if err == nil {
			t.Fatalf("two entries written %q resolved to %+v: the tie must be refused, not broken by position", first.Match, got)
		}
		// The refusal is "I cannot pick between these two" and not "nothing
		// claims the model": a caller that read it as ErrNoEntry would report a
		// missing entry where the fix is one entry too many.
		if errors.Is(err, routing.ErrNoEntry) {
			t.Errorf("the twin refusal reads as ErrNoEntry: %v", err)
		}
		// It names the pattern, so the person reading it knows which entry to
		// delete; the model is named too, because which call met the tie is what
		// an audit line carries.
		for _, want := range []string{`"claude-*"`, `"claude-sonnet-5-5"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the twin refusal %q does not name %s", err, want)
			}
		}
	})

	t.Run("the order the twins were written in decides nothing", func(t *testing.T) {
		if _, err := routing.Resolve(routing.Table{second, first, other}, "claude-sonnet-5-5"); err == nil {
			t.Error("the same two entries resolve when they are written the other way round: the refusal is the tie itself and not which one came first")
		}
	})

	t.Run("a more specific entry decides the call whatever order the twins are in", func(t *testing.T) {
		// The tie that refuses is the WINNER's, so twins under a pattern a more
		// specific entry beats decide nothing: refusing where the tie is found
		// would make the answer depend on where the twins were written, and
		// {claude-*, claude-*, claude-sonnet-*} would disagree with
		// {claude-sonnet-*, claude-*, claude-*} on one model.
		narrow := pooled("claude-sonnet-*")
		for _, table := range []routing.Table{
			{first, second, narrow},
			{narrow, first, second},
			{first, narrow, second},
		} {
			got, err := routing.Resolve(table, "claude-sonnet-5-5")
			if err != nil {
				t.Fatalf("a table of %d entries whose winner is not tied was refused: %v", len(table), err)
			}
			if want := pooled("claude-sonnet-*"); !reflect.DeepEqual(got, want) {
				t.Errorf("claude-sonnet-5-5 resolves to %+v, want %+v", got, want)
			}
		}
		// A model the narrow entry does not claim is the twins' own call, and
		// there the tie is the answer's however the three are ordered.
		for _, table := range []routing.Table{
			{first, second, narrow},
			{narrow, first, second},
		} {
			if got, err := routing.Resolve(table, "claude-haiku-4-5"); err == nil {
				t.Errorf("a model only the twins claim resolved to %+v beside a more specific entry", got)
			}
		}
	})

	t.Run("a model the twins do not claim is answered", func(t *testing.T) {
		// The refusal is about the model asked for: a tie over `claude-*` says
		// nothing about a `deepseek-*` call, and refusing that call too would be
		// the resolver judging the table at rest — RT3's checks' job, and a
		// refusal a run would meet for a model it can reach.
		got, err := routing.Resolve(routing.Table{first, second, other}, "deepseek-v4.1")
		if err != nil {
			t.Fatalf("resolving a model the twins do not claim: %v", err)
		}
		if want := pooled("deepseek-*"); !reflect.DeepEqual(got, want) {
			t.Errorf("deepseek-v4.1 resolves to %+v, want %+v", got, want)
		}
	})
}

// TestResolveNoEntry pins the fail-closed answer (R1, R22): a model no entry
// claims is refused, and it is never served by the first entry or by whatever
// credential is around. The empty table is the same answer, and it is the one
// case the contract's stub got right on purpose.
func TestResolveNoEntry(t *testing.T) {
	table := routing.Table{pooled("claude-*"), pooled("deepseek-*")}

	for _, tc := range []struct {
		what  string
		table routing.Table
		model string
	}{
		{"a table with entries and no match", table, "gemini-4-pro"},
		{"an empty table", nil, "claude-sonnet-5-5"},
		// A prefix claims what starts with it and nothing else: the model that
		// merely contains the pattern's prefix is not routed by it.
		{"a model that only looks like a match", table, "my-claude-proxy"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, err := routing.Resolve(tc.table, tc.model)
			if !errors.Is(err, routing.ErrNoEntry) {
				t.Fatalf("resolving %q: err = %v, want routing.ErrNoEntry", tc.model, err)
			}
			if !reflect.DeepEqual(got, routing.Entry{}) {
				t.Errorf("a model no entry claims answered %+v beside the refusal", got)
			}
			if !strings.Contains(err.Error(), tc.model) {
				t.Errorf("the refusal %q does not name the model it refused", err)
			}
		})
	}
}
