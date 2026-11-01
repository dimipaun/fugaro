// Package yamledit edits a YAML document without rewriting it.
//
// Every settings command writes into a file a person also edits by hand: the
// local project config, the local project-layer file, and a repository's
// fugaro.yaml. Reading such a file into a value and marshalling it back is
// lossy — comments, blank lines, key order, quoting style and anchors all go —
// and those documents decide where a repository's code may go and what a run
// may cost, so a lossy rewrite can turn a refusal into a permission (the F11
// failure, D790). The same edit is also the first thing a person sees in the
// diff of a settings command, and a diff that rewrites the whole file is one
// nobody can review.
//
// So this package parses with yaml.v3 only to find where the bytes of the
// target are, and splices bytes into the original text. Nothing else moves:
// comments, blank lines, ordering, quoting, indentation, line endings, anchors
// and any part of the document the edit does not have to rewrite stay
// byte-identical. Every operation then re-parses its own result and holds it
// against the intended change, and refuses the edit rather than return bytes it
// cannot vouch for.
//
// The operations are generic: a path of mapping keys, and sequence items.
// SetScalar, RemoveKey, AddListItem, RemoveListItem, UpsertSeqItemByKey and
// RemoveSeqItemByKey return new bytes and never write a file. An operation that
// changes nothing returns the input unchanged, byte for byte — Changed tells
// that apart from a write of the same bytes — and a refusal returns the input
// unchanged with an error beside it, never a half-applied edit.
//
// A value is written as it stands unless YAML would read those bytes as
// something else, so the text of a setting and the type its schema gives that
// setting both survive the edit: `5` is written as the number, and a value that
// would read as a comment, a mapping or a sequence is quoted. Where the value
// goes decides what "something else" is — between braces `x, y` is two members
// and `}` ends the mapping — so a value on its way into a flow mapping is read
// back there before it is written without quotes. The quoting style of the
// value already in the document is kept, so a hand-written file does not lose
// the quotes its author chose.
//
// # What it refuses
//
// A refusal is an *Error naming the reason class; it never echoes document
// bytes, so a message can be logged or shown without leaking the values the
// file holds. The classes are:
//
//   - ReasonMultiDocument: the input holds more than one YAML document.
//     Rewriting one of them would change which document a reader sees.
//   - ReasonMergeKey: a key would be created in, or removed from, a mapping
//     that carries `<<`. There the meaning comes partly from another mapping,
//     so what a key resolves to cannot be read from these bytes. Replacing the
//     value of a key that is written explicitly is fine, merge key or not.
//   - ReasonTabIndentation: a line's indentation holds a tab, which leaves the
//     column yaml.v3 reports with no byte offset to map to.
//   - ReasonFlowCollection: the edit would go inside, or would have to measure,
//     a flow collection this package cannot splice into. A flow mapping written
//     on one line can be: the value of one of its members is replaced, a member
//     is added to a mapping that already holds one, and a member is removed
//     with one of the separators around it, every other byte of the line
//     staying as it was written. Everything else about flow is refused rather
//     than re-formatted: a collection that runs across lines or holds a
//     comment, a member whose own value is a collection, and an empty `{}`,
//     whose shape the first member written into it would decide. `settings: {}`
//     is not rewritten into block style, and no edit is written inside a flow
//     sequence. A flow collection an edit only removes along with the line that
//     holds it is fine, as long as it stays on one line. A flow collection
//     where a value would be replaced is this class; a block mapping or
//     sequence there is ReasonReplacesCollection.
//   - ReasonReplacesCollection: the value at the path is a mapping or a
//     sequence, which holds bytes and comments of its own. Replacing it with a
//     scalar would drop all of them, so the edit is refused; removing the key,
//     or writing a key inside it, are the edits that work instead.
//   - ReasonDuplicateKey: a key on the path is written twice, so which of the
//     two the edit changed would not be visible in the result.
//   - ReasonAnchorOrAlias: the node to change carries an anchor, is an alias,
//     or is written with a tag. Replacing it would drop that and leave any
//     `*alias` pointing at nothing.
//   - ReasonNotAMapping, ReasonNotASequence: the path crosses a node of the
//     wrong kind.
//   - ReasonBlockScalar: the value to rewrite is written as `|` or `>`, whose
//     extent the node's line and column do not give. A block scalar elsewhere
//     in the document is left alone.
//   - ReasonMultilineScalar: the value to rewrite runs across several lines,
//     for the same reason.
//   - ReasonEmptyPath, ReasonEmptyField, ReasonFieldIsKeyField: an empty path,
//     an empty key field, and an upsert given the key field among the fields to
//     write, which would rename the item it was told to look up.
//   - ReasonUnlocatable, ReasonMalformed, ReasonVerification: a node that does
//     not map back onto the text, a document yaml.v3 cannot parse, and — the
//     last guard — an edit whose result does not re-parse to the change that was
//     asked for. A `%YAML 1.2` directive is one of the unparsable ones, since
//     yaml.v3 reads that version as an incompatible document, and a `---` with
//     nothing under it is not a mapping: its document holds a null.
package yamledit

