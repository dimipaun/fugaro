package config

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestDefaultConstantsAreThePromisedValues pins the named defaults themselves.
// applyDefaults, the local loader and the settings registry's DEFAULT column
// all read these constants, so the wiring between them is checked where the
// wiring is (settings.TestRegistryDefaultsMatchApplyDefaults); what is checked
// here is the value: a change to one of these is a change to what every
// installation does on the next run, and it is cheaper to notice it in a table
// than in a job.
func TestDefaultConstantsAreThePromisedValues(t *testing.T) {
	values := []struct {
		key  string
		got  string
		want string
	}{
		{"git.base_branch", DefaultBaseBranch, "main"},
		{"agent.first_line_review", FirstLineAuto, "auto"},
		{"agent.auth", DefaultAgentAuth, "vertex"},
		{"agent.max_budget_usd", strconv.Itoa(DefaultMaxBudgetUSD), "25"},
		{"workflows.*.resources.cpu", strconv.Itoa(DefaultCPU), "4"},
		{"workflows.*.resources.memory", DefaultMemory, "8Gi"},
		{"workflows.*.checkout", CheckoutBaked, "baked"},
		{"workflows.*.commands.reports", strings.Join(DefaultReports(), ", "), "**/junit*.xml, **/build/test-results/**/*.xml"},
	}
	for _, v := range values {
		if v.got != v.want {
			t.Errorf("%s: the default is %q, and every installation was promised %q", v.key, v.got, v.want)
		}
	}
	if !DefaultUpdateCheck {
		t.Error("update_check: the default is off, and every installation was promised the daily check")
	}
}

// TestDefaultReportsAreDistinct keeps the list from silently losing a glob to
// a duplicate, which is how a run stops collecting one of its reports.
func TestDefaultReportsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range DefaultReports() {
		if seen[g] {
			t.Errorf("%q: listed twice", g)
		}
		seen[g] = true
	}
	if slices.Contains(DefaultReports(), "") {
		t.Error("an empty glob collects the whole checkout")
	}
}
