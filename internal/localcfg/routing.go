package localcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/routing"
)

// modelRoutingKey is the top-level key the table is written under. It is the
// same word the published policy's own key is spelled with (contract-table.md),
// and the two are one fact for a reader: an installation whose published policy
// carries this key is an installation whose runs are served by the table
// (contract-pools.md).
const modelRoutingKey = "model_routing"

// validateModelRouting refuses the table's own decode rules: its entries name
// one of the five kinds, and each source carries the members its kind uses —
// `pool` on a pool source, `provider` on a provider source, and `admit` on the
// kinds that are one credential of the project. Everything else about a table
// is routing.Problems' answer (RT3) and not this loader's: which models a
// project's crafts call and no entry matches, a source that cannot reach a
// model, a chain that is only the launcher's pool, and the rest are checks over
// a table a run may still be served by, and they are reported by `fugaro
// validate` and refused by `fugaro routing set` with the fix — not a refusal of
// a file every member of the project parses.
//
// The member rules are the contract's "a member a kind cannot use is RT2's
// decoder to refuse rather than something stored and never read"
// (contract-table.md, internal/routing's Source): a `provider` on a pool source
// and an `admit` list on a pool are exactly that, because nothing reads them —
// a pool's admission is its members' own `allow_data_to` (D3182, R20). The
// other direction is the same rule read once more: a source of a kind that
// names a credential and does not name it routes to nothing at all.
func (c *Config) validateModelRouting(bad func(string, ...any)) {
	kinds := make([]string, 0, len(routing.Kinds))
	for _, k := range routing.Kinds {
		kinds = append(kinds, string(k))
	}
	for i, entry := range c.ModelRouting {
		where := fmt.Sprintf("model_routing[%d]", i)
		if entry.Match == "" {
			bad("%s: an entry with no match answers no model: write the model or the pattern it routes", where)
		} else {
			where = fmt.Sprintf("%s (%s)", where, entry.Match)
		}
		for j, s := range entry.Chain {
			at := fmt.Sprintf("%s.chain[%d]", where, j)
			if !routing.ValidKind(s.Kind) {
				bad("%s: kind %q must be one of %s", at, s.Kind, strings.Join(kinds, ", "))
				continue
			}
			if s.Pool != "" && s.Kind != routing.KindPool {
				bad("%s: kind %s names no pool, so pool: %s is read by nothing: name the pool on a source of kind pool", at, s.Kind, s.Pool)
			}
			if s.Provider != "" && s.Kind != routing.KindProvider {
				bad("%s: kind %s names no provider, so provider: %s is read by nothing: name the provider on a source of kind provider", at, s.Kind, s.Provider)
			}
			if s.Kind == routing.KindPool && s.Pool == "" {
				bad("%s: a source of kind pool with no pool names no credential: write the pool the run's calls go out on", at)
			}
			if s.Kind == routing.KindProvider && s.Provider == "" {
				bad("%s: a source of kind provider with no provider names no credential: write the provider the run's calls go out on", at)
			}
			// A pool's admission is its members' own (D3182, R20), so an admit
			// list here is the widening this refusal exists to stop: it looks
			// like an admission and no walk would ever read it.
			if len(s.Admit) > 0 && (s.Kind == routing.KindLauncherPool || s.Kind == routing.KindPool) {
				bad("%s: kind %s carries no admit list: a pool is admitted by its members' own allow_data_to, so this list would be read by nothing (name the repositories on the account, or on a source of kind anthropic, provider or vertex)", at, s.Kind)
			}
		}
	}
}

