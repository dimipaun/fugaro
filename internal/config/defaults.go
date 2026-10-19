package config

import "time"

// BaseKind is the one Fugaro base image's kind (design base-image.md): what a
// workflow without base: builds on, and its key in the local config's
// base_images.
const BaseKind = "base"

// MigrationDoc is the guide every refusal of a removed base setting names
// (design/base-image.md §10).
const MigrationDoc = "docs/base-image-migration.md"

// Bases are the base kinds: the one Fugaro base (the legacy kinds were
// removed in 0.7.0).
var Bases = []string{BaseKind}

// legacyBaseMessage is the "base: removed in 0.7.0" refusal, shared by
// validate.go's workflows.*.base check and layer.go's profiles.*.base
// check: TestBadBaseMessageConsistency pins that both give byte-for-byte
// the same message, which a shared constant, not two independent literals,
// is what actually guarantees. %s is MigrationDoc. It does not repeat
// "base:" at the front: both callers add() this at a path already ending
// in ".base" (such as "workflows.web.base"), and Problem.String() joins
// path and message with ": ", so a leading "base:" here used to print as
// "workflows.web.base: base: was removed in 0.7.0...", the word doubled.
const legacyBaseMessage = "was removed in 0.7.0: every workflow builds on the one Fugaro base image. Delete this line and declare the workflow's runtimes in mise.toml (%s)"

// The round knobs' defaults, named: `fugaro budget preview` counts a
// recipe's stages at these when it has no checkout of the repository to read
// the knobs from, so the two must not drift apart.
const (
	DefaultFirstLineRounds = 1
	DefaultReviewRounds    = 2
	// DefaultAgentAuth is agent.auth's default: a fugaro.yaml that names none
	// authenticates with a Vertex key. applyDefaults applies it, and
	// AgentOf is the other reader of the rule.
	DefaultAgentAuth = "vertex"
)

// applyDefaults fills in every field fugaro.yaml may omit. The per-base cache
// default depends on the checkout's lockfile, so it is not applied here:
// DefaultCache computes it, and M4's cache restore applies it.
func applyDefaults(c *Config) {
	if c.Git.BaseBranch == "" {
		c.Git.BaseBranch = "main"
	}
	if c.Git.PR.EarlyDraft == nil {
		t := true
		c.Git.PR.EarlyDraft = &t
	}
	if c.Agent.Auth == "" {
		c.Agent.Auth = DefaultAgentAuth
	}
	if c.Agent.ReviewRounds == 0 {
		c.Agent.ReviewRounds = DefaultReviewRounds
	}
	if c.Agent.FirstLineReview == "" {
		c.Agent.FirstLineReview = FirstLineAuto
	}
	if c.Agent.FirstLineRounds == 0 {
		c.Agent.FirstLineRounds = DefaultFirstLineRounds
	}
	if c.Agent.MaxBudgetUSD == 0 {
		c.Agent.MaxBudgetUSD = 25
	}
	for name, w := range c.Workflows {
		if len(w.Commands.Reports) == 0 {
			w.Commands.Reports = []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}
		}
		if w.Resources.CPU == 0 {
			w.Resources.CPU = 4
		}
		if w.Resources.Memory == "" {
			w.Resources.Memory = "8Gi"
		}
		if w.Checkout == "" {
			w.Checkout = CheckoutBaked
		}
		w.Rebuild = w.Rebuild.Defaults()
		t := &w.Timeouts
		if t.Total.Duration == 0 {
			t.Total.Duration = 90 * time.Minute
		}
		// Unset timeouts scale down with a short total so a small total alone is valid.
		if t.FinalizeReserve.Duration == 0 {
			t.FinalizeReserve.Duration = min(5*time.Minute, t.Total.Duration/5)
		}
		if t.Stage.Duration == 0 {
			t.Stage.Duration = min(40*time.Minute, t.Total.Duration)
		}
		if t.Verify.Duration == 0 {
			t.Verify.Duration = min(30*time.Minute, t.Total.Duration)
		}
		// The render step is one command with its own timeout, so it gets
		// what is left of the run once the finalize reserve is set aside,
		// and never more than verify's own share (design artifact-runs.md
		// §3). The guard keeps a tiny total from defaulting it to zero,
		// which Validate would then refuse as "must be positive".
		if t.Render.Duration == 0 {
			if d := t.Total.Duration - t.FinalizeReserve.Duration; d > 0 && d < t.Verify.Duration {
				t.Render.Duration = d
			} else {
				t.Render.Duration = t.Verify.Duration
			}
		}
		c.Workflows[name] = w
	}
}
