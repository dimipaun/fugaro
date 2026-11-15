package eeboundary

// The dependency side of the licence boundary (licensing plan, the follow-up
// run of 2026-10-27): the console and the mobile app are under the business licence
// in their own `LICENSE.md`, the portal is a business-licensed feature under
// `ee/`, and what any of them ships is not only their own code.
//
// This file holds one rule and one recording:
//
//  1. **no copyleft among the console's, the mobile app's or the portal's
//     dependencies.** A GPL, AGPL or SSPL package reaching one of them is a
//     decision the owner makes, not a `npm install` somebody runs, and the
//     lockfiles declare each package's licence, so the rule can be enforced
//     hermetically: no network, no `node_modules`, no install. An entry the
//     audit justifies is named in copyleftAllowed below, so the exception is a
//     line a reviewer reads rather than a rule nobody can tell is being
//     enforced.
//  2. **every dependency declares a licence at all.** A package whose licence
//     is not in the lockfile is one nobody has classified; the report says so,
//     and this half keeps a new one from arriving without an answer.
//
// The audit itself is `docs/licensing/dependency-audit.md`: this file enforces
// what a machine can, and that document carries what a machine cannot -- what
// each licence obliges, which declarations are stale, and the app-store
// questions for counsel. It weighs the console's and the mobile app's
// dependencies today and the portal's not yet, which is the one thing about the
// third lockfile that is still owed there.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	ruleCopyleft     = "no GPL, AGPL or SSPL dependency"
	ruleLicenceNamed = "every dependency declares a licence"
)

// copyleftPattern matches the identifier families this rule refuses, wherever
// they appear in an SPDX expression: GPL and AGPL, LGPL (a copyleft too, and
// the one a package is likeliest to import by accident) and SSPL, the family
// the specification adds.
//
// The version is optional and its spelling is loose on purpose. A lockfile's
// `license` field is written by whichever tool produced it, and the published
// forms are not one shape: `GPL-2.0`, `GPL-2.0-only`, `GPL-2.0-or-later`,
// `GPL-3.0+`, and the older `GPL`, `GPLv2` and `GPLv3` all mean the same family.
// A pattern that required a hyphen and a version would let every versionless or
// `v`-spelled declaration through while the rule said it refuses them, so the
// version is matched when present and the bare family name matches on its own.
// The trailing `\b` is what keeps `GPL` from matching a longer word, and the
// leading one keeps `LGPL`'s `GPL` tail from matching on its own.
var copyleftPattern = regexp.MustCompile(`\b(?:A?GPL|LGPL|SSPL)(?:-?v?\d+(?:\.\d+)*)?(?:-only|-or-later)?\b`)

// copyleftAllowed is the explicit allow-list of dependencies whose declared
// licence matches copyleftPattern and which `docs/licensing/dependency-audit.md`
// justifies. It is keyed by the app whose lockfile carries the package, and then
// by the package's name, because an exception is the audit's reading of one
// app's dependency rather than a property of a name: the same package arriving
// in another app's lockfile is one nobody has weighed, and a list keyed by name
// alone would excuse it there in silence. A lockfile with no entry here excuses
// nothing, which is the honest state of the portal's until the audit reads it.
//
// The reason is written beside each name because the list is what a reviewer
// reads to tell a justified exception from a package that slipped in: an entry
// here without a reason is the rule switched off for one name.
var copyleftAllowed = map[string]map[string]string{
	mobileDirName: {
		"node-forge": "(BSD-3-Clause OR GPL-2.0) is a choice between two licences, and the audit reads the mobile app as taking BSD-3-Clause; the GPL arm is never exercised. Recorded here rather than excluded by a smarter expression parser, so the fact that a copyleft identifier appears at all is visible in the diff that changes it.",
	},
}

// portalDirName is the portal's directory, inside the business-licensed tree.
// It is spelled here rather than in the boundary test's list of licence
// directories because the portal is not a fourth one: it is a feature under
// ee/, whose every file already carries ee/'s own notice, and this file is the
// only place that reads anything of it.
const portalDirName = eeDirName + "/portal"

// lockDirectory is one app whose lockfile this file reads. The path is built
// from the directory's own constant rather than written out: no Go file outside
// the console boundary test's allow-list may name a path under it.
type lockDirectory struct {
	dir  string
	path string
	// absentWith is the directory this app's lockfile is absent together with,
	// and it is empty for a lockfile every checkout has. The portal's is the
	// one: `ee/` is deleted for the core build and its tests, so this rule holds
	// the portal to the same two readings as the console and the mobile app
	// wherever ee/ exists, and applies to nothing there where it does not.
	//
	// What excuses the absence is the boundary's and never the lockfile's. The
	// two are not the same state: a checkout that has ee/ and has lost the
	// portal's lockfile is one where both rules below would quietly read two of
	// the three apps and say nothing about it, which is the silence they exist
	// to break -- so this field names a directory to look for, not a file to
	// forgive.
	absentWith string
}

