package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// noprompt_lint_test.go is the static half of D1547 (docs/no-prompt-policy):
// what holds the no-prompt policy while the confirmations are removed is not
// that every reader was found by hand once, but that a read of the shapes this
// package asks a person with cannot be added unnoticed. The rule is about the
// SHAPE of a read rather than about a list of callers, and the walk covers
// every declaration of the package — functions, methods, and the function
// literals a package-level var is bound to (pair.go's pairTerminal is the shape
// of that plumbing, so a read written inside one is the same read).
//
// What it matches are the SHAPES a stream is read with here, and a shape is not
// a verdict: no syntax says whether a stream is stdin, since a reader that
// arrived as a struct field or as a parameter — the wizard's r.in,
// readConfirmLine's in — is read exactly like a reader over a response body. The
// four lists below are the verdict, and a site's shape is the evidence a failure
// prints. A read of stdin for a decision is one of
//
//   - bufio.NewReader / NewReaderSize / NewScanner / NewReadWriter — the line
//     reader this package asks a person with, however it is built. It is
//     matched by the package the call comes from and not by its text, so
//     `import b "bufio"` and `import . "bufio"` are the same read and a
//     function of the same name in this package is not one.
//   - .ReadString / .ReadBytes / .ReadLine / .ReadRune / .ReadByte / .ReadSlice
//     on such a reader: the reading half of the same reader, and what no data
//     read in the package uses.
//   - io.ReadFull / io.ReadAtLeast, which fill a buffer from the reader they
//     are given, unless that reader belongs to a package (crypto/rand's
//     rand.Reader — no person is behind it). io.ReadAll is deliberately not a
//     shape: it is how every data read in this package is written, and the four
//     of them are named in dataReadSites.
//   - .Read(p) — a byte-level read of a reader, which is how readConfirmLine is
//     written itself (`n, err := in.Read(b)`) and how the wizard's reader is:
//     the package's store reads are Read(ctx, key) and a data path reads
//     through io.ReadAll or a decoder, so one argument on a receiver that is
//     not an imported package is this shape and not one of those.
//   - readConfirmLine — the confirmation line itself.
//   - term.ReadPassword — the hidden read, which is the value entry's engine.
//   - fmt.Scan/Scanf/Scanln/Fscan/Fscanf/Fscanln — the other way a line of
//     input becomes a value. This package uses none today; the shape is here so
//     the first one that appears is a failure rather than a read this rule is
//     blind to.
//
// What the rule cannot see is a read that arrives another way: a decoder over
// stdin (json.NewDecoder(cmd.InOrStdin()).Decode(&v) waits for a person as
// surely as ReadString does, and the row that pins the silence says so), a
// channel, a syscall on fd 0. That is a blind spot and not an exemption: no list
// here says such a read is safe. The half that catches it is the closed-stdin
// harness T2 owes — internal/cli/noprompt_harness_test.go, which is not in the
// tree yet (only T2a's static gcloud scan has landed) — and until it lands a
// prompt written as a decoder is a hole this lint knows it has.
//
// It reports where the reading happens, not who called the reader. The alias
// the plan calls out (`in := cmd.InOrStdin(); bufio.NewReader(in)`) is a row of
// TestLintCatchesAnAliasedStdinReader: the shape is matched whatever expression
// the reader was built from, so a rule keyed on the literal cmd.InOrStdin()
// could not see a reader that arrived through a variable, and the wizard's own
// reader field is the same read one function away. A comment or a string that
// mentions a read is not a read, which is what keeps this an AST rule and not a
// grep.
//
// Four lists hold the answer, and none of them is a promise:
//
//   - pendingSites: every decision read the package still makes, with the task
//     of docs/no-prompt-policy/implementation-plan.md that removes it — the
//     task that owns the file, since no level holds two writers of one file.
//     The list is the debt the policy is paid off against, and it is exact in
//     both directions — a site that is gone and still listed fails, and a site
//     that is not listed fails — so the task that removes a read deletes its
//     entry in the same commit and cannot leave the lint blind. It reaches
//     empty when the confirmations are gone.
//   - sanctionedSites: the reads the policy keeps — the `--ask` helper (D1544,
//     the owner's one sanctioned exemption) and value entry with the three
//     reads it is made of (D1540: a value is not a confirmation and cannot be
//     defaulted, so it stays a terminal read that fails at once everywhere
//     else). Each is asserted to be found, so the lint says out loud that it
//     allows a read rather than having lost one. Value entry is a call down from
//     the helper — readValue reads through readConfirmLine, and its hidden path
//     through readHidden to readHiddenLine — so the engines are listed by name
//     rather than the helper alone: an exemption for a helper whose engine was
//     unlisted would leave the lint reporting the one read the helper is allowed
//     to make. readHiddenPEM is the fourth, and the one that is not a call down
//     from readValue: it is the multi-line shape of the same value entry (a PEM
//     cannot be typed on one line, and the one-line helper cannot take it),
//     called from the secret store's own door, which T12 owns. Listing it as
//     debt would be a lie about the policy — D1540 keeps value entry, and no
//     task of the plan removes this read — so it is sanctioned with its reason
//     like the other three. None of them is a confirmation: they are how a value
//     is entered.
//   - dataReadSites: the reads F1 names as neither confirmations nor values
//     (`--task-file -`, `--review-levels -`, the exec task file, the
//     credentials bundle). These are asserted NOT to be found, so a data read
//     that grows a decision read fails here instead of passing as an exemption
//     nobody re-reads.
//   - notADecisionSites: the fourth answer, and the one whose absence made the
//     lint's own failure message impossible to take — a read of one of the
//     shapes above that is not a read of a person (a response body, a file, a
//     buffer) and the reason it is not. Each is asserted to be FOUND, like a
//     sanctioned site, so the entry and the read it explains live and die
//     together. Shapes cannot tell these apart from stdin, which is why the
//     answer has to be written down by a person; a prompt belongs in no list.

