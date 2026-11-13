// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal API's sign-in (P1.3 of docs/portal/implementation-plan.md; the
// spec's auth section as amended by F10, F31 and F33 of
// docs/portal/reconciliation.md).
//
// **What is verified.** The SPA signs in with Firebase Auth and presents the
// project's ID token, and the token's own rules — the audience and the issuer
// built from the project id, a verified address, the window — are
// mcpauth.VerifyIDToken's, which this file calls rather than copies (F31: the
// portal is the second caller D923's comment names, and a second implementation
// of those rules is the thing the front door's review asked to be told about).
//
// **What this file adds** is every rule that depends on WHO is asking, which the
// token cannot answer: the SSO rule of the membership the request uses (D2207,
// F10), the age of the sign-in itself (`auth_time`, not the token's `iat`, which
// is what D2187's five minutes are about), and the tenant a Microsoft sign-in
// has to come from. The addresses are compared through access.Member on BOTH
// sides (F31): the member this verifier builds and the member a stored record
// carries are one spelling of one person, so a list entry cannot be missed by a
// fold or matched by a domain's case. The member is built from the address as
// the token CLAIMED it and not from mcpauth's folded one, because the two folds
// are not the same fold: mcpauth lower-cases the whole address and access.Member
// keeps the local part byte for byte, so building it from the folded spelling
// would give a member a capitalised local part can never equal.
//
// **D2207, and the owner's ruling of 2026-11-13 ("Pin the real directory id").**
// The operations organisation accepts `google.com` only. A reseller organisation
// accepts Google, or Microsoft only when the VERIFIED token's Microsoft (Entra
// ID) directory id — the `tid` claim, which mcpauth reads out of the signed
// token and which the person signing in cannot choose — is on the reseller's
// pinned list.
//
// **What P1.3 first pinned, and what #2515's fourth review round found.** P1.3
// compared the list against `firebase.tenant`, and that claim is the Identity
// Platform tenant the sign-in was MADE IN (mcpauth.Identity.TenantID), which the
// CLIENT names when it calls the SDK. A list entry over it therefore admits
// whoever names a pinned value rather than the directory the person's account
// lives in — the tenant is a door, not an identity — and the list read as a
// directory control only because the deployment was also expected to back that
// tenant's Microsoft provider with a single-tenant Azure app registration. The
// pin is now the directory and
// nothing else: `SignIn.TenantID` is carried and RequireSSO never reads it.
//
// **Where the directory id comes from.** mcpauth reads `tid` from the verified
// token, in either shape a deployment's Microsoft provider writes it: the
// token's own claim set, or `firebase.sign_in_attributes`, where Identity
// Platform's OIDC providers put the provider's own claims (a `tid` in both, and
// disagreeing, is answered as none). Both are inside the signed payload, so the
// value is the provider's own report of where the account lives — the property
// `firebase.tenant` does not have. Which shape a project's provider actually
// mints is deployment configuration this file can neither see nor check (P0.1's
// provider accounts, docs/portal/reconciliation.md): a provider that forwards no
// `tid` at all leaves every `microsoft.com` sign-in carrying no directory, and
// that is refused below.
//
// **The single-tenant app registration is a belt-and-braces and not the
// control.** Backing a tenant's Microsoft provider with a registration for one
// directory still narrows which directories can reach that provider, and an
// owner who wants the provider itself to stop at one directory keeps doing it.
// It is no longer what makes the list a directory control: the `tid` in the
// token is, and a multi-tenant registration can no longer turn a pinned entry
// into an admission for every Entra directory there is.
//
// A deployment that cannot get a directory into the token takes D2207's own
// fallback for "no usable Microsoft directory id": a reseller organisation is
// Google-only too, which is the zero SSORule — no pinned directory, and so every
// `microsoft.com` sign-in refused — and the state the recorded shape is already
// in. `TestResellerMicrosoftNeedsAPinnedDirectory` is what holds that. The
// recorded shape is also what P2.2 needs about `email_verified`: it is a claim
// Firebase sets from the provider's own report, and this verifier requires it of
// every sign-in, so whether a Microsoft directory's addresses arrive verified
// never decides an admission here — no Microsoft sign-in without a pinned
// directory is admitted at all.
//
// **The Scheduler token is a different document** (F33): a Cloud Scheduler job
// presents an OIDC ID token Google signs, and the API checks its ACCOUNT and its
// AUDIENCE, both against what the deployment configured, before it runs a job's
// work. Its keys are Google's OIDC certs and not the securetoken JWKS, so it
// has a verifier of its own below.
//
// Nothing on a refusal path repeats a value out of a token: a refusal reaches a
// log line and an answer body, and the token, the provider, the tenant and the
// address are all text a stranger's sign-in chose.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/access"
)

