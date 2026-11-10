package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fakeSlipID is the id the fixtures' service grants. It is not a credential
// and not a token: a slip's id is the node name the run presents, and its
// protection is the binding to the run and the claim secret (R8, D1941), so
// nothing here is secret-shaped.
const fakeSlipID = "fake-slip-0f1e2d3c-not-a-credential"

// fakeLauncherToken is what the fixture's launcher presents instead of a real
// Google ID token, which no test may mint.
const fakeLauncherToken = "fake-identity-token-not-a-real-credential"

// slipLaunchRig is one launch of a switched-on installation: the service's
// block with its URL, the repository's own record (the half of the switch only
// an apply answers), a checkout of the cascade and the task object the run's
// launch reads, exactly as run_credentials_env_test's own fixture builds them.
type slipLaunchRig struct {
	f    *launchBudgetFixture
	env  *cloudEnv
	rec  *envRecorder
	spec *task.Spec
	// warned is what the launch wrote as warnings, which is where the two
	// answers that are not a slip go.
	warned *strings.Builder
	// asked is what the launch asked the grant call for: the URL, the request
	// and how many times the call was made.
	url   string
	req   credsvc.SlipGrantRequest
	calls int
}

func newSlipLaunchRig(t *testing.T) *slipLaunchRig {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	var warned strings.Builder
	prevWarn := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prevWarn })
	return &slipLaunchRig{f: f, env: env, rec: rec, spec: raceSpec(t, env), warned: &warned}
}

