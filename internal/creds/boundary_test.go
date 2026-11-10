package creds

// The import boundary around the credential state (credentials-service plan
// Convention 11; O1's ruling that creds/ is denied to every run token).
//
// What keeps a run out of the cascade's state is the DATABASE, and this file is
// not that. A run token holds no permission on creds/ at all: the rules deny the
// whole subtree to every run identity, and the service writes it with its own
// admin account (O1, D136). That is the enforcement, it is pinned by C0.6/C1.3,
// and no test in this package can stand in for it.
//
// What this file pins is a second layer, and it is worth being exact about its
// limits, because the first version of this comment was not (review finding m8).
// It is an allow-list of the packages that may IMPORT internal/creds: the
// package itself, the service that owns the state (internal/credsvc, C1.10
// onward), and the operator's CLI (internal/cli, whose `fugaro accounts`
// commands show the marks and clear a broken one, C1.21). Every package on the
// run side is off the list — internal/runner and internal/gateway (the in-runner
// gateway), internal/agent, internal/managed, internal/backend, internal/task —
// so that a refactor cannot quietly move a write of the state into the code a
// run executes, where a later loosening of the rules would hand it to a
// contributor's process.
//
// It is not "nothing here is ever linked into the runner", and a reader should
// not take it that way: cmd/fugaro links internal/cli and the runner into ONE
// binary, so the binary a run's image carries does contain the operator commands
// and, from C1.21, the state above them. The plan puts them there deliberately
// (the operator has no admin endpoint to call, so the CLI writes creds/ as the
// operator's own admin identity) and what a run cannot do is hold that identity:
// the run's token is a different principal, and the rules are what refuse it. So
// this test says "no run-side package imports the state", and never "the state
// is not in the run's process".
//
// The scan reads import lines, parsed rather than grepped, so a comment or a
// string that merely names the package is not an import and is not this test's
// business. It walks the whole module, this file included: a test file is as
// much part of a package's build as any other, and there is no exemption. The
// allow-list itself is asserted to be exactly the three entries below, so
// widening it is an edit a reviewer sees rather than a new import that slips in.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// credsPackage is the import path this boundary is about.
const credsPackage = "github.com/dimipaun/fugaro/internal/creds"

// credsImporters is the ONE-LIST of directories (module-relative, slash
// separated) whose files may import credsPackage. Widening it is a deliberate
// edit here, made with the reason written next to the new entry, and never a
// quiet new import in some future package.
var credsImporters = []string{
	"internal/creds",   // the package itself, and its tests
	"internal/credsvc", // the service: the cascade's one caller (C1.10, C1.11)
	"internal/cli",     // the operator's `fugaro accounts` commands (C1.21)
}

// TestCredsIsNotReachableFromARun fails if any file outside the allow-list
// imports the credential state.
func TestCredsIsNotReachableFromARun(t *testing.T) {
	root := moduleRoot(t)

	var found []string
	err := eachGoFile(root, func(rel, path string) error {
		if allowlisted(rel) {
			return nil
		}
		imports, err := importPaths(path)
		if err != nil {
			return err
		}
		for _, imp := range imports {
			if imp == credsPackage {
				found = append(found, rel+" imports "+credsPackage)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("a run-side file imports the credential state:\n  %s\n\nA run token holds no write on creds/ (O1), and the rules are what enforce it (C1.3); this list is what keeps a write of the state out of the code a run executes, so that a later loosening of the rules is not enough to hand the cascade's marks to a contributor's process. Move the call into the service, or add the directory to credsImporters with its reason if this is genuinely a new caller of the state.",
			strings.Join(found, "\n  "))
	}

	// The allow-list is exactly the three callers above: a fourth entry is a
	// deliberate edit here and not a quiet import somewhere (review finding m8),
	// and a name that is no longer a caller should leave the list rather than
	// widen it.
	want := []string{"internal/creds", "internal/credsvc", "internal/cli"}
	if len(credsImporters) != len(want) {
		t.Fatalf("the allow-list holds %d entries %v; want exactly %v", len(credsImporters), credsImporters, want)
	}
	for i, dir := range want {
		if credsImporters[i] != dir {
			t.Fatalf("the allow-list is %v; want exactly %v", credsImporters, want)
		}
	}
}

// allowlisted reports whether a module-relative file is one of the allowed
// importers: its directory is on the list, or is below one that is.
func allowlisted(rel string) bool {
	return under(credsImporters, rel)
}

// under reports whether a module-relative file is one of the directories on a
// one-list: its directory is on the list, or is below one that is.
func under(list []string, rel string) bool {
	for _, dir := range list {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// moduleRoot is this module's root, read from this package's own directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	// The tests of a package run with the package's directory as the working
	// directory, so the module root is two levels up.
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not at %s: %v", root, err)
	}
	return root
}

// eachGoFile walks the module and calls fn with every .go file in it, as a
// module-relative slash path and as a path to read. Dot-directories,
// underscore-directories and testdata/ are skipped, as they are for the import
// rule: neither is a package the go tool builds.
func eachGoFile(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), path)
	})
}

