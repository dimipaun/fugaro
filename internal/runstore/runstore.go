// Package runstore reads and writes a run's objects in the runs bucket (design §3.3).
package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Status is the lifecycle state of a run.
type Status string

const (
	StatusRunning Status = "running"
	// StatusSucceeded is a ready PR, or (outcome none, Record.Gate "clean")
	// a check gate that found nothing to fix, so no PR was ever needed.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a draft PR exists, or (outcome none) review mode
	// refusing a pull request before checkout (no PR of its own ever
	// exists): a policy decision the runner made correctly, not an
	// infrastructure fault.
	StatusFailed     Status = "failed"
	StatusInfraError Status = "infra_error"
	StatusCancelled  Status = "cancelled"
	StatusHalted     Status = "halted" // a budget or token limit stopped the run
)

// HaltReason says which limit halted a run.
type HaltReason string

const (
	HaltKillSwitch         HaltReason = "kill_switch"
	HaltRunCap             HaltReason = "run_cap"
	HaltRepoDailyCap       HaltReason = "repo_daily_cap"
	HaltGlobalDailyCap     HaltReason = "global_daily_cap"
	HaltNoCap              HaltReason = "no_cap"
	HaltTokenCap           HaltReason = "token_cap"
	HaltBudgetUnavailable  HaltReason = "budget_unavailable"
	HaltBudgetTokenExpired HaltReason = "budget_token_expired"
	// HaltNoCredential is a credentials-cascade run that ran out of job: the
	// service answered a typed "exhausted until T" (C1.10) and T is not
	// inside what is left of the job's own timeout, so the run can wait for
	// no credential before it must be over (C1.17). The wait-or-switch
	// decision itself is the service's; this reason only says the reset it
	// named fits in no job like this one.
	HaltNoCredential HaltReason = "no_credential"
	// HaltCredsRulesDenied is a cascade run whose write was refused by the
	// deployed rules: the `credential` or `waiting_until` child of its own
	// registry entry, or a by-tier counter (C1.3). It is the known gap of a
	// rules downgrade — an older Fugaro running `fugaro update --firebase-project`
	// redeployed a document without the cascade's children — so the message
	// names the redeploy and the write is never retried (C1.17): silently
	// dropping it would hide the run's credential from the dashboard, and
	// retrying it forever would fill the log instead of the operator's queue.
	HaltCredsRulesDenied HaltReason = "creds_rules_denied"
)

// Halt is why and when a run was halted.
type Halt struct {
	Reason HaltReason `json:"reason"`
	Scope  string     `json:"scope"` // run | repo | global
	At     time.Time  `json:"at"`
	Detail string     `json:"detail,omitempty"`
}

// Outcome is the state of the run's pull request.
type Outcome string

const (
	OutcomeReady Outcome = "ready"
	OutcomeDraft Outcome = "draft"
	OutcomeNone  Outcome = "none"
	// OutcomeReviewed is a mode: review run (design generic-tool §3.6,
	// G12): it reviewed one or more existing pull requests and commented
	// on each; it never pushes and never opens or edits a pull request of
	// its own.
	OutcomeReviewed Outcome = "reviewed"
)

// PRRef identifies the run's pull request.
type PRRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	// Desc is a digest of the title and description (outside the status
	// section) the runner last wrote, so finalize replaces them from pr.md
	// only while nobody has edited them. Empty for a follow-up's PR and
	// for one the runner opened at finalize.
	Desc string `json:"desc,omitempty"`
	// StatusAt is when the runner last wrote the status section into the
	// description. A running record whose StatusAt is long past is the
	// stale draft of a run that died; diagnose and ls say so.
	StatusAt *time.Time `json:"status_at,omitempty"`
}

// ReviewSummary is one review round's verdict.
type ReviewSummary struct {
	Round int `json:"round"`
	// Tier is TierFirst for a first-line review (by the coder's model,
	// never deciding readiness) and TierSenior for the reviewer's; empty
	// is a senior review in a run that had no first line.
	Tier     string `json:"tier,omitempty"`
	Verdict  string `json:"verdict"`
	Findings int    `json:"findings"`
	// Bounce is which cheap loop this review followed, under review:
	// {bounce: first_line}: 0 for a review before any senior rejection, 1
	// for the cheap loop after the first one, and so on. Always 0 on a
	// senior (tier "" or "senior") review.
	Bounce int `json:"bounce,omitempty"`
}

