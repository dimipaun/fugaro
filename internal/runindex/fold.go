package runindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The index's own two documents (contract-index.md).
const (
	// EntriesCollection holds one document per run, `runIndex/<run id>`.
	EntriesCollection = "runIndex"
)

// The estimate table's bounds. They are the ones `plan status` reads its own
// history window with (planEstimateRuns, planEstimateRefresh,
// internal/cli/plan_status.go): the summary carries the table that command
// used to build, so a reader that read one and then the other must not see two
// spellings of one history.
const (
	// estimateRuns bounds the runs the table is drawn from: the newest ones the
	// index holds, which is what a distribution over tiers and modes needs.
	estimateRuns = 5000
	// estimateRefresh is how old the table may be before the fold pays for it
	// again. A table moves as runs finish rather than as a reader looks, and
	// reading thousands of documents on every pass of a fifteen-minute sweep
	// would cost the fold its own window for a figure nobody re-reads.
	estimateRefresh = 6 * time.Hour
)

// Store is the Firestore surface the index is read and written through. It is
// *firestore.Client's own three methods.
type Store interface {
	Get(ctx context.Context, coll, id string) (*firestore.Doc, error)
	Patch(ctx context.Context, coll, id string, fields map[string]any, opts ...firestore.PatchOption) (*firestore.Doc, error)
	RunQuery(ctx context.Context, q firestore.Query) ([]firestore.Doc, error)
}

// Fold is one sweep's fold (RI4, D3201): the runs that changed since the
// index's stamp, read out of the runs bucket and written into the index.
//
// It is the index's only incremental writer, and it decides as little as it
// can. A run's own three records -- task.json, launch.json, result.json -- are
// folded into one document, every field of it taken from those records, and
// the summary is moved by that run's own figures: nothing is counted twice,
// nothing is invented, and **no run object is written**. The history account
// reads the runs bucket and never writes it, which is why the index can be
// built by an account that holds no write on it at all.
//
// Three things are the fold's own, and each is stated where it is done: the
// writer lease it takes (lease.go), the discovery that finds what changed
// (outcomes/ and agents/, D3202), and the stamp, which moves only when the
// whole fold landed (Summary.FoldedThrough).
type Fold struct {
	DB     *rtdb.Client
	FS     Store
	Bucket *blob.Bucket
	// Holder is who takes the writer lease: the execution's name (F39).
	Holder string
	// Now is the wall clock the summary's own stamps are written from; nil is
	// time.Now. The lease's stamps are the database's clock, never this one.
	Now func() time.Time
	// Warn receives what could not be folded; nil discards it.
	Warn func(string)
}

// FoldReport is what one fold did.
type FoldReport struct {
	// Held is true when another writer's claim was fresh: nothing was read and
	// nothing was written, and Holder names who holds it.
	Held   bool
	Holder string
	// Folded is how many runs were written into the index, and Unreadable how
	// many changed runs carried no record at all to write (a run whose objects
	// are gone, which is a warning rather than a failure).
	Folded     int
	Unreadable int
	// Through is the stamp the summary now carries: the moment the fold read
	// the discovery from, which is where the next fold resumes.
	Through time.Time
	// Estimate says whether the summary's estimate table was rebuilt.
	Estimate bool
}