const (
	// The `firebase.sign_in_provider` values this file names. The other two a
	// person can reach in the portal's project are `password` and `emailLink` —
	// the two spellings a passwordless sign-in is recorded under, both of them
	// refused by RequireSSO, which is what the reconciliation's UNVERIFIED item
	// about the email-link provider asks P1.3 to pin.
	signInGoogle    = "google.com"
	signInMicrosoft = "microsoft.com"
	// signInClockSkew is how far ahead of this service's clock a sign-in may be.
	// It is the tolerance mcpauth's own token window carries, and it is here for
	// the same reason: the signer's clock and this service's are not the same
	// clock, and a sign-in a minute early is one a person is making.
	signInClockSkew = 2 * time.Minute

	// GoogleOIDCKeysURL is where Google publishes the keys that sign the OIDC ID
	// tokens it mints, which is the set a Cloud Scheduler job's token verifies
	// against. It is a different document from the securetoken JWKS an ID token
	// is checked against, and it is served in the same shape.
	GoogleOIDCKeysURL = "https://www.googleapis.com/oauth2/v3/certs"
	// maxOIDCTokenBytes bounds a presented Scheduler token before it is parsed,
	// for the reason mcpauth bounds a Firebase one: it arrives as one header
	// value, and one past the bound is refused at the door rather than decoded.
	maxOIDCTokenBytes = 8 << 10
	// maxOIDCEmailBytes bounds the address claim of a Scheduler token. A service
	// account's address is the longest this claim is expected to be, by orders
	// of magnitude.
	maxOIDCEmailBytes = 512
	// The two spellings Google uses for its own issuer. Both are published as
	// this issuer's value, and admitting one and refusing the other would refuse
	// a job whose token Google minted correctly.
	googleIssuerPrefix = "https://accounts.google.com"
	googleIssuerBare   = "accounts.google.com"
	schedulerTokenAlg  = "RS256"
	maxOIDCHeaderBytes = 4 << 10
	maxOIDCClaimsBytes = 64 << 10
)

// The refusals of a sign-in. They are sentinels of their own rather than one
// answer-to-everything, because the caller's answer differs: the SPA opens its
// "sign in with Google or Microsoft" dialog for ErrSSORequired, an operations
// actor is asked for a fresh sign-in on ErrStaleSignIn, and a token this project
// never signed is an answer no dialog explains.
var (
	// ErrUnverifiedSignIn is a sign-in this service does not admit: a token
	// another project minted, an unverified or unreadable address, a window that
	// has closed, or an address that is no member. It is deliberately NOT the
	// answer to a key set that could not be read — that error is passed through
	// as it arrives, because a service answering "not you" to its own outage
	// sends every person to sign in again.
	ErrUnverifiedSignIn = errors.New("keyservice: the portal sign-in is not verified")
	// ErrSSORequired is a sign-in that is not one the membership the request
	// uses admits (D2207, F10): an email-link or password sign-in, a provider the
	// portal never offers, or a Microsoft sign-in at the operations organisation,
	// which is Google-only.
	ErrSSORequired = errors.New("keyservice: this membership requires a Google or Microsoft SSO sign-in")
	// ErrDirectoryNotPinned is a Microsoft sign-in whose Microsoft (Entra ID)
	// directory is not one the organisation pinned: no directory in the token at
	// all, or one outside its list. It is a sentinel of its own and not
	// ErrSSORequired because the two answers differ for the person: an SSO
	// refusal is "sign in the other way", and this one is "your directory is not
	// one this organisation sells through", which no other button fixes.
	ErrDirectoryNotPinned = errors.New("keyservice: the Microsoft sign-in is not from a pinned directory")
	// ErrStaleSignIn is a sign-in older than the action allows (D2187's five
	// minutes for a mint, P5.1's reads included), or one that attests no sign-in
	// time at all.
	ErrStaleSignIn = errors.New("keyservice: the sign-in is older than this action allows")
	// ErrUnverifiedSchedulerToken is every Scheduler token this service does not
	// admit: malformed, signed by a key Google does not publish, minted for
	// another account or another audience, or expired.
	ErrUnverifiedSchedulerToken = errors.New("keyservice: the Cloud Scheduler token is not verified")
)

