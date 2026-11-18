package issuesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// Cadence is how often one repository's issues are read: at most once per this
// per repository, whatever wakes the tick (D2742).
//
// It is a cadence and not a timeout, for the reason the merge read's is: an
// issue filed now becomes an item within this long, and the wait costs the
// issue route one listing per repository per minute however many times the
// daemon wakes in between — a wake every two seconds would otherwise spend the
// installation's own GitHub budget on listings.
const Cadence = 60 * time.Second

// Logger is where one repository's turn leaves the line that says it did not
// happen: a host that would not answer, an item whose issue would not open. It
// is the daemon's log (the tick's own IssueSyncLog), and it is never the
// place a turn's outcome is read from: nothing here fails a cycle.
type Logger interface {
	Warn(ctx context.Context, line string)
}

// Config is what one repository's turn is told: which repository, whether the
// mirror is on, and where a line about a write that did not happen goes.
type Config struct {
	// Repo is the repository this turn reads, in the canonical spelling
	// (task.CanonicalRepo). The external id an item is keyed on is built from
	// it, so a name in any other spelling is a turn refused rather than one
	// that writes a key no lookup reaches.
	Repo string
	// Mirror is whether an unapproved, spec-less item of this repository opens
	// an issue of its own (D2719). Off is the default and means it does not.
	Mirror bool
	// AppID is the GitHub App this installation recorded for the repository
	// (localcfg's github_app_id), and the one value that says an issue was
	// opened by THIS installation's App rather than by another App the
	// repository has installed (D2728): the sync compares every issue's own
	// AppID against it. Empty is an installation that names none, and then no
	// issue counts as the App's own — the marker neither hides an issue from
	// the ingest nor lets an item adopt one.
	AppID string
	// Log is where a line goes about something a turn could not do that is not
	// the whole turn — an issue that would not open, above all. A nil one
	// writes nothing.
	Log Logger
}

// Nodes is the state one repository's turn reads and the node its patch lands
// on: the backlog node itself, every node beside it, the server time the turn
// is stamped with, and how much of the patch the turn may use.
type Nodes struct {
	// Backlog is the standing backlog's node (D2735): the node every op the
	// turn answers with lands on, and the one whose items are ingested into,
	// mirrored out of and archived.
	Backlog orchestrator.State
	// Others are the nodes beside it — the state node and every plan's
	// (D2735) — which the turn reads and never writes. They are here because
	// the external id is unique across the project's items and not only within
	// one node (D2728): an issue whose id some other node's item already
	// carries is one that node's, and an id already taken anywhere is not one
	// this turn may mint.
	Others []orchestrator.State
	// Now is the database server's reading of the clock (D1230), and the stamp
	// of every op the turn builds.
	Now time.Time
	// Room is how many ops the patch this turn is part of may still carry: one
	// patch carries at most orchestrator.MaxOps, and a turn that joins a patch
	// other turns have already filled is told what is left. Zero or less means
	// the whole of one patch.
	Room int
}