import (
	"bytes"
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// Reason is the class of a refusal. A refusal names its class, never the
// document's contents.
type Reason string

const (
	ReasonMultiDocument      Reason = "more than one YAML document"
	ReasonMergeKey           Reason = "a mapping on the path carries a merge key"
	ReasonTabIndentation     Reason = "a tab used for indentation"
	ReasonFlowCollection     Reason = "a flow collection the edit cannot splice into"
	ReasonReplacesCollection Reason = "a mapping or sequence the value would replace"
	ReasonDuplicateKey       Reason = "a key on the path is written twice"
	ReasonAnchorOrAlias      Reason = "an anchor, alias or tag on the node to change"
	ReasonNotAMapping        Reason = "the path crosses a node that is not a mapping"
	ReasonNotASequence       Reason = "the path crosses a node that is not a sequence"
	ReasonBlockScalar        Reason = "a block scalar to rewrite"
	ReasonMultilineScalar    Reason = "a scalar written across several lines"
	ReasonEmptyPath          Reason = "an empty path"
	ReasonEmptyField         Reason = "an empty key field"
	ReasonFieldIsKeyField    Reason = "a field name that is the key field"
	ReasonUnlocatable        Reason = "a node that does not map back onto the text"
	ReasonMalformed          Reason = "a document that is not valid YAML"
	ReasonVerification       Reason = "an edit that did not re-parse to itself"
)

// Error is a refusal: the operation named nothing that could be changed safely.
// It carries the reason class and the path it was asked about, and never any of
// the document's bytes.
type Error struct {
	Op     string
	Path   string
	Reason Reason
}

func (e *Error) Error() string {
	msg := "yamledit: " + e.Op + ": " + string(e.Reason)
	if e.Path != "" {
		msg += " (at " + e.Path + ")"
	}
	return msg
}

// ReasonOf returns the reason class of a refusal, or "" for a nil error or one
// this package did not produce.
func ReasonOf(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

// RemoveOptions are the choices a removal cannot make on its own.
type RemoveOptions struct {
	// DropEmptyParents also removes the key whose mapping or sequence the
	// removal leaves empty, and so on up to the root. Without it, removing the
	// last key of a nested mapping leaves that key written with no value, which
	// is how block style spells an empty mapping. Every key the cascade reaches
	// is held to the rules the named one was: an entry carrying an anchor, an
	// alias or a tag, or sitting in a mapping that carries `<<`, is refused
	// rather than taken along, and the whole edit is refused with it.
	DropEmptyParents bool
}

// Changed reports whether an edit changed the document. Every operation returns
// the input unchanged, byte for byte, when there is nothing to do, so a caller
// that has to tell "nothing to do" from "wrote the same bytes" asks this.
func Changed(before, after []byte) bool { return !bytes.Equal(before, after) }

// SetScalar sets path to a value, creating the mappings on the way that the
// document does not have yet. The value is quoted only when YAML needs it: it is
// written as it stands whenever those bytes read back as exactly this text as
// one scalar, so `5` is written as the number the file's schema expects and only
// a value YAML would read as something else (a comment, a nested mapping, a
// sequence, a trimmed string) is quoted. An existing value keeps its quoting
// style, and an edit that would write the bytes already there changes nothing.
// A value written as a mapping or a sequence is refused rather than replaced:
// those bytes, and the comments among them, are not this operation's to drop.
func SetScalar(src []byte, path []string, value string) ([]byte, error) {
	const op = "SetScalar"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	out, err := d.setScalar(op, path, value)
	if err != nil {
		return src, err
	}
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		return scalarIs(resolve(root, path), value) && d.sameFlowSiblings(path, root)
	}); err != nil {
		return src, err
	}
	return out, nil
}

