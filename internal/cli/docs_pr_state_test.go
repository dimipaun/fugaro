package cli

import (
	"strings"
	"testing"
	"time"
)

// TestDocsDesignDocPinsTheHostOfThePRStateLookup is the review finding on the
// amendment drifting from the code: c8e230d pinned the host in the argv gh is
// given (--repo github.com/<owner>/<repo>, so a forwarded GH_HOST cannot move
// the question to another GitHub), and the amendment that documents that call —
// normative here, the code comments cite docs/design as the authority — still
// showed the hostless form, in the very sentence that lists the call's
// guarantees. A reader implementing from the spec would drop the host and
// reintroduce exactly the confusion the fix removed.
//
// The check is the real argv rather than a literal: watch is run against a fake
// gh over a github.com row, the repository gh was asked for is read out of it,
// and the doc has to name that same host in the same argument. So the doc and
// the code cannot drift apart in either direction, and the host is not written
// twice here for one of them to rot.
func TestDocsDesignDocPinsTheHostOfThePRStateLookup(t *testing.T) {
	// This test is about the argv gh was given, not about how fast a loaded
	// machine can fork and exec it: it owns the lookup's budget (and the
	// --once wait, which must outlast it) so a slow exec is never read as a
	// lookup that answered nothing.
	longPRStateBudget(t)
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Minute, "ff01")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)
	calls := fakeWatchGH(t, "echo OPEN\n")

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	argv := ghWatchCalls(t, calls)
	if len(argv) != 1 {
		t.Fatalf("calls = %v, want one lookup of the ready row\nframe:\n%s", argv, plain)
	}
	repo := repoArgument(t, argv[0])
	// The property the doc has to carry: the repository gh is asked for is
	// named host-first. A bare owner/repo is resolved against gh's default
	// host, which GH_HOST sets — the bug the pin exists to prevent.
	parts := strings.Split(repo, "/")
	if len(parts) != 3 || !strings.Contains(parts[0], ".") {
		t.Fatalf("gh was asked for %q: the repository must carry its host (github.com/acme/app), not resolve against GH_HOST", repo)
	}

	doc := readDoc(t, "docs/design/generic-tool.md")
	line := docLine(doc, "gh pr view")
	if line == "" {
		t.Fatalf("the design doc no longer documents the gh call:\n%s", docLineish(doc))
	}
	// The doc's owner and repository are placeholders; the host is not, and it
	// is the host the code pins.
	want := "--repo " + parts[0] + "/<owner>/<repo>"
	if !strings.Contains(line, want) {
		t.Errorf("the amendment does not show the pinned call: want %q in the sentence that documents it:\n%s", want, line)
	}
	if !strings.Contains(line, "GH_HOST") {
		t.Errorf("the amendment gives the pinned host without saying what it is pinned against (GH_HOST):\n%s", line)
	}
}

// repoArgument is the value of gh's --repo in one recorded argv line, "" when
// the line has none.
func repoArgument(t *testing.T, call string) string {
	t.Helper()
	fields := strings.Fields(call)
	for i, a := range fields {
		switch {
		case a == "--repo" && i+1 < len(fields):
			return fields[i+1]
		case strings.HasPrefix(a, "--repo="):
			return strings.TrimPrefix(a, "--repo=")
		}
	}
	t.Fatalf("gh was run without --repo: %q", call)
	return ""
}

// docLine is the first line of doc containing want, "" when none does.
func docLine(doc, want string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// docLineish names the lines that mention gh, so a failure to find the sentence
// under test can be read: the doc may have been reworded, not wrong.
func docLineish(doc string) string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "gh ") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
