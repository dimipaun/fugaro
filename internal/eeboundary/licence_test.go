package eeboundary

// The dependency side of the licence boundary (licensing plan, the follow-up
// run of 2026-10-27): the console and the mobile app are under the business licence
// in their own `LICENSE.md`, and what they ship is not only their own code.
//
// This file holds one rule and one recording:
//
//  1. **no copyleft among the console's or the mobile app's dependencies.** A
//     GPL, AGPL or SSPL package reaching either app is a decision the owner
//     makes, not a `npm install` somebody runs, and the lockfiles declare each
//     package's licence, so the rule can be enforced hermetically: no network,
//     no `node_modules`, no install. An entry the audit justifies is named in
//     copyleftAllowed below, so the exception is a line a reviewer reads rather
//     than a rule nobody can tell is being enforced.
//  2. **every dependency declares a licence at all.** A package whose licence
//     is not in the lockfile is one nobody has classified; the report says so,
//     and this half keeps a new one from arriving without an answer.
//
// The audit itself is `docs/licensing/dependency-audit.md`: this file enforces
// what a machine can, and that document carries what a machine cannot -- what
// each licence obliges, which declarations are stale, and the app-store
// questions for counsel.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	ruleCopyleft     = "no GPL, AGPL or SSPL dependency"
	ruleLicenceNamed = "every dependency declares a licence"
)

// copyleftPattern matches the identifiers this rule refuses, wherever they
// appear in an SPDX expression. The match is on the identifier and not on a
// substring of one: `LGPL-2.1` and `AGPL-3.0` are copyleft too, and
// `SSPL-1.0` is the one the specification adds to the pair.
var copyleftPattern = regexp.MustCompile(`\b(?:A?LGPL-[\d.]+|AGPL-[\d.]+|GPL-[\d.]+|SSPL-[\d.]+|GPL-[\d.]+-only|GPL-[\d.]+-or-later)\b`)

// copyleftAllowed is the explicit allow-list of dependency names whose declared
// licence matches copyleftPattern and which `docs/licensing/dependency-audit.md`
// justifies. The reason is written beside each name because the list is what a
// reviewer reads to tell a justified exception from a package that slipped in:
// an entry here without a reason is the rule switched off for one name.
var copyleftAllowed = map[string]string{
	"node-forge": "(BSD-3-Clause OR GPL-2.0) is a choice between two licences, and the audit reads the app as taking BSD-3-Clause; the GPL arm is never exercised. Recorded here rather than excluded by a smarter expression parser, so the fact that a copyleft identifier appears at all is visible in the diff that changes it.",
}

// lockDirectory is one app whose lockfile this file reads. The path is built
// from the directory's own constant rather than written out: no Go file outside
// the console boundary test's allow-list may name a path under it.
type lockDirectory struct {
	dir  string
	path string
}

var lockDirectories = []lockDirectory{
	{dir: consoleDirName, path: consoleDirName + "/package-lock.json"},
	{dir: mobileDirName, path: mobileDirName + "/package-lock.json"},
}

// lockPackage is one entry of a v3 lockfile's `packages` map. `license` is a
// raw message because the field is either an SPDX string or an object of
// legacy `type`/`url` pairs, and the two spellings are read differently below.
type lockPackage struct {
	Version string          `json:"version"`
	License json.RawMessage `json:"license"`
	Dev     bool            `json:"dev"`
}

type lockDocument struct {
	LockfileVersion int                    `json:"lockfileVersion"`
	Packages        map[string]lockPackage `json:"packages"`
}

// dependency is one package as this file judges it: the name npm knows it by,
// the version, and its declared licence normalized to a string ("" when the
// lockfile declares none).
type dependency struct {
	name    string
	version string
	licence string
}

// readLockfile parses one app's lockfile and answers its dependencies, sorted by
// name. The root entry (`""`, the app's own manifest) is skipped: the app is not
// its own dependency, and its licence is the business licence read elsewhere.
func readLockfile(t *testing.T, root string, dir lockDirectory) []dependency {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir.path)))
	if err != nil {
		t.Fatalf("%s: %v -- the copyleft rule reads the lockfiles, so a lockfile it cannot open is a rule that would pass by finding nothing", dir.path, err)
	}
	var doc lockDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not the JSON this rule reads: %v", dir.path, err)
	}
	if doc.LockfileVersion != 3 {
		t.Fatalf("%s declares lockfileVersion %d, and this rule reads the v3 `packages` map: a different shape would be read as no dependencies at all, which is a green rule that checks nothing", dir.path, doc.LockfileVersion)
	}
	deps := make([]dependency, 0, len(doc.Packages))
	for path, pkg := range doc.Packages {
		if path == "" {
			continue
		}
		deps = append(deps, dependency{
			name:    strings.TrimPrefix(path, "node_modules/"),
			version: pkg.Version,
			licence: licenceOf(pkg.License),
		})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].name < deps[j].name })
	return deps
}