// RemoveKey removes the key at path and its value. The comment lines directly
// above the key go with it, but only while they sit there with no blank line
// between — a blank line ends what the comments below it belong to — and never a
// block that reaches the file's first line, which is the file's own header
// rather than a note about the key below it.
func RemoveKey(src []byte, path []string) ([]byte, error) {
	return RemoveKeyWithOptions(src, path, RemoveOptions{})
}

// RemoveKeyWithOptions removes the key at path, with the choices
// RemoveOptions names.
func RemoveKeyWithOptions(src []byte, path []string, opts RemoveOptions) ([]byte, error) {
	const op = "RemoveKey"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	out, err := d.removeKey(op, path, opts)
	if err != nil {
		return src, err
	}
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		return resolve(root, path) == nil && d.sameFlowSiblings(path, root)
	}); err != nil {
		return src, err
	}
	return out, nil
}

// AddListItem appends a scalar to the sequence at path, creating the sequence
// when the document does not have it. A value that is already an item of the
// sequence is left alone.
func AddListItem(src []byte, path []string, value string) ([]byte, error) {
	const op = "AddListItem"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	out, err := d.addItem(op, path, value)
	if err != nil {
		return src, err
	}
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		return listHas(resolve(root, path), value)
	}); err != nil {
		return src, err
	}
	return out, nil
}

// RemoveListItem removes the first scalar item equal to value, and only that
// one: a value written twice loses one of its items, not both. A value the
// sequence does not hold is left alone.
func RemoveListItem(src []byte, path []string, value string) ([]byte, error) {
	return RemoveListItemWithOptions(src, path, value, RemoveOptions{})
}

// RemoveListItemWithOptions removes a sequence item, with the choices
// RemoveOptions names.
func RemoveListItemWithOptions(src []byte, path []string, value string, opts RemoveOptions) ([]byte, error) {
	const op = "RemoveListItem"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	out, err := d.removeItem(op, path, value, opts)
	if err != nil {
		return src, err
	}
	before := countItems(resolve(d.root, path), value)
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		after := countItems(resolve(root, path), value)
		return after == before || after == before-1
	}); err != nil {
		return src, err
	}
	return out, nil
}

// UpsertSeqItemByKey sets fields on the item of a sequence of mappings whose
// keyField holds keyValue, and appends that item when the sequence does not
// have it yet. The item's own fields are written in a fixed order, so the same
// edit always writes the same bytes. An upsert given no fields writes nothing
// and returns the document unchanged, but it still reads the document: the path
// is walked, and refused where it cannot be walked, as it is for every other
// edit.
func UpsertSeqItemByKey(src []byte, path []string, keyField, keyValue string, fields map[string]string) ([]byte, error) {
	const op = "UpsertSeqItemByKey"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	if keyField == "" {
		return src, refuse(op, pathString(path), ReasonEmptyField)
	}
	if _, ok := fields[keyField]; ok {
		return src, refuse(op, pathString(path), ReasonFieldIsKeyField)
	}
	if len(fields) == 0 {
		// Nothing to write, but the document and the path are still read: an
		// upsert whose document this package cannot edit, or whose path is not
		// a path of mappings down to a sequence, is refused the way the removal
		// side refuses it, rather than reported as a no-op that succeeded.
		d, err := open(op, src)
		if err != nil {
			return src, err
		}
		chain, found, err := d.walk(op, path)
		if err != nil || !found {
			return src, err
		}
		if _, err := d.sequenceAt(op, path, chain[len(chain)-1]); err != nil {
			return src, err
		}
		return src, nil
	}
	out := src
	for _, field := range sortedKeys(fields) {
		next, err := upsertField(op, out, path, keyField, keyValue, field, fields[field])
		if err != nil {
			return src, err
		}
		out = next
	}
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		item := keyedItem(resolve(root, path), keyField, keyValue)
		if item == nil {
			return false
		}
		for field, want := range fields {
			if !scalarIs(mapValue(item, field), want) {
				return false
			}
		}
		return true
	}); err != nil {
		return src, err
	}
	return out, nil
}

