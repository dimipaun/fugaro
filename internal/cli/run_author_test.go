package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// releaseAuthor and releaseBeforeAuthor are the two job images the tests here
// judge: one built from a base at the release that ships the author field and
// the git.author key, one from the release before it. devAuthorBase is a base
// image that is not a release at all — a development build, which no judge can
// place against a release.
const (
	releaseAuthor       = "ghcr.io/dimipaun/fugaro-base:0.14.0"
	releaseBeforeAuthor = "ghcr.io/dimipaun/fugaro-base:0.13.0"
	devAuthorBase       = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"
)

// authorYAML is a fugaro.yaml whose git: block names author as git.author.
func authorYAML(author string) string {
	return "version: 1\nproject: aurora\ngit: { provider: github, author: " + author + " }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
}

// setLocalUser rewrites the user: key of the fixture's local config, which
// newCloudFixture writes as an address: the launch resolves its identity from
// it (localcfg.Me), so a CI box or a bot account — a value that is not an
// address — is only reachable from a config that carries one.
func setLocalUser(t *testing.T, user string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines, set := strings.Split(string(data), "\n"), false
	for i, l := range lines {
		if strings.HasPrefix(l, "user: ") {
			lines[i], set = "user: "+user, true
		}
	}
	if !set {
		t.Fatalf("the fixture's local config carries no user: line:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunAuthorFromLocalConfig: a repository that opted in (git.author:
// launcher) gets a task whose author's address is this machine's own identity —
// the local config's `user:`, someone@example.com — and whose name is that
// address's local part, which is what launchAuthor falls back to because the
// checkout baseBranchCheckout builds has no user.name for git to answer with
// (testutil.IsolateGit empties git's own config). Each fact is asserted on its
// own field: a rule that stopped reading the local config, or one that took a
// name from anywhere but the address, is caught by the field it changed.
func TestRunAuthorFromLocalConfig(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261030-100000-abcd")
	if spec.Author == nil {
		t.Fatal("the launch recorded no author for a repository that opted in")
	}
	if spec.Author.Email != "someone@example.com" {
		t.Errorf("author email = %q, want the local config's own user: (someone@example.com)", spec.Author.Email)
	}
	if spec.Author.Name != "someone" {
		t.Errorf("author name = %q, want the address's local part, which is what a checkout with no user.name falls back to", spec.Author.Name)
	}
}

// TestRunAuthorOmittedWithoutOptIn: git.author is the repository's own decision
// (D606), so a repository that does not write the key records no author even
// though the launcher has an identity to offer — the default every repository
// has today, and the reason `default` in this file's git: block is read rather
// than the launcher's identity alone.
func TestRunAuthorOmittedWithoutOptIn(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorFugaro), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, "20261030-100000-abcd"); spec.Author != nil {
		t.Fatalf("author = %+v, want none: the repository did not opt in", spec.Author)
	}
}

// TestRunAuthorCommitEmailOverridesMe: the local config's commit_email is the
// address a run's commits are authored as, with commit_name as the name, for
// the person who keeps their real address out of a repository's history
// (D606): a forge noreply address is accepted even though it is not the
// address the launcher's own identity (user:) holds (D649).
func TestRunAuthorCommitEmailOverridesMe(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "commit_name: Someone Else\ncommit_email: someone-else@users.noreply.github.com\n")
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261030-100000-abcd")
	want := &task.Person{Name: "Someone Else", Email: "someone-else@users.noreply.github.com"}
	if spec.Author == nil || *spec.Author != *want {
		t.Fatalf("author = %+v, want %+v: commit_email and commit_name are what the run's commits are authored as", spec.Author, want)
	}
}