// SetModelRouting returns doc with its top-level `model_routing:` block
// replaced by table, and every other byte of the document as it was written:
// the comments, the blank lines, the order of the keys, the indentation and the
// quoting of everything the edit does not have to rewrite. An empty table
// removes the block, since a table with no entry routes nothing and the absent
// key says the same thing without a line to read. Table is never stored as a
// value the write decided on: a caller that passes what the file already says
// gets its own bytes back, unchanged.
//
// It is a byte splice and not a marshal of the decoded config for the reason
// yamledit exists: the local config is a file a person also edits by hand, and
// reading it into a value and marshalling it back drops every comment, blank
// line and quoting choice in it — a loss a `fugaro routing set` must not make
// (D790, internal/yamledit). The block is the unit this writer owns, so an
// entry's own comment goes with that entry when the entry itself changes, and
// nothing outside the block's lines moves at all.
//
// It refuses rather than return bytes it cannot vouch for: a document that is
// not one YAML mapping, a block written twice, a value it cannot measure to its
// last line (an alias, an anchor or a tag a rewrite would drop, a flow mapping
// the block is written inside), and — the last guard — a result that does not
// read back as the table that was asked for with the document's own keys where
// they were. Every refusal returns the input unchanged with an error beside it,
// never a half-applied edit.
func SetModelRouting(doc []byte, table routing.Table) ([]byte, error) {
	root, err := documentRoot(doc)
	if err != nil {
		return doc, err
	}
	if root == nil {
		// A file that holds no mapping at all — an empty one, or one that is
		// only comments and blank lines — has no other key to keep and no line
		// to measure: the block goes after it, and the comments stay where
		// their author wrote them.
		block, err := renderModelRouting(table, defaultIndent)
		if err != nil {
			return doc, err
		}
		if len(block) == 0 {
			return doc, nil
		}
		return appendBlock(doc, block), nil
	}
	if root.Kind != yaml.MappingNode || len(root.Content) == 0 {
		return doc, fmt.Errorf("%s: the local config is one YAML mapping, and this file is not one: write the installation's keys into it (or `fugaro setup`) and set the table again", modelRoutingKey)
	}
	before := mappingKeys(root)
	key, value, found, err := modelRoutingIn(root)
	if err != nil {
		return doc, err
	}
	indent := indentUnit(root)
	if found {
		// An anchor or a tag inside the block's own bytes goes with the bytes a
		// write replaces, and an `*alias` elsewhere in the file would be left
		// pointing at nothing: the file says something a splice cannot carry, so
		// nothing is written (yamledit's own reason, one block over).
		for _, n := range []*yaml.Node{key, value} {
			if a := anchorIn(n); a != "" {
				return doc, fmt.Errorf("%s: the block carries %s, and an `*alias` elsewhere in this file may point at it: rewriting the block would leave that alias pointing at nothing, so no edit is written", modelRoutingKey, a)
			}
		}
		var current routing.Table
		if err := value.Decode(&current); err != nil {
			// A block the writer cannot read is one it cannot hold its own
			// result against, and rewriting it would be a write nobody
			// checked: the parse of the file reports the value instead.
			return doc, fmt.Errorf("%s: %v: fix the block and write it again", modelRoutingKey, err)
		}
		if sameTable(current, table) {
			return doc, nil
		}
	}
	block, err := renderModelRouting(table, indent)
	if err != nil {
		return doc, err
	}
	out := doc
	if found {
		if out, err = replaceBlock(doc, key, value, block); err != nil {
			return doc, err
		}
	} else if len(block) > 0 {
		out = appendBlock(doc, block)
	}
	if err := verifyModelRouting(out, table, before); err != nil {
		return doc, err
	}
	return out, nil
}

// defaultIndent is the indentation the block is written with in a document that
// states none of its own: yaml.Marshal's, which is what every config
// `fugaro setup` and `fugaro update` wrote is laid out with.
const defaultIndent = 4

// documentRoot is the document's root mapping node, nil for a document that
// holds nothing at all. More than one document is refused: a block written into
// one of them would change which document a reader sees.
func documentRoot(doc []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(doc))
	var root yaml.Node
	err := dec.Decode(&root)
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: this file does not read as YAML: %v", modelRoutingKey, err)
	}
	var second yaml.Node
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: this file holds more than one YAML document, and writing the block into one of them would change which document a reader sees", modelRoutingKey)
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, nil
		}
		return root.Content[0], nil
	}
	return &root, nil
}

// mappingKeys are a mapping node's top-level keys, sorted, for the check that a
// splice left every one of them where it was. It reads the node rather than
// decoding the bytes again: the document is already parsed here, and a second
// read of the same bytes is a second answer that could disagree with the first.
func mappingKeys(mapping *yaml.Node) []string {
	keys := make([]string, 0, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keys = append(keys, mapping.Content[i].Value)
	}
	sort.Strings(keys)
	return keys
}

