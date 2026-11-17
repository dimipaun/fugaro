package cli

// `fugaro backlog index` and `fugaro backlog list`: the pairing the backlog
// command family has had since rigor modes (D28, plan T4.4) and the two halves
// it has now (docs/orchestrator-graph-estimates, D2715–D2717).
//
// `index` still works over the folder — `docs/backlog/` is history (D2716), and
// a history a reader browses should still be regenerable — and its cases here
// build a folder of their own, so the CLI half is checked without the
// repository's own backlog being the fixture. `list` reads the project's
// database instead: the orchestrator's backlog node and the spec-less items of
// the state node, which are the same backlog to a reader (D2715).
//
// The folder tests reach no network, no credential and no installation; the
// list tests run in newOrchestratorFixture's hermetic database.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// backlogFixture writes two items into a fresh folder and returns it: one
// open item and one closed, so a group and the count line both have something
// to say.
func backlogFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, doc string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("20261021-01-a-title.md", itemDoc("20261021-01", "a title"))
	write("20261021-02-another.md", strings.Replace(itemDoc("20261021-02", "another"), "status: open", "status: closed", 1))
	return dir
}

// TestBacklogIndexWritesTheIndexFromTheItems: `index` writes <dir>/README.md
// from the item files beside it and says which file it wrote. The bytes are
// the renderer's own, so the command is a thin wrapper over the one function
// the folder's pin holds.
//
// Mutation (verified, run and restored): write the index into the working
// directory instead of <dir> — this fails on the file it cannot find.
func TestBacklogIndexWritesTheIndexFromTheItems(t *testing.T) {
	dir := backlogFixture(t)
	out, _, err := execute(t, "backlog", "index", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, backlog.IndexName)
	if lines := reportLines(out); lines[0] != markDone+" wrote "+path {
		t.Errorf("fugaro backlog index printed %q, want %q and the closing line", out, markDone+" wrote "+path)
	}
	items, err := backlog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fugaro backlog index wrote no %s: %v", path, err)
	}
	if got, want := string(data), backlog.Render(items); got != want {
		t.Errorf("%s is not what the items render to:\n--- written ---\n%s\n--- rendered ---\n%s", path, got, want)
	}
	if !strings.HasPrefix(string(data), backlog.Header) {
		t.Errorf("%s does not open with %q: the generated file names the one command that rewrites it (D28)", path, backlog.Header)
	}
}

// backlogSeed is the state a list case reads: two items filed into the backlog
// node by the command itself, one spec-less item a session queued in the state
// node — the same backlog to a reader (D2715) — and one item that carries a
// spec, which belongs to the plan that spec names.
func backlogSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	if _, _, err := execute(t, addArgs()...); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, addArgs("--title", "the second thing")...); err != nil {
		t.Fatal(err)
	}
	planned := workItemUpsert("plan-1", "a planned item")
	planned.WorkItem.Spec = strPtr("some-plan")
	seedOrchestratorState(t, f, workItemUpsert("session-1", "a session queued this"), planned)
}

