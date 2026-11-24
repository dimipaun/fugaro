// Package mobilesession is the contract the mobile app's identity, pairing,
// session and restore work is built against (plan task C1 of
// docs/mobile-identity-pairing). It declares the shapes the sides of that work
// call while they are written in parallel, and nothing here decides anything:
// the stubs in stub.go return a shape and decide nothing, and I1 wires the real
// sides and deletes that file.
//
// The sides, and what each one owes or reads:
//
//   - the signer (A1, ee/identitysvc) verifies a Firebase ID token from the
//     identity project and signs an assertion for one audience and one
//     challenge. It produces AssertionResponse and the Claims inside it.
//   - the verifier (P1, internal/assertion) verifies that assertion against
//     the identity project's published keys and the envelope below. It
//     implements Verifier and consumes Claims.
//   - the invitation (P2, internal/cli's `fugaro pair`) writes the invite the
//     pairing reads, and the QR carries the endpoint, the email, the nonce and
//     the consumer key's fingerprint and no credential (F4, O16). It produces
//     Invite.
//   - the project side (P3 pairing, P4 refresh, K2 restore, P7 revoke,
//     internal/credsvc and internal/cli) serves the mobile routes on the
//     public service of F31/O8. It consumes AssertionRequest, PairRequest,
//     RefreshRequest, RestoreChallengeRequest, RestoreRequest and
//     RevokeSelfRequest, implements Policy, InviteStore, MemberStore,
//     RefreshStore, Minter and ClaimStore, and produces SessionResponse,
//     RefreshResponse and Error.
//   - the identity project (K1, ee/identitysvc) stores the synced paired list.
//     It consumes PairedEntry.
//   - the app (M1 identities, M2 pairing, M3 session, K3 continuity) signs in
//     by REST (D2793) and calls those routes. It consumes every response here,
//     the secure-store item names and the audience derivation below.
//
// The prose half is docs/mobile-identity-pairing/contract.md: what each side
// may rely on, what none of them may, and what amending this contract costs.
// The data half is shared/mobile-session/vectors.json, which this package's
// test replays and the app's tests read, so the two languages cannot drift
// apart on a claim name, a route, a code or the envelope's exact bytes.
//
// Three rules hold for everything here:
//
//   - No shape carries a credential. There is no provider session, no Firebase
//     refresh token and no key material in any field: RefreshToken and
//     CustomToken name tokens the project side hands to the one device that
//     proved possession of its key, Assertion is the signed claim set and
//     never a credential of Fugaro's, and the device's private key never
//     appears in a request or a response at all.
//   - The zero value of every shape is the fail-closed one, because the sides
//     are written in parallel and a shape nobody has decided must never read
//     as one. A SessionResponse with no role holds nothing, a Verdict that was
//     never allowed carries no role, and an Error with no code is no code.
//   - Every refusal is one of Codes(). The app maps a code to a screen and
//     refuses anything else outright, so a side that invents a code has
//     invented a screen nobody wrote.
package mobilesession

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
)

// AssertionLifetime is how long a signed assertion is valid (O4: 2 minutes).
// It is the figure the signer stamps into exp and the verifier bounds with
// skew, and it is short because the assertion is spent immediately: the app
// asks for it and posts it in the same breath.
const AssertionLifetime = 2 * time.Minute

// InviteLifetime is how long a pairing QR stays usable (O4: 10 minutes), and
// ChallengeLifetime is the same figure for the challenge a restore is bound
// to: both are one-time values a person's screen or a phone's request carries,
// and neither may be replayed after it.
const (
	InviteLifetime    = 10 * time.Minute
	ChallengeLifetime = 10 * time.Minute
)

// IdleLifetime is how long a refresh family survives without use (D2783: about
// 30 days), and CustomTokenLifetime is the access token's life (D2783: about
// one hour). The first is the project side's own store's bound and the second
// is what ExpiresIn reports, so the app refreshes on the figure the service
// uses rather than on one of its own.
const (
	IdleLifetime        = 30 * 24 * time.Hour
	CustomTokenLifetime = time.Hour
)

// DevicePublicKeySize and DeviceSignatureSize are the sizes of an Ed25519
// public key and signature, and the wire spelling of both is unpadded base64url
// (RFC 4648 §5), the one spelling this tree already uses for a key
// (internal/budget/token's deviceKeyEncoding). A caller decodes strictly, so a
// padded or standard-alphabet spelling is refused rather than leniently read
// as the key it would decode to.
const (
	DevicePublicKeySize = 32
	DeviceSignatureSize = 64
)

// SignaturePurpose names what a device key signed, and SignatureLabel prefixes
// every message a device signs, so a signature over a refresh token can never
// be replayed as a signature over something else the same key signs (a mailbox
// message, O6).
type SignaturePurpose string

const (
	// PurposeRefresh is the proof of possession a refresh presents: the device
	// key signs the refresh token it is rotating (P4's route, D2783).
	PurposeRefresh SignaturePurpose = "refresh"
	// PurposeRevokeSelf is the same proof for the sign-out-everywhere call
	// that ends the caller's own family (D2794).
	PurposeRevokeSelf SignaturePurpose = "revoke-self"
	// SignatureLabel is the prefix of the signed bytes, and it is a label
	// rather than a key: what it separates is one use of a device key from
	// another, not one device from another.
	SignatureLabel = "fugaro-mobile-"
	// SignatureSeparator ends the purpose in the signed bytes.
	SignatureSeparator = ":"
)

