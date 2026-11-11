package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The fixture's installation: a credentials service URL in the shape Cloud Run
// serves one (localcfg holds the block to that shape), and the intake's own
// address, which is the same host with the service's leading label swapped.
// Neither is a credential: both are a deployment's published addresses.
const (
	intakeTestCredentialsURL = "https://fugaro-credentials-abcde-uc.a.run.app"
	intakeTestIntakeURL      = "https://fugaro-intake-abcde-uc.a.run.app"
)

// intakeTestValue is the value the value-handling tests type at stdin. It is
// spelled as what it is, a fake, for the reason run_slip_test.go's fake
// launcher token is: no test in this tree carries a secret-shaped literal.
const intakeTestValue = "fake-intake-value-not-a-credential"

// intakeFixture writes the project config the intake commands read: one
// installation whose credentials service is deployed.
func intakeFixture(t *testing.T) {
	t.Helper()
	path := isolateProjects(t, t.TempDir())
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"credentials_service:\n  url: " + intakeTestCredentialsURL + "\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
}

// intakeSeen is one call the commands made, as the fake below recorded it.
type intakeSeen struct {
	URL  string
	Call secretsIntakeCall
}

// fakeIntake replaces the one seam every intake call goes through with one that
// records the calls and answers through reply. No test reaches a service, and
// the recording is what a test judges the client by.
func fakeIntakeCalls(t *testing.T, reply func(c secretsIntakeCall)) *[]intakeSeen {
	t.Helper()
	seen := &[]intakeSeen{}
	prev := secretsIntakeDo
	secretsIntakeDo = func(_ context.Context, url string, c secretsIntakeCall) error {
		*seen = append(*seen, intakeSeen{URL: url, Call: c})
		if reply != nil {
			reply(c)
		}
		return nil
	}
	t.Cleanup(func() { secretsIntakeDo = prev })
	return seen
}

// TestLsPrintsNamesAndDates: `fugaro secrets user ls` and its `shared` twin ask
// the intake for the caller's own prefix and print what a listing is — the
// names and the dates — with no value anywhere, and against the installation's
// intake address rather than anything the caller typed.
func TestLsPrintsNamesAndDates(t *testing.T) {
	intakeFixture(t)
	made := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	seen := fakeIntakeCalls(t, func(c secretsIntakeCall) {
		ans, ok := c.Out.(*intake.ListAnswer)
		if !ok {
			t.Fatalf("the list call asks for %T, want the intake's own ListAnswer (the body the route answers with)", c.Out)
		}
		*ans = intake.ListAnswer{Secrets: []intake.ListedSecret{
			{ID: "fugaro_u_1234_github-token", Name: "github-token", Scope: intake.ScopeUser, Created: made},
			{ID: "fugaro_u_1234_npm-token", Name: "npm-token", Scope: intake.ScopeUser, Created: made.Add(time.Hour)},
		}}
	})

	out, _, err := execute(t, "secrets", "user", "ls")
	if err != nil {
		t.Fatalf("secrets user ls: %v\n%s", err, out)
	}
	for _, want := range []string{"NAME", "CREATED", "github-token", "npm-token", "2026-03-04 05:06", "2026-03-04 06:06"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want one", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestIntakeURL {
		t.Errorf("the listing asked %s, want the installation's intake (%s): the address is the installation's own and never a flag", got.URL, intakeTestIntakeURL)
	}
	if got.Call.Path != intake.ListPath {
		t.Errorf("the listing asked %q, want the intake's list route (%q)", got.Call.Path, intake.ListPath)
	}
	if req, ok := got.Call.Body.(intake.ListRequest); !ok || req.Scope != intake.ScopeUser {
		t.Errorf("the listing sent %#v, want the caller's own scope (%q)", got.Call.Body, intake.ScopeUser)
	}

	// --json is the same answer as a document, and still no value.
	*seen = (*seen)[:0]
	jout, _, err := execute(t, "secrets", "user", "ls", "--json")
	if err != nil {
		t.Fatalf("secrets user ls --json: %v\n%s", err, jout)
	}
	var doc intake.ListAnswer
	if err := json.Unmarshal([]byte(jout), &doc); err != nil {
		t.Fatalf("--json printed %q, want the intake's own listing document: %v", jout, err)
	}
	if len(doc.Secrets) != 2 || doc.Secrets[0].Name != "github-token" || !doc.Secrets[0].Created.Equal(made) {
		t.Errorf("--json printed %+v, want the two names and dates the intake answered", doc)
	}

	// And the shared scope asks for the project's prefix instead.
	*seen = (*seen)[:0]
	if _, _, err := execute(t, "secrets", "shared", "ls"); err != nil {
		t.Fatalf("secrets shared ls: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("secrets shared ls made %d calls, want one", len(*seen))
	}
	if req, ok := (*seen)[0].Call.Body.(intake.ListRequest); !ok || req.Scope != intake.ScopeShared {
		t.Errorf("secrets shared ls sent %#v, want the project-shared scope (%q)", (*seen)[0].Call.Body, intake.ScopeShared)
	}
}

// TestSetRefusedInAgentSession: every intake verb is refused inside a coding
// agent's session and inside a run's own process, before anything is read and
// before the service is reached (F9, F12): a person's own Google ID token is
// what the intake verifies, and an agent holds that person's credentials.
func TestSetRefusedInAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		args  []string
		value string
	}{
		{"an agent's session stores", "CLAUDECODE", []string{"secrets", "user", "set", "github-token"}, intakeTestValue},
		{"an agent's session lists", "CLAUDECODE", []string{"secrets", "user", "ls"}, ""},
		{"a run's process stores", "FUGARO_RUN", []string{"secrets", "shared", "set", "npm-token"}, intakeTestValue},
		{"a run's process removes", "CLOUD_RUN_JOB", []string{"secrets", "shared", "rm", "npm-token"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intakeFixture(t)
			t.Setenv(tc.env, "1")
			seen := fakeIntakeCalls(t, nil)

			out, _, err := executeStdin(t, tc.value+"\n", tc.args...)
			if err == nil {
				t.Fatalf("%s was answered with success (%s is set): %s", strings.Join(tc.args, " "), tc.env, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a refusal of the session, not a failure): %v", ExitCode(err), ExitUserError, err)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Errorf("the refusal does not name the marker that refused it: %v", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%s reached the intake: the refusal is the client's own and it comes first", strings.Join(tc.args, " "))
			}
			if tc.value != "" && strings.Contains(out, tc.value) {
				t.Errorf("the refusal echoed the value: %s", out)
			}
		})
	}
}

