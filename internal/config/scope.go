package config

import "strings"

// Scope is the set of layers a fugaro.yaml key may be set in
// (docs/design/layered-config.md §5). The Fugaro default is not a scope:
// applyDefaults fills what no layer set.
type Scope uint8

// The layers a key may be set in, narrowest last.
const (
	InProject  Scope = 1 << iota // the project layer's defaults:
	InProfile                    // a profile of the project layer
	InRepo                       // the repository's fugaro.yaml
	InOverride                   // a task flag (--model, --review-rounds, ...)
)

// ScopeRow is one key and where it may be set. Key is a fugaro.yaml path;
// "*" stands for a workflow name. Why is said when a layer sets the key
// outside its scope.
type ScopeRow struct {
	Key string
	In  Scope
	Why string
}

const (
	whyAnchor     = "it identifies the repository and anchors the project layer"
	whyBranch     = "a project-wide base branch in the launcher-writable project layer would let a bucket writer point image builds at an unreviewed branch; the project's default base branch is the owner's, published to the policy node instead"
	whyPeople     = "reviewers are people of one repository"
	whyFiles      = "it names a file in the repository"
	whyPolicy     = "it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it"
	whySecrets    = "secrets belong to one repository's Secret Manager entries"
	whyFollowup   = "it decides whose comments steer a run with the repository's credentials"
	whyRepoShape  = "it is the repository's own"
	whyRemoved    = "it was removed in 0.7.0 (" + MigrationDoc + ")"
	whyGovernance = "it is a guardrail Fugaro ships: the project layer sets it for every repository of the project" +
		" (a top-level key of the layer file, like claude_models) and a repository can only tighten it," +
		" because the per-run cap stays the ceiling and a stage budget is a fraction under it (G6)"
)

