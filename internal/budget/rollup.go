package budget

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// DayRecordVersion is the DayRecord schema version (the document's version
// field). FromFields refuses any other.
const DayRecordVersion = 1

// UnknownPerson is the byPerson key of spend with no known requester.
const UnknownPerson = "unknown"

// UnknownModel is the byModel key of notional dollars a repository's own
// total has that no model's own figure explains (modelName's own fallback,
// lease.go, for an empty or oversized model name, agrees with this name).
const UnknownModel = "unknown"

// maxPersonBytes clips a requester address before it becomes a map key.
const maxPersonBytes = 200

// The outcome statuses a DayRecord counts; any other status counts as
// "other" so a hostile or new status is visible, never dropped.
var outcomeKeys = []string{"succeeded", "failed", "halted", "cancelled", "infra_error", "other"}

// slugSafe is what a slug must match to be part of a document ID.
var slugSafe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// RunFact is what the runs bucket knows about one run (result.json and
// run.json), already read by the caller. Rollup does no I/O.
type RunFact struct {
	Slug, Run   string // raw (unescaped) slug and run id
	StartDay    int64  // UTC day of the run's start: the day compute and the outcome belong to
	RequestedBy string
	// ComputeUSD counts only when ComputeEstimated; otherwise the run adds
	// hours but "not estimated" is not "free".
	ComputeUSD       float64
	ComputeEstimated bool
	Hours            float64
}

// LifetimeRun is a run's lifetime ledger (/runs/<slug>/<run>) and the latest
// day on which the run has a share. Its overrun and its unreconciled
// remainder belong to that day (H3). LastShareDay 0 means unknown: the run
// is then not attributed anywhere.
type LifetimeRun struct {
	RunLedger
	LastShareDay int64
}

// RepoDay is one repository's node of the day.
type RepoDay struct {
	Counters Counters
	Repo     string // display name, e.g. owner/name; empty falls back to the slug
	CapDaily *Micros
}

// RollupInput is everything Rollup reads. The slug and run keys of the maps
// are RTDB keys (Key-escaped), as on the wire.
type RollupInput struct {
	Day      int64
	Repos    map[string]RepoDay                // spend/<day>/repos
	Shares   map[string]map[string]RunLedger   // spend/<day>/runs
	Ledgers  map[string]map[string]LifetimeRun // runs/<slug>/<run>, for the runs with a share on this day or later
	Outcomes map[string]map[string]Outcome     // outcomes/<day>
	NextDay  map[string]map[string]Outcome     // outcomes/<day+1>
	Runs     []RunFact
}

// ModelTotals is one model's usage in a DayRecord. Micros is model dollars
// actually billed; NotionalMicros is an oauth run's own re-derived estimate
// of its share of the subscription's list-price figure. The two are never
// summed: see the package doc.
type ModelTotals struct {
	Micros, NotionalMicros Micros
	In, Out, CR, CW        int64
}

// PersonTotals is one requester's spend in a DayRecord.
type PersonTotals struct {
	Micros, NotionalMicros Micros
	Runs                   int64
}

// DayRecord is one repository's day: the body of a spendDaily document.
// Amounts are integer micro-dollars; NotionalMicros (subscription runs, never
// charged) and ComputeMicros are separate from SpentMicros (model dollars)
// and are never summed into it.
type DayRecord struct {
	Repo, Slug, Date string

	SpentMicros, NotionalMicros, ComputeMicros Micros
	UnreconciledMicros, OverrunMicros          Micros
	ComputeEstimatedRuns                       int64 // runs whose compute was estimated: 0 with Runs > 0 means "n/a"
	RunHours                                   float64
	Calls, Runs                                int64
	Outcomes                                   map[string]int64
	ByModel                                    map[string]ModelTotals  // keyed by ModelKey (escaped)
	ByPerson                                   map[string]PersonTotals // keyed by Key(requester)
	CapDailyMicros                             *Micros

	Final      bool
	Version    int
	ArchivedAt time.Time // set by the rollover, zero here
	WrittenAt  time.Time
}

