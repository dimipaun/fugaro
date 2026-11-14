package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"gocloud.dev/blob"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// `fugaro runs watch` prints the long-run triage (O14c).
//
// What is pinned here is that the print is the daemon's own decision and
// nothing else: the same rule over the same reading, the same threshold, an
// unreadable input named rather than turned into an error, and not one write
// anywhere — no record, no audit line, no marker, no work item and no host
// call that changes anything. The command carries no flag that acts, and the
// run it prints is the one the tick judges: the two are compared through the
// daemon's own input builder, and for the fail-closed outcome through the
// audit line the tick itself wrote.

// watchNowAt pins the clock both sides read: the CLI's listing window (lsNow)
// and the fixture's database (orchestratorTestNow). The daemon's triage ages a
// run from the intent it wrote and `runs watch` from the run's own mint time,
// so a test that means the two to agree has to hand both the same instant.
func watchNowAt(t *testing.T, at time.Time) {
	t.Helper()
	wasLs, wasOrch := lsNow, orchestratorTestNow
	lsNow = func() time.Time { return at }
	orchestratorTestNow = at
	t.Cleanup(func() { lsNow, orchestratorTestNow = wasLs, wasOrch })
}

// newWatchFixture is the daemon's triage fixture with this command's own host
// seam pointed at the same fake host, and the clock pinned so that the run's
// mint time is the instant its intent carries: the daemon's age and the age
// the print computes are then the same triageAge exactly.
//
// The clock is pinned before the fixture is built, because the fixture stamps
// the seeded intent and its database with it.
func newWatchFixture(t *testing.T, checks []gitprov.CheckRun) *triageFixture {
	t.Helper()
	at, err := runstore.RunTime(triageRunID)
	if err != nil {
		t.Fatal(err)
	}
	watchNowAt(t, at.Add(triageAge))
	f := newTriageFixture(t, checks)
	installWatchProvider(t, f.prov)
	return f
}

// installWatchProvider hands `runs watch` a fake host, the way every other
// command in this package is handed one. Which repositories the command asks it
// about is not this helper's business; a test that pins that reads
// installWatchProviderAsking.
func installWatchProvider(t *testing.T, p *fake.Provider) {
	t.Helper()
	installWatchProviderAsking(t, p)
}

// installWatchProviderAsking is installWatchProvider with the seam's own
// question kept: every repository the command asks the host about, in the order
// it asks them. The repository is what a print about a run's pull request and
// its checks is decided by, so it is what a test reads to say which repository
// the print was about (F24, F30).
func installWatchProviderAsking(t *testing.T, p *fake.Provider) *[]string {
	t.Helper()
	asked := &[]string{}
	was := openWatchProvider
	openWatchProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "github", func(_ context.Context, _, repo string) (gitprov.Provider, []string, error) {
			*asked = append(*asked, repo)
			return p, nil, nil
		}, nil
	}
	t.Cleanup(func() { openWatchProvider = was })
	return asked
}

// watchJSON is `fugaro runs watch --json`'s document.
func watchJSON(t *testing.T, args ...string) watchDoc {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "watch", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("runs watch: %v (%s)", err, errOut)
	}
	var got watchDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

// watchJSONKeys is that document's own keys and its first row's: the field set
// is what a consumer parses, so it is asserted as a set rather than one field
// at a time.
func watchJSONKeys(t *testing.T, args ...string) (doc, row []string) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "watch", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("runs watch: %v (%s)", err, errOut)
	}
	var d map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(d["runs"], &rows); err != nil {
		t.Fatalf("runs: %v: %s", err, out)
	}
	if len(rows) == 0 {
		t.Fatalf("the document judges no run: %s", out)
	}
	return slices.Sorted(maps.Keys(d)), slices.Sorted(maps.Keys(rows[0]))
}

