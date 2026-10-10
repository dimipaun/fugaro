package config

import "time"

// BaseKind is the one Fugaro base image's kind (design base-image.md): what a
// workflow without base: builds on, and its key in the local config's
// base_images.
const BaseKind = "base"

// MigrationDoc is the guide every refusal of a removed base setting names.
const MigrationDoc = "docs/base-image-migration.md"

// Bases are the base kinds: BaseKind and, until the 0.7.0 cut, the legacy
// kinds a workflow's base: may still name.
var Bases = []string{BaseKind, "go", "java-services", "web-node"}

type baseDefault struct {
	reports []string
	cpu     int
	memory  string
}

var baseDefaults = map[string]baseDefault{
	// java-services runs the repository's services (Postgres, Redis, the
	// Firebase emulators) next to Gradle and the test JVM in one container,
	// and the services' data lives in memory on Cloud Run: 16Gi, the most a
	// 4 vCPU container may have.
	"java-services": {reports: []string{"**/build/test-results/**/*.xml"}, cpu: 4, memory: "16Gi"},
	"go":            {reports: []string{"**/junit*.xml"}, cpu: 4, memory: "8Gi"},
	"web-node":      {reports: []string{"**/junit*.xml"}, cpu: 4, memory: "8Gi"},
	BaseKind:        {reports: []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}, cpu: 4, memory: "8Gi"},
}

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
		if d, ok := baseDefaults[w.BaseKind()]; ok {
			if len(w.Commands.Reports) == 0 {
				w.Commands.Reports = d.reports
			}
			if w.Resources.CPU == 0 {
				w.Resources.CPU = d.cpu
			}
			if w.Resources.Memory == "" {
				w.Resources.Memory = d.memory
			}
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
