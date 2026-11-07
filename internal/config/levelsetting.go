package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Efforts is the closed set of reasoning-effort values, ordered from the
// least to the most, that the pinned Claude Code accepts for one stage: the
// list its own --help prints, measured on 2026-10-30 against Claude Code
// 2.1.283 (docs/multi-model.md, "How the pinned Claude Code takes an effort
// setting").
//
// It is closed in both directions. "auto" and "unset" are not values a run
// may ask for: absent already means the model's own default, which is what
// the binary sends for them. And nothing outside this list is ever sent,
// because Claude Code is no gate at all — the flag warns and falls back to
// the default, and the environment variable and the settings key are dropped
// in silence — so Fugaro refusing the value here is the only refusal there
// is.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// ValidEffort reports whether e is one of Efforts.
func ValidEffort(e string) bool { return slices.Contains(Efforts, e) }

// MaxLevelFocusBytes bounds one review level's focus text, at every layer
// that may set it. The text is quoted into the reviewer's prompt and is
// never a path, a command or an authorization input, so the bound is what
// keeps one level's line from crowding out the rest of the instructions.
const MaxLevelFocusBytes = 500

// NoneModel is the literal model name that says a level does not run: `model:
// none` is exactly `none: true` (owner, 2026-11-07) — the level's own switch
// written the other way — and it is refused beside any other key for the same
// reason. LevelSetting.validate folds it into Off, so a level written that way
// is skipped wherever a level is read and the string is never resolved as a
// model name.
const NoneModel = "none"

// LevelSetting is one review level's settings (D722): which model runs it,
// at which effort, whether it runs at all, and what it is asked to focus on.
// It is the task's own per-level block and the same type a configuration
// layer's agent.levels carries, so a setting means one thing wherever it is
// written.
//
// The type lives here, in internal/config, rather than in the package that
// consumes it, because internal/task already imports this one and a resolver
// over the merged configuration (L3.1) has to read a task's own block.
type LevelSetting struct {
	// Model is the exact model ID this level runs. Empty leaves the level on
	// the model its own layer resolves, which is agent.models.reviewer for a
	// senior level, and it is bounded by allowed_models as every other model
	// setting is. The literal none is not an ID: it is the level's own switch
	// (NoneModel), and validate folds it into Off rather than reading it as a
	// model.
	Model string `json:"model,omitempty" yaml:"model"`
	// Effort is a value of Efforts, or empty for the model's own default.
	// It is refused for a provider route (D725), where it would be a setting
	// that looks applied and is not: those stages run with thinking off.
	Effort string `json:"effort,omitempty" yaml:"effort"`
	// Off is the YAML/JSON key `none`: true turns this level off. It is the
	// one key whose name is not the field's, because `none` is what an owner
	// writes to say a level does not run, and `off: true` would read as a
	// value rather than as a level's absence.
	//
	// It is a plain bool and not a pointer, so `none: false` and an absent
	// key are the same setting: a level that is on. What the key buys is the
	// refusal of `none: true` beside any other key — a level that is off has
	// no model, effort or focus to read.
	Off bool `json:"none,omitempty" yaml:"none"`
	// Focus is what this level's reviewer is asked to look at, in the
	// owner's or the launcher's own words. It is data and never a command:
	// L4.2 puts it in the reviewer's prompt as quoted text, and it is never
	// put through a shell or joined into a path.
	Focus string `json:"focus,omitempty" yaml:"focus"`
	// noneFalse records that a document wrote the `none` key with the value
	// false. A plain bool cannot tell that from the absent key — everywhere
	// else the two are the same setting, which is what Off's own rule says —
	// but it is the one key that says "this level runs" beside a `model:
	// none`, and a block that says both things is refused rather than read
	// (validate). Only the mapping node can tell them apart, the way
	// JobProfile.allowReposWritten does for allow_repos.
	noneFalse bool
}