// RemoveSeqItemByKey removes the item of a sequence of mappings whose keyField
// holds keyValue, and only that one: a key value written twice loses one of its
// items, not both. A sequence that does not hold it is left alone.
func RemoveSeqItemByKey(src []byte, path []string, keyField, keyValue string) ([]byte, error) {
	return RemoveSeqItemByKeyWithOptions(src, path, keyField, keyValue, RemoveOptions{})
}

// RemoveSeqItemByKeyWithOptions removes a keyed item, with the choices
// RemoveOptions names.
func RemoveSeqItemByKeyWithOptions(src []byte, path []string, keyField, keyValue string, opts RemoveOptions) ([]byte, error) {
	const op = "RemoveSeqItemByKey"
	if err := checkPath(op, path); err != nil {
		return src, err
	}
	if keyField == "" {
		return src, refuse(op, pathString(path), ReasonEmptyField)
	}
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	out, err := d.removeKeyedItem(op, path, keyField, keyValue, opts)
	if err != nil {
		return src, err
	}
	before := len(keyedItems(resolve(d.root, path), keyField, keyValue))
	if err := verified(op, path, out, func(root *yaml.Node) bool {
		after := len(keyedItems(resolve(root, path), keyField, keyValue))
		return after == before || after == before-1
	}); err != nil {
		return src, err
	}
	return out, nil
}

// setScalar walks path, creating the mappings it does not find, and writes the
// value at the end of it.
func (d *doc) setScalar(op string, path []string, value string) ([]byte, error) {
	if d.root == nil {
		return d.openRoot(chain(path, 0, d.step, scalarLeaf(value))), nil
	}
	cur := d.root
	for i := 0; i < len(path)-1; i++ {
		out, next, done, err := d.descend(op, cur, path, i, scalarLeaf(value))
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
		cur = next
	}
	if err := d.mappingHolding(op, cur, pathString(path)); err != nil {
		return nil, err
	}
	return d.setKey(op, cur, path[len(path)-1], value, pathString(path))
}

// descend goes one mapping deeper along path, or writes out the rest of the
// path when the document stops here.
func (d *doc) descend(op string, cur *yaml.Node, path []string, i int, leaf leafOf) ([]byte, *yaml.Node, bool, error) {
	here := pathString(path[:i+1])
	if err := d.expectMapping(op, cur, here); err != nil {
		return nil, nil, true, err
	}
	e, err := d.find(op, cur, path[i], here)
	if err != nil {
		return nil, nil, true, err
	}
	if e == nil {
		if mergeKey(cur) {
			return nil, nil, true, refuse(op, here, ReasonMergeKey)
		}
		out, err := d.appendBlock(op, cur, chain(path[i:], d.entryIndent(cur), d.step, leaf))
		return out, nil, true, err
	}
	if e.value.Kind == yaml.AliasNode {
		return nil, nil, true, refuse(op, here, ReasonAnchorOrAlias)
	}
	empty, err := d.emptyValue(e.value)
	if err != nil {
		return nil, nil, true, withOp(err, op)
	}
	if empty {
		indent := d.entryIndent(cur) + d.step
		out, err := d.openChild(op, e, chain(path[i+1:], indent, d.step, leaf))
		return out, nil, true, err
	}
	return nil, e.value, false, nil
}

