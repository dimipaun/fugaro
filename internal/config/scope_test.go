package config

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestScopeOf(t *testing.T) {
	for path, want := range map[string]Scope{
		"workflows.web.commands.test":                 InProfile | InRepo,
		"workflows.web.commands.rerun_failed.command": InProfile | InRepo,
		"workflows.web.secrets":                       InRepo,
		"git.provider":                                InProject | InRepo,
		"agent.model":                                 InProject | InRepo | InOverride,
	} {
		row, ok := ScopeOf(path)
		if !ok || row.In != want {
			t.Errorf("ScopeOf(%s) = %v %v, want %v", path, row.In, ok, want)
		}
	}
	if _, ok := ScopeOf("agent.colour"); ok {
		t.Error("agent.colour has a scope")
	}
	if s := (InProject | InRepo).String(); s != "project, repo" {
		t.Errorf("String = %q", s)
	}
}

// TestScopeTableMatchesDocs pins, row by row, the layer set of every key in
// config.Scopes against the table of docs/design/layered-config.md §5 (the
// plan's Task 1). A key whose scope changes, or a key added without a row
// here, fails this test.
func TestScopeTableMatchesDocs(t *testing.T) {
	want := map[string]Scope{
		"version":     InRepo,
		"project":     InRepo,
		"gcp_project": InRepo,
		"profile":     InRepo,

		"git.provider":       InProject | InRepo,
		"git.base_branch":    InRepo,
		"git.pr.labels":      InProject | InRepo,
		"git.pr.reviewers":   InRepo,
		"git.pr.early_draft": InProject | InRepo,
		"git.pr.checkpoints": InProject | InRepo,

		"agent.auth":                       InProject | InRepo,
		"agent.model":                      InProject | InRepo | InOverride,
		"agent.models.coder":               InProject | InRepo,
		"agent.models.reviewer":            InProject | InRepo,
		"agent.models.background":          InProject | InRepo,
		"agent.review_rounds":              InProject | InRepo | InOverride,
		"agent.max_budget_usd":             InProject | InRepo | InOverride,
		"agent.instructions":               InRepo,
		"agent.review":                     InRepo,
		"agent.max_output_tokens.coder":    InRepo,
		"agent.max_output_tokens.reviewer": InRepo,
		"agent.max_run_tokens":             InRepo,
		"agent.first_line_review":          InProject | InRepo,
		"agent.first_line_rounds":          InProject | InRepo,
		"agent.recipe":                     InProject | InRepo | InOverride,

		"budget.mode":           InRepo,
		"budget.per_run_usd":    InRepo,
		"budget.allowed_models": InRepo,
		"budget.per_day_usd":    InRepo,

		"workflows.*.profile":                   InRepo,
		"workflows.*.base":                      InProfile | InRepo,
		"workflows.*.image.node":                InProfile | InRepo,
		"workflows.*.image.jdk":                 InRepo,
		"workflows.*.image.apt":                 InProfile | InRepo,
		"workflows.*.image.setup":               InProfile | InRepo,
		"workflows.*.image.skip_build_scripts":  InProfile | InRepo,
		"workflows.*.image.tools":               InProfile | InRepo,
		"workflows.*.dockerfile":                InRepo,
		"workflows.*.commands.build":            InProfile | InRepo,
		"workflows.*.commands.test":             InProfile | InRepo,
		"workflows.*.commands.rerun_failed":     InProfile | InRepo,
		"workflows.*.commands.reports":          InProfile | InRepo,
		"workflows.*.cache":                     InProfile | InRepo,
		"workflows.*.secrets":                   InRepo,
		"workflows.*.resources.cpu":             InProfile | InRepo,
		"workflows.*.resources.memory":          InProfile | InRepo,
		"workflows.*.timeouts.total":            InProfile | InRepo | InOverride,
		"workflows.*.timeouts.stage":            InProfile | InRepo,
		"workflows.*.timeouts.verify":           InProfile | InRepo,
		"workflows.*.timeouts.finalize_reserve": InProfile | InRepo,
		"workflows.*.rebuild.check":             InProfile | InRepo,
		"workflows.*.rebuild.max_age":           InProfile | InRepo,
		"workflows.*.rebuild.lockfiles":         InProfile | InRepo,
		"workflows.*.rebuild.base":              InProfile | InRepo,
		"workflows.*.rebuild.paths":             InProfile | InRepo,

		"followup.trusted":      InRepo,
		"followup.allow_public": InRepo,
	}

	seen := make(map[string]bool, len(Scopes))
	for _, row := range Scopes {
		seen[row.Key] = true
		wantIn, ok := want[row.Key]
		if !ok {
			t.Errorf("config.Scopes has key %q with no row in this test's golden table; add one from docs/design/layered-config.md §5", row.Key)
			continue
		}
		if row.In != wantIn {
			t.Errorf("Scopes[%q].In = %v, want %v (docs/design/layered-config.md §5)", row.Key, row.In, wantIn)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("golden table has key %q, which is not in config.Scopes", key)
		}
	}
}

// TestExecutableKeys pins decision L7 option A: the keys a profile may set
// that run as shell, in the job or in the image build.
func TestExecutableKeys(t *testing.T) {
	want := []string{
		"workflows.*.commands.build",
		"workflows.*.commands.test",
		"workflows.*.commands.rerun_failed",
		"workflows.*.image.apt",
		"workflows.*.image.setup",
		"workflows.*.image.tools",
	}
	got := append([]string(nil), ExecutableKeys...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ExecutableKeys = %v, want %v", ExecutableKeys, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("ExecutableKeys = %v, want %v", ExecutableKeys, want)
			break
		}
	}
}

// TestEveryImageFieldIsClassified walks the Image struct by reflection: every
// field's workflows.*.image.<yaml tag> path must be in ExecutableKeys or in
// NonExecutableImageKeys with a non-empty reason. A field added to Image
// without updating either list fails this test, so a future executable
// field (the next image.tools) cannot silently skip the
// --executable-changes gate (PR #244).
func TestEveryImageFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(Image{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("Image.%s has no yaml tag to classify", f.Name)
		}
		key := "workflows.*.image." + tag
		executable := slices.Contains(ExecutableKeys, key)
		reason, nonExecutable := NonExecutableImageKeys[key]
		switch {
		case executable && nonExecutable:
			t.Errorf("%s is in both ExecutableKeys and NonExecutableImageKeys", key)
		case !executable && !nonExecutable:
			t.Errorf("Image.%s (%s) is in neither ExecutableKeys nor NonExecutableImageKeys: classify it", f.Name, key)
		case nonExecutable && strings.TrimSpace(reason) == "":
			t.Errorf("%s has no reason in NonExecutableImageKeys", key)
		}
	}
}
