package cli

// The launcher's own Google ID token (A3, D902): the identity a launch with no
// checkout of the repository presents to the installation's credentials
// service, which verifies it against the installation's own people list and
// mints a read-only GitHub installation token for one repository. The launcher
// holds no App key and reads no environment variable (spec.md §5).
//
// It is the coordinator's own mint respelled (ee/coordinator/cli,
// coordinatorPersonIdentity) rather than shared with it: that package imports
// this one, so this one cannot import it back, and internal/eeboundary's
// boundary test is what holds the core to that (reconciliation F13). What it
// runs and how it runs it — the argv, gcloud's own listing of the active
// account, the refusals — are that function's, restated here for this
// launcher's subject; the process seams it runs through are the ones this
// package already has for the same binary and the same rule (reviewverify.go's
// reviewGcloud*), because a second copy of "run gcloud with its stderr
// captured" would be a second place that could leak a child process's words
// into an error about a credential.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// personIdentityArgs is the argv the mint is run with: the account is the
// person's own (no --account), and --quiet is passed so that nothing gcloud
// would ask can hold a read.
//
// The flags are not constants, and they are properties of the ACCOUNT rather
// than preferences. gcloud refuses --audiences outright for a user account
// ("Invalid account type for --audiences. Requires valid service account"), so
// a person's own login can only mint the plain token — the one whose audience
// is gcloud's own client id, which is the second audience the credentials
// service's person class admits (credsvc.GcloudAudience). A service account, or
// an identity gcloud is impersonating, keeps the audience form: that token
// names the credentials service's URL, which is what the service's own audience
// check wants (credsvc.AuthConfig.Audience). An IMPERSONATED identity needs
// --include-email as well, and a user account or a logged-in service account
// must not be given it (personGcloudConfig.impersonated): the email claim is
// what the service looks a caller up by, and an impersonated mint's elevation
// call defaults it off. Which of the three the account is comes from gcloud's
// own listing (personGcloudAccount), never from the refusal of an attempted
// mint — that would mean asking for a credential the account cannot have.
func personIdentityArgs(url string, cfg personGcloudConfig) []string {
	args := []string{"auth", "print-identity-token"}
	if cfg.serviceAccount() {
		args = append(args, "--audiences="+url)
	}
	if cfg.impersonated() {
		args = append(args, "--include-email")
	}
	return append(args, "--quiet")
}

// personGcloudConfigArgs reads the ACTIVE account out of gcloud's own
// configuration. `config list` rather than `auth list` because one call answers
// both halves of the question — which account is active, and whether gcloud
// would mint as an account it is impersonating instead — and because it prints
// an empty object rather than failing when nothing is configured, which is the
// state the refusal below names the fix for.
func personGcloudConfigArgs() []string {
	return []string{"config", "list", "--format=json"}
}

// personGcloudConfig is the part of that listing this mint reads. Both fields
// are gcloud's own statement about the credential it would mint with, and it is
// the address that decides the argv: a user account is what cannot take
// --audiences.
type personGcloudConfig struct {
	Core struct {
		Account string `json:"account"`
	} `json:"core"`
	Auth struct {
		ImpersonateServiceAccount string `json:"impersonate_service_account"`
	} `json:"auth"`
}

// serviceAccount reports whether gcloud would mint as a service account:
// either the active account is one or gcloud is impersonating one.
func (c personGcloudConfig) serviceAccount() bool {
	return c.impersonated() ||
		strings.HasSuffix(strings.ToLower(c.Core.Account), ".gserviceaccount.com")
}

// impersonated reports whether gcloud would mint as an account it is
// IMPERSONATING rather than as the one logged in, and it is the argv's other
// half (personIdentityArgs).
func (c personGcloudConfig) impersonated() bool {
	return c.Auth.ImpersonateServiceAccount != ""
}

// loggedIn reports whether gcloud names a credential to mint with at all.
func (c personGcloudConfig) loggedIn() bool {
	return c.Core.Account != "" || c.Auth.ImpersonateServiceAccount != ""
}

// personGcloudAccount runs the listing. A listing that cannot be read is not
// distinguished from one that names nothing: both mean this machine has no
// credential to mint the person's token with, and both take the same refusal.
func personGcloudAccount(ctx context.Context) (personGcloudConfig, error) {
	out, err := reviewGcloudRun(ctx, personGcloudConfigArgs()...)
	if err != nil {
		return personGcloudConfig{}, err
	}
	var cfg personGcloudConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return personGcloudConfig{}, err
	}
	return cfg, nil
}

// personIDToken mints the caller's own Google ID token for the installation's
// credentials service at url, with `gcloud auth print-identity-token`: the
// identity the service's person class verifies, and the same credential every
// other command a person runs reaches the project with.
//
// It runs gcloud rather than calling an id-token library because the Google
// client libraries cannot mint one from a person's own credential at all: their
// id-token sources handle a service account, an impersonated account and the
// metadata server, and refuse an authorized-user credential by name. A person's
// own ADC is exactly that credential, and print-identity-token is the supported
// way to turn it into an ID token — gcloud resolves it from the person's own
// gcloud login or their ADC, and no variable of Fugaro's decides which.
//
// It is a package variable so that a test replaces it: no test mints a real
// credential, and no value this returns or refuses with reaches an error, a log
// line or an output — the checks below are the ones the service applies to a
// presented token (one clean header value, bounded), and the refusals are
// sentences written here.
var personIDToken = func(ctx context.Context, url string) (string, error) {
	if _, err := reviewGcloudLookPath(reviewGcloudBin); err != nil {
		return "", userErr("a launch outside a repository's checkout reads its fugaro.yaml with a token the installation's credentials service mints for you, and the %s command that mints one is not on PATH: install the Google Cloud CLI (https://cloud.google.com/sdk/docs/install) and run `%s auth login`", reviewGcloudBin, reviewGcloudBin)
	}
	cctx, cancel := context.WithTimeout(ctx, reviewIdentityTimeout)
	defer cancel()
	cfg, err := personGcloudAccount(cctx)
	if err == nil && !cfg.loggedIn() {
		err = errors.New("gcloud names no active account")
	}
	if err != nil {
		return "", userErr("a launch outside a repository's checkout reads its fugaro.yaml with a token the installation's credentials service mints for you, and this machine has no Google account for %s to mint one with: run `%s auth login` and try again", reviewGcloudBin, reviewGcloudBin)
	}
	out, err := reviewGcloudRun(cctx, personIdentityArgs(url, cfg)...)
	if err != nil {
		return "", userErr("your own Google ID token could not be minted for the installation's credentials service: run `%s auth login`, or `%s auth application-default login`, and try again", reviewGcloudBin, reviewGcloudBin)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || len(tok) > reviewIdentityMaxBytes || !reviewPrintableToken(tok) {
		return "", userErr("`%s auth print-identity-token` answered with no usable identity token: run `%s auth login` and try again", reviewGcloudBin, reviewGcloudBin)
	}
	return tok, nil
}