// setKey writes one key of an existing mapping, which is a flow mapping when
// the document wrote it as one.
func (d *doc) setKey(op string, m *yaml.Node, key, value, path string) ([]byte, error) {
	if m.Style&yaml.FlowStyle != 0 {
		return d.setFlowKey(op, m, key, value, path)
	}
	e, err := d.find(op, m, key, path)
	if err != nil {
		return nil, err
	}
	if e == nil {
		if mergeKey(m) {
			return nil, refuse(op, path, ReasonMergeKey)
		}
		line := indentOf(d.entryIndent(m)) + renderKey(key) + ": " + renderValue(value, 0)
		return d.appendBlock(op, m, []string{line})
	}
	return d.setEntryValue(op, e, value, path)
}

// setEntryValue replaces a scalar — or, for a key written with no value, fills
// one in — and refuses an entry whose value is a collection: a mapping or a
// sequence is written with bytes and comments of its own, and splicing a scalar
// over it would drop them.
func (d *doc) setEntryValue(op string, e *entry, value, path string) ([]byte, error) {
	if err := d.anchorFree(op, e, path); err != nil {
		return nil, err
	}
	v := e.value
	empty, err := d.emptyValue(v)
	if err != nil {
		return nil, withOp(err, op)
	}
	if empty {
		off, err := d.offset(v)
		if err != nil {
			return nil, withOp(err, op)
		}
		return splice(d.src, off, off, []byte(" "+renderValue(value, 0))), nil
	}
	if v.Kind == yaml.ScalarNode {
		start, end, err := d.valueSpan(op, v)
		if err != nil {
			return nil, err
		}
		want := []byte(renderValue(value, v.Style))
		if bytes.Equal(d.src[start:end], want) {
			return d.src, nil
		}
		return splice(d.src, start, end, want), nil
	}
	if v.Style&yaml.FlowStyle != 0 {
		return nil, refuse(op, path, ReasonFlowCollection)
	}
	return nil, refuse(op, path, ReasonReplacesCollection)
}

// removeKey removes the entry at path, and with it whatever RemoveOptions asks
// for.
func (d *doc) removeKey(op string, path []string, opts RemoveOptions) ([]byte, error) {
	chain, found, err := d.walk(op, path)
	if err != nil || !found {
		return d.src, err
	}
	target := chain[len(chain)-1]
	if mergeKey(target.parent) {
		return nil, refuse(op, pathString(path), ReasonMergeKey)
	}
	if err := d.anchorFree(op, target.e, pathString(path)); err != nil {
		return nil, err
	}
	var start, end int
	if target.parent.Style&yaml.FlowStyle != 0 {
		start, end, err = d.flowMemberSpan(op, target.parent, target.e)
	} else {
		start, end, err = d.entrySpan(op, target.e)
	}
	if err != nil {
		return nil, err
	}
	if start, end, err = d.dropEmptyMappings(op, pathString(path), chain, start, end, opts); err != nil {
		return nil, err
	}
	return splice(d.src, start, end, nil), nil
}

// dropEmptyMappings extends a removal to the keys whose mappings it leaves
// empty, and on up to the root, when RemoveOptions asks for it. Every key the
// cascade reaches is held to the rules the named one was: an entry carrying an
// anchor, an alias or a tag, or sitting in a mapping that carries `<<`, is
// refused rather than taken along, and the whole edit is refused with it.
func (d *doc) dropEmptyMappings(op, path string, chain []step, start, end int, opts RemoveOptions) (int, int, error) {
	if !opts.DropEmptyParents {
		return start, end, nil
	}
	for i := len(chain) - 1; i > 0 && len(chain[i].parent.Content) == 2; i-- {
		up := chain[i-1]
		if err := d.expandable(op, up, path); err != nil {
			return 0, 0, err
		}
		var err error
		if start, end, err = d.entrySpan(op, up.e); err != nil {
			return 0, 0, err
		}
	}
	return start, end, nil
}

