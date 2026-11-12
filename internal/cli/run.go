package cli

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// Launch timing. claimWait and claimPoll are variables so tests can shrink them.
const (
	claimTTL      = runstore.ClaimTTL
	launchTimeout = 2 * time.Minute // well under claimTTL: a hung RunJob can't outlive its claim
	// holdTimeout bounds everything done while holding the claim (the
	// re-check, Launch and the launch.json write), so a claim is never
	// judged stale by another CLI while its holder is still at work.
	//
	// Clock assumption: a claim's age is this machine's clock minus the
	// holder's (Claim.At). Every CLI's clock must be within
	// claimTTL - holdTimeout (5m) of every other's, or a live claim can be
	// judged stale and taken over. The object's server-side update time
	// would narrow that to this machine against Cloud Storage, not remove
	// it, and is not used. If the assumption fails, the result is a double
	// launch, which the runner's duplicate-execution check contains: the
	// second execution finds the first's record and exits writing nothing
	// (design §4.7).
	holdTimeout = claimTTL / 2
)

var (
	claimWait = 30 * time.Second // how long a loser waits for the winner's launch.json
	claimPoll = time.Second
)

// launchRecipe is the recipe a launch said it runs.
type launchRecipe struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// launchResult is what fugaro run reports, and its --json output.
type launchResult struct {
	Project string `json:"project"` // the Fugaro project
	Run     string `json:"run"`
	Repo    string `json:"repo"`
	RunID   string `json:"run_id"`
	Branch  string `json:"branch"`
	// Ref is the ref a new run's branch is cut from, which its review
	// measures the run's work against; empty for a follow-up and a review
	// run, whose review reads each pull request's own base.
	Ref       string `json:"ref,omitempty"`
	Execution string `json:"execution"`
	LogURL    string `json:"log_url,omitempty"`
	Status    string `json:"status"` // launched | already-launched
	// PR and PreviousRun are a follow-up's pull request and the run it
	// continues; Branch is then the PR's branch.
	PR          int    `json:"pr,omitempty"`
	PreviousRun string `json:"previous_run,omitempty"`
	// Recipe is the run's recipe; nil when the runner chooses it (no
	// --recipe and no checkout of the repository: agent.recipe at the ref).
	Recipe *launchRecipe `json:"recipe,omitempty"`
	// ProjectLayer is the project layer the stored task actually carries,
	// nil when none applies (task.ProjectLayer, embedProjectLayer).
	ProjectLayer *launchProjectLayer `json:"project_layer,omitempty"`
	// Pair is the A/B pair this launch sampled (A3.0), nil for a launch that
	// sampled none; Challenger is the challenger's run id, empty until that
	// arm has started, and Reason is why nothing was sampled when a planner
	// was asked and declined.
	Pair *launchPair `json:"pair,omitempty"`
	// account is the account this launch resolved and the alias it read it
	// through, which the launch's own line prints as ALIAS -> ACCOUNT
	// (docs/account-aliases/spec.md §7, D2099). It is unexported on purpose:
	// it is the launcher's own display fact, the document above is the
	// contract a consumer reads, and the task — the record — carries the
	// resolved account alone and no alias at all (F4).
	account launchAccount
}

// launchAccount is the Claude account a launch resolved and the alias it read
// it through. Alias is "" when the flag or the key named the account itself, or
// when the name came from a place no alias lives (the committed
// agent.oauth_token, D2090), which is what makes the launch's own line print the
// pair only where an alias really decided it.
type launchAccount struct {
	Name  string
	Alias string
}

