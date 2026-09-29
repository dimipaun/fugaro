package config

import "time"

type baseDefault struct {
	reports []string
	cpu     int
	memory  string
}

var baseDefaults = map[string]baseDefault{
	"server-jvm": {reports: []string{"**/build/test-results/**/*.xml"}, cpu: 8, memory: "32Gi"},
	"web-node":   {reports: []string{"**/junit*.xml"}, cpu: 4, memory: "8Gi"},
}

// applyDefaults fills in every field fugaro.yaml may omit. The per-base cache
// default depends on the checkout's lockfile, so it is not applied here:
// DefaultCache computes it, and M4's cache restore applies it.
func applyDefaults(c *Config) {
	if c.Git.BaseBranch == "" {
		c.Git.BaseBranch = "main"
	}
	if c.Agent.Auth == "" {
		c.Agent.Auth = "vertex"
	}
	if c.Agent.ReviewRounds == 0 {
		c.Agent.ReviewRounds = 2
	}
	if c.Agent.MaxBudgetUSD == 0 {
		c.Agent.MaxBudgetUSD = 25
	}
	for name, w := range c.Workflows {
		if d, ok := baseDefaults[w.Base]; ok {
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
