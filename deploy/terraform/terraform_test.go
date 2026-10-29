package terraform

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// TestProjectJobTftestTitlesAreTheOnesInternalInfraWrites: the project job's
// tftest supplies the two IAM condition titles as input variables and asserts
// the module passes them through, so a title internal/infra never writes is
// nothing but consistent with itself. The 2026-10-19 review found exactly that
// there: the bucket condition's title read "fugaro-project-job-runs", a string
// that occurs nowhere else in the repository, while internal/infra writes
// gcp.BucketConditionTitle(saID) = "fugaro-" + saID (the golden tfvars carry
// "fugaro-fugaro-p-standard-git-0ca96e64",
// internal/infra/testdata/installation-budget-job.tfvars.json) and
// internal/infra/tf's own plan review requires title ==
// BucketConditionTitlePrefix + <account id>, so a plan carrying the literal
// would have been rejected by it. Each title is derived here instead, from the
// account id of the block it sits in, through the two functions internal/infra
// builds them with.
func TestProjectJobTftestTitlesAreTheOnesInternalInfraWrites(t *testing.T) {
	const path = "gcp/roots/installation/tests/project_job.tftest.hcl"
	data, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	text := string(data)
	// An assignment, not a comparison: the same file asserts on
	// google_service_account.job.account_id == "..." and on a condition's own
	// title == "...", and those are the module's values read back, not the
	// variables this test is about.
	reAccount := regexp.MustCompile(`account_id\s*=\s*"([^"]+)"`)
	reBucketTitle := regexp.MustCompile(`bucket_condition\s*=\s*\{[^}]*?title\s*=\s*"([^"]+)"`)
	reTokenTitle := regexp.MustCompile(`token_condition\s*=\s*\{[^}]*?title\s*=\s*"([^"]+)"`)

	accounts := reAccount.FindAllStringSubmatchIndex(text, -1)
	if len(accounts) == 0 {
		t.Fatalf("%s declares no job account: the test file this checks is gone or reworded", path)
	}
	for n, a := range accounts {
		// Each block's own account id, its titles following it and the next
		// block's account id the boundary.
		end := len(text)
		if n+1 < len(accounts) {
			end = accounts[n+1][0]
		}
		sec := text[a[0]:end]
		id := text[a[2]:a[3]]
		bucket := reBucketTitle.FindStringSubmatch(sec)
		token := reTokenTitle.FindStringSubmatch(sec)
		if bucket == nil || token == nil {
			t.Errorf("%s: the block whose account id is %q declares no bucket_condition/token_condition title of its own (bucket %v, token %v): this test pairs each account with the titles that follow it, so a reworded block must not leave it pairing the wrong two", path, id, bucket, token)
			continue
		}
		if want := gcp.BucketConditionTitle(id); bucket[1] != want {
			t.Errorf("%s: the block whose account id is %q gives its bucket condition the title %q, want %q (gcp.BucketConditionTitle, the title internal/infra writes into the tfvars and its plan review requires): a plan carrying the other would be rejected by internal/infra/tf/cover.go, and nothing else in the repository spells that string", path, id, bucket[1], want)
		}
		if want := gcp.ProjectTokenBucketConditionTitle(id); token[1] != want {
			t.Errorf("%s: the block whose account id is %q gives its tokens/ condition the title %q, want %q (gcp.ProjectTokenBucketConditionTitle)", path, id, token[1], want)
		}
	}
}

// TestProjectJobTftestAppKeyNameIsTheOneInternalInfraWrites: the tftest run
// shared_job_mounting_the_app_key_keeps_the_accessor asserts the module's
// secretAccessor member by the App key's project-wide container name, exactly
// like the job names and condition titles above — and a name spelled here that
// internal/infra never writes would make that assertion consistent with itself
// and with nothing else. The installation's secrets map is where that name
// comes from (internal/infra/testdata/installation-accounts.tfvars.json's
// "github-app-key" entry), and internal/infra builds it with
// gcp.ProjectSecretID from the Fugaro project name.
//
// Mutation (run, restore): change one hex digit of the name in the tftest and
// this fails; change internal/infra's naming and the tftest's run fails at
// terraform test time, since the module would grant the name the map holds
// while the run asserts the literal.
func TestProjectJobTftestAppKeyNameIsTheOneInternalInfraWrites(t *testing.T) {
	const path = "gcp/roots/installation/tests/project_job.tftest.hcl"
	data, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// The run's own secrets map, so this pairs the literal with the run that
	// asserts it rather than with any other name in the file.
	re := regexp.MustCompile(`github-app-key\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("%s spells no github-app-key container: the run this test checks is gone or reworded, and the review's [low] item — the shared job account's retention, pinned by a run and not only by a plan paragraph — would be unguarded again", path)
	}
	want := gcp.ProjectSecretID("aurora", config.GitHubAppKeySecret)
	for _, m := range matches {
		if m[1] != want {
			t.Errorf("%s: the App key's project-wide container is %q, want %q (gcp.ProjectSecretID for the tftest's fugaro_project and config.GitHubAppKeySecret): the installation's secrets map holds the latter, so a member asserted by the former is a grant the plan would never create", path, m[1], want)
		}
	}
}

// The embedded tree is what fugaro update writes into its workdir, so every
// root, module and lock file it needs must be in it. Each task that adds a
// root or module adds its paths here.
func TestEmbeddedTree(t *testing.T) {
	for _, p := range []string{
		"gcp/.tflint.hcl",
		"gcp/modules/installation/versions.tf",
		"gcp/modules/installation/variables.tf",
		"gcp/modules/installation/outputs.tf",
		"gcp/roots/installation/main.tf",
		"gcp/roots/installation/variables.tf",
		"gcp/roots/installation/outputs.tf",
		"gcp/roots/installation/.terraform.lock.hcl",
		"gcp/roots/installation/tests/installation.tftest.hcl",
		"gcp/modules/project/versions.tf",
		"gcp/modules/project/variables.tf",
		"gcp/modules/project/sa.tf",
		"gcp/modules/project/job.tf",
		"gcp/modules/project/iam.tf",
		"gcp/modules/project/outputs.tf",
		"gcp/modules/repo/versions.tf",
		"gcp/modules/repo/variables.tf",
		"gcp/modules/repo/secrets.tf",
		"gcp/modules/repo/registry.tf",
		"gcp/modules/repo/build.tf",
		"gcp/modules/repo/check.tf",
		"gcp/modules/repo/workflows.tf",
		"gcp/modules/repo/outputs.tf",
		"gcp/modules/workflow/versions.tf",
		"gcp/modules/workflow/variables.tf",
		"gcp/modules/workflow/sa.tf",
		"gcp/modules/workflow/job.tf",
		"gcp/modules/workflow/outputs.tf",
		"gcp/roots/repo/main.tf",
		"gcp/roots/repo/variables.tf",
		"gcp/roots/repo/outputs.tf",
		"gcp/roots/repo/.terraform.lock.hcl",
		"gcp/roots/repo/tests/repo.tftest.hcl",
		"gcp/roots/repo/tests/testdata/bitbucket-oauth.tfvars.json",
		// The shared project job's plan test (design project-centric §4.1,
		// plan tasks 3.2 and 3.5), in the installation root's own test
		// directory beside installation.tftest.hcl: what a task that adds a
		// root's test file owes this list.
		"gcp/roots/installation/tests/project_job.tftest.hcl",
		"gcp/roots/repo/tests/testdata/github-vertex.tfvars.json",
		"gcp/modules/installation/history.tf",
		"gcp/modules/installation/coordinator.tf",
		"gcp/modules/firebase/versions.tf",
		"gcp/modules/firebase/variables.tf",
		"gcp/modules/firebase/apis.tf",
		"gcp/modules/firebase/firebase.tf",
		"gcp/modules/firebase/iam.tf",
		"gcp/modules/firebase/outputs.tf",
		"gcp/roots/firebase/main.tf",
		"gcp/roots/firebase/variables.tf",
		"gcp/roots/firebase/outputs.tf",
		"gcp/roots/firebase/.terraform.lock.hcl",
		"gcp/roots/firebase/tests/firebase.tftest.hcl",
		// The Batch backend's installation-level provisioning (design
		// compute-backend §8.1 task 6). Its build is a script, not HCL: the
		// wait in the middle of it has no Terraform resource.
		"gcp/modules/compute/versions.tf",
		"gcp/modules/compute/variables.tf",
		"gcp/modules/compute/apis.tf",
		"gcp/modules/compute/iam.tf",
		"gcp/modules/compute/network.tf",
		"gcp/modules/compute/preload.tf",
		"gcp/modules/compute/preload.sh",
		// The reaper's invocation (design compute-backend §8.1 task 11): the
		// module file with it, the way history.tf is listed above.
		"gcp/modules/compute/reaper.tf",
		"gcp/modules/compute/outputs.tf",
		"gcp/roots/compute/main.tf",
		"gcp/roots/compute/variables.tf",
		"gcp/roots/compute/outputs.tf",
		"gcp/roots/compute/.terraform.lock.hcl",
		"gcp/roots/compute/tests/compute.tftest.hcl",
	} {
		if _, err := fs.Stat(FS, p); err != nil {
			t.Errorf("embedded tree lacks %s: %v", p, err)
		}
	}
	// Nothing a local terraform init leaves behind may be embedded.
	walk(t, func(path string, _ []byte) {
		if strings.Contains(path, "/.terraform/") || strings.HasSuffix(path, ".tfstate") {
			t.Errorf("embedded tree holds %s, a local terraform artifact", path)
		}
	})
}

// No secret value may ever reach Terraform or its state: secrets are
// containers only, and values are added with fugaro secrets set.
func TestNoSecretVersionsInTerraform(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, bad := range []string{"google_secret_manager_secret_version", "google_secret_manager_regional_secret_version", "secret_data"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %s", path, bad)
			}
		}
	})
}

// Shared things are only ever granted on additively, never with a resource
// that owns the whole policy or binding, whatever the resource type
// (project, folder, organization, service account, bucket, registry, job...).
func TestNoAuthoritativeIAM(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, blk := range allResourceBlocks(t, path, b) {
			if authoritativeIAMRE.MatchString(blk.typ) {
				t.Errorf("%s: %s is authoritative IAM (binding, policy or audit config)", path, blk.name)
			}
		}
	})
}

// Secret access is granted per secret, never on the whole project. A plan
// assertion can't enumerate every resource, so this is a text check.
func TestNoProjectLevelSecretAccessor(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if strings.Contains(blk.body, "secretmanager.secretAccessor") {
				t.Errorf("%s: %s grants secretmanager.secretAccessor on the project", path, blk.name)
			}
		}
	})
}

// The tag mover role lets a repository's build move its own :latest, which
// needs artifactregistry.tags.delete, and nothing more: no version or
// package delete. It is granted on a repository's own registry only, never
// on the project.
func TestTagMoverRoleIsTagsDeleteOnly(t *testing.T) {
	roles, grants := 0, 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_custom_role") {
			if blk.name != "google_project_iam_custom_role.tag_mover" {
				continue
			}
			roles++
			m := regexp.MustCompile(`(?s)\n\s*permissions\s*=\s*\[(.*?)\]`).FindStringSubmatch(blk.body)
			if m == nil {
				t.Errorf("%s: %s has no literal permissions list", path, blk.name)
				continue
			}
			var perms []string
			for _, p := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
				perms = append(perms, p[1])
			}
			if fmt.Sprint(perms) != "[artifactregistry.tags.delete]" {
				t.Errorf("%s: %s holds %v, want exactly artifactregistry.tags.delete", path, blk.name, perms)
			}
		}
		for _, typ := range []string{"google_project_iam_member", "google_artifact_registry_repository_iam_member"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				if !strings.Contains(blk.body, "tag_mover") {
					continue
				}
				grants++
				if typ != "google_artifact_registry_repository_iam_member" || path != "gcp/modules/repo/build.tf" ||
					!strings.Contains(blk.body, "google_artifact_registry_repository.images.repository_id") {
					t.Errorf("%s: %s grants the tag mover role other than on the repository's own registry", path, blk.name)
				}
			}
		}
	})
	if roles != 1 || grants != 1 {
		t.Errorf("found %d tag mover roles and %d grants, want 1 of each", roles, grants)
	}
}

// The base images are copied into fugaro-base by fugaro update, as the person running
// it (design m11 §3.3): a build account must never be able to write that
// registry, since the image every repository's credential step runs comes
// from it. So the only grant on the base registry that is not a reader's is
// the operators' writer role, and no grant of any kind to a service account
// writes it.
func TestMirrorRunsAsUserNotBuildAccount(t *testing.T) {
	writers := 0
	walk(t, func(path string, b []byte) {
		// Nor may anything grant a registry write role on the whole project,
		// which would cover fugaro-base.
		for _, typ := range []string{"google_project_iam_member", "google_project_iam_binding"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				if regexp.MustCompile(`roles/artifactregistry\.(writer|admin|repoAdmin)`).MatchString(blk.body) {
					t.Errorf("%s: %s grants an Artifact Registry write role on the whole project", path, blk.name)
				}
			}
		}
		for _, typ := range []string{"google_artifact_registry_repository_iam_member", "google_artifact_registry_repository_iam_binding", "google_artifact_registry_repository_iam_policy"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				onBase := strings.Contains(blk.body, "repository.base.") || strings.Contains(blk.body, "base_registry") || strings.Contains(blk.body, `"fugaro-base"`)
				if !onBase {
					continue
				}
				switch {
				case hasAttr(blk.body, "role", `"roles/artifactregistry.reader"`):
				case blk.name == typ+".base_writer" && path == "gcp/modules/installation/iam.tf" && typ == "google_artifact_registry_repository_iam_member" &&
					strings.Contains(blk.body, "toset(var.operators)") && hasAttr(blk.body, "member", "each.value"):
					writers++
				default:
					t.Errorf("%s: %s grants something other than reader on the base registry to something other than the operators", path, blk.name)
				}
			}
		}
	})
	if writers != 1 {
		t.Errorf("found %d operator writer grants on the base registry, want exactly 1", writers)
	}
}

// TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr: the installation
// owns a Docker remote repository over https://ghcr.io, in the project it
// installs into, named like every other fugaro registry, unauthenticated
// (the packages are public), without cleanup policies (design §7), with
// prevent_destroy (the migration never deletes it), and with upstream
// validation left on (design §2.3) so a typo fails the apply.
//
// Assertions use this file's own helpers, resourceBlocks and hasAttr —
// there is no HCL decoder here, and adding one would be a new dependency
// in a test that only needs to read our own HCL (TestResourceBlocks at
// :288 is the model).
func TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr(t *testing.T) {
	found := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_artifact_registry_repository") {
			if blk.name != "google_artifact_registry_repository.ghcr" {
				continue
			}
			found++
			if !hasAttr(blk.body, "mode", `"REMOTE_REPOSITORY"`) ||
				!hasAttr(blk.body, "format", `"DOCKER"`) ||
				!hasAttr(blk.body, "repository_id", "var.names.ghcr_registry") ||
				!hasAttr(blk.body, "uri", `"https://ghcr.io"`) {
				t.Errorf("%s: %s is not the remote repository (design §3.1)", path, blk.name)
			}
			if strings.Contains(blk.body, "cleanup_policies") {
				t.Errorf("%s: %s must carry no cleanup policies (design §7)", path, blk.name)
			}
			// Validation is left at its default: an unset attribute, so
			// assert the absence of the escape hatch, not its value.
			if strings.Contains(blk.body, "disable_upstream_validation") {
				t.Errorf("%s: %s must not disable upstream validation (design §2.3)", path, blk.name)
			}
			if !hasAttr(blk.body, "labels", `{ fugaro = "managed" }`) {
				t.Errorf("%s: %s must carry fugaro=managed", path, blk.name)
			}
		}
	})
	if found != 1 {
		t.Errorf("found %d google_artifact_registry_repository.ghcr blocks, want exactly 1", found)
	}
}

// TestRemoteRepositoryReaderGrants: exactly two reader grants on the new
// remote repository, and nothing else. The operators' (the run that pins
// reads the verified digest back through the proxy with their own
// credentials, design §3.2) and each repository's build account's (Cloud
// Build pulls the derived build's step image and its FROM through it,
// design §6). Neither writes it: the packages are public, so nothing in
// the repository is ever written (§2.3, §5.2).
//
// Both must name the remote repository and nothing that matches
// TestMirrorRunsAsUserNotBuildAccount's own substring test for "on the
// base registry" — that test reads a grant as being on fugaro-base when
// its body contains "base_registry" or "repository.base.".
// The count is the whole point, so it is taken over EVERY iam_member whose
// body names ghcr rather than over the two blocks by name: a third grant
// added anywhere — a writer, a second reader, a grant on another resource
// that happens to name the repository — is a failure here.
func TestRemoteRepositoryReaderGrants(t *testing.T) {
	want := map[string]string{ // "path|resource" -> the member it must grant
		"gcp/modules/installation/iam.tf|google_artifact_registry_repository_iam_member.ghcr_reader": "toset(var.operators)",
		"gcp/modules/repo/build.tf|google_artifact_registry_repository_iam_member.build_ghcr":        "google_service_account.build.member",
	}
	seen := map[string]bool{}
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_artifact_registry_repository_iam_member") {
			if !strings.Contains(blk.body, "ghcr") {
				continue
			}
			id := path + "|" + blk.name
			member, known := want[id]
			if !known {
				t.Errorf("%s: %s grants on the remote repository and is not one of the two known grants: the remote repository is read-only (design §5.2)", path, blk.name)
				continue
			}
			seen[id] = true
			switch {
			case !hasAttr(blk.body, "role", `"roles/artifactregistry.reader"`):
				t.Errorf("%s: %s must be a reader grant: neither the operator's read-back nor the build account writes the remote repository (design §5.2)", path, blk.name)
			case !strings.Contains(blk.body, member):
				t.Errorf("%s: %s must grant %s", path, blk.name, member)
			case strings.Contains(blk.body, "base_registry") || strings.Contains(blk.body, "repository.base.") || strings.Contains(blk.body, `"fugaro-base"`):
				t.Errorf("%s: %s names the base registry: the remote repository's grant must name ghcr_registry and nothing else", path, blk.name)
			}
		}
	})
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: no such grant", id)
		}
	}
}

// Destroying the Terraform config must never turn off an API the project
// (or someone else in it) still uses.
func TestProjectServicesKeepOnDestroy(t *testing.T) {
	n := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_service") {
			n++
			for _, attr := range []string{"disable_on_destroy", "disable_dependent_services"} {
				if !hasAttr(blk.body, attr, "false") {
					t.Errorf("%s: %s lacks %s = false", path, blk.name, attr)
				}
			}
		}
	})
	if n == 0 {
		t.Error("no google_project_service block found")
	}
}

// The secrets, the runs bucket and every registry hold data a plan must
// never delete, so each carries prevent_destroy.
func TestPreventDestroy(t *testing.T) {
	counts := map[string]int{}
	walk(t, func(path string, b []byte) {
		for _, typ := range []string{
			"google_secret_manager_secret", "google_secret_manager_regional_secret",
			"google_storage_bucket", "google_artifact_registry_repository",
		} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				counts[typ]++
				if !hasAttr(blk.body, "prevent_destroy", "true") {
					t.Errorf("%s: %s lacks prevent_destroy = true", path, blk.name)
				}
			}
		}
	})
	for _, typ := range []string{"google_secret_manager_secret", "google_storage_bucket", "google_artifact_registry_repository"} {
		if counts[typ] == 0 {
			t.Errorf("no %s block found", typ)
		}
	}
}

// The service holds no data of its own, and the container beside it is the
// one thing worth keeping, so turning the opt-in off is NOT "a removal a plan
// may make": with the container in state, `credentials_service = null` fails
// the whole plan ("Instance cannot be destroyed") and applies nothing, and the
// service, its account and its grant cannot be removed in that plan either.
// Opting out is two owner steps — forget the container with `terraform state
// rm` (which deletes nothing in GCP), then an ordinary plan that removes the
// service — and C1.22's runbook carries them. This test pins the file's
// account of that, because the first version of the comment claimed the
// opposite (independent security review of this file, M2) and nothing else in
// the tree states the order. deletion_protection = false is what makes step
// two possible; the container's prevent_destroy is what keeps a credential
// from being deleted by a plan. The first step's address is pinned exactly as
// an owner types it — module-prefixed and quoted (re-review N1), because the
// first version of it named a resource the root's state does not hold.
func TestCredentialsServiceOptOutIsTwoSteps(t *testing.T) {
	const path = "gcp/modules/installation/credentials.tf"
	data, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	text := string(data)
	// The address carries the module prefix, because the container is a
	// resource of `module "installation"` (roots/installation/main.tf) and not
	// of the root: `terraform state rm` on the bare address names nothing in
	// the root's state and is refused. internal/infra/forget.go's
	// LogBucketAddress and TagMoverRoleAddress are the same shape for the same
	// root. It is single-quoted because `[0]` is a glob to zsh, which fails the
	// command with "no matches found" rather than passing it through
	// (independent security review of this file, re-review N1).
	const step = "terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'"
	if !strings.Contains(text, step) {
		t.Errorf("%s does not document the opt-out's first step (%q): without it a reader is left with the plan failure and no path", path, step)
	}
	// The two forms that must not come back: the unprefixed address (the
	// review's N1 — it names nothing in the root), and the prefixed one
	// unquoted (zsh refuses it before terraform ever sees it).
	for _, wrong := range []string{
		"terraform state rm google_secret_manager_secret.credentials_bundle[0]",
		"terraform state rm module.installation.google_secret_manager_secret.credentials_bundle[0]",
	} {
		if strings.Contains(text, wrong) {
			t.Errorf("%s carries the opt-out's first step as %q: the address needs the module prefix and single quotes, %q", path, wrong, step)
		}
	}
	if strings.Contains(text, "is therefore a removal a plan may make") || strings.Contains(text, "removal a plan may make, not a refusal") {
		t.Errorf("%s still claims the opt-out is a removal a plan may make: it is not, the container's prevent_destroy refuses the whole plan", path)
	}
	// The two installation steps are not the whole opt-out: the invoker grants
	// and the dropped model-secret accessors are in the REPOSITORY roots, so a
	// reader who stops here leaves every one of them declaring a service that
	// is gone (re-review N2). The file has to say so.
	const repoStep = "re-applied too, and before these two steps"
	if !strings.Contains(text, repoStep) {
		t.Errorf("%s does not say that every repository root is re-applied as part of the opt-out (%q): the invoker grants and the dropped model-secret accessors live in the repository roots, and one left declaring a service that is gone cannot apply", path, repoStep)
	}
	// The two lifecycle settings the two steps depend on.
	containers := resourceBlocks(t, path, data, "google_secret_manager_secret")
	if len(containers) != 1 || !hasAttr(containers[0].body, "prevent_destroy", "true") {
		t.Errorf("%s: the bundle container must be exactly one secret with prevent_destroy = true (got %d blocks), which is what makes the state rm step necessary", path, len(containers))
	}
	services := resourceBlocks(t, path, data, "google_cloud_run_v2_service")
	if len(services) != 1 || !hasAttr(services[0].body, "deletion_protection", "false") {
		t.Errorf("%s: the service must be exactly one Cloud Run service with deletion_protection = false (got %d blocks), so the second step can remove it", path, len(services))
	}
}

// The token signer holds no roles, and no run's job or build account is
// granted anything on the Firebase project — the one account of a job's that
// is, the coordinator's writer, is granted the FP's database admin by name,
// and only once coordinator_account is set (coordinator plan task P3; the
// account is created in P5). The plan assertions check the values;
// this text check catches a grant written to either by reference, which no
// input could ever reveal.
func TestFirebaseModuleGrantsNothingToSignerOrJobs(t *testing.T) {
	n := 0
	walk(t, func(path string, b []byte) {
		if !strings.HasPrefix(path, "gcp/modules/firebase/") {
			return
		}
		n++
		code := stripComments(string(b))
		for _, typ := range []string{"google_project_iam_member", "google_service_account_iam_member", "google_project_iam_custom_role"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				for _, bad := range []string{"google_service_account.signer.member", "google_service_account.signer.email", "serviceAccount:", "roles/storage", "roles/run."} {
					if strings.Contains(blk.body, bad) && !(bad == "serviceAccount:" && (strings.Contains(blk.name, "history_") || strings.Contains(blk.name, "coordinator_"))) {
						t.Errorf("%s: %s mentions %s", path, blk.name, bad)
					}
				}
			}
		}
		for _, bad := range []string{"fugaro-b-", "google_service_account.job", "google_service_account.build", "google_service_account.scheduler"} {
			if strings.Contains(code, bad) {
				t.Errorf("%s mentions %s: job, build and scheduler accounts hold nothing on the FP", path, bad)
			}
		}
	})
	if n == 0 {
		t.Error("no file under gcp/modules/firebase")
	}
}

// Nobody may act as the history account (it holds firebasedatabase.admin on
// the FP): no iam member in the installation module names it, and none grants
// serviceAccountUser or serviceAccountTokenCreator on it.
func TestNoActAsOnHistoryAccount(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_service_account_iam_member") {
			if strings.Contains(blk.body, "google_service_account.history") || strings.Contains(blk.name, "history") {
				t.Errorf("%s: %s grants on the history account", path, blk.name)
			}
		}
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			for _, bad := range []string{"serviceAccountUser", "serviceAccountTokenCreator"} {
				if strings.Contains(blk.body, bad) {
					t.Errorf("%s: %s grants %s on the project", path, blk.name, bad)
				}
			}
		}
	})
}