// launchPair is launchResult's --json view of the A/B decision this launch
// made: the pair's id and the one batch label both arms carry, the challenger's
// run once that arm has started, and — for a launch that sampled nothing — the
// reason it did not (D556: the closed reason a person reads on stderr is in
// --json too).
type launchPair struct {
	ID         string `json:"id,omitempty"`
	Batch      string `json:"batch,omitempty"`
	Challenger string `json:"challenger,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// launchProjectLayer is launchResult's --json view of a task's project
// layer: just enough to tell which one it was, never its text.
type launchProjectLayer struct {
	SHA256     string `json:"sha256"`
	Generation int64  `json:"generation,omitempty"`
}

// launchHooks lets tests stop launchRun between its steps, to make the
// races of design §4.7 deterministic. Production code never sets them.
var launchHooks struct {
	beforeClaim    func() // after the first launch.json/result.json check
	beforeRead     func() // after a failed Claim, before reading the held claim
	beforeTakeover func() // after reading a stale claim, before replacing it
	afterClaim     func() // after taking the claim, before the re-check
}

func hook(f func()) {
	if f != nil {
		f()
	}
}

type runOptions struct {
	cloud                                 cloudOptions
	repo, ref, workflow, runID, batch, tf string
	retry                                 string
	totalTimeout                          string
	// pr is --pr, repeated: a single entry continues Fugaro's own pull
	// request N (the only meaning before Task 12 of the generic-tool
	// plan); 2 to 4 need a mode: review recipe such as pick (G13).
	pr []int
	// reviewRun is --review-run: the independent review run whose findings
	// this follow-up fixes (CSR5, D887). It is what makes a `fix first`
	// verdict reach the fix round's brief without a person copying anything
	// between the two runs — the launcher reads the review's own artifact
	// (reviewFindings) and prepends its issues to the follow-up's
	// instructions, so the fix round answers a review it names. Empty (the
	// default) is no claim at all: the brief is the launcher's TEXT alone,
	// exactly as before the flag existed. Refused without --pr (there is no
	// pull request to fix) and with a mode: review recipe (the launch is a
	// review, not a fix), and refused rather than launched when the
	// artifact is missing or unreadable: a brief that says nothing about
	// why it was launched is worse than no run (spec §6).
	//
	// The flag is HIDDEN, and that is a debt rather than a choice: the
	// generated CLI reference (website/content/cli-reference.md) is written
	// by another work stream and this task may not edit it, and a visible
	// flag on a documented command fails the drift tests that hold the two
	// together. Hiding it keeps the tree green without a second copy of the
	// fact; the release that owns that file documents it (reconciliation
	// F22/E4).
	reviewRun string
	// ownerRuled is --owner-ruled: the repository owner's answer to the
	// findings a run on this PR left for them (OD1). A follow-up whose
	// previous run stopped waiting on owner decisions is refused without it
	// (resolvePR), so a person's answer is what unblocks the pull request
	// rather than another blind run; the text is stored on the task
	// (task.Spec.OwnerRuled) and appended to the brief verbatim by the
	// runner. Empty is no ruling at all, which is every launch that is not
	// answering one.
	ownerRuled string
	// attach is --attach, repeated: files this launch uploads into the
	// run's own inputs/ for a mode: artifact run to download (design
	// artifact-runs.md §4, plan Task 6). It is refused outright on a run id
	// that already holds a task, refused unless the launch resolves to an
	// artifact recipe, and refused over any of the workflow's three
	// inputs caps before the first byte is uploaded.
	attach        []string
	asJSON        bool
	noBudgetCheck bool
	recipe        string
	// attempts is --attempts: 2 to 4 launches a best-of-N batch of
	// independent runs of the same task, workflow, ref and recipe (G13).
	// It is refused together with --pr, --run-id, --retry or a
	// review-mode recipe (the last already refused by chooseRecipe, since
	// a batch never sets --pr).
	attempts int
	// title is --title, as the task stores it: the run's own job title
	// (task.Spec.Title), the one line every surface that names a run shows
	// — fugaro watch's rows, the budget registry's entry, the pull
	// request's title, runs ls. runRun validates and sanitises it once
	// (titleFlag), so every spec builder below writes the same value and
	// the follow-up comparison reads it back. Empty (the default) is no
	// title at all: the task prompt's first line is the title, exactly as
	// before the flag existed. A follow-up (--pr) inherits its previous
	// run's title unless this is given (newFollowUpSpec).
	title string
	// token is the named Claude account (docs/design/named-oauth-tokens.md)
	// this run uses: fugaro run --token. A follow-up (--pr) inherits its
	// previous run's account when this is empty; an explicit value overrides
	// it instead (owner ruling, 2026-10-12, task 9b review), through the
	// same allow-list, agent.auth and version-gate checks a first run's does.
	// Either name may be an alias of the owner's local config, and what the
	// launch resolves it to is account below.
	token string
	// account is what the launch resolved its own Claude account to and the
	// alias it read it through, set by the spec builder at the one place the
	// resolution happens (claudeAccountChoice): the pair the launch's own
	// line prints as ALIAS -> ACCOUNT (docs/account-aliases/spec.md §7,
	// D2099). Alias is "" when a flag or a key named the account itself, so a
	// launch that read no alias prints nothing new. It is the launcher's own
	// display fact and reaches no other reader: the task is the record, and
	// it carries the resolved account alone (F4).
	account launchAccount
	// planTask is --plan-task, as the task stores it: the id of one task in
	// one plan ("setup-wizard/W1.3"), recorded so the console can join this
	// run to a node of that plan's dependency graph (console dependency
	// graph plan, task GV1.2, decision GD5). runRun validates it once
	// (planTaskFlag), so every spec builder below writes the same value and
	// the repeat comparison reads it back. Empty (the default) is no claim
	// at all: the launch writes no plan_task field anywhere, exactly as
	// before the flag existed. A follow-up (--pr) carries one only when this
	// is given: it never inherits its previous run's, and is otherwise
	// joined to the graph through its pull request number (GV1.6).
	planTask string
	// rigor is --mode: the rigor mode this run's own launcher asks for
	// (docs/rigor-modes implementation-plan T1.6). It is the one input that
	// may raise the mode the run resolves from its configuration and never
	// lower it (D6), so a launcher that says nothing cannot outvote a
	// repository that configured one. Empty (the default) is no request at
	// all: the launch resolves from its layers alone, exactly as it did
	// before the flag existed, and its task carries no such field. Kept as
	// the flag's own string and validated against the vocabulary once, in
	// rigorFlags, because a value outside it is a launcher's mistake rather
	// than a run's opinion.
	rigor string
	// tier is --tier: the expectation tier this run's own launcher dispatches
	// it at (docs/task-governance spec §3, implementation-plan T8.1): how much
	// the run is expected to cost and take, which the runner records with the
	// expectation the governance resolved for it. Empty (the default) is no
	// expectation at all: the launch writes no such field and the record
	// carries no tier block. Kept as the flag's own string and validated
	// against the vocabulary once, in tierFlag, because a value outside it is
	// a launcher's mistake rather than a run's opinion about its own size.
	tier string
	// noCompression is --no-compression: turn the repository's
	// agent.compression off for this run only (docs/design/
	// headroom-compression.md §4). The override is deliberately
	// one-directional — there is no --compression flag — because turning
	// compression on is a reviewed fugaro.yaml change, and a flag that could
	// force it on from the command line would undo that for every workflow a
	// launcher can already run. It writes task.Spec.CompressionOff when the
	// resolved configuration turns compression on: with compression already
	// off there is nothing to override, and the field would only add version
	// skew (an older runner refuses the whole task). A configuration the
	// launcher could not read leaves nothing to judge, so the launch is
	// refused rather than guessed at (finishLaunch).
	noCompression bool
	// paths is --path, repeated: the paths this launch declares it will
	// touch, which the runner matches against the area rules at D22's first
	// moment (bootstrap). Nil (the default) is no declaration, and a
	// declared path that matches no rule changes nothing. A path that does
	// match replaces the repository's value with the area's, which may be
	// lower: areas are defaults, not floors (docs/project-layer.md §2), and
	// finalize resolves again from the paths the run really changed.
	paths []string
	// reviewLevelsPath is --review-levels: a YAML document naming the review
	// levels this run asks for (D724), or - for stdin. Empty (the default)
	// is no request at all: the launch writes no field and the run resolves
	// its levels from its own configuration, exactly as before the flag
	// existed.
	reviewLevelsPath string
	// reviewLevels is reviewLevelsPath's document, decoded once
	// (reviewLevelsFlag) into the block the task stores, or nil when the
	// flag is not given. Kept parsed rather than as text so the refusal a
	// near miss gets is this package's (config.ParseReviewLevelsYAML), not
	// the runner's at bootstrap after the launch was paid for.
	reviewLevels *config.ReviewLevels
	// The four chain limits (docs/run-limit-carry-over D1042), one flag per
	// dimension and one member per flag: --max-usd, --max-tokens, --max-time
	// and --max-review-rounds. They are the flags' own text here and are
	// parsed once, by chainLimitsFlag, into limits below, so every launch
	// shape writes the same four numbers.
	maxUSD, maxTokens, maxTime, maxReviewRounds string
	// limits is the four flags above, parsed. A launch that names none of
	// them carries no chain limit at all and its task is byte-identical to
	// what the previous release wrote (D1041).
	limits chainLimits
	// continues is --continues: the run whose chain this launch joins when
	// nothing else names one (D1045) — the flag a chain with no pull request
	// is continued by. It is refused with the three flags that name a chain's
	// previous run themselves (--pr, --retry, --attempts).
	continues string
	// The four --used-* figures (D1048), one per dimension, the flags' own
	// text here and parsed once by chainContinueFlag into used below. Like the
	// four --max-* ones they are members of runOptions rather than of
	// chainLimits: a caller's statement of what a chain has already spent is
	// not a limit the merge judges (a limit may only tighten, and this one may
	// only raise a charge), and only the chain half of a launch reads it.
	usedUSD, usedTokens, usedTime, usedReviewRounds string
	// used is the four figures above, parsed.
	used chainUsedFigures
}

func newRunCmd() *cobra.Command {
	var o runOptions
	cmd := &cobra.Command{
		Use:   "run [TEXT]",
		Short: "Launch a task in the cloud; it always ends with a PR",
		Long: "Launch a task in the cloud. The task is TEXT or --task-file (- for stdin).\n\n" +
			"A repeated --run-id reports the existing launch instead of starting another, so\n" +
			"a caller that is unsure whether its launch went through can simply repeat it.\n" +
			"--retry RUN launches a run whose task was stored but never started.\n\n" +
			"--pr N continues Fugaro's pull request N: the run acts on the PR's review comments\n" +
			"by the authors fugaro.yaml's followup.trusted lists, on the PR's own branch, with\n" +
			"the base branch and workflow of the runs before it. TEXT, or --task-file, is\n" +
			"optional then, and adds instructions.\n\n" +
			"--recipe NAME --pr N, NAME naming a mode: review recipe such as review-only or pick,\n" +
			"reviews any open pull request of the repository instead (not only Fugaro's own) and\n" +
			"never pushes or opens one; repeat --pr (2 to 4 times) for pick, which compares them.\n" +
			"TEXT, or --task-file, is optional then too, and adds instructions to the review.\n\n" +
			"--attempts N (2 to 4) launches N independent runs of the same task as a best-of-N\n" +
			"batch, under one batch label (bestof-<first run id> unless --batch is given); once\n" +
			"they have all opened a pull request, compare them with fugaro run --recipe pick.\n" +
			"It takes no --pr, --run-id or --retry.\n\n" +
			"--title TEXT names the run: one line, at most 100 characters. It is what\n" +
			"fugaro watch and runs ls list the run by, and the title (with the body's first\n" +
			"heading) of the pull request the run opens when the agent writes no pr.md of\n" +
			"its own. Without it the run is named by the task's first line, so briefs that\n" +
			"start with the same preamble all read alike. A follow-up (--pr) keeps its run's\n" +
			"title unless --title is given, and --retry takes none (the stored task keeps its\n" +
			"own).\n\n" +
			"--plan-task PLAN/TASK records which task of which plan this run works on, so\n" +
			"the console's graph can join the run to that node. One plan and one task id,\n" +
			"as the plan's own graph spells them, at most 128 bytes; what the run does is\n" +
			"unchanged by it. A follow-up (--pr) records one only when it is given, and\n" +
			"--retry takes none (the stored task keeps its own).\n\n" +
			"--no-compression turns the repository's agent.compression off for this run only,\n" +
			"overriding its fugaro.yaml. There is no --compression flag: turning compression\n" +
			"on is a reviewed fugaro.yaml change. A follow-up (--pr) keeps its run's choice,\n" +
			"and --retry takes none (the stored task keeps its own).\n\n" +
			"--mode MODE asks for a rigor mode for this run: " + rigor.ModeList() + ".\n" +
			"It may raise the mode the run resolves from its own configuration\n" +
			"(rigor.default and rigor.areas in fugaro.yaml, the project layer's defaults)\n" +
			"and never lower it, so a launch that asks for less cannot outvote a repository\n" +
			"that asked for more. A follow-up (--pr) keeps the mode the PR's runs recorded\n" +
			"and may only raise it: asking for less is refused, and so is hotfix, which\n" +
			"only an operator's own run reaches. --path PATH declares a path this run will\n" +
			"touch, repeatable, matched against those area rules before the run starts;\n" +
			"without either flag a first run resolves from its configuration alone, and a\n" +
			"follow-up from that and the mode it keeps. Both are\n" +
			"recorded in the run's record and named in the pull request's status line.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRun(cmd, &o, args) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "repository as owner/name, or a path inside its checkout (default: this checkout's origin)")
	f.StringVar(&o.ref, "ref", "", "base branch or ref the run is cut from (default: the branch this checkout is on, when origin has it and it is not the base branch; else the base branch (fugaro.yaml git.base_branch, then the local config's, then the project's base_branch, then main))")
	f.StringVar(&o.workflow, "workflow", "", "workflow to run (default: the repo's only workflow)")
	f.StringVar(&o.runID, "run-id", "", "run ID to use; repeating one reports its launch instead of starting another")
	f.StringVar(&o.batch, "batch", "", "batch label, to group runs in fugaro runs ls")
	f.StringVar(&o.title, "title", "", "the run's title, one line: what fugaro watch and runs ls list the run by, and the pull request's title unless the agent wrote its own pr.md (default: the task's first line); a follow-up (--pr) keeps its run's title unless this is given")
	f.StringVar(&o.planTask, "plan-task", "", "record the plan task this run works on, as <plan>/<task> (for example setup-wizard/W1.3): the id the console's graph joins this run to a node by; it changes nothing about what the run does, and a follow-up (--pr) records one only when given")
	f.StringVar(&o.tf, "task-file", "", "read the task from this file, or - for stdin")
	f.StringVar(&o.retry, "retry", "", "launch the stored task of RUN (a full run ID, or <repo-slug>/<run-id>) that never started: a run an ending names is never launched, since the bucket cannot vouch for which run an ending means")
	f.StringVar(&o.totalTimeout, "total-timeout", "", "override the workflow's timeouts.total for this run, such as 45m")
	f.StringVar(&o.maxUSD, "max-usd", "", "cap this chain's model spend at X dollars, such as 5 or 12.5: it may only tighten the ceiling this launch resolves (fugaro refuses a looser value and names the layer that set the lower one), and it is recorded in the task's overrides, beside the total_timeout that is there today. A chain is the run that sets it and the runs that continue it; a launch that names no such limit gets today's per-run caps and no chain at all")
	f.StringVar(&o.maxTokens, "max-tokens", "", "cap this chain's token total at N tokens, such as 900000: tighten-only, like --max-usd, and recorded in the task's overrides")
	f.StringVar(&o.maxTime, "max-time", "", "cap this chain's elapsed run time, as a duration such as 45m or 2h: --total-timeout keeps its own meaning and may raise this run's clock, and the two compose as the smaller of the two (the run's clock is min(--total-timeout, this))")
	f.StringVar(&o.maxReviewRounds, "max-review-rounds", "", "cap this chain's review rounds at N, senior-review iterations included: tighten-only, and never refused against the mode's own number — the rounds a run spends stay the mode's and the recipe's, and this bounds the whole chain")
	addChainContinueFlags(cmd, &o)
	f.IntSliceVar(&o.pr, "pr", nil, "continue Fugaro PR N: act on its trusted review comments (TEXT adds instructions); with a mode: review --recipe, review PR N instead (repeatable, 1 to 4, any open PR)")
	f.StringVar(&o.recipe, "recipe", "", "the recipe (task loop) to run: .fugaro/recipes/NAME.yaml, the project's, or the catalog's ("+strings.Join(recipe.CatalogNames(), ", ")+"); default: agent.recipe in fugaro.yaml, else default; a follow-up (--pr) keeps its previous run's")
	f.StringVar(&o.token, "token", "", "named Claude account (claude_accounts in the local config) this run uses, for a workflow whose agent.auth is oauth; default: agent.oauth_token in fugaro.yaml, else the repository's default account; a follow-up (--pr) keeps its previous run's account unless this is given explicitly, which overrides it (checked the same way as a first run's)")
	f.StringArrayVar(&o.attach, "attach", nil, "attach a file to the run: repeatable, uploaded to the run's own inputs/ and downloaded by a mode: artifact run into the checkout's inputs/ (needs a mode: artifact recipe; refused on a run ID that already has a task)")
	f.StringVar(&o.rigor, "mode", "", "the rigor mode to ask for: "+rigor.ModeList()+"; it may raise the mode the run resolves from its configuration (rigor.default, rigor.areas, the project layer) and never lower it; a follow-up (--pr) keeps the mode the PR's runs recorded and may only raise it, and takes no hotfix; without it a first run resolves from those layers alone, and a follow-up from those and the mode it keeps")
	f.StringArrayVar(&o.paths, "path", nil, "declare a path this run will touch, matched against the rigor.areas rules in effect (the repository's, and the project layer's beside them): repeatable; a run that declares none matches no area rule")
	f.StringVar(&o.tier, "tier", "", "the expectation tier to dispatch this run at: "+tier.List()+". It is what the run is expected to cost and take (governance.tiers in fugaro.yaml and the project layer), and never a cap: the run's own budget cap stays the ceiling. A --pr follow-up keeps the tier its PR's runs recorded; without this flag a run carries no expectation at all")
	f.StringVar(&o.reviewLevelsPath, "review-levels", "", "read the run's own review levels from this YAML file, or - for stdin: the block the task stores as review_levels (l1, l2, l3, each of model, effort, none and focus), decoded strictly, with no key on no level meaning the configuration decides it. Asking for levels needs a job image whose runner acts on them, so a launch on an older one is refused and names fugaro image refresh; without the flag the run resolves its levels from its configuration and its mode alone")
	// Hidden: see the field's own note on the generated reference.
	f.StringVar(&o.reviewRun, "review-run", "", "carry the findings of review RUN (<repo-slug>/<run-id> or a run ID) into this fix round's brief; needs --pr, and refused with a mode: review recipe")
	_ = f.MarkHidden("review-run")
	f.StringVar(&o.ownerRuled, "owner-ruled", "", "the repository owner's ruling on the findings a run on this PR left for them (OD1): either the text itself, a file, or - for stdin. A follow-up on a pull request whose last run stopped waiting on owner decisions is refused without it, and one whose last run left no such decisions is refused with it: the ruling answers what that run left, and nothing else; it is appended to this run's brief verbatim and recorded in the run")
	f.IntVar(&o.attempts, "attempts", 0, "launch N (2 to 4) independent runs of the same task, workflow, ref and recipe as a best-of-N batch (bestof-<first run id> unless --batch is given); refused with --pr, --run-id, --retry or a review-mode recipe")
	f.BoolVar(&o.noCompression, "no-compression", false, "turn agent.compression off for this run only, overriding fugaro.yaml; there is no flag to turn it on, which is a reviewed fugaro.yaml change")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.noBudgetCheck, "no-budget-check", false, "skip the launch pre-check of the budget (kill switches, missing caps, no headroom); the run is held to the same limits when it starts")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// taskFlags are the flags --retry excludes: the stored task.json supplies them.
var taskFlags = []string{"repo", "ref", "workflow", "run-id", "batch", "title", "plan-task", "task-file", "total-timeout", "pr", "recipe", "token", "attach", "mode", "path", "tier", "no-compression", "review-levels", "review-run", "max-usd", "max-tokens", "max-time", "max-review-rounds", "continues", "used-usd", "used-tokens", "used-time", "used-review-rounds"}

// prFlags are the flags --pr excludes: the previous run on the PR supplies them.
var prFlags = []string{"ref", "workflow"}

func runRun(cmd *cobra.Command, o *runOptions, args []string) error {
	ctx := cmd.Context()
	attemptsSet := cmd.Flags().Changed("attempts")
	if attemptsSet {
		if o.attempts < 2 || o.attempts > 4 {
			return userErr("--attempts: use 2 to 4, got %d", o.attempts)
		}
		for _, name := range []string{"pr", "run-id", "retry", "continues"} {
			if cmd.Flags().Changed(name) {
				return userErr("--attempts launches a fresh batch; it takes no --%s", name)
			}
		}
		// A best-of-N batch is N chains of one run each (D1053), so one
		// --used-* figure would have to be charged to all N of them or to
		// none, and neither is what a caller stating one figure means. The
		// flags the batch does not take are named here, beside the ones that
		// name a previous run.
		for _, name := range []string{"used-usd", "used-tokens", "used-time", "used-review-rounds"} {
			if cmd.Flags().Changed(name) {
				return userErr("--attempts launches a fresh batch of chains; it takes no --%s", name)
			}
		}
	}
	prSet := cmd.Flags().Changed("pr") && o.retry == ""
	if prSet {
		if len(o.pr) == 0 {
			return userErr("--pr: use a pull request number")
		}
		if len(o.pr) > 4 {
			return userErr("--pr: at most 4 may be given")
		}
		for _, n := range o.pr {
			if n <= 0 {
				return userErr("--pr %d: use a pull request number", n)
			}
		}
	}
	// --pr names the chain's previous run itself, by the pull request the
	// chain is linkable through, and --continues names one directly (D1045):
	// two answers to one question are refused rather than merged, since the
	// second would either be ignored or read a chain the first never named.
	// --retry does not reach here (it takes no --continues, taskFlags) and
	// --attempts is refused above.
	if prSet && o.continues != "" {
		return userErr("--continues names the run this launch continues, and --pr names the pull request whose chain it joins; pass one of them")
	}
	var text string
	var total time.Duration
	if o.retry != "" {
		for _, name := range taskFlags {
			if cmd.Flags().Changed(name) {
				return userErr("--retry launches the stored task as it is; it takes no --%s", name)
			}
		}
		if len(args) > 0 {
			return userErr("--retry launches the stored task as it is; it takes no TEXT")
		}
	} else {
		var err error
		// --title, resolved once for every launch shape below (a first run,
		// a best-of-N batch, a review, a follow-up): o.title then holds the
		// title exactly as the task stores it, or "" for no title at all.
		if err = titleFlag(cmd, o); err != nil {
			return err
		}
		// --plan-task, resolved once for every launch shape below, like the
		// title: o.planTask then holds the id exactly as the task stores it,
		// or "" for no claim at all.
		if err = planTaskFlag(cmd, o); err != nil {
			return err
		}
		// --mode and --path, resolved once for every launch shape below, as
		// --title is: a request the launcher accepts here is the one every
		// spec builder writes, and a refused one leaves nothing behind.
		if err = rigorFlags(o); err != nil {
			return err
		}
		// --tier, resolved the same way and for the same reason: the
		// vocabulary is closed, and a launch that names none writes no field.
		if err = tierFlag(o); err != nil {
			return err
		}
		// The four --max-* chain limits, parsed once for every launch shape
		// below and before the cloud is opened: a mistyped number is a
		// caller's mistake, and it costs no read. A value that would loosen
		// is a different refusal, and it needs the policy this launch
		// resolves (chainLimitGate). --retry never reaches here: taskFlags
		// refuses the four with it, since the stored task keeps its own.
		if err = chainLimitsFlag(cmd, o); err != nil {
			return err
		}
		// --continues and the four --used-* figures, parsed once here and for
		// the same reason (chainContinueFlag): the syntax is this launcher's
		// own mistake to refuse, and whether a figure is above what the chain
		// already used needs the chain read (chainInherit). --retry never
		// reaches here either: taskFlags refuses the five with it.
		if err = chainContinueFlag(cmd, o); err != nil {
			return err
		}
		if text, err = taskText(cmd, o.tf, args, prSet); err != nil {
			return err
		}
		// --review-levels, after the task text: - reads stdin, and the task
		// may not have it too (an empty document would be the only thing the
		// levels read got). Resolved here, before openCloud, so a document
		// this launcher refuses costs no cloud call and leaves nothing.
		if o.reviewLevelsPath == "-" && o.tf == "-" {
			return userErr("--review-levels - and --task-file - both read stdin; pass one of them as a file")
		}
		if err = reviewLevelsFlag(cmd, o); err != nil {
			return err
		}
		// --owner-ruled, read here with the rest of the launch's text: the
		// task may not read stdin twice, and the ruling is stored on the task
		// (task.Spec.OwnerRuled) rather than kept beside it.
		if o.ownerRuled == "-" && (o.tf == "-" || o.reviewLevelsPath == "-") {
			return userErr("--owner-ruled - and another flag both read stdin; pass one of them as a file")
		}
		if o.ownerRuled, err = ownerRuledText(cmd, o.ownerRuled); err != nil {
			return err
		}
		if o.totalTimeout != "" {
			if total, err = parseTotalTimeout(o.totalTimeout); err != nil {
				return err
			}
		}
	}
	// --review-run carries a review's findings into a FIX ROUND's brief, and
	// a fix round is a follow-up: without --pr there is no pull request to
	// fix, and a launch that accepted the flag anyway would start a run whose
	// brief carries nothing while reporting the review as answered. Checked
	// here, before openCloud, so the refusal costs no cloud call; the other
	// half of the rule — a mode: review recipe is a review and not a fix — is
	// checked below, where the recipe is resolved.
	if cmd.Flags().Changed("review-run") && !prSet {
		return userErr("--review-run needs --pr: it carries a review run's findings into a fix round of a pull request")
	}
	// --owner-ruled answers findings a run on a pull request left (OD1), so
	// it is a follow-up's flag and nothing else: refused here, before
	// openCloud, the way --review-run is.
	if cmd.Flags().Changed("owner-ruled") && !prSet {
		return userErr("--owner-ruled needs --pr: it answers the findings the last run on that pull request left for you")
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	env.noBudgetCheck = o.noBudgetCheck

	// --repo, resolved once for every launch shape below and written back, so
	// that a path (`.`, a subdirectory, an absolute one) is the owner/name of
	// its checkout's origin everywhere the option is read: the review branch,
	// review-run's findings, and the spec builders (newSpec, prSpec, retrySpec
	// and reviewSpec each read o.repo for themselves). Not higher, because the
	// flags above refused the launch before this ever ran, and their sentences
	// are still the ones a caller gets.
	if o.repo, err = repoArg(ctx, o.cloud, o.repo); err != nil {
		return err
	}

	// --pr's second meaning (design generic-tool §3.6, G12): with an
	// explicit --recipe naming a mode: review recipe, --pr targets any
	// open pull request of the repository, not only Fugaro's own, and may
	// repeat (pick compares them). Detected only from an explicit --recipe,
	// never a bare agent.recipe default: the ordinary follow-up path below
	// (prSpec) already resolves that case from the PR's own history.
	var reviewRecipe *resolvedRecipe
	if prSet {
		switch {
		case o.recipe == "":
			if len(o.pr) > 1 {
				return userErr("several --pr need a review-mode recipe such as pick")
			}
		default:
			repo := o.repo
			if repo == "" {
				if repo, err = originRepo(ctx); err != nil {
					return err
				}
			}
			o.repo = repo
			// Checked before resolveRecipe (review finding E, the same
			// ordering newSpec/prSpec already enforce internally):
			// resolveRecipe can read a project or catalog recipe from the
			// bucket, a network call a disallowed --token should never
			// trigger. reviewSpec repeats this same check once a review
			// recipe is confirmed below (it is pure and local, so doing it
			// twice costs nothing); this call is what stops the read
			// itself from ever running for a refused account.
			// The ref is not known yet here (--pr's follow-up path takes it
			// from the previous run): the account is resolved from the flag
			// and the checkout's own value, and reviewSpec resolves it again
			// from the ref it names once one is.
			if _, err := claudeAccountFor(ctx, env, repo, o.token, "", o.ref, checkoutOf(ctx, repo)()); err != nil {
				return err
			}
			rr, rerr := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), o.recipe, time.Now(), false)
			switch {
			case rerr == nil && rr.Recipe.Mode == recipe.ModeReview:
				if cmd.Flags().Changed("review-run") {
					return userErr("--review-run carries a review run's findings into a fix round, and --recipe %s reviews a pull request instead", rr.Name)
				}
				// --owner-ruled is refused here for the same reason
				// --review-run is: the ruling answers findings a run on the
				// pull request left, the runner appends it to that
				// follow-up's brief, and task.Validate holds it to a spec
				// with a branch, a pr and a previous_run — the reviewSpec
				// below builds none of the three, because a review-mode
				// launch reviews a pull request rather than continuing one.
				// Accepted, the flag would write no field anywhere and the
				// owner's answer would be dropped without a word.
				if cmd.Flags().Changed("owner-ruled") {
					return userErr("--owner-ruled answers the findings a run on a pull request left, and --recipe %s reviews a pull request instead of fixing one", rr.Name)
				}
				if lo, hi, ok := recipe.ReviewPRRange(rr.Name); ok {
					if n := len(o.pr); n < lo || n > hi {
						if lo == hi {
							return userErr("--recipe %s needs exactly %d --pr, got %d", rr.Name, lo, n)
						}
						return userErr("--recipe %s needs %d to %d --pr, got %d", rr.Name, lo, hi, n)
					}
				}
				reviewRecipe = rr
			case len(o.pr) > 1:
				return userErr("several --pr need a review-mode recipe such as pick")
			case rerr != nil:
				// A single --pr with a --recipe that does not resolve (or
				// resolves but is not mode: review) is an ordinary
				// follow-up candidate: prSpec below re-resolves --recipe
				// itself, but only after its own account check (review
				// finding E, named-oauth-tokens task 9b) — returning rerr
				// here instead, unconditionally, would run this read
				// before that check ever has a chance to refuse a
				// disallowed --token or inherited account.
			}
		}
	}
	if prSet && reviewRecipe == nil {
		for _, name := range prFlags {
			if cmd.Flags().Changed(name) {
				return userErr("--pr continues the PR's base branch and workflow; it takes no --%s", name)
			}
		}
	}
	if attemptsSet {
		// The batch opens its own reporter once its own refusals are behind
		// it (runAttempts): a batch this launcher refuses is no run to keep a
		// log of either (D1352, D1354).
		return runAttempts(cmd, env, o, text, total)
	}

	var (
		spec     *task.Spec
		slug     string
		reused   bool // a stored follow-up, which the PR's checks never saw run
		firstRun bool // a new task's own launch: the one shape the A/B hook is asked about
	)
	switch {
	case o.retry != "":
		slug, spec, err = retrySpec(ctx, env, o.retry)
		reused = err == nil && spec.IsFollowUp()
	case reviewRecipe != nil:
		slug, spec, err = reviewSpec(ctx, env, o, reviewRecipe, text, total, cmd.ErrOrStderr())
		// A review job's repeat is the run the stored task is, as a follow-up's
		// is (prSpec): the reviewSpec above builds the task from the flags and
		// the checkout, but the launch then writes the chain's remaining into it
		// (chainInherit), so a repeat that compared the freshly built task would
		// be refused as a different one as soon as a run behind the chain spent
		// or a run in flight aged. Reusing the stored task is what makes the
		// repeat the launch it repeats rather than a second one refused.
		if err == nil {
			spec, reused, err = reuseStored(ctx, env, slug, o, 0, text, total, spec)
		}
	case prSet:
		// --review-run, before prSpec and not after it: the findings are part
		// of the follow-up's text, which is the whole of what prSpec compares
		// a repeated --run-id against and the whole of what it stores, so a
		// repeat of this same launch finds the task it stored instead of a
		// text that differs from it by the findings.
		if o.reviewRun != "" {
			repo := o.repo
			if repo == "" {
				if repo, err = originRepo(ctx); err != nil {
					return err
				}
			}
			var findings string
			if findings, err = reviewFindings(ctx, env, repo, o.reviewRun, o.pr[0]); err != nil {
				return err
			}
			text = findings + text
		}
		slug, spec, reused, err = prSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
	default:
		slug, spec, err = newSpec(ctx, env, o, text, total, cmd.ErrOrStderr())
		firstRun = true
		// A repeated --continues launch is the same run, and what its chain
		// left it is what its stored task says: that launch wrote the
		// remaining the chain had at the moment it ran, and a chain read now
		// answers another figure as soon as a run behind it spends or a run
		// still in flight ages (runstore's chainSeconds measures an unfinished
		// run to now). So a repeat is answered from the stored task, the way a
		// --pr follow-up's is (storedFollowUp): reused when it is this call's
		// own launch — a --run-id repeat is idempotent as any other — refused
		// when the run ID holds a different one, and built fresh when it holds
		// nothing.
		if err == nil && o.continues != "" {
			spec, reused, err = reuseStored(ctx, env, slug, o, 0, text, total, spec)
		}
	}
	if err != nil {
		return err
	}
	// The launch's reporter opens where the launch's own work does, once the
	// task is built: every refusal above it — the flags, and the spec a
	// --repo, --retry, --pr or --recipe does not resolve — is answered before
	// the run begins, and a launch that never began keeps no log (D1352,
	// D1354).
	rep, sink := reporterFor(cmd, "run", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	if o.retry == "" {
		noteRigorRequest(ctx, o, spec.Repo, cmd.ErrOrStderr())
	}
	step := rep.Step("launch")
	res, err := finishLaunch(ctx, env, o, slug, spec, reused, firstRun, cmd.ErrOrStderr(), false)
	if err != nil {
		return failStep(rep, step, err)
	}
	if res.Status == "already-launched" {
		// The launch the caller asked for is the one that already ran: there
		// is nothing to wait for, and the run it names is the run to look at,
		// so the reason carries it — a skipped step's tail is its reason, and
		// the reader still has to be told which run was not launched again.
		step.Skipped(launchWhat(res) + " already launched")
	} else {
		step.Done(launchWhat(res))
	}
	if o.asJSON {
		// One document, and the step above went to stderr with it (G2). The
		// log keeps a copy beside the screen's: the document is this
		// command's whole output, and the log holds what the command printed
		// (D1352).
		err = printLaunch(loggedOut(cmd.OutOrStdout(), sink), res, true)
	} else {
		err = launchDetail(loggedOut(cmd.OutOrStdout(), sink), res)
	}
	rep.Summary()
	return err
}

// finishLaunch is everything a launch does once its spec is built, however
// it was built (a new task, a follow-up, a review or a retry): embedding the
// project layer, the image and budget gates, storing the task and starting
// the execution. A best-of-N batch (runAttempts, through the launchAttempt
// seam) calls this once per attempt, each with its own fresh spec from
// buildUniqueAttemptSpec, and skipBudget true: the batch already leased its
// whole budget headroom once, before any attempt (design generic-tool
// review, 2026-10-13), so a per-attempt re-check here would both re-judge
// already-leased state and print its notes once per attempt instead of once
// per batch.
//
// plan is whether this launch is a first run's — the one shape the A/B hook
// is asked about (D558): a follow-up, a review, a retry and every attempt of
// a batch pass false, and the hook then does not run at all.
func finishLaunch(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, reused, plan bool, warn io.Writer, skipBudget bool) (launchResult, error) {
	// The repository's own document for this whole launch, resolved once
	// before any of it: where the working tree cannot give a fugaro.yaml, the
	// gates below and the job the launch runs on are judged by the document at
	// the ref the run checks out (withLaunchDocument). Resolved here and not
	// later because a reader that starts after this one is a reader that
	// judged the launch on another file.
	ctx = withLaunchDocument(ctx, spec.Repo, spec.Ref)
	// The one read a launch with no checkout of the repository makes of that
	// document at the forge (project_job.go) is shared by its two readers —
	// the image gates below, which need the workflow's own kind and checkout
	// mode, and the job decision — so it is installed here, before either: a
	// cache installed inside one of them is not the other's, and the two
	// would cost two mints where spec.md §6 allows one (D904). The daemon's
	// own launches install one in the same place and for the same reason
	// (orchestrator_launcher's Launch).
	ctx = withServiceReadCache(ctx)
	var layerData []byte
	var err error
	if o.retry == "" && !reused {
		if layerData, err = embedProjectLayer(ctx, env, spec, warn); err != nil {
			return launchResult{}, err
		}
	}
	// Whether the configuration this run will read turns compression on,
	// resolved once for every launch shape (a first run, a follow-up, a
	// review, a retry, each attempt of a batch) and used by both --no-compression
	// and the image gate below. Read after the layer is embedded, so it is the
	// resolution the run itself makes. compressionKnown is false when the
	// launcher could not read that configuration at all — a --repo launch from
	// outside a checkout of that repository, say — which is not the same as
	// reading one that leaves compression off.
	compression, compressionKnown := compressionRequested(ctx, env, spec)
	// --no-compression over a configuration the launcher could not read is
	// refused rather than guessed at: writing the field would put
	// compression_off on a task whose repository may never turn compression on,
	// and an older runner refuses the whole task for a field it does not know
	// (task.Parse decodes with DisallowUnknownFields) — so a flag that only
	// ever turns compression off would be what fails such a launch at
	// bootstrap. Skipping the image gate in exchange for the guess would let
	// that same run start on such a runner. Refused before anything is written
	// or uploaded, so nothing starts.
	if o.noCompression && !compressionKnown {
		return launchResult{}, userErr("--no-compression: the launcher cannot read %s's fugaro.yaml at %s to know whether compression is on; run from a checkout that has the ref, or drop the flag", spec.Repo, spec.Ref)
	}
	// --no-compression writes the task's own field only when there is
	// compression to turn off, the way the title and the plan task are
	// written: a launch that has nothing to override carries no field and
	// stays byte-identical to what the previous release wrote. Written before
	// createTask below, so a repeated --run-id with a flag the stored task
	// does not carry is refused as a different task rather than silently
	// dropped.
	if o.noCompression && compression {
		spec.CompressionOff = true
	}
	// --review-levels writes the task's own block, on every launch shape
	// below (a first run, each attempt of a batch, a review, a follow-up):
	// the flag is this launcher's request for the run about to start, and a
	// follow-up that gives it asks for its own levels rather than inheriting
	// the run it continues — nothing else carries them, the chain being read
	// for the title, the tier and the account alone. Written before
	// createTask below, like --no-compression, so a repeated --run-id with a
	// flag the stored task does not carry is refused as a different task
	// rather than silently dropped. A --retry takes no such flag (taskFlags),
	// so a stored task keeps the block it was launched with.
	if o.reviewLevels != nil {
		spec.ReviewLevels = o.reviewLevels
	}
	// The four --max-* chain limits (docs/run-limit-carry-over §2, D1042), on
	// every launch shape below as --review-levels is, and refused here rather
	// than dropped: the policy this launch resolves is merged first and a
	// value that would loosen is a refusal naming both numbers and the layer
	// that set the lower one (D1043). Written before createTask below, so a
	// repeated --run-id with a limit the stored task does not carry is
	// refused as a different task rather than silently dropped, and before
	// the gates that read the task (freshLaunchGates judges the image on the
	// fields this writes).
	if err := chainLimitGate(ctx, env, o, spec, reused, warn); err != nil {
		return launchResult{}, err
	}
	// The chain half (docs/run-limit-carry-over L4, D1045/D1047/D1052): what a
	// launch that does not begin a chain owes — the link it names, the chain's
	// own limit off the run that began it, what the chain has spent off its
	// runs' ledgers, and each dimension written as what is left rather than as
	// the chain's whole limit again. After chainLimitGate, whose figures are
	// this launch's own request and are what this call holds under the
	// remaining, and before the gates below, which judge the fields the task
	// now carries (freshLaunchGates reads the image off them).
	if err := chainInherit(ctx, env, o, slug, spec, reused, warn); err != nil {
		return launchResult{}, err
	}
	// The compression image gate's own condition, resolved here beside the
	// write and used by both launch branches below. It is the field's as much
	// as the configuration's, and the two halves catch different failures: a
	// run that will read a configuration turning compression on needs a runner
	// that can parse the key, and a task carrying compression_off needs one
	// that can parse the field — task.Parse decodes with DisallowUnknownFields,
	// so an older runner refuses the whole task either way. The field's half is
	// not implied by the configuration's: a follow-up (--pr) and a --retry
	// inherit the field from the run they continue, which carries it even after
	// the repository's key has been removed. A launch that writes no field and
	// reads no such key is never gated.
	compressionGate := compression || spec.CompressionOff
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	// The run-id freshness check, first of everything an --attach launch
	// does and before the first byte it could upload (design §4 step 1,
	// plan Review Focus 11). It is outside the prior == nil block below
	// because a repeated --run-id that already launched must be refused
	// here too rather than report the existing launch while holding
	// attachments that never went anywhere.
	if len(o.attach) > 0 {
		if err := attachRefusesStoredRun(ctx, s, spec.RunID); err != nil {
			return launchResult{}, err
		}
	}
	prior, err := existingLaunch(ctx, env, s, spec)
	if err != nil {
		return launchResult{}, err
	}
	// The other half of that check, and the last place --attach can still
	// refuse a run that exists: launch.json (or result.json's execution)
	// says this run id has launched while no task.json is there to read (a
	// task deleted after the fact, a record backfilled without one). Every
	// --attach step lives inside the prior == nil block below — the caps,
	// the image gate, the upload itself — so a launch that found a prior
	// would write a task naming no inputs while the files the owner named
	// went nowhere, silently. No attachment is added to a run that already
	// went: it started with the task it started with.
	if prior != nil && len(o.attach) > 0 {
		return launchResult{}, userErr("run ID %s has already launched; --attach uploads a run's files before it starts, so it never adds attachments to a run that already went — use a new run ID", spec.RunID)
	}
	// The check is the pull request link's, and IsFollowUp is the stored task's
	// own branch: a reused --retry or a reused --pr follow-up carries one, while
	// a reused --continues task carries none — its link is the run the caller
	// named and there is no PR to re-read.
	if prior == nil && reused && spec.IsFollowUp() {
		if err := recheckFollowUp(ctx, env, slug, spec, warn); err != nil {
			return launchResult{}, err
		}
	}
	// The other half of that check, for the launch shape whose stored task
	// carries no link to re-read: what a --continues task was written for is in
	// no field of it, so the chain the caller names is held to the figures the
	// stored task carries (recheckContinues). Also only for a run ID that never
	// launched: one that did is the run the stored task already is.
	if prior == nil && reused && o.continues != "" {
		if err := recheckContinues(ctx, env, o, slug, spec); err != nil {
			return launchResult{}, err
		}
	}
	if o.retry == "" {
		// Before the image gate below, not after: a run ID whose earlier
		// attempt stored a task but never launched it (prior == nil, an
		// ambiguous launch failure, say) must be gated on the layer that
		// stored task actually carries, since createTask keeps it
		// regardless of what embedProjectLayer just freshly re-resolved
		// (repeatTaskBytes excludes the layer from "is this the same
		// task", and createTask adopts the stored one either way). Doing
		// this adoption only here, as a read-only peek, keeps a genuinely
		// new launch's image gate running before any write, as it always
		// has: createTask's own, later ReadTask only happens on ErrExists.
		if err := adoptStoredProjectLayer(ctx, s, spec); err != nil {
			return launchResult{}, err
		}
	}
	// The A/B hook (A3.0), last of what this launch decides and before its
	// first gate writes anything: a first run's task is offered to the build's
	// planner, which answers a pair to sample or nothing at all. The planner
	// is asked here rather than where the task is built because the task it is
	// handed carries the project layer the run reads (embedProjectLayer
	// above), and because a run id whose earlier attempt already launched is
	// no launch to sample beside (prior).
	//
	// A pair the launcher accepts leases both arms' headroom once, so the
	// champion's own single-run pre-check is skipped below exactly as a
	// batch's attempts skip it (skipBudget); a pair it refuses, or a planner
	// that errs, leaves this launch exactly as it was — no ab block, no batch
	// label, today's single pre-check — with one line saying why (D554).
	var pair *Pair
	var pairWhy string
	if plan && prior == nil {
		if pair, pairWhy = sampledPair(ctx, env, o, slug, spec, warn); pair != nil {
			skipBudget = true
		}
	}
	if prior == nil {
		caps, err := freshLaunchGates(ctx, env, o, slug, s, spec, compressionGate, warn, skipBudget)
		if err != nil {
			return launchResult{}, err
		}
		// The upload, last of everything an --attach launch does and still
		// before createTask: every refusal this launcher can make from what
		// it already has has been made above, so a refused launch leaves no
		// input object under a run id that never gets a task (design §4
		// step 1, plan Review Focus 11), and the task written next names
		// exactly the bytes that are in the bucket.
		if err := attachUpload(ctx, env, spec, s.Prefix(), o.attach, caps); err != nil {
			return launchResult{}, err
		}
		// The manifest a mode: evaluate launch leaves for its own recipe's step
		// (A5.2), by the same rule as the attachments above: after every
		// refusal this launcher can make and before createTask, so a refused
		// launch stores nothing and starts nothing, and a task that names a
		// manifest is a task whose manifest is in the bucket.
		if err := writeRecipeManifest(ctx, env, o, slug, spec, s, warn); err != nil {
			return launchResult{}, err
		}
	}
	if o.retry == "" {
		if err := createTask(ctx, s, spec); err != nil {
			return launchResult{}, err
		}
		if !reused {
			// After createTask, so a repeated --run-id announces whatever it
			// adopted (the stored task's layer), not whatever this call
			// happened to resolve first.
			announceProjectLayer(warn, spec, layerData)
		}
	}
	chooses := runnerChooses(ctx, spec)
	fmt.Fprintln(warn, recipeLine(spec, chooses))
	res, err := launchRun(ctx, env, slug, spec, time.Now())
	if err != nil {
		return launchResult{}, err
	}
	if o.retry != "" && res.Status == "already-launched" {
		fmt.Fprintf(warn, "%s launched already; --retry only starts a run that never launched. "+
			"To see how it went, run fugaro diagnose %s; to redo it, start a new run.\n",
			task.ShortRun(oneLine(res.Run)), task.ShortRun(oneLine(res.Run)))
	}
	res.Project = env.lc.Name
	res.Recipe = launchRecipeOf(spec, chooses)
	if spec.ProjectLayer != nil {
		res.ProjectLayer = &launchProjectLayer{SHA256: spec.ProjectLayer.SHA256, Generation: spec.ProjectLayer.Generation}
	}
	// The second arm, once the champion has actually gone: the pair's own
	// pre-check leased both arms, and a champion that turned out to be a run
	// that already launched samples nothing beside itself.
	if res.Status == "launched" {
		switch {
		case pair != nil:
			res.Pair = &launchPair{ID: pair.Champion.PairID, Batch: spec.Batch}
			res.Pair.Challenger = launchChallenger(ctx, env, slug, spec, pair, warn)
		case pairWhy != "":
			// Nothing was sampled, and a planner was asked and said why: the
			// line it printed on stderr is in the document too (D556).
			res.Pair = &launchPair{Reason: pairWhy}
		}
	}
	return res, nil
}

// sampledPair asks the build's planner whether this launch samples a pair,
// and judges what it answers: the room for two runs, the pair's own budget
// pre-check (D566) and D573's margin. It answers the pair to launch, or nil —
// and a nil is never a refusal of the launch: the champion goes on alone,
// unmarked, on the single pre-check every launch makes, with one line saying
// why (D554).
func sampledPair(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, warn io.Writer) (*Pair, string) {
	if !planning() {
		return nil, "" // no planner registered: an untagged launch does none of this
	}
	run, why := plannedRun(ctx, env, o, slug, spec)
	if why != "" {
		pairSkip(warn, why)
		return nil, why
	}
	pair, err := planLaunch(ctx, env, run)
	if err != nil {
		why := "the planner failed: " + oneLineCLI(err.Error())
		pairSkip(warn, why)
		return nil, why
	}
	if pair == nil {
		// Not eligible, or the rate's own roll said no. Both are the ordinary
		// answer of a launch that samples nothing, and neither is worth a line:
		// most launches of a project with a challenger configured are one.
		return nil, ""
	}
	if pair.Challenger == nil {
		// The planner's own refusal, with its closed reason (D556). A reason
		// is the whole of what it has to say, so a pair with none is a
		// planner bug and is reported as one.
		why := pair.Skip
		if why == "" {
			why = "the planner answered a pair with no challenger"
		}
		pairSkip(warn, why)
		return nil, why
	}
	// A launch that attaches files samples nothing. An --attach launch uploads
	// them under its own run's prefix and a mode: artifact run downloads them
	// from there (runstore.InputKey), so the challenger — a run id of its own —
	// would name files that are not in its bucket and die at bootstrap
	// (runner/artifact.go). The arms cannot be the one run a pair needs them to
	// be, and a task file that names inputs has the same shape.
	if len(o.attach) > 0 || len(spec.Inputs) > 0 {
		why := "the launch attaches files, and a challenger would read them under its own run id, where they are not"
		pairSkip(warn, why)
		return nil, why
	}
	// The two blocks are the arms they claim, and one pair: the codec below
	// judges a block's own shape, and an absent block is legal to it, so a
	// challenger's task with no block at all — or with the champion's arm —
	// would pass it and launch a second run that publishes (D554).
	if why := pairArms(pair); why != "" {
		pairSkip(warn, why)
		return nil, why
	}
	label, why := pairLabel(o.batch, pair)
	if why != "" {
		pairSkip(warn, why)
		return nil, why
	}
	// Both arms' blocks are judged here, before either is written: the task
	// codec refuses a block the runner would refuse at bootstrap
	// (task.Validate), and a planner's own bug must cost a pair rather than
	// the champion's run. The probe is the task as it would be written, with
	// the block and the label this pair brings.
	probe := *run.Spec
	probe.Batch = label
	for _, ab := range []*task.AB{&pair.Champion, pair.Challenger.AB} {
		probe.AB = ab
		if err := probe.Validate(); err != nil {
			why := "the pair is not a pair: " + oneLineCLI(err.Error())
			pairSkip(warn, why)
			return nil, why
		}
	}
	if _, err := runstore.RunTime(pair.Challenger.RunID); err != nil {
		// The error quotes the value, which is the point: what a planner
		// answered is not a run ID, so there is no ending of one to shorten.
		why := "the challenger's run id is not one: " + oneLineCLI(err.Error())
		pairSkip(warn, why)
		return nil, why
	}
	if pair.Challenger.RunID == spec.RunID {
		why := "the challenger carries the champion's own run id"
		pairSkip(warn, why)
		return nil, why
	}
	// The pair's own image gate, after the checks that judge the pair's shape
	// and before the two that read the cloud for room and for money: a runner
	// older than the ab field either refuses the whole task at bootstrap
	// (task.Parse decodes with DisallowUnknownFields, on a release older than
	// the wire) or reads the block with no challenger rule and publishes the
	// arm as an ordinary run (the wire's own release, one before abSince), and
	// either way a pair written onto such an image costs the champion more than
	// a sample. A pair is dropped, the launch is not (D554, pairImageWhy).
	if why := pairImageWhy(ctx, env, slug, run.Spec); why != "" {
		pairSkip(warn, why)
		return nil, why
	}
	// Room for two, before the money: a pair is two runs at once, and a launch
	// that cannot hold both starts neither (the shape runAttempts checks for a
	// batch). The champion's own gate below asks the same question for one run,
	// so a pair this refuses still starts the champion if there is room for it.
	if err := checkMaxParallelFor(ctx, env, 2); err != nil {
		why := oneLineCLI(err.Error())
		pairSkip(warn, why)
		return nil, why
	}
	if why := env.pairBudgetPrecheck(ctx, slug, spec, warn); why != "" {
		pairSkip(warn, why)
		return nil, why
	}
	spec.AB = &pair.Champion
	spec.Batch = label
	return pair, ""
}

// plannedRun is what the planner is handed. It answers a reason instead of a
// run when a figure a pair is judged on cannot be read at all: a pair is
// never sampled on a number the launcher does not have.
func plannedRun(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec) (PlannedRun, string) {
	cfg := launchLevelsConfig(ctx, env, spec)
	prices, _, err := priceTable(env.lc, time.Now())
	if err != nil {
		return PlannedRun{}, "the price table could not be read: " + oneLineCLI(err.Error())
	}
	copied, err := cloneSpec(spec)
	if err != nil {
		return PlannedRun{}, "the task could not be copied for the planner: " + oneLineCLI(err.Error())
	}
	// The switch that decides whether this job is handed the credentials
	// service's address, asked of the one function that decides it
	// (credentialsServiceLaunchEnv, proxiedRepo's last question too); its own
	// warning is the launch's to word, later and once.
	auth, deployed := env.repoServiceAuth(ctx, spec, io.Discard)
	run := PlannedRun{
		Slug: slug, Repo: spec.Repo, Commit: launchCommit(ctx, spec.Repo, spec.Ref),
		Spec: copied, Chosen: o.runID != "", Config: cfg,
		Providers:      mixRoutes(env, spec.Repo, cfg),
		Prices:         prices,
		ServiceHeldKey: len(credentialsServiceLaunchEnv(env.lc, "", spec.Repo, auth, deployed)) > 0,
	}
	eff, _ := chainLimitPolicyOf(ctx, env, spec, cfg).merge(o.limits.layer())
	run.Policy = eff
	return run, ""
}

// launchCommit is the commit a first run is cut from, for the planner (D571):
// the ref resolved in this machine's checkout of the repository.
func launchCommit(ctx context.Context, repo, ref string) string {
	return commitAt(ctx, checkoutRoot(ctx, repo), ref)
}

// commitAt is launchCommit's own read: the ref itself when it is already a
// full commit SHA, else the SHA a checkout has for it, read the way every
// other read of a ref in a checkout is (the remote-tracking ref first, then
// the ref itself). Empty where neither is true — a launch from outside a
// checkout names a branch this machine cannot resolve, and a planner that
// needs the commit then resolves it itself.
func commitAt(ctx context.Context, root, ref string) string {
	if gitops.IsFullSHA(ref) {
		return ref
	}
	if root == "" || ref == "" {
		return ""
	}
	for _, candidate := range []string{"refs/remotes/origin/" + ref, ref} {
		sha, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		if err == nil && gitops.IsFullSHA(sha) {
			return sha
		}
	}
	return ""
}

// cloneSpec is a deep copy of a task, for a planner that may keep or edit what
// it is handed: the launch writes its own spec, never the copy.
func cloneSpec(spec *task.Spec) (*task.Spec, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	out := &task.Spec{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

// pairArms judges the two blocks a planner answered as the pair they have to
// be: the champion's arm on the champion, a challenger's arm on the challenger,
// and one pair id on both. task.Validate judges each block's own shape and
// treats an absent block as the ordinary unsampled task, so this is the half of
// the pair it cannot see — and the half that decides whether the second run is
// a shadow that never publishes (D554) or a record a reader can join to its
// pair (task.AB.PairID). It answers why the answer is not a pair, or "".
func pairArms(pair *Pair) string {
	if pair.Champion.Arm != task.ABArmChampion {
		return fmt.Sprintf("the champion's block carries the %q arm", oneLine(string(pair.Champion.Arm)))
	}
	if pair.Challenger.AB == nil {
		return "the challenger's task carries no ab block"
	}
	if pair.Challenger.AB.Arm != task.ABArmChallenger {
		return fmt.Sprintf("the challenger's block carries the %q arm", oneLine(string(pair.Challenger.AB.Arm)))
	}
	if pair.Champion.PairID != pair.Challenger.AB.PairID {
		return fmt.Sprintf("the two blocks carry different pair ids (%q and %q)",
			oneLine(pair.Champion.PairID), oneLine(pair.Challenger.AB.PairID))
	}
	return ""
}

// pairLabel is the one batch label both arms of a pair carry, so a person
// reads the pair out of `fugaro runs ls --batch`: the launch's own --batch
// when it has one, else one derived from the pair's id, which has to be a
// label a task may carry (task.BatchRE).
func pairLabel(batch string, pair *Pair) (string, string) {
	if batch != "" {
		return batch, ""
	}
	label := "ab-" + pair.Champion.PairID
	if !task.BatchRE.MatchString(label) {
		return "", fmt.Sprintf("the pair's id %q makes no batch label (%s)", oneLine(pair.Champion.PairID), label)
	}
	return label, ""
}

// launchChallenger starts the second arm: the champion's own launched task
// with the arm's two differences — its run id and its ab block (D555) — so
// every other field of the two tasks, the project layer and the chain limits
// among them, is the launch's own and the arms cannot drift apart (D571). It
// answers the challenger's run id, or "" when the arm could not start: the
// champion has already gone by then, so nothing about the second arm is
// allowed to fail the developer's run.
func launchChallenger(ctx context.Context, env *cloudEnv, slug string, champion *task.Spec, pair *Pair, warn io.Writer) string {
	arm, err := cloneSpec(champion)
	if err != nil {
		pairWarn(warn, "the challenger's task could not be built: "+oneLineCLI(err.Error()))
		return ""
	}
	arm.RunID, arm.AB = pair.Challenger.RunID, pair.Challenger.AB
	if err := arm.Validate(); err != nil {
		pairWarn(warn, "the challenger's task is not one: "+oneLineCLI(err.Error()))
		return ""
	}
	s := runstore.Open(env.bucket.Bucket, slug, arm.RunID)
	if err := createTask(ctx, s, arm); err != nil {
		pairWarn(warn, "the challenger's task was not stored: "+oneLineCLI(err.Error()))
		return ""
	}
	res, err := launchRun(ctx, env, slug, arm, time.Now())
	if err != nil {
		pairWarn(warn, "the challenger did not launch: "+oneLineCLI(err.Error()))
		return ""
	}
	if res.Status != "launched" {
		// The run id already went, so this is not the pair's second arm:
		// naming it as one would put a challenger in the champion's record
		// that this launcher never started, which is the run a batch refuses
		// to count for the same reason (runAttempts).
		pairWarn(warn, fmt.Sprintf("run %s had already launched, so no challenger is recorded for this pair",
			task.ShortRun(oneLine(res.Run))))
		return ""
	}
	return arm.RunID
}

// pairOldImage is the launcher's own closed reason for sampling nothing: the
// job image this launch will run on is not one the pair may be written for
// (pairImageWhy). Like the planner's reasons it is one word, because the skip
// line prints it and `fugaro run --json` carries it.
const pairOldImage = "old-image"

// pairSkip is the one line a launch prints when it meant to sample a pair and
// did not: what was wrong, and what happened instead. The champion always
// reaches the developer (D554).
func pairSkip(warn io.Writer, why string) {
	fmt.Fprintf(warn, "ab: no pair sampled: %s; launching the champion alone\n", oneLine(why))
}

// pairWarn is the one line a launch prints when the second arm alone could not
// start, after the champion has gone.
func pairWarn(warn io.Writer, why string) {
	fmt.Fprintf(warn, "ab: the challenger did not start: %s; the champion is unaffected\n", oneLine(why))
}

// chainLimitsAsk reports whether the task carries any of the four chain limits.
// It is the fields' check and not the flags': a launch that reuses a stored
// task — --retry, or a repeated --run-id of one that never launched — carries
// limits no flag of this call named, and an image gate that read the flags
// would launch it onto a runner that refuses the task.
func chainLimitsAsk(spec *task.Spec) bool {
	o := spec.Overrides
	return o.MaxUSD != nil || o.MaxTokens != nil || o.MaxTime != "" || o.MaxReviewRounds != nil
}

// chainLimitGate writes this launch's four --max-* limits into the task's
// overrides, resolving first the policy they merge under (chainLimitPolicyOf)
// and refusing a value that would loosen it (D1043, D1044).
//
// The four are written as the merge's own effective values: a chain limit may
// only tighten, so the merged figure is the caller's wherever it was not
// refused, and writing the merged one keeps the task and the policy record
// saying the same number from the same layer.
//
// The two dimensions no layer under the chain's sets are the launcher's own
// comparisons, and both are recorded divergences from the plan that asked for
// this flag (docs/run-limit-carry-over L3, F15, F18):
//
//   - The review rounds are never refused against the mode's number: a mode's
//     row is a default and not a cap (internal/rigor's DefaultRounds, which
//     replaced the RoundCeiling the plan quotes, #1483), and a chain spans
//     runs, so a chain limit above one run's number is a launch the engine
//     would run. A refusal that names a figure at all is the one for a value
//     below 1 round, and the figure it names is chainRoundCeiling's output.
//   - The wall time is compared with the run's own clock, the number
//     --total-timeout raises and this one may only tighten: a launch that gives
//     both composes as min, and a --max-time above the clock is refused rather
//     than silently ineffective (F18, spec §2). The clock is the launch's own
//     --total-timeout, else the workflow's timeouts.total when this launcher
//     can read the checkout; a launcher that can read neither writes the chain
//     limit alone and leaves the composition to the runner's bootstrap (L5).
//
// A launch that reuses a stored follow-up (reused) writes nothing here: the
// limits such a task carries were merged, refused and composed when it was
// written, and what this call's flags claim is compared byte for byte against
// them (storedFollowUp), so there is no figure left to judge — while the merge
// and the clock both depend on the environment this call happens to read (the
// policy as it stands now, the checkout in hand now), and re-deriving them
// would refuse a task that is not changing, or write a different composition
// into a run that has already started.
func chainLimitGate(ctx context.Context, env *cloudEnv, o *runOptions, spec *task.Spec, reused bool, warn io.Writer) error {
	if o.retry != "" || reused || !o.limits.set() {
		return nil
	}
	c := chainLimitPolicyOf(ctx, env, spec, launchLevelsConfig(ctx, env, spec))
	e, base := c.merge(o.limits.layer())
	for _, ig := range e.Ignored[base:] {
		if err := chainLimitRefusal(ig); err != nil {
			return err
		}
	}
	if o.limits.usd != nil {
		spec.Overrides.MaxUSD = &e.PerRunUSD
	}
	if o.limits.tokens != nil {
		spec.Overrides.MaxTokens = &e.MaxRunTokens
	}
	if r := o.limits.rounds; r != nil {
		if *r < 1 {
			return userErr("--max-review-rounds %d: a chain's review rounds must be at least 1. A %s run's own review loop starts from %d rounds per run (the mode's default, held inside the recipe's %d), so a limit below that bounds a chain that could never spend it",
				*r, c.mode, chainRoundCeiling(c.mode), recipe.MaxReviewRounds)
		}
		n := int(e.MaxReviewRounds)
		spec.Overrides.MaxReviewRounds = &n
	}
	if o.limits.time > 0 {
		clock := launchClockOf(ctx, env, spec)
		if clock > 0 && o.limits.time > clock {
			return userErr("--max-time %s is above %s, this run's own clock (its %s): a chain's wall time can only tighten the clock the run itself has, and the two compose as the smaller of the two. Pass %s or less, or raise the clock rather than the chain",
				o.limits.time, clock, launchClockSource(o, spec), clock)
		}
		t := o.limits.time.String()
		spec.Overrides.MaxTime = t
		if clock > 0 {
			// The composition (spec §2): the run's clock is the smaller of
			// the two, written as the task's own total_timeout — which is
			// the field the backend's override reads (launchTimeoutOf) — and
			// the chain's limit stays in max_time beside it, where the
			// runner merges it again at bootstrap (D1044).
			composed := min(clock, o.limits.time)
			// The composed figure becomes the run's own total_timeout, so
			// the rule the --total-timeout flag is held to holds it too: a
			// clock that does not clear the workflow's finalize reserve
			// leaves the run no time to finish in.
			if co := checkoutConfig(ctx, spec.Repo); co != nil {
				if w, ok := co.Workflows[spec.Workflow]; ok && composed <= w.Timeouts.FinalizeReserve.Duration {
					return userErr("--max-time %s leaves this run a %s clock, which workflow %s's timeouts.finalize_reserve %s does not fit inside: pass more than the reserve (the run's clock is the smaller of --max-time and its own %s)",
						o.limits.time, composed, spec.Workflow, w.Timeouts.FinalizeReserve.Duration, launchClockSource(o, spec))
				}
			}
			spec.Overrides.TotalTimeout = composed.String()
		}
	}
	return nil
}

// launchClockOf is the clock this launch may give the run: the total timeout
// the caller asked for, else the workflow's own timeouts.total as the checkout
// in hand declares it. 0 is a launcher that can resolve neither — a launch
// from outside the repository's checkout with no --total-timeout — and then
// there is nothing to compare the chain's wall time with: it is still written,
// and the runner composes it at bootstrap (L5).
func launchClockOf(ctx context.Context, env *cloudEnv, spec *task.Spec) time.Duration {
	if d := launchTimeoutOf(spec); d > 0 {
		return d
	}
	if co := checkoutConfig(ctx, spec.Repo); co != nil {
		if w, ok := co.Workflows[spec.Workflow]; ok {
			return w.Timeouts.Total.Duration
		}
	}
	return 0
}

// launchClockSource names where the clock above came from, as the refusal
// reads it: the flag the caller typed, or the workflow's own key.
func launchClockSource(o *runOptions, spec *task.Spec) string {
	if o.totalTimeout != "" {
		return "--total-timeout"
	}
	return "workflows." + spec.Workflow + ".timeouts.total"
}

// freshLaunchGates is the block of gates a fresh launch passes before anything
// of it is written, and it is one function because the daemon's own launch path
// (orchestrator_launcher.go) is that same launch shape and must pass the same
// block: a daemon that ran only the budget pre-check and the policy gate started
// runs `fugaro run` refuses — onto a job image whose runner cannot read the
// sealed token or a field the task carries, which is a start that spends an
// approved attempt on a run the platform then refuses at bootstrap.
//
// It judges the spec and the installation and nothing a person typed beyond the
// task the launch writes, and it resolves the workflow's kind and checkout mode
// itself, so both callers judge the same reads. Everything it can refuse, it
// refuses before the claim and before the task: a launch it refuses leaves no
// task, no token, no claim and no execution behind. What it returns is the
// attach launch's own caps, which the caller's upload needs; the upload itself
// is the caller's, because the daemon never carries attachments.
func freshLaunchGates(ctx context.Context, env *cloudEnv, o *runOptions, slug string, s *runstore.Store, spec *task.Spec, compressionGate bool, warn io.Writer, skipBudget bool) (config.Inputs, error) {
	// The cascade's own gate, first: nothing about a run this launch
	// cannot carry is worth an --attach upload, a cap check or a claim
	// (cascadeLaunchRefusal).
	if err := cascadeLaunchRefusal(ctx, env, spec); err != nil {
		return config.Inputs{}, err
	}
	// --attach, before the gates below: its recipe refusal, its caps,
	// D9's line and its image gate (design §4, plan P7/P9). It uploads
	// nothing here — attachUpload does that below, after the last gate
	// that can still refuse this launch, so a refused launch leaves
	// nothing in the bucket under a run id that never gets a task. A
	// launch with no --attach does nothing here at all.
	agentRecipe, kind, clone, shared := "", "", false, false
	// mode is the same answer for baseBranchSkew, and nil where no workflow
	// was read at all — a repository no shared job serves, a project that
	// published no profile, a ref whose tree carries no document, and a
	// checkout whose document could not be read: baseBranchSkew asks both
	// judges there rather than take the one a derived workflow would use
	// (its comment has why).
	var mode *bool
	co := checkoutConfig(ctx, spec.Repo)
	if co == nil {
		// The working tree gave no document. Where the machine holds no
		// checkout of the repository at all, this launch's document is the one
		// read at the forge (source 3, D902) — the same read the job decision
		// makes, memoized, so a launch costs one mint — because the workflow's
		// kind and checkout mode are what the image gates below are judged by:
		// a checkout: clone workflow runs the installation's base image and
		// has no build record, so judging it as a derived image refuses it for
		// a record it can never have (image_refresh.go). Where that read
		// fails, this refuses in the decision's own words rather than judging
		// an image by a workflow nobody read.
		cfg, err := env.launchConfigWhereTheTreeGivesNone(ctx, spec)
		if err != nil {
			return config.Inputs{}, err
		}
		co = cfg
	}
	if co != nil {
		agentRecipe = co.Agent.Recipe
		if w, ok := co.Workflows[spec.Workflow]; ok {
			kind = w.BaseKind()
			clone = w.Checkout == config.CheckoutClone
			shared = w.Checkout == config.CheckoutShared
			m := clone
			mode = &m
		}
	}
	caps, err := attachLaunch(ctx, env, slug, spec, s.Prefix(), o.attach, kind, clone, warn)
	if err != nil {
		return config.Inputs{}, err
	}
	// The base branch's own gate, before anything is written: a runner
	// older than baseBranchSince cannot read the field and would refuse
	// the whole task, so the field is dropped rather than the launch
	// (baseBranchSkew). Here, and not with the gates below, because it
	// decides what the task createTask writes.
	baseBranchSkew(ctx, env, slug, spec, mode, warn)
	// What this launch asks of its job image's runner about review
	// levels, resolved once for the two branches below (a derived image
	// and a checkout: clone workflow), the way compressionGate is: a
	// launch that asks nothing is never gated, whatever its image is.
	levelsRecipe, levelsRecipeUnknown := launchRecipeForLevels(ctx, spec)
	// The launch is refused on the resolution's own refusal (L3.2): a run
	// whose levels cannot be resolved — the same model at level 1 and 2, a
	// level turned off under the owner's floor, an effort a provider route
	// would drop — is refused here, before the task is written, rather
	// than by the job it was launched into (G2, D721, D722).
	// The configuration this launch can read of the run's own — the file
	// at the ref, with the project layer it resolves over, or the layer
	// the task carries where no file can be read — resolved once for both
	// readers below: the levels the launch asks about, and the level one of
	// the two writes with its model named none (checkNoneModelImage,
	// launchLevelsConfig).
	fileCfg := launchLevelsConfig(ctx, env, spec)
	levelsAsk, err := launchReviewLevelsAsk(ctx, env, spec, fileCfg, levelsRecipe, levelsRecipeUnknown)
	if err != nil {
		return config.Inputs{}, err
	}
	// checkRecipeImage and checkImageSince judge a workflow's derived
	// image by its build record, which a checkout: clone workflow never
	// has (it runs the installation's base image directly, not a
	// per-repository build): checkCloneImageSince judges base_images.base
	// itself instead. cloneSince is unconditional: a clone workflow needs
	// the runner's own blobless-clone bootstrap (plan Task 17,
	// FUGARO_CHECKOUT) whether or not it uses a recipe or a project
	// layer, and nothing else here enforces that base_images.base is
	// actually that recent: fugaro-base could first be published from an
	// older release, or --image-source could mirror one.
	if clone {
		if err := checkCloneImageSince(env.lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", warn); err != nil {
			return config.Inputs{}, err
		}
		if needsRecipeImage(spec, agentRecipe) {
			if err := checkCloneRecipeImage(ctx, env.lc, spec, warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.ProjectLayer != nil {
			if err := checkCloneImageSince(env.lc, spec, layeredSince, "the project layer", "the project layer", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.OAuthToken != "" {
			if err := checkCloneImageSince(env.lc, spec, namedTokensSince,
				"Claude account "+pluginwire.Printable(spec.OAuthToken), "named Claude accounts", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.Title != "" {
			if err := checkCloneImageSince(env.lc, spec, titlesSince,
				"a job title (--title, kept by --pr follow-ups and --retry)", "the job title", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.PlanTask != "" {
			if err := checkCloneImageSince(env.lc, spec, planTaskSince,
				"a plan task (--plan-task)", "the plan task", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The chain limits' own gate, for the same reason as the four above:
		// a runner older than launchLimitsSince refuses a task carrying one
		// outright, and the run would die at bootstrap over fields that change
		// nothing about what it does. A launch that carries none (the common
		// case) is never gated, so a task stays byte-identical to what the
		// previous release wrote: the check is the field's, not the flag's.
		if chainLimitsAsk(spec) {
			if err := checkCloneImageSince(env.lc, spec, launchLimitsSince,
				chainLimitsSubject, chainLimitsKnows, chainLimitsAlt, warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The compression gate: a runner older than compressionSince knows
		// neither the key nor the field and refuses whichever it meets at
		// bootstrap, so the launch is judged on both (compressionGate
		// above). It fires whether or not --no-compression is set, since
		// the override turns the sidecar off and not the key the old
		// runner cannot read.
		if compressionGate {
			if err := checkCloneImageSince(env.lc, spec, compressionSince,
				"agent.compression", "context compression", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The rigor: gate, judged on the file the run will read rather
		// than on a task field: see checkRigorImage. Unconditional, since
		// the repository's file alone can carry the key.
		if err := checkCloneRigorImage(ctx, env, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The governance: gate, judged the same way and for the same
		// reason: see checkGovernanceImage.
		if err := checkCloneGovernanceImage(ctx, env, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The project layer's jobs: block, the same shape and the same
		// reason again: see jobsSince. The key is the layer's alone, so
		// there is no repository half here.
		if err := checkCloneJobsImage(ctx, env, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The security: block, the same shape as the rigor: gate above
		// and for the same reason: see checkSecurityImage.
		if err := checkCloneSecurityImage(ctx, env, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The planning: block, the same shape and the same reason again:
		// see checkPlanningImage.
		if err := checkClonePlanningImage(ctx, env, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The follow-up fields a filed task carries, gated on the fields
		// themselves: see followUpSince. It comes before the request
		// below because a task this command files also carries the
		// parent's recorded mode, and followUpSince is the newer of the
		// two: the refusal a person can act on names the command they
		// just ran rather than a --mode flag they never passed.
		if err := checkCloneFollowUpImage(env.lc, spec, warn); err != nil {
			return config.Inputs{}, err
		}
		// The run's own rigor request, gated on the field the same way:
		// a runner older than rigorRequestSince refuses a task carrying
		// rigor or paths outright (task.Parse decodes with
		// DisallowUnknownFields). A launch that carries neither field is
		// never gated — but a follow-up that passed no flag still carries
		// one when its PR recorded a mode (T5.2), which is what
		// rigorGateSubject says in the refusal.
		if asksForRigor(spec) {
			if err := checkCloneImageSince(env.lc, spec, rigorRequestSince,
				rigorGateSubject(o, spec), "the rigor request", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The tier a task carries, judged on the field like the request
		// above and under the release that ships both the field and the
		// governance: block its expectation comes from (governanceSince):
		// a runner older than that refuses a task carrying a tier outright
		// (task.Parse decodes with DisallowUnknownFields). A launch that
		// carries none is never gated — but a follow-up that passed no
		// flag still carries one when its PR recorded a tier, which is
		// what tierGateSubject says in the refusal.
		if spec.Tier != "" {
			if err := checkCloneImageSince(env.lc, spec, governanceSince,
				tierGateSubject(o, spec), "the tier", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The review levels' gate, judged on the run this launch
		// resolves rather than on a task field: a mode's own level 3, a
		// level the run inserts (O7) and the owner's floor all reach a
		// run with no field and no configuration key, and a runner older
		// than reviewLevelsSince drops every one of them in silence
		// (reviewLevelsAsk).
		if err := checkCloneReviewLevelsImage(env.lc, spec, levelsAsk, warn); err != nil {
			return config.Inputs{}, err
		}
		// The spelling's own gate, on the same configuration read and
		// beside the one above: a runner older than noneModelSince reads
		// `model: none` as a model ID rather than as the level's switch
		// (checkCloneNoneModelImage), the task's own layer included.
		if err := checkCloneNoneModelImage(env.lc, spec, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
		// The stop rule's key, on the same configuration read: a runner
		// older than stopFractionSince refuses agent.review_stop_fraction
		// at bootstrap, whatever it says, so the launch is judged on the
		// text the run will read (checkCloneStopFractionImage).
		if err := checkCloneStopFractionImage(env.lc, spec, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
		// The triage stage's switch, on the same configuration read: a
		// runner older than triageSince refuses agent.triage at
		// bootstrap, whatever it says (checkCloneTriageImage).
		if err := checkCloneTriageImage(env.lc, spec, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
	} else {
		if needsRecipeImage(spec, agentRecipe) {
			if err := checkRecipeImage(ctx, env, slug, spec, kind, warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.ProjectLayer != nil {
			if err := checkImageSince(ctx, env, slug, spec, kind, layeredSince, "the project layer", "the project layer", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		if spec.OAuthToken != "" {
			if err := checkImageSince(ctx, env, slug, spec, kind, namedTokensSince,
				"Claude account "+pluginwire.Printable(spec.OAuthToken), "named Claude accounts", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The job title's own gate, for the same reason as the two
		// above: task.Parse decodes with DisallowUnknownFields, so a
		// runner older than titlesSince refuses a task carrying a title
		// outright. A launch with no --title carries no such field (the
		// spec is byte-identical to what the previous release wrote) and
		// is never gated: the check is the field's, not the flag's.
		if spec.Title != "" {
			if err := checkImageSince(ctx, env, slug, spec, kind, titlesSince,
				"a job title (--title, kept by --pr follow-ups and --retry)", "the job title", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The plan task's own gate, for the same reason as the three
		// above: task.Parse decodes with DisallowUnknownFields, so a
		// runner older than planTaskSince refuses a task carrying one
		// outright, and the run would die at bootstrap over a field that
		// changes nothing about what it does. A launch with no
		// --plan-task carries no such field (the spec is byte-identical
		// to what the previous release wrote) and is never gated: the
		// check is the field's, not the flag's.
		if spec.PlanTask != "" {
			if err := checkImageSince(ctx, env, slug, spec, kind, planTaskSince,
				"a plan task (--plan-task)", "the plan task", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The same gate as the clone branch's above, and for the same
		// reason: see launchLimitsSince.
		if chainLimitsAsk(spec) {
			if err := checkImageSince(ctx, env, slug, spec, kind, launchLimitsSince,
				chainLimitsSubject, chainLimitsKnows, chainLimitsAlt, warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The compression gate: the same gate as the clone branch's above,
		// and for the same reason. It is judged from the deployed job's
		// build record because that record names the base image the image
		// runs the binary of.
		if compressionGate {
			if err := checkImageSince(ctx, env, slug, spec, kind, compressionSince,
				"agent.compression", "context compression", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The rigor: gate, judged on the file the run will read rather
		// than on a task field: see checkRigorImage. Unconditional, since
		// the repository's file alone can carry the key.
		if err := checkRigorImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The governance: gate, the same shape as the clone branch's
		// above and for the same reason.
		if err := checkGovernanceImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The project layer's jobs: block, the same shape and the same
		// reason again: see jobsSince.
		if err := checkJobsImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The security: block, the same shape as the rigor: gate above and
		// for the same reason: see checkSecurityImage.
		if err := checkSecurityImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The planning: block, the same shape and the same reason again:
		// see checkPlanningImage.
		if err := checkPlanningImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The same gate as the clone branch's above, and for the same
		// reason: see followUpSince, and the order is the clone branch's.
		if err := checkFollowUpImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The same gate as the clone branch's above, and for the same
		// reason: the request is a new task field, and a runner that
		// predates it refuses the whole spec at bootstrap — bytes in the
		// bucket under a run that never starts. Judged from the deployed
		// job's build record, and never reached by a launch that carries
		// no such field at all.
		if asksForRigor(spec) {
			if err := checkImageSince(ctx, env, slug, spec, kind, rigorRequestSince,
				rigorGateSubject(o, spec), "the rigor request", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The same gate as the clone branch's above, and for the same
		// reason: the tier is a new task field, a runner older than
		// governanceSince refuses the whole spec at bootstrap, and a
		// launch that carries none is never gated.
		if spec.Tier != "" {
			if err := checkImageSince(ctx, env, slug, spec, kind, governanceSince,
				tierGateSubject(o, spec), "the tier", "", warn); err != nil {
				return config.Inputs{}, err
			}
		}
		// The review levels' gate, the same as the clone branch's above
		// and for the same reason: see reviewLevelsAsk.
		if err := checkReviewLevelsImage(ctx, env, slug, spec, kind, levelsAsk, warn); err != nil {
			return config.Inputs{}, err
		}
		// The same gate as the clone branch's above, and for the same
		// reason: see checkNoneModelImage.
		if err := checkNoneModelImage(ctx, env, slug, spec, kind, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
		// The same gate as the clone branch's above, and for the same
		// reason: see checkStopFractionImage.
		if err := checkStopFractionImage(ctx, env, slug, spec, kind, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
		// The same gate as the clone branch's above, and for the same
		// reason: see checkTriageImage.
		if err := checkTriageImage(ctx, env, slug, spec, kind, fileCfg, warn); err != nil {
			return config.Inputs{}, err
		}
		// The digest drift, last of this branch's image reads, and the
		// only one that never refuses: a checkout: clone workflow has no
		// derived image and so no record to compare (the branch above),
		// and nothing here can stop a launch D619 ruled may not be
		// pinned.
		warnImageDigestDrift(ctx, env, slug, spec, warn)
	}
	// A checkout: shared workflow's job runs a shared dependency-layer
	// image, not a per-repository one: it is judged on the image the lock
	// says its job is on (checkSharedImageSince), in neither branch above —
	// the clone branch's judge is the local config's base image, which is
	// not what built the shared one, and the other branch's is a build
	// record a repository that adopted a sibling's image never wrote.
	if shared {
		if err := checkSharedImageSince(ctx, env, slug, spec, sharedSince,
			"this checkout: shared workflow", "how to clone for a shared image (FUGARO_CHECKOUT)", warn); err != nil {
			return config.Inputs{}, err
		}
	}
	if spec.Mode == recipe.ModeArtifact {
		// The mode: artifact gate: a task carrying mode is refused
		// outright by a runner older than artifactsSince (task.Parse
		// decodes with DisallowUnknownFields), so the launch must not
		// reach such a job at all. Judged from the deployed job's build
		// record exactly as the project layer's and the named token's
		// gates above are, and for an artifact recipe launched with
		// nothing attached just as much as for one launched with
		// --attach.
		if clone {
			if err := checkCloneImageSince(env.lc, spec, artifactsSince, "a mode: artifact recipe", "mode: artifact", "", warn); err != nil {
				return config.Inputs{}, err
			}
		} else if err := checkImageSince(ctx, env, slug, spec, kind, artifactsSince, "a mode: artifact recipe", "mode: artifact", "", warn); err != nil {
			return config.Inputs{}, err
		}
	}
	if spec.Mode == recipe.ModeEvaluate {
		// The mode: evaluate gate, artifactsSince's own shape: a runner
		// older than evaluateSince refuses a task carrying mode
		// outright ("evaluate" is not a value its task validation
		// knows), so a judge launch must not reach such a job at all.
		// Judged from the deployed job's build record exactly as the
		// gates above are, and from the task's own field rather than the
		// recipe's constructs: an evaluate recipe trips none of the
		// parse-time gates, so this is the only gate that can refuse the
		// launch before it is paid for.
		if clone {
			if err := checkCloneImageSince(env.lc, spec, evaluateSince, "a mode: evaluate recipe", "mode: evaluate", "", warn); err != nil {
				return config.Inputs{}, err
			}
		} else if err := checkImageSince(ctx, env, slug, spec, kind, evaluateSince, "a mode: evaluate recipe", "mode: evaluate", "", warn); err != nil {
			return config.Inputs{}, err
		}
	}
	if spec.Mode == recipe.ModeReview {
		// The review job's gate: a review launch starts the review-only
		// variant, and an image older than reviewJobSince cannot serve
		// one — below the variant's release there is no such job beside
		// its coding job, and between the two (0.19.0) there is one with
		// no model path for the tokenless container it is. Judged from
		// the deployed job's build record as
		// the gates above are, and on the mode alone — the field only
		// `fugaro review` writes — so a review never reaches a job that
		// would run it on the coder's container.
		if clone {
			if err := checkCloneReviewJobImage(env.lc, spec, warn); err != nil {
				return config.Inputs{}, err
			}
		} else if err := checkReviewJobImage(ctx, env, slug, spec, kind, warn); err != nil {
			return config.Inputs{}, err
		}
		// The installation's half of the same gate, and the half no job
		// image can show: the review job mounts no credential, so its
		// account comes from the credentials service, and only a service
		// from reviewAdmissionSince on admits the review job's own
		// caller. Judged on the installation rather than on the job,
		// because the job's image and the service's release are pinned by
		// different things (checkReviewAdmission).
		if err := checkReviewAdmission(env.lc, warn); err != nil {
			return config.Inputs{}, err
		}
	}
	// The author, last of the fields this launch decides: the key that
	// opted the repository in is judged first and refuses an image that
	// cannot read it, the field is dropped rather than refused where the
	// image is not known to read it (authorSkew), and only then is the
	// identity resolved from this machine's local config.
	if err := authorGates(ctx, env, slug, spec, kind, mode, warn); err != nil {
		return config.Inputs{}, err
	}
	// The seal's gate (D19): a launch that mints hands its execution the
	// seal key in the marker, which only a runner at sealSince or later
	// parses without echoing and reads the token for. Judged here, before
	// the claim, the mint or the object, like the gates above.
	if err := env.sealGate(ctx, slug, spec, kind, clone, warn); err != nil {
		return config.Inputs{}, err
	}
	// The proxy's gate (G14): a run of a repository whose Bitbucket
	// access is brokered is launched neither onto a job image whose
	// runner predates the proxy nor on the Batch backend. Judged beside
	// the seal's, from the same build record and the same local config,
	// and before the claim and the task like every gate here — a launch
	// that is not proxied is not judged at all (G9).
	if err := env.proxyGate(ctx, slug, spec, kind, clone, warn); err != nil {
		return config.Inputs{}, err
	}
	if err := checkMaxParallel(ctx, env); err != nil {
		return config.Inputs{}, err
	}
	// The artifact gate: locally computable (a recipe's caps against
	// the workflow's memory, a render or probe step against the
	// commands they need), so it runs before the budget read below and
	// before createTask writes anything at all.
	if err := checkArtifactLaunch(ctx, env, spec); err != nil {
		return config.Inputs{}, err
	}
	if !skipBudget {
		if err := env.budgetPrecheck(ctx, slug, spec, warn); err != nil {
			return config.Inputs{}, err
		}
	}
	// The project's policy, read with the operator's own credentials
	// before anything of this launch is written (plan task 1.5 step 4):
	// the repository must be admitted, the policy must not be newer than
	// this binary, and the job image the launch would run — judged on
	// whichever of the three checkout modes it is, the image lock included
	// — has to be one whose runner can read the node at all
	// (policyImageGate). Before
	// createTask and before the launch claim, so a refused launch leaves
	// no task, no token and no execution behind.
	// Outside the --no-budget-check branch, which is deliberate: neither
	// check is a budget check, so neither is an operator's to skip. After
	// the budget pre-check, so an unreachable database is refused by the
	// pre-check's own fail-closed message when that check runs; the gate
	// refuses it itself, with the node named, when it does not. Before the
	// attach upload below as well, so a repository the policy does not
	// admit never gets its inputs into the bucket either.
	if err := launchPolicyGates(ctx, env, slug, spec, kind, clone, shared, warn); err != nil {
		return config.Inputs{}, err
	}
	return caps, nil
}

// warnImageDigestDrift warns, once, when the workflow's image is no longer at
// the digest its build record names: the record and the registry's latest tag
// disagree, so what the job runs and what the record says it runs are not the
// same image. It is a warning and never a refusal, and it pins nothing: Cloud
// Run cannot vary the image per execution, so a launch has no way to run the
// recorded digest (D619) — it only tells the operator, whose fix is the daily
// image check or fugaro image refresh. Neither side's read can refuse the
// launch either: a workflow with no build record at all — a checkout: clone
// workflow runs the installation's base image and is never built — is silent,
// anything else the launcher cannot read is a warning, and an identity the
// registry refuses is silent too (registryDenied).
//
// The reference it reads is the installation's own image for this workflow —
// the same derivation the job spec uses (Workflow's registryPath and
// ImageName) — and never the record's own Image field: the record comes from
// the runs bucket, so building the outbound read's host out of it would let a
// hand-edited or forged record choose where the launcher connects, and a
// registry-path change after the build would leave the record naming an image
// this installation no longer has. A record naming no digest of its own is
// nothing to compare, so the registry is not read at all.
func warnImageDigestDrift(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, warn io.Writer) {
	skip := func(why string) {
		fmt.Fprintf(warn, "warning: the image digest of %s workflow %s is not compared with its build record: %s\n", spec.Repo, spec.Workflow, why)
	}
	host, err := infra.RegistryHost(env.lc)
	if err != nil {
		skip(oneLine(err.Error()))
		return
	}
	image := gcp.ImageName(host+"/"+gcp.RegistryRepoID(slug), slug, spec.Workflow)
	b, err := env.recordBucket(ctx)
	if err != nil {
		skip(oneLine(err.Error()))
		return
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return
	case err != nil:
		skip(oneLine(err.Error()))
		return
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		skip("the record is unreadable (" + oneLine(err.Error()) + ")")
		return
	}
	if rec.ImageDigest == "" {
		return
	}
	live, err := checkRegistry().Digest(ctx, image+":latest")
	switch {
	// A launchers-only identity holds no artifactregistry read (the
	// operator role has it): every launch would otherwise warn about a
	// permission it was never meant to have, which is not drift and not
	// anything its reader can act on. Silence here says "not compared" by
	// saying nothing, exactly as a workflow with no build record does.
	case registryDenied(err):
		return
	// The tag the build promoted the recorded digest to is gone — the
	// daily check's own latest-drift case (LatestMissing) — which is the
	// one read failure the operator can act on, so it is named as such.
	case errors.Is(err, imagecheck.ErrManifestUnknown):
		fmt.Fprintf(warn, "warning: the registry has no %s:latest, so the digest the build record of %s workflow %s names (%s) is not the one that tag resolves to\n", image, spec.Repo, spec.Workflow, oneLine(rec.ImageDigest))
		return
	case err != nil:
		skip(oneLine(err.Error()))
		return
	}
	if rec.DigestMismatch(live) {
		fmt.Fprintf(warn, "warning: the image of %s workflow %s is at digest %s, not the %s its build record names\n", spec.Repo, spec.Workflow, oneLine(live), oneLine(rec.ImageDigest))
	}
}

// registryDenied reports whether err is the registry refusing the launcher's
// identity rather than failing to answer: a 401 or 403 from the manifest read.
// The launcher role is granted run.jobs, run.executions, run.operations.get
// and secretmanager.list, and artifactregistry read goes to operators alone
// (deploy/terraform/gcp/modules/repo/registry.tf), so a principal that may
// launch and may not read the registry is the expected state rather than a
// fault. The check is textual because the error crosses the OCI client, which
// reports the status in its message and wraps nothing a caller can match on.
func registryDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403")
}

// taskText is the task from exactly one of TEXT and --task-file. With
// optional (a follow-up, whose runner has instructions of its own) both
// may be absent, and the task may be empty.
func taskText(cmd *cobra.Command, file string, args []string, optional bool) (string, error) {
	var text string
	switch {
	case file != "" && len(args) > 0:
		return "", userErr("pass the task as TEXT or with --task-file, not both")
	case file == "-":
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", userErr("reading the task from stdin: %v", err)
		}
		text = string(data)
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", userErr("reading the task: %v", err)
		}
		text = string(data)
	case len(args) == 1:
		text = args[0]
	case optional:
		return "", nil
	default:
		return "", userErr("pass the task as TEXT or with --task-file (- for stdin)")
	}
	if text = strings.TrimSpace(text); text == "" && !optional {
		return "", userErr("the task is empty")
	}
	return text, nil
}

// ownerRuledText is fugaro run --owner-ruled's value as the ruling to store
// (task.Spec.OwnerRuled): "-" reads stdin, a value naming a regular file reads
// that file, and every other value is the ruling as it was given. A value that
// happens to name an existing file is read rather than taken literally, which
// is what makes one flag carry "text or file" without a second flag saying
// which — and why the help text says so.
//
// The text is trimmed and its CRLFs are made newlines, and nothing else about
// it is touched: the ruling is a person's own words, appended to the brief
// verbatim, and a launcher that rewrote them would be answering for them.
// Blank and over-long rulings are refused here, in the launcher's words,
// rather than by the run at bootstrap: the rules are task.Spec.Validate's, so
// a ruling this accepts is never one the run refuses after it was paid for.
func ownerRuledText(cmd *cobra.Command, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	text := value
	switch {
	case value == "-":
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", userErr("reading the owner's ruling from stdin: %v", err)
		}
		text = string(data)
	default:
		if fi, err := os.Stat(value); err == nil && fi.Mode().IsRegular() {
			data, err := os.ReadFile(value)
			if err != nil {
				return "", userErr("reading the owner's ruling: %v", err)
			}
			text = string(data)
		}
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	switch {
	case text == "":
		return "", userErr("--owner-ruled is empty: pass the owner's ruling, a file holding it, or - for stdin")
	case len(text) > task.MaxOwnerRuledBytes:
		return "", userErr("--owner-ruled is %d bytes; a ruling must be at most %d", len(text), task.MaxOwnerRuledBytes)
	}
	return text, nil
}

// titleFlag resolves fugaro run --title into the title the task stores, in
// o.title: the flag trimmed, checked against task's ONE list of what a
// title may be (task.TitleProblem, the same list the runner applies when it
// reads a task back, so a title this accepts is never one the run refuses
// at bootstrap) and sanitised the way every other string this program shows
// is (safetext.Field: bidi and zero-width characters dropped, runs of
// whitespace collapsed to one space).
//
// The flag's absence is not a title, and leaves o.title empty — every
// surface then falls back to the task's first line, exactly as before the
// flag existed. Its PRESENCE with nothing in it is a mistake and is
// refused: --title "" would otherwise be silently the same as no --title at
// all, which is the one reading nobody meant. The emptiness check runs
// again on the sanitised value, because the characters safetext.Field drops
// (zero-width spaces and joiners, bidi embeddings and overrides, word
// joiners, soft hyphens, the BOM) are not whitespace: a title made only of
// them passes the trim and the check, and would otherwise be stored as no
// title at all — the same silent no-op, one layer down.
func titleFlag(cmd *cobra.Command, o *runOptions) error {
	if !cmd.Flags().Changed("title") {
		return nil
	}
	title := strings.TrimSpace(o.title)
	if p := task.TitleProblem(title); p != "" {
		return userErr("--title: %s", p)
	}
	if o.title = safetext.Field(title); strings.TrimSpace(o.title) == "" {
		return userErr("--title: %s", "is empty (or nothing but whitespace and invisible characters)")
	}
	return nil
}

// reviewLevelsFlag resolves fugaro run --review-levels into the block the
// task stores, in o.reviewLevels, and nil when the flag is not given: the
// document is read (the file, or stdin for -) and decoded by
// config.ParseReviewLevelsYAML — strictly, and validated — so an unknown key,
// an effort outside the closed set, a focus over its bound or the level's own
// switch (`none: true`, or its model named none) beside another key is refused
// here, by the launcher, rather than by the runner at bootstrap after the
// launch was paid for.
//
// The flag's absence is not a block, and leaves o.reviewLevels nil: the
// launch writes no field and the run resolves its levels from its own
// configuration and its mode, exactly as before the flag existed (L3.1's
// resolver is then the only writer). Its presence with nothing readable in it
// is refused, like an empty --title: a launch that meant to ask for levels
// and asked for none would otherwise be indistinguishable from one that did
// not use the flag.
func reviewLevelsFlag(cmd *cobra.Command, o *runOptions) error {
	if !cmd.Flags().Changed("review-levels") {
		return nil
	}
	var data []byte
	var err error
	switch o.reviewLevelsPath {
	case "":
		return userErr("--review-levels: pass a file, or - for stdin")
	case "-":
		if data, err = io.ReadAll(cmd.InOrStdin()); err != nil {
			return userErr("reading the review levels from stdin: %v", err)
		}
	default:
		if data, err = os.ReadFile(o.reviewLevelsPath); err != nil {
			return userErr("reading the review levels: %v", err)
		}
	}
	levels, err := config.ParseReviewLevelsYAML(data)
	if err != nil {
		return userErr("--review-levels: %v", err)
	}
	o.reviewLevels = levels
	return nil
}

// planTaskFlag resolves fugaro run --plan-task into the id the task stores,
// in o.planTask: the flag trimmed, checked against task's ONE list of what a
// plan task id may be (task.PlanTaskProblem, the same list the runner applies
// when it reads a task back, so an id this accepts is never one the run
// refuses at bootstrap).
//
// The flag's absence is not a claim about any plan, and leaves o.planTask
// empty — every launch before the flag existed, and every launch that does
// not use it. A present but empty value (--plan-task "") is refused: it is
// indistinguishable from no flag at all once stored, and the reading it would
// get silently — "this run was not asked to join anything" — is the one
// reading nobody meant.
//
// The value is NOT sanitised the way a title is: a plan task id is a shape,
// not prose, and the shape check already allows only ASCII letters, digits,
// '.', '_', '-' and the '/' between them (safetext.Field, which drops bidi
// overrides and invisible characters and re-spaces text, cannot change such a
// value — and a value it WOULD change is one PlanTaskProblem refuses).
func planTaskFlag(cmd *cobra.Command, o *runOptions) error {
	if !cmd.Flags().Changed("plan-task") {
		return nil
	}
	o.planTask = strings.TrimSpace(o.planTask)
	if p := task.PlanTaskProblem(o.planTask); p != "" {
		return userErr("--plan-task: %s", p)
	}
	return nil
}

// rigorFlags is the run's own rigor request, validated once for every launch
// shape that accepts it (a first run, a best-of-N batch, a review, a
// follow-up): the mode against the closed vocabulary (rigor.Modes, D20), and
// the declared paths against the two bounds a task spec carries
// (task.MaxDeclaredPaths, task.MaxDeclaredPathBytes).
//
// It runs before openCloud and before any write, so a near miss — "Poc", a
// trailing space, "sizzle" — is the launcher's own mistake, refused with the
// vocabulary in the message, rather than a value the runner reads as an
// unknown mode and quietly ignores, which is the fail-open direction. The
// same rules are applied again by task.Spec.Validate to whatever a task
// carries, so a hand-written task.json cannot smuggle one past a launcher.
func rigorFlags(o *runOptions) error {
	if o.rigor != "" && !rigor.Known(rigor.Mode(o.rigor)) {
		return userErr("--mode %q: use %s, or leave it off to resolve the mode from the run's own configuration", o.rigor, rigor.ModeList())
	}
	if len(o.paths) > task.MaxDeclaredPaths {
		return userErr("--path: at most %d paths may be declared, got %d", task.MaxDeclaredPaths, len(o.paths))
	}
	for i, p := range o.paths {
		switch {
		case strings.TrimSpace(p) == "":
			return userErr("--path: path %d is blank; a declared path is a repository-relative path such as internal/payments/x.go", i+1)
		case len(p) > task.MaxDeclaredPathBytes:
			return userErr("--path: path %d is longer than %d bytes", i+1, task.MaxDeclaredPathBytes)
		}
	}
	return nil
}

// tierFlag is the expectation tier the run's own launcher asks for, validated
// once for every launch shape that accepts it (a first run, a best-of-N batch,
// a review, a follow-up): the tier against the closed vocabulary (tier.Tiers,
// spec §3).
//
// Like rigorFlags it runs before openCloud and before any write, so a near
// miss — "Trivial", a trailing space, "huge" — is the launcher's own mistake,
// refused with the vocabulary in the message, rather than a value the runner
// reads as an unknown tier. The same rule is applied again by
// task.Spec.Validate to whatever a task carries, so a hand-written task.json
// cannot smuggle one past a launcher.
func tierFlag(o *runOptions) error {
	if o.tier != "" && !tier.Known(tier.Tier(o.tier)) {
		return userErr("--tier %q: use %s, or leave it off to dispatch the run with no expectation", o.tier, tier.List())
	}
	return nil
}

// noteRigorRequest says, once per launch, when --mode asks for a mode the
// layers already meet: the request may raise and never lower (D6), so such a
// request is recorded as asked and changes nothing, and a launcher that typed
// it — `--mode hotfix` above all, which ranks below every on-axis mode —
// would otherwise believe it took effect. It reads the checkout's fugaro.yaml
// with the project layer, the closest thing to the base branch the launcher
// has; without a checkout of repo it cannot tell, and says nothing. Never an
// error: the runner, reading the base branch, is the one that decides.
func noteRigorRequest(ctx context.Context, o *runOptions, repo string, warn io.Writer) {
	if o.rigor == "" {
		return
	}
	c := checkoutConfig(ctx, repo)
	if c == nil {
		return
	}
	cfg := c.RigorConfig()
	if rigor.Resolve(cfg, rigor.Request{Mode: rigor.Mode(o.rigor), Paths: o.paths}).From == rigor.FromRequest {
		return
	}
	resolved := rigor.Resolve(cfg, rigor.Request{Paths: o.paths}).Mode
	fmt.Fprintf(warn, "--mode %s is not higher than the repository's resolved mode %s: it has no effect\n", o.rigor, resolved)
}

// attemptPlan is everything a new run's attempts share, resolved once: the
// repository, workflow, ref, account and recipe. newSpec (a single launch)
// and runAttempts (every attempt of a best-of-N batch) both build one, then
// stamp a fresh run id onto it per spec (buildAttemptSpec): a project recipe
// published mid-batch must not give a batch's later attempts a different
// recipe from its first (design generic-tool review, 2026-10-13).
type attemptPlan struct {
	repo, slug, workflow, ref, me, account, totalOverride string
	// base is the task's base_branch field, the branch the runner resolves the
	// configuration and the pull request's base from: the layers the launcher
	// read when one of them (and not the checkout's own file) named it, and ""
	// otherwise, so the spec stays byte-identical to the previous release's for
	// a launch that changes nothing (specBaseBranch).
	base   string
	recipe *task.Recipe
	// mode is the resolved recipe's mode when the runner cannot read it
	// from the task alone (mode: artifact today): buildAttemptSpec writes it
	// into the spec, and it is empty for implement and review, so those
	// specs stay byte-identical to what the previous release wrote.
	mode string
	// digestRuns is the manifest of a report run (run_digest_manifest.go):
	// this repository's runs its digest step may read, listed once here so
	// every attempt of a batch carries the same list, and empty for every
	// other launch, whose task stores no manifest at all.
	digestRuns []string
}

// resolveAttemptPlan resolves everything a new run's spec needs except its
// run id, from the flags, the local config and the checkout in the working
// directory. forAttempts is true only for runAttempts: chooseRecipe then
// gives a batch-appropriate refusal for a mode: review recipe, since
// --attempts cannot take the --pr a plain launch's refusal suggests.
func resolveAttemptPlan(ctx context.Context, env *cloudEnv, o *runOptions, total time.Duration, warn io.Writer, forAttempts bool) (*attemptPlan, error) {
	repo := o.repo
	var err error
	if repo, err = repoArg(ctx, o.cloud, repo); err != nil {
		return nil, err
	}
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return nil, userErr("--repo: %v", err)
	}
	if err := refuseSharedCheckout(ctx, repo); err != nil {
		return nil, err
	}
	checkout := checkoutOf(ctx, repo)
	slug, err := env.repoSlug(repo, checkout)
	if err != nil {
		return nil, err
	}
	// The base branch is resolved before the ref. An explicit ref is the
	// branch the run is cut from, not its base, and the file that decides a
	// base under one is the file at that ref — which only the runner reads,
	// and it is read first there (ResolveConfig takes the task's base only
	// where the file writes none). So a --ref launch carries the two layers
	// below the file, and a launch without one carries all four.
	ref, base := o.ref, ""
	if ref == "" {
		b, source := launchBaseBranch(ctx, env, repo)
		base, ref = resolvedBaseBranch(b, source), newRunRef(ctx, repo, b, warn)
	} else {
		base = specBaseBranch(env.layersBaseBranch(ctx, repo))
	}
	// The launch's own document, resolved here and before every reader of it,
	// and not left to finishLaunch: the plan's readers below are the launch's —
	// the workflow, the total-timeout check, the recipe — and a reader that
	// judged the working tree while the launch judged the ref's file decides by
	// a document the run never reads, which is what the runner resolves after
	// cloning (the workflow and agent.recipe among it). Here, because the ref
	// the read is made at is what the two lines above resolve. Idempotent, so
	// finishLaunch's own call — the one a launch shape with no attempt plan
	// behind it (a follow-up, a review, a retry, the daemon's) still needs —
	// answers with this same document.
	ctx = withLaunchDocument(ctx, repo, ref)
	// The closure above is bound to the working tree and memoizes it, so it is
	// built again over the ctx that carries the launch's document: every reader
	// below is one of the launch's, and what they resolve is written into the
	// task the runner applies at the ref.
	checkout = checkoutOf(ctx, repo)
	workflow, err := resolveWorkflow(env, repo, o.workflow, checkout)
	if err != nil {
		return nil, err
	}
	var totalOverride string
	if total > 0 {
		if c := checkout(); c != nil {
			if w, ok := c.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
		totalOverride = total.String()
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	// Resolved before chooseRecipe (review finding E): chooseRecipe's
	// resolveRecipe can read a project or catalog recipe from the bucket, a
	// network call a disallowed --token should never trigger — the account
	// is checked against config.AllowedClaudeAccounts first, using only the
	// local config and the checkout already in hand, no network of its own.
	account, err := claudeAccountFor(ctx, env, repo, o.token, "", ref, checkout())
	if err != nil {
		return nil, err
	}
	root := checkoutRoot(ctx, repo)
	rcp, mode, err := chooseRecipe(ctx, env, o.recipe, checkout, root, warn, forAttempts)
	if err != nil {
		return nil, err
	}
	// The report run's manifest is listed here, once per launch, and before
	// the launch itself: the listing and the horizon it needs are cloud
	// calls of their own, but they come before the task is stored and the
	// execution started, so a repository already writing its reports is
	// refused without launching anything. A batch never reaches this with a
	// report recipe (chooseRecipe refuses --attempts for one), so no
	// attempt reads another's stored task as a report run still going.
	digestRuns, err := digestManifest(ctx, env, slug, root, rcp, time.Now(), warn)
	if err != nil {
		return nil, err
	}
	return &attemptPlan{repo: repo, slug: slug, workflow: workflow, ref: ref, base: base, me: me, account: account, totalOverride: totalOverride, recipe: rcp, mode: mode, digestRuns: digestRuns}, nil
}

// buildAttemptSpec stamps a fresh run id (or o.runID, when given) onto plan,
// building one spec; runAttempts calls it once per attempt of a batch, each
// getting its own id over the same plan.
func buildAttemptSpec(plan *attemptPlan, o *runOptions, text string) (string, *task.Spec, error) {
	runID := o.runID
	if runID == "" {
		var err error
		if runID, err = runIDGenerator(); err != nil {
			return "", nil, err
		}
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: plan.repo, Ref: plan.ref, BaseBranch: plan.base, Workflow: plan.workflow, Task: text, Title: o.title,
		PlanTask: o.planTask, RequestedBy: plan.me, Batch: o.batch, Recipe: plan.recipe, Mode: plan.mode,
		Rigor: rigor.Mode(o.rigor), Paths: o.paths, Tier: tier.Tier(o.tier), DigestRuns: plan.digestRuns}
	if plan.totalOverride != "" {
		spec.Overrides.TotalTimeout = plan.totalOverride
	}
	if plan.account != config.DefaultClaudeAccount {
		spec.OAuthToken = plan.account
	}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return plan.slug, spec, nil
}

// runIDGenerator is task.NewRunID's call in production; a test swaps it to
// force a run id collision inside a best-of-N batch (buildUniqueAttemptSpec
// retries on one).
var runIDGenerator = func() (string, error) { return task.NewRunID(time.Now(), crand.Reader) }

// newSpec builds a new run's spec, and its repository's slug, from the
// flags, the local config and the checkout in the working directory.
func newSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string, total time.Duration, warn io.Writer) (string, *task.Spec, error) {
	plan, err := resolveAttemptPlan(ctx, env, o, total, warn, false)
	if err != nil {
		return "", nil, err
	}
	warnTaskCutsBranch(warn, text, plan.ref)
	return buildAttemptSpec(plan, o, text)
}

// batchResult is fugaro run --attempts's --json output: the batch label,
// how many attempts it was meant to launch, and each one's ordinary
// launchResult, in launch order. Incomplete is set only on a partial batch
// (a failure stopped the loop before Attempts were launched), so a reader
// never mistakes len(Runs) < Attempts for success.
type batchResult struct {
	Batch      string         `json:"batch"`
	Attempts   int            `json:"attempts"`
	Incomplete bool           `json:"incomplete,omitempty"`
	Runs       []launchResult `json:"runs"`
}

// launchAttempt is finishLaunch's call in production; a test swaps it to
// fail a chosen attempt of a batch, to exercise runAttempts's partial-
// failure reporting without a live fault in the backend fake.
var launchAttempt = finishLaunch

// resolveBatchPlan is runAttempts's one call to resolveAttemptPlan in
// production; a test swaps it to count calls and so prove it runs once per
// batch, not once per attempt (design generic-tool review, 2026-10-13: a
// recipe published mid-batch must not give a batch's later attempts a
// different recipe from its first).
var resolveBatchPlan = resolveAttemptPlan

// runAttempts launches o.attempts (2 to 4) independent runs of the same
// task, workflow, ref and recipe, as a best-of-N batch (design generic-tool
// G13): the plan (repository, workflow, ref, account and recipe) is
// resolved once and shared by every attempt, each stamped with its own run
// id over it (buildUniqueAttemptSpec), through the ordinary finishLaunch.
// It never partially launches a batch the max_parallel cap or the budget
// cannot hold, and it never counts a run it did not itself start: a failure
// after k launches (including finishLaunch reporting "already-launched",
// which here can only mean a run id collision slipped past the retry)
// reports those k and returns the error.
func runAttempts(cmd *cobra.Command, env *cloudEnv, o *runOptions, text string, total time.Duration) error {
	ctx := cmd.Context()
	// One read per command, not one per attempt (spec.md §6, D904): every
	// attempt of a batch runs the same repository's document at the same ref,
	// so the batch asks the forge one question, and a read mint is an act the
	// credentials service audits. Installed over the whole batch, before the
	// plan below, because the cache finishLaunch installs is one launch's own —
	// the attempts share this context and would otherwise each pay a mint.
	ctx = withServiceReadCache(ctx)
	warn := cmd.ErrOrStderr()
	n := o.attempts
	// A batch prints one step per attempt, and stops the run on the attempt
	// that failed: the launcher returns the error that says which of how many
	// it was, and the step above it says the same thing on the screen.
	// Resolved, including the recipe, before checkMaxParallelFor's backend
	// listing call and the budget pre-check's database read (design
	// generic-tool review, 2026-10-13): a batch a review-mode recipe (or an
	// unresolvable one) refuses outright must not first pay for either.
	plan, err := resolveBatchPlan(ctx, env, o, total, warn, true)
	if err != nil {
		return err
	}
	noteRigorRequest(ctx, o, plan.repo, warn)
	warnTaskCutsBranch(warn, text, plan.ref)
	if err := checkMaxParallelFor(ctx, env, n); err != nil {
		return err
	}
	if err := env.batchBudgetPrecheck(ctx, plan.slug, plan.repo, n, warn); err != nil {
		return err
	}
	// The batch's reporter opens where its own refusals end — a recipe that
	// does not resolve, a mode the batch cannot run, no room for all the
	// attempts, no budget headroom — so a refused batch keeps no log, and
	// from here every attempt is one step of this run (D1352, D1354).
	rep, sink := reporterFor(cmd, "run", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	out := loggedOut(cmd.OutOrStdout(), sink)
	seen := make(map[string]bool, n)
	results := make([]launchResult, 0, n)
	for i := 0; i < n; i++ {
		step := rep.Step(fmt.Sprintf("launch %d of %d", i+1, n))
		slug, spec, err := buildUniqueAttemptSpec(plan, o, text, seen)
		if err != nil {
			failStep(rep, step, err)
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, err)
		}
		if i == 0 && o.batch == "" {
			// With no --batch, the first attempt's own run id names the
			// batch (design generic-tool §3.6): set once buildUniqueAttemptSpec
			// has generated it, then kept in o.batch so every later
			// attempt's spec carries the same label.
			spec.Batch = "bestof-" + spec.RunID
			if err := spec.Validate(); err != nil {
				failStep(rep, step, err)
				return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, userErr("%v", err))
			}
			o.batch = spec.Batch
		}
		seen[spec.RunID] = true
		// skipBudget: the batch's own budget pre-check above already leased
		// n * MinLaunchHeadroom once; a per-attempt re-check here would both
		// re-evaluate state it already judged and print its notes n times.
		res, err := launchAttempt(ctx, env, o, slug, spec, false, false, warn, true)
		if err != nil {
			failStep(rep, step, err)
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, err)
		}
		if res.Status == "already-launched" {
			// Not a fresh launch: buildUniqueAttemptSpec's retry already
			// guards against a run id this loop generated twice, so this can
			// only be some other process's run reusing the id — too
			// improbable to recover from; the batch must not silently count
			// a run it did not itself start.
			err := fmt.Errorf("run %s was already launched, not by this batch", task.ShortRun(oneLine(res.Run)))
			failStep(rep, step, err)
			return reportBatchFailure(cmd, o.batch, n, o.asJSON, results, i, err)
		}
		step.Done(launchWhat(res))
		if !o.asJSON {
			if err := launchDetail(out, res); err != nil {
				return err
			}
		}
		results = append(results, res)
	}
	if o.asJSON {
		if err := printBatch(out, o.batch, n, false, results); err != nil {
			return err
		}
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("--pr <%c>", 'A'+i)
	}
	fmt.Fprintf(warn, "pick: fugaro run --recipe pick %s once all have opened PRs (fugaro runs ls --batch %s)\n",
		strings.Join(parts, " "), o.batch)
	rep.Summary()
	return nil
}

// buildUniqueAttemptSpec is buildAttemptSpec, retried while its fresh run id
// collides with one already seen in this batch (task.NewRunID's own
// resolution, a timestamp plus two random bytes, makes two attempts in the
// same second collide about 1 time in 65536: rare, but a silent duplicate
// would make the batch report N results for N-1 real runs). o.runID is
// always "" here (--attempts refuses --run-id), so each retry calls
// runIDGenerator again.
func buildUniqueAttemptSpec(plan *attemptPlan, o *runOptions, text string, seen map[string]bool) (string, *task.Spec, error) {
	const maxRetries = 8
	for try := 0; ; try++ {
		slug, spec, err := buildAttemptSpec(plan, o, text)
		if err != nil || !seen[spec.RunID] {
			return slug, spec, err
		}
		if try >= maxRetries {
			return "", nil, userErr("could not generate a run id that was not already in this batch after %d tries", maxRetries+1)
		}
	}
}

// reportBatchFailure prints the runs a batch already launched (never more:
// launched runs are never cancelled behind the user's back) before
// returning the error that stopped it, wrapped with which attempt failed,
// out of how many, and the run ids that did launch. Text mode already
// printed each as it launched; JSON mode holds everything for one document,
// so it prints the partial one here instead, marked Incomplete.
func reportBatchFailure(cmd *cobra.Command, batch string, n int, asJSON bool, results []launchResult, i int, err error) error {
	if asJSON {
		_ = printBatch(cmd.OutOrStdout(), batch, n, true, results)
	}
	ids := make([]string, len(results))
	for j, r := range results {
		ids[j] = task.ShortRun(r.RunID)
	}
	return fmt.Errorf("attempt %d of %d: %w; launched %d (batch %s): %s", i+1, n, err, len(results), batch, strings.Join(ids, ", "))
}

// printBatch is fugaro run --attempts's --json output: one document for the
// whole batch, not one per run.
func printBatch(w io.Writer, batch string, attempts int, incomplete bool, results []launchResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(batchResult{Batch: batch, Attempts: attempts, Incomplete: incomplete, Runs: results})
}

// reviewSpec is the task of a review-mode launch: fugaro run --recipe NAME
// --pr A [--pr B ...], NAME naming a mode: review recipe (rr, already
// resolved and confirmed by the caller). It reviews any open pull request
// of the repository, not only Fugaro's own (design generic-tool §3.6,
// G12): there is no branch, no previous run and no follow-up chain, unlike
// prSpec's. --token is honoured the same way a first run's is (newSpec):
// there is no previous run's account to inherit, so a review-mode launch
// is always resolved from the flag, else agent.oauth_token, else default,
// never silently ignored.
func reviewSpec(ctx context.Context, env *cloudEnv, o *runOptions, rr *resolvedRecipe, text string, total time.Duration, warn io.Writer) (string, *task.Spec, error) {
	repo := o.repo
	var err error
	if repo, err = repoArg(ctx, o.cloud, repo); err != nil {
		return "", nil, err
	}
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return "", nil, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", nil, userErr("--repo: %v", err)
	}
	if err := refuseSharedCheckout(ctx, repo); err != nil {
		return "", nil, err
	}
	checkout := checkoutOf(ctx, repo)
	slug, err := env.repoSlug(repo, checkout)
	if err != nil {
		return "", nil, err
	}
	// The layers, and the field, as a first run's (resolveAttemptPlan) with
	// one case short: a review run reads its own configuration from the branch
	// it is cut from — the runner's readBaseConfig refuses a file whose base
	// is not that branch — so an explicit --ref writes no field. There, the
	// field would name a branch other than the ref, and the run would be
	// refused rather than held to it; the file at that ref is the judge, as it
	// is for a first run.
	ref, base := o.ref, ""
	if ref == "" {
		b, source := launchBaseBranch(ctx, env, repo)
		base, ref = resolvedBaseBranch(b, source), b
	}
	// As resolveAttemptPlan: the launch's own document before the readers
	// below, which are the launch's too (the workflow and the total-timeout
	// check), and the closure rebuilt over it — the one above is bound to the
	// working tree and memoizes it.
	ctx = withLaunchDocument(ctx, repo, ref)
	checkout = checkoutOf(ctx, repo)
	workflow, err := resolveWorkflow(env, repo, o.workflow, checkout)
	if err != nil {
		return "", nil, err
	}
	if total > 0 {
		if c := checkout(); c != nil {
			if w, ok := c.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return "", nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return "", nil, userErr("%v", err)
	}
	runID := o.runID
	if runID == "" {
		if runID, err = task.NewRunID(time.Now(), crand.Reader); err != nil {
			return "", nil, err
		}
	}
	account, err := claudeAccountFor(ctx, env, repo, o.token, "", ref, checkout())
	if err != nil {
		return "", nil, err
	}
	printRecipeNote(warn, rr, true)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, BaseBranch: base, Workflow: workflow, Task: text, Title: o.title, PlanTask: o.planTask,
		RequestedBy: me, Batch: o.batch, Recipe: rr.taskRecipe(), ReviewPRs: append([]int(nil), o.pr...),
		Rigor: rigor.Mode(o.rigor), Paths: o.paths, Tier: tier.Tier(o.tier)}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if account != config.DefaultClaudeAccount {
		spec.OAuthToken = account
	}
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return slug, spec, nil
}

// claudeAccountFor is the named Claude account a first run uses: flag
// (fugaro run --token), else stages.<stage>.claude_account for the stage asked
// for, else agent.oauth_token of the fugaro.yaml at the ref the run checks
// out, else the project's default_claude_account, else the unsuffixed default
// (config.DefaultClaudeAccount). That is rungs 1 to 5 of the accounts design's
// chain (docs/accounts-add/spec.md §11.2, D873), and stage is "" for the run's
// own account — the three launch call sites ask for that, and rung 2 is walked
// only when a stage is named, because a run's own credential is one account
// and not a stage's. A non-default name is refused (claudeAccountAllowed)
// unless config.AllowedClaudeAccounts (the local config's claude_accounts)
// lists it for repo, before any launch (design
// docs/design/named-oauth-tokens.md §3 T7) — checked whether or not a checkout
// is available here, so --repo X --token N outside any checkout is held to the
// same rule a checkout's own resolution would be.
//
// Every name this function reads out of the local config — the flag, the
// stage key of rung 2 and the project default of rung 4 — resolves through the
// file's aliases first (localcfg.Config.ResolveAccount, D2091, D2092), and
// before any rule is asked of it: the account the run is given is the account
// the alias stands for, the allow-list is asked about that account and never
// the alias (F6), and the task carries the account rather than the name the key
// holds (F4). A repo's own committed fugaro.yaml names an account and never an
// alias — an alias lives in the owner's local config alone (D2090) — so rung 3
// has nothing to resolve.
//
// The mix rule is judged first, for every call and whatever stage was asked
// for (config.CheckStageAccountMix): an api-key run may name one Claude
// account, for the senior review and the adversarial review and no other stage
// (docs/design/mixed-credentials.md §11, the owner's B0 ruling), and a run
// under vertex or cascade may name none at all. That refusal belongs to the
// launch rather than to the stage it was asked about: a config that declares a
// mix the ruling does not admit is refused before anything is read or started,
// and the sentence names the key that has to change. Its budget-mode clause
// follows it in the same place (config.CheckMixBudget): a declared mix is
// admitted only under budget.mode: enforce, because that is the only mode that
// routes a call to a provider at all, and only there does a provider key sit in
// the gateway beside the account.
//
// Rung 4 (default_claude_account) fills a *stage* account for an api-key run
// too, under the condition the owner ruled on 2026-11-05 (§11, amending the
// 2026-11-04 conservative reading): the stage asked for is one of the mix's
// two, its own model is an Anthropic one, the coder is served by a route this
// repository may use, and the mode the run resolves is enforce (mixedStageAccount
// is the whole rule). Elsewhere it is not a rung for a stage at all, and such
// a run's coder stays on the provider key the gateway holds whatever its own
// account resolves to. Rung 4 is never a way to a coder's stage, and never a
// second account: the run holds one Claude account, which rung 2 names when it
// names one (mixedStageAccount refuses a project default that disagrees).
//
// A mixed run is the one case where the launch's *own* account is the mix's
// (mixedRunAccount, below the walk): spec.OAuthToken is what names the job
// variant, the account's job is the only one that mounts its token, and the
// review stages read the token out of the runner's environment — so a launch
// that resolved the unsuffixed default would start a run whose own review
// stage is refused for a token nothing mounted (Check 290's failure). It is
// the launch's account and never the run's *credential*: the coder's stages
// stay on the provider key the gateway holds, and the runner builds each
// stage's environment (B3). Nothing but the mix's account is admitted either:
// a name the walk resolves to something else under api-key is
// claudeAccountAllowed's refusal, and the flag and a follow-up's inherited
// name are held to it the same way.
//
// Both halves are the run's, and neither is the working tree's. The account is
// read at the ref the run checks out, out of this checkout's git objects
// (agentAtRef/checkoutFugaroYAMLAtRef), because the committed fugaro.yaml is
// the only place that may name an account (docs/design/named-oauth-tokens.md
// T2) — a working tree left on another branch, or carrying an uncommitted
// edit, would otherwise select the account the launch runs as: the job it
// lands on holds that account's token, and that account's allow_repos is what
// decides whether this repository may use it. The auth is the *run's* too, and
// that means the ref's bytes resolved over the project layer (agentAtRef's
// second answer), not the ref's bytes alone: agent.auth is a project-scope,
// layerable key (docs/design/layered-config.md's scopes table), so the run
// resolves it layer-applied (projectJobAgentAuth) — a launcher that read the
// raw file would answer Fugaro's own default (applyDefaults' vertex) for the
// shape projects adopt accounts with, a layer whose defaults say
// `agent.auth: oauth` over a repository that only names an account, and refuse
// a launch the run resolves as oauth and doctor reports as fine.
//
// ref is "" when the launch has no ref yet — the pre-check runRun makes with
// --pr and an explicit --recipe, before the previous run's ref is known (which
// reviewSpec repeats with the ref in hand) — and reading it may also fail for a
// ref this checkout does not hold. Both leave the working tree's own value
// standing, which is what every launch had before this read existed: the run
// itself resolves the ref's fugaro.yaml after cloning it, and a launch onto a
// shared project job whose own credential cannot be resolved is refused
// outright (projectJobAgentAuth) rather than judged on a guess.
func claudeAccountFor(ctx context.Context, env *cloudEnv, repo, flag, stage, ref string, co *config.Config) (string, error) {
	account, _, err := claudeAccountChoice(ctx, env, repo, flag, stage, ref, co)
	return account, err
}

// claudeAccountChoice is claudeAccountFor with the alias the account was read
// through: "" when the name was the account's own, a committed
// agent.oauth_token (D2090 — no alias lives outside the owner's local config)
// or the unsuffixed default, and the alias itself when the flag, a stage key or
// the project default named one. It is the resolution's own answer, carried out
// of the walk rather than read again from the keys: the launch's line prints
// the pair the account was really read through (docs/account-aliases/spec.md
// §7, D2099), and a second reading of the same keys could name an alias the
// walk never consulted. Callers that only need the account keep calling
// claudeAccountFor.
func claudeAccountChoice(ctx context.Context, env *cloudEnv, repo, flag, stage, ref string, co *config.Config) (string, string, error) {
	a, refAuth, refAgent := agentAtRef(ctx, env, repo, ref)
	// The auth a name is judged against, as the run resolves it: the document
	// at the ref over the project layer when that could be read, and the
	// checkout's own, layer-applied resolution otherwise.
	auth := ""
	if co != nil {
		auth = co.Agent.Auth
	}
	if refAgent {
		auth = refAuth
	}
	// The stage accounts this run applies, and the one note that says which of
	// the operator's project-wide keys a cascade run skips
	// (config.CascadeSkippedStageAccounts is the rule and the sentence): the
	// keys name accounts no rung of this launch reads, and refusing them would
	// let one repository's auth decide the whole project's config. The note is
	// printed once per launch, whatever stage asked for it.
	declared, note := config.CascadeSkippedStageAccounts(auth, stageAccountsOf(env.lc))
	if note != "" && !env.stageAccountNote {
		env.stageAccountNote = true
		fmt.Fprint(launchWarn, noteLine(note))
	}
	if ps := config.CheckStageAccountMix(auth, declared, "stages"); len(ps) > 0 {
		return "", "", userErr("%s: %s", ps[0].Path, ps[0].Message)
	}
	// Every *declared* stage account is checked before any launch, whatever rung
	// ends up using it: this call may not use one as the run's own credential,
	// but the run's review stages will, and the account's allow_data_to is what
	// decides whether this repository may put its code where that token is. The
	// check is the allow-list's, not claudeAccountAllowed's whole rule — its
	// auth clause keeps a named *run* account out of every auth but oauth, and
	// an api-key run is exactly the run the ruling gives a stage account to. It
	// is asked of what the config *declares* rather than of what this call can
	// resolve, because an auth the launch cannot place yet ("" — see
	// stageAccountOf) must not be the thing that skips the allow-list.
	for _, at := range slices.Sorted(maps.Keys(declared)) {
		if err := claudeAccountMembership(env, repo, declared[at]); err != nil {
			return "", "", err
		}
	}
	// The project default is a name too, and it is held to this repository's
	// allow-list exactly where rung 4 would hand it to something. Untying it
	// from the rung was a bug of the first cut: the key is written once for
	// every run the project has, so a default no stage of *this* run can use
	// must not refuse the launch — an api-key run with a provider reviewer is
	// the ruling's own "no mix and no error", and under vertex and cascade no
	// Claude OAuth credential reaches the run at all. Each auth's own reader
	// therefore does the checking:
	//
	//   - api-key: the mix is the only way the default reaches a stage, so
	//     mixedStageAccount holds it there (the stages it may serve, and no
	//     other), and a run whose reviews are not Claude's checks nothing;
	//   - oauth: the walk below ends in claudeAccountAllowed, which checks the
	//     default whenever it is what the run picked as its own credential,
	//     while a --token or an agent.oauth_token that names an allowed account
	//     leaves the default unused and unjudged;
	//   - vertex, cascade: the key can never apply, and a refusal would be about
	//     a name no rung of such a run reads.
	//
	// What no rung reads is an auth the launch cannot place yet (""): a
	// repository whose own committed fugaro.yaml carries the value is exactly
	// the launch that has to fail closed, so the default is checked here, where
	// nothing else will.
	//
	// The default resolves once, here, and the three readers below take the
	// resolved name: this check, the budget clause and rung 4. A key may name an
	// alias (D2091, D2092), and every one of them is asking about the account
	// the run holds — an alias handed to any of them would be judged as a name
	// no account answers to, which is the state F6 exists to keep out of the
	// launch (the allow-list is asked about the account, never the alias). The
	// alias the key named is kept beside it for the same reason rung 4 keeps the
	// resolved name: the line the launch prints names the pair only where rung 4
	// is what read the key.
	def, defAlias := env.lc.ResolveAccount(env.lc.DefaultClaudeAccount)
	if def != "" && def != config.DefaultClaudeAccount {
		switch auth {
		case "api-key":
			// The account it returns is the stage's resolution and not this
			// launch's; what the launch wants here is the rule's own refusals,
			// raised for a stage the run has whether or not this call asked for
			// it.
			for _, at := range config.MixedAccountStages {
				if _, _, err := mixedStageAccount(ctx, env, repo, at, auth, co); err != nil {
					return "", "", err
				}
			}
		case "oauth", "vertex", config.AuthCascade:
			// Nothing to check here: the oauth walk checks the default itself
			// when it picks it, and neither of the other two reaches a Claude
			// OAuth credential at all.
		default:
			if err := claudeAccountMembership(env, repo, def); err != nil {
				return "", "", err
			}
		}
	}
	// The mix's budget-mode clause (config.CheckMixBudget, §11), asked of what
	// the config *declares* and whatever stage was asked for: the one Claude
	// account an api-key run may hold exists because the gateway holds the
	// provider key and routes the coder's calls to it, and only budget.mode:
	// enforce routes a call to a provider at all, so a configuration that
	// declares a mix under any other mode declares a key that could never apply
	// — refused at the launch that picks the account, not left to a stage that
	// would never be handed one. The auth is asked before the clause's own
	// arguments are built, which is CheckMixBudget's first test said here: an
	// oauth launch must not spend a local default-branch git read (mixMode) and
	// a route resolution (mixRoutes) on a clause that cannot apply to it.
	if auth == "api-key" {
		if ps := config.CheckMixBudget(auth, mixMode(ctx, env, repo, co), mixCoder(co), def, declared, mixRoutes(env, repo, co), "budget.mode"); len(ps) > 0 {
			return "", "", userErr("%s: %s", ps[0].Path, ps[0].Message)
		}
	}
	// The flag is a name a person wrote (--token, or the account a follow-up
	// inherits from its previous run), so it may name an alias exactly as a key
	// does: it resolves before every check below, which is the same step and the
	// same order the keys take above. alias is the alias the walk answers with,
	// and it is set exactly where a name of the owner's local config decides the
	// account — the flag here, a stage key or the project default below — so no
	// other rung can leave one standing (rung 3 is a committed file's name, D2090).
	name, alias := env.lc.ResolveAccount(flag)
	if name == "" && stage != "" {
		if account := stageAccountOf(env.lc, auth, stage); account != "" {
			return account, stageAliasOf(env.lc, stage), nil
		}
		if account, stageAccountAlias, err := mixedStageAccount(ctx, env, repo, stage, auth, co); err != nil || account != "" {
			return account, stageAccountAlias, err
		}
	}
	if name == "" {
		switch {
		case refAgent:
			name = a.OAuthToken
		case co != nil:
			name = co.Agent.OAuthToken
		}
	}
	// A mixed run starts on the job that mounts its account's token, so the
	// launch's own account is the mix's (mixedRunAccount): the run's review
	// stages are served by that account and read its token out of the runner's
	// environment, which only the account's own job variant mounts. The
	// sentinel here is config.DefaultClaudeAccount — the spelling a repeated
	// launch's stored task uses for "no account chosen", which is not a name of
	// the run's own, so it leaves the mix's account standing; the account
	// axis's non-account values (config.AnthropicKeyAccount,
	// config.ProviderKeyAccount) are not this case and must not be: they are
	// what name the review class's own pairing (D1417, D1422), so the mix never
	// takes their place, and the test below is exactly why — neither is "" nor
	// the default. Any other name
	// is left to the walk below, which refuses it unless it is the mix's own. A
	// *stage* asked for is not this resolution: it has its own chain above, and
	// the mix serves the stages of the allow-list and no other.
	//
	// A non-account value and a declared mix are the one pair here that neither
	// of those answers covers: the name is no account, so it is refused below
	// only where the pair cannot be judged at all (claudeAccountAllowed admits
	// it for api-key, which is the auth a mix is declared under), while the
	// runner serves the run's mixed stages (review, adversarial and triage)
	// out of the mix
	// whatever the launch's own account is. Such a launch would start on the
	// value's job — the one whose account segment it is — which mounts no Claude
	// token, and its review stages would halt for one. It is refused here
	// instead, where the mix is known.
	if stage == "" {
		mix, mixAlias, err := mixedRunAccount(ctx, env, repo, auth, co)
		if err != nil {
			return "", "", err
		}
		if mix != "" && config.AxisNonAccountValue(name) {
			return "", "", userErr("this launch names %s (%s) as the run's own account, and this run declares a mix: %s serves its %s stages, and those stages read the account's token out of the runner's environment, which only the account's own job mounts — the mix is the one Claude account an api-key run may hold, and this value is not an account, so the launch would start on its job, whose review stages have no token to run on. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Launch without --token, which is where a mixed run starts — the mix is the run's own account — or remove the account this mix names for the %s stages",
				nonAccountPhrase(name), name, mix, config.MixedStagePhrase(), config.MixedStagePhrase())
		}
		if mix != "" && (name == "" || name == config.DefaultClaudeAccount) {
			// The mix is what decided this launch's account, so the key that
			// named it names the alias too — the flag named neither (it is empty
			// or the sentinel here), and the mix's own alias is the other
			// candidate for a --token that already names the mix's account.
			name, alias = mix, mixAlias
		}
		if mix != "" && name == mix {
			// The one name an api-key launch may carry: it is the account whose
			// token this run's job mounts, so it is not the "named account with
			// no secret to reach" claudeAccountAllowed refuses under api-key. A
			// follow-up inherits it and a --token may repeat it, and both arrive
			// here as the flag.
			return name, alias, nil
		}
	}
	if name == "" && auth == "oauth" {
		// Rung 4, and the same resolved name the default was checked under
		// above: one read of the key, so the account the allow-list admitted
		// and the account the run is given cannot be two different names.
		name, alias = def, defAlias
	}
	if name == "" {
		name = config.DefaultClaudeAccount
	}
	return name, alias, claudeAccountAllowed(env, repo, name, auth)
}

// mixedRunAccount is the account a mixed run's launch lands on: the one Claude
// OAuth account this run's review stages are served by, and "" when the run is
// no mix at all (docs/design/mixed-credentials.md §11).
//
// It is the launch's own account for a mixed run and not the stages' alone,
// because the account is what picks the job: spec.OAuthToken names the job
// variant (JobName(slug, WorkflowVariant(workflow, token)), internal/cli), the
// job that mounts the account's token is that variant and no other, and the
// runner's own environment is where the review stage reads the credential from
// (agent.OAuthStageEnv). A mixed run that started on the base job would refuse
// its own review stage for a token nothing mounted — Check 290's failure. The
// account stays the stages': the coder's stages keep the provider key the
// gateway holds, and the runner is what builds each stage's environment (B3).
//
// Rung 2 comes first and is served as written — a stage key is a declaration
// about that stage, whatever its model — and rung 4 follows under the whole of
// mixedStageAccount's rule, so the two rungs and the order are exactly the ones
// claudeAccountFor's stage path reads. That is what keeps the launch's job and
// the credential the review stage runs on the same account, and the job set
// agrees: mixedStageAccounts (internal/infra) builds a variant for each stage
// key and for rung 4's account, so the job this resolves always exists.
func mixedRunAccount(ctx context.Context, env *cloudEnv, repo, auth string, co *config.Config) (string, string, error) {
	if auth != "api-key" {
		return "", "", nil
	}
	for _, stage := range config.MixedAccountStages {
		if account := stageAccountOf(env.lc, auth, stage); account != "" {
			return account, stageAliasOf(env.lc, stage), nil
		}
	}
	for _, stage := range config.MixedAccountStages {
		account, alias, err := mixedStageAccount(ctx, env, repo, stage, auth, co)
		if err != nil || account != "" {
			return account, alias, err
		}
	}
	return "", "", nil
}

// stageAliasOf is the alias the stage's own key named, "" when it named the
// account itself or named nothing. stageAccountOf's answer cannot say which of
// the two happened, and the launch's own line names the pair only where an
// alias decided the account (docs/account-aliases/spec.md §7, D2099): the two
// are read side by side so the key is resolved once per answer and not once per
// reader.
func stageAliasOf(lc *localcfg.Config, stage string) string {
	if lc == nil {
		return ""
	}
	_, alias := lc.ResolveAccount(lc.ClaudeAccountStages[stage].ClaudeAccount)
	return alias
}

// stageAccountsOf is the local config's stage accounts as the rules read them:
// the stage name to the account it names, empty values dropped, each name
// resolved through the file's aliases (D2091, D2092).
//
// Resolution belongs here rather than at each caller because every one of them
// is asking the same question — which account does this stage run on — and the
// answers are the mix rules (CheckStageAccountMix), the launch's own allow-list
// check and `fugaro validate`'s report (stageAccountProblems). A key left as
// written would make an alias a stage account no account answers to: the mix
// rules would judge a stage that names one as a stage that names nothing, and
// validate would refuse a file the run accepts.
func stageAccountsOf(lc *localcfg.Config) map[string]string {
	if lc == nil || len(lc.ClaudeAccountStages) == 0 {
		return nil
	}
	out := make(map[string]string, len(lc.ClaudeAccountStages))
	for stage, choice := range lc.ClaudeAccountStages {
		if account, _ := lc.ResolveAccount(choice.ClaudeAccount); account != "" {
			out[stage] = account
		}
	}
	return out
}

// stageAccountOf is the account the stage's own key names, for the auths that
// may have one: oauth, where the accounts design's rung 2 gives a stage an
// account of its own, and api-key, where the B0 ruling's one account serves the
// mix's stages: the senior review, the adversarial review and the triage that
// weighs their findings. Every other auth is "" — vertex is refused outright by
// CheckStageAccountMix, cascade's project-wide accounts are skipped before that
// rule is asked (config.CascadeSkippedStageAccounts), and an auth the launch
// cannot resolve yet is not judged on a guess.
//
// The key resolves here (D2091, D2092): rung 2's answer is the account the
// stage runs on, whatever name the key holds, and both callers compare it with
// another account — claudeAccountFor with the mix's, mixedStageAccount with the
// project default — so a name left as written would read as a second account.
func stageAccountOf(lc *localcfg.Config, auth, stage string) string {
	if lc == nil || (auth != "oauth" && auth != "api-key") {
		return ""
	}
	account, _ := lc.ResolveAccount(lc.ClaudeAccountStages[stage].ClaudeAccount)
	return account
}

// mixRoutes is the provider routes this repository may use, as the run resolves
// them: the local config's table with the document's own claims merged over it
// (layerRoutes), narrowed to the entries that may see this repository
// (runner.ProvidersFor — an entry whose allow_data_to does not name it is not a
// route this run has). It is what "the coder is served by a provider" is asked
// of, here and in the runner.
func mixRoutes(env *cloudEnv, repo string, co *config.Config) map[string]config.ModelProvider {
	if co == nil {
		return nil
	}
	routes, _, _ := layerRoutes(env.lc, co.Layer, repo)
	return runner.ProvidersFor(routes, repo)
}

// mixCoder is the coder's model as the mix's conditions read it: the document's
// own pin with the recipe's role aliasing applied, and "" when no document was
// read — a stage account is admitted for no coder the launch cannot see.
func mixCoder(co *config.Config) string {
	if co == nil {
		return ""
	}
	return co.Agent.ModelFor(config.RoleCoder)
}

// mixMode is the budget mode the run resolves, over the three layers the runner
// merges (runner's own policy resolution): the installation's ceiling, the
// default branch's policy keys and the document at hand. It reads no network —
// the published policy is the job environment's ceiling, which ceilingLayer
// already carries — so a launch and the run itself agree on every layer they
// both can see, and a launch with no checkout judges the layers it has (an
// absent document contributes no mode of its own, runner.FileLayer(nil)).
func mixMode(ctx context.Context, env *cloudEnv, repo string, co *config.Config) string {
	var layer policy.Layer
	if co != nil {
		layer = runner.FileLayer(co)
	}
	return policy.Merge(ceilingLayer(env.lc), defaultBranchPolicyLayer(ctx, repo), layer).Mode
}

// mixedStageAccount is the account rung 4 puts on one of the mix's two stages
// of an api-key run, its whole rule, and "" when the mix is not admitted for
// that stage (docs/design/mixed-credentials.md §11, the owner's ruling of
// 2026-11-05: "IF default_claude_account is set AND we have an Anthropic model
// for senior review and/or security review, then we get mixed runs"). rung 2 is
// read before this call and wins, so the stage's own key is not consulted here.
//
// The mix is admitted for the stage when every one of these holds, and they are
// read off what a launch has: the checkout's own, layer-applied document and
// the project's local config. `fugaro validate` judges the other half of the
// rule and not this one — stageAccountProblems asks
// config.CheckStageAccountMix of the file's auth and the local stage keys, and
// the offline report never calls config.CheckMixBudget, so the mode and the
// provider routes below are the launch's own judgement (the runner makes it
// again, with the recipe's roles applied, in the job):
//
//   - the run's credential is a provider key (api-key): the mix exists to give
//     such a run a Claude review credential it otherwise has none of;
//   - the stage is review or adversarial, the ruling's allow-list: rung 4 never
//     reaches implement, review_first, fix, plan or any later stage;
//   - the stage's model is an Anthropic one (mixedStageModel): with a provider
//     reviewer or a non-Claude one, default_claude_account changes nothing —
//     no mix and no error, because nothing about that stage would use the
//     account;
//   - the coder is served by a route this repository may use: the mix is the
//     coder on a provider route *and* the reviews on the subscription, and a
//     run whose coder is not provider-served has no provider key to mix;
//   - the mode the run resolves is enforce: the provider key lives in the
//     gateway, and only enforce routes a call to a provider at all
//     (config.CheckProviderGateway), which is what "provider-served" means at
//     run time.
//
// The default is held to this repository's allow-list here, at the end of the
// rule, and not at the top of a launch: it is a name rung 4 can use only where
// the mix is admitted, so a launch whose reviews are not Claude's, or whose
// coder has no provider key in the gateway, must not be refused for a key that
// can never apply. The one-account refusal is raised here with it, and both are
// the caller's to see — the launch asks this function per mixed stage for
// exactly that reason.
//
// A launch that cannot read a document (no checkout) admits nothing: the mix is
// a fact about the run's configuration, and a guess is not one.
func mixedStageAccount(ctx context.Context, env *cloudEnv, repo, stage, auth string, co *config.Config) (string, string, error) {
	if auth != "api-key" || !slices.Contains(config.MixedAccountStages, stage) {
		return "", "", nil
	}
	if co == nil {
		return "", "", nil
	}
	// The default resolves before anything is judged of it (D2091, D2092): the
	// mix's account is the account the key stands for, and the comparisons
	// below — with the other mixed stage's key and with the allow-list — are
	// all about accounts. An alias left as written would look like a name the
	// allow-list does not hold and like a second credential beside a stage key
	// naming its target.
	def, defAlias := env.lc.ResolveAccount(env.lc.DefaultClaudeAccount)
	// "" and the unsuffixed-default sentinel (config.DefaultClaudeAccount,
	// "default") are the same answer here: a local config may spell the key
	// either way (localcfg's choiceRefProblem admits the sentinel), and neither
	// names an account of its own — an api-key run's job carries no OAuth token
	// for the default account, which is exactly what spec.OAuthToken's own rule
	// says (a run whose account is the sentinel sets no token at all). So the
	// key declares no mix, and reading it as one would invent the second
	// credential this rule refuses below. An alias whose target is the sentinel
	// resolves into this same answer, which is the ruling's own case: it names
	// no account of its own either.
	if def == "" || def == config.DefaultClaudeAccount {
		return "", "", nil
	}
	if !isClaudeModel(mixedStageModel(co.Agent, stage)) {
		return "", "", nil
	}
	if _, _, served := config.ProviderFor(mixRoutes(env, repo, co), mixCoder(co)); !served {
		return "", "", nil
	}
	if mixMode(ctx, env, repo, co) != policy.ModeEnforce {
		return "", "", nil
	}
	// The run holds one Claude account (docs/design/mixed-credentials.md §11,
	// "never a second provider's key"): the other mixed stage's own key names
	// the mix's account when it names one, and a project default that names a
	// *different* one would be a second credential this run may not hold. It is
	// refused rather than ignored, the way a stage account that could never
	// apply is, so nothing the operator wrote reads as if it were in use.
	for _, other := range config.MixedAccountStages {
		if other == stage {
			continue
		}
		if account := stageAccountOf(env.lc, auth, other); account != "" && account != def {
			return "", "", userErr("default_claude_account is %s and stages.%s.claude_account is %s: an api-key run holds one Claude account beside the provider key the gateway holds, and %s is already this run's — the project default would be a second credential. Name %s in both keys, or remove the project default",
				pluginwire.Printable(def), other, pluginwire.Printable(account), pluginwire.Printable(account), pluginwire.Printable(account))
		}
	}
	if err := claudeAccountMembership(env, repo, def); err != nil {
		return "", "", err
	}
	return def, defAlias, nil
}

// mixedStageModel is the model a review stage runs, for the mix's
// Anthropic-model condition: level 2's own model for review and for the triage
// that weighs its findings, and level 3's for adversarial — the levels those
// stages are (config.ResolveLevels' own defaults; triage's is the senior
// reviewer's, D1848) — and the reviewer role's model when the level names
// none. Level
// models are read here because a level's model is a pin of its own: it runs in
// place of the role slot the level sits in, so a Claude reviewer with a
// DeepSeek level 2 is a stage the account has nothing to do on.
//
// What it does not apply is the run's recipe: a recipe's roles mapping
// (roles: {reviewer: coder}) and a task's own level override are read by the
// run at bootstrap, and the runner makes this same judgement there with them
// applied. A launch that cannot see them answers the configuration's own
// model, which is what a launch that reads no recipe can honestly answer.
func mixedStageModel(a config.Agent, stage string) string {
	level := a.Levels.L2
	if stage == "adversarial" {
		level = a.Levels.L3
	}
	if level != nil && level.Model != "" {
		return level.Model
	}
	return a.ModelFor(config.RoleReviewer)
}

// agentAtRef is the two agent facts a launch judges a named account with, read
// at ref: the file's own `agent:` block (i) and agent.auth as the *run*
// resolves it (ii), with whether the ref could be read at all.
//
// The document is the checkout's own object database (checkoutFugaroYAMLAtRef:
// git cat-file at refs/remotes/origin/<ref>, else <ref>, never the network and
// never the working tree), and the two facts are read differently because the
// two keys have different scopes. agent.oauth_token is repository-scope — the
// committed fugaro.yaml is the only place that may name an account
// (docs/design/named-oauth-tokens.md T2) and no project layer may name one —
// so the file's own value is the run's, via the strict config.AgentOf. And
// agent.auth is project-scope and layerable (docs/design/layered-config.md's
// scopes table): the run composes it from the ref's bytes and the project
// layer its task carries, through runner.ResolveConfig, exactly as
// projectJobAgentAuth does, and a launcher that answered from the raw file
// would answer Fugaro's default where the layer is what says oauth.
//
// The layer is the one this command already resolved for that checkout
// (cachedProjectLayer: checkoutConfig resolved it to tell the repository's
// provider, and createTask's embedProjectLayer carries that same one into the
// task), never a fresh bucket read — this gate makes no network call of its
// own. A layer this command resolved nowhere leaves the file's own value
// standing, and a document that does not resolve over it leaves it standing
// too: such a document is one the run itself refuses at bootstrap, and for a
// shared job the launch gate re-resolves the run's own credential
// (projectJobAgentAuth) and refuses one that cannot be resolved.
//
// Nothing is fetched to make the read, so a ref this checkout does not hold, a
// repository with no checkout here and a file that does not decode all answer
// "no" rather than an error: the caller falls back to the working tree, which
// is the resolution every launch had before this read existed.
func agentAtRef(ctx context.Context, env *cloudEnv, repo, ref string) (config.Agent, string, bool) {
	if ref == "" {
		return config.Agent{}, "", false
	}
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return config.Agent{}, "", false
	}
	data, err := readFugaroYAMLAtRef(ctx, root, ref)
	if err != nil {
		return config.Agent{}, "", false
	}
	a, ok := config.AgentOf(data)
	if !ok {
		return config.Agent{}, "", false
	}
	auth := a.Auth
	fl, cached := cachedProjectLayer(ctx, root, env.lc)
	if cached && fl.Layer != nil {
		pl := &task.ProjectLayer{SHA256: fl.Layer.SHA256, Generation: fl.Generation, YAML: string(fl.Layer.Raw)}
		// No base branch: the task is not built yet at this point (this is the
		// read the launch resolves its account from), and only agent.auth is
		// read out of the resolution — a base changes git.base_branch and
		// nothing else.
		if res := runner.ResolveConfig(data, pl, env.lc.Name, ""); res.LayerErr == nil && len(res.Problems) == 0 {
			auth = res.Cfg.Agent.Auth
		}
	}
	return a, auth, true
}

// cascadeRefConfig is the document a cascade launch's own rules are read from
// here — its agent.auth and its budget mode both: the fugaro.yaml at the ref
// the run checks out (spec.Ref), read out of this checkout's object database,
// resolved over the project layer the task carries (spec.ProjectLayer) through
// runner.ResolveConfig, the runner's own function, exactly as
// projectJobAgentAuth resolves the credential.
//
// It is deliberately not the launcher's working tree (checkoutConfig). The run
// clones spec.Ref and resolves *that* file (runner.go's blobless checkout of
// origin/spec.Ref), so a launcher judging its working tree judges a document
// the run never reads: a checkout left on another branch, or carrying an
// uncommitted edit, can say budget.mode: observe over a ref that says off, and
// the launch this gate exists to refuse — one whose run cannot carry a single
// call — would take the claim, mint a budget token and start an execution that
// refuses in agent.BuildEnv instead. The reverse refuses a launch the run would
// carry. agentAtRef reads the ref for this very reason; the mode has to come
// from the same bytes as the auth, or the gate judges a run that does not
// exist.
//
// nil when the ref cannot be read or does not resolve: the caller falls back to
// the checkout's own document, as the auth read always has, and the run's own
// bootstrap refuses what the launcher could not read.
func cascadeRefConfig(ctx context.Context, env *cloudEnv, spec *task.Spec) *config.Config {
	if spec.Ref == "" {
		return nil
	}
	root := checkoutRoot(ctx, spec.Repo)
	if root == "" {
		return nil
	}
	data, err := readFugaroYAMLAtRef(ctx, root, spec.Ref)
	if err != nil {
		return nil
	}
	out := runner.ResolveConfig(data, spec.ProjectLayer, env.lc.Name, spec.BaseBranch)
	if out.LayerErr != nil || len(out.Problems) > 0 {
		return nil
	}
	return out.Cfg
}

// defaultBranchPolicyLayer is the default branch's policy as the run reads it:
// the policy keys of origin/<default branch>'s fugaro.yaml (runner's
// resolvePolicy, config.PolicyOf). The run merges three layers — the ceiling,
// this one and the ref's file — and a layer can only tighten, so leaving this
// one out is not neutral: a mode this layer raises (off < observe < enforce) is
// one the run has and the gate would not see, and a launch the run would carry
// would be refused. The security direction is safe the other way (no layer
// below the ceiling can lower a mode), so a default branch this launcher cannot
// read is an empty layer rather than a refusal: it has no fetch of its own — a
// fetch is a network write into a repository this command does not own — and
// the run's own bootstrap refuses the file it cannot read.
func defaultBranchPolicyLayer(ctx context.Context, repo string) policy.Layer {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return policy.Layer{}
	}
	db := defaultBranchParse(ctx, root)
	if !db.read {
		return policy.Layer{}
	}
	p, err := config.PolicyOf(db.data)
	if err != nil {
		return policy.Layer{}
	}
	return policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, PerDayUSD: p.PerDayUSD, MaxRunTokens: p.MaxRunTokens,
		AllowedModels: p.AllowedModels, MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer}
}

// checkoutAgentAuth is a checkout's own agent.auth as claudeAccountAllowed
// judges a name with it, "" when there is no checkout to read one from.
func checkoutAgentAuth(co *config.Config) string {
	if co == nil {
		return ""
	}
	return co.Agent.Auth
}

// claudeAccountAllowed refuses name (already resolved: flag, agent.oauth_token
// or an inherited follow-up value) unless it is the repository's default
// account. Checked before any network call, build-record read or task.json
// write (design §3 T7), and regardless of whether a local checkout exists:
// fugaro run --repo X --token N run from outside any checkout, and a
// follow-up's inherited name (bucket data a launcher can write directly),
// must both be checked exactly as a fresh resolution from a checkout would
// be — review finding: skipping the check just because there is no checkout
// here let either one through with its shape checked but never its
// membership in config.AllowedClaudeAccounts. When the auth a run resolves
// is known and is not oauth, naming an account makes no sense at all (no
// secret is ever mounted for it, so the name would be silently unused) and is
// refused outright, not merely left unchecked.
//
// auth is the agent.auth of the document the name came from (claudeAccountFor
// passes the ref's when it could read it, the checkout's own otherwise), and
// "" when the caller has no document at all: a name resolved with no checkout
// known is then only checked for membership, which is the rule the two
// pre-check callers (a follow-up's inherited account, --retry's stored task)
// depend on.
//
// The account axis's non-account values are the exception, and it is the
// membership half they are excepted from: config.AnthropicKeyAccount names the
// project's own Anthropic API key and config.ProviderKeyAccount the project's
// provider key, neither of which a claude_accounts entry may define and both of
// which the launch is judged against the profile's own list for (the arm
// below says the whole of it).
func claudeAccountAllowed(env *cloudEnv, repo, name, auth string) error {
	if name == config.DefaultClaudeAccount {
		return nil
	}
	// The two documents auth can come from are read differently, and only one
	// of them is validated. The checkout's own config went through
	// config.Validate (agent.auth is one of config.AgentAuths), while the
	// ref's bytes are read by config.AgentOf, which decodes strictly and
	// validates nothing — so an arbitrary string arrives here from a committed
	// fugaro.yaml. A value this Fugaro cannot place is refused, which is what
	// the run does with that same file at bootstrap (its own config.Validate)
	// and so only says so earlier, and it is printed through
	// pluginwire.Printable: it is a repository's own text, a newline in it
	// would add a line to a refusal a person reads (safetext.MultiLine keeps
	// newlines, by design), and an escape sequence would rewrite that line.
	// The message names what auth may be, because the file's author is the one
	// who can fix it.
	if auth != "" && !config.KnownAgentAuth(auth) {
		return userErr("named Claude account %s cannot be judged against %s's agent.auth, which is %s — not a value this Fugaro knows (agent.auth is one of %s)",
			pluginwire.Printable(name), repo, pluginwire.Printable(auth), strings.Join(config.AgentAuths, ", "))
	}
	// The axis's non-account values (config.AnthropicKeyAccount,
	// config.ProviderKeyAccount) are admitted here and judged where they can be:
	// the profile's own claude_accounts list is what deploys the job each one
	// names and its review-only variant (internal/infra's ProjectJobs and
	// ProjectReviewJobs), and the first thing the launch does with such a name is
	// ask that list (project_job.go's projectJobAccountFor). They cannot be
	// judged HERE, by the allow-list: neither is a Claude account and
	// config.ValidateClaudeAccounts refuses to define either in claude_accounts,
	// so config.AllowedClaudeAccounts — the list this function's membership half
	// reads — can never hold them, and a refusal here would name a
	// claude_accounts entry the operator may not write.
	//
	// api-key is the auth they are admitted for, and "" beside it: D1417 pairs
	// each credential with a repository whose own agent.auth is api-key (the
	// launch names the pairing with --token), which is the one auth the class
	// serves it under, and the pre-check callers — a follow-up's inherited
	// account and --retry's stored task, resolved with no document to read an
	// auth from — are held to the same rule at the launch that can read one
	// (projectJobAccountFor, and the runner's own checkProjectJobAccount after
	// it). Every other auth keeps the answer it already had: an OAuth run's
	// credential is a Claude account and neither value is one, and vertex and
	// cascade are refused by the class path itself (internal/runner's
	// checkReviewClassModelPath), so admitting them for those would write a task
	// the run then halts on.
	if config.AxisNonAccountValue(name) && (auth == "api-key" || auth == "") {
		return nil
	}
	if auth != "" && auth != "oauth" {
		return userErr("named Claude account %s only applies to a workflow whose agent.auth is oauth; %s's agent.auth is %s",
			pluginwire.Printable(name), repo, auth)
	}
	return claudeAccountMembership(env, repo, name)
}

// claudeAccountMembership is the allow-list half of claudeAccountAllowed, and
// the whole check for a *stage* account: every name is held to the account's own
// allow_data_to for the repository, whatever permitted rung it came from
// (docs/design/mixed-credentials.md §11, "every name is still held to
// claude_accounts's allow-list"). It is one function rather than two copies so
// that the run's own rungs and the stage's cannot drift: a name one admits for
// this repository is a name the other admits, and a name one refuses the other
// refuses, in the same sentence.
//
// The auth clause is deliberately not here. It keeps a named *run* account out
// of every auth but oauth — no secret is mounted for such a name, so it would
// be silently unused — while a stage account under api-key is exactly the mix
// the B0 ruling admits, so the stage path must not pass through it.
func claudeAccountMembership(env *cloudEnv, repo, name string) error {
	allowed := config.AllowedClaudeAccounts(env.lc.ClaudeAccounts, repo)
	if !slices.Contains(allowed, name) {
		return userErr("named Claude account %s is not one %s may use (allowed: %s); ask the operator to add it to claude_accounts in their local config",
			pluginwire.Printable(name), repo, strings.Join(allowed, ", "))
	}
	return nil
}

// chooseRecipe is the recipe of a new first run: --recipe, else the
// checkout's agent.recipe, else default, resolved over the checkout at root,
// the project and the catalog. With no flag and no checkout it resolves
// default (a project default included), and a catalog default leaves the
// choice to the runner, which reads agent.recipe at the task's ref.
// forAttempts gives a mode: review refusal that fits runAttempts's caller
// (--attempts refuses --pr, so the plain launch's "add --pr" advice would be
// wrong there).
func chooseRecipe(ctx context.Context, env *cloudEnv, flag string, checkout func() *config.Config, root string, warn io.Writer, forAttempts bool) (*task.Recipe, string, error) {
	name, explicit := flag, flag != ""
	var cfg *config.Config
	if c := checkout(); c != nil {
		cfg = c
		if name == "" && c.Agent.Recipe != "" {
			name, explicit = c.Agent.Recipe, true
		}
	}
	if name == "" {
		name = recipe.DefaultName
	}
	rr, err := resolveRecipe(ctx, env, root, name, time.Now(), false)
	if err != nil {
		return nil, "", err
	}
	if rr.Recipe.Mode == recipe.ModeReview {
		if forAttempts {
			return nil, "", userErr("recipe %s is mode: review, which only reviews pull requests; --attempts launches a fresh batch and cannot use it (review one with fugaro run --recipe %s --pr N instead)", rr.Name, rr.Name)
		}
		return nil, "", userErr("recipe %s is mode: review, which only reviews pull requests; launch it with --recipe %s --pr N (1 to 4 times)", rr.Name, rr.Name)
	}
	// A mode: artifact launch carries its mode in the task, and the runner
	// selects the run's shape from that field before it resolves the recipe
	// itself. So the launcher may only write it when it is sure of the
	// recipe the run will get: resolveRecipe has paths where what it
	// returned is NOT that recipe (a project layer this launcher may not
	// read, a stale cached project recipe standing in for an unreachable
	// bucket), and on those the mode would stay empty for a recipe that is
	// mode: artifact, the spec would be indistinguishable from an implement
	// one, and the image gate below would not fire: a silent pull request
	// where the owner asked for a file. The conservative direction is the
	// right one; the owner reruns when the layer is reachable.
	//
	// A mode: evaluate launch carries its mode the same way and for the same
	// reason: the runner's own shape, the branch claim its identity may not
	// carry (budgetBranch) and the whole of the run's work are decided from
	// the spec's mode before the recipe is read.
	if rr.Recipe.Mode == recipe.ModeArtifact || rr.Recipe.Mode == recipe.ModeEvaluate {
		if rr.Note != "" {
			return nil, "", userErr("recipe %s is mode: %s, but %s; the launch is refused rather than run without the recipe's mode in its task (retry when the layer is reachable)", rr.Name, rr.Recipe.Mode, rr.Note)
		}
		if forAttempts {
			return nil, "", userErr("recipe %s is mode: %s, which opens no pull request; --attempts launches a fresh batch of pull requests and cannot use it", rr.Name, rr.Recipe.Mode)
		}
	}
	// A report recipe reads this repository's own run records and writes the
	// reports the repository is missing (task-governance plan T3.6), and the
	// repository has one report run at a time: a batch's attempts are
	// launched together, each carrying the same manifest, so they would all
	// write the same reports into their own pull requests. The batch is
	// refused here, where the recipe is known, rather than by the
	// in-flight check in resolveAttemptPlan, which a batch's own attempts
	// cannot see (they are stored only as they launch).
	if forAttempts && recipe.UsesDigest(rr.Recipe) {
		return nil, "", userErr("recipe %s writes this repository's run reports, and a repository has one report run at a time; --attempts launches a batch of runs at once and cannot use it", rr.Name)
	}
	printRecipeNote(warn, rr, explicit)
	// An explicit --recipe default overrides agent.recipe, which applies when
	// the checkout sets it, or when there is no checkout and the runner reads
	// it at the ref: then the default is carried in the task.
	override := flag == recipe.DefaultName && (cfg == nil || cfg.Agent.Recipe != "")
	mode := ""
	// The two modes whose shape the runner selects from the spec: mode:
	// review and mode: implement are decided from the task's own fields
	// (ReviewPRs, the recipe's steps), so neither is carried here.
	switch rr.Recipe.Mode {
	case recipe.ModeArtifact, recipe.ModeEvaluate:
		mode = rr.Recipe.Mode
	}
	return rr.taskRecipeEmbedding(override), mode, nil
}

// printRecipeNote prints rr's note when the user chose the recipe by name
// (--recipe or agent.recipe), that is not default, and the lookup fell
// through to the catalog; an offline-cache note (a project recipe in use)
// is always printed.
func printRecipeNote(warn io.Writer, rr *resolvedRecipe, explicit bool) {
	if rr.Note == "" {
		return
	}
	if rr.Source == recipe.SourceCatalog && (!explicit || rr.Name == recipe.DefaultName) {
		return
	}
	io.WriteString(warn, noteLine(rr.Note))
}

// cascadeLaunchRefusal is the launcher's own side of the cascade's rules
// (C1.19): a `agent.auth: cascade` workflow is refused here, before the claim
// is taken, before a budget token is minted and before any byte is written —
// so a repository that asks for a cascade no installation can carry leaves no
// task, no execution and no token behind.
//
// The runner refuses the rest of the same rules in the job (the missing
// service, serviceUpstream). What is refused here is refused here because a
// person is watching a terminal:
//
//   - an installation with no database, first of all: the switch the operator
//     publishes and the credentials rules marker both live there, so this CLI
//     can see neither and refuses rather than launching a run whose first
//     recorded credential the deployed rules may refuse. The model credential
//     is no longer mounted in such a job (C1.20), so there is nothing to fall
//     back to either (D143) — and without a database there is no policy to
//     read a mode from, so the gateway rule below could only guess.
//   - the O2 switch: the project's published policy must carry
//     routing.claude: {via: cascade}, or no run of this workflow is admitted
//     anywhere. The policy read below is the one this needs, and the refusal
//     is fugaro validate's cascadeProblems said again on this side — a
//     repository asks for the cascade, and only the operator can set it there
//     — except that here it is judged before the claim is taken, before a
//     budget token is minted and before an execution starts, rather than
//     leaving the runner's bootstrap to refuse a run already paid for.
//   - the gateway rule (config.CheckCascadeGateway): a cascade run has no
//     credential of its own, so a budget whose effective mode is off leaves it
//     with nothing to carry its calls. config.CheckCascadeGateway is the one
//     place that rule lives, and its own comment named this task as the
//     launch's caller of it.
//   - the project's published model allow-list (the owner's ruling of
//     2026-10-22): the credentials service holds every call it carries to the
//     project's budget.allowed_models and refuses a project that publishes
//     none (internal/credsvc's requestNoAllowList), so a cascade run of such a
//     project would have every call refused and halt on the first (D143). What
//     is judged is the *policy node's* list and not the run's merged one: the
//     node is the only list the service can read, and the environment's
//     ceiling and the repository's file never reach it. The rule has no skip
//     for a project layer this launcher cannot read — an absent node is
//     refused by the switch's arm above — and this read is the one all three
//     surfaces the cascade is judged on make: `fugaro validate` reports it to
//     the author (validate.go's cascadeProblems) and the run refuses it in
//     bootstrap, before any model call (internal/runner/routing.go's
//     applyRouting).
//   - the rules marker (budget/rules, C1.3): the deployed document must carry
//     the credentials family, or the run's first write of the credential's
//     name is denied and it halts named (HaltCredsRulesDenied, C1.17). This is
//     the same read `fugaro update --firebase-project` makes before it applies
//     anything and the same pair `fugaro doctor` reports; here it is a
//     refusal, because a launch is the moment it can still save the run. The
//     marker's generation is wanted to be exactly this binary's, and the two
//     ways it can miss it are two different problems with two different fixes
//     (doctorCredsRules' pair): below this Fugaro's it is an old deploy, above
//     it a newer Fugaro's document — and a newer marker is not one this
//     binary may deploy over (internal/infra's DB.Plan refuses it), so telling
//     its owner to re-run init from this Fugaro would send them at a command
//     that refuses them.
//
// Both documents this gate judges are the run's: the auth *and* the mode come
// from the file the run checks out (spec.Ref, read out of this checkout's
// object database by cascadeRefConfig), never from the launcher's working tree,
// which the run does not read — projectJobAgentAuth's finding, and the reason
// the auth was read at the ref here from the start. The mode is judged over the
// same three layers the run merges (runner's resolvePolicy): the environment's
// ceiling, the default branch's policy (defaultBranchPolicyLayer) and the ref's
// own file.
func cascadeLaunchRefusal(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	runCfg := cascadeRefConfig(ctx, env, spec)
	if runCfg == nil {
		// The ref could not be read or does not resolve: the checkout's own
		// document stands in, as the auth read has always fallen back to it,
		// and the run's own bootstrap refuses what the launcher could not
		// read.
		runCfg = checkoutConfig(ctx, spec.Repo)
	}
	auth := checkoutAgentAuth(runCfg)
	if auth != config.AuthCascade {
		return nil
	}
	if env.lc.Budget == nil || env.lc.Budget.RTDBURL == "" {
		return userErr("agent.auth: cascade cannot run here: this installation has no database, so neither the policy switch that turns the cascade on (%s) nor the credentials rules marker (%s) can be read — and a job of such an installation mounts no model credential at all (C1.20), so this run would have nothing to fall back to (D143). Run 'fugaro update --firebase-project %s' in your own terminal to give this installation the database it lacks: an ordinary `fugaro update` then deploys the credentials service on it, and `fugaro run` is where the address reaches a job. Or set agent.auth to api-key or oauth in this workflow",
			projectpolicy.PathShared(), rules.PathCredsRulesMarker(), firebaseProjectIDWord(env.lc.GCPProject))
	}
	ceiling := ceilingLayer(env.lc)
	def := defaultBranchPolicyLayer(ctx, spec.Repo)
	var layer policy.Layer
	if runCfg != nil {
		layer = runner.FileLayer(runCfg)
	}
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil {
		return policyReadErr(err)
	}
	// O2, on the launch's own side (validate.go's cascadeProblems is the
	// author's): the policy read above is the one the switch is judged from,
	// and it fails closed in every direction — nothing published, a policy
	// with no routing block, or a Claude switch written with another via
	// (`direct` names the direct path and `auto` keeps today's routing
	// meaning, O3) are all "not turned on".
	switch {
	case !found || p == nil:
		return userErr("agent.auth: cascade cannot run here: this project has published no policy at %s, so nothing turns the cascade on. Set routing.claude: {via: cascade} in the project policy and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)",
			projectpolicy.PathShared())
	case p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.ViaValue() != config.RouteCascade:
		return userErr("agent.auth: cascade cannot run here: this project's published policy (%s) does not turn the cascade on. Set routing.claude: {via: cascade} in the project policy and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)",
			projectpolicy.PathShared())
	}
	if p.Budget.Mode != "" {
		ceiling.Mode = p.Budget.Mode
	}
	if ps := config.CheckCascadeGateway(config.Agent{Auth: auth}, policy.Merge(ceiling, def, layer).Mode); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return userErr("agent.auth: cascade cannot run here: %s. A cascade run holds no model credential of its own — the gateway carries every call to the credentials service and accounts for it (D143) — so a budget that leaves the gateway off cannot run one", strings.Join(msgs, "; "))
	}
	// The allow-list the credentials service holds this run's calls to, read
	// from the policy node and refused fail closed: there is no fall-back to
	// the environment's ceiling or to the repository's file, because the
	// service never sees either and refuses every call of a project that
	// publishes none (the owner's ruling of 2026-10-22).
	if len(p.Budget.AllowedModels) == 0 {
		return userErr("agent.auth: cascade cannot run here: this project's published policy (%s) carries no budget.allowed_models, and the credentials service — which carries every call of a cascade run and accounts for it (D143) — refuses a project that publishes none, so this run's first call would be refused and halt it. agent.auth: cascade needs the project's budget.allowed_models; add the allow-list to the project layer and publish it (fugaro project policy publish)",
			projectpolicy.PathShared())
	}
	db, err := newBudgetClient(ctx, env.lc.Budget.RTDBURL, env.lc.Endpoints.NoAuth)
	if err != nil {
		return userErr("%v", err)
	}
	var raw json.RawMessage
	marker := rules.PathCredsRulesMarker()
	foundMarker, err := db.Get(ctx, marker, &raw)
	if err != nil {
		return dbErr(env.lc, fmt.Errorf("reading the credentials rules marker at %s: %w", marker, err), false)
	}
	if !foundMarker {
		return userErr("agent.auth: cascade cannot run here: the installation's database carries no credentials rules marker at %s, so its deployed rules predate the credentials service and the first credential this run records would be refused — the run would halt instead of running (C1.17). Re-run 'fugaro update --firebase-project <gcp-project-id>' in your own terminal from a Fugaro that carries the credentials rules (%s), then launch again",
			marker, credsServiceSince)
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil || projectpolicy.MarkerHandEdited(gen) {
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s is %s, which is not a generation number; fix it with 'fugaro update --firebase-project <gcp-project-id>'",
			marker, oneLineCLI(string(raw)))
	}
	switch {
	case gen < rules.CredsRulesVersion:
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s records generation %d, below the generation %d this Fugaro's credentials rules carry, so the deployed document predates the credentials family and the first credential this run records would be refused (C1.17); re-run 'fugaro update --firebase-project <gcp-project-id>' in your own terminal from this Fugaro (%s), then launch again",
			marker, gen, rules.CredsRulesVersion, credsServiceSince)
	case gen > rules.CredsRulesVersion:
		return userErr("agent.auth: cascade cannot run here: the credentials rules marker at %s records generation %d, above the generation %d this Fugaro's credentials rules carry, so a newer Fugaro deployed them and this release refuses to deploy its own rules over a newer marker; upgrade Fugaro first (brew upgrade dimipaun/tap/fugaro) and re-run 'fugaro update --firebase-project <gcp-project-id>' with the newer binary, then launch again",
			marker, gen, rules.CredsRulesVersion)
	}
	return nil
}

// checkArtifactLaunch refuses a mode: artifact launch whose caps cannot fit
// the workflow's memory, or whose render or probe step names a command the
// workflow leaves empty: the launch's arm of runner.RecipeGateProblems
// (design artifact-runs.md §5.2, Review Focus 12), called before createTask
// writes anything, so an over-memory artifact launch leaves no task and no
// execution behind. A launch whose recipe or workflow the launcher cannot
// resolve locally is judged by the runner's own bootstrap instead
// (checkGateCommands), which reports the same list.
//
// It is also the arm of the artifact boundary that survives the enable (Global
// Constraint 13, plan compute-backend.md Task 8 step 4): an artifact-producing
// run is never launched on cloud-batch, and this is where the backend and the
// recipe the launch names are both known -- `fugaro validate` judges the
// checkout's own recipe offline, so a launch naming an artifact recipe with
// --recipe is refused here rather than there. The two arms ask one function
// (artifactOnBatchRefusal), so the boundary cannot be lifted from one path
// while the other keeps it.
//
// What it judges is the recipe as recipeForLaunch resolves it, and that is not
// always the recipe the runner reads, because the runner resolves the
// repository layer at the task's ref FIRST (internal/runner/recipe.go's
// resolveRecipe, TestRecipeRepoLayerWins):
//
//   - a project or catalog recipe's text travels in the task, but a file of
//     that name at the ref is the recipe that runs, mode included, whatever
//     text the task carries. So the ref is asked for that name as the runner
//     asks it (carriedRecipeAtRef, recipeAtRef), and a ref this checkout has
//     none of is refused fail-closed (artifactShadowedOnBatchRefusal) -- the
//     text a task carries says nothing about what the base branch holds beside
//     it. TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch
//     pins both halves.
//   - a repository recipe (named, or the one agent.recipe names) is read from
//     this launcher's working tree, while the runner reads it at the task's
//     ref -- the repository's base branch unless the launch names another. A
//     working tree whose recipe is not mode: artifact used to pass this gate
//     for a ref whose recipe is; since the enable (plan Task 10 step 5 entry 6)
//     the ref is asked for the name this launcher resolved (repoRecipeAtRef),
//     so the file the ref holds is judged on its own mode, and a ref this
//     checkout does not hold is refused fail-closed
//     (artifactRepoRecipeAtRefRefusal).
//     TestTheArtifactLaunchGateJudgesTheRefNotTheWorkingTree pins it. That
//     read is defence in depth and never the closure: it is of this checkout's
//     last fetch, so a mode: artifact file pushed after it passes here and is
//     refused by the runner, which reads the recipe the run actually uses
//     (internal/runner's FUGARO_BACKEND refusal).
//
// It has three shapes of its own, because a launch can arrive without a recipe
// this launcher can read at all: the refusal for a recipe known to be mode:
// artifact, and -- on cloud-batch only -- the refusal for a recipe that could
// not be resolved here (artifactUnreadOnBatchRefusal) and for a carried recipe
// whose repository layer at the ref could not be read
// (artifactShadowedOnBatchRefusal), since nothing on this path can then say the
// run is not an artifact one. All are asked before anything about this launch
// is written, so none leaves a task or an execution behind.
func checkArtifactLaunch(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	cfg := checkoutConfig(ctx, spec.Repo)
	// The boundary is asked here, before the checkout can bail out below and
	// with the recipe resolved as far as this checkout allows. Both of those
	// are the arm itself and not an ordering detail: recipeForLaunch needs no
	// checkout for a task that carries its own recipe text (a project or
	// catalog recipe -- `fugaro run --recipe`), so asking the boundary only
	// after the checkout had resolved left exactly that launch unrefused on
	// cloud-batch, from any directory whose origin is not spec.Repo. The
	// workflow is not consulted either: SelectWorkflow reads this checkout's
	// config, which a launch whose local configuration disagrees with its
	// repository would fail -- and the boundary is about the backend and the
	// recipe, not about the workflow.
	rcp := recipeForLaunch(ctx, cfg, spec)
	if msg, ok := artifactOnBatchRefusal(launchBackend(env.be), rcp); ok {
		// Before the caps below and before anything is written, like every
		// other refusal here: the run the boundary holds back leaves no task
		// and no execution behind.
		return userErr("%s", msg)
	}
	if launchBackend(env.be) == backend.CloudBatch && rcp == nil {
		// Fail closed, and this is the other shape of the same hole: a recipe
		// this launcher cannot read (a repository recipe with no checkout of
		// that repository here, or a first run whose recipe is the one this
		// checkout's agent.recipe names) reaches the boundary as nil, which
		// artifactOnBatchRefusal reads as "not an artifact run" -- and the
		// runner resolves that recipe at the task's ref, so on
		// cloud-batch an unread recipe is refused rather than passed on.
		return userErr("%s", artifactUnreadOnBatchRefusal(spec.Repo))
	}
	if launchBackend(env.be) == backend.CloudBatch && spec.Recipe != nil && spec.Recipe.YAML != "" {
		// A recipe whose text the task carries is not the run's recipe either,
		// and for the same reason: the runner reads the repository layer at the
		// task's ref FIRST and only falls back to the task's copy
		// (internal/runner/recipe.go's resolveRecipe, TestRecipeRepoLayerWins),
		// so a file of that name at the ref is the recipe that runs, its mode
		// included. So the ref is asked for that name, as the runner asks it:
		// the file it finds (if any) is judged on its own mode, and a ref this
		// checkout has none of is refused fail-closed -- a project or catalog
		// recipe's text says nothing about what the repository's base branch
		// holds beside it.
		refRcp, err := carriedRecipeAtRef(ctx, spec)
		if err != nil {
			return userErr("%s", artifactShadowedOnBatchRefusal(spec.Recipe.Name, spec.Repo, spec.Ref))
		}
		if msg, ok := artifactOnBatchRefusal(backend.CloudBatch, refRcp); ok {
			return userErr("%s", msg)
		}
	}
	if launchBackend(env.be) == backend.CloudBatch {
		// The same read for the recipes that are a repository FILE rather than
		// text: the recipe a task names as repo-sourced (fugaro run --recipe,
		// read from the working tree by repoRecipeFrom) and the one the runner
		// resolves for a task that names none at all (agent.recipe from the
		// ref's own fugaro.yaml, else the default name -- resolveRecipe, which
		// reads agent.recipe for a first run only). Both are read at the task's
		// ref by the run and from this checkout's working tree here, so the ref
		// is asked for the name this launcher resolved, as it is asked above for
		// a carried one: the file it holds is judged on its own mode. This is
		// defence in depth and not the boundary's closure, because the read is
		// of this checkout's last fetch -- a same-named mode: artifact file
		// pushed after it passes here and is refused by the runner.
		refRcp, err := repoRecipeAtRef(ctx, spec)
		if err != nil {
			return userErr("%s", artifactRepoRecipeAtRefRefusal(err, spec.Repo, spec.Ref))
		}
		if msg, ok := artifactOnBatchRefusal(backend.CloudBatch, refRcp); ok {
			return userErr("%s", msg)
		}
	}
	if cfg == nil {
		return nil
	}
	if rcp == nil || rcp.Mode != recipe.ModeArtifact {
		return nil
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return nil
	}
	var msgs []string
	for _, p := range runner.RecipeGateProblems(rcp, wf) {
		msgs = append(msgs, fmt.Sprintf("workflows.%s.%s: the %s", name, p.Field, p.Message))
	}
	if len(msgs) == 0 {
		return nil
	}
	return userErr("recipe %s: %s", rcp.Name, strings.Join(msgs, "; "))
}

// carriedRecipeAtRef is the recipe the runner will use for a launch whose task
// carries its own recipe text: the repository layer at the task's ref when the
// ref has a file of that name -- which resolveRecipe reads FIRST and which wins
// over the task's copy, mode included (internal/runner/recipe.go,
// TestRecipeRepoLayerWins) -- else the text the task carries.
//
// err is non-nil whenever the ref's answer cannot be had at all: no checkout of
// the run's repository in this directory, a checkout that does not hold
// spec.Ref, or a file at the ref this launcher cannot parse. The caller refuses
// on it, because then nothing here can say whether the run is an artifact one.
func carriedRecipeAtRef(ctx context.Context, spec *task.Spec) (*recipe.Recipe, error) {
	root := checkoutRoot(ctx, spec.Repo)
	data, found, err := recipeAtRef(ctx, root, spec.Ref, spec.Recipe.Name)
	if err != nil {
		return nil, err
	}
	if !found {
		data = []byte(spec.Recipe.YAML)
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, fmt.Errorf("recipe %s at %s is invalid: %s", spec.Recipe.Name, spec.Ref, recipe.ProblemsText(ps))
	}
	if rcp.Name != spec.Recipe.Name {
		return nil, fmt.Errorf("recipe %s at %s names itself %q; the file name and the name must agree", spec.Recipe.Name, spec.Ref, rcp.Name)
	}
	return rcp, nil
}

// repoRecipeAtRef is the recipe the run will resolve when the one this launch
// names is a repository FILE rather than text the task carries: the name this
// launcher resolved -- spec.Recipe's for a repo-sourced recipe, else the
// default name for a follow-up, else agent.recipe out of the REF's own
// fugaro.yaml for a first run that names none (internal/runner/recipe.go's
// resolveRecipe) -- read at the task's ref, which is where the runner reads it
// and not the working tree this launcher read it from.
//
// nil, nil for a launch whose recipe is not one of those: one that carries its
// own text is carriedRecipeAtRef's, and answers above. A name the ref holds no
// file for is nil as well, because the runner has its own answer for that (a
// repo-sourced recipe with no file at the base is refused there, and a first
// run with none falls back to the catalog). err is non-nil whenever the ref
// cannot be read at all, which the caller refuses on: nothing here can then say
// whether the run is an artifact one.
func repoRecipeAtRef(ctx context.Context, spec *task.Spec) (*recipe.Recipe, error) {
	name := recipe.DefaultName
	switch {
	case spec.Recipe != nil && spec.Recipe.YAML != "":
		return nil, nil
	case spec.Recipe != nil:
		name = spec.Recipe.Name
	case spec.IsFollowUp():
		// A follow-up never reads agent.recipe (resolveRecipe), so the name is
		// the default's and the layer it reads is origin/<ref>'s, which
		// recipeAtRef tries first.
	default:
		cfg, err := refConfigAtRef(ctx, checkoutRoot(ctx, spec.Repo), spec.Ref)
		if err != nil {
			return nil, err
		}
		if cfg != nil && cfg.Agent.Recipe != "" {
			name = cfg.Agent.Recipe
		}
	}
	data, found, err := recipeAtRef(ctx, checkoutRoot(ctx, spec.Repo), spec.Ref, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil, fmt.Errorf("recipe %s at %s is invalid: %s", name, spec.Ref, recipe.ProblemsText(ps))
	}
	if rcp.Name != name {
		return nil, fmt.Errorf("recipe %s at %s names itself %q; the file name and the name must agree", name, spec.Ref, rcp.Name)
	}
	return rcp, nil
}

// recipeAtRef reads .fugaro/recipes/<name>.yaml at ref out of this checkout's
// own object database -- git cat-file, never the network and never the working
// tree -- the file the runner resolves first (internal/runner/recipe.go's
// readRepoRecipe). found is false when the ref has no such file; err when the
// answer cannot be had at all (no checkout root, a ref this checkout does not
// hold, a path that is not a blob).
//
// The refs are tried the way checkoutFugaroYAMLAtRef tries them, and for its
// reason: refs/remotes/origin/<ref> first, because that is the ref the run
// clones, and the plain ref as the fallback for a checkout that has the branch
// but no remote-tracking ref of it. Nothing is fetched here -- a fetch is a
// network write into a repository this command does not own.
func recipeAtRef(ctx context.Context, root, ref, name string) (data []byte, found bool, err error) {
	if root == "" {
		return nil, false, errors.New("this directory is not a checkout of the run's repository")
	}
	if ref == "" {
		return nil, false, errors.New("the launch names no ref")
	}
	at := ""
	for _, candidate := range []string{"refs/remotes/origin/" + ref, ref} {
		sha, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		if err == nil && sha != "" {
			at = candidate
			break
		}
	}
	if at == "" {
		return nil, false, fmt.Errorf("this checkout holds no %s ref (fetch it, or check it out)", ref)
	}
	rel := recipe.RepoPath(name)
	if _, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", at+":"+rel); err != nil {
		return nil, false, nil
	}
	data, err = gitCmd(ctx, root, "cat-file", "blob", at+":"+rel).Output()
	if err != nil {
		return nil, false, fmt.Errorf("%s at %s is not a readable file", rel, at)
	}
	if len(data) > recipe.MaxBytes {
		return nil, false, fmt.Errorf("%s at %s is %d bytes, over the 16 KiB limit", rel, at, len(data))
	}
	return data, true, nil
}

// recipeForLaunch is the recipe this launch will run, parsed: the task's own
// (the text the launcher embedded for a project or catalog recipe, else the
// repository file a repo-sourced one names, read from this checkout), or,
// for a task that names none, the one the runner will resolve for it —
// agent.recipe from this checkout's config, for a FIRST run (localRecipe,
// exactly as `fugaro validate` resolves it), and the catalog default for a
// follow-up, which never takes agent.recipe at all (the runner's
// resolveRecipe, internal/runner/recipe.go: `r.follow == nil &&
// cfg.Agent.Recipe != ""`). nil when the launcher cannot read or parse it
// here: the runner resolves the same recipe at the task's ref and refuses it
// there.
//
// The follow-up arm is not a detail: --attach resolves the mode through this
// function (checkAttachRecipe), and reading agent.recipe for a follow-up made
// the launcher allow, upload for and write a task for an artifact recipe the
// runner would then not run — it takes the default (mode: implement) — so the
// run failed at bootstrap with the bytes already in the bucket.
func recipeForLaunch(ctx context.Context, cfg *config.Config, spec *task.Spec) *recipe.Recipe {
	root := checkoutRoot(ctx, spec.Repo)
	if spec.Recipe == nil {
		if spec.IsFollowUp() {
			// Resolved without cfg: agent.recipe is not consulted for a
			// follow-up, so the name is the default's and this checkout's
			// layer, when there is one, is read for it exactly as the runner
			// reads that layer at the base — from the working tree here, which
			// is the approximation every launcher-side read of a base file
			// makes, and the runner stays the backstop for a base whose file
			// differs from it.
			return localRecipeNamed(root, recipe.DefaultName)
		}
		if cfg == nil {
			return nil
		}
		return localRecipe(root, cfg)
	}
	data := []byte(spec.Recipe.YAML)
	if len(data) == 0 {
		return repoRecipeFrom(spec, root)
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil
	}
	return rcp
}

// repoRecipeFrom is a repository recipe's own file: task.json carries its
// name alone (the runner reads the file at the task's ref), so the launcher
// reads the same file from its own checkout of the repository. nil when this
// checkout is not the repository's, the file is not there, or it does not
// parse — the runner's bootstrap is the backstop for all three, and the
// image gates judge what it returns the same way they judge an embedded
// recipe (repoRecipeForGate).
func repoRecipeFrom(spec *task.Spec, root string) *recipe.Recipe {
	if root == "" {
		return nil
	}
	data, found, err := recipe.ReadRepoFile(root, spec.Recipe.Name)
	if err != nil || !found {
		return nil
	}
	rcp, ps := recipe.Parse(data)
	if len(ps) > 0 {
		return nil
	}
	return rcp
}

// runnerChooses reports whether the runner, not this CLI, decides spec's
// recipe: a first run with no recipe in its task and no checkout here.
//
// checkoutConfig (Task 10) resolves the repository's project layer,
// lenient (validate.go's layerOptions{Lenient: true}): an unreadable
// bucket or no project selected leaves it nil for a minimal (layer-only)
// file, the same as no checkout at all, so this reports the runner as
// choosing even where a checkout is right here.
func runnerChooses(ctx context.Context, spec *task.Spec) bool {
	return spec.Recipe == nil && !spec.IsFollowUp() && checkoutConfig(ctx, spec.Repo) == nil
}

// recipeLine is the launch's one-line recipe preview (decision D3).
func recipeLine(spec *task.Spec, chooses bool) string {
	switch {
	case spec.Recipe != nil:
		return fmt.Sprintf("recipe: %s (%s)", spec.Recipe.Name, spec.Recipe.Source)
	case chooses:
		return fmt.Sprintf("recipe: chosen by the runner (agent.recipe in fugaro.yaml at %s, else default)", oneLine(spec.Ref))
	}
	return "recipe: default (catalog)"
}

// launchRecipeOf is the --json form of recipeLine; nil when the runner chooses.
func launchRecipeOf(spec *task.Spec, chooses bool) *launchRecipe {
	switch {
	case spec.Recipe != nil:
		return &launchRecipe{Name: spec.Recipe.Name, Source: spec.Recipe.Source}
	case chooses:
		return nil
	}
	return &launchRecipe{Name: recipe.DefaultName, Source: string(recipe.SourceCatalog)}
}

// OverrideCap is the longest total time a run may ask for with
// --total-timeout: a runaway value is more likely a typo than a plan. It is
// the flag's own name for backend.MaxRunTime, which is where the bound is
// defined -- it is a fact about what a run may ask for rather than a CLI
// policy, and the reaper's deadlines have to account for it too
// (internal/backend cannot import this package).
const OverrideCap = backend.MaxRunTime

// parseTotalTimeout checks the parts of --total-timeout that need no
// checkout: the runner and the platform bound what is left.
func parseTotalTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, userErr("--total-timeout %q must be a duration such as 45m", s)
	}
	if d <= 0 {
		return 0, userErr("--total-timeout must be greater than 0")
	}
	if d > OverrideCap {
		return 0, userErr("--total-timeout %s is above the %s cap", d, OverrideCap)
	}
	if limit := gcp.MaxTaskTimeout - backend.TaskTimeoutSlack; d > limit {
		return 0, userErr("--total-timeout %s is above what a Cloud Run task allows (%s)", d, limit)
	}
	return d, nil
}

