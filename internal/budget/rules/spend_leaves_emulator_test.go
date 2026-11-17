//go:build firebase

package rules

// The day-counter leaves end to end (spend-accuracy task SA5.4). One run's
// session -- the writer SA5.1 built -- is driven against the emulator's real
// security rules from its stage reports to the counters a reader decodes, so
// what is proved is the chain the pieces above only prove one link at a time:
// the session writes exactly the leaves its installation's document grants,
// the rules take every write a report is made of (a stage report is one atomic
// multi-path update, so one leaf the document lacks refuses it whole), and what
// lands reads back as `budget.ModelUse` with the four spend leaves and the
// presence signal SA5.3 and SA5.7 read.
//
// The two halves are one session each, over the two documents a run really
// meets: the one this binary deploys, which grants the leaves, and the frozen
// generation the release behind deployed (`testdata/rules.generation6.json`,
// SA5.0), which does not. The leaves arrived in generation 7 and not the plan's
// 6, because 6 is the mailbox registration kind already deployed on every
// updated installation (`docs/spend-accuracy/day-leaves.md`).

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const (
	// spendLeafSlug is the repository the session below runs for, and
	// spendLeafModel the model every report in it is about, under a name the
	// rules' `$model` wildcard takes and ModelKey leaves alone.
	spendLeafSlug  = "aurora-app"
	spendLeafModel = "claude-sonnet-5-5"
)

// todaysLeaves are the model's leaves every deployed document carries, and
// newLeaves the four generation 7 added beside them.
var todaysLeaves = []string{"micros", "in", "out", "cr", "cw"}
var newLeaves = []string{"notional", "rep", "tlo", "thi"}

