package runner

// T4.6's run-side half: the mode decides what the run files, and nothing else
// (docs/rigor-modes/implementation-plan.md T4.6, reconciliation.md D30, D19).
//
// The table itself is internal/rigor's (rigor.Files); what is asserted here is
// that the run reads it. A run at a mode whose row files nothing writes no
// item, commits nothing and leaves its branch exactly as it was — the tree and
// the pull request body a Critical run produces are the ones main produces —
// while the modes whose row files one file the round they left open, and
// Hotfix files its follow-up whatever it left open. An item the owner filed by
// hand is never touched, whatever mode the run is at, and a repository that
// configures no rigor files nothing at all.
//
// Nothing here reaches a network, a cloud project or a clock: the day is
// injected (filedRunAt).

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// openRound is the review round the cases below leave open: the last senior
// review, a `changes` verdict, three findings — what a run files an item for
// when its mode's row files one.
var openRound = runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3}

// modeBase is a repository whose base branch pins one mode and declares no
// area rule, so the run's own paths match nothing and the mode it resolves at
// finalize is exactly the one the file names (D22's third moment reads the
// run's diff, and a matching rule would outrank the default). An empty block
// is a repository that configures no rigor at all, which is D19's case.
func modeBase(t *testing.T, block string) string {
	t.Helper()
	const head = "version: 1\nproject: aurora\ngit: { provider: github }\n"
	const tail = "workflows:\n  server: { commands: { build: make, test: make test } }\n"
	return testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": head + block + tail})
}

// modeRun is one run at finalize, cloned from a remote of its own whose base
// pins mode, and resolved from its own work. It returns the run and the path
// of the remote it pushes to.
func modeRun(t *testing.T, mode string, reviews ...runstore.ReviewSummary) (*run, string) {
	t.Helper()
	block := ""
	if mode != "" {
		block = "rigor: { default: " + mode + " }\n"
	}
	remote := modeBase(t, block)
	return filedRunAt(t, remote, "fugaro/x", "20261024-120000-abcd", reviews...), remote
}

// filedItemNames are the item files the branch the run pushes holds, read from
// the remote: what a reviewer of the pull request sees. A file written but not
// committed is a file no pull request carries, which is the difference these
// cases exist for.
func filedItemNames(t *testing.T, remote, branch string) []string {
	t.Helper()
	entries := testutil.Git(t, remote, "ls-tree", "--name-only", branch, backlog.Dir+"/")
	var names []string
	for _, path := range strings.Fields(entries) {
		if base := filepath.Base(path); base != backlog.IndexName && base != backlog.FormatName {
			names = append(names, base)
		}
	}
	return names
}

// TestPOCFilesNothingAndItsBodyLineIsTheWholeRecord: POC's row files nothing
// of its own (D30). The run leaves no item, moves no commit, and its pull
// request body's line — the review line, which T4.5's status section already
// carries — is the whole record of what it left open; a person who wants an
// item asks for one with `fugaro backlog add`.
func TestPOCFilesNothingAndItsBodyLineIsTheWholeRecord(t *testing.T) {
	r, _ := modeRun(t, "poc", openRound)
	if got := r.reachedRigor(); got != rigor.POC {
		t.Fatalf("the run resolved %q; want poc, or the case proves nothing", got)
	}
	before := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD")

	if r.fileOpenItems(context.Background()) {
		t.Fatal("a POC run filed an item of its own")
	}
	if len(r.rec.Backlog) != 0 {
		t.Errorf("the record carries ids for items a POC run did not file: %v", r.rec.Backlog)
	}
	if after := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved from %s to %s for a run that filed nothing", before, after)
	}
	if _, err := os.Stat(filepath.Join(r.repo.Dir, backlog.Dir)); !os.IsNotExist(err) {
		t.Errorf("a POC run left %s behind (err %v)", backlog.Dir, err)
	}
	section := r.finalSection(false, "review round 2 still has 3 findings", nil)
	if strings.Contains(section, "filed ") {
		t.Errorf("a POC run's status section names an item it did not file:\n%s", section)
	}
	if !strings.Contains(section, "3 findings") {
		t.Errorf("the status section lost the review line that is POC's whole record:\n%s", section)
	}
}

