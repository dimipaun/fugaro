package issuesync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/issuesync"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/tier"
)

// repo is the repository every test below syncs, in the canonical spelling the
// external ids are built from.
const repo = "acme/app"

// at is the database server's reading of the clock every turn below is stamped
// with: one fixed instant, because nothing here reads a clock of its own.
func at() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) }

// backlog is the standing backlog's node as a daemon reads one: the schema
// version, the agent and the role, and every collection the document carries
// empty rather than nil.
func backlog() orchestrator.State {
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           3,
		AgentID:       "orchestrator-a",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{},
		Decisions:     []orchestrator.Decision{},
		Facts:         map[string]orchestrator.Fact{},
		Archived:      []orchestrator.Archived{},
		Intents:       []orchestrator.Intent{},
	}
}

// item is a work item of the backlog as this package reads one: a person's own
// filing — queued, unapproved, with no spec and no external id.
func item(id string) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID:        id,
		Title:     "the work item " + id,
		Repo:      repo,
		Recipe:    recipe.DefaultName,
		RigorTier: string(tier.Small),
		Brief:     "The brief of " + id + ".",
		DependsOn: []string{},
		Phase:     orchestrator.PhaseQueued,
	}
}

// approved is the item with a person's own approval standing over it.
func approved(w orchestrator.WorkItem) orchestrator.WorkItem {
	w.Approved = &orchestrator.Approval{
		By: "owner", At: at(), SpecDigest: orchestrator.SpecDigest(w), Attempts: orchestrator.ApprovalAttempts,
	}
	return w
}

// sync is one repository's turn, or the test's own failure.
func sync(t *testing.T, a issuesync.Adapter, before orchestrator.State, others []orchestrator.State, cfg issuesync.Config) orchestrator.Patch {
	t.Helper()
	p, err := issuesync.Sync(context.Background(), a, issuesync.Nodes{Backlog: before, Others: others, Now: at()}, cfg)
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	return p
}

// apply is the patch through the daemon's own fence, which is the write the
// tick makes: a patch the fence refuses is the test's own failure.
func apply(t *testing.T, before orchestrator.State, p orchestrator.Patch) orchestrator.State {
	t.Helper()
	if len(p.Ops) == 0 {
		return before
	}
	after, err := orchestrator.Apply(before, p, orchestrator.Actor{Class: orchestrator.Daemon, Now: at()})
	if err != nil {
		t.Fatalf("the fence refused the patch: %v", err)
	}
	return after
}

// ingested is the patch applied, once.
func ingested(t *testing.T, a issuesync.Adapter, before orchestrator.State, cfg issuesync.Config) orchestrator.State {
	t.Helper()
	return apply(t, before, sync(t, a, before, nil, cfg))
}