// launchTimeoutOf is the backend timeout of a stored task: its
// overrides.total_timeout, or zero for the job's own.
func launchTimeoutOf(spec *task.Spec) time.Duration {
	if spec.Overrides.TotalTimeout == "" {
		return 0
	}
	d, err := time.ParseDuration(spec.Overrides.TotalTimeout)
	if err != nil {
		return 0 // Validate refuses this earlier
	}
	return d
}

// checkoutOf returns a memoized lookup of the working directory's
// fugaro.yaml, for repo only (see checkoutConfig).
func checkoutOf(ctx context.Context, repo string) func() *config.Config {
	var (
		done bool
		cfg  *config.Config
	)
	return func() *config.Config {
		if !done {
			cfg, done = checkoutConfig(ctx, repo), true
		}
		return cfg
	}
}

// resolveWorkflow is --workflow, else the local config's single workflow
// for repo, else the single workflow of the checkout's fugaro.yaml.
func resolveWorkflow(env *cloudEnv, repo, flag string, checkout func() *config.Config) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if r, ok := env.localRepo(repo); ok && len(r.Workflows) == 1 {
		return r.Workflows[0], nil
	}
	if c := checkout(); c != nil && len(c.Workflows) == 1 {
		for name := range c.Workflows {
			return name, nil
		}
	}
	return "", userErr("cannot tell which workflow of %s to run; pass --workflow", repo)
}

