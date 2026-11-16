// Package estimate answers what a task of a given tier and rigor is expected to
// take and cost, out of the runs that have already happened
// (docs/orchestrator-graph-estimates, D2712-D2714, D2724).
//
// A plan's estimate needs one answer per task, and nothing in the tree had one:
// `fugaro estimate` reprices a single run's tokens on another model, and
// internal/stats groups by recipe with no tier, rigor or plan in it (F20). This
// package learns the answer from history instead. Table is a run history read
// per (tier, rigor), with a fallback to the tier alone and then to the tier's
// seed, because a plan's tasks are spread over four tiers and five modes and
// the interesting group is almost always thin (D2713).
//
// Three properties are the point of the package.
//
//   - No figure is drawn from fewer than MinRuns runs. A range read off two
//     runs reads as a promise precisely because it is a range, so a group that
//     thin does not answer at all and the fallback does. D96's twenty stays
//     where it is, for `fugaro stats`: this is the estimate's own threshold.
//   - Nothing is invented where history is missing. A tier no run reaches
//     answers its seed in minutes and no cost at all (D2712): cost depends too
//     much on a repository's own model choices to guess, so a cost figure reads
//     "no history yet" until runs have paid for it, even where a repository
//     configured expected_usd.
//   - The table is the file. `fugaro plan status` reads the runs bucket and
//     writes the cache; the commands that may not reach the network — `fugaro
//     plan graph` and the session-start brief (D1243, D2714 as amended) — read
//     it back, so the shape on the wire is Table itself and a reader from
//     another release refuses it rather than reading a shape that moved.
//
// Nothing here reserves anything: an estimate is informational (spec §1), the
// deployed per-run cap stays the ceiling whatever a tier expects, and no run
// consults a table.
package estimate

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// MinRuns is the least runs a group may answer from (D2713). Below it the
// group is not an answer at all: the tier alone answers, and a tier that is
// thin too answers its seed.
const MinRuns = 5

// WideRuns is the sample size at and above which a range is published as the
// p25-p75 it is (D2713). Below it the range is widened by Widening, because a
// range drawn from a handful of runs is narrower than the uncertainty behind
// it.
const WideRuns = 20

// Widening is the factor each half of a range grows by while the sample is
// under WideRuns runs (D2713). It grows from the median, which does not move:
// the median is the figure a reader takes as the estimate, and a range widened
// around its own centre would move it.
const Widening = 1.5

// SeedLow and SeedHigh are what a tier's seed is widened to when no history
// reaches it (D2712): a seeded answer is the seed minutes at half of them and
// at twice them, which is the "keep ranges wide early on" the spec asks for.
const (
	SeedLow  = 0.5
	SeedHigh = 2
)

// maxFigure is the largest figure a record may carry and still be one this
// package reads: past it the interpolation below overflows to an infinity, and
// 1e308 is a value a bad record can carry. NaN fails the same comparison and is
// refused with it.
const maxFigure = math.MaxFloat64 / 100

// Basis says which sample an answer's figures came from (D2713). It is the
// `basis` a plan node carries (D2722), less `assumed`: a node that names no
// tier is the plan's own answer and never this package's.
type Basis string

const (
	// BasisHistory is the run's own (tier, rigor) group, MinRuns runs or more.
	BasisHistory Basis = "history"
	// BasisTier is the tier alone, every run of it whatever mode it resolved:
	// the (tier, rigor) group is under MinRuns and the tier is not.
	BasisTier Basis = "tier"
	// BasisSeed is the tier's seed minutes, with no cost figure beside them:
	// no group of the tier reached MinRuns (D2712).
	BasisSeed Basis = "seed"
)

// Range is a low-high pair, in the unit the field holding it names.
type Range struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// Answer is what the table says about one task's tier and rigor.
type Answer struct {
	// Minutes is the duration range in minutes, always present: where no
	// history reaches the tier the seed answers it (D2712).
	Minutes Range
	// Cost is the cost range in dollars, and a figure only when HasCost.
	Cost Range
	// HasCost is false where no history carries a cost for this answer: the
	// figure reads "no history yet" rather than zero, which a reader would
	// take for free (D2712, D2723).
	HasCost bool
	// Basis is the sample the figures came from.
	Basis Basis
	// Runs is the sample size behind them: the group's runs for history and
	// tier, and zero for a seed, which is not a sample.
	Runs int
}

// Quartiles are the three figures one group is summarised by: the 25th, 50th
// and 75th percentile of its sample, interpolated between the closest ranks.
type Quartiles struct {
	P25 float64 `json:"p25"`
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
}