// TestValueNeverInArgsOrEnv: the value comes from stdin (or the hidden prompt a
// terminal gets) and from nowhere else — never an argument, never a flag, never
// the environment — and it reaches the intake in the request body, base64 as
// Secret Manager's own payloads are.
func TestValueNeverInArgsOrEnv(t *testing.T) {
	intakeFixture(t)
	seen := fakeIntakeCalls(t, nil)

	// An argument is refused, and the value is in no sentence and no output.
	out, errOut, err := execute(t, "secrets", "user", "set", "github-token", intakeTestValue)
	if err == nil {
		t.Fatal("a value passed as an argument was accepted: arguments land in shell history and process lists")
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused invocation reached the intake (%d calls)", len(*seen))
	}
	for _, s := range []string{out, errOut, err.Error()} {
		if strings.Contains(s, intakeTestValue) {
			t.Errorf("the value was echoed back: %q", s)
		}
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Errorf("the refusal does not say where the value comes from: %v", err)
	}

	// The environment is not a source either: a value sitting in one is not
	// read, and what stdin carries is what the intake is sent.
	const envValue = "fake-environment-value-not-a-credential"
	t.Setenv("FUGARO_SECRET_VALUE", envValue)
	t.Setenv("FUGARO_VALUE", envValue)
	*seen = (*seen)[:0]
	if _, _, err := executeStdin(t, intakeTestValue+"\n", "secrets", "user", "set", "github-token"); err != nil {
		t.Fatalf("storing from stdin: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("the store made %d calls, want one", len(*seen))
	}
	req, ok := (*seen)[0].Call.Body.(intake.CreateRequest)
	if !ok {
		t.Fatalf("the store sent %#v, want the intake's CreateRequest", (*seen)[0].Call.Body)
	}
	if req.Name != "github-token" || req.Scope != intake.ScopeUser {
		t.Errorf("the store sent %+v, want the name the caller typed in their own scope", req)
	}
	if want := base64.StdEncoding.EncodeToString([]byte(intakeTestValue)); req.Value != want {
		t.Errorf("the store sent a value the intake cannot read back from stdin: the body is base64 of what was typed (%t)", req.Value != want)
	}
	if strings.Contains(req.Value, envValue) || req.Value == base64.StdEncoding.EncodeToString([]byte(envValue)) {
		t.Errorf("the store read the value out of the environment: a value comes from stdin and a hidden prompt only")
	}

	// And with nothing on stdin it refuses rather than falling back to the
	// environment, and reaches nothing.
	*seen = (*seen)[:0]
	_, _, err = executeStdin(t, "", "secrets", "user", "set", "github-token")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty stdin with a value in the environment answered %v, want the empty-value refusal", err)
	}
	if len(*seen) != 0 {
		t.Errorf("the refusal still called the intake: nothing may be stored")
	}
}