// anchorIn is the first anchor or explicit tag written in a node's own bytes,
// spelling itself ("&t", "!foo"), or "" when there is none.
func anchorIn(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	if n.Anchor != "" {
		return "&" + n.Anchor
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return n.Tag
	}
	for _, c := range n.Content {
		if a := anchorIn(c); a != "" {
			return a
		}
	}
	return ""
}

// modelRoutingIn is the document's `model_routing` key and value nodes. found is
// false when the mapping does not hold the key. A key written twice is refused:
// which of the two an edit landed on would not be visible in the result.
func modelRoutingIn(mapping *yaml.Node) (key, value *yaml.Node, found bool, err error) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != modelRoutingKey {
			continue
		}
		if found {
			return nil, nil, false, fmt.Errorf("%s is written twice in this file: which of the two a write changed would not be visible in the result, so name one", modelRoutingKey)
		}
		key, value, found = mapping.Content[i], mapping.Content[i+1], true
	}
	return key, value, found, nil
}

// indentUnit is the step the document indents with, measured from its first
// nested block: a document a person keeps by hand may be laid out at two where
// Fugaro writes four, and a block written at the wrong step is a diff nobody
// asked for.
func indentUnit(root *yaml.Node) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if v.Line > k.Line && v.Column > k.Column {
			if step := v.Column - k.Column; step >= 1 && step <= 8 {
				return step
			}
		}
	}
	return defaultIndent
}

// renderModelRouting is the block's bytes: the key, and the table under it at
// step, or nothing at all for a table with no entry (an absent key and a key
// that routes nothing are one statement, and the absent one is the shorter).
//
// The table's own yaml spelling is the encoder's, which is the contract's: its
// types carry the yaml tags that name the same members as the json ones
// (internal/routing), so a source writes the members its kind uses and no
// others.
func renderModelRouting(table routing.Table, step int) ([]byte, error) {
	if len(table) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(step)
	if err := enc.Encode(table); err != nil {
		return nil, fmt.Errorf("%s: %v", modelRoutingKey, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("%s: %v", modelRoutingKey, err)
	}
	pad := strings.Repeat(" ", step)
	var out strings.Builder
	out.WriteString(modelRoutingKey + ":\n")
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		out.WriteString(pad)
		out.WriteString(line)
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

// replaceBlock is doc with the lines the block's key and value occupy — and
// only those — replaced by block. An empty block removes them, the line's own
// indentation going with them.
func replaceBlock(doc []byte, key, value *yaml.Node, block []byte) ([]byte, error) {
	starts := lineStarts(doc)
	from, to := key.Line-1, lastLine(value)-1
	if from < 0 || from >= len(starts) || to < from || to >= len(starts) {
		return doc, fmt.Errorf("%s: the block's own lines cannot be located in this file (the document's line numbers do not reach it), so no edit is written", modelRoutingKey)
	}
	if prefix := doc[starts[from]:offsetOfLine(doc, starts, from, key)]; len(bytes.TrimLeft(prefix, " \t")) != 0 {
		return doc, fmt.Errorf("%s: the key does not begin its own line, and a block cannot be written there without rewriting the mapping around it", modelRoutingKey)
	}
	// A line yaml.v3 gives no line number takes the block's own extent with it:
	// the value's last line is measured from the lines its nodes state, and a
	// line the reader cannot place — a folded scalar's continuation, which the
	// node states as its first line alone — would be left behind by a splice
	// that stops short of it. Comment and blank lines are skipped: they are
	// nobody's bytes but their author's.
	for i := to + 1; i < len(starts); i++ {
		next := lineText(doc, starts, i)
		if strings.TrimSpace(next) == "" || strings.HasPrefix(strings.TrimSpace(next), "#") {
			continue
		}
		if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
			return doc, fmt.Errorf("%s: the lines after the block are indented, so this file writes more than the reader can measure under the key: no edit is written", modelRoutingKey)
		}
		break
	}
	var out []byte
	out = append(out, doc[:starts[from]]...)
	if len(block) > 0 {
		out = append(out, doc[starts[from]:offsetOfLine(doc, starts, from, key)]...)
		out = append(out, block...)
	}
	rest := len(doc)
	if to+1 < len(starts) {
		rest = starts[to+1]
	}
	out = append(out, doc[rest:]...)
	return out, nil
}

// appendBlock is doc with the block written after its last line. A file whose
// last line has no newline gains one: the block is a line of its own, and
// running the two together would rewrite both.
func appendBlock(doc []byte, block []byte) []byte {
	out := doc
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(append([]byte{}, out...), '\n')
	}
	out = append(append([]byte{}, out...), block...)
	return out
}

