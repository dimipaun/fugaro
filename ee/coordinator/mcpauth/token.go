// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"errors"
	"fmt"
	"time"
)

// Token is what an access token carries (spec.md §6): the identity it acts
// for, the client it was issued to, the limits it is held to, and its expiry.
// There is no session behind it — the 2026-07-28 revision removed
// protocol-level sessions, so every request carries its own bearer token and
// every request is validated from this shape.
//
// It is a value and not a record. What is stored is a hash and never this
// (D930), and the value itself is shown once at mint: the shape is defined
// here, minted by the mint, and read by the resource server's validation on
// every call.
type Token struct {
	// Identity is what the token acts for: the exact `user:` entry of the
	// project's launcher or operator list that a person signed in as (D923,
	// D933), or an unattended client's own identity (D929). Which list entries
	// are read at all is the front door's rule and not this shape's — a
	// `group:`, `domain:` or `serviceAccount:` entry is refused where the
	// identity is resolved.
	Identity string
	// ClientID is the client registration the token was issued to. It is
	// what revocation and rotation are counted by: a refresh token's family
	// is the client's, so a token is never anonymous about who holds it.
	ClientID string
	// Limits are the token's own limits, already narrowed against the
	// project's when the token was minted (Narrow). They are held verbatim:
	// a token is not a second place a limit is decided.
	Limits Limits
	// ExpiresAt is when the token stops being valid. Access tokens are
	// short-lived (D930), and the zero time is refused rather than read as
	// "never": a token with no expiry is one that is never refused for age.
	ExpiresAt time.Time
}

// ErrTokenIncomplete refuses a token shape the front door could never resolve:
// no identity to authorize, no client to count its family by, or no expiry to
// bound it. It is refused at mint, where the reason can be shown to the
// operator, rather than stored and refused on every call.
var ErrTokenIncomplete = errors.New("mcpauth: a token must carry an identity, a client id and an expiry")

// Validate is the mint-time check of the token's shape, and the one place the
// mint asks whether a token may be issued at all: the four things this shape
// needs and the limits' own check (Limits.Validate), which is where a window
// that admits nothing and an unknown scope are refused.
func (t Token) Validate() error {
	switch {
	case t.Identity == "":
		return fmt.Errorf("mcpauth: identity is empty: %w", ErrTokenIncomplete)
	case t.ClientID == "":
		return fmt.Errorf("mcpauth: client id is empty: %w", ErrTokenIncomplete)
	case t.ExpiresAt.IsZero():
		return fmt.Errorf("mcpauth: expiry is unset: %w", ErrTokenIncomplete)
	}
	return t.Limits.Validate()
}