// Review tiers (ReviewSummary.Tier).
const (
	TierFirst  = "first"
	TierSenior = "senior"
)

// StageTiming records how long one stage took, and what its model calls
// cost (generic-tool plan Task 24). ModelUSD is the gateway's StageReport
// figure for the stage when the run went through the gateway, and the
// result event's total_cost_usd otherwise (an oauth run); Model is the
// model the stage was pinned to (config.Agent.ModelFor). Both are absent
// in records from before 0.8.0, and in a local run that had no gateway and
// reported no cost.
type StageTiming struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
	// Calls is how many model calls the stage made, and Agents how many
	// sub-agent tool calls its own stream carried. Calls is the gateway's
	// stage report, absent without a gateway (an oauth run has no call
	// count); Agents is the relay's count of the sub-agent tool calls the
	// stream named, for every auth mode, gateway or not. The gateway's count
	// of distinct agent IDs is a different figure and is never recorded: it
	// rests on the agent ID a client claims, which nothing verifies. Both are
	// pointers because a stage nobody counted has the field absent, and that
	// is not the same fact as a stage counted at zero.
	Calls  *int `json:"calls,omitempty"`
	Agents *int `json:"agents,omitempty"`
	// ModelUSD is what this stage's calls cost, in USD, whatever model they
	// were on: under the gateway, every call the stage's report carried,
	// the background model's (haiku, say) included. The stages sum to
	// Cost.ModelUSD to the micro-dollar, except for gateway spend that
	// settled after its stage's wait ran out: that is in Cost.ModelUSD and
	// in Cost.ModelBy["unattributed"], in no stage. So, exactly:
	// sum(stages[].model_usd) + Cost.ModelBy["unattributed"] ==
	// Cost.ModelUSD.
	ModelUSD float64 `json:"model_usd,omitempty"`
	// Model is the model the stage was pinned to: the stage role's
	// models.<role>, else agent.model. "" when the config pinned none
	// (Claude Code's own default). It is what the config asked for, not
	// what the upstream served, and a stage may also have called the
	// background model: Model does not account for ModelUSD on its own.
	Model string `json:"model,omitempty"`
	// ModelNotionalUSD is the part of this stage's ModelUSD that was
	// notional: what the calls a subscription paid for would have cost at
	// list rates (D140, P12) -- Claude Code's own figure when the stage ran
	// on oauth, and, in a cascade run, the tier-A calls' share of what the
	// gateway settled. The stage's own split is the only place it exists:
	// one stage of a cascade run can hold both, so Cost.ModelNotionalUSD
	// says how much of the run's figure was notional and this says where.
	ModelNotionalUSD float64 `json:"model_notional_usd,omitempty"`
}

// Switch is one entry of a run's switch log (D140, C1.16): the run's cascade
// moved its calls from one credential to another, and the credentials
// service said why. From, To and Reason are the service's own record
// (credsvc.Switch, C1.15) as the gateway read it off the answer; everything
// here is a name or a word of that contract, never a credential value.
type Switch struct {
	// From is the credential the call was on, empty when the call started on
	// the credential that answered it (the service's own meaning for an
	// empty from), which is why it is not required.
	From string `json:"from,omitempty"`
	// To is the credential that answered.
	To string `json:"to"`
	// Reason is why the cascade moved, one of the service's closed set of
	// reason words (credsvc.ValidReason), never free text.
	Reason string `json:"reason"`
	// CacheMissUSD is the estimated cost of re-reading what the previous
	// call had cached, priced at the answering tier's input rate less its
	// cache-read rate (F24, C1.16). It is an estimate for a reader and it
	// is charged to nothing: the ledger settles the calls themselves.
	CacheMissUSD float64 `json:"cache_miss_usd,omitempty"`
	// CacheMissUnmeasured is true when the answering tier's cache behaviour
	// is not measured (a Claude model on tier C's OpenRouter route, F24,
	// Checks 46 and 50). Then the miss is unknown rather than free, and
	// CacheMissUSD is 0 because there is no figure -- not because it is one.
	CacheMissUnmeasured bool `json:"cache_miss_unmeasured,omitempty"`
}