// TestRunAuthorRefusesAColleaguesAddress: commit_email that is neither the
// address the launcher launches as nor a forge noreply address is refused at
// launch (D649) — a run must not write a colleague's address into a history
// the forge links to that colleague's account, and silently ignoring the key
// would hide the mistake instead.
func TestRunAuthorRefusesAColleaguesAddress(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "commit_email: someone-else@example.com\n")
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	_, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "commit_email") || !strings.Contains(err.Error(), "somebody else") {
		t.Fatalf("err = %v, want a refusal naming commit_email and whose address it is", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunAuthorOmittedWithoutIdentity: the resolver records nobody when the
// launcher's identity is empty, so the run's commits stay the run's own
// (D605). It is called directly because a launch never reaches it with an
// empty identity: newSpec asks localcfg.Me first and refuses with "cannot tell
// who you are" when neither user: nor git config user.email holds anything.
func TestRunAuthorOmittedWithoutIdentity(t *testing.T) {
	author, err := launchAuthor(t.Context(), &localcfg.Config{}, "")
	if err != nil || author != nil {
		t.Fatalf("author = %+v, err = %v, want no author and no error", author, err)
	}
	// A commit_email does not conjure an identity either: with no launcher to
	// compare it to, an address that is not a forge noreply address is still
	// somebody else's.
	if _, err := launchAuthor(t.Context(), &localcfg.Config{CommitEmail: "someone-else@example.com"}, ""); ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// TestRunAuthorOmittedForNonEmailIdentity: the launcher's identity is not an
// address — user: alice, a CI box or a bot account (D605) — so the launch
// records no author and the run's commits keep the run's own identity. The
// opt-in is read, the identity is not one git can write into a commit, and
// nothing about the launch is refused.
func TestRunAuthorOmittedForNonEmailIdentity(t *testing.T) {
	f := newCloudFixture(t)
	setLocalUser(t, "alice")
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	out, errOut, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}
	if spec := readSpec(t, f, "20261030-100000-abcd"); spec.Author != nil {
		t.Fatalf("author = %+v, want none for a launcher whose identity is not an address (out %q)", spec.Author, out)
	}
}

// TestRunAuthorOmittedForOlderRunner: an image that cannot be shown to run a
// fugaro at authorSince or later gets a task with no author at all — a runner
// older than that refuses a task carrying the field outright (task.Parse
// decodes with DisallowUnknownFields), so carrying it would turn a launch that
// works today into a run that dies after it was paid for, over an attribution
// alone.
//
// The base image here is a development build, which no judge can place against
// a release: the key's own gate only warns about such an image
// (checkImageSince's non-release arm), so this is the case where the field's
// gate is what decides, and the stricter "not shown to" rule drops the field
// where the other gates' "not shown to predate" would keep it.
func TestRunAuthorOmittedForOlderRunner(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", devAuthorBase)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	_, errOut, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}
	if spec := readSpec(t, f, "20261030-100000-abcd"); spec.Author != nil {
		t.Fatalf("author = %+v, want none: an image that is not known to read the field must not be handed one", spec.Author)
	}
	if !strings.Contains(errOut, "not known to run fugaro "+authorSince) {
		t.Fatalf("stderr = %q, want the drop to say which image was not known", errOut)
	}
}

// TestRunAuthorOmittedForReviewRun: a review run pushes nothing, so the
// identity its task would carry is a claim about commits that are never made
// and the launch records none (D605) — while the key the repository opted in
// with is still read and judged, since the runner decodes the same file in
// every mode.
func TestRunAuthorOmittedForReviewRun(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-110000-abcd", "--recipe", "review-only", "--pr", "7"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261030-110000-abcd")
	if len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("review_prs = %v, want [7]: this launch is not the review run the test means", spec.ReviewPRs)
	}
	if spec.Author != nil {
		t.Fatalf("author = %+v, want none for a review run", spec.Author)
	}
}

