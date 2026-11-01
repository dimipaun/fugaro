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
	Owner     string // the command family that writes it
	Doc       string // the document that must state it
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
	"version":     {Default: unset("the file is refused (version: 1 is required)")},
	"project":     {Default: unset("the file is refused (project: is required)")},
	"gcp_project": {Default: unset("the installation's GCP project")},
	"profile":     {Default: unset("the project layer's default_profile")},

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
	"workflows.*.timeouts.total":            {Default: unset("90m")},
	"workflows.*.timeouts.stage":            {Default: unset("40m, or the total when it is shorter")},
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
}

// buildRows is the table, assembled from the three documents' own sources: the
// `fugaro.yaml` rows from config.Scopes (so the two cannot drift), the project
// layer's from config.ProjectLayer, the local config's from localcfg.Config.
func buildRows() []Row {
	out := make([]Row, 0, len(config.Scopes)+len(layerRows)+len(localRows))
	for _, s := range config.Scopes {
		out = append(out, yamlRow(s))
	}
	out = append(out, layerRows...)
	out = append(out, localRows...)
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

// layerRows are the project layer's keys. They are written out rather than
// derived because a struct field is not a setting until someone decides what
// its absence means and who may write it; TestRegistryCoversEveryConfigField
// walks config.ProjectLayer and fails on a field with no row here.
//
// The layer's own block names are prefixed `layer.` (SD14): the project
// layer's `profiles.<n>` is a workflow profile and the local config's is a job
// profile, and one key may not mean two things.
var layerRows = []Row{
	layerRow("version", "the file is refused (version: is required)", "settings set", docLayer),
	layerRow("project", "the file is refused (project: is required)", "settings set", docLayer),
	layerRow("gcp_project", "the installation's GCP project", "settings set", docLayer),
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
	layerRow("defaults.agent.recipe", "the built-in recipe", "models", docModels),
	layerRow("defaults.rigor.default", "the built-in mode (pro)", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.name", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.match", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.mode", "none", "rigor", docRigor),

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
	loc("commit_name", "Fugaro", "setup", docSetup),
	loc("commit_email", "Fugaro's own address", "setup", docSetup),
	// max_parallel and build.machine_type are the local loader's own values:
	// Parse fills both for a file that sets them nowhere, so a run gets them
	// whatever the file says, and their rows carry a value rather than an
	// "unset means" sentence (from defaultValues below, as update_check's
	// does).
	{Key: "max_parallel", Layers: []string{LayerLocal, LayerShared}, Merge: Replace, Owner: "config", Doc: docSetup},
	loc("base_branch", "none: each repository's own branch", "config", docSetup),
	// update_check is the one key a process environment sets as well as a
	// file (spec §2.2: env overrides local), so its row carries the env level
	// the read prints for FUGARO_NO_UPDATE_CHECK. Its default is a value and
	// comes from defaultValues below.
	{Key: "update_check", Layers: []string{LayerLocal, LayerShared, LayerEnv}, Merge: Replace, Owner: "setup", Doc: docSetup},
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
	loc("terraform.launchers", "none", "setup", docSetup),
	loc("terraform.operators", "none", "setup", docSetup),
	loc("terraform.budget_admins", "none", "setup", docSetup),
	loc("terraform.registry_cleanup", "Fugaro's own cleanup rule", "setup", docSetup),
	loc("terraform.no_log_isolation", "false: each run's logs are isolated", "setup", docSetup),
	loc("terraform.budget_backend", "false: no Realtime Database budget backend", "setup", docSetup),
	loc("terraform.coordinator", "false: no coordinator is deployed", "setup", docSetup),

	loc("compute_prices.*.vcpu_second_usd", "the built-in list price", "setup", docSetup),
	loc("compute_prices.*.gib_second_usd", "the built-in list price", "setup", docSetup),

	loc("budget.firebase_api_key", "none: the Realtime Database API key", "budget", docSetup),
	loc("budget.max_run_tokens", "the deployed ceiling's", "budget", docSetup),
	loc("budget.rtdb_url", "none: no Realtime Database budget backend", "budget", docSetup),
	loc("budget.firebase_project", "the installation's GCP project", "budget", docSetup),
	loc("budget.token_signer", "none", "budget", docSetup),
	loc("budget.unreachable_grace", "Fugaro's own grace period", "budget", docSetup),

	loc("model_prices.*.input_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.output_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_5m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_1h", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_read", "the built-in price", "models", docModels),
	loc("model_prices.*.web_search_per_1k", "the built-in price", "models", docModels),
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
	loc("project_secrets.*.allow_data_to", "nobody", "secrets", docSetup),
	loc("project_secrets.*.proxy", "none", "secrets", docSetup),

	loc("job_profiles.*.provider", "none", "project", docSetup),
	loc("job_profiles.*.cpu", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.memory", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.timeout_class", "the workflow's own", "project", docSetup),
	loc("job_profiles.*.secrets", "none", "project", docSetup),
	loc("job_profiles.*.allow_repos", "every repository of the project", "project", docSetup),
	loc("job_profiles.*.claude_accounts", "every account of the project", "project", docSetup),

	loc("watch.burn_alert_usd_per_hour", "Fugaro's own alert threshold", "budget", docSetup),
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

	loc("repos.*.provider", "none", "project", docSetup),
	loc("repos.*.base_branch", "the project's base_branch", "project", docSetup),
	loc("repos.*.workflows", "every workflow the repository defines", "project", docSetup),
	loc("repos.*.github_app_id", "none", "project", docSetup),
	loc("repos.*.vertex", "false", "project", docSetup),
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
	// The local file's own format version: the loader fills 1 for a file that
	// sets it nowhere (localcfg.Parse) and no constant holds it, so the value
	// is written here and read back out of a parsed config like the two above.
	"local.version": value("1", ""),
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
