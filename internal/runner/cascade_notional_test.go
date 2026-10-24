package runner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// These are C1.16's runner half as a whole run: a cascade stage whose calls
// the service settled on two different credentials -- one on the run's
// subscription, one paid -- through the run's own stage bookkeeping
// (stageOnce's stage end, which is the only writer of a gateway stage's
// figures) and the record the run ends with. Nothing here drives the run's
// internals by hand: the run starts its own gateway, makes its own calls and
// writes its own record, so what the tests assert on is the wiring, not a
// fixture's idea of it.

// cascadeTierA is the usage the stand-in settles a subscription call by, and
// cascadeTierB the one it bills: two different figures, so a stage that mixed
// the two up shows.
var (
	cascadeTierA = pricing.Usage{Input: 100_000} // the plan's, not billed
	cascadeTierB = pricing.Usage{Output: 4_000}  // paid, $0.04 on sonnet
)

// cascadeService is the credentials service's stand-in: a fake upstream that
// attaches the tier and the credential name the answer carries, the way the
// service does (C1.15), with a switch on the calls it is asked for one.
type cascadeService struct {
	fake *anthropicfake.Fake
	// switchOn is the 1-based call number whose answer carries a switch, 0
	// for none.
	switchOn int
	mu       sync.Mutex
	calls    int
}

func (c *cascadeService) next() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.calls
}

// count is how many calls the stand-in has answered.
func (c *cascadeService) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// newCascade is newGW's cascade twin: the same harness and the same fake
// upstream, but the run's gateway sends its calls to the stand-in above
// instead of straight to the upstream, because that is what a cascade run
// does (C1.15, D143). The upstream is the stand-in's, and the job's own
// environment carries no credential at all.
func newCascade(t *testing.T, cfg string, svc *cascadeService) *bk {
	t.Helper()
	h := newHarnessFiles(t, cfg, nil, nil)
	fake, up := anthropicfake.New(t)
	fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		n := svc.next()
		h := http.Header{
			gateway.HeaderTier:       []string{gateway.TierA},
			gateway.HeaderCredential: []string{"repo-shared-a"},
		}
		usage := cascadeTierA
		if n > 1 {
			// The second call of a stage is the one the service pays for, and
			// the one a switch can be about: a first call has no cache behind
			// it for a miss to be estimated from (F24).
			h.Set(gateway.HeaderTier, gateway.TierB)
			h.Set(gateway.HeaderCredential, "acme-key-b")
			usage = cascadeTierB
			if svc.switchOn == n {
				h.Set(gateway.HeaderSwitch, `{"from":"repo-shared-a","to":"acme-key-b","reason":"exhausted"}`)
			}
		}
		rep := anthropicfake.MessageOK(sonnet, usage)
		rep.Header = h
		return rep
	}
	svc.fake = fake
	// The job's Google ID token, which the service verifies the call by: the
	// metadata server's answer, from a stand-in.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)

	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"),
		runner.CredentialsServiceURLEnv+"="+up.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"))
	mode := map[string]string{runner.BudgetModeEnv: "observe"}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := mode[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend = spend
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	// A cascade run is a job run: `fugaro exec` never cascades (D149), and
	// the job a run is in is the platform's own report of its execution.
	h.deps.Local = false
	h.deps.Project = "aurora"
	h.deps.Execution = cloudRunExecution(gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")))
	g := &gw{harness: h, fake: fake, logs: logs}
	// The switch is what makes the calls go through the service at all (O2):
	// without it the run does not start.
	b := publishPolicy(t, g, fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":"enforce","allowed_models":["claude-haiku-4-5","claude-sonnet-5-5"]}}`, projectpolicy.Version))
	return b
}

// cascadeConfig is the fixture's config with the agent on the cascade.
func cascadeConfig(t *testing.T) string {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: cascade", 1)
	if cfg == gwConfig(t, "") {
		t.Fatal("the fixture's agent.auth line moved: this is not switching it to cascade")
	}
	return cfg
}

// ratesOf is the price table's row for the model the fixture pins, the same
// table the gateway charges from.
func ratesOf(t *testing.T, model string) pricing.Rates {
	t.Helper()
	m, ok := pricing.Embedded().Lookup(model)
	if !ok {
		t.Fatalf("the built-in table has no %s", model)
	}
	return m.Rates
}

// cents is a USD figure rounded the way the record rounds a total.
func cents(v float64) float64 { return math.Round(v*100) / 100 }

// crossChecked is the gateway figure the run's cost cross-check named for a
// stage, and whether it warned at all.
func crossChecked(t *testing.T, logs, stage string) (float64, bool) {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "cost cross-check") || !strings.Contains(line, "stage="+stage) {
			continue
		}
		_, rest, ok := strings.Cut(line, "gateway_usd=")
		if !ok {
			t.Fatalf("the cross-check's line names no gateway figure: %q", line)
		}
		v, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
		if err != nil {
			t.Fatalf("the cross-check's line carries %q: %v", line, err)
		}
		return v, true
	}
	return 0, false
}

