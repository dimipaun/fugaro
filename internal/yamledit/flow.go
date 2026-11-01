package yamledit

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// A flow mapping written on one line is editable, and only that: the project
// config most installations have writes its budget block as
// `budget: { mode: enforce, per_run_usd: 5 }`, and refusing every edit inside
// `{...}` refused the shape setup itself writes. What is spliced here is the
// bytes of one member's value, or one member with its separator — the braces,
// the spacing, the comments on the line and the quoting of every member the
// edit does not name stay as they were written.
//
// Everything a splice would have to guess at is refused: a collection written
// across lines or holding a comment (no span to measure, and bytes a removal
// would take that are not the member's), a member whose own value is a
// collection (a nested `{...}` or `[...]` has bytes of its own), and an empty
// `{}`, whose shape writing the first member would decide. A key that is not
// written is added to a mapping that already has a member; nothing is written
// into a collection with no members.

// setFlowKey writes one member of a flow mapping: the value of a key that is
// written, or a new member in a mapping that already holds one.
func (d *doc) setFlowKey(op string, m *yaml.Node, key, value, path string) ([]byte, error) {
	e, err := d.find(op, m, key, path)
	if err != nil {
		return nil, err
	}
	open, close, err := d.flowBounds(op, m)
	if err != nil {
		return nil, err
	}
	if e == nil {
		if mergeKey(m) {
			return nil, refuse(op, path, ReasonMergeKey)
		}
		if len(m.Content) == 0 {
			return nil, refuse(op, path, ReasonFlowCollection)
		}
		return d.addFlowMember(open, close, key, value), nil
	}
	if err := d.anchorFree(op, e, path); err != nil {
		return nil, err
	}
	start, end, err := d.flowValueEnd(op, e.value, close)
	if err != nil {
		return nil, err
	}
	want := []byte(renderFlowValue(value, e.value.Style))
	if bytes.Equal(d.src[start:end], want) {
		return d.src, nil
	}
	return splice(d.src, start, end, want), nil
}

// addFlowMember writes a new member into a flow mapping that already has one:
// the separator goes in front of it, and the spacing the author left before the
// closing brace stays where it is.
func (d *doc) addFlowMember(open, close int, key, value string) []byte {
	at := close - 1
	for at > open+1 && (d.src[at-1] == ' ' || d.src[at-1] == '\t') {
		at--
	}
	sep := ", "
	if at > open+1 && d.src[at-1] == ',' {
		sep = " "
	}
	member := sep + renderFlowKey(key) + ": " + renderFlowValue(value, 0)
	return splice(d.src, at, at, []byte(member))
}

// flowMemberSpan returns the bytes one member of a flow mapping occupies,
// together with the separator a removal takes with it: the comma that separates
// it from its neighbour, on whichever side one is written, and the spaces
// around that comma. A member written alone between the braces takes their
// padding with it, so a mapping whose last member goes is left `{}`.
func (d *doc) flowMemberSpan(op string, m *yaml.Node, e *entry) (int, int, error) {
	open, close, err := d.flowBounds(op, m)
	if err != nil {
		return 0, 0, err
	}
	start, err := d.offset(e.key)
	if err != nil {
		return 0, 0, withOp(err, op)
	}
	_, end, err := d.flowValueEnd(op, e.value, close)
	if err != nil {
		return 0, 0, err
	}
	if i := skipSpaces(d.src, end, close); i < close && d.src[i] == ',' {
		return start, skipSpaces(d.src, i+1, close), nil
	}
	if i := skipSpacesBack(d.src, start-1, open); i > open && d.src[i] == ',' {
		return i, end, nil
	}
	if len(m.Content) != 2 {
		// No separator on either side and a neighbour written all the same:
		// these bytes are not a member this package can cut out whole.
		return 0, 0, refuse(op, "", ReasonFlowCollection)
	}
	return open + 1, close - 1, nil
}

