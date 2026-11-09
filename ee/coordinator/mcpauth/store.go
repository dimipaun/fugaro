// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file is M2's half of the store: what a record is, where it lives, and
// the interface every other task builds against. It names no store, which is
// the point — an owner who answers §8 of the plan with one of the other two
// candidates changes the one backend (store_bucket.go) and its test, and no
// task's code.
//
// **Hashes only** (D922, D930). No record here holds a token value, a refresh
// token or a client secret: a record holds the sha256 of one, so a reader of
// the store cannot present what it reads, and the value is shown once at mint
// and never again. That is the second barrier; the first is D935, which moved
// these records out of the project's database into a store whose IAM names the
// front door's own account (`mcp_server`) and no other member, because every
// launcher and operator holds a project-level database grant no rule binds
// (reconciliation.md F32).
//
// **The family** (D930, D924). A mint opens a family: the client and identity
// pair whose refresh chain it is, carrying the limits the whole chain is held
// to. Rotation happens inside a family and revocation deletes it — one delete,
// after which no token of it resolves at all — which is what makes rotation a
// lifetime mechanism and the spend counter the family's rather than the
// token's.
//
// The store is objects rather than rows, because D935's answer is a bucket.
// Everything here is therefore written the way internal/blobx writes: create
// if absent, replace if the generation still matches, delete. The one place
// that matters is rotation, which is a compare-and-swap on the family's record
// — a database gave that away in a transaction, and a bucket has to be asked
// for it (Rotate, and TestARotationUnderAConcurrentRevokeLosesNeither).

// PathPrefix is where every record of this store lives. It is the store's own
// prefix and not a path in anything else: the store's bucket is its own, and
// the prefix is what makes one bucket a store rather than a set of them.
const PathPrefix = "mcpauth/"

const (
	clientPrefix = PathPrefix + "clients/"
	familyPrefix = PathPrefix + "families/"
	tokenPrefix  = PathPrefix + "tokens/"
	// requestPrefix and codePrefix are the authorization flow's two
	// short-lived records (M5): the pending authorize request a consent
	// answers, and the code that consent produces. Both are single-use, and
	// both are spent by a compare-and-swap rather than by a read and a
	// delete (consume).
	//
	// Both are also written before a person has proved anything: the request
	// is written by the unauthenticated GET, so a stranger with a loop leaves
	// one behind per iteration until its TTL passes. The TTL is a bound on
	// what the record is *read* as and not on what is stored — nothing in
	// this tree sweeps an expired request or code, RevokeFamily being the
	// store's only delete — and that is the deferral Client.CreatedAt records
	// too. No task of the plan carries it, so it is raised as a finding for
	// the owner rather than left to look assigned.
	requestPrefix = PathPrefix + "requests/"
	codePrefix    = PathPrefix + "codes/"
)

// The refusals a caller acts on. Each is a sentinel so that a caller can tell
// the two apart that look alike from the outside — a token that was never
// minted and a token whose family was revoked are both "not found", and only
// the second is worth an audit entry.
var (
	// ErrTokenNotFound is every way a presented token resolves to nothing:
	// no record for its hash, a record whose family is gone, or a family
	// that was revoked. A revoked token reads as a token that never existed
	// because that is exactly what it is now — one delete took the family
	// and every token in it (D930).
	ErrTokenNotFound = errors.New("mcpauth: no token with that hash")
	// ErrClientNotFound is a client id no registration wrote.
	ErrClientNotFound = errors.New("mcpauth: no client with that id")
	// ErrFamilyNotFound is a family that is not in the store: one that was
	// never minted, or one a revoke deleted.
	ErrFamilyNotFound = errors.New("mcpauth: no such token family")
	// ErrConflict is a write that lost a race with another writer: the
	// object changed, or vanished, between the read the caller made and the
	// write it asked for.
	ErrConflict = errors.New("mcpauth: the record changed under the write")
	// ErrRefreshReplay is a refresh token presented after it was rotated
	// away (D930). It is the theft signal: the family is revoked before this
	// error reaches the caller, so the legitimate holder and the thief both
	// stop working and the audit record is the only thing left holding the
	// event.
	ErrRefreshReplay = errors.New("mcpauth: a refresh token that has already been rotated away was presented: the family is revoked")
	// ErrWrongTokenKind is a refresh grant presented an access token, or an
	// access token looked up as a refresh one. The kind is stored rather
	// than inferred for this: an access token is the value an agent leaks,
	// and it must not be the value that extends its own family.
	ErrWrongTokenKind = errors.New("mcpauth: the token is not of the kind the caller asked for")
	// ErrRequestNotFound is a consent naming a flow that is not pending: one
	// that was never issued, one that was already answered, or one that is
	// gone. A consent is answered once, and a second answer to the same
	// binding is refused rather than honoured (D928's CSRF binding).
	ErrRequestNotFound = errors.New("mcpauth: no such pending authorize request")
	// ErrRequestUsed is a consent naming a flow that has already been
	// answered. It is the single-use half of the binding, and it is separate
	// from ErrRequestNotFound because the two are different events: one is a
	// stranger's guess, the other is a replay of a page this server issued.
	ErrRequestUsed = errors.New("mcpauth: the authorize request was already answered")
	// ErrRequestApproved is a paired device's act naming a flow that a device
	// has already approved (M5b). One flow is approved once: a second approval
	// would be a second identity for a code that is minted once, and the
	// record's own identity is what the code acts for, so the first one stands
	// and the second is refused rather than written over it.
	ErrRequestApproved = errors.New("mcpauth: the authorize request was already approved")
	// ErrIDTokenSpent is an ID token presented at a second approval (M5b). The
	// spend is what makes a leaked token one approval rather than one per
	// replay until it expires, so the second use is refused as the token it is
	// — not as a flow, and not as a sign-in that failed to verify.
	ErrIDTokenSpent = errors.New("mcpauth: the ID token has already approved a flow")
	// ErrCodeNotFound is an authorization code that resolves to nothing: one
	// that was never issued, or one whose record is gone.
	ErrCodeNotFound = errors.New("mcpauth: no such authorization code")
	// ErrCodeUsed is an authorization code presented twice. The code is
	// single-use (spec.md §3), and the consumption is a compare-and-swap so
	// that two redemptions of one code cannot both succeed.
	ErrCodeUsed = errors.New("mcpauth: the authorization code was already redeemed")
)