// watchObjects is every object the bucket holds, keyed by name: the whole of
// what a write or a delete would change, so a command that writes nothing
// leaves it equal to the snapshot taken before it ran.
func watchObjects(t *testing.T, url string) map[string]string {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	out := map[string]string{}
	for it := b.List(nil); ; {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := b.ReadAll(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[obj.Key] = string(body)
	}
	return out
}

// watchObjectDiff is how two snapshots differ, one line each: what a command
// that wrote nothing leaves empty.
func watchObjectDiff(before, after map[string]string) []string {
	var out []string
	for key, body := range after {
		was, ok := before[key]
		switch {
		case !ok:
			out = append(out, "written: "+key)
		case was != body:
			out = append(out, "changed: "+key)
		}
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			out = append(out, "deleted: "+key)
		}
	}
	slices.Sort(out)
	return out
}

// watchLine is an outcome as one line of text: the outcome and the codes
// behind it, joined the way the daemon writes its own audit line. It is the
// composition the print is held to, not a second definition of it — the tick's
// line is compared against this, so the two have to agree on the words.
func watchLine(outcome string, reasons []string) string {
	return outcome + ": " + strings.Join(reasons, " ")
}

// TestRunsWatchPrintsTheTriageWithoutWriting: the print is a reading and
// nothing else. Every write a triage could make fails the test — the
// database's writes are refused and its whole tree is compared before and
// after, the bucket is compared object by object, and the host records every
// write it is asked for — and the print stands: a command that changed
// anything to say what it says would fail each of the three.
//
// Mutation (run, restore): in `runs watch`, call `orchestratorTriage`'s own
// seams instead of reading — `Cancel` after a restart outcome, or `Append` the
// line the tick writes — and the refused write fails the command itself, while
// the database tree comparison catches one that swallowed the error.
func TestRunsWatchPrintsTheTriageWithoutWriting(t *testing.T) {
	f := newWatchFixture(t, green())
	before := watchObjects(t, f.bucket)
	dbBefore, _ := json.Marshal(f.db.Value("/"))
	hostBefore := f.prov.Snapshot().Calls
	// The guard on the guard: the tree this compares is the whole database, and
	// a path that answered nothing would make a comparison of two empty things
	// pass everything. The node the daemon iterates is in it.
	if !strings.Contains(string(dbBefore), "orchestrator") {
		t.Fatalf("the database snapshot is %s, and the triage's own state and log are not in it", dbBefore)
	}
	calls := failEveryCall(t)

	out, errOut, err := execute(t, "runs", "watch")
	if err != nil {
		t.Fatalf("runs watch: %v (%s)", err, errOut)
	}
	// The one thing this command reads from the database is the roles node the
	// gate every reading command shares asks for (RS6a), and it reads nothing
	// else: the triage line a long run leaves is the tick's to write, and this
	// command is a print. The reads are named rather than counted, because a
	// count alone cannot tell a roles read from a read that reaches the daemon's
	// own nodes, and the failure has to say which one it was.
	if got := calls.all(); len(got) != 1 || got[0] != "GET /"+accesslib.PathNode+".json" {
		t.Errorf("runs watch made the database calls %v, and one read of the roles (%s) is the whole of it: the triage line a long run leaves is the tick's to write, and this command is a print", got, accesslib.PathNode)
	}
	if dbAfter, _ := json.Marshal(f.db.Value("/")); string(dbAfter) != string(dbBefore) {
		t.Errorf("runs watch changed the project's database:\nbefore %s\nafter  %s", dbBefore, dbAfter)
	}
	if got := f.prov.Snapshot().Calls; len(got) != len(hostBefore) {
		t.Errorf("the host was asked to write %v, and a print changes nothing on it", got[len(hostBefore):])
	}
	if diff := watchObjectDiff(before, watchObjects(t, f.bucket)); len(diff) > 0 {
		t.Errorf("runs watch changed the runs bucket: %v", diff)
	}
	// The print itself: the run's row, with the fail-closed outcome the rule
	// takes when the runner's findings artifact is not there.
	line := watchRowLine(t, out, appSlug, triageRunID)
	for _, want := range []string{"recommend-only", "findings_absent", "findings"} {
		if !strings.Contains(line, want) {
			t.Errorf("the row %q does not carry %q:\n%s", line, want, out)
		}
	}
}

