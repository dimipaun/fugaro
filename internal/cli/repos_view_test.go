package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
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
	v, err := viewErrOf(t, path, fake, canonical)
	if err != nil {
		t.Fatalf("newReposView: %v", err)
	}
	return v
}

// viewErrOf is viewOf with the error as the answer: the view refuses a config
// pointed at another project's sources, and a test that expects that refusal
// reads it here.
func viewErrOf(t *testing.T, path string, fake *gcpfake.RTDB, canonical string) (*reposView, error) {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return newReposView(context.Background(), lc, db, canonical)
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
	viewCatalogNodeBase(t, db, allow, "")
}

// viewCatalogNodeBase is viewCatalogNode with the node's own base_branch: the
// project's answer for a repository whose entry names none, and the layer behind
// the installation's own top-level one (config.EffectiveBaseBranch).
func viewCatalogNodeBase(t *testing.T, db *gcpfake.RTDB, allow []string, baseBranch string) {
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
	if baseBranch != "" {
		node["base_branch"] = baseBranch
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

// A row's image is resolved under the repository's provider, so one name at two
// providers is two rows with two images (RC51, D3370): the map is repo_images,
// keyed by provider/owner/name, and a lookup that dropped the provider would
// answer both rows with one entry — the collision L11 exists for.
//
// The project holds a GitHub and a Bitbucket acme/app: the GitHub one in this
// machine's config, the Bitbucket one admitted by the node's allow-list alone
// (the config's `repos:` is keyed by owner/name and cannot hold both, F2). The
// node's repo_images maps them to two entries.
//
// Mutation (verified, run and restored): read the map by the bare name —
// reposViewRowOf handing the resolver the legacy ImageMap instead of the
// mapping's request — and the Bitbucket row resolves the GitHub row's key, or
// the default entry, and this fails naming the entry it got.
func TestReposViewImageByProvider(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app", "bitbucket/acme/app", "bitbucket/acme/other"},
		"image_catalog": map[string]any{"entries": []any{
			map[string]any{"name": "go", "tags": []string{"node"}, "default": true,
				"source": map[string]any{"kind": "inline", "inline": "FROM scratch\n"}},
			map[string]any{"name": "tools", "tags": []string{"node"},
				"source": map[string]any{"kind": "inline", "inline": "FROM scratch\n"}},
		}},
		"repo_images": projectpolicy.RepoImages{
			"github/acme/app":    "go",
			"bitbucket/acme/app": "tools",
		},
	})

	v := viewOf(t, f.path, f.db, "")
	if v.Allow.State != "" {
		t.Fatalf("the published node did not read: %s", v.Allow.State)
	}
	for canonical, want := range map[string]string{
		"github/acme/app":    "go",
		"bitbucket/acme/app": "tools",
	} {
		r := viewRow(t, v, canonical)
		if r.ImageEntry != want || r.ImageSource != reposViewImageMap {
			t.Errorf("%s runs %q from %q, want %q from %q", canonical, r.ImageEntry, r.ImageSource, want, reposViewImageMap)
		}
	}
}

