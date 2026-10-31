package cli

// `--firebase-project` is a string flag with no default, so a remedy that stops
// at the flag prints a command that fails on a missing argument, and the person
// reading it is sent to fix something with a command that refuses before it does
// anything. The same sentence with the flag and no project at all reads as a
// command and is not one.
//
// The messages these files print are built several ways — one literal, a
// concatenation whose project word is computed, a `fmt.Sprintf` that passes the
// project as an argument, and one helper (`promptLeft`) that joins the flag with
// its value as separate arguments — so the check is on the text a reader would
// see, not on the source. Every string expression in these files is flattened
// into the shape it prints, with each part that is not a literal standing in as
// one marked placeholder word, and every `--firebase-project` in that shape has
// to be followed by a project: a format verb, a `<gcp-project-id>` placeholder,
// or the mark left by a computed operand. A literal English word after the flag
// is the failure this test exists for, because that is what a sentence with the
// value missing looks like.
//
// The files are listed by name because the check is about a class of message
// rather than about a package: these are the files that print a remedy with a
// project in it, and the class comes back one message at a time otherwise.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// firebaseProjectFlag is the flag whose value the person reading has to type.
const firebaseProjectFlag = "--firebase-project"

// firebaseProjectComputed stands in for a string expression the scan cannot read
// — a computed project word, a format argument, an expression on the other side
// of a `+`. It is marked rather than spelled out so that a shape says whether
// its project word was written in the message or handed to it: only the second
// can be a real project, and only the first can be prose the check must catch.
const firebaseProjectComputed = "\x00project\x00"

// firebaseProjectFlagSources are the files whose printed remedies name the flag.
var firebaseProjectFlagSources = []string{
	"history_rollover.go",
	"doctor.go",
	"init_firebase.go",
	"upgrade_cloud.go",
	filepath.Join("..", "projectpolicy", "read.go"),
	filepath.Join("..", "budget", "session.go"),
	// The history job's own two lines, which print beside the job's warning.
	filepath.Join("..", "budget", "rollover.go"),
	// The rest of the remedies the same round fixed, none of which the list
	// above covered: the rules stage's two skips (its own half of the ownership
	// predicate), budget's own database check, the restore's leftovers and the
	// accounts note. A message added to any of them is the same failure.
	"init_rules_stage.go",
	"budget.go",
	"restore.go",
	"accounts.go",
}

// firebaseProjectCommandIn reports whether a shape prints a command at the
// flag — "fugaro update --firebase-project ..." — and no project after it. The
// failure is a remedy a person pastes and the CLI refuses on the missing
// argument, and it is the `update` that makes it a remedy: these files also
// write sentences ABOUT the flag ("--firebase-project takes only the
// installation's own GCP project"), where the next word is prose because the
// sentence continues, not because a value is missing, and a reader cannot paste
// one of those.
func firebaseProjectCommandIn(shape string) bool {
	at := 0
	for {
		j := strings.Index(shape[at:], firebaseProjectFlag)
		if j < 0 {
			return false
		}
		i := at + j
		if strings.HasSuffix(strings.TrimRight(shape[:i], "\t\n"), "update ") &&
			!firebaseProjectWordIn(shape[i+len(firebaseProjectFlag):]) {
			return true
		}
		at = i + len(firebaseProjectFlag)
	}
}

// firebaseProjectWordIn reports whether what a shape prints after the flag is a
// project. Anything else — nothing at all, the next flag, or the sentence
// carrying on in words — is a command that cannot run, and it sends the reader
// to the wrong place.
func firebaseProjectWordIn(rest string) bool {
	word := strings.TrimLeft(rest, " \t\n")
	if k := strings.IndexAny(word, " \t\n"); k >= 0 {
		word = word[:k]
	}
	word = strings.Trim(word, ".,;:!?`'\"()[]")
	if word == "" || strings.HasPrefix(word, "-") {
		return false
	}
	return strings.Contains(word, "\x00") ||
		strings.HasPrefix(word, "%") ||
		strings.HasPrefix(word, "<") && strings.HasSuffix(word, ">")
}

