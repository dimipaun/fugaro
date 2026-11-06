// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M2's store tests: what a presented token resolves to, what a revoked family
// does to every token in it, and the one race a bucket has to be asked for a
// generation match to win.
//
// They run against internal/gcpfake rather than an in-memory double, so the
// compare-and-swap under TestARotationUnderAConcurrentRevokeLosesNeither is
// the real one: the fake answers ifGenerationMatch, and a store that quietly
// turned a conditional write into a read-modify-write would fail here
// (implementation-plan.md §6).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLookupFindsByThePresentedToken(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	access, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	for _, tc := range []struct {
		name  string
		value string
		kind  TokenKind
	}{
		{"access", access, KindAccess},
		{"refresh", refresh, KindRefresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := s.LookupByHash(ctx, TokenHash(tc.value))
			if err != nil {
				t.Fatalf("LookupByHash: %v", err)
			}
			if rec.Kind != tc.kind {
				t.Errorf("Kind = %q; want %q", rec.Kind, tc.kind)
			}
			if rec.Token.Identity != "user:person-1" {
				t.Errorf("Identity = %q; want %q", rec.Token.Identity, "user:person-1")
			}
			if rec.Token.ClientID != "client-1" {
				t.Errorf("ClientID = %q; want %q", rec.Token.ClientID, "client-1")
			}
			if rec.Token.Limits.SpendUSD != 5 {
				t.Errorf("SpendUSD = %v; want 5", rec.Token.Limits.SpendUSD)
			}
		})
	}

	if _, err := s.LookupByHash(ctx, TokenHash("marker-a-token-that-was-never-minted")); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("LookupByHash of an unknown hash = %v; want ErrTokenNotFound", err)
	}
}

func TestRevokedTokenIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	access, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec, err := s.LookupByHash(ctx, TokenHash(access))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if err := s.RevokeFamily(ctx, rec.FamilyID); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	for _, value := range []string{access, refresh} {
		if _, err := s.LookupByHash(ctx, TokenHash(value)); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("LookupByHash of a revoked token = %v; want ErrTokenNotFound", err)
		}
	}
	// One delete took the family and everything under it, so a second revoke
	// of the same family is not an error: an operator revoking twice is an
	// operator doing the right thing twice.
	if err := s.RevokeFamily(ctx, rec.FamilyID); err != nil {
		t.Errorf("RevokeFamily twice = %v; want nil", err)
	}
}

// TestARotationUnderAConcurrentRevokeLosesNeither is the generation-matched
// write a bucket-backed store stands on. A rotation reads the family and then
// compares-and-swaps it; a revoke landing between those two steps must not be
// lost, and the rotation must not resurrect the family it just lost a race
// with. On a database a transaction gave this away for free; here it is the
// precondition, and this test is what says the precondition is read.
func TestARotationUnderAConcurrentRevokeLosesNeither(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	_, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec, err := s.LookupByHash(ctx, TokenHash(refresh))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	fam, gen, err := s.GetFamily(ctx, rec.FamilyID)
	if err != nil {
		t.Fatalf("GetFamily: %v", err)
	}

	// The revoke lands here: after the rotation has read the family and
	// before its write, which is exactly the window a generation match
	// closes.
	if err := s.RevokeFamily(ctx, fam.ID); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	fam.RefreshHash = TokenHash("marker-the-next-refresh-token")
	if err := s.PutFamily(ctx, fam, gen); !errors.Is(err, ErrConflict) {
		t.Errorf("PutFamily against a revoked family = %v; want ErrConflict", err)
	}
	if _, _, err := s.GetFamily(ctx, fam.ID); !errors.Is(err, ErrFamilyNotFound) {
		t.Errorf("GetFamily after the lost rotation = %v; want ErrFamilyNotFound: the rotation resurrected a revoked family", err)
	}
	if _, err := s.LookupByHash(ctx, TokenHash(refresh)); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("LookupByHash after the lost rotation = %v; want ErrTokenNotFound", err)
	}
}

// racingObjects is a store's backend with one hook: after the chosen key is
// read, a competing rotation is run to completion before the bytes that read
// asked for are handed back. It is how the window a rotation has between
// reading a family and writing it is made deterministic —
// TestRotateDoesNotRevokeWhenItLosesTheRace is what it is for.
//
// The race is an interleave and not two goroutines because the outcome this
// test relies on is not the same under every schedule: a rotation that
// completes before the loser *reads* the family is a replay by D930's rule and
// belongs to the error-path tests, where a rotation landing between the read
// and the write belongs here. A goroutine test would be red or green
// depending on the machine rather than on the code.
type racingObjects struct {
	objects
	at    string
	race  func()
	fired bool
}

func (o *racingObjects) read(ctx context.Context, key string) ([]byte, int64, error) {
	data, gen, err := o.objects.read(ctx, key)
	if err == nil && key == o.at && !o.fired {
		o.fired = true
		o.race()
	}
	return data, gen, err
}

