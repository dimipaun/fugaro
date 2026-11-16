package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// reposLsJSON is the document `ls --json` prints, decoded the way a reader
// outside this binary decodes it: the members it needs by name, and the
// raw members beside them so a test can check the shape's own set.
type reposLsJSON struct {
	Version int                        `json:"version"`
	Project string                     `json:"project"`
	Repos   []reposLsJSONRow           `json:"repos"`
	Allow   reposLsJSONAllow           `json:"allow"`
	Seen    reposLsJSONSeen            `json:"seen"`
	Local   reposLsJSONLocal           `json:"local"`
	Raw     map[string]json.RawMessage `json:"-"`
}

type reposLsJSONRow struct {
	Canonical string `json:"canonical"`
	Provider  string `json:"provider"`
	Published struct {
		Admitted bool   `json:"admitted"`
		Pattern  string `json:"pattern"`
	} `json:"published"`
	Local struct {
		Present bool   `json:"present"`
		Key     string `json:"key"`
	} `json:"local"`
}

type reposLsJSONAllow struct {
	Entries    []string `json:"entries"`
	Generation string   `json:"generation"`
	State      string   `json:"state"`
}

type reposLsJSONSeen struct {
	Entries []reposLsJSONSeenRepo      `json:"entries"`
	Notes   []string                   `json:"notes"`
	State   string                     `json:"state"`
	Raw     map[string]json.RawMessage `json:"-"`
}

type reposLsJSONSeenRepo struct {
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Sources  []string `json:"sources"`
}

type reposLsJSONLocal struct {
	Repos []string `json:"repos"`
	State string   `json:"state"`
}

// reposLsJSONOf runs `ls --json` over the fixture and decodes what it printed,
// checking first that the whole of stdout is the one JSON object (a listing
// that printed a header line before it is not a document the console can read).
func reposLsJSONOf(t *testing.T) reposLsJSON {
	t.Helper()
	out, errOut, err := execute(t, "project", "repos", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	var doc reposLsJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json printed something that is not one JSON object (%v):\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &doc.Raw); err != nil {
		t.Fatal(err)
	}
	// The seen object's own members, for the window check below: the shape is
	// pinned by the members it carries, not by looking for a word that sounds
	// like a window.
	if err := json.Unmarshal(doc.Raw["seen"], &doc.Seen.Raw); err != nil {
		t.Fatalf("the seen member is not an object (%v)", err)
	}
	return doc
}

// TestReposLsJSONIsVersionedAndKeepsSeenApart: D2410's document, and D2409's
// separation inside it. `ls` had no `--json` at all before this task, so every
// half of this test is the red the task exists for.
//
// The separation is the load-bearing half: a repository the runs bucket names
// and the allow-list does not admit is a repository that has RUN here, and a
// document that carried it among `repos` would say it is enrolled — the
// misreading D2409 refuses ("a reader who took it for enrolment would draw the
// wrong conclusion"). The positive control is beside it, so a document with no
// rows at all cannot pass this test by being empty.
//
// Mutation (verified, run and restored): build the rows from the seen list as
// well (`v.Repos` extended with every seen name) — this test fails, the
// bucket-only repository appearing as a row.
func TestReposLsJSONIsVersionedAndKeepsSeenApart(t *testing.T) {
	f := newSeenFixture(t)
	viewCatalogNode(t, f.db, []string{"github/acme/app"})
	// A repository only the runs bucket names: not in this machine's config
	// (so no row of its own) and not admitted by the published list (so not a
	// concrete name the allow-list adds a row for).
	ghost := slugOf(t, "bitbucket", "acme/ghost")
	putRun(t, f.dir, ghost)

	doc := reposLsJSONOf(t)
	if doc.Version != reposLsDocVersion {
		t.Errorf("version = %d, want %d: the console reads the shape by this member, and a document without it is a shape that changes under its readers", doc.Version, reposLsDocVersion)
	}
	if doc.Project != "aurora" {
		t.Errorf("project = %q, want the installation the read was aimed at", doc.Project)
	}
	var seenNames, rowNames []string
	for _, e := range doc.Seen.Entries {
		seenNames = append(seenNames, e.Name)
	}
	for _, r := range doc.Repos {
		rowNames = append(rowNames, r.Canonical)
	}
	if !slices.Contains(seenNames, ghost) {
		t.Errorf("the repository the runs bucket names is not in seen (%v): the document dropped a reading of what has run here", seenNames)
	}
	if slices.Contains(rowNames, ghost) {
		t.Errorf("the repository only the runs bucket names is a row of repos (%v): a reading of what has run was drawn as an enrolment", rowNames)
	}
	// Every row is a repository the project knows, and the admitted one is
	// there — without this the emptiness above would pass on a document whose
	// rows were never built.
	for _, r := range doc.Repos {
		if strings.Contains(r.Canonical, ghost) {
			t.Errorf("the seen-only repository shaped a row (%q)", r.Canonical)
		}
	}
	if !slices.Contains(rowNames, "github/acme/app") {
		t.Errorf("repos = %v, want a row for the repository the allow-list admits", rowNames)
	}
	if !slices.Contains(doc.Allow.Entries, "github/acme/app") {
		t.Errorf("allow.entries = %v, want the published entry list", doc.Allow.Entries)
	}
}

