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
// Mutation (verified, run and restored): in the launch, write the granted id
// onto the task object (`spec.Task` plus that entry, `s.WriteTask`) and this
// fails on the object's own bytes.
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

// TestTheGrantCallRefusesAURLItWillNotSendATokenTo: the grant call carries the
// launcher's own Google ID token, which is a bearer credential, so the URL it
// is sent to is judged before anything is minted: https, or http on a loopback
// address, which is a test's own service. A URL that is more than a scheme and
// a host is refused too — the route is appended to it, and a base path, a
// query or a fragment would decide where this token goes.
//
// Mutation (verified, run and restored): `return nil` as the first statement of
// checkSlipServiceURL and this fails on the plain-http cases.
func TestTheGrantCallRefusesAURLItWillNotSendATokenTo(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://creds.example.com", true},
		{"https://creds.example.com/", true},
		{"https://creds.example.com:8443", true},
		{"http://127.0.0.1:8080", true},
		{"http://localhost:8080", true},
		{"http://creds.example.com", false},
		{"http://10.0.0.1", false},
		{"https://creds.example.com/v1", false},
		{"https://creds.example.com?x=1", false},
		{"https://creds.example.com#f", false},
		{"https://user:pass@creds.example.com", false},
		{"creds.example.com", false},
		{"", false},
	} {
		err := checkSlipServiceURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("checkSlipServiceURL(%q) = %v, want refused = %v: this URL is where the launcher's own identity token would be sent", c.url, err, !c.want)
		}
		if err != nil && strings.Contains(err.Error(), c.url) && c.url != "" {
			t.Errorf("the refusal of %q repeats the URL: %v", c.url, err)
		}
	}
}

// TestTheGrantCallDoesNotFollowARedirect: a 3xx is not an answer, and the
// request holds the launcher's own token in a header a redirect would carry to
// whatever host the Location named. The second server here is exactly that
// host, and it must never be reached.
//
// Mutation (verified, run and restored): drop the CheckRedirect from
// slipClient and this fails on the request that reached it.
func TestTheGrantCallDoesNotFollowARedirect(t *testing.T) {
	reached := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slip":"` + fakeSlipID + `"}`))
	}))
	t.Cleanup(elsewhere.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+credsvc.SlipPath, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	prev := slipIdentity
	slipIdentity = func(context.Context, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { slipIdentity = prev })

	req := credsvc.SlipGrantRequest{Slug: appSlug, Run: "20261110-100000-abcd",
		Account: "fugaro-web-abcde@proj.iam.gserviceaccount.com", Credentials: []string{config.CredentialVertex}, MaxRuntimeSeconds: 3600}
	if _, err := slipGrantOverService(context.Background(), srv.URL, req); err == nil {
		t.Fatal("the grant call followed a redirect, want a refusal: the launcher's identity token is a bearer credential and the redirect names the host it would go to")
	}
	if reached != 0 {
		t.Errorf("the redirected grant call reached the host the Location named (%d requests), want none", reached)
	}
}