// Record is result.json, the run record (design §4.6).
type Record struct {
	Version  int    `json:"version"`
	RunID    string `json:"run_id"`
	Repo     string `json:"repo"`
	Workflow string `json:"workflow,omitempty"`
	// OAuthAccount is the named Claude OAuth account this run's agent.auth:
	// oauth workflow used ("" meaning the repository's default account,
	// docs/design/named-oauth-tokens.md); informational only, never read by
	// anything that makes a security decision. Absent in records from
	// before this field existed.
	OAuthAccount string  `json:"oauth_account,omitempty"`
	Execution    string  `json:"execution,omitempty"` // canonical full name, never the short CLOUD_RUN_EXECUTION; compare only via backend.SameExecution
	Status       Status  `json:"status"`
	Stage        string  `json:"stage"`
	Outcome      Outcome `json:"outcome"`
	Reason       string  `json:"reason,omitempty"`
	// Gate is "clean" or "fixed" when the recipe's check step decided the
	// run (gate.go); "" when the recipe has no check step, or it found a
	// failure and the run went on to implement as usual.
	Gate string `json:"gate,omitempty"`
	// Autofix is what the check step's commands.fix did (gate.go), when the
	// recipe's check step has autofix: true; nil otherwise. Never carries
	// the command's own output.
	Autofix *AutofixRecord `json:"autofix,omitempty"`
	Branch  string         `json:"branch,omitempty"`
	// BaseBranch is the base the run's pull request targets, the
	// git.base_branch of the config it ran with (not the task's ref, which
	// a first run may set to another branch). A follow-up takes it as its
	// ref. Absent in records from before it was kept.
	BaseBranch string `json:"base_branch,omitempty"`
	// StartSHA is the commit the run's branch was cut from: the task's ref
	// (--ref, or the base branch) as bootstrap read it, right after the
	// checkout and before the agent ran. It is the base every question of
	// "did this run change X" is measured against — the workflow guard most
	// of all — which is not the base branch when the run was started from
	// another ref. A follow-up records the commit it started from in
	// follow_up.start_sha instead, and a mode: review run has no branch.
	// Absent in records from before it was kept.
	StartSHA string `json:"start_sha,omitempty"`
	HeadSHA  string `json:"head_sha,omitempty"`
	PR       *PRRef `json:"pr,omitempty"`
	// DraftFallback is set when the host refused real draft pull requests,
	// so the PR is an ordinary one marked "[DRAFT]" in its title.
	DraftFallback bool            `json:"draft_fallback,omitempty"`
	Reviews       []ReviewSummary `json:"reviews,omitempty"`
	Verify        []verify.Record `json:"verify,omitempty"`
	CostUSD       float64         `json:"cost_usd"` // the model spend (design §4.6)
	Cost          *Cost           `json:"cost,omitempty"`
	Stages        []StageTiming   `json:"stages,omitempty"`
	// Switches is the run's switch log (D140, C1.16): every credential
	// switch the credentials service reported on one of this run's calls,
	// in the order the gateway settled the answers that carried them, each
	// beside the cache-miss estimate only the gateway can compute (F24). It
	// is the service's record and the estimate, nothing else: no credential
	// value is in it, and nothing reads it to decide anything. Absent for a
	// run that never switched, and in records from before this field
	// existed.
	Switches   []Switch   `json:"switches,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	Deadline   *time.Time `json:"deadline,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// FinalizeReserveS is the workflow's timeouts.finalize_reserve at the
	// task's ref, overrides applied, in seconds; read it with
	// FinalizeReserve. Absent until bootstrap has read fugaro.yaml.
	FinalizeReserveS float64 `json:"finalize_reserve_s,omitempty"`
	// Image is what the image the run started in was built from; absent
	// when the runner could not tell.
	Image *ImageInfo `json:"image,omitempty"`
	// FollowUp is set for a run that continues an existing pull request.
	FollowUp *FollowUp `json:"follow_up,omitempty"`
	// PushedHead is the commit the run pushed, set right after a
	// successful push (every run) and saved at once, so a run killed
	// after posting still records that it updated its pull request.
	PushedHead string `json:"pushed_head,omitempty"`
	// Halt is set when Status is halted.
	Halt *Halt `json:"halt,omitempty"`
	// Policy is the budget and model policy the run ran under, set only
	// when a layer set something (a run with no policy writes none).
	Policy *PolicyRecord `json:"policy,omitempty"`
	// Recipe is the recipe the run's loop came from (docs/design/recipes.md
	// §8); absent in records from before 0.5.0.
	Recipe *RecipeRecord `json:"recipe,omitempty"`
	// ProjectLayer is the project layer the run's task carried
	// (docs/design/layered-config.md §8); absent when it carried none, and
	// in records from before 0.6.0.
	ProjectLayer *ProjectLayerRecord `json:"project_layer,omitempty"`
	// ConfigSHA256 is the sha256 of the resolved fugaro.yaml, before the
	// per-task overrides (config.Config.SHA256); absent before 0.6.0.
	ConfigSHA256 string `json:"config_sha256,omitempty"`
	// Budget is the run's part in the project's shared budget, set only
	// when the run used the budget backend (M9b).
	Budget *BudgetRecord `json:"budget,omitempty"`
	// Reviewed is set for a mode: review run (outcome reviewed): one entry
	// per pull request it reviewed, in the task's review_prs order.
	Reviewed []ReviewedPR `json:"reviewed,omitempty"`
	// Render is set for a mode: artifact run (outcome artifacts, or a
	// failed artifact run whose render ran): what commands.render did. A
	// run refused before its render step (a blank command, a bad recipe)
	// records none.
	Render *RenderRecord `json:"render,omitempty"`
	// Inputs are the files the launcher attached to the run (fugaro run
	// --attach, design artifact-runs.md §4 step 6): the task spec's own
	// list, echoed onto the record once the runner downloaded and verified
	// every one, so a reader can see exactly what the run was given — name,
	// size and sha256. Absent for a run launched without an attachment, and
	// in records from before this field existed.
	Inputs []InputFile `json:"inputs,omitempty"`
	// ChangedFiles is the paths the run changed, recorded by the runner at
	// finalize (console v2 design D27, F17): paths only, bounded, and with
	// the truncation recorded as a fact (changedfiles.go). Absent for a run
	// that recorded none — one that died before finalize, or whose git read
	// failed — and in records from before this field existed, so a reader
	// from before it sees the record it always saw.
	ChangedFiles *ChangedFiles `json:"changed_files,omitempty"`
	// Artifacts is the files a mode: artifact run uploaded, in name order
	// (design artifact-runs.md §3, §8): one entry per object under the
	// run's artifacts/ prefix, each the account a reader verifies what it
	// downloads against. Absent — never an empty array — for every run
	// that delivered none, so a record written before this field existed
	// is byte-for-byte the record an old reader saw.
	Artifacts []ArtifactFile `json:"artifacts,omitempty"`
	// Rigor is the mode the run resolved, the layer that decided it, the
	// areas that matched and the moment of the run each was known at
	// (docs/rigor-modes/implementation-plan.md T1.2, D22, D24, D25). Absent
	// for a run that resolved none — every record written before this field
	// existed, and a run that died before bootstrap — so an old reader sees
	// the record it always saw.
	Rigor *RigorRecord `json:"rigor,omitempty"`
	// Backlog is the ids of the backlog items this run filed for what it
	// left open, in the order it filed them: the `<YYYYMMDD-NN>` names
	// their files under docs/backlog/ are known by (docs/rigor-modes/
	// implementation-plan.md T4.5, D9, D26). Nothing reads it yet — no
	// `Diagnosis` field carries it and `runview.Row`, which the console's
	// record keys are derived from, has no backlog key; the items
	// themselves are on the run's branch and in its pull request, and
	// `fugaro backlog list` is how they are read today. It is written
	// because the record is what survives the run, and the ids are the one
	// part of the filing nothing else on the record names. Absent — never
	// an empty list — for a run that filed none, which is every record
	// written before this field existed.
	Backlog []string `json:"backlog,omitempty"`
	// BacklogClosed is the ids of the backlog items this run closed as the
	// follow-up whose review shipped the round an earlier run on its pull
	// request left open (D31, plan T4.7), in the order it closed them, the
	// same `<YYYYMMDD-NN>` names as Backlog. Nothing reads it yet either; it
	// is written so the record says what the run answered as well as what it
	// left. At most MaxBacklogClosed, and absent — never an empty list — for
	// a run that closed none.
	BacklogClosed []string `json:"backlog_closed,omitempty"`
}