// decisionRead is one place the package reads a decision from stdin: the file
// and function it is in, which is the site the lists above name, and the shape
// of the read, which is the evidence a failure prints.
type decisionRead struct {
	file, fn, form string
}

// decisionReadsInPackage walks every non-test file of the package. Nothing is
// type-checked — a read is a call of one of the shapes above wherever it
// appears — and the walk is over the whole package rather than a list of files,
// so a file added later is covered without touching this test.
func decisionReadsInPackage(t *testing.T) []decisionRead {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []decisionRead
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		out = append(out, scanDecisionReads(file, name)...)
	}
	return out
}

// decisionReadsInSource is the same walk over one source, which is how
// TestLintCatchesAnAliasedStdinReader feeds the rule the shapes whose answer is
// known. The rows are parsed and not compiled: what the rule reads is the tree.
func decisionReadsInSource(t *testing.T, src string) []decisionRead {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "row.go", "package row\n"+src, 0)
	if err != nil {
		t.Fatalf("parsing the row %q: %v", src, err)
	}
	return scanDecisionReads(file, "row.go")
}

// scanDecisionReads reports every decision read in one parsed file, each under
// the declaration it sits in: a function or method by its own name — a closure
// belongs to the function that writes it, so a read inside the
// `go func(){…}()` pairReadCode reads its code in is the site of pairReadCode —
// and a package-level var by the name the literal is bound to, which is the
// declaration a reader plumbed that way lives in.
func scanDecisionReads(file *ast.File, name string) []decisionRead {
	imports := localPackageNames(file)
	var out []decisionRead
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			out = append(out, readsIn(d.Body, imports, name, d.Name.Name)...)
		case *ast.GenDecl:
			if d.Tok != token.VAR && d.Tok != token.CONST {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range vs.Values {
					out = append(out, readsIn(v, imports, name, valueName(vs, i))...)
				}
			}
		}
	}
	return out
}

