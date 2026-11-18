package cli

// The resident orchestrator's own identity to the installation's credentials
// service (F19, O2; orchestrator_identity.go): the account it impersonates, the
// argv that mint is run with, the two clients that take the same seam, and the
// refusals a machine that cannot mint one gets.
//
// Every test here replaces the two gcloud seams, so no test runs a real gcloud
// and none impersonates a real account. What is NOT replaced is everything
// else: the address is composed, the argv is built and the refusals are written
// by the code under test, and a test that replaced the mint wholesale would pin
// nothing about the identity the daemon actually presents.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// orchestrationIdentityURL is the credentials service's own URL, in the shape
// the installation publishes it: it is also the token's audience, and the two
// are the same string on purpose (orchestratorLabelSeams).
const orchestrationIdentityURL = "https://fugaro-credentials-abc-uc.a.run.app"

// TestOrchestratorIdentityArgsImpersonateTheInstallationsAccount is the argv
// rule the mint's correctness rests on: --impersonate-service-account names the
// account rather than letting gcloud's active configuration decide it (a daemon
// that minted as whatever the operator last logged in as would present the
// launcher class's credential to a route that is not the launcher's),
// --audiences is what only a service account or an impersonated identity may be
// given and is what makes the token the credentials service's own audience, and
// --include-email is what an impersonated mint needs because the elevation call
// defaults the email claim off -- the claim the service looks a caller up by.
func TestOrchestratorIdentityArgsImpersonateTheInstallationsAccount(t *testing.T) {
	const account = "fugaro-orch@proj-1234.iam.gserviceaccount.com"
	got := strings.Join(orchestratorIdentityArgs(orchestrationIdentityURL, account), " ")
	want := "auth print-identity-token --impersonate-service-account=" + account +
		" --audiences=" + orchestrationIdentityURL + " --include-email --quiet"
	if got != want {
		t.Errorf("orchestratorIdentityArgs = %q, want %q", got, want)
	}
}

// TestOrchestratorIdentityComposesTheInstallationsAccount: the address is the
// one account per installation, under the ID the design fixes, in the
// installation's own project -- the same address `fugaro doctor` reads the
// account back by (infra.OrchestratorAccountEmail), which is what makes "the
// account the check reads" and "the account the daemon presents" one answer.
//
// A configuration that names no project is the one arm that refuses, and it
// refuses rather than composing an address out of nothing: a daemon with no
// account has no identity to present, and the sentence says which command
// writes the project.
func TestOrchestratorIdentityComposesTheInstallationsAccount(t *testing.T) {
	got, err := orchestratorDaemonAccount(&localcfg.Config{GCPProject: "proj-1234"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if want := infra.OrchestratorAccountEmail("proj-1234"); got != want {
		t.Errorf("the daemon's account is %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, infra.OrchestratorAccountID+"@") {
		t.Errorf("the daemon's account is %q, want it under the ID the design fixes (%s)", got, infra.OrchestratorAccountID)
	}

	for _, tc := range []struct {
		name string
		lc   *localcfg.Config
	}{{"no configuration at all", nil}, {"a configuration with no project", &localcfg.Config{}}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := orchestratorDaemonAccount(tc.lc)
			if got != "" {
				t.Errorf("the daemon's account is %q, want none", got)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), "fugaro update") {
				t.Errorf("err = %v, want it to say what to run (fugaro update writes the project into this machine's configuration)", err)
			}
		})
	}
}