// TestCriticalRunFilesNothing: Critical tolerates nothing (D7), so it has
// nothing of its own to file and D30's row says so. The branch and the pull
// request body a Critical run produces are the ones main produces — this is
// the case the whole plan's "Critical is byte-identical to today" claim rests
// on for the filing half (plan §5, §7.1).
func TestCriticalRunFilesNothing(t *testing.T) {
	r, remote := modeRun(t, "critical", openRound)
	if got := r.reachedRigor(); got != rigor.Critical {
		t.Fatalf("the run resolved %q; want critical, or the case proves nothing", got)
	}
	before := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD")

	if r.fileOpenItems(context.Background()) {
		t.Fatal("a Critical run filed an item of its own")
	}
	if len(r.rec.Backlog) != 0 {
		t.Errorf("the record carries ids for items a Critical run did not file: %v", r.rec.Backlog)
	}
	if after := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved from %s to %s for a run that filed nothing", before, after)
	}
	if err := r.repo.Push(context.Background(), r.rec.Branch); err != nil {
		t.Fatal(err)
	}
	if names := filedItemNames(t, remote, "fugaro/x"); len(names) != 0 {
		t.Errorf("the pushed branch holds %v; want no item at all", names)
	}
	if section := r.finalSection(false, "review round 2 still has 3 findings", nil); strings.Contains(section, "filed ") {
		t.Errorf("a Critical run's status section names an item it did not file:\n%s", section)
	}
}

// TestStartupAndProFileTheRoundTheyLeftOpen: the two modes that tolerate
// findings file one item per tolerated finding, in the same pull request as
// the change it was filed from (D30, D9). The two differ in their thresholds,
// not in how they file, so one case drives both.
func TestStartupAndProFileTheRoundTheyLeftOpen(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"startup", "pro"} {
		t.Run(mode, func(t *testing.T) {
			r, remote := modeRun(t, mode, openRound)
			if got := r.reachedRigor(); got != rigor.Mode(mode) {
				t.Fatalf("the run resolved %q; want %s, or the case proves nothing", got, mode)
			}
			if !r.fileOpenItems(ctx) {
				t.Fatalf("a %s run that left a round open filed nothing", mode)
			}
			if !reflect.DeepEqual(r.rec.Backlog, []string{"20261024-01"}) {
				t.Fatalf("the record's items = %v; want the id of the file it wrote", r.rec.Backlog)
			}
			if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
				t.Fatal(err)
			}
			names := filedItemNames(t, remote, "fugaro/x")
			if len(names) != 1 {
				t.Fatalf("the pushed branch holds %v; want the one item", names)
			}
			doc := testutil.Git(t, remote, "show", "fugaro/x:"+filepath.Join(backlog.Dir, names[0]))
			item, err := backlog.Parse(filepath.Join(backlog.Dir, names[0]), []byte(doc))
			if err != nil {
				t.Fatalf("what the run pushed is not an item the folder accepts: %v\n%s", err, doc)
			}
			if item.Mode != mode {
				t.Errorf("mode = %q; want the mode the run resolved (D27)", item.Mode)
			}
			if !strings.Contains(item.Body, "Round 2") || !strings.Contains(item.Body, "3 findings") {
				t.Errorf("the item does not name the round and the findings it was filed for:\n%s", item.Body)
			}
		})
	}
}