// Sync is one repository's turn at the ingest (B4, D2718, D2719, D2720,
// D2728): the patch one tick makes to the backlog node for one repository
// whose open issues are read through adapter.
//
// It reads the listing once — open issues only, which is what makes an item
// whose number the listing does not carry an item whose issue has closed — and
// answers, in this order:
//
//   - every open issue of the repository that no item of any node carries the
//     external id of becomes a queued, unapproved item of the backlog node,
//     holding that id, a title of at most MaxTitleRunes characters that has
//     passed safetext (D2747) and the SHA-256 of the issue's body — never the
//     body (D2720);
//   - an item of this repository whose issue is not in the listing any more —
//     it closed — is archived if it never launched;
//   - with Mirror, an unapproved spec-less item of this repository with no
//     external id adopts an open issue one of the installation's App opened
//     with this item's marker in it, and opens one otherwise (D2719).
//
// An issue THIS installation's App opened whose body carries a marker is never
// ingested: it is the other face of an item that is already here (D2728). A
// marker in anybody else's issue — a person's, or another App the repository
// has installed — is text and nothing more: AppOpened is the whole of the
// distinction, and it holds an issue to the App ID the installation recorded
// (Config.AppID). Such an issue is ingested like any other, which is what keeps
// a marker from hiding an issue from the daemon, whoever wrote it.
//
// Every op is put through the daemon's own fence before it is answered with
// (orchestrator.Apply, item by item, against the state the turn has built so
// far), and one the fence refuses is dropped rather than returned: the item
// moved under this turn, or the node has no room for it, and the next turn
// reads it as it stands. Nothing here fails a cycle, and a refusal is never
// answered with a state the release would not read.
//
// An error is the whole of the turn: a repository whose name the key cannot be
// built from, a host that would not answer, in which case NOTHING is answered
// with — a listing that failed is not an empty listing, and a turn that read it
// as one would archive every item the repository has. The caller logs the line
// and goes on (the tick's own step).
func Sync(ctx context.Context, adapter Adapter, nodes Nodes, cfg Config) (orchestrator.Patch, error) {
	repo, err := task.CanonicalRepo(cfg.Repo)
	if err != nil || repo != cfg.Repo {
		return orchestrator.Patch{}, fmt.Errorf("issuesync: %q is not a repository as this turn reads one: it is the canonical owner/name the external id is built from", safetext.Field(cfg.Repo))
	}
	issues, err := adapter.ListOpen(ctx)
	if err != nil {
		return orchestrator.Patch{}, err
	}
	if nodes.Now.IsZero() {
		return orchestrator.Patch{}, fmt.Errorf("issuesync: the turn carries no server time, and every stamp comes from the database server's clock (D1230)")
	}
	room := nodes.Room
	if room <= 0 {
		room = orchestrator.MaxOps
	}
	s := &syncer{
		ctx: ctx, adapter: adapter, repo: repo, mirror: cfg.Mirror, appID: cfg.AppID, log: cfg.Log,
		base: nodes.Backlog, cur: nodes.Backlog, others: nodes.Others, now: nodes.Now,
		actor: orchestrator.Actor{Class: orchestrator.Daemon, Now: nodes.Now},
		room:  room, open: make(map[int]Issue, len(issues)),
		marked: map[string]Issue{}, taken: map[string]bool{}, ids: map[string]bool{},
	}
	s.index(issues)
	s.archiveClosed()
	s.ingest(issues)
	if s.mirror {
		s.mirrorItems(issues)
	}
	s.recordStop(len(issues))
	if len(s.ops) == 0 {
		return orchestrator.Patch{}, nil
	}
	// The patch is built from the node as the caller handed it in, whatever the
	// fence incremented while this turn simulated its own ops: Rev is the
	// document's revision and not a counter the ops move, so the patch the
	// caller applies in one write is the state this turn derived.
	return orchestrator.Patch{BaseRev: nodes.Backlog.Rev, Ops: s.ops}, nil
}

// syncer is one repository's turn as it is built: the state the fence has
// already taken this turn's ops into, the room left, and the lookups the
// listing is read through.
type syncer struct {
	ctx     context.Context
	adapter Adapter
	repo    string
	mirror  bool
	appID   string
	log     Logger

	base   orchestrator.State
	cur    orchestrator.State
	others []orchestrator.State
	now    time.Time
	actor  orchestrator.Actor
	room   int
	ops    []orchestrator.Op

	open   map[int]Issue
	marked map[string]Issue
	taken  map[string]bool
	ids    map[string]bool

	// stopped is why the ingest did not take every open issue this turn, and
	// left is how many issues it did not reach. Both are what the one fact is
	// written from (D2720), and both are empty when it took them all.
	stopped string
	left    int
}

// index reads the listing and the nodes into the lookups the turn works from:
// which issues are open, which item ids are taken anywhere, which external ids
// the project's items already carry, and which of this repository's open issues
// are an item's other face.
func (s *syncer) index(issues []Issue) {
	for _, i := range issues {
		s.open[i.Number] = i
	}
	states := append([]orchestrator.State{s.base}, s.others...)
	for _, st := range states {
		for _, w := range st.WorkItems {
			s.ids[w.ID] = true
			if w.External != "" {
				s.taken[w.External] = true
			}
		}
		for _, a := range st.Archived {
			s.ids[a.ID] = true
		}
	}
	for _, i := range issues {
		if !i.AppOpened(s.appID) {
			continue
		}
		if id, ok := markedItem(i.Body); ok {
			if _, seen := s.marked[id]; !seen {
				s.marked[id] = i
			}
		}
	}
}

