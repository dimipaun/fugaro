package cli

// This file is CSR10's launcher half: the two things a fix round's verify call
// needs and the launcher itself must supply — the verifier the launch calls
// the service as, and the head the service's signature is checked against.
// The reader that uses them is reviewFindings (review.go).
//
// It is a file of its own rather than two functions in review.go because both
// answers come from outside the launcher's own process state — an identity the
// installation owns and a read from the forge — and each is reached through a
// seam a hermetic test replaces, which is a shape review.go does not otherwise
// have.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// reviewVerifierSeam, when a test sets it, is what reviewVerifier returns
// instead of the launcher's own verifier. It exists for the reason the
// runner's reviewSignerSeam does: the verify route is a service this task must
// not need running, and the identity it admits is not a test's to mint. The
// tests satisfy runner.SignatureVerifier with a stand-in service over
// httptest, so the wire the launcher sends is the real one.
var reviewVerifierSeam func(ctx context.Context, account, run, repository string) (runner.SignatureVerifier, error)

// reviewVerifier is the launcher's verifier for one review artifact: the
// credentials service of the installation's published config, the identity the
// verify route admits (reviewVerifierIdentity), and the three values the
// service needs to answer — the review run's own account, the review run, and
// the repository it is for.
//
// A launch whose installation has no credentials service at all refuses here
// rather than calling nothing: a fix round carries a verdict only after the
// service has verified its signature, and a launcher that treated a missing
// service as "nothing to check" would put an unverified artifact in a brief.
func reviewVerifier(ctx context.Context, env *cloudEnv, account, run, repository string) (runner.SignatureVerifier, error) {
	if reviewVerifierSeam != nil {
		return reviewVerifierSeam(ctx, account, run, repository)
	}
	var url string
	if env.lc != nil && env.lc.CredentialsService != nil {
		url = env.lc.CredentialsService.URL
	}
	if url == "" {
		return nil, userErr("this installation has no credentials service (credentials_service is not published in this project's config), and a fix round carries a review's verdict only after the service verifies its signature: no launch is started on a verdict nobody checked")
	}
	return runner.ServiceVerifier{
		URL:        url,
		IDToken:    func(ctx context.Context) (string, error) { return reviewVerifierIdentity(ctx, env, url) },
		Account:    account,
		Run:        run,
		Repository: repository,
	}, nil
}