// importPaths parses one Go file and answers the import paths it names. Parsing
// rather than grepping is what keeps a comment or a string that mentions the
// package out of the result.
func importPaths(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		out = append(out, strings.Trim(imp.Path.Value, `"`))
	}
	return out, nil
}

// The slip half of the boundary, which is narrower than the import list above.
//
// internal/cli is a caller of this package because `fugaro accounts` reads the
// marks and clears a broken one (C1.21) — an operator's own act, on the
// operator's own machine, over the state the cascade keeps. A slip is not that.
// It is the service's grant to ONE run: the grant is what a launcher asks for,
// the claim is what a run's job presents, and the node names a run's credentials
// and holds the hash of the secret that proves the claim (D1932, D1941). No
// operator command grants, claims or ends one, and G10 keeps the slip routes to
// the grant and the claim so that no admin surface grows beside them; the
// retirement of a slip is the service's, when the run's outcome appears or its
// max runtime passes. So the packages that may name a slip are the state's own
// and the service's, and the scan below is what says so.
//
// It reads selectors rather than text: a file that imports this package and
// writes creds.Slip, or a slip's sentinel error, is a finding, while a comment
// or a string that merely spells one is not. A dot import of the state is a
// finding of its own — it binds every name unqualified, so the selector walk
// would not see a slip named at all — and so is a second import of the state
// under another name, which is a second spelling of the same names that the walk,
// reading one name, cannot see; and so is a file that cannot be parsed, because
// "cannot be checked" is not "is fine".
//
// A name is not the only way in, and the first version of this scan read only
// names (a review of this change): the slip methods hang off the store the
// marks are read through, so a file can reach Create, Slip, Claim and End
// through a store VALUE without this package's name appearing at the call site.
// Following values is not a thing a parse can do, and two attempts to narrow it
// showed why (a second review): a store copied to another name, ranged over,
// indexed, or taken as a method value reaches a slip method through a value the
// walk has lost. So the scan does not follow values. It reports the MENTION:
// outside the service, naming creds.RTDBStore or creds.SlipStore at all is a
// finding, because a file that holds a store has to write one of those names
// somewhere to get one — and this is the point a reader should take, that the
// rule is deliberately coarser than the call it is about.
//
// Three shapes are exempt, and each of them is the marks half of this boundary —
// the one thing internal/cli imports this package for (C1.21):
//
//   - the store built inline as the receiver of a marks call, and never held:
//     (&creds.RTDBStore{DB: db}).Marks(ctx);
//   - the store bound to a name a FUNCTION uses only for the marks: every other
//     appearance of that identifier is a marks receiver or another binding of
//     that name (store := &creds.RTDBStore{DB: db}; store.UpdateMark(...)). A
//     copy, an argument, an index or a method value is none of those, so the
//     name stops being marks-only and the mention becomes a finding again. The
//     binding has to be one a function body holds: a name at package level, and a
//     struct field, are reachable from the package's other files — this scan reads
//     one file at a time — so neither is ever exempt, however it is used;
//   - the store handed to the service, which may hold one: the operator's CLI
//     builds the state the credentials service serves from locally. This is the
//     service package alone and not the state's own — nothing outside hands that
//     one a store, and a shape no caller writes is a hole in the guard — and a
//     plant pins each direction. It is drawn at a function body and at the
//     hand-off, never at the store: a value built here and kept here is a store
//     this file can still reach, so the mention is exempt only where the value
//     leaves the file — the result of a return, or an argument of a call that
//     names the service — and a store built into a value bound to a name, put in
//     another value, or passed to a function of this package is a finding.
//
// The list is closed the way the importers are, and Marks and UpdateMark are not
// slip methods — that distinction is what the lists below are.
//
// The limits are the ones no parse can pass, and they are narrower than they
// were. A store that crosses a file boundary is caught in the file that writes
// it, because writing it is the only way to have one (there is no exported
// constructor in this package, and a function that answers one writes its type in
// its signature): the package-level name and the struct field are the shapes that
// cross, and both are findings whatever they are used for. What is left is a
// store that arrives from a package that may hold one (internal/credsvc) — as an
// argument, or as a result whose signature this file does not write, a value
// returned by another file of the package included — and one carried in an
// interface beside which this package's name is never written. Closing that means
// making the store type unnameable outside the service, which is a change to the
// marks API internal/cli reads through today, and it is reported rather than made
// here.

