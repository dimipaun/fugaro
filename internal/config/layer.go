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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	// tree is the text as Resolve merges it (nodeTree).
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
// (ExecutableKeys). Image.SkipBuildScripts is deliberately not one of
// these: true is the safe setting (it skips third-party install/build
// scripts), so a profile whose only setting is skip_build_scripts: true
// must not count as executable on its own; only a change to the value
// (true -> false, or a true removed) is gated, through the direct
// executableChanges key comparison in internal/cli/config_publish.go, not
// through this method.
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

// tokenRE matches text shaped like a credential: Anthropic, OpenRouter,
// GitHub, Slack, Google API, Bitbucket and AWS access keys, PEM private
// keys and a GCP service-account key file. Nothing in the project layer is
// a secret, so one is refused rather than published, whether a value or a
// key.
var tokenRE = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}|sk-or-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|gh[osu]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|ATBB[A-Za-z0-9]{20,}|(?:AKIA|ASIA)[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|"private_key"\s*:|"type"\s*:\s*"service_account"`)

// showKey is a key of the layer as an error may print it: a credential in
// it redacted, control and formatting characters escaped.
func showKey(k string) string {
	return pluginwire.Printable(tokenRE.ReplaceAllString(k, "<credential>"))
}

// badRune reports a character no project layer value holds: a control
// character other than tab and newline, an invisible formatting (bidi)
// character, a line or paragraph separator or invalid UTF-8.
func badRune(r rune) bool {
	return (unicode.IsControl(r) && r != '\t' && r != '\n') || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError
}

// badKeyRune is badRune for keys, which hold no tab or newline either.
func badKeyRune(r rune) bool { return r == '\t' || r == '\n' || badRune(r) }

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
		return nil, safeProblems(err)
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
	// layerShape refused anchors, tags and keys other than strings, so the
	// tree is the text, node for node.
	tree, ps0 := docTree(&doc)
	if len(ps0) > 0 {
		return nil, ps0
	}
	var ps []Problem
	for _, k := range sortedKeys(tree) {
		if msg, ok := layerReserved[k]; ok {
			ps = append(ps, Problem{Path: k, Message: msg})
		} else if !slices.Contains(layerTopKeys, k) {
			ps = append(ps, Problem{Path: showKey(k), Message: "is not a project layer key (" + strings.Join(layerTopKeys, ", ") + ")"})
		}
	}
	if d, ok := tree["defaults"].(map[string]any); ok {
		ps = append(ps, scopeProblems("defaults", "", d, InProject)...)
	}
	if prs, ok := tree["profiles"].(map[string]any); ok {
		for _, name := range sortedKeys(prs) {
			// A null profile (profiles: {name:}) would decode as a valid
			// empty Profile, usable as default_profile, with no scope
			// check ever run on it; Resolve's own tree has no such profile
			// (null, not a mapping), so it would then refuse to find a
			// profile ParseProjectLayer just accepted. Fail closed instead.
			if prs[name] == nil {
				ps = append(ps, Problem{Path: "profiles." + showKey(name), Message: "must be a mapping of profile keys, not null"})
				continue
			}
			if p, ok := prs[name].(map[string]any); ok {
				q := maps1(p)
				delete(q, "description")
				ps = append(ps, scopeProblems("profiles."+showKey(name), "workflows.*", q, InProfile)...)
			}
		}
	}
	ps = append(ps, valueProblems("", doc.Content[0])...)
	if len(ps) > 0 {
		return nil, ps
	}
	var l ProjectLayer
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(&l); err != nil {
		return nil, safeProblems(err)
	}
	if ps := validateLayer(&l, a); len(ps) > 0 {
		return nil, ps
	}
	if ps := rawTextProblems(data); len(ps) > 0 {
		return nil, ps
	}
	l.Raw, l.SHA256, l.tree = slices.Clone(data), LayerSum(data), tree
	return &l, nil
}

// safeProblems is yamlProblems with every message through showKey: yaml.v3
// echoes alias and anchor names, which are the writer's text.
func safeProblems(err error) []Problem {
	ps := yamlProblems(err)
	for i := range ps {
		ps[i].Message = showKey(ps[i].Message)
	}
	return ps
}

// blockHeaderRE matches a line ending in a block scalar indicator (| or >,
// with an optional chomping +/- and an optional explicit indentation digit,
// in either order: the YAML spec allows both "|-2" and "|2-"), right after
// the ':' or '-' that introduces it: the header line that opens a literal
// or folded scalar's body. Applied to a line with any trailing comment
// already stripped. The digit, wherever it falls, lands in one of the two
// capture groups (whichever alternative matched); the other is "".
var blockHeaderRE = regexp.MustCompile(`(?:^|[:-])\s*[|>](?:[+-]([1-9])?|([1-9])[+-]?)?\s*$`)

// commentStart returns the index of the '#' that starts line's comment, or
// -1 for none: a '#' at the start of the line or after whitespace, outside
// single or double quotes. A quote left open at line's end (a multi-line
// flow scalar) is not tracked across lines, so a '#' inside one is rarely,
// not never, missed as a comment.
func commentStart(line string) int {
	var inSingle, inDouble bool
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case inSingle:
			inSingle = c != '\''
		case inDouble:
			if c == '\\' {
				i++
			} else {
				inDouble = c != '"'
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return i
		}
	}
	return -1
}

// indentOf is the count of leading spaces of line (YAML indentation is
// never a tab).
func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// block scalar body-tracking states for rawTextProblems.
const (
	bodyNone     = iota // not inside a block scalar's body
	bodyAwaiting        // inside one, but its content indent isn't known yet
	bodyKnown           // inside one, content indent known
)

// rawTextProblems scans the whole text, comments and directives included,
// which the node walk never sees (valueProblems already ran over every key
// and value the node walk does see). A directive (a line starting at
// column 0 with %, YAML's own rule) and a line's comment, if any, get the
// key rule, which holds no tab either; the rest of the line gets the value
// rule. A block scalar's body (opened by a line matching blockHeaderRE) is
// never split at '#': a literal or folded scalar reads its body as plain
// text, a '#' in it starts no comment. But YAML's own rule for where that
// body actually is is "at or past the indentation of its first non-blank
// line (or the header's own explicit digit, header indent + N)", not
// merely "more indented than the header": a line indented more than the
// header but less than the body's real content indent is a real,
// standalone comment, not body text, the same as yaml.v3 itself reads it.
// Getting the content indent wrong by using the header's indent alone (an
// earlier version of this function did) wrongly widens the body and skips
// tokenRE and badKeyRune on a line that is, in fact, a comment: a
// credential or a tab there would go unrefused. A body line still runs
// tokenRE and badRune (never badKeyRune: a tab is fine in a value), so a
// credential or any other control/bidi character in one is still caught,
// just without the "(in a comment or directive)" wording, since it is not
// one. It reports the line only, never the text.
func rawTextProblems(data []byte) []Problem {
	var ps []Problem
	text := strings.TrimPrefix(string(data), "\ufeff")
	state := bodyNone
	headerIndent, contentIndent := 0, 0
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		blank := strings.TrimSpace(line) == ""
		if state == bodyAwaiting && !blank {
			if ind := indentOf(line); ind > headerIndent {
				state, contentIndent = bodyKnown, ind
			} else {
				state = bodyNone // the header's scalar is empty
			}
		}
		if state == bodyKnown {
			if blank {
				continue
			}
			if indentOf(line) < contentIndent {
				state = bodyNone // dedented out of the body
			} else {
				switch {
				case tokenRE.MatchString(line):
					ps = append(ps, Problem{Line: i + 1, Message: "holds a credential-shaped string (in a comment or directive); nothing in the project layer is secret, and secrets are never published"})
				case strings.ContainsFunc(line, badRune):
					ps = append(ps, Problem{Line: i + 1, Message: "holds a control or invisible formatting character"})
				}
				continue
			}
		}
		if tokenRE.MatchString(line) {
			ps = append(ps, Problem{Line: i + 1, Message: "holds a credential-shaped string (in a comment or directive); nothing in the project layer is secret, and secrets are never published"})
			continue
		}
		strict, loose := "", line
		switch c := commentStart(line); {
		case strings.HasPrefix(line, "%"):
			strict, loose = line, ""
		case c >= 0:
			strict, loose = line[c:], line[:c]
		}
		switch {
		case strings.ContainsFunc(strict, badKeyRune):
			ps = append(ps, Problem{Line: i + 1, Message: "holds a control or invisible formatting character (in a comment or directive)"})
		case strings.ContainsFunc(loose, badRune):
			ps = append(ps, Problem{Line: i + 1, Message: "holds a control or invisible formatting character"})
		}
		if m := blockHeaderRE.FindStringSubmatch(loose); m != nil {
			headerIndent = indentOf(line)
			if digit := m[1] + m[2]; digit != "" {
				n, _ := strconv.Atoi(digit)
				state, contentIndent = bodyKnown, headerIndent+n
			} else {
				state = bodyAwaiting
			}
		}
	}
	return ps
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
		path, key := prefix+"."+showKey(k), k
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
		case isBlock(key):
			ps = append(ps, Problem{Path: path, Message: "is a block of fugaro.yaml keys, so it must be a mapping of them"})
		default:
			ps = append(ps, Problem{Path: path, Message: "is not a fugaro.yaml key the project layer knows"})
		}
	}
	return ps
}

// valueProblems walks the layer's nodes and refuses every key and value
// shaped like a credential or holding a control or formatting character,
// and any node it does not know (fail closed). Neither the credential nor
// the character is printed back.
func valueProblems(path string, n *yaml.Node) []Problem {
	join := func(k string) string {
		if path == "" {
			return showKey(k)
		}
		return path + "." + showKey(k)
	}
	const secret = "nothing in the project layer is secret, and secrets are never published"
	var ps []Problem
	switch n.Kind {
	case yaml.ScalarNode:
		switch {
		case tokenRE.MatchString(n.Value):
			ps = append(ps, Problem{Path: path, Line: n.Line, Message: "holds a value shaped like a credential; " + secret})
		case strings.ContainsFunc(n.Value, badRune):
			ps = append(ps, Problem{Path: path, Line: n.Line, Message: "holds a control or invisible formatting character"})
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			switch {
			case tokenRE.MatchString(k.Value):
				ps = append(ps, Problem{Path: path, Line: k.Line, Message: "has a key shaped like a credential; " + secret})
			case strings.ContainsFunc(k.Value, badKeyRune):
				ps = append(ps, Problem{Path: path, Line: k.Line, Message: "has a key \"" + showKey(k.Value) + "\" holding a control or invisible formatting character"})
			default:
				ps = append(ps, valueProblems(join(k.Value), v)...)
			}
		}
	case yaml.SequenceNode:
		for i, e := range n.Content {
			ps = append(ps, valueProblems(fmt.Sprintf("%s[%d]", path, i), e)...)
		}
	default:
		ps = append(ps, Problem{Path: path, Line: n.Line, Message: "holds a YAML node the project layer does not know"})
	}
	return ps
}

// layerShape refuses anchors, aliases, explicit tags, merge keys, keys that
// are not strings (1:, null:, true:, which would decode as other types and
// slip past the checks of the decoded tree) and repeated keys anywhere.
func layerShape(n *yaml.Node) *Problem {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return &Problem{Line: n.Line, Message: "uses a YAML anchor or alias, which the project layer refuses"}
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return &Problem{Line: n.Line, Message: fmt.Sprintf("uses an explicit YAML tag %q, which the project layer refuses", showKey(n.Tag))}
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
			case k.Tag != "!!str":
				return &Problem{Line: k.Line, Message: fmt.Sprintf("has the key %q, which YAML reads as %s, not a string; quote it", tokenRE.ReplaceAllString(k.Value, "<credential>"), showKey(strings.TrimPrefix(k.Tag, "!!")))}
			case seen[k.Value]:
				return &Problem{Line: k.Line, Message: fmt.Sprintf("repeats the key %q", tokenRE.ReplaceAllString(k.Value, "<credential>"))}
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
	if badUSD(ag.MaxBudgetUSD) {
		add("defaults.agent.max_budget_usd", "must be a finite number, not negative")
	}
	for _, p := range validateAgentModels(Agent{Model: ag.Model, Models: ag.Models}) {
		p.Path = "defaults." + p.Path
		ps = append(ps, p)
	}
	for _, name := range sortedKeys(l.Profiles) {
		p := "profiles." + showKey(name)
		if !ProjectNameRE.MatchString(name) {
			add(p, "a profile name must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
		ps = append(ps, validateProfile(p, l.Profiles[name])...)
	}
	if l.DefaultProfile != "" {
		if _, ok := l.Profiles[l.DefaultProfile]; !ok {
			names := sortedKeys(l.Profiles)
			for i, n := range names {
				names[i] = showKey(n)
			}
			add("default_profile", "names %q, which is not one of profiles: (%s)", showKey(l.DefaultProfile), strings.Join(names, ", "))
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
	if utf8.RuneCountInString(pr.Description) > maxProfileDescription || strings.ContainsFunc(pr.Description, badKeyRune) {
		add(p+".description", "must be one line of at most %d characters, with no control or formatting characters", maxProfileDescription)
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
		add(p+".resources.cpu", "must not be negative (0 leaves it unset, filled in from the workflow's own default later)")
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
