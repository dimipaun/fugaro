package batch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	batch "google.golang.org/api/batch/v1"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/backend"
)

// This file is the reaper and the queue deadline (design
// docs/design/compute-backend.md §1.4, §3.4, C8, C14), the residual path
// that exists because Batch's own teardown -- which the owner measured on
// Check 57a: a job's VM is gone 3 to 26 seconds after the job reaches a
// terminal state -- is measured, not guaranteed. The reaper is a SAFETY NET
// for the leak Batch did not clean up, never the path a finished run's VM is
// expected to go through; and it is deliberately narrow, because everything
// it deletes is someone's data or someone's billing.
//
// What it will delete, and what it refuses:
//
//   - It is JOB-FIRST. Every decision is taken from a job's own state, never
//     from an instance listing alone, and the instance pass runs after the
//     job pass: a job that must go is deleted (jobs.delete) before any
//     instance is looked at, because deleting a Batch-created instance
//     underneath its own job races Batch's management -- Batch is mid-teardown,
//     sees an instance vanish, and reports the task failed for a reason that
//     has nothing to do with the run (§3.4).
//   - It deletes an instance only when it carries BOTH this installation's
//     marker (fugaro=managed) AND a fugaro_run label, and only when no job it
//     can see is still live for that run. A VM with no run label, a VM whose
//     fugaro label is another installation's, an instance whose job is still
//     running, and a VM young enough that Batch may simply be mid-teardown are
//     all REFUSED and reported (§3.4: "never a VM or job it cannot
//     attribute").
//   - It deletes -- it cannot cancel -- a job that has sat in QUEUED past the
//     queue deadline (§1.4). jobs.delete is the only verb available for it:
//     jobs.cancel is not a permission Google has (see the permissions below),
//     so a role that named it could never be created. That is the one case
//     where erasing the record is affordable, because a job that never left
//     QUEUED never ran a task -- but the run is NOT what is erased with it:
//     launch.json is written by the launcher, not by the job (cli/run.go:
//     Launch, then WriteLaunch), so it survives the delete and fugaro ls,
//     diagnose and watch, which read the runs bucket, keep showing the run.
//     What the job's record did hold is its own state and status message, and
//     those go with it. result.json never existed (the runner writes it in
//     finalize) and no VM ever did (Batch creates them at SCHEDULED). A job
//     that DID leave QUEUED keeps its record, and the queue deadline never
//     deletes one (deleteQueued reads the job back before it acts).
//   - It never deletes a finished job's record. That record is history, and
//     the VM of a finished job is Batch's own teardown; §3.4's "the reaper
//     deletes the job" is about the jobs it has decided must go (a stuck
//     SCHEDULED/RUNNING job past every deadline it could legitimately have,
//     and a stuck DELETION_IN_PROGRESS) -- for those the delete IS the
//     teardown, so it comes first. A terminal job whose VM leaked is handled
//     by the instance pass, which deletes the VM and leaves the record: a
//     record that outlives its VM is a history bug, not a billing bug.
//   - It refuses ambiguity everywhere: a job or VM whose time it cannot read,
//     a job whose state this build does not know, a job whose two label
//     fields disagree -- none of those are deleted, and each is named in the
//     report with the reason.
//
// It is idempotent: everything it deletes is gone afterwards, every refusal
// is refusable again, and a second pass over the same project reports the
// same refusals and no actions. DryRun lists what a real pass would delete
// and touches nothing at all.
//
// The permissions this needs are Task 6's to grant (§3.7, C22); the reaper is
// its own dedicated, project-wide service account and must not share the
// launcher's or the job's. Exactly, and nothing more:
//
//   - batch.jobs.list and batch.jobs.get -- to read every Fugaro job in the
//     region, which is where each decision comes from.
//   - batch.jobs.get and batch.jobs.delete -- the queue deadline (§1.4) needs
//     both. A job stuck QUEUED past the deadline counts against max_parallel
//     and can wait out Batch's own 2-day ceiling, and the verb that clears it
//     is jobs.delete: jobs.cancel is NOT a permission Google defines. The
//     API's *methods* on a job do include cancel -- which is why `gcloud batch
//     jobs cancel` exists and §9 of the design uses it -- but the permissions
//     it defines on jobs are create, get, list and delete, and
//     `batch.jobs.cancel` is in no permission list and no predefined role: the
//     six-permission role this file used to describe could never be created --
//     which is what kept --provision-batch refused until the role was fixed.
//     delete is also the destructive one, and the reason for a separate
//     account: it erases a job and its history, so it belongs to a job the
//     reaper has decided must go -- a stuck SCHEDULED/RUNNING job, a stuck
//     deletion, or a job that never left QUEUED -- and to nothing else. The
//     get is what keeps the last of those honest: the reaper reads a queued
//     job back before deleting it and leaves one that has left QUEUED alone,
//     so a run that started between the listing and the delete is never the
//     one erased.
//   - compute.instances.list -- the project-wide aggregated list, which is
//     how a Fugaro-labelled VM in any of the region's zones is found.
//   - compute.instances.delete -- the other destructive one.
//
// Five permissions, and the grant must be a custom role that carries exactly
// these: roles/batch.jobsEditor and roles/compute.instanceAdmin.v1 -- the
// roles Google's own pages name for creating, cancelling and deleting a job
// and for deleting an instance -- are both far wider. jobsEditor also carries
// batch.jobs.create, i.e. the power to run any container in the project, which
// is the launcher-shaped power §3.7 spends its length distinguishing; and
// instanceAdmin.v1 carries compute.instances.create and the rest of the
// instance lifecycle, including compute.instances.setMetadata, which is how
// SSH keys reach a VM (the full permission list of each built-in role is
// Google's and was NOT re-read for this task: UNVERIFIED). A reaper account
// that could create jobs and VMs would be a second launcher, not a janitor.
// Design §3.7 and docs/backends.md state the same five for the same reason:
// this header is where a reviewer checks the role against the code.
//
// IAM cannot scope a delete by label. Nothing in the grant says "only
// fugaro=managed": the five permissions are project-wide, `jobs.delete` erases
// any job in the project and `instances.delete` any VM in it, and no Condition
// can express "the resource carries this label" (Conditions match resource
// names and types -- design §3.7's Check 57a step 9 question -- not labels).
// What confines the reaper to this installation's resources is therefore this
// file and only this file: listJobs filters on the marker and re-checks it
// client-side, reapJob refuses what it cannot attribute, and reapInstances
// re-checks both labels before any delete. A reviewer who trusts the reaper's
// narrowness is trusting reapJob's and reapInstances' refusals, not the IAM.
//
// Nothing here reads a secret, and nothing here needs Secret Manager, storage
// or logging: the reaper's whole surface is the five permissions above.

// ReapMargin is the margin the reaper's age filter adds on top of the longest
// a run may be active (see NewReaper's base), its slack and the queue
// deadline: the VM boot and image pull the design budgets 120 s for (§1.2),
// Batch's own teardown after a terminal state (Check 57a measured 3 to 26 s),
// and the settle window a
// listing's own lag needs. A VM younger than MaxAge is left alone whatever
// else is true about it, because "young" is the one explanation the reaper
// cannot tell apart from "Batch is still doing its job". (MaxAge bounds
// instances only: a job's deadlines are its time in the state it reports, see
// ReaperOptions.MaxAge.)
const ReapMargin = 15 * time.Minute

// MaxDeadline is the longest deadline a caller may configure, for either of
// the reaper's own. A VM or a job cannot leak for longer than Batch keeps the
// record that would have owned it -- Batch deletes a job and its history 60
// days after it finishes (design §9) -- so a deadline past a month protects
// nothing a shorter one does not, and is the shape a unit error takes (a
// seconds value where minutes were meant). The reaper refuses such a
// configuration rather than obey it: an absurd deadline is a caller's bug, and
// a reaper that deletes things must not paper over one by substituting its own
// idea of "reasonable".
const MaxDeadline = 30 * 24 * time.Hour

