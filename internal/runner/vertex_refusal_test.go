package runner

// CS5: Vertex is no longer refused under `enforce` (docs/model-credentials-routing,
// implementation-plan.md). One test per clause of the task's acceptance:
//
//   - TestVertexInAChainIsAcceptedUnderEnforce — a run whose model path is the
//     credentials service's has its Vertex calls made by the service, on the
//     service's own identity and capped as a key's (D3174): neither
//     VertexBudgetRefusal nor the refusal of a pool with a Vertex member fires
//     for it.
//   - TestLegacyVertexIsStillRefusedUnderEnforce — a repository the table does
//     not serve, whose file still says `auth: vertex`, runs on the job's own GCP
//     identity, so both refusals stand.
//
// The fact both turn on is the run's model path (serviceModelPath) and never
// the auth alone: the launch's address on the job is what says the table serves
// this repository (B2.2/D2072), and CS6 put `vertex` in that set (D3174), so a
// `vertex` run carrying the address is served while one without it is not.
// What is left refusing is the CLI's half of the same rule (validate and
// update --repo's checkVertexBudget), which reads the local config rather than
// a run: it still refuses a `vertex` file outright, so a migrated vertex
// repository cannot be launched at all. That half is reported as a
// follow-up in the PR of this task rather than moved here, and internal/cli's
// TestTheVertexRefusalAgreesWithTheRunner holds the two answers on one fixture.

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// vertexServiceEnv is the job's entry that says the installation's credentials
// service is where this repository's model calls go
// (credentialsServiceURLEnv).
func vertexServiceEnv() []string {
	return []string{credentialsServiceURLEnv + "=https://creds.example.run.app"}
}

// vertexPool is the pool D1626 refuses under enforce: a subscription beside the
// built-in Vertex credential, which no layer defines (config.CredentialVertex).
func vertexPool() config.Pool {
	return config.Pool{Name: "with-vertex", Members: []string{"acct-one", "vertex"}}
}

// enforceSpend is the mode both clauses are judged under, with a cap so the
// api-key arm of checkBudget does not halt the served run for the reason that
// has nothing to do with Vertex.
func enforceSpend() Spend {
	return Spend{Mode: "enforce", Cap: 5}
}

// vertexRefused reports whether err is one of the two refusals D3174 lifts.
func vertexRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), VertexBudgetRefusal)
}

// TestVertexInAChainIsAcceptedUnderEnforce (CS5, D3174): a run the service
// serves — its model path is the credentials service's — is no longer refused
// for a chain that names Vertex. The service calls Vertex on its own identity
// and the gateway prices and settles the call as it does a key's, so there is
// no uncapped path for either refusal to be about: the run goes on to the
// checks that judge every other run, and neither of the two refusals is one of
// them.
func TestVertexInAChainIsAcceptedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	// The switch's arm, which is the served path this release has: a served
	// auth whose job carries the address, with a pool naming the credential.
	r, _ := poolRun(t, poolSpec{access: "with-vertex", pools: []config.Pool{vertexPool()},
		creds: poolCreds("acct-one"), model: poolDirectModel, env: vertexServiceEnv()})
	if !r.vertexServedByTheService() {
		t.Fatal("a run whose model path is the credentials service's is not read as served by it")
	}
	r.spend = enforceSpend()
	if err := r.checkBudget(ctx); vertexRefused(err) {
		t.Fatalf("a run the credentials service serves was refused for naming Vertex: %v", err)
	}
	if got := r.vertexPoolRefusal(ctx); got != "" {
		t.Fatalf("a pool listing the built-in vertex was refused for a run the service serves: %q", got)
	}
	// The `vertex` file's own arm, which reaches the same answer the other way:
	// a review class run's model path is the service's (modelAuth), so its
	// Vertex calls are the service's too, whatever its file says.
	r, _ = poolRun(t, poolSpec{auth: "vertex", access: "with-vertex", pools: []config.Pool{vertexPool()},
		creds: poolCreds("acct-one"), model: poolDirectModel, env: vertexServiceEnv()})
	r.spec.Mode = recipe.ModeReview
	r.job = jobTriple{Profile: "standard", Provider: "github", Account: "default-review", Class: classReview}
	if !r.vertexServedByTheService() {
		t.Fatal("a vertex file whose model path is the service's is not read as served by it")
	}
	r.spend = enforceSpend()
	if err := r.checkBudget(ctx); vertexRefused(err) {
		t.Fatalf("a vertex file whose model path is the service's was refused under enforce: %v", err)
	}
	if got := r.vertexPoolRefusal(ctx); got != "" {
		t.Fatalf("a pool listing the built-in vertex was refused for a run the service serves: %q", got)
	}
}

// TestLegacyVertexIsStillRefusedUnderEnforce (CS5, D1626): an unmigrated
// repository whose file still says `auth: vertex` runs on the job's own GCP
// identity — its chain is the file's, and no chain of the project's table may
// serve it — so nothing caps its Vertex calls and the refusal stands, at
// bootstrap and for a pool that lists the credential.
//
// The job's address is what separates the two: without it the launch served
// this repository nothing, and a `vertex` run authenticates to Vertex itself
// (gatewayUpstream's vertex arm), so the two refusals stand over exactly the
// uncapped path they exist for. With it the run is the service's, which is the
// path TestEveryStageGoesThroughTheChain (CS6) pins instead.
func TestLegacyVertexIsStillRefusedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	r, _ := poolRun(t, poolSpec{auth: "vertex", model: poolDirectModel})
	if r.serviceModelPath() || r.vertexServedByTheService() {
		t.Fatal("a vertex run whose job carries no service address is read as served by the service")
	}
	r.spend = enforceSpend()
	if err := r.checkBudget(ctx); !vertexRefused(err) {
		t.Fatalf("an unmigrated vertex run was not refused under enforce: %v", err)
	}
	// The pool's half: the built-in credential in a pool of the same run, whose
	// Vertex calls are the job's own.
	r, _ = poolRun(t, poolSpec{auth: "vertex", access: "with-vertex", pools: []config.Pool{vertexPool()},
		creds: poolCreds("acct-one"), model: poolDirectModel})
	r.spend = enforceSpend()
	got := r.vertexPoolRefusal(ctx)
	if !strings.Contains(got, VertexBudgetRefusal) || !strings.Contains(got, "with-vertex") || !strings.Contains(got, "vertex") {
		t.Fatalf("a pool listing the built-in vertex was not refused for an unmigrated run: %q", got)
	}
}
