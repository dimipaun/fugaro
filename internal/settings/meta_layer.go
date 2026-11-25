package settings

import "strings"

// This file is the metadata (meta.go, D2908) of the project layer's own keys:
// the file header's default_profile, the defaults: overlay, the profiles, the
// ab: block and the two jobs: schedules. It is SM3 of
// docs/console-improvements/implementation-plan.md, which splits the backfill
// four ways by family; the workflows.* half is meta_workflows.go.
//
// Every key here belongs to GroupProfiles, because MetaGroups gives that group
// "the project layer's own file: layer.*" — a person looking for what a project
// sets for every repository of it looks in one section, and not in the section
// of each block the key happens to name. The console draws a key with no entry
// under its own "Other" group by its raw key (spec §4, D2908), so an entry this
// file is missing is a screen a person cannot read rather than a failure.
//
// The bounds and the enumerations are the ones the layer's own reader enforces
// (internal/config's validateLayer, validateProfile, validateGovernance and
// validateAB), and internal/cli's TestSettingsMetaLayerBoundariesAreAccepted
// sets each of them through settingsReparse — the strict decode `settings set`
// re-reads the bytes it wrote with — so a bound a person is offered is a bound
// the write accepts, and one just past it is a value the write refuses.

func init() { registerLayerMeta() }

