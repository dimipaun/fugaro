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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"

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
	if email != account {
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
		return "", userErr("this installation names %d verifier accounts, and the launcher's verify call is one caller: a launcher that chose one would be choosing which identity verifies a verdict, which is the owner's to say, so a fix round is not launched", len(accounts))
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
	_, opener, err := OpenProvider(os.Environ(), nil)
	if err != nil {
		return "", remote(err)
	}
	p, _, err := opener(ctx, kind, repo)
	if err != nil {
		return "", remote(err)
	}
	head, err := p.FetchPRHead(ctx, pr)
	if err != nil {
		return "", remote(err)
	}
	if strings.TrimSpace(head.SHA) == "" {
		return "", remote(fmt.Errorf("%s reported no head for pull request #%d", kind, pr))
	}
	return head.SHA, nil
}
