package terraform

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
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
		for _, bad := range []string{"google_secret_manager_secret_version", "secret_data"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %s", path, bad)
			}
		}
	})
}

// Shared things are only ever granted on additively, never with a resource
// that owns the whole policy or binding.
func TestNoAuthoritativeIAM(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, bad := range []string{`_iam_binding"`, `_iam_policy"`, "google_project_iam_audit_config"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %s", path, bad)
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
		for _, typ := range []string{"google_secret_manager_secret", "google_storage_bucket", "google_artifact_registry_repository"} {
			for _, blk := range resourceBlocks(t, path, b, typ) {
				counts[typ]++
				if !hasAttr(blk.body, "prevent_destroy", "true") {
					t.Errorf("%s: %s lacks prevent_destroy = true", path, blk.name)
				}
			}
		}
	})
	for _, typ := range []string{"google_storage_bucket", "google_artifact_registry_repository"} {
		if counts[typ] == 0 {
			t.Errorf("no %s block found", typ)
		}
	}
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
		out = append(out, block{name: typ + "." + code[m[4]:m[5]], body: code[open+1 : end]})
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
