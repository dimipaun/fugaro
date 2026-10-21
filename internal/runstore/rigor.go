package runstore

import (
	"slices"

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
	// "default", the run's own "request", or "reached" for a mode an earlier
	// moment set that this one did not lower (D22).
	From rigor.Layer `json:"from"`
	// Areas is every rule that matched, strictest first (D23), each with the
	// moment it was first applied at. Absent — never an empty list — when
	// none matched: a reader must not see "there were rules" where the answer
	// is "there were none".
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

// RigorArea is one matched rule as the record carries it: the rule's name and
// the moment of the run it was first applied at (D24).
type RigorArea struct {
	Name string `json:"name"`
	At   string `json:"at"`
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
// moment it was first applied at, and the block's At is the last moment that
// changed it. A Moment the record cannot use — no mode, or a moment outside
// D22's three — leaves the block exactly as it was, so a nil receiver stays
// nil.
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
	added := false
	for _, name := range m.Resolved.Areas {
		if name == "" || slices.ContainsFunc(out.Areas, func(a RigorArea) bool { return a.Name == name }) {
			continue
		}
		out.Areas = append(out.Areas, RigorArea{Name: name, At: m.At})
		added = true
	}
	if raised || added {
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
// from rigor.Layers and one of D22's three moments, or it is not a decision
// anybody made and nil — the same answer the field's absence gives, which
// every reader already renders as "no record". What survives is bounded
// again: at most rigor.MaxAreas rules, each name at most
// rigor.MaxAreaName runes, an area whose own moment is unreadable dropped
// whole, and the revision at most MaxRigorRevision runes.
func (r *RigorRecord) Bounded() *RigorRecord {
	if r == nil {
		return nil
	}
	if !rigor.Known(r.Mode) || !rigor.KnownLayer(r.From) || !KnownRigorMoment(r.At) {
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
// moment folded in is already as small as a reader would make it.
func (r *RigorRecord) bounded() *RigorRecord {
	out := r.Clone()
	out.Areas = boundRigorAreas(out.Areas)
	out.Revision = clipRunes(out.Revision, MaxRigorRevision)
	return out
}

func boundRigorAreas(in []RigorArea) []RigorArea {
	var out []RigorArea
	for _, a := range in {
		if len(out) >= rigor.MaxAreas {
			break
		}
		if a.Name == "" || !KnownRigorMoment(a.At) {
			continue
		}
		out = append(out, RigorArea{Name: clipRunes(a.Name, rigor.MaxAreaName), At: a.At})
	}
	return out
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
