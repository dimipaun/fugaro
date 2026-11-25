package cli

// indexed_reads_e2e_test.go is I-INDEX (D3205): the two measurements the task
// is accepted on, taken through the seam the commands really read (the wired
// runindex.Reader, runindex_wire.go) and not through a fake one.
//
// TestIndexedReadsConstantRequests is F10 at the size F1 measured. Two
// installations are built, identical in every way but one: the second's run
// index carries a hundred times the entries -- 3,500 runs against 350,000 --
// and the fakes both reads go through count what they serve. `runs ls` and
// `plan status` are then run against each, and the two costs are compared: the
// requests the installation's Firestore and its budget database served, the
// object reads and the listings its runs bucket served, and the runs the
// document named.
//
// What it catches is a read that walks history. The index is queried by
// equality alone (RI1), so a page is a fixed number of requests whatever the
// index holds, and a page query that paged it instead would spend 1,167
// requests at 350,000 entries where it spends 12 at 3,500. The bucket's two
// counters catch the other direction, a read that went back to the window: the
// index answers with the runs to read -- one object read each (D3205) -- while
// a window listing lists the repository's own objects and reads theirs, so a
// bucket listing where the index path makes none is the failure, and the read
// counts differ by orders of magnitude.
//
// TestFixedInvocationCost is U1: what a command pays before it has read the
// index at all -- the configuration, the clients, the database's own metadata
// -- named as the list of requests it is, at both history sizes. That half of a
// read is the one no index can shorten, so it is the half that has to be
// watched by hand: a release that adds a request in front of the index read
// adds it to every command, and this test is where it is seen.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The histories every read here is measured at: the runs F1 measured the
// project at, and a hundred times as many entries in the same installation's
// index (F10).
const (
	indexedHistoryRuns = 3500
	indexedPageRuns    = 51 // the newest lsIndexPage plus the one that says a page has more
	indexedRunSpans    = time.Hour
)

// indexedHistory is the two index sizes, in the order the costs are compared.
var indexedHistory = []int{indexedHistoryRuns, indexedHistoryRuns * 100}

// indexedRead is one command this file measures, and how the runs its document
// names are read out of it: the two rigs are held to having *answered* the same
// as well as to having cost the same, because a request count is only evidence
// of a read if the read is the one that was asked for.
type indexedRead struct {
	name   string
	args   []string
	runIDs func(t *testing.T, out string) []string
}

var indexedReads = []indexedRead{
	{
		name: "runs ls",
		args: []string{"runs", "ls", "--json"},
		runIDs: func(t *testing.T, out string) []string {
			t.Helper()
			var doc struct {
				Runs []struct {
					RunID string `json:"run_id"`
				} `json:"runs"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("runs ls --json: %v: %s", err, out)
			}
			ids := make([]string, 0, len(doc.Runs))
			for _, r := range doc.Runs {
				ids = append(ids, r.RunID)
			}
			return ids
		},
	},
	{
		// --no-merges is the console's first paint (D3217 as amended): it is
		// the read that spawns no gh, so what it costs is the index's own cost
		// and not a host's, and it is the one a timing can be held to.
		name: "plan status",
		args: []string{"plan", "status", "--json", "--no-merges"},
		runIDs: func(t *testing.T, out string) []string {
			t.Helper()
			var doc struct {
				Plans []struct {
					Nodes []struct {
						ID  string `json:"id"`
						Run string `json:"run"`
					} `json:"nodes"`
				} `json:"plans"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("plan status --json: %v: %s", err, out)
			}
			var ids []string
			for _, p := range doc.Plans {
				for _, n := range p.Nodes {
					if n.Run != "" {
						ids = append(ids, n.ID+"="+n.Run)
					}
				}
			}
			return ids
		},
	},
}

// indexedCost is what one command cost the installation it ran against.
type indexedCost struct {
	// requests are every request the installation's fakes served: its Firestore
	// and its budget database, which is where the index and its overlay live,
	// and the execution, logging and IAM endpoints a command reaches for its
	// own credentials. No runs bucket is among them: it is a driver
	// (indexedRig.b) and its own counters are below.
	requests int
	// index are those requests that named the run index -- a document of
	// runIndex or runIndexMeta -- which is what the red step turns on: with the
	// stub wired, no request names the index at all.
	index int
	// reads and listings are what the runs bucket served: the objects a read
	// reached, and the listings it asked for. A page of the index costs one
	// read per run it names and no listing (D3205).
	reads, listings int
	// runIDs are the runs the command's document named.
	runIDs []string
}

