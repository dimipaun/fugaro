package yamledit

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// doc is one parsed document together with everything needed to map the nodes
// yaml.v3 hands back onto the bytes they were written from.
type doc struct {
	src   []byte
	root  *yaml.Node // the root mapping; nil when the document holds no mapping
	lines lineIndex
	eol   string
	step  int
}

// entry is one key/value pair of a block mapping.
type entry struct {
	key   *yaml.Node
	value *yaml.Node
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// lineIndex maps a line and column — what yaml.v3 reports, counted in runes —
// back to a byte offset. yaml.v3 treats CR, LF, CRLF, NEL, LS and PS as line
// breaks, so this does too, and a leading byte-order mark is skipped because
// yaml.v3 does not count it in the first line's columns.
type lineIndex struct {
	starts []int // starts[i]: the offset where line i+1 begins
	breaks []int // breaks[i]: the offset where the break ending line i+1 begins
	end    int
}

func newLineIndex(src []byte) lineIndex {
	first := 0
	if bytes.HasPrefix(src, bom) {
		first = len(bom)
	}
	idx := lineIndex{starts: []int{first}, end: len(src)}
	for i := first; i < len(src); {
		w := breakWidth(src, i)
		if w == 0 {
			i++
			continue
		}
		idx.breaks = append(idx.breaks, i)
		i += w
		idx.starts = append(idx.starts, i)
	}
	return idx
}

// breakWidth returns the length of the line break at i, or 0 when there is
// none: the same set yaml.v3's scanner breaks lines on.
func breakWidth(src []byte, i int) int {
	switch {
	case src[i] == '\r':
		if i+1 < len(src) && src[i+1] == '\n' {
			return 2
		}
		return 1
	case src[i] == '\n':
		return 1
	case src[i] == 0xC2 && i+1 < len(src) && src[i+1] == 0x85:
		return 2
	case src[i] == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && (src[i+2] == 0xA8 || src[i+2] == 0xA9):
		return 3
	}
	return 0
}

func (idx lineIndex) count() int { return len(idx.starts) }
func (idx lineIndex) start(line int) int {
	if line < 1 {
		return 0
	}
	if line > len(idx.starts) {
		return idx.end
	}
	return idx.starts[line-1]
}

// contentEnd returns where the line's text ends, before its break.
func (idx lineIndex) contentEnd(line int) int {
	if line >= 1 && line <= len(idx.breaks) {
		return idx.breaks[line-1]
	}
	return idx.end
}

// endsWithBreak reports whether the last line was opened by a break, which is
// how a document that ends with a newline is told from one that does not: a
// trailing break leaves one more line start than there are breaks, and that
// last start sits at the end of the text.
func (idx lineIndex) endsWithBreak() bool {
	return len(idx.breaks) > 0 && idx.starts[len(idx.starts)-1] == idx.end
}

// lineOf returns the line holding an offset. A break belongs to the line it
// ends, so an offset on a break names the line above it.
func (idx lineIndex) lineOf(off int) int {
	i := sort.Search(len(idx.starts), func(i int) bool { return idx.starts[i] > off }) - 1
	if i < 0 {
		return 1
	}
	return i + 1
}

// detectEOL returns the first line ending the document uses, so that lines this
// package adds look like the lines already there.
func detectEOL(src []byte) string {
	for i := 0; i < len(src); {
		if w := breakWidth(src, i); w > 0 {
			return string(src[i : i+w])
		}
		i++
	}
	return "\n"
}

// hasTabIndent reports whether a line's leading whitespace holds a tab.
func hasTabIndent(line []byte) bool {
	for _, b := range line {
		switch b {
		case ' ':
		case '\t':
			return true
		default:
			return false
		}
	}
	return false
}

func checkIndentationTabs(src []byte) error {
	lines := newLineIndex(src)
	for line := 1; line <= lines.count(); line++ {
		if hasTabIndent(src[lines.start(line):lines.contentEnd(line)]) {
			return &Error{Reason: ReasonTabIndentation}
		}
	}
	return nil
}

// decodeOne returns the document's root mapping. A second document is refused
// rather than ignored: an edit that rewrote one document of a stream would
// change which document a reader sees.
func decodeOne(src []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var first yaml.Node
	if err := dec.Decode(&first); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, &Error{Reason: ReasonMalformed}
	}
	var second yaml.Node
	switch err := dec.Decode(&second); {
	case err == nil:
		return nil, &Error{Reason: ReasonMultiDocument}
	case !errors.Is(err, io.EOF):
		return nil, &Error{Reason: ReasonMalformed}
	}
	if first.Kind == 0 || len(first.Content) == 0 {
		return nil, nil
	}
	return first.Content[0], nil
}