// slipImporters is the ONE-LIST of directories whose files may reach the slip
// store. It is deliberately not credsImporters: a fourth entry, or an entry
// here that is not one of these two, is an edit a reviewer reads.
var slipImporters = []string{
	"internal/creds",   // the package itself, and its tests
	"internal/credsvc", // the service: the grant and the claim (DS1.3, DS1.6)
}

// slipNames are the identifiers of the slip half of this package: naming one of
// them from another package is reaching the slip store. The list is closed
// rather than a prefix match, so a new name is an edit here.
var slipNames = []string{
	"Slip",            // the record
	"SlipStore",       // the store's interface
	"Claim",           // the claim a slip carries, and the act of claiming
	"ClaimSecretHash", // the hash a claim secret is kept as
	"PathSlip",        // the node's path
	"ErrNoSlip",       // the refusals
	"ErrSlipExists",
	"ErrSlipMalformed",
	"ErrSlipClaimed",
	"ErrSlipExpired",
	"ErrSlipIdentity",
}

// slipMethods are the SlipStore methods. A call of one of them on a value of a
// creds store is reaching the slip store just as plainly as naming SlipStore
// is, and it is the form that does not spell this package's name at the call
// site — which is why the store's TYPE is what the scan reports.
var slipMethods = []string{"Create", "Slip", "Claim", "End"}

// slipStoreTypes are the type names of this package a slip method can be called
// through: the concrete store, which implements SlipStore, and the interface
// itself. Naming either of them outside the service is a finding, because a file
// that holds a store writes one of these names to get one, however it then
// reaches the value (the marks shapes below excepted).
var slipStoreTypes = []string{"RTDBStore", "SlipStore"}

// markMethods are the two methods a file outside the service may call on a creds
// store: the marks read and the marks write, which is what `fugaro accounts`
// does (C1.21). Every other method the store carries is the slip half's.
var markMethods = []string{"Marks", "UpdateMark"}

// servicePackage is the one package a file outside may hand a creds store to: the
// service, which is the operator's CLI building the state the service serves
// from. The state's own package is deliberately not here (a review of this
// change): nothing outside hands it a store, so exempting that shape would
// widen the third hole for a caller that does not exist. The shape is pinned by
// a plant in each direction, so widening this is an edit a reviewer sees.
const servicePackage = "github.com/dimipaun/fugaro/internal/credsvc"

