// Package sensitivity decides whether a change is security-sensitive, from the
// change's changed paths and from nothing else (D1234, G6).
//
// The class picks the model a decision agent runs on: the routine one for a
// change the owner's reviewer routing rule sends to the light reviewer, Opus
// for every other. That rule is decided over the changed paths and never over
// how a change describes itself — a change that says "documentation" must not
// buy a cheaper review of a guard — so the classifier takes paths, its
// signature has no room for a title or a description, and a table test pins
// that shape (TestDescriptionCannotLowerTheClass).
//
// It is an allowlist and not a denylist: a change is Routine only when every
// one of its paths is inside a routine tree below. Everything else — a tree
// nobody classified yet, a path this package cannot place, and a change whose
// path list it could not read at all — is Sensitive. A tree added to the
// repository later is therefore Sensitive until a person adds it here, which is
// what makes the answer safe to act on without a person in the loop.
package sensitivity

import (
	"path"
	"strings"
	"unicode/utf8"
)

// Class is what a change is: the key O4's model setting is looked up by and the
// one a decision agent's model and its review level follow.
type Class string

const (
	// Routine is a change whose every path is inside one of the routine trees:
	// documentation, and the website's copy and the pages a reader is sent to.
	// The owner's routing rule gives the light reviewer docs, tests and design;
	// the website is the plan's own addition for a tree that is mostly rendered
	// copy, and the paths under it that carry what a reader installs, runs or
	// verifies are exceptions below rather than part of it.
	Routine Class = "routine"
	// Sensitive is every change the classifier cannot place inside a routine
	// tree — a credential, a guard, a prompt, a recipe, a workflow, the
	// agent's environment, Terraform or IAM, the console's writes — and every
	// change whose paths it could not read. It is never reviewed below the
	// Opus class, and no configuration lowers it (D1234, O4).
	Sensitive Class = "sensitive"
)

// rule is one line of a table: a name the routing text's words can be held
// against, and the repository-relative path prefix it matches. A line that
// carries a suffix matches the class of files under its prefix that end in
// those bytes, which is the one shape a boundary inside a tree can have when
// the files it names sit at any depth.
type rule struct {
	name   string
	prefix string
	suffix string
}

// routineRules is the allowlist (D1234): the trees a change may be Routine in.
// A tree is added here by a person deciding it, which is why the list is short
// and why the tests hold each line against the plan's O1.
var routineRules = []rule{
	{name: "docs", prefix: "docs/"},
	{name: "website", prefix: "website/"},
}

// sensitiveExceptions close the holes the allowlist leaves open. An exception
// narrows the allowlist, so this table can only ever move a path toward
// Sensitive, which is why a line may be added to it from a review rather than
// from a person deciding a tree: nothing here can make a change cheaper.
//
// Three shapes. A prefix that ends in a slash is a tree, and the exception
// covers it and the files in it: CI is a category of the routing rule, and
// docs/pending-workflows/ holds the workflow files a person copies into
// .github/workflows. A prefix that names a file is that one path and nothing
// else. A prefix with a suffix is the class of files under that prefix that end
// in those bytes, which is what a path this classifier cannot enumerate by name
// needs: the class, not one member of it.
//
// The website's lines are files because the tree's routine part is its copy:
// these carry what a reader installs, runs or verifies — the npm manifest and
// its lock, whose install scripts run in CI and on the owner's machine; the
// download URLs and the checksums a reader checks a console build against; the
// install commands the getting-started page offers; and the published security
// policy, which is SECURITY.md byte for byte below a generated header and the
// one website path .claude/rules/credentials-service.md scopes as
// security-sensitive.
//
// The docs tree's lines are files or one class of file for the same reason, and
// the same one that already puts docs/pending-workflows/ on this table: docs is
// the tree a reader reads, and these are not reading matter. docs/release-streams.yaml
// is the release gate's own catalogue — internal/streams refuses a file that breaks
// it, scripts/release.sh takes the CLI stream's name and version from it, and a CI
// guard fails on a bump that leaves it behind. The two scripts under
// docs/website/redesign/ are what produced the redesign brief's evidence and are
// committed to be re-runnable. The `.graph.json` line is a class because the
// artifact is drawn beside the plan it belongs to: internal/cli/plan_status.go
// walks the whole docs tree for the suffix and reads every file it finds, so
// naming today's one artifact would leave the next one — written here or in a
// repository that took Fugaro — in the cheap class until somebody remembered a
// line of this table, and a guard that depends on remembering is not one.
var sensitiveExceptions = []rule{
	{name: "CI", prefix: "docs/pending-workflows/"},
	{name: "the release streams", prefix: "docs/release-streams.yaml"},
	{name: "the plan graph artifacts", prefix: "docs/", suffix: ".graph.json"},
	{name: "the screenshot script", prefix: "docs/website/redesign/capture-screenshots.mjs"},
	{name: "the logo script", prefix: "docs/website/redesign/sample-logo-hue.py"},
	{name: "the website's dependencies", prefix: "website/package.json"},
	{name: "the website's dependency lock", prefix: "website/package-lock.json"},
	{name: "the published downloads", prefix: "website/content/downloads.json"},
	{name: "the published checksums", prefix: "website/data/console-sha256sums.txt"},
	{name: "the install commands", prefix: "website/data/install.yaml"},
	{name: "the security policy", prefix: "website/content/security.md"},
}