// TestCascadeMixedStageSplitsMoneyFromNotional is C1.16's accounting in a
// whole run (D140, P12, F25): one stage makes two calls, the service settles
// the first on the run's subscription and bills the second, and the record
// tells the two apart exactly where the plan puts them.
//
//	model_usd        the stage's and the run's whole model figure: the money
//	                 and the notional part together, which is what keeps
//	                 sum(stages[].model_usd) + model_by["unattributed"]
//	                 == cost.model_usd true, notional dollars included;
//	model_notional_usd  the subscription's part of it, stage by stage;
//	total_usd        billed dollars: the money part and the compute, and none
//	                 of the notional;
//	switches         the service's own record of the move, with the gateway's
//	                 cache-miss estimate beside it.
//
// The stage end is stageOnce's own -- the run makes the calls and closes the
// stage itself -- so this is what fails if the runner stops taking a stage's
// settled figures into the record (or takes only the billed half of them).
func TestCascadeMixedStageSplitsMoneyFromNotional(t *testing.T) {
	r := ratesOf(t, sonnet)
	notional := r.Cost(cascadeTierA).USD()
	money := r.Cost(cascadeTierB).USD()

	svc := &cascadeService{switchOn: 2}
	b := newCascade(t, cascadeConfig(t), svc)
	var statuses []int
	rec, err := b.run(t,
		calling(&statuses, implement("feature"), call{sonnet, 4096}, call{sonnet, 4_000}),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(statuses) != 2 || statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Fatalf("the stage's calls were answered %v, want two served calls", statuses)
	}
	if rec.Cost == nil || len(rec.Stages) == 0 {
		t.Fatalf("rec = %+v", rec)
	}

	// The stage that made the calls: its whole figure is both halves, and its
	// own statement about the notional part is the subscription's call alone.
	st := rec.Stages[0]
	if !near(st.ModelUSD, notional+money) || !near(st.ModelNotionalUSD, notional) {
		t.Fatalf("the implement stage is %+v, want model_usd %v with %v of it notional", st, notional+money, notional)
	}
	// The review stage called no model: nothing, not a share of the stage
	// before it.
	if len(rec.Stages) < 2 || !near(rec.Stages[1].ModelUSD, 0) || !near(rec.Stages[1].ModelNotionalUSD, 0) {
		t.Fatalf("the stages are %+v", rec.Stages)
	}

	c := *rec.Cost
	if !near(c.ModelUSD, notional+money) || !near(c.ModelNotionalUSD, notional) {
		t.Fatalf("cost = %+v, want model_usd %v with %v of it notional", c, notional+money, notional)
	}
	// Billed dollars: the money part, and not one notional dollar of it.
	if want := cents(money + c.ComputeUSD); !near(c.TotalUSD, want) {
		t.Fatalf("total_usd = %v, want the billed dollars %v (money %v + compute %v)", c.TotalUSD, want, money, c.ComputeUSD)
	}
	// The invariant C1.16 documents, with the notional included: the stages
	// and the split measure the same whole figure.
	sum := stageMicros(t, rec.Stages)
	if rest := micros(t, c.ModelUSD) - sum - micros(t, c.ModelBy["unattributed"]); rest != 0 {
		t.Fatalf("sum(stages) = %v and model_by[unattributed] = %v against model_usd %v: %v unaccounted for", sum, c.ModelBy["unattributed"], c.ModelUSD, rest)
	}

	// The run's own log of what the service did, beside the estimate the
	// gateway made for that move.
	if len(rec.Switches) != 1 {
		t.Fatalf("switches = %+v, want the switch the second call's answer carried", rec.Switches)
	}
	sw := rec.Switches[0]
	if sw.From != "repo-shared-a" || sw.To != "acme-key-b" || sw.Reason != "exhausted" || sw.CacheMissUnmeasured {
		t.Fatalf("the switch is %+v", sw)
	}
	if rec.OAuthAccount != "acme-key-b" {
		t.Fatalf("oauth_account = %q, want the credential the last call was served with", rec.OAuthAccount)
	}
	// The stage's own end is where the cross-check runs (recordGatewayStageCost,
	// which stageOnce calls): Claude Code reported $1 for a stage the gateway
	// settled at the whole figure above, and the warning names that figure --
	// money and notional part together, because that is what Claude Code's own
	// total_cost_usd counts (F25). Compared against the billed part alone, the
	// warning would name $%v and read as a 75%% disagreement that is not one.
	if v, warned := crossChecked(t, b.logs.String(), "implement"); !warned || !near(v, notional+money) {
		t.Fatalf("the cost cross-check named %v (warned=%v), want the whole model figure %v", v, warned, notional+money)
	}
	// Every call went to the service, and the job held no credential to make
	// one with: the tier-A answer's whole point (D143).
	if svc.count() != 2 || b.fake.Count() != 2 {
		t.Fatalf("the service answered %d calls of the upstream's %d", svc.count(), b.fake.Count())
	}
}

// TestCascadeStageCostIsTheGatewaysNotClaudeCodes is the same wiring from the
// other side: what the agent reports for its stage never reaches the record,
// while the gateway's settled figures do -- the stage's, the run's and the
// stored record's.
func TestCascadeStageCostIsTheGatewaysNotClaudeCodes(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	var statuses []int
	rec, err := b.run(t, calling(&statuses, implement("feature"), call{sonnet, 4096}, call{sonnet, 4_000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// implement reports $1 and review $0.5 of its own: neither counts.
	if rec.Cost == nil || rec.Cost.ModelSource != "gateway" || near(rec.Cost.ModelUSD, 1.5) {
		t.Fatalf("cost = %+v", rec.Cost)
	}
	if !near(rec.Cost.ModelUSD, rec.Stages[0].ModelUSD) {
		t.Fatalf("the run's model figure is %v, the implement stage's %v", rec.Cost.ModelUSD, rec.Stages[0].ModelUSD)
	}
	stored, err := b.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || !near(stored.Cost.ModelUSD, rec.Cost.ModelUSD) ||
		!near(stored.Cost.ModelNotionalUSD, rec.Cost.ModelNotionalUSD) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}
