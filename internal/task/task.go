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
	// Recipe is the task loop the launching CLI resolved. Nil on a first run
	// leaves the choice to the runner (agent.recipe at the ref, else default);
	// nil on a follow-up is default.
	Recipe *Recipe `json:"recipe,omitempty"`
	// ReviewPRs is set only for a mode: review recipe (design generic-tool
	// §3.6, G12): the pull requests to review, 1 to 4, in launch order.
	// Mutually exclusive with Branch/PR/PreviousRun (review mode never
	// continues a pull request of its own); the runner refuses a mismatch
	// against the resolved recipe's mode either way (defense in depth: a
	// CLI older than this one, or a hand-built task, could set either
	// alone).
	ReviewPRs []int `json:"review_prs,omitempty"`
	// ProjectLayer is the project layer the launching CLI read
	// (docs/design/layered-config.md §8): its exact text, which the runner
	// resolves fugaro.yaml against. Nil: none. A runner before 0.6.0
	// refuses an unknown field, so fugaro run's own launch-time image gate
	// (checkImageSince, layered-config plan Task 11) refuses the launch
	// before this field is ever set, rather than let a stale runner reject
	// it after the fact. Nothing in this package enforces that; a launch
	// this field reaches has already passed that gate.
	ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
	// OAuthToken is the named Claude account (config.ClaudeAccountNameRE,
	// at most config.MaxClaudeAccountNameLen characters, never
	// config.DefaultClaudeAccount itself — "" means that) this run's
	// agent.auth: oauth workflow used (docs/design/named-oauth-tokens.md).
	// A runner before namedTokensSince refuses the field, since it is new
	// and this package's decoder disallows unknown fields; the launching
	// CLI checks the job's build record first, the same way it already
	// does for ProjectLayer.
	OAuthToken string `json:"oauth_token,omitempty"`
	// Mode is the recipe's mode, written by the launcher from the recipe it
	// resolved ("artifact" today; implement and review write nothing, so
	// every existing spec is byte-identical to what the previous release
	// wrote). The runner cannot read the recipe before it reads the
	// configuration, so the spec is what selects the shape at bootstrap —
	// exactly as ReviewPRs does for mode: review — and the resolved recipe
	// is cross-checked against it afterwards, both ways.
	//
	// An EMPTY Mode is a real case and not a default to the implement
	// path: the launcher leaves the recipe to the runner when it cannot
	// resolve one (runnerChooses, internal/cli/run.go), so the spec carries
	// no mode at all and the runner reads agent.recipe at the ref. The
	// runner's rule is "this Mode when set, the resolved recipe's mode when
	// not" — never "empty means implement". A runner before 0.8.0 refuses
	// the field outright (Parse disallows unknown fields), so the
	// launcher's own image gate refuses an artifact launch before this
	// field is ever set at such a job.
	Mode string `json:"mode,omitempty"`
	// Inputs are the files the launching CLI attached (fugaro run
	// --attach; design artifact-runs.md §4): what it uploaded, under
	// runs/<slug>/<run>/inputs/<name>, with the size and the hash it read
	// on the way up. Nil for every launch that attached nothing, so a spec
	// this release writes for a launch without --attach is byte-identical
	// to what the previous release wrote.
	//
	// It is only meaningful to a mode: artifact run: the runner refuses a
	// task that carries inputs under a resolved recipe of another mode
	// (design §8.2), and the launcher refuses --attach unless the launch
	// resolves to an artifact recipe. A runner before this field refuses
	// the spec outright (Parse disallows unknown fields), which is why the
	// launcher's image gate refuses an --attach launch at such a job before
	// this field is ever set (plan P9).
	Inputs []InputFile `json:"inputs,omitempty"`
}