// archiveClosed archives the item of every issue of this repository that the
// listing no longer carries — a closed issue — when the item has not launched
// (D2720).
//
// "Not launched" is read off the item and not off the host: a queued item no
// intent of this node is open for, no approval stands on and nothing in the
// list depends on is one nothing has started and nothing is waiting for, which
// is the same set the fence's own archivable refuses for. An item that ran, one
// a person approved and one something depends on is left where it is: the issue
// closing is not a person's word about work that is going, and a dependent
// named by an item that is gone is a document this release does not read.
//
// The write is two ops and one action: a queued item is not a finished one, and
// only a finished item leaves the work list, so the item is cancelled and
// archived in the same patch. The fence takes the pair or neither.
func (s *syncer) archiveClosed() {
	for _, w := range s.base.WorkItems {
		number, ok := ExternalNumber(s.repo, w.External)
		if !ok {
			continue
		}
		if _, open := s.open[number]; open {
			continue
		}
		if w.Phase != orchestrator.PhaseQueued || w.Approved != nil {
			continue
		}
		if s.hasOpenIntent(w.ID) || s.hasDependent(w.ID) {
			continue
		}
		phase := orchestrator.PhaseCancelled
		s.add(
			orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: w.ID, Phase: &phase}},
			orchestrator.Op{Op: orchestrator.OpWorkItemArchive, Archive: &orchestrator.ArchiveOp{ID: w.ID, Outcome: "the issue closed"}},
		)
	}
}

// hasOpenIntent reports whether an attempt of this node's is open for the item:
// a run that has not reported, which is a run going.
func (s *syncer) hasOpenIntent(itemID string) bool {
	for _, it := range s.base.Intents {
		if orchestrator.IntentItem(it.ID) == itemID && it.Outcome == "" {
			return true
		}
	}
	return false
}

// hasDependent reports whether an item of this node names the id: a dependency
// is resolved inside one node (D2735, F44), so the node the item is in is the
// whole of where one may be.
func (s *syncer) hasDependent(itemID string) bool {
	for _, w := range s.base.WorkItems {
		if w.ID == itemID {
			continue
		}
		if slices.Contains(w.DependsOn, itemID) {
			return true
		}
	}
	return false
}

// ingest turns the open issues of the repository into items, in the host's own
// order, until the node runs out of the room the ingest keeps for somebody
// else's write (D2720).
//
// What stops it is either of the two reserves: the item count the acceptances
// name — the ingest stops with ingestHeadroomOf items of room below
// MaxWorkItems — and the bytes below MaxStateBytes that a person's own
// `backlog add` needs. A flood of issues therefore lands as many items as the
// never-used space of the node holds and no more, and the node is left with the
// room it had for the work a person files. The stop is recorded as one fact,
// once per repository (recordStop).
func (s *syncer) ingest(issues []Issue) {
	for i, issue := range issues {
		if issue.AppOpened(s.appID) {
			if _, ok := markedItem(issue.Body); ok {
				// The other face of an item that is already here (D2728). The
				// item carries the external id this issue's number makes, so
				// the ingest would skip it in any case — and an issue whose
				// item was archived refuses to be ingested back into a second
				// item, which is what this skip is for.
				continue
			}
		}
		external := ExternalID(s.repo, issue.Number)
		if s.taken[external] {
			continue
		}
		if len(s.cur.WorkItems)+1 > orchestrator.MaxWorkItems-ingestHeadroomOf {
			s.stop("the node is near the item count it holds", len(issues)-i)
			return
		}
		id := issueItemID(s.repo, issue.Number, func(id string) bool { return s.ids[id] })
		title := issueTitle(issue.Title)
		if title == "" {
			// A title is required of every work item and an issue's own is
			// required by the host, so this is the shape of a title that is
			// nothing but control characters: what is stored is the id the item
			// is keyed on, which says where to read the issue.
			title = external
		}
		brief := ingestBrief(external, title)
		hash := bodyHash(issue.Body)
		op := orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
			ID: id, Title: &title, Repo: &s.repo, Recipe: &recipeName, RigorTier: &tierName,
			Brief: &brief, External: &external, BodySHA256: &hash,
		}}
		if !s.addWithin(ingestByteReserve, op) {
			// The fence refused it or it would have eaten the room a person's
			// own write needs: the body of the node is full, and the issues
			// behind this one wait for the next cadence.
			s.stop("the node is near the size it holds", len(issues)-i)
			return
		}
		s.ids[id], s.taken[external] = true, true
	}
}

