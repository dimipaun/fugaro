package runstore

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// The record's rigor block (docs/rigor-modes/implementation-plan.md T1.2,
// D22, D24, D25): the mode a run resolved, the layer that decided it, every
// area rule that matched, and the moment of the run each was known at. It is
// the run's own account of its rigor, written by the runner at the three
// moments D22 names and read by `fugaro runs ls`, `fugaro diagnose` and the
// console through internal/runview.
//
// Nothing acts on the mode yet: this slice records what was resolved and
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

// RigorRecord is the rigor mode a run resolved and where it came from.
type RigorRecord struct {
	// Mode is the mode the run resolved.
	Mode rigor.Mode `json:"mode"`
	// From is the layer that decided it (rigor.Layer): "area" for a rule the
	// run's paths matched, then "repo", "project", Fugaro's built-in
	// "default", and the run's own "request" (D6's raise-only flag). It is
	// never "reached": that is the resolver's answer to a later moment asking
	// again — "hold what you already have" (D22) — and the fold keeps the
	// earlier, narrower reason instead, which is the layer that actually set
	// the mode (D24's "the layer that won"). KnownRigorFrom is this set.
	From rigor.Layer `json:"from"`
	// Areas is every rule that matched, strictest first (D23), each with the
	// mode it sets and the moment it was first applied at. Absent — never an
	// empty list — when none matched: a reader must not see "there were
	// rules" where the answer is "there were none".
	Areas []RigorArea `json:"areas,omitempty"`
	// At is the moment of the resolution this block records (D24). It is the
	// last moment that changed the block, so a moment that resolved the same
	// answer leaves it alone.
	At string `json:"at"`
	// Revision is the revision of the configuration the mode was read from
	// (D25: the base branch, for every run kind), "origin/main" and the like.
	// Absent when the run could not name it.
	Revision string `json:"revision,omitempty"`
}

// Line is the block as one line of text: the mode, the layer that decided it,
// every area rule that matched with the moment it was applied at (D23, D24),
// the moment of the block and the revision the mode's configuration was read
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
	line := string(r.Mode) + ", from " + string(r.From)
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
// mode it sets, and the moment of the run it was first applied at (D24).
//
// The mode is here, and not only on the block, because D23's order is
// "strictest first" and a reader shows the first entry as the rule that
// decided: `fugaro runs ls`'s MODE column names it, and a later screen will
// too. Without the mode the order would be a claim no reader could check — a
// single moment's list arrives ordered (rigor.Select), but folding two moments
// concatenates them, and a block the job wrote is not ordered at all — which
// is why both the writer and the reader sort by this field (sortAreas).
type RigorArea struct {
	Name string     `json:"name"`
	Mode rigor.Mode `json:"mode"`
	At   string     `json:"at"`
}

// KnownRigorFrom reports whether l is a value a record's `from` may carry. It
// is rigor.KnownLayer minus the one value no record ever says: a block names
// the layer that won (D24), while "reached" is the resolver's way of holding
// the mode an earlier moment set (D22) — the fold keeps that earlier layer
// instead, so `from: "reached"` is a block no writer of this runner produces.
func KnownRigorFrom(l rigor.Layer) bool {
	return rigor.KnownLayer(l) && l != rigor.FromReached
}

// Moment is one of D22's three resolutions as the runner hands it to the
// record: the resolver's answer, the moment it was taken, and the revision of
// the configuration it read (D25).
type Moment struct {
	// Resolved is what rigor.Resolve answered. A Moment whose mode is not in
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
// higher mode replaces the mode and the layer that decided it; a moment that
// resolves a lower one changes nothing; a moment at the same mode keeps the
// layer already recorded, since that is the decision the run actually took,
// and still adds any area the block does not already name. An area keeps the
// moment it was first applied at and the stricter of the modes a moment gave
// it, and the block's At is the last moment that changed it. What comes back
// is the bounded block (bounded below), which is where D23's strictest-first
// order is established for a fold too: two moments concatenate, and the first
// entry is the one a reader names as the rule that decided.
//
// A Moment the record cannot use — no mode, or a moment outside D22's three —
// leaves the block exactly as it was, so a nil receiver stays nil.
func (r *RigorRecord) Applied(m Moment) *RigorRecord {
	if !rigor.Known(m.Resolved.Mode) || !KnownRigorMoment(m.At) {
		return r
	}
	out := r.Clone()
	raised := !rigor.Known(out.Mode)
	if raised || m.Resolved.Mode.Rank() > out.Mode.Rank() {
		out.Mode, out.From = m.Resolved.Mode, m.Resolved.From
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
			if a.Mode.Rank() > out.Areas[i].Mode.Rank() {
				out.Areas[i].Mode = a.Mode
				changed = true
			}
			continue
		}
		out.Areas = append(out.Areas, RigorArea{Name: a.Name, Mode: a.Mode, At: m.At})
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
// be the one Applied wrote: it must name a mode from the vocabulary, a layer
// from KnownRigorFrom and one of D22's three moments, or it is not a decision
// anybody made and nil — the same answer the field's absence gives, which
// every reader already renders as "no record". What survives is bounded
// again: at most rigor.MaxAreas rules, each name at most rigor.MaxAreaName
// runes, an area whose own mode or moment is unreadable dropped whole, the
// rules sorted strictest first (D23, which is what makes the first one the
// strictest whatever the block said), and the revision at most
// MaxRigorRevision runes.
func (r *RigorRecord) Bounded() *RigorRecord {
	if r == nil {
		return nil
	}
	if !rigor.Known(r.Mode) || !KnownRigorFrom(r.From) || !KnownRigorMoment(r.At) {
		return nil
	}
	out := &RigorRecord{Mode: r.Mode, From: r.From, At: r.At, Revision: clipRunes(r.Revision, MaxRigorRevision)}
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
		if a.Name == "" || !rigor.Known(a.Mode) || !KnownRigorMoment(a.At) {
			continue
		}
		out = append(out, RigorArea{Name: clipRunes(a.Name, rigor.MaxAreaName), Mode: a.Mode, At: a.At})
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
	slices.SortStableFunc(areas, func(x, y RigorArea) int { return y.Mode.Rank() - x.Mode.Rank() })
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