// indexedCounts is the raw counters, taken before and after one command so the
// cost is the difference and never the fixture's own setup.
type indexedCounts struct {
	fs, rtdb, run, logging, iam int
	reads, listings             int
}

// indexedRig is one installation to measure a read against: its fakes, the runs
// bucket it reads through (whose counts are its own), and the checkout
// `plan status` reads its plans from.
type indexedRig struct {
	f   *cloudFixture
	fs  *gcpfake.Firestore
	b   *genDriver
	dir string
}

// newIndexedRig builds the installation: n entries in its run index -- the
// page's own runs, newest first, and the rest of history behind them -- the
// page's runs in its runs bucket, and a checkout of acme/app carrying the plan
// the read joins against.
//
// The bucket holds the page's runs and nothing else, in both rigs: the size
// under test is the index's, which is what F10 is about ("a page costs the same
// at one run of history and at a hundred thousand"), and the bucket's history
// is what the index replaces. What a read spends on the bucket is held by the
// two counts beside it: a page reads the runs the index named, and a read that
// listed the bucket instead is a listing no page of the index makes.
func newIndexedRig(t *testing.T, at time.Time, n int) *indexedRig {
	t.Helper()
	// Before any git runs: CI has no identity of its own.
	testutil.IsolateGit(t)
	f := newCloudFixture(t)
	fs := gcpfake.NewFirestore(t)
	// The installation's own Firestore, and the Firebase project its index
	// lives in (budget.firebase_project): what every read of the index is
	// opened from (openRunIndex, runindex_wire.go). The installation is a
	// no-auth one -- the fakes are its cloud -- so the database is named at
	// endpoints.firestore.
	rewriteConfig(t, "endpoints: { ", "endpoints: { firestore: "+fs.URL+"/, ")
	rewriteConfig(t, "budget: { ", "budget: { firebase_project: proj-1234, ")

	r := &indexedRig{f: f, fs: fs, dir: newIndexedCheckout(t)}
	for i := range indexedPageRuns {
		r.seedRun(t, at, i)
	}
	r.seedHistory(t, at, n)
	// The summary is the same document in both rigs but for the runs it counts:
	// it is what a page's figure and the estimate table come from, and its own
	// size is one document whatever history is behind it (RI4).
	fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, indexSummaryDoc(t, at, n))

	r.b = genObjectsOf(t, f)
	installGenBucket(t, f, r.b)
	return r
}

// indexedRunID is the id of the i-th run of the page these rigs are measured
// on, minted from the pinned clock so both rigs hold the same runs at the same
// moments: the page is the same page at 1x and 100x, which is what makes the
// two costs comparable at all.
func indexedRunID(at time.Time, i int) string {
	// One hour apart, newest first, and never the same hour twice: a page is
	// ordered by updated_at, and two runs sharing one would hand the order to
	// the run id.
	return mintRunID(func() time.Time { return at }, time.Duration(i+1)*indexedRunSpans, fmt.Sprintf("%04x", i))
}

// seedRun seeds the i-th run of the page: its own objects in the runs bucket
// (task, launch, result) and its document in the index, which is what the fold
// would have written from them. The three oldest page runs claim the checkout's
// plan tasks, so the plan status read joins runs and not an empty plan; the two
// newest are going, which is the half of the default listing the newest-N alone
// would drop (D3203).
func (r *indexedRig) seedRun(t *testing.T, at time.Time, i int) {
	t.Helper()
	id := indexedRunID(at, i)
	status, planTask := runstore.StatusSucceeded, ""
	switch {
	case i < 2:
		status = runstore.StatusRunning
	case i < 5:
		planTask = fmt.Sprintf("plan/%c", 'A'+i-2)
	}
	at = at.Add(-time.Duration(i+1) * indexedRunSpans)
	going := status == runstore.StatusRunning
	if exec := seedSpec(t, r.f, firstRunSpec(id), going); going {
		r.f.run.SetState(exec, backend.StateRunning)
	}
	finished := at.Add(time.Minute)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: status, StartedAt: at, FinishedAt: &finished}
	if going {
		rec.FinishedAt = nil
	}
	writeRecord(t, r.f, id, rec)
	r.fs.Set(runindex.EntryCollection, id, indexEntryDoc(t, runindex.Entry{
		RunID: id, Slug: appSlug, Repo: "acme/app", UpdatedAt: at,
		Status: status, PlanTask: planTask, Workflow: "web",
		Launch: &runstore.Launch{Version: 1, RunID: id, LaunchedAt: at, PlanTask: planTask},
		Record: rec,
	}))
}

