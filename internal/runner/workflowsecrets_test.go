package runner_test

// B3.4's runner half as whole runs (S3, D491, D1267, D2073-D2075, F4, D143): a
// declared workflow secret whose variable the job does not carry is fetched
// from the installation's credentials service at bootstrap, and everything
// worth pinning about it -- that a mounted value wins, that the value is
// redacted everywhere the run publishes, that a review run's never reaches its
// agent, and that a service which cannot serve the call stops the run before
// any stage -- is asserted on the run that results rather than on the helper
// that does the work.
//
// The runs here are the shape a repository on the switch really has (B2.2): a
// job run (Local false, the platform's own execution of this repository's job,
// the project's policy and the budget backend's fakes) whose job carries the
// credentials service's URL, so its model path is the service and its declared
// secrets are fetched from it. The service itself is a stand-in: the
// workflow-secrets route answered from a table, and every other path relayed to
// the model upstream's own stand-in, exactly as the real service carries both.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The fixture's workflow declares one secret (testdata/fixture-repo's
// fugaro.yaml), and its test command reads the variable as the path of a file
// listing the tests that fail: a value naming no file is a value the fixture's
// tests pass with, which is what makes one usable as a secret here.
const secretEnvVar = "FIXTURE_FAILS_FILE"

// secretName is that secret's LOGICAL name, the spelling version 2 asks by
// (config.Secret.Name, D3333): the call names this and no variable.
const secretName = "fixture-fails"

// fetchedSecretValue is what the stand-in service answers for secretEnvVar, and
// mountedSecretValue what a job that still mounts it carries: two different
// values, so a run that took the wrong one shows.
const (
	fetchedSecretValue = "fetched-workflow-secret-9c1e"
	mountedSecretValue = "mounted-workflow-secret-4a20"
)

// otherSecretName is a secret that is not the one a call asks for: what a
// service answers when the answer belongs to another secret, which the run may
// not read as the one it asked for.
const otherSecretName = "some-other-secret"

// shortSecretValue is under the floor a value must clear before a run may
// register it for redaction: a service that answered it is refused rather than
// registered, because a value that short would rewrite unrelated text wherever
// this run publishes.
const shortSecretValue = "abc"

// minFetchedSecretLen is that floor, which the refusal names. It is spelled as
// a literal because the runner's own constant is unexported and these cases run
// from outside the package.
const minFetchedSecretLen = 4

// secretsCall is one call the stand-in service received, kept whole: what the
// call asked for -- the variable (version 1) or the logical name (version 2) --
// and what it presented, which is what a test asserts the fetch is signed by.
type secretsCall struct {
	env     string
	name    string
	version int
	header  http.Header
	body    string
}

// secretsService is the credentials service's stand-in: the workflow-secrets
// route (B3.2's contract) answered from names, version 2 by logical name and
// version 1 by variable, and every other path relayed to upstream, the model
// upstream's own stand-in -- the real service carries a run's model calls too,
// and a stand-in that refused them would turn a stray call into a mystery.
//
// The two served cases are C1's own stub (credsvc.WorkflowSecretsStub), so the
// shape, the echo and version 2's "not in the record" refusal are held to one
// implementation rather than to two readers agreeing with a comment (the
// plan's stub, which I1 deletes when the route itself lands).
//
// status, body and malformed are the three ways a call is not served: a typed
// refusal with status, a 200 whose body is not this contract's, and (with no
// value under what the call asked for) the refusal the stub writes for a
// record that holds nothing there. answerAs is the fourth, the one the contract
// itself forbids: a 200 that names some other secret than the one the call
// asked for.
type secretsService struct {
	mu        sync.Mutex
	names     map[string]string
	envs      map[string]string
	calls     []secretsCall
	status    int
	body      string
	malformed string
	answerAs  string
	upstream  string
}

