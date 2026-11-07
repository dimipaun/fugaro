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
// that every reader was found by hand once, but that a reader added later
// cannot pass unnoticed. So the rule is deliberately about the SHAPE of a read
// rather than about a list of callers: a read of stdin for a decision is one of
//
//   - bufio.NewReader / bufio.NewScanner — the line reader this package asks a
//     person with. It is matched by the package the call comes from and not by
//     its text, so `import b "bufio"` and `import . "bufio"` are the same read
//     and a function of the same name in this package is not one.
//   - .ReadString / .ReadBytes / .ReadLine on such a reader: the reading half
//     of the same reader, and what no data read in the package uses.
//   - readConfirmLine — the confirmation line itself.
//   - term.ReadPassword — the hidden read, which is the value entry's engine.
//
// and it reports where the reading happens, not who called the reader. The
// alias the plan calls out (`in := cmd.InOrStdin(); bufio.NewReader(in)`) is a
// row of TestLintCatchesAnAliasedStdinReader: the shape is matched whatever
// expression the reader was built from, so a rule keyed on the literal
// cmd.InOrStdin() could not see a reader that arrived through a variable, and
// the wizard's own reader field is the same read one function away. A comment
// or a string that mentions a read is not a read, which is what keeps this an
// AST rule and not a grep.
//
// Three lists hold the answer, and none of them is a promise:
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
//     the owner's one sanctioned exemption) and the value-entry helper with the
//     hidden read underneath it (D1540: a value is not a confirmation and
//     cannot be defaulted, so it stays a terminal read that fails at once
//     everywhere else). Each is asserted to be found, so the lint says out loud
//     that it allows a read rather than having lost one. The hidden read is
//     listed under `secrets.go:readHiddenLine` even though the helper is
//     readValue: it is one call down — readValue's hidden path is readHidden,
//     which is readHiddenLine — and T7 changes its door without moving its
//     hiding, so exempting the helper without its engine would leave the
//     helper refusing at every terminal.
//   - dataReadSites: the reads F1 names as neither confirmations nor values
//     (`--task-file -`, `--review-levels -`, the exec task file, the
//     credentials bundle). These are asserted NOT to be found, so a data read
//     that grows a decision read fails here instead of passing as an exemption
//     nobody re-reads.

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
// the function declaration it sits in — a closure belongs to the function that
// writes it, so a read inside the `go func(){…}()` pairReadCode reads its code
// in is the site of pairReadCode.
func scanDecisionReads(file *ast.File, name string) []decisionRead {
	imports := localPackageNames(file)
	var out []decisionRead
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if form := decisionReadForm(call, imports); form != "" {
				out = append(out, decisionRead{file: name, fn: fd.Name.Name, form: form})
			}
			return true
		})
	}
	return out
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
		// A dot-import (`import . "bufio"`) puts the constructors in this
		// file's own namespace, where a bare NewReader is the same read; any
		// other function of that name is not, which is why the package is
		// matched through the import rather than by the name alone.
		switch imports["."] {
		case "bufio":
			if fun.Name == "NewReader" || fun.Name == "NewScanner" {
				return "bufio." + fun.Name
			}
		case "golang.org/x/term":
			if fun.Name == "ReadPassword" {
				return "term.ReadPassword"
			}
		}
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok {
			switch imports[pkg.Name] {
			case "bufio":
				if fun.Sel.Name == "NewReader" || fun.Sel.Name == "NewScanner" {
					return "bufio." + fun.Sel.Name
				}
			case "golang.org/x/term":
				if fun.Sel.Name == "ReadPassword" {
					return "term.ReadPassword"
				}
			}
		}
		// A reader this package built is read a line at a time here or not at
		// all: the three line reads are the reading half of the bufio reader
		// above, and no data read in the package uses one.
		switch fun.Sel.Name {
		case "ReadString", "ReadBytes", "ReadLine":
			return fun.Sel.Name
		}
	}
	return ""
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

// TestLintLeavesPredicatesAndDataReadsAlone is the other half of "not a grep":
// the shapes the policy does NOT touch. stdinIsTerminal(cmd.InOrStdin()) is a
// predicate — it reads nothing — and it is the guard of almost every site in
// the package; a data read (io.ReadAll, a decoder, a child process handed the
// terminal) fails on EOF and waits for nobody; and a comment or a string that
// names a read is text, which a grep could not tell from a call.
func TestLintLeavesPredicatesAndDataReadsAlone(t *testing.T) {
	rows := []struct {
		name string
		src  string
	}{
		{"the terminal predicate", "func f(cmd *cobra.Command) { if stdinIsTerminal(cmd.InOrStdin()) { return } }"},
		{"a local function of a matching name", "func NewReader(x any) {}\nfunc f() { NewReader(1) }"},
		{"a data read", "func f(cmd *cobra.Command) { data, _ := io.ReadAll(cmd.InOrStdin()); _ = data }"},
		{"a decoder over stdin", "func f(cmd *cobra.Command) { _ = json.NewDecoder(cmd.InOrStdin()) }"},
		{"stdin handed to a child", "func f(cmd *cobra.Command) { child.SetIn(cmd.InOrStdin()) }"},
		{"a file read", "func f(path string) { _, _ = os.ReadFile(path) }"},
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
// decision read is one of the two sanctioned helpers or one of the sites the
// policy still owes a removal for, and the three lists are exact — no site
// unlisted, no entry stale, no exemption that no longer has a read under it.
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
		if _, ok := pendingSites[key]; !ok {
			t.Errorf("%s reads stdin for a decision (%s) and is neither a sanctioned helper nor a listed site; a new prompt is not allowed (docs/no-prompt-policy), and a data read belongs in dataReadSites with the reason it is one",
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
}

// sanctionedSites is what the policy keeps, and why: the owner's one
// sanctioned confirmation and the value-entry helper with the hidden read it
// drives. Each is asserted to be found, so the lint says out loud that it is
// allowing a read rather than having lost one.
var sanctionedSites = map[string]string{
	"managed_ask.go:askConfirm": "the --ask helper, the owner's single sanctioned exemption (D1544, T1)",
	"valueentry.go:readValue":   "the value-entry helper: a value is not a confirmation and cannot be defaulted (D1540)",
	"secrets.go:readHiddenLine": "the hidden read readValue's value entry is the engine of (D1540; T7 changes its door, not its hiding)",
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
	"accounts.go:accountsClear":              "T5",
	"billing_prompt.go:billingAPIOff":        "T7",
	"budget.go:confirmTyped":                 "T3",
	"budget.go:confirmTypedStrict":           "T3",
	"budget.go:runBudgetKill":                "T3",
	"budget.go:runBudgetSet":                 "T3",
	"image.go:confirmBuild":                  "T4",
	"init.go:confirmResumedMark":             "T7",
	"init.go:newInitRun":                     "T7",
	"init.go:typed":                          "T7",
	"init_firebase.go:confirmLocation":       "T7",
	"init_fugaroyaml.go:writeAnchor":         "T7",
	"init_inputs.go:promptChoice":            "T7",
	"init_plugin_stage.go:Apply":             "T7",
	"init_project.go:typed":                  "T7",
	"init_repo_gate.go:confirmRepo":          "T7",
	"managed_apply.go:managedAsk":            "T9",
	"managed_flag.go:confirmTypedGCPProject": "T6",
	"pair.go:pairReadCode":                   "T7",
	"project_cmd.go:confirmRemoval":          "T6",
	"project_enroll.go:runProjectSync":       "T6",
	"restore.go:anchor":                      "T6",
	"restore.go:confirm":                     "T6",
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
