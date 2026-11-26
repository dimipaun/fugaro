package cli

import (
	"context"
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
// The models are the project layer's in every fixture here, because since S1
// (D3237) a repository's file may name none — config.Resolve drops the lifted
// keys — and the pins the gate stops are the ones a run of the repository
// carries. The last case under the positive one is the file that writes a
// lifted pin anyway: it earns the lifted-key note and no line here.
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

// claudeGateYAML is a repository the gate judges: an enforcing budget and an
// api-key run (the two states a call reaches a provider key in at all), its
// workflows, and the anchor its project layer is matched by. It names no model
// — that is the layer's (claudeGateLayer).
func claudeGateYAML() string {
	return "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\n" +
		"budget: { mode: enforce, per_run_usd: 10 }\n" +
		"agent: { auth: api-key }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
}

// claudeGateLayer is the project layer the gate reads a repository's models
// from: the roles it names, over the fixture layer's own defaults. Every role
// models does not name runs the provider's own model, so a case that pins one
// role earns exactly one pin.
func claudeGateLayer(models string) string {
	return layerWithAgent("auth: api-key, model: " + gatePinOtherModel + ", models: {" + models + "}")
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
	// The layer is where the repository's models are (S1, D3237), and it is
	// the copy both surfaces read: attachLayerHere publishes it in the bucket
	// findLayer reads and in this machine's cache, which is what the gate
	// resolves the repository's file over.
	//
	// The bucket_url keeps doctor's own layer reads (layerDrift) local: the
	// rig's config names the real gs://fugaro-runs-proj-1234, and a layer that
	// applies would open it for real.
	addLocalLines(t, r, "bucket_url: file://"+t.TempDir()+"\n")
	attachLayerHere(t, "aurora", claudeGateLayer("coder: "+gatePinModel))
	root := repoCheckout(t, githubOrigin, claudeGateYAML())
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
	// keeps the finding from reading as a warning about every Claude pin. The
	// view carries the config a reader hands doctor beside the bytes — its own
	// file resolved over no layer, which is the shape doctor's read has, since
	// it passes the reader none — and that config names no model at all: the
	// gate must read the layer, not this.
	for _, tc := range []struct {
		name      string
		lc        *localcfg.Config
		layer     string
		yaml      string
		wantLines int
		wantPath  string
	}{
		{"a provider's own model under the same route", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("coder: " + gatePinOtherModel), claudeGateYAML(), 0, ""},
		{"a Claude pin no provider claims", claudeGateLC(nil),
			claudeGateLayer("coder: " + gatePinModel), claudeGateYAML(), 0, ""},
		{"a Claude pin the route does not claim", claudeGateLC(map[string]config.ModelProvider{"openrouter": {
			Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}}}),
			claudeGateLayer("coder: " + gatePinModel), claudeGateYAML(), 0, ""},
		{"a route that does not admit this repository", claudeGateLC(claudeGateRoute("acme/other")),
			claudeGateLayer("coder: " + gatePinModel), claudeGateYAML(), 0, ""},
		{"an observing budget", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("coder: " + gatePinModel),
			strings.Replace(claudeGateYAML(), "mode: enforce", "mode: observe", 1), 0, ""},
		{"a run that reaches no service", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("coder: " + gatePinModel),
			strings.Replace(claudeGateYAML(), "auth: api-key", "auth: oauth", 1), 0, ""},
		{"a level pinned to the routed Claude ID", claudeGateLC(claudeGateRoute("acme/app")),
			layerWithRoles("levels: { l1: { model: " + gatePinModel + " } }"), claudeGateYAML(), 1, "agent.levels.l1.model"},
		// The other three role pins and a level that is not the first: each is
		// read the same way, and a gate that named only the coder would leave a
		// repository stopped by its reviewer, its background model, its vision
		// model or its third level unnamed.
		{"the reviewer pinned to the routed Claude ID", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("reviewer: " + gatePinModel), claudeGateYAML(), 1, "agent.models.reviewer"},
		{"the background model pinned to it", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("background: " + gatePinModel), claudeGateYAML(), 1, "agent.models.background"},
		{"the vision model pinned to it", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("vision: " + gatePinModel), claudeGateYAML(), 1, "agent.models.vision"},
		{"the third level pinned to it", claudeGateLC(claudeGateRoute("acme/app")),
			layerWithRoles("levels: { l3: { model: " + gatePinModel + " } }"), claudeGateYAML(), 1, "agent.levels.l3.model"},
		// The pin a repository's file still writes is dropped before the merge
		// (S1, D3237): it is the lifted-key note S2 and S5 print, and the gate
		// reads the layer's model instead — here the provider's own.
		{"a file that writes the pin itself", claudeGateLC(claudeGateRoute("acme/app")),
			claudeGateLayer("coder: " + gatePinOtherModel),
			strings.Replace(claudeGateYAML(), "agent: { auth: api-key }",
				"agent: { auth: api-key, models: { coder: "+gatePinModel+" } }", 1), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheLayerNow(t, "aurora", tc.layer)
			yaml := []byte(tc.yaml)
			cfg, _, _ := config.Resolve(yaml, nil)
			lines := claudeGateLines(tc.lc, []repoView{{Repo: "acme/app", Provider: "github", YAML: yaml, Cfg: cfg}})
			if len(lines) != tc.wantLines {
				t.Fatalf("%s earns %d lines, want %d: %+v", tc.name, len(lines), tc.wantLines, lines)
			}
			if tc.wantPath != "" && !strings.Contains(lines[0].Detail, tc.wantPath) {
				t.Errorf("%s's line is %q, want it to name %s", tc.name, lines[0].Detail, tc.wantPath)
			}
		})
	}
}

// TestDoctorReadsTheEnrolledListOnce: doctor's three per-repository checks --
// the plugin pin, budget.mode: off and the gate -- read C1's list, and one read
// answers all three. The list is a read of every enrolled repository, so a
// second read is every one of them read twice for the same facts, and nothing
// in the output says how many there were: the reader stands in for the real one
// and counts the calls, which is the only way to see it.
//
// The views are the gate's own fixture, so the read is one the checks really
// judge rather than one they would have made anyway.
func TestDoctorReadsTheEnrolledListOnce(t *testing.T) {
	r := newDoctorRig(t)
	// The bucket_url keeps doctor's own layer reads local (layerDrift), as the
	// positive case above does: the rig's config names the real bucket, and a
	// layer that applies would open it for real.
	addLocalLines(t, r, "bucket_url: file://"+t.TempDir()+"\n")
	root := repoCheckout(t, githubOrigin, claudeGateYAML())
	t.Chdir(root)

	reads := 0
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		reads++
		yaml := []byte(claudeGateYAML())
		cfg, _, _ := config.Resolve(yaml, nil)
		return []repoView{{Repo: "acme/app", Provider: "github", Branch: "main", Head: "0f1e2d", YAML: yaml, Cfg: cfg}}, nil
	})
	repoNoticeDoctorChecks(t, root)
	if reads != 1 {
		t.Errorf("doctor read the enrolled list %d times, want 1: the three per-repository checks read the same list, and one read answers them all", reads)
	}
}
