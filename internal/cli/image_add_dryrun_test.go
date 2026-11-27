package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// updateImageAddPlanGolden rewrites testdata/image_add_plan.golden.json, the
// document the dry run of an add prints.
var updateImageAddPlanGolden = flag.Bool("update-image-plan", false, "rewrite testdata/image_add_plan.golden.json")

// imageAddPlanFixture is the machine the dry-run tests read: the catalog
// fixture's two entries, with nothing owed and nothing built (D1140, D1561).
func imageAddPlanFixture(t *testing.T) *cloudFixture {
	t.Helper()
	return catalogFixture(t)
}

// TestImageAddUnchanged is written before --dry-run exists and stays green
// under it: without the new flags an add prints exactly the three lines it
// printed before — the write's own backup line, the chain's saved line and the
// sentence naming `fugaro apply` — and writes the same entry and the same
// record. A document bled into the plain invocation, or a step no longer
// queued, is what this fails on.
func TestImageAddUnchanged(t *testing.T) {
	imageAddPlanFixture(t)
	df := entryDockerfile(t, "FROM scratch\n")

	out, _, err := execute(t, "image", "add", "extra", "--tags", "redis,postgres", "--inline", df, "--description", "a redis image", "--no-apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("image add --no-apply printed %d lines, want the three it always had:\n%s", len(lines), out)
	}
	if !regexp.MustCompile(`^wrote .*aurora\.yaml \(the old file is at .*\.bak-\d{8}T\d{6}Z\)$`).MatchString(lines[0]) {
		t.Errorf("the write's own line changed:\n%s", lines[0])
	}
	if lines[1] != "saved, not live yet: catalog-build, repository, policy owed" {
		t.Errorf("the saved line changed:\n%s", lines[1])
	}
	if lines[2] != "Run `fugaro apply` to run it: every step checks the state first, so nothing is done twice." {
		t.Errorf("the sentence naming fugaro apply changed:\n%s", lines[2])
	}

	lc, err := localcfg.Parse([]byte(configOnDisk(t)))
	if err != nil {
		t.Fatalf("the config after the add: %v", err)
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 3 {
		t.Fatalf("ImageCatalog = %+v, want the two entries plus the new one", lc.ImageCatalog)
	}
	added := lc.ImageCatalog.Entries[2]
	if added.Name != "extra" || added.Source.Kind != config.SourceInline || added.Source.Inline != "FROM scratch\n" {
		t.Errorf("the written entry = %+v", added)
	}
	if strings.Join(added.Tags, ",") != "redis,postgres" || added.Description != "a redis image" {
		t.Errorf("the written entry's tags and description = %v %q", added.Tags, added.Description)
	}
	if names := record(t).Names(); !slices.Equal(names, []string{"catalog-build", "repository", "policy"}) {
		t.Errorf("the queued steps = %v, want the catalog's three", names)
	}
}

// TestImageAddDryRunWritesNothing: `--dry-run` validates the definition as the
// real command does and stops there (O4): the project config keeps its bytes,
// no build is submitted — the fixture's installation has no Cloud Build
// endpoint, so an add that tried to build would fail rather than pass — no
// step is queued, and the catalog a later read names does not carry the entry.
func TestImageAddDryRunWritesNothing(t *testing.T) {
	imageAddPlanFixture(t)
	before := configOnDisk(t)
	if _, err := os.Stat(recordPath(t)); !os.IsNotExist(err) {
		t.Fatalf("the fixture already owes something: %v", err)
	}

	// The console's own shape: --dockerfile, --no-apply, and --dry-run. Nothing
	// is built here and the run must still answer.
	out, _, err := execute(t, "image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--description", "the app image", "--no-apply", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "app") || !strings.Contains(out, "catalog-build") {
		t.Errorf("the dry run printed no account of the entry and its steps:\n%s", out)
	}
	after := configOnDisk(t)
	if after != before {
		t.Errorf("the dry run wrote the project config:\n%s", after)
	}
	if _, err := os.Stat(recordPath(t)); !os.IsNotExist(err) {
		t.Errorf("the dry run queued a step (stat %s: %v)", recordPath(t), err)
	}
	// The entry is not in the catalog afterwards: a dry run that had written
	// the file would leave it there, and `image ls` is what reads it back.
	lsOut, _, err := execute(t, "image", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, lsOut)
	}
	var listed catalogLsOut
	if err := json.Unmarshal([]byte(lsOut), &listed); err != nil {
		t.Fatalf("%v\n%s", err, lsOut)
	}
	for _, e := range listed.Entries {
		if e.Name == "app" {
			t.Errorf("the dry run added the entry:\n%s", lsOut)
		}
	}

	// Without --no-apply the same dry run still writes nothing: the document
	// says the steps would run, and it is the document alone that changes.
	out, _, err = execute(t, "image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc imageAddPlanDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the dry run's output is not one document: %v\n%s", err, out)
	}
	for _, step := range doc.Steps {
		if step.Saved {
			t.Errorf("a step of a dry run without --no-apply is saved: %+v", doc.Steps)
		}
	}
	if got := configOnDisk(t); got != before {
		t.Errorf("the dry run without --no-apply wrote the project config:\n%s", got)
	}
	if _, err := os.Stat(recordPath(t)); !os.IsNotExist(err) {
		t.Errorf("the dry run without --no-apply queued a step")
	}

	// The whole flag set travels with the dry run, as the console passes it:
	// --project and --config name the installation and the file the entry
	// would land in, and the document names the project it read them from.
	out, _, err = execute(t, "image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--project", initProjectName, "--config", os.Getenv("FUGARO_CONFIG"), "--no-apply", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var aimed imageAddPlanDoc
	if err := json.Unmarshal([]byte(out), &aimed); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if aimed.Project != initProjectName {
		t.Errorf("the aimed dry run's project = %q, want %q", aimed.Project, initProjectName)
	}
	if got := configOnDisk(t); got != before {
		t.Errorf("the aimed dry run wrote the project config:\n%s", got)
	}
}