func TestResourceBlocks(t *testing.T) {
	src := `
# resource "google_storage_bucket" "commented" { }
resource "google_storage_bucket" "a" {
  name = "x-${var.y}-}" # a brace } in a comment
  /* resource "google_storage_bucket" "nested" { */
  lifecycle {
    prevent_destroy = true
  }
}
resource "google_storage_bucket" "b" {
  name = format("%s", "{")
}
resource "google_storage_bucket_iam_member" "c" {
  role = "x"
}
`
	blks := resourceBlocks(t, "src.tf", []byte(src), "google_storage_bucket")
	if len(blks) != 2 || blks[0].name != "google_storage_bucket.a" || blks[1].name != "google_storage_bucket.b" {
		t.Fatalf("blocks = %+v", blks)
	}
	if !hasAttr(blks[0].body, "prevent_destroy", "true") || hasAttr(blks[1].body, "prevent_destroy", "true") {
		t.Errorf("prevent_destroy detection is wrong: %+v", blks)
	}
}

// walk calls fn with every Terraform source file in the embedded tree.
func walk(t *testing.T, fn func(path string, b []byte)) {
	t.Helper()
	n := 0
	err := fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".tf") && !strings.HasSuffix(path, ".hcl") {
			return nil
		}
		b, err := fs.ReadFile(FS, path)
		if err != nil {
			return err
		}
		n++
		fn(path, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("the embedded tree holds no Terraform files")
	}
}

type block struct {
	typ  string
	name string // type.name
	body string // the text between the block's braces, comments removed
}

var resourceHeader = regexp.MustCompile(`(?m)^\s*resource\s+"([a-z0-9_]+)"\s+"([A-Za-z0-9_-]+)"\s*\{`)

// resourceBlocks returns the resource blocks of type typ in src. It removes
// comments, then matches braces while skipping string literals (and the
// interpolations inside them), which is enough for our own HCL.
func resourceBlocks(t *testing.T, path string, src []byte, typ string) []block {
	t.Helper()
	code := stripComments(string(src))
	var out []block
	for _, m := range resourceHeader.FindAllStringSubmatchIndex(code, -1) {
		if code[m[2]:m[3]] != typ {
			continue
		}
		open := m[1] - 1
		end := matchBrace(code, open)
		if end < 0 {
			t.Fatalf("%s: unbalanced braces in %s.%s", path, typ, code[m[4]:m[5]])
		}
		out = append(out, block{typ: typ, name: typ + "." + code[m[4]:m[5]], body: code[open+1 : end]})
	}
	return out
}

// hasAttr reports whether body sets attr to the literal value val.
func hasAttr(body, attr, val string) bool {
	return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(attr) + `\s*=\s*` + regexp.QuoteMeta(val) + `\s*$`).MatchString(body)
}

// stripComments blanks out #, // and /* */ comments outside strings,
// keeping newlines so line anchors still work.
func stripComments(s string) string {
	var b strings.Builder
	inStr, depth := false, 0 // depth counts open ${ inside a string
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr && c == '\\' && i+1 < len(s):
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		case inStr && depth == 0 && c == '"':
			inStr = false
		case inStr && c == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			b.WriteString("${")
			i++
			continue
		case inStr && depth > 0 && c == '}':
			depth--
		case !inStr && c == '"':
			inStr = true
		case !inStr && (c == '#' || (c == '/' && i+1 < len(s) && s[i+1] == '/')):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
			continue
		case !inStr && c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				if s[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// matchBrace returns the index of the brace closing the one at open, in
// comment-free code, skipping string literals.
func matchBrace(code string, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '"':
			i = skipString(code, i)
			if i < 0 {
				return -1
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// skipString returns the index of the quote closing the string opening at
// i, stepping over escapes and ${ } interpolations (which may hold strings).
func skipString(code string, i int) int {
	for j := i + 1; j < len(code); j++ {
		switch code[j] {
		case '\\':
			j++
		case '"':
			return j
		case '$':
			if j+1 < len(code) && code[j+1] == '{' {
				depth := 0
				for k := j + 1; k < len(code); k++ {
					if code[k] == '"' {
						if k = skipString(code, k); k < 0 {
							return -1
						}
						continue
					}
					if code[k] == '{' {
						depth++
					} else if code[k] == '}' {
						if depth--; depth == 0 {
							j = k
							break
						}
					}
				}
			}
		}
	}
	return -1
}

// The alert and the log exclusion pick out the check jobs by a name
// prefix, which must be the one gcp.CheckJobName gives them.
func TestCheckJobPrefixMatchesGo(t *testing.T) {
	re := regexp.MustCompile(`job_name=~\\"\^([a-z0-9-]+)\\"`)
	seen := 0
	walk(t, func(path string, b []byte) {
		for _, m := range re.FindAllSubmatch(b, -1) {
			seen++
			for _, slug := range []string{"bitbucket-acme-sandbox", strings.Repeat("a", 80)} {
				if name := gcp.CheckJobName(slug); !strings.HasPrefix(name, string(m[1])) {
					t.Errorf("%s matches check jobs by %q, but the check job of %s is %s", path, m[1], slug, name)
				}
			}
		}
	})
	if seen < 3 {
		t.Errorf("found %d check-job name filters, want the alert's two and the exclusion's", seen)
	}
}

// The installation's log sink and its _Default exclusion carry one arm per
// platform a Fugaro-managed job's lines can come from (design
// docs/design/compute-backend.md §1.5, §8.1 task 4): Cloud Run's
// cloud_run_job, which every installation's sink and exclusion already
// filter on, and Cloud Batch's batch.googleapis.com/Job, without which a
// Batch run's lines sit in _Default where every project viewer reads them.
//
// Four things this pins, none of which a plan test can see as a string:
//
//   - the Cloud Run arm is byte-for-byte what it was before the Batch arm
//     existed. An installation's live sink filters on that text, and an arm
//     that drifted by a byte would move lines the installation never meant
//     to route (or stop routing its own);
//   - the Batch arm is exactly the documented one -- the resource type Batch
//     gives a job's entries, pinned to the installation's own marker;
//   - the rendered sink and exclusion filters, exactly, parenthesis for
//     parenthesis;
//   - and that neither filter mixes AND with OR at one parenthesis level.
//     Cloud Logging's query language does not group them the way most
//     languages do -- NOT has the highest precedence, "followed by OR and
//     AND in that order" (Logging query language, "Boolean operators"), so
//     AND binds *loosest* -- and a filter that mixes them at one level then
//     means something its author probably did not intend: `A1 AND A2 OR B1
//     AND B2` is `A1 AND (A2 OR B1) AND B2`, which matches only the Cloud Run
//     arm, so the Batch arm would route and exclude nothing and a Batch run's
//     task lines would sit in _Default where every project viewer reads them.
//     A filter where every level is all-AND or all-OR means the same thing
//     under either precedence, which is the property pinned here.
//
// Who can read the lines does NOT change: the arm routes into the same
// bucket, and the view's IAM member is pinned here to the launchers and
// operators it already named, so a second arm cannot widen the audience.
func TestLogSinkRoutesBothPlatforms(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/installation/logging.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	arm := func(name string) string {
		t.Helper()
		m := regexp.MustCompile(`(?m)^\s*` + name + `\s*=\s*"((?:[^"\\]|\\.)*)"\s*$`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("logging.tf has no %s local", name)
		}
		return m[1]
	}
	// The Cloud Run arm, byte for byte. This string is the one every
	// installation's sink and exclusion already carries; it is written out
	// here rather than derived, so that a change to it fails this test.
	const cloudRunArm = `resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\"`
	const batchArm = `resource.type=\"batch.googleapis.com/Job\" AND labels.\"fugaro\"=\"managed\"`
	if got := arm("fugaro_cloud_run_logs"); got != cloudRunArm {
		t.Errorf("the Cloud Run arm = %s\nwant                  %s\nthe Cloud Run arm must stay byte-for-byte unchanged", got, cloudRunArm)
	}
	if got := arm("fugaro_batch_logs"); got != batchArm {
		t.Errorf("the Batch arm = %s\nwant            %s", got, batchArm)
	}
	// How the arms are joined, in the source.
	if got, want := arm("fugaro_logs"), "((${local.fugaro_cloud_run_logs}) OR (${local.fugaro_batch_logs}))"; got != want {
		t.Errorf("the combined filter = %s\nwant                  %s\nthe arms must be one group with each arm parenthesized: Cloud Logging binds AND loosest (NOT, then OR, then AND)", got, want)
	}
	// The two rendered filters, as Cloud Logging receives them: the local
	// substituted in, and the HCL's \" unescaped back to the " the API sees.
	// Written out rather than derived, so any change to either filter fails
	// here.
	unescape := func(s string) string { return strings.ReplaceAll(s, `\"`, `"`) }
	render := func(s string) string {
		s = strings.ReplaceAll(s, "${local.fugaro_cloud_run_logs}", cloudRunArm)
		return strings.ReplaceAll(s, "${local.fugaro_batch_logs}", batchArm)
	}
	sinkFilter := unescape(render(arm("fugaro_logs")))
	wantSink := "((" + unescape(cloudRunArm) + ") OR (" + unescape(batchArm) + "))"
	if sinkFilter != wantSink {
		t.Errorf("the sink's filter renders as %s\nwant                        %s", sinkFilter, wantSink)
	}
	// The exclusion's own literal, with the file's own combined local
	// substituted in, so that a change to either (or to what hangs off the
	// group) fails here too.
	m := regexp.MustCompile(`(?m)^\s*filter\s*=\s*"((?:[^"\\]|\\.)*)"\s*$`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("logging.tf's exclusion has no quoted filter")
	}
	exclusionFilter := unescape(strings.ReplaceAll(m[1], "${local.fugaro_logs}", render(arm("fugaro_logs"))))
	wantExclusion := wantSink + ` AND NOT (resource.labels.job_name=~"^fugarochk-" AND jsonPayload.event="image-check") AND NOT logName:"run.googleapis.com%2Fvarlog%2Fsystem"`
	if exclusionFilter != wantExclusion {
		t.Errorf("the exclusion's filter renders as %s\nwant                             %s", exclusionFilter, wantExclusion)
	}
	// And the grouping itself: no group may mix AND with OR, which is what
	// makes the filter mean the same thing under Cloud Logging's precedence
	// (NOT, then OR, then AND) and the usual one.
	for _, c := range []struct{ what, filter string }{
		{"the sink's filter", sinkFilter},
		{"the exclusion's filter", exclusionFilter},
	} {
		if mixedAndOr(c.filter) {
			t.Errorf("%s mixes AND with OR in one group: %s\nCloud Logging binds AND loosest, so this does not group the way it reads", c.what, c.filter)
		}
	}
	// Both the sink and the exclusion carry both arms; the view's IAM
	// member still names only the launchers and operators.
	for _, want := range []string{
		"filter                 = local.fugaro_logs",
		"filter  = \"${local.fugaro_logs} AND NOT",
		"for_each = var.log_isolation ? toset(concat(var.launchers, var.operators)) : toset([])",
		"role     = \"roles/logging.viewAccessor\"",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("logging.tf does not contain %q", want)
		}
	}
}

// mixedAndOr reports whether filter uses AND and OR in the same group -- one
// parenthesis span, or the whole expression -- outside quoted values. Cloud
// Logging's query language is not the usual one (NOT, then OR, then AND, so
// AND binds loosest), which makes a mixed group mean something other than
// what it reads as: `A1 AND A2 OR B1 AND B2` is `A1 AND (A2 OR B1) AND B2`.
// A filter with no mixed group means the same thing under either precedence.
// Sibling groups do not count as one group: `(a OR b) AND NOT (c AND d)` has
// an OR and an AND in different spans.
func mixedAndOr(filter string) bool {
	word := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	stack := []int{0} // the innermost span is the current group; the whole expression is group 0
	next := 1
	and, or := map[int]bool{}, map[int]bool{}
	for i := 0; i < len(filter); i++ {
		switch c := filter[i]; {
		case c == '"': // a value: operators inside it are text
			for i++; i < len(filter) && filter[i] != '"'; i++ {
				if filter[i] == '\\' {
					i++
				}
			}
		case c == '(':
			stack = append(stack, next)
			next++
		case c == ')':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case word(c):
			j := i
			for j < len(filter) && word(filter[j]) {
				j++
			}
			group := stack[len(stack)-1]
			switch filter[i:j] {
			case "AND":
				and[group] = true
			case "OR":
				or[group] = true
			}
			i = j - 1
		}
	}
	for g := range and {
		if or[g] {
			return true
		}
	}
	return false
}

// TestMixedAndOr pins the check the sink test relies on, on the filter that
// was actually written once (an unparenthesized pair of arms) and on the ones
// that are correct, so that a checker that never fires cannot pass for a pin.
func TestMixedAndOr(t *testing.T) {
	for _, c := range []struct {
		filter string
		want   bool
	}{
		// The bug: the arms' AND clauses and the OR between them at one level.
		{`resource.type="cloud_run_job" AND labels."fugaro"="managed" OR resource.type="batch.googleapis.com/Job" AND labels."fugaro"="managed"`, true},
		{`a OR b AND c`, true},
		{`(a AND b) OR (c AND d)`, false},
		{`((a AND b) OR (c AND d)) AND NOT e`, false},
		{`((a AND b) OR (c AND d)) AND NOT (e AND f) AND NOT g`, false},
		{`resource.type="cloud_run_job" AND labels."fugaro"="managed"`, false},
		{`textPayload:"AND OR" AND severity="INFO"`, false},
	} {
		if got := mixedAndOr(c.filter); got != c.want {
			t.Errorf("mixedAndOr(%q) = %v, want %v", c.filter, got, c.want)
		}
	}
}

// Cloud Monitoring refuses an alert policy with a log-match condition and
// any other condition ("Alert policies with a log matching condition can
// only have a single condition"), and a mock-provider plan can't see that.
// So each policy with a condition_matched_log has exactly one, static,
// conditions block.
func TestLogMatchAlertPolicySingleCondition(t *testing.T) {
	n := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_monitoring_alert_policy") {
			n++
			for _, v := range logMatchViolations(blk) {
				t.Errorf("%s: %s", path, v)
			}
		}
	})
	if n == 0 {
		t.Error("no google_monitoring_alert_policy block found")
	}
}

func TestLogMatchViolations(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`conditions {
    condition_matched_log { filter = "a" }
  }
  conditions {
    condition_threshold { filter = "b" }
  }`, 1},
		{`conditions {
    condition_matched_log { filter = "a" }
  }
  conditions {
    condition_matched_log { filter = "b" }
  }`, 2},
		{`dynamic "conditions" {
    for_each = local.x
    content {
      condition_matched_log { filter = conditions.value }
    }
  }`, 1},
		{`conditions {
    condition_matched_log { filter = "a" }
  }`, 0},
		{`conditions {
    condition_threshold { filter = "a" }
  }
  conditions {
    condition_absent { filter = "b" }
  }`, 0},
	} {
		src := "resource \"google_monitoring_alert_policy\" \"p\" {\n  " + c.src + "\n}\n"
		blks := resourceBlocks(t, "src.tf", []byte(src), "google_monitoring_alert_policy")
		if len(blks) != 1 {
			t.Fatalf("blocks = %+v", blks)
		}
		if got := logMatchViolations(blks[0]); len(got) != c.want {
			t.Errorf("violations of\n%s\n= %q, want %d", c.src, got, c.want)
		}
	}
}

var (
	conditionsBlock = regexp.MustCompile(`(?m)^\s*(conditions|dynamic\s+"conditions")\s*\{`)
	dynamicCond     = regexp.MustCompile(`(?m)^\s*dynamic\s+"conditions"\s*\{`)
	logMatchBlock   = regexp.MustCompile(`(?m)^\s*condition_matched_log\s*\{`)
)

// logMatchViolations says how an alert policy breaks the log-match rule:
// at most one condition_matched_log, and none beside any other condition
// (a dynamic conditions block counts as several).
func logMatchViolations(blk block) []string {
	logs := len(logMatchBlock.FindAllString(blk.body, -1))
	if logs == 0 {
		return nil
	}
	var out []string
	if logs > 1 {
		out = append(out, fmt.Sprintf("%s has %d log-match conditions, want at most 1", blk.name, logs))
	}
	if n := len(conditionsBlock.FindAllString(blk.body, -1)); n != 1 || dynamicCond.MatchString(blk.body) {
		out = append(out, fmt.Sprintf("%s has a log-match condition beside other conditions (%d conditions blocks, dynamic: %v); the API allows it only alone",
			blk.name, n, dynamicCond.MatchString(blk.body)))
	}
	return out
}

// The history account gets its own narrow role, never fugaroLauncher (which
// can cancel executions and list secret metadata).
func TestHistoryAccountDoesNotHoldTheLauncherRole(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if strings.Contains(blk.body, "google_service_account.history") && strings.Contains(blk.body, "custom_role.launcher") {
				t.Errorf("%s: %s grants the launcher role to the history account", path, blk.name)
			}
		}
	})
}

// M9d: the history account writes Firestore documents with roles/datastore.user
// and holds no other datastore role (owner would let it manage databases and
// indexes). Only datastore.user and datastore.viewer exist in the tree, and
// a datastore role never lands on a service account except the history
// account's user grant.
func TestHistoryNoDatastoreAdmin(t *testing.T) {
	granted := map[string]int{}
	walk(t, func(path string, b []byte) {
		for _, typ := range []string{"google_project_iam_member", "google_storage_bucket_iam_member"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				for _, role := range regexp.MustCompile(`roles/datastore\.[A-Za-z]+`).FindAllString(blk.body, -1) {
					granted[role]++
					switch role {
					case "roles/datastore.user":
						if blk.name != "google_project_iam_member.history_firestore" {
							t.Errorf("%s: %s grants datastore.user; only history_firestore may", path, blk.name)
						}
					case "roles/datastore.viewer":
						if strings.Contains(blk.body, "history") || strings.Contains(blk.body, "serviceAccount:") {
							t.Errorf("%s: %s grants datastore.viewer to a service account", path, blk.name)
						}
					default:
						t.Errorf("%s: %s grants %s", path, blk.name, role)
					}
				}
			}
		}
	})
	if granted["roles/datastore.user"] != 1 || granted["roles/datastore.viewer"] != 1 {
		t.Errorf("want one datastore.user and one datastore.viewer resource, got %v", granted)
	}
}

// H8: the Firestore database is an idempotent REST ensure step (a Terraform
// create of a singleton does not adopt), and the rules are REST too: no
// Terraform resource for either.
func TestNoFirestoreDatabaseResource(t *testing.T) {
	walk(t, func(path string, b []byte) {
		code := stripComments(string(b))
		for _, bad := range []string{`"google_firestore_database"`, `"google_firebase_rules_`, `"google_firestore_`} {
			if strings.Contains(code, bad) {
				t.Errorf("%s declares %s: the database and its rules are fugaro update's REST steps", path, bad)
			}
		}
	})
}

// The history account may read the runs bucket (objectViewer) and nothing
// more there, and the Firebase module (which has no bucket) never names one.
func TestHistoryRunsBucketIsReadOnly(t *testing.T) {
	n := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_storage_bucket_iam_member") {
			if strings.Contains(blk.body, "google_service_account.history") {
				n++
				if !hasAttr(blk.body, "role", `"roles/storage.objectViewer"`) {
					t.Errorf("%s: %s grants the history account more than objectViewer", path, blk.name)
				}
			}
		}
	})
	if n != 1 {
		t.Errorf("want one bucket grant to the history account, got %d", n)
	}
}

// M9d: serviceusage.services.use (serviceUsageConsumer) is granted on the
// Firebase project only, in the firebase module, to the people set (like
// datastore.viewer) and the history account; never to job accounts, never
// with a domain or wildcard member.
func TestUsageConsumerOnlyInFirebaseModule(t *testing.T) {
	seen := map[string]bool{}
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if !strings.Contains(blk.body, "serviceusage.serviceUsageConsumer") {
				continue
			}
			seen[blk.name] = true
			if !strings.Contains(path, "modules/firebase/") {
				t.Errorf("%s: %s grants serviceUsageConsumer outside the firebase module", path, blk.name)
			}
			if strings.Contains(blk.body, "domain:") || strings.Contains(blk.body, `"*"`) || strings.Contains(blk.body, "allUsers") {
				t.Errorf("%s: %s grants serviceUsageConsumer to a domain or wildcard", path, blk.name)
			}
			if blk.name == "google_project_iam_member.history_usage" {
				if !strings.Contains(blk.body, "var.history_account") {
					t.Errorf("%s: history_usage does not name the history account", path)
				}
			} else if blk.name != "google_project_iam_member.usage_consumer" || !strings.Contains(blk.body, "setunion(local.people, local.admins)") {
				t.Errorf("%s: %s: only usage_consumer (people and admins) and history_usage may grant it", path, blk.name)
			}
		}
	})
	if len(seen) != 2 {
		t.Errorf("want exactly usage_consumer and history_usage, got %v", seen)
	}
}

// The rollover Scheduler job is created paused and Terraform never touches
// that again, so an apply neither pauses nor resumes it behind a person's back
// (the first rollover prunes RTDB days). ignore_changes is not visible in a
// plan test, so the source is pinned.
func TestRolloverJobPausedAndIgnored(t *testing.T) {
	found := false
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_cloud_scheduler_job") {
			if blk.name != "google_cloud_scheduler_job.history_rollover" {
				continue
			}
			found = true
			if !regexp.MustCompile(`paused\s*=\s*true`).MatchString(blk.body) {
				t.Errorf("%s: the rollover job is not created paused", path)
			}
			if !regexp.MustCompile(`ignore_changes\s*=\s*\[\s*paused\s*\]`).MatchString(blk.body) {
				t.Errorf("%s: the rollover job does not ignore changes to paused", path)
			}
		}
	})
	if !found {
		t.Fatal("no rollover scheduler job found")
	}
}

// REGRESSION (live, Check 23 step 8, 2026-10-05): a Scheduler call that carries
// overrides in its body needs run.jobs.runWithOverrides, which roles/run.invoker
// lacks (Cloud Run answered 403). The rollover job's body does carry overrides,
// so the scheduler account holds fugaroJobRunner on the history job, never
// roles/run.invoker. The binding stays job-scoped (a job IAM member).
func TestHistorySchedulerHoldsJobRunnerOnTheJob(t *testing.T) {
	found := false
	walk(t, func(path string, b []byte) {
		for _, blk := range allResourceBlocks(t, path, b) {
			if blk.typ != "google_cloud_run_v2_job_iam_member" || !strings.HasPrefix(blk.name, "google_cloud_run_v2_job_iam_member.history_") {
				continue
			}
			found = true
			if r := firstAttr(blk.body, "role"); r != "google_project_iam_custom_role.job_runner.name" {
				t.Errorf("%s: %s role is %s, want the job_runner custom role", path, blk.name, r)
			}
			if m := firstAttr(blk.body, "member"); m != "google_service_account.scheduler.member" {
				t.Errorf("%s: %s member is %s, want the scheduler account", path, blk.name, m)
			}
			if strings.Contains(blk.body, "run.invoker") {
				t.Errorf("%s: %s grants run.invoker, which cannot start a run with overrides", path, blk.name)
			}
		}
	})
	if !found {
		t.Fatal("no history job IAM member found")
	}
}