// SignIn is a verified portal sign-in: who signed in, with what, when, and the
// member the address folds to.
//
// It is not a permission. What the person may do is their memberships' answer,
// read from the store where the request is turned into an action; what this
// carries is only what the token proved and what a rule over it needs.
type SignIn struct {
	// UID is the Firebase uid, which is the key every membership record is
	// written under — an address changes hands, a uid does not.
	UID string
	// Email is the address the token asserted, folded by mcpauth.
	Email string
	// Member is access.Member of the address as the token CLAIMED it — not of
	// Email, which mcpauth has folded whole: access.Member keeps the local part
	// byte for byte, so a member built from the folded spelling is one no stored
	// record can equal. It is empty only when the address is no member, which
	// Verify refuses.
	Member string
	// Provider is `firebase.sign_in_provider`, read by mcpauth. It is empty when
	// the token carries none, which RequireSSO refuses.
	Provider string
	// AuthTime is when the person's sign-in happened (`auth_time`), which is
	// what FreshWithin measures. It is the zero time when the token carries no
	// readable one, which FreshWithin refuses.
	AuthTime time.Time
	// TenantID is `firebase.tenant`, the Identity Platform tenant the sign-in
	// came from, or empty when the project runs none. It is the tenant the
	// client asked for at sign-in — a door and not an identity — and NOT the
	// Microsoft (Entra ID) directory the person's account lives in, which is
	// DirectoryID. RequireSSO reads the directory and never this, because a rule
	// over a value the client chooses admits whoever chooses a pinned one.
	TenantID string
	// DirectoryID is the Microsoft (Entra ID) directory the verified token
	// reports the person's account lives in: the `tid` claim, read by mcpauth out
	// of the token's own claim set or out of `firebase.sign_in_attributes`, both
	// of which are inside the signed payload. It is empty for a Google sign-in,
	// for a Microsoft one whose provider forwarded no directory, and for one
	// whose two claims disagree — which RequireSSO refuses.
	DirectoryID string
}

// SSORule is what the organisation a request USES demands of the sign-in
// (D2207, F10). The rule follows the actor's membership and not the organisation
// the request targets, so it is passed for a request that uses an operations or
// a reseller membership whatever that request is about.
//
// It is the caller's to build from the organisation's own record — operations
// sets the two lists (P5.1) — and its zero value is the strictest rule there is:
// SSO with no Microsoft directory, which is Google only.
type SSORule struct {
	// Operations is the operations organisation, which accepts `google.com`
	// only: a Microsoft sign-in is refused whatever MicrosoftDirectories holds,
	// because D2207's answer for operations is Google alone and a list that
	// could widen it is a list somebody would set.
	Operations bool
	// MicrosoftDirectories is a reseller organisation's pinned Microsoft (Entra
	// ID) directory list, and D2207's condition for Microsoft as the owner's
	// ruling of 2026-11-13 pins it: a `microsoft.com` sign-in is admitted only
	// when the VERIFIED token's `tid` is one of these. Empty or nil refuses every
	// Microsoft sign-in (F40), which is what a reseller is left with until the
	// owner pins a directory its Microsoft provider forwards into the token — a
	// list over the Identity Platform tenant a client names would instead admit
	// whoever named a pinned value (this file's comment). Entries are compared
	// exactly, as the memberships in this tree are: the value is one the owner
	// copies out of the token, and a second fold here would be one spelling of a
	// directory that only one of the two sides applies.
	MicrosoftDirectories []string
}

