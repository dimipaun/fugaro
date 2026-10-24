package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The run's rigor mode: where it is read from, when it is resolved, and how
// the record gets it (docs/rigor-modes/implementation-plan.md T1.2, D22, D24,
// D25).
//
// Nothing acts on the mode in this slice. Decide still decides by today's
// rule, so a repository that configures POC is reviewed exactly as it was —
// the stricter direction — while its record says what it asked for. That is
// what lets this land alone. The one part that is real is the read: the mode's
// configuration comes from the base branch (D25), read as the repository's
// default branch — the revision no file in the checkout can name, which is what
// makes it the base rather than the branch under review's own answer — and
// never from the branch under review, which is the ground everything a later
// slice makes the mode do will stand on.

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

// recordRigor is D22's fold at the runner: one resolution, at one moment,
// folded into the record, which is where "a mode a run has reached never falls"
// lives (runstore.RigorRecord.Applied). The mode the run has already reached
// goes to the resolver as well (rigor.Request.Reached), so the resolver's own
// answer is monotone too and the record keeps the narrower reason when a later
// moment resolves the same mode.
func (r *run) recordRigor(at string, paths []string) {
	res := rigor.Resolve(r.rigorCfg, rigor.Request{Paths: paths, Reached: r.reachedRigor()})
	r.rec.Rigor = r.rec.Rigor.Applied(runstore.Moment{Resolved: res, At: at, Revision: r.rigorRev})
}

// reachedRigor is the mode this run has already resolved at an earlier moment;
// "" before the first one.
func (r *run) reachedRigor() rigor.Mode {
	if r.rec.Rigor == nil {
		return ""
	}
	return r.rec.Rigor.Mode
}

// declaredPaths are the paths the run says it will touch: D22's first moment,
// where `fugaro run --path` (repeatable) and the task's own list land.
//
// Nothing declares any yet. The flag, the task field it travels in, and the
// image-skew gate a runner-side read of a new spec field needs are T1.6's, so a
// run starts at its repository's or its project's value — exactly what D22 says
// a run that declares no path does. It is a function rather than a nil at the
// call site so the seam has one name, and one place to fill.
func (r *run) declaredPaths() []string { return nil }

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
	out := ResolveConfig(data, r.spec.ProjectLayer, r.d.Project)
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
