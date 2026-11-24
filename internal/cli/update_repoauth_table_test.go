package cli

// The repository auth record BESIDE the table (MG3 of
// docs/model-credentials-routing/implementation-plan.md, D3196, R25): for
// release 1 `fugaro update` keeps writing creds/repoauth/<slug> as it did, so a
// run that bootstrapped on the release behind — a run whose image predates the
// table — still resolves at the service (credsvc's legacy bridge,
// internal/credsvc/legacy_auth.go).
//
// Two facts, one test each. The record is still written, for every auth a
// repository can have: the write is the whole of the release-1 promise, and
// dropping it would stop every run in flight at its next model call. And the
// record gains no source the repository's own configuration does not give it:
// its content is derived from the same answer the table is written from
// (repoAuthSources, update_repoauth.go), so it is never wider than the
// repository's own pre-migration access — while an entry the table's checks
// refuse, which is in no table at all, stays on the record (D3196: the record is
// wider than the table by exactly those, and "never wider than the table" is the
// wrong rule to write code by).
//
// The record is read back through creds' own reader and through the bridge the
// service will read it with, and never through a copy this test decoded for
// itself: what is asserted is what a run whose image predates the table meets.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// repoAuthSourcesJSON is a chain as the document it is, which is how these tests
// compare two: a routing.Source carries the admit list, so the type is not
// comparable, and the JSON is the shape the table, the answer and the run's
// record carry (contract-table.md).
func repoAuthSourcesJSON(t *testing.T, sources []routing.Source) string {
	t.Helper()
	b, err := json.Marshal(sources)
	if err != nil {
		t.Fatalf("marshalling %v: %v", sources, err)
	}
	return string(b)
}

// bridgeSources is the record of one repository as the release-1 bridge reads
// it: the same client the service reads the node with, over the installation's
// database, so what is asserted is the chain a run whose image predates the
// table is served — and not what this file happens to think the record means.
// The policy reader is the service's own (RTDBPolicy over that client), so the
// paid fallback bound is read where the service reads it.
func bridgeSources(t *testing.T, url, slug string) ([]routing.Source, bool) {
	t.Helper()
	db, err := newBudgetClient(context.Background(), url, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	return credsvc.NewLegacyAuth(db, credsvc.RTDBPolicy{DB: db}).OldSources(slug)
}

// publishPaidFallback puts the project's policy node where the bridge reads it.
// A written `auto` carries the budget block it is only valid with (D142: paying
// at once is what an enforcing ceiling bounds), and it is the node MG1 writes the
// key into the chain for; an empty fallback publishes nothing at all, which is
// the policy of an installation that never wrote one and reads as `oauth-only`
// (D152).
func publishPaidFallback(t *testing.T, r *gcpfake.RTDB, fallback string) {
	t.Helper()
	if fallback == "" {
		return
	}
	r.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{"fallback": fallback},
	})
}

// TestTheOldRecordIsStillWritten: `fugaro update` of a repository leaves the
// record the release behind wrote, for every auth a repository can have.
//
// The record is what the service narrows a run's calls by before the table
// exists, and a run whose image predates the table is served from it alone
// (D3196, R25). So a release that stopped writing it would not fail this test
// quietly: every run in flight would halt at its next model call, which is the
// one thing the migration's first release promises not to do.
func TestTheOldRecordIsStillWritten(t *testing.T) {
	slug := mustSlug("github", "acme/app")
	for _, tc := range []struct {
		what         string
		auth         string
		wantAccounts []string
	}{
		{"an api-key repository", config.AuthAPIKey, nil},
		{"an oauth repository", config.AuthOAuth, []string{"acme-personal", config.DefaultClaudeAccount}},
		{"a cascade repository", config.AuthCascade, nil},
		{"a vertex repository", "vertex", nil},
	} {
		t.Run(tc.what, func(t *testing.T) {
			db := repoAuthRig(t, &initOptions{door: updateDoor.name}, tc.auth)
			rec, found := repoAuthOf(t, db, slug)
			if !found {
				t.Fatalf("%s is not there after `fugaro update --repo` applied this repository's root: a run whose image predates the table reads this record and no table, so a release that stopped writing it would halt every run in flight", creds.PathRepoAuth(slug))
			}
			if rec.Auth != tc.auth {
				t.Errorf("the record's auth is %q, want %q: the record is the repository's own configuration, whichever release reads it", rec.Auth, tc.auth)
			}
			if got, want := len(rec.Accounts), len(tc.wantAccounts); got != want {
				t.Errorf("the record names %d accounts (%v), want %d (%v)", got, rec.Accounts, want, tc.wantAccounts)
			}
			for _, want := range tc.wantAccounts {
				if !containsString(rec.Accounts, want) {
					t.Errorf("the record names %v, want %q among them: the accounts this repository's jobs go out on", rec.Accounts, want)
				}
			}
		})
	}
}