func open(op string, src []byte) (*doc, error) {
	d := &doc{src: src, lines: newLineIndex(src), eol: detectEOL(src)}
	if err := checkIndentationTabs(src); err != nil {
		return nil, withOp(err, op)
	}
	root, err := decodeOne(src)
	if err != nil {
		return nil, withOp(err, op)
	}
	d.root = root
	d.step = indentStep(root)
	return d, nil
}

// indentStep returns the width one nesting level is indented by in this
// document, so a mapping this package creates looks like the ones around it.
func indentStep(root *yaml.Node) int {
	step := 0
	var walk func(m *yaml.Node)
	walk = func(m *yaml.Node) {
		if m == nil || m.Kind != yaml.MappingNode || len(m.Content) == 0 {
			return
		}
		base := m.Content[0].Column - 1
		for i := 0; i+1 < len(m.Content); i += 2 {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode || v.Style&yaml.FlowStyle != 0 || len(v.Content) == 0 {
				continue
			}
			if d := v.Content[0].Column - 1 - base; d > 0 && (step == 0 || d < step) {
				step = d
			}
			walk(v)
		}
	}
	walk(root)
	if step == 0 {
		return 2
	}
	return step
}

// offset returns the byte offset a node was written at.
func (d *doc) offset(n *yaml.Node) (int, error) {
	if n == nil || n.Line < 1 || n.Line > d.lines.count() {
		return 0, refuse("", "", ReasonUnlocatable)
	}
	off := d.lines.start(n.Line)
	end := d.lines.contentEnd(n.Line)
	for c := 1; c < n.Column; c++ {
		if off >= end {
			return 0, refuse("", "", ReasonUnlocatable)
		}
		_, size := utf8.DecodeRune(d.src[off:end])
		if size == 0 {
			size = 1
		}
		off += size
	}
	if off > end {
		return 0, refuse("", "", ReasonUnlocatable)
	}
	return off, nil
}

func leadingSpaces(line []byte) int {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	return i
}

// expectMapping refuses a path that crosses anything but a block mapping. A
// flow mapping is refused too: an edit inside `{...}` would have to reformat
// bytes this package promises to leave alone.
func (d *doc) expectMapping(op string, n *yaml.Node, path string) error {
	if n.Style&yaml.FlowStyle != 0 {
		return refuse(op, path, ReasonFlowCollection)
	}
	if n.Kind != yaml.MappingNode {
		return refuse(op, path, ReasonNotAMapping)
	}
	return nil
}

// find returns the entry written for key. A key written twice is refused rather
// than guessed at, because which of the two an edit changed would be invisible
// in the result.
func (d *doc) find(op string, m *yaml.Node, key, path string) (*entry, error) {
	var found *entry
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		if found != nil {
			return nil, refuse(op, path, ReasonDuplicateKey)
		}
		found = &entry{key: m.Content[i], value: m.Content[i+1]}
	}
	return found, nil
}

// mergeKey reports whether a mapping carries `<<`. Where it does, what the
// document means is decided partly by another mapping, so a key that is created
// in it — or removed from it — cannot be resolved from these bytes alone.
func mergeKey(m *yaml.Node) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Tag == "!!merge" {
			return true
		}
	}
	return false
}

// anchorFree refuses a node an edit cannot replace without dropping what is
// written on it.
func (d *doc) anchorFree(op string, e *entry, path string) error {
	if e.key.Anchor != "" || e.value.Anchor != "" || e.value.Kind == yaml.AliasNode {
		return refuse(op, path, ReasonAnchorOrAlias)
	}
	return nil
}

// emptyValue reports whether an entry writes no value at all: `key:` with
// nothing after it on its line but spaces and a comment. yaml.v3 reads that as
// a null, but a written `key: null` is a value this package replaces.
func (d *doc) emptyValue(v *yaml.Node) (bool, error) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!null" || v.Value != "" {
		return false, nil
	}
	off, err := d.offset(v)
	if err != nil {
		return false, err
	}
	end := d.lines.contentEnd(d.lines.lineOf(off))
	return strings.TrimSpace(string(plainComment(d.src[off:end]))) == "", nil
}

// plainComment cuts a line's text where a comment starts.
func plainComment(text []byte) []byte {
	for i := 0; i < len(text); i++ {
		if text[i] == '#' && (i == 0 || text[i-1] == ' ' || text[i-1] == '\t') {
			return text[:i]
		}
	}
	return text
}

// valueSpan locates the bytes a scalar was written with. yaml.v3 reports where
// a scalar starts but never where it ends, so the candidate text is re-parsed
// and held against the node: when they agree, the scalar is complete on that
// one line and the span is exact. A scalar written across lines is refused
// rather than guessed at.
func (d *doc) valueSpan(op string, v *yaml.Node) (int, int, error) {
	if v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return 0, 0, refuse(op, "", ReasonBlockScalar)
	}
	start, err := d.offset(v)
	if err != nil {
		return 0, 0, withOp(err, op)
	}
	if d.src[start] == '!' || d.src[start] == '&' {
		return 0, 0, refuse(op, "", ReasonAnchorOrAlias)
	}
	rest := d.src[start:d.lines.contentEnd(d.lines.lineOf(start))]
	n, ok := d.scalarText(v, rest)
	if !ok {
		return 0, 0, refuse(op, "", ReasonMultilineScalar)
	}
	return start, start + n, nil
}

