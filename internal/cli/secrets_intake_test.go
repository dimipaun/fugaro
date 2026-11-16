package cli

import (
	"bytes"
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
	"github.com/dimipaun/fugaro/internal/secretname"
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

// intakeJSONCase is one of the intake families' --json fixtures
// (report_json_fixtures_test.go): the rig is the installation's own config —
// the credentials service whose address the intake's own is read from — and the
// one seam every call goes through, so the run reaches the document the command
// prints with no service, no token and no value on the wire.
func intakeUserJSONCase(verb, stdin string, reads bool) jsonCase {
	return intakeJSONCase(intake.ScopeUser, verb, stdin, reads)
}

func intakeSharedJSONCase(verb, stdin string, reads bool) jsonCase {
	return intakeJSONCase(intake.ScopeShared, verb, stdin, reads)
}

func intakeJSONCase(scope intake.Scope, verb, stdin string, reads bool) jsonCase {
	argv := []string{"secrets", intakeScopeWord(scope), verb}
	if verb != "ls" {
		argv = append(argv, "github-token")
	}
	return jsonCase{
		stdin: stdin,
		reads: reads,
		run: func(t *testing.T) []string {
			intakeFixture(t)
			fakeIntakeCalls(t, intakeAnswerFor(scope))
			return argv
		},
	}
}

// intakeAnswerFor fills whichever of the intake's three answers the verb asked
// for, the way the route the command called would have: the document is the
// service's own (internal/intake), so what --json prints is what the intake
// would have written.
func intakeAnswerFor(scope intake.Scope) func(c secretsIntakeCall) {
	return func(c secretsIntakeCall) {
		switch out := c.Out.(type) {
		case *intake.SecretAnswer:
			*out = intake.SecretAnswer{ID: intakeTestID(scope), Version: "3", Created: true}
		case *intake.ListAnswer:
			*out = intake.ListAnswer{Secrets: []intake.ListedSecret{
				{ID: intakeTestID(scope), Name: "github-token", Scope: scope, Created: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)},
			}}
		case *intake.DeleteAnswer:
			*out = intake.DeleteAnswer{Deleted: []string{intakeTestID(scope)}}
		}
	}
}

