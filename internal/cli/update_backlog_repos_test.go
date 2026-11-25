package cli

// U8's tests: the fold reads every enrolled repository's docs/backlog/ from the
// project's list and not from the working directory (R5, R20, D3344), which is
// what the four tests below hold it to. Every one of them runs the step from a
// directory that is no checkout at all — updateRig stands the run in a temp dir
// — against repositories only the reader knows, so a fold that looked at "."
// folds nothing and fails.
//
// The step is driven on its own, as the other one-stage tests drive theirs, and
// for the reason the lines force: a step after the stage loop reports its
// per-repository lines through e.reportRepos, which is a record and not a
// print (the output members are U13), so the lines are read off the run's own
// state here rather than guessed from a terminal.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// foldUpdateRig is one update installation whose repositories the reader stands
// in for: the fixture's own local config carries a database this installation's
// — the mark and the project name, which is what budgetDBUnadopted reads — and
// the engine is the update door's, with --plan-only when asked.
func foldUpdateRig(t *testing.T, planOnly bool, views ...repoView) (*initEngine, *gcpfake.RTDB) {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := updateRig(t)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	db.Set(budget.PathMark, auroraMark())
	db.Set(budget.PathProject, initProjectName)
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return views, nil
	})
	fakeTerminal(t)
	return rigEngine(t, r, &initOptions{door: updateDoor.name, planOnly: planOnly}), db
}

// fetchedFile is one file a code host's tree read answered: its path in the
// repository, and its bytes.
type fetchedFile struct {
	path string
	data []byte
}

// itemFile is one item file of a repository's docs/backlog/ folder, in the
// format backlog.Parse reads.
func itemFile(id, slug, title, status string) fetchedFile {
	return fetchedFile{path: backlog.Dir + "/" + id + "-" + slug + ".md", data: []byte(backlogItemText(id, title, status))}
}

// folderOf is what such a read answers for one repository: the item files
// given, and the index every folder carries, which is not an item.
func folderOf(files ...fetchedFile) map[string][]byte {
	out := map[string][]byte{backlog.Dir + "/" + backlog.IndexName: []byte("# items\n")}
	for _, f := range files {
		out[f.path] = f.data
	}
	return out
}

// backlogView is one repository the reader read. The fold reads a view's
// repository, its reason and its files; the rest of the shape is the reader's
// (contract-repos.md).
func backlogView(repo string, files map[string][]byte) repoView {
	return repoView{Repo: repo, Provider: "github", Branch: "main", Files: files}
}

// reportedOf is the lines the run reported under name, which is how a step
// after the stage loop hands its per-repository lines to the output members.
func reportedOf(e *initEngine, name string) []initflow.RepoLine {
	var out []initflow.RepoLine
	for _, r := range e.reposState.reported {
		if r.name == name {
			out = append(out, r.lines...)
		}
	}
	return out
}

// TestFoldFromEveryDefaultBranch is U8's red step: the run stands in a
// directory that is no checkout, and the items are on two repositories' default
// branches, which the fold reads through the project's list and folds under
// each repository's own name — from anywhere (spec §Acceptance 6, R5). The
// third repository is one whose host could not list its tree: its line carries
// the reason and nothing else, and the two readable ones are folded all the
// same.
func TestFoldFromEveryDefaultBranch(t *testing.T) {
	const unread = "its host listed no tree, so its default branch could not be read"
	e, db := foldUpdateRig(t, false,
		backlogView("acme/app", folderOf(
			itemFile("20261021-01", "one", "the first thing left open", "open"),
			itemFile("20261021-04", "four", "a thing already dealt with", "closed"))),
		backlogView("acme/lib", folderOf(
			itemFile("20261021-02", "two", "the second thing left open", "open"),
			itemFile("20261021-03", "three", "the third thing left open", "open"))),
		repoView{Repo: "acme/web", Unread: unread},
	)
	e.foldBacklog(t.Context())

	out := e.r.w.(*syncBuf).String()
	if !strings.Contains(out, "folded 3") || !strings.Contains(out, backlog.Dir) {
		t.Errorf("the run does not print the count of what it folded, and the folder it folded it from (D2716):\n%s", out)
	}

	byID := storedBacklogItems(t, db)
	if len(byID) != 3 {
		t.Fatalf("the backlog node holds %d work items, want the three open files of the two repositories the run could read: %+v", len(byID), byID)
	}
	if got := byID["20261021-01"].Repo; got != "acme/app" {
		t.Errorf("the file read from acme/app was folded under %q, want the repository it was read from", got)
	}
	if got := byID["20261021-02"].Repo; got != "acme/lib" {
		t.Errorf("the file read from acme/lib was folded under %q, want the repository it was read from", got)
	}
	if _, ok := byID["20261021-04"]; ok {
		t.Errorf("the closed file was folded: a closed or dropped item is history, and the standing backlog holds no item for it (D2716)")
	}

	// One line per repository, in the list's canonical order, through the
	// step's own name.
	want := []initflow.RepoLine{
		newRepoLine("acme/app", initflow.RepoDone, "folded 1 open item(s)"),
		newRepoLine("acme/lib", initflow.RepoDone, "folded 2 open item(s)"),
		newRepoLine("acme/web", initflow.RepoUnread, unread),
	}
	if got := reportedOf(e, "backlog"); !slices.Equal(got, want) {
		t.Errorf("the step reported %+v, want %+v", got, want)
	}
}

