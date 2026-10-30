package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// cloneYAML is the fixture workflow with checkout: clone (design
// generic-tool.md §2, G3): no image block, so a clone job may run it.
const cloneYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  review_rounds: 2
workflows:
  app:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed:
        command: sh test.sh
        each: "{id}"
      reports: ["build/test-results/*.xml"]
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
followup:
  trusted: ["1234567"]
`

// sharedYAML is cloneYAML with the workflow on checkout: shared (design
// forge-authorship-images/shared-images.md §6, route A): a job that runs a
// shared dependency-layer image and clones the repository's own source at
// launch. The runner decides whether it clones from the job's
// FUGARO_CHECKOUT alone and never from the checkout's config — before the
// clone there is no checkout to read one from — so the two are kept equal
// here rather than one standing in for the other.
var sharedYAML = strings.Replace(cloneYAML, "checkout: clone", "checkout: shared", 1)

// cloneJob makes the run's job a checkout: clone one: no checkout in
// WorkDir (the harness never creates it) and FUGARO_CHECKOUT=clone in the
// job's environment, with a provider token for mise's rate limit. The
// provider is GitHub, so provision may hand mise the run's git token —
// which is what the tests below are about.
func (h *harness) cloneJob(t *testing.T) {
	t.Helper()
	h.cloneShapedJob(t, "clone")
}

// sharedJob is cloneJob for the third kind: a checkout: shared workflow's
// job runs a shared dependency-layer image, which holds no checkout either,
// so the run clones its own repository exactly as a clone job does (design
// forge-authorship-images/shared-images.md §1, §6, route A).
func (h *harness) sharedJob(t *testing.T) {
	t.Helper()
	h.cloneShapedJob(t, config.CheckoutShared)
}

func (h *harness) cloneShapedJob(t *testing.T, kind string) {
	t.Helper()
	if _, err := os.Stat(h.deps.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("the harness's checkout already exists: %v", err)
	}
	h.deps.Env = append(h.deps.Env, runner.CheckoutEnv+"="+kind)
	h.deps.ProviderKind = gitprovKindGitHub
	h.provider.Auth = staticAuth(gitToken)
}

// gitprovKindGitHub is gitprov.KindGitHub, spelled here so the test file
// need not import the package for one constant.
const gitprovKindGitHub = "github"

// fakeMise puts a mise on the run's PATH that logs every call (`<cwd>|<argv>`
// per line), dumps the environment of every call (marked by a "--- <argv>"
// line and the environment after it; MISE_ENV), and behaves like the real
// one where it matters: `install` installs (exiting with exit, and reporting
// a failure on stderr the way mise does), `ls --current --missing` prints
// MISE_MISSING on stdout and exits 0. MISE_HANG makes install hang, for the
// timeout tests. It returns the call log's path.
func (h *harness) fakeMise(t *testing.T, exit int) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "mise.log")
	envLog := filepath.Join(dir, "mise.env")
	st := strconv.Itoa(exit)
	script := "#!/bin/sh\n" +
		// The install line is printed before anything else the script does:
		// the timeout test's bound is short, and the first execution of a
		// freshly written script (macOS especially, where a new file's first
		// exec is slow) must not eat it before mise says anything.
		"if [ \"$1\" = install ]; then echo 'mise: installing tools'; fi\n" +
		"printf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$MISE_LOG\"\n" +
		"printf -- '--- %s\\n' \"$*\" >> \"$MISE_ENV\"\n" +
		// Which mise config this call actually saw (its first line), for the
		// test that asks which branch's config provision read.
		"printf 'MISETOML=%s\\n' \"$(head -n 1 mise.toml 2>/dev/null)\" >> \"$MISE_ENV\"\n" +
		"env >> \"$MISE_ENV\"\n" +
		"case \"$1\" in\n" +
		"ls)\n" +
		"  printf '%s' \"$MISE_MISSING\"\n" +
		"  exit \"${MISE_LS_EXIT:-0}\" ;;\n" +
		"install)\n" +
		"  if [ -f \"$MISE_HANG\" ]; then sleep 30; fi\n" +
		"  test " + strconv.Quote(st) + " -eq 0 || {\n" +
		"    echo 'mise: no such tool node' >&2\n" +
		"    echo \"mise: github api said: token $GITHUB_TOKEN\" >&2\n" +
		"  }\n" +
		"  exit " + st + " ;;\n" +
		"esac\n" +
		"exit 0\n"
	testutil.WriteFiles(t, dir, map[string]string{"mise": script})
	// exec.Command resolves the binary through the *test process's* PATH,
	// not through the child's environment, so the fake has to be first
	// there as well as in the run's own environment.
	path := dir + ":" + os.Getenv("PATH")
	t.Setenv("PATH", path)
	h.deps.Env = append(filterEnv(h.deps.Env, "PATH"), "PATH="+path, "MISE_LOG="+log, "MISE_ENV="+envLog)
	return log
}

// miseHang makes the fake mise's install hang (the timeout tests).
func (h *harness) miseHang(t *testing.T) {
	t.Helper()
	flag := filepath.Join(t.TempDir(), "hang")
	testutil.WriteFiles(t, filepath.Dir(flag), map[string]string{filepath.Base(flag): ""})
	h.deps.Env = append(h.deps.Env, "MISE_HANG="+flag)
}

type miseCall struct {
	Dir  string
	Args []string
}

// miseCalls reads the fake mise's log: each call's directory and arguments.
func miseCalls(t *testing.T, log string) []miseCall {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil // mise never ran
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []miseCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		dir, args, _ := strings.Cut(line, "|")
		out = append(out, miseCall{Dir: dir, Args: strings.Fields(args)})
	}
	return out
}

// miseEnvs reads the environment the fake mise was given, one entry per
// call, in call order, keyed by variable name.
func miseEnvs(t *testing.T, envLog string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(envLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "--- ") {
			out = append(out, map[string]string{})
			continue
		}
		if len(out) == 0 {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[len(out)-1][k] = v
		}
	}
	return out
}

// provisionTiming is the record's provision stage timing, if it ran.
func provisionTiming(rec *runstore.Record) *runstore.StageTiming {
	for i, s := range rec.Stages {
		if s.Name == "provision" {
			return &rec.Stages[i]
		}
	}
	return nil
}

// TestCloneProvisionRunsMiseInstall (plan Task 17, design generic-tool.md
// §2, G4): a run whose job has no derived image clones its repository
// blobless and installs the checkout's root mise config itself, before its
// first stage, recording the time as the provision stage.
func TestCloneProvisionRunsMiseInstall(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	// mise logs $PWD, which is the resolved path: on macOS a temp dir is
	// under /var, itself a symlink to /private/var, so the comparison is
	// against the resolved workdir (review L2).
	work, err := filepath.EvalSymlinks(h.deps.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	got := miseCalls(t, log)
	if len(got) != 2 || got[0].Args[0] != "install" || got[0].Dir != work ||
		!slices.Equal(got[1].Args, []string{"ls", "--current", "--missing"}) {
		t.Fatalf("mise calls = %+v, want `mise install` then `mise ls --current --missing` in %s", got, work)
	}
	if provisionTiming(rec) == nil {
		t.Fatalf("no provision timing in %+v", rec.Stages)
	}
	if v := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("the checkout's filter = %q, want blob:none", v)
	}
}

// TestProvisionClonesOnShared: a run whose job says FUGARO_CHECKOUT=shared
// clones exactly as a clone job does — blobless, from the job's own remote —
// and installs the checkout's mise tools before its first stage. The switch
// in cloneJob() is the runner's whole side of checkout: shared (design
// forge-authorship-images/shared-images.md §6, route A: "the runner needs no
// change beyond the FUGARO_CHECKOUT value it already switches on"), and a
// value it does not know would leave the run opening a checkout its image
// does not hold.
//
// Mutation (run, restore): drop config.CheckoutShared from cloneJob()'s
// switch — this test then fails: the run makes no blobless clone, opens the
// checkout the image is expected to hold, and mise runs nothing.
func TestProvisionClonesOnShared(t *testing.T) {
	h := newHarnessFiles(t, sharedYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	h.sharedJob(t)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(h.deps.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	got := miseCalls(t, log)
	if len(got) != 2 || got[0].Args[0] != "install" || got[0].Dir != work ||
		!slices.Equal(got[1].Args, []string{"ls", "--current", "--missing"}) {
		t.Fatalf("mise calls = %+v, want `mise install` then `mise ls --current --missing` in %s", got, work)
	}
	if provisionTiming(rec) == nil {
		t.Fatalf("no provision timing in %+v", rec.Stages)
	}
	if v := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("the checkout's filter = %q, want blob:none: a shared job's image holds no checkout either", v)
	}
}

// TestCloneProvisionMiseEnvironment (review M5 and this round's high
// finding): the environment mise runs with is pinned exactly. A mise backend
// runs repository-chosen code and, on a follow-up, the branch's own config,
// so the run's model credentials, the gateway's provider keys, and the static
// git-provider credentials (the App private key, the Bitbucket token, and
// every other FUGARO_* variable, from which the run's GITHUB_TOKEN is minted)
// are gone. The run's git token is GITHUB_TOKEN, which is what mise's GitHub
// API calls use for their rate limit.
//
// Mutation (run, restore): drop `contains(modelCredentialVars, k)` (or the
// GITHUB_TOKEN drop) from provisionEnv — the ANTHROPIC_API_KEY (or the token
// absence) assertion fails; drop the `strings.HasPrefix(k, "FUGARO_")` arm —
// the git-provider credential assertions fail; drop the GITHUB_TOKEN append
// in provisionGitToken — the token assertion fails.
func TestCloneProvisionMiseEnvironment(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 0)
	h.cloneJob(t)
	h.deps.Env = append(h.deps.Env,
		"ANTHROPIC_API_KEY=model-secret",
		"FUGARO_PROVIDER_KEY_OPENROUTER=provider-secret",
		"GH_TOKEN=planted-gh-secret",
		// The static git-provider credentials (this round's high finding):
		// mise runs the branch's own code, so none of these may reach it.
		providers.EnvGitHubAppKey+"=app-private-key-secret",
		providers.EnvGitHubAppKeyFile+"=/secrets/app.pem",
		providers.EnvGitHubAppID+"=1234567",
		providers.EnvBitbucketToken+"=bitbucket-secret",
		providers.EnvGitHubAPIURL+"=https://api.github.com",
		"FUGARO_GIT_TOKEN=derived-git-secret",
	)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	envs := miseEnvs(t, envValue(h.deps.Env, "MISE_ENV"))
	if len(envs) == 0 {
		t.Fatal("mise never ran")
	}
	for i, env := range envs {
		if _, ok := env["ANTHROPIC_API_KEY"]; ok {
			t.Errorf("mise call %d sees ANTHROPIC_API_KEY", i)
		}
		if _, ok := env["FUGARO_PROVIDER_KEY_OPENROUTER"]; ok {
			t.Errorf("mise call %d sees a provider key", i)
		}
		if _, ok := env["GH_TOKEN"]; ok {
			t.Errorf("mise call %d sees GH_TOKEN", i)
		}
		for _, k := range []string{
			providers.EnvGitHubAppKey, providers.EnvGitHubAppKeyFile, providers.EnvGitHubAppID,
			providers.EnvBitbucketToken, providers.EnvGitHubAPIURL,
		} {
			if _, ok := env[k]; ok {
				t.Errorf("mise call %d sees the git-provider credential %s", i, k)
			}
		}
		// Nothing of Fugaro's own at all: the prefix rule is what makes
		// this fail closed for a credential added later.
		for k := range env {
			if strings.HasPrefix(k, "FUGARO_") {
				t.Errorf("mise call %d sees Fugaro's own %s", i, k)
			}
		}
		if got := env["GITHUB_TOKEN"]; got != gitToken {
			t.Errorf("mise call %d's GITHUB_TOKEN = %q, want the run's git token", i, got)
		}
	}
}

// TestCloneProvisionNoTokenWithoutGitHub (review M1): a Bitbucket
// repository's access token is not a GitHub credential, and mise sends
// GITHUB_TOKEN to api.github.com and to GitHub-backed plugin hosts: a
// provider that is not GitHub gets no GITHUB_TOKEN at all.
func TestCloneProvisionNoTokenWithoutGitHub(t *testing.T) {
	cfg := strings.NewReplacer("provider: github", "provider: bitbucket", `trusted: ["1234567"]`,
		`trusted: ["557058:00000000-0000-0000-0000-000000000001"]`).Replace(cloneYAML)
	h := newHarnessFiles(t, cfg, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 0)
	h.cloneJob(t)
	h.deps.ProviderKind = "bitbucket"
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	envs := miseEnvs(t, envValue(h.deps.Env, "MISE_ENV"))
	if len(envs) == 0 {
		t.Fatal("mise never ran")
	}
	for i, env := range envs {
		if got, ok := env["GITHUB_TOKEN"]; ok && got != "" {
			t.Errorf("mise call %d's GITHUB_TOKEN = %q, want none for a Bitbucket repository", i, got)
		}
	}
}

// cloneFollowUpHarness is followUpHarness's sequencing for a checkout:
// clone job whose repository carries a root mise config: the same fixture
// and the same first run, with mise.toml committed and mise installed.
func cloneFollowUpHarness(t *testing.T) *fuHarness {
	t.Helper()
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.provider.SelfID = fugaroSelf
	statePath := filepath.Join(t.TempDir(), "fake.json")
	h.provider.Path = statePath
	h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, commitOnly("feature"), review("ship", 0))
	if err != nil || rec.PushedHead == "" || rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("first run: rec = %+v, err = %v", rec, err)
	}
	return &fuHarness{harness: h, statePath: statePath, first: rec}
}

// TestCloneFollowUpProvisionsWithoutTheToken (review H1): a follow-up runs
// mise in the pull request's own branch, which an earlier agent wrote and
// which a mise config can make run any code ([env] templates, [hooks],
// plugins from any git URL). The push token the runner holds must not reach
// it there, though a first run — the user's own ref — still gives mise the
// token for its rate limit.
func TestCloneFollowUpProvisionsWithoutTheToken(t *testing.T) {
	h := cloneFollowUpHarness(t)
	first := miseEnvs(t, envValue(h.deps.Env, "MISE_ENV"))
	if len(first) == 0 || first[0]["GITHUB_TOKEN"] != gitToken {
		t.Fatalf("the first run's mise env = %+v, want the git token", first)
	}
	h.followUp(t, followID, runID, "please continue")
	rec, err := h.run(t, implement("more"), review("ship", 0))
	mustReady(t, rec, err)
	envs := miseEnvs(t, envValue(h.deps.Env, "MISE_ENV"))
	if len(envs) <= len(first) {
		t.Fatalf("the follow-up ran no mise: %+v", envs)
	}
	for i, env := range envs[len(first):] {
		if got, ok := env["GITHUB_TOKEN"]; ok && got != "" {
			t.Errorf("follow-up mise call %d's GITHUB_TOKEN = %q, want none: the branch is agent-written code", i, got)
		}
	}
}

// TestCloneReviewProvisionsTheBaseBranch (review L4): a review run takes its
// configuration from the base branch (readBaseConfig) and never from the
// branch under review, so its mise config has to come from the base branch
// too — not from whatever branch the remote calls its default, which is all
// a clone leaves behind.
//
// Mutation (run, restore): drop the `if r.cloneJob() { repo.CheckoutRef
// ... }` block from the review case in bootstrap — there is no working tree
// to read, so mise sees no config at all and the test fails.
func TestCloneReviewProvisionsTheBaseBranch(t *testing.T) {
	spec := reviewTaskSpec("reviewonly", reviewOnlyYAML, 7)
	spec.Ref = "release"
	h := newHarnessFiles(t, cloneYAML, spec, map[string]string{"mise.toml": "# main\n[tools]\nnode = \"24\"\n"})
	// A base branch that is not the remote's default branch, with a mise
	// config of its own: only reading the base branch can find it.
	pushReviewBranch(t, h, "release", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{
			"mise.toml":   "# release\n[tools]\nnode = \"22\"\n",
			"fugaro.yaml": strings.Replace(cloneYAML, "base_branch: main", "base_branch: release", 1),
		})
	})
	pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "someone-elses-change", "")
	h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("review record = %+v", rec)
	}
	envs := miseEnvs(t, envValue(h.deps.Env, "MISE_ENV"))
	if len(envs) == 0 {
		t.Fatal("mise never ran")
	}
	if got := envs[0]["MISETOML"]; got != "# release" {
		t.Fatalf("mise read %q as the checkout's mise config, want the base branch's (# release)", got)
	}
}

