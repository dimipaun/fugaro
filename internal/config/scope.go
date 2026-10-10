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
	whyAnchor    = "it identifies the repository and anchors the project layer"
	whyBranch    = "a project-wide base branch would let a bucket writer point image builds at an unreviewed branch"
	whyPeople    = "reviewers are people of one repository"
	whyFiles     = "it names a file in the repository"
	whyPolicy    = "it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it"
	whySecrets   = "secrets belong to one repository's Secret Manager entries"
	whyFollowup  = "it decides whose comments steer a run with the repository's credentials"
	whyRepoShape = "it is the repository's own"
	whyForks     = "review mode would run a fork's code next to the job's secrets; only the repository opts in"
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
	{Key: "agent.recipe", In: InProject | InRepo | InOverride},
	{Key: "budget.mode", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_run_usd", In: InRepo, Why: whyPolicy},
	{Key: "budget.allowed_models", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_day_usd", In: InRepo, Why: whyPolicy},
	{Key: "workflows.*.profile", In: InRepo, Why: "it chooses a profile"},
	{Key: "workflows.*.base", In: InProfile | InRepo},
	{Key: "workflows.*.image.node", In: InProfile | InRepo},
	{Key: "workflows.*.image.jdk", In: InRepo, Why: "it is refused on every base"},
	{Key: "workflows.*.image.apt", In: InProfile | InRepo},
	{Key: "workflows.*.image.setup", In: InProfile | InRepo},
	{Key: "workflows.*.image.skip_build_scripts", In: InProfile | InRepo},
	{Key: "workflows.*.image.tools", In: InProfile | InRepo},
	{Key: "workflows.*.dockerfile", In: InRepo, Why: whyFiles},
	{Key: "workflows.*.commands.build", In: InProfile | InRepo},
	{Key: "workflows.*.commands.test", In: InProfile | InRepo},
	{Key: "workflows.*.commands.lint", In: InProfile | InRepo},
	{Key: "workflows.*.commands.fix", In: InProfile | InRepo},
	{Key: "workflows.*.commands.rerun_failed", In: InProfile | InRepo},
	{Key: "workflows.*.commands.reports", In: InProfile | InRepo},
	{Key: "workflows.*.cache", In: InProfile | InRepo},
	{Key: "workflows.*.secrets", In: InRepo, Why: whySecrets},
	{Key: "workflows.*.resources.cpu", In: InProfile | InRepo},
	{Key: "workflows.*.resources.memory", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.total", In: InProfile | InRepo | InOverride},
	{Key: "workflows.*.timeouts.stage", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.verify", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.finalize_reserve", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.check", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.max_age", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.lockfiles", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.base", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.paths", In: InProfile | InRepo},
	{Key: "followup.trusted", In: InRepo, Why: whyFollowup},
	{Key: "followup.allow_public", In: InRepo, Why: whyFollowup},
	{Key: "review.allow_forks", In: InRepo, Why: whyForks},
}

// ExecutableKeys are the profile keys whose values run as shell, in the
// job or in the image build (decision L7): a publish that changes one needs
// --executable-changes.
//
// workflows.*.image.tools is here because it is exactly as powerful as
// image.setup: every mise backend with a URL or host form (cargo:, go:,
// npm:, asdf:/vfox:, ubi:/github:/aqua:) passes ValidMiseTool's charset, and
// a build installs it with the workflow's build secrets mounted, so a mise
// "tool" can name an arbitrary git ref or release to fetch and run
// (install scripts, build.rs, a plugin). That is within the threat model
// for a repository's own reviewed fugaro.yaml, the same as image.setup; a
// profile published from the bucket must gate it the same way.
var ExecutableKeys = []string{
	"workflows.*.commands.build", "workflows.*.commands.test", "workflows.*.commands.lint", "workflows.*.commands.fix",
	"workflows.*.commands.rerun_failed", "workflows.*.image.apt", "workflows.*.image.setup", "workflows.*.image.tools",
}

// NonExecutableImageKeys are the Image struct's fields ExecutableKeys
// deliberately leaves out, each with why its value never runs as shell.
// TestEveryImageFieldIsClassified walks Image by reflection and fails if a
// future field is in neither this map nor ExecutableKeys, so a field that
// slips past config publish's --executable-changes gate (PR #244) is a
// compile-time-visible test failure, not a silent gap.
var NonExecutableImageKeys = map[string]string{
	"workflows.*.image.node":               "a pinned Node major or exact version (nodeVersionRE); never a command",
	"workflows.*.image.jdk":                "refused on every base (validateImage); never reaches a Dockerfile",
	"workflows.*.image.skip_build_scripts": "a boolean flag, not a command",
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