// ingestBrief is the brief an ingested item carries: the issue's title, as the
// first line a task file's own heading takes, and where the item came from.
//
// It is a reference and not the work. The body is read again at launch (B9,
// D2720) and the run is briefed with it, so nothing here is derived from the
// body — what the item carries is the id an approval binds and the hash that
// says the body is still the body the person read.
func ingestBrief(external, title string) string {
	return "# Task: " + title + "\n\nThe item is " + external + ": its brief is read from the issue when a run starts.\n"
}

// bodyHash is a ticket body as an item stores it: the plain SHA-256 in
// lower-case hex, with no `sha256:` prefix (orchestrator's bodySHA256RE, D2720).
func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// mirrorItems opens the item's own issue, or adopts the one it already has
// (D2719).
//
// Only an item of this repository is mirrored, and that is a decision the
// acceptance does not spell out. The adapter is one repository's, an external
// id names the repository its issue is in, and a project may name several
// adapters: an item of a repository this turn does not answer for would be
// mirrored by whichever repository happened to be read first, so the issue
// would land somewhere nobody chose — and the second repository's turn would
// find the marker on an issue it did not write. The item's own repository is
// the one repository whose adapter can open its issue, so it is the one that
// does.
//
// Adoption comes first and is per item, not per turn: an issue the App opened
// with this item's marker is this item's issue whatever happened to the write
// that recorded it, so a turn that opened one and lost the record meets it here
// rather than opening a second (D2719). Only an item no such issue exists for
// is opened, which is the one write this turn makes outside the fence — it
// cannot be taken back — and it is made only where the patch still has the op
// its write-back needs.
func (s *syncer) mirrorItems(issues []Issue) {
	for _, w := range s.cur.WorkItems {
		if w.External != "" || w.Spec != "" || w.Approved != nil || w.Phase != orchestrator.PhaseQueued || w.Repo != s.repo {
			continue
		}
		if issue, ok := s.marked[w.ID]; ok {
			s.adopt(w.ID, issue)
			continue
		}
		if len(s.ops)+1 > s.room {
			// The write-back the issue needs is one op, and a patch with no
			// room for it is not one to open an issue in: the issue would be
			// opened and never recorded here, which the next turn repairs by
			// adopting it — but a turn that opens nothing costs nothing.
			return
		}
		issue, err := s.adapter.Open(s.ctx, issueTitle(w.Title), mirrorBody(w.ID))
		if err != nil {
			// Field and not OneLine: a host's answer is remote text of a length
			// this side does not choose, and Field bounds it.
			s.warn(fmt.Sprintf("the issue for %s could not be opened: %s", safetext.Field(w.ID), safetext.Field(err.Error())))
			return
		}
		if issue.Number <= 0 {
			s.warn(fmt.Sprintf("the host answered the issue opened for %s without a number", safetext.Field(w.ID)))
			return
		}
		s.adopt(w.ID, issue)
	}
}

// mirrorBody is what the issue the daemon opens for an item carries: the marker
// with the item's id (D2719), and one line of the daemon's own.
//
// The item's own text is not published here, and that is a decision no decision
// record spells out: what the issue is for is that work sitting in the backlog
// is visible where the people who filed it already look, and a brief written for
// a coding agent is not text anybody asked to publish — mirroring is opt-in, and
// publishing a brief is not the same opt-in. The title, which is the item's own
// line and is what a reader needs to tell the issue apart, is on the issue;
// everything else stays in the project's own document, where approval happens.
func mirrorBody(itemID string) string {
	return Marker(itemID) + "\n\n" + "Filed by Fugaro for a work item of this project's backlog.\n"
}

// adopt writes the issue's id and its body's hash back onto the item: the
// external id the dedupe key is (D2728) and the hash an approval binds (D2720).
//
// The op goes through the fence like every other, and it is the daemon's alone
// to write (applyUpsert). It touches no declaration of the item's own, so it
// changes nothing a person approved — an item that has one is not mirrored at
// all — while an item whose external id moves to a new value would revoke one,
// which is the same rule seen from the other side.
func (s *syncer) adopt(itemID string, issue Issue) {
	external, hash := ExternalID(s.repo, issue.Number), bodyHash(issue.Body)
	if s.taken[external] {
		// Some other item already carries this issue's key, which is a document
		// that should not exist (ValidateState refuses two items with one key in
		// a node). Writing it here would be the second one.
		return
	}
	if !s.add(orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID: itemID, External: &external, BodySHA256: &hash,
	}}) {
		return
	}
	s.taken[external] = true
}

