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
	DefaultName    = "default"
	RepoDir        = ".fugaro/recipes"
	ObjectPrefix   = "fugaro/recipes/"
)

// NameRE is a recipe name: the shape of a project name.
var NameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// StepKind is a step type of version 1.
type StepKind string

const (
	StepFirstLine StepKind = "first_line"
	StepReview    StepKind = "review"
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
}

// Recipe is a parsed, valid recipe.
type Recipe struct {
	Version     int
	Name        string
	Description string
	// ReviewerIsCoder is roles: {reviewer: coder}.
	ReviewerIsCoder bool
	Steps           []Step
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

// reserved are the keys version 1 refuses with a message of their own.
var reserved = map[string]string{
	"checks":    "check steps are reserved for a later recipe version; version 1 refuses them",
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

// key reports an unexpected key k under prefix ("" for the top level).
func (p *parser) key(prefix string, k *yaml.Node) {
	path := k.Value
	if prefix != "" {
		path = prefix + "." + k.Value
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
		case "roles":
			r.ReviewerIsCoder = p.roles(v)
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
	if seen["steps"] {
		p.order(r.Steps, srcIdx)
	}
	return r
}

func (p *parser) roles(v *yaml.Node) bool {
	if v.Kind != yaml.MappingNode {
		p.add("roles", v.Line, "must be a mapping such as {reviewer: coder}")
		return false
	}
	alias := false
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		path := "roles." + k.Value
		switch {
		case k.Value != "reviewer":
			p.add(path, k.Line, "only the reviewer role can be mapped in recipe version 1 (reviewer: coder)")
		case val.Kind == yaml.ScalarNode && val.Value == "coder":
			alias = true
		case val.Kind == yaml.ScalarNode && (val.Value == "reviewer" || val.Value == "background"):
			p.add(path, val.Line, "can only be coder in recipe version 1")
		case val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value != "":
			p.add(path, val.Line, "can only be coder: %s", modelMsg)
		default:
			p.add(path, val.Line, "must be a string; the only allowed value is coder")
		}
	}
	return alias
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
			p.add(path, item.Line, "must be one step: first_line: {...} or review: {...}")
			continue
		}
		k, body := item.Content[0], item.Content[1]
		kpath := path + "." + k.Value
		var kind StepKind
		switch k.Value {
		case string(StepFirstLine):
			kind = StepFirstLine
		case string(StepReview):
			kind = StepReview
		default:
			if msg, ok := reserved[k.Value]; ok {
				p.add(kpath, k.Line, "%s", msg)
			} else {
				p.add(kpath, k.Line, "is not a step type (version 1 has first_line and review)")
			}
			continue
		}
		s := Step{Kind: kind}
		limit := MaxReviewRounds
		if kind == StepFirstLine {
			limit = MaxFirstLineRounds
		}
		switch {
		case body.Kind == yaml.ScalarNode && body.Tag == "!!null":
		case body.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(body.Content); j += 2 {
				bk, bv := body.Content[j], body.Content[j+1]
				if bk.Value != "max_rounds" {
					p.key(kpath, bk)
					continue
				}
				bp := kpath + ".max_rounds"
				if n, ok := p.int(bp, bv); ok {
					if n < 1 || n > limit {
						p.add(bp, bv.Line, "must be between 1 and %d", limit)
					} else {
						s.MaxRounds = n
					}
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

// order checks the sequence of the valid steps; src[i] is steps[i]'s position
// in the source list, so messages point at the line the author wrote.
func (p *parser) order(steps []Step, src []int) {
	firsts, reviews := 0, 0
	for i, s := range steps {
		path := fmt.Sprintf("steps[%d]", src[i])
		switch s.Kind {
		case StepFirstLine:
			firsts++
			if firsts > 1 {
				p.add(path, 0, "first_line may appear at most once")
			}
			if reviews > 0 {
				p.add(path, 0, "first_line must come before review")
			}
		case StepReview:
			reviews++
			if reviews > 1 {
				p.add(path, 0, "review must appear exactly once")
			}
		}
	}
	switch {
	case reviews == 0:
		p.add("steps", 0, "must end with a review step: readiness needs the senior review's ship verdict")
	case steps[len(steps)-1].Kind != StepReview:
		p.add("steps", 0, "review must be the last step")
	}
}