// ActionKind is what the reaper did, or in a dry run would do.
type ActionKind string

// There is no cancel action, and there must not be one: jobs.cancel is not a
// permission Google has, so the reaper's role cannot carry it and nothing this
// package emits can be a cancel. A constant nothing emits is a permission
// someone re-adds -- which is how the role came to name six permissions for a
// verb that does not exist.
const (
	ActionDeleteJob ActionKind = "delete-job" // jobs.delete: the job and its VMs
	ActionDeleteVM  ActionKind = "delete-vm"  // compute.instances.delete
)

// Action is one thing the reaper did (or, in a dry run, would do).
type Action struct {
	Kind   ActionKind
	Target string // the job's resource name, or the instance's "zone/name"
	Reason string // why, in the operator's words
}

// Refusal is something the reaper declined to touch, and why. Every VM it
// will not delete is in here: the report is the audit trail the review of
// this file is about, so "it deleted nothing" is always explainable.
type Refusal struct {
	Target string
	Reason string
}

// Report is what one pass did, and where and by what deadlines it did it.
//
// The four context fields are not the pass's own business -- Reap decides by
// them, it does not decide them -- but they belong in the report, because the
// report is read long after the process that wrote it is gone: the schedule
// that runs the reaper starts a Cloud Run job whose whole output is this
// report (internal/cli/reaper.go), and "deleted job X" is not a line anyone
// can check months later without the project, the region and the deadlines it
// was judged by -- an installation that changed a task timeout changed the
// reaper's patience with it, which is the one thing that makes an old
// deletion explainable. Reap fills them in from the Reaper; a zero one (a
// Report built by hand, as the lines' own test does) renders no header line at
// all.
type Report struct {
	// Project and Region are the GCP project and the region the pass swept.
	Project, Region string
	// QueueDeadline, RunBudget and MaxAge are the deadlines the pass decided
	// by, after NewReaper's defaults (Reaper.QueueDeadline, RunBudget and
	// MaxAge, which is what these are copied from).
	QueueDeadline, RunBudget, MaxAge time.Duration
	DryRun                           bool
	Actions                          []Action
	Refused                          []Refusal
}

// Lines renders the report for an operator: one line for what the pass was and
// by what it judged, then one line per action and per refusal, every action
// first and every refusal after. That is not the order they were decided in --
// refusals come out of both passes, so a job refused in the job pass prints
// after an instance deleted in the instance pass -- but it is the order an
// operator reads: what the pass did to their project, then what it left alone
// and why. A dry run says "would" on every action, and says so on the header
// too, since a log that holds only "would delete job ..." lines must not be
// mistaken later for a pass that deleted nothing because there was nothing to
// delete.
func (r Report) Lines() []string {
	out := make([]string, 0, len(r.Actions)+len(r.Refused)+1)
	if head := r.headline(); head != "" {
		out = append(out, head)
	}
	for _, a := range r.Actions {
		verb := actionVerb(a.Kind, r.DryRun)
		line := verb + " " + a.Target
		if a.Reason != "" {
			line += " (" + a.Reason + ")"
		}
		out = append(out, line)
	}
	for _, f := range r.Refused {
		out = append(out, "refused "+f.Target+": "+f.Reason)
	}
	return out
}

// headline is the report's first line: the project and region the pass swept
// and the three deadlines it decided by, or "" for a Report that names no
// project (one built by hand rather than by Reap). It is the line that makes
// the rest of the report checkable later -- every deletion in it was judged by
// these numbers, and the numbers are the installation's own task timeouts.
func (r Report) headline() string {
	if r.Project == "" && r.Region == "" {
		return ""
	}
	verb := "reaped"
	if r.DryRun {
		verb = "would reap"
	}
	return fmt.Sprintf("%s projects/%s/locations/%s: queue deadline %v, run budget %v, max age %v",
		verb, r.Project, r.Region, r.QueueDeadline, r.RunBudget, r.MaxAge)
}

// actionVerb is how one action reads in a report: what it did, or what a dry
// run would do.
func actionVerb(k ActionKind, dryRun bool) string {
	var present, past string
	switch k {
	case ActionDeleteJob:
		present, past = "delete job", "deleted job"
	case ActionDeleteVM:
		present, past = "delete VM", "deleted VM"
	default:
		return string(k)
	}
	if dryRun {
		return "would " + present
	}
	return past
}

// outOfBoundsFloor are the GCP projects no pass of this reaper may touch,
// whatever path named them. The first is the installation the plan puts out of
// bounds -- no agent and no automated run touches it, which is why the desktop
// console's fixture recorder refuses it by name too (its BELONG_PROJECT) -- and
// the second is the same installation under the project id it answers to now.
//
// WHY TWO PROJECT IDS ARE IN THE ENGINE, which is otherwise the one thing this
// package never does (CONTRIBUTING.md's "No project specifics in the engine"):
// because a refusal the caller can forget is not a guarantee. The failure this
// list exists for is a pass pointed at an installation that is NOT the one
// being swept -- a live installation's jobs and VMs deleted by a reaper that
// read the wrong project from a config or from a shell -- and the two paths
// that can name a project (a project config's gcp_project, and the reaper
// job's own environment) do not share a configuration file or a variable, so
// anything a caller has to wire up twice is a guarantee with a gap in it. The
// owner's ruling for this task is that the refusal lives here, where both
// paths meet; CONTRIBUTING.md carries the exception beside the rule.
//
// It is a FLOOR and not the whole list: a caller may name further projects it
// never touches (ReaperOptions.OutOfBoundsProjects), and the two paths that
// reach NewReaper both do, from the same environment variable
// (internal/cli's FUGARO_REAPER_REFUSE_PROJECTS). What a caller cannot do is
// take one of these two off the list, which is the point of a floor: an
// installation that believes it is safe to sweep one of them is wrong, and
// nothing in its own configuration can make it right.
//
// A deletion the reaper makes is a deletion of someone's billing or someone's
// data, and this list is the one place where "the installation's own project"
// is not enough of a reason: a reaper pointed at one of these is pointed at a
// live installation that is not the one being swept, so it reads nothing and
// deletes nothing. The list is matched EXACTLY (a project whose id merely
// begins with one of these is another project, and refusing it would refuse a
// project nobody named) and case-insensitively (Google's own API does not treat
// project ids as case-sensitive, so `Edge-Devel-Dimi` is the same project) --
// see outOfBounds.
var outOfBoundsFloor = []string{"edge-devel-dimi", "fugaro-belong"}

// outOfBounds reports whether project is in list. The value is trimmed first
// because one of the two paths that reach NewReaper hands it over unvalidated:
// a project config's gcp_project is checked against localcfg's own
// gcp_project pattern (projectRE), but the reaper job's own environment
// (internal/cli's reaperEnvOptions) is read straight out of FUGARO_GCP_PROJECT
// -- so " edge-devel-dimi " is a spelling of the same project and must not be a
// way around the list. Nothing else is folded and nothing is matched by prefix:
// the list is exact, and this function is the whole of the rule.
func outOfBounds(project string, list []string) bool {
	p := strings.TrimSpace(project)
	for _, bad := range list {
		if strings.EqualFold(p, strings.TrimSpace(bad)) {
			return true
		}
	}
	return false
}

// outOfBoundsProjects is the list one pass refuses: the engine's floor plus
// whatever the caller named. The floor is first, so the refusal names it first
// -- it is the one an operator cannot remove.
func outOfBoundsProjects(o ReaperOptions) []string {
	return append(append([]string{}, outOfBoundsFloor...), o.OutOfBoundsProjects...)
}

