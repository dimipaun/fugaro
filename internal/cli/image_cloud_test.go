package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The Cloud Build clone authenticates with the Bitbucket token, so an
// origin on any host but bitbucket.org is refused before anything is
// submitted: the token would go to that host.
func TestImageBuildCloudPinsTheProviderHost(t *testing.T) {
	for _, origin := range []string{
		"https://bitbucket.example.com/acme/app.git",
		"https://github.com/acme/app.git",
		"https://bitbucket.org:8443/acme/app.git",
	} {
		t.Run(origin, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			testutil.Git(t, ".", "remote", "set-url", "origin", origin)
			_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bitbucket") {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(fb.Requests()) != 0 {
				t.Error("a build cloning from another host reached Cloud Build")
			}
		})
	}
}

// buildPosts are the builds submitted to the fake.
func buildPosts(fb *gcpfake.Build) int {
	n := 0
	for _, r := range fb.Requests() {
		if r.Method == "POST" {
			n++
		}
	}
	return n
}

// TestImageBuildCloudNeedsRegistry: the build pushes to the repository's
// own registry, which fugaro init --repo creates; without it, nothing is
// submitted.
func TestImageBuildCloudNeedsRegistry(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, true)
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro init --repo") || !strings.Contains(err.Error(), "proj-1234") ||
		!strings.Contains(err.Error(), gcp.RegistryRepoID(mustSlug("bitbucket", "acme/app"))) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if buildPosts(fb) != 0 {
		t.Error("a build without its registry reached Cloud Build")
	}
}

// Review fix: a declined (or unconfirmable) billable build must leave no
// trace in the bucket. prepareLayerCopy both writes
// builds/<slug>/project-layer.yaml and validates the base image's release,
// so running it before confirmBuild would publish the copy (and could even
// refuse the build on the base-image gate) before the user ever typed
// anything.
//
// Mutation (run, restore): move the cfg.Layer != nil { ... prepareLayerCopy
// ... } block in runImageBuildCloud back above confirmBuild, and this test
// fails: the copy is written even though "not-aurora" never confirms the
// build.
func TestImageBuildCloudDeclinedConfirmationWritesNoLayerCopy(t *testing.T) {
	fb := gcpfake.NewBuild(t)
	f := newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	publishedLayer(t, f, testProjectLayer)
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const reposLine = "repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	updated := strings.Replace(string(data), reposLine, reposLine+`  acme/other: { provider: github, github_app_id: "12345" }`+"\n", 1)
	if updated == string(data) {
		t.Fatal("the fixture config's repos: line has changed")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	slug := mustSlug("github", "acme/other")
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(slug))
	layerCheckout(t, f, minimalAnchored)
	fakeTerminal(t)
	_, _, err = executeStdin(t, "not-aurora\n", "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	if buildPosts(fb) != 0 {
		t.Fatal("a declined build still reached Cloud Build")
	}
	if bucketText(t, f, config.LayerCopyKey(slug)) != "" {
		t.Fatal("a declined build still wrote the project layer copy")
	}
}

