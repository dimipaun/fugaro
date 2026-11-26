package policy_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// removedConstants matches the two constants I1 deleted, assembled from
// fragments so that this file — which the walk below reads like any other —
// holds neither word itself, and the sweep it stands in for keeps finding
// nothing (docs/always-a-budget/implementation-plan.md §I1).
var removedConstants = regexp.MustCompile(`\b(?:Mode|Budget)` + "Off" + `\b`)

// TestNoCodeReadsTheRemovedMode (I1, D3009): off is not a mode any more, so no
// package may still reach it through a constant that says otherwise. The word
// that was a mode survives in exactly one place — the reading that refuses it
// — and a second name for it anywhere in the tree would be a reader compiling
// against a vocabulary the configuration and the gateway no longer have.
//
// The walk is over the whole tree rather than over the packages the sweep
// touched: the constants were reachable from outside this package, so the
// place that names them next is as likely to be a new file as an old one.
func TestNoCodeReadsTheRemovedMode(t *testing.T) {
	var found []string
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("../..", p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if removedConstants.MatchString(line) {
				found = append(found, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the tree could not be walked, so nothing was checked: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("a constant for the removed mode is still named:\n%s", strings.Join(found, "\n"))
	}
}