// errMissing and errChanged are what a backend hands the record layer instead
// of its own driver's errors, so the layout below reads the same over GCS as
// it would over any other store.
var (
	errMissing = errors.New("mcpauth: the object is not there")
	errChanged = errors.New("mcpauth: the object is not the one the write matched")
)

// Client is one registration (M5 writes it, M5 and M7 read it). It is the
// record D922 calls the client list, and its secret is a hash for the same
// reason a token is: the registration response returns the secret once and
// nothing at rest can present it.
type Client struct {
	// ID is the client id the authorization server issued. It is what a
	// token's family is counted by, so it is never a name a client chose.
	ID string `json:"id"`
	// Name is the client's own name for itself, shown on the consent page.
	// It is a stranger's text and never a key, a path or a command.
	Name string `json:"name"`
	// RedirectURIs are the URIs this client registered, validated at
	// registration and again at authorize against the same rule (D928).
	RedirectURIs []string `json:"redirect_uris"`
	// SecretHash is sha256 of the client secret, hex, empty for a client
	// that holds none. The secret itself is returned once, at registration.
	SecretHash string `json:"secret_hash"`
	// CreatedAt is when the registration was written. It is the field a
	// lifecycle reads, and it is deliberately the only thing here that
	// anticipates one: spec.md §8 bounds an open registration endpoint's
	// residual risk by a per-source rate limit and by these records being
	// expirable, and neither bound is implemented in this package or anywhere
	// else in this tree. No task of the plan carries either one — the
	// M10 entry is the audit record and revocation — so both are recorded in
	// reconciliation.md §4a as a finding for the owner to assign. Until one
	// lands the exposure is latent only because no binary mounts these
	// routes yet: the record carries the timestamp that lifecycle will read
	// and nothing here enforces it. The flow's two records are in the same
	// position and are named on requestPrefix above.
	CreatedAt time.Time `json:"created_at"`
}

