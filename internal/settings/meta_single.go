package settings

import (
	"maps"
	"slices"
	"strconv"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// meta_single.go is SM4 of docs/console-improvements/implementation-plan.md:
// the metadata of the keys that belong to no family of their own — `budget.`'s
// caps and its mode, the project's base branch and its parallel limit, this
// machine's own update check and commit identity, the account keys a run's
// model calls go out on, the triage switch, the review-against-task switch's
// local carrier, the planning pass's switch and the phone admissions block.
// The families beside it are the other three backfills' own files (SM1 to
// SM3), and D2908 is why every writable key has an entry somewhere in the
// table.
//
// The review-against-task carrier and the phone block are the two entries
// SMC's own coverage test found: SM4's brief names the twelve keys beside them
// and neither of the two is one of the four backfills' families, so the table
// covered every key of every family and no key of neither.
//
// A bound here is the loader's own constant and not a number written out: what
// the console's number input allows and what settingsReparse accepts part
// company the moment one of them is copied, so pricing.MaxUSD is the maximum
// both budget caps carry, and budget.MinGrace and budget.MaxGrace are the two
// ends `unreachable_grace` is held to (both are the bounds the job itself
// enforces). The entries are proven against the strict decode by
// TestSettingsMetaSingleBoundariesAreAccepted in internal/cli, which is where
// settingsReparse lives.
//
// Two keys of the family are lists — budget.allowed_models, whose members are
// model IDs, and verifier_accounts, whose members are addresses. The type
// vocabulary has no list member (SM0's MetaTypes), and none is needed: a block
// a person edits with --add or --add-entry is drawn read-only with the command
// to copy (reconciliation.md R5), so the type of an entry there states the
// shape of one member rather than the control the key is edited with.
var singleMeta = map[string]Meta{
	// The budget block's six keys are one group, and D1179 is why the generic
	// write path carries all of them. per_run_usd's 0 is a value and not an
	// absence: it is how a file says "no cap of this layer's", which is why the
	// floor is 0 rather than the smallest cap that spends ($0.000001, which is
	// what a figure above zero rounds to).
	"budget.per_run_usd": {
		Label:       "Per-run budget",
		Description: "The most one run may spend, in US dollars, and 0 sets no cap.",
		Group:       GroupBudget,
		Type:        TypeFloat,
		Min:         "0",
		Max:         strconv.Itoa(pricing.MaxUSD),
	},
	"budget.per_day_usd": {
		Label:       "Daily budget",
		Description: "The most this repository may spend in one UTC day, in US dollars, and 0 sets no cap.",
		Group:       GroupBudget,
		Type:        TypeFloat,
		Min:         "0",
		Max:         strconv.Itoa(pricing.MaxUSD),
	},
	"budget.max_run_tokens": {
		Label:       "Tokens per run",
		Description: "The most tokens one run may spend, and 0 sets no ceiling.",
		Group:       GroupBudget,
		Type:        TypeInt,
		Min:         "0",
	},
	"budget.allowed_models": {
		Label:       "Allowed models",
		Description: "The only models a run may call, each named by its explicit model ID.",
		Group:       GroupBudget,
		Type:        TypeString,
	},
	"budget.unreachable_grace": {
		Label:       "Budget outage grace",
		Description: "How long a run keeps going while the budget backend is unreachable.",
		Group:       GroupBudget,
		Type:        TypeDuration,
		Min:         budget.MinGrace.String(),
		Max:         budget.MaxGrace.String(),
	},
	// The mode is the one enum of the family: loosest to strictest, which is
	// the order a select draws them and the order policy modeRank ranks them.
	// `enforce` is refused by the loader unless a per-run cap stands beside it
	// (localcfg's validateBudget), which is a rule about the pair rather than
	// about this value, and it is the boundary test's own document that
	// carries the pair.
	"budget.mode": {
		Label:       "Budget mode",
		Description: "What the gateway does with a run's calls: nothing, price them, or refuse one over a cap.",
		Group:       GroupBudget,
		Type:        TypeEnum,
		Enum:        []string{"off", "observe", "enforce"},
	},
	// The bake switch is this machine's own (D1901) and not a repository's: it
	// says whether a job's image carries the repositories the build can read.
	"bake_repos": {
		Label:       "Bake repositories into images",
		Description: "Whether a repository the build can read is baked into the image its job runs.",
		Group:       GroupMachine,
		Type:        TypeBool,
	},
	"base_branch": {
		Label:       "Default base branch",
		Description: "The branch a run starts from when a repository names none.",
		Group:       GroupRepositories,
		Type:        TypeString,
		Format:      FormatBranch,
	},
	"max_parallel": {
		Label:       "Runs at once",
		Description: "How many runs this machine may have running at the same time.",
		Group:       GroupRuns,
		Type:        TypeInt,
		// 0 is refused here and accepted by the loader, which reads it as the
		// default (localcfg's applyDefaults fills DefaultMaxParallel over it):
		// the floor is the lowest figure a person sets, and a person who wants
		// the default removes the key rather than writing a 0 over it.
		Min: "1",
	},
	"update_check": {
		Label:       "Update check",
		Description: "Whether Fugaro's daily update check reports a newer release.",
		Group:       GroupMachine,
		Type:        TypeBool,
	},
	// The two identity keys travel with a run only when its repository opted in
	// (git.author: launcher, D604, D606). commit_email's own rule at the launch
	// is D649's — the launcher accepts it only when it is this machine's own
	// recorded address or a forge noreply one — which is a rule about the
	// person and not about the shape of an address; the shape an address has is
	// the one task.AuthorProblem holds the author to when the run is written
	// (one @, a local part and a domain), and that is the format stated here.
	"commit_name": {
		Label:       "Commit author name",
		Description: "The name a run's commits are authored with when a repository opted in.",
		Group:       GroupMachine,
		Type:        TypeString,
	},
	"commit_email": {
		Label:       "Commit author email",
		Description: "The address a run's commits are authored with when a repository opted in.",
		Group:       GroupMachine,
		Type:        TypeString,
		Format:      FormatEmail,
	},
	// The account keys are one group (spec §11.1 to §11.3, D873 to D875): what
	// a run's model calls go out on. Every one of them is this project's own
	// install document, which is what makes the credentials service's own
	// allow_data_to the only way a name is admitted anywhere else.
	"default_claude_account": {
		Label:       "Default Claude account",
		Description: "The account every stage uses when nothing more specific does.",
		Group:       GroupCredentials,
		Type:        TypeString,
	},
	"claude_account_auto": {
		Label:       "Choose accounts automatically",
		Description: "Whether Fugaro may choose an account for a run when nothing more specific does.",
		Group:       GroupCredentials,
		Type:        TypeBool,
	},
	"subscription_spread": {
		Label:       "Spread across subscriptions",
		Description: "Whether a run may spread across two or more Claude subscriptions.",
		Group:       GroupCredentials,
		Type:        TypeBool,
	},
	"verifier_accounts": {
		Label:       "Signature verifier accounts",
		Description: "The accounts that may ask the credentials service to verify a signature.",
		Group:       GroupCredentials,
		Type:        TypeString,
	},
	// The member's switch for the personal half of a workspace's credentials
	// (WC6, R9, D2972): the machine's own file states a boolean the publish
	// copies into the member's policy node, which the workspace's service — the
	// only enforcer — reads per call. It is the shape `triage` below has, and
	// its group is the credentials one because what it turns on is a person's
	// own token being served.
	"personal_workspace_tokens": {
		Label:       "Personal workspace tokens",
		Description: "Whether this project's runs may be served a person's own workspace token ahead of the shared entries.",
		Group:       GroupCredentials,
		Type:        TypeBool,
	},
	"triage": {
		Label:       "Triage review findings",
		Description: "Whether a review round's findings are judged by the triage stage.",
		Group:       GroupReviews,
		Type:        TypeBool,
	},
	// The review-against-task switch's local carrier (RA8b, R7, D2935): the
	// machine's own file states it and the publish copies it into the policy
	// node, which is the shape `triage` above has, and it is the local half of
	// the pair `agent.review_against_task` (meta_agent.go) is the repository's
	// half of.
	"review_against_task": {
		Label:       "Review against task",
		Description: "Whether this machine's review stages are shown the task the pull request was written for.",
		Group:       GroupReviews,
		Type:        TypeBool,
	},
	// The phone admissions block (D231, D2603): the entries a person declares a
	// device in. The entry pair writes it (`settings set mobile_allow
	// --add-entry`), which is why no scalar write lands on the block's own
	// spelling and the write refuses one of its leaves. The type states the
	// shape of one member — a uid, an address and a role — for the reason the
	// two lists above state theirs: the block is drawn read-only with the
	// command to copy (R5), so no control is built from this. The group is this
	// machine's own copy because the file the block lives in is (LayerLocal).
	"mobile_allow": {
		Label:       "Admitted phones",
		Description: "The phones admitted to drive this project's runs, each with a uid, an address and a role.",
		Group:       GroupMachine,
		Type:        TypeString,
	},
	// The planning pass's switch is the project's, and the group is this
	// machine's because that is where the order puts it (SM0's GroupMachine):
	// the panel's groupings are the owner's answer and not this file's.
	"planning.decoupling": {
		Label:       "Decoupled planning",
		Description: "Whether the planning pass breaks a dependency edge with a contract task.",
		Group:       GroupMachine,
		Type:        TypeBool,
	},
}

// init registers the family, which is where a table this file got wrong is
// caught: a key named twice, or an entry Register refuses, is a programming
// error and stops the binary rather than a screen, as the writers table does
// for RegisterWriter. The keys are walked in order so that the first refusal a
// broken table produces is the same one every run.
func init() {
	for _, key := range slices.Sorted(maps.Keys(singleMeta)) {
		if err := Register(key, singleMeta[key]); err != nil {
			panic(err)
		}
	}
}
