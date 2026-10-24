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

// filedRun is a run at finalize that left one review round open: a checkout
// cloned from a real remote, on its own branch with the run's work committed,
// the mode resolved from that work, and a record whose reviews are given. It
// returns the run and the path of the remote it pushed to.
func filedRun(t *testing.T, reviews ...runstore.ReviewSummary) (*run, string) {
	t.Helper()
	testutil.IsolateGit(t)
	base := `version: 1
project: aurora
git: { provider: github }
rigor:
  default: poc
  areas:
    - { name: payments, match: "internal/payments/**", mode: startup }
workflows:
  server: { commands: { build: make, test: make test } }
`
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n", "fugaro.yaml": base})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
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
			Version: 1, RunID: "20261024-120000-abcd", Branch: "fugaro/x", BaseBranch: "main",
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
	return r, remote
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
	name := "20261024-01-review-round-2-left-3-findings-open.md"
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

// TestRunFilesNothingWhenTheRoundShipped: the item is what a run *left* open,
// so a round that shipped, a round that found nothing, a run that ended after
// its first line, one that was never reviewed and one whose review was
// skipped all file nothing, leave the branch exactly as it was and write no
// field on the record.
func TestRunFilesNothingWhenTheRoundShipped(t *testing.T) {
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

// TestFilingRefusesWhatTheFolderAlreadyHolds: the filing is a warning path —
// an item that cannot be written costs a follow-up, never the run — so a
// filing the folder refuses leaves the branch and the record exactly as they
// were and is not recorded as a filing.
func TestFilingRefusesWhatTheFolderAlreadyHolds(t *testing.T) {
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