// Classify answers a change's class from its changed paths. Every path decides:
// a change with one guard file and nine documentation files is a guard change,
// and a class is never the majority's.
func Classify(changedPaths []string) Class {
	if len(changedPaths) == 0 {
		return Sensitive
	}
	for _, p := range changedPaths {
		if !routinePath(p) {
			return Sensitive
		}
	}
	return Routine
}

// routinePath reports whether one changed path is inside a routine tree. It is
// asked about every path of a change, so a path it cannot place — which is
// every path that is not a clean repository-relative file path — is Sensitive
// rather than a guess: `docs/../website/x` may well be website code, but a
// string that means two things is not a path this classifier may admit to the
// cheaper class.
func routinePath(p string) bool {
	if !cleanRelative(p) {
		return false
	}
	for _, e := range sensitiveExceptions {
		if e.covers(p) {
			return false
		}
	}
	for _, r := range routineRules {
		if strings.HasPrefix(p, r.prefix) {
			return true
		}
	}
	return false
}

// withinTree reports whether p names the tree prefix describes or lies inside
// it. An exception needs the tree's own path and not only the files in it,
// because an exception subtracts from a rule that has already admitted that
// path: `docs/` admits `docs/pending-workflows`, so a subtraction matching only
// paths with a slash would leave that one spelling Routine — the CI tree, the
// category the routing rule calls security-sensitive, classified as the cheap
// one. The plan's glob says the same: `docs/pending-workflows/**` matches the
// tree's own path as readily as the files under it.
//
// The routine rules are asked the narrower question on purpose. A prefix of
// `docs/` matched against the tree's own path would admit a directory, and a
// directory is not a changed file: the answer there is Sensitive, which is what
// the bare `docs` in TestUnlistedPathIsSensitive pins.
func withinTree(p, prefix string) bool {
	return p == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(p, prefix)
}

// covers reports whether an exception line covers p. A prefix ending in a slash
// names a tree, a prefix with a suffix names the class of files under it that
// end in those bytes, and a prefix that does neither names a single file, so an
// exception for `website/package.json` does not reach `website/package.json.bak`.
//
// Exceptions are matched without case, and only they are: a checkout on the
// owner's macOS, whose filesystem does not distinguish `docs/pending-workflows`
// from `docs/Pending-Workflows`, resolves the second spelling into the folder a
// person copies into .github/workflows, so a case-sensitive match would answer
// Routine for a path that is the CI tree on the machine that reads it. The
// routine rules are still asked in the bytes they were written in, so `DOCS/x`
// stays Sensitive. Folding the case of one side only moves paths toward
// Sensitive, which is the direction this table exists for.
//
// strings.ToLower folds the ASCII range and is not case folding: the case
// variants it does not reach are not this match's to guess at, and cleanRelative
// refuses their bytes before any table is asked, so a spelling folding to one of
// these lines outside ASCII is Sensitive and not Routine.
func (e rule) covers(p string) bool {
	p, prefix, suffix := strings.ToLower(p), strings.ToLower(e.prefix), strings.ToLower(e.suffix)
	if suffix != "" {
		return strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix)
	}
	if strings.HasSuffix(prefix, "/") {
		return withinTree(p, prefix)
	}
	return p == prefix
}

// cleanRelative reports whether p is a repository-relative path that means
// itself, which is the only kind of path a run records and the only kind the
// tables are written against. What it refuses is every spelling that could mean
// more than one path, or that names a path outside the checkout: an empty
// string, an absolute one, and a backslash or a NUL (a prefix match reads
// either as an ordinary byte, and a filesystem that read one as a separator
// would open a tree this table never opened), plus anything path.Clean
// rewrites — a `..` or `.` component, a doubled or trailing slash — and a
// leading `..`, which is the one escaping spelling path.Clean leaves alone: it
// rewrites `a/../b` to `b` but cannot rewrite `../b`, which names the tree above
// the checkout. Bare `..` is that same path, and both are refused here by name
// rather than left to a caller's second copy of this rule.
// Comparing the path with its own cleaned form rather than matching its bytes
// is what keeps a normalising filesystem or a second `..` from collapsing
// something into a routine tree between this answer and the checkout.
//
// A byte at or above 0x80 is refused on the same ground, and it is the one
// spelling path.Clean cannot see: the exceptions below fold case with
// strings.ToLower, which folds the ASCII range and leaves U+017F LATIN SMALL
// LETTER LONG S alone, while Unicode case folding — what a case-insensitive
// filesystem compares by — maps it to `s`, so `docs/pending-workflowſ/evil.yml`
// is the CI tree on the machine that checks it out and an ordinary docs path to
// this table. One name's composed and decomposed spellings are the same problem
// on the other axis: two byte strings that are one file on HFS+. Both buy a
// cheaper review of a guard file, so a path carrying such a byte is refused
// rather than folded, which is the direction that costs at most a review nobody
// needed.
func cleanRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] >= utf8.RuneSelf {
			return false
		}
	}
	if p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return path.Clean(p) == p
}