// intakeTestID is the secret ID one scope's grammar builds for the name the
// fixtures store, which is what the intake answers with.
func intakeTestID(scope intake.Scope) string {
	if scope == intake.ScopeShared {
		return secretname.ProjectPrefix + "github-token"
	}
	return secretname.UserPrefix + "1234_github-token"
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
		name     string
		env      string
		envValue string
		args     []string
		value    string
	}{
		{"an agent's session stores", "CLAUDECODE", "", []string{"secrets", "user", "set", "github-token"}, intakeTestValue},
		{"an agent's session lists", "CLAUDECODE", "", []string{"secrets", "user", "ls"}, ""},
		{"a run's process stores", "FUGARO_RUN", "", []string{"secrets", "shared", "set", "npm-token"}, intakeTestValue},
		{"a run's process removes", "CLOUD_RUN_JOB", "", []string{"secrets", "shared", "rm", "npm-token"}, ""},
		// The third refusal in the order: with http2debug on, Go prints the
		// request — the caller's own token in its header — to stderr, so the
		// command is refused before the token is minted at all.
		{"Go would print the request", "GODEBUG", "http2debug=2", []string{"secrets", "user", "set", "github-token"}, intakeTestValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intakeFixture(t)
			envValue := tc.envValue
			if envValue == "" {
				envValue = "1"
			}
			t.Setenv(tc.env, envValue)
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
	// The value on stdin is what makes this a pin on the argument guard rather
	// than on an empty read: with the guard gone the run would store what stdin
	// carries, under the name the argument took, and the call below is what
	// says it does not.
	out, errOut, err := executeStdin(t, intakeTestValue+"\n", "secrets", "user", "set", "github-token", intakeTestValue)
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
	if !strings.Contains(err.Error(), "never as an argument") {
		t.Errorf("the refusal does not say what was wrong with the invocation: %v", err)
	}

	// A value-shaped flag: pflag quotes an unknown flag back, which is why the
	// family sets its own flag error (creds_test.go holds fugaro creds to the
	// same case). The value is in no sentence, and nothing is sent.
	out, errOut, err = execute(t, "secrets", "user", "set", "github-token", "--"+intakeTestValue)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a value-shaped flag answered %v, want the family's own refusal", err)
	}
	for _, s := range []string{out, errOut, err.Error()} {
		if strings.Contains(s, intakeTestValue) {
			t.Errorf("the value-shaped flag was echoed back: %q", s)
		}
	}
	// The family's own sentence and not the parent's: `secrets` answers an
	// unknown flag with the flags its own verbs take (--repo, --project-wide,
	// --allow-data-to …), which are not this family's.
	if !strings.Contains(err.Error(), "unknown or malformed flag; secrets user commands take --json") {
		t.Errorf("the flag error is not the family's own: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("a refused flag still reached the intake (%d calls)", len(*seen))
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

// TestTheIntakeFamilyTakesOneLineValues: a value that spans lines is refused,
// and the refusal names the command that stores one rather than a name this
// family writes and the intake refuses again — `github-app-key` is a reserved
// platform kind (R7), so the PEM the old family stores under it has no business
// here, and the set help says so.
func TestTheIntakeFamilyTakesOneLineValues(t *testing.T) {
	intakeFixture(t)
	seen := fakeIntakeCalls(t, nil)
	const pem = "-----BEGIN PRIVATE KEY-----\nfake-pem-body-not-a-credential\n-----END PRIVATE KEY-----\n"

	_, _, err := executeStdin(t, pem, "secrets", "user", "set", "deploy-key")
	if err == nil {
		t.Fatal("a value spanning several lines was accepted: no name this family writes may carry one")
	}
	if !strings.Contains(err.Error(), "spans several lines") {
		t.Errorf("the refusal does not say what was wrong with the value: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro secrets set "+multilineSecret) {
		t.Errorf("the refusal does not name the command that stores a PEM: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("a refused value still reached the intake (%d calls)", len(*seen))
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
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path, auth = r.URL.Path, r.Header.Get("Authorization")
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

	// An address with a trailing slash is the same address: the check admits
	// one, and the route appended to it is the intake's own path rather than
	// one with the slash doubled.
	path = ""
	if err := secretsIntakeOver(context.Background(), srv.URL+"/", secretsIntakeCall{
		Path: intake.CreatePath, Body: req, Out: &got,
	}); err != nil {
		t.Errorf("the call to the same address with a trailing slash failed: %v", err)
	}
	if path != intake.CreatePath {
		t.Errorf("the address with a trailing slash asked %q, want the intake's route (%q)", path, intake.CreatePath)
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

}

// TestTheIntakeCallRefusesAnAnswerItDoesNotReadWhole: the answer is read
// strictly in both directions Convention 6 names — a field this release does not
// know, and a second document after the first — as the intake's own reader
// refuses them on the request side. An answer is the service's text and this
// call is the only reader of it, so what it will not read it must not half-read.
func TestTheIntakeCallRefusesAnAnswerItDoesNotReadWhole(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prevIdentity := secretsIntakeIdentity
	secretsIntakeIdentity = func(context.Context, string, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { secretsIntakeIdentity = prevIdentity })
	call := secretsIntakeCall{
		Path: intake.CreatePath,
		Body: intake.CreateRequest{Scope: intake.ScopeUser, Name: "github-token", Value: base64.StdEncoding.EncodeToString([]byte(intakeTestValue))},
	}

	const one = `{"id":"fugaro_u_1234_github-token","version":"3","created":true}`
	body = one
	call.Out = &intake.SecretAnswer{}
	if err := secretsIntakeOver(context.Background(), srv.URL, call); err != nil {
		t.Errorf("the intake's own answer was refused: %v", err)
	}

	body = one + ` ` + one
	call.Out = &intake.SecretAnswer{}
	err := secretsIntakeOver(context.Background(), srv.URL, call)
	if err == nil || !strings.Contains(err.Error(), "more than one document") {
		t.Errorf("an answer carrying two documents was read as %v, want the refusal of a second one", err)
	}

	body = `{"id":"fugaro_u_1234_github-token","version":"3","created":true,"unknown":1}`
	call.Out = &intake.SecretAnswer{}
	err = secretsIntakeOver(context.Background(), srv.URL, call)
	if err == nil || !strings.Contains(err.Error(), "not the document this call reads") {
		t.Errorf("an answer carrying a field this release does not know was read as %v, want the refusal of the shape", err)
	}
}

// TestTheIntakeCallReadsABoundedAnswer: the answer is another writer's and it is
// rendered into this process's memory, so its size is judged before its shape —
// a service that answers without end cannot make this CLI hold it
// (run_slip_test.go's grant test is the same one for the launcher's own call).
// The document is grown in a field the strict decode reads, so the size alone
// is what refuses the second answer and not an unknown one.
func TestTheIntakeCallReadsABoundedAnswer(t *testing.T) {
	const id = "fugaro_u_1234_github-token"
	base, err := json.Marshal(intake.SecretAnswer{ID: id, Version: "3", Created: true})
	if err != nil {
		t.Fatal(err)
	}
	answer := func(extra int) string {
		pad := maxIntakeAnswerBytes - len(base) + extra
		if pad < 0 {
			t.Fatalf("the answer's own shape is %d bytes, larger than the bound (%d)", len(base), maxIntakeAnswerBytes)
		}
		doc, err := json.Marshal(intake.SecretAnswer{ID: id + strings.Repeat("a", pad), Version: "3", Created: true})
		if err != nil {
			t.Fatal(err)
		}
		return string(doc)
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prevIdentity := secretsIntakeIdentity
	secretsIntakeIdentity = func(context.Context, string, string) (string, error) { return fakeLauncherToken, nil }
	t.Cleanup(func() { secretsIntakeIdentity = prevIdentity })
	call := secretsIntakeCall{
		Path: intake.CreatePath,
		Body: intake.CreateRequest{Scope: intake.ScopeUser, Name: "github-token", Value: base64.StdEncoding.EncodeToString([]byte(intakeTestValue))},
	}

	body = answer(0)
	if len(body) != maxIntakeAnswerBytes {
		t.Fatalf("the fixture is %d bytes, want exactly the bound (%d)", len(body), maxIntakeAnswerBytes)
	}
	var got intake.SecretAnswer
	call.Out = &got
	if err := secretsIntakeOver(context.Background(), srv.URL, call); err != nil || !strings.HasPrefix(got.ID, id) {
		t.Errorf("an answer of exactly %d bytes was read as %+v, %v, want the document it carries: %d is the bound, not one past it", len(body), got, err, maxIntakeAnswerBytes)
	}

	body = answer(1)
	call.Out = &intake.SecretAnswer{}
	err = secretsIntakeOver(context.Background(), srv.URL, call)
	if err == nil || !strings.Contains(err.Error(), "larger than this call's") {
		t.Errorf("an answer of %d bytes was read as %v, want the refusal of an answer larger than this call's (%d bytes)", len(body), err, maxIntakeAnswerBytes)
	}
}

// TestTheIntakeCallSendsABoundedRequest: the request is a document the intake
// reads under its own bound, so this side judges it by a bound of its own
// before it sends anything. The case that decides that bound is the largest
// request a person may legally make — a value at Secret Manager's 64 KiB under
// a name at its own longest — because a bound the least bit tighter than the
// intake's would refuse that person's secret here, before it is ever sent, and
// answer them with a sentence about the name. The document is grown in the
// name, which is the only part of it a person can make large: a value over
// 64 KiB is refused before this, by the read that took it
// (TestTheIntakeCallReadsABoundedAnswer is the same test for the answer).
//
// Mutation (verified, run and restored): relax the check by the one byte —
// `len(payload) > maxIntakeBodyBytes+1` — and the last case is sent instead of
// refused; write the duplicated envelope as 1<<8 and the first case is refused
// here before anything is sent, which is a person's legal secret refused.
func TestTheIntakeCallSendsABoundedRequest(t *testing.T) {
	// A name at Secret Manager's longest, over a value at its own: what a
	// person may store, and the request the bound must admit.
	const longestName = 255
	value := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("v"), maxSecretBytes))
	request := func(name string) intake.CreateRequest {
		return intake.CreateRequest{Scope: intake.ScopeUser, Name: name, Value: value}
	}
	// document grows the name until the document is exactly the bound plus
	// extra: 'a' is one byte in JSON however many of them there are, so the
	// document grows with the name one for one. The bound itself is not named
	// here — a test that computed its own would agree with a wrong one.
	document := func(extra int) intake.CreateRequest {
		base, err := json.Marshal(request(strings.Repeat("a", longestName)))
		if err != nil {
			t.Fatal(err)
		}
		grow := maxIntakeBodyBytes + extra - len(base)
		if grow < 0 {
			t.Fatalf("the largest secret a person may store is a %d-byte request, over this call's bound of %d: it would be refused here, before it was sent", len(base), maxIntakeBodyBytes)
		}
		return request(strings.Repeat("a", longestName+grow))
	}

	for _, tc := range []struct {
		name string
		req  intake.CreateRequest
		// want is the refusal's own words for a request that must not be sent,
		// and empty for one that must be.
		want string
	}{
		{"a value at Secret Manager's longest under a name at its own", request(strings.Repeat("a", longestName)), ""},
		{"a request exactly the bound's size", document(0), ""},
		{"a request one byte past it", document(1), "larger than the intake reads"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mints, reached int
				sent           intake.CreateRequest
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached++
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &sent)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(intake.SecretAnswer{ID: "fugaro_u_1234_github-token", Version: "3", Created: true})
			}))
			t.Cleanup(srv.Close)
			prevIdentity := secretsIntakeIdentity
			secretsIntakeIdentity = func(context.Context, string, string) (string, error) {
				mints++
				return fakeLauncherToken, nil
			}
			t.Cleanup(func() { secretsIntakeIdentity = prevIdentity })

			raw, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			var got intake.SecretAnswer
			err = secretsIntakeDo(context.Background(), srv.URL, secretsIntakeCall{
				Path: intake.CreatePath, What: "a secret of yours is stored", Body: tc.req, Out: &got,
			})

			if tc.want == "" {
				if err != nil {
					t.Fatalf("a request of %d bytes was refused, want it inside this call's bound of %d: %v", len(raw), maxIntakeBodyBytes, err)
				}
				if reached != 1 {
					t.Errorf("a request of %d bytes reached the service %d times, want once: it is inside the bound of %d", len(raw), reached, maxIntakeBodyBytes)
				}
				if sent.Name != tc.req.Name || sent.Value != tc.req.Value {
					t.Errorf("the service read a name of %d bytes and a value of %d, want the %d and %d the call was given", len(sent.Name), len(sent.Value), len(tc.req.Name), len(tc.req.Value))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a request of %d bytes was answered %v, want the refusal that names a body %q (the bound is %d)", len(raw), err, tc.want, maxIntakeBodyBytes)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal of an oversized request exits %d, want the user's own error", ExitCode(err))
			}
			if mints != 0 {
				t.Errorf("the caller's own token was minted %d times for a request that was never sent, want none: the bound is judged before a credential is asked for", mints)
			}
			if reached != 0 {
				t.Errorf("a request this call's bound refuses reached the service %d times, want none", reached)
			}
		})
	}
}

// TestTheIntakeCallDoesNotFollowARedirect: a 3xx is not an answer, and the
// request holds the caller's own Google ID token in a header a redirect would
// carry to whatever host the Location named. The second server here is exactly
// that host, and it must never be reached (run_slip_test.go's grant test is the
// same one for the launcher's own call).
//
// Every 3xx is asked, because they differ in what a client that followed one
// would do with this request: 302 and 303 turn the POST into a GET at the new
// host, which is the token carried to it in the header and the body dropped,
// and 307 and 308 send the POST there whole. A client that wants the token to
// reach only the service it judged sends it nowhere else under any of them.
//
// Mutation (verified, run and restored): drop the CheckRedirect from
// secretsIntakeClient and this fails on the request that reached it.
func TestTheIntakeCallDoesNotFollowARedirect(t *testing.T) {
	for _, code := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		reached := 0
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(intake.SecretAnswer{ID: "fugaro_u_1234_github-token", Version: "3", Created: true})
		}))
		t.Cleanup(elsewhere.Close)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+intake.CreatePath, code)
		}))
		t.Cleanup(srv.Close)
		prevIdentity := secretsIntakeIdentity
		secretsIntakeIdentity = func(context.Context, string, string) (string, error) { return fakeLauncherToken, nil }
		t.Cleanup(func() { secretsIntakeIdentity = prevIdentity })

		var answer intake.SecretAnswer
		err := secretsIntakeOver(context.Background(), srv.URL, secretsIntakeCall{
			Path: intake.CreatePath,
			What: "a secret of yours is stored",
			Body: intake.CreateRequest{Scope: intake.ScopeUser, Name: "github-token", Value: base64.StdEncoding.EncodeToString([]byte(intakeTestValue))},
			Out:  &answer,
		})
		if err == nil {
			t.Errorf("the call followed a %d, want a refusal: the caller's own identity token is a bearer credential and the redirect names the host it would go to", code)
		}
		if reached != 0 {
			t.Errorf("a %d reached the host the Location named (%d requests), want none: the token in the header is the person's own", code, reached)
		}
	}
}

