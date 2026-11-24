package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// C1's tests are about the contract around the reader: the order of the list
// (and its memo), the hint, and the line. What reads the repositories is
// enrolledReposImpl, which a test stands in for here -- it is git and, after
// I1, the code host's API.

// stubEnrolledRepos stands impl in for the command's reader and restores the
// real one when the test ends.
func stubEnrolledRepos(t *testing.T, impl func(context.Context, enrolledIn) ([]repoView, []string)) {
	t.Helper()
	old := enrolledReposImpl
	t.Cleanup(func() { enrolledReposImpl = old })
	enrolledReposImpl = impl
}

// reposOf is the views' repositories, in the order they came.
func reposOf(views []repoView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Repo)
	}
	return out
}

// TestRepoViewOrderIsCanonical: whatever order the reader listed the
// repositories in, every stage and both output members see them in canonical
// owner/name order ("Zeta/app" sorts after "beta/x", which byte order would
// put last), and one run reads the list once (the memo). The caller's copy is
// its own: a stage that reordered it would reorder the run's.
func TestRepoViewOrderIsCanonical(t *testing.T) {
	calls := 0
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		calls++
		return []repoView{{Repo: "Zeta/app"}, {Repo: "beta/x"}, {Repo: "Acme/Web"}}, []string{"github/acme/*"}
	})
	want := []string{"Acme/Web", "beta/x", "Zeta/app"}

	state := &reposState{}
	first, patterns := enrolledRepos(context.Background(), enrolledIn{Memo: state})
	if got := reposOf(first); !slices.Equal(got, want) {
		t.Fatalf("the list came back as %v, want %v: the views are in canonical owner/name order whatever order the reader listed them in", got, want)
	}
	if !slices.Equal(patterns, []string{"github/acme/*"}) {
		t.Errorf("the admit patterns came back as %v, want the one the reader could not list: a pattern is reported, never expanded", patterns)
	}

	first[0].Repo = "mutated/by the caller"
	second, _ := enrolledRepos(context.Background(), enrolledIn{Memo: state})
	if got := reposOf(second); !slices.Equal(got, want) {
		t.Errorf("the second read of the run came back as %v, want %v: the run's list is its own, and a caller's edit must not reach it", got, want)
	}
	if calls != 1 {
		t.Errorf("the repositories were read %d times in one run, want 1: the list is memoized on the engine's state (e.repos)", calls)
	}

	// A caller with no memo (doctor, a test) reads fresh, and still in
	// canonical order.
	fresh, _ := enrolledRepos(context.Background(), enrolledIn{})
	if got := reposOf(fresh); !slices.Equal(got, want) {
		t.Errorf("a read with no memo came back as %v, want %v", got, want)
	}
	if calls != 2 {
		t.Errorf("a read with no memo made %d reads in all, want 2: without a memo nothing is remembered between calls", calls)
	}
}

// TestHintFirstMovesOnlyHumanLines: the hint puts the working directory's
// repository at the top of the human lines and moves nothing else -- the rest
// keep their order -- and it leaves the caller's slice alone, which is the
// one the --json document and the event stream are built from (they are never
// reordered). A hint no line names, or one already first, changes nothing.
func TestHintFirstMovesOnlyHumanLines(t *testing.T) {
	lines := []initflow.RepoLine{{Repo: "acme/app"}, {Repo: "beta/web"}, {Repo: "gamma/x"}}
	before := slices.Clone(lines)

	if got := hintFirst("Beta/Web", lines); !slices.Equal(got, []initflow.RepoLine{lines[1], lines[0], lines[2]}) {
		t.Errorf("hintFirst(beta/web) came back as %v, want the hinted line first and the rest in the order they came", got)
	}
	if !slices.Equal(lines, before) {
		t.Errorf("hintFirst changed the caller's lines: the --json document and the event stream are built from the same slice and keep the canonical order")
	}
	for _, hint := range []string{"", "delta/other", "acme/app"} {
		if got := hintFirst(hint, lines); !slices.Equal(got, lines) {
			t.Errorf("hintFirst(%q) came back as %v, want the lines as they stand: a hint that names no line, and one already first, move nothing", hint, got)
		}
	}
}

// TestNewRepoLineSanitises: a line about a repository can quote a branch name,
// a path or a provider's own sentence, so the repository and the detail are
// made safe for one line of a terminal and bounded in length before anything
// prints them -- while the state, which is this program's own word, is not
// touched.
func TestNewRepoLineSanitises(t *testing.T) {
	line := newRepoLine("acme/\x1b[31mweb\r\n", initflow.RepoNeedsYou,
		"secret \x07 ‮gpj.txt is missing\nrun `fugaro secrets set`")
	if got, want := line.Repo, "acme/web??"; got != want {
		t.Errorf("the repository came back as %q, want %q: an escape sequence is dropped, and every other control becomes a printable one", got, want)
	}
	if got, want := line.Detail, "secret ? ?gpj.txt is missing?run `fugaro secrets set`"; got != want {
		t.Errorf("the detail came back as %q, want %q", got, want)
	}
	if line.State != initflow.RepoNeedsYou {
		t.Errorf("the state came back as %q, want %q: it is this program's own word and is not sanitised", line.State, initflow.RepoNeedsYou)
	}

	long := newRepoLine("acme/app", initflow.RepoDone, strings.Repeat("x", pluginwire.MaxPrintable+100))
	if got, want := len([]rune(long.Detail)), pluginwire.MaxPrintable+len("... (truncated)"); got != want {
		t.Errorf("a detail of %d runes came back %d runes long, want %d: the line is bounded", pluginwire.MaxPrintable+100, got, want)
	}
	if !strings.HasSuffix(long.Detail, "(truncated)") {
		t.Errorf("a long detail came back as %q, and a cut one says so", long.Detail)
	}
}