// A Cloud Build submission must refuse, not silently proceed, when the
// project layer's bucket cannot be read: design §8 and decision L16
// classify Cloud Build submission as strict ("fail when the bucket cannot
// be read"), unlike image build --local and image render, which stay
// lenient. cloudBuildCheckout's fixture isn't anchored (no gcp_project:),
// so this adds one to exercise findLayer's bucket read at all.
//
// Mutation (run, restore): change runImageBuildCloud's
// loadCheckoutWorkflow(ctx, o.workflow, lc, layerOptions{}) call back to
// loadCheckout(ctx, o.workflow) (lenient), and this test fails: the build
// is submitted despite the unreadable bucket.
func TestImageBuildCloudRefusesAnUnreadableProjectLayer(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	isolateCache(t)
	publishedLayer(t, f, "") // the default runs bucket name; no layer object needed
	data, err := os.ReadFile("fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	anchored := strings.Replace(string(data), "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	if anchored == string(data) {
		t.Fatal("fugaro.yaml has no project: line to anchor")
	}
	if err := os.WriteFile("fugaro.yaml", []byte(anchored), 0o644); err != nil {
		t.Fatal(err)
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, errors.New("boom")
	}
	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if buildPosts(fb) != 0 {
		t.Error("a build over an unreadable project layer reached Cloud Build")
	}
}

// A Cloud Build submission must resolve the project layer against the same
// project config openCloud already selected from --config (or
// --project/--gcp-project), never a flag-blind re-selection:
// findLayer's bucket comes from that config's own bucket_url
// (layer_resolve.go), so a config picked flag-blind (here, none at all:
// there is deliberately no project config anywhere selectedProjectConfig's
// empty cloudOptions{} could find on its own, only the one --config names)
// could check the wrong bucket, or find no config and so no bucket
// override at all, and let the strict submission proceed as though no
// layer applied instead of refusing.
//
// Mutation (run, restore): change runImageBuildCloud's
// loadCheckoutWorkflow(ctx, o.workflow, lc, layerOptions{}) call to pass
// nil instead of lc, and this test fails: with the selection re-derived
// flag-blind, selectedProjectConfig finds nothing (no $FUGARO_PROJECT, no
// $FUGARO_CONFIG, no config under the isolated XDG directory), so
// findLayer's bucket-override never applies and it falls back to the
// derived default gs://fugaro-runs-proj-1234 — a real bucket this test
// never fakes, so TestMain's own guard against a test reaching a real
// gs:// URL panics (see main_test.go), which is exactly what would have
// been a silent "no layer" in production instead.
func TestImageBuildCloudUsesTheSelectedProjectsLayer(t *testing.T) {
	files := npmFiles()
	files["fugaro.yaml"] = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	checkoutWith(t, files)
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")

	dir := t.TempDir()
	// A clean slate: isolateProjects clears $FUGARO_PROJECT/$FUGARO_CONFIG
	// and points XDG at an empty directory, so selectedProjectConfig's
	// flag-blind cloudOptions{} has nothing to find by name, $FUGARO_*, or
	// "exactly one project config" — only --config below names one at all.
	isolateProjects(t, dir)
	run := gcpfake.NewRun(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	logging := gcpfake.NewLogging(t)
	fb := gcpfake.NewBuild(t)
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(mustSlug("bitbucket", "acme/app")))

	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject)), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	layer := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  git: { provider: bitbucket }\n  agent: { auth: api-key }\n" +
		"profiles:\n  svc:\n    base: web-node\n    commands: { build: sh build.sh, test: sh test.sh }\ndefault_profile: svc\n"
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project-layer.yaml"), []byte(layer), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "selected.yaml")
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\nbucket_url: file://" + runs +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + run.URL + "/, logging: " + logging.URL + "/, cloud_build: " + fb.URL + "/, no_auth: true }\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeBuild(t, "image", "build", "--config", cfgPath, "--base", "b:1")
	if err != nil || !strings.Contains(out, "built ") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// TestImageBuildCloudGitHub: a GitHub repository builds with its App's key
// and ID, as its own build account, into its own registry.
func TestImageBuildCloudGitHub(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	fb := gcpfake.NewBuild(t)
	newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "workflows: [web] }", `workflows: [web], github_app_id: "12345" }`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(appSlug))
	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatal(err)
	}
	last := fb.Last()
	subs, _ := last["substitutions"].(map[string]any)
	if subs["_GITHUB_APP_ID"] != "12345" || subs["_REPO_URL"] != "https://github.com/acme/app.git" ||
		subs["_IMAGE"] != gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(appSlug), appSlug, "app") {
		t.Fatalf("substitutions = %v", subs)
	}
	if _, ok := subs["_GIT_USER"]; ok {
		t.Errorf("a GitHub build sent _GIT_USER: %v", subs)
	}
	secrets, _ := last["availableSecrets"].(map[string]any)
	sm, _ := secrets["secretManager"].([]any)
	first, _ := sm[0].(map[string]any)
	if first["env"] != "GITHUB_APP_KEY" || first["versionName"] != "projects/proj-1234/secrets/"+gcp.SecretID(appSlug, "github-app-key")+"/versions/latest" {
		t.Fatalf("availableSecrets = %v", sm)
	}
	if sa, _ := last["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(appSlug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

// TestImageBuildCloudGitHubNeedsAppID: without the App's ID the build
// could not mint a token, so nothing is submitted.
func TestImageBuildCloudGitHubNeedsAppID(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	fb := gcpfake.NewBuild(t)
	newCloudFixture(t, "cloud_build: "+fb.URL+"/")
	fb.AddRegistry("proj-1234", "us-east5", gcp.RegistryRepoID(appSlug))
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "GitHub App ID") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a build without an App ID reached Cloud Build")
	}
}