// itemOf is one item of a state, or the test's own failure.
func itemOf(t *testing.T, s orchestrator.State, id string) orchestrator.WorkItem {
	t.Helper()
	for _, w := range s.WorkItems {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("%s is not in the node", id)
	return orchestrator.WorkItem{}
}

// ids is a state's item ids, for a failure message that says what it holds.
func ids(s orchestrator.State) []string {
	out := make([]string, 0, len(s.WorkItems))
	for _, w := range s.WorkItems {
		out = append(out, w.ID)
	}
	return out
}

// TestIngestOpenIssuesOnce is D2720 and D2728: every open issue of the
// repository with no item of its external id becomes a queued, unapproved item
// holding that id — and the turn after it writes nothing, because one issue is
// one item.
func TestIngestOpenIssuesOnce(t *testing.T) {
	fake := issuesync.NewFake(repo).
		Add(issuesync.Issue{Title: "the first issue", Body: "the body of the first"}).
		Add(issuesync.Issue{Title: "the second issue", Body: "the body of the second"})
	before := backlog()

	after := ingested(t, fake, before, issuesync.Config{Repo: repo})
	if len(after.WorkItems) != 2 {
		t.Fatalf("the ingest wrote %d items, want 2: %v", len(after.WorkItems), ids(after))
	}
	first := itemOf(t, after, "gh-acme-app-1")
	if first.External != "github:acme/app#1" {
		t.Fatalf("the first item carries the external id %q", first.External)
	}
	if first.Title != "the first issue" {
		t.Fatalf("the first item's title is %q", first.Title)
	}
	for _, w := range after.WorkItems {
		if w.Phase != orchestrator.PhaseQueued {
			t.Fatalf("%s is %s, and an ingested item is queued", w.ID, w.Phase)
		}
		if w.Approved != nil {
			t.Fatalf("%s carries an approval, and approval is the person's own word (D2717)", w.ID)
		}
		if w.Repo != repo {
			t.Fatalf("%s is about %q", w.ID, w.Repo)
		}
	}
	if w := itemOf(t, after, "gh-acme-app-2"); w.External != "github:acme/app#2" {
		t.Fatalf("the second item carries the external id %q", w.External)
	}

	if p := sync(t, fake, after, nil, issuesync.Config{Repo: repo}); len(p.Ops) != 0 {
		t.Fatalf("the second turn wrote %d ops over a node that already had every item", len(p.Ops))
	}
	if fake.Lists() != 2 {
		t.Fatalf("the adapter answered %d listings, want 2: the second turn's emptiness is the "+
			"dedupe and not a turn that never read", fake.Lists())
	}
}

// TestIngestStoresNoBodyOnlyItsHash is D2720 and F49: the item holds the
// SHA-256 of the issue's body and the body itself appears nowhere in the
// document — not in the brief, not in a note, not anywhere.
//
// Mutation: write the body into the item (into its brief, say) instead of its
// hash and the substring check below fails while every other assertion here
// still passes.
func TestIngestStoresNoBodyOnlyItsHash(t *testing.T) {
	const body = "the body of the issue, which the state never carries"
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{Title: "an issue", Body: body})

	after := ingested(t, fake, backlog(), issuesync.Config{Repo: repo})
	w := itemOf(t, after, "gh-acme-app-1")
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:]); w.BodySHA256 != want {
		t.Fatalf("the item carries the body hash %q, want %q", w.BodySHA256, want)
	}
	raw, err := json.Marshal(after)
	if err != nil {
		t.Fatalf("marshalling the node: %v", err)
	}
	if strings.Contains(string(raw), "never carries") {
		t.Fatalf("the node carries the issue's body: %s", raw)
	}
}

// TestMarkerFromAnotherAuthorIgnored is D2728: the marker counts only on an
// issue the installation's App opened. A person's issue that quotes a marker is
// ingested like any other — so nobody can hide their own issue from the daemon,
// and no item is adopted by a marker a person wrote.
//
// Mutation: read the marker off any author and this issue is skipped, and the
// node is left with no item for it at all.
func TestMarkerFromAnotherAuthorIgnored(t *testing.T) {
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{
		Title: "a person's own issue", Body: issuesync.Marker("wi-1") + "\n\nplease leave this one alone",
		AuthorLogin: "someone", AppSlug: "",
	})
	before := backlog()
	before.WorkItems = []orchestrator.WorkItem{item("wi-1")}

	after := ingested(t, fake, before, issuesync.Config{Repo: repo})
	if len(after.WorkItems) != 2 {
		t.Fatalf("the person's issue came in as %d items, want 2: %v", len(after.WorkItems), ids(after))
	}
	if w := itemOf(t, after, "gh-acme-app-1"); w.External != "github:acme/app#1" {
		t.Fatalf("the person's issue became %q", w.ID)
	}
	if w := itemOf(t, after, "wi-1"); w.External != "" {
		t.Fatalf("wi-1 adopted a person's issue: %q", w.External)
	}
}