// TestBoundaryAllowsSlipsOnlyViaService fails if a file outside slipImporters
// reaches the slip store — by naming a slip, by naming a creds store's type, or
// by calling a slip method on one — or if the state is dot-imported anywhere.
func TestBoundaryAllowsSlipsOnlyViaService(t *testing.T) {
	// The scan is proved first, on sources this test states, so that an empty
	// result over the module below is a result and not a walk that read nothing.
	for _, c := range []struct {
		name string
		src  string
		// also is a second file of the same package. Two of the shapes below are
		// about what a per-file scan cannot see on its own, and a plant for one is
		// only the shape if the other file is there (a review of this change).
		also string
		// wantFile is the file the finding has to be on, for the plants whose point
		// is WHICH file is reported: a package's files are read one at a time, so a
		// shape that crosses the boundary has to be caught in the file that writes
		// it rather than in the one that uses it.
		wantFile string
		want     bool
	}{
		{
			name: "a package holding a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s creds.Slip\n",
			want: true,
		},
		{
			name: "a package matching a slip's refusal",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar err = creds.ErrSlipClaimed\n",
			want: true,
		},
		{
			name: "a package renaming the state's import",
			src:  "package x\n\nimport c \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s c.SlipStore\n",
			want: true,
		},
		{
			name: "a package dot-importing the state",
			src:  "package x\n\nimport . \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s Slip\n",
			want: true,
		},
		{
			name: "a package importing the state under two names",
			src:  "package x\n\nimport (\n\tc \"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n)\n\nfunc f() { st := &creds.RTDBStore{}; st.End(\"id\"); _ = c.Mark{} }\n",
			want: true,
		},
		{
			name: "a package naming a mark, which is not a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar m creds.Mark\n",
			want: false,
		},
		{
			name: "a comment that merely spells a slip",
			src:  "package x\n\n// creds.Slip is the service's grant to one run.\nvar s = 1\n",
			want: false,
		},
		{
			name: "a blank import of the state",
			src:  "package x\n\nimport _ \"github.com/dimipaun/fugaro/internal/creds\"\n",
			want: false,
		},
		{
			name: "a store built inline and claimed through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { (&creds.RTDBStore{}).Claim() }\n",
			want: true,
		},
		{
			name: "a store bound to a local and ended through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { st := &creds.RTDBStore{}; st.End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store declared as a variable and read through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar st *creds.RTDBStore\n\nfunc f() { st.Slip(\"id\") }\n",
			want: true,
		},
		{
			name: "a store held as a parameter and created through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f(st *creds.RTDBStore) { st.Create() }\n",
			want: true,
		},
		{
			name: "a store reached through a field",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\ntype t struct{ State *creds.RTDBStore }\n\nfunc f(x t) { x.State.Claim() }\n",
			want: true,
		},
		{
			name: "a store obtained from a call that names the state",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { st := creds.NewStore(db); st.End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store obtained from a call this file cannot see",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc helper() *creds.RTDBStore { return nil }\n\nfunc f() { st := helper(); st.Claim(\"id\") }\n",
			want: true,
		},
		{
			name: "a store handed out by a function",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc helper() *creds.RTDBStore { return nil }\n",
			want: true,
		},
		{
			name: "a store held as a struct field, which is holding one",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\ntype t struct{ store *creds.RTDBStore }\n",
			want: true,
		},
		{
			name: "a store's type aliased and reached through the alias",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\ntype st = creds.RTDBStore\n\nfunc f(x st) { x.End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store copied to another name and claimed through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { st := &creds.RTDBStore{}; s2 := st; s2.Claim(\"id\") }\n",
			want: true,
		},
		{
			name: "a store ranged over and claimed through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { for _, st := range []*creds.RTDBStore{{}} { st.Claim(\"id\") } }\n",
			want: true,
		},
		{
			name: "a store indexed and ended through",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { s := []*creds.RTDBStore{{}}; s[0].End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store's claim taken as a method value and called",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { g := &creds.RTDBStore{}; c := g.Claim; c(\"id\") }\n",
			want: true,
		},
		{
			name: "a store's marks read, which is not a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { (&creds.RTDBStore{}).Marks(ctx) }\n",
			want: false,
		},
		{
			name: "a store's mark updated through a local, which is not a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { st := &creds.RTDBStore{}; st.UpdateMark(ctx, \"n\", nil) }\n",
			want: false,
		},
		{
			name: "a store inferred by var and its marks read, which is not a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { var st = &creds.RTDBStore{}; st.Marks(ctx) }\n",
			want: false,
		},
		{
			name: "a store handed to the service from a function, which may hold one",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\nfunc f() credsvc.Config { return credsvc.Config{State: &creds.RTDBStore{}} }\n",
			want: false,
		},
		{
			name: "a store handed to the service at package level, which another file can claim through",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\nvar c = credsvc.Config{State: &creds.RTDBStore{}}\n",
			want: true,
		},
		{
			name: "a store handed to the service as an argument of the service's own call",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\nfunc f() { credsvc.Serve(credsvc.Config{State: &creds.RTDBStore{}}) }\n",
			want: false,
		},
		{
			name: "a store handed to the service into a value the file keeps",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\nfunc f() { c := credsvc.Config{State: &creds.RTDBStore{}}; c.State.End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store handed to the service inside another value the file keeps",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\ntype h struct{ C credsvc.Config }\n\nfunc f() { x := h{C: credsvc.Config{State: &creds.RTDBStore{}}}; x.C.State.End(\"id\") }\n",
			want: true,
		},
		{
			name: "a store handed to the service through a function of this package",
			src:  "package x\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/creds\"\n\t\"github.com/dimipaun/fugaro/internal/credsvc\"\n)\n\nfunc g(c credsvc.Config) { c.State.Claim(\"id\") }\n\nfunc f() { g(credsvc.Config{State: &creds.RTDBStore{}}) }\n",
			want: true,
		},
		{
			name: "a store handed to the state's own package, which is not the service",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { sel := creds.Selector{Store: &creds.RTDBStore{}}; sel.Pick(ctx) }\n",
			want: true,
		},
		{
			name: "a store bound and used for nothing, which is not the marks either",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar st = &creds.RTDBStore{}\n",
			want: true,
		},
		{
			name: "a store whose name escapes to a copy, which the marks shapes do not cover",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nfunc f() { st := &creds.RTDBStore{}; st.Marks(ctx); s2 := st; _ = s2 }\n",
			want: true,
		},
		{
			name: "a store bound at package level and used only for the marks",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar st = &creds.RTDBStore{DB: db}\n\nfunc f() { st.Marks(ctx) }\n",
			want: true,
		},
		{
			name:     "a store bound at package level and claimed through in another file",
			src:      "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar st = &creds.RTDBStore{DB: db}\n\nfunc f() { st.Marks(ctx) }\n",
			also:     "package x\n\nfunc g() { st.End(\"s-1\") }\n",
			wantFile: "planted.go",
			want:     true,
		},
		{
			name: "a store held as a struct field and used only for the marks",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\ntype t struct{ st *creds.RTDBStore }\n\nfunc (x t) f() { x.st.Marks(ctx) }\n",
			want: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A plant is one file, or a package of them. The two-file shapes are read
			// the way the module is: file by file, which is what they are here to
			// hold.
			files := []string{c.src}
			if c.also != "" {
				files = append(files, c.also)
			}
			var found []string
			for i, src := range files {
				rel := "planted.go"
				if i > 0 {
					rel = "also.go"
				}
				found = append(found, slipReferences(rel, rel, []byte(src))...)
			}
			if c.want && len(found) == 0 {
				t.Fatal("the scan read a source that reaches the slip store as reaching nothing: a scan that cannot fail is not a scan")
			}
			if !c.want && len(found) > 0 {
				t.Fatalf("the scan refused a source that does not reach the slip store: %v", found)
			}
			if c.wantFile != "" && !slices.ContainsFunc(found, func(s string) bool { return strings.HasPrefix(s, c.wantFile+" ") }) {
				t.Fatalf("the finding is not on %s, the file that writes the store: %v", c.wantFile, found)
			}
		})
	}

	var found []string
	err := eachGoFile(moduleRoot(t), func(rel, path string) error {
		if under(slipImporters, rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = append(found, slipReferences(path, rel, src)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("a file outside the service reaches the slip store:\n  %s\n\nA slip is the service's grant to ONE run (D1932): the grant is what a launcher asks for, the claim is what the run's job presents, and no operator command grants, claims or ends one (G10). Reading the marks is the one thing this package is imported for outside the service (fugaro accounts, C1.21), and it is done with the store built inline as a marks receiver, held in a name used only for the marks, or handed to the service: a file outside the service has no other business naming a creds store, because naming one is how a store is got, and a store is what the slip half hangs off. Read or write slips from internal/credsvc, or add the directory to slipImporters with its reason if this is genuinely a new caller.", strings.Join(found, "\n  "))
	}

	// The one-list is exactly the two callers above, for the reason
	// credsImporters is held to its own: a third entry is an edit here and not a
	// quiet import somewhere.
	want := []string{"internal/creds", "internal/credsvc"}
	if !slices.Equal(slipImporters, want) {
		t.Fatalf("slipImporters is %v; want exactly %v", slipImporters, want)
	}
}

// slipReferences answers the ways one Go file reaches the slip store. src is
// passed in rather than read from path, so the plants above run through the same
// walk the module does.
//
// Three ways, because a name is not the only one: the identifiers of the slip
// half named through this package, a mention of a creds store type (which the
// marks shapes except), and a slip method called on a store the file bound.
func slipReferences(path, rel string, src []byte) []string {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil || f == nil {
		return []string{rel + " cannot be parsed, so its slip references cannot be checked"}
	}
	name, dot, ok := credsImport(f)
	if dot {
		return []string{rel + " dot-imports " + credsPackage + ", so every name it holds — a slip's included — is bound unqualified and unreadable by this scan"}
	}
	// One name is what a caller writes. Two is a file whose other name this walk
	// cannot see, and a mention this walk cannot see is a mention it does not
	// report: a finding of its own, as the dot import is, rather than a walk that
	// reads some of a file's mentions and calls it clean.
	if names := credsImportNames(f); len(names) > 1 {
		return []string{rel + " imports " + credsPackage + " under more than one name (" + strings.Join(names, ", ") + "), so the name this scan reads is not the only one the file writes"}
	}
	if !ok {
		return nil
	}
	// The mentions of the store type this file is allowed to write: the marks
	// shapes, and a store handed to a package that may hold one.
	allowed := exemptStoreMentions(f, name)
	// The identifiers this file binds a creds value to: a slip method called on
	// one of them is the call the mention walk below cannot see.
	stores := credsStoreNames(f, name)
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == name {
			// The name walk first, so SlipStore — a name of the slip half
			// besides being a store a method is called on — has no exempt shape
			// at all.
			if slices.Contains(slipNames, sel.Sel.Name) {
				found = append(found, rel+" names "+name+"."+sel.Sel.Name)
				return true
			}
			// The node the scan is about: a file that holds a store writes this
			// name, and following the value from here is what two attempts
			// showed a parse cannot do.
			if isStoreMention(n, name) {
				if !allowed[n] {
					found = append(found, rel+" names "+name+"."+sel.Sel.Name+", the store the slip methods hang off")
				}
				return true
			}
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			if slices.Contains(stores, x.Name) && slices.Contains(slipMethods, sel.Sel.Name) {
				found = append(found, rel+" calls "+x.Name+"."+sel.Sel.Name+", a slip method on a "+name+" store it holds")
			}
			return true
		}
		// A store the file did not bind to a name: one it built inline
		// ((&creds.RTDBStore{DB: db}).Claim), and one it reached through a field
		// of a value whose type is a store (s.State.Claim).
		if slices.Contains(slipMethods, sel.Sel.Name) && (mentionsCreds(sel.X, name) || selectsCredsStore(sel.X, stores)) {
			found = append(found, rel+" calls ."+sel.Sel.Name+" on a "+name+" store it reached without binding it")
		}
		return true
	})
	return found
}

// exemptStoreMentions answers the mentions of a creds store type a file outside
// the service is allowed to write — the three shapes the section comment names.
// They are keyed by their own node, so a file that spells the type once for the
// marks and once for a store it claims through is still found for the second.
func exemptStoreMentions(f *ast.File, pkg string) map[ast.Node]bool {
	allowed := map[ast.Node]bool{}
	// A store built inline as the receiver of a marks call, and never held.
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !slices.Contains(markMethods, sel.Sel.Name) {
			return true
		}
		if t := storeLiteralType(sel.X, pkg); t != nil {
			allowed[t] = true
		}
		return true
	})
	// A store bound to a name a function uses only for the marks: every other
	// appearance of that identifier is a marks receiver or another binding of the
	// name. A copy, an argument, an index or a method value is none of those, so
	// the name stops being marks-only and the mention stays a finding.
	//
	// The binding has to be one a FUNCTION body holds (a review of this change).
	// A name bound at package level is reachable from the package's other files,
	// and this scan reads one file at a time: `var st = &creds.RTDBStore{}` with
	// st.Marks(ctx) here and st.End(id) in another file of the package would be
	// exempt by the marks rule while the second file is what claims. A package
	// level name is therefore never exempt, which is the shape that crosses a
	// file boundary and the reason the exemption is drawn here rather than over
	// the package.
	only := marksOnlyNames(f)
	for _, body := range functionBodies(f) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range d.Rhs {
					if id, ok := identAt(d.Lhs, i); ok && only[id.Name] {
						storeMentions(allowed, rhs, pkg)
					}
				}
			case *ast.ValueSpec:
				for i, v := range d.Values {
					if i < len(d.Names) && only[d.Names[i].Name] {
						storeMentions(allowed, v, pkg)
					}
				}
			}
			return true
		})
	}
	// A store handed to a package that may hold one — the operator's CLI builds
	// the state the credentials service serves from. It is drawn at a function
	// body for the reason above (a package-level hand-off is a store another file
	// can claim through) and at the hand-off itself, never at the store (a review
	// of this change): a value built here and KEPT here is a store this file can
	// still reach, so the mention is exempt only where the value leaves the file —
	// the result of a return, or an argument of a call that names the service —
	// and a store built into a value bound to a name, put in another value, or
	// passed to a function of this package is a finding again.
	held := serviceImportNames(f)
	for _, body := range functionBodies(f) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.ReturnStmt:
				for _, res := range d.Results {
					handedOn(allowed, res, held, pkg)
				}
			case *ast.CallExpr:
				if namesPackage(d.Fun, held) {
					for _, arg := range d.Args {
						storeMentions(allowed, arg, pkg)
					}
				}
			}
			return true
		})
	}
	return allowed
}

