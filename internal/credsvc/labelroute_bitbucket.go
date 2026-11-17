package credsvc

// The label route's Bitbucket half (D2736, G5): the one act a Bitbucket
// repository of the installation answers, and the read token it is performed
// with.
//
// `get` is that act, and it is the daemon's merge read: the state of one pull
// request — open, merged or closed — which is what the orchestrator waits on
// before it folds a run's outcome into the node. It serves a Bitbucket
// repository because the read the daemon needs there is exactly one the host
// can answer, and the four acts around it are refused as unsupported, because
// Bitbucket Cloud pull requests carry no labels at all (R7, and the host's own
// adapter says the same: internal/gitprov/bitbucket/labels.go).
//
// Four things are the whole of this half's design:
//
//   - The repository's HOST is decided here, and never stated by the caller
//     (labelHostOf): the name the call carries is resolved against the GitHub
//     inventory and then the Bitbucket one, because the two inventories are two
//     slugs of one table and a name may be held by both. A name both resolve is
//     refused as ambiguous rather than guessed at, and a name neither resolves
//     is refused as unknown — the daemon sends "owner/name" and the service, not
//     the daemon, is what knows which host that is.
//   - The token is minted from the installation's READ consumer and from nothing
//     else, with the read mint's own guards spelled out again below because this
//     is a second caller of it and not a second route: no consumer is a refusal
//     and never a fallback (ErrReadNoConsumer), the consumer's workspace must be
//     the repository's (G11, ErrReadOtherWorkspace), and only the read scope
//     allowlist is accepted (U11, readScopeAllowed). It is cached per WORKSPACE,
//     because that is what such a token reaches (U3), and it never leaves this
//     process: no answer, log line or error of this file carries it.
//   - The read goes through the host's own adapter (bitbucket.Provider), which
//     is the same code the daemon's own provider is (T1): the state it reports
//     is gitprov.PRState's spelling and the mapping from Bitbucket's words is
//     the adapter's, so the two ends of R8 cannot disagree about what "merged"
//     means. The answer carries no label field with anything in it, because the
//     host has none.
//   - Every refusal here names the READ CONSUMER as its credential and not the
//     App key the route's GitHub acts name (labelCredential): an operator
//     reading the line has to see which credential the act was performed with,
//     and a Bitbucket repository's acts are performed with this one — which is
//     also the only credential of the two an installation with Bitbucket alone
//     holds.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// The two refusals this half adds to the route's own.
var (
	// ErrLabelAmbiguousRepository is a repository name that resolves against
	// BOTH of the installation's inventories. It is refused rather than
	// resolved in either direction: the two readings are two different
	// repositories — one GitHub's and one Bitbucket's — and this service would
	// otherwise be deciding, from a name it cannot see the host of, which of
	// them a caller meant.
	ErrLabelAmbiguousRepository = errors.New("credsvc: the repository resolves against both of this installation's inventories")
	// ErrLabelBitbucket is bitbucket.org refusing the pull request read, or the
	// call to it failing. It is ErrLabelGitHub's counterpart, and like it the
	// host's own text reaches neither a caller nor a log line: the request that
	// failed carried an access token.
	ErrLabelBitbucket = errors.New("credsvc: bitbucket.org did not answer the label call")
)

// labelBitbucketMinValid is how long a cached Bitbucket read token must still
// be valid to be reused.
//
// It is internal/gitprov/bitbucket's own apiMinValid, spelled here because this
// service holds the token itself and hands the provider a static one: the
// provider's own "at least two minutes left" is asked of a TokenSource, and the
// source this file replaces that with is its cache below. A token with less than
// this left is re-minted rather than used, so no call of this route goes out on
// a token that expires while it is in flight.
const labelBitbucketMinValid = 2 * time.Minute

// labelHost is one repository's host as this route resolved it: the slug the
// line is keyed by, which host it is, and the two path segments that host's API
// addresses the repository by.
//
// owner and name are GitHub's owner and repository and Bitbucket's workspace and
// repository slug, which is one pair of fields rather than two because the route
// reads them in one place: which host they belong to is bitbucket below, and
// every use of them is behind a branch on it.
type labelHost struct {
	slug      string
	bitbucket bool
	owner     string
	name      string
}