// TestRotateDoesNotRevokeWhenItLosesTheRace is the finding this test was
// written for: the read that decides a rotation is a replay has to be the read
// the write is matched against, or a legitimate rotation landing between the
// two halves of that pair revokes the family of a client nobody stole from.
//
// The loser here holds a refresh token that is current when it reads the
// family and is not current when it writes. It loses the compare-and-swap, and
// that is all it may do: ErrConflict out, family alive.
func TestRotateDoesNotRevokeWhenItLosesTheRace(t *testing.T) {
	ctx := context.Background()
	b := newFakeBucket(t)

	// The store the competing rotation runs through. Same bucket, so both
	// rotations are real writes to the same objects and the compare-and-swap
	// between them is the fake's ifGenerationMatch.
	inner := newRecords(bucketObjects{b: b})
	_, refresh, err := Mint(ctx, inner, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec, err := inner.LookupByHash(ctx, TokenHash(refresh))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}

	raced := &racingObjects{objects: bucketObjects{b: b}, at: familyPrefix + rec.FamilyID}
	raced.race = func() {
		// The other client, doing nothing wrong: it holds the same refresh
		// token, it got there first, and it rotates it.
		if _, _, err := RotateAt(ctx, inner, refresh, time.Now().UTC()); err != nil {
			t.Errorf("the competing rotation: %v", err)
		}
	}
	s := newRecords(raced)

	if _, _, err := RotateAt(ctx, s, refresh, time.Now().UTC()); !errors.Is(err, ErrConflict) {
		t.Errorf("RotateAt into a concurrent rotation = %v; want ErrConflict", err)
	}

	// The family is where the competing rotation left it, and alive: a lost
	// race is a lost race, and a revoke here would have taken a family from a
	// client that did nothing but arrive second.
	fam, _, err := inner.GetFamily(ctx, rec.FamilyID)
	if err != nil {
		t.Fatalf("the family of a rotation that lost its race: %v", err)
	}
	if fam.RefreshHash == TokenHash(refresh) {
		t.Error("the family still holds the token the winner rotated away")
	}

	// Where the boundary is, stated here rather than left to be discovered:
	// the record of the rotated-away token is kept, so this same request made
	// again reads the family and finds it has moved past the token it holds —
	// and that is a replay, and D930's answer to a replay is the family
	// revoked. The one presentation that was current when it looked is the one
	// the conflict above protects; presenting it again after being told is
	// presenting a token known to be rotated away, which no store can tell
	// from a theft (TestRefreshRotatesAndReplayRevokesTheFamily).
	if _, err := inner.LookupByHash(ctx, TokenHash(refresh)); err != nil {
		t.Errorf("the record of the token the loser presented = %v; want it kept: it is what a replay is recognized by", err)
	}
	if _, _, err := RotateAt(ctx, s, refresh, time.Now().UTC()); !errors.Is(err, ErrRefreshReplay) {
		t.Errorf("the loser presenting the same token again = %v; want ErrRefreshReplay", err)
	}
}

// TestTheStoreKeepsOnlyTheClientSecretHash holds the registration record to
// the same rule a token record is held to: the secret is returned once, at
// registration, and what is at rest is its hash.
func TestTheStoreKeepsOnlyTheClientSecretHash(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	secret := "marker-a-client-secret-value-never-stored"
	client := testClient()
	client.SecretHash = TokenHash(secret)
	if err := s.PutClient(ctx, client); err != nil {
		t.Fatalf("PutClient: %v", err)
	}

	got, err := s.GetClient(ctx, client.ID)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if got.SecretHash != TokenHash(secret) {
		t.Errorf("SecretHash = %q; want the secret's hash", got.SecretHash)
	}
	if got.Name != client.Name || len(got.RedirectURIs) != 1 {
		t.Errorf("GetClient returned %+v; want the registered client", got)
	}
	for key, data := range s.objects(t) {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s holds the client secret", key)
		}
	}
	if err := s.PutClient(ctx, client); !errors.Is(err, ErrConflict) {
		t.Errorf("PutClient twice = %v; want ErrConflict: a client id is issued once", err)
	}
	if _, err := s.GetClient(ctx, "no-such-client"); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("GetClient of an unknown id = %v; want ErrClientNotFound", err)
	}
	// A client id is the one value in this store a stranger could name, so a
	// segment that could walk out of its own prefix is refused rather than
	// escaped.
	if _, err := s.GetClient(ctx, "../../families/x"); err == nil {
		t.Error("GetClient accepted an id that is not a key segment")
	}
}

// TestTheStoreRefusesARecordItDoesNotUnderstand is the strict-decoding half:
// the objects are written and read by this package alone, so a field it does
// not know is a version skew, and reading past one would be enforcing limits
// this build does not understand.
func TestTheStoreRefusesARecordItDoesNotUnderstand(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	if _, _, err := Mint(ctx, s, "user:person-1", testClient(), testLimits()); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// A field a later version added, planted under a hash this build looks
	// up: the record has to be refused rather than read without it.
	hash := TokenHash("marker-a-token-whose-record-came-from-a-later-build")
	planted := fmt.Sprintf(`{"hash":%q,"family_id":"x","kind":"access","token":{},"scopes_of_a_later_version":["tasks:read"]}`, hash)
	if err := s.bucket.Put(ctx, tokenPrefix+hash, []byte(planted), "application/json"); err != nil {
		t.Fatalf("planting the record: %v", err)
	}
	_, err := s.LookupByHash(ctx, hash)
	if err == nil {
		t.Fatal("LookupByHash read a record carrying a field it does not know")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("LookupByHash = %v; want the decode's own refusal of the field", err)
	}
}