// readsIn walks one body or expression — a function's, or the value a
// package-level var is bound to — and reports each decision read in it under
// fn.
func readsIn(root ast.Node, imports map[string]string, file, fn string) []decisionRead {
	var out []decisionRead
	ast.Inspect(root, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if form := decisionReadForm(call, imports); form != "" {
			out = append(out, decisionRead{file: file, fn: fn, form: form})
		}
		return true
	})
	return out
}

// valueName is the name a package-level value's read is reported under: the
// name that value is bound to, so `var pairTerminal = func(…) io.Reader {…}`
// belongs to pairTerminal. A value with one name and several values — or, as a
// var without names cannot have values, the reverse — takes the name at the
// value's own position where there is one, which is where the read is written.
func valueName(vs *ast.ValueSpec, value int) string {
	if len(vs.Names) == 0 {
		return "var"
	}
	if value < len(vs.Names) {
		return vs.Names[value].Name
	}
	return vs.Names[len(vs.Names)-1].Name
}

// localPackageNames is what each import is called in this file, so the rule
// matches the package a call really comes from: `b "bufio"` is bufio, and a
// local function called NewReader is not.
func localPackageNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}
	return names
}

// decisionReadForm is the rule itself: the shape of a call that reads a
// decision from stdin, or "" for every other call in the package. The reader
// and the read are the same site whether they are written as one expression or
// two, which is what makes an aliased reader — and the reader the wizard keeps
// in its own reader field — as visible as an inline one.
func decisionReadForm(call *ast.CallExpr, imports map[string]string) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == "readConfirmLine" {
			return "readConfirmLine"
		}
		// A dot-import puts a package's names in this file's own namespace,
		// where a bare NewReader is the same read; any other function of that
		// name is not, which is why the package is matched through the import
		// rather than by the name alone.
		return dotImportForm(fun.Name, call, imports)
	case *ast.SelectorExpr:
		if form := packageCallForm(fun, call, imports); form != "" {
			return form
		}
		// A reader this package built is read here or not at all — a line, a
		// rune, a byte, a delimiter — and no data read in the package uses one.
		// The receiver is checked not to be a package for the same reason as
		// Read below: io.ReadSlice and a store's own read are not this shape.
		switch fun.Sel.Name {
		case "ReadString", "ReadBytes", "ReadLine", "ReadRune", "ReadByte", "ReadSlice":
			if !importedPackage(fun.X, imports) {
				return fun.Sel.Name
			}
		}
		// The same reader read a byte at a time is the same read one level
		// down: readConfirmLine is a loop over in.Read(b), and the wizard's
		// reader is one too. What separates it from the package's data reads is
		// the shape of the call and not the type of the receiver — a byte-level
		// read takes one argument, `Read(p []byte)`, while every store read
		// here takes a context and a key, and a package function of the same
		// name (streams.Read) is not a reader at all.
		if fun.Sel.Name == "Read" && len(call.Args) == 1 && !importedPackage(fun.X, imports) {
			return "Read"
		}
	}
	return ""
}

// packageCallForm is the rule for a call that names a package: the line
// reader's own constructors and reads, the hidden read, the formatted reads,
// and a read filled from a reader. Each is matched through the import map, so
// an alias is the same call and a same-named function in this package is not.
func packageCallForm(fun *ast.SelectorExpr, call *ast.CallExpr, imports map[string]string) string {
	pkg, ok := fun.X.(*ast.Ident)
	if !ok {
		return ""
	}
	switch imports[pkg.Name] {
	case "bufio":
		if isReaderConstructor(fun.Sel.Name) {
			return "bufio." + fun.Sel.Name
		}
	case "io":
		if isFilledRead(fun.Sel.Name) && !fromPackageReader(call, imports) {
			return "io." + fun.Sel.Name
		}
	case "golang.org/x/term":
		if fun.Sel.Name == "ReadPassword" {
			return "term.ReadPassword"
		}
	case "fmt":
		switch fun.Sel.Name {
		case "Scan", "Scanf", "Scanln", "Fscan", "Fscanf", "Fscanln":
			return "fmt." + fun.Sel.Name
		}
	}
	return ""
}

