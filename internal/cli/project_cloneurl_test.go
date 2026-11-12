package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// projectCloneURLBlock reads the selected project config's clone_urls block
// back through the strict loader, so a test asserts the file a publish reads
// rather than the bytes a command happened to write.
func projectCloneURLBlock(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("%s does not read back after the edit: %v", path, err)
	}
	return lc.CloneURLs
}

// TestProjectCloneURLSetWritesTheLocalBlock: the clone_urls block has one
// writer that is not the Firebase console (G3). It lands in the selected
// project's own config file, which is the file the publish reads, so a cluster
// nobody can reach with a browser can still be given its clone URLs.
func TestProjectCloneURLSetWritesTheLocalBlock(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	out, _, err := execute(t, "project", "clone-url", "set", "github/acme/app", "https://github.com/acme/app")
	if err != nil {
		t.Fatalf("project clone-url set: %v", err)
	}
	if got := projectCloneURLBlock(t, localPath)["github/acme/app"]; got != "https://github.com/acme/app" {
		t.Errorf("clone_urls[github/acme/app] = %q, want the URL that was set", got)
	}
	// The write reaches a run through the published node, and `fugaro update` is
	// the whole of what publishes it (the HARD RULE): the command names that one
	// command and no other step.
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not name the one command that publishes it:\n%s", out)
	}
	if strings.Contains(out, "policy publish") {
		t.Errorf("the write names a second command:\n%s", out)
	}
}

// TestProjectCloneURLSetKeepsWhatItDoesNotTouch: a hand-written file survives
// the write unchanged except for the entry the command wrote (§9), which is the
// byte-preservation property of the one editor (D790).
func TestProjectCloneURLSetKeepsWhatItDoesNotTouch(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "# the project's own file\nbase_branch: main\n")
	_, _, err := execute(t, "project", "clone-url", "set", "github/acme/app", "https://github.com/acme/app")
	if err != nil {
		t.Fatalf("project clone-url set: %v", err)
	}
	got := readFileString(t, localPath)
	if !strings.Contains(got, "# the project's own file") || !strings.Contains(got, "base_branch: main") {
		t.Errorf("the edit dropped what it does not touch:\n%s", got)
	}
	if !strings.Contains(got, "clone_urls:") {
		t.Errorf("the edit wrote no clone_urls block:\n%s", got)
	}
}

// TestProjectCloneURLRefusesAnUnknownCanonical: the entry is refused before
// anything is written, in the node's own words (projectpolicy.ValidCloneURLs,
// the rule the publish applies), because a file that carries an entry the node
// refuses is a file whose `fugaro update` fails — one broken installation, from
// one typo.
func TestProjectCloneURLRefusesAnUnknownCanonical(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	_, _, err := execute(t, "project", "clone-url", "set", "acme/app", "https://github.com/acme/app")
	if err == nil {
		t.Fatal("project clone-url set accepted a key that is not a canonical repository name")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if got := readFileString(t, localPath); got != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", got)
	}
}

// TestProjectCloneURLRefusesAURLThatIsNotThisRepository: the value is where a
// run clones from, so an entry pointing at another repository — or at another
// host — is refused by the node's rule before it is written rather than at the
// publish that would leave the installation unpublishable.
func TestProjectCloneURLRefusesAURLThatIsNotThisRepository(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	_, _, err := execute(t, "project", "clone-url", "set", "github/acme/app", "https://github.com/acme/other.git")
	if err == nil {
		t.Fatal("project clone-url set accepted a URL naming another repository")
	}
	if got := readFileString(t, localPath); got != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", got)
	}
}

