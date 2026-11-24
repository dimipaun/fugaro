package credsvc

// The mobile restore routes (K2): the single-use challenge a device that has
// never paired asks for, and the restore that spends it and hands the device a
// session (C1's restore shapes, D2784).
//
// # What a restore is
//
// A phone that already holds a project pairs no QR for a second device: it asks
// the project for a challenge, has the identity project sign an assertion bound
// to it, and posts the assertion back. That is the whole of the difference from
// a pairing (C1's RestoreRequest): no QR, no invite nonce and no admin in the
// loop — the person is already a member, and what is left is to apply the same
// policy again and admit one more device.
//
// # The policy is the pairing's, and the same one
//
// The route asks C1's Policy — the implementation the pairing route asks (F35)
// — so membership, the accepted provider, the authentication age, `mobile.enabled`
// and the recorded `sub` are decided once, in one place, for every route that
// admits a device. What this route adds is its own switch: a project whose
// published policy carries `mobile.strict_new_device` refuses a restore and
// sends the device back to the QR (D2784), because a second factor the person
// holds physically is what that project asked for.
//
// The published policy is also where the values a session answer carries come
// from (`SessionPolicy`: the costly modes and the biometric switch, P6), which
// is why this route reads the node as well as asking the policy. It is read
// AFTER the policy and not before: a caller the policy refuses is refused with
// the policy's own code and never with a project setting it has no business
// learning.
//
// # The order, and why it is this one
//
// The request's shape, then the assertion, then the challenge, then the policy,
// then the strict switch, then the allow path — the device node, the refresh
// family, the custom token, the audit line, the answer.
//
//   - The shape first, and the challenge's MAC with it, because both are
//     decided from the text alone: a body this route will not hold, and a
//     challenge this service did not issue, are refused without reading
//     anything at all — no assertion, no store, no policy.
//   - The assertion second, then the challenge's life and its claim: the
//     challenge is bound into the assertion, so a claim taken first would let a
//     caller burn challenges with assertions that never verified. Both of these
//     are refusals of an identity this service HAS established, which is why
//     they are recorded and a failed assertion is not — and both come after the
//     assertion for the same reason: a phone whose own challenge went stale, or
//     whose challenge was already spent, is told so in the contract's own code
//     and not as a body this service would not read.
//   - The policy before the strict switch, so that a caller who is not a member
//     — or whose provider is not accepted, or whose sign-in is too old — is
//     told that, and not "pair again with a QR" (which would send them to a
//     pairing that refuses them for the same reason).
//   - The strict switch before the allow path, which is the red step this
//     task's plan states: a strict project refuses with no device node written,
//     no family issued and no token minted.
//   - The allow path's two READS before its writes: the mailbox consumer key the
//     phone pins (O12) and the crash-report switch its collector is gated on
//     (D731, D734, D738). The key is the pairing's own store read and a failure
//     of it refuses; the switch is a value and never a refusal, so a policy that
//     cannot answer it fills the answer with it off.
//   - The audit line last and before the answer, because it is the line the
//     session cannot be answered without (D143). What the route did before it —
//     a node, a family, a token — is taken back when it cannot be recorded,
//     which is F32's rule one step early: no device node and no live family
//     survive a restore this service could not answer.
//
// # The challenge is this service's, and it is spent once
//
// The challenge is the restore's one-time value, as the invite's nonce is the
// pairing's (F39), and two things have to hold of it: that this service issued
// it, and that it is used once.
//
// Issued by this service, because the challenge route is the only thing on this
// path that makes a value the project's own. The identity project's signer
// signs an assertion for ANY challenge a verified phone names (A1 refuses only
// a missing one), and C1's ClaimStore cannot answer "was this ever issued" —
// it has one method, and a challenge claimed at issue would be dead before the
// restore that spends it. So the value carries its own proof: the nonce, the
// second it was issued at and the HMAC-SHA-256 over both, under the key this
// deployment holds (ChallengeKey). A restore computes the MAC before anything
// else, refuses a value that is not ours as the shape refusal, and refuses one
// of ours that is past ChallengeLifetime as the contract's nonce_expired —
// which is the ten minutes the challenge route reports, enforced rather than
// advertised.
//
// Spent once, through C1's ClaimStore, so two instances of the public service
// cannot both honour one challenge: the store is what makes single use hold
// across them, and a map in this process would not. The claim is taken at SPEND
// and never at issue, and the MAC is what lets that stay true: a value claimed
// when it was issued would be dead before the restore that spends it, and
// without the MAC the claim would only record that SOMEONE had used the value
// — including a caller who invented it.
//
// # The audit lines
//
// Once the assertion has verified AND the address it names is one this tree can
// write down, every answer — an admission, a refusal and the service's own
// failure — writes exactly one line under PurposeMobileRestore, carrying the
// member's address as the caller and no run, no repository, no job and no
// credential (P0's credentialless shape: a phone is not a job of this
// installation and the act touches no bundle entry).
//
// A refusal taken before that point writes none, which is the rule every route
// of this service keeps for a caller whose identity it could not establish: the
// assertion refusals and the request-shape refusals below write none, and
// ServeHTTP's own refusal of a call that carries no run identity writes none
// either.
//
// The line's status is this service's and never the contract's: the mobile
// codes are not statuses of the audit's closed set (audit.go's ValidStatus
// names the wire's ten error types and no code of this contract), so a refusal
// of the caller is recorded as the request refusal it is and a failure of the
// service as its own unavailable state — which is what the line's reader needs
// to tell one from the other.
//
// # What a refusal is, and the shapes it takes
//
// A refusal of a call this route read is C1's Error: one of the contract's
// codes, which is what the app renders a screen from, and never a sentence
// alone. Two answers carry no code, and each is this service's own WireError
// instead: a body this route could not read, which is the 400 every route of
// this service answers and which C1's closed code set has no member for, and a
// failure of the service's own (a store that could not be read, a token that
// could not be minted), which is no refusal of the caller at all.
//
// An assertion this route will not act on is answered as `auth_too_old`: the
// app's screen for that code is "sign out and back in", which is exactly what a
// phone holding an assertion this project will not accept must do, and the
// route says nothing about WHICH check failed — a code of its own would let a
// caller probe the verifier's and the address rule's decisions. The address
// rule is the second half of it: an assertion whose `email` is not one
// mobileauth.NormalizeEmail can write down is an identity this service cannot
// name in a device node or in an audit line, so it is refused with the same
// sentence and recorded nowhere.
//
// Every answer — the session, the challenge and every refusal — is written
// no-store, because the public service serves one endpoint for a project and a
// shared cache in front of it would answer one phone's call with another's.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The restore routes' own vocabulary: the sizes of the two values they draw,
// and the bound on a request body.
const (
	// mobileChallengeNonceBytes is the random part of a challenge: 256 bits.
	// It is the pairing's nonce length and not a second standard — a challenge
	// is the same kind of value for the same reason (F39), and a guessable one
	// is a challenge an attacker can bind their own assertion to and spend
	// before the phone does.
	mobileChallengeNonceBytes = 32
	// mobileChallengeTimeBytes is the challenge's issue time: the unix second
	// this service stamped it with, big-endian. It is in the value because the
	// ten minutes ExpiresIn advertises have to be a life this route enforces
	// and not a figure it only reports, and the service that spends a
	// challenge is not always the instance that issued it.
	mobileChallengeTimeBytes = 8
	// mobileChallengeMACBytes is the HMAC-SHA-256 the service stamps the two
	// above with, and mobileChallengeKeyBytes is the least key it will stamp
	// them under. The MAC is what makes a challenge provably this service's:
	// the compare-and-set store can say whether a value has been used and
	// cannot say whether one was ever issued (C1's ClaimStore has one method,
	// and claiming at issue would spend the challenge before its restore), so
	// a value a caller invented would otherwise restore a device on the
	// strength of an assertion the identity project signs for ANY challenge —
	// which is the signer's own rule (A1 refuses only a missing one).
	mobileChallengeMACBytes = sha256.Size
	mobileChallengeKeyBytes = 32
	// mobileChallengeLen is the whole value: the nonce, the issue time and the
	// MAC, in that order, which is also the signing input's order.
	mobileChallengeLen = mobileChallengeNonceBytes + mobileChallengeTimeBytes + mobileChallengeMACBytes
	// maxMobileRestoreBytes is the bound on the body either route reads: the
	// three short strings of C1's RestoreRequest and nothing else.
	maxMobileRestoreBytes = 8 << 10
)