// TestTheCoordinatorJobTakesItsCredentialFromASecretAndNothingElse is P5's
// third red step ("the plan's generated diff carries no credential material in
// the job's environment"). The aggregation job's environment is the one place
// its credential could reach a plan, a `terraform show` and a log line, and
// the App key is the one credential it holds: its GitHub read is P2's
// read-only installation token, minted from this key, and its database write
// is its own account (D194). So the key is a secret_key_ref to the
// project-wide container and never a value, every other entry is the plain
// environment ee/coordinator/cli/coordinator.go's sweep requires, and the job runs
// as the account the Firebase root grants the FP's database admin to — the
// account and no default Compute account.
//
// Mutation (run, restore): replace the value_source with a literal value — a
// plan, a `terraform show` and the job's own describe would then carry the
// App's private key, and this fails on the entry that has no value_source.
func TestTheCoordinatorJobTakesItsCredentialFromASecretAndNothingElse(t *testing.T) {
	const path = "gcp/modules/installation/coordinator.tf"
	b, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatalf("%s: %v (P5's aggregation job is declared there)", path, err)
	}
	jobs := resourceBlocks(t, path, b, "google_cloud_run_v2_job")
	if len(jobs) != 1 || jobs[0].name != "google_cloud_run_v2_job.coordinator" {
		t.Fatalf("%s declares %d google_cloud_run_v2_job blocks (%v), want the coordinator's one", path, len(jobs), blockNames(jobs))
	}
	job := jobs[0]
	if !hasAttr(job.body, "service_account", "google_service_account.coordinator[0].email") {
		t.Errorf("%s: the aggregation job does not run as the coordinator account. Unset it runs as the default Compute Engine account, which is not the account the Firebase root grants the database admin to, and the sweep would then write nothing at all.", job.name)
	}
	for _, want := range [][2]string{
		{"command", `["/usr/local/bin/fugaro"]`},
		{"args", `["coordinator", "sweep"]`},
		// One tick is bounded and a failure is the next tick's job: nothing
		// retries a sweep into a second pass of its own (the plan's security
		// review asks for exactly this).
		{"max_retries", "0"},
		{"timeout", `"600s"`},
		{"deletion_protection", "true"},
	} {
		if !hasAttr(job.body, want[0], want[1]) {
			t.Errorf("%s: %s does not set %s = %s", path, job.name, want[0], want[1])
		}
	}

	// Every env entry of the container: one is the key through a secret, and
	// the rest are the plain environment the command reads.
	envs := nestedBlocks(job.body, "env")
	if len(envs) == 0 {
		t.Fatalf("%s: %s declares no env blocks", path, job.name)
	}
	plain := map[string]string{}
	mounted := 0
	for _, env := range envs {
		name := strings.Trim(firstAttr(env, "name"), `"`)
		if strings.Contains(env, "value_source") {
			mounted++
			if name != "FUGARO_GITHUB_APP_PRIVATE_KEY" {
				t.Errorf("%s: %s mounts %s through a secret: the App key is the one credential this job holds", path, job.name, name)
			}
			for _, want := range []string{
				`(?m)^\s*secret_key_ref\s*\{`,
				// The project-wide container of the logical name the local
				// config records (config.GitHubAppKeySecret), never a path.
				`(?m)^\s*secret\s*=\s*var\.project_secrets\[var\.coordinator\.app_key_secret\]\s*$`,
				`(?m)^\s*version\s*=\s*"latest"\s*$`,
			} {
				if !regexp.MustCompile(want).MatchString(env) {
					t.Errorf("%s: %s mounts the App key without %s:\n%s", path, job.name, want, env)
				}
			}
			continue
		}
		if name == "FUGARO_GITHUB_APP_PRIVATE_KEY" {
			t.Errorf("%s: %s carries the App's private key as a plain environment value:\n%s", path, job.name, env)
		}
		plain[name] = firstAttr(env, "value")
	}
	if mounted != 1 {
		t.Errorf("%s: %s mounts %d entries through a secret, want the App key's one", path, job.name, mounted)
	}
	for _, want := range []string{
		"FUGARO_PROJECT", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL",
		"FUGARO_RUNS_BUCKET", "FUGARO_COORDINATOR_REPOS", "FUGARO_GITHUB_APP_ID",
	} {
		if _, ok := plain[want]; !ok {
			t.Errorf("%s: %s does not set %s, which the sweep refuses to run without (ee/coordinator/cli/coordinator.go's runCoordinatorSweep)", path, job.name, want)
		}
	}
	if len(plain) != 6 {
		t.Errorf("%s: %s sets %d plain environment values (%v), want the six the command reads", path, job.name, len(plain), slices.Sorted(maps.Keys(plain)))
	}
	// And no plain value is key material, whatever its entry is called: a PEM
	// in a plan is the leak this test exists for.
	for name, value := range plain {
		if strings.Contains(value, "PRIVATE KEY") || strings.Contains(value, "BEGIN ") {
			t.Errorf("%s: %s's value looks like key material: %s", path, name, value)
		}
	}
}

// nestedBlocks returns the bodies of the blocks of type typ at any depth of
// body: resourceBlocks reads resource headers only, and a job's env blocks sit
// inside its container.
func nestedBlocks(body, typ string) []string {
	re := regexp.MustCompile(`(?m)^\s*` + typ + `\s*\{`)
	var out []string
	for _, m := range re.FindAllStringIndex(body, -1) {
		open := strings.LastIndex(body[m[0]:m[1]], "{") + m[0]
		end := matchBrace(body, open)
		if end < 0 {
			return out
		}
		out = append(out, body[open+1:end])
	}
	return out
}

// The same-project layout (design D3, revised 2026-10-04): the installation
// and the Firebase budget backend can live in one GCP project, so the budget
// backend's separation from the jobs rests on the IAM in this tree (and on
// what people grant by hand, which no test here can see). The tests below pin
// what THIS repository's Terraform grants, as text, because a plan assertion
// sees only the inputs it is given and could never notice a new grant written
// by reference. They are deliberately strict: every IAM resource of every
// type must use a role and a member expression from an explicit allowlist, so
// a grant routed through a local, a variable or format() fails here and has to
// be reviewed and added knowingly.

var (
	quotedRoleRE = regexp.MustCompile(`"(roles/[A-Za-z0-9_.]+)"`)
	roleRefRE    = regexp.MustCompile(`var\.installation\.role_ids\.[a-z_]+|google_project_iam_custom_role\.[a-z_]+(?:\[0\])?\.name`)
	jobAccountRE = regexp.MustCompile(`google_service_account\.(job|build|scheduler)\b`)

	// Every IAM resource type: google_<scope>_iam_member|binding|policy|audit_config.
	anyIAMRE           = regexp.MustCompile(`^google_[a-z0-9_]+_iam_(member|binding|policy|audit_config)$`)
	authoritativeIAMRE = regexp.MustCompile(`^google_[a-z0-9_]+_iam_(binding|policy|audit_config)$`)
	attrRE             = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*` + name + `\s*=\s*(.+?)\s*$`)
	}

	// The role expressions an IAM member may use. A literal "roles/..." is
	// checked against backendRoleRE; each.value (and each.value.role) takes
	// its roles from literals and references elsewhere in the file, which are
	// scanned the same way.
	// The compute module's two roles and its build-submitter reference are on
	// the allowlist knowingly: each is created by that module with a pinned
	// permission list (TestComputeReaperRoleIsExactlyFive and
	// TestComputePreloadBuilderRoleAreExactlyTheBuild), and the
	// build-submitter id is the installation's own role.
	allowedRoleExprs = regexp.MustCompile(`^(each\.value(\.role)?|var\.job_runner_role|var\.installation\.role_ids\.(tag_mover|build_submitter)|var\.build_submitter_role|google_project_iam_custom_role\.(launcher|build_submitter|token_minter|job_runner|reaper|preload_builder)\.name|google_project_iam_custom_role\.history\[0\]\.name)$`)
	// The member expressions an IAM member may use. google_service_account
	// .credentials[0] is the credentials service's own account (C1.18): the
	// one member its bundle's secretAccessor grant names.
	// google_service_account.coordinator[0] is the coordinator's writer (P5):
	// the aggregation job's account, whose own grants are objectViewer on the
	// runs bucket and secretAccessor on the App key's one container, both on
	// the account and nothing wider. var.coordinator_account is the same
	// account as the Firebase root reads it (P3): the installation root
	// outputs it and that root grants it the FP's database admin, as it grants
	// var.history_account's.
	allowedMemberExprs = regexp.MustCompile(`^(each\.(value|key)(\.member)?|each\.value\.member|google_service_account\.(job|build|scheduler|reaper|preload|fugaro_deployer)\.member|google_service_account\.(history|credentials|coordinator)\[0\]\.member|local\.cloud_(build|scheduler)_agent|"serviceAccount:\$\{var\.(history_account|coordinator_account|installation\.scheduler_service_account)\}")$`)

	// A custom role of one of this run's projects, spelled as the literal path
	// "projects/${var.<project>}/roles/<id>" rather than read from the role
	// resource's own name attribute. Task 1.5 step 5: the creating apply plans
	// the deployer's eight roles, and a project grant whose role is computed
	// cannot be told from a conditioned one, whose IAM condition is the whole
	// fence around the account — so the project grants planned by that same
	// apply (the deployer's eight, the launchers', the build submitters' and
	// the history account's) name their role literally instead. Both project
	// variables are accepted because the credentials service's database role
	// lives in the Firebase project (credentials.tf, and the reason is the same
	// one: an unknown role is a grant the cover cannot read). Both are known at
	// plan time, and the ID must be one the modules create (tf.FugaroRoles),
	// checked below.
	projectRoleLiteralRE = regexp.MustCompile(`^"projects/\$\{var\.(project|credentials_service\.firebase_project)\}/roles/([A-Za-z0-9_.]+)"$`)

	// Roles that reach the budget backend (RTDB, Firestore, Identity
	// Platform, API keys, the token signer, IAM or project policy), or that
	// would let an account grant itself such a role, or run code as a
	// default account that holds one (Cloud Build, Cloud Run admin,
	// Functions). Only the Firebase module may grant the first group.
	backendRoleRE = regexp.MustCompile(`^roles/(firebase[A-Za-z.]*|datastore\.[A-Za-z]+|identitytoolkit\.[A-Za-z]+|serviceusage\.[A-Za-z]+|apikeys\.[A-Za-z]+|owner|editor|viewer|resourcemanager\.[A-Za-z]+|iam\.(admin|roleAdmin|securityAdmin|securityReviewer|serviceAccount[A-Za-z]*|workloadIdentity[A-Za-z]*|organizationRoleAdmin|denyAdmin)|run\.(admin|developer)|cloudfunctions\.[A-Za-z]+|aiplatform\.admin|cloudbuild\.[A-Za-z.]+|appengine\.[A-Za-z]+|compute\.[A-Za-z]*[aA]dmin|logging\.admin|secretmanager\.admin)$`)
)

// projectRoles are the roles (literal or by reference) a block grants.
func projectRoles(body string) []string {
	var out []string
	for _, m := range quotedRoleRE.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return append(out, roleRefRE.FindAllString(body, -1)...)
}

// TestIAMResourcesUseKnownExpressions fails on any IAM resource (any type, in
// any root or module) whose role or member is an expression nobody has
// reviewed: a local, a variable, format(), a conditional. The allowlist is
// the set this tree uses today. A literal role must also not be a backend
// role outside the Firebase module, nor a primitive role anywhere.
func TestIAMResourcesUseKnownExpressions(t *testing.T) {
	n := 0
	walk(t, func(path string, b []byte) {
		if !strings.HasSuffix(path, ".tf") {
			return
		}
		inFirebase := strings.HasPrefix(path, "gcp/modules/firebase/")
		for _, blk := range allResourceBlocks(t, path, b) {
			if !anyIAMRE.MatchString(blk.typ) {
				continue
			}
			n++
			role, member := firstAttr(blk.body, "role"), firstAttr(blk.body, "member")
			switch {
			case role == "":
				t.Errorf("%s: %s has no role attribute the scan can read", path, blk.name)
			case strings.HasPrefix(role, `"`):
				if m := projectRoleLiteralRE.FindStringSubmatch(role); m != nil {
					if !slices.Contains(tf.FugaroRoles, m[2]) {
						t.Errorf("%s: %s: role %s names the custom role %s, which the modules do not create", path, blk.name, role, m[2])
					}
					break
				}
				m := quotedRoleRE.FindStringSubmatch(role)
				if m == nil || `"`+m[1]+`"` != role {
					t.Errorf("%s: %s: role %s is not a plain literal \"roles/...\" (no interpolation, format() or concatenation)", path, blk.name, role)
				}
			case !allowedRoleExprs.MatchString(role):
				t.Errorf("%s: %s: role expression %s is not on the allowlist (route it through a reviewed reference)", path, blk.name, role)
			}
			if member == "" || !allowedMemberExprs.MatchString(member) {
				t.Errorf("%s: %s: member expression %q is not on the allowlist", path, blk.name, member)
			}
			for _, r := range quotedRoleRE.FindAllStringSubmatch(blk.body, -1) {
				checkLiteralRole(t, path, blk.name, r[1], blk.typ, inFirebase)
			}
		}
		// A role may arrive through each.value from a local: every literal role
		// in the file is held to the same rule.
		for _, r := range quotedRoleRE.FindAllStringSubmatch(stripComments(string(b)), -1) {
			checkLiteralRole(t, path, "(file)", r[1], "", inFirebase)
		}
	})
	if n < 30 {
		t.Errorf("found only %d IAM resources: the scan is not seeing the tree", n)
	}
}

func checkLiteralRole(t *testing.T, path, where, role, typ string, inFirebase bool) {
	t.Helper()
	if slices.Contains(tf.PrimitiveRoles(), role) {
		t.Errorf("%s: %s grants or names the primitive role %s", path, where, role)
		return
	}
	if !backendRoleRE.MatchString(role) || inFirebase {
		return
	}
	// serviceAccountUser (actAs, for the operators who deploy a job or a
	// build) on a service account is one allowed shape, and
	// serviceAccountTokenCreator is the other: D75's impersonation grant,
	// which lets the owner assume the deployer account and nothing else
	// (installation/deployer.tf, one grant on that account, for var.operators).
	// Neither may be granted on the project or on another type.
	if (role == "roles/iam.serviceAccountUser" || role == "roles/iam.serviceAccountTokenCreator") && (typ == "google_service_account_iam_member" || typ == "") {
		return
	}
	t.Errorf("%s: %s names %s, a role that reaches the budget backend or IAM, outside the Firebase module", path, where, role)
}

func firstAttr(body, name string) string {
	m := attrRE(name).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestServiceAccountTokenCreatorIsTheDeployersImpersonationGrant pins the
// claim D75 makes in prose — serviceAccountTokenCreator is "one grant on that
// account, for var.operators" — as a property of the whole tree, because the
// role scanner does not hold it. checkLiteralRole exempts
// roles/iam.serviceAccountTokenCreator beside roles/iam.serviceAccountUser so
// the deployer's impersonation grant is allowed at all, and an exemption
// cannot tell an operator impersonating the deployer from the same role on a
// job account, which is a credential that runs as that account. A literal
// token-creator grant is therefore allowed in exactly one place, and it is the
// deployer's own block: the deployer account, written as a literal path, for
// var.operators.
//
// Its members are people on the review screen (internal/infra/tf's grantRules)
// and the plan itself is screened again by Cover.deployerTokenCreator, so what
// this test is the file-level half of is: no second block anywhere grants the
// role, and the one that does is the one D75 describes.
func TestServiceAccountTokenCreatorIsTheDeployersImpersonationGrant(t *testing.T) {
	const (
		role = "roles/iam.serviceAccountTokenCreator"
		// The install module's deployer account, as deployer.tf writes it.
		account = `"projects/${var.project}/serviceAccounts/fugaro-deployer@${var.project}.iam.gserviceaccount.com"`
		file    = "gcp/modules/installation/deployer.tf"
		block   = "google_service_account_iam_member.fugaro_deployer_token_creator"
	)
	var sites []string
	walk(t, func(path string, b []byte) {
		code := stripComments(string(b))
		for _, m := range quotedRoleRE.FindAllStringSubmatch(code, -1) {
			if m[1] == role {
				sites = append(sites, path)
			}
		}
	})
	if len(sites) != 1 || sites[0] != file {
		t.Errorf("the literal %s appears in %v; want once, in %s (D75's impersonation grant, and nothing else in the tree)", role, sites, file)
	}

	var found bool
	walk(t, func(path string, b []byte) {
		if path != file {
			return
		}
		for _, blk := range allResourceBlocks(t, path, b) {
			if blk.name != block {
				continue
			}
			found = true
			if !hasAttr(blk.body, "service_account_id", account) {
				t.Errorf("%s: %s does not name the deployer account as the literal %s: a grant whose account is computed is a grant nobody can check, and this is the account an operator is impersonating", path, blk.name, account)
			}
			if !hasAttr(blk.body, "for_each", "toset(var.operators)") {
				t.Errorf("%s: %s is not for var.operators, the people the owner names at setup", path, blk.name)
			}
			if !hasAttr(blk.body, "member", "each.value") {
				t.Errorf("%s: %s does not grant the role to each.value", path, blk.name)
			}
		}
	})
	if !found {
		t.Fatalf("%s has no %s", file, block)
	}
}

// TestInstallationServiceAccountGrantsNameTheirAccount pins the one shape the
// installation module's service-account grants have to have: the account is a
// literal path built from var.project, never read from the account resource's
// own name attribute.
//
// Why it is a rule and not a preference. The installation module is the one
// that creates the deployer account (D75), and the review that screens the
// apply — internal/infra/tf's Cover.onDeployer — refuses every
// service-account grant in a deployer-creating module whose account is
// computed, because a computed account there could be the deployer, and actAs
// on the deployer is a job that runs as the deployer. A grant written as
// google_service_account.<x>.name is therefore not merely unreadable: it makes
// the installation apply stop riding the run's one confirmation and ask for
// its own, printing a reason that is false about the module's own grant. The
// operators' actAs on the scheduler account did exactly that (scheduler.tf),
// which is what this test was added for.
//
// deployer.tf's token-creator grant gives the same reason for the same choice
// in its own comment; this holds the whole module to it. The other modules are
// not held to it: none of them creates the deployer, so a grant there may read
// its account from the resource, and most of them do.
func TestInstallationServiceAccountGrantsNameTheirAccount(t *testing.T) {
	const dir = "gcp/modules/installation/"
	// The literal form: var.project twice — the account path and its own
	// domain — and the account's ID, either written out or read from a
	// variable the module validates as a service account ID. What the pattern
	// is here to refuse is an account read from the account RESOURCE's own
	// name attribute (google_service_account.<x>.name), which is the shape
	// that is unknown at plan time.
	literalSA := regexp.MustCompile(`^"projects/\$\{var\.project\}/serviceAccounts/([a-z][a-z0-9-]*|\$\{var\.[A-Za-z0-9_.\[\]]+\})@\$\{var\.project\}\.iam\.gserviceaccount\.com"$`)
	n := 0
	walk(t, func(path string, b []byte) {
		if !strings.HasPrefix(path, dir) {
			return
		}
		for _, blk := range allResourceBlocks(t, path, b) {
			if blk.typ != "google_service_account_iam_member" {
				continue
			}
			n++
			id := firstAttr(blk.body, "service_account_id")
			// Only deployer.tf grants on the deployer (the token-creator
			// rule); another file's grant naming it is actAs on the deployer.
			if m := literalSA.FindStringSubmatch(id); m != nil && m[1] == "fugaro-deployer" && !strings.HasSuffix(path, "/deployer.tf") {
				t.Errorf("%s: %s grants on the deployer account outside deployer.tf (%q)", path, blk.name, id)
			}
			if !literalSA.MatchString(id) {
				t.Errorf("%s: %s names its account as %q: a service-account grant in the installation module must name its account as a literal path, because this module creates the deployer account and cover.onDeployer refuses a computed account beside it — the apply then asks its own confirmation and refuses the module's own grant", path, blk.name, id)
			}
		}
	})
	if n < 2 {
		t.Errorf("found only %d service-account grants in %s: the scan is not seeing the module's own grants", n, dir)
	}
}

// REGRESSION (same-project layout): the project-level roles any job, build or
// scheduler account holds are exactly these, in every module and IAM resource
// type. None gives access to the RTDB, Firestore, Identity Platform or the
// token signer, and no primitive role is among them. A new project-level
// grant to one of these accounts must be added here knowingly. (Members are
// held to the allowlist above, so a grant cannot hide behind a local.)
func TestJobAccountsProjectRolesAreExactly(t *testing.T) {
	want := []string{
		"roles/aiplatform.user",                     // a Vertex workflow's job account
		"roles/logging.logWriter",                   // a repository's build account
		"var.installation.role_ids.build_submitter", // fugaroBuildSubmitter: cloudbuild.builds.create/get
	}
	got := map[string]bool{}
	walk(t, func(path string, b []byte) {
		for _, blk := range allResourceBlocks(t, path, b) {
			if !anyIAMRE.MatchString(blk.typ) || !strings.HasPrefix(blk.typ, "google_project_") || !jobAccountRE.MatchString(blk.body) {
				continue
			}
			for _, r := range projectRoles(blk.body) {
				got[r] = true
			}
		}
	})
	var have []string
	for r := range got {
		have = append(have, r)
	}
	slices.Sort(have)
	if !slices.Equal(have, want) {
		t.Errorf("job, build and scheduler accounts hold project roles %v, want exactly %v", have, want)
	}
}

// Only the token minter role carries signJwt, and it is never granted on the
// project: only on the signer account. Every custom role's permission list
// must be a plain literal list (a variable, local or concat could carry
// anything), and none but the minter may hold a permission on the budget
// backend, identities, IAM or the project policy.
func TestCustomRolesNeverReachTheBackend(t *testing.T) {
	banned := regexp.MustCompile(`^(firebase[a-z]*\.|datastore\.|identitytoolkit\.|iam\.|resourcemanager\.|serviceusage\.|apikeys\.|cloudbuild\.builds\.(update|approve)|cloudfunctions\.|run\.(services|jobs)\.(create|update|setIamPolicy)|run\.[a-z]+\.setIamPolicy|compute\.|appengine\.)`)
	listRE := regexp.MustCompile(`(?s)permissions\s*=\s*\[([^\]]*)\]`)
	permRE := regexp.MustCompile(`^\s*(?:"[a-z0-9]+(?:\.[A-Za-z0-9]+)+"\s*,?\s*)*$`)
	n := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_custom_role") {
			n++
			m := listRE.FindStringSubmatch(blk.body)
			if m == nil || !permRE.MatchString(strings.ReplaceAll(m[1], "\n", " ")) {
				// Comments are stripped; what is left must be quoted strings only.
				t.Errorf("%s: %s: permissions is not a literal list of quoted permissions (a variable, local or concat is refused)", path, blk.name)
				continue
			}
			// The compute module's two roles are the one knowing exception to
			// the compute ban, and each is pinned to its exact permission
			// list by TestComputeReaperRoleIsExactlyFive and
			// TestComputePreloadBuilderRoleAreExactlyTheBuild: the reaper ends
			// Batch jobs and VMs and creates nothing, and the builder boots,
			// reads, stops, images and deletes one VM. Neither reaches the
			// budget backend, identities or IAM in any other way.
			if blk.name == "google_project_iam_custom_role.reaper" || blk.name == "google_project_iam_custom_role.preload_builder" {
				if !strings.HasPrefix(path, "gcp/modules/compute/") {
					t.Errorf("%s: %s is not in the compute module", path, blk.name)
				}
				for _, p := range quotedPerm.FindAllStringSubmatch(m[1], -1) {
					if !strings.HasPrefix(p[1], "compute.") && !strings.HasPrefix(p[1], "batch.jobs.") {
						t.Errorf("%s: %s holds %s, which is neither the reaper's five nor the preload builder's build", path, blk.name, p[1])
					}
				}
				continue
			}
			// The deployer's roles (installation/deployer.tf, D75) hold
			// permissions this ban exists to keep out of Fugaro's own custom
			// roles: IAM, serviceusage and apikeys permissions, and a
			// resource-level setIamPolicy. That is the owner's answer of
			// 2026-10-16, and the bound on them is elsewhere and is exact:
			// tf.RolePermissions pins every list permission for permission
			// (internal/infra/tf/cover_test.go) with a fixture that refuses a
			// widening, and each of the eight is bound to one account, the
			// seven conditioned ones only under the managed-prefix fence of
			// their own service (docs/design/managed-deployer-fence.md). The
			// deployer is not an installation role: it holds nothing
			// project-wide but creation and reads.
			if strings.HasPrefix(blk.name, "google_project_iam_custom_role.fugaro_deployer") {
				continue
			}
			// The credentials service's database role (installation/
			// credentials.tf, 0.10.1, ledger #903): the one knowing exception
			// that holds a firebasedatabase permission, because the service
			// reads the kill switches, the project policy, a run's outcome node
			// and the credential state another revision wrote, and writes the
			// creds/ nodes it owns. Realtime Database IAM has no path-level
			// scope, so the two instance DATA permissions are the whole of the
			// narrowest grant that can do that — nothing else is allowed here,
			// so no lifecycle permission (create, delete, disable, reenable,
			// undelete) and no list may appear, and the role is bound to the
			// service's own account alone (tf's toCredentials rows). The exact
			// list is pinned by TestCredentialsDatabaseRoleIsTheTwoDataPermissions
			// below; the ban this continues past is about roles whose
			// permissions reach further than the account's work needs.
			if blk.name == "google_project_iam_custom_role.credentials_database" {
				if !strings.HasPrefix(path, "gcp/modules/installation/credentials") {
					t.Errorf("%s: %s is not in the installation module's credentials.tf", path, blk.name)
				}
				for _, p := range quotedPerm.FindAllStringSubmatch(m[1], -1) {
					if !slices.Contains(credentialsDatabasePermissions, p[1]) {
						t.Errorf("%s: %s holds %s: the service's database role is its two data permissions and nothing else (no lifecycle, no list)", path, blk.name, p[1])
					}
				}
				continue
			}
			if blk.name == "google_project_iam_custom_role.token_minter" {
				if !strings.HasPrefix(path, "gcp/modules/firebase/") || strings.TrimSpace(strings.Trim(strings.TrimSpace(m[1]), ",")) != `"iam.serviceAccounts.signJwt"` {
					t.Errorf("%s: %s is not the signJwt-only role in the Firebase module", path, blk.name)
				}
				continue
			}
			for _, p := range quotedPerm.FindAllStringSubmatch(m[1], -1) {
				if banned.MatchString(p[1]) {
					t.Errorf("%s: %s holds %s, a permission on the budget backend, identities, IAM or code execution", path, blk.name, p[1])
				}
			}
		}
		for _, blk := range allResourceBlocks(t, path, b) {
			if strings.HasPrefix(blk.typ, "google_project_iam_") && strings.Contains(blk.body, "token_minter") {
				t.Errorf("%s: %s grants the token minter role on the project", path, blk.name)
			}
		}
	})
	if n < 6 {
		t.Errorf("found %d custom roles, want at least 6", n)
	}
}

