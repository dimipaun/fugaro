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
	{id: "E5", keys: []string{"internal/backend/batch/run.go"}},
	{id: "E6", keys: []string{"internal/backend/gcp/build.go"}},
	{
		id: "E7",
		keys: []string{
			"internal/credsvc/bundle.go",
			"internal/cli/credentials_serve.go",
			"deploy/terraform/gcp/modules/installation/credentials.tf google_secret_manager_secret_iam_member.credentials_bundle",
		},
	},
	{id: "E8", keys: []string{"internal/cli/creds.go", "internal/cli/doctor_brokering.go"}},
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
