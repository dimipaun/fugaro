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
)

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
}

// ReviewSummary is one review round's verdict.
type ReviewSummary struct {
	Round    int    `json:"round"`
	Verdict  string `json:"verdict"`
	Findings int    `json:"findings"`
}

// StageTiming records how long one stage took.
type StageTiming struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
}

// Record is result.json, the run record (design §4.6).
type Record struct {
	Version    int             `json:"version"`
	RunID      string          `json:"run_id"`
	Repo       string          `json:"repo"`
	Workflow   string          `json:"workflow,omitempty"`
	Execution  string          `json:"execution,omitempty"` // canonical full name, never the short CLOUD_RUN_EXECUTION; compare only via backend.SameExecution
	Status     Status          `json:"status"`
	Stage      string          `json:"stage"`
	Outcome    Outcome         `json:"outcome"`
	Reason     string          `json:"reason,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	HeadSHA    string          `json:"head_sha,omitempty"`
	PR         *PRRef          `json:"pr,omitempty"`
	Reviews    []ReviewSummary `json:"reviews,omitempty"`
	Verify     []verify.Record `json:"verify,omitempty"`
	CostUSD    float64         `json:"cost_usd"` // the model spend (design §4.6)
	Cost       *Cost           `json:"cost,omitempty"`
	Stages     []StageTiming   `json:"stages,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	Deadline   *time.Time      `json:"deadline,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	// FinalizeReserveS is the workflow's timeouts.finalize_reserve at the
	// task's ref, overrides applied, in seconds; read it with
	// FinalizeReserve. Absent until bootstrap has read fugaro.yaml.
	FinalizeReserveS float64 `json:"finalize_reserve_s,omitempty"`
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

// Read caps (S-M4). The job's service account, and so a run's agent, can
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

// RunID is the run ID this store was opened with.
func (s *Store) RunID() string { return s.runID }

// Slug is the repository slug this store was opened with. The runner keys
// the branch lock and caches on it, so they always agree with the run's
// own prefix, however the slug was obtained (FUGARO_RUN or --task-file).
func (s *Store) Slug() string { return s.slug }

var runRefRE = regexp.MustCompile(`^([a-z0-9._-]+)/([0-9]{8}-[0-9]{6}-[0-9a-f]{4})$`)

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
