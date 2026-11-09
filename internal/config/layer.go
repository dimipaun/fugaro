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

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
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

// buildsPrefix is where everything a build reads out of the runs bucket
// lives, one directory per writer: the per-repository copies
// (LayerCopyKey) and the catalog's Dockerfiles (CatalogDockerfileKey). The
// build and check accounts hold their grants conditioned on it, so a key
// outside it is one they cannot read.
const buildsPrefix = "builds/"

// LayerCopyKey is the per-repository copy of the project layer, under the
// build account's own prefix, which the daily image check and Cloud Build
// read (decision L4).
func LayerCopyKey(slug string) string { return buildsPrefix + slug + "/project-layer.yaml" }

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

	// ClaudeModels are the Claude model IDs a project layer adds to the
	// owner's routes: provider name -> explicit "anthropic/<id>" entries
	// (config.ApplyClaudeClaims). A top-level key on purpose: it is a
	// route entry, not a fugaro.yaml key, so a repository cannot write it
	// (Resolve merges only defaults: into the repository's tree, with
	// KnownFields).
	ClaudeModels map[string][]string `yaml:"claude_models"`

	// Governance is the project's governance: block (docs/task-governance).
	// A top-level key on purpose, like ClaudeModels and for a sharper reason:
	// the defaults: overlay is replace-not-tighten (resolve.go), so a
	// governance: under defaults: would let a repository's own block replace
	// the project's value with a looser one instead of tightening it. Read
	// here, the two meet only in the merge (internal/policy), which only ever
	// takes the tighter — which is the whole of "the project layer sets them,
	// a repository may only tighten" (D331). It is the one layer that may
	// loosen Fugaro's own defaults (policy.Governance.WithDefaults).
	Governance *Governance `yaml:"governance"`

	// AB is the project's model A/B block (docs/model-ab-testing, D550). A
	// top-level key on purpose, like Governance and for the same reason: the
	// defaults: overlay is replace-not-tighten (resolve.go), so an ab: under
	// defaults: would let a repository's own block replace the owner's
	// choices instead of only tightening them. Read here, the two meet only
	// in the merge (internal/policy), which takes the lower rate and nothing
	// else of the block from the repository: a repository may only opt out
	// (D556).
	AB *AB `yaml:"ab"`

	// Jobs is the project's jobs: block (R13): the installation's two
	// scheduled jobs and whether each is stopped. A top-level key on purpose,
	// like Governance and AB and for the same reason: the defaults: overlay is
	// replace-not-tighten (resolve.go), so a jobs: under defaults: could be
	// replaced by a repository's own value. A repository's fugaro.yaml has no
	// such key at all, and its decoding refuses unknown fields, so the two
	// never meet: the only value in play is the operator's, and paused: false
	// — the loosening direction — is theirs alone, which is what a schedule
	// the project runs should be.
	Jobs *Jobs `yaml:"jobs"`

	// Security is the project's security: block (docs/security-classification,
	// D1501): the rows every repository of the project treats as
	// security-sensitive, and the layer's own always_level_3. A top-level key
	// on purpose, like Governance and AB and for their reason: the defaults:
	// overlay is replace-not-tighten (resolve.go), so a security: under
	// defaults: would let a repository's own block replace the project's rows,
	// and a repository dropping a row the project wrote is the one direction
	// D1506 forbids. Read here, the layer's rows and the repository's meet only
	// in the union, which keeps the project's first.
	Security *LayerSecurity `yaml:"security"`

	// Raw is the exact text, SHA256 its hex sha256.
	Raw    []byte `yaml:"-"`
	SHA256 string `yaml:"-"`
	// tree is the text as Resolve merges it (nodeTree).
	tree map[string]any
}