func (s *secretsService) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req credsvc.WorkflowSecretsRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, secretsCall{
		env: req.Env, name: req.Name, version: req.VersionOf(), header: r.Header.Clone(), body: string(raw),
	})
	status, refusal, malformed, answerAs := s.status, s.body, s.malformed, s.answerAs
	stub := credsvc.WorkflowSecretsStub{Values: s.names, Envs: s.envs}
	s.mu.Unlock()
	switch {
	case malformed != "":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, malformed)
	case status != 0:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, refusal)
	case answerAs != "":
		var resp credsvc.WorkflowSecretsResponse
		if req.VersionOf() == credsvc.WorkflowSecretsVersion2 {
			resp = credsvc.WorkflowSecretsResponse{Name: answerAs, Value: s.names[req.Name]}
		} else {
			resp = credsvc.WorkflowSecretsResponse{Env: answerAs, Value: s.envs[req.Env]}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	default:
		// The stub reads the body it is handed, and the call above has already
		// read it: it is handed the same bytes again.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		stub.ServeHTTP(w, r)
	}
}

// relay carries a call that is not the workflow-secrets route to the model
// upstream's stand-in, the way the service carries a run's model calls.
func (s *secretsService) relay(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	upstream := s.upstream
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// asked is the variable of every version-1 call the service received, in order.
func (s *secretsService) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.env)
	}
	return out
}

// askedNames is the logical name of every call the service received, in order:
// the version-2 question, and "" for a version-1 one.
func (s *secretsService) askedNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.name)
	}
	return out
}

// callsOf is every call the service received.
func (s *secretsService) callsOf() []secretsCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]secretsCall(nil), s.calls...)
}

// secretsConfig is the fixture's configuration on the switch: its agent on the
// cascade (as cascadeConfig does), with extra lines under `agent:`. A run
// reaches a credentials service only when its auth is the cascade one (C1.15,
// D143), which is the state B3.4's fetch exists for.
func secretsConfig(t *testing.T, extra string) string {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, extra), "auth: api-key", "auth: cascade", 1)
	if cfg == gwConfig(t, extra) {
		t.Fatal("the fixture's agent.auth line moved: this is not switching it to cascade")
	}
	return cfg
}

// otherFetchedValue is what the service answers for the second declared secret
// twoSecretsConfig adds: two values, so an answer read as the other secret's
// shows.
const otherFetchedValue = "fetched-marker-secret-77b3"

// twoSecretsConfig is the fixture's configuration with a second declared
// secret: a run that declares more than one is asked about each of them, by its
// own name.
func twoSecretsConfig(t *testing.T) string {
	t.Helper()
	const declared = "      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }"
	cfg := secretsConfig(t, "  first_line_review: off\n")
	out := strings.Replace(cfg, declared,
		declared+"\n      - { name: fixture-marker, env: FIXTURE_MARKER }", 1)
	if out == cfg {
		t.Fatal("the fixture's declared secret moved: this is not adding a second one")
	}
	return out
}

// fileSecretsConfig is the fixture's configuration with its declared secret
// exposed as a file (config.Secret.File, D3327): the variable is the path the
// value was written to rather than the value.
func fileSecretsConfig(t *testing.T, extra string) string {
	t.Helper()
	const (
		declared = "      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }"
		asAFile  = "      - { name: fixture-fails, env: FIXTURE_FAILS_FILE, file: true }"
	)
	cfg := secretsConfig(t, extra)
	out := strings.Replace(cfg, declared, asAFile, 1)
	if out == cfg {
		t.Fatal("the fixture's declared secret moved: this is not exposing it as a file")
	}
	return out
}

// fileSecretsRun is secretsRun for a repository whose secret is exposed as a
// file.
func fileSecretsRun(t *testing.T, extra string, spec *task.Spec, svc *secretsService) *bk {
	t.Helper()
	return secretsRunConfig(t, fileSecretsConfig(t, extra), spec, svc)
}

// secretsRun is the whole run these tests are about: the fixture repository's
// job run whose environment carries the credentials service's URL (so the run
// is on the switch and its model path is the service, B2.2), its own account of
// the secrets it mounts — which does not name the fixture's declared secret, the
// shape the flip leaves a switched job in — and no mounted value for that
// secret. extra is inserted under the fixture's `agent:` block, spec the task
// (nil for the fixture's own) and svc the service's stand-in.
//
// The list is what makes every fetch case here a case about it too: the
// fixture's own secret is left out of it, so a reader that took it for
// everything the run needs drops the declared name, and the run fails on the
// empty variable where the fetch is what answers for it.
func secretsRun(t *testing.T, extra string, spec *task.Spec, svc *secretsService) *bk {
	t.Helper()
	return secretsRunConfig(t, secretsConfig(t, extra), spec, svc)
}

