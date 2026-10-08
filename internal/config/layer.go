package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// The project layer (docs/design/layered-config.md): one object per project
// in the runs bucket, published by fugaro config publish, holding the
// defaults and the workflow profiles of every repository of the project.
const (
	// LayerKey is the project layer's object in the runs bucket.
	LayerKey = "fugaro/project-layer.yaml"
	// LayerMaxBytes caps the object, as the shared config is capped.
	LayerMaxBytes = 64 << 10
	// ImplicitWorkflow is the workflow a fugaro.yaml with no workflows:
	// gets from its profile.
	ImplicitWorkflow = "default"
	// maxProfileDescription bounds a profile's description.
	maxProfileDescription = 200
)

// LayerCopyKey is the per-repository copy of the project layer, under the
// build account's own prefix, which the daily image check and Cloud Build
// read (decision L4).
func LayerCopyKey(slug string) string { return "builds/" + slug + "/project-layer.yaml" }

// LayerAnchor is what a project layer must name: the project and GCP
// project of the repository (or local config) it is read for. An empty
// field is not checked.
type LayerAnchor struct{ Project, GCPProject string }

// ProjectLayer is a parsed, validated project layer.
type ProjectLayer struct {
	Version        int                `yaml:"version"`
	Project        string             `yaml:"project"`
	GCPProject     string             `yaml:"gcp_project"`
	Defaults       LayerDefaults      `yaml:"defaults"`
	Profiles       map[string]Profile `yaml:"profiles"`
	DefaultProfile string             `yaml:"default_profile"`

	// Raw is the exact text, SHA256 its hex sha256.
	Raw    []byte `yaml:"-"`
	SHA256 string `yaml:"-"`
	// tree is the text as plain maps, which Resolve merges.
	tree map[string]any
}

// LayerDefaults are the project-wide values of the repository keys the
// project layer may set (scope project).
type LayerDefaults struct {
	Git   LayerGit   `yaml:"git"`
	Agent LayerAgent `yaml:"agent"`
}

// LayerGit is defaults.git.
type LayerGit struct {
	Provider string  `yaml:"provider"`
	PR       LayerPR `yaml:"pr"`
}

// LayerPR is defaults.git.pr.
type LayerPR struct {
	Labels      []string `yaml:"labels"`
	EarlyDraft  *bool    `yaml:"early_draft"`
	Checkpoints *bool    `yaml:"checkpoints"`
}

// LayerAgent is defaults.agent.
type LayerAgent struct {
	Auth            string     `yaml:"auth"`
	Model           string     `yaml:"model"`
	Models          ModelRoles `yaml:"models"`
	ReviewRounds    int        `yaml:"review_rounds"`
	MaxBudgetUSD    float64    `yaml:"max_budget_usd"`
	FirstLineReview string     `yaml:"first_line_review"`
	FirstLineRounds int        `yaml:"first_line_rounds"`
	Recipe          string     `yaml:"recipe"`
}

// Profile is a named workflow template (scope profile).
type Profile struct {
	Description string       `yaml:"description"`
	Base        string       `yaml:"base"`
	Image       Image        `yaml:"image"`
	Commands    Commands     `yaml:"commands"`
	Cache       []CacheEntry `yaml:"cache"`
	Resources   Resources    `yaml:"resources"`
	Timeouts    Timeouts     `yaml:"timeouts"`
	Rebuild     Rebuild      `yaml:"rebuild"`
}

// HasExecutable reports whether the profile sets a key that runs as shell
// (ExecutableKeys).
func (p Profile) HasExecutable() bool {
	return p.Commands.Build != "" || p.Commands.Test != "" || p.Commands.RerunFailed != nil || len(p.Image.Apt) > 0 || len(p.Image.Setup) > 0
}

// LayerSum is the hex sha256 of a project layer's text.
func LayerSum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

var layerTopKeys = []string{"version", "project", "gcp_project", "defaults", "profiles", "default_profile"}

// layerReserved are keys a later release defines; this one refuses them
// with a message saying so.
var layerReserved = map[string]string{
	"environments": "the image catalog (named environments) arrives with the single base image (docs/design/layered-config.md, Phase 2); this release refuses it",
	"extends":      "profiles do not extend each other in this release",
}