// scalarText returns the length of the written scalar at the start of rest,
// which is on one line only when it re-parses as exactly this node.
func (d *doc) scalarText(v *yaml.Node, rest []byte) (int, bool) {
	var n int
	switch {
	case v.Style&yaml.DoubleQuotedStyle != 0:
		n = quotedEnd(rest, '"', '\\')
	case v.Style&yaml.SingleQuotedStyle != 0:
		n = quotedEnd(rest, '\'', '\'')
	default:
		n = len(plainComment(rest))
		for n > 0 && (rest[n-1] == ' ' || rest[n-1] == '\t') {
			n--
		}
	}
	if n <= 0 || n > len(rest) {
		return 0, false
	}
	return n, d.sameScalar(v, rest[:n])
}

// quotedEnd returns the length of a quoted scalar, closing quote included, or 0
// when the quote does not close on this line. In a double-quoted scalar a
// backslash escapes the next character; in a single-quoted one a doubled quote
// stands for one.
func quotedEnd(rest []byte, quote, escape byte) int {
	for i := 1; i < len(rest); i++ {
		switch rest[i] {
		case escape:
			if quote == escape && (i+1 >= len(rest) || rest[i+1] != '\'') {
				return i + 1
			}
			i++
		case quote:
			return i + 1
		}
	}
	return 0
}

func (d *doc) sameScalar(v *yaml.Node, text []byte) bool {
	if len(text) == 0 {
		return false
	}
	var n yaml.Node
	if err := yaml.Unmarshal(text, &n); err != nil {
		return false
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		return false
	}
	c := n.Content[0]
	return c.Kind == yaml.ScalarNode && c.Tag == v.Tag && c.Value == v.Value
}

// nodeEnd returns the offset just past a node's written text.
func (d *doc) nodeEnd(op string, n *yaml.Node) (int, error) {
	switch n.Kind {
	case yaml.AliasNode:
		start, err := d.offset(n)
		if err != nil {
			return 0, withOp(err, op)
		}
		return start + 1 + len(n.Value), nil
	case yaml.ScalarNode:
		if empty, err := d.emptyValue(n); err != nil {
			return 0, withOp(err, op)
		} else if empty {
			return d.offset(n)
		}
		_, end, err := d.valueSpan(op, n)
		return end, err
	case yaml.MappingNode, yaml.SequenceNode:
		if n.Style&yaml.FlowStyle != 0 {
			return d.flowEnd(op, n)
		}
		if len(n.Content) == 0 {
			return 0, refuse(op, "", ReasonUnlocatable)
		}
		return d.nodeEnd(op, n.Content[len(n.Content)-1])
	}
	return 0, refuse(op, "", ReasonUnlocatable)
}

// flowEnd bounds a flow collection that is written on one line, which is all a
// removal needs. A flow collection that runs across lines has no end this
// package can measure, so it is refused.
func (d *doc) flowEnd(op string, n *yaml.Node) (int, error) {
	last := n
	for len(last.Content) > 0 {
		last = last.Content[len(last.Content)-1]
	}
	if last.Line != n.Line {
		return 0, refuse(op, "", ReasonFlowCollection)
	}
	start, err := d.offset(n)
	if err != nil {
		return 0, withOp(err, op)
	}
	return d.lines.contentEnd(d.lines.lineOf(start)), nil
}

// entryIndent is the column a block mapping writes its keys at, which is where
// a key this package adds to it belongs.
func (d *doc) entryIndent(m *yaml.Node) int {
	if len(m.Content) == 0 {
		return 0
	}
	return m.Content[0].Column - 1
}

// blockEnd returns the offset where a new line can be opened after a block
// mapping's or sequence's last entry: the end of its last line's text.
func (d *doc) blockEnd(op string, n *yaml.Node) (int, error) {
	end, err := d.nodeEnd(op, n)
	if err != nil {
		return 0, err
	}
	return d.lines.contentEnd(d.lines.lineOf(end)), nil
}

// removalEnd extends an offset to the end of the line it sits on, the line
// break included: an entry owns its whole last line, a trailing comment with it.
func (d *doc) removalEnd(off int) int {
	line := d.lines.lineOf(off)
	if line < d.lines.count() {
		return d.lines.start(line + 1)
	}
	return d.lines.end
}