// TestFloodLeavesRoomForBacklogAdd is D2720's 20 items of headroom: a flood of
// issues lands as items up to the room the node has and stops there, so the
// work a person files afterwards still fits — in the item count and in the
// node's bytes, which is the cap that binds first — and the stop is recorded
// as one fact.
func TestFloodLeavesRoomForBacklogAdd(t *testing.T) {
	fake := issuesync.NewFake(repo)
	for n := 1; n <= 400; n++ {
		fake.Add(issuesync.Issue{Number: n, Title: fmt.Sprintf("issue %d: %s", n, strings.Repeat("x", 60)), Body: "b"})
	}
	after := ingested(t, fake, backlog(), issuesync.Config{Repo: repo})
	if orchestrator.OverCap(after) {
		t.Fatalf("the flood left the node over its size target")
	}
	if got := len(after.WorkItems); got > orchestrator.MaxWorkItems-20 {
		t.Fatalf("the flood left %d items, and 20 of the %d are kept for a person's own filing",
			got, orchestrator.MaxWorkItems)
	}
	if len(after.WorkItems) == 0 {
		t.Fatalf("the flood ingested nothing at all")
	}

	// The write a person files afterwards: the shape `fugaro backlog add`
	// writes, through the same fence, on the node the flood left.
	title, brief, id := "a person's item", "The brief of the item a person files.", "20261118-01"
	recipeName, tierName := recipe.DefaultName, string(tier.Small)
	_, err := orchestrator.Apply(after, orchestrator.Patch{BaseRev: after.Rev, Ops: []orchestrator.Op{{
		Op: orchestrator.OpWorkItemUpsert,
		WorkItem: &orchestrator.WorkItemOp{
			ID: id, Title: &title, Repo: stringPtr(repo), Recipe: &recipeName, RigorTier: &tierName, Brief: &brief,
		},
	}}}, orchestrator.Actor{Class: orchestrator.Session, Now: at()})
	if err != nil {
		t.Fatalf("the flood left no room for the item a person files: %v", err)
	}

	if len(after.Facts) != 1 {
		t.Fatalf("a stopped ingest left %d facts, want one: %v", len(after.Facts), after.Facts)
	}
	for key, f := range after.Facts {
		if !strings.Contains(f.Value, repo) || !strings.Contains(f.Value, "stopped") {
			t.Fatalf("the fact %q reads %q, and it is about the stopped ingest of %s", key, f.Value, repo)
		}
	}
}

// TestIssueFloodNeverPassesTheCap is the second half of D2720: the patch the
// ingest answers with is a patch the fence takes. A flood never produces a node
// over MaxWorkItems or over MaxStateBytes — the item is left out rather than
// the whole patch refused, which is what would stop every other write to the
// node with it.
func TestIssueFloodNeverPassesTheCap(t *testing.T) {
	fake := issuesync.NewFake(repo)
	for n := 1; n <= 500; n++ {
		fake.Add(issuesync.Issue{Number: n, Title: strings.Repeat("a long title ", 30), Body: strings.Repeat("body ", 400)})
	}
	before := backlog()
	after := apply(t, before, sync(t, fake, before, nil, issuesync.Config{Repo: repo}))
	if orchestrator.OverCap(after) {
		t.Fatalf("the flood left the node over its size target: %d work items", len(after.WorkItems))
	}
	if len(after.WorkItems) > orchestrator.MaxWorkItems {
		t.Fatalf("the flood left %d work items, and a node holds %d", len(after.WorkItems), orchestrator.MaxWorkItems)
	}
	if len(after.WorkItems) == 0 {
		t.Fatalf("the flood ingested nothing at all")
	}
	for _, w := range after.WorkItems {
		if utf8.RuneCountInString(w.Title) > 80 {
			t.Fatalf("%s carries %d runes of title, and an ingested title is at most 80",
				w.ID, utf8.RuneCountInString(w.Title))
		}
	}
}

// TestMirroredIssueIsNotIngestedBack is D2728's other direction: an issue the
// App opened with an item's marker in it is that item's other face and is never
// ingested as an item of its own. This is the case the dedupe key cannot catch
// — the item carries no external id, because the write that records one was
// lost — so the marker and the App's own authorship are the whole of the fence.
func TestMirroredIssueIsNotIngestedBack(t *testing.T) {
	fake := issuesync.NewFake(repo)
	before := backlog()
	before.WorkItems = []orchestrator.WorkItem{approved(item("wi-1"))}

	after := ingested(t, fake, before, issuesync.Config{Repo: repo})
	if len(after.WorkItems) != 1 {
		t.Fatalf("the mirrored issue came back as %d items: %v", len(after.WorkItems), ids(after))
	}
	if w := itemOf(t, after, "wi-1"); w.External != "" {
		t.Fatalf("an approved item adopted an issue: %q", w.External)
	}
	if fake.Opens() != 0 {
		t.Fatalf("the mirror opened %d issues for an approved item, and it mirrors none (D2728)", fake.Opens())
	}
	opened := issuesync.NewFake(repo).Add(issuesync.Issue{Title: "the item's issue", Body: issuesync.Marker("wi-1")})
	after2 := ingested(t, opened, before, issuesync.Config{Repo: repo})
	if len(after2.WorkItems) != 1 {
		t.Fatalf("the App-opened marked issue was ingested: %v", ids(after2))
	}
}

