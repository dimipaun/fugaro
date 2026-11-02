// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// `fugaro coordinator intents` (docs/console-coordinator-v2/implementation-plan.md,
// task C4, D827): what the project's callers have declared, read out of the
// coordinator's own database. It is the read the console's transcript pane is
// built on — the intent records are in the tree and no command could print them.
//
// The read is the stored view's own (status --from-store, D364) and this file
// holds the three properties that make it one:
//
//   - TestCoordinatorIntentsPrintsTheStoredDeclarations is C4's red step: a
//     fixture's two declarations for one repository, and NONE for a repository
//     the store holds none for — "no records" is a fact about the store, and it
//     is not the read having failed.
//   - TestCoordinatorIntentsTextDrawsTheDeclarationsAndTheNote is the same
//     document for a person, carrying the sentence every coordinator surface
//     carries: declared by is a declaration and never an identity.
//   - TestCoordinatorIntentsNeedsNoKeyAndOpensNoHost: this read holds no
//     credential of the host's, opens no provider and works where the
//     project-wide read refuses (the credentials service).
//   - TestCoordinatorIntentsWindowBoundsTheDeclarations: --since bounds the
//     declarations by the record's own clock, and no records in the window is
//     an empty list rather than a failure.
//   - TestCoordinatorIntentsRefusesAReadTheDatabaseRefuses: a refused read is a
//     refusal naming the role that admits it, and prints no document — an empty
//     one would say the project had declared nothing.
//   - TestCoordinatorIntentsNeedsADatabase: a project with no database is
//     refused naming the command that deploys one.

// newCoordinatorIntentsFixture is the installation this read is driven against:
// the report fixture's database and config — an RTDB fake reached through
// budget.rtdb_url, and the mark `fugaro update --firebase-project` writes — with
// a SECOND repository the store holds no records for, and the Declarations of
// testdata/coordinator_intents.json behind the first one.
func newCoordinatorIntentsFixture(t *testing.T) *coordinatorReportFixture {
	t.Helper()
	f := newCoordinatorReportFixture(t)
	addOtherRepo(t, f.cloudFixture)
	f.db.Set(coordinator.PathIntents(appSlug), intentsNode(t))
	return f
}

// addOtherRepo writes a second repository into the fixture's config, beside
// the one it already lists: --all reads the repositories the config names, so a
// repository the store holds no records for has to be one of them. The insert
// is inside the repos block — the config's own text is rewritten in place
// rather than appended to, since what follows it is the budget block and a
// second repository after that is not YAML.
func addOtherRepo(t *testing.T, f *cloudFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := regexp.MustCompile(`(?m)^  acme/app: (.*)\n`).
		ReplaceAllString(string(data), "  acme/app: $1\n  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	if !strings.Contains(cfg, "acme/other") {
		t.Fatalf("the fixture's config lists no acme/app to set a second repository beside: %s", data)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// intentsNode is testdata/coordinator_intents.json as the database holds it:
// two declarations of the fixture's repository, each under the ID its own text
// derives (IntentID), so the node is one the report path itself could have
// written, and a hand-edited file cannot pass off a key no declaration has.
func intentsNode(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "coordinator_intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(data, &node); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return node
}

// intentsFixtureRecords is the fixture file's declarations, keyed by the ID it
// stores them under: the test's expectation is the file, and never a second copy
// of it written out here.
func intentsFixtureRecords(t *testing.T) map[string]coordinator.Intent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "coordinator_intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]coordinator.Intent
	if err := json.Unmarshal(data, &node); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return node
}

// noIntentsOpens fails the test if the command opens a provider at all: this
// read is the database's, so there is no App credential to open a host with, and
// an opener that was reached is the read going somewhere it must not.
func noIntentsOpens(t *testing.T) {
	t.Helper()
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		t.Error("coordinator intents opened a provider: this read holds no App credential and reads no host")
		return nil, errors.New("no provider")
	})
}

