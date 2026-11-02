// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// `fugaro coordinator intents` (coordinator v2 C4, D827): what the fleet has
// declared, read out of the project's own database and nothing else. It is the
// source of the console's read-only pane (§5.8 of the reconciliation), so the
// tests here hold the three things that make it one:
//
//   - TestCoordinatorIntentsPrintsTheStoresDeclarations: a repository's two
//     declarations, newest first, each carrying the record's own fields and no
//     others, and a repository the store holds none for as an empty list and
//     not an error — while no host is opened at all.
//   - TestCoordinatorIntentsTextIsSafeAndSaysWhatDeclaredByIs: the text output
//     is the pane's other half, so another writer's bytes never reach the
//     terminal raw and the list carries the sentence that says what a
//     declaration is.
//   - TestCoordinatorIntentsRefusesAReadTheDatabaseDenies: a read the database
//     refused is a refusal naming the role that admits one, never an empty
//     list — a console that read it as "nothing declared" would draw a quiet
//     project where the truth is that nobody could look.
//   - TestCoordinatorIntentsRefusesARecordItCannotRead: the same, for one
//     document this build cannot decode.
//   - TestCoordinatorIntentsSinceBoundsTheDeclarations: --since is measured
//     against the record's own clock, and 0 is every declaration the store
//     holds.
//   - TestCoordinatorIntentsReadsWithTheCredentialsServiceSet: the read is the
//     database's and the caller's, so the installation whose App key lives in
//     its credentials service — the ordinary one — is served here exactly as
//     it is everywhere else, where §6.13's host read refuses.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// coordinatorIntentsFixture is a project whose database holds the coordinator's
// intent records: the cloud fixture's config, its marker and its two billed
// repositories, plus the RTDB fake the coordinator's state is written to.
type coordinatorIntentsFixture struct {
	*cloudFixture
	db *gcpfake.RTDB
}

func newCoordinatorIntentsFixture(t *testing.T) *coordinatorIntentsFixture {
	t.Helper()
	f := &coordinatorIntentsFixture{cloudFixture: newCloudFixture(t), db: gcpfake.NewRTDB(t)}
	fixtureRepos(t, f.cloudFixture, "acme/app", "acme/other")
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+" }\n")
	return f
}

// declare leaves one intent record under repo's node, written by the report
// path's own writer with a clock of the test's own. The records this read is
// about are the ones `coordinator report` writes, so the fixture writes them
// through the same Reporter rather than by hand, and the clock is what --since
// is measured against.
func (f *coordinatorIntentsFixture) declare(t *testing.T, repo string, at time.Time, in coordinator.Intent) {
	t.Helper()
	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	writer := &coordinator.Reporter{Store: coordinator.NewStore(db), Now: func() time.Time { return at }}
	if _, _, err := writer.Report(context.Background(), mustSlug(gitprov.KindGitHub, repo), in); err != nil {
		t.Fatalf("declaring %s: %v", repo, err)
	}
}

// coordinatorIntentsDoc is the document the command prints, read as the raw
// keys of every declaration rather than through a shape: a field the command
// added to the record would be invisible to a decoder written to the same
// struct the command marshals.
type coordinatorIntentsDoc struct {
	Repos []struct {
		Repo     string                       `json:"repo"`
		Provider string                       `json:"provider"`
		Intents  []map[string]json.RawMessage `json:"intents"`
	} `json:"repos"`
}

