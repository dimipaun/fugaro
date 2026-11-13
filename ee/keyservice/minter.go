// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The minter: the half of the service that can sign (§3.2, §3.3). It is a
// second Cloud Run service with no public invoker, it takes a claim set and a
// term, it computes `issued` and `expires` itself, and the whole of its state
// is a mint request it has just served — no store, no mail, no applicant's
// fields (§3.2).
//
// This file is the only one in the package that calls a signer, and
// TestOnlyTheMinterCallsTheSigner holds that: the split T13 rules is an
// identity boundary, and a second file that signs would be a second identity
// that can mint (§3.3, Check 174).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// Environment is which of the service's two pairs this is. The key id is what
// carries it into a key: a staging key's id is in no production keyring, which
// is the whole of Check 179's refusal (§2, §5.4).
type Environment string

const (
	Production Environment = "production"
	Staging    Environment = "staging"
)

// Keys is the two key ids the owner creates (S3.8), one per environment. The
// ids are configuration and not constants because a rotation adds one (§3.3),
// and they name the ring rather than the key's bytes: the private half never
// leaves the managed store (§3.3, .claude/rules/ee.md R5).
type Keys struct {
	Production string
	Staging    string
}

// For answers the key id one environment mints under.
func (k Keys) For(env Environment) (string, error) {
	switch env {
	case Production:
		if k.Production == "" {
			return "", errors.New("the production key id is not configured, and a key minted without one verifies nowhere")
		}
		return k.Production, nil
	case Staging:
		if k.Staging == "" {
			return "", errors.New("the staging key id is not configured")
		}
		return k.Staging, nil
	default:
		return "", fmt.Errorf("the environment %q is neither %s nor %s", env, Production, Staging)
	}
}

// RouteMint is the minter's route for the older format's claims. It is not the
// intake's: the two are separate services and the intake reaches this one by an
// authenticated call (§3.2).
const RouteMint = "/mint"

// RouteMintV1 is the minter's route for a v1 key minted from a row of the
// default policy (D2130, D2133). It is a second route rather than a second shape
// on one, because both bodies decode strictly (CLAUDE.md, "Config and wire
// formats decode strictly") and one path could only tell them apart by trying
// one and then the other.
const RouteMintV1 = "/mint/v1"

// Signer is the minter's one route to a signature: a managed key's signing API.
// Nothing on this side holds a private key to sign with instead, and the
// interface is what keeps that true of every caller (.claude/rules/ee.md, R5).
type Signer interface {
	Sign(ctx context.Context, payload []byte) ([]byte, error)
}

// Request is what the service asks the minter for: the claims a key carries,
// the plan it was minted under, and a term **in days**.
//
// There is no date in it, and TestTheMinterTakesNoDateFromItsCaller holds that.
// `issued` is the minter's own clock and `expires` is `issued` plus the term,
// which is what makes a service-minted key impossible to future-date (§2,
// §9.2): a caller that could name `expires` could mint a key that outlives its
// tier's maximum.
type Request struct {
	Environment  Environment `json:"environment"`
	Tier         string      `json:"tier"`
	Features     []string    `json:"features"`
	Seats        int         `json:"seats"`
	Organisation string      `json:"organisation"`
	Plan         string      `json:"plan"`
	TermDays     int         `json:"term_days"`
}

// Minted is what the minter answers: the key text, which is mailed to the
// applicant and never stored (§7.3), and the record of what it minted, which
// the caller writes with the source the minter does not know.
type Minted struct {
	Key    string `json:"key"`
	Record Record `json:"record"`
}

// Mints is the intake's one route to a key, and the whole of it: the intake
// holds a Mints and never a Signer (Check 174). The minted key is answered here
// and not read from a store, because there is no store of keys.
type Mints interface {
	Mint(ctx context.Context, req Request) (Minted, error)
}