// SignatureMessage returns the bytes a device key signs for a purpose and a
// value: SignatureLabel, the purpose, SignatureSeparator and the value, ASCII.
//
// It lives in the contract rather than in either end because the two ends must
// build the identical bytes and there is no second chance to agree: a service
// that spelled the message its own way would refuse every honest refresh, and
// the failure would look like a bad device key rather than a bad string. An
// unknown purpose is refused (nil) rather than signed, so a caller cannot
// invent a fourth message with the device's key.
func SignatureMessage(purpose SignaturePurpose, value string) []byte {
	if value == "" {
		return nil
	}
	switch purpose {
	case PurposeRefresh, PurposeRevokeSelf:
	default:
		return nil
	}
	return []byte(SignatureLabel + string(purpose) + SignatureSeparator + value)
}

// AudienceOf returns the audience an assertion for this endpoint must carry:
// the endpoint's scheme and host lower-cased, its path kept, and any trailing
// slash removed. It refuses a string that is not an http or https URL, one
// carrying a userinfo, a query or a fragment, and one with no host.
//
// It exists because of F39: the QR carries the endpoint and nothing else that
// could name a project (F4), so the audience has to be *derived* from the
// endpoint rather than sent beside it. The app asks the identity project for an
// assertion with aud = AudienceOf(the QR's endpoint), and the project side
// derives the same string from the endpoint its caller sent, compares it with
// the assertion's aud, and requires the endpoint to be its own — which is what
// refuses a relayed QR. Two ends that spelled this rule for themselves would
// agree until one of them trimmed a slash.
func AudienceOf(endpoint string) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if u.Host == "" || u.User != nil {
		return "", false
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	path := strings.TrimSuffix(u.Path, "/")
	return scheme + "://" + strings.ToLower(u.Host) + path, true
}

// Claims is an assertion's claim set (D2792): what the identity project
// verified about a person, bound to one audience and one challenge. The JSON
// names are the wire names, and their order is the order the signer writes
// them, because the envelope's signature covers the serialised bytes: a claim
// renamed or reordered on one side is a signature the other side refuses.
//
// Every field is a fact the identity project read off the provider's own token
// and checked. EmailVerified is that check and not the app's claim — a password
// sign-in whose address was never verified is refused (D2785) — and Subject is
// the provider's stable id for the person, which is what a member record keeps
// so that a reassigned address cannot inherit an old pairing (F9, F34, D2791).
type Claims struct {
	// Issuer is the identity project's issuer (iss). The verifier pins it
	// (O7): a pin the caller can pass in the assertion is no pin at all.
	Issuer string `json:"iss"`
	// Audience is the project this assertion is for (aud): AudienceOf of the
	// endpoint, never a value the app chose beside it.
	Audience string `json:"aud"`
	// Subject is the provider's stable id for the signed-in identity (sub).
	Subject string `json:"sub"`
	// Email is the address the provider vouched for, and it is what the
	// invite, the roster and the member record are keyed by.
	Email string `json:"email"`
	// EmailVerified is the provider's own verdict on that address.
	EmailVerified bool `json:"email_verified"`
	// Provider is the identity provider that signed the person in (google,
	// microsoft, apple or password), spelled the way the accepted-provider
	// policy spells it.
	Provider string `json:"provider"`
	// AuthTime is when the person last signed in, in seconds since the epoch,
	// and it is what the project's maximum authentication age is measured
	// against (O4: 24 hours, per project).
	AuthTime int64 `json:"auth_time"`
	// JTI is the assertion's own id: the verifier claims it once through the
	// ClaimStore, so a replay of the same assertion is refused even inside its
	// two minutes (P1).
	JTI string `json:"jti"`
	// IssuedAt and ExpiresAt bound the assertion, in seconds since the epoch,
	// and the verifier allows a bounded skew on both. The lifetime is
	// AssertionLifetime.
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
	// Challenge is what this assertion is bound to: the invite's nonce at
	// pairing, the challenge the project issued at restore (F39). It is the
	// claim that keeps an assertion from being replayed against another
	// project's pairing even while it is valid.
	Challenge string `json:"challenge"`
}

// AssertionRequest is what the app asks the identity project's signer for (A1):
// one audience and one challenge, and nothing else. The identity comes from the
// ID token the app authenticates with, so no field here names a person: a
// request that could name one would let a caller ask for an assertion about
// somebody else.
type AssertionRequest struct {
	// Audience is AudienceOf of the endpoint the app is pairing or restoring
	// with.
	Audience string `json:"aud"`
	// Challenge is the invite's nonce or the challenge the project issued.
	Challenge string `json:"challenge"`
}

// AssertionResponse carries the signed assertion: the compact EdDSA JWT whose
// claims are Claims, signed with the identity project's KMS key (D2792). It is
// a signed claim set and not a credential of Fugaro's, and it is spent
// immediately: the app posts it to the project in the same flow.
type AssertionResponse struct {
	Assertion string `json:"assertion"`
}