// ReaperOptions configure a Reaper.
type ReaperOptions struct {
	GCPProject, Region string
	// OutOfBoundsProjects are further GCP projects this pass refuses BY NAME,
	// on top of the engine's own floor (outOfBoundsFloor, whose two ids and
	// whose reason are stated there): a caller that knows of a project an
	// automated sweep must never touch names it here, and the pass then
	// refuses it exactly as it refuses the floor -- before any client is
	// opened, so nothing is read and nothing can be deleted.
	//
	// It is the caller's data and not the engine's, which is why it is a field
	// and not another constant: the ids are an installation's own (design
	// §3.7's reach is project-wide, so "which projects must never be swept" is
	// a fact about the installations, not about the reaper). Both paths that
	// reach NewReaper fill it from the same variable, internal/cli's
	// FUGARO_REAPER_REFUSE_PROJECTS, so the config path and the reaper job's
	// own environment can never disagree about it.
	//
	// It can only ADD. The floor is not removable: an installation that
	// believed it was safe to sweep one of those two is wrong, and a
	// configuration that could say so would be a configuration that turns a
	// guarantee into a preference.
	OutOfBoundsProjects []string
	// Timeouts is where the workflow timeouts come from, exactly as
	// Options' own is (design §2.3). Required: both of the reaper's deadlines
	// derive from the longest configured task timeout, so that an
	// installation which lengthens a workflow lengthens the reaper's patience
	// with it rather than the other way round. That is the longest TASK
	// timeout, and a run may override its own total time past it
	// (backend.MaxRunTime), so what both deadlines actually derive from is
	// max(longest, backend.MaxRunTime) -- see NewReaper's base.
	Timeouts Timeouts
	// QueueDeadline is how long a job may sit in QUEUED before the queue
	// deadline deletes it (design §1.4, C8). Zero means the longest a run
	// may be active -- the longest configured task timeout, or a run's own
	// override ceiling (backend.MaxRunTime) when that is longer -- plus
	// backend.TaskTimeoutSlack: "the launcher's own task timeout", the
	// design's natural default, read as the launcher's longest one.
	//
	// It is also the bound under which a job's latest status event is read as
	// the transition into the state the job reports (Reaper.reapJob's SCHEDULED
	// and DELETION_IN_PROGRESS branches): an event older than the creation time
	// plus this deadline may date the queue wait instead. A value longer than a
	// whole run budget is refused by NewReaper for that reason -- the state
	// deadlines would then only ever be read against jobs that had queued for
	// longer than a run.
	QueueDeadline time.Duration
	// MaxAge is the age floor for an INSTANCE: how old an instance must be
	// before the reaper will delete it. Zero means the queue deadline, plus
	// the longest a run may be active (the longest configured task timeout,
	// or a run's own override ceiling, whichever is longer) and its slack,
	// plus ReapMargin: nothing a Fugaro run can legitimately be doing takes
	// longer than queueing, running and tearing down. The age filter exists
	// because Batch's own teardown takes seconds, so a VM that is still there
	// moments after its job ended is not yet evidence of a leak (§3.4).
	//
	// It bounds no job. A job's deadlines are separate and both about time
	// spent, not age: a QUEUED job is deleted when it has waited longer than
	// QueueDeadline (§1.4, C8) and never when it reaches MaxAge, and a job in
	// any other state is deleted only by the time it has spent in that state
	// (stateSince, bounded by Reaper.RunBudget), never by its age since
	// creation. Raising MaxAge buys an instance more time before the reaper
	// touches it, and buys a queued job nothing -- QueueDeadline is the knob
	// for that.
	//
	// A value that is negative, longer than MaxDeadline, or shorter than the
	// queue wait plus a run it must contain is refused by NewReaper rather
	// than obeyed.
	MaxAge time.Duration
	// DryRun makes Reap list what it would delete and touch nothing: no
	// instance is deleted and no job is deleted from the job pass either.
	DryRun    bool
	Endpoints Endpoints
	// HTTPClient, when set, is the client the API calls use (tests).
	HTTPClient *http.Client
	// Now, when set, is the clock the age filters read (tests).
	Now func() time.Time
	// Warn, when set, receives problems that don't fail a pass: a job or an
	// instance whose state or time the reaper cannot read (it is refused,
	// never deleted).
	Warn func(msg string)
}

// Reaper is the job-first reaper of design §3.4: one installation's leaked
// Batch jobs and leaked Compute Engine instances, deleted (or, for a queued
// job past the deadline, deleted) as its own scheduled job would.
type Reaper struct {
	o             ReaperOptions
	batch         *batch.Service
	compute       *compute.Service
	now           func() time.Time
	queueDeadline time.Duration
	maxAge        time.Duration
	// runBudget is how long a job may spend in one state (RUNNING,
	// SCHEDULED, DELETION_IN_PROGRESS) before the reaper reads it as stuck:
	// the longest a run may be active (NewReaper's base), its slack and
	// ReapMargin, i.e. everything in MaxAge except the queue wait. Time in a
	// state is measured from the job's own status events, never from its
	// creation time.
	runBudget time.Duration
	// listPageSize is the job listing's page size; tests shrink it to
	// exercise paging.
	listPageSize int64
}