// launch runs the fixture's launch with the grant call answered by answer, and
// returns the launch's plain environment without the services stamp.
func (r *slipLaunchRig) launch(t *testing.T, answer func(credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error)) []string {
	t.Helper()
	prev := slipGrant
	slipGrant = func(_ context.Context, url string, req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		r.url, r.req, r.calls = url, req, r.calls+1
		return answer(req)
	}
	t.Cleanup(func() { slipGrant = prev })
	if _, err := launchRun(context.Background(), r.env, appSlug, r.spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	return withoutServicesStamp(t, r.rec.env)
}

// TestRunRequestsSlipWhenServiceServesIt: a switched-on launch asks the
// installation's credentials service for the run's slip and hands the job its
// id, under the one name the runner claims it by (DS1.5).
//
// The request is the whole of what a launcher may say about a run: which
// repository and which run it is starting, the job account it expects the run
// to call as (the account the claim is checked against, D1937), the
// credentials the run may be served (the crafts' own answers, so a pool or a
// credential the run's configuration selects is served under the slip and
// nothing else is) and how long it asks to be served for. Nothing in it is a
// credential value (G9).
//
// The entry is PLAIN and not secret, and it lives on the execution rather than
// in any stored document: SecretEnv is refused by the Batch backend outright
// (backend.LaunchSpec.SecretEnv), and the slip's protection is its binding
// rather than its secrecy — the agent can read the runner's environment anyway
// (F22, reconciliation).
func TestRunRequestsSlipWhenServiceServesIt(t *testing.T) {
	r := newSlipLaunchRig(t)
	plain := r.launch(t, func(req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	})
	if r.calls != 1 {
		t.Fatalf("the launch made %d grant calls, want exactly one: a run has one slip", r.calls)
	}
	if r.url != launchCredsURL {
		t.Errorf("the grant call went to %q, want the service the launch handed the job (%q)", r.url, launchCredsURL)
	}
	if r.req.Slug != appSlug || r.req.Run != r.spec.RunID {
		t.Errorf("the grant call named %s/%s, want the run this launch is starting (%s/%s)", r.req.Slug, r.req.Run, appSlug, r.spec.RunID)
	}
	wantAccount := gcp.ServiceAccountID(appSlug, "web") + "@" + r.env.lc.GCPProject + ".iam.gserviceaccount.com"
	if r.req.Account != wantAccount {
		t.Errorf("the grant call expected the account %q, want the job's own (%q): the claim is checked against it and another account's slip is one this run can never claim", r.req.Account, wantAccount)
	}
	if want := []string{config.CredentialVertex}; !slices.Equal(r.req.Credentials, want) {
		t.Errorf("the grant call asked for %v, want the crafts' own accesses %v: a credential the run's configuration selects and the slip does not name is refused at its call (R9)", r.req.Credentials, want)
	}
	if want := int64(backend.MaxRunTime / time.Second); r.req.MaxRuntimeSeconds != want {
		t.Errorf("the grant call asked to be served for %ds, want the run's own ceiling (%d): a task that names no total timeout may live for the platform's maximum", r.req.MaxRuntimeSeconds, want)
	}
	want := []string{envCredentialsServiceURL + "=" + launchCredsURL, envSlip + "=" + fakeSlipID}
	if !slices.Equal(plain, want) {
		t.Errorf("the launch handed the job %v, want %v: the id is one more plain entry beside the service's address, and the slip the service granted is the one the job is told", plain, want)
	}
	for _, kv := range r.rec.secretEnv {
		if strings.HasPrefix(kv, envSlip+"=") || strings.Contains(kv, fakeSlipID) {
			t.Errorf("the launch carried %q as secret environment, want the slip's id in plain environment only: the id is not a secret, and the backend that matters refuses SecretEnv outright (backend.LaunchSpec.SecretEnv)", kv)
		}
	}
	if r.warned.Len() != 0 {
		t.Errorf("the launch warned about a slip it was granted: %s", r.warned)
	}
}

// TestRunFallsBackWithoutSlipRoute: an installation whose credentials service
// is older than this CLI proceeds on the existing path — the launch spec it
// built before the slip existed, and nothing said about it, because a service
// one release behind is the skew `fugaro doctor` reports rather than a fault
// of this run.
func TestRunFallsBackWithoutSlipRoute(t *testing.T) {
	r := newSlipLaunchRig(t)
	plain := r.launch(t, func(credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{}, fmt.Errorf("%w: the service answered %d", errSlipRouteAbsent, http.StatusNotFound)
	})
	want := []string{envCredentialsServiceURL + "=" + launchCredsURL}
	if !slices.Equal(plain, want) {
		t.Errorf("a service that does not serve the grant route had the launch hand the job %v, want %v: the run proceeds on the path it took before the slip, with the service's address and no id", plain, want)
	}
	if r.warned.Len() != 0 {
		t.Errorf("the version skew was reported as %q, want nothing: an installation one release behind is what `fugaro doctor` reports, and every launch of it would say it again", r.warned.String())
	}
}

// TestSlipIDNotInTaskFile: what the service grants reaches the execution and
// never a stored document. The task object is the repository's own file — it
// is written once per run ID and repeated byte for byte by a `--retry` — so a
// slip written there would be handed to a second launch, and a slip is one
// run's.
//
// Mutation (verified, run and restored): store the id in the task object at
// the launch (write a task carrying it on the grant path) and this fails on
// the object's own bytes.
func TestSlipIDNotInTaskFile(t *testing.T) {
	r := newSlipLaunchRig(t)
	plain := r.launch(t, func(req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	})
	if !slices.Contains(plain, envSlip+"="+fakeSlipID) {
		t.Fatalf("the launch handed the job %v, so no slip was granted and this test cannot say where the id went", plain)
	}
	st := runstore.Open(r.env.bucket.Bucket, appSlug, r.spec.RunID)
	data, _, err := r.env.bucket.Read(context.Background(), st.Prefix()+"task.json")
	if err != nil {
		t.Fatalf("the run's task object could not be read: %v", err)
	}
	if strings.Contains(string(data), fakeSlipID) {
		t.Errorf("the run's task object carries the slip's id, and it must not: the object is the repository's own file, a repeated run ID repeats it byte for byte, and the grant is one run's — it belongs on the execution this launcher starts and nowhere else")
	}
}

// TestTheGrantCallAsksTheGrantRoute: the call itself, against a service a test
// runs: the grant route's own path, the launcher's own Google ID token as the
// bearer, and the id the service answers handed back. A service that does not
// serve the route is the version skew and not a refusal (DS1.4), which is what
// the launch's fallback is judged on.
func TestTheGrantCallAsksTheGrantRoute(t *testing.T) {
	var (
		path, auth string
		body       credsvc.SlipGrantRequest
		status     int
		answer     credsvc.SlipGrantResponse
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.Unmarshal(raw, &body)
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(srv.Close)
	prev := slipIdentity
	slipIdentity = func(context.Context, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { slipIdentity = prev })

	req := credsvc.SlipGrantRequest{Slug: appSlug, Run: "20261110-100000-abcd",
		Account: "fugaro-web-abcde@proj.iam.gserviceaccount.com", Credentials: []string{config.CredentialVertex}, MaxRuntimeSeconds: 3600}
	answer = credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: 3600}
	got, err := slipGrantOverService(context.Background(), srv.URL, req)
	if err != nil {
		t.Fatalf("the grant call failed against a service that answers it: %v", err)
	}
	if got.Slip != fakeSlipID || got.MaxRuntimeSeconds != 3600 {
		t.Errorf("the grant call read %+v, want the answer the service gave (%s, 3600s): the launcher passes the id on and the granted runtime is what it was answered", got, fakeSlipID)
	}
	if path != credsvc.SlipPath {
		t.Errorf("the grant call asked for %q, want the grant route (%q)", path, credsvc.SlipPath)
	}
	if auth != "Bearer "+fakeLauncherToken {
		t.Errorf("the grant call presented %q, want the launcher's own Google ID token: the service's launcher class verifies that token and no other identity", auth)
	}
	if body.Slug != req.Slug || body.Run != req.Run || body.Account != req.Account || body.MaxRuntimeSeconds != req.MaxRuntimeSeconds || !slices.Equal(body.Credentials, req.Credentials) {
		t.Errorf("the grant call sent %+v, want the request it was given (%+v)", body, req)
	}

	status = http.StatusNotFound
	if _, err := slipGrantOverService(context.Background(), srv.URL, req); !errors.Is(err, errSlipRouteAbsent) {
		t.Errorf("a service that does not serve the grant route answered %v, want the version skew (errSlipRouteAbsent) rather than a refusal of this launcher", err)
	}
	status = http.StatusForbidden
	if _, err := slipGrantOverService(context.Background(), srv.URL, req); err == nil || errors.Is(err, errSlipRouteAbsent) {
		t.Errorf("a service that refuses the launcher answered %v, want a refusal that is not the version skew", err)
	}
}
