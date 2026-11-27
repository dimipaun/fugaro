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
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/tier"
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
	// RetiredWhy is the sentence saying the key is retired: named and still
	// honoured by this release, and rejected by the next one (D3063, R3 of
	// docs/credentials-belong-to-models/reconciliation.md). Empty is a live
	// key. A `fugaro.yaml` key and a project layer's own take it from
	// config.ScopeRow.RetiredWhy rather than restating it (yamlRow, buildRows),
	// so the registry and the config's table cannot drift; the installation's
	// own keys — the local config's `routing.claude.via` and
	// `subscription_spread`, the policy's `credentials.fallback` and
	// `credentials.wait_threshold` — carry the sentence here (MG5 of
	// docs/model-credentials-routing/implementation-plan.md).
	RetiredWhy string
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

// routingKinds is routing.Kinds as a default's sentence: the closed set of a
// chain's source kinds is declared once, in the contract, and this table only
// spells it (routing.ValidKind refuses the sixth, and so does RT2's decode).
func routingKinds() string {
	names := make([]string, 0, len(routing.Kinds))
	for _, k := range routing.Kinds {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}

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
	// agent.auth is retired and its document is the routing table's (F23 of
	// docs/credentials-belong-to-models/reconciliation.md: the row named
	// `docs/credentials-service.md`, which is no file — the folder is
	// `docs/credentials-service/` — and what replaced the key is the table).
	"agent.auth":         {Default: value(config.DefaultAgentAuth, "config.DefaultAgentAuth"), Doc: docRouting},
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
		Default: unset("none: refused where the coder cannot read an image and the task names one"),
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
	// The crafts (docs/model-crafts/spec.md §3), the spec's own table
	// read back: the coder and the senior review are what a run cannot do
	// without, and the review, the security review and the merge judge follow
	// the craft above them when they set nothing of their own.
	"agent.crafts.coder.model":            {Owner: "models", Default: unset("required: the coder implements, so a run needs its model"), Doc: docCrafts},
	"agent.crafts.coder.access":           {Owner: "accounts", Default: unset("required: the coder's calls go out on a credential or a pool"), Doc: docCrafts},
	"agent.crafts.review.model":           {Owner: "models", Default: unset("the coder's model"), Doc: docCrafts},
	"agent.crafts.review.access":          {Owner: "accounts", Default: unset("the coder's access"), Doc: docCrafts},
	"agent.crafts.senior-review.model":    {Owner: "models", Default: unset("required: the senior review is the second opinion a run needs"), Doc: docCrafts},
	"agent.crafts.senior-review.access":   {Owner: "accounts", Default: unset("required: the senior review's calls go out on a credential or a pool"), Doc: docCrafts},
	"agent.crafts.security-review.model":  {Owner: "models", Default: unset("the senior review's model"), Doc: docCrafts},
	"agent.crafts.security-review.access": {Owner: "accounts", Default: unset("the senior review's access"), Doc: docCrafts},
	"agent.crafts.merge-judge.model":      {Owner: "models", Default: unset("the coder's model"), Doc: docCrafts},
	"agent.crafts.merge-judge.access":     {Owner: "accounts", Default: unset("the coder's access"), Doc: docCrafts},

	// The escalation ladder's two model grades (docs/outer-loop-escalation E2,
	// D2039): the model each rung runs and the credential or pool its calls go
	// out on, the pair a craft carries. The MERGE column reads replace and the
	// rule is stricter than that word: a grade is the project's own model
	// choice, so a repository's entry never decides the value a run gets —
	// internal/policy records it in the run's ignored list (tightenGrades) and
	// the project's stands. What a repository's entry says is which model it
	// would have run, and the record is where that is kept.
	"model_grades.strong.model":     {Owner: "models", Default: unset("none: a rung whose grade the project sets nowhere refuses and asks"), Doc: docEscalation},
	"model_grades.strong.access":    {Owner: "accounts", Default: unset("the project's default credential"), Doc: docEscalation},
	"model_grades.strongest.model":  {Owner: "models", Default: unset("none: a rung whose grade the project sets nowhere refuses and asks"), Doc: docEscalation},
	"model_grades.strongest.access": {Owner: "accounts", Default: unset("the project's default credential"), Doc: docEscalation},

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
	// The keep-warm beat's switch (R10, D2985). On is the default and the
	// constant is the literal "true" rather than a config constant, as
	// git.pr.early_draft's is: the readers are the accessor
	// (config.Agent.WorkspaceBeatOn) and this row, and a Go constant between
	// them would be a third name for one value.
	"agent.workspace_beat": {Default: value("true", ""), Doc: docWorkspaceService},
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
	// The review-against-task switch (R7 of docs/review-against-task/): an
	// absent key is the check running — every review stage is handed the task
	// the pull request was written for — so the DEFAULT column says so rather
	// than reading "none", which a reader would take for the check being off.
	"agent.review_against_task": {
		Default: unset("on (config.Agent.ReviewAgainstTaskOn)"),
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

	// A repository's A/B opt-out (D556, F18). The block is the project layer's
	// and rate is the one leaf a repository may write, only downward to 0
	// (validateRepoAB reads a repository's ab: block and refuses every other
	// key), so the row is the block's own family's: `models` names the command
	// that decides sampling, as the five `layer.ab.*` rows beside it do, and the
	// write refuses the key in that family's name rather than offering a
	// generic edit at a rate the project's own ceiling decides.
	"ab.rate": {Owner: "models", Default: unset("the project layer's ab.rate, or 0: no run is a challenger"), Doc: docModels},

	// The tags a job of a repository's workflow needs (D1155, spec
	// project-image-catalog §Repo configuration item 3). A list of strings is
	// what the generic path's --add writes (D1162), so the row keeps the default
	// owner the yamlRow fallback gives it and needs no family of its own: the
	// catalog validates a tag against the entries it publishes when a run
	// selects one, where the project's catalog is at hand and this command's is
	// not.
	//
	// The row is reachable only once the allowlist names the key, which is the
	// write path's own task (W3 of docs/config-ownership): until then the write
	// refuses the spelling with "no setting has that name" and the row is
	// testdata/writer_gaps.golden's.
	"workflows.*.needs": {Default: unset("none: the job needs no tag of the entry the run picks"), Doc: docImages},

	// A repository's ask for an A/B reviewer arm (O12, the owner's ruling of
	// 2026-11-16): the review level the arm replays over the champion's own
	// recorded patch, one ask per workflow of the file. It keeps the default
	// owner the yamlRow fallback gives it, exactly as workflows.*.needs beside
	// it does, and it is reachable only once the write path's allowlist names
	// the key (W3 of docs/config-ownership), so the row is
	// testdata/writer_gaps.golden's until then.
	"workflows.*.ab.reviewer_arm": {Default: unset("none: the workflow asks for no A/B reviewer arm"), Doc: docABJobmax},

	"rigor.default": {
		Owner: "rigor",
		// A value here would be the fourth copy of the mode's floor: the
		// sentence names the constant the resolver reads instead (F12).
		Default: Default{Unset: "unset = " + string(rigor.Default) + " (rigor.Default)"},
		Doc:     "docs/rigor-modes/spec.md",
	},
	"rigor.areas": {Owner: "rigor", Merge: Union, Default: unset("no area rules"), Doc: "docs/rigor-modes/spec.md"},
	// The project layer's per-repository level (LD4, LF4): the key is the
	// layer's alone — a repository's fugaro.yaml may not state it at all, and
	// says the level it runs at with its own rigor.default — and the file it is
	// stated in is the layer, which `fugaro config publish` carries. That is why
	// the owner is not "rigor", the family that edits an area rule's own fields,
	// and why it is not the fallback `settings set`: the generic path's allowlist
	// is the keys a repository is edited into, and it refuses this one, which is
	// the F13 shape the four header keys above are.
	"rigor.repos": {Owner: "config", Doc: docRigor},

	// The security list is rigor.areas' twin in the two fields a Scopes row
	// cannot answer. Its merge is Union and not the default Replace (D1506):
	// the repository's rows are added to the project's and the engine's, never
	// put in their place, so a layer that writes the key can only ever say
	// more. And its owner is not `settings set`, though that is the row a key
	// with no entry here takes: D1515 decides the editor is fugaro.yaml and the
	// project layer file — a list has no scalar floor for the allow-list's rule
	// to raise — and `fugaro config publish` is the command that carries the
	// layer's half. The family that edits one entry by its key field is
	// `fugaro rigor security` (M4 of docs/config-ownership, D2613), which is
	// what the row names: an entry is addressed by its name and written whole,
	// which no scalar path can do, and the token is a command a person can run
	// — the read prints it and the write refuses in its name.
	"security.patterns": {Owner: "rigor security", Merge: Union, Default: unset("the engine's floor alone")},

	// The planning pass's switch (docs/contract-first-decoupling, D1813). It
	// is a preference rather than a bound, so the ordinary rule decides it:
	// the narrowest layer that sets it wins. The row takes the default owner
	// the yamlRow fallback gives it (D2603): the allowlist carries the
	// repository's spelling below and the layer's
	// `layer.defaults.planning.decoupling` beside it, so both halves are the
	// generic path's, where D1515's file tokens named a file the repository's
	// half is only one of two in.
	"planning.decoupling": {Default: value("true", "config.DefaultPlanningDecoupling")},
}

// buildRows is the table, assembled from the three documents' own sources: the
// `fugaro.yaml` rows from config.Scopes (so the two cannot drift), the project
// layer's from config.ProjectLayer, the local config's from localcfg.Config.
func buildRows() []Row {
	out := make([]Row, 0, len(config.Scopes)+len(layerRows)+len(localRows)+len(cloudRows)+len(config.RunStages)+len(governanceStages)*3)
	for _, s := range config.Scopes {
		out = append(out, yamlRow(s))
	}
	out = append(out, layerRows...)
	out = append(out, localRows...)
	out = append(out, cloudRows...)
	out = append(out, policyRows...)
	out = append(out, stageAccountRows()...)
	out = append(out, governanceLeafRows()...)
	for i := range out {
		out[i].Sensitive = sensitiveKeys[out[i].Key]
		if v, ok := defaultValues[out[i].Key]; ok {
			out[i].Default = v
		}
		if strings.HasPrefix(out[i].Key, "governance.") {
			out[i].Doc = docGovernance
		}
		// A block of settings is not the generic writer's row. The write refuses
		// the block's own spelling — a block is a shape, so a scalar written at
		// it has no value to land on (IsBlock, settingsRefuseRow) — and a row
		// naming `settings set` while the write refuses it is the pair D1178 and
		// F13 forbid: the read prints `writable: false` beside the command it
		// says writes the key. The truth about a block is the other way round —
		// `settings set` writes the settings the block holds, one at a time — so
		// the row names that instead (ownerBlockToken).
		if out[i].Owner == "settings set" && blockAmong(out, out[i].Key) {
			out[i].Owner = ownerBlockToken
		}
		// A layer's spelling of a retired key is retired with it: the layer's
		// `layer.defaults.agent.auth` is the same key config.Scopes marks
		// retired in a project layer, and the sentence a person reads is that
		// row's rather than a second copy (RetiredKeyNotes reports it at the
		// path `defaults.agent.auth`).
		if strings.HasPrefix(out[i].Key, "layer.defaults.") {
			if s, ok := config.ScopeOf(strings.TrimPrefix(out[i].Key, "layer.defaults.")); ok && s.Retired&config.InProject != 0 {
				out[i].RetiredWhy = s.RetiredWhy
			}
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
	r.RetiredWhy = s.RetiredWhy
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
	docSetup = "docs/gcp-setup.md"
	docLayer = "docs/project-layer.md"
	// docLocations is the record's own document (LP0 of
	// docs/location-preference/, D3087): what the block is, that it is one
	// machine's own, and that nothing reads it as an input (R5).
	docLocations = "docs/location-preference/record.md"
	// docGovernance is docLayer plus the one fact about a governance key the
	// table has no column for (F12): the guardrails are enforced by a build with
	// the `ee` tag, so an untagged build reads the block and holds no run to it
	// (internal/policy/hook.go, D522). The released runner image is built with
	// the tag, which is why the row says so rather than that the keys do
	// nothing.
	docGovernance = docLayer + " (enforced by the ee build)"
	docModels     = "docs/multi-model.md"
	docRigor      = "docs/rigor-modes/spec.md"
	docCrafts     = "docs/model-crafts/spec.md"
	// docRouting is the document that states the project's routing table
	// (docs/model-credentials-routing/): where a project's models are reached,
	// the five kinds of source a chain may name and the per-source admission
	// (spec.md, D3182). The folder's contract-table.md and reconciliation.md
	// are the contract's own documents, and the spec is where the block a
	// person writes is stated.
	docRouting = "docs/model-credentials-routing/spec.md"
	// docEscalation is the escalation ladder's own document. The two model
	// grades are stated in the plan rather than in the spec — the spec is the
	// ladder's design and the plan is where each key it adds is defined (E2,
	// D2039) — so the rows point at the plan, as docOrchestrator does for the
	// daemon's guardrails.
	docEscalation = "docs/outer-loop-escalation/implementation-plan.md"
	docImages     = "docs/project-image-catalog/spec.md"
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
	// docWorkspaceService is the document that states the keep-warm beat's
	// switch (R10, D2985). It is the folder's reconciliation and not its spec:
	// the spec is the owner's brief and the ruling the switch comes from is
	// R10, which only the reconciliation states — the plan writes it as WS13's
	// acceptance and no document of the folder has it as a spec sentence.
	docWorkspaceService = "docs/workspace-service/reconciliation.md"
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
	// docReviewAgainstTask is the document that states the check every review
	// stage makes of the task a pull request was written for, and the local key
	// the publish carries the switch from (R7 of docs/review-against-task/). It
	// is the work's plan, docTriage's reason: the key, its default and D2935's
	// two directions are stated there and nowhere else yet, and a document of
	// the check's own would take the row over.
	docReviewAgainstTask = "docs/review-against-task/implementation-plan.md"
	// docWorkspaceCredentials is the document that states the personal half of
	// a workspace's credentials and the member's own switch for it (R9, D2972).
	// It is the work's plan, docTriage's reason: the key, its home on the
	// published node and its off-when-absent default are stated there and
	// nowhere else yet (its contract C1 fixes the key's spelling and its home),
	// and a document of the personal half's own would take the row over.
	docWorkspaceCredentials = "docs/workspace-credentials/implementation-plan.md"
	// docABJobmax is the document that states the A/B reviewer arm's key (O12).
	// It is the work's plan rather than a spec, for docOrchestrator's reason:
	// the ask is named there and in the reconciliation that records the ruling,
	// and a document of the arm's own would take the row over.
	docABJobmax = "docs/review-levels-ab-jobmax/implementation-plan.md"
	// docBinaryAllow is the design of the binary allow rule (D981–D983,
	// binary-allow-rule plan task AR1): it states the two members — the globs a
	// run may add a binary at and the cap one is held to — and what a file that
	// states neither is read as, which is the whole of the DEFAULT column for
	// the two rows below. Unlike docTriage and the other plan documents, the
	// spec is where the rule itself lives, so the rows point there rather than
	// at the plan.
	docBinaryAllow = "docs/binary-allow-rule/spec.md"
	// docConfigOwnership is the config-ownership work's plan, which is where
	// the node-only blocks' local carriers are named (plan task N1): the
	// cascade's own key and its members, and the `node_removals` record with
	// the three blocks it names. The folder's spec lists the concept — the
	// cascade's `fallback` and the node's `model_grades` among the
	// semantic-command fields — and leaves the local key names to the plan, so
	// the rows point at the plan for docOrchestrator's reason: a document of
	// the carriers' own would take the rows over.
	docConfigOwnership = "docs/config-ownership/implementation-plan.md"
	// docAccounts is the account document, which states the repository-level
	// source beside the block and the profiles' lists it unions with.
	docAccounts = "docs/project-claude-accounts.md"
	// docMobileAuth is the document that states the declared allowlist: D231
	// of the mobile work's reconciliation decides where the declaration lives,
	// what the publish seeds from it and what it must never re-add, and the
	// keys of the block are named there and in the plan's P5 and nowhere else
	// yet. The spec is the owner's own text and states no key, so a document
	// of the block's own would be the one that takes these rows over.
	docMobileAuth = "docs/mobile-auth/reconciliation.md"
	// docCredentialsCascade is the document that states the credentials
	// cascade's controls (D152, D153, D142): the wait-or-switch threshold, the
	// backoff's two ends and the two owner-headroom knobs are named in the
	// credentials service's plan, and the spec of the service (its README's
	// sibling) is the design of the service rather than of the policy an
	// operator writes. K1's command and the carrier N1 adds are what state the
	// rows now.
	docCredentialsCascade = "docs/credentials-service/implementation-plan.md"
	// docOrchestratorGraph is the document that states the orchestrator's
	// switch and the project's issue adapter (D2701, D2733, D2739): both keys
	// are named in the work's plan, and the reconciliation's decision table is
	// where each spelling was settled — the switch after the review that moved
	// it out of the strictly decoded orchestrator block (F35) and the adapter
	// after the round that moved it off a localcfg block (F46). The plan is the
	// rows' document for docOrchestrator's reason: the keys are named there and
	// nowhere else yet, and a document of the daemon's own would take the rows
	// over the day it is written.
	docOrchestratorGraph = "docs/orchestrator-graph-estimates/implementation-plan.md"
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

// retiredLoc is loc for a key this release still reads and no longer honours:
// the local config's `routing.claude.via`, which the project's routing table
// replaces (D3173), is the one such key of the local file's.
func retiredLoc(key, def, owner, doc, why string) Row {
	r := loc(key, def, owner, doc)
	r.RetiredWhy = why
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
	layerRow("defaults.agent.models.vision", "none: refused where the coder cannot read an image and the task names one", "models", docVision),
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
	layerRow("defaults.agent.crafts.merge-judge.model", "the coder's model", "models", docCrafts),
	layerRow("defaults.agent.crafts.merge-judge.access", "none: only the local config and the policy node name a credential", "accounts", docCrafts),
	// The grades' layer rows (E2, D2039): the project layer sets both halves
	// for every repository of it, and a repository's own entry is recorded
	// rather than applied (internal/policy's tightenGrades). A grade nobody set
	// is not a model Fugaro chooses for the rung — it refuses and asks.
	layerRow("defaults.model_grades.strong.model", "none: a rung whose grade the project sets nowhere refuses and asks", "models", docEscalation),
	layerRow("defaults.model_grades.strong.access", "the project's default credential", "accounts", docEscalation),
	layerRow("defaults.model_grades.strongest.model", "none: a rung whose grade the project sets nowhere refuses and asks", "models", docEscalation),
	layerRow("defaults.model_grades.strongest.access", "the project's default credential", "accounts", docEscalation),
	layerRow("defaults.agent.review_rounds", "the run's rigor mode decides (rigor.DefaultRounds)", "settings set", docLayer),
	layerRow("defaults.agent.max_budget_usd", "25", "settings set", docLayer),
	layerRow("defaults.agent.first_line_review", "auto", "settings set", docLayer),
	layerRow("defaults.agent.first_line_rounds", "the run's rigor mode decides (rigor.DefaultRounds)", "settings set", docLayer),
	layerRow("defaults.agent.workspace_beat", "true", "settings set", docWorkspaceService),
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
	layerRow("defaults.rigor.default", "the built-in level (pro)", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.name", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.match", "none", "rigor", docRigor),
	layerRow("defaults.rigor.areas.*.level", "none", "rigor", docRigor),
	// What an area of the project layer is made of (LD4): each member is a
	// repository of the project, or a repository with the paths of it that are
	// in the area. `fugaro rigor set --area <glob> --name <name> --repo
	// <owner/name>` writes one member (RG3, D3237); the row's registered writer
	// stays the generic path, which reaches the same key by its own spelling.
	layerRow("defaults.rigor.areas.*.members.*.repo", "none", "settings set", docRigor),
	layerRow("defaults.rigor.areas.*.members.*.paths", "none", "settings set", docRigor),
	// The project layer's per-repository level, one entry per repository of the
	// project it names one by one (LD4), which `fugaro rigor set <mode> --repo
	// <owner/name>` writes (RG3, D3237). Its row under the fugaro.yaml spelling
	// is yamlMeta's, which names `config` as the owner because a repository's
	// own file may not set the key at all.
	layerRow("defaults.rigor.repos", "none", "settings set", docRigor),
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
	layerRow("ab.repos", "none: every repository runs at the block's own rate", "models", docModels),
	layerRow("ab.max_pairs_per_day", "the A/B design's own cap", "models", docModels),
	layerRow("ab.challengers", "none", "models", docModels),
	layerRow("ab.judge_model", "the A/B design's own judge", "models", docModels),
	layerRow("ab.exchange_rate", "the A/B design's own rate", "models", docModels),

	// The two schedules (R13). They are the installation's only keys in this
	// table whose loosening direction starts something: paused: false is what
	// makes a job run, so the key is the operator's own and a repository writes
	// no jobs: block at all. Owner `settings set` (R1 of docs/config-ownership):
	// `update` is not a writer, so a row naming it named a command that never
	// writes the key, and the allowlist carries the two spellings by name —
	// they are not §4.4's list, and a row no list carries is a key nobody can
	// write. The pair is a project-layer key, so the ceiling rule a layer-file
	// write meets and the coding agent's session's own refusal are what stand
	// in front of starting a job.
	layerValueRow("jobs.reports.paused", "true", "settings set", docPeriodicReviews),
	layerValueRow("jobs.catalog_check.paused", "true", "settings set", docPeriodicReviews),

	// The project layer's own half of the security list (D1501) and the switch
	// that is its alone (D1517, D1503): the rows the layer adds for every
	// repository of the project, and always_level_3, which asks for the
	// adversarial review level for every change of the project. Owner `rigor
	// security` for security.patterns' reason, since M4's command writes them
	// there too: the rows are judged by the rules the repository's own are,
	// which is why they share that row's answer, and always_level_3 is the one
	// that is no list — it is a switch that only turns on, and false is a layer
	// that states nothing.
	layerRow("security.patterns.*.name", "none", "rigor security", docLayer),
	layerRow("security.patterns.*.match", "none", "rigor security", docLayer),
	layerRow("security.patterns.*.category", "none", "rigor security", docLayer),
	layerValueRow("security.always_level_3", "false", "rigor security", docLayer),
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
	loc("rigor_pending", "none: the wizard's rigor answer has been written into the project layer", "setup", docSetup),
	loc("policy_generation", "none: no generation is recorded; the next update adopts the published one", "update", docSetup),
	loc("layer_generation", "none: no generation is recorded; the next update adopts the published one", "update", docSetup),
	loc("base_images", "the installation's own base image per kind", "setup", docSetup),
	// The shared images' retention (design shared-images.md §9): the project
	// layer's, beside base_images and published with it, and read by
	// `fugaro image prune`. A file that sets it nowhere gets the loader's own
	// answer through the accessor, so the row carries a value rather than an
	// "unset means" sentence (from defaultValues below).
	{Key: "images.retention_days", Layers: []string{LayerLocal, LayerShared}, Merge: Replace, Owner: "setup", Doc: docSetup},
	loc("image_source", "none: no base pin has been recorded", "setup", docSetup),
	loc("backend", "cloud-run", "setup", "docs/backends.md"),
	loc("log_view", "the project's default logs", "setup", docSetup),
	loc("scheduler_region", "the region the jobs run in", "setup", docSetup),
	// The locations record (LP0 of docs/location-preference/, D3087): where
	// this installation's located things already are, as the converge read
	// them from the cloud. One row per member, and each is local-only — the
	// record is never published, because the shared config decodes strictly
	// and a teammate's older CLI would refuse the key — and written by the
	// two converge doors from what the cloud holds rather than by any
	// editor: no flag states a location, because nothing that exists is
	// given a place by a rule — what already is somewhere is read from the
	// cloud (R5) — and an absent member means no converge has read it yet.
	locLocal("locations.firestore", "not known yet: no converge has read where the project's Firestore database is", "setup", docLocations),
	locLocal("locations.log_bucket", "not known yet: no converge has read where the installation's run log bucket is", "setup", docLocations),
	locLocal("locations.rtdb", "not known yet: no converge has read where the project's Realtime Database is", "setup", docLocations),
	locLocal("locations.secrets", "not known yet: no converge has read what the installation's own secret containers do", "setup", docLocations),
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
	// The Bitbucket tokens block (T2, R1): what each stored access token
	// reaches, as the operator's own local config states it. A record's name is
	// what the bundle key bitbucket-token--<name> is spelled from and what
	// every audit line carries; the value itself is in the service's bundle
	// and the identity and verified_at are in the creds state store, so no row
	// here holds either (R12) — and the owner column is `secrets`, the token
	// family's, because the block is stated in the machine's own config like
	// bitbucket_api_roots above it.
	loc("bitbucket_tokens.*.coverage", "required: the repository, workspace or workspace slug this token reaches", "secrets", docSetup),
	loc("bitbucket_tokens.*.expires", "none: no expiry date is recorded", "secrets", docSetup),
	loc("bitbucket_tokens.*.name", "required: the name the bundle key and every audit line are spelled from", "secrets", docSetup),
	loc("bitbucket_tokens.*.project_key", "none: kept for display, and never matched", "secrets", docSetup),
	loc("bitbucket_tokens.*.project_uuid", "none: a project token names its project by UUID or key", "secrets", docSetup),
	loc("bitbucket_tokens.*.type", "required: repo, project or workspace, and coverage is read as the same fact", "secrets", docSetup),

	loc("batch.image", "Fugaro's own batch image", "setup", "docs/backends.md"),
	loc("batch.machine_type", "the batch design's own machine", "setup", "docs/backends.md"),
	loc("batch.boot_image", "the batch design's own boot image", "setup", "docs/backends.md"),
	loc("batch.network", "the project's default network", "setup", "docs/backends.md"),
	loc("batch.subnetwork", "the project's default subnetwork", "setup", "docs/backends.md"),
	loc("batch.service_account", "the project's default service account", "setup", "docs/backends.md"),
	loc("batch.secrets", "none", "secrets", "docs/backends.md"),
	loc("batch.socket_gid", "the image's own docker group", "setup", "docs/backends.md"),
	loc("batch.task_timeout", "Fugaro's own task timeout", "setup", "docs/backends.md"),
	// The Batch installation's two identity facts (R2, R13, D3110, D3111). Their
	// owner is `update` and not `setup` and not a settings family, and that is
	// the rule rather than a column: R13 says `fugaro update` writes the key
	// fact from the compute root's own output and no settings command writes it,
	// because a key fact a settings command could write is a substituted public
	// key — and a substituted one hands its holder every seal key wrapped to it.
	// The opt-in fact is an OPERATOR's, written by opting in (R2) and by opting
	// out (R16), and never by a machine that merely selects the backend again.
	loc("batch.opt_in", "the installation has not opted in, so no compute root is applied", "update", "docs/backends.md"),
	loc("batch.seal_key.public_key", "none: without the key fact a sealed launch is refused", "update", "docs/backends.md"),
	loc("batch.seal_key.version", "none: without the key fact a sealed launch is refused", "update", "docs/backends.md"),

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
	loc("model_prices.*.cache_write_5m_mult", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_5m_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_1h", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_1h_mult", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_write_1h_per_m", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_read", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_read_mult", "the built-in price", "models", docModels),
	loc("model_prices.*.cache_read_per_m", "the built-in price", "models", docModels),
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

	// The routing table replaces `via` (D3173, RT2): the entry a person writes
	// in the table says which source reaches which model, and `via` is the key
	// the migration reads it out of. A provider's `models` stays as what the
	// provider can serve, and `routing.claude.models` beside it is not this
	// row's business.
	retiredLoc("routing.claude.via", "direct: the model is called at its own provider", "models", docModels,
		"the project's routing table replaces it: a chain names the sources a model is reached through, and"+
			" `fugaro update` writes the Claude entry from this value and offers its removal, see"+
			" docs/model-credentials-routing/spec.md"),
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
	// The owner is the `accounts` family, which D2096 gives the write to:
	// `fugaro accounts alias set ALIAS ACCOUNT` and `accounts alias rm ALIAS`
	// are the two doors, and their whole point is converging the node and the
	// job set on the repoint, so the generic path carries neither row. The bare
	// row is the block the file holds and the wildcard is one entry of it: a
	// reader that asks about `claude_account_aliases.<name>` is asking about the
	// key the same command writes.
	locLocal("claude_account_aliases", "none: no alias is defined, and every choice key names an account of claude_accounts directly", "accounts", docModels),
	locLocal("claude_account_aliases.*", "required: the one account the alias stands for, an account of claude_accounts or default", "accounts", docModels),
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
	// A pool's rule, its principal and its optional threshold (PL1, R5, R21,
	// D3178, D3181). They are local-only like the rest of the block, and the
	// principal is the enforcing half of who a pool's credentials are handed
	// to — the reason `owner` beside it is local-only as well (R7).
	//
	// The rule is the pool's own and is a NEW member: an absent one is served
	// by the pool's `strategy`, and by failover only where it names neither
	// (config.Pool.RuleOrDefault), so a pool an installation already holds
	// moves only where its owner writes a rule. `ordered` is a rule and never a
	// strategy, which is why it is not in the strategy row's own vocabulary.
	locLocal("pools.*.rule", "the pool's strategy, and failover where it names neither", "accounts", docCrafts),
	locLocal("pools.*.principal.kind", "required: whose pool it is, one of "+strings.Join(config.PrincipalKinds, ", "), "accounts", docCrafts),
	locLocal("pools.*.principal.id", "required: the person's Google subject, the service account's address, or the project's name", "accounts", docCrafts),
	locLocal("pools.*.threshold", "none: the pool does nothing beyond its rule, and no value is invented for it", "accounts", docCrafts),
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
	// The cascade's own marker, legacy_open's neighbour in every way that
	// matters here: `fugaro update` is its only writer, for the pool it
	// migrates out of a project whose cascade serves (D3064 as amended), so it
	// is in the registry for `fugaro settings` to name and is no key an
	// operator writes — a marker written by hand is refused where the config is
	// validated (config.ValidateCascadePools).
	locLocal("pools.*.cascade", "false: only `fugaro update` writes this, for the pool it migrates out of a cascade that serves", "accounts", docCrafts),
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
	//
	// It is retired (D3177, R5): which subscriptions a run may spread over is a
	// pool's own rule now, so the switch decides nothing and release 2 rejects
	// it. It is still honoured here — the typed gate above all — because this
	// release reads it.
	{
		Key:        "subscription_spread",
		Layers:     []string{LayerLocal},
		Merge:      Replace,
		Default:    unset("false: a run spreads across one subscription at a time"),
		Owner:      "settings set",
		Doc:        docCrafts,
		TypedWhy:   "this lets a run spread a project across two or more Claude OAuth subscriptions, which is the owner's own decision about the terms the subscriptions are held under",
		RetiredWhy: "the project's pools replace it: which subscriptions a run may spread over is the pool's own rule, and `fugaro update` offers its removal, see docs/model-credentials-routing/spec.md",
	},
	locLocal("crafts.coder.model", "required: the coder implements, so a run needs its model", "models", docCrafts),
	locLocal("crafts.coder.access", "required: the coder's calls go out on a credential or a pool", "accounts", docCrafts),
	locLocal("crafts.review.model", "the coder's model", "models", docCrafts),
	locLocal("crafts.review.access", "the coder's access", "accounts", docCrafts),
	locLocal("crafts.senior-review.model", "required: the senior review is the second opinion a run needs", "models", docCrafts),
	locLocal("crafts.senior-review.access", "required: the senior review's calls go out on a credential or a pool", "accounts", docCrafts),
	locLocal("crafts.security-review.model", "the senior review's model", "models", docCrafts),
	locLocal("crafts.security-review.access", "the senior review's access", "accounts", docCrafts),
	locLocal("crafts.merge-judge.model", "the coder's model", "models", docCrafts),
	locLocal("crafts.merge-judge.access", "the coder's access", "accounts", docCrafts),
	// The project's routing table (docs/model-credentials-routing/spec.md,
	// D3182, RT2): the ordered entries that say which credential reaches which
	// model, and `admit` per source — the repositories a source of the three
	// single-credential kinds may serve. It is local-only for the credentials'
	// reason one step out (F12): the table says which credential serves which
	// model, so a launcher who could write it into the shared file would aim
	// every teammate's calls at a credential the owner never admitted. It is
	// also not a key an operator writes by hand — `fugaro update`'s migration
	// writes a project's first table (MG2) and `fugaro routing set` edits one
	// after that (RT5) — which is why the rows name `update` until that command
	// exists and the rows move to its own token.
	//
	// The two spellings are the block's own shape: `model_routing.*` is one
	// entry, `model_routing.*.chain.*` one source of an entry's chain, and
	// `admit` is a list per source rather than per kind because one provider
	// key may be admitted to one repository and not another.
	locLocal("model_routing.*.match", "required: an entry that matches nothing answers no model", "update", docRouting),
	locLocal("model_routing.*.chain.*.kind", "required: a source names which credential serves the model, one of "+routingKinds(), "update", docRouting),
	locLocal("model_routing.*.chain.*.pool", "none: only a source of kind pool names a pool", "update", docRouting),
	locLocal("model_routing.*.chain.*.provider", "none: only a source of kind provider names a provider key", "update", docRouting),
	locLocal("model_routing.*.chain.*.model", "the model the call asked for: a source names a model only for a written downgrade", "update", docRouting),
	locLocal("model_routing.*.chain.*.admit", "nobody: an absent list and an empty one both admit no repository, and a pool's admission is its members' own allow_data_to", "update", docRouting),
	// The pause block (R11 of docs/model-credentials-routing, D3184, D3187,
	// D3190): the in-job floor, the shortest a pause may be and the longest one
	// chain may stay paused in total. It is one new top-level key of the policy
	// node — `pause`, beside `credentials` and never inside it, or a
	// release-behind runner would refuse the whole node (F49) — and the local
	// config's own block of the same name, which is what `fugaro settings set`
	// writes and what the publish copies to the node. The credentials service
	// is the reader that decides between waiting, pausing and halting by them
	// (PR3); nothing else acts on one.
	//
	// The rows are local-only, the cascade's carriers' reason one step out: a
	// launcher who could write them into the runs bucket's shared file would be
	// deciding how long every teammate's run holds a job open before it
	// suspends, and how long their chains may sit paused. Each default sentence
	// names the publisher's own constant rather than restating the figure:
	// projectpolicy holds the three defaults once, its decode reads a member
	// written beside them, and a second answer here is how the two drift.
	locLocal("pause.wait_in_job", "5m: a reset nearer than this is waited out inside the job and a farther one suspends the run (projectpolicy.DefaultWaitInJob, D3184)", "settings set", docRouting),
	locLocal("pause.min_pause", "30m: a pause is never shorter than this (projectpolicy.DefaultMinPause, D3190 as amended)", "settings set", docRouting),
	locLocal("pause.max_pause", "6h: the longest one chain may stay paused in total, and a reset beyond it halts the run at once (projectpolicy.DefaultMaxPause, D3187)", "settings set", docRouting),

	loc("project_secrets.*.allow_data_to", "nobody", "secrets", docSetup),
	loc("project_secrets.*.proxy", "none: an entry that says nothing about the switch is the state `fugaro update` turns on, and a written false is a hold it never lifts", "secrets", docSetup),

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

	// The identity list is the owner's own (CSR14, verifier.tf). Owner
	// `settings set` (R1 of docs/config-ownership, D2603): a list in the
	// machine's own config is what the generic path writes, and the `secrets`
	// family next door writes secret expiry and rotation and no key of this
	// list — so naming it was a row saying a command writes a key it does not.
	// The TypedWhy is the widening direction: an entry is an identity that may
	// ask the credentials service to verify a signature for this installation's
	// runs, which is the owner's own admission rather than an edit of a
	// threshold.
	{
		Key:      "verifier_accounts",
		Layers:   []string{LayerLocal, LayerShared},
		Merge:    Replace,
		Default:  unset("nobody may ask the credentials service to verify a signature, except on an installation whose credentials service is deployed, where the installation's own verifier account may"),
		Owner:    "settings set",
		Doc:      docSetup,
		TypedWhy: "this admits an identity that may ask the credentials service to verify a signature for the installation's runs",
	},

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
	// The repository-to-image map is the map above in the form L11 gives it
	// (D3370, D3371): the same entry per repository, keyed by
	// provider/owner/name, so one name at two providers is two entries rather
	// than one. It sits beside the old map's row while both fields exist (R15,
	// D3372): whichever member the file holds, the key is one the read answers
	// and the write refuses against, and `fugaro image map set` is the command
	// that writes it — the owner names that family for the reason the row above
	// states.
	loc("repo_images", "none: no repository is mapped, and the catalog's default entry decides the image", "image map", docImages),
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
	// node is published from. Owner `settings set` (R1 of docs/config-ownership,
	// D2603): a boolean in the machine's own config is the generic path's, and
	// `project` named a family none of whose subcommands edits the key — the
	// publish carries whatever the file holds rather than writing it. The switch
	// is the owner's because it decides which of a review round's findings hold
	// a pull request back (D1854), and the publish is what carries the value to
	// the node every run of the project reads it from.
	loc("triage", "true: a review round's findings are judged by the triage stage in the modes that triage at all, and a written false turns the stage off for the project", "settings set", docTriage),
	// The review-against-task switch is that policy key's shape one over (R7 of
	// docs/review-against-task/, D2935): a local key the node is published from,
	// whose owner is `settings set` for the triage row's reason — a boolean in
	// the machine's own config is the generic path's. It is the owner's answer
	// for the whole project, and the publish is what carries it to the node
	// every run of the project reads it from. The absent key reads as on rather
	// than as "none", because a reader would take "none" for the check itself
	// being off; a written false is the project-wide default a repository's own
	// `true` overrides, and a written true is the check no repository may walk
	// out of.
	loc("review_against_task", "on: every review stage is handed the task its pull request was written for, and a written false is the project-wide default a repository's own true turns back on", "settings set", docReviewAgainstTask),
	// The member's own switch for the personal half of a workspace's
	// credentials (R9 of docs/workspace-credentials/, WC6, D2972) is the triage
	// switch's shape once more: a local key the node is published from, whose
	// owner is `settings set` for the triage row's reason — a boolean in the
	// machine's own config is the generic path's. It is the member's admin's
	// answer for the whole project, and the member's `fugaro update` is what
	// carries it onto the node, where the workspace's service — the only
	// enforcer — reads it. The absent key reads as OFF rather than as "none",
	// because a reader would take "none" for a value the key does not have: off
	// is what every member is before its admin sets it, which is D2972's own
	// "off by default", so the sentence states it.
	loc("personal_workspace_tokens", "off: the workspace's service serves this project's people no personal token and its runs are offered the shared entries as before, and a written true turns the personal half on", "settings set", docWorkspaceCredentials),
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

	// The binaries block is the owner's grant of the paths a run may add a
	// binary at and of the cap a run is held to (D981–D983; binary-allow-rule,
	// plan task AR1), and one row per member for the catalog's reason: a
	// settings read names the leaf a value came from, and the two members are
	// what the rule's two inputs are. Both rows are local-only, like the clone
	// URLs above and for a reason one step out: the block is read by the
	// project's runs as the rule they are held to, so a launcher who could
	// write it into the runs bucket's shared file would be admitting its own
	// payload to every teammate's runs (D986; Shared nils it besides, and
	// sharedForbidden refuses the key).
	//
	// The owner is `fugaro project binaries` (K2 of docs/config-ownership): it
	// writes both members into this file, and the update it ends on publishes
	// the block onto the node — the run reads it there and nowhere else. It is
	// a token of its own rather than the `project` family's because that family
	// writes the project's other policy keys and neither member of this block:
	// an entry is a doublestar path glob held to the node's own rule
	// (projectpolicy.ValidBinariesAllow) and the cap to its ceiling, which are
	// checks no generic write can make, and a refusal for one of the two binds
	// names the command that works rather than `fugaro project`, which does not
	// write it.
	locLocal("binaries.allow", "none: no path is admitted, so a run that adds a binary anywhere fails finalize and the person reads the reason there", "project binaries", docBinaryAllow),
	locLocal("binaries.max_bytes", "2097152 (2 MiB): the largest a single added file may be, and a project may lower it", "project binaries", docBinaryAllow),

	// The reports_commit block is the owner's designation of the one repository
	// a report run may commit into and of the folder inside it (D2750, D2751;
	// periodic-reviews plan task R14), and one row per member for the catalog's
	// reason: a settings read names the leaf a value came from, and the two
	// members are what the designation is. Both rows are local-only for the
	// binaries block's reason and one more of their own: the block is carried
	// onto the policy node, which is what a report run reads it from, so a
	// launcher who could write it into the runs bucket's shared file would be
	// choosing the repository every teammate's reports land in — a place their
	// own runs would then write to and read from — and the file is where a
	// prompt-injected checkout writes besides (Shared nils it, and
	// sharedForbidden refuses the key).
	//
	// The owner is the project family because `fugaro project policy publish`
	// — the publish `fugaro update` runs, both of them the single
	// buildProjectPolicy — is the door that carries the block onto the node,
	// and because no generic write reaches these leaves: a repository is held
	// to task.CanonicalRepo's form and the folder to
	// projectpolicy.ValidReportsCommitDir's, which are checks no scalar write
	// can make. The default of `dir` is the node's own
	// (projectpolicy.DefaultReportsCommitDir), because a block that designates
	// a repository and names no folder commits into a folder run-digest never
	// writes (owner ruling 2026-11-18).
	locLocal("reports_commit.repo", "none: the project commits no report anywhere, and a written empty block is how the owner takes a designation away", "project", docPeriodicReviews),
	locLocal("reports_commit.dir", "docs/fugaro-reviews-mirror: a folder the report layout never writes, so a designated repository's own reports and the mirror cannot meet", "project", docPeriodicReviews),

	// The node-only blocks' local carriers (config-ownership plan task N1,
	// D2607, F22, F23): the cascade, and the record that states an explicit
	// unset. One row per member for the catalog's reason — a settings read
	// names the leaf a value came from, and each member is one input of the
	// cascade's own rule. Both blocks are local-only for the binaries block's
	// reason one step out: each is carried onto the policy node, which is what
	// a run reads, so a launcher who could write a carrier into the runs
	// bucket's shared file would be stating the rule its own teammates' runs
	// are held to — and the record is the destructive half of that, since a
	// removal deletes a pool, a credential or a craft of the project's node
	// (localcfg's Shared nils both, and sharedForbidden refuses the keys
	// besides).
	//
	// The owner is the accounts family: the local config's `credentials` key is
	// the list of typed credentials and the cascade takes a name of its own
	// (F23), which is why the leaves are `credential_cascade.*` rather than
	// `credentials.*` — the node's own member is the cascade, and one key may
	// not mean two things in two documents (D2607, D2613). No generic write
	// reaches any of the nine: `fallback` is a word with two answers whose
	// second one spends the project's money, and a removal is a name held to
	// the rule of the block it names.
	//
	// The defaults are the node's own (projectpolicy's), because a file that
	// states nothing keeps what the node holds and the node is what a run reads
	// (D2607's silence rule): the figures below are what a cascade with no
	// member written already means there, so a reader of this table is not told
	// that a key means nothing when it means 15m.
	locLocal("credential_cascade.fallback", "oauth-only: a run waits for the shared token's reset and never falls over to the paid tiers, and the written auto is the value that does — the node refuses it unless the budget block enforces a per-run ceiling above zero (D152)", "accounts", docConfigOwnership),
	locLocal("credential_cascade.wait_threshold", "15m: how long a run waits for an exhausted credential's reset before it fails over to the next tier (projectpolicy.DefaultWaitThreshold); under oauth-only there is no tier to fail over to, so the run waits for the earliest reset whatever the threshold says", "accounts", docConfigOwnership),
	locLocal("credential_cascade.backoff.initial", "15m: the first wait a credential is held for when its exhaustion reported no reset time, doubling from there (projectpolicy.DefaultBackoffInitial)", "accounts", docConfigOwnership),
	locLocal("credential_cascade.backoff.cap", "5h: the longest that wait doubles to (projectpolicy.DefaultBackoffCap); a cap below the initial wait is refused, because a probe schedule that never doubles retries a credential with no reset time for ever (D153)", "accounts", docConfigOwnership),
	locLocal("credential_cascade.stop_after_exhaustion_per_window", "true: a run stops rather than spending the project's money on a credential that has exhausted its window, and only the written false opts out (D142)", "accounts", docConfigOwnership),
	locLocal("credential_cascade.concurrency_per_token", "1: one run at a time may hold a shared credential, and the written 0 is no cap at all (projectpolicy.DefaultConcurrencyPerToken, D142)", "accounts", docConfigOwnership),
	locLocal("node_removals.pools", "none: silence in this file keeps the node's pools, so a pool is removed by naming it here (D2607, F22)", "accounts", docConfigOwnership),
	locLocal("node_removals.crafts", "none: silence in this file keeps the node's crafts, so a craft is removed by naming it here (D2607, F22)", "accounts", docConfigOwnership),
	locLocal("node_removals.craft_credentials", "none: silence in this file keeps the node's credentials, so a credential is removed by naming it here (D2607, F22); the name stays recorded until the node no longer holds it", "accounts", docConfigOwnership),

	// The orchestrator daemon's own guardrails (D1239, D1234) are the owner's
	// policy like the catalog above, and one row per member for the catalog's
	// reason. They carry Tighten rather than the ordinary Replace, which is the
	// block's kind: a ceiling, whose counts are caps and whose class models move
	// up the tier order or not at all. The default of each count is the figure
	// policy.WithDefaults fills the block with, named by the constant that holds
	// it — the effective cap of the running count is the person's own
	// max_parallel, which is why its sentence names that key rather than a
	// number.
	//
	// Owner `settings set` (R1 of docs/config-ownership, D2603): the block is a
	// set of scalars in the machine's own config, which is exactly what the
	// generic path writes, and the allowlist carries the six spellings by name
	// so that the row and the write agree. G12 named `project policy`, the token
	// whose words are `fugaro project policy publish` carrying the local block
	// to the node, which left every one of the six refused by the write while
	// its row named a writer — the pair D1178 forbids — and sent a reader to a
	// bare command for keys no subcommand of it edits. Not the merge, though:
	// the machine's own block REPLACES the node's when it states any key of it
	// (localcfg.OrchestratorEffective), and what holds the block is its two
	// writers — the publish door refusing a loosened node (policyLoosening)
	// and `settings set` typing a raise (settingsCeilingKey).
	locTighten("orchestrator.max_running", "the person's max_parallel", "settings set", docOrchestrator),
	locTighten("orchestrator.batch_size", "10 (policy.DefaultBatchSize)", "settings set", docOrchestrator),
	locTighten("orchestrator.spawn_per_minute", "2 (policy.DefaultSpawnPerMinute)", "settings set", docOrchestrator),
	locTighten("orchestrator.spawn_per_hour", "12 (policy.DefaultSpawnPerHour)", "settings set", docOrchestrator),
	locTighten("orchestrator.spawn_per_day", "60 (policy.DefaultSpawnPerDay)", "settings set", docOrchestrator),
	// The one key of the block that is not a count: a class the map does not
	// carry is at its floor, which is the bottom of the order for a routine
	// decision and the top for a security-sensitive one (policy.ValidOrchestrator
	// refuses a name outside the order and a model under the class's floor, so
	// the classes' two ends are the two answers an absent key can take).
	locTighten("orchestrator.decision_model_per_class", "each class's floor: haiku for a routine decision and opus for a sensitive one", "settings set", docOrchestrator),

	// The declared mobile allowlist's three members (docs/mobile-auth, D231).
	// The block is stated in the machine's own config, exactly as the
	// orchestrator's is and for a stronger reason: the role an entry carries is
	// an authority over every phone admitted to the project's runs, and every
	// other store of it is one a run or a teammate can write (localcfg's
	// MobileAllow comment says which).
	//
	// Owner `settings set` (R1 of docs/config-ownership, D2603): the block's
	// entries are objects and the generic path writes an object list entry with
	// --add-entry (D1162), so the command that can write one is this path's and
	// not `fugaro project policy publish`'s, which carries the local declaration
	// to the device node the rules read — a carrier and not an editor, exactly as
	// it is for the orchestrator block above. The rows are marked widening
	// (TypedWhy, D1165): an entry admits a phone to the project's runs, so
	// growing the list is the edit the fence asks the owner's own hand for, and
	// the coding agent's session is refused it whatever the gate says. The
	// spelling carries a `*`, which is the shape of a map entry rather than a key
	// a write lands on, so the literal refusals answer these rows today and W3's
	// allowlist is what reaches their concrete spellings. The default is the
	// block's own emptiness — an absent mobile_allow: declares nothing, so
	// nothing is seeded and nobody is admitted by the file.
	{
		Key:      "mobile_allow.*.uid",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Default:  unset("no uid: an entry that names one seeds no device node, and the node is what admits a phone (D231)"),
		Owner:    "settings set",
		Doc:      docMobileAuth,
		TypedWhy: "this admits a phone to the project's runs: an entry that names a uid seeds the device node the rules read, so growing the list widens who may drive them",
	},
	{
		Key:      "mobile_allow.*.address",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Default:  unset("required: the administered email or the domain the entry admits"),
		Owner:    "settings set",
		Doc:      docMobileAuth,
		TypedWhy: "this admits a phone to the project's runs: an entry whose address matches a device admits it, so growing the list widens who may drive them",
	},
	{
		Key:      "mobile_allow.*.role",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Default:  unset("required: an entry with no role is refused rather than given one"),
		Owner:    "settings set",
		Doc:      docMobileAuth,
		TypedWhy: "the role an entry carries is an authority over every phone admitted to the project's runs, so writing it is the widening this fence asks the owner's own hand for",
	},

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
	// The repository's own account list, beside the keys of the entry it sits
	// in but not of their kind: it is the half of an account's admission that
	// keeps a repository whose runs are not on a profile job served
	// (docs/project-claude-accounts.md §3), so it is local-only, and it is
	// stripped from the published copy of this map besides
	// (localcfg.withoutRepoLocalKeys, internal/cli/sharedcfg.go's refusal on
	// the key). The owner is the accounts family: `fugaro accounts repo add`
	// adds to the list and `accounts rm` clears a name with the declaration it
	// names, and neither is a `settings set` path — which is what the read's
	// owner column may say rather than a bare command.
	locLocal("repos.*.claude_accounts", "nobody: the repository's runs are served by the accounts the profiles of its jobs name", "accounts", docAccounts),
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
	// The two facts the same record carries beside the switch (R12, KS19):
	// whether the bundle holds an entry for every provider credential this
	// repository's config names — the fact a routed call is sent through the
	// service for — and whether a stage account of this repository is one the
	// service serves. Both are machine-written like the switch, both are read
	// back by the plan a repository's root is built with (infra's
	// providerKeysServed/stageAccountServed) and both are written off as
	// well as on, so a row here for the same reason the switch has one.
	loc("repos.*.provider_keys_served", "false: no update has found this repository's bundle holding an entry for every provider credential its config names, or the deployed service is not at the release that serves one, or the project publishes no allow-list", "project", docSetup),
	loc("repos.*.stage_account_served", "false: no update has found a stage account this repository mixes with a provider key, or the deployed service is not at the release that serves one", "project", docSetup),

	// The workspace keys (docs/workspaces/spec.md §2, §5, §6), all seventeen of
	// them local-only: the pin is the member's own trust and the member table
	// and shared list are the workspace owner's own declaration, and a launcher
	// who could write any of them into the shared file could aim this project's
	// exchange, this project's database grant and this project's calls, or
	// choose what the workspace's credentials service offers
	// (internal/cli/sharedcfg.go's sharedForbidden). Their owner is the `fugaro
	// workspace` family, so the generic writer refuses them (D853) — a member
	// entry, a pin and a shared entry are written whole by the commands that
	// know what the values have to agree about.
	locLocal("workspace", "this project is a member of no workspace", "workspace", docWorkspace),
	locLocal("workspace_pin.firebase_project", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.issuer", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.assertion_key", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.service_account", "no workspace is pinned", "workspace", docWorkspace),
	locLocal("workspace_pin.service_address", "this project takes no workspace credential", "workspace", docWorkspace),
	locLocal("workspace_members.*.repos", "the member contributes no repository", "workspace", docWorkspace),
	locLocal("workspace_members.*.job_service_accounts.*.account", "no job service account is declared for the member", "workspace", docWorkspace),
	locLocal("workspace_members.*.job_service_accounts.*.repos", "the account is a job of no repository", "workspace", docWorkspace),
	locLocal("workspace_members.*.database_region", "the member's database lives in its project's default region", "workspace", docWorkspace),
	locLocal("workspace_members.*.firebase_project", "none", "workspace", docWorkspace),
	locLocal("workspace_members.*.fp", "none", "workspace", docWorkspace),
	locLocal("workspace_members.*.exchange_url", "none", "workspace", docWorkspace),
	locLocal("workspace_cap.monthly", "the workspace states no cap", "workspace", docWorkspace),
	locLocal("workspace_shared.*.kind", "the workspace offers no credential", "workspace", docWorkspace),
	locLocal("workspace_shared.*.all_members", "false: the credential is offered to the members `projects` names, and only the written true offers it to every member of the workspace", "workspace", docWorkspace),
	locLocal("workspace_shared.*.projects", "the credential is offered to no member", "workspace", docWorkspace),
	locLocal("workspace_shared.*.covers", "the credential covers no repository", "workspace", docWorkspace),

	// The terminal Issues section's nudge thresholds (T-IS15). They are
	// local-only rather than loc's two layers: the section is printed on the
	// machine running the digest, the committed report carries no part of it,
	// and no other machine acts on this one's thresholds — so the shared file
	// never holds the block (Shared drops it) and a row advertising the shared
	// layer would name a store the key cannot have. Their defaults are values
	// the loader leaves in place, so they come from defaultValues below, as
	// update_check's does, rather than from an "unset means" sentence.
	//
	// Owner `settings set` (R1 of docs/config-ownership, D2603): the machine's
	// own config is the key's only store and this CLI's generic path is its one
	// door, which is what puts the owner's own hand in front of a threshold
	// nothing else can write. G12 named the file instead (`config local`) while
	// the allowlist did not carry the two spellings, and a row that names a
	// file as an owner while a command writes it is the pair D1178 refuses.
	{Key: "issues.nudge_after_days", Layers: []string{LayerLocal}, Merge: Replace, Owner: "settings set", Doc: docTroubleshoot},
	{Key: "issues.nudge_recurrence", Layers: []string{LayerLocal}, Merge: Replace, Owner: "settings set", Doc: docTroubleshoot},

	// The outer loop's ladder (E3, D2032). Every row here is local-only and
	// every one is `settings set`'s: a threshold, a grade the ladder's rungs
	// run at and the switch that decides whether the daemon acts at all are the
	// machine owner's own, and the one door they are written through is this
	// CLI's generic path — which is what makes the agent-session fence and the
	// typed form reach them at all. A row naming the file instead would be
	// refused by the write and print "not by the generic path" beside a key the
	// generic path does write.
	//
	// The four scalars are what every mode reads, and the `modes.*` rows are
	// the override one mode reads instead; both take their default from the
	// shipped table (escalation.DefaultThresholds, E1), so the sentence names
	// that table rather than a figure this table would then have to keep in
	// step with the code. critical_path_factor and ready_drafts are the two
	// with a value of their own, and they come from defaultValues below.
	//
	// ready_drafts is the one row here whose widening direction is its truth:
	// the daemon readying a stalled draft is new authority over work a person
	// left alone (D2041, D2046), so the row carries TypedWhy and the write of
	// `true` is the typed form.
	locLocal("escalation.r2_after_hours", "the shipped table's own, one row per mode (escalation.DefaultThresholds)", "settings set", docEscalation),
	locLocal("escalation.r2_after_attempts", "the shipped table's own, one row per mode (escalation.DefaultThresholds)", "settings set", docEscalation),
	locLocal("escalation.r3_after_hours", "the shipped table's own, one row per mode (escalation.DefaultThresholds)", "settings set", docEscalation),
	locLocal("escalation.r2_coder", "the shipped table's own, one row per mode (escalation.DefaultThresholds)", "settings set", docEscalation),
	locLocal("escalation.cost_ceiling", "none: a file that states no ceiling gets no cost trigger", "settings set", docEscalation),
	// A literal rather than a locLocal call, as max_parallel is: its default is
	// a value the loader leaves in place, so it comes from defaultValues below
	// rather than from an "unset means" sentence.
	{Key: "escalation.critical_path_factor", Layers: []string{LayerLocal}, Merge: Replace, Owner: "settings set", Doc: docEscalation},
	locLocal("escalation.merge_judge", "none: a judge nobody named is a question for the owner (escalation.Grades)", "settings set", docEscalation),
	locLocal("escalation.modes.*.r2_after_hours", "the block's own, or the shipped table's when it states none", "settings set", docEscalation),
	locLocal("escalation.modes.*.r2_after_attempts", "the block's own, or the shipped table's when it states none", "settings set", docEscalation),
	locLocal("escalation.modes.*.r3_after_hours", "the block's own, or the shipped table's when it states none", "settings set", docEscalation),
	locLocal("escalation.modes.*.r2_coder", "the block's own, or the shipped table's when it states none", "settings set", docEscalation),
	{
		Key:      "escalation.ready_drafts",
		Layers:   []string{LayerLocal},
		Merge:    Replace,
		Owner:    "settings set",
		Doc:      docEscalation,
		TypedWhy: "this lets the orchestrator ready a stalled draft on its own: it then acts on work a person left alone, which is the owner's own decision",
	},
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

// policyRows are the published policy node's own keys (spec §2.1, §2.2): the
// credentials cascade's controls, which today live in the Realtime Database
// node and nowhere else (docs/config-ownership/audit.md's own row for them;
// projectpolicy.Credentials is the shape the node decodes into). They are the
// first rows of the `policy` layer, which the vocabulary has carried since the
// settings design and no row claimed: a node member an operator writes is
// neither a file of this machine nor the budget database's, and a row claiming
// one of the three file layers would send the read and the write at a file the
// key is not in.
//
// The spelling is `credentials.<leaf>`, which is §4.4's own name for the block,
// and the owner is the `accounts` family's, so the generic path refuses every
// one of the six in that family's name rather than writing an operator's node
// policy as a local setting (D2603, R1 of docs/config-ownership). `credentials.`
// left the generic path's prefixes with K1, which registers `fugaro accounts
// cascade set` as their writer, so a refusal now names that command.
//
// The six stay testdata/writer_gaps.golden's, and the carrier's six beside
// them. A registered writer is measured where the row spells the key, and these
// six are the node's own spelling while the write lands in this machine's
// carrier (`credential_cascade.<leaf>`, writers_cascade.go) — and the carrier's
// six owe the publish the write ends on (D2615), which a hermetic installation
// cannot complete, so the command exits non-zero after the bytes land. That is
// the gap every semantic family's rows show for the same reason (pools',
// providers' and the crafts' among them): the golden lists them until I1's
// strict ratchet has a rig that can run a stage.
//
// Every default is a sentence naming the constant the node's own reader falls
// back to (projectpolicy.FallbackValue, WaitThresholdDuration,
// BackoffInitialDuration, BackoffCapDuration, StopsAfterExhaustion,
// ConcurrencyCap), because an absent member is the protective answer rather
// than a value the node holds — and the two switches among them are written to
// be turned off, which is why replacing this block's policy is the widening
// its own family's write fences (D142).
var policyRows = []Row{
	// The first two are retired (R4 of
	// docs/model-credentials-routing/reconciliation.md): a chain's own order
	// replaces the fixed tier order, so `fugaro update` writes a key into the
	// chain where `fallback: auto` was set and leaves it out for `oauth-only`,
	// and a run moves to the next source at once rather than waiting for a
	// reset. Release 2 rejects both; this release still reads them, which is
	// why the rows keep their owner and their default.
	{Key: "credentials.fallback", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("oauth-only: a run does not fail over to the paid tier (projectpolicy.FallbackOAuthOnly), and the one value that spends the project's money is written by an operator"), Owner: "accounts", Doc: docCredentialsCascade,
		RetiredWhy: "the chain's own order replaces it: `fugaro update` puts the key in the chain where `fallback: auto` was set and leaves it out for `oauth-only`, then offers its removal, see docs/model-credentials-routing/spec.md"},
	{Key: "credentials.wait_threshold", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("15m: how long a run waits for an exhausted credential's reset before failing over (projectpolicy.DefaultWaitThreshold)"), Owner: "accounts", Doc: docCredentialsCascade,
		RetiredWhy: "the chain's own order replaces it: a run moves to the next source at once rather than waiting for a reset, and `fugaro update` offers its removal, see docs/model-credentials-routing/spec.md"},
	{Key: "credentials.backoff.initial", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("15m: the first wait of a credential backed off with no reset time (projectpolicy.DefaultBackoffInitial)"), Owner: "accounts", Doc: docCredentialsCascade},
	{Key: "credentials.backoff.cap", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("5h: the longest wait the doubling reaches (projectpolicy.DefaultBackoffCap)"), Owner: "accounts", Doc: docCredentialsCascade},
	{Key: "credentials.stop_after_exhaustion_per_window", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("true: a credential exhausted within its window stops the runs that need it (projectpolicy.Credentials.StopsAfterExhaustion)"), Owner: "accounts", Doc: docCredentialsCascade},
	{Key: "credentials.concurrency_per_token", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("1: how many runs may hold one shared credential at a time (projectpolicy.DefaultConcurrencyPerToken), where a written 0 is no cap at all"), Owner: "accounts", Doc: docCredentialsCascade},

	// The daemon's own two (R1 of docs/orchestrator-graph-estimates, D2739,
	// D2733): the switch that hands orchestration back to the person and their
	// agent, and the project's issue adapter. Both are top-level keys of the
	// published node and of nothing else, so `policy` is the whole of their
	// Layers for the cascade's reason — and for one of their own: the node is
	// what every machine of the project reads, which is the whole point of the
	// switch (one write reaches them all, D2701), where a file would reach the
	// machine it sits on.
	//
	// Both name the `project` family, whose command is the node's door
	// (`fugaro project policy publish`), because that is what makes the generic
	// path refuse them in that family's name rather than write them somewhere
	// they do not live: handing orchestration back and naming the repositories
	// whose issues feed the backlog are not scalars `settings set` may land.
	// What the family does *not* have is a carrier for either: no key of the
	// local config states the switch or the adapter (D2701, D2733), so a person
	// writes the node's own member and `publish` carries it from the node
	// forward — which is why the two are testdata/writer_gaps.golden's, as the
	// cascade's own twelve are.
	//
	// The switch merges by Tighten, and it is the rule rather than a figure
	// here: false is its stricter value and the only one that can undo the
	// daemon's work, so a layer that writes it off decides whatever another
	// layer writes and no layer turns it back on over one (D2701, D2739). The
	// adapter replaces, because which repositories are synced and whether an
	// item opens an issue is one answer rather than the stricter of two.
	{Key: "orchestrator_enabled", Layers: []string{LayerPolicy}, Merge: Tighten, Default: unset("true: the project's daemon launches runs, and a written false hands orchestration back to the person and their agent"), Owner: "project", Doc: docOrchestratorGraph},
	{Key: "issue_sync", Layers: []string{LayerPolicy}, Merge: Replace, Default: unset("none: no repository's issues are ingested into the project's backlog, and no item opens an issue of its own"), Owner: "project", Doc: docOrchestratorGraph},
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

// governanceStages are the stages the governance block bounds, in the block's
// own order: the ones a run's stages carry (config.StageTimeout's fields, the
// three names config's validator lists). An artifact run's plan and render
// steps have no governance — no budget share, no token ceiling and no timeout
// of their own — so the vocabulary is these three and not config.RunStages.
var governanceStages = []string{"implement", "review", "fix"}

// governanceTierKeys are the keys every tier of governance.tiers carries
// (policy's own three, T8.1). A tier's own key is one of them, so the twelve
// names below are built from the tier's name rather than declared.
var governanceTierKeys = []string{policy.KeyExpectedUSD, policy.KeyExpectedMinutes, policy.KeyMaxSubagents}

// governanceLeafRows are the concrete rows of the governance block at the two
// layers a project and a repository set it in (composite D2604, W2).
//
// The block's own rows (yamlMeta) are the read's: `governance.stage_timeout` is
// a mapping of keys, so its own spelling is no setting and the write refuses it
// (IsBlock, settingsRefuseRow). What a person writes is a stage's own bound,
// and before this the only spelling of it was the project layer's
// `layer.governance.stage_timeout.implement` — a concrete unprefixed
// `governance.stage_timeout.implement` found no row at all and `settings set`
// answered "no setting has that name" for the owner's 99% case (F1).
//
// The rows are a literal table rather than a walk of the config structs,
// because a row's fields are decisions and not the struct's: the layers are
// project and repo (D2604 — the project layer is the one layer that may
// loosen the guardrail, and a repository may only tighten it), the merge is
// Tighten, and the DEFAULT column repeats the block's own sentence so that the
// long and the short spelling of one stage's bound read alike. The ratchet that
// keeps the table complete is TestRegistryCarriesEveryGovernanceLeaf
// (registry_stage_test.go), which walks config.Governance by reflection: a
// stage, a tier or a key added to the block and left out here is that test's
// failure.
func governanceLeafRows() []Row {
	out := make([]Row, 0, len(governanceStages)*3+len(tier.Tiers)*len(governanceTierKeys))
	add := func(key, def string) {
		out = append(out, Row{
			Key:     key,
			Layers:  []string{LayerProject, LayerRepo},
			Merge:   Tighten,
			Default: unset(def),
			Owner:   "settings set",
			Doc:     docLayer,
		})
	}
	for _, stage := range governanceStages {
		add("governance.stage_budget."+stage, "Fugaro's own stage budgets")
		add("governance.stage_tokens."+stage, "Fugaro's own stage token caps")
		add("governance.stage_timeout."+stage, "Fugaro's own stage timeouts")
	}
	for _, t := range tier.Tiers {
		for _, key := range governanceTierKeys {
			add("governance.tiers."+string(t)+"."+key, "Fugaro's own tiers")
		}
	}
	return out
}

// ownerBlockToken is the owner column's entry for a block of settings: the key
// whose own spelling holds other rows below it and no value of its own
// (IsBlock). No command writes such a key, and the command that writes the
// settings under it is `settings set` — which is what the token's sentence in
// the read says (settingsOwnerCommands). It is a token of its own rather than
// the default one because the write refuses the block's spelling
// (settingsRefuseRow): a row naming the generic writer while the write refuses
// it is the pair D1178 and F13 forbid, and the read's own blanking rule is only
// the display half of it.
const ownerBlockToken = "settings block"

// blockAmong reports whether any row of the table sits below key. It is the
// question IsBlock asks of the assembled table, and buildRows cannot ask it
// there: the table is what it is building, so the rows it holds are not yet the
// package's (rows).
func blockAmong(out []Row, key string) bool {
	prefix := key + "."
	for _, r := range out {
		if strings.HasPrefix(r.Key, prefix) {
			return true
		}
	}
	return false
}

// IsBlock reports whether key is a block of the registry: a key whose own
// spelling holds other rows below it, so a write of it as one value is a write
// of a shape and not of a setting. `governance.stage_timeout` is one against
// its three stages, `governance.tiers` against its four, and the layer file's
// own `layer.profiles.*` against the profile the pattern covers.
//
// The read keeps the row — a person reading `fugaro settings` wants to see the
// block and what it holds — and the write refuses it rather than failing at the
// file's own re-read ("cannot unmarshal !!str into config.StageTimeout"), which
// is what the four governance block rows did while they listed themselves
// writable (D2604).
func IsBlock(key string) bool {
	return len(BlockChildren(key)) > 0
}

// BlockChildren are the keys key holds, by the segment that follows it in
// registry order: `governance.tiers` holds `governance.tiers.large`,
// `governance.tiers.medium`, `governance.tiers.small` and
// `governance.tiers.trivial`. A key no row sits under answers nothing, which is
// what makes it a setting rather than a block (IsBlock).
func BlockChildren(key string) []string {
	prefix := key + "."
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !strings.HasPrefix(r.Key, prefix) {
			continue
		}
		child := prefix + strings.SplitN(strings.TrimPrefix(r.Key, prefix), ".", 2)[0]
		if seen[child] {
			continue
		}
		seen[child] = true
		out = append(out, child)
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
	"local.version": value("3", "localcfg.ConfigVersion"),
	// The Issues section's nudge thresholds (T-IS15): the loader answers the
	// constants for a file that sets neither, through the accessors rather
	// than the fields, so "the file wrote nothing" and "the file wrote the
	// default" are one reading here.
	"issues.nudge_after_days": value("7", "localcfg.DefaultIssuesNudgeAfterDays"),
	"issues.nudge_recurrence": value("3", "localcfg.DefaultIssuesNudgeRecurrence"),
	// The ladder's own two (E3): the scale a critical-path item's times are
	// multiplied by, and the switch that decides whether the orchestrator may
	// ready a stalled draft itself (on, D2046).
	"escalation.critical_path_factor": value("0.5", "localcfg.DefaultEscalationCriticalPathFactor"),
	"escalation.ready_drafts":         value("true", "localcfg.DefaultEscalationReadyDrafts"),
	// The shared images' retention (C8c): the local loader answers 30 for a
	// file that sets it nowhere (localcfg.ImageRetentionDays), so the reader
	// reads the value a run gets back out through the accessor.
	"images.retention_days": value("30", "localcfg.DefaultImageRetentionDays"),
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
