package rigor

import "slices"

// Layer names where a resolved mode came from: D24's `from`, the second of
// the three parts the one resolved value carries (the mode, the layer that
// won, and the areas that matched).
//
// The three words are the ones internal/config's Resolution already uses for
// a resolved value's source (SourceDefault, SourceProject, SourceRepo), so a
// run record and a config diff say the same thing about the same layer; this
// package adds the two ranks that resolution has no equivalent of, an area
// rule and the run's own request.
type Layer string

// The layers a mode may come from, narrowest last for the three that are
// layers, then the two that are not.
const (
	// FromDefault is Fugaro's own default (D19: Pro).
	FromDefault Layer = "default"
	// FromProject is defaults.rigor.default of the project layer.
	FromProject Layer = "project"
	// FromRepo is rigor.default of the repository's own fugaro.yaml.
	FromRepo Layer = "repo"
	// FromArea is a rigor.areas rule the run's paths matched.
	FromArea Layer = "area"
	// FromRequest is the run's own request, which may raise and never lower
	// (D6): fugaro run --mode, carried in the task and handed to Resolve as
	// Request.Mode by the runner. A launcher that passes no flag sends none,
	// so a record says this only when a launch asked for more than its
	// configuration did.
	FromRequest Layer = "request"
	// FromReached is the mode this run had already reached at an earlier
	// moment, kept because a mode never falls within a run (D22). It is not
	// a layer: it says the run is holding the answer an earlier resolution
	// gave, and that resolution's own layer is what named the mode — which
	// is why a run record never carries this value: the fold keeps the
	// earlier, narrower reason (internal/runstore's KnownRigorFrom).
	FromReached Layer = "reached"
)

// Layers is every value From may take: the three layers from the narrowest,
// then the two that are not layers. It is the closed set one place names,
// and every reader of a resolution checks against it — a run record is an
// object the job can write, so internal/runstore bounds a `from` read back
// by this list rather than by one of its own that would drift from it the
// day a layer is added. The one exception is FromReached, which a
// resolution carries and a record never says (D24: a record names the layer
// that won), so runstore checks KnownRigorFrom = KnownLayer minus that
// value.
var Layers = []Layer{FromArea, FromRepo, FromProject, FromDefault, FromRequest, FromReached}

// KnownLayer reports whether l is a layer a resolution may name.
func KnownLayer(l Layer) bool { return slices.Contains(Layers, l) }

// Config is the rigor configuration of one run: the two layers that may set
// a mode (D20), each with its own area list.
//
// The two area lists are kept apart rather than merged into one because both
// are in effect at once (D20c): a repository rule and a project rule that
// both match resolve by D23's strictest rule, and the repository's list does
// not replace the project's the way a list in a narrower layer otherwise
// does (docs/project-layer.md §4). Resolve is where they are read together.
type Config struct {
	// Project is defaults.rigor.default; "" sets none.
	Project Mode
	// ProjectAreas is defaults.rigor.areas; nil sets none.
	ProjectAreas []Area
	// Repo is fugaro.yaml's rigor.default; "" sets none.
	Repo Mode
	// RepoAreas is fugaro.yaml's rigor.areas; nil sets none.
	RepoAreas []Area
	// RepoBlock is whether fugaro.yaml wrote a rigor: block at all, even
	// one that sets nothing: the block is a repository's opt-in, which
	// Configured reads.
	RepoBlock bool
}

// Configured reports whether a repository opted in to rigor modes: its
// fugaro.yaml has a rigor: block, or the project layer sets a rigor default
// or area. An unconfigured repository still resolves Default (D19), which the
// budget fraction reads, but it files no backlog item (the owner's ruling of
// 2026-10-24): filing is opt-in, so a repository that never named a mode
// gains no docs/backlog/ it did not ask for.
func (c Config) Configured() bool {
	return c.RepoBlock || c.Repo != "" || len(c.RepoAreas) > 0 || c.Project != "" || len(c.ProjectAreas) > 0
}

