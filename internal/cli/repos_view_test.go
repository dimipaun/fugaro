package cli

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// viewOf builds the enrolment view over the fixture, the way a command does:
// the database opened over the fixture's fake, and the config it wrote read
// back the way every command reads it.
func viewOf(t *testing.T, path string, fake *gcpfake.RTDB, canonical string) *reposView {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	v, err := newReposView(context.Background(), lc, db, canonical)
	if err != nil {
		t.Fatalf("newReposView: %v", err)
	}
	return v
}

// viewRow is the row for one canonical name, and a failure naming the rows that
// are there.
func viewRow(t *testing.T, v *reposView, canonical string) reposViewRepo {
	t.Helper()
	for _, r := range v.Repos {
		if r.Canonical == canonical {
			return r
		}
	}
	t.Fatalf("no row for %s in %v", canonical, canonicalsOf(v))
	return reposViewRepo{}
}

func canonicalsOf(v *reposView) []string {
	out := make([]string, 0, len(v.Repos))
	for _, r := range v.Repos {
		out = append(out, r.Canonical)
	}
	return out
}

// viewCatalogNode is a published node with the fixture's catalog and a map of
// one repository, which is where a project's image selection is written
// (D1140: the node is `fugaro update`'s copy of the config file).
func viewCatalogNode(t *testing.T, db *gcpfake.RTDB, allow []string) {
	t.Helper()
	node := map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": allow,
		"image_catalog": map[string]any{"entries": []any{
			map[string]any{
				"name": "base", "tags": []string{"node", "postgres"}, "default": true,
				"source": map[string]any{"kind": "dockerfile", "repo": "acme/app", "path": ".fugaro/base.Dockerfile"},
			},
			map[string]any{
				"name": "tools", "tags": []string{"node"},
				"source": map[string]any{"kind": "inline", "inline": "FROM scratch\n"},
			},
		}},
		"image_map": map[string]any{"acme/other": "base"},
	}
	db.Set(projectpolicy.PathShared(), node)
}

// The view names WHICH answer chose a repository's image, and it names it
// differently for each of the four the resolver can give (D2409): the
// workflow's own catalog reference, the project's image map, the catalog's
// default entry, and — where the project names none of the three — the image
// this project builds for the repository.
//
// This is the red the task exists for. catalog.JobImage answers `catalog` for
// the first three of them (they differ only by Selection.Reference, which the
// resolver's own docs call out as the thing it does NOT distinguish), so a
// view that printed the resolver's Source would print one word for three
// different reasons and a person could not tell their own image_name: from the
// map they wrote from a default nobody chose.
//
// Mutation (verified, run and restored): return string(sel.Source) from
// reposViewSourceOf — this fails on all three rows, each named "catalog".
func TestReposViewImageSourceIsNamedOnce(t *testing.T) {
	f := newProjectFixture(t, "  acme/plain: { provider: github, workflows: [web] }\n")
	viewCatalogNode(t, f.db, []string{"github/acme/app"})
	// The one checkout: its fugaro.yaml is where a workflow's own image_name:
	// lives, and it is that repository's that the view reads it from.
	catalogCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n"+
		"  web: { image_name: tools, commands: { build: sh build.sh, test: sh test.sh } }\n")

	v := viewOf(t, f.path, f.db, "")
	if v.Allow.State != "" {
		t.Fatalf("the published node did not read: %s", v.Allow.State)
	}
	want := map[string]struct {
		entry  string
		source reposViewImageSource
	}{
		"github/acme/app":      {"tools", reposViewImageWorkflow},
		"bitbucket/acme/other": {"base", reposViewImageMap},
		"github/acme/plain":    {"base", reposViewImageDefault},
	}
	if len(v.Repos) != len(want) {
		t.Fatalf("rows are %v, want one per repository", canonicalsOf(v))
	}
	seen := map[reposViewImageSource]string{}
	for canonical, w := range want {
		r := viewRow(t, v, canonical)
		if r.ImageEntry != w.entry || r.ImageSource != w.source {
			t.Errorf("%s runs %q from %q, want %q from %q", canonical, r.ImageEntry, r.ImageSource, w.entry, w.source)
		}
		if len(r.Workflows) == 0 {
			t.Errorf("%s has no workflow entry", canonical)
			continue
		}
		if r.Workflows[0].Source != w.source || r.Workflows[0].Entry != w.entry {
			t.Errorf("%s's workflow says %q from %q, want %q from %q",
				canonical, r.Workflows[0].Entry, r.Workflows[0].Source, w.entry, w.source)
		}
		if r.Workflows[0].State != "" {
			t.Errorf("%s's workflow was refused: %s", canonical, r.Workflows[0].State)
		}
		if other, ok := seen[w.source]; ok {
			t.Errorf("%s and %s are both reported as %q: the sources are not named apart", other, canonical, w.source)
		}
		seen[w.source] = canonical
	}
	if len(seen) != 3 {
		t.Errorf("three repositories resolved through three different answers, and the view names %d of them", len(seen))
	}
}

