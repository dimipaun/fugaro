package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The cloud diagnosis's pre-flight (T-TC1, D2401): the launch path's own
// judgement of the spec a diagnosis would launch, read without launching any
// of it.

const preflightRun = "20261116-150000-tc0a"

// preflightSpec is the task a cloud diagnosis would launch: a mode: artifact
// run of the repository the failed command ran in, carrying the recipe the
// tier's own launch names.
func preflightSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: preflightRun, Repo: "acme/app", Ref: "main",
		Workflow: "web", Task: "diagnose a failed run", Mode: recipe.ModeArtifact,
		Recipe: &task.Recipe{Name: "troubleshoot"}}
}

// preflightRepoYAML is the fixture repository's fugaro.yaml on the workflow a
// diagnosis is judged on: checkout: baked, the mode whose job image is judged
// from its build record rather than from the installation's base image.
func preflightRepoYAML() string {
	return strings.Replace(projectJobRepoYAML, "checkout: clone", "checkout: baked", 1)
}

// preflightArtifactRecipe is the recipe text T-TC3 will ship as the catalog's
// troubleshoot entry, spelled as far as this task needs it: a mode: artifact
// recipe with a render step, so the render arm still judges it once T-TC11
// narrows that arm to the recipes that render.
const preflightArtifactRecipe = "version: 1\nname: troubleshoot\nmode: artifact\n" +
	"outputs: { globs: [out/*.md] }\nsteps:\n  - render: {}\n"

// preflightFixture is the installation a diagnosis is judged against: a
// checkout of acme/app on a baked workflow, the base image this release's, and
// the two facts the review-only variant's checks read — the credentials service
// published in the local config, and the installation's own record of that
// service at the release that admits the review job's own caller.
func preflightFixture(t *testing.T, mode string) *launchBudgetFixture {
	t.Helper()
	f := newLaunchBudget(t, mode)
	f.appendConfig(t, "base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+nodeRelease+" }\n")
	isolateState(t)
	writeInstallationServiceImage(t, os.Getenv("XDG_STATE_HOME"), initProject, reviewAdmissionSince)
	projectJobCheckoutYAML(t, preflightRepoYAML())
	return f
}

// preflightSlug is the slug the launcher stores a repository's runs under: the
// fixture's own derivation, so a case that launches the Bitbucket repository
// names the same slug the launch would.
func preflightSlug(repo string) string {
	if repo == proxiedRepo {
		return mustSlug("bitbucket", repo)
	}
	return mustSlug("github", repo)
}

// preflight runs the pre-flight once over the fixture's own environment.
func runPreflight(t *testing.T, f *launchBudgetFixture, spec *task.Spec) error {
	t.Helper()
	env := fileEnv(t, f.cloudFixture)
	slug := preflightSlug(spec.Repo)
	return troubleshootLaunchPath(context.Background(), env, &runOptions{repo: spec.Repo}, slug,
		runstore.Open(env.bucket.Bucket, slug, spec.RunID), spec)
}

// preflightRuns is the fixture's runs bucket, the directory bucketTree walks.
func preflightRuns(f *cloudFixture) string { return filepath.Join(f.dir, "runs") }

