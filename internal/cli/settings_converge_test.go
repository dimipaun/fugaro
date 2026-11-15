package cli

// S11: the converge loop, end to end — set a key, read it back as
// `unpublished`, run the stage `fugaro update` publishes it with, read it back
// as `ok`.
//
// The loop is the whole reason `unpublished` exists (SD3, SD4, D849): the STATE
// column answers "does the value I just wrote reach a run", and that answer is
// only worth something if the command the row names really moves the row. Every
// other test of this state seeds the published node or the published object by
// hand (settings_cloud_test.go, settings_layer_test.go), which pins the read
// against a state a fixture wrote; here the write is the stage's own, and the
// row after it is the row the operator sees once `fugaro update` has run.
//
// Nothing in this file touches a cloud: the installation is newProjectFixture's
// (the gcpfake Realtime Database and Firestore, a directory as the runs
// bucket), and the two stages are driven directly — the policy tests' own
// pattern — because the door's converge has its tests in update_test.go.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// settingsConvergeRig is the fixture's two stages over one hermetic
// installation: the project config the fixture wrote, and a run whose streams
// are a buffer, so what a stage would print to a person is what the test reads.
type settingsConvergeRig struct {
	f   *projectFixture
	lc  *localcfg.Config
	run *initRun
	out *syncBuf
}

func newSettingsConvergeRig(t *testing.T, f *projectFixture) *settingsConvergeRig {
	t.Helper()
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	run := newInitRun(cmd, &initOptions{door: updateDoor.name})
	c := &settingsConvergeRig{f: f, run: run, out: out}
	c.reload(t)
	return c
}

// reload re-reads the file the operator has just edited and hands it to the
// run: a stage the update runs reads the config as it stands when the run
// starts, and every stage below is a separate run of this rig.
func (c *settingsConvergeRig) reload(t *testing.T) {
	t.Helper()
	lc, err := localcfg.Load(c.f.path)
	if err != nil {
		t.Fatal(err)
	}
	c.lc = lc
	c.run.setProject(lc)
}

func (c *settingsConvergeRig) engine() *initEngine {
	return &initEngine{r: c.run, lc: c.lc, path: c.f.path}
}

// publishPolicy runs the stage `fugaro update` publishes the node with, over
// the file as it stands on disk now.
func (c *settingsConvergeRig) publishPolicy(t *testing.T) {
	t.Helper()
	c.reload(t)
	env := initflow.Env{Yes: true, Interactive: false}
	if _, err := newPolicyMigrateStage(c.engine()).Apply(context.Background(), env); err != nil {
		t.Fatalf("the policy stage: %v\n%s", err, c.out.String())
	}
}

// publishLayer is the same for the object every repository of the project
// resolves against.
func (c *settingsConvergeRig) publishLayer(t *testing.T) {
	t.Helper()
	c.reload(t)
	env := initflow.Env{Yes: true, Interactive: false}
	if _, err := newLayerStage(c.engine()).Apply(context.Background(), env); err != nil {
		t.Fatalf("the layer stage: %v\n%s", err, c.out.String())
	}
}

// settingsConvergeFixture is one hermetic installation that has already
// published its node — the publisher's own command is what wrote it, so the
// state the loop starts from is one a person's installation is in and not a
// node a test spelled out. Publishing is the only way a node exists at all on a
// project whose config states no node-only member (firstPublishCarriesANodeOnlyMember),
// so without this the loop would start from an installation the policy stage
// has nothing to publish to.
//
// The budget block is rewritten to block style first, because that is the
// shape of the file the operator edits and the one `settings set` writes into.
func settingsConvergeFixture(t *testing.T) *settingsConvergeRig {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	f := newProjectFixture(t, "")
	blockBudgetConfig(t, f)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the fixture publishes no node: %v", err)
	}
	c := newSettingsConvergeRig(t, f)
	if got := ceilingOn(t, f.db); got != 5 {
		t.Fatalf("the published node's ceiling = %v, want the file's 5", got)
	}
	return c
}