// dotImportForm is the same rule for a file that dot-imports one of these
// packages, where the call arrives as a bare name.
func dotImportForm(name string, call *ast.CallExpr, imports map[string]string) string {
	switch imports["."] {
	case "bufio":
		if isReaderConstructor(name) {
			return "bufio." + name
		}
	case "io":
		if isFilledRead(name) && !fromPackageReader(call, imports) {
			return "io." + name
		}
	case "golang.org/x/term":
		if name == "ReadPassword" {
			return "term.ReadPassword"
		}
	}
	return ""
}

// isReaderConstructor is how the reader this package asks a person with is
// built: over the terminal at a size of its own, or as the reader half of a
// read-writer. All of them read stdin the same way NewReader does, so a prompt
// written with one is the same prompt.
func isReaderConstructor(name string) bool {
	switch name {
	case "NewReader", "NewReaderSize", "NewScanner", "NewReadWriter":
		return true
	}
	return false
}

// isFilledRead is a read of a whole reader rather than of a line: io.ReadFull
// and io.ReadAtLeast fill the buffer they are given from the reader they are
// given, so the first argument is the stream and the shape is the same
// decision when that stream is stdin. io.ReadAll is deliberately not here: it
// is how every data read in this package is written (the four named in
// dataReadSites), so it is the data reads' own shape rather than this rule's.
func isFilledRead(name string) bool {
	return name == "ReadFull" || name == "ReadAtLeast"
}

// fromPackageReader reports whether a filled read draws from a reader a
// package owns rather than from a stream handed in: creds.go's key material
// comes from crypto/rand's rand.Reader, which is not a person and not stdin.
// A package's own *stream* — os.Stdin, or a reader this package built — is not
// exempt, since only a package-level reader value is this shape.
func fromPackageReader(call *ast.CallExpr, imports map[string]string) bool {
	if len(call.Args) == 0 {
		return false
	}
	sel, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Reader" {
		return false
	}
	return importedPackage(sel.X, imports)
}

// importedPackage reports whether a selector's left side is a package name —
// `streams.Read(path)` is a package function, not a reader being read, and the
// rule must not sweep it in with the byte-level reads.
func importedPackage(x ast.Expr, imports map[string]string) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return false
	}
	_, isPackage := imports[id.Name]
	return isPackage
}

