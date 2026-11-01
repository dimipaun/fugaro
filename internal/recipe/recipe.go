// Package recipe is the format of a Fugaro recipe: the named, selectable
// task loop that follows the implement stage (docs/design/recipes.md). It is
// pure: no I/O, no config, no runner. A recipe is untrusted input wherever it
// comes from, so Parse refuses, never repairs.
package recipe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"gopkg.in/yaml.v3"
)

const (
	Version            = 1
	MaxBytes           = 16 << 10
	MaxFirstLineRounds = 3
	MaxReviewRounds    = 10
	// MaxDescription is the longest description, counted in bytes (not
	// characters), so a multi-byte description has fewer characters.
	MaxDescription = 200
	// MaxUseWhen is the longest use_when text, counted in bytes.
	MaxUseWhen   = 300
	DefaultName  = "default"
	RepoDir      = ".fugaro/recipes"
	ObjectPrefix = "fugaro/recipes/"
)

// The values of Recipe.Mode. ModeArtifact is 0.8.0's third mode: a run whose
// deliverable is a file set rather than a pull request (design
// artifact-runs.md §3), with no git token in the agent's environment, one
// optional plan stage, one render step and a top-level outputs: block.
const (
	ModeImplement = "implement"
	ModeReview    = "review"
	ModeArtifact  = "artifact"
)

// modeMsg is the mode refusal. It names all three values, so a 0.7 recipe
// that wrote one of the two it knew still gets a message it can act on.
const modeMsg = "must be implement, review or artifact"

// NameRE is a recipe name: the shape of a project name.
var NameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// StepKind is a step type of version 1.
type StepKind string

const (
	StepFirstLine StepKind = "first_line"
	StepReview    StepKind = "review"
	// StepAdversarial is review level 3 (D720): a second, harder review after
	// the senior one, run in a fresh session, at most once and only after the
	// review step. Its bounce is the same single target the review step's is:
	// a rejection runs the fix and then another first-line loop before the
	// next adversarial round.
	StepAdversarial StepKind = "adversarial"
	StepCheck       StepKind = "check"
	// StepDigest is the report run's deterministic step (T3.1): the runner
	// writes the run reports the repository is missing and commits them, so
	// a recipe may hold it only as the first step of a mode: implement
	// recipe, at most once, and only with the review step after it.
	StepDigest StepKind = "digest"
	// StepPlan is the optional agent stage a mode: artifact recipe may begin
	// with: it writes a render plan, and nothing else (design
	// artifact-runs.md §6).
	StepPlan StepKind = "plan"
	// StepRender is the deterministic render: always the last step of a
	// mode: artifact recipe, and never a step of the other two modes.
	StepRender StepKind = "render"
)

// CheckCommand is the commands.* key a check step runs.
type CheckCommand string

const (
	CheckBuild CheckCommand = "build"
	CheckTest  CheckCommand = "test"
	CheckLint  CheckCommand = "lint"
)

// Source is the layer a recipe was found in.
type Source string

const (
	SourceRepo    Source = "repo"
	SourceProject Source = "project"
	SourceCatalog Source = "catalog"
)

// Step is one step after implement. MaxRounds 0 means the agent.* value.
type Step struct {
	Kind      StepKind
	MaxRounds int
	// Bounce is review: {bounce: first_line} or adversarial: {bounce:
	// first_line}: a rejection of either runs the cheap loop again instead of
	// ending the run (or, for the adversarial step, instead of going straight
	// to the next adversarial round).
	Bounce bool
	// Command is a check step's commands.* key.
	Command CheckCommand
	// Autofix is a check step's autofix: true (lint only): commands.fix runs
	// before commands.lint.
	Autofix bool
}

// The ceilings and defaults of a mode: artifact recipe's outputs: block
// (design artifact-runs.md §5.2, P6). MaxFiles, MaxBytes (per artifact) and
// MaxTotalBytes (the whole set) are what a recipe may ask for; the
// defaults are what an absent key means, and Parse refuses a written cap
// below 1 or above its ceiling rather than clamping it. The whole set is
// the number that matters: on Cloud Run the container's filesystem is
// memory, so the runner stages a second copy of the set beside the
// render's originals and every upload reads from the staging directory.
const (
	MaxOutputFiles      = 32
	MaxOutputBytes      = int64(2) << 30
	MaxOutputTotalBytes = int64(16) << 30

	DefaultOutputFiles      = 8
	DefaultOutputBytes      = int64(256) << 20
	DefaultOutputTotalBytes = int64(2) << 30
)

// Probe is a mode: artifact recipe's optional check on what it rendered:
// outputs.probe names one of these.
const (
	// ProbeCommand is commands.probe, the workflow's own one command.
	ProbeCommand = "probe"
	// ProbeNone is the default: the render is uploaded unchecked.
	ProbeNone = "none"
)

