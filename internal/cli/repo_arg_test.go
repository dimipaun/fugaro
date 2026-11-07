package cli

// repoArg is the one resolver of a --repo value (repo_arg.go): a value that
// already reads as owner/name passes through, and a path resolves to its
// checkout's origin. These are its own tests; the commands that take --repo
// are covered where they are (budget_repo_flag_test.go's path case, and the
// commands' own tables).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// repoArgCheckout makes dir a git checkout whose origin is the values given,
// and returns dir.
func repoArgCheckout(t *testing.T, dir string, remotes ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-q")
	for i, url := range remotes {
		name := "origin"
		if i > 0 {
			name = "upstream"
		}
		testutil.Git(t, dir, "remote", "add", name, url)
	}
	return dir
}

// TestRepoArgPassesOwnerNameThrough: a value that already reads as owner/name
// is the repository, whatever else is on disk. It is returned exactly as it
// was typed -- this helper does not canonicalize, because the caller's own
// task.CanonicalRepo is the one place that reduction happens.
func TestRepoArgPassesOwnerNameThrough(t *testing.T) {
	ctx := context.Background()
	for _, value := range []string{"acme/app", "group/sub/app", "Acme/App", "acme/app.git", "acme/a_b.c-d"} {
		got, err := repoArg(ctx, value)
		if err != nil {
			t.Errorf("repoArg(%q) = %v", value, err)
			continue
		}
		if got != value {
			t.Errorf("repoArg(%q) = %q, want it unchanged", value, got)
		}
	}
}

// TestRepoArgResolvesACheckoutPath: ., .., a subdirectory of a checkout and an
// absolute path all resolve to the owner/name of that checkout's origin, for a
// github and for a bitbucket remote alike (repoFromOrigin is the one parser).
func TestRepoArgResolvesACheckoutPath(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	for name, origin := range map[string]string{
		"github":    githubOrigin,
		"bitbucket": bitbucketOrigin,
	} {
		t.Run(name, func(t *testing.T) {
			root := repoArgCheckout(t, filepath.Join(t.TempDir(), "checkout"), origin)
			sub := filepath.Join(root, "internal", "cli")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(sub)
			for _, tc := range []struct{ name, value string }{
				{"dot", "."},
				{"dotdot", ".."},
				{"relative", "../.."},
				{"absolute", root},
			} {
				got, err := repoArg(ctx, tc.value)
				if err != nil {
					t.Errorf("%s: repoArg(%q) = %v", tc.name, tc.value, err)
					continue
				}
				if got != "acme/app" {
					t.Errorf("%s: repoArg(%q) = %q, want acme/app", tc.name, tc.value, got)
				}
			}
		})
	}
}

// TestRepoArgOwnerNameWinsOverADirectoryOfThatName: a checkout sitting at
// ./acme/app does not shadow `--repo acme/app`, which names the repository
// wherever it is typed. This is the test that fails when the owner/name rule
// is dropped and every existing directory is read as a path.
func TestRepoArgOwnerNameWinsOverADirectoryOfThatName(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	cwd := t.TempDir()
	repoArgCheckout(t, filepath.Join(cwd, "acme", "app"), "https://github.com/other/thing.git")
	t.Chdir(cwd)
	if _, err := os.Stat(filepath.Join(cwd, "acme", "app", ".git")); err != nil {
		t.Fatalf("the shadowing checkout was not made: %v", err)
	}
	got, err := repoArg(ctx, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if got != "acme/app" {
		t.Fatalf("repoArg(acme/app) = %q, want acme/app (the directory of that name shadowed it)", got)
	}
}

// TestRepoArgRefusesWhatIsNotACheckout: a directory that exists and is in no
// checkout, and a path with nothing at it, are both refused, and the refusal
// names the path. A value with no directory behind it is still refused with
// the owner/name sentence, so a mistyped repository name reads as a mistyped
// repository name.
func TestRepoArgRefusesWhatIsNotACheckout(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)
	plain := filepath.Join(dir, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, value, says string }{
		{"a directory that is no checkout", plain, plain},
		{"a path with nothing at it", filepath.Join(dir, "gone"), "gone"},
		{"a mistyped repository name", "acme-app", "owner/name"},
		{"a URL", "https://github.com/acme/app", "owner/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repoArg(ctx, tc.value)
			if err == nil {
				t.Fatalf("repoArg(%q) was accepted", tc.value)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not name %q: %v", tc.says, err)
			}
		})
	}
}

// TestRepoArgRefusesAnOriginThatNamesNoRepository: a checkout with no origin
// remote, and one whose origin is a nested path (which repoFromOrigin
// refuses, so no repository of this project can be named by it), are refused
// with a sentence naming the path. The origin URL is printed redacted, never
// as it was typed.
func TestRepoArgRefusesAnOriginThatNamesNoRepository(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	t.Chdir(t.TempDir())
	noOrigin := repoArgCheckout(t, filepath.Join(t.TempDir(), "no-origin"))
	nested := repoArgCheckout(t, filepath.Join(t.TempDir(), "nested"), "https://github.com/group/sub/app")
	for _, tc := range []struct{ name, dir, says string }{
		{"no origin", noOrigin, "no origin remote"},
		{"an origin that names no owner/name", nested, "does not name owner/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repoArg(ctx, tc.dir)
			if err == nil {
				t.Fatalf("repoArg(%q) was accepted", tc.dir)
			}
			if !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), tc.dir) {
				t.Errorf("the refusal does not say %q about %s: %v", tc.says, tc.dir, err)
			}
		})
	}
}

// TestRepoArgQuotesThePathItNames: what was typed reaches the refusal through
// safetext.Field, so a path carrying an escape sequence (or a bidi override:
// it is argv text from anywhere) cannot rewrite the line a person reads.
func TestRepoArgQuotesThePathItNames(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	t.Chdir(t.TempDir())
	hostile := "no-such-\x1b[31mdir‮gpj"
	_, err := repoArg(ctx, hostile)
	if err == nil {
		t.Fatal("the path was accepted")
	}
	if strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "‮") {
		t.Fatalf("the refusal carries the raw path: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no-such-") {
		t.Errorf("the refusal does not name the path at all: %v", err)
	}
}