// watchRowLine is the table's own line for one run, which is what a person
// reads: the line whose first cell is the run's ending, computed the way the
// print computes it rather than assumed.
func watchRowLine(t *testing.T, out, slug, runID string) string {
	t.Helper()
	ref := task.Ref{Slug: slug, RunID: runID}
	ending := task.DisplayRuns([]task.Ref{ref})[ref]
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == ending {
			return line
		}
	}
	t.Fatalf("the print has no row for %s/%s:\n%s", slug, runID, out)
	return ""
}

// watchCalls splits a fake's request log into what it read and what it wrote:
// a read of an object, a listing and a metadata get are all GETs, so the
// writes are everything else. It is the instrument the seams below are watched
// with, and it is taken from the fakes themselves rather than from a snapshot
// of their state, so a write that was refused or swallowed is still seen.
func watchCalls(reqs []gcpfake.Request) (reads, writes []string) {
	for _, r := range reqs {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			reads = append(reads, r.Method+" "+r.Path)
			continue
		}
		writes = append(writes, r.Method+" "+r.Path)
	}
	return reads, writes
}

// watchNoNewWrites fails when a seam was asked to write since the snapshot
// taken before, which is what a print leaves untouched.
func watchNoNewWrites(t *testing.T, seam string, before, after []string) {
	t.Helper()
	if len(after) != len(before) {
		t.Errorf("runs watch made %s write %v, and a print changes nothing anywhere", seam, after[len(before):])
	}
}