// seedHistory writes the rest of the index's entries: n minus the page, all of
// them older than the page's oldest run, so the page is the page at both sizes.
// They are what history is -- a document each, no record and no bucket object
// anywhere -- and where the page's runs span hours, these span days.
func (r *indexedRig) seedHistory(t *testing.T, at time.Time, n int) {
	t.Helper()
	oldest := at.Add(-time.Duration(indexedPageRuns) * indexedRunSpans)
	for i := range n - indexedPageRuns {
		id := mintRunID(func() time.Time { return oldest }, time.Duration(i)*20*time.Second, fmt.Sprintf("%04x", i&0xffff))
		r.fs.Set(runindex.EntryCollection, id, map[string]any{
			"slug": appSlug, "updated_at": oldest.Add(-time.Duration(i) * 20 * time.Second),
		})
	}
}

// newIndexedCheckout is a checkout of the fixture's own repository carrying one
// plan artifact and the streams catalogue, read the way planStatusRig builds
// one: the read joins the runs of the checkout's own plans.
func newIndexedCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	writeCheckoutFile(t, dir, "docs/plan.graph.json", statusGraph)
	writeCheckoutFile(t, dir, "docs/release-streams.yaml", statusStreams)
	return dir
}

// indexEntryDoc is one entry as the fold writes it: the entry's own fields
// under the names its struct declares (decodeDoc reads a document back through
// them), as the Go values the Firestore client encodes.
func indexEntryDoc(t *testing.T, e runindex.Entry) map[string]any {
	t.Helper()
	var doc map[string]any
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return plainDocFields(t, doc)
}

// indexSummaryDoc is the installation's one summary document (contract-index.md
// and runindex.Summary), with the estimate table a plan status read draws its
// history from: built from the page's own records, so the two rigs carry the
// same table and it is the real builder that made it (F13, F15).
func indexSummaryDoc(t *testing.T, at time.Time, n int) map[string]any {
	t.Helper()
	records := make([]runstore.Record, 0, estimate.MinRuns)
	for i := range estimate.MinRuns {
		rec := *historyRecord(tier.Small, 30, 2)
		rec.RunID = fmt.Sprintf("history-%d", i)
		records = append(records, rec)
	}
	est, err := json.Marshal(estimate.Build(records, at))
	if err != nil {
		t.Fatal(err)
	}
	var table map[string]any
	if err := json.Unmarshal(est, &table); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"version": runindex.Version, "complete": true, "folded_through": at,
		"updated_at": at, "runs": int64(n),
		"states":          map[string]any{"succeeded": int64(n - 2), "running": int64(2)},
		"spent_micros":    int64(0),
		"notional_micros": int64(0),
		"compute_runs":    int64(n),
		"estimate":        plainDocFields(t, table),
	}
}

// plainDocFields is a JSON object as the Firestore client's own Go values: the
// numbers back to the integers the wire carries them as, and the fields a run's
// windows are recorded in back to time.Time, which is what a page orders and
// pages by (docTime). The keys are the ones the contract names, and a field
// that is not one is left as it came.
func plainDocFields(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	for k, v := range doc {
		switch x := v.(type) {
		case json.Number:
			i, err := x.Int64()
			if err != nil {
				t.Fatalf("the document's %s is not an integer: %v", k, err)
			}
			doc[k] = i
		case string:
			if !strings.HasSuffix(k, "_at") {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, x)
			if err != nil {
				t.Fatalf("the document's %s is not a time: %v", k, err)
			}
			doc[k] = at.UTC()
		case map[string]any:
			doc[k] = plainDocFields(t, x)
		case []any:
			for i, e := range x {
				if m, ok := e.(map[string]any); ok {
					x[i] = plainDocFields(t, m)
				}
			}
		}
	}
	return doc
}

