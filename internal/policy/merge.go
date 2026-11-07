package policy

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
)

func modeRank(m string) int {
	switch m {
	case ModeOff:
		return 1
	case ModeObserve:
		return 2
	case ModeEnforce:
		return 3
	}
	return 0 // unset or unknown: carries no value
}

// layerName names the i-th layer: 0 is the ceiling, 1 the default branch, and
// anything after that the run's branch.
func layerName(i int) string {
	switch i {
	case 0:
		return SourceCeiling
	case 1:
		return SourceDefaultBranch
	}
	return SourceBranch
}

// Merge applies layers in order (the ceiling first, then tighten[0] as the
// default branch, tighten[1] as the run's branch). A later layer may only
// tighten: the smaller positive cap, the stricter mode (enforce > observe >
// off), the intersection of allow-lists. A key a layer leaves unset passes the
// earlier layers through. A value a later layer sets that does not tighten is
// dropped and recorded in Ignored (equal values are not ignored; the ceiling
// is never ignored). Later layers are compared to the running effective value.
func Merge(ceiling Layer, tighten ...Layer) Effective {
	e := Effective{Sources: map[string]string{}}
	layers := append([]Layer{ceiling}, tighten...)
	for i, l := range layers {
		src := layerName(i)

		if l.PerRunUSD > 0 && !math.IsInf(l.PerRunUSD, 0) { // NaN fails > 0; +Inf is unset
			switch {
			case e.PerRunUSD == 0:
				e.PerRunUSD, e.Sources[KeyPerRunUSD] = l.PerRunUSD, src
			case l.PerRunUSD < e.PerRunUSD:
				e.PerRunUSD, e.Sources[KeyPerRunUSD] = l.PerRunUSD, src
			case l.PerRunUSD > e.PerRunUSD:
				e.Ignored = append(e.Ignored, Ignored{KeyPerRunUSD,
					strconv.FormatFloat(l.PerRunUSD, 'f', -1, 64),
					strconv.FormatFloat(e.PerRunUSD, 'f', -1, 64), e.Sources[KeyPerRunUSD]})
			}
		}

		e.tightenCap(&e.PerDayUSD, l.PerDayUSD, KeyPerDayUSD, src)

		// The A/B rate is a bound like the caps above, and the one key of the
		// ab: block that crosses layers: the project layer sets it and a
		// repository may only lower it, to 0, which never samples that
		// repository (D556, D557). A layer that writes no rate passes the
		// earlier layers through, which is a nil ABRate and not a 0 one: the
		// opt-out is the value 0, so the two cannot share the zero value. No
		// layer carries a rate yet, so the rule is unexercised by a real
		// configuration until the launcher that fills it ships (Layer.ABRate).
		if l.ABRate != nil {
			switch {
			case e.ABRate == nil || *l.ABRate < *e.ABRate:
				e.ABRate, e.Sources[KeyABRate] = l.ABRate, src
			case *l.ABRate > *e.ABRate:
				e.Ignored = append(e.Ignored, Ignored{KeyABRate,
					strconv.FormatFloat(*l.ABRate, 'f', -1, 64),
					strconv.FormatFloat(*e.ABRate, 'f', -1, 64), e.Sources[KeyABRate]})
			}
		}

		if l.MaxRunTokens > 0 {
			switch {
			case e.MaxRunTokens == 0 || l.MaxRunTokens < e.MaxRunTokens:
				e.MaxRunTokens, e.Sources[KeyMaxRunTokens] = l.MaxRunTokens, src
			case l.MaxRunTokens > e.MaxRunTokens:
				e.Ignored = append(e.Ignored, Ignored{KeyMaxRunTokens,
					strconv.FormatInt(l.MaxRunTokens, 10),
					strconv.FormatInt(e.MaxRunTokens, 10), e.Sources[KeyMaxRunTokens]})
			}
		}

		e.tightenInt(&e.MaxOutputCoder, l.MaxOutputCoder, KeyMaxOutputCoder, src)
		e.tightenInt(&e.MaxOutputReviewer, l.MaxOutputReviewer, KeyMaxOutputReviewer, src)

		// Governance is the same rule over a different vocabulary: every key
		// is a positive bound whose smaller value is the stricter one, so a
		// layer that writes none of them (the job environment, the published
		// policy node, a repository that has no governance: block) tightens
		// nothing and the ceiling it was handed stands. The rule is asked of
		// the policy hook (hook.go), whose default tightens nothing at all: an
		// untagged build decodes the block and holds no run to it, and the
		// rule itself is ee/governance's (§5.2, D522).
		governanceHook().Tighten(&e, l.Governance, src)

		// The review-level floor is the one key whose "stricter" is a union
		// rather than a comparison: a level a layer requires is a level every
		// layer under it must run, so a later layer can add one and can never
		// take one away (D722, G1). It is also the one key no launcher-written
		// layer is given: the owner's floor arrives on the ceiling, from the
		// policy node (Layer.ReviewFloor).
		if len(l.ReviewFloor) > 0 {
			e.unionReviewFloor(l.ReviewFloor)
		}

		// The orchestrator's guardrails (D1239) are the caps rule over one
		// more block of counts: a layer that writes none of them tightens
		// nothing, and the daemon's ceiling is the guardrails Fugaro ships
		// (WithDefaults) with every layer under them applied in turn. The
		// model of a decision class is the one key of the block that is not a
		// count, and it tightens by the tier order instead (D1234).
		o := l.Orchestrator
		e.tightenInt(&e.Orchestrator.MaxRunning, o.MaxRunning, KeyOrchestratorMaxRunning, src)
		e.tightenInt(&e.Orchestrator.BatchSize, o.BatchSize, KeyOrchestratorBatchSize, src)
		e.tightenInt(&e.Orchestrator.SpawnPerMinute, o.SpawnPerMinute, KeyOrchestratorSpawnPerMinute, src)
		e.tightenInt(&e.Orchestrator.SpawnPerHour, o.SpawnPerHour, KeyOrchestratorSpawnPerHour, src)
		e.tightenInt(&e.Orchestrator.SpawnPerDay, o.SpawnPerDay, KeyOrchestratorSpawnPerDay, src)
		for _, class := range DecisionClasses {
			e.tightenDecisionModel(class, o.DecisionModelPerClass[class], src)
		}

		if r := modeRank(l.Mode); r > 0 {
			cur := modeRank(e.Mode)
			switch {
			case r > cur:
				e.Mode, e.Sources[KeyMode] = l.Mode, src
			case r < cur:
				e.Ignored = append(e.Ignored, Ignored{KeyMode, l.Mode, e.Mode, e.Sources[KeyMode]})
			}
		}

		if l.AllowedModels != nil {
			if e.AllowedModels == nil {
				e.AllowedModels, e.Sources[KeyAllowedModels] = dedupe(l.AllowedModels), src
			} else {
				in, running := toSet(l.AllowedModels), toSet(e.AllowedModels)
				kept := make([]string, 0, len(e.AllowedModels))
				for _, m := range e.AllowedModels { // running order is kept
					if in[m] {
						kept = append(kept, m)
					}
				}
				extra := false
				for _, v := range l.AllowedModels {
					if !running[v] {
						extra = true
						break
					}
				}
				prev := e.Sources[KeyAllowedModels]
				if len(kept) < len(e.AllowedModels) {
					e.AllowedModels, e.Sources[KeyAllowedModels] = kept, src
				}
				if extra { // asked for a model the earlier layers had excluded
					e.Ignored = append(e.Ignored, Ignored{KeyAllowedModels,
						strings.Join(dedupe(l.AllowedModels), ","),
						strings.Join(e.AllowedModels, ","), prev})
				}
			}
		}
	}
	if len(e.Sources) == 0 {
		e.Sources = nil
	}
	return e
}

