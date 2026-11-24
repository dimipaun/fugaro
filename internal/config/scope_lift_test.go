package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// keptByD3237 is every fugaro.yaml key D3237 kept in the repository — the
// audit's items B1, B2, B4, B5, B6, B7, B8, B10, B12, B13, B14, B15 and B17
// (docs/repo-coupling-audit/report.md §3) — and liftedByD3237 every key it
// moved to the project (B3, B9, B11), with L13's two model grades beside them
// (D3299, module `dead-keys`).
//
// Two keys of the report's rows are deliberately absent from both lists.
// B4's `agent.levels.lN.model` is lifted rather than kept, because D3291 puts
// it under B3; and B18's and B19's rows are database nodes, not fugaro.yaml
// keys, so they have no scope row to cite anything.
var (
	keptByD3237 = []string{
		"git.base_branch",
		"git.author", "git.pr.labels", "git.pr.reviewers", "git.pr.early_draft", "git.pr.checkpoints",
		"agent.review_rounds", "agent.max_budget_usd",
		"agent.first_line_review", "agent.first_line_rounds",
		"agent.levels.l1.effort", "agent.levels.l1.none", "agent.levels.l1.focus",
		"agent.levels.l2.effort", "agent.levels.l2.none", "agent.levels.l2.focus",
		"agent.levels.l3.effort", "agent.levels.l3.none", "agent.levels.l3.focus",
		"agent.adversarial_rounds", "agent.review_stop_fraction", "agent.workspace_beat", "agent.recipe",
		"agent.max_output_tokens.coder", "agent.max_output_tokens.reviewer", "agent.max_run_tokens",
		"agent.triage", "agent.review_against_task",
		"budget.mode", "budget.per_run_usd", "budget.allowed_models", "budget.per_day_usd",
		"governance.review_ratio", "governance.review_floor_calls", "governance.max_subagents",
		"governance.stage_budget", "governance.stage_tokens", "governance.stage_timeout", "governance.tiers",
		"followup.trusted", "followup.allow_public",
		"planning.decoupling",
		"security.patterns",
		"workflows.*.job_profile", "workflows.*.needs", "workflows.*.ab.reviewer_arm",
		"workflows.*.commands.build", "workflows.*.commands.test", "workflows.*.commands.lint",
		"workflows.*.commands.fix", "workflows.*.commands.render", "workflows.*.commands.probe",
		"workflows.*.commands.rerun_failed", "workflows.*.commands.reports",
		"workflows.*.cache", "workflows.*.checkout",
		"workflows.*.inputs.max_files", "workflows.*.inputs.max_bytes", "workflows.*.inputs.max_total_bytes",
		"agent.instructions", "agent.review",
	}
	liftedByD3237 = []string{
		"agent.model",
		"agent.models.coder", "agent.models.reviewer", "agent.models.background",
		"agent.models.vision", "agent.models.diagnose",
		"agent.crafts.coder.model", "agent.crafts.review.model", "agent.crafts.senior-review.model",
		"agent.crafts.security-review.model", "agent.crafts.merge-judge.model",
		"agent.levels.l1.model", "agent.levels.l2.model", "agent.levels.l3.model",
		"ab.rate",
		"rigor.default", "rigor.areas",
		"model_grades.strong.model", "model_grades.strongest.model",
	}
)

// TestKeptRowsCiteD3237 is S0 of docs/repo-override-rulings/implementation-plan.md:
// every row D3237 kept says so in its Why, docs/project-layer.md prints the
// same beside the row, and no lifted row is marked Retired.
//
// The last is what the spec first asked for and R1 (amended after review) took
// back. On this tree "retired" means named and still honoured (D3063,
// RetiredKeyNotes), and TestEveryRetiredKeyIsNamedWhereItIsWritten holds
// RetiredKeys to model-credentials-routing's list exactly, so marking a lifted
// row Retired would both break that test and tell a person a key had moved
// before `fugaro update` can move it. Release B takes InRepo off the lifted
// rows instead (XB1).
//
// Mutation (drop the citation from one kept row, add it to a row D3237 never
// ruled on, or mark a lifted row Retired): this test fails.
func TestKeptRowsCiteD3237(t *testing.T) {
	kept := make(map[string]bool, len(keptByD3237))
	for _, key := range keptByD3237 {
		kept[key] = true
		row, ok := scopeRow(key)
		if !ok {
			t.Errorf("%s is a key D3237 kept and has no Scopes row", key)
			continue
		}
		if !strings.Contains(row.Why, "D3237") {
			t.Errorf("%s is a row D3237 kept, and its Why cites no decision: %q", key, row.Why)
		}
	}

	// The other direction, so a sentence shared with a row D3237 never reached
	// cannot carry the citation onto it.
	for _, r := range Scopes {
		if strings.Contains(r.Why, "D3237") && !kept[r.Key] {
			t.Errorf("%s cites D3237 and is not a row D3237 kept", r.Key)
		}
	}

	for _, key := range liftedByD3237 {
		row, ok := scopeRow(key)
		if !ok {
			t.Errorf("%s is a key D3237 lifted and has no Scopes row", key)
			continue
		}
		if row.Retired != 0 {
			t.Errorf("%s is lifted by D3237 and marked Retired %v: a lifted key stays off the retired list until release B takes InRepo off it (R1)", key, row.Retired)
		}
	}

	// docs/project-layer.md prints the table, so the citation is beside the row
	// there too. TestScopeTableMatchesDocs keeps the two tables' keys and
	// layers equal; it does not read the Why column, which is the doc's own
	// prose.
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-layer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, "<!-- scope-table:start -->"), strings.Index(doc, "<!-- scope-table:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/project-layer.md has no scope-table markers")
	}
	rowRE := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([a-z, ]+) \\| (.*) \\|$")
	docWhy := make(map[string]string)
	for _, m := range rowRE.FindAllStringSubmatch(doc[start:end], -1) {
		docWhy[m[1]] = m[3]
	}
	for _, key := range keptByD3237 {
		why, ok := docWhy[key]
		if !ok {
			t.Errorf("docs/project-layer.md's scope table has no row for %s", key)
			continue
		}
		if !strings.Contains(why, "D3237") {
			t.Errorf("docs/project-layer.md's row for %s cites no decision: %q", key, why)
		}
	}
}
