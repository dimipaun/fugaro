package lift

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanTighten is B4's planner (RT3): every value a repository's file writes
// that B4's tighten-only rule drops is named, one line per value, before any
// write — above all an agent.max_budget_usd above the baseline its runs get
// where the project says nothing, config.DefaultMaxBudgetUSD, whose runs now
// stop earlier than the repository's own file asks (R20, spec §5.2).
//
// It names and writes nothing: a tightened key is not lifted, so release A
// leaves it in the repository's own file and every run resolves it — the fix is
// the person's (state the value in the project layer, or lower it in the
// repository), which is why this item has no layer edit, no store object, no
// database node and no migration record at all (C2). A repository the project
// already migrated is not named again (D3305): the update that migrated it ran
// this item too and printed the line there.
//
// What it judges, and what it leaves to the rule that owns it:
//
//   - Every key of spec §5.2's table the project layer sets: the repository's
//     value is dropped where it does not tighten the project's, and the line
//     names the value that stands. That is the drop RT1 makes in
//     config.Resolve, judged by the same table spec §5.2 gives it — a
//     repository whose file asks for less review than its project now learns
//     that its runs take the project's value.
//   - The two keys no mode row holds, where the project says nothing: R20's
//     baseline is Fugaro's own default, and it is a constant update can read —
//     config.DefaultMaxBudgetUSD for a stage's budget, and
//     config.DefaultReviewStopFraction for the stop rule. Those are the drops
//     that end a run earlier than its file asks, which is what this item is
//     named for.
//   - A key whose baseline is the run's mode — a level's switch and effort,
//     and the three round counts — is named nowhere here. update cannot know a
//     run's mode (the task, the paths and the request decide it), so the
//     baseline belongs where the mode is known: RT2's own resolution at the
//     run, which takes the tighter of the repository's value and the mode's
//     row. A repository's value under a silent project is unchecked (R3) and
//     no line of this change claims a mode this item cannot see.
//
// It answers no error: a repository whose fugaro.yaml is not YAML, one whose
// provider and name are not a repository this can key, and one the reader could
// not read are named by the item that owns them and by M2 (D3306), and a value
// this item cannot judge is a value no line claims. A drop is never a refusal.
func PlanTighten(p Project, repos []Repo) (Change, error) {
	var out Change
	seen := map[string]bool{}
	for _, r := range sortedRepos(repos) {
		if r.Repo == "" || r.Unreadable != "" || len(r.Config) == 0 {
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil || seen[slug] {
			continue
		}
		seen[slug] = true
		if p.Migrated[slug] {
			continue
		}
		written, ok := tightenWritten(r.Config)
		if !ok {
			continue
		}
		for _, w := range written {
			if line := tightenDropLine(r.Repo, p.Layer, w); line != "" {
				out.Lines = append(out.Lines, line)
			}
		}
	}
	return out, nil
}

// tightenKind is how a repository's value of a tighten-only key compares with
// the project's: the rule spec §5.2's table gives the key, the way config's own
// tighten rule reads it. tightenReplace is a key no drop can be named for —
// focus is additive, agent.levels.lN.model is lifted (D3291), and agent.recipe
// and agent.workspace_beat are replaced whole (D3292, D3293).
type tightenKind int

const (
	tightenReplace tightenKind = iota
	tightenMore                // a larger number tightens
	tightenLess                // a smaller number tightens
	tightenRank                // a later rank tightens
	tightenSwitch              // a level on beats off
)

// tightenKindOf is the rule of one path, tightenReplace for a key this item
// names no drop for.
func tightenKindOf(path string) tightenKind {
	switch path {
	case "agent.review_rounds", "agent.first_line_rounds", "agent.adversarial_rounds", "agent.review_stop_fraction":
		return tightenMore
	case "agent.max_budget_usd":
		return tightenLess
	case "agent.first_line_review":
		return tightenRank
	}
	level, ok := strings.CutPrefix(path, "agent.levels.")
	if !ok {
		return tightenReplace
	}
	switch {
	case strings.HasSuffix(level, ".effort"):
		return tightenRank
	case strings.HasSuffix(level, ".none"):
		return tightenSwitch
	}
	return tightenReplace
}

// tightenValue is one tighten-only value a repository's file writes: the
// resolved path, the text as its author wrote it, and the rule it is judged by.
type tightenValue struct {
	path string
	text string
	kind tightenKind
}

// tightenWritten is every tighten-only value in data, in path order, and
// whether the file could be read at all. The walk is over the file as written:
// the values a run honours are the ones config.Resolve merges, and this item
// names the drops among them, never a key it moved (D3291's lifted levels, the
// additive focus, the two replaced blocks).
func tightenWritten(data []byte) ([]tightenValue, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, true
		}
		root = root.Content[0]
	}
	var out []tightenValue
	tightenWalk(root, "", &out)
	slices.SortFunc(out, func(a, b tightenValue) int { return strings.Compare(a.path, b.path) })
	return out, true
}

// tightenWalk collects the scalar leaves of one mapping, the path of each in
// the spelling a resolution uses (one key per dot).
func tightenWalk(n *yaml.Node, path string, out *[]tightenValue) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := k.Value
		if path != "" {
			p = path + "." + k.Value
		}
		switch {
		case v.Kind == yaml.MappingNode:
			tightenWalk(v, p, out)
		case v.Kind == yaml.ScalarNode && v.ShortTag() != "!!null":
			if kind := tightenKindOf(p); kind != tightenReplace {
				*out = append(*out, tightenValue{path: p, text: v.Value, kind: kind})
			}
		}
	}
}

