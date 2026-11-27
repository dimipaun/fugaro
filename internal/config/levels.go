package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The review-level resolver (L3.1, D721, D722, D723, D725): the one place a
// run's three review levels are decided.
//
// Precedence is the rigor mode's own defaults, then the merged configuration
// (the project layer and the repository, which config.Resolve has already
// merged key by key), then the task's own block, with the owner's floor
// applied last. The floor is read from the owner-only policy node
// (policy.Effective.ReviewFloor) and never from a layer a launcher writes,
// which is why it arrives as an input of its own rather than as a field of
// the configuration.
//
// The resolver only resolves. It does not build the runner's plan, and it
// does not touch the recipe: where a level is on and the recipe has no step
// for it, it says so (ResolvedLevel.Inserted) and the runner that holds the
// recipe inserts it (L4.2). The same function is the launcher's check (L3.2)
// and the runner's bootstrap check, so a task written straight to the runs
// bucket meets the same refusals as a task fugaro run wrote.

// ResolvedLevel is one review level after resolution: whether it runs, the
// model, effort and focus it runs with, and whether the recipe has no step
// for it. Every field but On is zero on a level that does not run.
type ResolvedLevel struct {
	// On is whether the level runs.
	On bool
	// Model is the exact model ID the level runs; "" leaves the model to
	// Claude Code's own default. A level with no layer naming one runs the
	// coder's model at level 1 and the reviewer's at levels 2 and 3, after
	// the recipe's roles mapping.
	Model string
	// Effort is the level's reasoning effort, "" for the model's own
	// default. The set is closed (Efforts), and a provider route may carry
	// none at all (D725).
	Effort string
	// Focus is what this level's reviewer is asked to look at, "" for
	// nothing. It is data the reviewer's prompt quotes, never a command.
	Focus string
	// Inserted is whether the recipe has no step for this level, so the run
	// has to add one in the level's fixed place (O7): level 1 before the
	// review step, level 3 after it with bounce: first_line. Level 2's step
	// is the review step every loop recipe carries, so it is never true for
	// it, and an off level is never inserted.
	Inserted bool
}

// Levels is a run's three review levels, resolved.
type Levels struct {
	L1 ResolvedLevel
	L2 ResolvedLevel
	L3 ResolvedLevel
}

// Warning is one thing the resolver noticed about the levels that is not a
// refusal: the run may go on, and the launcher and the runner show it.
type Warning struct {
	// Level is the level the warning is about, in the vocabulary the
	// owner's floor writes.
	Level policy.Level
	// Message is the warning, in the terms the level's settings are written
	// in (l2.model, l2.effort).
	Message string
}

func (w Warning) String() string { return string(w.Level) + ": " + w.Message }