// TestReposLsSeenCarriesSourcesAndNoWindow: D2409's own distinction, and the
// member it forbids. The seen list is read from two sources and only one of
// them is windowed — the runs bucket's runs/<slug>/ prefixes are listed with no
// window at all (runstore.ListSlugs: "the window bounds a query rather than the
// history"), and only Firestore's spendDaily is read over seenWindowDays — so
// the document carries each entry's SOURCES, which are where and not when, and
// names no window over the list: a window-length member would label a
// runs-bucket entry older than it as seen inside the window.
//
// The shape is pinned as an exact member set rather than by looking for a word
// that sounds like a window: a member named `window_days` fails the same way
// one named `seen_since` would, and the set is what a reader outside this
// binary builds against.
//
// Mutation (verified, run and restored): add `"window_days": seenWindowDays` to
// the seen object — this test fails, the member the design forbids being the
// one thing it adds.
func TestReposLsSeenCarriesSourcesAndNoWindow(t *testing.T) {
	f := newSeenFixture(t)
	viewCatalogNode(t, f.db, []string{"github/acme/app"})
	appSlug := slugOf(t, "github", "acme/app")
	putRun(t, f.dir, appSlug)
	f.spent(t, budget.DayDate(budget.Day(f.now)), appSlug, "acme/app")
	// A repository only the bucket names and this machine's config does not, so
	// it stays the slug it was stored under and one source is all it has.
	onlyBucket := slugOf(t, "bitbucket", "acme/ghost")
	putRun(t, f.dir, onlyBucket)

	doc := reposLsJSONOf(t)

	// The document's own members, and the seen object's: no window over the
	// list, whatever it would be called.
	wantTop := []string{"allow", "local", "project", "repos", "seen", "version"}
	var gotTop []string
	for k := range doc.Raw {
		gotTop = append(gotTop, k)
	}
	slices.Sort(gotTop)
	if !slices.Equal(gotTop, wantTop) {
		t.Errorf("the document carries %v, want exactly %v: a member over the whole seen list would be a window the runs-bucket half does not have", gotTop, wantTop)
	}
	wantSeen := []string{"entries", "notes", "state"}
	var gotSeen []string
	for k := range doc.Seen.Raw {
		gotSeen = append(gotSeen, k)
	}
	slices.Sort(gotSeen)
	if !slices.Equal(gotSeen, wantSeen) {
		t.Errorf("the seen object carries %v, want exactly %v: its two sources are not one window, so no window-length member belongs here", gotSeen, wantSeen)
	}

	byName := map[string]reposLsJSONSeenRepo{}
	for _, e := range doc.Seen.Entries {
		byName[e.Name] = e
	}
	both, ok := byName["github/acme/app"]
	if !ok {
		t.Fatalf("seen = %v, want the repository both sources name", doc.Seen.Entries)
	}
	if !slices.Contains(both.Sources, sourceRunsBucket) || !slices.Contains(both.Sources, sourceSpendDaily) {
		t.Errorf("the entry both sources name carries %v, want both %q and %q: the sources are where an entry was read, and a document without them cannot say what a window would have meant", both.Sources, sourceRunsBucket, sourceSpendDaily)
	}
	one, ok := byName[onlyBucket]
	if !ok {
		t.Fatalf("seen = %v, want the repository only the bucket names", doc.Seen.Entries)
	}
	if len(one.Sources) != 1 || one.Sources[0] != sourceRunsBucket {
		t.Errorf("the entry only the runs bucket names carries %v, want exactly [%q]", one.Sources, sourceRunsBucket)
	}
	// Every entry names at least one source, and every source is one of the
	// two the listing reads: an empty list would read as an entry that was
	// never read from anywhere.
	for _, e := range doc.Seen.Entries {
		if len(e.Sources) == 0 {
			t.Errorf("the entry %q carries no source at all", e.Name)
		}
		for _, s := range e.Sources {
			if s != sourceRunsBucket && s != sourceSpendDaily {
				t.Errorf("the entry %q names the source %q, which is not one of the two this listing reads", e.Name, s)
			}
		}
	}
}