// TestMirrorAdoptsItsOwnIssueAfterLostSave is D2719's adoption: the daemon
// opened an issue for the item and the write that recorded it did not land, so
// the next turn finds an open issue the App opened carrying the item's marker
// and takes it as the item's own instead of opening a second one.
func TestMirrorAdoptsItsOwnIssueAfterLostSave(t *testing.T) {
	fake := issuesync.NewFake(repo)
	before := backlog()
	before.WorkItems = []orchestrator.WorkItem{item("wi-1")}

	// The first turn opens the issue and records it.
	opened := ingested(t, fake, before, issuesync.Config{Repo: repo, Mirror: true})
	if fake.Opens() != 1 {
		t.Fatalf("the mirror opened %d issues for one item", fake.Opens())
	}
	number := fake.Opened()[0].Number
	if w := itemOf(t, opened, "wi-1"); w.External != issuesync.ExternalID(repo, number) {
		t.Fatalf("the item carries %q, want the issue it was mirrored to", w.External)
	}

	// The state the write-back was lost from: the item as it was, and the issue
	// as the host has it.
	after := ingested(t, fake, before, issuesync.Config{Repo: repo, Mirror: true})
	if fake.Opens() != 1 {
		t.Fatalf("the mirror opened a second issue (%d) instead of adopting its own", fake.Opens())
	}
	if len(after.WorkItems) != 1 {
		t.Fatalf("the adoption left %d items: %v", len(after.WorkItems), ids(after))
	}
	if w := itemOf(t, after, "wi-1"); w.External != issuesync.ExternalID(repo, number) {
		t.Fatalf("the adoption wrote %q, want %q", w.External, issuesync.ExternalID(repo, number))
	}
}

// TestMirrorOffByDefault is D2719: the mirror is a separate opt-in. Without it
// an unapproved, spec-less item with no external id opens nothing and adopts
// nothing — the backlog stays internal until the owner says otherwise.
func TestMirrorOffByDefault(t *testing.T) {
	fake := issuesync.NewFake(repo)
	before := backlog()
	before.WorkItems = []orchestrator.WorkItem{item("wi-1")}

	p := sync(t, fake, before, nil, issuesync.Config{Repo: repo})
	if len(p.Ops) != 0 {
		t.Fatalf("a turn with the mirror off wrote %d ops: %+v", len(p.Ops), p.Ops)
	}
	if fake.Opens() != 0 {
		t.Fatalf("a turn with the mirror off opened %d issues", fake.Opens())
	}
	if w := itemOf(t, before, "wi-1"); w.External != "" {
		t.Fatalf("the item carries %q with the mirror off", w.External)
	}
}