// PortalVerifier verifies the portal's sign-in tokens and applies the rules a
// membership adds over them.
type PortalVerifier struct {
	verify *mcpauth.Verifier
	now    func() time.Time
}

// PortalOption configures a PortalVerifier.
type PortalOption func(*PortalVerifier)

// WithPortalClock sets the clock a sign-in's age is measured against (tests).
func WithPortalClock(now func() time.Time) PortalOption {
	return func(v *PortalVerifier) { v.now = now }
}

// NewPortalVerifier returns the verifier for one portal project, reading the
// published key set through keys.
//
// The project id and the key set are refused here rather than at the first
// sign-in, for mcpauth's reason: both are configuration mistakes, and a verifier
// built anyway refuses every person with a message that reads like a bad token.
func NewPortalVerifier(projectID string, keys mcpauth.KeySource, opts ...PortalOption) (*PortalVerifier, error) {
	v := &PortalVerifier{now: time.Now}
	for _, o := range opts {
		o(v)
	}
	// ONE clock for both rules: the token's own window is read by mcpauth's
	// verifier and the sign-in's age by FreshWithin, and two clocks would be a
	// deployment where a token is inside its window and its sign-in is from the
	// future, or the reverse, for a reason nobody could see from either answer.
	firebase, err := mcpauth.NewVerifier(projectID, keys, mcpauth.WithVerifyClock(v.now))
	if err != nil {
		return nil, err
	}
	v.verify = firebase
	return v, nil
}

// Verify verifies a Firebase ID token and answers the sign-in it attests.
//
// Every rule the token itself is held to — audience, issuer, a verified
// address, the window, and the uid's two claims agreeing — is
// mcpauth.VerifyIDToken's, and ErrUnverifiedSignIn wraps its refusal so a caller
// can answer the person without reading a message. A key set that could not be
// read is passed through unwrapped, because it is this service's failure and not
// the person's.
func (v *PortalVerifier) Verify(ctx context.Context, raw string) (SignIn, error) {
	id, err := v.verify.VerifyIDToken(ctx, raw)
	if err != nil {
		if errors.Is(err, mcpauth.ErrUnverifiedIDToken) {
			return SignIn{}, fmt.Errorf("%w: %w", ErrUnverifiedSignIn, err)
		}
		return SignIn{}, err
	}
	// The member is built from the address AS CLAIMED and not from mcpauth's
	// folded one: access.Member keeps the local part byte for byte while mcpauth
	// folds the whole address, so building this from the folded spelling gives a
	// member whose local part is lower-cased — one a stored record written from
	// a capitalised address can never equal, and one that merges two spellings
	// access.Member calls two people.
	member, err := access.Member(id.ClaimedEmail)
	if err != nil {
		// mcpauth has already refused a claim that is not printable ASCII, so
		// this is the "not a member" half of its rule: an address this tree
		// cannot spell as a member is one no stored membership can be compared
		// against, and the refusal names the shape rather than the address.
		return SignIn{}, fmt.Errorf("%w: the identity's address is not one this service can compare", ErrUnverifiedSignIn)
	}
	return SignIn{
		UID:         id.UID,
		Email:       id.Email,
		Member:      member,
		Provider:    id.Provider,
		AuthTime:    id.AuthTime,
		TenantID:    id.TenantID,
		DirectoryID: id.DirectoryID,
	}, nil
}