// recordStop writes the one fact a stopped ingest leaves (D2720), and takes it
// away again when a turn took every open issue it read.
//
// It is one fact per repository, keyed on the repository and not on the node:
// what it says is that this repository's issues are not all ingested, which is
// a fact about that repository alone, and a second repository's flood says
// nothing about the first. The write is skipped when the node already carries
// exactly this value, which is what makes a turn that runs twice over one node
// write nothing the second time.
func (s *syncer) recordStop(issues int) {
	key := factKey(s.repo)
	if s.stopped == "" {
		if _, ok := s.cur.Facts[key]; ok {
			s.add(orchestrator.Op{Op: orchestrator.OpFactDelete, Fact: &orchestrator.FactOp{Key: key}})
		}
		return
	}
	value := fmt.Sprintf("the ingest of %s stopped with %d open issue(s) left: %s, and that room is kept for the work a person files", s.repo, s.left, s.stopped)
	value = safetext.Clip(safetext.OneLine(value), orchestrator.MaxFactValueRunes)
	if held, ok := s.cur.Facts[key]; ok && held.Value == value {
		return
	}
	s.add(orchestrator.Op{Op: orchestrator.OpFactSet, Fact: &orchestrator.FactOp{Key: key, Value: value}})
}

// stop records why the ingest did not reach every open issue, and how many it
// did not reach. The first reason wins: the first stop is the one that ended
// the walk.
func (s *syncer) stop(why string, left int) {
	if s.stopped == "" {
		s.stopped, s.left = why, left
	}
}

// simulate is one action — one op or the pair an archive takes — put through the
// fence against the state this turn has built so far, and the state it makes.
// The second return is false where the fence refuses it, which is the caller's
// cue to leave the action out rather than to fail the turn.
func (s *syncer) simulate(ops ...orchestrator.Op) (orchestrator.State, bool) {
	if len(ops) == 0 || len(s.ops)+len(ops) > s.room {
		return orchestrator.State{}, false
	}
	next, err := orchestrator.Apply(s.cur, orchestrator.Patch{BaseRev: s.cur.Rev, Ops: ops}, s.actor)
	if err != nil {
		return orchestrator.State{}, false
	}
	return next, true
}

// add is one action the turn takes: it lands when the fence takes it, and when
// the whole patch still has the room for it.
func (s *syncer) add(ops ...orchestrator.Op) bool {
	next, ok := s.simulate(ops...)
	if !ok {
		return false
	}
	s.cur, s.ops = next, append(s.ops, ops...)
	return true
}

// addWithin is add with a byte reserve: the action lands, and it is taken back
// when what it leaves is closer to the node's size target than the room a
// person's own write needs (D2720).
//
// The measure is the fence's own (orchestrator.stateBytes, which is the length
// of the document's JSON), computed here because the reserve is a question
// about how far below the target a turn stops and not about whether the
// document is over it: that second question is the fence's and a state past it
// is refused outright.
func (s *syncer) addWithin(reserve int, ops ...orchestrator.Op) bool {
	next, ok := s.simulate(ops...)
	if !ok {
		return false
	}
	if reserve > 0 && stateBytes(next) > orchestrator.MaxStateBytes-reserve {
		return false
	}
	s.cur, s.ops = next, append(s.ops, ops...)
	return true
}

// warn leaves one line in the log, and writes nothing when there is no log: a
// turn that was wired without one still does its work.
func (s *syncer) warn(line string) {
	if s.log != nil {
		s.log.Warn(s.ctx, line)
	}
}

// stateBytes is the document's size as the fence reads it. Two readings of one
// number and not two numbers: what the reserve is held below (MaxStateBytes)
// and what a write is refused over are the same measure, and a Marshal that
// cannot represent the document is over the target rather than under it.
func stateBytes(s orchestrator.State) int {
	raw, err := json.Marshal(s)
	if err != nil {
		return orchestrator.MaxStateBytes + 1
	}
	return len(raw)
}