// InputFile is one attached file, as the launcher recorded it: the name it
// is stored under (runs/<slug>/<run>/inputs/<name>, and <checkout>/inputs/
// <name> in the run), its size, and the sha256 the launcher read while
// uploading it. Name, Bytes and SHA256 are the launcher's own claims about
// bytes it has already written; the runner verifies them against the object
// it downloads and enforces its own caps and its own name refusals on them,
// because a task spec is text a launcher wrote (design §8.2, §8.4).
type InputFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// inputSHA256RE is the form of an InputFile's SHA256: the same lower-case
// hex sha256 every other hash in a run's record uses.
var inputSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MaxInputNameBytes bounds an attached file's name, and it is deliberately
// well under a filesystem's NAME_MAX (255): the runner downloads into
// <checkout>/inputs/<name> through blobx.GetFile, which writes a temporary
// beside the destination whose name is "." + the base name + ".part-" plus
// random digits, so a name at the limit would fail with ENAMETOOLONG after
// the bytes were already read. A name is a name: this is a refusal, not a
// truncation.
const MaxInputNameBytes = 200

// InputNameBlank reports whether an attached file's name is no name at all:
// empty, or nothing but whitespace. It is the ONE predicate for that rule,
// shared by InputNameProblem below and by Spec.Validate's required-name check,
// because a name one of them accepts has to be a name the other accepts.
//
// They disagreed once, and the disagreement was not academic: "   " is a legal
// file name and a legal JSON string, so the launcher (which asked only for
// non-empty) uploaded it and wrote the task.json naming it, and the runner's
// decoder (which asked for TrimSpace(name) != "") refused that task at
// bootstrap — bytes in the bucket under a run that never starts, the one
// outcome design §4 step 1's ordering exists to prevent.
func InputNameBlank(name string) bool { return strings.TrimSpace(name) == "" }

// InputNameProblem reports what is wrong with an attached file's name as a
// filesystem path — "" when nothing is — for the two places one becomes a
// path: the launcher, choosing it (the base name of the path --attach named),
// and the runner, writing <checkout>/inputs/<name> from a task spec it did
// not write (design artifact-runs.md §8.2). It is one function rather than
// two copies of a list because the two refusals must be the same list.
//
// The rules are the design's own list for an input name: not empty, no
// absolute path, no ".." element (and no "." or empty element either, so a
// name that is not already its own cleaned form is refused rather than
// cleaned into something plausible), no NUL or newline, and the length limit
// above. A name beginning with a dot is deliberately NOT refused here, unlike
// an artifact name on the way back out: an input is written under inputs/,
// which is never a checkout root, so `.git/hooks/pre-commit` lands inside
// inputs/ and cannot reach the repository's own .git — and inputs/ is
// excluded from git anyway (design §4 step 5).
func InputNameProblem(name string) string {
	switch {
	case name == "":
		return "is empty"
	case InputNameBlank(name):
		// Nothing but whitespace: the name Spec.Validate refuses as missing,
		// so it is refused here too — before the file is uploaded, rather
		// than by the runner reading the task back (InputNameBlank).
		return "is only whitespace"
	case len(name) > MaxInputNameBytes:
		return fmt.Sprintf("is longer than %d bytes", MaxInputNameBytes)
	case strings.ContainsAny(name, "\x00\n\r"):
		return "holds a NUL or a newline"
	case strings.HasPrefix(name, "/"):
		return "is an absolute path"
	}
	for _, elem := range strings.Split(name, "/") {
		switch elem {
		case "":
			return "has an empty path element"
		case ".":
			return `has a "." path element`
		case "..":
			return `has a ".." path element`
		}
	}
	return ""
}

// Recipe is the recipe the launching CLI resolved (docs/design/recipes.md §5).
type Recipe struct {
	Name   string `json:"name"`
	Source string `json:"source"`           // "repo" | "project" | "catalog"
	SHA256 string `json:"sha256,omitempty"` // of YAML; empty for repo
	YAML   string `json:"yaml,omitempty"`   // the exact text; empty for repo
}