// The refusals and failures of these routes, as sentences written here. Each
// one names what was wrong and never what the call sent, what the store holds
// or which node was read: the caller of the challenge route has proved nothing
// and the caller of the restore route learns only what the app's screen needs.
const (
	// mobileAssertionRefused answers an assertion this route will not act on:
	// one the verifier would not accept, and one whose address this tree cannot
	// write down (the file doc's two halves of the same answer).
	mobileAssertionRefused = "the assertion is not one this project can act on: sign in again and retry"
	// mobileChallengeUnissued answers a challenge this service did not issue:
	// a value whose MAC does not hold, which no amount of well-formedness
	// makes ours. It is the shape refusal, because it is decided from the
	// value alone and before any identity is read — such a call is not a call
	// of this contract's at all.
	mobileChallengeUnissued = "the challenge is not one this service issued, so the assertion is bound to a value this project never handed out"
	// mobileChallengeStale answers a challenge of ours that is past the life
	// the challenge route reported (C1's ChallengeLifetime): the contract's own
	// nonce_expired, whose doc gives it as "unknown, already spent, or past its
	// life". The app's screen for it is "ask for a new challenge", which is
	// what a phone holding a stale one must do.
	mobileChallengeStale = "the challenge is past its life: ask for a new challenge and a new assertion"
	// mobileChallengeSpent answers a challenge of ours that was already spent.
	// The MAC says the value is one this service issued; the store says it has
	// been used, and a second restore with it is the replay the single use
	// exists for.
	mobileChallengeSpent = "the challenge was already used: ask for a new challenge and a new assertion"
	// mobileStrictRefused answers a project whose strict switch is on: the
	// device has to pair again with a QR (D2784).
	mobileStrictRefused = "this project requires a QR code for a device it has not seen: pair this device again with fugaro pair"
	// mobileProviderRefused answers a provider outside the project's accepted
	// list, and the answer carries the list (the contract's Error).
	mobileProviderRefused = "this project does not accept the identity provider the assertion names: sign in with one of the accepted providers"
	// mobileAuthTooOldRefused answers a sign-in older than the project accepts.
	mobileAuthTooOldRefused = "the sign-in is older than this project accepts: sign out and back in"
	// mobileNotAMemberRefused answers an address the roster holds no grant for,
	// or a subject that is not the one this project recorded for it.
	mobileNotAMemberRefused = "this address is not a member of this project: ask an administrator for access"
	// mobileDisabledRefused answers a project whose mobile surface is off.
	mobileDisabledRefused = "this project has mobile pairing turned off"
	// mobileDeviceRevokedRefused answers a device or a family that was revoked.
	mobileDeviceRevokedRefused = "this device's session was revoked: pair this device again"
)

