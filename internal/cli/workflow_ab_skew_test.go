package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// workflow_ab_skew_test.go is workflowABSince's half of the plan's second
// Convention, and the reason the constant is not slotSince's: the key is
// fugaro.yaml's — workflows.<name>.ab, the ask for an A/B reviewer arm (O12) —
// while slotSince's is the task's own ab.slot. Every release decodes the file
// with KnownFields, so the block alone is what an older runner refuses, and
// 0.25.0 is tagged — it carries the task's ab.slot and the rules that read it,
// and no config.WorkflowAB — which is why the two keys cannot share a number.

// workflowABConfig is a configuration as the three gates read one: a file whose
// workflows write their own ab: blocks.
func workflowABConfig(blocks map[string]string) *config.Config {
	cfg := &config.Config{Workflows: map[string]config.Workflow{}}
	for name, arm := range blocks {
		cfg.Workflows[name] = config.Workflow{AB: &config.WorkflowAB{ReviewerArm: arm}}
	}
	return cfg
}

// TestWorkflowABSinceHasAReleaseBullet: workflowABSince names 0.26.0, which is
// not tagged, so docs/release.md's Before-you-tag list is what holds the
// constant to the release actually cut — the job every other *Since constant's
// bullet does, and the one this constant's own comment claims. The bullet also
// has to say why the constant is not slotSince's: one number for the two keys
// would ship a gate that admits an image whose runner dies at bootstrap on the
// file's key, 0.25.0 being the tagged release that carries the slot and not the
// block.
func TestWorkflowABSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `workflowABSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no workflowABSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+workflowABSince+"\"`") {
		t.Errorf("docs/release.md's workflowABSince bullet does not carry the constant's release (%q)", workflowABSince)
	}
	for _, want := range []string{"workflows.<name>.ab", "reviewer_arm", "gateWorkflowAB", "checkWorkflowABImage", "checkCloneWorkflowABImage", "workflowABImageWarnings", "not tagged yet", "slotSince", "0.25.0"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's workflowABSince bullet does not say %q", want)
		}
	}
}

// TestRecipesSkewRefusesWorkflowABOnOldImage: a configuration that asks for an
// A/B reviewer arm needs a job image whose runner knows the key (B1.4, O12). It
// is not slotSince's gate: the ask is a key of the file the job decodes either
// way, and 0.25.0 is tagged with no config.WorkflowAB, so an image the slot
// gate admits dies at bootstrap on `field ab not found in type
// config.Workflow`. Every workflow of the file that writes a block is judged and
// not only the one the task selects, because the file that fails to decode is
// the whole file; a file that writes none, and one that could not be read at
// all, are gated by nothing; a base image that is not a release cannot be dated,
// so it only warns; and an image at the release or later is not gated.
func TestRecipesSkewRefusesWorkflowABOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "x"}
	old := shiftRelease(t, workflowABSince, -1)
	cfg := workflowABConfig(map[string]string{"web": "l2"})
	check := func(baseRef string) (string, error) {
		var warn bytes.Buffer
		writeBuildRecord(t, f, appSlug, "web", baseRef)
		err := checkWorkflowABImage(context.Background(), env, appSlug, spec, "base", cfg, &warn)
		return warn.String(), err
	}
	warn, err := check("ghcr.io/dimipaun/fugaro-base:" + old)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("%s: warn = %q, err = %v, want the launch refused", old, warn, err)
	}
	for _, want := range []string{
		"fugaro.yaml's workflows.web.ab of acme/app",
		"needs a job image whose runner knows a workflow's ab: block (workflows.<name>.ab) (fugaro " + workflowABSince + " or later)",
		imageGateRemedy,
		"remove the ab: block from fugaro.yaml",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if warn != "" {
		t.Errorf("%s: warning %q, want none (refused, not warned)", old, warn)
	}
	if warn, err := check("ghcr.io/dimipaun/fugaro-base:" + workflowABSince); err != nil {
		t.Fatalf("%s: warn = %q, err = %v", workflowABSince, warn, err)
	}
	warn, err = check("us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c")
	if err != nil {
		t.Fatalf("dev base: %v", err)
	}
	if !strings.Contains(warn, "not a release base image") || !strings.Contains(warn, "is not checked") {
		t.Errorf("dev base: warning %q, want the not-a-release note", warn)
	}
	// The same launch on a checkout: clone workflow, whose job has no derived
	// image and so no build record, and the gate raises checkCloneImageSince's
	// threshold on base_images.base itself.
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:" + old}
	var cloneWarn bytes.Buffer
	err = checkCloneWorkflowABImage(&lc, spec, cfg, &cloneWarn)
	if err == nil || !strings.Contains(err.Error(), "fugaro "+workflowABSince+" or later") || !strings.Contains(err.Error(), "release "+old) || ExitCode(err) != ExitUserError {
		t.Fatalf("clone %s: warn = %q, err = %v", old, cloneWarn.String(), err)
	}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:" + workflowABSince
	if err := checkCloneWorkflowABImage(&lc, spec, cfg, &bytes.Buffer{}); err != nil {
		t.Fatalf("clone %s: %v", workflowABSince, err)
	}
	// A sibling workflow's block is the same refusal: the job dies decoding the
	// file, before it reads which workflow it runs.
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+old)
	sibling := workflowABConfig(map[string]string{"api": "l3", "web": "l2"})
	if got := workflowABNames(sibling); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Errorf("workflowABNames = %v, want both workflows, sorted", got)
	}
	err = checkWorkflowABImage(context.Background(), env, appSlug, spec, "base", sibling, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "workflows.api.ab, workflows.web.ab") {
		t.Fatalf("a sibling workflow's block: err = %v, want both keys named", err)
	}
	// A workflow that writes no block asks nothing of the image, and neither
	// does a configuration that was never read.
	for _, c := range []*config.Config{nil, {}, &config.Config{Workflows: map[string]config.Workflow{"web": {}}}} {
		if err := checkWorkflowABImage(context.Background(), env, appSlug, spec, "base", c, &bytes.Buffer{}); err != nil {
			t.Errorf("%+v: err = %v, want none", c, err)
		}
	}
	// A block that names no level is still a block: the configuration reader
	// refuses it before any launch (validateWorkflowAB), and the gate judges the
	// key being written rather than the arm it holds.
	if err := checkWorkflowABImage(context.Background(), env, appSlug, spec, "base", workflowABConfig(map[string]string{"web": ""}), &bytes.Buffer{}); err == nil {
		t.Error("an ab: block naming no level was not judged: the gate reads the key, not the value")
	}
}

