package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The CLI's half of the build lease (T8; D1543, D1548, D1550, F19, F22). Every
// image build a CLI submits reserves its estimated cost before the submission
// and gives back what it did not spend after it, so the caps bound image builds
// the way they bound runs. What it reserves is the price the Cloud Billing
// catalog carries for the build's machine type and region, times the fixed
// build timeout, and a price that is missing or unknown refuses the build
// rather than defaulting to a number of this binary's or of any config
// (internal/budget/buildcost.go; D1550).
//
// The lease is judged by internal/budget's own predicate, over the nodes the
// database rules read, so a build meets the same per-run cap, day caps,
// maxReserve and switches a run does, and in observe mode the cap refusal comes
// back as an advisory the build proceeds under (D1548). The over-cap refusal
// names the estimate, the cap, the day's counted figure — this repository's
// jobs and its settled builds together — and the one command that raises that
// cap, and it exits non-zero, because nothing was submitted.
//
// Where the money lives: the reservation goes into the day counters a run's own
// lease writes (spend/<day>/repos/<slug>/counted and spend/<day>/global/
// counted), and settling or releasing moves it back out — a release lowers
// those counters, a spend raises the day's spent leaves. Those leaves are
// written with the etag the read carried and read again when a concurrent
// writer moved them first, which is this writer's version of the guard a run's
// write gets from the rules: the CLI writes as a budget admin, so no rule
// computes its delta for it, and without the etag two writers of one counter
// could lose each other's move. The lease's own node is written first and the
// counters after it, so a write that fails half way leaves the ledger saying
// what happened and the day counting money as still held — the fail-closed
// direction — and the refusal says which half was written.

// buildPricer is the price lookup a lease is sized from. internal/budget's
// Pricer is the real one: it reads the catalog once per command, on the
// caller's own credentials. It is an interface so a test can price a build
// with a book of its own, which is the one thing an estimator test cannot do
// from this side of the package.
type buildPricer interface {
	Rate(ctx context.Context, machineType, region string) (budget.BuildRate, error)
}

// imageCost is one command's build leases: one repository, one project's budget
// database, one price lookup.
type imageCost struct {
	lc *localcfg.Config
	db *rtdb.Client
	// repo is the repository as a person writes it (owner/name) and slug its
	// database key. The refusal names the first and the paths are the second,
	// and both come from the same checkout the build comes from.
	repo   string
	slug   string
	who    string
	pricer buildPricer
	now    func() time.Time
}

func newImageCost(lc *localcfg.Config, db *rtdb.Client, repo, slug, who string, pricer buildPricer) *imageCost {
	return &imageCost{lc: lc, db: db, repo: repo, slug: slug, who: who, pricer: pricer, now: time.Now}
}

// BuildOverCapError is the refusal of an image build no cap has room for
// (D1543). It carries what the operator needs to decide what to do — what the
// build reserves at most, the cap it met, what the day already counts, and the
// one command that raises that cap — and the command exits non-zero, because
// nothing was built.
type BuildOverCapError struct {
	Machine  string
	Region   string
	Estimate budget.Micros // the build timeout at the machine's rate
	Cap      budget.Micros // the cap the build met
	Scope    budget.Scope  // whose cap: the run's, the repository's day, or the project's day
	Spent    budget.Micros // the counter that cap compared; zero for the per-run cap
	Command  string        // the one `fugaro budget set` that raises it
	Detail   string        // the predicate's own words for this state
}

func (e *BuildOverCapError) Error() string {
	spent := ""
	switch e.Scope {
	case budget.ScopeRepo:
		spent = fmt.Sprintf(", with %s counted today by this repository's jobs and its settled builds", usdFixed(e.Spent.USD()))
	case budget.ScopeGlobal:
		spent = fmt.Sprintf(", with %s counted today across the project by jobs and settled builds", usdFixed(e.Spent.USD()))
	}
	return fmt.Sprintf("this image build of %s in %s reserves %s at most (the %d s build timeout at the machine's rate, read from the Cloud Billing catalog), and %s%s: raise the cap with `%s`, or build images on a smaller machine type (build.machine_type in fugaro.yaml) so each build reserves less",
		e.Machine, e.Region, usdFixed(e.Estimate.USD()), budget.MaxBuildSeconds, e.Detail, spent, e.Command)
}