// intentsDoc is the command's document for one repository, read as the shape it
// is.
func intentsDoc(t *testing.T, args ...string) coordinator.RepoIntents {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinator.RepoIntents
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

// TestCoordinatorIntentsPrintsTheStoredDeclarations is C4's red step: the
// fixture's two declarations for the repository the store holds them for, and
// none for the repository it holds none for. The second half is why the red step
// names it: a repository with no records is a fact — an empty list, and the key
// present — while "the read failed" is a different answer that must never be
// drawn as one.
func TestCoordinatorIntentsPrintsTheStoredDeclarations(t *testing.T) {
	newCoordinatorIntentsFixture(t)

	out, errOut, err := execute(t, "coordinator", "intents", "--all", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var doc coordinator.ProjectIntents
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(doc.Repos) != 2 {
		t.Fatalf("--all read %d repositories, want the two the local config lists: %s", len(doc.Repos), out)
	}
	app, other := doc.Repos[0], doc.Repos[1]
	if app.Repo != "acme/app" || other.Repo != "acme/other" {
		t.Fatalf("the document's repositories are %q and %q, want the config's two in its own order", app.Repo, other.Repo)
	}
	// The provider is the caller's answer and not the read's: the node is keyed
	// by a slug of it and holds no host (the same field §6.13's document carries).
	if app.Provider != "github" {
		t.Errorf("provider = %q, want github, which is what the fixture's repos entry records for acme/app", app.Provider)
	}

	// The declarations are the fixture's, field for field: the document is read
	// against the file rather than against a second copy of it written here.
	fixture := intentsFixtureRecords(t)
	if len(app.Intents) != len(fixture) {
		t.Fatalf("acme/app read %d declarations, want the fixture's %d: %s", len(app.Intents), len(fixture), out)
	}
	wantOrder := []string{
		"27561d7a8bc946dec9f1f79706498c8a0715602d72ef90b53852447cd19455a9",
		"8659c4432bd9c171f7585e013d17d6a4b5d0812e8b627d9e48e28690eabf871a",
	}
	for i, rec := range app.Intents {
		want, ok := fixture[rec.ID]
		if !ok {
			t.Fatalf("the document holds a record under %q, which is not a key of the fixture: %s", rec.ID, out)
		}
		if rec.ID != wantOrder[i] {
			t.Errorf("record %d is %q, want %q: the declarations are printed newest first, by the record's own clock", i, rec.ID, wantOrder[i])
		}
		if id := coordinator.IntentID(rec.Intent); rec.ID != id {
			t.Errorf("the record is stored under %q and its own declaration derives %q: the key is not the one IntentID gives", rec.ID, id)
		}
		if !rec.At.Equal(want.At) || rec.DeclaredBy != want.DeclaredBy || rec.Branch != want.Branch ||
			rec.Release != want.Release || !slices.Equal(rec.Files, want.Files) || !slices.Equal(rec.Services, want.Services) {
			t.Errorf("record %d = %+v, want the fixture's %+v", i, rec, want)
		}
	}

	// The second half: a repository the store holds none for is an empty list
	// under the key, and never a missing key or a null a reader has to
	// interpret. The claim is about the bytes, so it is read off the bytes.
	if len(other.Intents) != 0 {
		t.Errorf("a repository the store holds none for read as %+v, want no declarations", other.Intents)
	}
	if !strings.Contains(out, `"intents": []`) {
		t.Errorf("a repository with no records printed no empty list, so a reader cannot tell it from a document older than the field:\n%s", out)
	}

	// Every key a record carries is one of the shape's seven, and the whole set
	// of them is: the document is the fields Intent already has and no others,
	// which is what the console's decoder is written against.
	keys := map[string]bool{}
	var raw struct {
		Repos []struct {
			Intents []map[string]json.RawMessage `json:"intents"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, r := range raw.Repos {
		for _, rec := range r.Intents {
			for key := range rec {
				keys[key] = true
			}
		}
	}
	var got []string
	for key := range keys {
		got = append(got, key)
	}
	slices.Sort(got)
	want := []string{"at", "branch", "declared_by", "files", "id", "release", "services"}
	if !slices.Equal(got, want) {
		t.Errorf("the document's records carry %v, want the fields coordinator.Intent carries and the id it is stored under %v", got, want)
	}
}

// The same document for a person: one table per repository, and the sentence
// every coordinator surface carries under it. The record's ID is the document's
// — it is the node's key, a hash of the declaration — and no column of a
// person's table is a hash.
func TestCoordinatorIntentsTextDrawsTheDeclarationsAndTheNote(t *testing.T) {
	newCoordinatorIntentsFixture(t)

	out, errOut, err := execute(t, "coordinator", "intents", "--all", "--since", "0")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{
		"acme/app: 2 intents",
		"person-1@example.com",
		"release/v0.15.0",
		"ee/coordinator/state.go, internal/cli/run.go",
		"acme/other: no intent records",
		"a declaration, and never a",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q:\n%s", want, out)
		}
	}
	if id := "27561d7a8bc946dec9f1f79706498c8a0715602d72ef90b53852447cd19455a9"; strings.Contains(out, id) {
		t.Errorf("the table prints the record's ID, which is the node's key and not a person's column:\n%s", out)
	}
}

// --since bounds the declarations by the record's OWN clock, which is the
// declaration's ("when the caller said so") and not the read's. Neither
// repository here is the fixture's: both records are written below with stamps
// the test chose, so the window is measured against a span the test holds
// rather than against a date the clock slides past.
func TestCoordinatorIntentsWindowBoundsTheDeclarations(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	addOtherRepo(t, f.cloudFixture)
	ctx := context.Background()
	store, err := openCoordinatorStore(ctx, f.config(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []struct {
		in coordinator.Intent
		at time.Time
	}{
		{coordinator.Intent{DeclaredBy: "person-1@example.com", Branch: "topic/recent"}, fcli.LsNow().UTC().Add(-10 * time.Minute)},
		{coordinator.Intent{DeclaredBy: "person-2@example.com", Branch: "topic/old"}, fcli.LsNow().UTC().Add(-48 * time.Hour)},
	} {
		at, in := rec.at, rec.in
		if _, _, err := (&coordinator.Reporter{Store: store, Now: func() time.Time { return at }}).Report(ctx, appSlug, in); err != nil {
			t.Fatal(err)
		}
	}

	// One hour back: the declaration made ten minutes ago is in the window and
	// the one made two days ago is not.
	got := intentsDoc(t, "coordinator", "intents", "--repo", "acme/app", "--since", "1h", "--json")
	if len(got.Intents) != 1 || got.Intents[0].Branch != "topic/recent" {
		t.Errorf("--since 1h read %+v, want the declaration made ten minutes ago and no other", got.Intents)
	}

	// No bound: both. And a window with nothing in it is an empty list rather
	// than a failure — the read answered, and the answer is that nothing was
	// declared that recently.
	got = intentsDoc(t, "coordinator", "intents", "--repo", "acme/app", "--since", "0", "--json")
	if len(got.Intents) != 2 {
		t.Errorf("--since 0 read %d declarations, want the two written here: %+v", len(got.Intents), got.Intents)
	}
	if got = intentsDoc(t, "coordinator", "intents", "--repo", "acme/other", "--since", "1h", "--json"); got.Intents == nil || len(got.Intents) != 0 {
		t.Errorf("a repository with nothing declared in the window read as %#v, want an empty list", got.Intents)
	}
}

// This read holds no credential of the host's: no App ID, no App key, no
// Bitbucket token, and no provider opened — with the installation's credentials
// service in the environment, where the project-wide read (coordinatorReader)
// refuses, it still answers. Everything it prints is what the report path wrote
// into the database.
func TestCoordinatorIntentsNeedsNoKeyAndOpensNoHost(t *testing.T) {
	newCoordinatorIntentsFixture(t)
	noIntentsOpens(t)
	t.Setenv(providers.EnvGitHubAppID, "")
	t.Setenv(providers.EnvGitHubAppKey, "")
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example")

	got := intentsDoc(t, "coordinator", "intents", "--repo", "acme/app", "--since", "0", "--json")
	if len(got.Intents) != 2 {
		t.Errorf("the read of a fixture the store holds two declarations for printed %d: %+v", len(got.Intents), got.Intents)
	}
}

// A database that refused the read is a refusal naming the one role that admits
// it, and it prints no document at all: an empty one would draw the same screen
// as a project that has declared nothing, which is a different fact from a
// reader whose credential the database does not admit.
func TestCoordinatorIntentsRefusesAReadTheDatabaseRefuses(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	f.db.DenyRead(coordinator.PathIntents(appSlug))

	out, _, err := execute(t, "coordinator", "intents", "--repo", "acme/app", "--since", "0", "--json")
	if err == nil {
		t.Fatalf("a database that refused the read answered with a document: %s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d, want %d: the credential the caller holds is the caller's to fix", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "roles/firebasedatabase.viewer") {
		t.Errorf("the refusal is %v, want the role that admits the read named in it", err)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}

// A project with no database is refused naming the command that deploys one,
// which is the same refusal the stored view's own read makes: the coordinator's
// state lives in the project's budget database and nowhere else.
func TestCoordinatorIntentsNeedsADatabase(t *testing.T) {
	newCloudFixture(t)

	out, _, err := execute(t, "coordinator", "intents", "--repo", "acme/app", "--json")
	if err == nil {
		t.Fatalf("a project with no database answered with a document: %s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "fugaro update --firebase-project") {
		t.Errorf("exit %d, %v; want the refusal naming the command that deploys the database", fcli.ExitCode(err), err)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}
