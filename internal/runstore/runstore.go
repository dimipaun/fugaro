// Package runstore reads and writes a run's objects in the runs bucket (design §3.3).
package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Status is the lifecycle state of a run.
type Status string

const (
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded" // a ready PR exists
	StatusFailed     Status = "failed"    // a draft PR exists
	StatusInfraError Status = "infra_error"
	StatusCancelled  Status = "cancelled"
	StatusHalted     Status = "halted" // a budget or token limit stopped the run
)

// HaltReason says which limit halted a run.
type HaltReason string

const (
	HaltKillSwitch         HaltReason = "kill_switch"
	HaltRunCap             HaltReason = "run_cap"
	HaltRepoDailyCap       HaltReason = "repo_daily_cap"
	HaltGlobalDailyCap     HaltReason = "global_daily_cap"
	HaltNoCap              HaltReason = "no_cap"
	HaltTokenCap           HaltReason = "token_cap"
	HaltBudgetUnavailable  HaltReason = "budget_unavailable"
	HaltBudgetTokenExpired HaltReason = "budget_token_expired"
)

// Halt is why and when a run was halted.
type Halt struct {
	Reason HaltReason `json:"reason"`
	Scope  string     `json:"scope"` // run | repo | global
	At     time.Time  `json:"at"`
	Detail string     `json:"detail,omitempty"`
}

// Outcome is the state of the run's pull request.
type Outcome string

const (
	OutcomeReady Outcome = "ready"
	OutcomeDraft Outcome = "draft"
	OutcomeNone  Outcome = "none"
)

// PRRef identifies the run's pull request.
type PRRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	// Desc is a digest of the title and description (outside the status
	// section) the runner last wrote, so finalize replaces them from pr.md
	// only while nobody has edited them. Empty for a follow-up's PR and
	// for one the runner opened at finalize.
	Desc string `json:"desc,omitempty"`
	// StatusAt is when the runner last wrote the status section into the
	// description. A running record whose StatusAt is long past is the
	// stale draft of a run that died; diagnose and ls say so.
	StatusAt *time.Time `json:"status_at,omitempty"`
}

// ReviewSummary is one review round's verdict.
type ReviewSummary struct {
	Round int `json:"round"`
	// Tier is TierFirst for a first-line review (by the coder's model,
	// never deciding readiness) and TierSenior for the reviewer's; empty
	// is a senior review in a run that had no first line.
	Tier     string `json:"tier,omitempty"`
	Verdict  string `json:"verdict"`
	Findings int    `json:"findings"`
}

// Review tiers (ReviewSummary.Tier).
const (
	TierFirst  = "first"
	TierSenior = "senior"
)

// StageTiming records how long one stage took.
type StageTiming struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
}