// TestTheIntakeCallRefusesAURLItWillNotSendATokenTo: the call carries the
// caller's own Google ID token, which is a bearer credential, so the address it
// is sent to is judged before anything is minted: https, or http on a loopback
// address, which is a test's own service. A URL that is more than a scheme and
// a host is refused too — the route is appended to it, and a base path, a query
// or a fragment would decide where this token goes. The rule is
// checkSlipServiceURL's, and this is its table for the intake's address.
//
// Mutation (verified, run and restored): `return nil` as the first statement of
// checkIntakeServiceURL and this fails on the plain-http cases.
func TestTheIntakeCallRefusesAURLItWillNotSendATokenTo(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://intake.example.com", true},
		{"https://intake.example.com/", true},
		{"https://intake.example.com:8443", true},
		{"http://127.0.0.1:8080", true},
		{"http://localhost:8080", true},
		{"http://intake.example.com", false},
		{"http://10.0.0.1", false},
		{"https://intake.example.com/v1", false},
		{"https://intake.example.com?x=1", false},
		{"https://intake.example.com#f", false},
		{"https://user:pass@intake.example.com", false},
		{"intake.example.com", false},
		{"", false},
	} {
		err := checkIntakeServiceURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("checkIntakeServiceURL(%q) = %v, want refused = %v: this URL is where the caller's own identity token would be sent", c.url, err, !c.want)
		}
		if err != nil && strings.Contains(err.Error(), c.url) && c.url != "" {
			t.Errorf("the refusal of %q repeats the URL: %v", c.url, err)
		}
	}
}

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