// TestRunsWatchTriageReachesNoWriteSeam: every seam a run's triage could write
// through is instrumented — the runs bucket, the project's database, the
// backend that starts and stops runs, and the code host — and the print
// touches none of them.
//
// The instruments are the fakes' own request logs, and the ones the print
// reads through are proved live in the same test: the bucket is the GCS fake,
// so a write is a request it records rather than a change a snapshot has to
// notice, and the host is asked about the run's own repository, which is what
// the row's outcome is taken from. The backend the print only reads (the
// listing joins each run's execution) and the database it never reaches at all
// are instrumented so that a change which makes it start or cancel a run, or
// write a record, an audit line or a cancel marker, fails here rather than
// beside a running daemon.
//
// The reading is driven with the environment handed in — watchRead and
// writeRunsWatch, which is the whole of what runRunsWatch does with them —
// because the bucket has to be the GCS fake for a write to be a request
// (gcsEnv's own construction: a bucket a command opens itself is a directory
// or a real bucket, and watching one of those is watching the filesystem).
//
// Mutation (run, restore): in watchRead, write the reading back —
// `runstore.Open(env.bucket.Bucket, ref.Slug, ref.RunID).PutFile(ctx,
// "triage.json", …)` after the outcome is taken — and this fails naming the
// object the fake was asked to store.
func TestRunsWatchTriageReachesNoWriteSeam(t *testing.T) {
	f := newWatchFixture(t, green())
	// A reading that is whole: the artifact the runner finalized with, and the
	// execution its launch record names, so the print reads the host and the
	// backend for real and the instruments are watching seams it reaches.
	f.findings(t)
	job := gcp.JobName(appSlug, "web")
	exec := startRunExecution(f.cloudFixture, job, appSlug, triageRunID)
	if err := f.store(t).WriteLaunch(context.Background(), &runstore.Launch{
		Version: 1, RunID: triageRunID, Backend: "cloud-run", Job: job,
		Execution: exec, LaunchedAt: orchestratorTestNow,
	}); err != nil {
		t.Fatal(err)
	}
	asked := installWatchProviderAsking(t, f.prov)

	// The runs bucket, as the GCS fake: every read of the run's record, task
	// and findings goes through its log, and so would a write.
	gcs := gcpfake.NewGCS(t)
	for name, body := range watchObjects(t, f.bucket) {
		gcs.Put("runs", name, []byte(body))
	}
	env := envOn(t, f.cloudFixture, gcs.Bucket(t, "runs"))

	// Every seam as it stands before the print. The database's log is not empty
	// already — the fixture's own daemon took its lease through it — so an
	// empty difference after the print is a fact about the print.
	_, gcsWrote := watchCalls(gcs.Requests())
	_, runWrote := watchCalls(f.run.Requests())
	_, dbWrote := watchCalls(f.db.Requests())
	hostWrote := len(f.prov.Snapshot().Calls)

	doc, err := watchRead(context.Background(), env, &runsWatchOptions{}, 0, lsNow())
	if err != nil {
		t.Fatalf("runs watch: %v", err)
	}
	var out strings.Builder
	if err := writeRunsWatch(&out, doc, false); err != nil {
		t.Fatalf("printing the triage: %v", err)
	}

	// The print itself: the window's one run, judged the way its own host's
	// green checks and its clear findings give it, and nothing warned about —
	// a reading that was whole.
	if len(doc.Runs) != 1 {
		t.Fatalf("the print judged %d runs, want the window's one: %+v", len(doc.Runs), doc.Runs)
	}
	row := doc.Runs[0]
	if row.Outcome != string(orchestrator.MergeAsIs) || !slices.Contains(row.Reasons, string(orchestrator.ReasonCIGreen)) {
		t.Errorf("the print judged the run %q %v, want the merge the run's own host's green checks and its clear findings give it", row.Outcome, row.Reasons)
	}
	if len(*asked) != 1 || (*asked)[0] != "acme/app" {
		t.Errorf("the print asked a host about %v, want the run's own repository alone", *asked)
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("the print warned about %v, and a reading that is whole warns about nothing", doc.Warnings)
	}

	// And not one seam was asked to write.
	gcsRead, gcsWroteAfter := watchCalls(gcs.Requests())
	runRead, runWroteAfter := watchCalls(f.run.Requests())
	_, dbWroteAfter := watchCalls(f.db.Requests())
	watchNoNewWrites(t, "the runs bucket", gcsWrote, gcsWroteAfter)
	watchNoNewWrites(t, "the backend", runWrote, runWroteAfter)
	watchNoNewWrites(t, "the project's database", dbWrote, dbWroteAfter)
	if got := len(f.prov.Snapshot().Calls); got != hostWrote {
		t.Errorf("the code host was asked to write %v, and a print changes nothing on it", f.prov.Snapshot().Calls[hostWrote:])
	}

	// The guard on the guard: the bucket and the backend are read through in
	// this test, and the database's log already holds the fixture's own writes,
	// so none of the three is an instrument watching a seam nothing reaches.
	if len(gcsRead) == 0 {
		t.Error("no read of the runs bucket reached the GCS fake, so its empty write log is a fact about this test and not about the print")
	}
	if len(runRead) == 0 {
		t.Error("no read of the executions reached the backend fake, so its empty write log is a fact about this test and not about the print")
	}
	if len(dbWrote) == 0 {
		t.Error("the database's write log is empty before the print, so nothing there shows a write through it would be caught")
	}
}