// DocID is the Firestore document ID of the record.
func (r DayRecord) DocID() string { return r.Date + "_" + r.Slug }

// ComputeKnown reports whether any run's compute was estimated; when false
// (and there were runs) a report shows n/a, never 0.
func (r DayRecord) ComputeKnown() bool { return r.ComputeEstimatedRuns > 0 || r.Runs == 0 }

func satAdd(a, b Micros) Micros {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func satAddN(a, b int64) int64 { return int64(satAdd(Micros(a), Micros(b))) }

func clamp0(m Micros) Micros {
	if m < 0 {
		return 0
	}
	return m
}

func microsFromUSD(usd float64) (Micros, bool) {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return 0, false
	}
	f := math.Round(usd * 1e6)
	if f >= math.MaxInt64 {
		return math.MaxInt64, true
	}
	return Micros(f), true
}

func unkeyOr(k string) string {
	if v, err := Unkey(k); err == nil {
		return v
	}
	return k
}

// personKey normalises a requester address into a byPerson map key.
func personKey(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxPersonBytes {
		s = s[:maxPersonBytes]
	}
	s = strings.ToValidUTF8(s, "?")
	if s == "" {
		return UnknownPerson
	}
	return Key(s)
}

func runKey(slug, run string) string { return slug + "\x00" + run }

func newRecord(day int64, slug, repo string) *DayRecord {
	if repo == "" {
		repo = slug
	}
	rec := &DayRecord{
		Repo: repo, Slug: slug, Date: DayDate(day), Version: DayRecordVersion,
		Outcomes: map[string]int64{}, ByModel: map[string]ModelTotals{}, ByPerson: map[string]PersonTotals{},
	}
	for _, k := range outcomeKeys {
		rec.Outcomes[k] = 0
	}
	return rec
}