// tokenRE matches values shaped like a credential. Nothing in the project
// layer is a secret, so one is refused rather than published.
var tokenRE = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|gh[osu]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|ATBB[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// ParseProjectLayer parses and validates a project layer. Anyone holding
// objectAdmin on the runs bucket can write it, so, like the shared config,
// it refuses (never repairs) anything off: over LayerMaxBytes, more than
// one document, anchors, aliases, merge keys, tags, repeated keys, unknown
// or reserved keys, a key outside its scope, a credential-shaped value, a
// project or GCP project other than a's, and invalid values.
func ParseProjectLayer(data []byte, a LayerAnchor) (*ProjectLayer, []Problem) {
	if len(data) > LayerMaxBytes {
		return nil, []Problem{{Message: fmt.Sprintf("is over the %d KiB limit", LayerMaxBytes>>10)}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "is empty"}}
		}
		return nil, yamlProblems(err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, []Problem{{Message: "holds more than one YAML document"}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []Problem{{Message: "is not a YAML mapping"}}
	}
	if p := layerShape(doc.Content[0]); p != nil {
		return nil, []Problem{*p}
	}
	var tree map[string]any
	if err := doc.Decode(&tree); err != nil {
		return nil, yamlProblems(err)
	}
	var ps []Problem
	for _, k := range sortedKeys(tree) {
		if msg, ok := layerReserved[k]; ok {
			ps = append(ps, Problem{Path: k, Message: msg})
		} else if !slices.Contains(layerTopKeys, k) {
			ps = append(ps, Problem{Path: k, Message: "is not a project layer key (" + strings.Join(layerTopKeys, ", ") + ")"})
		}
	}
	if d, ok := tree["defaults"].(map[string]any); ok {
		ps = append(ps, scopeProblems("defaults", "", d, InProject)...)
	}
	if prs, ok := tree["profiles"].(map[string]any); ok {
		for _, name := range sortedKeys(prs) {
			if p, ok := prs[name].(map[string]any); ok {
				q := maps1(p)
				delete(q, "description")
				ps = append(ps, scopeProblems("profiles."+name, "workflows.*", q, InProfile)...)
			}
		}
	}
	ps = append(ps, tokenProblems("", tree)...)
	if len(ps) > 0 {
		return nil, ps
	}
	var l ProjectLayer
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(&l); err != nil {
		return nil, yamlProblems(err)
	}
	if ps := validateLayer(&l, a); len(ps) > 0 {
		return nil, ps
	}
	l.Raw, l.SHA256, l.tree = slices.Clone(data), LayerSum(data), tree
	return &l, nil
}

func maps1(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// scopeProblems walks m, the layer's block at prefix, whose keys are the
// fugaro.yaml keys under repoPrefix, and reports every key that layer may
// not set or that does not exist.
func scopeProblems(prefix, repoPrefix string, m map[string]any, layer Scope) []Problem {
	var ps []Problem
	for _, k := range sortedKeys(m) {
		path, key := prefix+"."+k, k
		if repoPrefix != "" {
			key = repoPrefix + "." + k
		}
		key = starWorkflow(key)
		row, isRow := scopeRow(key)
		sub, isMap := m[k].(map[string]any)
		switch {
		case isRow && row.In&layer == 0:
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("%s may only be set in: %s (%s); the project layer may not set it", row.Key, row.In, row.Why)})
		case isRow:
		case isMap && isBlock(key):
			ps = append(ps, scopeProblems(path, key, sub, layer)...)
		default:
			ps = append(ps, Problem{Path: path, Message: "is not a fugaro.yaml key the project layer knows"})
		}
	}
	return ps
}

func tokenProblems(path string, v any) []Problem {
	switch t := v.(type) {
	case string:
		if tokenRE.MatchString(t) {
			return []Problem{{Path: path, Message: "holds a value shaped like a credential; nothing in the project layer is secret, and secrets are never published"}}
		}
	case map[string]any:
		var ps []Problem
		for _, k := range sortedKeys(t) {
			p := k
			if path != "" {
				p = path + "." + k
			}
			ps = append(ps, tokenProblems(p, t[k])...)
		}
		return ps
	case []any:
		var ps []Problem
		for i, e := range t {
			ps = append(ps, tokenProblems(fmt.Sprintf("%s[%d]", path, i), e)...)
		}
		return ps
	}
	return nil
}

// layerShape refuses anchors, aliases, explicit tags, merge keys, non-scalar
// keys and repeated keys anywhere, as the shared config's reader does.
func layerShape(n *yaml.Node) *Problem {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return &Problem{Line: n.Line, Message: "uses a YAML anchor or alias, which the project layer refuses"}
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return &Problem{Line: n.Line, Message: fmt.Sprintf("uses an explicit YAML tag %q, which the project layer refuses", n.Tag)}
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			switch {
			case k.Kind != yaml.ScalarNode:
				return &Problem{Line: k.Line, Message: "has a key that is not a plain value"}
			case k.Value == "<<" || k.Tag == "!!merge":
				return &Problem{Line: k.Line, Message: "uses a YAML merge key <<, which the project layer refuses"}
			case seen[k.Value]:
				return &Problem{Line: k.Line, Message: fmt.Sprintf("repeats the key %q", k.Value)}
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if p := layerShape(c); p != nil {
			return p
		}
	}
	return nil
}