// Family is one token family: the client and the identity it acts for, the
// limits the whole chain is held to, and the refresh token that is currently
// valid. It is the unit D930 rotates and revokes, and the unit D924 counts
// spend by.
type Family struct {
	// ID is the family's own id. It is random and server-issued: two
	// authorizations by one client on one account are two families, and a
	// derived id would make them one.
	ID string `json:"id"`
	// ClientID is the registration the family belongs to.
	ClientID string `json:"client_id"`
	// Identity is the exact `user:` entry the person signed in as (D923,
	// D933), or an unattended client's own identity (D929).
	Identity string `json:"identity"`
	// Limits are the limits every token of this family carries, fixed when
	// the family was minted and narrowed against the project's before that
	// (Limits.Narrow). Rotation carries them unchanged: a refresh token
	// extends a family, and it never widens one.
	Limits Limits `json:"limits"`
	// RefreshHash is sha256 of the refresh token that is valid now, hex. The
	// previous one stays where it was, so presenting it again is not a miss
	// but a replay (Rotate).
	RefreshHash string `json:"refresh_hash"`
	// CreatedAt is when the family was minted.
	CreatedAt time.Time `json:"created_at"`
	// LastUsedAt is when this family last rotated, and it is the idle bound's
	// own field (M5b's fourth requirement). It is written by the rotation and
	// by nothing else — the mint that opens a family leaves it zero, which the
	// bound reads as CreatedAt — because the thing the bound is about is a
	// chain nothing has extended for a while, and only a rotation extends one.
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// The two bounds a refresh family ages by (M5b's fourth requirement, spec.md §6
// and §8).
//
// **The figures are the owner's to set.** The requirement M-a and the review of
// M5 raised is that a family's exposure *ends* rather than outliving the ID
// token's hour by an unbounded amount: the approval is bound to a device's
// session, and until this task the family that approval left behind had no
// lifetime at all — `AccessTokenTTL` was the package's only expiry and a
// refresh token's is the zero time (spec.md §8). What the tree carries until he
// answers is a shape: an absolute bound on one chain's whole life and an idle
// bound on a chain nothing has extended, both read at the rotation, which is
// the only place a family is used.
const (
	// FamilyTTL is how long a family lives from the mint, however busy it is.
	// It is the bound that ends the exposure a leaked ID token leaves behind:
	// a device's session approves one flow, and the chain that flow opened
	// stops being extendable after this.
	FamilyTTL = 30 * 24 * time.Hour
	// FamilyIdleTTL is how long a family may go un-rotated. It is the bound a
	// leaked refresh token runs into: an attacker's rotations keep the family
	// inside FamilyTTL only for as long as they keep using it, and a chain
	// nobody touches — the one a client that stopped using it left behind —
	// ends here.
	FamilyIdleTTL = 7 * 24 * time.Hour
)

// TokenKind says which half of a family's pair a record is.
type TokenKind string

const (
	// KindAccess is a short-lived access token: the value a request carries
	// and the resource server validates (spec.md §6).
	KindAccess TokenKind = "access"
	// KindRefresh is a refresh token: the long-lived half, whose lifetime is
	// the family's rotation and revocation rather than an expiry.
	KindRefresh TokenKind = "refresh"
	// KindIDToken is the record of a spent Firebase ID token: the sign-in a
	// paired device's approval carried, written by M5b so that the same token
	// cannot approve a second flow (spec.md §8). It is a kind of its own and
	// not a kind the front door reads a bearer out of: the value is never one
	// this server issued, so a record of it that resolved as an access or a
	// refresh token would be a Firebase token this door acted on, which is the
	// one thing spec.md §8 says it will not do.
	KindIDToken TokenKind = "id_token"
)

// TokenRecord is one token at rest: its hash, the family it belongs to,
// which half of the pair it is, and the token it stands for. The token's own
// fields are stored with it so that a lookup is one read of this and one of
// the family, and a reader of the store sees the limits the front door will
// enforce without holding anything that could be presented.
//
// Token is a snapshot taken at mint. The family is the authority a rotation
// writes to; a record is what one token was issued as.
type TokenRecord struct {
	// Hash is sha256 of the token value, hex. It is both the record's key
	// and the only thing about the value that is written (D922, D930).
	Hash string `json:"hash"`
	// FamilyID is the family this token belongs to.
	FamilyID string `json:"family_id"`
	// Kind is the half of the pair this record is.
	Kind TokenKind `json:"kind"`
	// Token is the value the token stands for, resolved by LookupByHash.
	// For KindRefresh the expiry is the zero time: a refresh token is
	// bounded by rotation and by revocation and by nothing else (D930).
	Token Token `json:"token"`
}

// Request is one pending authorize request: the flow a consent answers (M5).
//
// It exists for the CSRF binding D928 asks for. The authorize request carries
// the client's `state`, and the consent has to carry the coordinator's own
// binding between the request and the answer — a binding a stranger cannot
// guess, because without it an attacker's client can finish a sign-in a person
// began for another one. The binding is this record's own id: it is random and
// server-issued, it is written to the page the person reads and to nowhere a
// stranger can see, and it is spent when the consent is answered
// (Store.TakeRequest).
//
// Everything the approval decides is taken from here and never from the
// approval itself: the approval carries the binding, the flow's number and the
// proof of the sign-in, and an approval that could name its own redirect URI
// would be an approval to send a code somewhere the person was never shown.
// The identity is here too, and it is the one field of the flow that arrives
// later: it is what the paired device's ID token attests (M5b), so it is
// settled when the device's act is recorded and read by the page's answer,
// which mints the code.
type Request struct {
	// ID is the flow's own id and the binding the consent must carry.
	ID string `json:"id"`
	// Number is the flow's own number, and it is M-a's number matching: it
	// is shown on the screen the authorization request renders and the
	// approval has to present it back. An approval that carried the binding
	// alone would be one an attacker who read the id out of a browser's
	// history could answer, and a number the approver had to have seen is
	// what ties the approval to a person looking at the screen that asked.
	Number string `json:"number"`
	// ClientID is the registration that asked.
	ClientID string `json:"client_id"`
	// RedirectURI is the URI the code will be sent to. It was matched
	// against the client's registered list when this record was written, and
	// it is carried here so that the consent does not have to name it.
	RedirectURI string `json:"redirect_uri"`
	// Challenge is the PKCE code challenge, verbatim. The method is not
	// stored: S256 is the only one this server accepts (spec.md §3).
	Challenge string `json:"code_challenge"`
	// State is the client's own state, carried here so that it returns on
	// the redirect unaltered.
	State string `json:"state"`
	// Scopes are the scopes the client asked for, already checked against
	// D927's vocabulary. They become the token's scopes, narrowed against the
	// project's.
	Scopes []string `json:"scopes"`
	// ExpiresAt is when the flow stops waiting for its consent.
	ExpiresAt time.Time `json:"expires_at"`
	// Approval is the paired device's approval of this flow, written when the
	// device's own act arrives and nil until then (M5b). It is a field of this
	// record rather than a record of its own, and that is the third
	// requirement's whole mechanism: an approval that lives on the flow cannot
	// be moved to another client, another redirect URI or another state,
	// because there is nowhere else to put it. The code is minted only for a
	// flow whose record carries one.
	Approval *Approval `json:"approval,omitempty"`
}

// Approval is one paired device's approval of one flow (M5b): what the device's
// act recorded before any code existed.
//
// It holds a hash and never the ID token itself (D922), exactly as a token
// record does, so a reader of the store cannot present what it reads — and the
// identity it proved, because that is what the code and the family the flow
// mints act for (D923, D933).
type Approval struct {
	// IDTokenHash is sha256 of the ID token whose act this was, hex. It is
	// written here beside the flow for the same reason the token's own spend
	// record exists: the two together are what says which sign-in approved
	// which flow, and neither holds the value.
	IDTokenHash string `json:"id_token_hash"`
	// Identity is the exact `user:` entry the device's sign-in proved, which
	// is what the minted code carries (verifySignIn).
	Identity string `json:"identity"`
	// ApprovedAt is when the device's act was recorded, read from the server's
	// own clock at the endpoint rather than from the store's.
	ApprovedAt time.Time `json:"approved_at"`
}

// Code is one authorization code at rest (M5): the hash of the value that was
// sent to the client, and what that value stands for.
//
// Like every other record here it holds a hash and never the value (D922,
// D930), so a reader of the store cannot present what it reads. The code is
// bound to the client, the redirect URI and the code challenge it was issued
// against, and to the identity the sign-in proved: the token endpoint reads all
// four out of this record rather than out of the request, so an exchange cannot
// retarget a code that was issued for somewhere else.
type Code struct {
	// Hash is sha256 of the code value, hex: the record's key.
	Hash string `json:"hash"`
	// ClientID is the registration the code was issued to.
	ClientID string `json:"client_id"`
	// RedirectURI is the URI the code was issued for. The exchange has to
	// present it again, exactly.
	RedirectURI string `json:"redirect_uri"`
	// Challenge is the PKCE code challenge the code was issued against.
	Challenge string `json:"code_challenge"`
	// Identity is the exact identity the sign-in proved (M6's verifier), and
	// what the token will act for.
	Identity string `json:"identity"`
	// Scopes are the scopes the code was issued with.
	Scopes []string `json:"scopes"`
	// ExpiresAt is when the code stops being redeemable.
	ExpiresAt time.Time `json:"expires_at"`
	// FamilyID is the token family the code's first exchange minted. It is
	// not written with the code: BindCodeFamily writes it into the spent
	// marker afterwards, and ConsumeCode answers it on the one path that
	// reads a spent code — the replay, which revokes that family
	// (OAuth 2.1 §4.1.3). It is empty on a code that was never exchanged and
	// on one whose exchange minted nothing.
	FamilyID string `json:"family_id,omitempty"`
}

// Store is the token records of D935, wherever the owner's answer to §8 puts
// them. Every method is one object read, one conditional write, or — for
// RotateRefresh — a read and a write that have to be one act: there is no
// transaction here and no query, so an operation whose answer depends on two
// objects being read at one instant belongs inside this interface rather than
// in a caller holding two of its methods.
type Store interface {
	// PutClient writes a registration, and refuses a client id that is
	// already there. A client id is issued once.
	PutClient(ctx context.Context, c Client) error
	// GetClient reads one registration.
	GetClient(ctx context.Context, clientID string) (Client, error)
	// GetFamily reads a family and the generation the read saw. The
	// generation is what PutFamily matches, so it is the caller's to keep
	// between the two calls.
	GetFamily(ctx context.Context, familyID string) (Family, int64, error)
	// PutFamily writes a family: gen 0 creates it and fails if one is
	// already there, any other gen replaces it only while the object is
	// still that generation. ErrConflict is a family that changed under the
	// caller, which is the answer rotation has to have.
	PutFamily(ctx context.Context, f Family, gen int64) error
	// PutToken writes a token record, and refuses a hash that is already
	// there. A token value is random, so its hash is written once and is
	// never replaced: the record of a rotated-away refresh token is what a
	// replay is recognized by.
	PutToken(ctx context.Context, r TokenRecord) error
	// LookupByHash resolves a presented token's hash to its record, or
	// ErrTokenNotFound when there is no record for it or its family is
	// gone. It is a lookup and not a validation: the caller decides on the
	// expiry, the kind and the scopes, because the answer to "may this be
	// used" belongs to the resource server and not to the store.
	LookupByHash(ctx context.Context, hash string) (TokenRecord, error)
	// RotateRefresh is D930's rotation: the refresh token whose hash is
	// presentedHash is exchanged for one whose hash is nextRefreshHash, or the
	// family is revoked because the token presented was already rotated away.
	// It is one method of the store and not a caller's composition of a lookup
	// and a read and a write, because the read that decides has to be the read
	// the write is matched against — see the implementation for what a caller
	// composing them gets wrong.
	RotateRefresh(ctx context.Context, presentedHash, nextRefreshHash string) (Family, error)
	// RevokeFamily revokes a family: one delete, after which every access
	// token and every refresh token of it resolves to nothing (D930). It is
	// idempotent and unconditional — a revoke must not lose a race with the
	// rotation it is revoking, which is what a generation-matched delete
	// would let it do.
	RevokeFamily(ctx context.Context, familyID string) error
	// PutRequest writes a pending authorize request, and refuses an id that
	// is already there: the id is the consent's binding and is issued once.
	PutRequest(ctx context.Context, req Request) error
	// TakeRequest reads a pending authorize request and spends it in the same
	// act, so that one binding answers one consent: a second consent carrying
	// it is refused with ErrRequestUsed rather than answered twice. It is one
	// method and not a read the caller composes with a write for the same
	// reason RotateRefresh is — the read that decides has to be the write
	// that spends it.
	TakeRequest(ctx context.Context, id string) (Request, error)
	// GetRequest reads a pending authorize request without spending it, and is
	// what M5b's two steps judge a flow by before either of them writes: the
	// device's act checks the flow's number and its expiry, and the page's
	// answer asks whether the flow carries an approval at all. A request that
	// was answered is refused with ErrRequestUsed, so a caller cannot read a
	// spent record as a pending one.
	GetRequest(ctx context.Context, id string) (Request, error)
	// ApproveRequest records a paired device's approval on a pending request
	// and answers the record as it now stands (M5b). It reads and writes in
	// the same act, matched against the generation it read, so an approval
	// that lands as the flow is being answered is a conflict rather than a
	// write over a spent record, and two devices' acts on one flow are one
	// approval and one refusal (ErrRequestApproved).
	ApproveRequest(ctx context.Context, id string, ap Approval) (Request, error)
	// PutCode writes an authorization code, and refuses a hash that is
	// already there. The code value is random, so its hash is written once.
	PutCode(ctx context.Context, c Code) error
	// ConsumeCode reads an authorization code and spends it in the same act
	// (spec.md §3, "single-use"): the second redemption of one code is
	// refused with ErrCodeUsed, and the compare-and-swap is what makes two
	// simultaneous redemptions one success rather than two. On that refusal
	// it answers the spent record's FamilyID, which is the family the first
	// exchange minted: the caller is the one that has to take it down
	// (OAuth 2.1 §4.1.3), and the store does not revoke on its own.
	ConsumeCode(ctx context.Context, hash string) (Code, error)
	// BindCodeFamily writes the family a code's first exchange minted into
	// the code's spent record, so that a replay of the code can revoke it.
	// It is called once, by the exchange that spent the code and after the
	// family exists; a code that is not spent is refused rather than
	// rewritten.
	BindCodeFamily(ctx context.Context, hash, familyID string) error
	// AppendAudit adds one entry to the audit record of one identity, newest
	// first and bounded (audit_record.go). It is one method and not a caller's
	// read composed with a write for the same reason RotateRefresh is: the read
	// that decides the write has to be the read the write is matched against,
	// or two calls of one person at once lose one of the two entries.
	AppendAudit(ctx context.Context, identity string, entry AuditEntry) error
	// GetAudit reads one identity's audit record. An identity no call has been
	// served for has an empty record, which is an answer and not an error: the
	// audit record is where D920's audit is at rest (D935), and the tool that
	// serves it is the only reader of it a caller has (D933).
	GetAudit(ctx context.Context, identity string) (AuditRecord, error)
}

// TokenHash is sha256 of a token value, hex: what the store keys a record by
// and the only thing about a token that is written. A caller presenting a
// token hashes it and looks it up by the result; nothing in this package ever
// keys a record by anything else.
func TokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// NewValue is a fresh token value: 32 random bytes, hex. The store never sees
// it — only its hash — so a caller that loses it has lost the token, which is
// the "shown once" of D922 stated as a fact rather than as a rule.
func NewValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcpauth: reading randomness: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// NewID is a fresh record id, for a family. It is random and server-issued:
// an id a client could choose is an id a client could collide with, and a
// family id names the thing D930 revokes.
func NewID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcpauth: reading randomness: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// records is the record layer: the key layout and the encoding above, over
// whatever backend a store's own file supplies.
type records struct {
	objs objects
	// now is the clock a family's two bounds are read against (M5b). It is a
	// field here rather than an instant a caller passes because the call that
	// judges a family's age is RotateRefresh's, whose signature is the token
	// endpoint's (mint.go), and because a caller that passed the instant would
	// be a caller deciding on one instant and swapping the family at another.
	// A store built without one is the process's own clock.
	now func() time.Time
}

// objects is what a backend gives the record layer, and it is exactly the four
// conditional operations internal/blobx offers. A backend that cannot do a
// create-if-absent or a generation-matched replace cannot carry this store:
// rotation is a compare-and-swap, and a store that quietly turned one into a
// read-modify-write would reintroduce exactly the race D930's replay
// detection is counting on.
type objects interface {
	read(ctx context.Context, key string) (data []byte, gen int64, err error)
	create(ctx context.Context, key string, data []byte) (gen int64, err error)
	replace(ctx context.Context, key string, data []byte, gen int64) (newGen int64, err error)
	delete(ctx context.Context, key string) error
}

func newRecords(objs objects) Store { return records{objs: objs} }

// newRecordsAt is newRecords over a clock a caller owns, which is how a test
// reaches the family's two bounds without waiting for a month: nothing else
// reads a clock in this layer, and every other expiry this package has is the
// server's own.
func newRecordsAt(objs objects, now func() time.Time) Store {
	return records{objs: objs, now: now}
}

// clock is the store's instant, and the process's own when no caller supplied
// one (newRecords).
func (r records) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

func (r records) PutClient(ctx context.Context, c Client) error {
	if err := keySegment(c.ID); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("mcpauth: encoding client %q: %w", c.ID, err)
	}
	if _, err := r.objs.create(ctx, clientPrefix+c.ID, data); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("mcpauth: client %q is already registered: %w", c.ID, ErrConflict)
		}
		return err
	}
	return nil
}

