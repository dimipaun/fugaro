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

// TestMintRefusesATokenThatCouldNeverBeUsed is the mint's own refusal: D926
// and D927 put a window that admits nothing and a scope that reaches no tool
// at the person's keyboard rather than in an agent's retry loop, and a token
// with no identity or no expiry is one the front door could never resolve. A
// refusal here writes nothing, which is the half worth asserting: a family
// minted and then refused would be a family nothing can present and nothing
// can revoke.
func TestMintRefusesATokenThatCouldNeverBeUsed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity string
		client   Client
		limits   Limits
		want     error
	}{
		{"no identity", "", testClient(), testLimits(), ErrTokenIncomplete},
		{"no client", "user:person-1", Client{}, testLimits(), ErrTokenIncomplete},
		{"no scope", "user:person-1", testClient(), Limits{MinRigor: rigor.POC, MaxRigor: rigor.Pro}, ErrNoScope},
		{"an unknown scope", "user:person-1", testClient(), Limits{MinRigor: rigor.POC, MaxRigor: rigor.Pro, Scopes: []string{"tasks:admin"}}, ErrUnknownScope},
		{"a window that admits nothing", "user:person-1", testClient(), Limits{MinRigor: rigor.Pro, MaxRigor: rigor.POC, Scopes: []string{ScopeTasksRead}}, ErrEmptyWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newFakeStore(t)
			if _, _, err := Mint(ctx, s, tc.identity, tc.client, tc.limits); !errors.Is(err, tc.want) {
				t.Errorf("Mint = %v; want %v", err, tc.want)
			}
			if got := s.objects(t); len(got) != 0 {
				t.Errorf("a refused mint wrote %d objects; want none", len(got))
			}
		})
	}
}

// TestRotateReturnsAFreshPair is the happy path every error path below is the
// error path of: one refresh token in, one new access token and one new
// refresh token out, and a family that has moved to the new one.
func TestRotateReturnsAFreshPair(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	access, refresh, err := MintAt(ctx, s, "user:person-1", testClient(), testLimits(), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("MintAt: %v", err)
	}
	before, err := s.LookupByHash(ctx, TokenHash(refresh))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}

	nextAccess, nextRefresh, err := RotateAt(ctx, s, refresh, now)
	if err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	for _, pair := range [][2]string{
		{"access", nextAccess}, {"refresh", nextRefresh},
	} {
		if pair[1] == "" {
			t.Fatalf("RotateAt returned no %s token", pair[0])
		}
	}
	if nextAccess == nextRefresh {
		t.Error("RotateAt returned the same value for both halves of the pair")
	}
	for _, value := range []string{nextAccess, nextRefresh} {
		if value == refresh || value == access {
			t.Error("RotateAt returned a value it had already issued")
		}
	}

	// The family is the authority a rotation writes to, and it moved: the
	// token that was presented is not the current one any more.
	fam, _, err := s.GetFamily(ctx, before.FamilyID)
	if err != nil {
		t.Fatalf("GetFamily: %v", err)
	}
	if fam.RefreshHash != TokenHash(nextRefresh) {
		t.Error("the family does not hold the refresh token RotateAt just issued")
	}
	if fam.RefreshHash == TokenHash(refresh) {
		t.Error("the family still holds the token that was rotated away")
	}

	for _, tc := range []struct {
		name  string
		value string
		kind  TokenKind
		// expires is whether the token is bounded by age: an access token is
		// (D930), and a refresh token is bounded by rotation and revocation
		// and by nothing else.
		expires bool
	}{
		{"access", nextAccess, KindAccess, true},
		{"refresh", nextRefresh, KindRefresh, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := s.LookupByHash(ctx, TokenHash(tc.value))
			if err != nil {
				t.Fatalf("LookupByHash: %v", err)
			}
			if rec.Kind != tc.kind {
				t.Errorf("Kind = %q; want %q", rec.Kind, tc.kind)
			}
			if rec.FamilyID != before.FamilyID {
				t.Errorf("FamilyID = %q; want the family that was rotated, %q", rec.FamilyID, before.FamilyID)
			}
			if got, want := rec.Token.ExpiresAt, now.Add(AccessTokenTTL); tc.expires && !got.Equal(want) {
				t.Errorf("ExpiresAt = %s; want %s", got, want)
			}
			if !tc.expires && !rec.Token.ExpiresAt.IsZero() {
				t.Errorf("ExpiresAt = %s; want the zero time", rec.Token.ExpiresAt)
			}
			// Rotation extends a family and never widens one: what the mint
			// issued the family with comes back out of the rotation.
			if rec.Token.Identity != before.Token.Identity {
				t.Errorf("Identity = %q; want %q", rec.Token.Identity, before.Token.Identity)
			}
			if rec.Token.Limits.SpendUSD != before.Token.Limits.SpendUSD || len(rec.Token.Limits.Scopes) != len(before.Token.Limits.Scopes) {
				t.Errorf("Limits = %+v; want the family's own", rec.Token.Limits)
			}
		})
	}

	// An access token is bounded by its expiry and by revocation, not by a
	// rotation: the access token minted alongside the refresh token that was
	// just exchanged stays valid until it ages out.
	if _, err := s.LookupByHash(ctx, TokenHash(access)); err != nil {
		t.Errorf("the access token of a rotated family = %v; want it to still resolve", err)
	}
}