// ProjectLayer is a project layer as the launching CLI read it.
type ProjectLayer struct {
	SHA256     string `json:"sha256"`               // of YAML
	Generation int64  `json:"generation,omitempty"` // the object's, for display
	YAML       string `json:"yaml"`                 // the exact text
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
	anyReview := s.ReviewPRs != nil
	switch {
	case anyReview && anyFollowUp:
		bad("task spec: review_prs and branch/pr/previous_run don't go together: review mode never continues a pull request")
	case anyReview && (len(s.ReviewPRs) == 0 || len(s.ReviewPRs) > 4):
		bad("task spec: review_prs must name 1 to 4 pull requests")
	case anyReview:
		seen := map[int]bool{}
		for _, n := range s.ReviewPRs {
			if n <= 0 {
				bad("task spec: review_prs must be positive pull request numbers")
			} else if seen[n] {
				bad("task spec: review_prs repeats #%d", n)
			}
			seen[n] = true
		}
	}
	if !anyFollowUp && !anyReview && strings.TrimSpace(s.Task) == "" {
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
	// config.ClaudeAccountNameRE and config.MaxClaudeAccountNameLen are the
	// single source of truth for a named account's shape (internal/config/
	// claude_accounts.go, Task 1): this package imports them rather than
	// keeping its own copy, so the two can never silently drift apart the
	// way an earlier version of this check did.
	switch {
	case s.OAuthToken == "":
	case s.OAuthToken == config.DefaultClaudeAccount:
		bad(`task spec: oauth_token must not be "default": the empty value already means the repository's default account`)
	case len(s.OAuthToken) > config.MaxClaudeAccountNameLen:
		bad("task spec: oauth_token %q must be at most %d characters", s.OAuthToken, config.MaxClaudeAccountNameLen)
	case !config.ClaudeAccountNameRE.MatchString(s.OAuthToken):
		bad("task spec: oauth_token %q is not a Claude account name", s.OAuthToken)
	}
	// Mode is the recipe's mode, so the only values a spec may carry are
	// the recipe modes; "" (the launcher could not resolve a recipe, or the
	// recipe is implement or review) is legal and means "the runner reads
	// it from the resolved recipe".
	switch s.Mode {
	case "", recipe.ModeImplement, recipe.ModeReview, recipe.ModeArtifact:
	default:
		bad("task spec: mode %q must be %s, %s, %s or empty", s.Mode, recipe.ModeImplement, recipe.ModeReview, recipe.ModeArtifact)
	}
	// The entries' own shapes only. What an input NAME may be — no absolute
	// path, no ".." element, no NUL or newline, a length limit, no duplicate
	// — is deliberately NOT checked here: the runner applies that list where
	// the name becomes a filesystem path (design §8.2), and the launcher
	// applies it where the name is chosen. Refusing those shapes here would
	// make the runner's own check unreachable — this decoder runs first —
	// and the list belongs beside the write it protects.
	for i, in := range s.Inputs {
		// The same predicate the launcher's InputNameProblem applies to a
		// name, so that a name this refuses is a name the launcher refuses
		// before it uploads anything (InputNameBlank).
		if InputNameBlank(in.Name) {
			bad("task spec: inputs[%d].name is required", i)
		}
		if in.Bytes < 0 {
			bad("task spec: inputs[%d].bytes must not be negative", i)
		}
		if !inputSHA256RE.MatchString(in.SHA256) {
			bad("task spec: inputs[%d].sha256 %q must be 64 lower-case hex digits, the sha256 of the file", i, in.SHA256)
		}
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
	if pl := s.ProjectLayer; pl != nil {
		switch {
		case pl.YAML == "" || len(pl.YAML) > config.LayerMaxBytes:
			bad("task spec: project_layer.yaml must hold the project layer, at most %d bytes", config.LayerMaxBytes)
		case pl.SHA256 != config.LayerSum([]byte(pl.YAML)):
			bad("task spec: project_layer.sha256 does not match project_layer.yaml")
		}
		if pl.Generation < 0 {
			bad("task spec: project_layer.generation must not be negative")
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
