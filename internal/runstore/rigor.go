package runstore

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// The record's rigor block (docs/rigor-modes/implementation-plan.md T1.2,
// D22, D24, D25): the level a run resolved, the layer that decided it, every
// area rule that matched, and the moment of the run each was known at. It is
// the run's own account of its rigor, written by the runner at the three
// moments D22 names and read by `fugaro runs ls`, `fugaro diagnose` and the
// console through internal/runview.
//
// **The level is carried under two keys.** LD2 retired the word "mode" for
// this axis, so the block's level is written as `level` — and as `mode` beside
// it, because a record is a wire shape a reader outside this binary consumes
// and a field never changes meaning (CLAUDE.md): a console or a history built
// against the older CLI reads `mode`, and one built since reads `level`. Both
// carry the same value on every record this runner writes; a record written by
// an older CLI carries `mode` alone, which is why every reader goes through
// LevelOf (the block's, and RigorArea's) rather than reading either field
// directly. `version` stays 1: the field was added, never retyped.
//
// Nothing acts on the level yet: this slice records what was resolved and
// changes no behaviour (the plan's Slice 1 note), so a repository that
// configures POC is still reviewed as Critical — the stricter direction —
// while its record says what it asked for.

// The three moments D22 resolves at. A block's `at` is the moment that last
// changed it, and an area keeps the moment it was first applied at.
const (
	// RigorAtBootstrap is bootstrap, from the paths the caller declared
	// (fugaro run --path).
	RigorAtBootstrap = "bootstrap"
	// RigorAtRound is after a review round that changed the tree, from the
	// run's own diff.
	RigorAtRound = "round"
	// RigorAtFinalize is before the pull request is marked ready, from
	// Record.ChangedFiles.
	RigorAtFinalize = "finalize"
)

// RigorMoments is every moment a block may name, in the order a run reaches
// them. It is a closed set: a record that names another one is not a record
// of a resolution this runner takes, and Bounded refuses it.
var RigorMoments = []string{RigorAtBootstrap, RigorAtRound, RigorAtFinalize}

// KnownRigorMoment reports whether at is one of D22's three moments.
func KnownRigorMoment(at string) bool { return slices.Contains(RigorMoments, at) }

// MaxRigorRevision bounds the revision a record names, so a tampered record
// cannot put a kilobyte of revision in every listing. A real one is
// "origin/<branch>" or a commit id.
const MaxRigorRevision = 120

// RigorRecord is the rigor level a run resolved and where it came from.
type RigorRecord struct {
	// Mode is the level the run resolved, under the key this block has always
	// carried. A record this runner writes sets it and Level to the same
	// value; a record written before LD2 carries it alone. Read both through
	// LevelOf, never directly.
	Mode rigor.Rigor `json:"mode"`
	// Level is the same fact under the name the vocabulary uses now (LD2).
	// It is written beside Mode so an older reader still has the key it
	// knows, and it is the one a current reader prefers.
	Level rigor.Rigor `json:"level"`
	// From is the layer that decided it (rigor.Layer): "area" for a rule the
	// run's paths matched, then "repo", "project", Fugaro's built-in
	// "default", and the run's own "request" (LD3's narrowest rank: fugaro
	// run --mode, or the level a --pr follow-up keeps from its pull request,
	// flag or no flag, T5.2). It is
	// never "reached": that is the resolver's answer to a later moment asking
	// again — "hold what you already have" (D22) — and the fold keeps the
	// earlier, narrower reason instead, which is the layer that actually set
	// the level (D24's "the layer that won"). KnownRigorFrom is this set.
	From rigor.Layer `json:"from"`
	// Areas is every rule that matched, strictest first (D23), each with the
	// level it sets and the moment it was first applied at. Absent — never an
	// empty list — when none matched: a reader must not see "there were
	// rules" where the answer is "there were none".
	Areas []RigorArea `json:"areas,omitempty"`
	// At is the moment of the resolution this block records (D24). It is the
	// last moment that changed the block, so a moment that resolved the same
	// answer leaves it alone.
	At string `json:"at"`
	// Revision is the revision of the configuration the level was read from
	// (D25: the base branch, for every run kind), "origin/main" and the like.
	// Absent when the run could not name it.
	Revision string `json:"revision,omitempty"`
}

// MarshalJSON writes the block with its level under both keys, so a record
// this binary writes carries `mode` and `level` with the same value whatever
// the caller built in memory — the invariant the two keys exist for, kept in
// one place instead of at every construction site. A block holding a level
// neither spelling carries is written as it stands: no writer of this runner
// produces one (Bounded drops it), and inventing a value here would hide it.
func (r RigorRecord) MarshalJSON() ([]byte, error) {
	type plain RigorRecord // no methods: every field and tag is written as declared
	out := plain(r)
	if lvl := r.LevelOf(); rigor.Known(lvl) {
		out.Mode, out.Level = lvl, lvl
	}
	return json.Marshal(out)
}

