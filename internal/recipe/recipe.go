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
	"strconv"
	"strings"

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

// ModeImplement and ModeReview are the two values of Recipe.Mode.
const (
	ModeImplement = "implement"
	ModeReview    = "review"
)

// NameRE is a recipe name: the shape of a project name.
var NameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// StepKind is a step type of version 1.
type StepKind string

const (
	StepFirstLine StepKind = "first_line"
	StepReview    StepKind = "review"
	StepCheck     StepKind = "check"
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
	// Bounce is review: {bounce: first_line}: a senior rejection runs the
	// cheap loop again instead of ending the run.
	Bounce bool
	// Command is a check step's commands.* key.
	Command CheckCommand
	// Autofix is a check step's autofix: true (lint only): commands.fix runs
	// before commands.lint.
	Autofix bool
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
	// Mode is ModeImplement or ModeReview, never "".
	Mode  string
	Steps []Step
}

// Renamed are catalog names removed in 0.7.0 and what replaced them.
var Renamed = map[string]string{"cheap-loop-senior": "standard", "claude-solo": "solo"}

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
				if !NameRE.MatchString(s) {
					p.add("name", v.Line, "must be a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
				}
				r.Name = s
			}
		case "description":
			if s, ok := p.str("description", v); ok {
				if len(s) > MaxDescription {
					p.add("description", v.Line, "must be at most %d bytes", MaxDescription)
				}
				r.Description = s
			}
		case "use_when":
			if s, ok := p.str("use_when", v); ok {
				if len(s) > MaxUseWhen {
					p.add("use_when", v.Line, "must be at most %d bytes", MaxUseWhen)
				}
				r.UseWhen = s
			}
		case "mode":
			if s, ok := p.str("mode", v); ok {
				r.Mode = s
			}
		case "roles":
			r.ReviewerIsCoder, r.CoderIsReviewer = p.roles(v)
		case "steps":
			r.Steps, srcIdx = p.steps(v)
		default:
			p.key("", k)
		}
	}
	for _, req := range []string{"version", "name", "steps"} {
		if !seen[req] {
			p.add(req, 0, "is required")
		}
	}
	switch r.Mode {
	case "":
		r.Mode = ModeImplement
	case ModeImplement, ModeReview:
	default:
		p.add("mode", 0, "mode must be implement or review")
	}
	if seen["steps"] {
		p.order(r, srcIdx)
	}
	return r
}

// roles returns (reviewerIsCoder, coderIsReviewer): which of the two
// exclusive role mappings the recipe set.
func (p *parser) roles(v *yaml.Node) (revIsCoder, coderIsRev bool) {
	if v.Kind != yaml.MappingNode {
		p.add("roles", v.Line, "must be a mapping such as {reviewer: coder}")
		return false, false
	}
	want := map[string]string{"reviewer": "coder", "coder": "reviewer"}
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		path := "roles." + pluginwire.Printable(k.Value)
		w := want[k.Value]
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
func (p *parser) steps(v *yaml.Node) ([]Step, []int) {
	if v.Kind != yaml.SequenceNode {
		p.add("steps", v.Line, "must be a list of steps")
		return nil, nil
	}
	var out []Step
	var idx []int
	for i, item := range v.Content {
		path := fmt.Sprintf("steps[%d]", i)
		if item.Kind != yaml.MappingNode || len(item.Content) != 2 {
			p.add(path, item.Line, "must be one step: first_line: {...}, check: {...} or review: {...}")
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
		case string(StepCheck):
			kind = StepCheck
		case "checks":
			p.add(kpath, k.Line, "the step type is check, not checks")
			continue
		default:
			if msg, ok := reserved[k.Value]; ok {
				p.add(kpath, k.Line, "%s", msg)
			} else {
				p.add(kpath, k.Line, "is not a step type (version 1 has first_line, check and review)")
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
				case kind == StepReview && bk.Value == "bounce":
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

// checkBody parses a check step's body: command (required: build, test or
// lint) and autofix (bool, lint only).
func (p *parser) checkBody(kpath string, body *yaml.Node, s *Step) {
	if body.Kind != yaml.MappingNode {
		p.add(kpath, body.Line, "must be a mapping such as {command: test}")
		return
	}
	seenCommand := false
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
	if s.Autofix && s.Command != CheckLint {
		p.add(kpath+".autofix", 0, "autofix is only for command: lint")
	}
}

// order checks the sequence of the valid steps; src[i] is steps[i]'s position
// in the source list, so messages point at the line the author wrote.
func (p *parser) order(r *Recipe, src []int) {
	steps := r.Steps
	checks, firsts, reviews := 0, 0, 0
	sawOther, firstSeen := false, false
	for i, s := range steps {
		path := fmt.Sprintf("steps[%d]", src[i])
		switch s.Kind {
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
		}
	}
	switch {
	case reviews == 0:
		p.add("steps", 0, "must end with a review step: readiness needs the senior review's ship verdict")
	case steps[len(steps)-1].Kind != StepReview:
		p.add("steps", 0, "review must be the last step")
	}
	if r.Mode == ModeReview {
		if checks > 0 || firsts > 0 || reviews != 1 {
			p.add("steps", 0, "mode: review allows exactly one review step and nothing else")
		} else if steps[len(steps)-1].MaxRounds > 1 {
			p.add("steps", 0, "mode: review reviews once: max_rounds must be 1")
		}
	}
}
