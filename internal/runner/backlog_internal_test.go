package runner

// The run-side half of plan T4.5: a run that reaches finalize with a review
// round it did not ship files the item for what it left open — into its own
// checkout, its own commit and therefore its own branch and pull request
// (D9) — records the id, and names the id in the status section.
//
// Every case drives a real checkout cloned from a real remote and asserts on
// the tree the remote holds, never on the working directory alone: a file
// this run wrote and did not commit is a file no reviewer's pull request
// carries, which is the difference the task exists for. Nothing here reaches
// a network, a cloud project or a clock: the day is injected.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// filingBase is the repository this file's runs are cut from: a bare remote
// whose main holds a fugaro.yaml naming one area rule, which the run's own
// work then matches.
func filingBase(t *testing.T) string {
	t.Helper()
	const base = `version: 1
project: aurora
git: { provider: github }
rigor:
  default: poc
  areas:
    - { name: payments, match: "internal/payments/**", mode: startup }
workflows:
  server: { commands: { build: make, test: make test } }
`
	return testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base})
}

// filedRunAt is a run at finalize that left one review round open: a checkout
// cloned from remote, on branch with the run's work committed, the mode
// resolved from that work, and a record whose reviews are given. runID is the
// run's own id, which is what two runs of one repository are told apart by.
func filedRunAt(t *testing.T, remote, branch, runID string, reviews ...runstore.ReviewSummary) *run {
	t.Helper()
	testutil.IsolateGit(t)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.CheckoutNewBranch(ctx, "main", branch); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"internal/payments/charge.go": "package payments\n"})
	if _, err := repo.CommitAll(ctx, "the run's work"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Git.BaseBranch = "main"
	r := &run{
		repo: repo, cfg: cfg, spec: &task.Spec{RequestedBy: "octocat"},
		rec: &runstore.Record{
			Version: 1, RunID: runID, Branch: branch, BaseBranch: "main",
			Status: runstore.StatusRunning, Outcome: runstore.OutcomeNone, Reviews: reviews,
		},
		d: Deps{Log: slog.New(slog.DiscardHandler)},
	}
	// The day the item is filed is injected rather than read from a clock, so
	// the id and the file's name are the same on every machine (D26, D27).
	r.d.Now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
	r.bootstrapRigor(ctx)
	r.recordChangedFiles(ctx)
	r.finalizeRigor()
	return r
}

// filedRun is one run at finalize, cloned from a remote of its own; it
// returns the run and the path of the remote it pushes to.
func filedRun(t *testing.T, reviews ...runstore.ReviewSummary) (*run, string) {
	t.Helper()
	remote := filingBase(t)
	return filedRunAt(t, remote, "fugaro/x", "20261024-120000-abcd", reviews...), remote
}

// itemNames are the item files a folder holds, sorted; the index and the
// format document are not items.
func itemNames(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, backlog.Dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range matches {
		if base := filepath.Base(m); base != backlog.IndexName && base != backlog.FormatName {
			names = append(names, base)
		}
	}
	sort.Strings(names)
	return names
}

