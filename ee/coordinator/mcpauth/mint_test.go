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