// The seen list is a member of its own and never creates a row (D2409). What
// has run here is a reading of the runs bucket and of Firestore's spendDaily,
// and neither enrolls a repository: a slug no configuration names has run and
// is not one of the project's repositories, while a repository this machine's
// config holds and nothing has ever run is one.
//
// The two are different questions — is it enrolled, has it run — and a view
// that merged them would answer the first with the second: a repository
// deleted from the allow-list but still in the bucket would read as enrolled,
// and one just added would read as never enrolled.
//
// Mutation (verified, run and restored): append a row per seen entry in
// newReposView — this fails on the ghost slug, which becomes a row.
func TestReposViewSeenIsNotEnrolment(t *testing.T) {
	f := newSeenFixture(t)
	viewCatalogNode(t, f.db, []string{"github/acme/app"})
	// A run in the bucket alone, and a spendDaily document alone: the two
	// sources the seen list is the union of, each answering with something
	// no configuration names.
	putRun(t, f.dir, slugOf(t, "bitbucket", "acme/other"))
	f.spent(t, budget.DayDate(budget.Day(f.now)), "ghost-0123", "acme/ghost")

	v := viewOf(t, f.path, f.db, "")
	if v.Seen.State != "" {
		t.Fatalf("the seen sources did not read: %s", v.Seen.State)
	}
	var ghost bool
	for _, s := range v.Seen.Entries {
		if s.Name == "ghost-0123" {
			ghost = true
			if !slices.Contains(s.Sources, sourceSpendDaily) {
				t.Errorf("the ghost entry does not name the source it was read from: %v", s.Sources)
			}
		}
	}
	if !ghost {
		t.Fatalf("the spendDaily-only repository is not in the seen list: %+v", v.Seen.Entries)
	}
	if got, want := canonicalsOf(v), []string{"bitbucket/acme/other", "github/acme/app", "github/acme/idle"}; !slices.Equal(got, want) {
		t.Errorf("rows are %v, want the repositories this machine and the allow-list name (%v)", got, want)
	}
	for _, r := range v.Repos {
		if strings.Contains(r.Canonical, "ghost") {
			t.Errorf("a repository only the seen window knows became a row: %s", r.Canonical)
		}
	}
	r := viewRow(t, v, "github/acme/idle")
	if !r.Local.Present || r.Published.Admitted {
		t.Errorf("acme/idle is this machine's and not the allow-list's, and the row reads local=%v published=%v", r.Local, r.Published)
	}
	// Both halves were read here, so a name no row answers for is decided:
	// not enrolled, and with no state sentence — the answer RE1 turns into
	// its own line and a nonzero exit, rather than the "could not be decided"
	// the test above pins.
	named := viewOf(t, f.path, f.db, "github/acme/ghost")
	if named.Named == nil || named.Named.Enrolled || named.Named.State != "" {
		t.Errorf("a repository neither half names is not decided as not enrolled: %+v", named.Named)
	}
}

