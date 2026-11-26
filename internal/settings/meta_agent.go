package settings

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/escalation"
)

// The `agent.`, `escalation.`, `stages.`, `orchestrator.`, `issues.` and
// `repos.` blocks' metadata (SM1 of
// docs/console-improvements/implementation-plan.md, D2908): what a person
// reads beside a run's own settings instead of the raw key, and the control
// each value calls for.
//
// The labels and the descriptions are the console's whole vocabulary for these
// blocks, so they are written once, here, rather than in a view: the CLI's own
// help and the console's panel read the same table (spec §4).
//
// Every bound is one the loaders accept, and a key whose bound is open on a
// side states none on that side rather than a figure the loader refuses:
// `escalation.r3_after_hours` and `escalation.cost_ceiling` are *more* than
// zero, and Meta has no exclusive bound, so the console draws their field
// unbounded and the dry run is what refuses a zero. An enum is the closed set
// the loader admits, named by the constant that holds it — config.AgentAuths,
// config.Efforts, config.Providers and escalation.Grades — so a value added to
// one of those lists is a value the select offers without a second edit here.
// TestSettingsMetaAgentBoundariesAreAccepted (internal/cli) is what holds every
// one of them to the strict decode.
var metaAgent = []struct {
	key  string
	meta Meta
}{
	{"agent.adversarial_rounds", Meta{
		Label:       "Adversarial review rounds",
		Description: "How many adversarial review rounds a run spends, or 0 to let the rigor mode decide.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
		Max:         "10",
	}},
	{"agent.auth", Meta{
		Label:       "Model call authentication",
		Description: "Where a run's model calls are authenticated.",
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        config.AgentAuths,
	}},
	{"agent.compression", Meta{
		Label:       "Context compression",
		Description: "Reserved for the context-compression proxy a workflow's model calls route through, which no release accepts yet.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.coder.access", Meta{
		Label:       "Coder craft access",
		Description: "The credential or pool the coder craft's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.coder.model", Meta{
		Label:       "Coder craft model",
		Description: "The model the coder craft runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.merge-judge.access", Meta{
		Label:       "Merge judge craft access",
		Description: "The credential or pool the merge judge craft's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.merge-judge.model", Meta{
		Label:       "Merge judge craft model",
		Description: "The model the merge judge craft runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.review.access", Meta{
		Label:       "Review craft access",
		Description: "The credential or pool the review craft's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.review.model", Meta{
		Label:       "Review craft model",
		Description: "The model the review craft runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.security-review.access", Meta{
		Label:       "Security review craft access",
		Description: "The credential or pool the security review craft's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.security-review.model", Meta{
		Label:       "Security review craft model",
		Description: "The model the security review craft runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.senior-review.access", Meta{
		Label:       "Senior review craft access",
		Description: "The credential or pool the senior review craft's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.crafts.senior-review.model", Meta{
		Label:       "Senior review craft model",
		Description: "The model the senior review craft runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.first_line_review", Meta{
		Label:       "First-line review",
		Description: "Whether a review by the coder's own model comes before the senior review.",
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        []string{config.FirstLineAuto, config.FirstLineOn, config.FirstLineOff},
	}},
	{"agent.first_line_rounds", Meta{
		Label:       "First-line review rounds",
		Description: "How many first-line review and fix cycles a run spends, or 0 to let the rigor mode decide.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
		Max:         "3",
	}},
	{"agent.instructions", Meta{
		Label:       "Extra instructions",
		Description: "Text added to every stage's instructions.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l1.effort", metaAgentEffort("Level 1 effort", "The reasoning effort review level 1 runs at.")},
	{"agent.levels.l1.focus", Meta{
		Label:       "Level 1 focus",
		Description: "What review level 1's reviewer is asked to focus on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l1.model", Meta{
		Label:       "Level 1 model",
		Description: "The model review level 1 runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l1.none", Meta{
		Label:       "Level 1 off",
		Description: "Whether review level 1 does not run.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},
	{"agent.levels.l2.effort", metaAgentEffort("Level 2 effort", "The reasoning effort review level 2 runs at.")},
	{"agent.levels.l2.focus", Meta{
		Label:       "Level 2 focus",
		Description: "What review level 2's reviewer is asked to focus on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l2.model", Meta{
		Label:       "Level 2 model",
		Description: "The model review level 2 runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l2.none", Meta{
		Label:       "Level 2 off",
		Description: "Whether review level 2 does not run.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},
	{"agent.levels.l3.effort", metaAgentEffort("Level 3 effort", "The reasoning effort review level 3 runs at.")},
	{"agent.levels.l3.focus", Meta{
		Label:       "Level 3 focus",
		Description: "What review level 3's reviewer is asked to focus on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l3.model", Meta{
		Label:       "Level 3 model",
		Description: "The model review level 3 runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.levels.l3.none", Meta{
		Label:       "Level 3 off",
		Description: "Whether review level 3 does not run.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},
	{"agent.max_budget_usd", Meta{
		Label:       "Per-run budget",
		Description: "The most one run of this repository may spend, in US dollars.",
		Group:       GroupRuns,
		Type:        TypeFloat,
		Min:         "0",
	}},
	{"agent.max_output_tokens.coder", Meta{
		Label:       "Coder output token limit",
		Description: "The most tokens one coder call may produce, or 0 for no limit.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
		Max:         "128000",
	}},
	{"agent.max_output_tokens.reviewer", Meta{
		Label:       "Reviewer output token limit",
		Description: "The most tokens one reviewer call may produce, or 0 for no limit.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
		Max:         "128000",
	}},
	{"agent.max_run_tokens", Meta{
		Label:       "Run token limit",
		Description: "The most tokens one run may spend, or 0 for no limit.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"agent.model", Meta{
		Label:       "Default model",
		Description: "The model every stage uses unless a role names its own.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.models.background", Meta{
		Label:       "Background model",
		Description: "The model the background stages run.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.models.coder", Meta{
		Label:       "Coder model",
		Description: "The model the implement stage runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.models.diagnose", Meta{
		Label:       "Diagnose model",
		Description: "The model a diagnose pass runs.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.models.reviewer", Meta{
		Label:       "Reviewer model",
		Description: "The model the review stages run.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.models.vision", Meta{
		Label:       "Vision model",
		Description: "The model a run's coder stages run when the task names an image their own model cannot read.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.oauth_token", Meta{
		Label:       "OAuth account",
		Description: "The named Claude account a workflow with oauth authentication uses.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.recipe", Meta{
		Label:       "Task recipe",
		Description: "The recipe the run's task loop follows.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.review", Meta{
		Label:       "Review instructions file",
		Description: "A file in the repository holding extra review instructions.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"agent.review_against_task", Meta{
		Label:       "Review against task",
		Description: "Whether every review stage is shown the task the pull request was written for.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},
	{"agent.review_rounds", Meta{
		Label:       "Review rounds",
		Description: "How many review rounds a run spends before it finalizes, or 0 to let the rigor mode decide.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
		Max:         "10",
	}},
	{"agent.review_stop_fraction", Meta{
		Label:       "Review stop fraction",
		Description: "The share of the run's time budget after which the review loop starts no further round.",
		Group:       GroupRuns,
		Type:        TypeFloat,
		Min:         "0",
		Max:         "1",
	}},
	{"agent.triage", Meta{
		Label:       "Triage stage",
		Description: "Whether the run's triage stage runs where the rigor mode has it.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},
	{"agent.workspace_beat", Meta{
		Label:       "Workspace keep-warm beat",
		Description: "Whether a run that uses its workspace's credentials service keeps it warm between calls.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},

	{"escalation.cost_ceiling", Meta{
		Label:       "Escalation cost ceiling",
		Description: "The spend over an item's run records at which the ladder escalates, and no value means no cost trigger.",
		Group:       GroupRuns,
		Type:        TypeFloat,
	}},
	{"escalation.critical_path_factor", Meta{
		Label:       "Critical path time scale",
		Description: "The factor every time threshold of a critical-path item is multiplied by.",
		Group:       GroupRuns,
		Type:        TypeFloat,
		Max:         "1",
	}},
	{"escalation.merge_judge", Meta{
		Label:       "Merge judge grade",
		Description: "The model grade the advisory merge judge runs on.",
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        metaEscalationGrades(),
	}},
	{"escalation.modes.*.r2_after_attempts", Meta{
		Label:       "Mode rung 2 attempts",
		Description: "How many runs of an item's normal recipe one mode spends before rung 2.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"escalation.modes.*.r2_after_hours", Meta{
		Label:       "Mode rung 2 hours",
		Description: "How long an item may wait in one mode before rung 2 runs.",
		Group:       GroupRuns,
		Type:        TypeFloat,
		Min:         "0",
	}},
	{"escalation.modes.*.r2_coder", Meta{
		Label:       "Mode rung 2 grade",
		Description: "The model grade one mode's rung 2 runs at.",
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        metaEscalationGrades(),
	}},
	{"escalation.modes.*.r3_after_hours", Meta{
		Label:       "Mode rung 3 hours",
		Description: "The hard ceiling in hours at which one mode's ladder merges instead of launching another pass.",
		Group:       GroupRuns,
		Type:        TypeFloat,
	}},
	{"escalation.r2_after_attempts", Meta{
		Label:       "Rung 2 attempts",
		Description: "How many runs of an item's normal recipe a run spends before rung 2.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"escalation.r2_after_hours", Meta{
		Label:       "Rung 2 hours",
		Description: "How long an item may wait before rung 2 runs.",
		Group:       GroupRuns,
		Type:        TypeFloat,
		Min:         "0",
	}},
	{"escalation.r2_coder", Meta{
		Label:       "Rung 2 grade",
		Description: "The model grade rung 2 runs at.",
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        metaEscalationGrades(),
	}},
	{"escalation.r3_after_hours", Meta{
		Label:       "Rung 3 hours",
		Description: "The hard ceiling in hours at which the ladder merges instead of launching another pass.",
		Group:       GroupRuns,
		Type:        TypeFloat,
	}},
	{"escalation.ready_drafts", Meta{
		Label:       "Ready stalled drafts",
		Description: "Whether the orchestrator may ready a stalled draft on its own.",
		Group:       GroupRuns,
		Type:        TypeBool,
	}},

	{"issues.nudge_after_days", Meta{
		Label:       "Nudge after days",
		Description: "The age in days at which a pending issue's line firms up.",
		Group:       GroupReviews,
		Type:        TypeInt,
		Min:         "1",
	}},
	{"issues.nudge_recurrence", Meta{
		Label:       "Nudge recurrence",
		Description: "The occurrence count at which a recurring issue's line firms up.",
		Group:       GroupReviews,
		Type:        TypeInt,
		Min:         "1",
	}},

	{"orchestrator.batch_size", Meta{
		Label:       "Runs per cycle",
		Description: "How many runs one orchestrator cycle may start.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"orchestrator.decision_model_per_class", Meta{
		Label:       "Decision models per class",
		Description: "The model each decision class runs on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"orchestrator.max_running", Meta{
		Label:       "Runs at once",
		Description: "How many runs the orchestrator may have going at once.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"orchestrator.spawn_per_day", Meta{
		Label:       "Runs per day",
		Description: "How many runs the orchestrator may start in a day.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"orchestrator.spawn_per_hour", Meta{
		Label:       "Runs per hour",
		Description: "How many runs the orchestrator may start in an hour.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},
	{"orchestrator.spawn_per_minute", Meta{
		Label:       "Runs per minute",
		Description: "How many runs the orchestrator may start in a minute.",
		Group:       GroupRuns,
		Type:        TypeInt,
		Min:         "0",
	}},

	{"repos.*.base_branch", Meta{
		Label:       "Repository base branch",
		Description: "The branch this repository's runs build on.",
		Group:       GroupRepositories,
		Type:        TypeString,
		Format:      FormatBranch,
	}},
	{"repos.*.github_app_id", Meta{
		Label:       "GitHub App ID",
		Description: "The GitHub App this repository's runs authenticate as.",
		Group:       GroupRepositories,
		Type:        TypeString,
	}},
	{"repos.*.provider", Meta{
		Label:       "Git provider",
		Description: "The forge this repository is hosted on.",
		Group:       GroupRepositories,
		Type:        TypeEnum,
		Enum:        config.Providers,
	}},
	{"repos.*.workflows", Meta{
		Label:       "Managed workflows",
		Description: "The workflows of this repository that Fugaro manages.",
		Group:       GroupRepositories,
		Type:        TypeString,
	}},

	{"stages.*.claude_account", Meta{
		Label:       "Stage account",
		Description: "The Claude account a stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.adversarial.claude_account", Meta{
		Label:       "Adversarial stage account",
		Description: "The Claude account the adversarial review stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.fix.claude_account", Meta{
		Label:       "Fix stage account",
		Description: "The Claude account the fix stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.implement.claude_account", Meta{
		Label:       "Implement stage account",
		Description: "The Claude account the implement stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.plan.claude_account", Meta{
		Label:       "Plan stage account",
		Description: "The Claude account the plan stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.review.claude_account", Meta{
		Label:       "Review stage account",
		Description: "The Claude account the review stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.review_first.claude_account", Meta{
		Label:       "First review stage account",
		Description: "The Claude account the first-line review stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
	{"stages.triage.claude_account", Meta{
		Label:       "Triage stage account",
		Description: "The Claude account the triage stage's calls go out on.",
		Group:       GroupRuns,
		Type:        TypeString,
	}},
}

// metaAgentEffort is one review level's effort entry: the three levels share
// the label's shape and the closed set, and only the level's name and the
// description's differ.
func metaAgentEffort(label, description string) Meta {
	return Meta{
		Label:       label,
		Description: description,
		Group:       GroupRuns,
		Type:        TypeEnum,
		Enum:        config.Efforts,
	}
}

// metaEscalationGrades is escalation.Grades as the strings a document writes:
// the ladder names a grade and never a model (D2033, G11), and the closed set
// is the ladder's own.
func metaEscalationGrades() []string {
	out := make([]string, 0, len(escalation.Grades))
	for _, g := range escalation.Grades {
		out = append(out, string(g))
	}
	return out
}

// The registration is a package init and its error is a programming error: a
// key another family already claimed, a group outside the order or a control
// that cannot carry what it carries is a diff to settle rather than a
// registration to win, so this panics and every command of the binary fails
// loudly instead of one of the two silently winning.
func init() {
	for _, e := range metaAgent {
		if err := Register(e.key, e.meta); err != nil {
			panic(err)
		}
	}
}