// TestCloneFollowUpProvisionsOnlyAfterThePullRequestCheck (review H1): the
// order in bootstrap is the fix, not only the missing token. checkPullRequest
// is what refuses a pull request the run must not touch at all (a public
// repository without followup.allow_public, a fork, a branch that moved), and
// it runs before provision — so a refused follow-up never runs the branch's
// own mise config, i.e. never runs code from a branch the run then refuses.
//
// Mutation (run, restore): move the `if r.follow != nil { checkPullRequest }`
// block after the provision call — mise runs for the refused pull request and
// the call count below grows.
func TestCloneFollowUpProvisionsOnlyAfterThePullRequestCheck(t *testing.T) {
	h := cloneFollowUpHarness(t)
	log := envValue(h.deps.Env, "MISE_LOG")
	before := len(miseCalls(t, log))
	// A public repository without followup.allow_public: checkPullRequest
	// refuses the follow-up outright.
	h.editState(t, func(st *fake.State) { st.Public = true })
	h.followUp(t, followID, runID, "please continue")
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "followup.allow_public") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if after := len(miseCalls(t, log)); after != before {
		t.Fatalf("mise ran %d more time(s) for a pull request the run refuses: the branch's own config executed", after-before)
	}
}

// TestCloneProvisionNoMiseConfig: a checkout with no root mise config has
// nothing to install: no mise call and no provision stage.
func TestCloneProvisionNoMiseConfig(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, nil)
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := miseCalls(t, log); len(got) != 0 {
		t.Fatalf("mise calls = %+v, want none", got)
	}
	if provisionTiming(rec) != nil {
		t.Fatalf("a provision timing with nothing to install: %+v", rec.Stages)
	}
}