// tightenDropLine is the line for one value of a repository's file, "" when
// nothing drops it: the project's own value where the project sets the key, and
// R20's baseline where it says nothing.
func tightenDropLine(repo string, layer map[string]any, w tightenValue) string {
	if w.kind == tightenSwitch {
		return levelOffLine(repo, layer, w)
	}
	proj, set := projectTightenValue(layer, w.path)
	if !set {
		return silentTightenDrop(repo, w)
	}
	if !tightenDrops(w, proj) {
		return ""
	}
	return fmt.Sprintf("%s: %s %s dropped; the project's %s stands", repo, w.path, w.text, tightenText(proj))
}

// levelOffLine is the line for a repository's own off for one review level: a
// level on beats off, so the value is dropped where the project's block for
// that level is written and does not turn the level off either (mergeLevel's
// rule at RT1, spec §5.2). Where the project writes no block for the level the
// value stands unchecked (R3) and the run's mode decides, so no line claims a
// drop this item cannot see.
func levelOffLine(repo string, layer map[string]any, w tightenValue) string {
	if !tightenOff(w.text) {
		return ""
	}
	block, set := layerValue(layer, "defaults", "agent", "levels", levelOf(w.path)).(map[string]any)
	if !set || tightenLevelOff(block) {
		return ""
	}
	return fmt.Sprintf("%s: %s %s dropped; the project runs %s", repo, w.path, w.text, levelOf(w.path))
}

// levelOf is the review level a tighten path names: agent.levels.l2.effort is l2.
func levelOf(path string) string {
	segs := strings.Split(path, ".")
	if len(segs) < 3 {
		return ""
	}
	return segs[2]
}

// tightenLevelOff is whether a level's block says the level does not run, in
// either spelling the config reads it in: `none: true`, or the level's model
// named with the literal none (config.NoneModel).
func tightenLevelOff(block map[string]any) bool {
	if off, ok := block["none"].(bool); ok && off {
		return true
	}
	model, ok := block["model"]
	return ok && tightenText(model) == config.NoneModel
}

// tightenOff is a written `none` value: true says the level does not run.
func tightenOff(text string) bool {
	off, err := strconv.ParseBool(text)
	return err == nil && off
}

// projectTightenValue is the project layer's own value of a tighten-only key,
// false where the layer sets none: the layer file's defaults block, which is
// the tree M2 hands a planner (C2), and the path of a key with the layer's own
// defaults segment in front of it.
func projectTightenValue(layer map[string]any, path string) (any, bool) {
	v := layerValue(layer, append([]string{"defaults"}, strings.Split(path, ".")...)...)
	if v == nil {
		return nil, false
	}
	if _, isMap := v.(map[string]any); isMap {
		return nil, false
	}
	return v, true
}

// tightenDrops reports whether B4's rule drops a repository's value that the
// project sets its own to: a value that does not tighten the project's is
// dropped, and an equal one changes nothing. A pair it cannot judge — a value
// the decoder refuses, which Validate reports itself — is not judged here, so
// no line claims a drop the resolution may not make (config's tightenWins).
func tightenDrops(w tightenValue, proj any) bool {
	switch w.kind {
	case tightenMore, tightenLess:
		r, rok := tightenNumber(w.text)
		p, pok := tightenNumber(tightenText(proj))
		if !rok || !pok || r == p {
			return false
		}
		if w.kind == tightenMore {
			return r < p
		}
		return r > p
	case tightenRank:
		r, rok := tightenRankOf(w.path, w.text)
		p, pok := tightenRankOf(w.path, tightenText(proj))
		if !rok || !pok {
			return false
		}
		return r < p
	}
	return false
}

// silentTightenDrop is the line for one value the project says nothing about,
// "" when R20's baseline does not drop it: Fugaro's own default for the two
// keys no mode row holds. The value 0 is unset at every layer for both (the
// config's own validation), so it states nothing and is left alone.
func silentTightenDrop(repo string, w tightenValue) string {
	f, ok := tightenNumber(w.text)
	if !ok {
		return ""
	}
	switch w.path {
	case "agent.max_budget_usd":
		if f <= config.DefaultMaxBudgetUSD {
			return ""
		}
		return fmt.Sprintf("%s: %s %s dropped; the project sets none, so its runs now stop at %s",
			repo, w.path, w.text, tightenUSD(config.DefaultMaxBudgetUSD))
	case "agent.review_stop_fraction":
		if f == 0 || f >= config.DefaultReviewStopFraction {
			return ""
		}
		return fmt.Sprintf("%s: %s %s dropped; the project sets none, so the run reviews to %s of its budget",
			repo, w.path, w.text, tightenNumberText(config.DefaultReviewStopFraction))
	}
	return ""
}

// tightenRankOf is a scalar's rank in its key's own order: off < auto < on for
// agent.first_line_review, and its index in config.Efforts for an effort.
func tightenRankOf(path, text string) (int, bool) {
	switch path {
	case "agent.first_line_review":
		switch text {
		case config.FirstLineOff:
			return 0, true
		case config.FirstLineAuto:
			return 1, true
		case config.FirstLineOn:
			return 2, true
		}
		return 0, false
	}
	if i := slices.Index(config.Efforts, text); i >= 0 {
		return i, true
	}
	return 0, false
}

// tightenNumber is a scalar as a number, false for anything the config does
// not read as one.
func tightenNumber(text string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return f, err == nil
}

// tightenText is one value as a person reads it: the text a file wrote a key
// with, and a layer value's own spelling, which for a budget is a figure in
// dollars and for a fraction a share.
func tightenText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// tightenUSD is a figure in dollars, the way the budget commands print money.
func tightenUSD(f float64) string { return "$" + tightenNumberText(f) }

// tightenNumberText is a number without a trailing zero or an exponent.
func tightenNumberText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