// lineStarts are the byte offsets each line of doc begins at, 0-based by line.
func lineStarts(doc []byte) []int {
	starts := []int{0}
	for i, b := range doc {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetOfLine is the byte offset a node at line and column begins at: yaml.v3
// counts lines from 1 and columns in characters, and a column that reaches past
// the line it is on is the line's end.
func offsetOfLine(doc []byte, starts []int, line int, node *yaml.Node) int {
	start := starts[line]
	end := len(doc)
	if line+1 < len(starts) {
		end = starts[line+1] - 1
	}
	off := start + node.Column - 1
	if off > end {
		return end
	}
	return off
}

// lineText is one line of doc without its newline.
func lineText(doc []byte, starts []int, line int) string {
	start := starts[line]
	end := len(doc)
	if line+1 < len(starts) {
		end = starts[line+1] - 1
	}
	return string(doc[start:end])
}

// lastLine is the last line a node's own bytes reach: a scalar's text may run
// across lines, and a collection reaches as far as its last member does.
func lastLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	last := n.Line + strings.Count(n.Value, "\n")
	for _, c := range n.Content {
		if l := lastLine(c); l > last {
			last = l
		}
	}
	return last
}

// sameTable reports whether two tables say the same thing, by the document's
// own spelling: each one is written and read back, so a difference the document
// cannot carry — an empty admit list against an absent one, which both mean
// "admits nobody" — is not a difference between two tables, and a write that
// re-rendered only that is a diff nobody asked for.
func sameTable(a, b routing.Table) bool {
	ca, err := canonicalTable(a)
	if err != nil {
		return false
	}
	cb, err := canonicalTable(b)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(ca, cb)
}

// canonicalTable is the table as its own document writes it and reads it back.
func canonicalTable(t routing.Table) (routing.Table, error) {
	block, err := renderModelRouting(t, defaultIndent)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Table routing.Table `yaml:"model_routing"`
	}
	if len(block) == 0 {
		return doc.Table, nil
	}
	if err := yaml.Unmarshal(block, &doc); err != nil {
		return nil, err
	}
	return doc.Table, nil
}

// verifyModelRouting is the writer's last guard, and the reason a caller may
// trust the bytes: the result reads back as the table that was asked for, and
// the document's own keys are the ones it had, with `model_routing` gained or
// gone as the write said. A splice that swallowed the key below the block, or
// wrote the block where a reader does not see it, is refused here rather than
// returned.
func verifyModelRouting(out []byte, table routing.Table, before []string) error {
	after := []string{}
	if root, err := documentRoot(out); err != nil {
		return err
	} else if root != nil {
		if root.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: the write would leave this file something other than one YAML mapping, so no edit is written", modelRoutingKey)
		}
		after = mappingKeys(root)
	}
	want := make([]string, 0, len(before)+1)
	for _, k := range before {
		if k != modelRoutingKey {
			want = append(want, k)
		}
	}
	if len(table) > 0 {
		want = append(want, modelRoutingKey)
		sort.Strings(want)
	}
	if !reflect.DeepEqual(after, want) {
		return fmt.Errorf("%s: the write would leave this file with the keys %v, and it holds %v: no edit is written", modelRoutingKey, after, want)
	}
	if len(table) == 0 {
		return nil
	}
	root, err := documentRoot(out)
	if err != nil {
		return err
	}
	_, value, found, err := modelRoutingIn(root)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: the write does not read back: this file does not hold the block it was asked to write", modelRoutingKey)
	}
	var doc routing.Table
	if err := value.Decode(&doc); err != nil {
		return fmt.Errorf("%s: the write does not read back: %v", modelRoutingKey, err)
	}
	if !sameTable(doc, table) {
		return fmt.Errorf("%s: the write does not read back as the table it was asked to hold", modelRoutingKey)
	}
	return nil
}
