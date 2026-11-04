package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// catalogYAML is the image catalog a catalog fixture's project names: two
// entries, one of them the default and one carrying a description, and the
// two source kinds a person writes by hand (D1145).
const catalogYAML = `image_catalog:
  entries:
    - name: base
      tags: [node, postgres]
      default: true
      description: the everyday image
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/base.Dockerfile }
    - name: tools
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
`

// catalogFixture is newCloudFixture with the catalog above appended to the
// project config, which is where a person writes it (the published node is
// fugaro update's copy of this file, D1140).
func catalogFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, catalogYAML)
	return f
}

// catalogCheckout makes the working directory a checkout of acme/app whose
// fugaro.yaml is yaml, with an origin the project's config knows, so
// checkoutConfig resolves it (the way `fugaro image rm` asks which
// repositories still point at an entry).
func catalogCheckout(t *testing.T, yaml string) string {
	t.Helper()
	return catalogCheckoutFiles(t, yaml, nil)
}

// catalogCheckoutFiles is catalogCheckout with the other files a fixture's
// definition needs: a workflow's dockerfile: has to be a file that is there,
// or config.Parse refuses the checkout before the command under test reads it.
func catalogCheckoutFiles(t *testing.T, yaml string, extra map[string]string) string {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = yaml
	for name, body := range extra {
		files[name] = body
	}
	dir := checkoutWith(t, files)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	return dir
}

