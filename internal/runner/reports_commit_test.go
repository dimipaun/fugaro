package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The commit-reports step (periodic-reviews R9, E5, D2750 and D2754). A commit
// run's first step is the runner's own work: it commits the stored reports the
// launcher handed in into the folder the project designates inside the
// repository the project designates, regenerates the folder's index over every
// report it holds, and leaves the pull request to the recipe's one senior
// review. These tests pin the properties that step's review rests on: the
// repository the run stands in is the one the project designated or the run
// does not start; the name the task carries decides where the bytes land, and a
// name outside the mirror's shape is refused before a byte is written; the
// bytes that land are the object the task's own sha256 describes; and a file
// the folder holds that the store does not is left alone.
//
// The designation is a member of the published policy, so these runs go through
// the RTDB-backed harness (policyRun): without a policy node there is no
// designation to read, and the run is refused before its first stage -- which
// is the refusal's own case below.

// commitReportsCatalog is the catalog recipe a commit run names.
const commitReportsCatalog = "commit-reports"

// commitReportsOtherSlug is another repository's storage slug (task.Slug): the
// bucket holds every repository's objects, so a handed-in name may be one of
// them.
var commitReportsOtherSlug = mustSlug("github", "other/repo")

// commitReportsSpec is a commit run's task: the catalog commit-reports recipe
// embedded the way `fugaro run --recipe commit-reports` embeds it, and the
// reports the launcher handed in.
func commitReportsSpec(reports ...task.InputFile) *task.Spec {
	text, ok := recipe.CatalogText(commitReportsCatalog)
	if !ok {
		panic("commit-reports is not in the catalog")
	}
	return &task.Spec{
		Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Commit the stored run reports",
		Recipe: &task.Recipe{
			Name: commitReportsCatalog, Source: string(recipe.SourceCatalog),
			SHA256: recipe.Sum(text), YAML: string(text),
		},
		Reports: reports,
	}
}

// commitReportsPolicy is the policy node a commit run reads: version 10, with
// the project designating repo for its reports into dir (the layout's own
// folder when dir is ""), or designating nothing at all when repo is empty.
// Its budget mode is observe because every node a run reads must name one of the
// two live ones now: a node that still says the word the vocabulary dropped is
// refused at bootstrap (AB2, D3009), and these tests are about the designation.
func commitReportsPolicy(repo, dir string) string {
	if repo == "" {
		return `{"version":10,"budget":{"mode":"observe"}}`
	}
	block := fmt.Sprintf(`{"repo":%q`, repo)
	if dir != "" {
		block += fmt.Sprintf(`,"dir":%q`, dir)
	}
	return `{"version":10,"budget":{"mode":"observe"},"reports_commit":` + block + `}}`
}

