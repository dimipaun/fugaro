package cli

import (
	"strings"
	"testing"
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
	if strings.Contains(out, "credentials") {
		t.Fatalf("fugaro --help lists the credentials service's command:\n%s", out)
	}
	out, _, err = execute(t, "credentials", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "serve") {
		t.Fatalf("fugaro credentials --help lists serve:\n%s", out)
	}
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

// And with its environment complete, the command still refuses to serve: the
// service's own code (plan task C1.11) is not in this build, so there is
// nothing to serve, and a command that bound a port and answered a run with
// something that is not the service would be worse than one that does not
// start. The refusal has to name what is missing -- an operator who deploys
// this image is owed the reason -- and it is a user error, so the revision
// fails to start rather than serving.
func TestCredentialsServeRefusesToServeWithNoServiceInTheBuild(t *testing.T) {
	t.Setenv(envProject, "fugaro")
	t.Setenv(envGCPProject, "fugaro-dev")
	t.Setenv(envRegion, "us-east5")
	_, _, err := execute(t, "credentials", "serve")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error (exit 1)", err)
	}
	for _, want := range []string{"credentials service", "C1.11"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
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
