package runview

// The plan task id is display text and nothing else.
//
// Row.PlanTask is a join hint written by whoever launched the run, so it is
// attacker-controlled for anyone who can write a launch.json (a launcher,
// and on a shared job every run: SECURITY.md, D4). The plan it names is
// never read from the run, the id authorises nothing, admits nothing and
// budgets nothing (the console dependency graph plan's decision GD5 and its
// constraint C3): it is how a row is matched to a node of a plan graph the
// operator holds, no more. The only way to keep that true as the code grows
// is to fail a test the moment some other package starts reading it.
//
// This is that test, in the shape of launched_by_test.go beside it: a
// whole-module scan with an allow-list, because a rule that named the
// packages it knows about would pass the day someone adds a new one, which
// is exactly the day it would matter.

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// planTaskNaming matches the field's two spellings as words: the Go name
// alone, so that a longer identifier built on it (planTaskSince,
// PlanTaskProblem, PlanTaskElemRE) is not mistaken for a read of the field
// itself, and the JSON key.
var planTaskNaming = regexp.MustCompile(`\bPlanTask\b|plan_task`)

// planTaskReaders is the allow-list: a non-test Go file that may name
// PlanTask or the plan_task key, and why. Anything else that names it fails
// TestPlanTaskIsDisplayTextOnly.
var planTaskReaders = map[string]string{
	// The field's own definition and its shape rule.
	"internal/task/task.go": "defines task.Spec.PlanTask and PlanTaskProblem, the shape the launcher applies on write and task.Parse on read",
	// The copy a launched review's spec carries (docs/review-against-task R2):
	// the launcher copies the id off the root task onto the spec the review job
	// is handed, because that job holds no other run of its own (F3), and the
	// runner then quotes the section of the plan it names, as data, to the
	// reviewer (D2932). The plan is matched by id against an enumerated list of
	// the plan graphs the operator holds -- never a path built from this text,
	// and never an authorisation -- so the worst a forged id can reach is the
	// text of a plan document a person already has, shown to a reviewer. It
	// admits no run, budgets nothing and grants nothing.
	"internal/task/reviewspec.go": "defines task.ReviewSpec.PlanTask, the copy the launcher carries onto a launched review's spec so its job can quote that plan task's section",
	// The launch record it is read from, which is the untrusted object.
	"internal/runstore/launch.go": "defines runstore.Launch.PlanTask, the launch.json field itself",
	// The launcher that carries the copy above (docs/review-against-task RA5a,
	// R2): it reads the id off the root run's stored task and puts it on the
	// spec a launched review is handed, and it applies task.PlanTaskProblem to
	// it before it does. It decides nothing else by the id -- no path is built
	// from it, nothing is matched on it -- so what it can reach is what the
	// copy it writes reaches: the text of a plan document the operator already
	// holds, shown to a reviewer, and it admits no run, budgets nothing and
	// grants nothing.
	"internal/cli/reviewspec.go": "copies the root task's id onto a launched review's spec, after applying task.PlanTaskProblem to it",
	// The id a run carries out in its own record, which is the field and not a
	// read of it. runstore.DiscoveredItem.PlanTask is what the run's own agent
	// reported discovering, bounded at write and again at read, and the daemon
	// folds it add-only into a plan's graph (E3): an edge in front of an item
	// that has not launched, which un-approves that item so the person sees why
	// and approves again (D2740), or at most one graph_suspect fact for an item
	// this run needed, which reorders nothing (D2709). So the worst a forged id
	// can do is draw an edge a screen shows and a person re-approves; it admits
	// no run, budgets nothing and launches nothing, and a fold that would make a
	// cycle or pass the node's cap is refused outright.
	"internal/runstore/discovered.go": "defines runstore.DiscoveredItem.PlanTask, the id a run's own record carries out for the daemon to fold, add-only, into a graph a screen draws",
	// The two runner files that put such an entry on the record and ask for it
	// (E2). The reader copies the id out of the file the run's own agent wrote
	// and decides nothing by it: it draws no edge, reads no graph and admits
	// nothing, and what it does carry is bounded at write and again at read
	// (runstore.NewDiscovered). The brief is a sentence to the coder naming the
	// key it writes an entry under, and reads nothing at all.
	"internal/runner/discovered.go": "reads the id the run's own agent reported into the record's block, bounded and redacted, and decides nothing by it",
	"internal/runner/prompts.go":    "the coder's brief names the key an entry is written under; a text instructing a model reads nothing",
	// The review's own read of the id (docs/review-against-task R2, D2932): the
	// runner copies it out of the task or the launch record it is already
	// reading and matches it against an enumerated list of the plan graphs the
	// operator holds -- never a path built from this text, so the worst a forged
	// id can reach is the text of a plan document a person already has, quoted
	// to a reviewer behind a nonce fence. It admits no run, budgets nothing and
	// launches nothing.
	"internal/runner/taskspec.go": "resolves the task a review is checked against and quotes the plan task's section out of the plan the id matches, by id over the tree's own listing",
	// The one writer of both records: fugaro run stores the id in the task
	// (so --retry and the recovery path keep it) and in launch.json, and
	// gates the launch on the image whose runner can read it.
	"internal/cli/run.go": "writes the id into task.json and launch.json at launch, and gates the launch on the image that reads it",
	// A follow-up's spec carries its own --plan-task, never the followed
	// run's.
	"internal/cli/followup.go": "writes a follow-up's own --plan-task into its task",
	// Only the version floor's comment names the field: the constant beside
	// this file's allow-list value is what a launch is gated on.
	"internal/cli/recipes_skew.go": "planTaskSince, the release floor the launcher's gate judges an image by",
	// The duplicate guard (fugaro run --plan-task): it matches the id a
	// launch is about to claim against the ids the runs of one repository
	// already carry, and does exactly one thing with a match -- declines to
	// start a second run of that task while a run of it is going or a run of
	// it left a pull request behind (run_duplicate.go). It is on this list
	// deliberately and with that reasoning: the id is untrusted for anyone
	// who can write a launch.json, and the guard is written so that the worst
	// a forged id can do is make a NEW launch refuse, which is the direction
	// the display-only rule cares about. It admits no run, budgets nothing,
	// grants nothing, reads no code host (the rows it was handed are the whole
	// of its evidence), fails open (a read it cannot make warns and launches)
	// and is escaped by --allow-duplicate, and no state a claimed id could
	// reach is a state a reader acts on -- the refusal is the whole of it and
	// nothing is written back.
	"internal/cli/run_duplicate.go": "the launch-time duplicate guard: it refuses to launch a plan task while a run of it is going or a run of it left a pull request behind",
	// The documents that show it, and the reads that feed them.
	"internal/runview/runview.go":  "the runs ls / diagnose row's own field, sanitised from the launch record",
	"internal/watch/build.go":      "the watch row's own field",
	"internal/watch/facts.go":      "stamps the id a scan read onto the rows a frame draws",
	"internal/watch/json.go":       "the watch --json key",
	"internal/cli/watch_queued.go": "reads launch.json for the rows the watch scan produces, and carries the fact into the frame",
	// The graph joined to the runs (GV1.6): the join matches a plan's task
	// ids against the id the row already carries, and both sides are display
	// only -- the id decides nothing, and the state it produces is a word a
	// screen draws.
	// The index entry carries it as the join key plan status and graph match a
	// run to a node by (C-INDEX, D3201). The entry is a fold of the run's own
	// launch record, the id is shape-checked where it is read, and the join it
	// serves decides nothing but which node of a plan a row is drawn under --
	// the same display-only join the rows already carry, from the same field,
	// and no path is built from it.
	"internal/runindex/contract.go": "the index entry's own plan_task field: the id plan status and graph join a run to a node by, display-only",
	// The fold that writes such an entry (RI4): it copies the id out of the
	// run's own task record into the document and decides nothing by it -- no
	// path is built from it, nothing is matched on it, and it admits no run,
	// budgets nothing and grants nothing.
	"internal/runindex/fold.go": "writes the entry's plan_task from the run's own task record, for the join a screen draws",
	// The reader of that entry (RI3): it names the field as the equality one
	// page of the index is queried by, and the value is compared and nothing
	// else -- no path is built from it, no record is opened by it, and what the
	// query returns carries it as the display text contract.go's entry above
	// already allows. The id a page was filtered by is the caller's own
	// argument, never a value read out of a run.
	"internal/runindex/firestore.go": "names the field as the equality a page of the index is queried by, and decides nothing by it",
	"internal/plan/state.go":         "joins the id the row published to a plan's task, for the states a screen draws",
	"internal/cli/plan_status.go":    "turns the read's own sanitised rows into the facts the join reads; it never opens launch.json",
	// The orchestrator's own graph: an item carries the plan task it stands
	// for, so the import of a plan (D2704) and the plan's adopted runs in
	// flight (D2702) meet in one node instead of duplicating it (D2746). The
	// id decides nothing here: the state carries it, shape-checks it and
	// digests it, and the reads the plan schedules key on the operator's own
	// graph rather than on anything the id authorises.
	"internal/orchestrator/state.go":    "defines WorkItem.PlanTask, the node of a plan the item stands for",
	"internal/orchestrator/validate.go": "applies task.PlanTaskProblem to the id a patch writes, the shape rule the launcher applies on write",
	"internal/orchestrator/patch.go":    "the upsert op that carries a plan's node id into the state",
	// The claim one item's work is keyed by (CG4, D3209): ClaimRef reads the
	// item's plan task so that two orchestrators of one project that imported
	// one plan claim the SAME node for it, and only one of them works it. The
	// id decides which claim node one item's work stands under and nothing
	// else. The node is `orchestrator/~claims/<plan task>` with the id escaped
	// by budget.Key, so no id leaves that node, it is under the daemon's own
	// IAM fence like the state beside it (the rules refuse every token there),
	// and the claim admits no run, budgets nothing and grants nothing. Both
	// sides of the choice are items already in the node, and the worst a forged
	// id can reach is a collision -- an item whose claim stands where another's
	// was taken -- which makes an orchestrator SKIP work rather than start a
	// second run of it, the refusing direction this rule cares about.
	"internal/orchestrator/claim.go": "keys an item's claim node by the plan task it stands for, so the project's orchestrators claim one node per task (CG4, D3209)",
	// Adoption (R4, D2702): a run in flight whose launch recorded a plan task
	// is filed in that plan's own node rather than beside it, which is the one
	// thing here the id decides -- and it decides it about a record of a run
	// that is really in flight, not about a run. A forged id files that record
	// under a plan node of its choosing, so what it can reach is a document in
	// the daemon's own subtree, written under the daemon's own lease: the name
	// is specName's fold and bound and PathPlan's check, so no id escapes
	// `orchestrator/<id>/plans/`, and the fact it leaves is one bounded,
	// escaped line (cleanLine) asking a session to import a plan the operator
	// already holds -- an import that is add-only, queued and unapproved. It
	// admits no run, budgets nothing, grants nothing and launches nothing: the
	// item is written unapproved by construction, and the ladder will not
	// enter the rung that merges one (D2702).
	"internal/orchestrator/adopt.go": "files the record of a run in flight under the plan node its launch named, and names that plan in one fact (D2702, D2721, D2746)",
	// Adoption's reader of the runs bucket (R4, D2702): the plan task a run's
	// own launch record carries is read out here and into RunInFlight, which is
	// the record adoption files -- so the id decides exactly what it decides
	// inside internal/orchestrator/adopt.go and nothing more: which plan's node
	// one in-flight run is filed under, under the same fold and bound
	// (specName, PathPlan), leaving the item unapproved by construction. It
	// admits no run, budgets nothing, grants nothing and launches nothing.
	"internal/cli/orchestrator_runlister.go": "reads the plan task a run's own launch record carries into RunInFlight, which is what adoption files under the plan node the id names (D2702)",
	// The item's id on its way to the launch (B7): the declaration the state
	// digests carries it into the spec the launcher is handed, and the daemon's
	// launcher passes it through the command's own resolution, which is what
	// writes it into the task and into launch.json -- the records a screen joins
	// a run to its node with. Neither decides anything with it.
	"internal/orchestrator/launch.go":       "carries the item's plan task into the launch spec (Spec.PlanTask, SpecOf), where the launcher reads it",
	"internal/cli/orchestrator_launcher.go": "passes the item's plan task into the command's own launch resolution, which records it in the task and in launch.json",
	// The import of a plan's graph (D2704): it writes the id as an item's
	// plan_task, and matches on it so an item the daemon adopted for the same
	// task is linked rather than duplicated (D2746). What the match decides is
	// which item of the plan's own node an edge names, and both sides are items
	// already in that node -- the adopted one written by the daemon from a
	// launch record, the one this import would write arriving unapproved -- so
	// nothing is admitted, budgeted or granted by it, and it refuses rather
	// than guesses when one plan task stands under two items.
	"internal/orchestrator/planimport.go": "writes a plan's task id as an item's plan_task, and matches on it to link an adopted item rather than duplicate it (D2746)",
	// The fold of what a finished run reported about the graph (E3): it matches
	// the id an entry of the run's OWN RECORD carries against the plan task of
	// the items of the node that run's item is in, and the id decides nothing
	// beyond which of those items an edge names. Both sides are items the node
	// already holds -- one written by a plan's import, one adopted from a launch
	// record -- and the entry arrives through runstore.Discovered, bounded at
	// write and again at read. What a match can do is add one edge in front of
	// an item that has not launched, which un-approves that item so a person
	// reads why and approves again (D2740), and at most one graph_suspect fact
	// for an item the run said it needed, which reorders nothing. The fold is
	// add-only, is refused outright for a cycle or a node past its cap (F42) and
	// crosses into no other node (F44): a forged id draws an edge a screen shows
	// and a person re-approves, and it admits no run, budgets nothing and
	// launches nothing.
	"internal/orchestrator/fold_discovered.go": "matches the id an entry of a run's own record carries against the items' plan tasks in the run's own node, to add one edge in front of an item that has not launched (E3, D2709)",
	// The resolution of a cross-orchestrator reference (CG3, D3208): the item
	// half of `<orchestrator>/<item>` may be a work item id or the plan task an
	// item stands for (validItem), so finding the item a reference names means
	// matching against the id an item carries -- and the match decides exactly
	// what the fold's and the import's do: which item of a graph the operator
	// already holds an edge points at. Both sides are items a node already
	// carries, written by a plan's import or adopted from a launch record under
	// the daemon's own lease, and the edge is written by the operator's own
	// patch or by the fold, which only adds one in front of an item that has not
	// launched. What a match can reach is a phase, and a phase decides nothing
	// by itself: the item it holds or frees is still gated by its own approval,
	// its spec digest, the caps and the breaker, so nothing is admitted,
	// budgeted or granted by the id.
	"internal/orchestrator/shard.go": "finds the item a cross-orchestrator reference names in one orchestrator's shards, by work item id or by the plan task an item stands for (D3208)",
	// The note a blocked cycle leaves (CG3, D3220): it names each item of the
	// cycle the way a reference does, so the person who reads it edits the
	// dependency the note points at. It is the text of one bounded line and
	// nothing is matched, decided or reached by it.
	"internal/orchestrator/tick.go": "writes the plan task an item stands for into the note of a cycle it blocked, so the note names the item a person would edit (D3220)",
}

