package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
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
	// setting is.
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

// validate checks one level's settings under name.
func (s *LevelSetting) validate(name string) error {
	var errs []error
	if s.Effort != "" && !ValidEffort(s.Effort) {
		errs = append(errs, fmt.Errorf("%s.effort %q must be one of %s", name, s.Effort, strings.Join(Efforts, ", ")))
	}
	if s.Off && (s.Model != "" || s.Effort != "" || s.Focus != "") {
		errs = append(errs, fmt.Errorf("%s has none: true beside another key: a level that is off has no settings", name))
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
