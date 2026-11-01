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

// TestScopesIncludesCompression pins agent.compression's scope (design
// headroom-compression.md §4, decision P11's fourth edit): the project layer
// and a profile are the same launcher-writable bucket object agent.oauth_token
// already distrusts, so neither may set it — and the row must exist at all, or
// the project layer would treat the key as unknown instead of out of scope.
func TestScopesIncludesCompression(t *testing.T) {
	i := slices.IndexFunc(Scopes, func(r ScopeRow) bool { return r.Key == "agent.compression" })
	if i < 0 {
		t.Fatal("agent.compression has no Scopes row")
	}
	if Scopes[i].In != InRepo|InOverride {
		t.Errorf("agent.compression scope = %v, want InRepo|InOverride", Scopes[i].In)
	}
}

// TestProjectLayerRefusesCompression is the consequence TestScopesIncludesCompression
// only implies: a project layer that sets defaults.agent.compression is refused
// for being out of scope, not for naming an unknown key.
func TestProjectLayerRefusesCompression(t *testing.T) {
	const doc = `
defaults:
  agent:
    compression: headroom
`
	_, ps := ParseProjectLayer([]byte(doc), LayerAnchor{Project: "example", GCPProject: "example-1a2b3c"})
	if len(ps) == 0 {
		t.Fatal("the project layer accepted defaults.agent.compression")
	}
	if !slices.ContainsFunc(ps, func(p Problem) bool { return strings.Contains(p.Message, "may only be set in") }) {
		t.Errorf("the project layer refused compression for the wrong reason: %+v", ps)
	}
}

// TestGitAuthorIsRepoOnly pins git.author's row (A2a, D604/D606) and its
// consequence: the key is the repository's own opt-in, so a project layer
// that writes defaults.git.author is refused for being out of scope and not
// for naming a key the project layer does not know — the distinction the row
// itself makes, since an unlisted key falls through to the wrong sentence.
func TestGitAuthorIsRepoOnly(t *testing.T) {
	i := slices.IndexFunc(Scopes, func(r ScopeRow) bool { return r.Key == "git.author" })
	if i < 0 {
		t.Fatal("git.author has no Scopes row: a project layer that writes it is refused as an unknown key")
	}
	if Scopes[i].In != InRepo {
		t.Errorf("git.author scope = %v, want InRepo alone", Scopes[i].In)
	}
	_, ps := ParseProjectLayer([]byte("defaults:\n  git:\n    author: launcher\n"), LayerAnchor{Project: "example", GCPProject: "example-1a2b3c"})
	if !slices.ContainsFunc(ps, func(p Problem) bool { return strings.Contains(p.Message, "may only be set in") }) {
		t.Errorf("the project layer refused defaults.git.author for the wrong reason: %+v", ps)
	}
}

// TestReviewAllowForksScopeRemoved (owner ruling 2026-10-12): the key no
// longer exists at all (review mode is trusted-author-only; there is no
// opt-in for untrusted ones to remove a scope row for), so it must not be
// in Scopes under any layer.
func TestReviewAllowForksScopeRemoved(t *testing.T) {
	if _, ok := ScopeOf("review.allow_forks"); ok {
		t.Fatal("review.allow_forks is still a known scope; it should not exist at all")
	}
}