// addItem appends to the sequence at path, writing the sequence when the
// document does not have it.
func (d *doc) addItem(op string, path []string, value string) ([]byte, error) {
	if d.root == nil {
		return d.openRoot(chain(path, 0, d.step, listLeaf(value))), nil
	}
	cur := d.root
	for i := 0; i < len(path)-1; i++ {
		out, next, done, err := d.descend(op, cur, path, i, listLeaf(value))
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
		cur = next
	}
	last := path[len(path)-1]
	here := pathString(path)
	if err := d.expectMapping(op, cur, here); err != nil {
		return nil, err
	}
	e, err := d.find(op, cur, last, here)
	if err != nil {
		return nil, err
	}
	if e == nil {
		if mergeKey(cur) {
			return nil, refuse(op, here, ReasonMergeKey)
		}
		indent := d.entryIndent(cur)
		return d.appendBlock(op, cur, []string{
			indentOf(indent) + renderKey(last) + ":",
			indentOf(indent+d.step) + "- " + renderValue(value, 0),
		})
	}
	if err := d.anchorFree(op, e, here); err != nil {
		return nil, err
	}
	if empty, err := d.emptyValue(e.value); err != nil {
		return nil, withOp(err, op)
	} else if empty {
		indent := d.entryIndent(cur) + d.step
		return d.openChild(op, e, []string{indentOf(indent) + "- " + renderValue(value, 0)})
	}
	seq := e.value
	if seq.Style&yaml.FlowStyle != 0 {
		return nil, refuse(op, here, ReasonFlowCollection)
	}
	if seq.Kind != yaml.SequenceNode {
		return nil, refuse(op, here, ReasonNotASequence)
	}
	if listHas(seq, value) {
		return d.src, nil
	}
	indent, err := d.itemIndent(seq)
	if err != nil {
		return nil, err
	}
	off, err := d.blockEnd(op, seq)
	if err != nil {
		return nil, err
	}
	return splice(d.src, off, off, []byte(d.eol+indentOf(indent)+"- "+renderValue(value, 0))), nil
}

// removeItem removes one item of the sequence at path.
func (d *doc) removeItem(op string, path []string, value string, opts RemoveOptions) ([]byte, error) {
	chain, found, err := d.walk(op, path)
	if err != nil || !found {
		return d.src, err
	}
	target := chain[len(chain)-1]
	if err := d.anchorFree(op, target.e, pathString(path)); err != nil {
		return nil, err
	}
	seq, err := d.sequenceAt(op, path, target)
	if err != nil || seq == nil {
		return d.src, err
	}
	index := -1
	for i, item := range seq.Content {
		if item.Kind == yaml.ScalarNode && item.Value == value {
			index = i
			break
		}
	}
	if index < 0 {
		return d.src, nil
	}
	start, end, err := d.itemSpan(op, seq.Content[index])
	if err != nil {
		return nil, err
	}
	if start, end, err = d.dropEmptySequence(op, pathString(path), chain, seq, start, end, opts); err != nil {
		return nil, err
	}
	return splice(d.src, start, end, nil), nil
}

// removeKeyedItem removes the item of a sequence of mappings that keyField
// identifies.
func (d *doc) removeKeyedItem(op string, path []string, keyField, keyValue string, opts RemoveOptions) ([]byte, error) {
	chain, found, err := d.walk(op, path)
	if err != nil || !found {
		return d.src, err
	}
	target := chain[len(chain)-1]
	if err := d.anchorFree(op, target.e, pathString(path)); err != nil {
		return nil, err
	}
	seq, err := d.sequenceAt(op, path, target)
	if err != nil || seq == nil {
		return d.src, err
	}
	item := keyedItem(seq, keyField, keyValue)
	if item == nil {
		return d.src, nil
	}
	start, end, err := d.itemSpan(op, item)
	if err != nil {
		return nil, err
	}
	if start, end, err = d.dropEmptySequence(op, pathString(path), chain, seq, start, end, opts); err != nil {
		return nil, err
	}
	return splice(d.src, start, end, nil), nil
}