var lockDirectories = []lockDirectory{
	{dir: consoleDirName, path: consoleDirName + "/package-lock.json"},
	{dir: mobileDirName, path: mobileDirName + "/package-lock.json"},
	{dir: portalDirName, path: portalDirName + "/package-lock.json", absentWith: eeDirName},
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
// name, and whether there was a lockfile to read at all. The root entry (`""`,
// the app's own manifest) is skipped: the app is not its own dependency, and its
// licence is the business licence read elsewhere.
//
// A lockfile absent together with the directory it lives under is the one
// absence this answers instead of failing: the portal lives under ee/, and the
// core build and its tests run with that directory deleted, which is a tree this
// rule does not cover rather than one where it would pass by finding nothing.
// Any other absence fails, this app's included, because a lockfile the rule
// cannot open is a rule that checks nothing about the app it belongs to.
func readLockfile(t *testing.T, root string, dir lockDirectory) ([]dependency, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir.path)))
	if err != nil {
		if lockfileIsOptional(root, dir) {
			return nil, false
		}
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
	return deps, true
}

// lockfileIsOptional answers whether one lockfile's absence is this checkout's
// rather than a rule that covers nothing: only when the directory it is absent
// together with is not there, which is what the core build's deleted `ee/` looks
// like from here.
//
// Only "not there" excuses it. A directory this process cannot read is a
// question for the rule below rather than an answer here, or a permission
// problem would be read as the core build and two readings would cover two apps
// in silence.
func lockfileIsOptional(root string, dir lockDirectory) bool {
	if dir.absentWith == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir.absentWith)))
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return !info.IsDir()
}

// lockDirectoryFor answers the app whose lockfile this file reads, by its
// directory name, and fails when there is none: a case that names an app the
// rule does not read is one that would pass without looking at a lockfile.
func lockDirectoryFor(t *testing.T, dirName string) lockDirectory {
	t.Helper()
	for _, dir := range lockDirectories {
		if dir.dir == dirName {
			return dir
		}
	}
	t.Fatalf("%s is not an app this file reads a lockfile for", dirName)
	return lockDirectory{}
}

// copyleftExcuse answers the reason one app's lockfile allows a package whose
// declared licence is copyleft, and whether it allows it at all.
//
// It is the only way this file asks the question, so that which lockfile a
// package was found in is part of the question every time: a lookup of the name
// alone is the exception the audit read for one app applying to the others.
func copyleftExcuse(dir lockDirectory, name string) (string, bool) {
	reason, ok := copyleftAllowed[dir.dir][name]
	return reason, ok
}