// counts is every counter the fakes hold right now.
func (r *indexedRig) counts() indexedCounts {
	return indexedCounts{
		fs: len(r.fs.Requests()), rtdb: len(r.f.budget.DB.Requests()),
		run: len(r.f.run.Requests()), logging: len(r.f.logging.Requests()), iam: len(r.f.iam.Requests()),
		reads: len(r.b.keysRead()), listings: r.b.listingsServed(),
	}
}

// measure runs every read against this installation, from the checkout of its
// own repository, and answers what each one cost.
func (r *indexedRig) measure(t *testing.T) []indexedCost {
	t.Helper()
	t.Chdir(r.dir)
	out := make([]indexedCost, 0, len(indexedReads))
	for _, read := range indexedReads {
		before := r.counts()
		stdout, stderr, err := execute(t, read.args...)
		if err != nil {
			t.Fatalf("%s: %v (%s)", read.name, err, stderr)
		}
		after := r.counts()
		fs := r.fs.Requests()[before.fs:]
		out = append(out, indexedCost{
			requests: (after.fs - before.fs) + (after.rtdb - before.rtdb) + (after.run - before.run) +
				(after.logging - before.logging) + (after.iam - before.iam),
			index:    indexRequests(fs),
			reads:    after.reads - before.reads,
			listings: after.listings - before.listings,
			runIDs:   read.runIDs(t, stdout),
		})
	}
	return out
}

// indexRequests counts the requests that named the run index. A page of the
// index is a structured query (the collection is in the body, not the path) and
// the summary is a document read (the path names it), so both are looked for.
func indexRequests(reqs []gcpfake.Request) int {
	n := 0
	for _, q := range reqs {
		if strings.Contains(q.Path, runindex.SummaryCollection) || strings.Contains(string(q.Body), runindex.EntryCollection) {
			n++
		}
	}
	return n
}

// TestIndexedReadsConstantRequests is I-INDEX's own claim at F10's size: the
// first page of `runs ls` and of `plan status` costs the same number of requests
// at 3,500 runs of history and at 350,000 entries of index, and it is the index
// the two reads asked.
func TestIndexedReadsConstantRequests(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	costs := make([][]indexedCost, len(indexedHistory))
	for i, n := range indexedHistory {
		costs[i] = newIndexedRig(t, at, n).measure(t)
	}
	for j, read := range indexedReads {
		t.Run(read.name, func(t *testing.T) {
			small, big := costs[0][j], costs[1][j]
			t.Logf("%s at %d runs: %d requests, %d of them the index, %d bucket reads, %d listings, %d runs named; at %d: %d requests, %d index, %d reads, %d listings, %d runs",
				read.name, indexedHistory[0], small.requests, small.index, small.reads, small.listings, len(small.runIDs),
				indexedHistory[1], big.requests, big.index, big.reads, big.listings, len(big.runIDs))
			// The red step: with the stub wired, no read reaches the index at
			// all, and every cost below is the window's.
			if small.index == 0 {
				t.Fatalf("%s read no run index: %d requests named it, and the wired reader answers from it (a stub, or a listing that read the window)",
					read.name, small.index)
			}
			if small.index != big.index {
				t.Errorf("%s read the index %d times at %d runs and %d times at %d: the index is queried by equality, so a page is a fixed number of requests (F10)",
					read.name, small.index, indexedHistory[0], big.index, indexedHistory[1])
			}
			if small.requests != big.requests {
				t.Errorf("%s made %d requests at %d runs and %d at %d: a read whose cost tracks history is the read the index exists to remove",
					read.name, small.requests, indexedHistory[0], big.requests, indexedHistory[1])
			}
			if small.reads != big.reads {
				t.Errorf("%s read %d runs-bucket objects at %d runs and %d at %d",
					read.name, small.reads, indexedHistory[0], big.reads, indexedHistory[1])
			}
			// A page of the index names the runs to read and lists nothing
			// (D3205): a window listing is the read this path replaced, and it
			// is the one thing the bucket's counters can still see.
			if small.listings != 0 || big.listings != 0 {
				t.Errorf("%s listed the runs bucket %d and %d times, want none: the index answers with the runs to read, and a window listing is every object of the repository",
					read.name, small.listings, big.listings)
			}
			if len(small.runIDs) == 0 {
				t.Errorf("%s named no run at all, so the request counts above are the cost of an empty answer", read.name)
			}
			if !slices.Equal(small.runIDs, big.runIDs) {
				t.Errorf("%s answered %v at %d runs and %v at %d: the two rigs hold the same page, so the same runs are the same read",
					read.name, small.runIDs, indexedHistory[0], big.runIDs, indexedHistory[1])
			}
		})
	}
}

