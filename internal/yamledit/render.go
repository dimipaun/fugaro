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
	if _, ok := plainForm(value); ok {
		return value
	}
	return doubleQuote(value)
}

// renderKey writes a mapping key, which is always a string: a key that resolved
// to a number would not be found by the reader that looks for it by name.
func renderKey(key string) string {
	if tag, ok := plainForm(key); ok && tag == "!!str" {
		return key
	}
	return doubleQuote(key)
}

// plainForm reports whether a value written without quotes reads back as exactly
// this text — one plain scalar, not a comment, a nested mapping, a sequence or a
// trimmed string — and returns the tag YAML gives it.
//
// The tag is returned rather than required to be a string because the settings
// this package edits decide their own types: `retries: 5` is how a schema spells
// a number, and a writer that quoted it would leave the file unloadable for the
// run that reads it next. A value whose plain form would read as
// something other than this text — `#x` is a comment, `a: b` is a mapping, `- x`
// is a sequence, ` pro` would lose its space — is quoted instead. A tab is
// quoted too, so that no value can ever look like indentation.
func plainForm(value string) (string, bool) {
	if value == "" || strings.ContainsAny(value, "\n\r\t") {
		return "", false
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(value), &n); err != nil {
		return "", false
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		return "", false
	}
	c := n.Content[0]
	if c.Kind != yaml.ScalarNode || c.Style != 0 || c.Tag == "!!merge" || c.Anchor != "" || c.Value != value {
		return "", false
	}
	return c.Tag, true
}

// renderFlowValue writes a value for a place inside a flow collection, keeping
// the quoting style already there and quoting a value whose plain form would
// not read back as itself between braces.
func renderFlowValue(value string, style yaml.Style) string {
	if style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		return renderValue(value, style)
	}
	if _, ok := flowPlainForm(value); ok {
		return value
	}
	return doubleQuote(value)
}

// renderFlowKey writes a mapping key for a place inside a flow collection.
func renderFlowKey(key string) string {
	if tag, ok := flowPlainForm(key); ok && tag == "!!str" {
		return key
	}
	return doubleQuote(key)
}

// flowPlainForm reports whether a value written without quotes inside a flow
// collection reads back as exactly this text, and returns the tag YAML gives
// it. plainForm's answer is about block style, where `x, y` is one scalar and
// `}` is a character like any other; between braces the same bytes are two
// members and the end of the mapping, so the value is read back where it is
// going to be written rather than trusted to the other answer. A quote is
// refused whatever YAML reads it as, because this package's own scan of the line
// takes it for the start of a quoted scalar and would never find the end of the
// member again.
func flowPlainForm(value string) (string, bool) {
	if value == "" || strings.ContainsAny(value, "\n\r\t\"'") {
		return "", false
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("k: {v: "+value+"}"), &n); err != nil {
		return "", false
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		return "", false
	}
	m := mapValue(n.Content[0], "k")
	if m == nil || m.Kind != yaml.MappingNode || len(m.Content) != 2 {
		return "", false
	}
	c := m.Content[1]
	if c.Kind != yaml.ScalarNode || c.Style != 0 || c.Tag == "!!merge" ||
		c.Anchor != "" || c.Value != value {
		return "", false
	}
	return c.Tag, true
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
