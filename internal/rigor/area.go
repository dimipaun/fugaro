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

// Area is one rule of rigor.areas (D20c): a name, what the rule is made of,
// and the level the rule sets.
//
// The name is not decoration. It is what the run record, the pull request
// body and fugaro watch print when they say which rule decided (D24), and a
// rule that cannot be named cannot be explained to the person reading the
// pull request it governed.
//
// What a rule is made of depends on the layer that writes it (LD4). A
// repository's own rule names a path glob — Match — because the rule is
// already about that repository's own tree; that is the shape the code has
// always had and it does not change. A rule of the project layer names its
// **members**: whole repositories (`{repo}`) or parts of them
// (`{repo, paths}`), because an area is a named semantic part of the project
// and not necessarily one repository (LD4), and because one repository may
// sit in two areas on disjoint paths — its ui/ in one and its mobile/ in the
// other. A project rule may also carry Match, the shape v0.11.0 shipped and
// still read, which is a glob in effect for every repository of the project
// beside the members; the two are a union, and a rule that matches by either
// decides.
//
// Match is matched by the same doublestar matcher the rest of the tree uses
// (F9: internal/plan/footprint.go, internal/imagecheck and
// internal/config/validate.go all match with it) and validated at load, so a
// pattern the matcher cannot read is a configuration error rather than a
// rule that silently never fires. The paths it is matched against are
// repository-relative, which is what runstore.Record.ChangedFiles holds, and
// so are a member's paths — relative to that member's own repository root.
type Area struct {
	Name    string   `yaml:"name" json:"name"`
	Match   string   `yaml:"match,omitempty" json:"match,omitempty"`
	Members []Member `yaml:"members,omitempty" json:"members,omitempty"`
	Level   Rigor    `yaml:"level" json:"level"`
}

// Member is one member of a project layer's area rule: a repository, and —
// optionally — the paths of it that are in the area (LD4). A member with no
// paths is the whole repository.
//
// The repository is named "owner/name", the spelling a task's own Repo
// carries, so the resolver's answer for a run needs no translation: the
// project layer cannot name a provider either, and the layer is one document
// for every repository of the project whatever provider hosts it.
//
// Paths are doublestar globs relative to that repository's root, held to
// Match's four checks, so no member can name a path outside the repository
// it is a member of — a member is a part of a repository, and a rule that
// reached outside its own tree would be naming another member by accident.
type Member struct {
	Repo  string   `yaml:"repo" json:"repo"`
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

// memberKeys are the yaml keys one member accepts, for the same reason and in
// the same way as areaKeys: decoding through a Node is always lenient, so the
// unknown-field check the decoder would make is repeated here.
var memberKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(Member{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

var memberType = reflect.TypeOf(Member{})

// UnmarshalYAML decodes one member and refuses a key it does not know, with
// the message yaml.v3's strict mode would have built (Area.UnmarshalYAML's
// own reason).
func (m *Member) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: a member must be a mapping of field to value", node.Line)}}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; !memberKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, memberType)}}
		}
	}
	type plain Member // no methods: decodes the fields without recursing into this one
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*m = Member(v)
	return nil
}

// areaType is Area's own type name, for the unknown-field refusal below:
// UnmarshalYAML has to build the message yaml.v3's strict mode would have
// built, and that mode names the type it was decoding into.
var areaType = reflect.TypeOf(Area{})

