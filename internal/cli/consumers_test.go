package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// Review Focus 2: one repository file and one layer give one resolved
// config, whichever consumer resolves it. The runner leg calls
// runner.ResolveConfig, the exported core of (*run).resolveConfig
// (internal/runner/runner.go), not a hand-rolled config.Resolve call: a
// runner-side divergence (TestRunnerRecordsTheLayer, Task 7, pins the
// same function from inside the package) would otherwise go unnoticed
// here.
func TestEveryConsumerResolvesTheSameBytes(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	repoYAML := minimalAnchored + "agent:\n  review_rounds: 3\n"
	dir := layerCheckout(t, f, repoYAML)
	env := fileEnv(t, f)
	ctx := context.Background()
	slug := mustSlug("github", "acme/other")
	if err := writeLayerCopy(ctx, env.bucket, slug, []byte(testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}

	rf, err := resolveFugaroYAML(ctx, []byte(repoYAML), env.lc, layerOptions{})
	if err != nil || len(rf.Problems) > 0 {
		t.Fatal(err, rf.Problems)
	}
	sums["cli (validate, run, config show)"] = rf.Res.ConfigSHA256

	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Task: "x"}
	if _, err := embedProjectLayer(ctx, env, spec, io.Discard); err != nil || spec.ProjectLayer == nil {
		t.Fatal(err, spec.ProjectLayer)
	}
	rc := runner.ResolveConfig([]byte(repoYAML), spec.ProjectLayer, env.lc.Name, spec.BaseBranch)
	if rc.LayerErr != nil || len(rc.Problems) > 0 || !rc.Applied {
		t.Fatal(rc.LayerErr, rc.Problems, rc.Applied)
	}
	sums["runner (the task's embedded text)"] = rc.Res.ConfigSHA256

	data, err := readLayerCopy(ctx, f.bucket, slug, config.LayerSum([]byte(testProjectLayer)))
	if err != nil {
		t.Fatal(err)
	}
	_, rr, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Data: data, Where: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	sums["cloud build render (the copy)"] = rr.Res.ConfigSHA256

	head, err := jobHeadConfig(ctx, env.bucket, slug, cloneOf(t, map[string]string{"fugaro.yaml": repoYAML}), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	sums["check job (the copy, over head)"] = head.SHA256()

	want := sums["cli (validate, run, config show)"]
	for who, got := range sums {
		if got != want {
			t.Errorf("%s resolved %s, the CLI %s", who, got, want)
		}
	}
}

// TestEveryReaderDropsLiftedKeys is I1's half of lift L8 on the CLI: every
// reader of a repository's file drops every lifted key (docs/repo-override-rulings/spec.md
// §6, the report's §5). TestEveryConsumerResolvesTheSameBytes above pins that the
// readers agree on one file's bytes; this test pins what those bytes leave out —
// a file that writes every key C1 lists (testdata/lifted/fugaro.yaml) resolves,
// through every reader, to a configuration carrying none of their values, and
// the readers answering a config.Resolution name every one of them as dropped.
//
// The drop lives in one place — config.Resolve, both arms, every ref (S1) — and
// config.AgentOf for the four readers that read the agent block without
// resolving (F25). So this walk is a fence around that one place: a reader that
// grew a read of its own would resolve the file outside it and carry a sentinel
// here. The four agent readers are driven as they are called —
// validateCraftLayers, run.go's agentAtRef, craft ls's own layers, and
// budget_stops.go's UnpricedPins — because each takes config.AgentOf's answer,
// not a resolved config.
func TestEveryReaderDropsLiftedKeys(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	body := string(readFile(t, filepath.Join("testdata", "lifted", "fugaro.yaml")))
	if got := config.LiftedKeyNotes([]byte(body)); len(got) != len(config.LiftedKeys) {
		t.Fatalf("the fixture names %d lifted keys, want all %d", len(got), len(config.LiftedKeys))
	}
	dir := liftedCheckout(t, f, body)
	t.Chdir(dir)

	ctx := context.Background()
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	if err := writeLayerCopy(ctx, env.bucket, slug, []byte(testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	spec := &task.Spec{Version: 1, RunID: "20261127-100000-abcd", Repo: "acme/other",
		Ref: "main", BaseBranch: "main", Task: "x"}
	if _, err := embedProjectLayer(ctx, env, spec, io.Discard); err != nil || spec.ProjectLayer == nil {
		t.Fatal(err, spec.ProjectLayer)
	}

	// Each reader answers the configuration it read the file into, and — where
	// it hands one back — the resolution, whose Lifted is what the drop named.
	readers := []struct {
		name string
		read func(t *testing.T) (*config.Config, []config.RetiredKeyNote)
	}{
		{"resolveFugaroYAML (validate, run, config show)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			rf, err := resolveFugaroYAML(ctx, []byte(body), env.lc, layerOptions{})
			if err != nil || len(rf.Problems) > 0 {
				t.Fatal(err, rf.Problems)
			}
			return rf.Cfg, rf.Res.Lifted
		}},
		{"checkoutParse (the working tree)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			cfg, ps := checkoutParse(ctx, "acme/other")
			if cfg == nil || len(ps) > 0 {
				t.Fatal(cfg, ps)
			}
			return cfg, nil
		}},
		{"configLayerAnchor (config layer)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			data, _, err := configLayerAnchor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			fl, err := findLayer(ctx, getenvOS, data, env.lc, layerOptions{}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			cfg, res, ps := config.Resolve(data, fl.Layer)
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			return cfg, res.Lifted
		}},
		{"checkoutFugaroYAMLAtRef (the ref arm)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			data, err := checkoutFugaroYAMLAtRef(ctx, dir, "main")
			if err != nil {
				t.Fatal(err)
			}
			rc := runner.ResolveConfig(data, spec.ProjectLayer, env.lc.Name, spec.BaseBranch)
			if rc.LayerErr != nil || len(rc.Problems) > 0 || !rc.Applied {
				t.Fatal(rc.LayerErr, rc.Problems, rc.Applied)
			}
			return rc.Cfg, rc.Res.Lifted
		}},
		{"cascadeRefConfig (a cascade launch)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			cfg := cascadeRefConfig(ctx, env, spec)
			if cfg == nil {
				t.Fatal("cascadeRefConfig read no document at the ref")
			}
			return cfg, nil
		}},
		{"launchLevelsConfig (the launch's level gates)", func(t *testing.T) (*config.Config, []config.RetiredKeyNote) {
			cfg := launchLevelsConfig(ctx, env, spec)
			if cfg == nil {
				t.Fatal("launchLevelsConfig read no configuration")
			}
			return cfg, nil
		}},
	}

	for _, r := range readers {
		t.Run(r.name, func(t *testing.T) {
			cfg, lifted := r.read(t)
			assertNoLiftedValue(t, r.name, cfg)
			if lifted != nil {
				assertNamesEveryLiftedKey(t, r.name, lifted, body)
			}
		})
	}

	// The four config.AgentOf readers, each driven as it is called: they read
	// the agent block without resolving, so the drop has to be AgentOf's own.
	t.Run("craft ls's layers (craft.go)", func(t *testing.T) {
		out, errOut, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s%s", err, out, errOut)
		}
		if bytes.Contains([]byte(out), []byte(liftedSentinelModel)) {
			t.Errorf("craft ls --json printed %q, a lifted key's value:\n%s", liftedSentinelModel, out)
		}
	})

	t.Run("validateCraftLayers (validate.go)", func(t *testing.T) {
		rf, err := resolveFugaroYAML(ctx, []byte(body), env.lc, layerOptions{})
		if err != nil || len(rf.Problems) > 0 {
			t.Fatal(err, rf.Problems)
		}
		out, err := json.Marshal(validateCraftLayers([]byte(body), rf))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte(liftedSentinelModel)) {
			t.Errorf("validateCraftLayers read %q, a lifted key's value: %s", liftedSentinelModel, out)
		}
	})

	t.Run("agentAtRef (run.go)", func(t *testing.T) {
		agent, _, ok := agentAtRef(ctx, env, "acme/other", "main")
		if !ok {
			t.Fatal("agentAtRef read no agent block at the ref")
		}
		assertNoLiftedValue(t, "agentAtRef", &config.Config{Agent: agent})
	})

	t.Run("UnpricedPins (budget_stops.go)", func(t *testing.T) {
		agent, ok := config.AgentOf([]byte(body))
		if !ok {
			t.Fatal("the fixture's file names no agent block")
		}
		pins := config.UnpricedPins(agent, nil)
		out, err := json.Marshal(pins)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte(liftedSentinelModel)) {
			t.Errorf("UnpricedPins read %q, a lifted key's value: %s", liftedSentinelModel, out)
		}
	})
}