// backlogFinished adds the two terminal items a project that has finished work
// keeps: one done and one cancelled. A phase is the daemon's to write (G12), so
// the actor seeding them is the daemon, and they land in the node the seed
// above wrote — the last few finished items stay in it as a record rather than
// being archived away.
func backlogFinished(t *testing.T, f *budgetFixture) {
	t.Helper()
	s := storedOrchestratorState(t, f)
	done, cancelled := orchestrator.PhaseDone, orchestrator.PhaseCancelled
	var ops []orchestrator.Op
	for _, item := range []struct {
		id    string
		title string
		phase *orchestrator.Phase
	}{
		{"done-1", "work that finished", &done},
		{"done-2", "work that was dropped", &cancelled},
	} {
		op := workItemUpsert(item.id, item.title)
		op.WorkItem.Phase = item.phase
		ops = append(ops, op)
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops},
		orchestrator.Actor{Class: orchestrator.Daemon, Name: "daemon", Now: orchestratorTestNow})
	if err != nil {
		t.Fatalf("seeding the finished items: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(orchestratorPath(t, orchestrator.StateNode), json.RawMessage(raw))
}

// TestBacklogListReadsBothNodes is B1's own test for the read: `list` prints the
// backlog node's items and the state node's spec-less ones, and nothing that
// carries a spec — an item with a plan is the plan's, not the backlog's
// (D2715). The rows say which node each came from, so a reader can tell the two
// apart, and nothing is written.
func TestBacklogListReadsBothNodes(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	backlogSeed(t, f)
	before := backlogNodes(t, f)

	out, _, err := execute(t, "backlog", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"20261108-01", "20261108-02", "session-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "plan-1") {
		t.Errorf("the listing carries the item that names a spec: the backlog is the spec-less work, and a planned item is its plan's (D2715):\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(row, "20261108-01"):
			if !strings.Contains(row, orchestrator.BacklogNode) {
				t.Errorf("the row of an item filed into the backlog node does not say so: %q", row)
			}
		case strings.Contains(row, "session-1"):
			if !strings.Contains(row, orchestrator.StateNode) {
				t.Errorf("the row of a session's spec-less item does not say which node it came from: %q", row)
			}
		}
	}
	// Nothing in the backlog is approved — every item arrived queued and
	// unapproved (D2717) — so the listing says so, and the count is what a screen
	// shows as the work waiting on the person.
	if !strings.Contains(out, "3 waiting for a person's approval") {
		t.Errorf("the listing does not state how much waits on the person:\n%s", out)
	}
	if after := backlogNodes(t, f); after != before {
		t.Errorf("fugaro backlog list changed a node: %q, want %q", after, before)
	}
}

// TestBacklogListDoesNotCountFinishedItemsAsWaiting: the figure the listing
// prints is the unfinished work nobody has approved, not the node's whole work
// list. A done or cancelled item can never be approved — the state model
// refuses an approval on a terminal one — and the last few finished items stay
// in the node as a record, so counting them would show a person a number that
// never clears and that no action of theirs can change. The rows themselves
// still carry them: they are work of this project's, and the listing reads the
// node (D2715).
//
// Mutation (verified, run and restored): count a done item into the figure
// (`w.Phase != orchestrator.PhaseDone` in place of `!w.Phase.Done()`) — this
// fails on both checks, which read 4 rather than 3.
func TestBacklogListDoesNotCountFinishedItemsAsWaiting(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	backlogSeed(t, f)
	backlogFinished(t, f)

	out, _, err := execute(t, "backlog", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "3 waiting for a person's approval") {
		t.Errorf("the listing counts the finished items into the work waiting on the person:\n%s", out)
	}
	for _, want := range []string{"done-1", "done-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %s: a finished item stays in the node's work list, and the listing is what reads it:\n%s", want, out)
		}
	}
	var doc struct {
		Waiting int `json:"waiting"`
		Items   []struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
		} `json:"items"`
	}
	js, _, err := execute(t, "backlog", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("fugaro backlog list --json printed no document: %v\n%s", err, js)
	}
	if doc.Waiting != 3 {
		t.Errorf("waiting = %d, want 3: the terminal items wait on nobody, and the state model refuses the approval that would clear them", doc.Waiting)
	}
	if len(doc.Items) != 5 {
		t.Fatalf("the document carries %d rows, want the five items the two nodes hold", len(doc.Items))
	}
	for _, row := range doc.Items {
		if row.ID == "done-1" && row.Phase != string(orchestrator.PhaseDone) {
			t.Errorf("the done item's phase is %q, want %q", row.Phase, orchestrator.PhaseDone)
		}
	}
}

// TestBacklogListPrintsTheItemsAsATable: the human half is one row per item
// under the columns the document carries, so a reader meets the same six facts
// a screen reads. The order is by id, which is the day an item was filed.
func TestBacklogListPrintsTheItemsAsATable(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	backlogSeed(t, f)
	out, _, err := execute(t, "backlog", "list")
	if err != nil {
		t.Fatal(err)
	}
	header := strings.Fields(strings.Split(out, "\n")[0])
	want := []string{"ID", "PHASE", "NODE", "REPO", "APPROVED", "TITLE"}
	if len(header) != len(want) {
		t.Fatalf("the table's header is %q, want %q", header, want)
	}
	for i := range want {
		if header[i] != want[i] {
			t.Fatalf("the table's header is %q, want %q", header, want)
		}
	}
	if strings.Index(out, "20261108-01") > strings.Index(out, "session-1") {
		t.Errorf("the rows are not sorted by id, which is the day an item was filed:\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if strings.Contains(row, "20261108-01") {
			if !strings.Contains(row, "queued") || !strings.Contains(row, "no") {
				t.Errorf("the row of a filed item is %q, want its phase and that nobody has approved it", row)
			}
		}
	}
}