// TestCloneJobReusesAnExistingCheckout (review L6): a clone job is not
// always a bare directory. A warm container that already ran a job, or one
// an earlier run on a shared host left, holds a checkout: the run uses it
// (OpenOrClone), never trying to clone over it — which git would refuse
// outright, so the run succeeding is itself the assertion — and its own
// branch is still checked out from it.
//
// Mutation (run, restore): drop the `if _, err := gitops.Open(...); err ==
// nil ||` arm of openCheckout so a clone job always calls CloneBlobless —
// the run fails, "destination path ... already exists".
func TestCloneJobReusesAnExistingCheckout(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	// The checkout that is already there, made the way a warm container's
	// would have been (the image's own clone, not CloneBlobless), with a
	// marker the run must not have removed.
	testutil.Git(t, filepath.Dir(h.deps.WorkDir), "clone", "--quiet", h.remote, h.deps.WorkDir)
	marker := filepath.Join(h.deps.WorkDir, ".git", "keep-me")
	testutil.WriteFiles(t, filepath.Dir(marker), map[string]string{filepath.Base(marker): "x\n"})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the checkout was replaced: %v", err)
	}
	work, err := filepath.EvalSymlinks(h.deps.WorkDir) // $PWD is resolved (review L2)
	if err != nil {
		t.Fatal(err)
	}
	if got := miseCalls(t, log); len(got) == 0 || got[0].Dir != work {
		t.Fatalf("mise calls = %+v, want the provision stage in the existing checkout %s", got, work)
	}
	// And the run's own branch is checked out from that same checkout.
	if got := testutil.Git(t, h.deps.WorkDir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/"+runID {
		t.Fatalf("the checkout is on %q, want the run's own branch", got)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "fugaro.yaml")); err != nil {
		t.Fatalf("the checkout has no fugaro.yaml: %v", err)
	}
}

