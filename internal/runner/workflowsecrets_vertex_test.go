package runner_test

// U1 of docs/secrets-brokering/image-secrets/reconciliation.md, settled where
// it says to settle it: a run of a repository on `agent.auth: vertex` holds a
// run identity, which is what the credentials service verifies a fetch by and
// what fetchWorkflowSecrets refuses without ("this run has no run identity").
//
// It matters because D3326 as amended by F17 hands a vertex repository the
// service's ADDRESS — the one auth the switch holds off and the address still
// reaches — and the address is there for one thing: the workflow secrets such
// a run fetches instead of mounting (F17, S3's flip). An identity a vertex run
// turned out not to hold would make that address a run that stops at
// bootstrap, so the assumption is settled on a whole run rather than reasoned
// about: the run below is a vertex job run that reaches its stages with a
// fetched value, which is only possible if the identity was there.
//
// The identity is the budget session's (always-a-budget's "every run has an
// identity"), taken before anything reads the auth: this test walks the
// bootstrap a cascade run walks, with a vertex document, and asserts the call
// the service received carried the run's identity header.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/runner"
)

// vertexSecretsRun is secretsRun's shape on the other auth: the fixture
// repository's job run whose fugaro.yaml says `agent.auth: vertex` (so the run
// reads no credential of the service's and takes no model route through it),
// whose job carries the credentials service's URL — which IS what D3326 hands
// this auth — and whose environment holds no value for the fixture's declared
// secret, so the only way to a value is the fetch.
//
// A vertex run's model calls are the agent's own, made on the job's Google
// credentials, so nothing here is pointed at the model upstream's stand-in: the
// stand-in below carries the workflow-secrets route alone, and the stages are
// scripted, so the run makes no model call through this harness at all.
func vertexSecretsRun(t *testing.T, svc *secretsService) *bk {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, "  first_line_review: off\n"), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg, "observe", "")
	g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
	g.deps.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.TEST", TokenType: "Bearer"})
	srv := httptest.NewServer(http.HandlerFunc(svc.serve))
	t.Cleanup(srv.Close)
	// The job's own Google ID token, which the service verifies a call by: the
	// metadata server's answer, from a stand-in.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)
	g.deps.Env = append(filterEnv(g.deps.Env, secretEnvVar),
		runner.CredentialsServiceURLEnv+"="+srv.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"),
		runner.SecretEnvsVar+"="+config.ReservedSecrets[config.GitHubAppKeySecret],
	)
	// A vertex run in a job is a job run: `fugaro exec` is local and never
	// fetches (D149), and the job a run is in is the platform's own report of
	// its execution — which the call must carry for the service to read it.
	g.deps.Local = false
	g.deps.Project = "aurora"
	g.deps.Execution = cloudRunExecution(fixtureJob())
	return attachBackend(t, g, "observe")
}

// TestVertexRunHasRunIdentity settles U1: a vertex repository's job run holds
// the run identity the service verifies a fetch by, so the address D3326 hands
// it is one its own fetch can be made over.
//
// It is written red against the refusal it names: with no run identity for this
// run, the fetch stops at `this run has no run identity` before any stage, and
// every assertion below fails on a run that never reached its first one.
func TestVertexRunHasRunIdentity(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := vertexSecretsRun(t, svc)

	var seen string
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen = envValue(req.Env, secretEnvVar)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if seen != fetchedSecretValue {
		t.Errorf("the agent's environment carries %s=%q, want the value the service answered: a vertex run's workflow secrets arrive through the fetch like any other's, and a value that never arrived is a run that stopped before its first stage", secretEnvVar, seen)
	}
	calls := svc.callsOf()
	if got := svc.askedNames(); len(got) != 1 || got[0] != secretName {
		t.Fatalf("the service was asked for %v, want exactly [%s]: the fetch is the one caller a vertex repository's address exists for (D3326, F17), and it asks the contract's version-2 question like every repository's (D3325)", got, secretName)
	}
	if want := `{"name":"` + secretName + `","version":2}`; calls[0].body != want {
		t.Errorf("the call carried %q, want the contract's version-2 body for %s", calls[0].body, secretName)
	}
	if got := calls[0].header.Get(gateway.HeaderRunIdentity); got == "" {
		t.Error("the call presents no run identity: a vertex run of a repository the launcher handed the address must hold the budget session's identity, which is what the service verifies the run by and what this run's fetch refuses without (U1)")
	}
	if got := calls[0].header.Get("Authorization"); got != "Bearer job-id-token-from-the-metadata-stand-in" {
		t.Errorf("the call's bearer is %q, want the job's own ID token", got)
	}
	if got := calls[0].header.Get(gateway.HeaderRunJob); got != fixtureJob() {
		t.Errorf("the call reports the job as %q, want %q", got, fixtureJob())
	}
	// And the half the auth does NOT take: a vertex run reads no credential of
	// the service's, so the call carries none of its own and the model path the
	// address does not touch stays exactly what it was (G7, F17).
	for _, hdr := range []string{"X-Api-Key", gateway.HeaderAuthMode} {
		if v := calls[0].header.Get(hdr); v != "" {
			t.Errorf("the call carries %s=%q, which a fetch neither has nor needs", hdr, v)
		}
	}
}
