package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// claude_gate_test.go is KS20's tests. The owner ruled on 2026-11-26 that the
// release ships without Check 50's result, so the branch built here is the
// not-run one: the refusal stays, and the two surfaces that can see the whole
// project name every repository whose runs it stops, with the fix.
//
// The positive case drives the whole command — doctor's row and the update's
// line for one repository, in the same words — and the cases under it hold the
// predicate down: a pin the gate does not stop earns no line, in either
// direction.

// gatePinModel is the one model spelling a route may claim (config.
// CheckClaudeClaim), and gatePinOtherModel the provider's own model beside it.
const (
	gatePinModel      = "anthropic/claude-opus-5-5"
	gatePinOtherModel = "deepseek/deepseek-v4.1"
)

// claudeGateYAML is a repository the gate stops: an enforcing budget, an
// api-key run (the two states a call reaches a provider key in at all), and a
// coder pinned to the Claude ID the route below claims.
func claudeGateYAML(models string) string {
	return "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"budget: { mode: enforce, per_run_usd: 10 }\n" +
		"agent: { auth: api-key, models: {" + models + "} }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
}

// claudeGateRoutes is the machine's own side: the owner's providers block,
// which claims the pinned Claude ID for acme/app and admits its code.
func claudeGateRoutes(allow string) string {
	return "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n" +
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"" + gatePinModel + "\", \"deepseek/*\"]\n" +
		"    allow_data_to: " + allow + "\n"
}

// claudeGateLC is a machine whose project config carries providers, for the
// unit cases below, where doctor's own config file is not in play.
func claudeGateLC(providers map[string]config.ModelProvider) *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		Repos:     map[string]localcfg.Repo{"acme/app": {Provider: "github"}},
		Providers: providers}
}

// claudeGateRoute is the one route claudeGateRoutes writes, as the local
// config's own table.
func claudeGateRoute(allow ...string) map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Models:      []string{gatePinModel, "deepseek/*"},
		AllowDataTo: allow,
	}}
}

// TestDoctorNamesARepositoryTheGateWillStop: a repository whose pin reaches a
// provider route and is a Claude model is named by doctor, with the pin and the
// edit that takes it out of the gate's way, and `fugaro update` prints the same
// line for it — the release stops these runs, so the owner is owed the list
// before it does (KS20, D3000, D3013).
func TestDoctorNamesARepositoryTheGateWillStop(t *testing.T) {
	r := newDoctorRig(t)
	addLocalLines(t, r, claudeGateRoutes(`["acme/app"]`))
	root := repoCheckout(t, githubOrigin, claudeGateYAML("coder: "+gatePinModel+", reviewer: "+gatePinOtherModel))
	t.Chdir(root)

	checks := repoNoticeDoctorChecks(t, root)
	c, ok := checks[claudeGateCheckID]
	if !ok {
		t.Fatalf("doctor reports no %s on a repository whose pin reaches a provider route: %+v", claudeGateCheckID, checks)
	}
	if c.OK {
		t.Fatalf("%s = %+v, want a finding: the credentials service refuses this model and release A serves the repository's keys", claudeGateCheckID, c)
	}
	if c.Severity != "warning" || c.Class != doctorClassActionable {
		t.Errorf("%s is %q/%q, want a warning in the actionable class: the project config is the owner's and nothing writes it here", claudeGateCheckID, c.Severity, c.Class)
	}
	// The pin, the provider, and the fix, under the repository they are about.
	want := "acme/app: agent.models.coder: " + gatePinModel + " through openrouter: " + claudeGateFix
	if !strings.Contains(c.Problem, want) {
		t.Errorf("%s does not carry the line, %q:\n%s", claudeGateCheckID, want, c.Problem)
	}
	if !strings.Contains(c.Fix, "route's models") {
		t.Errorf("%s's fix does not name the edit: %q", claudeGateCheckID, c.Fix)
	}
	if strings.Contains(c.Problem, "reviewer") {
		t.Errorf("%s names the reviewer's pin, which the gate does not stop:\n%s", claudeGateCheckID, c.Problem)
	}
	// An enforcing mode and one route: nothing else in the config is a finding.
	if strings.Contains(c.Problem, gatePinOtherModel) {
		t.Errorf("%s names a model the gate does not stop:\n%s", claudeGateCheckID, c.Problem)
	}

	// And the update's own line for the same repository, verbatim: one finding,
	// one text, printed by both.
	e, out := checkoutEngine(t, nil)
	// checkoutEngine's config is a literal with no routes (a rig sets
	// FUGARO_CONFIG empty), and the run reads the project's own table: the
	// owner's block is written onto it here, as the rig above writes it into
	// doctor's config file.
	e.lc.Providers = claudeGateRoute("acme/app")
	// The step itself, not the helper: what a person running `fugaro update`
	// meets is the step after the stage loop, and the gate's line has to be in
	// its output.
	e.noticeRepoValues(theUpdateLeavesTheRemoval(t.Context()))
	if seen := out.String(); !strings.Contains(seen, "acme/app: "+string(initflow.RepoNeedsYou)+": "+want[len("acme/app: "):]) {
		t.Fatalf("`fugaro update` does not print doctor's line for the same repository:\n%s", seen)
	}
	steps := e.reposState.steps
	if len(steps) != 1 || steps[0].name != claudeGateStep || len(steps[0].lines) != 1 {
		t.Fatalf("the step recorded %+v, want one %s notice with one line: the run's own document carries what it printed", steps, claudeGateStep)
	}

	// A repository the gate does not stop earns no line at all, which is what
	// keeps the finding from reading as a warning about every Claude pin.
	for _, tc := range []struct {
		name      string
		lc        *localcfg.Config
		yaml      string
		wantLines int
		wantPath  string
	}{
		{"a provider's own model under the same route", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateYAML("coder: " + gatePinOtherModel), 0, ""},
		{"a Claude pin no provider claims", claudeGateLC(nil),
			claudeGateYAML("coder: " + gatePinModel), 0, ""},
		{"a Claude pin the route does not claim", claudeGateLC(map[string]config.ModelProvider{"openrouter": {
			Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}}}),
			claudeGateYAML("coder: " + gatePinModel), 0, ""},
		{"a route that does not admit this repository", claudeGateLC(claudeGateRoute("acme/other")),
			claudeGateYAML("coder: " + gatePinModel), 0, ""},
		{"an observing budget", claudeGateLC(claudeGateRoute("acme/app")),
			strings.Replace(claudeGateYAML("coder: "+gatePinModel), "mode: enforce", "mode: observe", 1), 0, ""},
		{"a run that reaches no service", claudeGateLC(claudeGateRoute("acme/app")),
			strings.Replace(claudeGateYAML("coder: "+gatePinModel), "auth: api-key", "auth: oauth", 1), 0, ""},
		{"a level pinned to the routed Claude ID", claudeGateLC(claudeGateRoute("acme/app")),
			strings.Replace(claudeGateYAML("coder: "+gatePinOtherModel), "agent: {", "agent: { levels: { l1: { model: "+gatePinModel+" } },", 1), 1, "agent.levels.l1.model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := claudeGateLines(tc.lc, []repoView{{Repo: "acme/app", Provider: "github", Cfg: viewCfg(t, tc.yaml)}})
			if len(lines) != tc.wantLines {
				t.Fatalf("%s earns %d lines, want %d: %+v", tc.name, len(lines), tc.wantLines, lines)
			}
			if tc.wantPath != "" && !strings.Contains(lines[0].Detail, tc.wantPath) {
				t.Errorf("%s's line is %q, want it to name %s", tc.name, lines[0].Detail, tc.wantPath)
			}
		})
	}
}