// unionReviewFloor adds l's levels to the effective floor, mode by mode, and
// keeps each mode's levels in the vocabulary's order (Levels). Nothing is
// recorded in Ignored: a union has no loser — the levels a layer asked for
// are all required, and the levels it left out are the ones no layer asked
// for.
//
// Every slice the result holds is built here, so neither the layers' maps nor
// their slices are aliased or written to.
func (e *Effective) unionReviewFloor(l map[rigor.Mode][]Level) {
	for mode, added := range l {
		running := e.Layer.ReviewFloor[mode]
		var out []Level
		for _, lv := range Levels {
			if slices.Contains(added, lv) || slices.Contains(running, lv) {
				out = append(out, lv)
			}
		}
		if len(out) == 0 {
			continue
		}
		if e.Layer.ReviewFloor == nil {
			e.Layer.ReviewFloor = make(map[rigor.Mode][]Level, len(l))
		}
		e.Layer.ReviewFloor[mode] = out
	}
}

// TightenDuration is tightenDuration for the packages outside this one:
// ee/governance's Tighten and its tightenTier are the governance policy's
// (plan task E2.3, design ee-boundary-moves.md §5.2) and cannot reach an
// unexported method, so the moved code calls the rule here rather than keeping
// a copy of it that would drift from this one.
func (e *Effective) TightenDuration(cur *time.Duration, v time.Duration, key, src string) {
	e.tightenDuration(cur, v, key, src)
}

