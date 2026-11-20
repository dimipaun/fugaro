package cli

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// config_ownership_e2e_test.go is I1's integration half
// (docs/config-ownership/implementation-plan.md, cm-conformance). Two things
// are pinned here. The stub C1 left behind is gone and the seam holds N3's
// function, which is a fact about the whole binary and not about one command
// (TestNoStubSurvives). And the semantic commands of the plan leave this
// machine's config and the published node saying the same thing — after the
// command, after `fugaro project policy publish` and after `fugaro update`,
// with stdin closed at every one of them (TestConfigOwnershipEndToEnd).

// afterWriteStubFile is C1's stub, the file I1 deletes: an init assigning
// runAfterWrite the no-op that keeps every semantic command's write out of the
// published node. Its absence is what TestNoStubSurvives reads, so the name is
// a string the test holds rather than a path only a comment spells: a tree
// where the file came back under this name fails.
const afterWriteStubFile = "after_write_stub.go"

// afterWriteAssigns matches an assignment to the hook seam in a source file,
// and not the three other things a line can spell: the declaration
// (after_write.go's `var runAfterWrite afterWriteFunc`), a comparison (a test's
// `runAfterWrite == nil`), and a call (`runAfterWrite(ctx, cmd, change)`).
// Comments are skipped by the caller, so a sentence about the seam is not read
// as one.
var afterWriteAssigns = regexp.MustCompile(`\brunAfterWrite\s*=\s*[^=]`)

// TestNoStubSurvives is I1's red step: it fails while after_write_stub.go
// exists, because that file's init assigns the no-op and every semantic
// command's write would go unpublished with nothing reporting it.
//
// What the test reads is deliberately more than the file's absence. The seam
// is one variable, so what EVERY semantic command reaches is decided by the
// assignments to it rather than by which commands call it — a second file
// assigning it (a stub, a test's own no-op in a non-test file, a later
// "simplification") would take the hook away from all of them at once, which
// is exactly how C1's stub was built and exactly why deleting it is not
// enough on its own. So three facts are read together: the file is gone, the
// variable holds N3's function by pointer, and exactly one non-test source
// file assigns it, that being write_publish.go's init.
func TestNoStubSurvives(t *testing.T) {
	if _, err := os.Stat(afterWriteStubFile); err == nil {
		t.Fatalf("%s is in internal/cli: its init assigns runAfterWrite the no-op, so every semantic command's write is saved and never published — delete the file (I1)", afterWriteStubFile)
	} else if !os.IsNotExist(err) {
		t.Fatalf("reading %s: %v", afterWriteStubFile, err)
	}
	if runAfterWrite == nil {
		t.Fatal("runAfterWrite is nil: a semantic command's write would publish nothing and report nothing")
	}
	if got, want := reflect.ValueOf(runAfterWrite).Pointer(), reflect.ValueOf(publishAfterWrite).Pointer(); got != want {
		t.Fatalf("runAfterWrite holds %#x and publishAfterWrite is %#x: the hook the seam holds is not write_publish.go's", got, want)
	}
	if got, want := afterWriteAssigningFiles(t), []string{"write_publish.go"}; !slices.Equal(got, want) {
		t.Errorf("the non-test files assigning runAfterWrite are %v, want %v: one assignment is what makes the hook N3's for every command that calls it, and a second is a file silently taking it away", got, want)
	}
}

// afterWriteAssigningFiles names the package's non-test source files that
// assign runAfterWrite, sorted. The package's own directory is the working
// directory `go test` runs in (closedStdinSourceDir's rule), so the scan is of
// the files the binary is built from.
func afterWriteAssigningFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if afterWriteAssigns.MatchString(trimmed) {
				out = append(out, name)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