// TestRunAuthorGitAuthorKeyGatedForOlderRunner: a launch whose fugaro.yaml
// sets git.author against a job image built from a release older than
// gitAuthorSince is refused, with the fix that kind of image has, rather than
// launched to die at bootstrap on a key its runner's KnownFields decoder
// refuses — and rather than quietly ignored, which would drop the opt-in the
// repository asked for.
//
// Every value of the key is judged, because the decoder that refuses it reads
// the key alone: git.author: fugaro is the key absent to this release's config
// and git.author: with nothing after it is fugaro again once decoded, while
// both are lines an older runner refuses exactly like launcher. A gate that
// judged the value would launch those to die at bootstrap on a run already paid
// for.
func TestRunAuthorGitAuthorKeyGatedForOlderRunner(t *testing.T) {
	for _, c := range []struct{ name, author string }{
		{"launcher", config.GitAuthorLauncher},
		{"fugaro", config.GitAuthorFugaro},
		{"an explicit null", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			writeBuildRecord(t, f, appSlug, "web", releaseBeforeAuthor)
			baseBranchCheckout(t, "acme/app", authorYAML(c.author), "main")
			_, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
			for _, want := range []string{"git.author", "the git.author key", "fugaro " + gitAuthorSince + " or later", "release 0.13.0", "fugaro image refresh"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want it to name %q", err, want)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Fatalf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesGitAuthorOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge (it runs the installation's base image
// as it is), so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image refresh
// of a workflow that is never built.
func TestRunRefusesGitAuthorOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, gitAuthorSince, -1)
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	configSkewCheckout(t, f, authorCloneHead)
	_, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"fugaro.yaml's git.author of acme/other", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// authorCloneHead is configSkewHead's file with the git.author key written
// beside it, for a workflow that is never built: the clone gate's own case.
const authorCloneHead = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop, author: launcher }\nagent: { auth: api-key }\n" +
	"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"

// TestAuthorSkewDropsForACloneWorkflowsBaseImage: authorSkew's clone branch,
// which judges the installation's base image rather than a build record — the
// clone job runs that image itself — and drops the field unless that image is
// *shown* to read it. A base image older than authorSince, and one no judge can
// place at all (a development build), both drop it with the warning that names
// the image and the fix; one at the release keeps what the launch resolved.
// Called directly with the local config alone: the clone branch reads nothing
// else.
func TestAuthorSkewDropsForACloneWorkflowsBaseImage(t *testing.T) {
	for _, c := range []struct {
		name, base string
		kept       bool
	}{
		{"the release", releaseAuthor, true},
		{"the release before it", releaseBeforeAuthor, false},
		{"a development build", devAuthorBase, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			spec := &task.Spec{Repo: "acme/other", Workflow: "svc", Author: &task.Person{Name: "someone", Email: "someone@example.com"}}
			env := &cloudEnv{lc: &localcfg.Config{BaseImages: map[string]string{"base": c.base}}}
			clone := true
			authorSkew(t.Context(), env, "slug", spec, &clone, &warn)
			if kept := spec.Author != nil; kept != c.kept {
				t.Fatalf("author = %+v, want kept = %v for a clone workflow on %s", spec.Author, c.kept, c.base)
			}
			if !c.kept && !strings.Contains(warn.String(), "not known to run fugaro "+authorSince) {
				t.Errorf("warn = %q, want the drop to say which field it dropped", warn.String())
			}
			if c.kept && warn.Len() != 0 {
				t.Errorf("warn = %q, want none for an image that reads the field", warn.String())
			}
		})
	}
}

// TestUpdateRepoRefusesGitAuthorOnOlderBaseImage is gitAuthorSince's gate at
// fugaro update --repo, the same shape as rigorSince's
// (TestUpdateRepoRefusesRigorOnOlderBaseImage): the key is decoded strictly by
// every release, so a runner older than the release that reads it dies at
// bootstrap on it, and update --repo refuses to deploy a job that would. The
// boundary is the constant's own: one release below is refused, the constant
// and one above pass, a base that is not a release only warns, and no base at
// all passes.
//
// The repository's half is the key being written (RepoGitAuthorSet), not the
// value it holds: git.author: fugaro is the key absent to this release and an
// explicit null decodes to fugaro too, while both are the same line to an older
// runner's decoder — a value test deploys a job that dies at bootstrap on a
// line the operator did write.
func TestUpdateRepoRefusesGitAuthorOnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, gitAuthorSince, -1), shiftRelease(t, gitAuthorSince, 1)
	older := "ghcr.io/dimipaun/fugaro-base:" + below
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	written := []*config.Config{
		{Git: config.Git{Author: config.GitAuthorLauncher}, RepoGitAuthorSet: true},
		{Git: config.Git{Author: config.GitAuthorFugaro}, RepoGitAuthorSet: true},
		{RepoGitAuthorSet: true},
	}
	for _, cfg := range written {
		err := r.gateGitAuthor(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg.Git.Author, err)
		}
		for _, want := range []string{
			"fugaro " + gitAuthorSince + " or later",
			older + ", release " + below,
			"fugaro update --base base",
			"fugaro.yaml's git.author of acme/app",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg.Git.Author, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + gitAuthorSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseGitAuthorSkew("acme/app", true, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseGitAuthorSkew("acme/app", true, devAuthorBase, &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no git.author asks nothing of the image:
	// the value alone, without the key, is every repository that never wrote
	// it, and the explicit fugaro a file may spell instead.
	for _, cfg := range []*config.Config{nil, {}, {Git: config.Git{Author: config.GitAuthorLauncher}}, {Git: config.Git{Author: config.GitAuthorFugaro}}} {
		if err := r.gateGitAuthor(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateGitAuthor(o, "acme/app", lc(older), written[0]); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateGitAuthor(pv, "acme/app", lc(older), written[0]); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// validateAuthor runs fugaro validate on a repository file whose git: block is
// gitBlock, in a directory that is no checkout and has no project config: the
// file's own key is all this judges. It is validateRigor's shape for a key that
// lives under git:.
func validateAuthor(t *testing.T, gitBlock string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\n" + gitBlock +
		"agent: { auth: api-key }\nworkflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatGitAuthorNeedsEveryJobImage: the file is valid — the
// skew is the installation's, not the file's — so validate warns rather than
// refusing, the shape rigorImageWarnings has, and names both ways to move an
// image (the installation's base, and a workflow's own). Every value counts,
// since it is the key an older runner refuses, and a file that writes none is
// warned about nothing.
func TestValidateWarnsThatGitAuthorNeedsEveryJobImage(t *testing.T) {
	for _, gitBlock := range []string{
		"git: { provider: github, base_branch: main, author: launcher }\n",
		"git: { provider: github, base_branch: main, author: fugaro }\n",
		"git: { provider: github, base_branch: main, author: }\n",
	} {
		doc, err := validateAuthor(t, gitBlock)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", gitBlock, err)
		}
		if !has(doc.Warnings, "git.author", "needs fugaro "+gitAuthorSince) {
			t.Errorf("%q: warnings = %+v, want the git.author warning", gitBlock, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "git.author", want) {
				t.Errorf("%q: the warning does not name %q: %+v", gitBlock, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "git.author", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", gitBlock, doc.Problems)
		}
	}
	if doc, err := validateAuthor(t, "git: { provider: github, base_branch: main }\n"); err != nil || has(doc.Warnings, "git.author", "") {
		t.Errorf("a file that writes no git.author was warned about: %+v, %v", doc.Warnings, err)
	}
}

// TestGitAuthorSinceIsTheReleaseTheChecklistNames holds gitAuthorSince and
// authorSince each to the half of docs/release.md's Before-you-tag bullet that
// states its own number — the reader a release verifies the constants against
// the tag actually cut. Each half is read apart from the other: the two
// constants name the same release today, so a check that read the number from
// the whole bullet would pass with either one wrong as long as the other was
// right, which is the mistake the two spellings invite.
func TestGitAuthorSinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `gitAuthorSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no gitAuthorSince bullet")
	}
	// The bullet's second half is where it turns to the task field the same
	// release carries, and its own constant with it.
	gitAuthorHalf, fieldHalf, ok := strings.Cut(bullet, "**The same release carries")
	if !ok {
		t.Fatal("docs/release.md's gitAuthorSince bullet no longer states the task field's own constant in a half of its own")
	}
	for _, c := range []struct{ name, half, release string }{
		{"gitAuthorSince", gitAuthorHalf, gitAuthorSince},
		{"authorSince", fieldHalf, authorSince},
	} {
		if !strings.Contains(c.half, "`"+c.name+"`") {
			t.Errorf("the half of docs/release.md's gitAuthorSince bullet about %s does not name the constant", c.name)
		}
		if !strings.Contains(c.half, "currently `\""+c.release+"\"`") {
			t.Errorf("the half of docs/release.md's gitAuthorSince bullet about %s does not carry its release (%q)", c.name, c.release)
		}
	}
}
