package cli

// laconic_census_test.go is CO5 (D3439): CLAUDE.md's 140-character bound over
// every human message this package carries, rather than over update's warnings
// alone. update_laconic_test.go held about thirty of them by name; this census
// is folded in from it, and reads the tree instead, so a message added later is
// measured the day it is written.
//
// A message is a string a command says to a person: a refusal, a warning, a
// notice, a step reason, a caveat. The census reads the package's own syntax
// tree for them wherever they are said:
//
//   - the string values it binds to a name, wherever it binds them -- package
//     level or inside a function, as a const, a var or a `:=`;
//   - the first string argument of the calls that say a refusal -- userErr and
//     fmt.Errorf -- where the sentence is written at the call site rather than
//     bound to a name. Most of this package's refusals are written that way,
//     and a refusal at a call site is as much a message as one bound to a name;
//   - the second argument of fmt's print family, which is the sentence a person
//     reads and whose first argument is only the writer it goes to;
//   - the first argument of the calls that warn -- this package's own warn and
//     warnf, whatever receiver they hang off, log.Warn among them.
//
// The literals a value or an argument is built from are folded, so a message
// written as `"a" + "b"` is measured whole, and one written across several
// lines is measured whole too.
//
// Two tables beside the census hold the messages no walk of the source can see,
// and each of their texts is held to the bound and to one line, which is what
// the update-only test held: the sentences internal/initflow assembles at run
// time from a marker or a flag, and the ones this package builds from a format
// and its arguments, read from the same format with one sample set of them.
//
// A value bound to a help field -- cobra's Use, Short, Long and Example, or the
// door structs' own lower-case spelling of them -- is a help screen rather than
// a message: `--help` prints it whole, and nobody is told it as a line. That is
// the whole of the exclusion, and it is read from the tree rather than listed
// here, so a help screen added later is excluded the day it is written. It is
// what keeps cobra's long help out of the census.
//
// Messages over the bound today are recorded in testdata/laconic_allowlist.txt,
// one Go-quoted text per line so that a message with a newline in it is one
// line of the file. The list only shrinks: a message that is shortened leaves
// its line stale, and TestLaconicAllowlistOnlyShrinks fails until the line is
// deleted, and it fails too unless the file's own count and laconicAllowlistMax
// have come down with it -- so the ceiling the file may never grow past is the
// count of today, not a slack a later message could be added into.

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

	"github.com/dimipaun/fugaro/internal/initflow"
)

// maxMessageChars is CLAUDE.md's bound on what a command tells a person: one
// line, what happened and what to do, no decision number and no internal name.
const maxMessageChars = 140

// laconicAllowlistMax is how many messages testdata/laconic_allowlist.txt
// carries. It is the file's own count, kept equal to it by
// TestLaconicAllowlistOnlyShrinks: it comes down with the list and never goes
// up, so a message that would have to be added is a message to shorten
// instead.
const laconicAllowlistMax = 1348

// laconicCalls are the calls whose string argument is a message, and which
// argument it is: this package's own refusal builder and fmt.Errorf, whose
// first argument is the sentence a caller reads as the reason its command
// stopped, and fmt's print family, whose second is (the first is the writer the
// sentence goes to).
var laconicCalls = map[string]int{
	"userErr":      0,
	"fmt.Errorf":   0,
	"fmt.Fprint":   1,
	"fmt.Fprintf":  1,
	"fmt.Fprintln": 1,
}

// laconicWarnCalls are the methods that warn, by the method's own name so that
// whatever the receiver is spelled as -- warn, r.warn, log.Warn -- is the same
// call: what one says is a message like any other.
var laconicWarnCalls = map[string]bool{"warn": true, "warnf": true}

// laconicCalleeArg is the argument of a call that carries the message, and
// whether the call is one that says one.
func laconicCalleeArg(fun ast.Expr) (int, bool) {
	name := calleeName(fun)
	if i, ok := laconicCalls[name]; ok {
		return i, true
	}
	last := name[strings.LastIndex(name, ".")+1:]
	if laconicWarnCalls[strings.ToLower(last)] {
		return 0, true
	}
	return 0, false
}

