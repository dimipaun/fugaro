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

// TestLevelSettingNoneModelIsTheSwitch: the literal model name `none` is the
// level's own switch written the other way (owner, 2026-11-07), so a block that
// writes it and nothing else is exactly `none: true`: validate folds it into
// Off, the level is skipped, and no reader ever sees the string none as a model
// name. Beside any other key it is refused by the rule `none: true` is refused
// by, and the refusal names both keys — the written `none: false` included,
// which is the one that says the level runs, so a block holding that beside
// `model: none` says both things at once.
func TestLevelSettingNoneModelIsTheSwitch(t *testing.T) {
	set := &LevelSetting{Model: NoneModel}
	if err := set.validate("l2"); err != nil {
		t.Fatalf("model: none alone was refused: %v", err)
	}
	if !set.Off || set.Model != "" {
		t.Errorf("after validate the level = %+v, want it folded into the switch (none: true, no model)", set)
	}

	for _, tc := range []struct {
		name string
		set  LevelSetting
		want string
	}{
		{"model: none with an effort", LevelSetting{Model: NoneModel, Effort: "high"}, "model: none beside effort"},
		{"model: none with a focus", LevelSetting{Model: NoneModel, Focus: "the migration"}, "model: none beside focus"},
		{"model: none beside none: true", LevelSetting{Off: true, Model: NoneModel}, "none: true beside another key"},
		{"model: none with both", LevelSetting{Model: NoneModel, Effort: "high", Focus: "the migration"}, "model: none beside effort and focus"},
	} {
		err := tc.set.validate("l2")
		if err == nil {
			t.Errorf("%s: validate() accepted the block", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "l2") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: validate() = %q, want a refusal naming l2 and %q", tc.name, err, tc.want)
		}
	}

	// A written `none: false` is the case no struct literal can build: the
	// plain bool reads it as the absent key, and only the decoder sees the key
	// at all.
	if _, err := ParseReviewLevelsYAML([]byte("l2:\n  model: none\n  none: false\n")); err == nil ||
		!strings.Contains(err.Error(), "model: none beside none: false") {
		t.Errorf("model: none beside a written none: false = %v, want a refusal naming both keys", err)
	}
	// The fold is what the block says, not what a reader may hope: a level
	// that writes `model: none` decodes to the switch and no model.
	got, err := ParseReviewLevelsYAML([]byte("l1:\n  model: none\nl2:\n  none: false\n"))
	if err != nil {
		t.Fatalf("a document with model: none and a written none: false on another level was refused: %v", err)
	}
	if got.L1 == nil || !got.L1.Off || got.L1.Model != "" {
		t.Errorf("l1 = %+v, want the level folded to none: true with no model", got.L1)
	}
	if got.L2 == nil || got.L2.Off || got.L2.Model != "" {
		t.Errorf("l2 = %+v, want a level that runs: a written none: false says nothing", got.L2)
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
		{"model: none beside an effort", "l2:\n  model: none\n  effort: high\n", "model: none beside effort"},
		{"model: none beside a written none: false", "l2:\n  model: none\n  none: false\n", "model: none beside none: false"},
		{"model: none beside none: true", "l2:\n  model: none\n  none: true\n", "none: true"},
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

// TestReviewLevelsMergeKey: a level block is a YAML mapping like any other, so
// a document may merge an anchor into one (`<<: *review`), which yaml.v3
// expands into the block's own keys before it sets a field. The block checks
// its keys itself (UnmarshalYAML), and it has to read the mapping the way the
// decoder does: taking `<<` for a key of its own would refuse a document that
// was read before, and looking only at the keys written in the block would let
// a key an anchor brings in through, where the decoder's strict mode refused
// it. Both spellings are the same block either way, at every door that decodes
// a level — the task's own `review_levels` is JSON, which has no merge key, and
// a configuration layer's document is the one door that refuses merges
// outright (layerShape).
func TestReviewLevelsMergeKey(t *testing.T) {
	const doc = `l1: &review
  model: claude-opus-5-5
  effort: xhigh
l2:
  <<: *review
l3:
  <<: *review
  focus: the migration and its rollback
`
	got, err := ParseReviewLevelsYAML([]byte(doc))
	if err != nil {
		t.Fatalf("a level that merges an anchor was refused: %v", err)
	}
	base := LevelSetting{Model: "claude-opus-5-5", Effort: "xhigh"}
	for _, tc := range []struct {
		name string
		got  *LevelSetting
		want LevelSetting
	}{
		{"l1, the anchor itself", got.L1, base},
		{"l2, which merges it and writes nothing", got.L2, base},
		{"l3, which merges it and writes a key beside it", got.L3, LevelSetting{Model: "claude-opus-5-5", Effort: "xhigh", Focus: "the migration and its rollback"}},
	} {
		if tc.got == nil || *tc.got != tc.want {
			t.Errorf("%s = %+v, want %+v", tc.name, tc.got, tc.want)
		}
	}

	// The same block in a fugaro.yaml: agent.levels is the same type decoded by
	// the same rules, so the launcher-side file accepts it as well.
	if _, problems := Parse([]byte(minimalYAML + `agent:
  levels:
    l1: &review
      model: claude-opus-5-5
    l2:
      <<: *review
      effort: xhigh
`)); len(problems) > 0 {
		t.Errorf("a fugaro.yaml whose level merges an anchor is refused: %v", problems)
	}

	// A key an anchor brings in is a key of the level it is merged into, even
	// when the anchor is a block of another kind: agent.models merged into a
	// level says nothing about the level, and a check of only the keys written
	// in the block would drop it — a setting that looks applied and is read by
	// nobody. The file is read here without being expanded first (decodeRepo,
	// the plain strict read AgentOf and a job's bootstrap use; Resolve's merged
	// tree has the anchor expanded before a level is decoded), so this is where
	// the merged keys are checked.
	if _, ok := AgentOf([]byte(minimalYAML + `agent:
  models: &models
    reviewer: claude-opus-5-5
  levels:
    l1:
      <<: *models
`)); ok {
		t.Errorf("AgentOf accepted a level that merges agent.models: the merged key was dropped rather than refused")
	}

	// And a `none: false` a merge brings in is written on the level all the
	// same: it says the level runs, so it is refused beside `model: none`
	// exactly as the written key is.
	if _, err := ParseReviewLevelsYAML([]byte("l1: &review\n  none: false\nl2:\n  <<: *review\n  model: none\n")); err == nil ||
		!strings.Contains(err.Error(), "model: none beside none: false") {
		t.Errorf("model: none beside a merged none: false = %v, want a refusal naming both keys", err)
	}
}

// TestLevelsNoneModelSetReadsTheText: RepoLevelsNoneModelSet and
// ProjectLayer.SetsLevelsNoneModel are the launch gate's read of the spelling
// (noneModelSince), and they have to read the text, because the resolved
// configuration cannot answer: LevelSetting.validate folds the literal into
// Off, so a level written `model: none` resolves exactly as one that wrote
// `none: true` and nothing downstream says which of the two the file used,
// while a runner older than the release reads the literal as a model ID
// instead of a switch. The two flags follow the fields beside them
// (RepoRigorSet, SetsRigor): they read the value, never the merged one, and
// carry nothing into the file, SHA256 or the resolved levels.
func TestLevelsNoneModelSetReadsTheText(t *testing.T) {
	layer := mustLayer(t, testLayer)
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"the literal, one line", minimalRepo + "agent: { levels: { l2: { model: none } } }\n", true},
		{"the literal, at another level", minimalRepo + "agent:\n  levels:\n    l3:\n      model: none\n", true},
		{"the literal beside a level that runs", minimalRepo + "agent: { levels: { l1: { model: claude-haiku-4-5 }, l3: { model: none } } }\n", true},
		{"the other spelling", minimalRepo + "agent: { levels: { l2: { none: true } } }\n", false},
		{"a model that is not the literal", minimalRepo + "agent: { levels: { l2: { model: claude-opus-5-5 } } }\n", false},
		{"a level that is not a mapping", minimalRepo + "agent: { levels: { l2: } }\n", false},
		{"no levels at all", minimalRepo, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, _, ps := Resolve([]byte(c.text), layer)
			if len(ps) > 0 {
				t.Fatalf("Resolve: %v", ps)
			}
			if cfg.RepoLevelsNoneModelSet != c.want {
				t.Errorf("RepoLevelsNoneModelSet = %v, want %v for\n%s", cfg.RepoLevelsNoneModelSet, c.want, c.text)
			}
			// And never the merged levels: a layer's level does not set a
			// repository's flag, and a repository's does not set a layer's.
			if cfg.Layer.SetsLevelsNoneModel() {
				t.Errorf("the layer sets the flag, and its text names no level for\n%s", c.text)
			}
		})
	}

	// The fold is why the flag exists: the resolved level reads as the switch
	// and carries no model, so a gate reading agent.levels could not tell this
	// document from one that wrote none: true.
	cfg, _, ps := Resolve([]byte(minimalRepo+"agent: { levels: { l2: { model: none } } }\n"), layer)
	if len(ps) > 0 {
		t.Fatalf("Resolve: %v", ps)
	}
	if l2 := cfg.Agent.Levels.L2; l2 == nil || !l2.Off || l2.Model != "" {
		t.Errorf("the resolved l2 = %+v, want the level off with no model, exactly as none: true resolves", l2)
	}

	// The layer's half: defaults.agent.levels is the other text the launch
	// reads, and it is read the same way. The fixture is testLayer with the
	// levels written under its own defaults.agent, so the rest of the layer
	// still resolves and the two flags can be told apart below.
	layerWith := func(levels string) string {
		with := strings.Replace(testLayer, "    recipe: claude-solo\n", "    recipe: claude-solo\n    levels:\n"+levels, 1)
		if with == testLayer {
			t.Fatal("testLayer no longer writes defaults.agent.recipe: the levels would land nowhere")
		}
		return with
	}
	for _, c := range []struct {
		name   string
		levels string
		want   bool
	}{
		{"the literal", "      l2:\n        model: none\n", true},
		{"the other spelling", "      l2:\n        none: true\n", false},
		{"a model that is not the literal", "      l2:\n        model: claude-opus-5-5\n", false},
	} {
		t.Run("layer, "+c.name, func(t *testing.T) {
			l := mustLayer(t, layerWith(c.levels))
			if l.SetsLevelsNoneModel() != c.want {
				t.Errorf("SetsLevelsNoneModel() = %v, want %v for\n%s", l.SetsLevelsNoneModel(), c.want, c.levels)
			}
			// The layer says nothing about a repository's own file: the
			// launch reads the two texts separately for exactly this reason.
			repo, _, ps := Resolve([]byte(minimalRepo), l)
			if len(ps) > 0 {
				t.Fatalf("Resolve: %v", ps)
			}
			if repo.RepoLevelsNoneModelSet {
				t.Errorf("a file that names no level set RepoLevelsNoneModelSet over\n%s", c.levels)
			}
		})
	}
	if mustLayer(t, testLayer).SetsLevelsNoneModel() {
		t.Error("testLayer names no level and sets the flag")
	}
	var nilLayer *ProjectLayer
	if nilLayer.SetsLevelsNoneModel() {
		t.Error("a nil layer sets the flag")
	}

	// The door the launch's own file read uses (launchFileConfig, Parse):
	// the flag is set on the configuration a launcher gets back, needs no
	// layer, and is not part of what the file's SHA256 covers — it is a
	// reading of the text, not a setting of it.
	parsed, ps := Parse([]byte(minimalYAML + "agent: { levels: { l1: { model: none } } }\n"))
	if len(ps) > 0 {
		t.Fatalf("Parse: %v", ps)
	}
	if !parsed.RepoLevelsNoneModelSet {
		t.Error("Parse lost the flag: the launch gate reads the file through this door")
	}
	bare, ps := Parse([]byte(minimalYAML + "agent: { levels: { l1: { none: true } } }\n"))
	if len(ps) > 0 {
		t.Fatalf("Parse: %v", ps)
	}
	if bare.RepoLevelsNoneModelSet {
		t.Error("the other spelling set the flag")
	}
}