// PairRequest is what the app posts to a project's pairing route: the assertion
// for that project, the endpoint the QR carried, the invite's nonce and the
// device's own public key (O6).
//
// The endpoint is here so the project can refuse a relayed QR: the service
// requires AudienceOf(Endpoint) to be AudienceOf(its own endpoint) *and* to be
// the assertion's aud, so a QR that points a phone at another project's service
// is refused rather than honoured (F39). The nonce is here because it is what
// the invite is found by, and the service requires it to equal the assertion's
// challenge as well.
type PairRequest struct {
	// Assertion is the compact JWT the identity project signed.
	Assertion string `json:"assertion"`
	// Endpoint is the QR's endpoint, verbatim.
	Endpoint string `json:"endpoint"`
	// Nonce is the QR's one-time nonce.
	Nonce string `json:"nonce"`
	// DevicePublicKey is the device's Ed25519 public key, unpadded base64url.
	// It is stored on the device node, where it is what the mailbox gate
	// verifies a signature against and what proves possession at every
	// refresh; deleting the node ends both at once (O6).
	DevicePublicKey string `json:"device_public_key"`
	// PriorDeviceIDToken is the Firebase ID token of the device that already
	// holds this project, when the phone is re-pairing one it was paired to
	// before, and it is omitted when it is not. The service verifies it and
	// deletes that old device node, which is how re-pairing retires an old
	// pairing without a command of the person's own (round 5).
	PriorDeviceIDToken string `json:"prior_device_id_token,omitempty"`
}

// RestoreRequest is what the app posts to a project's restore route (K2): an
// assertion bound to the challenge the project issued, that challenge, and the
// new device's public key. It carries no QR and no nonce, which is the whole
// difference between a restore and a pairing: the project already knows the
// member, so what is left is to apply the same policy again and hand the new
// device a session (D2784).
type RestoreRequest struct {
	Assertion       string `json:"assertion"`
	Challenge       string `json:"challenge"`
	DevicePublicKey string `json:"device_public_key"`
}

// RestoreChallengeRequest is what the app posts to a project's challenge route,
// and it carries nothing: the project issues a random single-use challenge and
// remembers it, so there is no request text for it to trust or to store. What
// binds the challenge to a person is the assertion that later carries it.
type RestoreChallengeRequest struct{}

// RestoreChallengeResponse carries the challenge and its life in seconds
// (ChallengeLifetime).
type RestoreChallengeResponse struct {
	Challenge string `json:"challenge"`
	ExpiresIn int    `json:"expires_in"`
}

// RefreshRequest is what the app posts to a project's refresh route (P4): the
// refresh token and the device key's signature over it. The signature is the
// proof of possession that makes a stolen token useless without the device's
// private key, and what it covers is SignatureMessage(PurposeRefresh, the
// token).
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	Signature    string `json:"signature"`
}

// RevokeSelfRequest is what the app posts to a project's revoke-self route
// (P4, D2794): the refresh token and the device key's signature over it, as the
// refresh carries. It ends the caller's own family, so it needs the same proof
// that the caller is the device and not somebody holding a copied token.
type RevokeSelfRequest struct {
	RefreshToken string `json:"refresh_token"`
	Signature    string `json:"signature"`
}

// RevokeSelfResponse carries nothing: the family is gone and there is no state
// left to report. It is declared rather than left implicit so that the route's
// answer is a shape like every other answer here.
type RevokeSelfResponse struct{}

// SessionPolicy is the project's mobile policy as the phone needs it (P6, with
// the compaction threshold of Z2): the values a phone screen decides with, and
// never the whole policy document, which the phone cannot read
// (config/policy/** is not in the paired device's read set).
//
// Every one of them is a *display* input and not an enforcement: what a role,
// a mode or a threshold permits is decided at the consumer (D2791), so a phone
// that edited these would hide a button and change nothing else.
type SessionPolicy struct {
	// CostlyModes are the rigor modes a start must be read back for (D2786,
	// default pro and critical).
	CostlyModes []string `json:"costly_modes"`
	// BiometricOnCostly is the project switch that asks for a biometric
	// re-authentication as well, on a costly start (D2786, P5.1g's path).
	BiometricOnCostly bool `json:"biometric_on_costly"`
	// CompactThresholdPercent is the context fullness at which the phone
	// offers automatic compaction, in percent, and 0 means the project set no
	// automatic compaction at all.
	CompactThresholdPercent int `json:"compact_threshold_percent"`
}