// Group is one sample of the history: the runs of one tier, or of one tier and
// one rigor mode. Its figures are the sample's own -- the widening under
// WideRuns runs is the answer's, not the sample's -- so the table says what
// history measured and the answer says what may be read from it.
type Group struct {
	// Tier is the group's tier. A group naming a tier this release does not
	// ship is refused where it is read back.
	Tier tier.Tier `json:"tier"`
	// Rigor is the mode this group is the runs of, and empty for the tier's
	// own group: every run of the tier, whatever mode it resolved, which is
	// the fallback D2713 reaches for second.
	Rigor rigor.Mode `json:"rigor,omitempty"`
	// Runs is the group's sample size: every run the table could draw a
	// duration from.
	Runs int `json:"runs"`
	// DurationMinutes is the group's wall-clock duration in minutes.
	DurationMinutes Quartiles `json:"duration_minutes"`
	// CostUSD is the same three figures for the runs of the group that carry a
	// cost breakdown, and absent where fewer than MinRuns of them do: a cost
	// read off two paid runs is not a figure either (D2712).
	CostUSD *Quartiles `json:"cost_usd,omitempty"`
	// CostRuns is how many of the group's runs carried one, and so the sample
	// the cost figures are read from: it is the count the cost range is
	// widened by, which is not always the group's own -- a group that reaches
	// MinRuns on runs whose records predate the breakdown carries fewer. It is
	// zero exactly when CostUSD is absent.
	CostRuns int `json:"cost_runs,omitempty"`
}

// Table is a run history as this package reads it: one Group per tier, and one
// per (tier, rigor) pair that history carries. Its zero value -- and a nil
// table -- is the empty history, which answers every tier its seed.
type Table struct {
	// Version is the cache format's own version, so a reader tells a file this
	// release reads from one it does not.
	Version int `json:"schema_version"`
	// WrittenAt is when the runs behind the table were read, which is what
	// says how stale an offline answer is. A file carrying no stamp is
	// refused: a reader that cannot say how old the table is cannot say
	// whether to trust it.
	WrittenAt time.Time `json:"written_at"`
	// Groups are the samples, by tier in the vocabulary's order and by mode
	// within a tier, so two tables built from the same runs are the same
	// document.
	Groups []Group `json:"groups"`
}

// Build reads a run history into a table, stamped at now. Every record is
// either a sample or nothing:
//
//   - a run that has not finished has no duration, so it is not one;
//   - an infra_error is a fault of the platform rather than a measure of the
//     work, and a cancelled run was stopped at whatever moment a person chose:
//     neither says how long the work takes, and both are left out;
//   - a run whose tier is not one this release ships is grouped under no tier
//     at all -- the answer the field's own absence gives -- and a run whose
//     rigor block is missing or names a mode outside the vocabulary is a
//     sample of its tier and of no (tier, rigor) group.
//
// A run that ended failed or halted is none of those: it took the time it took,
// and its duration is a real sample of its tier. A record whose duration or
// cost is not a figure -- zero or negative minutes, a negative dollar, an
// infinity, a NaN, a value past maxFigure -- is dropped from that measure
// alone, so one bad record costs one sample and never the table.
//
// now is the caller's clock, stamped on the table: a reader says how stale a
// cached table is from it, so a caller that passes the zero time writes one its
// own reader refuses.
func Build(records []runstore.Record, now time.Time) *Table {
	type sample struct {
		durations, costs []float64
	}
	// One map holds both kinds of group: the (tier, mode) pair where the mode
	// is known, and (tier, "") -- the tier's own group -- for every run.
	groups := map[[2]string]*sample{}
	of := func(tr tier.Tier, mode rigor.Mode) *sample {
		key := [2]string{string(tr), string(mode)}
		s := groups[key]
		if s == nil {
			s = &sample{}
			groups[key] = s
		}
		return s
	}
	for i := range records {
		rec := &records[i]
		tr, mode, ok := historyOf(rec)
		if !ok {
			continue
		}
		minutes := rec.FinishedAt.Sub(rec.StartedAt).Minutes()
		if minutes <= 0 || !figure(minutes) {
			continue
		}
		// A run feeds the tier's own group and, where its mode is known, the
		// (tier, mode) one beside it: the same run is a sample of both, and
		// the fallback D2713 reaches for is the wider of the two.
		own := of(tr, "")
		own.durations = append(own.durations, minutes)
		byMode := own
		if mode != "" {
			byMode = of(tr, mode)
			byMode.durations = append(byMode.durations, minutes)
		}
		if rec.Cost != nil && figure(rec.Cost.TotalUSD) {
			own.costs = append(own.costs, rec.Cost.TotalUSD)
			if mode != "" {
				byMode.costs = append(byMode.costs, rec.Cost.TotalUSD)
			}
		}
	}

	out := &Table{Version: CacheVersion, WrittenAt: now.UTC(), Groups: make([]Group, 0, len(groups))}
	for _, key := range slices.SortedFunc(maps.Keys(groups), compareKeys) {
		s := groups[key]
		g := Group{
			Tier:            tier.Tier(key[0]),
			Rigor:           rigor.Mode(key[1]),
			Runs:            len(s.durations),
			DurationMinutes: quartiles(s.durations),
		}
		if len(s.costs) >= MinRuns {
			q := quartiles(s.costs)
			g.CostUSD, g.CostRuns = &q, len(s.costs)
		}
		out.Groups = append(out.Groups, g)
	}
	return out
}