// secretsRunConfig is secretsRun with the configuration handed in whole, for a
// case whose declaration is not the fixture's own (a secret exposed as a file,
// a second declared secret).
func secretsRunConfig(t *testing.T, cfg string, spec *task.Spec, svc *secretsService) *bk {
	t.Helper()
	h := newHarnessFiles(t, cfg, spec, nil)
	fake, up := anthropicfake.New(t)
	fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 1000, Output: 3})
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierA},
			gateway.HeaderCredential: []string{"repo-shared-a"},
		}
		return rep
	}
	svc.upstream = up.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == credsvc.WorkflowSecretsPath {
			svc.serve(w, r)
			return
		}
		svc.relay(w, r)
	}))
	t.Cleanup(srv.Close)
	// The job's own Google ID token, which the service verifies a call by: the
	// metadata server's answer, from a stand-in.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)

	// The job carries no mounted value for the fixture's secret: that is the
	// state S3's flip leaves a switched repository's job in, and the one the
	// fetch exists for.
	//
	// Its own account of its mounts is the whole of that state, and it is the
	// account every switched job has: the list is present and names a variable
	// the job does carry — the git credential a flip keeps mounting — and the
	// workflow's own secret is not in it, because the flip mounts none of a
	// workflow's secrets: FUGARO_SECRET_ENVS stops being the complete list of
	// what the run needs and the fetch answers for the rest (B3.8). A reader
	// that took it for that list drops this secret and never asks.
	h.deps.Env = append(filterEnv(h.deps.Env, secretEnvVar),
		runner.CredentialsServiceURLEnv+"="+srv.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"),
		runner.SecretEnvsVar+"="+config.ReservedSecrets[config.GitHubAppKeySecret])
	mode := map[string]string{runner.BudgetModeEnv: "observe"}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := mode[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	h.deps.Spend = spend
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	// A switched run is a job run: `fugaro exec` never cascades (D149), and the
	// job a run is in is the platform's own report of its execution.
	h.deps.Local = false
	h.deps.Project = "aurora"
	h.deps.Execution = cloudRunExecution(fixtureJob())
	g := &gw{harness: h, fake: fake, logs: logs}
	// The switch is what makes the calls go through the service at all (O2):
	// without it the run does not start.
	b := publishPolicy(t, g, fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":"enforce","allowed_models":["claude-haiku-4-5","claude-sonnet-5-5"]}}`, projectpolicy.Version))
	return b
}

// fixtureJob is the job name the fixture's runs are in.
func fixtureJob() string { return gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")) }

// imageEntryName is the catalog entry an image-env case's job is stamped with,
// the way the flip's derived image stamps one (docs/image-env, D1370).
const imageEntryName = "fixture-entry"

// TestBootstrapFetchesMissingWorkflowSecretsFromTheService is B3.4's red step:
// a run whose job does not carry a declared secret's value fails today with
// "secret FIXTURE_FAILS_FILE is not set in the runner environment", and with
// the fetch it starts and runs with the value in the environment its stages
// run in.
//
// What is asserted is the whole of it: the run reaches its stages and ends
// ready, the agent's own environment carries the fetched value, the service was
// asked once -- for the declared secret's logical name, in the contract's
// version-2 shape -- and the call it was asked with is signed the way every
// call to the service is -- the job's own ID token as the bearer, the run's
// identity, the job's name -- and carries no credential of the job's own
// (D143).
//
// Hand mutation this fails on: dropping the call from bootstrap (the run stops
// on BuildEnv's refusal), or fetching a variable the job already carries.
func TestBootstrapFetchesMissingWorkflowSecretsFromTheService(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)

	var seen string
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen = envValue(req.Env, secretEnvVar)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if seen != fetchedSecretValue {
		t.Errorf("the agent's environment carries %s=%q, want the value the service answered", secretEnvVar, seen)
	}
	if got := svc.askedNames(); len(got) != 1 || got[0] != secretName {
		t.Fatalf("the service was asked for %v, want exactly %s", got, secretName)
	}
	call := svc.callsOf()[0]
	if want := `{"name":"` + secretName + `","version":2}`; call.body != want {
		t.Errorf("the call carried %q, want the contract's version-2 body for %s", call.body, secretName)
	}
	if call.version != credsvc.WorkflowSecretsVersion2 {
		t.Errorf("the call declares version %d, want version %d", call.version, credsvc.WorkflowSecretsVersion2)
	}
	if call.env != "" {
		t.Errorf("the call carries env=%q, which version 2 has no field for", call.env)
	}
	if got := call.header.Get("Authorization"); got != "Bearer job-id-token-from-the-metadata-stand-in" {
		t.Errorf("the call's bearer is %q, want the job's own ID token", got)
	}
	if call.header.Get(gateway.HeaderRunIdentity) == "" {
		t.Error("the call presents no run identity, which is what the service verifies the run by")
	}
	if got := call.header.Get(gateway.HeaderRunJob); got != fixtureJob() {
		t.Errorf("the call reports the job as %q, want %q", got, fixtureJob())
	}
	for _, hdr := range []string{"X-Api-Key", gateway.HeaderAuthMode} {
		if v := call.header.Get(hdr); v != "" {
			t.Errorf("the call carries %s=%q, which a fetch neither has nor needs", hdr, v)
		}
	}
}

// TestBootstrapPrefersTheMountedValue: a variable the job's own environment
// carries is never fetched. The value the job mounted is the one the run's
// stages see and the service is not asked at all, which is what makes S3's flip
// a no-op for a repository whose job still mounts its secrets.
//
// Hand mutation this fails on: fetching whenever the service URL is set (the
// service is asked, and its answer would have to win over the mount), or
// letting an answer overwrite a mounted value.
func TestBootstrapPrefersTheMountedValue(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)
	// The mount is the environment entry the deploy leaves, which is the file
	// the fixture's own tests read.
	b.deps.Env = append(b.deps.Env, secretEnvVar+"="+mountedSecretValue)

	var seen string
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen = envValue(req.Env, secretEnvVar)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if seen != mountedSecretValue {
		t.Errorf("the agent's environment carries %s=%q, want the mounted value", secretEnvVar, seen)
	}
	if got := svc.asked(); len(got) != 0 {
		t.Errorf("the service was asked for %v, want nothing: the job mounts that value", got)
	}
}