// SessionResponse is what a pairing or a restore answers with (P3, K2): the
// session a phone holds from then on. Both routes answer the same shape,
// because a restored device is in the same position as a paired one — it holds
// a project, an endpoint, a role, a policy, a pinned consumer key and a refresh
// token, and it needs the database URL and the API key to reach the mailbox.
//
// The database URL and the API key are here and not in the QR (F4): the QR
// carries no credential, so the two bootstrap values that are not secrets
// travel on this answer, once, to a device that has already proved an
// identity. The refresh token and the custom token are what the device holds
// afterwards, and the Firebase refresh token the app derives from the custom
// token is never returned by a Fugaro service at all (D2783).
type SessionResponse struct {
	// ProjectID is the project's opaque id, the name the paired list stores
	// (K1) and the name a phone shows without any readable name from the
	// server.
	ProjectID string `json:"project_id"`
	// Endpoint is the project's mobile endpoint, which the phone stores and
	// calls for every later refresh and restore.
	Endpoint string `json:"endpoint"`
	// DatabaseURL and APIKey are the project's Realtime Database URL and its
	// restricted Firebase API key: what the app needs to reach the mailbox
	// after exchanging the custom token.
	DatabaseURL string `json:"database_url"`
	APIKey      string `json:"api_key"`
	// Role is the roster's role for this person at the time of the answer
	// (O14). It is for display and for hiding what the phone cannot do; the
	// consumer resolves the role again from the roster at every action
	// (D2791).
	Role access.Role `json:"role"`
	// Policy is what the phone's screens decide with.
	Policy SessionPolicy `json:"policy"`
	// ConsumerPublicKey is the project's consumer key, unpadded base64url: the
	// key the laptop signs its mailbox replies and heartbeats with (O12). The
	// phone pins it, and it accepts it only when it hashes to the fingerprint
	// the QR carried (pairing) or its paired-list entry carried (restore)
	// (O16).
	ConsumerPublicKey string `json:"consumer_public_key"`
	// RefreshToken is the Fugaro refresh token of this device's family
	// (D2783), unpadded base64url, hashed at rest by the service. It is bound
	// to the device's public key and rotates on every use.
	RefreshToken string `json:"refresh_token"`
	// CustomToken is the Firebase custom token the app exchanges for an ID
	// token, and ExpiresIn is its life in seconds (CustomTokenLifetime). The
	// Firebase refresh token that exchange yields is the app's own and is
	// never sent to Fugaro.
	CustomToken string `json:"custom_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// RefreshResponse is what a refresh answers with (P4): the successor refresh
// token, a fresh custom token and the role and policy as they are now. It
// carries no project configuration, because the session already holds it: a
// refresh that re-sent the database URL and the API key would be a second
// source of a value the phone stores once.
type RefreshResponse struct {
	// RefreshToken is the family's successor. The presented token is dead the
	// moment this one is minted, and a second use of it revokes the whole
	// family (D2783, F32).
	RefreshToken string `json:"refresh_token"`
	// CustomToken and ExpiresIn are the one-hour access token, as in
	// SessionResponse.
	CustomToken string `json:"custom_token"`
	ExpiresIn   int    `json:"expires_in"`
	// Role is the roster's role rewritten at this refresh (O14): a person
	// whose membership changed learns it here, and a refresh for an address
	// with no grant left deletes the device node and answers device_revoked.
	Role access.Role `json:"role"`
	// Policy is the project's policy as it is now.
	Policy SessionPolicy `json:"policy"`
}

// Invite is the invitation an administrator writes with `fugaro pair` for a
// named person (P2) and the pairing route consumes once (P3). It is what makes
// a QR a pointer and not a credential: the QR carries the nonce, the email and
// the endpoint, and everything else a pairing decides is decided from the
// invite, the roster and the assertion (D2799, F4).
//
// It carries the role resolved for the person at the write (D224's resolution
// with its two refusals, D2791) and the accepted providers as they were at the
// write, and neither is authority later: the roster is the member's membership
// and role at every pairing, refresh and restore (O14), and the accepted
// providers are the project's policy (D2785). A pairing that finds a different
// roster role writes the roster's (P3).
type Invite struct {
	// Nonce is the one-time value the QR carries and the invite node is keyed
	// by.
	Nonce string `json:"nonce"`
	// Email is the address the administrator named, canonical
	// (mobileauth.NormalizeEmail), and the pairing requires the assertion's
	// email to equal it.
	Email string `json:"email"`
	// Role is the role resolved at the write, for the pairing's audit line and
	// for a refusal that names what was offered; the node's role is the
	// roster's.
	Role access.Role `json:"role"`
	// AcceptedProviders is the accepted-provider list at the write, which the
	// pairing shows in a provider_not_accepted refusal.
	AcceptedProviders []string `json:"accepted_providers"`
	// ExpiresAt is when the invite stops working (InviteLifetime, O4), and it
	// is a time and not a countdown so that a clock that moved between the
	// write and the scan is visible rather than quietly absorbed.
	ExpiresAt time.Time `json:"expires_at"`
}

// Member is the record that outlives a device (D2791 as amended by O14): the
// first (email, provider, sub) this project accepted for an address, created by
// the invite and written with provider and sub at the first pairing. It is what
// refuses an address that was reassigned to another person (F9, F34) — the
// roster says the address may pair, and this record says *which* identity the
// address is.
//
// It carries no role: membership and role are the roster's, read at every
// pairing, refresh and restore (O14), and a copy here would be a second source
// that no command updates.
type Member struct {
	// Email is the canonical address the record is keyed by.
	Email string `json:"email"`
	// Provider and Sub are the identity the project accepted first for this
	// address, and every later pairing must match both.
	Provider string `json:"provider"`
	Sub      string `json:"sub"`
}

// PairedEntry is one paired project in the synced list (K1, K3, O16): the
// minimum the identity project stores so that a new device can restore without
// a QR, and no more. Readable names stay on the phone (D2796, spec).
//
// The list is what Fugaro's own identity project holds, so it is deliberately
// poor: an opaque id, an endpoint and which identity was used. A dump of the
// store shows no readable project name, and the entries are encrypted under a
// key the function holds (K1).
type PairedEntry struct {
	// ProjectID is the project's opaque id, the same value a session reports.
	ProjectID string `json:"project_id"`
	// Endpoint is the project's mobile endpoint, which restore calls.
	Endpoint string `json:"endpoint"`
	// Identity is the identity that paired this project, in IdentityID's form,
	// so a restore knows which of the person's signed-in identities to ask for
	// an assertion with.
	Identity string `json:"identity"`
	// ConsumerKeyFingerprint is the fingerprint of the project's consumer
	// public key as the pairing saw it. A restore accepts the key the project
	// returns only when it hashes to this, so forging replies needs the
	// public service *and* this list (O16).
	ConsumerKeyFingerprint string `json:"consumer_key_fingerprint"`
}

// IdentityID returns the string that names one signed-in identity in a
// PairedEntry and in a secure-store item: the provider, a dot and the
// provider's stable id, both as the assertion carries them.
//
// It is a join and not a name invented from the email, because an address can
// be reassigned (F9) and an identity may have no usable address at all (an
// Apple private-relay sign-in, D2785).
//
// The dot is the separator and not a colon or a slash because this string ends
// up as the second half of a keychain key, where expo-secure-store accepts only
// the bytes keychainSafe reports and *throws* on any other: an item named for
// an identity whose id carried a colon could not be written on the phone at
// all, and the failure would look like a broken keychain rather than a bad
// name. A part that is empty, or that carries a byte outside that alphabet, is
// therefore refused ("") rather than joined into a name no device can hold. The
// string is compared and never taken apart, so a part may keep dots of its own
// (an Apple subject id has them). The identity list is keyed by this string,
// and one collision would show one person's session under another's account.
func IdentityID(provider, sub string) string {
	if provider == "" || sub == "" {
		return ""
	}
	if !keychainSafe(provider) || !keychainSafe(sub) {
		return ""
	}
	return provider + "." + sub
}

// keychainSafe reports whether s may be one part of a secure-store item name:
// every byte a letter, a digit, "." or "-" or "_". It is expo-secure-store's
// rule, and mobile/src/accounts/account.js's ACCOUNT_ID_RE is the app's copy of
// it: a key outside this set makes the platform throw rather than return a
// miss, so a name composed from unsafe parts is a session the phone cannot
// write and cannot report.
func keychainSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		case b == '.', b == '-', b == '_':
		default:
			return false
		}
	}
	return true
}

// The mobile routes (P5's public service, I1's fail-closed allowlist). They are
// fixed here because the app and the service are written apart: a path spelled
// differently on one side is a 404 on a project that is working perfectly.
const (
	// RoutePair is the pairing route: PairRequest in, SessionResponse out.
	RoutePair = "/mobile/v1/pair"
	// RouteRefresh is the session's rotation route: RefreshRequest in,
	// RefreshResponse out.
	RouteRefresh = "/mobile/v1/refresh"
	// RouteRestoreChallenge issues the single-use challenge a restore is bound
	// to: RestoreChallengeRequest in, RestoreChallengeResponse out.
	RouteRestoreChallenge = "/mobile/v1/restore/challenge"
	// RouteRestore is the silent restore: RestoreRequest in,
	// SessionResponse out.
	RouteRestore = "/mobile/v1/restore"
	// RouteRevokeSelf ends the caller's own refresh family: RevokeSelfRequest
	// in, RevokeSelfResponse out (D2794).
	RouteRevokeSelf = "/mobile/v1/revoke-self"
)

// Routes is the closed set of mobile routes, in the order they are declared.
// The public service mounts exactly these and no other route of the image
// (P5, I1), so this is the list an allowlist is written from rather than a
// prefix rule that would admit a route nobody reviewed.
func Routes() []string {
	return []string{RoutePair, RouteRefresh, RouteRestoreChallenge, RouteRestore, RouteRevokeSelf}
}

// Code is one of the refusal codes a mobile route answers with. It is a closed
// set: the app maps a code to a screen, and anything else is refused outright
// rather than shown as if it were a screen somebody wrote.
type Code string

const (
	// CodeProviderNotAccepted is a provider the project does not accept. Its
	// Error carries AcceptedProviders, and the screen offers to sign in with
	// one of them (D2785).
	CodeProviderNotAccepted Code = "provider_not_accepted"
	// CodeAuthTooOld is a sign-in older than the project's maximum
	// authentication age (O4: 24 hours). The screen says to sign out and back
	// in.
	CodeAuthTooOld Code = "auth_too_old"
	// CodeNotAMember is an address the roster does not admit, an address whose
	// member record holds another identity (a reassigned address, F34), or an
	// entry that cannot be read at all — the resolution fails closed and the
	// screen says the same thing for all three, because which one it is, is
	// not something to tell whoever holds the phone.
	CodeNotAMember Code = "not_a_member"
	// CodeNonceExpired is an invite nonce that is unknown, already spent or
	// past its InviteLifetime. Unknown and spent are the same answer: a
	// distinct one would tell a caller which nonces a project has seen.
	CodeNonceExpired Code = "nonce_expired"
	// CodeStrictNewDeviceNeedsQR is a restore into a project whose strict
	// switch is on (D2784): the device must pair by QR instead.
	CodeStrictNewDeviceNeedsQR Code = "strict_new_device_needs_qr"
	// CodeDeviceRevoked is a refresh whose device node is gone or whose family
	// was revoked -- the answer after an admin revoke, a sign-out everywhere, a
	// replayed token or a membership re-check that failed (F32, D2794). The
	// screen signs the project out.
	CodeDeviceRevoked Code = "device_revoked"
	// CodeMobileDisabled is a project whose published policy has mobile off,
	// including a policy published before the mobile key existed (O9, O14).
	CodeMobileDisabled Code = "mobile_disabled"
)

// Codes is the closed set of refusal codes, in the order they are declared.
func Codes() []Code {
	return []Code{
		CodeProviderNotAccepted,
		CodeAuthTooOld,
		CodeNotAMember,
		CodeNonceExpired,
		CodeStrictNewDeviceNeedsQR,
		CodeDeviceRevoked,
		CodeMobileDisabled,
	}
}

// Error is what a mobile route answers with when it refuses (P3, P4, K2). It
// carries a code, the accepted-provider list when that is what the code is
// about, and a sentence for a person.
//
// The sentence is for a reader and never a value: no token, no key, no
// assertion and no database content ever reaches this shape, because it is
// answered to whoever holds the phone — which, at pairing, is a caller who has
// not yet proved anything.
type Error struct {
	Code Code `json:"code"`
	// AcceptedProviders is carried only by provider_not_accepted, and it is
	// the project's accepted list: the screen offers a sign-in that will work
	// rather than repeating a refusal.
	AcceptedProviders []string `json:"accepted_providers,omitempty"`
	// Message is the sentence for a person.
	Message string `json:"message,omitempty"`
}

// Error reports the code and the sentence, and it is the whole of this file's
// behaviour beyond the two derivations above: an error value that carries the
// code is what lets a handler refuse with one value and a caller read the code
// off it.
func (e Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// The secure-store item names (D2790, D2793): the phone's own store, one item
// per identity and per paired project. The names are prefixes ending in a dot,
// and the id follows: an identity's IdentityID, or a project's opaque id.
//
// The separator is a dot and not a slash or a colon because the alphabet is the
// platform's, not this contract's: expo-secure-store accepts only letters,
// digits, "." and "-" and "_" in a key, and throws on anything else
// (mobile/src/accounts/account.js's ACCOUNT_ID_RE is the app's own copy of that
// rule, over one part rather than a composed name). A composed name is
// therefore only a name the keychain takes while both halves stay inside that
// alphabet, which is what keychainSafe reports and why IdentityID refuses a
// part that is not.
//
// They are fixed here because two sides write them: M1 and M3 store under them
// and M2, K3 and I2 read them, and an item written under one spelling and read
// under another is a session that silently disappears at the next launch.
const (
	// SecureStoreIdentitySession holds one signed-in identity's provider
	// session. It is the only place a provider session is kept, and there is
	// never a project credential in it (D2790 amends D232).
	SecureStoreIdentitySession = "fugaro.identity-session."
	// SecureStoreDeviceKey holds the device's own private key for one paired
	// project: hardware-backed where M0 found one, software otherwise, as the
	// plan's docs say (U5).
	SecureStoreDeviceKey = "fugaro.device-key."
	// SecureStoreProjectSession holds one paired project's session: the
	// refresh token, the project id, the endpoint and the role. The Firebase
	// refresh token the app derives from a custom token is never stored here
	// (D2783).
	SecureStoreProjectSession = "fugaro.project-session."
	// SecureStoreConsumerKey holds one paired project's pinned consumer public
	// key (O12), which is what the phone verifies mailbox replies with.
	SecureStoreConsumerKey = "fugaro.consumer-key."
)

// Binding is what a call expects an assertion to be bound to: the audience it
// was signed for and the challenge it was signed against. It is the caller's
// expectation and never a value read off the assertion, because an expectation
// read off the thing it judges is no check at all.
type Binding struct {
	// Audience is AudienceOf of this project's own endpoint (F39).
	Audience string
	// Challenge is the invite's nonce at pairing, the issued challenge at
	// restore.
	Challenge string
}

// Verifier verifies one assertion (P1, internal/assertion). It is the interface
// P3 and K2 call, and it exists so that neither of them needs the signer or the
// key set: what they hold is an assertion, what they know is what it must be
// bound to, and what they get back is the claim set or a typed refusal naming
// the field that failed (P1's own errors).
//
// A verifier that answers must have checked the signature against the pinned
// identity-project keys, the issuer, the audience, the challenge, the lifetime
// with bounded skew, and the single use of the jti through a ClaimStore — the
// store is the verifier's own dependency and never a parameter here, because a
// caller that could pass a store could pass one that remembers nothing.
type Verifier interface {
	Verify(ctx context.Context, assertion string, want Binding) (Claims, error)
}

// Verdict is a policy's answer (P3, K2). An allowed verdict carries the
// roster's role, which the caller writes on the device node and shows to the
// phone; a refused verdict carries one of Codes() and never a role, so a
// caller that ignored Allowed still gets nothing that admits anybody.
type Verdict struct {
	// Allowed is the answer. The zero value is false: a verdict nobody made
	// admits nothing.
	Allowed bool
	// Code is the refusal, and it is empty when Allowed. A refusal is one of
	// Codes() — not_a_member, provider_not_accepted, auth_too_old or
	// mobile_disabled — and a caller that found any other value has found a
	// code nobody wrote.
	Code Code
	// Role is the roster's role for the email, and it is set only when
	// Allowed. access.RoleNone on an allowed verdict is a policy bug the
	// device node would carry: the node's role is what the consumer resolves
	// actions against (D2791).
	Role access.Role
	// Accepted is the accepted-provider list, carried when Code is
	// provider_not_accepted, for the same screen the Error carries it for.
	Accepted []string
}

// Policy applies one project's mobile policy to a verified identity (F35): is
// this address a member of the roster with which role (O14), is this provider
// accepted (D2785), was the sign-in recent enough (O4), is mobile on for this
// project (D2796), and does the identity match the member record's first use
// (D2791 as amended)? Pairing, refresh and restore all ask it, and they ask it
// the same way: an implementation that answered differently for two of the
// three would be a way into a project that only one route guards.
//
// The implementation holds what it reads — the roster (internal/access) and
// the member record (MemberStore) — and it is the one place in the mobile flow
// where those two meet, so the checks are made once rather than three times in
// three handlers.
type Policy interface {
	Allow(ctx context.Context, project string, claims Claims) (Verdict, error)
}

// InviteStore reads the invitations `fugaro pair` wrote and spends one (P2
// writes, P3 consumes). The nonce is spent in the same call that reads it: read
// then spend is a window two credsvc instances can both pass through, and a
// pairing that two instances both honoured would be two device nodes for one
// invite.
type InviteStore interface {
	// Consume returns the invite the nonce names and marks it used, reporting
	// false when there is no such invite or it was already used. The expiry is
	// the caller's check, because it is the caller that answers
	// nonce_expired.
	Consume(ctx context.Context, project, nonce string) (Invite, bool, error)
}

// MemberStore keeps the member records (D2791 as amended by O14), which outlive
// any device: the first (email, provider, sub) a project accepted. The record
// is created by the invite and written at the first pairing (P3), read by
// Policy at every pairing, refresh and restore, and removed only by an explicit
// admin command — never by a device revoke, because revoking a phone must not
// hand the next pairing back to whoever signs in first with that address (F34).
type MemberStore interface {
	// Member returns the record for an email, reporting false when there is
	// none (an invitation nobody has paired with yet).
	Member(ctx context.Context, project, email string) (Member, bool, error)
	// Record writes the first-use record, and it is the caller's rule that it
	// happens once: this store does not overwrite a record, so a second
	// pairing with a different sub is refused by the policy rather than
	// quietly recorded.
	Record(ctx context.Context, project string, member Member) error
	// Remove deletes the record alone. The device nodes and refresh families
	// of that email go with it, and the caller that removes a member (P7's
	// removal, called by `fugaro user revoke`, P9) is the one that deletes
	// them: a store cannot reach a family and a node that live behind other
	// interfaces, and a removal that half happened would leave a device
	// holding database access with no member behind it (F32).
	Remove(ctx context.Context, project, email string) error
}

// RefreshGrant is one refresh token as RefreshStore hands it out and takes it
// back (D2783).
type RefreshGrant struct {
	// Token is the opaque token the device holds, unpadded base64url. The
	// store keeps its hash and never the token, so a dump of the store holds
	// nothing a caller can present.
	Token string
	// Family is the family the token belongs to, which is what a replay, an
	// admin revoke or a sign-out everywhere ends: every token of the family
	// stops working at once (D2783, F32).
	Family string
	// ExpiresAt is the idle expiry the store slid forward: the family ends
	// after IdleLifetime without use.
	ExpiresAt time.Time
}

// RefreshStore keeps a project's refresh families (P4 implements it; P7 revokes
// through it). It is the store and not the route, so that the CLI's admin revoke
// ends a family without a phone or a service in the loop, and so that the
// refresh's rules — rotation, replay, idle expiry, one family per device — live
// in one place.
type RefreshStore interface {
	// Issue starts a new family for a device that has just paired or restored,
	// and returns the token the device holds.
	Issue(ctx context.Context, project, uid string) (RefreshGrant, error)
	// Rotate exchanges a presented token for its successor in the same family.
	// The caller has already checked the signature over the token; this is
	// what makes a presented token dead, a replay of it revoke the family
	// (F32) and an idle family lapse.
	Rotate(ctx context.Context, project, token string) (RefreshGrant, error)
	// Revoke ends a family. It is idempotent, because the two callers that use
	// it — a replay rule and an admin — may both reach one family.
	Revoke(ctx context.Context, project, family string) error
}

// RefreshRoot is where a project keeps its refresh families — one node per
// device, under config/admin beside the device allowlist, the invites, the
// member records and the push registrations, where no client token may read or
// write — and RefreshFamilyPath is one device's node in it.
//
// The path is in the contract because two sides write it and neither may spell
// its own: P4's store creates, rotates and deletes a family at this path, and
// P7's admin revoke deletes the node with the database admin it holds, with no
// phone and no service in the loop. A store that kept its families somewhere
// else would make that revoke delete nothing and report no error, leaving a
// stolen phone refreshing — the hole D2794 closes (F32) — so the one place a
// family is looked for is fixed here rather than in either caller.
const RefreshRoot = "config/admin/refresh"

// RefreshFamilyPath is the node one device's family lives at. The key is the
// DEVICE's uid, which is the one name both sides already hold: Issue is asked
// for a family by uid, the device node is keyed by it, and an admin revoke
// names the family it ends from the uid alone.
func RefreshFamilyPath(deviceUID string) string { return RefreshRoot + "/" + deviceUID }

// MintRequest is what a session asks the minter for: the uid the device's
// Firebase token is for, and the role the token reports.
type MintRequest struct {
	// UID is the device node's uid, which is the Firebase uid the rules read
	// (config/admin/devices/$uid): minting for another uid is minting a
	// session for another device.
	UID string
	// Role is written into the token's claims for the phone's display only
	// (D2791). The Firebase rules read the device node and never a claim, and
	// the consumer resolves the role from the roster again at every action.
	Role access.Role
}

// CustomToken is the Firebase custom token a session hands the app (D2783,
// O8), and its life.
type CustomToken struct {
	Token     string
	ExpiresAt time.Time
}

// Minter mints the one-hour custom token a session's app exchanges for an ID
// token. It is separate from the refresh store because what it holds is the
// signer grant — the separate device-only signer account of O8 — and a store
// that could mint would be a store that needs the grant.
//
// What it signs is a Firebase custom token and never an assertion: a token
// signed by this account is a session for one device's uid, and the assertion
// is signed by the identity project's key and answers a different question
// (O8, D2792).
type Minter interface {
	Mint(ctx context.Context, req MintRequest) (CustomToken, error)
}

// The namespaces a ClaimStore key is claimed under, one per kind of single-use
// value. A new kind adds a constant here rather than a spelling at the call
// site, so that two callers cannot claim the same value under two names and
// believe they share a decision.
const (
	// NamespaceJTI is an assertion's own id (P1).
	NamespaceJTI = "jti"
	// NamespaceChallenge is a challenge a restore was issued (K2).
	NamespaceChallenge = "challenge"
)

// ClaimStore is the compare-and-set store that makes a single-use value
// single-use across credsvc's several instances (F39): a `jti`, a restore
// challenge, an invite nonce. It is deliberately not a map in the process,
// because two instances of the public service are two places a value would
// otherwise be unused in twice.
type ClaimStore interface {
	// Claim records key as used for ttl and reports whether this call is the
	// one that used it. False is a refusal and never an error: a value that
	// was already spent is the ordinary case the store exists for.
	Claim(ctx context.Context, namespace, key string, ttl time.Duration) (bool, error)
}

// The key-set envelope (O7, A4): the JWS the root key signs over the identity
// project's JWKS, with the set's sequence number and its validity beside the
// keys. A fetched key set is accepted only inside one of these, so someone who
// can change what the keys URL serves cannot publish keys of their own.
const (
	// KeySetFormat is the payload's format tag: a reader refuses a document
	// that is not this, rather than reading a future one as if it were this
	// one.
	KeySetFormat = "fugaro-identity-key-set"
	// KeySetAlg is the only algorithm the envelope or a key in it may carry.
	// A verifier that accepted another alg would accept the alg the attacker
	// wrote in the header.
	KeySetAlg = "EdDSA"
	// KeySetTyp is the header's type tag, which is what makes the envelope one
	// document and not a signed blob that could be replayed as something else.
	KeySetTyp = "fugaro-key-set+jws"
)

// KeySetHeader is the JWS header of a signed key set (A4 writes it, P1 reads
// it). The signing input is base64url of this header, a dot, base64url of the
// KeySet, in the field order declared here and with no whitespace, and the
// signature is the root key's over those bytes.
type KeySetHeader struct {
	Alg string `json:"alg"`
	// Kid is the root key's id, and it is the *root's*: the keys the envelope
	// carries have ids of their own, and mixing the two up is how a reader
	// ends up verifying an envelope with a key the envelope brought.
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// JWK is one public key in a JWKS: the OKP/Ed25519 shape, which is the only
// one the identity project publishes (A2).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	// X is the public key, unpadded base64url, and there is no private field
	// here and never will be: this is the shape a public document carries.
	X   string `json:"x"`
	Kid string `json:"kid"`
	Alg string `json:"alg,omitempty"`
	Use string `json:"use,omitempty"`
}

// JWKS is a key set's keys.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// KeySet is the payload of a signed key set: the identity project's JWKS, the
// sequence number that orders one set against another, and the set's own
// validity. Seq is what a verifier compares against the floor its build pinned
// and the highest sequence it has stored, so a stale document served by a cache
// cannot walk a project back onto older keys (O7).
type KeySet struct {
	Format string `json:"format"`
	Seq    int64  `json:"seq"`
	Iat    int64  `json:"iat"`
	Exp    int64  `json:"exp"`
	JWKS   JWKS   `json:"jwks"`
}
