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
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runner"
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
		IDToken:    func(ctx context.Context) (string, error) { return reviewVerifierIdentity(ctx, url) },
		Account:    account,
		Run:        run,
		Repository: repository,
	}, nil
}

// reviewVerifierIdentity mints the launcher's Google ID token for the verify
// route's audience, which is the service's own URL (C1.14).
//
// WHICH identity the launcher impersonates was Open item 5 of
// docs/cloud-senior-review/implementation-plan.md §6.6, and the owner settled
// it on 2026-11-05: a dedicated verifier service account the installation root
// creates and names in credentials_service.verifier_accounts, which this
// launcher impersonates. That answer is also the one identity it may NOT be,
// the launcher person's own address: every `user:` operator is already a
// person account to the service, and NewAuthenticator refuses to start an
// installation whose verifier list names one (internal/credsvc/auth.go), so
// the caller is that service account — the launcher-side twin of the job's
// metadata identity.
//
// Neither the account nor the published field it is named through exists yet:
// both are CSR14/CSR15 of the same document, so the stub below stays as it is
// — fail-closed, and not a temporary state to relax — until they do. The seam
// above is what the tests reach the reader with meanwhile.
func reviewVerifierIdentity(context.Context, string) (string, error) {
	return "", fmt.Errorf("the launcher has no verifier identity to call the credentials service's verify route as: the installation root creates a dedicated verifier service account and names it in credentials_service.verifier_accounts, which this launcher impersonates once that account is real (docs/cloud-senior-review/implementation-plan.md CSR14/CSR15: neither exists yet). No fix round is launched on a verdict nobody verified")
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