// Record is result.json, the run record (design §4.6).
type Record struct {
	Version   int     `json:"version"`
	RunID     string  `json:"run_id"`
	Repo      string  `json:"repo"`
	Workflow  string  `json:"workflow,omitempty"`
	Execution string  `json:"execution,omitempty"` // canonical full name, never the short CLOUD_RUN_EXECUTION; compare only via backend.SameExecution
	Status    Status  `json:"status"`
	Stage     string  `json:"stage"`
	Outcome   Outcome `json:"outcome"`
	Reason    string  `json:"reason,omitempty"`
	Branch    string  `json:"branch,omitempty"`
	// BaseBranch is the base the run's pull request targets, the
	// git.base_branch of the config it ran with (not the task's ref, which
	// a first run may set to another branch). A follow-up takes it as its
	// ref. Absent in records from before it was kept.
	BaseBranch string `json:"base_branch,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	PR         *PRRef `json:"pr,omitempty"`
	// DraftFallback is set when the host refused real draft pull requests,
	// so the PR is an ordinary one marked "[DRAFT]" in its title.
	DraftFallback bool            `json:"draft_fallback,omitempty"`
	Reviews       []ReviewSummary `json:"reviews,omitempty"`
	Verify        []verify.Record `json:"verify,omitempty"`
	CostUSD       float64         `json:"cost_usd"` // the model spend (design §4.6)
	Cost          *Cost           `json:"cost,omitempty"`
	Stages        []StageTiming   `json:"stages,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	Deadline      *time.Time      `json:"deadline,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	// FinalizeReserveS is the workflow's timeouts.finalize_reserve at the
	// task's ref, overrides applied, in seconds; read it with
	// FinalizeReserve. Absent until bootstrap has read fugaro.yaml.
	FinalizeReserveS float64 `json:"finalize_reserve_s,omitempty"`
	// Image is what the image the run started in was built from; absent
	// when the runner could not tell.
	Image *ImageInfo `json:"image,omitempty"`
	// FollowUp is set for a run that continues an existing pull request.
	FollowUp *FollowUp `json:"follow_up,omitempty"`
	// PushedHead is the commit the run pushed, set right after a
	// successful push (every run) and saved at once, so a run killed
	// after posting still records that it updated its pull request.
	PushedHead string `json:"pushed_head,omitempty"`
	// Halt is set when Status is halted.
	Halt *Halt `json:"halt,omitempty"`
	// Policy is the budget and model policy the run ran under, set only
	// when a layer set something (a run with no policy writes none).
	Policy *PolicyRecord `json:"policy,omitempty"`
	// Budget is the run's part in the project's shared budget, set only
	// when the run used the budget backend (M9b).
	Budget *BudgetRecord `json:"budget,omitempty"`
}

// BudgetRecord says how the run used the shared budget: the UTC day number
// it started on, what its leases granted and gave back in micro-dollars, the
// effective mode and the backend's kind.
type BudgetRecord struct {
	Day            int64  `json:"day"`
	GrantedMicros  int64  `json:"granted_micros"`
	ReleasedMicros int64  `json:"released_micros"`
	Mode           string `json:"mode"`
	Backend        string `json:"backend"`
}

// PolicyRecord is the effective budget and model policy of a run, merged
// from the owner's ceiling, the default branch's fugaro.yaml and the run's
// own, and what was dropped for being looser.
type PolicyRecord struct {
	Effective PolicyEffective `json:"effective"`
	// Sources maps each key that is set to the layer that set it:
	// "ceiling", "default-branch" or "branch".
	Sources map[string]string `json:"sources,omitempty"`
	Ignored []PolicyIgnored   `json:"ignored,omitempty"`
}

// PolicyEffective is the merged values. A zero value means unset, except
// AllowedModels: nil is unset (every model is allowed) and an empty
// non-nil list is set and forbids every model, so it is written as [] and
// read back as an empty non-nil slice.
type PolicyEffective struct {
	PerRunUSD       float64       `json:"per_run_usd,omitempty"`
	PerDayUSD       float64       `json:"per_day_usd,omitempty"`
	Mode            string        `json:"mode,omitempty"`
	MaxRunTokens    int64         `json:"max_run_tokens,omitempty"`
	MaxOutputTokens *PolicyOutput `json:"max_output_tokens,omitempty"`
	AllowedModels   []string      `json:"allowed_models"`
}

// PolicyOutput is the per-call output limits by role.
type PolicyOutput struct {
	Coder    int64 `json:"coder,omitempty"`
	Reviewer int64 `json:"reviewer,omitempty"`
}

// MarshalJSON omits allowed_models only when it is nil: an empty list is a
// restriction and must survive the round trip.
func (e PolicyEffective) MarshalJSON() ([]byte, error) {
	type plain PolicyEffective
	aux := struct {
		plain
		AllowedModels *[]string `json:"allowed_models,omitempty"`
	}{plain: plain(e)}
	if e.AllowedModels != nil {
		aux.AllowedModels = &e.AllowedModels
	}
	return json.Marshal(aux)
}

// PolicyIgnored is a value a layer set that was looser than the limits in
// force and was dropped. Value and Effective are text; Source is the layer
// of the effective value and From the layer that asked for Value.
type PolicyIgnored struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Effective string `json:"effective"`
	Source    string `json:"source"`
	From      string `json:"from"`
}

