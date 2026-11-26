package recipe

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rename is a repository's file with its top-level name: scalar rewritten to
// name and every other byte untouched (R14, the copy rule of copyname.go): the
// copy is the same recipe under another name, so the line's quoting, its
// comment and the rest of the text stay exactly as the repository wrote them.
//
// The migration publishes copies with it, and a reader that has to compare a
// copy against a record of the repository's file rewrites one back with it
// (internal/cli's carryChosenRecipe): what a copy says of the file it came from
// is its own text modulo this one line, so it is the one rewrite both sides
// spell the same way.
func Rename(data []byte, name string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("is not a YAML mapping")
	}
	m := doc.Content[0]
	var val *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "name" {
			val = m.Content[i+1]
			break
		}
	}
	if val == nil || val.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("writes no name: scalar")
	}
	lines := splitLines(data)
	if val.Line < 1 || val.Line > len(lines) {
		return nil, fmt.Errorf("its name: value is not on a line")
	}
	line, start := lines[val.Line-1], val.Column-1
	if start < 0 || start >= len(line) {
		return nil, fmt.Errorf("its name: value is not where the parser put it")
	}
	width, ok := scalarWidth(line[start:])
	if !ok {
		return nil, fmt.Errorf("its name: value's end could not be found")
	}
	at := start
	for i := 0; i < val.Line-1; i++ {
		at += len(lines[i])
	}
	out := make([]byte, 0, len(data)+len(name))
	out = append(out, data[:at]...)
	out = append(out, name...)
	out = append(out, data[at+width:]...)
	// A rewrite this function got wrong is not published: the copy is read by
	// the same parser that read the file, and a name: line the text no longer
	// agrees with is a recipe no run can resolve.
	if got, ok := topName(out); !ok || got != name {
		return nil, fmt.Errorf("its name: line could not be rewritten")
	}
	return out, nil
}

// topName is the top-level name: scalar of a recipe's text.
func topName(data []byte) (string, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", false
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "name" {
			v := m.Content[i+1]
			return v.Value, v.Kind == yaml.ScalarNode
		}
	}
	return "", false
}

// splitLines splits data into lines, each keeping its own terminator, so a
// rewrite puts back the bytes it did not touch exactly as they were.
func splitLines(data []byte) []string {
	var out []string
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return append(out, string(data))
		}
		out = append(out, string(data[:i+1]))
		data = data[i+1:]
	}
	return out
}

// scalarWidth is the width of the YAML scalar token s starts with: a quoted
// scalar's whole token, escapes and doubled quotes included, or a plain
// scalar's text up to a comment or the line's end. It is the width the rewrite
// replaces, so the comment after a name: value survives it.
func scalarWidth(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	switch s[0] {
	case '"':
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				return i + 1, true
			}
		}
		return 0, false
	case '\'':
		for i := 1; i < len(s); i++ {
			if s[i] != '\'' {
				continue
			}
			if i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			return i + 1, true
		}
		return 0, false
	default:
		i := strings.IndexAny(s, "#\r\n")
		if i < 0 {
			i = len(s)
		}
		tok := strings.TrimRight(s[:i], " \t")
		if tok == "" {
			return 0, false
		}
		return len(tok), true
	}
}