// retrySpec loads the stored task of an existing run for --retry. The
// slug is where the run is stored, so no provider lookup is needed.
func retrySpec(ctx context.Context, env *cloudEnv, ref string) (string, *task.Spec, error) {
	slug, runID, err := locateRun(ctx, env, ref)
	if err != nil {
		return "", nil, err
	}
	s := runstore.Open(env.bucket.Bucket, slug, runID)
	spec, err := s.ReadTask(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return "", nil, userErr("run %s/%s has no task.json", slug, runID)
	}
	if err != nil {
		return "", nil, remote(err)
	}
	if spec.RunID != runID {
		return "", nil, userErr("run %s/%s holds the task of run %s; it can't be retried", slug, runID, spec.RunID)
	}
	// An arm is one half of a pair whose other half is a run of its own, and
	// the pair is judged from both records (A3.0, D571): replaying one arm as
	// a lone run would write a record that no pair explains and that the
	// sampling rules never chose. Refused, like the report run below, rather
	// than launched as it stands.
	if spec.AB != nil {
		return "", nil, userErr("run %s/%s is an A/B %s, and --retry launches the task of a first launch as it stands: a pair is judged from both arms' records, so an arm is never retried alone — launch a new run", slug, runID, spec.AB.Arm)
	}
	// A stored task this old may name no workflow at all, and the reader below
	// is a reader of the launch's document like any other: the ref is this
	// task's own, so the document is the one the retried run resolves.
	ctx = withLaunchDocument(ctx, spec.Repo, spec.Ref)
	checkout := checkoutOf(ctx, spec.Repo)
	if spec.Workflow == "" {
		if spec.Workflow, err = resolveWorkflow(env, spec.Repo, "", checkout); err != nil {
			return "", nil, err
		}
	}
	// spec is stored bucket data nobody vouches for (review finding A): its
	// OAuthToken's shape was checked when it was first written (task.Parse),
	// but whether this repository may still use that account was never
	// re-checked here, the one place --retry resolves what it is about to
	// launch, before any build-record read or the launch itself.
	//
	// It is the whole resolver a fresh launch's account goes through
	// (claudeAccountFor) and not the allow-list check alone, exactly as the
	// repeated --run-id path asks it (storedFollowUp): a mixed run's stored
	// task names its account under agent.auth: api-key — the shape this
	// launcher itself writes for one — and that is a name
	// claudeAccountAllowed refuses outright while the mix admits it
	// (mixedRunAccount). Nothing but that name is admitted here either: the
	// resolver's own refusals still stand, the allow-list among them. What it
	// resolves is deliberately not written back: --retry launches the stored
	// task as it stands (createTask does not run for one), and task.json is
	// what the runner places itself in the job by (matchJobName), so a task
	// carrying no account is a run of the plain job the resolver's answer
	// must not silently disagree with.
	if _, err := claudeAccountFor(ctx, env, spec.Repo, spec.OAuthToken, "", spec.Ref, checkout()); err != nil {
		return "", nil, err
	}
	// A report run is refused, the way a follow-up carrying one is
	// (refuseFollowUpReportMode): --retry launches the task of a first
	// launch as it stands, so the manifest it carries is the one that launch
	// listed, stale by now, and none of the checks a fresh report launch
	// makes — digestManifest's in-flight refusal among them — run at all. A
	// repository has one report run at a time because both would write the
	// same reports, and a stale manifest writes a report of records that
	// have moved on; the launcher of a fresh report run lists them again.
	// The task says it is one two ways: the manifest itself (digest_runs is
	// accepted under no other recipe, task.Validate), which is the only one
	// that speaks for a repository recipe, and the recipe's text or file,
	// for a report run whose manifest came out empty.
	if spec.Recipe != nil && (len(spec.DigestRuns) > 0 || digestRecipe(checkoutRoot(ctx, spec.Repo), spec.Recipe)) {
		return "", nil, userErr("run %s/%s is a report run, and --retry launches the task of its first launch as it stands: its manifest is stale by now, and a repository has one report run at a time, because both would write the same reports — launch a new report run with fugaro run --recipe %s", slug, runID, spec.Recipe.Name)
	}
	return slug, spec, nil
}

