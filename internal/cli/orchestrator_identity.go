// The daemon's own identity at the installation's credentials service (F19 and
// O2 of docs/forge-labels): the account the service's orchestrator class
// admits, and the one mint the daemon's two callers of that service present —
// the label route (orchestrator_labels.go's seams) and the merge mint (E16,
// D2049; installed on the providers the triage opens, E10b).
//
// The service's class is a list of accounts AND an audience (credsvc's
// VerifyOrchestrator): the token has to be minted for the service's own URL and
// has to say, in its email claim, that the daemon's account is the one it was
// minted as. A plain `gcloud auth print-identity-token` gives neither — it is
// the operator's own token, for gcloud's client id — so the daemon mints an
// IMPERSONATED token: `--impersonate-service-account` for the account,
// `--audiences` for the URL, and `--include-email` because an impersonated
// service-account token carries no email claim without it (the flag
// personid.go's mint names for the same reason).
//
// The account is the installation module's own (orchestrator.tf, through
// infra.OrchestratorAccountID), derived from the local config's project the way
// `fugaro doctor`'s orchestrator-identity check derives it, and the URL is the
// one `fugaro update` published into that config (localcfg.Config.CredentialsService),
// which is the same string the module renders into the service's own
// environment as its audience. Reading the URL from anywhere else would be a
// token minted for an audience no revision answers to.
//
// The installation deliberately grants nobody the right to mint a token for
// that account: no `actAs` on it (orchestrator.tf's "Nothing is granted actAs
// on it") and no `roles/iam.serviceAccountTokenCreator` for the operators — the
// live check that measures the account's envelope adds that grant by hand and
// removes it afterwards (docs/gcp-live-checklist.md). So a mint that is refused
// is a NAMED refusal that says what to run, and never a daemon that labels
// nothing without saying why.
package cli