// A row's base branch is the branch a run of that repository starts from, which
// is what config.EffectiveBaseBranch resolves — the one function every layer
// resolves through, handed the same four arguments as launchBaseBranch — and not
// the node's value: the checkout's own git.base_branch where this working
// directory holds a checkout of the repository, else this machine's entry for
// it, else the project's (the installation's own top-level base_branch first,
// the published node's behind it), else main.
//
// The last of those is why the resolution is not the row's own: an installation
// that names nothing anywhere makes every row "" unless the default is applied,
// and "" is not a branch. RE1 and RE2 read this member as the branch a launch
// will be cut from.
//
// Mutation (verified, run and restored): the derivation this replaced — the
// repository's entry, else the project layer, else "", with no checkout read —
// fails "nothing names one" and "a checkout whose file writes none" ("" where a
// launch resolves main) and "the checkout's file" (release where the file names
// trunk). A second mutation, the project layer read from the node's value alone
// and "" on a node that did not answer, fails four of them: "the installation's
// own, over the node's" and "the repository's entry" read release where the
// installation names develop, and "the installation's own, with no node to read"
// and "the checkout's file" read main.
func TestReposViewBaseBranchIsTheLaunchesBranch(t *testing.T) {
	check := func(t *testing.T, v *reposView, want map[string]string) {
		t.Helper()
		if len(v.Repos) != len(want) {
			t.Fatalf("rows are %v, want the repositories named here (%d of them)", canonicalsOf(v), len(want))
		}
		for c, branch := range want {
			if got := viewRow(t, v, c).BaseBranch; got != branch {
				t.Errorf("%s reads base_branch %q, want %q", c, got, branch)
			}
		}
	}
	t.Run("nothing names one", func(t *testing.T) {
		f := newProjectFixture(t, "")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      config.DefaultBaseBranch,
			"bitbucket/acme/other": config.DefaultBaseBranch,
		})
	})
	t.Run("the installation's own, over the node's", func(t *testing.T) {
		f := newProjectFixture(t, "")
		setConfig(t, f, func(s string) string {
			return strings.Replace(s, "repos:\n", "base_branch: develop\nrepos:\n", 1)
		})
		viewCatalogNodeBase(t, f.db, []string{"github/acme/app"}, "release")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      "develop",
			"bitbucket/acme/other": "develop",
		})
	})
	t.Run("the published node's", func(t *testing.T) {
		f := newProjectFixture(t, "")
		viewCatalogNodeBase(t, f.db, []string{"github/acme/app"}, "release")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      "release",
			"bitbucket/acme/other": "release",
		})
	})
	t.Run("the installation's own, with no node to read", func(t *testing.T) {
		// No node is published: the layer is this machine's own answer, and a
		// node that did not answer is no reason to lose it for the default.
		f := newProjectFixture(t, "")
		setConfig(t, f, func(s string) string {
			return strings.Replace(s, "repos:\n", "base_branch: develop\nrepos:\n", 1)
		})
		v := viewOf(t, f.path, f.db, "")
		if v.Allow.State == "" {
			t.Fatal("the unpublished node did not leave a state sentence, so this case is not the one it means to be")
		}
		check(t, v, map[string]string{
			"github/acme/app":      "develop",
			"bitbucket/acme/other": "develop",
		})
	})
	t.Run("the repository's entry", func(t *testing.T) {
		f := newProjectFixture(t, "  acme/release: { provider: github, workflows: [web], base_branch: pin }\n")
		setConfig(t, f, func(s string) string {
			return strings.Replace(s, "repos:\n", "base_branch: develop\nrepos:\n", 1)
		})
		viewCatalogNodeBase(t, f.db, []string{"github/acme/app"}, "release")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      "develop",
			"bitbucket/acme/other": "develop",
			"github/acme/release":  "pin",
		})
	})
	t.Run("the checkout's file", func(t *testing.T) {
		f := newProjectFixture(t, "")
		setConfig(t, f, func(s string) string {
			return strings.Replace(strings.Replace(s, "repos:\n", "base_branch: develop\nrepos:\n", 1),
				"acme/app: { provider: github, workflows: [web] }",
				"acme/app: { provider: github, workflows: [web], base_branch: release }", 1)
		})
		catalogCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github, base_branch: trunk }\nworkflows:\n"+
			"  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      "trunk",
			"bitbucket/acme/other": "develop",
		})
	})
	t.Run("a checkout whose file writes none", func(t *testing.T) {
		f := newProjectFixture(t, "")
		setConfig(t, f, func(s string) string {
			return strings.Replace(s,
				"acme/app: { provider: github, workflows: [web] }",
				"acme/app: { provider: github, workflows: [web], base_branch: release }", 1)
		})
		catalogCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n"+
			"  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
		check(t, viewOf(t, f.path, f.db, ""), map[string]string{
			"github/acme/app":      "release",
			"bitbucket/acme/other": config.DefaultBaseBranch,
		})
	})
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

// The reads of what has run here are the ones that carry the project's identity
// — the runs bucket's marker and Firestore's installation mark — and a config
// pointed at another project's sources is refused rather than drawn (D2409).
// `project repos ls` stops on exactly this error, which its own comment calls
// the half of the command that can fail on the way to the data and
// TestProjectReposRefusesAnotherProjectsSources pins as the one way it could
// print another installation's data; this view is what `ls` and `status` read,
// so a state sentence here would draw the allow-list, the rows built from it
// and the named repository's credential containers for a project whose data is
// not this one's.
//
// The other half is the arm beside it, and the two are the distinction the
// answer rests on: a read that was attempted and did not answer is a state
// sentence with the rows still drawn, and a refusal is not.
//
// Mutation (verified, run and restored): flip the class refusedBySeen reads
// (ExitUserError to ExitRemoteError) — all three cases below fail: the two
// refusals answer with a view carrying the other project's allow-list, the rows
// built from it and the named repository's credential containers, and the read
// that did not answer is returned instead of stated.
func TestReposViewRefusesAnotherProjectsSources(t *testing.T) {
	t.Run("runs bucket", func(t *testing.T) {
		f := newProjectFixture(t, "")
		viewCatalogNode(t, f.db, []string{"github/acme/app"})
		writeRunsMarker(t, filepath.Join(f.dir, "runs"), "someone-else", "proj-9999")

		v, err := viewErrOf(t, f.path, f.db, "github/acme/app")
		if err == nil {
			t.Fatalf("another project's runs bucket was read and the view answered: %+v", v)
		}
		if !strings.Contains(err.Error(), "someone-else") {
			t.Errorf("the refusal does not name the project the bucket belongs to: %v", err)
		}
	})
	t.Run("installation mark", func(t *testing.T) {
		f := newSeenFixture(t)
		viewCatalogNode(t, f.db, []string{"github/acme/app"})
		f.fs.Set(budget.MetaCollection, budget.MarkDocument, map[string]any{"project": "someone-else", "version": int64(1)})

		v, err := viewErrOf(t, f.path, f.db, "github/acme/app")
		if err == nil {
			t.Fatalf("another project's Firestore database was read and the view answered: %+v", v)
		}
		if !strings.Contains(err.Error(), "someone-else") {
			t.Errorf("the refusal does not name the project the database belongs to: %v", err)
		}
	})
	t.Run("a source that did not answer", func(t *testing.T) {
		f := newProjectFixture(t, "")
		viewCatalogNode(t, f.db, []string{"github/acme/app"})
		setConfig(t, f, func(s string) string {
			return strings.Replace(s, "bucket_url: file://"+filepath.Join(f.dir, "runs"), "bucket_url: nosuch://runs", 1)
		})

		v := viewOf(t, f.path, f.db, "")
		if v.Seen.State == "" {
			t.Errorf("a bucket that could not be opened left no state sentence: %+v", v.Seen)
		}
		if got, want := canonicalsOf(v), []string{"bitbucket/acme/other", "github/acme/app"}; !slices.Equal(got, want) {
			t.Errorf("rows are %v, want the repositories this machine's config holds (%v)", got, want)
		}
	})
}