// MaxBacklogClosed bounds Record.BacklogClosed. A run closes only what the
// runs of its own pull request filed for a round, one item a run over a walk
// the runner bounds at twenty runs, so this is room for every close a real
// run makes and the point past which a record is not believed
// (result.schema.json's maxItems).
const MaxBacklogClosed = 64

// ReviewedPR is one pull request a mode: review run reviewed (design
// generic-tool §3.6, G12): the commit it reviewed and the stage's verdict,
// ship or changes, shared by every pull request a pick run reviewed
// together.
type ReviewedPR struct {
	Number  int    `json:"number"`
	SHA     string `json:"sha"`
	Verdict string `json:"verdict"`
}

// BudgetRecord says how the run used the shared budget: the UTC day number
// it started on, what its leases granted and gave back in micro-dollars, the
// effective mode and the backend's kind.
type BudgetRecord struct {
	Day            int64  `json:"day"`
	GrantedMicros  int64  `json:"granted_micros"`
	ReleasedMicros int64  `json:"released_micros"`
	Mode           string `json:"mode"`
	Backend        string `json:"backend"`
}

// PolicyRecord is the effective budget and model policy of a run, merged
// from the owner's ceiling, the default branch's fugaro.yaml and the run's
// own, and what was dropped for being looser.
type PolicyRecord struct {
	Effective PolicyEffective `json:"effective"`
	// Sources maps each key that is set to the layer that set it:
	// "ceiling", "default-branch", "branch", or "policy" for a key the
	// project's published policy named (internal/policy.SourcePolicy —
	// the policy replaces the ceiling key by key, plan task 1.3).
	Sources map[string]string `json:"sources,omitempty"`
	Ignored []PolicyIgnored   `json:"ignored,omitempty"`
}