// TestMountedSecretsAreRedacted: a declared secret the job itself mounts is a
// credential of the repository's like any other, and the run publishes none of
// it either. Nothing registers a mounted value on the runner's side -- the
// fetch never sees it, and FUGARO_SECRET_ENVS names the credentials the runner
// itself uses, not the workflow's declared secrets -- so what redacts it is the
// value agent.BuildEnv hands back for a DECLARED non-file secret, and only that.
//
// Hand mutation this fails on: broadening fileSecretEnvs to every declared
// secret (runner.go's bootstrap), which sends a mounted declared secret down
// the file arm and leaves its value registered with nobody -- it is then
// published in full wherever the stage echoes it.
func TestMountedSecretsAreRedacted(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)
	b.deps.Env = append(b.deps.Env, secretEnvVar+"="+mountedSecretValue)

	leaky := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = fmt.Fprintf(req.Transcript, "{\"leaked\":%q}\n", mountedSecretValue)
		_, _ = fmt.Fprintln(req.Stderr, "claude: "+mountedSecretValue)
		res, err := implement("feature")(t, ctx, req)
		res.Text = "done, the file is " + mountedSecretValue
		return res, err
	}
	rec, err := b.run(t, leaky, review("ship", 0))
	mustReady(t, rec, err)

	logs := b.logs.String()
	if strings.Contains(logs, mountedSecretValue) {
		t.Fatalf("the runner's log publishes the mounted secret:\n%s", logs)
	}
	if !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("nothing was redacted, so this test proves nothing:\n%s", logs)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(mountedSecretValue)) {
		t.Fatalf("the record holds the mounted secret: %s", got)
	}
	if keys := objectsContaining(t, b.harness, mountedSecretValue); len(keys) > 0 {
		t.Fatalf("the mounted secret is stored in %v", keys)
	}
	pr := onlyPR(t, b.provider)
	if body := pr.Spec.Body + strings.Join(pr.Comments, "\n"); strings.Contains(body, mountedSecretValue) {
		t.Fatalf("the pull request holds the mounted secret:\n%s", body)
	}
}

