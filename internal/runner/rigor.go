package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The run's rigor mode: where it is read from, when it is resolved, and how
// the record gets it (docs/rigor-modes/implementation-plan.md T1.2, T1.4, D22,
// D24, D25).
//
// D22's three moments are the three call sites below: bootstrap, after a fix
// that changed the tree, and finalize. Decide still decides by today's rule, so
// a repository that configures POC is reviewed exactly as it was — the stricter
// direction — while its record says what it asked for. The mode does have
// readers, and none of them is here: the per-mode budget defaults (T6.1), the
// review round ceiling (T3.2) and the mode's findings policy (outcome.go, T3.1)
// ask the mode the run has reached, which is what makes the moments the one
// place that answer comes from. The read is this
// file's own part: the mode's configuration comes from the base branch (D25),
// read as the repository's default branch — the revision no file in the
// checkout can name, which is what makes it the base rather than the branch
// under review's own answer — and never from the branch under review, which is
// the ground everything a later slice makes the mode do stands on.

// bootstrapRigor resolves the run's mode from the base branch — for a run whose
// checkout is the branch under review's, the repository's default branch — and
// records it at D22's first moment, bootstrap.
//
// It never fails the run. The mode acts on nothing yet, so a base read that
// cannot be made is a warning and a mode resolved from what the run does know —
// the project layer, which travelled with the task and did not come from any
// branch — rather than a run that dies over a value nothing reads.
func (r *run) bootstrapRigor(ctx context.Context) {
	r.rigorCfg, r.rigorRev = r.baseRigor(ctx)
	r.recordRigor(runstore.RigorAtBootstrap, r.declaredPaths())
}

// finalizeRigor resolves the run's mode from the paths the run's own record
// measured — D22's third moment, finalize — and folds it in. It is called at
// the decision point, before Decide reads the run's gates, so a later slice's
// rules act on the answer the run's whole diff gave.
func (r *run) finalizeRigor() {
	r.recordRigor(runstore.RigorAtFinalize, r.changedPaths())
}

// roundRigor resolves the run's mode again after a fix that changed the tree —
// D22's second moment, "after a round that changed the tree"
// (docs/rigor-modes/implementation-plan.md T1.4) — and folds it in. It reads
// the run's own diff, the same read finalize makes, so the mode the remaining
// rounds are spent at is the one the diff had reached by then. Without it a run
// whose diff first matches a Critical area in round 2 buys nothing from the
// area until finalize, which is the cost D22 names when it rejects a
// two-moment reading.
//
// It never fails the run: a read that cannot be made is a warning and the mode
// stays where it is, as bootstrap's unreadable base leaves it (D25).
func (r *run) roundRigor(ctx context.Context) {
	paths, err := r.repo.ChangedFiles(ctx, r.workBase())
	if err != nil {
		r.d.Log.Warn("reading the run's changed paths after a round failed; the mode stays where it is", "err", r.redact(err.Error()))
		return
	}
	r.recordRigor(runstore.RigorAtRound, paths)
}

// recordRigor is D22's fold at the runner: one resolution, at one moment,
// folded into the record, which is where "a mode a run has reached never falls"
// lives (runstore.RigorRecord.Applied). The mode the run has already reached
// goes to the resolver as well (rigor.Request.Reached), so the resolver's own
// answer is monotone too and the record keeps the narrower reason when a later
// moment resolves the same mode.
//
// The run's own request (rigor.Request.Mode, the spec's Rigor: fugaro run
// --mode) is part of every moment, not only the first: the resolver's
// raise-only rule is what keeps a request from being outvoted by a later,
// weaker read, and giving it to all three moments is what makes a requested
// mode hold for the whole run rather than for the moment it arrived at.
//
// It is also where a raise is read (noteRigorRaise): the mode the run had
// reached before this moment and the resolver's answer for it are both in hand
// here, and nowhere else — the block records the last mode and the rules that
// matched, not the mode each moment resolved.
func (r *run) recordRigor(at string, paths []string) {
	before := r.reachedRigor()
	res := rigor.Resolve(r.rigorCfg, rigor.Request{Mode: r.spec.Rigor, Paths: paths, Reached: before})
	r.rec.Rigor = r.rec.Rigor.Applied(runstore.Moment{Resolved: res, At: at, Revision: r.rigorRev})
	r.noteRigorRaise(at, before, res)
}

// noteRigorRaise records the raise this moment took, if it took one, as the
// readiness rule reads it (T3.5). It is kept here rather than read back off the
// block at finalize because the block keeps no per-moment history, and which
// moment raised the mode is the one fact that says whether any review round was
// held at the mode the run ends at: the block's own `at` is the last moment
// that *changed* the block, which a moment that matches a rule changes without
// raising the mode.
//
// Bootstrap's own resolution is where a run starts rather than a raise, and a
// moment that resolves the mode the run has already reached takes none: D22's
// fold never falls, so the two answers differ only when this moment raised the
// mode. A moment that does raise it replaces any earlier raise — it is the last
// one, and the mode the run ends at is the one it is judged by.
//
// The rule named is the one this moment matched, its own strictest
// (rigor.Select), and only where the layer that decided it is an area: a raise
// by a layer, or by a floor outside every layer (D18), has no rule to name.
func (r *run) noteRigorRaise(at string, before rigor.Mode, res rigor.Resolved) {
	if before == "" || res.Mode == before {
		return
	}
	raise := &RigorRaise{Mode: res.Mode, At: at}
	if res.From == rigor.FromArea && len(res.Areas) > 0 {
		raise.Area = res.Areas[0].Name
	}
	r.rigorRaise = raise
}