// TestRunFilesTheItemsItLeftOpenIntoItsOwnBranch: a run whose last senior
// review did not ship writes the item, commits it, and pushes it — the file
// is in the commit the branch points at, its id is on the record, and the
// front matter names the run, the launcher, the mode the run resolved and the
// area its paths matched (D9, D26, D27).
func TestRunFilesTheItemsItLeftOpenIntoItsOwnBranch(t *testing.T) {
	r, remote := filedRun(t, runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3})
	ctx := context.Background()

	if !r.fileOpenItems(ctx) {
		t.Fatal("a run that left a round open filed nothing")
	}
	if !reflect.DeepEqual(r.rec.Backlog, []string{"20261024-01"}) {
		t.Fatalf("the record's items = %v, want the id of the file it wrote", r.rec.Backlog)
	}
	// The tree the run pushes, read from the remote: what a reviewer of the
	// pull request sees. A file written but not committed would fail here.
	if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		t.Fatal(err)
	}
	// The file's name carries the run's own id, so two runs that leave the
	// same round and the same count open cannot compose one name (D26).
	name := "20261024-01-run-20261024-120000-abcd-review-round-2-left-3-findings-open.md"
	doc := testutil.Git(t, remote, "show", "fugaro/x:"+filepath.Join(backlog.Dir, name))
	item, err := backlog.Parse(filepath.Join(backlog.Dir, name), []byte(doc))
	if err != nil {
		t.Fatalf("what the run pushed is not an item the folder accepts: %v\n%s", err, doc)
	}
	if item.Mode != "startup" {
		t.Errorf("mode = %q, want the mode the run resolved (D27)", item.Mode)
	}
	if item.Area != "payments" {
		t.Errorf("area = %q, want the area the run's paths matched (D27)", item.Area)
	}
	if item.FiledBy != "octocat" || item.FiledIn != "run:20261024-120000-abcd" {
		t.Errorf("front matter = %+v, want the launcher and the run it was filed from (D27)", item)
	}
	if item.Date != "2026-10-24" {
		t.Errorf("date = %q, want the day the run filed it (D27)", item.Date)
	}
	// The index is regenerated in the same commit, so the folder the run
	// pushes passes its own regenerate-and-compare guard (D28).
	index := testutil.Git(t, remote, "show", "fugaro/x:"+filepath.Join(backlog.Dir, backlog.IndexName))
	if !strings.Contains(index, item.ID) {
		t.Errorf("the pushed index does not list %s:\n%s", item.ID, index)
	}
	// And the pull request's own status section names it, beside the review
	// line that says what was left open.
	section := r.finalSection(false, "review round 2 still has 3 findings", nil)
	if !strings.Contains(section, "filed `20261024-01`") {
		t.Errorf("the status section does not name the filed item:\n%s", section)
	}
	if !strings.Contains(section, "3 findings") {
		t.Errorf("the status section lost the review line the id belongs beside:\n%s", section)
	}
}

// TestRunFilesNoItemWhenItsRoundShipped: the item is what a run *left* open,
// so a round that shipped, a round that found nothing, a run that ended after
// its first line, one that was never reviewed and one whose review was
// skipped all file nothing, leave the branch exactly as it was and write no
// field on the record.
func TestRunFilesNoItemWhenItsRoundShipped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reviews []runstore.ReviewSummary
	}{
		{"the round shipped", []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship", Findings: 3}}},
		{"the round found nothing", []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 0}}},
		{"the run ended after its first line", []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 2}}},
		{"the run was never reviewed", nil},
		{"the review was skipped", []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierSenior, Verdict: "none"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := filedRun(t, tc.reviews...)
			before := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD")

			if r.fileOpenItems(context.Background()) {
				t.Fatal("the run filed an item for something it did not leave open")
			}
			if len(r.rec.Backlog) != 0 {
				t.Errorf("the record carries ids for items it did not file: %v", r.rec.Backlog)
			}
			if after := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD"); after != before {
				t.Errorf("HEAD moved from %s to %s for a run that filed nothing", before, after)
			}
			if _, err := os.Stat(filepath.Join(r.repo.Dir, backlog.Dir)); !os.IsNotExist(err) {
				t.Errorf("a run that filed nothing left %s behind (err %v)", backlog.Dir, err)
			}
			if section := r.finalSection(false, "review round 2 still has 3 findings", nil); strings.Contains(section, "filed ") {
				t.Errorf("the status section names an item a run that filed nothing wrote:\n%s", section)
			}
		})
	}
}

// TestFilingRefusesAnItemTheFolderAlreadyHolds: the filing is a warning path —
// an item that cannot be written costs a follow-up, never the run — so a
// filing the folder refuses leaves the branch and the record exactly as they
// were and is not recorded as a filing.
func TestFilingRefusesAnItemTheFolderAlreadyHolds(t *testing.T) {
	r, remote := filedRun(t, runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3})
	ctx := context.Background()
	if !r.fileOpenItems(ctx) {
		t.Fatal("the first filing was not made")
	}
	before := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD")
	// The same item again: the second filing takes the day's next sequence
	// and meets the first one's title, which the folder refuses as the same
	// item filed twice (D26).
	r.rec.Backlog = nil
	if r.fileOpenItems(ctx) {
		t.Fatal("the folder's refusal was recorded as a filing")
	}
	if len(r.rec.Backlog) != 0 {
		t.Errorf("a refused filing was recorded: %v", r.rec.Backlog)
	}
	if after := testutil.Git(t, r.repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved from %s to %s for a refused filing", before, after)
	}
	if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		t.Fatal(err)
	}
	entries := testutil.Git(t, remote, "ls-tree", "--name-only", "fugaro/x", filepath.Join(backlog.Dir)+"/")
	if got := strings.Fields(entries); len(got) != 2 {
		t.Errorf("the pushed folder holds %v, want the item and its index", got)
	}
}

