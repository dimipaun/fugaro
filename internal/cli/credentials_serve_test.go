package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
// FUGARO_PROJECT, FUGARO_GCP_PROJECT and FUGARO_REGION on the service, and
// internal/infra/tf's credentialsServiceEnv is the allow-list that pins those
// three names and no others. A service started without one of them cannot say
// which project it is serving, so the command refuses by name before anything
// else -- the same refusal the history job makes for the same variables.
func TestCredentialsServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range []string{envProject, envGCPProject, envRegion} {
		t.Run(k, func(t *testing.T) {
			// The three names are set, then the one under test is emptied: an
			// empty variable is not a value the module can set.
			t.Setenv(envProject, "fugaro")
			t.Setenv(envGCPProject, "fugaro-dev")
			t.Setenv(envRegion, "us-east5")
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

// With its environment complete, the command still refuses -- and the refusal
// names what the deployment is missing rather than a piece of this build.
// internal/credsvc carries the service (C1.11) and NewServer is what the host
// below starts, but a credsvc.Config cannot be built from the three names the
// installation module sets on the container: the service's realtime database
// URL, its Firebase project, its own audience and the installation's job
// service accounts are facts of the deployment, none of them derivable from
// those three, and inventing any of them is a service that admits a caller it
// should not. The refusal is a user error, so a revision deployed without them
// fails to start rather than serving something a run would act on.
func TestCredentialsServeRefusesWithoutTheDeploymentsOwnFacts(t *testing.T) {
	t.Setenv(envProject, "fugaro")
	t.Setenv(envGCPProject, "fugaro-dev")
	t.Setenv(envRegion, "us-east5")
	_, _, err := execute(t, "credentials", "serve")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error (exit 1)", err)
	}
	// It names the four facts, and the task that puts them on the container,
	// so an operator who deploys this image is owed the reason and the fix.
	for _, want := range []string{"realtime database URL", "Firebase project", "audience", "job service accounts", "C1.18"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	// And it never claims the service is absent from this build: C1.11 is in
	// it, and a refusal that said otherwise would send an operator looking for
	// the wrong thing (PR #872's stub said exactly that, on a base where it was
	// true).
	if strings.Contains(err.Error(), "C1.11") {
		t.Fatalf("err = %v, but internal/credsvc carries the service (C1.11) in this build", err)
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
