package cli

import (
	"encoding/json"
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
func catalogUpdateRig(t *testing.T) *imagesRig {
	t.Helper()
	r := newImagesRig(t, convergeRelease)
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