// copyleftIn answers the dependencies of one app's lockfile whose declared
// licence is copyleft, split into the ones that app's allow-list excuses and the
// ones nothing does. Both halves are needed: the first is what the rule records
// as an exception a reviewer can see is still applying, and the second is what
// it refuses. The app's lockfile is an argument rather than the loop above it, so
// a case built by hand is judged by the same rule a real lockfile is.
func copyleftIn(dir lockDirectory, deps []dependency) (allowed, refused []dependency) {
	for _, dep := range deps {
		if !copyleftPattern.MatchString(dep.licence) {
			continue
		}
		if _, ok := copyleftExcuse(dir, dep.name); ok {
			allowed = append(allowed, dep)
			continue
		}
		refused = append(refused, dep)
	}
	return allowed, refused
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

// TestCopyleftPatternSpellsTheFamiliesOut is the rule's own table. It is here
// because the pattern is the half of the rule that decides what gets read at
// all: a declaration the pattern misses is a package the copyleft test never
// looks at, and the spellings below are the ones a lockfile carries that a
// pattern written for `GPL-2.0` alone would miss. The refusals are matched
// first, so a permissive declaration that grew a copyleft arm fails here rather
// than in a lockfile nobody edits.
func TestCopyleftPatternSpellsTheFamiliesOut(t *testing.T) {
	for _, tt := range []struct {
		declared string
		refused  bool
	}{
		{"GPL-2.0", true},
		{"GPL-2.0-only", true},
		{"GPL-2.0-or-later", true},
		{"GPL-3.0+", true},
		{"GPL", true},
		{"GPLv2", true},
		{"GPLv3", true},
		{"LGPL-2.1", true},
		{"LGPLv2.1", true},
		{"AGPL-3.0", true},
		{"AGPLv3", true},
		{"SSPL-1.0", true},
		{"SSPL", true},
		{"BSD-3-Clause OR GPL-2.0", true},
		{"MIT OR AGPL-3.0", true},

		{"", false},
		{"MIT", false},
		{"ISC", false},
		{"Apache-2.0", false},
		{"MPL-2.0", false},
		{"BSD-2-Clause", false},
		{"BSD-3-Clause", false},
		{"BlueOak-1.0.0", false},
		{"MIT OR CC0-1.0", false},
		{"WTFPL OR ISC", false},
		{"CC-BY-4.0", false},
		{"Unlicense", false},
		{"Python-2.0", false},
		{"0BSD", false},
	} {
		if got := copyleftPattern.MatchString(tt.declared); got != tt.refused {
			t.Errorf("copyleftPattern.MatchString(%q) is %v, want %v: the pattern decides which packages the copyleft rule reads at all, so a declaration it misses is one nothing refuses", tt.declared, got, tt.refused)
		}
	}
}

// TestLicenceBoundaryRefusesCopyleftDependencies: neither the console, the
// mobile app nor the portal depends on a GPL, AGPL or SSPL package. The check is
// hermetic -- it reads the lockfiles, which declare each package's licence, and
// nothing else -- so it runs in CI and in a container with no network.
//
// A new copyleft entry fails here by name, and the fix is not an edit to this
// file: it is either dropping the dependency or a line in copyleftAllowed under
// the app whose lockfile carries it, with the reason, which is the decision the
// owner makes -- and an exception already granted to another app is not one,
// because the audit read that app's dependency and not this one's.
func TestLicenceBoundaryRefusesCopyleftDependencies(t *testing.T) {
	root := moduleRoot(t)
	seen := map[string]map[string]bool{}
	for _, dir := range lockDirectories {
		deps, present := readLockfile(t, root, dir)
		if !present {
			continue
		}
		// The floor: a parse that read no dependencies would pass the loop
		// below without looking at one of them.
		if len(deps) < 100 {
			t.Fatalf("%s: read %d dependencies, and every one of these apps carries hundreds: the rule would pass by checking nothing", dir.path, len(deps))
		}
		allowed, refused := copyleftIn(dir, deps)
		for _, dep := range refused {
			t.Errorf("%s depends on %s %s, whose declared licence is %q: a copyleft identifier is a decision the owner makes rather than one a dependency update makes. Drop the dependency, or add its name to copyleftAllowed under this app with the reason -- and if the report has not weighed it, weigh it there first (docs/licensing/dependency-audit.md)", dir.path, dep.name, dep.version, dep.licence)
		}
		if len(allowed) == 0 {
			continue
		}
		seen[dir.dir] = map[string]bool{}
		names := make([]string, 0, len(allowed))
		for _, dep := range allowed {
			seen[dir.dir][dep.name] = true
			names = append(names, dep.name)
		}
		slices.Sort(names)
		t.Logf("%s: %d copyleft identifier(s) allowed by copyleftAllowed: %s", dir.path, len(names), strings.Join(names, ", "))
	}
	// The other direction: an allow-list entry whose app no longer carries the
	// package with a matching licence is an exception nobody can see has stopped
	// applying.
	for dirName, entries := range copyleftAllowed {
		for name := range entries {
			if !seen[dirName][name] {
				t.Errorf("copyleftAllowed excuses %q in %s, and that lockfile does not carry it with a matching licence any more: the exception has gone stale and should be removed deliberately, in the commit that shows why it is no longer needed", name, dirName)
			}
		}
	}
}

// TestCopyleftExceptionBelongsToTheLockfileItWasReadFor holds the allow-list to
// the app whose dependency the audit read it for. The list is one map inside the
// rule, and the question it answers -- is this package excused -- is about a
// package in a lockfile; asked of the name alone it would hand one app's
// exception to the others, so the package the audit justified for the mobile app
// arriving in the portal's lockfile would be allowed there without a word, in a
// tree `docs/licensing/dependency-audit.md` says a copyleft identifier arriving
// in is a task rather than a line in copyleftAllowed.
//
// The judgement goes through copyleftIn, which is what the rule above refuses
// with, so this is the refusal itself and not a second reading of the map.
func TestCopyleftExceptionBelongsToTheLockfileItWasReadFor(t *testing.T) {
	known := map[string]bool{}
	for _, dir := range lockDirectories {
		known[dir.dir] = true
	}
	for dirName, entries := range copyleftAllowed {
		if !known[dirName] {
			t.Errorf("copyleftAllowed excuses packages in %q, which is not an app this file reads a lockfile for: the entry applies to nothing, and reads as a decision that is being enforced", dirName)
		}
		for name, reason := range entries {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("copyleftAllowed excuses %q in %s with no reason: the list is what a reviewer reads to tell a justified exception from a package that slipped in, so an entry without one is the rule switched off for that name", name, dirName)
			}
		}
	}

	// The exception the audit has read, as the pair the rule has to hold apart:
	// the package's name, and the lockfile it was read in.
	forge := dependency{name: "node-forge", version: "1.3.1", licence: "(BSD-3-Clause OR GPL-2.0)"}
	if _, ok := copyleftExcuse(lockDirectoryFor(t, mobileDirName), forge.name); !ok {
		t.Fatalf("the mobile app's allow-list no longer excuses %s: this test holds an exception that exists to its own lockfile, so an exception that is gone is a change to make deliberately rather than to read around", forge.name)
	}
	for _, dir := range lockDirectories {
		allowed, refused := copyleftIn(dir, []dependency{forge})
		excused := len(allowed) == 1 && len(refused) == 0
		if want := dir.dir == mobileDirName; excused != want {
			t.Errorf("%s with %s %s, whose licence is %q: excused = %v, want %v. An exception is the audit's reading of one app's dependency rather than a property of the name, so a package arriving in another app's lockfile has to be refused there: the fix is weighing it, never a name the list already carries", dir.path, forge.name, forge.version, forge.licence, excused, want)
		}
	}
}