// Outputs is a mode: artifact recipe's declaration of what it delivers, from
// the recipe's top-level outputs: block. Recipe.Outputs is non-nil exactly
// when Recipe.Mode is ModeArtifact.
type Outputs struct {
	// Globs are the artifact paths to upload, relative to the checkout
	// root: never absolute, never holding a ".." path element.
	Globs []string
	// MaxFiles, MaxBytes and MaxTotalBytes are the caps as written. 0 means
	// the key was absent, and the accessors below then give the default:
	// MaxBytes is one artifact's cap, MaxTotalBytes the whole set's.
	MaxFiles      int
	MaxBytes      int64
	MaxTotalBytes int64
	// Probe is "probe" or "none", never "": an absent key parses as
	// ProbeNone, so a recipe never silently probes.
	Probe string
}

// FileCount is the most artifacts the run may upload: MaxFiles, or
// DefaultOutputFiles when the block did not write it.
func (o *Outputs) FileCount() int {
	if o == nil || o.MaxFiles <= 0 {
		return DefaultOutputFiles
	}
	return o.MaxFiles
}

// MaxFileBytes is the most one artifact may hold: MaxBytes, or
// DefaultOutputBytes when the block did not write it.
func (o *Outputs) MaxFileBytes() int64 {
	if o == nil || o.MaxBytes <= 0 {
		return DefaultOutputBytes
	}
	return o.MaxBytes
}

// MaxSetBytes is the most the whole set may hold: MaxTotalBytes, or
// DefaultOutputTotalBytes when the block did not write it. This is the
// number the memory rule doubles (gate.artifactGateProblems).
func (o *Outputs) MaxSetBytes() int64 {
	if o == nil || o.MaxTotalBytes <= 0 {
		return DefaultOutputTotalBytes
	}
	return o.MaxTotalBytes
}

// Recipe is a parsed, valid recipe.
type Recipe struct {
	Version     int
	Name        string
	Description string
	// UseWhen is the routing skill's self-description, at most MaxUseWhen
	// bytes.
	UseWhen string
	// ReviewerIsCoder is roles: {reviewer: coder}.
	ReviewerIsCoder bool
	// CoderIsReviewer is roles: {coder: reviewer}.
	CoderIsReviewer bool
	// Mode is ModeImplement, ModeReview or ModeArtifact, never "".
	Mode  string
	Steps []Step
	// Outputs is the recipe's outputs: block, non-nil exactly when Mode is
	// ModeArtifact (Parse refuses the block in the other two modes, and
	// refuses a mode: artifact recipe without one).
	Outputs *Outputs
	// newKeys is set when the recipe text uses a key or construct added in
	// 0.7.0 (use_when, mode, roles.coder, a check step, review.bounce),
	// regardless of the value given: a 0.6 parser refuses every one of
	// these keys outright ("is not a recipe key"), even one written with a
	// value that looks like a no-op (mode: implement, use_when: ""), so
	// UsesV07 must decide from presence, never from the parsed value.
	newKeys bool
	// newKeysV08 is the same rule for 0.8.0's constructs: mode: artifact,
	// an outputs: block, a plan or render step. A 0.7 parser refuses all
	// three (mode: "must be implement or review", outputs: "is not a
	// recipe key", the step kinds "is not a step type"), so UsesV08 decides
	// from presence too: the block holding nothing but its globs still
	// counts.
	newKeysV08 bool
}

// UsesV08 reports whether r uses a construct added in 0.8.0: mode:
// artifact, an outputs: block, or a plan or render step. It decides by
// presence (Recipe.newKeysV08), not by the parsed value, for the same
// reason UsesV07 does: a 0.7 parser refuses all three outright, so a mode:
// artifact recipe whose outputs block holds nothing but its globs still
// needs a 0.8 runner. A recipe that uses a 0.8.0 construct always uses a
// 0.7.0 one too (mode is one), so UsesV08 implies UsesV07; the two are
// separate gates because the images they need are tagged separately.
//
// It is read by the launch gate P9 asks for (artifact-runs plan Task 2):
// checkRecipeImage and checkCloneRecipeImage choose artifactsSince beside
// UsesV07 (internal/cli/recipes_skew.go), and the launcher's own spec-mode
// gate judges a task carrying mode at the same constant
// (internal/cli/run.go). So an artifact recipe launched at a job image whose
// runner predates 0.8.0 is refused before it starts, naming fugaro image
// refresh, rather than reaching the runner's own mode cross-check.
func UsesV08(r *Recipe) bool {
	return r.newKeysV08
}

