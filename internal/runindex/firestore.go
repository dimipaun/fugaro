package runindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The index's own names. The document id of an entry is the run id, which is
// what makes the index one collection rather than one per repository, and the
// summary is one document beside it.
const (
	entryCollection   = "runIndex"
	summaryCollection = "runIndexMeta"
	summaryDocID      = "summary"
	// sortField is the entry's sort key: the moment the run last changed. A page
	// with no window is ordered by it, descending, and it is the field such a
	// page's cursor is a position in.
	sortField = "updated_at"
	// nameField is Firestore's own document-name field, which is the run id.
	// A page with a window is ordered by it instead: a window is the run's
	// start (Filter.Since), the run id names that moment, and the bucket read
	// the index replaces lists the same way (runstore.ListRunIDs), so the two
	// reads of one window hold the same runs in the same order.
	nameField = "__name__"
)

// windowed reports whether the filter asks for a window rather than the whole
// of what it matches.
func windowed(f Filter) bool { return !f.Since.IsZero() }

// sinceRunID is the run id a window starts at: run ids begin with their UTC
// mint time (task.NewRunID), so comparing ids compares start times, and these
// are the same 15 characters runstore.ListRunIDs offsets its listing by.
//
// The floor is the first whole second at or after the window, because a run id
// names whole seconds while a window does not: every real one is a moment
// (`runs stats` builds it as now minus a duration), and the record read tests
// the run's mint time itself (`!t.Before(since)`, runstore.ListRunIDs). Rounding
// up is what makes the id comparison say the same thing: a run minted earlier in
// the window's own second is below the floor, exactly as it is before the window.
func sinceRunID(since time.Time) string {
	at := since.UTC().Truncate(time.Second)
	if at.Before(since) {
		at = at.Add(time.Second)
	}
	return at.Format("20060102-150405")
}

