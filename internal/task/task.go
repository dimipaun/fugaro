// Package task defines the task spec handed to a Fugaro run (design §5.3).
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

var (
	runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)
)

// BatchRE is the form of a task's batch label.
var BatchRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Spec is the task spec stored as runs/<repo-slug>/<run-id>/task.json.
type Spec struct {
	Version     int       `json:"version"`
	RunID       string    `json:"run_id"`
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref"`
	Workflow    string    `json:"workflow,omitempty"`
	Task        string    `json:"task,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	PR          int       `json:"pr,omitempty"`
	PreviousRun string    `json:"previous_run,omitempty"`
	Overrides   Overrides `json:"overrides"`
	RequestedBy string    `json:"requested_by,omitempty"`
	Batch       string    `json:"batch,omitempty"`
}

// Overrides are the only config values a single task may change.
type Overrides struct {
	ReviewRounds *int     `json:"review_rounds,omitempty"`
	MaxBudgetUSD *float64 `json:"max_budget_usd,omitempty"`
	Model        string   `json:"model,omitempty"`
	TotalTimeout string   `json:"total_timeout,omitempty"`
}

// NewRunID returns a run ID for now, in UTC, with 4 random hex digits from r.
func NewRunID(now time.Time, r io.Reader) (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generating run ID: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

// scpLikeRE matches git's scp-like SSH remote syntax, git@host:owner/repo.
var scpLikeRE = regexp.MustCompile(`^(?:[^@/:]+@)?([^/:]+):(.+)$`)

// CanonicalRepo is the canonical form of a repository reference: its path,
// "owner/name" (nested groups kept), lower-cased, without surrounding
// slashes or a ".git" suffix. It accepts that form and https, ssh and
// scp-like git remotes, whose host it drops: Fugaro names repositories by
// path alone, one provider host per deployment.
func CanonicalRepo(ref string) string {
	s := strings.TrimSpace(ref)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" && u.Opaque == "" {
		s = u.Path
	} else if m := scpLikeRE.FindStringSubmatch(s); m != nil {
		s = m[2]
	}
	s = strings.Trim(strings.ToLower(s), "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.Trim(s, "/")
}

// maxSlugReadable bounds a slug's readable part; the slug is at most 9
// characters longer.
const maxSlugReadable = 48

var slugUnsafeRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is a repository's storage prefix, the IAM boundary between
// repositories in the bucket (runs/<slug>/, cache/<slug>/, locks/<slug>/):
// its canonical path made readable ("acme/app" → "acme-app"), truncated,
// then "-" and 8 hex of sha256(CanonicalRepo(repo)). The readable part
// merges distinct repositories ("acme/app-web" and "acme-app/web"); the
// hash keeps them apart, and is the same for every spelling of one
// repository (case, ".git", https or SSH remote).
func Slug(repo string) string {
	c := CanonicalRepo(repo)
	sum := sha256.Sum256([]byte(c))
	readable := strings.Trim(slugUnsafeRE.ReplaceAllString(c, "-"), "-")
	if len(readable) > maxSlugReadable {
		readable = strings.TrimRight(readable[:maxSlugReadable], "-")
	}
	if readable == "" {
		readable = "repo"
	}
	return readable + "-" + hex.EncodeToString(sum[:4])
}

// Parse decodes and validates a task spec, rejecting unknown fields.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("task spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// IsFollowUp reports whether the task continues an existing Fugaro PR.
func (s *Spec) IsFollowUp() bool { return s.Branch != "" }

// Validate checks the spec's own rules; it does not look at the repository.
func (s *Spec) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Version != 1 {
		bad("task spec: version must be 1")
	}
	if !runIDRE.MatchString(s.RunID) {
		bad("task spec: run_id %q must look like 20260926-221530-a1b2", s.RunID)
	}
	if !repoRE.MatchString(s.Repo) {
		bad("task spec: repo %q must look like owner/name", s.Repo)
	}
	if strings.TrimSpace(s.Ref) == "" {
		bad("task spec: ref is required")
	}
	anyFollowUp := s.Branch != "" || s.PR != 0 || s.PreviousRun != ""
	allFollowUp := s.Branch != "" && s.PR > 0 && s.PreviousRun != ""
	if anyFollowUp && !allFollowUp {
		bad("task spec: branch, pr and previous_run must be set together")
	}
	if !anyFollowUp && strings.TrimSpace(s.Task) == "" {
		bad("task spec: task is required")
	}
	if s.PreviousRun != "" && !runIDRE.MatchString(s.PreviousRun) {
		bad("task spec: previous_run %q is not a run ID", s.PreviousRun)
	}
	if s.Batch != "" && !BatchRE.MatchString(s.Batch) {
		bad("task spec: batch %q must be 1-63 lower-case letters, digits, '.', '_' or '-'", s.Batch)
	}
	o := s.Overrides
	if o.ReviewRounds != nil && (*o.ReviewRounds < 1 || *o.ReviewRounds > 10) {
		bad("task spec: overrides.review_rounds must be between 1 and 10")
	}
	// Zero is not "unlimited" here: it would silently drop --max-budget-usd
	// for a task that asked to change the cap.
	if o.MaxBudgetUSD != nil && *o.MaxBudgetUSD <= 0 {
		bad("task spec: overrides.max_budget_usd must be greater than 0")
	}
	if o.TotalTimeout != "" {
		if d, err := time.ParseDuration(o.TotalTimeout); err != nil || d <= 0 {
			bad("task spec: overrides.total_timeout %q must be a positive duration such as 45m", o.TotalTimeout)
		}
	}
	return errors.Join(errs...)
}

// Marshal encodes the spec as indented JSON.
func (s *Spec) Marshal() ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// Apply writes the overrides into the repository config and selected workflow.
func (s *Spec) Apply(c *config.Config, w *config.Workflow) error {
	o := s.Overrides
	if o.ReviewRounds != nil {
		c.Agent.ReviewRounds = *o.ReviewRounds
	}
	if o.MaxBudgetUSD != nil {
		c.Agent.MaxBudgetUSD = *o.MaxBudgetUSD
	}
	if o.Model != "" {
		c.Agent.Model = o.Model
	}
	if o.TotalTimeout != "" {
		d, err := time.ParseDuration(o.TotalTimeout)
		if err != nil {
			return fmt.Errorf("overrides.total_timeout: %w", err)
		}
		if d <= w.Timeouts.FinalizeReserve.Duration {
			return fmt.Errorf("overrides.total_timeout %s must be longer than the workflow's timeouts.finalize_reserve %s", d, w.Timeouts.FinalizeReserve.Duration)
		}
		w.Timeouts.Total.Duration = d
		w.Timeouts.Stage.Duration = min(w.Timeouts.Stage.Duration, d)
	}
	return nil
}