// tightenDuration is tightenInt for a stage timeout: a duration that is not
// positive is unset.
func (e *Effective) tightenDuration(cur *time.Duration, v time.Duration, key, src string) {
	if v <= 0 {
		return
	}
	switch {
	case *cur <= 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, v.String(), cur.String(), e.Sources[key]})
	}
}

// TightenCap is tightenCap for the packages outside this one, on the same
// terms as TightenDuration: the governance policy moves to ee/governance and
// calls the one rule rather than a copy of it.
func (e *Effective) TightenCap(cur *float64, v float64, key, src string) {
	e.tightenCap(cur, v, key, src)
}

// tightenCap is tightenInt for a positive float limit: a dollar cap, or a
// stage budget's fraction of one. NaN, +Inf, zero and negatives are unset.
func (e *Effective) tightenCap(cur *float64, v float64, key, src string) {
	if !(v > 0) || math.IsInf(v, 0) {
		return
	}
	switch {
	case *cur == 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, strconv.FormatFloat(v, 'f', -1, 64),
			strconv.FormatFloat(*cur, 'f', -1, 64), e.Sources[key]})
	}
}

// TightenInt is tightenInt for the packages outside this one, on the same
// terms as TightenDuration.
func (e *Effective) TightenInt(cur *int64, v int64, key, src string) {
	e.tightenInt(cur, v, key, src)
}

// tightenInt applies a layer's positive limit v to *cur: smaller wins, a
// larger one is ignored and recorded, 0 and below are unset.
func (e *Effective) tightenInt(cur *int64, v int64, key, src string) {
	if v <= 0 {
		return
	}
	switch {
	case *cur == 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, strconv.FormatInt(v, 10),
			strconv.FormatInt(*cur, 10), e.Sources[key]})
	}
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// The orchestrator's keys, as Effective.Sources and Ignored.Key spell them:
// the block the published policy node and the local config both carry under
// orchestrator: (D1239). Each class's own model key is built by
// DecisionModelKey, so the class vocabulary and the key spelling are one
// thing.
const (
	KeyOrchestratorMaxRunning     = "orchestrator.max_running"
	KeyOrchestratorBatchSize      = "orchestrator.batch_size"
	KeyOrchestratorSpawnPerMinute = "orchestrator.spawn_per_minute"
	KeyOrchestratorSpawnPerHour   = "orchestrator.spawn_per_hour"
	KeyOrchestratorSpawnPerDay    = "orchestrator.spawn_per_day"
)