// NewReaper connects to Cloud Batch and the Compute Engine API with
// Application Default Credentials unless NoAuth. The account it runs as is
// the dedicated project-wide reaper account (design §3.7, C22): see this
// file's header for the exact five permissions it needs and nothing else.
func NewReaper(ctx context.Context, o ReaperOptions) (*Reaper, error) {
	switch {
	case o.GCPProject == "" || o.Region == "":
		return nil, errors.New("the reaper needs a GCP project and a region")
	case outOfBounds(o.GCPProject, outOfBoundsProjects(o)):
		return nil, fmt.Errorf("GCP project %s is out of bounds: it is one of the projects this reaper never touches (%s), and it is refused here so that neither source of a project can reach it -- a project config's gcp_project and the reaper job's own environment alike. Nothing has been read and nothing deleted", o.GCPProject, strings.Join(outOfBoundsProjects(o), ", "))
	case o.Timeouts == nil:
		return nil, errors.New("the reaper needs a source for the workflow timeouts: both of its deadlines are derived from the longest one")
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	longest, err := o.Timeouts.LongestTaskTimeout()
	if err != nil {
		return nil, fmt.Errorf("reading the longest workflow timeout: %w", err)
	}
	// The base is not the longest workflow timeout on its own: a run may
	// override its total time well past every configured workflow
	// (LaunchSpec.Timeout, which the launcher takes from --total-timeout and
	// which only backend.MaxRunTime caps), and a run that outran the base
	// would be deleted by the pass after it crossed -- a live run killed by
	// the component built never to delete one. So the base is the same
	// max(longest, the per-run ceiling) internal/cli's max_parallel horizon
	// uses, for the same reason. An installation with no workflows at all
	// answers the ceiling too, which is the right answer there: it has no
	// configured timeouts to derive anything from, and a run may still ask
	// for a day.
	base := max(longest, backend.MaxRunTime)
	switch {
	case o.QueueDeadline < 0:
		return nil, fmt.Errorf("a negative queue deadline (%v): a caller meaning \"no deadline\" leaves it zero", o.QueueDeadline)
	case o.MaxAge < 0:
		return nil, fmt.Errorf("a negative maximum age (%v): a caller meaning \"use the default\" leaves it zero", o.MaxAge)
	}
	queueDeadline := o.QueueDeadline
	if queueDeadline == 0 {
		queueDeadline = base + backend.TaskTimeoutSlack
	}
	// Everything in MaxAge except the queue wait: how long a job may
	// legitimately be RUNNING (or in any other single state) before it is
	// stuck. Derived before MaxAge, because MaxAge's floor is built from it.
	runBudget := base + backend.TaskTimeoutSlack + ReapMargin
	maxAge := o.MaxAge
	if maxAge == 0 {
		maxAge = queueDeadline + runBudget
	}
	// A caller's deadlines are validated rather than clamped. MaxAge shorter
	// than the queue wait it must contain would delete a job that had just
	// started (or a VM behind it) the moment its queue wait alone reached it;
	// one past MaxDeadline is a unit error. A queue deadline longer than a
	// whole run budget is the third shape of the same bug: a state deadline
	// reads a job's time in state from its status event only when that event
	// postdates the creation time plus the queue deadline
	// (stateEventPostdatesQueueDeadline), so a queue deadline that long means
	// no job can be judged stuck unless its queue wait alone exceeded a whole
	// run. All three are the caller's bug, and a reaper that deletes things
	// must say so rather than invent a deadline.
	switch floor := queueDeadline + runBudget; {
	case queueDeadline > MaxDeadline:
		return nil, fmt.Errorf("a queue deadline of %v is longer than any deadline the reaper will act on (%v)", queueDeadline, MaxDeadline)
	case queueDeadline > runBudget:
		return nil, fmt.Errorf("a queue deadline of %v is longer than a run budget (%v): a job would have to queue longer than a whole run before the reaper could ever act on its state", queueDeadline, runBudget)
	case maxAge > MaxDeadline:
		return nil, fmt.Errorf("a maximum age of %v is longer than any deadline the reaper will act on (%v)", maxAge, MaxDeadline)
	case maxAge < floor:
		return nil, fmt.Errorf("a maximum age of %v is too short to contain the queue deadline of %v plus a run: it must be at least %v", maxAge, queueDeadline, floor)
	}
	bs, err := batch.NewService(ctx, clientOptions(o.Endpoints.Batch, o.Endpoints, o.HTTPClient, o.GCPProject)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Batch: %w", err)
	}
	cs, err := compute.NewService(ctx, clientOptions(o.Endpoints.Compute, o.Endpoints, o.HTTPClient, o.GCPProject)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to the Compute Engine API: %w", err)
	}
	return &Reaper{
		o: o, batch: bs, compute: cs, now: o.Now,
		queueDeadline: queueDeadline, maxAge: maxAge, runBudget: runBudget,
		listPageSize: 100,
	}, nil
}

// QueueDeadline, MaxAge and RunBudget are the deadlines this Reaper decides
// by, after the defaults above are applied. Exported for the caller that logs
// the plan before a pass (and for tests that pin them).
//
// QueueDeadline bounds a QUEUED job's wait, MaxAge bounds an instance's age
// and no job at all, and RunBudget bounds how long a job may be in any one
// state before the reaper calls it stuck.
func (r *Reaper) QueueDeadline() time.Duration { return r.queueDeadline }
func (r *Reaper) MaxAge() time.Duration        { return r.maxAge }
func (r *Reaper) RunBudget() time.Duration     { return r.runBudget }

// location is projects/<p>/locations/<r>, the parent of the jobs.
func (r *Reaper) location() string {
	return "projects/" + r.o.GCPProject + "/locations/" + r.o.Region
}

// Reap is one pass: the job pass, then the instance pass.
//
// The order is the design's (§3.4): the job pass decides every job before the
// instance listing is read, so no instance is ever deleted underneath a job
// that still exists, and a VM whose run no listed job answers for is reaped by
// the pass that read that listing.
//
// A job THIS pass deletes does not free its run, though: deleting it hands the
// run to Batch's own teardown, which is still going when the pass ends, and
// deleting the VM in the same pass races it (§3.4's race, at the other end of a
// job's life). Its VMs are therefore left to the pass AFTER this one, which
// reaps them if they really outlived the job -- deleteJob owns the run for
// exactly that reason, and the instance pass refuses a VM whose run is owned.
//
// An error means the pass could not be trusted to have finished: the report
// of what it did before that point is still returned, and a caller must not
// read a short report as a clean project.
func (r *Reaper) Reap(ctx context.Context) (Report, error) {
	// The report carries the context of the pass as well as its outcome, and
	// it is filled in before the first request: a pass that errors on its
	// first listing still hands back a report whose header says where it was
	// looking and by what (Lines' headline).
	rep := Report{
		Project: r.o.GCPProject, Region: r.o.Region,
		QueueDeadline: r.queueDeadline, RunBudget: r.runBudget, MaxAge: r.maxAge,
		DryRun: r.o.DryRun,
	}
	jobs, err := r.listJobs(ctx)
	if err != nil {
		return rep, err
	}
	// owned is what the job pass hands the instance pass, and the instance pass
	// reads nothing else: a VM is deleted only when its run label is not in
	// here (§3.4, "never from a VM listing alone").
	owned := owners{}
	for _, j := range jobs {
		if err := r.reapJob(ctx, j, owned, &rep); err != nil {
			return rep, err
		}
	}
	if err := r.reapInstances(ctx, owned, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// owners is the job pass's answer to the instance pass's one question -- "may
// I delete this run's VMs?" -- keyed by the per-run label the VMs carry, with
// the clause the instance pass reports.
//
// "Did not positively clear" is deliberately wider than "is live". A job the
// pass refused to act on -- an unknown Batch state, a create time it cannot
// read, two label fields that disagree -- is still in the region, may still be
// RUNNING, and owns its VMs exactly as a live job does: a refusal must never
// hand the VM it just declined to explain to the instance pass. Two jobs the
// pass acted on own their runs for the same reason: one it deleted (Batch is
// still tearing that run down) and one Batch is itself still cancelling
// (CANCELLATION_IN_PROGRESS). Only a run whose job this pass positively
// classified as finished, and a run that no job in the listing mentions at all,
// is reapable: §3.4's "an instance whose job is already gone".
type owners map[string]string

// own records that run's VMs are not the reaper's, with the clause the
// instance pass reports for them. The first clause wins: a run named twice was
// decided once, and that decision is the one the report must carry.
func (o owners) own(run, why string) {
	if run == "" {
		return
	}
	if _, taken := o[run]; !taken {
		o[run] = why
	}
}

// reapJob decides one job from its own state, and acts on it. Every branch
// that does not positively clear the job also puts its run -- or every run the
// job claims -- into owned, so the instance pass cannot delete the VMs of a job
// this pass refused to explain.
func (r *Reaper) reapJob(ctx context.Context, j *batch.Job, owned owners, rep *Report) error {
	run, ok := jobRunLabel(j)
	if !ok {
		// Not attributable as one of this installation's runs: the per-run
		// label the VMs are joined by is missing or malformed, or the two
		// label fields disagree about it. (The marker in both fields was
		// checked before the job was listed at all.) Nothing is done to it --
		// an unattributable job is not ours to delete -- and its VMs are not
		// ours either: every run label it claims is owned, so a VM carrying
		// one is refused rather than deleted under a job that is still here.
		why := "its job's " + RunLabel + " label is missing, malformed, or not the same in both label fields, so the reaper could not attribute the job to a run and refused to act on it"
		for _, claimed := range jobRunLabels(j) {
			owned.own(claimed, why)
		}
		rep.refuse(j.Name, "the job does not carry this installation's fugaro=managed marker and one matching fugaro_run label in both label fields")
		return nil
	}
	created, err := parseTime(j.CreateTime)
	if err != nil || created.IsZero() {
		r.o.Warn(fmt.Sprintf("reaper: job %s: createTime: %v", j.Name, err))
		// Its age is unknown, so no age filter cleared it and its state is
		// whatever the API said: it is not cleared, so its VMs are not either.
		owned.own(run, "its job's create time is unreadable, so the reaper refused to act on the job")
		rep.refuse(j.Name, "its create time is unreadable, so its age is unknown and no age filter can clear it")
		return nil
	}
	age := r.now().Sub(created)
	state := ""
	if j.Status != nil {
		state = j.Status.State
	}
	switch state {
	case "QUEUED":
		// The queue wait is the one deadline judged from the creation time,
		// and the one job state where that is sound: a job that has not
		// started has been waiting since it was created, and the wait between
		// creation and the API reporting QUEUED is a few milliseconds the
		// reaper reads as waiting -- the conservative side of a deadline that
		// deletes rather than leaves.
		if age <= r.queueDeadline {
			// Still waiting for capacity, within the deadline the design
			// gives a queue wait. It owns its VM if it has somehow been given
			// one; Batch is still managing it.
			owned.own(run, "its job is QUEUED, still within the queue deadline: Batch is still managing it")
			return nil
		}
		// Deleted, not cancelled: jobs.cancel is not a permission Google has
		// (see this file's header), and a job that never left QUEUED never ran
		// a task, so there is no result.json and no VM to lose -- what the
		// delete does take with it is the job's own record, its status message
		// included, and the run's launch.json is the launcher's own object and
		// stays. deleteQueued owns
		// the run as well -- Batch tears a deleted job's VMs down itself, so
		// the VM is left to the pass after this one -- and it reads the job
		// back before it acts, because "never left QUEUED" is the fact the
		// delete rests on and the listing it came from is a snapshot.
		//
		// What it must never do is delete a job that DID leave QUEUED: that
		// run may have executed a task, and its record is what the run's ls,
		// diagnose and watch read.
		return r.deleteQueued(ctx, j, run, age, owned, rep)
	case "SCHEDULED", "RUNNING":
		// Judged on the time spent IN THIS STATE, not on the job's age: a run
		// may wait in the queue far longer than the queue deadline (the
		// deadline is the previous pass's, or a reaper that was not running),
		// and then start, and its age since creation would call that run stuck
		// seconds after it began.
		spent, ok := stateSince(j, r.now())
		if !ok {
			return r.refuseUnknownStateTime(j, state, run, owned, rep)
		}
		if state == "SCHEDULED" && !stateEventPostdatesQueueDeadline(j, created, r.queueDeadline) {
			// SCHEDULED has no second measurement (unlike RUNNING, whose
			// runDuration corrects the event), and the state before it is the
			// queue wait -- whose event sits at the creation time. The event
			// list is not a per-state log (UNVERIFIED, §3.4), so the latest
			// event a SCHEDULED job carries may be its QUEUED transition, and a
			// job that has just been scheduled after a long queue wait would
			// then read as that whole wait's length into SCHEDULED and be
			// deleted: a live run deleted, by the very uncertainty this build
			// refuses to act on elsewhere. So the event is trusted only when it
			// postdates the creation time plus the queue deadline -- the moment
			// after which the queue wait, if it happened at all, was over-long
			// -- and the job is refused, not deleted, when it does not.
			return r.refuseStaleStateEvent(j, state, run, owned, rep)
		}
		if spent <= r.runBudget {
			// A live run: Batch owns its VM, and the instance pass must leave
			// it alone.
			owned.own(run, fmt.Sprintf("its job is %s, %.0f minutes into the state: Batch owns its teardown", state, spent.Minutes()))
			return nil
		}
		// Longer than any Fugaro run can legitimately be in this state:
		// Batch's own maxRunDuration should have failed it long ago, so this
		// is the hung job §3.4 keeps the reaper for. Deleting the job IS the
		// VM's teardown, which is why it happens here, before the instance
		// pass.
		return r.deleteJob(ctx, j, run, fmt.Sprintf("still %s %.0f minutes into the state, past every run deadline this installation configures", state, spent.Minutes()), owned, rep)
	case "DELETION_IN_PROGRESS":
		// Likewise judged on the time in this state. A deletion STARTED
		// moments ago on an old job -- Batch's own 60-day auto-delete, §9 --
		// must not read as stuck just because the job is old.
		spent, ok := stateSince(j, r.now())
		if !ok {
			return r.refuseUnknownStateTime(j, state, run, owned, rep)
		}
		if !stateEventPostdatesQueueDeadline(j, created, r.queueDeadline) {
			// The same uncertainty as SCHEDULED's, and this branch deletes too:
			// the latest event may date the queue wait rather than the deletion,
			// and a job whose deletion just started on a long-queued job would
			// read as stuck and be deleted again. Refused, never deleted on a
			// measurement that may not be the state's.
			return r.refuseStaleStateEvent(j, state, run, owned, rep)
		}
		if spent <= r.runBudget {
			// Someone is already deleting this job, and Batch is deleting its
			// VMs with it: the instance pass must not race that.
			owned.own(run, "its job is DELETION_IN_PROGRESS: its deletion is already under way")
			return nil
		}
		// A stuck deletion: the residual §3.4 names. Deleting it again is
		// how the deletion finishes (and is what makes this idempotent for a
		// job the real API has already removed -- a 404 below is success).
		return r.deleteJob(ctx, j, run, fmt.Sprintf("a deletion that has not finished in %.0f minutes", spent.Minutes()), owned, rep)
	case "CANCELLATION_IN_PROGRESS":
		// NOT a finished job, though the seam's own table maps it to the
		// cancelled state (stateOf): a cancellation goes
		// CANCELLATION_IN_PROGRESS -> CANCELLED (§1.4), and Batch is still
		// tearing the run's VMs down for as long as this state lasts. Reading
		// it as finished would delete the VM of a job that Batch is
		// mid-teardown on -- the race §3.4 exists to avoid. So the run is
		// owned here, and stays owned: this is not a state a VM is ever freed
		// from.
		//
		// This job is also never deleted, whatever its time in state, where a
		// stuck DELETION_IN_PROGRESS is: a job in this state was cancelled by
		// someone -- an operator's `fugaro cancel`, or a QueueDeadline the
		// cancelled run's own record is the history of -- so its record is
		// what the run's ls, diagnose and watch read. A cancellation that has
		// not finished within the run budget is therefore reported, not acted
		// on -- a line an operator can look at, which is all this build can
		// honestly do with it. (The reaper itself never cancels anything: see
		// its header. This state is reached by someone else's cancel.)
		spent, ok := stateSince(j, r.now())
		if !ok {
			return r.refuseUnknownStateTime(j, state, run, owned, rep)
		}
		if !stateEventPostdatesQueueDeadline(j, created, r.queueDeadline) {
			// The same uncertainty as SCHEDULED's and DELETION_IN_PROGRESS's,
			// and here it is the report rather than an action that rests on
			// the measurement: the latest event may date a long queue wait,
			// and reading it as this state's would name a cancellation that
			// has not finished in N minutes when it has barely begun. The
			// report is the audit trail, so a line it cannot stand behind is
			// not written: refused, exactly as the two delete branches are.
			return r.refuseStaleStateEvent(j, state, run, owned, rep)
		}
		owned.own(run, "its job is CANCELLATION_IN_PROGRESS: Batch tears a cancelled job's VMs down from there")
		if spent > r.runBudget {
			rep.refuse(j.Name, fmt.Sprintf("a cancellation that has not finished in %.0f minutes: the reaper will not delete it, because a deleted job's record is what the run's ls, diagnose and watch read", spent.Minutes()))
		}
		return nil
	case "SUCCEEDED", "FAILED", "CANCELLED":
		// A finished job: its record is history -- fugaro ls, diagnose and
		// watch read it -- and Batch's own teardown owns its VMs. The one
		// branch that clears the run: if a VM of it did survive, the instance
		// pass below finds it, because this job is not in owned. That is the
		// case the reaper exists for.
		return nil
	default:
		r.o.Warn(fmt.Sprintf("reaper: job %s: Batch state %q is not one this build knows: refusing to act on it", j.Name, state))
		// An unknown state may be a run in progress -- the API added a state
		// this build has never heard of -- so its VMs are owned, not reaped.
		owned.own(run, fmt.Sprintf("its job is in the Batch state %q, which this build does not know, and the reaper refused to act on the job", state))
		rep.refuse(j.Name, fmt.Sprintf("its Batch state %q is not one this build knows", state))
		return nil
	}
}

// refuseUnknownStateTime is what a job whose time in the current state cannot
// be read gets: nothing is done to it (a job whose stuckness is unknown is not
// stuck), it is owned so the instance pass leaves
// its VMs alone, and the refusal says why. It is the same posture as an
// unreadable createTime: fail closed, and name it in the report.
func (r *Reaper) refuseUnknownStateTime(j *batch.Job, state, run string, owned owners, rep *Report) error {
	r.o.Warn(fmt.Sprintf("reaper: job %s: no readable time for the %s state: refusing to act on it", j.Name, state))
	owned.own(run, "its job reports no readable time for the state it is in, so the reaper refused to act on it")
	rep.refuse(j.Name, fmt.Sprintf("it reports no readable time for the %s state it is in, so how long it has been there is unknown and no deadline can clear it", state))
	return nil
}

// refuseStaleStateEvent is what a job whose only measurement of its time in
// state may date an earlier state gets, in the two branches that have no
// second measurement: nothing is done to it, it is
// owned so the instance pass leaves its VMs alone, and the refusal says why. It
// is the same posture as an unreadable time in state -- fail closed, and name
// it in the report -- because "this measurement may not be about the state it
// is read as" is exactly an unknown time in state.
func (r *Reaper) refuseStaleStateEvent(j *batch.Job, state, run string, owned owners, rep *Report) error {
	r.o.Warn(fmt.Sprintf("reaper: job %s: its latest job-level status event predates its queue deadline, so it may date the queue wait rather than the %s state: refusing to act on it", j.Name, state))
	owned.own(run, fmt.Sprintf("its job's latest status event predates its queue deadline, so it may date the queue wait rather than the %s state, and the reaper refused to act on the job", state))
	rep.refuse(j.Name, fmt.Sprintf("its latest job-level status event predates its queue deadline, so it may date the queue wait rather than the %s state it reports: how long it has been in that state is unknown and no deadline can clear it", state))
	return nil
}

// stateEventPostdatesQueueDeadline reports whether the latest job-level status
// event can be read as the transition INTO the state the job reports, rather
// than as the job's queue wait.
//
// It is the one thing that can be checked about an event whose state is not
// named: a job is created (the queue entry, and its event, at the creation
// time), so an event that postdates the creation time plus the queue deadline
// cannot be the queue entry -- the queue wait, if it was that long, was over
// by then. An event that does NOT postdate it may be the queue entry or may be
// the real transition, and no field distinguishes the two, so the caller
// refuses rather than act on a measurement that may be about another state.
// Nothing here says a job-level event is emitted on every transition: that is
// still UNVERIFIED (design §3.4), which is why the check is a refusal and not
// a guess.
//
// RUNNING does not need it: its runDuration is a second measurement of the same
// quantity, and stateSince takes the shorter (and refuses when it is absent).
func stateEventPostdatesQueueDeadline(j *batch.Job, created time.Time, queueDeadline time.Duration) bool {
	ev := latestJobEvent(j)
	return !ev.IsZero() && ev.After(created.Add(queueDeadline))
}

// deleteQueued is the queue deadline: a job that has not reached SCHEDULED or
// RUNNING within the deadline is deleted (§1.4's queue deadline is
// Fugaro-side, and a job that never left QUEUED never ran a task, so what is
// erased is the job's own record -- its state and the status message Batch
// attaches -- and nothing else: the run's launch.json is the launcher's, not
// the job's, and survives, so ls, diagnose and watch keep showing the run).
//
// It is a delete and not a cancel because jobs.cancel is not a permission
// Google has: the role this package's header used to describe -- six
// permissions, one of them a verb the Batch API does not define -- could never
// be created, which is what kept --provision-batch refused. jobs.delete is
// already the reaper's, and it is strictly the more destructive verb; what a
// cancel would have kept is the job's record, and the record of a job that
// never ran is not worth a permission that does not exist.
//
// Before it deletes, it reads the job back. The decision came from a listing,
// which is a snapshot: a job that STARTED between that listing and this call is
// a live run with a task in it, and deleting it would erase a record the run's
// ls, diagnose and watch read -- the one thing this file exists never to do.
// So a job that is no longer QUEUED is left to the pass after this one, which
// judges it on the time it spends in the state it has reached, exactly as it
// judges every other job. The read costs one API call per queued job past the
// deadline, which is a rare shape; the alternative is a delete decided from a
// stale snapshot.
func (r *Reaper) deleteQueued(ctx context.Context, j *batch.Job, run string, age time.Duration, owned owners, rep *Report) error {
	reason := fmt.Sprintf("queued for %.0f minutes and never left QUEUED, past the queue deadline of %.0f", age.Minutes(), r.queueDeadline.Minutes())
	deleting := "is being deleted by this pass for the queue deadline"
	if r.o.DryRun {
		deleting = "would be deleted by this pass for the queue deadline"
	}
	// Owned is a first-wins map (owners.own), so the clause is written once,
	// on the path the pass actually took: a run whose job left QUEUED is not
	// one this pass deleted, and its VM is refused for the reason that is
	// true of it.
	own := func(why string) {
		owned.own(run, "its job "+why+", and Batch tears a deleted job's VMs down itself: leaving the VM to the pass after this one")
	}
	if r.o.DryRun {
		own(deleting)
		rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteJob, Target: j.Name, Reason: reason})
		return nil
	}
	// The read-back. It is where the "never left QUEUED" in the reason is
	// checked rather than assumed.
	got, err := r.batch.Projects.Locations.Jobs.Get(j.Name).Context(ctx).Do()
	if err != nil {
		// A job that is already gone needed no delete: someone else's delete
		// (or Batch's own 60-day one) removed it between the listing and this
		// call. Reading that 404 as a failure would end the whole pass -- one
		// vanished job stopping the reaper from doing anything else it had
		// decided to do, which is the opposite of the idempotence deleteJob
		// has. Nothing is recorded, and the run is owned for this pass as if
		// the delete had happened: the pass decided on it, and one pass of
		// grace costs an interval on a leak the next pass reaps (§3.4).
		if isNotFound(err) {
			own(deleting)
			return nil
		}
		return apiError("reading queued job "+j.Name, err)
	}
	if state := jobState(got); state != "QUEUED" {
		// It left the queue: it is a run now, and its record is the run's.
		owned.own(run, fmt.Sprintf("its job left QUEUED (%s) between the listing and the queue deadline's delete, so the reaper left it to the next pass", state))
		return nil
	}
	own(deleting)
	if _, err := r.batch.Projects.Locations.Jobs.Delete(j.Name).Context(ctx).Do(); err != nil {
		// The same idempotence deleteJob has: a job removed between the
		// read-back and the delete is a pass that has nothing left to do.
		if isNotFound(err) {
			return nil
		}
		return apiError("deleting queued job "+j.Name, err)
	}
	rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteJob, Target: j.Name, Reason: reason})
	return nil
}