var quotedPerm = regexp.MustCompile(`"([^"]+)"`)

// The compute module's provisioning, held to what the design says it must be
// (docs/design/compute-backend.md §3.2, §3.7, §8.1 task 6). These are text
// checks because the values that matter -- which role, on what, narrowed by
// what condition -- are what a reviewer has to read, and a plan assertion
// only ever sees the inputs a test hands it.

// The socket gid's round trip, file by file (design §2.2, C2; the plan's
// Task 9 step 3). A run reaches the VM's Docker socket through
// `--group-add <gid>` and the gid belongs to the boot image, so the one place
// it can come from is the image itself: the build reads it off the builder VM
// and labels every generation it makes with it, and the module publishes the
// label's name for whatever reads it back (Check 57b today, `doctor` later).
//
// It is a text test because no plan can see it: the image is built out of
// band by a script, so terraform's refresh covers none of it and no resource
// holds the value. What the test can hold is that the three files agree on the
// chain rather than merely each looking right -- and that no gid is a literal
// anywhere in this tree, because a gid written here would be a guess about
// whatever boot image an installation runs. The value the fake in
// internal/cli/init_compute_test.go uses stands in for one: `testSocketGID`
// (998) is internal/backend/batch's, and 412 -- the gid Check 57a-6 measured
// on the COS image it used -- appears in no file this test reads.
func TestComputeModuleRecordsTheSocketGID(t *testing.T) {
	const (
		script  = "gcp/modules/compute/preload.sh"
		preload = "gcp/modules/compute/preload.tf"
		outputs = "gcp/modules/compute/outputs.tf"
	)
	read := func(path string) string {
		t.Helper()
		b, err := fs.ReadFile(FS, path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		return string(b)
	}
	sh := read(script)
	for _, want := range []struct{ what, text string }{
		{"the read off the builder VM", "stat -c %g /var/run/docker.sock"},
		{"the marker the build parses it back out of", "GID_MARKER="},
		{"the label written onto the image the build creates", `"${SOCKET_GID_LABEL}=${SOCKET_GID}"`},
		{"the refusal of a gid that is not one", "*[!0-9]*"},
		{"the refusal of gid 0, which no container can be added to", "socket gid is 0"},
		{"the label's name taken from the build rather than spelled here", "${SOCKET_GID_LABEL:?}"},
	} {
		if !strings.Contains(sh, want.text) {
			t.Errorf("%s no longer carries %s (%q): the gid is read off the VM, recorded on the image the build makes, and the label it is recorded under is the build's to name", script, want.what, want.text)
		}
	}
	// The read has to happen ON the VM (the build step cannot reach the
	// socket), so it sits in the builder's own startup script, and the label
	// has to be written by the create that makes the generation (an image
	// labelled afterwards is an image a run can reach a socket through with a
	// gid nothing recorded).
	// LastIndex, not Index: the header comment above quotes the same command,
	// and a check that read the comment would pass on a file whose script no
	// longer reads the socket at all.
	if i, j := strings.LastIndex(sh, "stat -c %g /var/run/docker.sock"), strings.Index(sh, "--metadata=startup-script="); i < 0 || j < 0 || i < j {
		t.Errorf("%s reads the gid outside the builder VM's startup script: the build step runs in Cloud Build and has no socket to stat", script)
	}
	if i, j := strings.Index(sh, `"${SOCKET_GID_LABEL}=${SOCKET_GID}"`), strings.Index(sh, "gcloud compute images create"); i < 0 || j < 0 || i < j {
		t.Errorf("%s writes the gid's label before the image it belongs to is created", script)
	}
	tf := read(preload)
	// One name for the label, and both ends of the chain reading it: the
	// build's substitution carries it into the step, and the output publishes
	// the same local.
	for _, want := range []struct{ what, text string }{
		{"the single local the label's name is written in", `socket_gid_label = "socket-gid"`},
		{"the build substitution the step reads it from", "_SOCKET_GID_LABEL = local.socket_gid_label"},
		// `$${` is the escape terraform needs: `${` alone is terraform reading
		// a variable, and the build config is what has to carry the
		// substitution's name (terraform validate refuses the unescaped form).
		{"the step's own environment", "SOCKET_GID_LABEL=$${_SOCKET_GID_LABEL}"},
	} {
		if !strings.Contains(tf, want.text) {
			t.Errorf("%s no longer carries %s (%q): the writer and the reader would then be two spellings of one name", preload, want.what, want.text)
		}
	}
	if n := strings.Count(tf, `"socket-gid"`); n != 1 {
		t.Errorf("%s spells the label's name %d times, want once (in the local): a second literal is a name that can drift from the output below", preload, n)
	}
	if !strings.Contains(read(outputs), "preload_socket_gid_label") {
		t.Errorf("%s publishes no preload_socket_gid_label: the stage and a later doctor check read the label's name from the root's outputs, together with the image's family (infra.ComputeOutputs)", outputs)
	}
	// And no gid is a literal in any of the three, in the root the stage reads
	// back, or in the Go struct that decodes it.
	for _, path := range []string{script, preload, outputs,
		"gcp/roots/compute/outputs.tf", "gcp/roots/compute/main.tf"} {
		for _, gid := range []string{"412", "998"} {
			if strings.Contains(read(path), gid) {
				t.Errorf("%s carries the literal %s: a gid written into this tree is a guess about whatever boot image an installation runs -- the build reads it off the VM it builds the image on", path, gid)
			}
		}
	}
}

// permissionsOf returns the literal permissions a custom role block holds.
func permissionsOf(t *testing.T, path string, blk block) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)permissions\s*=\s*\[(.*?)\]`).FindStringSubmatch(blk.body)
	if m == nil {
		t.Fatalf("%s: %s has no literal permissions list", path, blk.name)
	}
	var perms []string
	for _, p := range quotedPerm.FindAllStringSubmatch(m[1], -1) {
		perms = append(perms, p[1])
	}
	slices.Sort(perms)
	return perms
}

// The reaper's role is the FIVE permissions the reaper's code uses, and
// nothing else: list, get and delete a job, and list and delete an instance.
// It carries no create permission of any kind, and the two built-in roles
// Google's own pages name for the same calls -- batch.jobsEditor (which also
// carries batch.jobs.create) and compute.instanceAdmin.v1 (which carries the
// whole instance lifecycle, setMetadata included) -- are granted nowhere in
// the tree.
//
// It named six until 0.9.5, the sixth being batch.jobs.cancel. That is not a
// permission Google defines -- the Batch API has jobs.create, jobs.delete,
// jobs.get and jobs.list, which is what TestCustomRolePermissionsAreAllowed-
// ByGoogle's snapshot says -- so the role could not be created at all and
// --provision-batch was refused for it. It must not come back: the reaper's
// package has no cancel action to emit either
// (internal/backend/batch/reap.go), because a permission nothing uses is a
// permission someone re-adds.
func TestComputeReaperRoleIsExactlyFive(t *testing.T) {
	want := []string{
		"batch.jobs.delete",
		"batch.jobs.get",
		"batch.jobs.list",
		"compute.instances.delete",
		"compute.instances.list",
	}
	roles, grants := 0, 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_custom_role") {
			if blk.name != "google_project_iam_custom_role.reaper" {
				continue
			}
			roles++
			if path != "gcp/modules/compute/iam.tf" {
				t.Errorf("%s: the reaper's role is the compute module's", path)
			}
			if got := permissionsOf(t, path, blk); !slices.Equal(got, want) {
				t.Errorf("%s: the reaper holds %v, want exactly %v (design §3.7)", path, got, want)
			}
		}
		// The project-level grant, and only it: what this test is about is
		// what the reaper account is GRANTED on the project. Task 11's
		// reaper.tf holds three grants the other way round -- the account is
		// the member of a job-scoped roles/run.invoker and of two actAs
		// bindings (the Scheduler agent's, so the schedule can mint the job's
		// token, and the operators', so applying this root can create it) --
		// and none of those is a project role. They are not counted here
		// because they say nothing about this: a reaper that could reach more
		// of the project would reach it through a project role, which is what
		// the scan below still refuses, whatever type carries it.
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if !strings.Contains(blk.body, "google_service_account.reaper") {
				continue
			}
			grants++
			if blk.name != "google_project_iam_member.reaper" ||
				firstAttr(blk.body, "role") != "google_project_iam_custom_role.reaper.name" {
				t.Errorf("%s: %s grants the reaper something other than its own five-permission role", path, blk.name)
			}
		}
	})
	if roles != 1 || grants != 1 {
		t.Errorf("found %d reaper roles and %d reaper grants, want 1 of each", roles, grants)
	}
}

// TestTheReaperJobRunsAsTheReaperAccountOnASchedule pins Task 11's invocation
// (design §8.1 Task 11, steps 2 and 3): the Cloud Run job that runs
// `fugaro reaper`, the invoker grant it is started with, and the Scheduler job
// that starts it every ReapMargin -- in the scheduler region and not the
// installation's, which is the one attribute of it a plan cannot check and a
// wrong value cannot be seen from (a Scheduler job in a region Cloud Scheduler
// does not serve fails the apply).
//
// It is the task's red step, and it was written against a reaper.tf whose job
// left `service_account` unset: a Cloud Run job with no service account runs as
// the project's default Compute Engine account, which holds the editor role by
// default -- far wider than the five permissions iam.tf narrowed for this job,
// and the exact shape the module refuses for a run's VM
// (internal/infra/compute.go's checkServiceAccount). The assertion below is
// what makes that unset job impossible to merge.
//
// The sibling tftest run (gcp/roots/compute/tests/compute.tftest.hcl,
// TestTheReaperRunsAsTheReaperAccountOnASchedule) asserts the same three
// resources against a plan, where the URI's parts and the account's member are
// values rather than text. This test is the text: a plan cannot see that the
// name in front of it is the reaper's, and this scan is what keeps the two
// files' literals from drifting apart.
func TestTheReaperJobRunsAsTheReaperAccountOnASchedule(t *testing.T) {
	const path = "gcp/modules/compute/reaper.tf"
	b, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatalf("%s: %v (Task 11's invocation lives there)", path, err)
	}
	jobs := resourceBlocks(t, path, b, "google_cloud_run_v2_job")
	if len(jobs) != 1 || jobs[0].name != "google_cloud_run_v2_job.reaper" {
		t.Fatalf("%s declares %d google_cloud_run_v2_job blocks (%v), want the reaper's one", path, len(jobs), blockNames(jobs))
	}
	job := jobs[0]
	// The pin the task was written against.
	if !hasAttr(job.body, "service_account", "google_service_account.reaper.email") {
		t.Errorf("%s: the reaper's job does not run as the reaper account. Unset it runs as the default Compute Engine account, whose editor role is far wider than the five permissions this job is meant to hold.", job.name)
	}
	if strings.Contains(job.body, "developer.gserviceaccount.com") {
		t.Errorf("%s: the reaper's job names a default Compute account: %s", path, job.name)
	}
	for _, want := range [][2]string{
		{"name", "local.reaper_job"},
		{"image", "var.base_image"},
		{"command", `["/usr/local/bin/fugaro"]`},
		{"args", `["reaper"]`},
		// The one pass over every job and VM is bounded: inside the cadence,
		// and never retried into a second pass of its own.
		{"max_retries", "0"},
		{"timeout", `"600s"`},
		{"deletion_protection", "true"},
	} {
		if !hasAttr(job.body, want[0], want[1]) {
			t.Errorf("%s: %s does not set %s = %s", path, job.name, want[0], want[1])
		}
	}
	// The job's whole configuration is the project and the region: a Cloud Run
	// job has no project config file and no checkout, and the timeouts the
	// pass judges by come from a run's own ceiling (internal/cli/reaper.go's
	// reaperEnvOptions).
	for _, want := range [][2]string{
		{"name", `"FUGARO_GCP_PROJECT"`},
		{"name", `"FUGARO_REGION"`},
		// The marker that tells the command it is the job and not a shell:
		// without it the environment path is a dry run
		// (internal/cli/reaper.go's reaperEnvOptions), and the two variables
		// above are the history job's.
		{"name", `"FUGARO_REAPER_JOB"`},
		{"value", `"1"`},
	} {
		if !hasAttr(job.body, want[0], want[1]) {
			t.Errorf("%s: %s does not set %s = %s", path, job.name, want[0], want[1])
		}
	}
	// And both are filled from this root's own project and region, which is
	// what makes the module the Batch provisioning rather than a second
	// config: two env values, one per variable.
	for _, want := range []string{"var.project", "var.region"} {
		if !regexp.MustCompile(`(?m)^\s*value\s*=\s*` + regexp.QuoteMeta(want) + `\s*$`).MatchString(job.body) {
			t.Errorf("%s: %s does not fill an environment value from %s", path, job.name, want)
		}
	}
	// The name must not look like a workflow's: ls and max_parallel count the
	// `fugaro-` jobs as runs (internal/infra/firebase.go's HistoryJob says why).
	if m := regexp.MustCompile(`(?m)^\s*reaper_job\s*=\s*"([^"]+)"`).FindStringSubmatch(string(b)); m == nil {
		t.Errorf("%s: no literal reaper_job name: the job's name is a local, and this test is what reads it back", path)
	} else if strings.HasPrefix(m[1], "fugaro-") {
		t.Errorf("%s: the reaper's job is named %q, which `fugaro ls` and max_parallel read as a workflow's job", path, m[1])
	}

	runners := resourceBlocks(t, path, b, "google_cloud_run_v2_job_iam_member")
	if len(runners) != 1 || runners[0].name != "google_cloud_run_v2_job_iam_member.reaper_runner" {
		t.Fatalf("%s declares %d google_cloud_run_v2_job_iam_member blocks (%v), want the invoker's one", path, len(runners), blockNames(runners))
	}
	if !hasAttr(runners[0].body, "role", `"roles/run.invoker"`) ||
		!hasAttr(runners[0].body, "member", "google_service_account.reaper.member") ||
		// On this job and no other: the name is the resource's, not a literal.
		!hasAttr(runners[0].body, "name", "google_cloud_run_v2_job.reaper.name") {
		t.Errorf("%s: %s must grant roles/run.invoker on the reaper's own job to the reaper account, which is the account the schedule mints the token from", path, runners[0].name)
	}

	schedules := resourceBlocks(t, path, b, "google_cloud_scheduler_job")
	if len(schedules) != 1 || schedules[0].name != "google_cloud_scheduler_job.reaper_sweep" {
		t.Fatalf("%s declares %d google_cloud_scheduler_job blocks (%v), want the sweep's one", path, len(schedules), blockNames(schedules))
	}
	sweep := schedules[0]
	for _, want := range [][2]string{
		// ReapMargin is 15 minutes: a run that should have ended is swept at
		// most one margin late, and a slower schedule would leave a stuck
		// job's VM alive for its own period PLUS the margin.
		{"schedule", `"*/15 * * * *"`},
		{"time_zone", `"Etc/UTC"`},
		{"retry_count", "0"},
		{"http_method", `"POST"`},
		// The SCHEDULER region and never var.region. Cloud Scheduler does not
		// serve every Cloud Run region -- us-east5, this module's own
		// variables block and its tfvars testdata, is one it does not -- and a
		// Scheduler job created in a region it does not serve fails the whole
		// apply, so the sweep would not merely be unscheduled: it would not
		// exist, and the leaked VMs it is here to delete would be swept by
		// nothing. The URI below is the other half of the split and stays the
		// Cloud Run job's own location.
		{"region", "var.scheduler_region"},
	} {
		if !hasAttr(sweep.body, want[0], want[1]) {
			t.Errorf("%s: %s does not set %s = %s", path, sweep.name, want[0], want[1])
		}
	}
	uri := "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.reaper.location}/jobs/${google_cloud_run_v2_job.reaper.name}:run"
	if !strings.Contains(sweep.body, uri) {
		t.Errorf("%s: %s does not start the reaper's own job: want the URI %s", path, sweep.name, uri)
	}
	if !regexp.MustCompile(`(?s)oauth_token\s*\{[^}]*service_account_email\s*=\s*google_service_account\.reaper\.email`).MatchString(sweep.body) {
		t.Errorf("%s: %s does not call the job as the reaper account", path, sweep.name)
	}
	// The token is minted by Cloud Scheduler's own agent, which needs actAs on
	// the account: without the grant the schedule fails with PERMISSION_DENIED
	// before the job is ever started.
	agents := resourceBlocks(t, path, b, "google_service_account_iam_member")
	found := false
	for _, blk := range agents {
		if blk.name == "google_service_account_iam_member.reaper_scheduler_agent" &&
			hasAttr(blk.body, "member", "local.cloud_scheduler_agent") &&
			hasAttr(blk.body, "role", `"roles/iam.serviceAccountUser"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("%s: no actAs grant for Cloud Scheduler's agent on the reaper account: minting the job's OAuth token needs it", path)
	}
}

// blockNames lists the addresses of blocks, for a failure message that says
// which one the scan found.
func blockNames(blks []block) []string {
	out := make([]string, 0, len(blks))
	for _, b := range blks {
		out = append(out, b.name)
	}
	return out
}

// The preload builder's role is the build's own calls and nothing more, and
// it is granted on the project to the preload account only.
//
// The list is longer than the build's four calls because a create is checked
// argument by argument: the boot disk (compute.disks.create), the interface on
// the named subnetwork (compute.subnetworks.use), the startup script
// (compute.instances.setMetadata), the labels (compute.instances.setLabels),
// the run account the VM runs as (compute.instances.setServiceAccount) and the
// image it boots from (compute.images.useReadOnly) are each their own
// permission. What it does NOT carry is the point: no instance or image admin
// role, no compute.disks.delete (the boot disk is the instance's own and goes
// with it), and nothing that could create a job or a second VM.
//
// The two *Operations.get permissions are the ones a list like this is easy to
// get wrong, because nothing in the build's own names suggests them: a create
// call is authorized by its own permission and then WAITS on an operation, and
// polling that operation is checked against compute.zoneOperations.get (zonal:
// the instance, its disk) or compute.globalOperations.get (global: images are
// global resources, so images.create and images.delete both wait on a global
// operation — cloud.google.com/compute/docs/reference/rest/v1/images/insert,
// checked 2026-10-16). Missing the global one leaves images.create authorized
// and the call still failing, which is every pass that makes a generation; the
// sweep's delete fails the same way and is swallowed by its `|| true`, so a
// stale generation would never be removed and C19's stale-image half would
// never happen.
//
// setMetadata is the one permission here wider than the build's own use of it
// -- it is also how SSH keys reach a VM -- and it is pinned as an exception
// rather than forbidden, because the build has no version of itself without
// it: a builder VM with no startup script pulls nothing (design §1.2). What
// bounds it is the holder: exactly one account, the preload builder's, which
// nothing but the scheduled rebuild and its Cloud Build step runs as.
func TestComputePreloadBuilderRoleAreExactlyTheBuild(t *testing.T) {
	want := []string{
		"compute.disks.create",
		"compute.disks.useReadOnly",
		"compute.globalOperations.get",
		"compute.images.create",
		"compute.images.delete",
		"compute.images.get",
		"compute.images.getFromFamily",
		"compute.images.list",
		"compute.images.setLabels",
		"compute.images.useReadOnly",
		"compute.instances.create",
		"compute.instances.delete",
		"compute.instances.get",
		"compute.instances.getSerialPortOutput",
		"compute.instances.list",
		"compute.instances.setLabels",
		"compute.instances.setMetadata",
		"compute.instances.setServiceAccount",
		"compute.instances.stop",
		"compute.subnetworks.use",
		"compute.zoneOperations.get",
	}
	found := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_custom_role") {
			if blk.name != "google_project_iam_custom_role.preload_builder" {
				continue
			}
			found++
			if got := permissionsOf(t, path, blk); !slices.Equal(got, want) {
				t.Errorf("%s: the preload builder holds %v, want exactly %v", path, got, want)
			}
		}
	})
	if found != 1 {
		t.Errorf("found %d preload builder roles, want 1", found)
	}
}

// The builder VM is placed on the network and the subnetwork the module
// creates its NAT on, and on no other: the VM has no external IP, so a
// placement anywhere else is a VM with no egress, and the pull the whole
// preload exists for would never finish (design §1.7).
func TestComputePreloadBuildsOnTheNATsNetwork(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	// The create command itself -- its line and its continuation lines -- and
	// not the script: a comment that mentions a flag is not the flag.
	create := shellCommand(t, string(b), `gcloud compute instances create "$BUILDER"`)
	for _, want := range []string{`--network="$NETWORK"`, `--subnet="$SUBNETWORK"`, "--no-address"} {
		if !slices.Contains(strings.Fields(create), want) {
			t.Errorf("the builder VM's create does not name %s: %s", want, create)
		}
	}
	// And the two it names are the module's own inputs, passed to the step.
	tf, err := fs.ReadFile(FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NETWORK=${var.network}", "SUBNETWORK=${var.subnetwork}"} {
		if !strings.Contains(string(tf), want) {
			t.Errorf("the preload build's environment does not pass %s to the script", want)
		}
	}
}

// shellCommand is the shell command in script that starts with start: its
// line and every continuation line after it, comments excluded.
func shellCommand(t *testing.T, script, start string) string {
	t.Helper()
	var cmd []string
	in := false
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if !in {
			if !strings.HasPrefix(l, start) {
				continue
			}
			in = true
		}
		cmd = append(cmd, strings.TrimSuffix(l, "\\"))
		if !strings.HasSuffix(l, "\\") {
			break
		}
	}
	if !in {
		t.Fatalf("the script no longer runs %s", start)
	}
	return strings.Join(cmd, " ")
}

// The built-in roles the design forbids are granted nowhere, and the one
// batch.jobsEditor binding is narrowed by the Condition 57a-9 read back as
// written (2026-10-15): a resource-name prefix, never the bare project-wide
// grant. An unconditioned jobsEditor is the launcher-shaped power to run any
// container in the project with any image (design §3.7).
func TestComputeLauncherJobsEditorIsConditioned(t *testing.T) {
	for _, bad := range []string{"roles/batch.jobsEditor.limited", "roles/compute.instanceAdmin.v1", "roles/compute.admin", "roles/editor", "roles/owner"} {
		walk(t, func(path string, b []byte) {
			for _, blk := range allResourceBlocks(t, path, b) {
				if anyIAMRE.MatchString(blk.typ) && strings.Contains(blk.body, `"`+bad+`"`) {
					t.Errorf("%s: %s grants %s", path, blk.name, bad)
				}
			}
		})
	}
	launchers, conditioned := 0, 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if !hasAttr(blk.body, "role", `"roles/batch.jobsEditor"`) {
				continue
			}
			launchers++
			if path != "gcp/modules/compute/iam.tf" || blk.name != "google_project_iam_member.launcher_jobs_editor" {
				t.Errorf("%s: %s grants Batch Job Editor and is not the compute module's launcher binding", path, blk.name)
				continue
			}
			if !strings.Contains(blk.body, "condition {") || !strings.Contains(blk.body, "${local.job_name_prefix}") {
				t.Errorf("%s: %s grants Batch Job Editor with no Condition on the job-name prefix", path, blk.name)
				continue
			}
			conditioned++
		}
	})
	if launchers != 1 || conditioned != 1 {
		t.Errorf("found %d batch.jobsEditor bindings, %d of them conditioned, want 1 and 1", launchers, conditioned)
	}
}

// The run-class accounts hold the two roles Batch requires of a job's service
// account (agentReporter, logWriter) plus the reader grant on the repository
// the PIN names -- and the pin's repository is a variable, never a constant:
// on 2026-10-15 it was the remote fugaro-ghcr, not fugaro-base (§7.2).
func TestComputeRunAccountsHoldTheRunRoles(t *testing.T) {
	want := map[string]string{
		"google_project_iam_member.run_agent_reporter": "roles/batch.agentReporter",
		"google_project_iam_member.run_log_writer":     "roles/logging.logWriter",
	}
	seen := map[string]bool{}
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			role, ok := want[blk.name]
			if !ok {
				continue
			}
			seen[blk.name] = true
			if !hasAttr(blk.body, "role", `"`+role+`"`) {
				t.Errorf("%s: %s must grant %s", path, blk.name, role)
			}
			if !hasAttr(blk.body, "for_each", "var.run_accounts") || !hasAttr(blk.body, "member", "each.value") {
				t.Errorf("%s: %s must grant the role to each run account", path, blk.name)
			}
		}
		for _, blk := range resourceBlocks(t, path, b, "google_artifact_registry_repository_iam_member") {
			if !strings.Contains(blk.name, "registry_reader") {
				continue
			}
			if !hasAttr(blk.body, "repository", "var.registry_repository") {
				t.Errorf("%s: %s must name the repository the pin names, never a constant (§7.2)", path, blk.name)
			}
			if strings.Contains(blk.body, `"fugaro-base"`) || strings.Contains(blk.body, `"fugaro-ghcr"`) {
				t.Errorf("%s: %s writes the repository out by hand", path, blk.name)
			}
		}
	})
	for name := range want {
		if !seen[name] {
			t.Errorf("no %s grant found", name)
		}
	}
}

// Creating the builder VM names a run-class account, and the caller is the
// build STEP -- which runs as the preload account. So the preload account
// holds actAs on each run account, and nothing holds it on a run account
// directly: no grant here names Cloud Build's own agent.
//
// That is the shorter path this binding closes, not a claim that a build
// cannot reach a run account: naming the preload account as a build's
// serviceAccount takes actAs on that account (which the operators hold, and
// the account holds on itself for the scheduled call), and such a build boots
// a builder VM as a run account. The chain is operator -> build -> run
// account, one reviewed step at a time, and the agent -- which any principal
// who can submit a build in this project drives -- holds no run account.
func TestComputeThePreloadBuildActsAsTheRunAccountsOnly(t *testing.T) {
	users := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_service_account_iam_member") {
			if !strings.Contains(blk.name, "run_account") {
				continue
			}
			users++
			if path != "gcp/modules/compute/iam.tf" || blk.name != "google_service_account_iam_member.preload_run_account_user" {
				t.Errorf("%s: %s grants actAs on a run account", path, blk.name)
				continue
			}
			if !hasAttr(blk.body, "for_each", "var.run_accounts") || !hasAttr(blk.body, "member", "google_service_account.preload.member") {
				t.Errorf("%s: %s must grant the run accounts' actAs to the preload account", path, blk.name)
			}
			if strings.Contains(blk.body, "cloud_build_agent") {
				t.Errorf("%s: %s grants Cloud Build's agent actAs on a run account", path, blk.name)
			}
		}
	})
	if users != 1 {
		t.Errorf("found %d actAs grants on the run accounts, want 1", users)
	}
}

// The job's no-external-IP posture needs BOTH the network and the subnetwork
// named (measured 2026-10-15: Batch refuses the field without them), so both
// are required variables with a shape check and neither has a default, and
// the NAT is created on exactly those two.
func TestComputeNetworkAndSubnetworkAreExplicit(t *testing.T) {
	vars, err := fs.ReadFile(FS, "gcp/modules/compute/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"network", "subnetwork"} {
		blk := regexp.MustCompile(`(?s)variable "` + v + `" \{(.*?)\n\}`).FindStringSubmatch(string(vars))
		if blk == nil {
			t.Fatalf("variable %q is not declared", v)
		}
		if strings.Contains(blk[1], "default") {
			t.Errorf("variable %q has a default: Batch refuses noExternalIpAddress unless both are named explicitly (design §1.7, §7.2)", v)
		}
		if !strings.Contains(blk[1], "validation {") {
			t.Errorf("variable %q has no shape check", v)
		}
	}
	nats, subnetworks := 0, 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_compute_router_nat") {
			nats++
			if !strings.Contains(blk.body, "subnetwork {") || !hasAttr(blk.body, "name", "var.subnetwork") {
				t.Errorf("%s: %s must NAT the subnetwork the job's VMs are placed in", path, blk.name)
			}
			subnetworks++
		}
		for _, blk := range resourceBlocks(t, path, b, "google_compute_router") {
			if !hasAttr(blk.body, "network", "var.network") {
				t.Errorf("%s: %s must sit on the network the job's VMs are placed on", path, blk.name)
			}
		}
	})
	if nats != 1 || subnetworks != 1 {
		t.Errorf("found %d NATs over %d subnetworks, want 1 and 1", nats, subnetworks)
	}
}

// streaming's API is NOT enabled: the design takes the preloaded boot image,
// and containerfilesystem is streaming's prerequisite only (§1.2, C4). A run
// that needs no socket could stream one day; nothing here does.
func TestComputeDoesNotEnableImageStreaming(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/apis.tf")
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))
	if strings.Contains(code, "containerfilesystem.googleapis.com") {
		t.Error("the compute module enables containerfilesystem.googleapis.com: the design takes the preload, not streaming")
	}
	for _, want := range []string{"compute.googleapis.com", "batch.googleapis.com"} {
		if !strings.Contains(code, want) {
			t.Errorf("the compute module does not enable %s", want)
		}
	}
}

// The preload build is a job on a schedule, and the script it runs is the one
// measured procedure: Google's own base family, a pull into the VM's Docker
// data root, the serial-port marker, then the image. The build must never
// delete a VM or an image that is not its own, so every delete in the script
// is guarded by the fugaro=managed label.
func TestComputePreloadBuildFollowsTheMeasuredProcedure(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(b)
	for _, want := range []string{
		"describe-from-family",   // the family is resolved in Google's project
		"docker pull",            // the pull is what fills the Docker data root
		"get-serial-port-output", // the wait, because the VM has no external IP
		"instances stop",         // the disk must be detached before imaging
		"images create",          // --source-disk
		"instances delete",       // the builder always goes
		"fugaro=managed",         // the ownership mark on both
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("preload.sh does not %q: it is not 57a-3b's procedure", want)
		}
	}
	if n := strings.Count(sh, "--labels=fugaro=managed"); n < 2 {
		t.Errorf("preload.sh marks its own VM and image %d times, want at least 2", n)
	}
	for _, line := range strings.Split(sh, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(line, "delete") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "say ") {
			continue
		}
		if !strings.Contains(line, `"$BUILDER"`) && !strings.Contains(line, `"$img"`) {
			t.Errorf("preload.sh deletes something that is not its builder VM or a fugaro-labelled image of its own prefix: %s", trimmed)
		}
	}
	j, err := fs.ReadFile(FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	tf := string(j)
	if !strings.Contains(tf, `file("${path.module}/preload.sh")`) {
		t.Error("the build does not run the committed script")
	}
	if !strings.Contains(tf, "google_cloud_scheduler_job") || !strings.Contains(tf, "cloudbuild.googleapis.com/v1/projects/") {
		t.Error("the preload has no scheduled rebuild calling Cloud Build")
	}
	if !strings.Contains(tf, "sha256(var.base_image)") {
		t.Error("the preload image's name must carry the pinned base version: one image per pin")
	}
}

// The rebuild replaces what is stale, or the schedule is decoration. The
// image's FAMILY carries the pin's hash and the pin does not move when Google
// rolls the base family, so the family name cannot say which release is inside
// its newest generation and an image that is already there is not
// automatically current: what it was built from is recorded on it (its
// base-release label) and how old it is is its creation date, both read back
// from the API, and a generation that is stale or past the refresh window is
// replaced by a NEW generation in the same family. The window is an input with
// a ceiling below the 60 days Batch documents for a custom image (§1.2, C19),
// because a window at or past the lifetime would leave the image unsupported
// before anything replaced it.
func TestComputePreloadRebuildsAStaleImage(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(b)
	for _, want := range []string{
		// The five answers the decision reads: the release it was built from,
		// its age, and the socket gid it recorded (Task 9 -- a generation with
		// no gid recorded is replaced whatever its age, so a run's
		// `--group-add` is never a guess).
		"csv[no-heading](name,labels.fugaro,labels.base-release,creationTimestamp,labels.${SOCKET_GID_LABEL})",
		"IFS=, read -r",                         // and read as columns, not one joined line
		"REFRESH_DAYS:?",                        // the window is an input, not a literal
		`--labels=fugaro=managed,base-release=`, // what the generation was built from is recorded on it
		`--family="$IMAGE_FAMILY"`,              // the new generation joins the family the runs boot
		`describe-from-family "$IMAGE_FAMILY"`,  // and the decision is read from that family
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("preload.sh does not name %q: the scheduled rebuild would leave a stale image in place", want)
		}
	}
	// The decision is made from the API's answers, and it is the api's: the
	// newest generation in the family, the release it holds, how old it is.
	describe := strings.Index(sh, `describe-from-family "$IMAGE_FAMILY"`)
	release := strings.Index(sh, "labels.base-release")
	create := strings.Index(sh, "gcloud compute images create")
	if describe < 0 || release < describe || create < release {
		t.Errorf("the new generation is not created after the describe that decides it (describe %d, release %d, create %d)", describe, release, create)
	}
	// The replacement needs nothing of the previous generation, so the new one
	// exists BEFORE any image is deleted: a delete first would leave the
	// installation with no image at all if the create then failed, and the one
	// delete the script makes outside its own prefix sweep is the sweep's.
	if del := strings.Index(sh, `gcloud compute images delete "$img"`); del < create {
		t.Errorf("an image is deleted before the new generation exists (create %d, delete %d)", create, del)
	}
	if n := strings.Count(sh, "compute images delete"); n != 1 {
		t.Errorf("preload.sh deletes images in %d places, want 1 (the sweep, which never deletes the family's newest)", n)
	}
	// The generation is only created once the pull has finished: an image
	// taken from a disk whose pull has not finished is an image with nothing
	// in it, and the runs would silently pay the pull the preload exists to
	// remove.
	pull := strings.Index(sh, "for _ in $(seq 1 60)")
	keep := strings.Index(sh, `if [ -n "$BUILT" ]; then`)
	if pull < 0 || keep < pull || create < keep {
		t.Errorf("the new generation is not created inside the guard that follows the pull (pull %d, guard %d, create %d)", pull, keep, create)
	}
	// An image under this family that this build did not make is left alone:
	// the refusal names the label, and it comes before anything is created.
	refuse := strings.Index(sh, "is not fugaro=managed; refusing to touch it")
	if refuse < describe || refuse > create {
		t.Errorf("preload.sh does not refuse an image that is not its own before replacing it (describe %d, refusal %d, create %d)", describe, refuse, create)
	}
	// A newest generation that is its own, current and inside the window is
	// the only thing left alone, and that is said, not assumed: both outcomes
	// are inside the conditional, so an unconditional "nothing to build" --
	// the bug this test exists for, a scheduled rebuild that never refreshed
	// anything -- fails here.
	if !strings.Contains(sh, "nothing to build") {
		t.Error("preload.sh never reports the one case it leaves an image alone in")
	}
	decide := regexp.MustCompile(`(?s)if \[ "\$IMG_RELEASE" = "\$RELEASE_LABEL" \].*?\nfi`).FindString(sh)
	if decide == "" {
		t.Fatal("preload.sh does not decide the rebuild from the release the image holds")
	}
	for _, want := range []string{`[ "$IMG_AGE_DAYS" -lt "$REFRESH_DAYS" ]`, `BUILD=""`} {
		if !strings.Contains(decide, want) {
			t.Errorf("the rebuild's decision does not hold %q: an image is only left alone when it is current AND inside the window", want)
		}
	}

	// The window arrives from the module, and the module's and the root's
	// defaults are inside the lifetime Batch documents.
	tf, err := fs.ReadFile(FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tf), "REFRESH_DAYS=${var.preload_refresh_days}") {
		t.Error("the build's environment does not pass the refresh window to the script")
	}
	if !strings.Contains(string(tf), "retry_config") {
		t.Error("the scheduled rebuild has no retry: a failed replacement would wait a week")
	}
	for _, path := range []string{"gcp/modules/compute/variables.tf", "gcp/roots/compute/variables.tf"} {
		vars, err := fs.ReadFile(FS, path)
		if err != nil {
			t.Fatal(err)
		}
		blk := regexp.MustCompile(`(?s)variable "preload_refresh_days" \{(.*?)\n\}`).FindStringSubmatch(string(vars))
		if blk == nil {
			t.Errorf("%s does not declare preload_refresh_days", path)
			continue
		}
		if !regexp.MustCompile(`(?m)^\s*default\s*=\s*30\s*$`).MatchString(blk[1]) {
			t.Errorf("%s's preload_refresh_days default is not the 30 days the Go side sends", path)
		}
	}
	moduleVars, err := fs.ReadFile(FS, "gcp/modules/compute/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moduleVars), "var.preload_refresh_days < 60") {
		t.Error("the module does not refuse a refresh window at or past the 60-day supported lifetime")
	}
}

// The run accounts are the only holders of a reader grant on the installation's
// registry, and the preload's own pull is one of them: the builder VM boots as
// a run-class account (preload.sh's --service-account) and the docker pull runs
// in its startup script, so the preload ACCOUNT reads nothing from that
// repository -- the build step's own image is Google's, from gcr.io. A grant to
// it would be a binding with no reader: the project hands out a role whose
// permissions nothing in the installation ever exercises.
func TestComputeTheRegistryReaderHasOneHolder(t *testing.T) {
	grants := 0
	walk(t, func(path string, b []byte) {
		if !strings.HasPrefix(path, "gcp/modules/compute/") {
			return
		}
		for _, blk := range resourceBlocks(t, path, b, "google_artifact_registry_repository_iam_member") {
			grants++
			if blk.name != "google_artifact_registry_repository_iam_member.run_registry_reader" {
				t.Errorf("%s: %s grants a registry role in the compute module, and every reader grant there is the run accounts'", path, blk.name)
				continue
			}
			if !hasAttr(blk.body, "for_each", "var.run_accounts") || !hasAttr(blk.body, "member", "each.value") {
				t.Errorf("%s: %s must grant the reader role to each run account", path, blk.name)
			}
			if strings.Contains(blk.body, "google_service_account.preload") {
				t.Errorf("%s: %s grants the reader role to the preload account, which pulls nothing from this repository", path, blk.name)
			}
		}
	})
	if grants != 1 {
		t.Errorf("found %d registry grants in the compute module, want 1 (the run accounts' reader)", grants)
	}
}

// Cloud Build's own agent is the SERVICE AGENT
// service-<project number>@gcp-sa-cloudbuild.iam.gserviceaccount.com
// (cloud.google.com/build/docs/securing-builds/configure-access-for-cloud-build-service-account,
// checked 2026-10-16). service-<number>@cloudbuild.gserviceaccount.com is no
// principal at all -- nothing holds that address -- and the form that does
// exist, the bare <number>@cloudbuild.gserviceaccount.com, is the LEGACY
// default BUILD account, a build identity an organization may choose; granting
// it actAs on the preload account would hand a build identity this module never
// uses the power to run as the preload account
// (cloud.google.com/build/docs/cloud-build-service-account, checked
// 2026-10-16). The repo's own tokensigners.go draws the same line: the
// service- form is recognised only under gcp-sa-* domains, and cloudbuild's
// legacy domain only in the bare-number form.
func TestComputePreloadCloudBuildAgentIsTheServiceAgent(t *testing.T) {
	iam, err := fs.ReadFile(FS, "gcp/modules/compute/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	want := `cloud_build_agent     = "serviceAccount:service-${var.project_number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"`
	if !strings.Contains(string(iam), want) {
		t.Errorf("the Cloud Build principal is not the service agent:\n  want %s", want)
	}
	if strings.Contains(string(iam), `service-${var.project_number}@cloudbuild.gserviceaccount.com`) {
		t.Error("the module names service-<number>@cloudbuild.gserviceaccount.com, which is not a principal")
	}
	// And no file in the tree grants anything to that address.
	walk(t, func(path string, b []byte) {
		if !strings.Contains(string(b), "cloudbuild.gserviceaccount.com") {
			return
		}
		for _, m := range regexp.MustCompile(`serviceAccount:[^"\s]*cloudbuild\.gserviceaccount\.com`).FindAllString(string(b), -1) {
			if !strings.Contains(m, "gcp-sa-cloudbuild") && !regexp.MustCompile(`^serviceAccount:[0-9]+@cloudbuild`).MatchString(m) {
				t.Errorf("%s: %s is not a Cloud Build principal: the service agent carries the gcp-sa- prefix, and the only other form is the legacy bare-number build account", path, m)
			}
		}
	})
}