func (f *Fold) now() time.Time {
	if f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

func (f *Fold) warnf(format string, a ...any) {
	if f.Warn != nil {
		f.Warn(fmt.Sprintf(format, a...))
	}
}

// Fold folds what changed since the index's stamp, or reports the writer that
// is in the way.
//
// The order of the writes is the whole of its failure story. Every run is read
// and written before the summary is touched, and the summary carries the stamp
// as well as the deltas, so a fold that fails leaves the stamp where it was
// and the next sweep reads the same window again: the runs already written
// fold to no change, and the runs after the failure are folded for the first
// time. What that leaves, if the summary write itself is the failure, is a
// summary short by the deltas of the runs written in that pass -- the entries
// are the truth and the summary is a sum over them, and a reindex (RI5)
// recomputes it from the documents, which is the one thing that repairs it.
func (f *Fold) Fold(ctx context.Context) (FoldReport, error) {
	var rep FoldReport
	if f.DB == nil || f.FS == nil || f.Bucket == nil {
		return rep, errors.New("runindex: the fold needs the budget database, a Firestore client and the runs bucket")
	}
	lease, err := NewLease(f.DB, f.Holder, f.DB.ServerNow)
	if err != nil {
		return rep, err
	}
	claim, held, err := lease.Acquire(ctx)
	if err != nil {
		return rep, err
	}
	if !held {
		rep.Held, rep.Holder = true, claim.Holder
		return rep, nil
	}
	defer func() {
		if err := lease.Release(ctx, claim.At); err != nil {
			f.warnf("the index lease could not be released: %v", err)
		}
	}()

	sum, found, err := summaryDoc(ctx, f.FS)
	if err != nil {
		return rep, err
	}
	// The stamp is the moment before the discovery read and not after the
	// fold: a run that changed while the fold was reading is then still after
	// the stamp, and the next sweep finds it. A stamp taken at the end would
	// step over exactly that run, and nothing would ever fold it.
	start := f.now()
	changed, err := f.changed(ctx, sum.FoldedThrough)
	if err != nil {
		return rep, err
	}
	if sum.States == nil {
		sum.States = map[string]int64{}
	}
	for _, s := range runstore.Statuses {
		if _, ok := sum.States[string(s)]; !ok {
			sum.States[string(s)] = 0
		}
	}

	for _, k := range changed {
		entry, err := f.read(ctx, k)
		if err != nil {
			return rep, err
		}
		if entry == nil {
			rep.Unreadable++
			continue
		}
		old, err := f.stored(ctx, k.run)
		if err != nil {
			return rep, err
		}
		move(&sum, old, entry)
		if err := f.write(ctx, entry); err != nil {
			return rep, err
		}
		rep.Folded++
	}

	fields := map[string]any{
		"version":               int64(Version),
		"updated_at":            f.now(),
		"folded_through":        start,
		"runs":                  sum.Runs,
		"states":                sum.States,
		"spent_micros":          sum.SpentMicros,
		"notional_micros":       sum.NotionalMicros,
		"compute_runs":          sum.ComputeRuns,
		"compute_not_estimated": sum.ComputeNotEstimated,
	}
	// complete is seeded once and never moved by a fold: a walk reaching the
	// end of history is what sets it true (RI5, D3215) and a stamp gone stale
	// is what sets it false (the rollover, RI4). Writing back the value this
	// fold read would be a read-modify-write of a field two other writers own,
	// and the one it would lose is the rollover's.
	if !found {
		fields["complete"] = false
	}
	var clear []string
	if sum.ComputeMicros != nil {
		fields["compute_micros"] = *sum.ComputeMicros
	} else {
		clear = append(clear, "compute_micros")
	}
	if stale(sum.Estimate, f.now()) {
		table, err := f.estimate(ctx, f.now())
		if err != nil {
			// A table that cannot be read is not a reason to lose the fold: the
			// counts and the stamp are what a reader of the summary needs, and
			// the table is a distribution that moves slowly.
			f.warnf("the estimate table could not be rebuilt: %v (the summary keeps the one it has)", err)
		} else {
			fields["estimate"] = table
			rep.Estimate = true
		}
	}
	opts := []firestore.PatchOption{}
	if len(clear) > 0 {
		opts = append(opts, firestore.Clear(clear...))
	}
	// The summary's own fields go through the same encoder the entries do, so
	// the estimate table is a map of the service's kinds and not a Go value
	// the client cannot hold.
	encoded, err := docFields(fields)
	if err != nil {
		return rep, err
	}
	if _, err := f.FS.Patch(ctx, SummaryCollection, SummaryDocument, encoded, opts...); err != nil {
		return rep, err
	}
	rep.Through = start
	return rep, nil
}

// stale reports whether the summary's estimate table is due to be built again.
func stale(t *estimate.Table, now time.Time) bool {
	if t == nil || t.WrittenAt.IsZero() {
		return true
	}
	return now.Sub(t.WrittenAt) >= estimateRefresh
}

// runKey is one run of one repository: what a fold reads and writes.
type runKey struct {
	slug string
	run  string
}

// changed is the discovery (D3202, F9): the runs whose own records the fold
// has to look at again.
//
// The two nodes it reads are the two the run's own writers keep, and neither
// is the index's: `agents/<slug>/<run>` is the heartbeat of a run that is
// alive (AgentEntry.UpdatedAt, the moment the run last wrote), and
// `outcomes/<day>/<slug>/<run>` is what a run writes as it ends. A run is a
// candidate when its heartbeat is at or after the stamp, or when it has an
// outcome under a day at or after the stamp's own day -- at or after and not
// after, because a stamp taken at a moment has already been read past, and a
// run updated in the same millisecond as the stamp must not fall between two
// folds and be lost.
//
// Both are read whole, and that is what bounds the walk the fold can do
// without a stamp: the days it sees are the days the rollover has not pruned
// yet (PruneAfter, 8 days), and a first fold over an installation whose index
// is empty therefore reads a week of outcomes rather than all of history --
// which is the reindex's job (RI5) and not the sweep's.
func (f *Fold) changed(ctx context.Context, through *time.Time) ([]runKey, error) {
	seen := map[runKey]bool{}
	add := func(slug, run string) {
		s, err := budget.Unkey(slug)
		if err != nil {
			f.warnf("agents or outcomes carries the repository key %q, which is not a key: skipped", slug)
			return
		}
		r, err := budget.Unkey(run)
		if err != nil {
			f.warnf("%s carries the run key %q, which is not a key: skipped", s, run)
			return
		}
		if s != "" && r != "" {
			seen[runKey{slug: s, run: r}] = true
		}
	}
	var agents map[string]map[string]budget.AgentEntry
	if _, err := f.DB.Get(ctx, "agents", &agents); err != nil {
		return nil, err
	}
	for slug, runs := range agents {
		for run, e := range runs {
			if through == nil || e.UpdatedAt >= through.UnixMilli() {
				add(slug, run)
			}
		}
	}
	var outcomes map[string]map[string]map[string]budget.Outcome
	if _, err := f.DB.Get(ctx, "outcomes", &outcomes); err != nil {
		return nil, err
	}
	from := int64(math.MinInt64)
	if through != nil {
		from = budget.Day(*through)
	}
	for day, slugs := range outcomes {
		d, err := strconv.ParseInt(day, 10, 64)
		if err != nil {
			f.warnf("outcomes/%s is not a day key: skipped", day)
			continue
		}
		if d < from {
			continue
		}
		for slug, runs := range slugs {
			for run := range runs {
				add(slug, run)
			}
		}
	}
	out := make([]runKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].slug != out[j].slug {
			return out[i].slug < out[j].slug
		}
		return out[i].run < out[j].run
	})
	return out, nil
}