// Jobs is the project layer's jobs: block (R13): the installation's two
// scheduled jobs, and whether each is stopped. Every field is a pointer
// because the default is paused: a layer that writes no jobs: block, no block
// for one job or no paused: key means the schedule stays stopped, and the
// zero value of a bool would say the opposite — the one direction this key
// must never take by omission.
type Jobs struct {
	// Reports is the reports job: the project's one writer of reports/ in the
	// runs bucket (R12's module, R11's code). Fugaro before 0.22.0 refuses
	// the key (jobsSince in internal/cli, the constant its gates judge the
	// job image against).
	Reports *JobState `yaml:"reports"`
	// CatalogCheck is the project's catalog check job, scheduled since 0.18.0
	// (D1148). It is the job whose own operator note offered a `gcloud
	// scheduler jobs resume` that did not stick: paused was a Terraform
	// variable with no field in CatalogCheckSpec for a run to set it from,
	// so the next fugaro update applied the module's true again.
	CatalogCheck *JobState `yaml:"catalog_check"`
}

// JobState is one scheduled job's own settings.
type JobState struct {
	// Paused stops the job's Cloud Scheduler job. It defaults to true, so a
	// project that publishes a catalog and one that runs the reports job
	// deploy both schedules stopped, and turning one on is the operator's own
	// edit of the layer file: `fugaro settings set` refuses both keys (the
	// generic path writes its allowlist and nothing else), so the file is
	// written by hand and `fugaro update` is what publishes it and applies it.
	Paused *bool `yaml:"paused"`
}

// LayerSecurity is the project layer's security: block (docs/security-
// classification, D1501): the rows every repository of the project treats as
// security-sensitive, and the switch that puts every run of the project at
// level 3. Fugaro before 0.22.0 refuses the key, in every repository of the
// project: the layer is decoded strictly by every release, so an older job
// image dies at bootstrap on the key alone.
//
// The rows are sensitivity.Pattern, the type the repository's own security:
// block carries (config.Security): a row is written, validated and read the
// same way in either layer, so a project's row moves to a repository's file
// unchanged and neither layer has a rule the other lacks (D1500, D1502). They
// are judged by validateSecurity, the one function both blocks go through.
//
// AlwaysLevel3 is the block's one member a repository may not write: a
// repository's security: block is the rows and nothing else, so a file that
// writes it is refused by the decoder and by the published schema. That is
// deliberate. "Everything this repository touches is security-sensitive" is a
// whole-repository statement about a shared project, and it is the statement
// D1503 refuses to let a `**` pattern hide inside a glob — a repository that
// could write it would be answering a project's control from below.
type LayerSecurity struct {
	// Patterns are the project's rows: a name, a doublestar glob relative to
	// the repository root, and the category the paths it matches are labelled
	// with. Each row is validated by sensitivity.Pattern.Validate, at load and
	// at its own index (D1502, F21): a glob the matcher cannot read or a
	// category outside the seven-value vocabulary is a configuration error
	// rather than a rule that silently never fires.
	Patterns []sensitivity.Pattern `yaml:"patterns"`
	// AlwaysLevel3 puts every run of the project at level 3, whatever paths it
	// changed (D1517). It only ever turns the level on: false and an absent
	// key are the same statement, and there is no value that asks for less.
	AlwaysLevel3 bool `yaml:"always_level_3"`
}

// ReportsPaused is the reports job's own state: the value the layer wrote,
// and true (paused) wherever it wrote none — no jobs: block, no reports:
// block, no paused: key, and no layer at all. It is nil-safe in both hops, so
// a caller holding no layer file reads the default.
func (l *ProjectLayer) ReportsPaused() bool {
	return jobPaused(l, func(j *Jobs) *JobState { return j.Reports })
}

// CatalogCheckPaused is the catalog check job's own state; see
// ReportsPaused for the default and why silence means paused.
func (l *ProjectLayer) CatalogCheckPaused() bool {
	return jobPaused(l, func(j *Jobs) *JobState { return j.CatalogCheck })
}

// jobPaused reads one job's paused out of a layer that may be absent at any
// of the three hops: the default is a fact of one place rather than of each
// caller, because "the layer wrote nothing" and "the layer wrote false" are
// one bool apart and only one of them stops the schedule.
func jobPaused(l *ProjectLayer, job func(*Jobs) *JobState) bool {
	if l == nil || l.Jobs == nil {
		return true
	}
	st := job(l.Jobs)
	return st == nil || st.Paused == nil || *st.Paused
}