// TestOrchestratorIdentityIsBothClientsSeam: the label route's client and the
// merge mint take their identity as a field of the same name and the same
// signature, and this is the one seam both are given
// (orchestratorIdentityMint). It is a compile-time pin on purpose -- a second
// mint for the second route is a second answer to "which account is this
// daemon", and this is the line that fails when one is added.
//
// The mint is built for the account and the URL this daemon has, so neither can
// be passed wrongly by a caller, and the empty arm is the refusal a daemon with
// an incomplete configuration gets rather than a call to the service.
func TestOrchestratorIdentityIsBothClientsSeam(t *testing.T) {
	mint := orchestratorIdentityMint("fugaro-orch@proj-1234.iam.gserviceaccount.com", orchestrationIdentityURL)
	labeler := &creds.ServiceLabeler{URL: orchestrationIdentityURL, Repository: "acme/app", AppID: "123456", IDToken: mint}
	mergemint := &creds.ServiceMergeMint{URL: orchestrationIdentityURL, AppID: "123456", IDToken: mint}
	if labeler.IDToken == nil || mergemint.IDToken == nil {
		t.Fatal("one of the credentials service's two clients was built without the daemon's own identity")
	}

	empty := orchestratorIdentityMint("", "")
	if _, err := empty(context.Background()); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("a mint with no account and no URL answered %v, want a user error naming fugaro update", err)
	}
}

// TestOrchestratorIdentityRefusesWhatItCannotMint: every state in which this
// machine cannot present the daemon's identity is a refusal naming the fix, and
// none of them is a silent skip. The daemon has no second credential to fall
// back on -- the person's own token is refused by the service's audience check
// whatever list it would have been matched against -- so a mint that could not
// happen has to say so, in one sentence, at the place that knows what was being
// asked for.
//
// None of the refusals carries a value: the token is a credential and gcloud's
// own words are another writer's, so the child's error is dropped rather than
// relayed (personid.go's rule, and creds.ServiceLabeler's).
//
// Mutation (run, restore): return an empty token instead of refusing where a
// mint failed -- the daemon presents nothing, the service answers
// `unverified_job_identity`, and the line the operator reads names neither the
// account nor the command.
func TestOrchestratorIdentityRefusesWhatItCannotMint(t *testing.T) {
	const account = "fugaro-orch@proj-1234.iam.gserviceaccount.com"
	for _, tc := range []struct {
		name string
		stub *personGcloudStub
		want string
	}{
		{"no gcloud on PATH", &personGcloudStub{lookPathErr: errors.New("not found")}, "is not on PATH"},
		{"the mint fails", &personGcloudStub{tokenErr: errors.New("gcloud said no")}, "could not be minted as " + account},
		{"an empty answer", &personGcloudStub{tokenOut: "  \n"}, "no usable identity token"},
		{"an answer with a newline inside", &personGcloudStub{tokenOut: "one\ntwo\n"}, "no usable identity token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.stub.use(t)
			tok, err := orchestratorServiceIdentity(context.Background(), orchestrationIdentityURL, account)
			if tok != "" {
				t.Errorf("the mint answered %q, want nothing", tok)
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "gcloud said no") {
				t.Errorf("err = %v, which carries the child process's own words: no refusal of this package relays them", err)
			}
			if tc.name == "no gcloud on PATH" && len(s.args) != 0 {
				t.Errorf("the mint ran %q with gcloud off PATH, want no call at all", s.args)
			}
			if tc.name != "no gcloud on PATH" {
				// The one sentence an operator needs is what to run, and it
				// names the account: the command without it would mint as
				// whatever gcloud is logged in as, which the service refuses.
				if !strings.Contains(err.Error(), "--impersonate-service-account="+account) {
					t.Errorf("err = %v, want it to say what to run, naming the account", err)
				}
				if len(s.args) != 1 || !strings.Contains(strings.Join(s.args[0], " "), "--impersonate-service-account="+account) {
					t.Errorf("the mint ran %q, want one call naming the account", s.args)
				}
			}
		})
	}
}

// TestOrchestratorIdentityMintsTheTrimmedAnswer: gcloud's answer is a token and
// a newline, and what the credentials service is presented with is the token.
// Nothing else of the answer survives.
func TestOrchestratorIdentityMintsTheTrimmedAnswer(t *testing.T) {
	(&personGcloudStub{tokenOut: "a-token-fixture\n"}).use(t)
	tok, err := orchestratorServiceIdentity(context.Background(), orchestrationIdentityURL, "fugaro-orch@proj-1234.iam.gserviceaccount.com")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok != "a-token-fixture" {
		t.Errorf("the mint answered %q, want the trimmed token", tok)
	}
}