// FollowUp is what a follow-up run acted on (design §4.4).
type FollowUp struct {
	PR          int    `json:"pr"`
	PreviousRun string `json:"previous_run"`
	// StartSHA is the pull request's head the run started from.
	StartSHA string `json:"start_sha,omitempty"`
	// Session is "resumed" or "fresh"; SessionNote says why.
	Session     string `json:"session,omitempty"`
	SessionNote string `json:"session_note,omitempty"`
	// Comments is how many comments reached the agent.
	Comments int `json:"comments"`
	// Authors maps each author's display name to the comments of theirs used.
	Authors map[string]int `json:"authors,omitempty"`
	// UntrustedAuthors are the display names whose comments were dropped
	// because their authors aren't trusted: sorted, at most 20.
	UntrustedAuthors []string `json:"untrusted_authors,omitempty"`
	// UntrustedAuthorCount is how many distinct untrusted authors were
	// dropped, the ones beyond the 20 named included; absent in records
	// from before it was kept.
	UntrustedAuthorCount int `json:"untrusted_author_count,omitempty"`
	// Omitted counts the comments left out, by reason.
	Omitted map[string]int `json:"omitted,omitempty"`
}

// ImageInfo says what the run's image was built from, read at bootstrap
// before the sync moves the checkout.
type ImageInfo struct {
	// BuiltAt is when the image was built; nil when the image doesn't say.
	BuiltAt *time.Time `json:"built_at,omitempty"`
	// BakedCommit is the baked checkout's HEAD when the run started.
	BakedCommit string `json:"baked_commit"`
}

// FinalizeReserve is the run's finalize reserve, for cancel's grace floor
// without a checkout. ok is false when the record has none: an older
// record, or a run that failed before bootstrap read its workflow.
func (r *Record) FinalizeReserve() (d time.Duration, ok bool) {
	if r.FinalizeReserveS <= 0 {
		return 0, false
	}
	return time.Duration(r.FinalizeReserveS * float64(time.Second)), true
}

// ErrNotFound means the requested object does not exist.
var ErrNotFound = errors.New("not found")

// ErrAmbiguous means a bare run ID names runs in several repositories.
var ErrAmbiguous = errors.New("ambiguous run ID")

// ErrExists means a create-once object is already there.
var ErrExists = errors.New("already exists")

// ErrTooLarge means an object is larger than its read cap.
var ErrTooLarge = errors.New("object is larger than its read cap")

// Read caps. The job's service account, and so a run's agent, can
// write every object under runs/, and the CLI reads them on the
// operator's machine: a hostile object of gigabytes must fail with
// ErrTooLarge rather than exhaust memory. MaxRecordBytes caps the JSON
// objects (task.json, launch.json, result.json, the launch claim) and
// any other object ReadFile serves; MaxTranscriptBytes caps a stage
// transcript (transcripts/…), which legitimately runs larger.
const (
	MaxRecordBytes     = blobx.MaxReadBytes
	MaxTranscriptBytes = 32 << 20
)

// Store addresses one run's objects: runs/<repo-slug>/<run-id>/...
type Store struct {
	bucket *blob.Bucket
	slug   string
	runID  string
	prefix string
}

// Open returns the store for one run.
func Open(b *blob.Bucket, repoSlug, runID string) *Store {
	return &Store{bucket: b, slug: repoSlug, runID: runID, prefix: path.Join("runs", repoSlug, runID) + "/"}
}

// Sibling returns the store for another run of the same repository, in
// the same bucket. The caller checks runID's form first: it becomes part
// of an object name.
func (s *Store) Sibling(runID string) *Store { return Open(s.bucket, s.slug, runID) }

// RunID is the run ID this store was opened with.
func (s *Store) RunID() string { return s.runID }

// Slug is the repository slug this store was opened with. The runner keys
// the branch lock and caches on it, so they always agree with the run's
// own prefix, however the slug was obtained (FUGARO_RUN or --task-file).
func (s *Store) Slug() string { return s.slug }

