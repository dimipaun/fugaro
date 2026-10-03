package budget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The rollover (design §6.2/§9, plan M9d H3-H5) moves each finished UTC day
// from the Realtime Database into Firestore, one spendDaily document per
// repository, and only then lets old day nodes go. It is the one place that
// deletes spend history, so it is written to lose nothing:
//
//   - A day is written provisionally (final=false) once it has ended and
//     final (final=true) from 00:00 UTC of D+2, when no run can still write
//     into it (the rules allow today and yesterday only). A final document is
//     never rewritten without --day D --force, and a provisional write is
//     conditioned on the document's updateTime, so a slow writer that read a
//     provisional document cannot overwrite a final one a faster writer
//     produced between its read and its write.
//   - A day's nodes are deleted only when the day is older than PruneAfter
//     days, every repository's document is final, a fresh read of each
//     document equals the figures it was derived from, the day's global
//     counter equals the sum of the documents, the nodes still read the same
//     as when the documents were derived, and the day before it is already
//     gone (its late outcomes feed that day's record). The delete is one
//     atomic multi-path update of leaves, under spend/<day>, outcomes/<day>
//     and the lifetime ledgers whose last share is that day; nothing else.
//   - Any error leaves the database untouched for that day.

// Rollover constants.
const (
	// LookbackDays is how many finished days a pass writes (the design's 7).
	LookbackDays = 7
	// PruneAfter is the age in days beyond which a day may leave the database.
	PruneAfter = 8
	// SpendCollection and MetaCollection are the Firestore collections.
	SpendCollection = "spendDaily"
	MetaCollection  = "meta"
	MarkDocument    = "installation"
)

// ErrRefused marks a refusal (a wrong database, a final document in the way, a
// read-back that differs, ...): something a person must look at, exit 1. Other
// errors are backend failures, exit 2.
var ErrRefused = errors.New("refused")