func (f *coordinatorIntentsFixture) read(t *testing.T, args ...string) coordinatorIntentsDoc {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinatorIntentsDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

func TestCoordinatorIntentsPrintsTheStoresDeclarations(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	first := fcli.LsNow().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	second := first.Add(time.Hour)
	f.declare(t, "acme/app", first, coordinator.Intent{
		DeclaredBy: "person-1", Branch: "release/v0.13.0",
		Files: []string{"internal/cli/exec.go"}, Services: []string{"internal/cli"},
	})
	f.declare(t, "acme/app", second, coordinator.Intent{
		DeclaredBy: "person-2", Branch: "fugaro/20261102-120000-abcd", Release: "0.13.0",
	})
	// The read is the database's and nothing else's: the fixture holds no App
	// credential, so a read that opened a host would have nothing to open one
	// with, and an opener that was reached fails the test.
	noStoreOpens(t)

	out, errOut, err := execute(t, "coordinator", "intents", "--all", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var got coordinatorIntentsDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Repos) != 2 || got.Repos[0].Repo != "acme/app" || got.Repos[1].Repo != "acme/other" {
		t.Fatalf("--all printed %d repositories (%+v), want acme/app and acme/other in the selection's order", len(got.Repos), got.Repos)
	}
	for _, r := range got.Repos {
		if r.Provider != "github" {
			t.Errorf("%s's provider = %q, want the config's own answer for it", r.Repo, r.Provider)
		}
	}
	// A repository the store holds none for is a list with nothing in it:
	// "no records" is a fact about the project and "the read failed" is not.
	if got.Repos[1].Intents == nil || len(got.Repos[1].Intents) != 0 {
		t.Errorf("the repository with no declarations = %+v, want an empty list", got.Repos[1].Intents)
	}
	if !strings.Contains(out, "\"intents\": []") {
		t.Errorf("the repository with no declarations is not printed as an empty list:\n%s", out)
	}

	intents := got.Repos[0].Intents
	if len(intents) != 2 {
		t.Fatalf("the repository with two declarations printed %d (%+v)", len(intents), intents)
	}
	// Newest first, by the record's own clock: the pane's first line is the
	// most recent thing the fleet declared.
	var who []string
	for _, in := range intents {
		var declaredBy string
		if err := json.Unmarshal(in["declared_by"], &declaredBy); err != nil {
			t.Fatal(err)
		}
		who = append(who, declaredBy)
	}
	if len(who) != 2 || who[0] != "person-2" || who[1] != "person-1" {
		t.Errorf("the declarations are ordered %v, want the newest first", who)
	}
	var at time.Time
	if err := json.Unmarshal(intents[0]["at"], &at); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(second) {
		t.Errorf("the newest declaration's at = %s, want the clock it was written with (%s)", at, second)
	}
	// The record's own fields and no others: the console's decoder is written
	// against exactly this set (json-contract.md §6.16).
	keys := make([]string, 0, len(intents[0]))
	for k := range intents[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"at", "branch", "declared_by", "files", "id", "release", "services"}
	if !slices.Equal(keys, want) {
		t.Errorf("one declaration carries the keys %v, want %v: the fields coordinator.Intent carries and no others", keys, want)
	}
	var files, services []string
	if err := json.Unmarshal(intents[1]["files"], &files); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(intents[1]["services"], &services); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "internal/cli/exec.go" || len(services) != 1 || services[0] != "internal/cli" {
		t.Errorf("the older declaration's files/services = %v/%v", files, services)
	}
	if len(intents[0]["files"]) == 0 || string(intents[0]["files"]) != "[]" {
		t.Errorf("a declaration that names no file carries %s, want an empty list", intents[0]["files"])
	}
}

func TestCoordinatorIntentsTextIsSafeAndSaysWhatDeclaredByIs(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	f.declare(t, "acme/app", fcli.LsNow().Add(-time.Hour), coordinator.Intent{
		DeclaredBy: "person-1",
		// A branch and a path are the caller's own text, and a caller may put
		// anything in them: a screen-clear and an OSC-8 hyperlink are two
		// sequences a terminal acts on and a reader cannot see.
		Branch: "release/v0.13.0\x1b[2J",
		Files:  []string{"internal/cli/\x1b]8;;http://example.invalid\x07exec.go"},
	})
	noStoreOpens(t)

	out, errOut, err := execute(t, "coordinator", "intents", "--repo", "acme/app")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, bad := range []struct {
		name string
		r    rune
	}{{"ESC", 0x1b}, {"BEL", 0x07}} {
		if i := strings.IndexRune(out, bad.r); i >= 0 {
			t.Errorf("the text output carries %s at byte %d, so another writer's bytes reached the terminal:\n%q", bad.name, i, out)
		}
	}
	for _, want := range []string{"release/v0.13.0", "internal/cli/", "exec.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q, so the sanitiser dropped the readable words and not only the escapes:\n%q", want, out)
		}
	}
	if !strings.Contains(out, coordinatorIntentsNote) {
		t.Errorf("the text output does not carry the sentence that says what declared by is:\n%s", out)
	}
	// The count line reads as a person's sentence, so one declaration is one
	// declaration: "1 declarations" is the one place this output addresses
	// nobody in particular and reads as a machine's.
	if !strings.Contains(out, "acme/app: 1 declaration\n") || strings.Contains(out, "1 declarations") {
		t.Errorf("the count line for one declaration = %q, want \"acme/app: 1 declaration\"", firstLine(out))
	}

	f.declare(t, "acme/app", fcli.LsNow().Add(-time.Minute), coordinator.Intent{
		DeclaredBy: "person-2", Branch: "release/v0.13.0",
	})
	out, errOut, err = execute(t, "coordinator", "intents", "--repo", "acme/app")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, "acme/app: 2 declarations\n") {
		t.Errorf("the count line for two declarations = %q, want \"acme/app: 2 declarations\"", firstLine(out))
	}
}

// firstLine is the header of a text read, which is what the count assertions
// above are about: the rest of the output is the table the note follows.
func firstLine(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return line
}

