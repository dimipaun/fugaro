package yamledit

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// renderValue writes a value for a place in the document, keeping the quoting
// style of the node it replaces when there is one: a hand-written file should
// not lose the quotes its author chose.
func renderValue(value string, style yaml.Style) string {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return doubleQuote(value)
	case style&yaml.SingleQuotedStyle != 0:
		if s, ok := singleQuote(value); ok {
			return s
		}
		return doubleQuote(value)
	}
	if plainOK(value) {
		return value
	}
	return doubleQuote(value)
}

func renderKey(key string) string {
	if plainOK(key) {
		return key
	}
	return doubleQuote(key)
}

// plainOK reports whether a value written without quotes re-parses as exactly
// this string: as a string, and neither as another type (a boolean, a number, a
// null, a timestamp) nor as something that is not one scalar at all (a comment,
// a nested mapping or sequence, a trailing space). A tab is written quoted so
// that no value can ever look like indentation.
func plainOK(value string) bool {
	if value == "" || strings.ContainsAny(value, "\n\r\t") {
		return false
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(value), &n); err != nil {
		return false
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		return false
	}
	c := n.Content[0]
	return c.Kind == yaml.ScalarNode && c.Style == 0 && c.Tag == "!!str" && c.Value == value
}

func doubleQuote(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0x85:
			b.WriteString(`\N`)
		case 0x2028:
			b.WriteString(`\L`)
		case 0x2029:
			b.WriteString(`\P`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// singleQuote writes a single-quoted scalar, which holds any text that is not a
// line break or a control character: a doubled quote stands for one.
func singleQuote(value string) (string, bool) {
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == 0x85 || r == 0x2028 || r == 0x2029 {
			return "", false
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'", true
}

// leafOf renders the last element of a path, at the indentation it belongs at.
type leafOf func(key string, indent, step int) []string

// scalarLeaf renders `key: value`.
func scalarLeaf(value string) leafOf {
	return func(key string, indent, _ int) []string {
		return []string{indentOf(indent) + renderKey(key) + ": " + renderValue(value, 0)}
	}
}

// listLeaf renders `key:` with one sequence item under it.
func listLeaf(value string) leafOf {
	return func(key string, indent, step int) []string {
		return []string{
			indentOf(indent) + renderKey(key) + ":",
			indentOf(indent+step) + "- " + renderValue(value, 0),
		}
	}
}

// itemLeaf renders `key:` with one sequence item under it, that item a mapping
// whose keyField comes first and whose other fields line up under it.
func itemLeaf(keyField, keyValue string, fields map[string]string) leafOf {
	return func(key string, indent, step int) []string {
		item := indentOf(indent+step) + "- " + renderKey(keyField) + ": " + renderValue(keyValue, 0)
		lines := []string{indentOf(indent) + renderKey(key) + ":", item}
		for _, f := range sortedKeys(fields) {
			lines = append(lines, indentOf(indent+step+2)+renderKey(f)+": "+renderValue(fields[f], 0))
		}
		return lines
	}
}

// chain renders a path as a block of nested mappings, calling leaf for its last
// element. The intermediate keys are written empty, which is how a document
// grows a level a hand-written file did not have yet.
func chain(path []string, indent, step int, leaf leafOf) []string {
	if len(path) == 1 {
		return leaf(path[0], indent, step)
	}
	lines := []string{indentOf(indent) + renderKey(path[0]) + ":"}
	return append(lines, chain(path[1:], indent+step, step, leaf)...)
}

func indentOf(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// sortedKeys keeps a map's fields in one order, so that two runs of the same
// edit write the same bytes.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// splice replaces src[start:end] with repl and leaves every other byte alone.
func splice(src []byte, start, end int, repl []byte) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(repl))
	out = append(out, src[:start]...)
	out = append(out, repl...)
	out = append(out, src[end:]...)
	return out
}