// RequireSSO refuses a sign-in the membership the request uses does not admit
// (D2207, F10): anything but `google.com` and `microsoft.com` is refused before
// the rule is read, a Microsoft sign-in at the operations organisation is
// refused outright, and at a reseller it is admitted only when the VERIFIED
// token's Microsoft (Entra ID) directory id is on the rule's list — the `tid`
// the provider wrote, never the Identity Platform tenant the client named at
// sign-in, which is carried on the sign-in and read nowhere here.
//
// It is called for a request that uses an operations or a reseller membership —
// the rule follows the ACTOR and not the organisation the request is about — and
// a customer membership is not one of those, so a request that uses only one
// never reaches this.
func (v *PortalVerifier) RequireSSO(signIn SignIn, rule SSORule) error {
	switch signIn.Provider {
	case signInGoogle:
		return nil
	case signInMicrosoft:
		if rule.Operations {
			return fmt.Errorf("%w: the operations organisation signs in with Google only (D2207)", ErrSSORequired)
		}
		if signIn.DirectoryID == "" || !slices.Contains(rule.MicrosoftDirectories, signIn.DirectoryID) {
			return fmt.Errorf("%w: a Microsoft sign-in is admitted only when the token's tid is a directory the organisation pinned", ErrDirectoryNotPinned)
		}
		return nil
	default:
		// Everything else — `password`, `emailLink`, `custom`, `anonymous`, a
		// provider the project has not enabled, or a token carrying none — is
		// refused by the same rule, and the refusal names no value: what the
		// token said is the caller's text and a log line is not a place for it.
		return fmt.Errorf("%w: the sign-in used a provider that is not SSO, and this membership admits nothing else", ErrSSORequired)
	}
}

// FreshWithin refuses a sign-in that happened longer ago than within (D2187's
// five minutes for a mint; P5.1's reads are the other caller).
//
// The age is `auth_time`'s and not the token's `iat`: an ID token lives an hour,
// so a token minted this minute can attest a sign-in from this morning, and a
// step-up measured on `iat` would be a step-up over the wrong fact.
func (v *PortalVerifier) FreshWithin(signIn SignIn, within time.Duration) error {
	if within <= 0 {
		// A window of nothing is a configuration mistake, and the answer that
		// keeps it from becoming "everything is fresh" is a refusal.
		return fmt.Errorf("%w: a freshness window of %s admits no sign-in at all", ErrStaleSignIn, within)
	}
	if signIn.AuthTime.IsZero() {
		return fmt.Errorf("%w: the token attests no sign-in time, and a step-up over one nobody recorded is no step-up", ErrStaleSignIn)
	}
	if signIn.AuthTime.After(v.now().Add(signInClockSkew)) || v.now().Sub(signIn.AuthTime) > within {
		return fmt.Errorf("%w: the sign-in is not within the last %s", ErrStaleSignIn, within)
	}
	return nil
}

// SameMember reports whether two addresses are one member of this tree: both
// sides go through access.Member, so a stored record and a verified sign-in
// cannot spell one person two ways (F31).
//
// An address that is no member at all answers false rather than an error: every
// caller of this is a permission check, where the only two answers that mean
// anything are "yes" and "not this person", and a caller that had to read an
// error to tell them apart is one that can get it wrong.
func SameMember(a, b string) bool {
	left, err := access.Member(a)
	if err != nil {
		return false
	}
	right, err := access.Member(b)
	if err != nil {
		return false
	}
	return left == right
}

// GoogleOIDCKeys is the key source over Google's published OIDC certs, which is
// the set a Cloud Scheduler token is verified against. Build ONE per deployment:
// the source caches the set and reads Google's endpoint again only when the
// cache has run out or a token names a key the set does not hold, so one built
// per request would read that endpoint on every request.
func GoogleOIDCKeys() mcpauth.KeySource {
	return mcpauth.NewSecureTokenKeys(mcpauth.WithKeysEndpoint(GoogleOIDCKeysURL))
}