// historyOf is the tier and rigor mode a record is a sample of. ok is false
// for a record no group can hold: one that has not finished, one an
// infrastructure fault or a cancellation ended, and one naming a tier this
// release does not ship. The mode is empty where the record carries no rigor
// block or names a mode outside the vocabulary, and the run is then a sample of
// its tier alone.
func historyOf(rec *runstore.Record) (tr tier.Tier, mode rigor.Mode, ok bool) {
	if rec.Status == runstore.StatusRunning || rec.FinishedAt == nil {
		return "", "", false
	}
	if rec.Status == runstore.StatusInfraError || rec.Status == runstore.StatusCancelled {
		return "", "", false
	}
	if rec.Tier == nil || !tier.Known(rec.Tier.Tier) {
		return "", "", false
	}
	if rec.Rigor != nil && rigor.Known(rec.Rigor.Mode) {
		mode = rec.Rigor.Mode
	}
	return rec.Tier.Tier, mode, true
}

// compareKeys orders two group keys: by tier in the vocabulary's own order --
// smallest first, as Tiers spells them -- and by mode within a tier, with the
// tier's own group (the empty mode) first.
func compareKeys(a, b [2]string) int {
	if c := slices.Index(tier.Tiers, tier.Tier(a[0])) - slices.Index(tier.Tiers, tier.Tier(b[0])); c != 0 {
		return c
	}
	return cmp.Compare(a[1], b[1])
}

// figure reports whether v can be one of the table's figures: a duration in
// minutes or a cost in dollars is never negative, never NaN, and never so large
// that a range built from it stops being a figure.
func figure(v float64) bool { return v >= 0 && v <= maxFigure }

// Answer is what the table says about a task of tier tr and mode: the
// (tier, mode) group at MinRuns runs or more, else the tier alone at MinRuns,
// else the tier's seed minutes with no cost figure (D2712, D2713).
//
// An empty or unknown mode is answered by the tier alone, never by the tier's
// own group as though it were the (tier, mode) one: a caller that named no mode
// asked for the tier's answer, and its basis says so. A tier this release does
// not ship has no seed and answers the zero range -- the plan's own `assumed`
// answer (D2722) is where a node that names no tier is decided, and never here.
func (t *Table) Answer(tr tier.Tier, mode rigor.Mode) Answer {
	if rigor.Known(mode) {
		if g := t.group(tr, mode); g != nil && g.Runs >= MinRuns {
			return g.answer(BasisHistory)
		}
	}
	if g := t.group(tr, ""); g != nil && g.Runs >= MinRuns {
		return g.answer(BasisTier)
	}
	return seedAnswer(tr)
}

// group is the table's group for one tier and mode, or nil where the history
// carries none. The scan is linear, and a table holds at most one group per
// tier and one per (tier, mode) -- twenty-four, for four tiers and five modes
// -- so the flat shape on the wire costs nothing a reader can measure.
func (t *Table) group(tr tier.Tier, mode rigor.Mode) *Group {
	if t == nil {
		return nil
	}
	for i := range t.Groups {
		if t.Groups[i].Tier == tr && t.Groups[i].Rigor == mode {
			return &t.Groups[i]
		}
	}
	return nil
}

// answer is the group's figures as an answer drawn from it.
func (g *Group) answer(basis Basis) Answer {
	a := Answer{
		Minutes: widen(g.DurationMinutes, g.Runs),
		Basis:   basis,
		Runs:    g.Runs,
	}
	if g.CostUSD != nil {
		a.Cost = widen(*g.CostUSD, g.CostRuns)
		a.HasCost = true
	}
	return a
}

// widen is a group's quartiles as a range: the p25-p75 it is, and each half of
// it Widening times as wide while the sample is under WideRuns runs (D2713).
//
// The widened low is floored at zero: a sample whose p25 sits under a third of
// its p50 reaches below it -- five runs of 1, 1, 5, 5 and 9 minutes are p25/p50
// of 1/5, and the low half of that reaches -1 -- and a plan has no negative
// minute or dollar figure to print. The high needs no floor: the samples are
// durations and costs, which Build admits only at zero or above, so it is never
// under the median.
func widen(q Quartiles, runs int) Range {
	if runs >= WideRuns {
		return Range{Low: q.P25, High: q.P75}
	}
	return Range{
		Low:  max(0, q.P50-Widening*(q.P50-q.P25)),
		High: q.P50 + Widening*(q.P75-q.P50),
	}
}

