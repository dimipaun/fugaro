package infra

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

func TestWriteImports(t *testing.T) {
	dir := t.TempDir()
	im := Imports{List: []Import{
		{To: "module.repo.google_service_account.build", ID: "projects/p/serviceAccounts/b@p.iam.gserviceaccount.com"},
		{To: `module.repo.google_secret_manager_secret.this["a"]`, ID: "projects/p/secrets/a"},
	}}
	if err := WriteImports(dir, im); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "imports.tf.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Import []Import `json:"import"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, b)
	}
	// Sorted by address, so equal discoveries give equal files.
	if len(doc.Import) != 2 || doc.Import[0] != im.List[1] || doc.Import[1] != im.List[0] {
		t.Errorf("imports.tf.json = %s", b)
	}
	if err := WriteImports(dir, Imports{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "imports.tf.json")); strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("no imports: %s", b)
	}
}

// moduleCallRE finds a module call and its source.
var moduleCallRE = regexp.MustCompile(`(?s)module "([a-z_]+)" \{\s*source\s*=\s*"([^"]+)"(.*?)\n\}`)

// addressStepRE is one step of an import address: module.<name>[key] or
// <type>.<name>[index].
var addressStepRE = regexp.MustCompile(`^(module\.[a-z_]+|[a-z0-9_]+\.[a-z_]+)(\[("[^"]*"|[0-9]+)\])?$`)

// splitAddress splits an address at the dots outside brackets.
func splitAddress(a string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range a {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				parts = append(parts, a[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, a[start:])
}

// joinSteps rejoins the module.<name> and <type>.<name> steps splitAddress
// cut apart.
func joinSteps(parts []string) []string {
	var steps []string
	for i := 0; i < len(parts); i += 2 {
		steps = append(steps, parts[i]+"."+parts[i+1])
	}
	return steps
}

func readDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := fs.ReadDir(terraform.FS, dir)
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	var b strings.Builder
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tf") {
			data, _ := fs.ReadFile(terraform.FS, path.Join(dir, e.Name()))
			b.Write(data)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TestImportAddressesExistInModule checks each import address against the
// embedded Terraform with checkModuleAddress.
func TestImportAddressesExistInModule(t *testing.T) {
	for kind := range importTable {
		checkModuleAddress(t, newImport(kind, "proj-1234", "us-east5", "web", "x").To)
	}
}

// checkModuleAddress checks an address against the embedded Terraform:
// every module call exists with for_each where the address has a key, and
// the resource exists in the module it names, with count where the address
// has an index and for_each where it has a key.
func checkModuleAddress(t *testing.T, to string) {
	t.Helper()
	roots := map[string]string{"installation": "gcp/roots/installation", "repo": "gcp/roots/repo"}
	steps := joinSteps(splitAddress(to))
	if len(steps) < 2 {
		t.Errorf("address %s names no module", to)
		return
	}
	first := addressStepRE.FindStringSubmatch(steps[0])
	if first == nil {
		t.Errorf("%s: step %q is not a Terraform address", to, steps[0])
		return
	}
	root, ok := roots[strings.TrimPrefix(first[1], "module.")]
	if !ok {
		t.Errorf("%s: no root calls %s", to, first[1])
		return
	}
	dir := root
	for _, step := range steps {
		m := addressStepRE.FindStringSubmatch(step)
		if m == nil {
			t.Errorf("%s: step %q is not a Terraform address", to, step)
			return
		}
		src := readDir(t, dir)
		name, key := m[1], m[3]
		if mod, ok := strings.CutPrefix(name, "module."); ok {
			var found bool
			for _, call := range moduleCallRE.FindAllStringSubmatch(src, -1) {
				if call[1] != mod {
					continue
				}
				found = true
				if hasEach := strings.Contains(call[3], "for_each"); hasEach != strings.HasPrefix(key, `"`) {
					t.Errorf("%s: module %s for_each is %v, but the address key is %q", to, mod, hasEach, key)
				}
				dir = path.Clean(path.Join(dir, call[2]))
			}
			if !found {
				t.Errorf("%s: %s has no module %q", to, dir, mod)
				return
			}
			continue
		}
		typ, rname, _ := strings.Cut(name, ".")
		re := regexp.MustCompile(`(?s)resource "` + typ + `" "` + rname + `" \{(.*?)\n\}`)
		res := re.FindStringSubmatch(src)
		if res == nil {
			t.Errorf("%s: %s has no resource %s.%s", to, dir, typ, rname)
			return
		}
		head := res[1]
		switch {
		case key == "":
			if regexp.MustCompile(`\n  (count|for_each) `).MatchString(head) {
				t.Errorf("%s: %s.%s has count or for_each, but the address has no index", to, typ, rname)
			}
		case strings.HasPrefix(key, `"`):
			if !regexp.MustCompile(`\n  for_each `).MatchString(head) {
				t.Errorf("%s: %s.%s has no for_each for key %s", to, typ, rname, key)
			}
		default:
			if !regexp.MustCompile(`\n  count `).MatchString(head) {
				t.Errorf("%s: %s.%s has no count for index %s", to, typ, rname, key)
			}
		}
	}
}