// ReviewLevels is a task's or a layer's per-level settings: L1 is the first
// line of review, L2 the senior round and L3 the adversarial one. A nil
// level says nothing about it, which is not the same as a level that is off
// — Off is how a layer says `none` — so a nil block leaves the level to the
// layers below it.
type ReviewLevels struct {
	L1 *LevelSetting `json:"l1,omitempty" yaml:"l1"`
	L2 *LevelSetting `json:"l2,omitempty" yaml:"l2"`
	L3 *LevelSetting `json:"l3,omitempty" yaml:"l3"`
}

// Validate checks a level block's own rules. Every message names the level
// it is about, relative to the block (l1.effort, l2.focus), so a caller
// need only name the block's own container: this one is review_levels in a
// task and agent.levels in a configuration layer.
func (r *ReviewLevels) Validate() error {
	if r == nil {
		return nil
	}
	var errs []error
	for _, l := range []struct {
		name string
		set  *LevelSetting
	}{{"l1", r.L1}, {"l2", r.L2}, {"l3", r.L3}} {
		if l.set != nil {
			errs = append(errs, l.set.validate(l.name))
		}
	}
	return errors.Join(errs...)
}

// levelSettingType is LevelSetting's own type name, for the unknown-key
// refusal below: UnmarshalYAML has to build the message yaml.v3's strict mode
// would have built, and that mode names the type it was decoding into.
var levelSettingType = reflect.TypeOf(LevelSetting{})

// levelSettingYAMLKeys are the yaml keys a level block accepts, read from the
// struct's own tags so a new field is picked up without a second list (the
// same thing providerKeys does for a providers entry).
var levelSettingYAMLKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(LevelSetting{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// levelSettingJSONKeys are the keys a level block accepts when a task's spec
// spells it, read from the json tags the same way. The two vocabularies are
// the same by the type's own rule — a setting means one thing wherever it is
// written — but each decoder is checked against its own tags, so a field that
// ever diverged would not be refused in one spelling and read in the other.
var levelSettingJSONKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(LevelSetting{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// UnmarshalYAML decodes one level block, refuses a key it does not know, and
// records a written `none: false` (noneFalse), which no plain bool can tell
// from the absent key.
//
// The refusal is normally the decoder's own (Decoder.KnownFields), but known
// fields are a property of a Decoder while decoding through a Node — the only
// way to see the `none` key itself, written true or false — is always lenient,
// so the check is repeated here, with the same message (JobProfile.UnmarshalYAML
// does the same for a profiles entry).
func (s *LevelSetting) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return levelNotAMapping(node)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; !levelSettingYAMLKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, levelSettingType)}}
		}
	}
	type plain LevelSetting // no methods: decodes the fields without recursing into this one
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*s = LevelSetting(v)
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "none" {
			s.noneFalse = !s.Off
			break
		}
	}
	return nil
}

// levelNotAMapping is the refusal for a level written as something other than
// a block of keys, in yaml.v3's own words: a custom unmarshaler replaces the
// message the decoder builds for a value of the wrong shape, and this is that
// message (its terror), so `l1: claude-haiku-4-5` reads here exactly as it
// read before one was installed.
func levelNotAMapping(node *yaml.Node) error {
	tag, value := node.ShortTag(), node.Value
	if tag == "!!map" || tag == "!!seq" {
		value = ""
	} else if len(value) > 10 {
		value = " `" + value[:7] + "...`"
	} else {
		value = " `" + value + "`"
	}
	return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: cannot unmarshal %s%s into %s", node.Line, tag, value, levelSettingType)}}
}