// DecisionModelKey is the policy key of one decision class's model, as
// Effective.Sources and Ignored.Key spell it
// ("orchestrator.decision_model_per_class.routine").
func DecisionModelKey(class DecisionClass) string {
	return "orchestrator.decision_model_per_class." + string(class)
}

// The guardrail counts Fugaro ships (D1239): what a person and a project that
// write no orchestrator: block run under. They are the conservative end on
// purpose — the spec's batch of 50 lets one looping cycle launch 50 real runs
// before anyone is asked — and they enter as the ceiling (WithDefaults), which
// is why each of them is a bound every layer under it may only lower: a default
// nobody wrote is not a decision a layer may loosen.
//
// The running cap has no constant here. Its default is the person's own
// max_parallel — the launch limit `fugaro run` already counts active runs
// against, so the daemon and the CLI share one number — which is a fact about
// the installation and not a figure of this package; WithDefaults takes it as
// its argument.
const (
	DefaultBatchSize      = 10
	DefaultSpawnPerMinute = 2
	DefaultSpawnPerHour   = 12
	DefaultSpawnPerDay    = 60
)

// DecisionClass is the security-sensitivity class a decision is made under.
// Which class a decision belongs to is decided by code over the paths the
// change touches and never by a config key, a model's judgement or how the
// change describes itself (D1234): the blocks that carry DecisionClass say
// which MODEL a class runs on, and nothing here can move a decision between
// the two classes.
type DecisionClass string

const (
	// DecisionRoutine is a decision whose paths are all on the routine
	// allow-list.
	DecisionRoutine DecisionClass = "routine"
	// DecisionSensitive is a decision that touches a path the allow-list does
	// not cover, and the class whose model may never be lowered.
	DecisionSensitive DecisionClass = "sensitive"
)

// DecisionClasses is the closed vocabulary of the two classes, in the order a
// reader prints it.
var DecisionClasses = []DecisionClass{DecisionRoutine, DecisionSensitive}

// DecisionModels is the models a decision may run on, from the least to the
// most capable. That order is the whole of the tightening rule for
// decision_model_per_class: a layer may move a class's model up it and never
// down, and the Sensitive class may not move at all, because its floor is the
// top of the order (D1234, G6). The names are the ones Claude Code's own
// --model takes (internal/pricing's aliases), because the value is handed to
// `claude -p` as it stands.
var DecisionModels = []string{"haiku", "sonnet", "opus"}

// Orchestrator is one layer's block of the orchestrator's guardrails: how many
// runs the daemon may have running, how many one cycle may launch and how fast,
// and the model each decision class runs on (D1239, D1234).
//
// Every key's zero value means "unset", the rule every other field of a Layer
// follows, so a layer that writes no orchestrator: block tightens nothing. The
// five counts are positive numbers whose smaller value is the stricter one and
// are merged as caps; DecisionModelPerClass is the one key of the block that is
// not a count, and a class's model tightens by the tier order (DecisionModels)
// — a layer may name a more capable model for a class and never a less capable
// one.
//
// It carries the tags of both documents that hold this block: the published
// policy node's (JSON) and the local config's (YAML). That is deliberate — the
// two are one block of one set of rules, and a second carrier spelled beside
// this one would be a copy that could drift from it silently, which is the same
// reason internal/projectpolicy embeds config.ModelProvider rather than
// respelling it.
type Orchestrator struct {
	MaxRunning     int64 `json:"max_running,omitempty" yaml:"max_running,omitempty"`
	BatchSize      int64 `json:"batch_size,omitempty" yaml:"batch_size,omitempty"`
	SpawnPerMinute int64 `json:"spawn_per_minute,omitempty" yaml:"spawn_per_minute,omitempty"`
	SpawnPerHour   int64 `json:"spawn_per_hour,omitempty" yaml:"spawn_per_hour,omitempty"`
	SpawnPerDay    int64 `json:"spawn_per_day,omitempty" yaml:"spawn_per_day,omitempty"`
	// DecisionModelPerClass is the model a class's decisions run on, keyed by
	// the class's own name. A class the map does not carry is at its floor;
	// a name that is not a model is refused where the block is written
	// (ValidOrchestrator) and read as unset by the merge.
	DecisionModelPerClass map[DecisionClass]string `json:"decision_model_per_class,omitempty" yaml:"decision_model_per_class,omitempty"`
}