// The build config is substitution-processed before any step runs, so every
// `$` in the inlined script has to be doubled: one unescaped `$VAR` that is
// not a built-in substitution makes the Cloud Build API reject the build with
// INVALID_ARGUMENT before a step starts
// (cloud.google.com/build/docs/configuring-builds/substitute-variable-values,
// checked 2026-10-16: "To use an expression starting with $ you must use $$",
// and $FOO "is invalid since it is not a built-in substitution"). The script
// is shell, so it is full of them: every $PROJECT, ${...:?} and $(...) in
// preload.sh is Cloud Build's to reject.
func TestComputePreloadEscapesItsScriptForCloudBuild(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	tf := string(b)
	if !strings.Contains(tf, `preload_script = replace(file("${path.module}/preload.sh"), "$", "$$")`) {
		t.Error(`the inlined script is not escaped: it must be replace(file("${path.module}/preload.sh"), "$", "$$")`)
	}
	if !strings.Contains(tf, `args       = ["-c", local.preload_script]`) {
		t.Error("the build step does not run the escaped script: args must use local.preload_script, not file(...)")
	}
	sh, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	// The escape is load-bearing: the raw script DOES carry unescaped
	// substitutions, so escaping it is not a formality.
	raw := unescapedSubstitutions(string(sh))
	if len(raw) == 0 {
		t.Fatal("preload.sh holds no $ at all: this test no longer says anything about the escape")
	}
	escaped := unescapedSubstitutions(strings.ReplaceAll(string(sh), "$", "$$"))
	if len(escaped) != 0 {
		t.Errorf("the escaped script still holds %d unescaped substitutions, starting at %q: Cloud Build would reject the build", len(escaped), escaped[0])
	}
}

// unescapedSubstitutions returns the `$`-expressions of a Cloud Build config
// that its substitution pass would read as substitutions: every `$` except the
// `$$` escape. A built-in substitution ($BUILD_ID and friends) would be a
// false positive here; the build config uses none.
func unescapedSubstitutions(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			i++
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		if c := s[i+1]; c == '{' || c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			end := i + 2
			for end < len(s) && (s[end] == '_' || (s[end] >= '0' && s[end] <= '9') || (s[end] >= 'A' && s[end] <= 'Z') || (s[end] >= 'a' && s[end] <= 'z')) {
				end++
			}
			out = append(out, s[i:end])
		}
	}
	return out
}

// With a user-specified service account the build's logs must go to a Cloud
// Storage bucket or a Cloud Logging bucket in the project, and the value that
// names the latter is CLOUD_LOGGING_ONLY. CLOUD_LOGGING is not a value of that
// enum at all, so the API rejects the build
// (cloud.google.com/build/docs/securing-builds/configure-user-specified-service-accounts,
// checked 2026-10-16).
func TestComputePreloadLogsWithAValueTheAPIHas(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.tf")
	if err != nil {
		t.Fatal(err)
	}
	tf := string(b)
	if !strings.Contains(tf, `options = { logging = "CLOUD_LOGGING_ONLY" }`) {
		t.Error("the build's logging option is not CLOUD_LOGGING_ONLY: a build run as a user-specified service account must log to Cloud Logging, and CLOUD_LOGGING is not a value of the enum")
	}
	if regexp.MustCompile(`logging\s*=\s*"CLOUD_LOGGING"`).MatchString(tf) {
		t.Error("the build's logging option is CLOUD_LOGGING, which the Cloud Build API does not have")
	}
}

// The scheduled call and the build it creates are the same account: Cloud
// Scheduler calls the Cloud Build API with an OAuth token minted from the
// preload account (preload.tf), and the build it creates names that account as
// its serviceAccount. Google's note on that case is that the account "must
// still have the iam.serviceAccounts.actAs permission"
// (cloud.google.com/build/docs/cloud-build-service-account, checked
// 2026-10-16), and a service account does not hold actAs on itself by default:
// without this binding both the weekly rebuild and the first build the stage
// starts fail with PERMISSION_DENIED before any step runs.
func TestComputeThePreloadAccountActsAsItself(t *testing.T) {
	found := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_service_account_iam_member") {
			if path != "gcp/modules/compute/iam.tf" {
				continue
			}
			if !hasAttr(blk.body, "member", "google_service_account.preload.member") {
				continue
			}
			// Both sides name the preload account: the run accounts' binding
			// has the same member and a run account as its target, so it is
			// not this one.
			if !hasAttr(blk.body, "service_account_id", "google_service_account.preload.name") {
				continue
			}
			found++
			if blk.name != "google_service_account_iam_member.preload_self_user" ||
				!hasAttr(blk.body, "role", `"roles/iam.serviceAccountUser"`) {
				t.Errorf("%s: %s must be actAs on the preload account, held by the preload account", path, blk.name)
			}
		}
	})
	if found != 1 {
		t.Errorf("found %d self-actAs bindings on the preload account, want 1", found)
	}
}

// A run's VM is a Docker HOST: its container talks to the host's daemon over
// the socket, and the host has the run's secrets in its environment. The
// installation's network is therefore not a boundary, and a default network
// arrives with default-allow-internal (priority 65534, 10.128.0.0/9, all
// protocols): without this rule any VM in the project could open every port on
// a run's VM. The rule matches first, and it is targeted by the run accounts
// themselves -- a network tag would have to be written by the launcher
// (Task 7), and a rule that silently matches nothing is worse than no rule.
func TestComputeRunVMsTakeNoIngress(t *testing.T) {
	rules := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_compute_firewall") {
			if path != "gcp/modules/compute/network.tf" {
				continue
			}
			rules++
			if !hasAttr(blk.body, "direction", `"INGRESS"`) || !hasAttr(blk.body, "network", "var.network") {
				t.Errorf("%s: %s must be an ingress rule on the network the runs are placed on", path, blk.name)
			}
			if !hasAttr(blk.body, "target_service_accounts", `[for a in var.run_accounts : trimprefix(a, "serviceAccount:")]`) {
				t.Errorf("%s: %s must target the run accounts themselves", path, blk.name)
			}
			if !strings.Contains(blk.body, "deny {") || !strings.Contains(blk.body, `protocol = "all"`) {
				t.Errorf("%s: %s must deny every protocol, not just ssh", path, blk.name)
			}
			// Every source, not the network's internal range: a deny wins
			// over an allow of a higher priority number, and this rule is
			// what stops a project-level allow (default-allow-ssh, or one a
			// launcher added) reaching a run's VM. Narrowing the source
			// ranges to 10.128.0.0/9 is the fallback the module's comment
			// reserves for 57b-1's answer, and it is a change of the §3.2
			// control: it must be made here, in the open, and in the tftest's
			// TestTheRunVMsTakeNoIngress with it.
			if !hasAttr(blk.body, "source_ranges", `["0.0.0.0/0"]`) {
				t.Errorf("%s: %s must deny ingress from every source, not from a range of them", path, blk.name)
			}
			// A lower number wins, and default-allow-internal is 65534.
			m := regexp.MustCompile(`(?m)^\s*priority\s*=\s*([0-9]+)\s*$`).FindStringSubmatch(blk.body)
			if m == nil {
				t.Fatalf("%s: %s sets no priority, so it would be appended after the default rules", path, blk.name)
			}
			p, err := strconv.Atoi(m[1])
			if err != nil || p >= 65534 {
				t.Errorf("%s: %s has priority %s, which is not lower than default-allow-internal's 65534 and would therefore not win", path, blk.name, m[1])
			}
		}
	})
	if rules != 1 {
		t.Errorf("found %d firewall rules in the compute module, want 1 (the run VMs' deny-ingress)", rules)
	}
}