// LevelInputs is everything ResolveLevels reads.
type LevelInputs struct {
	// Config is the merged configuration the run starts from: the project
	// layer over the repository (config.Resolve), or a task's own copy of
	// it. Required.
	Config *Config
	// Mode is the run's rigor mode, and the first layer of the precedence.
	Mode rigor.Rigor
	// Floor is the owner's floor for the run's mode: the levels a run of
	// that mode must run whatever a lower layer asks for. nil is no floor.
	// It is the answer policy.Effective.ReviewFloorFor gives — the row of
	// Mode, and, when the run's own request lowered it below what the
	// layers resolved, the row of the mode they resolved beside it too,
	// because a request lowers a mode and never a floor (D8, LF5); a mode
	// the request took the run to outright (hotfix) reads its own row and
	// nothing beside it (the owner's ruling of 2026-11-20) — and never a row
	// a resolver reads itself.
	Floor []policy.Level
	// Task is the task's own review_levels block, nil for none. It is
	// written by the launcher, so it is the layer the resolver trusts
	// least: a `none` in it is what the floor exists to refuse. A level's
	// block in it that writes no key at all says nothing about that level
	// and leaves it to the configuration, exactly as an empty block does in
	// the merge between the two configuration layers (writesLevel).
	Task *ReviewLevels
	// Recipe is the recipe the run follows, nil when the caller has none.
	// It decides which levels already have a step and which a layer's
	// `none` removes.
	Recipe *recipe.Recipe
	// CatalogDefault is whether Recipe is the catalog's own `default`. It
	// is the one recipe whose first_line step runs only when
	// agent.first_line_review turns it on; every other recipe, a `default`
	// the repository or the project defines included, is taken as written
	// (docs/recipes.md §2), which is what runner.planOf calls derived.
	CatalogDefault bool
	// Providers are the installation's model providers by name. A model one
	// of them claims is routed to it, so an effort set for that model is
	// refused rather than sent (D725).
	Providers map[string]ModelProvider
	// AllowedModels is the effective allow-list
	// (policy.Effective.AllowedModels): the models a level may name. nil is
	// no list, which allows every model; an empty non-nil list allows none,
	// exactly as policy.Effective.HasAllowList reads it.
	AllowedModels []string
	// CraftModels is the model each craft resolved to, by craft name
	// (CraftCoder, CraftReview, CraftSeniorReview, CraftSecurityReview): the
	// caller's own craft resolution, read through craft.ModelFor (MC11), with
	// the inheritance already applied (the review craft follows the coder). It
	// is rung 3 of the model half's contract (D1660), so it answers a level
	// that writes no model of its own, above the role slot that level pins —
	// and it is the value MC12 writes into the stage's slot, so a level
	// resolved against it names the model the run really runs.
	//
	// A caller that resolved no crafts passes none, and every level then runs
	// its own model, else the role slot it pins, exactly as it did before the
	// crafts reached this resolver: an installation that sets no craft is
	// unchanged (D1662).
	CraftModels map[string]string
}

// ProjectSilent reports whether the project layer this configuration resolved
// over says nothing about a tighten-only key (spec §5.2): the question RT1's
// merge marks in Resolution.Unchecked, asked by the readers that hold a
// resolved configuration rather than the resolution it came from — the
// resolver below, the runner's plan, the launcher and the preview. A
// configuration that resolved over no layer at all is silent about every key,
// which is the layerless arm's own mark (uncheckedIn).
func (c *Config) ProjectSilent(path string) bool {
	if c == nil || c.Layer == nil {
		return true
	}
	return !c.Layer.SetsDefault(path)
}

// TightenRounds is one tighten-only round key as the run really reads it (R3,
// R20, RT2): the number a layer set, where it is the tighter of that and the
// mode's own row, and 0 — the layers' "unset" — where the project layer says
// nothing about the key and the mode's row is the higher of the two, so every
// reader takes that row instead and the plan records the mode as the one that
// decided (planStep.Precedence). Reading the mode's row rather than its number
// is what lets a mode a run raises between rounds supply the ones remaining
// (D22).
func TightenRounds(c *Config, key string, set, modeDefault int) int {
	if set > 0 && set <= modeDefault && c.ProjectSilent(key) {
		return 0
	}
	return set
}

// ResolveLevels decides the three review levels of one run.
//
// It returns the levels, the warnings a caller shows, and the refusal that
// stops the run. On a refusal the levels are the zero Levels: a caller must
// not act on a set of levels the run may not use.
func ResolveLevels(in LevelInputs) (Levels, []Warning, error) {
	if in.Config == nil {
		return Levels{}, nil, errors.New("review levels: there is no configuration to resolve against")
	}
	if in.Recipe != nil && in.Recipe.Mode == recipe.ModeArtifact {
		// An artifact run calls no reviewer at all (design
		// artifact-runs.md §6): its recipe takes one render, after an
		// optional plan, and nothing else, so it has no loop for a level to
		// join and there is nothing to resolve.
		return Levels{}, nil, nil
	}
	row, err := rigor.DefaultLevels(in.Mode)
	if err != nil {
		return Levels{}, nil, err
	}
	r := &resolver{
		in:    in,
		agent: withRecipeRoles(in.Config.Agent, in.Recipe),
		row:   row,
		loop:  in.Recipe != nil && in.Recipe.Mode == recipe.ModeImplement,
		// A mode: review recipe (review-only, pick) allows exactly one
		// review step and nothing else (recipe.Parse): it has level 2
		// alone. See reviewOnlyLevel for what happens to a level 1 or level
		// 3 something else asks for there.
		reviewOnly: in.Recipe != nil && in.Recipe.Mode == recipe.ModeReview,
	}
	r.run()
	if len(r.errs) > 0 {
		return Levels{}, r.warns, errors.Join(r.errs...)
	}
	return r.levels, r.warns, nil
}

