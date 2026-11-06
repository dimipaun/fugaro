// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"fmt"
	"time"
)

// This file is M2's other half: the mint, and the one mutation D930 puts on a
// token family after it exists.
//
// **Shown once.** A mint returns two values and writes neither. What it writes
// is the family and two records holding the sha256 of each value, which is
// what "store only hashes" means as code rather than as a rule: there is no
// path in this package that writes a token value, and there is therefore no
// "show me my token again" path, because that path would need the value at
// rest (D922, spec.md §6).
//
// **Rotation and replay.** A refresh token rotates on every use, and the token
// that was rotated away is kept where it is rather than deleted: presenting it
// again is not a miss but the signature of a theft, and D930's answer is to
// revoke the whole family — one delete, after which neither the thief's token
// nor the legitimate holder's resolves. Rotation is what makes a theft
// detectable rather than merely survivable.
//
// **Rotation is a compare-and-swap**, and that is the whole reason this design
// can live in a bucket. Rotation reads the family, checks the refresh hash and
// writes the replacement, and a revoke landing between the read and the write
// must not be lost: the write matches the generation the read saw, so it fails
// with ErrConflict and the family stays revoked
// (TestARotationUnderAConcurrentRevokeLosesNeither). A database transaction
// gave that away; a bucket has to be asked for it.
//
// The swap is the store's own operation (Store.RotateRefresh) rather than a
// pair of reads composed here, because a legitimate rotation landing between
// two reads taken here would look exactly like a replay — and a replay
// revokes the family. See that method for the window this closes.

// AccessTokenTTL is how long an access token lives. D930 answers "short-lived"
// and names no number, so the number is this mint's own: short enough that a
// leaked access token is worth little, long enough that an ordinary client's
// call pattern is not a rotation on every call. A refresh token has no
// lifetime of its own — rotation and revocation are what bound it.
const AccessTokenTTL = 15 * time.Minute

// Mint issues a new family: one access token, one refresh token, and the
// records of both, with the limits the whole chain will be held to.
//
// identity is what the token acts for (D923, D933) and client is the
// registration it was issued to; both are refused when empty, and the limits
// are put to Limits.Validate here, because a token whose window admits nothing
// or that reaches no tool should fail at the person's keyboard rather than in
// an agent's retry loop (D926, D927).
func Mint(ctx context.Context, s Store, identity string, client Client, limits Limits) (access, refresh string, err error) {
	return MintAt(ctx, s, identity, client, limits, time.Now().UTC())
}

