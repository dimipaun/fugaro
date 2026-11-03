package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// baseBranchCheckout makes the working directory a checkout of repo with the
// given fugaro.yaml, and gives origin the remote-tracking refs of main,
// develop and master. A branch of "" leaves HEAD detached: that is the
// checkout whose own branch never decides the ref (checkoutBranchOnOrigin),
// so the launch's ref is the base branch itself and the test reads the
// layers' answer rather than the checkout's.
func baseBranchCheckout(t *testing.T, repo, yaml, branch string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:"+repo+".git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml, "README.md": "code\n"})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "init")
	for _, b := range []string{"main", "develop", "master"} {
		testutil.Git(t, dir, "update-ref", "refs/remotes/origin/"+b, "HEAD")
	}
	switch branch {
	case "":
		testutil.Git(t, dir, "checkout", "-q", "--detach", "HEAD")
	case "main":
	default:
		testutil.Git(t, dir, "checkout", "-q", "-b", branch)
	}
	t.Chdir(dir)
}

// baseBranchYAML is a fugaro.yaml whose git: block is exactly gitBlock.
func baseBranchYAML(gitBlock string) string {
	return "version: 1\nproject: aurora\n" + gitBlock + "workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
}

// noBaseBranchYAML writes no git.base_branch at all: the file the project's
// own default applies to.
const noBaseBranchYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// publishBaseBranch writes the policy node a launcher reads the project's
// default base branch from, with the rules marker that makes the node visible
// to it (projectpolicy.Read).
func publishBaseBranch(t *testing.T, db *gcpfake.RTDB, branch string) {
	t.Helper()
	node := map[string]any{"version": projectpolicy.Version, "allow_repos": []string{"github/acme/other"}, "budget": map[string]any{"mode": "off"}}
	if branch != "" {
		node["base_branch"] = branch
	}
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), node)
}

// addOtherRepo enrolls acme/other in the fixture's local config and deploys
// its job. It is the repository the launches below use: the fixture's own
// acme/app entry sets base_branch: main, which would answer the repository
// layer for every one of them.
func addOtherRepo(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.appendConfig(t, "  acme/other: { provider: github, workflows: [web] }\n")
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "web"), "2", "4Gi")
	// Its job image is built from a base that reads the project's base branch,
	// which is the state every other gate here is judged against too (the
	// seal's, when the launch mints a budget token).
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "web", "ghcr.io/dimipaun/fugaro-base:"+baseBranchSince)
}

// TestLaunchBaseBranchReadsThePublishedPolicy is layer 3's other half: a
// launcher whose own config sets no base_branch reads the project's default
// from the policy node the owner published, each case on its own installation
// (the read is memoized per command), and the installation config's own value
// wins over the published one when both are set.
func TestLaunchBaseBranchReadsThePublishedPolicy(t *testing.T) {
	cases := []struct {
		name, local, published, want, wantSource string
	}{
		{"the published node", "", "master", "master", config.BaseBranchSourceProject},
		// Both halves of layer 3 print the same source: they are one layer, the
		// installation's own value winning over the one published for it.
		{"the installation config wins", "develop", "master", "develop", config.BaseBranchSourceProject},
		{"a node without the key", "", "", "main", config.SourceDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			db := gcpfake.NewRTDB(t)
			addOtherRepo(t, f)
			f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+db.URL+", token_signer: "+testSigner+" }\n")
			if tc.local != "" {
				f.appendConfig(t, "base_branch: "+tc.local+"\n")
			}
			publishBaseBranch(t, db, tc.published)
			baseBranchCheckout(t, "acme/other", noBaseBranchYAML, "main")
			b, source := launchBaseBranch(t.Context(), fileEnv(t, f), "acme/other")
			if b != tc.want || source != tc.wantSource {
				t.Fatalf("base/source = %q/%q, want %q/%q", b, source, tc.want, tc.wantSource)
			}
		})
	}
	t.Run("a read that fails leaves the layers above", func(t *testing.T) {
		f := newCloudFixture(t)
		addOtherRepo(t, f)
		// A database nothing answers on: the project's default is unreadable,
		// and the launch still has a base branch — the two layers above it, and
		// the default — because a policy read is never worth refusing a launch
		// over (the launch's own policy gate reports the read when it matters).
		f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: http://127.0.0.1:1/, token_signer: "+testSigner+" }\n")
		baseBranchCheckout(t, "acme/other", noBaseBranchYAML, "main")
		b, source := launchBaseBranch(t.Context(), fileEnv(t, f), "acme/other")
		if b != "main" || source != config.SourceDefault {
			t.Fatalf("base/source = %q/%q, want main/default", b, source)
		}
	})
	t.Run("the two layers above skip the read", func(t *testing.T) {
		f := newCloudFixture(t)
		addOtherRepo(t, f)
		f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: http://127.0.0.1:1/, token_signer: "+testSigner+" }\n")
		baseBranchCheckout(t, "acme/other", baseBranchYAML("git: { provider: github, base_branch: develop }\n"), "main")
		b, source := launchBaseBranch(t.Context(), fileEnv(t, f), "acme/other")
		if b != "develop" || source != config.BaseBranchSourceFile {
			t.Fatalf("base/source = %q/%q, want develop/"+config.BaseBranchSourceFile, b, source)
		}
	})
}