// handedOn exempts the store mentions an expression hands to the service: the
// service literals it carries, and nothing else. A store the expression merely
// holds — the bare &creds.RTDBStore{} a function hands out, or one in a literal
// of another package's type — is not a hand-off and stays a finding.
func handedOn(allowed map[ast.Node]bool, e ast.Expr, held []string, pkg string) {
	if e == nil {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !namesPackage(lit.Type, held) {
			return true
		}
		for _, el := range lit.Elts {
			storeMentions(allowed, el, pkg)
		}
		return true
	})
}

// functionBodies answers the bodies of the functions a file declares, which is
// where a binding the scan exempts has to be: a name a function holds cannot be
// read by another file, and a name at package level can.
func functionBodies(f *ast.File) []*ast.BlockStmt {
	var bodies []*ast.BlockStmt
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			bodies = append(bodies, fn.Body)
		}
	}
	return bodies
}

// storeMentions adds every mention of a creds store type in an expression to a
// set.
func storeMentions(set map[ast.Node]bool, e ast.Expr, pkg string) {
	if e == nil {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		if isStoreMention(n, pkg) {
			set[n] = true
		}
		return true
	})
}

// isStoreMention reports whether a node is a mention of a creds store type,
// however the file spelled it.
func isStoreMention(n ast.Node, pkg string) bool {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg && slices.Contains(slipStoreTypes, sel.Sel.Name)
}