// MaxItemIDRunes is the longest item id this package mints: the bound the
// fence's own id rule holds every work item's id to (orchestrator's idRE,
// sixty-four characters).
const MaxItemIDRunes = 64

// ingestHeadroomOf is D2720's bound on the item count: the ingest stops with
// this many items of room below MaxWorkItems, so that a flood of issues never
// takes the room a person's own `backlog add` files work in.
const ingestHeadroomOf = 20

// ingestByteReserve is the same bound in bytes: the room the ingest leaves below
// MaxStateBytes, which is the cap that binds first on a node of ordinary items —
// a state of a few dozen items is at the size target long before it is at the
// item count (F34).
//
// It is sized for the write it keeps the room for: a `backlog add` item is an
// id, a title, a repository, a recipe, a tier and a brief, and four kilobytes
// is a brief of a few thousand ASCII characters with room to spare. A brief
// written at MaxBriefRunes in characters of four bytes is past this reserve, and
// the person whose item it is is told by the fence itself that the node is full
// — with the fix (finish or delete items), which is what the fact beside this
// reserve says as well.
const ingestByteReserve = 4 << 10

// recipeName and tierName are what an ingested item carries for the two fields
// every work item has and an issue says nothing about: the recipe a project has
// without configuring one, and the smallest tier.
//
// They are read here rather than invented because the state model requires both
// of every item and the person's approval is what changes either — the same
// answer `backlog add` writes for the same reason (backlogUpsert).
var (
	recipeName = recipe.DefaultName
	tierName   = string(tier.Small)
)

// idName is a repository name as an item id and a fact key may carry it: the
// class a name is written with (orchestrator's idRE — letters, digits, hyphens
// and underscores), with everything else — the slash between the owner and the
// name above all — written as a hyphen.
func idName(repo string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, repo)
}

// issueItemID mints the id of the item an issue becomes: the repository the
// issue is in and its number, as `gh-acme-app-12`.
//
// The repository is in the id because a project may sync several of them and
// two issues of two repositories may carry the same number — the ids are the
// node's and the external ids are the project's, and an id that collided across
// repositories would be two items of one name.
//
// taken is the ids the turn has already seen anywhere — the nodes' items, their
// archives and what it has minted this turn. A name already taken is not
// overwritten: the number is kept and a sequence goes in front of it, so an
// issue whose item was archived and whose issue was reopened becomes a second
// item under a name of its own rather than an upsert over a stranger's item.
func issueItemID(repo string, number int, taken func(string) bool) string {
	for k := 1; ; k++ {
		tail := "-" + strconv.Itoa(number)
		if k > 1 {
			tail = "-" + strconv.Itoa(k) + tail
		}
		head := safetext.Clip("gh-"+idName(repo), MaxItemIDRunes-len(tail))
		if head == "" {
			head = "gh"
		}
		if id := head + tail; !taken(id) {
			return id
		}
	}
}

// factKey is the key one repository's stopped ingest is recorded under. It is a
// name and not the repository: the fence holds a fact key to the same class an
// item id is written with, and a repository name carries a slash. A name too
// long for a key keeps its head and a digest of the whole name, so that two
// repositories sharing a long prefix do not share the fact — one fact per
// repository is the whole of what it says (D2720).
func factKey(repo string) string {
	const prefix = "issue_ingest_"
	name := idName(repo)
	if len(prefix)+len(name) <= orchestrator.MaxFactKeyRunes {
		return prefix + name
	}
	sum := sha256.Sum256([]byte(repo))
	digest := hex.EncodeToString(sum[:])[:8]
	head := safetext.Clip(name, orchestrator.MaxFactKeyRunes-len(prefix)-1-len(digest))
	return prefix + head + "-" + digest
}