// cobraHelpFields are the help fields a help screen is bound to: cobra spells
// them Use, Short, Long and Example, and this package's own door structs spell
// them lower case.
var cobraHelpFields = map[string]bool{"use": true, "short": true, "long": true, "example": true}

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
	if len(msgs) < 2000 {
		t.Fatalf("the census found %d messages, and this package carries far more: a census that walks nothing passes everything", len(msgs))
	}
	// The hand-held tables are not allowlistable: a message built at run time
	// has no line in the tree to fail, so this is the whole of the hold on it.
	for _, m := range handHeldMessages() {
		n := len([]rune(m.text))
		switch {
		case m.text == "":
			t.Errorf("%s %s says nothing: %q", m.where, m.name, m.text)
		case strings.ContainsAny(m.text, "\r\n"):
			t.Errorf("%s %s is not one line: %q", m.where, m.name, m.text)
		case n > maxMessageChars:
			t.Errorf("%s %s is %d characters, over the %d a message gets: %q", m.where, m.name, n, maxMessageChars, m.text)
		}
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
		t.Errorf("%s %s is %d characters, over the %d a message gets: shorten it, or record it in %s with -update-laconic-allowlist: %q",
			m.where, m.name, n, maxMessageChars, laconicAllowlistPath, m.text)
	}
}

// TestLaconicAllowlistOnlyShrinks is the other half of the ratchet, and it is
// what makes the list shrink rather than merely sit still.
//
// Every line has to be a message the tree still says and that is still over the
// bound -- a line the tree has moved past is a line nobody re-read -- and the
// file's count has to be exactly laconicAllowlistMax, which is the count of the
// day this census was written. The equality is the point: a message that now
// fits takes its line out, and the count comes down with it, so the ceiling
// falls instead of leaving room for a long message to be allowlisted into it.
func TestLaconicAllowlistOnlyShrinks(t *testing.T) {
	if *updateLaconicAllowlist {
		t.Skip("the allowlist is being regenerated")
	}
	allow := readLaconicAllowlist(t)
	if len(allow) != laconicAllowlistMax {
		t.Errorf("%s carries %d messages and laconicAllowlistMax is %d: the list only shrinks, so a message that now fits takes its line out and the count comes down with it, and a message that was added is a message to shorten instead of a line to raise the count for",
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

// laconicMessage is one message, where it is said and what says it.
type laconicMessage struct {
	where string // the file and line, or the package a run-time sentence comes from
	name  string // the name it is bound to, or the call that says it
	text  string
}

// censusMessages is every message this package carries: the string values it
// binds to a name, the refusals its call sites write inline, and the sentences
// internal/initflow builds at run time.
func censusMessages(t *testing.T) []laconicMessage {
	t.Helper()
	src := parseCensusSource(t)
	help := helpValueNames(src)
	var out []laconicMessage
	add := func(file string, pos token.Pos, name string, value ast.Expr) {
		text, ok := foldMessageString(value)
		if !ok || text == "" || help[name] {
			return
		}
		out = append(out, laconicMessage{where: fmt.Sprintf("%s:%d", file, src.fset.Position(pos).Line), name: name, text: text})
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
			case *ast.CallExpr:
				i, ok := laconicCalleeArg(node.Fun)
				if !ok || len(node.Args) <= i {
					return true
				}
				add(file, node.Pos(), calleeName(node.Fun), node.Args[i])
			}
			return true
		})
	}
	out = append(out, initflowMessages()...)
	out = append(out, filledMessages()...)
	slices.SortFunc(out, func(a, b laconicMessage) int {
		if a.where != b.where {
			return strings.Compare(a.where, b.where)
		}
		return strings.Compare(a.name, b.name)
	})
	return out
}

// helpValueNames are the names bound to a help field -- applyLong, setupLong,
// updateLong, budget's own use, short and long -- read from the tree rather
// than listed here, so a help screen added later is excluded the day it is
// written.
func helpValueNames(src *censusSource) map[string]bool {
	out := map[string]bool{}
	for _, file := range src.files {
		ast.Inspect(file, func(n ast.Node) bool {
			field, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := field.Key.(*ast.Ident)
			if !ok || !cobraHelpFields[strings.ToLower(key.Name)] {
				return true
			}
			if value, ok := field.Value.(*ast.Ident); ok {
				out[value.Name] = true
			}
			return true
		})
	}
	return out
}

// initflowMessages are the sentences internal/initflow builds at run time,
// assembled from a marker or a flag rather than written as one literal, so no
// walk of its source can see them. They are read from the functions that build
// them, which is how the update-only test held them.
func initflowMessages() []laconicMessage {
	return []laconicMessage{
		{where: "internal/initflow", name: "AgentProceeded", text: initflow.AgentProceeded("CLAUDECODE")},
		{where: "internal/initflow", name: "GateProceeded", text: initflow.GateProceeded()},
		{where: "internal/initflow", name: "GateOrdinary", text: initflow.GateOrdinary()},
		{where: "internal/initflow", name: "OwnerProceeded", text: initflow.OwnerProceeded()},
		{where: "internal/initflow", name: "AskRestored", text: initflow.AskRestored()},
		{where: "internal/initflow", name: "AskUnmanaged", text: initflow.AskUnmanaged()},
		{where: "internal/initflow", name: "AskNoGate", text: initflow.AskNoGate()},
	}
}