// TestFetchedSecretsAreRedacted: a fetched value is a credential of the
// repository's, and the run publishes none of it -- not its log, not the
// objects it leaves in its bucket, not its record, not the pull request it
// opens. The stage here echoes the value into its transcript, its stderr and
// its own text, which is the ordinary shape of an accident and every one of
// them reaches published text.
//
// The redaction is asserted to have fired -- [REDACTED] is in the log -- so a
// mutation that stopped publishing the stage's output cannot pass this by
// publishing nothing.
func TestFetchedSecretsAreRedacted(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)

	leaky := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = fmt.Fprintf(req.Transcript, "{\"leaked\":%q}\n", fetchedSecretValue)
		_, _ = fmt.Fprintln(req.Stderr, "claude: "+fetchedSecretValue)
		res, err := implement("feature")(t, ctx, req)
		res.Text = "done, the file is " + fetchedSecretValue
		return res, err
	}
	rec, err := b.run(t, leaky, review("ship", 0))
	mustReady(t, rec, err)

	logs := b.logs.String()
	if strings.Contains(logs, fetchedSecretValue) {
		t.Fatalf("the runner's log publishes the fetched secret:\n%s", logs)
	}
	if !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("nothing was redacted, so this test proves nothing:\n%s", logs)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(fetchedSecretValue)) {
		t.Fatalf("the record holds the fetched secret: %s", got)
	}
	if keys := objectsContaining(t, b.harness, fetchedSecretValue); len(keys) > 0 {
		t.Fatalf("the fetched secret is stored in %v", keys)
	}
	pr := onlyPR(t, b.provider)
	if body := pr.Spec.Body + strings.Join(pr.Comments, "\n"); strings.Contains(body, fetchedSecretValue) {
		t.Fatalf("the pull request holds the fetched secret:\n%s", body)
	}
}

// TestReviewRunFetchedSecretsNeverReachTheAgent: a review run's agent reads an
// untrusted checkout and prompt (G12), so the run fetches what its own
// registration needs -- a declared secret its job does not carry is fetched and
// registered for redaction, and bootstrap refuses a run that cannot register it
// -- and hands the agent none of it.
//
// Hand mutation this fails on: building the review arm's secretEnvs from
// wf.Secrets (the value reaches the agent's environment), or skipping the fetch
// in review mode (the registration's own refusal stops the run).
func TestReviewRunFetchedSecretsNeverReachTheAgent(t *testing.T) {
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	spec := reviewTaskSpec("reviewonly", reviewOnlyYAML, 7)
	// The workflow the task names is what places a job run in the job it is in
	// (gcp.JobName of the slug and that workflow), and a review-only spec carries
	// a recipe instead: without this the run stops before the fetch, on a job it
	// cannot place.
	spec.Workflow = "app"
	b := secretsRun(t, "", spec, svc)
	// The flipped repository's job runs a derived image, so it carries the
	// catalog stamp -- which is what turns the image arm on: without it the run
	// passes none of its own environment on (D1371's fail-closed), and with it a
	// name the run did not declare must still not reach this agent (F8).
	b.harness.deps.Env = append(b.harness.deps.Env, runner.ImageEntryEnv+"="+imageEntryName)
	pushReviewBranch(t, b.harness, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(b.harness, 7, "someone-elses-change", "")

	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for _, kv := range req.Env {
			if strings.Contains(kv, fetchedSecretValue) {
				t.Errorf("the review agent's environment carries the fetched secret: %q", kv)
			}
		}
		if strings.Contains(req.Prompt, fetchedSecretValue) {
			t.Error("the review agent's prompt carries the fetched secret")
		}
		return review("changes", 1)(t, ctx, req)
	})
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := svc.askedNames(); len(got) != 1 || got[0] != secretName {
		t.Fatalf("the service was asked for %v, want %s: a review run registers the value for redaction and fetches it like any other run", got, secretName)
	}
	if keys := objectsContaining(t, b.harness, fetchedSecretValue); len(keys) > 0 {
		t.Fatalf("the fetched secret is stored in %v", keys)
	}
	pr := onlyPR(t, b.provider)
	if posted := strings.Join(pr.Comments, "\n") + pr.Spec.Body; strings.Contains(posted, fetchedSecretValue) {
		t.Fatalf("the review run published the fetched secret:\n%s", posted)
	}
}