// jobState is the raw Batch state a job reports, or "" when it reports none.
func jobState(j *batch.Job) string {
	if j.Status == nil {
		return ""
	}
	return j.Status.State
}

// deleteJob is jobs.delete: the job and, with it, its VMs. Only jobs the
// reaper has decided must go reach it (a stuck job, and a stuck deletion).
//
// It also OWNS the run, in a dry run as much as in a live pass: Batch tears a
// deleted job's VMs down itself, and deleting one in the same pass races that
// teardown -- the very race §3.4 keeps the age floor for, at the other end of a
// job's life. So a VM behind a job this pass deletes is left for the pass after
// it, which reaps it if it really outlived its job. The cost of the grace is
// one reaper interval on a leak; the cost of racing is an API error (or a
// half-torn-down job) on a path that runs whenever the reaper does its job.
func (r *Reaper) deleteJob(ctx context.Context, j *batch.Job, run, why string, owned owners, rep *Report) error {
	reason := "stuck: " + why
	deleting := "is being deleted by this pass"
	if r.o.DryRun {
		deleting = "would be deleted by this pass"
	}
	owned.own(run, "its job "+deleting+", and Batch tears a deleted job's VMs down itself: leaving the VM to the pass after this one")
	if r.o.DryRun {
		rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteJob, Target: j.Name, Reason: reason})
		return nil
	}
	if _, err := r.batch.Projects.Locations.Jobs.Delete(j.Name).Context(ctx).Do(); err != nil {
		// A job that is already gone needed no delete: the pass is
		// idempotent, and a second run over the same project must not fail
		// because the first one worked.
		if isNotFound(err) {
			return nil
		}
		return apiError("deleting job "+j.Name, err)
	}
	rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteJob, Target: j.Name, Reason: reason})
	return nil
}