// marksOnlyNames answers the identifiers the file uses only for the marks: at
// least one marks receiver, and no appearance that is neither a receiver nor
// another binding of the name. Appearances are counted rather than scoped,
// deliberately: the file is all the scan has, and a name with more appearances
// than the two shapes account for is a name whose uses this rule cannot see —
// which fails closed, and names nothing a reader cannot check by looking at the
// file. Where the binding has to be is the caller's question, not this one: a
// name bound in a function body can be exempt (exemptStoreMentions) and a name
// at package level cannot. The marks receiver is required rather than left to the
// count (a review of this change): a name bound once and used for nothing at all
// satisfies a count that only asks for no other uses, and "no other uses" is not
// the marks.
func marksOnlyNames(f *ast.File) map[string]bool {
	appearances := map[string]int{}
	bindings := map[string]int{}
	marks := map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.Ident:
			appearances[d.Name]++
		case *ast.AssignStmt:
			for _, lhs := range d.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					bindings[id.Name]++
				}
			}
		case *ast.ValueSpec:
			for _, id := range d.Names {
				bindings[id.Name]++
			}
		case *ast.SelectorExpr:
			if x, ok := d.X.(*ast.Ident); ok && slices.Contains(markMethods, d.Sel.Name) {
				marks[x.Name]++
			}
		}
		return true
	})
	only := map[string]bool{}
	for name, n := range appearances {
		if name != "_" && marks[name] > 0 && n == bindings[name]+marks[name] {
			only[name] = true
		}
	}
	return only
}