// TestRunsWatchPrintsTheSameOutcomeAsTheDaemon: the print is the daemon's own
// decision, not a second reading of the same facts. The rule is run twice over
// the one input — once through the reading the tick's own builder makes, once
// by the command — and the two have to agree on the outcome, the codes, the
// input neither could read and whether a person was asked. For the fail-closed
// case the daemon is then stepped for real and its audit line compared with
// the row, which is the same claim one layer out: the tick's line and the
// print are the same words.
func TestRunsWatchPrintsTheSameOutcomeAsTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name string
		// findings are the runner's artifact: nil writes none, and an empty
		// list is a run whose review left nothing open.
		findings []string
		// stepped is whether the daemon is run for real and its audit line
		// compared with the row. A merge is not stepped: the tick's actor
		// refuses every merge (O14b) and its line says so, which is a fact
		// about the actor and not about the rule this print is.
		stepped bool
	}{
		{name: "the findings are not there at all", stepped: true},
		{name: "the work is wholly routine and CI is green", findings: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t, green())
			if tc.findings != nil {
				f.findings(t, tc.findings...)
			}
			// The daemon's own reading, through the tick's own builder, with
			// the figures the cycle itself sets on it.
			target := f.target(t)
			in := f.input(t)
			in.Elapsed, in.Threshold = target.Age, target.Threshold
			want := orchestrator.Triage(in)

			doc := watchJSON(t)
			if len(doc.Runs) != 1 {
				t.Fatalf("the document judges %d runs, want the one the bucket holds: %+v", len(doc.Runs), doc.Runs)
			}
			row := doc.Runs[0]
			reasons := make([]string, 0, len(want.Reasons))
			for _, r := range want.Reasons {
				reasons = append(reasons, string(r))
				if !orchestrator.KnownTriageReason(r) {
					t.Errorf("the daemon's outcome carries the code %q, which is outside the closed vocabulary", r)
				}
			}
			if row.Outcome != watchOutcome(want) {
				t.Errorf("the print says the run is %q, and the daemon's own reading of it is %q", row.Outcome, watchOutcome(want))
			}
			if !slices.Equal(row.Reasons, reasons) {
				t.Errorf("the print gives the codes %v, and the daemon's own reading gives %v", row.Reasons, reasons)
			}
			if row.Unreadable != string(want.Unreadable) {
				t.Errorf("the print names %q as the input it could not read, and the daemon names %q", row.Unreadable, want.Unreadable)
			}
			if row.WithoutAPerson != want.WithoutAPerson {
				t.Errorf("the print says without_a_person is %v, and the rule says %v", row.WithoutAPerson, want.WithoutAPerson)
			}
			if !tc.stepped {
				return
			}
			// The tick's own line, which is what fences the run for every
			// cycle after this one.
			f.step(t)
			if got, want := f.line(t).Text, watchLine(row.Outcome, row.Reasons); got != want {
				t.Errorf("the tick logged %q, and the print says %q: the two are one decision", got, want)
			}
		})
	}
}

// TestRunsWatchAsksTheHostAboutTheRunsOwnRepository: the repository the print
// asks the forge about comes from the slug the run was found under and this
// machine's own resolution of it — the launcher's task and this machine's
// config — and never from the record the job wrote, so a job cannot point the
// read at a repository the run never touched (F24, F30). The daemon's own pin of
// the same rule is TestTriageAsksTheHostAboutTheRunsOwnRepository, and this is
// its twin for the print: the three cases are the ones a hostile or merely
// wrong record can make.
//
// Mutation (run, restore): resolve the repository out of rec.Repo instead of
// followUpsRepo over the task and the second and third cases ask a host about
// "attacker/other", which is the whole of what this test exists to refuse.
func TestRunsWatchAsksTheHostAboutTheRunsOwnRepository(t *testing.T) {
	for _, tc := range []struct {
		name string
		// taskRepo is what the launcher wrote into the run's own storage and
		// recordRepo what the job wrote into the record. The record is the
		// job's own file, which is why it is the one that may name anything.
		taskRepo, recordRepo string
	}{
		{name: "the task its launcher wrote", taskRepo: "acme/app", recordRepo: "acme/app"},
		{name: "a record naming another repository", taskRepo: "acme/app", recordRepo: "attacker/other"},
		{name: "a task naming another repository", taskRepo: "attacker/other", recordRepo: "attacker/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t, green())
			f.writeTaskRepo(t, tc.taskRepo)
			rec := triageRecord()
			rec.Repo = tc.recordRepo
			f.putRecord(t, rec)
			// The findings the run left, so the read is whole and the outcome is
			// the one the run's own host's green checks give it: a print that
			// failed closed on an unreadable input would say less about which
			// repository it asked.
			f.findings(t)
			asked := installWatchProviderAsking(t, f.prov)

			doc := watchJSON(t)
			if len(*asked) != 1 || (*asked)[0] != "acme/app" {
				t.Fatalf("the print asked a host about %v, want the run's own repository", *asked)
			}
			if len(doc.Runs) != 1 {
				t.Fatalf("the document judges %d runs, want the one the bucket holds: %+v", len(doc.Runs), doc.Runs)
			}
			row := doc.Runs[0]
			if row.Outcome != string(orchestrator.MergeAsIs) {
				t.Errorf("the print says the run is %q, want the merge the run's own host's green checks and its clear findings give it:\n%+v", row.Outcome, row)
			}
			if !slices.Contains(row.Reasons, string(orchestrator.ReasonCIGreen)) {
				t.Errorf("the codes behind %q are %v, and the checks the run's own host reports at its head are not among them", row.Outcome, row.Reasons)
			}
		})
	}
}

