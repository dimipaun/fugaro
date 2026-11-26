package terraform

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/secretname"
)

// The guard D499 asks for: every reader of a Secret Manager value in Fugaro's
// own artifacts is a listed exception, and every listed exception is still
// there. A hit with no row and a row with no hit both fail, so neither a new
// reader nor a stale row can slip through.
//
// The rows are docs/secrets-brokering/secret-readers.md's IDs (B0.1): this
// table is the exceptions list that doc points at, and
// TestSecretReadersDocMatchesTheTable keeps the two in step. A row's key is a
// hit key — a file the scan found, with the block in it for Terraform — or,
// for a reader no scan of this tree can hold an instance of, a rule key.
var secretReaderExceptions = []struct {
	id   string
	keys []string
}{
	{
		id: "E1",
		keys: []string{
			"deploy/terraform/gcp/modules/workflow/sa.tf google_secret_manager_secret_iam_member.job",
			"deploy/terraform/gcp/modules/workflow/job.tf google_cloud_run_v2_job.this",
		},
	},
	{
		id: "E2",
		keys: []string{
			"deploy/terraform/gcp/modules/project/sa.tf google_secret_manager_secret_iam_member.job",
			"deploy/terraform/gcp/modules/project/job.tf google_cloud_run_v2_job.this",
		},
	},
	{
		id: "E3",
		keys: []string{
			"deploy/terraform/gcp/modules/repo/build.tf google_secret_manager_secret_iam_member.build",
			"deploy/terraform/gcp/modules/repo/check.tf google_cloud_run_v2_job.check",
			// B6 moved the catalog build onto the project's build identity,
			// and the identity then held one secretAccessor per repository
			// whose catalog entries this project builds, exactly the reach the
			// per-repository build account's row above carries and nothing
			// wider (never the project). IS8 took that accessor out again
			// (D3330): a catalog entry's build mounts no secret of its own and
			// asks the credentials service for the git credential it clones
			// with, so the identity reads none.
			"images/derived/cloudbuild.yaml",
		},
	},
	{
		id: "E4",
		keys: []string{
			"deploy/terraform/gcp/modules/installation/coordinator.tf google_secret_manager_secret_iam_member.coordinator_app_key",
			"deploy/terraform/gcp/modules/installation/coordinator.tf google_cloud_run_v2_job.coordinator",
		},
	},
	{id: "E6", keys: []string{"internal/backend/gcp/build.go", "internal/backend/gcp/catalog.go"}},
	{
		id: "E7",
		keys: []string{
			"internal/credsvc/bundle.go",
			"internal/credsvc/dynamicsecrets.go",
			"internal/cli/credentials_serve.go",
			"deploy/terraform/gcp/modules/installation/credentials.tf google_secret_manager_secret_iam_member.credentials_bundle",
			// D2074's per-container grants, in a REPOSITORY root: one
			// secretAccessor for the service's account on each secret
			// container that repository's job mounts, so the service can read
			// back the container a run asks it for (B3.4). The same account
			// and the same role as the bundle's grant above, and a different
			// decision — the module holds the set to the job's mounts
			// (deploy/terraform's TestServiceWorkflowSecretGrantsMatchTheJobsMounts).
			"deploy/terraform/gcp/modules/workflow/sa.tf google_secret_manager_secret_iam_member.service",
		},
	},
	// owner_probe.go is E8's second read of the bundle's own container and not
	// another reader of a value: it asks Secret Manager what the caller may do
	// ON that container (TestIamPermissions), which is the one question that
	// tells a project owner from an operator before a provider key is written
	// into the bundle's entries (KS2, D3006, F36). No value is read, and the
	// answer is a permission list.
	{id: "E8", keys: []string{"internal/cli/creds.go", "internal/cli/doctor_brokering.go", "internal/cli/owner_probe.go"}},
	{
		id: "E9",
		keys: []string{
			"deploy/terraform/gcp/modules/repo/secrets.tf google_secret_manager_secret_iam_member.operator",
			"deploy/terraform/gcp/modules/installation/secrets.tf google_secret_manager_secret_iam_member.operator",
		},
	},
	{id: "E10", keys: []string{"deploy/terraform/gcp/modules/installation/iam.tf google_project_iam_member.launcher"}},
	{
		id: "E11",
		keys: []string{
			"deploy/terraform/gcp/modules/installation/deployer.tf google_project_iam_member.fugaro_deployer",
			"deploy/terraform/gcp/modules/installation/deployer.tf google_project_iam_member.fugaro_deployer_secret",
		},
	},
	// Project Owners read every secret through roles/owner whatever this tree
	// grants, so no file in it can be their hit. The row is pinned to the rule
	// instead, and the fixture test below proves a roles/owner grant written
	// into a .tf would be a hit like any other.
	{id: "E12", keys: []string{readerRoleRule + "roles/owner"}},
	{id: "E13", keys: []string{"internal/backend/gcp/secrets.go"}},
	{id: "E14", keys: []string{"internal/infra/discover.go"}},
	{id: "E15", keys: []string{"internal/infra/grants.go"}},
	{id: "E16", keys: []string{"internal/infra/readiness.go"}},
	{id: "E17", keys: []string{"internal/infra/retire.go"}},
	{id: "E18", keys: []string{"internal/cli/initsecrets.go"}},
	{id: "E19", keys: []string{"internal/infra/apis.go"}},
	{id: "E20", keys: []string{"internal/infra/tf/cover.go"}},
	{id: "E21", keys: []string{"internal/managed/stateaccounts.go"}},
	// The intake's adapter (DS2.1) holds the API the way every reader above
	// does -- the guard keys on the import, so a writer is a hit like any
	// other -- and it is the first row here that names a file no value can
	// come out of: it has no method that reads a payload, and the account it
	// runs as (E26, DS2.2's) holds no secretAccessor and no secrets.get (F30).
	{id: "E22", keys: []string{"internal/intake/gcp.go"}},
	{id: "E23", keys: []string{"internal/cli/doctor_batch.go"}},
	// The viewer list's project grant (RS4 of docs/roles-scopes): the viewer's
	// read-only job role carries the launcher's own `secretmanager.secrets.list`
	// and `secretmanager.versions.list` — the read `fugaro secrets ls` needs —
	// and no way to a value, which is what makes it a row of this table rather
	// than a permission the role gives up.
	{id: "E24", keys: []string{"deploy/terraform/gcp/modules/installation/iam.tf google_project_iam_member.viewer"}},
	// The credentials service's DYNAMIC half (D1931, DS2.2): the prefix
	// accessor reads a version's payload, and only of a container the grammar
	// names -- the condition is what makes it not the project-wide read its
	// role would otherwise be -- and the lister beside it answers names and
	// metadata alone, because secretmanager.secrets.list is refused under that
	// condition (Check 510). E7 is the same account's bundle.
	{
		id: "E25",
		keys: []string{
			"deploy/terraform/gcp/modules/installation/credentials.tf google_project_iam_member.credentials_prefix_accessor",
			"deploy/terraform/gcp/modules/installation/credentials.tf google_project_iam_member.credentials_lister",
		},
	},
	// The intake's own account (D1931, DS2.2): create, version-add and delete,
	// under the SAME prefix condition as E25's accessor -- so it writes only a
	// container of the grammar -- and list, unconditioned, which answers
	// metadata and no value. Neither role carries versions.access or
	// secrets.get, which TestIntakeAccountHasNoAccessor pins.
	{
		id: "E26",
		keys: []string{
			"deploy/terraform/gcp/modules/installation/intake.tf google_project_iam_member.intake_secrets",
			"deploy/terraform/gcp/modules/installation/intake.tf google_project_iam_member.intake_lister",
		},
	},
	// The installation's resident orchestrator (mobile plan task P5.1b, the
	// mobile orchestrator design's section 4.3): the same fugaroLauncher role
	// E10's launchers hold, granted to the installation's own account instead of
	// to the people on the list — names, labels and
	// version counts, and no way to a value, because the role carries the two
	// list permissions and not secretmanager.versions.access.
	{
		id:   "E28",
		keys: []string{"deploy/terraform/gcp/modules/installation/orchestrator.tf google_project_iam_member.orchestrator_launcher"},
	},
	// The enrolment view (repos-management RE0, `internal/cli/repos_view.go`):
	// the fourth member of the group's reads that answers "what is this
	// repository made of". It reads the same four things doctor's rotation
	// check reads and one fewer than E23 — the containers carrying a
	// repository's `fugaro_repo` label, each one's name and labels, whether it
	// holds an enabled version, and that version's creation time, which is the
	// rotation age D2415 lets a row print — and no value on any path: a
	// listing of metadata and a listing of versions, never `versions.access`.
	// The guard keys on the import, so it is a row of this table for the same
	// reason E22 is.
	{id: "E27", keys: []string{"internal/cli/repos_view.go"}},
	// The licence renewal job's account (K4.2, docs/licence-key-v2): the one
	// reader of the renewal container, which `update` creates empty and
	// `fugaro licence set` later writes. The role is a CUSTOM one
	// (fugaroLicenceRenewal) carrying exactly K4.1's four calls -- the
	// container's metadata, its enabled versions, one version's payload and the
	// version a renewal writes -- so no predefined role is close: the two that
	// would cover the four (secretmanager.secretVersionManager and
	// secretmanager.viewer) also carry create, delete, destroy, enable, disable
	// and getIamPolicy on the container, none of which the job calls.
	{id: "E30", keys: []string{"deploy/terraform/gcp/modules/installation/licence.tf google_secret_manager_secret_iam_member.licence_renewal"}},
	// The pairing key's cloud copy (mobile plan task P5.1e, the mobile
	// orchestrator design's section 4.2 and its section 4.7 step 2b), and it is
	// the one row in this table whose value is a SECRET Secret Manager holds for
	// Fugaro itself: the tag over every message between a paired phone and the
	// process that serves it is a symmetric HMAC, so the consumer that verifies
	// one holds the key the phone signs with, and a cloud consumer is not the
	// operator's laptop (section 4.2). The container is the installation's own
	// — one per installation, one version per pairing, created by the
	// installation module and destroyed version by version by `fugaro pair
	// revoke` — and the account is the resident orchestrator's, the same one
	// E28 lists for metadata. The grant is the SECRET row and never the project
	// row: `internal/infra/tf/cover.go` refuses a project-wide accessor for
	// every principal but the credentials service's, and the design's section
	// 4.3 states the readers this row does not bound — every project Owner
	// reads any container's value past its own policy (E12, SECURITY.md).
	{
		id: "E29",
		keys: []string{
			"internal/mailbox/keychain/cloud.go",
			"deploy/terraform/gcp/modules/installation/orchestrator.tf google_secret_manager_secret_iam_member.orchestrator_pairing_key",
		},
	},
	// The voice hop (mobile plan tasks P5.3.3 and P5.3.5, the voice design's
	// section 4.4 point 4 with Q2 answered 2026-10-19). It is a row of this
	// table for the reason E22 is -- the guard keys on the import, so the two Go
	// files are hits like any grant, and the client's constructor is one of them
	// although no method of it reads a payload -- and it is the one reader here
	// behind an endpoint ANYONE on the internet may call (voice.tf's allUsers
	// binding), so what it reads is bounded by its own code as much as by the
	// grant: the JSON object of register names to provider keys, five minutes of
	// cache at a time (internal/voicesvc's providerKeyTTL), and no byte of a
	// caller's body. The grant is the SECRET row and never the project row
	// (internal/infra/tf's cover refuses a project-wide accessor for every
	// principal but the credentials service's), on the one container
	// `fugaro-voice-provider-keys` whose ID the module writes as a literal and
	// this tree's Go carries as a constant; `update` creates it empty and the
	// person who holds the provider keys adds the version, so no provider key
	// reaches Terraform or its state.
	{
		id: "E31",
		keys: []string{
			"internal/voicesvc/provider.go",
			"internal/cli/voice_serve.go",
			"deploy/terraform/gcp/modules/installation/secrets.tf google_secret_manager_secret_iam_member.voice_provider_keys",
		},
	},
	// The workspace's own credentials service (WS6, D2980): the same image, the
	// same code and the same bundle read E7 audits, deployed into a workspace's
	// OWN project as that project's own account, told what it is by
	// FUGARO_WORKSPACE_MODE (var.workspace) and invoked by the members' job
	// accounts alone. It is a row of its own rather than a key under E7 because
	// the reader is a different account class in a different project: the
	// installation's account reads a job's mounts and the dynamic-delivery
	// containers besides, and this one reads the workspace's own bundle and the
	// personal token containers of the people the workspace serves, and nothing
	// else -- its project holds no job and no repository, so there is no mount
	// and no grammar container for it to reach, and no secret ID is an
	// environment value of its revision (the service derives the container's
	// name from the workspace's own name).
	//
	// The second key is WC2's grant (D2971, R6): the same account, one
	// PROJECT-level accessor conditioned to the per-person prefix and to the
	// one personal name the workspace serves, which is the only way to read a
	// container whose name carries a person's own subject
	// (`fugaro_u_<sub>_claude-oauth-token`, secretname.User). What bounds it is
	// the condition, and both halves are held byte for byte by
	// TestNoProjectLevelSecretAccessor and by internal/infra/tf's
	// WorkspaceNotCovered.
	{
		id: "E32",
		keys: []string{
			"deploy/terraform/gcp/roots/workspace/main.tf google_secret_manager_secret_iam_member.workspace_bundle",
			"deploy/terraform/gcp/roots/workspace/main.tf google_project_iam_member.workspace_person_token",
		},
	},
	// The locations read (location-preference LP7): `fugaro update` and
	// `fugaro setup` list the project's `fugaro=managed` containers to write
	// down whether they are automatic, regional or both, which is the one row
	// of the record LP14 keeps that Secret Manager answers (D3087). It is the
	// read class E23 and E27 make -- a listing by label, as the operator's own
	// identity -- and it is a row of its own because it is a file of its own:
	// no value comes out of it, and a listing answers names, labels and
	// replication policy and nothing else.
	{id: "E33", keys: []string{"internal/infra/locations_observe.go"}},
}

