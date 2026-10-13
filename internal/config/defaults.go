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
		c.Agent.Auth = "vertex"
	}
	if c.Agent.ReviewRounds == 0 {
		c.Agent.ReviewRounds = 2
	}
	if c.Agent.FirstLineReview == "" {
		c.Agent.FirstLineReview = FirstLineAuto
	}
	if c.Agent.FirstLineRounds == 0 {
		c.Agent.FirstLineRounds = 1
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
		c.Workflows[name] = w
	}
}
