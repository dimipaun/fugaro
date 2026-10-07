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
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

var (
	runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)
)

// BatchRE is the form of a task's batch label.
var BatchRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// BranchRE is the form of a Fugaro run's branch, fugaro/<run-id>: the only
// branch a follow-up may continue.
var BranchRE = regexp.MustCompile(`^fugaro/[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// ValidRunID reports whether id has a run ID's form, as Validate requires
// of run_id: check it before id names any object.
func ValidRunID(id string) bool { return runIDRE.MatchString(id) }

// BranchRunID returns the run ID a Fugaro branch names (the run that
// created it), and false for any other branch.
func BranchRunID(branch string) (string, bool) {
	if !BranchRE.MatchString(branch) {
		return "", false
	}
	return strings.TrimPrefix(branch, "fugaro/"), true
}

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
	// Recipe is the task loop the launching CLI resolved; nil is the
	// default recipe from the catalog.
	Recipe *Recipe `json:"recipe,omitempty"`
}

// Recipe is the recipe the launching CLI resolved (docs/design/recipes.md §5).
type Recipe struct {
	Name   string `json:"name"`
	Source string `json:"source"`           // "repo" | "project" | "catalog"
	SHA256 string `json:"sha256,omitempty"` // of YAML; empty for repo
	YAML   string `json:"yaml,omitempty"`   // the exact text; empty for repo
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

// CanonicalRepo is the canonical form of a repository path "owner/name"
// (nested groups allowed): ASCII lower-cased, without a trailing ".git".
// Anything that isn't such a path, before or after the reduction (a URL,
// "acme/.git", a "." or ".." segment), is an error.
func CanonicalRepo(repo string) (string, error) {
	c := strings.TrimSuffix(asciiLower(repo), ".git")
	if !repoRE.MatchString(repo) || !repoRE.MatchString(c) {
		return "", errors.New("the repository must look like owner/name") // never quoted: it may be a value pasted in the wrong place
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == "." || seg == ".." {
			return "", errors.New("the repository must look like owner/name") // never quoted: it may be a value pasted in the wrong place
		}
	}
	return c, nil
}

// asciiLower lower-cases A–Z only, so no Unicode case folding can merge
// look-alike names (repoRE admits ASCII only anyway).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

const (
	// slugHashHex is the width of a slug's hash suffix, and
	// maxSlugReadable bounds its readable part, so a slug is at most 63
	// characters and fits a label value.
	slugHashHex     = 16
	maxSlugReadable = 63 - 1 - slugHashHex
)

var (
	slugUnsafeRE = regexp.MustCompile(`[^a-z0-9]+`)
	providerRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// Slug is a repository's storage prefix, the IAM boundary between
// repositories in the bucket (runs/<slug>/, cache/<slug>/, locks/<slug>/).
// It is the canonical path made readable ("acme/app" → "acme-app"),
// truncated, then "-" and 16 hex of sha256(provider NUL canonical path).
// provider is the git provider kind ("github", "bitbucket", or "fake" in
// tests): git.provider is per repository, so one deployment can hold a
// GitHub acme/app and a Bitbucket acme/app, and they must not share a slug.
// The readable part merges distinct repositories ("acme/app-web" and
// "acme-app/web"); the hash keeps them apart, and is the same for every
// spelling of one repository (case, ".git").
//
// A 64-bit hash stops accidental collisions, not a searched-for one; the
// control against a chosen collision is the ownership check before an
// existing job, service account or secret is reused.
func Slug(provider, repo string) (string, error) {
	if !providerRE.MatchString(provider) {
		return "", fmt.Errorf("git provider %q is not a provider kind", provider)
	}
	c, err := CanonicalRepo(repo)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + c))
	readable := strings.Trim(slugUnsafeRE.ReplaceAllString(c, "-"), "-")
	if len(readable) > maxSlugReadable {
		readable = strings.TrimRight(readable[:maxSlugReadable], "-")
	}
	if readable == "" {
		readable = "repo"
	}
	return readable + "-" + hex.EncodeToString(sum[:])[:slugHashHex], nil
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
	if s.Branch != "" && !BranchRE.MatchString(s.Branch) {
		bad("task spec: branch %q must be fugaro/<run-id>", s.Branch)
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
	if rc := s.Recipe; rc != nil {
		switch {
		case !recipe.NameRE.MatchString(rc.Name):
			bad("task spec: recipe.name %q is not a recipe name", rc.Name)
		case rc.Source == string(recipe.SourceRepo):
			if rc.YAML != "" || rc.SHA256 != "" {
				bad("task spec: a repo recipe is read from the checkout; recipe.yaml and recipe.sha256 must be empty")
			}
		case rc.Source == string(recipe.SourceProject) || rc.Source == string(recipe.SourceCatalog):
			if rc.YAML == "" || len(rc.YAML) > recipe.MaxBytes {
				bad("task spec: recipe.yaml must hold the recipe, at most %d bytes", recipe.MaxBytes)
			} else if rc.SHA256 != recipe.Sum([]byte(rc.YAML)) {
				bad("task spec: recipe.sha256 does not match recipe.yaml")
			}
		default:
			bad("task spec: recipe.source must be repo, project or catalog")
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
		if c.Agent.Models.Coder == "" && c.Agent.Models.Reviewer == "" {
			// No per-role models: the override keeps the reach it always
			// had, every stage.
			c.Agent.Model = o.Model
		} else {
			c.Agent.Models.Coder = o.Model
		}
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
