// Package runstore reads and writes a run's objects in the runs bucket (design §3.3).
package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

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
	Status     Status          `json:"status"`
	Stage      string          `json:"stage"`
	Outcome    Outcome         `json:"outcome"`
	Reason     string          `json:"reason,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	HeadSHA    string          `json:"head_sha,omitempty"`
	PR         *PRRef          `json:"pr,omitempty"`
	Reviews    []ReviewSummary `json:"reviews,omitempty"`
	Verify     []verify.Record `json:"verify,omitempty"`
	CostUSD    float64         `json:"cost_usd"`
	Stages     []StageTiming   `json:"stages,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// ErrNotFound means the requested object does not exist.
var ErrNotFound = errors.New("not found")

// Store addresses one run's objects: runs/<repo-slug>/<run-id>/...
type Store struct {
	bucket *blob.Bucket
	prefix string
}

// Open returns the store for one run.
func Open(b *blob.Bucket, repoSlug, runID string) *Store {
	return &Store{bucket: b, prefix: path.Join("runs", repoSlug, runID) + "/"}
}

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

func (s *Store) read(ctx context.Context, name string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, s.prefix+name)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, fmt.Errorf("%s%s: %w", s.prefix, name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s%s: %w", s.prefix, name, err)
	}
	return data, nil
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
	data, err := s.read(ctx, "task.json")
	if err != nil {
		return nil, err
	}
	return task.Parse(data)
}

// WriteRecord stores result.json.
func (s *Store) WriteRecord(ctx context.Context, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "result.json", data, "application/json")
}

// ReadRecord loads result.json.
func (s *Store) ReadRecord(ctx context.Context) (*Record, error) {
	data, err := s.read(ctx, "result.json")
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