// UsesDigest reports whether r holds the digest step, the report run's own
// step (run-digest, T3.1). It reads the parsed steps rather than a
// parse-time flag, unlike UsesV07 and UsesV08: a parser that predates the
// step refuses the whole recipe ("is not a step type") instead of dropping
// the key, so no recipe that parses without the step can have carried one.
//
// It is read by the launch gate: checkRecipeImage and
// checkCloneRecipeImage choose digestSince when the embedded recipe holds
// the step (internal/cli/recipes_skew.go), so a digest launch against a job
// image whose runner predates the release that executes it is refused
// before it starts, naming fugaro image refresh, rather than reaching the
// runner's own refusal (internal/runner/runner.go, right after
// resolveRecipe) — which is the backstop for the launches the launcher
// cannot judge, a development base image among them.
func UsesDigest(r *Recipe) bool {
	return slices.ContainsFunc(r.Steps, func(s Step) bool { return s.Kind == StepDigest })
}

// UsesAdversarial reports whether r holds the adversarial step, review level
// 3. It reads the parsed steps rather than a parse-time flag, for the same
// reason UsesDigest does: a parser that predates the step refuses the whole
// recipe ("is not a step type") instead of dropping the key, so no recipe
// that parses without the step can have carried one.
//
// It is read by the launch gate: recipeSinceFor chooses adversarialSince when
// the embedded recipe holds the step (internal/cli/recipes_skew.go), so an
// adversarial launch against a job image whose runner predates the release
// that acts on it is refused before it starts, naming fugaro image refresh,
// rather than reaching the runner's own parse refusal at bootstrap. Unlike
// the digest step there is no task field behind it, so the gate has nothing
// else to judge a recipe the launcher cannot read (a repository-sourced one).
func UsesAdversarial(r *Recipe) bool {
	return slices.ContainsFunc(r.Steps, func(s Step) bool { return s.Kind == StepAdversarial })
}

// Renamed are catalog names removed in 0.7.0 and what replaced them.
var Renamed = map[string]string{"cheap-loop-senior": "standard", "claude-solo": "solo"}

// reviewPRCounts is how many --pr the catalog's two mode: review recipes
// need: review-only reviews exactly one pull request and has no "keep"
// verdict to give; pick compares 2 to 4 and always gives one. A custom
// mode: review recipe (any other name) is unconstrained beyond the 1-to-4
// range --pr itself already enforces.
var reviewPRCounts = map[string][2]int{
	"review-only": {1, 1},
	"pick":        {2, 4},
}

// ReviewPRRange reports the number of --pr a named catalog mode: review
// recipe requires, min and max inclusive. ok is false for any other name:
// a custom mode: review recipe is unconstrained, and a project or agent
// recipe that merely reuses "review-only" or "pick" as its own name (rare,
// but resolveRecipe lets it shadow the catalog one) is refused by this
// check exactly like the catalog recipe it is named after would be, which
// is the conservative side to be wrong on.
func ReviewPRRange(name string) (min, max int, ok bool) {
	r, ok := reviewPRCounts[name]
	return r[0], r[1], ok
}

// Problem is one reason a recipe is invalid.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	switch {
	case p.Path != "" && p.Line > 0:
		return fmt.Sprintf("%s (line %d): %s", p.Path, p.Line, p.Message)
	case p.Path != "":
		return p.Path + ": " + p.Message
	case p.Line > 0:
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	}
	return p.Message
}

// ProblemsText joins problems for one error line.
func ProblemsText(ps []Problem) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return strings.Join(out, "; ")
}

// Sum is the lower-case hex sha256 of a recipe's exact bytes.
func Sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// RepoPath is a repository recipe's path in the checkout.
func RepoPath(name string) string { return RepoDir + "/" + name + ".yaml" }

// ObjectKey is a project recipe's object in the runs bucket.
func ObjectKey(name string) string { return ObjectPrefix + name + ".yaml" }

const modelMsg = "a recipe never names a model; models come from agent.models in fugaro.yaml"

// unsafeCharMsg is hasControlChar's refusal message, shared by every field
// it guards.
const unsafeCharMsg = "must not contain control characters, bidi overrides, invisible characters or line/paragraph separators"

// reserved are the keys version 1 refuses with a message of their own.
var reserved = map[string]string{
	"goto":      "goto is reserved for a later recipe version; version 1 refuses it",
	"on_reject": "on_reject is reserved for a later recipe version; version 1 refuses it",
	"extends":   "extends is reserved for a later recipe version; version 1 refuses it (copy the recipe instead)",
	"on_pass":   "on_pass is not configurable: a ready PR needs a verified passing test and a ship verdict",
	"on_fail":   "on_fail is not configurable: anything short of ready is a draft",
	"model":     modelMsg,
	"models":    modelMsg,
}

// Parse decodes and validates a recipe strictly.
func Parse(data []byte) (*Recipe, []Problem) {
	if len(data) > MaxBytes {
		return nil, []Problem{{Message: fmt.Sprintf("is %d bytes, over the %d byte (16 KiB) limit", len(data), MaxBytes)}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "is empty"}}
		}
		return nil, []Problem{{Message: "is not valid YAML: " + strconv.Quote(err.Error())}}
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, []Problem{{Message: "holds more than one YAML document"}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []Problem{{Message: "is not a YAML mapping"}}
	}
	if p := checkShape(&doc); p != nil {
		return nil, []Problem{*p}
	}
	p := &parser{}
	r := p.top(doc.Content[0])
	if len(p.ps) > 0 {
		return nil, p.ps
	}
	return r, nil
}

