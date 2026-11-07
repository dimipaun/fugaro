// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package oauth is the member's half of the workspace's single sign-on: the
// exchange that accepts the workspace's member-bound assertion as the sign-in
// proof at the member's own `/oauth/authorize`, and the signer that mints one
// at the workspace (docs/workspaces/spec.md §3, D1293/D1294; findings 1, 2 and
// 7 of the post-merge security review).
//
// **What a member accepts from a workspace is one JWT, at one place, bound to
// that one member and to one use** (finding 1). The assertion is verified
// against the key, the workspace project and the issuer the member pins in ITS
// OWN configuration (W1's WorkspacePin), against the member's own exchange URL,
// against its expiry, and against the jti values this member has already spent.
// The pinned values are the member's own and never values a workspace supplies:
// the published workspace record names no key, no issuer, no project and no
// service account (§2, and `projectpolicy` writes none of them), so a
// "helpful" refactor that reached for the record would have nothing to read —
// and there is deliberately no constructor here that takes one.
//
// **The member mints nothing of its own from a workspace's say-so.** What the
// exchange answers is a verified identity and the member's OWN limits (Session),
// which is what the member's existing mint is built from: the assertion carries
// no scope, no limit and no token, and W5's admission reads the member's own
// launcher and operator lists beside this identity rather than any permission a
// workspace sent. The workspace cannot make anybody a launcher, and rule 1 of
// the folder survives exactly because the only permission decision this file
// makes is a refusal.
//
// **What the package does not do: the front door.** It mounts no route, serves
// no page and holds no PKCE state. The member keeps the MCP design's flow
// unchanged — S256 PKCE, its consent page and its paired-device approval (its
// D923) — and this package's answer is one more proof that flow accepts, which
// is why the exchange's entry point is a function over a verified assertion
// rather than a handler.
//
// **Hashes only** (D1296). The one thing the exchange stores is the sha256 of
// each jti it has spent, under a prefix no client of the member's own database
// may read — never the assertion, never a token value.
package oauth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// The pinned `firebase.sign_in_provider` set (finding 7, spec.md §3's "provider
// pin"): the two providers the workspace's own Firebase carries. `custom` and
// `anonymous` are refused, and the reason is the finding — anyone who can mint
// a custom token for the workspace's project (signBlob or TokenCreator on any
// service account in it) can set `email` and `email_verified` to whatever they
// like, and an anonymous sign-in proves no address at all, so a set that
// admitted either would make the whole admission chain a decoration.
//
// It is a set the member and the workspace's signer both read, and they are two
// binaries: a value added here is a value both halves must carry before it
// admits anything, which is why the set is a list of words and not a predicate
// a deployment could widen.
var PinnedProviders = []string{"google.com", "password"}

// ProviderPinned reports whether p is one of the pinned providers. It is the
// only test a provider is put to, so a near miss cannot be matched as a hit.
func ProviderPinned(p string) bool { return slices.Contains(PinnedProviders, p) }

// The refusals a caller acts on. Each refusal of an assertion is
// ErrUnverifiedAssertion with the rule named inside it and no value repeated:
// an assertion is a stranger's document, and a refusal reaches a log line and
// an answer body (the credsvc rule, applied to this surface).
var (
	// ErrUnverifiedAssertion is every assertion this member does not admit:
	// malformed, signed by a key this member does not pin, minted for another
	// member, expired, spent already, or carrying a claim this member refuses.
	// The refusals are one sentinel on purpose — a caller that branched on the
	// rule would be a caller deciding a permission question from a diagnostic —
	// and the rule is in the message for the operator's log.
	ErrUnverifiedAssertion = errors.New("oauth: the workspace assertion is not verified")
	// ErrNoWorkspacePinned is the member's own `workspace rm` having dropped
	// the membership key AND the pin: there is no key, no project and no issuer
	// left to verify against, so the exchange refuses whatever the workspace's
	// record still says about this member (spec.md §3, "the member's side is the
	// one that bites"). It is a refusal of its own rather than
	// ErrUnverifiedAssertion's, because it is a fact about this member's
	// configuration that its operator can fix and not a property of the
	// assertion that arrived.
	ErrNoWorkspacePinned = errors.New("oauth: this member pins no workspace, so no assertion is admitted")
	// ErrAssertionKeyUnreadable is a pin whose assertion key is not a public key
	// this exchange can verify against. It is a configuration mistake and is
	// reported as one at construction rather than as a refusal of every
	// sign-in.
	ErrAssertionKeyUnreadable = errors.New("oauth: the pinned assertion key cannot be read")
	// ErrUnusableExchangeURL is a member whose own exchange URL is not the
	// endpoint an assertion could name: it is derived from the member's own
	// coordinator URL, and a member whose coordinator is not an https origin
	// would bind assertions to a URL no client can reach.
	ErrUnusableExchangeURL = errors.New("oauth: the member's own exchange URL is not usable")
)

