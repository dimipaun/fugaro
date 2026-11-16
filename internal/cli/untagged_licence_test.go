package cli

// TestUntaggedBuildHasNoLicenceCommand: an untagged build has no `licence`
// command at all, and — unlike every other paid name — no stub for it either
// (F6, D523, D524). The reason is in the name: the sentence D522's stub carries
// names the command the build does not include, so a stub for `licence` would
// itself be the licence notice D523 forbids an untagged build to print
// (assertStubSaysWhy pins that from the other side). What this build answers
// instead is cobra's own "unknown command", the answer it gives a name no build
// has, which is the right one here: the core does not speak of a licence at
// all, rather than speaking of a Subscription it does not carry.
//
// The name is still one the CLI answers to in the sense PaidCommandNames means
// — the reference's paid sections, the plugin's skill checks and the tagged
// tree's own scan read that list — so the two lists are held apart here: the
// paid names carry it, the stub names do not.

import (
	"slices"
	"strings"
	"testing"
)

func TestUntaggedBuildHasNoLicenceCommand(t *testing.T) {
	extendState(t)
	if !slices.Contains(paidCommandNames(), "licence") {
		t.Fatal("`licence` is not a paid name: the tagged tree's licence command would then be read as a core command by the reference's paid sections and the plugin's own checks")
	}
	if slices.Contains(stubCommandNames(), "licence") {
		t.Error("`licence` is a stub name: a core build would print the licence notice D523 forbids, in the stub's own sentence")
	}
	if slices.Contains(coreRootPaths(), "fugaro licence") {
		t.Fatal("the released tree holds a licence command: F6 has an untagged build answer none")
	}
	for _, path := range commandPaths(NewRootCmd()) {
		for _, field := range strings.Fields(path) {
			if strings.Contains(strings.ToLower(field), "licen") {
				t.Errorf("an untagged build answers the command path %q: F6 and D524 have it answer no licence command, under any spelling", path)
			}
		}
	}

	// Every spelling reaches cobra's own answer — non-zero, nothing on stdout —
	// and never the stub's sentence, which is the one thing a core build may not
	// say here.
	for _, args := range [][]string{
		{"licence"},
		{"license"},
		{"licence", "set"},
		{"licence", "status", "--json"},
		{"licence", "--help"},
	} {
		out, errOut, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v ran in a build that carries no licence command", args)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v exited %d, want %d: %v", args, ExitCode(err), ExitUserError, err)
		}
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%v = %q, want cobra's unknown-command answer: this build has no licence command and does not stand in for one", args, err.Error())
		}
		if out != "" {
			t.Errorf("%v wrote %q to stdout, want it empty", args, out)
		}
		if strings.Contains(errOut, "part of Fugaro Subscription Edition") {
			t.Errorf("%v printed the stub's sentence %q: that stub is the licence notice D523 forbids this build", args, errOut)
		}
	}

	help, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(help), "licen") {
		t.Errorf("`fugaro --help` names a licence command, which a reader of an untagged build cannot run:\n%s", help)
	}
}
