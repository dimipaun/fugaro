package credsvc

// legacy_auth_test.go pins the release-1 bridge (MG3): the old record
// creds/repoauth/<slug> read as the chain of sources it stands for.
//
// The test is the promise the migration's whole first release rests on. `fugaro
// update` keeps writing the record beside the table for release 1, a run that
// bootstrapped on the release behind carries no table and asks in the old wire,
// and the service it reaches is this one — so the record has to answer that run
// a chain, and it has to answer it the sources its own configuration gave it and
// no others. What is asserted here is therefore the pair the bridge is for: a
// record a release behind really wrote still resolves (the writer is creds'
// own, the one `fugaro update` writes with), and every read this bridge cannot
// complete refuses rather than serving whatever the record happened to hold. The
// second half of the promise is the project's policy: the record is what the
// repository's own configuration gave it, and the paid fallback is the bound the
// old path applied over it, so the two together are the reach that run had.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// legacyBridgeRig is one installation's database and a bridge over it: the same
// client a deployment hands in (ticketStore's own pair), so the record is read
// through creds' reader over a node the fake really holds — and the policy
// through the reader the service's own Config carries.
//
// fallback is the published policy's `credentials.fallback` ("" publishes a node
// that names none, which is D152's protective default): the second bound this
// bridge applies, beside the record.
func legacyBridgeRig(t *testing.T, fallback string) (*gcpfake.RTDB, LegacyBridge) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client := legacyClient(t, db)
	legacyPolicyNode(t, db, fallback)
	return db, NewLegacyAuth(client, RTDBPolicy{DB: client})
}

// legacyClient is the database client the fake's own URL answers, which is what
// both the bridge and creds' writer are handed.
func legacyClient(t *testing.T, db *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	return client
}

// legacyPolicyNode publishes the project's policy node: an empty fallback is a
// node with no credentials block, which is the policy of an installation that
// published none and reads as `oauth-only` (R4, D152).
//
// A written `auto` carries the budget block it is only valid with (D142: paying
// at once is what an enforcing ceiling bounds), so the node this publishes is the
// one an installation that wrote the fallback really has.
func legacyPolicyNode(t *testing.T, db *gcpfake.RTDB, fallback string) {
	t.Helper()
	if fallback == "" {
		db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version})
		return
	}
	db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{"fallback": fallback},
	})
}

// legacyRecord writes one repository's record through creds' own writer, which
// is the one `fugaro update` writes it with (creds.PutRepoAuth): what the bridge
// reads is then a record a release behind really left, and not a shape this test
// invented.
func legacyRecord(t *testing.T, db *gcpfake.RTDB, slug, auth string, accounts ...string) {
	t.Helper()
	rec := creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: auth, Accounts: accounts}
	if err := creds.PutRepoAuth(context.Background(), legacyClient(t, db), slug, rec); err != nil {
		t.Fatalf("writing the record %+v: %v", rec, err)
	}
}

// sourcesJSON is a chain as the document it is, which is how this test compares
// two: a routing.Source carries the admit list, so the type is not comparable
// and slices.Equal cannot take it — and the JSON is the shape the answer and the
// run's record carry (contract-table.md), so a chain that reads back as the
// wanted one is a chain the rest of the contract can read too.
func sourcesJSON(t *testing.T, sources []routing.Source) string {
	t.Helper()
	b, err := json.Marshal(sources)
	if err != nil {
		t.Fatalf("marshalling %v: %v", sources, err)
	}
	return string(b)
}