// withRecipeRoles is the recipe's roles mapping applied to a copy of the
// agent's models: roles: {reviewer: coder} makes the reviewer run the coder's
// model and roles: {coder: reviewer} the other way round. It is
// runner.ApplyRoles' rule, which the resolver cannot call — internal/runner
// imports this package — and both halves have to agree, because the model a
// level resolves to is the model that runs.
func withRecipeRoles(a Agent, r *recipe.Recipe) Agent {
	if r == nil {
		return a
	}
	switch {
	case r.ReviewerIsCoder:
		a.Models.Reviewer = a.ModelFor(RoleCoder)
	case r.CoderIsReviewer:
		a.Models.Coder = a.ModelFor(RoleReviewer)
	}
	return a
}

// levelSpec is one level's three possible answers — the merged
// configuration's block, the task's and the mode's row — plus the craft and
// the role slot its model comes from when no layer names one.
type levelSpec struct {
	name policy.Level
	// craft is the craft the level's stages belong to (F13, D1663): the review
	// craft for level 1, the senior review's for level 2's review and its
	// triage, the security review's for level 3's adversarial pass. It is the
	// contract's map read at the level rather than at the stage, because the
	// resolver's question is which model the level runs.
	craft string
	// role is the slot the level's stage pins (StageRole): the coder's for
	// level 1's first-line review, the reviewer's for the other two.
	role Role
	cfg  *LevelSetting
	task *LevelSetting
	row  rigor.LevelState
}

// resolver is one ResolveLevels call's state.
type resolver struct {
	in         LevelInputs
	agent      Agent
	row        rigor.Levels
	loop       bool
	reviewOnly bool
	levels     Levels
	warns      []Warning
	errs       []error
}

func (r *resolver) run() {
	cfg := r.in.Config.Agent.Levels
	l1 := levelSpec{name: policy.LevelL1, craft: CraftReview, role: RoleCoder, cfg: cfg.L1, task: taskLevel(r.in.Task, policy.LevelL1), row: r.row.L1}
	l2 := levelSpec{name: policy.LevelL2, craft: CraftSeniorReview, role: RoleReviewer, cfg: cfg.L2, task: taskLevel(r.in.Task, policy.LevelL2), row: r.row.L2}
	l3 := levelSpec{name: policy.LevelL3, craft: CraftSecurityReview, role: RoleReviewer, cfg: cfg.L3, task: taskLevel(r.in.Task, policy.LevelL3), row: r.row.L3}
	// Only level 1 has a fallback. A mode's row that leaves level 2 or level 3
	// unset means the level does not run — rigor.defaultLevels places level 2
	// in every mode and level 3 at critical alone — and agent.first_line_review
	// is not their answer.
	first := r.firstLineDefault()
	r.one(l1, first)
	r.one(l2, false)
	r.one(l3, false)
	r.guard(l1, l2)
}