// reapInstances is the second pass: an instance whose job the job pass
// positively cleared -- finished, or no longer in the listing at all -- and
// which carries both of the run's labels, deleted with
// compute.instances.delete (§3.4).
//
// Everything else is refused with a reason, and a job the job pass refused to
// act on is in "everything else": owned carries those runs too, so this pass
// never deletes a VM out from under a job the same pass declined to explain.
//
// The one VM this cannot protect is one whose job the listing never returned
// at all: Fugaro's launcher writes the marker into both label fields (Labels,
// names.go), so a job the installation's own marker filter does not return was
// not one this build launched, and there is nothing here to attribute its VMs
// to. Such a VM is a leak by the only definition left -- it carries the
// installation's marker and a run label, and no job of this installation
// answers for it.
func (r *Reaper) reapInstances(ctx context.Context, owned owners, rep *Report) error {
	instances, err := r.listInstances(ctx)
	if err != nil {
		return err
	}
	for _, in := range instances {
		target := in.zone + "/" + in.name
		// The client-side label check is not redundant with the server-side
		// filter: whether the API honours a label filter or ignores it, the
		// answer here is the same (the same shape List uses for jobs). A VM
		// the filter should not have returned is refused, not deleted.
		if in.labels[labelManaged] != managedValue {
			rep.refuse(target, fmt.Sprintf("its %s label is %q, not this installation's %q", labelManaged, in.labels[labelManaged], managedValue))
			continue
		}
		if !zoneInRegion(in.zone, r.o.Region) {
			// The jobs this pass decided from are the reaper's REGION's
			// (jobs.list is regional); the aggregated listing is not, it is
			// project-wide. A VM in another region belongs to another region's
			// jobs, which this pass never read -- so it is a VM listing
			// deciding, which is what §3.4 forbids -- and it is refused rather
			// than deleted. Another Fugaro region in the same project is a
			// second reaper's business, not this one's.
			rep.refuse(target, fmt.Sprintf("it is in zone %s, not this reaper's region %s, whose jobs are the ones this pass read", in.zone, r.o.Region))
			continue
		}
		run := in.labels[RunLabel]
		if run == "" {
			// §2.4's whole point: the per-run label is what tells one run's
			// VM from another's. Without it there is nothing to attribute the
			// VM to, and the reaper does not guess.
			rep.refuse(target, "it carries no "+RunLabel+" label, so it cannot be attributed to a run")
			continue
		}
		if why, ok := owned[run]; ok {
			// The job pass did not clear this run: either Batch is still
			// managing it or the pass refused to act on it. Both are a reason
			// to leave the VM where it is, and the clause says which.
			rep.refuse(target, why)
			continue
		}
		created, err := parseTime(in.created)
		if err != nil || created.IsZero() {
			r.o.Warn(fmt.Sprintf("reaper: instance %s: creationTimestamp: %v", target, err))
			rep.refuse(target, "its creation time is unreadable, so its age is unknown and no age filter can clear it")
			continue
		}
		age := r.now().Sub(created)
		if age <= r.maxAge {
			// Batch's own teardown takes seconds (Check 57a: 3 to 26 s). A
			// VM this young with no live job is far more likely to be a
			// teardown in progress than a leak, and deleting it would race
			// Batch (§3.4).
			rep.refuse(target, fmt.Sprintf("created %.0f minutes ago, within the %.0f-minute age floor: Batch may still be tearing it down", age.Minutes(), r.maxAge.Minutes()))
			continue
		}
		reason := fmt.Sprintf("%s=%s answers to no job that is still running, and the VM is %.0f minutes old", RunLabel, run, age.Minutes())
		if r.o.DryRun {
			rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteVM, Target: target, Reason: reason})
			continue
		}
		if _, err := r.compute.Instances.Delete(r.o.GCPProject, in.zone, in.name).Context(ctx).Do(); err != nil {
			if isNotFound(err) {
				// Already gone: the pass is idempotent.
				continue
			}
			return apiError("deleting instance "+target, err)
		}
		rep.Actions = append(rep.Actions, Action{Kind: ActionDeleteVM, Target: target, Reason: reason})
	}
	return nil
}

