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
)

// Class is what a change is: the key O4's model setting is looked up by and the
// one a decision agent's model and its review level follow.
type Class string

const (
	// Routine is a change whose every path is inside one of the routine trees:
	// documentation and the website, the same two trees the owner's routing
	// rule sends to the light reviewer.
	Routine Class = "routine"
	// Sensitive is every change the classifier cannot place inside a routine
	// tree — a credential, a guard, a prompt, a recipe, a workflow, the
	// agent's environment, Terraform or IAM, the console's writes — and every
	// change whose paths it could not read. It is never reviewed below the
	// Opus class, and no configuration lowers it (D1234, O4).
	Sensitive Class = "sensitive"
)

// rule is one line of a table: a name the routing text's words can be held
// against, and the repository-relative path prefix it matches.
type rule struct {
	name   string
	prefix string
}

// routineRules is the allowlist (D1234): the trees a change may be Routine in.
// A tree is added here by a person deciding it, which is why the list is short
// and why the tests hold each line against the plan's O1.
var routineRules = []rule{
	{name: "docs", prefix: "docs/"},
	{name: "website", prefix: "website/"},
}

// sensitiveExceptions close the one hole the allowlist leaves open: CI is a
// category of the routing rule, and docs/pending-workflows/ holds the workflow
// files a person copies into .github/workflows. An exception narrows the
// allowlist, so the second table can only ever move a path toward Sensitive.
var sensitiveExceptions = []rule{
	{name: "CI", prefix: "docs/pending-workflows/"},
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
		if strings.HasPrefix(p, e.prefix) {
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

// cleanRelative reports whether p is a repository-relative path that means
// itself, which is the only kind of path a run records and the only kind the
// tables are written against. What it refuses is every spelling that could mean
// more than one path: an empty string, an absolute one, and a backslash or a
// NUL (a prefix match reads either as an ordinary byte, and a filesystem that
// read one as a separator would open a tree this table never opened), plus
// anything path.Clean rewrites — a `..` or `.` component, a doubled or
// trailing slash.
// Comparing the path with its own cleaned form rather than matching its bytes
// is what keeps a normalising filesystem or a second `..` from collapsing
// something into a routine tree between this answer and the checkout.
func cleanRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	return path.Clean(p) == p
}
