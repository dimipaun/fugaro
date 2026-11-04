package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// The project's catalog check job (D1148 (i)): one run of the project's own
// Cloud Run job decides every entry of the project's image catalog, and
// submits at most one Cloud Build per entry that fires — as that entry's own
// repository's build account, never as the job's own.

// catalogCheckCommit is the commit the fixture's records name: what a rebuild
// is pinned to, since the job clones nothing.
const catalogCheckCommit = "0123456789abcdef0123456789abcdef01234567"

const (
	catalogCheckAppDef   = "FROM example.invalid/app\nRUN true\n"
	catalogCheckOtherDef = "FROM example.invalid/other\nRUN true\n"
)

// catalogCheckEntry is the definition of the catalog entry named name: two
// entries whose Dockerfiles are acme/app's (tools, extra), one whose Dockerfile
// is acme/other's (runtime), and one prebuilt image.
func catalogCheckEntry(name string) config.Entry {
	if name == "prebuilt" {
		return config.Entry{Name: name, Tags: []string{"docker"},
			Source: config.Source{Kind: config.SourceImage, Image: "docker.io/library/node:22"}}
	}
	repo := "acme/app"
	if name == "runtime" {
		repo = "acme/other"
	}
	return config.Entry{Name: name, Tags: []string{"node"},
		Source: config.Source{Kind: config.SourceDockerfile, Repo: repo, Path: ".fugaro/" + name + ".Dockerfile"}}
}

// catalogCheckCatalog is the project's published catalog: the entries a test
// names, so each test's catalog holds exactly the entries it seeds.
func catalogCheckCatalog(names ...string) *config.Catalog {
	cat := &config.Catalog{}
	for _, n := range names {
		cat.Entries = append(cat.Entries, catalogCheckEntry(n))
	}
	return cat
}

// catalogCheckSpecFor renders the job's spec from cat, the way the
// installation renders it at fugaro update: a dockerfile-sourced entry goes to
// the repository its source names, and an entry with no repository of its own
// (an inline definition, a prebuilt image) to the project's first repository.
func catalogCheckSpecFor(t *testing.T, cat *config.Catalog, maxBuilds int) catalogCheckSpec {
	t.Helper()
	spec := catalogCheckSpec{
		Version: 1, Catalog: cat,
		BaseImages: map[string]string{config.BaseKind: checkBase},
		MachineType: "E2_HIGHCPU_8", BuildRegion: "us-east5", MaxImageBuilds: maxBuilds,
	}
	for i, repo := range []string{"acme/app", "acme/other"} {
		slug := mustSlug("bitbucket", repo)
		r := catalogCheckRepo{
			Repo: repo, Slug: slug, Provider: "bitbucket",
			RepoURL: "https://bitbucket.org/" + repo + ".git", BaseBranch: "main",
			GitSecretID: gcp.SecretID(slug, "bitbucket-token"), GitUser: bitbucket.GitUsername,
			Workflow: "app", BuildServiceAccount: gcp.BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com",
		}
		for _, e := range cat.Entries {
			switch {
			case e.Source.Kind == config.SourceDockerfile && e.Source.Repo == repo:
				r.Entries = append(r.Entries, e.Name)
			case e.Source.Kind != config.SourceDockerfile && i == 0:
				r.Entries = append(r.Entries, e.Name)
			}
		}
		if len(r.Entries) > 0 {
			spec.Repositories = append(spec.Repositories, r)
		}
	}
	return spec
}

// catalogCheckFixture is the job's world: the runs bucket (a GCS fake, so the
// copied definition has an object generation), the Cloud Build fake its
// rebuilds are submitted to, and the registry fake the base's digest is read
// from.
type catalogCheckFixture struct {
	fb    *gcpfake.Build
	gcs   *gcpfake.GCS
	reg   *gcpfake.Registry
	spec  catalogCheckSpec
	slugs map[string]string // repository -> slug
}