// take reserves the estimate for one build and returns the lease that holds it.
// The advisory is the cap refusal observe mode let through, which the caller
// prints: the build proceeds, and the day counts what it reserved (D1548).
//
// A build this command may not submit — an unknown price, a switch, no room in
// the day or the run, a price read that failed — returns an error and writes
// nothing.
func (c *imageCost) take(ctx context.Context, id string) (budget.BuildLease, *budget.Decision, error) {
	machineType, region := c.lc.Build.MachineType, c.lc.BuildRegion()
	rate, err := c.pricer.Rate(ctx, machineType, region)
	var noPrice *budget.NoPriceError
	switch {
	case errors.As(err, &noPrice):
		return budget.BuildLease{}, nil, userErr("this image build is not submitted: %v", err)
	case err != nil:
		return budget.BuildLease{}, nil, remote(fmt.Errorf("the price of this image build could not be read, so it is not submitted: %w", err))
	}
	now := c.now()
	want := budget.BuildLease{
		State:       budget.BuildLeaseOpen,
		Repo:        c.slug,
		ID:          id,
		By:          c.who,
		MachineType: rate.MachineType,
		Region:      rate.Region,
		SKUs:        rate.SKUs,
		PriceReadAt: rate.ReadAt.UnixMilli(),
		Reserved:    rate.Estimate(),
		StartedAt:   now.UnixMilli(),
		Exp:         budget.BuildLeaseDeadline(now).UnixMilli(),
	}
	if want.Reserved <= 0 {
		return budget.BuildLease{}, nil, remote(fmt.Errorf("the rate for %s in %s came back as nothing, so there is no reservation to take", rate.MachineType, rate.Region))
	}

	path := want.Path()
	_, etag, found, err := c.readLease(ctx, path)
	if err != nil {
		return budget.BuildLease{}, nil, err
	}
	if found {
		// One id names one submission: a lease already under this id is a
		// command run twice with the same name, and the first lease may be
		// holding money this one would count twice.
		return budget.BuildLease{}, nil, userErr("a build lease already exists at /%s: a build id names one submission, so this build is not submitted under it; give the build another id, or give the first one back if its CLI died", path)
	}

	read, err := c.read(ctx, want.Day())
	if err != nil {
		return budget.BuildLease{}, nil, err
	}
	d := budget.EvaluateBuild(want, budget.Write{Op: budget.OpLease, Day: want.Day(), Amount: want.Reserved},
		read.State, read.Caps, read.Kills, now)
	if !d.Allow {
		return budget.BuildLease{}, nil, c.refuse(d, read.Caps, want, read.State)
	}
	if err := c.writeWith(ctx, path, etag, budget.BuildLease{}, want); err != nil {
		return budget.BuildLease{}, nil, err
	}
	return want, d.Advisory, nil
}

// settle records what the build really spent and gives the rest back. actual is
// the build's own figure — Cloud Build's billed build time — and never more
// than the lease holds.
func (c *imageCost) settle(ctx context.Context, l budget.BuildLease, actual budget.Micros) (budget.BuildLease, error) {
	after, err := l.Settle(actual)
	if err != nil {
		return budget.BuildLease{}, userErr("settling the build lease at /%s: %v", l.Path(), err)
	}
	return c.transition(ctx, l, after)
}

// release gives the whole reservation back: the build never ran, or its
// submission failed before Cloud Build billed anything.
func (c *imageCost) release(ctx context.Context, l budget.BuildLease) (budget.BuildLease, error) {
	after, err := l.Release()
	if err != nil {
		return budget.BuildLease{}, userErr("releasing the build lease at /%s: %v", l.Path(), err)
	}
	return c.transition(ctx, l, after)
}