// The service's own failures. Every one of them is answered as the wire's
// unavailable state and recorded as such: a store that could not be read, a
// token that could not be minted and a log that could not be written are the
// service's failures and never a refusal of the caller, which is what the
// sentence says.
const (
	mobileEndpointUnusable    = "this service's mobile endpoint is not one an assertion's audience can be derived from, so no assertion can be bound: the installation's endpoint is misconfigured"
	mobileChallengeUnreadable = "the challenge could not be checked against the store, so this service cannot tell a fresh challenge from a spent one"
	mobileChallengeUndrawn    = "this service could not draw a challenge, and a challenge that is not random is one an attacker can predict"
	mobilePolicyUnreadable    = "the project's mobile policy could not be read, so this service cannot say whether this identity is admitted"
	mobilePolicyUnnamed       = "the project's mobile policy answered a refusal this route does not serve, so this service cannot answer it"
	mobilePolicyNoRole        = "the project's mobile policy admitted this identity with no role, and a device node with no role is a device no consumer can resolve"
	mobileProjectPolicyUnread = "the project's published policy could not be read, so this service cannot tell whether this project requires a QR for a new device"
	mobileUIDUndrawn          = "this service could not draw a device id, and a device id that is not random is one another device's name could collide with"
	mobileConsumerKeyUnread   = "the project's mailbox consumer key could not be read, so a phone would have no key to pin and every reply it reads would be one it cannot check (O12)"
	mobileDeviceUnwritable    = "the device could not be recorded, so this service cannot admit it"
	mobileFamilyUnwritable    = "the device's session could not be started, so this service cannot admit it"
	mobileTokenUnminted       = "the device's first token could not be minted, so this service cannot admit it"
	mobileAnswerUnbuildable   = "this service could not build the session it was about to answer with"
	// mobileRestoreUnaudited is this route's own sentence and not the pairing's
	// (mobile_pair.go's mobileUnaudited): each act says which act could not be
	// recorded, and an operator reading one of the two is reading one act.
	mobileRestoreUnaudited = "the restore could not be recorded, and an act this service cannot record is not made: the device was not admitted"
)

// The three shapes this route shares with the pairing route are the pairing
// route's own and are NOT respelled here: MobileProjectConfig, MobileDeviceNode
// and MobileDeviceNodes all live in mobile_pair.go, because the pairing is what
// wrote the device node first (P3) and one node read by the database rules, the
// mailbox's reader and this route has one spelling. This route reads the
// consumer key and writes and retires device nodes THROUGH that store, so a
// restore and a pairing cannot disagree about what a device is.

// MobileRestore serves the two restore routes. Every side it calls is a
// dependency of the deployment and none of them is a field of the request: the
// project is the deployment's own, the policy and the stores are wired at
// construction, and the only caller-chosen values are the assertion, the
// challenge and the device's public key.
type MobileRestore struct {
	// Project is this deployment's own mobile configuration.
	Project MobileProjectConfig
	// ChallengeKey is the key this deployment holds for its challenges: the
	// HMAC key ServeChallenge stamps a challenge with and ServeRestore checks
	// it against. It is read from the installation and never from a caller,
	// and no caller can compute it — which is the whole of what tells a
	// challenge this service issued from one a phone invented.
	//
	// It is required and it is one key for the deployment, not a per-call or
	// per-visitor value: a challenge issued by one instance has to be
	// recognised by the next (F39), and a key drawn per process would refuse
	// every challenge that crossed an instance.
	ChallengeKey []byte
	// Policy reads the published policy: the project's mobile block is where
	// the strict switch and the session's display values live (P6).
	Policy PolicyReader
	// Verifier verifies one assertion against the binding this route requires
	// (C1's Verifier, P1).
	Verifier mobilesession.Verifier
	// Membership applies the project's mobile policy to a verified identity
	// (C1's Policy, P4). It is the same implementation the pairing route asks.
	Membership mobilesession.Policy
	// Claims is the compare-and-set store the challenge is spent in (C1's
	// ClaimStore, F39).
	Claims mobilesession.ClaimStore
	// Refresh starts the new device's family (C1's RefreshStore, P4).
	Refresh mobilesession.RefreshStore
	// Minter mints the device's first custom token (C1's Minter, O8).
	Minter mobilesession.Minter
	// Devices is the device-node store the pairing route also writes through
	// (MobileDeviceNodes, P3): the mailbox consumer's key, the node this restore
	// admits the device by, and the retirement that takes it back when the
	// session cannot be answered (F32).
	Devices MobileDeviceNodes
	// Audit writes this route's lines. It is required: a restore this service
	// cannot record is a restore it must not make (D143).
	Audit *Auditor
	// Log is where the service's own lines go, the cleanup's included. nil is
	// slog.Default().
	Log *slog.Logger
}

