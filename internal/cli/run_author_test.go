package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
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
// launcher) gets a task carrying the launcher's own identity, taken from the
// local config's user: — the name being git's user.name, else the address's
// local part (git has no user.name in these fixtures, so it is the local
// part).
func TestRunAuthorFromLocalConfig(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261030-100000-abcd")
	want := &task.Person{Name: "someone", Email: "someone@example.com"}
	if spec.Author == nil || *spec.Author != *want {
		t.Fatalf("author = %+v, want %+v: the launch recorded no author for a repository that opted in", spec.Author, want)
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
func TestRunAuthorGitAuthorKeyGatedForOlderRunner(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseBeforeAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	_, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	for _, want := range []string{"git.author", "the git.author key", "fugaro " + gitAuthorSince + " or later", "release 0.13.0", "fugaro image refresh"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestGitAuthorSinceIsTheReleaseTheChecklistNames holds gitAuthorSince to the
// reader that states its number: docs/release.md's Before-you-tag bullet,
// which is where a release verifies the constant against the tag actually cut,
// and which carries the field's own constant (authorSince) in the same bullet.
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
	if !strings.Contains(bullet, "currently `\""+gitAuthorSince+"\"`") {
		t.Errorf("docs/release.md's gitAuthorSince bullet does not carry the constant's release (%q)", gitAuthorSince)
	}
	if !strings.Contains(bullet, "`authorSince`") || !strings.Contains(bullet, "currently `\""+authorSince+"\"`") {
		t.Errorf("docs/release.md's gitAuthorSince bullet does not carry the task field's own constant and release (%q): the two gates ship together", authorSince)
	}
}
