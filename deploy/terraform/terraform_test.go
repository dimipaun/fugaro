package terraform

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The embedded tree is what fugaro init writes into its workdir, so every
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
		"gcp/roots/repo/tests/testdata/github-vertex.tfvars.json",
		"gcp/modules/installation/history.tf",
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

// The base images are copied into fugaro-base by init, as the person running
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

// The token signer holds no roles, and no job or build account is granted
// anything on the Firebase project. The plan assertions check the values;
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
					if strings.Contains(blk.body, bad) && !(strings.Contains(blk.name, "history_") && bad == "serviceAccount:") {
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
				t.Errorf("%s declares %s: the database and its rules are init --firebase's REST steps", path, bad)
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
	allowedRoleExprs = regexp.MustCompile(`^(each\.value(\.role)?|var\.job_runner_role|var\.installation\.role_ids\.(tag_mover|build_submitter)|google_project_iam_custom_role\.(launcher|build_submitter|token_minter)\.name|google_project_iam_custom_role\.history\[0\]\.name)$`)
	// The member expressions an IAM member may use.
	allowedMemberExprs = regexp.MustCompile(`^(each\.(value|key)(\.member)?|google_service_account\.(job|build|scheduler)\.member|google_service_account\.history\[0\]\.member|"serviceAccount:\$\{var\.(history_account|installation\.scheduler_service_account)\}")$`)

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
	if role == "roles/owner" || role == "roles/editor" || role == "roles/viewer" {
		t.Errorf("%s: %s grants or names the primitive role %s", path, where, role)
		return
	}
	if !backendRoleRE.MatchString(role) || inFirebase {
		return
	}
	// serviceAccountUser (actAs, for the operators who deploy a job or a
	// build) on a service account is the one allowed shape.
	if role == "roles/iam.serviceAccountUser" && (typ == "google_service_account_iam_member" || typ == "") {
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