// read reads one run's own records and folds them into an entry, or answers
// nil for a run there is nothing left of.
//
// A record that cannot be read as one -- absent, corrupt, past the size cap --
// is not a failure of the fold: the entry is written from what is there, which
// is the same answer contract-index.md gives a reader ("a run whose result.json
// cannot be read carries no record: the entry is still written, because the run
// exists and its task and launch do"). A read that failed for another reason --
// the bucket unreachable, the account refused -- is a failure, and the fold
// stops rather than writing an entry that says a run has no task when nobody
// could tell.
func (f *Fold) read(ctx context.Context, k runKey) (*Entry, error) {
	st := runstore.Open(f.Bucket, k.slug, k.run)
	spec, err := st.ReadTask(ctx)
	if err != nil {
		if !readable(err) {
			return nil, err
		}
		f.warnUnreadable(k, "task.json", err)
		spec = nil
	}
	launch, err := st.ReadLaunch(ctx)
	if err != nil {
		if !readable(err) {
			return nil, err
		}
		f.warnUnreadable(k, "launch.json", err)
		launch = nil
	}
	rec, err := st.ReadRecord(ctx)
	if err != nil {
		if !readable(err) {
			return nil, err
		}
		f.warnUnreadable(k, "result.json", err)
		rec = nil
	}
	if spec == nil && launch == nil && rec == nil {
		f.warnf("%s/%s: no task, launch or record to fold: skipped", k.slug, k.run)
		return nil, nil
	}
	// The task-derived fields are the row's own, joined the way runview.Join
	// joins them (contract-index.md): a reader that builds a row from this
	// entry gets the row it would have built from the bucket, and the rules
	// that decide one -- which pull request a run carries, which account it
	// used, which plan task may be shown -- stay in the one place they live.
	// The prices are left out: the row's cost is not a field of the entry,
	// which carries the record's own cost block whole.
	row := runview.Join(runview.Input{Slug: k.slug, RunID: k.run, Task: spec, Launch: launch, Record: rec}, nil, f.now())
	e := &Entry{
		RunID:        k.run,
		Slug:         k.slug,
		Repo:         row.Repo,
		UpdatedAt:    updatedAt(rec, launch, k.run),
		Status:       statusOf(rec),
		PlanTask:     row.PlanTask,
		PR:           row.PR,
		RequestedBy:  row.RequestedBy,
		LaunchedBy:   row.LaunchedBy,
		Batch:        row.Batch,
		Tier:         tierOf(rec),
		Rigor:        rigorOf(rec),
		StartedAt:    startedAt(rec),
		FinishedAt:   finishedAt(rec),
		Workflow:     row.Workflow,
		Title:        row.Title,
		Recipe:       row.Recipe,
		OAuthAccount: row.OAuthAccount,
		PreviousRun:  row.PreviousRun,
		FollowUp:     row.FollowUp,
		FollowUpOf:   row.FollowUpOf,
		Launch:       launch,
		Record:       rec,
	}
	if e.Repo == "" {
		// A run whose task nothing recorded is still a run of its repository:
		// the slug is what the bucket holds it under, and a row with no name
		// at all is worse than one named after its storage key.
		e.Repo = k.slug
	}
	return e, nil
}

