package settings

import (
	"slices"
	"strings"
	"testing"
)

// register is Register with the entry dropped again when the test ends: the
// table is package state, so a key a test registered would otherwise be seen by
// another test of this package.
//
// The keys here and below are this file's own `probe.` names rather than
// registry keys: the families register their real keys in init (meta_git.go is
// the first), and a test that registered `agent.review_rounds`, or asked
// MetaOf("git.pr.early_draft") for a key it never registered, would be reading
// a family's entry rather than testing Register's own rules.
func register(t *testing.T, key string, m Meta) {
	t.Helper()
	if err := Register(key, m); err != nil {
		t.Fatalf("Register(%q, ...) refused it: %v", key, err)
	}
	t.Cleanup(func() { unregisterMeta(key) })
}

// goodMeta is the smallest metadata Register accepts: a label, a one-sentence
// description, a group of the order and a type. Every case below changes one
// field of it.
func goodMeta() Meta {
	return Meta{
		Label:       "Review rounds",
		Description: "How many review rounds a run spends before it finalizes.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}
}

func TestMetaRegisterRefusesDuplicate(t *testing.T) {
	const key = "probe.review_rounds"
	register(t, key, goodMeta())
	if err := Register(key, goodMeta()); err == nil {
		t.Fatalf("Register(%q, ...) a second time registered the key again", key)
	}
}

func TestMetaLookupMissingKey(t *testing.T) {
	register(t, "probe.review_rounds", goodMeta())
	if m, ok := MetaOf("probe.review_round"); ok {
		t.Fatalf("MetaOf answered %+v for a key that was never registered", m)
	}
	if m, ok := MetaOf(""); ok {
		t.Fatalf("MetaOf answered %+v for the empty key", m)
	}
}

// TestMetaRegisterAndLookupRoundTrip is the pair the backfills are written
// against: what Register is given is what MetaOf answers, and neither side's
// slice is the other's.
func TestMetaRegisterAndLookupRoundTrip(t *testing.T) {
	want := goodMeta()
	want.Type, want.Enum, want.Min = TypeEnum, []string{"auto", "always", "never"}, ""
	register(t, "probe.first_line_review", want)

	got, ok := MetaOf("probe.first_line_review")
	if !ok {
		t.Fatal("MetaOf found no metadata for a key Register was given")
	}
	if !slices.Equal(got.Enum, want.Enum) || got.Label != want.Label || got.Description != want.Description ||
		got.Group != want.Group || got.Type != want.Type || got.Format != want.Format {
		t.Fatalf("MetaOf answered %+v, and Register was given %+v", got, want)
	}

	got.Enum[0] = "mutated"
	if again, _ := MetaOf("probe.first_line_review"); again.Enum[0] != "auto" {
		t.Fatalf("editing the answered enum changed the table's own: %v", again.Enum)
	}
}

// TestMetaRegisterRefusesMalformed is the table's own shape rule: an entry that
// names no group of the order, no type of the vocabulary, or a control that
// cannot carry what it carries is a row the console would draw wrong, so
// Register refuses it where the family's file is loaded rather than at a
// screen.
func TestMetaRegisterRefusesMalformed(t *testing.T) {
	cases := []struct {
		name string
		key  string
		mut  func(*Meta)
	}{
		{name: "no key", key: "", mut: func(*Meta) {}},
		{name: "no label", key: "probe.review_rounds", mut: func(m *Meta) { m.Label = "" }},
		{name: "no description", key: "probe.review_rounds", mut: func(m *Meta) { m.Description = "" }},
		{name: "no group", key: "probe.review_rounds", mut: func(m *Meta) { m.Group = "" }},
		{name: "an unknown group", key: "probe.review_rounds", mut: func(m *Meta) { m.Group = "Agents" }},
		{name: "no type", key: "probe.review_rounds", mut: func(m *Meta) { m.Type = "" }},
		{name: "an unknown type", key: "probe.review_rounds", mut: func(m *Meta) { m.Type = "integer" }},
		{name: "an unknown format", key: "probe.review_rounds", mut: func(m *Meta) { m.Format = "semver" }},
		{
			name: "an enum with no value",
			key:  "probe.first_line_review",
			mut:  func(m *Meta) { m.Type, m.Min = TypeEnum, "" },
		},
		{
			name: "an enum on a non-enum type",
			key:  "probe.review_rounds",
			mut:  func(m *Meta) { m.Enum = []string{"1", "2"} },
		},
		{
			name: "a bound on a bool",
			key:  "probe.toggle",
			mut:  func(m *Meta) { m.Type = TypeBool },
		},
		{
			name: "a bound on a string",
			key:  "probe.name",
			mut:  func(m *Meta) { m.Type = TypeString },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodMeta()
			tc.mut(&m)
			if err := Register(tc.key, m); err == nil {
				t.Fatalf("Register(%q, %+v) accepted it", tc.key, m)
			}
			if _, ok := MetaOf(tc.key); ok {
				t.Fatalf("a refused entry left %q in the table", tc.key)
			}
		})
	}
}

// TestMetaRegisterAcceptsEveryVocabularyEntry is the other half: every group of
// the order, every type and every format is a metadata Register takes, so a
// family that picks one of them is not refused by a vocabulary that never
// matched what the table itself offers.
func TestMetaRegisterAcceptsEveryVocabularyEntry(t *testing.T) {
	for _, g := range MetaGroups {
		for _, ty := range MetaTypes {
			for _, f := range MetaFormats {
				m := goodMeta()
				m.Group, m.Type, m.Format = g, ty, f
				m.Min, m.Max = "", ""
				if ty == TypeEnum {
					m.Enum = []string{"one", "two"}
				}
				if ty == TypeInt || ty == TypeFloat || ty == TypeDuration {
					m.Min = "1"
				}
				key := strings.Join([]string{"vocab", string(g), string(ty), string(f)}, ".")
				register(t, key, m)
			}
		}
	}
}

// TestMetaVocabulariesAreDistinct pins the lists a console keys on: two
// spellings of one group would draw two accordion sections, and a repeated type
// or format would make a membership check answer twice.
func TestMetaVocabulariesAreDistinct(t *testing.T) {
	if len(MetaGroups) == 0 {
		t.Fatal("the group order is empty, and every entry names a group of it")
	}
	for _, g := range MetaGroups {
		if g == "" {
			t.Fatal("the group order holds the empty group")
		}
	}
	if n := len(slices.Compact(slices.Sorted(slices.Values(MetaGroups)))); n != len(MetaGroups) {
		t.Fatal("the group order names a group twice")
	}
	if n := len(slices.Compact(slices.Sorted(slices.Values(MetaTypes)))); n != len(MetaTypes) {
		t.Fatal("MetaTypes names a type twice")
	}
	if n := len(slices.Compact(slices.Sorted(slices.Values(MetaFormats)))); n != len(MetaFormats) {
		t.Fatal("MetaFormats names a format twice")
	}
}
