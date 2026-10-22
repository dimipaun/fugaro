package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/credsvc"
)

// The credentials service's entry point (plan task C1.14) is not a command
// anyone is offered: it is what the image's own entrypoint runs, in the Cloud
// Run service the installation module creates, and it takes no argument, no
// flag and no config file. So it is hidden twice over -- the group and the
// subcommand -- and the root help is where that is checked, because a hidden
// cobra command is one that does not appear there and nowhere else.
func TestCredentialsServeHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "credentials") {
		t.Fatalf("fugaro --help lists the credentials service's command:\n%s", out)
	}
	out, _, err = execute(t, "credentials", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro credentials --help lists serve:\n%s", out)
	}
	// And the hiding is the commands' own Hidden field, not an accident of
	// cobra's rules. The help output above would still be clean without it: a
	// group with no Run of its own and only hidden subcommands is left out of
	// its parent's list anyway, and the day this group gains a visible
	// subcommand `fugaro --help` would list it. The field is what the image's
	// entrypoint is held to.
	root := NewRootCmd()
	group := findSub(t, root, "credentials")
	if !group.Hidden {
		t.Error("the credentials group is not hidden: fugaro --help would list it as soon as it gains a visible subcommand")
	}
	if !findSub(t, group, "serve").Hidden {
		t.Error("credentials serve is not hidden: fugaro credentials --help would list it")
	}
}

// offersCommand reports whether a help page offers a command called name:
// cobra lists a command as an indented line whose first field is its name
// ("  credentials  Run the credentials service ..."). It is a line test and
// not a substring test on purpose -- the `creds` command's own Short names the
// credentials service ("Set and list the credentials service's one bundle
// secret"), so a substring test would fail on a tree where nothing is wrong
// (this test failed exactly that way when C1.25's command landed after C1.14
// was written).
func offersCommand(help, name string) bool {
	for _, line := range strings.Split(help, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

// findSub returns the named direct subcommand of cmd, failing when there is
// none: a test that walked an empty list instead would pass for the wrong
// reason.
func findSub(t *testing.T, cmd *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("%s has no %q subcommand", cmd.CommandPath(), name)
	return nil
}

// The service's environment is the installation module's: credentials.tf sets
// seven names on the service's container, internal/infra/tf's
// credentialsServiceEnv is the allow-list that pins exactly those, and
// credentialsServiceEnvNames is what this command reads. A service started
// without one of them cannot say which installation it is serving, so the
// command refuses by name before anything else -- the same refusal the history
// job makes for the same variables, and one that never quotes the value it
// found (or did not find).
func TestCredentialsServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range credentialsServiceEnvNames {
		t.Run(k, func(t *testing.T) {
			// Every name is set, then the one under test is emptied: an empty
			// variable is not a value the module can set (Terraform writes
			// each of these from an input the installation has), so the
			// refusal must be about that name and not about another.
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			t.Setenv(k, "")
			_, _, err := execute(t, "credentials", "serve")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
		})
	}
}

// credentialsServeEnv is a complete, valid service environment: the values the
// installation module would set, with the Fugaro project, GCP project and
// Firebase project the golden fixtures use (internal/infra/testdata's
// installation-budget-job.tfvars.json) so the derived names here are the names
// a real installation derives.
var credentialsServeEnv = map[string]string{
	envProject:               "aurora",
	envGCPProject:            "proj-1234",
	envRegion:                "us-east5",
	envRTDBURL:               "https://aurora-fp-default-rtdb.firebaseio.com",
	envFirebaseFP:            "aurora-fp",
	envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app",
	envJobAccounts:           `[{"provider":"github","repo":"acme/webapp","workflow":"api"}]`,
}

// TestCredentialsServiceConfigBuildsFromAFullEnvironment is the headline: the
// whole of this task is that the service can be configured at all, so a
// complete environment must build a credsvc.Config that NewServer accepts --
// the object the host below serves. Until this task the builder could not: it
// read the three names the module set and refused, because the four facts a
// Config needs (the database, the Firebase project, the audience and the job
// accounts) were not in the container.
//
// The two things the builder needs that reach Google as the service itself --
// the token source the database is read with and the Secret Manager client the
// bundle is read through -- are the test seam (credentialsTest); nothing else
// here is faked, so the derivation of the job accounts and every validation
// the verifier makes are the production ones.
//
// The derived table is pinned by value and not by comparison with the
// function that made it: these emails are the ones internal/infra deploys
// (gcp.ServiceAccountID over task.Slug), so a change in either derivation
// fails here rather than in a run that cannot authenticate.
func TestCredentialsServiceConfigBuildsFromAFullEnvironment(t *testing.T) {
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	fakeCredentialsService(t)

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig: %v", err)
	}
	want := []credsvc.JobAccount{{
		Email: "fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com",
		Slug:  "acme-webapp-cf67624405f4121a",
		Repo:  "github/acme/webapp",
	}}
	if len(cfg.JobAccounts) != 1 || cfg.JobAccounts[0] != want[0] {
		t.Fatalf("job accounts = %+v, want %+v", cfg.JobAccounts, want)
	}
	// Every piece NewServer requires is built here, and the one thing that is
	// deliberately left empty is the shared project jobs' accounts (Global
	// Constraint 6: per-repository jobs only until C3.9).
	for _, missing := range []struct {
		name string
		set  bool
	}{
		{"Auth", cfg.Auth != nil},
		{"Bundles", cfg.Bundles != nil},
		{"Policy", cfg.Policy != nil},
		{"State", cfg.State != nil},
		{"Kills", cfg.Kills != nil},
		{"Audit", cfg.Audit != nil},
		{"DB", cfg.DB != nil},
		{"Probes", cfg.Probes != nil},
		{"AuditLog", cfg.AuditLog != nil},
		{"Log", cfg.Log != nil},
	} {
		if !missing.set {
			t.Errorf("the configuration has no %s: the service cannot serve without it", missing.name)
		}
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the configuration a complete environment builds: %v", err)
	}
}