// PolicyEffective is the merged values. A zero value means unset, except
// AllowedModels: nil is unset (every model is allowed) and an empty
// non-nil list is set and forbids every model, so it is written as [] and
// read back as an empty non-nil slice.
type PolicyEffective struct {
	PerRunUSD       float64       `json:"per_run_usd,omitempty"`
	PerDayUSD       float64       `json:"per_day_usd,omitempty"`
	Mode            string        `json:"mode,omitempty"`
	MaxRunTokens    int64         `json:"max_run_tokens,omitempty"`
	MaxOutputTokens *PolicyOutput `json:"max_output_tokens,omitempty"`
	AllowedModels   []string      `json:"allowed_models"`
}

// PolicyOutput is the per-call output limits by role.
type PolicyOutput struct {
	Coder    int64 `json:"coder,omitempty"`
	Reviewer int64 `json:"reviewer,omitempty"`
}

// MarshalJSON omits allowed_models only when it is nil: an empty list is a
// restriction and must survive the round trip.
func (e PolicyEffective) MarshalJSON() ([]byte, error) {
	type plain PolicyEffective
	aux := struct {
		plain
		AllowedModels *[]string `json:"allowed_models,omitempty"`
	}{plain: plain(e)}
	if e.AllowedModels != nil {
		aux.AllowedModels = &e.AllowedModels
	}
	return json.Marshal(aux)
}

// PolicyIgnored is a value a layer set that was looser than the limits in
// force and was dropped. Value and Effective are text; Source is the layer
// of the effective value and From the layer that asked for Value.
type PolicyIgnored struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Effective string `json:"effective"`
	Source    string `json:"source"`
	From      string `json:"from"`
}

// FollowUp is what a follow-up run acted on (design §4.4).
type FollowUp struct {
	PR          int    `json:"pr"`
	PreviousRun string `json:"previous_run"`
	// StartSHA is the pull request's head the run started from.
	StartSHA string `json:"start_sha,omitempty"`
	// Session is "resumed" or "fresh"; SessionNote says why.
	Session     string `json:"session,omitempty"`
	SessionNote string `json:"session_note,omitempty"`
	// Comments is how many comments reached the agent.
	Comments int `json:"comments"`
	// Authors maps each author's display name to the comments of theirs used.
	Authors map[string]int `json:"authors,omitempty"`
	// UntrustedAuthors are the display names whose comments were dropped
	// because their authors aren't trusted: sorted, at most 20.
	UntrustedAuthors []string `json:"untrusted_authors,omitempty"`
	// UntrustedAuthorCount is how many distinct untrusted authors were
	// dropped, the ones beyond the 20 named included; absent in records
	// from before it was kept.
	UntrustedAuthorCount int `json:"untrusted_author_count,omitempty"`
	// Omitted counts the comments left out, by reason.
	Omitted map[string]int `json:"omitted,omitempty"`
}