// WithDefaults is o with every key it leaves unset replaced by the guardrail
// Fugaro ships (D1239): the four counts are the constants above, the running
// cap is personMaxRunning, and each class's model is its floor — the bottom of
// the class's own range, which is haiku for a routine decision and opus for a
// sensitive one.
//
// The result is a ceiling layer: it is merged first, so a project and a person
// who write blocks may only tighten what this returned, and a key the layer did
// set is kept exactly as written. The map it returns is its own, so a caller
// that writes to the result does not write into the receiver.
//
// personMaxRunning is the person's own launch limit — the max_parallel the CLI
// counts a person's active runs against — and it reaches this package as an
// argument because it is a fact about the installation. A caller with no limit
// to give passes 0 or less, which leaves the running cap unset; Merge reads an
// unset cap as no cap, so the caller that has the config file in hand is the
// one that must pass the value (localcfg.Parse holds it to 1 or more).
func (o Orchestrator) WithDefaults(personMaxRunning int64) Orchestrator {
	if o.MaxRunning == 0 {
		o.MaxRunning = personMaxRunning
	}
	if o.BatchSize == 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.SpawnPerMinute == 0 {
		o.SpawnPerMinute = DefaultSpawnPerMinute
	}
	if o.SpawnPerHour == 0 {
		o.SpawnPerHour = DefaultSpawnPerHour
	}
	if o.SpawnPerDay == 0 {
		o.SpawnPerDay = DefaultSpawnPerDay
	}
	models := make(map[DecisionClass]string, len(DecisionClasses))
	for _, class := range DecisionClasses {
		if name := o.DecisionModelPerClass[class]; name != "" {
			models[class] = name
			continue
		}
		models[class] = classFloorModel(class)
	}
	o.DecisionModelPerClass = models
	return o
}

// ValidOrchestrator holds a written block to its rules. It is the one door for
// both writers — the published policy node's decoder and the local config's
// loader — so the two cannot disagree about what a block may say, the way
// ParseFloor and ValidVerifierAccounts are one door for their own keys.
//
// The refusals are a count that is not positive, a class that is not one of
// DecisionClasses, a class written with no model at all, and a model that is
// outside DecisionModels or under the floor of the class it is written for.
// They live here rather than in the merge because Merge is total: it reads any
// value it cannot place as unset, so a block that skipped this check would
// tighten nothing rather than be refused, silently, and the guardrail would
// read as the default.
//
// A nil or empty block is valid: an absent block and one written empty are the
// same document, and they set nothing.
func ValidOrchestrator(o *Orchestrator) error {
	if o == nil {
		return nil
	}
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("orchestrator."+f, a...)) }
	for _, count := range []struct {
		key string
		v   int64
	}{
		{"max_running", o.MaxRunning},
		{"batch_size", o.BatchSize},
		{"spawn_per_minute", o.SpawnPerMinute},
		{"spawn_per_hour", o.SpawnPerHour},
		{"spawn_per_day", o.SpawnPerDay},
	} {
		if count.v < 0 {
			bad("%s: %d is not a number of runs: a guardrail is a positive count, and a key left out takes the default", count.key, count.v)
		}
	}
	for _, class := range slices.Sorted(maps.Keys(o.DecisionModelPerClass)) {
		name := o.DecisionModelPerClass[class]
		switch {
		case name == "":
			bad("decision_model_per_class.%s: no model is named; remove the class to leave it at its default", class)
		case !slices.Contains(DecisionClasses, class):
			bad("decision_model_per_class.%s: is not a decision class (%s)", class, vocabulary(DecisionClasses))
		default:
			switch rank, ok := modelRank(name); {
			case !ok:
				bad("decision_model_per_class.%s: %q is not a model this Fugaro knows (%s)", class, name, strings.Join(DecisionModels, ", "))
			case rank < classFloor(class):
				bad("decision_model_per_class.%s: %q is below the floor of this class (%s): a security-sensitive decision is never run on a cheaper model (D1234)", class, name, classFloorModel(class))
			}
		}
	}
	return errors.Join(errs...)
}

