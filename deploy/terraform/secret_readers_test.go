package terraform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
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
