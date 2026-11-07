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

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/rigor"
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

// TestAConsumeThatLosesTheRaceIsSpent is the second half of the same
// precondition, over the single-use records M5 adds: the generation-matched
// replace is what makes one binding and one code answer exactly one caller, and
// the branch that turns a lost compare-and-swap into errConsumed is the whole of
// it. Without that branch a loser's error is the backend's, and the caller that
// reads it as "not mine" — TakeRequest answers ErrRequestNotFound and ConsumeCode
// ErrCodeNotFound — would tell a person their own approval names no pending flow
// while the record it names is spent, and a code exchange is refused as a code
// this installation never issued.
//
// The race is the same deterministic interleave racingObjects was written for,
// and the competing caller is a real one: the second store is over the same
// bucket, so the spend it makes is the fake's own ifGenerationMatch write and
// the loser's replace is the one that loses it.
func TestAConsumeThatLosesTheRaceIsSpent(t *testing.T) {
	ctx := context.Background()
	b := newFakeBucket(t)
	inner := newRecords(bucketObjects{b: b})

	requestID, err := NewValue()
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	if err := inner.PutRequest(ctx, Request{
		ID:          requestID,
		Number:      "481920",
		ClientID:    "client-1",
		RedirectURI: testClientRedirect,
		Challenge:   testChallenge(),
		State:       testState,
		Scopes:      []string{ScopeTasksRead},
		ExpiresAt:   testNow.Add(RequestTTL),
	}); err != nil {
		t.Fatalf("PutRequest: %v", err)
	}
	codeHash := TokenHash("a-code-this-installation-issued")
	if err := inner.PutCode(ctx, Code{
		Hash:        codeHash,
		ClientID:    "client-1",
		RedirectURI: testClientRedirect,
		Challenge:   testChallenge(),
		Identity:    "user:person-1",
		Scopes:      []string{ScopeTasksRead},
		ExpiresAt:   testNow.Add(CodeTTL),
	}); err != nil {
		t.Fatalf("PutCode: %v", err)
	}

	for _, tc := range []struct {
		what string
		key  string
		// spend is the read-and-spend under test, run once by the competing
		// caller (which wins it) and once by the loser.
		spend func(s Store) error
		want  error
	}{
		{
			what:  "an approval",
			key:   requestPrefix + requestID,
			spend: func(s Store) error { _, err := s.TakeRequest(ctx, requestID); return err },
			want:  ErrRequestUsed,
		},
		{
			what:  "a code exchange",
			key:   codePrefix + codeHash,
			spend: func(s Store) error { _, err := s.ConsumeCode(ctx, codeHash); return err },
			want:  ErrCodeUsed,
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			raced := &racingObjects{objects: bucketObjects{b: b}, at: tc.key}
			raced.race = func() {
				// The other caller holds the same binding, or presents the same
				// code, and got there first. Its spend is the real one.
				if err := tc.spend(inner); err != nil {
					t.Errorf("the competing read of %s: %v", tc.what, err)
				}
			}

			err := tc.spend(newRecords(raced))
			if !errors.Is(err, tc.want) {
				t.Errorf("the read of %s that lost the race = %v; want %v: a lost compare-and-swap is a record that is spent, not one the store cannot read", tc.what, err, tc.want)
			}

			// And it really was spent by the other caller, so the refusal above
			// is about this record rather than about the race alone: the same
			// read made again is refused the same way.
			if err := tc.spend(inner); !errors.Is(err, tc.want) {
				t.Errorf("the second read of a spent %s = %v; want %v", tc.what, err, tc.want)
			}
		})
	}
}

// TestBindCodeFamilyBindsOnlyASpentCode is the guard on the one write this
// package makes to a record that is already spent. The family a code's first
// exchange minted is written into the marker the consume left behind, and it is
// what a replay of that code is read for (D930): a code whose family was bound
// before an exchange spent it would carry a family no exchange made, so two
// presentations of one code would be read as one family's rather than as the
// replay the second one is.
func TestBindCodeFamilyBindsOnlyASpentCode(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	codeHash := TokenHash("a-code-this-installation-issued")
	if err := s.PutCode(ctx, Code{
		Hash:        codeHash,
		ClientID:    "client-1",
		RedirectURI: testClientRedirect,
		Challenge:   testChallenge(),
		Identity:    "user:person-1",
		Scopes:      []string{ScopeTasksRead},
		ExpiresAt:   testNow.Add(CodeTTL),
	}); err != nil {
		t.Fatalf("PutCode: %v", err)
	}

	if err := s.BindCodeFamily(ctx, codeHash, "family-1"); err == nil {
		t.Error("BindCodeFamily bound a family to a code no exchange had spent")
	}

	// A code that has been spent takes the family, which is the write the
	// exchange makes after it wins the consume.
	if _, err := s.ConsumeCode(ctx, codeHash); err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	if err := s.BindCodeFamily(ctx, codeHash, "family-1"); err != nil {
		t.Fatalf("BindCodeFamily of a spent code: %v", err)
	}

	// Read back the way a replay reads it: the refusal carries the family, and
	// the bind is where that family comes from.
	replayed, err := s.ConsumeCode(ctx, codeHash)
	if !errors.Is(err, ErrCodeUsed) {
		t.Fatalf("the second ConsumeCode = %v; want ErrCodeUsed", err)
	}
	if replayed.FamilyID != "family-1" {
		t.Errorf("the family a replayed code names = %q; want %q", replayed.FamilyID, "family-1")
	}
}

