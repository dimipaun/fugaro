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
	// expirable, and neither bound is implemented in this package. Both are
	// M7's (implementation-plan.md, the note in its entry), because M7 is the
	// task that makes the endpoint reachable from a real binary; until then
	// the exposure is latent and the record carries the timestamp the
	// lifecycle will need.
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
}

// TokenKind says which half of a family's pair a record is.
type TokenKind string

const (
	// KindAccess is a short-lived access token: the value a request carries
	// and the resource server validates (spec.md §6).
	KindAccess TokenKind = "access"
	// KindRefresh is a refresh token: the long-lived half, whose lifetime is
	// the family's rotation and revocation rather than an expiry.
	KindRefresh TokenKind = "refresh"
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
// would be an approval to send a code somewhere the person was never shown. The
// identity is the one thing that is not here — it is what the approval's ID
// token attests, so it is settled when the approval arrives and read by the
// handler that writes the code.
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
type records struct{ objs objects }

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
	if err := r.PutFamily(ctx, next, gen); err != nil {
		return Family{}, err
	}
	return next, nil
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
	if err := keySegment(id); err != nil {
		return Request{}, err
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
		// The code was spent by an earlier exchange. Its marker is read for
		// the family that exchange minted, which is the whole of what a
		// replay can act on: the caller revokes it, and the store does not
		// decide that for it.
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
// already spent. It answers the spent bytes on that refusal, because a caller
// that lost the race is looking at the winner's marker and not at the record.
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