// firebaseProjectShapes flattens every string expression in src into the text it
// would print. Comments are not in the syntax tree, so a remedy described in a
// comment is not a printed one and is not checked.
func firebaseProjectShapes(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	// A literal that IS the flag, alone, is a word handed to a helper that
	// joins it with its value: promptLeft takes them as separate arguments, so
	// the value is not the next character of any one string. Those literal
	// words are allowed as call arguments and nowhere else, where a message
	// that names the flag and stops is caught.
	inCall := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			for _, a := range call.Args {
				inCall[a] = true
			}
		}
		return true
	})
	var flatten func(e ast.Expr) string
	flatten = func(e ast.Expr) string {
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return s
			}
		}
		if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
			return flatten(bin.X) + flatten(bin.Y)
		}
		return firebaseProjectComputed
	}
	var shapes []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				shapes = append(shapes, flatten(x))
				// Its operands are part of the shape already.
				return false
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				return true
			}
			if s == firebaseProjectFlag && inCall[x] {
				return true
			}
			shapes = append(shapes, s)
		}
		return true
	})
	return shapes
}

// TestEveryPrintedFirebaseProjectFlagCarriesAProject pins the printers of
// `--firebase-project` against the one failure every one of them can have: a
// printed command that stops at the flag. Each of these files was fixed one
// message at a time (a doctor fix that printed the project word as a bare
// concatenation, a sentence that named the flag and no project), and a fix that
// is one message at a time comes back the next time a message is added — so the
// check is the file, not the message.
func TestEveryPrintedFirebaseProjectFlagCarriesAProject(t *testing.T) {
	for _, name := range firebaseProjectFlagSources {
		for _, shape := range firebaseProjectShapes(t, name) {
			if firebaseProjectCommandIn(shape) {
				t.Errorf("%s prints a `%s` command with no project after it: the flag takes a value, so the command it prints refuses on the missing argument: %q", name, firebaseProjectFlag, shape)
			}
		}
	}
}