// LayerDefaults are the project-wide values of the repository keys the
// project layer may set (scope project). Rigor is the third since 0.11.0:
// default is the mode a repository that writes none takes, and areas are
// rules that stay in effect beside the repository's own (D20c).
type LayerDefaults struct {
	Git      LayerGit      `yaml:"git"`
	Agent    LayerAgent    `yaml:"agent"`
	Rigor    LayerRigor    `yaml:"rigor"`
	Planning LayerPlanning `yaml:"planning"`
}

// LayerPlanning is defaults.planning (docs/contract-first-decoupling, D1813):
// the same key a repository's own planning: block carries, because either
// layer may set it.
type LayerPlanning struct {
	Decoupling *bool `yaml:"decoupling"`
}

// SetsPlanning reports whether the layer's text writes defaults.planning, at
// any value: a runner before planningSince decodes the layer with KnownFields,
// so the key alone is what it refuses, and LayerPlanning's zero value cannot
// tell an absent block from one written with decoupling: true — and the skew
// gate judges both files by the key, not by the value.
func (l *ProjectLayer) SetsPlanning() bool {
	if l == nil {
		return false
	}
	d, _ := l.tree["defaults"].(map[string]any)
	_, ok := d["planning"]
	return ok
}

// SetsRigor reports whether the layer's text writes defaults.rigor, at any
// value: a runner before 0.11.0 decodes the layer with KnownFields, so the key
// alone is what it refuses, and LayerRigor's zero value cannot tell an empty
// block from none.
func (l *ProjectLayer) SetsRigor() bool {
	if l == nil {
		return false
	}
	d, _ := l.tree["defaults"].(map[string]any)
	_, ok := d["rigor"]
	return ok
}

// SetsLevelsNoneModel reports whether the layer's text names a review level's
// model with the literal none (NoneModel), the level's own switch written the
// other way. It reads the tree and not the parsed defaults.agent.levels, which
// the decode and validate fold the literal out of, for the reason SetsRigor
// reads its own: the question is what the text says, and the gate needs it
// because a runner older than noneModelSince reads the literal as a model ID
// rather than skipping the level.
func (l *ProjectLayer) SetsLevelsNoneModel() bool {
	if l == nil {
		return false
	}
	d, _ := l.tree["defaults"].(map[string]any)
	return levelsNoneModel(d["agent"])
}

// SetsGovernance reports whether the layer's text writes governance:, at any
// value: a runner before governanceSince decodes the layer with KnownFields,
// so the key alone is what it refuses, and Governance's zero value cannot
// tell an empty block from none. The key is top-level, not under defaults:
// (TestProjectLayerGovernanceIsTopLevel), so the tree is read at the root.
func (l *ProjectLayer) SetsGovernance() bool {
	if l == nil {
		return false
	}
	_, ok := l.tree["governance"]
	return ok
}

// SetsJobs reports whether the layer's text writes jobs:, at any value: a
// runner before jobsSince (internal/cli) decodes the layer with KnownFields, so
// the key alone is what it refuses, and Jobs' zero value cannot tell an empty
// block from none. It is what the gates judge the job images against. The key is
// top-level, not under defaults: (TestProjectLayerJobsIsTopLevel), like
// governance: and for the same reason: the block is a policy the project sets
// for every one of its repositories rather than a default a branch may replace.
func (l *ProjectLayer) SetsJobs() bool {
	if l == nil {
		return false
	}
	_, ok := l.tree["jobs"]
	return ok
}

// SetsSecurity reports whether the layer's text writes security:, at any
// value: a runner before securitySince (internal/cli) decodes the layer with
// KnownFields, so the key alone is what it refuses, and Security's zero value
// cannot tell an empty block from none. It is what the gates judge the job
// images against. The key is top-level, not under defaults:
// (TestProjectLayerSecurityIsInTheTopKeyAllowList), like jobs: and for the same
// reason: the block is a policy the project sets for every one of its
// repositories rather than a default a branch may replace (D1501).
func (l *ProjectLayer) SetsSecurity() bool {
	if l == nil {
		return false
	}
	_, ok := l.tree["security"]
	return ok
}

