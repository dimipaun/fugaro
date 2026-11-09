package cli

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The issue this file is about (0.21.0, #1843): a project's first catalog
// entry — `fugaro image add server` — leaves a local config whose image
// catalog has an entry this installation builds and no base_images.base. From
// then on a plain `fugaro update` refuses, before it plans anything: the
// catalog check job (D1148) runs FROM that image, and the run that recorded
// the catalog is the run that has to pin it. The user is not to be sent to a
// second command (the HARD RULE in CLAUDE.md): the pin is the images stage's,
// and the installation root's second apply — the one the history job already
// uses — deploys the job once the pin is there.

// catalogUpdateRig is an installation in that state: an installation already
// there (the state bucket, outputs that read back), a project config with one
// inline-sourced catalog entry and no base image recorded, and the registries
// the images stage reads, verifies and reads back through.
func catalogUpdateRig(t *testing.T) *imagesRig { return catalogUpdateRigAt(t, convergeRelease) }

// catalogUpdateRigAt is the same installation run by the build version (a
// development build pins nothing, and says so).
func catalogUpdateRigAt(t *testing.T, version string) *imagesRig {
	t.Helper()
	r := newImagesRig(t, version)
	// Every root matches its plan, so the run is about the pin and the job
	// alone: what the installation root plans is scripted per pass below.
	r.script["plan"] = map[string]any{"exit": 0}
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.save(t)
	// The entry has no repository of its own, so it is built by the project's
	// first repository — the rig's own acme/sandbox, which the config already
	// names with a workflow and a provider kind (newInitRig's initConfig).
	r.appendConfig(t, "image_catalog:\n  entries:\n    - name: server\n      source: { kind: inline, inline: \"FROM scratch\\n\" }\n")
	return r
}

// TestUpdatePinsTheCatalogBaseAndDeploysTheCheckJob: an installation that has
// just been given its first catalog entry reaches the next release with
// `fugaro update` alone. The images stage resolves, verifies and records the
// release's base image, and the installation root's second apply deploys the
// catalog check job from it — the job the catalog's entries need, and the one
// fact that made the run refuse before.
func TestUpdatePinsTheCatalogBaseAndDeploysTheCheckJob(t *testing.T) {
	r := catalogUpdateRig(t)
	// The two passes of the installation root are told apart by their plans:
	// the first one has nothing to deploy (the job's image is not pinned
	// yet), the second one deploys the job. A run that plans the root only
	// once leaves the catalog check job off the installation entirely.
	r.script["plan@installation"] = []map[string]any{
		{"exit": 0},
		{"exit": 2},
	}
	r.script["show@installation"] = map[string]any{"stdout": planJSON(t, change("module.installation.google_cloud_run_v2_job.catalog_check", "create"))}
	r.save(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("a project with an image catalog and no base image does not reach this release with `fugaro update` alone (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}

	// 1. The pin: what `fugaro update --base base` writes, written by the run
	// itself, through the remote repository, at the digest its signature was
	// verified for.
	pin := r.pinnedBase(config.BaseKind, convergeRelease)
	lc := r.localConfig(t)
	if got := lc.BaseImage(config.BaseKind); got != pin {
		t.Errorf("the local config records base_images.%s = %q, want %q", config.BaseKind, got, pin)
	}
	if r.resolveCalls == 0 {
		t.Error("the run recorded a base image without resolving the release's signed -pinned image and reading its digest back through the remote repository")
	}

	// 2. The job: deployed by the installation root again, running from the
	// pin, with the entry assigned to the repository that builds it.
	cc, _ := r.tfvars(t)["catalog_check"].(map[string]any)
	if cc == nil {
		t.Fatalf("the installation tfvars carry no catalog_check, so this run deployed an installation whose catalog is built by nothing: %v", r.tfvars(t))
	}
	if got := cc["image"]; got != pin {
		t.Errorf("the catalog check job runs from %v, want the base image the run pinned, %q", got, pin)
	}
	env, _ := cc["env"].(map[string]any)
	raw, _ := env[infra.CatalogCheckSpecEnv].(string)
	if raw == "" {
		t.Fatalf("the catalog check job's environment carries no %s: the job refuses to start without it", infra.CatalogCheckSpecEnv)
	}
	var spec struct {
		Catalog      *config.Catalog `json:"catalog"`
		Repositories []struct {
			Repo    string   `json:"repo"`
			Entries []string `json:"entries"`
		} `json:"repositories"`
		BaseImages map[string]string `json:"base_images"`
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("%s: %v", infra.CatalogCheckSpecEnv, err)
	}
	if spec.Catalog == nil || len(spec.Catalog.Entries) != 1 || spec.Catalog.Entries[0].Name != "server" {
		t.Errorf("the job's spec carries the catalog %+v, want the project's one entry", spec.Catalog)
	}
	if spec.BaseImages[config.BaseKind] != pin {
		t.Errorf("the job's spec builds FROM %q, want the pin %q", spec.BaseImages[config.BaseKind], pin)
	}
	var assigned []string
	for _, repo := range spec.Repositories {
		for _, name := range repo.Entries {
			assigned = append(assigned, repo.Repo+":"+name)
		}
	}
	if got := strings.Join(assigned, ","); got != "acme/sandbox:server" {
		t.Errorf("the job builds %q, want the project's first repository to build the entry", got)
	}

	// 3. And the run is the whole upgrade: the root's own stages converged,
	// and doctor's checks are the ones the fixture was built to have fire.
	for _, c := range doctorChecks(t, ".").Checks {
		if c.ID == "base-image" && !c.OK {
			t.Errorf("after fugaro update, doctor's base-image check still fires: %s (fix: %s)", c.Problem, c.Fix)
		}
	}
}

// TestUpdatePinsTheCatalogBaseForAPlainRunWithNoCheckout: the same state in a
// directory that is no checkout at all: the catalog is the config's, not a
// fugaro.yaml's, and the pin is still the run's own — the images stage's
// kinds are the flag's, the checkout's workflows' and, since #1843, the
// catalog's.
func TestUpdatePinsTheCatalogBaseForAPlainRunWithNoCheckout(t *testing.T) {
	r := catalogUpdateRig(t)
	if _, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("fugaro update refused a catalog whose base image it is about to pin: %v", err)
	}
	if got, want := r.localConfig(t).BaseImage(config.BaseKind), r.pinnedBase(config.BaseKind, convergeRelease); got != want {
		t.Errorf("base_images.%s is %q, want %q", config.BaseKind, got, want)
	}
}

// TestUpdateLeavesAnInstallationWithACurrentCatalogBaseAlone: the state the
// pin above leaves behind — a catalog, and the base image this release's own
// run would write — is the ordinary one: the images stage says No changes and
// the installation root is planned once, like every other installation. The
// second apply is the missing pin's, not every run's.
func TestUpdateLeavesAnInstallationWithACurrentCatalogBaseAlone(t *testing.T) {
	r := catalogUpdateRig(t)
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+r.pinnedBase(config.BaseKind, convergeRelease)+"}\n")
	r.save(t)
	if _, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("fugaro update refused an installation whose catalog base image is current: %v", err)
	}
	// One plan pass and no more: a second one is the run re-applying an
	// installation that has nothing to change.
	plans := 0
	for _, c := range r.calls(t) {
		if len(c) > 0 && c[0] == "plan" {
			plans++
		}
	}
	if plans != 1 {
		t.Errorf("the installation root was planned %d times, want once: a current base image needs no second apply", plans)
	}
	if _, ok := r.tfvars(t)["catalog_check"].(map[string]any); !ok {
		t.Errorf("the installation tfvars carry no catalog_check for a project whose catalog has an entry and a current base image: %v", r.tfvars(t))
	}
}

