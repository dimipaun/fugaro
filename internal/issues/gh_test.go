package issues

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

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