// identAt answers the identifier an assignment binds at one position, if it
// binds one there at all.
func identAt(lhs []ast.Expr, i int) (*ast.Ident, bool) {
	if i >= len(lhs) {
		return nil, false
	}
	id, ok := lhs[i].(*ast.Ident)
	return id, ok
}

// storeLiteralType answers the store mention inside an expression that builds
// one — the composite literal's type node, which is the mention itself — or nil
// when the expression builds something else. &, and the parentheses a receiver
// is written with, are unwrapped.
func storeLiteralType(e ast.Expr, pkg string) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return nil
			}
			e = x.X
		case *ast.CompositeLit:
			if credsStoreType(x.Type, pkg) == "" {
				return nil
			}
			return x.Type
		default:
			return nil
		}
	}
}

// serviceImportNames answers the local names a file binds the service package to:
// its own name unless the import renames it. A blank or dot import binds no name
// a call can be made through, and a dot import is already a finding of its own.
func serviceImportNames(f *ast.File) []string {
	var names []string
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != servicePackage {
			continue
		}
		if imp.Name == nil {
			if i := strings.LastIndex(path, "/"); i >= 0 {
				names = append(names, path[i+1:])
			}
			continue
		}
		if imp.Name.Name != "_" && imp.Name.Name != "." {
			names = append(names, imp.Name.Name)
		}
	}
	return names
}

