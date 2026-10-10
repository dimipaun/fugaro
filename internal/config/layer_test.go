package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// TestProfileCheckout pins that a profile may set checkout: (scope
// InProfile|InRepo, design generic-tool.md §2, G3): it decodes onto
// Profile.Checkout rather than failing the strict decode as an unknown
// field.
func TestProfileCheckout(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	l := mustLayer(t, head+"profiles:\n  p:\n    base: go\n    checkout: clone\n    commands: { build: make, test: make test }\n")
	if l.Profiles["p"].Checkout != CheckoutClone {
		t.Fatalf("Profiles[p].Checkout = %q, want %q", l.Profiles["p"].Checkout, CheckoutClone)
	}
}

// TestCheckoutProfileCorpusOneReason reads each checkout-related
// testdata/project-layer/invalid fixture from disk and pins that it fails
// for exactly the one reason it exists to exercise: a schema corpus gap
// (project-layer.schema.json's checkout enum, and its clone-refuses-setup
// and clone-refuses-skip_build_scripts rules, each previously uncovered
// except apt) is only closed if the fixture is this precise.
func TestCheckoutProfileCorpusOneReason(t *testing.T) {
	a := LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	const cloneMsg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	for file, want := range map[string]struct{ path, msg string }{
		"checkout-bad-value-profile":        {"profiles.p.checkout", "must be baked or clone"},
		"checkout-clone-apt-profile":        {"profiles.p.image.apt", cloneMsg},
		"checkout-clone-setup-profile":      {"profiles.p.image.setup", cloneMsg},
		"checkout-clone-skip-build-profile": {"profiles.p.image.skip_build_scripts", cloneMsg},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "project-layer", "invalid", file+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		_, ps := ParseProjectLayer(data, a)
		if len(ps) != 1 || ps[0].Path != want.path || !strings.Contains(ps[0].Message, want.msg) {
			t.Errorf("%s: problems = %v, want exactly one at %s: %s", file, ps, want.path, want.msg)
		}
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
		{"a bad base", head + "profiles:\n  p: { base: java-17 }\n", "profiles.p.base: must be one of go, java-services, web-node, or left out for the Fugaro base"},
		{"an unknown default profile", head + "profiles:\n  p: { base: go }\ndefault_profile: q\n", `default_profile: names "q"`},
		{"a bad profile name", head + "profiles:\n  P_1: { base: go }\n", "a profile name must be"},
		{"node off web-node", head + "profiles:\n  p: { base: go, image: { node: '20' } }\n", "only applies to base web-node"},
		{"a bad checkout", head + "profiles:\n  p: { checkout: sometimes }\n", "profiles.p.checkout: must be baked or clone"},
		{"clone refuses apt in a profile", head + "profiles:\n  p: { checkout: clone, image: { apt: [jq] } }\n", "profiles.p.image.apt: checkout: clone runs the base image with no build; use checkout: baked to build an image"},
		{"oversized", head + "# " + strings.Repeat("x", LayerMaxBytes) + "\n", "over the 64 KiB limit"},
		// A null profile would otherwise decode as a valid empty Profile
		// (even as default_profile) with no scope check ever run on it,
		// while Resolve's own tree has no such profile at all.
		{"a null profile", head + "profiles:\n  p:\n", "profiles.p: must be a mapping of profile keys, not null"},
		{"a null default profile", head + "profiles:\n  p:\ndefault_profile: p\n", "profiles.p: must be a mapping of profile keys, not null"},
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

// layerProblems is the problems of text, joined, as a caller prints them.
func layerProblems(t *testing.T, text string) string {
	t.Helper()
	l, ps := ParseProjectLayer([]byte(text), testAnchor)
	if len(ps) == 0 {
		t.Fatalf("layer accepted: %+v", l)
	}
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.String())
	}
	return strings.Join(msgs, "; ")
}

const layerHead = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"