// reviewVerifierIdentity mints the launcher's Google ID token for the verify
// route's audience, which is the service's own URL (C1.14), as the
// installation's dedicated verifier service account.
//
// WHICH identity was Open item 5 of docs/cloud-senior-review/implementation-plan.md
// §6.6, and the owner settled it on 2026-11-05: a dedicated verifier service
// account the installation root creates (CSR14) and names in the published
// verifier_accounts, which the launcher impersonates. It is the one identity
// the launcher may NOT be itself: every `user:` operator is a person account to
// the service, and NewAuthenticator refuses an installation whose verifier list
// names one (internal/credsvc/auth.go), so the caller is the service account.
//
// The impersonation is the launcher person's own credential calling the IAM
// Credentials API, which needs roles/iam.serviceAccountTokenCreator on the
// verifier account, and CSR14 grants that to var.operators and to nobody else.
// A launcher who is not an operator is refused by IAM, and that refusal is
// worded here as what it is, so the person meets a sentence and not a 403.
//
// The deployer's own impersonation is deliberately not reused: it exists to
// assume the deployer, who holds the installation's authority and not one
// route's, and a launcher that reached the verify route as the deployer would
// be one caller in two classes at the service. The token is minted for the
// service's URL and no other audience, and its email must name the account the
// call was made for.
func reviewVerifierIdentity(ctx context.Context, env *cloudEnv, serviceURL string) (string, error) {
	account, err := reviewVerifierAccount(ctx, env)
	if err != nil {
		return "", err
	}
	_, ts, err := managedCaller(ctx, env.lc)
	if err != nil {
		return "", userErr("the launcher cannot mint the verifier identity: %v", err)
	}
	body, err := json.Marshal(map[string]any{"audience": serviceURL, "includeEmail": true})
	if err != nil {
		return "", err
	}
	out, err := impersonateVerifier(ctx, ts, account, body)
	if err != nil {
		return "", err
	}
	var tok struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(out, &tok) != nil || tok.Token == "" {
		return "", fmt.Errorf("assuming the verifier account %s: generateIdToken answered without a token", safetext.Field(account))
	}
	email, err := jwtEmail(tok.Token)
	if err != nil {
		return "", fmt.Errorf("assuming the verifier account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	if !strings.EqualFold(email, account) {
		return "", fmt.Errorf("the token is for %s, not for the verifier account %s: a token for another identity is never used as a credential", safetext.Field(email), safetext.Field(account))
	}
	return tok.Token, nil
}

// reviewVerifierAccount is the one verifier account the installation's PUBLISHED
// node names: the list the credentials service's own environment is rendered
// from, and therefore the only list whose callers the service admits. The local
// config is not asked: a config that names a verifier the node does not names a
// caller the service refuses. None refuses, and so do two, because a launcher
// that picked one of two would be choosing which identity verifies a verdict,
// which is the owner's to say.
func reviewVerifierAccount(ctx context.Context, env *cloudEnv) (string, error) {
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil {
		return "", userErr("the installation's published policy could not be read, so the verifier account a fix round is verified as is unknown: %v", policyReadErr(err))
	}
	var accounts []string
	if found && p != nil {
		accounts = p.VerifierAccounts
	}
	switch len(accounts) {
	case 0:
		return "", userErr("this installation names no verifier (verifier_accounts is empty in its published policy), and a fix round is not launched on a verdict nobody verified: run fugaro update, which names the installation's verifier service account")
	case 1:
	default:
		return "", userErr("this installation names %d verifier accounts, and the launcher's verify call is one caller: a launcher that chose one would be choosing which identity verifies a verdict, which is the owner's to say, so a fix round is not launched (trim verifier_accounts to the one verifier service account and run fugaro update)", len(accounts))
	}
	if !strings.HasSuffix(accounts[0], ".iam.gserviceaccount.com") || strings.ContainsAny(accounts[0], "/?#% \t\n") {
		return "", userErr("the published verifier %s is not a service account address, so the launcher cannot impersonate it", safetext.Field(accounts[0]))
	}
	return accounts[0], nil
}

// impersonateVerifier is one IAM Credentials generateIdToken call for the
// account with the launcher's own credential. It is the shape of the deployer's
// call (deployerImpersonator.call) with the launcher's wording: the 403 a
// launcher who is not an operator gets from IAM names the operators as the
// holders and the operators list as the fix.
func impersonateVerifier(ctx context.Context, ts oauth2.TokenSource, account string, body []byte) ([]byte, error) {
	if ts == nil {
		return nil, errors.New("assuming the verifier account: no credential to make the call with")
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("assuming the verifier account: fetching your own credential failed (%v): run gcloud auth application-default login", safetext.FieldErr(err))
	}
	u := strings.TrimRight(impersonationEndpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(account) + ":generateIdToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := impersonationHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("assuming the verifier account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("assuming the verifier account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, userErr("IAM refused your credential on the verifier account %s (%s): only the installation's operators may verify a review's verdict, because roles/iam.serviceAccountTokenCreator on that account is granted to the operators and to nobody else; ask the owner to add you to the operators list and run fugaro update", safetext.Field(account), safetext.Field(resp.Status))
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("assuming the verifier account %s: %s: %s", safetext.Field(account), safetext.Field(resp.Status), safetext.Field(string(out)))
	}
	return out, nil
}

// reviewReadIdentity mints the caller's own Google ID token for the
// installation's credentials service at serviceURL, which is the identity the
// service's person class verifies and therefore the credential a read of a pull
// request's head is made with: the machine that reads holds no GitHub App key
// at all (C2.2, D145), so the service mints the token and this presents who
// asked for it.
//
// It is the PLAIN mint, whose audience is gcloud's own client id
// (credsvc.GcloudAudience), and that audience is the whole of the reason: the
// service's read mints admit it and admit no other for this class, and a USER
// account is the only account that can mint it -- gcloud refuses --audiences
// for one ("Invalid account type for --audiences. Requires valid service
// account"). A user account is what this caller is: the people the service
// looks a caller up in are the installation's operators, who are people, and
// the class refuses a service account by name.
//
// It runs gcloud rather than a client library because the Google libraries
// cannot mint an ID token from a person's own credential at all: their
// id-token sources handle a service account, an impersonated account and the
// metadata server, and refuse an authorized-user credential by name. A
// person's own ADC is exactly that credential, and print-identity-token is the
// supported way to turn it into an ID token -- from the person's own gcloud
// login or from their ADC, and no variable of Fugaro's decides which.
//
// It is a package variable so a test replaces it: no test mints a real
// credential, and no value this returns or refuses with reaches an error, a
// log line or an output.
//
// The mint is the coordinator's own small mint respelled
// (ee/coordinator/cli, coordinatorPersonIdentity) rather than shared with it:
// that package imports this one, so this one cannot import it back, and the
// alternative -- lifting the mint into internal/ -- is a change to files this
// task does not hold.
var reviewReadIdentity = func(ctx context.Context, serviceURL string) (string, error) {
	if _, err := reviewGcloudLookPath(reviewGcloudBin); err != nil {
		return "", userErr("a pull request's head is read with a token the installation's credentials service mints for you, and the service verifies you by your own Google ID token: the %s command that mints one is not on PATH. Install the Google Cloud CLI (https://cloud.google.com/sdk/docs/install) and run `%s auth login`", reviewGcloudBin, reviewGcloudBin)
	}
	cctx, cancel := context.WithTimeout(ctx, reviewIdentityTimeout)
	defer cancel()
	out, err := reviewGcloudRun(cctx, "auth", "print-identity-token", "--quiet")
	if err != nil {
		return "", userErr("your own Google ID token could not be minted for the installation's credentials service at %s, and a pull request's head is read with a token the service mints for you: run `%s auth login`, or `%s auth application-default login`, and try again", safetext.Field(serviceURL), reviewGcloudBin, reviewGcloudBin)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || len(tok) > reviewIdentityMaxBytes || !reviewPrintableToken(tok) {
		return "", userErr("`%s auth print-identity-token` answered with no usable identity token: run `%s auth login` and try again", reviewGcloudBin, reviewGcloudBin)
	}
	return tok, nil
}

const (
	// reviewGcloudBin is the binary a person's own Google ID token is minted
	// with.
	reviewGcloudBin = "gcloud"
	// reviewIdentityTimeout bounds the whole mint: gcloud is a local process
	// that answers in a second when it answers at all, and a launch must not
	// hold on one that does not.
	reviewIdentityTimeout = 30 * time.Second
	// reviewIdentityMaxBytes is internal/credsvc's own bound on a presented
	// token (auth.go's maxTokenBytes): an answer longer than it is refused
	// here rather than sent to be refused there.
	reviewIdentityMaxBytes = 8 << 10
)

// reviewGcloudLookPath and reviewGcloudRun are the two seams the mint is run
// through, so that a test drives the real function with no binary and no
// credential. The real runner CAPTURES gcloud's stderr and drops it: this
// command's subject is a credential, and a refusal that quoted a child
// process's own words would be another writer's value reaching an error.
var (
	reviewGcloudLookPath = exec.LookPath
	reviewGcloudRun      = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, reviewGcloudBin, args...)
		// A leaked descendant holding the output pipe must not hold us.
		cmd.WaitDelay = 5 * time.Second
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		return cmd.Output()
	}
)

// reviewPrintableToken reports whether tok is one clean header value, which is
// the rule internal/credsvc reads a presented token by (wire.go's
// printableHeaderValue) and the one internal/runner's own id token keeps --
// neither of which is exported.
func reviewPrintableToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 0x21 || tok[i] > 0x7e {
			return false
		}
	}
	return true
}