// TestProjectCloneURLLsListsEveryCanonical: the read names every repository the
// block gives a URL, under the canonical name the runner looks it up by, so an
// operator can see what a publish would write without opening the policy node
// (spec §5.6).
func TestProjectCloneURLLsListsEveryCanonical(t *testing.T) {
	settingsWriteHomeWith(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n  github/acme/other: https://github.com/acme/other.git\n")
	out, _, err := execute(t, "project", "clone-url", "ls")
	if err != nil {
		t.Fatalf("project clone-url ls: %v", err)
	}
	for _, want := range []string{"github/acme/app", "https://github.com/acme/app", "github/acme/other", "https://github.com/acme/other.git"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls does not print %q:\n%s", want, out)
		}
	}
}

// TestProjectCloneURLShowIsTheSameRead: the family's read answers to both
// spellings the spec names (`ls|show`), and --json prints the same block as a
// document.
func TestProjectCloneURLShowIsTheSameRead(t *testing.T) {
	settingsWriteHomeWith(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n")
	out, _, err := execute(t, "project", "clone-url", "show", "--json")
	if err != nil {
		t.Fatalf("project clone-url show --json: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("show --json does not print a document:\n%s", out)
	}
	if !strings.Contains(out, "github/acme/app") {
		t.Errorf("show --json does not carry the block:\n%s", out)
	}
}

// TestProjectCloneURLRmRemovesOneEntryAndKeepsTheRest: removing the entry of
// one repository is not removing the block, and every other entry survives
// byte for byte.
func TestProjectCloneURLRmRemovesOneEntryAndKeepsTheRest(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n  github/acme/other: https://github.com/acme/other.git\n")
	if _, _, err := execute(t, "project", "clone-url", "rm", "github/acme/app"); err != nil {
		t.Fatalf("project clone-url rm: %v", err)
	}
	block := projectCloneURLBlock(t, localPath)
	if _, ok := block["github/acme/app"]; ok {
		t.Errorf("rm left the entry it removed: %v", block)
	}
	if got := block["github/acme/other"]; got != "https://github.com/acme/other.git" {
		t.Errorf("rm dropped another repository's entry: %v", block)
	}
}

// TestProjectCloneURLRmOfTheLastEntryWritesNothingAndSaysWhy: taking the last
// entry away is the project's statement that it gives no repository a URL, and
// that statement is an empty mapping — YAML's only spelling of one. This
// release writes no form of it (the settings plan gives that operation to the
// shared editor's own task), and a key left with no entry under it is one the
// loader reads as a file that says nothing about the block — which keeps the
// published node's entries, so the removal would be a no-op that printed a
// write. The command refuses instead, names the line that states it, and leaves
// the file exactly as it was.
func TestProjectCloneURLRmOfTheLastEntryWritesNothingAndSaysWhy(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n")
	before := readFileString(t, localPath)
	_, _, err := execute(t, "project", "clone-url", "rm", "github/acme/app")
	if err == nil {
		t.Fatal("project clone-url rm took the last entry away, and the block it leaves cannot be written")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	for _, want := range []string{"clone_urls: {}", localPath, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, localPath); got != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", got)
	}
	// And the entry is still the one a publish carries, which is the half the
	// operator checks: the node keeps it because the file still states it.
	if got := projectCloneURLBlock(t, localPath)["github/acme/app"]; got != "https://github.com/acme/app" {
		t.Errorf("clone_urls[github/acme/app] = %q after the refusal, want the entry that was there", got)
	}
}

// TestProjectCloneURLRefusesInAnAgentSessionEvenWithYes: the write is a project
// policy an agent session may not make, --yes included (SD9). It lands outside
// every repository, which is the half of the fence that refuses a session
// whatever the flag says.
func TestProjectCloneURLRefusesInAnAgentSessionEvenWithYes(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	t.Setenv("CLAUDECODE", "1")
	_, _, err := execute(t, "project", "clone-url", "set", "github/acme/app", "https://github.com/acme/app", "--yes")
	if err == nil {
		t.Fatal("project clone-url set wrote in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "coding agent") {
		t.Errorf("the refusal does not say it is a session: %v", err)
	}
	if got := readFileString(t, localPath); got != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", got)
	}
}