// TestRunsWatchHasNoFlagThatActs: the command carries the window, the project
// and the document, and nothing that acts on a run. The set is asserted whole
// rather than as a denylist, so a flag added later is a decision someone writes
// down here, and the words that would make one act are named separately so the
// failure says why.
//
// Mutation (run, restore): add `--merge` or `--yes` to the command and this
// fails twice over — the set is no longer the read flags, and the flag is one
// of the acting words.
func TestRunsWatchHasNoFlagThatActs(t *testing.T) {
	read := []string{"all", "batch", "config", "gcp-project", "json", "mine", "project", "region", "repo", "since"}
	acts := []string{"apply", "cancel", "fix", "force", "merge", "restart", "write", "yes"}
	var got []string
	newRunsWatchCmd().Flags().VisitAll(func(f *pflag.Flag) { got = append(got, f.Name) })
	slices.Sort(got)
	for _, name := range got {
		if slices.Contains(acts, name) {
			t.Errorf("`fugaro runs watch --%s`: the triage print reads and changes nothing, so it has no flag that acts", name)
		}
	}
	if !slices.Equal(got, read) {
		t.Errorf("`fugaro runs watch` carries the flags %v, want exactly the read flags %v", got, read)
	}
}

// TestRunsWatchIsRegisteredOnce: `fugaro runs watch` is a command a person can
// type, once. The name is registered in the runs family's one AddCommand list,
// and the tree the binary builds resolves the path to that command.
func TestRunsWatchIsRegisteredOnce(t *testing.T) {
	if _, ok := censusCommands(t)["runs watch"]; !ok {
		t.Fatalf("the tree has no visible runnable `fugaro runs watch`: the census of the command tree is what every reader of it walks")
	}
	seen := 0
	for _, c := range newRunsCmd().Commands() {
		if c.Name() == "watch" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("`fugaro runs` registers watch %d times, want once", seen)
	}
	found, _, err := NewRootCmd().Find([]string{"runs", "watch"})
	if err != nil {
		t.Fatalf("fugaro runs watch: %v", err)
	}
	if found.Name() != "watch" || found.Parent().Name() != "runs" {
		t.Errorf("`fugaro runs watch` resolves to %q under %q", found.Name(), found.Parent().Name())
	}
}