// TestClosedIssueArchivesQueuedItem is D2720: closing the issue archives an item
// that has not launched. An item that has run is left where it is — the issue
// closing is not a person's word about work that is going — and so is one
// something depends on, which a document without it would not read back.
func TestClosedIssueArchivesQueuedItem(t *testing.T) {
	before := backlog()
	queued := item("wi-1")
	queued.External = issuesync.ExternalID(repo, 5)
	ran := item("wi-2")
	ran.External = issuesync.ExternalID(repo, 6)
	ran.Phase = orchestrator.PhaseFailed
	depended := item("wi-3")
	depended.External = issuesync.ExternalID(repo, 7)
	before.WorkItems = []orchestrator.WorkItem{
		queued, ran, depended,
		{ID: "wi-4", Title: "the dependent", Repo: repo, Recipe: recipe.DefaultName, RigorTier: string(tier.Small),
			Brief: "Waiting on wi-3.", DependsOn: []string{"wi-3"}, Phase: orchestrator.PhaseQueued},
	}
	// The listing carries one open issue, of an item that has run; #5 and #7
	// have closed, and #7's item is depended on.
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{Number: 6, Title: "the issue of the item that ran", Body: ""})

	after := ingested(t, fake, before, issuesync.Config{Repo: repo})
	if len(after.Archived) != 1 || after.Archived[0].ID != "wi-1" {
		t.Fatalf("the archive holds %+v, want wi-1 alone", after.Archived)
	}
	if after.Archived[0].Outcome == "" {
		t.Fatalf("the archived item says nothing about how it ended")
	}
	for _, id := range []string{"wi-2", "wi-3", "wi-4"} {
		itemOf(t, after, id)
	}
}

// TestSyncIsIdempotent is the tick's own rule over this step: the same node read
// twice answers the same patch, and a turn over the node that patch made writes
// nothing at all (D1229).
func TestSyncIsIdempotent(t *testing.T) {
	fake := issuesync.NewFake(repo).
		Add(issuesync.Issue{Title: "one", Body: "b"}).
		Add(issuesync.Issue{Title: "two", Body: "b"}).
		Close(2)
	before := backlog()
	before.WorkItems = []orchestrator.WorkItem{item("wi-1")}
	cfg := issuesync.Config{Repo: repo, Mirror: true}

	first := sync(t, fake, before, nil, cfg)
	again := sync(t, fake, before, nil, cfg)
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("the same node answered two different patches:\n%+v\n%+v", first, again)
	}
	after := apply(t, before, first)
	if p := sync(t, fake, after, nil, cfg); len(p.Ops) != 0 {
		t.Fatalf("the turn after the write wrote %d more ops: %+v", len(p.Ops), p.Ops)
	}
}

// TestSyncKeepsItsCadence is D2742: the daemon reads a repository at most once
// per 60 s, whatever wakes the tick, and each repository's cadence is its own.
func TestSyncKeepsItsCadence(t *testing.T) {
	one, other := issuesync.NewFake(repo), issuesync.NewFake("acme/other")
	runner := &issuesync.Runner{Adapters: map[string]issuesync.Adapter{repo: one, "acme/other": other}}
	before := backlog()

	for _, turn := range []time.Time{at(), at().Add(30 * time.Second), at().Add(59 * time.Second)} {
		if _, err := runner.Sync(context.Background(), before, nil, turn); err != nil {
			t.Fatalf("the turn at %s failed: %v", turn, err)
		}
	}
	if one.Lists() != 1 || other.Lists() != 1 {
		t.Fatalf("the repositories were read %d and %d times inside one cadence", one.Lists(), other.Lists())
	}

	if _, err := runner.Sync(context.Background(), before, nil, at().Add(issuesync.Cadence)); err != nil {
		t.Fatalf("the turn at the cadence's edge failed: %v", err)
	}
	if one.Lists() != 2 || other.Lists() != 2 {
		t.Fatalf("the cadence's edge read %d and %d listings, want two each", one.Lists(), other.Lists())
	}

	// One repository's cadence is its own: a second adapter added after the
	// first was read is read at once, whatever the first one's clock says.
	third := issuesync.NewFake("acme/third")
	runner.Adapters["acme/third"] = third
	if _, err := runner.Sync(context.Background(), before, nil, at().Add(61*time.Second)); err != nil {
		t.Fatalf("the turn with a new repository failed: %v", err)
	}
	if third.Lists() != 1 || one.Lists() != 2 {
		t.Fatalf("the new repository was read %d times and the old one %d", third.Lists(), one.Lists())
	}
}