// TestARevokeTheStoreRefusesIsNotARevoke is D930's one unconditional write
// held to the answer the read path already refuses to fold. gocloud's GCS
// driver reports an HTTP 403 as NotFound — the fold ReadStrict exists to undo
// — so a delete refused for lack of permission and a delete of an object that
// is not there arrive here as the same thing. A revoke that read that as
// "already gone" would report success while the family is still alive, and in
// the replay path that is the theft revocation failing silently.
func TestARevokeTheStoreRefusesIsNotARevoke(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	_, refused, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, allowed, err := Mint(ctx, s, "user:person-2", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec, err := s.LookupByHash(ctx, TokenHash(refused))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	other, err := s.LookupByHash(ctx, TokenHash(allowed))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}

	// One family's record is behind a refusal: every write to it is a 403,
	// which is what a retention policy, a hold, a VPC-SC perimeter or a
	// mistyped IAM change answers. The other family is not, so the contrast
	// below is a refusal and not a store that stopped working.
	s.gcs.DenyWrites(testBucket(), familyPrefix+rec.FamilyID)

	if err := s.RevokeFamily(ctx, rec.FamilyID); err == nil {
		t.Error("RevokeFamily reported success while the store refused the delete")
	} else if !errors.Is(err, blobx.ErrForbidden) {
		t.Errorf("RevokeFamily = %v; want it to carry %v, as the strict read carries a refused read", err, blobx.ErrForbidden)
	}

	// The family is still there, which is the whole reason the success was a
	// lie: its tokens keep resolving.
	if _, _, err := s.GetFamily(ctx, rec.FamilyID); err != nil {
		t.Errorf("the family a refused revoke reported revoking = %v; want it still there", err)
	}
	if _, err := s.LookupByHash(ctx, TokenHash(refused)); err != nil {
		t.Errorf("a token of the family a refused revoke reported revoking = %v; want it still valid", err)
	}

	// A family the store is allowed to delete still goes, and a second revoke
	// of it is still a success: the refusal above is told apart from the
	// absence, not folded into it.
	if err := s.RevokeFamily(ctx, other.FamilyID); err != nil {
		t.Fatalf("RevokeFamily of a family the store may delete: %v", err)
	}
	if err := s.RevokeFamily(ctx, other.FamilyID); err != nil {
		t.Errorf("RevokeFamily of a family already revoked = %v; want nil", err)
	}
}

// TestARotationRefusesAFamilyThatCouldNotCarryAToken is the ordering of the
// rotation's own two writes. The family is written first and the records of
// the pair after it, so a family no token could be issued from has to be
// refused before that write: a refusal arriving after it would leave the
// family pointing at a refresh hash with no record behind it and the token
// that was just rotated away a replay, and the client would hold a family it
// could neither use nor get back.
func TestARotationRefusesAFamilyThatCouldNotCarryAToken(t *testing.T) {
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

	// A family whose limits admit no tool, as one written before a vocabulary
	// changed, or by hand, would be. It is a record this build can read and
	// cannot issue from, which is exactly the family the rotation must not
	// write.
	fam.Limits = Limits{MinRigor: rigor.POC, MaxRigor: rigor.Pro}
	if err := s.PutFamily(ctx, fam, gen); err != nil {
		t.Fatalf("PutFamily: %v", err)
	}

	if _, _, err := Rotate(ctx, s, refresh); !errors.Is(err, ErrNoScope) {
		t.Errorf("Rotate of a family that could not carry a token = %v; want ErrNoScope", err)
	}

	// Nothing was orphaned: the family still holds the token the client has,
	// so that token is still the current one and still rotates once the family
	// can carry one again.
	held, heldGen, err := s.GetFamily(ctx, rec.FamilyID)
	if err != nil {
		t.Fatalf("GetFamily: %v", err)
	}
	if held.RefreshHash != TokenHash(refresh) {
		t.Error("the family moved to a refresh hash with no record behind it")
	}
	held.Limits = testLimits()
	if err := s.PutFamily(ctx, held, heldGen); err != nil {
		t.Fatalf("PutFamily: %v", err)
	}
	if _, _, err := Rotate(ctx, s, refresh); err != nil {
		t.Errorf("Rotate of the same token once the family can carry one = %v; want it to rotate", err)
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