// newCatalogCheckJob installs the project's catalog check job for spec: the
// fakes, and the environment fugaro update sets on the job.
func newCatalogCheckJob(t *testing.T, spec catalogCheckSpec) *catalogCheckFixture {
	t.Helper()
	f := &catalogCheckFixture{fb: gcpfake.NewBuild(t), gcs: gcpfake.NewGCS(t), reg: gcpfake.NewRegistry(t), spec: spec,
		slugs: map[string]string{}}
	for _, r := range spec.Repositories {
		f.slugs[r.Repo] = r.Slug
	}
	oldBucket, oldEndpoints, oldRegistry := checkOpenBucket, checkEndpoints, checkRegistry
	t.Cleanup(func() { checkOpenBucket, checkEndpoints, checkRegistry = oldBucket, oldEndpoints, oldRegistry })
	checkOpenBucket = func(_ context.Context, url string) (*blobx.Bucket, error) {
		if url != "gs://"+catalogBuildBucket {
			t.Errorf("the catalog check opened %s, want the runs bucket", url)
		}
		return f.gcs.Bucket(t, catalogBuildBucket), nil
	}
	checkEndpoints = gcp.Endpoints{CloudBuild: f.fb.URL + "/", NoAuth: true}
	checkRegistry = func() *imagecheck.Registry {
		return &imagecheck.Registry{HTTP: &http.Client{}, Google: &http.Client{}, Endpoint: func(string) string { return f.reg.URL }}
	}
	f.reg.SetManifest("proj-1234/fugaro-base/fugaro-base", "dev-abc", checkBaseDigest)

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_PROJECT", "aurora")
	t.Setenv("FUGARO_GCP_PROJECT", "proj-1234")
	t.Setenv("FUGARO_REGION", "us-east5")
	t.Setenv("FUGARO_BUCKET", "gs://"+catalogBuildBucket)
	t.Setenv(catalogCheckSpecEnv, string(data))
	return f
}