func TestParseProjectLayerRefusesMore(t *testing.T) {
	prof := func(body string) string { return layerHead + "profiles:\n  p:\n" + body }
	agent := func(body string) string { return layerHead + "defaults:\n  agent:\n" + body }
	for _, tc := range []struct{ name, text, want string }{
		// shape
		{"an explicit tag", layerHead + "defaults: !!map {}\n", "explicit YAML tag"},
		{"an explicit tag on a value", layerHead + "default_profile: !!str p\nprofiles: {p: {base: go}}\n", "explicit YAML tag"},
		{"an anchor with no alias", layerHead + "defaults: &a\n  agent: {}\n", "anchor or alias"},
		{"a non-scalar key", layerHead + "profiles:\n  ? [a]\n  : {base: go}\n", "not a plain value"},
		{"an int profile name hides a credential", layerHead + "profiles: {1: {base: go}, p: {commands: {test: 'curl -H x:ghp_abcdefghijklmnopqrstuvwxyz0123 x'}}}\n", `has the key "1", which YAML reads as int`},
		{"a null profile name", layerHead + "profiles: {null: {base: go}, ~: {base: go}}\n", "which YAML reads as null"},
		{"a bool key", agent("    true: x\n"), "which YAML reads as bool"},
		{"a block that is not a mapping", layerHead + "defaults: {git: null}\n", "defaults.git: is a block of fugaro.yaml keys, so it must be a mapping"},
		// credentials
		{"a credential in a list", layerHead + "defaults: {git: {pr: {labels: [ok, ghp_abcdefghijklmnopqrstuvwxyz0123]}}}\n", "defaults.git.pr.labels[1] (line 4): holds a value shaped like a credential"},
		{"a PEM key", prof("    commands: { test: '-----BEGIN RSA PRIVATE KEY-----' }\n"), "shaped like a credential"},
		{"an Anthropic key", prof("    commands: { test: 'echo sk-ant-api03-abcdefgh' }\n"), "shaped like a credential"},
		{"an OpenRouter key", prof("    commands: { test: 'echo sk-or-v1-abcdefgh' }\n"), "shaped like a credential"},
		{"an AWS key", prof("    commands: { test: 'echo AKIAABCDEFGHIJKLMNOP' }\n"), "shaped like a credential"},
		{"a service-account key file", prof("    commands: { test: 'echo {\"type\": \"service_account\"} > k.json' }\n"), "shaped like a credential"},
		{"a service-account private key", prof("    commands: { test: 'echo {\"private_key\": \"x\"}' }\n"), "shaped like a credential"},
		// control characters
		{"ESC in a label", layerHead + "defaults: {git: {pr: {labels: [\"a\\eb\"]}}}\n", "defaults.git.pr.labels[0] (line 4): holds a control or invisible formatting character"},
		{"bidi in a model", agent("    model: \"a\\u202eb\"\n"), "defaults.agent.model (line 6): holds a control"},
		// validateLayer
		{"a version", "version: 2\nproject: acme\ngcp_project: acme-fugaro\n", "version: must be 1"},
		{"a project name", "version: 1\nproject: Acme_1\ngcp_project: acme-fugaro\n", "project: must be a project name"},
		{"a gcp project", "version: 1\nproject: acme\ngcp_project: X\n", "gcp_project: must be a GCP project ID"},
		{"a provider", layerHead + "defaults: {git: {provider: gitlab-ish}}\n", "defaults.git.provider: must be one of"},
		{"an auth", agent("    auth: token\n"), "defaults.agent.auth: must be one of"},
		{"review rounds", agent("    review_rounds: 11\n"), "defaults.agent.review_rounds: must be between 1 and 10"},
		{"first-line review", agent("    first_line_review: maybe\n"), "defaults.agent.first_line_review: must be one of"},
		{"first-line rounds", agent("    first_line_rounds: 99\n"), "defaults.agent.first_line_rounds: must be between"},
		{"a recipe", agent("    recipe: Bad_Recipe\n"), "defaults.agent.recipe: must be a recipe name"},
		{"a negative budget", agent("    max_budget_usd: -1\n"), "defaults.agent.max_budget_usd: must be a finite number"},
		{"a NaN budget", agent("    max_budget_usd: .nan\n"), "defaults.agent.max_budget_usd: must be a finite number"},
		{"an infinite budget", agent("    max_budget_usd: .inf\n"), "defaults.agent.max_budget_usd: must be a finite number"},
		{"a model", agent("    models: { coder: 'a b' }\n"), "defaults.agent.models.coder: must not contain whitespace"},
		// validateProfile
		{"a long description", prof("    description: " + strings.Repeat("é", 201) + "\n"), "profiles.p.description: must be one line"},
		{"ESC in a description", prof("    description: \"a\\eb\"\n"), "holds a control"},
		{"a tab in a description", prof("    description: \"a\\tb\"\n"), "profiles.p.description: must be one line"},
		{"a rerun command", prof("    commands: { rerun_failed: { command: ' ', each: '{id}' } }\n"), "profiles.p.commands.rerun_failed.command: is required"},
		{"a rerun each", prof("    commands: { rerun_failed: { command: x, each: y } }\n"), "profiles.p.commands.rerun_failed.each: must contain {id}"},
		{"a cache key", prof("    cache: [ { paths: [a] } ]\n"), "profiles.p.cache[0].key: must list"},
		{"cache paths", prof("    cache: [ { key: [a] } ]\n"), "profiles.p.cache[0].paths: must list"},
		{"a cpu", prof("    resources: { cpu: -1 }\n"), "profiles.p.resources.cpu: must be at least 1"},
		{"a memory", prof("    resources: { memory: 16GB }\n"), "profiles.p.resources.memory: must look like"},
		{"a timeout", prof("    timeouts: { stage: -1h }\n"), "profiles.p.timeouts.stage: must be positive"},
		{"a rebuild", prof("    rebuild: { check: weekly }\n"), "profiles.p.rebuild.check: must be daily or off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := layerProblems(t, tc.text); !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want one containing %q", got, tc.want)
			}
		})
	}
}