// horizonMargin is added to the longest job timeout for the max_parallel
// listing: the task timeout slack is added separately, and an hour more
// covers queueing.
const horizonMargin = time.Hour

// checkMaxParallel refuses a new launch when max_parallel runs are active.
// No run can be active for longer than the longest job timeout (or the
// override cap, whichever is more) plus the timeout slack and queueing, and
// the execution listing is newest first across every job, so it stops
// paging at that horizon. One jobs.list finds the longest timeout.
func checkMaxParallel(ctx context.Context, env *cloudEnv) error {
	return checkMaxParallelFor(ctx, env, 1)
}

// activeHorizon is the longest a run can still be going: the longest job
// timeout, or the cap a per-run override may reach, whichever is more, plus
// the task timeout slack and the queueing margin. It bounds every "is a run
// still active" question, which is why the report run's launch shares it
// with checkMaxParallelFor below (run_digest_manifest.go).
func activeHorizon(ctx context.Context, env *cloudEnv) (time.Duration, error) {
	longest, err := env.be.LongestTaskTimeout(ctx)
	if err != nil {
		return 0, remote(err)
	}
	return max(longest, OverrideCap) + backend.TaskTimeoutSlack + horizonMargin, nil
}

// checkMaxParallelFor is checkMaxParallel, generalized to a launch that
// needs room for `need` runs at once: a best-of-N batch (runAttempts) checks
// for all of N before its first launch, so it never starts part of a batch
// it cannot finish (design generic-tool G13). need is always 1 outside that
// one caller, which checkMaxParallel keeps as the common case.
func checkMaxParallelFor(ctx context.Context, env *cloudEnv, need int) error {
	horizon, err := activeHorizon(ctx, env)
	if err != nil {
		return err
	}
	active, err := env.be.List(ctx, backend.ListFilter{ActiveOnly: true, Since: time.Now().Add(-horizon)})
	if err != nil {
		return remote(err)
	}
	if len(active)+need > env.lc.MaxParallel {
		if need > 1 {
			return userErr("%d runs active; max_parallel is %d (--attempts %d needs %d)", len(active), env.lc.MaxParallel, need, need)
		}
		return userErr("%d runs active; max_parallel is %d", len(active), env.lc.MaxParallel)
	}
	return nil
}