// reviewGitHubAPIURL and reviewBitbucketAPIURL are the forge API roots a head
// read goes to, and the one place they are named: the installation's service
// mints a token for the repository, and a root taken from the launcher's
// environment would send that token (and the head the replay check compares
// against) to whatever host a shell variable names. The coordinator's person-class
// read holds its root the same way (coordinatorGitHubAPIURL, D474). They are
// variables only so a test can stand a server in for the forge.
var (
	reviewGitHubAPIURL    = github.DefaultBaseURL
	reviewBitbucketAPIURL = bitbucket.DefaultBaseURL
)

// reviewReadProvider opens the provider a pull request's head is read through:
// the forge's own adapter for one repository, keyless, with the installation's
// credentials service as the source of every token it uses.
//
// The three values the read mint needs each have ONE source, and none of them
// is a variable of this process's environment: the service URL is the
// installation's published credentials_service block (the same value and the
// same source reviewVerifier reads), the GitHub App ID is the project's own
// recorded value for the repository, and the caller's own identity is minted
// here (reviewReadIdentity). A read that reached for any of the three in the
// environment would read differently on a machine that exports something --
// and a person's machine is the one this command runs on (F23).
//
// The provider is opened SERVICE-HELD-KEY rather than through the opener the
// launch's own provider comes from (OpenProvider, providers.FromEnv): that one
// resolves a GitHub App ID and an App key out of the environment, and an App
// key is exactly what this machine does not hold. A provider with no read mint
// at all is refused rather than opened some other way, which is what keeps the
// fallback this task removes from coming back through the default branch.
func reviewReadProvider(ctx context.Context, env *cloudEnv, kind, repo string) (gitprov.Provider, error) {
	serviceURL := ""
	if env.lc != nil && env.lc.CredentialsService != nil {
		serviceURL = env.lc.CredentialsService.URL
	}
	if serviceURL == "" {
		return nil, userErr("this installation has no credentials service (credentials_service is not published in this project's config), and a pull request's head is read with a token the service mints for you: a person's machine holds no GitHub App key to read one with. Ask the owner to opt this installation in to the credentials service and run fugaro update")
	}
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return nil, userErr("a pull request's head is read from %s, which is not a repository in owner/name form", pluginwire.Printable(repo))
	}
	identity := func(ctx context.Context) (string, error) { return reviewReadIdentity(ctx, serviceURL) }
	switch kind {
	case gitprov.KindGitHub:
		appID := ""
		if r, ok := env.localRepo(repo); ok {
			appID = r.GitHubAppID
		}
		if appID == "" {
			return nil, userErr("the project's configuration records no GitHub App ID for %s, and the installation's credentials service signs this read's token as that App: `fugaro doctor` reports what the project holds for the repository, and a setup run in its checkout records it", pluginwire.Printable(repo))
		}
		p, err := github.New(github.Options{
			Owner: owner, Repo: name, AppID: appID, ServiceHeldKey: true,
			BaseURL: reviewGitHubAPIURL,
		})
		if err != nil {
			return nil, remote(err)
		}
		// The mint is a client of ONE repository: the service mints for the
		// repository the call names, and the client refuses an answer naming
		// another, so a token minted for one repository is never used for a
		// read of a second.
		p.UseServiceTokens((&github.ServiceReadTokens{URL: serviceURL, IDToken: identity}).Repo(repo, appID))
		return p, nil
	case gitprov.KindBitbucket:
		// The source goes into the provider's own options rather than through
		// UseServiceTokens: a Bitbucket provider takes its tokens as an
		// argument to New (Options.TokenSource) and offers no install.
		return bitbucket.New(bitbucket.Options{
			Workspace: owner, Slug: name, BaseURL: reviewBitbucketAPIURL,
			TokenSource: (&bitbucket.ServiceReadTokens{URL: serviceURL, IDToken: identity}).Repo(repo),
		})
	default:
		return nil, userErr("a pull request's head is read with a token the installation's credentials service mints, and there is no read mint for the %s provider: only %s and %s have one, so no read is made", pluginwire.Printable(kind), gitprov.KindGitHub, gitprov.KindBitbucket)
	}
}