// checkShape refuses anchors, aliases, explicit tags, merge keys, non-scalar
// keys and repeated keys anywhere in the document.
func checkShape(n *yaml.Node) *Problem {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return &Problem{Line: n.Line, Message: "uses a YAML anchor or alias, which recipes refuse"}
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return &Problem{Line: n.Line, Message: fmt.Sprintf("uses an explicit YAML tag %q, which recipes refuse", n.Tag)}
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			switch {
			case k.Kind != yaml.ScalarNode:
				return &Problem{Line: k.Line, Message: "has a key that is not a plain value"}
			case k.Value == "<<" || k.Tag == "!!merge":
				return &Problem{Line: k.Line, Message: "uses a YAML merge key <<, which recipes refuse"}
			case seen[k.Value]:
				return &Problem{Line: k.Line, Message: fmt.Sprintf("repeats the key %q", k.Value)}
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if p := checkShape(c); p != nil {
			return p
		}
	}
	return nil
}

type parser struct{ ps []Problem }

func (p *parser) add(path string, line int, format string, args ...any) {
	p.ps = append(p.ps, Problem{Path: path, Line: line, Message: fmt.Sprintf(format, args...)})
}

// key reports an unexpected key k under prefix ("" for the top level). k.Value
// comes from an untrusted recipe, so it is made terminal-safe before it joins
// Problem.Path (same rule pluginwire applies to untrusted repository text).
func (p *parser) key(prefix string, k *yaml.Node) {
	path := pluginwire.Printable(k.Value)
	if prefix != "" {
		path = prefix + "." + path
	}
	if msg, ok := reserved[k.Value]; ok {
		p.add(path, k.Line, "%s", msg)
		return
	}
	p.add(path, k.Line, "is not a recipe key")
}

func (p *parser) int(path string, v *yaml.Node) (int, bool) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!int" {
		p.add(path, v.Line, "must be a whole number")
		return 0, false
	}
	n, err := strconv.Atoi(v.Value)
	if err != nil {
		p.add(path, v.Line, "must be a whole number")
		return 0, false
	}
	return n, true
}

func (p *parser) str(path string, v *yaml.Node) (string, bool) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
		p.add(path, v.Line, "must be a string")
		return "", false
	}
	return v.Value, true
}

// bool accepts only the literal YAML scalars true and false. YAML 1.1 also
// resolves True, TRUE, False and FALSE to tag !!bool, but a recipe is
// untrusted input that Parse refuses rather than repairs, so those spellings
// are refused rather than silently reinterpreted.
func (p *parser) bool(path string, v *yaml.Node) (bool, bool) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" || (v.Value != "true" && v.Value != "false") {
		p.add(path, v.Line, "must be true or false")
		return false, false
	}
	return v.Value == "true", true
}

func (p *parser) top(m *yaml.Node) *Recipe {
	r := &Recipe{}
	seen := map[string]bool{}
	seenOutputs := false
	var srcIdx []int
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		seen[k.Value] = true
		switch k.Value {
		case "version":
			if n, ok := p.int("version", v); ok {
				if n != Version {
					p.add("version", v.Line, "must be 1")
				}
				r.Version = n
			}
		case "name":
			if s, ok := p.str("name", v); ok {
				switch {
				case hasControlChar(s):
					p.add("name", v.Line, unsafeCharMsg)
				case !NameRE.MatchString(s):
					p.add("name", v.Line, "must be a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
				}
				r.Name = s
			}
		case "description":
			if s, ok := p.str("description", v); ok {
				switch {
				case hasControlChar(s):
					p.add("description", v.Line, unsafeCharMsg)
				case len(s) > MaxDescription:
					p.add("description", v.Line, "must be at most %d bytes", MaxDescription)
				}
				r.Description = s
			}
		case "use_when":
			if s, ok := p.str("use_when", v); ok {
				switch {
				case hasControlChar(s):
					p.add("use_when", v.Line, unsafeCharMsg)
				case len(s) > MaxUseWhen:
					p.add("use_when", v.Line, "must be at most %d bytes", MaxUseWhen)
				}
				r.UseWhen = s
			}
		case "mode":
			if s, ok := p.str("mode", v); ok {
				r.Mode = s
				switch s {
				case ModeImplement, ModeReview:
				case ModeArtifact:
					r.newKeysV08 = true
				default:
					p.add("mode", v.Line, "%s", modeMsg)
				}
			}
		case "outputs":
			seenOutputs = true
			r.newKeysV08 = true
			r.Outputs = p.outputs(v)
		case "roles":
			r.ReviewerIsCoder, r.CoderIsReviewer = p.roles(r, v)
		case "steps":
			r.Steps, srcIdx = p.steps(r, v)
		default:
			p.key("", k)
		}
	}
	for _, req := range []string{"version", "name", "steps"} {
		if !seen[req] {
			p.add(req, 0, "is required")
		}
	}
	if r.Mode == "" {
		r.Mode = ModeImplement
	}
	// The mode's own rules are checked here and not while its key is read:
	// a recipe may write mode after the keys it governs (steps, roles,
	// outputs), so at the end of the loop is the first point every one of
	// them is known.
	switch {
	case r.Mode == ModeArtifact:
		if !seenOutputs {
			p.add("outputs", 0, "is required for mode: artifact")
		}
		// roles map the coder onto the reviewer and back; an artifact
		// recipe has neither role. Checked here rather than in roles(),
		// for the same reason as the step rules: mode may come after it.
		if r.ReviewerIsCoder || r.CoderIsReviewer {
			p.add("roles", 0, "mode: artifact has no agent steps to map roles onto: remove roles")
		}
	case seenOutputs:
		p.add("outputs", 0, "is only for mode: artifact")
	}
	if seen["use_when"] || seen["mode"] {
		r.newKeys = true
	}
	if seen["steps"] {
		p.order(r, srcIdx)
	}
	return r
}

