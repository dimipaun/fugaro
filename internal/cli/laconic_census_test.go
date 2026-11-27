package cli

// laconic_census_test.go is CO5 (D3439): CLAUDE.md's 140-character bound over
// every human message this package carries, rather than over update's warnings
// alone. update_laconic_test.go held about thirty of them by name; this census
// is folded in from it, and walks the tree instead, so a message added later is
// measured the day it is written.
//
// A message is a string this package binds to a name and says to a person: a
// refusal, a warning, a notice, a step reason, a caveat. The census reads the
// package's own syntax tree for the single-line string values it declares --
// package-level or inside a function, as a const, a var or a `:=` -- and folds
// the literals a value is built from, so a message written as `"a" + "b"` is
// measured whole.
//
// A value with a newline in it is not one of these: it is a help screen, a
// template or a block of documentation, printed whole rather than said as a
// line, and the bound is on the line. That is the whole of the exclusion --
// there is no list of names a message may not have -- and it is what keeps
// cobra's Long help, the version template and stats' disclaimer out of the
// census while every one-line message is in it.
//
// Messages over the bound today are recorded in testdata/laconic_allowlist.txt.
// The list only shrinks: a message that is shortened leaves its line stale, and
// TestLaconicAllowlistOnlyShrinks fails until the line is deleted, while
// laconicAllowlistMax there is the ceiling the file may never grow past.

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// maxMessageChars is CLAUDE.md's bound on what a command tells a person: one
// line, what happened and what to do, no decision number and no internal name.
const maxMessageChars = 140

// laconicAllowlistMax is how many messages testdata/laconic_allowlist.txt
// carried when the census was written. It moves down with the list and never
// up: a message that would have to be added is a message to shorten instead.
const laconicAllowlistMax = 83

// laconicAllowlistPath is the recorded set.
var laconicAllowlistPath = func() string {
	path, err := filepath.Abs("testdata/laconic_allowlist.txt")
	if err != nil {
		panic(err)
	}
	return path
}()

// updateLaconicAllowlist rewrites that file from the census. A second flag.Bool
// of the same name panics, so the name is this test's alone.
var updateLaconicAllowlist = flag.Bool("update-laconic-allowlist", false, "rewrite testdata/laconic_allowlist.txt from the messages the census finds over the bound")

// TestMessagesAreLaconic walks the package's messages and holds each to the
// bound: one line saying what happened and what to do, no longer than
// maxMessageChars. A message over it fails unless its own text is recorded in
// the allowlist, so the fix is to shorten the message and not to reach for the
// file.
func TestMessagesAreLaconic(t *testing.T) {
	msgs := censusMessages(t)
	if len(msgs) < 300 {
		t.Fatalf("the census found %d messages, and this package carries far more: a census that walks nothing passes everything", len(msgs))
	}
	if *updateLaconicAllowlist {
		laconicAllowlistWrite(t, msgs)
		return
	}
	allow := readLaconicAllowlist(t)
	for _, m := range msgs {
		n := len([]rune(m.text))
		if n <= maxMessageChars || allow[m.text] {
			continue
		}
		t.Errorf("%s:%d %s is %d characters, over the %d a message gets: shorten it, or record it in %s with -update-laconic-allowlist: %q",
			m.file, m.line, m.name, n, maxMessageChars, laconicAllowlistPath, m.text)
	}
}

// TestLaconicAllowlistOnlyShrinks is the other half of the ratchet. Every line
// of the allowlist has to be a message the tree still says and that is still
// over the bound -- a line the tree has moved past is a line nobody re-read, so
// the file only ever loses lines -- and the file may never carry more than
// laconicAllowlistMax of them.
func TestLaconicAllowlistOnlyShrinks(t *testing.T) {
	if *updateLaconicAllowlist {
		t.Skip("the allowlist is being regenerated")
	}
	allow := readLaconicAllowlist(t)
	if len(allow) > laconicAllowlistMax {
		t.Errorf("%s carries %d messages and laconicAllowlistMax is %d: the list only shrinks, so shorten the message that was added rather than allowlisting it",
			laconicAllowlistPath, len(allow), laconicAllowlistMax)
	}
	live := map[string]bool{}
	for _, m := range censusMessages(t) {
		if len([]rune(m.text)) > maxMessageChars {
			live[m.text] = true
		}
	}
	for _, text := range slices.Sorted(maps.Keys(allow)) {
		if !live[text] {
			t.Errorf("%s records a message over %d that the tree no longer says: delete the line -- the list only shrinks, and a line the tree has moved past is a line nobody re-read: %q",
				laconicAllowlistPath, maxMessageChars, text)
		}
	}
}