// Runner is a project's issue sync as the daemon's tick reaches it: the
// repositories `issue_sync` names, each with the adapter that answers for it,
// and the cadence its reads are held to (D2742).
//
// One turn of the tick is one turn of each repository that is due, in the order
// of their names — a map's order is not an order, and two daemons of one
// project must build the same patch from the same node. A repository inside its
// cadence is skipped without a read, whatever woke the tick.
//
// Nothing here holds a credential, names a host or reaches a network of its
// own: the adapters are the daemon's, built in internal/cli over the
// credentials service's issue route (B3), which is what makes this package's
// tests a unit test rather than a cloud experiment.
type Runner struct {
	// Adapters are the repositories' adapters, keyed by the canonical
	// repository name the external ids are built from.
	Adapters map[string]Adapter
	// AppIDs are the GitHub App this installation recorded for each of those
	// repositories, keyed the same way: the id a turn holds an issue's own App
	// to before its marker counts (Config.AppID, D2728). A repository absent
	// from it names no App, and no issue of it counts as the App's own.
	AppIDs map[string]string
	// Mirror is whether an unapproved, spec-less item of a repository opens an
	// issue there (D2719).
	Mirror bool
	// Log is where a repository's failed turn leaves its one line. A nil one
	// writes nothing.
	Log Logger
	// kept is when each repository was last read, made on first use: this is a
	// value the daemon holds for as long as it runs, and a daemon that restarted
	// is one whose memory is empty, which costs one read per repository before
	// its cadence is known.
	kept map[string]time.Time
}

// Sync is one tick's turn at the ingest, over every repository that is due: the
// patch the backlog node takes, built from the node the caller hands in.
//
// The patch is one patch for every repository, and each repository's turn is
// built against what the turns before it left (orchestrator.Apply, in memory),
// so that a second repository's turn sees the item the first one ingested and
// never mints its id again. What is returned is that whole patch against
// backlog's own revision: the caller applies it in one write, and the state it
// makes is the state this turn derived.
//
// A repository whose read failed is one line in the log and nothing else. It is
// looked at again on the next cadence and not on the next wake.
func (r *Runner) Sync(ctx context.Context, backlog orchestrator.State, others []orchestrator.State, now time.Time) (orchestrator.Patch, error) {
	repos := make([]string, 0, len(r.Adapters))
	for repo := range r.Adapters {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	var ops []orchestrator.Op
	cur := backlog
	for _, repo := range repos {
		if len(ops) >= orchestrator.MaxOps {
			// The patch is full: the repositories behind this one wait for the
			// next cadence rather than for a second write in this cycle.
			break
		}
		if !r.due(repo, now) {
			continue
		}
		// Marked before the read and not after it lands: a host that would not
		// answer is a host to ask again in a minute, not one to ask again on the
		// next wake.
		r.markRead(repo, now)
		patch, err := Sync(ctx, r.Adapters[repo], Nodes{Backlog: cur, Others: others, Now: now, Room: orchestrator.MaxOps - len(ops)},
			Config{Repo: repo, Mirror: r.Mirror, AppID: r.AppIDs[repo], Log: r.Log})
		if err != nil {
			r.warn(ctx, fmt.Sprintf("the issues of %s could not be read: %s", safetext.Field(repo), safetext.Field(err.Error())))
			continue
		}
		if len(patch.Ops) == 0 {
			continue
		}
		next, err := orchestrator.Apply(cur, orchestrator.Patch{BaseRev: cur.Rev, Ops: patch.Ops}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
		if err != nil {
			// A patch this package built and the fence refused is a bug and not
			// an outage: the turn is dropped rather than written, and the line
			// says so.
			r.warn(ctx, fmt.Sprintf("the issues of %s were read and the patch they answer with was refused: %s", safetext.Field(repo), safetext.Field(err.Error())))
			continue
		}
		cur, ops = next, append(ops, patch.Ops...)
	}
	if len(ops) == 0 {
		return orchestrator.Patch{}, nil
	}
	return orchestrator.Patch{BaseRev: backlog.Rev, Ops: ops}, nil
}

// due reports whether one repository may be read at this reading of the
// server's clock: the first reading of a repository always is, and one inside
// its cadence never is. A clock that reads earlier than the reading it last
// stamped holds the read back rather than making one early, which is the
// direction a shared budget is spent in.
func (r *Runner) due(repo string, now time.Time) bool {
	last, ok := r.kept[repo]
	return !ok || !now.Before(last.Add(Cadence))
}

// markRead records that one repository has been read at this reading.
func (r *Runner) markRead(repo string, now time.Time) {
	if r.kept == nil {
		r.kept = map[string]time.Time{}
	}
	r.kept[repo] = now
}

func (r *Runner) warn(ctx context.Context, line string) {
	if r.Log != nil {
		r.Log.Warn(ctx, line)
	}
}
