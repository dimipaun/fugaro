package ciselect

import (
	"context"
	"strings"
	"testing"
)

// The in-job verify step's question about this repository's own change: which
// packages does it reach. The graph, the read map and the buildable set are
// this module's real ones, so a case says what the packages a run would test
// really are; the diff and the git call are the fake's, so no case needs a
// repository.

// verifyOverRepo is a selection over this repository's real read map and graph.
// The map keeps its `any` flags, which is what the committed map has: a
// package the map marks `any` runs for every change (R4), and a change under
// docs/ is read by the packages whose entries name it (R7).
func verifyOverRepo(t *testing.T, files ...string) VerifySelection {
	t.Helper()
	return verifyIn(t, verifyInput(t, selectRoot(t), files...))
}

func verifyInput(t *testing.T, root string, files ...string) Input {
	t.Helper()
	return Input{
		Root: root,
		Base: "origin/main",
		// No GitHub: the verify step has no run to ask and needs no timings.
		Command: &fakeCommand{
			graph:     selectListing(t, BuildTags),
			buildable: selectBuildable(t),
			diff:      selectDiff(files...),
		},
	}
}

func verifyIn(t *testing.T, in Input) VerifySelection {
	t.Helper()
	return SelectVerify(context.Background(), in)
}

// TestSelectVerifyKeepsTheTestsThatPinTheDocs: documents are pinned by tests
// (CLAUDE.md), and the read map is what says which package reads a document.
// A change under docs/ must still reach them — the empty selection would run
// the whole tree, which is safe but is not the answer a documents-only change
// has: it reaches ./internal/cli and its Docs tests.
func TestSelectVerifyKeepsTheTestsThatPinTheDocs(t *testing.T) {
	sel := verifyOverRepo(t, "docs/ci-affected-tests/reconciliation.md")
	if len(sel.Packages) == 0 {
		t.Fatalf("no packages for a documents-only change: %s", sel.Reason)
	}
	if !verifyHas(sel.Packages, "./internal/cli") {
		t.Errorf("packages = %v, want ./internal/cli, which is where the Docs tests are", sel.Packages)
	}
	// R7: a documents-only change takes the readers and nothing that merely
	// imports them, so a document edit does not put the whole tree back.
	if verifyHas(sel.Packages, "./internal/rtdb") {
		t.Errorf("packages = %v, want a package that reads no document left out", sel.Packages)
	}
}

// verifySynthetic is a module of three packages, so that a case can say which
// of them a change reaches without this repository's own shape — a change to a
// package near the bottom of the import graph reaches most of the tree here,
// which is the selector being right and the case being unreadable.
//
// changed is the package a case changes, importer imports it, and other
// imports neither.
var verifySynthetic = map[string]string{
	"go.mod":    "module example.com/m\n\ngo 1.24\n",
	readMapPath: `{"packages":[]}` + "\n",
}