// SchedulerVerifier verifies the OIDC ID token a Cloud Scheduler job presents to
// the routes that run a job's work (F33): its ACCOUNT and its AUDIENCE, both
// against the deployment's own values, and the token's own signature and window
// under them.
//
// The account is what the deployment configured the job to run as, and the
// audience is the route's own answer URL — a Cloud Scheduler job mints its
// token for the target it calls. A verifier that read only one of them would
// admit a token the other job in the project minted, or one minted for another
// service.
type SchedulerVerifier struct {
	account  string
	audience string
	keys     mcpauth.KeySource
	now      func() time.Time
}

// SchedulerOption configures a SchedulerVerifier.
type SchedulerOption func(*SchedulerVerifier)

// WithSchedulerClock sets the clock the token's window is read against (tests).
func WithSchedulerClock(now func() time.Time) SchedulerOption {
	return func(s *SchedulerVerifier) { s.now = now }
}

// NewSchedulerVerifier returns the verifier for one job account and one
// audience, reading Google's published keys through keys.
//
// An empty account, an empty audience or no key set at all is refused here: each
// is a configuration mistake, and a verifier built anyway would refuse every
// job with a message that reads like a bad token — or, worse, admit one by
// comparing against nothing.
func NewSchedulerVerifier(account, audience string, keys mcpauth.KeySource, opts ...SchedulerOption) (*SchedulerVerifier, error) {
	if account == "" {
		return nil, errors.New("keyservice: a Scheduler verifier needs the account the job runs as")
	}
	if audience == "" {
		return nil, errors.New("keyservice: a Scheduler verifier needs the audience the job's token is minted for")
	}
	if keys == nil {
		return nil, errors.New("keyservice: a Scheduler verifier needs a key source: the published set is the only proof a token has")
	}
	s := &SchedulerVerifier{account: account, audience: audience, keys: keys, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Verify verifies a Scheduler OIDC token: its signature against Google's
// published keys, its issuer, its account, its audience and its window, in that
// order — the order a caller can act on, signature first because nothing in the
// claims is worth reading before it.
func (s *SchedulerVerifier) Verify(ctx context.Context, raw string) error {
	head, claims, err := decodeOIDCToken(raw)
	if err != nil {
		return fmt.Errorf("%w: the token is not a JWT this service can read", ErrUnverifiedSchedulerToken)
	}
	if alg := oidcClaimString(head, "alg"); alg != schedulerTokenAlg {
		return fmt.Errorf("%w: the token is not signed with %s", ErrUnverifiedSchedulerToken, schedulerTokenAlg)
	}
	key, err := s.keys.Key(ctx, oidcClaimString(head, "kid"))
	if err != nil {
		if errors.Is(err, mcpauth.ErrUnknownKeyID) {
			return fmt.Errorf("%w: %w", ErrUnverifiedSchedulerToken, err)
		}
		return err
	}
	if key == nil {
		// A key source that answers no error and no key is one nothing can be
		// checked against, and the alternative to this refusal is a signature
		// check that reads through the missing key.
		return fmt.Errorf("%w: no key was returned for the token's key id", ErrUnverifiedSchedulerToken)
	}
	if err := verifyOIDCRS256(raw, key); err != nil {
		return fmt.Errorf("%w: the token's signature does not verify", ErrUnverifiedSchedulerToken)
	}
	switch iss := oidcClaimString(claims, "iss"); iss {
	case googleIssuerPrefix, googleIssuerBare:
	default:
		return fmt.Errorf("%w: the token was issued by an issuer that is not Google's", ErrUnverifiedSchedulerToken)
	}
	if aud := oidcClaimString(claims, "aud"); aud != s.audience {
		// The audience is what tells this route's token from the same project's
		// other jobs' tokens, so the refusal is about the check and not about
		// the value: naming the configured audience would put the deployment's
		// own URL in an answer body.
		return fmt.Errorf("%w: the token was minted for another audience", ErrUnverifiedSchedulerToken)
	}
	if verified, ok := claims["email_verified"].(bool); !ok || !verified {
		return fmt.Errorf("%w: the token's address is not verified", ErrUnverifiedSchedulerToken)
	}
	email := oidcClaimString(claims, "email")
	if len(email) == 0 || len(email) > maxOIDCEmailBytes || !SameMember(email, s.account) {
		return fmt.Errorf("%w: the token's account is not the one this route runs as", ErrUnverifiedSchedulerToken)
	}
	now := s.now()
	if exp, ok := oidcClaimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return fmt.Errorf("%w: the token has expired", ErrUnverifiedSchedulerToken)
	}
	if iat, ok := oidcClaimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(signInClockSkew)) {
		return fmt.Errorf("%w: the token is issued in the future", ErrUnverifiedSchedulerToken)
	}
	if nbf, ok := oidcClaimInt(claims, "nbf"); ok && time.Unix(nbf, 0).After(now.Add(signInClockSkew)) {
		return fmt.Errorf("%w: the token is not valid yet", ErrUnverifiedSchedulerToken)
	}
	return nil
}