// commitReportsObject writes one stored report into the run's own prefix, where
// the launcher's own upload puts it: `runs/<slug>/<run>/reports/<name>`, spelled
// here rather than taken from the step, so a step that looked somewhere else
// fails this instead of agreeing with itself. It answers the InputFile the task
// carries for the object.
func commitReportsObject(t *testing.T, h *harness, name, body string) task.InputFile {
	t.Helper()
	key := h.store.Prefix() + digest.ReportsPrefix + "/" + name
	if err := h.bucket.WriteAll(context.Background(), key, []byte(body), &blob.WriterOptions{ContentType: "text/markdown"}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return task.InputFile{Name: name, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
}

// commitReportsRun is a commit run against the designation the policy carries:
// the harness with the node published, the given objects written into the run's
// own prefix where the launcher's own upload puts them, and the task that
// carries their names last.
func commitReportsRun(t *testing.T, policy string, reports map[string]string) *bk {
	t.Helper()
	b := policyRun(t, gwConfig(t, ""), "off", "", policy)
	files := make([]task.InputFile, 0, len(reports))
	for _, name := range slices.Sorted(maps.Keys(reports)) {
		files = append(files, commitReportsObject(t, b.harness, name, reports[name]))
	}
	b.setSpec(t, commitReportsSpec(files...))
	return b
}

// commitReportsRoundsRun is commitReportsRun with the recipe's review rounds
// overridden: a commit run's recipe carries one senior round, and a test that
// wants a fix stage to run in needs a second. The override is the task's own,
// the way a report run's tests set it (digestSpecRounds).
func commitReportsRoundsRun(t *testing.T, rounds int, reports map[string]string) *bk {
	t.Helper()
	b := policyRun(t, gwConfig(t, ""), "off", "", commitReportsPolicy("acme/app", ""))
	files := make([]task.InputFile, 0, len(reports))
	for _, name := range slices.Sorted(maps.Keys(reports)) {
		files = append(files, commitReportsObject(t, b.harness, name, reports[name]))
	}
	spec := commitReportsSpec(files...)
	spec.Overrides.ReviewRounds = &rounds
	b.setSpec(t, spec)
	return b
}

// commitReportsAssertStepOnly is what a commit run's pull request must be: the
// files the commit-reports step wrote -- each handed-in report at the bytes the
// store holds, and the folder's own index -- and no other path, byte for byte.
// The bytes wanted are the test's own constants and not the branch's, so a
// branch a stage moved is held to them rather than agreeing with itself.
func commitReportsAssertStepOnly(t *testing.T, b *bk, branch string, reports map[string]string) {
	t.Helper()
	want := map[string]string{projectpolicy.DefaultReportsCommitDir + "/" + digest.IndexName: commitReportsIndex(t, slices.Sorted(maps.Keys(reports))...)}
	for name, body := range reports {
		want[projectpolicy.DefaultReportsCommitDir+"/"+name] = body
	}
	changed := digestChanged(t, b.harness, branch)
	gotPaths, wantPaths := slices.Sorted(maps.Keys(changed)), slices.Sorted(maps.Keys(want))
	if !slices.Equal(gotPaths, wantPaths) {
		t.Errorf("the pull request changes %v; want the reports the step committed and the folder's index (%v)", gotPaths, wantPaths)
	}
	for rel, body := range want {
		if !fileInTree(t, b.remote, branch, rel) {
			t.Errorf("%s is not in the pull request", rel)
			continue
		}
		if got := commitReportsBytesAt(t, b.remote, branch, rel); got != body {
			t.Errorf("%s in the pull request holds %q, want the bytes the step committed %q", rel, got, body)
		}
	}
}

// commitReportsIndex is the index the step regenerates over the names it is
// given: IndexMarkdown's own bytes over the rows MirrorRows builds when no file
// carries a summary block, which is what the bodies these tests hand in are (a
// plain document, not one of the layout's). The order is MirrorRows' own.
func commitReportsIndex(t *testing.T, names ...string) string {
	t.Helper()
	rows := make([]digest.Row, 0, len(names))
	for _, name := range names {
		_, dim, key, ok := digest.ParseMirrorPath(name)
		if !ok {
			t.Fatalf("%q is not a mirror name", name)
		}
		rows = append(rows, digest.Row{Dim: dim, Key: key, Rel: name})
	}
	slices.SortFunc(rows, func(a, b digest.Row) int {
		if a.Key != b.Key {
			return strings.Compare(a.Key, b.Key)
		}
		return strings.Compare(a.Rel, b.Rel)
	})
	return digest.IndexMarkdown(rows)
}

// commitReportsBytesAt is one committed file's bytes, exactly: git's own
// plumbing, which is what the step's byte-identity claims rest on -- fileContentAt
// reads through a porcelain command that trims, and a report that lost its
// trailing newline on the way would read as identical there.
func commitReportsBytesAt(t *testing.T, remote, ref, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", remote, "cat-file", "blob", ref+":"+path).Output()
	if err != nil {
		t.Fatalf("reading %s at %s: %v", path, ref, err)
	}
	return string(out)
}

// commitReportsLog captures what the run's own logger wrote, so a test can read
// the line the step leaves about a file it did not touch.
type commitReportsLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *commitReportsLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *commitReportsLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestCommitReportsWritesEachRepositorysReportUnderItsSlug: the step writes
// every object the launcher handed in at its mirror-relative name under the
// designated folder -- a repository's report under its own slug, the project's
// aggregate with no slug -- with the store's own bytes, commits them as the
// runner, and runs the recipe's one review and no implement stage.
//
// The project's store holds more than one repository's reports and the task
// hands two of them in: the designated folder is where all of them are
// committed, and each one's slug is what tells them apart.
//
// Mutation (write every object at its base name, dropping the slug, or read the
// objects from the store's own prefix rather than the run's): the repository's
// report lands where the aggregate belongs, and the assertion on the slug's own
// path fails.
func TestCommitReportsWritesEachRepositorysReportUnderItsSlug(t *testing.T) {
	own, other, agg := "2026/2026-10-20.md", "2026/2026-10-20.md", "2026/2026-10-19.md"
	ownBody, otherBody, aggBody := "# acme/app's report\n", "# other/repo's report\n", "# the project's report\n"
	b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), map[string]string{
		repoSlug + "/" + own:                 ownBody,
		commitReportsOtherSlug + "/" + other: otherBody,
		agg:                                  aggBody,
	})
	var logs commitReportsLog
	b.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))

	rec, err := b.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	if got := stageNames(rec); len(got) != 2 || got[0] != "commit_reports" || got[1] != "review" {
		t.Errorf("stages = %v, want the commit-reports step and the review, and no implement", got)
	}
	if len(b.agent.calls) != 1 {
		t.Errorf("the run made %d model calls, want its one review", len(b.agent.calls))
	}
	branch := "fugaro/" + runID
	for rel, body := range map[string]string{
		projectpolicy.DefaultReportsCommitDir + "/" + repoSlug + "/" + own:                 ownBody,
		projectpolicy.DefaultReportsCommitDir + "/" + commitReportsOtherSlug + "/" + other: otherBody,
		projectpolicy.DefaultReportsCommitDir + "/" + agg:                                  aggBody,
	} {
		if !fileInTree(t, b.remote, branch, rel) {
			t.Errorf("%s is not on the run's branch", rel)
			continue
		}
		if got := commitReportsBytesAt(t, b.remote, branch, rel); got != body {
			t.Errorf("%s holds %q, want the store's own bytes %q", rel, got, body)
		}
	}
}