// LevelOf is the level the block records: Level where a writer set it, and
// Mode otherwise — the preference LD2 states, in one place, so no reader has
// to know which key the record it is holding was written with. It is "" for a
// block that carries neither, which every caller already treats as "no record
// of a level".
func (r *RigorRecord) LevelOf() rigor.Rigor {
	if r == nil {
		return ""
	}
	if rigor.Known(r.Level) {
		return r.Level
	}
	return r.Mode
}

// Line is the block as one line of text: the level, the layer that decided it,
// every area rule that matched with the moment it was applied at (D23, D24),
// the moment of the block and the revision the level's configuration was read
// from (D25).
//
// It is here rather than in a reader because two of them print it — `fugaro
// diagnose`'s Rigor line and the run's report — and a second copy would be a
// second wording for one fact. It is one line whatever the block holds: the
// block is job-writable, so an area's name or a revision carrying a newline
// would otherwise forge a line of its own under the reader's.
//
// The caller still owns the medium: a terminal reader runs the result through
// its own sanitiser, and a Markdown one puts its label in front.
func (r *RigorRecord) Line() string {
	if r == nil {
		return ""
	}
	line := string(r.LevelOf()) + ", from " + string(r.From)
	if len(r.Areas) > 0 {
		parts := make([]string, 0, len(r.Areas))
		for _, a := range r.Areas {
			parts = append(parts, a.Name+" at "+a.At)
		}
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	line += ", at " + r.At
	if r.Revision != "" {
		line += ", read from " + r.Revision
	}
	return strings.Join(strings.Fields(line), " ")
}

// RigorArea is one matched rule as the record carries it: the rule's name, the
// level it sets, and the moment of the run it was first applied at (D24).
//
// The level is here, and not only on the block, because D23's order is
// "strictest first" and a reader shows the first entry as the rule that
// decided: `fugaro runs ls`'s LEVEL column names it, and a later screen will
// too. Without the level the order would be a claim no reader could check — a
// single moment's list arrives ordered (rigor.Select), but folding two moments
// concatenates them, and a block the job wrote is not ordered at all — which
// is why both the writer and the reader sort by this field (sortAreas).
//
// The level is carried under two keys for the block's reason (see
// RigorRecord): Level is written beside Mode, and both readers go through
// LevelOf.
type RigorArea struct {
	Name string      `json:"name"`
	Mode rigor.Rigor `json:"mode"`
	// Level is the same fact under the name the vocabulary uses now (LD2).
	Level rigor.Rigor `json:"level"`
	At    string      `json:"at"`
}

// MarshalJSON writes the rule the way RigorRecord.MarshalJSON writes the
// block: both keys, one value, whoever built it.
func (a RigorArea) MarshalJSON() ([]byte, error) {
	type plain RigorArea
	out := plain(a)
	if lvl := a.LevelOf(); rigor.Known(lvl) {
		out.Mode, out.Level = lvl, lvl
	}
	return json.Marshal(out)
}

// LevelOf is the level the rule sets: Level where a writer set it, and Mode
// otherwise — RigorRecord.LevelOf's rule for the rule inside a block.
func (a RigorArea) LevelOf() rigor.Rigor {
	if rigor.Known(a.Level) {
		return a.Level
	}
	return a.Mode
}

// KnownRigorFrom reports whether l is a value a record's `from` may carry. It
// is rigor.KnownLayer minus the one value no record ever says: a block names
// the layer that won (D24), while "reached" is the resolver's way of holding
// the level an earlier moment set (D22) — the fold keeps that earlier layer
// instead, so `from: "reached"` is a block no writer of this runner produces.
func KnownRigorFrom(l rigor.Layer) bool {
	return rigor.KnownLayer(l) && l != rigor.FromReached
}

// Moment is one of D22's three resolutions as the runner hands it to the
// record: the resolver's answer, the moment it was taken, and the revision of
// the configuration it read (D25).
type Moment struct {
	// Resolved is what rigor.Resolve answered. A Moment whose level is not in
	// the vocabulary resolves nothing and is ignored.
	Resolved rigor.Resolved
	// At is one of RigorAtBootstrap, RigorAtRound or RigorAtFinalize.
	At string
	// Revision is where the configuration was read from; "" names none.
	Revision string
}

// Applied returns the block with one moment's resolution folded into it,
// starting from nothing when the receiver is nil: `rec.Rigor =
// rec.Rigor.Applied(m)` is how the runner writes every moment.
//
// The fold is D22's monotone rule in one place. A moment that resolves a
// higher level replaces the level and the layer that decided it; a moment that
// resolves a lower one changes nothing; a moment at the same level keeps the
// layer already recorded, since that is the decision the run actually took,
// and still adds any area the block does not already name. An area keeps the
// moment it was first applied at and the stricter of the levels a moment gave
// it, and the block's At is the last moment that changed it. What comes back
// is the bounded block (bounded below), which is where D23's strictest-first
// order is established for a fold too: two moments concatenate, and the first
// entry is the one a reader names as the rule that decided.
//
// A Moment the record cannot use — no level, or a moment outside D22's three —
// leaves the block exactly as it was, so a nil receiver stays nil.
func (r *RigorRecord) Applied(m Moment) *RigorRecord {
	if !rigor.Known(m.Resolved.Level) || !KnownRigorMoment(m.At) {
		return r
	}
	out := r.Clone()
	raised := !rigor.Known(out.LevelOf())
	if raised || m.Resolved.Level.Rank() > out.LevelOf().Rank() {
		out.Mode, out.Level, out.From = m.Resolved.Level, m.Resolved.Level, m.Resolved.From
		raised = true
	}
	changed := false
	for _, a := range m.Resolved.Areas {
		if a.Name == "" {
			continue
		}
		if i := slices.IndexFunc(out.Areas, func(x RigorArea) bool { return x.Name == a.Name }); i >= 0 {
			// The same name again: a run's configuration is fixed, so this is
			// the same rule, and the stricter reading of it is the one to
			// keep — rigor.Select's own dedupe, so the order boundRigorAreas
			// then sorts stays the record's honest one.
			if a.Level.Rank() > out.Areas[i].LevelOf().Rank() {
				out.Areas[i].Mode, out.Areas[i].Level = a.Level, a.Level
				changed = true
			}
			continue
		}
		out.Areas = append(out.Areas, RigorArea{Name: a.Name, Mode: a.Level, Level: a.Level, At: m.At})
		changed = true
	}
	if raised || changed {
		out.At = m.At
		if m.Revision != "" {
			out.Revision = m.Revision
		}
	}
	return out.bounded()
}

// Bounded is the block as a reader may show it. The record is an object the
// job's service account can write, so the block read back is not trusted to
// be the one Applied wrote: it must name a level from the vocabulary, a layer
// from KnownRigorFrom and one of D22's three moments, or it is not a decision
// anybody made and nil — the same answer the field's absence gives, which
// every reader already renders as "no record". What survives is bounded
// again: at most rigor.MaxAreas rules, each name at most rigor.MaxAreaName
// runes, an area whose own level or moment is unreadable dropped whole, the
// rules sorted strictest first (D23, which is what makes the first one the
// strictest whatever the block said), and the revision at most
// MaxRigorRevision runes.
//
// What comes back carries the level under both keys, whichever one the record
// it read held: a reader of the returned block may read either field, though
// LevelOf is still the rule (it is the one a caller holding a raw block, which
// this is not, has to follow).
func (r *RigorRecord) Bounded() *RigorRecord {
	if r == nil {
		return nil
	}
	level := r.LevelOf()
	if !rigor.Known(level) || !KnownRigorFrom(r.From) || !KnownRigorMoment(r.At) {
		return nil
	}
	out := &RigorRecord{Mode: level, Level: level, From: r.From, At: r.At, Revision: clipRunes(r.Revision, MaxRigorRevision)}
	out.Areas = boundRigorAreas(r.Areas)
	return out
}

// Clone is the block as its own copy, so a writer can fold a moment in
// without touching the record a caller is still holding. nil clones to an
// empty block, which is what Applied starts from.
func (r *RigorRecord) Clone() *RigorRecord {
	out := &RigorRecord{}
	if r != nil {
		*out = *r
		out.Areas = slices.Clone(r.Areas)
	}
	return out
}

// bounded is the write-side bound: the caps, without Bounded's refusal, so a
// moment folded in is already as small as a reader would make it — and ordered
// the way a reader reads it (D23), since this is the one place both sides go
// through (boundRigorAreas).
func (r *RigorRecord) bounded() *RigorRecord {
	out := r.Clone()
	out.Areas = boundRigorAreas(out.Areas)
	out.Revision = clipRunes(out.Revision, MaxRigorRevision)
	return out
}

func boundRigorAreas(in []RigorArea) []RigorArea {
	var out []RigorArea
	for _, a := range in {
		level := a.LevelOf()
		if a.Name == "" || !rigor.Known(level) || !KnownRigorMoment(a.At) {
			continue
		}
		out = append(out, RigorArea{Name: clipRunes(a.Name, rigor.MaxAreaName), Mode: level, Level: level, At: a.At})
	}
	// Sorted before the cap, so the bound keeps the strictest rules rather
	// than whichever ones the block happened to list first.
	sortAreas(out)
	if len(out) > rigor.MaxAreas {
		out = out[:rigor.MaxAreas]
	}
	return out
}

// sortAreas orders areas strictest first (D23), stable, so rules of equal rank
// keep the order they were applied in. It is called from boundRigorAreas
// alone, which is on every path that builds a block — the writer's `bounded`
// and the reader's Bounded both — so the order a reader depends on is one this
// file establishes wherever a block is built, never one a caller, or the job's
// service account, is trusted to have kept.
func sortAreas(areas []RigorArea) {
	slices.SortStableFunc(areas, func(x, y RigorArea) int { return y.LevelOf().Rank() - x.LevelOf().Rank() })
}

// clipRunes is s cut to at most n runes: runes and not bytes, so a cut never
// splits one in two (an area name is ASCII by validation, but a tampered
// record's is not).
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