// TestTitlesPassSafetext is D2747 and F57: a title anybody on the tracker can
// write passes safetext on the way into an item — one line, no escape sequence,
// no control character — and is clipped to the 80 characters D2720 bounds an
// ingested title to. The title is the sentence a session reads, so this is what
// keeps a stranger's text from being anything but text.
func TestTitlesPassSafetext(t *testing.T) {
	raw := "\x1b[31mred\x1b[0m title\nsecond line\x07 " + strings.Repeat("x", 300)
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{Title: raw, Body: "b"})

	after := ingested(t, fake, backlog(), issuesync.Config{Repo: repo})
	w := itemOf(t, after, "gh-acme-app-1")
	if strings.ContainsAny(w.Title, "\x1b\n\x07") {
		t.Fatalf("the title carries an escape, a control or a newline: %q", w.Title)
	}
	if utf8.RuneCountInString(w.Title) > 80 {
		t.Fatalf("the title is %d runes: %q", utf8.RuneCountInString(w.Title), w.Title)
	}
	if !strings.Contains(w.Title, "red") || !strings.Contains(w.Title, "second line") {
		t.Fatalf("the title lost its own text: %q", w.Title)
	}
	// The brief is the item's own multi-line text with the title at its head,
	// and it carries no escape and no control character either.
	if strings.ContainsAny(w.Brief, "\x1b\x07") {
		t.Fatalf("the brief carries an escape or a control character: %q", w.Brief)
	}
	if strings.SplitN(w.Brief, "\n", 2)[0] != "# Task: "+w.Title {
		t.Fatalf("the brief's own line reads %q", strings.SplitN(w.Brief, "\n", 2)[0])
	}
}

// TestIngestOnTheBacklogNodeOnTheTick is B4's tick half: the daemon's own cycle
// runs the sync and writes what it answers with to the standing backlog's node
// and no other, through the cycle's fence — and a second cycle inside the
// cadence reads nothing at all (D2742).
func TestIngestOnTheBacklogNodeOnTheTick(t *testing.T) {
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{Title: "an issue on the project's tracker", Body: "b"})
	runner := &issuesync.Runner{Adapters: map[string]issuesync.Adapter{repo: fake}}
	stateStore, backlogStore := &memStore{s: stateNode()}, &memStore{s: backlog()}
	lg := &log{}
	tick := &orchestrator.Tick{
		Store: stateStore,
		Nodes: []orchestrator.Store{backlogStore},
		Runs:  noRuns{},
		Mint:  func(time.Time) (string, error) { return "20261118-090000-abcd", nil },
		Now:   func() (time.Time, bool) { return at(), true },
		Issues: orchestrator.IssueSyncSeams{
			Sync: runner, Backlog: backlogStore, Log: lg,
		},
	}

	if _, err := tick.Step(context.Background()); err != nil {
		t.Fatalf("the cycle failed: %v", err)
	}
	if len(backlogStore.s.WorkItems) != 1 {
		t.Fatalf("the cycle's node holds %v, want the issue it ingested", ids(backlogStore.s))
	}
	w := backlogStore.s.WorkItems[0]
	if w.External != issuesync.ExternalID(repo, 1) || w.Phase != orchestrator.PhaseQueued || w.Approved != nil {
		t.Fatalf("the ingested item is %+v", w)
	}
	if len(stateStore.s.WorkItems) != 0 {
		t.Fatalf("the ingest wrote the state node: %v", ids(stateStore.s))
	}

	if _, err := tick.Step(context.Background()); err != nil {
		t.Fatalf("the second cycle failed: %v", err)
	}
	if fake.Lists() != 1 {
		t.Fatalf("the second cycle read the repository again, %d listings inside one cadence", fake.Lists())
	}
	if len(lg.lines) != 0 {
		t.Fatalf("the cycles logged %v", lg.lines)
	}
}