// TestTwoRunsFilingOneFolderTheSameDayBothKeepTheirItem: two runs of one
// repository, cut from one base, each ending its round with the same count on
// the same day. A run reads only its own checkout, so both allocate the day's
// first id and both compose the same title — the run's own id in the name is
// what keeps them two files instead of one path (D26). What the second branch
// then meets on a base carrying the first is the id, and D26's answer to that
// is the rename this refusal names, never a quietly dropped item.
func TestTwoRunsFilingOneFolderTheSameDayBothKeepTheirItem(t *testing.T) {
	ctx := context.Background()
	base := filingBase(t)
	open := runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3}
	a := filedRunAt(t, base, "fugaro/a", "20261024-120000-abcd", open)
	b := filedRunAt(t, base, "fugaro/b", "20261024-130000-ef01", open)

	for _, r := range []*run{a, b} {
		if !r.fileOpenItems(ctx) {
			t.Fatalf("run %s filed nothing for the round it left open", r.rec.RunID)
		}
		if !reflect.DeepEqual(r.rec.Backlog, []string{"20261024-01"}) {
			t.Errorf("run %s recorded %v, want the day's first id", r.rec.RunID, r.rec.Backlog)
		}
	}
	aNames, bNames := itemNames(t, a.repo.Dir), itemNames(t, b.repo.Dir)
	if len(aNames) != 1 || len(bNames) != 1 {
		t.Fatalf("the runs filed %v and %v, want one item each", aNames, bNames)
	}
	if aNames[0] == bNames[0] {
		t.Fatalf("both runs wrote %s: one path for two items, so neither branch can merge the other", aNames[0])
	}
	// The second branch's item, standing in the first's folder the way the
	// merge that would bring it there leaves it: two names, one id.
	data, err := os.ReadFile(filepath.Join(b.repo.Dir, backlog.Dir, bNames[0]))
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(a.repo.Dir, backlog.Dir)
	if err := os.WriteFile(filepath.Join(folder, bNames[0]), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backlog.Load(folder); err == nil || !strings.Contains(err.Error(), "20261024-01") {
		t.Fatalf("the joined folder did not name the id both runs took: %v", err)
	}
}

// TestRunFilingAfterAnotherRunsItemReachedTheBase: the other half of the same
// day — a run cut from a base that already carries another run's item. It
// allocates that day's next sequence and writes its own file beside it, so the
// folder ends with both items and the index over both, neither run's item
// standing in the other's way (D26).
func TestRunFilingAfterAnotherRunsItemReachedTheBase(t *testing.T) {
	ctx := context.Background()
	base := filingBase(t)
	open := runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 3}
	first := filedRunAt(t, base, "fugaro/a", "20261024-120000-abcd", open)
	if !first.fileOpenItems(ctx) {
		t.Fatal("the first run filed nothing")
	}
	// The first run's branch is what the second run's base carries, which is
	// the merge into main that happened while the second was being cut.
	testutil.Git(t, first.repo.Dir, "push", "origin", "fugaro/a:main")

	second := filedRunAt(t, base, "fugaro/b", "20261024-130000-ef01", open)
	if !second.fileOpenItems(ctx) {
		t.Fatal("the second run dropped its item for the one already on the base")
	}
	if !reflect.DeepEqual(second.rec.Backlog, []string{"20261024-02"}) {
		t.Fatalf("the second run recorded %v, want the day's next id", second.rec.Backlog)
	}
	names := itemNames(t, second.repo.Dir)
	if len(names) != 2 {
		t.Fatalf("the folder holds %v, want both runs' items", names)
	}
	items, err := backlog.Load(filepath.Join(second.repo.Dir, backlog.Dir))
	if err != nil {
		t.Fatalf("the folder the second run wrote does not pass its own guard: %v", err)
	}
	index := backlog.Render(items)
	for _, want := range []string{"20261024-01", "20261024-02"} {
		if !strings.Contains(index, want) {
			t.Errorf("the index does not name %s:\n%s", want, index)
		}
	}
}