func TestProjectLayerDescriptionCountsCharacters(t *testing.T) {
	l := mustLayer(t, layerHead+"profiles:\n  p: { description: "+strings.Repeat("é", 200)+" }\n")
	if n := len(l.Profiles["p"].Description); n != 400 {
		t.Fatalf("description is %d bytes", n)
	}
}

// TestProjectLayerErrorsArePrintable: the writer's keys and profile names
// come back escaped, and a credential written as a key never comes back.
func TestProjectLayerErrorsArePrintable(t *testing.T) {
	const tok = "ghp_abcdefghijklmnopqrstuvwxyz0123"
	for _, tc := range []struct{ name, text, want string }{
		{"ESC in a top-level key", layerHead + "\"x\\ey\": 1\n", `x\u001by: is not a project layer key`},
		{"newline in a key", layerHead + "defaults: {agent: {\"a\\nb\": 1}}\n", `defaults.agent (line 4): has a key "a\u000ab" holding a control`},
		{"ESC in a profile name", layerHead + "profiles: {\"p\\e[2J\": {secrets: []}}\n", `profiles.p\u001b[2J.secrets: workflows.*.secrets may only be set in: repo`},
		{"newline in a profile name", layerHead + "profiles: {\"p\\nq\": {secrets: []}}\n", `profiles.p\u000aq.secrets: workflows.*.secrets may only`},
		{"a credential as a key", layerHead + "profiles:\n  p:\n    commands: {" + tok + ": x}\n", "profiles.p.commands (line 6): has a key shaped like a credential"},
		{"a credential as a profile name", layerHead + "profiles: {" + tok + ": {secrets: []}}\n", "profiles.<credential>.secrets: workflows.*.secrets may only"},
		{"a credential as a repeated key", layerHead + "profiles: {" + tok + ": {}, " + tok + ": {}}\n", `repeats the key "<credential>"`},
		{"a credential as a non-string key", layerHead + "profiles: {? !!str " + tok + " : {}}\n", "explicit YAML tag"},
		// yaml.v3 percent-decodes a verbatim tag, so a key's own tag (not
		// just its value) can carry control or formatting characters.
		{"a key's tag", layerHead + "profiles:\n  !<tag:x,2000:%1B%5B2J%E2%80%AE> api: {}\n", `has the key "api", which YAML reads as tag:x,2000:`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := layerProblems(t, tc.text)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want one containing %q", got, tc.want)
			}
			if strings.ContainsAny(got, "\x1b\n") || strings.Contains(got, tok) {
				t.Fatalf("problems echo raw text: %q", got)
			}
		})
	}
}

func TestProfileHasExecutable(t *testing.T) {
	for _, body := range []string{
		"commands: { build: make }", "commands: { test: make test }",
		"commands: { lint: make lint }", "commands: { fix: make fmt }",
		"commands: { rerun_failed: { command: go test, each: ' -run {id}' } }",
		"{ base: java-services, image: { apt: [graphviz] } }", "image: { setup: [make tools] }",
		// A mise tool is as powerful as image.setup: every URL/host-form
		// backend (cargo:, go:, npm:, asdf:/vfox:, ubi:/github:/aqua:)
		// passes ValidMiseTool's charset, and the build installs it with
		// the workflow's build secrets mounted. A profile holding only
		// this must report HasExecutable true, so publishing it needs
		// --executable-changes.
		`image: { tools: { "cargo:https://evil.example/x": "ref:main" } }`,
	} {
		if !strings.HasPrefix(body, "{") {
			body = "{ " + body + " }"
		}
		l := mustLayer(t, layerHead+"profiles:\n  p: "+body+"\n")
		if !l.Profiles["p"].HasExecutable() {
			t.Errorf("%s: HasExecutable() = false", body)
		}
	}
	l := mustLayer(t, layerHead+"profiles:\n  p: { base: go, description: x, resources: { cpu: 2 }, commands: { reports: [r.xml] } }\n")
	if l.Profiles["p"].HasExecutable() {
		t.Error("a profile with no shell has HasExecutable() = true")
	}
}