// A key under this prefix names a rule the scan enforces rather than a hit it
// found, so a change that stops enforcing the rule fails too.
const readerRoleRule = "rule:"

// Secret Manager roles whose holders read names and metadata but never a
// value, so a project-level grant of one is not a reader. Any other
// roles/secretmanager.* role counts, which is what catches a predefined role
// added after this guard was written without needing a new list entry.
var secretReaderNonReaderRoles = map[string]bool{
	"roles/secretmanager.viewer":             true,
	"roles/secretmanager.secretVersionAdder": true,
}

// projectRoleReadsSecrets reports whether a project-level grant of role hands
// out every secret of the project.
func projectRoleReadsSecrets(role string) bool {
	if role == "roles/owner" {
		return true
	}
	return strings.HasPrefix(role, "roles/secretmanager.") && !secretReaderNonReaderRoles[role]
}

func TestEverySecretReaderIsAListedException(t *testing.T) {
	listed := map[string]string{}
	for _, row := range secretReaderExceptions {
		for _, key := range row.keys {
			listed[key] = row.id
		}
	}
	for _, hit := range secretReaderHits(t) {
		if _, ok := listed[hit]; !ok {
			t.Errorf("Secret Manager reader %s is not a listed exception: add it to docs/secrets-brokering/secret-readers.md and to secretReaderExceptions in this file, or stop it reading", hit)
		}
	}
}