func refusef(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// DocStore is the Firestore client's surface the rollover uses.
type DocStore interface {
	Get(ctx context.Context, coll, id string) (*firestore.Doc, error)
	Patch(ctx context.Context, coll, id string, fields map[string]any, opts ...firestore.PatchOption) (*firestore.Doc, error)
}

// Facts reads what the runs bucket knows about the runs started on each day of
// [from, to]. Repos names the repositories ("owner/name") by slug, for display.
type Facts interface {
	Facts(ctx context.Context, from, to int64) (facts map[int64][]RunFact, repos map[string]string, err error)
}

// Roller is one rollover pass's wiring.
type Roller struct {
	DB      *rtdb.Client
	FS      DocStore
	Facts   Facts
	Project string // the Fugaro project this job belongs to (both marks must name it)
	Now     func() time.Time
	// MaxSkew, when positive, refuses to run if the local clock and the
	// database server's differ by more: finality depends on the clock.
	MaxSkew time.Duration
	// Warn receives what was skipped; nil discards it.
	Warn func(string)
	// ServerNow is the database's time (default: the RTDB client's, from its
	// Date header); tests replace it.
	ServerNow func() (time.Time, bool)
	// StartLimit, when set, stops the pass from starting a new day after that
	// wall-clock time (the job's timeout minus a margin); the report says
	// more days remain and the next pass continues.
	StartLimit time.Time
	// Wall is the wall clock for StartLimit (default time.Now).
	Wall func() time.Time
}

func (r *Roller) wall() time.Time {
	if r.Wall != nil {
		return r.Wall()
	}
	return time.Now()
}

// RolloverOptions selects a single day and/or a forced rewrite.
type RolloverOptions struct {
	Day   *int64 // only this day, any age still in the database
	Force bool   // rewrite final documents; needs Day
}

// DayReport says what happened to one day.
type DayReport struct {
	Day       int64
	Written   int // documents created or changed
	Unchanged int // provisional documents already equal, or final ones kept
	Final     bool
	Pruned    bool
	Note      string // why a day was not pruned, when it matters
	Err       error
}

// RolloverReport is the whole pass.
type RolloverReport struct {
	More        bool // the pass stopped early (time); the next pass continues
	NoFirestore bool // no Firestore database exists yet: nothing was done
	Days        []DayReport
}

// Summary is the one-line total.
func (r RolloverReport) Summary() string {
	if r.NoFirestore {
		return "rollover: no Firestore database (run fugaro init --firebase); nothing moved"
	}
	var w, u, p, f int
	for _, d := range r.Days {
		w, u = w+d.Written, u+d.Unchanged
		if d.Pruned {
			p++
		}
		if d.Err != nil {
			f++
		}
	}
	more := ""
	if r.More {
		more = "; more days remain, the next pass continues"
	}
	return fmt.Sprintf("rollover: %d days, %d documents written, %d unchanged, %d days pruned, %d failed%s", len(r.Days), w, u, p, f, more)
}

// Line is the day's summary line.
func (d DayReport) Line() string {
	s := fmt.Sprintf("rollover: day %s: %d written, %d unchanged", DayDate(d.Day), d.Written, d.Unchanged)
	if d.Final {
		s += ", final"
	} else {
		s += ", provisional"
	}
	if d.Pruned {
		s += ", pruned"
	}
	if d.Note != "" {
		s += " (" + d.Note + ")"
	}
	if d.Err != nil {
		s += ": FAILED: " + d.Err.Error()
	}
	return s
}

func (r *Roller) warnf(format string, a ...any) {
	if r.Warn != nil {
		r.Warn(fmt.Sprintf(format, a...))
	}
}

// dayNode is spend/<day>, as far as the rollover reads it.
type dayNode struct {
	Repos  map[string]json.RawMessage            `json:"repos"`
	Runs   map[string]map[string]json.RawMessage `json:"runs"`
	Global json.RawMessage                       `json:"global"`
}

// Rollover does one pass. The database and the Firestore mark are checked
// before anything is read for writing; a missing Firestore database is not an
// error (history is optional until init creates it) and touches nothing.
func (r *Roller) Rollover(ctx context.Context, opt RolloverOptions) (RolloverReport, error) {
	var rep RolloverReport
	if opt.Force && opt.Day == nil {
		return rep, refusef("--force rewrites final documents and needs --day")
	}
	now := r.Now().UTC()
	today := Day(now)
	if opt.Day != nil && *opt.Day >= today {
		return rep, refusef("day %s has not ended (today is %s)", DayDate(*opt.Day), DayDate(today))
	}

	// The marks: the account can reach other projects' databases.
	var rawMark json.RawMessage
	found, err := r.DB.Get(ctx, PathProject, &rawMark)
	if err != nil {
		return rep, err
	}
	var mark string
	if !found || json.Unmarshal(rawMark, &mark) != nil || mark != r.Project {
		return rep, refusef("the budget database's /fugaro/project does not name project %s; nothing was moved", r.Project)
	}
	if r.MaxSkew > 0 {
		sn := r.ServerNow
		if sn == nil {
			sn = r.DB.ServerNow
		}
		srv, ok := sn()
		if !ok {
			return rep, refusef("the database's clock was not seen (no Date header), so the job's clock cannot be checked; nothing was moved")
		}
		if d := now.Sub(srv); d > r.MaxSkew || d < -r.MaxSkew {
			return rep, refusef("this job's clock differs from the database's by %s; days are finalized by the clock, so nothing was moved", d.Round(time.Second))
		}
		// A day is final from 00:00 UTC of D+2 by both clocks: use the
		// earlier, so a job clock running ahead cannot finalize early.
		if srv.Before(now) {
			now = srv.UTC()
			today = Day(now)
		}
	}
	if opt.Day != nil && *opt.Day >= today {
		return rep, refusef("day %s has not ended by the database's clock (today is %s)", DayDate(*opt.Day), DayDate(today))
	}

	doc, err := r.FS.Get(ctx, MetaCollection, MarkDocument)
	switch {
	case errors.Is(err, firestore.ErrNoDatabase):
		rep.NoFirestore = true
		return rep, nil
	case errors.Is(err, firestore.ErrNotFound):
		return rep, refusef("the Firestore database has no %s/%s mark document; run fugaro init --firebase. Nothing was moved", MetaCollection, MarkDocument)
	case err != nil:
		return rep, err
	}
	if p, _ := doc.Fields["project"].(string); p != r.Project {
		return rep, refusef("the Firestore database's mark does not name project %s; nothing was moved", r.Project)
	}

	// Everything the pass reads from the database, once.
	var caps struct {
		Defaults *DefaultCaps        `json:"defaults"`
		Repos    map[string]RepoCaps `json:"repos"`
	}
	if _, err := r.DB.Get(ctx, "config/caps", &caps); err != nil {
		return rep, err
	}
	snap, err := r.snapshot(ctx)
	if err != nil {
		return rep, err
	}
	spend, outcomes, runs, last, present := snap.spend, snap.outcomes, snap.runs, snap.last, snap.present

	// The days to handle, oldest first.
	var days []int64
	if opt.Day != nil {
		if present[*opt.Day] {
			days = []int64{*opt.Day}
		}
	} else {
		for d := range present {
			if d < today {
				days = append(days, d)
			}
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	if len(days) == 0 {
		return rep, nil
	}

	var errs []error
	for _, d := range days {
		if !r.StartLimit.IsZero() && r.wall().After(r.StartLimit) {
			rep.More = true // the next pass continues with the days still left
			break
		}
		dr := DayReport{Day: d, Final: today >= d+2}
		// Each day's facts are read just before the day is written, so a pass
		// that is cut off has already finished the days before it. A run lasts
		// at most a day: the outcome nodes of d may hold a run that started on
		// d-1, whose facts say it is not d's.
		facts, repoNames, err := r.Facts.Facts(ctx, d-1, d)
		if err != nil {
			err = fmt.Errorf("reading the runs bucket: %w", err)
			dr.Err = err
			errs = append(errs, fmt.Errorf("day %s: %w", DayDate(d), err))
			rep.Days = append(rep.Days, dr)
			break // the later days need the same bucket
		}
		dayFacts := append(append([]RunFact(nil), facts[d-1]...), facts[d]...)
		in, err := r.input(d, spend, outcomes, runs, last, caps.Defaults, caps.Repos, dayFacts, repoNames)
		var recs []DayRecord
		if err == nil {
			recs = Rollup(in)
			err = r.writeDay(ctx, &dr, recs, now, dr.Final, opt.Force)
		}
		if err == nil && d < today-PruneAfter && dr.Final {
			fresh := func() ([]DayRecord, *snapshot, error) {
				fs, err := r.snapshot(ctx)
				if err != nil {
					return nil, nil, err
				}
				fin, err := r.input(d, fs.spend, fs.outcomes, fs.runs, fs.last, caps.Defaults, caps.Repos, dayFacts, repoNames)
				if err != nil {
					return nil, nil, err
				}
				return Rollup(fin), fs, nil
			}
			err = r.prune(ctx, &dr, d, today, fresh)
			if err == nil && dr.Pruned {
				delete(present, d)
			}
		}
		if err != nil {
			dr.Err = err
			errs = append(errs, fmt.Errorf("day %s: %w", DayDate(d), err))
		}
		rep.Days = append(rep.Days, dr)
	}
	return rep, errors.Join(errs...)
}

// snapshot is what the rollover reads from the database.
type snapshot struct {
	spend, outcomes map[string]json.RawMessage
	runs, agents    map[string]map[string]json.RawMessage
	last            map[string]map[string]int64 // each run's latest share day
	ledgerOK        bool                        // false: some day node is unreadable, remove no ledgers
	present         map[int64]bool              // days with a spend or outcomes node
}

func (r *Roller) snapshot(ctx context.Context) (*snapshot, error) {
	s := &snapshot{last: map[string]map[string]int64{}, ledgerOK: true, present: map[int64]bool{}}
	for _, rd := range []struct {
		path string
		out  any
	}{{"spend", &s.spend}, {"outcomes", &s.outcomes}, {"runs", &s.runs}, {"agents", &s.agents}} {
		if _, err := r.DB.Get(ctx, rd.path, rd.out); err != nil {
			return nil, err
		}
	}
	for _, m := range []map[string]json.RawMessage{s.spend, s.outcomes} {
		for k := range m {
			if d, ok := parseDayKey(k); ok {
				s.present[d] = true
			}
		}
	}
	for k, raw := range s.spend {
		d, ok := parseDayKey(k)
		if !ok {
			continue
		}
		var n dayNode
		if json.Unmarshal(raw, &n) != nil {
			s.ledgerOK = false
			r.warnf("spend/%s is unreadable; no lifetime ledgers are removed this pass", k)
			continue
		}
		for slug, rr := range n.Runs {
			for run := range rr {
				if s.last[slug] == nil {
					s.last[slug] = map[string]int64{}
				}
				s.last[slug][run] = max(s.last[slug][run], d)
			}
		}
	}
	return s, nil
}

func parseDayKey(k string) (int64, bool) {
	d, err := strconv.ParseInt(k, 10, 64)
	if err != nil || d < 0 || DayKey(d) != k {
		return 0, false
	}
	return d, true
}

// input assembles Rollup's input for day d from the database snapshot.
func (r *Roller) input(d int64, spend, outcomes map[string]json.RawMessage, runs map[string]map[string]json.RawMessage,
	last map[string]map[string]int64, defaults *DefaultCaps, repoCaps map[string]RepoCaps, facts []RunFact, repoNames map[string]string) (RollupInput, error) {
	in := RollupInput{Day: d, Repos: map[string]RepoDay{}, Shares: map[string]map[string]RunLedger{},
		Ledgers: map[string]map[string]LifetimeRun{}, Runs: facts}
	if raw, ok := spend[DayKey(d)]; ok {
		var n dayNode
		if err := json.Unmarshal(raw, &n); err != nil {
			return in, refusef("spend/%s is not readable: %v", DayKey(d), err)
		}
		for slug, rr := range n.Repos {
			var c Counters
			if err := json.Unmarshal(rr, &c); err != nil {
				return in, refusef("spend/%s/repos/%s is not readable: %v", DayKey(d), slug, err)
			}
			raw := unkeyOr(slug)
			rd := RepoDay{Counters: c, Repo: displayRepo(repoCaps[raw].Repo, repoNames[raw])}
			caps := Caps{Defaults: defaults}
			if rc, ok := repoCaps[raw]; ok {
				caps.Repo = &rc
			}
			if v, ok := caps.RepoDaily(); ok {
				rd.CapDaily = &v
			}
			in.Repos[slug] = rd
		}
		for slug, rr := range n.Runs {
			for run, l := range rr {
				var led RunLedger
				if err := json.Unmarshal(l, &led); err != nil {
					return in, refusef("spend/%s/runs/%s/%s is not readable: %v", DayKey(d), slug, run, err)
				}
				if in.Shares[slug] == nil {
					in.Shares[slug] = map[string]RunLedger{}
				}
				in.Shares[slug][run] = led
			}
		}
	}
	for slug, rr := range last {
		for run, ld := range rr {
			if ld != d {
				continue
			}
			raw, ok := runs[slug][run]
			if !ok {
				continue
			}
			var led RunLedger
			if err := json.Unmarshal(raw, &led); err != nil {
				return in, refusef("runs/%s/%s is not readable: %v", slug, run, err)
			}
			if in.Ledgers[slug] == nil {
				in.Ledgers[slug] = map[string]LifetimeRun{}
			}
			in.Ledgers[slug][run] = LifetimeRun{RunLedger: led, LastShareDay: ld}
		}
	}
	var err error
	if in.Outcomes, err = outcomesOf(outcomes, d); err != nil {
		return in, err
	}
	if in.NextDay, err = outcomesOf(outcomes, d+1); err != nil {
		return in, err
	}
	return in, nil
}

func displayRepo(names ...string) string {
	for _, n := range names {
		n = strings.TrimSpace(strings.ToValidUTF8(n, "?"))
		if n != "" {
			if len(n) > maxPersonBytes {
				n = n[:maxPersonBytes]
			}
			return n
		}
	}
	return ""
}

func outcomesOf(outcomes map[string]json.RawMessage, d int64) (map[string]map[string]Outcome, error) {
	raw, ok := outcomes[DayKey(d)]
	if !ok {
		return nil, nil
	}
	var out map[string]map[string]Outcome
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, refusef("outcomes/%s is not readable: %v", DayKey(d), err)
	}
	return out, nil
}

// writeDay writes the day's documents.
func (r *Roller) writeDay(ctx context.Context, dr *DayReport, recs []DayRecord, now time.Time, final, force bool) error {
	var errs []error
	for _, rec := range recs {
		st, err := r.put(ctx, rec, now, final, force)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rec.DocID(), err))
			continue
		}
		if st == putWritten {
			dr.Written++
		} else {
			dr.Unchanged++
		}
	}
	return errors.Join(errs...)
}

