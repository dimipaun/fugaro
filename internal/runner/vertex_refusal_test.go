package runner

// CS5: Vertex is no longer refused under `enforce` (docs/model-credentials-routing,
// implementation-plan.md). One test per clause of the task's acceptance:
//
//   - TestVertexInAChainIsAcceptedUnderEnforce — a run the project's routing
//     table serves carries the credentials service's address on its job, and
//     its Vertex calls are the service's, made on the service's own identity
//     and capped as a key's (D3174): neither VertexBudgetRefusal nor the
//     refusal of a pool with a Vertex member fires for it.
//   - TestLegacyVertexIsStillRefusedUnderEnforce — an unmigrated repository
//     whose file still says `auth: vertex` runs on the job's own identity, its
//     job carries no address, and the refusal stands.
//
// The CLI's halves of the same rule (validate, init) read the table itself,
// which a run has no document for: internal/cli's tests pin those, and
// servedByTheTable is where the fact comes from there.

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// vertexServiceEnv is the job's entry that says its installation serves this
// repository's model calls (credentialsServiceURLEnv). A `vertex` repository is
// handed it by the project's routing table alone: the release-1 bridge serves
// api-key and oauth (ServiceSwitchedAuth, infra.CredentialsServiceServes), so a
// vertex job that carries it is one a chain may serve.
func vertexServiceEnv() []string {
	return []string{credentialsServiceURLEnv + "=https://creds.example.run.app"}
}

// vertexPool is the pool D1626 refuses under enforce: a subscription beside the
// built-in Vertex credential, which no layer defines (config.CredentialVertex).
func vertexPool() config.Pool {
	return config.Pool{Name: "with-vertex", Members: []string{"acct-one", "vertex"}}
}

// TestVertexInAChainIsAcceptedUnderEnforce (CS5, D3174): a run whose Vertex
// calls are the credentials service's is no longer refused at bootstrap. The
// service calls Vertex on its own identity and the gateway prices and settles
// the call as it does a key's, so there is no uncapped path for either refusal
// to be about — the run goes on to the checks that judge every other run, and
// neither of the two refusals is one of them.
func TestVertexInAChainIsAcceptedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	// The vertex arm: the file still says `auth: vertex`, and the table's
	// chain is what makes the run's Vertex the service's.
	r, _ := poolRun(t, poolSpec{auth: "vertex", model: poolDirectModel,
		access: "with-vertex", pools: []config.Pool{vertexPool()}, creds: poolCreds("acct-one"),
		env: vertexServiceEnv()})
	if !r.vertexServedByTheService() {
		t.Fatal("a job carrying the credentials service's address is not read as served by it")
	}
	r.spend = Spend{Mode: "enforce"}
	if err := r.checkBudget(ctx); err != nil && strings.Contains(err.Error(), VertexBudgetRefusal) {
		t.Fatalf("a vertex run the credentials service serves was refused under enforce: %v", err)
	}
	// And the pool arm: the same run's pool lists the built-in Vertex
	// credential, whose member the service serves as it serves any other.
	if got := r.vertexPoolRefusal(ctx); got != "" {
		t.Fatalf("a pool listing the built-in vertex was refused for a run the service serves: %q", got)
	}
}

// TestLegacyVertexIsStillRefusedUnderEnforce (CS5, D1626): an unmigrated
// repository whose file still says `auth: vertex` runs on the job's own GCP
// identity — its chain is the file's, and no chain of the project's table may
// serve it — so nothing caps its Vertex calls and the refusal stands, at
// bootstrap and for a pool that lists the credential.
func TestLegacyVertexIsStillRefusedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	// The job of an unmigrated repository carries no address: the bridge
	// serves api-key and oauth alone, so a vertex repository is handed none.
	r, _ := poolRun(t, poolSpec{auth: "vertex", model: poolDirectModel})
	if r.vertexServedByTheService() {
		t.Fatal("a job with no credentials service address is read as served by it")
	}
	r.spend = Spend{Mode: "enforce"}
	err := r.checkBudget(ctx)
	if err == nil || !strings.Contains(err.Error(), VertexBudgetRefusal) {
		t.Fatalf("an unmigrated vertex run was not refused under enforce: %v", err)
	}
	// The pool's half, on a run whose auth is not vertex: its Vertex is the
	// job's own identity whatever the file says, so the pool is refused.
	r, _ = poolRun(t, poolSpec{access: "with-vertex", pools: []config.Pool{vertexPool()},
		creds: poolCreds("acct-one"), model: poolDirectModel})
	got := r.vertexPoolRefusal(ctx)
	if !strings.Contains(got, VertexBudgetRefusal) || !strings.Contains(got, "with-vertex") || !strings.Contains(got, "vertex") {
		t.Fatalf("a pool listing the built-in vertex was not refused for an unmigrated run: %q", got)
	}
}