// UnmarshalJSON is UnmarshalYAML for a task's spec, whose block is decoded by
// encoding/json: the decoder's DisallowUnknownFields no longer reaches inside
// a type that decodes itself, so the unknown-key refusal is repeated here, and
// a written `"none": false` is recorded for the same reason as above.
func (s *LevelSetting) UnmarshalJSON(data []byte) error {
	type plain LevelSetting
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(data, &written); err != nil {
		return err
	}
	for k := range written {
		if !levelSettingJSONKeys[k] {
			return errors.New("json: unknown field " + strconv.Quote(k))
		}
	}
	*s = LevelSetting(v)
	if _, ok := written["none"]; ok {
		s.noneFalse = !s.Off
	}
	return nil
}

// validate checks one level's settings under name.
func (s *LevelSetting) validate(name string) error {
	var errs []error
	if s.Effort != "" && !ValidEffort(s.Effort) {
		errs = append(errs, fmt.Errorf("%s.effort %q must be one of %s", name, s.Effort, strings.Join(Efforts, ", ")))
	}
	switch {
	case s.Off && (s.Model != "" || s.Effort != "" || s.Focus != ""):
		errs = append(errs, fmt.Errorf("%s has none: true beside another key: a level that is off has no settings", name))
	case s.Model == NoneModel:
		// The literal model name none is the level's own switch written the
		// other way, so it is refused beside any other key exactly as
		// `none: true` is, and by a sentence naming them both. Alone it is
		// folded into the switch, so no reader of the block ever sees the
		// string none as a model name.
		if beside := s.besideNoneModel(); len(beside) > 0 {
			errs = append(errs, fmt.Errorf("%s has model: none beside %s: a level that is off has no settings", name, strings.Join(beside, " and ")))
		} else {
			s.Off, s.Model = true, ""
		}
	}
	if len(s.Focus) > MaxLevelFocusBytes {
		errs = append(errs, fmt.Errorf("%s.focus is longer than %d bytes (%d)", name, MaxLevelFocusBytes, len(s.Focus)))
	}
	// The character rule is the project layer's own for a value a file
	// supplies (badRune, internal/config/layer.go), plus tab and the line
	// breaks, which that one exempts for a multi-line value: a focus is
	// shown and quoted as one line, so a tab or a break in it moves the
	// text the reviewer reads.
	for _, c := range s.Focus {
		if badKeyRune(c) {
			errs = append(errs, fmt.Errorf("%s.focus holds a character it may not carry (U+%04X): a control character, an invisible formatting (bidi or zero-width) character, a line or paragraph separator, or invalid UTF-8", name, c))
			break
		}
	}
	return errors.Join(errs...)
}

// besideNoneModel are the keys a `model: none` was written beside, in the
// order the refusal about it names them: the settings a level that does not
// run has none of, and a written `none: false`, which says the level does run.
func (s *LevelSetting) besideNoneModel() []string {
	var out []string
	if s.Effort != "" {
		out = append(out, "effort")
	}
	if s.Focus != "" {
		out = append(out, "focus")
	}
	if s.noneFalse {
		out = append(out, "none: false")
	}
	return out
}

// ParseReviewLevelsYAML decodes a review-levels document — fugaro run
// --review-levels's file, and a configuration layer's agent.levels block —
// under this package's rules, and returns either the block or the reason it
// was refused.
//
// It is strict, because the writer is the launcher and the block decides
// which reviews a run does: an unknown key is refused at every level of the
// object rather than dropped, a second YAML document in the same file is
// refused rather than ignored, and the decode is followed by
// ReviewLevels.Validate, so an effort outside Efforts, a focus over its
// bound or beside a control character, and `none: true` beside another key
// are all refused here. The task's own copy of the block is decoded by
// internal/task's Parse, which is strict the same way (its
// DisallowUnknownFields reaches every nested object) and applies the same
// Validate through Spec.Validate.
func ParseReviewLevelsYAML(data []byte) (*ReviewLevels, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var r ReviewLevels
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("review levels: the document is empty")
		}
		return nil, fmt.Errorf("review levels: %w", err)
	}
	var more ReviewLevels
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, errors.New("review levels: the document holds more than one YAML document")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}