// instance is one Compute Engine instance as the reaper reads it: the zone it
// is in (the aggregated listing's scope key, which is what
// instances.delete needs), its name, its labels and its creation time.
type instance struct {
	zone, name string
	labels     map[string]string
	created    string
}

// instanceFilter is what the aggregated listing is asked for: this
// installation's VMs, by the marker every Fugaro resource carries (§2.4).
// The per-run label cannot be a second clause of the server-side filter and
// be safe -- a VM without one must be REFUSED and reported, not silently
// filtered out of the listing -- so the run label is checked client-side,
// where a missing one is a refusal with a reason.
const instanceFilter = `labels.` + labelManaged + ` = "` + managedValue + `"`

// listInstances is instances.aggregatedList across the project's zones: the
// one call that finds a labelled VM wherever in the region it is. The
// listing is project-wide because the API's label filter is; a second Fugaro
// installation is a second GCP project, which this call never reaches.
func (r *Reaper) listInstances(ctx context.Context) ([]instance, error) {
	var out []instance
	token := ""
	for {
		resp, err := r.compute.Instances.AggregatedList(r.o.GCPProject).Filter(instanceFilter).PageToken(token).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("listing the instances of project %s (the reaper needs compute.instances.list there): %w", r.o.GCPProject, err)
		}
		// A page's scopes are a map, and a map is not ordered, so each page's
		// scopes are sorted before it is read rather than left in Go's order: a
		// report an operator reads twice must not reorder itself page by page,
		// and the tests read it. (The API answers the scopes of a page in its
		// own order, which is not defined either; this is what makes the report
		// reproducible given the same page boundaries.)
		scopes := make([]string, 0, len(resp.Items))
		for scope := range resp.Items {
			scopes = append(scopes, scope)
		}
		sort.Strings(scopes)
		for _, scope := range scopes {
			for _, in := range resp.Items[scope].Instances {
				if in == nil {
					continue
				}
				out = append(out, instance{
					zone:    zoneOf(scope, in.Zone),
					name:    in.Name,
					labels:  in.Labels,
					created: in.CreationTimestamp,
				})
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return out, nil
}

// zoneInRegion reports whether a zone is one of a region's, by the naming
// Google documents: a zone's name is its region's, a dash and a letter
// (us-central1-a is in us-central1). It is what confines the instance pass to
// the region whose jobs the job pass read. A name that does not match is
// refused, never deleted, so a naming Google changes is a reaper that stops
// reaping (and says so in the report) rather than one that deletes the wrong
// region's VMs.
func zoneInRegion(zone, region string) bool {
	return region != "" && strings.HasPrefix(zone, region+"-")
}

// zoneOf is an instance's zone NAME, which is what instances.delete needs and
// what neither field carries directly: the aggregated listing scopes an
// instance under "zones/<zone>" and the instance's own Zone field is the
// resource URL ending in that same name. The scope key is read first, because
// it is the API's own grouping and not a URL this build has to parse.
func zoneOf(scope, rawURL string) string {
	if z, ok := strings.CutPrefix(scope, "zones/"); ok && z != "" {
		return z
	}
	return rawURL[strings.LastIndex(rawURL, "/")+1:]
}

// listJobs is the job pass's listing: every Fugaro job in the region, the
// same AIP-160 filter List sends, checked client-side as well.
//
// It does NOT skip finished jobs the way List does with ActiveOnly: a
// finished job is not live, and its run label must be absent from the live
// map for its VM to be reapable -- so the reaper reads them and drops them
// itself.
func (r *Reaper) listJobs(ctx context.Context) ([]*batch.Job, error) {
	var out []*batch.Job
	token := ""
	for {
		resp, err := r.batch.Projects.Locations.Jobs.List(r.location()).Filter(fugaroLabelFilter).
			PageSize(r.listPageSize).PageToken(token).Context(ctx).Do()
		if err != nil {
			return nil, apiError("listing jobs in "+r.location()+" (the reaper needs batch.jobs.list there)", err)
		}
		// A partial listing must never be read as a complete one: the instance
		// pass deletes a VM whose run no listed job answers for, so a job the
		// API could not reach would look exactly like a run with no job behind
		// it -- a live job's VM deleted because a listing was short. There is
		// no partial answer this pass can act on, so it fails, and the caller
		// retries.
		if len(resp.Unreachable) > 0 {
			return nil, fmt.Errorf("listing jobs in %s: the API could not reach %s, so the listing may be missing jobs: refusing to reap anything from a partial listing", r.location(), strings.Join(resp.Unreachable, ", "))
		}
		for _, j := range resp.Jobs {
			if j == nil || !managedJob(j) {
				continue
			}
			out = append(out, j)
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return out, nil
}

// jobRunLabel is a job's per-run label when the job is one the reaper may
// act on: the installation's marker in BOTH label fields (managedJob, design
// §2.4) and one matching, well-formed fugaro_run in both of them. The run
// label is the join with the VMs' own label, so a job whose two fields
// disagree about it is ambiguous -- two runs, or a hand-edited job -- and the
// reaper refuses it rather than pick one.
func jobRunLabel(j *batch.Job) (string, bool) {
	if !managedJob(j) {
		return "", false
	}
	run := j.Labels[RunLabel]
	if run == "" || !labelValueRE.MatchString(run) {
		return "", false
	}
	if j.AllocationPolicy.Labels[RunLabel] != run {
		return "", false
	}
	return run, true
}

// jobRunLabels is every per-run label the job claims, from either label field,
// well-formed or not. It is what a job the reaper refused to attribute still
// tells it: whatever the two fields disagree about, the VMs of that job carry
// one of these values, and none of them is the reaper's to reap (see owners). A
// job whose fields agree returns the one value twice over, which the caller
// dedupes.
//
// Everything non-empty, NOT only the well-formed values. reapInstances refuses
// or deletes a VM by whatever non-empty fugaro_run it carries, and the value a
// VM carries is the one from AllocationPolicy.labels -- so a value this
// function dropped for its shape would be exactly the value the instance pass
// then read off the VM and found absent from owned: a VM deleted under a job
// that is still RUNNING, which is what owners exists to prevent. Owning a
// garbage value costs a map entry; the alternative costs someone's live run.
func jobRunLabels(j *batch.Job) []string {
	var out []string
	seen := map[string]bool{}
	fields := []map[string]string{j.Labels}
	if j.AllocationPolicy != nil {
		fields = append(fields, j.AllocationPolicy.Labels)
	}
	for _, l := range fields {
		v := l[RunLabel]
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// stateSince is how long a job has been in the state it reports now, which is
// what the run-scoped deadline is measured against. It is deliberately NOT the
// job's age since creation. A job's age says how long ago it was created, and a
// run may spend longer in the queue than the queue deadline (the deadline is
// the previous pass's, and a reaper that was not running enforces nothing),
// then start and run legitimately: judged by its age, that run is "stuck" the
// second it starts, and jobs.delete would kill a live run. A deletion is the
// same story from the other end: Batch's own 60-day job delete (§9) puts a
// DELETION_IN_PROGRESS on a months-old job, and the state began moments ago.
//
// Two measurements are read, and the SHORTER one wins:
//
//   - the latest job-level status event (TaskState unset -- a task-level event
//     dates a task, not the job's state, so those are skipped);
//   - for RUNNING, the runDuration Batch reports, which is exactly this
//     quantity: "the duration of time that the Job spent in status RUNNING"
//     (batch/v1.JobStatus).
//
// The shorter one, because the event list is not a per-state log: nothing in
// the API reference says a job-level event is emitted on every transition
// (UNVERIFIED -- Check 57a is where an installation could measure it), so the
// latest job-level event a RUNNING job carries may be its QUEUED transition
// hours ago. Read as the time in state that is a live run deleted, and
// runDuration is the field that corrects it.
//
// RUNNING therefore REQUIRES the second measurement: a RUNNING job that
// reports no readable runDuration has no trustworthy time in state at all --
// the event is all there is, and it may date an earlier state -- so it is
// unknown and the caller refuses it (refuseUnknownStateTime), exactly as if
// there were no event either. A guard that only applied "when a RUNNING job
// happens to report a runDuration" would leave the stale-event case it exists
// for unfixed in the one shape where acting on it deletes a live run. SCHEDULED
// and DELETION_IN_PROGRESS, the other two states a delete decision rests on,
// have no second measurement either: there a stale event can only make a job
// look LONGER in its state than it is, and the caller's answer is not to guess
// but to check the one thing an event's own state field cannot tell it
// (stateEventPostdatesQueueDeadline) and refuse the job when the event may date
// its queue wait (refuseStaleStateEvent). Being wrong the other way --
// deleting a live run -- is the failure this whole function exists to prevent.
//
// A job with no readable time in state is not stuck: the caller refuses it and
// owns its VMs (refuseUnknownStateTime), because an unknown time never clears a
// job.
func stateSince(j *batch.Job, now time.Time) (time.Duration, bool) {
	if j.Status == nil {
		return 0, false
	}
	var run time.Duration
	haveRun := false
	if j.Status.State == "RUNNING" && j.Status.RunDuration != "" {
		if d, err := time.ParseDuration(j.Status.RunDuration); err == nil && d >= 0 {
			run, haveRun = d, true
		}
	}
	latest := latestJobEvent(j)
	if j.Status.State == "RUNNING" && !haveRun {
		// The event, if any, may belong to the queue wait: a RUNNING job with
		// no runDuration has no measurement a delete may rest on.
		return 0, false
	}
	if latest.IsZero() {
		return run, haveRun
	}
	spent := now.Sub(latest)
	if spent < 0 {
		// A clock that disagrees with the API's: read the state as just
		// entered rather than as one it has been in for a negative time.
		spent = 0
	}
	if haveRun && run < spent {
		return run, true
	}
	return spent, true
}

// latestJobEvent is the time of the job's latest job-level status event, or the
// zero time when it has none that can be read. Task-level events are skipped: a
// task-level event dates a task, not the job's state (batch/v1.StatusEvent's
// TaskState is set for those). It is the event measurement both stateSince and
// stateEventPostdatesQueueDeadline read, so the two can never disagree about
// which event they are talking about.
func latestJobEvent(j *batch.Job) time.Time {
	if j.Status == nil {
		return time.Time{}
	}
	latest := time.Time{}
	for _, ev := range j.Status.StatusEvents {
		if ev == nil || ev.TaskState != "" {
			continue
		}
		t, err := parseTime(ev.EventTime)
		if err != nil || t.IsZero() {
			continue
		}
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}

// isNotFound reports whether err is the API's 404, for a delete that is
// idempotent because the resource is already gone. The deletes read the raw
// client error rather than apiError's wrapping, so the check is on the
// googleapi error itself.
func isNotFound(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == http.StatusNotFound
}

// refuse records something the reaper declined to touch.
func (rep *Report) refuse(target, reason string) {
	rep.Refused = append(rep.Refused, Refusal{Target: target, Reason: reason})
}
