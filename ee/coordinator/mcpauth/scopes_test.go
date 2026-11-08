// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// This file is M7's own scope tests: the rule CheckScopes states, and the
// property the catalogue rests on — that every tool this front door serves
// maps to a scope v1 mints.

// TestUnknownScopeIsRefusedNotIgnored is M7's red step. A token that carries a
// scope this build does not know is refused rather than served as if it held
// no scopes: dropping the unknown name would leave a token that works for some
// calls and is refused on others with nothing saying why, and the operator who
// minted it would never learn which name this build is missing.
//
// It is written at the door rather than against the rule alone, because the
// two halves are what make it a test: the *same* token shape without the
// unknown scope reaches tools/list, so the refusal below is about the scope
// and not about a door that refuses everything. That is what it failed on
// before M7 existed — there was no registry and no resource server, so even a
// token the server itself had minted was answered 401.
//
// The unknown name is `roadmap:write`, the one D927 refuses by name: it is a
// real scope for a surface that does not exist yet, which is exactly the value
// a later build would make known, and a door that ignored it today would serve
// its holder tomorrow's permission today.
func TestUnknownScopeIsRefusedNotIgnored(t *testing.T) {
	ts := newResourceServer(t)
	_, tokens := ts.runFlow(t, testClientRedirect)

	rec := ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a token this server minted was answered %d (%s) at tools/list; want 200: without this half the refusal below would pass for a door that refuses every token", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The same token, minted with one scope this build does not know. It is
	// seeded rather than minted through the flow, because the flow refuses to
	// mint it (Limits.Validate) — which is the point: a value only a later
	// build could issue is exactly what this door has to refuse.
	const unknown = "roadmap:write"
	ts.seedAccessToken(t, testSeededToken, Token{
		Identity:  testIdentity,
		ClientID:  "client-that-minted-it",
		Limits:    Limits{Scopes: []string{ScopeTasksRead, unknown}},
		ExpiresAt: testNow.Add(AccessTokenTTL),
	})

	rec = ts.rpc(t, testSeededToken, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a token carrying the scope %q was answered %d (%s); want 401: a scope this build does not know is refused, not dropped — read as holding tasks:read it would serve the call", unknown, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if body := refusalOf(t, rec); body.Error != "invalid_token" {
		t.Errorf("the refusal of a token holding %q is %q; want invalid_token: the token is not usable here until it is minted against a build that knows every name it carries", unknown, body.Error)
	}
	if !strings.Contains(rec.Body.String(), unknown) {
		t.Errorf("the refusal does not name the scope it could not read: %s", strings.TrimSpace(rec.Body.String()))
	}
}

// TestTheCatalogueMapsEveryToolToAScopeV1Mints is finding M-e held as a
// property of the source: every tool the front door serves has a scope written
// down, and that scope is one v1 mints (D927).
//
// It is what makes "the six tools are public" a statement about this table
// rather than about spec.md: a tool added here with no scope, or with a scope
// outside the vocabulary, fails this test rather than being served under
// whatever a reader assumed. The description is checked with it because it is
// the other half of what tools/list shows, and a name with no line would be a
// tool a client cannot decide to call.
func TestTheCatalogueMapsEveryToolToAScopeV1Mints(t *testing.T) {
	if len(toolCatalogue) == 0 {
		t.Fatal("the catalogue is empty: this test would pass over a registry that serves nothing")
	}
	seen := map[string]bool{}
	for _, spec := range toolCatalogue {
		if spec.name == "" || spec.description == "" {
			t.Errorf("the catalogue holds %+v: every served tool needs a name and the line tools/list shows", spec)
		}
		if seen[spec.name] {
			t.Errorf("the catalogue holds %q twice: one name is one tool, and a second entry is a second scope for it", spec.name)
		}
		seen[spec.name] = true
		if !KnownScope(spec.scope) {
			t.Errorf("the catalogue maps %q to the scope %q, which v1 does not mint (%v): a tool whose scope nobody decided is not served", spec.name, spec.scope, ScopeVocabulary)
		}
		if scope, ok := ToolScope(spec.name); !ok || scope != spec.scope {
			t.Errorf("ToolScope(%q) = %q, %v; want the catalogue's own %q", spec.name, scope, ok, spec.scope)
		}
	}
	if _, ok := ToolScope("get_state"); ok {
		t.Error("ToolScope maps a name the catalogue does not hold: a name outside the table has no scope and is not served")
	}
}

// TestCheckScopesRefusesAnEmptySetAndAnUnknownOne is the rule on its own, in
// the shape the door and a later reader of a token record both need: a set
// this build can read, or the refusal that says which of the two failures it
// is. The empty set is not an unknown scope — the two are different events,
// and the operator's fix differs.
func TestCheckScopesRefusesAnEmptySetAndAnUnknownOne(t *testing.T) {
	for _, tc := range []struct {
		what   string
		scopes []string
		want   error
	}{
		{"one scope", []string{ScopeTasksRead}, nil},
		{"both scopes", []string{ScopeTasksRead, ScopeTasksWrite}, nil},
		{"one unknown scope", []string{ScopeTasksRead, "roadmap:write"}, ErrUnknownScope},
		{"only an unknown scope", []string{"roadmap:write"}, ErrUnknownScope},
		{"no scopes at all", nil, ErrNoScope},
		{"an empty set", []string{}, ErrNoScope},
	} {
		t.Run(tc.what, func(t *testing.T) {
			err := CheckScopes(tc.scopes)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckScopes(%v) = %v; want it read", tc.scopes, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckScopes(%v) = %v; want %v", tc.scopes, err, tc.want)
			}
		})
	}
}