// writeCatalogRecord stores the build record a catalog build writes for
// entry, as internal/backend/gcp's record step writes it.
func writeCatalogRecord(t *testing.T, f *cloudFixture, slug, entry, digest string) {
	t.Helper()
	key, err := gcp.CatalogRecordKey(slug, entry)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := json.Marshal(imagecheck.CatalogRecordState{
		Version: 1, Entry: entry, SourceCommit: strings.Repeat("a", 40),
		DefinitionGeneration: 1, DefinitionSHA256: strings.Repeat("b", 64),
		BaseRef: "ghcr.io/dimipaun/fugaro-base:0.16.0", BaseDigest: "sha256:" + strings.Repeat("c", 64),
		Image:      "us-east5-docker.pkg.dev/proj-1234/fugaro-shared/catalog-" + entry,
		ContentTag: strings.Repeat("b", 64), ImageDigest: digest, BuildID: "build-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, string(rec))
}

// TestImageLsListsCatalogEntriesAndTheDefault: `fugaro image ls` lists the
// catalog's entries by default — name, tags, which one is the default and
// the digest of the image the last build recorded — and `--shared` is the
// other source of the same command (D1143).
func TestImageLsListsCatalogEntriesAndTheDefault(t *testing.T) {
	f := catalogFixture(t)
	writeCatalogRecord(t, f, appSlug, "base", "sha256:"+strings.Repeat("d", 64))

	out, _, err := execute(t, "image", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base", "node", "postgres", "default", "the everyday image", "tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("image ls lacks %q:\n%s", want, out)
		}
	}
	// The default marker is on one entry and not the other: a listing that
	// marked both (or neither) would leave the tie-break of D1146 unreadable.
	var marked []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "base") || strings.Contains(line, "tools") {
			marked = append(marked, line)
		}
	}
	if len(marked) != 2 {
		t.Fatalf("want one row per entry, got %d:\n%s", len(marked), out)
	}
	if !strings.Contains(marked[0], "default") || strings.Contains(marked[1], "default") {
		t.Errorf("the default marker is not on exactly the entry the catalog marks:\n%s", out)
	}
	// The digest comes from the record a build wrote, not from the entry's
	// definition: an entry nothing built shows none.
	if !strings.Contains(marked[0], "sha256:"+strings.Repeat("d", 64)) {
		t.Errorf("the built entry's row lacks the digest its record holds:\n%s", out)
	}
	if strings.Contains(marked[1], "sha256:") {
		t.Errorf("an entry nothing built shows a digest:\n%s", out)
	}

	asJSON, _, err := execute(t, "image", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Project string `json:"project"`
		Entries []struct {
			Name    string   `json:"name"`
			Tags    []string `json:"tags"`
			Default bool     `json:"default"`
			Digest  string   `json:"digest"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(asJSON), &got); err != nil {
		t.Fatalf("%v: %s", err, asJSON)
	}
	if got.Project != "aurora" || len(got.Entries) != 2 {
		t.Fatalf("--json = %+v, want the project and two entries", got)
	}
	if got.Entries[0].Name != "base" || !got.Entries[0].Default || got.Entries[0].Digest == "" {
		t.Errorf("first entry = %+v", got.Entries[0])
	}
	if got.Entries[1].Default || got.Entries[1].Digest != "" {
		t.Errorf("second entry = %+v", got.Entries[1])
	}
}

// TestImageLsSharedListsTheLockEntries: `--shared` lists the shared-image
// lock's entries instead of the catalog (D1143), the C8c design's columns.
func TestImageLsSharedListsTheLockEntries(t *testing.T) {
	f := catalogFixture(t)
	lock, err := json.Marshal(imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		"node20": {
			Key: "node20", Image: "us-east5-docker.pkg.dev/proj-1234/fugaro-shared/node20",
			Digest: "sha256:" + strings.Repeat("e", 64), BaseRef: "ghcr.io/dimipaun/fugaro-base:0.16.0",
			Repositories: []string{"acme/app"}, Jobs: map[string]string{"acme/app": "web"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.LockKey, string(lock))

	out, _, err := execute(t, "image", "ls", "--shared")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node20", "sha256:" + strings.Repeat("e", 64), "acme/app", "web"} {
		if !strings.Contains(out, want) {
			t.Errorf("image ls --shared lacks %q:\n%s", want, out)
		}
	}
	// The catalog is not what --shared prints: the two sources are one
	// command with a flag, and a listing that printed both would leave a
	// reader unable to tell which entry came from where.
	if strings.Contains(out, "the everyday image") {
		t.Errorf("--shared printed the catalog:\n%s", out)
	}
}

// TestImageAddWritesAnEntry: `fugaro image add` writes the entry into the
// project config, which is the copy fugaro update publishes (D1140, D1145).
// It backs the file up first, as every other writer of this file does.
func TestImageAddWritesAnEntry(t *testing.T) {
	catalogFixture(t)
	df := filepath.Join(t.TempDir(), "extra.Dockerfile")
	if err := os.WriteFile(df, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "image", "add", "extra", "--tags", "redis,postgres", "--inline", df, "--description", "a redis image")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "extra") {
		t.Errorf("image add printed %q, want the entry it wrote", out)
	}
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the config after the add: %v\n%s", err, data)
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 3 {
		t.Fatalf("ImageCatalog = %+v, want the two entries plus the new one", lc.ImageCatalog)
	}
	added := lc.ImageCatalog.Entries[2]
	if added.Name != "extra" || added.Source.Kind != config.SourceInline || added.Source.Inline != "FROM scratch\n" {
		t.Errorf("the written entry = %+v", added)
	}
	if strings.Join(added.Tags, ",") != "redis,postgres" {
		t.Errorf("tags = %v", added.Tags)
	}
	// The two entries the file already had are still there: an add that
	// replaced the catalog would drop an operator's images silently.
	names := []string{lc.ImageCatalog.Entries[0].Name, lc.ImageCatalog.Entries[1].Name}
	if strings.Join(names, ",") != "base,tools" {
		t.Errorf("the existing entries are %v", names)
	}
	// A second add of the same name is refused, and the file is unchanged.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "image", "add", "extra", "--tags", "redis", "--inline", df)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("adding a taken name: exit %d, err %v", ExitCode(err), err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a refused add still wrote the config")
	}

	// --dockerfile names a Dockerfile in one of the project's repositories,
	// as owner/name:path, and --branch is the branch the build reads it from:
	// the entry keeps the pointer, so it follows the file rather than a copy
	// of it.
	if _, _, err := execute(t, "image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--branch", "release-1.0"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err = localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the config after the add: %v\n%s", err, data)
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 4 {
		t.Fatalf("ImageCatalog = %+v, want the three entries plus the new one", lc.ImageCatalog)
	}
	src := lc.ImageCatalog.Entries[3].Source
	if src.Kind != config.SourceDockerfile || src.Repo != "acme/app" || src.Path != ".fugaro/app.Dockerfile" || src.Branch != "release-1.0" {
		t.Errorf("the written source = %+v, want the repository, the path and the branch", src)
	}

	// A --dockerfile that is not owner/name:path is refused before anything
	// is written: a pointer the build could not read is not an entry.
	for _, df := range []string{"acme/app", ":path", "acme/app:"} {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = execute(t, "image", "add", "bad", "--dockerfile", df)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "owner/name:path") {
			t.Errorf("--dockerfile %q: exit %d, err %v, want it refused as owner/name:path", df, ExitCode(err), err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Errorf("--dockerfile %q wrote the config", df)
		}
	}

	// The two definitions are one entry's alternatives, and --branch belongs
	// to the pointer alone: neither pair may be given together.
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"both definitions", []string{"--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--inline", df}, "--inline"},
		{"a branch for an inline definition", []string{"--inline", df, "--branch", "main"}, "--branch"},
		{"no definition at all", []string{"--tags", "node"}, "needs a definition"},
	} {
		_, _, err := execute(t, append([]string{"image", "add", "bad"}, c.args...)...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: exit %d, err %v, want it refused naming %q", c.name, ExitCode(err), err, c.want)
		}
	}
}

// TestImageRmRefusesAnEntryAJobStillPointsAt: removing an entry a
// repository's job runs would leave that job on an image no catalog
// describes, so the removal is refused and nothing is written (D1145).
func TestImageRmRefusesAnEntryAJobStillPointsAt(t *testing.T) {
	catalogFixture(t)
	catalogCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { image_name: tools, commands: { build: sh build.sh, test: sh test.sh } }\n")
	path := os.Getenv("FUGARO_CONFIG")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, "image", "rm", "tools")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v, want the removal refused", ExitCode(err), err)
	}
	for _, want := range []string{"tools", "acme/app", "app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %v lacks %q", err, want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a refused rm still wrote the config")
	}

	// An entry no job runs is removed, and the file says so.
	out, _, err := execute(t, "image", "rm", "base")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "base") {
		t.Errorf("image rm printed %q, want the entry it removed", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the config after the rm: %v\n%s", err, data)
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 1 || lc.ImageCatalog.Entries[0].Name != "tools" {
		t.Fatalf("ImageCatalog = %+v, want only tools left", lc.ImageCatalog)
	}
}

// TestImageCatalogGateIsSpelledApartFromThePriceCatalog: the image family's
// skew gate is the image catalog's own pair (D1156), so it judges an image
// document by the release that reads image_catalog and never by the price
// catalog's catalogSince (F14).
func TestImageCatalogGateIsSpelledApartFromThePriceCatalog(t *testing.T) {
	var warn strings.Builder
	if err := imageCatalogSkew("the catalog build of entry base", "", imageCatalogShipped, &warn); err != nil {
		t.Fatalf("no job image at all: %v, want no refusal (the job is built from this release's own image)", err)
	}
	if err := imageCatalogSkew("the catalog build of entry base", "ghcr.io/dimipaun/fugaro-base:"+imageCatalogSince, true, &warn); err != nil {
		t.Fatalf("an image at %s: %v, want no refusal", imageCatalogSince, err)
	}
	err := imageCatalogSkew("the catalog build of entry base", "ghcr.io/dimipaun/fugaro-base:0.16.0", true, &warn)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("an older release image: exit %d, err %v, want refused", ExitCode(err), err)
	}
	for _, want := range []string{"the catalog build of entry base", "image_catalog", "fugaro " + imageCatalogSince + " or later", "release 0.16.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), catalogSince) {
		t.Errorf("the refusal names the price catalog's release (%s): %v", catalogSince, err)
	}
	// Before the release that reads the key exists, an image nothing can
	// date is refused too: no release reads image_catalog yet, so an
	// operator cannot be told that one does.
	err = imageCatalogSkew("the catalog build of entry base", "ghcr.io/dimipaun/fugaro-base:dev-abc", false, &warn)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "image_catalog") {
		t.Fatalf("an undated image while the key is unshipped: exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(err.Error(), catalogSince) {
		t.Errorf("the refusal names the price catalog's release (%s): %v", catalogSince, err)
	}
}

// TestUpdatePrintsTheDeployTimePinWarning: D1146's warning is computed where
// the job's image is chosen — deploy time — and rides the spec
// (WorkflowSpec.Warnings, PIC5). fugaro update reads it and prints it, so the
// run that moves a job onto an entry its needs have outgrown says so, and the
// --json result carries it too. Every account a workflow may choose among
// gets its own spec, and the same warning in each: a person reads it once.
func TestUpdatePrintsTheDeployTimePinWarning(t *testing.T) {
	const pin = "the catalog entry base this job pins does not cover the tags it needs: missing [node]"
	var out strings.Builder
	r := &initRun{w: &out}
	printWorkflowWarnings(r, infra.RepoSpec{Workflows: map[string]infra.WorkflowSpec{
		"app":     {Name: "app", Warnings: []string{pin}},
		"app__b":  {Name: "app", Warnings: []string{pin}},
		"deploy":  {Name: "deploy", Warnings: []string{"the catalog entry tools this job pins does not cover the tags it needs: missing [go]"}},
		"nothing": {Name: "nothing"},
	}})
	if got := strings.Count(out.String(), pin); got != 1 {
		t.Errorf("the warning is printed %d times, want once:\n%s", got, out.String())
	}
	for _, want := range []string{"warning: " + pin, "missing [go]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the update printed %q, want %q", out.String(), want)
		}
	}
	if len(r.res.Warnings) != 2 {
		t.Errorf("the run's warnings are %v, want the two the specs carry", r.res.Warnings)
	}

	// And the update path is where it is read: the field had no reader at
	// all before this, so a spec that carries a warning and a stage that
	// never asks for it is the gap this test exists for.
	src, err := os.ReadFile("init.go")
	if err != nil {
		t.Fatal(err)
	}
	call := strings.Index(string(src), "printWorkflowWarnings(r, spec)")
	if call < 0 {
		t.Fatal("init.go does not read the specs' warnings: fugaro update prints none of them")
	}
	if at := strings.LastIndex(string(src)[:call], "infra.Repo(in)"); at < 0 || call-at > 200 {
		t.Error("the warnings are read somewhere other than the stage that builds the specs fugaro update applies")
	}
}