// TestTheIntakeCallIsTheRoutesOwnCall: the call itself, against a service a
// test runs — the route the intake declares, the caller's own Google ID token
// as the bearer, a body carrying the value base64 (Secret Manager's own
// payloads are) and an answer read strictly, with the intake's typed refusals
// coming back as this CLI's and a service that does not serve the routes named
// as the deployment it is.
func TestTheIntakeCallIsTheRoutesOwnCall(t *testing.T) {
	var (
		path, auth string
		body       intake.CreateRequest
		status     int
		answer     any
		redirects  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		if r.URL.Path == "/elsewhere" {
			// A followed redirect would come back here a second time — with the
			// caller's own identity token in the second request's header.
			redirects++
			http.Redirect(w, r, srvURLPlaceholder, http.StatusTemporaryRedirect)
			return
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case status == http.StatusNotFound:
			http.NotFound(w, r)
		case status != 0:
			credsvc.WriteWireError(w, status, credsvc.WireError{Error: intake.ErrorNotAdmin})
		default:
			_ = json.NewEncoder(w).Encode(answer)
		}
	}))
	t.Cleanup(srv.Close)
	srvURLPlaceholder = srv.URL
	prevIdentity := secretsIntakeIdentity
	secretsIntakeIdentity = func(context.Context, string, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { secretsIntakeIdentity = prevIdentity })

	value := []byte(intakeTestValue)
	req := intake.CreateRequest{Scope: intake.ScopeUser, Name: "github-token", Value: base64.StdEncoding.EncodeToString(value)}
	var got intake.SecretAnswer
	answer = intake.SecretAnswer{ID: "fugaro_u_1234_github-token", Version: "3", Created: true}
	if err := secretsIntakeOver(context.Background(), srv.URL, secretsIntakeCall{
		Path: intake.CreatePath, What: "a secret of yours is stored", Body: req, Out: &got,
	}); err != nil {
		t.Fatalf("the call failed against a service that answers the route: %v", err)
	}
	if path != intake.CreatePath {
		t.Errorf("the call asked %q, want the intake's create route (%q)", path, intake.CreatePath)
	}
	if auth != "Bearer "+fakeLauncherToken {
		t.Errorf("the call presented %q, want the caller's own Google ID token: the intake's launcher class verifies that token and no other identity", auth)
	}
	if body.Name != req.Name || body.Scope != req.Scope || body.Value != req.Value {
		t.Errorf("the call sent %+v, want the request it was given (%+v)", body, req)
	}
	if got.ID != "fugaro_u_1234_github-token" || got.Version != "3" || !got.Created {
		t.Errorf("the call read %+v, want the answer the service gave", got)
	}

	// The intake's own refusal is what the caller reads, and it is a user error.
	status = http.StatusForbidden
	err := secretsIntakeOver(context.Background(), srv.URL, secretsIntakeCall{Path: intake.ListPath, Body: intake.ListRequest{Scope: intake.ScopeShared}, Out: &intake.ListAnswer{}})
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a refusal by the intake answered %v (exit %d), want the user's own error", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), intake.ErrorNotAdmin) {
		t.Errorf("the refusal does not name the intake's own type: %v", err)
	}

	// A service that does not serve the routes is the deployment, not the
	// caller: it is named with the command that deploys the intake.
	status = http.StatusNotFound
	err = secretsIntakeOver(context.Background(), srv.URL, secretsIntakeCall{Path: intake.ListPath, Body: intake.ListRequest{Scope: intake.ScopeUser}, Out: &intake.ListAnswer{}})
	if err == nil || !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("a service that does not serve the intake's routes answered %v, want the refusal that names `fugaro update`", err)
	}

	// Nothing is followed: the request carries the caller's own token, and a
	// redirect would hand it to whatever host the Location named.
	status = 0
	err = secretsIntakeOver(context.Background(), srv.URL, secretsIntakeCall{Path: "/elsewhere", Body: intake.ListRequest{Scope: intake.ScopeUser}, Out: &intake.ListAnswer{}})
	if redirects != 1 {
		t.Errorf("the redirect target was reached %d times, want once: a followed 307 would carry the caller's own identity token to whatever host the Location named", redirects)
	}
	if err == nil {
		t.Error("a 307 answer was read as the route's own answer")
	}
}

// srvURLPlaceholder is the redirect target the test above hands the handler
// after the server is built (the handler is what needs the address, and the
// address is what the server is).
var srvURLPlaceholder string

// TestIntakeAddressIsTheServicesOwnLabelOver: the intake's address is read from
// the installation's own credentials service address, and every way that can
// fail is a refusal naming the step that fixes it rather than a call to a host
// that answers nobody.
func TestIntakeAddressIsTheServicesOwnLabelOver(t *testing.T) {
	lc := func(u string) *localcfg.Config {
		if u == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: u}}
	}
	got, err := intakeURLOf(lc(intakeTestCredentialsURL))
	if err != nil || got != intakeTestIntakeURL {
		t.Errorf("the intake's address = %q, %v; want %q: both are Cloud Run services of one installation and differ in the service's own label", got, err, intakeTestIntakeURL)
	}
	for _, tc := range []struct {
		name string
		lc   *localcfg.Config
		want string
	}{
		{"no credentials service block", lc(""), "fugaro update"},
		{"an address that is not Cloud Run's", lc("https://credentials.example.com"), "fugaro update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := intakeURLOf(tc.lc); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("intakeURLOf answered %q, %v; want a refusal naming %q", got, err, tc.want)
			}
		})
	}
}