func TestEveryListedExceptionStillExists(t *testing.T) {
	hits := map[string]bool{}
	for _, hit := range secretReaderHits(t) {
		hits[hit] = true
	}
	for _, row := range secretReaderExceptions {
		if len(row.keys) == 0 {
			t.Errorf("exception %s lists no reader at all", row.id)
		}
		for _, key := range row.keys {
			if role, ok := strings.CutPrefix(key, readerRoleRule); ok {
				if !projectRoleReadsSecrets(role) {
					t.Errorf("exception %s is pinned to the rule for %s, which this scan no longer enforces", row.id, role)
				}
				continue
			}
			if !hits[key] {
				t.Errorf("exception %s names %s, which no longer reads Secret Manager: drop the row from secret-readers.md and from secretReaderExceptions in this file, or fix the name", row.id, key)
			}
		}
	}
}

func TestSecretReadersDocMatchesTheTable(t *testing.T) {
	const path = "../../docs/secrets-brokering/secret-readers.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	inDoc := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\| (E[0-9]+) \|`).FindAllStringSubmatch(string(data), -1) {
		inDoc[m[1]] = true
	}
	inTable := map[string]bool{}
	for _, row := range secretReaderExceptions {
		inTable[row.id] = true
	}
	for id := range inDoc {
		if !inTable[id] {
			t.Errorf("%s lists %s, which the exceptions table does not", path, id)
		}
	}
	for id := range inTable {
		if !inDoc[id] {
			t.Errorf("the exceptions table lists %s, which %s does not", id, path)
		}
	}
}