// warnUnreadable says an object the fold read is not one it can use. An object
// that is simply not there is not a warning: a run in flight has no
// result.json and a run just launched has no record yet, so every live run of
// the installation would warn on every sweep. A run with no objects at all is
// caught by the caller, and that is the one worth saying.
func (f *Fold) warnUnreadable(k runKey, name string, err error) {
	if errors.Is(err, runstore.ErrNotFound) {
		return
	}
	f.warnf("%s/%s: %s could not be read (%v): the entry carries no %s", k.slug, k.run, name, err, strings.TrimSuffix(name, ".json"))
}

// readable reports whether a read failed because of what is in the bucket
// rather than because the bucket could not be read: an absent object, one that
// is not the JSON it claims to be, and one past the size cap.
func readable(err error) bool {
	return errors.Is(err, runstore.ErrNotFound) || errors.Is(err, runstore.ErrCorrupt) || errors.Is(err, runstore.ErrTooLarge)
}

// updatedAt is the entry's sort key: the moment the run last changed, as its
// own records carry it (contract-index.md). A run whose records carry no
// moment at all -- none of the three readable -- is stamped with the moment
// its id was minted (task.NewRunID), because the index is ordered by this
// field and an entry with none sorts nowhere.
func updatedAt(rec *runstore.Record, launch *runstore.Launch, run string) time.Time {
	if rec != nil {
		if rec.FinishedAt != nil && !rec.FinishedAt.IsZero() {
			return rec.FinishedAt.UTC()
		}
		if !rec.StartedAt.IsZero() {
			return rec.StartedAt.UTC()
		}
	}
	if launch != nil && !launch.LaunchedAt.IsZero() {
		return launch.LaunchedAt.UTC()
	}
	if t, err := runstore.RunTime(run); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func statusOf(rec *runstore.Record) runstore.Status {
	if rec == nil {
		return ""
	}
	return rec.Status
}

func startedAt(rec *runstore.Record) *time.Time {
	if rec == nil || rec.StartedAt.IsZero() {
		return nil
	}
	at := rec.StartedAt.UTC()
	return &at
}

func finishedAt(rec *runstore.Record) *time.Time {
	if rec == nil || rec.FinishedAt == nil || rec.FinishedAt.IsZero() {
		return nil
	}
	at := rec.FinishedAt.UTC()
	return &at
}

func tierOf(rec *runstore.Record) tier.Tier {
	if rec == nil || rec.Tier == nil {
		return ""
	}
	return rec.Tier.Tier
}

func rigorOf(rec *runstore.Record) rigor.Rigor {
	if rec == nil {
		return ""
	}
	return rec.Rigor.LevelOf()
}

// stored is the entry the index holds for a run, or nil when it holds none.
func (f *Fold) stored(ctx context.Context, run string) (*Entry, error) {
	doc, err := f.FS.Get(ctx, EntriesCollection, run)
	if errors.Is(err, firestore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e, err := entryFrom(doc)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// write upserts one run's document.
//
// The mask is the document's own field list and not the keys of the fields
// map: an entry that no longer carries a field -- a record whose status went
// from running to none, a run whose task is no longer readable -- must not
// leave the last write's value standing, because a reader cannot tell a stale
// value from a fresh one.
func (f *Fold) write(ctx context.Context, e *Entry) error {
	fields, err := docFields(*e)
	if err != nil {
		return fmt.Errorf("runindex: %s/%s: %w", e.Slug, e.RunID, err)
	}
	if _, err := firestore.EncodeFields(fields); err != nil {
		// A record this release cannot encode (a figure no double can hold) is
		// one run's problem: the fold writes the rest and says so, rather than
		// failing the pass and the stamp with it.
		f.warnf("%s/%s: the entry cannot be written (%v): skipped", e.Slug, e.RunID, err)
		return nil
	}
	var clear []string
	for _, name := range docFieldNames(reflect.TypeOf(Entry{})) {
		if _, ok := fields[name]; !ok {
			clear = append(clear, name)
		}
	}
	opts := []firestore.PatchOption{}
	if len(clear) > 0 {
		opts = append(opts, firestore.Clear(clear...))
	}
	if _, err := f.FS.Patch(ctx, EntriesCollection, e.RunID, fields, opts...); err != nil {
		return fmt.Errorf("runindex: writing %s/%s: %w", e.Slug, e.RunID, err)
	}
	return nil
}

// runFigures is one run's own contribution to the summary: the two halves of
// its model figure in micro-dollars, and its compute.
type runFigures struct {
	spent, notional           int64
	compute                   int64
	computeRuns, notEstimated int64
}

// figures reads a run's contribution off its own record.
//
// The model figure is split by the cost block's own two methods and never by
// its basis (P12, D140): a mixed run's basis is api-list while part of its
// figure is the subscription's, and reading the basis would call that part
// money. Compute counts as known only where the record says it was priced,
// which is the rule the rollover's own facts take: "not estimated" is not
// "free", and a run that carries no figure at all is counted as carrying none.
func figures(rec *runstore.Record) runFigures {
	var g runFigures
	if rec == nil || rec.Cost == nil {
		g.notEstimated = 1
		return g
	}
	g.spent = micros(rec.Cost.ModelMoneyUSD())
	g.notional = micros(rec.Cost.ModelSubscriptionUSD())
	if rec.Cost.ComputeEstimated {
		g.compute, g.computeRuns = micros(rec.Cost.ComputeUSD), 1
	} else {
		g.notEstimated = 1
	}
	return g
}

// micros is a dollar figure in the unit the summary's money is summed in.
// A figure no integer can hold (an infinity, a NaN) is no figure: it counts
// as nothing rather than as a broken total.
func micros(usd float64) int64 {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || math.Abs(usd) > math.MaxInt64/1e6 {
		return 0
	}
	return int64(math.Round(usd * 1e6))
}

// move applies one run's state change to the summary: the entry written over
// the one the index held, or the first one for a run it did not hold.
func move(sum *Summary, old *Entry, new *Entry) {
	if old == nil {
		sum.Runs++
	} else if old.Status != "" {
		drop(sum, old.Status)
	}
	if new.Status != "" {
		sum.States[string(new.Status)]++
	}
	var before runFigures
	if old != nil {
		before = figures(old.Record)
	}
	after := figures(new.Record)
	sum.SpentMicros += after.spent - before.spent
	sum.NotionalMicros += after.notional - before.notional
	sum.ComputeRuns += after.computeRuns - before.computeRuns
	sum.ComputeNotEstimated += after.notEstimated - before.notEstimated
	total := after.compute - before.compute
	if sum.ComputeMicros != nil {
		total += *sum.ComputeMicros
	}
	// "Absent when no run's compute is known" is the field's own rule: a run
	// whose compute was priced at nothing still has one, and a summary whose
	// runs have none carries none.
	if sum.ComputeRuns > 0 {
		sum.ComputeMicros = &total
	} else {
		sum.ComputeMicros = nil
	}
}

// drop takes one run out of a state's count. A count never goes below zero:
// a figure below zero is not a count, and the summary's figures are written
// whole rather than as increments, so a delta that would go under is a
// document that disagrees with the entries -- which a reindex repairs, and a
// negative count would only hide.
func drop(sum *Summary, s runstore.Status) {
	if n := sum.States[string(s)]; n > 0 {
		sum.States[string(s)] = n - 1
	}
}

// summaryDoc reads the summary document and whether it is there at all.
func summaryDoc(ctx context.Context, st Store) (Summary, bool, error) {
	doc, err := st.Get(ctx, SummaryCollection, SummaryDocument)
	if errors.Is(err, firestore.ErrNotFound) {
		return Summary{Version: Version, States: map[string]int64{}}, false, nil
	}
	if err != nil {
		return Summary{}, false, err
	}
	var s Summary
	if err := fromDoc(doc, &s); err != nil {
		return Summary{}, false, err
	}
	return s, true, nil
}

// ReadSummary reads the installation's summary. An index that has never been
// folded reads as the zero summary -- version, no stamp, no counts -- and
// never as an error: "the index is not there" is a state its readers have to
// handle anyway, and RI6's doctor line and the rollover's prune guard both ask
// this question.
func ReadSummary(ctx context.Context, st Store) (Summary, error) {
	s, _, err := summaryDoc(ctx, st)
	return s, err
}

// Index is the run index as the budget rollover asks about it (budget.Index):
// the fold's stamp, and the completeness latch the rollover sets when the
// stamp has fallen behind. It is a type of its own rather than methods on the
// Fold, because the rollover asks these two questions without folding
// anything.
type Index struct{ Store Store }

// FoldedThrough is the summary's own stamp, and false for an index that has
// never been folded.
func (i Index) FoldedThrough(ctx context.Context) (time.Time, bool, error) {
	s, _, err := summaryDoc(ctx, i.Store)
	if err != nil {
		return time.Time{}, false, err
	}
	if s.FoldedThrough == nil {
		return time.Time{}, false, nil
	}
	return s.FoldedThrough.UTC(), true, nil
}

// Incomplete latches the summary's completeness off.
func (i Index) Incomplete(ctx context.Context, now time.Time) error {
	return MarkIncomplete(ctx, i.Store, now)
}

// MarkIncomplete latches the summary's completeness off, so that every reader
// that prints a short list says why and `fugaro doctor` names the reindex
// (RI6). It is the rollover's write (RI4): a stamp that has fallen further
// behind than the days the database keeps is an index whose fold has stopped,
// and no day may be pruned against it. A summary that is not there at all is
// nothing to mark.
func MarkIncomplete(ctx context.Context, st Store, now time.Time) error {
	_, err := st.Patch(ctx, SummaryCollection, SummaryDocument,
		map[string]any{"complete": false, "updated_at": now.UTC()}, firestore.MustExist())
	if errors.Is(err, firestore.ErrNotFound) {
		return nil
	}
	return err
}

// The two Go types the document's shape turns on: a time is a Firestore
// timestamp, and a type carrying its own JSON shape is encoded as that shape.
var (
	timeType      = reflect.TypeOf(time.Time{})
	marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
)

// docFields renders a Go value as a document's fields: the names its json tags
// declare, and the values as the kinds the service holds rather than as JSON
// holds them -- a time as a timestamp, an integer as an integer, a double as a
// double. That is the difference between a document the index can be queried
// and read by (contract-index.md: `record.cost.cache_read_tokens` is an
// integer, `updated_at` a timestamp) and a JSON blob that happens to parse.
//
// A type that carries its own JSON shape is encoded as that shape and not as
// its fields (runstore.RigorRecord writes its level under two keys, and
// PolicyEffective omits an allowed_models that is unset): the document is the
// record the run wrote, and a second spelling of it here would be a second
// answer to what the record says.
func docFields(v any) (map[string]any, error) {
	out, err := docValue(reflect.ValueOf(v))
	if err != nil {
		return nil, err
	}
	fields, ok := out.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("runindex: a %T is not a document", v)
	}
	return fields, nil
}

func docValue(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if v.Type() == timeType {
		return v.Interface().(time.Time).UTC(), nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
	}
	if data, ok, err := asJSON(v); ok {
		return data, err
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return docValue(v.Elem())
	case reflect.Struct:
		out := make(map[string]any, v.NumField())
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, omitempty := jsonTag(f)
			if name == "" || name == "-" {
				continue
			}
			fv := v.Field(i)
			if omitempty && emptyValue(fv) {
				continue
			}
			if isNil(fv) {
				continue
			}
			enc, err := docValue(fv)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			out[name] = enc
		}
		return out, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("runindex: a %s has keys that are not strings", v.Type())
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			enc, err := docValue(iter.Value())
			if err != nil {
				return nil, err
			}
			out[iter.Key().String()] = enc
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			enc, err := docValue(v.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, enc)
		}
		return out, nil
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() > math.MaxInt64 {
			return nil, fmt.Errorf("runindex: %d does not fit a Firestore integer", v.Uint())
		}
		return int64(v.Uint()), nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("runindex: %v is not a number Firestore holds", f)
		}
		return f, nil
	}
	return nil, fmt.Errorf("runindex: a %s is not a value a document holds", v.Type())
}