// TestTheGrantCallReadsABoundedAnswer: the service is another writer, and its
// answer is rendered into this launcher's memory, so its size is judged before
// its shape. The answers here are ones a launcher would otherwise accept — the
// id it granted, and padding — so the size alone is what refuses the second
// one: both refusals reach a caller as one error, which is why the refusal's
// own words are what says which of the two this was.
//
// Mutation (verified, run and restored): delete the `len(data) >
// maxSlipAnswerBytes` refusal and the second case stops being refused.
func TestTheGrantCallReadsABoundedAnswer(t *testing.T) {
	const prefix, suffix = `{"slip":"` + fakeSlipID + `","padding":"`, `"}`
	answer := func(extra int) string {
		return prefix + strings.Repeat("a", maxSlipAnswerBytes-len(prefix)-len(suffix)+extra) + suffix
	}
	body := answer(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := slipIdentity
	slipIdentity = func(context.Context, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { slipIdentity = prev })

	req := credsvc.SlipGrantRequest{Slug: appSlug, Run: "20261110-100000-abcd",
		Account: "fugaro-web-abcde@proj.iam.gserviceaccount.com", Credentials: []string{config.CredentialVertex}, MaxRuntimeSeconds: 3600}
	got, err := slipGrantOverService(context.Background(), srv.URL, req)
	if err != nil || got.Slip != fakeSlipID {
		t.Errorf("an answer of exactly %d bytes was read as %+v, %v, want the id it carries: %d is the bound, not one past it", len(body), got, err, maxSlipAnswerBytes)
	}
	body = answer(1)
	_, err = slipGrantOverService(context.Background(), srv.URL, req)
	if err == nil || errors.Is(err, errSlipRouteAbsent) || !strings.Contains(err.Error(), "larger than a grant's") {
		t.Errorf("an answer of %d bytes was read as %v, want the refusal of an answer larger than a grant's (%d bytes)", len(body), err, maxSlipAnswerBytes)
	}
}

// TestASlipIDIsUsableOnlyAsOneCleanEntry: the granted id becomes ONE entry of
// the execution's environment, so a value that is empty, longer than any id
// the service draws, or not one clean printable token is not an id this
// launcher puts on a job — the backend builds the environment out of these
// entries, and the value is the service's, not the launcher's.
//
// Mutation (verified, run and restored): `return true` from slipIDUsable and
// this fails on every case but the first.
func TestASlipIDIsUsableOnlyAsOneCleanEntry(t *testing.T) {
	for _, c := range []struct {
		id   string
		want bool
	}{
		{fakeSlipID, true},
		{strings.Repeat("a", maxSlipIDBytes), true},
		{strings.Repeat("a", maxSlipIDBytes+1), false},
		{"", false},
		{"two words", false},
		{"line\nbreak", false},
		{"tab\there", false},
		{"sliß", false},
	} {
		if got := slipIDUsable(c.id); got != c.want {
			t.Errorf("slipIDUsable(%q) = %v, want %v: the id is one environment entry this launcher hands a job", c.id, got, c.want)
		}
	}
}

// TestALaunchThatCannotReadTheRepositoryAsksForNoSlip: the slip names the
// credentials the run may be served, and only the repository's own file can
// select a craft's access — so a launch that can read neither the file at the
// run's ref nor a checkout of the repository cannot name what the run, which
// will check the file out, is about to ask for. It starts without a slip and
// says so, rather than claiming a slip that names less than the run asks for:
// that is the run refused at its next call with no way back (R9), where no
// slip is the path every run took before the slip existed.
//
// Mutation (verified, run and restored): resolve the layers with no repo layer
// where the file could not be read (an empty `if co != nil`) instead of
// refusing to ask, and this fails on the grant call it makes with defaults the
// run is not configured for.
func TestALaunchThatCannotReadTheRepositoryAsksForNoSlip(t *testing.T) {
	r := newSlipLaunchRig(t)
	// A directory that is no checkout of the repository: nothing for
	// launchFileConfig to read the run's document from, which is the daemon's
	// own launch (orchestrator_launcher) and `fugaro run --repo` from
	// elsewhere on the machine.
	t.Chdir(t.TempDir())
	plain := r.launch(t, func(req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	})
	if r.calls != 0 {
		t.Errorf("a launch that could not read the repository's own fugaro.yaml made %d grant calls, want none: the slip would name defaults the run is not configured to ask for", r.calls)
	}
	if want := []string{envCredentialsServiceURL + "=" + launchCredsURL}; !slices.Equal(plain, want) {
		t.Errorf("the launch handed the job %v, want %v", plain, want)
	}
	if !strings.Contains(r.warned.String(), "without a slip") {
		t.Errorf("the launch said %q, want a warning that this run starts without a slip and why", r.warned.String())
	}
}