// modelRank is v's position in the tier order (DecisionModels), and false for a
// name the order does not hold. A name with no rank carries no value at all —
// the answer Merge gives a mode that is not one of the three — while a name
// that has a rank but is under its class's floor is a value the merge compares
// and drops like any other loser, because it is on the order and lost.
func modelRank(v string) (int, bool) {
	i := slices.Index(DecisionModels, v)
	return i, i >= 0
}

// classFloor is the rank a class may never go under. The Sensitive class is
// floored at opus, the most capable model of the order, so no layer can lower
// it — and no name above it exists to raise it to either (D1234, G6). The
// Routine class has no floor of its own and starts at haiku, the bottom of the
// order, where every layer can only raise it.
func classFloor(class DecisionClass) int {
	if class == DecisionSensitive {
		return len(DecisionModels) - 1
	}
	return 0
}

// classFloorModel is the model a class starts at and may never go under: the
// bottom of its own range (WithDefaults, ValidOrchestrator).
func classFloorModel(class DecisionClass) string { return DecisionModels[classFloor(class)] }

// tightenDecisionModel applies one layer's model for a class to the running
// one. The comparison is the tier order — a more capable model is the stricter
// one — so the higher rank wins and a lower one is recorded, which is a cap's
// rule over a different scale; each class is compared with its own running
// value and never with the other class.
//
// A name with no rank carries no value at all: it is refused where the layer is
// written (ValidOrchestrator) and read as unset here, the way Merge reads a
// mode that is not one of the three. A name that has a rank and loses — under
// the class's floor included, which only a layer built by hand can carry, since
// the writers refuse the block — is a value the merge did read and drop, so it
// is recorded in Ignored like any other loser.
//
// The class's floor is not this function's rule, and it does not need to be:
// no layer can lower the running value, and the running value starts at the
// floor (WithDefaults), so the Sensitive class cannot be lowered from opus by
// any sequence of layers — the floor holds in the merge's own direction, while
// ValidOrchestrator is what refuses a block that wrote it lower.
func (e *Effective) tightenDecisionModel(class DecisionClass, v, src string) {
	if v == "" {
		return
	}
	rank, ok := modelRank(v)
	if !ok {
		return
	}
	key := DecisionModelKey(class)
	cur := e.Orchestrator.DecisionModelPerClass[class]
	curRank := -1
	if r, ok := modelRank(cur); ok {
		curRank = r
	}
	switch {
	case cur == "" || rank > curRank:
		if e.Orchestrator.DecisionModelPerClass == nil {
			e.Orchestrator.DecisionModelPerClass = make(map[DecisionClass]string, len(DecisionClasses))
		}
		e.Orchestrator.DecisionModelPerClass[class] = v
		e.Sources[key] = src
	case rank < curRank:
		e.Ignored = append(e.Ignored, Ignored{key, v, cur, e.Sources[key]})
	}
}

// dedupe returns a copy of s without repeats, order kept; non-nil for non-nil s.
func dedupe(s []string) []string {
	out := make([]string, 0, len(s))
	seen := map[string]bool{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