// TestRunsWatchNamesTheUnreadableInput: an input the rule cannot read is the
// rule's own fail-closed answer — recommend-only, with the input named — and
// never an error exit. Each case breaks one reading and pins the name the
// print gives it.
func TestRunsWatchNamesTheUnreadableInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		// checks are what the host reports at the run's head: nil is a commit
		// it reports nothing about, which is not green.
		checks []gitprov.CheckRun
		seed   func(t *testing.T, f *triageFixture)
	}{
		{
			name: "the findings artifact is not there", input: "findings",
			checks: green(),
		},
		{
			name: "the host reports nothing at the head", input: "ci",
			seed: func(t *testing.T, f *triageFixture) { f.findings(t) },
		},
		{
			name: "the record's status is one nothing here can place", input: "record",
			checks: green(),
			seed: func(t *testing.T, f *triageFixture) {
				f.findings(t)
				rec := triageRecord()
				rec.Status = runstore.Status("flying")
				f.putRecord(t, rec)
			},
		},
		{
			name: "the record carries no changed path", input: "changed paths",
			checks: green(),
			seed: func(t *testing.T, f *triageFixture) {
				f.findings(t)
				rec := triageRecord()
				rec.ChangedFiles = nil
				f.putRecord(t, rec)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t, tc.checks)
			if tc.seed != nil {
				tc.seed(t, f)
			}
			doc := watchJSON(t)
			if len(doc.Runs) != 1 {
				t.Fatalf("the document judges %d runs, want one: %+v", len(doc.Runs), doc.Runs)
			}
			row := doc.Runs[0]
			if row.Outcome != string(orchestrator.RecommendOnly) {
				t.Errorf("the print says the run is %q, and an input nobody read is the rule's recommend-only", row.Outcome)
			}
			if row.Unreadable != tc.input {
				t.Errorf("the print names %q as the input it could not read, want %q", row.Unreadable, tc.input)
			}
			if len(row.Reasons) != 1 || !orchestrator.KnownTriageReason(orchestrator.TriageReason(row.Reasons[0])) {
				t.Errorf("the print gives the codes %v, want one code of the rule's closed vocabulary", row.Reasons)
			}
			// And the same reading as a person sees it, with no error exit.
			out, errOut, err := execute(t, "runs", "watch")
			if err != nil {
				t.Fatalf("runs watch over an input it cannot read: %v (%s)", err, errOut)
			}
			if line := watchRowLine(t, out, appSlug, triageRunID); !strings.Contains(line, tc.input) {
				t.Errorf("the row %q does not name %q as the unreadable input:\n%s", line, tc.input, out)
			}
		})
	}
}

// TestRunsWatchJSONShape: the document is a contract (G2), so its members are
// pinned as a set: the version it carries, the project and the threshold it
// judged against, the window's rows and the lines its reads warned about.
func TestRunsWatchJSONShape(t *testing.T) {
	f := newWatchFixture(t, green())
	_ = f
	docKeys, rowKeys := watchJSONKeys(t)
	if want := []string{"project", "runs", "threshold_seconds", "version", "warnings"}; !slices.Equal(docKeys, want) {
		t.Errorf("the document's keys are %v, want %v", docKeys, want)
	}
	if want := []string{"age_seconds", "outcome", "reasons", "repo", "run", "unreadable", "without_a_person"}; !slices.Equal(rowKeys, want) {
		t.Errorf("a row's keys are %v, want %v", rowKeys, want)
	}
	doc := watchJSON(t)
	if doc.Version != runsWatchDocVersion {
		t.Errorf("the document's version is %d, want %d", doc.Version, runsWatchDocVersion)
	}
	if doc.Project != orchestratorTestID {
		t.Errorf("the document names the project %q, want %q", doc.Project, orchestratorTestID)
	}
	if want := int64(orchestrator.TriageAfter(rigor.Default) / time.Second); doc.ThresholdSeconds != want {
		t.Errorf("the document judges against %ds, want the ladder's own ceiling for the default mode (%ds)", doc.ThresholdSeconds, want)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("the document judges %d runs, want one: %+v", len(doc.Runs), doc.Runs)
	}
	row := doc.Runs[0]
	if row.Run != appSlug+"/"+triageRunID {
		t.Errorf("the row is %q, want %s/%s", row.Run, appSlug, triageRunID)
	}
	// The fixture's task is the minimal one the daemon's own fixture writes,
	// which no reader can call a task: `repo` is then the empty string, the
	// meaning every runs document gives the field. The table still names a
	// repository, which is repoCell's fallback and is pinned below.
	if row.Repo != "" {
		t.Errorf("the row names the repository %q, and this run's task names none", row.Repo)
	}
	out, _, err := execute(t, "runs", "watch")
	if err != nil {
		t.Fatalf("runs watch: %v", err)
	}
	if line := watchRowLine(t, out, appSlug, triageRunID); !strings.Contains(line, appSlug) {
		t.Errorf("the row %q names no repository, and the slug the run was found under is the only one there is:\n%s", line, out)
	}
	if want := int64(triageAge / time.Second); row.AgeSeconds != want {
		t.Errorf("the row is %ds old, want %ds: the run's own mint time is the age a reader outside the daemon has", row.AgeSeconds, want)
	}
	if row.Outcome != string(orchestrator.RecommendOnly) || !slices.Equal(row.Reasons, []string{string(orchestrator.ReasonFindingsAbsent)}) {
		t.Errorf("the row reads %q %v, want recommend-only [%s]", row.Outcome, row.Reasons, orchestrator.ReasonFindingsAbsent)
	}
	if row.Unreadable != string(orchestrator.InputFindings) || row.WithoutAPerson {
		t.Errorf("the row names %q as unreadable and without_a_person %v, want findings and false", row.Unreadable, row.WithoutAPerson)
	}
}