// TestSecretReaderScanFindsBindingsVolumesAndCustomRoles runs the scan over
// text rather than the tree, so a class that quietly stops matching shows up
// here instead of in the one error a real reader would produce.
func TestSecretReaderScanFindsBindingsVolumesAndCustomRoles(t *testing.T) {
	const tfSrc = `
resource "google_secret_manager_secret_iam_member" "member" {
  secret_id = "a"
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:a@example.iam.gserviceaccount.com"
}

resource "google_secret_manager_secret_iam_binding" "binding" {
  secret_id = "b"
  role      = "roles/secretmanager.secretAccessor"
  members   = ["serviceAccount:b@example.iam.gserviceaccount.com"]
}

resource "google_secret_manager_secret_iam_policy" "policy" {
  secret_id   = "c"
  policy_data = "{}"
}

data "google_secret_manager_secret_version" "token" {
  secret = "bitbucket-token"
}

resource "google_project_iam_member" "owner" {
  project = "p"
  role    = "roles/owner"
  member  = "user:person-1@example.test"
}

resource "google_project_iam_custom_role" "reader" {
  role_id     = "fugaroReader"
  permissions = ["secretmanager.versions.access"]
}

resource "google_project_iam_member" "custom" {
  project = "p"
  role    = google_project_iam_custom_role.reader.name
  member  = "serviceAccount:c@example.iam.gserviceaccount.com"
}

# The role of a mapped grant is each.value, so the map's own values are what
# the scan has to read: a reader added among them must not pass.
resource "google_project_iam_member" "mapped" {
  for_each = {
    log_writer = "roles/logging.logWriter"
    secret_reader = "roles/secretmanager.secretAccessor"
  }

  project = "p"
  role    = each.value
  member  = "serviceAccount:d@example.iam.gserviceaccount.com"
}

resource "google_project_iam_member" "from_variable" {
  project = "p"
  role    = var.reader_role
  member  = "serviceAccount:e@example.iam.gserviceaccount.com"
}

resource "google_project_iam_member" "opaque" {
  project = "p"
  role    = var.unset_role
  member  = "serviceAccount:f@example.iam.gserviceaccount.com"
}

resource "google_project_iam_custom_role" "unrelated" {
  role_id     = "fugaroUnrelated"
  permissions = ["run.jobs.get"]
}

resource "google_project_iam_member" "unrelated" {
  project = "p"
  role    = google_project_iam_custom_role.unrelated.name
  member  = "serviceAccount:g@example.iam.gserviceaccount.com"
}

resource "google_cloud_run_v2_job" "mounted" {
  template {
    template {
      containers {
        env {
          value_source {
            secret_key_ref {
              secret  = "d"
              version = "latest"
            }
          }
        }
        volumes {
          name = "values"
          secret {
            secret = "e"
          }
        }
      }
    }
  }
}

# A scratch volume is not a secret volume, and a comment naming secret_key_ref
# and google_secret_manager_secret_iam_member is not a reader either.
resource "google_cloud_run_v2_job" "scratch" {
  template {
    template {
      containers {
        volumes {
          name = "tmp"
          empty_dir {
            medium = "MEMORY"
          }
        }
      }
    }
  }
}
`
	const varsSrc = `
variable "reader_role" {
  type    = string
  default = "roles/secretmanager.admin"
}

variable "unset_role" {
  type = string
}

locals {
  nested = var.reader_role
}
`
	want := []string{
		"deploy/terraform/gcp/modules/m/f.tf data.google_secret_manager_secret_version.token",
		"deploy/terraform/gcp/modules/m/f.tf google_cloud_run_v2_job.mounted",
		"deploy/terraform/gcp/modules/m/f.tf google_project_iam_member.custom",
		"deploy/terraform/gcp/modules/m/f.tf google_project_iam_member.from_variable",
		"deploy/terraform/gcp/modules/m/f.tf google_project_iam_member.mapped",
		"deploy/terraform/gcp/modules/m/f.tf google_project_iam_member.owner",
		"deploy/terraform/gcp/modules/m/f.tf google_secret_manager_secret_iam_binding.binding",
		"deploy/terraform/gcp/modules/m/f.tf google_secret_manager_secret_iam_member.member",
		"deploy/terraform/gcp/modules/m/f.tf google_secret_manager_secret_iam_policy.policy",
	}
	got := scanTerraformDir([]tfFile{
		{rel: "deploy/terraform/gcp/modules/m/f.tf", src: tfSrc},
		{rel: "deploy/terraform/gcp/modules/m/variables.tf", src: varsSrc},
	}, map[string]bool{})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("terraform scan found:\n\t%s\nwant:\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}

	const goSrc = `package p

import secretmanager "google.golang.org/api/secretmanager/v1"

// secretmanager.googleapis.com appears in this comment and in no literal.
var _ = secretmanager.NewService

func f(b *cloudbuild.Build) {
	b.AvailableSecrets = nil
	env.SecretVariables = nil
	host := "secretmanager.googleapis.com"
	_, _ = env, host
}
`
	if got := scanGoFile("internal/p/f.go", goSrc); strings.Join(got, "\n") != "internal/p/f.go" {
		t.Errorf("Go scan found %v, want the one file", got)
	}

	const cbSrc = `steps:
  - name: gcr.io/cloud-builders/docker
    args: ["build", "."]
availableSecrets:
  secretManager:
    - versionName: projects/p/secrets/s/versions/latest
      env: TOKEN
`
	if got := scanCloudBuildFile("images/derived/cloudbuild.yaml", cbSrc); strings.Join(got, "\n") != "images/derived/cloudbuild.yaml" {
		t.Errorf("Cloud Build scan found %v, want the one file", got)
	}
}

// secretReaderHits walks the artifacts a reader can hide in: the Terraform
// tree and the roles it defines, the derived image's Cloud Build config, and
// every non-test Go file under internal/ and cmd/ — the Go scope the plan
// names. ee/ is the same module but no file there reaches Secret Manager
// today; a reader added there is B0.3's runtime sweep to find.
func secretReaderHits(t *testing.T) []string {
	t.Helper()
	const root = "../.."
	var hits []string

	byDir := map[string][]tfFile{}
	err := filepath.WalkDir(filepath.Join(root, "deploy/terraform/gcp"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := repoRel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		byDir[dir] = append(byDir[dir], tfFile{rel: rel, src: string(data)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the Terraform tree: %v", err)
	}

	readerRoleNames := map[string]bool{}
	for _, files := range byDir {
		for _, name := range secretManagerRoleNames(files) {
			readerRoleNames[name] = true
		}
	}
	for _, files := range byDir {
		hits = append(hits, scanTerraformDir(files, readerRoleNames)...)
	}

	err = filepath.WalkDir(filepath.Join(root, "images"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "cloudbuild.yaml" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := repoRel(root, path)
		if err != nil {
			return err
		}
		hits = append(hits, scanCloudBuildFile(rel, string(data))...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking images/: %v", err)
	}

	for _, dir := range []string{"internal", "cmd"} {
		err = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := repoRel(root, path)
			if err != nil {
				return err
			}
			hits = append(hits, scanGoFile(rel, string(data))...)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s/: %v", dir, err)
		}
	}

	sort.Strings(hits)
	return hits
}

func repoRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

type tfFile struct {
	rel string
	src string
}

// tfBlock is one resource or data block, with the text between its braces.
type tfBlock struct {
	mode string
	typ  string
	name string
	body string
	file string
}

// tfModule is one directory of the tree: a custom role is wired to the grants
// that carry it inside the module that defines it, and a variable's default
// or a local's value may be the only place a role is spelled out.
type tfModule struct {
	blocks []tfBlock
	vars   map[string]string
	locals map[string]string
}

var (
	tfBlockRE      = regexp.MustCompile(`(?m)^[ \t]*(resource|data)[ \t]+"([^"]+)"[ \t]+"([^"]+)"[ \t]*\{`)
	tfVariableRE   = regexp.MustCompile(`(?m)^[ \t]*variable[ \t]+"([^"]+)"[ \t]*\{`)
	tfLocalsRE     = regexp.MustCompile(`(?m)^[ \t]*locals[ \t]*\{`)
	tfLocalNameRE  = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_-]*)[ \t]*=`)
	tfStringRE     = regexp.MustCompile(`"([^"\n]*)"`)
	tfRefRE        = regexp.MustCompile(`\b(var|local)\.([A-Za-z0-9_]+)`)
	tfSecretPermRE = regexp.MustCompile(`"secretmanager\.[a-zA-Z.]+"`)
	tfSecretVolRE  = regexp.MustCompile(`(?m)^[ \t]*secret[ \t]*\{`)
	tfVolumeRE     = regexp.MustCompile(`(?m)^[ \t]*volumes[ \t]*\{`)
	tfCustomRoleRE = regexp.MustCompile(`google_project_iam_custom_role\.([A-Za-z0-9_]+)`)
	cbSecretsRE    = regexp.MustCompile(`(?m)^availableSecrets:`)
	tfDataReaderRE = regexp.MustCompile(`^google_secret_manager_`)
)

func newTFModule(files []tfFile) *tfModule {
	mod := &tfModule{vars: map[string]string{}, locals: map[string]string{}}
	for _, f := range files {
		clean := stripTerraformComments(f.src)
		mod.blocks = append(mod.blocks, terraformBlocks(f.rel, clean)...)
		for _, loc := range tfVariableRE.FindAllStringSubmatchIndex(clean, -1) {
			body, ok := terraformBlockBody(clean, loc[1]-1)
			if !ok {
				continue
			}
			mod.vars[clean[loc[2]:loc[3]]] = terraformAttrExpr(body, "default")
		}
		for _, loc := range tfLocalsRE.FindAllStringSubmatchIndex(clean, -1) {
			body, ok := terraformBlockBody(clean, loc[1]-1)
			if !ok {
				continue
			}
			for _, name := range tfLocalNameRE.FindAllStringSubmatch(body, -1) {
				mod.locals[name[1]] = terraformAttrExpr(body, name[1])
			}
		}
	}
	return mod
}

// secretManagerRoleNames is the set of custom roles in these files whose
// permissions include a secretmanager one, by Terraform name and by role ID:
// a grant in another module can only be tied to such a role by that name.
func secretManagerRoleNames(files []tfFile) []string {
	var names []string
	mod := newTFModule(files)
	for _, b := range mod.blocks {
		if b.typ != "google_project_iam_custom_role" || !tfSecretPermRE.MatchString(b.body) {
			continue
		}
		names = append(names, normalizeRoleName(b.name))
		if id := terraformAttrExpr(b.body, "role_id"); id != "" {
			names = append(names, normalizeRoleName(id))
		}
	}
	return names
}

// scanTerraformDir reads a module directory at a time, because a custom role
// and the grant that carries it are wired to each other inside the module: the
// launcher's grant spells the role out as a string while the role block takes
// its ID from a variable, so nothing but the module ties them. readerRoleNames
// carries the tree's secretmanager-touching role names for a grant that points
// at a role another module defines.
func scanTerraformDir(files []tfFile, readerRoleNames map[string]bool) []string {
	mod := newTFModule(files)
	for _, name := range secretManagerRoleNames(files) {
		readerRoleNames[name] = true
	}

	var hits []string
	for _, b := range mod.blocks {
		key := b.file + " " + b.typ + "." + b.name
		if b.mode == "data" {
			key = b.file + " data." + b.typ + "." + b.name
		}
		switch {
		case b.mode == "data":
			if tfDataReaderRE.MatchString(b.typ) || holdsSecretValue(b.body) {
				hits = append(hits, key)
			}
		case b.typ == "google_secret_manager_secret_iam_member", b.typ == "google_secret_manager_secret_iam_binding", b.typ == "google_secret_manager_secret_iam_policy":
			hits = append(hits, key)
		case b.typ == "google_project_iam_policy":
			// Its policy_data is opaque JSON, so a role it grants cannot be
			// read out of it; a project-wide policy is flagged whole.
			hits = append(hits, key)
		case b.typ == "google_project_iam_member", b.typ == "google_project_iam_binding":
			if grantReadsSecrets(b, mod, readerRoleNames) {
				hits = append(hits, key)
			}
		default:
			if holdsSecretValue(b.body) {
				hits = append(hits, key)
			}
		}
	}
	sort.Strings(hits)
	return hits
}

// grantReadsSecrets resolves a grant's role as far as the tree allows: through
// a variable's default, a local, and the map a for_each grant is built from,
// so a reader written any of those ways still fails the build. A role handed
// in from outside the tree (a root variable internal/infra writes a tfvars
// value for) resolves to nothing here, and B0.3's live sweep is what covers it.
func grantReadsSecrets(b tfBlock, mod *tfModule, readerRoleNames map[string]bool) bool {
	expr := terraformAttrExpr(b.body, "role")
	for _, candidate := range expandRoleExpr(expr, mod, terraformAttrExpr(b.body, "for_each"), 0) {
		if projectRoleReadsSecrets(candidate) || namesReaderRole(candidate, readerRoleNames) {
			return true
		}
	}
	return false
}

// expandRoleExpr returns every role string the expression can take: the
// literals in it, plus the literals of the variable defaults, locals and
// for_each values it refers to, plus the references themselves.
func expandRoleExpr(expr string, mod *tfModule, eachExpr string, depth int) []string {
	if expr == "" || depth > 4 {
		return nil
	}
	out := []string{expr}
	for _, m := range tfStringRE.FindAllStringSubmatch(expr, -1) {
		out = append(out, m[1])
	}
	if eachExpr != "" && strings.Contains(expr, "each.value") {
		out = append(out, expandRoleExpr(eachExpr, mod, "", depth+1)...)
	}
	for _, m := range tfRefRE.FindAllStringSubmatch(expr, -1) {
		next := ""
		switch m[1] {
		case "var":
			next = mod.vars[m[2]]
		case "local":
			next = mod.locals[m[2]]
		}
		if next != "" {
			out = append(out, expandRoleExpr(next, mod, eachExpr, depth+1)...)
		}
	}
	return out
}

// namesReaderRole matches a role expression to a role that lists a
// secretmanager permission. The grant usually spells the role out as a string
// ("projects/x/roles/fugaroLauncher"), so the module's role IDs and the role
// blocks' own names are both compared, ignoring case and the separators that
// differ between a Terraform name and a role ID.
func namesReaderRole(candidate string, readerRoleNames map[string]bool) bool {
	if candidate == "" || len(readerRoleNames) == 0 {
		return false
	}
	tokens := []string{candidate}
	if i := strings.LastIndex(candidate, "/roles/"); i >= 0 {
		tokens = append(tokens, candidate[i+len("/roles/"):])
	}
	if m := tfCustomRoleRE.FindStringSubmatch(candidate); m != nil {
		tokens = append(tokens, m[1])
	}
	if i := strings.LastIndex(candidate, "."); i >= 0 {
		tokens = append(tokens, candidate[i+1:])
	}
	for _, token := range tokens {
		normal := normalizeRoleName(token)
		for name := range readerRoleNames {
			if len(name) >= 3 && (normal == name || strings.HasSuffix(normal, name)) {
				return true
			}
		}
	}
	return false
}

func normalizeRoleName(s string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(s))
}

