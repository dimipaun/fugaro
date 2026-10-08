package config

import (
	"strings"
	"testing"
)

const testLayer = `version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  git:
    provider: github
    pr:
      labels: [fugaro]
      early_draft: false
  agent:
    review_rounds: 3
    recipe: claude-solo
profiles:
  java-service:
    description: Gradle service
    base: java-services
    image:
      apt: [graphviz]
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
    resources: { cpu: 4, memory: 16Gi }
    timeouts: { total: 2h }
  node-web:
    base: web-node
    image: { node: "20" }
    commands: { build: npm run build, test: npm test }
default_profile: java-service
`

var testAnchor = LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"}

func mustLayer(t *testing.T, text string) *ProjectLayer {
	t.Helper()
	l, ps := ParseProjectLayer([]byte(text), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

func TestParseProjectLayer(t *testing.T) {
	l := mustLayer(t, testLayer)
	if l.SHA256 != LayerSum([]byte(testLayer)) || l.DefaultProfile != "java-service" || l.Profiles["node-web"].Image.Node != "20" {
		t.Fatalf("layer = %+v", l)
	}
	if !l.Profiles["java-service"].HasExecutable() {
		t.Fatal("java-service sets commands")
	}
}

func TestParseProjectLayerRefuses(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, tc := range []struct{ name, text, want string }{
		{"secrets are repo-only", head + "profiles:\n  p:\n    secrets: [{name: db, env: DB}]\n", "profiles.p.secrets: workflows.*.secrets may only be set in: repo"},
		{"followup is repo-only", head + "defaults:\n  followup:\n    trusted: ['1']\n", "defaults.followup.trusted: followup.trusted may only be set in: repo"},
		{"reviewers are repo-only", head + "defaults:\n  git:\n    pr:\n      reviewers: [someone]\n", "git.pr.reviewers may only be set in: repo"},
		{"instructions are repo-only", head + "defaults:\n  agent:\n    instructions: AGENTS.md\n", "agent.instructions may only be set in: repo"},
		{"policy is the owner's", head + "defaults:\n  budget:\n    per_run_usd: 100\n", "budget.per_run_usd may only be set in: repo"},
		{"run tokens are policy", head + "defaults:\n  agent:\n    max_run_tokens: 9\n", "agent.max_run_tokens may only be set in: repo"},
		{"base branch is repo-only", head + "defaults:\n  git:\n    base_branch: evil\n", "git.base_branch may only be set in: repo"},
		{"dockerfile is repo-only", head + "profiles:\n  p:\n    dockerfile: x.Dockerfile\n", "workflows.*.dockerfile may only be set in: repo"},
		{"workflow keys are not defaults", head + "defaults:\n  workflows:\n    web:\n      commands: { test: x }\n", "may only be set in: profile, repo"},
		{"unknown key", head + "defaults:\n  agent:\n    colour: red\n", "defaults.agent.colour: is not a fugaro.yaml key"},
		{"the catalog is reserved", head + "environments:\n  java-17: {}\n", "arrives with the single base image"},
		{"a credential", head + "profiles:\n  p:\n    commands: { test: 'curl -H \"x: ghp_abcdefghijklmnopqrstuvwxyz0123\" x' }\n", "shaped like a credential"},
		{"another project", "version: 1\nproject: other\ngcp_project: acme-fugaro\n", `project: is "other", but it is read for project "acme"`},
		{"another gcp project", "version: 1\nproject: acme\ngcp_project: other-proj\n", "gcp_project: is \"other-proj\""},
		{"an anchor", head + "defaults: &d\n  agent: {}\nprofiles:\n  p: *d\n", "anchor or alias"},
		{"a merge key", head + "profiles:\n  p:\n    <<: {base: go}\n", "merge key"},
		{"a repeated key", head + "profiles:\n  p: {base: go}\n  p: {base: go}\n", `repeats the key "p"`},
		{"two documents", head + "---\nversion: 1\n", "more than one YAML document"},
		{"a bad base", head + "profiles:\n  p: { base: java-17 }\n", "profiles.p.base: must be one of go, java-services, web-node"},
		{"an unknown default profile", head + "profiles:\n  p: { base: go }\ndefault_profile: q\n", `default_profile: names "q"`},
		{"a bad profile name", head + "profiles:\n  P_1: { base: go }\n", "a profile name must be"},
		{"node off web-node", head + "profiles:\n  p: { base: go, image: { node: '20' } }\n", "only applies to base web-node"},
		{"oversized", head + "# " + strings.Repeat("x", LayerMaxBytes) + "\n", "over the 64 KiB limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := ParseProjectLayer([]byte(tc.text), testAnchor)
			var msgs []string
			for _, p := range ps {
				msgs = append(msgs, p.String())
			}
			if got := strings.Join(msgs, "; "); !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want one containing %q", got, tc.want)
			}
		})
	}
}