type putStatus int

const (
	putWritten putStatus = iota
	putUnchanged
)

const putAttempts = 4

// put writes one document: create-if-absent, or replace conditioned on the
// version read, never replacing a final document (without force) and never
// turning a final document provisional.
func (r *Roller) put(ctx context.Context, rec DayRecord, now time.Time, final, force bool) (putStatus, error) {
	for attempt := 0; attempt < putAttempts; attempt++ {
		var existing *DayRecord
		opts := []firestore.PatchOption{firestore.MustNotExist()}
		doc, err := r.FS.Get(ctx, SpendCollection, rec.DocID())
		switch {
		case errors.Is(err, firestore.ErrNotFound):
		case err != nil:
			return 0, err
		default:
			e, derr := FromFields(doc.Fields)
			if derr != nil || e.Slug != rec.Slug || e.Date != rec.Date {
				if !force {
					return 0, refusef("the stored document is not a day record of this repository and day (%v); --day %s --force replaces it", derr, rec.Date)
				}
			} else {
				existing = &e
			}
			opts = []firestore.PatchOption{firestore.IfUpdateTime(doc.UpdateTime)}
		}
		if existing != nil && existing.Final && !force {
			return putUnchanged, nil
		}
		want := rec
		want.Final = final || (existing != nil && existing.Final)
		want.WrittenAt = now
		switch {
		case !want.Final:
		case existing != nil && !existing.ArchivedAt.IsZero():
			want.ArchivedAt = existing.ArchivedAt
		default:
			want.ArchivedAt = now
		}
		if existing != nil && sameStored(*existing, want) {
			return putUnchanged, nil
		}
		if want.CapDailyMicros == nil {
			opts = append(opts, firestore.Clear("capDailyMicros")) // a cap removed since an earlier write
		}
		_, err = r.FS.Patch(ctx, SpendCollection, rec.DocID(), want.ToFields(), opts...)
		if errors.Is(err, firestore.ErrPrecondition) {
			continue // another pass wrote in between: look again
		}
		if err != nil {
			return 0, err
		}
		return putWritten, nil
	}
	return 0, fmt.Errorf("document %s kept changing under this pass; the next pass retries", rec.DocID())
}