// TestLicenceBoundaryNamesEveryDependencyLicence: every package in any of these
// lockfiles declares its licence. Nothing is refused for a licence this rule
// dislikes -- that is the test above -- but a package nobody has classified is
// a package nobody has read, and the report lists each declaration as of
// 2026-10-27 precisely so that a new one is visible.
func TestLicenceBoundaryNamesEveryDependencyLicence(t *testing.T) {
	root := moduleRoot(t)
	for _, dir := range lockDirectories {
		deps, present := readLockfile(t, root, dir)
		if !present {
			continue
		}
		if len(deps) < 100 {
			t.Fatalf("%s: read %d dependencies, and every one of these apps carries hundreds: the rule would pass by checking nothing", dir.path, len(deps))
		}
		for _, dep := range deps {
			if dep.licence == "" {
				t.Errorf("%s depends on %s %s, which declares no licence in the lockfile: the report classifies every dependency by licence, and one it cannot classify is one nobody has looked at", dir.path, dep.name, dep.version)
			}
		}
	}
}

// TestLicenceBoundarySkipsOnlyWhereTheBoundaryIsAbsent holds the one escape
// either rule has: the portal's lockfile may be missing, and only because `ee/`
// is -- the core build, and the suite that runs beside it, delete that
// directory. The state is built here rather than described, because the two
// readings differ by one directory: excused by the lockfile's absence, a
// checkout that has `ee/` and has lost the portal's lockfile has both rules read
// two of the three apps and pass, which is the green rule they exist not to be.
func TestLicenceBoundarySkipsOnlyWhereTheBoundaryIsAbsent(t *testing.T) {
	portal := lockDirectoryFor(t, portalDirName)
	if portal.absentWith != eeDirName {
		t.Fatalf("%s is excused by %q, want %q: the portal's lockfile is absent exactly when the business-licensed tree it lives in is, and any other excuse is a lockfile these rules stop reading without saying so", portal.path, portal.absentWith, eeDirName)
	}

	root := t.TempDir()
	if !lockfileIsOptional(root, portal) {
		t.Errorf("%s with no %s/ in the tree: that absence is the core build's, so the rule covers nothing here rather than failing", portal.path, eeDirName)
	}
	if err := os.MkdirAll(filepath.Join(root, eeDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if lockfileIsOptional(root, portal) {
		t.Errorf("%s with %s/ in the tree: the lockfile's absence is this checkout's own and has to fail the rule, or both readings cover two of the three apps and say nothing", portal.path, eeDirName)
	}
	for _, dir := range lockDirectories {
		if dir.absentWith == "" && lockfileIsOptional(root, dir) {
			t.Errorf("%s is excused by an absence that has nothing to do with it: only the portal's lockfile is optional, and only while %s/ is gone", dir.path, eeDirName)
		}
	}
}