// reviewReadMintRefusal is what a read the read mint refused is worded as, and
// nil for every other failure. It exists because FetchPRHead mints its token
// lazily, on its first API call: a mint this launcher could not get comes back
// out of the forge's own call, and reporting it the way a forge's failure is
// reported would read as a broken pull request rather than as a caller the
// service would not mint for.
//
// The refusal names both halves a person has to act on: the read mint is what
// mints the token, and what it verifies is the caller's own identity.
func reviewReadMintRefusal(err error) error {
	switch {
	case errors.Is(err, github.ErrServiceRead), errors.Is(err, github.ErrServiceReadIdentity),
		errors.Is(err, bitbucket.ErrServiceRead), errors.Is(err, bitbucket.ErrServiceReadIdentity):
	default:
		return nil
	}
	return userErr("the installation's credentials service's read mint did not mint the token a pull request's head is read with: the read presents your own Google ID token, and the service mints only for a person the installation lists (its people are the installation's operators) and only for a caller it admits on the service (roles/run.invoker on it). Ask the owner to add you to the installation's people and run fugaro update, then try again (%s)", safetext.FieldErr(err))
}

// reviewPRHeadSeam, when a test sets it, is what reviewHead answers instead of
// the forge's own read: a real FetchPRHead needs a provider credential this
// package's fixtures do not hold.
var reviewPRHeadSeam func(ctx context.Context, env *cloudEnv, repo string, pr int) (string, error)

