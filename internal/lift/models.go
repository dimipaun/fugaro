package lift

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanModels is B3's planner (MC1): the model a run's stages choose moves out of
// a repository's own fugaro.yaml and into the project layer's defaults.agent.*,
// where the project answers it for every repository of it (D3237, spec §5.1).
//
// One port per lifted key — agent.model, the five role slots, the five crafts'
// models, and the three review levels' (D3291) — and one rule per port:
//
//   - A port the project already sets keeps the project's value. A repository
//     that wrote another one is named and its own value is not moved: a value a
//     person set in the project is never replaced by a repository's file (C2),
//     and the write release A makes is the drop in config.Resolve, not this
//     planner's.
//   - A port the project is silent about takes the value the repositories
//     write: the one most of them write, and, where two are written by as many,
//     the cheaper of them in the price table (D3290). Every repository whose
//     model that changes is named — the ones that wrote another value, and the
//     ones that wrote nothing at all, which move off Fugaro's default to the
//     project's value — before any write, which is the HARD RULE on
//     `fugaro update`.
//   - A repository whose own budget.allowed_models (B7, kept) does not admit a
//     model is no vote for it (R17): a value its runs may not use is not one
//     they get to choose for every other repository of the project. Where that
//     leaves the port silent, the value does not move at all and the
//     repository is told so; where the port takes a value the repository's
//     allow-list refuses, the move is named with the fix. Either way
//     budget.allowed_models itself is left where it is: it narrows one
//     repository and is no part of the choice.
//
// It answers no error: every repository it cannot move — one update has already
// migrated, one nothing could be read for, one the reader could not read at all
// — is a line and not a refusal, so `update` converges for the rest (C2).
func PlanModels(p Project, repos []Repo) (Change, error) {
	var reads []modelRead
	seen := map[string]bool{}
	for _, r := range repos {
		if r.Unreadable != "" {
			// M2 names every repository the reader could not read, in a line
			// of its own (D3306), and no planner acts on one.
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			reads = append(reads, modelRead{repo: r, state: modelBroken,
				broken: "its provider and name are not a repository this can key (" + err.Error() + ")"})
			continue
		}
		if seen[slug] {
			// One repository named twice is one repository: its model is one
			// vote for the project's value and its record is written once.
			continue
		}
		seen[slug] = true
		if p.Migrated[slug] {
			reads = append(reads, modelRead{repo: r, slug: slug, state: modelMigrated})
			continue
		}
		f, err := readModelFile(r.Config)
		if err != nil {
			reads = append(reads, modelRead{repo: r, slug: slug, state: modelBroken, broken: err.Error()})
			continue
		}
		reads = append(reads, modelRead{repo: r, slug: slug, state: modelActs, models: f.models, allow: f.allow})
	}

	chosen := make(map[string]string, len(modelPorts))
	held := make(map[string]bool, len(modelPorts))
	var c Change
	for _, port := range modelPorts {
		v := layerScalar(p.Layer, port.home)
		if v == "" {
			v = majorityOf(port, reads, p.Prices)
			if v != "" {
				c.LayerEdits = append(c.LayerEdits, LayerEdit{Path: slices.Clone(port.home), Value: v})
			}
		} else {
			held[port.key] = true
		}
		chosen[port.key] = v
	}

	for _, rd := range reads {
		switch rd.state {
		case modelActs:
			c.Records = append(c.Records, rd.slug)
			c.Lines = append(c.Lines, movedLines(rd, chosen, held)...)
		case modelMigrated:
			c.Lines = append(c.Lines, leftoverLines(rd.repo)...)
		default:
			c.Lines = append(c.Lines,
				fmt.Sprintf("%s: %s, so no model of it moved and the project's values stand", rd.repo.Repo, rd.broken))
		}
	}
	return c, nil
}

// modelRead is one repository of the slice as B3's planner read it: the models
// its own file writes, its allow-list, and which of the three answers the
// planner has for it.
type modelRead struct {
	repo   Repo
	slug   string
	state  modelState
	models map[string]string
	allow  []string
	// broken is why nothing could be read for this repository, as the second
	// half of update's line about it.
	broken string
}

type modelState int

const (
	// modelActs is a repository the migration may move: readable, and with no
	// record yet (D3305).
	modelActs modelState = iota
	// modelMigrated is a repository update has already migrated: this planner
	// names a key its file still writes and moves nothing (D3305).
	modelMigrated
	// modelBroken is a repository whose file, or whose key, this cannot read:
	// it is named and left alone.
	modelBroken
)

