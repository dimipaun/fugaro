package issues

// This file is the `gh` half of submitting: the three questions `fugaro issue
// submit` asks about the person's own GitHub CLI — is it installed, is it
// logged in, file this — and the line the fallback prints when the answer to
// the first is no (F15). Nothing here reaches the network by itself: `gh`
// does, with the person's own credentials, and every command goes through an
// injected runner so a test runs no binary (the seam internal/billing uses for
// gcloud).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// GHBin is the binary every command and every hint here names.
const GHBin = "gh"

// GHRepo is the repository the post goes to, in the one form `-R` takes that
// names the host as well as the slug (F22). It is built from updatecheck.Repo,
// so the repository a post goes to and the repository the updater knows cannot
// drift apart.
const GHRepo = "github.com/" + updatecheck.Repo

// ghTimeout bounds one `gh issue create`. The call goes to GitHub on a
// connection this machine does not control, and a call that never returns
// would hold the issue's claim until its lease lapses (F24): this is well
// inside claimLease, and long enough for a slow connection and a large body.
const ghTimeout = 5 * time.Minute

// ghStatusTimeout bounds `gh auth status`, a question about this machine that
// must answer at once: it reads the person's own configuration and asks
// nothing, so a wait here is a wait on a hung process, not on a network.
const ghStatusTimeout = 15 * time.Second

// GHRunner runs gh with args and returns what it wrote to stdout. It is the
// whole seam: a test replaces it and no binary is run, and the real one is
// ExecGH.
type GHRunner func(ctx context.Context, args ...string) ([]byte, error)

// ExecGH runs gh with args as a child process. The answer is returned rather
// than printed, and stderr — gh's own words about a refusal — is folded into
// the error, which a caller prints as the reason nothing was filed.
func ExecGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, GHBin, args...)
	cmd.Env = ghEnv(cmd.Environ())
	// A leaked descendant holding the output pipe must not hang us (see
	// internal/image's runner, which waits the same way).
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil &&
		cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// gh itself exited 0 and had already printed the issue URL; WaitDelay
		// fired only because a descendant it left behind still held the output
		// pipe. Waiting for that descendant is what the delay is for, but
		// calling the call a failure is not: the caller records the URL this
		// returns and nothing else, so reporting "nothing was filed" for an
		// issue that was filed files a second public issue on the next submit.
		// internal/gitops guards the same case for the same reason.
		err = nil
	}
	if err != nil {
		if msg := ghFirstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", GHBin, strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("%s %s: %w", GHBin, strings.Join(args, " "), err)
	}
	return out, nil
}

// ghEnv is the child's environment: the person's own — exec.Cmd's own copy of
// it, so this file still names no package that could store what gh answers
// with — with two changes.
//
// GH_HOST and GH_REPO are dropped. `-R github.com/<owner>/<repo>` already
// decides both the host and the repository (F22), and an inherited GH_HOST
// names a different GitHub for a person who has one, which is a machine this
// post was never asked to go to; there is no reason to hand it to this call.
// Everything else is left alone: GH_TOKEN is how a person in a terminal
// without an interactive login authenticates, and gh's own credentials are
// gh's to use.
//
// GH_PROMPT_DISABLED says what the no-prompt policy says (docs/no-prompt-policy):
// no command waits for input. Title and body are supplied on the command line,
// so gh has nothing to ask, and this makes that a property of the child rather
// than of the arguments.
func ghEnv(environ []string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name == "GH_HOST" || name == "GH_REPO" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GH_PROMPT_DISABLED=1")
}

// GH is the person's own GitHub CLI, as the three things submitting needs.
// The zero value is the real one: the binary is looked up on PATH with
// exec.LookPath and run with ExecGH, and a test replaces either.
type GH struct {
	// LookPath reports gh's path on PATH; an error means it is not installed,
	// and then no question is put to it. Nil is exec.LookPath.
	LookPath func(file string) (string, error)
	// Run runs the command. Nil is ExecGH.
	Run GHRunner
}

// Installed reports whether gh is on PATH. It answers the only question the
// fallback needs: without gh nothing is filed, and the install hint and the
// new-issue link are what the person is given instead (spec 5).
func (g GH) Installed() bool {
	lookPath := g.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath(GHBin)
	return err == nil
}

// LoggedIn reports whether gh has an account to file with. `gh auth status`
// exits non-zero when it has none, and its answer is not read here: what it
// prints can hold a token's name and a host, and the caller needs only the
// yes or no — the person is told to run `gh auth login` when it is no.
func (g GH) LoggedIn(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, ghStatusTimeout)
	defer cancel()
	_, err := g.run()(ctx, "auth", "status")
	return err == nil
}