// TestImageBuildIgnoresDeprecatedBuildSA: build.service_account still
// loads, with a warning, but the build runs as the repository's own build
// account.
func TestImageBuildIgnoresDeprecatedBuildSA(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	f.appendConfig(t, "build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n")
	_, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "build.service_account is deprecated") {
		t.Errorf("stderr = %q", stderr)
	}
	slug := mustSlug("bitbucket", "acme/app")
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(slug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccount = %q", sa)
	}
}

// TestImageBuildResolvesSpecWithoutOutputs: fugaro image build has no
// installation outputs, so the repository's spec takes the project's name
// from the project config and still resolves.
func TestImageBuildResolvesSpecWithoutOutputs(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	_, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if buildPosts(fb) != 1 || !strings.Contains(stderr, "project: aurora") {
		t.Fatalf("builds %d, stderr %q", buildPosts(fb), stderr)
	}
}

// TestImageBuildCloudRefusesOtherProjectRegistry: images are pushed with
// the project's credentials, so the project config can't be pointed at
// another GCP project with --gcp-project.
func TestImageBuildCloudRefusesOtherProjectRegistry(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	f.appendConfig(t, "registry_host: us-east5-docker.pkg.dev/proj-1234\n")
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1", "--gcp-project", "other-proj")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-proj") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.Requests()) != 0 {
		t.Error("a build for another project's registry reached Cloud Build")
	}
}

// TestImageBuildCloudRegistryForbidden: an operator may submit builds but
// not read the repository's registry. A 403 on the check is not a missing
// registry: the build is submitted, with a warning.
func TestImageBuildCloudRegistryForbidden(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	fb.ForbidRegistries = true
	_, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "could not check") {
		t.Errorf("stderr = %q", stderr)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("builds submitted = %d", buildPosts(fb))
	}
}

// recordFlow wires the build fake's gate and record steps (and the render
// step's cloud outputs) to the real fugaro image render, gate and record,
// against a file bucket at the returned directory, so a simulated build
// writes and reads records as a real one does. The build step's hook
// stands in for the base digest the real build step writes.
func recordFlow(t *testing.T, fb *gcpfake.Build) (bucket string) {
	t.Helper()
	dir := t.TempDir()
	bucket = "file://" + dir
	slug := mustSlug("bitbucket", "acme/app")
	run := func(args ...string) error {
		_, stderr, err := execute(t, args...)
		if err != nil {
			return fmt.Errorf("%v: %s", err, stderr)
		}
		return nil
	}
	fb.Steps = map[string]func(string) error{
		"render": func(ws string) error {
			return run("image", "render", "--workflow", "app", "--cloud-outputs", ws)
		},
		"build": func(ws string) error {
			return os.WriteFile(filepath.Join(ws, "base-digest"), []byte("example.com/base@sha256:"+strings.Repeat("b", 64)+"\n"), 0o644)
		},
		"gate": func(ws string) error {
			return run("image", "gate", "--record", filepath.Join(ws, "record.json"), "--slug", slug, "--bucket", bucket)
		},
		"record": func(ws string) error {
			return run("image", "record", "--in", filepath.Join(ws, "record.json"), "--digest-from", filepath.Join(ws, "image-digest"),
				"--base-digest-from", filepath.Join(ws, "base-digest"), "--image", appImage(), "--base", "b:1", "--build-id", "b0001",
				"--slug", slug, "--bucket", bucket)
		},
	}
	return bucket
}

// appImage is acme/app's image in its own registry.
func appImage() string {
	slug := mustSlug("bitbucket", "acme/app")
	return gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(slug), slug, "app")
}