// laconicMessage is one message and where it is written.
type laconicMessage struct {
	file string
	line int
	name string
	text string
}

// censusMessages is every message this package carries: the single-line string
// values it binds to a name, wherever it binds them, with the literals a value
// is built from folded.
func censusMessages(t *testing.T) []laconicMessage {
	t.Helper()
	src := parseCensusSource(t)
	var out []laconicMessage
	add := func(file string, pos token.Pos, name string, value ast.Expr) {
		text, ok := foldMessageString(value)
		if !ok || text == "" || strings.ContainsAny(text, "\r\n") {
			return
		}
		out = append(out, laconicMessage{file: file, line: src.fset.Position(pos).Line, name: name, text: text})
	}
	for _, file := range slices.Sorted(maps.Keys(src.files)) {
		ast.Inspect(src.files[file], func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ValueSpec:
				for i, name := range node.Names {
					if i < len(node.Values) {
						add(file, node.Pos(), name.Name, node.Values[i])
					}
				}
			case *ast.AssignStmt:
				if node.Tok != token.DEFINE {
					return true
				}
				for i, lhs := range node.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && i < len(node.Rhs) {
						add(file, node.Pos(), id.Name, node.Rhs[i])
					}
				}
			}
			return true
		})
	}
	slices.SortFunc(out, func(a, b laconicMessage) int {
		if a.file != b.file {
			return strings.Compare(a.file, b.file)
		}
		return a.line - b.line
	})
	return out
}

// foldMessageString is the text a value holds, when the value is a string
// literal or a sum of them: what a message written as `"a" + "b"` says whole.
func foldMessageString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return foldMessageString(v.X)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok := foldMessageString(v.X)
		if !ok {
			return "", false
		}
		right, ok := foldMessageString(v.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// readLaconicAllowlist is the recorded set, one message text per line. A
// missing file is the empty set rather than an error: a tree whose every
// message fits the bound carries no allowlist at all, which is the state the
// list is shrinking towards.
func readLaconicAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(laconicAllowlistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("reading %s: %v", laconicAllowlistPath, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// laconicAllowlistHeader is the file's own comment block. It is written by the
// regeneration rather than kept beside it, so the file a run rewrites is the
// file a reader reads.
const laconicAllowlistHeader = `# Every message internal/cli carries that is longer than the 140 characters
# CLAUDE.md gives one line, one per line: the message's own text, whole.
#
# A message is a single-line string this package binds to a name -- a refusal, a
# warning, a notice, a step reason, a caveat. A value with a newline in it is a
# help screen, a template or a block of documentation rather than a line, and is
# not one. TestMessagesAreLaconic fails on a message over the bound that is not
# here.
#
# The list only shrinks. A message that is shortened leaves its line stale, and
# TestLaconicAllowlistOnlyShrinks fails until the line is deleted; a message
# that would have to be added is a message to shorten instead, because
# laconicAllowlistMax in laconic_census_test.go is the ceiling this file may
# never grow past.
#
# Generated by
#   go test ./internal/cli -run TestMessagesAreLaconic -update-laconic-allowlist
`

// laconicAllowlistWrite rewrites the allowlist from the census: every message
// over the bound, one text per line, in the order the census walks them.
func laconicAllowlistWrite(t *testing.T, msgs []laconicMessage) {
	t.Helper()
	var b strings.Builder
	b.WriteString(laconicAllowlistHeader)
	seen := map[string]bool{}
	n := 0
	for _, m := range msgs {
		if len([]rune(m.text)) <= maxMessageChars || seen[m.text] {
			continue
		}
		seen[m.text] = true
		fmt.Fprintln(&b, m.text)
		n++
	}
	if err := os.WriteFile(laconicAllowlistPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", laconicAllowlistPath, err)
	}
	t.Logf("%s: %d messages over %d", laconicAllowlistPath, n, maxMessageChars)
}
