package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRecordAgentModelTokensOnlyWithoutAGateway (G1.2, F8): the run's counts
// have one source, never two. A run with a gateway takes them from its
// ledger, at the run's last step (closeGateway); a stage's own report is the
// source only on a run with no gateway, and is not added on top of the
// ledger's on one that has.
//
// The rule is worth pinning because a gateway run's agent reports model usage
// too -- Claude Code's own account of the calls the gateway proxied -- so an
// ungated sum would put the same calls in the field twice over the run and
// leave a record written before the ledger is read (a run that dies mid-stage,
// the console's live view) carrying Claude Code's counts against the cache
// totals the gateway had not read yet. The mutation it fails on is the guard's
// removal.
func TestRecordAgentModelTokensOnlyWithoutAGateway(t *testing.T) {
	res := agent.Result{ModelUsage: map[string]agent.Usage{
		"claude-sonnet-5-5": {Input: 1000, CacheCreation: 700, CacheRead: 250, Output: 300},
	}}
	withGateway := svcRun(t, "api-key", func(r *run) { r.gw = &gateway.Server{} })
	withGateway.rec = &runstore.Record{Version: 1, RunID: svcRunID, Status: runstore.StatusRunning}
	withGateway.recordAgentModelTokens(res)
	withGateway.updateCost()
	if len(withGateway.modelTokens) != 0 {
		t.Fatalf("a gateway run's counts = %+v, want none from the agent's report: the ledger's sums are this run's counts", withGateway.modelTokens)
	}
	if withGateway.rec.Cost == nil || withGateway.rec.Cost.ModelTokens != nil {
		t.Fatalf("the record's model_tokens = %+v, want none on a gateway run: the ledger fills them, and it has not been read yet", withGateway.rec.Cost)
	}

	direct := svcRun(t, "oauth", nil)
	direct.recordAgentModelTokens(res)
	want := runstore.ModelTokenCounts{Input: 1000, Output: 300, CacheRead: 250, CacheWrite5m: 700}
	if got := direct.modelTokens["claude-sonnet-5-5"]; got != want {
		t.Fatalf("model_tokens[claude-sonnet-5-5] = %+v, want %+v: with no gateway the agent's report is the run's only source", got, want)
	}
}