// TestBakedRunProvisionsNothing: a baked job's image installed the tools at
// build time, so it runs no mise and records no provision stage.
func TestBakedRunProvisionsNothing(t *testing.T) {
	h := newHarnessFiles(t, "", nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := miseCalls(t, log); len(got) != 0 {
		t.Fatalf("mise calls = %+v, want none", got)
	}
	if provisionTiming(rec) != nil {
		t.Fatalf("a baked run recorded a provision stage: %+v", rec.Stages)
	}
}

// TestCloneProvisionFailureIsInfraError: mise refusing to install the
// repository's tools ends the run as infra_error with mise's last line
// (redacted) as the reason.
func TestCloneProvisionFailureIsInfraError(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 1)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("a failed mise install did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || rec.Reason == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "mise install failed") || !strings.Contains(rec.Reason, "github api said") {
		t.Fatalf("reason = %q, want mise's last line", rec.Reason)
	}
	if !strings.Contains(rec.Reason, "[REDACTED]") || strings.Contains(rec.Reason, gitToken) {
		t.Fatalf("reason is not redacted: %q", rec.Reason)
	}
	if provisionTiming(rec) == nil {
		t.Fatalf("a failed provision was not timed: %+v", rec.Stages)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("the agent ran anyway: %+v", h.agent.calls)
	}
}

