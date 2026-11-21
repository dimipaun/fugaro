package rigor

import "slices"

// Layer names where a resolved level came from: D24's `from`, the second of
// the three parts the one resolved value carries (the level, the layer that
// won, and the areas that matched).
//
// The three words are the ones internal/config's Resolution already uses for
// a resolved value's source (SourceDefault, SourceProject, SourceRepo), so a
// run record and a config diff say the same thing about the same layer; this
// package adds the two ranks that resolution has no equivalent of, an area
// rule and the run's own request.
type Layer string

// The layers a level may come from, narrowest last for the three that are
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
	// Request.Level by the runner. A first run that passes no flag sends none,
	// so a record says this when a launch asked for more than its
	// configuration did — and a --pr follow-up's launcher writes the request
	// itself, so a record of one says this for the level that follow-up keeps
	// from its pull request when it passed no flag at all (T5.2, D6, F14:
	// followUpRigor, internal/cli). The record cannot tell the two apart, and
	// neither can this layer's word.
	FromRequest Layer = "request"
	// FromReached is the level this run had already reached at an earlier
	// moment, kept because a level never falls within a run (D22). It is not
	// a layer: it says the run is holding the answer an earlier resolution
	// gave, and that resolution's own layer is what named the level — which
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
// a level (D20), each with its own area list, and the project layer's map of
// one level per repository of the project.
//
// The two area lists are kept apart rather than merged into one because both
// are in effect at once (D20c): a repository rule and a project rule that
// both match resolve by D23's strictest rule, and the repository's list does
// not replace the project's the way a list in a narrower layer otherwise
// does (docs/project-layer.md §4). Resolve is where they are read together.
type Config struct {
	// Project is defaults.rigor.default; "" sets none.
	Project Rigor
	// ProjectAreas is defaults.rigor.areas; nil sets none.
	ProjectAreas []Area
	// ProjectRepos is defaults.rigor.repos: the level the project layer sets
	// for one repository of the project, by the "owner/name" a run's own
	// repo carries. nil sets none. It is the project's own answer for a
	// whole repository, beside the areas that answer for parts of them, and
	// it is keyed by a repository name the layer cannot check against the
	// published allow_repos — the shape is checked where the layer is
	// written, and `fugaro doctor` warns about a name the policy does not
	// admit.
	ProjectRepos map[string]Rigor
	// Repo is fugaro.yaml's rigor.default; "" sets none.
	Repo Rigor
	// RepoAreas is fugaro.yaml's rigor.areas; nil sets none.
	RepoAreas []Area
	// RepoBlock is whether fugaro.yaml wrote a rigor: block at all, even
	// one that sets nothing: the block is a repository's opt-in, which
	// Configured reads.
	RepoBlock bool
}

// Configured reports whether a repository opted in to rigor levels: its
// fugaro.yaml has a rigor: block, or the project layer sets a rigor default,
// area or per-repository level. An unconfigured repository still resolves
// Default (D19), which the budget fraction reads, but it files no backlog item
// (the owner's ruling of 2026-10-24): filing is opt-in, so a repository that
// never named a level gains no docs/backlog/ it did not ask for.
func (c Config) Configured() bool {
	return c.RepoBlock || c.Repo != "" || len(c.RepoAreas) > 0 || c.Project != "" || len(c.ProjectAreas) > 0 || len(c.ProjectRepos) > 0
}

// Areas is every rule in effect, the project layer's first. The order
// carries no meaning: Select orders the matches by level, and D23 is what
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
	// Level is the level the run's own task asks for (fugaro run --mode); ""
	// asks for none. It may raise and never lower (D6, F14): the fail-closed
	// direction, kept for every run kind.
	//
	// Hotfix is the one level the request takes a run to outright rather than
	// by outranking what the layers resolved (Rigor.PerRun): it is off the
	// axis and ranks below every on-axis level, so a rank comparison would
	// always discard it, while an operator asking for it is saying
	// production is on fire. The owner's ruling of 2026-11-13 is that this
	// door needs no opt-in — the gate D12 named is dropped rather than built
	// (docs/rigor-command/implementation-plan.md R7) — so `--mode hotfix`
	// decides for the run that asked for it.
	//
	// The floors stay outside every layer and are applied over this
	// resolution, not by it, and they are keyed by the level the run is at
	// (config.LevelInputs.Floor). A request raises a level and never lowers a
	// floor (D8, LF5), so moving the level may not move the run off a floor: a
	// run a PerRun request takes to hotfix is held to hotfix's row *and* to
	// the row the layers resolved for it, which is what LayersRigor answers and
	// what the two readers of a floor hand to
	// policy.Effective.ReviewFloorFor. An owner who wants hotfix runs floored
	// says so for hotfix as well: `fugaro rigor floor set hotfix l2,l3`. What
	// no level reaches is the security-sensitive diff's own escalation, which
	// turns the adversarial pass on for the run whatever level it is at
	// (internal/runner/plan.go).
	Level Rigor
	// Repo is the repository the run is for, in the "owner/name" spelling a
	// task's own Repo carries; "" for a caller that has none. It is what the
	// project layer's members are matched on (LD4) and what
	// Config.ProjectRepos is read by, so a resolution for a run that names
	// no repository falls back to what the layers set for the project and
	// for the paths alone — which is what every caller resolved before
	// members and the per-repository map existed.
	Repo string
	// Paths are the paths the run touches, matched against the area rules
	// (D20c): the paths a caller declared at bootstrap, and the run's own
	// measured diff at finalize (D22).
	Paths []string
	// Reached is the level the run has already resolved at an earlier
	// moment; "" for the first resolution. A level never falls within a run
	// (D22, D23's own only-tightens shape), so a later resolution that comes
	// back weaker leaves the reached value standing.
	Reached Rigor
}