// Scopes is every fugaro.yaml key and the layers it may be set in. A key
// not listed under a block is unknown. docs/project-layer.md prints this
// table; TestScopeTableMatchesDocs keeps the two equal.
var Scopes = []ScopeRow{
	{Key: "version", In: InRepo, Why: whyRepoShape},
	{Key: "project", In: InRepo, Why: whyAnchor},
	{Key: "gcp_project", In: InRepo, Why: whyAnchor},
	{Key: "profile", In: InRepo, Why: "it chooses a profile; the project chooses its default with default_profile"},
	{Key: "git.provider", In: InProject | InRepo},
	{Key: "git.base_branch", In: InRepo, Why: whyBranch},
	{Key: "git.author", In: InRepo, Why: whyRepoShape},
	{Key: "git.pr.labels", In: InProject | InRepo},
	{Key: "git.pr.reviewers", In: InRepo, Why: whyPeople},
	{Key: "git.pr.early_draft", In: InProject | InRepo},
	{Key: "git.pr.checkpoints", In: InProject | InRepo},
	{Key: "agent.auth", In: InProject | InRepo},
	{Key: "agent.model", In: InProject | InRepo | InOverride},
	{Key: "agent.models.coder", In: InProject | InRepo},
	{Key: "agent.models.reviewer", In: InProject | InRepo},
	{Key: "agent.models.background", In: InProject | InRepo},
	{Key: "agent.review_rounds", In: InProject | InRepo | InOverride},
	{Key: "agent.max_budget_usd", In: InProject | InRepo | InOverride},
	{Key: "agent.instructions", In: InRepo, Why: whyFiles},
	{Key: "agent.review", In: InRepo, Why: whyFiles},
	{Key: "agent.max_output_tokens.coder", In: InRepo, Why: whyPolicy},
	{Key: "agent.max_output_tokens.reviewer", In: InRepo, Why: whyPolicy},
	{Key: "agent.max_run_tokens", In: InRepo, Why: whyPolicy},
	{Key: "agent.first_line_review", In: InProject | InRepo},
	{Key: "agent.first_line_rounds", In: InProject | InRepo},
	// The per-level review block (L1.3, D722): each level's four keys are its
	// own row because a level's settings are read one by one, and either
	// layer may set any of them — the merge is per key, so a project's l1
	// stays in effect beside a repository's l2.
	{Key: "agent.levels.l1.model", In: InProject | InRepo},
	{Key: "agent.levels.l1.effort", In: InProject | InRepo},
	{Key: "agent.levels.l1.none", In: InProject | InRepo},
	{Key: "agent.levels.l1.focus", In: InProject | InRepo},
	{Key: "agent.levels.l2.model", In: InProject | InRepo},
	{Key: "agent.levels.l2.effort", In: InProject | InRepo},
	{Key: "agent.levels.l2.none", In: InProject | InRepo},
	{Key: "agent.levels.l2.focus", In: InProject | InRepo},
	{Key: "agent.levels.l3.model", In: InProject | InRepo},
	{Key: "agent.levels.l3.effort", In: InProject | InRepo},
	{Key: "agent.levels.l3.none", In: InProject | InRepo},
	{Key: "agent.levels.l3.focus", In: InProject | InRepo},
	{Key: "agent.adversarial_rounds", In: InProject | InRepo},
	{Key: "agent.recipe", In: InProject | InRepo | InOverride},
	{Key: "agent.oauth_token", In: InRepo | InOverride, Why: "it names a credential identity that only the repository's own, PR-reviewed fugaro.yaml may choose among names the local config (never the project layer) allow-lists"},
	{Key: "agent.compression", In: InRepo | InOverride, Why: "it changes what the model sees on every call; the project layer and a profile are the same launcher-writable bucket object agent.oauth_token already distrusts for the identical reason"},
	{Key: "budget.mode", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_run_usd", In: InRepo, Why: whyPolicy},
	{Key: "budget.allowed_models", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_day_usd", In: InRepo, Why: whyPolicy},
	{Key: "governance.review_ratio", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.review_floor_calls", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.max_subagents", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.stage_budget", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.stage_tokens", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.stage_timeout", In: InProject | InRepo, Why: whyGovernance},
	{Key: "governance.tiers", In: InProject | InRepo, Why: whyGovernance},
	{Key: "workflows.*.profile", In: InRepo, Why: "it chooses a profile"},
	{Key: "workflows.*.job_profile", In: InRepo, Why: "it chooses a job profile of the project policy; the policy is the project's, so a repository picks among the profiles it defines and never writes them"},
	{Key: "workflows.*.base", In: InProfile | InRepo, Why: whyRemoved},
	{Key: "workflows.*.checkout", In: InProfile | InRepo},
	{Key: "workflows.*.image.node", In: InProfile | InRepo, Why: whyRemoved},
	{Key: "workflows.*.image.jdk", In: InRepo, Why: whyRemoved},
	{Key: "workflows.*.image.apt", In: InProfile | InRepo},
	{Key: "workflows.*.image.setup", In: InProfile | InRepo},
	{Key: "workflows.*.image.skip_build_scripts", In: InProfile | InRepo},
	{Key: "workflows.*.image.tools", In: InProfile | InRepo},
	{Key: "workflows.*.image.key_files", In: InRepo, Why: whyFiles},
	{Key: "workflows.*.dockerfile", In: InRepo, Why: whyFiles},
	{Key: "workflows.*.commands.build", In: InProfile | InRepo},
	{Key: "workflows.*.commands.test", In: InProfile | InRepo},
	{Key: "workflows.*.commands.lint", In: InProfile | InRepo},
	{Key: "workflows.*.commands.fix", In: InProfile | InRepo},
	{Key: "workflows.*.commands.render", In: InProfile | InRepo},
	{Key: "workflows.*.commands.probe", In: InProfile | InRepo},
	{Key: "workflows.*.commands.rerun_failed", In: InProfile | InRepo},
	{Key: "workflows.*.commands.reports", In: InProfile | InRepo},
	{Key: "workflows.*.cache", In: InProfile | InRepo},
	{Key: "workflows.*.secrets", In: InRepo, Why: whySecrets},
	{Key: "workflows.*.resources.cpu", In: InProfile | InRepo},
	{Key: "workflows.*.resources.memory", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.total", In: InProfile | InRepo | InOverride},
	{Key: "workflows.*.timeouts.stage", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.verify", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.render", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.finalize_reserve", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.check", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.max_age", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.lockfiles", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.base", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.paths", In: InProfile | InRepo},
	{Key: "workflows.*.inputs.max_files", In: InProfile | InRepo},
	{Key: "workflows.*.inputs.max_bytes", In: InProfile | InRepo},
	{Key: "workflows.*.inputs.max_total_bytes", In: InProfile | InRepo},
	{Key: "followup.trusted", In: InRepo, Why: whyFollowup},
	{Key: "followup.allow_public", In: InRepo, Why: whyFollowup},
	// Both rigor keys are settable in both layers (D20, D20c): the project
	// sets defaults.rigor.default as the mode every repository of the project
	// runs at, and either layer may name area rules. A repository cannot
	// loosen the project's area rules by writing its own: the two lists are a
	// union, not a replacement (D20c).
	{Key: "rigor.default", In: InProject | InRepo},
	{Key: "rigor.areas", In: InProject | InRepo},
}

// ExecutableKeys are the profile keys whose values run as shell, or control
// whether shell a profile already describes runs, in the job or in the
// image build (decision L7): a publish that changes one needs
// --executable-changes. image.skip_build_scripts going from true to false
// (or being dropped) turns the dependency install/build scripts back on in
// the image build; going from false to true is reported too (the owner's
// ruling: a direction being safer is not a reason to leave it ungated).
//
// workflows.*.image.tools is here too because it is exactly as powerful as
// image.setup: every mise backend with a URL or host form (cargo:, go:,
// npm:, asdf:/vfox:, ubi:/github:/aqua:) passes ValidMiseTool's charset, and
// a build installs it with the workflow's build secrets mounted, so a mise
// "tool" can name an arbitrary git ref or release to fetch and run
// (install scripts, build.rs, a plugin). That is within the threat model
// for a repository's own reviewed fugaro.yaml, the same as image.setup; a
// profile published from the bucket must gate it the same way.
//
// commands.render and commands.probe are here for the same reason
// commands.build is: an artifact recipe's render and probe steps run the
// workflow's own text as shell in the job (design artifact-runs.md §3), so
// a profile that sets either must be gated like any other command.
//
// workflows.*.image.skip_build_scripts going from true to false (or being
// dropped) turns the dependency install/build scripts back on in the image
// build; going from false to true is reported too (the owner's ruling: a
// direction being safer is not a reason to leave it ungated).
var ExecutableKeys = []string{
	"workflows.*.commands.build", "workflows.*.commands.test", "workflows.*.commands.lint", "workflows.*.commands.fix",
	"workflows.*.commands.render", "workflows.*.commands.probe",
	"workflows.*.commands.rerun_failed", "workflows.*.image.apt", "workflows.*.image.setup", "workflows.*.image.tools",
	"workflows.*.image.skip_build_scripts",
}

// NonExecutableImageKeys are the Image struct's fields ExecutableKeys
// deliberately leaves out, each with why its value never runs as shell.
// TestEveryImageFieldIsClassified walks Image by reflection and fails if a
// future field is in neither this map nor ExecutableKeys, so a field that
// slips past config publish's --executable-changes gate (PR #244) is a
// compile-time-visible test failure, not a silent gap.
var NonExecutableImageKeys = map[string]string{
	"workflows.*.image.node": "removed in 0.7.0, refused unconditionally by validateImage (" + MigrationDoc + "); never a command",
	"workflows.*.image.jdk":  "removed in 0.7.0, refused unconditionally by validateImage (" + MigrationDoc + "); never reaches a Dockerfile",
	// key_files holds repository paths, not shell: each one is written into
	// the image's checkout loop as a single-quoted word, and validateImage
	// refuses a value that is not a clean relative path, so a path can only
	// name a file in the repository being built.
	"workflows.*.image.key_files": "names repository paths checked out before the source layer, never run as shell",
}

// ScopeOf is the row of path, a fugaro.yaml path with a real workflow name
// ("workflows.web.commands.test") or "*". A path inside a listed key (an
// entry of cache:) is the listed key's. false: no such key.
func ScopeOf(path string) (ScopeRow, bool) {
	parts := strings.Split(starWorkflow(path), ".")
	for n := len(parts); n > 0; n-- {
		if r, ok := scopeRow(strings.Join(parts[:n], ".")); ok {
			return r, true
		}
	}
	return ScopeRow{}, false
}

// starWorkflow is path with its workflow name, if any, replaced by "*".
func starWorkflow(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) >= 2 && parts[0] == "workflows" {
		parts[1] = "*"
	}
	return strings.Join(parts, ".")
}

// scopeRow is the row whose key is exactly key.
func scopeRow(key string) (ScopeRow, bool) {
	for _, r := range Scopes {
		if r.Key == key {
			return r, true
		}
	}
	return ScopeRow{}, false
}

// isBlock reports whether path ("agent", "workflows.*.image") is a block
// that holds listed keys rather than a key itself.
func isBlock(path string) bool {
	for _, r := range Scopes {
		if strings.HasPrefix(r.Key, path+".") {
			return true
		}
	}
	return false
}

// String names the layers of s, as the docs table and the errors do.
func (s Scope) String() string {
	var out []string
	for _, l := range []struct {
		bit  Scope
		name string
	}{{InProject, "project"}, {InProfile, "profile"}, {InRepo, "repo"}, {InOverride, "override"}} {
		if s&l.bit != 0 {
			out = append(out, l.name)
		}
	}
	return strings.Join(out, ", ")
}