// fakeCredentialsService replaces the two constructors that would need
// Application Default Credentials with ones that build nothing: the token
// source is never asked for a token (nothing in the configuration reads the
// database) and the Secret Manager client is never asked for a version.
func fakeCredentialsService(t *testing.T) {
	t.Helper()
	credentialsTest = &credentialsWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return oauth2.StaticTokenSource(&oauth2.Token{}), nil
		},
		secrets: func(context.Context) (*secretmanager.Service, error) { return &secretmanager.Service{}, nil },
	}
	t.Cleanup(func() { credentialsTest = nil })
}

// TestCredentialsServiceConfigRefusesAMalformedVariable: every name has a
// shape, and a value that is not it is refused by name -- before the
// configuration is built, so a deployment that is wrong never reaches a
// database read or a caller. The three that matter most are the ones a wrong
// value would silently mis-serve with: a database URL that is another
// installation's (the pair with the Firebase project, which is what a copied
// environment looks like), an audience that is not this service's URL (a
// service that admits nobody, or the wrong somebody), and a job account table
// that cannot be derived (a service whose account names do not exist).
func TestCredentialsServiceConfigRefusesAMalformedVariable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   map[string]string
		names []string // the variables the refusal must name
	}{
		{
			name:  "the database URL is not one",
			set:   map[string]string{envRTDBURL: "aurora-fp-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database URL has a path",
			set:   map[string]string{envRTDBURL: "https://aurora-fp-default-rtdb.firebaseio.com/fugaro"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database is another Firebase project's",
			set:   map[string]string{envRTDBURL: "https://other-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL, envFirebaseFP},
		},
		{
			name:  "the audience has no scheme",
			set:   map[string]string{envCredentialsServiceURL: "fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience is http",
			set:   map[string]string{envCredentialsServiceURL: "http://fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience carries a path",
			set:   map[string]string{envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app/v1/messages"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the job accounts are not JSON",
			set:   map[string]string{envJobAccounts: "acme/webapp api"},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are an object",
			set:   map[string]string{envJobAccounts: `{"provider":"github"}`},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are empty",
			set:   map[string]string{envJobAccounts: `[]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job has no workflow",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job carries a field nothing reads",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp","workflow":"api","project":"aurora"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's provider is not a provider kind",
			set:   map[string]string{envJobAccounts: `[{"provider":"GitHub","repo":"acme/webapp","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's repository cannot be named",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			// No seam: every refusal below is made before the configuration
			// reaches Google, which is the property this pins -- a deployment
			// that is wrong costs nothing but the refusal.
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			for _, n := range tc.names {
				if !strings.Contains(err.Error(), n) {
					t.Fatalf("err = %v, want a refusal naming %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceRefusalsNeverCarryAValue: this process is the one that
// holds the bundle's values in memory, and every refusal below is a line an
// operator reads in a log, so none of them may quote what the environment
// held. The values here are shaped like a credential (and deliberately not
// like the names the installation really uses), and every refusal path is
// walked with them set: the variable that is wrong is the one that gets
// named, never the text that was in it, and never the text in the others.
func TestCredentialsServiceRefusalsNeverCarryAValue(t *testing.T) {
	secret := func(n string) string { return "s3cr3t-" + strings.ToLower(strings.TrimPrefix(n, "FUGARO_")) }
	values := map[string]string{
		envProject:               secret(envProject),
		envGCPProject:            secret(envGCPProject),
		envRegion:                secret(envRegion),
		envRTDBURL:               "https://" + secret(envFirebaseFP) + "-default-rtdb.firebaseio.com",
		envFirebaseFP:            secret(envFirebaseFP),
		envCredentialsServiceURL: "https://" + secret(envCredentialsServiceURL) + ".run.app",
		envJobAccounts:           `[{"provider":"github","repo":"` + secret("repo") + `","workflow":"api"}]`,
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"every name is missing", map[string]string{
			envProject: "", envGCPProject: "", envRegion: "", envRTDBURL: "",
			envFirebaseFP: "", envCredentialsServiceURL: "", envJobAccounts: "",
		}},
		{"the database URL is not one", map[string]string{envRTDBURL: "s3cr3t-db"}},
		{"the database is another project's", map[string]string{envRTDBURL: "https://s3cr3t-other-default-rtdb.firebaseio.com"}},
		{"the audience is malformed", map[string]string{envCredentialsServiceURL: "s3cr3t-url"}},
		{"the job accounts are malformed", map[string]string{envJobAccounts: "s3cr3t-jobs"}},
		{"a job cannot be derived", map[string]string{envJobAccounts: `[{"provider":"github","repo":"s3cr3t-nope","workflow":"api"}]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for n, v := range values {
				t.Setenv(n, v)
			}
			for n, v := range tc.set {
				t.Setenv(n, v)
			}
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil {
				t.Fatal("want a refusal")
			}
			for n, v := range values {
				if v == "" {
					continue
				}
				if strings.Contains(err.Error(), v) {
					t.Errorf("err = %v, which carries the value of %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceEnvIsTheDeploymentsOwn: the seven names this command
// reads are the seven credentials.tf sets on the service's container, and
// neither side may move without the other. The direction that matters is the
// module setting a name this command does not read: an operator reading
// credentials.tf would believe the fact was in effect while the service
// refused to start without it (or, worse, served with a value it never read).
// The other direction is a name this command reads that nothing sets, which is
// a fact the service silently does not have.
//
// internal/infra/tf's credentialsServiceEnv is the third side of this, and it
// is held to the module by that package's own test (the cover's allow-list
// refuses a container that sets a name outside it, which is what makes the
// module's list a boundary rather than a convention).
func TestCredentialsServiceEnvIsTheDeploymentsOwn(t *testing.T) {
	tf := readRepoFile(t, "deploy/terraform/gcp/modules/installation/credentials.tf")
	inModule := map[string]bool{}
	for _, m := range regexp.MustCompile(`name\s*=\s*"(FUGARO_[A-Z0-9_]+)"`).FindAllStringSubmatch(tf, -1) {
		inModule[m[1]] = true
	}
	for _, n := range credentialsServiceEnvNames {
		if !inModule[n] {
			t.Errorf("this command reads %s, which credentials.tf does not set on the service: the service would refuse to start on a deployment that is right", n)
		}
		delete(inModule, n)
	}
	for n := range inModule {
		t.Errorf("credentials.tf sets %s, which this command does not read: an operator would believe the fact was in effect and the service would refuse to start without it", n)
	}
}

// TestCredentialsServePort: PORT is the platform's, and a value that is not a
// port is a deployment that is wrong -- refused by name rather than defaulted,
// because a service listening where the front end does not reach it looks
// started and answers nothing.
func TestCredentialsServePort(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		bad      bool
	}{
		{in: "", want: defaultServePort}, // Cloud Run always sets it; a developer's run does not
		{in: "8080", want: "8080"},
		{in: "1", want: "1"},
		{in: "65535", want: "65535"},
		{in: "0", bad: true},
		{in: "65536", bad: true},
		{in: "-1", bad: true},
		{in: "http", bad: true},
		{in: "8080 ", bad: true}, // strconv.Atoi refuses the space; a port is digits
	} {
		got, err := servePort(func(string) string { return tc.in })
		switch {
		case tc.bad && err == nil:
			t.Errorf("servePort(%q) = %q, want a refusal", tc.in, got)
		case tc.bad:
			if ExitCode(err) != ExitUserError {
				t.Errorf("servePort(%q) err = %v, want a user error", tc.in, err)
			}
		case err != nil:
			t.Errorf("servePort(%q): %v", tc.in, err)
		case got != tc.want:
			t.Errorf("servePort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCredentialsServeHostStopsOnSignal drives the host's own machinery: a
// real listener, a real request and a real shutdown, with the handler of a
// test rather than the service's (the service needs the deployment's facts
// above, and none of them is what this asserts). What it pins is what the
// owner asked this task for -- the port the platform gives (PORT), the bounds
// a long-lived streamed call allows, and a SIGTERM that drains instead of
// dying -- and it pins them on the object the service is served by, so a
// timeout added to it fails here.
func TestCredentialsServeHostStopsOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := credentialsHTTPServer(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "answered")
	}), &logs)
	// The bounds, stated as the values and not as "non-zero": a
	// ReadHeaderTimeout is what stops a caller that connects and says nothing
	// from holding a connection for ever, and a WriteTimeout (or a
	// ReadTimeout) would cut a model call in flight, which is a run that fails
	// for a reason nothing in its record explains.
	if srv.ReadHeaderTimeout != serveReadHeaderTimeout || srv.IdleTimeout != serveIdleTimeout {
		t.Errorf("host bounds = read-header %s, idle %s, want %s and %s", srv.ReadHeaderTimeout, srv.IdleTimeout, serveReadHeaderTimeout, serveIdleTimeout)
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Errorf("the host sets WriteTimeout %s / ReadTimeout %s: a proxied streaming call is long-lived and either would cut it", srv.WriteTimeout, srv.ReadTimeout)
	}
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, ln, srv, &logs) }()
	// The listener is serving: a request is answered before the signal.
	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/messages")
	if err != nil {
		t.Fatalf("the host did not answer: %v\n%s", err, logs.String())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "answered" {
		t.Fatalf("host answered %d %q", resp.StatusCode, body)
	}
	// The signal: the context ends, and the host returns with no error -- a
	// platform that is replacing this revision is not a failure.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntilStop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilStop did not return after the signal")
	}
	if !strings.Contains(logs.String(), "listening on") {
		t.Errorf("the host's own line does not say what it listens on: %q", logs.String())
	}
}

// TestCredentialsServeRefusesAnUnbuildableConfiguration: NewServer names the
// piece a configuration is missing (credsvc.NewServer's own refusals), and the
// host turns that into a user error -- a revision whose deployment is wrong
// must fail to start, not serve a service that answers every call with a
// refusal.
func TestCredentialsServeRefusesAnUnbuildableConfiguration(t *testing.T) {
	err := hostCredentialsService(context.Background(), credsvc.Config{}, func(string) string { return "" }, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("err = %v, want NewServer's own refusal (the missing verifier)", err)
	}
}

// GODEBUG=http2debug makes Go print every HTTP request it handles, headers
// included, to stderr -- and this process handles the calls that carry a
// model credential. The refusal is the one every command that touches a
// credential makes (refuseHTTP2Debug), and it comes before the environment is
// read, so nothing at all happens under it.
func TestCredentialsServeRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "credentials", "serve")
	if err == nil || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("err = %v, want the http2debug refusal", err)
	}
}