// one resolves a single level.
func (r *resolver) one(s levelSpec, fallback bool) {
	if r.reviewOnly && s.name != policy.LevelL2 {
		r.reviewOnlyLevel(s)
		r.set(s.name, ResolvedLevel{})
		return
	}
	lv := ResolvedLevel{
		Model:  firstSet(r.levelDef(s), s.cfg, s.task, levelModel),
		Effort: firstSet("", s.cfg, s.task, func(l *LevelSetting) string { return l.Effort }),
		Focus:  firstSet("", s.cfg, s.task, func(l *LevelSetting) string { return l.Focus }),
	}
	on, wroteNone := r.layerAnswer(s, fallback)
	floored := slices.Contains(r.in.Floor, s.name)
	switch {
	case floored && wroteNone:
		// G1: a level the owner's floor requires is refused when a layer
		// turns it off, never quietly kept on. A launcher writes the task,
		// so ignoring its `none` would be the launcher deciding how much
		// review it gets.
		r.errf("%s: none is refused: the owner's floor for mode %s requires this level (D722)", s.name, r.in.Mode)
	case floored:
		// The floor is a requirement, not a veto: it turns a level on that
		// the mode's own row leaves off (POC's level 2, say).
		on = true
	}
	if !on {
		// A level that does not run carries no settings: nothing may read a
		// model, an effort or a focus off it and act on the level anyway.
		r.set(s.name, ResolvedLevel{})
		return
	}
	lv.On = true
	lv.Inserted = r.inserts(s)
	r.check(s, lv)
	r.set(s.name, lv)
}

// reviewOnlyLevel is levels 1 and 3 on a mode: review recipe: review-only and
// pick allow exactly one review step and nothing else (recipe.Parse), so
// neither level has a step to run in. Neither the mode's own row nor
// first_line_review reaches them — a report run has no coder for a first-line
// pass to be, and no senior loop for an adversarial one to follow — but a
// layer that turns one on, and an owner's floor that requires one, cannot be
// honoured either, so those are refused rather than dropped (G1).
func (r *resolver) reviewOnlyLevel(s levelSpec) {
	if slices.Contains(r.in.Floor, s.name) {
		r.errf("%s is required by the owner's floor for mode %s, and recipe %s is mode: review, which allows exactly one review step and nothing else: level %s has no step to run in (D722)", s.name, r.in.Mode, r.in.Recipe.Name, s.name)
		return
	}
	for _, l := range []*LevelSetting{s.task, s.cfg} {
		if !writesLevel(l) {
			continue
		}
		if !levelOff(l) {
			r.errf("%s is on, and recipe %s is mode: review, which allows exactly one review step and nothing else: level %s has no step to run in", s.name, r.in.Recipe.Name, s.name)
		}
		return
	}
}

// layerAnswer is the level's on/off as the layers below the floor give it:
// the task's block, else the merged configuration's, else the mode's row, and
// otherwise fallback — skipping a block that writes no key, which says nothing
// rather than turning the level on (writesLevel). wroteNone reports whether the
// layer that decided it wrote the level's own switch (`none: true`, or its
// model named none — levelOff) rather than merely leaving the level off.
func (r *resolver) layerAnswer(s levelSpec, fallback bool) (on, wroteNone bool) {
	switch {
	case writesLevel(s.task):
		return !levelOff(s.task), levelOff(s.task)
	case writesLevel(s.cfg):
		// A level the mode's own row runs is a level a repository may turn
		// on and never off (spec §5.2), and where the project layer says
		// nothing about the level the merge kept the repository's switch
		// unjudged, for this to judge (R3, R20, RT2): the row is the
		// baseline, so the repository's off stands down and the level runs.
		if levelOff(s.cfg) && s.row == rigor.LevelOn && r.in.Config.ProjectSilent("agent.levels."+string(s.name)) {
			return true, false
		}
		return !levelOff(s.cfg), levelOff(s.cfg)
	}
	switch s.row {
	case rigor.LevelOn:
		return true, false
	case rigor.LevelOff:
		return false, false
	}
	return fallback, false
}

// writesLevel is whether a layer's block says anything about the level at all.
// An absent block and a block that carries no key (`review_levels: {l2: {}}`)
// both say nothing, so the level is left to the layer below — the rule the
// layer merge follows (writesAKey, resolve.go), where an empty block is what
// leaves an inherited none: true in place rather than clearing it, and the two
// rules have to agree: a task's empty block must not turn a level back on that
// the configuration turned off. `none: false` is the zero Off and reads as an
// unwritten key here, which is the same setting it is everywhere else
// (LevelSetting.Off): a block that writes one, and so is really turning the
// level back on, is the one a task cannot tell apart from an empty one.
func writesLevel(l *LevelSetting) bool {
	return l != nil && (levelOff(l) || l.Model != "" || l.Effort != "" || l.Focus != "")
}