// TestCommitReportsRegeneratesTheIndexOverTheFolder: the folder's index is
// regenerated over every report the folder holds after the write, so a reader
// who opens <dir>/README.md finds the reports that are there. It is the
// layout's own index -- the same header and the same table the repository's own
// report folder carries -- written over this folder's own rows.
//
// Mutation (write the index before the reports, or skip it when the folder is
// fresh): the index names no report, or the folder has none at all.
func TestCommitReportsRegeneratesTheIndexOverTheFolder(t *testing.T) {
	names := []string{repoSlug + "/2026/2026-10-20.md", "2026/2026-10-19.md"}
	reports := map[string]string{}
	for _, name := range names {
		reports[name] = "# " + name + "\n"
	}
	b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), reports)

	rec, err := b.run(t, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	index := projectpolicy.DefaultReportsCommitDir + "/" + digest.IndexName
	if !fileInTree(t, b.remote, "fugaro/"+runID, index) {
		t.Fatalf("%s is not on the run's branch", index)
	}
	body := fileContentAt(t, b.remote, "fugaro/"+runID, index)
	if !strings.HasPrefix(body, digestIndexFirstLine) {
		t.Errorf("the index does not start with %q: %.60q", digestIndexFirstLine, body)
	}
	for _, name := range names {
		if !strings.Contains(body, "]("+name+")") {
			t.Errorf("the index does not list %s:\n%s", name, body)
		}
	}
	if !strings.Contains(body, "2 reports") {
		t.Errorf("the index does not count the folder's reports:\n%s", body)
	}
}

