package sensitivity

import "slices"

// Floor is the engine's own list: what Fugaro calls security-sensitive before
// any project and any repository says anything. Every later layer is measured
// against it and may only add to it (D1505, D1506).
//
// It is a positive list because the question it answers is not the classifier's
// question. Classify is an allowlist of routine trees, so its Sensitive side is
// everything else — right for the model, and the wrong level-3 trigger:
// "adversarial review for every change that is not documentation" is not a
// rule, it is the cost the ask exists to bound. The floor therefore names the
// trees, files and file classes a change has to touch, one row each: a name, one
// of the seven categories and a doublestar glob in rigor.Area's shape (D1500,
// §6).
//
// Forty of the 43 rows cover a path a mechanism on this tree already names: the
// routing rule's fixture (32 paths across 15 categories), the classifier's
// eleven sensitiveExceptions — the eight `artifacts` rows plus pending-workflows,
// release-streams and published-security-policy — the three path-scoped
// `.claude/rules/` scopes (SECURITY.md, website/content/security.md for
// credentials-service; deploy/terraform/** and internal/infra/** for terraform;
// ee/** for ee) and the review brief's areas. Three are this design's own, and
// the owner accepts or drops each: images/** (the pinned base image every run's
// container is built from, named by no mechanism today), internal/policy/** (the
// merge that decides what a run may do) and internal/gateway/** — a widening,
// where the credentials rule's own scope is the narrower
// internal/gateway/gitforward*.go and the review brief says "the gateway" with
// no path at all.
//
// TestFloorIsAtLeastWhatTheRoutingRuleNames is what holds the table above the
// two machine-readable statements of the rule, in both halves: every path of the
// routing fixture and every rule of the classifier's exception table — the
// latter in the case-folded spelling the classifier's own covers matches it in,
// which is what Match folds for (see its doc). The second half is the one a
// fixture-only test misses — the published checksums, the install commands and
// the plan-graph artifacts live under website/ and docs/ and are in no fixture,
// so a floor that lost them would still pass the first.
func Floor() []Pattern { return slices.Clone(floor) }

// floor is the table Floor answers with, cloned on the way out: a caller merges
// it with two more lists and keeps the result, and a shared backing array would
// let a later write reach the floor itself.
var floor = []Pattern{
	{Name: "credentials", Match: "internal/creds/**", Category: Credentials},
	{Name: "credentials-service", Match: "internal/credsvc/**", Category: Credentials},
	{Name: "git-proxy", Match: "internal/gitproxy/**", Category: Credentials},
	{Name: "model-gateway", Match: "internal/gateway/**", Category: Credentials},
	{Name: "secrets-client", Match: "internal/rtdb/**", Category: Credentials},
	{Name: "security-policy", Match: "SECURITY.md", Category: Credentials},
	{Name: "published-security-policy", Match: "website/content/security.md", Category: Credentials},
	{Name: "mobile", Match: "mobile/**", Category: Credentials},
	{Name: "subscription-boundary", Match: "ee/**", Category: Credentials},

	{Name: "settings-writes", Match: "internal/cli/settings_*.go", Category: Guards},
	{Name: "managed-gate", Match: "internal/managed/**", Category: Guards},
	{Name: "managed-cli", Match: "internal/cli/managed_*.go", Category: Guards},
	{Name: "policy-merge", Match: "internal/policy/**", Category: Guards},
	{Name: "init-flow", Match: "internal/initflow/**", Category: Guards},
	{Name: "classifier", Match: "internal/orchestrator/**", Category: Guards},
	{Name: "console-main", Match: "console/src/main/**", Category: Guards},

	{Name: "terraform", Match: "deploy/terraform/**", Category: Targets},
	{Name: "infra-runner", Match: "internal/infra/**", Category: Targets},

	{Name: "images", Match: "images/**", Category: Infrastructure},
	{Name: "agent-environment", Match: "internal/agentenv/**", Category: Infrastructure},
	{Name: "agent-environment-runner", Match: "internal/runner/agentenv.go", Category: Infrastructure},

	{Name: "agent-prompts", Match: "internal/agent/**", Category: Prompts},
	{Name: "followup-prompts", Match: "internal/followup/**", Category: Prompts},
	{Name: "shared-prompts", Match: "shared/prompts/**", Category: Prompts},
	{Name: "recipes", Match: "internal/recipe/**", Category: Prompts},
	{Name: "repo-config", Match: "fugaro.yaml", Category: Prompts},
	{Name: "claude-md", Match: "CLAUDE.md", Category: Prompts},
	{Name: "agents-md", Match: "AGENTS.md", Category: Prompts},
	{Name: "claude-dir", Match: ".claude/**", Category: Prompts},
	{Name: "plugin-skills", Match: "plugin/skills/**", Category: Prompts},
	{Name: "review-brief", Match: ".fugaro/**", Category: Prompts},

	{Name: "ci-workflows", Match: ".github/**", Category: CI},
	{Name: "pending-workflows", Match: "docs/pending-workflows/**", Category: CI},
	{Name: "release-streams", Match: "docs/release-streams.yaml", Category: CI},
	{Name: "release-script", Match: "scripts/release.sh", Category: CI},

	{Name: "plan-graphs", Match: "docs/**/*.graph.json", Category: Artifacts},
	{Name: "redesign-screenshot-script", Match: "docs/website/redesign/capture-screenshots.mjs", Category: Artifacts},
	{Name: "redesign-logo-script", Match: "docs/website/redesign/sample-logo-hue.py", Category: Artifacts},
	{Name: "website-manifest", Match: "website/package.json", Category: Artifacts},
	{Name: "website-lock", Match: "website/package-lock.json", Category: Artifacts},
	{Name: "published-downloads", Match: "website/content/downloads.json", Category: Artifacts},
	{Name: "published-checksums", Match: "website/data/console-sha256sums.txt", Category: Artifacts},
	{Name: "install-commands", Match: "website/data/install.yaml", Category: Artifacts},
}