// namesPackage reports whether an expression names one of these packages, as a
// call or a literal of it does.
func namesPackage(e ast.Expr, names []string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && slices.Contains(names, x.Name)
}

// credsStoreNames answers the identifiers a file binds a creds value to: a
// local, a parameter, a result, a struct field. A slip method called on one of
// them is reaching the slip store through a value, which is the way in that
// spells this package's name only where the value was made.
//
// The binding is anything that NAMES this package rather than only a store's
// type, because a call the scan cannot see the signature of — creds.NewStore(db)
// — yields a value from the package just as a composite literal does. The cost
// of the wider net is an identifier that is not a store at all (a Mark from
// creds.Cleared()); it can only be a finding if a slip method is called on it,
// which nothing but a store has.
func credsStoreNames(f *ast.File, pkg string) []string {
	var names []string
	add := func(id *ast.Ident) {
		if id != nil && id.Name != "_" {
			names = append(names, id.Name)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range d.Rhs {
				if i < len(d.Lhs) && mentionsCreds(rhs, pkg) {
					if id, ok := d.Lhs[i].(*ast.Ident); ok {
						add(id)
					}
				}
			}
		case *ast.ValueSpec:
			if credsStoreType(d.Type, pkg) != "" {
				for _, id := range d.Names {
					add(id)
				}
				break
			}
			for i, v := range d.Values {
				if i < len(d.Names) && mentionsCreds(v, pkg) {
					add(d.Names[i])
				}
			}
		case *ast.Field:
			if credsStoreType(d.Type, pkg) != "" {
				for _, id := range d.Names {
					add(id)
				}
			}
		}
		return true
	})
	return names
}

// credsStoreType answers the name of the creds store type an expression spells
// ("RTDBStore" or "SlipStore"), or "" when it spells none. It is the one walk
// the rest of the scan asks "is this a store?" through, and it reads the written
// type only: a value that merely holds one is not a spelling of it.
func credsStoreType(e ast.Expr, pkg string) string {
	if e == nil {
		// A declaration with no type written out (var st = ..., a result with no
		// type): nothing to walk, and ast.Walk panics on a nil node.
		return ""
	}
	found := ""
	ast.Inspect(e, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == pkg && slices.Contains(slipStoreTypes, sel.Sel.Name) {
			found = sel.Sel.Name
		}
		return true
	})
	return found
}

// mentionsCreds reports whether an expression names this package at all: a value
// it yields, however the expression is shaped.
func mentionsCreds(e ast.Expr, pkg string) bool {
	if e == nil {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == pkg {
			found = true
		}
		return true
	})
	return found
}

// selectsCredsStore reports whether an expression is a field of a value the file
// bound a creds store to, as in s.State.Claim.
func selectsCredsStore(e ast.Expr, stores []string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && slices.Contains(stores, sel.Sel.Name)
}

// credsImportNames answers every local name a file binds this package to. A
// caller writes one; a second name is a second spelling of the same names, and
// the walk reads one spelling. A blank import binds no name and is not one.
func credsImportNames(f *ast.File) []string {
	var names []string
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != credsPackage {
			continue
		}
		switch {
		case imp.Name == nil:
			names = append(names, "creds")
		case imp.Name.Name == "_":
		default:
			names = append(names, imp.Name.Name)
		}
	}
	return names
}

// credsImport answers how a file reaches this package: the local name it binds
// (the package's own name unless the import renames it) and whether it is a dot
// import. A blank import binds nothing and is not a caller.
func credsImport(f *ast.File) (name string, dot bool, ok bool) {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != credsPackage {
			continue
		}
		switch {
		case imp.Name == nil:
			return "creds", false, true
		case imp.Name.Name == ".":
			return "", true, true
		case imp.Name.Name == "_":
			continue
		default:
			return imp.Name.Name, false, true
		}
	}
	return "", false, false
}