// TestCommitReportsNamesTheRepositoryOfARowThatBelongsToOne: the index's row
// for a repository's report carries the slug in its path, and the project's
// aggregate carries none -- the mirror's own row shape, built from the names
// the folder holds rather than from the layout's slugless one. Two rows of the
// same period, from two repositories and the aggregate, stay three rows: the
// name is what tells them apart, and a row that lost its slug would send a
// reader to a document about another set of runs.
//
// Mutation (build the rows from the layout's own PeriodPath rather than from
// the mirror's names): the slug disappears, and the three rows collapse into
// one file's worth of index entries.
func TestCommitReportsNamesTheRepositoryOfARowThatBelongsToOne(t *testing.T) {
	period := "2026/2026-10-20.md"
	repo, other, agg := repoSlug+"/"+period, commitReportsOtherSlug+"/"+period, period
	b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), map[string]string{
		repo:  "# acme/app's report\n",
		other: "# other/repo's report\n",
		agg:   "# the project's report\n",
	})

	rec, err := b.run(t, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	body := fileContentAt(t, b.remote, "fugaro/"+runID, projectpolicy.DefaultReportsCommitDir+"/"+digest.IndexName)
	for _, name := range []string{repo, other, agg} {
		if !strings.Contains(body, "]("+name+")") {
			t.Errorf("the index has no row for %s:\n%s", name, body)
		}
	}
	if n := strings.Count(body, "| ["); n != 3 {
		t.Errorf("the index lists %d rows, want the three reports of the same period, one per repository and the aggregate:\n%s", n, body)
	}
	if !strings.Contains(body, "3 reports") {
		t.Errorf("the index does not count the three reports:\n%s", body)
	}
}

// TestCommitReportsLeavesAFileTheStoreDoesNotHold: the store only adds and
// overwrites, so a report the folder holds and the store does not is neither
// deleted nor rewritten -- a run is not where a report is taken away -- and the
// run's own record names the path it left alone. The index is regenerated over
// every report the folder holds, that one among them.
//
// Mutation (commit the folder as the store sees it, or delete what was not
// handed in): the committed file is gone or its bytes are rewritten.
func TestCommitReportsLeavesAFileTheStoreDoesNotHold(t *testing.T) {
	const kept, keptBy = "2026/2026-01-05.md", "# a report a person committed\n"
	b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), map[string]string{
		repoSlug + "/2026/2026-10-20.md": "# acme/app's report\n",
	})
	rel := projectpolicy.DefaultReportsCommitDir + "/" + repoSlug + "/" + kept
	advanceBranch(t, b.remote, "main", map[string]string{rel: keptBy})
	var logs commitReportsLog
	b.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))

	rec, err := b.run(t, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	branch := "fugaro/" + runID
	if !fileInTree(t, b.remote, branch, rel) {
		t.Fatalf("%s was taken away by a run that was not handed it", rel)
	}
	if got := commitReportsBytesAt(t, b.remote, branch, rel); got != keptBy {
		t.Errorf("%s holds %q, want the bytes the folder already held %q", rel, got, keptBy)
	}
	body := fileContentAt(t, b.remote, branch, projectpolicy.DefaultReportsCommitDir+"/"+digest.IndexName)
	if !strings.Contains(body, "]("+repoSlug+"/"+kept+")") {
		t.Errorf("the index does not list the report the folder already held:\n%s", body)
	}
	if !strings.Contains(logs.String(), rel) {
		t.Errorf("the run's record does not name the file it left alone (%s):\n%s", rel, logs.String())
	}
}

// TestCommitReportsEndsWithoutADRWhenNothingDiffers: a run whose reports and
// whose index are already in the tree byte for byte writes nothing, commits
// nothing, opens no pull request and runs no model stage -- it ends the way a
// caught-up report run does. ("ADR" is the plan's own spelling of "APR"; the
// property it names is that no pull request is opened for a folder that did not
// change.)
//
// Mutation (commit unconditionally, or count a rewritten index as a change):
// the run pushes a branch that holds nothing new.
func TestCommitReportsEndsWithoutADRWhenNothingDiffers(t *testing.T) {
	repo, agg := repoSlug+"/2026/2026-10-20.md", "2026/2026-10-19.md"
	bodies := map[string]string{repo: "# acme/app's report\n", agg: "# the project's report\n"}
	b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), bodies)
	held := map[string]string{projectpolicy.DefaultReportsCommitDir + "/" + digest.IndexName: commitReportsIndex(t, repo, agg)}
	for name, body := range bodies {
		held[projectpolicy.DefaultReportsCommitDir+"/"+name] = body
	}
	advanceBranch(t, b.remote, "main", held)

	rec, err := b.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, "every stored report is already in the folder") {
		t.Errorf("reason = %q, want it to say every stored report is already in the folder", rec.Reason)
	}
	if got := stageNames(rec); len(got) != 1 || got[0] != "commit_reports" {
		t.Errorf("stages = %v, want the commit-reports step alone", got)
	}
	if len(b.agent.calls) != 0 {
		t.Errorf("the run made %d model calls with nothing to commit", len(b.agent.calls))
	}
	if hasBranch(t, b.remote, "fugaro/"+runID) {
		t.Error("the run pushed a branch with nothing to commit")
	}
	if len(b.provider.State.PRs) != 0 {
		t.Errorf("the run opened %+v with nothing to commit", b.provider.State.PRs)
	}
}