// ConsumedJTIPrefix is where a spent jti lives in the member's own store. It is
// a subtree of the member's own rather than a path in anything shared, and no
// client of the member's database may read it (D1296): a reader of these values
// holds, for each one, the hash of a jti it could present, and the hash is what
// makes holding one worth nothing.
const ConsumedJTIPrefix = "workspace_jti/"

// ConsumedJTI is the member's own record of the assertion jti values it has
// spent (finding 1's single-use rule).
//
// **The exchange hands it a hash and never a jti.** That is what "hashes only"
// means as code rather than as a rule (D1296): there is no call here that takes
// an assertion, a jti or a token value, so a store cannot keep one, and a
// reader of the store cannot present what it reads.
//
// **It is keyed by hash and bounded by the assertion's own expiry.** An entry
// is spent until the assertion it names has expired, and nothing shorter: a
// store that forgot a jti sooner than the assertion's `exp` would admit the
// same assertion a second time, which is exactly the replay the rule exists to
// refuse.
type ConsumedJTI interface {
	// Consume records hash as spent until expiresAt and reports whether it had
	// already been spent. already is true only for a value this store is still
	// holding: one whose assertion has expired is not a replay, because the
	// assertion it names has expired too.
	Consume(ctx context.Context, hash string, expiresAt time.Time) (already bool, err error)
}

// MemoryConsumedJTI is the consumed-jti set in this process's memory: the store
// a test and a single-process run use.
//
// It is NOT the member's deployed store, and the difference is the point of the
// interface: a member that restarted would forget what it spent, so the store
// that runs in the member's project is the same interface over the member's own
// database or bucket, in the subtree ConsumedJTIPrefix names, and it is mounted
// with the wiring that puts this exchange behind the member's `/oauth/authorize`
// — the same place the MCP front door's own Store is opened (mcpauth.OpenStore).
// Nothing in this file assumes the memory one.
type MemoryConsumedJTI struct {
	mu      sync.Mutex
	spent   map[string]time.Time
	expired int
	now     func() time.Time
}

// NewMemoryConsumedJTI returns an empty set. now reads the clock the entries
// are expired against (tests).
func NewMemoryConsumedJTI(now func() time.Time) *MemoryConsumedJTI {
	if now == nil {
		now = time.Now
	}
	return &MemoryConsumedJTI{spent: map[string]time.Time{}, now: now}
}