// registerLayerMeta fills the table with this family's entries. Registration is
// a programming error rather than a user's mistake (Register), so a refusal
// stops the binary rather than one command: a label too long or a group outside
// the order is a diff to fix, never a screen to ship.
func registerLayerMeta() {
	each := func(key string, m Meta) {
		if err := Register(key, m); err != nil {
			panic("internal/settings: " + err.Error())
		}
	}

	each("layer.default_profile", Meta{
		Label:       "Default profile",
		Description: "The profile every workflow of the project starts from when its own profile names none.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})

	// The A/B block (docs/model-ab-testing, D550-D573). The sampling rate and
	// the daily pair count are the two bounds validateAB enforces; the
	// challenger list, the judge and the mode table are the owner's own
	// choices, which it refuses only for the shape of a model ID or a mode.
	each("layer.ab", Meta{
		Label:       "A/B sampling",
		Description: "The project's A/B sampler: how many runs are sampled, which models may challenge and what a pair may cost.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.ab.rate", Meta{
		Label:       "Sampling rate",
		Description: "The share of eligible first runs sampled for a pair, where 0 turns sampling off.",
		Group:       GroupProfiles,
		Type:        TypeFloat,
		Min:         "0",
		Max:         "0.1",
	})
	each("layer.ab.repos", Meta{
		Label:       "Sampling rate by repository",
		Description: "The sampling rate the project names for one repository, inside the block's own rate, where 0 opts that repository out.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.ab.max_pairs_per_day", Meta{
		Label:       "Pairs per day",
		Description: "The most pairs one repository is sampled for in a day, where 0 takes the built-in cap.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
	})
	each("layer.ab.challengers", Meta{
		Label:       "Challenger models",
		Description: "The models the project allows as a challenger, in priority order.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.ab.judge_model", Meta{
		Label:       "Judge model",
		Description: "The model a pair's blind judge run uses.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.ab.exchange_rate", Meta{
		Label:       "Extra cost by mode",
		Description: "The extra median cost a pair may carry under each rigor mode, as a fraction of the champion's.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})

	// The two schedules (R13). paused is the one direction of the pair: true
	// stops the job, false starts it.
	each("layer.jobs.catalog_check.paused", Meta{
		Label:       "Pause catalog check",
		Description: "Whether the project's periodic image-catalog check is paused.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.jobs.reports.paused", Meta{
		Label:       "Pause reports",
		Description: "Whether the project's periodic reports job is paused.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})

	// defaults.git: the overlay a repository's own git: block replaces key by
	// key.
	each("layer.defaults.git.provider", Meta{
		Label:       "Git provider",
		Description: "The git host every repository of the project is reached through.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"github", "bitbucket"},
	})
	each("layer.defaults.git.pr.labels", Meta{
		Label:       "Pull request labels",
		Description: "The labels every pull request of the project carries.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.git.pr.early_draft", Meta{
		Label:       "Open early drafts",
		Description: "Whether a first run opens its draft pull request at the first checkpoint push, or at the first verified stage end when checkpoints are off, instead of only at finalize.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.defaults.git.pr.checkpoints", Meta{
		Label:       "Push checkpoints",
		Description: "Whether a first run pushes its new commits to its branch while a stage runs and at every stage boundary, so a container that dies keeps its committed work.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})

	// defaults.agent: the project-wide model and review overlay.
	each("layer.defaults.agent.auth", Meta{
		Label:       "Model access",
		Description: "How the project's model calls authenticate.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"vertex", "api-key", "oauth", "cascade"},
	})
	each("layer.defaults.agent.model", Meta{
		Label:       "Default model",
		Description: "The model every repository of the project runs on unless a slot or the repository itself names another.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.models.coder", Meta{
		Label:       "Coder model",
		Description: "The model the coder role runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.models.reviewer", Meta{
		Label:       "Reviewer model",
		Description: "The model the reviewer role runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.models.background", Meta{
		Label:       "Background model",
		Description: "The model the background role runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.models.vision", Meta{
		Label:       "Vision model",
		Description: "The model a stage falls back to when its own model cannot read an image.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.models.diagnose", Meta{
		Label:       "Diagnose model",
		Description: "The model a local diagnosis runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.coder.model", Meta{
		Label:       "Coder craft model",
		Description: "The model the coder craft runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.coder.access", Meta{
		Label:       "Coder craft credential",
		Description: "The credential or pool the coder craft's calls go out on, which no project layer may name.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.review.model", Meta{
		Label:       "Review craft model",
		Description: "The model the review craft runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.review.access", Meta{
		Label:       "Review craft credential",
		Description: "The credential or pool the review craft's calls go out on, which no project layer may name.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.senior-review.model", Meta{
		Label:       "Senior review craft model",
		Description: "The model the senior review craft runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.senior-review.access", Meta{
		Label:       "Senior review craft credential",
		Description: "The credential or pool the senior review craft's calls go out on, which no project layer may name.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.security-review.model", Meta{
		Label:       "Security review craft model",
		Description: "The model the security review craft runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.security-review.access", Meta{
		Label:       "Security review craft credential",
		Description: "The credential or pool the security review craft's calls go out on, which no project layer may name.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.merge-judge.model", Meta{
		Label:       "Merge judge craft model",
		Description: "The model the merge-judge craft runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.crafts.merge-judge.access", Meta{
		Label:       "Merge judge craft credential",
		Description: "The credential or pool the merge-judge craft's calls go out on, which no project layer may name.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.model_grades.strong.model", Meta{
		Label:       "Strong grade model",
		Description: "The model the escalation ladder's strong rung runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.model_grades.strong.access", Meta{
		Label:       "Strong grade credential",
		Description: "The credential or pool the strong rung's calls go out on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.model_grades.strongest.model", Meta{
		Label:       "Strongest grade model",
		Description: "The model the escalation ladder's strongest rung runs on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.model_grades.strongest.access", Meta{
		Label:       "Strongest grade credential",
		Description: "The credential or pool the strongest rung's calls go out on for every repository of the project.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.agent.review_rounds", Meta{
		Label:       "Review rounds",
		Description: "How many review rounds a run spends for every repository of the project, where 0 leaves it to the rigor mode.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "10",
	})
	each("layer.defaults.agent.max_budget_usd", Meta{
		Label:       "Run budget",
		Description: "The most money a run of the project's repositories may spend, in US dollars.",
		Group:       GroupProfiles,
		Type:        TypeFloat,
		Min:         "0",
	})
	each("layer.defaults.agent.first_line_review", Meta{
		Label:       "First-line review",
		Description: "Whether a run's first diff is reviewed before the rest of the work continues.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"auto", "on", "off"},
	})
	each("layer.defaults.agent.first_line_rounds", Meta{
		Label:       "First-line rounds",
		Description: "How many rounds the first-line review spends, where 0 leaves it to the rigor mode.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "3",
	})
	each("layer.defaults.agent.adversarial_rounds", Meta{
		Label:       "Adversarial rounds",
		Description: "How many rounds the adversarial review level spends, where 0 leaves it to the rigor mode.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "10",
	})
	each("layer.defaults.agent.review_stop_fraction", Meta{
		Label:       "Review stop fraction",
		Description: "The share of the run's time budget after which the review loop starts no further round.",
		Group:       GroupProfiles,
		Type:        TypeFloat,
		Min:         "0",
		Max:         "1",
	})
	each("layer.defaults.agent.workspace_beat", Meta{
		Label:       "Workspace keep-warm beat",
		Description: "Whether the project's runs keep their workspace's credentials service warm between calls.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.defaults.agent.recipe", Meta{
		Label:       "Model recipe",
		Description: "The named recipe the project's model choices are taken from.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	for level, n := range map[string]string{"l1": "1", "l2": "2", "l3": "3"} {
		each("layer.defaults.agent.levels."+level+".model", Meta{
			Label:       "Level " + n + " model",
			Description: "The model review level " + n + " runs on for every repository of the project, or none for a level that does not run.",
			Group:       GroupProfiles,
			Type:        TypeString,
		})
		each("layer.defaults.agent.levels."+level+".effort", Meta{
			Label:       "Level " + n + " effort",
			Description: "The reasoning effort review level " + n + " asks its model for.",
			Group:       GroupProfiles,
			Type:        TypeEnum,
			Enum:        []string{"low", "medium", "high", "xhigh", "max"},
		})
		each("layer.defaults.agent.levels."+level+".none", Meta{
			Label:       "Disable level " + n,
			Description: "Whether review level " + n + " does not run for the project at all.",
			Group:       GroupProfiles,
			Type:        TypeBool,
		})
		each("layer.defaults.agent.levels."+level+".focus", Meta{
			Label:       "Level " + n + " focus",
			Description: "What review level " + n + "'s reviewer is asked to look at, in the owner's own words.",
			Group:       GroupProfiles,
			Type:        TypeString,
		})
	}

	// defaults.rigor: the project's own mode and its path rules.
	each("layer.defaults.rigor.default", Meta{
		Label:       "Default rigor mode",
		Description: "The rigor mode every repository of the project runs at unless its own file names another.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"poc", "startup", "pro", "critical", "hotfix"},
	})
	each("layer.defaults.rigor.areas.*.name", Meta{
		Label:       "Rigor rule name",
		Description: "The name of a path rule, which is what the run record and the pull request say decided the level.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.rigor.areas.*.match", Meta{
		Label:       "Rigor rule paths",
		Description: "The repository-relative glob whose matching changes the rigor mode of the project's runs.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.rigor.areas.*.level", Meta{
		Label:       "Rigor rule mode",
		Description: "The rigor mode a run takes when its diff matches this rule's paths.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"poc", "startup", "pro", "critical", "hotfix"},
	})
	each("layer.defaults.rigor.areas.*.members.*.repo", Meta{
		Label:       "Rigor area member repository",
		Description: "The repository of the project that one member of a rigor area names.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.rigor.areas.*.members.*.paths", Meta{
		Label:       "Rigor area member paths",
		Description: "The repository-relative paths of the member's repository that belong to the rigor area.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.rigor.repos", Meta{
		Label:       "Rigor mode by repository",
		Description: "The rigor mode the project names for one of its repositories.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.defaults.planning.decoupling", Meta{
		Label:       "Plan before coding",
		Description: "Whether a run of the project writes its plan before it writes code.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})

	// governance: the project's own guardrails, which a repository may only
	// tighten. The bounds are validateGovernance's, which counts three shapes:
	// a counted key with its own ceiling, a stage budget, which is a fraction
	// of the run's cap and so stays below 1, and a stage timeout, which is any
	// positive duration and carries no ceiling at all.
	each("layer.governance.review_ratio", Meta{
		Label:       "Review ratio",
		Description: "Review calls above this multiple of the implement stage's calls stop the review stage.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "1000",
	})
	each("layer.governance.review_floor_calls", Meta{
		Label:       "Review floor calls",
		Description: "The count of implement calls below which the ratio does not apply, so a short implementation is still reviewed.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "100000",
	})
	each("layer.governance.max_subagents", Meta{
		Label:       "Subagent cap",
		Description: "The most distinct subagents one stage of a run of the project may start.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "100",
	})
	for _, stage := range []string{"implement", "review", "fix"} {
		each("layer.governance.stage_timeout."+stage, Meta{
			Label:       "Stage timeout: " + stage,
			Description: "How long the " + stage + " stage may take before the run's own cap ends it.",
			Group:       GroupProfiles,
			Type:        TypeDuration,
		})
		// 0.99 and not 1: the decoder refuses a share of 1 or more — the run's
		// cap stays the ceiling whatever a stage is given — and a bound here is
		// inclusive, so the ceiling a console offers is the largest value under
		// the boundary rather than the boundary itself.
		each("layer.governance.stage_budget."+stage, Meta{
			Label:       "Stage budget: " + stage,
			Description: "The share of the run's cap the " + stage + " stage may spend, a fraction below 1.",
			Group:       GroupProfiles,
			Type:        TypeFloat,
			Min:         "0",
			Max:         "0.99",
		})
		each("layer.governance.stage_tokens."+stage, Meta{
			Label:       "Stage tokens: " + stage,
			Description: "The most model tokens the " + stage + " stage may spend.",
			Group:       GroupProfiles,
			Type:        TypeInt,
			Min:         "0",
			Max:         "1000000000000",
		})
	}
	for _, size := range []string{"trivial", "small", "medium", "large"} {
		each("layer.governance.tiers."+size+".expected_usd", Meta{
			Label:       "Tier cost: " + size,
			Description: "The dollars a " + size + " task is expected to cost, which the report compares a run against.",
			Group:       GroupProfiles,
			Type:        TypeFloat,
			Min:         "0",
		})
		each("layer.governance.tiers."+size+".expected_minutes", Meta{
			Label:       "Tier minutes: " + size,
			Description: "The minutes a " + size + " task is expected to take, which the report compares a run against.",
			Group:       GroupProfiles,
			Type:        TypeInt,
			Min:         "0",
		})
		each("layer.governance.tiers."+size+".max_subagents", Meta{
			Label:       "Tier subagents: " + size,
			Description: "The most subagents a " + size + " task may fan out to.",
			Group:       GroupProfiles,
			Type:        TypeInt,
			Min:         "0",
			Max:         "100",
		})
	}

	// profiles.*: the named workflow templates. The labels name the profile
	// rather than repeating its name, because the console shows the key beside
	// the label and the block is what a person picked in the list.
	each("layer.profiles.*.description", Meta{
		Label:       "Profile description",
		Description: "One line saying what this profile is for, shown beside it in the list.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.base", Meta{
		Label:       "Profile base image",
		Description: "The base image of the profile, which 0.7.0 removed in favour of the one Fugaro base.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.node", Meta{
		Label:       "Profile Node version",
		Description: "The Node version of the profile's image, which 0.7.0 removed in favour of mise tools.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.jdk", Meta{
		Label:       "Profile Java version",
		Description: "The Java version of the profile's image, which 0.7.0 removed in favour of mise tools.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.apt", Meta{
		Label:       "Profile system packages",
		Description: "The system packages the profile's image installs as root.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.setup", Meta{
		Label:       "Profile image steps",
		Description: "The extra build steps the profile's image runs after its warm-up.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.skip_build_scripts", Meta{
		Label:       "Profile skips build scripts",
		Description: "Whether the profile's Node dependency install skips package lifecycle and build scripts.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.profiles.*.image.tools", Meta{
		Label:       "Profile mise tools",
		Description: "The mise tools and versions the profile's image writes into its global config.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.image.key_files", Meta{
		Label:       "Profile key files",
		Description: "The files the profile's dependency install reads, whose change rebuilds its image.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.build", Meta{
		Label:       "Build command",
		Description: "The command a workflow starting from this profile runs to build.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.test", Meta{
		Label:       "Test command",
		Description: "The command a workflow starting from this profile runs to test.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.lint", Meta{
		Label:       "Lint command",
		Description: "The gate command a workflow starting from this profile runs after its fix command.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.fix", Meta{
		Label:       "Fix command",
		Description: "The command the check step runs before lint to repair what the gate finds.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.render", Meta{
		Label:       "Render command",
		Description: "The command an artifact run of this profile runs once to render its outputs.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.probe", Meta{
		Label:       "Probe command",
		Description: "The command an artifact run of this profile runs to check what it rendered.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.reports", Meta{
		Label:       "Report globs",
		Description: "The paths an artifact run of this profile collects as its report.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.rerun_failed", Meta{
		Label:       "Rerun failed tests",
		Description: "The command that reruns the tests a failed run left behind, built from the profile's own command and per-test arguments.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.rerun_failed.command", Meta{
		Label:       "Rerun command",
		Description: "The test command a rerun of this profile's failed tests starts from.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.commands.rerun_failed.each", Meta{
		Label:       "Rerun per-test arguments",
		Description: "The arguments appended once per failed test, with {id} standing for the test it names.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.cache", Meta{
		Label:       "Profile caches",
		Description: "The dependency caches the profile's jobs restore and write back.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.cache.*.key", Meta{
		Label:       "Cache key files",
		Description: "The files whose hash decides which cached entry this profile restores.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.cache.*.paths", Meta{
		Label:       "Cache paths",
		Description: "The directories the profile's cache saves and restores.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.resources.cpu", Meta{
		Label:       "Profile CPU",
		Description: "The number of CPUs a job of this profile's workflows runs with.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
	})
	each("layer.profiles.*.resources.memory", Meta{
		Label:       "Profile memory",
		Description: "The memory a job of this profile's workflows runs with, such as 8Gi.",
		Group:       GroupProfiles,
		Type:        TypeString,
		Format:      FormatMemory,
	})
	for to, name := range map[string]string{
		"total":            "Profile timeout",
		"stage":            "Profile stage timeout",
		"verify":           "Profile verify timeout",
		"render":           "Profile render timeout",
		"finalize_reserve": "Profile finalize reserve",
	} {
		each("layer.profiles.*.timeouts."+to, Meta{
			Label:       name,
			Description: "The " + strings.ToLower(strings.TrimPrefix(name, "Profile ")) + " of a job of this profile's workflows, a positive duration such as 90m.",
			Group:       GroupProfiles,
			Type:        TypeDuration,
			Format:      FormatDuration,
		})
	}
	each("layer.profiles.*.rebuild.check", Meta{
		Label:       "Rebuild check",
		Description: "Whether an image built from this profile is checked daily or only rebuilt by hand.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"daily", "off"},
	})
	each("layer.profiles.*.rebuild.max_age", Meta{
		Label:       "Rebuild max age",
		Description: "The age at which an image built from this profile is rebuilt, or 0 for no age limit.",
		Group:       GroupProfiles,
		Type:        TypeDuration,
		Format:      FormatDuration,
	})
	each("layer.profiles.*.rebuild.lockfiles", Meta{
		Label:       "Rebuild on lockfiles",
		Description: "Whether a lockfile change on the base branch rebuilds an image built from this profile.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.profiles.*.rebuild.base", Meta{
		Label:       "Rebuild on base image",
		Description: "Whether a move of the base image's digest rebuilds an image built from this profile.",
		Group:       GroupProfiles,
		Type:        TypeBool,
	})
	each("layer.profiles.*.rebuild.paths", Meta{
		Label:       "Rebuild paths",
		Description: "The globs whose change on the base branch rebuilds an image built from this profile.",
		Group:       GroupProfiles,
		Type:        TypeString,
	})
	each("layer.profiles.*.inputs.max_files", Meta{
		Label:       "Input file cap",
		Description: "The most files a launcher may attach to a run of this profile's workflows.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "64",
	})
	each("layer.profiles.*.inputs.max_bytes", Meta{
		Label:       "Input file size",
		Description: "The most bytes one attached file may hold.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "1073741824",
	})
	each("layer.profiles.*.inputs.max_total_bytes", Meta{
		Label:       "Input total size",
		Description: "The most bytes the files attached to one run may hold together.",
		Group:       GroupProfiles,
		Type:        TypeInt,
		Min:         "0",
		Max:         "4294967296",
	})
	each("layer.profiles.*.checkout", Meta{
		Label:       "Profile checkout kind",
		Description: "Whether a job of this profile's workflows runs a baked per-repository image or the base image with a clone.",
		Group:       GroupProfiles,
		Type:        TypeEnum,
		Enum:        []string{"baked", "clone"},
	})
}