// MintAt is Mint at a caller's own instant, so that a test's arithmetic about
// a token's expiry is the test's own rather than the wall clock's.
func MintAt(ctx context.Context, s Store, identity string, client Client, limits Limits, now time.Time) (access, refresh string, err error) {
	if client.ID == "" {
		return "", "", fmt.Errorf("mcpauth: the client has no id: %w", ErrTokenIncomplete)
	}
	token := Token{
		Identity:  identity,
		ClientID:  client.ID,
		Limits:    limits,
		ExpiresAt: now.Add(AccessTokenTTL),
	}
	if err := token.Validate(); err != nil {
		return "", "", err
	}
	fam, err := newFamily(identity, client.ID, limits, now)
	if err != nil {
		return "", "", err
	}
	access, err = NewValue()
	if err != nil {
		return "", "", err
	}
	refresh, err = NewValue()
	if err != nil {
		return "", "", err
	}
	fam.RefreshHash = TokenHash(refresh)

	// The family first: a family with no record yet is a family nothing can
	// present, where a record with no family would be a token that was
	// already revoked.
	if err := s.PutFamily(ctx, fam, 0); err != nil {
		return "", "", err
	}
	if err := s.PutToken(ctx, accessRecord(fam, access, token)); err != nil {
		return "", "", err
	}
	if err := s.PutToken(ctx, refreshRecord(fam, refresh)); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Rotate exchanges a presented refresh token for a new access token and a new
// refresh token, and revokes the family when the token presented is one that
// was already rotated away. The limits are the family's and are carried
// unchanged: rotation extends a family, and nothing a client sends can widen
// one.
//
// An access token is refused (ErrWrongTokenKind). It is the value an agent
// leaks, and it must not be the value that extends its own family — and the
// refusal is the store's own rotation's, so it is an answer to the refresh
// grant rather than a check every caller of the mint has to remember.
func Rotate(ctx context.Context, s Store, presented string) (access, refresh string, err error) {
	return RotateAt(ctx, s, presented, time.Now().UTC())
}

// RotateAt is Rotate at a caller's own instant, for the same reason MintAt is.
//
// The token the caller presented is exchanged by one call into the store
// (Store.RotateRefresh), which is where the replay decision and the family's
// compare-and-swap have to share a read. What is left here is the arithmetic
// the mint owns: the new pair, the access record's expiry, and the limits,
// which are the family's and are carried unchanged.
//
// The refresh record is written after the family has already been rotated to
// its hash, so a process that died in between leaves a family whose refresh
// token resolves to nothing. The other order — the record first — would leave
// a token that resolves to a family that has moved past it, which is a replay
// and a revocation of a family nobody stole. Of the two, the one that makes a
// client re-authorize is the one to have.
//
// Both halves of the token that is about to be written are therefore settled
// *before* the family moves: the family's half by the store, on the read it
// rotates on, and the expiry here, because it is the one field this call owns.
// A validation failure after the swap would leave the family pointing at a
// refresh hash whose record was never written and the token that was just
// rotated away a replay — no token of the family usable and none of it
// recoverable (TestARotationRefusesAFamilyThatCouldNotCarryAToken,
// TestARotationRefusesAnInstantThatWouldIssueNoExpiry).
func RotateAt(ctx context.Context, s Store, presented string, now time.Time) (access, refresh string, err error) {
	expires := now.Add(AccessTokenTTL)
	if expires.IsZero() {
		return "", "", fmt.Errorf("mcpauth: a rotation at %s would issue an access token with no expiry: %w", now, ErrTokenIncomplete)
	}
	access, err = NewValue()
	if err != nil {
		return "", "", err
	}
	refresh, err = NewValue()
	if err != nil {
		return "", "", err
	}
	fam, err := s.RotateRefresh(ctx, TokenHash(presented), TokenHash(refresh))
	if err != nil {
		return "", "", err
	}

	token := Token{
		Identity:  fam.Identity,
		ClientID:  fam.ClientID,
		Limits:    fam.Limits,
		ExpiresAt: expires,
	}
	if err := token.Validate(); err != nil {
		return "", "", err
	}
	if err := s.PutToken(ctx, accessRecord(fam, access, token)); err != nil {
		return "", "", err
	}
	if err := s.PutToken(ctx, refreshRecord(fam, refresh)); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// newFamily is a family with nothing rotated yet: the client and identity
// pair, and the limits the whole chain carries.
func newFamily(identity, clientID string, limits Limits, now time.Time) (Family, error) {
	id, err := NewID()
	if err != nil {
		return Family{}, err
	}
	return Family{
		ID:        id,
		ClientID:  clientID,
		Identity:  identity,
		Limits:    limits,
		CreatedAt: now,
	}, nil
}

func accessRecord(fam Family, value string, token Token) TokenRecord {
	return TokenRecord{
		Hash:     TokenHash(value),
		FamilyID: fam.ID,
		Kind:     KindAccess,
		Token:    token,
	}
}

// refreshRecord is the refresh half. Its token carries the family's identity,
// client and limits and no expiry: a refresh token is bounded by rotation and
// by revocation and by nothing else (D930).
func refreshRecord(fam Family, value string) TokenRecord {
	return TokenRecord{
		Hash:     TokenHash(value),
		FamilyID: fam.ID,
		Kind:     KindRefresh,
		Token: Token{
			Identity: fam.Identity,
			ClientID: fam.ClientID,
			Limits:   fam.Limits,
		},
	}
}