// TestCommitReportsEndsWithoutAPullRequestWhenNothingIsHandedIn: a launch that
// hands in no report at all is the nothing-to-commit case and not a refusal.
// That is what the launcher produces on a checkout whose folder already holds
// every stored report (R16 hands in every report the folder lacks), so a second
// run of a caught-up project -- and the store is still one writer's, so the run
// has no list of its own to compare against -- has to end with no pull request
// rather than as an infrastructure failure. The folder that holds no report is
// the same case: the step writes nothing, and in particular no index into a
// repository it was handed nothing for, which is what would open the pull
// request this case exists to avoid.
//
// Mutation (return the refusal this case used to return, or regenerate the
// index over the folder here): the first fails the run as an infrastructure
// error, and the second pushes a branch holding an index the step was handed
// nothing for.
func TestCommitReportsEndsWithoutAPullRequestWhenNothingIsHandedIn(t *testing.T) {
	repo, agg := repoSlug+"/2026/2026-10-20.md", "2026/2026-10-19.md"
	bodies := map[string]string{repo: "# acme/app's report\n", agg: "# the project's report\n"}
	held := map[string]string{projectpolicy.DefaultReportsCommitDir + "/" + digest.IndexName: commitReportsIndex(t, repo, agg)}
	for name, body := range bodies {
		held[projectpolicy.DefaultReportsCommitDir+"/"+name] = body
	}
	for _, tc := range []struct {
		name  string
		files map[string]string // what the checkout's folder holds, nil for a folder that holds nothing
	}{
		{"an up-to-date folder", held},
		{"a folder with no report in it", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), nil)
			// The store holds both reports and the launch handed in neither,
			// which is the launcher's own answer for a folder that already has
			// them: the step has no bytes to fetch and none to fetch them for.
			for _, name := range slices.Sorted(maps.Keys(bodies)) {
				commitReportsObject(t, b.harness, name, bodies[name])
			}
			if tc.files != nil {
				advanceBranch(t, b.remote, "main", tc.files)
			}

			rec, err := b.run(t, review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
				t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
			}
			if !strings.Contains(rec.Reason, "every stored report is already in the folder") {
				t.Errorf("reason = %q, want it to say every stored report is already in the folder", rec.Reason)
			}
			if got := stageNames(rec); len(got) != 1 || got[0] != "commit_reports" {
				t.Errorf("stages = %v, want the commit-reports step alone", got)
			}
			if len(b.agent.calls) != 0 {
				t.Errorf("the run made %d model calls with nothing handed in", len(b.agent.calls))
			}
			if hasBranch(t, b.remote, "fugaro/"+runID) {
				t.Error("the run pushed a branch with nothing handed in")
			}
			if len(b.provider.State.PRs) != 0 {
				t.Errorf("the run opened %+v with nothing handed in", b.provider.State.PRs)
			}
		})
	}
}