// The runs boot a FAMILY, and a rebuild adds a generation to it: the previous
// generation keeps serving until the new one exists, so no build ever leaves
// the installation without an image, and a job's reference never moves
// (cloud.google.com/compute/docs/images/image-families-best-practices, checked
// 2026-10-16). The script's own sweep deletes only what it must: images of its
// own prefix that are neither the generation it just made nor the newest of
// the family the runs boot.
func TestComputePreloadKeepsAFamilyAndSweepsGenerations(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(b)
	// The builder VM does not outlive the build, whatever ends it: the trap is
	// the backstop for an abort between the create and step 7.
	if !strings.Contains(sh, "trap cleanup EXIT") {
		t.Error("preload.sh sets no EXIT trap: an abort after the builder VM is created leaks the VM and its disk")
	}
	create := strings.Index(sh, "gcloud compute instances create")
	up := strings.Index(sh, `BUILDER_UP=yes`)
	if create < 0 || up < create {
		t.Error("preload.sh does not raise the flag the trap reads when it creates the builder VM")
	}
	// The sweep's rule: the newest of the family the runs boot, and the
	// generation this build just made, are never victims.
	for _, want := range []string{`-v keep="$NEW_IMAGE"`, `-v fam="$IMAGE_FAMILY"`, "gcloud compute images delete"} {
		if !strings.Contains(sh, want) {
			t.Errorf("the sweep does not name %q", want)
		}
	}
	sweep := regexp.MustCompile(`(?s)awk -F, -v keep=.*?print name\[i\]`).FindString(sh)
	if sweep == "" {
		t.Fatal("preload.sh has no sweep over the images it might replace")
	}
	if !strings.Contains(sweep, "name[i] == keep") || !strings.Contains(sweep, "family[i] == fam") {
		t.Error("the sweep does not keep the image it just created and the newest of the family")
	}
}

// The preload's pull is a run account's, so the image it pulls is one a run
// could pull: the pin's host, project and repository are the installation's
// own, and the module refuses a pin that disagrees with region, project or
// registry_repository rather than failing the build that pulls it.
func TestComputeBaseImageIsInTheInstallationsRegistry(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/compute/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	blk := regexp.MustCompile(`(?s)variable "base_image" \{(.*?)\n\}`).FindStringSubmatch(string(b))
	if blk == nil {
		t.Fatal("the compute module does not declare base_image")
	}
	// The cross-check names the three variables the path has to agree with,
	// not a constant: a literal host or repository here would drift from the
	// root the same way.
	for _, want := range []string{"${var.region}-docker", "${var.project}", "${var.registry_repository}"} {
		if !strings.Contains(blk[1], want) {
			t.Errorf("the base_image validation does not check the image against %s", want)
		}
	}
}

// The signer's own IAM is the launchers' and operators' minter grant on the
// signer account, and nothing else: no role on the project names it, and no
// other service account IAM resource grants on it.
func TestSignerIAMIsMinterOnly(t *testing.T) {
	grants := 0
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_service_account_iam_member") {
			if !strings.Contains(blk.body, "google_service_account.signer") {
				continue
			}
			grants++
			if blk.name != "google_service_account_iam_member.minter" || !strings.Contains(blk.body, "google_project_iam_custom_role.token_minter.name") {
				t.Errorf("%s: %s grants on the signer something other than the minter role", path, blk.name)
			}
		}
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if strings.Contains(blk.body, "google_service_account.signer") {
				t.Errorf("%s: %s grants a project role to the signer", path, blk.name)
			}
		}
	})
	if grants != 1 {
		t.Errorf("want one grant on the signer (minter), got %d", grants)
	}
}

// The history account's project roles are the installation's narrow Cloud Run
// role plus, on the Firebase project, exactly the four the sweep and the
// rollover need. In one project these are the only grants a Fugaro service
// account holds on the backend.
func TestHistoryAccountBackendRolesAreExactly(t *testing.T) {
	want := []string{"roles/datastore.user", "roles/firebaseauth.admin", "roles/firebasedatabase.admin", "roles/serviceusage.serviceUsageConsumer"}
	var got []string
	walk(t, func(path string, b []byte) {
		if !strings.HasPrefix(path, "gcp/modules/firebase/") {
			return
		}
		for _, blk := range resourceBlocks(t, path, b, "google_project_iam_member") {
			if strings.Contains(blk.body, "var.history_account") {
				ms := quotedRoleRE.FindAllStringSubmatch(blk.body, -1)
				if len(ms) != 1 {
					t.Errorf("%s: %s grants the history account %d literal roles, want exactly 1", path, blk.name, len(ms))
					continue
				}
				got = append(got, ms[0][1])
			}
		}
	})
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the history account holds %v on the Firebase project, want %v", got, want)
	}
}

// Two roots apply to one project in the same-project layout, and they must
// never own one address twice: the only resource types both could create are
// project services, and the Firebase module takes the shared ones out through
// skip_apis. Every other project-scoped singleton has a name of its own in
// each root (the Go side pins the names).
func TestFirebaseModuleSkipsSharedAPIs(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/modules/firebase/apis.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "setsubtract(local.apis, var.skip_apis)") {
		t.Error("the Firebase module does not subtract skip_apis from the APIs it enables")
	}
	for _, p := range []string{"gcp/modules/firebase/variables.tf", "gcp/roots/firebase/variables.tf"} {
		v, err := fs.ReadFile(FS, p)
		if err != nil || !strings.Contains(string(v), `variable "skip_apis"`) {
			t.Errorf("%s declares no skip_apis: %v", p, err)
		}
	}
}

// preloadFixture runs preload.sh against a fake gcloud and returns what the
// script printed, whether it exited cleanly, and every gcloud call it made.
//
// The fake answers the API the way the real one does and records the calls, so
// the script's DECISIONS run for real: its csv parsing, its comparison of the
// release the API reports against the one the family is on, its awk sweep, and
// its EXIT trap. The alternative -- asserting on the script's text -- cannot
// see the bug this exists for: a `read` without IFS split the comma-joined csv
// line into one variable, so every existing image looked like one that is not
// fugaro=managed and the build refused to touch it.
func preloadFixture(t *testing.T, env map[string]string) (out string, err error, calls []string) {
	t.Helper()
	script, err := fs.ReadFile(FS, "gcp/modules/compute/preload.sh")
	if err != nil {
		t.Fatal(err)
	}
	// preload.sh reads the API's timestamps with `date -d`, which is GNU: it
	// runs in Cloud Build's Debian image, and this test needs the same of its
	// own host to say anything.
	if exec.Command("date", "-u", "-d", "2020-01-01T00:00:00Z", "+%s").Run() != nil {
		t.Skip("no GNU date: preload.sh parses the API's creation timestamps with `date -d`")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash: preload.sh is a bash script")
	}
	dir := t.TempDir()
	sh := filepath.Join(dir, "preload.sh")
	if err := os.WriteFile(sh, script, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(dir, "calls")
	if err := os.WriteFile(callLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(dir, "created")
	if err := os.WriteFile(filepath.Join(bin, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", sh)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+callLog,
		"CREATED="+created,
		// The preload's own inputs, as the module passes them (preload.tf).
		"PROJECT=proj-1234",
		"ZONE=us-east5-a",
		"IMAGE_FAMILY=fugaro-preload-a0b6aca0",
		"IMAGE_PREFIX=fugaro-preload",
		"BASE_IMAGE=us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/acme/fugaro-base:1.2.3",
		"BASE_PROJECT=batch-custom-image",
		"BASE_FAMILY=batch-cos-stable-official",
		"BASE_RELEASE=batch-cos-stable-official-2026-10-01-00-00-00",
		"REGISTRY_HOST=us-east5-docker.pkg.dev",
		"RUN_ACCOUNT=fugaro-acme-web@proj-1234.iam.gserviceaccount.com",
		"MACHINE_TYPE=e2-medium",
		"NETWORK=projects/proj-1234/global/networks/default",
		"SUBNETWORK=projects/proj-1234/regions/us-east5/subnetworks/default",
		"REFRESH_DAYS=30",
		// The socket gid's label, as the build's substitution carries it
		// (preload.tf). preload.sh refuses to run without it.
		"SOCKET_GID_LABEL=socket-gid",
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(callLog)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	return string(b), err, calls
}

// fakeGcloud is the API the script talks to, as far as it talks to one: the two
// families, the instance calls, and the image calls. Every call is logged
// first, so a test can assert on their order as well as their set.
const fakeGcloud = `#!/bin/bash
printf '%s\n' "$*" >> "$CALLS"
args="$*"
case "$args" in
  *"images describe-from-family $BASE_FAMILY "*) printf '%s\n' "$BASE_RELEASE"; exit 0;;
esac
case "$args" in
  *"images describe-from-family "*)
      case "$args" in
        *"--format="*) printf '%s\n' "$IMAGE_ROW";;
      esac
      exit 0;;
  *"instances describe "*)
      # A leftover builder VM from an interrupted build, when the test names
      # one: its fugaro label is LEFTOVER_LABEL.
      [ -n "$LEFTOVER" ] || exit 1
      case "$args" in
        *"--format="*) printf '%s\n' "$LEFTOVER_LABEL";;
      esac
      exit 0;;
  *"instances create "*) exit 0;;
  *"instances get-serial-port-output "*)
      # The builder VM's serial output, as its startup script writes it: the
      # gid it read off its own socket, then the marker the build waits for.
      # NO_SOCKET_GID drops the first line, which is a build whose VM never
      # printed a gid. SERIAL_CRLF ends the lines the way the real serial
      # console does (a tty, so CRLF), which is the shape the gid parse has to
      # survive -- see the carriage-return case in the rebuild test.
      crlf=""
      if [ -n "$SERIAL_CRLF" ]; then crlf="$(printf '\r')"; fi
      if [ -z "$NO_SOCKET_GID" ]; then printf '%s%s\n' "${SOCKET_GID_LINE:-fugaro-preload-socket-gid=998}" "$crlf"; fi
      printf '%s%s\n' "$MARKER" "$crlf"; exit 0;;
  *"instances stop "*) exit 0;;
  *"instances delete "*)
      # A delete that will not go: step 7 tolerates it, and the flag it reads
      # is what decides whether the trap tries once more.
      if [ -n "$FAIL_INSTANCE_DELETE" ]; then printf 'the delete failed\n' >&2; exit 1; fi
      exit 0;;
  *"images create "*)
      if [ -n "$FAIL_CREATE" ]; then printf 'the create failed\n' >&2; exit 1; fi
      name="$(printf '%s\n' "$args" | awk '{print $4}')"
      printf '%s,%s\n' "$name" "$IMAGE_FAMILY" >> "$CREATED"
      exit 0;;
  *"images delete "*) exit 0;;
  *"images list "*)
      # The ownership filter is the SERVER's: gcloud applies --filter, so an
      # image without fugaro=managed is simply not in the answer to a filtered
      # list. The fake applies it too, out of IMAGE_LIST_UNMANAGED -- what a
      # list WITHOUT that filter would hand back -- so a script that stopped
      # asking for managed-only is given foreign images, and sweeps them.
      printf '%s\n' "$IMAGE_LIST"
      case "$args" in
        *labels.fugaro=managed*) ;;
        *) printf '%s\n' "$IMAGE_LIST_UNMANAGED";;
      esac
      [ -f "$CREATED" ] && cat "$CREATED"
      exit 0;;
esac
printf 'unexpected gcloud call: %s\n' "$args" >&2
exit 9
`

// gcloudCalls returns the calls in calls that ran the subcommand sub (as in
// "images create"), as their fields.
func gcloudCalls(t *testing.T, calls []string, sub string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range calls {
		f := strings.Fields(c)
		if len(f) >= 3 && strings.Join(f[1:3], " ") == sub {
			out = append(out, f)
		}
	}
	return out
}

// staleRow and currentRow are the API's answers about the family's newest
// generation, as csv[no-heading]: name, the ownership label, the release it was
// built from, its creation stamp, and the socket gid the generation recorded.
// The release is the fixture's, and the stamps are computed here rather than by
// the host's date, so the age the script reads is the age this test means.
//
// The gid column is preloadSocketGID and never 412: 412 is what Check 57a-6
// measured on the COS image it used, and a gid written into this tree would be
// a guess about whatever boot image an installation runs
// (TestComputeModuleRecordsTheSocketGID refuses both numbers in the module's
// files). A row without it stands for a generation built before the label
// existed, which the build replaces rather than keeps.
func preloadRow(t *testing.T, release string, age time.Duration) string {
	t.Helper()
	return preloadRowOwned(t, "managed", release, age)
}

// preloadRowOwned is preloadRow with the ownership label spelled out, for the
// rows that are NOT this module's: an image someone else built, or one from
// before the label existed.
func preloadRowOwned(t *testing.T, owner, release string, age time.Duration) string {
	t.Helper()
	return preloadRowGID(t, owner, release, age, preloadSocketGID)
}

// preloadSocketGID is the gid the fake VM's socket reports, and the one a
// generation's label carries back: any positive integer but 412, which is the
// measurement's and not an installation's (see preloadRow).
const preloadSocketGID = "998"

// preloadRowGID is preloadRowOwned with the gid column spelled out, for a row
// that records none.
func preloadRowGID(t *testing.T, owner, release string, age time.Duration, gid string) string {
	t.Helper()
	now := time.Now().UTC()
	return fmt.Sprintf("%s-%s,%s,%s,%s,%s", "fugaro-preload-a0b6aca0",
		now.Add(-age).Format("20060102T150405"), owner, release, now.Add(-age).Format(time.RFC3339), gid)
}

// imageListCall is the one `gcloud compute images list` the sweep makes, as
// the shell logged it (gcloud's own field splitting is not the script's).
func imageListCall(t *testing.T, calls []string) string {
	t.Helper()
	var got []string
	for _, c := range calls {
		if strings.Contains(c, "compute images list") {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("the sweep listed images %d times, want once:\n%s", len(got), strings.Join(calls, "\n"))
	}
	return got[0]
}

// The preload's family holds generations, and what the scheduled rebuild does
// with them decides whether a run boots the image the pin asks for: a newest
// generation that is its own, built from the release the base family is on, and
// younger than the refresh window is left alone; anything else is replaced by a
// NEW generation in the same family, created before anything is deleted, with
// the builder VM deleted even when the build fails.
func TestComputePreloadShellDecidesAndReplaces(t *testing.T) {
	baseRelease := "batch-cos-stable-official-2026-10-01-00-00-00"

	t.Run("a current generation is left alone", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":  preloadRow(t, baseRelease, 48*time.Hour),
			"IMAGE_LIST": "fugaro-preload-a0b6aca0-20200101T030000,fugaro-preload-a0b6aca0\n",
			"MARKER":     "fugaro-preload-built",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v, and it should have left the family alone:\n%s", err, out)
		}
		if !strings.Contains(out, "nothing to build") {
			t.Errorf("preload.sh did not report the generation as current:\n%s", out)
		}
		if strings.Contains(out, "refusing to touch it") {
			t.Errorf("preload.sh refused an image that is its own: the csv it read was not split into columns:\n%s", out)
		}
		if len(gcloudCalls(t, calls, "instances create")) != 0 || len(gcloudCalls(t, calls, "images create")) != 0 {
			t.Errorf("a current generation was rebuilt anyway:\n%s", strings.Join(calls, "\n"))
		}
	})

	t.Run("a stale generation is replaced in the same family", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW": preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			// The old generation of the family the runs boot, a generation a
			// concurrent build left behind (newer than anything this build
			// makes, so only the keep-rule saves it), and the family of a pin
			// that has moved on.
			"IMAGE_LIST": "fugaro-preload-a0b6aca0-20200101T030000,fugaro-preload-a0b6aca0\n" +
				"fugaro-preload-a0b6aca0-20990101T000000,fugaro-preload-a0b6aca0\n" +
				"fugaro-preload-deadbeef-20200101T030000,fugaro-preload-deadbeef\n",
			"MARKER": "fugaro-preload-built",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v on a stale generation:\n%s", err, out)
		}
		created := gcloudCalls(t, calls, "images create")
		if len(created) != 1 || !strings.HasPrefix(created[0][3], "fugaro-preload-a0b6aca0-") {
			t.Fatalf("the rebuild made %d new generations, want 1 in the family the runs boot:\n%s", len(created), strings.Join(calls, "\n"))
		}
		if !strings.Contains(strings.Join(created[0], " "), "--family=fugaro-preload-a0b6aca0") {
			t.Errorf("the new generation does not join the family the runs boot: %s", strings.Join(created[0], " "))
		}
		// Created before anything is deleted: the family keeps serving if the
		// create failed.
		firstDelete := -1
		for i, c := range calls {
			if strings.Contains(c, "compute images delete") || strings.Contains(c, "compute instances delete") {
				firstDelete = i
				break
			}
		}
		createIdx := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, "compute images create") })
		if createIdx < 0 || firstDelete < 0 || createIdx > firstDelete {
			t.Errorf("the new generation is created after something was deleted (create %d, first delete %d)", createIdx, firstDelete)
		}
		deleted := map[string]bool{}
		for _, d := range gcloudCalls(t, calls, "images delete") {
			deleted[d[3]] = true
		}
		if want := map[string]bool{
			"fugaro-preload-a0b6aca0-20200101T030000": true, // the previous generation
			"fugaro-preload-deadbeef-20200101T030000": true, // the family of a pin that has moved on
		}; !slices.Equal(slices.Sorted(maps.Keys(deleted)), slices.Sorted(maps.Keys(want))) {
			t.Errorf("the sweep deleted %v, want %v: it must never delete the newest of the family the runs boot or the generation it just made", slices.Sorted(maps.Keys(deleted)), slices.Sorted(maps.Keys(want)))
		}
		// And the builder VM goes, after the image it was imaged into exists.
		il := len(gcloudCalls(t, calls, "instances delete"))
		if il == 0 {
			t.Error("the rebuild left its builder VM behind")
		}
	})

	t.Run("a generation that records no socket gid is replaced", func(t *testing.T) {
		// Current by release and by age, and still replaced: an image built
		// before this label existed records no gid, and a run's `--group-add`
		// against it would be a guess. This is the half of "every rebuild
		// re-reads it" that a build deciding from the age alone would miss.
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":  preloadRowGID(t, "managed", baseRelease, 48*time.Hour, ""),
			"IMAGE_LIST": "fugaro-preload-a0b6aca0-20200101T030000,fugaro-preload-a0b6aca0\n",
			"MARKER":     "fugaro-preload-built",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v:\n%s", err, out)
		}
		if !strings.Contains(out, "records no socket-gid label") {
			t.Errorf("preload.sh did not say why a current generation is being replaced:\n%s", out)
		}
		created := gcloudCalls(t, calls, "images create")
		if len(created) != 1 {
			t.Fatalf("the rebuild made %d generations, want 1: an image with no recorded gid is not one to keep\n%s", len(created), strings.Join(calls, "\n"))
		}
		// And the generation it makes carries the gid the VM reported, under
		// the label the build was given.
		if !slices.Contains(created[0], "--labels=fugaro=managed,base-release="+baseRelease+",socket-gid="+preloadSocketGID) {
			t.Errorf("the new generation does not record the socket gid it read: %s", strings.Join(created[0], " "))
		}
	})

	t.Run("a builder VM that prints no gid makes no image", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":     preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":    "",
			"MARKER":        "fugaro-preload-built",
			"NO_SOCKET_GID": "1",
		})
		if err == nil {
			t.Fatalf("preload.sh succeeded with no socket gid to record:\n%s", out)
		}
		if !strings.Contains(out, "printed no usable socket gid") {
			t.Errorf("the refusal was not reported:\n%s", out)
		}
		if c := gcloudCalls(t, calls, "images create"); len(c) != 0 {
			t.Errorf("an image was created with no gid recorded on it:\n%s", strings.Join(calls, "\n"))
		}
		// The family keeps serving the generation it has, and the builder VM
		// still goes: the build failed, and a failed build is not a leak.
		if d := gcloudCalls(t, calls, "instances delete"); len(d) == 0 {
			t.Errorf("the builder VM was left behind by a build that made no image:\n%s", strings.Join(calls, "\n"))
		}
	})

	t.Run("a gid line ending CRLF is still a gid", func(t *testing.T) {
		// The serial console is a tty: its lines end CRLF, so the marker
		// match's last line is "998\r", and a parse that does not strip the
		// carriage return reads that as "not a number" and refuses -- on a
		// real GCP build, every time, while a fixture printing LF stays green.
		// This is the fixture printing what the console prints.
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":   preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":  "",
			"MARKER":      "fugaro-preload-built",
			"SERIAL_CRLF": "1",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v on CRLF serial output:\n%s", err, out)
		}
		if strings.Contains(out, "no usable socket gid") {
			t.Errorf("preload.sh read the carriage return as part of the gid:\n%s", out)
		}
		created := gcloudCalls(t, calls, "images create")
		if len(created) != 1 {
			t.Fatalf("the rebuild made %d generations on CRLF serial output, want 1:\n%s", len(created), strings.Join(calls, "\n"))
		}
		if !slices.Contains(created[0], "--labels=fugaro=managed,base-release="+baseRelease+",socket-gid="+preloadSocketGID) {
			t.Errorf("the generation built from CRLF serial output does not record the gid: %s", strings.Join(created[0], " "))
		}
	})

	t.Run("a builder VM that prints a gid that is not one makes no image", func(t *testing.T) {
		for _, gid := range []string{"socket-gid: no such file", "0"} {
			out, err, calls := preloadFixture(t, map[string]string{
				"IMAGE_ROW":       preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
				"IMAGE_LIST":      "",
				"MARKER":          "fugaro-preload-built",
				"SOCKET_GID_LINE": "fugaro-preload-socket-gid=" + gid,
			})
			if err == nil {
				t.Fatalf("preload.sh accepted the socket gid %q:\n%s", gid, out)
			}
			if c := gcloudCalls(t, calls, "images create"); len(c) != 0 {
				t.Errorf("an image was created for the socket gid %q:\n%s", gid, strings.Join(calls, "\n"))
			}
		}
	})

	t.Run("the builder VM is created with no external address", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":  preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST": "",
			"MARKER":     "fugaro-preload-built",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v:\n%s", err, out)
		}
		created := gcloudCalls(t, calls, "instances create")
		if len(created) != 1 {
			t.Fatalf("the builder VM was created %d times, want 1:\n%s", len(created), strings.Join(calls, "\n"))
		}
		// The call gcloud received, not the script's text: the VM's egress is
		// the module's NAT, and an external address would be a second path
		// around it.
		for _, want := range []string{"--no-address", "--network=projects/proj-1234/global/networks/default",
			"--subnet=projects/proj-1234/regions/us-east5/subnetworks/default", "--labels=fugaro=managed"} {
			if !slices.Contains(created[0], want) {
				t.Errorf("the builder VM's create lacks %s: %s", want, strings.Join(created[0], " "))
			}
		}
	})

	t.Run("a leftover builder VM that is not ours is refused, not deleted", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":      preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":     "",
			"MARKER":         "fugaro-preload-built",
			"LEFTOVER":       "1",
			"LEFTOVER_LABEL": "someone-else",
		})
		if err == nil {
			t.Fatalf("preload.sh went on beside a VM of its builder's name that is not fugaro=managed:\n%s", out)
		}
		if !strings.Contains(out, "is not fugaro=managed; refusing to touch it") {
			t.Errorf("the refusal was not reported:\n%s", out)
		}
		if d := gcloudCalls(t, calls, "instances delete"); len(d) != 0 {
			t.Errorf("a VM that is not fugaro=managed was deleted:\n%s", strings.Join(calls, "\n"))
		}
		if c := gcloudCalls(t, calls, "instances create"); len(c) != 0 {
			t.Errorf("a builder VM was created beside a foreign one:\n%s", strings.Join(calls, "\n"))
		}
	})

	t.Run("a leftover builder VM of ours is deleted before the new one", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":      preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":     "",
			"MARKER":         "fugaro-preload-built",
			"LEFTOVER":       "1",
			"LEFTOVER_LABEL": "managed",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v:\n%s", err, out)
		}
		del := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, "compute instances delete") })
		create := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, "compute instances create") })
		if del < 0 || create < 0 || del > create {
			t.Errorf("the leftover builder was not deleted before the new one was created (delete %d, create %d):\n%s", del, create, strings.Join(calls, "\n"))
		}
	})

	t.Run("a failed build still deletes its builder VM", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":   preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":  "",
			"MARKER":      "fugaro-preload-built",
			"FAIL_CREATE": "1",
		})
		if err == nil {
			t.Fatalf("preload.sh exited 0 although the image was never created:\n%s", out)
		}
		if len(gcloudCalls(t, calls, "instances create")) != 1 {
			t.Fatalf("the fixture did not reach the builder VM:\n%s", strings.Join(calls, "\n"))
		}
		// The trap is the backstop: the VM is deleted even though the script
		// aborted at the create, before its own step 7.
		if len(gcloudCalls(t, calls, "instances delete")) == 0 {
			t.Errorf("the build aborted and leaked its builder VM and its disk:\n%s", strings.Join(calls, "\n"))
		}
		if len(gcloudCalls(t, calls, "images delete")) != 0 {
			t.Errorf("a failed build deleted an image:\n%s", strings.Join(calls, "\n"))
		}
	})

	t.Run("a builder VM whose delete fails is tried once more by the trap", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":            preloadRow(t, "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST":           "",
			"MARKER":               "fugaro-preload-built",
			"FAIL_INSTANCE_DELETE": "1",
		})
		// The build itself is not failed by a VM that will not go: the image it
		// made exists and the sweep is not its business.
		if err != nil {
			t.Fatalf("preload.sh exited %v although the generation was created:\n%s", err, out)
		}
		if len(gcloudCalls(t, calls, "images create")) != 1 {
			t.Fatalf("the fixture did not reach the image create:\n%s", strings.Join(calls, "\n"))
		}
		if !strings.Contains(out, "could not be deleted; the trap tries once more") {
			t.Errorf("step 7 did not report the delete it lost:\n%s", out)
		}
		// Two: step 7's own attempt, and the trap's, which only runs because
		// step 7 left the flag raised. Clearing the flag unconditionally after
		// a failed delete leaks the VM and its disk to the next build's step 3.
		if got := len(gcloudCalls(t, calls, "instances delete")); got < 2 {
			t.Errorf("the failed delete was attempted %d time(s), want a second attempt by the EXIT trap:\n%s", got, strings.Join(calls, "\n"))
		}
		if !strings.Contains(out, "the build exited") {
			t.Errorf("the trap did not run for the builder VM it was told is still up:\n%s", out)
		}
	})

	// The two ways an image under this installation's own name prefix could
	// still not be the build's to touch. Both are the same rule -- delete only
	// what you created -- and neither is covered by the counts above: an
	// unmanaged image is refused before anything is built, and an unmanaged
	// image is never handed to the sweep to begin with.
	t.Run("the family's newest generation is refused when it is not fugaro=managed", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			// The family's newest, built by someone else -- no ownership
			// label -- and old enough that a build which never read the label
			// would replace it.
			"IMAGE_ROW":  preloadRowOwned(t, "not-fugaro", "batch-cos-stable-official-2020-01-01-00-00-00", 200*24*time.Hour),
			"IMAGE_LIST": "",
			"MARKER":     "fugaro-preload-built",
		})
		if err == nil {
			t.Fatalf("preload.sh exited 0 with a foreign image as the family's newest generation:\n%s", out)
		}
		if !strings.Contains(out, "refusing to touch it") {
			t.Errorf("the refusal was not reported:\n%s", out)
		}
		if n := len(gcloudCalls(t, calls, "instances create")) + len(gcloudCalls(t, calls, "images create")); n != 0 {
			t.Errorf("a build ran beside an image that is not this module's (%d create call(s)):\n%s", n, strings.Join(calls, "\n"))
		}
		if n := len(gcloudCalls(t, calls, "images delete")); n != 0 {
			t.Errorf("an image that is not this module's was deleted (%d delete call(s)):\n%s", n, strings.Join(calls, "\n"))
		}
	})

	t.Run("an image under the prefix that is not fugaro=managed is never swept", func(t *testing.T) {
		out, err, calls := preloadFixture(t, map[string]string{
			"IMAGE_ROW":  preloadRow(t, baseRelease, 48*time.Hour),
			"IMAGE_LIST": "",
			// What a list WITHOUT the ownership filter would return: an image
			// under this installation's prefix that this module did not make.
			// It is the sweep's worst case, because the name prefix alone
			// matches it and the sweep deletes everything it is handed that is
			// neither this build's generation nor the newest of the family.
			"IMAGE_LIST_UNMANAGED": "fugaro-preload-operator-20200101T030000,fugaro-preload-operator\n",
			"MARKER":               "fugaro-preload-built",
		})
		if err != nil {
			t.Fatalf("preload.sh exited %v:\n%s", err, out)
		}
		list := imageListCall(t, calls)
		for _, want := range []string{"--filter=labels.fugaro=managed", "name~^fugaro-preload-"} {
			if !strings.Contains(list, want) {
				t.Errorf("the sweep listed images with %q, which does not ask for %s: the answer to an unfiltered list is every image under the prefix, and the sweep deletes what it is handed:\n%s", list, want, list)
			}
		}
		for _, d := range gcloudCalls(t, calls, "images delete") {
			if strings.Contains(strings.Join(d, " "), "fugaro-preload-operator-") {
				t.Errorf("the sweep deleted an image this module did not create: %s", strings.Join(d, " "))
			}
		}
		if len(gcloudCalls(t, calls, "images delete")) != 0 {
			t.Errorf("a current generation's sweep deleted something:\n%s", strings.Join(calls, "\n"))
		}
	})
}

