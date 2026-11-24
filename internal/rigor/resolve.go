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
	// FromRequest is the run's own request: fugaro run --mode, carried in the
	// task and handed to Resolve as Request.Level by the runner. A first run
	// that passes no flag sends none, so a record says this when a launch
	// asked for a level of its own — and a --pr follow-up's launcher writes
	// the request itself, so a record of one says this for the level that
	// follow-up keeps from its pull request when it passed no flag at all
	// (T5.2, D6, F14: followUpRigor, internal/cli). The record cannot tell the
	// two apart, and neither can this layer's word.
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

// Configured reports whether repo opted in to rigor levels (the "Opt-in gap"
// of docs/repo-override-rulings/spec.md §5.4). An unconfigured repository
// still resolves Default (D19), which the budget fraction reads, but it files
// no backlog item (the owner's ruling of 2026-10-24): filing is opt-in, so a
// repository that never named a level gains no docs/backlog/ it did not ask
// for. It is also the gate on the mode's findings table (internal/runner's
// findingsPolicyOf, D9), which is the same claim read the other way: a
// repository that named no level is not judged by the level's table.
//
// The question is about one repository and not about the project, because a
// layer may say something about one repository of it: the project layer's own
// default, and a project-wide area — a rule carrying a Match, which D20c puts
// in effect for every repository of the project — opt every repository in, and
// they answer for a run that names no repository as well. This repository is
// opted in by its own defaults.rigor.repos entry or by a member area naming
// it, and by nothing the layer writes for another repository: a per-repository
// map or a member list is an answer about the repositories it names, so a
// project that names one repository must not turn filing on for the rest of
// them.
//
// The entry is read as Resolve reads it (ok and Known), so an entry the
// vocabulary does not know — which only a hand-built Config reaches, the layer
// refusing it at load — is no answer about the repository and cannot make the
// opt-in and the resolved level disagree.
//
// The last tier is the repository's own file, which release A drops (S1): its
// rigor.default and its own path areas, and the empty rigor: block, which sets
// no level and is still the repository's own opt-in until release B retires it
// (XB3).
func (c Config) Configured(repo string) bool {
	if c.Project != "" || projectWideAmong(c.ProjectAreas) {
		return true
	}
	if repo != "" {
		if lvl, ok := c.ProjectRepos[repo]; ok && Known(lvl) {
			return true
		}
		for _, a := range c.ProjectAreas {
			if a.namesRepo(repo) {
				return true
			}
		}
	}
	return c.RepoBlock || c.Repo != "" || len(c.RepoAreas) > 0
}