// ImageInfo says what the run's image was built from, read at bootstrap
// before the sync moves the checkout.
type ImageInfo struct {
	// BuiltAt is when the image was built; nil when the image doesn't say.
	BuiltAt *time.Time `json:"built_at,omitempty"`
	// BakedCommit is the baked checkout's HEAD when the run started.
	BakedCommit string `json:"baked_commit"`
}

// AutofixRecord is what the check gate's commands.fix did (gate.go's
// autofixOutcome): its exit code and whether the workflow's timeouts.verify
// killed it before it exited on its own. Never its output.
type AutofixRecord struct {
	ExitCode int `json:"exit_code"`
	// TimedOut is whether commands.fix was killed before it exited; its
	// changes are never committed when this is true (gate.go), so
	// ExitCode is then procgroup's own placeholder for a killed process,
	// not a real exit status.
	TimedOut bool `json:"timed_out,omitempty"`
}

// FinalizeReserve is the run's finalize reserve, for cancel's grace floor
// without a checkout. ok is false when the record has none: an older
// record, or a run that failed before bootstrap read its workflow.
func (r *Record) FinalizeReserve() (d time.Duration, ok bool) {
	if r.FinalizeReserveS <= 0 {
		return 0, false
	}
	return time.Duration(r.FinalizeReserveS * float64(time.Second)), true
}

// ErrNotFound means the requested object does not exist.
var ErrNotFound = errors.New("not found")

// ErrAmbiguous means a bare run ID names runs in several repositories.
var ErrAmbiguous = errors.New("ambiguous run ID")

// ErrExists means a create-once object is already there.
var ErrExists = errors.New("already exists")

// ErrTooLarge means an object is larger than its read cap.
var ErrTooLarge = errors.New("object is larger than its read cap")

// Read caps. The job's service account, and so a run's agent, can
// write every object under runs/, and the CLI reads them on the
// operator's machine: a hostile object of gigabytes must fail with
// ErrTooLarge rather than exhaust memory. MaxRecordBytes caps the JSON
// objects (task.json, launch.json, result.json, the launch claim) and
// any other object ReadFile serves; MaxTranscriptBytes caps a stage
// transcript (transcripts/…), which legitimately runs larger.
const (
	MaxRecordBytes     = blobx.MaxReadBytes
	MaxTranscriptBytes = 32 << 20
)

// Store addresses one run's objects: runs/<repo-slug>/<run-id>/...
type Store struct {
	bucket *blob.Bucket
	slug   string
	runID  string
	prefix string
}

// Open returns the store for one run.
func Open(b *blob.Bucket, repoSlug, runID string) *Store {
	return &Store{bucket: b, slug: repoSlug, runID: runID, prefix: path.Join("runs", repoSlug, runID) + "/"}
}

// Sibling returns the store for another run of the same repository, in
// the same bucket. The caller checks runID's form first: it becomes part
// of an object name.
func (s *Store) Sibling(runID string) *Store { return Open(s.bucket, s.slug, runID) }

// RunID is the run ID this store was opened with.
func (s *Store) RunID() string { return s.runID }

// Slug is the repository slug this store was opened with. The runner keys
// the branch lock and caches on it, so they always agree with the run's
// own prefix, however the slug was obtained (FUGARO_RUN or --task-file).
func (s *Store) Slug() string { return s.slug }

// runRefRE takes a slug in task.Slug's alphabet, [a-z0-9-]. With no "."
// allowed, a slug can't be "." or "..", which path.Join would resolve
// outside runs/.
var runRefRE = regexp.MustCompile(`^([a-z0-9-]+)/([0-9]{8}-[0-9]{6}-[0-9a-f]{4})$`)

// ParseRef splits "<repo-slug>/<run-id>".
func ParseRef(ref string) (slug, runID string, err error) {
	m := runRefRE.FindStringSubmatch(ref)
	if m == nil {
		return "", "", fmt.Errorf("run %q must look like <repo-slug>/<run-id>", ref)
	}
	return m[1], m[2], nil
}

// Prefix is the object-name prefix of this run, ending in "/".
func (s *Store) Prefix() string { return s.prefix }