// A member that could not be read carries the sentence that says so, and
// never an empty object (F14, D2410): managedStatusDoc's rule, and for its
// reason — an allow-list printed empty because the node did not answer draws a
// project that admits nothing, and a config whose entries could not all be
// spelled as canonical names printed as the ones that could draws a machine
// that does not hold the repository.
//
// Both absences are here at once, because they are independent: no policy node
// is published at all, and one `repos:` entry carries no provider — the field
// whose value is the checkout's fugaro.yaml's to decide, and so one this
// machine's file cannot be read as a canonical name without.
//
// Mutation (verified, run and restored): drop the unreadable entry from
// reposViewLocalOf instead of naming it in State — this fails three times
// over, on the sentence, on the entry the sentence has to name, and on the
// marshalled document's own `local` member.
func TestReposViewAbsentMembersCarryTheirState(t *testing.T) {
	f := newProjectFixture(t, "  acme/broken: { workflows: [web] }\n")

	v := viewOf(t, f.path, f.db, "")
	if v.Allow.State == "" {
		t.Error("nothing is published and the allow-list carries no state sentence")
	}
	if len(v.Allow.Entries) != 0 || v.Allow.Generation != "" {
		t.Errorf("an unread allow-list carries entries or a generation: %+v", v.Allow)
	}
	if v.Local.State == "" {
		t.Error("an entry that cannot be spelled as a canonical name carries no state sentence")
	}
	if !strings.Contains(v.Local.State, "acme/broken") {
		t.Errorf("the local state sentence does not name the entry it is about: %s", v.Local.State)
	}
	if got, want := v.Local.Repos, []string{"bitbucket/acme/other", "github/acme/app"}; !slices.Equal(got, want) {
		t.Errorf("the readable repositories are %v, want %v", got, want)
	}
	for _, r := range v.Repos {
		if strings.Contains(r.Canonical, "broken") {
			t.Errorf("an entry that could not be read became a row: %s", r.Canonical)
		}
	}

	// A named repository no row answers for is "not enrolled" only when both
	// halves were read (D2411, managed status's rule). Here the allow-list
	// half did not answer, so the same absent row is a read this view cannot
	// make — and a caller that printed "not enrolled" would be telling an
	// operator that a policy nobody could read does not admit them.
	named := viewOf(t, f.path, f.db, "github/acme/ghost")
	if named.Named == nil || named.Named.Enrolled {
		t.Fatalf("a repository no row names reads as enrolled: %+v", named.Named)
	}
	if named.Named.Canonical != "github/acme/ghost" {
		t.Errorf("the named repository is %q, want the canonical name it was asked about", named.Named.Canonical)
	}
	if named.Named.State == "" {
		t.Error("an enrolment that could not be decided carries no state sentence")
	}

	// The shape, not only the builder: a consumer outside this binary reads
	// the document, and a member that is absent must not arrive as {}.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"allow", "local"} {
		m, ok := doc[member].(map[string]any)
		if !ok {
			t.Fatalf("%s is %T, not an object: %s", member, doc[member], b)
		}
		state, _ := m["state"].(string)
		if strings.TrimSpace(state) == "" {
			t.Errorf("%s carries no state sentence: %s", member, b)
		}
	}
	if _, ok := doc["named"]; ok {
		t.Errorf("a view built without a named repository carries one: %s", b)
	}
}

// listMember fails unless the member the path names is a list in the marshalled
// document, descending into a list of rows so that a member of every row is
// checked at once. doc is the document decoded, and label is how the member is
// written in a failure.
func listMember(t *testing.T, b []byte, doc any, label string, path ...string) {
	t.Helper()
	if len(path) == 0 {
		switch v := doc.(type) {
		case nil:
			t.Errorf("%s is null, and an empty member of the document is []: %s", label, b)
		case []any:
		default:
			t.Errorf("%s is %T, and a member of the document is a list: %s", label, v, b)
		}
		return
	}
	switch v := doc.(type) {
	case map[string]any:
		listMember(t, b, v[path[0]], label, path[1:]...)
	case []any:
		for _, e := range v {
			listMember(t, b, e, label, path...)
		}
	default:
		// A member a list of rows would have to be walked through is not a
		// list, which is the failure this test is for: the other members are
		// still worth reporting, so this reports rather than stops.
		t.Errorf("%s: the path to it is not a list of rows: %s", label, b)
	}
}