// TestUpdateRepoRefusesWorkflowABOnAnOlderBaseImage is workflowABSince's gate in
// fugaro update --repo, refuseTriageSkew's shape: a launch from outside a
// checkout reads no fugaro.yaml, so the key the run will find once it has one is
// judged by the operator's own converge, before any cloud effect. The boundary
// is the constant's own: one release below is refused, the constant and one
// above pass, a base that is not a release only warns, and no base at all
// passes.
func TestUpdateRepoRefusesWorkflowABOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, workflowABSince, -1), shiftRelease(t, workflowABSince, 1)
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	older := "ghcr.io/dimipaun/fugaro-base:" + below
	cfg := workflowABConfig(map[string]string{"web": "l2"})
	err := r.gateWorkflowAB(r.o, "acme/app", lc(older), cfg)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want refused", err)
	}
	for _, want := range []string{"fugaro.yaml's workflows.web.ab of acme/app", "fugaro " + workflowABSince + " or later", older + ", release " + below, "fugaro update --base base"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
	}
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + workflowABSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseWorkflowABSkew("acme/app", cfg, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseWorkflowABSkew("acme/app", cfg, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no block asks nothing of the image, in any
	// shape of "nothing": no configuration, no workflows, a workflow with no
	// ab: block.
	for _, c := range []*config.Config{nil, {}, &config.Config{Workflows: map[string]config.Workflow{"web": {}}}} {
		if err := r.gateWorkflowAB(r.o, "acme/app", lc(older), c); err != nil {
			t.Errorf("%+v: err = %v, want none", c, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateWorkflowAB(o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateWorkflowAB(pv, "acme/app", lc(older), cfg); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRunRefusesWorkflowABOnAnOlderBaseImage is workflowABSince's launch gate,
// on a checkout the run reads the file from: the judge is the workflow's build
// record, as it is for every other per-workflow image gate (checkImageSince),
// and the boundary is the constant's own. The refusal names the workflow's key
// and not the task's, which carries no ab block at all here.
func TestRunRefusesWorkflowABOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, workflowABSince, -1), shiftRelease(t, workflowABSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{workflowABSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+
				"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh }, ab: { reviewer_arm: l2 } }\n")
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+c.rel)
			_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
			if !c.refused {
				if err != nil {
					t.Fatalf("release %s: err = %v, want launched", c.rel, err)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want the launch refused", err)
			}
			for _, want := range []string{
				"fugaro.yaml's workflows.svc.ab of acme/other",
				"knows a workflow's ab: block (workflows.<name>.ab)",
				"fugaro " + workflowABSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				imageGateRemedy,
				"; or remove the ab: block from fugaro.yaml",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesANullWorkflowABBlockOnAnOlderBaseImage: `ab:` with nothing
// under it writes the key and decodes to no block at all, so the gate has to
// judge the key and not the block it holds — an older runner's KnownFields
// reader refuses the whole file either way, and a gate that read Workflow.AB
// alone would launch a run that dies at bootstrap on a line the operator did
// write (TestRunRefusesANullGovernanceBlockOnAnOlderBaseImage's case, on this
// key).
func TestRunRefusesANullWorkflowABBlockOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+
		"workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh }\n    ab:\n")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, workflowABSince, -1))
	wantRefused(t, f, "fugaro.yaml's workflows.svc.ab of acme/other needs a job image whose runner knows a workflow's ab: block (workflows.<name>.ab) (fugaro "+workflowABSince+" or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

// TestWorkflowABNamesReadTheKeyNotTheBlock: the presence of the key is the
// question the gates ask, and the text answers it where the decoded block
// cannot — an explicit null (`ab:`, `ab: null`, `ab: ~`) is one line to a
// runner older than workflowABSince, exactly as setsSecurity's null is to a
// runner older than securitySince. The three spellings resolve to no block at
// all, and every one of them is still named by workflowABNames and warned about
// by workflowABImageWarnings.
func TestWorkflowABNamesReadTheKeyNotTheBlock(t *testing.T) {
	const head = "version: 1\nproject: example\ngcp_project: example-1a2b3c\ngit: { provider: github }\nagent: { auth: api-key }\n"
	for _, wf := range []string{"    ab:\n", "    ab: null\n", "    ab: ~\n"} {
		data := []byte(head + "workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh }\n" + wf)
		cfg, _, ps := config.Resolve(data, nil)
		if len(ps) > 0 {
			t.Fatalf("%q: %+v, want the file valid", wf, ps)
		}
		if cfg.Workflows["svc"].AB != nil {
			t.Errorf("%q: the file decoded to a block, and this test is the null one", wf)
		}
		if got := workflowABNames(cfg); len(got) != 1 || got[0] != "svc" {
			t.Errorf("%q: workflowABNames = %v, want [svc]: the gate judges the key being written", wf, got)
		}
		if ps := workflowABImageWarnings(cfg); len(ps) != 1 || ps[0].Path != "workflows.svc.ab" {
			t.Errorf("%q: warnings = %+v, want one for workflows.svc.ab", wf, ps)
		}
	}
	// A file that writes no ab: key at all is gated by nothing, the other side
	// of the same read.
	cfg, _, ps := config.Resolve([]byte(head+"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"), nil)
	if len(ps) > 0 {
		t.Fatalf("%+v, want the file valid", ps)
	}
	if got := workflowABNames(cfg); len(got) != 0 {
		t.Errorf("workflowABNames = %v, want none for a file that writes no key", got)
	}
}

// validateWorkflowAB runs fugaro validate on a repository file whose workflows:
// block is workflows, in a directory that is no checkout and has no project
// config: the file's own key is all this judges. It is validateGrades' shape for
// this key, and the file is minimal otherwise so the arm's warning is the one
// read back.
func validateWorkflowAB(t *testing.T, workflows string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github }\n" +
		"agent: { auth: api-key }\nworkflows:\n" + workflows})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatWorkflowABNeedsEveryJobImage: the file is valid — the
// skew is the installation's, not the file's — so validate warns rather than
// refusing, the shape triageImageWarnings has, and names both ways to move an
// image. Each workflow that writes a block is warned about by its own key, and a
// file that writes none is warned about nothing. An ask that is not a review
// level is a Problem and not a warning, which is the other half: the key is
// admitted, its value is still held to the vocabulary.
func TestValidateWarnsThatWorkflowABNeedsEveryJobImage(t *testing.T) {
	svc := "  svc: { commands: { build: sh build.sh, test: sh test.sh }, ab: { reviewer_arm: l2 } }\n"
	doc, err := validateWorkflowAB(t, svc)
	if err != nil {
		t.Fatalf("err = %v, want a valid file", err)
	}
	if !has(doc.Warnings, "workflows.svc.ab", "needs fugaro "+workflowABSince) {
		t.Errorf("warnings = %+v, want the arm's warning", doc.Warnings)
	}
	for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
		if !has(doc.Warnings, "workflows.svc.ab", want) {
			t.Errorf("the warning does not name %q: %+v", want, doc.Warnings)
		}
	}
	if has(doc.Problems, "workflows.svc.ab", "") {
		t.Errorf("the key was refused, and the file is valid: %+v", doc.Problems)
	}
	// A sibling workflow's block is warned about as well, on its own path: the
	// job decodes the whole file whatever the launch selects.
	doc, err = validateWorkflowAB(t, svc+"  api: { commands: { build: sh build.sh, test: sh test.sh }, ab: { reviewer_arm: l3 } }\n")
	if err != nil {
		t.Fatalf("two blocks: err = %v, want a valid file", err)
	}
	for _, path := range []string{"workflows.svc.ab", "workflows.api.ab"} {
		if !has(doc.Warnings, path, "needs fugaro "+workflowABSince) {
			t.Errorf("two blocks: no warning for %s: %+v", path, doc.Warnings)
		}
	}
	if doc, err := validateWorkflowAB(t, "  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"); err != nil || has(doc.Warnings, "workflows.svc.ab", "") {
		t.Errorf("a workflow that writes no ab: block was warned about: %+v, %v", doc.Warnings, err)
	}
	doc, _ = validateWorkflowAB(t, "  svc: { commands: { build: sh build.sh, test: sh test.sh }, ab: { reviewer_arm: coder } }\n")
	if !has(doc.Problems, "workflows.svc.ab.reviewer_arm", "names the coder") {
		t.Errorf("an ask naming the coder was not refused: %+v", doc.Problems)
	}
}
