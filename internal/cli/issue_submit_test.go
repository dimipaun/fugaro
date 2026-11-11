package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/issues"
)

// filedIssueURL is what the fake `gh` answers with: the shape `gh issue
// create` prints for this project's repository, and the only shape GH.Create
// records as this issue's (F28).
const filedIssueURL = "https://github.com/dimipaun/fugaro/issues/4242"

// issueSubmitConfig is the project config the submit tests select: a project
// with a name, a GCP project and one repository, which are the names the post
// is scrubbed of (F4, F5), and a runs bucket, whose URL's digest is the
// installation a record this run writes is stamped with (F8b). It is the shape
// newCloudFixture writes, with the endpoints left out — nothing in submit reads
// those.
const issueSubmitConfig = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
user: someone@example.com
repos:
  acme/widget: { provider: github, base_branch: main, workflows: [web] }
`

// issueSubmitBucket is the runs bucket URL the config above gives this run, and
// its digest is the installation this run stamps on what it files (F8b).
const issueSubmitBucket = "gs://unused-bucket"

// foreignBucket is another project's runs bucket on the same machine: its
// digest is the installation of a record this run does not file for.
const foreignBucket = "gs://another-project-bucket"

// issueSubmitState is the state a submit test runs in: the issue store in a
// directory of its own, and the project config of a project whose own names
// the post must lose. FUGARO_CONFIG names it, so what is selected does not
// depend on the directory the test binary happens to run in.
func issueSubmitState(t *testing.T) *issues.Store {
	t.Helper()
	path := isolateProjects(t, t.TempDir())
	if err := os.WriteFile(path, []byte(issueSubmitConfig), 0o600); err != nil {
		t.Fatalf("writing the project config: %v", err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	store, err := issues.OpenStore(os.Getenv, runtime.GOOS)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	return store
}

// issueSubmitStoreWithoutAProject is issueSubmitState with no project selected
// at all: the isolated environment and the store in it, and no config file
// anywhere, so selectedProjectConfig answers nil and this run has no names to
// scrub a post with (F4) and no installation of its own (F8b).
func issueSubmitStoreWithoutAProject(t *testing.T) *issues.Store {
	t.Helper()
	isolateProjects(t, t.TempDir())
	store, err := issues.OpenStore(os.Getenv, runtime.GOOS)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	return store
}

// ghSpy is the `gh` these tests file with: it records every call and answers
// `gh issue create` with filedIssueURL, so nothing here runs a binary and
// nothing reaches GitHub. lookPathErr and authErr are the two answers a
// machine without gh, and one whose gh is not logged in, give (spec 5).
type ghSpy struct {
	calls       [][]string
	lookPathErr error
	authErr     error
	// createErr is a `gh issue create` that failed: a machine that has gh and
	// an account on it, and no filing at the end of the call.
	createErr error
	url       string
	// posts is what the spy was handed, read out of the file `--body-file`
	// named while gh holds it: issues.GH removes that file before it returns,
	// so this is the only moment the bytes of the post exist.
	posts []ghPost
}

// ghPost is one issue the spy was asked to file.
type ghPost struct {
	title, body string
}

func (s *ghSpy) gh(t *testing.T) issues.GH {
	return issues.GH{
		LookPath: func(string) (string, error) {
			if s.lookPathErr != nil {
				return "", s.lookPathErr
			}
			return "/usr/bin/gh", nil
		},
		Run: func(_ context.Context, args ...string) ([]byte, error) {
			s.calls = append(s.calls, args)
			switch {
			case len(args) > 0 && args[0] == "auth":
				return nil, s.authErr
			case len(args) > 0 && args[0] == "issue":
				s.posts = append(s.posts, readPost(t, args))
				if s.createErr != nil {
					return nil, s.createErr
				}
				return []byte(s.url + "\n"), nil
			}
			return nil, fmt.Errorf("gh %s: not a call this fake answers", strings.Join(args, " "))
		},
	}
}

// installIssueGH points the command at the spy for this test.
func installIssueGH(t *testing.T, s *ghSpy) {
	t.Helper()
	old := issueGH
	issueGH = s.gh(t)
	t.Cleanup(func() { issueGH = old })
}

// installOSRelease pins what the command reads as /etc/os-release: the install
// hint is about the machine the command runs on, and this is the machine's own
// distribution replaced by the test's.
func installOSRelease(t *testing.T, content string) {
	t.Helper()
	old := issueOSRelease
	issueOSRelease = func() string { return content }
	t.Cleanup(func() { issueOSRelease = old })
}

// creates is the calls that filed an issue: the login check is not one.
func (s *ghSpy) creates() [][]string {
	var out [][]string
	for _, call := range s.calls {
		if len(call) > 0 && call[0] == "issue" {
			out = append(out, call)
		}
	}
	return out
}

// readPost is the title and the exact body bytes of one `gh issue create`
// call, read out of the file `--body-file` named while the call is in flight:
// what was filed, as opposed to what the terminal showed (F19).
func readPost(t *testing.T, args []string) ghPost {
	t.Helper()
	var p ghPost
	for i, a := range args {
		switch a {
		case "--title":
			p.title = args[i+1]
		case "--body-file":
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				t.Fatalf("reading the body gh was given: %v", err)
			}
			p.body = string(data)
		}
	}
	return p
}

// filed is the title and the exact body bytes of the nth issue this fake was
// given.
func (s *ghSpy) filed(t *testing.T, n int) (title, body string) {
	t.Helper()
	if n >= len(s.posts) {
		t.Fatalf("%d issue(s) were filed, want at least %d", len(s.posts), n+1)
	}
	return s.posts[n].title, s.posts[n].body
}

// TestIssueSubmitPreviewFilesNothing: `fugaro issue submit N` is a preview and
// not a filing (D1720). The exact post — its title, its body and the sha256 of
// the bytes — reaches the terminal, and neither `gh` nor the record is
// touched: the issue is still pending, with no URL.
func TestIssueSubmitPreviewFilesNothing(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	i.Summary = "the update stops half way"
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	out, _, err := execute(t, "issue", "submit", i.LocalID)
	if err != nil {
		t.Fatalf("issue submit: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("submit filed %d issue(s) with no --ask: the flag is the whole confirmation (D1720)", got)
	}
	for _, want := range []string{"Title: ", "Hash: ", i.Summary} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview does not show %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "Pass --ask"); got != 1 {
		t.Errorf("a preview says what to do about it %d time(s), want the run's one line:\n%s", got, out)
	}

	// A sweep prints every waiting issue and the one instruction once: the
	// preview is each issue's, the line about the flag is the run's.
	second := savedIssue("48214", issues.StatusPending, false)
	second.Summary = "the second one waiting"
	if err := store.Save(second); err != nil {
		t.Fatalf("saving the second issue: %v", err)
	}
	out, _, err = execute(t, "issue", "submit")
	if err != nil {
		t.Fatalf("issue submit sweep: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("the sweep filed %d issue(s) with no --ask", got)
	}
	for _, want := range []string{i.Summary, second.Summary} {
		if !strings.Contains(out, want) {
			t.Errorf("the sweep does not preview %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "Pass --ask"); got != 1 {
		t.Errorf("the sweep says what to do about the previews %d time(s), want once:\n%s", got, out)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" {
		t.Errorf("a preview left the issue %q with URL %q, want %q and none", back.Status, back.URL, issues.StatusPending)
	}
}

// TestIssueSubmitAskFilesPreviewedBytes: a yes at the owner's terminal files
// exactly the bytes that were previewed (F19), and the record keeps what
// happened: the URL GitHub answered with and the `submitted` status (spec 4).
// The body is read back out of the file `--body-file` named and hashed the way
// Post.Hash hashes it, so a post rebuilt between the preview and the filing —
// or a body that differs from the preview anywhere — is a failure here.
//
// The same run holds the other half of D1722: the summary and the diagnosis
// name the project and the repository, and what is filed does not carry either
// of them, nor the reporter and the run the record keeps (F4).
func TestIssueSubmitAskFilesPreviewedBytes(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	i.Summary = "the cache never refreshes: the acme/widget provider is stale"
	i.Diagnosis = "aurora's provider cache went stale"
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	out, _, err := executeStdin(t, "y\n", "issue", "submit", i.LocalID, "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	title, body := spy.filed(t, 0)
	sum := sha256.Sum256([]byte(title + "\n\n" + body))
	if want := "Hash: " + hex.EncodeToString(sum[:]); !strings.Contains(out, want) {
		t.Errorf("the bytes filed are not the bytes previewed: the preview does not carry %q:\n%s", want, out)
	}
	for _, gone := range []string{"aurora", "acme", "widget", i.Reporter, i.Run} {
		if strings.Contains(title+body, gone) {
			t.Errorf("the post to a public repository carries %q:\n%s", gone, title+body)
		}
	}
	for _, want := range []string{"the cache never refreshes", "provider cache went stale"} {
		if !strings.Contains(title+body, want) {
			t.Errorf("the scrubbed post dropped the finding's own words %q:\n%s", want, title+body)
		}
	}
	// And the names were scrubbed rather than dropped with the words around
	// them: the marker sits where the repository was named.
	if !strings.Contains(title+body, "[REDACTED]") {
		t.Errorf("the post carries no redaction marker, so no name was scrubbed:\n%s", title+body)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusSubmitted || back.URL != filedIssueURL {
		t.Errorf("the filed issue is %q with URL %q, want %q and %q", back.Status, back.URL, issues.StatusSubmitted, filedIssueURL)
	}
	// The record this run wrote names the project that wrote it (F8b): the
	// issues folder is one for the whole machine, and an issue filed under this
	// project is not one another project's `sync` uploads or its `mine` prints.
	if want := issues.Digest(issueSubmitBucket); back.Installation != want {
		t.Errorf("the filed record is stamped %q, want the project's installation %q", back.Installation, want)
	}
	if !strings.Contains(out, filedIssueURL) {
		t.Errorf("the run does not name the issue it filed:\n%s", out)
	}
}

// TestIssueSubmitAllSkipsSubmittedAndDismissed: `submit` with no reference goes
// through the issues that are still waiting and files nothing for the ones that
// are already filed or set aside (acceptance 3). The spy is the assertion: one
// issue is filed — the pending one, by its own words — whatever the terminal
// answers, because a skipped issue is never even asked about.
func TestIssueSubmitAllSkipsSubmittedAndDismissed(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	pending := savedIssue("48213", issues.StatusPending, false)
	pending.Summary = "the pending one"
	filed := savedIssue("48214", issues.StatusSubmitted, true)
	filed.Summary = "the already filed one"
	setAside := savedIssue("48215", issues.StatusDismissed, false)
	setAside.Summary = "the set-aside one"
	for _, i := range []issues.Issue{pending, filed, setAside} {
		if err := store.Save(i); err != nil {
			t.Fatalf("saving: %v", err)
		}
	}

	out, _, err := executeStdin(t, "y\ny\ny\n", "issue", "submit", "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 1 {
		t.Fatalf("submit filed %d issue(s), want the one that is pending: %v", got, spy.creates())
	}
	title, body := spy.filed(t, 0)
	if !strings.Contains(title+body, "the pending one") {
		t.Errorf("submit filed the wrong issue:\n%s", title+body)
	}
	for _, other := range []issues.Issue{filed, setAside} {
		if strings.Contains(title+body, other.Summary) {
			t.Errorf("submit filed %q, which is not pending:\n%s", other.Summary, title+body)
		}
		back, err := store.Get(other.LocalID)
		if err != nil {
			t.Fatalf("reading %s back: %v", other.LocalID, err)
		}
		if back.Status != other.Status || back.URL != other.URL {
			t.Errorf("a skipped issue became %q with URL %q, want %q and %q", back.Status, back.URL, other.Status, other.URL)
		}
	}
	// A reference that names an issue already on GitHub is refused rather than
	// filed a second time: the record says so, and no call is made.
	out, _, err = execute(t, "issue", "submit", "7")
	if err == nil || !strings.Contains(err.Error(), "already filed") {
		t.Fatalf("submit of a filed issue: err = %v, want the refusal naming it as filed", err)
	}
	if got := len(spy.creates()); got != 1 {
		t.Errorf("a refused submit filed %d issue(s), want the 1 that was pending", got)
	}
}

// TestIssueSubmitLeavesAnotherInstallationAlone: the issues folder is one for
// the whole machine, and a record that says it belongs to another project is
// never filed from here (F8b) — its words are this project's scrub list's
// other project's, so filing it would carry another team's machine straight to
// a public repository. The sweep steps over it without asking, and a reference
// that names it is refused rather than filed.
//
// The record that names no installation is the other half, and the more
// important one: it was saved by a build that had no field to stamp, it is the
// user's own issue, and it is filed — with this run's installation written on
// it, so it is this project's from now on.
func TestIssueSubmitLeavesAnotherInstallationAlone(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)

	mine := savedIssue("48213", issues.StatusPending, false)
	mine.Summary = "this project's own"
	mine.Installation = issues.Digest(issueSubmitBucket)
	other := savedIssue("48214", issues.StatusPending, false)
	other.Summary = "another project's own"
	other.Installation = issues.Digest(foreignBucket)
	unstamped := savedIssue("48215", issues.StatusPending, false)
	unstamped.Summary = "saved before there was a field"
	for _, i := range []issues.Issue{mine, other, unstamped} {
		if err := store.Save(i); err != nil {
			t.Fatalf("saving: %v", err)
		}
	}

	out, _, err := executeStdin(t, "y\ny\ny\n", "issue", "submit", "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 2 {
		t.Fatalf("the sweep filed %d issue(s), want the two that are this project's: %v\n%s", got, spy.creates(), out)
	}
	for n, want := range []string{mine.Summary, unstamped.Summary} {
		if _, body := spy.filed(t, n); !strings.Contains(body, want) {
			t.Errorf("the sweep filed the wrong issue at %d, want the one carrying %q:\n%s", n, want, body)
		}
	}
	if strings.Contains(out, other.Summary) {
		t.Errorf("the sweep previewed another installation's issue:\n%s", out)
	}
	back, err := store.Get(other.LocalID)
	if err != nil {
		t.Fatalf("reading the other installation's issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" || back.Installation != other.Installation {
		t.Errorf("the other installation's issue became %q with URL %q and installation %q, want it untouched", back.Status, back.URL, back.Installation)
	}
	if back, err := store.Get(unstamped.LocalID); err != nil {
		t.Fatalf("reading the unstamped issue back: %v", err)
	} else if want := issues.Digest(issueSubmitBucket); back.Installation != want {
		t.Errorf("a filed record that named no installation is stamped %q, want %q", back.Installation, want)
	}

	// Named on its own, it is refused in a sentence of its own rather than
	// previewed and offered: the person is told whose it is.
	out, _, err = execute(t, "issue", "submit", other.LocalID)
	if err == nil || !strings.Contains(err.Error(), "another installation") {
		t.Fatalf("submit of another installation's issue: err = %v, want the refusal naming it\n%s", err, out)
	}
	if strings.Contains(out, other.Summary) {
		t.Errorf("a refused submit previewed the other installation's issue:\n%s", out)
	}
	if got := len(spy.creates()); got != 2 {
		t.Errorf("a refused submit filed %d issue(s), want the 2 that were this project's", got)
	}
}

// TestIssueSubmitWithNoProjectSelectedLeavesStampedIssuesAlone: a run in a
// directory that selects no project has no names to scrub a post with (F4) and
// no installation of its own (F8b), so a record a project stamped is the one
// thing it must not file: that project's name, GCP project and repository names
// would reach a public repository as they are, which is the whole of what the
// scrub and the stamp are for (D1722). The sweep steps over it, a reference
// that names it is refused with what to do about it, and the record is
// untouched.
//
// The record that names no installation is the other half, and the one this
// command exists for in a directory like this: nobody's config wrote it, it is
// the user's own issue, and it is filed — with no names to scrub, which is the
// best this run can do and all it claims.
func TestIssueSubmitWithNoProjectSelectedLeavesStampedIssuesAlone(t *testing.T) {
	store := issueSubmitStoreWithoutAProject(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)

	stamped := savedIssue("48213", issues.StatusPending, false)
	stamped.Summary = "the one a project wrote"
	stamped.Installation = issues.Digest(issueSubmitBucket)
	unstamped := savedIssue("48214", issues.StatusPending, false)
	unstamped.Summary = "the one saved before there was a field"
	for _, i := range []issues.Issue{stamped, unstamped} {
		if err := store.Save(i); err != nil {
			t.Fatalf("saving: %v", err)
		}
	}

	out, _, err := executeStdin(t, "y\ny\ny\n", "issue", "submit", "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 1 {
		t.Fatalf("the sweep filed %d issue(s), want the one that names no installation: %v\n%s", got, spy.creates(), out)
	}
	if _, body := spy.filed(t, 0); !strings.Contains(body, unstamped.Summary) {
		t.Errorf("the sweep filed the wrong issue, want the one saved before there was a field:\n%s", body)
	}
	if strings.Contains(out, stamped.Summary) {
		t.Errorf("a run with no project selected previewed a record a project stamped:\n%s", out)
	}
	back, err := store.Get(stamped.LocalID)
	if err != nil {
		t.Fatalf("reading the stamped record back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" || back.Installation != stamped.Installation {
		t.Errorf("the stamped record became %q with URL %q and installation %q, want it untouched", back.Status, back.URL, back.Installation)
	}

	// Named on its own it is refused, and the refusal says what this run cannot
	// do and who can: the project it belongs to is the one holding the names.
	out, _, err = execute(t, "issue", "submit", stamped.LocalID)
	if err == nil || !strings.Contains(err.Error(), "another installation") {
		t.Fatalf("submit of a record a project stamped, with none selected: err = %v, want the refusal naming it\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "select the project it belongs to") {
		t.Errorf("the refusal does not say what to do about it: %q", err.Error())
	}
	if strings.Contains(out, stamped.Summary) {
		t.Errorf("a refused submit previewed the record a project stamped:\n%s", out)
	}
	if got := len(spy.creates()); got != 1 {
		t.Errorf("a refused submit filed %d issue(s), want the 1 that names no installation", got)
	}
}

// TestIssueSubmitWithoutGhPrintsFallback: with no `gh` on PATH nothing is
// filed, the machine is told how to get it, and the person is handed the
// new-issue link with the post already in it and the command that prints the
// whole issue here (spec 5, F16, F15). The run exits non-zero: an issue that
// was not filed is not a success.
func TestIssueSubmitWithoutGhPrintsFallback(t *testing.T) {
	store := issueSubmitState(t)
	spy := &ghSpy{lookPathErr: errors.New(`exec: "gh": executable file not found in $PATH`), url: filedIssueURL}
	installIssueGH(t, spy)
	// The distribution is pinned, so what the hint is built from is this
	// test's and not the machine's: on Linux the line it answers with is the
	// package manager's own.
	installOSRelease(t, "ID=ubuntu\n")
	i := savedIssue("48213", issues.StatusPending, false)
	second := savedIssue("48214", issues.StatusPending, false)
	second.Summary = "the second one waiting"
	for _, one := range []issues.Issue{i, second} {
		if err := store.Save(one); err != nil {
			t.Fatalf("saving: %v", err)
		}
	}

	out, _, err := execute(t, "issue", "submit")
	if err == nil {
		t.Fatalf("submit without gh exited 0:\n%s", out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a machine with no gh filed %d issue(s)", got)
	}
	// The one thing that is wrong is said once, however many issues the run
	// would have filed: five waiting issues are five links and one line about
	// gh, the shape setup's own gh hint is printed in.
	if want := issues.InstallHint(runtime.GOOS, "ID=ubuntu\n"); !strings.Contains(out, want) {
		t.Errorf("the fallback does not say how to install gh (%q):\n%s", want, out)
	} else if got := strings.Count(out, want); got != 1 {
		t.Errorf("the fallback says how to install gh %d time(s), want once:\n%s", got, out)
	}
	if got := strings.Count(out, "fugaro issue show "+i.LocalID); got != 1 {
		t.Errorf("the fallback names the command that prints the whole issue %d time(s), want once per issue:\n%s", got, out)
	}
	if !strings.Contains(out, "fugaro issue show "+second.LocalID) {
		t.Errorf("the fallback prints no reference for the second issue it would have filed:\n%s", out)
	}
	if !strings.Contains(out, "https://github.com/dimipaun/fugaro/issues/new?title=") {
		t.Errorf("the fallback prints no new-issue link:\n%s", out)
	}
	if !strings.Contains(out, "fugaro issue show "+i.LocalID) {
		t.Errorf("the fallback does not name the command that prints the whole issue:\n%s", out)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" {
		t.Errorf("a fallback left the issue %q with URL %q, want %q and none", back.Status, back.URL, issues.StatusPending)
	}
}

// TestIssueSubmitNotLoggedInPrintsLogin: an installed `gh` with no account on
// this host is the other half of spec 5. The person is told to log in rather
// than to install anything, the link and the reference are printed beside it,
// and nothing is filed — the check names the host the post goes to, so a login
// elsewhere is not an answer (ghHost).
func TestIssueSubmitNotLoggedInPrintsLogin(t *testing.T) {
	store := issueSubmitState(t)
	spy := &ghSpy{authErr: errors.New("gh auth status --hostname github.com: not logged in"), url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	out, _, err := execute(t, "issue", "submit", i.LocalID)
	if err == nil {
		t.Fatalf("submit with no login exited 0:\n%s", out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a machine with no login filed %d issue(s)", got)
	}
	if got := strings.Count(out, "gh auth login"); got != 1 {
		t.Errorf("the fallback tells the person to log in %d time(s), want once:\n%s", got, out)
	}
	if strings.Contains(out, issues.GHInstallPage) {
		t.Errorf("a gh that is installed is told how to install it:\n%s", out)
	}
	if !strings.Contains(out, "fugaro issue show "+i.LocalID) {
		t.Errorf("the fallback does not name the command that prints the whole issue:\n%s", out)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" {
		t.Errorf("a fallback left the issue %q with URL %q, want %q and none", back.Status, back.URL, issues.StatusPending)
	}
}

// TestIssueSubmitRefusesInAgentSession: a coding agent's session does not file
// (F10). The guard is the one `dismiss` and `recipes publish` use — the marker
// with a terminal, asked even where one is present — the refusal is the agent
// one and not a failure to find the issue, and the record is untouched.
func TestIssueSubmitRefusesInAgentSession(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	t.Setenv("CLAUDECODE", "1")
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	_, _, err := executeStdin(t, "y\n", "issue", "submit", i.LocalID, "--ask")
	if err == nil || !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Fatalf("submit --ask in a coding agent's session: err = %v, want the agent refusal", err)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a coding agent's session filed %d issue(s)", got)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" {
		t.Errorf("a refused submit left the issue %q with URL %q, want %q and none", back.Status, back.URL, issues.StatusPending)
	}
}

// TestIssueSubmitAgentSessionStillPreviewsItsOwnProject: what a coding agent's
// session is refused is the write, and not the read (F10). The session reaches
// no store, but the names the post is scrubbed of are the local config and the
// checkout, so a plain `submit` there previews what a person's would — the
// project's own names taken out (F4) — and a record this project stamped is
// still this project's own rather than another project's (F8b, issueForeign):
// a session that resolved no project would refuse the user's own issue as
// somebody else's.
func TestIssueSubmitAgentSessionStillPreviewsItsOwnProject(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, true)
	t.Setenv("CLAUDECODE", "1")
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	i.Summary = "the acme/widget upload fails"
	i.Installation = issues.Digest(issueSubmitBucket)
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	out, _, err := execute(t, "issue", "submit", i.LocalID)
	if err != nil {
		t.Fatalf("a coding agent's session previewing its own project's issue: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a coding agent's session filed %d issue(s)", got)
	}
	if !strings.Contains(out, "the [REDACTED] upload fails") {
		t.Errorf("a coding agent's session previewed the project's own names:\n%s", out)
	}
	for _, name := range []string{"acme", "widget", "aurora", "proj-1234"} {
		if strings.Contains(out, name) {
			t.Errorf("the preview carries %q, which the post is scrubbed of (F4):\n%s", name, out)
		}
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" || back.Installation != i.Installation {
		t.Errorf("a preview left the issue %q with URL %q and installation %q, want it untouched", back.Status, back.URL, back.Installation)
	}
}

// TestIssueSubmitAskRefusesWithoutATerminal: `--ask` at a pipe asks nothing and
// files nothing — a confirmation nobody can answer is a refusal and never a
// silent proceed — and the refusal is this command's own line rather than the
// managed gate's (F24). Without the flag the same pipe is the preview it always
// was: the policy asks nothing of a command whose default is to do nothing.
func TestIssueSubmitAskRefusesWithoutATerminal(t *testing.T) {
	store := issueSubmitState(t)
	withTerminal(t, false)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	i := savedIssue("48213", issues.StatusPending, false)
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	_, _, err := executeStdin(t, "y\n", "issue", "submit", i.LocalID, "--ask")
	if err == nil {
		t.Fatal("--ask read a yes from a pipe and filed an issue")
	}
	if refusal := err.Error(); !strings.Contains(refusal, "terminal") {
		t.Errorf("the refusal does not say what a confirmation needs: %q", refusal)
	}
	if refusal := err.Error(); strings.Contains(refusal, "managed project") {
		t.Errorf("the refusal is the managed gate's sentence, which is about another command entirely: %q", refusal)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("--ask with no terminal filed %d issue(s)", got)
	}

	out, _, err := executeStdin(t, "", "issue", "submit", i.LocalID)
	if err != nil {
		t.Fatalf("submit with no --ask at a pipe: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("submit with no --ask at a pipe filed %d issue(s)", got)
	}
	back, err := store.Get(i.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Status != issues.StatusPending || back.URL != "" {
		t.Errorf("a refused submit left the issue %q with URL %q, want %q and none", back.Status, back.URL, issues.StatusPending)
	}
}

// submitRigIssue is the state `submit --ask` starts from for an issue the store
// already has a number for: one record in the shared store under the verified
// reporter's name, and this machine's own copy of it, numbered by the upload the
// way SyncPending numbers a file (F13). The copy names this project, because
// this is the machine that uploaded it (F8b).
func submitRigIssue(t *testing.T, rig *mineRig, signature string) issues.Issue {
	t.Helper()
	i := mineRec("48213", signature)
	up := rig.upload(i, rig.caller)
	local := i
	local.Number, local.Uploaded = up.Number, true
	if err := rig.store.Save(local); err != nil {
		t.Fatalf("saving this machine's copy: %v", err)
	}
	return local
}

// sharedClaim is the claim a shared record is under, read out of the record's
// own object: the claim lives inside the record it is about (F22), and the
// store's exported reads answer with the record alone. A nil claim is an issue
// nobody is filing.
func sharedClaim(t *testing.T, b *blobx.Bucket, number int) *issues.Lease {
	t.Helper()
	var doc struct {
		Claim *issues.Lease `json:"claim"`
	}
	if err := json.Unmarshal(sharedObject(t, b, number), &doc); err != nil {
		t.Fatalf("decoding shared issue %d: %v", number, err)
	}
	return doc.Claim
}

// sharedObject is the store's own object for one issue, as it is on the bucket
// (F24): the record's text and, while somebody is filing it, the claim.
func sharedObject(t *testing.T, b *blobx.Bucket, number int) []byte {
	t.Helper()
	raw, _, err := b.Read(context.Background(), fmt.Sprintf("issues/records/%04d", number))
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", number, err)
	}
	return raw
}

// rewriteSharedSummary is another launcher's client editing one shared issue,
// applied straight to the store's object (F24): the record's own text comes
// back out of the object, the summary of it is the one thing changed, and the
// object is written as it was otherwise — a valid record of the same issue with
// other bytes.
func rewriteSharedSummary(t *testing.T, b *blobx.Bucket, number int, summary string) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(sharedObject(t, b, number), &doc); err != nil {
		t.Fatalf("decoding the shared object of issue %d: %v", number, err)
	}
	var text string
	if err := json.Unmarshal(doc["record"], &text); err != nil {
		t.Fatalf("reading the record of shared issue %d: %v", number, err)
	}
	i, err := issues.Unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("parsing the record of shared issue %d: %v", number, err)
	}
	i.Summary = summary
	record, err := issues.Marshal(i)
	if err != nil {
		t.Fatalf("rewriting the record of shared issue %d: %v", number, err)
	}
	if doc["record"], err = json.Marshal(string(record)); err != nil {
		t.Fatalf("encoding the record of shared issue %d: %v", number, err)
	}
	object, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the shared object of issue %d: %v", number, err)
	}
	if err := b.Put(context.Background(), fmt.Sprintf("issues/records/%04d", number), object, "application/json"); err != nil {
		t.Fatalf("writing shared issue %d: %v", number, err)
	}
}

// TestIssueSubmitRefusesWhenClaimedByAnother: two shells filing one issue file
// it once (F22, check 451). The claim another submitter holds is refused before
// anything is filed — with who holds it and until when — so the second one
// never reaches `gh`, and the claim it did not take is left as it was.
func TestIssueSubmitRefusesWhenClaimedByAnother(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-claimed")

	other := "user:bo@example.test"
	if _, err := issues.Claim(context.Background(), rig.bucket, local.Number, other, rig.now); err != nil {
		t.Fatalf("another submitter's claim: %v", err)
	}
	held := sharedClaim(t, rig.bucket, local.Number)
	if held == nil {
		t.Fatal("the other submitter's claim is not in the record")
	}

	_, _, err := executeStdin(t, "y\n", "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err == nil {
		t.Fatal("submit filed an issue another submitter holds the claim on")
	}
	if !strings.Contains(err.Error(), other) {
		t.Errorf("the refusal does not name who is filing it: %q", err.Error())
	}
	if want := held.ExpiresAt.UTC().Format(time.RFC3339); !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not say until when (%s): %q", want, err.Error())
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a refused submit made %d filing call(s), want none", got)
	}
	if c := sharedClaim(t, rig.bucket, local.Number); c == nil || c.Holder != other || !c.ExpiresAt.Equal(held.ExpiresAt) {
		t.Errorf("the refused submit left the claim as %+v, want the other submitter's %+v", c, held)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusPending || shared.URL != "" {
		t.Errorf("a refused submit left the shared issue %q with URL %q, want it pending with none", shared.Status, shared.URL)
	}
}

// TestIssueSubmitClaimsOnlyAfterYes: the claim is taken after the yes and never
// before it (F28). A prompt answered "no" — or one left open — holds nothing,
// so an issue nobody is filing is never made to wait for a lease, and the next
// submitter takes it at once.
func TestIssueSubmitClaimsOnlyAfterYes(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-no")

	out, _, err := executeStdin(t, "n\n", "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing was filed for") {
		t.Errorf("a no does not say that nothing was filed:\n%s", out)
	}
	if c := sharedClaim(t, rig.bucket, local.Number); c != nil {
		t.Errorf("a no took the claim %+v, want none: the claim is taken after the yes (F28)", c)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a no filed %d issue(s)", got)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusPending || shared.URL != "" {
		t.Errorf("a no left the shared issue %q with URL %q, want it pending with none", shared.Status, shared.URL)
	}
}

// TestIssueSubmitRecordsURLInSharedStore: the filing is recorded where the team
// reads it (F22, spec 4). The store's own record of the issue carries the URL,
// `submitted` and the instant it was filed, in the one write conditioned on
// still holding the claim — and that write is the one that gives the claim back,
// so the issue is nobody's to file from then on.
func TestIssueSubmitRecordsURLInSharedStore(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-filed")

	out, _, err := executeStdin(t, "y\n", "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 1 {
		t.Fatalf("submit filed %d issue(s), want one", got)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusSubmitted || shared.URL != filedIssueURL || !shared.Uploaded {
		t.Errorf("the shared issue is %q with URL %q (uploaded %t), want %q with %q", shared.Status, shared.URL, shared.Uploaded, issues.StatusSubmitted, filedIssueURL)
	}
	if shared.SubmittedAt == nil || !shared.SubmittedAt.UTC().Equal(rig.now) {
		t.Errorf("the shared issue was filed at %v, want the run's own clock %s", shared.SubmittedAt, rig.now)
	}
	if c := sharedClaim(t, rig.bucket, local.Number); c != nil {
		t.Errorf("the filing left the claim %+v on the issue, want none", c)
	}
	back, err := rig.store.Get(strconv.Itoa(local.Number))
	if err != nil {
		t.Fatalf("reading this machine's copy back: %v", err)
	}
	if back.Status != issues.StatusSubmitted || back.URL != filedIssueURL {
		t.Errorf("this machine's copy is %q with URL %q, want %q with %q", back.Status, back.URL, issues.StatusSubmitted, filedIssueURL)
	}
}

// TestIssueSubmitReleasesClaimOnGhFailure: a post that failed gives the claim
// back (F22). The record keeps no claim and no URL: nobody is made to wait a
// lease for a filing that did not happen, and the next run claims it and tries
// again.
func TestIssueSubmitReleasesClaimOnGhFailure(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL, createErr: errors.New("gh: could not create the issue")}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-failed")

	_, _, err := executeStdin(t, "y\n", "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err == nil {
		t.Fatal("submit exited 0 after gh refused to file the issue")
	}
	if !strings.Contains(err.Error(), "was not filed") {
		t.Errorf("a failed post is not reported as one: %q", err.Error())
	}
	if c := sharedClaim(t, rig.bucket, local.Number); c != nil {
		t.Errorf("a failed post left the claim %+v on the issue, want it given back", c)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusPending || shared.URL != "" || shared.SubmittedAt != nil {
		t.Errorf("a failed post left the shared issue %q with URL %q and filed at %v, want it pending with neither", shared.Status, shared.URL, shared.SubmittedAt)
	}
}

// rewriteBeforeYes is a terminal that edits the shared issue and then answers
// yes: another launcher's client rewriting the record between the preview and
// the confirmation (F24), which is the window F19 is about.
type rewriteBeforeYes struct {
	t       *testing.T
	bucket  *blobx.Bucket
	number  int
	summary string
	done    bool
}

func (r *rewriteBeforeYes) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	rewriteSharedSummary(r.t, r.bucket, r.number, r.summary)
	return copy(p, "y\n"), nil
}

// TestIssueSubmitRefusesChangedRecord: the bytes filed are the bytes previewed
// (F19). A record another submitter changed between the preview and the yes is
// refused rather than filed unseen under this person's account, nothing reaches
// `gh`, and the claim the run took on its way to that comparison is given back.
func TestIssueSubmitRefusesChangedRecord(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-changed")

	stdin := &rewriteBeforeYes{t: t, bucket: rig.bucket, number: local.Number, summary: "a teammate rewrote this while you read"}
	_, _, err := executeStdinReader(t, stdin, "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err == nil {
		t.Fatal("submit filed a record that changed after the preview")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Errorf("the refusal does not say the record changed: %q", err.Error())
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a refused submit made %d filing call(s), want none", got)
	}
	if c := sharedClaim(t, rig.bucket, local.Number); c != nil {
		t.Errorf("the refusal left the claim %+v on the issue, want it given back", c)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Summary != stdin.summary {
		t.Errorf("the shared issue reads %q, want the teammate's own text %q", shared.Summary, stdin.summary)
	}
	if shared.Status != issues.StatusPending || shared.URL != "" {
		t.Errorf("a refused submit left the shared issue %q with URL %q, want it pending with none", shared.Status, shared.URL)
	}
}

// sharedRows is every issue the project's store holds, as the store's own
// listing reads them: what an upload would have put there, and nothing else.
// The false it answers with is a failure of the test's own store.
func sharedRows(t *testing.T, b *blobx.Bucket) []issues.SharedRow {
	t.Helper()
	rows, err := issues.ListShared(context.Background(), b)
	if err != nil {
		t.Fatalf("listing the shared store: %v", err)
	}
	return rows
}

// TestIssueSubmitPreviewUploadsNothing: a plain `submit` is a read (D1720). The
// issue it previews stays in this machine's own folder — unnumbered, unuploaded,
// pending — and the project's store is untouched by it, which is the promise the
// run's own line makes: "Nothing was filed" is said by a run that put nothing up
// either. The upload is --ask's, and only --ask's (F10, F22).
func TestIssueSubmitPreviewUploadsNothing(t *testing.T) {
	rig := newMineRig(t)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := rig.save(mineRec("48213", "sig-preview"))

	out, _, err := execute(t, "issue", "submit")
	if err != nil {
		t.Fatalf("issue submit: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing was filed") {
		t.Errorf("a preview does not say that nothing was filed:\n%s", out)
	}
	if rows := sharedRows(t, rig.bucket); len(rows) != 0 {
		t.Errorf("a preview uploaded %d issue(s) to the project's store, want none: %+v", len(rows), rows)
	}
	back, err := rig.store.Get(local.LocalID)
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if back.Number != 0 || back.Uploaded {
		t.Errorf("a preview numbered this machine's record %d (uploaded %t), want it left as it is", back.Number, back.Uploaded)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a preview filed %d issue(s)", got)
	}
}

// TestIssueSubmitAgentSessionRefusesBeforeAnyPreview: the refusal a coding
// agent's session gets comes before anything is shown or written (F10). `--ask`
// is the half of the command that uploads and files, so the guard is answered
// first: no preview reaches the terminal, and the pending issue that --ask would
// have uploaded is still only in this machine's folder.
func TestIssueSubmitAgentSessionRefusesBeforeAnyPreview(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	t.Setenv("CLAUDECODE", "1")
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := rig.save(mineRec("48213", "sig-agent"))

	out, _, err := executeStdin(t, "y\n", "issue", "submit", local.LocalID, "--ask")
	if err == nil || !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Fatalf("submit --ask in a coding agent's session: err = %v, want the agent refusal", err)
	}
	if strings.Contains(out, "Title:") {
		t.Errorf("the refusal printed a preview of the issue, want nothing but the refusal:\n%s", out)
	}
	if rows := sharedRows(t, rig.bucket); len(rows) != 0 {
		t.Errorf("a refused session uploaded %d issue(s), want none: %+v", len(rows), rows)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a refused session filed %d issue(s)", got)
	}
}

// TestIssueSubmitSweepLeavesASetAsideIssueAlone: a teammate's dismissal is the
// store's and nowhere else — `dismiss` writes the status on the machine that ran
// it, and nothing copies it into this machine's folder, which still says
// `pending` (F13). The sweep reads the store's copy of a numbered issue and
// steps over a decided one, so a dismissal is never filed by accident; naming
// the issue on its own still files it, because a reference is the person's own
// decision and a dismissal only says it was not worth filing then.
func TestIssueSubmitSweepLeavesASetAsideIssueAlone(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-set-aside")
	if err := issues.SetSharedStatus(context.Background(), rig.bucket, local.Number, issues.StatusDismissed, rig.now); err != nil {
		t.Fatalf("setting the shared issue aside: %v", err)
	}

	out, _, err := executeStdin(t, "y\n", "issue", "submit", "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing is pending") {
		t.Errorf("the sweep does not report the set-aside issue as nothing to do:\n%s", out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("the sweep filed %d issue(s), want the set-aside one left alone", got)
	}
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusDismissed || shared.URL != "" {
		t.Errorf("the sweep left the shared issue %q with URL %q, want it %q and unfiled", shared.Status, shared.URL, issues.StatusDismissed)
	}

	// Named on its own the same issue is filed: the reference overrides the
	// dismissal, and the record the run files is the store's own copy of it.
	out, _, err = executeStdin(t, "y\n", "issue", "submit", strconv.Itoa(local.Number), "--ask")
	if err != nil {
		t.Fatalf("issue submit --ask of a named set-aside issue: %v\n%s", err, out)
	}
	if got := len(spy.creates()); got != 1 {
		t.Fatalf("a named set-aside issue was filed %d time(s), want once", got)
	}
	shared, err = issues.SharedIssue(context.Background(), rig.bucket, local.Number)
	if err != nil {
		t.Fatalf("reading the shared issue back: %v", err)
	}
	if shared.Status != issues.StatusSubmitted || shared.URL != filedIssueURL {
		t.Errorf("the filed issue is %q with URL %q, want %q with %q", shared.Status, shared.URL, issues.StatusSubmitted, filedIssueURL)
	}
}

// TestIssueSubmitRefusesAnIssueTheStoreAlreadyFiled: the store's copy of a
// numbered issue is the one this run would file, so its status is what says
// whether there is anything left to file (F13, F28). Another machine filed the
// issue and this machine's own file still says `pending`; a reference that names
// it is refused before the preview, so nobody reads a post that will never be
// made, and nothing reaches `gh` or the record.
func TestIssueSubmitRefusesAnIssueTheStoreAlreadyFiled(t *testing.T) {
	rig := newMineRig(t)
	withTerminal(t, true)
	spy := &ghSpy{url: filedIssueURL}
	installIssueGH(t, spy)
	local := submitRigIssue(t, rig, "sig-filed-elsewhere")

	c, err := issues.Claim(context.Background(), rig.bucket, local.Number, "user:bo@example.test", rig.now)
	if err != nil {
		t.Fatalf("another submitter's claim: %v", err)
	}
	if err := issues.RecordSubmitted(context.Background(), rig.bucket, c, filedIssueURL, rig.now); err != nil {
		t.Fatalf("another machine's filing: %v", err)
	}
	back, err := rig.store.Get(strconv.Itoa(local.Number))
	if err != nil {
		t.Fatalf("reading this machine's copy back: %v", err)
	}
	if back.Status != issues.StatusPending {
		t.Fatalf("this machine's copy of the issue is %q, want the %q it was saved as: the file is the half that is not refreshed", back.Status, issues.StatusPending)
	}

	out, _, err := execute(t, "issue", "submit", strconv.Itoa(local.Number))
	if err == nil {
		t.Fatalf("submit previewed an issue the project's own record says is filed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "submitted") {
		t.Errorf("the refusal does not say what the store's record says: %q", err.Error())
	}
	if strings.Contains(out, "Title:") {
		t.Errorf("a refused submit printed a preview:\n%s", out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a refused submit filed %d issue(s)", got)
	}
}
