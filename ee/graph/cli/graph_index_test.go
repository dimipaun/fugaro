// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// IR3 of docs/console-fixes/implementation-plan.md: the coordinator scope's
// runs-half fallback reads the run index.
//
//   - TestGraphFallbackReadsIndex is F3. `fugaro graph` reads the coordinator's
//     stored view out of the project's database, and a repository the sweep has
//     never reconciled is answered with the runs alone. That half used to be
//     `coordinatorRuns`: a listing of the repository's runs over the window plus
//     about three object reads for each of them, with no cache — and the
//     coordinator's sweep is off on most installations, so it is the common case
//     (F3). It is now one List of C-INDEX's Reader, whose cost does not grow
//     with the repository's history. The test holds the two things that makes
//     true: the run the document draws is the index's row and not the bucket's
//     copy of another run, and the index was asked once, for this repository,
//     over the read's own window.
//   - This file is where a fake index lives for this package's tests. The real
//     reader is RI3's and I-INDEX wires it in; until then the command reads
//     C-INDEX's stub, so `indexing` is how a test says what the index holds and
//     what it was asked.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// indexedRunID is the run the index's own row names in these tests, and it is
// deliberately not the id `seedRun` mints: which of the two runs a document
// draws is the whole question IR3 answers.
const indexedRunID = "20261013-100000-aaaa"

// fakeIndex is a runindex.Reader over entries a test hands it. It answers the
// page a filter asks for — this repository's runs at or after the filter's
// window, newest first, which is the contract's own order — with the summary's
// own completeness beside it, and keeps every question it was asked, so a test
// can hold both what the fallback drew and that the index is where it read it.
type fakeIndex struct {
	entries []runindex.Entry
	filters []runindex.Filter
	// complete and folded are the summary's two fields as the page carries
	// them. The zero value is an index that has folded nothing, which is
	// exactly the state the read must never answer as a repository with no
	// runs, so `indexing` installs a finished walk and a test that wants the
	// other state clears it.
	complete bool
	folded   *time.Time
}

func (f *fakeIndex) List(_ context.Context, filter runindex.Filter) (runindex.Page, error) {
	f.filters = append(f.filters, filter)
	slugs := make(map[string]bool, len(filter.Slugs))
	for _, slug := range filter.Slugs {
		slugs[slug] = true
	}
	page := runindex.Page{Entries: []runindex.Entry{}, IndexComplete: f.complete, FoldedThrough: f.folded}
	for _, e := range f.entries {
		if len(slugs) > 0 && !slugs[e.Slug] {
			continue
		}
		if !filter.Since.IsZero() && e.UpdatedAt.Before(filter.Since) {
			continue
		}
		page.Entries = append(page.Entries, e)
	}
	slices.SortStableFunc(page.Entries, func(a, b runindex.Entry) int {
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return b.UpdatedAt.Compare(a.UpdatedAt)
		}
		return strings.Compare(a.RunID, b.RunID)
	})
	return page, nil
}

func (f *fakeIndex) Summary(context.Context) (runindex.Summary, error) {
	return runindex.Summary{Version: runindex.Version, States: map[string]int64{}}, nil
}

// indexing installs a fake index holding entries as what the coordinator
// scope's runs-half fallback reads, and answers it: the questions the read
// asked are index.filters.
//
// The index it installs has finished a walk, which is the steady state and the
// state every other test in this package is about; a test that wants an index
// still building clears index.complete (and index.folded with it) before the
// command runs.
func (f *graphFixture) indexing(t *testing.T, entries ...runindex.Entry) *fakeIndex {
	t.Helper()
	folded := fcli.LsNow().UTC().Add(-time.Minute)
	index := &fakeIndex{entries: entries, complete: true, folded: &folded}
	previous := graphIndex
	graphIndex = func(context.Context, *fcli.Cloud) (runindex.Reader, error) { return index, nil }
	t.Cleanup(func() { graphIndex = previous })
	return index
}