func TestProjectLayerRawAndTree(t *testing.T) {
	l := mustLayer(t, testLayer)
	if string(l.Raw) != testLayer {
		t.Fatalf("Raw = %q", l.Raw)
	}
	prs, ok := l.tree["profiles"].(map[string]any)
	if !ok || prs["node-web"] == nil || nodeValue(l.tree["default_profile"]) != "java-service" {
		t.Fatalf("tree = %v", l.tree)
	}
	if got := LayerCopyKey("acme-web"); got != "builds/acme-web/project-layer.yaml" {
		t.Fatalf("LayerCopyKey = %q", got)
	}
}

// nodeValue is a tree leaf's text ("" for anything else).
func nodeValue(v any) string {
	if n, ok := v.(*yaml.Node); ok {
		return n.Value
	}
	return ""
}

func TestFugaroYAMLBudgetIsFinite(t *testing.T) {
	cfg, ps := Parse(readCorpus(t, "valid", "budget-policy.yaml"))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg.Agent.MaxBudgetUSD = v
		var got []string
		for _, p := range Validate(cfg) {
			got = append(got, p.String())
		}
		if !strings.Contains(strings.Join(got, "; "), "agent.max_budget_usd: must be a finite number") {
			t.Errorf("%v: problems %v", v, got)
		}
	}
}

// TestProjectLayerNeverEchoesCredentialsOrLongText: an alias or a tag holding
// a credential, a credential or bidi character in a comment or a %TAG
// directive, and an over-long default_profile or key.
func TestProjectLayerNeverEchoesCredentialsOrLongText(t *testing.T) {
	const tok = "ghp_abcdefghijklmnopqrstuvwxyz0123"
	long := strings.Repeat("z", 500)
	for _, tc := range []struct {
		name, text, want string
		long             bool
	}{
		{"an alias", layerHead + "profiles: {p: *" + tok + "}\n", "", false},
		{"a short tag", layerHead + "profiles: {p: !" + tok + " {}}\n", "explicit YAML tag", false},
		{"a verbatim tag", layerHead + "profiles: {p: !<tag:" + tok + "> {}}\n", "explicit YAML tag", false},
		{"a tag on the root", "!" + tok + "\n" + layerHead, "explicit YAML tag", false},
		{"a comment", layerHead + "# " + tok + "\n", "line 4: holds a credential-shaped string", false},
		{"a trailing comment", layerHead + "profiles: {} # " + tok + "\n", "line 4: holds a credential-shaped string", false},
		{"a %TAG directive", "%TAG !e! tag:" + tok + ":\n---\n" + layerHead, "holds a credential-shaped string", false},
		{"a bidi character in a comment", layerHead + "# a‮b\n", "line 4: holds a control or invisible", false},
		{"a long default_profile", layerHead + "profiles: {}\ndefault_profile: " + long + "\n", "names", true},
		{"a long key", layerHead + "defaults: {agent: {\"a\\n" + long + "\": 1}}\n", "holding a control", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := layerProblems(t, tc.text)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want one containing %q", got, tc.want)
			}
			if strings.Contains(got, tok) || strings.Contains(got, "‮") {
				t.Fatalf("problems echo the credential: %q", got)
			}
			if tc.long && strings.Contains(got, long) {
				t.Fatalf("problems echo %d characters", len(long))
			}
		})
	}
}

func TestProjectLayerCorpus(t *testing.T) {
	a := LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	for _, kind := range []string{"valid", "invalid"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "project-layer", kind, "*.yaml"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no %s project layer corpus", kind)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			l, ps := ParseProjectLayer(data, a)
			switch {
			case kind == "valid" && len(ps) > 0:
				t.Errorf("%s: unexpected problems %v", f, ps)
			case kind == "invalid" && l != nil:
				t.Errorf("%s: parsed without problems, want invalid", f)
			}
		}
	}
}
