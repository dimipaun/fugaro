package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/issues"
)

// filedIssueURL is what the fake `gh` answers with: the shape `gh issue
// create` prints for this project's repository, and the only shape GH.Create
// records as this issue's (F28).
const filedIssueURL = "https://github.com/dimipaun/fugaro/issues/4242"

// issueSubmitConfig is the project config the submit tests select: a project
// with a name, a GCP project and one repository, which are the names the post
// is scrubbed of (F4, F5). It is the shape newCloudFixture writes, with the
// endpoints and the bucket URL left out — nothing in submit reads either.
const issueSubmitConfig = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
user: someone@example.com
repos:
  acme/widget: { provider: github, base_branch: main, workflows: [web] }
`

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

// ghSpy is the `gh` these tests file with: it records every call and answers
// `gh issue create` with filedIssueURL, so nothing here runs a binary and
// nothing reaches GitHub. lookPathErr and authErr are the two answers a
// machine without gh, and one whose gh is not logged in, give (spec 5).
type ghSpy struct {
	calls       [][]string
	lookPathErr error
	authErr     error
	url         string
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
	if err := store.Save(i); err != nil {
		t.Fatalf("saving: %v", err)
	}

	out, _, err := execute(t, "issue", "submit", i.LocalID)
	if err == nil {
		t.Fatalf("submit without gh exited 0:\n%s", out)
	}
	if got := len(spy.creates()); got != 0 {
		t.Errorf("a machine with no gh filed %d issue(s)", got)
	}
	if want := issues.InstallHint(runtime.GOOS, "ID=ubuntu\n"); !strings.Contains(out, want) {
		t.Errorf("the fallback does not say how to install gh (%q):\n%s", want, out)
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
	if !strings.Contains(out, "gh auth login") {
		t.Errorf("the fallback does not tell the person to log in:\n%s", out)
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