func validateLayer(l *ProjectLayer, a LayerAnchor) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if l.Version != 1 {
		add("version", "must be 1")
	}
	switch {
	case !ProjectNameRE.MatchString(l.Project):
		add("project", "must be a project name")
	case a.Project != "" && l.Project != a.Project:
		add("project", "is %q, but it is read for project %q", l.Project, a.Project)
	}
	switch {
	case !GCPProjectRE.MatchString(l.GCPProject):
		add("gcp_project", "must be a GCP project ID")
	case a.GCPProject != "" && l.GCPProject != a.GCPProject:
		add("gcp_project", "is %q, but it is read for GCP project %q", l.GCPProject, a.GCPProject)
	}
	d := l.Defaults
	if d.Git.Provider != "" && !slices.Contains(Providers, d.Git.Provider) {
		add("defaults.git.provider", "must be one of %s", strings.Join(Providers, ", "))
	}
	ag := d.Agent
	if ag.Auth != "" && !slices.Contains([]string{"vertex", "api-key", "oauth"}, ag.Auth) {
		add("defaults.agent.auth", "must be one of vertex, api-key, oauth")
	}
	if ag.ReviewRounds != 0 && (ag.ReviewRounds < 1 || ag.ReviewRounds > 10) {
		add("defaults.agent.review_rounds", "must be between 1 and 10")
	}
	if ag.FirstLineReview != "" && !slices.Contains([]string{FirstLineAuto, FirstLineOn, FirstLineOff}, ag.FirstLineReview) {
		add("defaults.agent.first_line_review", "must be one of auto, on, off")
	}
	if ag.FirstLineRounds != 0 && (ag.FirstLineRounds < 1 || ag.FirstLineRounds > MaxFirstLineRounds) {
		add("defaults.agent.first_line_rounds", "must be between 1 and %d", MaxFirstLineRounds)
	}
	if ag.Recipe != "" && !recipe.NameRE.MatchString(ag.Recipe) {
		add("defaults.agent.recipe", "must be a recipe name")
	}
	if ag.MaxBudgetUSD < 0 {
		add("defaults.agent.max_budget_usd", "must not be negative")
	}
	for _, p := range validateAgentModels(Agent{Model: ag.Model, Models: ag.Models}) {
		p.Path = "defaults." + p.Path
		ps = append(ps, p)
	}
	for _, name := range sortedKeys(l.Profiles) {
		p := "profiles." + name
		if !ProjectNameRE.MatchString(name) {
			add(p, "a profile name must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
		ps = append(ps, validateProfile(p, l.Profiles[name])...)
	}
	if l.DefaultProfile != "" {
		if _, ok := l.Profiles[l.DefaultProfile]; !ok {
			add("default_profile", "names %q, which is not one of profiles: (%s)", l.DefaultProfile, strings.Join(sortedKeys(l.Profiles), ", "))
		}
	}
	return ps
}

// validateProfile checks what a profile sets; what it leaves out the
// repository may set, and Validate checks the merged workflow.
func validateProfile(p string, pr Profile) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if len(pr.Description) > maxProfileDescription || strings.ContainsAny(pr.Description, "\r\n") {
		add(p+".description", "must be one line of at most %d characters", maxProfileDescription)
	}
	if pr.Base != "" && !slices.Contains(Bases, pr.Base) {
		add(p+".base", "must be one of %s", strings.Join(Bases, ", "))
	}
	ps = append(ps, validateImage(p, Workflow{Base: pr.Base, Image: pr.Image})...)
	if rf := pr.Commands.RerunFailed; rf != nil {
		if strings.TrimSpace(rf.Command) == "" {
			add(p+".commands.rerun_failed.command", "is required")
		}
		if !strings.Contains(rf.Each, "{id}") {
			add(p+".commands.rerun_failed.each", "must contain {id}")
		}
	}
	for i, ce := range pr.Cache {
		cp := fmt.Sprintf("%s.cache[%d]", p, i)
		if len(ce.Key) == 0 {
			add(cp+".key", "must list at least one file")
		}
		if len(ce.Paths) == 0 {
			add(cp+".paths", "must list at least one path")
		}
	}
	if pr.Resources.CPU < 0 {
		add(p+".resources.cpu", "must be at least 1 (the compute backend checks its own limits)")
	}
	if m := pr.Resources.Memory; m != "" && !memoryRE.MatchString(m) {
		add(p+".resources.memory", "must look like 512Mi or 16Gi")
	}
	t := pr.Timeouts
	for _, d := range []struct {
		name string
		v    Duration
	}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"finalize_reserve", t.FinalizeReserve}} {
		if d.v.Set && d.v.Duration <= 0 {
			add(p+".timeouts."+d.name, "must be positive")
		}
	}
	r := pr.Rebuild
	if r.Check == "" {
		r.Check = "daily"
	}
	return append(ps, validateRebuild(p+".rebuild", r)...)
}