// reclaim closes the leases a CLI that died mid-build left open, and reports
// how many it closed. A command calls it before it submits a build, so a
// reservation no process holds any more is given back before this build is
// judged against the day.
//
// A lease whose build may still be running is left alone: only a lease past its
// own deadline is abandoned (internal/budget's BuildLease.Abandoned), and a
// lease the predicate would refuse a release for — one from a day older than
// yesterday — is closed without a counter write, because the counter it
// reserved from belongs to a day no cap compares against any more and paying it
// back there would change a figure nothing reads.
func (c *imageCost) reclaim(ctx context.Context) (int, error) {
	var leases map[string]budget.BuildLease
	if _, err := c.db.Get(ctx, budget.PathBuildLeases(c.slug), &leases); err != nil {
		return 0, remote(fmt.Errorf("this repository's build leases could not be read, so no abandoned one could be given back: %w", err))
	}
	now, today := c.now(), budget.Day(c.now())
	closed := 0
	for _, id := range slices.Sorted(maps.Keys(leases)) {
		l := leases[id]
		if !l.Abandoned(now) || l.ID != id || l.Repo != c.slug {
			continue
		}
		after, err := l.Reclaim()
		if err != nil {
			continue // an open lease with no deadline: whether its build is still running is not something this can answer
		}
		if l.Day() < today-1 {
			if err := c.closeStale(ctx, l, after); err != nil {
				return closed, err
			}
			closed++
			continue
		}
		if _, err := c.transition(ctx, l, after); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// transition moves a lease this command holds: it re-reads the node, refuses if
// another process closed the lease while this one was building, and writes the
// move.
func (c *imageCost) transition(ctx context.Context, before, after budget.BuildLease) (budget.BuildLease, error) {
	path := before.Path()
	if path == "" {
		return budget.BuildLease{}, remote(fmt.Errorf("this build lease does not name the node it belongs at (repo %q, id %q)", before.Repo, before.ID))
	}
	cur, etag, found, err := c.readLease(ctx, path)
	if err != nil {
		return budget.BuildLease{}, err
	}
	switch {
	case !found:
		return budget.BuildLease{}, remote(fmt.Errorf("the build lease at /%s is gone: another process released it, and this build's money is accounted for there", path))
	case cur.State != before.State || cur.Released != before.Released || cur.Settled != before.Settled:
		return budget.BuildLease{}, remote(fmt.Errorf("the build lease at /%s is %s there and %s here: another process closed it while this one was building, so nothing was written", path, oneLine(cur.State), oneLine(before.State)))
	}
	return after, c.writeWith(ctx, path, etag, before, after)
}

// closeStale closes a reclaimed lease whose day no longer takes a release,
// without touching a counter: see reclaim.
func (c *imageCost) closeStale(ctx context.Context, before, after budget.BuildLease) error {
	path := before.Path()
	cur, etag, found, err := c.readLease(ctx, path)
	if err != nil || !found || cur.State != before.State {
		return err
	}
	if err := c.db.PutIfMatch(ctx, path, etag, after); err != nil {
		return remote(fmt.Errorf("closing the abandoned build lease at /%s: %w", path, err))
	}
	return nil
}

// writeWith writes the lease node and then the day's counters. The lease first,
// for the reason the file's comment gives: a failure after it leaves the ledger
// right and the day counting money as still held.
func (c *imageCost) writeWith(ctx context.Context, path, etag string, before, after budget.BuildLease) error {
	if err := c.db.PutIfMatch(ctx, path, etag, after); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return remote(fmt.Errorf("the build lease at /%s changed while it was written: run the build again", path))
		}
		return remote(fmt.Errorf("writing the build lease at /%s: %w", path, err))
	}
	day := after.Day()
	for _, counter := range []string{budget.PathSpendRepo(day, c.slug), budget.PathSpendGlobal(day)} {
		for _, m := range []struct {
			leaf  string
			delta budget.Micros
		}{
			{"/counted", after.Outstanding() - before.Outstanding()},
			{"/spent", after.Settled - before.Settled},
		} {
			if m.delta == 0 {
				continue
			}
			if err := c.addLeaf(ctx, counter+m.leaf, m.delta); err != nil {
				return remote(fmt.Errorf("%w\nthe build lease at /%s is written as %s and the day's counters are not, so the day still counts what this build gave back: `fugaro budget show` says what it holds, and the counters of %s are /%s and /%s",
					err, path, oneLine(after.State), budget.DayDate(day), budget.PathSpendRepo(day, c.slug), budget.PathSpendGlobal(day)))
			}
		}
	}
	return nil
}

// addLeaf moves one day-counter leaf by delta, conditionally: the leaf is read,
// written with the etag that read carried, and read again when a concurrent
// writer moved it first, so neither writer's move is lost. A counter is never
// written as a negative, which is a state the predicate refuses and no release
// can be right about.
func (c *imageCost) addLeaf(ctx context.Context, path string, delta budget.Micros) error {
	for attempt := 1; attempt <= setAttempts; attempt++ {
		var cur budget.Micros
		etag, found, err := c.db.GetETag(ctx, path, &cur)
		if err != nil {
			return fmt.Errorf("reading /%s: %w", path, err)
		}
		if !found && delta < 0 {
			return fmt.Errorf("/%s holds nothing, so the money this build gives back is not there to give", path)
		}
		if cur+delta < 0 {
			return fmt.Errorf("/%s holds %s, and this build gives back more than that", path, usdFixed(cur.USD()))
		}
		if err := c.db.PutIfMatch(ctx, path, etag, cur+delta); err == nil {
			return nil
		} else if !errors.Is(err, rtdb.ErrPrecondition) {
			return fmt.Errorf("writing /%s: %w", path, err)
		}
		// Another process moved this counter: read it again and add to what is
		// there now.
	}
	return fmt.Errorf("/%s kept changing under the write (%d attempts)", path, setAttempts)
}