// runIDTime is when a run id was minted, and false for an id this build cannot
// read as one. A run whose id names no moment is not one the window can place,
// so it is kept rather than dropped on a guess -- the same rule the record read
// applies (readRunObjects).
func runIDTime(runID string) (time.Time, bool) {
	t, err := runstore.RunTime(runID)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// pageSize is how many documents one page of a walk reads. It is the client's
// own default (firestore.WithPageSize), spelled here because a walk that asked
// for a document at a time would spend a request per row, and the walk has to
// name a limit to know when a query has more rows behind it.
const pageSize = 300

// firestoreReader is the real Reader: the run index as the history job's fold
// writes it, with the Realtime Database's live set and its recent outcomes
// merged in (D3202, F9).
//
// It reads three things and no more: the summary document, the page's query,
// and the overlay. Nothing here reads the runs bucket, the plan files or the
// backend, so a page costs the same at one run of history and at a hundred
// thousand (F10).
type firestoreReader struct {
	db   *firestore.Client
	live *rtdb.Client
	now  func() time.Time
}

// Option configures a reader.
type Option func(*firestoreReader)

// WithClock is the reader's clock, and is for tests: what a live registry
// entry's freshness is measured against is the moment of the read, and a test
// that seeded a heartbeat has to say what that moment is.
func WithClock(now func() time.Time) Option {
	return func(r *firestoreReader) {
		if now != nil {
			r.now = now
		}
	}
}

// NewFirestore is the index's real Reader: the Firestore documents the history
// job's fold writes, paged with the Realtime Database's overlay merged in.
//
// Both clients are the installation's own. The Firestore one reads the
// database `fugaro setup` created and the history job already holds
// `datastore.user` on; the RTDB one is the budget database, where the live
// registry and the settled outcomes already are -- so the overlay costs no new
// store, no new credential and no new IAM (D3201, D3202).
func NewFirestore(db *firestore.Client, live *rtdb.Client, opts ...Option) Reader {
	r := &firestoreReader{db: db, live: live, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	return r
}

// List reads one page: the summary it was asked for, one query per value the
// filter names, and the overlay read beside it.
func (r *firestoreReader) List(ctx context.Context, f Filter) (Page, error) {
	if r.db == nil {
		return Page{}, errors.New("runindex: the index reader needs a Firestore client")
	}
	if err := checkFilter(f); err != nil {
		return Page{}, err
	}
	sum, err := r.summary(ctx)
	if err != nil {
		return Page{}, err
	}
	// The overlay and the page are read at the same time: the overlay is what
	// fills the gap between two folds, and a page that waited for it would pay
	// for the gap twice. The two cannot simply be started together, because a
	// page starts after a run id and a run the index does not carry has no sort
	// key until the overlay has been read -- which is the one case the page
	// waits for it.
	overlay := &overlayRead{ready: make(chan struct{})}
	var (
		wg      sync.WaitGroup
		page    Page
		pageErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(overlay.ready)
		overlay.rows, overlay.err = readOverlay(ctx, r.live, sum.FoldedThrough, r.now())
	}()
	go func() {
		defer wg.Done()
		page, pageErr = r.page(ctx, f, sum, overlay)
	}()
	wg.Wait()
	if pageErr != nil {
		return Page{}, pageErr
	}
	return page, nil
}

// Summary reads the installation's one summary document.
func (r *firestoreReader) Summary(ctx context.Context) (Summary, error) {
	if r.db == nil {
		return Summary{}, errors.New("runindex: the index reader needs a Firestore client")
	}
	return r.summary(ctx)
}

// summary reads `runIndexMeta/summary`, or answers the summary of an index
// that has never folded.
//
// An absent document is not an error and is not an empty index either: it is
// an index that has not been built, and every listing has to say so rather
// than print the short list it would otherwise print (IR1, RI5). Version is
// the one field a reader refuses: the summary is the document a reader acts
// on, whole, and the history job's image may lag the CLI that reads it.
func (r *firestoreReader) summary(ctx context.Context) (Summary, error) {
	doc, err := r.db.Get(ctx, summaryCollection, summaryDocID)
	if errors.Is(err, firestore.ErrNotFound) {
		return emptySummary(), nil
	}
	if err != nil {
		return Summary{}, fmt.Errorf("runindex: reading the summary: %w", err)
	}
	return decodeSummary(*doc)
}

// emptySummary is the summary of an index that has not been built: no runs, no
// money, no estimate table, and `complete` false, which is what tells every
// reader to say the index is still building.
func emptySummary() Summary {
	states := make(map[string]int64, len(runstore.Statuses))
	for _, s := range runstore.Statuses {
		states[string(s)] = 0
	}
	return Summary{Version: Version, States: states}
}

// decodeSummary reads the summary document, refusing one this release cannot
// read and filling in a state the document does not carry: every key is
// present so a reader reads one without a nil check, and a summary written
// before a status existed is not a summary that lost its runs.
func decodeSummary(doc firestore.Doc) (Summary, error) {
	var sum Summary
	if err := decodeDoc(doc, &sum); err != nil {
		return Summary{}, err
	}
	if sum.Version != Version {
		return Summary{}, fmt.Errorf("runindex: %s/%s is version %d and this release reads version %d", doc.Collection, doc.ID, sum.Version, Version)
	}
	if sum.States == nil {
		sum.States = map[string]int64{}
	}
	for _, s := range runstore.Statuses {
		if _, ok := sum.States[string(s)]; !ok {
			sum.States[string(s)] = 0
		}
	}
	return sum, nil
}

// page is one page of the index: the queries the filter is served by, merged
// with the overlay, in the index's order and cut to the page's own size.
func (r *firestoreReader) page(ctx context.Context, f Filter, sum Summary, overlay *overlayRead) (Page, error) {
	after, err := r.cursor(ctx, f, overlay)
	if err != nil {
		return Page{}, err
	}
	rows, err := overlay.wait()
	if err != nil {
		return Page{}, err
	}
	// How many rows of each query are read. A page prints its own first rows
	// and one more says whether another page exists; the overlay's rows are
	// room to spare, because they are read whole on every page, they displace
	// the index's rows in the merged order, and an outcome among them takes one
	// of those rows out of the active set.
	top := 0
	if f.Limit > 0 {
		top = f.Limit + 1 + len(rows)
	}
	var (
		entries []Entry
		seen    = make(map[string]bool)
		more    bool
	)
	for _, q := range queriesOf(f) {
		got, rest, err := r.walk(ctx, q, after, top, f.Since)
		if err != nil {
			return Page{}, err
		}
		more = more || rest
		for _, e := range got {
			if seen[e.RunID] {
				continue
			}
			seen[e.RunID] = true
			entries = append(entries, e)
		}
	}
	merged := make([]Entry, 0, len(entries)+len(rows))
	for _, e := range entries {
		merged = append(merged, mergeEntry(e, rows[e.RunID]))
	}
	// A row the overlay carries and the queries did not reach is a run the
	// index may already carry, and it has to be placed where its entry is and
	// not where the overlay's own account of it is. A page starts after a run
	// id and that run's cursor is read off its entry (cursor), so a row placed
	// at its heartbeat would be printed above the runs a page resuming from it
	// then skips, and the runs between the two positions would be listed by no
	// page at all. The entries are read for these rows alone -- a run a query
	// returned is merged above -- and the rows are the live set and the days
	// since the fold, so the reads are bounded by the overlay's own size rather
	// than by the history's (F10).
	for id, o := range rows {
		if seen[id] || !overlayMatches(o, f) {
			continue
		}
		e, found, err := r.entry(ctx, id)
		if err != nil {
			return Page{}, err
		}
		if !found {
			merged = append(merged, o.entry())
			continue
		}
		merged = append(merged, mergeEntry(e, o))
	}
	w := windowed(f)
	sort.Slice(merged, func(i, j int) bool {
		if w {
			return newerByRunID(merged[i], merged[j])
		}
		return newer(merged[i], merged[j])
	})

	page := Page{IndexComplete: sum.Complete, FoldedThrough: sum.FoldedThrough, Entries: []Entry{}}
	for _, e := range merged {
		if !afterCursor(e, after, w) {
			continue
		}
		// The window is the run's start, applied by the walk to the index's
		// rows, where it is what stops the walk, and here to the overlay's,
		// which are read whole and have no order to stop in. A run the window
		// cannot place -- an id this build cannot read as a moment -- is kept,
		// as the record read keeps it, rather than dropped on a guess.
		if w {
			if at, ok := runIDTime(e.RunID); ok && at.Before(f.Since) {
				continue
			}
		}
		// The state is applied here and not only in the query the page was read
		// by: a page read per state is a page about the state the run is in, and
		// the merge is what decides it -- an entry the fold read as going and an
		// outcome that settled it is not a row of a page of going runs.
		if len(f.States) > 0 && !slices.Contains(f.States, e.Status) {
			continue
		}
		if f.Active && !active(e, rows[e.RunID]) {
			continue
		}
		page.Entries = append(page.Entries, e)
	}
	if f.Limit > 0 {
		overflow := len(page.Entries) > f.Limit
		if overflow {
			page.Entries = page.Entries[:f.Limit]
		}
		// A page that holds more rows than it prints has another one behind it.
		// So has a page a query stopped short of: the walk reached the top of
		// what it reads, and a page shorter than the caller asked for must not
		// read as the whole of what matched. The cursor resumes either one.
		if overflow || (more && len(page.Entries) > 0) {
			last := page.Entries[len(page.Entries)-1]
			if w {
				// A windowed page is ordered by the run id, so the cursor is
				// one: the run the caller hands back as Filter.After.
				page.Next = &Cursor{RunID: last.RunID}
			} else {
				at := last.UpdatedAt
				page.Next = &Cursor{UpdatedAt: &at, RunID: last.RunID}
			}
		}
	}
	return page, nil
}

// walk reads one query from the top of its order, one Firestore page at a
// time, and stops at top rows, at the first row below the window, or at the
// end of what the query matches. rest says it stopped at top rather than at
// the end, which is what tells a caller another page exists.
//
// The window is a bound on the order and not a filter the index serves: the
// rows arrive ordered by the order's key, so the walk stops at the first row
// below the bound -- every row behind it is past it too. A window orders the
// walk by the run id, which names the moment the run started at, so the first
// run older than the window ends it -- the same place runstore.ListRunIDs stops
// its listing of the same window.
func (r *firestoreReader) walk(ctx context.Context, q firestore.Query, from *Cursor, top int, since time.Time) ([]Entry, bool, error) {
	// A window orders the walk by the run id, which is what places a run's
	// start, so the first id before the window's own is the first row outside
	// it and the rest are older still.
	floor := ""
	if !since.IsZero() {
		floor = sinceRunID(since)
	}
	var (
		out  []Entry
		last *firestore.Doc
	)
	if from != nil {
		last = cursorDoc(from)
	}
	for {
		limit := pageSize
		if top > 0 && top-len(out) < limit {
			limit = top - len(out)
		}
		if limit <= 0 {
			return out, true, nil
		}
		q.Limit, q.StartAfter = limit, last
		docs, err := r.db.RunQuery(ctx, q)
		if err != nil {
			return nil, false, err
		}
		for i := range docs {
			e, err := decodeEntry(docs[i])
			if err != nil {
				return nil, false, err
			}
			if floor != "" && e.RunID < floor {
				return out, false, nil
			}
			out = append(out, e)
		}
		if len(docs) < limit {
			return out, false, nil
		}
		last = &docs[len(docs)-1]
	}
}

// cursor is where the page starts: the sort key of the run the caller paged
// past, from that run's index document where the index carries it and from the
// overlay where it does not (IR1). A run neither of them knows is an error and
// never the beginning of the listing, which would page from the first row for
// ever.
func (r *firestoreReader) cursor(ctx context.Context, f Filter, overlay *overlayRead) (*Cursor, error) {
	if f.After == "" {
		return nil, nil
	}
	doc, err := r.db.Get(ctx, entryCollection, f.After)
	switch {
	case err == nil:
		// A windowed page is ordered by the run id, so its cursor is one run
		// id; the document read is still what refuses a run id the index and
		// the overlay both do not know (below), never a page from the start.
		if windowed(f) {
			return &Cursor{RunID: f.After}, nil
		}
		at, err := docTime(*doc)
		if err != nil {
			return nil, err
		}
		return &Cursor{UpdatedAt: &at, RunID: f.After}, nil
	case !errors.Is(err, firestore.ErrNotFound):
		return nil, fmt.Errorf("runindex: reading the run %s to page after: %w", f.After, err)
	}
	rows, err := overlay.wait()
	if err != nil {
		return nil, err
	}
	row, ok := rows[f.After]
	if !ok {
		return nil, fmt.Errorf("runindex: %s is neither an indexed run nor one the overlay knows, so there is no page after it", f.After)
	}
	if windowed(f) {
		return &Cursor{RunID: row.RunID}, nil
	}
	at := row.At
	return &Cursor{UpdatedAt: &at, RunID: row.RunID}, nil
}

// entry is one run's index document, or false where the index does not carry
// the run -- a run the fold has not read yet, which the overlay is the whole
// account of.
func (r *firestoreReader) entry(ctx context.Context, run string) (Entry, bool, error) {
	doc, err := r.db.Get(ctx, entryCollection, run)
	if errors.Is(err, firestore.ErrNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("runindex: reading the run %s the overlay carries: %w", run, err)
	}
	e, err := decodeEntry(*doc)
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

// afterCursor reports whether a row comes after the page's cursor in the
// page's order -- the run id for a windowed page, the sort key then the run id
// otherwise. The query applies the same bound to the index's own rows; this
// applies it to the overlay's, which are read whole on every page and would
// otherwise be listed again on each one.
func afterCursor(e Entry, c *Cursor, w bool) bool {
	if c == nil {
		return true
	}
	// A windowed page is ordered by the run id alone, so its cursor is one run
	// id and nothing else (Page.Next).
	if w {
		return e.RunID < c.RunID
	}
	if c.UpdatedAt == nil {
		return true
	}
	switch {
	case e.UpdatedAt.After(*c.UpdatedAt):
		return false
	case e.UpdatedAt.Before(*c.UpdatedAt):
		return true
	}
	return e.RunID < c.RunID
}

// newerByRunID orders two rows as a windowed page's order does: the run id
// descending, which is the run's start descending (runstore.ListRunIDs).
func newerByRunID(a, b Entry) bool { return a.RunID > b.RunID }

// newer orders two rows as the index's order does: the sort key descending,
// then the run id descending.
//
// The run id breaks the tie in the direction the client appends `__name__` to
// the orderBy, which is the direction of the last clause it was given -- here
// descending (RI1). The merge has to use the query's own order rather than one
// of its own: a page continues from a cursor over this pair, so a merge that
// ordered a tie the other way would list rows the cursor had already read.
func newer(a, b Entry) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.RunID > b.RunID
}

// mergeEntry is the index's row with the overlay's account of it (D3202).
// Where the two speak about one run, the index wins unless its own account is
// unsettled: an entry whose status is running, or which carries none, yields
// to an overlay that says the run ended, because the fold has not read that
// change yet, and an entry that carries a settled status is the fold's own
// reading of the record the run wrote and is not overridden.
//
// The sort key is the index's own and is not moved. The overlay's is a floor
// under a run it alone carries, not a second history, and the entry is what
// says when the run it carries last changed.
func mergeEntry(e Entry, o overlayRow) Entry {
	if o.Status != "" && (e.Status == "" || e.Status == runstore.StatusRunning) {
		e.Status = o.Status
	}
	return e
}

// overlayMatches reports whether a run the overlay alone supplies answers the
// filter.
//
// The overlay carries the slug the RTDB path names and the status it can
// account for, so a page read per repository or per state is answered from it
// exactly. It carries no plan task, no pull request, no identity and no batch
// -- those are the run's own records, which the overlay never reads (F9) -- so
// a page filtered by one of those lists only the runs the index carries. The
// alternative is a row shown under a filter it may not match at all, and a run
// the fold has not read yet is in the index within one fold either way.
func overlayMatches(o overlayRow, f Filter) bool {
	if f.PlanTask != "" || f.PR != 0 || f.Mine != "" || f.Batch != "" {
		return false
	}
	if len(f.Slugs) > 0 && !slices.Contains(f.Slugs, o.Slug) {
		return false
	}
	if len(f.States) > 0 && !slices.Contains(f.States, o.Status) {
		return false
	}
	return true
}

// active reports whether a run has not ended, which is the second half of the
// default listing (D3203). A run the overlay settled has ended whatever the
// index says: the fold has simply not read that change yet. A run the overlay
// says is in flight is in flight, whatever the entry says, because the two
// accounts are of different moments and the overlay's is the newer one -- the
// index is folded through a moment minutes old and a live registry entry's
// heartbeat is minutes new. Where the overlay says nothing about the run, the
// entry's own settled status is the only account there is.
func active(e Entry, o overlayRow) bool {
	if o.Live {
		return true
	}
	if o.Status != "" {
		return false
	}
	return e.Status == runstore.StatusRunning
}

// checkFilter refuses a filter the index does not serve.
//
// The composite indexes are a closed list (contract-index.md), and a filter
// applied after the page was cut would print a page shorter than the caller
// asked for -- the one failure this package exists to prevent -- so a
// combination with no row on that list is refused rather than answered from a
// page that was read wrong.
func checkFilter(f Filter) error {
	if f.Limit < 0 {
		return fmt.Errorf("runindex: a page limit cannot be negative, not %d", f.Limit)
	}
	keys := 0
	key := ""
	for _, k := range []struct {
		name string
		on   bool
	}{
		{"plan_task", f.PlanTask != ""},
		{"pr", f.PR != 0},
		{"requested_by", f.Mine != ""},
		{"batch", f.Batch != ""},
	} {
		if k.on {
			keys, key = keys+1, k.name
		}
	}
	if keys > 1 {
		return errors.New("runindex: the index carries one key filter at a time: a page is one plan task, one pull request, one identity or one batch, never two")
	}
	if f.Active && len(f.States) > 0 {
		return errors.New("runindex: the active set and an explicit state are two answers to one question")
	}
	if key != "" && (f.Active || len(f.States) > 0) {
		return fmt.Errorf("runindex: a state beside %s is not a filter the index serves", key)
	}
	return nil
}

// queriesOf is the queries one page is read with.
//
// The index is queried by equality alone (RI1), so a filter that names several
// values -- several repositories, several states -- is one query per pair the
// closed list of composite indexes carries, and never a scan: the number of
// requests is the values the caller named, not the history behind them (F10).
//
// The active set is the runs whose own status is running. It is what the
// overlay's live entries are merged with, and it is why a run that ended after
// the last fold is still listed: the fold has it as running, the overlay says
// it ended, and the merge settles it.
func queriesOf(f Filter) []firestore.Query {
	key := map[string]any{}
	switch {
	case f.PlanTask != "":
		key["plan_task"] = f.PlanTask
	case f.PR != 0:
		key["pr"] = f.PR
	case f.Mine != "":
		key["requested_by"] = f.Mine
	case f.Batch != "":
		key["batch"] = f.Batch
	}
	states := f.States
	if f.Active {
		states = []runstore.Status{runstore.StatusRunning}
	}
	// A query names a slug and a state only when the filter does. The empty
	// value is the query that names neither: every repository, every state.
	slugs := f.Slugs
	if len(slugs) == 0 {
		slugs = []string{""}
	}
	if len(states) == 0 {
		states = []runstore.Status{""}
	}
	out := make([]firestore.Query, 0, len(slugs)*len(states))
	for _, slug := range slugs {
		for _, state := range states {
			where := make(map[string]any, len(key)+2)
			for k, v := range key {
				where[k] = v
			}
			if slug != "" {
				where["slug"] = slug
			}
			if state != "" {
				where["status"] = string(state)
			}
			// A window is the run's start, so a windowed page is ordered by the
			// run id -- which is the document's own name, and sorts by the
			// moment it was minted. The walk then stops at the first run older
			// than the window, exactly as runstore.ListRunIDs stops its own
			// listing, so the index and the record read it replaces hold one
			// window and not two.
			order := firestore.Order{Field: sortField, Direction: "DESCENDING"}
			if windowed(f) {
				order = firestore.Order{Field: nameField, Direction: "DESCENDING"}
			}
			out = append(out, firestore.Query{
				Collection: entryCollection,
				Where:      where,
				OrderBy:    []firestore.Order{order},
			})
		}
	}
	return out
}

// decodeEntry reads one index document.
//
// The document id is the run id -- it is what makes the index one collection
// rather than one per repository -- so a row is named by where it was read and
// not by what it says about itself. A document without its sort key is refused
// rather than placed at the epoch: no page can order it, and a cursor over it
// would be a position in an order it is not in.
func decodeEntry(doc firestore.Doc) (Entry, error) {
	var e Entry
	if err := decodeDoc(doc, &e); err != nil {
		return Entry{}, err
	}
	e.RunID = doc.ID
	if e.UpdatedAt.IsZero() {
		return Entry{}, fmt.Errorf("runindex: %s/%s carries no %s, so no page can place it", doc.Collection, doc.ID, sortField)
	}
	return e, nil
}

// decodeDoc reads a document into a Go value through the names its fields are
// written under, which are the struct's own: the fold encodes the entry and
// the summary the way the wire format does, and the strict readers behind a
// record's own decoding are the ones that read it. A field a later release
// added arrives with the document and is ignored here, the way the same bytes
// from the runs bucket would be.
func decodeDoc(doc firestore.Doc, out any) error {
	raw, err := json.Marshal(doc.Fields)
	if err != nil {
		return fmt.Errorf("runindex: reading %s/%s: %w", doc.Collection, doc.ID, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("runindex: reading %s/%s: %w", doc.Collection, doc.ID, err)
	}
	return nil
}

// docTime is a document's own sort key, which is what a cursor is a position
// in.
func docTime(doc firestore.Doc) (time.Time, error) {
	v, ok := doc.Fields[sortField]
	if !ok {
		return time.Time{}, fmt.Errorf("runindex: %s/%s carries no %s, so no page can place it", doc.Collection, doc.ID, sortField)
	}
	at, ok := v.(time.Time)
	if !ok {
		return time.Time{}, fmt.Errorf("runindex: %s/%s carries a %s that is not a timestamp", doc.Collection, doc.ID, sortField)
	}
	return at.UTC(), nil
}

// overlayRead is the overlay's read, started beside the page and waited on by
// it: a page resolves its cursor through the overlay, and the wait is the
// overlay's own read rather than a second one.
type overlayRead struct {
	ready chan struct{}
	rows  map[string]overlayRow
	err   error
}

// wait is the overlay's result, blocking until its read is done. It is safe to
// call more than once.
func (o *overlayRead) wait() (map[string]overlayRow, error) {
	<-o.ready
	return o.rows, o.err
}

// cursorDoc is the document a query is told to start after: the run id is the
// document's own id, and the sort key is its value. Which fields the client
// reads off it are the query's order's own (RI1) -- the document name for a
// windowed page, the sort key otherwise -- so a cursor that carries no sort key
// is a windowed page's position and nothing else.
func cursorDoc(c *Cursor) *firestore.Doc {
	doc := &firestore.Doc{Collection: entryCollection, ID: c.RunID}
	if c.UpdatedAt != nil {
		doc.Fields = map[string]any{sortField: *c.UpdatedAt}
	}
	return doc
}
