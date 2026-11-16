package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestRunSecurityFlagRaisesTheLevel: --security writes the task's own request
// (task.Spec.Security), which is what the runner forces review level 3 from
// (SC10, D1517). The field is the whole of the flag's effect on the launch: the
// run's own resolution is what turns it into the level, and the second half
// here is that a launch which passes nothing carries nothing — a task
// byte-identical to what the previous release wrote, which is what keeps the
// flag from gating (and changing) every other launch.
func TestRunSecurityFlagRaisesTheLevel(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+securityRequestSince)
	const id = "20261112-120000-abcd"
	if _, _, err := execute(t, "run", "--run-id", id, "--security", "A task"); err != nil {
		t.Fatalf("--security: %v", err)
	}
	if spec := otherSpec(t, f, id); !spec.Security {
		t.Fatalf("--security left the task's security field off: %+v", spec)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the one launch", n)
	}
}

// TestRunWithoutSecurityWritesNoField: a launch that asks for nothing carries
// no field at all, so its task is byte-identical to what the release before
// this one wrote and no job image is judged for a request nobody made.
func TestRunWithoutSecurityWritesNoField(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	const id = "20261112-120000-abcd"
	if _, _, err := execute(t, "run", "--run-id", id, "A task"); err != nil {
		t.Fatal(err)
	}
	data, err := otherSpec(t, f, id).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "security") {
		t.Fatalf("a launch with no --security wrote the key anyway: %s", data)
	}
}

// TestRunRefusesTheSecurityRequestOnAnOlderBaseImage is
// securityRequestSince's launch gate for a workflow whose job image is a
// derived one: the request is a new task field, and a runner older than the
// release that reads it refuses the whole spec at bootstrap (task.Parse decodes
// with DisallowUnknownFields) — bytes in the bucket under a run that never
// starts. The judge is the workflow's build record, as it is for every other
// per-workflow image gate (checkImageSince), and the boundary is the constant's
// own: one release below is refused, the constant and one above pass.
func TestRunRefusesTheSecurityRequestOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, securityRequestSince, -1), shiftRelease(t, securityRequestSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{securityRequestSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+c.rel)
			_, _, err := execute(t, "run", "--run-id", "20261112-120000-abcd", "--security", "A task")
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
				securityRequestSubject,
				"knows " + securityRequestKnows,
				"fugaro " + securityRequestSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				imageGateRemedy,
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

// TestRunDoesNotGateASecurityRequestItWasNotGiven: the check is the field's and
// not the release the CLI happens to be — a launch on an old image that passes
// no --security carries no field for a runner to refuse, so it must launch. The
// image here is one release below the constant, so a gate that fired on
// anything but the field would refuse this launch.
func TestRunDoesNotGateASecurityRequestItWasNotGiven(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, securityRequestSince, -1))
	if _, _, err := execute(t, "run", "--run-id", "20261112-120000-abcd", "A task"); err != nil {
		t.Fatalf("the launch asks for nothing, so it must not be gated: %v", err)
	}
}

// TestRunRefusesTheSecurityRequestOnACloneWorkflowsOlderBaseImage: a checkout:
// clone workflow has no build record to judge (it runs the installation's base
// image as it is), so the same gate is judged against base_images.base itself —
// the shape every other request gate has in that branch.
func TestRunRefusesTheSecurityRequestOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, securityRequestSince, -1)
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	configSkewCheckout(t, f, configSkewHead+
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	_, _, err := execute(t, "run", "--run-id", "20261112-120000-abcd", "--security", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{securityRequestSubject, "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestSecurityRequestSinceHasAReleaseBullet: securityRequestSince names 0.23.0,
// which is not tagged, so docs/release.md's Before-you-tag list is what holds
// the constant to the release actually cut — the job every other *Since
// constant's bullet does (TestEscalationSinceHasAReleaseBullet). A release that
// landed the field elsewhere would otherwise keep refusing images that can run
// the request, or accept images that cannot.
func TestSecurityRequestSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `securityRequestSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no securityRequestSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+securityRequestSince+"\"`") {
		t.Errorf("docs/release.md's securityRequestSince bullet does not carry the constant's release (%q)", securityRequestSince)
	}
	for _, want := range []string{"--security", "checkSecurityRequestImage", "checkCloneSecurityRequestImage", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's securityRequestSince bullet does not say %q", want)
		}
	}
}