// TestTheOldRecordGainsNoSourceTheTableLacks: the record stands for the sources
// the repository's own configuration gives it — the same answer the table is
// written from — and for nothing beside them.
//
// The four chains are the spec's own answers, not this implementation's: `oauth`
// becomes the account's pool, `api-key` the Anthropic key, `cascade` the
// launcher's own credentials then the key, `vertex` a chain naming Vertex
// (MG1's brief). Every one of them is read on a project that pays (`fallback:
// auto`), because the key is the one source the project's policy governs (R4,
// D152) and the last row is the other half of that: the same record on a project
// that never wrote the fallback stands for nothing at all, which is what the old
// path served it (creds' selection admits no paid tier under `oauth-only`) and
// what no widening of the record may undo. The negative is the fixture's other
// account: acme-other is a
// configured account of this installation that acme/app may not use, so a record
// built from the local config rather than from this repository's own answer
// would widen the service's reach to a subscription this repository's
// configuration never asks for — which is the whole of what the record bounds
// (D2070 (a), R7, and TestAuthModeNeverWidensTheAdmission on the service's side).
//
// The vertex row is D3196 read from this side: the entry the table's checks
// refuse (a Vertex model with no checked-in spelling, Check 882) is in no table
// at all, and it is on the record — the record is wider than the table by
// precisely the entries the checks refused, and never wider than the
// repository's own pre-migration access. Dropping it would move a run the
// migration's whole promise is not to move.
func TestTheOldRecordGainsNoSourceTheTableLacks(t *testing.T) {
	slug := mustSlug("github", "acme/app")
	for _, tc := range []struct {
		what     string
		auth     string
		fallback string
		want     []routing.Source
	}{
		{
			what:     "an api-key repository",
			auth:     config.AuthAPIKey,
			fallback: projectpolicy.FallbackAuto,
			want:     []routing.Source{{Kind: routing.KindAnthropic}},
		},
		{
			what:     "an oauth repository",
			auth:     config.AuthOAuth,
			fallback: projectpolicy.FallbackAuto,
			want: []routing.Source{
				{Kind: routing.KindPool, Pool: "acme-personal"},
				{Kind: routing.KindPool, Pool: config.DefaultClaudeAccount},
			},
		},
		{
			what:     "a cascade repository",
			auth:     config.AuthCascade,
			fallback: projectpolicy.FallbackAuto,
			want:     []routing.Source{{Kind: routing.KindLauncherPool}, {Kind: routing.KindAnthropic}},
		},
		{
			what:     "a vertex repository, whose entry no table can carry",
			auth:     "vertex",
			fallback: projectpolicy.FallbackAuto,
			want:     []routing.Source{{Kind: routing.KindVertex}},
		},
		{
			what: "an api-key repository on a project that does not pay",
			auth: config.AuthAPIKey,
			want: nil,
		},
		{
			what: "a cascade repository on a project that does not pay",
			auth: config.AuthCascade,
			want: []routing.Source{{Kind: routing.KindLauncherPool}},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			db := repoAuthRig(t, &initOptions{door: updateDoor.name}, tc.auth)
			publishPaidFallback(t, db, tc.fallback)
			got, found := bridgeSources(t, db.URL, slug)
			if !found {
				t.Fatalf("the record at %s stands for no source at all: a run whose image predates the table has nothing to resolve", creds.PathRepoAuth(slug))
			}
			if got, want := repoAuthSourcesJSON(t, got), repoAuthSourcesJSON(t, tc.want); got != want {
				t.Errorf("the record stands for %s, want %s", got, want)
			}
			for _, src := range got {
				if src.Pool == "acme-other" {
					t.Errorf("the record stands for %v, which names acme-other: the fixture's other account is admitted to another repository alone, and a record that named it would widen this one's reach at the service to a subscription its own configuration never asks for", got)
				}
			}
		})
	}
}

// containsString is the one membership question these tests ask of a record's
// accounts, spelled here rather than with slices.Contains so the failure above
// can name the account it did not find.
func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