func TestCoordinatorIntentsRefusesAReadTheDatabaseDenies(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	f.db.DenyRead(coordinator.PathIntents(appSlug))
	noStoreOpens(t)

	out, _, err := execute(t, "coordinator", "intents", "--repo", "acme/app", "--json")
	if err == nil {
		t.Fatalf("a read the database refused succeeded, and printed:\n%s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("a refused read = exit %d, %v; want the caller's own error", fcli.ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "refused the read") || !strings.Contains(err.Error(), "roles/firebasedatabase.viewer") {
		t.Errorf("a refused read = %v, want the refusal naming the role that admits one", err)
	}
	if out != "" {
		t.Errorf("a refused read printed a document, so a console would draw an empty project where the truth is that nobody could look:\n%s", out)
	}
}

func TestCoordinatorIntentsRefusesARecordItCannotRead(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	// A document written by another writer this build does not know: the read
	// is strict like the view's own, and a record it cannot decode is refused
	// rather than shown as a declaration with the unknown field dropped.
	f.db.Set(coordinator.PathIntent(appSlug, "0f0f0f0f0f0f0f0f"), map[string]any{
		"declared_by": "person-1", "at": fcli.LsNow().UTC().Format(time.RFC3339), "holds": "a field this build never wrote",
	})

	out, _, err := execute(t, "coordinator", "intents", "--repo", "acme/app", "--json")
	if err == nil {
		t.Fatalf("a record this build cannot decode was printed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("an undecodable record = %v, want the decoder's own refusal", err)
	}
	// The refusal is the stored status's own, so it carries that command's exit
	// code: a record this build cannot decode is a failure that is not the
	// caller's argv, and §6.16 is the sentence a console reads it from.
	if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
		t.Errorf("an undecodable record = exit %d, %v; want exit %d", code, err, fcli.ExitRemoteError)
	}
	if out != "" {
		t.Errorf("an undecodable record printed a document:\n%s", out)
	}
}

func TestCoordinatorIntentsSinceBoundsTheDeclarations(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	now := fcli.LsNow()
	f.declare(t, "acme/app", now.Add(-48*time.Hour), coordinator.Intent{DeclaredBy: "person-1", Branch: "fugaro/20261031-120000-old1"})
	f.declare(t, "acme/app", now.Add(-time.Hour), coordinator.Intent{DeclaredBy: "person-2", Branch: "fugaro/20261102-120000-new1"})

	recent := f.read(t, "coordinator", "intents", "--all", "--since", "24h", "--json")
	if len(recent.Repos) != 2 || len(recent.Repos[0].Intents) != 1 {
		t.Fatalf("--since 24h printed %+v, want the one declaration made inside the window", recent.Repos)
	}
	if declaredBy := string(recent.Repos[0].Intents[0]["declared_by"]); declaredBy != `"person-2"` {
		t.Errorf("--since 24h printed %s, want the declaration made an hour ago", declaredBy)
	}
	// 0 is no bound, and it is the default: a declaration is a tiny document,
	// the sweep keeps the newest few hundred of them, and a window that
	// defaulted to hiding the rest would be a fact a reader never sees.
	all := f.read(t, "coordinator", "intents", "--all", "--json")
	if len(all.Repos[0].Intents) != 2 {
		t.Errorf("--since 0 printed %d declarations, want every one the store holds", len(all.Repos[0].Intents))
	}
}

// TestCoordinatorIntentsReadsWithTheCredentialsServiceSet: §6.16's own
// sentence, tested — with the installation's credentials service on, this read
// still succeeds where the host read refuses. The service holds the App's key
// (C2.2, D145), so §6.13's project-wide read has no credential here to mint its
// token with and is refused (TestCoordinatorStatusFromStoreWithService is that
// half); this read mints nothing and opens nothing, so the caller's own
// database credential serves it and it answers the same in that environment as
// in any other. Without this, "the console child needs no key" is a claim with
// nothing behind it on the ordinary installation — the one that keeps its key
// in the service.
func TestCoordinatorIntentsReadsWithTheCredentialsServiceSet(t *testing.T) {
	f := newCoordinatorIntentsFixture(t)
	at := fcli.LsNow().Add(-time.Hour).UTC().Truncate(time.Second)
	f.declare(t, "acme/app", at, coordinator.Intent{DeclaredBy: "person-1", Release: "0.13.0"})
	noStoreOpens(t)
	// The environment that makes the difference: the service holds the App's
	// key, and this process holds no App credential of its own either.
	t.Setenv(providers.EnvGitHubAppID, "")
	t.Setenv(providers.EnvGitHubAppKey, "")
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example.com/")

	read := f.read(t, "coordinator", "intents", "--all", "--json")
	if len(read.Repos) != 2 {
		t.Fatalf("with the credentials service set, the read printed %d repositories, want both", len(read.Repos))
	}
	if len(read.Repos[0].Intents) != 1 {
		t.Fatalf("with the credentials service set, %s printed %d declarations, want the one it holds",
			read.Repos[0].Repo, len(read.Repos[0].Intents))
	}
	if declaredBy := string(read.Repos[0].Intents[0]["declared_by"]); declaredBy != `"person-1"` {
		t.Errorf("the declaration printed as %s, want the one the store holds", declaredBy)
	}
	if len(read.Repos[1].Intents) != 0 {
		t.Errorf("%s printed %d declarations, want the empty list it holds", read.Repos[1].Repo, len(read.Repos[1].Intents))
	}
	// The declarations are the ones the store holds, read through the same
	// document shape a console decodes: a command that served this environment
	// by minting, or by asking the service for a run's token, would fail above
	// rather than print anything at all.
	if want := at.Format(time.RFC3339); string(read.Repos[0].Intents[0]["at"]) != `"`+want+`"` {
		t.Errorf("the declaration's at = %s, want %s", read.Repos[0].Intents[0]["at"], want)
	}
}