// A repository a published pattern admits is enrolled, and it has a row of its
// own while the answer is about it (D2409, D2411): the rows are built from
// concrete names, and `github/acme/*` names no repository — it admits them — so
// the listing has none for it. An answer that read enrolment off row existence
// alone would call such a repository not enrolled, with no state sentence,
// which is what RE1 turns into "not enrolled" and a nonzero exit; and its
// credential containers, the half a named repository is read for at all, would
// never be read.
//
// Mutation (verified, run and restored): drop the row newReposView builds for a
// named repository no row answers for — the named answer reads Enrolled:false
// with no state sentence, and the listing's rows are the two this machine's
// config holds. The credential member hangs off that row and cannot report
// without it: reading credentials only for a row the allow-list names (the
// `if found` arm gated on a concrete entry) leaves the row and reddens that
// member on its own.
func TestReposViewNamedPatternAdmittedIsEnrolled(t *testing.T) {
	f := newProjectFixture(t, "")
	// One pattern and no concrete entry, and the named repository is in no
	// config: the allow-list is the only half that could know it.
	viewCatalogNode(t, f.db, []string{"github/acme/*"})

	v := viewOf(t, f.path, f.db, "github/acme/aaa")
	if v.Named == nil {
		t.Fatal("the named answer is missing")
	}
	if !v.Named.Enrolled || v.Named.State != "" {
		t.Errorf("a repository the published pattern admits reads %+v, want it enrolled with no state sentence", v.Named)
	}
	// The named repository sorts between the two this machine's config holds, so
	// its row is one of the listing's rows in name order and not an append.
	want := []string{"bitbucket/acme/other", "github/acme/aaa", "github/acme/app"}
	if got := canonicalsOf(v); !slices.Equal(got, want) {
		t.Fatalf("rows are %v, want the named repository among them in name order (%v)", got, want)
	}
	row := viewRow(t, v, "github/acme/aaa")
	if !row.Published.Admitted || row.Published.Pattern != "github/acme/*" {
		t.Errorf("the row's published half is %+v, want the pattern that admits it", row.Published)
	}
	if row.Local.Present {
		t.Errorf("a repository only the allow-list admits reads as this machine's: %+v", row.Local)
	}
	if row.ImageEntry == "" {
		t.Errorf("the built row resolved no image, and it is the row every other repository gets: %+v", row)
	}
	// The credential member is the other half of what a name is for: this
	// machine names no Secret Manager endpoint, so the member is the guard's own
	// sentence with the empty list beside it — and nil is the read that never
	// happened.
	if row.Credentials == nil {
		t.Fatal("the named repository's credential containers were not read")
	}
	if row.Credentials.State == "" {
		t.Errorf("this machine names no Secret Manager endpoint and the credential member answered: %+v", row.Credentials)
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
//
// The read is scoped to the one repository the caller named (E27, D2415) — that
// repository's containers are the ones that come back — and the scoping is what
// this test now pins too: the fixture holds two containers the read must not
// return — another repository's, and a project-wide logical secret, which
// carries no repository label at all (infra/discover.go's own shape for one) —
// so a listing widened from gcp.LabelRepo to the project's managed containers
// returns three entries here and reddens the count below, where one seeded
// container left the widened answer identical to the scoped one.
//
// Mutation (verified, run and restored): replacing the read's filter
// map[string]string{gcp.LabelRepo: slug} with map[string]string{gcp.LabelManaged:
// gcp.ManagedValue} fails this test on the count — three containers, want the
// named repository's one — and would have put another repository's names, IDs
// and rotation ages into the row with the suite green.
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
	other := slugOf(t, "bitbucket", "acme/other")
	sm.Seed(gcp.SecretID(other, "bitbucket-app-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelRepo:    other,
		gcp.LabelSecret:  "bitbucket-app-key",
	}, []byte(stored))
	sm.Seed(gcp.ProjectSecretID("aurora", "claude-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelProject: "aurora",
		gcp.LabelSecret:  "claude-key",
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
		t.Fatalf("the credential member holds %+v, want the named repository's one container: another repository's and the project-wide one are not this read's", row.Credentials.Entries)
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