// TestFoldRefusesASharedId: two repositories whose items share an id get one
// line naming both, and neither item is folded (R5) — the node is the
// project's own, so one id is one work item whoever filed it. D26's collision
// is the case the ids exist for: two branches each filed 20261021-01, and both
// merged.
func TestFoldRefusesASharedId(t *testing.T) {
	e, db := foldUpdateRig(t, false,
		backlogView("acme/app", folderOf(itemFile("20261021-01", "one", "filed here first", "open"))),
		backlogView("acme/lib", folderOf(itemFile("20261021-01", "one-elsewhere", "the same id filed on another branch", "open"))),
	)
	e.foldBacklog(t.Context())

	if v := db.Value(orchestratorPath(t, orchestrator.BacklogNode)); v != nil {
		t.Errorf("an id two repositories state was folded anyway: the node holds %v (R5)", v)
	}
	want := backlog.Dir + ": id 20261021-01 is in acme/app, acme/lib, so neither item was folded until one file is renamed (D26)"
	if !slices.Contains(e.r.res.Warnings, want) {
		t.Errorf("the run did not warn that one id is in two repositories, and the fix:\n%q", e.r.res.Warnings)
	}
	if out := e.r.w.(*syncBuf).String(); strings.Contains(out, "into the standing backlog") {
		t.Errorf("the run printed the folded line over two items it refused:\n%s", out)
	}
}

// TestFoldRepositoryIsTheSource: an item's repository is the repository its
// file was read from, never a field of the file (R20). The folder states no
// repository at all, and this installation's own config names acme/sandbox —
// the repository a fold that took the machine's own instead of the source would
// put every item under. The title of the file read from acme/lib names another
// repository too, because it is text and not a field.
func TestFoldRepositoryIsTheSource(t *testing.T) {
	e, db := foldUpdateRig(t, false,
		backlogView("acme/lib", folderOf(itemFile("20261021-01", "one", "acme/app: fold me under the other repository", "open"))),
		backlogView("acme/sandbox", folderOf(itemFile("20261021-02", "two", "the second thing left open", "open"))),
	)
	e.foldBacklog(t.Context())

	byID := storedBacklogItems(t, db)
	if got := byID["20261021-01"].Repo; got != "acme/lib" {
		t.Errorf("the file read from acme/lib was folded under %q, want acme/lib: the repository is the source, not a field of the file (R20)", got)
	}
	if got := byID["20261021-02"].Repo; got != "acme/sandbox" {
		t.Errorf("the file read from acme/sandbox was folded under %q, want acme/sandbox", got)
	}
}

// TestFoldPlanOnlyCounts: under --plan-only the reads happen and the write does
// not, and what the step is there to say is how many items it would fold (spec
// §Interactions) — the count line, and per repository the state that says
// nothing was written.
//
// The count is the one the update behind it would WRITE, not the number of open
// files: an id the node already knows is an item already filed (D2716), so the
// second case folds a folder a fold has already dealt with and the step says
// what the real run would — the items of the repository whose file is not
// already in the node, and silence for the one whose is.
func TestFoldPlanOnlyCounts(t *testing.T) {
	t.Run("nothing is folded yet", func(t *testing.T) {
		e, db := foldUpdateRig(t, true,
			backlogView("acme/app", folderOf(
				itemFile("20261021-01", "one", "the first thing left open", "open"),
				itemFile("20261021-03", "three", "a thing already dealt with", "closed"))),
			backlogView("acme/lib", folderOf(itemFile("20261021-02", "two", "the second thing left open", "open"))),
		)
		e.foldBacklog(t.Context())

		out := e.r.w.(*syncBuf).String()
		if !strings.Contains(out, "would fold 2 open item(s)") {
			t.Errorf("a plan-only run does not say how many items it would fold:\n%s", out)
		}
		if v := db.Value(orchestratorPath(t, orchestrator.BacklogNode)); v != nil {
			t.Errorf("a plan-only run wrote the backlog node: %v", v)
		}
		if v := db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
			t.Errorf("a plan-only run wrote the state node: %v", v)
		}
		want := []initflow.RepoLine{
			newRepoLine("acme/app", initflow.RepoSkipped, "would fold 1 open item(s); --plan-only writes nothing"),
			newRepoLine("acme/lib", initflow.RepoSkipped, "would fold 1 open item(s); --plan-only writes nothing"),
		}
		if got := reportedOf(e, "backlog"); !slices.Equal(got, want) {
			t.Errorf("the step reported %+v, want %+v", got, want)
		}
	})

	t.Run("an id the node already knows is not counted", func(t *testing.T) {
		e, db := foldUpdateRig(t, true,
			backlogView("acme/app", folderOf(itemFile("20261021-01", "one", "the first thing left open", "open"))),
			backlogView("acme/lib", folderOf(itemFile("20261021-02", "two", "the second thing left open", "open"))),
		)
		// The node as a fold that already ran left it: acme/app's file is filed.
		seedBacklogNode(t, db, orchestrator.Actor{Class: orchestrator.Daemon, Name: "daemon", Now: orchestratorTestNow},
			workItemUpsert("20261021-01", "the first thing left open"))
		e.foldBacklog(t.Context())

		out := e.r.w.(*syncBuf).String()
		if !strings.Contains(out, "would fold 1 open item(s)") {
			t.Errorf("a plan-only run counts the items the node already holds, so it claims work the update behind it would not do:\n%s", out)
		}
		byID := storedBacklogItems(t, db)
		if len(byID) != 1 || byID["20261021-01"].Title != "the first thing left open" {
			t.Errorf("a plan-only run wrote the backlog node: %+v", byID)
		}
		want := []initflow.RepoLine{
			newRepoLine("acme/lib", initflow.RepoSkipped, "would fold 1 open item(s); --plan-only writes nothing"),
		}
		if got := reportedOf(e, "backlog"); !slices.Equal(got, want) {
			t.Errorf("the step reported %+v, want %+v: a repository whose every item the node already holds has nothing to report", got, want)
		}
	})
}
