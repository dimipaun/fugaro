package yamledit_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/yamledit"
	"gopkg.in/yaml.v3"
)

// FuzzSetScalar holds the package to the promises it makes about bytes it did not
// write itself: no input ever panics it, a refusal returns the input untouched,
// and an edit that succeeds re-parses to the value that was asked for, in a
// document where every comment the file had is still there and everything off
// the path still reads back as it did. A splice that drifted by a byte, or a
// quoting decision that produced text YAML reads as something else, shows up as
// one of those.
func FuzzSetScalar(f *testing.F) {
	for _, seed := range []struct{ doc, key, value string }{
		{"", "a", "value"},
		{"# a comment\n", "a", "value"},
		{"project: workspace-a\n", "budget", "5"},
		{"project: workspace-a\nrigor:\n  default: standard   # or startup\n", "rigor", "pro"},
		{"allow_data_to:\n  - owner/repo-a\n", "allow_data_to", "owner/repo-b"},
		{"a: 'single'\nb: \"double\"\nc: plain\n", "b", "owner/repo-a"},
		{"a:\n  b:\nc: 3\n", "b", "1"},
		{"a: {b: 1}\n", "a", "2"},
		{"a: [x, y]\n", "a", "2"},
		{"a: |\n  line\n", "a", "line"},
		{"a: &anchor 1\nb: *anchor\n", "b", "2"},
		{"base: &base\n  x: 1\nprod:\n  <<: *base\n", "prod", "2"},
		{"a: 1\na: 2\n", "a", "3"},
		{"a:\n\tb: 1\n", "a", "2"},
		{"a: 1\n---\nb: 2\n", "a", "2"},
		{"a: 1\r\nb: 2\r\n", "b", "3"},
		{"# budget:\n#   max_runs: 20\nproject: workspace-a\n", "budget", "5"},
		{"a:\n  # keep me\n  x: 1\nb: 1\n", "a", "z"},
		{"list:\n  - x\n", "list", "y"},
		{"a: # note\n  x: 1\n", "a", "z"},
		{"a: pro # a value that looks like a number\n", "a", "true"},
	} {
		f.Add(seed.doc, seed.key, seed.value)
	}
	f.Fuzz(func(t *testing.T, doc, key, value string) {
		if !utf8.ValidString(doc) || !utf8.ValidString(key) || !utf8.ValidString(value) {
			// A settings file and the value put into it are text; bytes that are
			// not text are nothing this package speaks about.
			return
		}
		src := []byte(doc)
		path := []string{key}
		out, err := yamledit.SetScalar(src, path, value)
		if err != nil {
			if !bytes.Equal(out, src) {
				t.Fatalf("a refusal returned changed bytes: %q -> %q (%v)", src, out, err)
			}
			return
		}
		reparses(t, out)
		if got, ok := valueAt(out, path...); !ok || got != value {
			t.Fatalf("the result does not carry %q at %v: %q (%v)\n%q", value, path, got, ok, out)
		}
		if !isSubsequence(commentLines(doc), commentLines(string(out))) {
			t.Fatalf("a comment went missing:\n%q\n%q", doc, out)
		}
		if !sameOffThePath(decodeMap(doc), decodeMap(string(out)), path) {
			t.Fatalf("the edit reached past %v:\n%q\n%q", path, doc, out)
		}
		again, err := yamledit.SetScalar(out, path, value)
		if err != nil {
			t.Fatalf("the same edit again was refused: %v\n%q", err, out)
		}
		if !bytes.Equal(again, out) {
			t.Fatalf("the same edit again rewrote the document:\n%q\n%q", out, again)
		}
	})
}

// valueAt returns the text of the scalar written at a path, read out of the
// document the way any reader of the file reads it. The text is what a setting
// carries, whatever tag YAML gives those bytes.
func valueAt(doc []byte, path ...string) (string, bool) {
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil || len(n.Content) == 0 {
		return "", false
	}
	cur := n.Content[0]
	for _, key := range path {
		if cur.Kind != yaml.MappingNode {
			return "", false
		}
		var next *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == key {
				next = cur.Content[i+1]
			}
		}
		if next == nil {
			return "", false
		}
		cur = next
	}
	if cur.Kind != yaml.ScalarNode {
		return "", false
	}
	return cur.Value, true
}

// decodeMap reads a document into plain Go values, which is how the parts of it
// that an edit did not touch are compared. A document that is not a mapping of
// strings decodes to nil: there is nothing there to compare.
func decodeMap(doc string) map[string]any {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		return nil
	}
	return m
}

// sameOffThePath reports whether everything the edit did not have to touch still
// reads back as it did. Anything at the path itself may change; a key beside it
// may not. A layer the path had to be created through holds nothing that was
// there before, so there is nothing to compare under it.
func sameOffThePath(before, after any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	bm, ok := before.(map[string]any)
	if !ok {
		return true
	}
	am, ok := after.(map[string]any)
	if !ok {
		return false
	}
	for key, bv := range bm {
		av, ok := am[key]
		if !ok {
			return false
		}
		if key == path[0] {
			if !sameOffThePath(bv, av, path[1:]) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(bv, av) {
			return false
		}
	}
	return true
}

func commentLines(doc string) []string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "#") {
			out = append(out, line)
		}
	}
	return out
}

func isSubsequence(want, got []string) bool {
	i := 0
	for _, line := range got {
		if i < len(want) && want[i] == line {
			i++
		}
	}
	return i == len(want)
}
