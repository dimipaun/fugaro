// Package settings holds the one registry of Fugaro's settings: every key
// `fugaro settings` can print, where it may be set, what decides it when no
// layer does, and which command family writes it (docs/settings-commands
// spec §3, D840).
//
// The registry exists so that the DEFAULT column, `--changed` and the JSON
// Schema's `default` keywords have one source rather than a fourth hand-kept
// copy (F12). It is read-only knowledge: nothing here reads a file, a cache or
// a cloud object, and no writer consults it to decide what to write.
//
// The arrow runs one way. `internal/settings` imports `internal/config` for
// `config.Scopes`, so `applyDefaults` — and the local loader — can never
// import this package back: the constants holding the defaults live in
// `internal/config`, both loaders read them, and the registry reads the same
// constants for its rows. TestRegistryDefaultsMatchApplyDefaults pins a row to
// what `applyDefaults` actually produces, which is what keeps the column and a
// real run's value from parting company with nothing red.
package settings

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// Merge is how a key's layers combine into the effective value a run gets.
type Merge string

const (
	// Replace is the ordinary rule: the narrowest layer that sets the key
	// decides.
	Replace Merge = "replace"
	// Tighten is the ceiling rule (policy.Merge): the smaller cap, the
	// stricter mode and the intersection of the lists win, whatever the
	// layer order.
	Tighten Merge = "tighten"
	// Union is the additive rule: a lower layer only adds, as review_floor
	// and rigor.areas do.
	Union Merge = "union"
)

// The layers a key may be set in, as `--layer` spells them (spec §2.6).
// `default` and `env` are always readable; `policy` and `caps` are the two
// cloud layers; `job` and `branch` are levels `WINS` may name but no `--layer`
// selects, so no row lists them.
const (
	LayerDefault = "default"
	LayerProject = "project"
	LayerProfile = "profile"
	LayerRepo    = "repo"
	LayerFlag    = "flag"
	LayerLocal   = "local"
	LayerShared  = "shared"
	LayerPolicy  = "policy"
	LayerCaps    = "caps"
	LayerEnv     = "env"
)

// Layers is the closed vocabulary `--layer` accepts, in the order the
// synopsis prints it.
var Layers = []string{
	LayerDefault, LayerProject, LayerProfile, LayerRepo, LayerFlag,
	LayerLocal, LayerShared, LayerPolicy, LayerCaps, LayerEnv,
}

// Default is a row's built-in default: the value the loaders leave in place,
// with the Go constant that holds it, or the sentence saying what unset means.
// Exactly one of Value and Unset is set — a value where a value is the answer,
// a sentence where the answer is a decision rather than a value (spec §2.4).
type Default struct {
	Value string // the effective default, "" when Unset says what unset means
	Const string // the qualified Go constant holding Value, "" for a literal
	Unset string // the "unset means" sentence, "" when Value is the answer
}

// Row is one setting: a key, the layers that may set it, how they combine,
// its default, and the two facts a writer needs.
type Row struct {
	Key       string
	Layers    []string
	Merge     Merge
	Default   Default
	Sensitive bool   // the value is never printed (spec §2.3)
	Owner     string // the token for what writes it: a command, or the file a key no command edits is stated in
	Doc       string // the document that must state it
	// TypedWhy is the sentence saying why turning this key ON is the widening
	// direction rather than the ordinary write: the write prints it and the dry
	// run carries it as `why`. A widening is the owner's own step and is asked
	// nothing for itself (D1552); what fences it is the coding agent's session,
	// at every door that writes it. The widening directions do not include every
	// key, so the row carries its own. Empty is a key whose write is the
	// ordinary one.
	TypedWhy string
}

// rows is the table. It is assembled once, at package initialization, because
// the `fugaro.yaml` rows are derived from config.Scopes rather than restated:
// a key added there and not here is TestRegistryFugaroYAMLKeysMatchConfigScopes'
// failure, and the two can never drift.
var rows = buildRows()

// Rows returns every row, in key order. The slice is a copy: a caller cannot
// reorder or edit the registry.
func Rows() []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	return out
}

// Lookup is the row of key. false: no such key.
func Lookup(key string) (Row, bool) {
	for _, r := range rows {
		if r.Key == key {
			return r, true
		}
	}
	return Row{}, false
}

// DefaultOf is what the DEFAULT column prints for key and the constant that
// holds it: the value and its source, or the "unset means" sentence and no
// source. An unknown key answers ("", "").
func DefaultOf(key string) (string, string) {
	r, ok := Lookup(key)
	if !ok {
		return "", ""
	}
	if r.Default.Unset != "" {
		return r.Default.Unset, ""
	}
	return r.Default.Value, r.Default.Const
}

// unset is a row whose default is a decision rather than a value.
func unset(what string) Default { return Default{Unset: "unset = " + what} }

// value is a row whose default is the value the loaders leave in place.
func value(v, constant string) Default { return Default{Value: v, Const: constant} }