func (r records) GetClient(ctx context.Context, clientID string) (Client, error) {
	if err := keySegment(clientID); err != nil {
		return Client{}, err
	}
	var c Client
	if err := r.get(ctx, clientPrefix+clientID, &c); err != nil {
		if errors.Is(err, errMissing) {
			return Client{}, fmt.Errorf("mcpauth: client %q: %w", clientID, ErrClientNotFound)
		}
		return Client{}, err
	}
	return c, nil
}

func (r records) GetFamily(ctx context.Context, familyID string) (Family, int64, error) {
	if err := keySegment(familyID); err != nil {
		return Family{}, 0, err
	}
	data, gen, err := r.objs.read(ctx, familyPrefix+familyID)
	if errors.Is(err, errMissing) {
		return Family{}, 0, fmt.Errorf("mcpauth: family %q: %w", familyID, ErrFamilyNotFound)
	}
	if err != nil {
		return Family{}, 0, err
	}
	var f Family
	if err := decode(data, &f); err != nil {
		return Family{}, 0, fmt.Errorf("mcpauth: family %q: %w", familyID, err)
	}
	return f, gen, nil
}

func (r records) PutFamily(ctx context.Context, f Family, gen int64) error {
	if err := keySegment(f.ID); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("mcpauth: encoding family %q: %w", f.ID, err)
	}
	if gen == 0 {
		if _, err := r.objs.create(ctx, familyPrefix+f.ID, data); err != nil {
			if errors.Is(err, errChanged) {
				return fmt.Errorf("mcpauth: family %q already exists: %w", f.ID, ErrConflict)
			}
			return err
		}
		return nil
	}
	if _, err := r.objs.replace(ctx, familyPrefix+f.ID, data, gen); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("mcpauth: family %q: %w", f.ID, ErrConflict)
		}
		return err
	}
	return nil
}