// indexEntry is one run as the index holds it, for a fake to answer with: a run
// in flight, on no pull request, with the launch record its launcher is read
// from and the record its branch, status and window are read from. Those are
// the fields runview.Input and coordinatorRun take a run's objects apart for,
// and the index carries the same two records whole.
func indexEntry(id string, at time.Time) runindex.Entry {
	return runindex.Entry{
		RunID: id, Slug: appSlug, Repo: "acme/app", UpdatedAt: at,
		Status: runstore.StatusRunning,
		Launch: &runstore.Launch{Version: 1, RunID: id, LaunchedBy: "someone@example.com"},
		Record: &runstore.Record{
			Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
			Status: runstore.StatusRunning, Branch: "fugaro/" + id, BaseBranch: "main",
		},
	}
}

// TestGraphFallbackReadsIndex is IR3, F3. A repository the sweep has never
// reconciled is the runs half, and that half is read from the run index rather
// than from the runs bucket: the object read it replaces is a listing of the
// repository's window plus a read per run, which is what the graph timed out
// on at the project F3 measured.
func TestGraphFallbackReadsIndex(t *testing.T) {
	f := newGraphFixture(t)
	// A run the bucket holds and the index does not: what the fallback drew
	// before this task, and what a listing would draw again.
	bucketOnly := f.seedRun(t)
	// The index's own row, for the same repository.
	index := f.indexing(t, indexEntry(indexedRunID, fcli.LsNow().UTC().Add(-time.Hour)))

	doc := graphDocument(t, "graph", "--json")

	if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceDown {
		t.Fatalf("a repository with no stored view is %+v, want the scope down with its runs", doc.Sources)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("the runs half drew %d nodes, want the index's one: %+v", len(doc.Nodes), doc.Nodes)
	}
	n := doc.Nodes[0]
	if n.ID != "acme/app#"+indexedRunID {
		t.Errorf("the runs half drew %q, want the index's run acme/app#%s: acme/app#%s is the run only the bucket holds, so the fallback listed the bucket",
			n.ID, indexedRunID, bucketOnly)
	}
	if n.Kind != graph.NodeRun || n.State != string(runstore.StatusRunning) {
		t.Errorf("the index's run is drawn as %+v, want the in-flight run node the bucket's own objects drew", n)
	}
	// A finished index says nothing about itself: the runs half it answered with
	// is the whole truth it can be, and a warning here would be a line every
	// document carries for nothing.
	if len(doc.Warnings) != 0 {
		t.Errorf("a finished index warned %q, want none: the runs half is the whole truth it can be", doc.Warnings)
	}

	// One List, for this repository, over the read's own window: the fallback
	// read the index, and not something that merely agreed with it.
	window, err := fcli.ParseSince(fcli.LsDefaultSince())
	if err != nil {
		t.Fatal(err)
	}
	want := fcli.LsSince(window)
	if len(index.filters) != 1 {
		t.Fatalf("the fallback asked the index %d times, want one read per repository: %+v", len(index.filters), index.filters)
	}
	asked := index.filters[0]
	if len(asked.Slugs) != 1 || asked.Slugs[0] != appSlug {
		t.Errorf("the index was asked for slugs %v, want the repository the view is read for, %s", asked.Slugs, appSlug)
	}
	// The read's window is measured back from the clock when it was made, and
	// this is a second later, so the two are compared at the resolution the
	// window has: what is held is that the filter carries the listing's own
	// default span and not another one.
	if d := asked.Since.Sub(want); d.Abs() > time.Minute {
		t.Errorf("the index was asked since %v, want the read's own window %v", asked.Since, want)
	}
	if asked.Limit != 0 || asked.After != "" || asked.States != nil || asked.Active {
		t.Errorf("the index was asked %+v, want the whole window: the bucket read it replaces listed every run of it", asked)
	}

	// The one fact the index carries that a run's own two records do not: the
	// pull request the run's *task* names, which is what files a run under its
	// pull request in the view rather than in the in-flight set.
	t.Run("a run the index files under a pull request", func(t *testing.T) {
		f := newGraphFixture(t)
		entry := indexEntry(indexedRunID, fcli.LsNow().UTC().Add(-time.Hour))
		entry.PR = 12
		f.indexing(t, entry)

		doc := graphDocument(t, "graph", "--json")
		if len(doc.Nodes) != 1 {
			t.Fatalf("the runs half drew %d nodes, want the one record: %+v", len(doc.Nodes), doc.Nodes)
		}
		n := doc.Nodes[0]
		if n.Kind != graph.NodePullRequest || n.ID != "acme/app#12" {
			t.Errorf("the run is drawn as %+v, want the pull request its task names, acme/app#12", n)
		}
		// The runs half knows no host state for a pull request, so the newest
		// run's own status stands in for it — the same rule the bucket read's
		// rows drew (coordinator.Join).
		if n.State != string(runstore.StatusRunning) {
			t.Errorf("the pull request's state is %q, want the run's own status: the runs half read no host", n.State)
		}
	})
}