// PutFile writes a file under the run's prefix. Routed through blobx.Put so
// a GCS 403 is blobx.ErrForbidden, not gocloud's unclassified NotFound
// (docs/design/bucket-iam.md §2.3).
func (s *Store) PutFile(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + strings.TrimPrefix(name, "/")
	if err := blobx.Wrap(s.bucket).Put(ctx, key, data, contentType); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// PutFileFrom writes the file at path under the run's prefix, streaming it
// (blobx.PutFile), and returns the size and the lower-case hex sha256 of the
// bytes that reached the bucket.
//
// want is the sha256 the caller already took of the same bytes, or "" for no
// check. It is an argument and not something the caller checks afterwards
// because of what a mismatch means here: the file was hashed in the staging
// directory and uploaded from it, and on Cloud Run the agent that produced it
// runs as the same user in the same container, so bytes that changed in
// between are bytes no record of this run may attest to. A mismatch deletes
// the object this call just wrote — it is this call's own key, written a
// moment ago — and fails, so a run that fails here leaves nothing in the
// bucket for its record to be wrong about.
//
// The write is create-only (blobx.PutFile's ifNotExist): a run's artifacts
// are written once, and a second execution of the same run must not overwrite
// what the first one recorded.
func (s *Store) PutFileFrom(ctx context.Context, name, path, contentType, want string) (int64, string, error) {
	key := s.prefix + strings.TrimPrefix(name, "/")
	// The file was staged by this run a moment ago, so a symlink or a device
	// at path is not the staged artifact: it is something that got between the
	// two steps. This names that in the record's own words; what makes the
	// upload itself safe is blobx.PutFile's own guards — the open
	// (O_NOFOLLOW|O_NONBLOCK), the fstat on the fd it opened that refuses
	// anything which is not a regular file before a byte is read, and the copy
	// bounded by that size and by the context — which is why this check may be
	// a check and not the guard.
	if st, err := os.Lstat(path); err != nil {
		return 0, "", fmt.Errorf("reading the staged artifact %s: %w", path, err)
	} else if !st.Mode().IsRegular() {
		return 0, "", fmt.Errorf("the staged artifact %s is not a regular file (%s): %w (it was hashed as a regular file of %d bytes)",
			path, st.Mode(), blobx.ErrChanged, st.Size())
	}
	b := &blobx.Bucket{Bucket: s.bucket}
	size, sum, err := b.PutFile(ctx, key, path, contentType, true)
	if err != nil {
		return 0, "", fmt.Errorf("writing %s: %w", key, err)
	}
	if want != "" && sum != want {
		// The delete is what keeps the bucket free of an object no record
		// accounts for, and its failure is reported with the refusal rather
		// than dropped: the run's reason is where a reader learns what is in
		// the bucket. deleteDetached is what makes it happen at all — the
		// caller's context is usually already cancelled or nearly out of time
		// when an upload goes wrong, which is exactly when the cleanup has to
		// work.
		err := fmt.Errorf("%s: %w (this run staged %s and uploaded %s): the staged file changed before the upload", key, blobx.ErrMismatch, want, sum)
		if derr := deleteDetached(ctx, s.bucket, key); derr != nil {
			return 0, "", errors.Join(err, fmt.Errorf("removing the object this attempt wrote: %w", derr))
		}
		return 0, "", err
	}
	return size, sum, nil
}

// DeleteCleanupTimeout is the budget a cleanup delete gets when it runs
// detached from its caller's context (deleteDetached): long enough for one
// object, short enough that a run being finalised is not held past its
// deadline by a bucket that is not answering.
const DeleteCleanupTimeout = 30 * time.Second

// deleteDetached removes key on a context of its own, so a delete that is
// cleanup after a failure still happens when the caller's context has already
// been cancelled — the reason the failure being cleaned up after happened.
func deleteDetached(ctx context.Context, b *blob.Bucket, key string) error {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DeleteCleanupTimeout)
	defer cancel()
	return b.Delete(dctx, key)
}

// DeleteFile removes one of the run's own objects. It is what a failed upload
// step cleans up after itself with: the writes are create-only, so an object
// left behind by an attempt whose record says it uploaded nothing is both an
// artifact no record accounts for and a key the next execution of the same run
// would fail on. The delete is detached from ctx for the same reason
// PutFileFrom's is.
func (s *Store) DeleteFile(ctx context.Context, name string) error {
	key := s.prefix + strings.TrimPrefix(name, "/")
	if err := deleteDetached(ctx, s.bucket, key); err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	return nil
}