// RequestV1 is what the service asks the minter for when the key is a v1 one:
// the row of the default policy it is issued under, the workspace it binds to,
// and the organisation, seats and term it is for (spec section 3.2).
//
// It carries no policy of its own — no grace, no renewal mode, no warnings —
// because those are the row's (D2133, D2138) and a caller that could name them
// could mint a key that never checks at all. What it names instead is bounded,
// and every bound is the minter's: an end the row's own length caps, a term in
// days the tier's ceiling caps, and a check interval inside `P1D`–`P30D` that
// no row but the air-gapped one may be minted without (F28, F30).
//
// A request that names none of the three leaves them out of the JSON rather
// than carrying their zero values: the two halves are deployed one after the
// other and the minter reads its request strictly, so the zero value of a field
// a minter from before this release has never heard of is a 400 on every mint
// for as long as the deploy takes — the field's own bound is what the minter
// applies to a value that is there, and an absent one is the row's.
//
// The answer carries the same skew the other way, and that one is held by the
// order the two halves are deployed in rather than by anything in this file:
// the intake reads the answer as strictly as the minter reads this request, so
// a record field a newer minter adds is every mint failing until the intake is
// the revision serving. That is why the services root writes the dependency
// from the minter onto the intake — the intake lands first — and why the pair is
// ordered one way and not symmetrically: an intake newer than its minter costs
// nothing, because a field the older answer leaves out is the zero value the
// reader already falls back from (terraform/services/main.tf).
type RequestV1 struct {
	Environment  Environment `json:"environment"`
	Policy       string      `json:"policy"`
	WorkspaceID  string      `json:"workspace_id"`
	Organisation string      `json:"organisation"`
	Seats        int         `json:"seats"`
	// TermDays is the term in days, or 0 for the row's own lifetime. A first
	// purchase is the plan's period; a renewal names the days from now to the
	// end of the term the customer is in, which is longer than a fresh period
	// and still inside the tier's ceiling.
	TermDays int `json:"term_days"`
	// RequestedEnd is the instant the key is to end at, or the zero time for one
	// that runs its term from the minter's own clock. A reveal ends on the
	// predecessor's `paid_through`, which is an instant and not a count of days
	// (D2182, P3.2), and an entitlement's key ends where the customer's term
	// ends. It is a cap and never an extension: the key runs to it when it is
	// inside the row and to the row's own longest otherwise (F30).
	RequestedEnd time.Time `json:"requested_end,omitzero"`
	// CheckInterval is the interval the key checks at, or empty for the row's
	// own. F28 lets an operations mint name one within `P1D`–`P30D`, which is
	// what turns a temporary grant into a key that stops on time; it is refused
	// on the air-gapped row, whose key never contacts the server at all.
	CheckInterval string `json:"check_interval,omitempty"`
	// RenewalSecretHash is the hash the key is to carry in place of a fresh
	// secret's: a reissue under an entitlement that already has one keeps it, so
	// that a seat change or a renewal does not invalidate the secret the
	// installation stored (D2200, amending D2135). Empty is a new secret, which
	// is an issue and a reveal.
	RenewalSecretHash string `json:"renewal_secret_hash,omitempty"`
	// Note is free text a key carries and shows admins only.
	Note string `json:"note"`
}

// MintedV1 is what the minter answers: the key text and the renewal secret,
// which are the two lines of the bundle an issue path shows once and never
// stores (D2135), and the record the caller writes.
//
// The secret is here and nowhere else on this side: only its hash went into the
// key, and a caller that loses it holds a key that can never renew.
type MintedV1 struct {
	Key           string `json:"key"`
	RenewalSecret string `json:"renewal_secret"`
	Record        Record `json:"record"`
}

// MintsV1 is the intake's route to a v1 key, beside Mints and for the same
// reason: the half that is reachable from the internet mints through the
// minter's own service and holds no signer (Check 174).
type MintsV1 interface {
	MintV1(ctx context.Context, req RequestV1) (MintedV1, error)
}

// Minter is the half that signs. It is built once per process and it signs
// through a key whose private half it never holds.
//
// Environment is the minter's own, and not the request's. D459(a) rules one
// project and one key ring for both environments — which is what makes the two
// spellings of a key id a thing that has to be held apart here: a minter that
// took the environment from its caller would sign a production key for whoever
// could reach it, and no test or code would say so. A minter built with the
// environment unset signs nothing at all (§2, §3.3, Check 179).
type Minter struct {
	Signer      Signer
	Keys        Keys
	Environment Environment
	Now         func() time.Time
}