// sequenceAt returns the sequence an entry holds. A key written with no value
// holds no sequence, which is not an error: there is simply nothing to remove.
func (d *doc) sequenceAt(op string, path []string, target step) (*yaml.Node, error) {
	v := target.e.value
	if empty, err := d.emptyValue(v); err != nil {
		return nil, withOp(err, op)
	} else if empty {
		return nil, nil
	}
	if v.Style&yaml.FlowStyle != 0 {
		return nil, refuse(op, pathString(path), ReasonFlowCollection)
	}
	if v.Kind != yaml.SequenceNode {
		return nil, refuse(op, pathString(path), ReasonNotASequence)
	}
	return v, nil
}

// dropEmptySequence extends a removal to the key that held the sequence when
// the item removed was its last, and on up while a mapping is left empty.
func (d *doc) dropEmptySequence(op, path string, chain []step, seq *yaml.Node, start, end int, opts RemoveOptions) (int, int, error) {
	if !opts.DropEmptyParents || len(seq.Content) != 1 {
		return start, end, nil
	}
	last := len(chain) - 1
	if err := d.expandable(op, chain[last], path); err != nil {
		return 0, 0, err
	}
	start, end, err := d.entrySpan(op, chain[last].e)
	if err != nil {
		return 0, 0, err
	}
	for i := last; i > 0 && len(chain[i].parent.Content) == 2; i-- {
		up := chain[i-1]
		if err := d.expandable(op, up, path); err != nil {
			return 0, 0, err
		}
		if start, end, err = d.entrySpan(op, up.e); err != nil {
			return 0, 0, err
		}
	}
	return start, end, nil
}

// upsertField applies one field of an upsert: the item is written whole when
// the sequence does not have it, and otherwise the field is set on the item
// that is already there.
func upsertField(op string, src []byte, path []string, keyField, keyValue, field, value string) ([]byte, error) {
	d, err := open(op, src)
	if err != nil {
		return src, err
	}
	if d.root == nil {
		fields := map[string]string{field: value}
		return d.openRoot(chain(path, 0, d.step, itemLeaf(keyField, keyValue, fields))), nil
	}
	cur := d.root
	leaf := itemLeaf(keyField, keyValue, map[string]string{field: value})
	for i := 0; i < len(path)-1; i++ {
		out, next, done, err := d.descend(op, cur, path, i, leaf)
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
		cur = next
	}
	last := path[len(path)-1]
	here := pathString(path)
	if err := d.expectMapping(op, cur, here); err != nil {
		return nil, err
	}
	e, err := d.find(op, cur, last, here)
	if err != nil {
		return nil, err
	}
	if e == nil {
		if mergeKey(cur) {
			return nil, refuse(op, here, ReasonMergeKey)
		}
		indent := d.entryIndent(cur)
		lines := []string{indentOf(indent) + renderKey(last) + ":"}
		return d.appendBlock(op, cur, append(lines,
			indentOf(indent+d.step)+"- "+renderKey(keyField)+": "+renderValue(keyValue, 0),
			indentOf(indent+d.step+2)+renderKey(field)+": "+renderValue(value, 0)))
	}
	if err := d.anchorFree(op, e, here); err != nil {
		return nil, err
	}
	if empty, err := d.emptyValue(e.value); err != nil {
		return nil, withOp(err, op)
	} else if empty {
		indent := d.entryIndent(cur) + d.step
		return d.openChild(op, e, []string{
			indentOf(indent) + "- " + renderKey(keyField) + ": " + renderValue(keyValue, 0),
			indentOf(indent+2) + renderKey(field) + ": " + renderValue(value, 0),
		})
	}
	seq := e.value
	if seq.Style&yaml.FlowStyle != 0 {
		return nil, refuse(op, here, ReasonFlowCollection)
	}
	if seq.Kind != yaml.SequenceNode {
		return nil, refuse(op, here, ReasonNotASequence)
	}
	item := keyedItem(seq, keyField, keyValue)
	if item == nil {
		indent, err := d.itemIndent(seq)
		if err != nil {
			return nil, err
		}
		off, err := d.blockEnd(op, seq)
		if err != nil {
			return nil, err
		}
		return splice(d.src, off, off, []byte(d.eol+
			indentOf(indent)+"- "+renderKey(keyField)+": "+renderValue(keyValue, 0)+d.eol+
			indentOf(indent+2)+renderKey(field)+": "+renderValue(value, 0))), nil
	}
	return d.setKey(op, item, field, value, pathString(append(append([]string{}, path...), field)))
}