// adoptStoredProjectLayer peeks at whatever task is already stored for
// spec's run ID, without writing anything, and when it is spec's own task
// (repeatTaskBytes equal, ignoring the layer itself), adopts its project
// layer into spec before the image gate below runs: a repeat whose earlier
// attempt stored a task but never launched it must be gated on the layer
// that stored task actually carries, since createTask (below) keeps it
// either way. No stored task (ErrNotFound), or one for a different
// request, leaves spec untouched: createTask is still the one that refuses
// "a different task".
func adoptStoredProjectLayer(ctx context.Context, s *runstore.Store, spec *task.Spec) error {
	have, err := s.ReadTask(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return remote(err)
	}
	a, err1 := repeatTaskBytes(have)
	b, err2 := repeatTaskBytes(spec)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if string(a) == string(b) {
		spec.ProjectLayer = have.ProjectLayer
	}
	return nil
}

// createTask stores task.json once. A repeated run ID must repeat its task.
func createTask(ctx context.Context, s *runstore.Store, spec *task.Spec) error {
	err := s.CreateTask(ctx, spec)
	if err == nil {
		return nil
	}
	if !errors.Is(err, runstore.ErrExists) {
		return remote(err)
	}
	have, err := s.ReadTask(ctx)
	if err != nil {
		return remote(err)
	}
	a, err1 := repeatTaskBytes(have)
	b, err2 := repeatTaskBytes(spec)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if string(a) != string(b) {
		return userErr("run ID %s already holds a different task", spec.RunID)
	}
	// The stored task is what the job actually reads: a repeat keeps its
	// project layer exactly as first launched, however spec just resolved
	// it (L8, L15 are about a first launch; a repeated run ID is the same
	// run, not a second resolution of it). Without this, a layer republished
	// between the first launch and the repeat would make this call embed a
	// different project_layer into the in-memory spec than what launchRun
	// and the --json report actually describe.
	spec.ProjectLayer = have.ProjectLayer
	return nil
}

// repeatTaskBytes is spec.Marshal with the project layer left out entirely,
// so a repeated --run-id is judged on everything the caller actually chose
// (repo, ref, workflow, task text, overrides, recipe...), never on the
// project layer: embedProjectLayer re-reads the bucket on every call
// (decision L13, no fresh window), so its content can legitimately differ
// between the first launch and a repeat (a republish, or just a new
// generation of unchanged bytes); without this, either would make a
// repeated --run-id spuriously look like "a different task".
func repeatTaskBytes(spec *task.Spec) ([]byte, error) {
	clone := *spec
	clone.ProjectLayer = nil
	return clone.Marshal()
}

func printLaunch(w io.Writer, res launchResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	verb := "launched"
	if res.Status == "already-launched" {
		verb = "already launched"
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", verb, launchWhat(res)); err != nil {
		return err
	}
	return launchDetail(w, res)
}

// launchWhat is what a launch is of: the run, and, for a follow-up, the pull
// request it continues. It is the tail of the line a launch prints, whether
// that line is the command's own ("launched <what>") or the reporter's step
// for a migrated launch ("launch <what>", CL7c).
//
// The runs are named in their short form: the line a person reads is where a
// short run ID belongs, and the full one is one command away (D1705). The
// JSON above keeps them whole.
//
// A launch whose account came through an alias names the pair the operator's
// own config holds, `ALIAS -> ACCOUNT` (docs/account-aliases/spec.md §7,
// D2099): the account alone would hide which alias decided the launch, and the
// alias alone would hide the account the job it started on mounts. A launch
// that read no alias prints what it always printed.
func launchWhat(res launchResult) string {
	what := task.ShortRun(oneLine(res.Run))
	if res.PR > 0 {
		what += fmt.Sprintf(" (follow-up of PR #%d, after %s)", res.PR, task.ShortRun(oneLine(res.PreviousRun)))
	}
	if res.account.Alias != "" {
		what += fmt.Sprintf(" (account %s -> %s)", pluginwire.Printable(res.account.Alias), pluginwire.Printable(res.account.Name))
	}
	return what
}