// TestGraphSaysWhenTheIndexIsStillBuilding is the other half of IR3: the read
// answers a page short of the index's own completeness, and it says so rather
// than drawing an empty repository.
//
// The state is not an edge case. IndexComplete is false from the first read
// until a walk finishes — the wired stub says it, and after I-INDEX every
// installation says it until its first reindex does (RI4, RI5) — and the
// fallback is the common case, because the coordinator's sweep is off on most
// installations (F3). Before this, `fugaro graph` drew zero nodes for such a
// repository and nothing in the document said why, which the plan's own
// constraint forbids (a reader never prints a silently short list while the
// index is incomplete) and which the contract names as the stub's whole point.
// Two things are held here: the document carries the sentence, and the sentence
// carries the summary's other field, so a reader can tell "folded nothing yet"
// from "folded through this morning".
func TestGraphSaysWhenTheIndexIsStillBuilding(t *testing.T) {
	f := newGraphFixture(t)
	// A run the bucket holds and the index does not: the runs a bucket listing
	// would draw, and what the empty page must not be read as.
	f.seedRun(t)
	index := f.indexing(t)
	index.complete, index.folded = false, nil

	out, errOut, err := execute(t, "graph", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	doc, err := graph.ParseDocument([]byte(out))
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	if len(doc.Nodes) != 0 {
		t.Errorf("an index that has folded nothing drew %d nodes: %+v", len(doc.Nodes), doc.Nodes)
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("the document's warnings are %q, want the one sentence about the index", doc.Warnings)
	}
	line := doc.Warnings[0]
	if !strings.Contains(line, "the run index is still building") || !strings.Contains(line, "nothing is folded yet") {
		t.Errorf("the warning is %q, want it to say the index is still building and that nothing is folded yet", line)
	}
	// The table has no field for what the read could not do, so the text form
	// carries the sentence on stderr instead of in the document (runGraph): a
	// person reading the empty table is not left to infer why it is empty. A
	// second read, because --json echoes nothing to stderr but the header.
	_, textErr, err := execute(t, "graph")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, textErr)
	}
	if !strings.Contains(textErr, "the run index is still building") {
		t.Errorf("the table's stderr is %q, want the sentence the document carries: a reader of the table is not left to infer it", textErr)
	}

	// A walk in progress: the page holds what it has folded so far, and the
	// sentence names how fresh those rows are, so "the runs of this window" and
	// "the runs folded by 09:14" do not look alike.
	t.Run("a page short of a walk in progress", func(t *testing.T) {
		f := newGraphFixture(t)
		f.seedRun(t)
		index := f.indexing(t, indexEntry(indexedRunID, fcli.LsNow().UTC().Add(-time.Hour)))
		index.complete = false

		doc := graphDocument(t, "graph", "--json")
		if len(doc.Nodes) != 1 || doc.Nodes[0].ID != "acme/app#"+indexedRunID {
			t.Fatalf("the half-folded index drew %+v, want the one row it has folded", doc.Nodes)
		}
		if len(doc.Warnings) != 1 {
			t.Fatalf("the document's warnings are %q, want the one sentence about the index", doc.Warnings)
		}
		want := "folded through " + index.folded.UTC().Format(time.RFC3339)
		if !strings.Contains(doc.Warnings[0], want) {
			t.Errorf("the warning is %q, want it to name when the index folded through (%s)", doc.Warnings[0], want)
		}
	})
}