// levelOff is whether a block says the level does not run: `none: true`, or
// the level's model named none, which is the same switch written the other way
// (NoneModel). A block read from a document arrives here folded — validate is
// what folds `model: none` into Off — and this is the same rule for a block
// built in Go, which no document's rules have been over.
func levelOff(l *LevelSetting) bool { return l != nil && (l.Off || l.Model == NoneModel) }

// levelModel is a block's model as the resolver reads it: the exact ID that
// runs the level, "" when the block names none. The literal none is the
// level's own switch rather than a model (NoneModel), so a block that writes
// it says nothing about which model the level runs — a layer beside one that
// turns the level back on with an effort must not run a model called none.
func levelModel(l *LevelSetting) string {
	if l.Model == NoneModel {
		return ""
	}
	return l.Model
}

// levelDef is the model a level runs when no layer of its own names one: its
// craft's own resolved model where the caller gave one — rung 3 of the model
// half's contract (D1660), the model MC12 writes into the level's slot and so
// the model the level's stages really run — and the role slot it pins below
// that (agent.models.<role>, else agent.model). The level's own
// agent.levels.lN.model is rung 2 and firstSet reads it above this.
func (r *resolver) levelDef(s levelSpec) string {
	if m := r.in.CraftModels[s.craft]; m != "" {
		return m
	}
	return r.agent.ModelFor(s.role)
}

// levelSource is the key the model a level will run comes from, in the terms a
// reader edits. A model the craft resolved — the level wrote none and rung 3
// answered (D1660, MC13) — comes from the craft's key, which is what the level
// has to be given to change it; every other model comes from the level's own
// key, which is the one a message about the level has always named and the one
// that overrules the role slot and agent.model below it (rungs 4 and 5).
func (r *resolver) levelSource(s levelSpec) string {
	if firstSet("", s.cfg, s.task, levelModel) == "" {
		if m := r.in.CraftModels[s.craft]; m != "" {
			return "agent.crafts." + s.craft + ".model"
		}
	}
	return string(s.name) + ".model"
}

// levelWords is levelSource and the model itself: how a warning names the model
// a level will run, so that a reader sees both the ID they are asked to change
// and the line it is written on.
func (r *resolver) levelWords(s levelSpec, model string) string {
	return r.levelSource(s) + " " + model
}

// coderModel is the model the coder will really run: its craft's where the
// caller resolved one (rung 3), else the coder slot, else agent.model below it
// (rungs 4 and 5). The coder warning of D721 is about the model that wrote the
// change, so it is this one and not the slot alone (D1664); the task's grade
// and a review level's own model do not reach it, because neither is the
// coder's.
func (r *resolver) coderModel() string {
	if m := r.in.CraftModels[CraftCoder]; m != "" {
		return m
	}
	return r.agent.ModelFor(RoleCoder)
}

// firstLineDefault is level 1's answer when no layer names it. Level 1 is not
// the rigor mode's to set: the mode leaves it to agent.first_line_review
// (config.Agent.FirstLineOn), except at POC, which turns level 2 off and so
// turns level 1 on to keep a review in the run (D723, rigor.DefaultLevels).
// The recipe decides twice over: the catalog's own `default` runs its
// first_line step only when first_line_review turns it on, and every other
// recipe with a first_line step always runs it (docs/recipes.md §2).
func (r *resolver) firstLineDefault() bool {
	if r.loop && hasStep(r.in.Recipe, recipe.StepFirstLine) && !r.in.CatalogDefault {
		return true
	}
	a := r.agent
	if a.FirstLineReview == FirstLineOff && r.in.Config.ProjectSilent("agent.first_line_review") {
		// applyDefaults' auto is this key's baseline where no mode row holds
		// it (R20, RT2): the repository's off is the looser answer, so the
		// key reads as the run would have read it unwritten.
		a.FirstLineReview = FirstLineAuto
	}
	return a.FirstLineOn(r.in.Providers)
}