// reachedRigor is the mode this run has resolved so far: bootstrap's answer
// once it has run, and "" before the first resolution. It is what the policy
// reads too — the mode a run gets its per-run default from is the one it
// resolved at bootstrap, and a policy a run's own stages could move would not
// be the ceiling it is (plan T6.1) — and what the resolver is given as
// Request.Reached, so its own answer is monotone as well.
func (r *run) reachedRigor() rigor.Mode {
	if r.rec.Rigor == nil {
		return ""
	}
	return r.rec.Rigor.Mode
}

// declaredPaths are the paths the run says it will touch: D22's first moment,
// where `fugaro run --path` (repeatable) and the task's own list land. The
// spec is where the launcher's flags travelled, and it is the whole of the
// input: the run's configuration is not consulted, and a path no area rule
// matches changes nothing. A path one does match sets the area's mode, which
// may be below the repository's: areas are defaults, not floors
// (docs/project-layer.md §2), so a declaration can lower the bootstrap mode,
// and finalize resolves again from the paths the run really changed.
//
// A run that declared none — every launch without the flag — starts at its
// repository's or its project's value, which is what D22 says a run that
// declares no path does.
func (r *run) declaredPaths() []string { return r.spec.Paths }

// changedPaths are the paths the run's own record measured
// (runstore.Record.ChangedFiles). D22's third moment reads exactly that field,
// and the bound is the record's: a run that changed more paths than a record
// carries resolves from the ones it kept, never from a set nothing measured.
func (r *run) changedPaths() []string {
	if r.rec.ChangedFiles == nil {
		return nil
	}
	return r.rec.ChangedFiles.Paths
}

// baseRigor is the run's rigor configuration as the *base branch* has it (D25)
// and the revision it was read from — "" when the read could not be made, which
// the record leaves `revision` absent for.
//
// A follow-up and a review run have already read that file: readBaseConfig
// fetched origin/<ref> and resolved fugaro.yaml from it, so the configuration
// the run is running on *is* the base's, and only the mode's half of it is
// taken from it. Neither reads a second time.
//
// A first run reads its other keys from the checked-out ref (bootstrap's own
// os.ReadFile), and the mode is the one key that must not come from there: the
// checkout is cut from the ref the run will push, so a branch that names itself
// as git.base_branch would be reading its own file as "the base branch's" —
// a branch that could set its own mode could set it to POC in the same pull
// request it wants merged (D25). D25's base branch is therefore read as the
// repository's default branch, through the read checkProject and resolvePolicy
// already make (defaultBranchFile): the file no file in the checkout can
// choose. A repository whose git.base_branch is not its default branch resolves
// the mode from the default branch's file nonetheless, and D24's revision is
// what the record says it read.
//
// A base that cannot be read, or whose fugaro.yaml does not resolve, falls back
// to the project layer alone — the layer came with the task and is no branch's
// — and names no revision, rather than falling back to the branch under
// review's own value, which is the one answer this must never give.
func (r *run) baseRigor(ctx context.Context) (rigor.Config, string) {
	if r.follow != nil || r.review {
		return r.cfg.RigorConfig(), "origin/" + r.cfg.Git.BaseBranch
	}
	rev, data, err := r.readBaseRigor(ctx)
	if err != nil {
		r.d.Log.Warn("the base branch's fugaro.yaml could not be read; the run resolves its rigor from the project layer alone",
			"revision", rev, "err", r.redact(err.Error()))
		return r.cfg.ProjectRigor(), ""
	}
	out := ResolveConfig(data, r.spec.ProjectLayer, r.d.Project, r.spec.BaseBranch)
	switch {
	case out.LayerErr != nil:
		err = out.LayerErr
	case len(out.Problems) > 0:
		err = fmt.Errorf("fugaro.yaml is invalid: %s", problemsText(out.Problems))
	}
	if err != nil {
		r.d.Log.Warn("the base branch's fugaro.yaml does not resolve; the run resolves its rigor from the project layer alone",
			"revision", rev, "err", r.redact(err.Error()))
		return r.cfg.ProjectRigor(), ""
	}
	return out.Cfg.RigorConfig(), rev
}

// readBaseRigor is the file the mode is read from and the revision it was read
// at. A run with a project reads the repository's default branch: the checkout
// is the branch under review, and the base it names for itself is its own to
// choose. A local run reads the checkout's own base ref, as it did before the
// default-branch read arrived: it has no project, so checkProject and
// resolvePolicy both skip that read there, and it has no branch under review
// for the mode to be protected from — the checkout is the developer's own.
//
// The revision is "" when the repository never named a default branch, which is
// the only case where the read has no revision to report.
func (r *run) readBaseRigor(ctx context.Context) (rev string, data []byte, err error) {
	if r.d.Project == "" {
		rev = "origin/" + r.cfg.Git.BaseBranch
		data, err = r.repo.ShowFile(ctx, rev, "fugaro.yaml")
		return rev, data, err
	}
	data, def, err := r.defaultBranchFile(ctx)
	if def != "" {
		rev = "origin/" + def
	}
	return rev, data, err
}