// seed copies definition into the runs bucket as entry's Dockerfile and writes
// the record a build of it would have written, built FROM baseDigest. It
// returns the copy's object generation and its sha256, which are what a
// rebuild of it pins.
func (f *catalogCheckFixture) seed(t *testing.T, entry, repo, definition, baseDigest string) (int64, string) {
	t.Helper()
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.Put(catalogBuildBucket, key, []byte(definition))
	data, gen, err := f.gcs.Bucket(t, catalogBuildBucket).Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	recKey, err := gcp.CatalogRecordKey(f.slugs[repo], entry)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := json.Marshal(imagecheck.CatalogRecordState{
		Version: imagecheck.CatalogRecordVersion, Entry: entry, SourceCommit: catalogCheckCommit,
		DefinitionGeneration: gen, DefinitionSHA256: hex.EncodeToString(sum[:]),
		BaseRef: checkBase, BaseDigest: baseDigest, BuiltAt: time.Now().Add(-time.Hour).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.Put(catalogBuildBucket, recKey, rec)
	return gen, hex.EncodeToString(sum[:])
}

// put stores data as the object a build or a check would have written at key.
func (f *catalogCheckFixture) put(t *testing.T, key string, data []byte) {
	t.Helper()
	f.gcs.Put(catalogBuildBucket, key, data)
}

func (f *catalogCheckFixture) state(t *testing.T, entry, repo string) *imagecheck.CatalogCheckState {
	t.Helper()
	data, _, err := f.gcs.Bucket(t, catalogBuildBucket).Read(context.Background(), imagecheck.CatalogCheckKey(f.slugs[repo], entry))
	if errors.Is(err, blobx.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := imagecheck.ParseCatalogCheckState(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// catalogCheckLine is one JSON log line of the catalog check job.
type catalogCheckLine struct {
	Severity        string   `json:"severity"`
	Event           string   `json:"event"`
	Entry           string   `json:"entry"`
	Repo            string   `json:"repo"`
	Decision        string   `json:"decision"`
	Reasons         []string `json:"reasons"`
	BuildID         *string  `json:"build_id"`
	LastBuildStatus string   `json:"last_build_status"`
	Error           string   `json:"error"`
	Message         string   `json:"message"`
}

func catalogLines(t *testing.T, out string) []catalogCheckLine {
	t.Helper()
	var lines []catalogCheckLine
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var l catalogCheckLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("not a JSON line: %q", sc.Text())
		}
		if l.Event != "catalog-check" || l.Reasons == nil || l.BuildID == nil {
			t.Fatalf("line = %s", sc.Text())
		}
		lines = append(lines, l)
	}
	return lines
}

func runCatalogCheck(t *testing.T, args ...string) ([]catalogCheckLine, error) {
	t.Helper()
	out, stderr, err := executeJob(t, append([]string{"image", "check", "--job", "--catalog"}, args...)...)
	if stderr != "" {
		t.Logf("stderr: %s", stderr)
	}
	return catalogLines(t, out), err
}

// lineFor is the one line the run logged for entry.
func lineFor(t *testing.T, lines []catalogCheckLine, entry string) catalogCheckLine {
	t.Helper()
	var found []catalogCheckLine
	for _, l := range lines {
		if l.Entry == entry {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("lines for %s = %+v, want exactly one", entry, found)
	}
	return found[0]
}

// catalogCheckSelftest is the smoke spec a rebuild of entry submits, rendered
// from the image package rather than from the job's own helper: the two must
// be the same bytes.
func catalogCheckSelftest(t *testing.T, repo, commit string) []byte {
	t.Helper()
	spec, err := image.SpecForCloud(&config.Config{Workflows: map[string]config.Workflow{"app": {}}}, "app", commit, "https://bitbucket.org/"+repo+".git")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCatalogCheckJobRebuildsTheChangedEntryAsItsOwnRepositorysAccount: the
// job submits exactly the entry whose base moved, and the request is the one
// fugaro image build <entry> sends — the entry's own image in the shared
// registry, the copied definition pinned by generation and sha256, and
// acme/other's build account, which is the account of the repository the
// entry's Dockerfile is in and never the job's own.
func TestCatalogCheckJobRebuildsTheChangedEntryAsItsOwnRepositorysAccount(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools", "runtime", "prebuilt"), 5))
	f.seed(t, "tools", "acme/app", catalogCheckAppDef, checkBaseDigest)
	gen, sum := f.seed(t, "runtime", "acme/other", catalogCheckOtherDef, "sha256:"+strings.Repeat("9", 64))

	lines, err := runCatalogCheck(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildPosts(f.fb); got != 1 {
		t.Fatalf("builds submitted = %d, want 1", got)
	}

	otherSlug := f.slugs["acme/other"]
	want, err := gcp.CatalogBuildRequest("proj-1234", gcp.CatalogBuildSpec{
		Entry: "runtime", Slug: otherSlug,
		GitProvider: "bitbucket", RepoURL: "https://bitbucket.org/acme/other.git", BaseBranch: "main",
		GitSecretID: gcp.SecretID(otherSlug, "bitbucket-token"), GitUser: bitbucket.GitUsername,
		Base: checkBase, Image: catalogBuildImage(t, "runtime"),
		Definition:     gcp.CatalogDefinition{SourceCommit: catalogCheckCommit, Generation: gen, SHA256: sum},
		ServiceAccount: gcp.BuildServiceAccountID(otherSlug) + "@proj-1234.iam.gserviceaccount.com",
		MachineType:    "E2_HIGHCPU_8",
		Bucket:         "gs://" + catalogBuildBucket,
		Selftest:       catalogCheckSelftest(t, "acme/other", catalogCheckCommit),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var wantMap map[string]any
	if err := json.Unmarshal(data, &wantMap); err != nil {
		t.Fatal(err)
	}
	if got := f.fb.Last(); !reflect.DeepEqual(got, wantMap) {
		t.Fatalf("request =\n%v\nwant\n%v", got, wantMap)
	}

	// The entry that changed is the one that was rebuilt; the entry whose base
	// did not move is decided a skip.
	rt := f.state(t, "runtime", "acme/other")
	if rt == nil || rt.Decision != imagecheck.Rebuild || rt.BuildID != "b0001" || rt.BuildInputs == nil ||
		rt.BuildInputs.BaseDigest != checkBaseDigest || rt.BuildInputs.DefinitionSHA256 != sum || !slices.Equal(rt.Reasons, []string{imagecheck.ReasonBase}) {
		t.Fatalf("runtime check.json = %+v", rt)
	}
	if tools := f.state(t, "tools", "acme/app"); tools == nil || tools.Decision != imagecheck.Skip || len(tools.Reasons) != 0 {
		t.Fatalf("tools check.json = %+v", tools)
	}
	if l := lineFor(t, lines, "runtime"); l.Decision != imagecheck.Rebuild || *l.BuildID != "b0001" || l.Repo != "acme/other" || l.Severity != "INFO" {
		t.Fatalf("runtime line = %+v", l)
	}
	if l := lineFor(t, lines, "tools"); l.Decision != imagecheck.Skip {
		t.Fatalf("tools line = %+v", l)
	}
	// A prebuilt entry has nothing to build, and the job says so rather than
	// failing the run: it is passed over with no line of its own.
	for _, l := range lines {
		if l.Entry == "prebuilt" {
			t.Fatalf("prebuilt line = %+v, want none", l)
		}
	}
}

// TestCatalogCheckJobBuildsNothingFromAnUnreadableRecord: a record that cannot
// be read is not a record that is not there. It holds the definition the image
// was built from and the commit a rebuild is pinned to, so nothing is
// submitted for that entry and the run reports it.
func TestCatalogCheckJobBuildsNothingFromAnUnreadableRecord(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools", "runtime"), 5))
	f.seed(t, "tools", "acme/app", catalogCheckAppDef, checkBaseDigest)
	gen, sum := f.seed(t, "runtime", "acme/other", catalogCheckOtherDef, checkBaseDigest)
	// A record with no version: the shape ParseCatalogRecordState refuses.
	key, err := gcp.CatalogRecordKey(f.slugs["acme/other"], "runtime")
	if err != nil {
		t.Fatal(err)
	}
	f.put(t, key, []byte(`{"entry":"runtime","source_commit":"`+catalogCheckCommit+`"}`))

	lines, err := runCatalogCheck(t)
	if err == nil {
		t.Fatal("the catalog check reported no failure for an unreadable record")
	}
	if got := buildPosts(f.fb); got != 0 {
		t.Fatalf("builds submitted = %d, want 0", got)
	}
	l := lineFor(t, lines, "runtime")
	if l.Decision != imagecheck.CheckFailed || l.Severity != "ERROR" || !strings.Contains(l.Error, "unreadable") ||
		!strings.Contains(l.Error, key) || *l.BuildID != "" {
		t.Fatalf("runtime line = %+v", l)
	}
	// The check state says the same thing, so the next run does not read this
	// entry as one that was never checked.
	if st := f.state(t, "runtime", "acme/other"); st == nil || st.Decision != imagecheck.CheckFailed || !strings.Contains(st.Error, "unreadable") {
		t.Fatalf("runtime check.json = %+v", st)
	}
	_ = gen
	_ = sum
}

// TestCatalogCheckJobStopsAtTheRunBuildCap: one run submits at most the cap's
// builds, whatever the catalog says changed, and an entry the cap stopped is
// recorded as a rebuild left for the next run rather than dropped or failed.
func TestCatalogCheckJobStopsAtTheRunBuildCap(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools", "extra", "runtime"), 2))
	old := "sha256:" + strings.Repeat("9", 64)
	f.seed(t, "tools", "acme/app", catalogCheckAppDef, old)
	f.seed(t, "extra", "acme/app", catalogCheckAppDef, old)
	f.seed(t, "runtime", "acme/other", catalogCheckOtherDef, old)

	lines, err := runCatalogCheck(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildPosts(f.fb); got != 2 {
		t.Fatalf("builds submitted = %d, want 2 (the run's cap)", got)
	}
	if l := lineFor(t, lines, "tools"); l.Decision != imagecheck.Rebuild || *l.BuildID != "b0001" {
		t.Fatalf("tools line = %+v", l)
	}
	if l := lineFor(t, lines, "extra"); l.Decision != imagecheck.Rebuild || *l.BuildID != "b0002" {
		t.Fatalf("extra line = %+v", l)
	}
	l := lineFor(t, lines, "runtime")
	if l.Decision != imagecheck.Rebuild || !slices.Equal(l.Reasons, []string{imagecheck.ReasonBase, imagecheck.ReasonBuildCap}) ||
		l.Severity != "ERROR" || *l.BuildID != "" {
		t.Fatalf("runtime line = %+v", l)
	}
	// The capped entry stays due, with no build of its own recorded: the next
	// run reads it as a rebuild that never happened.
	st := f.state(t, "runtime", "acme/other")
	if st == nil || st.Decision != imagecheck.Rebuild || st.BuildID != "" || st.BuildInputs != nil ||
		!slices.Equal(st.Reasons, []string{imagecheck.ReasonBase, imagecheck.ReasonBuildCap}) {
		t.Fatalf("runtime check.json = %+v", st)
	}
}

// TestCatalogCheckJobCapsAtTheManagedDefault: a spec that names no cap gets
// M5's own default, one build per run — the bound the managed marker's
// max_image_builds carries.
func TestCatalogCheckJobCapsAtTheManagedDefault(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools", "extra"), 0))
	old := "sha256:" + strings.Repeat("9", 64)
	f.seed(t, "tools", "acme/app", catalogCheckAppDef, old)
	f.seed(t, "extra", "acme/app", catalogCheckAppDef, old)

	lines, err := runCatalogCheck(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildPosts(f.fb); got != 1 {
		t.Fatalf("builds submitted = %d, want 1 (the default cap)", got)
	}
	if l := lineFor(t, lines, "extra"); !slices.Contains(l.Reasons, imagecheck.ReasonBuildCap) {
		t.Fatalf("extra line = %+v, want the build-cap note", l)
	}
}

// TestCatalogCheckJobSkipsWhileItsLastRebuildRuns: an entry whose last build of
// these very inputs has not finished is a skip, not a second build.
func TestCatalogCheckJobSkipsWhileItsLastRebuildRuns(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools"), 5))
	f.seed(t, "tools", "acme/app", catalogCheckAppDef, "sha256:"+strings.Repeat("9", 64))

	if _, err := runCatalogCheck(t); err != nil {
		t.Fatal(err)
	}
	if got := buildPosts(f.fb); got != 1 {
		t.Fatalf("builds submitted = %d, want 1", got)
	}
	f.fb.Outcome = "WORKING"

	lines, err := runCatalogCheck(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildPosts(f.fb); got != 1 {
		t.Fatalf("builds submitted = %d, want still 1 (the last one is running)", got)
	}
	l := lineFor(t, lines, "tools")
	if l.Decision != imagecheck.Skip || !slices.Equal(l.Reasons, []string{imagecheck.ReasonBase, imagecheck.ReasonBuildRunning}) || l.Severity != "INFO" {
		t.Fatalf("tools line = %+v", l)
	}
	if st := f.state(t, "tools", "acme/app"); st == nil || st.Decision != imagecheck.Skip || st.BuildID != "b0001" || st.LastBuildStatus != "WORKING" {
		t.Fatalf("tools check.json = %+v", st)
	}
}

// TestCatalogCheckJobRefusesAnEntryAssignedToAnotherRepository: the guard the
// manual path answers to is the scheduled path's too. A spec that assigns an
// entry to a repository its Dockerfile is not in is refused — the build would
// run as the wrong repository's account — and the run builds nothing for it.
func TestCatalogCheckJobRefusesAnEntryAssignedToAnotherRepository(t *testing.T) {
	spec := catalogCheckSpecFor(t, catalogCheckCatalog("tools", "runtime"), 5)
	spec.Repositories = spec.Repositories[:1]
	spec.Repositories[0].Entries = []string{"runtime"}
	f := newCatalogCheckJob(t, spec)

	lines, err := runCatalogCheck(t)
	if err == nil {
		t.Fatal("the catalog check reported no failure for an entry assigned to another repository")
	}
	if got := buildPosts(f.fb); got != 0 {
		t.Fatalf("builds submitted = %d, want 0", got)
	}
	l := lineFor(t, lines, "runtime")
	if l.Decision != imagecheck.CheckFailed || l.Severity != "ERROR" ||
		!strings.Contains(l.Error, "is built from acme/other's Dockerfile") {
		t.Fatalf("runtime line = %+v", l)
	}
	// The entries of the catalog this spec assigns to no repository are named
	// too: nothing refreshes them, and the installed job is behind.
	if l := lineFor(t, lines, "tools"); l.Decision != imagecheck.NotInstalled || l.Severity != "WARNING" || !slices.Equal(l.Reasons, []string{catalogNotInstalledReason}) {
		t.Fatalf("tools line = %+v", l)
	}
}

// TestCatalogCheckJobNeedsItsSpec: a job that cannot read what to check says
// so, and submits nothing.
func TestCatalogCheckJobNeedsItsSpec(t *testing.T) {
	f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools"), 5))
	t.Setenv(catalogCheckSpecEnv, "")

	lines, err := runCatalogCheck(t)
	if err == nil || !strings.Contains(err.Error(), catalogCheckSpecEnv) {
		t.Fatalf("err = %v, want the missing spec", err)
	}
	if got := buildPosts(f.fb); got != 0 {
		t.Fatalf("builds submitted = %d, want 0", got)
	}
	if len(lines) != 1 || lines[0].Decision != imagecheck.CheckFailed || lines[0].Severity != "ERROR" {
		t.Fatalf("lines = %+v", lines)
	}
}
