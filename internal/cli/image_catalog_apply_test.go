package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is AP4's own claim about a management command that runs its
// chain: `fugaro image add` alone leaves the project's jobs running the image
// it built, and `--no-apply` is the one way to save the entry without it. The
// three arms of the agent refusal are the mutation check the plan asks for:
// delete imageAddAgentRefusal and they write the entry instead (nothing else
// in this world refuses an --inline entry to a coding agent — that is the
// point of D1573).

// entryDockerfile writes an entry's definition somewhere of the test's own, as
// the person `--inline` reads it: the fixture's own definition is a constant, so
// a test can tell this copy from any other.
func entryDockerfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entry.Dockerfile")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// configOnDisk is the project config as the command left it.
func configOnDisk(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// imageAddChainWorld is the machine AP4's chain runs on: an applied
// installation of aurora (the repository root plans and applies, the policy
// stage publishes into the budget database the build's lease is priced from,
// update_switch_test.go's world), with what a catalog build needs beside it —
// the Cloud Build fake the entry is submitted to, the runs bucket it records
// into, and a checkout of the listed acme/sandbox, so the redeploy the chain
// owes has a repository to redeploy (D1574).
func imageAddChainWorld(t *testing.T) (*initRig, *gcpfake.Build) {
	t.Helper()
	// applyWorld is the installation every chain runs on: an applied
	// installation, the rules generation this binary deploys, and a policy
	// node this machine's config may publish over (a publish that loosens the
	// node is the owner's typed confirmation, which a chain has no terminal
	// for).
	r, db := applyWorld(t)
	// An applied installation whose repository plan has nothing to change:
	// update --repo of the listed acme/sandbox runs to its end (repoReady).
	r.repoReady(t)
	// The base image is a release that reads the policy's image_catalog key:
	// the entry this test adds is judged against it (imageCatalogSkew), and
	// repoReady's own pin is a release from before the key existed.
	cfg := strings.Replace(configOnDisk(t), "fugaro-base:0.4.0", "fugaro-base:0.20.0", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// The lease a build takes before it is submitted (D1543) is priced against
	// this database's caps, and the price lookup is this package's own book.
	// The caps are written as the whole of config/limits, which is the node the
	// deployed rules' marker lives under (budget.PathLimits,
	// projectpolicy.PathRulesMarker), so the marker applyWorld set is written
	// again here: the policy stage refuses to publish under rules it cannot
	// read a marker for.
	buildBudgetCaps(db)
	stubBuildPricer(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)

	fb := gcpfake.NewBuild(t)
	fb.AddRegistry(initProject, "us-east5", gcp.SharedRegistryRepoID())
	// The Cloud Build endpoint is one of the endpoints the rig's config already
	// names, so it goes into that block rather than beside it (a top-level
	// cloud_build is not a key this config has).
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "no_auth: true }") {
		t.Fatalf("the rig's endpoints block has changed shape:\n%s", data)
	}
	cfg = strings.Replace(string(data), "no_auth: true }", "cloud_build: "+fb.URL+"/, no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// bucket_url names a bucket other than the runs bucket, and one on GCS:
	// that is what makes the record bucket (the runs bucket) a bucket this
	// machine reads through openRecordBucket rather than through the bucket
	// the config itself points at (recordReadURL, cloud.go).
	bucketURL := "gs://fugaro-layer-" + initProject
	r.appendConfig(t, "bucket_url: "+bucketURL+"\n")
	// And the installation's credentials service, which is where a catalog
	// entry's build reads the git credential it clones with (IS8, D3330): the
	// request names no secret of its own any more, so a submit on a machine
	// whose config records no address is refused rather than billed for a
	// build that would fail at its credential step.
	r.appendConfig(t, "credentials_service: { url: "+catalogBuildServiceURL+" }\n")
	// A gs:// bucket_url is opened by gocloud, which wants credentials it never
	// uses, and the project-name check is answered from its cache as a check
	// made a moment ago is (setBucketURL, image_test.go).
	adc := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	if err := localcfg.SaveNameCheck(os.Getenv, initProjectName, localcfg.NameCheck{
		GCPProject: initProject, RunsBucket: bucketURL, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The runs bucket those records live in, served by a GCS fake: what a
	// submission pins is the copy's object generation, which a file:// bucket
	// has none of (catalogBuildCheckoutFor).
	gcs := gcpfake.NewGCS(t)
	prev := openRecordBucket
	openRecordBucket = func(_ context.Context, u string) (*blobx.Bucket, error) {
		if u != "gs://"+initRunsBucket {
			t.Errorf("the record bucket was opened as %s, want the runs bucket", u)
		}
		return gcs.Bucket(t, initRunsBucket), nil
	}
	t.Cleanup(func() { openRecordBucket = prev })

	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", "")))
	return r, fb
}

// repoFact is one key of the repository root's tfvars: the spec the next apply
// of that root deploys, which is where the image a job runs is fixed (F7).
func repoFact(t *testing.T, slug, key string) json.RawMessage {
	t.Helper()
	wd, err := infra.RepoWorkdir(os.Getenv, initProject, slug)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "gcp", "roots", "repo", infra.VarsFile))
	if err != nil {
		t.Fatalf("the repository root wrote no tfvars, so its redeploy did not run: %v", err)
	}
	var doc struct {
		Repo map[string]json.RawMessage `json:"repo"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.Repo[key]
	if !ok {
		t.Fatalf("the repository root's spec has no repo.%s: %s", key, b)
	}
	return raw
}

// deployedImage is the image the repository root's own tfvars gives the
// workflow job: what the redeploy pinned, read where the plan reads it.
func deployedImage(t *testing.T, workflow string) string {
	t.Helper()
	var workflows map[string]struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(repoFact(t, mustSlug("bitbucket", "acme/sandbox"), "workflows"), &workflows); err != nil {
		t.Fatal(err)
	}
	ws, ok := workflows[workflow]
	if !ok {
		t.Fatalf("the repository root's spec has no workflow %q, only %v", workflow, workflows)
	}
	return ws.Image
}

// deployedDigest is the digest the deployed job's own reference resolves to:
// a job's image is a tag and not a digest (D1152), so what says which build a
// job will run is what the tag it carries points at.
func deployedDigest(fb *gcpfake.Build, deployed string) string {
	i := strings.LastIndex(deployed, ":")
	if i < 0 {
		return ""
	}
	return fb.Tag(deployed[:i], deployed[i+1:])
}

// TestImageAddAloneMakesTheImageUsable is AP4's own claim: `fugaro image add`
// is one command that makes the change it writes live. The entry is built, the
// checkout's repository is redeployed onto the image that build promoted, and
// the catalog is published — so the job that root deploys runs the image this
// one command built, and nothing is left owed afterwards (check 390).
//
// The digest is not asked of the command, which never names one: the built
// image's tag is what the Cloud Build fake promoted, and the workflow's own
// image is that tag (a job's image is a tag, catalog-<entry>:latest — D1152),
// so the two are compared through the digest the fake's registry resolves each
// of them to.
func TestImageAddAloneMakesTheImageUsable(t *testing.T) {
	r, fb := imageAddChainWorld(t)
	df := entryDockerfile(t, catalogBuildInline)

	out, errOut, err := executeStdin(t, "", "image", "add", "base", "--inline", df, "--default")
	if err != nil {
		t.Fatalf("image add alone did not finish its own chain: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if got := out + errOut; strings.Contains(got, "still owed") || strings.Contains(got, "saved, not live yet") {
		t.Errorf("the entry is reported as pending although every step ran:\n%s", got)
	}
	if !strings.Contains(out, "Image base added and now available to your jobs.") {
		t.Errorf("the answer does not say the entry is live:\n%s", out)
	}

	// The one build this chain submits is the entry's, and the image it
	// promoted is the image the redeployed repository names: the deploy's own
	// reader of a catalog reference names the entry back out of it (D1152), so
	// the job the root deploys is running this entry's image.
	if n := buildPosts(fb); n != 1 {
		t.Errorf("image add submitted %d builds, want the one entry it added", n)
	}
	image := catalogBuildImage(t, "base")
	digest := fb.Tag(image, "latest")
	if digest == "" {
		t.Fatalf("no build of %s was promoted, so the chain never built the entry", image)
	}
	deployed := deployedImage(t, "app")
	if entry, ok := gcp.CatalogImageEntry(deployed); !ok || entry != "base" {
		t.Errorf("the deployed job's image is %q, which is not the catalog image of base: the redeploy did not follow the entry this command added", deployed)
	} else if got := deployedDigest(fb, deployed); got != digest {
		t.Errorf("the deployed job's image resolves to %q, want the digest this build promoted (%s): the job would run an image this command did not build", got, digest)
	}
	// And the redeploy is a repository root's own plan and not a document
	// written for somebody else to apply (D1574).
	if len(r.ran(t, "plan")) == 0 {
		t.Errorf("the chain ran no terraform plan: the repository was not redeployed")
	}

	// And nothing is owed: the change went live in the one command.
	out, _, err = executeStdin(t, "", "apply", "--status", "--json")
	if err != nil {
		t.Fatalf("apply --status: %v\n%s", err, out)
	}
	doc := applyDocOf(t, out)
	if len(doc.Pending) != 0 || len(doc.Applied) != 0 {
		t.Errorf("image add left %v owed and applied %v, want the change live", applySteps(doc.Pending), doc.Applied)
	}
}

// TestImageAddNoApplyLeavesPending: `--no-apply` saves the change and runs none
// of its steps (D1565). What is written is the entry alone; the record beside
// the project config holds the steps that make it live — the catalog build,
// the redeploy and the publication — and the answer says so in D1562's words
// and names the one command that finishes the job.
func TestImageAddNoApplyLeavesPending(t *testing.T) {
	catalogFixture(t)
	df := entryDockerfile(t, catalogBuildInline)

	out, errOut, err := executeStdin(t, "", "image", "add", "extra", "--inline", df, "--no-apply")
	if err != nil {
		t.Fatalf("image add --no-apply: %v\n%s", err, out+errOut)
	}
	if got := configOnDisk(t); !strings.Contains(got, "name: extra") {
		t.Errorf("the entry was not written:\n%s", got)
	}
	if strings.Contains(out, "now available to your jobs") {
		t.Errorf("a run that applied nothing says the entry is live:\n%s", out)
	}
	for _, want := range []string{"saved, not live yet", "fugaro apply"} {
		if !strings.Contains(out+errOut, want) {
			t.Errorf("the answer lacks %q:\n%s", want, out+errOut)
		}
	}
	steps, ok := apply.Steps(apply.Change{Kind: apply.Resource, Key: apply.ResourceImage})
	if !ok {
		t.Fatal("the table declares no steps for an image change")
	}
	got, want := record(t).Names(), slices.Clone(steps)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the record owes %v, want the change's steps %v", got, want)
	}
}

// TestImageAddRefusesExecutableContentForAnAgent is D1573's red step: the entry
// an agent's session may not write is the one whose content it also writes —
// `--inline` (a Dockerfile out of the session's own hands), `--image` (a
// reference nothing here built) and `--default` (the entry every job that
// names no needs runs) are executable content the project's jobs run, so each
// is a person's own entry. The refusal is the command's own and not the
// managed gate's: this project is not managed at all, no gate is read, and
// nothing is written either way.
//
// `--dockerfile` alone is deliberately not among them: it names a Dockerfile in
// one of the project's own repositories, committed and reviewed there. `--branch`
// is, and it is the arm that closes the route --dockerfile would otherwise leave
// open: with it the entry reads that Dockerfile from a branch no review has
// vouched for, which the session may have pushed itself, so the build a person's
// next `fugaro apply` runs would bake content no person wrote.
func TestImageAddRefusesExecutableContentForAnAgent(t *testing.T) {
	df := entryDockerfile(t, catalogBuildInline)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"an inline definition", []string{"image", "add", "extra", "--inline", df}},
		{"the default entry", []string{"image", "add", "extra", "--default"}},
		{"a prebuilt image", []string{"image", "add", "extra", "--image", "docker.io/library/node:22"}},
		{"a Dockerfile read from a branch", []string{"image", "add", "extra", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--branch", "release-1.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalogFixture(t)
			before := configOnDisk(t)
			t.Setenv("CLAUDECODE", "1") // a coding agent's session

			out, errOut, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("an agent's session wrote %s:\n%s", strings.Join(tc.args, " "), out)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			got := out + errOut + ErrorText(err)
			for _, want := range []string{imageAddAgentClause, initflow.AgentRefusal("CLAUDECODE")} {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal lacks %q:\n%s", want, got)
				}
			}
			if after := configOnDisk(t); after != before {
				t.Errorf("the project config changed under a refusal:\n%s", after)
			}
			if p := record(t); len(p.Steps) != 0 {
				t.Errorf("the refusal queued %v", p.Names())
			}
		})
	}
}

// TestImageRmOwesNoBuild: a removal takes an entry out of the catalog and
// changes nothing an image is built from — the entry's image is built, pushed
// and left in the registry, and the catalog the removal leaves is the one the
// remaining entries make up. The removal used to owe the catalog's own steps,
// which include the build (internal/apply's catalogSteps), so `fugaro image rm`
// rebuilt every entry of the catalog and failed the whole removal on the first
// entry whose build failed — over a change that never touched it (check 390).
//
// What it owes is the two stages that make it live: the checkout's own
// redeploy without the entry (D1574) and the publication of the catalog the
// removal leaves.
func TestImageRmOwesNoBuild(t *testing.T) {
	r, fb := imageAddChainWorld(t)
	// The first addition anchors the checkout (gcp_project in its fugaro.yaml),
	// and every command after it resolves the project layer that anchor makes
	// apply: a bucket this world names as gs://, which this package's TestMain
	// refuses to open for real. The layer is empty here, the state every fixture
	// in this package is in (managementWorldImageRm's own override).
	oldOpen := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = oldOpen })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+t.TempDir())
	}
	df := entryDockerfile(t, catalogBuildInline)
	if out, errOut, err := executeStdin(t, "", "image", "add", "base", "--inline", df, "--default"); err != nil {
		t.Fatalf("image add base: %v\n%s", err, out+errOut)
	}
	// The redeploy refuses a working tree that is not the default branch's until
	// the anchor is committed (enrolLanded), which is what a person running the
	// two commands a moment apart does.
	enrolLanded(t)
	if out, errOut, err := executeStdin(t, "", "image", "add", "tools", "--inline", df); err != nil {
		t.Fatalf("image add tools: %v\n%s", err, out+errOut)
	}
	steps, ok := apply.Steps(apply.Change{Kind: apply.Resource, Key: apply.ResourceImageRemoval})
	if !ok {
		t.Fatal("the table declares no steps for a catalog removal")
	}
	if !slices.Equal(steps, []string{initflow.Repository, initflow.Policy}) {
		t.Fatalf("the removal's steps = %v, want the redeploy and the publication and no build", steps)
	}
	plans, builds := len(r.ran(t, "plan")), buildPosts(fb)
	out, errOut, err := executeStdin(t, "", "image", "rm", "tools")
	if err != nil {
		t.Fatalf("image rm: %v\n%s", err, out+errOut)
	}
	if got := buildPosts(fb); got != builds {
		t.Errorf("the removal submitted %d builds, want none: an entry taken out of the catalog is nothing to build", got-builds)
	}
	if len(r.ran(t, "plan")) == plans {
		t.Errorf("the removal ran no terraform plan: the checkout was not redeployed without the entry")
	}
	if got := configOnDisk(t); strings.Contains(got, "name: tools") {
		t.Errorf("the entry is still in this machine's config:\n%s", got)
	}
	out, _, err = executeStdin(t, "", "apply", "--status", "--json")
	if err != nil {
		t.Fatalf("apply --status: %v\n%s", err, out)
	}
	if doc := applyDocOf(t, out); len(doc.Pending) != 0 {
		t.Errorf("the removal left %v owed, want the change live", applySteps(doc.Pending))
	}
}