// indexedFixedRequests is U1's list: what `runs ls` asks before it reads a page
// of the index, in the order it asks. Each is a read of the installation rather
// than of its history -- the job the runs go in, the project's own marks -- so
// none of them grows with the runs behind them. The test fails when one is
// added, changed or dropped: a request that slides in front of the index read
// is a deliberate change to this list, not a quiet one.
var indexedFixedRequests = []string{
	"GET /v2/projects/proj-1234/locations/us-east5/jobs/fugarochk-acme-app-014bd3f49b1a",
	"GET /config/admin/access.json",
	"GET /config/admin/access_migrated.json",
}

// TestFixedInvocationCost is U1: it names every request a command makes before
// it reads the index, at both history sizes, so the part of an invocation no
// index can shorten is watched where it is added rather than where it is felt
// (the plan's Check 870 times the reads; this is the list behind the timing).
//
// The list is the process's own start: the clients a read is opened from, and
// whatever the databases answer before the first index request -- no page of
// the index and no run's objects are in it. It is the same list at 3,500 runs
// and at 350,000: a request in front of the index read that scaled with history
// would be a request per invocation that no page can pay for.
func TestFixedInvocationCost(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	costs := make([][]string, len(indexedHistory))
	for i, n := range indexedHistory {
		rig := newIndexedRig(t, at, n)
		t.Chdir(rig.dir)
		costs[i] = rig.fixedRequests(t)
	}
	small, big := costs[0], costs[1]
	if !slices.Equal(small, big) {
		t.Errorf("the requests before the index read are\n  %v at %d runs\n  %v at %d\nwant the same: the index's size is behind the read, not in front of it",
			small, indexedHistory[0], big, indexedHistory[1])
	}
	if !slices.Equal(small, indexedFixedRequests) {
		t.Errorf("`runs ls` asked\n  %v\nbefore it read the index, want\n  %v",
			small, indexedFixedRequests)
	}
	if len(small) == 0 {
		t.Errorf("no request was made before the index read at all: the fixture is not exercising the invocation, and a timing of it would measure nothing")
	}
	t.Logf("requests before the first index read at %d runs: %v", indexedHistory[0], small)
}

// fixedRequests is every request `runs ls` made before it read the index, in
// the order it made them: the fakes' own logs merged by each call's own
// sequence (gcpfake.Request.Seq), so the list is the invocation's start rather
// than one endpoint's view of it.
func (r *indexedRig) fixedRequests(t *testing.T) []string {
	t.Helper()
	baseline := r.lastSeq()
	if _, stderr, err := execute(t, indexedReads[0].args...); err != nil {
		t.Fatalf("%s: %v (%s)", indexedReads[0].name, err, stderr)
	}
	type call struct {
		seq    int64
		method string
		path   string
		index  bool
	}
	var calls []call
	add := func(fs bool, reqs []gcpfake.Request) {
		for _, q := range reqs {
			if q.Seq <= baseline {
				continue
			}
			calls = append(calls, call{seq: q.Seq, method: q.Method, path: q.Path,
				index: fs && (strings.Contains(q.Path, runindex.SummaryCollection) || strings.Contains(string(q.Body), runindex.EntryCollection))})
		}
	}
	add(true, r.fs.Requests())
	add(false, r.f.budget.DB.Requests())
	add(false, r.f.run.Requests())
	add(false, r.f.logging.Requests())
	add(false, r.f.iam.Requests())
	slices.SortFunc(calls, func(a, b call) int { return int(a.seq - b.seq) })
	var out []string
	for _, c := range calls {
		if c.index {
			break
		}
		out = append(out, c.method+" "+c.path)
	}
	return out
}

// lastSeq is the sequence of the last request any fake had served when this was
// called: everything after it belongs to the command that follows.
func (r *indexedRig) lastSeq() int64 {
	last := int64(0)
	for _, reqs := range [][]gcpfake.Request{r.fs.Requests(), r.f.budget.DB.Requests(), r.f.run.Requests(), r.f.logging.Requests(), r.f.iam.Requests()} {
		for _, q := range reqs {
			last = max(last, q.Seq)
		}
	}
	return last
}