// TestImageAddDryRunDocument holds the dry run's document to the golden
// recorded from the CLI's own output: the entry the flags name (its name, tags,
// description, default and source), the command, the installation, and the
// three steps the add owes with whether --no-apply left them to `fugaro apply`.
func TestImageAddDryRunDocument(t *testing.T) {
	imageAddPlanFixture(t)
	out, _, err := execute(t, "image", "add", "app", "--tags", "node,redis", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--branch", "release-1.0", "--description", "the app image", "--no-apply", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	path := filepath.Join("testdata", "image_add_plan.golden.json")
	if *updateImageAddPlanGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/cli -run TestImageAddDryRunDocument -update-image-plan")
	}
	if out != string(want) {
		t.Errorf("the dry run's document changed:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}

	var doc imageAddPlanDoc
	if err := json.Unmarshal(want, &doc); err != nil {
		t.Fatalf("the golden is not this command's document: %v", err)
	}
	if doc.Version != imageAddPlanVersion || doc.Command != imageAddDoor || doc.Project != initProjectName {
		t.Errorf("the document's own facts = %+v", doc)
	}
	if doc.Entry.Name != "app" || strings.Join(doc.Entry.Tags, ",") != "node,redis" ||
		doc.Entry.Description != "the app image" || doc.Entry.Default ||
		!strings.Contains(doc.Entry.Source, "acme/app:.fugaro/app.Dockerfile") {
		t.Errorf("the document's entry = %+v", doc.Entry)
	}
	wantSteps := []string{apply.CatalogBuild, initflow.Repository, initflow.Policy}
	if len(doc.Steps) != len(wantSteps) {
		t.Fatalf("the document's steps = %+v, want %v", doc.Steps, wantSteps)
	}
	for i, step := range doc.Steps {
		if step.Name != wantSteps[i] || !step.Saved {
			t.Errorf("step %d = %+v, want %s saved", i, step, wantSteps[i])
		}
	}
}

// TestImageAddDryRunRefusesAsTheCommandDoes: the dry run is the same command
// with the same refusals — the same sentence, the same exit code, and nothing
// written — for the definition's own rules, the catalog's taken name and
// D1573's agent refusal alike.
func TestImageAddDryRunRefusesAsTheCommandDoes(t *testing.T) {
	df := entryDockerfile(t, "FROM scratch\n")
	cases := []struct {
		name string
		args []string
	}{
		{"a name the catalog already has", []string{"image", "add", "base", "--tags", "node", "--dockerfile", "acme/app:.fugaro/base.Dockerfile"}},
		{"a --dockerfile that is not owner/name:path", []string{"image", "add", "bad", "--tags", "node", "--dockerfile", "acme/app"}},
		{"two definitions for one entry", []string{"image", "add", "bad", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--image", "docker.io/library/node:22"}},
		{"no definition at all", []string{"image", "add", "bad", "--tags", "node"}},
		{"a branch for an inline definition", []string{"image", "add", "bad", "--tags", "node", "--inline", df, "--branch", "main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageAddPlanFixture(t)
			before := configOnDisk(t)

			_, _, plainErr := execute(t, tc.args...)
			if plainErr == nil || ExitCode(plainErr) != ExitUserError {
				t.Fatalf("the command did not refuse %v: %v", tc.args, plainErr)
			}
			_, _, dryErr := execute(t, append(slices.Clone(tc.args), "--dry-run")...)
			if dryErr == nil || ExitCode(dryErr) != ExitUserError {
				t.Fatalf("the dry run did not refuse %v: %v", tc.args, dryErr)
			}
			if ErrorText(plainErr) != ErrorText(dryErr) {
				t.Errorf("the two refusals differ:\n--dry-run: %s\n         : %s", ErrorText(dryErr), ErrorText(plainErr))
			}
			if got := configOnDisk(t); got != before {
				t.Errorf("a refused dry run wrote the project config:\n%s", got)
			}
		})
	}
}

// TestImageAddPlanSchema validates the dry run's document against
// schemas/image-add-plan.schema.json, the contract a consumer reads it by
// (D848).
func TestImageAddPlanSchema(t *testing.T) {
	imageAddPlanFixture(t)
	out, _, err := execute(t, "image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--description", "the app image", "--no-apply", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "image-add-plan.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/image-add-plan.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/image-add-plan.schema.json: %v\n%s", err, out)
	}

	// The document is only worth validating while it carries the members the
	// schema closes over: a fixture that stopped producing the entry's members
	// or a step would leave those branches unchecked and still green.
	var decoded struct {
		Version int `json:"version"`
		Entry   struct {
			Name        string   `json:"name"`
			Tags        []string `json:"tags"`
			Description string   `json:"description"`
			Source      string   `json:"source"`
		} `json:"entry"`
		Steps []struct {
			Name  string `json:"name"`
			Saved bool   `json:"saved"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || decoded.Entry.Name == "" || len(decoded.Entry.Tags) == 0 ||
		decoded.Entry.Description == "" || decoded.Entry.Source == "" || len(decoded.Steps) == 0 {
		t.Fatalf("the document leaves the schema's members unexercised: %s", out)
	}
}

// TestImageAddJSONNeedsDryRun: --json is the dry run's own shape, so it is
// refused without --dry-run, in the words that name it, and nothing is written
// either way.
func TestImageAddJSONNeedsDryRun(t *testing.T) {
	imageAddPlanFixture(t)
	df := entryDockerfile(t, "FROM scratch\n")
	before := configOnDisk(t)

	for _, args := range [][]string{
		{"image", "add", "extra", "--tags", "redis", "--inline", df, "--json"},
		{"image", "add", "extra", "--tags", "redis", "--inline", df, "--no-apply", "--json"},
	} {
		out, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(ErrorText(err), "--dry-run") {
			t.Errorf("%v: exit %d, err %v, want it refused naming --dry-run", args, ExitCode(err), err)
		}
		if strings.Contains(out, `"version"`) {
			t.Errorf("%v printed a document:\n%s", args, out)
		}
		if got := configOnDisk(t); got != before {
			t.Errorf("%v wrote the project config:\n%s", args, got)
		}
	}
}