func (r records) PutToken(ctx context.Context, rec TokenRecord) error {
	if err := keySegment(rec.Hash); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("mcpauth: encoding a token record: %w", err)
	}
	if _, err := r.objs.create(ctx, tokenPrefix+rec.Hash, data); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("mcpauth: a record for that hash is already written: %w", ErrConflict)
		}
		return err
	}
	return nil
}

func (r records) LookupByHash(ctx context.Context, hash string) (TokenRecord, error) {
	rec, err := r.lookupRecord(ctx, hash)
	if err != nil {
		return TokenRecord{}, err
	}
	// A record that names no family is not a token of this front door's: the
	// one record written that way is a spent ID token's (M5b, KindIDToken), and
	// it resolves to nothing here rather than reaching the family read below
	// with an empty id. A caller that presented a Firebase ID token as a bearer
	// gets the answer a token that was never minted gets, which is the answer
	// spec.md §8 wants it to get: a Firebase token is never an access token and
	// never a grant, and a lookup that resolved one would be the door reading
	// the identity of a token it did not issue.
	if rec.FamilyID == "" {
		return TokenRecord{}, fmt.Errorf("mcpauth: hash %s: %w", hash, ErrTokenNotFound)
	}
	// The family is read for one reason: revocation is a delete of it, so a
	// record whose family is gone is a record that was revoked, and it has
	// to read as one that never existed.
	if _, _, err := r.GetFamily(ctx, rec.FamilyID); err != nil {
		if errors.Is(err, ErrFamilyNotFound) {
			return TokenRecord{}, fmt.Errorf("mcpauth: hash %s: %w", hash, ErrTokenNotFound)
		}
		return TokenRecord{}, err
	}
	return rec, nil
}