// TestCloneProvisionLeavesNothingMissing (review L1): `mise install`
// exiting 0 is not proof the tools are there — the derived image's build
// checks `mise ls --current --missing` for exactly that reason
// (images/derived/Dockerfile.tmpl) — so provision checks it too, and a
// tool it names fails the run as infra_error instead of surfacing later, in
// a stage that needs the missing binary.
func TestCloneProvisionLeavesNothingMissing(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 0)
	h.cloneJob(t)
	h.deps.Env = append(h.deps.Env, "MISE_MISSING=node")
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("a partial install did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "left these tools missing") ||
		!strings.Contains(rec.Reason, "node") {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("the agent ran anyway: %+v", h.agent.calls)
	}
}

// TestCloneProvisionMiseListFailureIsInfraError (review L1): `mise ls
// --current --missing` says what is missing on stdout, so its status is not
// consulted for that — but a non-zero status with nothing on stdout (a
// config mise cannot parse) is a broken check, not a clean install, and the
// derived image's own `missing="$(mise ls ...)" && ...` fails on exactly
// that. Without the exit-status check the run went on as if nothing were
// missing.
//
// Mutation (run, restore): drop the `code != 0` case from miseComplete —
// the run no longer fails and the agent runs.
func TestCloneProvisionMiseListFailureIsInfraError(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 0)
	h.cloneJob(t)
	h.deps.Env = append(h.deps.Env, "MISE_LS_EXIT=2")
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("a failing mise ls did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "checking the mise install") ||
		!strings.Contains(rec.Reason, "exit 2") {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("the agent ran anyway: %+v", h.agent.calls)
	}
}