// holdsSecretValue reports whether a block mounts or reads a secret value: a
// secret_key_ref, or a volumes block with a secret child. A scratch volume is
// not one, and neither is a comment naming either shape.
func holdsSecretValue(body string) bool {
	if strings.Contains(body, "secret_key_ref") {
		return true
	}
	for _, loc := range tfVolumeRE.FindAllStringIndex(body, -1) {
		inner, ok := terraformBlockBody(body, loc[1]-1)
		if ok && tfSecretVolRE.MatchString(inner) {
			return true
		}
	}
	return false
}

func terraformBlocks(file, clean string) []tfBlock {
	var out []tfBlock
	for _, loc := range tfBlockRE.FindAllStringSubmatchIndex(clean, -1) {
		body, ok := terraformBlockBody(clean, loc[1]-1)
		if !ok {
			continue
		}
		out = append(out, tfBlock{
			mode: clean[loc[2]:loc[3]],
			typ:  clean[loc[4]:loc[5]],
			name: clean[loc[6]:loc[7]],
			body: body,
			file: file,
		})
	}
	return out
}

// terraformBlockBody returns the text between the brace at open and the brace
// that closes it, skipping quoted strings so a brace inside a string cannot
// end a block early.
func terraformBlockBody(src string, open int) (string, bool) {
	depth := 0
	inString := false
	for i := open; i < len(src); i++ {
		c := src[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open+1 : i], true
			}
		}
	}
	return "", false
}

// terraformAttrExpr returns a single attribute's value as written: the quoted
// string for “role = "roles/owner"“, the reference itself for a role wired
// to a resource or a variable, and a whole map or call for a for_each.
func terraformAttrExpr(body, attr string) string {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(attr) + `[ \t]*=[ \t]*`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		return ""
	}
	rest := body[loc[1]:]
	depth := 0
	inString := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
			if depth < 0 {
				return strings.TrimSpace(rest[:i])
			}
		case '\n':
			if depth == 0 {
				return strings.TrimSpace(rest[:i])
			}
		}
	}
	return strings.TrimSpace(rest)
}

// stripTerraformComments removes # and // comments and /* */ blocks, keeping
// the newlines so the line-anchored patterns above still see the same lines.
// Without it a prose mention of a resource type or a role in a comment would
// read as a grant.
func stripTerraformComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inString := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '#', c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func scanCloudBuildFile(rel, src string) []string {
	if cbSecretsRE.MatchString(src) {
		return []string{rel}
	}
	return nil
}