// read is the caps, switches and counters a lease is judged against, for one
// day, read the way the database rules read them: an unreadable node refuses
// the build (fail closed).
func (c *imageCost) read(ctx context.Context, day int64) (budget.BuildRead, error) {
	read, err := budget.ReadBuildState(ctx, c.db, c.slug, day)
	if err != nil {
		return budget.BuildRead{}, remote(fmt.Errorf("the budget database could not be read, so this image build is not submitted (fail closed): %w", err))
	}
	return read, nil
}

func (c *imageCost) readLease(ctx context.Context, path string) (budget.BuildLease, string, bool, error) {
	var l budget.BuildLease
	etag, found, err := c.db.GetETag(ctx, path, &l)
	if err != nil {
		return budget.BuildLease{}, "", false, remote(fmt.Errorf("reading /%s: %w", path, err))
	}
	return l, etag, found, nil
}

// refuse words a decision that does not allow the build. A cap the build met is
// the one an operator can raise, so it gets BuildOverCapError and names the
// command; a switch, an unset cap and maxReserve are states to fix on their own
// terms, and the predicate's own words are the refusal.
func (c *imageCost) refuse(d budget.Decision, caps budget.Caps, want budget.BuildLease, st budget.BuildState) error {
	switch d.Reason {
	case budget.ReasonRepoDailyCap, budget.ReasonGlobalDailyCap, budget.ReasonRunCap:
		over := &BuildOverCapError{Machine: want.MachineType, Region: want.Region, Estimate: want.Reserved,
			Scope: d.Scope, Detail: oneLine(d.Detail)}
		switch d.Reason {
		case budget.ReasonRepoDailyCap:
			over.Command, over.Cap = c.repoDailyCommand(caps)
			over.Spent = st.Repo.Counted
		case budget.ReasonGlobalDailyCap:
			over.Command = "fugaro budget set --global --daily <usd>"
			over.Cap, _ = caps.GlobalDaily()
			over.Spent = st.Global.Counted
		case budget.ReasonRunCap:
			over.Command, over.Cap = c.perRunCommand(caps)
		}
		return &ExitError{Code: ExitUserError, Err: over}
	case budget.ReasonMaxReserve:
		return userErr("this image build is not submitted: %s; raise it with `fugaro budget set --global --max-reserve <usd>` once you have read what a %s build can cost, or build images on a machine type whose %d s timeout fits under the limit",
			oneLine(d.Detail), want.MachineType, budget.MaxBuildSeconds)
	default:
		return userErr("this image build is not submitted: %s", oneLine(d.Detail))
	}
}

// repoDailyCommand is the one `fugaro budget set` that raises the daily cap a
// build met, and the value it raises: the repository's own cap when it has one,
// else the default for repositories without one. The predicate compares the
// repository's day counter against the repository's own side first, so raising
// any other side would change nothing about this build.
func (c *imageCost) repoDailyCommand(caps budget.Caps) (string, budget.Micros) {
	if caps.Repo != nil && caps.Repo.DailyMicros != nil {
		return "fugaro budget set --repo " + c.repo + " --daily <usd>", *caps.Repo.DailyMicros
	}
	cap, _ := caps.RepoDaily()
	return "fugaro budget set --defaults --daily <usd>", cap
}

// perRunCommand is the same for the per-run cap. The predicate takes it as the
// smaller of two sides — the repository's own or the default for repositories
// without one on one side, the project's on the other (Caps.PerRun) — so the
// command names the side that is set and smaller, and the repository's own cap
// is distinguished from the defaults because raising the one the repository
// does not fall back on would change nothing.
func (c *imageCost) perRunCommand(caps budget.Caps) (string, budget.Micros) {
	repo, repoSet := caps.RepoPerRun()
	global, globalSet := caps.GlobalPerRun()
	switch {
	case repoSet && (!globalSet || repo <= global):
		if caps.Repo != nil && caps.Repo.PerRunMicros != nil {
			return "fugaro budget set --repo " + c.repo + " --per-run <usd>", repo
		}
		return "fugaro budget set --defaults --per-run <usd>", repo
	case globalSet:
		return "fugaro budget set --global --per-run <usd>", global
	default:
		// No per-run cap anywhere refuses with ReasonNoCap, not with this
		// error, so this arm is the unreachable one.
		return "fugaro budget set --defaults --per-run <usd>", 0
	}
}