// liftedSentinelModel is the model testdata/lifted/fugaro.yaml writes in every
// lifted model key. The layer the fixture publishes names other models, so a
// resolved configuration carrying this one came from the file.
const liftedSentinelModel = "repo-9"

// liftedCheckout makes the working directory a checkout of acme/other whose
// fugaro.yaml is body exactly as written — publishFilePins and layerCheckout
// move a fixture's own lifted keys into the layer, which is the shape this test
// is not about — committed at main, with the job the implicit workflow resolves.
func liftedCheckout(t *testing.T, f *cloudFixture, body string) string {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), config.ImplicitWorkflow), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml": body, "build.sh": "true\n", "test.sh": "true\n", "package-lock.json": "{}\n",
	})
	commitRef(t, dir, "main")
	return dir
}

// liftedSentinels are the values testdata/lifted/fugaro.yaml writes for the
// lifted keys a resolution can carry out of it: the model every model key names,
// the rigor area's own name and match, and the rigor level. None is a value the
// layer the fixture publishes names, so one in a resolved configuration came from
// the file. The names check beside this one cannot stand in for them: three of
// the readers answer no config.Resolution (checkoutParse, cascadeRefConfig and
// launchLevelsConfig), and a reader that resolved rigor or ab.rate outside
// config.Resolve would slip past a search for the model alone (the review's
// finding).
var liftedSentinels = []string{liftedSentinelModel, "repo-area", "hotfix"}

