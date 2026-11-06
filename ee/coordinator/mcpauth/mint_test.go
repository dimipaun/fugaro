// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M2's mint tests: what a mint writes, and what a token family does when its
// refresh token is used twice.
//
// The first test is the task's red step and the whole reason the store left
// the project's database: the two values Mint returns are shown once and are
// never at rest, so every object under the store's prefix is read back and
// compared against them.

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// testLimits is a token's limits for these tests: a window, a spend cap and
// the two scopes of v1, so a minted token's record carries every field.
func testLimits() Limits {
	return Limits{
		MinRigor: rigor.POC,
		MaxRigor: rigor.Pro,
		Hotfix:   true,
		SpendUSD: 5,
		Scopes:   []string{ScopeTasksRead, ScopeTasksWrite},
	}
}

// testClient is one registration, secret hash and all, so the mint has a
// client to count a family by.
func testClient() Client {
	return Client{
		ID:           "client-1",
		Name:         "an editor",
		RedirectURIs: []string{"http://127.0.0.1:7777/callback"},
		SecretHash:   TokenHash("client-secret-not-a-real-one"),
		CreatedAt:    time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
	}
}

// TestMintStoresOnlyTheHash is the red step of M2 and the rule the whole task
// exists for: a token value is shown once at mint and is never written. Every
// object the store holds is read back and compared against both values, whole
// and by prefix, so a record that kept "the first half" of a token fails here
// rather than in a review.
func TestMintStoresOnlyTheHash(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	access, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if access == "" || refresh == "" {
		t.Fatal("Mint returned an empty token: the test would then prove nothing")
	}
	if access == refresh {
		t.Fatal("Mint returned the same value twice")
	}

	records := s.objects(t)
	if len(records) == 0 {
		t.Fatal("the store holds nothing: Mint wrote no record, so this test proves nothing")
	}

	for key, data := range records {
		for _, value := range []string{access, refresh} {
			if bytes.Contains(data, []byte(value)) {
				t.Errorf("%s holds the token value it was minted from", key)
			}
			for _, prefix := range valuePrefixes(value) {
				if bytes.Contains(data, []byte(prefix)) {
					t.Errorf("%s holds the first %d characters of a token value", key, len(prefix))
				}
			}
		}
	}
}

// valuePrefixes is the value's prefixes a record must not contain. The floor
// is above the length at which a base64url value could share a run of
// characters with the hex of a hash by accident: below it the test would fail
// on a coincidence rather than on a stored value.
func valuePrefixes(value string) []string {
	const floor = 12
	out := make([]string, 0, len(value))
	for n := floor; n <= len(value); n++ {
		out = append(out, value[:n])
	}
	return out
}

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

func TestRefreshRotatesAndReplayRevokesTheFamily(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	_, first, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	access, second, err := Rotate(ctx, s, first)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if second == first {
		t.Fatal("Rotate returned the same refresh token: rotation is what makes a theft visible")
	}
	if _, _, err := Rotate(ctx, s, access); !errors.Is(err, ErrWrongTokenKind) {
		t.Errorf("Rotate with an access token = %v; want ErrWrongTokenKind", err)
	}

	// The token that was rotated away is the stolen one: presenting it again
	// revokes the whole family, including the pair rotation just issued.
	if _, _, err := Rotate(ctx, s, first); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("Rotate with a rotated-away token = %v; want ErrRefreshReplay", err)
	}
	for _, value := range []string{access, second} {
		if _, err := s.LookupByHash(ctx, TokenHash(value)); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("LookupByHash after a replay = %v; want ErrTokenNotFound", err)
		}
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

	// The revoke lands here, after the rotation has read the family and
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