// settingsConvergeLayerFile writes this machine's layer file under the header
// every reader of it requires — an unanchored file is refused, and a file no
// reader accepts would pin the refusal rather than the converge.
func settingsConvergeLayerFile(t *testing.T) string {
	t.Helper()
	path, err := localcfg.LayerFile(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(layerPreamble), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsConvergeSet runs one `settings set` and returns its output.
func settingsConvergeSet(t *testing.T, key, value, layer string) string {
	t.Helper()
	out, _, err := execute(t, "settings", "set", key, value, "--layer", layer)
	if err != nil {
		t.Fatalf("settings set %s %s --layer %s: %v\n%s", key, value, layer, err, out)
	}
	return out
}

// TestSetReadUnpublishedUpdateThenConverged is S11's whole claim: the loop
// closes. A ceiling the operator tightens in the local file reads `unpublished`
// with the file's value in VALUE and `local` in WINS — no run gets it yet — and
// after the stage `fugaro update` runs, the same row reads `ok`, the same value
// and `policy` in WINS, because the node now carries what the file says and the
// node is the level a run is admitted by (D849). The project-layer half is the
// same loop over the object every repository of the project resolves against.
//
// The fixture's node carries no ceiling of its own — the deployed job's ceiling
// is the publish's — so `WINS policy` here is the node this stage wrote and not
// a figure the deployed copy already held.
func TestSetReadUnpublishedUpdateThenConverged(t *testing.T) {
	c := settingsConvergeFixture(t)

	// The write: the file's ceiling is the tighter one, so it is the value a run
	// gets once the node carries it.
	if out := settingsConvergeSet(t, "budget.per_run_usd", "3", "local"); !strings.Contains(out, "next: fugaro update") {
		t.Fatalf("the write does not end with the one command that publishes it:\n%s", out)
	}

	waiting := rowOf(t, readSettingsJSON(t, "budget.per_run_usd"), "budget.per_run_usd", "project")
	if deref(waiting.Value) != "3" || waiting.Wins != "local" {
		t.Errorf("value/wins = %q/%q, want 3/local: the file's ceiling is the one a run gets", deref(waiting.Value), waiting.Wins)
	}
	if waiting.State != stateUnpublished {
		t.Errorf("state = %q, want unpublished: no run is held to the file's 3 before the update", waiting.State)
	}

	c.publishPolicy(t)
	if got := ceilingOn(t, c.f.db); got != 3 {
		t.Fatalf("the published node's ceiling = %v, want the file's 3", got)
	}

	converged := rowOf(t, readSettingsJSON(t, "budget.per_run_usd"), "budget.per_run_usd", "project")
	if deref(converged.Value) != "3" {
		t.Errorf("value = %q after the update, want the file's 3: the value does not move, the level that decides it does", deref(converged.Value))
	}
	if converged.Wins != "policy" {
		t.Errorf("wins = %q after the update, want policy: the node is the level a run is admitted by", converged.Wins)
	}
	if converged.State != stateOK {
		t.Errorf("state = %q after the update, want ok: the node carries what the file says", converged.State)
	}

	// The project-layer half: the layer's one store is this machine's own file
	// (spec §2.2) and the object is what a run resolves against, so the row is
	// `unpublished` until the layer stage publishes the file.
	settingsConvergeLayerFile(t)
	settingsConvergeSet(t, "layer.governance.review_ratio", "2", "project")

	layer := rowOf(t, readSettingsJSON(t, "layer.governance.review_ratio"), "layer.governance.review_ratio", "project")
	if deref(layer.Value) != "2" || layer.Wins != "project" {
		t.Errorf("value/wins = %q/%q, want 2/project: the local layer file is the layer's one store", deref(layer.Value), layer.Wins)
	}
	if layer.State != stateUnpublished {
		t.Errorf("state = %q, want unpublished: the published object does not carry the file's edit yet", layer.State)
	}

	c.publishLayer(t)

	layer = rowOf(t, readSettingsJSON(t, "layer.governance.review_ratio"), "layer.governance.review_ratio", "project")
	if deref(layer.Value) != "2" {
		t.Errorf("value = %q after the update, want the file's 2", deref(layer.Value))
	}
	if layer.State != stateOK {
		t.Errorf("state = %q after the update, want ok: the published object carries what the file says", layer.State)
	}
	// And the second half does not un-converge the first: the node still carries
	// the file's ceiling, so that row is the converged one it was.
	again := rowOf(t, readSettingsJSON(t, "budget.per_run_usd"), "budget.per_run_usd", "project")
	if deref(again.Value) != "3" || again.Wins != "policy" || again.State != stateOK {
		t.Errorf("value/wins/state = %q/%q/%q after the layer stage, want 3/policy/ok", deref(again.Value), again.Wins, again.State)
	}
}

// TestConvergePrintsOnlyFugaroUpdateNext: the loop names one command, once, and
// never a list of steps (spec §1). The write ends with `next: fugaro update`,
// the read that answers `unpublished` names no command at all, and the stage
// the update runs neither prints a step of its own nor sends the operator to a
// second door — the publish is `fugaro update`'s step — so the loop's whole
// output carries exactly one `next:` line.
//
// The whole output is the loop of one key, which is what an operator converges
// in one go; the first test's second half is the same loop over the layer, and
// a loop of two edits naturally ends with the same line twice.
func TestConvergePrintsOnlyFugaroUpdateNext(t *testing.T) {
	c := settingsConvergeFixture(t)

	whole := strings.Builder{}
	whole.WriteString(settingsConvergeSet(t, "budget.per_run_usd", "3", "local"))

	// The read is the one command whose output carries no step at all: it
	// answers `unpublished` and says nothing about what to do about it.
	out, _, err := execute(t, "settings", "--json", "budget.per_run_usd")
	if err != nil {
		t.Fatalf("settings budget.per_run_usd: %v\n%s", err, out)
	}
	whole.WriteString(out)

	c.publishPolicy(t)
	whole.WriteString(c.out.String())

	next := []string{}
	for _, line := range strings.Split(strings.TrimRight(whole.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "next:") {
			next = append(next, line)
		}
	}
	if len(next) != 1 || next[0] != "next: fugaro update" {
		t.Errorf("next lines = %q, want exactly one `next: fugaro update`", next)
	}
	// The two doors this publish belongs to are not offered as a second step: a
	// loop that sent the operator to `fugaro project policy publish` after the
	// update, or to `fugaro config publish` after a layer write, is the list of
	// steps spec §1 exists to forbid.
	for _, banned := range []string{"config publish", "project policy publish"} {
		if strings.Contains(whole.String(), banned) {
			t.Errorf("the loop names %q, which is a second command:\n%s", banned, whole.String())
		}
	}
}