// TestUpdatePreviewNamesTheCatalogArmsSecondApply: the run's own preview is
// what the review screen is built from, and it has to name installation-2 —
// the stage the run applies the root again in *is* that second apply, and the
// person's one confirmation covers what the preview said.
//
// The state a preview reads is the config the run started with, which never
// holds the pin the run is about to make: on the first pin there is no
// base_images.base at all, and on a release upgrade it holds the older
// release's. So "does the config already record a base the installation's job
// does not run from" answers false in both, while the images stage's own
// decision answers true — the preview has to ask the second question.
func TestUpdatePreviewNamesTheCatalogArmsSecondApply(t *testing.T) {
	for _, tc := range []struct{ name, ver string }{
		{"first pin", ""},
		{"release upgrade", "0.20.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := catalogUpdateRigAt(t, convergeRelease)
			if tc.ver != "" {
				// The pin shape is the owner's, so the run has to be told the
				// owner it pinned under (the state the older release's run
				// left behind).
				r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+r.pinnedBase(config.BaseKind, tc.ver)+"}\n")
			}
			// An owned project shows the review screen, which is printed from
			// the preview alone: nothing is applied (--plan-only).
			r.markedRuns(t, initProjectName, initProject)
			fakeTerminal(t)
			r.save(t)
			out, errOut, err := executeStdin(t, "", "update", "--plan-only")
			if err != nil {
				t.Fatalf("update --plan-only: %v\n%s\n%s", err, out, errOut)
			}
			if !strings.Contains(out, "Review: project "+initProjectName) {
				t.Fatalf("the run showed no review screen to read:\n%s", out)
			}
			if !regexp.MustCompile(`(?m)^\s+\[will do\]\s+installation-2\b`).MatchString(out) {
				t.Errorf("the preview does not report installation-2 as work this run will do, so the second apply that deploys the catalog check job is not the one the user's confirmation covers:\n%s", out)
			}
			if !strings.Contains(out, "  installation-2: Terraform apply of the installation root again") {
				t.Errorf("the review screen omits installation-2, the stage whose apply deploys the catalog check job:\n%s", out)
			}
		})
	}
}