// TestTroubleshootPreflightRefusesWhatTheLaunchRefuses is T-TC1's red step: a
// case per gate, each breaking one fact of a healthy installation and asserting
// that the gate's own sentence comes back. Every case fails while the block is
// not called, because the pre-flight then returns nil for a launch the launch
// itself would refuse.
//
// The last five cases are the checks the launch's own block does not carry for
// this spec: the review-only variant's image and the installation's admission
// of it sit inside `if spec.Mode == recipe.ModeReview`, which a mode: artifact
// spec never enters, and the three checks only checkReviewLaunch makes are in
// no block at all.
func TestTroubleshootPreflightRefusesWhatTheLaunchRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		// noService is the installation that has published no credentials
		// service, which is the one fact checkReviewService reads.
		noService bool
		// mode is the installation's own budget mode, which only the two
		// budget gates depend on: they judge a launch that mints, and a launch
		// mints when the mode is on or a policy is published.
		mode  string
		setup func(t *testing.T, f *launchBudgetFixture) *task.Spec
		// want is the gate's own sentence, in fragments: every one of them must
		// appear, so a case asserts the refusal rather than a paraphrase of it.
		want []string
	}{
		{
			name: "a stale job image",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.1.0")
				return nil
			},
			want: []string{"needs a job image whose runner knows recipes", "fugaro " + recipesSince + " or later"},
		},
		{
			name: "a refused credentials cascade",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				projectJobCheckoutYAML(t, strings.Replace(preflightRepoYAML(), "auth: api-key", "auth: cascade", 1))
				return nil
			},
			want: []string{"agent.auth: cascade cannot run here"},
		},
		{
			name: "a workflow with no commands.render",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				spec := preflightSpec()
				spec.Recipe = &task.Recipe{Name: "troubleshoot", YAML: preflightArtifactRecipe}
				return spec
			},
			want: []string{"render step needs commands.render"},
		},
		{
			name: "a job image that predates the seal",
			mode: "enforce",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.8.5")
				return nil
			},
			want: []string{"needs a job image whose runner knows the sealed budget token", "fugaro " + sealSince + " or later"},
		},
		{
			name: "a job image that predates the proxy",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				addProxiedRepo(t, f, "ghcr.io/dimipaun/fugaro-base:0.21.0", "", true)
				publishPolicy(t, f, proxiedPolicy)
				_, spec := proxiedLaunchSpec()
				spec.Mode = recipe.ModeArtifact
				spec.Recipe = &task.Recipe{Name: "troubleshoot"}
				return spec
			},
			want: []string{"needs a job image whose runner knows the Bitbucket proxy", "fugaro " + proxySince + " or later"},
		},
		{
			name: "a full parallel pool",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				for _, id := range []string{"20261116-140000-aaaa", "20261116-140001-bbbb"} {
					exec := f.run.StartWithEnv(gcp.JobName(appSlug, "web"), map[string]string{"FUGARO_RUN": appSlug + "/" + id})
					f.run.SetState(exec, backend.StateRunning)
				}
				return nil
			},
			want: []string{"2 runs active; max_parallel is 2"},
		},
		{
			name: "a budget halt",
			mode: "enforce",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				f.db.Set("config/kill/global", map[string]any{"on": true, "by": "boss@example.com", "reason": "incident"})
				return nil
			},
			want: []string{"kill switch"},
		},
		{
			name: "a policy that does not admit the repository",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				publishPolicy(t, f, `{"version":9,"allow_repos":["github/acme/other"]}`)
				return nil
			},
			want: []string{"the project's policy does not admit github/acme/app"},
		},
		{
			name: "a stale review-variant image",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.19.0")
				return nil
			},
			want: []string{"a review launch needs a job image whose runner knows " + reviewJobKnows, "fugaro " + reviewJobSince + " or later"},
		},
		{
			name: "an installation whose service is older than the admission",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				writeInstallationServiceImage(t, os.Getenv("XDG_STATE_HOME"), initProject, "0.20.3")
				return nil
			},
			want: []string{"only from fugaro " + reviewAdmissionSince + " on"},
		},
		{
			name:      "a service the installation has not published",
			noService: true,
			want:      []string{"credentials_service is not published in this project's config"},
		},
		{
			name: "an exhausted review account",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				f.db.Set(credsMarkPath(config.DefaultClaudeAccount), map[string]any{"state": "exhausted"})
				return nil
			},
			want: []string{"no review is started on it"},
		},
		{
			name: "a binary that predates the admission",
			setup: func(t *testing.T, f *launchBudgetFixture) *task.Spec {
				unadmitReviewJob(t)
				return nil
			},
			want: []string{"this fugaro predates the release in which the credentials service admits the review job's own caller"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			if mode == "" {
				mode = "off"
			}
			f := preflightFixture(t, mode)
			if !tc.noService {
				f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
			}
			spec := preflightSpec()
			if tc.setup != nil {
				if override := tc.setup(t, f); override != nil {
					spec = override
				}
			}
			err := runPreflight(t, f, spec)
			switch {
			case err == nil:
				t.Fatalf("the pre-flight admitted a launch that would be refused: %q", tc.want)
			case ExitCode(err) != ExitUserError:
				t.Fatalf("the refusal's exit code: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the pre-flight's refusal is not the gate's own sentence:\ngot:  %v\nwant: %q", err, want)
				}
			}
		})
	}
}

// TestTroubleshootPreflightLaunchesNothing: the judgement is reads alone. No
// job is asked for, no execution exists, no token is minted and not one object
// of the runs bucket is written or removed — which is what lets the tier be
// reached before a person has spent anything on it.
func TestTroubleshootPreflightLaunchesNothing(t *testing.T) {
	f := preflightFixture(t, "off")
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	before := bucketTree(t, preflightRuns(f.cloudFixture))
	spec := preflightSpec()
	if err := runPreflight(t, f, spec); err != nil {
		t.Fatalf("the pre-flight refused a healthy installation: %v", err)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Fatalf("the pre-flight asked the backend to start %d jobs: %+v", len(calls), calls)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Fatalf("the pre-flight started %d executions: %v", len(execs), execs)
	}
	if signs := f.iam.SignCalls(); len(signs) != 0 {
		t.Fatalf("the pre-flight minted a budget token: %+v", signs)
	}
	if after := bucketTree(t, preflightRuns(f.cloudFixture)); !slices.Equal(before, after) {
		t.Errorf("the pre-flight changed the runs bucket:\nbefore: %v\nafter:  %v", before, after)
	}
}

// TestTroubleshootPreflightIsOfflineForTheImageRead: the review-variant image
// of a checkout: clone workflow is judged from the local config's recorded base
// image — the value doctorPolicyVsJobs compares — and no build record is read
// for it, because a clone workflow never has one. The refusal arrives with the
// bucket holding no record for the workflow at all, which is the reading that
// says the comparison is the local config's.
func TestTroubleshootPreflightIsOfflineForTheImageRead(t *testing.T) {
	f := preflightFixture(t, "off")
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	projectJobCheckoutYAML(t, projectJobRepoYAML)
	removeBucketFile(t, f.cloudFixture, imagecheck.RecordKey(appSlug, "web"))
	setBaseImage(t, "0.19.0")
	spec := preflightSpec()

	err := runPreflight(t, f, spec)
	if err == nil {
		t.Fatal("a review-variant image older than the admission was admitted")
	}
	ref := fileEnv(t, f.cloudFixture).lc.BaseImage(config.BaseKind)
	if !strings.Contains(ref, "0.19.0") {
		t.Fatalf("the fixture's base image is %q, want the one this test moved it to", ref)
	}
	if !strings.Contains(err.Error(), ref) {
		t.Fatalf("the refusal does not name the local config's base image %q: %v", ref, err)
	}
	if !strings.Contains(err.Error(), reviewJobSince) {
		t.Fatalf("the refusal does not name the release the variant's runner is from: %v", err)
	}
}

// removeBucketFile deletes one object from the fixture's file:// runs bucket.
func removeBucketFile(t *testing.T, f *cloudFixture, key string) {
	t.Helper()
	path := filepath.Join(f.dir, "runs", filepath.FromSlash(key))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