// RotateRefresh is the rotation above as one operation, and it is here rather
// than in the caller for the read that decides it. A caller that looked the
// token up, read the family, compared the hashes and then wrote would be
// deciding on one read of the family and writing against another: a
// legitimate rotation landing between the two halves of that pair leaves the
// caller's own read showing the token it holds as a rotated-away one, and the
// caller revokes the family of a client nobody stole from
// (TestRotateDoesNotRevokeWhenItLosesTheRace). Here the family is read once,
// that read is the whole decision, and the write is matched against the
// generation it returned — so a rotation that lands after the read loses the
// compare-and-swap instead of being mistaken for a theft, and one that landed
// before it is the replay D930 says it is.
//
// The outcomes are one per branch below: the family rotated and comes back;
// the token was already rotated away, so the family is revoked and
// ErrRefreshReplay is returned; the compare-and-swap lost a race with another
// writer, so ErrConflict is returned and the family is left exactly as the
// winner left it; or the family is one no token could be issued from, which is
// refused before the swap rather than after it. A lost race is never a
// revocation, and a refusal never leaves the family half-written.
func (r records) RotateRefresh(ctx context.Context, presentedHash, nextRefreshHash string) (Family, error) {
	rec, err := r.lookupRecord(ctx, presentedHash)
	if err != nil {
		return Family{}, err
	}
	if rec.Kind != KindRefresh {
		return Family{}, fmt.Errorf("mcpauth: a %s token was presented at the refresh grant: %w", rec.Kind, ErrWrongTokenKind)
	}
	fam, gen, err := r.GetFamily(ctx, rec.FamilyID)
	if errors.Is(err, ErrFamilyNotFound) {
		// The record outlived its family by however long the family's delete
		// took: a revoked family holds no token at all, so this is the same
		// absence a lookup reports rather than a family that went missing.
		return Family{}, fmt.Errorf("mcpauth: hash %s: %w", presentedHash, ErrTokenNotFound)
	}
	if err != nil {
		return Family{}, err
	}
	// The family's own two bounds, read before anything of it is inspected
	// (M5b's fourth requirement). A family past either of them is over, and it
	// is answered as a family that is gone rather than as a family that may not
	// rotate: the caller's whole vocabulary for a token whose family is not
	// there is ErrTokenNotFound, and the token endpoint answers that with the
	// terminal invalid_grant a client acts on instead of the 5xx that would
	// invite it to present the same token again.
	//
	// The replay check below is deliberately after this one. A family that has
	// aged is not a family a theft signal is worth raising on — the read that
	// decides is the age — and the revoke the replay would perform is of a
	// family whose tokens are already worthless.
	if fam.aged(r.clock()) {
		return Family{}, fmt.Errorf("mcpauth: family %q: %w", fam.ID, ErrTokenNotFound)
	}
	if presentedHash != fam.RefreshHash {
		// The theft signal: this family rotated past the token being
		// presented. Everything it holds stops working here, and the revoke is
		// reported alongside the replay when it too fails — a caller has to
		// see the replay either way.
		if rerr := r.RevokeFamily(ctx, fam.ID); rerr != nil {
			return Family{}, errors.Join(ErrRefreshReplay, rerr)
		}
		return Family{}, ErrRefreshReplay
	}
	// The family has to be one a token can be issued from *before* the swap
	// below, because the swap is the family's own write and the records of the
	// pair are written after it: a refusal that surfaced once the family had
	// moved would leave it pointing at a refresh hash with no record behind it
	// and the token that was just rotated away a replay, so the client could
	// neither use its tokens nor get the family back
	// (TestARotationRefusesAFamilyThatCouldNotCarryAToken). The check is the
	// family's half of what Token.Validate asks — an identity, a client id and
	// limits that admit a tool; the expiry is the caller's, set from its own
	// instant, and is checked there before this call for the same reason.
	if err := fam.Limits.Validate(); err != nil {
		return Family{}, fmt.Errorf("mcpauth: family %q: %w", fam.ID, err)
	}
	if fam.Identity == "" || fam.ClientID == "" {
		return Family{}, fmt.Errorf("mcpauth: family %q has no identity or no client id: %w", fam.ID, ErrTokenIncomplete)
	}

	next := fam
	next.RefreshHash = nextRefreshHash
	// The idle bound's own field: this rotation is the family being used, and
	// the write below is where that is recorded. It is set from the store's
	// instant, which is the same instant the bounds above were read at, so a
	// rotation cannot be judged at one time and stamped with another.
	next.LastUsedAt = r.clock()
	if err := r.PutFamily(ctx, next, gen); err != nil {
		return Family{}, err
	}
	return next, nil
}

// aged reports whether a family is past either of its two bounds at now: the
// absolute one every family has (FamilyTTL) and the idle one a chain nothing
// has extended runs into (FamilyIdleTTL).
//
// A family the mint has just opened has no LastUsedAt — nothing has rotated it
// — and the idle bound reads CreatedAt for it, because the two are the same
// instant at that point and a zero time read literally would make every family
// as old as the calendar. A CreatedAt that is zero is a record no build of this
// package wrote, and it is refused rather than read as "never": the bounds
// exist to end an exposure, and a family whose age is unknown is one that
// cannot be shown to be inside them.
func (f Family) aged(now time.Time) bool {
	if now.Sub(f.CreatedAt) > FamilyTTL {
		return true
	}
	last := f.LastUsedAt
	if last.IsZero() {
		last = f.CreatedAt
	}
	return now.Sub(last) > FamilyIdleTTL
}