// TestIngestLeavesTheCycleOnTheStateNode is the tick's own side of B4: the
// ingest reaches the backlog node and the cycle goes on working the state node.
// A backlog node that is over its size target takes no write — the ingest has no
// room to write into — and the cycle carries the state node's own ready item in
// the same cycle, which is what a cycle left on the backlog node would not.
func TestIngestLeavesTheCycleOnTheStateNode(t *testing.T) {
	fake := issuesync.NewFake(repo).Add(issuesync.Issue{Title: "an issue", Body: "b"})
	state := stateNode()
	state.WorkItems = []orchestrator.WorkItem{approved(item("wi-main"))}
	stateStore, backlogStore := &memStore{s: state}, &memStore{s: overCap(t)}
	l := &launcher{}
	tick := &orchestrator.Tick{
		Store:    stateStore,
		Nodes:    []orchestrator.Store{backlogStore},
		Runs:     noRuns{},
		Launcher: l,
		Mint:     func(time.Time) (string, error) { return "20261118-090000-abcd", nil },
		Now:      func() (time.Time, bool) { return at(), true },
		Caps: orchestrator.Caps{
			MaxRunning: 8, MaxPerCycle: 4, SpawnPerMinute: 4, SpawnPerHour: 16, SpawnPerDay: 64,
		},
		Issues: orchestrator.IssueSyncSeams{
			Sync: &issuesync.Runner{Adapters: map[string]issuesync.Adapter{repo: fake}}, Backlog: backlogStore, Log: &log{},
		},
	}

	rep, err := tick.Step(context.Background())
	if err != nil {
		t.Fatalf("the cycle failed: %v", err)
	}
	if rep.Carried != 1 {
		t.Fatalf("the cycle carried %d action(s), and the state node's item is ready whatever the node beside it holds", rep.Carried)
	}
	if len(l.asked) != 1 {
		t.Fatalf("the cycle started %d run(s)", len(l.asked))
	}
	if w := itemOf(t, stateStore.s, "wi-main"); w.Phase != orchestrator.PhaseRunning || w.RunID != "20261118-090000-abcd" {
		t.Fatalf("the state node's item reads back as %+v: the cycle went on working the state node", w)
	}
	if backlogStore.saves != 0 {
		t.Fatalf("the cycle wrote the backlog node %d time(s), and the ingest has no room to write into", backlogStore.saves)
	}
	if fake.Lists() != 0 {
		t.Fatalf("the ingest read the repository %d time(s) for a node over its size target", fake.Lists())
	}
}

// overCap is a backdrop node past its size target: the shape a document holds
// when a release stops writing it, and the one the ingest has no room in.
func overCap(t *testing.T) orchestrator.State {
	t.Helper()
	s := backlog()
	for n := 0; len(s.WorkItems) < 200 && !orchestrator.OverCap(s); n++ {
		w := item(fmt.Sprintf("done-%d", n))
		w.Brief = strings.Repeat("b", orchestrator.MaxBriefRunes)
		w.Phase = orchestrator.PhaseDone
		s.WorkItems = append(s.WorkItems, w)
	}
	if !orchestrator.OverCap(s) {
		t.Fatalf("the fixture is not over the size target")
	}
	return s
}

// launcher is a run starter that records what it was asked for and answers with
// the id it was handed.
type launcher struct{ asked []string }

func (l *launcher) Launch(_ context.Context, _ orchestrator.Spec, runID string) (string, error) {
	l.asked = append(l.asked, runID)
	return runID, nil
}

// stateNode is the project's state node as a daemon reads one: the orchestrator
// id and the role, empty.
func stateNode() orchestrator.State {
	s := backlog()
	s.Rev = 1
	return s
}

// memStore is one node a test hands the tick: the state it answers and the
// writes it takes in memory, under an ETag that moves with every write.
type memStore struct {
	s     orchestrator.State
	etag  orchestrator.ETag
	saves int
}

func (m *memStore) Load(context.Context) (orchestrator.State, orchestrator.ETag, error) {
	return m.s, m.etag, nil
}

func (m *memStore) Save(_ context.Context, s orchestrator.State, etag orchestrator.ETag) error {
	if etag != m.etag {
		return orchestrator.ErrStateMoved
	}
	m.s, m.etag, m.saves = s, etag+"+", m.saves+1
	return nil
}

// noRuns is a run view that answers for no run at all: the fold then has
// nothing to write and the plan nothing to launch.
type noRuns struct{}

func (noRuns) Runs(context.Context, []string) ([]orchestrator.RunReport, error) { return nil, nil }

// log is the issue sync's log, kept whole for the test.
type log struct{ lines []string }

func (l *log) Warn(_ context.Context, line string) { l.lines = append(l.lines, line) }

func stringPtr(s string) *string { return &s }