// licenceOf renders a lockfile's `license` field as one string. The field is an
// SPDX expression in a v3 lockfile, and the object spelling is the older
// `{ "type": ..., "url": ... }` pair; the type is the name of the licence and
// the URL is where its text is, so the type is what this rule reads. A package
// with no `license` field at all answers "", which is its own finding.
func licenceOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var expression string
	if err := json.Unmarshal(raw, &expression); err == nil {
		return strings.TrimSpace(expression)
	}
	var legacy struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &legacy); err == nil {
		return strings.TrimSpace(legacy.Type)
	}
	return string(raw)
}

// moduleRootFrom answers the repository root from this package's own directory.
// It is separate from boundary_test.go's moduleRoot, which asserts the go.mod it
// needs; this one answers the path for a caller that only reads lockfiles.
func moduleRootFrom(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller answered nothing: this file cannot find the repository root without its own path")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestLicenceBoundaryRefusesCopyleftDependencies: neither the console nor the
// mobile app depends on a GPL, AGPL or SSPL package. The check is hermetic --
// it reads the two lockfiles, which declare each package's licence, and nothing
// else -- so it runs in CI and in a container with no network.
//
// A new copyleft entry fails here by name, and the fix is not an edit to this
// file: it is either dropping the dependency or a line in copyleftAllowed with
// the reason, which is the decision the owner makes.
func TestLicenceBoundaryRefusesCopyleftDependencies(t *testing.T) {
	root := moduleRootFrom(t)
	seen := map[string]bool{}
	for _, dir := range lockDirectories {
		deps := readLockfile(t, root, dir)
		// The floor: a parse that read no dependencies would pass the loop
		// below without looking at one of them.
		if len(deps) < 100 {
			t.Fatalf("%s: read %d dependencies, and both apps carry hundreds: the rule would pass by checking nothing", dir.path, len(deps))
		}
		var allowed []string
		for _, dep := range deps {
			if !copyleftPattern.MatchString(dep.licence) {
				continue
			}
			if _, ok := copyleftAllowed[dep.name]; ok {
				seen[dep.name] = true
				allowed = append(allowed, dep.name)
				continue
			}
			t.Errorf("%s depends on %s %s, whose declared licence is %q: a copyleft identifier is a decision the owner makes rather than one a dependency update makes. Drop the dependency, or add its name to copyleftAllowed with the reason -- and if the report has not weighed it, weigh it there first (docs/licensing/dependency-audit.md)", dir.path, dep.name, dep.version, dep.licence)
		}
		if len(allowed) > 0 {
			slices.Sort(allowed)
			t.Logf("%s: %d copyleft identifier(s) allowed by copyleftAllowed: %s", dir.path, len(allowed), strings.Join(allowed, ", "))
		}
	}
	// The other direction: an allow-list entry whose package is gone, or whose
	// licence no longer matches, is an exception nobody can see has stopped
	// applying.
	for name := range copyleftAllowed {
		if !seen[name] {
			t.Errorf("copyleftAllowed names %q, and neither lockfile carries it with a matching licence any more: the exception has gone stale and should be removed deliberately, in the commit that shows why it is no longer needed", name)
		}
	}
}

// TestLicenceBoundaryNamesEveryDependencyLicence: every package in either
// lockfile declares its licence. Nothing is refused for a licence this rule
// dislikes -- that is the test above -- but a package nobody has classified is
// a package nobody has read, and the report lists each declaration as of
// 2026-10-27 precisely so that a new one is visible.
func TestLicenceBoundaryNamesEveryDependencyLicence(t *testing.T) {
	root := moduleRootFrom(t)
	for _, dir := range lockDirectories {
		deps := readLockfile(t, root, dir)
		if len(deps) < 100 {
			t.Fatalf("%s: read %d dependencies, and both apps carry hundreds: the rule would pass by checking nothing", dir.path, len(deps))
		}
		for _, dep := range deps {
			if dep.licence == "" {
				t.Errorf("%s depends on %s %s, which declares no licence in the lockfile: the report classifies every dependency by licence, and one it cannot classify is one nobody has looked at", dir.path, dep.name, dep.version)
			}
		}
	}
}