// LayersRigor is the level the layers alone resolve for req — the run's own
// request left out of the resolution — given the answer an earlier moment gave
// for it (reached, "" for the first), and "" when the request asks for no level
// a run takes whole (Rigor.PerRun). For every other request the layers' answer
// is the level the run is at, since a request may only raise it, and there is
// no second row for a reader of the floors to hold beside the first.
//
// It is the level whose floor holds beside the requested one's (D8, LF5: a
// request raises a level and never lowers a floor), which is why the two
// readers of an owner's floor — internal/runner's resolveLevelsForcing and the
// launcher's copy in internal/cli — ask it and hand the answer to
// policy.Effective.ReviewFloorFor. reached folds the way Resolve's own
// Reached does, so a caller asking at each of D22's moments keeps the
// strictest answer its layers have given.
func LayersRigor(cfg Config, req Request, reached Rigor) Rigor {
	if !req.Level.PerRun() {
		return ""
	}
	return Resolve(cfg, Request{Paths: req.Paths, Reached: reached}).Level
}

// Resolved is one resolution (D24): the level, the layer that won, and every
// area that matched, strictest first.
type Resolved struct {
	// Level is the resolved level; it is never empty.
	Level Rigor
	// From is the layer that set Level.
	From Layer
	// Areas is every area rule that matched, strictest first (D23), each
	// with the level it sets — not just its name. The level is what lets a
	// caller that folds two resolutions together keep the order D23 states
	// (internal/runstore's RigorRecord.Applied), and it is already here:
	// Select answers with the rules themselves. nil when none matched, so a
	// record keeps the field absent rather than carrying an empty list.
	Areas []Area
}

// Resolve is the one resolver (D6): the narrowest layer that sets the level
// wins — an area the run's repo and paths match, then the repository's own
// value, then the project layer's value for that repository or for the whole
// project, then Fugaro's own default — and on top of that the run's own
// request may raise and never lower it, except for Hotfix, which the request
// takes whole (Rigor.PerRun).
//
// A narrower layer may set a *lower* level than a wider one (D21): the order
// is a precedence, not an only-tightens rule — that is what a default is
// for, and a project that wants a floor says so with a floor rather than
// with a default. The only-tightens rule the levels do share belongs to the
// floors, which are outside every layer and which this function does not
// carry.
//
// It is the only place any of that is decided: no other package orders
// levels, and this function is what docs/project-layer.md §2's one rule
// states for a reader.
func Resolve(cfg Config, req Request) Resolved {
	level, from := Default, FromDefault
	if Known(cfg.Project) {
		level, from = cfg.Project, FromProject
	}
	// The project layer's own answer for one repository of the project is
	// narrower than its default for all of them (LD4, LF4): it is read
	// beside the areas, which answer for parts of repositories, and below
	// the repository's own file, which answers for itself.
	if req.Repo != "" {
		if lvl, ok := cfg.ProjectRepos[req.Repo]; ok && Known(lvl) {
			level, from = lvl, FromProject
		}
	}
	if Known(cfg.Repo) {
		level, from = cfg.Repo, FromRepo
	}
	var areas []Area
	if matched := Select(cfg.Areas(), req.Repo, req.Paths); len(matched) > 0 {
		// An area replaces the value below it whole, in either direction
		// (D21), and the rules are kept whatever the level turns out to be
		// (D23: the record lists every area that matched).
		level, from = Strictest(RigorsOf(matched)...), FromArea
		areas = matched
	}
	if Known(req.Level) && (req.Level.PerRun() || req.Level.Rank() > level.Rank()) {
		// Raise-only for every on-axis level: a request equal to the resolved
		// level leaves the layer that already set it named, since that is the
		// narrower answer. Hotfix is not raised to and not compared at all —
		// it is taken whole, so a request for it decides whether the layers
		// resolved pro or critical (Rigor.PerRun).
		level, from = req.Level, FromRequest
	}
	if Known(req.Reached) && req.Reached.Rank() > level.Rank() {
		level, from = req.Reached, FromReached
	}
	return Resolved{Level: level, From: from, Areas: areas}
}