// TestServiceDownHaltsTheRunBeforeTheAgent: a fetch the service cannot serve
// stops the run where it stands -- before the agent's environment is built,
// before any stage, before anything is pushed or opened. Carrying on without
// the value, or reaching for a credential of the run's own instead, is the one
// thing a run may not do here (D143).
//
// The ways the service does not serve the call are the ways the run's own
// sentences tell apart: it cannot be reached at all, it refuses the call, and
// it answers something the run may not read as this secret's value -- a body
// that is not this contract's, one that carries a second document after it, one
// that names another secret, and one whose value is too short to be redacted.
// Each of those stops the run here rather than handing the answer on: the
// contract echoes what the call named for exactly the third of them, and the
// floor of the fourth is the same one agent.BuildEnv refuses a declared secret
// under. A non-2xx answer whose body names no code this contract could have
// written is a refusal too, and stops the run rather than being read as a served
// answer with no value.
func TestServiceDownHaltsTheRunBeforeTheAgent(t *testing.T) {
	// A service that is not listening at all: a URL whose port nothing holds.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	for _, tc := range []struct {
		name      string
		edit      func(*secretsService)
		url       string
		wantInErr string
	}{
		{name: "the service cannot be reached", url: closedURL, wantInErr: "could not be reached"},
		{name: "the service refuses the call", edit: func(s *secretsService) {
			s.status, s.body = credsvc.StatusUnavailable, fmt.Sprintf(`{"error":%q,"message":"the workflow records cannot be read"}`, credsvc.ErrorStateUnavailable)
		}, wantInErr: "answered 503, refused: " + credsvc.ErrorStateUnavailable},
		{name: "the refusal names no code this contract could have written", edit: func(s *secretsService) {
			// A refusal whose code is not one reviewServiceErrorCode carries is
			// still a refusal: read as an answer it would put an EMPTY value
			// under the variable and stop the run in agent.BuildEnv, naming the
			// mount instead of the service -- and version 1 would never be asked
			// for it, which is the fallback this shape of service needs.
			s.status, s.body = credsvc.StatusUnavailable, `{"error":"Workflow-Records-Unreadable","message":"the workflow records cannot be read"}`
		}, wantInErr: "answered 503"},
		{name: "the answer is not this contract's", edit: func(s *secretsService) {
			s.malformed = `{"name":"fixture-fails","value":"x","extra":1}`
		}, wantInErr: "is not a workflow secret's"},
		{name: "the answer carries more than one document", edit: func(s *secretsService) {
			s.malformed = fmt.Sprintf(`{"name":%q,"value":%q}{"name":%q,"value":%q}`, secretName, fetchedSecretValue, otherSecretName, fetchedSecretValue)
		}, wantInErr: "carries more than one document"},
		{name: "the answer names another secret", edit: func(s *secretsService) {
			s.names[secretName] = fetchedSecretValue
			s.answerAs = otherSecretName
		}, wantInErr: "is for another secret"},
		{name: "the answer is a value too short to redact", edit: func(s *secretsService) {
			s.names[secretName] = shortSecretValue
		}, wantInErr: fmt.Sprintf("answered a value shorter than %d bytes", minFetchedSecretLen)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &secretsService{names: map[string]string{}, envs: map[string]string{}}
			if tc.edit != nil {
				tc.edit(svc)
			}
			b := secretsRun(t, "  first_line_review: off\n", nil, svc)
			if tc.url != "" {
				b.deps.Env = append(filterEnv(b.deps.Env, runner.CredentialsServiceURLEnv),
					runner.CredentialsServiceURLEnv+"="+tc.url)
			}
			failed, err := b.run(t, implement("feature"), review("ship", 0))
			if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("err = %v, want a refusal naming %q", err, tc.wantInErr)
			}
			if !strings.Contains(err.Error(), secretEnvVar) {
				t.Errorf("the refusal does not name the variable it was fetching: %v", err)
			}
			if len(b.agent.calls) != 0 {
				t.Fatalf("the agent was called %d times, want none: the run stops before its environment is built", len(b.agent.calls))
			}
			if len(b.provider.State.PRs) != 0 {
				t.Fatal("a run that could not fetch its secret opened a pull request")
			}
			// No second call: version 2's not-in-the-record answer is the only
			// one the runner asks again under version 1
			// (TestFallsBackToV1WhenNotInRecord), and a refusal asked again
			// would be the service's decision questioned twice. A service that
			// was never reached is the one case with none.
			if got := len(svc.callsOf()); got > 1 {
				t.Errorf("the service was called %d times, want at most one", got)
			}
			if failed == nil || failed.Status != runstore.StatusInfraError || failed.Outcome != runstore.OutcomeNone {
				t.Fatalf("record = %+v, want an infra error with no outcome", failed)
			}
		})
	}
}

