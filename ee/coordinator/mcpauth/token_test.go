// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// mintable is the token shape every case below varies one field of: an
// identity a project's `user:` list could hold, the client it was issued to,
// limits that pass their own check, and an expiry.
func mintable() mcpauth.Token {
	return mcpauth.Token{
		Identity:  "user:person-1",
		ClientID:  "client-1",
		ExpiresAt: time.Unix(1_800_000_000, 0),
		Limits: mcpauth.Limits{
			MinRigor: rigor.POC,
			MaxRigor: rigor.Pro,
			SpendUSD: 5,
			Scopes:   []string{mcpauth.ScopeTasksRead},
		},
	}
}

// TestTokenCarriesTheLimitsItWasMintedWith pins the token's half of "only
// narrows": the limits a token carries are the ones Narrow resolved, held
// verbatim, so the token is not a second place a limit is decided and a
// reader of the token reads the project's answer rather than the request's.
func TestTokenCarriesTheLimitsItWasMintedWith(t *testing.T) {
	tok := mintable()
	narrowed, err := tok.Limits.Narrow(mcpauth.Limits{MaxRigor: rigor.Startup, SpendUSD: 2})
	if err != nil {
		t.Fatalf("Narrow: %v", err)
	}
	tok.Limits = narrowed

	if tok.Limits.MaxRigor != rigor.Startup || tok.Limits.SpendUSD != 2 {
		t.Fatalf("the token carries %+v; want the narrowed limits", tok.Limits)
	}
	if err := tok.Validate(); err != nil {
		t.Errorf("Validate() = %v; want ok", err)
	}
}

// TestTokenValidateRefusesWhatMintCannotIssue pins the mint-time check of the
// token's shape: a token with no identity, no client or no expiry is one the
// front door could never resolve, and it is refused where it is written
// rather than stored and refused on every call.
//
// The expiry is the shape's own bound and not a clock's: Short-lived access
// tokens are D930's, and a check that read the clock would make this test
// and the mint that calls it disagree at the second boundary.
func TestTokenValidateRefusesWhatMintCannotIssue(t *testing.T) {
	if err := mintable().Validate(); err != nil {
		t.Fatalf("Validate() = %v; want ok", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*mcpauth.Token)
		field  string
	}{
		{"no identity", func(t *mcpauth.Token) { t.Identity = "" }, "identity"},
		{"no client", func(t *mcpauth.Token) { t.ClientID = "" }, "client id"},
		{"no expiry", func(t *mcpauth.Token) { t.ExpiresAt = time.Time{} }, "expiry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok := mintable()
			tc.mutate(&tok)
			err := tok.Validate()
			if !errors.Is(err, mcpauth.ErrTokenIncomplete) {
				t.Fatalf("Validate() = %v; want ErrTokenIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("Validate() = %q; want it to name %s", err, tc.field)
			}
		})
	}

	// The limits are the token's own check, not a second copy of it: a token
	// whose limits admit nothing is refused by the same rule that refuses
	// them at mint.
	tok := mintable()
	tok.Limits.MinRigor, tok.Limits.MaxRigor = rigor.Critical, rigor.Pro
	if err := tok.Validate(); !errors.Is(err, mcpauth.ErrEmptyWindow) {
		t.Errorf("Validate() with an empty window = %v; want ErrEmptyWindow", err)
	}
}
