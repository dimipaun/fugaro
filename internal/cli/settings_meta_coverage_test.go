package cli

// settings_meta_coverage_test.go is SMC of docs/console-improvements/implementation-plan.md:
// the join of SM-E and the four backfills. SM1 to SM4 each cover their own
// family, and a key that falls between the families — named in
// settingsGenericKeys, reached by a prefix, claimed by none of the four — is a
// key every one of those tests passes over. This file is the one question asked
// of the whole table rather than of a family (D2908): every key the generic
// write path lands a write on carries the four facts the console draws a row
// from — a label, a description, a group and a type.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsMetaCoverageSpellings are the shapes the writable set has to reach,
// and the guard against the predicate narrowing to nothing: a scalar reached by
// a prefix, a named enum, a wildcard block row (whose concrete spelling
// `workflows.web.commands.test` is the key a person writes), the two block
// spellings only the entry pair writes (`--add-entry`), and one of §4.4's
// standalone names that no prefix reaches. A predicate that stopped reaching
// one of these would leave the completeness below passing over a set smaller
// than the keys a write really lands on.
var settingsMetaCoverageSpellings = []string{
	"agent.review_rounds",
	"budget.mode",
	"workflows.*.commands.test",
	"layer.ab",
	"mobile_allow",
	"review_against_task",
}

// settingsMetaWildcardName is the name a registry spelling's `*` is filled with
// to ask whether a write lands on the row at all. What the segment is called
// decides nothing the write path answers: which rows this path writes is asked
// of the pattern (settingsWriteLookup), and the vocabulary a name must be of is
// checked by the write's own validation after that — so any name the registry
// accepts as a segment answers the same, and `x` is one no family owns.
const settingsMetaWildcardName = "x"

// settingsMetaWritableKeys is every key the generic path allows and writes, in
// key order: the spellings settingsGenericKeys names — spec §4.4's list and the
// rows decisions put beside it — together with every registry row the prefixes
// of settingsGenericPrefixes reach.
//
// The allowlist alone is not the set — settingsGenericKey answers for a block
// whole, and `governance.tiers` is a row the read prints while the write
// refuses it — so each key is asked of the write path itself, which is the
// predicate the read's own `writable` column is (D1102, settings_read.go's
// projectRow) asked of both spellings the path has: the scalar write
// (settings set, settingsWriteRow) and the entry pair that edits one entry of
// a block (--add-entry, settingsEntryRow). `layer.ab` and `mobile_allow` are
// keys no scalar write lands on and the entry pair does, and a table that
// covered the scalar half alone would leave them unlabelled while the console
// draws the command to copy (R5).
func settingsMetaWritableKeys(t *testing.T) []string {
	t.Helper()
	set := map[string]bool{}
	add := func(key string) {
		if !settingsGenericKey(key) || !settingsMetaWriteLands(key) {
			return
		}
		set[key] = true
	}
	for key := range settingsGenericKeys {
		add(key)
	}
	for _, row := range settings.Rows() {
		add(row.Key)
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		t.Fatal("no key is both allowlisted and writable: both predicates have moved, and a completeness test over an empty set proves nothing")
	}
	for _, key := range settingsMetaCoverageSpellings {
		if !set[key] {
			t.Errorf("%s is not in the writable set, and a write lands on it: the predicate above has narrowed away a shape this test exists to cover", key)
		}
	}
	return keys
}

// settingsMetaWriteLands reports whether a write lands on key as a person
// spells it: the scalar write or the entry pair for the key itself, and — for a
// registry spelling carrying a `*`, which nobody types — the same question
// asked of a concrete spelling of the pattern, because that is the key a person
// writes (`workflows.web.commands.test` for `workflows.*.commands.test`).
func settingsMetaWriteLands(key string) bool {
	if settingsMetaWriteAccepts(key) {
		return true
	}
	if !strings.Contains(key, "*") {
		return false
	}
	return settingsMetaWriteAccepts(strings.ReplaceAll(key, "*", settingsMetaWildcardName))
}

// settingsMetaWriteAccepts reports whether one spelling is one the write path
// takes: the scalar write, or the entry pair's row for a block.
func settingsMetaWriteAccepts(key string) bool {
	if _, err := settingsWriteRow(key); err == nil {
		return true
	}
	_, err := settingsEntryRow(key)
	return err == nil
}

// TestSettingsMetaCoversEveryWritableKey is the plan's acceptance for SMC: a
// key the generic write path lands on and the metadata table has no entry for
// is a failure, named. What the test asks of an entry is the four facts the
// console draws a row from and what Register cannot ask — that the entry says
// something a person can read: a label of one to six words, a description of
// one sentence, a group of the group order and a type of the type vocabulary.
// A key with no entry at all is drawn by its raw key under the console's own
// "Other" group (D2908), which is the fallback this test backfills away for
// every key a person may write.
//
// The other direction is deliberately not asked: a key the write refuses may
// still carry metadata, and several do — `agent.model` is a registry row the
// models family owns and the generic path refuses, and a label beside it is
// what the console's read prints for a row a person can see and not edit.
func TestSettingsMetaCoversEveryWritableKey(t *testing.T) {
	checked, missing := 0, 0
	for _, key := range settingsMetaWritableKeys(t) {
		m, ok := settings.MetaOf(key)
		if !ok {
			missing++
			t.Errorf("%s is a key the generic write path lands on and it carries no metadata: the console draws it by its raw key under its own Other group, and D2908 backfills every writable key", key)
			continue
		}
		checked++
		if n := len(strings.Fields(m.Label)); n == 0 || n > settingsMetaLabelWords {
			t.Errorf("%s: the label %q is %d words, and a label is one to %d", key, m.Label, n, settingsMetaLabelWords)
		}
		if !settingsMetaOneSentence(m.Description) {
			t.Errorf("%s: the description %q is not one sentence ending in a full stop", key, m.Description)
		}
		if !containsMetaGroup(m.Group) {
			t.Errorf("%s: the group %q is not one of the group order", key, m.Group)
		}
		if !containsMetaType(m.Type) {
			t.Errorf("%s: the type %q is not one of the type vocabulary", key, m.Type)
		}
	}
	if checked == 0 {
		t.Fatal("no writable key was read, and the walk is what this test is")
	}
	if missing > 0 {
		t.Errorf("%d of the %d keys the generic path writes carry no metadata", missing, checked+missing)
	}
}