// NewMobileRestore validates cfg and returns the routes' handler.
//
// Every side is required, and the reason is the same one NewServer gives for
// the stores it cannot start without: a handler missing one of them is not a
// handler that refuses — it is one that would answer a session it cannot make,
// or admit a device it cannot record. A deployment that has no mobile
// configuration serves no mobile route at all, which is a mounting decision and
// not this constructor's (I1).
func NewMobileRestore(cfg MobileRestore) (*MobileRestore, error) {
	switch {
	case cfg.Project.ProjectID == "":
		return nil, errors.New("credsvc: the mobile routes have no project id, so no session answer could name the project")
	case cfg.Project.Endpoint == "":
		return nil, errors.New("credsvc: the mobile routes have no endpoint, so no assertion's audience could be derived")
	case cfg.Project.DatabaseURL == "":
		return nil, errors.New("credsvc: the mobile routes have no database URL, so a paired phone could not reach its mailbox")
	case cfg.Project.APIKey == "":
		return nil, errors.New("credsvc: the mobile routes have no API key, so a paired phone could not reach its mailbox")
	case len(cfg.ChallengeKey) < mobileChallengeKeyBytes:
		return nil, fmt.Errorf("credsvc: the mobile routes hold no challenge key (or one shorter than %d bytes), so this service could not tell a challenge it issued from one a caller invented", mobileChallengeKeyBytes)
	case cfg.Policy == nil:
		return nil, errors.New("credsvc: the mobile routes cannot read the published policy")
	case cfg.Verifier == nil:
		return nil, errors.New("credsvc: the mobile routes have no assertion verifier")
	case cfg.Membership == nil:
		return nil, errors.New("credsvc: the mobile routes have no membership policy")
	case cfg.Claims == nil:
		return nil, errors.New("credsvc: the mobile routes cannot spend a challenge")
	case cfg.Refresh == nil:
		return nil, errors.New("credsvc: the mobile routes cannot start a refresh family")
	case cfg.Minter == nil:
		return nil, errors.New("credsvc: the mobile routes cannot mint a custom token")
	case cfg.Devices == nil:
		return nil, errors.New("credsvc: the mobile routes cannot write a device node")
	case cfg.Audit == nil:
		return nil, errors.New("credsvc: the mobile routes cannot record an act, and an act this service cannot record is not made")
	}
	if _, ok := mobilesession.AudienceOf(cfg.Project.Endpoint); !ok {
		return nil, fmt.Errorf("credsvc: the mobile routes' endpoint %q is not one an assertion's audience can be derived from: it must be an http or https URL with a host and no userinfo, query or fragment", cfg.Project.Endpoint)
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &MobileRestore{
		Project: cfg.Project, ChallengeKey: cfg.ChallengeKey,
		Policy: cfg.Policy, Verifier: cfg.Verifier,
		Membership: cfg.Membership, Claims: cfg.Claims, Refresh: cfg.Refresh,
		Minter: cfg.Minter, Devices: cfg.Devices, Audit: cfg.Audit,
		Log: cfg.Log,
	}, nil
}

// ServeChallenge answers POST /mobile/v1/restore/challenge: a fresh single-use
// challenge and its life in seconds (C1's RestoreChallengeResponse).
//
// It verifies nothing and records nothing, because there is nothing to verify
// and nothing to record: the request carries no identity at all (C1's
// RestoreChallengeRequest is an empty object), and a challenge is a value the
// assertion is bound to and not an admission. What makes it single-use is the
// claim the restore takes on it — and what makes it THIS service's is the MAC
// and the issue time it draws into the value, which is what the restore checks
// before it claims anything.
//
// The body must still be the empty object the contract declares, and a body
// naming a field is refused: a route whose request shape is stated by the
// contract is held to it, and a field this release ignored would be a field the
// next one reads as something else.
func (m *MobileRestore) ServeChallenge(w http.ResponseWriter, r *http.Request) {
	if err := readMobileChallengeRequest(r); err != nil {
		WriteWireError(w, StatusRequestRefused, WireError{Error: ErrorRequestRefused, Message: err.Error()})
		return
	}
	nonce := make([]byte, mobileChallengeNonceBytes)
	if _, err := io.ReadFull(mobileRandom, nonce); err != nil {
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: mobileChallengeUndrawn})
		return
	}
	body, err := json.Marshal(mobilesession.RestoreChallengeResponse{
		Challenge: mobileChallengeValue(m.ChallengeKey, nonce, time.Now()),
		ExpiresIn: int(mobilesession.ChallengeLifetime / time.Second),
	})
	if err != nil {
		// Unreachable: the shape is two strings' worth of value and a number.
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: mobileAnswerUnbuildable})
		return
	}
	writeReadAnswer(w, body)
}