// yamlMeta is the metadata of every `fugaro.yaml` key: what config.Scopes
// cannot answer. The key set itself, and the layers, come from Scopes — this
// table adds the default, the merge rule and the two facts a writer needs, and
// TestRegistryFugaroYAMLKeysMatchConfigScopes fails on a key that is in one and
// not the other.
var yamlMeta = map[string]Row{
	// The four keys a checkout's `fugaro.yaml` is opened with. The command
	// that writes them is `fugaro config init`, which writes exactly these
	// lines and nothing else (minimalFugaroYAML, docs/project-layer.md §3);
	// the generic path refuses all four, because its allowlist is the keys a
	// repository is *edited* into and the file's header is written once, when
	// the checkout has none. The default owner would say `settings set`
	// writes four keys its own allowlist refuses — F13's shape, where D1178's
	// rule is that a non-writable key names a command that exists and writes
	// it.
	"version":     {Owner: "config init", Default: unset("the file is refused (version: 1 is required)")},
	"project":     {Owner: "config init", Default: unset("the file is refused (project: is required)")},
	"gcp_project": {Owner: "config init", Default: unset("the installation's GCP project")},
	"profile":     {Owner: "config init", Default: unset("the project layer's default_profile")},

	"git.provider":    {Default: unset("the local config's repos.<owner/name>.provider")},
	"git.base_branch": {Default: value(config.DefaultBaseBranch, "config.DefaultBaseBranch")},
	"git.author":      {Default: unset("none, which is Fugaro as the commit author")},
	"git.pr.labels":   {Default: unset("none")},
	"git.pr.reviewers": {
		Default: unset("none"),
		Doc:     "docs/git-providers.md",
	},
	"git.pr.early_draft": {Default: value("true", "")},
	"git.pr.checkpoints": {Default: unset("inherit")},
	"agent.auth":         {Default: value(config.DefaultAgentAuth, "config.DefaultAgentAuth"), Doc: "docs/credentials-service.md"},
	"agent.model":        {Owner: "models", Default: unset("Fugaro's own model choice"), Doc: "docs/multi-model.md"},
	"agent.models.coder": {Owner: "models", Default: unset("agent.model"), Doc: "docs/multi-model.md"},
	"agent.models.reviewer": {
		Owner:   "models",
		Default: unset("agent.model"),
		Doc:     "docs/multi-model.md",
	},
	"agent.models.background": {
		Owner:   "models",
		Default: unset("agent.model"),
		Doc:     "docs/multi-model.md",
	},
	// The vision slot (V1, D2003) is the one model key with no fallback: a
	// stage whose own model cannot read an image keeps it rather than running
	// agent.model, which is exactly the text-only model that must not answer
	// one. The models family owns it as it owns the three rows above — the
	// slot's checks are that command's (D2013: priced, a route opens it to this
	// repository, no whitespace), which a generic scalar write does not make —
	// and V6 of docs/vision-model gave the family the fourth role name, so this
	// row's refusal names the command that writes the key. A hand edit is the
	// other way in — in fugaro.yaml, or in the project layer's own file for
	// every repository of it.
	"agent.models.vision": {
		Owner:   "models",
		Default: unset("none: a stage whose own model cannot read an image keeps it"),
		Doc:     docVision,
	},
	// The diagnose slot (D1607) is the model `fugaro troubleshoot` runs on,
	// and it is a decision rather than a value like the vision slot's: nothing
	// applies Fugaro's own choice to the resolved configuration, so the row
	// says what unset means instead of naming the constant. The models family
	// owns it as it owns the four rows above — what a model key may hold is
	// that command's check — and the key is written by hand until `models use`
	// takes the role.
	"agent.models.diagnose": {
		Owner:   "models",
		Default: unset("Fugaro's own diagnose model (config.DefaultDiagnoseModel)"),
		Doc:     docDiagnose,
	},
	// The four crafts (docs/model-crafts/spec.md §3), the spec's own table
	// read back: the coder and the senior review are what a run cannot do
	// without, and the review and the security review follow the craft above
	// them when they set nothing of their own.
	"agent.crafts.coder.model":            {Owner: "models", Default: unset("required: the coder implements, so a run needs its model"), Doc: docCrafts},
	"agent.crafts.coder.access":           {Owner: "accounts", Default: unset("required: the coder's calls go out on a credential or a pool"), Doc: docCrafts},
	"agent.crafts.review.model":           {Owner: "models", Default: unset("the coder's model"), Doc: docCrafts},
	"agent.crafts.review.access":          {Owner: "accounts", Default: unset("the coder's access"), Doc: docCrafts},
	"agent.crafts.senior-review.model":    {Owner: "models", Default: unset("required: the senior review is the second opinion a run needs"), Doc: docCrafts},
	"agent.crafts.senior-review.access":   {Owner: "accounts", Default: unset("required: the senior review's calls go out on a credential or a pool"), Doc: docCrafts},
	"agent.crafts.security-review.model":  {Owner: "models", Default: unset("the senior review's model"), Doc: docCrafts},
	"agent.crafts.security-review.access": {Owner: "accounts", Default: unset("the senior review's access"), Doc: docCrafts},

	"agent.review_rounds": {Default: unset("the run's rigor mode decides (rigor.DefaultRounds)")},
	"agent.max_budget_usd": {
		Default: value("25", "config.DefaultMaxBudgetUSD"),
	},
	"agent.instructions":               {Default: unset("none")},
	"agent.review":                     {Default: unset("none")},
	"agent.max_output_tokens.coder":    {Merge: Tighten, Default: unset("the ceiling's")},
	"agent.max_output_tokens.reviewer": {Merge: Tighten, Default: unset("the ceiling's")},
	"agent.max_run_tokens":             {Merge: Tighten, Default: unset("the ceiling's")},
	"agent.first_line_review":          {Default: value(config.FirstLineAuto, "config.FirstLineAuto")},
	"agent.first_line_rounds":          {Default: unset("the run's rigor mode decides (rigor.DefaultRounds)")},
	// The per-level review block (L1.3, D722). The model a level resolves to
	// when the block names none is the resolver's rule (L3.1): level 2 and
	// level 3 are the reviewer's model, level 1 the coder's.
	"agent.levels.l1.model":  {Owner: "models", Default: unset("the coder's model"), Doc: docModels},
	"agent.levels.l2.model":  {Owner: "models", Default: unset("agent.models.reviewer"), Doc: docModels},
	"agent.levels.l3.model":  {Owner: "models", Default: unset("agent.models.reviewer"), Doc: docModels},
	"agent.levels.l1.effort": {Owner: "models", Default: unset("the model's own default"), Doc: docModels},
	"agent.levels.l2.effort": {Owner: "models", Default: unset("the model's own default"), Doc: docModels},
	"agent.levels.l3.effort": {Owner: "models", Default: unset("the model's own default"), Doc: docModels},
	"agent.levels.l1.none":   {Default: unset("the run's rigor mode and the recipe decide")},
	"agent.levels.l2.none":   {Default: unset("the run's rigor mode and the recipe decide")},
	"agent.levels.l3.none":   {Default: unset("the run's rigor mode and the recipe decide")},
	"agent.levels.l1.focus":  {Default: unset("none")},
	"agent.levels.l2.focus":  {Default: unset("none")},
	"agent.levels.l3.focus":  {Default: unset("none")},
	"agent.adversarial_rounds": {
		Default: unset("the run's rigor mode decides (rigor.AdversarialDefault)"),
	},
	"agent.review_stop_fraction": {
		Default: unset("0.8 (config.DefaultReviewStopFraction)"),
	},
	// The triage stage's switch (D1854): an absent key is the stage running,
	// in the two modes that have such a stage at all, so the DEFAULT column
	// names those modes rather than reading "none" — which a reader would take
	// for the stage being off.
	"agent.triage": {
		Default: unset("on (startup and pro only, config.TriageMode)"),
	},

	"agent.recipe":      {Owner: "models", Default: unset("the built-in recipe"), Doc: "docs/recipes.md"},
	"agent.oauth_token": {Default: unset("the job's own credential")},
	"agent.compression": {Default: unset("inherit")},

	"budget.mode":           {Merge: Tighten, Default: unset("observe")},
	"budget.per_run_usd":    {Merge: Tighten, Default: unset("the deployed ceiling")},
	"budget.allowed_models": {Merge: Tighten, Default: unset("every model the installation allows")},
	"budget.per_day_usd":    {Merge: Tighten, Default: unset("no daily cap")},

	"governance.review_ratio":       {Merge: Tighten, Default: unset("every review")},
	"governance.review_floor_calls": {Merge: Tighten, Default: unset("none")},
	"governance.max_subagents":      {Merge: Tighten, Default: unset("Fugaro's own cap")},
	"governance.stage_budget":       {Merge: Tighten, Default: unset("Fugaro's own stage budgets")},
	"governance.stage_tokens":       {Merge: Tighten, Default: unset("Fugaro's own stage token caps")},
	"governance.stage_timeout":      {Merge: Tighten, Default: unset("Fugaro's own stage timeouts")},
	"governance.tiers":              {Merge: Tighten, Default: unset("Fugaro's own tiers")},

	"workflows.*.profile":                  {Default: unset("the project layer's default_profile")},
	"workflows.*.job_profile":              {Default: unset("the project policy's default job profile"), Doc: "docs/project-layer.md"},
	"workflows.*.base":                     {Default: unset("none; the key was removed in 0.7.0"), Doc: config.MigrationDoc},
	"workflows.*.checkout":                 {Default: value(config.CheckoutBaked, "config.CheckoutBaked")},
	"workflows.*.image.node":               {Default: unset("none; the key was removed in 0.7.0"), Doc: config.MigrationDoc},
	"workflows.*.image.jdk":                {Default: unset("none; the key was removed in 0.7.0"), Doc: config.MigrationDoc},
	"workflows.*.image.apt":                {Default: unset("none")},
	"workflows.*.image.setup":              {Default: unset("none")},
	"workflows.*.image.skip_build_scripts": {Default: unset("false: the dependency install and build scripts run")},
	"workflows.*.image.tools":              {Default: unset("none")},
	"workflows.*.image.key_files":          {Default: unset("none")},
	"workflows.*.dockerfile":               {Default: unset("none")},
	"workflows.*.commands.build":           {Default: unset("none")},
	"workflows.*.commands.test":            {Default: unset("none")},
	"workflows.*.commands.lint":            {Default: unset("none")},
	"workflows.*.commands.fix":             {Default: unset("none")},
	"workflows.*.commands.render":          {Default: unset("none")},
	"workflows.*.commands.probe":           {Default: unset("none")},
	"workflows.*.commands.rerun_failed":    {Default: unset("none")},
	"workflows.*.commands.reports": {
		Default: value(strings.Join(config.DefaultReports(), ", "), "config.DefaultReports"),
	},
	"workflows.*.cache":                     {Default: unset("none")},
	"workflows.*.secrets":                   {Default: unset("none")},
	"workflows.*.resources.cpu":             {Default: value("4", "config.DefaultCPU")},
	"workflows.*.resources.memory":          {Default: value("8Gi", "config.DefaultMemory")},
	"workflows.*.timeouts.total":            {Default: unset("3h")},
	"workflows.*.timeouts.stage":            {Default: unset("60m, or the total when it is shorter")},
	"workflows.*.timeouts.verify":           {Default: unset("30m, or the total when it is shorter")},
	"workflows.*.timeouts.render":           {Default: unset("what verify gets")},
	"workflows.*.timeouts.finalize_reserve": {Default: unset("5m, or a fifth of the total when it is shorter")},
	"workflows.*.rebuild.check":             {Default: unset("the rebuild rule's own default")},
	"workflows.*.rebuild.max_age":           {Default: unset("the rebuild rule's own default")},
	"workflows.*.rebuild.lockfiles":         {Default: unset("the rebuild rule's own default")},
	"workflows.*.rebuild.base":              {Default: unset("the rebuild rule's own default")},
	"workflows.*.rebuild.paths":             {Default: unset("the rebuild rule's own default")},
	"workflows.*.inputs.max_files":          {Default: unset("the built-in cap")},
	"workflows.*.inputs.max_bytes":          {Default: unset("the built-in cap")},
	"workflows.*.inputs.max_total_bytes":    {Default: unset("the built-in cap")},

	"followup.trusted":      {Default: unset("nobody")},
	"followup.allow_public": {Default: unset("false")},

	"rigor.default": {
		Owner: "rigor",
		// A value here would be the fourth copy of the mode's floor: the
		// sentence names the constant the resolver reads instead (F12).
		Default: Default{Unset: "unset = " + string(rigor.Default) + " (rigor.Default)"},
		Doc:     "docs/rigor-modes/spec.md",
	},
	"rigor.areas": {Owner: "rigor", Merge: Union, Default: unset("no area rules"), Doc: "docs/rigor-modes/spec.md"},

	// The security list is rigor.areas' twin in the two fields a Scopes row
	// cannot answer. Its merge is Union and not the default Replace (D1506):
	// the repository's rows are added to the project's and the engine's, never
	// put in their place, so a layer that writes the key can only ever say
	// more. And its owner is not `settings set`, though that is the row a key
	// with no entry here takes: D1515 decides the editor is fugaro.yaml and the
	// project layer file — a list has no scalar floor for the allow-list's rule
	// to raise — and `fugaro config publish` is the command that carries the
	// layer's half, which is what the row names.
	"security.patterns": {Owner: "config", Merge: Union, Default: unset("the engine's floor alone")},

	// The planning pass's switch (docs/contract-first-decoupling, D1813). It
	// is a preference rather than a bound, so the ordinary rule decides it:
	// the narrowest layer that sets it wins. Owner `config` for
	// security.patterns' reason (D1515): the key is stated in a file — a
	// repository's `fugaro.yaml`, and the layer's `defaults:` — and the
	// layer's half is what `fugaro config publish` carries. Its second half is
	// `layer.defaults.planning.decoupling`, which the generic path does write,
	// so a project sets the key through that row; the repository's own half is
	// the file's, and the allowlist does not carry the bare spelling.
	"planning.decoupling": {Owner: "config", Default: value("true", "config.DefaultPlanningDecoupling")},
}