// scanGoFile finds a reader in Go by what it does, not by what it says: a
// comment naming the API host, or a dead string in prose, is not a reader.
func scanGoFile(rel, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil
	}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err == nil && p == "google.golang.org/api/secretmanager/v1" {
			return []string{rel}
		}
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if x.Sel.Name == "AvailableSecrets" || x.Sel.Name == "SecretVariables" {
				found = true
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(x.Value)
			if err == nil && strings.Contains(value, "secretmanager.googleapis.com") {
				found = true
			}
		}
		return true
	})
	if found {
		return []string{rel}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The mount rule: no run job mounts a workflow or build secret (IS7, D3331, T3)
// ---------------------------------------------------------------------------
//
// The guard above audits who may READ a secret: a grant, a project role, an
// availableSecrets, the API. This one audits what a RUN JOB MOUNTS, because a
// mount is the one place a class of secret reaches a job's container without a
// per-secret grant of its own — and D3331 rules that after the flip no run job
// mounts a workflow secret or a build secret, in Terraform or in the Go that
// writes the tfvars it is planned from. T3 scopes it to those two classes:
// every other class keeps its secret-readers.md row and the plan that ends it
// (vertex repositories keep their git and model mounts, the image-check job
// and unserved builds keep the git credential, per-repository provider keys
// still mount).

// runJobModules are the module directories a RUN JOB is defined in (D3321): a
// repository's workflow job, a shared project job, and the repository's own
// build and check jobs. An installation SERVICE — the coordinator, the licence
// renewal, the orchestrator, the voice hop — is not a run job: it is long-lived
// infrastructure of the installation's own, and the classes it reads are its
// own rows in secret-readers.md.
var runJobModules = []string{
	"deploy/terraform/gcp/modules/workflow",
	"deploy/terraform/gcp/modules/project",
	"deploy/terraform/gcp/modules/repo",
}

// runJobMountClassTokens are T3's two classes in the tree's own spelling. The
// workflow-secret class is the one IS6's flip took off every job spec, which
// modules/workflow/sa.tf reads as local.vault_secret_names; the build-secret
// class is the one modules/repo/build.tf drops as local.build_mounts_served
// over var.repo.build_secrets (D2077). A mount may reach either only to EXCLUDE
// it — every drop in this tree is written as `!( ... )`, see ungatedClassRef —
// so a reference outside an exclusion is a mount that admits the class.
var runJobMountClassTokens = []string{"vault_secret_names", "build_secrets"}

// runJobMountUntilRows are T1's two until-rows, and the whole of what this
// rule allows: the classes a shared job still mounts and this plan's flip does
// not end, each with the task whose pull request deletes its row. T3 scopes the
// rule to the workflow-secret and build-secret classes, so neither row is a hit
// today — they are held here, and checked below, because a rule that never
// names the two debts is a rule a later reader cannot see them in. Nothing else
// may be added: a class that mounts is either one of these two, with the plan
// that ends it, or a hit.
var runJobMountUntilRows = []struct {
	id    string
	class string
	key   string
	task  string
}{
	{
		id:    "U1",
		class: "the shared jobs' GitHub App key",
		key:   "deploy/terraform/gcp/modules/project/job.tf google_cloud_run_v2_job.this",
		task:  "C2.3",
	},
	{
		id:    "U2",
		class: "the shared jobs' provider keys",
		key:   "deploy/terraform/gcp/modules/project/job.tf google_cloud_run_v2_job.this",
		task:  "KS16",
	},
}

// goRunJobBuilders are the Go files that write a run job's or a build's spec —
// the per-repository job and its build request, the shared project job, and the
// catalog entry's build. They are scanned by what they DO: a delivery name
// derived here is one a run job would mount, and a mount written from the
// workflow's own secrets is the defect the Terraform half refuses.
var goRunJobBuilders = []string{
	"internal/infra/spec.go",
	"internal/infra/projectjobs.go",
	"internal/backend/gcp/build.go",
	"internal/backend/gcp/catalog.go",
}

// goMountFields are the fields a spec mounts a secret IN. VaultSecretNames is
// deliberately not one: it is the record of a mount IS6 took AWAY, written
// where the drop happens, and a guard that read it as a mount would fail on the
// flip itself.
var goMountFields = []string{"SecretEnv", "AvailableSecrets", "SecretVariables", "BuildSecrets"}

// goMountClassRefs are where the workflow's OWN secrets are read in these
// builders: `w` is the config.Workflow whose `secrets:` these are, and `s` is
// the BuildSpec carrying the same list for the build. Named, and not a bare
// ".Secrets": the same field name is worn by rs.Secrets and pr.Secrets, which
// are maps of secret IDs and project-wide names and not the class at all. A
// statement that reaches the class under another receiver is not seen — this
// half is a tripwire on the flip being written back, and the delivery-name half
// below is the one that carries the defect D3324 fixed.
var goMountClassRefs = []string{"w.Secrets", "s.WorkflowSecrets"}

// goServeGateWords are the words the flip's gates are written with in these
// builders: `served` (buildMountsServed, s.served(), projectJobMounts' served),
// `serves` (CredentialsServiceServes, projectJobServesTheCredentialsService)
// and `addressed` (credentialsServiceAddressed, the one class whose drop is the
// address's and not the service's). A statement that mounts the class under one
// of them is the flip; the same statement without one is a hit.
var goServeGateWords = []string{"served", "serves", "addressed"}

// runJobMountHits is every way the tree mounts a workflow or build secret in a
// run job: the modules' own text, and the Go that writes their tfvars.
func runJobMountHits(t *testing.T) []string {
	t.Helper()
	var files []tfFile
	for _, dir := range runJobModules {
		files = append(files, moduleFiles(t, dir)...)
	}
	hits := runJobMountHitsInFiles(files)
	for _, rel := range goRunJobBuilders {
		src, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		hits = append(hits, goMountHits(t, rel, string(src))...)
	}
	sort.Strings(hits)
	return hits
}

func TestRunJobsMountNoWorkflowSecret(t *testing.T) {
	hits := runJobMountHits(t)
	if len(hits) > 0 {
		t.Errorf("a run job mounts a class D3331's rule takes off every job spec (T3, IS6, IS8):\n\t%s", strings.Join(hits, "\n\t"))
	}

	// T1: exactly two until-rows, each naming a class the shared jobs really
	// mount and the task whose pull request deletes it. A third row is the
	// widening T1 refuses, and a row whose class this rule claims is the
	// contradiction T3 refuses.
	if len(runJobMountUntilRows) != 2 {
		t.Fatalf("the guard allows exactly two until-rows (T1), and this table has %d", len(runJobMountUntilRows))
	}
	var files []tfFile
	for _, dir := range runJobModules {
		files = append(files, moduleFiles(t, dir)...)
	}
	mounting := map[string]bool{}
	for _, site := range runJobSecretSites(files) {
		mounting[site.key] = true
	}
	for _, row := range runJobMountUntilRows {
		if !mounting[row.key] {
			t.Errorf("until-row %s names %s, which mounts no secret at all: the class left that job, so delete the row in the task that ended it (%s)", row.id, row.key, row.task)
		}
		if row.task == "" {
			t.Errorf("until-row %s names no task: the row is the debt, and the task that ends it is what a reader has to find (T1)", row.id)
		}
		if strings.Contains(row.class, "workflow secret") || strings.Contains(row.class, "build secret") {
			t.Errorf("until-row %s holds %q, and T3 scopes the rule to the workflow-secret and build-secret classes: the rule must catch that mount, not allow it", row.id, row.class)
		}
	}
}

// TestCatalogIdentityHoldsNoSecretAccessor is the installation half of the
// same rule (D3330, IS8): the one account a catalog entry's build runs as reads
// its credentials from the credentials service, so it holds no accessor on any
// secret — the git credential's grant is the one IS8 removed, and a new one
// would hand a build the container the flip took away.
func TestCatalogIdentityHoldsNoSecretAccessor(t *testing.T) {
	files := moduleFiles(t, "deploy/terraform/gcp/modules/installation")
	mod := newTFModule(files)
	for _, f := range files {
		if strings.Contains(f.src, "build_identity_git_secret") {
			t.Errorf("%s names google_secret_manager_secret_iam_member.build_identity_git_secret: IS8 removed that grant, and the build identity reads the git credential at the credentials service's /v1/build/token (D3330)", f.rel)
		}
	}
	for _, b := range mod.blocks {
		if b.mode != "resource" || !strings.Contains(b.body, "google_service_account.build_identity") {
			continue
		}
		if catalogIdentitySecretGrant(b) {
			t.Errorf("%s %s.%s grants the catalog build identity a secret: the identity reads every credential it needs from the credentials service (D3330, IS8), and a secretmanager accessor here is one no run asks for", b.file, b.typ, b.name)
		}
	}
}

// catalogIdentitySecretGrant reports whether a block whose member is the build
// identity hands it a secret: a secretmanager grant of any shape, or a
// project-level role whose role string is a secretmanager one.
func catalogIdentitySecretGrant(b tfBlock) bool {
	switch b.typ {
	case "google_secret_manager_secret_iam_member", "google_secret_manager_secret_iam_binding", "google_secret_manager_secret_iam_policy":
		return true
	case "google_project_iam_member", "google_project_iam_binding":
		return projectRoleReadsSecrets(terraformAttrExpr(b.body, "role"))
	}
	return false
}

// TestMountRuleCatchesAFixture is the rule's own red step (spec §6): the
// scanner is handed a fixture that mounts a workflow secret, one that mounts a
// delivery name, and one Go builder that does each, and each has to be caught;
// the shape IS6 and IS8 leave has to pass. A rule nobody has seen fail is a
// rule nobody knows the shape of.
func TestMountRuleCatchesAFixture(t *testing.T) {
	fixtures := []struct {
		name string
		why  string
		file tfFile
	}{
		{
			name: "the workflow-secret class mounted whole",
			why:  "for_each over the fetched class mounts every workflow secret the flip took off the job (IS6, D3324)",
			file: tfFile{rel: "deploy/terraform/gcp/modules/workflow/job.tf", src: fixtureWorkflowClassMounted},
		},
		{
			name: "the installation's fugaro_p_ copy mounted",
			why:  "the container IS13 retires: a job naming it mounts a secret the run reads from the service (D3323, D3324)",
			file: tfFile{rel: "deploy/terraform/gcp/modules/repo/check.tf", src: fixtureDeliveryNameMounted},
		},
	}
	for _, f := range fixtures {
		if hits := runJobMountHitsInFiles([]tfFile{f.file}); len(hits) == 0 {
			t.Errorf("the mount rule passes the fixture %q, and it must fail it: %s", f.name, f.why)
		}
	}

	good := []tfFile{
		{rel: "deploy/terraform/gcp/modules/workflow/sa.tf", src: fixtureServeGatedDrop},
		{rel: "deploy/terraform/gcp/modules/workflow/job.tf", src: fixtureGatedMount},
	}
	if hits := runJobMountHitsInFiles(good); len(hits) > 0 {
		t.Errorf("the mount rule fails the shape IS6 leaves, and that shape is the rule's own subject:\n\t%s", strings.Join(hits, "\n\t"))
	}

	goFixtures := []struct {
		name string
		src  string
	}{
		{"a builder mounting the workflow's own secrets with no serve gate", fixtureGoMountsTheClass},
		{"a build builder mounting the same list", fixtureGoMountsTheBuildClass},
		{"a builder deriving the installation's fugaro_p_ copy", fixtureGoDerivesDeliveryName},
	}
	for _, f := range goFixtures {
		if hits := goMountHits(t, f.name, f.src); len(hits) == 0 {
			t.Errorf("the mount rule passes a fixture that must fail it: %s", f.name)
		}
	}
	if hits := goMountHits(t, "the gated shape", fixtureGoServeGatedMount); len(hits) > 0 {
		t.Errorf("the mount rule fails the gated shape IS6 leaves:\n\t%s", strings.Join(hits, "\n\t"))
	}
}

func moduleFiles(t *testing.T, dir string) []tfFile {
	t.Helper()
	const root = "../.."
	abs := filepath.Join(root, dir)
	entries, err := os.ReadDir(abs)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []tfFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		p := filepath.Join(abs, e.Name())
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		rel, err := repoRel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, tfFile{rel: rel, src: string(src)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files
}

// runJobSecretSite is one place a run-job module mounts a secret value: the
// resource it is declared in, by the key the reader table uses too, and the
// expressions that decide which secrets reach it.
type runJobSecretSite struct {
	key         string
	expressions []string
	body        string
}

// runJobSecretSites is every place a run-job module mounts a secret VALUE: a
// resource block, or a dynamic block inside one, that holds a secret_key_ref or
// a secret volume. A resource that only GRANTS the class — build.tf's accessor
// on var.repo.build_secrets, sa.tf's per-container grant — is not a mount, and
// neither is a locals definition: the rule is about what reaches a container.
var tfDynamicRE = regexp.MustCompile(`(?m)^[ \t]*dynamic[ \t]+"([A-Za-z0-9_]+)"[ \t]*\{`)

func runJobSecretSites(files []tfFile) []runJobSecretSite {
	mod := newTFModule(files)
	var sites []runJobSecretSite
	for _, b := range mod.blocks {
		if b.mode != "resource" {
			continue
		}
		key := b.file + " " + b.typ + "." + b.name
		rest := b.body
		for _, loc := range tfDynamicRE.FindAllStringSubmatchIndex(b.body, -1) {
			inner, ok := terraformBlockBody(b.body, loc[1]-1)
			if !ok {
				continue
			}
			rest = blankSpan(rest, loc[0], loc[1]+len(inner)+1)
			if !holdsSecretValue(inner) {
				continue
			}
			sites = append(sites, runJobSecretSite{key: key, body: inner, expressions: siteExpressions(mod, inner, b.body)})
		}
		if holdsSecretValue(rest) {
			sites = append(sites, runJobSecretSite{key: key, body: rest, expressions: siteExpressions(mod, rest, b.body)})
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].key < sites[j].key })
	return sites
}

// siteExpressions are the expressions that decide which secrets reach a mount:
// the container's own for_each, the secret it names, and the value it carries,
// each read off the block and off the resource around it, and each with its
// locals and variables inlined (a mount that reaches the class through a local
// is the same mount).
func siteExpressions(mod *tfModule, block, resourceBody string) []string {
	var out []string
	for _, body := range []string{block, resourceBody} {
		for _, attr := range []string{"for_each", "secret", "value"} {
			if expr := terraformAttrExpr(body, attr); expr != "" {
				out = append(out, inlineTerraformExpr(mod, expr, 0))
			}
		}
	}
	return out
}

// inlineTerraformExpr replaces every var.x and local.x with the expression it
// is defined as, in place and in parentheses. In place, and not as a list of
// expansions, because that is what keeps an exclusion an exclusion: the class
// modules/workflow/sa.tf drops sits inside `!( ... )`, and a reference that is
// only ever reached from within that span must not be read as one the mount
// admits. The depth bound stops a local that names itself.
func inlineTerraformExpr(mod *tfModule, expr string, depth int) string {
	if expr == "" || depth > 4 {
		return expr
	}
	var b strings.Builder
	last := 0
	for _, m := range tfRefRE.FindAllStringSubmatchIndex(expr, -1) {
		var def string
		switch expr[m[2]:m[3]] {
		case "var":
			def = mod.vars[expr[m[4]:m[5]]]
		case "local":
			def = mod.locals[expr[m[4]:m[5]]]
		}
		if def == "" {
			continue
		}
		b.WriteString(expr[last:m[0]])
		b.WriteString("(" + inlineTerraformExpr(mod, def, depth+1) + ")")
		last = m[1]
	}
	if last == 0 {
		return expr
	}
	b.WriteString(expr[last:])
	return b.String()
}

// ungatedClassRef returns the class token this expression admits, or "": a
// reference to the class that is not inside a `!( ... )`. Every drop in this
// tree is written that way — job_mounts and job_secret_names in
// modules/workflow/sa.tf, is_shared_job_served_model in modules/project/sa.tf,
// build_mounts_served in modules/repo/build.tf — so a reference outside one is
// a mount that keeps the class rather than one that excludes it.
func ungatedClassRef(expr string) string {
	spans := negatedSpans(expr)
	for _, token := range runJobMountClassTokens {
		for at := 0; at < len(expr); {
			j := strings.Index(expr[at:], token)
			if j < 0 {
				break
			}
			at += j
			if !insideSpans(spans, at) {
				return token
			}
			at += len(token)
		}
	}
	return ""
}

// negatedSpans is every `!( ... )` in an expression, by the offsets of its
// parentheses.
func negatedSpans(s string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(s); i++ {
		if s[i] != '!' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] != '(' {
			continue
		}
		if end, ok := matchingParen(s, j); ok {
			spans = append(spans, [2]int{j, end})
		}
	}
	return spans
}

