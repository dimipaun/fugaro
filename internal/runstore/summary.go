package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/task"
)

// SummaryVersion is the format version of one run's summary object. It is
// written on every summary and it is the only version this reader reads: a
// summary of another version is left alone and the run's three objects are
// read instead, because a field never changes meaning and a version this
// reader does not know is a shape it cannot vouch for (F8 of
// docs/runs-ls-speed).
const SummaryVersion = 1

// SummaryName is the object a run's summary lives at, a sibling of the three
// objects a listing reads today: runs/<slug>/<run>/summary.json. It sits
// under the job account's existing runs/<slug>/ grant, so it needs no IAM
// change, and it is written by the run's own job account — which is not
// trusted, exactly as that account's task.json, result.json and the
// pull-request index entry are not (SECURITY.md, docs/bucket-iam/spec.md
// §2.1).
const SummaryName = "summary.json"

// Summary is one run's summary object: the parts of the run's task and record
// that a listing's row is built from, so `runs ls` reads one object instead of
// three (docs/runs-ls-speed, RL4, D2966).
//
// It is a HINT and never a source of rows or of decisions. It is written by
// the run's own job account, so a reader trusts nothing in it beyond what it
// can check: the object is decoded strictly (an unknown member, a second JSON
// value or a value of the wrong type is refused) and checked against the run
// it was found under (the version, the run ID and the slug). A summary that
// fails any of those is not an error: a listing reads the run's three objects
// instead — which is what every run written before this object existed needs,
// and what the run's own record always remains the truth for.
//
// What it does NOT carry is as deliberate as what it does. The launch-time
// fields a row shows — who launched the run, its log URL, the plan task —
// belong to launch.json, which a LAUNCHER wrote, and this object may never
// override them: a listing reads launch.json beside this one and takes those
// fields from there. The execution is read the same way. So the summary
// stands in for task.json and result.json alone, the two objects a run's own
// account writes anyway.
type Summary struct {
	// Version is SummaryVersion on every summary this tree writes. A reader
	// refuses any other value rather than read it as this shape.
	Version int `json:"version"`
	// RunID is the run's own ID, which is also the directory the object sits
	// under. A reader refuses a summary whose run_id is not the run it asked
	// for, so one run's summary can never stand in for another's.
	RunID string `json:"run_id"`
	// Slug is the repository slug, which is also the directory the run sits
	// under.
	Slug string `json:"slug"`
	// At is when the summary was written, in UTC, from the writing run's own
	// clock. It orders nothing and decides nothing: the run that wrote it is
	// not trusted, and every field a row shows is read from the block below
	// rather than from when it was written.
	At time.Time `json:"at"`
	// Task is the part of the run's task.json a row is built from. It is
	// absent for a run whose task could not be read, which is the row the
	// three objects give for an unreadable task.json too.
	Task *SummaryTask `json:"task,omitempty"`
	// Record is the part of the run's result.json a row is built from. It is
	// never absent from a summary that reaches a reader: a summary with no
	// record stands in for nothing, so the writer refuses to write one and a
	// reader refuses to read one.
	Record *SummaryRecord `json:"record,omitempty"`
}