// TestRunsWatchPrintsARunUnderTheThresholdAsNotDue: the rule decides nothing
// about a run that has not reached the threshold, and the zero outcome is the
// empty string — a document with no outcome in it would read as a field
// nobody wrote. The print says not-due, which is this print's own word and the
// only one of the five that is not the rule's.
func TestRunsWatchPrintsARunUnderTheThresholdAsNotDue(t *testing.T) {
	// The run is minted at its own timestamp and the clock is an hour past it,
	// so the age the print computes is an hour against the ladder's twelve.
	at, err := runstore.RunTime(triageRunID)
	if err != nil {
		t.Fatal(err)
	}
	watchNowAt(t, at.Add(time.Hour))
	f := newTriageFixture(t, green())
	installWatchProvider(t, f.prov)

	doc := watchJSON(t)
	if len(doc.Runs) != 1 {
		t.Fatalf("the document judges %d runs, want the window's one: %+v", len(doc.Runs), doc.Runs)
	}
	row := doc.Runs[0]
	if row.Outcome != watchNotDue {
		t.Errorf("a run an hour old is printed as %q, want %q", row.Outcome, watchNotDue)
	}
	if len(row.Reasons) != 0 || row.Unreadable != "" || row.WithoutAPerson {
		t.Errorf("the row is %+v, and a run the rule did not read carries no code, no unreadable input and no outcome without a person", row)
	}
	out, errOut, err := execute(t, "runs", "watch")
	if err != nil {
		t.Fatalf("runs watch: %v (%s)", err, errOut)
	}
	if line := watchRowLine(t, out, appSlug, triageRunID); !strings.Contains(line, watchNotDue) {
		t.Errorf("the row %q does not say the run is not due:\n%s", line, out)
	}
}

// TestRunsWatchSaysANoRunWindowPlainly: a window nothing ran in is said in one
// line rather than printed as an empty table a reader could take for a
// failure, and the document's rows are then empty rather than null.
func TestRunsWatchSaysANoRunWindowPlainly(t *testing.T) {
	f := newWatchFixture(t, green())
	doc := watchJSON(t, "--since", "1m")
	if len(doc.Runs) != 0 {
		t.Fatalf("a one-minute window judged %d runs: %+v", len(doc.Runs), doc.Runs)
	}
	if doc.Runs == nil {
		t.Error("the document's rows are null, and a consumer reads them without a nil check")
	}
	out, errOut, err := execute(t, "runs", "watch", "--since", "1m")
	if err != nil {
		t.Fatalf("runs watch: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "no run in this window") {
		t.Errorf("the print does not say the window holds no run:\n%s", out)
	}
	_ = f
}