// filledMessages are the messages this package writes from a format and its
// arguments: the walk sees the format -- and the bound on a format is not the
// bound on the sentence a person reads -- so each is read here from its own
// format with one sample set of arguments, the shape it is read in.
func filledMessages() []laconicMessage {
	r := &initRun{o: &initOptions{door: "update"}}
	return []laconicMessage{
		{where: "internal/cli", name: "accessStale", text: fmt.Sprintf(accessStale, "operators list", "a@example.com", "b@example.com")},
		{where: "internal/cli", name: "accessDropped", text: fmt.Sprintf(accessDropped, "a@example.com", "operator")},
		{where: "internal/cli", name: "accessAdded", text: fmt.Sprintf(accessAdded, "a@example.com")},
		{where: "internal/cli", name: "gateNamesAnIAMCall", text: r.gateNamesAnIAMCall("gs://fugaro-runs-aurora")},
		{where: "internal/cli", name: "reindexNotMarked", text: fmt.Sprintf(reindexNotMarked, "the budget database could not be opened: firestore 404")},
		{where: "internal/cli", name: "routingNoCheckoutWarn", text: fmt.Sprintf(routingNoCheckoutWarn, "acme/api, acme/web")},
		{where: "internal/cli", name: "routingUnwrittenWarn", text: fmt.Sprintf(routingUnwrittenWarn, "invalid_credentials")},
		{where: "internal/cli", name: "routingUnreadableWarn", text: fmt.Sprintf(routingUnreadableWarn, "/home/u/.config/fugaro/projects/aurora.yaml", "permission denied")},
		{where: "internal/cli", name: "routingWriteFailedWarn", text: fmt.Sprintf(routingWriteFailedWarn, "/home/u/.config/fugaro/projects/aurora.yaml", "permission denied")},
		{where: "internal/cli", name: "routingUnpublishedWarn", text: fmt.Sprintf(routingUnpublishedWarn, "team")},
		{where: "internal/cli", name: "routingFallbackAutoLine", text: routingFallbackAutoLine},
	}
}

// handHeldMessages are the messages the census reads from a table rather than
// from the source itself, so a test can hold each of their texts to the bound
// and to one line: a message built at run time has no line in the tree to fail.
func handHeldMessages() []laconicMessage {
	return append(initflowMessages(), filledMessages()...)
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

// readLaconicAllowlist is the recorded set, one Go-quoted message text per
// line. A missing file is the empty set rather than an error: a tree whose
// every message fits the bound carries no allowlist at all, which is the state
// the list is shrinking towards.
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
		text, err := strconv.Unquote(line)
		if err != nil {
			t.Fatalf("%s records a line that is not a quoted message: %v: %q", laconicAllowlistPath, err, line)
		}
		out[text] = true
	}
	return out
}

// laconicAllowlistHeader is the file's own comment block. It is written by the
// regeneration rather than kept beside it, so the file a run rewrites is the
// file a reader reads.
const laconicAllowlistHeader = `# Every message internal/cli carries that is longer than the 140 characters
# CLAUDE.md gives one line, one per line, Go-quoted so that a message with a
# newline in it is still one line of this file.
#
# A message is a string a command says to a person -- a refusal, a warning, a
# notice, a step reason, a caveat -- whether it is bound to a name or written at
# the call site. A value bound to a cobra help field (Use, Short, Long,
# Example) is a help screen rather than a message, and is not one.
# TestMessagesAreLaconic fails on a message over the bound that is not here.
#
# The list only shrinks. A message that is shortened leaves its line stale, and
# TestLaconicAllowlistOnlyShrinks fails until the line is deleted and
# laconicAllowlistMax in laconic_census_test.go comes down with it. A message
# that would have to be added is a message to shorten instead.
#
# Generated by
#   go test ./internal/cli -run TestMessagesAreLaconic -update-laconic-allowlist
`

// laconicAllowlistWrite rewrites the allowlist from the census: every message
// over the bound, one quoted text per line, in the order the census walks them.
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
		fmt.Fprintln(&b, strconv.Quote(m.text))
		n++
	}
	if err := os.WriteFile(laconicAllowlistPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", laconicAllowlistPath, err)
	}
	t.Logf("%s: %d messages over %d", laconicAllowlistPath, n, maxMessageChars)
}