func matchingParen(s string, open int) (int, bool) {
	depth := 0
	inString := false
	for i := open; i < len(s); i++ {
		c := s[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func insideSpans(spans [][2]int, at int) bool {
	for _, span := range spans {
		if span[0] < at && at < span[1] {
			return true
		}
	}
	return false
}

// blankSpan replaces a byte range with spaces, keeping the newlines so the
// line-anchored patterns still see the same lines.
func blankSpan(s string, start, end int) string {
	b := []byte(s)
	for i := start; i < end && i < len(b); i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

// deliveryGrammarRef returns the delivery prefix a mount names, or "": a job
// that names fugaro_p_<name> or fugaro_u_<sub>_<name> mounts the installation's
// own copy, which D3324 stopped deriving and IS13 retires — the run reads that
// value from the credentials service instead (D3323).
func deliveryGrammarRef(s string) string {
	for _, prefix := range []string{secretname.ProjectPrefix, secretname.UserPrefix} {
		if strings.Contains(s, prefix) {
			return prefix
		}
	}
	return ""
}

func runJobMountHitsInFiles(files []tfFile) []string {
	var hits []string
	for _, site := range runJobSecretSites(files) {
		for _, expr := range site.expressions {
			if token := ungatedClassRef(expr); token != "" {
				hits = append(hits, fmt.Sprintf("%s mounts the %s class (%s): a run job reads that class from the credentials service (D3331, T3)", site.key, runJobClassWord(token), token))
			}
		}
		if prefix := deliveryGrammarRef(site.body); prefix != "" {
			hits = append(hits, fmt.Sprintf("%s mounts a %s container: the installation's copy is not a run job's to mount (D3324)", site.key, prefix))
		}
	}
	return hits
}

func runJobClassWord(token string) string {
	switch token {
	case "vault_secret_names":
		return "workflow-secret"
	case "build_secrets":
		return "build-secret"
	}
	return token
}

// goMountHits is the rule's Go half: a run job's or a build's spec is written
// by these builders, so a mount of the class here is the same defect the
// Terraform half refuses — and a delivery name derived here is one no run job
// has any business naming.
func goMountHits(t *testing.T, rel, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var hits []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(x.Value)
			if err != nil {
				return true
			}
			if prefix := deliveryGrammarRef(value); prefix != "" {
				hits = append(hits, fmt.Sprintf("%s:%d names a %s container: the installation's copy is not a run job's to mount (D3324)", rel, fset.Position(x.Pos()).Line, prefix))
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "secretname" && (sel.Sel.Name == "Project" || sel.Sel.Name == "User") {
				hits = append(hits, fmt.Sprintf("%s:%d derives a delivery name with secretname.%s: no run job mounts the installation's copy (D3324)", rel, fset.Position(x.Pos()).Line, sel.Sel.Name))
			}
		}
		return true
	})
	walk := &goMountWalk{fset: fset, rel: rel, seen: map[*ast.FuncLit]bool{}}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			walk.stmts(fn.Body.List, false, false)
		}
	}
	hits = append(hits, walk.hits...)
	return hits
}