// TestReposLsRowNamesPublishedAndLocalSeparately: D2409's two halves, as two
// cells of one row. A repository admitted by a pattern, one admitted by its own
// entry, one admitted that this machine has never configured and one configured
// that nothing admits are four different states, and each pair of cells has to
// print what it is: folded into one column ("enrolled"/"not enrolled") the four
// would be two figures and an operator could not tell a repository that runs
// from one that does not.
//
// Mutation (verified, run and restored): print the published cell as
// `enrolled`/`not enrolled` alone (the two halves folded into one boolean) —
// this test fails on the pattern row, whose published cell no longer says which
// entry admits it.
func TestReposLsRowNamesPublishedAndLocalSeparately(t *testing.T) {
	// A repository on the other provider, so the one pattern on the allow-list
	// (github/acme/*) does not admit it: the row that is configured here and
	// admitted by nothing.
	f := newProjectFixture(t, "  acme/plain: { provider: bitbucket, workflows: [web] }\n")
	viewCatalogNode(t, f.db, []string{"github/acme/*", "bitbucket/acme/other", "github/acme/extra"})

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, tc := range []struct {
		name      string
		published string
		local     string
	}{
		// Admitted by the pattern, and configured here: the published cell names
		// the entry that admits it, which is not its own name.
		{"github/acme/app", "published: github/acme/*", "local: repos: acme/app"},
		// Admitted by its own entry.
		{"bitbucket/acme/other", "published: bitbucket/acme/other", "local: repos: acme/other"},
		// Admitted, and this machine has never configured it: it runs, and it is
		// not in this machine's config.
		{"github/acme/extra", "published: github/acme/extra", "local: not in this machine's config"},
		// Configured here, and nothing admits it: the reverse state.
		{"bitbucket/acme/plain", "published: not admitted", "local: repos: acme/plain"},
	} {
		line := listLine(t, out, tc.name)
		for _, want := range []string{tc.published, tc.local} {
			if !strings.Contains(line, want) {
				t.Errorf("the row for %s does not say %q: %s", tc.name, want, line)
			}
		}
	}
	// The same two halves in the document, from the same read: a row's
	// published entry is the pattern's where a pattern admits it, and its local
	// half is this machine's own key.
	doc := reposLsJSONOf(t)
	rows := map[string]reposLsJSONRow{}
	for _, r := range doc.Repos {
		rows[r.Canonical] = r
	}
	for _, tc := range []struct {
		name     string
		pattern  string
		present  bool
		key      string
		admitted bool
	}{
		{"github/acme/app", "github/acme/*", true, "acme/app", true},
		{"bitbucket/acme/other", "bitbucket/acme/other", true, "acme/other", true},
		{"github/acme/extra", "github/acme/extra", false, "", true},
		{"bitbucket/acme/plain", "", true, "acme/plain", false},
	} {
		r, ok := rows[tc.name]
		if !ok {
			t.Errorf("the document has no row for %s (%v)", tc.name, rows)
			continue
		}
		if r.Published.Admitted != tc.admitted || r.Published.Pattern != tc.pattern {
			t.Errorf("%s: published = {admitted: %v, pattern: %q}, want {admitted: %v, pattern: %q}", tc.name, r.Published.Admitted, r.Published.Pattern, tc.admitted, tc.pattern)
		}
		if r.Local.Present != tc.present || r.Local.Key != tc.key {
			t.Errorf("%s: local = {present: %v, key: %q}, want {present: %v, key: %q}", tc.name, r.Local.Present, r.Local.Key, tc.present, tc.key)
		}
	}
}

// TestReposLsUnreadablePolicyIsAStateNotAnEmptyList: a policy read that did not
// answer is a state, and never an empty allow-list. The two are different facts
// about the project — the first is a list nobody could read, the second is a
// project that admits nothing — and the listing's own empty-list line is a
// sentence about the second: printed over the first, it tells an operator their
// project admits no repository when the truth is that this machine could not
// ask.
//
// It is not the command's failure either: `ls` prints what it has, with the
// sentence saying which member is missing (reposViewAllow's own rule, F14).
//
// Mutation (verified, run and restored): let the read's failure be an empty
// entry list with no state (reposViewAllowOf's `case err != nil` returning the
// zero value) — this test fails, the empty-list line printed over a read that
// did not answer.
func TestReposLsUnreadablePolicyIsAStateNotAnEmptyList(t *testing.T) {
	f := newProjectFixture(t, "")
	viewCatalogNode(t, f.db, []string{"github/acme/app"})
	f.db.DenyRead(projectpolicy.PathShared())

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("a policy read that did not answer failed the listing: %v\n%s", err, out)
	}
	if !strings.Contains(out, "allow_repos") || !strings.Contains(out, "could not be read") {
		t.Errorf("the listing does not say that the published allow-list could not be read:\n%s", out)
	}
	const emptyList = "(empty: a published policy with an empty allow-list admits no repository at all)"
	if strings.Contains(out, emptyList) {
		t.Errorf("the listing draws a read that did not answer as a published policy that admits nothing:\n%s", out)
	}
	// The same distinction in the document: the state sentence is a member, and
	// the entry list is empty beside it rather than absent.
	doc := reposLsJSONOf(t)
	if doc.Allow.State == "" {
		t.Error("allow.state is empty over a read that did not answer: a reader cannot tell an unread list from an empty one")
	}
	if !strings.Contains(doc.Allow.State, "could not be read") {
		t.Errorf("allow.state = %q, want the sentence for a read that did not answer", doc.Allow.State)
	}
	if doc.Allow.Entries == nil || len(doc.Allow.Entries) != 0 {
		t.Errorf("allow.entries = %v, want an empty list and not null: the state sentence is what tells it apart from a policy that admits nothing", doc.Allow.Entries)
	}
}