// flowValueEnd locates the bytes a member's value was written with inside a
// flow collection, where the members that follow it are on the same line and
// the value does not run to the end of it. A value written as a collection of
// its own is refused: its span is not one this package measures, and replacing
// it would drop the bytes and comments inside it. A value written with nothing
// at all — `{ mode: , per_run_usd: 5 }` — is an empty span at the offset its
// value belongs at, which is where a set writes one in.
func (d *doc) flowValueEnd(op string, v *yaml.Node, close int) (int, int, error) {
	if v.Kind != yaml.ScalarNode {
		return 0, 0, refuse(op, "", ReasonFlowCollection)
	}
	if v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return 0, 0, refuse(op, "", ReasonBlockScalar)
	}
	start, err := d.offset(v)
	if err != nil {
		return 0, 0, withOp(err, op)
	}
	if start < len(d.src) && (d.src[start] == '!' || d.src[start] == '&') {
		return 0, 0, refuse(op, "", ReasonAnchorOrAlias)
	}
	if v.Tag == "!!null" && v.Value == "" && start < len(d.src) &&
		(d.src[start] == ',' || d.src[start] == '}') {
		return start, start, nil
	}
	limit := close
	if v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
		limit = start
		for limit < close && !flowSeparator(d.src[limit]) {
			limit++
		}
	}
	n := scalarLength(v, d.src[start:limit])
	if n <= 0 || n > limit-start || !d.sameFlowScalar(v, d.src[start:start+n]) {
		return 0, 0, refuse(op, "", ReasonUnlocatable)
	}
	return start, start + n, nil
}

// flowBounds returns where a flow mapping's braces are. Every splice inside a
// flow collection is measured from them, so a collection whose braces are not
// both on its own line is refused here.
func (d *doc) flowBounds(op string, m *yaml.Node) (int, int, error) {
	open, err := d.offset(m)
	if err != nil {
		return 0, 0, withOp(err, op)
	}
	if open >= len(d.src) || d.src[open] != '{' {
		return 0, 0, refuse(op, "", ReasonUnlocatable)
	}
	close, err := d.flowClose(op, open)
	if err != nil {
		return 0, 0, err
	}
	return open, close, nil
}

// flowClose returns the offset just past the `}` that closes the flow
// collection opening at start. The scan ends with the line: a collection
// written across lines, and one holding a comment, are refused rather than
// measured. A quoted scalar is skipped whole, so the separators and braces
// written inside one are its text and not the collection's.
func (d *doc) flowClose(op string, start int) (int, error) {
	end := d.lines.contentEnd(d.lines.lineOf(start))
	depth := 0
	for i := start; i < end; {
		switch c := d.src[i]; {
		case c == '"' || c == '\'':
			escape := c
			if c == '"' {
				escape = '\\'
			}
			n := quotedEnd(d.src[i:end], c, escape)
			if n == 0 {
				return 0, refuse(op, "", ReasonFlowCollection)
			}
			i += n
		case c == '{' || c == '[':
			depth++
			i++
		case c == '}' || c == ']':
			depth--
			if depth <= 0 {
				return i + 1, nil
			}
			i++
		case c == '#' && (i == start || d.src[i-1] == ' ' || d.src[i-1] == '\t'):
			return 0, refuse(op, "", ReasonFlowCollection)
		default:
			i++
		}
	}
	return 0, refuse(op, "", ReasonFlowCollection)
}

// flowSeparator reports whether a byte ends a plain scalar inside a flow
// collection: the separators and the brackets that close one.
func flowSeparator(b byte) bool {
	switch b {
	case ',', '}', ']', '\n', '\r':
		return true
	}
	return false
}

func skipSpaces(src []byte, off, limit int) int {
	for off < limit && (src[off] == ' ' || src[off] == '\t') {
		off++
	}
	return off
}

func skipSpacesBack(src []byte, off, limit int) int {
	for off > limit && (src[off] == ' ' || src[off] == '\t') {
		off--
	}
	return off
}