// TestTheFirebaseProjectFlagScanSeesTheMessagesItIsFor is the scan's own
// control: a scan that rejected every shape would pass the test above while
// saying nothing, and one that accepted every shape would pass it while hiding
// the failure it exists for. Both sides are exercised on messages shaped the way
// these files build them.
func TestTheFirebaseProjectFlagScanSeesTheMessagesItIsFor(t *testing.T) {
	dir := t.TempDir()
	write := func(source string) string {
		t.Helper()
		p := filepath.Join(dir, "sample.go")
		if err := os.WriteFile(p, []byte("package sample\n\nfunc f() string { return "+source+" }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flagged := func(source string) bool {
		t.Helper()
		for _, shape := range firebaseProjectShapes(t, write(source)) {
			if firebaseProjectCommandIn(shape) {
				return true
			}
		}
		return false
	}
	for _, source := range []string{
		`"run fugaro update --firebase-project"`,
		`"run fugaro update --firebase-project --repo ."`,
		`"run fugaro update --firebase-project again"`,
		`"run fugaro update --firebase-project " + ""`,
		`"run fugaro update --firebase-project proj-1234"`,
	} {
		if !flagged(source) {
			t.Errorf("the scan accepts %s, whose printed command stops at the flag", source)
		}
	}
	// The last of the bad ones is a project id written into the message itself:
	// the scan cannot tell one installation's project from the sentence
	// carrying on, and a remedy has no business carrying either.
	for _, source := range []string{
		`"run fugaro update --firebase-project <gcp-project-id>"`,
		`"run fugaro update --firebase-project %s"`,
		`"run fugaro update --firebase-project " + project`,
		`"run fugaro update --firebase-project " + project + " again"`,
		`join("--firebase-project", project)`,
		// A sentence about the flag, where the word after it is the verb.
		`"the flag --firebase-project takes only the installation's own GCP project"`,
	} {
		if flagged(source) {
			t.Errorf("the scan rejects %s, whose printed command carries a project", source)
		}
	}
}

// firebaseProjectRemedyStopsAtTheFlag is firebaseProjectCommandIn for text
// rather than for a shape: whether the rendered sentence prints an update
// command whose --firebase-project carries no value. A shape marks a computed
// operand and is accepted for it, which is the blind spot the test below
// exists for: a remedy built from a value that can be empty (`r.o.
// firebaseProject()` on a run that was given no --firebase-project) is a shape
// the scan reads as a project handed to the sentence.
//
// What a value is, in text, is one shell word after exactly one space. A value
// that came out empty leaves the space behind and nothing else, so the next
// character is a second space, the backtick that closes a printed command, or
// the end of the text; what is allowed to start the word is what a project id
// or the placeholder for one starts with — the flag or a flag after it is a
// command that refuses on the argument just as an empty one is.
func firebaseProjectRemedyStopsAtTheFlag(text string) bool {
	const at = "update " + firebaseProjectFlag
	i := 0
	for {
		j := strings.Index(text[i:], at)
		if j < 0 {
			return false
		}
		rest := text[i+j+len(at):]
		if len(rest) < 2 || rest[0] != ' ' || !firebaseProjectWordStart(rune(rest[1])) {
			return true
		}
		i += j + len(at)
	}
}

func firebaseProjectWordStart(r rune) bool {
	return r == '<' || r == '\'' || r == '"' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// TestTheRemediesPrintedFromAnEmptyConfigCarryAProject is the half of the check
// above that a scan cannot do, and it is the half the round's finding came
// from: the scan reads a shape, where a computed word is a word, and
// `r.o.firebaseProject()` is "" on a run that was given no --firebase-project —
// so the shape carried a project and the sentence a reader saw stopped at the
// flag. Every remedy that can be printed from a config that names no project is
// printed here from one, and what it prints has to carry a value.
//
// The empty config is the point rather than a convenience: it is the shape in
// which a value read off the command line or the config is empty, and the
// remedy is what the person sees. The two warnings in init_firebase.go are the
// last of the round's fixes and are not in the table: they are printed only
// inside the run that IS given --firebase-project, so the value they now read
// (firebaseProjectIDWord of the installation's own GCP project) is the one the
// case below covers, and the sentence around it cannot print another.
func TestTheRemediesPrintedFromAnEmptyConfigCarryAProject(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewRTDB(t)
	client, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	empty := &localcfg.Config{}
	noURL := &localcfg.Config{Budget: &localcfg.Budget{FirebaseProject: "aurora-fp"}}
	separate := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{FirebaseProject: "aurora-fp", RTDBURL: fake.URL}}
	mismatch := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{FirebaseProject: "aurora-fp", RTDBURL: "https://other-default-rtdb.firebaseio.com"}}
	_, left, err := restoreConfig(&localcfg.Config{}, &localcfg.Config{Budget: &localcfg.Budget{FirebaseProject: "aurora-fp", RTDBURL: fake.URL}}, "proj-9999")
	if err != nil {
		t.Fatal(err)
	}
	_, note, err := accountsStates(ctx, empty, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	unchecked := checkDBProject(ctx, client, empty)
	if unchecked == nil {
		t.Fatal("a database with no /fugaro/project was accepted as the project's")
	}
	cases := []struct{ name, text string }{
		{"rulesNoBudget, no budget block", rulesNoBudget(empty)},
		{"rulesNoBudget, a Firebase project and no URL", rulesNoBudget(noURL)},
		{"rulesForeignDatabase", rulesForeignDatabase(mismatch)},
		{"budgetDBUnadopted, a separate project and an empty database", budgetDBUnadopted(ctx, separate, infra.NewDB(client, "aurora", "", ""), "this stage neither marks nor writes there", "fugaro update")},
		{"checkDBProject, a database with no /fugaro/project", unchecked.Error()},
		{"accountsStates, no budget backend", note},
		{"restoreConfig, a budget backend that did not come across", strings.Join(left, "\n")},
		{"firebaseProjectIDWord with no project to read", "fugaro update --firebase-project " + firebaseProjectIDWord("")},
	}
	for _, c := range cases {
		if firebaseProjectRemedyStopsAtTheFlag(c.text) {
			t.Errorf("%s prints a `%s` command with no project after it: %q", c.name, firebaseProjectFlag, c.text)
		}
	}
	// The assertion's own control: text shaped the way the failure is shaped,
	// and text shaped the way a passed remedy is, so a check that accepted (or
	// rejected) everything is not read as "the remedies are fine".
	for _, bad := range []string{
		"then run fugaro update --firebase-project  again",
		"then run fugaro update --firebase-project --yes",
		"then run fugaro update --firebase-project",
	} {
		if !firebaseProjectRemedyStopsAtTheFlag(bad) {
			t.Errorf("the check accepts %q, whose printed command stops at the flag", bad)
		}
	}
	for _, good := range []string{
		"then run fugaro update --firebase-project proj-1234 again",
		"then run fugaro update --firebase-project <gcp-project-id>",
		"the flag --firebase-project takes only the installation's own GCP project",
	} {
		if firebaseProjectRemedyStopsAtTheFlag(good) {
			t.Errorf("the check rejects %q, whose printed command carries a project", good)
		}
	}
}