// reviewHead is the pull request's current head, read from the forge the
// repository's runs go to. It is the value ReviewExpectation.HeadSHA is
// checked against, and the check is the one a signature alone does not make: a
// validly signed verdict for an EARLIER head of the same pull request is a
// replay, and it is refused rather than acted on (spec §6, §8's window).
//
// The credential it reads WITH is the installation's credentials service's
// read mint and never this process's own (reviewReadProvider): the machine
// that reads holds no GitHub App key at all, and the token the service mints
// is read-only and one repository's.
//
// A head the forge cannot be asked for — a closed pull request, a provider
// that refuses the read — refuses the brief rather than passing it.
func reviewHead(ctx context.Context, env *cloudEnv, repo string, pr int) (string, error) {
	if reviewPRHeadSeam != nil {
		return reviewPRHeadSeam(ctx, env, repo, pr)
	}
	// The provider is resolved the way the repository's slug already was
	// (repoSlug's rule: the local config's entry first, then the checkout's
	// fugaro.yaml), and the two cannot disagree by the time this runs — the
	// caller read the slug through that rule first.
	kind := ""
	if r, ok := env.localRepo(repo); ok {
		kind = r.Provider
	}
	if kind == "" {
		if co := checkoutOf(ctx, repo)(); co != nil {
			kind = co.Git.Provider
		}
	}
	if kind == "" {
		return "", userErr("cannot tell the git provider of %s, so its pull request's head cannot be read: set provider in its repos entry of the local config, or run from its checkout", pluginwire.Printable(repo))
	}
	p, err := reviewReadProvider(ctx, env, kind, repo)
	if err != nil {
		return "", err
	}
	head, err := p.FetchPRHead(ctx, pr)
	if err != nil {
		// The mint is the forge call's first step, so a mint refused comes
		// back here: it is worded as what it is rather than as a failure of
		// the forge's.
		if refusal := reviewReadMintRefusal(err); refusal != nil {
			return "", refusal
		}
		return "", remote(err)
	}
	if strings.TrimSpace(head.SHA) == "" {
		return "", remote(fmt.Errorf("%s reported no head for pull request #%d", kind, pr))
	}
	return head.SHA, nil
}
