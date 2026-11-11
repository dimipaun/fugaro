package gitprov

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The ladder's write calls: marking a pull request ready and merging it, each
// bound to the one head a person's review and the gate's checks were about
// (E8, G2, G14). Nothing here merges past branch protection: the call sends no
// admin or bypass flag, and a provider whose merge cannot be bound to an
// expected head refuses rather than merging whatever the head happens to be.

// The refusals of a head-bound write call. They are sentinels so a caller can
// tell a refusal it must report — and a rung that must end with
// waiting_on: person — from an outage it may retry.
var (
	// ErrHeadMoved is a pull request whose head is not the commit the call
	// named. It is the refusal G2 exists for: what was reviewed, checked and
	// judged is the commit the caller carries, so a head that has moved is a
	// new review and not a retry. Every head-bound call re-reads the head and
	// refuses with this rather than acting on the one it finds.
	ErrHeadMoved = errors.New("the pull request's head is not the commit the call named")
	// ErrMergeNoHeadPrecondition is a merge call on a provider whose merge
	// API has no expected-head precondition (F24: Bitbucket's was not shown
	// to have one). Merging there would be a merge at whatever head the pull
	// request is at when the call lands, which is the one thing G2 forbids,
	// so the ladder refuses and asks a person instead of merging unbound.
	ErrMergeNoHeadPrecondition = errors.New("this provider's merge cannot be bound to an expected head")
	// ErrNoMergeMint is a ready or merge call on a provider that has no merge
	// credential installed (UseMergeMint). It is a refusal and never a
	// fallback: the credential these calls take is the gated mint's (E16,
	// D2049), and a provider that quietly used its own instead would be a
	// merge outside the gate.
	ErrNoMergeMint = errors.New("no merge credential is installed on this provider")
	// ErrChecksUnsupported is a Checks read on a provider that cannot report
	// a commit's checks at all. It is a read that fails rather than a commit
	// with no checks: the caller sees an error and never a state it could
	// mistake for green.
	ErrChecksUnsupported = errors.New("this provider cannot read a commit's checks")
)

// MergeMinter is where a ready or merge call's credential comes from: the
// gated, audited mint E16 builds (D2049), which mints one short-lived
// installation token for one pull request, bound to the head the call named
// and refused when the base's bypass list names the App.
//
// It is the same shape as TokenSource, and for the same reason: the provider
// asks for a credential per call and holds none of its own. It is not
// TokenSource, because a merge token is bound to a head and to one pull
// request, and the mint is the one place that binding is checked before a
// credential exists (credsvc/mergemint.go).
//
// creds.MergeMinter is the client the daemon holds; it is adapted to this
// interface where the daemon wires the provider (E10b), because gitprov sits
// below internal/creds and cannot import it.
type MergeMinter interface {
	// MintMerge mints a token for repository ("owner/name") and pull request
	// pr, which must still be at headSHA. The mint refuses when it is not, so
	// a caller that gets a token holds one the gate checked.
	MintMerge(ctx context.Context, repo string, pr int, headSHA string) (token string, expires time.Time, err error)
}

// MergeMintSetter is implemented by a provider whose ready and merge calls
// take their credential from the gated merge mint: UseMergeMint installs the
// source both calls then use. It mirrors ServiceTokenSetter, including the
// rule that a provider without one refuses those calls rather than reaching
// for another credential of its own.
//
// Bitbucket's provider does not implement it: there is no mint for it (E16's
// route is GitHub's App), and its merge refuses for the reason that matters
// more (ErrMergeNoHeadPrecondition).
type MergeMintSetter interface {
	UseMergeMint(MergeMinter)
}

// CheckExpectedHead compares a pull request's current head with the commit a
// caller carries, the way every head-bound call must: with SameCommit, so an
// abbreviated head a host reports (Bitbucket gives 12 hex) still matches the
// full one, and with no tolerance for an empty or malformed value on either
// side — an empty head is not a commit anybody reviewed, and a call that
// carries none is refused rather than treated as "no expectation".
func CheckExpectedHead(current, expected string) error {
	if !SameCommit(current, expected) {
		return fmt.Errorf("%w: it is at %s, and the call named %s", ErrHeadMoved, orNone(current), orNone(expected))
	}
	return nil
}

// orNone spells an absent commit for a message, so a refusal reads as the
// missing value it is rather than as an empty gap.
func orNone(sha string) string {
	if sha == "" {
		return "no commit"
	}
	return sha
}