// Every list member of the document is a list, [] for the one that read nothing
// and never null (D2410). The two documents are read outside this binary, so a
// contract that says [] for one absence and null for the absence beside it
// makes every reader special-case members it cannot tell apart — which is what
// allow.entries, wrapped, did beside claude_accounts, which was not.
//
// The state sentence is not a substitute: an empty list is a true answer here
// (a machine that records no Claude account records none) as much as a failed
// read is, and both arrive in a member of the same kind.
//
// Mutation (verified, run and restored): drop the nonNil wraps in newReposView,
// reposViewRowOf, reposViewLocalOf and reposViewCredentialsOf, and the empty
// literal in reposViewAllowOf's fileOnly. This fails on seen.entries,
// allow.entries, repos[].workflows, repos[].claude_accounts and the named row's
// credentials.entries in the first document, and on repos, seen.entries,
// allow.entries and local.repos in the second. seen.notes is the one member the
// mutation leaves alone, and rightly: this machine's Firestore holds no history
// mark, so the note that says so fills the list and it was never empty here.
func TestReposViewEmptyListMembersAreLists(t *testing.T) {
	// One repository of this machine's config states no workflow and no Claude
	// account, nothing is published, nothing has run, and the one repository
	// the caller names reaches the credential member's own guard: the lists
	// below are empty in this one document.
	f := newProjectFixture(t, "  acme/bare: { provider: github }\n")

	check := func(v *reposView) map[string]any {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		for _, m := range []struct {
			label string
			path  []string
		}{
			{"repos", []string{"repos"}},
			{"repos[].workflows", []string{"repos", "workflows"}},
			{"repos[].claude_accounts", []string{"repos", "claude_accounts"}},
			{"allow.entries", []string{"allow", "entries"}},
			{"seen.entries", []string{"seen", "entries"}},
			{"seen.notes", []string{"seen", "notes"}},
			{"local.repos", []string{"local", "repos"}},
		} {
			listMember(t, b, doc, m.label, m.path...)
		}
		return doc
	}

	v := viewOf(t, f.path, f.db, "github/acme/bare")
	if v.Allow.State == "" || v.Seen.State != "" {
		t.Fatalf("the fixture is not the one this test is about: allow=%q seen=%q", v.Allow.State, v.Seen.State)
	}
	doc := check(v)

	// The one row the document carries a credential member for: a listing reads
	// none, and this machine names no Secret Manager endpoint, so the member is
	// the guard's own state sentence with the empty list beside it.
	var bare map[string]any
	for _, r := range doc["repos"].([]any) {
		if m := r.(map[string]any); m["canonical"] == "github/acme/bare" {
			bare = m
		}
	}
	if bare == nil {
		t.Fatalf("the named repository has no row: %v", doc["repos"])
	}
	creds, _ := bare["credentials"].(map[string]any)
	if creds == nil {
		t.Fatalf("the named row carries no credential member: %v", bare)
	}
	if state, _ := creds["state"].(string); state == "" {
		t.Errorf("the credential member answered, and this machine names no Secret Manager endpoint: %v", creds)
	}
	if _, ok := creds["entries"].([]any); !ok {
		t.Errorf("credentials.entries is %v, want the empty list it read: %v", creds["entries"], creds)
	}

	// A machine that holds no repository, which is the only document that can
	// empty the two members a project with a repository always fills: the
	// rows, and the config's own list.
	src, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := yamledit.SetEmptyMapping(src, []string{"repos"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	none := viewOf(t, f.path, f.db, "")
	if len(none.Repos) != 0 {
		t.Fatalf("a config that holds no repository built rows: %v", canonicalsOf(none))
	}
	check(none)
}

// The credential member carries presence, a name, a version's state and a
// rotation age — never a value, in any member (D2415). This pins it at the
// document: a value stored in the fixture's Secret Manager must appear nowhere
// in what the view marshals, and the row must still say what it found, or the
// test would pass on a view that read nothing at all.
//
// Mutation (verified, run and restored): read the container's value in
// reposViewCredentialsOf — one secrets.Access call, to put it in Name — and
// the fake refuses it by itself ("gcpfake: a client read the value of secret
// …"), before any assertion here has to notice. A view that needs a value to
// answer a row is what D2415 refuses, and the test is written so that the
// reading is the failure and not only the printing.
func TestReposViewCredentialsCarryNoValue(t *testing.T) {
	const stored = "not-a-secret-shaped-literal"
	f := newProjectFixture(t, "")
	sm := gcpfake.NewSecrets(t)
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "no_auth: true", "secret_manager: "+sm.URL+"/, no_auth: true", 1)
	})
	slug := slugOf(t, "github", "acme/app")
	sm.Seed(gcp.SecretID(slug, "github-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelRepo:    slug,
		gcp.LabelSecret:  "github-app-key",
	}, []byte(stored))

	v := viewOf(t, f.path, f.db, "github/acme/app")
	row := viewRow(t, v, "github/acme/app")
	if row.Credentials == nil {
		t.Fatal("the named repository carries no credential member")
	}
	if row.Credentials.State != "" {
		t.Fatalf("the credential containers did not read: %s", row.Credentials.State)
	}
	if len(row.Credentials.Entries) != 1 {
		t.Fatalf("the credential member holds %+v, want the one container", row.Credentials.Entries)
	}
	e := row.Credentials.Entries[0]
	if e.Name != "github-app-key" || e.ID != gcp.SecretID(slug, "github-app-key") || !e.Enabled || e.AgeDays == nil {
		t.Errorf("the credential row is %+v, want the container's name, ID, enabled state and age", e)
	}
	if sm.Accessed() != nil {
		t.Errorf("the view read a value: %v", sm.Accessed())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), stored) {
		t.Errorf("the view carries the stored value: %s", b)
	}
	// Every other row: a repository the caller did not name has no credential
	// member at all, so a listing cannot become one Secret Manager call per
	// repository.
	for _, r := range v.Repos {
		if r.Canonical != "github/acme/app" && r.Credentials != nil {
			t.Errorf("%s carries credentials, and only the named repository's were read: %+v", r.Canonical, r.Credentials)
		}
	}
}