// buildRows is the table, assembled from the three documents' own sources: the
// `fugaro.yaml` rows from config.Scopes (so the two cannot drift), the project
// layer's from config.ProjectLayer, the local config's from localcfg.Config.
func buildRows() []Row {
	out := make([]Row, 0, len(config.Scopes)+len(layerRows)+len(localRows)+len(cloudRows)+len(config.RunStages))
	for _, s := range config.Scopes {
		out = append(out, yamlRow(s))
	}
	out = append(out, layerRows...)
	out = append(out, localRows...)
	out = append(out, cloudRows...)
	out = append(out, stageAccountRows()...)
	for i := range out {
		out[i].Sensitive = sensitiveKeys[out[i].Key]
		if v, ok := defaultValues[out[i].Key]; ok {
			out[i].Default = v
		}
	}
	// Each document's rows are written in its own order, so the three groups
	// are sorted here rather than in a reader: the table, `--changed` and the
	// schema's key list are one stable order, and a row added to any group
	// lands where a reader looking for it by name expects it.
	slices.SortFunc(out, func(a, b Row) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// yamlRow is the registry's row for a fugaro.yaml key: its key and layers come
// from config.Scopes, everything else from yamlMeta.
//
// A key yamlMeta does not hold takes the ordinary rule for every field: a
// missed lookup answers the zero Row, and the assignments below are what turn
// that into one — the merge rule Replace, the default `unset = none`, the owner
// `settings set` and docs/project-layer.md. They cover every field a yamlMeta
// entry can leave empty, Key and Layers being written here either way and
// Sensitive by buildRows, so a Row field added without a fallback below is a
// field a yamlMeta-less key would carry as its zero value. That is the contract
// this comment records, and why there is no branch for the miss itself.
func yamlRow(s config.ScopeRow) Row {
	r := yamlMeta[s.Key]
	r.Key = s.Key
	r.Layers = scopeLayers(s.In)
	if sharedWithLocal[s.Key] {
		r.Layers = append(r.Layers, LayerLocal, LayerShared)
	}
	if r.Merge == "" {
		r.Merge = Replace
	}
	if r.Default == (Default{}) {
		r.Default = unset("none")
	}
	if r.Owner == "" {
		r.Owner = "settings set"
	}
	if r.Doc == "" {
		r.Doc = "docs/project-layer.md"
	}
	return r
}

// scopeLayers is a ScopeRow's layers as `--layer` spells them, in the
// vocabulary's own order.
func scopeLayers(in config.Scope) []string {
	var out []string
	if in&config.InProject != 0 {
		out = append(out, LayerProject)
	}
	if in&config.InProfile != 0 {
		out = append(out, LayerProfile)
	}
	if in&config.InRepo != 0 {
		out = append(out, LayerRepo)
	}
	if in&config.InOverride != 0 {
		out = append(out, LayerFlag)
	}
	return out
}

// The documents a row's Doc names. A key whose document is not one of these
// names it in full.
const (
	docSetup  = "docs/gcp-setup.md"
	docLayer  = "docs/project-layer.md"
	docModels = "docs/multi-model.md"
	docRigor  = "docs/rigor-modes/spec.md"
	docCrafts = "docs/model-crafts/spec.md"
	docImages = "docs/project-image-catalog/spec.md"
	// docDiagnose is the troubleshooting guide's document: it is written for
	// the model `fugaro troubleshoot` starts, so it is where the key that
	// chooses that model is stated (D1607).
	docDiagnose = "docs/troubleshooting-for-agents.md"
	// docOrchestrator is the document that states the orchestrator daemon's
	// guardrails. It is the work's plan rather than a spec because the guardrail
	// keys are stated there and nowhere else yet (D1239, D1234): a spec of the
	// daemon's own would be the document that takes the row over.
	docOrchestrator = "docs/stateless-orchestrator/implementation-plan.md"
	// The workspace keys' document: the design that decides the membership
	// key, the pin and the owner's member table (docs/workspaces/spec.md §2,
	// §5 and §6). The folder's own README, reconciliation and plan are not the
	// document a key's meaning is stated in, so the row points at the spec.
	docWorkspace = "docs/workspaces/spec.md"
	// docPeriodicReviews is the document that states the project's jobs:
	// block (R13). It is the work's plan rather than a spec, for
	// docOrchestrator's reason: the two keys and the default that turns a
	// schedule on are stated there and nowhere else yet, and a spec of the
	// installation's own would be the document that takes the rows over.
	docPeriodicReviews = "docs/periodic-reviews/implementation-plan.md"
	// docTroubleshoot is the document that states the issue nudge's
	// thresholds (docs/troubleshoot-issue/). The spec states the behaviour
	// (§6: the wording gets firmer as an issue ages or recurs more often, at
	// thresholds config may set) and leaves the keys unnamed, so the row
	// points at the plan for docOrchestrator's reason: the two keys are named
	// there and nowhere else yet. The plan states no figure — it says the
	// defaults are documented — so the values themselves are this table's
	// DEFAULT column and the schema's keyDefaults, and a spec of the section's
	// own would be the document that takes the rows over.
	docTroubleshoot = "docs/troubleshoot-issue/implementation-plan.md"
	// docVision is the document that states the vision slot (docs/vision-model)
	// and the command that will write it. It is the work's plan rather than
	// docs/multi-model.md, the three roles' own document, for docOrchestrator's
	// reason: the slot is named there and nowhere else yet, and V8's documents
	// take the row over.
	docVision = "docs/vision-model/implementation-plan.md"
	// docTriage is the document that states the triage stage and the switch
	// that turns it off (D1854, D1851). It is the work's plan for
	// docOrchestrator's reason: the stage, its default and the switch's home on
	// the published node are stated there and nowhere else yet, and a document
	// of the stage's own would take the row over.
	docTriage    = "docs/post-review-triage/implementation-plan.md"
	docCloneURLs = "docs/project-claude-accounts.md"
)

// layerRow is a row of the project layer's file. The layer is its only store,
// so `--layer project` is the whole of its Layers.
func layerRow(key, def, owner, doc string) Row {
	return Row{
		Key:     "layer." + key,
		Layers:  []string{LayerProject},
		Merge:   Replace,
		Default: unset(def),
		Owner:   owner,
		Doc:     doc,
	}
}

// layerValueRow is layerRow for a layer key whose default is a value the layer
// reader leaves in place rather than an "unset means" sentence: the two jobs:
// keys default to paused, and true is what a layer that states nothing has to
// be read as. TestRegistryDefaultsMatchApplyDefaults then pins the value
// against the reader itself, which is the point of writing it as one.
func layerValueRow(key, v, owner, doc string) Row {
	r := layerRow(key, "", owner, doc)
	r.Default = value(v, "")
	return r
}

// loc is a row of the local config. The local file is its only store, and
// `shared` stands in for a key the shared config also carries (spec §2.2).
func loc(key, def, owner, doc string) Row {
	return Row{
		Key:     key,
		Layers:  []string{LayerLocal, LayerShared},
		Merge:   Replace,
		Default: unset(def),
		Owner:   owner,
		Doc:     doc,
	}
}

// locTighten is loc for a key whose layers combine by the ceiling rule rather
// than by replace: the smaller figure wins whatever layer wrote it, so a key
// written in two of them is the stricter of the two and not the narrower one
// (policy.Merge). The orchestrator's guardrails are the keys of this kind
// (D1239): every layer under the ceiling may lower a cap and none may raise it.
func locTighten(key, def, owner, doc string) Row {
	r := loc(key, def, owner, doc)
	r.Merge = Tighten
	return r
}

// locLocal is loc for a key the local config alone holds: no `shared`
// stand-in, so `--layer local` is the whole of its Layers. It is the row of a
// key only the owner may write — one whose presence where a launcher can
// reach it would be a way around a rule this document states.
func locLocal(key, def, owner, doc string) Row {
	return Row{
		Key:     key,
		Layers:  []string{LayerLocal},
		Merge:   Replace,
		Default: unset(def),
		Owner:   owner,
		Doc:     doc,
	}
}

// layerRows are the project layer's keys. They are written out rather than
// derived because a struct field is not a setting until someone decides what
// its absence means and who may write it; TestRegistryCoversEveryConfigField
// walks config.ProjectLayer and fails on a field with no row here.
//
// The layer's own block names are prefixed `layer.` (SD14): the project
// layer's `profiles.<n>` is a workflow profile and the local config's is a job
// profile, and one key may not mean two things.
var layerRows = []Row{
	// The layer file's own header. Owner `config` for security.patterns'
	// reason (D1515): the file is the editor and `fugaro config publish`
	// carries it, while the generic path refuses all three — its allowlist
	// carries the keys a person edits a published layer through, and a header
	// is what a file is opened with rather than a key an edit lands on.
	// `default_profile` beside them is the generic path's, which is the
	// difference the owner column is for.
	layerRow("version", "the file is refused (version: is required)", "config", docLayer),
	layerRow("project", "the file is refused (project: is required)", "config", docLayer),
	layerRow("gcp_project", "the installation's GCP project", "config", docLayer),
	layerRow("default_profile", "the layer's own profile block is refused without one", "settings set", docLayer),
	layerRow("claude_models", "every Claude model the project's accounts may serve", "models", docModels),

	layerRow("defaults.git.provider", "none", "settings set", docLayer),
	layerRow("defaults.git.pr.labels", "none", "settings set", docLayer),
	layerRow("defaults.git.pr.early_draft", "true", "settings set", docLayer),
	layerRow("defaults.git.pr.checkpoints", "inherit", "settings set", docLayer),
	layerRow("defaults.agent.auth", "vertex", "settings set", docLayer),
	layerRow("defaults.agent.model", "Fugaro's own model choice", "models", docModels),
	layerRow("defaults.agent.models.coder", "defaults.agent.model", "models", docModels),
	layerRow("defaults.agent.models.reviewer", "defaults.agent.model", "models", docModels),
	layerRow("defaults.agent.models.background", "defaults.agent.model", "models", docModels),
	// The vision slot's layer row (V1, D2003): the layer sets it for every
	// repository of the project, and a layer that leaves it out leaves each
	// repository's own row to decide — including the empty answer, which is
	// the one slot whose empty answer is not the layer's model.
	layerRow("defaults.agent.models.vision", "none: a stage whose own model cannot read an image keeps it", "models", docVision),
	// The diagnose slot's layer row (D1607): the layer sets the local
	// diagnosis's model for every repository of the project, and a layer that
	// states nothing leaves Fugaro's own choice to the command.
	layerRow("defaults.agent.models.diagnose", "Fugaro's own diagnose model (config.DefaultDiagnoseModel)", "models", docDiagnose),
	// The crafts' models are the layer's to set, as the three models rows
	// above are; their access is not, and the row says so rather than leaving
	// the layer's reader to find out at publish time (config.Scopes refuses
	// defaults.agent.crafts.<craft>.access for the reason agent.oauth_token is
	// refused there: the layer is a launcher-writable bucket object).
	layerRow("defaults.agent.crafts.coder.model", "defaults.agent.model", "models", docCrafts),
	layerRow("defaults.agent.crafts.coder.access", "none: only the local config and the policy node name a credential", "accounts", docCrafts),
	layerRow("defaults.agent.crafts.review.model", "the coder's model", "models", docCrafts),
	layerRow("defaults.agent.crafts.review.access", "none: only the local config and the policy node name a credential", "accounts", docCrafts),
	layerRow("defaults.agent.crafts.senior-review.model", "defaults.agent.model", "models", docCrafts),
	layerRow("defaults.agent.crafts.senior-review.access", "none: only the local config and the policy node name a credential", "accounts", docCrafts),
	layerRow("defaults.agent.crafts.security-review.model", "the senior review's model", "models", docCrafts),
	layerRow("defaults.agent.crafts.security-review.access", "none: only the local config and the policy node name a credential", "accounts", docCrafts),
	layerRow("defaults.agent.review_rounds", "the run's rigor mode decides (rigor.DefaultRounds)", "settings set", docLayer),
	layerRow("defaults.agent.max_budget_usd", "25", "settings set", docLayer),
	layerRow("defaults.agent.first_line_review", "auto", "settings set", docLayer),
	layerRow("defaults.agent.first_line_rounds", "the run's rigor mode decides (rigor.DefaultRounds)", "settings set", docLayer),
	layerRow("defaults.agent.levels.l1.model", "the coder's model", "models", docModels),
	layerRow("defaults.agent.levels.l1.effort", "the model's own default", "models", docModels),
	layerRow("defaults.agent.levels.l1.none", "the run's rigor mode and the recipe decide", "settings set", docLayer),
	layerRow("defaults.agent.levels.l1.focus", "none", "settings set", docLayer),
	layerRow("defaults.agent.levels.l2.model", "defaults.agent.models.reviewer", "models", docModels),
	layerRow("defaults.agent.levels.l2.effort", "the model's own default", "models", docModels),
	layerRow("defaults.agent.levels.l2.none", "the run's rigor mode and the recipe decide", "settings set", docLayer),
	layerRow("defaults.agent.levels.l2.focus", "none", "settings set", docLayer),
	layerRow("defaults.agent.levels.l3.model", "defaults.agent.models.reviewer", "models", docModels),
	layerRow("defaults.agent.levels.l3.effort", "the model's own default", "models", docModels),
	layerRow("defaults.agent.levels.l3.none", "the run's rigor mode and the recipe decide", "settings set", docLayer),
	layerRow("defaults.agent.levels.l3.focus", "none", "settings set", docLayer),
	layerRow("defaults.agent.adversarial_rounds", "the run's rigor mode decides (rigor.AdversarialDefault)", "settings set", docLayer),
	layerRow("defaults.agent.review_stop_fraction", "0.8 (config.DefaultReviewStopFraction)", "settings set", docLayer),
	layerRow("defaults.agent.recipe", "the built-in recipe", "models", docModels),
	layerRow("defaults.rigor.default", "the built-in mode (pro)", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.name", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.match", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.mode", "none", "rigor", docRigor),
	// The planning pass's switch, read the way defaults.git.pr.early_draft
	// above is: a layer that states nothing leaves it as the fugaro.yaml row
	// says (docs/contract-first-decoupling, D1813).
	layerRow("defaults.planning.decoupling", "true", "settings set", docLayer),

	layerRow("profiles.*.description", "none", "settings set", docLayer),
	layerRow("profiles.*.base", "none; the key was removed in 0.7.0", "settings set", config.MigrationDoc),
	layerRow("profiles.*.image.node", "none; the key was removed in 0.7.0", "settings set", config.MigrationDoc),
	layerRow("profiles.*.image.jdk", "none; the key was removed in 0.7.0", "settings set", config.MigrationDoc),
	layerRow("profiles.*.image.apt", "none", "settings set", docLayer),
	layerRow("profiles.*.image.setup", "none", "settings set", docLayer),
	layerRow("profiles.*.image.skip_build_scripts", "false: the dependency install and build scripts run", "settings set", docLayer),
	layerRow("profiles.*.image.tools", "none", "settings set", docLayer),
	layerRow("profiles.*.image.key_files", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.build", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.test", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.lint", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.fix", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.render", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.probe", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.rerun_failed.command", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.rerun_failed.each", "none", "settings set", docLayer),
	layerRow("profiles.*.commands.reports", "the built-in report globs", "settings set", docLayer),
	layerRow("profiles.*.cache.*.key", "none", "settings set", docLayer),
	layerRow("profiles.*.cache.*.paths", "none", "settings set", docLayer),
	layerRow("profiles.*.resources.cpu", "4", "settings set", docLayer),
	layerRow("profiles.*.resources.memory", "8Gi", "settings set", docLayer),
	layerRow("profiles.*.timeouts.total", "the workflow's own", "settings set", docLayer),
	layerRow("profiles.*.timeouts.stage", "the workflow's own", "settings set", docLayer),
	layerRow("profiles.*.timeouts.verify", "the workflow's own", "settings set", docLayer),
	layerRow("profiles.*.timeouts.render", "the workflow's own", "settings set", docLayer),
	layerRow("profiles.*.timeouts.finalize_reserve", "the workflow's own", "settings set", docLayer),
	layerRow("profiles.*.rebuild.check", "the rebuild rule's own default", "settings set", docLayer),
	layerRow("profiles.*.rebuild.max_age", "the rebuild rule's own default", "settings set", docLayer),
	layerRow("profiles.*.rebuild.lockfiles", "the rebuild rule's own default", "settings set", docLayer),
	layerRow("profiles.*.rebuild.base", "the rebuild rule's own default", "settings set", docLayer),
	layerRow("profiles.*.rebuild.paths", "the rebuild rule's own default", "settings set", docLayer),
	layerRow("profiles.*.inputs.max_files", "the built-in cap", "settings set", docLayer),
	layerRow("profiles.*.inputs.max_bytes", "the built-in cap", "settings set", docLayer),
	layerRow("profiles.*.inputs.max_total_bytes", "the built-in cap", "settings set", docLayer),
	layerRow("profiles.*.checkout", "baked", "settings set", docLayer),

	layerRow("governance.review_ratio", "every review", "settings set", docLayer),
	layerRow("governance.review_floor_calls", "none", "settings set", docLayer),
	layerRow("governance.max_subagents", "Fugaro's own cap", "settings set", docLayer),
	layerRow("governance.stage_timeout.implement", "Fugaro's own stage timeouts", "settings set", docLayer),
	layerRow("governance.stage_timeout.review", "Fugaro's own stage timeouts", "settings set", docLayer),
	layerRow("governance.stage_timeout.fix", "Fugaro's own stage timeouts", "settings set", docLayer),
	layerRow("governance.stage_budget.implement", "Fugaro's own stage budgets", "settings set", docLayer),
	layerRow("governance.stage_budget.review", "Fugaro's own stage budgets", "settings set", docLayer),
	layerRow("governance.stage_budget.fix", "Fugaro's own stage budgets", "settings set", docLayer),
	layerRow("governance.stage_tokens.implement", "Fugaro's own stage token caps", "settings set", docLayer),
	layerRow("governance.stage_tokens.review", "Fugaro's own stage token caps", "settings set", docLayer),
	layerRow("governance.stage_tokens.fix", "Fugaro's own stage token caps", "settings set", docLayer),
	layerRow("governance.tiers.trivial.expected_usd", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.trivial.expected_minutes", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.trivial.max_subagents", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.small.expected_usd", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.small.expected_minutes", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.small.max_subagents", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.medium.expected_usd", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.medium.expected_minutes", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.medium.max_subagents", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.large.expected_usd", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.large.expected_minutes", "Fugaro's own tiers", "settings set", docLayer),
	layerRow("governance.tiers.large.max_subagents", "Fugaro's own tiers", "settings set", docLayer),

	layerRow("ab.rate", "0: no run is a challenger", "models", docModels),
	layerRow("ab.max_pairs_per_day", "the A/B design's own cap", "models", docModels),
	layerRow("ab.challengers", "none", "models", docModels),
	layerRow("ab.judge_model", "the A/B design's own judge", "models", docModels),
	layerRow("ab.exchange_rate", "the A/B design's own rate", "models", docModels),

	// The two schedules (R13). They are the installation's only keys in this
	// table whose loosening direction starts something: paused: false is what
	// makes a job run, and it is the operator's alone — a repository writes no
	// jobs: key at all, and the generic path writes its allowlist, which
	// carries neither of these. Owner update is the command that reads the
	// layer file and applies the value into the installation; `fugaro settings
	// set` refuses both keys, which is the state R13 ships.
	layerValueRow("jobs.reports.paused", "true", "update", docPeriodicReviews),
	layerValueRow("jobs.catalog_check.paused", "true", "update", docPeriodicReviews),

	// The project layer's own half of the security list (D1501) and the switch
	// that is its alone (D1517, D1503): the rows the layer adds for every
	// repository of the project, and always_level_3, which asks for the
	// adversarial review level for every change of the project. Owner `config`
	// for security.patterns' reason — the layer file is the second editor
	// D1515 names, and `fugaro config publish` is what carries it — and the
	// rows are judged by the rules the repository's own are, which is why they
	// share that row's answer. always_level_3 is the one that is no list: it is
	// a switch that only turns on, and false is a layer that states nothing.
	layerRow("security.patterns.*.name", "none", "config", docLayer),
	layerRow("security.patterns.*.match", "none", "config", docLayer),
	layerRow("security.patterns.*.category", "none", "config", docLayer),
	layerValueRow("security.always_level_3", "false", "config", docLayer),
}

// LocalPrefix marks a local row whose bare name a fugaro.yaml key already
// uses: the local config's version is its own file format's where a
// repository's fugaro.yaml version is the schema's, and the local gcp_project
// is the project the installation lives in where the fugaro.yaml one anchors a
// repository to it. One key may not mean two things (SD6), so the local one is
// named for its store, as the project layer's keys are (layer., SD14), and the
// read strips the prefix (localPath).
const LocalPrefix = "local."

// localRows are the local config's keys. The installation's own plumbing is in
// here too: the read prints one row per key, and a key an operator cannot see
// is a key nobody can explain.
var localRows = []Row{
	// The local file's own header, the two keys it shares a name with.
	// local.version's default is the value the loader leaves in place
	// (defaultValues below); the file's gcp_project is required.
	{Key: LocalPrefix + "version", Layers: []string{LayerLocal, LayerShared}, Merge: Replace, Owner: "setup", Doc: docSetup},
	loc(LocalPrefix+"gcp_project", "the file is refused (gcp_project: is required)", "setup", docSetup),

	loc("name", "the file is refused (name: is required)", "setup", docSetup),
	loc("region", "the file is refused (region: is required)", "setup", docSetup),
	loc("runs_bucket", "the file is refused (runs_bucket: is required)", "setup", docSetup),
	loc("bucket_url", "derived from runs_bucket", "setup", docSetup),
	loc("registry", "none: the legacy shared registry", "setup", docSetup),
	loc("registry_host", "none: no image has been pinned yet", "setup", docSetup),
	loc("setup_pending", "none: setup ran to the end", "setup", docSetup),
	loc("policy_generation", "none: no generation is recorded; the next update adopts the published one", "update", docSetup),
	loc("layer_generation", "none: no generation is recorded; the next update adopts the published one", "update", docSetup),
	loc("base_images", "the installation's own base image per kind", "setup", docSetup),
	loc("image_source", "none: no base pin has been recorded", "setup", docSetup),
	loc("backend", "cloud-run", "setup", "docs/backends.md"),
	loc("log_view", "the project's default logs", "setup", docSetup),
	loc("scheduler_region", "the region the jobs run in", "setup", docSetup),
	loc("user", "none", "setup", docSetup),
	// The keys of the installation a repository's own settings file also holds
	// say `settings set` where the allowlist carries them (D1178): commit_name,
	// commit_email, max_parallel, base_branch, update_check, the two budget keys
	// and watch.burn_alert_usd_per_hour are the generic path's, and a row naming
	// any other owner is a key `settings set` refuses while writing it (F13).
	// Everything else in these blocks is the converge's, and the read names
	// `fugaro setup` or `fugaro update` for it.
	loc("commit_name", "Fugaro", "settings set", docSetup),
	loc("commit_email", "Fugaro's own address", "settings set", docSetup),
	// max_parallel and build.machine_type are the local loader's own values:
	// Parse fills both for a file that sets them nowhere, so a run gets them
	// whatever the file says, and their rows carry a value rather than an
	// "unset means" sentence (from defaultValues below, as update_check's
	// does).
	{Key: "max_parallel", Layers: []string{LayerLocal, LayerShared}, Merge: Replace, Owner: "settings set", Doc: docSetup},
	loc("base_branch", "none: each repository's own branch", "settings set", docSetup),
	// update_check is the one key a process environment sets as well as a
	// file (spec §2.2: env overrides local), so its row carries the env level
	// the read prints for FUGARO_NO_UPDATE_CHECK. Its default is a value and
	// comes from defaultValues below.
	{Key: "update_check", Layers: []string{LayerLocal, LayerShared, LayerEnv}, Merge: Replace, Owner: "settings set", Doc: docSetup},
	loc("secret_rotation_days", "0: no rotation period is recorded", "secrets", docSetup),
	loc("secret_expires", "none: no expiry date is recorded", "secrets", docSetup),
	loc("bitbucket_api_roots", "the provider's public API", "secrets", docSetup),

	loc("batch.image", "Fugaro's own batch image", "setup", "docs/backends.md"),
	loc("batch.machine_type", "the batch design's own machine", "setup", "docs/backends.md"),
	loc("batch.boot_image", "the batch design's own boot image", "setup", "docs/backends.md"),
	loc("batch.network", "the project's default network", "setup", "docs/backends.md"),
	loc("batch.subnetwork", "the project's default subnetwork", "setup", "docs/backends.md"),
	loc("batch.service_account", "the project's default service account", "setup", "docs/backends.md"),
	loc("batch.secrets", "none", "secrets", "docs/backends.md"),
	loc("batch.socket_gid", "the image's own docker group", "setup", "docs/backends.md"),
	loc("batch.task_timeout", "Fugaro's own task timeout", "setup", "docs/backends.md"),

	loc("build.service_account", "the project's default service account", "setup", docSetup),
	{Key: "build.machine_type", Layers: []string{LayerLocal, LayerShared}, Merge: Replace, Owner: "setup", Doc: docSetup},
	loc("build.region", "the installation's region", "setup", docSetup),

	loc("terraform.state_bucket", "the installation's state bucket", "setup", docSetup),
	loc("terraform.alert_email", "none: no budget alert is mailed", "setup", docSetup),
	loc("terraform.operators", "none", "setup", docSetup),
	loc("terraform.admins", "none", "setup", docSetup),
	loc("terraform.budget_admins", "none", "setup", docSetup),
	loc("terraform.registry_cleanup", "Fugaro's own cleanup rule", "setup", docSetup),
	loc("terraform.no_log_isolation", "false: each run's logs are isolated", "setup", docSetup),
	loc("terraform.budget_backend", "false: no Realtime Database budget backend", "setup", docSetup),
	loc("terraform.coordinator", "false: no coordinator is deployed", "setup", docSetup),

	loc("compute_prices.*.vcpu_second_usd", "the built-in list price", "setup", docSetup),
	loc("compute_prices.*.gib_second_usd", "the built-in list price", "setup", docSetup),

	loc("budget.firebase_api_key", "none: the Realtime Database API key", "budget", docSetup),
	loc("budget.max_run_tokens", "the deployed ceiling's", "settings set", docSetup),
	loc("budget.rtdb_url", "none: no Realtime Database budget backend", "budget", docSetup),
	loc("budget.firebase_project", "the installation's GCP project", "budget", docSetup),
	loc("budget.token_signer", "none", "budget", docSetup),
	loc("budget.unreachable_grace", "Fugaro's own grace period", "settings set", docSetup),

	loc("model_prices.*.input_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.output_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_5m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_1h", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_read", "the built-in price", "models", docModels),
	loc("model_prices.*.web_search_per_1k", "the built-in price", "models", docModels),
	loc("model_prices.*.image_tokens", "the built-in price", "models", docModels),
	loc("model_prices.*.long_context.above_input_tokens", "the built-in price", "models", docModels),
	loc("model_prices.*.long_context.input_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.long_context.output_per_m", "the built-in price", "models", docModels),

	loc("providers.*.kind", "none: the provider is refused without one", "models", docModels),
	loc("providers.*.base_url", "the provider kind's own endpoint", "models", docModels),
	loc("providers.*.auth", "none", "models", docModels),
	loc("providers.*.secret", "none", "models", docModels),
	loc("providers.*.route_fee_pct", "0", "models", docModels),
	loc("providers.*.require_zdr", "false", "models", docModels),
	loc("providers.*.price_policy", "the provider kind's own policy", "models", docModels),
	loc("providers.*.models", "none", "models", docModels),
	loc("providers.*.allow_data_to", "nobody", "models", docModels),

	loc("routing.claude.via", "direct: the model is called at its own provider", "models", docModels),
	loc("routing.claude.models", "none", "models", docModels),
	loc("claude_accounts.*.allow_data_to", "nobody", "accounts", docModels),
	// The account choice: which account a run uses, and whether Fugaro may
	// pick one. The local config is their only store — neither a reviewed
	// repository nor a published layer may name an account, and a key that
	// could would be a way around an account's own allow_data_to — and the
	// generic path writes them, no family having been given one.
	locLocal("default_claude_account", "none: the run's own chain decides (the repository's agent.oauth_token, then the unsuffixed default)", "settings set", docModels),
	locLocal("stages.*.claude_account", "the project's default account", "settings set", docModels),
	{
		Key:      "claude_account_auto",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Default:  unset("false: nothing chooses an account for the owner"),
		Owner:    "settings set",
		Doc:      docModels,
		TypedWhy: "this lets Fugaro choose an account for the owner: a run then spreads one project across several accounts, which is the owner's own decision about the terms the subscriptions are held under",
	},
	// The choice keys' own vocabulary (docs/account-aliases, D2090–D2093): one
	// name the owner points at one account, so moving every stage that names it
	// is one edit. Local-only for `claude_account_auto`'s reason and one of its
	// own — a launcher who could write an alias into the shared file could name
	// an account the file never admitted, one indirection in.
	//
	// The owner is `config local` and not a family, because no command writes
	// the block yet: D2096 gives the write to `fugaro accounts alias set/rm`,
	// whose whole point is converging the node and the job set on the repoint,
	// and that command is a later task than the block and its rules. A row
	// naming the `accounts` family before the command exists would print an
	// owner for a key no subcommand of that family takes and refuse the write
	// in its name, which is the pair the owner column exists not to print
	// (D1178) — so the row says
	// what is true of it here: the key is stated in the machine's own config,
	// and `fugaro update` is what writes that file. The row moves to the
	// `accounts` family the day the command lands, and D2096 is the entry that
	// remembers it.
	locLocal("claude_account_aliases", "none: no alias is defined, and every choice key names an account of claude_accounts directly", "config local", docModels),
	// The installation's credentials, pools and crafts (docs/model-crafts/
	// spec.md §1–3). They are local-only for claude_accounts' reason, and the
	// one key here that names a credential rather than holding one is why: a
	// launcher who could write one into the shared file could aim every
	// teammate's runs at a credential the owner never admitted (F12, D1628).
	locLocal("credentials.*.name", "required: the name a craft's access and a pool's members speak", "accounts", docCrafts),
	locLocal("credentials.*.type", "required: the provider that serves it, one of "+strings.Join(config.SupportedCredentialTypes(), ", "), "accounts", docCrafts),
	locLocal("pools.*.name", "required: the name a craft's access speaks", "accounts", docCrafts),
	locLocal("pools.*.strategy", config.PoolStrategyFailover, "accounts", docCrafts),
	locLocal("pools.*.members", "required: a pool with no members serves nothing", "accounts", docCrafts),
	// A pool's owner and the people it is served to (D1936, R10). They are
	// local-only like the rest of the block, and they are the two keys that
	// decide WHO a pool's credentials are handed to, which is why they belong
	// to the owner and to no shared file a launcher can write. legacy_open is
	// the migration's marker and not a key an operator writes: it is in the
	// registry so that `fugaro settings` can name it, and the value it states
	// says as much.
	locLocal("pools.*.owner", "required: the pool is served to its owner and the users it names", "accounts", docCrafts),
	locLocal("pools.*.allowed_users", "only the owner", "accounts", docCrafts),
	locLocal("pools.*.legacy_open", "false: only the policy migration writes this, for a pool that predates owners", "accounts", docCrafts),
	// The pools' subscription switch (F22, D1622, D1636): whether a run may
	// spread a project across two or more Claude OAuth subscriptions at all.
	// With it off a pool of two or more subscriptions serves nothing and a
	// subscription inside a mixed pool is stepped over, which is the state
	// every installation is in until the owner says otherwise.
	//
	// It is the successor of claude_account_auto — the key the account walk's
	// own switch is written under — and it keeps that switch's refusals: the
	// local config alone, and the typed form when it is turned on, since
	// spreading a project over subscriptions is a decision about the terms the
	// subscriptions are held under. It is a literal rather than a locLocal call
	// because it carries TypedWhy, which no helper sets.
	{
		Key:      "subscription_spread",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Default:  unset("false: a run spreads across one subscription at a time"),
		Owner:    "settings set",
		Doc:      docCrafts,
		TypedWhy: "this lets a run spread a project across two or more Claude OAuth subscriptions, which is the owner's own decision about the terms the subscriptions are held under",
	},
	locLocal("crafts.coder.model", "required: the coder implements, so a run needs its model", "models", docCrafts),
	locLocal("crafts.coder.access", "required: the coder's calls go out on a credential or a pool", "accounts", docCrafts),
	locLocal("crafts.review.model", "the coder's model", "models", docCrafts),
	locLocal("crafts.review.access", "the coder's access", "accounts", docCrafts),
	locLocal("crafts.senior-review.model", "required: the senior review is the second opinion a run needs", "models", docCrafts),
	locLocal("crafts.senior-review.access", "required: the senior review's calls go out on a credential or a pool", "accounts", docCrafts),
	locLocal("crafts.security-review.model", "the senior review's model", "models", docCrafts),
	locLocal("crafts.security-review.access", "the senior review's access", "accounts", docCrafts),

	loc("project_secrets.*.allow_data_to", "nobody", "secrets", docSetup),
	loc("project_secrets.*.proxy", "none", "secrets", docSetup),

	loc("workflow_secrets.*.allow_data_to", "nobody", "secrets", docSetup),
	loc("workflow_secrets.*.proxy", "none: refused, D1275", "secrets", docSetup),

	loc("job_profiles.*.provider", "none", "project", docSetup),
	loc("job_profiles.*.cpu", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.memory", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.timeout_class", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.secrets", "none", "project", docSetup),
	loc("job_profiles.*.allow_repos", "every repository of the project", "project", docSetup),
	loc("job_profiles.*.claude_accounts", "every account of the project", "project", docSetup),

	loc("watch.burn_alert_usd_per_hour", "Fugaro's own alert threshold", "settings set", docSetup),
	loc("mailbox.keychain_item", "the keychain item the subscription design names", "secrets", docSetup),
	loc("credentials_service.url", "none: the credentials service is not deployed", "setup", docSetup),

	loc("endpoints.run", "the regional Cloud Run endpoint", "setup", docSetup),
	loc("endpoints.logging", "the regional Cloud Logging endpoint", "setup", docSetup),
	loc("endpoints.secret_manager", "the global Secret Manager endpoint", "setup", docSetup),
	loc("endpoints.cloud_build", "the global Cloud Build endpoint", "setup", docSetup),
	loc("endpoints.batch", "the regional Cloud Batch endpoint", "setup", docSetup),
	loc("endpoints.compute", "the regional Compute endpoint", "setup", docSetup),
	loc("endpoints.storage", "the global Storage endpoint", "setup", docSetup),
	loc("endpoints.iam", "the global IAM endpoint", "setup", docSetup),
	loc("endpoints.artifact_registry", "the regional Artifact Registry endpoint", "setup", docSetup),
	loc("endpoints.resource_manager", "the global Resource Manager endpoint", "setup", docSetup),
	loc("endpoints.cloud_scheduler", "the regional Cloud Scheduler endpoint", "setup", docSetup),
	loc("endpoints.service_usage", "the global Service Usage endpoint", "setup", docSetup),
	loc("endpoints.cloud_billing", "the global Cloud Billing endpoint", "setup", docSetup),
	loc("endpoints.firebase_database", "the global Firebase Database endpoint", "setup", docSetup),
	loc("endpoints.api_keys", "the global API Keys endpoint", "setup", docSetup),
	loc("endpoints.firebase_management", "the global Firebase Management endpoint", "setup", docSetup),
	loc("endpoints.identity_toolkit", "the global Identity Toolkit endpoint", "setup", docSetup),
	loc("endpoints.firestore", "the global Firestore endpoint", "setup", docSetup),
	loc("endpoints.firebase_rules", "the global Firebase Rules endpoint", "setup", docSetup),
	loc("endpoints.iam_credentials", "the global IAM Credentials endpoint", "setup", docSetup),
	loc("endpoints.cloud_asset", "the global Cloud Asset endpoint", "setup", docSetup),
	loc("endpoints.no_auth", "false: every call carries credentials", "setup", docSetup),

	loc("review_floor", "none: no floor is added", "rigor floor", docRigor),

	// Unset is not "nobody" on an installation that has opted into the
	// credentials service: the module answers an absent list with its own
	// verifier account, the identity the installation's runs verify as (CSR14,
	// verifier.tf), so the row says both states rather than the one that is
	// only true while the service is off.
	loc("verifier_accounts", "nobody may ask the credentials service to verify a signature, except on an installation whose credentials service is deployed, where the installation's own verifier account may", "secrets", docSetup),

	// The image catalog decides which image a run's job executes in, so it is
	// the operator's own list in the local file like the verifier list above,
	// and every member of an entry is a row because a settings read names the
	// leaf a value came from rather than the block.
	loc("image_catalog.entries.*.name", "none", "project", docImages),
	loc("image_catalog.entries.*.tags", "none", "project", docImages),
	loc("image_catalog.entries.*.source.kind", "none", "project", docImages),
	loc("image_catalog.entries.*.source.repo", "none", "project", docImages),
	loc("image_catalog.entries.*.source.path", "none", "project", docImages),
	loc("image_catalog.entries.*.source.branch", "none", "project", docImages),
	loc("image_catalog.entries.*.source.context", "none", "project", docImages),
	loc("image_catalog.entries.*.source.inline", "none", "project", docImages),
	loc("image_catalog.entries.*.source.image", "none", "project", docImages),
	loc("image_catalog.entries.*.description", "none", "project", docImages),
	loc("image_catalog.entries.*.default", "false", "project", docImages),
	// The services an entry's image provides are the same image policy one
	// step further (D1904): the run starts them before its tests, and the
	// entry's own list is the only source, so a person reading the row learns
	// which list decided it. The owner is the `fugaro project` family, as the
	// map's row below is `fugaro image map`'s: this leaf belongs to a catalog
	// entry and its path carries a `*`, so the generic write refuses it twice
	// over (settings_set.go) and the family is the command that writes it.
	loc("image_catalog.entries.*.services", "none: the run starts no backing service", "project", docImages),
	// The repo-to-image map is the catalog's counterpart one step down (D1906):
	// which entry each of the project's repositories runs, so it is the image
	// policy too and one row for the whole map because the map's keys are the
	// project's repositories and its values the entries — a settings read names
	// the leaf a value came from, and the leaf here is the map. `fugaro image
	// map set` writes it, which is why the owner names that family: the generic
	// path writes no image policy, and a person reading the row is sent to the
	// command that does.
	loc("image_map", "none: no repository is mapped, and the catalog's default entry decides the image", "image map", docImages),
	// The bake switch is the setup wizard's question (D1901), answered yes when
	// it is asked with nothing else, so an installation that never answers it is
	// told how the absent key reads rather than what a value would be. IS7 asks
	// it and writes the answer through the generic path — the same key is
	// changed later with `fugaro settings set bake_repos false`, which is why the
	// owner is that path rather than a family: there is no cross-key rule for a
	// boolean whose only reader is the resolver, and a family for one key would
	// be a command family that does nothing the generic write does not.
	loc("bake_repos", "true: a repository the build can read is baked into the image its job runs", "settings set", docImages),
	// The triage switch is the other policy key of that shape — a local key the
	// node is published from — and it is the catalog's rather than the bake
	// switch's: `fugaro project policy publish` is what carries it, which is why
	// the owner names the project family and not the generic path. The switch is
	// the owner's because it decides which of a review round's findings hold a
	// pull request back (D1854), and no command writes it beyond the publish: an
	// operator states it in the file and runs the update.
	loc("triage", "true: a review round's findings are judged by the triage stage in the modes that triage at all, and a written false turns the stage off for the project", "project", docTriage),
	// The clone URLs the project's repositories clone from (settings spec
	// §5.6, SD5, D842). The block moved out of the policy node and into the
	// owner's local config, and `fugaro project clone-url` is the family that
	// writes it: an entry's key is a canonical repository name and its value is
	// held to the node's own rule (projectpolicy.ValidCloneURLs), which is a
	// check no generic scalar write can make. One row for the whole map, like
	// the image map above, and local-only: a launcher who could write the block
	// into the shared file would be choosing the code every teammate's runs
	// execute.
	locLocal("clone_urls", "none: a run clones from its provider's own URL for its repository (https://github.com/owner/name.git)", "project clone-url", docCloneURLs),

	// The orchestrator daemon's own guardrails (D1239, D1234) are the owner's
	// policy like the catalog above, and one row per member for the catalog's
	// reason. They carry Tighten rather than the ordinary Replace: the smaller
	// cap wins whatever layer it was written in (policy.Merge), so a person or
	// a project may lower one and never raise it, and the model a decision
	// class runs on moves up the tier order or not at all. The default of each
	// count is the figure policy.WithDefaults fills the ceiling layer with,
	// named by the constant that holds it — the effective cap is the person's
	// own max_parallel, which is why the sentence names that key rather than a
	// number.
	//
	// Owner `project policy`, not `project`: the block is stated in the local
	// config and `fugaro project policy publish` is what carries it to the node
	// every run of the project is held to (buildProjectPolicy copies
	// lc.Orchestrator into the node, and policyCeilingValues spells these six
	// members back out of it), which is triage's fact above. The token is this
	// block's own because `project` is one of settingsFamilyCommands: a row
	// naming it is refused by name of `fugaro project` — a wording of the
	// write's that this task does not touch — and the read prints that bare
	// command, which D1178 asks a sentence in place of for a key no subcommand
	// edits. It was `config` until G12, whose read printed that token's
	// sentence — the layer file `fugaro config publish` carries — beside a row
	// whose own target_layer is `local`, a file the publish never reads.
	locTighten("orchestrator.max_running", "the person's max_parallel", "project policy", docOrchestrator),
	locTighten("orchestrator.batch_size", "10 (policy.DefaultBatchSize)", "project policy", docOrchestrator),
	locTighten("orchestrator.spawn_per_minute", "2 (policy.DefaultSpawnPerMinute)", "project policy", docOrchestrator),
	locTighten("orchestrator.spawn_per_hour", "12 (policy.DefaultSpawnPerHour)", "project policy", docOrchestrator),
	locTighten("orchestrator.spawn_per_day", "60 (policy.DefaultSpawnPerDay)", "project policy", docOrchestrator),
	// The one key of the block that is not a count: a class the map does not
	// carry is at its floor, which is the bottom of the order for a routine
	// decision and the top for a security-sensitive one (policy.ValidOrchestrator
	// refuses a name outside the order and a model under the class's floor, so
	// the classes' two ends are the two answers an absent key can take).
	locTighten("orchestrator.decision_model_per_class", "each class's floor: haiku for a routine decision and opus for a sensitive one", "project policy", docOrchestrator),

	// The four members of a repository's entry a PERSON writes are the generic
	// writer's (D1173, docs/config-management/spec.md §3.4): they are a map of
	// scalars and one list, which is what `fugaro settings set
	// repos.<owner/name>.<key>` writes, and the key's own name is the entry's —
	// the shape a bare scalar cannot express (a whole `workflows` selection) is
	// `fugaro project repos set`'s and is not a reason to give these four a
	// second path. They are listed in the generic allowlist beside them
	// (settingsGenericKeys), and their owner is the allowlist's own default
	// token rather than a file's, because the two answer one question (D1178):
	// a key the generic path writes is `settings set`'s, and these four are
	// stated in the machine's own config rather than in the layer file, so the
	// file-carriers' sentences are files they are not in (D1515).
	//
	// The owner is the allowlist's own token, `settings set`, for the reason
	// D1178 gives it to every key the list carries. The file-carrier tokens name
	// files these four keys are not in — the project layer `fugaro config
	// publish` carries, and a checkout's own `fugaro.yaml` — so a row naming one
	// has the read print that file's sentence beside a key stated in neither.
	// max_parallel above is the same row shape and carries the same token.
	//
	// The owner is the allowlist's own token, `settings set`, for the reason
	// D1178 gives it to every key the list carries. The file-carrier tokens name
	// files these four keys are not in — the project layer `fugaro config
	// publish` carries, and a checkout's own `fugaro.yaml` — so a row naming one
	// has the read print that file's sentence beside a key stated in neither.
	// max_parallel above is the same row shape and carries the same token.
	//
	// The owner is the allowlist's own token, `settings set`, for the reason
	// D1178 gives it to every key the list carries. The file-carrier tokens name
	// files these four keys are not in — the project layer `fugaro config
	// publish` carries, and a checkout's own `fugaro.yaml` — so a row naming one
	// has the read print that file's sentence beside a key stated in neither.
	// max_parallel above is the same row shape and carries the same token.
	//
	// vertex and credentials_service_ready below keep the `project` owner and
	// are NOT in that allowlist: they are not a person's settings at all — a
	// run records what it read and applied (this file's row comments) — so the
	// generic path's refusal is the right answer for them.
	loc("repos.*.provider", "none", "settings set", docSetup),
	loc("repos.*.base_branch", "the project's base_branch", "settings set", docSetup),
	loc("repos.*.workflows", "every workflow the repository defines", "settings set", docSetup),
	loc("repos.*.github_app_id", "none", "settings set", docSetup),
	loc("repos.*.vertex", "false", "project", docSetup),
	// The credentials service's switch, as `fugaro update` recorded it:
	// written from the bundle it read AND the agent.auth the plan was built
	// for, on the entry of the repository whose root the run applies, and
	// read back by the switch to decide whether this repository's runs may go
	// through the service (localcfg.Repo.CredentialsServiceReady,
	// docs/credentials-service/default-on.md §3 DON8). It is a
	// machine-written value like repos.*.vertex beside it, and a row here for
	// the same reason: every key a config file can hold is one a settings
	// read has to be able to name.
	loc("repos.*.credentials_service_ready", "false: no update has applied this repository's root with the service on — its bundle holds no model credential, or its agent.auth is not the cascade", "project", docSetup),

	// The workspace keys (docs/workspaces/spec.md §2, §5, §6), all eleven of
	// them local-only: the pin is the member's own trust and the member table
	// is the workspace owner's own declaration, and a launcher who could write
	// either into the shared file could aim this project's exchange and this
	// project's database grant (internal/cli/sharedcfg.go's sharedForbidden).
	// Their owner is the `fugaro workspace` family, so the generic writer
	// refuses them (D853) — a member entry and a pin are written whole by the
	// commands that know what the four values have to agree about.
	locLocal("workspace", "this project is a member of no workspace", "workspace", docWorkspace),
	locLocal("workspace_pin.firebase_project", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.issuer", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.assertion_key", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.service_account", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_members.*.repos", "the member contributes no repository", "workspace", docWorkspace),
	locLocal("workspace_members.*.job_service_accounts", "no job service account is declared for the member", "workspace", docWorkspace),
	locLocal("workspace_members.*.firebase_project", "none", "workspace", docWorkspace),
	locLocal("workspace_members.*.fp", "none", "workspace", docWorkspace),
	locLocal("workspace_members.*.exchange_url", "none", "workspace", docWorkspace),
	locLocal("workspace_cap.monthly", "the workspace states no cap", "workspace", docWorkspace),

	// The terminal Issues section's nudge thresholds (T-IS15). They are
	// local-only rather than loc's two layers: the section is printed on the
	// machine running the digest, the committed report carries no part of it,
	// and no other machine acts on this one's thresholds — so the shared file
	// never holds the block (Shared drops it) and a row advertising the shared
	// layer would name a store the key cannot have. Their defaults are values
	// the loader leaves in place, so they come from defaultValues below, as
	// update_check's does, rather than from an "unset means" sentence.
	//
	// Owner `config local` (G12): the machine's own config is their only store,
	// and no command edits them key by key — so the row names the file and the
	// command that writes it, the way `config` names the layer file and
	// `fugaro config publish`. It was `config` until G12, whose read printed
	// that token's sentence — a file these two are not stated in — beside a row
	// whose own target_layer is `local`.
	{Key: "issues.nudge_after_days", Layers: []string{LayerLocal}, Merge: Replace, Owner: "config local", Doc: docTroubleshoot},
	{Key: "issues.nudge_recurrence", Layers: []string{LayerLocal}, Merge: Replace, Owner: "config local", Doc: docTroubleshoot},
}

// cloudRows are the Realtime Database's own keys (spec §2.1, §2.2): the mode a
// lease compares against, the two cap scopes, the reserve limit and the global
// kill switch. The caps layer is their only store — no file carries them and no
// layer above them exists — so a read with --offline answers every one of them
// `unread: offline`, and the value a run gets is what the node holds.
//
// Each default is the sentence saying what an absent node means, because an
// absent node is not a value: no cap, no reserve limit, no run stopped, and the
// mode that behaviour is named by (observe). A row that claimed a value here
// would have the read print it for a database nobody asked (spec §2.5).
//
// The per-repository nodes (`config/caps/repos/<slug>`, `config/kill/repos/
// <slug>`) have no row: their key varies with a repository, and the level those
// two scopes combine at is `caps.repo`, a name `$defs.decider` does not carry —
// so a row claiming one would print a WINS word the schema refuses.
var cloudRows = []Row{
	{Key: "caps.mode", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("observe"), Owner: "budget", Doc: docSetup},
	{Key: "caps.global.per_run_usd", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("no global cap"), Owner: "budget", Doc: docSetup},
	{Key: "caps.global.per_day_usd", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("no global cap"), Owner: "budget", Doc: docSetup},
	{Key: "caps.defaults.per_run_usd", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("no default cap"), Owner: "budget", Doc: docSetup},
	{Key: "caps.defaults.per_day_usd", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("no default cap"), Owner: "budget", Doc: docSetup},
	{Key: "caps.max_reserve_usd", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("no reserve limit"), Owner: "budget", Doc: docSetup},
	{Key: "kill.global.on", Layers: []string{LayerCaps}, Merge: Replace, Default: unset("false: no run is stopped"), Owner: "budget", Doc: docSetup},
}

// stageAccountRows are the concrete spellings of the stages.*.claude_account
// pattern above, one per stage the config accepts — config.RunStages, the
// closed set config.CheckStageSegment refuses a segment outside of.
//
// The pattern row is the registry's own shape: the local config's stages: is a
// map, so the walk TestRegistryCoversEveryConfigField makes of it yields
// `stages.*.claude_account` and nothing else. A concrete row per stage is what
// makes the key addressable as it is written: `fugaro settings get
// stages.review.claude_account` answers the row the owner changed, and a bare
// `fugaro settings` prints every stage's own account rather than one wildcard
// row whose value is the account of whichever stage sorts first — the state the
// read had before this, and one that hides a second stage's account.
//
// The set is derived and not written out beside the pattern, so a stage added to
// config.RunStages lands here, in the schema's key list and in the write path's
// allowlist together: TestSettingsSchema fails when the schema has not moved
// with it, and internal/cli's TestSettingsWritesEveryRunStageAccountKey fails
// when the allowlist has not.
func stageAccountRows() []Row {
	out := make([]Row, 0, len(config.RunStages))
	for _, stage := range config.RunStages {
		out = append(out, locLocal("stages."+stage+".claude_account", "the project's default account", "settings set", docModels))
	}
	return out
}

// defaultValues are the rows whose default is a value the loaders leave in
// place, named by the constant that holds it — or by no constant where the
// loader is the only place the value lives. They are held apart from the tables
// above because the constant, not the row, is where the value lives:
// TestRegistryDefaultsMatchApplyDefaults reads each one back out of a resolved
// config.
var defaultValues = map[string]Default{
	"update_check":       value("true", "config.DefaultUpdateCheck"),
	"max_parallel":       value("20", "localcfg.DefaultMaxParallel"),
	"build.machine_type": value("E2_HIGHCPU_8", "localcfg.DefaultBuildMachineType"),
	// The local file's own format version: the loader fills the version this
	// release writes for a file that sets it nowhere (localcfg.Parse), which is
	// localcfg.ConfigVersion, so the value is written here and read back out of
	// a parsed config like the two above.
	"local.version": value("2", "localcfg.ConfigVersion"),
	// The Issues section's nudge thresholds (T-IS15): the loader answers the
	// constants for a file that sets neither, through the accessors rather
	// than the fields, so "the file wrote nothing" and "the file wrote the
	// default" are one reading here.
	"issues.nudge_after_days": value("7", "localcfg.DefaultIssuesNudgeAfterDays"),
	"issues.nudge_recurrence": value("3", "localcfg.DefaultIssuesNudgeRecurrence"),
}

// sensitiveKeys are the keys whose value is never printed (spec §2.3): the
// Realtime Database API key is public but is a credential-shaped string, so
// its row prints set or unset and nothing more. Every other key's value is a
// name, a path or a number, and TestRegistryMarksEverySensitiveKey holds this
// list to the keys that look like credentials.
var sensitiveKeys = map[string]bool{
	"budget.firebase_api_key": true,
}

// sharedWithLocal are the keys a run gets from the local config as well as
// from fugaro.yaml, so `--layer local` reads them (spec §2.2). The ceiling
// keys are there because the local file holds the ceiling the deployed job
// carries. version and gcp_project are not: both documents carry the word and
// mean different things by it — the local file's version is its own format's,
// where a repository's fugaro.yaml version is the schema's, and the local
// gcp_project is the project the installation lives in, where the
// fugaro.yaml one anchors a repository to it — so each fact has its own row
// (SD6) rather than one row two stores would claim.
var sharedWithLocal = map[string]bool{
	"budget.mode":           true,
	"budget.per_run_usd":    true,
	"budget.allowed_models": true,
}

// Sorted reports whether the table is in key order, which the read relies on
// for a stable table.
func Sorted() bool {
	return slices.IsSortedFunc(rows, func(a, b Row) int { return strings.Compare(a.Key, b.Key) })
}