// areaKeys are the yaml keys one rule accepts, read from the struct's own tags
// so a new field is picked up without a second list (the same thing
// internal/config's jobProfileKeys does for a profiles entry). `mode` is the
// one key added by hand, and it is not a field of Area: it is the spelling the
// level was written under before LD2 (see UnmarshalYAML).
var areaKeys = func() map[string]bool {
	out := map[string]bool{"mode": true}
	t := reflect.TypeOf(Area{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// AreaKeys is the keys one area rule accepts, in sorted order: the four fields
// above and `mode`, the spelling the level was written under before LD2 and read
// beside `level` since. A surface that has to admit what this loader admits asks
// here rather than keeping a list that could fall behind the decoder — the
// settings registry is not that surface for this key, because its rows are the
// keys `fugaro settings` writes and this one is read and never written.
func AreaKeys() []string {
	out := make([]string, 0, len(areaKeys))
	for k := range areaKeys {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// UnmarshalYAML decodes one rule, refuses a key it does not know, and reads the
// old spelling of the rule's level.
//
// The unknown-field check is normally the decoder's own (Decoder.KnownFields),
// but known fields are a property of a Decoder while decoding through a Node is
// always lenient, so the check is repeated here, with the same message.
//
// The old spelling is `mode:`, and it is read rather than refused because it is
// released: v0.11.0 shipped rigor.areas with that key, and a released
// `fugaro rigor set --area` wrote it into the project layer this file loads. A
// repository's own fugaro.yaml is never rewritten by `fugaro update`, so a
// refusal here would be a manual repair on two version-1 formats — the thing
// CLAUDE.md's rule about update forbids. It follows RigorRecord.LevelOf's rule
// for the same value: `level:` decides whenever it is written, `mode:` is the
// fallback, and a file carrying both is read as the new key says.
//
// `level:` being written but unknown is still a refusal — the rule's own level
// is what Validate checks — so a typo in the new spelling is a message naming
// the vocabulary, not a silent fall back to the old one.
func (a *Area) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: an area rule must be a mapping of field to value", node.Line)}}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; !areaKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, areaType)}}
		}
	}
	type plain Area // no methods: decodes the fields without recursing into this one
	var v struct {
		plain `yaml:",inline"`
		Mode  Rigor `yaml:"mode"` // the pre-LD2 spelling of Level, read and never written
	}
	if err := node.Decode(&v); err != nil {
		return err
	}
	rule := Area(v.plain)
	if rule.Level == "" {
		rule.Level = v.Mode
	}
	*a = rule
	return nil
}

// Bounds on a rule's two pieces of text, on how many rules a layer may
// write and on how large one project rule's members list may be. All four
// reach the run record, and Match also reaches the pull request body when a
// rule decides a run, so none is unbounded. A member's own paths reach the
// same surfaces as Match and are bounded by the same figure.
const (
	// MaxAreaName bounds a rule's name.
	MaxAreaName = 40
	// MaxAreaPattern bounds a rule's glob, and a member's.
	MaxAreaPattern = 200
	// MaxAreas bounds a layer's list of rules. The matched subset of it is
	// what the run record, the pull request body and fugaro watch print, so
	// the list a run can be made to carry is bounded where it is written.
	MaxAreas = 100
	// MaxMembers bounds one project rule's members list, for MaxAreas'
	// reason: the matched rule reaches the run record and the pull request
	// body, and so does its own list of members.
	MaxMembers = 100
	// MaxMemberPaths bounds the paths one member may name, for the same
	// reason: each is a glob that reaches the same surfaces.
	MaxMemberPaths = 100
	// MaxRepoLevels bounds the project layer's per-repository map. Every
	// entry is a repository of the project and the level it runs at, and the
	// map is read at every resolution, so it is bounded where it is written.
	MaxRepoLevels = 100
)

// areaNameRE is what an area name may be: a short label of letters, digits,
// space, dash, underscore and dot, starting with a letter or a digit. The
// name reaches three surfaces a person reads (the record, the pull request
// body, fugaro watch), so the set is closed here rather than escaped at each
// of them — a name holding a newline, a terminal control character or a
// bidi override is refused where it is written.
var areaNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

// Validate is a rule's load-time check in a repository's own fugaro.yaml
// (D20c). It returns "" for a rule the resolver can use, and otherwise the one
// reason it cannot, worded as the "must be ..." half of a configuration error
// (internal/config's Problem prints it after the rule's path).
//
// The pattern rule is internal/config/validate.go's rebuild.paths rule, the
// same four checks in the same order: not empty, repository-relative, no
// ".." component, and a pattern doublestar.ValidatePattern accepts. One
// matcher and one pattern rule, per F9.
//
// A repository's own rule takes no members: the rule is already about that
// repository's own tree, so its members would be a second way of saying what
// the file it is in already says, and a member's repo name could only be the
// repository's own or a mistake. ValidateProject is the project layer's half.
func (a Area) Validate() string {
	if msg := a.validateName(); msg != "" {
		return msg
	}
	if len(a.Members) > 0 {
		return "a repository's own rule takes no members: a rule in fugaro.yaml is already about this repository's paths, and only the project layer's rules name the repositories they are made of"
	}
	if strings.TrimSpace(a.Match) == "" {
		return "must have a match: a doublestar path glob relative to the repository root"
	}
	if msg := a.validateMatch(); msg != "" {
		return msg
	}
	return a.validateLevel()
}

// ValidateProject is a rule's load-time check in the project layer (LD4). It
// is Validate's twin and shares its checks: what differs is what the rule is
// made of, since a project rule names one of the two spellings — a Match in
// effect for every repository of the project (the shape v0.11.0 shipped, and
// what `fugaro rigor set --area` writes), a Members list, or both — and a
// Match alone leaves the rule with no repository to be a member of.
//
// The member bound and each member's own check are stated here rather than in
// Member alone so one rule's refusal names the rule: a member is refused
// because the area it belongs to cannot be used, and the path
// internal/config prints is the rule's.
func (a Area) ValidateProject() string {
	if msg := a.validateName(); msg != "" {
		return msg
	}
	if a.Match == "" && len(a.Members) == 0 {
		return "must say what the area is made of: a doublestar match in effect for every repository of the project, or a members list of {repo} or {repo, paths}"
	}
	if len(a.Members) > MaxMembers {
		return fmt.Sprintf("an area must hold at most %d members", MaxMembers)
	}
	for i, m := range a.Members {
		if msg := m.Validate(); msg != "" {
			return fmt.Sprintf("members[%d] %s", i, msg)
		}
	}
	if strings.TrimSpace(a.Match) != "" {
		if msg := a.validateMatch(); msg != "" {
			return msg
		}
	}
	return a.validateLevel()
}

// validateName is the check both rules share.
func (a Area) validateName() string {
	switch {
	case a.Name == "":
		return "must have a name: it is what the run record and the pull request say decided the level"
	case !areaNameRE.MatchString(a.Name):
		return fmt.Sprintf("a name must be 1 to %d characters of letters, digits, space, dash, underscore and dot, starting with a letter or a digit", MaxAreaName)
	}
	return ""
}

// validateMatch is Match's four checks, for a rule that writes one at all: a
// rule's emptiness is its caller's own question, since a project rule may name
// only members.
func (a Area) validateMatch() string {
	switch {
	case len(a.Match) > MaxAreaPattern:
		return fmt.Sprintf("a match must be at most %d characters", MaxAreaPattern)
	case strings.HasPrefix(a.Match, "/"):
		return "a match must be relative to the repository root, not start with /"
	case slices.Contains(strings.Split(a.Match, "/"), ".."):
		return "a match must not contain a .. component"
	case !doublestar.ValidatePattern(a.Match):
		return "is not a valid glob pattern"
	}
	return ""
}

// validateLevel is the level's own check, shared by both rules.
func (a Area) validateLevel() string {
	if !Known(a.Level) {
		return "level must be one of " + RigorList()
	}
	return ""
}

// Validate is one member's check (LD4): the repository it names, and the paths
// of it that are in the area. It returns "" for a member the resolver can use,
// and otherwise the one reason it cannot, worded as Area.ValidateProject's own
// messages are: the sentence the rule's refusal continues.
//
// The repository is checked for its shape only. Whether the project admits it
// at all is the published allow_repos' answer, which this layer cannot see and
// which `fugaro doctor` warns about — the shape check is what a layer file can
// be held to where it is written.
func (m Member) Validate() string {
	switch {
	case m.Repo == "":
		return "must name the repository it is a member of, as owner/name"
	case !memberRepoRE.MatchString(m.Repo) || hasDotSegment(m.Repo):
		return "a member's repo must be owner/name: letters, digits, dot, dash and underscore in each of two segments, and no . or .. segment"
	case len(m.Paths) > MaxMemberPaths:
		return fmt.Sprintf("a member must name at most %d paths", MaxMemberPaths)
	}
	for i, p := range m.Paths {
		if msg := memberPathProblem(p); msg != "" {
			return fmt.Sprintf("paths[%d] %s", i, msg)
		}
	}
	return ""
}

// memberPathProblem is Match's four checks, worded for a member's path: one
// glob rule for the layer, per F9.
func memberPathProblem(p string) string {
	switch {
	case strings.TrimSpace(p) == "":
		return "must be a doublestar path glob relative to the member repository's root"
	case len(p) > MaxAreaPattern:
		return fmt.Sprintf("must be at most %d characters", MaxAreaPattern)
	case strings.HasPrefix(p, "/"):
		return "must be relative to the member repository's root, not start with /"
	case slices.Contains(strings.Split(p, "/"), ".."):
		return "must not contain a .. component: a member is a part of its repository, so its paths stay inside it"
	case !doublestar.ValidatePattern(p):
		return "is not a valid glob pattern"
	}
	return ""
}

// memberRepoRE is the shape a member's repository name takes: two segments of
// letters, digits, dot, dash and underscore — one repository of a project, in
// the "owner/name" spelling a task's own Repo carries. It is the same shape
// internal/config's repoSlugRE gives the repository names of a provider's
// repositories and of the named accounts' allow-lists, stated here because the
// vocabulary lives in this package.
var memberRepoRE = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// hasDotSegment reports whether a repository name carries a "." or ".."
// segment: a name the grammar admits but which reads as a path step, and one
// no repository is called. internal/config's hasDotSegment is the same rule for
// the same reason.
func hasDotSegment(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
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