// SetsDefault reports whether the layer's text writes a fugaro.yaml key under
// defaults:, at any value — its own, or the nearest ancestor the text writes,
// because a list is one key (Resolution.Sources' own rule: rigor.areas is set
// by its list, not by each rule's mode).
//
// The text answers it, not LayerDefaults: the decoded fields cannot tell an
// unset key from one written with the type's zero value — agent.review_rounds:
// 0 and agent.model: "" decode to the same fields as keys the layer never
// wrote — so a reader that took the zero for a value would print a 0 or a
// blank for a key the layer does not set.
func (l *ProjectLayer) SetsDefault(key string) bool {
	if l == nil {
		return false
	}
	cur, ok := l.tree["defaults"]
	if !ok || cur == nil {
		return false
	}
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			// The text writes a leaf here and the path goes on, so the answer
			// is whether a list sits at it: a list is one key, and a scalar has
			// no keys under it.
			return listUnderLeaf(cur)
		}
		next, ok := m[part]
		if !ok || next == nil {
			return false
		}
		cur = next
	}
	return true
}

// listUnderLeaf answers the walk's leaf case: the path reached a value that is
// not a mapping and goes on past it, which only a list survives — a list is one
// key, so `defaults.rigor.areas.name` is set by the list `areas` writes
// (Resolution.Sources' own rule), where a scalar has no keys under it at all.
//
// The value is a *yaml.Node and not a decoded Go value, because that is what
// nodeTree keeps every non-mapping as — a copy of the node, so marshalling
// writes back the text and tag it was written with. A null is nil there and
// never reaches this, so the kind is what tells a list from a scalar.
func listUnderLeaf(v any) bool {
	n, ok := v.(*yaml.Node)
	return ok && n.Kind != yaml.ScalarNode
}

// LayerRigor is defaults.rigor: the same two keys the repository's own
// rigor: block carries, because either layer may set either of them
// (docs/project-layer.md §5).
type LayerRigor struct {
	Default rigor.Mode   `yaml:"default"`
	Areas   []rigor.Area `yaml:"areas"`
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
	// Levels is the project-wide per-level review block (L1.3, D722): the
	// same config.ReviewLevels a repository's agent.levels and a task's
	// review_levels carry. The merge is per key (resolve.go's overlay), so
	// the project's l1 survives a repository that pins l2, and neither is
	// replaced whole by the other.
	Levels ReviewLevels `yaml:"levels"`
	// AdversarialRounds is the project's review level 3 round count; a
	// repository's own value replaces it.
	AdversarialRounds int    `yaml:"adversarial_rounds"`
	Recipe            string `yaml:"recipe"`
	// Crafts sets each craft's model and access for every repository of the
	// project (docs/model-crafts/spec.md §3). Only a craft's model may be set
	// here: an access names a credential or a pool, and this layer file is
	// writable by anyone who can write the runs bucket — the reason
	// config.Scopes refuses it here, the same one agent.oauth_token carries —
	// a launcher who could write the file otherwise choosing the credential a
	// run goes out on. A craft's access is set in the local config and the
	// policy node, both written by the owner alone.
	Crafts Crafts `yaml:"crafts"`
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
	// Inputs is workflows.*.inputs through this profile (scope
	// InProfile|InRepo): the caps a launcher may attach to a run of the
	// workflows that start from it.
	Inputs Inputs `yaml:"inputs"`
	// Checkout is workflows.*.checkout's value through this profile; "" sets
	// none (a workflow or the Fugaro default decides).
	Checkout string `yaml:"checkout,omitempty"`
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
	return p.Commands.Build != "" || p.Commands.Test != "" || p.Commands.Lint != "" || p.Commands.Fix != "" ||
		p.Commands.Render != "" || p.Commands.Probe != "" ||
		p.Commands.RerunFailed != nil || len(p.Image.Apt) > 0 || len(p.Image.Setup) > 0 || len(p.Image.Tools) > 0
}

// LayerSum is the hex sha256 of a project layer's text.
func LayerSum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