// seedAnswer is the answer for a tier no history reaches: the tier's seed
// minutes, widened to SeedLow..SeedHigh of them, and no cost figure at all --
// there is no cost seed, because a run's cost depends too much on the
// repository's own model choices to guess at (D2712). A tier this release does
// not ship has no seed, so the range is zero.
func seedAnswer(tr tier.Tier) Answer {
	minutes, _ := tier.SeedMinutes(tr)
	return Answer{
		Minutes: Range{Low: SeedLow * float64(minutes), High: SeedHigh * float64(minutes)},
		Basis:   BasisSeed,
	}
}

// quartiles is the p25, p50 and p75 of xs, which is not modified.
func quartiles(xs []float64) Quartiles {
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	return Quartiles{
		P25: quantile(sorted, 0.25),
		P50: quantile(sorted, 0.5),
		P75: quantile(sorted, 0.75),
	}
}

// quantile is the p-th percentile of sorted (ascending), interpolated between
// the closest ranks: the method R calls type 7 and numpy's default, which is
// the one internal/stats publishes its own figures with, so a tier's median
// here is the median `fugaro stats` prints for the same runs. It is a second
// copy of that interpolation rather than a shared one because the figures
// differ -- that package publishes a minimum, a median and a p90 -- and its
// helper is unexported.
func quantile(sorted []float64, p float64) float64 {
	switch len(sorted) {
	case 0:
		return 0
	case 1:
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (idx-float64(lo))*(sorted[hi]-sorted[lo])
}

// CacheVersion is the version of the cached table's own format, written beside
// the table so a reader tells a file from another release from a current one
// rather than reading a shape that moved.
const CacheVersion = 1

// CachePath is where this machine's table cache lives:
// $XDG_CACHE_HOME/fugaro/estimate/history.json, placed through localcfg.UserDir
// the way the daemon's cached brief is. It is one path for both ends of the
// file -- the command that reads history and writes the cache, and the
// commands that read it offline (D2714 as amended) -- so the two cannot drift
// into reading one place and writing another.
//
// goos is a parameter rather than a read of runtime.GOOS for the reason UserDir
// takes one: the Windows rule stays testable from a Linux checkout.
func CachePath(getenv func(string) string, goos string) (string, error) {
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirCache)
	if err != nil {
		return "", fmt.Errorf("the estimate's cached history cannot be placed: %w", err)
	}
	return filepath.Join(dir, "fugaro", "estimate", "history.json"), nil
}

// WriteCache writes the table to path, creating the directory above it, as one
// private file replaced atomically: another command of this machine may be
// reading the same file at the same moment, and it sees either the previous
// table or this one and never half of either. The version on the wire is always
// this release's, so a table built by hand cannot write a version it does not
// know.
func (t *Table) WriteCache(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the directory of the cached history at %s: %w", dir, err)
	}
	doc := Table{}
	if t != nil {
		doc = *t
	}
	doc.Version = CacheVersion
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encoding the cached history: %w", err)
	}
	f, err := os.CreateTemp(dir, ".history-*.tmp")
	if err != nil {
		return fmt.Errorf("writing the cached history beside %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("setting the mode of the cached history: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the cached history: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the cached history: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("placing the cached history at %s: %w", path, err)
	}
	return nil
}

// ReadCache reads the table at path. A file that is not there is
// fs.ErrNotExist, which is the caller's "no cache yet" and a fallback to the
// seeds rather than a failure; every other trouble is refused -- a version from
// another release, an unknown field, a table with no stamp, a group naming a
// tier this release does not ship -- because half-reading a file whose shape
// moved would put a figure under a name no reader can look up.
func ReadCache(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the cached history at %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Table
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the cached history at %s is not a document this release reads: %w", path, err)
	}
	switch {
	case doc.Version != CacheVersion:
		return nil, fmt.Errorf("the cached history at %s carries schema_version %d, and this release reads %d", path, doc.Version, CacheVersion)
	case doc.WrittenAt.IsZero():
		return nil, fmt.Errorf("the cached history at %s carries no time it was written, so how stale it is cannot be shown", path)
	}
	for _, g := range doc.Groups {
		if !tier.Known(g.Tier) {
			return nil, fmt.Errorf("the cached history at %s carries a group of tier %q, which is not a tier this release ships", path, g.Tier)
		}
	}
	return &doc, nil
}