// goMountWalk carries two facts down a function's statements: whether the
// statement is under a serve gate, and whether the workflow's own secrets are
// in scope. A mount written from the class with neither is the defect; both
// flags are inherited, because the gate is an enclosing `if !served()` and the
// class is an enclosing `for _, s := range w.Secrets`.
type goMountWalk struct {
	fset *token.FileSet
	rel  string
	hits []string
	seen map[*ast.FuncLit]bool
}

func (w *goMountWalk) stmts(list []ast.Stmt, gated, class bool) {
	for _, st := range list {
		w.stmt(st, gated, class)
	}
}

func (w *goMountWalk) stmt(st ast.Stmt, gated, class bool) {
	if st == nil {
		return
	}
	text := w.text(st)
	gated = gated || mentionsAnyFold(text, goServeGateWords)
	class = class || mentionsAny(text, goMountClassRefs)
	if writesMountField(st) && class && !gated {
		w.hits = append(w.hits, fmt.Sprintf("%s:%d mounts the workflow's own secrets with no serve gate: the mount and its accessor are one answer (D3331, C2.3)", w.rel, w.fset.Position(st.Pos()).Line))
	}
	ast.Inspect(st, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok || w.seen[lit] {
			return true
		}
		w.seen[lit] = true
		w.stmts(lit.Body.List, gated, class)
		return true
	})
	switch x := st.(type) {
	case *ast.BlockStmt:
		w.stmts(x.List, gated, class)
	case *ast.IfStmt:
		w.stmt(x.Init, gated, class)
		w.stmts(x.Body.List, gated, class)
		if x.Else != nil {
			w.stmt(x.Else, gated, class)
		}
	case *ast.ForStmt:
		w.stmts(x.Body.List, gated, class)
	case *ast.RangeStmt:
		w.stmts(x.Body.List, gated, class)
	case *ast.SwitchStmt:
		for _, c := range x.Body.List {
			w.stmt(c, gated, class)
		}
	case *ast.TypeSwitchStmt:
		for _, c := range x.Body.List {
			w.stmt(c, gated, class)
		}
	case *ast.SelectStmt:
		for _, c := range x.Body.List {
			w.stmt(c, gated, class)
		}
	case *ast.CaseClause:
		w.stmts(x.Body, gated, class)
	case *ast.CommClause:
		w.stmts(x.Body, gated, class)
	case *ast.LabeledStmt:
		w.stmt(x.Stmt, gated, class)
	}
}

func (w *goMountWalk) text(n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, w.fset, n); err != nil {
		return ""
	}
	return b.String()
}

// writesMountField reports whether a statement writes a secret INTO a spec: an
// assignment whose target is a mount field, through an index or directly.
// delete(ws.SecretEnv, ...) is not one — the drop is the rule, not the defect.
func writesMountField(st ast.Stmt) bool {
	as, ok := st.(*ast.AssignStmt)
	if !ok {
		return false
	}
	for _, lhs := range as.Lhs {
		for e := lhs; ; {
			switch x := e.(type) {
			case *ast.IndexExpr:
				e = x.X
			case *ast.SelectorExpr:
				if slices.Contains(goMountFields, x.Sel.Name) {
					return true
				}
				e = x.X
			default:
				e = nil
			}
			if e == nil {
				break
			}
		}
	}
	return false
}

func mentionsAny(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// mentionsAnyFold is mentionsAny without case: the gates are spelled
// buildMountsServed, CredentialsServiceServes, credentialsServiceAddressed and
// the bare `served` a parameter carries, so the words have to match whatever
// case the identifier wears.
func mentionsAnyFold(text string, words []string) bool {
	lower := strings.ToLower(text)
	for _, word := range words {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// The fixtures the rule is shown to fail on (spec §6). The first two are the
// shapes the flip removed: the fetched class mounted whole, and the
// installation's own fugaro_p_ copy named by a job. The last three are the
// shape IS6 and IS8 leave, which the rule must pass.
const fixtureWorkflowClassMounted = `
locals {
  vault_secret_names = var.workflow.vault_secret_names
}

resource "google_cloud_run_v2_job" "this" {
  template {
    template {
      containers {
        dynamic "env" {
          for_each = toset(local.vault_secret_names)
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = var.secrets[env.key]
                version = "latest"
              }
            }
          }
        }
      }
    }
  }
}
`

const fixtureDeliveryNameMounted = `
resource "google_cloud_run_v2_job" "this" {
  template {
    template {
      containers {
        dynamic "env" {
          for_each = var.repo.check.secret_env
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = "fugaro_p_${env.value}"
                version = "latest"
              }
            }
          }
        }
      }
    }
  }
}
`

const fixtureServeGatedDrop = `
locals {
  service_addressed  = var.credentials_service != null
  vault_secret_names = var.workflow.vault_secret_names
  job_mounts = {
    for env, logical in var.workflow.secret_env :
    env => logical if !(local.service_addressed && contains(local.vault_secret_names, logical))
  }
}
`

const fixtureGatedMount = `
resource "google_cloud_run_v2_job" "this" {
  template {
    template {
      containers {
        dynamic "env" {
          for_each = local.job_mounts
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = var.secrets[env.value]
                version = "latest"
              }
            }
          }
        }
      }
    }
  }
}
`

const fixtureGoMountsTheClass = `
package gcp

func mount(ws *WorkflowSpec, w config.Workflow) {
	for _, s := range w.Secrets {
		ws.SecretEnv[s.Env] = s.Name
	}
}
`

const fixtureGoDerivesDeliveryName = `
package gcp

func mount(ws *WorkflowSpec, name string) {
	id, err := secretname.Project(name)
	if err != nil {
		return
	}
	ws.SecretEnv["BUILD_TOKEN"] = id
}
`

const fixtureGoServeGatedMount = `
package gcp

func mount(ws *WorkflowSpec, w config.Workflow, c *ctx) {
	if !c.buildMountsServed() {
		for _, s := range w.Secrets {
			ws.SecretEnv[s.Env] = s.Name
		}
	}
}
`

const fixtureGoMountsTheBuildClass = `
package gcp

func request(b *cloudbuild.Build, s BuildSpec) {
	for _, ws := range s.WorkflowSecrets {
		b.AvailableSecrets.SecretManager = append(b.AvailableSecrets.SecretManager, &cloudbuild.SecretManagerSecret{Env: ws.Env})
	}
}
`