// launchDetail prints what is under a launch's line — the branch it cut, and
// the run's own cloud log when the launcher named one — which is the same
// whatever that line was.
func launchDetail(w io.Writer, res launchResult) error {
	branch := oneLine(res.Branch)
	if res.Ref != "" {
		branch += " cut from " + oneLine(res.Ref)
	}
	if _, err := fmt.Fprintf(w, "  branch %s\n", branch); err != nil {
		return err
	}
	if res.LogURL != "" {
		_, err := fmt.Fprintf(w, "  logs %s\n", oneLine(res.LogURL))
		return err
	}
	return nil
}

// recordedLaunch is what a run recorded about its own launch, read without
// writing anything: launch.json as it stands, or — when the CLI that launched
// the run died before writing one — the launch the run's own record names,
// result.json's execution through backend.ParseExecution, which is what
// existingLaunch's backfill is built from. staged tells the two apart.
//
// It has two callers and that is the point: the "already launched" check
// below, and the job a launch continuing this run reuses (project_job.go,
// spec.md §8, D907). The two cannot disagree about what a run's job was, and
// the decision — which runs before the claim is taken, where a launch may not
// write — reads the same answer without writing it: only existingLaunch
// backfills.
func recordedLaunch(ctx context.Context, s *runstore.Store) (*runstore.Launch, bool, error) {
	if l, err := s.ReadLaunch(ctx); err == nil {
		return l, false, nil
	} else if !errors.Is(err, runstore.ErrNotFound) {
		return nil, false, remote(err)
	}
	rec, err := s.ReadRecord(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, remote(err)
	}
	l := &runstore.Launch{Version: 1, Execution: rec.Execution, LaunchedAt: rec.StartedAt}
	if id, ok := backend.ParseExecution(rec.Execution); ok {
		l.Job = id.Job
	}
	return l, true, nil
}

// existingLaunch is the run's launch, or nil when it has not launched. When
// the CLI that launched died before writing launch.json, it backfills one
// from result.json, where the runner records the same canonical execution
// name launch.json holds. Either name must be the run's own execution: the
// run's service account can write both objects, and the execution's own
// FUGARO_RUN is what says whose it is (checkExecution).
func existingLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec) (*runstore.Launch, error) {
	runID := spec.RunID
	check := func(name string) error {
		if _, err := env.checkExecution(ctx, nil, name, s.Slug(), runID); err != nil {
			return remote(fmt.Errorf("run %s/%s: not following its execution: %w", s.Slug(), runID, err))
		}
		return nil
	}
	l, staged, err := recordedLaunch(ctx, s)
	if err != nil || l == nil {
		return nil, err
	}
	if !staged {
		if err := check(l.Execution); err != nil {
			return nil, err
		}
		return l, nil
	}
	if l.Execution == "" {
		// It ran already, with no cloud execution (a local run against this
		// bucket): never launch it again. Nothing to backfill, and nothing
		// is stored: a run with no launch.json has no plan task for any
		// reader to find, the same way it has no launcher (LaunchedBy).
		return &runstore.Launch{Version: 1, RunID: runID, LaunchedAt: l.LaunchedAt}, nil
	}
	if err := check(l.Execution); err != nil {
		return nil, err
	}
	// The backend is the one this command selected, read from its codec, not
	// the constant Cloud Run's: a backfilled launch of a run that ran
	// elsewhere must be recorded as that backend's, or fugaro logs, cancel
	// and diagnose read the record against the wrong platform's names
	// (design compute-backend.md §2.5). The execution and the job are the
	// run's own record's (recordedLaunch); the run id and the plan task are
	// this call's, as they are for a fresh launch.
	backfill := &runstore.Launch{Version: 1, RunID: runID, Backend: launchBackend(env.be), Execution: l.Execution, Job: l.Job, PlanTask: spec.PlanTask, LaunchedAt: l.LaunchedAt}
	if err := s.WriteLaunch(ctx, backfill); errors.Is(err, runstore.ErrExists) {
		if l, err = s.ReadLaunch(ctx); err != nil {
			return nil, remote(err)
		}
		return l, nil
	} else if err != nil {
		return nil, remote(err)
	}
	return backfill, nil
}

// catalogDockerAsk answers whether this run needs a Docker daemon because the
// image the job it is about to start IS DEPLOYED WITH carries the docker tag
// (D1149), and refuses the launch where the backend that job would run on
// cannot give it one.
//
// The image is the deployed job's own and never a fresh selection. A job's
// image is fixed when the job is deployed (F7) — Cloud Run cannot vary it per
// execution — so a catalog.Choose run here would answer about an image the
// run does not start on: the catalog may have been edited, or the published
// policy republished, between the deploy and this launch. The entry is
// therefore read back from the deployed image (CatalogImageEntry of the
// shared registry's catalog-<entry>:latest, or the entry's own reference when
// the catalog uses a prebuilt image), and the tag's rule is asked of that
// entry.
//
// It is the tag's own rule and not the workflow's `docker: true`, which the
// backend refuses by itself (LaunchSpec.Docker is that ask, and the workflow
// key reaches it through the task): a docker-tagged entry is an ordinary
// candidate on the VM backend and never one on Cloud Run, which is decided
// where the job's image is chosen (internal/infra) and judged here again
// because the launcher is what picks the backend — a job deployed for one
// backend, launched onto the other, would otherwise start and die on its
// first docker command.
//
// Which jobs the project's images speak for is the one resolver (catalog.
// JobImage, D1900), asked exactly as the deploy-time spec builder asks it: a
// workflow that runs the base image or a shared layer is not one of them, and
// neither is one that wrote an image definition of its own while the project's
// default is an entry such a job cannot run — the resolver sets that default
// aside for it, so its job runs the image this project builds for the
// repository. Asking the rule on either one's behalf would refuse a launch for
// an entry the job does not run.
//
// A catalog that no longer agrees with the deployed image — the entry the
// image is was removed or renamed, the workflow now pins another one, or the
// entry no longer covers what the workflow needs — is refused, naming `fugaro
// update`: the launcher cannot change a deployed job's image, and going on
// would apply the tag's rule to an entry the run does not have. Nothing else
// the ask can say refuses a launch: a backend that has no deployed job to
// read (Cloud Batch starts one per run and keeps none) cannot be asked at all,
// a job not deployed yet is refused by the launch's own step, and a launcher
// with no checkout of this repository reads no workflow file, so whether the
// catalog speaks for this workflow is the deploy-time arm's to refuse (F7) —
// the ask answers nothing here, and the deployed image's entry is still read
// and stamped (deployedCatalogEntry).
//
// The workflow it judges is the one in this launcher's working tree, as the
// artifact gate's recipe is (recipeForLaunch) and for the same reason: the
// launcher has the operator's checkout and the runner reads the task's ref. A
// launch whose ref says something else is therefore judged by the tree, which
// is the residual the deploy-time arm does not have — that arm builds the spec
// the job is deployed with, from the same file (F7).
func catalogDockerAsk(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) (bool, error) {
	entry, ok, err := deployedCatalogEntry(ctx, env, slug, spec)
	if err != nil || !ok {
		return false, err
	}
	return catalogDockerOn(env, spec.Workflow, entry)
}

// deployedCatalogEntry is the catalog entry the job this launch starts was
// DEPLOYED with, and whether the catalog speaks for that job's workflow at all
// (the second is what the tag rule is asked on). It is catalogDockerAsk's own
// resolution, split out because the launch needs the entry itself beside the
// docker answer: the services the entry declares are stamped into the launch's
// environment (servicesLaunchEnv), and they have to be the deployed entry's
// for the same reason the tag rule does — a job's image is fixed when the job
// is deployed, so a fresh selection could name a different entry than the run
// starts on (D1149).
//
// The entry is read from the deployed job's IMAGE alone, and the workflow file
// only decides whether the catalog speaks for it: the two must come apart
// because the file is not always there. The desktop console spawns `fugaro run
// --repo` from a private empty directory on every machine, and `--repo` may be
// run from anywhere, so a launcher with no checkout of this repository reads
// no fugaro.yaml and cannot ask the resolver — while the job it is about to
// start is still the one the catalog deployed, and the services its tests need
// are still that entry's. Reading the workflow first (as this did) left such a
// launch with the zero entry, an empty stamp and a run whose tests fail on a
// service nobody started, with nothing said about why (F12).
//
// So what a missing workflow file costs is the tag rule's answer — the pin and
// coverage claims below are claims ABOUT the workflow, and nothing refuses
// over a workflow this launcher cannot read — and what it costs nothing is the
// entry: the deployed image is the catalog's whether or not a checkout here
// can read the file that describes the job.
//
// (`catalogDockerAsk` stays as the two composed for the callers that want the
// answer alone, which is every reader of the tag rule.)
func deployedCatalogEntry(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) (config.Entry, bool, error) {
	// The published node is read for its catalog alone, never as a decision
	// about this launch (D10, R2-8(a)): a policy this CLI cannot read — a
	// version ahead of it, a key it does not know — is refused by the launch
	// gate above launchRun, in its own words, and by the run itself, in the
	// runner's, and neither is here. A launcher that calls jobs.run directly
	// must not be stopped by a read this makes to stamp the entry it starts
	// on (TestPolicySinceGateIsNotOnTheRunnerPath), as project_job.go's own
	// existence read states it. What a read that fails costs is the published
	// catalog: this machine's own entry stands, exactly as it does on an
	// installation that has published no policy.
	var published *config.Catalog
	var publishedMap map[string]string
	var pol *projectpolicy.Policy
	if p, found, err := env.policy.read(ctx, env.lc); err == nil && found && p != nil {
		pol, published, publishedMap = p, p.ImageCatalog, p.ImageMap
	}
	cat := env.lc.ImageCatalogEffective(published)
	if cat == nil {
		return config.Entry{}, false, nil
	}
	// The workflow this launcher has a file for, if it has one at all, and
	// whether the catalog speaks for it. The one resolver, asked the way the
	// deploy-time arm asks it (F21): the project's images decide this job's
	// entry, be it through the workflow's own reference, the project's map or
	// the catalog's default.
	speaks := false
	var w config.Workflow
	var sel catalog.Selection
	var selErr error
	if co := checkoutConfig(ctx, spec.Repo); co != nil {
		if wt, ok := co.Workflows[spec.Workflow]; ok {
			w = wt
			sel, selErr = catalog.JobImage(catalog.ImageRequest{
				Catalog:   cat,
				ImageMap:  env.lc.ImageMapEffective(publishedMap),
				Canonical: spec.Repo,
				Workflow:  w,
				Backend:   catalog.Backend(launchBackend(env.be)),
			})
			// A resolver that refused the configuration is the catalog's own
			// claim on the job (the deploy-time arm refuses it the same way),
			// so it is not the answer that the catalog is silent.
			speaks = selErr != nil || sel.Source == catalog.SourceCatalog
		}
	}
	// The job is the workflow's own when it is a shared project job: the
	// catalog's jobs are per-repository ones, and the sole kind a shared
	// project job runs — a clone workflow (launchProjectJobFor) — is the kind
	// the resolver takes out of this rule (SourceBase and SourceShared).
	variant, ok := jobWorkflow(spec.Workflow, spec.OAuthToken)
	if !ok {
		// Not a job name: the launch itself refuses the variant a few lines
		// on.
		return config.Entry{}, false, nil
	}
	name := gcp.JobName(slug, variant)
	be, ok := env.be.(jobImageReader)
	if !ok {
		// A backend that keeps no deployed job to read (Cloud Batch starts
		// one per run and keeps none) cannot say what image the run starts
		// on: the tag's rule has nothing to judge here, and neither has the
		// entry's services — Batch runs every job on the installation's own
		// image and not a catalog entry's, so no entry is what the run starts
		// on and the stamp is the empty one. The installation that turns that
		// backend on states that contract with it (design compute-backend.md
		// §8.1); until then the config refuses it (internal/localcfg).
		return config.Entry{}, false, nil
	}
	image, deployed, err := be.JobImage(ctx, name)
	if err != nil {
		// Refused rather than answered empty, whatever the workflow file
		// says: the entry is what decides whether this run's tests get their
		// backing services, and a launch that cannot read its own job is one
		// nobody can say that about.
		return config.Entry{}, false, remote(err)
	}
	if !deployed {
		// Not deployed: nothing can start on an image none of this has seen,
		// and the launch's own step refuses the missing job.
		return config.Entry{}, false, nil
	}
	entry, ok := catalogEntryOf(*cat, image)
	if !ok {
		if !speaks {
			// A workflow the catalog does not speak for keeps its own image,
			// which is no entry's: the empty stamp, as for a job the catalog
			// gave no image to at all.
			return config.Entry{}, false, nil
		}
		return config.Entry{}, false, catalogMovedErr(spec.Workflow, name, image)
	}
	// A configuration the resolver refuses — this job's own reference, a pin or
	// the map's entry, naming a name the catalog does not hold — is refused at
	// launch too. The deploy-time arm refuses it the same way (F7), so the job
	// cannot have been deployed with what this launcher's file names, and the
	// image it happens to carry is not the one this configuration asks for:
	// going on would start the run on it and say nothing. The refusal carries
	// the resolver's own sentence, which names the entry there is not (a pin)
	// or the map that points at it (T1 b), and it is the same family as the
	// moves below because the person's fix is the same one command.
	//
	// Only a reference naming an entry the catalog does not hold is refused
	// here. The resolver's other errors are the tag-cover and docker rules,
	// which say which entries serve a job rather than which one it runs: an
	// entry the deployed image is but that no longer covers what the workflow
	// needs comes out of the lookup above, the coverage check below or the
	// docker rule, each with the reason the person needs.
	if speaks && errors.Is(selErr, catalog.ErrUnknownEntry) {
		return config.Entry{}, false, catalogClaimsErr(spec.Workflow, name, image, selErr)
	}
	// The entry the project's configuration NAMES for this job — a pin, or what
	// the project's map gives the repository (D1155, D1906) — against the entry
	// its deployed image is: both are claims about the deployed job, and a job
	// deployed with an image the project no longer names for it is one this
	// catalog does not give it. An entry the tag-cover rule chose is checked
	// below instead, by the coverage the choice was made on: the deploy fixed
	// the job's image, and which entry the rule would choose now is not a claim
	// about the job (F7).
	if speaks && sel.Reference && sel.Entry.Name != entry.Name {
		return config.Entry{}, false, catalogMovedErr(spec.Workflow, name, image)
	}
	// The tags the workflow needs, against the entry the job actually runs:
	// the deploy refused an uncovered needed set before the job existed, so
	// this one changed under it.
	if speaks && !catalog.Covers(entry.Tags, w.Needs) {
		return config.Entry{}, false, catalogMovedErr(spec.Workflow, name, image)
	}
	// The services the entry provides, from the same side the entry itself
	// came from: this machine's own catalog when it writes one — the list is a
	// config field and never a member of the node's block (F20) — and the
	// published node's own key when the node is what speaks for the catalog
	// (localcfg's keep rule, ImageCatalogEffective). The launch stamps the
	// answer into the run's environment (servicesLaunchEnv), which is the only
	// way the run hears it: the node's block cannot carry it without breaking
	// readers of the release behind.
	if env.lc.ImageCatalog == nil {
		entry.Services = pol.ServicesFor(entry.Name)
	}
	return entry, speaks, nil
}

// catalogDockerOn answers the docker tag's rule for the entry a job was
// deployed with (D1149): whether this run needs a Docker daemon because that
// entry's image carries the docker tag, and refuses the launch where the
// backend the job would run on cannot give it one. The entry carries the tag
// or it does not; the backend decides the rest.
func catalogDockerOn(env *cloudEnv, workflow string, entry config.Entry) (bool, error) {
	if !slices.Contains(entry.Tags, catalog.DockerTag) {
		return false, nil
	}
	if catalog.Backend(launchBackend(env.be)) == catalog.Batch {
		return true, nil
	}
	// The deployed image carries the tag on a backend with no daemon.
	return false, userErr("workflow %s: %v", workflow, catalog.DockerOnCloudRunError())
}

// jobImageReader is the backend's deployment read (gcp.Backend.JobImage): the
// image a job is deployed with. Declared where it is used, like the optional
// backend methods the warnings take (installedChecks): a backend whose jobs
// exist only for the length of a run answers it not at all.
type jobImageReader interface {
	JobImage(ctx context.Context, job string) (string, bool, error)
}

// catalogEntryOf is the one catalog entry whose image image is, and whether
// the catalog holds exactly one: the deployed image read against the
// spellings the deploy gives a job's image (internal/infra's repoCtx.
// catalogImage) — an entry the catalog builds is the shared registry's
// catalog-<entry>:latest (D1152), and one whose source is a prebuilt
// reference is that reference itself. No entry is a catalog that no longer
// gives this job its image, and two would make the tag rule's answer a guess.
func catalogEntryOf(cat config.Catalog, image string) (config.Entry, bool) {
	built, shared := gcp.CatalogImageEntry(image)
	var found config.Entry
	n := 0
	for _, e := range cat.Entries {
		matches := e.Source.Kind == config.SourceImage && e.Source.Image == image
		if !matches && shared && e.Source.Kind != config.SourceImage {
			matches = e.Name == built
		}
		if matches {
			found, n = e, n+1
		}
	}
	return found, n == 1
}

// catalogMovedErr refuses a launch whose job was deployed with an image this
// catalog no longer gives its workflow. The job keeps the image it was
// deployed with (F7), so the fix is to deploy it again, and the message names
// the one command that does.
func catalogMovedErr(workflow, job, image string) error {
	return userErr("workflow %s: the job %s is deployed with the image %s, which this project's image catalog no longer gives it; a job keeps the image it was deployed with, so run `fugaro update` to deploy it again with this project's catalog", workflow, job, image)
}

// catalogClaimsErr refuses a launch whose own configuration the resolver
// refuses: the reference this launcher's file makes is one the project's
// catalog cannot answer, so the job was not deployed with it and no image it
// carries is the one this file asks for. The resolver's sentence says which
// reference is wrong (why is its own); what this adds is the deployed image the
// launch would otherwise have started on, and the one command that deploys the
// job again once the file and the catalog agree.
func catalogClaimsErr(workflow, job, image string, why error) error {
	return userErr("workflow %s: %v; the job %s is deployed with the image %s, and a job keeps the image it was deployed with, so fix the reference above and run `fugaro update` to deploy it again with this project's catalog", workflow, why, job, image)
}

// launchRun starts spec's execution, stored under slug, at most once, however many CLIs race
// on the same run ID (design §4.7, Review Focus 1):
//
//   - launch.json (or result.json's execution) present → already launched.
//   - Otherwise take the "launching" claim with create-if-absent. The claim
//     is never deleted after a launch, so a later caller always finds it,
//     then re-reads launch.json, which the claim holder writes before it
//     returns.
//   - A held claim is read once, content and generation together. If that
//     content is stale (older than claimTTL, its holder presumably dead), it
//     is taken over with an overwrite matched to that same generation, never
//     delete-then-create, so two takers can't both win. A claim that
//     vanished before the read counts as absent: claim once more.
//   - A fresh claim, or a lost takeover, means someone else is launching:
//     wait up to claimWait for their launch.json and report it, else
//     exit 1, "still in flight". A claim that disappears while we wait was
//     released after a refused launch: go back for it at once.
//   - A run with a cancel marker is never launched.
//   - Having won the claim, re-check launch.json and result.json: an earlier
//     holder may have launched just before going stale.
//   - Launch. Only a definitive refusal (backend.ErrRejected) releases our
//     claim; after an ambiguous error the execution may exist, so the claim
//     stays until it goes stale, by which time the runner's result.json says
//     whether it started.
//
// file:// buckets are single-user: fileblob's IfNotExist is not atomic.
func launchRun(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, now time.Time) (launchResult, error) {
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	res := launchResult{Run: slug + "/" + spec.RunID, Repo: spec.Repo, RunID: spec.RunID, Branch: "fugaro/" + spec.RunID,
		PR: spec.PR, PreviousRun: spec.PreviousRun}
	if spec.Branch != "" {
		res.Branch = spec.Branch
	}
	if !spec.IsFollowUp() && len(spec.ReviewPRs) == 0 {
		res.Ref = spec.Ref
	}
	done := func(l *runstore.Launch, status string) (launchResult, error) {
		res.Execution, res.LogURL, res.Status = l.Execution, l.LogURL, status
		return res, nil
	}
	if l, err := existingLaunch(ctx, env, s, spec); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	if cancelled, err := s.CancelRequested(ctx); err != nil {
		return res, remote(err)
	} else if cancelled {
		return res, userErr("run %s was cancelled; start a new one", task.ShortRun(oneLine(res.Run)))
	}
	// The job this launch runs on (project_job.go): the shared project job of
	// the profile the workflow selects, or nil for the per-repository job this
	// launch has always used. Resolved before the claim is taken, so a policy
	// this CLI cannot read costs no claim to release — and it is resolved
	// before the mint, because it is what the token's window is measured
	// against.
	job, err := env.launchProjectJobFor(ctx, slug, spec)
	if err != nil {
		return res, err
	}
	// A review launch runs on the review-only variant of that job and carries
	// the marker its runner branches on (review.go, CSR7): never on the
	// shared job itself, which mounts the App key the review container exists
	// to be without, and always marked, so the run records its verdict in the
	// artifact instead of writing a comment on the pull request it read.
	reviewJob := spec.Mode == recipe.ModeReview
	if reviewJob {
		if job, err = env.reviewJobFor(ctx, job); err != nil {
			return res, err
		}
	}
	// An account-axis value that is no account, named as the run's own
	// credential (claudeAccountAllowed's arm), is placed on the shared project
	// job whose account segment is that value — and on the review-only variant
	// of it, which is the one job a review served by that credential runs on
	// (D1417, D1422, AK6a). A launch the shared path resolved can be on either;
	// a per-repository launch derives its job's name from the account segment
	// (gcp.WorkflowVariant), and no installation deploys a job of its own under
	// this one. Starting it would be Cloud Run's 404 for a job this
	// installation was never asked for; the launch stops here instead, before
	// the claim, as the account gate's refusals do.
	if job == nil && config.AxisNonAccountValue(spec.OAuthToken) {
		return res, userErr("this launch names %s (%s) as the run's own account, and this repository runs on a job of its own: the value is not a Claude account and no per-repository job holds it, so the job this launch derived from the name is one no installation was ever asked for. The jobs it names are the shared project job of a profile that lists it and that job's review-only variant, and a repository reaches them from a workflow whose checkout is clone. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Run this repository from a clone workflow, or launch without --token",
			nonAccountPhrase(spec.OAuthToken), spec.OAuthToken)
	}
	hook(launchHooks.beforeClaim)
	holder := claimHolder()
	for round := 0; ; round++ {
		won, other, err := takeClaim(ctx, env, s, holder, now)
		if err != nil {
			return res, err
		}
		if won {
			break
		}
		l, err := waitForLaunch(ctx, env, s, spec, res.Run, other)
		if errors.Is(err, errClaimReleased) {
			// The holder's launch was refused and it released the claim:
			// nothing started, so try for the claim again at once.
			if round < claimRounds-1 {
				continue
			}
			return res, userErr("the launch claim of %s keeps being released by other launches that were refused; fugaro run --retry %s tries again", task.ShortRun(oneLine(res.Run)), oneLine(res.Run))
		}
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	// We hold the claim. Everything from here is bounded well inside
	// claimTTL, measured from the claim's own timestamp (now), which is what
	// other CLIs judge staleness on: none can take it over while we work.
	hctx, cancelHold := context.WithDeadline(ctx, now.Add(holdTimeout))
	defer cancelHold()
	hook(launchHooks.afterClaim)
	if l, err := existingLaunch(hctx, env, s, spec); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	// A cancel that landed while we claimed: never launch it, and release
	// the claim so a waiting cancel sees the launch end.
	if cancelled, err := s.CancelRequested(hctx); err != nil {
		return res, remote(err)
	} else if cancelled {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, userErr("run %s was cancelled; start a new one", task.ShortRun(oneLine(res.Run)))
	}
	// The entry the job this launch starts on was DEPLOYED with, read once
	// for the two facts about that image the launch needs (D1149, D1904): the
	// docker tag's rule below, and the services the entry declares, which are
	// stamped into the launch's environment further down so the run starts
	// exactly what its own entry lists. It is the docker tag's rule and not a
	// fresh selection: a run whose DEPLOYED job image carries the docker tag
	// is refused here on a backend with no daemon, and on the VM backend the
	// answer asks the backend for the socket. Before the mint below, so a
	// refused launch has no token to drop, and before the declaration, which
	// is the coordinator's record that this run is about to work — a refusal
	// must leave nothing behind that says otherwise.
	//
	// catalogued is whether the catalog speaks for this workflow at all, which
	// is what the tag rule is asked on and NOT whether the entry was found:
	// a launch with no checkout of this repository resolves the entry from the
	// deployed image alone (deployedCatalogEntry), so it has services to stamp
	// and no workflow file to ask the tag rule about.
	entry, catalogued, err := deployedCatalogEntry(hctx, env, slug, spec)
	if err != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, err
	}
	var docker bool
	if catalogued {
		if docker, err = catalogDockerOn(env, spec.Workflow, entry); err != nil {
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			releaseClaim(rctx, env, s, holder)
			rcancel()
			return res, err
		}
	}
	// The launch declares itself to the coordinator's record (P4), beside the
	// claim it just won: the caller the task already names, the branch the run
	// is about to work on and the release that branch targets. It is a
	// declaration to an ADVISORY service, so nothing here can fail the launch
	// — a project with no database to declare to is skipped, and a report that
	// fails is a warning on stderr — and it is bounded well inside the hold,
	// because the launch's own work must not wait on it (spec §3: the
	// coordinator never blocks anything). A write the rules refuse is quiet:
	// only budget admins may write the record, so a refusal is what most
	// launchers get on every run, and a warning each time would be noise about
	// something they cannot change (owner ruling 2026-10-24). The error text can
	// quote what the database answered, so it goes through oneLine. The call
	// goes through the build's declarer (extend.go): an untagged build
	// registers none, so its launches declare nothing (D535), and a tagged
	// build registers the coordinator's own writer.
	if err := declareLaunch(hctx, env, slug, spec); err != nil && !errors.Is(err, rtdb.ErrPermission) {
		fmt.Fprintf(launchWarn, "warning: the launch was not reported to the coordinator: %s\n", oneLine(err.Error()))
	}
	// The run's budget identity: minted, sealed under a key drawn for this
	// launch and left in the bucket before the execution can start, by the
	// one CLI that holds the claim. A failure here means nothing started, so
	// the claim goes back. sealKey is the only copy of the key: it goes into
	// this execution's marker and nowhere else (D15). job, when the launch
	// resolved a shared project job, is the job the identity window is
	// measured from (project_job.go).
	sealKey, err := env.mintBudgetToken(hctx, slug, spec, job)
	minted := sealKey != nil
	if err != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		rcancel()
		return res, err
	}
	// The workflow variant is the per-repository job's name, so it is derived
	// where the job is the repository's own and nowhere else: a launch that
	// resolved a shared project job runs the job the project named (lspec.Job
	// below), and the backend reads no workflow from the spec for a job it is
	// handed by name. It is also the one derivation the account axis's
	// non-account value cannot survive — gcp.WorkflowVariant refuses the
	// sentinel's doubled hyphen — and a launch on the job the sentinel's
	// profile deploys needs no per-repository name at all.
	variant, ok := spec.Workflow, true
	if job == nil {
		variant, ok = jobWorkflow(spec.Workflow, spec.OAuthToken)
	}
	if !ok {
		// spec.Workflow, unlike spec.OAuthToken, is never shape-checked by
		// task.Parse: a bad --workflow, a stored task from before this
		// feature validated anything about it, or tampered bucket data could
		// all reach here. Definitive, same cleanup as a platform refusal:
		// nothing started.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		releaseClaim(rctx, env, s, holder)
		if minted {
			env.dropBudgetToken(rctx, slug, spec.RunID)
		}
		rcancel()
		return res, userErr("workflow %s is not a plain workflow name, so it can't be combined with Claude account %s; nothing started", oneLineCLI(spec.Workflow), pluginwire.Printable(spec.OAuthToken))
	}
	lctx, cancel := context.WithTimeout(hctx, launchTimeout)
	lspec := backend.LaunchSpec{Repo: backend.RepoRef{Repo: spec.Repo, Slug: slug}, Workflow: variant, RunID: spec.RunID, Timeout: launchTimeoutOf(spec), Docker: docker}
	if job != nil {
		// The shared project job, by its own name. FUGARO_RUN is still set
		// (backend/gcp's Launch): on a job every repository shares it is the
		// only thing that says which run an execution is.
		lspec.Job = job.Name
	}
	if minted {
		// This execution carries a budget token, so the runner must never read
		// the token's absence as "this project has published no policy": with
		// the marker set, a missing token stops the run whatever the job's own
		// mode (internal/runner's BudgetTokenEnv, plan R2-8(a)). It is an
		// execution override — minting is a fact about this launch, not about
		// the job's deploy-time environment — and its value is the launch's
		// seal key, the one thing that opens the object (D15): handed to this
		// execution and no other. A backend that cannot carry a secret where
		// only the execution and the override's own audience read it refuses
		// the launch rather than carry it (backend.LaunchSpec.SecretEnv).
		lspec.SecretEnv = []string{sealMarker(sealKey)}
	}
	// The credentials service, when its switch is on for this repository and
	// this execution is a repository's own job (job is the shared project job
	// otherwise, and the service refuses that caller: see the helper). The
	// pair the switch is judged with is repoServiceAuth's, read the same way
	// by the proxy gate that has to know whether this launch is a proxied one
	// (proxiedRepo) — one reader, because a gate reading the published node
	// alone would refuse a launch whose run holds the repository's own token.
	jobName := ""
	if job != nil {
		jobName = job.Name
	}
	svcAuth, deployed := env.repoServiceAuth(ctx, spec, launchWarn)
	lspec.Env = credentialsServiceLaunchEnv(env.lc, jobName, spec.Repo, svcAuth, deployed)
	// The run's slip, asked for exactly where the service's address was just
	// written: the grant the credentials service keeps for this run, whose id
	// the job claims at its first call and presents on every call after it
	// (DS1.3's two routes). It goes on THIS EXECUTION's environment and never
	// into a stored document: the task object is the repository's own file, a
	// repeated run ID repeats it byte for byte, and a slip is one run's.
	if url, ok := credentialsServiceURLOf(lspec.Env); ok {
		lspec.Env = append(lspec.Env, runSlipEnv(hctx, env, spec, slug, variant, url)...)
	}
	// The services the job's own catalog entry declares, on EVERY launch and
	// empty where the deployed image is no catalog entry (backend.ServicesEnv
	// states why the key is always written, and why it is not a reserved
	// name): the runner reads the entry's list back to start the services its
	// tests need, and a key always set is what keeps an image's own ENV under
	// the same name from answering for the entry. entry is the zero value for
	// a job whose deployed image no entry is, which writes the empty stamp.
	lspec.Env = append(lspec.Env, servicesLaunchEnv(entry))
	if reviewJob {
		// The marker is the whole fact (internal/runner's EnvReviewJob): the
		// variant's own deployment carries the credentials service's address,
		// and this is the one thing about the execution the launcher has to
		// add — a task field is a repository's to write, and which job is
		// running is the installation's own fact.
		lspec.Env = append(slices.Clone(lspec.Env), runner.EnvReviewJob+"=1")
	}
	ref, err := env.be.Launch(lctx, lspec)
	cancel()
	// A backend error may quote the request it was given; the key is in that
	// request, so it is taken out of the error before anything formats it.
	err = sealRedact(err, sealKey)
	if err != nil {
		if errors.Is(err, backend.ErrRejected) {
			// Definitive: the platform refused, so nothing started. Drop our
			// claim so a corrected retry can go at once.
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			releaseClaim(rctx, env, s, holder)
			if minted {
				env.dropBudgetToken(rctx, slug, spec.RunID) // nothing started, so nothing will take it
			}
			rcancel()
			// A GCP API error is a remote failure (exit 2); %w keeps
			// ErrRejected and ErrNotFound visible to errors.Is.
			return res, remote(fmt.Errorf("the launch of %s was refused, so nothing started; fix the cause, then fugaro run --retry %s: %w", task.ShortRun(oneLine(res.Run)), oneLine(res.Run), err))
		}
		// Ambiguous (timeout, 5xx, dropped connection, unreadable reply): the
		// execution may exist. Keep the claim, and the token object: an
		// execution that did start needs it (a --retry mints another); the runner's result.json tells
		// the truth, and --retry reads it.
		return res, remote(fmt.Errorf("launching %s: the outcome is unknown (%w); don't relaunch before %s: fugaro run --retry %s then reports the execution if it started, or launches it",
			res.Run, err, now.Add(claimTTL).UTC().Format(time.RFC3339), res.Run))
	}
	// The backend the launch actually used, from the seam, as above: the
	// launch succeeded on env.be, so env.be's codec is what names it.
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: launchBackend(env.be), Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, PlanTask: spec.PlanTask, LaunchedAt: now.UTC()}
	if err := s.WriteLaunch(hctx, l); errors.Is(err, runstore.ErrExists) {
		theirs, rerr := s.ReadLaunch(hctx)
		if rerr != nil {
			return res, remote(rerr)
		}
		return done(theirs, "already-launched")
	} else if err != nil {
		// The claim stays: nobody may relaunch until it goes stale, by which
		// time the runner has written result.json's execution for --retry.
		return res, remote(fmt.Errorf("launched %s but could not record launch.json (fugaro run --retry %s records it from result.json): %w", ref.Name, res.Run, err))
	}
	return done(l, "launched") // the claim stays in place for good
}