// movedLines is one repository's own lines: one per port whose model the
// project's value changes for it, in path order.
func movedLines(rd modelRead, chosen map[string]string, held map[string]bool) []string {
	var out []string
	for _, port := range modelPorts {
		v, was := chosen[port.key], rd.models[port.key]
		if v == "" {
			// The repository wrote a model, and it was no vote for itself: its
			// own allow-list refuses it (R17). Nothing is moved and release A
			// drops the key, so its runs need a model from somewhere — the
			// line says which two places it can come from.
			if was != "" {
				out = append(out, fmt.Sprintf("%s: %s %s did not move (its budget.allowed_models is %s): add it there, or set the project's model to one it allows",
					rd.repo.Repo, port.label, was, strings.Join(rd.allow, ", ")))
			}
			continue
		}
		// A repository that wrote nothing is named only where the project
		// gained the value: one the project already set was already its answer
		// for that repository, so no run of it changes.
		if was == v || (was == "" && held[port.key]) {
			continue
		}
		line := fmt.Sprintf("%s: %s %s -> %s (the project's)", rd.repo.Repo, port.label, orDefault(was), v)
		if !allows(rd.allow, v) {
			line += fmt.Sprintf("; %s is not in its budget.allowed_models (%s): add it there, or set the project's model to one it allows",
				v, strings.Join(rd.allow, ", "))
		}
		out = append(out, line)
	}
	return out
}

// leftoverLines is every moved key a repository update has already migrated
// still writes. C2's rule is that the planner names such a repository and never
// applies the value: update cannot commit to a repository, so removing the key
// is a person's to do (PR5 offers the diff). The sentence is
// config.LiftedKeyNotes', which is the one release B prints, so the same words
// reach a person at `update`, at `validate` and at a launch.
func leftoverLines(r Repo) []string {
	var out []string
	for _, note := range config.LiftedKeyNotes(r.Config) {
		if isModelKey(note.Path) {
			out = append(out, r.Repo+": "+note.Why)
		}
	}
	return out
}

// orDefault is the repository's model as a person reads it: the name it wrote,
// or Fugaro's own default for the port, which is what a file that writes
// nothing leaves it at.
func orDefault(model string) string {
	if model == "" {
		return "Fugaro's default"
	}
	return model
}

// majorityOf is D3290's answer for a port the project is silent about: the
// value most repositories write, and, where two are written by as many, the
// cheaper of them in the price table. A repository whose own allow-list refuses
// a value is no vote for it (R17), and "" is the answer when no repository's
// vote counts — an answer that moves nothing rather than a value nobody who
// wrote one may run.
func majorityOf(port modelPort, reads []modelRead, prices *pricing.Table) string {
	counts := map[string]int{}
	for _, rd := range reads {
		m := rd.models[port.key]
		if rd.state != modelActs || m == "" || !allows(rd.allow, m) {
			continue
		}
		counts[m]++
	}
	best := ""
	for m, n := range counts {
		if best == "" || n > counts[best] || (n == counts[best] && cheaper(prices, m, best)) {
			best = m
		}
	}
	return best
}

// cheaper reports whether a is the cheaper of two models in the price table:
// what its input costs a million tokens first and its output second, so the
// answer is one number, and the ID itself last, so a tie is still decided the
// same way on every run rather than by whichever the map held first. A model
// the table prices nowhere is the dearest there is, at both rates: the table's
// own rule is that nothing is priced free.
func cheaper(prices *pricing.Table, a, b string) bool {
	ai, ao := ratesOf(prices, a)
	bi, bo := ratesOf(prices, b)
	switch {
	case ai != bi:
		return ai < bi
	case ao != bo:
		return ao < bo
	}
	return a < b
}

func ratesOf(prices *pricing.Table, model string) (float64, float64) {
	if m, ok := prices.Lookup(model); ok {
		return m.Rates.InputPerM, m.Rates.OutputPerM
	}
	return math.Inf(1), math.Inf(1)
}

// allows reports whether a repository's own budget.allowed_models admits a
// model. A list it does not write admits every model, and an empty one is
// refused by the file's own rules rather than read here as "nothing is
// allowed" — which would let a repository with a broken list decide the
// project's model for every other repository of it.
func allows(allow []string, model string) bool {
	return len(allow) == 0 || slices.Contains(allow, model)
}

// modelFile is a repository's file as B3's planner reads it.
type modelFile struct {
	// models is the model each of B3's keys holds, "" where the file holds
	// none.
	models map[string]string
	// allow is budget.allowed_models, nil where the file writes none.
	allow []string
}

// readModelFile reads a repository's own fugaro.yaml the way the migration
// reads it: key by key, as a document, rather than through a whole-file strict
// decode — so a file that carries a key this Fugaro does not know (one written
// for a later release) still says what it says about B3's keys, which is how
// config.LiftedKeyNotes reads the same bytes. An error is a file no key of
// which can be trusted: YAML it cannot parse, a key written twice, or one of
// B3's keys, or the allow-list, written in a shape no model name or list of
// them is.
func readModelFile(data []byte) (*modelFile, error) {
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("its fugaro.yaml cannot be read as Fugaro reads one: %w", err)
	}
	out := &modelFile{models: make(map[string]string, len(modelPorts))}
	for _, port := range modelPorts {
		v, ok := scalarAt(tree, strings.Split(port.key, "."))
		if !ok {
			return nil, fmt.Errorf("its fugaro.yaml writes %s in a shape no model name is", port.key)
		}
		if port.noneSwitch && v == config.NoneModel {
			// `model: none` is the level's own switch and not a model: writing
			// the literal into the project layer would turn that level off for
			// every repository of the project.
			v = ""
		}
		out.models[port.key] = v
	}
	allow, ok := listAt(tree, []string{"budget", "allowed_models"})
	if !ok {
		return nil, errors.New("its fugaro.yaml writes budget.allowed_models in a shape no list of model IDs is")
	}
	out.allow = allow
	return out, nil
}