// TestAnOlderRunStillResolves: a repository's record — the one the release
// behind wrote, and the one this release keeps writing — answers the sources
// that repository's runs go out on, and nothing else.
//
// The modes are the closed set of config.AgentAuths, each under the two values
// of the project's paid fallback: the key is the one source the fallback governs
// (R4, D152), so an `api-key` or a `cascade` record answers it where the project
// pays and answers nothing or the launcher's pool alone where it does not — the
// sources the OLD path served that repository, which is the whole of what the
// bridge may stand for. The refusals are every way the record is not a fact
// about a repository's reach. The last one is the fail-closed direction spelled
// out: a record naming nothing is NOT "no record" — the flag beside the list is
// what tells the walk those two apart — and neither is an auth mode this release
// cannot stand for, which is a record written by a vocabulary that is not this
// one's.
func TestAnOlderRunStillResolves(t *testing.T) {
	slug := testSlug(t, testRepoPath)

	for _, tc := range []struct {
		what     string
		fallback string
		auth     string
		accounts []string
		want     []routing.Source
	}{
		{
			what:     "an api-key repository on a project that pays",
			fallback: projectpolicy.FallbackAuto,
			auth:     config.AuthAPIKey,
			want:     []routing.Source{{Kind: routing.KindAnthropic}},
		},
		{
			what:     "an oauth repository",
			fallback: projectpolicy.FallbackAuto,
			auth:     config.AuthOAuth,
			accounts: []string{testAccount, config.DefaultClaudeAccount},
			want: []routing.Source{
				{Kind: routing.KindPool, Pool: testAccount},
				{Kind: routing.KindPool, Pool: config.DefaultClaudeAccount},
			},
		},
		{
			what:     "an oauth repository on a project that does not pay",
			fallback: projectpolicy.FallbackOAuthOnly,
			auth:     config.AuthOAuth,
			accounts: []string{testAccount},
			want:     []routing.Source{{Kind: routing.KindPool, Pool: testAccount}},
		},
		{
			what:     "a cascade repository on a project that pays",
			fallback: projectpolicy.FallbackAuto,
			auth:     config.AuthCascade,
			want:     []routing.Source{{Kind: routing.KindLauncherPool}, {Kind: routing.KindAnthropic}},
		},
		{
			what:     "a cascade repository on a project that does not pay",
			fallback: projectpolicy.FallbackOAuthOnly,
			auth:     config.AuthCascade,
			want:     []routing.Source{{Kind: routing.KindLauncherPool}},
		},
		{
			what:     "a vertex repository on a project that does not pay",
			fallback: projectpolicy.FallbackOAuthOnly,
			auth:     authVertex,
			want:     []routing.Source{{Kind: routing.KindVertex}},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			db, bridge := legacyBridgeRig(t, tc.fallback)
			legacyRecord(t, db, slug, tc.auth, tc.accounts...)
			got, found := bridge.OldSources(slug)
			if !found {
				t.Fatalf("a run whose image predates the table meets no record at %s: it carries no table of its own, so the record is the only thing that can answer it", creds.PathRepoAuth(slug))
			}
			if got, want := sourcesJSON(t, got), sourcesJSON(t, tc.want); got != want {
				t.Errorf("the record stands for %s, want %s", got, want)
			}
		})
	}

	// The key is in the chain only where the project pays (R4), so the two
	// records whose whole content IS the key answer nothing on a project that
	// does not — the same call the old path refused with ErrFallbackForbidden,
	// and the one direction the record alone cannot see. A bridge that read the
	// record alone would serve the paid key on every installation that never
	// wrote `fallback: auto`, which is the protective default.
	t.Run("an api-key record on a project that publishes no policy", func(t *testing.T) {
		db, bridge := legacyBridgeRig(t, "")
		legacyRecord(t, db, slug, config.AuthAPIKey)
		got, found := bridge.OldSources(slug)
		if !found {
			t.Fatal("a record read from a project that published no policy is answered as no record: the record is there, and the policy's silence is a bound on it and not a missing record")
		}
		if len(got) != 0 {
			t.Errorf("an api-key record stands for %v on a project that publishes no fallback, want nothing: no written `auto` is `oauth-only`, and the old path never served the paid key under it", got)
		}
	})

	t.Run("an api-key record on a project whose policy cannot be read", func(t *testing.T) {
		db, bridge := legacyBridgeRig(t, projectpolicy.FallbackAuto)
		legacyRecord(t, db, slug, config.AuthAPIKey)
		// The node as a release this one does not know wrote it: readable enough
		// to be there and not readable as a policy.
		db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version, "credentials": "not a block"})
		got, found := bridge.OldSources(slug)
		if !found {
			t.Fatal("a policy that cannot be read refused the whole record: the record is a separate read, and a policy the service cannot read is the protective answer and not a missing record")
		}
		if len(got) != 0 {
			t.Errorf("an api-key record stands for %v under a policy this release cannot read, want nothing: the paid key is served only where the policy is read and writes `auto`", got)
		}
	})

	t.Run("a repository no update has recorded", func(t *testing.T) {
		_, bridge := legacyBridgeRig(t, projectpolicy.FallbackAuto)
		got, found := bridge.OldSources(slug)
		if found || got != nil {
			t.Errorf("a repository with no record answers %v (found=%v), want no record at all: found=false is the walk's refusal and not an empty chain", got, found)
		}
	})

	t.Run("an oauth record naming no account", func(t *testing.T) {
		db, bridge := legacyBridgeRig(t, projectpolicy.FallbackAuto)
		legacyRecord(t, db, slug, config.AuthOAuth)
		got, found := bridge.OldSources(slug)
		if !found {
			t.Fatal("a record that names no account is answered as no record at all: the two are different situations and the flag is what tells them apart")
		}
		if len(got) != 0 {
			t.Errorf("a record naming no account stands for %v, want nothing: an empty list admits nobody", got)
		}
	})

	t.Run("a record of an auth this release does not know", func(t *testing.T) {
		db, bridge := legacyBridgeRig(t, projectpolicy.FallbackAuto)
		// The node as a later release's writer would leave it: a record this
		// release can read and cannot stand for. creds' own writer refuses to
		// write one (PutRepoAuth checks the auth against config.AgentAuths), so
		// the node is seeded as the document it is.
		db.Set(creds.PathRepoAuth(slug), map[string]any{
			"version": creds.RepoAuthVersion,
			"auth":    "a-mode-a-later-release-wrote",
		})
		got, found := bridge.OldSources(slug)
		if !found {
			t.Fatal("a record this release cannot stand for is answered as no record: the repository does have one, and what it says is a mode whose sources nobody here recorded")
		}
		if len(got) != 0 {
			t.Errorf("a mode this release does not know stands for %v, want nothing: a source invented for it would be a credential nobody recorded", got)
		}
	})

	t.Run("a database nobody can read", func(t *testing.T) {
		got, found := NewLegacyAuth(errDB{}, nil).OldSources(slug)
		if found || got != nil {
			t.Errorf("a read that failed answers %v (found=%v), want no record: a reach invented from a read that failed is the direction that serves a call nobody bounded", got, found)
		}
	})

	t.Run("no repository named", func(t *testing.T) {
		_, bridge := legacyBridgeRig(t, projectpolicy.FallbackAuto)
		got, found := bridge.OldSources("")
		if found || got != nil {
			t.Errorf("a call naming no repository answers %v (found=%v), want no record", got, found)
		}
	})

	t.Run("a bridge over no database", func(t *testing.T) {
		got, found := NewLegacyAuth(nil, nil).OldSources(slug)
		if found || got != nil {
			t.Errorf("a bridge with no database answers %v (found=%v), want no record", got, found)
		}
	})

	t.Run("a bridge with no policy reader", func(t *testing.T) {
		db := gcpfake.NewRTDB(t)
		legacyRecord(t, db, slug, config.AuthAPIKey)
		got, found := NewLegacyAuth(legacyClient(t, db), nil).OldSources(slug)
		if !found {
			t.Fatal("a bridge with no policy reader answered as no record: the record read is its own, and the policy's absence is the protective default")
		}
		if len(got) != 0 {
			t.Errorf("a bridge with no policy reader stands for %v, want nothing: a reader that named no policy is `oauth-only`, the value a creds.Settings built without one has", got)
		}
	})
}