import (
	"context"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorIdentity is the daemon's identity at the installation's
// credentials service: the account it is and the URL its token is minted for.
//
// The two travel together because every caller needs both — the account is what
// the service's orchestrator class holds and the URL is the audience its gate
// requires — and an identity that carried one without the other would be a
// caller minting a token no revision answers to, or one the class refuses
// whoever presented it.
type orchestratorIdentity struct {
	account string
	url     string
}

// none reports whether this installation gave the daemon nothing to present: no
// account to mint as, or no service to present it to. Both halves arrive from
// the installation's own records (orchestratorIdentityOf), and either can be
// absent — a project whose local config names no credentials service runs a
// daemon exactly as it did before the service existed.
func (id orchestratorIdentity) none() bool { return id.account == "" || id.url == "" }

// orchestratorIdentityOf derives the daemon's identity from the installation's
// own records. It is the one place both halves are read, so a caller cannot
// mint for one project's account and present to another project's service.
func orchestratorIdentityOf(lc *localcfg.Config) orchestratorIdentity {
	var id orchestratorIdentity
	if lc == nil {
		return id
	}
	if lc.GCPProject != "" {
		id.account = infra.OrchestratorAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	}
	if lc.CredentialsService != nil {
		id.url = lc.CredentialsService.URL
	}
	return id
}

// orchestratorIdentityToken is the one seam both of the daemon's callers of the
// credentials service take their identity from: a function that mints this
// daemon's own token, ready to be handed to creds.ServiceLabeler and to
// creds.ServiceMergeMint, whose IDToken fields are exactly this shape.
//
// nil is the answer for an identity that is incomplete (none), and it is not a
// silent skip: a caller that gets nil builds no host at all rather than a host
// whose every call would be refused, which is the state an installation with no
// credentials service is in for ever (R7's direction). A mint that FAILS is a
// different thing and is never swallowed here — it travels back to the caller
// as orchestratorIDToken's own refusal, which names what to run.
func orchestratorIdentityToken(id orchestratorIdentity) func(context.Context) (string, error) {
	if id.none() {
		return nil
	}
	return func(ctx context.Context) (string, error) { return orchestratorIDToken(ctx, id) }
}

// orchestratorIDToken is that mint: the daemon's own Google ID token for the
// installation's credentials service, minted as the daemon's account.
//
// It runs gcloud rather than a client library for personid.go's reason: the
// Google libraries mint an ID token from a service account's own key, an
// impersonated account or the metadata server, and what the daemon holds on the
// operator's machine is none of those — it is the operator's own credential,
// which has to be exchanged for the daemon's account first. print-identity-token
// is the supported way to make that exchange, and the impersonation flags are
// what make it the daemon's identity rather than the operator's.
//
// It is a package variable so a test replaces it: no test mints a real
// credential, and no value this returns or refuses with reaches an error, a log
// line or an output.
var orchestratorIDToken = func(ctx context.Context, id orchestratorIdentity) (string, error) {
	if _, err := reviewGcloudLookPath(reviewGcloudBin); err != nil {
		return "", userErr("the daemon presents its own Google ID token at the installation's credentials service, and the %s command that mints one is not on PATH: install the Google Cloud CLI (https://cloud.google.com/sdk/docs/install) on the machine the daemon runs on, and run `%s auth login`", reviewGcloudBin, reviewGcloudBin)
	}
	cctx, cancel := context.WithTimeout(ctx, reviewIdentityTimeout)
	defer cancel()
	out, err := reviewGcloudRun(cctx, "auth", "print-identity-token",
		"--impersonate-service-account="+id.account,
		"--audiences="+id.url,
		"--include-email",
		"--quiet",
	)
	if err != nil {
		return "", userErr("the daemon's own Google ID token could not be minted as %s for the installation's credentials service at %s, and the service admits that identity and no other: run `%s auth login` on the machine the daemon runs on, and, if the impersonation itself is what was refused, grant that account the right to mint a token for it with `%s iam service-accounts add-iam-policy-binding %s --member=user:<you> --role=roles/iam.serviceAccountTokenCreator` (this installation grants nobody that right: docs/gcp-live-checklist.md's orchestrator-identity check adds the grant by hand)",
			pluginwire.Printable(id.account), safetext.Field(id.url), reviewGcloudBin, reviewGcloudBin, pluginwire.Printable(id.account))
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || len(tok) > reviewIdentityMaxBytes || !reviewPrintableToken(tok) {
		return "", userErr("`%s auth print-identity-token --impersonate-service-account` answered with no usable identity token: run `%s auth login` on the machine the daemon runs on, and try again", reviewGcloudBin, reviewGcloudBin)
	}
	return tok, nil
}

// credsMergeMint adapts the credentials service's merge mint to the shape a
// provider takes, which is the whole of why this type exists: gitprov sits
// below internal/creds and cannot import it (gitprov/merge.go's MergeMinter
// says so), and the two method sets are the same call in two spellings —
// creds.MergeToken, which carries the repository the service minted for, and
// the token and its expiry a provider's ready and merge calls take.
type credsMergeMint struct{ m creds.MergeMinter }

var _ gitprov.MergeMinter = credsMergeMint{}

func (a credsMergeMint) MintMerge(ctx context.Context, repo string, pr int, headSHA string) (string, time.Time, error) {
	tok, err := a.m.MintMerge(ctx, repo, pr, headSHA)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok.Token, tok.ExpiresAt, nil
}

// orchestratorUseMergeMint installs the gated merge mint on the provider a
// repository was opened with (E10b, E16/D2049): the credential that provider's
// ready and merge calls are made with is the service's mint, presented with the
// daemon's own identity — the same seam the label route uses, and the reason
// both halves of this file are one file.
//
// What it does NOT install is as much of the rule as what it does. A provider
// that is exempt from the mint (Bitbucket: its host has no App for a bypass
// list to refuse, gitprov's MintExempt) does not implement MergeMintSetter and
// is left exactly as it was; and a repository this installation records no
// GitHub App ID for gets no mint either, because the mint would sign as an App
// nobody named — such a provider's merge refuses with ErrNoMergeMint, which is
// the truthful answer, rather than a call the service refuses one round trip
// later.
func orchestratorUseMergeMint(p gitprov.Provider, env *cloudEnv, repo string, id orchestratorIdentity) {
	setter, ok := p.(gitprov.MergeMintSetter)
	if !ok {
		return
	}
	token := orchestratorIdentityToken(id)
	if token == nil {
		return
	}
	appID := ""
	if env != nil {
		if r, ok := env.localRepo(repo); ok {
			appID = r.GitHubAppID
		}
	}
	if appID == "" {
		return
	}
	setter.UseMergeMint(credsMergeMint{&creds.ServiceMergeMint{
		URL: id.url, AppID: appID, IDToken: token,
	}})
}