// TestCommitReportsFilesEditedByFixAreRestored: a review round that edits a
// committed report -- the one file a review's findings must never change, since
// the pull request is the store's bytes of it -- leaves the pull request
// carrying those bytes and not the fix's. The step's bytes are its own commit
// and nowhere else, and they cannot be written over at finalize: they are the
// store's objects, which only the launcher read (D2754), so the branch is put
// back to the step's commit instead. The step is also the run's only push
// (checkpointsOn, the early draft), so a fix's commit never reaches the remote
// while the round runs.
//
// Red step (R9): the fix's edit survives into the pull request, which is a
// repository the project designated for its reports and may be public -- a
// review agent publishing its own content under the user's identity.
func TestCommitReportsFilesEditedByFixAreRestored(t *testing.T) {
	name := repoSlug + "/2026/2026-10-20.md"
	const body = "# acme/app's report\n"
	rel := projectpolicy.DefaultReportsCommitDir + "/" + name
	b := commitReportsRoundsRun(t, 2, map[string]string{name: body})

	rec, err := b.run(t,
		review("changes", 1),
		fixRoundStep("printf 'edited by the fix stage\\n' >> "+rel+" && git commit -qam 'Address review'"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	branch := "fugaro/" + runID
	if got := commitReportsBytesAt(t, b.remote, branch, rel); got != body {
		t.Errorf("%s in the pull request holds %q, want the store's own bytes %q", rel, got, body)
	}
	commitReportsAssertStepOnly(t, b, branch, map[string]string{name: body})
}

// TestCommitReportsRunAddsNoOtherFile: a fix stage that writes a CLAUDE.md and
// a skill file -- files a repository would keep, and a review agent's own text
// -- leaves neither in the pull request, and the report it edited is the step's
// bytes again. The run's diff against its base is exactly the files the
// commit-reports step wrote under the designated folder, byte for byte.
//
// Mutation (let the stages' work stand): the run pushes the fix's commit, and
// the pull request adds CLAUDE.md and the skill file into a repository the
// project designated for its reports.
func TestCommitReportsRunAddsNoOtherFile(t *testing.T) {
	const notes = "CLAUDE.md"
	const skill = "plugin/skills/working/SKILL.md"
	name := repoSlug + "/2026/2026-10-20.md"
	const body = "# acme/app's report\n"
	rel := projectpolicy.DefaultReportsCommitDir + "/" + name
	b := commitReportsRoundsRun(t, 2, map[string]string{name: body})

	rec, err := b.run(t,
		review("changes", 1),
		fixRoundStep("mkdir -p plugin/skills/working && printf '# what the fix stage wrote\\n' > "+notes+
			" && printf '# a skill the fix stage wrote\\n' > "+skill+
			" && printf 'edited by the fix stage\\n' >> "+rel+
			" && git add -A && git commit -qm 'Address review'"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	branch := "fugaro/" + runID
	commitReportsAssertStepOnly(t, b, branch, map[string]string{name: body})
	for _, path := range []string{notes, skill} {
		if fileInTree(t, b.remote, branch, path) {
			t.Errorf("%s is in the pull request; a commit run writes only %s/", path, projectpolicy.DefaultReportsCommitDir)
		}
	}
}

// TestCommitReportsRunPushesNoCheckpoint: a commit run pushes no checkpoint,
// for the reason a report run does not: what a stage leaves on the branch is
// discarded at finalize (deterministicFinalize), so a tip pushed while a stage
// runs would publish a commit of the agent's that no run of this recipe meant
// to -- into the repository the project designated for its reports, under the
// user's identity, whatever finalize pushes after it. The stage here commits a
// file, which is what a boundary checkpoint would push.
//
// Mutation (checkpoint as any other run does): the stage's commit reaches the
// remote at the stage boundary.
func TestCommitReportsRunPushesNoCheckpoint(t *testing.T) {
	name := repoSlug + "/2026/2026-10-20.md"
	const body = "# acme/app's report\n"
	b := commitReportsRoundsRun(t, 2, map[string]string{name: body})

	var pushed int
	runner.SetCheckpointPushedSeam(t, func() { pushed++ })

	rec, err := b.run(t,
		review("changes", 1),
		fixRoundStep("printf 'a stage wrote this\\n' > notes.md && git add -A && git commit -qm 'a stage commit'"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	if pushed != 0 {
		t.Errorf("the commit run pushed %d checkpoint(s); its branch must hold only the commit the commit-reports step made", pushed)
	}
	commitReportsAssertStepOnly(t, b, "fugaro/"+runID, map[string]string{name: body})
}

// TestCommitReportsPushesNothingBeforeFinalize: the early draft's push at a
// stage boundary is off for a commit run too, not only the checkpoint. A fix
// stage's HEAD can be verified (the stage ran the tests itself), which is all
// afterStage asks before it opens the draft and pushes the verified tip, and a
// commit-reports recipe with a review-rounds override above 1 has fix stages.
// The push it would carry is a commit of the stage's -- the branch a commit run
// publishes is the store's bytes and nothing else (R9) -- so the remote sees the
// branch at finalize and at no earlier point.
//
// Mutation (leave the early-draft push ungated): the fix stage's commit is on
// the remote before finalize, with the report as the stage edited it.
func TestCommitReportsPushesNothingBeforeFinalize(t *testing.T) {
	name := repoSlug + "/2026/2026-10-20.md"
	const body = "# acme/app's report\n"
	rel := projectpolicy.DefaultReportsCommitDir + "/" + name
	b := commitReportsRoundsRun(t, 2, map[string]string{name: body})

	rec, err := b.run(t,
		review("changes", 1),
		fixRoundStep("printf 'edited by the fix stage\\n' >> "+rel+" && git commit -qam 'Address review'"),
		probe(func(t *testing.T) {
			if remoteHasBranch(t, b.harness) {
				t.Error("the commit run pushed its branch at a stage boundary, before finalize")
			}
			if n := len(b.provider.State.PRs); n != 0 {
				t.Errorf("the commit run opened %d pull request(s) at a stage boundary, want none", n)
			}
		}, review("ship", 0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
	commitReportsAssertStepOnly(t, b, "fugaro/"+runID, map[string]string{name: body})
}

// TestCommitReportsRefusesAByteWhoseHashDisagrees: the step holds every object
// it downloads to the size and the sha256 the task declares, and a mismatch is
// a refusal -- the run fails and commits nothing, so no byte that is not the
// object the task described ever reaches the repository. It is the only check
// that ties the bytes to the task, and it is made on the download's temporary,
// before the rename.
//
// Mutation (trust the download, or compare the hash after the file is in
// place): the run commits bytes the launcher never described.
func TestCommitReportsRefusesAByteWhoseHashDisagrees(t *testing.T) {
	const name = "2026/2026-10-20.md"
	b := policyRun(t, gwConfig(t, ""), "off", "", commitReportsPolicy("acme/app", ""))
	file := commitReportsObject(t, b.harness, name, "# the project's report\n")
	// The hash is the task's and the object is the store's: the two disagree,
	// which is what a truncated upload or a task written from a stale listing
	// looks like. The size is the object's, so the hash is what is refused.
	file.SHA256 = strings.Repeat("ab", 32)
	b.setSpec(t, commitReportsSpec(file))

	rec, err := b.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = status %s, class %s, reason %q, want the run to fail on the object it cannot verify", rec.Status, rec.FailureClass, rec.Reason)
	}
	if !strings.Contains(rec.Reason, name) {
		t.Errorf("reason = %q, want it to name the report whose bytes disagree", rec.Reason)
	}
	if hasBranch(t, b.remote, "fugaro/"+runID) {
		t.Error("the run pushed a branch holding bytes it could not verify")
	}
	if len(b.provider.State.PRs) != 0 {
		t.Errorf("the run opened %+v for reports it could not verify", b.provider.State.PRs)
	}
}

// TestCommitReportsRefusesANameOutsideTheMirrorShape: a handed-in name that is
// not a report of the mirror's own shape is refused before a byte is written,
// because the name is the path this step commits. The first case is the name
// the input rule admits on purpose -- task.InputNameProblem lets a name begin
// with a dot, since an --attach input is written under the git-excluded
// inputs/ -- and the rest are the shapes a name takes when it is not a report:
// a walk out of the folder, an absolute one, one with no period directory, one
// that is not a report file, one whose directory is not the period's own year,
// and one whose slug is not a storage slug.
//
// Mutation (check the name with task.InputNameProblem, or with no check at
// all): the run commits a path outside the mirror's shape, and the dot-prefixed
// case lands where no report belongs.
func TestCommitReportsRefusesANameOutsideTheMirrorShape(t *testing.T) {
	for _, name := range []string{
		".git/hooks/pre-commit",
		"../outside/2026/2026-10-20.md",
		"/etc/2026/2026-10-20.md",
		"2026-10-20.md",
		"2026/notes.md",
		"2026/2025-01-01.md",
		"acme/2026/2026-10-20.md",
	} {
		t.Run(name, func(t *testing.T) {
			// The object is there under the name the task carries, so what is
			// refused is the name and not a missing object.
			b := commitReportsRun(t, commitReportsPolicy("acme/app", ""), map[string]string{name: "# a report\n"})

			rec, err := b.run(t, review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusInfraError {
				t.Fatalf("record = status %s, reason %q, want the name refused", rec.Status, rec.Reason)
			}
			if !strings.Contains(rec.Reason, "not a report of the committed mirror's shape") {
				t.Errorf("reason = %q, want the refusal to be the mirror's own shape", rec.Reason)
			}
			if hasBranch(t, b.remote, "fugaro/"+runID) {
				t.Error("the run committed a name outside the mirror's shape")
			}
			if len(b.agent.calls) != 0 {
				t.Errorf("the run made %d model calls before refusing a name", len(b.agent.calls))
			}
		})
	}
}

// TestCommitReportsRefusesADesignationOfAnotherRepository: the repository the
// run stands in must be the one the project designates, and a run in any other
// repository is refused at bootstrap -- before its first stage, before any
// model call, and before it reads a single object -- because the store holds
// every repository's reports and the designation is the owner's one answer to
// where they may be committed. A project that designates nothing at all is the
// same refusal: it commits no report anywhere.
//
// This is the red step of the task: an implementation that commits wherever its
// checkout happens to be passes every other test here and fails this one.
//
// Mutation (compare the designation by name rather than through task.Slug, or
// skip the comparison when the run has a checkout): a run in another repository
// commits this project's reports under someone else's name.
func TestCommitReportsRefusesADesignationOfAnotherRepository(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy string
		want   string
	}{
		{"another repository", commitReportsPolicy("other/repo", ""), "designates other/repo"},
		{"another spelling of another repository", commitReportsPolicy("Acme/Other.git", ""), "designates acme/other"},
		{"no designation at all", commitReportsPolicy("", ""), "designates no repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := commitReportsRun(t, tc.policy, map[string]string{
				repoSlug + "/2026/2026-10-20.md": "# acme/app's report\n",
			})

			rec, err := b.run(t, review("ship", 0))
			if err == nil {
				t.Fatal("a run whose designation the runner refuses ended without an error")
			}
			if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || rec.FailureClass != failure.ConfigInvalid {
				t.Fatalf("record = status %s, outcome %s, class %s, reason %q, want the launch refused as config_invalid", rec.Status, rec.Outcome, rec.FailureClass, rec.Reason)
			}
			if !strings.Contains(rec.Reason, tc.want) {
				t.Errorf("reason = %q, want it to say the project %s", rec.Reason, tc.want)
			}
			for _, want := range []string{"reports_commit.repo", "fugaro project policy publish"} {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("reason = %q, want it to name %s", rec.Reason, want)
				}
			}
			if got := stageNames(rec); len(got) != 0 {
				t.Errorf("stages = %v, want no stage at all: the refusal is bootstrap's", got)
			}
			if len(b.agent.calls) != 0 {
				t.Errorf("the run made %d model calls before the refusal", len(b.agent.calls))
			}
			if hasBranch(t, b.remote, "fugaro/"+runID) {
				t.Error("the refused run pushed a branch")
			}
		})
	}
}

// TestCommitReportsRefusesAnAttachedInput: a task that attached files under a
// commit-reports recipe is refused, so a report object is never a recipe input
// and the --attach caps and their artifact gate stay exactly what they are. The
// launcher refuses the same pair for every launch it resolves; this is the arm
// the runner keeps for the ones it cannot.
//
// Mutation (let a mode: implement recipe read inputs/): the refused pair runs,
// and the input caps report objects are kept out of are reachable again.
func TestCommitReportsRefusesAnAttachedInput(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "off", "", commitReportsPolicy("acme/app", ""))
	spec := commitReportsSpec(commitReportsObject(t, b.harness, "2026/2026-10-20.md", "# the project's report\n"))
	spec.Inputs = []task.InputFile{{Name: "notes.md", Bytes: 3, SHA256: strings.Repeat("cd", 32)}}
	b.setSpec(t, spec)

	rec, err := b.run(t)
	if err == nil {
		t.Fatal("a commit run with an attached file ended without an error")
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = status %s, outcome %s, reason %q, want the launch refused", rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, "an attached file is only read by a mode: artifact run") {
		t.Errorf("reason = %q, want the attached-file refusal", rec.Reason)
	}
	if hasBranch(t, b.remote, "fugaro/"+runID) {
		t.Error("the refused run pushed a branch")
	}
}