// readImageRecord reads acme/app's app record from the file bucket, or nil.
func readImageRecord(t *testing.T, bucket string) *imagecheck.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(strings.TrimPrefix(bucket, "file://"), imagecheck.RecordKey(mustSlug("bitbucket", "acme/app"), "app")))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	r, err := imagecheck.ParseRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// writeImageRecord puts r in the file bucket as acme/app's app record.
func writeImageRecord(t *testing.T, bucket string, r imagecheck.Record) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(strings.TrimPrefix(bucket, "file://"), imagecheck.RecordKey(mustSlug("bitbucket", "acme/app"), "app"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// headCommit is the checkout's HEAD and its committer time.
func headCommit(t *testing.T) (string, time.Time) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, testutil.Git(t, ".", "show", "-s", "--format=%cI", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	return testutil.Git(t, ".", "rev-parse", "HEAD"), at
}

// TestUntagFailureIsOnlyAWarning: when the candidate tag's removal is
// refused (a build account without the tag mover role, say), the build
// still succeeds, latest is the candidate's digest, the record is written,
// and the log warns.
func TestUntagFailureIsOnlyAWarning(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	fb.UntagDenied = true
	out, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	latest := fb.Tag(appImage(), "latest")
	const id = "b0001"
	if latest == "" || !strings.Contains(out, "@"+latest+" (Cloud Build build "+id+")") {
		t.Fatalf("output %q, latest %q", out, latest)
	}
	if fb.Tag(appImage(), "candidate-"+id) != latest {
		t.Error("the candidate tag was removed although the delete was refused")
	}
	rec := readImageRecord(t, bucket)
	commit, _ := headCommit(t)
	if rec == nil || rec.ImageDigest != latest || rec.Image != appImage() || rec.SourceCommit != commit || rec.BuildID != "b0001" ||
		rec.BaseDigest != "sha256:"+strings.Repeat("b", 64) || rec.BaseRef != "b:1" || rec.Adopted {
		t.Fatalf("record = %+v", rec)
	}
	if !slices.ContainsFunc(fb.Log(id), func(l string) bool { return strings.Contains(l, "warning: could not remove candidate-"+id) }) {
		t.Errorf("log = %v", fb.Log(id))
	}
}

// TestFailedSmokeLeavesLatest: a candidate that fails its smoke test is
// never promoted or recorded.
func TestFailedSmokeLeavesLatest(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	old := "sha256:" + strings.Repeat("0", 63) + "a"
	fb.SetTag(appImage(), "latest", old)
	fb.FailStep = "smoke"
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if got := fb.Tag(appImage(), "latest"); got != old {
		t.Errorf("latest = %q, want the old %q", got, old)
	}
	if rec := readImageRecord(t, bucket); rec != nil {
		t.Errorf("a failed smoke wrote a record: %+v", rec)
	}
}

// TestGateSkipsWhenRecordNewer: a record of a later commit means a newer
// build already promoted, so this one neither retags latest nor records.
func TestGateSkipsWhenRecordNewer(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	_, at := headCommit(t)
	newer := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "app", SourceCommit: "f00d", SourceCommitTime: at.Add(time.Hour),
		BuiltAt: at.Add(2 * time.Hour), ImageDigest: "sha256:" + strings.Repeat("c", 64)}
	writeImageRecord(t, bucket, newer)
	fb.SetTag(appImage(), "latest", newer.ImageDigest)
	out, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if got := fb.Tag(appImage(), "latest"); got != newer.ImageDigest {
		t.Errorf("latest = %q, want the newer build's", got)
	}
	for _, l := range fb.Log("b0001") {
		if strings.Contains(l, "tags add") {
			t.Errorf("a superseded build retagged: %s", l)
		}
	}
	if rec := readImageRecord(t, bucket); rec == nil || rec.SourceCommit != "f00d" {
		t.Errorf("record = %+v", rec)
	}
	if !strings.Contains(out, "newer") || !strings.Contains(out, "latest") {
		t.Errorf("output = %q", out)
	}
}

// TestGateSameCommitLaterBuiltAt: for one commit, the later build wins.
func TestGateSameCommitLaterBuiltAt(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	commit, at := headCommit(t)
	later := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "app", SourceCommit: commit, SourceCommitTime: at,
		BuiltAt: time.Now().Add(time.Hour).UTC(), ImageDigest: "sha256:" + strings.Repeat("d", 64)}
	writeImageRecord(t, bucket, later)
	if _, stderr, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if fb.Tag(appImage(), "latest") != "" || readImageRecord(t, bucket).ImageDigest != later.ImageDigest {
		t.Errorf("latest = %q, record = %+v", fb.Tag(appImage(), "latest"), readImageRecord(t, bucket))
	}

	// The same commit built earlier is replaced.
	earlier := later
	earlier.BuiltAt = at.Add(-time.Hour)
	writeImageRecord(t, bucket, earlier)
	if _, stderr, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if rec := readImageRecord(t, bucket); rec.ImageDigest == earlier.ImageDigest || rec.ImageDigest != fb.Tag(appImage(), "latest") {
		t.Errorf("record = %+v, latest %q", rec, fb.Tag(appImage(), "latest"))
	}
}

