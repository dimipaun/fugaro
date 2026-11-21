package settings

import (
	"fmt"
	"strconv"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// metaGit is SM2 of docs/console-improvements/implementation-plan.md: the
// entries of six families of keys — `git.`, `followup.`, `clone_urls.`,
// `credentials.`, `watch.` and `governance.` — whose keys a person reads and
// writes on the console's settings panel (spec §3 and §4, D2908).
//
// The table holds the *writable* keys of those families and no others, which is
// what D2908 decided ("all writable keys now, the rest fall back to the key"):
// a row the write refuses has no control a person could commit, so an entry for
// it would draw a dialog whose every OK the CLI then refuses. Two of the six
// families are left with no entry at all for that reason, and both are recorded
// here rather than discovered later:
//
//   - `credentials.` — every row is either a map entry of the craft-credential
//     block (`credentials.*.name`, `credentials.*.type`), a policy-layer cap
//     this machine's file does not state (`credentials.backoff.cap` and the
//     seven beside it) or a key the accounts family owns (`credentials.fallback`);
//   - `clone_urls.` — the registry holds the block row `clone_urls` and no leaf
//     under it, and the block is a shape with no value of its own, so a
//     `clone_urls.` key is one a person writes with `fugaro project clone-url`.
//
// What is here is built against Meta and Register (SM0) and is registered the
// way the table's comment asks: in a package init, where a malformed entry is a
// programming error of this binary rather than a screen drawn wrong.
func init() {
	for _, e := range metaGit {
		if err := Register(e.key, e.m); err != nil {
			panic(fmt.Errorf("settings: the git family's metadata: %w", err))
		}
	}
}

// metaGit is the family's entries, in key order. The key stands beside its
// Meta rather than inside it: Meta is the shape the console reads, and it
// carries no key of its own (meta.go).
//
// The list is deliberately local to this file. A type of its own here would be
// the second backfill's compile error the day meta_agent.go lands, and the key
// is the one thing every family's file states differently anyway.
var metaGit = []struct {
	key string
	m   Meta
}{
	{
		key: "git.provider",
		m: Meta{
			Label:       "Git provider",
			Description: "The provider a run clones the repository from and opens its pull request on.",
			Group:       GroupRepositories,
			Type:        TypeEnum,
			// config.Providers is the list the loader holds git.provider to, so
			// the select draws exactly what the file may say (validate.go).
			Enum: config.Providers,
		},
	},
	{
		key: "git.base_branch",
		m: Meta{
			Label:       "Base branch",
			Description: "The branch a run's pull request is opened against.",
			Group:       GroupRepositories,
			Type:        TypeString,
			Format:      FormatBranch,
		},
	},
	{
		key: "git.author",
		m: Meta{
			Label:       "Commit author",
			Description: "Whether a run's commits are authored by the run itself or by the person who launched it.",
			Group:       GroupRepositories,
			Type:        TypeEnum,
			// The vocabulary is config.GitAuthors. The empty value is not in it
			// and is not offered: a file that spells it out is the key absent
			// (config.go's Git.Author), and a select has no member for that.
			Enum: config.GitAuthors,
		},
	},
	{
		key: "git.pr.labels",
		m: Meta{
			Label:       "Pull request labels",
			Description: "The labels applied to every pull request a run makes ready.",
			Group:       GroupRepositories,
			// A string list, and the type vocabulary has no list of its own
			// (meta.go's MetaTypes). It is written with --add and --remove, one
			// label at a time, so the console's own read-only rule for those
			// keys is what keeps a text control from standing in for them (R5).
			Type: TypeString,
		},
	},
	{
		key: "git.pr.reviewers",
		m: Meta{
			Label:       "Pull request reviewers",
			Description: "The reviewers requested on every pull request a run makes ready.",
			Group:       GroupRepositories,
			Type:        TypeString,
		},
	},
	{
		key: "git.pr.early_draft",
		m: Meta{
			Label:       "Early draft pull request",
			Description: "Whether the draft pull request opens at the first checkpoint push rather than at the end of the run.",
			Group:       GroupRepositories,
			Type:        TypeBool,
		},
	},
	{
		key: "git.pr.checkpoints",
		m: Meta{
			Label:       "Checkpoint pushes",
			Description: "Whether a first run pushes its new commits while a stage is still running.",
			Group:       GroupRepositories,
			Type:        TypeBool,
		},
	},
	{
		key: "followup.trusted",
		m: Meta{
			Label:       "Trusted comment authors",
			Description: "The account IDs whose pull request comments a follow-up run acts on.",
			Group:       GroupFollowups,
			// A string list, for git.pr.labels' reason.
			Type: TypeString,
		},
	},
	{
		key: "followup.allow_public",
		m: Meta{
			Label:       "Follow-ups on public repositories",
			Description: "Whether a follow-up run may act on a public repository, where anyone who can comment can steer it.",
			Group:       GroupFollowups,
			Type:        TypeBool,
		},
	},
	{
		key: "watch.burn_alert_usd_per_hour",
		m: Meta{
			Label:       "Burn alert threshold",
			Description: "The spend rate per hour above which watch marks a project or a repository as fast.",
			Group:       GroupWatching,
			Type:        TypeFloat,
			Min:         "0",
			// The loader's own ceiling is pricing.MaxUSD, which is the bound a
			// dollar amount is read through everywhere in this tree.
			Max: strconv.FormatInt(int64(pricing.MaxUSD), 10),
		},
	},
	{
		key: "governance.review_ratio",
		m: Meta{
			Label:       "Review ratio",
			Description: "How many times the implement stage's calls the review stage may spend before the run stops reviewing.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxReviewRatio), 10),
		},
	},
	{
		key: "governance.review_floor_calls",
		m: Meta{
			Label:       "Review floor calls",
			Description: "The implement-stage call count below which the review ratio does not apply at all.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxReviewFloorCalls), 10),
		},
	},
	{
		key: "governance.max_subagents",
		m: Meta{
			Label:       "Most sub-agents per stage",
			Description: "The most distinct sub-agents one stage may start, concurrently or in total.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxGovernanceSubagents), 10),
		},
	},
	{
		key: "governance.stage_budget.implement",
		m: Meta{
			Label:       "Implement stage budget",
			Description: "The implement stage's share of the run's per-run budget cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
			// No Max: the loader refuses 1 and everything above it, and Meta.Max
			// is an inclusive bound, so an entry stating it would claim a value
			// the file may not carry (validate.go's stage_budget rule). The
			// client's own validation is the hint; the dry run is the authority.
		},
	},
	{
		key: "governance.stage_budget.review",
		m: Meta{
			Label:       "Review stage budget",
			Description: "The review stage's share of the run's per-run budget cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.stage_budget.fix",
		m: Meta{
			Label:       "Fix stage budget",
			Description: "The fix stage's share of the implement stage's own budget.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.stage_tokens.implement",
		m: Meta{
			Label:       "Implement stage token cap",
			Description: "The implement stage's own ceiling on the tokens it may spend.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxStageTokens), 10),
		},
	},
	{
		key: "governance.stage_tokens.review",
		m: Meta{
			Label:       "Review stage token cap",
			Description: "The review stage's own ceiling on the tokens it may spend.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxStageTokens), 10),
		},
	},
	{
		key: "governance.stage_tokens.fix",
		m: Meta{
			Label:       "Fix stage token cap",
			Description: "The fix stage's own ceiling on the tokens it may spend.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxStageTokens), 10),
		},
	},
	{
		key: "governance.stage_timeout.implement",
		m: Meta{
			Label:       "Implement stage timeout",
			Description: "The implement stage's own wall-clock ceiling.",
			Group:       GroupReviews,
			Type:        TypeDuration,
			Format:      FormatDuration,
			// No Min: the loader's rule is a duration above zero, and "0" is
			// the one spelling a strictly positive bound cannot be stated with
			// (a Min of "0" would claim a value the file refuses).
		},
	},
	{
		key: "governance.stage_timeout.review",
		m: Meta{
			Label:       "Review stage timeout",
			Description: "The review stage's own wall-clock ceiling.",
			Group:       GroupReviews,
			Type:        TypeDuration,
			Format:      FormatDuration,
		},
	},
	{
		key: "governance.stage_timeout.fix",
		m: Meta{
			Label:       "Fix stage timeout",
			Description: "The fix stage's own wall-clock ceiling.",
			Group:       GroupReviews,
			Type:        TypeDuration,
			Format:      FormatDuration,
		},
	},
	{
		key: "governance.tiers.trivial.expected_usd",
		m: Meta{
			Label:       "Trivial tier cost",
			Description: "What a run of the trivial tier is expected to cost, which is an expectation and not a cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.trivial.expected_minutes",
		m: Meta{
			Label:       "Trivial tier minutes",
			Description: "How long a run of the trivial tier is expected to take.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.trivial.max_subagents",
		m: Meta{
			Label:       "Trivial tier sub-agents",
			Description: "The most distinct sub-agents one stage of a trivial run may start.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxGovernanceSubagents), 10),
		},
	},
	{
		key: "governance.tiers.small.expected_usd",
		m: Meta{
			Label:       "Small tier cost",
			Description: "What a run of the small tier is expected to cost, which is an expectation and not a cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.small.expected_minutes",
		m: Meta{
			Label:       "Small tier minutes",
			Description: "How long a run of the small tier is expected to take.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.small.max_subagents",
		m: Meta{
			Label:       "Small tier sub-agents",
			Description: "The most distinct sub-agents one stage of a small run may start.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxGovernanceSubagents), 10),
		},
	},
	{
		key: "governance.tiers.medium.expected_usd",
		m: Meta{
			Label:       "Medium tier cost",
			Description: "What a run of the medium tier is expected to cost, which is an expectation and not a cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.medium.expected_minutes",
		m: Meta{
			Label:       "Medium tier minutes",
			Description: "How long a run of the medium tier is expected to take.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.medium.max_subagents",
		m: Meta{
			Label:       "Medium tier sub-agents",
			Description: "The most distinct sub-agents one stage of a medium run may start.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxGovernanceSubagents), 10),
		},
	},
	{
		key: "governance.tiers.large.expected_usd",
		m: Meta{
			Label:       "Large tier cost",
			Description: "What a run of the large tier is expected to cost, which is an expectation and not a cap.",
			Group:       GroupReviews,
			Type:        TypeFloat,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.large.expected_minutes",
		m: Meta{
			Label:       "Large tier minutes",
			Description: "How long a run of the large tier is expected to take.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
		},
	},
	{
		key: "governance.tiers.large.max_subagents",
		m: Meta{
			Label:       "Large tier sub-agents",
			Description: "The most distinct sub-agents one stage of a large run may start.",
			Group:       GroupReviews,
			Type:        TypeInt,
			Min:         "0",
			Max:         strconv.FormatInt(int64(config.MaxGovernanceSubagents), 10),
		},
	},
}
