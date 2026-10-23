package runstore

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// costBasisSplitters is the lines that may name both the basis and the
// subscription basis, and why. Each is a place the basis is the right question:
// the split's own definition, whose fallback a record written before C1.16
// needs, and the one writer, which records the notional part a run's auth mode
// implies rather than reading a recorded figure. An entry is a line's own text
// rather than a whole file, so a *reader* added beside the writer in the same
// file is still caught; the anchor is asserted present below, so a list entry
// cannot rot into a permission for a line that is gone.
var costBasisSplitters = []struct {
	file, anchor, why string
}{
	{
		file:   "internal/runstore/cost.go",
		anchor: "if c.ModelBasis == BasisSubscription {",
		why:    "the split's own definition: ModelSubscriptionUSD falls back to the basis for a record written before C1.16 carried the field",
	},
	{
		file:   "internal/runstore/cost.go",
		anchor: "if c.ModelBasis != BasisSubscription {",
		why:    "the same fallback in WithNotional, which recomputes the billed total from the notional part",
	},
	{
		file:   "internal/runner/cost.go",
		anchor: "runstore.ModelBasis(r.cfg.Agent.Auth) == runstore.BasisSubscription",
		why:    "the writer: a stage of a run with no gateway records the notional part its auth mode implies, never a reading of a recorded run's dollars",
	},
}

// TestNoReaderSplitsTheRunDollarsOnTheBasis is the guard on the rule P12/D140
// states: every reader of the run's dollars splits on model_notional_usd and
// never on ModelBasis, because a mixed cascade run's basis is api-list for the
// whole run -- its billed part's -- so the basis cannot tell the plan's dollars
// from a bill's. The reader sweep that followed C1.16 (#904, and this task) is
// why it exists: every site it fixed read the basis, and a new one that does is
// the same defect arriving again.
//
// A line naming both the basis and the subscription basis is such a reader: it
// is the comparison, whichever way it is spelled (`==`, `!=`, a switch case).
// Lines are read with their comments cut, so the rule's own prose -- which
// necessarily names both -- is not a match, and what is checked is this tree's
// code rather than any of its documents.
//
// The console's readers follow the same rule under a weaker check, because
// their basis test is legitimate: a row written before C1.16 carries no
// model_notional_usd, so the screens fall back to the subscription basis there
// (`modelReading` in console/src/renderer/screens). What is never legitimate is
// the fallback without the field, which is what the second walk asserts.
func TestNoReaderSplitsTheRunDollarsOnTheBasis(t *testing.T) {
	const root = "../.."
	seen := map[string]bool{}
	// allowed is the entry a line is permitted by, or nil: the anchor is the
	// entry's own line text, so a second reader in the same file is not covered
	// by the writer's entry beside it.
	allowed := func(file, line string) bool {
		ok := false
		for _, e := range costBasisSplitters {
			if e.file == file && strings.Contains(line, e.anchor) {
				seen[e.file+" "+e.anchor] = true
				ok = true
			}
		}
		return ok
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor", "dist", "build":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), root+"/")
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j]
			}
			if !strings.Contains(code, "ModelBasis") || !strings.Contains(code, "BasisSubscription") {
				continue
			}
			if allowed(rel, line) {
				continue
			}
			t.Errorf("%s:%d reads the run's dollars off the basis: %s\n"+
				"P12/D140: a reader splits on model_notional_usd (Cost.ModelMoneyUSD, Cost.ModelSubscriptionUSD, Cost.AllNotional), "+
				"never on ModelBasis -- a mixed cascade run's basis is api-list for the whole run. "+
				"If the line is the split's definition or a writer rather than a reader, add it to costBasisSplitters with its reason.",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range costBasisSplitters {
		if !seen[e.file+" "+e.anchor] {
			t.Errorf("costBasisSplitters allows %s to carry %q, and it does not (%s): "+
				"a permission for a line that is gone hides the next reader added to that file", e.file, e.anchor, e.why)
		}
	}
	// The console's half: a screen that decides on the basis must read the
	// field beside it.
	notionalRead := []string{"model_notional_usd", "modelNotionalUsd"}
	basisTest := []string{"modelBasis", "model_basis"}
	names := func(text string, ss []string) bool {
		for _, s := range ss {
			if strings.Contains(text, s) {
				return true
			}
		}
		return false
	}
	err = filepath.WalkDir(filepath.Join(root, "console", "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".ts" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		if !strings.Contains(text, "'subscription'") && !strings.Contains(text, `"subscription"`) {
			return nil
		}
		if !names(text, basisTest) || names(text, notionalRead) {
			return nil
		}
		t.Errorf("console/src/%s decides on the run's model basis and never reads %s: the basis is only the fallback for a row written before C1.16 (P12/D140), "+
			"so the field is what a console reader splits on", strings.TrimPrefix(filepath.ToSlash(path), root+"/console/src/"), strings.Join(notionalRead, " or "))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