// Areas is every rule in effect, the project layer's first. The order
// carries no meaning: Select orders the matches by mode, and D23 is what
// makes that enough (a project rule and a repository rule that both match
// resolve to one answer whatever order they were read in).
func (c Config) Areas() []Area {
	if len(c.ProjectAreas) == 0 {
		return c.RepoAreas
	}
	if len(c.RepoAreas) == 0 {
		return c.ProjectAreas
	}
	out := make([]Area, 0, len(c.ProjectAreas)+len(c.RepoAreas))
	out = append(out, c.ProjectAreas...)
	return append(out, c.RepoAreas...)
}

// Request is what the run itself brings to the resolution, as opposed to
// what its configuration says.
type Request struct {
	// Mode is the mode the run's own task asks for (fugaro run --mode); ""
	// asks for none. It may raise and never lower (D6, F14): the fail-closed
	// direction, kept for every run kind.
	//
	// Hotfix is off the axis and ranks below every on-axis mode, so a
	// request for it raises nothing. That is deliberate: reaching Hotfix is
	// an operator invoking it under a repository's own opt-in (D12), not a
	// request that outranks an area, and no request may carry a run below a
	// floor.
	Mode Mode
	// Paths are the paths the run touches, matched against the area rules
	// (D20c): the paths a caller declared at bootstrap, and the run's own
	// measured diff at finalize (D22).
	Paths []string
	// Reached is the mode the run has already resolved at an earlier
	// moment; "" for the first resolution. A mode never falls within a run
	// (D22, D23's own only-tightens shape), so a later resolution that comes
	// back weaker leaves the reached value standing.
	Reached Mode
}

// Resolved is one resolution (D24): the mode, the layer that won, and every
// area that matched, strictest first.
type Resolved struct {
	// Mode is the resolved mode; it is never empty.
	Mode Mode
	// From is the layer that set Mode.
	From Layer
	// Areas is every area rule that matched, strictest first (D23), each
	// with the mode it sets — not just its name. The mode is what lets a
	// caller that folds two resolutions together keep the order D23 states
	// (internal/runstore's RigorRecord.Applied), and it is already here:
	// Select answers with the rules themselves. nil when none matched, so a
	// record keeps the field absent rather than carrying an empty list.
	Areas []Area
}

// Resolve is the one resolver (D6): the narrowest layer that sets the mode
// wins — an area the run's paths match, then the repository's own value,
// then the project default, then Fugaro's own default — and on top of that
// the run's own request may raise and never lower it.
//
// A narrower layer may set a *lower* mode than a wider one (D21): the order
// is a precedence, not an only-tightens rule — that is what a default is
// for, and a project that wants a floor says so with a floor rather than
// with a default. The only-tightens rule the modes do share belongs to the
// floors, which are outside every layer and which this function does not
// carry.
//
// It is the only place any of that is decided: no other package orders
// modes, and this function is what docs/project-layer.md §2's one rule
// states for a reader.
func Resolve(cfg Config, req Request) Resolved {
	mode, from := Default, FromDefault
	if Known(cfg.Project) {
		mode, from = cfg.Project, FromProject
	}
	if Known(cfg.Repo) {
		mode, from = cfg.Repo, FromRepo
	}
	var areas []Area
	if matched := Select(cfg.Areas(), req.Paths); len(matched) > 0 {
		// An area replaces the value below it whole, in either direction
		// (D21), and the rules are kept whatever the mode turns out to be
		// (D23: the record lists every area that matched).
		mode, from = Strictest(ModesOf(matched)...), FromArea
		areas = matched
	}
	if Known(req.Mode) && req.Mode.Rank() > mode.Rank() {
		// Raise-only: a request equal to the resolved mode leaves the layer
		// that already set it named, since that is the narrower answer.
		mode, from = req.Mode, FromRequest
	}
	if Known(req.Reached) && req.Reached.Rank() > mode.Rank() {
		mode, from = req.Reached, FromReached
	}
	return Resolved{Mode: mode, From: from, Areas: areas}
}