// walk finds the entry for the last element of path, without creating anything.
// It returns the chain of entries from the root, which a removal needs to know
// what it leaves behind, and false when the path is not written in full.
func (d *doc) walk(op string, path []string) ([]step, bool, error) {
	if d.root == nil {
		return nil, false, nil
	}
	var chain []step
	cur := d.root
	for i, key := range path {
		here := pathString(path[:i+1])
		check := d.expectMapping
		if i == len(path)-1 {
			// The mapping an edit names its key in may be a flow mapping: the
			// splice goes inside it, not through it.
			check = d.mappingHolding
		}
		if err := check(op, cur, here); err != nil {
			return nil, false, err
		}
		e, err := d.find(op, cur, key, here)
		if err != nil {
			return nil, false, err
		}
		if e == nil {
			return nil, false, nil
		}
		chain = append(chain, step{parent: cur, e: e})
		if i == len(path)-1 {
			return chain, true, nil
		}
		if e.value.Kind == yaml.AliasNode {
			return nil, false, refuse(op, here, ReasonAnchorOrAlias)
		}
		empty, err := d.emptyValue(e.value)
		if err != nil {
			return nil, false, withOp(err, op)
		}
		if empty {
			return nil, false, nil
		}
		cur = e.value
	}
	return nil, false, nil
}

// step is one element of a path that is written in the document.
type step struct {
	parent *yaml.Node
	e      *entry
}

// appendBlock opens a new line after a block mapping's or sequence's last entry
// and writes the block there, in the document's own line endings.
func (d *doc) appendBlock(op string, m *yaml.Node, lines []string) ([]byte, error) {
	off, err := d.blockEnd(op, m)
	if err != nil {
		return nil, err
	}
	return splice(d.src, off, off, []byte(d.eol+strings.Join(lines, d.eol))), nil
}

// openChild opens a block under a key that is written with no value, on the
// line below it.
func (d *doc) openChild(op string, e *entry, lines []string) ([]byte, error) {
	off, err := d.offset(e.key)
	if err != nil {
		return nil, withOp(err, op)
	}
	off = d.lines.contentEnd(d.lines.lineOf(off))
	return splice(d.src, off, off, []byte(d.eol+strings.Join(lines, d.eol))), nil
}

// openRoot writes a whole document where there is none yet: an empty file, or
// one that holds nothing but comments, which are kept above the new block. The
// new text goes onto a copy of the document, as every other edit's does: an
// append to the caller's own slice would reach into the array behind it, past
// the length the caller lent this package, and hand back bytes that change
// under a caller that goes on writing into its buffer.
func (d *doc) openRoot(lines []string) []byte {
	text := strings.Join(lines, d.eol) + d.eol
	out := append([]byte{}, d.src...)
	if len(out) > 0 && !d.lines.endsWithBreak() {
		out = append(out, []byte(d.eol)...)
	}
	return append(out, []byte(text)...)
}

// verified re-parses an edit's result and refuses it when it does not carry the
// change that was asked for. Nothing leaves this package that has not been read
// back.
func verified(op string, path []string, out []byte, want func(root *yaml.Node) bool) error {
	var n yaml.Node
	if err := yaml.Unmarshal(out, &n); err != nil {
		return refuse(op, pathString(path), ReasonVerification)
	}
	if !want(&n) {
		return refuse(op, pathString(path), ReasonVerification)
	}
	return nil
}

func checkPath(op string, path []string) error {
	if len(path) == 0 {
		return refuse(op, "", ReasonEmptyPath)
	}
	return nil
}

func refuse(op, path string, reason Reason) error {
	return &Error{Op: op, Path: path, Reason: reason}
}

func withOp(err error, op string) error {
	var e *Error
	if errors.As(err, &e) && e.Op == "" {
		e.Op = op
	}
	return err
}

func pathString(path []string) string { return strings.Join(path, ".") }