// allResourceBlocks returns every resource block in src, of any type.
func allResourceBlocks(t *testing.T, path string, src []byte) []block {
	t.Helper()
	code := stripComments(string(src))
	var out []block
	for _, m := range resourceHeader.FindAllStringSubmatchIndex(code, -1) {
		open := m[1] - 1
		end := matchBrace(code, open)
		typ := code[m[2]:m[3]]
		if end < 0 {
			t.Fatalf("%s: unbalanced braces in %s.%s", path, typ, code[m[4]:m[5]])
		}
		out = append(out, block{typ: typ, name: typ + "." + code[m[4]:m[5]], body: code[open+1 : end]})
	}
	return out
}

// TestLauncherConditionPreconditionMatchesGo pins the installation module's
// runs_launcher precondition to gcp.LauncherBucketCondition and its title:
// the launchers' write is widened to tokens/ (the budget token's move,
// docs/design/budget-token-channel.md §7.2, D16) in the Go that writes the
// tfvars, the install guard and the plan cover, and in this precondition, and
// a change that moved one without the other would refuse every installation
// or, worse, let a different condition through. The precondition is rendered
// for a bucket and compared with Go's own strings.
func TestLauncherConditionPreconditionMatchesGo(t *testing.T) {
	const bucket = "fugaro-runs-proj-1234"
	b, err := os.ReadFile("gcp/modules/installation/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`condition\s*=\s*var\.launcher_bucket_condition\.title == "([^"]*)" && var\.launcher_bucket_condition\.expression == "((?:[^"\\]|\\.)*)"\n`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no runs_launcher precondition of the expected shape in modules/installation/iam.tf")
	}
	title := string(m[1])
	expr := strings.ReplaceAll(strings.ReplaceAll(string(m[2]), `\"`, `"`), "${var.runs_bucket}", bucket)
	if title != gcp.LauncherBucketConditionTitle || expr != gcp.LauncherBucketCondition(bucket) {
		t.Fatalf("the precondition pins\n  %q / %q\nand Go writes\n  %q / %q", title, expr, gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(bucket))
	}
}

// TestJobTokenGrantIsReadOnlyOnTokens pins both job modules' job_tokens
// grants — the per-repository job's (modules/workflow, gcp.TokenBucketCondition)
// and the shared project job's (modules/project,
// gcp.ProjectTokenBucketCondition) — to objectViewer (no create, no delete: a
// peer run cannot remove or replace a queued run's token, D16), with a
// precondition on tokens/ of this runs bucket in one clause, and the jobs'
// objectUser grants never naming tokens/. The project module's grant is
// otherwise pinned only by the recorded installation golden plan, which a
// change to the module does not regenerate, so without this a role widened to
// objectUser there passed every test.
func TestJobTokenGrantIsReadOnlyOnTokens(t *testing.T) {
	for _, tc := range []struct{ file, variable string }{
		{"gcp/modules/workflow/sa.tf", "var.workflow"},
		{"gcp/modules/project/sa.tf", "var.job"},
	} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		i := strings.Index(src, `resource "google_storage_bucket_iam_member" "job_tokens" {`)
		if i < 0 {
			t.Errorf("no job_tokens grant in %s", tc.file)
			continue
		}
		block := src[i:]
		block = block[:strings.Index(block, "\n}\n")]
		if !strings.Contains(block, `role   = "roles/storage.objectViewer"`) {
			t.Errorf("%s: the job's tokens/ grant is not objectViewer:\n%s", tc.file, block)
		}
		if !strings.Contains(block, `objects/`+gcp.TokenBucketPrefix+`/")`) || !strings.Contains(block, `!strcontains(`+tc.variable+`.token_condition.expression, "||")`) {
			t.Errorf("%s: the job's tokens/ grant has no precondition on one tokens/ clause:\n%s", tc.file, block)
		}
	}
	if strings.Contains(gcp.BucketCondition("b", gcp.JobBucketPrefixes, "s"), "/"+gcp.TokenBucketPrefix+"/") {
		t.Error("the job's objectUser condition reaches tokens/")
	}
	if strings.Contains(gcp.ProjectBucketCondition("b", gcp.JobBucketPrefixes), "/"+gcp.TokenBucketPrefix+"/") {
		t.Error("the project job's objectUser condition reaches tokens/")
	}
}

// TestProjectJobSecretGrantFollowsTheMount pins the one property that makes a
// project job's account axis a credential boundary rather than a label: the
// module grants secretmanager.secretAccessor on each secret the job's *own*
// secret_env names, to that job's own account, at the secret level — one
// member per (job, mounted secret), never a project-wide role, never another
// job's secret — and the container mounts exactly those entries through
// secret_key_ref (design project-named-oauth-tokens §4.3 and D2, plan task 2).
//
// The grant and the mount are one list, which is what makes the credentials
// service a clean swap (§4.6): removing an account's one secret_env entry
// removes its member with it and moves nothing else — not the job's name, not
// its account, not its profile.
//
// Mutation (run, restore): iterate anything but var.job.secret_env in the
// member's for_each (a literal set, var.secrets, another variable) — the
// member and the mount then describe different secrets, and this fails.
func TestProjectJobSecretGrantFollowsTheMount(t *testing.T) {
	path := "gcp/modules/project/sa.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocks := resourceBlocks(t, path, b, "google_secret_manager_secret_iam_member")
	if len(blocks) != 1 {
		t.Fatalf("%s has %d secretAccessor resources, want the one the project job's mounts drive", path, len(blocks))
	}
	body := blocks[0].body
	for _, want := range []string{
		// One member per secret this job mounts, and none for a secret it
		// does not: the list is the job's own secret_env, not the
		// installation's secrets and not another job's.
		"for_each = toset(values(var.job.secret_env))",
		// The secret's own ID, at the secret level: never the project.
		"secret_id = var.secrets[each.value]",
		"role      = \"roles/secretmanager.secretAccessor\"",
		// The job's own account and no other member, ever.
		"member    = google_service_account.job.member",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: google_secret_manager_secret_iam_member.job does not hold %q:\n%s", path, want, body)
		}
	}
	if strings.Contains(body, "var.project_number") || strings.Contains(body, `"allUsers"`) || strings.Contains(body, `"allAuthenticatedUsers"`) {
		t.Errorf("%s: the job's secret grant names something wider than the job's own account:\n%s", path, body)
	}

	// And the container mounts each of those entries, as a secret_key_ref to
	// the same container: the mount is the carrier, so a secret_env entry the
	// job does not mount would be a grant with no reader, and a mount with no
	// entry would be a variable the job's account cannot read.
	jobPath := "gcp/modules/project/job.tf"
	jb, err := os.ReadFile(jobPath)
	if err != nil {
		t.Fatal(err)
	}
	jobs := resourceBlocks(t, jobPath, jb, "google_cloud_run_v2_job")
	if len(jobs) != 1 {
		t.Fatalf("%s has %d jobs, want the one shared project job", jobPath, len(jobs))
	}
	for _, want := range []string{
		"for_each = var.job.secret_env",
		"secret  = var.secrets[env.value]",
		`version = "latest"`,
	} {
		if !strings.Contains(jobs[0].body, want) {
			t.Errorf("%s: the job's container does not mount its secret_env through %q:\n%s", jobPath, want, jobs[0].body)
		}
	}
	// The values never travel in the plain environment: every secret the job
	// mounts is a value_source, and the plain env is the profile's own.
	if strings.Contains(jobs[0].body, "value = var.secrets") {
		t.Errorf("%s: a secret's value is written into the job's plain environment", jobPath)
	}
}

// customRolePermissions is the snapshot of the permissions Google allows in a
// custom role (GA, customRolesSupportLevel empty), for the service prefixes
// the modules use. See the header of the file for how it is made and refreshed.
const customRolePermissionsSnapshot = "testdata/custom-role-permissions.txt"

var (
	customRoleType       = regexp.MustCompile(`^google_[a-z0-9_]*iam_custom_role$`)
	permissionsAttr      = regexp.MustCompile(`(?m)^\s*permissions\s*=`)
	permissionsLiteral   = regexp.MustCompile(`(?s)^\s*permissions\s*=\s*\[([^\]]*)\]`)
	literalPermissionSeq = regexp.MustCompile(`^(?:\s*"[^"\\$]*"\s*,?)*\s*$`)
	quotedString         = regexp.MustCompile(`"([^"\\]*)"`)
)

// permissionStrings returns, for every custom role resource in a .tf file
// (google_*_iam_custom_role), the role and the entries of its `permissions`
// list. Comments of all three kinds (#, // and /* */) are dropped first, by
// the same stripComments the other tests use, so a permission written in a
// comment is never read as one.
//
// It reads literal lists only, and a role whose permissions are anything else
// is an error rather than a role with nothing to check: a local, a variable or
// a concat() could carry any permission past the test. A custom role with no
// permissions attribute, or with two, is an error too.
func permissionStrings(src string) (out []struct{ Role, Permission string }, errs []string) {
	code := stripComments(src)
	for _, m := range resourceHeader.FindAllStringSubmatchIndex(code, -1) {
		typ := code[m[2]:m[3]]
		if !customRoleType.MatchString(typ) {
			continue
		}
		role := typ + "." + code[m[4]:m[5]]
		open := m[1] - 1
		end := matchBrace(code, open)
		if end < 0 {
			errs = append(errs, role+": unbalanced braces")
			continue
		}
		body := code[open+1 : end]
		attrs := permissionsAttr.FindAllStringIndex(body, -1)
		if len(attrs) != 1 {
			errs = append(errs, fmt.Sprintf("%s: %d permissions attributes, want exactly one literal list", role, len(attrs)))
			continue
		}
		lit := permissionsLiteral.FindStringSubmatch(body[attrs[0][0]:])
		if lit == nil || !literalPermissionSeq.MatchString(lit[1]) {
			errs = append(errs, role+": permissions is not a literal list of quoted permissions (a local, a variable or a concat() cannot be checked)")
			continue
		}
		for _, q := range quotedString.FindAllStringSubmatch(lit[1], -1) {
			out = append(out, struct{ Role, Permission string }{role, q[1]})
		}
	}
	return out, errs
}

func loadPermissionSnapshot(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(customRolePermissionsSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		allowed[l] = true
	}
	return allowed
}

