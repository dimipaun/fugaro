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

// RouteMint is the minter's one route. It is not the intake's: the two are
// separate services and the intake reaches this one by an authenticated call
// (§3.2).
const RouteMint = "/mint"

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

// Minter is the half that signs. It is built once per process and it signs
// through a key whose private half it never holds.
type Minter struct {
	Signer Signer
	Keys   Keys
	Now    func() time.Time
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

// Mint signs one claim set through the key's API and answers the key text.
//
// The claim set is marshalled once and those bytes are both what is signed and
// what the key carries, so the signature covers exactly what a verifier reads
// (S2.1's own rule; ee/licence/mint/mint.go does the same in the owner's tool).
func (m Minter) Mint(ctx context.Context, req Request) (Minted, error) {
	maximum, known := tierMaxTerm[req.Tier]
	if !known {
		return Minted{}, fmt.Errorf("the tier %q is not one a key can be issued for", req.Tier)
	}
	if req.TermDays < 1 {
		return Minted{}, fmt.Errorf("a term of %d day(s) is not a term", req.TermDays)
	}
	term := time.Duration(req.TermDays) * 24 * time.Hour
	if term > maximum {
		return Minted{}, fmt.Errorf("%w: %d day(s) is longer than the %s tier allows", licence.ErrLifetime, req.TermDays, req.Tier)
	}
	if req.Seats < 1 {
		return Minted{}, fmt.Errorf("a key for %d seat(s) is one nobody can install", req.Seats)
	}
	if strings.TrimSpace(req.Organisation) == "" {
		return Minted{}, errors.New("a key with no organisation does not verify, so it is not minted")
	}
	keyID, err := m.Keys.For(req.Environment)
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

// keyText is the key text the design writes: the key id, the payload and the
// signature, all three unpadded base64url, in that order (S2.1). It is the same
// three lines as the owner's mint tool, which this package cannot import
// because that tool is a `main`.
func keyText(keyID string, payload, signature []byte) string {
	return keyID + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// Handler is the minter's one route: a JSON Request in, a JSON Minted out.
//
// It authenticates nothing by itself, and that is the design rather than an
// omission: the boundary is the platform's, which admits only the intake's
// service account to this service and verifies the ID token it presents
// (§3.2, §3.3; the Terraform that holds it is S3.5's and Check 174 measures
// it). A second check here would be a second thing to keep right, and the two
// services are one deployment's pair.
func (m Minter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err != nil {
			http.Error(w, "the mint request could not be read", http.StatusBadRequest)
			return
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		var req Request
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "the mint request is not one this service reads", http.StatusBadRequest)
			return
		}
		minted, err := m.Mint(r.Context(), req)
		if err != nil {
			// The refusal's text is the minter's own and it names no claim: a
			// caller here is the intake, which is the service's other half.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(minted); err != nil {
			http.Error(w, "the minted key could not be answered", http.StatusInternalServerError)
		}
	})
}