// decodeOIDCToken decodes a compact JWT's header and payload WITHOUT verifying
// it. Nothing it answers is trusted on its own: Verify holds the signature
// against the published keys before it reads a claim.
//
// It is a second decoder rather than a shared one because the front door's is
// unexported and mcpauth is a package about a different document (this is
// reported on P1.3's pull request as an extraction for the owner, the same
// finding M6 met). What it keeps of that one is the rule that matters: every
// segment is the base64url alphabet and nothing else — a byte a decoder would
// skip is another spelling of the same token — and a segment is exactly one JSON
// object, because a payload that is one claim set and then another is not a
// payload whose claims are the ones that were signed.
func decodeOIDCToken(tok string) (head, claims map[string]any, err error) {
	if tok == "" || len(tok) > maxOIDCTokenBytes {
		return nil, nil, errors.New("keyservice: not a JWT of a usable size")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("keyservice: not a compact JWT")
	}
	for _, seg := range parts {
		if !oidcBase64URLText(seg) {
			return nil, nil, errors.New("keyservice: a JWT segment is not base64url")
		}
	}
	if head, err = decodeOIDCSegment(parts[0], maxOIDCHeaderBytes); err != nil {
		return nil, nil, err
	}
	if claims, err = decodeOIDCSegment(parts[1], maxOIDCClaimsBytes); err != nil {
		return nil, nil, err
	}
	return head, claims, nil
}

// decodeOIDCSegment decodes one JWT segment: base64url, then exactly one JSON
// object of at most max bytes.
func decodeOIDCSegment(s string, max int) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) > max {
		return nil, errors.New("keyservice: a JWT segment is not a base64url object of a usable size")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("keyservice: a JWT segment is not a JSON object")
	}
	// Token, not More: More reports whether the next non-space byte is not a
	// closing bracket, so a segment ending in `}` would read as nothing left.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("keyservice: a JWT segment carries more than one JSON value")
	}
	return out, nil
}

// oidcBase64URLText reports whether s is unpadded base64url text and no other
// byte — not even one a decoder would skip. Go's base64 decoder ignores a CR or
// LF anywhere in its input, so a newline inside a segment decodes to the same
// bytes as the segment without it, and this is what keeps a token one string.
func oidcBase64URLText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// verifyOIDCRS256 checks a compact JWT's RS256 signature against key. The
// decode is Strict, so of the spellings base64url admits for one signature only
// the one that leaves the final character's spare bits zero is read.
func verifyOIDCRS256(tok string, key *rsa.PublicKey) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("keyservice: not a compact JWT")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return errors.New("keyservice: the signature is not base64url")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig)
}

func oidcClaimString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// oidcClaimInt reads a numeric claim as the exact integer the signer wrote, and
// reports false for anything else — including a fractional or out-of-range
// value, which a conversion to int64 would silently narrow into a window nobody
// wrote. Every decoder here sets UseNumber, so a JSON number arrives as a
// json.Number and a float64 case would be a branch that cannot be reached.
func oidcClaimInt(m map[string]any, k string) (int64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}