// catalogFlagBase is the image a --base-image flag names for base in these
// tests: an image of the operator's own, in a repository named for its kind
// (localcfg.BaseImageNameKind).
func catalogFlagBase() string {
	return "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/fugaro-base:dev-abc"
}

// TestUpdateDeploysTheCatalogCheckJobFromAFlaggedBase: the same installation
// with the operator's own base image named on the command line instead of
// pinned by the images stage. The config carries no base image until
// writeConfig records the flag after the first apply, so a decision made
// before any apply — the preview and the review screen the confirmation is —
// has to read the flag, not the config: the stage it reports as work is the
// one whose apply deploys the job from that image.
func TestUpdateDeploysTheCatalogCheckJobFromAFlaggedBase(t *testing.T) {
	r := catalogUpdateRig(t)
	// The two passes of the installation root: the first has nothing to deploy
	// (the config records no base image yet), the second deploys the job.
	r.script["plan@installation"] = []map[string]any{{"exit": 0}, {"exit": 2}}
	r.script["show@installation"] = map[string]any{"stdout": planJSON(t, change("module.installation.google_cloud_run_v2_job.catalog_check", "create"))}
	// An owned project asks once, by the project's name — the confirmation the
	// review screen is printed for.
	r.markedRuns(t, initProjectName, initProject)
	fakeTerminal(t)
	r.save(t)
	base := catalogFlagBase()
	// The name is typed once per apply of the installation root: the first
	// apply's own confirmation and this stage's, which isolates.
	out, errOut, err := executeStdin(t, names(2), "update", "--base-image", config.BaseKind+"="+base)
	if err != nil {
		t.Fatalf("a catalog project whose base image the run names itself does not converge with `fugaro update` alone (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if !regexp.MustCompile(`(?m)^\s+\[will do\]\s+installation-2\b`).MatchString(out) {
		t.Errorf("the preview reports installation-2 as skipped, though the run records the flag's base image after the first apply and applies the root again with it:\n%s", out)
	}
	if !strings.Contains(out, "  installation-2: Terraform apply of the installation root again") {
		t.Errorf("the review screen omits installation-2, so the apply that deploys the catalog check job is not one the user's confirmation named:\n%s", out)
	}
	if got := r.localConfig(t).BaseImage(config.BaseKind); got != base {
		t.Errorf("the local config records base_images.%s = %q, want the image the flag named, %q", config.BaseKind, got, base)
	}
	cc, _ := r.tfvars(t)["catalog_check"].(map[string]any)
	if cc == nil {
		t.Fatalf("the installation tfvars carry no catalog_check, so the run applied the root without the job the flagged base image is for: %v", r.tfvars(t))
	}
	if got := cc["image"]; got != base {
		t.Errorf("the catalog check job runs from %v, want the image the flag named, %q", got, base)
	}
}

// TestUpdateSaysWhenItCannotPinTheCatalogBase: a run that cannot pin the base
// image — a development build here, and any run that does not name base — has
// to say so. The catalog is the user's, its entries are buildable, and nothing
// deploys the check job that builds them: reporting the stage as skipped
// without a word
// about the base image would be an update that exits 0 in the issue's state.
func TestUpdateSaysWhenItCannotPinTheCatalogBase(t *testing.T) {
	r := catalogUpdateRigAt(t, "dev")
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("a development build's run: %v\n%s\n%s", err, out, errOut)
	}
	// The stage's reason is one clause on the screen (report.Brief) and the
	// whole of it in the log, which is where the base image is named.
	_, log := onlyLog(t, "update")
	whole := out + errOut + log
	for _, want := range []string{"installation-2", "image catalog builds its entries FROM base_images." + config.BaseKind, "this run does not pin"} {
		if !strings.Contains(whole, want) {
			t.Errorf("the run left the catalog's entries built by nothing without saying %q:\n%s", want, whole)
		}
	}
	if got := r.localConfig(t).BaseImage(config.BaseKind); got != "" {
		t.Errorf("a development build recorded base_images.%s = %q", config.BaseKind, got)
	}
}
