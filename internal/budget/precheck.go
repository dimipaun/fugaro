package budget

import (
	"context"
	"fmt"
	"math"
	"time"
)

// MinLaunchHeadroom is the least daily headroom a launch needs: the smallest
// lease the runner asks for (design §5.3).
const MinLaunchHeadroom Micros = 250_000

// ReasonForeignProject: the database's /fugaro/project is not the project the
// launcher is working for (or is not set): the wrong project's backend.
const ReasonForeignProject Reason = "foreign_project"

// Reader reads database nodes; *rtdb.Client is one. A null node is found=false.
type Reader interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

// PrecheckInput is what the launch pre-check judges.
type PrecheckInput struct {
	Project string    // the Fugaro project; /fugaro/project must equal it
	Slug    string    // the repository's storage slug (raw, as the path builders escape it)
	Capped  bool      // false for an oauth workflow: dollars are never capped, kills still apply
	Now     time.Time // the clock; its UTC day names the day counters
}

// PrecheckResult is the pre-check's verdict. When Refused, Reason, Scope and
// Detail say why; Notes are warnings that do not stop a launch. Detail and
// Notes quote values people wrote (a kill switch's reason): print them
// through the terminal-safe filters.
type PrecheckResult struct {
	Refused bool
	Reason  Reason
	Scope   Scope
	Detail  string
	Notes   []string
}

// Precheck answers, from the database alone, whether a launch is worth
// starting: it refuses the same things the rules would refuse the run's first
// lease of MinLaunchHeadroom (a kill switch in any mode; in enforce a missing
// cap, the per-run cap below the lease, or less than MinLaunchHeadroom of
// daily headroom in the repository's or the project's day), plus a database
// that belongs to another project. It reuses Evaluate, so it can not drift
// from the rules. It is advisory: the rules and the runner enforce the same
// things on the run itself, so a skipped or stale pre-check loosens nothing.
// An unreadable or malformed node is an error, never a pass (fail closed).
func Precheck(ctx context.Context, r Reader, in PrecheckInput) (PrecheckResult, error) {
	if in.Slug == "" || in.Project == "" {
		return PrecheckResult{}, fmt.Errorf("budget pre-check: no project or repository")
	}
	get := func(path string, out any) (bool, error) {
		found, err := r.Get(ctx, path, out)
		if err != nil {
			return false, fmt.Errorf("reading /%s: %w", path, err)
		}
		return found, nil
	}
	var project string
	if _, err := get(PathProject, &project); err != nil {
		return PrecheckResult{}, err
	}
	if project != in.Project {
		return PrecheckResult{Refused: true, Reason: ReasonForeignProject, Scope: ScopeGlobal,
			Detail: fmt.Sprintf("the budget database belongs to project %q, not %q (/fugaro/project)", project, in.Project)}, nil
	}

	var (
		caps  Caps
		kills Kills
		kg    Kill
		kr    Kill
		gc    GlobalCaps
		dc    DefaultCaps
		rc    RepoCaps
		st    State
		res   PrecheckResult
	)
	day := Day(in.Now)
	if _, err := get(PathMode, &caps.Mode); err != nil {
		return PrecheckResult{}, err
	}
	if found, err := get(PathKillGlobal, &kg); err != nil {
		return PrecheckResult{}, err
	} else if found {
		kills.Global = &kg
	}
	if found, err := get(PathKillRepo(in.Slug), &kr); err != nil {
		return PrecheckResult{}, err
	} else if found {
		kills.Repo = &kr
	}
	if in.Capped {
		reads := []struct {
			path string
			out  any
			set  func()
		}{
			{PathCapsGlobal, &gc, func() { caps.Global = &gc }},
			{PathCapsDefaults, &dc, func() { caps.Defaults = &dc }},
			{PathCapsRepo(in.Slug), &rc, func() { caps.Repo = &rc }},
			{PathSpendGlobal(day), &st.Global, func() {}},
			{PathSpendRepo(day, in.Slug), &st.Repo, func() {}},
		}
		for _, n := range reads {
			found, err := get(n.path, n.out)
			if err != nil {
				return PrecheckResult{}, err
			}
			if found {
				n.set()
			}
		}
		if caps.Mode != ModeEnforce && caps.Mode != ModeObserve {
			res.Notes = append(res.Notes, "the project's /config/mode is not set, so no cap is enforced yet (fugaro budget set --mode)")
		}
	} else {
		// oauth: dollars are uncapped, so no cap comparison applies; the
		// switches still do (Evaluate honours them in every mode).
		caps.Mode = ModeObserve
	}
	// maxReserve is not the launch's business (the runner sizes its leases
	// to it), so give Evaluate a limit that never binds.
	big := Micros(math.MaxInt64)
	caps.Limits = &Limits{MaxReserveMicros: &big}

	d := Evaluate(st, Write{Op: OpLease, Day: day, Amount: MinLaunchHeadroom}, caps, kills, in.Now)
	switch {
	case !d.Allow:
		res.Refused, res.Reason, res.Scope, res.Detail = true, d.Reason, d.Scope, d.Detail
	case d.Advisory != nil && in.Capped:
		res.Notes = append(res.Notes, "the project is in observe mode; under enforce this launch would be refused: "+d.Advisory.Detail)
	}
	return res, nil
}