// TestPlanTaskIsDisplayTextOnly walks every non-test Go file of the module
// and fails on any that names the plan task outside the allow-list above: a
// reader that is not the task, the launch record, the launcher, the row or a
// renderer is a reader that could be a decision.
func TestPlanTaskIsDisplayTextOnly(t *testing.T) {
	root := filepath.Join("..", "..") // internal/runview -> the module root
	var scanned int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if !planTaskNaming.Match(src) {
			return nil
		}
		if _, ok := planTaskReaders[rel]; !ok {
			t.Errorf("%s names the plan task (PlanTask/plan_task), but it is not on the display allow-list in %s: "+
				"the id is untrusted text written by whoever launched the run, and nothing may decide anything by it",
				rel, "internal/runview/plan_task_test.go")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	// A walk that read nothing would pass silently: pin that it read the tree.
	if scanned < 100 {
		t.Fatalf("only %d Go files were scanned; the walk is looking in the wrong place", scanned)
	}
	// And the other direction: an allow-list entry for a file that no longer
	// names the plan task is a stale permission, which is how an allow-list
	// quietly grows to cover code that should not be on it.
	for rel := range planTaskReaders {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("allow-list names %s: %v", rel, err)
			continue
		}
		if !planTaskNaming.Match(b) {
			t.Errorf("allow-list still names %s, which no longer mentions the plan task", rel)
		}
	}
	if len(planTaskReaders) == 0 {
		t.Fatal("the allow-list is empty")
	}
}

// TestPlanTaskAllowListNamesTheRealField keeps the allow-list from drifting
// away from the field it is about: the scan above looks for the Go name
// PlanTask, so a rename of the JSON key alone (which every reader of
// plan_task would notice, and this scan would not) is caught here.
func TestPlanTaskAllowListNamesTheRealField(t *testing.T) {
	f, ok := reflect.TypeOf(Row{}).FieldByName("PlanTask")
	if !ok {
		t.Fatal("runview.Row has no PlanTask field: the plan task is not on the row at all")
	}
	if got := strings.Split(f.Tag.Get("json"), ",")[0]; got != "plan_task" {
		t.Fatalf("the row's plan task key is %q, not plan_task: the documents that read the key, and the allow-list scan's name, are out of step", got)
	}
}