// lookupRecord reads one token record by its hash and nothing else. The family
// it names is left to whoever needs it: RotateRefresh needs it as the read it
// decides and writes on, and a lookup taken before that decision would be a
// second read of the same object, one round trip older than the one the write
// is matched against.
func (r records) lookupRecord(ctx context.Context, hash string) (TokenRecord, error) {
	if err := keySegment(hash); err != nil {
		return TokenRecord{}, err
	}
	var rec TokenRecord
	if err := r.get(ctx, tokenPrefix+hash, &rec); err != nil {
		if errors.Is(err, errMissing) {
			return TokenRecord{}, fmt.Errorf("mcpauth: hash %s: %w", hash, ErrTokenNotFound)
		}
		return TokenRecord{}, err
	}
	return rec, nil
}

func (r records) RevokeFamily(ctx context.Context, familyID string) error {
	if err := keySegment(familyID); err != nil {
		return err
	}
	// Unconditional on purpose: a revoke that lost a race with a rotation
	// would leave the family alive, which is the one outcome a revoke may
	// never have. The rotation's own generation match is what makes the
	// revoke win (Rotate).
	return r.objs.delete(ctx, familyPrefix+familyID)
}

func (r records) PutRequest(ctx context.Context, req Request) error {
	if err := keySegment(req.ID); err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mcpauth: encoding an authorize request: %w", err)
	}
	if _, err := r.objs.create(ctx, requestPrefix+req.ID, data); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("mcpauth: that authorize request is already pending: %w", ErrConflict)
		}
		return err
	}
	return nil
}

func (r records) TakeRequest(ctx context.Context, id string) (Request, error) {
	if keySegment(id) != nil {
		// A value that cannot be one segment of a key names no record this
		// store could hold, so it is answered as a request that is not pending
		// rather than as the store's own error — and that matters here more
		// than anywhere else in this file, because this is the one read a
		// stranger reaches: the consent step takes its request_id straight off
		// a public POST, and an error the server reports about itself would
		// answer 500 where the answer is a page telling the caller its approval
		// carries no binding of a flow this installation is waiting for.
		return Request{}, ErrRequestNotFound
	}
	var req Request
	switch _, err := r.consume(ctx, requestPrefix+id, &req); {
	case errors.Is(err, errMissing):
		return Request{}, ErrRequestNotFound
	case errors.Is(err, errConsumed):
		return Request{}, ErrRequestUsed
	case err != nil:
		return Request{}, err
	}
	return req, nil
}

// GetRequest reads one pending authorize request without spending it. The two
// refusals are the two TakeRequest names — a flow that is not there and a flow
// that was answered — and an id that cannot be one key segment is the first of
// them, for the reason TakeRequest gives: this is the read a public POST
// reaches with a value a stranger typed.
func (r records) GetRequest(ctx context.Context, id string) (Request, error) {
	req, _, err := r.readRequest(ctx, id)
	return req, err
}

// ApproveRequest records a paired device's approval on a pending request, and
// it is a read and a write that have to share their read for the same reason
// every other conditional write here does: the flow the approval is written to
// is the flow the read saw, or the approval belongs to a flow that is gone.
func (r records) ApproveRequest(ctx context.Context, id string, ap Approval) (Request, error) {
	req, gen, err := r.readRequest(ctx, id)
	if err != nil {
		return Request{}, err
	}
	if req.Approval != nil {
		// One flow, one approval: the identity in this record is what the code
		// and the family the flow mints act for, so a second device's act is
		// refused rather than written over the first.
		return Request{}, fmt.Errorf("mcpauth: the authorize request %q: %w", id, ErrRequestApproved)
	}
	req.Approval = &ap
	data, err := json.Marshal(req)
	if err != nil {
		return Request{}, fmt.Errorf("mcpauth: encoding the approved authorize request: %w", err)
	}
	if _, err := r.objs.replace(ctx, requestPrefix+id, data, gen); err != nil {
		if errors.Is(err, errChanged) {
			return Request{}, fmt.Errorf("mcpauth: the authorize request %q changed under the approval: %w", id, ErrConflict)
		}
		return Request{}, err
	}
	return req, nil
}

// readRequest is the read both of the above stand on: one pending request and
// the generation it was read at. A record the consume has already replaced is
// answered as an answered flow, because that is what it is — the marker is a
// record that is there and is not a request any more, and reading it as a
// pending one would be answering a flow twice.
func (r records) readRequest(ctx context.Context, id string) (Request, int64, error) {
	if keySegment(id) != nil {
		return Request{}, 0, ErrRequestNotFound
	}
	data, gen, err := r.objs.read(ctx, requestPrefix+id)
	switch {
	case errors.Is(err, errMissing):
		return Request{}, 0, ErrRequestNotFound
	case err != nil:
		return Request{}, 0, err
	}
	if isConsumed(data) {
		return Request{}, 0, ErrRequestUsed
	}
	var req Request
	if err := decode(data, &req); err != nil {
		return Request{}, 0, fmt.Errorf("%s: %w", requestPrefix+id, err)
	}
	return req, gen, nil
}

func (r records) PutCode(ctx context.Context, c Code) error {
	if err := keySegment(c.Hash); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("mcpauth: encoding an authorization code: %w", err)
	}
	if _, err := r.objs.create(ctx, codePrefix+c.Hash, data); err != nil {
		if errors.Is(err, errChanged) {
			return fmt.Errorf("mcpauth: a code record for that hash is already written: %w", ErrConflict)
		}
		return err
	}
	return nil
}