// TestSpendLeavesEndToEnd (SA5.4, F10, RS8): the same session's two reports --
// a gateway stage's, which carries the charge, the tokens, the tier-A part of
// it, the providers' report and the band over the same calls, and an oauth
// stage's notional spend on the same model -- against the document this release
// deploys and the one the release behind did. Under the first every leaf lands
// and the day counter decodes into a `ModelUse` carrying them; under the second
// exactly today's leaves land, nothing at all is refused, and the same read
// decodes to what a day written before the leaves looks like (RS8's absence,
// which a reader takes as "not recorded").
func TestSpendLeavesEndToEnd(t *testing.T) {
	previous, err := os.ReadFile("testdata/rules.generation6.json")
	if err != nil {
		t.Fatalf("reading the generation-6 fixture: %v", err)
	}
	current, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		doc    []byte
		gen    int
		leaves []string
	}{
		{"generation 7, the document this binary deploys", current, 7, concat(todaysLeaves, newLeaves)},
		{"generation 6, the document the release behind deployed", previous, 6, todaysLeaves},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLeaseRig(t)
			// The database is moved to the document under test exactly as
			// `fugaro update` moves one (deployRules): the same namespace, the
			// generation's own document, the same session's writes asserted
			// against both.
			r.h.deployRules(tc.doc)
			r.caps(spendLeafSlug, 60_000_000, 20_000_000)
			ctx := context.Background()
			day := budget.Day(time.Now())
			// The session is told its installation's generation, as the runner
			// reads it off the database before it opens (F27): a session under
			// the older document must not write a leaf that document does not
			// grant. Any halt -- the rig's OnHalt fails the test -- or any
			// error below is the rules refusing a report whole.
			s := r.session(t, spendLeafSlug, "20261117-060000-ab12", func(c *budget.Config) { c.RulesGeneration = tc.gen })
			if h, err := s.Admit(ctx, budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
				t.Fatalf("admit: %+v, %v", h, err)
			}
			if err := s.Start(ctx, budget.AgentEntry{Repo: "acme/app", Auth: "api-key"}); err != nil {
				t.Fatalf("the rules refused the registry entry: %v", err)
			}
			defer s.Finish(ctx, "succeeded")
			if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
				t.Fatalf("the rules refused a lease: %v", err)
			}
			s.SetUsage(func() budget.Micros { return 650_000 })

			rep := gateway.StageReport{
				Calls: 3, Used: 300_000, Tokens: 1_840,
				ByModel:    map[string]pricing.Micros{spendLeafModel: 300_000},
				TokensBy:   map[string]pricing.Usage{spendLeafModel: {Input: 1_000, Output: 500, CacheRead: 200, CacheWrite5m: 100, CacheWrite1h: 40}},
				NotionalBy: map[string]pricing.Micros{spendLeafModel: 100_000},
				ReportedBy: map[string]pricing.Micros{spendLeafModel: 180_000},
				TableLoBy:  map[string]pricing.Micros{spendLeafModel: 150_000},
				TableHiBy:  map[string]pricing.Micros{spendLeafModel: 260_000},
			}
			if err := s.Lease().Report(ctx, rep); err != nil {
				t.Fatalf("the rules refused the gateway stage's report: %v", err)
			}
			// An oauth stage's notional spend on the same model, which the
			// older document takes as the whole of the model's dollars and the
			// newer one as their notional part beside it (RS8).
			if err := s.AddNotional(ctx, 350_000, map[string]budget.ModelUse{
				spendLeafModel: {Micros: 350_000, In: 10, Out: 5, CR: 2, CW: 1},
			}); err != nil {
				t.Fatalf("the rules refused the notional stage's spend: %v", err)
			}

			// The node itself: exactly the granted leaves and nothing beside
			// them. A leaf the document does not grant would have refused the
			// whole update above, so this pins the other direction -- that the
			// four are what a report adds, and no more.
			var node map[string]json.RawMessage
			if _, err := r.h.admin.Get(ctx, budget.PathByModel(day, spendLeafSlug, spendLeafModel), &node); err != nil {
				t.Fatalf("reading the model's day counter: %v", err)
			}
			if len(node) != len(tc.leaves) {
				t.Fatalf("the day counter is %v, want exactly the leaves %v", keysOf(node), tc.leaves)
			}
			for _, leaf := range tc.leaves {
				if _, ok := node[leaf]; !ok {
					t.Fatalf("the day counter is %v, missing %s", keysOf(node), leaf)
				}
			}

			// And the counters read back the way a reader takes them: the
			// repository's day counter decoded into `budget.ModelUse`, the shape
			// SA5.2, SA5.3 and SA5.7 read.
			var counters budget.Counters
			if _, err := r.h.admin.Get(ctx, budget.PathSpendRepo(day, spendLeafSlug), &counters); err != nil {
				t.Fatalf("reading the repository's day counter: %v", err)
			}
			got, ok := counters.Models()[spendLeafModel]
			if !ok {
				t.Fatalf("the day counter has no row for %s: %v", spendLeafModel, counters.ByModel)
			}
			// micros is the whole of the model's dollars in both generations --
			// the gateway's charge and the oauth stage's figure together -- and
			// the four tokens are the two stages' shares (F25).
			want := budget.ModelUse{Micros: 650_000, In: 1_010, Out: 505, CR: 202, CW: 141}
			if tc.gen >= 7 {
				// The leaves are here: `notional` is the tier-A part of the
				// gateway stage's charge plus the whole of the oauth stage's,
				// and the presence signal is what tells a day carrying them
				// from one written before the leaf existed (RS8).
				want.Notional, want.NotionalSet = 450_000, true
				want.Rep, want.TLo, want.THi = 180_000, 150_000, 260_000
			}
			if got != want {
				t.Fatalf("the model's row reads %+v, want %+v", got, want)
			}
		})
	}
}

func concat(a, b []string) []string { return append(append([]string{}, a...), b...) }

func keysOf(node map[string]json.RawMessage) []string {
	out := make([]string, 0, len(node))
	for k := range node {
		out = append(out, k)
	}
	return out
}
