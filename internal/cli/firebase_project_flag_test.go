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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