// ServeRestore answers POST /mobile/v1/restore: the session a new device holds
// from then on (C1's SessionResponse). The order of what it does is the file
// doc's, and every branch below is a step of it.
func (m *MobileRestore) ServeRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, challengeAt, err := readMobileRestoreRequest(r, m.ChallengeKey)
	if err != nil {
		// No line: the call is refused before any identity was read, which is
		// the one refusal on this route with nothing to name. A challenge this
		// service did not issue is refused HERE and not after the assertion,
		// because the value alone decides it: no identity has to be read to
		// know that a MAC which does not hold is not one of ours, and a call
		// whose challenge this project never handed out is not a call of this
		// contract's at all.
		WriteWireError(w, StatusRequestRefused, WireError{Error: ErrorRequestRefused, Message: err.Error()})
		return
	}
	audience, ok := mobilesession.AudienceOf(m.Project.Endpoint)
	if !ok {
		// The constructor refuses an endpoint this could happen to, so this is
		// a handler built by hand. It is the service's own failure and no
		// caller's, and no identity has been read.
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: mobileEndpointUnusable})
		return
	}
	claims, err := m.Verifier.Verify(ctx, req.Assertion, mobilesession.Binding{Audience: audience, Challenge: req.Challenge})
	if err != nil {
		// No line: the assertion did not verify, so this service has no
		// identity to record.
		m.refuseAssertion(w)
		return
	}
	// The address the assertion names has to be one this tree can write down,
	// and it is asked for HERE because everything below names it: the device
	// node is keyed by it in the roster's own spelling and the audit line
	// carries it as the caller. An address NormalizeEmail refuses is an
	// identity this service cannot name, so it is the assertion refusal and
	// not a refusal of a member.
	email, err := mobileauth.NormalizeEmail(claims.Email)
	if err != nil {
		m.refuseAssertion(w)
		return
	}
	if time.Since(challengeAt) > mobilesession.ChallengeLifetime {
		// The value is ours — the reader checked the MAC — and it is past the
		// life the challenge route reported. It is refused BEFORE the claim,
		// because an expired challenge is not spent: a phone that comes back
		// with a fresh assertion for its own stale challenge has to ask for a
		// new one, and burning the value's store entry would answer the same
		// either way.
		m.refuse(ctx, w, email, mobilesession.Error{Code: mobilesession.CodeNonceExpired, Message: mobileChallengeStale})
		return
	}
	spent, err := m.Claims.Claim(ctx, mobilesession.NamespaceChallenge, req.Challenge, mobilesession.ChallengeLifetime)
	if err != nil {
		m.unavailable(ctx, w, email, mobileChallengeUnreadable)
		return
	}
	if !spent {
		m.refuse(ctx, w, email, mobilesession.Error{Code: mobilesession.CodeNonceExpired, Message: mobileChallengeSpent})
		return
	}
	verdict, err := m.Membership.Allow(ctx, m.Project.ProjectID, claims)
	if err != nil {
		m.unavailable(ctx, w, email, mobilePolicyUnreadable)
		return
	}
	if !verdict.Allowed {
		e, ok := mobilePolicyRefusal(verdict)
		if !ok {
			m.unavailable(ctx, w, email, mobilePolicyUnnamed)
			return
		}
		m.refuse(ctx, w, email, e)
		return
	}
	if verdict.Role == access.RoleNone {
		// The contract says an allowed verdict carries the roster's role and
		// that RoleNone on one is a policy bug. It is refused here rather than
		// written: a node with no role is a device the consumer cannot resolve
		// an action for, and the node's existence alone is what the database
		// rules read.
		m.unavailable(ctx, w, email, mobilePolicyNoRole)
		return
	}
	node, found, err := m.Policy.Shared(ctx)
	if err != nil || !found {
		m.unavailable(ctx, w, email, mobileProjectPolicyUnread)
		return
	}
	if node.MobileStrictNewDevice() {
		// K2's red step: the strict switch refuses BEFORE the allow path, so
		// nothing of it happens — no node, no family, no token.
		m.refuse(ctx, w, email, mobilesession.Error{Code: mobilesession.CodeStrictNewDeviceNeedsQR, Message: mobileStrictRefused})
		return
	}

	// The allow path, and its first step is a READ: a project that has published
	// no consumer key cannot complete this restore, so it is refused before a
	// device node exists that a later failure would have to take back.
	consumerKey, err := m.Devices.ConsumerKey(ctx)
	if err != nil {
		m.unavailable(ctx, w, email, mobileConsumerKeyUnread)
		return
	}
	// The other published value the answer carries is the crash-report switch
	// (C1 writes the member whether or not the project's policy states it, D738),
	// and it is a VALUE and never a refusal: a policy that cannot answer it is a
	// phone told nothing, which collects nothing (D734), so the answer goes out
	// with it off rather than the session being refused over a screen's switch.
	crashReports, err := m.Membership.CrashReports(ctx, m.Project.ProjectID)
	if err != nil {
		m.Log.Error("credsvc: the crash-report switch could not be read, so the session names it off", "error", err)
	}
	// Everything below this line has to be taken back when a later step fails
	// (undo), because a session this service did not answer must leave no device
	// node and no live family behind (F32).
	uid, err := mobileDeviceUID()
	if err != nil {
		m.unavailable(ctx, w, email, mobileUIDUndrawn)
		return
	}
	// The node is the pairing route's own shape and carries what it carries: the
	// address the roster knows, the roster's role (O14), the device's public key
	// (O6) and the moment. The provider and the subject are NOT here — they are
	// the member record's, which the pairing writes and the policy reads back
	// (D2791 as amended), so this route neither writes them nor keeps a second
	// copy of them.
	device := MobileDeviceNode{
		Email:     email,
		Role:      string(verdict.Role),
		PublicKey: req.DevicePublicKey,
		At:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := m.Devices.Write(ctx, uid, device); err != nil {
		m.unavailable(ctx, w, email, mobileDeviceUnwritable)
		return
	}
	grant, err := m.Refresh.Issue(ctx, m.Project.ProjectID, uid)
	if err != nil {
		m.undo(ctx, uid, "")
		m.unavailable(ctx, w, email, mobileFamilyUnwritable)
		return
	}
	token, err := m.Minter.Mint(ctx, mobilesession.MintRequest{UID: uid, Role: verdict.Role})
	if err != nil {
		m.undo(ctx, uid, grant.Family)
		m.unavailable(ctx, w, email, mobileTokenUnminted)
		return
	}
	body, err := json.Marshal(m.session(node, verdict.Role, consumerKey, crashReports, grant, token))
	if err != nil {
		// Unreachable for the same reason the challenge answer's marshal is:
		// every field is a string, a role or a number.
		m.undo(ctx, uid, grant.Family)
		m.unavailable(ctx, w, email, mobileAnswerUnbuildable)
		return
	}
	if err := m.line(ctx, email, StatusOK); err != nil {
		// The act was made and cannot be recorded, so it is taken back: a
		// session no line names is a session the trail cannot account for
		// (D143). The caller gets the service's own failure, which is what
		// happened.
		m.Log.Error("credsvc: a mobile restore could not be audited, so it was not answered", "error", err)
		m.undo(ctx, uid, grant.Family)
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: mobileRestoreUnaudited})
		return
	}
	writeReadAnswer(w, body)
}

