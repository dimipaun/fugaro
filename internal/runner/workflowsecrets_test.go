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

// fetchedSecretValue is what the stand-in service answers for secretEnvVar, and
// mountedSecretValue what a job that still mounts it carries: two different
// values, so a run that took the wrong one shows.
const (
	fetchedSecretValue = "fetched-workflow-secret-9c1e"
	mountedSecretValue = "mounted-workflow-secret-4a20"
)

// secretsCall is one call the stand-in service received, kept whole: what the
// call asked for and what it presented, which is what a test asserts the fetch
// is signed by.
type secretsCall struct {
	env    string
	header http.Header
	body   string
}

// secretsService is the credentials service's stand-in: the workflow-secrets
// route (B3.2's contract) answered from values, and every other path relayed to
// upstream, the model upstream's own stand-in -- the real service carries a
// run's model calls too, and a stand-in that refused them would turn a stray
// call into a mystery.
//
// status, body and malformed are the three ways a call is not served: a typed
// refusal with status, a 200 whose body is not this contract's, and (with no
// value under the variable asked for) the state_unavailable refusal the route
// writes for a record that holds nothing there.
type secretsService struct {
	mu        sync.Mutex
	values    map[string]string
	calls     []secretsCall
	status    int
	body      string
	malformed string
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
	s.calls = append(s.calls, secretsCall{env: req.Env, header: r.Header.Clone(), body: string(raw)})
	value, ok := s.values[req.Env]
	status, refusal, malformed := s.status, s.body, s.malformed
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case malformed != "":
		_, _ = io.WriteString(w, malformed)
	case status != 0:
		w.WriteHeader(status)
		_, _ = io.WriteString(w, refusal)
	case !ok:
		w.WriteHeader(credsvc.StatusUnavailable)
		_, _ = fmt.Fprintf(w, `{"error":%q,"message":"the record holds nothing under that name"}`, credsvc.ErrorStateUnavailable)
	default:
		_ = json.NewEncoder(w).Encode(credsvc.WorkflowSecretsResponse{Env: req.Env, Value: value})
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

// asked is the variable of every call the service received, in order.
func (s *secretsService) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.env)
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

// secretsRun is the whole run these tests are about: the fixture repository's
// job run whose environment carries the credentials service's URL (so the run
// is on the switch and its model path is the service, B2.2) and no mounted
// value for the fixture's declared secret. extra is inserted under the
// fixture's `agent:` block, spec the task (nil for the fixture's own) and svc
// the service's stand-in.
func secretsRun(t *testing.T, extra string, spec *task.Spec, svc *secretsService) *bk {
	t.Helper()
	h := newHarnessFiles(t, secretsConfig(t, extra), spec, nil)
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
	h.deps.Env = append(filterEnv(h.deps.Env, secretEnvVar),
		runner.CredentialsServiceURLEnv+"="+srv.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"))
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

// TestBootstrapFetchesMissingWorkflowSecretsFromTheService is B3.4's red step:
// a run whose job does not carry a declared secret's value fails today with
// "secret FIXTURE_FAILS_FILE is not set in the runner environment", and with
// the fetch it starts and runs with the value in the environment its stages
// run in.
//
// What is asserted is the whole of it: the run reaches its stages and ends
// ready, the agent's own environment carries the fetched value, the service was
// asked for exactly the one variable the job does not carry, and the call it
// was asked with is signed the way every call to the service is -- the job's
// own ID token as the bearer, the run's identity, the job's name -- and carries
// no credential of the job's own (D143).
//
// Hand mutation this fails on: dropping the call from bootstrap (the run stops
// on BuildEnv's refusal), or fetching a variable the job already carries.
func TestBootstrapFetchesMissingWorkflowSecretsFromTheService(t *testing.T) {
	svc := &secretsService{values: map[string]string{secretEnvVar: fetchedSecretValue}}
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
	if got := svc.asked(); len(got) != 1 || got[0] != secretEnvVar {
		t.Fatalf("the service was asked for %v, want exactly %s", got, secretEnvVar)
	}
	call := svc.callsOf()[0]
	if want := `{"env":"` + secretEnvVar + `"}`; call.body != want {
		t.Errorf("the call carried %q, want the contract's one field for %s", call.body, secretEnvVar)
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
	svc := &secretsService{values: map[string]string{secretEnvVar: fetchedSecretValue}}
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
	svc := &secretsService{values: map[string]string{secretEnvVar: fetchedSecretValue}}
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
	svc := &secretsService{values: map[string]string{secretEnvVar: fetchedSecretValue}}
	spec := reviewTaskSpec("reviewonly", reviewOnlyYAML, 7)
	// The workflow the task names is what places a job run in the job it is in
	// (gcp.JobName of the slug and that workflow), and a review-only spec carries
	// a recipe instead: without this the run stops before the fetch, on a job it
	// cannot place.
	spec.Workflow = "app"
	b := secretsRun(t, "", spec, svc)
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
	if got := svc.asked(); len(got) != 1 || got[0] != secretEnvVar {
		t.Fatalf("the service was asked for %v, want %s: a review run registers the value for redaction and fetches it like any other run", got, secretEnvVar)
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
// The three ways the service does not serve the call are the three the run's
// own sentences tell apart: it cannot be reached at all, it refuses the call,
// and it answers something that is not this contract's.
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
		{name: "the answer is not this contract's", edit: func(s *secretsService) {
			s.malformed = `{"env":"FIXTURE_FAILS_FILE","value":"x","extra":1}`
		}, wantInErr: "is not a workflow secret's"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &secretsService{values: map[string]string{}}
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
		b := admissionRun(t, "github", "acme/app", "enforce", "", `{"version":5,"allow_repos":["*"]}`)
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