// TestUnconfiguredRunFilesNothing: filing is opt-in per repository (the
// owner's ruling of 2026-10-24, R1). A repository whose fugaro.yaml has no
// rigor: block, under no project-layer rigor default, still resolves Pro —
// D19's default, which is what its budget fraction reads (T6.1) — but its run
// files nothing: no item, no commit, no docs/backlog/ in the tree it pushes, no
// id on the record and no backlog part in the pull request body.
func TestUnconfiguredRunFilesNothing(t *testing.T) {
	r, remote := modeRun(t, "", openRound)
	if got := r.reachedRigor(); got != rigor.Default {
		t.Fatalf("a repository with no rigor: block resolved %q; want the default %q (D19)", got, rigor.Default)
	}
	before := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD")

	if r.fileOpenItems(context.Background()) {
		t.Fatal("an unconfigured repository's run filed an item: filing starts only when a repository opts in with a rigor block")
	}
	if len(r.rec.Backlog) != 0 {
		t.Errorf("the record carries ids for items an unconfigured run did not file: %v", r.rec.Backlog)
	}
	if after := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved from %s to %s for a run that filed nothing", before, after)
	}
	if err := r.repo.Push(context.Background(), r.rec.Branch); err != nil {
		t.Fatal(err)
	}
	if tree := testutil.Git(t, remote, "ls-tree", "-r", "--name-only", "fugaro/x"); strings.Contains(tree, backlog.Dir+"/") {
		t.Errorf("the pushed tree holds %s:\n%s", backlog.Dir, tree)
	}
	if section := r.finalSection(false, "review round 2 still has 3 findings", nil); strings.Contains(section, "filed ") {
		t.Errorf("an unconfigured run's status section names an item it did not file:\n%s", section)
	}
}

// TestAnEmptyRigorBlockOptsIn: the opt-in is the block itself, so a
// repository that writes `rigor: {}` — a block that sets no mode and no area —
// resolves the same Pro as one that writes nothing, and files as Pro does.
func TestAnEmptyRigorBlockOptsIn(t *testing.T) {
	remote := modeBase(t, "rigor: {}\n")
	r := filedRunAt(t, remote, "fugaro/x", "20261024-120000-abcd", openRound)
	if got := r.reachedRigor(); got != rigor.Default {
		t.Fatalf("an empty rigor: block resolved %q; want the default %q (D19)", got, rigor.Default)
	}
	if !r.fileOpenItems(context.Background()) {
		t.Fatal("a repository that wrote a rigor: block filed nothing for the round it left open")
	}
	if !reflect.DeepEqual(r.rec.Backlog, []string{"20261024-01"}) {
		t.Errorf("the record's items = %v; want the id of the file it wrote", r.rec.Backlog)
	}
	if got := r.filedMode(); got != string(rigor.Default) {
		t.Errorf("the item records mode %q; want %q, the mode the run resolved", got, rigor.Default)
	}
}