// session builds the answer a restored device holds (C1's SessionResponse).
//
// The endpoint is the deployment's own value and not the canonical audience the
// assertion was bound to: the two are the same URL, the phone stores this one
// and calls it for every later refresh and restore, and re-spelling the
// deployment's own endpoint here would be a second statement of it. The role is
// the roster's, from the verdict the policy allowed with (O14), the consumer key
// is the published node's and the policy values are the published node's —
// mobileSessionPolicy is the pairing route's own function, so the two routes
// cannot answer a phone two different pictures of one project.
//
// The custom token's life is the CONTRACT's (C1's CustomTokenLifetime) and not
// the minter's own answer, so a minter that returned a longer-lived token would
// still be advertised at the life the phone and the contract agree on.
func (m *MobileRestore) session(node *projectpolicy.Policy, role access.Role, consumerKey string, crashReports bool, grant mobilesession.RefreshGrant, token mobilesession.CustomToken) mobilesession.SessionResponse {
	// The crash-report switch is the policy's own answer and not a field of the
	// published node, so it is set on the assembled value rather than read out of
	// the node the shared assembler takes.
	policy := mobileSessionPolicy(node)
	policy.CrashReports = crashReports
	return mobilesession.SessionResponse{
		ProjectID:         m.Project.ProjectID,
		Endpoint:          m.Project.Endpoint,
		DatabaseURL:       m.Project.DatabaseURL,
		APIKey:            m.Project.APIKey,
		Role:              role,
		Policy:            policy,
		ConsumerPublicKey: consumerKey,
		RefreshToken:      grant.Token,
		CustomToken:       token.Token,
		ExpiresIn:         int(mobilesession.CustomTokenLifetime / time.Second),
	}
}

// refuseAssertion answers an assertion this route will not act on, with the
// contract's auth_too_old and no audit line: the service has no identity it can
// name, which is the file doc's rule for a refusal taken before that point.
func (m *MobileRestore) refuseAssertion(w http.ResponseWriter) {
	writeMobileError(w, http.StatusForbidden, mobilesession.Error{Code: mobilesession.CodeAuthTooOld, Message: mobileAssertionRefused})
}

// refuse answers a refusal of the caller: the contract's Error, and one line
// naming the member and the purpose (P0's one-line-per-refusal rule). The line
// is written whether or not it lands, and a line that does not land is the
// service's own problem to log — the caller is refused either way.
func (m *MobileRestore) refuse(ctx context.Context, w http.ResponseWriter, caller string, e mobilesession.Error) {
	if err := m.line(ctx, caller, ErrorRequestRefused); err != nil {
		m.Log.Error("credsvc: a refused mobile restore could not be audited", "error", err)
	}
	writeMobileError(w, http.StatusForbidden, e)
}

// unavailable answers a failure of the service: the wire's unavailable state,
// and one line where the member's address is known. It is not a refusal of the
// caller and its sentence says so.
func (m *MobileRestore) unavailable(ctx context.Context, w http.ResponseWriter, caller, message string) {
	if caller != "" {
		if err := m.line(ctx, caller, ErrorStateUnavailable); err != nil {
			m.Log.Error("credsvc: a failed mobile restore could not be audited", "error", err)
		}
	}
	WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: message})
}

// line writes one line of this route's: the member's address as the caller, the
// purpose, the status and nothing else — no run, no repository, no job and no
// credential, which the auditor refuses on a mobile purpose anyway
// (credentiallessPurpose) and which a route that filled its event from the
// wrong identity would have written.
func (m *MobileRestore) line(ctx context.Context, caller, status string) error {
	return m.Audit.Write(ctx, AuditEvent{Caller: caller, Purpose: PurposeMobileRestore, Status: status})
}