// asJSON encodes a value whose type carries its own JSON shape, and answers
// false for one that does not. The JSON is read back with numbers kept whole
// (jsonNative), so a type's integer stays an integer in the document.
func asJSON(v reflect.Value) (any, bool, error) {
	t := v.Type()
	switch {
	case t.Implements(marshalerType):
	case v.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(marshalerType):
	default:
		return nil, false, nil
	}
	data, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, true, fmt.Errorf("runindex: encoding a %s as its JSON shape: %w", t, err)
	}
	out, err := jsonNative(data)
	return out, true, err
}

// jsonNative turns a JSON document into the values Firestore holds: a number
// with no fraction stays an integer, a double stays a double, and objects and
// arrays are walked.
func jsonNative(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return native(v)
}

func native(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			n, err := native(e)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			n, err := native(e)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	default:
		return x, nil
	}
}

// jsonTag is a field's json tag: the name it is written under and whether it
// is left out when it is empty. A field with no tag is its own name, as
// encoding/json reads it.
func jsonTag(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "-", false
	}
	name, rest, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name, strings.Contains(rest, "omitempty")
}

// isNil reports a value with nothing behind it: a pointer, an interface, a map
// or a slice with no value. Such a field is left out of the document rather
// than written as a null, which reads back the same way and costs a field of
// every record.
func isNil(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// emptyValue is encoding/json's own rule for omitempty, so a field the record
// leaves out of its JSON is left out of the document too.
func emptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}