// TestBacklogListJSONCarriesTheItems: the document carries the version, the
// project it read, the figures a screen counts and one row per item, with the
// field names the console's contract documents for it.
//
// Mutation (verified, run and restored): drop `waiting` from the document —
// this fails, because the count a screen shows is then absent rather than zero.
func TestBacklogListJSONCarriesTheItems(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	backlogSeed(t, f)
	out, _, err := execute(t, "backlog", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		Project string `json:"project"`
		Waiting int    `json:"waiting"`
		Items   []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Phase    string `json:"phase"`
			Repo     string `json:"repo"`
			Node     string `json:"node"`
			Approved bool   `json:"approved"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("fugaro backlog list --json printed no document: %v\n%s", err, out)
	}
	if doc.Version != backlogOutputVersion || doc.Kind != "backlog" {
		t.Errorf("the document says version %d, kind %q; want %d and backlog", doc.Version, doc.Kind, backlogOutputVersion)
	}
	if doc.Project != orchestratorTestID {
		t.Errorf("project = %q, want %q: the document says which project's backlog it read", doc.Project, orchestratorTestID)
	}
	if doc.Waiting != 3 {
		t.Errorf("waiting = %d, want 3: every item here arrived unapproved (D2717)", doc.Waiting)
	}
	if len(doc.Items) != 3 {
		t.Fatalf("the document carries %d rows, want 3", len(doc.Items))
	}
	first := doc.Items[0]
	if first.ID != "20261108-01" || first.Title != "Review round 2 left 3 findings open" ||
		first.Phase != string(orchestrator.PhaseQueued) || first.Repo != "acme/app" ||
		first.Node != orchestrator.BacklogNode || first.Approved {
		t.Errorf("the first row is %+v, want the item the command filed, unapproved and in the backlog node", first)
	}
	if doc.Items[2].Node != orchestrator.StateNode {
		t.Errorf("the last row's node is %q, want %q: the row says which node it was read from", doc.Items[2].Node, orchestrator.StateNode)
	}
}

// TestBacklogListJSONOnAnEmptyBacklogSaysSoWithAnEmptyList: an empty backlog is
// a document with an empty list and a zero, not a document with an absent key
// and not a `null`. The contract's own rule is that a list is `[]` rather than
// missing, so a screen can render an empty backlog without a second guess, and
// the difference is invisible on any fixture that has an item in it.
//
// Mutation (verified, run and restored): build the rows with `var rows
// []backlogRow` instead of a made slice — this fails, because the document then
// carries `"items": null`.
func TestBacklogListJSONOnAnEmptyBacklogSaysSoWithAnEmptyList(t *testing.T) {
	isolateState(t)
	newOrchestratorFixture(t)
	out, _, err := execute(t, "backlog", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"items": []`) {
		t.Errorf("an empty backlog's document does not carry `\"items\": []`:\n%s", out)
	}
	if strings.Contains(out, "null") {
		t.Errorf("an empty backlog's document carries a null, which a reader cannot tell from a key the command forgot to emit:\n%s", out)
	}
	var doc struct {
		Waiting int   `json:"waiting"`
		Items   []any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("fugaro backlog list --json printed no document: %v\n%s", err, out)
	}
	if len(doc.Items) != 0 || doc.Waiting != 0 {
		t.Errorf("an empty backlog's document is %+v, want an empty list and a zero", doc)
	}
	// The human half says the same thing, so the two spellings of "no items"
	// cannot drift.
	plain, _, err := execute(t, "backlog", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "no items") {
		t.Errorf("an empty backlog's listing does not say so:\n%s", plain)
	}
}

// TestBacklogRefusesAFolderThatIsNotThere: a missing folder is an error and
// not an empty index. `index` is the command that reads the folder — `list`
// reads the project's database — and it writes nothing on the way out. The same
// rule internal/backlog states, at the CLI's boundary: an empty index would
// read as a folder with no items.
//
// Mutation (verified, run and restored): make backlog.Load return an empty
// slice for a folder it cannot read — this fails.
func TestBacklogRefusesAFolderThatIsNotThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	if _, _, err := execute(t, "backlog", "index", "--dir", dir); err == nil {
		t.Error("fugaro backlog index accepted a folder that is not there")
	}
	if _, err := os.Stat(filepath.Join(dir, backlog.IndexName)); !os.IsNotExist(err) {
		t.Errorf("fugaro backlog index left %s behind in a folder it could not read: %v", backlog.IndexName, err)
	}
}

// TestBacklogIndexRefusesAnItemTheFormatRefuses: the folder is validated
// before the index is written, so a folder holding a file that is not an item
// leaves the index as it was rather than publishing a document that silently
// omits it. This is the same rule the folder's completeness guard enforces
// from the other side.
//
// Mutation (verified, run and restored): have runBacklogIndex ignore Load's
// error and render an empty index — this fails on the file it wrote.
func TestBacklogIndexRefusesAnItemTheFormatRefuses(t *testing.T) {
	dir := backlogFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "backlog", "index", "--dir", dir); err == nil {
		t.Error("fugaro backlog index accepted a folder holding a file that is not an item")
	}
	if _, err := os.Stat(filepath.Join(dir, backlog.IndexName)); !os.IsNotExist(err) {
		t.Errorf("fugaro backlog index wrote %s from a folder it refused: %v", backlog.IndexName, err)
	}
}

// backlogNodes is the two nodes a list reads, as bytes: the whole of what the
// command could have written, so "writes nothing" is a fact about the database
// rather than about the one field a test thought of.
func backlogNodes(t *testing.T, f *budgetFixture) string {
	t.Helper()
	var out strings.Builder
	for _, s := range []orchestrator.State{storedBacklogState(t, f), storedOrchestratorState(t, f)} {
		raw, err := orchestrator.EncodeState(s)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(raw)
	}
	return out.String()
}