// TestLintCatchesAnAliasedStdinReader is the not-vacuous half of the lint: it
// runs the rule over sources whose answer is known, so a green
// TestNoDecisionReadOutsideTheTwoHelpers means the package is clean rather than
// that the rule stopped seeing reads. The first row is the plan's own example —
// a reader carried in a variable — and the rest are the shapes a future
// confirmation would most plausibly be written as.
func TestLintCatchesAnAliasedStdinReader(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want []string
	}{
		{
			"a reader carried in a variable",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { in := cmd.InOrStdin(); _, _ = bufio.NewReader(in).ReadString('\\n') }",
			[]string{"bufio.NewReader", "ReadString"},
		},
		{
			"the stdin expression inline",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { _, _ = bufio.NewReader(cmd.InOrStdin()).ReadString('\\n') }",
			[]string{"bufio.NewReader", "ReadString"},
		},
		{
			"the reader put in a variable of its own",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { r := bufio.NewReader(cmd.InOrStdin()); _, _ = r.ReadString('\\n') }",
			[]string{"bufio.NewReader", "ReadString"},
		},
		{
			"os.Stdin through a scanner",
			"import \"bufio\"\nfunc f() { _ = bufio.NewScanner(os.Stdin) }",
			[]string{"bufio.NewScanner"},
		},
		{
			"the confirmation line",
			"func f(cmd *cobra.Command) { _, _ = readConfirmLine(cmd.InOrStdin()) }",
			[]string{"readConfirmLine"},
		},
		{
			"a hidden read",
			"import \"golang.org/x/term\"\nfunc f(fd int) { _, _ = term.ReadPassword(fd) }",
			[]string{"term.ReadPassword"},
		},
		{
			"an aliased import",
			"import b \"bufio\"\nfunc f(cmd *cobra.Command) { _ = b.NewReader(cmd.InOrStdin()) }",
			[]string{"bufio.NewReader"},
		},
		{
			"a dot-imported constructor",
			"import . \"bufio\"\nfunc f(cmd *cobra.Command) { _ = NewReader(cmd.InOrStdin()) }",
			[]string{"bufio.NewReader"},
		},
		{
			"stdin read a byte at a time",
			"import \"os\"\nfunc f() { b := make([]byte, 1); _, _ = os.Stdin.Read(b) }",
			[]string{"Read"},
		},
		{
			"the confirmation line written out by hand",
			"func f(in io.Reader) { b := make([]byte, 1); _, _ = in.Read(b) }",
			[]string{"Read"},
		},
		{
			"a formatted read",
			"import \"fmt\"\nfunc f(cmd *cobra.Command) { var s string; _, _ = fmt.Fscan(cmd.InOrStdin(), &s) }",
			[]string{"fmt.Fscan"},
		},
		{
			"a sized reader",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { r := bufio.NewReaderSize(cmd.InOrStdin(), 16); _, _, _ = r.ReadRune() }",
			[]string{"bufio.NewReaderSize", "ReadRune"},
		},
		{
			"a read-writer",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { _ = bufio.NewReadWriter(bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout()) }",
			[]string{"bufio.NewReadWriter", "bufio.NewReader"},
		},
		{
			"a byte read",
			"import \"bufio\"\nfunc f(cmd *cobra.Command) { r := bufio.NewReader(cmd.InOrStdin()); _, _ = r.ReadByte() }",
			[]string{"bufio.NewReader", "ReadByte"},
		},
		{
			"a read filled from stdin",
			"import \"io\"\nfunc f(cmd *cobra.Command) { b := make([]byte, 4); _, _ = io.ReadFull(cmd.InOrStdin(), b) }",
			[]string{"io.ReadFull"},
		},
		{
			"a read filled from stdin, at least",
			"import \"io\"\nfunc f(cmd *cobra.Command) { b := make([]byte, 4); _, _ = io.ReadAtLeast(cmd.InOrStdin(), b, 4) }",
			[]string{"io.ReadAtLeast"},
		},
		{
			"stdin read through a dot-imported io",
			"import . \"io\"\nfunc f(cmd *cobra.Command) { b := make([]byte, 4); _, _ = ReadFull(cmd.InOrStdin(), b) }",
			[]string{"io.ReadFull"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := decisionReadsInSource(t, row.src)
			// The order the shapes are found in is the walk's, not the rule's:
			// the reader is visited after the read it is the receiver of.
			if !slices.Equal(slices.Sorted(slices.Values(forms(got))), slices.Sorted(slices.Values(row.want))) {
				t.Fatalf("the rule found %v in %q, want %v", forms(got), row.src, row.want)
			}
		})
	}
}