// undo takes back what a restore made before it could be answered: the family
// it issued and the device node it wrote. Both are best-effort and both are
// logged when they fail, because the caller is already being refused and there
// is no second answer to give it — an operator reads the line and deletes what
// is left.
func (m *MobileRestore) undo(ctx context.Context, uid, family string) {
	if family != "" {
		if err := m.Refresh.Revoke(ctx, m.Project.ProjectID, family); err != nil {
			m.Log.Error("credsvc: a failed restore's refresh family could not be revoked", "family", family, "error", err)
		}
	}
	if err := m.Devices.Retire(ctx, uid); err != nil {
		m.Log.Error("credsvc: a failed restore's device node could not be removed", "device", uid, "error", err)
	}
}

// mobilePolicyRefusal renders a refused verdict as the contract's Error, and
// reports whether it is a refusal this route renders at all.
//
// The set is closed for the app's sake — it renders one sentence per code, and
// a code it does not know is a refusal shown as a crash — so a code outside
// Codes() is the service's own failure. Two codes inside it are refused here as
// well, and each for its own reason: `nonce_expired` is the pair route's own
// answer about an invite the policy never sees, and `strict_new_device_needs_qr`
// is this route's, decided from the published switch after the policy has
// answered — a policy that answered either would be a policy deciding for a
// route it is not, and this route must know that IT made the strict decision,
// because the answer decides whether a device node is written.
//
// The accepted-provider list is carried only with provider_not_accepted, which
// is the one code the contract carries it for.
func mobilePolicyRefusal(v mobilesession.Verdict) (mobilesession.Error, bool) {
	if !slices.Contains(mobilesession.Codes(), v.Code) {
		return mobilesession.Error{}, false
	}
	var message string
	switch v.Code {
	case mobilesession.CodeProviderNotAccepted:
		message = mobileProviderRefused
	case mobilesession.CodeAuthTooOld:
		message = mobileAuthTooOldRefused
	case mobilesession.CodeNotAMember:
		message = mobileNotAMemberRefused
	case mobilesession.CodeMobileDisabled:
		message = mobileDisabledRefused
	case mobilesession.CodeDeviceRevoked:
		message = mobileDeviceRevokedRefused
	default:
		return mobilesession.Error{}, false
	}
	e := mobilesession.Error{Code: v.Code, Message: message}
	if v.Code == mobilesession.CodeProviderNotAccepted {
		e.AcceptedProviders = v.Accepted
	}
	return e, true
}

// readMobileRestoreRequest reads a restore call's body, and answers the second
// its challenge was issued at beside it. It is bounded and decoded strictly,
// and no sentence it returns repeats what it read: the body is the caller's
// text, and a decoder's own message names the byte it choked on
// (readWorkflowSecretsRequest's rule, the reader this one is shaped after).
//
// The challenge is held to this service's own spelling AND to the MAC under the
// deployment's key, which is what tells a challenge this service issued from
// one a caller invented. Both are decided from the value alone, so both are
// refusals taken before any identity is read — and the issue time comes back
// with the request because the life it bounds is checked where the caller's
// identity has been established, so that a stale challenge is the contract's
// nonce_expired and not a body this service would not read.
func readMobileRestoreRequest(r *http.Request, challengeKey []byte) (mobilesession.RestoreRequest, time.Time, error) {
	body, err := readMobileBody(r)
	if err != nil {
		return mobilesession.RestoreRequest{}, time.Time{}, err
	}
	var req mobilesession.RestoreRequest
	if err := decodeStrict(body, &req); err != nil {
		return mobilesession.RestoreRequest{}, time.Time{}, fmt.Errorf("%w: the body is not one JSON object of the three fields this route reads", errMobileRestoreBadRequest)
	}
	if req.Assertion == "" {
		return mobilesession.RestoreRequest{}, time.Time{}, fmt.Errorf("%w: the call carries no assertion, and an assertion is the whole of what admits a device", errMobileRestoreBadRequest)
	}
	if req.Challenge == "" {
		return mobilesession.RestoreRequest{}, time.Time{}, fmt.Errorf("%w: the call carries no challenge, and an assertion bound to no challenge is one this service cannot tell from a replay", errMobileRestoreBadRequest)
	}
	if err := validMobileChallenge(req.Challenge); err != nil {
		return mobilesession.RestoreRequest{}, time.Time{}, err
	}
	issuedAt, ok := mobileChallengeIssuedAt(challengeKey, req.Challenge)
	if !ok {
		return mobilesession.RestoreRequest{}, time.Time{}, fmt.Errorf("%w: %s", errMobileRestoreBadRequest, mobileChallengeUnissued)
	}
	if err := mobileDevicePublicKey(req.DevicePublicKey); err != nil {
		// The key is held to the wire's own rule by the pairing route's checker
		// (mobileDevicePublicKey): 32 bytes of Ed25519 key in unpadded base64url,
		// which is the one spelling this contract has for a key. Its own sentence
		// is NOT repeated to the caller — it names the byte the decoder choked on
		// — because the body is the caller's text (readWorkflowSecretsRequest's
		// rule, which every reader of this service keeps).
		return mobilesession.RestoreRequest{}, time.Time{}, fmt.Errorf("%w: the device's public key is not %d bytes of unpadded base64url, which is the one spelling this contract has for a key", errMobileRestoreBadRequest, mobilesession.DevicePublicKeySize)
	}
	return req, issuedAt, nil
}