// TestNoServiceURLNoFetch: the fetch is gated on the job's own environment
// naming a credentials service, and a run that names none behaves exactly as it
// did before the fetch existed -- agent.BuildEnv's own "secret %s is not set",
// which names the variable and the fix. A local invocation is the same story
// even when a developer's environment does carry a URL (D149): `fugaro exec`
// holds no installation identity and reaches no private service, so that URL is
// not one it may use.
//
// The stand-in server here counts every call it receives: the point of these
// runs is that it receives none.
func TestNoServiceURLNoFetch(t *testing.T) {
	var hits atomic.Int64
	standin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(standin.Close)

	t.Run("a job whose environment names no service", func(t *testing.T) {
		hits.Store(0)
		// An api-key job run, so nothing about the run needs a service at all:
		// what is asserted is what a run does with a declared secret its job
		// does not carry when there is no one to ask.
		b := admissionRun(t, "github", "acme/app", "enforce", "", `{"version":9,"allow_repos":["*"]}`)
		b.deps.Local = false
		b.deps.Project = "aurora"
		b.deps.Env = filterEnv(b.deps.Env, secretEnvVar)
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err == nil || !strings.Contains(err.Error(), "secret "+secretEnvVar+" is not set in the runner environment") {
			t.Fatalf("rec = %+v, err = %v, want agent.BuildEnv's own refusal", rec, err)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("the agent was called %d times, want none", len(b.agent.calls))
		}
		if hits.Load() != 0 {
			t.Fatal("a run whose job names no service reached one")
		}
	})

	t.Run("a local run never uses a service URL it is given", func(t *testing.T) {
		hits.Store(0)
		// A local run handed a service URL is refused outright, and long before
		// the fetch (the provider opener's own D149 fence, serviceTokens): what
		// this pins is that no local run reaches the service at all.
		b := newBK(t, gwConfig(t, "  first_line_review: off\n"), "enforce", "")
		b.deps.Env = append(filterEnv(b.deps.Env, secretEnvVar),
			runner.CredentialsServiceURLEnv+"="+standin.URL)
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err == nil || !strings.Contains(err.Error(), "D149") {
			t.Fatalf("rec = %+v, err = %v, want D149's refusal of a local run with a service URL", rec, err)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("the agent was called %d times, want none", len(b.agent.calls))
		}
		if hits.Load() != 0 {
			t.Fatal("a local run reached a credentials service a developer's environment named (D149)")
		}
	})
}

