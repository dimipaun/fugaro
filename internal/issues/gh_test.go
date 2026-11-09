package issues

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// ghFound is the seam a test's GH uses when gh is on PATH: no binary is
// looked up and nothing outside the test is read.
func ghFound(string) (string, error) { return "/usr/bin/" + GHBin, nil }

// TestGHNotInstalledAndNotLoggedIn: the two answers that send `submit` to the
// fallback instead of filing anything (spec 5, acceptance 4). Neither runs gh
// in a way that could file: Installed only looks on PATH, and LoggedIn reads
// its exit status.
func TestGHNotInstalledAndNotLoggedIn(t *testing.T) {
	missing := GH{LookPath: func(string) (string, error) {
		return "", errors.New(`exec: "gh": executable file not found in $PATH`)
	}}
	if missing.Installed() {
		t.Error("Installed() = true for a gh that is not on PATH")
	}

	var asked [][]string
	loggedOut := GH{
		LookPath: ghFound,
		Run: func(_ context.Context, args ...string) ([]byte, error) {
			asked = append(asked, slices.Clone(args))
			return nil, errors.New("gh: To get started with GitHub CLI, please run: gh auth login")
		},
	}
	if !loggedOut.Installed() {
		t.Fatal("Installed() = false for a gh that is on PATH")
	}
	if loggedOut.LoggedIn(context.Background()) {
		t.Error("LoggedIn() = true for a gh that refused to answer")
	}
	if want := [][]string{{"auth", "status"}}; !reflect.DeepEqual(asked, want) {
		t.Errorf("LoggedIn asked gh %q, want %q: one question, about this machine", asked, want)
	}

	// A gh that cannot file reports the refusal and records no URL: the caller
	// sets `submitted` from the URL and nothing else, so an empty one leaves
	// the issue where it was.
	url, err := loggedOut.Create(context.Background(), BuildPost(issueSample(), nil))
	if err == nil {
		t.Errorf("Create() = %q, nil for a gh that refused", url)
	}
	if url != "" {
		t.Errorf("Create() = %q with an error, want no URL", url)
	}
}

// TestGHUsesBodyFileAndExplicitRepo: the body is filed through --body-file and
// the repository is named in the argv, so neither the environment nor the
// working directory can send the post somewhere else, and a URL of another
// repository is not recorded as this issue's (F22, F28).
func TestGHUsesBodyFileAndExplicitRepo(t *testing.T) {
	// An environment that would redirect a `gh` resolving its repository from
	// the environment: the argv is what decides, so this changes nothing.
	t.Setenv("GH_HOST", "github.example.test")
	t.Setenv("GH_REPO", "someone/other")

	p := BuildPost(issueSample(), nil)
	var argv []string
	var filed, filedPath string
	g := GH{
		LookPath: ghFound,
		Run: func(_ context.Context, args ...string) ([]byte, error) {
			argv = slices.Clone(args)
			for i, arg := range args {
				if arg == "--body-file" && i+1 < len(args) {
					filedPath = args[i+1]
					body, err := os.ReadFile(filedPath)
					if err != nil {
						t.Errorf("reading the body file %q: %v", filedPath, err)
					}
					filed = string(body)
				}
			}
			return []byte("https://github.com/" + updatecheck.Repo + "/issues/12\n"), nil
		},
	}

	url, err := g.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := "https://github.com/" + updatecheck.Repo + "/issues/12"; url != want {
		t.Errorf("Create() = %q, want %q", url, want)
	}
	want := []string{"issue", "create", "-R", "github.com/" + updatecheck.Repo,
		"--title", p.Title, "--body-file", filedPath}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("gh was run as %q, want %q", argv, want)
	}
	if filed != p.Body {
		t.Errorf("the body file holds %q, want the post's body", filed)
	}
	if _, err := os.Stat(filedPath); !os.IsNotExist(err) {
		t.Errorf("the body file %q is still there after Create: %v", filedPath, err)
	}

	t.Run("a URL of another repository is refused", func(t *testing.T) {
		other := GH{
			LookPath: ghFound,
			Run: func(context.Context, ...string) ([]byte, error) {
				return []byte("https://github.com/someone/other/issues/1\n"), nil
			},
		}
		url, err := other.Create(context.Background(), p)
		if err == nil {
			t.Errorf("Create() = %q, nil for another repository's URL", url)
		}
		if url != "" {
			t.Errorf("Create() = %q with an error, want no URL", url)
		}
	})
}