// tierMaxTerm is the longest term a key of each tier may be given, and it is a
// copy of the verifier's own table (ee/licence's `tierMaxTerm`).
//
// It is copied because that package keeps it unexported and the minter must
// refuse what the verifier would: a mint tool that signed a claim set the
// verifier then refused would answer a key nobody can use. What keeps the two
// from drifting is TestTheMinersCeilingsAreTheVerifiers, which finds this
// table's ceiling by asking the minter and holds the verifier to it, rather
// than repeating the numbers here.
//
// The bound is a tier's own term plus T26's 30-day lead, because the longest
// key the design mints is a renewal that lands on the first day it may
// (ee/licence/key.go, `termLead`).
var tierMaxTerm = map[string]time.Duration{
	licence.TierCommunity:  365*24*time.Hour + RenewalLead,
	licence.TierTeam:       365*24*time.Hour + RenewalLead,
	licence.TierEnterprise: 365*24*time.Hour + RenewalLead,
}

// termInDays is a request's term as a duration, refused when it is not a term at
// all or is longer than the tier's ceiling.
//
// The ceiling is compared in days and before any duration is built, which is the
// whole of why this is a function: the term arrives as an int from a wire, and
// `time.Duration(days) * 24 * time.Hour` wraps for a large enough one. A wrapped
// term is a small duration that passes a ceiling it should fail, and what the
// minter would sign is a key whose `paid_through` is before its `issued_at` —
// the one shape of key §2 says the minter's own clock exists to make impossible.
func termInDays(days int, tier string, maximum time.Duration) (time.Duration, error) {
	if days < 1 {
		return 0, fmt.Errorf("a term of %d day(s) is not a term", days)
	}
	if days > int(maximum/(24*time.Hour)) {
		return 0, fmt.Errorf("%w: %d day(s) is longer than the %s tier allows", licence.ErrLifetime, days, tier)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// Mint signs one claim set through the key's API and answers the key text.
//
// The claim set is marshalled once and those bytes are both what is signed and
// what the key carries, so the signature covers exactly what a verifier reads
// (S2.1's own rule; ee/licence/mint/mint.go does the same in the owner's tool).
func (m Minter) Mint(ctx context.Context, req Request) (Minted, error) {
	if req.Environment != m.Environment {
		return Minted{}, fmt.Errorf("this minter signs for the %s environment and the request names %q, and the key id it would sign under is the whole of what keeps the two apart", m.Environment, req.Environment)
	}
	maximum, known := tierMaxTerm[req.Tier]
	if !known {
		return Minted{}, fmt.Errorf("the tier %q is not one a key can be issued for", req.Tier)
	}
	term, err := termInDays(req.TermDays, req.Tier, maximum)
	if err != nil {
		return Minted{}, err
	}
	if req.Seats < 1 {
		return Minted{}, fmt.Errorf("a key for %d seat(s) is one nobody can install", req.Seats)
	}
	if strings.TrimSpace(req.Organisation) == "" {
		return Minted{}, errors.New("a key with no organisation does not verify, so it is not minted")
	}
	keyID, err := m.Keys.For(m.Environment)
	if err != nil {
		return Minted{}, err
	}
	if m.Signer == nil {
		return Minted{}, errors.New("the minter has no signer: the key ring's API is the only way to a signature and it is not configured")
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	issued := now.UTC()
	expires := issued.Add(term)
	claims := licence.Claims{
		Tier:         req.Tier,
		Features:     req.Features,
		Seats:        req.Seats,
		Organisation: req.Organisation,
		Issued:       issued.Unix(),
		Expires:      expires.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return Minted{}, fmt.Errorf("the claim set as the payload a verifier reads: %w", err)
	}
	signature, err := m.Signer.Sign(ctx, payload)
	if err != nil {
		return Minted{}, fmt.Errorf("signing the payload through the key ring's API: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return Minted{}, fmt.Errorf("the key ring answered a %d-byte signature, and one this verifier accepts is %d", len(signature), ed25519.SignatureSize)
	}
	return Minted{
		Key: keyText(keyID, payload, signature),
		Record: Record{
			KeyID:        keyID,
			Plan:         req.Plan,
			Tier:         req.Tier,
			Features:     append([]string(nil), req.Features...),
			Seats:        req.Seats,
			Organisation: req.Organisation,
			Issued:       issued,
			Expires:      expires,
			TermDays:     req.TermDays,
		},
	}, nil
}

// MintV1 signs one v1 key from a row of the default policy and answers the key
// text with the renewal secret beside it.
//
// The claim set is spec section 3.2's payload, and its policy is the row's and
// never the caller's: a request names which row (D2133, D2138) and nothing about
// what a key of it carries — not its grace, not its renewal mode, and not its
// warnings. What the request does name is bounded here, and the bounds are the
// three the design gives the minter: the term in days, because a renewal covers
// the rest of the term the customer is in rather than a fresh one, and a key of
// a length the tier does not allow is refused exactly as the older format
// refuses it; the end, which is capped at the longest key the row mints (F30);
// and the check interval, which must be inside `P1D`–`P30D` and cannot be
// removed from a row whose key checks (F28).
//
// Only a row something renews takes a term of its own: a row nothing renews is
// minted for its own lifetime, since its length is as much the row's as its
// grace is (D2136, D2138).
//
// `issued_at` is the minter's own clock and `paid_through` is that clock plus
// the term, or the end the caller named when that is inside it, which is what
// keeps a caller from dating a key past its tier's maximum (§2, §9.2). The
// renewal secret is generated here, in the same pass, so that the hash in the
// key and the secret on the bundle are one secret minted once (D2135) — unless
// the request keeps an existing hash, which is a reissue under an entitlement
// that already has one and answers no secret at all (D2200).
func (m Minter) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	if req.Environment != m.Environment {
		return MintedV1{}, fmt.Errorf("this minter signs for the %s environment and the request names %q, and the key id it would sign under is the whole of what keeps the two apart", m.Environment, req.Environment)
	}
	policy, known := DefaultPolicy(req.Policy)
	if !known {
		return MintedV1{}, fmt.Errorf("the policy %q is not one a key is issued under", req.Policy)
	}
	maximum, known := tierMaxTerm[policy.Tier]
	if !known {
		return MintedV1{}, fmt.Errorf("the tier %q is not one a key can be issued for", policy.Tier)
	}
	// The one row whose seats are its own: a free seat is one seat, and a request
	// that asks this row for a hundred has not been given them by anything, so it
	// mints a key for nobody rather than a key for a hundred (D2184, D2199).
	if policy.Seats != 0 && req.Seats != policy.Seats {
		return MintedV1{}, fmt.Errorf("the %s row is minted for its own %d seat(s) and the request names %d: this row's seats are the row's (D2184)",
			policy.Name, policy.Seats, req.Seats)
	}
	// F28: an operations mint may name the interval its key checks at, inside the
	// bounds, and the air-gapped row is the one row it may not touch — that key
	// never contacts the server, so an interval on it is a request that has
	// misunderstood which row it is minting from, and the row's null is the whole
	// of what makes its key work in an installation with no route out (section 8).
	if req.CheckInterval != "" {
		if policy.CheckInterval == nil {
			return MintedV1{}, fmt.Errorf("the %s row's key never contacts the server and the request names a check interval of %q: only the air-gapped row has none, and it has none because its installation cannot check (F28)",
				policy.Name, req.CheckInterval)
		}
		if err := checkIntervalInBounds(req.CheckInterval); err != nil {
			return MintedV1{}, fmt.Errorf("the %s row: %w", policy.Name, err)
		}
		interval := req.CheckInterval
		policy.CheckInterval = &interval
	}
	// A row that nothing renews is minted for its own lifetime and for no other:
	// a trial is D2136's fourteen days, and an air-gapped key is replaced by hand
	// when the customer's next year is paid for (section 7), so neither length is
	// a caller's to name. Only a row something renews — the job in the customer's
	// own project, or `fugaro license refresh` — may be given a longer term,
	// because that renewal covers the rest of the term the customer is in rather
	// than a fresh period. A period that is neither is a special deal, and a
	// special deal is the admin page's override of the row rather than a term on
	// this route (sections 12.1, 13; K2.6).
	//
	// An end is not a term in this sense: it is the instant a key's term ends,
	// and a key re-signed under a live entitlement ends where the key it
	// replaces ends whatever row it is of (P3.2). What caps it is the length the
	// key would have been minted for anyway, which is below.
	if req.TermDays != 0 && policy.RenewalMode == licence.RenewalNone && req.TermDays != int(policy.Term/(24*time.Hour)) {
		return MintedV1{}, fmt.Errorf("the %s row runs for its own %d day(s) and the request names %d: nothing renews this row, so its term is the row's and never the caller's (D2136, section 7)",
			policy.Name, int(policy.Term/(24*time.Hour)), req.TermDays)
	}
	days := req.TermDays
	if days == 0 {
		days = int(policy.Term / (24 * time.Hour))
	}
	term, err := termInDays(days, policy.Tier, maximum)
	if err != nil {
		return MintedV1{}, err
	}
	// The term the issuance itself stands for, and what the record's own term is
	// read from: D2202 shortens the key below it and never the entitlement, so
	// this is not `term` — while an end a caller names **is** the entitlement's
	// own and shortens this too (F30, and a reveal hands back a predecessor's
	// `paid_through`, which is where the term it stands for ends).
	stands := time.Duration(days) * 24 * time.Hour
	// D2202: a key of a row whose installation contacts the server runs thirty
	// days at the most, whatever the row is sold for and whatever the caller
	// named, because that key's end is the only thing that ends a rebound
	// installation's access. The air-gapped row keeps its own term.
	if limit := maxKeyTerm(policy); term > limit {
		term = limit
	}
	if req.Seats < 1 {
		return MintedV1{}, fmt.Errorf("a key for %d seat(s) is one nobody can install", req.Seats)
	}
	if strings.TrimSpace(req.Organisation) == "" {
		return MintedV1{}, errors.New("a key with no organisation does not verify, so it is not minted")
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		return MintedV1{}, errors.New("a key with no workspace id is refused by every verifier that reads it (D2134, TN2), so it is not minted")
	}
	kid, err := m.Keys.For(m.Environment)
	if err != nil {
		return MintedV1{}, err
	}
	if m.Signer == nil {
		return MintedV1{}, errors.New("the minter has no signer: the key ring's API is the only way to a signature and it is not configured")
	}
	keyID, err := newKeyID()
	if err != nil {
		return MintedV1{}, err
	}
	secret, secretHash := "", ""
	if req.RenewalSecretHash != "" {
		// D2200: a reissue under an entitlement that already has a secret keeps
		// it, so the installation's stored secret stays the one the server
		// compares and a seat change or a renewal does not lock the holder out
		// of renewing. What a caller may hand back is only a hash of the shape
		// this service writes — which entitlement owns it is the store's to know
		// (P3.6) — and the answer carries no secret, because there is no new one
		// to show: the hash is the holder's own secret's, and it is not this
		// service's to hand out a second time.
		if err := checkSecretHash(req.RenewalSecretHash); err != nil {
			return MintedV1{}, fmt.Errorf("the %s row: %w", policy.Name, err)
		}
		secretHash = req.RenewalSecretHash
	} else if secret, secretHash, err = newRenewalSecret(); err != nil {
		return MintedV1{}, err
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	issued := now.UTC()
	if !req.RequestedEnd.IsZero() {
		// F30: the end a caller names is a cap and never an extension. A reveal
		// hands back the predecessor's `paid_through` and the successor runs to
		// it, which is where the term it has left came from; anything past the
		// row was already capped above. An end the clock has reached is refused
		// rather than signed, because the key it would make is past before
		// anybody reads it.
		end := req.RequestedEnd.UTC()
		if !end.After(issued) {
			return MintedV1{}, errors.New("the end the request names is not after the minter's own clock, and a key minted to it is one that is already past (F30)")
		}
		until := end.Sub(issued)
		if until < term {
			term = until
		}
		if until < stands {
			stands = until
		}
	}
	paidThrough := issued.Add(term)
	claims := licence.ClaimsV1{
		V:                 versionV1,
		KID:               kid,
		KeyID:             keyID,
		WorkspaceID:       req.WorkspaceID,
		Organisation:      req.Organisation,
		Tier:              policy.Tier,
		Seats:             req.Seats,
		IssuedAt:          issued,
		PaidThrough:       paidThrough,
		GraceDays:         policy.GraceDays,
		RenewalMode:       policy.RenewalMode,
		RenewBeforeDays:   policy.RenewBeforeDays,
		CheckInterval:     policy.CheckInterval,
		RenewalSecretHash: secretHash,
		Warnings:          policy.Warnings,
		Kind:              policy.Kind,
		Note:              req.Note,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return MintedV1{}, fmt.Errorf("the claim set as the payload a verifier reads: %w", err)
	}
	signature, err := m.Signer.Sign(ctx, payload)
	if err != nil {
		return MintedV1{}, fmt.Errorf("signing the payload through the key ring's API: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return MintedV1{}, fmt.Errorf("the key ring answered a %d-byte signature, and one this verifier accepts is %d", len(signature), ed25519.SignatureSize)
	}
	return MintedV1{
		Key:           keyTextV1(payload, signature),
		RenewalSecret: secret,
		Record: Record{
			// The key's own id, and not the id of the key ring that signed it:
			// this is what a revocation and a renewal name (K2.2, K2.3), and a
			// reissue is a new key under a new one of these.
			KeyID:        keyID,
			Plan:         policy.Name,
			Tier:         policy.Tier,
			Seats:        req.Seats,
			Organisation: req.Organisation,
			Issued:       issued,
			Expires:      paidThrough,
			// The whole days this issuance stands for, and not the term the key
			// was minted for: what a caller rebuilding a subscription from the
			// record wants is the entitlement, which D2202's cap on the key does
			// not move (S3.5). The rounding up is the package's own: a term that
			// ends a few hours short of the end it stands for is one that lapses
			// inside it.
			TermDays: daysBetween(issued, issued.Add(stands)),
		},
	}, nil
}

// keyText is the key text the design writes: the key id, the payload and the
// signature, all three unpadded base64url, in that order (S2.1). It is the same
// three lines as the owner's mint tool, which this package cannot import
// because that tool is a `main`.
func keyText(keyID string, payload, signature []byte) string {
	return keyID + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// The two spellings of the v1 format this side writes, and they are copies of
// ee/licence's own unexported constants (keyv1.go's `keyPrefixV1` and
// `versionV1`): that package reads the format and this is the only side that
// writes it. What holds the two together is that every key these tests mint is
// read back through licence.VerifyV1, which refuses a text whose prefix or
// version was spelled wrong here.
const (
	keyPrefixV1 = "fgk_"
	versionV1   = 1
)

// keyTextV1 is a v1 key as it is written: the `fgk_` prefix, then the payload
// and the signature, both unpadded base64url (spec section 3.1). The payload is
// the bytes that were signed, so what this writes is the exact text that
// signature covers.
func keyTextV1(payload, signature []byte) string {
	return keyPrefixV1 +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// Handler is the minter's routes: a JSON request in, a JSON minted key out, one
// route per format the service mints.
//
// It authenticates nothing by itself, and that is the design rather than an
// omission: the boundary is the platform's, which admits only the intake's
// service account to this service and verifies the ID token it presents
// (§3.2, §3.3; the Terraform that holds it is S3.5's and Check 174 measures
// it). A second check here would be a second thing to keep right, and the two
// services are one deployment's pair.
func (m Minter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(RouteMint, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if !readRequest(w, r, &req) {
			return
		}
		minted, err := m.Mint(r.Context(), req)
		if err != nil {
			// The refusal's text is the minter's own and it names no claim: a
			// caller here is the intake, which is the service's other half.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAnswer(w, minted)
	})
	mux.HandleFunc(RouteMintV1, func(w http.ResponseWriter, r *http.Request) {
		var req RequestV1
		if !readRequest(w, r, &req) {
			return
		}
		minted, err := m.MintV1(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAnswer(w, minted)
	})
	return mux
}

// readRequest reads one route's body under this service's cap and through the
// strict decoder every wire format here uses, answering false when the body is
// not one the route reads. The refusal is written here because the body is the
// only thing that was wrong, and the route's own refusals are its minter's to
// word.
func readRequest(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "the mint request could not be read", http.StatusBadRequest)
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		http.Error(w, "the mint request is not one this service reads", http.StatusBadRequest)
		return false
	}
	return true
}

// writeAnswer writes one minted key back. What it carries is the minter's own
// answer and never a claim read out of the request.
func writeAnswer(w http.ResponseWriter, minted any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(minted); err != nil {
		http.Error(w, "the minted key could not be answered", http.StatusInternalServerError)
	}
}