// labelHostOf resolves the host of the repository one call names, and it is the
// only place the route decides which host an act is for.
//
// The GitHub inventory is read first and the Bitbucket one after it, and BOTH
// are read every time: the two slug derivations are different hashes of the same
// "owner/name" text, so a name the installation holds on both hosts is held
// under two keys and resolves twice. That is the ambiguous case and it is
// refused by name (ErrLabelAmbiguousRepository) rather than resolved by
// precedence: a rule that preferred one host would be a rule a caller could not
// see, and the wrong answer to it is a read of another repository's pull request.
//
// A name neither inventory holds is refused as unknown (ErrLabelUnknownRepository,
// the reading the GitHub read mint gives the same name), and the two errors each
// resolution answers with are dropped in its favour: which of the two inventories
// a repository is missing from is not a fact a caller may have, because "not in
// the GitHub one" is already half of what a Bitbucket-only installation's caller
// learns for free.
func labelHostOf(repos map[string]string, repo string) (labelHost, error) {
	ghSlug, ghOwner, ghName, ghErr := githubRepository(repos, repo)
	bbSlug, workspace, bbErr := workspaceOf(repos, repo)
	switch {
	case ghErr == nil && bbErr == nil:
		return labelHost{}, ErrLabelAmbiguousRepository
	case ghErr == nil:
		return labelHost{slug: ghSlug, owner: ghOwner, name: ghName}, nil
	case bbErr == nil:
		// The repository segment comes from the inventory's own canonical value
		// and not from the caller's text: the row is what this service resolved,
		// and the text is only what it was asked about. It is a second read of
		// the same row rather than a parser of this file's own, and it cannot
		// fail where workspaceOf above has succeeded — the two refuse the same
		// shapes — so the branch below is unreachable by construction. It is
		// answered rather than ignored because the alternative to refusing is a
		// host carrying an empty repository name.
		_, name, err := splitCanonicalRepo(repos[bbSlug])
		if err != nil {
			return labelHost{}, ErrLabelUnknownRepository
		}
		return labelHost{slug: bbSlug, bitbucket: true, owner: workspace, name: name}, nil
	}
	return labelHost{}, ErrLabelUnknownRepository
}

// checkAppID holds one call's App ID to what its host takes.
//
// A GitHub act names one and cannot be performed without it: the App ID is what
// the service signs its JWT as, and the service's own environment does not hold
// it. A Bitbucket act names NONE, and a call that carries one is refused: there
// is no GitHub App on that host, so an App ID beside a Bitbucket repository is
// either a caller that resolved the repository wrongly or a field this contract
// must not let a Bitbucket call carry at all — and the second is why the refusal
// is here rather than a field quietly dropped.
//
// The presence rule lives after the host is known (labelHostOf) and the SHAPE
// rule before it, in readLabelCall — which is the only place that rule is
// spelled, and which has already refused a malformed ID for every call by the
// time one reaches here: what is left at this point is presence alone, so a
// second shape check here would be a branch nothing can take. An empty field is
// a field this code cannot read as a malformed ID until it knows whether the
// host wants one, so a call whose App ID is absent is refused for the absence
// and not for its shape.
func (h labelHost) checkAppID(appID string) error {
	switch {
	case h.bitbucket && appID != "":
		// The field is named and its value is not, which is this package's rule
		// for a caller's own claim — and the shape rule before this point is
		// what makes the value a number if anything below ever wants it.
		return errors.New("the call names an App ID, and a Bitbucket call names none: that host has no GitHub App, and this route reads its pull requests with the installation's Bitbucket read consumer")
	case !h.bitbucket && appID == "":
		return errors.New("the call names no GitHub App ID, which every act on a GitHub repository takes")
	}
	return nil
}

// labelCredential is the credential one host's acts are performed with, and it
// is the name an audit line carries for the act.
//
// The purpose comes from routeAct, the ONE rule, and it is the same for both
// hosts: the act a line records is this route's and the two hosts are where it
// was performed. The credential is the one that differs, and it differs because
// the two acts really are performed with two credentials (D2736): a GitHub act
// with the App key's installation token, and a Bitbucket `get` with the token
// the installation's read consumer mints. A line that named the App key for a
// Bitbucket read would be claiming a credential this route never read.
func labelCredential(bitbucket bool) string {
	if bitbucket {
		return BitbucketReadConsumerSecret
	}
	_, credential := routeAct(LabelPath)
	return credential
}

// labelScopeRefusal is the read mint's scope guard (refusedScope, readScopeAllowed)
// as a refusal this file can carry out of the mint and answer with: it keeps the
// scope word the endpoint named, when it named one of the shape a scope word has,
// and the one sentence that says what to do about it.
//
// It is a type rather than a fmt.Errorf because the guard's sentence is built in
// two places already (bitbucketRead answers it with the same words) and the
// operator's way out is the same whatever was refused; a value keeps the two
// answers one sentence and lets this half name the scope in its own log line.
type labelScopeRefusal struct{ scope string }

func (e *labelScopeRefusal) Error() string {
	if e.scope == "" {
		return ErrReadUnexpectedScopes.Error() + ": " + readScopeFix
	}
	return fmt.Sprintf("%s (%s): %s", ErrReadUnexpectedScopes.Error(), e.scope, readScopeFix)
}