const verifySyntheticListing = `{"ImportPath":"example.com/m/internal/changed","Name":"changed",` +
	`"Dir":"/mod/internal/changed","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
	`"GoFiles":["changed.go"]}` + "\n" +
	`{"ImportPath":"example.com/m/internal/importer","Name":"importer",` +
	`"Dir":"/mod/internal/importer","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
	`"GoFiles":["importer.go"],"Imports":["example.com/m/internal/changed"]}` + "\n" +
	`{"ImportPath":"example.com/m/internal/other","Name":"other",` +
	`"Dir":"/mod/internal/other","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
	`"GoFiles":["other.go"]}` + "\n"

func TestSelectVerifyNarrowsAChangeToWhatItReaches(t *testing.T) {
	in := verifyInput(t, selectWriteRoot(t, verifySynthetic), "internal/changed/changed.go")
	in.Command.(*fakeCommand).graph = []byte(verifySyntheticListing)
	in.Command.(*fakeCommand).buildable = nil // the synthetic module has no tag-gated package
	sel := verifyIn(t, in)
	if len(sel.Packages) == 0 {
		t.Fatalf("no packages for a change to internal/changed: %s", sel.Reason)
	}
	for _, want := range []string{"./internal/changed", "./internal/importer"} {
		if !verifyHas(sel.Packages, want) {
			t.Errorf("packages = %v, want %s: the changed package and what imports it", sel.Packages, want)
		}
	}
	if verifyHas(sel.Packages, "./internal/other") {
		t.Errorf("packages = %v, want a package the change cannot reach left out", sel.Packages)
	}
}

// TestSelectVerifyWidensForEveryDoubt is R8 in the in-job direction: the
// caller's answer for anything the selector cannot place is the whole tree it
// would have run without a selector, which is the empty selection. A selector
// that failed open would run fewer tests than the change can break.
func TestSelectVerifyWidensForEveryDoubt(t *testing.T) {
	cases := []struct {
		name string
		// input builds the run's Input, the row's one difference from a
		// change that can be placed.
		input func(t *testing.T) Input
		want  string
	}{
		{
			name: "no commit to measure from",
			input: func(t *testing.T) Input {
				in := verifyInput(t, selectRoot(t), "internal/rtdb/client.go")
				in.Base = ""
				return in
			},
			want: "no commit to measure the change from",
		},
		{
			name: "nothing changed",
			input: func(t *testing.T) Input {
				return verifyInput(t, selectRoot(t))
			},
			want: "nothing changed",
		},
		{
			name: "the diff would not run",
			input: func(t *testing.T) Input {
				in := verifyInput(t, selectRoot(t), "internal/rtdb/client.go")
				in.Command.(*fakeCommand).failCall = "git diff"
				return in
			},
			want: ReasonDiff,
		},
		{
			name: "the read map is missing",
			input: func(t *testing.T) Input {
				return verifyInput(t, selectWriteRoot(t, map[string]string{"go.mod": "module example.com/m\n\ngo 1.24\n"}),
					"internal/rtdb/client.go")
			},
			want: ReasonNoReadMap,
		},
		{
			name: "the graph would not list",
			input: func(t *testing.T) Input {
				in := verifyInput(t, selectRoot(t), "internal/rtdb/client.go")
				in.Command.(*fakeCommand).failCall = "go list -deps"
				return in
			},
			want: ReasonNoGraph,
		},
		{
			// A path no package owns and no entry names: a Go file in a
			// directory that is no package of the listing, which is what a
			// package added, renamed or moved looks like to a graph listed
			// before it — and a path in the tree that is no Go file at all.
			// Both widen.
			name: "a Go path no package owns",
			input: func(t *testing.T) Input {
				in := verifyInput(t, selectWriteRoot(t, verifySynthetic), "internal/brandnew/thing.go")
				in.Command.(*fakeCommand).graph = []byte(verifySyntheticListing)
				in.Command.(*fakeCommand).buildable = nil // the synthetic module has no tag-gated package
				return in
			},
			want: ReasonUnmapped,
		},
		{
			name: "a path outside Go",
			input: func(t *testing.T) Input {
				return verifyInput(t, selectRoot(t), "notes/random.txt")
			},
			want: ReasonUnmapped,
		},
		{
			name: "go.mod",
			input: func(t *testing.T) Input {
				return verifyInput(t, selectRoot(t), "go.mod")
			},
			want: ReasonAlwaysFull,
		},
		{
			// The four packages the verify step never runs are not this
			// function's business — a caller drops them and widens on the
			// empty answer. What is: a change that reaches nothing this step
			// can run, because every package it reaches is tag-gated.
			name: "every package the change reaches is tag-gated",
			input: func(t *testing.T) Input {
				const listing = `{"ImportPath":"example.com/m/internal/infra/x","Name":"x",` +
					`"Dir":"/mod/internal/infra/x","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
					`"TestGoFiles":["x_test.go"]}` + "\n"
				empty := ""
				in := verifyInput(t, selectWriteRoot(t, verifySynthetic), "internal/infra/x/x_test.go")
				in.Command.(*fakeCommand).graph = []byte(listing)
				in.Command.(*fakeCommand).buildable = &empty
				return in
			},
			want: "no package this step can run",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sel := verifyIn(t, c.input(t))
			if len(sel.Packages) != 0 {
				t.Fatalf("packages = %v, want none so that the caller runs the whole tree", sel.Packages)
			}
			if !strings.Contains(sel.Reason, c.want) {
				t.Errorf("reason = %q, want it to name %q", sel.Reason, c.want)
			}
		})
	}
}

// TestSelectVerifyMeasuresTheWorkingTreeAgainstTheBase: what the verify step
// tests is the tree its command runs in, so the diff is the base against the
// working tree — one revision and no second one — and not base..HEAD, which
// would leave a change the agent made and has not committed out of the
// selection.
func TestSelectVerifyMeasuresTheWorkingTreeAgainstTheBase(t *testing.T) {
	in := verifyInput(t, selectRoot(t), "internal/rtdb/client.go")
	verifyIn(t, in)
	cmd := in.Command.(*fakeCommand)
	var diffs []string
	for _, call := range cmd.calls {
		if strings.HasPrefix(call, "git diff") {
			diffs = append(diffs, call)
		}
	}
	if want := []string{"git diff --name-only --no-renames origin/main"}; len(diffs) != 1 || diffs[0] != want[0] {
		t.Errorf("git calls = %v, want exactly %v", diffs, want)
	}
}

func verifyHas(pkgs []string, pkg string) bool {
	for _, got := range pkgs {
		if got == pkg {
			return true
		}
	}
	return false
}