// inserts is O7: a level that is on and whose recipe lacks its step is
// inserted in its fixed place. Level 1 goes before the review step and level
// 3 after it with bounce: first_line, and both take their knobs from agent.*
// (first_line_rounds, adversarial_rounds) exactly as a written step with no
// max_rounds does. A recipe that is not the loop has nowhere to put one, and
// its own shape decides what runs there.
func (r *resolver) inserts(s levelSpec) bool {
	if !r.loop {
		return false
	}
	switch s.name {
	case policy.LevelL1:
		return !hasStep(r.in.Recipe, recipe.StepFirstLine)
	case policy.LevelL3:
		return !hasStep(r.in.Recipe, recipe.StepAdversarial)
	}
	return false
}

// hasStep reports whether the recipe carries a step of kind.
func hasStep(r *recipe.Recipe, kind recipe.StepKind) bool {
	return r != nil && slices.ContainsFunc(r.Steps, func(s recipe.Step) bool { return s.Kind == kind })
}

// check applies the rules a level's own settings have to pass. It runs only
// for a level that is on: a level that does not run has no model to allow, no
// route to place and no effort to send.
func (r *resolver) check(s levelSpec, lv ResolvedLevel) {
	if lv.Effort != "" && !ValidEffort(lv.Effort) {
		r.errf("%s.effort %q must be one of %s: Claude Code refuses no value of its own (its flag warns and sends the default, and its variable and settings key are dropped in silence), so this is the only refusal there is", s.name, lv.Effort, strings.Join(Efforts, ", "))
	}
	if lv.Model == "" {
		// "" is Claude Code's own default rather than a model ID: there is
		// no allow-list entry to judge and no route to place it on.
		return
	}
	if r.in.AllowedModels != nil && !slices.Contains(r.in.AllowedModels, lv.Model) {
		// The model is judged whatever named it, so the refusal names the key
		// that did: a model the level wrote is refused at lN.model, and one its
		// craft resolved at the craft's key, which is the line to change
		// (D1664, MC13).
		key := r.levelSource(s)
		if len(r.in.AllowedModels) == 0 {
			r.errf("%s %s is refused: the run's allowed_models is empty, so no model is allowed", key, lv.Model)
		} else {
			r.errf("%s %s is not in the run's allowed_models (%s)", key, lv.Model, strings.Join(r.in.AllowedModels, ", "))
		}
	}
	if lv.Effort == "" {
		return
	}
	if name, _, ok := ProviderFor(r.in.Providers, lv.Model); ok {
		r.errf("%s.effort %q is refused: provider %s serves %s, and a provider stage runs with thinking off (MAX_THINKING_TOKENS=0), so the effort would look applied and do nothing (D725)", s.name, lv.Effort, name, lv.Model)
		return
	}
	if effortIgnored(lv.Model) {
		r.warnf(s.name, "%s.effort %q does nothing: %s sends no effort by any mechanism (docs/multi-model.md §7)", s.name, lv.Effort, lv.Model)
	}
}