// Consume implements ConsumedJTI. An entry whose assertion has expired is
// dropped as it is read: the set would otherwise grow by one entry per sign-in
// forever, and the value it holds is worth nothing once the assertion it names
// is refused on its expiry anyway.
func (m *MemoryConsumedJTI) Consume(_ context.Context, hash string, expiresAt time.Time) (bool, error) {
	if hash == "" {
		return false, errors.New("oauth: the consumed-jti set is keyed by a hash")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if at, ok := m.spent[hash]; ok {
		if m.now().Before(at) {
			return true, nil
		}
		delete(m.spent, hash)
		m.expired++
	}
	m.spent[hash] = expiresAt
	return false, nil
}

// PathConsumedJTI is where one spent jti lives in the member's own store: the
// subtree's own prefix and the hash, and never the jti or the assertion. The
// caller passes a hash (mcpauth.TokenHash of the jti), which is what the store
// is written with.
func PathConsumedJTI(hash string) string { return ConsumedJTIPrefix + hash }

// Pin is the member's own record of the workspace it trusts: W1's
// localcfg.WorkspacePin, in the values this exchange verifies against, and
// never a value the workspace's record supplies (§2).
//
// It is a struct of this package's rather than localcfg's because this is the
// package that reads it and because the ee half is licensed and tested on its
// own: the CLI passes the four values across, and nothing below this line
// depends on how the file spells them.
type Pin struct {
	// Workspace is the workspace's name, as this member's own config records
	// it. It is NOT a verification input — a name proves nothing — and it exists
	// for the audit line a refusal writes.
	Workspace string
	// FirebaseProject is the WORKSPACE's Firebase project id: the `aud` of the
	// workspace's ID token (checked at the signer) and the project the assertion
	// must name.
	FirebaseProject string
	// Issuer is the securetoken issuer this member requires, and it must be
	// exactly `https://securetoken.google.com/<FirebaseProject>`
	// (localcfg.WorkspaceIssuer). The pair is the whole of what tells one
	// project's tokens from another's: Google publishes ONE global set of
	// Firebase signing keys, so a pin whose two halves disagree would admit
	// tokens the other half forbids.
	Issuer string
	// AssertionKey is the workspace's public key in PEM, the half this member
	// verifies an assertion against. The workspace's record names no key, so a
	// workspace cannot move a member's trust by publishing a different one.
	AssertionKey string
}

// IsZero reports whether this member pins no workspace at all, which is the
// state `fugaro workspace rm` leaves and the state the exchange refuses in
// (ErrNoWorkspacePinned).
func (p Pin) IsZero() bool { return p == Pin{} }

// WorkspaceIssuer is the issuer a workspace's Firebase project mints its tokens
// under. It is localcfg.WorkspaceIssuer's rule, restated here for the reason the
// pin's own comment gives: the ee half is built and tested without the core, and
// a derivation that differed by one character between the two would be a pin
// that admits nothing.
func WorkspaceIssuer(firebaseProject string) string {
	return "https://securetoken.google.com/" + firebaseProject
}

// VerifiedIdentity is who the assertion proved signed in at the workspace, as
// this member reads them. It is NOT a permission (D1294, D1295): whether that
// person may do anything here is the member's own launcher and operator lists'
// answer (W5), read beside this value rather than from it.
type VerifiedIdentity struct {
	// Email is the address the workspace's Firebase attested, in the canonical
	// form this member's one address rule produces (mobileauth.NormalizeEmail,
	// the rule W5's lists are written and compared with), so the comparison W5
	// makes is between one spelling and another.
	Email string
	// Provider is the `firebase.sign_in_provider` the sign-in used, re-checked
	// by this member against the pinned set rather than taken from the signer's
	// word (finding 7).
	Provider string
	// WorkspaceProject is the workspace's Firebase project id, which the
	// assertion names and this member's pin requires.
	WorkspaceProject string
	// EmailVerified is the assertion's statement that the address was verified.
	// It is true for every identity this exchange answers — an assertion that
	// says otherwise is refused — and it is carried rather than dropped because
	// it is a fact W5's admission reports on (spec.md §3 fact 2).
	EmailVerified bool
}

// Session is what the exchange answers once an assertion is verified: the
// identity, and the limits the member's own token will be minted with.
//
// The two halves have different owners and that is the whole point. Identity is
// what the workspace asserted; Limits is what THIS member's own configuration
// already said, carried unchanged. The assertion carries no limit and no scope,
// and a payload that tried to carry one is refused by the closed claim set
// before it is read, so a workspace cannot widen or narrow what its people may
// do at this member by anything it puts in an assertion.
type Session struct {
	Identity VerifiedIdentity
	Limits   mcpauth.Limits
}

// ExchangeConfig is what this member's own configuration gives the exchange.
type ExchangeConfig struct {
	// Pin is this member's own pin: the workspace key, project and issuer it
	// verifies against. The zero pin is allowed here and refused at the first
	// Admit (ErrNoWorkspacePinned), because a member whose `workspace rm` has
	// run is a member with a working installation that admits nobody, and not a
	// member that cannot start.
	Pin Pin
	// CoordinatorIssuer is this member's own coordinator URL — the value
	// mcpauth.Config.Issuer holds, scheme and host and nothing else. The
	// exchange URL an assertion must name is built from it, so the binding an
	// assertion carries is this member's own address rather than anything a
	// caller passed in.
	CoordinatorIssuer string
	// Limits are this member's own limits. They are the ones the member's token
	// is minted with and they are never read from an assertion.
	Limits mcpauth.Limits
	// Consumed is the member's own spent-jti set. A nil one is refused: an
	// exchange with nowhere to record a spent jti would admit every assertion
	// twice, which is the replay finding 1 exists to refuse.
	Consumed ConsumedJTI
}

// Exchange is one member's exchange: the verifier of the assertions arriving at
// its own authorization endpoint.
type Exchange struct {
	pin         Pin
	key         *rsa.PublicKey
	exchangeURL string
	limits      mcpauth.Limits
	consumed    ConsumedJTI
	providers   []string
	now         func() time.Time
}

// ExchangeOption configures an Exchange.
type ExchangeOption func(*Exchange)

// WithExchangeClock sets the clock the assertion's window is read against
// (tests).
func WithExchangeClock(now func() time.Time) ExchangeOption {
	return func(e *Exchange) { e.now = now }
}

// WithPinnedProviders replaces the pinned provider set (tests). It exists so
// that the set's refusal can be exercised against a set of the test's own, and
// a deployment never calls it: the set is the finding's answer, not a knob.
func WithPinnedProviders(providers []string) ExchangeOption {
	return func(e *Exchange) { e.providers = append([]string(nil), providers...) }
}

// maxAssertionLifetime bounds an assertion's window, measured from its own
// `iat`. It is the member's check and not the signer's promise: the workspace's
// signing key can mint anything its holder likes, and a pin bounds what a key
// may be USED for, not that its holder is careful. Fifteen minutes is the
// longest a "short-lived" assertion can be and still bound a stolen one by its
// lifetime (spec.md §8's revocation paragraph is the same argument), and the
// signer's own default is shorter.
const maxAssertionLifetime = 15 * time.Minute

// assertionClockSkew is how far ahead of this member's clock an assertion's iat
// may be: the member's clock and the workspace's signer are not the same clock,
// and an assertion a minute early is one a person is holding.
const assertionClockSkew = 2 * time.Minute

// NewExchange returns the exchange for one member.
//
// The member's own exchange URL is derived here from the member's own
// coordinator URL and the MCP design's authorize path, and it is refused when
// that is not an https origin: a member whose coordinator URL is not one would
// be a member binding assertions to an address no browser can reach, and the
// refusal belongs at the start rather than at every sign-in.
func NewExchange(cfg ExchangeConfig, opts ...ExchangeOption) (*Exchange, error) {
	if cfg.Consumed == nil {
		return nil, errors.New("oauth: an exchange needs a consumed-jti set: without one every assertion is admitted twice")
	}
	exchangeURL, err := ExchangeURL(cfg.CoordinatorIssuer)
	if err != nil {
		return nil, err
	}
	e := &Exchange{
		pin:         cfg.Pin,
		exchangeURL: exchangeURL,
		limits:      cfg.Limits,
		consumed:    cfg.Consumed,
		providers:   append([]string(nil), PinnedProviders...),
		now:         time.Now,
	}
	for _, o := range opts {
		o(e)
	}
	if !cfg.Pin.IsZero() {
		key, err := parseAssertionKey(cfg.Pin.AssertionKey)
		if err != nil {
			return nil, err
		}
		e.key = key
	}
	return e, nil
}

// ExchangeURL is a member's own exchange URL: its coordinator's issuer joined
// with the authorize path the MCP design puts the sign-in proof at. It is the
// value an assertion's `aud` must be, and no caller passes it in from anywhere
// else — an audience a caller could choose would be an audience a caller could
// point at another member.
func ExchangeURL(coordinatorIssuer string) (string, error) {
	raw := strings.TrimSuffix(strings.TrimSpace(coordinatorIssuer), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: it must be the https origin this member's coordinator answers on, with no path, query or fragment", ErrUnusableExchangeURL)
	}
	return raw + mcpauth.AuthorizePath, nil
}

// Admit verifies a presented assertion and answers the identity it proves
// together with this member's own limits, or a refusal.
//
// The checks are ordered the way a caller can act on them, and every one of
// them reads the member's own pin or the member's own URL: the assertion's
// issuer and project are compared against this member's pin, its audience
// against this member's own exchange URL, its window against this member's
// clock, its address against this member's own address rule, and its jti
// against the values this member has already spent.
func (e *Exchange) Admit(ctx context.Context, raw string) (Session, error) {
	if e.pin.IsZero() {
		return Session{}, ErrNoWorkspacePinned
	}
	a, err := verifyAssertion(e.key, raw)
	if err != nil {
		return Session{}, err
	}
	folded, err := mobileauth.NormalizeEmail(a.Email)
	if err != nil {
		return Session{}, refusef("the assertion's address is not one this member can read")
	}
	return Session{
		Identity: VerifiedIdentity{
			Email:            folded,
			Provider:         a.Provider,
			WorkspaceProject: a.Subject,
			EmailVerified:    a.EmailVerified,
		},
		Limits: e.limits,
	}, nil
}
