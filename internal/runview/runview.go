// Package runview joins what the runs bucket and the backend know about a
// run into one row (design §4.7, §9.1 ls): status, stage, cost and links.
// It never reports a run as more finished or more successful than
// result.json says: an execution that ended without a final record is an
// infra_error, never a success.
package runview

import (
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// Statuses beyond runstore's.
const (
	StatusUnlaunched = "unlaunched"
	StatusLaunching  = "launching" // a fresh launch claim, no launch.json yet
	StatusPending    = "pending"
	// StatusError is a run whose objects can't be trusted or read (a
	// corrupt launch.json or result.json, an execution that isn't the
	// run's): Reason says what; nothing about its progress is guessed.
	StatusError = "error"
)

// ReasonLost explains a launched run that the backend doesn't know and that
// never wrote a record, past runstore.ClaimTTL after its launch: it failed
// before the runner started, and its execution is gone.
const ReasonLost = "no execution found and the runner never recorded the run"

// ReasonNoFinalRecord explains a run whose execution is gone but whose
// record never reached a final status (OOM kill, task timeout, node loss).
const ReasonNoFinalRecord = "execution ended without finalizing"

// DerivedPreempted is the read side's derivation for a run the platform
// reclaimed: a Cloud Batch job whose Spot VM was preempted ends FAILED, Batch
// does not retry it, and the runner never reaches finalize, so no object in
// the runs bucket ever records what happened. The status is derived when the
// run is read and is **never written back**: the bucket keeps what the run
// itself wrote, the launch's lease expires by its own timeout, and the
// history sweep settles the cost (the owner's 2026-10-21 ruling; design
// compute-backend.md §1.3).
const DerivedPreempted = "preempted"

// ReasonPreempted explains a row whose Derived is DerivedPreempted. It says
// what the platform did and not what the run did, because nothing about the
// run's own work is known: the record it would have said it with never came.
const ReasonPreempted = "the Batch job was preempted: its Spot VM was reclaimed before the run finalized"

// StaleDraftAfter is how long past its last status update a running first
// run with no recorded deadline may go before its draft is called stale.
// Stages and verifies run for many minutes and the limits are configurable,
// so it is generous. A recorded deadline always decides instead.
const StaleDraftAfter = 3 * time.Hour

// Input is everything known about one run.
type Input struct {
	Slug, RunID  string
	Task         *task.Spec
	Launch       *runstore.Launch
	Record       *runstore.Record
	Exec         *backend.Execution
	CancelMarker bool
	Claim        *runstore.Claim // the "launching" marker, if any
	// Problem, when set, is why the run's objects can't be followed; the
	// row is then StatusError with Problem as its reason.
	Problem string
}

// Row is one run as ls and diagnose show it.
type Row struct {
	Run      string `json:"run"`
	Repo     string `json:"repo"`
	Workflow string `json:"workflow,omitempty"`
	// OAuthAccount is the named Claude OAuth account the run used ("" for
	// the repository's default account, docs/design/named-oauth-tokens.md):
	// the record's own value when it has one (confirmed by the runner
	// itself at finalize), else the launching task's OAuthToken (Join falls
	// back to it when the record has none — a run that died before
	// finalize, or whose record predates this field). Still "" for a run
	// with neither a record nor a task carrying an account (not using
	// agent.auth: oauth, or neither is readable). Informational only.
	OAuthAccount string `json:"oauth_account,omitempty"`
	// Recipe is the run's recipe: the record's, else the task's; "" when
	// neither names one (a default run launched before 0.5.0, or not started).
	Recipe string `json:"recipe,omitempty"`
	// Title is the run's job title, from the ONE place the text comes from
	// (task.Spec.TitleLine): the title its launcher stored (fugaro run
	// --title), else the task prompt's first line. "" only when there is no
	// readable task, or the task has neither. runs ls renders no title
	// column (fugaro watch is where a run is listed by title), but its
	// --json rows carry it for the console and every other reader, so no
	// reader derives the title from the task itself any more.
	Title string `json:"title,omitempty"`
	// ProjectLayer is the project layer the run's task carried, and
	// ConfigSHA256 its resolved config's sum (docs/design/layered-config.md
	// §8); both absent before 0.6.0 and for a run that has not started.
	ProjectLayer *runstore.ProjectLayerRecord `json:"project_layer,omitempty"`
	ConfigSHA256 string                       `json:"config_sha256,omitempty"`
	RunID        string                       `json:"run_id"`
	Status       string                       `json:"status"`
	Stage        string                       `json:"stage,omitempty"`
	Reason       string                       `json:"reason,omitempty"`
	// Derived, when set, is the read side's own reading of a run the run's
	// record never settled: today the one value is DerivedPreempted, for a run
	// whose Cloud Batch job ended FAILED because its Spot VM was reclaimed.
	// It is a *derivation* and says so by being its own field: Status stays the
	// vocabulary every reader already switches on (`failed` here), and a
	// consumer that does not know this field loses the decoration, not the
	// status. Nothing writes it back to the runs bucket, and nothing acts on
	// it — a row whose record is not final is derived anew on every read.
	Derived string `json:"derived,omitempty"`
	// Halt is why a halted run was halted.
	Halt        *runstore.Halt `json:"halt,omitempty"`
	Batch       string         `json:"batch,omitempty"`
	RequestedBy string         `json:"requested_by,omitempty"`
	// LaunchedBy is who started the run, as the run's own launch.json
	// recorded it at launch (runstore.Launch.LaunchedBy, written from the
	// launching task's RequestedBy): a person's address, or the identity an
	// automated launch runs as (a service account, a CI runner). It is the
	// launch record's own account of itself and nothing else — absent for a
	// run whose launch.json has none (never launched, or launched by a
	// writer that recorded no identity), and never filled in from the task,
	// the record or the account the run used.
	//
	// UNTRUSTED display text, and the one field of this row that is
	// sanitised here rather than by the renderer: a launcher writes it, so
	// it can carry escapes, newlines, bidi overrides or 10 KB of text, and
	// both documents this row appears in — runs ls --json and the console
	// that reads it — are rendered as they are. safetext.Field drops what
	// could rewrite a line and caps what is left. It is read for display
	// only: nothing authorises or admits anything by it (the guard test in
	// this package fails if any other code reads the field).
	LaunchedBy string `json:"launched_by,omitempty"`
	// PlanTask is the plan task this run works on, as the run's own
	// launch.json recorded it at launch (runstore.Launch.PlanTask, from
	// task.Spec.PlanTask at fugaro run --plan-task): the id of one task in
	// one plan, the join key the console's graph screen matches a run to a
	// node by (console dependency graph plan, decision GD5).
	//
	// It is the launch record's own value and nothing else, like LaunchedBy
	// beside it: absent for a run whose launch.json has none (never
	// launched, or launched by a CLI that recorded none), and never filled
	// in from the task, which carries it for the recovery path and --retry
	// rather than for display.
	//
	// UNTRUSTED display text, dropped here unless it passes the launcher's
	// own shape check (task.PlanTaskProblem: the run's service account can
	// write launch.json too) and sanitised for the same reason the launcher
	// is (safetext.Field): a launcher writes it, and both documents
	// this row appears in — runs ls --json and the console that reads it —
	// are rendered as they are. It is a DISPLAY-ONLY join hint (the plan's
	// constraint C3): nothing decides anything by it, it is never joined
	// into a path, and a run's claim about it changes no other node's state.
	// Since console 0.118.0 it is also what the Coordinator queue prefills
	// the launch confirmation's plan-task field with (Coordinator v2 K1):
	// a second reader of the same text, not a second decision -- the
	// operator edits it or not, and the window checks it the way the flag
	// does before anything spawns.
	PlanTask  string        `json:"plan_task,omitempty"`
	PRURL     string        `json:"pr_url,omitempty"`
	Execution string        `json:"execution,omitempty"`
	LogURL    string        `json:"log_url,omitempty"`
	Created   time.Time     `json:"created"`
	Cost      runstore.Cost `json:"cost"`
	// ImageAgeS is how old the run's image was when the run started
	// (started_at minus built_at), in seconds; absent when either is unknown.
	ImageAgeS *int64 `json:"image_age_s,omitempty"`
	Terminal  bool   `json:"terminal"`
	// Settled means the row won't change on its own: terminal, or
	// unlaunched (nobody is launching it). runs ls --watch stops when every row
	// is settled; a launching row is not, since its launch.json is coming.
	Settled bool `json:"settled"`

	// Branch is the run's branch: the record's, else the task's (a
	// follow-up's, known before it starts).
	Branch  string `json:"branch,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	// ChangedFiles is the run's own record of the paths it changed (console
	// v2 design D27, F17): paths only, bounded, and with the truncation
	// recorded rather than hidden. nil when the run recorded none — a run
	// that died before finalize, or a record from before the field existed —
	// so a reader never sees an empty set where the answer is unknown.
	ChangedFiles *runstore.ChangedFiles `json:"changed_files,omitempty"`
	// Rigor is the mode the run resolved, the layer that decided it, the
	// areas that matched — each with the mode it sets and the moment it was
	// first applied at, strictest first (D23) — and the moment of the block
	// (docs/rigor-modes D22, D24): the record's own block, bounded on read
	// (runstore.RigorRecord.Bounded), and nil when the run recorded none —
	// a record from before the field existed, a run that died before
	// bootstrap, or a block that cannot be read as a decision — so no reader
	// renders "no record" as "it resolved nothing".
	Rigor *runstore.RigorRecord `json:"rigor,omitempty"`
	// ArtifactCount and ArtifactBytes are what the run's record says it
	// delivered: how many artifacts, and their recorded total size (design
	// artifact-runs.md §9). Both are the record's own account, never a
	// bucket listing, and both are 0 for a run whose record has none —
	// which is every run that opens a pull request, so the fields stay
	// absent in its JSON (the rule the other optional fields follow).
	// The artifacts themselves, name by name, are `fugaro artifacts ls`.
	ArtifactCount int   `json:"artifact_count,omitempty"`
	ArtifactBytes int64 `json:"artifact_bytes,omitempty"`
	// TaskPR is the pull request the task continues (a follow-up);
	// RecordPR is the one the run's record names; PR is the record's,
	// else the task's.
	TaskPR   int `json:"task_pr,omitempty"`
	RecordPR int `json:"record_pr,omitempty"`
	PR       int `json:"pr,omitempty"`
	// Pushed means the run updated its branch, and PushedHead is the
	// commit it pushed; see Pushed.
	Pushed     bool       `json:"pushed"`
	PushedHead string     `json:"pushed_head,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	// BaseBranch is the record's base branch, when it has one.
	BaseBranch string `json:"base_branch,omitempty"`
	// PreviousRun is the run a follow-up continues.
	PreviousRun string `json:"previous_run,omitempty"`
	FollowUp    bool   `json:"follow_up"`

	// DraftFallback: the host has no draft pull requests, so the PR is an
	// ordinary one with "[DRAFT]" in its title.
	DraftFallback bool `json:"draft_fallback,omitempty"`
	// PRStatusAt is when the runner last wrote the PR's status section.
	PRStatusAt *time.Time `json:"pr_status_at,omitempty"`
	// StaleDraft means the run's record is still not final but its draft
	// PR's status section is old, or the run's execution is gone: the run
	// probably died and the draft was left as it was.
	StaleDraft bool `json:"stale_draft,omitempty"`
}

// Pushed reports whether the run of task t (nil when unreadable) with
// record r pushed its branch, and the commit it pushed. A record with
// pushed_head has. A record written before follow-ups existed has no
// pushed_head, but then its pull request was opened only after a
// successful push: a PR number means it pushed its head_sha (none named,
// nothing pushed). That reading never applies to a follow-up, whose record
// names its PR from the start. The record tells one apart without its task
// too: a first run's branch always names its own run ID.
func Pushed(t *task.Spec, r *runstore.Record) (head string, ok bool) {
	switch {
	case r == nil:
		return "", false
	case r.PushedHead != "":
		return r.PushedHead, true
	case r.FollowUp != nil || (t != nil && t.IsFollowUp()):
		return "", false
	}
	if id, ok := task.BranchRunID(r.Branch); ok && id != r.RunID {
		return "", false // another run's branch: a follow-up
	}
	if r.PR != nil && r.PR.Number > 0 && r.HeadSHA != "" {
		return r.HeadSHA, true
	}
	return "", false
}

// LauncherShort is a launcher identity as a text column shows it: for an
// address, the part before its @ (dana@example.com → dana, which is what
// tells two colleagues' runs apart); for anything else — a service account
// name, a CI identity, whatever an automated launch recorded — the whole
// value, since it has no local part to shorten to. An address with nothing
// before its @ shortens to "", which the caller renders as its own empty
// cell marker.
//
// It shortens what is already there; it does not make a value safe. The
// launcher is untrusted text, so callers pass a sanitised value (Row's own
// LaunchedBy is one) and sanitise the result again for the cell they write
// it into, as every other bucket-derived column does.
func LauncherShort(by string) string {
	if name, _, ok := strings.Cut(by, "@"); ok {
		return name
	}
	return by
}

// PriceBook is the compute price table one execution is priced with (a
// local override, else the backend's list price), for the execution's own
// region and machine type. The execution is passed rather than a region
// string because the region is the backend's to resolve -- a second
// platform's name need not have Cloud Run's fields, so a caller parsing the
// name would price a VM run with the empty region's table (design
// compute-backend.md §5.2) -- and because a VM's price also depends on its
// machine type.
//
// The zero Prices means "no table prices this execution": Join then records
// compute as not estimated, never as $0.00, so a machine type or region the
// table does not carry reads as unknown rather than free. A book that carries
// a source line but bills nothing at all is the same thing (priced), because
// a "$0.00" estimate cannot be told from free either.
type PriceBook func(e *backend.Execution) backend.Prices

// priced reports whether p prices anything: a book whose two rates are both
// zero bills every execution at nothing, and "compute $0.00, estimate" is
// indistinguishable from free, which §5.2 refuses. A table that carries
// genuine prices always has a positive CPU rate -- neither platform sells
// vCPU-seconds for nothing -- so this never refuses a real table; it only
// catches a caller that handed over a partly-filled one.
func priced(p backend.Prices) bool {
	return p.VCPUSecondUSD > 0 || p.GiBSecondUSD > 0
}

// Join builds the row for in, pricing compute with prices.
func Join(in Input, prices PriceBook, now time.Time) Row {
	row := Row{Run: in.Slug + "/" + in.RunID, RunID: in.RunID}
	row.Created, _ = runstore.RunTime(in.RunID)
	if t := in.Task; t != nil {
		row.Repo, row.Workflow, row.Batch, row.RequestedBy = t.Repo, t.Workflow, t.Batch, t.RequestedBy
		row.Title = t.TitleLine()
	} else {
		row.Reason = "task.json unreadable"
	}
	if in.Launch != nil {
		row.Execution, row.LogURL = in.Launch.Execution, in.Launch.LogURL
		row.LaunchedBy = safetext.Field(in.Launch.LaunchedBy)
		// The run's own service account can write launch.json, so an id the
		// launcher could never have accepted is dropped, not shown.
		if task.PlanTaskProblem(in.Launch.PlanTask) == "" {
			row.PlanTask = safetext.Field(in.Launch.PlanTask)
		}
	}
	r, e := in.Record, in.Exec
	followUpFields(&row, in.Task, r)
	switch {
	case r != nil && r.Recipe != nil:
		row.Recipe = r.Recipe.Name
	case in.Task != nil && in.Task.Recipe != nil:
		row.Recipe = in.Task.Recipe.Name
	}
	switch {
	// The record's own OAuthAccount, confirmed by the runner itself at
	// finalize, is the best evidence once it exists.
	case r != nil && r.OAuthAccount != "":
		row.OAuthAccount = r.OAuthAccount
	// Otherwise fall back to the task's own OAuthToken, the account the
	// launcher actually resolved the run to run under (task.Spec.OAuthToken,
	// set once at launch, never changed afterward): a run that died before
	// finalize (no result.json at all, r == nil) or whose record predates
	// this field (r != nil but r.OAuthAccount == "") would otherwise read
	// "" indistinguishably from "used the default account" (named oauth
	// tokens task 9b, third review round, comment 3).
	case in.Task != nil:
		row.OAuthAccount = in.Task.OAuthToken
	}
	if r != nil {
		row.ProjectLayer, row.ConfigSHA256 = r.ProjectLayer, r.ConfigSHA256
		// Bounded again on read: the record is job-writable, so the row
		// never carries more than the bound or a cut set dressed as whole.
		row.ChangedFiles = r.ChangedFiles.Bounded()
		// The same for the rigor block (docs/rigor-modes D22, D24): a block
		// that cannot name a mode from the vocabulary, a layer from
		// rigor.Layers or one of D22's three moments reaches the row as none
		// at all, which is the answer its absence already gives.
		row.Rigor = r.Rigor.Bounded()
		row.Stage = r.Stage
		if r.Reason != "" {
			row.Reason = r.Reason
		}
		// Whatever the status: a halted run whose finalize then failed is
		// an infra_error that still says what halted it.
		row.Halt = r.Halt
		if r.PR != nil {
			row.PRURL = r.PR.URL
		}
		if im := r.Image; im != nil && im.BuiltAt != nil && !r.StartedAt.IsZero() {
			// A build time after the start (clock skew) is not an age.
			if s := int64(r.StartedAt.Sub(*im.BuiltAt) / time.Second); s >= 0 {
				row.ImageAgeS = &s
			}
		}
		if r.Execution != "" {
			// The runner's record names the execution that owns the run; after
			// a double launch, launch.json may name the duplicate.
			row.Execution = r.Execution
		}
		// The record's artifacts: the count and the recorded total, for a
		// listing that shows a column (or a field) only when a run has one.
		row.ArtifactCount = len(r.Artifacts)
		for _, a := range r.Artifacts {
			// A negative byte count is not a size; it shows as none rather
			// than subtracting from the total (the record is untrusted text).
			row.ArtifactBytes += max(0, a.Bytes)
		}
	}
	if e != nil && row.LogURL == "" {
		row.LogURL = e.LogURL
	}
	unstarted := in.Launch == nil && r == nil
	if in.Problem != "" {
		// Settled: re-reading won't make an untrusted object trustworthy.
		row.Status, row.Reason, row.Execution, row.Settled = StatusError, in.Problem, "", true
		row.Cost = runstore.ModelOnlyCost(0, runstore.BasisAPIList)
		return row
	}
	switch {
	case r != nil && r.Status != runstore.StatusRunning:
		row.Status = string(r.Status)
	case unstarted && in.CancelMarker:
		row.Status, row.Reason = string(runstore.StatusCancelled), "cancelled before launch"
	case unstarted && in.Claim != nil && now.Sub(in.Claim.At) < runstore.ClaimTTL:
		row.Status = StatusLaunching
	case unstarted:
		row.Status = StatusUnlaunched // no claim, or a stale one: its launcher is gone
	case e != nil && e.State == backend.StateCancelled:
		row.Status, row.Reason = string(runstore.StatusCancelled), "execution cancelled before the run finalized"
	case e != nil && e.Preempted:
		// The platform's own report of the execution settles what happened to
		// the run's machine, and the record above never settled the run: this
		// is a reclamation, not a fault, and it is derived here rather than
		// written anywhere (DerivedPreempted). It comes after the record's own
		// verdict and must: a run that DID finalize keeps what it recorded.
		row.Status, row.Reason, row.Derived = string(runstore.StatusFailed), ReasonPreempted, DerivedPreempted
	case e != nil && e.State.Terminal():
		row.Status, row.Reason = string(runstore.StatusInfraError), ReasonNoFinalRecord+" ("+string(e.State)+")"
	case e != nil && e.State == backend.StatePending:
		row.Status = StatusPending
	case e == nil && r == nil && Lost(in.Launch, row.Created, now):
		// A live execution is always found by the backend; past the claim
		// TTL, lag can't explain its absence.
		row.Status, row.Reason = string(runstore.StatusInfraError), ReasonLost
	case e == nil && r != nil && r.Deadline != nil && now.After(*r.Deadline):
		row.Status, row.Reason = string(runstore.StatusInfraError), "no execution found and past the run's deadline"
	case e == nil && r != nil && r.Deadline == nil:
		// Nothing bounds it and nothing runs it: it will never finish on
		// its own.
		row.Status, row.Reason = string(runstore.StatusInfraError), "no execution found and the run has no deadline"
	case e != nil || r != nil:
		row.Status = string(runstore.StatusRunning)
	default:
		row.Status = StatusPending // launch.json only; the backend doesn't know the execution yet
	}
	switch row.Status {
	case StatusPending, StatusLaunching, StatusUnlaunched, string(runstore.StatusRunning):
	default:
		row.Terminal = true
	}
	row.Settled = row.Terminal || row.Status == StatusUnlaunched
	row.StaleDraft = staleDraft(&row, r, now)
	row.Cost = cost(r, e, prices, now)
	if unstarted {
		// Never launched: no compute at all, which is known, not unestimated.
		row.Cost = runstore.NewCost(row.Cost.ModelUSD, 0, row.Cost.ModelBasis)
	}
	return row
}

// staleDraft reports whether row is a first run whose record is not final,
// has a PR, and either lost its execution, or (execution alive) is past its
// recorded deadline, or with no deadline has gone StaleDraftAfter without
// writing the PR's status section (a follow-up's PR may be ready,
// so it is never called a draft here).
func staleDraft(row *Row, r *runstore.Record, now time.Time) bool {
	if r == nil || r.Status != runstore.StatusRunning || r.PR == nil || r.PR.Number == 0 || row.FollowUp {
		return false
	}
	switch {
	case row.Terminal: // the execution ended or is gone, the record never finalized
		return true
	case row.Status != string(runstore.StatusRunning):
		return false
	case r.Deadline != nil:
		// Alive but past the run's own deadline: it should have ended.
		return now.After(*r.Deadline)
	case r.PR.StatusAt != nil:
		return now.Sub(*r.PR.StatusAt) >= StaleDraftAfter
	}
	return false
}

// followUpFields sets row's branch, PR and follow-up fields from task t
// and record r, either of which may be nil.
func followUpFields(row *Row, t *task.Spec, r *runstore.Record) {
	if t != nil {
		row.Branch, row.TaskPR, row.PreviousRun = t.Branch, t.PR, t.PreviousRun
		row.FollowUp = t.IsFollowUp()
	}
	if r != nil {
		if r.Branch != "" {
			row.Branch = r.Branch
		}
		row.Outcome, row.BaseBranch = string(r.Outcome), r.BaseBranch
		row.DraftFallback = r.DraftFallback
		if r.PR != nil {
			row.RecordPR = r.PR.Number
			row.PRStatusAt = r.PR.StatusAt
		}
		if !r.StartedAt.IsZero() {
			at := r.StartedAt
			row.StartedAt = &at
		}
		if fu := r.FollowUp; fu != nil {
			row.FollowUp = true
			if row.PreviousRun == "" {
				row.PreviousRun = fu.PreviousRun
			}
		}
	}
	row.PR = row.RecordPR
	if row.PR == 0 {
		row.PR = row.TaskPR
	}
	row.PushedHead, row.Pushed = Pushed(t, r)
}

// Lost reports whether launch l, of a run with no record and no execution
// the backend knows, is older than runstore.ClaimTTL, by launched_at, else
// by created (the run ID's time): the run is then lost, not pending.
func Lost(l *runstore.Launch, created, now time.Time) bool {
	at := created
	if l != nil && !l.LaunchedAt.IsZero() {
		at = l.LaunchedAt
	}
	return l != nil && !at.IsZero() && now.Sub(at) > runstore.ClaimTTL
}

// cost is the row's cost: the model spend from the record, and compute
// from the execution when its resources are known, priced with the price
// table of the execution's own backend, region and machine type. The
// record's own facts about the model charge that are not recomputed here are
// carried over — today CatalogStale, which `diagnose` reports (design
// provider-price-catalog.md §7, P7) — because the row is a view of that
// charge, not a second opinion on it; the compute estimate's own source line
// is carried over with it.
func cost(r *runstore.Record, e *backend.Execution, prices PriceBook, now time.Time) runstore.Cost {
	basis, model := runstore.BasisAPIList, 0.0
	var stored *runstore.Cost
	if r != nil {
		model, stored = r.CostUSD, r.Cost
		if stored != nil && stored.ModelBasis != "" {
			basis = stored.ModelBasis
		}
	}
	var c runstore.Cost
	switch {
	case e != nil && (e.CPU > 0 || e.MemoryGiB > 0 || e.MachineType != ""):
		// The window is the execution's own: a VM is billed from its boot
		// window with a one-minute minimum, Cloud Run from its start
		// (backend.Execution.BilledWindow). A window or a price table the
		// backend has not reported leaves compute unestimated, never 0. A
		// VM-backed execution qualifies on its machine type alone: Batch
		// reports no CPU/MemoryGiB limits, so requiring them would leave
		// every VM run on the model-only fallback (design §5.2).
		d, known := e.BilledWindow(now)
		p := prices(e)
		switch {
		case !known || !priced(p):
			c = runstore.ModelOnlyCost(model, basis)
		default:
			// A price table that bills a machine type carries that type's
			// quantities, and they decide the figure: a VM is billed for its
			// machine type whatever the workflow's resources asked for
			// (Prices.Quantities, design compute-backend.md §5.2).
			cpu, mem := p.Quantities(e.CPU, e.MemoryGiB)
			c = runstore.NewCost(model, p.ComputeUSD(cpu, mem, d), basis)
			// Only an estimate priced from a table that names itself
			// records a source line; Cloud Run's list price is what it
			// always was, and its rows are unchanged. A VM run also records
			// which time its window started from, so a reader can tell a
			// window that includes Batch's queue time from one that does
			// not (§5.2).
			if e.MachineType != "" {
				c.ComputeSource = p.Source
				if n := e.WindowNote(); n != "" {
					if c.ComputeSource == "" {
						c.ComputeSource = n
					} else {
						c.ComputeSource += "; " + n
					}
				}
			}
		}
	case stored != nil && stored.ComputeEstimated:
		c = runstore.NewCost(model, stored.ComputeUSD, basis)
		c.ComputeSource = stored.ComputeSource
	default:
		c = runstore.ModelOnlyCost(model, basis)
	}
	if stored != nil {
		c.CatalogStale = stored.CatalogStale
		// The cache token totals travel for the same reason the staleness
		// flag does: only the run's own gateway counted them, and no
		// execution the row re-prices can recompute them (P12).
		c.CacheReadTokens, c.CacheWriteTokens = stored.CacheReadTokens, stored.CacheWriteTokens
		// The tiers a call came back at that the table has no multiplier
		// for travel for the same reason: they name the conservative charge
		// the row's model figure holds, and no execution the row re-prices
		// can recover them (§6.1).
		c.ServiceTiers = stored.ServiceTiers
		// The notional part travels with the model figure: the row is a view
		// of the record's charge, and rebuilding the totals from ModelUSD and
		// ModelBasis alone would bill a cascade run's tier-A dollars as money
		// (P12, D140) -- precisely what the basis cannot tell apart. Applied
		// through WithNotional so the row's TotalUSD is recomputed from it.
		c = c.WithNotional(stored.ModelSubscriptionUSD())
	}
	return c
}

// Totals sums rows' costs (design §10.1).
type Totals struct {
	Runs             int     `json:"runs"`
	ModelUSD         float64 `json:"model_usd"`          // billed model spend: the money part of the rows' model figure
	ModelNotionalUSD float64 `json:"model_notional_usd"` // subscription model spend, not billed
	ComputeUSD       float64 `json:"compute_usd"`
	TotalUSD         float64 `json:"total_usd"`
	// ComputeNotEstimated counts the rows whose compute is unknown: their
	// compute_usd 0 means "not estimated", so the sums are a lower bound.
	ComputeNotEstimated int `json:"compute_not_estimated"`
}

// Sum totals rows.
func Sum(rows []Row) Totals {
	t := Totals{Runs: len(rows)}
	for _, r := range rows {
		// The split is the cost's own notional part, not a reading of the
		// basis: a mixed run's basis is api-list while part of its model
		// figure is the subscription's (P12, D140), and summing ModelUSD into
		// the billed column would call that part money.
		t.ModelNotionalUSD += r.Cost.ModelSubscriptionUSD()
		t.ModelUSD += r.Cost.ModelMoneyUSD()
		t.ComputeUSD += r.Cost.ComputeUSD
		t.TotalUSD += r.Cost.TotalUSD
		if !r.Cost.ComputeEstimated {
			t.ComputeNotEstimated++
		}
	}
	return t
}
