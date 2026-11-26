package config

import (
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/policy"
)

// B4's tighten-only merge (spec §5.2, docs/repo-override-rulings): review depth
// and spend are a repository's to raise and never to lower. overlay applies the
// rule where the two layers meet, so every reader of a resolved configuration —
// the runner, the launcher, validate — sees one answer: a value that does not
// tighten the project's is dropped, its source stays the project's, and the
// resolution records it (Resolution.Ignored); a tighter one wins; and a value
// the project says nothing about is kept and marked
// (Resolution.Unchecked) for the places that apply the run's mode to it (R3,
// RT2).
//
// agent.levels.lN.model is in §5.2's table and not here: D3291 makes it a
// lifted key, dropped before the merge ever sees it (lifted_drop.go).
// agent.recipe and agent.workspace_beat keep overlay's replace rule (D3292,
// D3293).

// tightenKind is how a repository's value of a tighten-only key compares with
// the project's.
type tightenKind int

const (
	tightenReplace tightenKind = iota // not a tighten-only key: overlay replaces
	tightenMore                       // a larger number tightens
	tightenLess                       // a smaller number tightens
	tightenRank                       // a later rank tightens
	tightenAdd                        // additive: both texts stand
	tightenSwitch                     // a level on beats off
)

// tightenRule is the rule of one resolved path, tightenReplace for a key
// overlay replaces whole.
func tightenRule(path string) tightenKind {
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
	case strings.HasSuffix(level, ".focus"):
		return tightenAdd
	case strings.HasSuffix(level, ".none"):
		return tightenSwitch
	}
	return tightenReplace
}

// mergeState is one merge's record: where each key came from, and what the
// tighten rule did with the values the repository's layer wrote.
type mergeState struct {
	sources   map[string]string
	ignored   []policy.Ignored
	unchecked []string
}

// record is the merge's tighten record, in path order.
func (m *mergeState) record() ([]policy.Ignored, []string) {
	slices.SortFunc(m.ignored, func(a, b policy.Ignored) int { return strings.Compare(a.Key, b.Key) })
	slices.Sort(m.unchecked)
	return m.ignored, m.unchecked
}

// tightenLeaf applies the rule to one leaf the repository's layer writes, and
// says whether it is written into the merged tree. It is a no-op for the
// project's own layer, for a key overlay replaces and for a value that is not
// a scalar: a list is one key, replaced whole, and no tighten key is one.
func (m *mergeState) tightenLeaf(dst map[string]any, k string, v any, path, source string) bool {
	kind := tightenRule(path)
	if source != SourceRepo || kind == tightenReplace || kind == tightenSwitch {
		// tightenSwitch is the level's own rule and mergeLevel's, because a
		// level is off in two spellings and the project's answer is the whole
		// block's, not one key's.
		return true
	}
	repo, ok := v.(*yaml.Node)
	if !ok {
		return true
	}
	cur, ok := dst[k].(*yaml.Node)
	if !ok {
		// The project says nothing about this key: the repository's value
		// stands, unjudged (R3).
		m.unchecked = append(m.unchecked, path)
		return true
	}
	wins, repoText, projText := tightenWins(path, kind, repo, cur)
	if wins {
		return true
	}
	if repoText != projText {
		m.ignored = append(m.ignored, policy.Ignored{Key: path, Value: repoText, Effective: projText, Source: SourceProject})
	}
	return false
}

// tightenWins reports whether a repository's value tightens the project's,
// with both as the text their own file wrote: the Ignored record renders a
// value the way it was written, not the way this program would format it
// (resolve.go's nodeTree keeps the text and tag a value was written with). A
// pair it cannot judge — a value the decoder refuses, which Validate reports
// itself — is kept, so the merge never hides the refusal of a file.
func tightenWins(path string, kind tightenKind, repo, project *yaml.Node) (bool, string, string) {
	repoText, projText := repo.Value, project.Value
	switch kind {
	case tightenMore, tightenLess:
		r, rok := nodeNumber(repo)
		p, pok := nodeNumber(project)
		if rok && pok {
			if kind == tightenMore {
				return r > p, repoText, projText
			}
			return r < p, repoText, projText
		}
	case tightenRank:
		r, rok := nodeRank(path, repo)
		p, pok := nodeRank(path, project)
		if rok && pok {
			return r > p, repoText, projText
		}
	}
	return true, repoText, projText
}

// nodeNumber is a scalar as a number, false for anything the decoder does not
// read as one.
func nodeNumber(n *yaml.Node) (float64, bool) {
	var f float64
	if n == nil || n.Decode(&f) != nil {
		return 0, false
	}
	return f, true
}

// nodeRank is a scalar's rank in its key's own order: off < auto < on for
// agent.first_line_review, and its index in config.Efforts for an effort.
func nodeRank(path string, n *yaml.Node) (int, bool) {
	var s string
	if n == nil || n.Decode(&s) != nil {
		return 0, false
	}
	if path == "agent.first_line_review" {
		switch s {
		case FirstLineOff:
			return 0, true
		case FirstLineAuto:
			return 1, true
		case FirstLineOn:
			return 2, true
		}
		return 0, false
	}
	if i := slices.Index(Efforts, s); i >= 0 {
		return i, true
	}
	return 0, false
}

// uncheckedIn is every tighten-only key a repository's tree writes, in path
// order: the arm with no project layer has nothing to tighten against, so
// every one of them is kept unjudged (R3), exactly as the layer arm marks one
// the project says nothing about.
func uncheckedIn(tree map[string]any) []string {
	var out []string
	var walk func(m map[string]any, path string)
	walk = func(m map[string]any, path string) {
		for _, k := range sortedKeys(m) {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if sub, ok := m[k].(map[string]any); ok {
				walk(sub, p)
				continue
			}
			if m[k] == nil {
				continue
			}
			if tightenRule(p) != tightenReplace {
				out = append(out, p)
			}
		}
	}
	walk(tree, "")
	slices.Sort(out)
	return out
}

// tightenFocus is the one tighten-only key the merge does not settle: focus is
// additive, so the resolved level carries the project's text and the
// repository's, one line each (spec §5.2, "both quoted"), and runner's
// FocusPrompt quotes them one "> " at a time.
//
// It runs over the resolved config, after Validate: the bound and the
// one-line rule (LevelSetting.validate) are about one layer's own text, and
// the combination is neither layer's — a project that writes a long focus
// must not make a repository's file invalid. Each layer's own text was
// checked where that layer was written.
func tightenFocus(c *Config, repo, layer map[string]any) {
	for _, l := range []struct {
		name string
		set  *LevelSetting
	}{{"l1", c.Agent.Levels.L1}, {"l2", c.Agent.Levels.L2}, {"l3", c.Agent.Levels.L3}} {
		if l.set == nil {
			continue
		}
		proj, repoText := focusText(layer, l.name), focusText(repo, l.name)
		if proj == "" || repoText == "" || proj == repoText {
			continue
		}
		l.set.Focus = proj + "\n\n" + repoText
	}
}

// focusText is the focus one tree's level block writes, "" for none.
func focusText(tree map[string]any, level string) string {
	agent, _ := tree["agent"].(map[string]any)
	levels, _ := agent["levels"].(map[string]any)
	block, _ := levels[level].(map[string]any)
	n, _ := block["focus"].(*yaml.Node)
	var s string
	if n == nil || n.Decode(&s) != nil {
		return ""
	}
	return s
}

// withoutKeys is m without the named keys, a copy: the tree it comes from is
// the file as written and is read again by the arm that validates it.
func withoutKeys(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}