// TestScopesMatchDesignDoc pins, row by row, the layer set of every key in
// config.Scopes against the table of docs/design/layered-config.md §5 (the
// plan's Task 1). A key whose scope changes, or a key added without a row
// here, fails this test. (Renamed from TestScopeTableMatchesDocs, Task 18:
// that name now belongs to docs_scope_test.go's test against
// docs/project-layer.md, the user-facing doc this design table feeds.)
func TestScopesMatchDesignDoc(t *testing.T) {
	want := map[string]Scope{
		"version":     InRepo,
		"project":     InRepo,
		"gcp_project": InRepo,
		"profile":     InRepo,

		"git.provider":       InProject | InRepo,
		"git.base_branch":    InRepo,
		"git.author":         InRepo,
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
		// The per-level review block (L1.3, D722): four keys per level, each
		// its own row because either layer may set any one of them.
		"agent.levels.l1.model":    InProject | InRepo,
		"agent.levels.l1.effort":   InProject | InRepo,
		"agent.levels.l1.none":     InProject | InRepo,
		"agent.levels.l1.focus":    InProject | InRepo,
		"agent.levels.l2.model":    InProject | InRepo,
		"agent.levels.l2.effort":   InProject | InRepo,
		"agent.levels.l2.none":     InProject | InRepo,
		"agent.levels.l2.focus":    InProject | InRepo,
		"agent.levels.l3.model":    InProject | InRepo,
		"agent.levels.l3.effort":   InProject | InRepo,
		"agent.levels.l3.none":     InProject | InRepo,
		"agent.levels.l3.focus":    InProject | InRepo,
		"agent.adversarial_rounds": InProject | InRepo,
		"agent.recipe":             InProject | InRepo | InOverride,
		// Named oauth tokens plan Task 6 (docs/design/named-oauth-tokens.md §3
		// T2): added after agent.recipe, in config.Scopes already; the design
		// doc's own §5 table (which this golden map otherwise mirrors) gains
		// its row in Task 9, once the shipped shape is documented.
		"agent.oauth_token": InRepo | InOverride,
		// Headroom compression plan Task 1 (docs/design/headroom-compression.md
		// §4): repo-only for the same reason agent.oauth_token is, and the
		// override is one-directional (--no-compression only), which the scope
		// table does not express.
		"agent.compression": InRepo | InOverride,

		"budget.mode":           InRepo,
		"budget.per_run_usd":    InRepo,
		"budget.allowed_models": InRepo,
		"budget.per_day_usd":    InRepo,

		"governance.review_ratio":       InProject | InRepo,
		"governance.review_floor_calls": InProject | InRepo,
		"governance.max_subagents":      InProject | InRepo,
		"governance.stage_budget":       InProject | InRepo,
		"governance.stage_tokens":       InProject | InRepo,
		"governance.stage_timeout":      InProject | InRepo,
		"governance.tiers":              InProject | InRepo,

		"workflows.*.profile":                   InRepo,
		"workflows.*.job_profile":               InRepo,
		"workflows.*.base":                      InProfile | InRepo,
		"workflows.*.image.node":                InProfile | InRepo,
		"workflows.*.image.jdk":                 InRepo,
		"workflows.*.image.apt":                 InProfile | InRepo,
		"workflows.*.image.setup":               InProfile | InRepo,
		"workflows.*.image.skip_build_scripts":  InProfile | InRepo,
		"workflows.*.checkout":                  InProfile | InRepo,
		"workflows.*.image.tools":               InProfile | InRepo,
		"workflows.*.image.key_files":           InRepo,
		"workflows.*.dockerfile":                InRepo,
		"workflows.*.commands.build":            InProfile | InRepo,
		"workflows.*.commands.test":             InProfile | InRepo,
		"workflows.*.commands.lint":             InProfile | InRepo,
		"workflows.*.commands.fix":              InProfile | InRepo,
		"workflows.*.commands.render":           InProfile | InRepo,
		"workflows.*.commands.probe":            InProfile | InRepo,
		"workflows.*.commands.rerun_failed":     InProfile | InRepo,
		"workflows.*.commands.reports":          InProfile | InRepo,
		"workflows.*.cache":                     InProfile | InRepo,
		"workflows.*.secrets":                   InRepo,
		"workflows.*.resources.cpu":             InProfile | InRepo,
		"workflows.*.resources.memory":          InProfile | InRepo,
		"workflows.*.timeouts.total":            InProfile | InRepo | InOverride,
		"workflows.*.timeouts.stage":            InProfile | InRepo,
		"workflows.*.timeouts.verify":           InProfile | InRepo,
		"workflows.*.timeouts.render":           InProfile | InRepo,
		"workflows.*.timeouts.finalize_reserve": InProfile | InRepo,
		"workflows.*.rebuild.check":             InProfile | InRepo,
		"workflows.*.rebuild.max_age":           InProfile | InRepo,
		"workflows.*.rebuild.lockfiles":         InProfile | InRepo,
		"workflows.*.rebuild.base":              InProfile | InRepo,
		"workflows.*.rebuild.paths":             InProfile | InRepo,

		"workflows.*.inputs.max_files":       InProfile | InRepo,
		"workflows.*.inputs.max_bytes":       InProfile | InRepo,
		"workflows.*.inputs.max_total_bytes": InProfile | InRepo,

		"followup.trusted":      InRepo,
		"followup.allow_public": InRepo,

		"rigor.default": InProject | InRepo,
		"rigor.areas":   InProject | InRepo,
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
		"workflows.*.commands.lint",
		"workflows.*.commands.fix",
		"workflows.*.commands.render",
		"workflows.*.commands.probe",
		"workflows.*.commands.rerun_failed",
		"workflows.*.image.apt",
		"workflows.*.image.setup",
		"workflows.*.image.skip_build_scripts",
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

// commandsNotExecutable lists Commands struct fields that never run as
// shell, with why. TestEveryCommandFieldIsExecutableOrExplained forces the
// next field someone adds to Commands into either ExecutableKeys or here,
// so it can't silently skip the config publish --executable-changes gate
// (L7 option A).
var commandsNotExecutable = map[string]string{
	"Reports": "a list of result-file paths the agent reads, not code",
}

// TestEveryCommandFieldIsExecutableOrExplained walks every field of
// Commands by reflection: each must be in ExecutableKeys
// ("workflows.*.commands.<field>") or in commandsNotExecutable, never both,
// never neither.
func TestEveryCommandFieldIsExecutableOrExplained(t *testing.T) {
	typ := reflect.TypeOf(Commands{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" {
			t.Fatalf("Commands.%s has no yaml tag", f.Name)
		}
		key := "workflows.*.commands." + name
		_, explained := commandsNotExecutable[f.Name]
		inExec := slices.Contains(ExecutableKeys, key)
		switch {
		case inExec && explained:
			t.Errorf("Commands.%s (key %s) is in both ExecutableKeys and commandsNotExecutable", f.Name, key)
		case !inExec && !explained:
			t.Errorf("Commands.%s (key %s) is in neither ExecutableKeys nor commandsNotExecutable: classify it as one or the other", f.Name, key)
		}
	}
}