// assertNoLiftedValue fails when the resolved configuration still carries a
// lifted key's value. The whole struct is marshalled rather than its fields read
// one by one: a lifted key can land in any of the model slots, the rigor block
// or the grades, and a reader that grew a slot of its own would slip past a
// hand-written list of them.
//
// ab.rate is the one value no search can find — the file writes 0, which is what
// an absent block reads as too — so the A/B opt-out is read by the block's own
// presence instead (R2: the drop takes the block with the key, D556).
func assertNoLiftedValue(t *testing.T, who string, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		t.Fatalf("%s resolved no configuration", who)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range liftedSentinels {
		if bytes.Contains(out, []byte(s)) {
			t.Errorf("%s resolved a configuration carrying %q, a lifted key's value: %s", who, s, out)
		}
	}
	if cfg.AB != nil {
		t.Errorf("%s resolved a configuration carrying the file's ab: block (the repository's opt-out R2 drops with it): %+v", who, cfg.AB)
	}
}

// assertNamesEveryLiftedKey fails when a reader that hands back a resolution did
// not name exactly the lifted keys the file wrote, in the file's own paths: the
// drop names what it took, as LiftedKeyNotes answers for the bytes.
func assertNamesEveryLiftedKey(t *testing.T, who string, got []config.RetiredKeyNote, body string) {
	t.Helper()
	want := config.LiftedKeyNotes([]byte(body))
	paths := func(notes []config.RetiredKeyNote) []string {
		out := make([]string, 0, len(notes))
		for _, n := range notes {
			out = append(out, n.Path)
		}
		return out
	}
	if !slices.Equal(paths(got), paths(want)) {
		t.Errorf("%s named %v as dropped, want the file's lifted keys (%v)", who, paths(got), paths(want))
	}
}