// hasControlChar reports whether s holds an unsafe display character: a C0
// control (tab, newline and friends) or DEL, a C1 control (U+0080 to
// U+009F), a Unicode format character (category Cf: bidi overrides such as
// U+202E, zero-width characters and the like), or a line or paragraph
// separator (U+2028, U+2029). name, description and use_when are shown
// verbatim by `recipes ls`, its `--json` and the routing skill, and a
// project recipe comes from the runs bucket, so every one of them is
// untrusted text that must never carry one: a bidi override can make a
// reader see different text than what is stored, and the rest can corrupt
// a terminal or break a line-oriented renderer. gopkg.in/yaml.v3's JSON
// encoder escapes U+2028/U+2029 but nothing else here, and this refusal
// comes first (recipes ls --json's own escaping, pluginwire.Printable, is a
// second, independent line of defense for text this never lets through).
func hasControlChar(s string) bool {
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f: // C0 and DEL
			return true
		case r >= 0x80 && r <= 0x9f: // C1
			return true
		case r == '\u2028' || r == '\u2029': // line/paragraph separator
			return true
		case unicode.Is(unicode.Cf, r): // bidi overrides, zero-width, etc.
			return true
		}
	}
	return false
}

// roles returns (reviewerIsCoder, coderIsReviewer): which of the two
// exclusive role mappings the recipe set.
func (p *parser) roles(r *Recipe, v *yaml.Node) (revIsCoder, coderIsRev bool) {
	if v.Kind != yaml.MappingNode {
		p.add("roles", v.Line, "must be a mapping such as {reviewer: coder}")
		return false, false
	}
	want := map[string]string{"reviewer": "coder", "coder": "reviewer"}
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		path := "roles." + pluginwire.Printable(k.Value)
		w := want[k.Value]
		if k.Value == "coder" {
			r.newKeys = true
		}
		switch {
		case w == "":
			p.add(path, k.Line, "only the reviewer and coder roles can be mapped (reviewer: coder, or coder: reviewer)")
		case val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value == w:
			if k.Value == "reviewer" {
				revIsCoder = true
			} else {
				coderIsRev = true
			}
		case val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value != "" && val.Value != "coder" && val.Value != "reviewer" && val.Value != "background":
			p.add(path, val.Line, "can only be %s: %s", w, modelMsg)
		default:
			p.add(path, val.Line, "must be %s", w)
		}
	}
	if revIsCoder && coderIsRev {
		p.add("roles", v.Line, "roles: coder: reviewer and reviewer: coder exclude each other")
	}
	return revIsCoder, coderIsRev
}