// Create files the post and returns the URL GitHub answered with. The body
// goes through a file `--body-file` names, so no argument quoting and no
// argument length is in the way of the text that is filed, and the repository
// is GHRepo, so GH_HOST and GH_REPO cannot send the post somewhere else
// (F22). The URL is accepted only in the shape of this project's issues
// (F28): the answer is text from outside, and a URL of any other shape is one
// a caller must not record as this issue's.
//
// The person's `gh` and their own credentials file it: nothing here reads,
// holds or forwards a token.
func (g GH) Create(ctx context.Context, p Post) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	body, err := os.CreateTemp("", "fugaro-post-*.md")
	if err != nil {
		return "", fmt.Errorf("issues: no file for the post's body: %w", err)
	}
	defer func() {
		body.Close()
		os.Remove(body.Name())
	}()
	if _, err := body.WriteString(p.Body); err != nil {
		return "", fmt.Errorf("issues: writing the post's body: %w", err)
	}
	if err := body.Close(); err != nil {
		return "", fmt.Errorf("issues: writing the post's body: %w", err)
	}
	out, err := g.run()(ctx, "issue", "create",
		"-R", GHRepo,
		"--title", p.Title,
		"--body-file", body.Name())
	if err != nil {
		return "", fmt.Errorf("issues: nothing was filed: %w", err)
	}
	url := createdIssueURL(out)
	if url == "" {
		return "", errors.New("issues: gh printed no issue URL of " + updatecheck.Repo + ", so nothing is recorded as filed")
	}
	return url, nil
}

// run is the runner this GH uses: the injected one, or the real gh.
func (g GH) run() GHRunner {
	if g.Run != nil {
		return g.Run
	}
	return ExecGH
}

// createdIssueURL is the issue URL in what `gh issue create` printed, or "".
// gh prints the URL on a line of its own, and the text around it is not read
// for anything: a line that is not this project's issue is not one to record
// (F28).
func createdIssueURL(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if projectIssueURL(line) {
			return line
		}
	}
	return ""
}

// ghFirstLine is the first non-blank line of gh's stderr as a person may be
// shown it: a run folded into an error message ends up on a terminal, and
// gh's own words about a refusal are text this machine did not write (F6).
// It is bounded, because an error line is there to say why, not to print a
// transcript.
func ghFirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return safetext.Clip(safetext.OneLine(t), ghFirstLineRunes)
		}
	}
	return ""
}

// ghFirstLineRunes is how much of gh's own line is carried into an error.
const ghFirstLineRunes = 200

// GHInstallPage is the official install page, and the last answer of
// InstallHint: a distribution this file does not know is told where gh is
// documented rather than guessed at (F15).
const GHInstallPage = "https://cli.github.com"

// InstallHint is how to get gh on this machine, for the platform and the
// distribution it was read on: the package manager's own command where one is
// known, and the install page otherwise. goos is runtime.GOOS and osRelease is
// the content of /etc/os-release, empty when it could not be read — both are
// arguments rather than reads here, so the answer is a test's and not the
// machine's (F15).
func InstallHint(goos, osRelease string) string {
	switch goos {
	case "darwin":
		return "brew install gh"
	case "windows":
		return "winget install GitHub.cli"
	case "linux":
		switch releaseFamily(osRelease) {
		case familyApt:
			return "sudo apt install gh"
		case familyDnf:
			return "sudo dnf install gh"
		}
	}
	return GHInstallPage
}

// familyApt and familyDnf are the two package managers InstallHint names, and
// the family of a Linux distribution is the one its own /etc/os-release
// declares (F15, U4).
const (
	familyApt = "apt"
	familyDnf = "dnf"
)

// aptIDs and dnfIDs are the distributions whose /etc/os-release declares them,
// by ID or by ID_LIKE — the field a derivative uses to name the family it
// follows. A distribution in neither list is given the install page: a wrong
// package manager is worse than none, and gh's page carries the command for
// every one of them.
var (
	aptIDs = []string{"debian", "ubuntu", "linuxmint", "pop", "elementary", "raspbian", "kali", "devuan", "zorin", "parrot", "mx", "neon", "trisquel", "deepin"}
	dnfIDs = []string{"fedora", "rhel", "centos", "rocky", "almalinux", "ol", "oracle", "amzn", "scientific", "nobara"}
)

// releaseFamily is the package manager osRelease names, "" when it names
// neither. ID is read before ID_LIKE and the whole set is read before the
// answer is given, so a derivative that names its own ID and its family both
// is answered by whichever of the two this file knows.
func releaseFamily(osRelease string) string {
	ids := osReleaseIDs(osRelease)
	for _, id := range ids {
		if slices.Contains(aptIDs, id) {
			return familyApt
		}
	}
	for _, id := range ids {
		if slices.Contains(dnfIDs, id) {
			return familyDnf
		}
	}
	return ""
}

// osReleaseIDs is the ID and ID_LIKE values of an /etc/os-release, in the
// order the file writes them: each is a bare word or a quoted list of words,
// and ID_LIKE's words are the families a derivative follows.
func osReleaseIDs(osRelease string) []string {
	var ids []string
	for _, line := range strings.Split(osRelease, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ID", "ID_LIKE":
			quoted := strings.Trim(strings.TrimSpace(value), `"'`)
			ids = append(ids, strings.Fields(strings.ToLower(quoted))...)
		}
	}
	return ids
}