func (r records) ConsumeCode(ctx context.Context, hash string) (Code, error) {
	if err := keySegment(hash); err != nil {
		return Code{}, err
	}
	var c Code
	spent, err := r.consume(ctx, codePrefix+hash, &c)
	switch {
	case errors.Is(err, errMissing):
		return Code{}, ErrCodeNotFound
	case errors.Is(err, errConsumed):
		// The code was spent by an earlier exchange, and its marker is read
		// for the family that exchange minted, which is the whole of what a
		// replay can act on: the caller revokes it, and the store does not
		// decide that for it. The family is there for a replay that found the
		// marker already written; the exchange that lost the compare-and-swap
		// gets this same error with no marker and therefore no family, and
		// `consume` above says why: the marker is bound after the mint, so
		// there is nothing to read at the moment the race is lost.
		var marker spentRecord
		if len(spent) > 0 {
			if err := json.Unmarshal(spent, &marker); err != nil {
				return Code{}, fmt.Errorf("mcpauth: decoding the spent code %s: %w", hash, err)
			}
		}
		return Code{FamilyID: marker.FamilyID}, ErrCodeUsed
	case err != nil:
		return Code{}, err
	}
	return c, nil
}

// BindCodeFamily writes the family a spent code's first exchange minted into
// the code's own marker, replacing the plain marker consume left.
//
// It is a second write to a record that is already spent, and it is
// generation-matched against the read for the same reason every other write
// here is: two exchanges of one code cannot both reach this call — the consume
// is what makes exactly one of them the winner — but a marker that a caller
// rewrote under another caller's read would still be one record written from
// two places.
func (r records) BindCodeFamily(ctx context.Context, hash, familyID string) error {
	if err := keySegment(hash); err != nil {
		return err
	}
	if err := keySegment(familyID); err != nil {
		return err
	}
	data, gen, err := r.objs.read(ctx, codePrefix+hash)
	if err != nil {
		return err
	}
	if !isConsumed(data) {
		return fmt.Errorf("mcpauth: code %s is not spent, so there is no exchange to bind a family to", hash)
	}
	marker, err := json.Marshal(spentRecord{Consumed: true, FamilyID: familyID})
	if err != nil {
		return fmt.Errorf("mcpauth: encoding the spent code %s: %w", hash, err)
	}
	if _, err := r.objs.replace(ctx, codePrefix+hash, marker, gen); err != nil {
		return err
	}
	return nil
}

// spentRecord is the marker a single-use record is replaced by when it is spent.
// FamilyID is empty for every record but a code: it is what BindCodeFamily adds
// afterwards, and it is the one thing a replay of a spent record is read for.
type spentRecord struct {
	Consumed bool   `json:"consumed"`
	FamilyID string `json:"family_id,omitempty"`
}

// consumedBytes is what a single-use record is replaced by when it is spent. It
// is written rather than deleted so that the second presentation of a code or a
// binding is a refusal that says so, instead of an absence a reader cannot tell
// from a record that never existed — and so that the spend is one
// generation-matched write, which is what makes it single-use under two
// simultaneous callers rather than one write behind a race.
var consumedBytes = []byte(`{"consumed":true}`)

// errConsumed is the record layer's own answer for a single-use record that was
// already spent. TakeRequest and ConsumeCode name it for their own callers, the
// way errMissing is named.
var errConsumed = errors.New("mcpauth: the record was already spent")

// consume reads one single-use record and spends it in the same act: the read
// returns the generation the write below is matched against, so a caller that
// lost a race is answered errConsumed rather than reading a record the winner
// already spent.
//
// The spent bytes come back only when it is this call's own read that found them
// spent, which is the sequential replay. A caller that lost the compare-and-swap
// is answered errConsumed with nothing: the winner's marker is not re-read, and
// the one thing that would make it worth reading — the family a spent code names
// — is not written as part of this write but by BindCodeFamily, after the winner
// has minted. So a second redemption that lost the race is refused as spent and
// refused with no family to revoke, and what the caller does with that is the
// caller's rule (ConsumeCode's own note below).
func (r records) consume(ctx context.Context, key string, into any) ([]byte, error) {
	data, gen, err := r.objs.read(ctx, key)
	if err != nil {
		return nil, err
	}
	if isConsumed(data) {
		return data, errConsumed
	}
	if err := decode(data, into); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if _, err := r.objs.replace(ctx, key, consumedBytes, gen); err != nil {
		if errors.Is(err, errChanged) {
			return nil, errConsumed
		}
		return nil, err
	}
	return nil, nil
}

// isConsumed reports whether an object is the marker consume leaves. It reads
// one field and ignores the rest on purpose: the alternative is a decode that
// has to know which record shape it was looking at, and a spent record is the
// one thing this store reads without knowing what it used to be.
func isConsumed(data []byte) bool {
	var marker struct {
		Consumed bool `json:"consumed"`
	}
	return json.Unmarshal(data, &marker) == nil && marker.Consumed
}

// get reads and decodes one record, answering the backend's own errMissing for
// an object that is not there. Each caller names what it was looking for: the
// same absence is a client that was never registered and a token that was
// never minted, and only the caller knows which it asked for.
func (r records) get(ctx context.Context, key string, into any) error {
	data, _, err := r.objs.read(ctx, key)
	if err != nil {
		return err
	}
	if err := decode(data, into); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// decode reads a record strictly. These objects are written and read by this
// package alone, so a field it does not know is a version skew rather than a
// newer writer: reading it as a record is how a store would come to enforce
// limits it did not understand.
func decode(data []byte, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decoding the record: %w", err)
	}
	return nil
}

// keySegment refuses a value that cannot be one segment of a key. Every id and
// every hash in this store is server-issued hex, and a client id at
// registration is the one value a stranger could name: a segment carrying a
// slash or a dot would be a key that walks out of its own prefix.
func keySegment(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("mcpauth: %q is not a key segment", s)
	}
	return nil
}