// steps returns the valid steps and each one's position in the source list.
func (p *parser) steps(r *Recipe, v *yaml.Node) ([]Step, []int) {
	if v.Kind != yaml.SequenceNode {
		p.add("steps", v.Line, "must be a list of steps")
		return nil, nil
	}
	var out []Step
	var idx []int
	for i, item := range v.Content {
		path := fmt.Sprintf("steps[%d]", i)
		if item.Kind != yaml.MappingNode || len(item.Content) != 2 {
			p.add(path, item.Line, "must be one step: first_line: {...}, review: {...}, adversarial: {...}, check: {...}, digest: {...}, plan: {...} or render: {...}")
			continue
		}
		k, body := item.Content[0], item.Content[1]
		kpath := path + "." + pluginwire.Printable(k.Value)
		var kind StepKind
		switch k.Value {
		case string(StepFirstLine):
			kind = StepFirstLine
		case string(StepReview):
			kind = StepReview
		case string(StepAdversarial):
			kind = StepAdversarial
		case string(StepCheck):
			kind = StepCheck
			r.newKeys = true
		case string(StepPlan):
			kind = StepPlan
			r.newKeysV08 = true
		case string(StepRender):
			kind = StepRender
			r.newKeysV08 = true
		case string(StepDigest):
			kind = StepDigest
		case "checks":
			p.add(kpath, k.Line, "the step type is check, not checks")
			continue
		default:
			if msg, ok := reserved[k.Value]; ok {
				p.add(kpath, k.Line, "%s", msg)
			} else {
				p.add(kpath, k.Line, "is not a step type (version 1 has first_line, review, adversarial, check, digest, plan and render)")
			}
			continue
		}
		s := Step{Kind: kind}
		if kind == StepCheck {
			p.checkBody(kpath, body, &s)
			out = append(out, s)
			idx = append(idx, i)
			continue
		}
		if kind == StepPlan || kind == StepRender || kind == StepDigest {
			p.emptyStepBody(kpath, body)
			out = append(out, s)
			idx = append(idx, i)
			continue
		}
		limit := MaxReviewRounds
		if kind == StepFirstLine {
			limit = MaxFirstLineRounds
		}
		switch {
		case body.Kind == yaml.ScalarNode && body.Tag == "!!null":
		case body.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(body.Content); j += 2 {
				bk, bv := body.Content[j], body.Content[j+1]
				switch {
				case bk.Value == "max_rounds":
					bp := kpath + ".max_rounds"
					if n, ok := p.int(bp, bv); ok {
						if n < 1 || n > limit {
							p.add(bp, bv.Line, "must be between 1 and %d", limit)
						} else {
							s.MaxRounds = n
						}
					}
				case (kind == StepReview || kind == StepAdversarial) && bk.Value == "bounce":
					if kind == StepReview {
						r.newKeys = true
					}
					bp := kpath + ".bounce"
					if bs, ok := p.str(bp, bv); ok {
						if bs != "first_line" {
							p.add(bp, bv.Line, "bounce can only be first_line")
						} else {
							s.Bounce = true
						}
					}
				default:
					p.key(kpath, bk)
				}
			}
		default:
			p.add(kpath, body.Line, "must be a mapping such as {max_rounds: 2}, or empty")
		}
		out = append(out, s)
		idx = append(idx, i)
	}
	return out, idx
}

// emptyStepBody parses a plan, render or digest step's body: it holds
// nothing. None of the three is a loop, so there is no max_rounds to set and
// no bounce (a digest step has no review before it to bounce back to), and
// any key here is refused as an unknown recipe key rather than silently
// ignored.
func (p *parser) emptyStepBody(kpath string, body *yaml.Node) {
	switch {
	case body.Kind == yaml.ScalarNode && body.Tag == "!!null":
	case body.Kind == yaml.MappingNode:
		for j := 0; j+1 < len(body.Content); j += 2 {
			p.key(kpath, body.Content[j])
		}
	default:
		p.add(kpath, body.Line, "must be a mapping such as {}, or empty")
	}
}

// outputs parses a mode: artifact recipe's top-level outputs: block. The
// block must name at least one glob, and a cap it writes must be between 1
// and its ceiling: 0 is not "the default" here, it is a cap no artifact can
// satisfy, and the defaults apply only to a key the block leaves out.
func (p *parser) outputs(v *yaml.Node) *Outputs {
	out := &Outputs{Probe: ProbeNone}
	if v.Kind != yaml.MappingNode {
		p.add("outputs", v.Line, "must be a mapping such as {globs: [out/*.png]}")
		return out
	}
	var sawGlobs, wroteFiles, wroteBytes, wroteTotal bool
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		switch k.Value {
		case "globs":
			sawGlobs = true
			out.Globs = p.globs(val)
		case "max_files":
			wroteFiles = true
			if n, ok := p.int("outputs.max_files", val); ok {
				if n < 1 || n > MaxOutputFiles {
					p.add("outputs.max_files", val.Line, "must be between 1 and %d", MaxOutputFiles)
				} else {
					out.MaxFiles = n
				}
			}
		case "max_bytes":
			wroteBytes = true
			if n, ok := p.int64("outputs.max_bytes", val); ok {
				if n < 1 || n > MaxOutputBytes {
					p.add("outputs.max_bytes", val.Line, "must be between 1 and %d bytes (%s)", MaxOutputBytes, SizeText(MaxOutputBytes))
				} else {
					out.MaxBytes = n
				}
			}
		case "max_total_bytes":
			wroteTotal = true
			if n, ok := p.int64("outputs.max_total_bytes", val); ok {
				if n < 1 || n > MaxOutputTotalBytes {
					p.add("outputs.max_total_bytes", val.Line, "must be between 1 and %d bytes (%s)", MaxOutputTotalBytes, SizeText(MaxOutputTotalBytes))
				} else {
					out.MaxTotalBytes = n
				}
			}
		case "probe":
			if s, ok := p.str("outputs.probe", val); ok {
				switch s {
				case ProbeCommand, ProbeNone:
					out.Probe = s
				default:
					p.add("outputs.probe", val.Line, "must be %s or %s", ProbeCommand, ProbeNone)
				}
			}
		default:
			p.key("outputs", k)
		}
	}
	if !sawGlobs {
		p.add("outputs.globs", 0, "is required")
	}
	// A set cap below the per-artifact cap with room for only one artifact
	// is a pair nobody can satisfy: that one legal artifact already breaks
	// the set, and the smaller cap would silently govern. Only the two caps
	// the block actually wrote are compared; a defaulted one is not a
	// relation the author wrote, and with room for more than one artifact
	// the pair is a set cap doing its job.
	if wroteFiles && wroteBytes && wroteTotal && out.MaxFiles == 1 && out.MaxTotalBytes < out.MaxBytes {
		p.add("outputs.max_total_bytes", 0, "must be at least outputs.max_bytes")
	}
	return out
}