// TestExecGHKeepsTheURLOfAChildThatExited: gh prints the issue URL to stdout
// and exits 0, and a descendant it left behind holding that pipe must not turn
// the filed issue into "nothing was filed". The caller records the URL this
// returns and nothing else, so losing it files a duplicate public issue at the
// next submit (F24's own guarantee is the other half: the call still returns
// well inside the claim's lease).
//
// The fake gh is a shell script, and the sleep it backgrounds inherits stdout
// with no redirection — that inheritance is the leak, and it outlives the
// shell. internal/gitops' TestLeakedChildDoesNotHang is the precedent, and its
// guard is the one mirrored in ExecGH.
func TestExecGHKeepsTheURLOfAChildThatExited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	// The path is the fake gh's alone, so the descendant is named by its own
	// path rather than found on a PATH this test has taken away.
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep to leak")
	}
	link := "https://github.com/" + updatecheck.Repo + "/issues/12"
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "leaked.pid")
	// 600s is far longer than ExecGH's wait delay, so a regression — waiting for
	// the leaked child — fails the bound below rather than passing slowly.
	script := "#!/bin/sh\n" + sleep + " 600 & echo $! > " + pidFile + "\necho " + link + "\n"
	fake := filepath.Join(dir, GHBin)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake gh: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Cleanup(func() {
		// The leaked child is this test's own: it is killed here whether the
		// assertions below held or not.
		pid, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if n, err := strconv.Atoi(strings.TrimSpace(string(pid))); err == nil {
			if p, err := os.FindProcess(n); err == nil {
				_ = p.Kill()
			}
		}
	})

	start := time.Now()
	out, err := ExecGH(context.Background(), "issue", "create")
	d := time.Since(start)
	if _, statErr := os.Stat(pidFile); statErr != nil {
		t.Fatalf("the fake gh left no descendant behind (%v): the case under test did not happen", statErr)
	}
	if d > time.Minute {
		t.Fatalf("ExecGH waited %s for a gh that had exited", d)
	}
	if d < time.Second {
		t.Fatalf("ExecGH returned in %s, before its wait delay could fire: the descendant did not hold the pipe", d)
	}
	if err != nil {
		t.Fatalf("ExecGH returned an error for a gh that exited 0: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != link {
		t.Errorf("ExecGH returned %q, want the URL gh printed (%q)", got, link)
	}
}

// TestGHEnvCannotRedirectTheHost: GH_HOST and GH_REPO are taken out of the
// child's environment, so the argv's -R is the only answer to where the post
// goes; a token a person authenticates with is left alone, because that is the
// one credential this call is meant to have (F22).
func TestGHEnvCannotRedirectTheHost(t *testing.T) {
	env := ghEnv([]string{
		"PATH=/usr/bin",
		"GH_HOST=github.example.test",
		"GH_REPO=someone/other",
		"GH_TOKEN=" + strings.Repeat("Qw3", 4),
	})
	for _, want := range []string{"PATH=/usr/bin", "GH_TOKEN=" + strings.Repeat("Qw3", 4), "GH_PROMPT_DISABLED=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("the child's environment lost %q:\n%q", want, env)
		}
	}
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == "GH_HOST" || name == "GH_REPO" {
			t.Errorf("the child's environment still carries %q", kv)
		}
	}
}