// readMobileChallengeRequest reads the challenge call's body, which the
// contract declares as an empty object: a field this release ignored would be
// one the next release reads as something else, so a body naming any field is
// refused rather than served.
func readMobileChallengeRequest(r *http.Request) error {
	body, err := readMobileBody(r)
	if err != nil {
		return err
	}
	var req mobilesession.RestoreChallengeRequest
	if err := decodeStrict(body, &req); err != nil {
		return fmt.Errorf("%w: this route's body is an empty JSON object and names no field", errMobileRestoreBadRequest)
	}
	return nil
}

// readMobileBody reads one mobile call's body, bounded: the route reads three
// short strings, so a body larger than maxMobileRestoreBytes is not a call of
// this contract's and is refused before it is parsed.
func readMobileBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMobileRestoreBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the body could not be read", errMobileRestoreBadRequest)
	}
	if len(body) > maxMobileRestoreBytes {
		return nil, fmt.Errorf("%w: the body is larger than the %d bytes this service accepts", errMobileRestoreBadRequest, maxMobileRestoreBytes)
	}
	return body, nil
}

// errMobileRestoreBadRequest is a call whose body is not this contract's. It is
// the sentinel the readers wrap, so a test can tell this refusal from another
// without reading a sentence.
var errMobileRestoreBadRequest = errors.New("credsvc: the mobile call is not one this service reads")

// validMobileChallenge holds the challenge a restore spends to the shape the
// challenge route issues: mobileChallengeLen bytes — the nonce, the issue time
// and the MAC — in unpadded base64url, decoded strictly. It is the same rule
// the device key above gets and for a sharper reason: the value is used
// VERBATIM as a key of the shared compare-and-set store, before any policy has
// been asked, so text this route did not draw is text it would write into a
// store every instance reads — a path-shaped string, a database-key-illegal
// character or a value long enough to be worth writing.
//
// The spelling is only the first half of what makes a challenge ours, and it is
// kept apart from the MAC below for one reason: a value that is not this
// service's spelling at all is a body this route would not hold, and the two
// refusals read differently.
func validMobileChallenge(challenge string) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	if err != nil || len(raw) != mobileChallengeLen {
		return fmt.Errorf("%w: the challenge is not the %d bytes of unpadded base64url this service issues, which is the one spelling a challenge has", errMobileRestoreBadRequest, mobileChallengeLen)
	}
	return nil
}

// mobileChallengeValue builds the challenge the route issues: the nonce it drew,
// the second it was issued at and the MAC over both, in unpadded base64url.
// There is no identifier of the challenge other than the value itself, because
// nothing has to be looked up to recognise it: the key does that.
func mobileChallengeValue(key, nonce []byte, issuedAt time.Time) string {
	raw := make([]byte, 0, mobileChallengeLen)
	raw = append(raw, nonce...)
	raw = binary.BigEndian.AppendUint64(raw, uint64(issuedAt.Unix()))
	raw = append(raw, mobileChallengeMAC(key, raw)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// mobileChallengeIssuedAt reports the second this service issued the challenge
// at, and false when the value is not one it issued. The MAC decides it, so a
// value a caller invented — however well-formed — is refused here, before the
// store is asked, before any policy is asked and before the assertion is even
// read: the identity project signs an assertion for any challenge a verified
// phone names (A1 refuses only a missing one), so the challenge is the only
// thing on this path that says the value came from this project.
//
// The comparison is hmac.Equal's and never ==, so where a forgery differs from
// a real challenge is not readable from how long the check took.
func mobileChallengeIssuedAt(key []byte, challenge string) (time.Time, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	if err != nil || len(raw) != mobileChallengeLen {
		return time.Time{}, false
	}
	signed, want := raw[:mobileChallengeNonceBytes+mobileChallengeTimeBytes], raw[mobileChallengeNonceBytes+mobileChallengeTimeBytes:]
	if !hmac.Equal(want, mobileChallengeMAC(key, signed)) {
		return time.Time{}, false
	}
	seconds := binary.BigEndian.Uint64(raw[mobileChallengeNonceBytes : mobileChallengeNonceBytes+mobileChallengeTimeBytes])
	return time.Unix(int64(seconds), 0), true
}

// mobileChallengeMAC is the HMAC-SHA-256 over the nonce and the issue time,
// under the deployment's key. It is the whole of what makes a challenge
// unforgeable, so the key never reaches a response, a log line or an error
// (the rule this file keeps for every value of the contract's).
func mobileChallengeMAC(key, signed []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(signed)
	return mac.Sum(nil)
}

// mobileRandom is the source of the challenge's nonce. It is a variable so that
// a test can make the draw fail, which is the one failure this route may never
// fall back from: a challenge that is not random is one an attacker binds their
// own assertion to and spends before the phone does. A device's uid is drawn by
// the pairing route's own helper (mobileDeviceUID), which reads crypto/rand
// itself.
var mobileRandom io.Reader = rand.Reader

// writeMobileError writes one refusal in the contract's shape: the code the app
// renders a screen from, the accepted-provider list when the code carries one,
// and a sentence. It is no-store like every answer of these routes, and the
// body is bounded by construction — every field is a code, a list of provider
// names and one of this file's own sentences.
func writeMobileError(w http.ResponseWriter, status int, e mobilesession.Error) {
	body, err := json.Marshal(e)
	if err != nil {
		// Unreachable: the shape is a code, a short list of strings and a
		// sentence.
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: mobileAnswerUnbuildable})
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