// TestCloneProvisionTimeoutIsItsOwnFailure (review M2): the ten-minute
// bound ends provision as its own error, naming the bound and mise's last
// line, rather than as a bare context deadline that reads like the run's own
// total timeout.
func TestCloneProvisionTimeoutIsItsOwnFailure(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 0)
	h.cloneJob(t)
	h.miseHang(t)
	// Two seconds, not a few hundred milliseconds: mise prints its line as
	// the script's first act (fakeMise), but the first execution of a
	// freshly written script costs enough on macOS that a tighter bound
	// killed it before it printed anything, and the test then failed there
	// with "mise said nothing" (review L2). The bound only has to be
	// smaller than the fake's 30-second hang, not small in itself.
	h.deps.ProvisionTimeout = 2 * time.Second
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("a hanging mise install did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "did not finish within 2s") {
		t.Fatalf("reason = %q, want provision's own bound", rec.Reason)
	}
	if !strings.Contains(rec.Reason, "installing tools") {
		t.Fatalf("reason = %q, want mise's last line", rec.Reason)
	}
}

// TestCloneProvisionCancelIsNotATimeout (review M2): when the *run's* own
// context ends while mise is running, provision reports that end — never its
// own ten-minute bound, which did not fire, and never a success because the
// killed mise happened to exit 0.
func TestCloneProvisionCancelIsNotATimeout(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	h.miseHang(t)
	// Cancel once mise is actually running: its own log line is the signal,
	// read here without any test helper (t.Fatal is not for other
	// goroutines).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 3000; i++ {
			if b, err := os.ReadFile(log); err == nil && len(b) > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	h.agent.steps = nil
	rec, err := runner.Run(ctx, h.deps)
	if err == nil {
		t.Fatal("a cancelled run returned no error")
	}
	if rec != nil && rec.Status == runstore.StatusSucceeded {
		t.Fatalf("record = %+v, want a failure", rec)
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want the run's own cancellation", err)
	}
	if strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("err = %v: the run's cancellation was reported as provision's bound", err)
	}
}