// TestBaseBranchPrecedenceInTheLauncher pins the order every layer wins in
// (config.EffectiveBaseBranch): the checkout's committed fugaro.yaml when it
// sets git.base_branch, else the local config's repos.<r>.base_branch, else
// the project's base_branch, else main.
//
// The first case is the ruling's reversal: before it, the launcher put the
// local config's repos.<r>.base_branch above the checkout's file, so an
// installation's entry overrode the branch a repository's own reviewed file
// names.
func TestBaseBranchPrecedenceInTheLauncher(t *testing.T) {
	t.Run("the file beats the installation config", func(t *testing.T) {
		f := newCloudFixture(t) // its acme/app entry sets base_branch: main
		// Detached: the checkout's own branch never decides the ref, so the ref
		// read here is the layers'. Before the reversal, the local config's main
		// won and the run was cut from main.
		baseBranchCheckout(t, "acme/app", baseBranchYAML("git: { provider: github, base_branch: develop }\n"), "")
		_, errOut, err := execute(t, "run", "--run-id", "20261027-100000-abcd", "A task")
		if err != nil {
			t.Fatal(err)
		}
		spec := readSpec(t, f, "20261027-100000-abcd")
		if spec.Ref != "develop" {
			t.Fatalf("ref = %q, want develop (the checkout's fugaro.yaml, not the local config's main)", spec.Ref)
		}
		if spec.BaseBranch != "" {
			t.Errorf("base_branch = %q, want none: the file sets the key, so its own value is the base", spec.BaseBranch)
		}
		if strings.Contains(errOut, "refresh") {
			t.Errorf("stderr:\n%s", errOut)
		}
	})
	t.Run("the project default when the file sets nothing", func(t *testing.T) {
		f := newCloudFixture(t)
		addOtherRepo(t, f)
		f.appendConfig(t, "base_branch: master\n")
		baseBranchCheckout(t, "acme/other", noBaseBranchYAML, "master")
		if _, _, err := execute(t, "run", "--repo", "acme/other", "--run-id", "20261027-100000-abcd", "A task"); err != nil {
			t.Fatal(err)
		}
		spec := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261027-100000-abcd")
		if spec.Ref != "master" || spec.BaseBranch != "master" {
			t.Fatalf("ref/base_branch = %q/%q, want master/master", spec.Ref, spec.BaseBranch)
		}
	})
	t.Run("an explicit ref carries the project default", func(t *testing.T) {
		// The file that decides a base under --ref is the one at that ref,
		// which only the runner reads; the two layers below it are about the
		// repository, not the checkout, so a --ref launch is cut from develop
		// and opens against the project's master, exactly as one without it.
		f := newCloudFixture(t)
		addOtherRepo(t, f)
		f.appendConfig(t, "base_branch: master\n")
		baseBranchCheckout(t, "acme/other", noBaseBranchYAML, "master")
		if _, _, err := execute(t, "run", "--repo", "acme/other", "--ref", "develop", "--run-id", "20261027-100000-abcd", "A task"); err != nil {
			t.Fatal(err)
		}
		spec := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261027-100000-abcd")
		if spec.Ref != "develop" || spec.BaseBranch != "master" {
			t.Fatalf("ref/base_branch = %q/%q, want develop/master", spec.Ref, spec.BaseBranch)
		}
	})
	t.Run("an explicit ref with no layer below the file writes none", func(t *testing.T) {
		// Nothing names a base but the default: the field is left out, which
		// is what every launch carried before it existed and what a runner
		// older than baseBranchSince reads without refusing the task.
		f := newCloudFixture(t)
		addOtherRepo(t, f)
		baseBranchCheckout(t, "acme/other", noBaseBranchYAML, "master")
		if _, _, err := execute(t, "run", "--repo", "acme/other", "--ref", "develop", "--run-id", "20261027-100000-abcd", "A task"); err != nil {
			t.Fatal(err)
		}
		spec := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261027-100000-abcd")
		if spec.Ref != "develop" || spec.BaseBranch != "" {
			t.Fatalf("ref/base_branch = %q/%q, want develop and no field", spec.Ref, spec.BaseBranch)
		}
	})
}