// sameStored reports whether writing want would change nothing but writtenAt.
func sameStored(have, want DayRecord) bool {
	have.WrittenAt, want.WrittenAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(have.ToFields(), want.ToFields())
}

// figuresEqual compares what the database holds now (src, derived like the
// document) with a fresh read of the stored document (got): the whole record
// except what does not come from the database or changes with configuration
// (compute, hours, cap, repository name, timestamps). Outcomes, byPerson,
// overrun and unreconciled are included: a late outcome or share after the
// document was written blocks the prune, because pruning would lose it.
func figuresEqual(src, got DayRecord) bool {
	if !got.Final {
		return false
	}
	for _, r := range []*DayRecord{&src, &got} {
		r.ComputeMicros, r.ComputeEstimatedRuns, r.RunHours = 0, 0, 0
		r.CapDailyMicros, r.Repo = nil, ""
		r.WrittenAt, r.ArchivedAt = time.Time{}, time.Time{}
		r.Final = true
	}
	return reflect.DeepEqual(src.ToFields(), got.ToFields())
}

// prune deletes the day's nodes if every condition holds. A refusal keeps the
// day and says why.
func (r *Roller) prune(ctx context.Context, dr *DayReport, d, today int64,
	fresh func() ([]DayRecord, *snapshot, error)) error {
	keep := func(format string, a ...any) error {
		return refusef("not pruned: "+format, a...)
	}
	// Everything below works from a fresh read of the database, derived the
	// same way as the documents: whatever a late write added since they were
	// written must show up as a difference, because nothing else holds it.
	recs, snap, err := fresh()
	if err != nil {
		return err
	}
	spend, outcomes, runs, last, present, ledgers := snap.spend, snap.outcomes, snap.runs, snap.last, snap.present, snap.ledgerOK
	if !present[d] {
		dr.Pruned = true // another pass removed it
		return nil
	}
	if present[d-1] {
		dr.Note = "waits for the day before"
		return nil
	}
	// Every repository's counters must be explained by a document.
	var sumSpent, sumNotional, sumCalls int64
	for _, rec := range recs {
		sumSpent += int64(rec.SpentMicros)
		sumNotional += int64(rec.NotionalMicros)
		sumCalls += rec.Calls
	}
	var n dayNode
	if raw, ok := spend[DayKey(d)]; ok {
		if err := json.Unmarshal(raw, &n); err != nil {
			return keep("spend/%s is not readable", DayKey(d))
		}
	}
	if len(n.Global) > 0 || len(recs) > 0 {
		var g Counters
		if len(n.Global) > 0 {
			if err := json.Unmarshal(n.Global, &g); err != nil {
				return keep("the global counter is not readable")
			}
		}
		if int64(g.Spent) != sumSpent || int64(g.Notional) != sumNotional || g.Calls != sumCalls {
			return keep("the day's global counter (%d spent, %d notional, %d calls) is not the sum of the repository records (%d, %d, %d)",
				g.Spent, g.Notional, g.Calls, sumSpent, sumNotional, sumCalls)
		}
	}
	for slug := range n.Repos {
		if !slugSafe.MatchString(unkeyOr(slug)) {
			return keep("repository %q cannot have a document", slug)
		}
	}
	// The read-back: a fresh read of every document equals its source.
	for _, rec := range recs {
		doc, err := r.FS.Get(ctx, SpendCollection, rec.DocID())
		if errors.Is(err, firestore.ErrNotFound) {
			return keep("%s is not in Firestore", rec.DocID())
		}
		if err != nil {
			return err
		}
		got, err := FromFields(doc.Fields)
		if err != nil {
			return keep("%s is not readable back: %v", rec.DocID(), err)
		}
		if !figuresEqual(rec, got) {
			return keep("%s read back is not final or does not equal what the database holds now (a late write? check, then --day %s --force)", rec.DocID(), rec.Date)
		}
	}

	// What goes: leaves of this day's nodes and of the ledgers last used today.
	type node struct {
		path string
		raw  json.RawMessage
	}
	nodes := []node{}
	if raw, ok := spend[DayKey(d)]; ok {
		nodes = append(nodes, node{"spend/" + DayKey(d), raw})
	}
	if raw, ok := outcomes[DayKey(d)]; ok {
		nodes = append(nodes, node{"outcomes/" + DayKey(d), raw})
	}
	if ledgers {
		for slug, rr := range last {
			for run, ld := range rr {
				if ld == d {
					if _, live := snap.agents[slug][run]; live {
						continue // still registered: never remove a live run's ledger
					}
					if raw, ok := runs[slug][run]; ok {
						nodes = append(nodes, node{"runs/" + slug + "/" + run, raw})
					}
				}
			}
		}
	}
	updates := map[string]any{}
	for _, nd := range nodes {
		// Nothing may have changed since the documents were derived.
		var cur json.RawMessage
		found, err := r.DB.Get(ctx, nd.path, &cur)
		if err != nil {
			return err
		}
		if !found {
			continue // another pass already removed it
		}
		if !sameJSON(cur, nd.raw) {
			return keep("%s changed while the day was being moved", nd.path)
		}
		if err := leaves(nd.path, nd.raw, updates); err != nil {
			return keep("%v", err)
		}
	}
	for p := range updates {
		if !pruneKeyOK(p, d, today) {
			return keep("internal: refusing to delete %q", p)
		}
	}
	if len(updates) == 0 {
		dr.Pruned = true
		return nil
	}
	if err := r.DB.Patch(ctx, "", updates); err != nil {
		return err
	}
	dr.Pruned = true
	return nil
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// leaves adds a null for every leaf under raw at path.
func leaves(path string, raw json.RawMessage, out map[string]any) error {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%s is not readable", path)
	}
	var walk func(p string, v any)
	walk = func(p string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, c := range m {
				walk(p+"/"+k, c)
			}
			return
		}
		out[p] = nil
	}
	walk(path, v)
	return nil
}

// pruneKeyOK is the last guard before a delete: only leaves below the day's
// own nodes or a lifetime ledger, and only for a day old enough to prune.
func pruneKeyOK(p string, d, today int64) bool {
	if d < 0 || d >= today-PruneAfter {
		return false
	}
	parts := strings.Split(p, "/")
	for _, s := range parts {
		if s == "" || strings.ContainsAny(s, ".$#[]") || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return false
		}
	}
	switch parts[0] {
	case "spend", "outcomes":
		return len(parts) >= 3 && parts[1] == DayKey(d)
	case "runs":
		return len(parts) >= 4
	}
	return false
}

// OnlyRefusals reports whether err is made of refusals alone (exit 1); an
// error that is, or contains, a backend failure is not (exit 2).
func OnlyRefusals(err error) bool {
	if err == nil {
		return false
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if !OnlyRefusals(e) {
				return false
			}
		}
		return true
	}
	if err == ErrRefused {
		return true
	}
	if u := errors.Unwrap(err); u != nil {
		return OnlyRefusals(u)
	}
	return false
}