// globs parses outputs.globs: the artifact paths to upload, relative to the
// checkout root. An absolute glob or one with a ".." path element would let
// a recipe name a file outside the run's own tree, which is exactly what an
// artifact must never do, so both are refused rather than cleaned up.
func (p *parser) globs(v *yaml.Node) []string {
	if v.Kind != yaml.SequenceNode {
		p.add("outputs.globs", v.Line, "must be a list of globs such as [\"out/*.png\"]")
		return nil
	}
	if len(v.Content) == 0 {
		p.add("outputs.globs", v.Line, "must name at least one glob")
		return nil
	}
	out := make([]string, 0, len(v.Content))
	for i, item := range v.Content {
		path := fmt.Sprintf("outputs.globs[%d]", i)
		s, ok := p.str(path, item)
		if !ok {
			continue
		}
		switch {
		case s == "":
			p.add(path, item.Line, "must not be empty")
		case strings.HasPrefix(s, "/"):
			p.add(path, item.Line, "must be relative to the checkout root")
		case slices.Contains(strings.Split(s, "/"), ".."):
			p.add(path, item.Line, "must not contain a .. path element")
		default:
			out = append(out, s)
		}
	}
	return out
}

// int64 is int, for a cap whose valid range does not fit an int32. The
// value is parsed the same way (a plain YAML integer, never a float or a
// string), so a cap of 2GiB+1 gets the same "must be a whole number"
// refusal a bad max_rounds gets.
func (p *parser) int64(path string, v *yaml.Node) (int64, bool) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!int" {
		p.add(path, v.Line, "must be a whole number")
		return 0, false
	}
	n, err := strconv.ParseInt(v.Value, 10, 64)
	if err != nil {
		p.add(path, v.Line, "must be a whole number")
		return 0, false
	}
	return n, true
}

// SizeText renders a byte count as GiB or MiB when it is a whole number of
// them, so a message about a cap or a memory limit can be read without
// arithmetic (the runner's memory rule and the inputs caps' refusals use
// the same shape).
func SizeText(n int64) string {
	const (
		gi = int64(1) << 30
		mi = int64(1) << 20
	)
	switch {
	case n >= gi && n%gi == 0:
		return fmt.Sprintf("%d GiB", n/gi)
	case n >= mi && n%mi == 0:
		return fmt.Sprintf("%d MiB", n/mi)
	}
	return fmt.Sprintf("%d bytes", n)
}

// checkBody parses a check step's body: command (required: build, test or
// lint) and autofix (bool, lint only).
func (p *parser) checkBody(kpath string, body *yaml.Node, s *Step) {
	if body.Kind != yaml.MappingNode {
		p.add(kpath, body.Line, "must be a mapping such as {command: test}")
		return
	}
	seenCommand := false
	autofixLine := 0
	for j := 0; j+1 < len(body.Content); j += 2 {
		bk, bv := body.Content[j], body.Content[j+1]
		switch bk.Value {
		case "command":
			seenCommand = true
			cp := kpath + ".command"
			if cs, ok := p.str(cp, bv); ok {
				switch CheckCommand(cs) {
				case CheckBuild, CheckTest, CheckLint:
					s.Command = CheckCommand(cs)
				default:
					p.add(cp, bv.Line, "must be build, test or lint (it names a commands.* key of fugaro.yaml; a recipe never holds a command)")
				}
			}
		case "autofix":
			autofixLine = bv.Line
			if b, ok := p.bool(kpath+".autofix", bv); ok {
				s.Autofix = b
			}
		default:
			p.key(kpath, bk)
		}
	}
	if !seenCommand {
		p.add(kpath+".command", 0, "is required")
	}
	// s.Command == "" means command was missing or already refused above
	// (the enum message), so this does not pile a second, confusing
	// problem onto the same bad command.
	if s.Autofix && s.Command != "" && s.Command != CheckLint {
		p.add(kpath+".autofix", autofixLine, "autofix is only for command: lint")
	}
}