// entryStart returns where an entry's own text begins: the start of its key's
// line, back over the comment lines that sit directly above it. An entry a path
// reaches is always written at the start of its line.
func (d *doc) entryStart(op string, e *entry) (int, error) {
	keyStart, err := d.offset(e.key)
	if err != nil {
		return 0, withOp(err, op)
	}
	line := d.lines.lineOf(keyStart)
	lineStart := d.lines.start(line)
	indent := leadingSpaces(d.src[lineStart:d.lines.contentEnd(line)])
	return d.absorbComments(lineStart, indent), nil
}

// absorbComments walks upward over the comment lines that belong to an entry:
// the lines directly above it, with no blank line between, indented at least as
// far as the entry itself. A blank line, a less indented comment or any other
// line ends the walk.
func (d *doc) absorbComments(start, indent int) int {
	for line := d.lines.lineOf(start); line > 1; {
		prev := line - 1
		ps, pe := d.lines.start(prev), d.lines.contentEnd(prev)
		text := d.src[ps:pe]
		trimmed := bytes.TrimLeft(text, " ")
		if len(trimmed) == 0 || trimmed[0] != '#' || len(text)-len(trimmed) < indent {
			return start
		}
		start = ps
		line = prev
	}
	return start
}

// entrySpan returns the bytes an entry occupies, its own comments included.
func (d *doc) entrySpan(op string, e *entry) (int, int, error) {
	start, err := d.entryStart(op, e)
	if err != nil {
		return 0, 0, err
	}
	end, err := d.nodeEnd(op, e.value)
	if err != nil {
		return 0, 0, err
	}
	return start, d.removalEnd(end), nil
}

// itemStart returns where a sequence item's own text begins: the start of its
// line, indentation included, back over the comment lines that sit directly
// above it. The whole line goes rather than just its `- `, or the indentation
// that was in front of the dash would be left behind in front of the next item.
func (d *doc) itemStart(op string, item *yaml.Node) (int, error) {
	off, err := d.offset(item)
	if err != nil {
		return 0, withOp(err, op)
	}
	line := d.lines.lineOf(off)
	lineStart := d.lines.start(line)
	indent := leadingSpaces(d.src[lineStart:d.lines.contentEnd(line)])
	return d.absorbComments(lineStart, indent), nil
}

// itemSpan returns the bytes a sequence item occupies.
func (d *doc) itemSpan(op string, item *yaml.Node) (int, int, error) {
	start, err := d.itemStart(op, item)
	if err != nil {
		return 0, 0, err
	}
	end, err := d.nodeEnd(op, item)
	if err != nil {
		return 0, 0, err
	}
	return start, d.removalEnd(end), nil
}

// itemIndent is the column a block sequence writes its `- ` at, which is where
// an item this package adds to it belongs.
func (d *doc) itemIndent(seq *yaml.Node) (int, error) {
	off, err := d.offset(seq.Content[0])
	if err != nil {
		return 0, err
	}
	line := d.lines.lineOf(off)
	return leadingSpaces(d.src[d.lines.start(line):d.lines.contentEnd(line)]), nil
}

// resolve walks a decoded document along a path without refusing anything: it
// checks the result of an edit, and never decides one.
func resolve(root *yaml.Node, path []string) *yaml.Node {
	n := root
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	for _, key := range path {
		n = mapValue(n, key)
		if n == nil {
			return nil
		}
	}
	return n
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	var found *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			found = m.Content[i+1]
		}
	}
	return found
}

// scalarIs compares the text a scalar was written with. A value written without
// quotes reads back as a number, a boolean or a date, and is still the text the
// caller asked for: the file's own schema decides what each setting means.
func scalarIs(n *yaml.Node, value string) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Value == value
}

func listHas(n *yaml.Node, value string) bool { return countItems(n, value) > 0 }

// countItems returns how many items of a sequence are the scalar value. A
// removal takes exactly one of them, which is what its result is held to.
func countItems(n *yaml.Node, value string) int {
	if n == nil || n.Kind != yaml.SequenceNode {
		return 0
	}
	count := 0
	for _, item := range n.Content {
		if scalarIs(item, value) {
			count++
		}
	}
	return count
}

// keyedItem returns the first mapping item of a sequence whose keyField holds
// keyValue.
func keyedItem(seq *yaml.Node, keyField, keyValue string) *yaml.Node {
	if items := keyedItems(seq, keyField, keyValue); len(items) > 0 {
		return items[0]
	}
	return nil
}

// keyedItems returns the mapping items of a sequence whose keyField holds
// keyValue, in the order they are written. A removal takes the first of them,
// which is what its result is held to.
func keyedItems(seq *yaml.Node, keyField, keyValue string) []*yaml.Node {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var out []*yaml.Node
	for _, item := range seq.Content {
		if v := mapValue(item, keyField); v != nil && v.Value == keyValue {
			out = append(out, item)
		}
	}
	return out
}