// TestBaseBranchSkew pins the one gate that omits a field rather than
// refusing a launch (baseBranchSkew), on the fixtures the other *Since gates
// use: a checkout: clone job is judged on the base image its workflow runs
// (checkCloneImageSince's judge), and a derived one on the base its build
// record was built from (checkImageSince's). A job image neither judge can
// show keeps the field: dropping a project's base branch on one is a silent
// change of the branch the run targets.
func TestBaseBranchSkew(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	newSpec := func() *task.Spec {
		return &task.Spec{Version: 1, RunID: "20261027-100000-abcd", Repo: "acme/app", Ref: "master", BaseBranch: "master", Workflow: "web", Task: "x"}
	}
	wantSkew := "the job image predates " + baseBranchSince + " and ignores the project's base branch master; refresh it with fugaro image refresh"
	cloneOf := func(v bool) *bool { return &v }
	baseImageOf := func(e *cloudEnv, ref string) *cloudEnv {
		lc := *e.lc
		lc.BaseImages = map[string]string{}
		if ref != "" {
			lc.BaseImages[config.BaseKind] = ref
		}
		// The env is reused, not copied: it holds a mutex (policyReadOnce), and
		// every case sets its own base images, so a case never sees the last one's.
		e.lc = &lc
		return e
	}
	for _, tc := range []struct {
		name, baseImage, wantBase, wantWarn string
	}{
		{"older than baseBranchSince", "ghcr.io/dimipaun/fugaro-base:0.12.0", "", wantSkew},
		{"at baseBranchSince", "ghcr.io/dimipaun/fugaro-base:" + baseBranchSince, "master", ""},
		{"newer", "ghcr.io/dimipaun/fugaro-base:0.13.0", "master", ""},
		{"no base image at all", "", "master", ""},
		{"a dev ref", "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", "master", ""},
	} {
		t.Run("clone: "+tc.name, func(t *testing.T) {
			e := baseImageOf(env, tc.baseImage)
			spec := newSpec()
			var warn bytes.Buffer
			baseBranchSkew(t.Context(), e, appSlug, spec, cloneOf(true), &warn)
			if spec.BaseBranch != tc.wantBase {
				t.Errorf("base_branch = %q, want %q", spec.BaseBranch, tc.wantBase)
			}
			if !strings.Contains(warn.String(), tc.wantWarn) || (tc.wantWarn == "" && warn.Len() != 0) {
				t.Errorf("stderr %q, want %q", warn.String(), tc.wantWarn)
			}
		})
	}
	t.Run("a derived image's old build record", func(t *testing.T) {
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.12.0")
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), env, appSlug, spec, cloneOf(false), &warn)
		if spec.BaseBranch != "" {
			t.Errorf("base_branch = %q, want none: the record's base predates %s", spec.BaseBranch, baseBranchSince)
		}
		if !strings.Contains(warn.String(), wantSkew) {
			t.Errorf("stderr %q, want %q", warn.String(), wantSkew)
		}
	})
	t.Run("a derived image at baseBranchSince keeps the field", func(t *testing.T) {
		writeBuildRecord(t, f, appSlug, "web", "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+baseBranchSince)
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), env, appSlug, spec, cloneOf(false), &warn)
		if spec.BaseBranch != "master" || warn.Len() != 0 {
			t.Errorf("base_branch = %q, stderr %q; want the field kept and nothing said", spec.BaseBranch, warn.String())
		}
	})
	t.Run("a derived image with no record is left alone", func(t *testing.T) {
		// A fixture of its own: a build record is one object per workflow, and
		// the subtests above stored one.
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), fileEnv(t, newCloudFixture(t)), appSlug, spec, cloneOf(false), &warn)
		if spec.BaseBranch != "master" || warn.Len() != 0 {
			t.Errorf("base_branch = %q, stderr %q; want the field kept and nothing said without a record", spec.BaseBranch, warn.String())
		}
	})
	// A launch for another repository from a checkout that is not its own reads
	// no workflow of it, so the mode is unknown and both judges are asked. The
	// clone judge is what matters there: a checkout: clone workflow is never
	// built, so judging the record alone finds nothing and keeps a field the
	// job image cannot read — the failure this gate exists to prevent.
	t.Run("an unknown mode takes the base image's answer", func(t *testing.T) {
		e := baseImageOf(fileEnv(t, newCloudFixture(t)), "ghcr.io/dimipaun/fugaro-base:0.12.0")
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), e, appSlug, spec, nil, &warn)
		if spec.BaseBranch != "" || !strings.Contains(warn.String(), wantSkew) {
			t.Errorf("base_branch = %q, stderr %q; want the field dropped with %q", spec.BaseBranch, warn.String(), wantSkew)
		}
	})
	t.Run("an unknown mode takes the build record's answer", func(t *testing.T) {
		// The base image is current, so the record is the judge that shows the
		// derived image's own base.
		f := newCloudFixture(t)
		e := baseImageOf(fileEnv(t, f), "ghcr.io/dimipaun/fugaro-base:"+baseBranchSince)
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.12.0")
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), e, appSlug, spec, nil, &warn)
		if spec.BaseBranch != "" || !strings.Contains(warn.String(), wantSkew) {
			t.Errorf("base_branch = %q, stderr %q; want the field dropped with %q", spec.BaseBranch, warn.String(), wantSkew)
		}
	})
	t.Run("an unknown mode with two current judges keeps the field", func(t *testing.T) {
		f := newCloudFixture(t)
		e := baseImageOf(fileEnv(t, f), "ghcr.io/dimipaun/fugaro-base:"+baseBranchSince)
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+baseBranchSince)
		spec := newSpec()
		var warn bytes.Buffer
		baseBranchSkew(t.Context(), e, appSlug, spec, nil, &warn)
		if spec.BaseBranch != "master" || warn.Len() != 0 {
			t.Errorf("base_branch = %q, stderr %q; want the field kept and nothing said", spec.BaseBranch, warn.String())
		}
	})
}