// TestLintWalksEveryDeclaration is the other half of "a reader added later
// cannot pass unnoticed": the walk covers the declarations a read can hide in
// and not only the functions, so a read written inside a package-level func
// literal — pair.go:59 keeps its stdin plumbing that way — is a site like any
// other, reported under the name the literal is bound to. The last row is that
// plumbing itself, which hands stdin on and reads nothing: plumbing is not a
// read, and the lint must not start reporting a door as the room behind it.
func TestLintWalksEveryDeclaration(t *testing.T) {
	rows := []struct {
		name string
		src  string
		site string
		want []string
	}{
		{
			"a read in a package-level literal",
			"import \"bufio\"\nvar askLater = func(cmd *cobra.Command) { _, _ = bufio.NewReader(cmd.InOrStdin()).ReadString('\\n') }",
			"row.go:askLater",
			[]string{"bufio.NewReader", "ReadString"},
		},
		{
			"a read in a literal inside a package-level literal",
			"import \"bufio\"\nvar askLater = func(cmd *cobra.Command) { f := func() { _, _ = bufio.NewReader(cmd.InOrStdin()).ReadLine() }; f() }",
			"row.go:askLater",
			[]string{"bufio.NewReader", "ReadLine"},
		},
		{
			"a method",
			"import \"bufio\"\ntype r struct{}\nfunc (r) read(cmd *cobra.Command) { _, _ = bufio.NewReader(cmd.InOrStdin()).ReadString('\\n') }",
			"row.go:read",
			[]string{"bufio.NewReader", "ReadString"},
		},
		{
			"pair.go's own plumbing, which reads nothing",
			"var pairTerminal = func(cmd *cobra.Command) io.Reader { return cmd.InOrStdin() }",
			"",
			nil,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := decisionReadsInSource(t, row.src)
			if row.site == "" {
				if len(got) != 0 {
					t.Fatalf("the rule reported %v in %q, which hands stdin on and does not read it", forms(got), row.src)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("the rule found no read in %q: the declaration it is written in is invisible to the lint", row.src)
			}
			for _, r := range got {
				if site := r.file + ":" + r.fn; site != row.site {
					t.Errorf("the read in %q is reported under %s, want %s", row.src, site, row.site)
				}
			}
			if !slices.Equal(slices.Sorted(slices.Values(forms(got))), slices.Sorted(slices.Values(row.want))) {
				t.Fatalf("the rule found %v in %q, want %v", forms(got), row.src, row.want)
			}
		})
	}
}

// TestLintLeavesPredicatesAndDataReadsAlone is the other half of "not a grep":
// the calls the policy does NOT touch, and the text that looks like them.
// stdinIsTerminal(cmd.InOrStdin()) is a predicate — it reads nothing — and it is
// the guard of almost every site in the package; a store read, a read a data
// path makes with io.ReadAll, and stdin handed to a child process are not a
// person being asked; and a comment or a string that names a read is text, which
// a grep could not tell from a call. The decoder row is different from the rest
// and is named as what it is: a decoder over stdin CAN wait for a person, this
// rule cannot see it, and the row pins the silence so that a later rule that
// does see it says so here.
func TestLintLeavesPredicatesAndDataReadsAlone(t *testing.T) {
	rows := []struct {
		name string
		src  string
	}{
		{"the terminal predicate", "func f(cmd *cobra.Command) { if stdinIsTerminal(cmd.InOrStdin()) { return } }"},
		{"a local function of a matching name", "func NewReader(x any) {}\nfunc f() { NewReader(1) }"},
		{"a data read", "func f(cmd *cobra.Command) { data, _ := io.ReadAll(cmd.InOrStdin()); _ = data }"},
		{"a decoder over stdin, which this rule cannot see", "func f(cmd *cobra.Command) { _ = json.NewDecoder(cmd.InOrStdin()) }"},
		{"stdin handed to a child", "func f(cmd *cobra.Command) { child.SetIn(cmd.InOrStdin()) }"},
		{"a file read", "func f(path string) { _, _ = os.ReadFile(path) }"},
		{"a store read", "import \"context\"\nfunc f(ctx context.Context, b *bucket) { _, _, _ = b.Read(ctx, \"k\") }"},
		{"a package data read", "import \"github.com/dimipaun/fugaro/internal/streams\"\nfunc f(p string) { _, _ = streams.Read(p) }"},
		{"stdin put in a variable of its own", "func f(cmd *cobra.Command) { in := cmd.InOrStdin(); _ = in }"},
		{"a full read from a package's own reader", "import (\n\"crypto/rand\"\n\"io\"\n)\nfunc f(b []byte) { _, _ = io.ReadFull(rand.Reader, b) }"},
		{"a read-writer written to", "import \"bufio\"\nfunc f(w io.Writer) { _ = bufio.NewWriter(w) }"},
		{"a call in a string", "func f() { _ = \"bufio.NewReader(cmd.InOrStdin()).ReadString('\\\\n')\" }"},
		{"a call in a comment", "func f() { // _ = bufio.NewReader(cmd.InOrStdin())\n}"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := decisionReadsInSource(t, row.src); len(got) != 0 {
				t.Errorf("the rule reported %v in %q, which is not a decision read", forms(got), row.src)
			}
		})
	}
}

