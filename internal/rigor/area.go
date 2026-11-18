package rigor

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Area is one rule of rigor.areas (D20c): a name, a doublestar path glob
// relative to the repository root, and the level the rule sets.
//
// The name is not decoration. It is what the run record, the pull request
// body and fugaro watch print when they say which rule decided (D24), and a
// rule that cannot be named cannot be explained to the person reading the
// pull request it governed.
//
// Match is a path glob, matched by the same doublestar matcher the rest of
// the tree uses (F9: internal/plan/footprint.go, internal/imagecheck and
// internal/config/validate.go all match with it) and validated at load, so a
// pattern the matcher cannot read is a configuration error rather than a
// rule that silently never fires. The paths it is matched against are
// repository-relative, which is what runstore.Record.ChangedFiles holds.
type Area struct {
	Name  string `yaml:"name" json:"name"`
	Match string `yaml:"match" json:"match"`
	Level Rigor  `yaml:"level" json:"level"`
}

// areaType is Area's own type name, for the unknown-field refusal below:
// UnmarshalYAML has to build the message yaml.v3's strict mode would have
// built, and that mode names the type it was decoding into.
var areaType = reflect.TypeOf(Area{})

// areaKeys are the yaml keys one rule accepts, read from the struct's own tags
// so a new field is picked up without a second list (the same thing
// internal/config's jobProfileKeys does for a profiles entry).
var areaKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(Area{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// UnmarshalYAML decodes one rule, refuses a key it does not know, and refuses
// `mode:` by name.
//
// The second job is normally the decoder's own (Decoder.KnownFields), but known
// fields are a property of a Decoder while decoding through a Node is always
// lenient, so the check is repeated here, with the same message.
//
// The third is the migration: the key this rule's level is written under was
// `mode:` before LD2 renamed the axis, so a document a person upgrades carries
// that key and a bare "field mode not found" would leave them to guess the new
// one. The refusal says both spellings, which is the whole of what a reader
// needs to fix the file.
func (a *Area) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: an area rule must be a mapping of field to value", node.Line)}}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if k.Value == "mode" {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: an area rule's level is written `level:`, not `mode:` (the mode key was renamed)", k.Line)}}
		}
		if !areaKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, areaType)}}
		}
	}
	type plain Area // no methods: decodes the fields without recursing into this one
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*a = Area(v)
	return nil
}

// Bounds on a rule's two pieces of text and on how many rules a layer may
// write. All three reach the run record, and Match also reaches the pull
// request body when a rule decides a run, so none is unbounded.
const (
	// MaxAreaName bounds a rule's name.
	MaxAreaName = 40
	// MaxAreaPattern bounds a rule's glob.
	MaxAreaPattern = 200
	// MaxAreas bounds a layer's list of rules. The matched subset of it is
	// what the run record, the pull request body and fugaro watch print, so
	// the list a run can be made to carry is bounded where it is written.
	MaxAreas = 100
)

// areaNameRE is what an area name may be: a short label of letters, digits,
// space, dash, underscore and dot, starting with a letter or a digit. The
// name reaches three surfaces a person reads (the record, the pull request
// body, fugaro watch), so the set is closed here rather than escaped at each
// of them — a name holding a newline, a terminal control character or a
// bidi override is refused where it is written.
var areaNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

// Validate is a rule's load-time check (D20c). It returns "" for a rule the
// resolver can use, and otherwise the one reason it cannot, worded as the
// "must be ..." half of a configuration error (internal/config's Problem
// prints it after the rule's path).
//
// The pattern rule is internal/config/validate.go's rebuild.paths rule, the
// same four checks in the same order: not empty, repository-relative, no
// ".." component, and a pattern doublestar.ValidatePattern accepts. One
// matcher and one pattern rule, per F9.
func (a Area) Validate() string {
	switch {
	case a.Name == "":
		return "must have a name: it is what the run record and the pull request say decided the level"
	case !areaNameRE.MatchString(a.Name):
		return fmt.Sprintf("a name must be 1 to %d characters of letters, digits, space, dash, underscore and dot, starting with a letter or a digit", MaxAreaName)
	case strings.TrimSpace(a.Match) == "":
		return "must have a match: a doublestar path glob relative to the repository root"
	case len(a.Match) > MaxAreaPattern:
		return fmt.Sprintf("a match must be at most %d characters", MaxAreaPattern)
	case strings.HasPrefix(a.Match, "/"):
		return "a match must be relative to the repository root, not start with /"
	case slices.Contains(strings.Split(a.Match, "/"), ".."):
		return "a match must not contain a .. component"
	case !doublestar.ValidatePattern(a.Match):
		return "is not a valid glob pattern"
	case !Known(a.Level):
		return "level must be one of " + RigorList()
	}
	return ""
}

// RigorList is the vocabulary as an error names it: "poc, startup, pro,
// critical, hotfix". One spelling of the set, read by every refusal that has
// to say what a value should have been — this package's and
// internal/config's, which validates the two layers' rigor blocks.
func RigorList() string {
	names := make([]string, len(Rigors))
	for i, m := range Rigors {
		names[i] = string(m)
	}
	return strings.Join(names, ", ")
}
