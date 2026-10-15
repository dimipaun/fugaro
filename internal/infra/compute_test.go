package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// The compute root's contract (design compute-backend §8.1 task 6). The root
// is applied by a stage of its own; these tests hold the Go side of that
// interface to the HCL side, so the two cannot drift apart silently.

// computeSpec is a complete spec: every input the root requires.
func computeSpec() ComputeSpec {
	return ComputeSpec{
		Project:            "proj-1234",
		ProjectNumber:      "123456789",
		Region:             "us-east5",
		Network:            DefaultNetwork("proj-1234"),
		Subnetwork:         DefaultSubnetwork("proj-1234", "us-east5"),
		Names:              ComputeNamesFor(),
		ManageAPIs:         true,
		Launchers:          []string{"user:owner@example.com"},
		Operators:          []string{},
		LauncherRunGrants:  []LauncherRunGrant{{Member: "user:owner@example.com", Account: "fugaro-acme-web@proj-1234.iam.gserviceaccount.com"}},
		RunAccounts:        []string{"serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"},
		RegistryRepository: GhcrRegistry,
		BaseImage:          "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3",
		BaseProject:        "batch-custom-image",
		BaseFamily:         "batch-cos-stable-official",
		PreloadZone:        "us-east5-a",
		PreloadSchedule:    "0 3 * * 1",
		BuilderMachineType: "e2-medium",
		BuildSubmitterRole: "projects/proj-1234/roles/fugaroBuildSubmitter",
	}
}

// declaredVariables are the variables a root's variables.tf declares.
func declaredVariables(t *testing.T, path string) []string {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^variable\s+"([a-z0-9_]+)"\s*\{`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s declares no variables", path)
	}
	slices.Sort(out)
	return out
}

// REGRESSION (the tfvars interface): the keys ComputeVars writes are exactly
// the variables the compute root declares, no more and no fewer. A variable
// added to the root and not fed from Go fails here rather than in a plan that
// would silently take its default, and a Go field the root no longer declares
// fails too.
func TestComputeVarsMatchRootVariables(t *testing.T) {
	raw, err := ComputeVars(computeSpec())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range doc {
		got = append(got, k)
	}
	slices.Sort(got)
	want := declaredVariables(t, "gcp/roots/compute/variables.tf")
	if !slices.Equal(got, want) {
		t.Errorf("tfvars keys = %v, the root declares %v", got, want)
	}
}

// A spec that could only produce a failing apply fails here instead: no run
// account, a network or subnetwork left out (Batch refuses noExternalIpAddress
// without both), no repository to read the pinned image from, no
// build-submitter role.
func TestComputeVarsRefuseWhatTheModuleRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(s *ComputeSpec)
		want string
	}{
		{"no project", func(s *ComputeSpec) { s.Project = "" }, "project"},
		{"no region", func(s *ComputeSpec) { s.Region = "" }, "region"},
		{"no network", func(s *ComputeSpec) { s.Network = "" }, "network"},
		{"no subnetwork", func(s *ComputeSpec) { s.Subnetwork = "" }, "subnetwork"},
		{"no run accounts", func(s *ComputeSpec) { s.RunAccounts = nil }, "run account"},
		{"no repository", func(s *ComputeSpec) { s.RegistryRepository = "" }, "repository"},
		{"no build-submitter role", func(s *ComputeSpec) { s.BuildSubmitterRole = "" }, "build-submitter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := computeSpec()
			c.mut(&s)
			if _, err := ComputeVars(s); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ComputeVars = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// The run accounts the module is given must not be the default Compute Engine
// account: the design forbids it (§3.2), and an empty set is refused above, so
// what is left to check here is that the spec's own default is no account at
// all — a caller has to say which identities run.
func TestComputeSpecRunsAsNamedAccountsOnly(t *testing.T) {
	if s := computeSpec(); len(s.RunAccounts) == 0 {
		t.Fatal("the fixture names no run account")
	}
	var zero ComputeSpec
	if len(zero.RunAccounts) != 0 || zero.Names.ReaperAccountID != "" {
		t.Error("an unset spec names identities")
	}
}

// The compute root has a workdir of its own, beside the installation's, and
// it is a root PrepareWorkdir knows: the installation's tfvars, imports and
// saved plan must never be rewritten by this root's run.
func TestComputeWorkdirIsItsOwn(t *testing.T) {
	env := func(k string) string { return map[string]string{"HOME": "/home/x"}[k] }
	inst, err := InstallationWorkdir(env, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ComputeWorkdir(env, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	if dir == inst || filepath.Dir(dir) != filepath.Dir(inst) {
		t.Errorf("compute workdir %s is not a sibling of %s", dir, inst)
	}
	if StatePrefixCompute == StatePrefixInstallation || !strings.HasPrefix(StatePrefixCompute, "fugaro/") {
		t.Errorf("state prefix %q is not the root's own", StatePrefixCompute)
	}
	if _, err := ComputeWorkdir(env, "not a project"); err == nil {
		t.Error("a bad project id was accepted")
	}
	wd, err := PrepareWorkdir(filepath.Join(t.TempDir(), "compute"), "compute")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(wd.Root, filepath.Join("gcp", "roots", "compute")) {
		t.Errorf("PrepareWorkdir(compute) root = %s", wd.Root)
	}
}

// The preloaded image's name is the module's: one image per pinned base, and
// the Go side derives the same name the module does, so the stage can name the
// image it expects without reading the state back. The module's expression is
// asserted here too, because the two must stay the same function.
func TestPreloadedImageMatchesTheModule(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	tf := string(b)
	if !strings.Contains(tf, "substr(sha256(var.base_image), 0, 8)") {
		t.Error("the module no longer derives the image's name from the pinned base's hash: PreloadedImage would be wrong")
	}
	one := PreloadedImage("ghcr.io/acme/base:1")
	if one == PreloadedImage("ghcr.io/acme/base:2") {
		t.Error("two pins share one image name: a stale image would serve a new pin")
	}
	if !strings.HasPrefix(one, PreloadImage+"-") || len(one) != len(PreloadImage)+9 {
		t.Errorf("PreloadedImage = %q, want %s-<8 hex>", one, PreloadImage)
	}
	// The exact value, so a change to the derivation is a visible one.
	if got, want := one, "fugaro-preload-"+hash8("ghcr.io/acme/base:1"); got != want {
		t.Errorf("PreloadedImage = %q, want %q", got, want)
	}
}

// hash8 is the fixture's own sha256 prefix, computed the long way so the test
// does not restate the implementation it checks.
func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])
	return h[:8]
}

// The launcher's Condition and the reaper's sweep are both defined over the
// job prefix, and the module takes it as an input rather than writing it out:
// a prefix that drifted between the two would leave the launcher unable to
// create a job the reaper is looking for.
func TestJobPrefixIsTheModulesInput(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "jobs/${var.names.job_prefix}") {
		t.Error("the launcher's Condition no longer takes the job prefix from names.job_prefix")
	}
	if JobPrefix != "fugaro-" {
		t.Errorf("JobPrefix = %q: the job names are fugaro-<slug>-<workflow>-<hash> (internal/backend/batch/names.go)", JobPrefix)
	}
}