// read reads one of the run's objects, refusing one larger than max.
func (s *Store) read(ctx context.Context, name string, max int64) ([]byte, error) {
	key := s.prefix + name
	r, err := s.bucket.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	defer r.Close()
	if r.Size() > max {
		return nil, fmt.Errorf("%s: %w (%d bytes, the cap is %d)", key, ErrTooLarge, r.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w (the cap is %d bytes)", key, ErrTooLarge, max)
	}
	return data, nil
}

// readRecordObject reads one of the run's JSON objects under MaxRecordBytes.
func (s *Store) readRecordObject(ctx context.Context, name string) ([]byte, error) {
	return s.read(ctx, name, MaxRecordBytes)
}

// ReadFile reads one of the run's objects, such as
// transcripts/review-1.jsonl: up to MaxTranscriptBytes under transcripts/,
// up to MaxRecordBytes for anything else (ErrTooLarge past it).
func (s *Store) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if strings.HasPrefix(name, "transcripts/") {
		return s.read(ctx, name, MaxTranscriptBytes)
	}
	return s.readRecordObject(ctx, name)
}

// create writes name only if it does not exist yet. Routed through
// blobx.Create so a GCS 403 is blobx.ErrForbidden (bucket-iam.md §2.3).
func (s *Store) create(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + name
	_, err := blobx.Wrap(s.bucket).Create(ctx, key, data, contentType)
	if errors.Is(err, blobx.ErrExists) {
		return fmt.Errorf("%s: %w", key, ErrExists)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// CreateRecord stores result.json only if none exists yet (ErrExists). On
// Cloud Run the runner writes its first record this way, so a duplicate
// execution of the same run never writes one (design §4.7).
func (s *Store) CreateRecord(ctx context.Context, r *Record) error {
	data, err := encodeRecord(r)
	if err != nil {
		return err
	}
	return s.create(ctx, "result.json", data, "application/json")
}

// CreateTask stores task.json unless it already exists (ErrExists), so two
// launches with one run ID cannot both write a task (design §4.7).
func (s *Store) CreateTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.create(ctx, "task.json", data, "application/json")
}

// WriteTask stores task.json.
func (s *Store) WriteTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "task.json", data, "application/json")
}

// ReadTask loads and validates task.json.
func (s *Store) ReadTask(ctx context.Context) (*task.Spec, error) {
	data, err := s.readRecordObject(ctx, "task.json")
	if err != nil {
		return nil, err
	}
	return task.Parse(data)
}

// WriteRecord stores result.json.
func (s *Store) WriteRecord(ctx context.Context, r *Record) error {
	data, err := encodeRecord(r)
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "result.json", data, "application/json")
}

// ReadRecord loads result.json.
func (s *Store) ReadRecord(ctx context.Context) (*Record, error) {
	data, err := s.readRecordObject(ctx, "result.json")
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decoding result.json: %w", err)
	}
	return &r, nil
}

// RequestCancel asks the running runner to stop and finalize.
func (s *Store) RequestCancel(ctx context.Context) error {
	return s.PutFile(ctx, "cancel", []byte(time.Now().UTC().Format(time.RFC3339)), "text/plain")
}

// CancelRequested reports whether a cancel marker exists.
func (s *Store) CancelRequested(ctx context.Context) (bool, error) {
	return s.bucket.Exists(ctx, s.prefix+"cancel")
}

// RecipeRecord names a run's recipe, where it came from and its exact text's
// sha256, so a run can be traced to the text it ran.
type RecipeRecord struct {
	Name   string `json:"name"`
	Source string `json:"source"` // repo | project | catalog
	SHA256 string `json:"sha256"`
}

// ProjectLayerRecord traces a run to the exact project layer it was given.
type ProjectLayerRecord struct {
	SHA256     string `json:"sha256"`
	Generation int64  `json:"generation,omitempty"`
	// Applied is false when fugaro.yaml at the ref did not name the layer's
	// project and gcp_project (a launch from outside a checkout), so the
	// run resolved without it.
	Applied bool `json:"applied"`
}