// Areas is every rule in effect, the project layer's first. It is the list a
// record is read from (D24) and not what decides a level: Resolve reads the
// two lists apart, at the two ranks D20 as amended gives them, so a
// repository's own rule may lower a project area's level. What Select adds
// here is the dedupe and the order a record carries.
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
	// asks for none. It is the narrowest rank of all: it settles the level
	// whether it raises or lowers one a layer set (the owner's LD3), and only
	// a floor, which is outside every layer, holds it back (LF5) — unless it
	// was carried rather than asked for (Carried below).
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
	// (config.LevelInputs.Floor). A request lowers a level and never a floor
	// (D8, LF5), so a run the request took below the layers is held to the row
	// the layers resolved for it, which is what LayersRigor answers and what
	// the two readers of a floor hand to policy.Effective.ReviewFloorFor. A run
	// the request took to hotfix is held to hotfix's own row and to nothing
	// beside it (the owner's ruling of 2026-11-20), and an owner who wants
	// hotfix runs floored says so for hotfix: `fugaro rigor floor set hotfix
	// l2,l3`. What no level reaches is the security-sensitive diff's own
	// escalation, which turns the adversarial pass on for the run whatever
	// level it is at (internal/runner/plan.go).
	Level Rigor
	// Carried marks a Level the launch took from a record rather than one its
	// caller typed on this run: the level a --pr follow-up keeps from its pull
	// request, and the parent run's own answer a task filed from one carries
	// (internal/task's Spec.RigorCarried, T5.2, F14, D1844). A carried level
	// may raise the level the layers resolve and never lower it (LF5 §3a), so
	// a record older or weaker than the configuration it is read under leaves
	// the run where the configuration put it: the carry is a starting point
	// and not a request this run's caller made.
	Carried bool
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
// for it (reached, "" for the first), and "" when the layers' answer is not
// one a reader of the floors has to hold: the request did not take the run
// below them, or it took the run to a level that reads a floor row of its own
// (Rigor.PerRun, hotfix).
//
// It answers exactly when the request lowered the level below what the layers
// set, because that is the one shape in which a floor written for the layers'
// level could be left behind (D8, LF5: a request lowers a default and never a
// floor). A request that raised the level, or that asked for the level the
// layers already set, leaves the run at or above them, and there is no second
// row to read. A request a launch carried (Request.Carried) never lowers at
// all, so it leaves no row behind either. Hotfix is the owner's ruling of
// 2026-11-20: a hotfix run is held to hotfix's own row and nothing beside it,
// so a floor written for pro or critical does not reach it, and the answer
// here is none.
//
// It is the level whose floor holds beside the requested one's, which is why
// the two readers of an owner's floor — internal/runner's resolveLevelsForcing
// and the launcher's copy in internal/cli — ask it and hand the answer to
// policy.Effective.ReviewFloorFor. reached folds the way Resolve's own
// Reached does, so a caller asking at each of D22's moments keeps the
// strictest answer its layers have given.
func LayersRigor(cfg Config, req Request, reached Rigor) Rigor {
	if !Known(req.Level) || req.Level.PerRun() || req.Carried {
		return ""
	}
	layers := Resolve(cfg, Request{Repo: req.Repo, Paths: req.Paths, Reached: reached}).Level
	if req.Level.Rank() < layers.Rank() {
		return layers
	}
	return ""
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

// Resolve is the one resolver (D6, D20 as amended, LD3): the order is stated
// once, here, weakest first —
//
//	Fugaro's own default (D19: Pro)
//	the project layer's default
//	a project-wide area of the project layer (a rule carrying a match)
//	the project layer's own entry for the run's repository
//	a project area naming the run's repository in one of its members
//	the repository's own default (its fugaro.yaml)
//	a rule of the repository's own areas
//	the run's own request
//
// — and the narrowest rank of them that sets a level wins. Every rank replaces
// what the ones above it said, in either direction (D21): the order is a
// precedence and not an only-tightens rule, which is what a default is for.
// Within one rank the strictest rule that matched decides (D23), and the two
// area lists are read at their own ranks rather than as one set — which is
// what lets a repository's own rule lower a project area's level for its own
// paths (D20 as amended), and what keeps a project-wide area from outranking
// the project's answer for a whole repository (LD4, LF4).
//
// The project layer's areas are the one rank that is read in two places, and
// D3294 is why: a rule that names the run's repository in a member answers for
// a named part of it, so it ranks above the project's entry for the whole of
// it (LF4 put both kinds below it); a rule in effect through its Match alone
// answers for every repository of the project, which is the project's own
// claim about all of them, and keeps the rank LD4 gave it. The rank follows
// the rule's own shape — a rule that names this repository is about this
// repository, whatever path of it the run declared — so a rule carrying both a
// match and a member naming this repository is read at the member's rank,
// which is the more specific of the two claims. It is an amendment for
// projects that use both (D3294's own words), and the migration of a
// repository's own rigor.default and rigor.areas names every repository and
// path whose level moves either way before it writes (R5).
//
// Two things sit outside the order and are applied over it rather than by it:
// the owner's floors, which no layer may lower — the run's own request
// included, which is why the level the layers alone resolve is kept beside a
// request that went below them (LayersRigor) — and D22's reached value, which
// holds the level a run has already been at.
//
// The request is the strongest rank for every level a run may ask for
// (Rigor.PerRun says which those are), and it is so for a level its caller
// typed on this run. A level a launch carried from a record instead
// (Request.Carried: a --pr follow-up's, a filed task's) may only raise the
// level the layers resolved and never lower it (LF5 §3a, T5.2, D1844), so a
// carried level the layers already reach or outrank leaves the rank that set
// it named. A request that names no level, or the very level the order already
// resolved, leaves that rank named too: the narrower reason for the same
// answer.
//
// It is the only place any of that is decided: no other package orders levels,
// and this function is what docs/project-layer.md §2's one rule states for a
// reader.
func Resolve(cfg Config, req Request) Resolved {
	level, from := Default, FromDefault
	if Known(cfg.Project) {
		level, from = cfg.Project, FromProject
	}
	// The project layer's areas are read at the two ranks D3294 gives them: a
	// rule naming the run's repository in a member above the project's entry
	// for that repository, a rule in effect through its match alone below it.
	var projectWide, projectNaming []Area
	for _, a := range Select(cfg.ProjectAreas, req.Repo, req.Paths) {
		if a.namesRepo(req.Repo) {
			projectNaming = append(projectNaming, a)
			continue
		}
		projectWide = append(projectWide, a)
	}
	if len(projectWide) > 0 {
		level, from = Strictest(RigorsOf(projectWide)...), FromArea
	}
	// The project layer's own answer for one repository of the project sits
	// above a project-wide area, which answers for every repository, and below
	// a rule that names this repository, which answers for a part of it, and
	// below the repository's own file, which answers for itself (LD4, LF4,
	// D3294).
	if req.Repo != "" {
		if lvl, ok := cfg.ProjectRepos[req.Repo]; ok && Known(lvl) {
			level, from = lvl, FromProject
		}
	}
	if len(projectNaming) > 0 {
		level, from = Strictest(RigorsOf(projectNaming)...), FromArea
	}
	if Known(cfg.Repo) {
		level, from = cfg.Repo, FromRepo
	}
	if matched := Select(cfg.RepoAreas, req.Repo, req.Paths); len(matched) > 0 {
		level, from = Strictest(RigorsOf(matched)...), FromArea
	}
	// A carried request is the one that may only raise: it decides when it is
	// a level a request takes whole (Rigor.PerRun) or when it outranks what
	// the layers resolved, and is left out when they reach or outrank it.
	if Known(req.Level) && req.Level != level &&
		(req.Level.PerRun() || !req.Carried || req.Level.Rank() > level.Rank()) {
		level, from = req.Level, FromRequest
	}
	if Known(req.Reached) && req.Reached.Rank() > level.Rank() {
		level, from = req.Reached, FromReached
	}
	// The rules are kept whatever the level turns out to be (D23: the record
	// lists every area that matched), in the one order the record reads them
	// in.
	return Resolved{Level: level, From: from, Areas: Select(cfg.Areas(), req.Repo, req.Paths)}
}
