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
//   - It cancels -- never deletes -- a job that has sat in QUEUED past the
//     queue deadline (§1.4): the record survives, which is what fugaro ls,
//     diagnose, watch and the sweeper read, and jobs.delete would erase it.
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
// and touches nothing at all -- no cancel either.
//
// The permissions this needs are Task 6's to grant (§3.7, C22); the reaper is
// its own dedicated, project-wide service account and must not share the
// launcher's or the job's. Exactly, and nothing more:
//
//   - batch.jobs.list and batch.jobs.get -- to read every Fugaro job in the
//     region, which is where each decision comes from.
//   - batch.jobs.cancel -- the queue deadline (§1.4). It is not optional: a
//     job stuck QUEUED past the deadline counts against max_parallel and can
//     wait out Batch's own 2-day ceiling.
//   - batch.jobs.delete -- the destructive one, and the reason for a separate
//     account: it erases a job and its history, so it belongs to a stuck job
//     the reaper has decided must go and to nothing else.
//   - compute.instances.list -- the project-wide aggregated list, which is
//     how a Fugaro-labelled VM in any of the region's zones is found.
//   - compute.instances.delete -- the other destructive one.
//
// roles/batch.jobsEditor on the project carries the four Batch permissions
// (design §3.7 names it for create, cancel and delete alike) and
// roles/compute.instanceAdmin.v1 the two Compute ones; whether the owner
// narrows them with an IAM Condition is Check 57a step 9's question
// (UNVERIFIED, design §9), and it does not change this list. Nothing here
// reads a secret, and nothing here needs Secret Manager, storage or logging:
// the reaper's whole surface is the five permissions above.

// ReapMargin is the margin the reaper's age filter adds on top of the
// longest task timeout, its slack and the queue deadline: the VM boot and
// image pull the design budgets 120 s for (§1.2), Batch's own teardown after
// a terminal state (Check 57a measured 3 to 26 s), and the settle window a
// listing's own lag needs. A VM or a job younger than MaxAge is left alone
// whatever else is true about it, because "young" is the one explanation the
// reaper cannot tell apart from "Batch is still doing its job".
const ReapMargin = 15 * time.Minute

// ActionKind is what the reaper did, or in a dry run would do.
type ActionKind string