// repoServiceAuth is the pair credentialsServiceLaunchEnv judges the switch's
// third condition with, read the same way by its two callers: the launch that
// writes the variable onto the execution, and the proxy gate that has to know
// whether the run it starts is a proxied one (proxiedRepo). One reader,
// because the two answers have to be one: a gate that judged proxiedness from
// the published node alone refuses, as brokered, a launch whose run is handed
// no service and holds the repository's ordinary Bitbucket token.
//
// The auth is the one the RUN will read — the ref's fugaro.yaml, else this
// machine's checkout of it (launchFileConfig, the same document
// cascadeLaunchRefusal judged a moment ago) — and never a copy stored when the
// record was written: an edit to it takes effect without another update, so a
// repository moved back to an auth the service does not serve is withheld at
// once. The record's own half carries the auth the apply that wrote it was
// built for, so the other direction needs no edit at all: a root applied
// before its owner committed the cascade is recorded held off, the job keeps
// the mounts that apply kept, and the address is never handed to an account
// that holds no roles/run.invoker to knock with.
//
// The read is skipped where there is no block to switch to: the answer is the
// same nil, and an installation with no service pays nothing for the gate it
// cannot pass.
//
// A document this launcher cannot read at all — `fugaro run --repo X` from a
// directory that holds no checkout of X, where launchFileConfig finds neither
// the run's ref in an object database nor a working tree to fall back to —
// leaves the auth unknown, and the switch's third condition cannot be judged
// here. The repository's own record stands in for it then, and this machine's
// tfvars of the repository root are the second answer
// (deployedRepoCredentialsService). Withholding the address instead would
// start a `cascade` run whose job mounts no model credential and halt at the
// service's door (D143) — the state every launch from outside a checkout was
// in before the switch was written down, and the one the desktop console's own
// launches would be in on every machine that did not apply the root itself: it
// spawns from a private empty directory, so launchFileConfig never answers
// there.
//
// warn is where the one failure this read can report goes. The launch passes
// the launch's own writer; the gate passes io.Discard, because it is the same
// read of the same documents and the launch words the warning once.
func (e *cloudEnv) repoServiceAuth(ctx context.Context, spec *task.Spec, warn io.Writer) (string, bool) {
	if e.lc == nil || e.lc.CredentialsService == nil {
		return "", false
	}
	auth := checkoutAgentAuth(launchFileConfig(ctx, e, spec))
	if auth != "" {
		return auth, false
	}
	deployed, err := deployedRepoCredentialsService(e.lc, spec.Repo)
	if err != nil {
		fmt.Fprintf(warn, "warning: whether %s's repository root carries the credentials service could not be read from this machine's tfvars (%s): the launch decides from the repository's record alone\n", spec.Repo, oneLineCLI(err.Error()))
	}
	return "", deployed
}

// servicesLaunchEnv is the launch entry that stamps the services the job's
// own catalog entry declares: backend.ServicesEnv set to the entry's list,
// space separated, which is exactly the value `fugaro-services` reads.
//
// It is one entry on EVERY launch, the empty value included, and that is the
// whole point of writing it here rather than on the job at deploy time (which
// internal/infra does for the image-entry stamp): the runner reads its own
// environment, and on a job whose image is a catalog entry's that environment
// carries the image's own ENV — so an image that set FUGARO_SERVICES itself
// would have the run start services no entry declared, and the entry's list is
// the only source these services may come from. An entry the launch sets
// overrides whatever the image holds, and a zero entry (a job the catalog does
// not speak for, or one whose entry declares no service) writes the empty
// stamp, which the runner reads as "no service" rather than as the script's
// own default of all three.
func servicesLaunchEnv(entry config.Entry) string {
	return backend.ServicesEnv + "=" + strings.Join(entry.Services, " ")
}

// credentialsServiceLaunchEnv is what this launch puts on the execution's
// environment for the credentials service (plan task C1.24, D146/D147): the
// service's URL, under the one name the runner and the keyless GitHub path
// both read, or nothing at all. job is the shared project job this launch
// resolved, or "" for a repository's own job, repo is the repository the run
// works on and auth its agent.auth as the run will read it (the ref's
// fugaro.yaml, else the checkout's).
//
// THE SWITCH DECIDES, and it is the same function the plan decides with
// (infra.CredentialsServiceServes): the installation has the block, the record
// `fugaro update` wrote for this repository says a root of it was applied with
// the service on — the bundle held a model credential and the auth was the
// cascade when that apply was made (localcfg.Repo.CredentialsServiceReady,
// written into the local config's repository entry and published with the rest
// of it, so a teammate's `fugaro run` on a machine that never ran the update
// reads it too, internal/localcfg/shared.go) — and this repository's
// agent.auth, read live, is one the service serves. All three, because the URL
// is not a hint: the same variable is what
// opens the git provider keyless (internal/gitprov/providers/providers.go),
// which reads no App key at all once it is set. A job that still mounts its
// own model credential and is handed the address of a service that cannot
// serve it would lose the credential it has and get nothing in its place.
//
// The config is the published shared one where this machine has no local
// config of its own (localcfg.Select), and the URL in it is the installation's
// own record -- Cloud Run's assignment for the service the installation root
// created, which the credentials stage writes and MergeShared never lets a
// machine that lacks it erase (internal/localcfg/shared.go, the ledger item of
// PR #903). So a teammate's `fugaro run`, on a machine that never ran the
// opt-in, still hands the job the installation's service.
//
// A SHARED PROJECT JOB IS NOT TOLD, and that is the service's own rule rather
// than a policy chosen here: its admission refuses an account that belongs to
// a shared project job whole, before any route (internal/credsvc/auth.go's
// verifyJob, Global Constraint 6, C3.9). So the address would buy such a job
// nothing it could use -- a `cascade` run would fail at the service's door
// instead of at the runner's clear "this job has no credentials service" --
// and it would cost it something: the same variable is what opens the git
// provider keyless (internal/gitprov/providers/providers.go), so a shared
// project job told about the service would stop reading the App key it still
// mounts and be refused the mint that replaced it, breaking every GitHub run
// on it. The shared jobs keep their own credentials until C3.9 moves them,
// which is what SECURITY.md and the credentials stage's confirmation both say.
//
// It is plain Env and not SecretEnv, deliberately. The URL is not a secret:
// it is the service's public address, it is the audience every caller's ID
// token is minted for, and the service's own container carries it in plain
// environment (credentials_serve.go's envCredentialsServiceURL, the same
// name). A secret entry would also be the wrong carrier on the backend that
// matters: Batch refuses SecretEnv outright (backend.LaunchSpec.SecretEnv),
// so an installation on the Batch backend would either lose the variable or
// fail to launch, and the one thing this value must not do is depend on which
// backend a project happens to use.
//
// A repository the switch holds off gets nil, so its launch spec is byte for
// byte the one it built before this variable existed: no entry, no empty
// value, nothing for a backend to merge. The URL-less block is the same
// answer for a different reason -- there is no address to hand a job, and the
// service's revision does not exist yet (fillCredentialsAudience writes it on
// the apply after the one that created the service).
//
// The gate that judges whether a run is a proxied one asks this same function
// — a job it writes the variable onto is a proxied run's job and no other's
// (proxiedRepo) — so the two answers cannot drift apart.
//
// deployed is this machine's record of the repository root itself, read only
// where the document could not be (see the call site): the tfvars the last
// apply of that root was built from, which carry the service exactly when that
// apply dropped the job's mounts and made its grant
// (deployedRepoCredentialsService). An empty auth is judged on the deployed
// root OR the repository's own record, never on this machine's tfvars alone:
// a machine that never applied the root holds no tfvars of it — the desktop
// console spawns from a private directory, so this is every console launch —
// while the record is written only alongside the apply it describes, by that
// apply's machine or by an installation-wide backfill that read the
// installation's own state and corrects a record that root's last apply has
// outlived, in both directions, on the next `fugaro update` any machine of the
// installation runs — so it agrees with the root wherever it was applied. A
// record that lags a bundle emptied since the apply cannot withdraw the address
// on its own here: the deployed root still answers for the machine that applied
// it.

func credentialsServiceLaunchEnv(lc *localcfg.Config, job, repo, auth string, deployed bool) []string {
	if job != "" || lc == nil {
		return nil
	}
	// The URL is part of "installed" here and on this side alone: the
	// installation's block without it is a service whose revision carries the
	// module's unassigned placeholder as its audience, and there is no address
	// to put on a job's environment either.
	installed := lc.CredentialsService != nil && lc.CredentialsService.URL != ""
	local, _ := lc.RepoFor(repo)
	serves := infra.CredentialsServiceServes(installed, local.CredentialsServiceOn(), auth)
	if auth == "" {
		// No document this launcher could read, so the switch's third
		// condition is judged on the two answers that do not need one: the
		// repository's own record, and this machine's tfvars of the root it
		// was applied from. Either one saying on is enough — both are written
		// by an apply that dropped the job's mounts and granted its account
		// roles/run.invoker, and a launch that withheld the address from a
		// repository whose mounts are already gone would halt at the service's
		// door (D143).
		serves = installed && (local.CredentialsServiceOn() || deployed)
	}
	if !serves {
		return nil
	}
	return []string{envCredentialsServiceURL + "=" + lc.CredentialsService.URL}
}

// envSlip is the variable a run reads its slip's id from. It is the launcher's
// half of the name the runner claims the slip by (DS1.5, which reads its own
// environment), and it is spelled here because passing the id to the job is
// this task's (DS1.4). It is plain environment and never SecretEnv: the
// backend that matters refuses SecretEnv outright
// (backend.LaunchSpec.SecretEnv), and the slip's protection is its binding to
// the run and to a claim secret rather than its secrecy — the run's own agent
// can read the runner's environment anyway (F22, reconciliation).
const envSlip = "FUGARO_SLIP"

// The grant call's own bounds. slipGrantTimeout bounds the whole of it — the
// launcher's own mint and the call — so a service that hangs cannot eat the
// hold this launcher took to launch (holdTimeout, half of claimTTL, and the
// launch call itself is bounded by launchTimeout's two minutes).
const (
	slipGrantTimeout = 30 * time.Second
	// maxSlipAnswerBytes bounds what the grant route may answer, as the read
	// mint's own bound does (internal/gitprov/github's maxServiceAnswerBytes):
	// the service is another writer, and an unbounded answer is one a launch
	// renders into memory.
	maxSlipAnswerBytes = 16 << 10
	// maxSlipIDBytes bounds the id this launcher will hand a job. The service's
	// own bound is on its side of the wire (internal/credsvc's maxSlipIDBytes,
	// the hex of the bytes it draws), and this one is generous because a
	// launcher cannot read that constant: an id longer than any id the service
	// draws is one this launcher will not carry.
	maxSlipIDBytes = 256
)

// errSlipRouteAbsent means the credentials service does not serve the grant
// route at all — a revision built before DS1.3 — rather than refusing this
// launcher's call. It is the version skew DS1.4 keeps: the run proceeds on the
// path it took before the slip existed, quietly, because an installation one
// release behind is the skew `fugaro doctor` reports and the one `fugaro
// update` converges, and a warning on every launch of it would be noise about
// something the launcher cannot change (D1945's shape for an older CLI).
var errSlipRouteAbsent = errors.New("this credentials service does not serve the grant route")

// slipRouteAbsent reports whether status is the answer of a service that does
// not serve the grant route: the 404 its own router gives a path it does not
// serve (internal/credsvc's servedRoute, which answers 404 rather than 405 for
// a route asked with another method), and the 405 an intermediary may give a
// route it holds for another method. Neither is a refusal of this launcher.
func slipRouteAbsent(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}

// slipGrant is the grant call a launch makes, and a package variable because
// TestMain answers it for every test that is not about the slip itself
// (main_test.go): the call is made on every switched-on launch, and no other
// test may reach a service or mint a token. A test about the slip replaces it
// or hands in slipGrantOverService with a service of its own.
var slipGrant = slipGrantOverService

// slipIdentity mints the launcher's own Google ID token for serviceURL, which
// is the identity the service's launcher class verifies (DS1.2, D1938).
//
// It is the PLAIN mint the read path presents (reviewReadIdentity), for the
// audience rather than for the token: a plain mint's audience is gcloud's own
// client id (credsvc.GcloudAudience), which the launcher class admits and which
// is the only one a person's account can mint — gcloud refuses --audiences for
// a user account. That a gcloud-audience token replaying a member's own access
// also gets a slip at this route is accepted and documented rather than
// refused (D1939, D1944).
var slipIdentity = func(ctx context.Context, serviceURL string) (string, error) {
	return personIdentity(ctx, serviceURL, "a run's slip is asked for")
}

// slipGrantOverService is the grant call: it presents the launcher's own
// identity token to the grant route and reads the slip's id out of the answer.
//
// Nothing here can put a value a caller sent into an error: the identity token
// is never formatted into one, the request body is the launcher's own names,
// and the answer is read for its status, its size and its shape alone — the
// message beside an error code is written for an operator's log and not for a
// command's output (internal/gitprov/github's read mint drops it for the same
// reason).
func slipGrantOverService(ctx context.Context, serviceURL string, req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
	if err := checkSlipServiceURL(serviceURL); err != nil {
		return credsvc.SlipGrantResponse{}, err
	}
	identity, err := slipIdentity(ctx, serviceURL)
	if err != nil {
		return credsvc.SlipGrantResponse{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return credsvc.SlipGrantResponse{}, errors.New("the grant call's request could not be built")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+credsvc.SlipPath, bytes.NewReader(body))
	if err != nil {
		return credsvc.SlipGrantResponse{}, errors.New("the grant call's request could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return credsvc.SlipGrantResponse{}, errors.New("the installation's credentials service could not be reached")
	}
	defer resp.Body.Close()
	if slipRouteAbsent(resp.StatusCode) {
		return credsvc.SlipGrantResponse{}, fmt.Errorf("%w: the service answered %d", errSlipRouteAbsent, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return credsvc.SlipGrantResponse{}, fmt.Errorf("the installation's credentials service refused the grant call (%d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSlipAnswerBytes+1))
	if err != nil {
		return credsvc.SlipGrantResponse{}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxSlipAnswerBytes {
		return credsvc.SlipGrantResponse{}, errors.New("the credentials service's answer is larger than a grant's")
	}
	var ans credsvc.SlipGrantResponse
	if err := json.Unmarshal(data, &ans); err != nil {
		return credsvc.SlipGrantResponse{}, errors.New("the credentials service's answer is not a grant's")
	}
	return ans, nil
}

// slipClient is the client the grant call is made with: it never follows a
// redirect, because the request carries the launcher's own Google ID token and
// a followed 307 or 308 would carry it to whatever host the Location named
// (internal/gitprov's serviceMintClient and internal/gateway's upstreamClient
// refuse redirects for the same reason), it keeps no cookie jar, and it does
// not wait on an answer forever.
func slipClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return &http.Client{
		Transport:     t,
		Timeout:       slipGrantTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// checkSlipServiceURL refuses a URL this launcher will not send its own
// identity token to: the token is a bearer credential, and over plain http it
// would travel in cleartext. The rule is internal/gitprov's read-mint rule
// respelled (that package does not export it): https, or http on a loopback
// address, which is a test's own service. The refusal carries the URL's shape
// and never the URL.
func checkSlipServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the credentials service URL this launch was built with is not a scheme and a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return errors.New("the credentials service URL this launch was built with is not https")
}

// runSlipEnv asks the installation's credentials service for this run's slip
// and answers the environment entry that hands the job its id, or nothing at
// all when there is no slip to hand it.
//
// Every failure but a refusal of the caller is quiet and the run starts
// anyway, and that is not a hole in R9's no-downgrade rule: the fence is the
// service's own, which serves a per-user, pool-owned or shared credential only
// under a claimed slip and fails closed without one. So a launcher that could
// not get a slip — no gcloud, a service that is not answering, an answer this
// launcher will not carry — starts the run it started before this task existed
// and no more than that. The alternative, failing the launch, would break runs
// that work today over the grant of a credential they may not even use.
func runSlipEnv(ctx context.Context, env *cloudEnv, spec *task.Spec, slug, workflow, serviceURL string) []string {
	names, why := slipCredentialNames(ctx, env, spec)
	if why != "" {
		// No slip rather than a slip that names less than the run will ask
		// for: the names are the set the service serves this run from (R9),
		// so one short of the run's own credentials is a run refused at the
		// call with no way back, where no slip at all is exactly the run this
		// launcher started before the slip existed.
		fmt.Fprintf(launchWarn, "warning: this run starts without a slip, because %s: a slip must name every credential the run will ask for, and one that named fewer would be a call refused with no way back (R9)\n", oneLine(why))
		return nil
	}
	req := credsvc.SlipGrantRequest{
		Slug: slug,
		Run:  spec.RunID,
		// The account the run will call as, derived exactly as the doctor's
		// invoker check and the installation's own root derive it: one account
		// is dedicated to one job per repository (D1937), and the claim is
		// checked against it.
		Account:           gcp.ServiceAccountID(slug, workflow) + "@" + env.lc.GCPProject + ".iam.gserviceaccount.com",
		Credentials:       names,
		MaxRuntimeSeconds: int64(slipRunTime(spec) / time.Second),
	}
	sctx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	ans, err := slipGrant(sctx, serviceURL, req)
	switch {
	case errors.Is(err, errSlipRouteAbsent):
		return nil
	case err != nil:
		fmt.Fprintf(launchWarn, "warning: this run's slip was not granted, so the run starts without one and a credential that only a slip serves is refused at the call: %s\n", oneLine(err.Error()))
		return nil
	}
	if !slipIDUsable(ans.Slip) {
		fmt.Fprintf(launchWarn, "warning: the installation's credentials service answered the grant call with no usable slip id, so this run starts without a slip and a credential that only a slip serves is refused at the call\n")
		return nil
	}
	if ans.MaxRuntimeSeconds < req.MaxRuntimeSeconds {
		fmt.Fprintf(launchWarn, "warning: the slip this run was granted is served for %s and the run may live for %s: the service stops issuing at the slip's end, and a credential the run still needs after it is refused\n",
			(time.Duration(ans.MaxRuntimeSeconds) * time.Second).String(), slipRunTime(spec).String())
	}
	return []string{envSlip + "=" + ans.Slip}
}

// slipCredentialNames are the credentials the run's slip asks the service to
// serve: what each of the four crafts resolves to for this launch, deduplicated
// — the same answer `fugaro craft ls` prints and the same one the runner will
// make its calls with, because the resolution is internal/craft's and not a
// second rule here (that package's own promise: a launcher and a runner cannot
// disagree about what one configuration means).
//
// The layers are the two that can write a craft's access: the document this
// run will check out, and the installation's config, which is where the owner
// creates the credentials and the pools a repository's file only selects among
// (F12, D1628). The project layer is not read because it cannot change this
// answer at all: it carries crafts and only their models, since config.Scopes
// refuses a craft access in a file anyone who can write the runs bucket can
// write.
//
// A craft no layer sets an access for resolves to the default credential, so
// the ordinary cascade repository asks for its own bundle entry and nothing
// else — and a slip that named nothing would be a run refused the one
// credential it has.
//
// The second answer is why this launcher cannot name the run's own set, ""
// when it can: the repository's own document could not be read at the run's ref
// (only a file that exists there is a set of accesses, and this one is read
// exactly as the run reads it — cascadeRefConfig, the ref out of the launcher's
// checkout, which is the document the run clones and resolves its crafts from),
// or it names an access no layer of this installation holds (craft.Resolve's
// own verdict, which its caller here keeps rather than drops). The caller asks
// for no slip then, because the names it would send are the slip's allow-list
// rather than a wish (R9: the service serves this run from exactly them), and a
// name in there that the run may not be served, or one the run will ask for
// that is missing, is a run let into a state it cannot get out of.
//
// launchFileConfig is deliberately not what reads it, though every other launch
// read does: its checkout stands in when the ref cannot be read, and the
// working tree is a document the run never sees — one left on another branch or
// carrying an uncommitted edit names accesses the run will not ask for, which
// is the same broken slip the nil branch above refuses, only silently.
//
// subscriptionSpread is false and that is not a judgement about the switch: it
// decides only whether a pool is reported inert, which is the run's own rule
// about which of a pool's members to skip (D1626, MC5), and it changes no
// access.
func slipCredentialNames(ctx context.Context, env *cloudEnv, spec *task.Spec) ([]string, string) {
	co := cascadeRefConfig(ctx, env, spec)
	if co == nil {
		return nil, "the repository's own fugaro.yaml could not be read at the ref this run will check out — there is no checkout of the repository here, or that ref carries no such file this launcher can resolve — so the credentials the run will ask for cannot be named, and the launcher's own working tree is not the document the run reads; run `fugaro run` from a checkout of the repository with the ref the run will clone to have this run served under a slip"
	}
	layers := []craft.Layer{{Name: craft.SourceRepo, Crafts: co.Agent.Crafts}}
	if env.lc != nil {
		layers = append(layers, craft.Layer{Name: craft.SourceLocal, Crafts: env.lc.Crafts,
			Credentials: env.lc.Credentials, Pools: env.lc.Pools, KeyPrefix: craftLocalKeyPrefix})
	}
	eff, ps := craft.Resolve(layers, false)
	if len(ps) > 0 {
		// The path and not the name an access wrote: that name is what the
		// slip must not carry, so it stays out of the launcher's output the
		// way internal/credsvc's own refusals keep it out of theirs.
		return nil, ps[0].Path + " names a credential or pool no layer of this installation holds, so the credentials the run will ask for cannot be named (`fugaro validate` names every one)"
	}
	names := make([]string, 0, len(config.CraftNames))
	for _, name := range config.CraftNames {
		if access := eff[name].Access; access != "" && !slices.Contains(names, access) {
			names = append(names, access)
		}
	}
	return names, ""
}

// slipRunTime is how long this run asks to be served for: the run's own
// ceiling, which is the execution's own timeout — the task's total plus the
// slack the backend adds to it, so the slip and the execution end together —
// and the platform's maximum where the task names no total, since that is what
// the backend would run it for. Asking for the run's own ceiling rather than a
// guess is what keeps the two from drifting apart: a slip that ends before the
// run does is a run refused at its next call, and the service clamps what it
// will grant to its own constant (R10).
func slipRunTime(spec *task.Spec) time.Duration {
	if d := launchTimeoutOf(spec); d > 0 {
		// The execution this slip is granted for is not the task's own total
		// but that total plus the slack the backend adds to it
		// (backend.LaunchSpec.Timeout), so the task's total alone buys the run
		// a slip that ends while the execution it was granted for is still
		// working. The service clamps what it will grant (R10), so asking for
		// the whole of it is asking for the run's own ceiling and no more.
		return d + backend.TaskTimeoutSlack
	}
	// A task that names no total runs for the job's own timeout, which is a
	// platform read this launcher does not make; the platform's maximum is
	// what it asks for instead, and that is also the most the service will
	// grant, so there is no slack to add here that would buy the run anything.
	return backend.MaxRunTime
}

// slipIDUsable reports whether id is one slip id this launcher will put on a
// job's environment. The id becomes ONE environment entry of the execution, so
// a value that is empty, longer than any id the service draws, or not one
// clean printable token would be an entry the service chose rather than the id
// it granted — and the backend builds the execution's environment out of these
// entries.
func slipIDUsable(id string) bool {
	return id != "" && len(id) <= maxSlipIDBytes && reviewPrintableToken(id)
}

// credentialsServiceURLOf is the address credentialsServiceLaunchEnv put on
// this launch, and whether it put one there at all: the slip is asked for
// exactly where the service's address is, so the two decisions cannot disagree
// about whether this run has a service to ask.
func credentialsServiceURLOf(entries []string) (string, bool) {
	for _, kv := range entries {
		if url, ok := strings.CutPrefix(kv, envCredentialsServiceURL+"="); ok {
			return url, url != ""
		}
	}
	return "", false
}

// releaseClaim drops our own claim after a failed launch, so a retry need
// not wait out claimTTL. It deletes only the exact object we hold.
func releaseClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string) {
	data, gen, err := env.bucket.Read(ctx, s.ClaimKey())
	var c runstore.Claim
	if err != nil || json.Unmarshal(data, &c) != nil || c.Holder != holder {
		return
	}
	_ = env.bucket.DeleteIf(ctx, s.ClaimKey(), gen, data)
}

// errClaimReleased means the claim a loser was waiting on disappeared:
// its holder's launch was refused and released it.
var errClaimReleased = errors.New("launch claim released")

// claimRounds bounds how often launchRun goes back for a released claim.
const claimRounds = 3

// launchWarn is where a launch's warnings go, os.Stderr in production. A
// variable so that a test reads the warning a failed declaration prints
// rather than leaking it into the test's own output.
var launchWarn io.Writer = os.Stderr

// reportIntentTimeout bounds one launch's declaration. It is short on purpose:
// the declaration is advisory, and a database that is slow or unreachable must
// cost the launch a few seconds rather than the hold it is working inside.
const reportIntentTimeout = 15 * time.Second

// takeClaim tries to take the run's launch claim. won is true when we hold
// it; otherwise other is the claim someone else holds, to wait on.
//
// A held claim is read ONCE: staleness is judged on that content and the
// takeover's ReplaceIf matches that read's generation, so nobody can
// replace a claim that turned fresh after we judged it stale. A
// claim that vanished before the read counts as absent.
func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {
	key := s.ClaimKey()
	someone := runstore.Claim{Holder: "another CLI", At: now}
	for attempt := 0; ; attempt++ {
		ok, _, err := s.Claim(ctx, holder, now) // Claim itself retries once if the claim vanishes mid-call
		if err != nil {
			return false, other, claimError(s, key, err)
		}
		if ok {
			return true, other, nil
		}
		hook(launchHooks.beforeRead)
		prev, gen, err := env.bucket.Read(ctx, key)
		if errors.Is(err, blobx.ErrNotExist) {
			if attempt == 0 {
				continue // released between our Claim and this read: absent, so claim once more
			}
			return false, someone, nil // released twice in a row: never loop here
		}
		if err != nil {
			return false, other, claimError(s, key, err)
		}
		var cur runstore.Claim
		if json.Unmarshal(prev, &cur) == nil && now.Sub(cur.At) < claimTTL {
			return false, cur, nil // fresh: someone is launching
		}
		// Stale (or unreadable): its holder presumably died. Take it over.
		hook(launchHooks.beforeTakeover)
		mine, err := json.Marshal(runstore.Claim{Holder: holder, At: now.UTC()})
		if err != nil {
			return false, other, err
		}
		if _, err := env.bucket.ReplaceIf(ctx, key, mine, gen, prev); errors.Is(err, blobx.ErrConflict) {
			return false, someone, nil // someone else changed the stale claim first: they launch
		} else if err != nil {
			return false, other, remote(err)
		}
		return true, other, nil
	}
}

// claimError is a failed read of the launch claim, as a remote error. An
// oversized claim is no claim a CLI wrote, and without its content and
// generation it can't be judged stale or taken over safely: say so.
func claimError(s *runstore.Store, key string, err error) error {
	if errors.Is(err, runstore.ErrTooLarge) || errors.Is(err, blobx.ErrTooLarge) {
		ref := s.Slug() + "/" + s.RunID()
		return remote(fmt.Errorf("the launch claim of %s is not one a CLI wrote (%w); delete %s, then fugaro run --retry %s", ref, err, key, ref))
	}
	return remote(err)
}

// waitForLaunch waits up to claimWait for the claim holder's launch.json
// so a concurrent "fugaro run --run-id X" then reports the winner's
// launch with exit 0, as a repeated launch should. Otherwise exit 1. When
// the claim disappears (its holder was refused and released it), it
// returns errClaimReleased at once, so the caller can claim again.
func waitForLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec, run string, holder runstore.Claim) (*runstore.Launch, error) {
	deadline := time.Now().Add(claimWait)
	for {
		l, err := existingLaunch(ctx, env, s, spec)
		if err != nil {
			return nil, err
		}
		if l != nil {
			return l, nil
		}
		held, err := env.bucket.Exists(ctx, s.ClaimKey())
		if err != nil {
			return nil, remote(err)
		}
		if !held {
			return nil, errClaimReleased
		}
		if !time.Now().Before(deadline) {
			return nil, userErr("a launch of %s by %s (claimed %s) is still in flight after %s; run fugaro runs ls, or fugaro run --retry %s after %s",
				run, holder.Holder, holder.At.UTC().Format(time.RFC3339), claimWait, run, holder.At.Add(claimTTL).UTC().Format(time.RFC3339))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(claimPoll):
		}
	}
}

// claimHolder names this launch attempt: unique per call, even within one process.
func claimHolder() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%d", host, os.Getpid(), rand.Uint64())
}

// noteLine is a recipe's note as a stderr line; the note can carry text
// from the installation's config, so it is escaped like other such text.
func noteLine(note string) string {
	return "note: " + pluginwire.Printable(oneLineCLI(note)) + "\n"
}

// authorGates is the launch's half of the authorship gates, run where
// finishLaunch knows the workflow's kind and checkout mode and before the task
// is written: the git.author key, whatever it says (gitAuthorSince), is judged
// first and refuses the launch, the field this launch would write (authorSince)
// is dropped rather than refused, and only then is the author resolved from
// this machine's local config — for a file that opted in by spelling the key
// launcher.
//
// clone is the workflow's checkout mode, nil where the launch could not read
// it: the key gate then takes the derived-image judge, as checkRigorImage and
// checkGovernanceImage do, and reaches no gate at all when the file itself
// could not be read. The key is judged for every mode — a review or artifact
// run's runner decodes the same file strictly — while the author is recorded
// for neither: neither pushes, so the identity such a task carried would be a
// claim about commits that are never made (D605). That is the two shapes
// budgetBranch reads as "pushes nothing" — a review run carries review_prs
// rather than a mode, and only an artifact recipe writes mode.
//
// Nothing is recorded without the opt-in: git.author is the repository's own
// decision whose name goes into its history (D606), and a repository that did
// not write the key keeps the run's own identity, as every repository does
// today, whether or not the launcher has an identity to offer.
func authorGates(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, clone *bool, warn io.Writer) error {
	// The file is read once and both questions are asked of it here: the key
	// (which images may run it) and the value (whether a run records an
	// author). Each read may run git, so a launch that opted in reads it once,
	// as checkRigorImage resolves its answer once for its own gate.
	cfg := launchFileConfig(ctx, env, spec)
	if gitAuthorSet(cfg) {
		if clone != nil && *clone {
			if err := checkCloneGitAuthorImage(ctx, env, spec, cfg, warn); err != nil {
				return err
			}
		} else if err := checkGitAuthorImage(ctx, env, slug, spec, kind, cfg, warn); err != nil {
			return err
		}
	}
	if !gitAuthorLauncher(cfg) || spec.ReviewPRs != nil || spec.Mode == recipe.ModeArtifact {
		return nil
	}
	author, err := launchAuthor(ctx, env.lc, spec.RequestedBy)
	if err != nil {
		return err
	}
	spec.Author = author
	authorSkew(ctx, env, slug, spec, clone, warn)
	return nil
}

// launchAuthor is the identity a launch records as its commits' author
// (task.Spec.Author, D604): the local config's commit_email when it is set,
// else the launcher's own identity me — the user: key, else git config
// user.email, which is what localcfg.Me answered — with, as the name, the local
// config's commit_name, else git's own user.name on this machine, else the
// address's local part.
//
// nil means the launch records nobody, so the run's commits author and commit
// under the run's own identity, which is what every run does today (D605): me
// is not an address (user: alice, a CI box, a bot account). A commit_email the
// launcher may not claim is the one case answered with a refusal instead of an
// omission (D649): it has to be the address the launcher launches as, or a
// forge noreply address, and anything else is a colleague's address — the one
// thing this rule exists to keep out of a history the forge links to that
// colleague's account.
func launchAuthor(ctx context.Context, lc *localcfg.Config, me string) (*task.Person, error) {
	email := me
	if lc.CommitEmail != "" {
		if lc.CommitEmail != me && !forgeNoreply(lc.CommitEmail) {
			return nil, userErr("the local config's commit_email %s is neither the address you launch as (%s) nor a forge noreply address, so a run must not author its commits with it: one launcher must not attribute commits to somebody else. Set user: to that address to launch as it, or leave commit_email out",
				pluginwire.Printable(lc.CommitEmail), pluginwire.Printable(me))
		}
		email = lc.CommitEmail
	}
	if !isAddress(email) {
		return nil, nil
	}
	name := lc.CommitName
	if name == "" {
		name = gitUserName(ctx)
	}
	if name == "" {
		name = email[:strings.IndexByte(email, '@')]
	}
	author := &task.Person{Name: name, Email: email}
	// The shape Spec.Validate enforces when the runner reads the task back
	// (task.AuthorProblem): the two fields become git's own header values, so a
	// commit_name holding a line break would forge the rest of the header
	// block, and a task the runner refuses empty-handed is worse than a launch
	// that says which key to fix.
	if p := task.AuthorProblem(*author); p != "" {
		return nil, userErr("the commit author this launch would record has a %s; fix the local config's commit_name or commit_email, or this machine's git config user.name, which is what the name falls back to", p)
	}
	return author, nil
}

// isAddress reports whether s is a commit address: the rule task.AuthorProblem
// applies to a Person's email — one @ with a local part and a domain around it
// — and the one D605's "not an email" means, so a launcher whose recorded
// identity is a bare name (user: alice) records no author at all rather than
// one git cannot read.
func isAddress(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at == strings.LastIndexByte(s, '@') && at < len(s)-1
}

// forgeNoreply reports whether email is one of a forge's own noreply addresses
// — GitHub's <id>+<login>@users.noreply.github.com and GitLab's
// <id>-<login>@users.noreply.gitlab.com — which a launcher may set commit_email
// to without claiming somebody else's address (D649): such an address is
// routed to the account that owns it and to no mailbox, which is the whole of
// what a launcher can arrange for itself. Only the domain is judged: the id and
// the login in the local part are the person's own, and nothing before E's
// authenticated identity could verify which login a launcher holds.
func forgeNoreply(email string) bool {
	dom := email[strings.IndexByte(email, '@')+1:]
	return dom == "users.noreply.github.com" || dom == "users.noreply.gitlab.com"
}

// gitUserName is this machine's git config user.name, "" when it is unset or
// git cannot be asked: a commit's display name is a nicety next to its address,
// and the address's local part stands in when git has none (launchAuthor), so a
// missing or slow git costs the author its name and never the launch.
func gitUserName(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "git", "config", "user.name")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