// SummaryTask is the part of a run's task.json a row is built from: the
// fields runview.Join reads off a task.Spec, and nothing else. The task's own
// text — the prompt, the recipe's YAML, the project layer, the attached
// files — is not here, which is most of what reading task.json costs.
type SummaryTask struct {
	// Repo, Workflow, Batch and RequestedBy are the four the row carries
	// whole (its REPO cell, its workflow, its batch and its requester).
	Repo        string `json:"repo"`
	Workflow    string `json:"workflow,omitempty"`
	Batch       string `json:"batch,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
	// Title is the run's title as task.Spec.TitleLine gives it — the stored
	// title, else the prompt's own first line — because the prompt itself is
	// not carried: a reader must not have to derive what the writer already
	// knew.
	Title string `json:"title,omitempty"`
	// OAuthToken is the named account the launch resolved, which the row
	// falls back to when the record names none.
	OAuthToken string `json:"oauth_token,omitempty"`
	// Recipe is the recipe's name the task named, the row's fallback when the
	// record names none; never the recipe's YAML, which no row shows.
	Recipe string `json:"recipe,omitempty"`
	// Branch, PR, PreviousRun and FollowUpOf are the follow-up fields: they
	// say whether the run continues a pull request, which pull request, and
	// the deferral it was filed from (task.Spec.IsFollowUp is Branch alone).
	Branch      string           `json:"branch,omitempty"`
	PR          int              `json:"pr,omitempty"`
	PreviousRun string           `json:"previous_run,omitempty"`
	FollowUpOf  *task.FollowUpOf `json:"follow_up_of,omitempty"`
	// ReviewPRs is set for a review run: the pull requests it reviews, which
	// is how a listing reads a review's row — and how the pull-request state
	// fold sees the review going on a pull request at all — while its record
	// names no pull request yet.
	ReviewPRs []int `json:"review_prs,omitempty"`
	// Debt marks a task filed from a triage deferral, which `runs ls --debt`
	// filters on.
	Debt bool `json:"debt,omitempty"`
}

// SummaryRecord is the part of a run's result.json a row is built from: the
// fields runview.Join, its cost reading and its stale-draft reading take off
// a runstore.Record, and nothing else. What is left out is everything a row
// never shows and nothing reads back: the stage timings, the review rounds,
// the verify records, the switch log, the policy, the budget block, the
// inputs, the chain raises.
//
// The blocks it does carry — the halt, the changed files, the rigor record,
// the owner decisions, the project layer, the cost, the image, the pull
// request, the follow-up — are the record's own Go types and are copied
// verbatim, so a row cannot be built from a summary that means something
// else: the reader hands them to the same join the three objects go through.
type SummaryRecord struct {
	Status Status `json:"status"`
	Stage  string `json:"stage"`
	// Reason, FailureClass and Halt are what the run's ending says: the row's
	// reason, its failure class (read through runview.ClassOf when the record
	// wrote none, which is why the status, the reason and the halt travel
	// with it) and its halt block.
	Reason       string        `json:"reason,omitempty"`
	FailureClass failure.Class `json:"failure_class,omitempty"`
	Halt         *Halt         `json:"halt,omitempty"`
	// Outcome is the run's own word for a pull request's state (`ready`,
	// `draft`), which is what the listing's pull-request fold reads.
	Outcome Outcome `json:"outcome,omitempty"`
	// Branch, BaseBranch, HeadSHA and PushedHead are the row's branch, its
	// base branch, and what says whether the run pushed: a record with
	// pushed_head has, and otherwise a head commit on a branch of this run's
	// own does (runview.Pushed).
	Branch        string `json:"branch,omitempty"`
	BaseBranch    string `json:"base_branch,omitempty"`
	HeadSHA       string `json:"head_sha,omitempty"`
	PushedHead    string `json:"pushed_head,omitempty"`
	PR            *PRRef `json:"pr,omitempty"`
	DraftFallback bool   `json:"draft_fallback,omitempty"`
	// NeedsGuidance is the record's own needs_guidance (LV5.2, LD8): the run's
	// agent asked a question it may not settle by itself, so the pull request is
	// a draft whose status section opens with OPEN QUESTIONS. It travels with
	// the summary because a row shows it, exactly as DraftFallback beside it
	// does, and a summary that dropped it would build a row that says the run
	// asked nothing.
	NeedsGuidance bool `json:"needs_guidance,omitempty"`
	// StartedAt and Deadline are the run's own two moments: the row's start
	// (and, with the image's build time, its image age), and the bound a run
	// with no execution left is judged against.
	StartedAt time.Time  `json:"started_at"`
	Deadline  *time.Time `json:"deadline,omitempty"`
	// FollowUp is the record's follow-up block, which places a run on a pull
	// request and names the run it continues when the task does not.
	FollowUp *FollowUp `json:"follow_up,omitempty"`
	// OAuthAccount is the account the run's own record confirmed ("" for the
	// repository's default), the row's first choice beside the task's.
	OAuthAccount string `json:"oauth_account,omitempty"`
	// Recipe is the recipe's name as the record wrote it.
	Recipe string `json:"recipe,omitempty"`
	// ProjectLayer and ConfigSHA256 trace the run to the layer and the config
	// it ran with, which the row carries whole.
	ProjectLayer *ProjectLayerRecord `json:"project_layer,omitempty"`
	ConfigSHA256 string              `json:"config_sha256,omitempty"`
	// Image is what the image was built from; its build time is the row's
	// image age.
	Image *ImageInfo `json:"image,omitempty"`
	// ChangedFiles, Rigor and OwnerDecisions are the three blocks a listing
	// shows a column (or a "Waiting on you" line) from, each bounded again on
	// read the way the record's own are.
	ChangedFiles   *ChangedFiles   `json:"changed_files,omitempty"`
	Rigor          *RigorRecord    `json:"rigor,omitempty"`
	OwnerDecisions *OwnerDecisions `json:"owner_decisions,omitempty"`
	// Artifacts is the files the run delivered: a row shows how many there
	// were and their total, and it counts them off this list, which is the
	// record's own.
	Artifacts []ArtifactFile `json:"artifacts,omitempty"`
	// Execution is the execution that owns the run, which is what a listing
	// follows and what tells two launches of one run apart.
	Execution string `json:"execution,omitempty"`
	// CostUSD and Cost are the run's model spend and its recorded breakdown:
	// the row's own figure is priced from them and from the execution, and
	// the parts only the run's gateway counted (the cache tokens, the
	// service tiers, the notional split, the catalog staleness) travel with
	// the block.
	CostUSD float64 `json:"cost_usd"`
	Cost    *Cost   `json:"cost,omitempty"`
}

// NewSummary projects one run's record and task into the summary a listing
// reads. rec is the run's record and must not be nil — a run with no record
// has nothing to summarize, and this writer is called where the record is
// written — while spec may be nil, which is a run whose task could not be
// read: the summary then carries no task block, and a row built from it says
// the task is unreadable, exactly as the row of the three objects does.
//
// The projection is one way: WriteSummary is the only writer of the object,
// and a reader turns the summary back into the two blocks runview.Join takes
// (Spec, AsRecord), so a row cannot be built from a summary that dropped a
// field the row shows — TestSummaryEqualsRecords is what pins that.
func NewSummary(rec *Record, spec *task.Spec, at time.Time) *Summary {
	sum := &Summary{
		Version: SummaryVersion,
		RunID:   rec.RunID,
		At:      at.UTC(),
		Record: &SummaryRecord{
			Status:         rec.Status,
			Stage:          rec.Stage,
			Reason:         rec.Reason,
			FailureClass:   rec.FailureClass,
			Halt:           rec.Halt,
			Outcome:        rec.Outcome,
			Branch:         rec.Branch,
			BaseBranch:     rec.BaseBranch,
			HeadSHA:        rec.HeadSHA,
			PushedHead:     rec.PushedHead,
			PR:             rec.PR,
			DraftFallback:  rec.DraftFallback,
			NeedsGuidance:  rec.NeedsGuidance,
			StartedAt:      rec.StartedAt,
			Deadline:       rec.Deadline,
			FollowUp:       rec.FollowUp,
			OAuthAccount:   rec.OAuthAccount,
			ProjectLayer:   rec.ProjectLayer,
			ConfigSHA256:   rec.ConfigSHA256,
			Image:          rec.Image,
			ChangedFiles:   rec.ChangedFiles,
			Rigor:          rec.Rigor,
			OwnerDecisions: rec.OwnerDecisions,
			Artifacts:      rec.Artifacts,
			Execution:      rec.Execution,
			CostUSD:        rec.CostUSD,
			Cost:           rec.Cost,
		},
	}
	if rec.Recipe != nil {
		sum.Record.Recipe = rec.Recipe.Name
	}
	if spec != nil {
		sum.Task = &SummaryTask{
			Repo:        spec.Repo,
			Workflow:    spec.Workflow,
			Batch:       spec.Batch,
			RequestedBy: spec.RequestedBy,
			Title:       spec.TitleLine(),
			OAuthToken:  spec.OAuthToken,
			Branch:      spec.Branch,
			PR:          spec.PR,
			PreviousRun: spec.PreviousRun,
			FollowUpOf:  spec.FollowUpOf,
			ReviewPRs:   spec.ReviewPRs,
			Debt:        spec.Debt,
		}
		if spec.Recipe != nil {
			sum.Task.Recipe = spec.Recipe.Name
		}
	}
	return sum
}

// Spec is the task the summary stands in for: the fields the projection
// carries, on an otherwise empty spec. Nothing else is read off it — the
// prompt, the ref, the overrides and the project layer are not what a row
// shows — so a reader may not use this spec for anything but the row it is
// built for.
func (s *Summary) Spec() *task.Spec {
	if s.Task == nil {
		return nil
	}
	t := &task.Spec{
		RunID:       s.RunID,
		Repo:        s.Task.Repo,
		Workflow:    s.Task.Workflow,
		Task:        s.Task.Title,
		Title:       s.Task.Title,
		OAuthToken:  s.Task.OAuthToken,
		Branch:      s.Task.Branch,
		PR:          s.Task.PR,
		PreviousRun: s.Task.PreviousRun,
		FollowUpOf:  s.Task.FollowUpOf,
		ReviewPRs:   s.Task.ReviewPRs,
		Debt:        s.Task.Debt,
		Batch:       s.Task.Batch,
		RequestedBy: s.Task.RequestedBy,
	}
	if s.Task.Recipe != "" {
		t.Recipe = &task.Recipe{Name: s.Task.Recipe}
	}
	return t
}

// AsRecord is the record the summary stands in for: the fields the projection
// carries, on an otherwise empty record. Like Spec, it is the row's own
// record and nothing more — the parts a row never shows are absent, so it may
// not be read for anything else (the run's reports, its verify records, its
// stage timings).
func (s *Summary) AsRecord() *Record {
	if s.Record == nil {
		return nil
	}
	r := &Record{
		RunID:          s.RunID,
		Status:         s.Record.Status,
		Stage:          s.Record.Stage,
		Reason:         s.Record.Reason,
		FailureClass:   s.Record.FailureClass,
		Halt:           s.Record.Halt,
		Outcome:        s.Record.Outcome,
		Branch:         s.Record.Branch,
		BaseBranch:     s.Record.BaseBranch,
		HeadSHA:        s.Record.HeadSHA,
		PushedHead:     s.Record.PushedHead,
		PR:             s.Record.PR,
		DraftFallback:  s.Record.DraftFallback,
		NeedsGuidance:  s.Record.NeedsGuidance,
		StartedAt:      s.Record.StartedAt,
		Deadline:       s.Record.Deadline,
		FollowUp:       s.Record.FollowUp,
		OAuthAccount:   s.Record.OAuthAccount,
		ProjectLayer:   s.Record.ProjectLayer,
		ConfigSHA256:   s.Record.ConfigSHA256,
		Image:          s.Record.Image,
		ChangedFiles:   s.Record.ChangedFiles,
		Rigor:          s.Record.Rigor,
		OwnerDecisions: s.Record.OwnerDecisions,
		Artifacts:      s.Record.Artifacts,
		Execution:      s.Record.Execution,
		CostUSD:        s.Record.CostUSD,
		Cost:           s.Record.Cost,
	}
	if s.Record.Recipe != "" {
		r.Recipe = &RecipeRecord{Name: s.Record.Recipe}
	}
	return r
}

// WriteSummary writes one run's summary object, overwriting whatever the
// run's previous save left: the summary is the run's own account of itself
// and moves with its record, so unlike the pull-request index it is rewritten
// at every stage end (D2966). encodeSummary is where the identity is stamped
// and the shape checked.
func WriteSummary(ctx context.Context, s *Store, sum *Summary) error {
	data, err := encodeSummary(s, sum)
	if err != nil {
		return err
	}
	return s.PutFile(ctx, SummaryName, data, "application/json")
}

// CreateSummary writes one run's summary object only if the run has none
// (ErrExists otherwise). It is the backfill's writer (internal/cli's
// update_summary.go): a summary is the run's own account of itself, so a step
// of `fugaro update` creates the object an installation predating it is
// missing and never rewrites one a run holds — the HARD RULE, and what keeps
// the walk off a run in flight (docs/design/update-in-flight.md).
func CreateSummary(ctx context.Context, s *Store, sum *Summary) error {
	data, err := encodeSummary(s, sum)
	if err != nil {
		return err
	}
	return s.create(ctx, SummaryName, data, "application/json")
}

// encodeSummary is the one place a summary is stamped and encoded: the
// identity members are the store's and never the caller's, so a summary this
// writes always names the run whose prefix it sits under and always carries
// SummaryVersion. A summary with no record — nothing to stand in for — and one
// that names no time are refused before the bucket is touched: a listing that
// read such an object back would have to fall back, and an object that can
// only be fallen back from is not worth writing.
func encodeSummary(s *Store, sum *Summary) ([]byte, error) {
	if sum == nil || sum.Record == nil {
		return nil, errors.New("a run summary with no record summarizes nothing")
	}
	if sum.At.IsZero() {
		return nil, fmt.Errorf("the summary of %s names no time", s.RunID())
	}
	sum.Version = SummaryVersion
	sum.RunID, sum.Slug = s.RunID(), s.Slug()
	data, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the summary of %s: %w", s.RunID(), err)
	}
	return data, nil
}

// ReadSummary reads a run's summary object from the bucket: ErrNotFound when
// the run has none, which is every run written before this object existed and
// every run whose summary a reader has to fall back from.
//
// A summary this reader cannot vouch for is ErrCorrupt, the class a
// result.json the bucket made undecodable is in: what DecodeSummary refuses is
// an object that is there and unusable, and a caller tells that apart from a
// read that failed (the bucket, the network) so it can fall back to the run's
// own objects rather than fail over a shortcut.
func (s *Store) ReadSummary(ctx context.Context) (*Summary, error) {
	raw, err := s.ReadFile(ctx, SummaryName)
	if err != nil {
		return nil, err
	}
	sum, err := DecodeSummary(raw, s.Slug(), s.RunID())
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w: %w", SummaryName, ErrCorrupt, err)
	}
	return sum, nil
}

// DecodeSummary reads the bytes of a summary object as the summary of the run
// it was found under, refusing everything about it that does not agree with
// that run or with a shape this reader knows: an unknown member, a second
// JSON value, a value of the wrong type, another version, another run ID,
// another slug, no record to stand in for, or no time at all.
//
// It is deliberately stricter than the contract in
// schemas/run-summary.schema.json, which tolerates a member it does not know so
// a newer writer's summary is still a document an editor can validate: what a reader cannot vouch for it
// refuses, and the caller reads the run's three objects instead. That is why
// every failure here is a summary to fall back from and never an error a
// listing reports: an object that is only a shortcut may not break the
// listing it was meant to speed up.
func DecodeSummary(raw []byte, slug, runID string) (*Summary, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var sum Summary
	if err := dec.Decode(&sum); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the summary carries more than one JSON value")
	}
	switch {
	case sum.Version != SummaryVersion:
		return nil, fmt.Errorf("the summary's version is %d, not %d", sum.Version, SummaryVersion)
	case sum.RunID != runID || sum.Slug != slug:
		return nil, fmt.Errorf("the summary is for %s/%s, not %s/%s", sum.Slug, sum.RunID, slug, runID)
	case sum.Record == nil:
		// A summary with no record stands in for nothing: the run's own
		// three objects are the only answer left, which is what the caller
		// reads when this returns an error.
		return nil, errors.New("the summary carries no record")
	case sum.At.IsZero():
		return nil, errors.New("the summary names no time")
	}
	return &sum, nil
}