// TestRotateRefusesWhatItMust is the other half: every way a refresh grant is
// answered with something other than a pair, and what each one does to the
// family. The last case is the one D930 exists for — a token that was already
// rotated away is not a miss but a theft, and the family dies here.
func TestRotateRefusesWhatItMust(t *testing.T) {
	ctx := context.Background()

	t.Run("an unknown token", func(t *testing.T) {
		s := newFakeStore(t)
		_, _, err := Rotate(ctx, s, "marker-a-refresh-token-that-was-never-minted")
		if !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("Rotate of a token that was never minted = %v; want ErrTokenNotFound", err)
		}
	})

	t.Run("an access token", func(t *testing.T) {
		s := newFakeStore(t)
		access, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		// An access token is the value an agent leaks, and it must not be the
		// value that extends its own family.
		if _, _, err := Rotate(ctx, s, access); !errors.Is(err, ErrWrongTokenKind) {
			t.Errorf("Rotate of an access token = %v; want ErrWrongTokenKind", err)
		}
		// Refused without touching the family: the refresh token the client
		// holds is still the current one and still rotates.
		if _, _, err := Rotate(ctx, s, refresh); err != nil {
			t.Errorf("Rotate after an access token was refused = %v; want the family untouched", err)
		}
	})

	t.Run("a revoked family", func(t *testing.T) {
		s := newFakeStore(t)
		_, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		rec, err := s.LookupByHash(ctx, TokenHash(refresh))
		if err != nil {
			t.Fatalf("LookupByHash: %v", err)
		}
		if err := s.RevokeFamily(ctx, rec.FamilyID); err != nil {
			t.Fatalf("RevokeFamily: %v", err)
		}
		// The record is still in the store and the family it names is not, so
		// the token has to read as one that was never minted rather than as a
		// replay of a family that still exists.
		if _, _, err := Rotate(ctx, s, refresh); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("Rotate after a revoke = %v; want ErrTokenNotFound", err)
		}
	})

	t.Run("a token that was rotated away", func(t *testing.T) {
		s := newFakeStore(t)
		_, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		access, next, err := Rotate(ctx, s, refresh)
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if _, _, err := Rotate(ctx, s, refresh); !errors.Is(err, ErrRefreshReplay) {
			t.Errorf("Rotate of a token that was rotated away = %v; want ErrRefreshReplay", err)
		}
		// The replay is a theft, so the answer is the family revoked: neither
		// the token the thief presented nor the one the legitimate holder was
		// just given resolves any more.
		for _, value := range []string{refresh, next, access} {
			if _, err := s.LookupByHash(ctx, TokenHash(value)); !errors.Is(err, ErrTokenNotFound) {
				t.Errorf("LookupByHash of %q after the replay = %v; want ErrTokenNotFound", value, err)
			}
		}
	})
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