// labelBitbucketGet performs the one act a Bitbucket repository answers: the
// state of one pull request, read with a token the installation's read consumer
// mints.
//
// The order is bitbucketRead's order and its reasons, with the repository
// already resolved by the caller (labelHostOf): the bundle and the read
// consumer first, failing closed and never falling back to another entry
// (D143); the consumer's workspace against the repository's, which is the one
// refusal here about the consumer's reach rather than about the call (G11); the
// mint, whose scope guard is the read mint's own (U11); and the read, through
// the host's adapter. Nothing below the bundle is reached by a call this
// contract already refused.
func (s *Server) labelBitbucketGet(ctx context.Context, w http.ResponseWriter, caller, purpose string, host labelHost, call labelCall) {
	credential := labelCredential(true)
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, secret, consumerWorkspace, ok := b.BitbucketReadConsumer()
	if !ok {
		s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, ErrReadNoConsumer.Error())
		return
	}
	if host.owner != consumerWorkspace {
		// G11's refusal, BY NAME, exactly as the read mint answers it: the
		// repository is named and the consumer's workspace is not.
		s.refuseLabel(ctx, w, caller, host.slug, credential, StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(ErrReadOtherWorkspace, call.req.Repository))
		return
	}
	token, err := s.labelBitbucketToken(ctx, host.owner, key, secret)
	if err != nil {
		var scoped *labelScopeRefusal
		switch {
		case errors.As(err, &scoped):
			attrs := []any{"purpose", purpose, "credential", credential, "repository", host.slug}
			if scoped.scope != "" {
				attrs = append(attrs, "scope", scoped.scope)
			}
			s.log.Warn("credsvc: the Bitbucket token minted for a label read carries a scope this route does not hand on", attrs...)
			s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, scoped.Error())
		default:
			// The operator gets the class of the failure and, when the endpoint
			// answered, its status -- and never a byte of its text, for the read
			// mint's reason: the exchange carried the consumer's secret.
			attrs := []any{"purpose", purpose, "credential", credential, "repository", host.slug}
			var se *httpjson.StatusError
			if errors.As(err, &se) {
				attrs = append(attrs, "status", se.Status)
			}
			s.log.Warn("credsvc: the Bitbucket read token for a label read could not be minted", attrs...)
			s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, ErrReadBitbucket.Error())
		}
		return
	}
	provider, err := bitbucket.New(bitbucket.Options{
		Workspace: host.owner, Slug: host.name, Token: token, HTTP: s.cfg.Client,
	})
	if err != nil {
		s.log.Warn("credsvc: the Bitbucket provider for a label read could not be built", "purpose", purpose, "credential", credential, "repository", host.slug)
		s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, ErrLabelBitbucket.Error())
		return
	}
	info, err := provider.PullRequest(ctx, call.req.Number)
	if err != nil {
		s.log.Warn("credsvc: the Bitbucket pull request of a label read could not be read", "purpose", purpose, "credential", credential, "repository", host.slug)
		s.refuseLabel(ctx, w, caller, host.slug, credential, StatusUnavailable, ErrorStateUnavailable, ErrLabelBitbucket.Error())
		return
	}
	// No labels, and the field is present and empty rather than absent: "this
	// pull request carries none" is this host's answer and not an omission the
	// caller has to read as one.
	s.answerLabel(ctx, w, caller, host.slug, credential, creds.LabelAnswer{
		PullRequests: []creds.LabelPullRequest{{
			Number: call.req.Number, State: string(info.State), Labels: []string{},
		}},
	})
}

// labelBitbucketToken returns the read token this route performs a Bitbucket
// read with, minting one when the cached token is near its expiry.
//
// The check-then-mint runs under the cache's lock, exactly as the GitHub half's
// token source does: the reads of one tick reach this at once, and a mint is an
// act at bitbucket.org that must not happen once per call.
//
// The cache is keyed by WORKSPACE, which is what such a token is scoped to
// (U3): two repositories of one workspace read through the same token, and a
// token minted for one workspace is never used for a repository of another.
// That key is also why this file's workspace guard above is not merely a check
// on the mint: it is what makes the key the repository's own workspace rather
// than one the caller's text could suggest.
func (s *Server) labelBitbucketToken(ctx context.Context, workspace, key, secret string) (string, error) {
	st := s.labels
	st.bitbucket.mu.Lock()
	defer st.bitbucket.mu.Unlock()
	if held, ok := st.bitbucket.held[workspace]; ok && held.expires.Sub(s.now()) >= labelBitbucketMinValid {
		return held.token, nil
	}
	minted, err := bitbucket.MintAccessToken(ctx, bitbucket.Consumer{Key: key, Secret: secret}, bitbucket.DefaultOAuthRoot, s.cfg.Client)
	if err != nil {
		return "", err
	}
	if refused, scope := refusedScope(minted.Scopes); refused {
		return "", &labelScopeRefusal{scope: scope}
	}
	if st.bitbucket.held == nil {
		st.bitbucket.held = map[string]labelToken{}
	}
	st.bitbucket.held[workspace] = labelToken{token: minted.Token, expires: minted.ExpiresAt}
	return minted.Token, nil
}