const (
	ActionDeleteJob ActionKind = "delete-job" // jobs.delete: the job and its VMs
	ActionCancelJob ActionKind = "cancel-job" // jobs.cancel: the queue deadline
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

// Report is what one pass did.
type Report struct {
	DryRun  bool
	Actions []Action
	Refused []Refusal
}

// Lines renders the report for an operator: one line per action and per
// refusal, in the order they were decided. A dry run says "would" on every
// action.
func (r Report) Lines() []string {
	out := make([]string, 0, len(r.Actions)+len(r.Refused))
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

// actionVerb is how one action reads in a report: what it did, or what a dry
// run would do.
func actionVerb(k ActionKind, dryRun bool) string {
	var present, past string
	switch k {
	case ActionDeleteJob:
		present, past = "delete job", "deleted job"
	case ActionCancelJob:
		present, past = "cancel job", "cancelled job"
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

// ReaperOptions configure a Reaper.
type ReaperOptions struct {
	GCPProject, Region string
	// Timeouts is where the workflow timeouts come from, exactly as
	// Options' own is (design §2.3). Required: both of the reaper's deadlines
	// derive from the longest configured task timeout, so that an
	// installation which lengthens a workflow lengthens the reaper's patience
	// with it rather than the other way round.
	Timeouts Timeouts
	// QueueDeadline is how long a job may sit in QUEUED before the queue
	// deadline cancels it (design §1.4, C8). Zero means the longest
	// configured task timeout plus backend.TaskTimeoutSlack -- "the
	// launcher's own task timeout", the design's natural default.
	QueueDeadline time.Duration
	// MaxAge is how old a job or an instance must be before the reaper will
	// delete it. Zero means the queue deadline, plus the longest task timeout
	// and its slack, plus ReapMargin: nothing a Fugaro run can legitimately
	// be doing takes longer than queueing, running and tearing down. The age
	// filter exists because Batch's own teardown takes seconds, so a resource
	// that is still there moments after its job ended is not yet evidence of
	// a leak (§3.4).
	MaxAge time.Duration
	// DryRun makes Reap list what it would delete and touch nothing: no
	// instance is deleted and no job is cancelled either.
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
// job past the deadline, cancelled) as its own scheduled job would.
type Reaper struct {
	o             ReaperOptions
	batch         *batch.Service
	compute       *compute.Service
	now           func() time.Time
	queueDeadline time.Duration
	maxAge        time.Duration
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
	// An installation with no workflows at all answers zero, and a zero base
	// would make every deadline two minutes: the default task timeout is what
	// "no workflow is longer than this" means (backend.DefaultTaskTimeout).
	base := longest
	if base <= 0 {
		base = backend.DefaultTaskTimeout
	}
	queueDeadline := o.QueueDeadline
	if queueDeadline <= 0 {
		queueDeadline = base + backend.TaskTimeoutSlack
	}
	maxAge := o.MaxAge
	if maxAge <= 0 {
		maxAge = queueDeadline + base + backend.TaskTimeoutSlack + ReapMargin
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
		queueDeadline: queueDeadline, maxAge: maxAge, listPageSize: 100,
	}, nil
}

// QueueDeadline and MaxAge are the deadlines this Reaper decides by, after
// the defaults above are applied. Exported for the caller that logs the plan
// before a pass (and for tests that pin the defaults).
func (r *Reaper) QueueDeadline() time.Duration { return r.queueDeadline }
func (r *Reaper) MaxAge() time.Duration        { return r.maxAge }

// location is projects/<p>/locations/<r>, the parent of the jobs.
func (r *Reaper) location() string {
	return "projects/" + r.o.GCPProject + "/locations/" + r.o.Region
}

// Reap is one pass: the job pass, then the instance pass.
//
// The order is the design's (§3.4). A job this pass deletes is deleted
// before the instance listing is read, so an instance whose job the reaper
// has just taken out is reapable in the same pass -- which is what makes
// "delete the job, and only then look for an instance with no live job
// behind it" true of a single run rather than of two.
//
// An error means the pass could not be trusted to have finished: the report
// of what it did before that point is still returned, and a caller must not
// read a short report as a clean project.
func (r *Reaper) Reap(ctx context.Context) (Report, error) {
	rep := Report{DryRun: r.o.DryRun}
	jobs, err := r.listJobs(ctx)
	if err != nil {
		return rep, err
	}
	// live maps a run label -- the per-run label the VMs carry -- to the raw
	// state of a job that is still answerable for its VMs. The instance pass
	// reads it and nothing else: a VM is deleted only when its run label is
	// not in here (§3.4, "never from a VM listing alone").
	live := map[string]string{}
	for _, j := range jobs {
		if err := r.reapJob(ctx, j, live, &rep); err != nil {
			return rep, err
		}
	}
	if err := r.reapInstances(ctx, live, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// reapJob decides one job from its own state, and acts on it.
func (r *Reaper) reapJob(ctx context.Context, j *batch.Job, live map[string]string, rep *Report) error {
	run, ok := jobRunLabel(j)
	if !ok {
		// Not attributable as one of this installation's runs: the marker is
		// missing from a label field, or the per-run label the VMs are joined
		// by is missing or the two fields disagree. Nothing is done to it --
		// an unattributable job is not ours to delete.
		rep.refuse(j.Name, "the job does not carry this installation's fugaro=managed marker and one matching fugaro_run label in both label fields")
		return nil
	}
	created, err := parseTime(j.CreateTime)
	if err != nil || created.IsZero() {
		r.o.Warn(fmt.Sprintf("reaper: job %s: createTime: %v", j.Name, err))
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
		if age <= r.queueDeadline {
			// Still waiting for capacity, within the deadline the design
			// gives a queue wait. It owns its VM if it has somehow been given
			// one; Batch is still managing it.
			live[run] = state
			return nil
		}
		return r.cancelQueued(ctx, j, run, age, rep)
	case "SCHEDULED", "RUNNING":
		if age <= r.maxAge {
			// A live run: Batch owns its VM, and the instance pass must leave
			// it alone.
			live[run] = state
			return nil
		}
		// Past every deadline a Fugaro run could legitimately have: Batch's
		// own maxRunDuration should have failed it long ago, so this is the
		// hung job §3.4 keeps the reaper for. Deleting the job IS the VM's
		// teardown, which is why it happens here, before the instance pass.
		return r.deleteJob(ctx, j, fmt.Sprintf("still %s %.0f minutes past every deadline this installation configures", state, age.Minutes()), rep)
	case "DELETION_IN_PROGRESS":
		if age <= r.maxAge {
			return nil
		}
		// A stuck deletion: the residual §3.4 names. Deleting it again is
		// how the deletion finishes (and is what makes this idempotent for a
		// job the real API has already removed -- a 404 below is success).
		return r.deleteJob(ctx, j, "a deletion that has not finished within every deadline", rep)
	case "SUCCEEDED", "FAILED", "CANCELLED", "CANCELLATION_IN_PROGRESS":
		// A finished job: its record is history -- fugaro ls, diagnose and
		// watch read it -- and Batch's own teardown owns its VMs. If a VM of
		// it did survive, the instance pass below finds it, because this job
		// is not in live.
		return nil
	default:
		r.o.Warn(fmt.Sprintf("reaper: job %s: Batch state %q is not one this build knows: refusing to act on it", j.Name, state))
		rep.refuse(j.Name, fmt.Sprintf("its Batch state %q is not one this build knows", state))
		return nil
	}
}

// cancelQueued is the queue deadline: a job that has not reached SCHEDULED or
// RUNNING within the deadline is cancelled with jobs.cancel -- never deleted,
// because its record is what the run's ls, diagnose and watch read (§1.4,
// C8) -- and the run shows a queue-timeout outcome (internal/cli).
func (r *Reaper) cancelQueued(ctx context.Context, j *batch.Job, run string, age time.Duration, rep *Report) error {
	reason := fmt.Sprintf("queued for %.0f minutes, past the queue deadline of %.0f", age.Minutes(), r.queueDeadline.Minutes())
	if r.o.DryRun {
		rep.Actions = append(rep.Actions, Action{Kind: ActionCancelJob, Target: j.Name, Reason: reason})
		return nil
	}
	if _, err := r.batch.Projects.Locations.Jobs.Cancel(j.Name, &batch.CancelJobRequest{}).Context(ctx).Do(); err != nil {
		// The job may have started, or finished, between the listing and this
		// call. Read it back: a terminal job needed no cancel, which is the
		// same tolerance Cancel has (§2.3, live check 5). Anything else is a
		// real failure and ends the pass.
		if got, rerr := r.batch.Projects.Locations.Jobs.Get(j.Name).Context(ctx).Do(); rerr == nil && jobIsTerminal(got) {
			return nil
		}
		return apiError("cancelling queued job "+j.Name, err)
	}
	rep.Actions = append(rep.Actions, Action{Kind: ActionCancelJob, Target: j.Name, Reason: reason})
	return nil
}

// deleteJob is jobs.delete: the job and, with it, its VMs. Only jobs the
// reaper has decided must go reach it (a stuck job, and a stuck deletion).
func (r *Reaper) deleteJob(ctx context.Context, j *batch.Job, why string, rep *Report) error {
	reason := "stuck: " + why
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

// reapInstances is the second pass: an instance whose job is not live and
// which carries both of the run's labels, deleted with
// compute.instances.delete (§3.4).
func (r *Reaper) reapInstances(ctx context.Context, live map[string]string, rep *Report) error {
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
		run := in.labels[RunLabel]
		if run == "" {
			// §2.4's whole point: the per-run label is what tells one run's
			// VM from another's. Without it there is nothing to attribute the
			// VM to, and the reaper does not guess.
			rep.refuse(target, "it carries no "+RunLabel+" label, so it cannot be attributed to a run")
			continue
		}
		if state, ok := live[run]; ok {
			rep.refuse(target, fmt.Sprintf("the job behind it is %s: Batch owns its teardown", state))
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
		reason := fmt.Sprintf("no live job carries %s=%s and it is %.0f minutes old", RunLabel, run, age.Minutes())
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
		// The scopes are not ordered, so the instances are sorted by
		// zone/name rather than left in a map's order: a report an operator
		// reads twice must not reorder itself, and the tests read it.
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

// jobIsTerminal reports whether a job has finished, by the same mapping the
// backend uses (stateOf): it is what makes a cancel that races a job's own
// end a no-op rather than an error.
func jobIsTerminal(j *batch.Job) bool {
	if j.Status == nil {
		return false
	}
	st, known := stateOf(j.Status.State)
	return known && st.Terminal()
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