// 0.8.5 shipped with a custom role naming cloudscheduler.jobs.resume, which
// does not exist (Google names it cloudscheduler.jobs.enable), so creating the
// role failed at apply. Every permission any module puts in a custom role
// must be one Google allows in a custom role.
func TestCustomRolePermissionsAreAllowedByGoogle(t *testing.T) {
	// Known-invalid entries not fixed by this change, keyed "file|permission".
	// It is empty, and 0.9.5 is what emptied it: the compute module's reaper
	// role named batch.jobs.cancel, which Google does not define (it lists
	// batch.jobs.create/delete/get/list only), so the role could not be
	// created and --provision-batch refused before any cloud call. The role
	// is five permissions now and the reaper's queue deadline deletes. A new
	// entry belongs here only with a reason and a task that removes it: the
	// test fails while an entry is stale.
	knownInvalid := map[string]string{}
	usedKnown := map[string]bool{}
	allowed := loadPermissionSnapshot(t)
	if len(allowed) < 100 {
		t.Fatalf("%s has %d permissions: the snapshot is missing or truncated", customRolePermissionsSnapshot, len(allowed))
	}
	checked := 0
	err := filepath.WalkDir("gcp", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tf") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		perms, errs := permissionStrings(string(b))
		for _, e := range errs {
			t.Errorf("%s: %s", p, e)
		}
		for _, ps := range perms {
			checked++
			key := filepath.ToSlash(p) + "|" + ps.Permission
			if _, ok := knownInvalid[key]; ok {
				usedKnown[key] = true
				if allowed[ps.Permission] {
					t.Errorf("%s is allowed by Google now: remove it from knownInvalid", key)
				}
				continue
			}
			if !allowed[ps.Permission] {
				t.Errorf("%s: %s names permission %q, which is not in %s (not a permission Google allows in a custom role, or a restricted/testing one); if it is valid, refresh the snapshot as its header says",
					p, ps.Role, ps.Permission, customRolePermissionsSnapshot)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := range knownInvalid {
		if !usedKnown[k] {
			t.Errorf("knownInvalid entry %q matches nothing: remove it", k)
		}
	}
	if checked < 30 {
		t.Fatalf("only %d permission strings found in deploy/terraform: the extraction is broken", checked)
	}
}

func TestPermissionStringsExtraction(t *testing.T) {
	src := `resource "google_project_iam_custom_role" "a" {
  permissions = [
    "x.y.z", # "no.pe.rm"
    "x.y.w", // "no.pe.rm2"
    /* "no.pe.rm3", */
  ]
}
/*
resource "google_project_iam_custom_role" "commented" {
  permissions = ["no.pe.rm4"]
}
*/
// resource "google_project_iam_custom_role" "commented2" { permissions = ["no.pe.rm5"] }
resource "google_project_iam_custom_role" "b" {
  permissions = ["p.q.r", "p.q.s"]
}
resource "google_organization_iam_custom_role" "c" {
  permissions = ["o.r.g"]
}
resource "google_storage_bucket" "notarole" {
  permissions = ["n.o.t"]
}
`
	got, errs := permissionStrings(src)
	if len(errs) != 0 {
		t.Fatalf("errors on a literal source: %v", errs)
	}
	want := []string{"google_project_iam_custom_role.a/x.y.z", "google_project_iam_custom_role.a/x.y.w", "google_project_iam_custom_role.b/p.q.r", "google_project_iam_custom_role.b/p.q.s", "google_organization_iam_custom_role.c/o.r.g"}
	var have []string
	for _, g := range got {
		have = append(have, g.Role+"/"+g.Permission)
	}
	if !slices.Equal(have, want) {
		t.Fatalf("got %v want %v", have, want)
	}

	// A custom role whose permissions are not a literal list must fail loudly,
	// never be skipped: each of these once passed the test with an invalid
	// permission in it.
	for name, body := range map[string]string{
		"local":           `permissions = local.sneaky`,
		"variable":        `permissions = var.perms`,
		"concat":          `permissions = concat(["run.jobs.get"], ["bogus.perm.x"])`,
		"list with local": `permissions = ["run.jobs.get", local.more]`,
		"interpolation":   `permissions = ["${local.p}"]`,
		"no permissions":  `title = "x"`,
		"two attributes":  "permissions = [\"a.b.c\"]\n  permissions = [\"d.e.f\"]",
	} {
		t.Run(name, func(t *testing.T) {
			src := "resource \"google_project_iam_custom_role\" \"r\" {\n  " + body + "\n}\n"
			if _, errs := permissionStrings(src); len(errs) == 0 {
				t.Errorf("%q was not refused", body)
			}
		})
	}
}

// TestRootManagedDeployerScopedDefaultsOff pins the installation root's
// default for managed_deployer_scoped. The tftest runs plan the module
// directly (assertions cannot reach into a child module from the root), so
// nothing else fails if the root's default flips to true and the scoped role
// and its binding return to every plan.
func TestRootManagedDeployerScopedDefaultsOff(t *testing.T) {
	b, err := fs.ReadFile(FS, "gcp/roots/installation/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))
	m := regexp.MustCompile(`(?m)^\s*variable\s+"managed_deployer_scoped"\s*\{`).FindStringIndex(code)
	if m == nil {
		t.Fatal("the installation root declares no managed_deployer_scoped variable")
	}
	body := code[m[1]:matchBrace(code, m[1]-1)]
	if !hasAttr(body, "default", "false") {
		t.Errorf("the root's managed_deployer_scoped must default to false: fugaro writes it only for an installation that uses managed projects (internal/cli/init.go's installRoot, from the project's own creation fact), so an installation that never asks for managed plans none of the fence's resources:\n%s", body)
	}
}

// squash collapses runs of whitespace, so an assertion about an attribute's
// value does not also pin the alignment of the block it is in.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestCredentialsServiceGrantsAreTheOneBundle is the owner's Q6 ruling of
// 2026-10-19 as a property of the source — and it is the guard the tftest
// runs cannot be: a run plans the module with one set of variables, so a
// *second* grant, or a condition on the one grant, is invisible to it the
// moment the run's variables do not reach it.
//
// Q6: the credentials service's reach in Secret Manager is ONE container and
// ONE accessor grant on it, to the service's own account, and nothing that
// makes the grant's reach depend on a naming convention — no
// secretmanager.googleapis.com/secretId condition, no name-prefix fence, no
// per-credential grant. The container is created here; its document is
// written only by the owner-run `fugaro creds set` (C1.25), so no value and
// no version is anywhere in this file.
//
// The mutation is any of the shapes Q6 replaced: a second
// google_secret_manager_secret_iam_member (the per-credential grants of the
// earlier attempt, PR #674), a `condition` block on the one grant, or a
// Secret Manager container or version the service has no business reading.
//
// The service's OTHER reach — the realtime database — is deliberately not a
// mutation here any more (0.10.1, ledger #903): the file grants exactly one
// project role, the service's own database role, and
// TestCredentialsDatabaseRoleIsTheTwoDataPermissions pins that grant's role,
// permissions, project and member. What this test still refuses is every
// SECOND way to reach the project: an authoritative binding or policy, an
// audit config, a key.
func TestCredentialsServiceGrantsAreTheOneBundle(t *testing.T) {
	const path = "gcp/modules/installation/credentials.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))

	secrets := resourceBlocks(t, path, b, "google_secret_manager_secret")
	if len(secrets) != 1 {
		t.Fatalf("%s declares %d secret containers, want the one bundle and no other (Q6)", path, len(secrets))
	}
	container := squash(secrets[0].body)
	if !strings.Contains(container, "secret_id = var.credentials_service.bundle_secret") {
		t.Errorf("%s: the bundle's secret_id must be the variable's — the name the loader reads — and never a second spelling of it:\n%s", path, container)
	}
	if strings.Contains(container, "fugaro_secret") {
		t.Errorf("%s: the bundle container must carry no fugaro_secret label: that label is what discovery reads to decide which logical secret of the project's set a container holds, and this one is one document with one writer (internal/infra/discover.go's liveProjectSecrets):\n%s", path, container)
	}
	if strings.Contains(container, "condition") {
		t.Errorf("%s: the bundle container carries a condition:\n%s", path, container)
	}

	grants := resourceBlocks(t, path, b, "google_secret_manager_secret_iam_member")
	if len(grants) != 1 {
		t.Fatalf("%s declares %d secret accessor grants, want exactly one — on the bundle, for the service's account (Q6)", path, len(grants))
	}
	grant := squash(grants[0].body)
	for _, want := range []string{
		`role = "roles/secretmanager.secretAccessor"`,
		"secret_id = google_secret_manager_secret.credentials_bundle[0].secret_id",
		"member = google_service_account.credentials[0].member",
		"count = local.credentials_service_on ? 1 : 0",
	} {
		if !strings.Contains(grant, want) {
			t.Errorf("%s: the one bundle grant does not hold %q:\n%s", path, want, grant)
		}
	}
	if strings.Contains(grant, "condition") {
		t.Errorf("%s: the bundle grant carries a condition. Q6 replaced the name-prefix fence and the secretId filter with one unconditioned grant on one container; the service's own ID-token check is what decides who is served:\n%s", path, grant)
	}

	// The rest of the file, in one place. Every one of these is a way the
	// service's reach could grow without a second resource to notice.
	for _, bad := range []struct{ needle, why string }{
		{"secretmanager.googleapis.com/secretId", "a secretId condition is the per-secret filter Q6 replaced"},
		{"allUsers", "an unauthenticated invoker on the service"},
		{"allAuthenticatedUsers", "an invoker on the service for every Google account"},
		{"google_cloud_run_v2_service_iam_member", "an invoker grant here: the job accounts' grants live in modules/workflow/sa.tf, one per job, and the installation root grants nobody"},
		{"google_service_account_iam_member", "an actAs grant on the service's account: anyone who could deploy a revision as it could read every model credential the installation holds"},
		{"google_project_iam_binding", "an authoritative project binding: one binding owns the project's whole policy for its role and takes every other member off it"},
		{"google_project_iam_policy", "an authoritative project policy"},
		{"google_project_iam_audit_config", "an audit config for the service's account"},
		{"google_service_account_key", "a key for the service's account"},
		{"google_secret_manager_secret_version", "a version: values are written by the owner-run fugaro creds set, never by Terraform"},
	} {
		if strings.Contains(code, bad.needle) {
			t.Errorf("%s mentions %s: %s", path, bad.needle, bad.why)
		}
	}

	// The account's lifecycle precondition is the one `condition` this file
	// is allowed to carry, and it is taken out before the sweep below so the
	// sweep is an assertion about conditions rather than a search that
	// happens to pass: any other one — on the service, on the container, on
	// the grant — is a finding.
	const precondition = "condition     = var.credentials_service != null"
	if strings.Count(code, "precondition") != 1 || !strings.Contains(code, precondition) {
		t.Fatalf("%s: the account's lifecycle precondition is the only `precondition` in this file, and it must stay that way:\n%s", path, code)
	}
	// A `condition` attribute or block, wherever it stands: "precondition"
	// does not match, and neither does a comment (the code is stripped).
	if rest := strings.Replace(code, precondition, "", 1); regexp.MustCompile(`(?m)^\s*condition\s*[={]`).MatchString(rest) {
		t.Errorf("%s: a condition outside the account's precondition. Q6 replaced the name-prefix fence and the secretId filter with one unconditioned grant on one container:\n%s", path, rest)
	}

	// Every resource in the file is gated on the one boolean, which is what the
	// header's "keeps the six resources of this file in step" means: a resource
	// added without it is created in an installation that never opted in.
	// TestCredentialsServiceResourcesAreCounted reads the same promise from the
	// other end — the sentence's number against the file — and this sweep is
	// kept beside it because it reads the gate itself, the exact expression and
	// not the boolean's name, so a resource that mentions the boolean without
	// counting on it is caught here and not there.
	gated := 0
	for _, m := range resourceHeader.FindAllStringSubmatchIndex(code, -1) {
		end := matchBrace(code, m[1]-1)
		if end < 0 {
			t.Fatalf("%s: unbalanced braces in %s.%s", path, code[m[2]:m[3]], code[m[4]:m[5]])
		}
		body := squash(code[m[1]:end])
		if !strings.Contains(body, "count = local.credentials_service_on ? 1 : 0") {
			t.Errorf("%s: %s.%s is not gated on local.credentials_service_on, so it is created in an installation that never opted in:\n%s", path, code[m[2]:m[3]], code[m[4]:m[5]], body)
			continue
		}
		gated++
	}
	if gated != len(resourceHeader.FindAllString(code, -1)) {
		t.Fatalf("%s: %d of the file's resources were read as gated; the sweep above is not covering the file", path, gated)
	}

	if !strings.Contains(code, "service_account = google_service_account.credentials[0].email") {
		t.Errorf("%s: the service must run as its own account", path)
	}
	// IAM is required, stated rather than implied: this is the fence every
	// caller passes through before the container runs.
	if !strings.Contains(squash(code), "invoker_iam_disabled = false") {
		t.Errorf("%s: the service must set invoker_iam_disabled = false explicitly, so a change of the provider's default cannot open it silently", path)
	}
}

// TestCredentialsServiceResourcesAreCounted reads the sentence that counts
// this file's own resources — locals' "a single boolean keeps the six
// resources of this file in step" — against the file. It said five while the
// file declared four, and nothing read it: a number a reader is meant to trust
// is a claim, and this is where it is checked rather than believed.
//
// Both directions are the point. A resource added to the file without the
// sentence is a resource the sentence no longer accounts for; a resource added
// WITHOUT the opt-in's gate is worse — the file's first promise is that with
// var.credentials_service null it creates nothing (D147) — so every resource in
// it must be gated on the one boolean, and the count must be the whole file's.
//
// Mutation (run, restore): a seventh resource block in credentials.tf fails
// both assertions ("declares 7 ... and says six", and the ungated one), and
// reverting the word to "seven" fails the count.
func TestCredentialsServiceResourcesAreCounted(t *testing.T) {
	const path = "gcp/modules/installation/credentials.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))

	// The sentence, from the raw source: the number lives in a comment, which
	// stripComments blanks out.
	m := resourceCountSentence.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s no longer says how many resources the opt-in keeps in step; that sentence is what this test reads, so it stays:\n%s", path, code)
	}
	want, ok := numberWords[m[1]]
	if !ok {
		if n, err := strconv.Atoi(m[1]); err == nil {
			want, ok = n, true
		}
	}
	if !ok {
		t.Fatalf("%s: the sentence counts the file's resources as %q, which this test cannot read as a number: spell it as a word (one..ten) or a decimal, and keep it true", path, m[1])
	}

	// Every resource the file declares, whatever its type: the sentence is
	// about the file, not about one kind of thing in it.
	all := resourceHeader.FindAllStringSubmatchIndex(code, -1)
	if len(all) == 0 {
		t.Fatalf("%s declares no resource at all, and this test's subject is gone:\n%s", path, code)
	}
	if len(all) != want {
		t.Errorf("%s declares %d resources and its own sentence says %d (\"%s\"): the number a reader trusts is the file's count", path, len(all), want, m[0])
	}
	// And every one of them is behind the one boolean: "keeps the N resources
	// of this file in step" is a statement about all N, and the file's first
	// promise is that a null var.credentials_service creates nothing.
	for _, r := range all {
		name := code[r[2]:r[3]] + "." + code[r[4]:r[5]]
		open := r[1] - 1
		end := matchBrace(code, open)
		if end < 0 {
			t.Fatalf("%s: unbalanced braces in %s", path, name)
		}
		if body := code[open+1 : end]; !strings.Contains(body, "local.credentials_service_on") {
			t.Errorf("%s: %s is not gated on local.credentials_service_on, so a null var.credentials_service does not leave the file creating nothing (D147):\n%s", path, name, squash(body))
		}
	}
}

// credentialsDatabasePermissions are the ONLY permissions the credentials
// service's database role may hold (installation/credentials.tf): the
// instance's two data permissions. See
// TestCredentialsDatabaseRoleIsTheTwoDataPermissions for what each one is for
// and why the two predefined roles are both wrong.
var credentialsDatabasePermissions = []string{"firebasedatabase.instances.get", "firebasedatabase.instances.update"}

// The credentials service's database role: the two DATA permissions of the
// instance, the Firebase project, the service's own account as the only
// member, and no condition (0.10.1, ledger #903 — before this the account had
// no realtime-database role at all, so in production the service failed closed
// and refused every call).
//
// Why these two. The service reads the kill switches (config/kill/global,
// config/kill/repos/$slug), the project policy (config/policy/shared,
// config/policy/repos/$slug), a run's outcome node (outcomes/$day/$slug/$run)
// and the credential state another revision wrote (creds/ — marks, current
// nodes, leases, probe leases), and writes the creds/ nodes it owns by
// compare-and-set. Realtime Database IAM has NO path-level scope: a binding is
// per instance and reaches the whole database, so the only question a role can
// answer is which OPERATIONS the account may make, and there are exactly two
// predefined answers:
//
//   - roles/firebasedatabase.viewer is get+list: it cannot write creds/ at
//     all, so the service could not serve a single run.
//   - roles/firebasedatabase.admin is all eight firebasedatabase.instances.*
//     permissions (deploy/terraform/testdata/custom-role-permissions.txt): the
//     two data permissions PLUS list and the instance lifecycle — create,
//     delete, disable, reenable, undelete. The service never creates, deletes
//     or disables a database, and an identity that holds every model
//     credential of the installation must not be able to delete the
//     installation's database.
//
// So the role is the intersection: get (the only read — the same permission
// the viewer grant above relies on, modules/firebase/iam.tf) and update (the
// only write — a realtime-database node has no separate create or delete, and
// update is what admin carries for the budget admins who write /config). A
// third permission is a decision, and this test is where it is made.
//
// The project matters as much as the permissions: the role is declared in the
// FIREBASE project, because that is where the database is (D6), while the
// account is created in the installation's project. A grant in one project to
// a member of another is ordinary cross-project IAM, and it is the shape the
// Firebase module's existing database grants have.
func TestCredentialsDatabaseRoleIsTheTwoDataPermissions(t *testing.T) {
	const path = "gcp/modules/installation/credentials.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	roles := resourceBlocks(t, path, b, "google_project_iam_custom_role")
	if len(roles) != 1 {
		t.Fatalf("%s declares %d custom roles, want the one database role", path, len(roles))
	}
	role := squash(roles[0].body)
	for _, want := range []string{
		"project = var.credentials_service.firebase_project", // the database's project, not the installation's
		`role_id = "` + tf.CredentialsDatabaseRole + `"`,
		"count = local.credentials_service_on ? 1 : 0",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("%s: the database role does not hold %q:\n%s", path, want, role)
		}
	}
	// Only the permissions list, so the role's title and role_id cannot stand
	// in for a permission.
	list := regexp.MustCompile(`(?s)permissions\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(roles[0].body)
	if list == nil {
		t.Fatalf("%s: the database role has no permissions list:\n%s", path, role)
	}
	var got []string
	for _, p := range quotedPerm.FindAllStringSubmatch(list[1], -1) {
		got = append(got, p[1])
	}
	want := slices.Clone(credentialsDatabasePermissions)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s: the database role holds %v, want exactly %v: get is the only read and update the only write, and every other firebasedatabase permission is instance lifecycle the service never uses", path, got, want)
	}
	// The same two, on the Go side, so a cover that accepted a third
	// permission and a module that granted one cannot drift apart: the pin
	// internal/infra/tf keeps (RolePermissions) is the one the confirmation
	// reads.
	if pin := tf.RolePermissions[tf.CredentialsDatabaseRole]; !slices.Equal(pin, want) {
		t.Errorf("tf.RolePermissions[%s] = %v, want exactly %v", tf.CredentialsDatabaseRole, pin, want)
	}
	if !slices.Contains(tf.FugaroRoles, tf.CredentialsDatabaseRole) {
		t.Errorf("tf.FugaroRoles does not name %s, so a grant of this role is refused", tf.CredentialsDatabaseRole)
	}

	// The one binding: on that role, in that project, to the service's own
	// account, with no condition and no second member.
	grants := resourceBlocks(t, path, b, "google_project_iam_member")
	if len(grants) != 1 {
		t.Fatalf("%s declares %d project grants, want exactly one — the database role, to the service's own account", path, len(grants))
	}
	grant := squash(grants[0].body)
	for _, want := range []string{
		"project = var.credentials_service.firebase_project",
		`role = "projects/${var.credentials_service.firebase_project}/roles/` + tf.CredentialsDatabaseRole + `"`,
		"member = google_service_account.credentials[0].member",
		"count = local.credentials_service_on ? 1 : 0",
	} {
		if !strings.Contains(grant, want) {
			t.Errorf("%s: the database grant does not hold %q:\n%s", path, want, grant)
		}
	}
	if strings.Contains(grant, "condition") {
		t.Errorf("%s: the database grant carries a condition: Realtime Database IAM has no path-level scope, so a condition here would be a fence that looks like one and is not (the reasoning Q6 used for the bundle's accessor):\n%s", path, grant)
	}
	// The role is named literally rather than read from the role resource:
	// google_project_iam_custom_role's name is computed, so the reference
	// reaches a plan as an unknown role and the cover could no longer say which
	// role the grant is (tf's checkGrant, the unknown-role arm).
	if roleAttr := firstAttr(grants[0].body, "role"); strings.Contains(roleAttr, "google_project_iam_custom_role.") {
		t.Errorf("%s: the database grant reads its role from the role resource (%s), which a plan sees as unknown:\n%s", path, roleAttr, grant)
	}

	// No predefined database role anywhere in the tree is granted to this
	// service's account: the whole point of the custom role is that the two
	// predefined ones are wrong for it, and a second binding of
	// roles/firebasedatabase.admin would hand it the instance lifecycle.
	n := 0
	walk(t, func(p string, fb []byte) {
		if !strings.HasSuffix(p, ".tf") {
			return
		}
		for _, blk := range allResourceBlocks(t, p, fb) {
			if !strings.HasPrefix(blk.typ, "google_project_iam_") || !strings.Contains(blk.body, "credentials") {
				continue
			}
			if !regexp.MustCompile(`credentials(_database|\[0\])`).MatchString(blk.body) {
				continue
			}
			n++
			if !strings.HasPrefix(p, "gcp/modules/installation/credentials.tf") {
				t.Errorf("%s: %s grants a project role to the credentials service (%s): its whole reach is the bundle's accessor and its own database role, both in credentials.tf", p, blk.name, squash(blk.body))
			}
		}
	})
	if n == 0 {
		t.Errorf("no project grant naming the credentials service was found: this test is not reading the tree")
	}
}

// resourceCountSentence is the sentence in credentials.tf's locals block that
// counts the file's own resources: the number is what the test above reads.
var resourceCountSentence = regexp.MustCompile(`keeps\s+the\s+(\w+)\s+resources\s+of\s+this\s+file`)

// numberWords are the spellings of a count this test reads in prose.
var numberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}

// TestServiceHeldSecretsMatchTheCredentialsService pins the four literals in
// modules/workflow/sa.tf — the two model-credential variable names, the
// provider-key prefix and the service-held logical names (empty on this branch,
// the App key alone once C2.2's release fills them) — to internal/config, and
// the drop they drive to the opt-in. Terraform cannot ask Go, so the module
// spells the names out; this test is what keeps the two spellings one rule (the
// TestLauncherConditionPreconditionMatchesGo precedent).
//
// The mutation is any one of the lists: rename a reserved key in Go and the
// module's literal keeps dropping the accessor of a variable no job has, while
// the renamed one keeps it — the job's account then reads a credential the
// service was supposed to be the only holder of. The App key's clause (C2.3)
// is the one the model lists cannot cover: it is a reserved logical name, not
// a variable, so dropping it needs its own list, and a module that spells it
// wrong leaves the job's account with the private key of the installation's
// GitHub App.
//
// The accessor and the mount are one rule and are pinned as one (C2.3, review
// finding F1): job_secret_names drops what the account may not read, and
// job_mounts — the container's own env — drops what it may not mount, which is
// the App key and only the App key, because C1.20 is the task that takes the
// model credentials' mounts away with the runner's switch to the service.
//
// Both of those rules are keyed on service_held_secret_names, and the list is
// EMPTY on this branch (review finding F1): the App key joins it in the release
// that also stops the runner reading the key (C2.2), and until then the job
// keeps the accessor and the mount it needs to run. The pin below is what keeps
// the module's literal and config.ServiceHeldSecretNames one value, so that
// release fills both in one commit — with the module's list filled and Go's
// empty, the plan drops a grant the spec still produces (and the tftest run
// credentials_service_on_github_app_key_stays fails).
func TestServiceHeldSecretsMatchTheCredentialsService(t *testing.T) {
	const path = "gcp/modules/workflow/sa.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))

	// quotedList is the strings in the list literal the pattern captures.
	quotedList := func(pattern, what string) []string {
		t.Helper()
		m := regexp.MustCompile(pattern).FindStringSubmatch(code)
		if m == nil {
			t.Fatalf("%s declares no %s list", path, what)
		}
		var got []string
		for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
			got = append(got, q[1])
		}
		slices.Sort(got)
		return got
	}
	equal := func(what string, got, want []string, goName string) {
		t.Helper()
		w := slices.Clone(want)
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s: %s = %v, config.%s = %v", path, what, got, goName, want)
		}
	}

	equal("model_secret_env_names",
		quotedList(`model_secret_env_names\s*=\s*\[([^\]]*)\]`, "model_secret_env_names"),
		config.ModelSecretEnvs, "ModelSecretEnvs")
	equal("service_held_secret_names",
		quotedList(`service_held_secret_names\s*=\s*\[([^\]]*)\]`, "service_held_secret_names"),
		config.ServiceHeldSecretNames, "ServiceHeldSecretNames")

	p := regexp.MustCompile(`model_secret_env_prefix\s*=\s*"([^"]*)"`).FindStringSubmatch(code)
	if p == nil || p[1] != config.ProviderKeyEnvPrefix {
		t.Errorf("%s: model_secret_env_prefix = %v, config.ProviderKeyEnvPrefix = %q", path, p, config.ProviderKeyEnvPrefix)
	}

	// And the drop itself, clause by clause: a mount is a credential the
	// service holds exactly when its VARIABLE is one of the model names, OR
	// carries the provider-key prefix, OR its LOGICAL NAME is one the service
	// holds. Pinning the whole expression is the point — the two model halves
	// are what the review's mutation m1 removed, and with the prefix clause
	// gone every FUGARO_PROVIDER_KEY_* secret keeps its job accessor, which is
	// the one thing the opt-in exists to take away; the name clause (C2.3) is
	// the App key, whose variable is a reserved name the model lists must never
	// grow to cover (D145 keeps Bitbucket's token with the job, F7).
	if !strings.Contains(squash(code), "env => contains(local.model_secret_env_names, env) || startswith(env, local.model_secret_env_prefix) || contains(local.service_held_secret_names, logical)") {
		t.Errorf("%s: is_service_held must be `contains(model_secret_env_names, env) || startswith(env, model_secret_env_prefix) || contains(service_held_secret_names, logical)`; a missing clause leaves that credential's accessor with the job (review m1, C2.3):\n%s", path, code)
	}

	// The drop is gated on the opt-in, in the same expression that reads the
	// names: with credentials_service null the set is the workflow's own
	// secret_env and nothing changes (D147).
	if !strings.Contains(squash(code), "logical if var.credentials_service == null || !local.is_service_held[env]") {
		t.Errorf("%s: the job's secret set must be the workflow's own secret_env while the credentials service is off, and drop the credentials it holds only when it is on:\n%s", path, code)
	}

	// And the grant the job gains: one invoker grant, on the one service the
	// installation names, to the job's own account, unconditioned.
	grants := resourceBlocks(t, path, b, "google_cloud_run_v2_service_iam_member")
	if len(grants) != 1 {
		t.Fatalf("%s declares %d invoker grants on the credentials service, want the job's one", path, len(grants))
	}
	grant := squash(grants[0].body)
	for _, want := range []string{
		"count = var.credentials_service == null ? 0 : 1",
		"name = var.credentials_service.name",
		"location = var.credentials_service.region",
		`role = "roles/run.invoker"`,
		"member = google_service_account.job.member",
	} {
		if !strings.Contains(grant, want) {
			t.Errorf("%s: the job's invoker grant on the credentials service does not hold %q:\n%s", path, want, grant)
		}
	}
	if strings.Contains(grant, "condition") {
		t.Errorf("%s: the job's invoker grant carries a condition; the service's own ID-token check is what decides which of the accounts IAM admits are served (Q6):\n%s", path, grant)
	}

	// And the other half of the same rule (C2.3, review finding F1): the
	// MOUNT goes with the accessor. A container that still declares a secret
	// its account may not read is not a job with one credential too many, it
	// is a job Cloud Run refuses to create -- every GitHub run of an
	// installation that opted in would fail. The two are one expression's
	// halves here, and the tftest run credentials_service_on_github_app_key_
	// stays plans the state this branch ships them in: neither moves, because
	// the list is empty and the job still reads the key.
	if !strings.Contains(squash(code), "env => logical if var.credentials_service == null || !contains(local.service_held_secret_names, logical)") {
		t.Errorf("%s: job_mounts must keep every mount while the credentials service is off and drop the service-held ones (the App key) once it is on, by logical name:\n%s", path, code)
	}
	// The container is the consumer of that local, and it is what a missing
	// pass-down would leave out: iterating var.workflow.secret_env instead
	// plans the App key's mount back into a job whose account cannot read it.
	jb, err := fs.ReadFile(FS, "gcp/modules/workflow/job.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(squash(stripComments(string(jb))), `dynamic "env" { for_each = local.job_mounts`) {
		t.Errorf("gcp/modules/workflow/job.tf: the container's secret env must iterate local.job_mounts rather than var.workflow.secret_env, or the App key's mount outlives its accessor (C2.3, F1):\n%s", jb)
	}
}

// TestCredentialsServiceIsOptIn pins the opt-in and the wiring: both
// declarations default to null (D147: with the service off nothing changes,
// and an installation that has not opted in must plan what it planned
// before), the installation root passes its variable to the module, the
// repository root carries it in the installation object, and the repo module
// passes it to each workflow — which is the only place the job's invoker
// grant and the model-secret drop can come from.
//
// The mutation is any one of the four: a default of `{}` or a required
// variable creates the service for every installation; a dropped pass-down
// leaves the workflow module with null and the job with the model-secret
// accessor it was supposed to lose, silently.
func TestCredentialsServiceIsOptIn(t *testing.T) {
	for _, tc := range []struct{ path, what string }{
		{"gcp/roots/installation/variables.tf", "the installation root's"},
		{"gcp/modules/installation/variables.tf", "the installation module's"},
	} {
		b, err := fs.ReadFile(FS, tc.path)
		if err != nil {
			t.Fatal(err)
		}
		code := stripComments(string(b))
		i := regexp.MustCompile(`(?m)^\s*variable\s+"credentials_service"\s*\{`).FindStringIndex(code)
		if i == nil {
			t.Errorf("%s declares no credentials_service variable", tc.path)
			continue
		}
		body := code[i[1]:matchBrace(code, i[1]-1)]
		if !hasAttr(body, "default", "null") {
			t.Errorf("%s credentials_service must default to null (D147): with it unset nothing is created and every job keeps its model-secret accessors:\n%s", tc.what, body)
		}
	}
	for _, tc := range []struct{ path, want, what string }{
		{"gcp/roots/installation/main.tf", "credentials_service = var.credentials_service", "the installation root must pass its own variable to the module"},
		{"gcp/modules/repo/workflows.tf", "credentials_service = var.installation.credentials_service", "the repo module must pass the installation's service down to each workflow, or the job's invoker grant and the model-secret drop never exist"},
	} {
		b, err := fs.ReadFile(FS, tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(squash(stripComments(string(b))), tc.want) {
			t.Errorf("%s: %s (want %q)", tc.path, tc.what, tc.want)
		}
	}
	rb, err := fs.ReadFile(FS, "gcp/roots/repo/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(squash(stripComments(string(rb))), "credentials_service = optional(object({ name = string region = string }))") {
		t.Error("gcp/roots/repo/variables.tf: the installation object must accept credentials_service, or the repo root cannot carry the installation's service")
	}
}

// The service's image must be a DIGEST, and the validation that says so must
// be exercised by a run that plans the tag form and expects the refusal. Why
// it is a security rule and not a preference: the service's account reads
// every credential in the bundle, and the registry grants
// roles/artifactregistry.writer to every operator, so a mutable tag would let
// an operator push the code that the next owner apply runs as that account
// (independent security review of this file, M1). The terraform test is the
// guard that bites — this test is what fails if someone deletes the refusal
// runs while leaving the validation, or loosens the validation while leaving
// the runs, either of which would take the digest requirement with it.
func TestCredentialsServiceImageMustBeADigest(t *testing.T) {
	const path = "gcp/modules/installation/variables.tf"
	b, err := fs.ReadFile(FS, path)
	if err != nil {
		t.Fatal(err)
	}
	code := stripComments(string(b))
	i := regexp.MustCompile(`(?m)^\s*variable\s+"credentials_service"\s*\{`).FindStringIndex(code)
	if i == nil {
		t.Fatalf("%s declares no credentials_service variable", path)
	}
	body := code[i[1]:matchBrace(code, i[1]-1)]
	if !strings.Contains(body, "@sha256:[0-9a-f]{64}") {
		t.Errorf("%s: the credentials_service validation must require an image pinned by digest (a regex ending in @sha256: and 64 lower-case hex digits); a tag is mutable and any operator may push to the installation's registry:\n%s", path, body)
	}
	if strings.Contains(body, "length(var.credentials_service.image) > 0") {
		t.Errorf("%s: the credentials_service validation still accepts any non-empty image, which is the shape M1 found", path)
	}
	const tftest = "gcp/roots/installation/tests/installation.tftest.hcl"
	tb, err := fs.ReadFile(FS, tftest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(tb)
	if !strings.Contains(text, "@sha256:") {
		t.Errorf("%s: no run in the installation root's terraform test carries a digest image, so nothing exercises the validation above", tftest)
	}
	n := 0
	for _, m := range regexp.MustCompile(`(?m)^\s*expect_failures\s*=\s*\[([^\]]*)\]`).FindAllStringSubmatch(text, -1) {
		if strings.Contains(m[1], "var.credentials_service") {
			n++
		}
	}
	if n < 2 {
		t.Errorf("%s: %d runs expect var.credentials_service to fail; the tag form and the truncated-digest form must each be planned and refused (M1)", tftest, n)
	}
}
