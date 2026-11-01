package config

import (
	"slices"
	"strings"
	"testing"
)

// TestEffortsClosedSet pins the set itself and its order: the five values
// the pinned Claude Code accepts, from the least to the most (L0.1,
// docs/multi-model.md). The order is part of the pin because a later task
// clamps one level's effort against another's, and a set that is right but
// in a different order would clamp the wrong way.
func TestEffortsClosedSet(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	if !slices.Equal(Efforts, want) {
		t.Fatalf("Efforts = %v, want %v (the five values Claude Code's --help prints, low to max)", Efforts, want)
	}
	for _, e := range want {
		if !ValidEffort(e) {
			t.Errorf("ValidEffort(%q) = false, want true", e)
		}
	}
	// Everything that is not one of the five, the empty string included:
	// "" is how an absent key reads once decoded, so it is not a value of
	// the set. "auto" and "unset" are the binary's own spellings for "no
	// setting", which is what leaving the key out already asks for.
	for _, e := range []string{"", "auto", "unset", "AUTO", "Low", "veryhigh", "none", "x-high", "xhigh ", "default"} {
		if ValidEffort(e) {
			t.Errorf("ValidEffort(%q) = true, want false: the set is closed", e)
		}
	}
}

// TestLevelSettingRefusesUnknownEffort: a value outside the set is refused
// where it is decoded, naming the set. Nothing downstream refuses it — the
// flag warns and sends the model's default, and the environment variable
// and the settings key are dropped in silence — so a run whose task carried
// a typo would quietly do the wrong thing (or, worse, the right one by
// accident) with nobody the wiser. "high" with a leading space and "HIGH"
// are the near misses a person writes; "auto" and "unset" are the ones the
// binary itself understands, which is exactly why they must not be read as
// levels here.
func TestLevelSettingRefusesUnknownEffort(t *testing.T) {
	for _, bad := range []string{"auto", "unset", "HIGH", " high", ""} {
		if ValidEffort(bad) {
			t.Errorf("ValidEffort(%q) = true, want false", bad)
		}
		if bad == "" {
			// An empty effort is the absent setting, which means the
			// model's own default, so Validate leaves it alone: refusing it
			// would refuse `effort: ""` and an omitted key differently over
			// a distinction nothing downstream can act on.
			continue
		}
		err := (&LevelSetting{Effort: bad}).validate("l2")
		if err == nil {
			t.Errorf("effort %q was accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), strings.Join(Efforts, ", ")) {
			t.Errorf("effort %q refusal %q does not name the value and the set", bad, err)
		}
	}
	for _, good := range Efforts {
		if err := (&LevelSetting{Effort: good}).validate("l2"); err != nil {
			t.Errorf("effort %q was refused: %v", good, err)
		}
	}
	// A whole block is judged at once, and every level's message names the
	// level it is about, so an operator reading a refusal of a file with two
	// bad levels knows which line to change.
	err := (&ReviewLevels{
		L1: &LevelSetting{Effort: "auto"},
		L3: &LevelSetting{Effort: "maximal"},
	}).Validate()
	if err == nil {
		t.Fatal("a block with two unknown efforts was accepted")
	}
	for _, want := range []string{"l1.effort", "l3.effort"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	if err := (&ReviewLevels{L1: &LevelSetting{Effort: "low"}}).Validate(); err != nil {
		t.Errorf("a level at an effort of the set was refused: %v", err)
	}
	if err := (*ReviewLevels)(nil).Validate(); err != nil {
		t.Errorf("a nil block was refused: %v", err)
	}
}

// TestReviewLevelsFocusBound: a focus is at most MaxLevelFocusBytes BYTES,
// which is what makes the cap hold whatever the text is made of — a rune
// count would let 500 four-byte characters through — and it carries no
// character that moves the text the reviewer reads: a control character
// (tab and the line breaks included), a bidi override, a zero-width or
// other invisible formatting character, a line or paragraph separator, or
// an invalid byte. The text is quoted into a reviewer's prompt (L4.2), so
// the rule is the one the project layer applies to a value out of a file
// (badRune), and the bound is the one L1.3 applies at every layer.
func TestReviewLevelsFocusBound(t *testing.T) {
	at := strings.Repeat("a", MaxLevelFocusBytes)
	if err := (&LevelSetting{Focus: at}).validate("l3"); err != nil {
		t.Errorf("a focus at the bound was refused: %v", err)
	}
	// Two bytes per rune: the same rune count is twice the bound, so a rule
	// counting runes would accept this.
	if err := (&LevelSetting{Focus: strings.Repeat("é", MaxLevelFocusBytes/2)}).validate("l3"); err != nil {
		t.Errorf("a focus at the bound in multi-byte characters was refused: %v", err)
	}
	for _, tc := range []struct {
		name  string
		focus string
	}{
		{"one byte over the bound", at + "a"},
		{"one multi-byte character over the bound", strings.Repeat("é", MaxLevelFocusBytes/2+1)},
		{"an escape character", "the migration \x1b[31mand the rollback"},
		{"a newline", "the migration\nthe rollback"},
		{"a tab", "the migration\tand the rollback"},
		{"a carriage return", "the migration\rthe rollback"},
		{"a bidi override (RLO)", "the migration \u202eand the rollback"},
		{"a bidi isolate (LRI)", "the migration \u2066and the rollback"},
		{"a right-to-left mark", "the migration \u200fand the rollback"},
		{"a zero-width space", "the migration\u200band the rollback"},
		{"a word joiner", "the migration\u2060and the rollback"},
		{"a byte-order mark", "\ufeffthe migration and the rollback"},
		{"a line separator", "the migration\u2028and the rollback"},
		{"a paragraph separator", "the migration\u2029and the rollback"},
		{"a soft hyphen", "the migra\u00adtion and the rollback"},
		{"a DEL character", "the migration\x7fand the rollback"},
		{"a C1 control character", "the migration\u0085and the rollback"},
		{"an invalid byte", "the migration\xffand the rollback"},
	} {
		err := (&LevelSetting{Focus: tc.focus}).validate("l3")
		if err == nil {
			t.Errorf("%s: the focus was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "l3.focus") {
			t.Errorf("%s: refusal %q does not name l3.focus", tc.name, err)
		}
	}
	// The bound is on bytes, so the refusal says how many there are rather
	// than how many runes: the two differ for exactly the text the bound
	// exists for.
	err := (&LevelSetting{Focus: at + "a"}).validate("l1")
	if err == nil || !strings.Contains(err.Error(), "501") {
		t.Errorf("a focus over the bound = %v, want a refusal naming 501 bytes", err)
	}
	if err := (&LevelSetting{}).validate("l1"); err != nil {
		t.Errorf("a level with no focus was refused: %v", err)
	}
}

// TestLevelSettingNoneExcludesOthers: `none: true` is a level that does not
// run, so the other three keys are refused beside it rather than dropped —
// a block that says both "do not run this level" and "run it on this model"
// is a mistake in one of the two, and which one is the writer's to say.
func TestLevelSettingNoneExcludesOthers(t *testing.T) {
	if err := (&LevelSetting{Off: true}).validate("l2"); err != nil {
		t.Errorf("none: true alone was refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		s    LevelSetting
	}{
		{"none: true with a model", LevelSetting{Off: true, Model: "claude-opus-5-5"}},
		{"none: true with an effort", LevelSetting{Off: true, Effort: "high"}},
		{"none: true with a focus", LevelSetting{Off: true, Focus: "the migration"}},
	} {
		err := tc.s.validate("l2")
		if err == nil || !strings.Contains(err.Error(), "l2") {
			t.Errorf("%s: validate() = %v, want a refusal naming l2", tc.name, err)
		}
	}
	// none: false is the same setting as an omitted key — the level runs —
	// so it sits beside any setting the way an omitted key does.
	if err := (&LevelSetting{Model: "claude-opus-5-5", Effort: "xhigh", Focus: "the migration"}).validate("l2"); err != nil {
		t.Errorf("a level with all three settings was refused: %v", err)
	}
}

// TestParseReviewLevelsYAMLIsStrict: the file a launcher passes is refused
// rather than partly read. An unknown key is the one that matters — the
// block decides which reviews a run does and on which model, and yaml.v3
// would drop `nmae: ...` without a word — and it is refused at every level
// of the object, since a key unknown at the top (`l4`) and one unknown
// inside a level (`effort` misspelled) are the same mistake at two depths.
// The block's own rules are applied to what the file says, so a document
// that decodes but asks for an effort outside the set is refused here too:
// this is the reader the launcher's flag uses, and the task's own copy goes
// through task.Parse, which is strict the same way.
func TestParseReviewLevelsYAMLIsStrict(t *testing.T) {
	ok, err := ParseReviewLevelsYAML([]byte("l1:\n  model: claude-haiku-4-5\nl2:\n  none: true\nl3:\n  effort: xhigh\n  focus: the migration and its rollback\n"))
	if err != nil {
		t.Fatalf("a valid document was refused: %v", err)
	}
	want := &ReviewLevels{
		L1: &LevelSetting{Model: "claude-haiku-4-5"},
		L2: &LevelSetting{Off: true},
		L3: &LevelSetting{Effort: "xhigh", Focus: "the migration and its rollback"},
	}
	if ok.L1 == nil || *ok.L1 != *want.L1 || ok.L2 == nil || *ok.L2 != *want.L2 || ok.L3 == nil || *ok.L3 != *want.L3 {
		t.Fatalf("decoded %+v, want %+v", ok, want)
	}
	// An empty document is refused rather than read as "no levels": the
	// caller asked for a block and handed over nothing, which is a mistake
	// to report, not a block that says nothing.
	if _, err := ParseReviewLevelsYAML(nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty document = %v, want a refusal saying it is empty", err)
	}
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"an unknown key at the top", "l4:\n  none: true\n", "l4"},
		{"an unknown key inside a level", "l1:\n  efforts: high\n", "efforts"},
		{"an unknown key inside a level beside a real one", "l2:\n  effort: high\n  model_name: claude-opus-5-5\n", "model_name"},
		{"an unknown key in the first level of three", "l1:\n  none: true\nl2:\n  none: true\nl3:\n  off: true\n", "off"},
		{"an effort outside the set", "l3:\n  effort: auto\n", "auto"},
		{"none: true beside another key", "l2:\n  none: true\n  model: claude-opus-5-5\n", "none: true"},
		{"a focus over the bound", "l3:\n  focus: " + strings.Repeat("a", MaxLevelFocusBytes+1) + "\n", "focus"},
		{"a focus with a bidi override", "l3:\n  focus: \"the \\u202emigration\"\n", "focus"},
		{"a second document", "l1:\n  none: true\n---\nl2:\n  none: true\n", "more than one"},
	} {
		_, err := ParseReviewLevelsYAML([]byte(tc.doc))
		if err == nil {
			t.Errorf("%s: the document was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refusal %q does not mention %q", tc.name, err, tc.want)
		}
	}
}