// TestGatePromotesWhenRecordOlder: a record of an earlier commit is
// replaced, and latest moves to this build's digest.
func TestGatePromotesWhenRecordOlder(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	_, at := headCommit(t)
	older := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "app", SourceCommit: "0ld", SourceCommitTime: at.Add(-time.Hour),
		BuiltAt: time.Now().Add(time.Hour).UTC(), ImageDigest: "sha256:" + strings.Repeat("e", 64)}
	writeImageRecord(t, bucket, older)
	fb.SetTag(appImage(), "latest", older.ImageDigest)
	out, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	latest := fb.Tag(appImage(), "latest")
	commit, _ := headCommit(t)
	if rec := readImageRecord(t, bucket); latest == older.ImageDigest || rec.ImageDigest != latest || rec.SourceCommit != commit {
		t.Fatalf("latest %q, record %+v", latest, rec)
	}
	if !strings.Contains(out, "built "+appImage()+"@"+latest) {
		t.Errorf("output = %q", out)
	}
}

// TestRecordGenerationMatched: a record written between the gate's read
// and the record step's write is never overwritten; the build fails.
func TestRecordGenerationMatched(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	bucket := recordFlow(t, fb)
	_, at := headCommit(t)
	older := imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: "app", SourceCommit: "0ld", SourceCommitTime: at.Add(-time.Hour), BuiltAt: at}
	writeImageRecord(t, bucket, older)
	concurrent := older
	concurrent.SourceCommit, concurrent.SourceCommitTime = "c0ncurrent", at.Add(time.Hour)
	fb.Steps["promote"] = func(string) error { writeImageRecord(t, bucket, concurrent); return nil }
	_, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if rec := readImageRecord(t, bucket); rec.SourceCommit != "c0ncurrent" {
		t.Errorf("the concurrent record was overwritten: %+v", rec)
	}
	if !slices.ContainsFunc(fb.Log("b0001"), func(l string) bool { return strings.HasPrefix(l, "record:") && strings.Contains(l, "changed") }) {
		t.Errorf("log = %v", fb.Log("b0001"))
	}

	// With no record at gate time, a record created meanwhile is not
	// overwritten either.
	fb2, _ := cloudBuildCheckout(t, false)
	bucket = recordFlow(t, fb2)
	fb2.Steps["promote"] = func(string) error { writeImageRecord(t, bucket, concurrent); return nil }
	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if rec := readImageRecord(t, bucket); rec.SourceCommit != "c0ncurrent" {
		t.Errorf("the concurrent record was overwritten: %+v", rec)
	}
}

// TestImageRecordNeedsGate: record refuses to write without the gate's
// read, and does nothing when the gate found a newer record.
func TestImageRecordNeedsGate(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	ws, bucket := t.TempDir(), "file://"+t.TempDir()
	if _, stderr, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", ws); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	testutil.WriteFiles(t, ws, map[string]string{"image-digest": "sha256:" + strings.Repeat("1", 64) + "\n", "base-digest": "b@sha256:" + strings.Repeat("2", 64) + "\n"})
	args := []string{"image", "record", "--in", filepath.Join(ws, "record.json"), "--digest-from", filepath.Join(ws, "image-digest"),
		"--base-digest-from", filepath.Join(ws, "base-digest"), "--image", "img", "--base", "b:1", "--build-id", "b1", "--slug", "acme-app-0123456789abcdef", "--bucket", bucket}
	if _, _, err := execute(t, args...); err == nil || !strings.Contains(err.Error(), "gate") {
		t.Fatalf("record without the gate: %v", err)
	}
	testutil.WriteFiles(t, ws, map[string]string{"superseded": ""})
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("record when superseded: %v", err)
	}
	if entries, _ := os.ReadDir(strings.TrimPrefix(bucket, "file://")); len(entries) != 0 {
		t.Errorf("a superseded record step wrote %v", entries)
	}
	// A digest that isn't one is refused.
	if err := os.Remove(filepath.Join(ws, "superseded")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "image", "gate", "--record", filepath.Join(ws, "record.json"), "--slug", "acme-app-0123456789abcdef", "--bucket", bucket); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, ws, map[string]string{"image-digest": "latest\n"})
	if _, _, err := execute(t, args...); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("record with a bad digest: %v", err)
	}
}

