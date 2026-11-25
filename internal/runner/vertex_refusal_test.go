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
// the auth alone: for the auths the release-1 bridge serves it is the launch's
// address on the job (B2.2/D2072), and `vertex` is the one CS6 added to that
// set (D3174) whose launch writes a second fact beside it — that the project's
// routing table reaches this repository (TableServesEnv) — because the address
// alone is handed to a documentless launch on a served profile whose table this
// repository is not in. Both are read (serviceSwitched), and a `vertex` run
// carrying the address and no statement is still the job's own identity.
// The CLI's half of the same rule (validate and update --repo's
// checkVertexBudget) reads the local config rather than a run, and it answers
// from the table itself: a repository the table serves is accepted and one it
// does not is refused exactly as before. internal/cli's
// TestTheVertexRefusalAgreesWithTheRunner holds the two answers on one fixture,
// which is why neither side can move alone.

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// vertexServiceEnv is the job entry that carries the installation's credentials
// service address (credentialsServiceURLEnv) and nothing else: what the bridge
// serves an api-key or oauth repository's run on, and — for a `vertex` file —
// the shape a documentless launch on a served profile produces, since the table
// states nothing about a repository it does not serve (TableServesEnv is the
// second entry, and the one this function deliberately leaves out).
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
// The table's own statement is what separates the two, and the address is not
// it: what the launch wrote where the table reaches this repository
// (TableServesEnv) is the served path TestEveryStageGoesThroughTheChain (CS6)
// pins, while a job carrying the address alone — the documentless launch on a
// served profile — is still a `vertex` run that authenticates to Vertex itself
// (gatewayUpstream's vertex arm), over exactly the uncapped path these refusals
// exist for.
func TestLegacyVertexIsStillRefusedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"a job with no address", nil},
		{"a job carrying the address, and no statement about the table", vertexServiceEnv()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := poolRun(t, poolSpec{auth: "vertex", model: poolDirectModel, env: tc.env})
			if r.serviceModelPath() || r.vertexServedByTheService() {
				t.Fatal("a vertex run whose model path is the job's own identity is read as served by the service")
			}
			r.spend = enforceSpend()
			if err := r.checkBudget(ctx); !vertexRefused(err) {
				t.Fatalf("an unmigrated vertex run was not refused under enforce: %v", err)
			}
			// The pool's half: the built-in credential in a pool of the same
			// run, whose Vertex calls are the job's own.
			r, _ = poolRun(t, poolSpec{auth: "vertex", access: "with-vertex", pools: []config.Pool{vertexPool()},
				creds: poolCreds("acct-one"), model: poolDirectModel, env: tc.env})
			r.spend = enforceSpend()
			got := r.vertexPoolRefusal(ctx)
			if !strings.Contains(got, VertexBudgetRefusal) || !strings.Contains(got, "with-vertex") || !strings.Contains(got, "vertex") {
				t.Fatalf("a pool listing the built-in vertex was not refused for an unmigrated run: %q", got)
			}
		})
	}
}
