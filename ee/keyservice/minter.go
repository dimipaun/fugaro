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
// It carries no policy of its own — no grace, no renewal mode, no check
// interval — because those are the row's (D2133, D2138) and a caller that could
// name them could mint a key that never checks at all. It carries no date
// either, for the reason the older request carries none.
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
		},
	}, nil
}

// MintV1 signs one v1 key from a row of the default policy and answers the key
// text with the renewal secret beside it.
//
// The claim set is spec section 3.2's payload, and its policy is the row's and
// never the caller's: a request names which row (D2133, D2138) and nothing about
// what a key of it carries. What the request does name is the term in days,
// because a renewal covers the rest of the term the customer is in rather than a
// fresh one, and a key of a length the tier does not allow is refused exactly as
// the older format refuses it. Only a row something renews takes that term: a
// row nothing renews is minted for its own lifetime, since its length is as much
// the row's as its grace is (D2136, D2138).
//
// `issued_at` and `paid_through` are the minter's own clock and that term, which
// is what keeps a caller from dating a key past its tier's maximum (§2, §9.2).
// The renewal secret is generated here, in the same pass, so that the hash in
// the key and the secret on the bundle are one secret minted once (D2135).
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
	// A row that nothing renews is minted for its own lifetime and for no other:
	// a trial is D2136's fourteen days, and an air-gapped key is replaced by hand
	// when the customer's next year is paid for (section 7), so neither length is
	// a caller's to name. Only a row something renews — the job in the customer's
	// own project, or `fugaro license refresh` — may be given a longer term,
	// because that renewal covers the rest of the term the customer is in rather
	// than a fresh period. A period that is neither is a special deal, and a
	// special deal is the admin page's override of the row rather than a term on
	// this route (sections 12.1, 13; K2.6).
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
	secret, secretHash, err := newRenewalSecret()
	if err != nil {
		return MintedV1{}, err
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	issued := now.UTC()
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
