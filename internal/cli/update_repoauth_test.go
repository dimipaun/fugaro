package cli

// The repository auth record `fugaro update` writes (B2.3 of
// docs/secrets-brokering/implementation-plan.md, D2070 (a), R7): what one
// repository's runs go out on, which is the boundary the credentials service
// narrows a non-cascade repository's calls to.
//
// The service cannot compute it and the caller may not state it (G16): the
// agent holds the run identity, so a record read from the wire would be the
// agent's own claim about a repository it may not be in. What these tests hold
// is that the run writes the repository's own configuration, and that the
// accounts it names are the ones this repository's jobs go out on.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// repoAuthRig is one call of the step under test against an installation's
// database: the local config of an installation whose claude_accounts admits
// acme-personal to acme/app and acme-other to another repository alone, and a
// repository spec for acme/app. auth and mode are what the tests vary.
func repoAuthRig(t *testing.T, o *initOptions, auth string) *gcpfake.RTDB {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		Budget:    &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints: localcfg.Endpoints{NoAuth: true},
		ClaudeAccounts: map[string]config.ClaudeAccount{
			"acme-personal": {AllowDataTo: []string{"acme/app"}},
			"acme-other":    {AllowDataTo: []string{"acme/other"}},
		},
	}
	ready := infra.RepoSpec{Slug: mustSlug("github", "acme/app"), Name: "acme/app"}
	cfg := &config.Config{Agent: config.Agent{Auth: auth}}
	(&initRun{o: o, w: &strings.Builder{}}).writeRepoAuth(t.Context(), lc, ready, cfg)
	return db
}

// repoAuthOf reads the repository's record back through internal/creds' own
// reader: what the service reads on a call, and not a copy this test decoded
// for itself.
func repoAuthOf(t *testing.T, r *gcpfake.RTDB, slug string) (creds.RepoAuth, bool) {
	t.Helper()
	db, err := newBudgetClient(context.Background(), r.URL, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	rec, found, err := creds.ReadRepoAuth(context.Background(), db, slug)
	if err != nil {
		t.Fatalf("reading %s after the run: %v", creds.PathRepoAuth(slug), err)
	}
	return rec, found
}

// TestUpdateWritesTheRepoAuthRecord: `fugaro update` of a repository leaves the
// record the service narrows its calls to — the repository's own agent.auth,
// and under oauth the accounts its runs may go out on, which are the ones its
// own jobs go out on (infra.WorkflowAccounts): the unsuffixed default every
// repository has, and each configured account whose allow_data_to names this
// repository, and no other.
//
// The fixture's acme-other is the negative half: it is a configured account of
// this installation that acme/app may not use, so a record that named every
// account of the local config would widen this repository's reach at the
// service to a subscription its own configuration never asks for — which is the
// whole of what the record exists to bound (D2070 (a), R7).
func TestUpdateWritesTheRepoAuthRecord(t *testing.T) {
	slug := mustSlug("github", "acme/app")
	db := repoAuthRig(t, &initOptions{door: updateDoor.name}, config.AuthOAuth)
	rec, found := repoAuthOf(t, db, slug)
	if !found {
		t.Fatalf("%s is not there after `fugaro update --repo` applied this repository's root: the service reads this record and no config of ours, so a switched oauth repository would be served the whole cascade", creds.PathRepoAuth(slug))
	}
	if rec.Version != creds.RepoAuthVersion {
		t.Errorf("the record's version is %d, want %d", rec.Version, creds.RepoAuthVersion)
	}
	if rec.Auth != config.AuthOAuth {
		t.Errorf("the record's auth is %q, want %q: it is the repository's own agent.auth that the service narrows a call to", rec.Auth, config.AuthOAuth)
	}
	if want := []string{"acme-personal", config.DefaultClaudeAccount}; !slices.Equal(rec.Accounts, want) {
		t.Errorf("the record names the accounts %v; want %v — the accounts this repository's jobs go out on, and never one its allow_data_to does not admit", rec.Accounts, want)
	}

	// A non-oauth repository records no account at all: an account names a
	// subscription, so a record naming one beside `api-key` would state a reach
	// its configuration does not have (creds refuses such a record outright).
	apiKey := repoAuthRig(t, &initOptions{door: updateDoor.name}, config.AuthAPIKey)
	rec, found = repoAuthOf(t, apiKey, slug)
	if !found || rec.Auth != config.AuthAPIKey {
		t.Fatalf("an api-key repository's record is %+v (found=%v), want its own auth", rec, found)
	}
	if len(rec.Accounts) != 0 {
		t.Errorf("an api-key repository's record names %v; want no account", rec.Accounts)
	}
}

// TestUpdateWritesTheRepoAuthRecordThroughTheConverge is the HARD RULE's own
// test for the record: the node is new with this release, so an installation
// that ran the release one behind has none, and the one command the user is
// told to run has to leave one. The fixture is the repository stage's own rig
// (workflowSecretsRig), whose repository is oauth and whose local config
// declares no account at all: the record is there afterwards, naming the
// default account every repository's runs may go out on.
func TestUpdateWritesTheRepoAuthRecordThroughTheConverge(t *testing.T) {
	r, slug := workflowSecretsRig(t)
	if v := r.db.Value(creds.PathRepoAuth(slug)); v != nil {
		t.Fatalf("the fixture carries a record at %s already (%v): the state this test is about is an installation that has not run the release that writes one", creds.PathRepoAuth(slug), v)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	rec, found, err := creds.ReadRepoAuth(context.Background(), db, slug)
	if err != nil {
		t.Fatalf("reading %s after the converge: %v", creds.PathRepoAuth(slug), err)
	}
	if !found {
		t.Fatalf("%s is not there after `fugaro update` alone: the service narrows this repository's calls by a record nothing wrote", creds.PathRepoAuth(slug))
	}
	if rec.Auth != config.AuthOAuth || !slices.Equal(rec.Accounts, []string{config.DefaultClaudeAccount}) {
		t.Errorf("the record is %+v; want the repository's own auth and the default account, which is the one its runs may go out on", rec)
	}
	if !strings.Contains(out, creds.PathRepoAuth(slug)) {
		t.Errorf("the run does not name %s, the record it wrote: the line is how a person knows the service has this repository\n%s", creds.PathRepoAuth(slug), out)
	}
}

// TestUpdateWritesNoRepoAuthRecordForAPlan: a plan writes nothing.
//
// --plan-only is where a coding agent's session reaches this engine (the loop
// plans an engine stage read-only and a session applies none), and `--yes`'s
// help promises that a session "changes nothing outside this machine". A record
// published out of a run that applied nothing is exactly that write: it would
// name a repository whose root this run never touched as one the service may
// serve.
func TestUpdateWritesNoRepoAuthRecordForAPlan(t *testing.T) {
	slug := mustSlug("github", "acme/app")
	db := repoAuthRig(t, &initOptions{door: updateDoor.name, planOnly: true}, config.AuthOAuth)
	if v := db.Value(creds.PathRepoAuth(slug)); v != nil {
		t.Errorf("%s exists after a plan (%v): --plan-only changes nothing outside this machine", creds.PathRepoAuth(slug), v)
	}
}