// TestRunRefusesTheLayersSecurityRequestOnAnOlderBaseImage is
// securityRequestSince's gate for the request's other home, the project layer's
// security.always_level_3 (D1503): a 0.22.0 job image decodes the key —
// securitySince's own gates admit it — and forces nothing, so a launch the
// switch governs would run every change of the project a level short in
// silence. The gate is judged on the configuration the run will read
// (launchLevelsConfig), which is where the layer is resolved, and the subject
// is the switch's rather than the task field's: a refusal that blamed the task
// would send an operator looking for a flag nobody passed. The boundary is the
// constant's own, as it is for every other image gate here.
func TestRunRefusesTheLayersSecurityRequestOnAnOlderBaseImage(t *testing.T) {
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{shiftRelease(t, securityRequestSince, -1), true},
		{securityRequestSince, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			isolateCache(t)
			publishedLayer(t, f, layerWithSecurity("security: { always_level_3: true }\n"))
			dir := layerCheckout(t, f, minimalAnchored)
			registerOtherLocally(t, f)
			commitRef(t, dir, "main")
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+c.rel)
			_, _, err := execute(t, "run", "--run-id", "20261112-120000-abcd", "A task")
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
				securitySwitchSubject,
				"knows " + securitySwitchKnows,
				"fugaro " + securityRequestSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				imageGateRemedy,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), securityRequestSubject) {
				t.Errorf("the refusal blames the task's own field, which this launch does not carry: %v", err)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesTheLayersSecurityRequestOnACloneWorkflowsOlderBaseImage: a
// checkout: clone workflow runs the installation's base image as it is, so the
// switch's gate is judged against base_images.base itself, the shape every
// other request gate has in that branch.
func TestRunRefusesTheLayersSecurityRequestOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithSecurity("security: { always_level_3: true }\n"))
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, securityRequestSince, -1)
	dir := layerCheckout(t, f, minimalAnchored+
		"workflows:\n  default: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	registerOtherLocally(t, f)
	// After registerOtherLocally, which appends its repos: entry: a top-level
	// key appended before an indented one leaves the file unparseable.
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	commitRef(t, dir, "main")
	_, _, err := execute(t, "run", "--run-id", "20261112-120000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{securitySwitchSubject, "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunToleratesAReviewRecipeUnderTheSecurityRequest: a mode: review recipe
// is the one run the request cannot be honoured by — it allows exactly one
// review step and nothing else (recipe.Parse), so level 3 has no step to run in
// — and both the launcher and the run drop the force for it rather than refuse
// the launch (launchReviewLevelsAsk, the runner's securityForcedLevels).
//
// The tolerance is the point and not a courtesy: `fugaro review` resolves
// review-only (reviewOnlyRecipe), so a project whose layer sets
// always_level_3 would otherwise refuse every review of every repository of it
// — the project owner's own included, with no flag that lifts it — and a
// launcher that refused while the run dropped the same force would make the
// runner's warning dead code for the only case it names. The launch goes
// through, the warning names the request and the reason, and the levels resolve
// as they would have without it.
//
// Both homes of the request are here: the task's field, which this launch
// carries because it passed --security, and the project layer's switch.
func TestRunToleratesAReviewRecipeUnderTheSecurityRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		layer   bool
		extra   []string
		subject string
	}{
		{"the task's field", false, []string{"--security"}, securityRequestSubject},
		{"the layer's always_level_3", true, nil, securitySwitchSubject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			workflow := "svc"
			if tc.layer {
				isolateCache(t)
				publishedLayer(t, f, layerWithSecurity("security: { always_level_3: true }\n"))
				dir := layerCheckout(t, f, minimalAnchored)
				registerOtherLocally(t, f)
				commitRef(t, dir, "main")
				workflow = config.ImplicitWorkflow
			} else {
				configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
			}
			// The recipe's own gate (recipeSince) and the layer's (layerSince)
			// are older than the constant, so an image on it passes both and
			// leaves the request's tolerance as the only thing under test.
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), workflow, "ghcr.io/dimipaun/fugaro-base:"+securityRequestSince)
			args := append([]string{"run", "--pr", "7", "--recipe", "review-only"}, tc.extra...)
			args = append(args, "Review the pull request")
			_, errOut, err := execute(t, args...)
			if err != nil {
				t.Fatalf("the launch must tolerate a request its recipe cannot hold: %v", err)
			}
			for _, want := range []string{"warning:", tc.subject, "mode: review", "review level 3"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("the warning lacks %q: %s", want, errOut)
				}
			}
			if n := len(f.run.Executions()); n != 1 {
				t.Errorf("%d executions, want the one this launch started", n)
			}
		})
	}
}