// modelPort is one of B3's keys: the path a repository's own file writes it at,
// the project layer's home for it, the sentence release B prints about it, and
// the name update's line calls it by.
type modelPort struct {
	key   string
	home  []string
	why   string
	label string
	// noneSwitch marks the three review levels, whose `model: none` is the
	// level's own switch rather than a model (config.NoneModel).
	noneSwitch bool
}

// modelPorts is B3's keys, taken from config.LiftedKeys rather than spelled
// again — the list is the only answer to "is this key lifted" (C1) — so the
// home a value moves to and the sentence a person reads are C1's, and a key the
// list gains is one this planner moves. A model key with no name for update's
// line is a diff to settle, not a key to migrate in silence, so the package
// refuses to start over it (as the settings and planner registries do).
var modelPorts = modelPortList()

func modelPortList() []modelPort {
	var out []modelPort
	for _, k := range config.LiftedKeys {
		if !isModelKey(k.Key) {
			continue
		}
		label, ok := modelLabels[k.Key]
		if !ok {
			panic("internal/lift: " + k.Key + " is a model key with no name for update's line")
		}
		out = append(out, modelPort{
			key:        k.Key,
			home:       strings.Split(k.Home, "."),
			why:        k.Why,
			label:      label,
			noneSwitch: strings.HasPrefix(k.Key, "agent.levels."),
		})
	}
	return out
}

// isModelKey reports whether a lifted key is B3's: the model a role, a craft or
// a review level runs as (spec §5.1's keys, and D3291 for the levels). The four
// other keys the list holds — ab.rate, rigor.default, rigor.areas and L13's two
// grades (D3299) — are another planner's each and are no model of a run's
// stages.
func isModelKey(key string) bool {
	switch {
	case key == "agent.model":
		return true
	case strings.HasPrefix(key, "agent.models."):
		return true
	case strings.HasPrefix(key, "agent.crafts."), strings.HasPrefix(key, "agent.levels."):
		return strings.HasSuffix(key, ".model")
	}
	return false
}

// modelLabels names each of B3's keys the way update's line names it: "acme/app:
// coder claude-x -> claude-y (the project's)". A role, a craft and a level are
// words a person reads; the key's own path is not, and the line is read next to
// a project's other lines.
var modelLabels = map[string]string{
	"agent.model":                        "model",
	"agent.models.coder":                 "coder",
	"agent.models.reviewer":              "reviewer",
	"agent.models.background":            "background",
	"agent.models.vision":                "vision",
	"agent.models.diagnose":              "diagnose",
	"agent.crafts.coder.model":           "craft coder",
	"agent.crafts.review.model":          "craft review",
	"agent.crafts.senior-review.model":   "craft senior-review",
	"agent.crafts.security-review.model": "craft security-review",
	"agent.crafts.merge-judge.model":     "craft merge-judge",
	"agent.levels.l1.model":              "level l1",
	"agent.levels.l2.model":              "level l2",
	"agent.levels.l3.model":              "level l3",
}

// layerScalar is the scalar the project layer's tree holds at path, "" when it
// holds none: a layer that writes the key anywhere is what makes the project
// the answer for it (C2's "never overwrite a project value").
func layerScalar(layer map[string]any, path []string) string {
	v, _ := scalarAt(layer, path)
	return v
}

// valueAt is what a decoded document holds at path. A path the document does
// not write answers nil — an unwritten key, and not an unreadable one — and so
// does a path that runs through YAML's null: a block whose members are all
// commented out, or a key with nothing indented under it, is a file the strict
// decode reads as the zero value, so it says nothing at that key rather than
// something no key of B3's and no layer scalar can be under. What answers ok
// false is a path that runs through something that is neither a mapping nor
// null — a scalar, a list — which no Fugaro config shape has under it.
func valueAt(tree map[string]any, path []string) (any, bool) {
	cur := any(tree)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			if cur == nil {
				return nil, true
			}
			return nil, false
		}
		v, has := m[seg]
		if !has {
			return nil, true
		}
		cur = v
	}
	return cur, true
}

// scalarAt is the string a decoded document holds at path, "" when it holds
// nothing there and ok false when it holds something that is not a string.
func scalarAt(tree map[string]any, path []string) (string, bool) {
	v, ok := valueAt(tree, path)
	if !ok || v == nil {
		return "", ok
	}
	s, isStr := v.(string)
	return s, isStr
}

// listAt is the list of strings a decoded document holds at path, nil when it
// holds nothing there and ok false when it holds anything else.
func listAt(tree map[string]any, path []string) ([]string, bool) {
	v, ok := valueAt(tree, path)
	if !ok || v == nil {
		return nil, ok
	}
	list, isList := v.([]any)
	if !isList {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, isStr := e.(string)
		if !isStr {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
