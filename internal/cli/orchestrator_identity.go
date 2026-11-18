package cli

// The daemon's own identity to the installation's credentials service (F19, O2;
// E10b, E16, D2049): the one seam every caller of the service inside the
// orchestrator is given, so that the class the service admits and the credential
// the daemon presents are one piece of work rather than one per route.
//
// Two clients carry it and both take it as a field of the same name
// (creds.ServiceLabeler.IDToken and creds.ServiceMergeMint.IDToken): the label
// route, which the tick's label sync drives, and the merge mint, which the
// ladder's rung-3 hook drives (E10b's `LadderBeyond`, which this tree does not
// wire: no daemon merges a pull request today, SECURITY.md says so, and this
// file builds the mint rather than a caller for it). What they share is this
// function and nothing else — a second mint for the second route would be a
// second answer to "which account is this daemon", and the one that drifts is
// the one that presents somebody else's identity.
//
// It is NOT the person's own token, and it cannot be: the service verifies an
// orchestrator on its own URL as the audience, and gcloud refuses --audiences
// outright for a user account, so the ambient credential of the operator whose
// machine the daemon runs on is refused before any list is read
// (credsvc.VerifyOrchestrator, and credsvc.AuthConfig.OrchestratorAccounts'
// own statement of the rule). The token this mints is one the operator obtains
// ON PURPOSE, by impersonating the service account the installation created for
// the daemon, and the way it is obtained is the named half of the refusal below.
//
// It is the launcher's own mint (personid.go's personIDToken) respelled rather
// than shared with it, for that function's own reason and one more: this one
// names the account it impersonates instead of taking whatever identity
// gcloud's active configuration happens to hold. A daemon that minted as
// whatever the operator last logged in as would present the launcher class's
// credential to a route that is not the launcher's, and would do it silently.
// It runs through the same two process seams (reviewverify.go's reviewGcloud*),
// because a second copy of "run gcloud with its stderr captured" would be a
// second place that could leak a child process's words into an error about a
// credential.

import (
	"context"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorIdentityArgs is the argv the daemon's token is minted with.
//
// All three flags are the account's properties rather than preferences, and
// personIdentityArgs states the same three the other way round: --audiences is
// what only a service account (or an impersonated one) may be given, and it is
// what makes the token's audience the credentials service's own URL rather than
// gcloud's client id; --include-email is what an IMPERSONATED mint needs and a
// logged-in account must not be given, because the elevation call defaults the
// email claim off and the email claim is the one thing the service looks a
// caller up by; and --impersonate-service-account is the account itself, named
// here so that no gcloud configuration of the operator's decides it
// (orchestratorDaemonAccount). --quiet is the rule every mint of this package
// follows: nothing gcloud would ask can hold a read (personid.go).
func orchestratorIdentityArgs(url, account string) []string {
	return []string{
		"auth", "print-identity-token",
		"--impersonate-service-account=" + account,
		"--audiences=" + url,
		"--include-email",
		"--quiet",
	}
}

// orchestratorDaemonAccount is the service account the installation created for
// the daemon, as this machine can name it: one account per installation, under
// the ID the design fixes (infra.OrchestratorAccountID), in the installation's
// own GCP project.
//
// The address is composed here rather than carried in the local config because
// the two halves are already there and neither is a new fact: the ID is a
// constant of the release and the project is the one every other line of this
// daemon's configuration is read from. `fugaro doctor` composes the same address
// from the same two (infra.OrchestratorAccountEmail), which is what makes
// "the account the check reads" and "the account the daemon presents" one
// answer rather than two.
//
// An installation with no GCP project named is the one arm that refuses, and it
// refuses rather than minting as anything: a daemon that could not name its own
// account has no identity to present, and the honest answer is the refusal the
// caller writes down, never a call the service would refuse anyway
// (creds.ErrLabelIdentity, creds.ErrMergeMintIdentity).
func orchestratorDaemonAccount(lc *localcfg.Config) (string, error) {
	if lc == nil || lc.GCPProject == "" {
		return "", userErr("the resident orchestrator presents its own Google identity to the installation's credentials service, and this machine's configuration names no GCP project to compose that account in: run `fugaro update` in the installation's checkout, which writes the project into this machine's configuration")
	}
	return infra.OrchestratorAccountEmail(lc.GCPProject), nil
}

// orchestratorServiceIdentity mints the daemon's own Google ID token for the
// installation's credentials service at url, as the installation's own
// orchestrator account.
//
// It is a package variable so that a test replaces it: no test impersonates a
// real account, and no value this returns or refuses with reaches an error, a
// log line or an output — the checks below are the ones the service applies to a
// presented token (one clean header value, bounded), and the refusals are
// sentences written here (personIDToken's own rule).
//
// Every failure is a NAMED refusal and never a silent skip. The daemon has no
// second credential to fall back on and there is no path here that returns an
// empty token, because the one thing this tree must not do is present nothing
// while looking like it presented something: a label sync that could not mint
// leaves one line naming what to run, and the tick goes on without labels
// (docs/forge-labels R7).
var orchestratorServiceIdentity = func(ctx context.Context, url, account string) (string, error) {
	const what = "the resident orchestrator keeps the pull request labels of its own runs through the installation's credentials service, and asks it for a merge credential"
	if _, err := reviewGcloudLookPath(reviewGcloudBin); err != nil {
		return "", userErr("%s: the %s command that mints its own identity is not on PATH. Install the Google Cloud CLI (https://cloud.google.com/sdk/docs/install) and run `%s auth login` on this machine", what, reviewGcloudBin, reviewGcloudBin)
	}
	cctx, cancel := context.WithTimeout(ctx, reviewIdentityTimeout)
	defer cancel()
	out, err := reviewGcloudRun(cctx, orchestratorIdentityArgs(url, account)...)
	if err != nil {
		return "", userErr("%s: its own Google ID token could not be minted as %s. Run `%s auth login --impersonate-service-account=%s` on this machine, which is what makes a token for the credentials service's own URL; an account that may not impersonate it needs `roles/iam.serviceAccountTokenCreator` on it, which the installation's owner holds", what, safetext.Field(account), reviewGcloudBin, safetext.Field(account))
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || len(tok) > reviewIdentityMaxBytes || !reviewPrintableToken(tok) {
		return "", userErr("`%s auth print-identity-token` answered with no usable identity token for %s: run `%s auth login --impersonate-service-account=%s` and try again", reviewGcloudBin, safetext.Field(account), reviewGcloudBin, safetext.Field(account))
	}
	return tok, nil
}

// orchestratorIdentityMint is the daemon's identity as the two clients of the
// credentials service take it: the IDToken field of creds.ServiceLabeler and of
// creds.ServiceMergeMint, which is the same signature and is called on every
// call and never cached by either client (their own contract: the token's life
// is the platform's, and a cached one would be a second place a credential
// lives).
//
// It closes over the account and the URL so that a caller cannot pass either
// wrongly, and so that a mint built twice — one per client — is still one seam:
// both are built from this function, with this daemon's own two values.
func orchestratorIdentityMint(account, url string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if account == "" || url == "" {
			return "", userErr("the resident orchestrator has no identity to present to the installation's credentials service: run `fugaro update` in the installation's checkout, which writes both the service's URL and the project the daemon's own account is composed in into this machine's configuration")
		}
		return orchestratorServiceIdentity(ctx, url, account)
	}
}