var layerTopKeys = []string{"version", "project", "gcp_project", "defaults", "profiles", "default_profile", "claude_models", "governance", "ab", "jobs", "security"}

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
	if ag.Auth != "" && !KnownAgentAuth(ag.Auth) {
		// The same list the repository's own agent.auth is checked against
		// (AgentAuths, one list for both): a layer that sets a value the file's
		// check refuses would otherwise be a config a run resolves to something
		// no document may name.
		add("defaults.agent.auth", "must be one of %s", strings.Join(AgentAuths, ", "))
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
	if ag.AdversarialRounds != 0 && (ag.AdversarialRounds < 1 || ag.AdversarialRounds > 10) {
		add("defaults.agent.adversarial_rounds", "must be between 1 and 10")
	}
	ps = append(ps, levelProblems("defaults.agent.levels", &ag.Levels)...)
	if ag.Recipe != "" && !recipe.NameRE.MatchString(ag.Recipe) {
		add("defaults.agent.recipe", "must be a recipe name")
	}
	if badUSD(ag.MaxBudgetUSD) {
		add("defaults.agent.max_budget_usd", "must be a finite number, not negative")
	}
	ps = append(ps, validateRigor("defaults.rigor", d.Rigor.Default, d.Rigor.Areas)...)
	ps = append(ps, validateGovernance("governance", l.Governance)...)
	ps = append(ps, validateAB("ab", l.AB)...)
	if l.Security != nil {
		// The project's rows, by the rules the repository's own are judged by:
		// one function, two blocks (D1502, D1506). always_level_3 is a bool and
		// has nothing to refuse.
		ps = append(ps, validateSecurity("security", l.Security.Patterns)...)
	}
	for _, p := range validateAgentModels(Agent{Model: ag.Model, Models: ag.Models, Crafts: ag.Crafts}) {
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
	for _, name := range sortedKeys(l.ClaudeModels) {
		p := "claude_models." + showKey(name)
		if !providerNameRE.MatchString(name) {
			add(p, "a provider name must match %s", providerNameRE)
		}
		ids := l.ClaudeModels[name]
		if len(ids) == 0 {
			add(p, "must list at least one Claude model ID (an explicit anthropic/<id>)")
			continue
		}
		for i, m := range ids {
			// Two rules, because a claim is appended to a route's own models
			// (ApplyClaudeClaims) and the merged entry is judged by both:
			// CheckClaudeClaim (a claim is one explicit anthropic/<id>, never
			// a pattern, a bare claude-* spelling, a ":" or "[1m]" variant,
			// or another provider's model) and CheckModelPattern, the rule a
			// route entry is judged by -- which the gateway's routes apply
			// again (CheckModelPattern's invariant: a config that validates
			// here always starts there). The claim rule alone holds no
			// length bound, so without the second check an over-long ID would
			// be accepted here, merged into the route, and then refuse to
			// start every run whose job carries that route.
			at := fmt.Sprintf("%s[%d]", p, i)
			if msg := CheckClaudeClaim(m); msg != "" {
				add(at, "%s", msg)
				continue
			}
			if msg := CheckModelPattern(m); msg != "" {
				add(at, "%s", msg)
			}
		}
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
	if pr.Base != "" {
		add(p+".base", legacyBaseMessage, MigrationDoc)
	}
	ps = append(ps, validateImage(p, Workflow{Base: pr.Base, Image: pr.Image})...)
	if pr.Checkout != "" {
		ps = append(ps, validateCheckout(p, Workflow{Base: pr.Base, Image: pr.Image, Checkout: pr.Checkout})...)
	}
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
	}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"render", t.Render}, {"finalize_reserve", t.FinalizeReserve}} {
		if d.v.Set && d.v.Duration <= 0 {
			add(p+".timeouts."+d.name, "must be positive")
		}
	}
	ps = append(ps, validateInputs(p+".inputs", pr.Inputs)...)
	r := pr.Rebuild
	if r.Check == "" {
		r.Check = "daily"
	}
	return append(ps, validateRebuild(p+".rebuild", r)...)
}