// TestBaseBranchSkewForAnotherRepository is the same unknown mode end to end:
// the launch is for acme/other from this checkout of acme/app, so the launcher
// reads no workflow of the repository it is launching and cannot tell whether
// its job is a checkout: clone one. The installation's base image predates
// baseBranchSince while acme/other's build record — the judge a derived
// workflow would use — is current, so the field is dropped on the base image's
// word alone; the earlier gate kept it and the run died at bootstrap.
func TestBaseBranchSkewForAnotherRepository(t *testing.T) {
	f := newCloudFixture(t)
	addOtherRepo(t, f)
	f.appendConfig(t, "base_branch: master\n")
	f.appendConfig(t, "base_images: { base: ghcr.io/dimipaun/fugaro-base:0.12.0 }\n")
	baseBranchCheckout(t, "acme/app", baseBranchYAML("git: { provider: github, base_branch: main }\n"), "main")
	_, errOut, err := execute(t, "run", "--repo", "acme/other", "--run-id", "20261027-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	spec := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261027-100000-abcd")
	if spec.BaseBranch != "" {
		t.Fatalf("base_branch = %q, want none: the installation's base image predates %s", spec.BaseBranch, baseBranchSince)
	}
	if !strings.Contains(errOut, "predates "+baseBranchSince) {
		t.Fatalf("stderr:\n%s", errOut)
	}
}