// guard is the rule that spans two levels: the senior review may not run the
// model level 1 already ran (D721). A second pass of one model over its own
// work is what level 1 is, and paying it twice is the cost the levels exist
// to avoid, so the two levels never launch on the same exact model ID.
//
// It reads the models the two levels will really run, not the models a layer
// wrote on them (D1664, MC13): rung 3 of the model half's contract is a level's
// model wherever the level names none, so two crafts that resolve to one ID are
// refused exactly as two levels that name it, and the two warnings follow the
// same resolved value. Every message names the key the model came from, because
// the line a reader has to change is the one that wrote it — a craft's
// agent.crafts.<craft>.model or the level's own lN.model — and that is not
// always the level the model landed on.
//
// The comparison is by exact ID and only for a level that is on: "" is the
// agent's own default rather than an ID the resolver can compare, and a level
// that does not run reviews nothing. Two levels both left unset are an
// installation naming no models at all, which is an ordinary configuration
// rather than a mistake to refuse — the owner ruled on 2026-11-01 that it
// warns — but they are one model all the same, and it is the coder's.
func (r *resolver) guard(l1s, l2s levelSpec) {
	l1, l2 := r.levels.L1, r.levels.L2
	if !l2.On {
		return
	}
	if coder := r.coderModel(); l2.Model != "" && l2.Model == coder {
		r.warnf(policy.LevelL2, "%s is the coder's own model: a review by the model that wrote the change is the first-line review's job, not the senior one", r.levelWords(l2s, l2.Model))
	}
	if !l1.On {
		return
	}
	if l1.Model == "" && l2.Model == "" {
		r.warnf(policy.LevelL2, "l2.model and l1.model are both unset: both levels run the same model, Claude Code's own default, so the senior review is not independent of the first-line one — and neither is independent of the coder, unless agent.models names the coder another model; name l2 a model of its own to make it independent (D721)")
		return
	}
	if l1.Model == "" || l2.Model == "" {
		return
	}
	if l1.Model == l2.Model {
		r.errf("%s and %s are both %s: review level 2 may not run the same model as level 1 (D721); name another model for l2, or turn one of the two levels off", r.levelSource(l2s), r.levelSource(l1s), l1.Model)
		return
	}
	if fam := ModelFamily(l1.Model); fam != "" && fam == ModelFamily(l2.Model) {
		r.warnf(policy.LevelL2, "%s and %s are both %s: a family reviews its own work best when level 2 is a different one", r.levelWords(l2s, l2.Model), r.levelWords(l1s, l1.Model), fam)
	}
}

// ModelFamily is the vendor family a model ID belongs to: a Claude ID is
// "anthropic", a provider ID its vendor prefix ("deepseek" for
// deepseek/deepseek-v4-flash), and "" an ID of neither shape. Two levels on
// different IDs of one family share its blind spots, which is what the
// warning above is about.
func ModelFamily(model string) string {
	if vendor, _, ok := strings.Cut(model, "/"); ok && vendor != "" {
		return vendor
	}
	if strings.HasPrefix(model, "claude-") {
		return "anthropic"
	}
	return ""
}

// effortIgnoringModels are the Claude models the pinned Claude Code sends no
// effort for, by any mechanism or level, also with
// CLAUDE_CODE_ALWAYS_ENABLE_EFFORT (docs/multi-model.md §7). An effort set
// for one of them is not a refusal — it is a setting that does nothing — so
// the resolver warns and the run goes on.
var effortIgnoringModels = []string{"claude-haiku-4-5"}

func effortIgnored(model string) bool {
	return slices.ContainsFunc(effortIgnoringModels, func(m string) bool { return strings.HasPrefix(model, m) })
}

// firstSet is one setting's value: the task's, else the merged
// configuration's, else the level's own default. "" is "this layer says
// nothing about it", the same rule the layer merge follows.
func firstSet(def string, cfg, task *LevelSetting, f func(*LevelSetting) string) string {
	if task != nil {
		if v := f(task); v != "" {
			return v
		}
	}
	if cfg != nil {
		if v := f(cfg); v != "" {
			return v
		}
	}
	return def
}

// taskLevel is t's block for level p, nil when t or the block is absent.
func taskLevel(t *ReviewLevels, p policy.Level) *LevelSetting {
	if t == nil {
		return nil
	}
	switch p {
	case policy.LevelL1:
		return t.L1
	case policy.LevelL2:
		return t.L2
	case policy.LevelL3:
		return t.L3
	}
	return nil
}

func (r *resolver) set(p policy.Level, lv ResolvedLevel) {
	switch p {
	case policy.LevelL1:
		r.levels.L1 = lv
	case policy.LevelL2:
		r.levels.L2 = lv
	case policy.LevelL3:
		r.levels.L3 = lv
	}
}

func (r *resolver) errf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

func (r *resolver) warnf(l policy.Level, format string, args ...any) {
	r.warns = append(r.warns, Warning{Level: l, Message: fmt.Sprintf(format, args...)})
}