// order checks the sequence of the valid steps; src[i] is steps[i]'s position
// in the source list, so messages point at the line the author wrote.
func (p *parser) order(r *Recipe, src []int) {
	if r.Mode == ModeArtifact {
		p.orderArtifact(r.Steps, src)
		return
	}
	steps := r.Steps
	checks, firsts, reviews, adversarials := 0, 0, 0, 0
	sawOther, firstSeen := false, false
	digests, digestAt := 0, -1
	for i, s := range steps {
		path := fmt.Sprintf("steps[%d]", src[i])
		switch s.Kind {
		case StepDigest:
			sawOther = true
			digests++
			digestAt = i
			if digests > 1 {
				p.add(path, 0, "digest may appear at most once")
			}
			if i != 0 {
				p.add(path, 0, "digest must be the first step")
			}
			if r.Mode != ModeImplement {
				p.add(path, 0, "digest is only for mode: implement")
			}
		case StepCheck:
			checks++
			if checks > 1 {
				p.add(path, 0, "check may appear at most once")
			}
			if sawOther {
				p.add(path, 0, "check steps must come first")
			}
		case StepFirstLine:
			sawOther = true
			firstSeen = true
			firsts++
			if firsts > 1 {
				p.add(path, 0, "first_line may appear at most once")
			}
			if reviews > 0 {
				p.add(path, 0, "first_line must come before review")
			}
		case StepReview:
			sawOther = true
			reviews++
			if reviews > 1 {
				p.add(path, 0, "review must appear exactly once")
			}
			if s.Bounce && !firstSeen {
				p.add(path, 0, "bounce: first_line needs a first_line step before the review")
			}
		case StepAdversarial:
			sawOther = true
			adversarials++
			if adversarials > 1 {
				p.add(path, 0, "adversarial may appear at most once")
			}
			if reviews == 0 {
				p.add(path, 0, "adversarial must come after review")
			}
			if s.Bounce && !firstSeen {
				p.add(path, 0, "bounce: first_line needs a first_line step before the adversarial step")
			}
		case StepPlan, StepRender:
			// Refused here rather than in steps(), which reads a step
			// before the recipe's mode is necessarily known.
			p.add(path, 0, "%s is only for mode: artifact", s.Kind)
		}
	}
	// The digest step writes the pull request the review then judges: a
	// first_line loop or a second review would be a loop over a report no
	// model wrote, so the shape is exactly digest then review.
	if digestAt >= 0 && (len(steps) != 2 || steps[1].Kind != StepReview) {
		p.add(fmt.Sprintf("steps[%d]", src[digestAt]), 0, "digest must be followed by one review step")
	}
	switch {
	case reviews == 0:
		p.add("steps", 0, "must end with a review step: readiness needs the senior review's ship verdict")
	case steps[len(steps)-1].Kind != StepReview && steps[len(steps)-1].Kind != StepAdversarial:
		p.add("steps", 0, "review must be the last step, or the adversarial step after it")
	}
	if r.Mode == ModeReview {
		// use_when is deliberately not restricted here: it is the routing
		// skill's description of when to pick the recipe, which a
		// review-only recipe needs exactly as much as any other, and it
		// names nothing to run.
		switch {
		case checks > 0 || firsts > 0 || adversarials > 0 || reviews != 1:
			p.add("steps", 0, "mode: review allows exactly one review step and nothing else")
		case r.ReviewerIsCoder || r.CoderIsReviewer:
			p.add("roles", 0, "mode: review has no coder step to map roles onto: remove roles")
		case steps[len(steps)-1].MaxRounds > 1:
			p.add("steps", 0, "mode: review reviews once: max_rounds must be 1")
		}
	}
}

// orderArtifact checks a mode: artifact recipe's steps: exactly one render,
// optionally led by one plan, and no agent step of the other two modes.
func (p *parser) orderArtifact(steps []Step, src []int) {
	for i, s := range steps {
		path := fmt.Sprintf("steps[%d]", src[i])
		switch s.Kind {
		case StepCheck, StepFirstLine, StepDigest, StepReview, StepAdversarial:
			// check included: an artifact recipe has no implement for a
			// gate to judge, no report to write and no review to give a
			// ship verdict.
			p.add(path+"."+string(s.Kind), 0, "%s is not a step of mode: artifact", s.Kind)
		}
	}
	if len(steps) == 1 && steps[0].Kind == StepRender {
		return
	}
	if len(steps) == 2 && steps[0].Kind == StepPlan && steps[1].Kind == StepRender {
		return
	}
	p.add("steps", 0, "mode: artifact takes one render step, optionally after one plan step")
}