// Rollup turns one day's database nodes into one DayRecord per repository
// that had activity, sorted by slug. It is pure and deterministic: the same
// input gives the same records (WrittenAt, ArchivedAt and Final are the
// caller's). Repositories whose slug cannot be a document ID are skipped.
//
// Attribution (H3): model dollars, notional, calls and run shares are the
// day's own counters; a run's compute, hours, run count and outcome belong to
// its start day (facts name it; without a fact the outcome counts on the day
// whose node holds it); a run's overrun and unreconciled remainder belong to
// the latest day it has a share. Whatever part of a repository's spend the
// run shares do not explain goes to the "unknown" person, so byPerson always
// sums to the repository's spend.
func Rollup(in RollupInput) []DayRecord {
	recs := map[string]*DayRecord{}
	get := func(rawSlug string) *DayRecord {
		slug := unkeyOr(rawSlug)
		if !slugSafe.MatchString(slug) {
			return nil
		}
		if r, ok := recs[slug]; ok {
			return r
		}
		r := newRecord(in.Day, slug, "")
		recs[slug] = r
		return r
	}

	facts := map[string]RunFact{}
	for _, f := range in.Runs {
		facts[runKey(Key(f.Slug), Key(f.Run))] = f
	}
	person := func(slug, run string, o *Outcome) string {
		if f, ok := facts[runKey(slug, run)]; ok && strings.TrimSpace(f.RequestedBy) != "" {
			return personKey(f.RequestedBy)
		}
		if o != nil {
			return personKey(o.RequestedBy)
		}
		return UnknownPerson
	}
	findOutcome := func(slug, run string) *Outcome {
		for _, m := range []map[string]map[string]Outcome{in.Outcomes, in.NextDay} {
			if o, ok := m[slug][run]; ok {
				return &o
			}
		}
		return nil
	}

	// Counters.
	for slug, rd := range in.Repos {
		r := get(slug)
		if r == nil {
			continue
		}
		if rd.Repo != "" {
			r.Repo = rd.Repo
		}
		c := rd.Counters
		r.SpentMicros, r.NotionalMicros = clamp0(c.Spent), clamp0(c.Notional)
		r.Calls = max(c.Calls, 0)
		if rd.CapDaily != nil {
			v := clamp0(*rd.CapDaily)
			r.CapDailyMicros = &v
		}
		for k, u := range c.ByModel {
			name, err := Unkey(k)
			if err != nil || name == "" {
				name = k
			}
			if name == "" {
				continue
			}
			key := Key(name)
			t := r.ByModel[key]
			t.Micros = satAdd(t.Micros, u.Micros)
			t.NotionalMicros = satAdd(t.NotionalMicros, u.NotionalMicros)
			t.In, t.Out = satAddN(t.In, u.In), satAddN(t.Out, u.Out)
			t.CR, t.CW = satAddN(t.CR, u.CR), satAddN(t.CW, u.CW)
			r.ByModel[key] = t
		}
	}

	// Run shares: byPerson. A run started on an earlier day is not counted
	// as a run of this day, but its dollars are.
	counted := map[string]bool{}
	sharedSpent, sharedNotional := map[string]Micros{}, map[string]Micros{}
	for slug, runs := range in.Shares {
		r := get(slug)
		if r == nil {
			continue
		}
		for run, l := range runs {
			p := person(slug, run, findOutcome(slug, run))
			t := r.ByPerson[p]
			t.Micros, t.NotionalMicros = satAdd(t.Micros, l.Spent), satAdd(t.NotionalMicros, l.Notional)
			r.ByPerson[p] = t
			sharedSpent[r.Slug] = satAdd(sharedSpent[r.Slug], l.Spent)
			sharedNotional[r.Slug] = satAdd(sharedNotional[r.Slug], l.Notional)
			if f, ok := facts[runKey(slug, run)]; !ok || f.StartDay == in.Day {
				counted[runKey(slug, run)] = true
			}
		}
	}

	// Facts: compute, hours, run count (start-day attribution).
	for _, f := range in.Runs {
		if f.StartDay != in.Day {
			continue
		}
		r := get(Key(f.Slug))
		if r == nil {
			continue
		}
		counted[runKey(Key(f.Slug), Key(f.Run))] = true
		if h := f.Hours; h > 0 && !math.IsInf(h, 0) {
			r.RunHours += h
		}
		if f.ComputeEstimated {
			if m, ok := microsFromUSD(f.ComputeUSD); ok {
				r.ComputeMicros = satAdd(r.ComputeMicros, m)
				r.ComputeEstimatedRuns++
			}
		}
	}

	// Outcomes, by the run's start day.
	for _, src := range []struct {
		m    map[string]map[string]Outcome
		node int64
	}{{in.Outcomes, in.Day}, {in.NextDay, in.Day + 1}} {
		for slug, runs := range src.m {
			for run, o := range runs {
				day := src.node
				if f, ok := facts[runKey(slug, run)]; ok {
					day = f.StartDay
				}
				if day != in.Day {
					continue
				}
				r := get(slug)
				if r == nil {
					continue
				}
				counted[runKey(slug, run)] = true
				k := o.Status
				if !contains(outcomeKeys, k) || k == "other" {
					k = "other"
				}
				r.Outcomes[k]++
			}
		}
	}

	// Overrun and unreconciled: the run's latest day.
	for slug, runs := range in.Ledgers {
		for _, l := range runs {
			if l.LastShareDay != in.Day {
				continue
			}
			r := get(slug)
			if r == nil {
				continue
			}
			r.OverrunMicros = satAdd(r.OverrunMicros, l.Overrun)
			if l.Crashed {
				r.UnreconciledMicros = satAdd(r.UnreconciledMicros, l.Unsettled())
			}
		}
	}

	for k := range counted {
		slug, _, _ := strings.Cut(k, "\x00")
		if r := get(slug); r != nil {
			r.Runs++
		}
	}
	// Per-person runs: one per attributed run with a share.
	for slug, runs := range in.Shares {
		r := get(slug)
		if r == nil {
			continue
		}
		for run := range runs {
			if counted[runKey(slug, run)] {
				p := person(slug, run, findOutcome(slug, run))
				t := r.ByPerson[p]
				t.Runs++
				r.ByPerson[p] = t
			}
		}
	}

	// Unexplained remainder goes to "unknown".
	out := make([]DayRecord, 0, len(recs))
	for slug, r := range recs {
		if d := r.SpentMicros - sharedSpent[slug]; d > 0 {
			t := r.ByPerson[UnknownPerson]
			t.Micros = satAdd(t.Micros, d)
			r.ByPerson[UnknownPerson] = t
		}
		if d := r.NotionalMicros - sharedNotional[slug]; d > 0 {
			t := r.ByPerson[UnknownPerson]
			t.NotionalMicros = satAdd(t.NotionalMicros, d)
			r.ByPerson[UnknownPerson] = t
		}
		// A model's notional dollars are a re-derived estimate (the owner's
		// price table against the run's own token counts), independent of
		// the repository's own total (the subscription's own reported
		// figure); unlike billed model dollars, which come from the same
		// gateway ledger as the total, they are not guaranteed to agree.
		// Whatever the per-model breakdown falls short of explaining goes to
		// "unknown", so byModel's notional always sums to the repository's.
		var modelNotional Micros
		for _, t := range r.ByModel {
			modelNotional = satAdd(modelNotional, t.NotionalMicros)
		}
		if d := r.NotionalMicros - modelNotional; d > 0 {
			t := r.ByModel[UnknownModel]
			t.NotionalMicros = satAdd(t.NotionalMicros, d)
			r.ByModel[UnknownModel] = t
		}
		if !r.active() {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

func (r *DayRecord) active() bool {
	if r.SpentMicros > 0 || r.NotionalMicros > 0 || r.ComputeMicros > 0 || r.UnreconciledMicros > 0 ||
		r.OverrunMicros > 0 || r.Calls > 0 || r.Runs > 0 || len(r.ByModel) > 0 {
		return true
	}
	for _, n := range r.Outcomes {
		if n > 0 {
			return true
		}
	}
	return false
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ToFields encodes the record as Firestore document fields: string, int64,
// float64, bool, time.Time and map[string]any only. Map keys are already
// Key-escaped. A zero ArchivedAt or WrittenAt and a nil cap are omitted.
func (r DayRecord) ToFields() map[string]any {
	models := map[string]any{}
	for k, m := range r.ByModel {
		models[k] = map[string]any{"micros": int64(m.Micros), "notionalMicros": int64(m.NotionalMicros), "in": m.In, "out": m.Out, "cr": m.CR, "cw": m.CW}
	}
	people := map[string]any{}
	for k, p := range r.ByPerson {
		people[k] = map[string]any{"micros": int64(p.Micros), "notionalMicros": int64(p.NotionalMicros), "runs": p.Runs}
	}
	outs := map[string]any{}
	for k, n := range r.Outcomes {
		outs[k] = n
	}
	f := map[string]any{
		"repo": r.Repo, "slug": r.Slug, "date": r.Date,
		"spentMicros": int64(r.SpentMicros), "notionalMicros": int64(r.NotionalMicros),
		"computeMicros": int64(r.ComputeMicros), "unreconciledMicros": int64(r.UnreconciledMicros),
		"overrunMicros": int64(r.OverrunMicros), "computeEstimatedRuns": r.ComputeEstimatedRuns,
		"runHours": r.RunHours, "calls": r.Calls, "runs": r.Runs,
		"outcomes": outs, "byModel": models, "byPerson": people,
		"final": r.Final, "version": int64(r.Version),
	}
	if r.CapDailyMicros != nil {
		f["capDailyMicros"] = int64(*r.CapDailyMicros)
	}
	if !r.ArchivedAt.IsZero() {
		f["archivedAt"] = r.ArchivedAt.UTC()
	}
	if !r.WrittenAt.IsZero() {
		f["writtenAt"] = r.WrittenAt.UTC()
	}
	return f
}

type fieldReader struct{ err error }

func (fr *fieldReader) fail(format string, a ...any) {
	if fr.err == nil {
		fr.err = fmt.Errorf(format, a...)
	}
}

func (fr *fieldReader) str(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		fr.fail("field %q is %T, not a string", k, v)
	}
	return s
}

func (fr *fieldReader) int(m map[string]any, k string) int64 {
	v, ok := m[k]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	}
	fr.fail("field %q is %T, not an integer", k, v)
	return 0
}

func (fr *fieldReader) float(m map[string]any, k string) float64 {
	v, ok := m[k]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	fr.fail("field %q is %T, not a number", k, v)
	return 0
}

func (fr *fieldReader) sub(m map[string]any, k string) map[string]any {
	v, ok := m[k]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(map[string]any)
	if !ok {
		fr.fail("field %q is %T, not a map", k, v)
	}
	return s
}

func (fr *fieldReader) when(m map[string]any, k string) time.Time {
	v, ok := m[k]
	if !ok || v == nil {
		return time.Time{}
	}
	t, ok := v.(time.Time)
	if !ok {
		fr.fail("field %q is %T, not a timestamp", k, v)
	}
	return t
}

// FromFields decodes a document's fields. Missing fields are zero; a field of
// the wrong type, or a version other than DayRecordVersion, is an error. Map
// keys are taken as stored (they are already escaped).
func FromFields(f map[string]any) (DayRecord, error) {
	fr := &fieldReader{}
	r := DayRecord{
		Repo: fr.str(f, "repo"), Slug: fr.str(f, "slug"), Date: fr.str(f, "date"),
		SpentMicros: Micros(fr.int(f, "spentMicros")), NotionalMicros: Micros(fr.int(f, "notionalMicros")),
		ComputeMicros: Micros(fr.int(f, "computeMicros")), UnreconciledMicros: Micros(fr.int(f, "unreconciledMicros")),
		OverrunMicros: Micros(fr.int(f, "overrunMicros")), ComputeEstimatedRuns: fr.int(f, "computeEstimatedRuns"),
		RunHours: fr.float(f, "runHours"), Calls: fr.int(f, "calls"), Runs: fr.int(f, "runs"),
		Final: false, Version: int(fr.int(f, "version")),
		ArchivedAt: fr.when(f, "archivedAt"), WrittenAt: fr.when(f, "writtenAt"),
		Outcomes: map[string]int64{}, ByModel: map[string]ModelTotals{}, ByPerson: map[string]PersonTotals{},
	}
	if v, ok := f["final"]; ok && v != nil {
		b, ok := v.(bool)
		if !ok {
			fr.fail("field %q is %T, not a bool", "final", v)
		}
		r.Final = b
	}
	if _, ok := f["capDailyMicros"]; ok && f["capDailyMicros"] != nil {
		c := Micros(fr.int(f, "capDailyMicros"))
		r.CapDailyMicros = &c
	}
	for k := range fr.sub(f, "outcomes") {
		r.Outcomes[k] = fr.int(fr.sub(f, "outcomes"), k)
	}
	for k := range fr.sub(f, "byModel") {
		m := fr.sub(fr.sub(f, "byModel"), k)
		r.ByModel[k] = ModelTotals{Micros: Micros(fr.int(m, "micros")), NotionalMicros: Micros(fr.int(m, "notionalMicros")),
			In: fr.int(m, "in"), Out: fr.int(m, "out"), CR: fr.int(m, "cr"), CW: fr.int(m, "cw")}
	}
	for k := range fr.sub(f, "byPerson") {
		p := fr.sub(fr.sub(f, "byPerson"), k)
		r.ByPerson[k] = PersonTotals{Micros: Micros(fr.int(p, "micros")), NotionalMicros: Micros(fr.int(p, "notionalMicros")), Runs: fr.int(p, "runs")}
	}
	if fr.err != nil {
		return DayRecord{}, fmt.Errorf("budget: day record: %w", fr.err)
	}
	if r.Version != DayRecordVersion {
		return DayRecord{}, fmt.Errorf("budget: day record version %d is not supported (this build reads %d)", r.Version, DayRecordVersion)
	}
	if !utf8.ValidString(r.Slug) || r.Slug == "" || r.Date == "" {
		return DayRecord{}, fmt.Errorf("budget: day record has no slug or date")
	}
	return r, nil
}