// TestNoDecisionReadOutsideTheTwoHelpers is the lint over the package: every
// read of one of the shapes is a sanctioned helper, a site the policy still
// owes a removal for, or a read a person wrote down as not a decision — and the
// four lists are exact in both directions, so no site is unlisted, no entry is
// stale, and no exemption survives the read under it.
func TestNoDecisionReadOutsideTheTwoHelpers(t *testing.T) {
	found := map[string][]decisionRead{}
	for _, r := range decisionReadsInPackage(t) {
		key := r.file + ":" + r.fn
		found[key] = append(found[key], r)
	}
	// The scan is only as good as what it read: the package has prompts in it
	// today, so finding none means the walk is broken, not that the policy is
	// already paid off.
	if len(found) == 0 {
		t.Fatal("the lint found no decision read in internal/cli, which still asks: it is reading nothing")
	}
	keys := slices.Sorted(maps.Keys(found))
	for _, key := range keys {
		if _, ok := sanctionedSites[key]; ok {
			continue
		}
		if _, ok := notADecisionSites[key]; ok {
			continue
		}
		if _, ok := pendingSites[key]; !ok {
			t.Errorf("%s reads a stream in a shape a person is asked with (%s) and no list accounts for it. If it asks a person, the policy removes it (docs/no-prompt-policy/T1): list it in pendingSites with the task that takes it out. If what it reads is not a person (a response body, a file, a buffer), a shape cannot say so and a person must: list it in notADecisionSites with the reason it is not one. A read that fails on EOF rather than waiting is neither and is already named in dataReadSites.",
				key, forms(found[key]))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(pendingSites)) {
		if _, ok := found[key]; !ok {
			t.Errorf("the list still names %s as a decision read (%s owns the entry) and nothing reads stdin there any more: delete its entry in the same change that removed the read",
				key, pendingSites[key])
		}
	}
	for _, key := range slices.Sorted(maps.Keys(sanctionedSites)) {
		if _, ok := found[key]; !ok {
			t.Errorf("the lint sanctions %s (%s) and the read is not there: the exemption is stale, and an exemption nothing needs is a hole",
				key, sanctionedSites[key])
		}
	}
	for _, key := range slices.Sorted(maps.Keys(dataReadSites)) {
		if _, ok := found[key]; ok {
			t.Errorf("%s is listed as a data read (%s) and now reads stdin like a decision (%s); a data read fails on EOF and waits for nobody, and one that asks is the prompt the policy removes",
				key, dataReadSites[key], forms(found[key]))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(notADecisionSites)) {
		if _, ok := found[key]; !ok {
			t.Errorf("the lint records %s as a read that is not a decision (%s) and no read of a shape is there any more: delete the entry in the same change that removed the read",
				key, notADecisionSites[key])
		}
	}
}

// sanctionedSites is what the policy keeps, and why: the owner's one
// sanctioned confirmation and the value-entry helper with the hidden read it
// drives. Each is asserted to be found, so the lint says out loud that it is
// allowing a read rather than having lost one.
var sanctionedSites = map[string]string{
	"managed_ask.go:askConfirm":    "the --ask helper, the owner's single sanctioned exemption (D1544, T1)",
	"valueentry.go:readValue":      "the value-entry helper: a value is not a confirmation and cannot be defaulted (D1540)",
	"restore.go:readConfirmLine":   "the line read both sanctioned helpers read through: a value's line is read here, and T6 deletes restore's confirmations, not this",
	"secrets.go:readHiddenLine":    "the hidden read of the value entry's hidden path, readValue to readHidden to here (D1540; T7 changes its door, not its hiding)",
	"initsecrets.go:readHiddenPEM": "the multi-line shape of the same value entry: a PEM cannot be typed on one line, and the one-line helper cannot take it (D1540 keeps value entry; T12 owns this file)",
}

// dataReadSites is neither a confirmation nor a value: a read that fails on
// EOF rather than waiting for a person, and that the lint must never report.
// Each is asserted NOT to be found, so a data read that grows a decision read
// is a failure rather than an exemption nobody reads.
var dataReadSites = map[string]string{
	"run.go:taskText":          "fugaro run's --task-file - reads the task itself",
	"run.go:reviewLevelsFlag":  "fugaro run's --review-levels - reads the levels themselves",
	"exec.go:readTaskFile":     "fugaro exec's --task-file - reads the task itself",
	"creds.go:readBundleValue": "fugaro creds set's data form reads the value a pipe delivers",
}

// notADecisionSites is a read of one of the shapes above that is not a read of
// a person, and the reason it is not: a response body, a file, a buffer. The
// shape cannot say which stream it is — a reader over a body and a reader over
// stdin are built and read identically — so the answer is written here by a
// person, and the entry is asserted to be FOUND, like a sanctioned site, so it
// cannot outlive the read it explains. It is empty today: every shape the
// package reads is either sanctioned or a confirmation the policy still owes a
// removal for. A prompt does not belong here: it belongs nowhere.
var notADecisionSites = map[string]string{}

// pendingSites is every decision read the package still makes and the task of
// docs/no-prompt-policy/implementation-plan.md that removes it. It is the
// allow-list of the transition and it is meant to reach empty: nothing here is
// a decision the policy wants kept, and the task named beside a site deletes
// its entry in the same commit that deletes the read. The files the plan names
// as T15's (`update_workspace.go`, the `Typed` init stages of
// `init_credentials.go` and `init_compute.go`) reach their reads through
// `confirmTypedStrict` and `askTyped`, so their entries are the helpers' own
// sites in `budget.go` and `init.go` — the read is what the lint holds.
var pendingSites = map[string]string{
	"accounts.go:accountsClear":        "T5",
	"billing_prompt.go:billingAPIOff":  "T7",
	"budget.go:confirmTyped":           "T3",
	"budget.go:confirmTypedStrict":     "T3",
	"init.go:Read":                     "T7",
	"init.go:confirmResumedMark":       "T7",
	"init.go:newInitRun":               "T7",
	"init.go:typed":                    "T7",
	"init_firebase.go:confirmLocation": "T7",
	"init_fugaroyaml.go:writeAnchor":   "T7",
	"init_inputs.go:promptChoice":      "T7",
	"init_plugin_stage.go:Apply":       "T7",
	"init_project.go:typed":            "T7",
	"init_repo_gate.go:confirmRepo":    "T7",
	"managed_apply.go:managedAsk":      "T9",
	"pair.go:pairReadCode":             "T7",
	// T6 removed every read of this command family it could remove. This one
	// stays because its callers do not: confirmRemoval is defined in a file T6
	// owns, but upgrade.go (T5), init_layer_stage.go and init_policy_migrate.go
	// (T7) and update_workspace.go (T15) still call it, so the entry outlives T6
	// and belongs to the task that removes the last caller. Deleting it here
	// would not compile.
	"project_cmd.go:confirmRemoval":          "T6's file, whose callers T5, T7 and T15 remove",
	"settings_write.go:confirmSettingsWrite": "T5",
}

// forms is the shapes found at one site, in a stable order, for a failure's
// evidence.
func forms(reads []decisionRead) []string {
	out := make([]string, 0, len(reads))
	for _, r := range reads {
		out = append(out, r.form)
	}
	slices.Sort(out)
	return out
}