// TestImageRenderCloudOutputs: the render step writes the smoke's
// selftest spec and the record's source side next to the Dockerfile.
func TestImageRenderCloudOutputs(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	ws := t.TempDir()
	out, stderr, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", ws)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(out, "FROM ${FUGARO_BASE}") {
		t.Errorf("the Dockerfile is not on stdout: %q", out)
	}
	commit, at := headCommit(t)
	var spec image.SelftestSpec
	data, err := os.ReadFile(filepath.Join(ws, "selftest.json"))
	if err != nil || json.Unmarshal(data, &spec) != nil {
		t.Fatalf("selftest.json = %s, %v", data, err)
	}
	if want := (image.SelftestSpec{Base: "web-node", RepoDir: "/work/repo", Commit: commit, Origin: "https://github.com/acme/app.git",
		CheckInit: true, CheckHardening: true, SkipVerify: true}); !reflect.DeepEqual(spec, want) {
		t.Errorf("selftest.json = %+v\nwant %+v", spec, want)
	}
	data, err = os.ReadFile(filepath.Join(ws, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	lockID := testutil.Git(t, ".", "rev-parse", "HEAD:package-lock.json")
	if rec.Repo != "acme/app" || rec.Workflow != "app" || rec.SourceCommit != commit || !rec.SourceCommitTime.Equal(at) || rec.BaseBranch != "main" ||
		!maps.Equal(rec.KeyFiles, map[string]string{"package-lock.json": lockID}) || len(rec.ImageConfigHash) != 64 ||
		rec.FugaroVersion != Version || rec.TemplateSalt != gcp.TemplateSalt(Version) || time.Since(rec.BuiltAt) > time.Minute || rec.Version != 1 ||
		rec.Image != "" || rec.ImageDigest != "" || rec.Adopted {
		t.Errorf("record.json = %+v", rec)
	}
	for name, want := range map[string]string{"source-commit": commit + "\n", "built-at": rec.BuiltAt.Format(time.RFC3339) + "\n"} {
		if got, _ := os.ReadFile(filepath.Join(ws, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestImageBuildCloudNoSmoke: --no-smoke leaves out the smoke step.
func TestImageBuildCloudNoSmoke(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)
	if _, _, err := executeBuild(t, "image", "build", "--no-smoke", "--no-wait", "--base", "b:1"); err != nil {
		t.Fatal(err)
	}
	steps, _ := fb.Last()["steps"].([]any)
	for _, s := range steps {
		if st, _ := s.(map[string]any); st["id"] == "smoke" {
			t.Error("--no-smoke sent the smoke step")
		}
	}
	if len(steps) != 9 {
		t.Errorf("%d steps", len(steps))
	}
	subs, _ := fb.Last()["substitutions"].(map[string]any)
	if subs["_BUCKET"] != "gs://unused-bucket" || subs["_SLUG"] != mustSlug("bitbucket", "acme/app") {
		t.Errorf("substitutions = %v", subs)
	}
}

// TestImageRenderCloudOutputsUseGitBlobIDs: the record's key-file IDs are
// git's own, which the check reads from the branch's tree, even when the
// checkout's bytes differ from the blob's (an eol=crlf attribute here).
func TestImageRenderCloudOutputsUseGitBlobIDs(t *testing.T) {
	files := npmFiles()
	files[".gitattributes"] = "package-lock.json text eol=crlf\n"
	files["package-lock.json"] = "{\n  \"name\": \"app\",\n  \"lockfileVersion\": 3,\n  \"requires\": true,\n  \"packages\": {\"\": {\"name\": \"app\"}}\n}\n"
	checkoutWith(t, files)
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	if data, _ := os.ReadFile("package-lock.json"); !strings.Contains(string(data), "\r\n") {
		t.Fatalf("the checkout did not apply the attribute: %q", data)
	}
	ws := t.TempDir()
	if _, stderr, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", ws); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	data, err := os.ReadFile(filepath.Join(ws, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if want := testutil.Git(t, ".", "rev-parse", "HEAD:package-lock.json"); rec.KeyFiles["package-lock.json"] != want {
		t.Errorf("key file ID = %s, want git's %s", rec.KeyFiles["package-lock.json"], want)
	}
}