// runRefRE takes a slug in task.Slug's alphabet, [a-z0-9-]. With no "."
// allowed, a slug can't be "." or "..", which path.Join would resolve
// outside runs/.
var runRefRE = regexp.MustCompile(`^([a-z0-9-]+)/([0-9]{8}-[0-9]{6}-[0-9a-f]{4})$`)

// ParseRef splits "<repo-slug>/<run-id>".
func ParseRef(ref string) (slug, runID string, err error) {
	m := runRefRE.FindStringSubmatch(ref)
	if m == nil {
		return "", "", fmt.Errorf("run %q must look like <repo-slug>/<run-id>", ref)
	}
	return m[1], m[2], nil
}

// Prefix is the object-name prefix of this run, ending in "/".
func (s *Store) Prefix() string { return s.prefix }

// PutFile writes a file under the run's prefix.
func (s *Store) PutFile(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + strings.TrimPrefix(name, "/")
	if err := s.bucket.WriteAll(ctx, key, data, &blob.WriterOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// read reads one of the run's objects, refusing one larger than max.
func (s *Store) read(ctx context.Context, name string, max int64) ([]byte, error) {
	key := s.prefix + name
	r, err := s.bucket.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	defer r.Close()
	if r.Size() > max {
		return nil, fmt.Errorf("%s: %w (%d bytes, the cap is %d)", key, ErrTooLarge, r.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w (the cap is %d bytes)", key, ErrTooLarge, max)
	}
	return data, nil
}

// readRecordObject reads one of the run's JSON objects under MaxRecordBytes.
func (s *Store) readRecordObject(ctx context.Context, name string) ([]byte, error) {
	return s.read(ctx, name, MaxRecordBytes)
}

// ReadFile reads one of the run's objects, such as
// transcripts/review-1.jsonl: up to MaxTranscriptBytes under transcripts/,
// up to MaxRecordBytes for anything else (ErrTooLarge past it).
func (s *Store) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if strings.HasPrefix(name, "transcripts/") {
		return s.read(ctx, name, MaxTranscriptBytes)
	}
	return s.readRecordObject(ctx, name)
}

// create writes name only if it does not exist yet.
func (s *Store) create(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + name
	err := s.bucket.WriteAll(ctx, key, data, &blob.WriterOptions{ContentType: contentType, IfNotExist: true})
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		return fmt.Errorf("%s: %w", key, ErrExists)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// CreateRecord stores result.json only if none exists yet (ErrExists). On
// Cloud Run the runner writes its first record this way, so a duplicate
// execution of the same run never writes one (design §4.7).
func (s *Store) CreateRecord(ctx context.Context, r *Record) error {
	data, err := encodeRecord(r)
	if err != nil {
		return err
	}
	return s.create(ctx, "result.json", data, "application/json")
}

// CreateTask stores task.json unless it already exists (ErrExists), so two
// launches with one run ID cannot both write a task (design §4.7).
func (s *Store) CreateTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.create(ctx, "task.json", data, "application/json")
}

// WriteTask stores task.json.
func (s *Store) WriteTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "task.json", data, "application/json")
}

// ReadTask loads and validates task.json.
func (s *Store) ReadTask(ctx context.Context) (*task.Spec, error) {
	data, err := s.readRecordObject(ctx, "task.json")
	if err != nil {
		return nil, err
	}
	return task.Parse(data)
}

// WriteRecord stores result.json.
func (s *Store) WriteRecord(ctx context.Context, r *Record) error {
	data, err := encodeRecord(r)
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "result.json", data, "application/json")
}

// ReadRecord loads result.json.
func (s *Store) ReadRecord(ctx context.Context) (*Record, error) {
	data, err := s.readRecordObject(ctx, "result.json")
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decoding result.json: %w", err)
	}
	return &r, nil
}

// RequestCancel asks the running runner to stop and finalize.
func (s *Store) RequestCancel(ctx context.Context) error {
	return s.PutFile(ctx, "cancel", []byte(time.Now().UTC().Format(time.RFC3339)), "text/plain")
}

// CancelRequested reports whether a cancel marker exists.
func (s *Store) CancelRequested(ctx context.Context) (bool, error) {
	return s.bucket.Exists(ctx, s.prefix+"cancel")
}