// TestHotfixFilesItsFollowUpItemAlways: Hotfix's row is the one that files
// when nothing was tolerated — the ruling's "a follow-up item for what the
// hotfix deferred", because the fix it ships is the expedient one and the
// proper one is owed whatever the review round did (D30, D15). The item names
// the round when there was one, and the deferral itself when there was not.
//
// A hotfix that also left a round open owes both — the deferral is not the
// round's residue, and a run that could not satisfy its review is the one with
// least to show for itself — so it files the two, in one commit, and the
// record carries both ids.
func TestHotfixFilesItsFollowUpItemAlways(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		reviews []runstore.ReviewSummary
		want    []string
	}{
		{"it left a round open", []runstore.ReviewSummary{openRound}, []string{"revisit this hotfix", "review round 2 left 3 findings open"}},
		{"it tolerated nothing", []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship", Findings: 3}}, []string{"revisit this hotfix"}},
		{"it was never reviewed", nil, []string{"revisit this hotfix"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, remote := modeRun(t, "hotfix", tc.reviews...)
			if got := r.reachedRigor(); got != rigor.Hotfix {
				t.Fatalf("the run resolved %q; want hotfix, or the case proves nothing", got)
			}
			if !r.fileOpenItems(ctx) {
				t.Fatal("a hotfix filed nothing: its row files one whatever it left open (D30)")
			}
			if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
				t.Fatal(err)
			}
			names := filedItemNames(t, remote, "fugaro/x")
			if len(names) != len(tc.want) {
				t.Fatalf("the pushed branch holds %v; want %d item(s): %v", names, len(tc.want), tc.want)
			}
			// The filing is one commit, whatever it holds: the branch a
			// reviewer reads gained one commit, not one per item.
			if ahead := testutil.Git(t, r.repo.Dir, "rev-list", "--count", "main.."+r.rec.Branch); strings.TrimSpace(ahead) != "2" {
				t.Errorf("the branch is %s commits ahead of its base; want the run's work and one filing commit", ahead)
			}
			if len(r.rec.Backlog) != len(tc.want) {
				t.Errorf("the record's items = %v; want one id per item filed", r.rec.Backlog)
			}
			titles := make([]string, 0, len(names))
			for _, name := range names {
				doc := testutil.Git(t, remote, "show", "fugaro/x:"+filepath.Join(backlog.Dir, name))
				item, err := backlog.Parse(filepath.Join(backlog.Dir, name), []byte(doc))
				if err != nil {
					t.Fatalf("what the run pushed is not an item the folder accepts: %v\n%s", err, doc)
				}
				if item.Mode != "hotfix" {
					t.Errorf("mode = %q; want hotfix (D27)", item.Mode)
				}
				if !strings.Contains(item.Title, r.rec.RunID) {
					t.Errorf("title = %q; want the run's own id in it, so two runs compose two names (D26)", item.Title)
				}
				if !slices.Contains(r.rec.Backlog, item.ID) {
					t.Errorf("the record does not carry the id of the item it filed: %v, %s", r.rec.Backlog, item.ID)
				}
				titles = append(titles, item.Title)
			}
			for _, want := range tc.want {
				found := false
				for _, got := range titles {
					found = found || strings.Contains(got, want)
				}
				if !found {
					t.Errorf("the items the run filed %v; want one naming %q", titles, want)
				}
			}
		})
	}
}

// TestNoModeSuppressesTheOwnersItem: the table is what a *run* files of its
// own, and nothing else. An item the owner filed by hand — `fugaro backlog
// add`, which writes what it is given and reads no mode — is in the checkout
// before the run reaches finalize, and a run at the mode that files nothing of
// its own leaves it exactly as it was, still listed in the index.
func TestNoModeSuppressesTheOwnersItem(t *testing.T) {
	r, _ := modeRun(t, "poc", openRound)
	folder := filepath.Join(r.repo.Dir, backlog.Dir)
	owned, err := backlog.Write(folder, backlog.New{
		Title: "The payments retry loop has no cap", Mode: "none", Area: "repo",
		FiledBy: "octocat", FiledIn: "hand", Date: "2026-10-20",
		Body: backlog.Body("the retry loop has no cap", "the owner wrote it down", "", nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	itemPath := filepath.Join(folder, filepath.Base(owned.Path))
	indexPath := filepath.Join(folder, backlog.IndexName)
	itemBefore, err := os.ReadFile(itemPath)
	if err != nil {
		t.Fatal(err)
	}
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	if r.fileOpenItems(context.Background()) {
		t.Fatal("a POC run filed an item of its own")
	}
	itemAfter, err := os.ReadFile(itemPath)
	if err != nil {
		t.Fatalf("the owner's item is gone after a run at POC: %v", err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("the index is gone after a run at POC: %v", err)
	}
	if string(itemAfter) != string(itemBefore) {
		t.Errorf("the owner's item changed:\nbefore:\n%s\nafter:\n%s", itemBefore, itemAfter)
	}
	if string(indexAfter) != string(indexBefore) {
		t.Errorf("the index changed:\nbefore:\n%s\nafter:\n%s", indexBefore, indexAfter)
	}
	if !strings.Contains(string(indexAfter), owned.ID) {
		t.Errorf("the index no longer lists the owner's item %s:\n%s", owned.ID, indexAfter)
	}
}