// docFieldNames is every name a document of this shape may carry, so the write
// can mask them all: a field the entry no longer carries is then deleted
// rather than left as the last write's value, which a reader cannot tell from
// a fresh one.
func docFieldNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if name, _ := jsonTag(f); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// fromDoc decodes a document back into the Go shape the writer encoded it
// from. It is the reader's own path -- the fields as the client hands them
// over, then JSON into the struct -- and not a second decoder: a document the
// index wrote has to read back as the record it carries, whoever reads it.
func fromDoc(doc *firestore.Doc, out any) error {
	data, err := json.Marshal(doc.Fields)
	if err != nil {
		return fmt.Errorf("runindex: %s: %w", doc.ID, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("runindex: %s is not a document of this shape: %w", doc.ID, err)
	}
	return nil
}

func entryFrom(doc *firestore.Doc) (Entry, error) {
	var e Entry
	if err := fromDoc(doc, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// estimate rebuilds the summary's estimate table from the index itself: the
// newest documents it holds, whose records are the samples estimate.Build
// groups by tier and rigor (F15). It reads the documents rather than the
// bucket because the index is the bounded read this table exists for, and it
// is the same table `plan status` used to build from the bucket.
func (f *Fold) estimate(ctx context.Context, now time.Time) (*estimate.Table, error) {
	docs, err := f.FS.RunQuery(ctx, firestore.Query{
		Collection: EntriesCollection,
		OrderBy:    []firestore.Order{{Field: "updated_at", Direction: "DESCENDING"}},
		Limit:      estimateRuns,
	})
	if err != nil {
		return nil, err
	}
	records := make([]runstore.Record, 0, len(docs))
	var skipped int
	for _, doc := range docs {
		raw, ok := doc.Fields["record"]
		if !ok || raw == nil {
			continue // a run that never finalized is no sample
		}
		data, err := json.Marshal(raw)
		if err != nil {
			skipped++
			continue
		}
		var rec runstore.Record
		if err := json.Unmarshal(data, &rec); err != nil {
			skipped++
			continue
		}
		records = append(records, rec)
	}
	if skipped > 0 {
		f.warnf("%d indexed records could not be read back for the estimate table", skipped)
	}
	return estimate.Build(records, now), nil
}