// TestBaseBranchSinceIsTheReleaseTheChecklistNames holds baseBranchSince to the
// reader that states its number: docs/release.md's Before-you-tag bullet, which
// is where a release verifies the constant against the tag actually cut.
func TestBaseBranchSinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `baseBranchSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no baseBranchSince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+baseBranchSince+"\"`") {
		t.Errorf("docs/release.md's baseBranchSince bullet does not carry the constant's release (%q)", baseBranchSince)
	}
}

// TestValidateNamesTheLayerAnInvalidBaseBranchIsIn: fugaro validate judges
// every base branch where it is written and names the layer it was written in
// — the file's own git.base_branch by its path, and an installation's
// base_branch (or its repos.<r>.base_branch) as the project config it could
// not read — so an operator is told which of the four layers to fix.
func TestValidateNamesTheLayerAnInvalidBaseBranchIsIn(t *testing.T) {
	t.Run("the file's own key", func(t *testing.T) {
		out, _, err := execute(t, "validate", writeConfig(t, baseBranchYAML("git: { provider: github, base_branch: \"-bad\" }\n")))
		if err == nil || !strings.Contains(out, "git.base_branch") {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
	t.Run("the project config's", func(t *testing.T) {
		dir := t.TempDir()
		isolateProjects(t, dir)
		path := writeProject(t, "aurora", "aurora-gcp")
		appendProjectConfig(t, path, "base_branch: \"-bad\"\n")
		enrollRepo(t, "aurora", "acme/app")
		t.Chdir(checkoutsAt(t, filepath.Join(dir, "src", "app"),
			"version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n",
			"https://github.com/acme/app.git"))
		out, errOut, err := execute(t, "validate")
		if err == nil {
			t.Fatalf("an installation whose base_branch is no branch name was accepted:\n%s%s", out, errOut)
		}
		if !strings.Contains(out, "base_branch") || !strings.Contains(out, "project config") {
			t.Fatalf("the layer is not named:\nstdout:\n%s\nstderr:\n%s", out, errOut)
		}
	})
}

// appendProjectConfig adds top-level YAML to a written project config.
func appendProjectConfig(t *testing.T, path, yaml string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, yaml...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBaseBranchOnAFollowUp: a --pr follow-up carries the base of the run it
// continues, which is the branch its configuration is read from — the
// runner's readBaseConfig refuses a file whose base is not the run's ref, and
// the file this follow-up reads sets nothing.
func TestBaseBranchOnAFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	root := firstRunSpec(rootID)
	root.Ref, root.BaseBranch = "master", "master"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, fuID)
	if spec.Ref != "master" || spec.BaseBranch != "master" {
		t.Fatalf("follow-up ref/base_branch = %q/%q, want master/master", spec.Ref, spec.BaseBranch)
	}
}