// TestFetchWorkflowSecretsSendsName: the call names the LOGICAL secret
// (config.Secret.Name), which is the unit the project record's allow-list is
// written in (D3333), and not the variable the flip's own record is written in.
// One call per declared secret, each carrying its own name, and each answer
// landing in the variable that name's declaration exposes it as -- which is the
// whole of what asking by name has to get right.
//
// Hand mutation this fails on: sending the variable where the name belongs (the
// record holds no variable), or reading an answer into a variable the call did
// not ask about.
func TestFetchWorkflowSecretsSendsName(t *testing.T) {
	svc := &secretsService{names: map[string]string{
		"fixture-fails":  fetchedSecretValue,
		"fixture-marker": otherFetchedValue,
	}}
	b := secretsRunConfig(t, twoSecretsConfig(t), nil, svc)

	seen := map[string]string{}
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen[secretEnvVar] = envValue(req.Env, secretEnvVar)
			seen["FIXTURE_MARKER"] = envValue(req.Env, "FIXTURE_MARKER")
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if got := svc.askedNames(); len(got) != 2 || got[0] != secretName || got[1] != "fixture-marker" {
		t.Fatalf("the service was asked for %v, want each declared secret by its own name", got)
	}
	for i, want := range []struct{ name, env, value string }{
		{secretName, secretEnvVar, fetchedSecretValue},
		{"fixture-marker", "FIXTURE_MARKER", otherFetchedValue},
	} {
		call := svc.callsOf()[i]
		if body := `{"name":"` + want.name + `","version":2}`; call.body != body {
			t.Errorf("call %d carried %q, want %q", i, call.body, body)
		}
		if seen[want.env] != want.value {
			t.Errorf("the agent's environment carries %s=%q, want the value the service answered for %s", want.env, seen[want.env], want.name)
		}
	}
}

// TestNoV2WhileRepoSecretsOn: version 2 rests on D2313, so while the door that
// ruling keeps is open -- config.RepoSecretsOff false, which is a repository
// reading its own copy again -- the fetch asks the way it always has and
// version 2 is never sent (G9, F24). Reversing D2313 is meant to be a visible
// change, and this is the wire half of the visibility.
//
// Hand mutation this fails on: sending version 2 whatever the door says (the
// call names a secret the record would answer the project's copy of), or never
// sending it (TestBootstrapFetchesMissingWorkflowSecretsFromTheService fails).
func TestNoV2WhileRepoSecretsOn(t *testing.T) {
	repoSecretsOff(t)
	svc := &secretsService{envs: map[string]string{secretEnvVar: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)

	var seen string
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen = envValue(req.Env, secretEnvVar)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if seen != fetchedSecretValue {
		t.Errorf("the agent's environment carries %s=%q, want the value the service answered", secretEnvVar, seen)
	}
	calls := svc.callsOf()
	if len(calls) != 1 {
		t.Fatalf("%d calls, want one: the door open means version 2 is never sent", len(calls))
	}
	if want := `{"env":"` + secretEnvVar + `"}`; calls[0].body != want {
		t.Errorf("the call carried %q, want the version-1 body %q", calls[0].body, want)
	}
	if calls[0].version != credsvc.WorkflowSecretsVersion1 {
		t.Errorf("the call declares version %d, want version %d", calls[0].version, credsvc.WorkflowSecretsVersion1)
	}
}

// TestFallsBackToV1WhenNotInRecord: version 2's "this name is not in the project
// record" (credsvc.ErrorWorkflowSecretNotAllowed) is the same answer a record
// that does not hold the name YET gives, and every repository S3 already serves
// is in exactly that state until update copies its names (D3325, D3328): the
// runner asks version 1 for the same variable, which is the record that
// repository is on today, and the run starts with the value.
//
// It is the ONE refusal with a fallback (contract.md): every other one is the
// service's decision, and TestServiceDownHaltsTheRunBeforeTheAgent pins that
// those stop the run on the first answer.
func TestFallsBackToV1WhenNotInRecord(t *testing.T) {
	// The name is not in the record and the variable is: the state of every
	// repository whose names update has not copied yet.
	svc := &secretsService{envs: map[string]string{secretEnvVar: fetchedSecretValue}}
	b := secretsRun(t, "  first_line_review: off\n", nil, svc)

	var seen string
	rec, err := b.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			seen = envValue(req.Env, secretEnvVar)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0))
	mustReady(t, rec, err)
	if seen != fetchedSecretValue {
		t.Errorf("the agent's environment carries %s=%q, want the value version 1 answered", secretEnvVar, seen)
	}
	calls := svc.callsOf()
	if len(calls) != 2 {
		t.Fatalf("%d calls, want two: version 2, then the same secret's variable", len(calls))
	}
	if want := `{"name":"` + secretName + `","version":2}`; calls[0].body != want {
		t.Errorf("the first call carried %q, want %q", calls[0].body, want)
	}
	if want := `{"env":"` + secretEnvVar + `"}`; calls[1].body != want {
		t.Errorf("the fallback call carried %q, want the version-1 body %q", calls[1].body, want)
	}
}
