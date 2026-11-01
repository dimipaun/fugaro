// Package config loads and validates a repository's fugaro.yaml (design §5.1).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// Config is a parsed fugaro.yaml.
type Config struct {
	Version int `yaml:"version"`
	// Project is the Fugaro project this repository belongs to
	// (ProjectNameRE). Validate requires it; the runner refuses a job of
	// another project.
	Project string `yaml:"project,omitempty"`
	// GCPProject is the GCP project of the installation this repository
	// belongs to (GCPProjectRE); optional. Never derived from Project.
	GCPProject string `yaml:"gcp_project,omitempty"`
	// Profile names the project layer's profile of the implicit workflow
	// (ImplicitWorkflow), for a file with no workflows:. "" is the
	// project's default_profile. Binaries before 0.6.0 refuse the key.
	Profile string `yaml:"profile,omitempty"`
	Git     Git    `yaml:"git"`
	Agent   Agent  `yaml:"agent"`
	// Budget is the repository's cost and model policy. It can only
	// tighten the owner's ceiling (the project config); see Policy.
	Budget    *Budget             `yaml:"budget,omitempty"`
	Workflows map[string]Workflow `yaml:"workflows"`
	// Governance is the repository's stage budgets, review ratio and fan-out
	// cap (docs/task-governance/spec.md §2, §5). A file that writes no
	// governance: block sets none: Fugaro's own defaults are the ceiling a
	// run starts under, and a repository may only tighten them (D331). Like
	// Rigor the pointer is what keeps an absent block out of the resolved
	// config's hash, which SHA256 takes over this struct.
	Governance *Governance `yaml:"governance,omitempty" json:"governance,omitempty"`
	// AB is the repository's A/B opt-out (docs/model-ab-testing, D556): a
	// file that writes no ab: block sets nothing, and one that writes it may
	// carry ab.rate: 0 alone — the block is otherwise the project layer's,
	// because a repository author could name a challenger model and send
	// that repository's code to a provider the owner opened only to others
	// (D556, F18). Validate is where a file that writes more is refused. The
	// pointer is what keeps an absent block out of the resolved config's
	// hash, as Governance's does.
	AB       *AB      `yaml:"ab,omitempty" json:"ab,omitempty"`
	Followup Followup `yaml:"followup"`
	// Rigor is the repository's rigor mode and its named area rules
	// (docs/rigor-modes/spec.md, reconciliation D20): how much cost a run
	// accepts to reduce risk. A file that writes no rigor: block sets none —
	// this is a default, not a floor, and a project layer's
	// defaults.rigor.default is what a repository that writes none takes.
	// The json tag is what keeps an absent block out of the resolved
	// config's hash: SHA256 marshals this struct (resolve.go), and a
	// pointer with no json name would write a null key into every file
	// that writes none, moving the hash of a file that did not change.
	Rigor *Rigor `yaml:"rigor,omitempty" json:"rigor,omitempty"`

	// Layer is the project layer Resolve resolved this config over, nil
	// for none. It is never part of the file or of SHA256: a consumer that
	// holds a resolved config (an image build) passes it on.
	Layer *ProjectLayer `yaml:"-" json:"-"`
	// RepoRigor is the rigor: block the repository's own file wrote, before
	// the project layer's defaults.rigor merged over it, nil when the file
	// wrote none or wrote an explicit null (`rigor:`, which decodes to a nil
	// block). Like Layer it is never part of the file or of SHA256, and
	// it exists because the project layer's area rules stay in effect beside
	// the repository's rather than being replaced by them (D20c: the two
	// lists are a union), which RigorConfig reads.
	RepoRigor *Rigor `yaml:"-" json:"-"`
	// RepoRigorSet is whether the repository's own file wrote the rigor: key
	// at all, at any value: RepoRigor cannot answer for an explicit null, and
	// the two questions are different ones — what the block says (RepoRigor,
	// which RigorConfig reads) and whether the key is there (this, which the
	// image-skew gates read, because a runner older than rigorSince decodes
	// the file with KnownFields and refuses it on the key alone). Like
	// RepoRigor it is never part of the file or of SHA256.
	RepoRigorSet bool `yaml:"-" json:"-"`
	// RepoGovernanceSet is whether the repository's own file wrote the
	// governance: key at all, at any value, the question RepoRigorSet answers
	// for rigor: Governance cannot answer for an explicit null (`governance:`,
	// which decodes to a nil block), and a runner older than governanceSince
	// refuses the key alone. Like the fields above it is never part of the
	// file or of SHA256.
	RepoGovernanceSet bool `yaml:"-" json:"-"`
	// RepoGitAuthorSet is whether the repository's own file wrote the
	// git.author key at all, at any value: the question RepoRigorSet answers
	// for rigor:, and Git.Author cannot answer it — the key written empty
	// (`git.author:`) decodes to the same GitAuthorFugaro as the key being
	// absent, and the key written `fugaro` decodes to the same value as that
	// key absent too, while a runner older than gitAuthorSince refuses the key
	// itself. Like the fields above it is never part of the file or of
	// SHA256.
	RepoGitAuthorSet bool `yaml:"-" json:"-"`
}

// Rigor is fugaro.yaml's rigor: block and the project layer's defaults.rigor
// (D20): the mode a repository runs at when nothing narrower decides, and
// the named area rules that decide it from the paths a run touches. Both
// keys may be set in either layer (docs/project-layer.md §5).
type Rigor struct {
	// Default is the mode this layer sets; "" sets none, so a layer below
	// decides (the built-in default is rigor.Default, D19). A narrower layer
	// may set a *lower* mode than a wider one (D21): this is a default, and a
	// project that wants a floor says so with a floor.
	Default rigor.Mode `yaml:"default,omitempty"`
	// Areas are this layer's area rules (D20c). A rule sets the mode for the
	// paths it matches; the repository's list and the project layer's are
	// both in effect, never one replacing the other (the merge rule §4
	// otherwise gives a list), which is RepoRigor's reason to exist.
	Areas []rigor.Area `yaml:"areas,omitempty"`
}

// RigorConfig is the rigor configuration a resolved config stands for: the
// project layer's defaults.rigor and the repository's own rigor: block, kept
// apart because both area lists are in effect at once (D20c). It is what
// internal/rigor's one resolver reads, and it is built here rather than in
// the resolver so that package never has to know about this one.
func (c *Config) RigorConfig() rigor.Config {
	out := c.ProjectRigor()
	if r := c.RepoRigor; r != nil {
		out.Repo, out.RepoAreas, out.RepoBlock = r.Default, r.Areas, true
	}
	return out
}

// ProjectRigor is the project layer's half alone: defaults.rigor.default and
// its areas, with the repository's rigor: block left out. It is what a run
// falls back to when the base branch's own fugaro.yaml cannot be read
// (internal/runner, D25): the layer came with the task and is no branch's, so
// it is the one part of the configuration that is still true when no branch
// could be asked, and falling back to the checked-out ref's own value instead
// would let the branch under review set its own mode.
func (c *Config) ProjectRigor() rigor.Config {
	var out rigor.Config
	if c.Layer != nil {
		out.Project, out.ProjectAreas = c.Layer.Defaults.Rigor.Default, c.Layer.Defaults.Rigor.Areas
	}
	return out
}

// Budget modes, as the project config spells them.
const (
	BudgetOff     = policy.ModeOff
	BudgetObserve = policy.ModeObserve
	BudgetEnforce = policy.ModeEnforce
)

// Budget is fugaro.yaml's budget: block. Every key is optional; a key left
// out sets no policy. The model prices are not settable here (a repository
// that could set prices could set them to zero).
type Budget struct {
	Mode      string  `yaml:"mode"`        // off | observe | enforce; "" sets none
	PerRunUSD float64 `yaml:"per_run_usd"` // 0 sets none; enforce needs a cap only after the merge
	// AllowedModels bounds the models the run may choose, explicit IDs
	// only; nil sets none, and an empty list is invalid.
	AllowedModels []string `yaml:"allowed_models"`
	// PerDayUSD is the repository's cap per UTC day, in dollars; 0 sets
	// none. The runner enforces it client-side against the repository's day
	// counter; the owner's database cap is separate and lower values win.
	PerDayUSD float64 `yaml:"per_day_usd"`
}

// Governance is fugaro.yaml's governance: block (docs/task-governance/spec.md
// §2, §5): what a run may spend on one stage, how much review it accepts, and
// how far one stage may fan out. Every key is optional, and every one of them
// is a bound whose smaller value is the stricter one, which is what makes the
// merge a single rule (internal/policy).
//
// One key is enforced today: the runner bounds a stage by stage_timeout and
// stops the run with a checkpoint report when the stage runs out (T7.6). The
// rest are what the gateway reads in a later task (T7.2), and until then a
// file that writes one of them says what its runs will be held to. A key left
// out, written as null, or written as 0 takes Fugaro's default — see
// ee/governance's Defaults, which is the one place they are decided (a build
// that links no ee/ features ships none of them, D522).
//
// The stages are the run's three spend stages. An artifact run's plan and
// render steps have no governance budget, so neither sub-block has a key for
// them: a file that writes one is refused as an unknown key rather than
// accepted and ignored.
type Governance struct {
	// ReviewRatio is N, the ratio guardrail: review calls above N x the
	// implement stage's calls stop the stage. ReviewFloorCalls is the count
	// below which the ratio does not apply, so a short implementation is
	// still reviewed.
	ReviewRatio      int64 `yaml:"review_ratio"`
	ReviewFloorCalls int64 `yaml:"review_floor_calls"`
	// MaxSubagents is the most distinct sub-agents one stage may start.
	MaxSubagents int64 `yaml:"max_subagents"`
	// StageBudget, StageTokens and StageTimeout bound one stage each.
	StageBudget  StageBudget  `yaml:"stage_budget"`
	StageTokens  StageTokens  `yaml:"stage_tokens"`
	StageTimeout StageTimeout `yaml:"stage_timeout"`
	// Tiers are what a run of each tier is expected to cost and take, and how
	// far one of its stages may fan out (G§3, D330). A tier is an expectation
	// and never a cap: the deployed per-run cap stays the ceiling, and an
	// expected_usd above it is an expectation the run still stops at the cap
	// for (G6).
	Tiers GovernanceTiers `yaml:"tiers"`
}

// GovernanceTiers is governance.tiers: the expectations of the four tiers of
// the vocabulary (internal/tier). The tiers are named one by one rather than
// held in a map, so a tier name outside the vocabulary is an unknown key the
// strict decoder refuses rather than a map entry nothing checks.
type GovernanceTiers struct {
	Trivial TierLimits `yaml:"trivial"`
	Small   TierLimits `yaml:"small"`
	Medium  TierLimits `yaml:"medium"`
	Large   TierLimits `yaml:"large"`
}

// TierLimits is one tier's block. Every key is optional; a key left out,
// written as null or written as 0 takes Fugaro's default for that tier
// (ee/governance's Defaults). None of the three is a cap: the per-run cap
// stays the ceiling whatever a tier says (G6).
type TierLimits struct {
	// ExpectedUSD is what a run of this tier is expected to cost; it is an
	// expectation, not a ceiling, so a value above the deployed per-run cap is
	// read and the run still stops at the cap.
	ExpectedUSD float64 `yaml:"expected_usd"`
	// ExpectedMinutes is what a run of this tier is expected to take.
	ExpectedMinutes int64 `yaml:"expected_minutes"`
	// MaxSubagents is the most distinct sub-agents one stage of a run of this
	// tier may start, concurrently or in total.
	MaxSubagents int64 `yaml:"max_subagents"`
}

// Policy is one tier's block as the merge reads it.
func (t TierLimits) Policy() policy.TierExpectation {
	return policy.TierExpectation{
		ExpectedUSD:     t.ExpectedUSD,
		ExpectedMinutes: t.ExpectedMinutes,
		MaxSubagents:    t.MaxSubagents,
	}
}

// Policy is the block as the merge reads it.
func (t GovernanceTiers) Policy() policy.TierExpectations {
	return policy.TierExpectations{
		Trivial: t.Trivial.Policy(),
		Small:   t.Small.Policy(),
		Medium:  t.Medium.Policy(),
		Large:   t.Large.Policy(),
	}
}

// StageBudget is a stage's share of the run's budget, as a fraction: of the
// per-run cap for Implement and Review (review counts the first-line and the
// senior review together), and of the implement stage's own budget for Fix
// (G§2, "a fraction of implementation budget"). A fraction of 1 or more is
// refused: the per-run cap stays the ceiling whatever a stage is given (G6).
type StageBudget struct {
	Implement float64 `yaml:"implement"`
	Review    float64 `yaml:"review"`
	Fix       float64 `yaml:"fix"`
}

// StageTokens is a stage's token ceiling, in tokens.
type StageTokens struct {
	Implement int64 `yaml:"implement"`
	Review    int64 `yaml:"review"`
	Fix       int64 `yaml:"fix"`
}

// StageTimeout is a stage's own wall-clock ceiling. The run's own
// timeouts.stage still bounds it (T7.6).
type StageTimeout struct {
	Implement Duration `yaml:"implement"`
	Review    Duration `yaml:"review"`
	Fix       Duration `yaml:"fix"`
}

// Policy is the block as the merge reads it. A nil receiver (a file that
// writes no governance: block) is the zero policy.Governance, which sets no
// limit and so leaves the ceiling alone.
func (g *Governance) Policy() policy.Governance {
	if g == nil {
		return policy.Governance{}
	}
	return policy.Governance{
		ReviewRatio:      g.ReviewRatio,
		ReviewFloorCalls: g.ReviewFloorCalls,
		MaxSubagents:     g.MaxSubagents,
		StageBudget: policy.StageBudget{
			Implement: g.StageBudget.Implement, Review: g.StageBudget.Review, Fix: g.StageBudget.Fix},
		StageTokens: policy.StageTokens{
			Implement: g.StageTokens.Implement, Review: g.StageTokens.Review, Fix: g.StageTokens.Fix},
		StageTimeout: policy.StageTimeout{
			Implement: g.StageTimeout.Implement.Duration, Review: g.StageTimeout.Review.Duration,
			Fix: g.StageTimeout.Fix.Duration},
		Tiers: g.Tiers.Policy(),
	}
}

// Followup says who can steer a follow-up run on a Fugaro pull request
// (design §4.4, §6.1). A follow-up agent runs with the workflow's secrets
// and the model credential, so only the listed accounts' PR comments reach
// it. The zero value trusts no comment and refuses public repositories.
type Followup struct {
	// Trusted are the account IDs whose PR comments a follow-up acts on:
	// on GitHub the numeric user ID, on Bitbucket the account_id. Both
	// survive a rename, unlike a login or a nickname.
	Trusted []string `yaml:"trusted"`
	// AllowPublic lets a follow-up run on a public repository, where
	// anyone can comment.
	AllowPublic bool `yaml:"allow_public"`
}

// GitAuthorFugaro and GitAuthorLauncher are the two values git.author may
// take: "fugaro" is a run's own identity authoring its commits, and
// "launcher" is the person who launched it (D604, D606). "" is neither of
// them and means the same as GitAuthorFugaro — the key absent, which is what
// every file written before the key said.
const (
	GitAuthorFugaro   = "fugaro"
	GitAuthorLauncher = "launcher"
)

// GitAuthors is git.author's closed vocabulary, the sentence a misspelling
// reads (Validate) and the list schemas/fugaro.schema.json's enum mirrors.
// The empty value is not in it: a file that spells it out is the key absent,
// and is accepted as such.
var GitAuthors = []string{GitAuthorFugaro, GitAuthorLauncher}

// Git describes the repository's git provider and pull request settings.
type Git struct {
	Provider   string     `yaml:"provider"`
	BaseBranch string     `yaml:"base_branch"`
	PR         PRSettings `yaml:"pr"`
	// Author is who a run's commits are authored by: GitAuthorLauncher, the
	// person who launched it as a stated claim (D604), or GitAuthorFugaro,
	// the run's own identity — the same as the key being absent, which is
	// what every repository has today (D606). It is the repository's own
	// opt-in and no other layer sets it: Scopes gives it InRepo alone, so a
	// project layer or a profile that writes it is refused for being out of
	// scope, and a repository decides for itself whose name goes into its
	// history.
	//
	// It selects a behaviour and mints no identity: the launcher fills the
	// address from its own local identity and writes it into the task
	// (internal/task's Spec.Author), which is what a fugaro at the release
	// that reads this key does with it (fugaro.yaml's git.author is gated on
	// gitAuthorSince, and the field on authorSince, in internal/cli).
	//
	// The json tag is what keeps an absent key out of the resolved config's
	// hash, as Rigor's and Governance's are: SHA256 marshals this struct
	// (resolve.go), so a string with no json name would write author: "" into
	// every repository's config and move the hash — and so the derived image —
	// of a file that did not change.
	Author string `yaml:"author,omitempty" json:"author,omitempty"`
}

// PRSettings are applied to every pull request Fugaro opens.
//
// Labels and Reviewers are applied only when a pull request is made ready:
// a draft carries neither (design §4.2a).
type PRSettings struct {
	Labels    []string `yaml:"labels"`
	Reviewers []string `yaml:"reviewers"`
	// EarlyDraft opens the draft pull request at the first checkpoint push
	// (or, with checkpoints off, the first verified stage end); false opens
	// it only at finalize. Unset means true (applyDefaults).
	EarlyDraft *bool `yaml:"early_draft"`
	// Checkpoints pushes a first run's new commits to its branch while a
	// stage runs, fast-forward only, so a container that dies keeps its
	// committed work. It is commit-triggered: a push within about a minute
	// of a commit (poll about 10 s, quiet 5 s, at most one push a minute),
	// at once at stage boundaries, with a 3-minute retry after a failure.
	// Independent of early_draft. Unset means true (see CheckpointsOn).
	Checkpoints *bool `yaml:"checkpoints"`
}

// EarlyDraftOn reports whether the draft pull request opens at the first
// checkpoint push (or, with checkpoints off, the first verified stage end)
// rather than at finalize: true unless git.pr.early_draft is false.
func (p PRSettings) EarlyDraftOn() bool { return p.EarlyDraft == nil || *p.EarlyDraft }

// CheckpointsOn reports whether a first run pushes its new commits while a
// stage runs: true unless git.pr.checkpoints is false.
func (p PRSettings) CheckpointsOn() bool { return p.Checkpoints == nil || *p.Checkpoints }

// Agent configures the Claude Code agent.
type Agent struct {
	// Auth is where the run's model calls are authenticated: vertex (the
	// default), api-key, oauth, or cascade — which asks for the project's
	// credentials cascade and is enabled only by the project policy's switch
	// (docs/credentials-service, D133, O2; config.AgentAuths is the list, and
	// CheckCascadeGateway is the budget it needs).
	Auth         string  `yaml:"auth"`
	Model        string  `yaml:"model"`
	ReviewRounds int     `yaml:"review_rounds"`
	MaxBudgetUSD float64 `yaml:"max_budget_usd"`
	Instructions string  `yaml:"instructions"`
	Review       string  `yaml:"review"`
	// Models picks a model per stage role; a role left out uses Model.
	Models ModelRoles `yaml:"models"`
	// MaxOutputTokens limits one call's output per role; 0 is no limit.
	MaxOutputTokens RoleTokens `yaml:"max_output_tokens"`
	// MaxRunTokens limits a whole run's tokens; 0 is none. It can only
	// tighten the project's ceiling and the default branch's value.
	MaxRunTokens int64 `yaml:"max_run_tokens"`
	// FirstLineReview is whether a review by the coder's own model comes
	// before the senior review: auto (on only when a provider serves the
	// coder and not the reviewer), on or off.
	FirstLineReview string `yaml:"first_line_review"`
	// FirstLineRounds bounds the first-line review/fix cycles, apart from
	// ReviewRounds.
	FirstLineRounds int `yaml:"first_line_rounds"`
	// Recipe names the task loop that follows implement
	// (docs/design/recipes.md): a recipe in .fugaro/recipes/, the project's,
	// or the catalog's. "" is default. fugaro run --recipe wins over it.
	// Binaries before 0.5.0 refuse the key.
	Recipe string `yaml:"recipe"`
	// OAuthToken names the Claude account (claude_accounts in the local
	// config, ClaudeAccountNameRE) this workflow's agent.auth: oauth uses;
	// "" is the repository's default account, the existing claude-oauth-token
	// secret (docs/design/named-oauth-tokens.md). fugaro run --token wins
	// over it. A name must still be allowed for this repository
	// (AllowedClaudeAccounts), checked at launch, not here: Validate checks
	// the name's shape, and refuses the reserved name. Repo and override scope
	// only (Scopes): neither the project layer nor a profile may set it
	// (design §3 T2). A binary before 0.6.5 refuses the key (strict decoder).
	OAuthToken string `yaml:"oauth_token,omitempty" json:"OAuthToken,omitempty"`
	// Compression names the context-compression proxy this workflow's model
	// calls route through: "" (off, the default) or "headroom"
	// (docs/design/headroom-compression.md). Reserved and refused (the plan's
	// P11): Validate knows the key and refuses every value with "not yet
	// implemented" rather than leaving a repository that tries it with a
	// strict-parse unknown-field error, so no run ever has the key set and
	// nothing reads this field yet. The lift is a later change that admits
	// "headroom" here and in schemas/fugaro.schema.json together. Once
	// implemented it is also refused together with agent.auth: oauth or
	// vertex — an ordinary Validate rule (P9), since Resolve calls Validate on
	// the merged config, not on one raw file. The budget-gateway requirement
	// cannot live here, because no fugaro.yaml resolution knows the
	// installation's ceiling; the runner's bootstrap checks that instead.
	// Repo and override scope only (Scopes), for the reason agent.oauth_token
	// is: the project layer and a profile are the same launcher-writable
	// bucket object. Binaries before this key's release refuse it outright.
	Compression string `yaml:"compression,omitempty" json:"Compression,omitempty"`
}

// First-line review settings (Agent.FirstLineReview).
const (
	FirstLineAuto = "auto"
	FirstLineOn   = "on"
	FirstLineOff  = "off"
)

// MaxFirstLineRounds is the most first-line review rounds a run may have.
const MaxFirstLineRounds = 3

// FirstLineOn reports whether the run has a first-line review: on, or auto
// with a coder a provider serves and a reviewer none does (an Anthropic
// coder gets none: same family, little to gain, a real cost).
func (a Agent) FirstLineOn(providers map[string]ModelProvider) bool {
	switch a.FirstLineReview {
	case FirstLineOn:
		return true
	case FirstLineOff:
		return false
	}
	return claimed(providers, a.ModelFor(RoleCoder)) && !claimed(providers, a.ModelFor(RoleReviewer))
}

// ModelRoles are the models of the stage roles.
type ModelRoles struct {
	Coder      string `yaml:"coder"`      // implement and fix
	Reviewer   string `yaml:"reviewer"`   // review
	Background string `yaml:"background"` // Claude Code's small background requests
}

// RoleTokens is a per-call output limit for each role.
type RoleTokens struct {
	Coder    int64 `yaml:"coder"`
	Reviewer int64 `yaml:"reviewer"`
}

// Role is what a stage does, and so which model and limit it gets.
type Role string

const (
	RoleCoder    Role = "coder"
	RoleReviewer Role = "reviewer"
)

// StageRole is the role of a stage: implement, fix and review_first (the
// first-line review) are the coder, review (the senior one) the reviewer. An
// unknown stage is the coder.
func StageRole(stage string) Role {
	if stage == "review" {
		return RoleReviewer
	}
	return RoleCoder
}

// IsReviewStage reports whether a stage reviews a change: the senior review
// and the first-line one. Both are handed the built review input
// (runner.reviewPromptWithInput) and both are restricted the same way; the
// two differ in whose model they run on, which is StageRole's business, not
// this one's.
func IsReviewStage(stage string) bool {
	return stage == "review" || stage == "review_first"
}

// ModelFor is the model of role r: models.<role>, else agent.model (a task's
// model override is applied to Models.Coder). "" means Claude Code's default.
func (a Agent) ModelFor(r Role) string {
	m := a.Models.Coder
	if r == RoleReviewer {
		m = a.Models.Reviewer
	}
	if m == "" {
		return a.Model
	}
	return m
}

// MaxOutputFor is role r's per-call output limit, 0 for none.
func (a Agent) MaxOutputFor(r Role) int64 {
	if r == RoleReviewer {
		return a.MaxOutputTokens.Reviewer
	}
	return a.MaxOutputTokens.Coder
}

// PinnedModels are the three models a run under a budget charges through the
// gateway: the coder's, the reviewer's and the background model's, in the
// order PinPaths names them. An empty name is one the config pins to nothing
// (the agent's own default), so callers that judge a pin keep the empty
// entry rather than dropping it.
func (a Agent) PinnedModels() []string {
	return []string{a.ModelFor(RoleCoder), a.ModelFor(RoleReviewer), a.Models.Background}
}

// Workflow is one buildable unit of the repository, such as a server or a web app.
type Workflow struct {
	// Profile names the project layer's profile this workflow starts from;
	// the workflow's own keys override it field by field. "" is none.
	// Binaries before 0.6.0 refuse the key.
	Profile string `yaml:"profile,omitempty"`
	// JobProfile names the project policy's job profile
	// (projectpolicy.Profile, design project-centric §4.1) this workflow
	// runs on: the Cloud Run job's shape — its provider, cpu, memory, task
	// timeout, secret mounts and admission list — selected by name from the
	// published policy. "" is the default, DefaultJobProfile.
	//
	// It is job_profile and NOT profile: profile: has named the project
	// layer's workflow template since 0.6.0 (internal/config/layer.go's
	// Profiles), and one key cannot carry both meanings — a layered
	// repository's `profile: server` would then also have to be a policy
	// profile called server, and `fugaro project policy publish` would
	// refuse every installation that uses a layer profile. The design's
	// §4.1 writes the selection as `workflows.<name>.profile`; this release
	// spells it job_profile, and the design needs the same one-word
	// correction (flagged for the plan's task 3.1 review).
	//
	// The json tag is what Config.SHA256 hashes: a field without one changes
	// every configuration hash in the installation — the goldens of
	// TestNoPolicyIsM9aBehaviour and TestCeilingOnlyPolicyGolden among them —
	// for a key most files do not set. It is omitempty, like every other
	// optional key here (the Agent.OAuthToken precedent).
	JobProfile string `yaml:"job_profile,omitempty" json:"job_profile,omitempty"`
	// Base is removed in 0.7.0: kept so Validate can refuse it with the
	// migration (MigrationDoc). Read the workflow's base kind through
	// BaseKind, which always returns BaseKind now.
	Base       string       `yaml:"base"`
	Image      Image        `yaml:"image"`
	Dockerfile string       `yaml:"dockerfile"`
	Commands   Commands     `yaml:"commands"`
	Cache      []CacheEntry `yaml:"cache"`
	Secrets    []Secret     `yaml:"secrets"`
	Resources  Resources    `yaml:"resources"`
	Timeouts   Timeouts     `yaml:"timeouts"`
	Rebuild    Rebuild      `yaml:"rebuild"`
	// Checkout is baked (the derived image holds a clone the runner moves
	// to the task's ref) or clone (the job runs the installation's base
	// image directly, with the runner making a blobless clone at start);
	// "" defaults to baked (design generic-tool.md §2, G3, G5). The build
	// acts on it (generic-tool plan Task 16): a clone workflow's job image
	// is base_images.base, with no per-repository image build or daily
	// check. The runner side is what actually reads FUGARO_CHECKOUT and
	// makes the blobless clone: it ships at fugaro 0.7.5
	// (internal/cli/recipes_skew.go's cloneSince), and checkout: clone
	// itself shipped in 0.9.0, when that flip landed. A launch is still
	// refused on a job image older than cloneSince, whether or not the
	// installation's own base image (fugaro-base) has been published — a
	// separate, person-run step. Use checkout: baked for a workflow that
	// needs a system package, a service or a Dockerfile.
	//
	// shared (design forge-authorship-images/shared-images.md §6, route A)
	// is the third kind: the job runs a shared dependency-layer image — one
	// image per resolved dependency set, holding no repository's source —
	// and the run clones the repository's own source at launch, the way a
	// clone run does, from the same two job variables. It accepts the keys
	// that describe that layer (image.apt, image.tools, image.key_files,
	// image.skip_build_scripts) and refuses the per-repository ones
	// (image.setup, dockerfile), which is what it exists for: a clone
	// workflow has no build to honor them with.
	Checkout string `yaml:"checkout,omitempty"`
	// Inputs caps what a launcher may attach to a run of this workflow
	// (design artifact-runs.md §4). Binaries before 0.8.0 refuse the key.
	Inputs Inputs `yaml:"inputs"`
}

// Checkout values of a workflow (design generic-tool.md §2, G3;
// checkout: shared is design forge-authorship-images/shared-images.md §6,
// route A).
const (
	CheckoutBaked = "baked"
	CheckoutClone = "clone"
	// CheckoutShared runs a shared dependency-layer image — one image per
	// resolved dependency set, holding no repository's source — and clones
	// the repository's own source at launch. The kind exists because such
	// a job must be clone-shaped while still declaring the
	// dependency-layer keys a build honors, which checkout: clone refuses.
	CheckoutShared = "shared"
)

// BaseKind is the base kind w builds on: always BaseKind, the one Fugaro
// base. A non-empty w.Base is a removed legacy value, refused by Validate.
func (w Workflow) BaseKind() string {
	return BaseKind
}

// DefaultJobProfile is the project policy profile a workflow that names none
// runs on (design project-centric §4.1). It is the one profile a published
// policy must define: internal/projectpolicy refuses a policy that carries a
// profiles block without it.
const DefaultJobProfile = "standard"

// JobProfileOrDefault is the policy profile w runs on: w.JobProfile, or
// DefaultJobProfile when it names none. One accessor, so the default cannot
// differ between the readers of the selection.
//
// A workflow that names no profile has nothing for the policy to refuse:
// internal/runner's checkJobProfile refuses an explicit name the published
// policy does not define (a policy that carries a profiles block must define
// the default, so the default is always defined where profiles exist, and a
// policy with no profiles block is one this release applies nothing from), and
// it reads the *default* through this accessor to compare it with the profile
// of the Cloud Run job the run is in. The readers that do need the default —
// that comparison, and the job layout Task 3.2 builds, which makes one Cloud
// Run job and one job service account per (profile, provider, account) — read
// it here, so the default is spelled in one place.
func (w Workflow) JobProfileOrDefault() string {
	if w.JobProfile == "" {
		return DefaultJobProfile
	}
	return w.JobProfile
}

// Rebuild says when the daily image check rebuilds the workflow's image
// (design §7.2). A change to image:, dockerfile: (or the file it names), base
// or image.setup always rebuilds, and can't be turned off: it makes the image
// wrong, not just stale.
type Rebuild struct {
	Check     string   `yaml:"check"`     // daily | off (off: rebuilt only by fugaro image build)
	MaxAge    Duration `yaml:"max_age"`   // rebuild an image older than this; 0 turns the age trigger off
	Lockfiles *bool    `yaml:"lockfiles"` // rebuild when a cache key file changed on the base branch
	Base      *bool    `yaml:"base"`      // rebuild when the base image's digest moved
	Paths     []string `yaml:"paths"`     // globs; a change under one on the base branch forces a rebuild
}

// DefaultMaxAge bounds how long a base image's security fixes wait until a
// published base image moves under the base trigger.
const DefaultMaxAge = 14 * 24 * time.Hour

// Defaults returns r with every field it leaves unset filled in: a daily
// check, a 14-day age limit, both the lockfile and base triggers on, and no
// extra paths. An explicit max_age of 0 is kept.
func (r Rebuild) Defaults() Rebuild {
	if r.Check == "" {
		r.Check = "daily"
	}
	if !r.MaxAge.Set {
		r.MaxAge = Duration{Duration: DefaultMaxAge, Set: true}
	}
	if r.Lockfiles == nil {
		t := true
		r.Lockfiles = &t
	}
	if r.Base == nil {
		t := true
		r.Base = &t
	}
	if r.Paths == nil {
		r.Paths = []string{}
	}
	return r
}

// Image customizes the workflow's generated derived image (design §7.2). The
// zero value means the base image's defaults.
type Image struct {
	// Node is removed in 0.7.0: kept so Validate can refuse it with the
	// migration (MigrationDoc).
	Node string `yaml:"node"`
	// JDK is removed in 0.7.0: kept so Validate can refuse it with the
	// migration (MigrationDoc).
	JDK   string   `yaml:"jdk"`
	Apt   []string `yaml:"apt"`   // extra system packages, installed as root
	Setup []string `yaml:"setup"` // extra RUN steps, run as fugaro in /work/repo after the warm-up
	// SkipBuildScripts makes a Node dependency warm-up (detected by content,
	// not by base kind: design base-image.md §4, decision B8) install
	// without running package lifecycle or build scripts, leaving those
	// builds to the workflow's build command (design §7.2).
	SkipBuildScripts bool `yaml:"skip_build_scripts"`
	// Tools are mise tools, name to version, written to the image's global
	// mise config for a repository without a mise config of its own (design
	// base-image.md section 4). The Fugaro base only.
	Tools MiseTools `yaml:"tools"`
	// KeyFiles are repository-relative paths — a file or a directory — the
	// dependency install reads and the lockfiles do not carry: the early
	// layer of the derived image checks them out before the source layer,
	// and a change to one of them rebuilds the image, exactly as a lockfile
	// change does (design §7.2). A repository that patches dependencies at
	// install time (patch-package, a vendored toolchain) declares them here;
	// validateImage refuses anything that could leave the repository or
	// match more than the path it names.
	//
	// The json tag keeps an absent key out of the resolved config's hash, as
	// Author's does: without it every repository's hash would gain
	// "KeyFiles":null and move.
	KeyFiles []string `yaml:"key_files" json:"key_files,omitempty"`
}

// IsZero reports whether the image block sets nothing.
func (i Image) IsZero() bool {
	return i.Node == "" && i.JDK == "" && len(i.Apt) == 0 && len(i.Setup) == 0 && !i.SkipBuildScripts && len(i.Tools) == 0 &&
		len(i.KeyFiles) == 0
}

// Commands are the repository's build and test commands, run through `sh -c`.
type Commands struct {
	Build string `yaml:"build"`
	Test  string `yaml:"test"`
	// Lint runs as the check step's gate command (design generic-tool
	// §1, G10); "" means the recipe may not name a check step over lint.
	Lint string `yaml:"lint"`
	// Fix is the check step's autofix command; it needs Lint, since the
	// gate runs fix, then lint, to judge whether the fix worked.
	Fix string `yaml:"fix"`
	// Render is a mode: artifact recipe's render command (design
	// artifact-runs.md §3): repository text, run once, in the checkout, as
	// the agent's user. "" means no artifact recipe may run against this
	// workflow: the runner refuses one whose workflow leaves it empty.
	Render string `yaml:"render"`
	// Probe is the artifact recipe's optional check on what it rendered:
	// one command, given the outputs directory and the names in its
	// environment. "" means the recipe's outputs.probe must be none.
	Probe       string       `yaml:"probe"`
	RerunFailed *RerunFailed `yaml:"rerun_failed"`
	Reports     []string     `yaml:"reports"`
}

// RerunFailed builds a command that reruns specific tests: Command followed by
// Each once per failed test, with {id} replaced by the shell-quoted test ID.
type RerunFailed struct {
	Command string `yaml:"command" json:"command"`
	Each    string `yaml:"each" json:"each"`
}

// CacheEntry is a content-addressed dependency cache (restored and written back in M4).
type CacheEntry struct {
	Key   []string `yaml:"key"`
	Paths []string `yaml:"paths"`
}

// Secret maps a logical secret name to the environment variable it is exposed as.
type Secret struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

// AnthropicSecret is the logical secret that holds the Anthropic API key. It
// is the one secret a job may go without: it is mounted whenever the secret
// has a version, and a run whose models all route through providers needs
// none (design docs/design/project-centric.md §6.1).
const AnthropicSecret = "anthropic-api-key"

// MissingAnthropicKey is what a call that needs Anthropic gets when no key is
// stored: the gateway refuses the call with it, and the agent's environment
// refuses to start without one in the one case where the gateway can't say so
// (the budget is off: there is no gateway, and every call goes to Anthropic).
// One text, so
// the two cannot say different things, and both name the fix that actually
// works: store a key and rerun `fugaro update` (the mount is planned at
// deploy), or route the model through a provider.
const MissingAnthropicKey = "no Anthropic API key is stored, so this call cannot go to Anthropic: store one with `fugaro secrets set anthropic-api-key` and rerun fugaro update, or route the model through a provider"

// BitbucketSecret is the logical secret that holds a Bitbucket credential:
// normally a repository access token, issued for one repository and unusable
// for any other, though an operator who holds one workspace or project access
// token instead stores that one for the project (docs/git-providers.md). It is
// the one reserved name D2's project-scope default leaves out, so
// `fugaro secrets set` and init's secrets stage store it in the repository's
// own copy unless the owner's local config allow-lists a repository for it
// (config.PerRepositorySecret, owner ruling of 2026-10-15; the workspace or
// project token is D461/D464 of 2026-10-27).
const BitbucketSecret = "bitbucket-token"

// ReservedSecrets are the logical secret names the platform itself mounts,
// mapped to the variable each one becomes. A workflow's secrets may not
// reuse them (design §5.1, §6.1).
var ReservedSecrets = map[string]string{
	BitbucketSecret:    "FUGARO_BITBUCKET_TOKEN",
	GitHubAppKeySecret: "FUGARO_GITHUB_APP_PRIVATE_KEY",
	ClaudeOAuthToken:   "CLAUDE_CODE_OAUTH_TOKEN",
	AnthropicSecret:    "ANTHROPIC_API_KEY",
}

// CodexOAuthToken is reserved for the Codex harness's OAuth credential but is
// deliberately NOT in ReservedSecrets: this map is the names the platform
// itself *mounts*, each with the variable it becomes, and nothing mounts a
// Codex credential yet. What the name needs now is to be refused wherever a
// secret name is accepted or written (CodexOAuthSecret), which is a different
// question from this one — and an entry here would claim a mount, a project
// job variable and a project-scope default that do not exist.

// Resources are the task resources for the workflow's job. The backend checks them against its own limits (Cloud Run: gcp.CheckResources).
type Resources struct {
	CPU    int    `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// JobProfile is the shape of one project job (design project-centric §4.1):
// what Cloud Run fixes per job and one workflow's selection picks by name.
// It is declared once, here, and carried in two documents — the operator's
// config file (localcfg.Config.Profiles) and the project policy
// (projectpolicy.Profile, which embeds this type) — so the two cannot drift.
//
// CPU and Memory are the same two fields Resources carries, and Cloud Run's
// own limits are checked against them (internal/backend/gcp.CheckResources):
// a profile naming a job the API would refuse must be refused here, where the
// operator can see it. TimeoutClass is the task timeout, a duration in Go
// syntax ("90m", "6h") through the same spelling timeouts.total uses; it
// becomes the job's task timeout — plus backend.TaskTimeoutSlack, the same
// sum internal/infra deploys the job with — so Cloud Run's 168h ceiling
// applies to the sum, which is what projectpolicy.validTimeoutClass bounds.
// Secrets are logical secret NAMES the job mounts, never values — the policy
// node is readable by every run token. AllowRepos is the profile's admission
// list (design §4.1): the key absent means the project's allow_repos applies,
// and a key written with no repository under it is refused (localcfg, from
// allowReposWritten) rather than read as the same statement — a blank list
// would widen the profile to the project's enrollment, and admission is the one
// thing a profile's list must never widen.
//
// The provider is not optional (finding 4): one job serves one provider,
// because the provider is part of the slug, the job's identity and the git
// credential the job holds.
type JobProfile struct {
	Provider     string   `yaml:"provider" json:"provider"`
	CPU          int      `yaml:"cpu" json:"cpu"`
	Memory       string   `yaml:"memory" json:"memory"`
	TimeoutClass string   `yaml:"timeout_class" json:"timeout_class"`
	Secrets      []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	AllowRepos   []string `yaml:"allow_repos,omitempty" json:"allow_repos,omitempty"`
	// ClaudeAccounts are the Claude accounts this profile serves, by name
	// ("default" for the unnamed claude-oauth-token, or a name of the local
	// config's claude_accounts block). An empty list is the profile that
	// serves no account, which is every profile today.
	//
	// It is a NEW field rather than a secrets: entry, and that is the point
	// (design project-named-oauth-tokens §4.1, D4): a Claude OAuth token is
	// never something an operator lists among a profile's mounts — the
	// secrets: refusal for one stays in force, unchanged
	// (projectpolicy's isClaudeOAuthSecret) — and the deployer adds the one
	// mount an account's job carries. So this field names an ACCOUNT, and the
	// job it selects is one per (profile, provider, account): the account
	// decides which job a run lands on, and the job's own service account is
	// the only thing that may read that account's token (D2).
	//
	// What it does not say is which repositories the token may be used by:
	// that is the account's own allow_data_to in the local config, and the
	// policy's accounts.<name>.allow_repos derived from it. A profile that
	// names an account admits only repositories that account covers, which
	// publish and `fugaro update` refuse to violate (§4.1's subset rule).
	ClaudeAccounts []string `yaml:"claude_accounts,omitempty" json:"claude_accounts,omitempty"`
	// allowReposWritten records that the document gave allow_repos at all. It
	// exists because a key written with nothing under it (`allow_repos:`, or
	// `~`, with every entry commented out) decodes to the same nil slice as an
	// absent key, and the two mean opposite things here: absent is "the
	// project's allow_repos applies", written is a list the operator wrote and
	// left empty. Only the mapping node can tell them apart (UnmarshalYAML),
	// the way ModelProvider.allowDataToWritten does for allow_data_to.
	allowReposWritten bool
}

// jobProfileType is JobProfile's own type name, for the unknown-field refusal
// below: UnmarshalYAML has to build the message yaml.v3's strict mode would
// have built, and that mode names the type it was decoding into.
var jobProfileType = reflect.TypeOf(JobProfile{})

// jobProfileKeys are the yaml keys a profiles entry accepts, read from the
// struct's own tags so a new field is picked up without a second list (the
// same thing providerKeys does for a providers entry).
var jobProfileKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(JobProfile{})
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}()

// UnmarshalYAML decodes one profiles entry, and refuses a key it does not know.
// That second job is normally the decoder's own (Decoder.KnownFields), but
// known fields are a property of a Decoder while decoding through a Node — the
// only way to see a key that was written with nothing under it — is always
// lenient, so the check is repeated here, with the same message.
func (p *JobProfile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: a profile must be a mapping of field to value", node.Line)}}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; !jobProfileKeys[k.Value] {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, jobProfileType)}}
		}
	}
	type plain JobProfile // no methods: decodes the fields without recursing into this one
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*p = JobProfile(v)
	// A null value is short-circuited by yaml.v3 before any unmarshaler sees
	// it, so the key's presence is read off the node itself: written with
	// nothing under it is not the same state as absent.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "allow_repos" {
			p.allowReposWritten = true
			break
		}
	}
	return nil
}

// AllowReposWritten reports whether the document gave allow_repos at all, so a
// reader can refuse a written list with nothing in it instead of reading it as
// the absent key (localcfg, and projectpolicy's node-side check for the same
// value written as JSON).
func (p JobProfile) AllowReposWritten() bool { return p.allowReposWritten }

// Timeouts bound a run (design §4.5).
type Timeouts struct {
	Total  Duration `yaml:"total"`
	Stage  Duration `yaml:"stage"`
	Verify Duration `yaml:"verify"`
	// Render bounds the render step alone (design artifact-runs.md §3);
	// unset means min(verify, total - finalize_reserve), computed in
	// applyDefaults beside the other three. The run's own deadline caps it
	// further.
	Render          Duration `yaml:"render"`
	FinalizeReserve Duration `yaml:"finalize_reserve"`
}

// The ceilings and defaults of a workflow's inputs: block (design
// artifact-runs.md §4, P6). MaxFiles, MaxBytes (per file) and MaxTotalBytes
// (the whole set) are what fugaro.yaml may ask for; a written 0 means the
// default, which the accessors below apply, so Validate can still tell a
// cap the file wrote from one it left out.
const (
	MaxInputFiles      = 64
	MaxInputBytes      = int64(1) << 30
	MaxInputTotalBytes = int64(4) << 30

	DefaultInputFiles      = 16
	DefaultInputBytes      = int64(256) << 20
	DefaultInputTotalBytes = int64(1) << 30
)

// Inputs caps what a launcher may attach to a run of this workflow: the same
// shape as a recipe's outputs block, with the launcher on the other side of
// the wire (design artifact-runs.md §4).
type Inputs struct {
	MaxFiles      int   `yaml:"max_files"`       // 0 means DefaultInputFiles
	MaxBytes      int64 `yaml:"max_bytes"`       // 0 means DefaultInputBytes; PER FILE
	MaxTotalBytes int64 `yaml:"max_total_bytes"` // 0 means DefaultInputTotalBytes; THE WHOLE SET
}

// MaxFileCount is how many files may be attached: MaxFiles, or
// DefaultInputFiles when the workflow did not write it.
func (i Inputs) MaxFileCount() int {
	if i.MaxFiles <= 0 {
		return DefaultInputFiles
	}
	return i.MaxFiles
}

// MaxFileBytes is the most one attached file may hold: MaxBytes, or
// DefaultInputBytes when the workflow did not write it.
func (i Inputs) MaxFileBytes() int64 {
	if i.MaxBytes <= 0 {
		return DefaultInputBytes
	}
	return i.MaxBytes
}

// MaxSetBytes is the most the whole set may hold: MaxTotalBytes, or
// DefaultInputTotalBytes when the workflow did not write it. The memory
// rule counts this beside the output set's doubled staging copy
// (runner.RecipeGateProblems).
func (i Inputs) MaxSetBytes() int64 {
	if i.MaxTotalBytes <= 0 {
		return DefaultInputTotalBytes
	}
	return i.MaxTotalBytes
}

// Duration is a time.Duration written in Go syntax, such as "90m" or "1h30m",
// optionally led by whole days, as in "14d" or "1d12h". Set records that the
// file gave a value, so an explicit 0 differs from an omitted field.
type Duration struct {
	time.Duration
	Set bool
}

var daysRE = regexp.MustCompile(`^([0-9]+)d(.*)$`)

// ParseDuration parses a duration the way fugaro.yaml writes one: Go syntax
// such as "90m" or "1h30m", optionally led by whole days, as in "14d" or
// "1d12h". It is the one parser for that spelling, so a duration accepted in
// one document is accepted in every document: the project policy's
// profile timeout_class is held to it too (projectpolicy.validTimeoutClass),
// where bare time.ParseDuration would refuse a "14d" the same installation
// accepts for timeouts.total.
func ParseDuration(s string) (time.Duration, error) {
	var days time.Duration
	rest, hasDays := s, false
	if m := daysRE.FindStringSubmatch(rest); m != nil {
		count, err := strconv.Atoi(m[1])
		// The rest must be unsigned: 1d-5h is not 19h.
		if err != nil || count > 36500 || strings.HasPrefix(m[2], "-") || strings.HasPrefix(m[2], "+") {
			return 0, fmt.Errorf("invalid duration %q (days lead an unsigned duration, as in 14d or 1d12h)", s)
		}
		days, rest, hasDays = time.Duration(count)*24*time.Hour, m[2], true
	}
	var v time.Duration
	if rest != "" || !hasDays {
		var err error
		if v, err = time.ParseDuration(rest); err != nil {
			return 0, fmt.Errorf("invalid duration %q (use Go syntax such as 90m or 1h30m, or days such as 14d)", s)
		}
	}
	return days + v, nil
}

// UnmarshalYAML parses a Go duration string, with an optional day count.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	d.Duration, d.Set = v, true
	return nil
}

// MarshalYAML writes the duration as UnmarshalYAML reads it, such as
// 1h30m0s, so a resolved config prints (fugaro config show) and re-reads
// as written.
func (d Duration) MarshalYAML() (any, error) { return d.Duration.String(), nil }

// Problem is one reason a fugaro.yaml is invalid.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
	// Code names a problem a caller may act on, so it need not read the
	// message; "" for the rest. It is not part of the JSON output.
	Code string `json:"-"`
}

// CodeProjectRequired is the Code of the problem of a fugaro.yaml with no
// project:.
const CodeProjectRequired = "project_required"

// String formats a Problem for human-readable output, such as `fugaro validate`'s
// default (non-JSON) mode.
func (p Problem) String() string {
	switch {
	case p.Path != "" && p.Line > 0:
		return fmt.Sprintf("%s (line %d): %s", p.Path, p.Line, p.Message)
	case p.Path != "":
		return p.Path + ": " + p.Message
	case p.Line > 0:
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	default:
		return p.Message
	}
}

// Parse decodes fugaro.yaml strictly, applies defaults and validates the result.
// It returns either a config or the problems that prevented one.
func Parse(data []byte) (*Config, []Problem) {
	c, _, ps := Resolve(data, nil)
	return c, ps
}

// AgentOf reads the agent block out of a fugaro.yaml's own bytes, and says
// whether the file could be read at all.
//
// It is the repository's own block: both keys it carries are repository-scope
// (docs/design/named-oauth-tokens.md T2 for agent.oauth_token, which no project
// layer may name, and layered-config.md for agent.auth, whose layer a caller
// merges itself), so the file's bytes are the whole answer here and a file that
// needs a layer for something else still says what it says — which is why this
// reads the plain, strict decode and not Resolve, and why internal/cli can
// judge a launch on the fugaro.yaml at the ref the run checks out without
// resolving that run's whole config. Auth carries the default the rest of
// Fugaro applies (DefaultAgentAuth), so a file that names no auth reads the
// same here as in a resolved config; everything else is exactly as written.
//
// ok is false for bytes this Fugaro cannot read as a fugaro.yaml at all (a
// decode the strict reader refuses, an unknown field, a duplicate key): that is
// "cannot tell", which is not the same answer as "names no account" — and it is
// the answer a run whose own file is one of those gets too, since the runner
// resolves that same file with the same strict decode inside the job.
func AgentOf(data []byte) (Agent, bool) {
	c, ps := decodeRepo(data)
	if c == nil || len(ps) > 0 {
		return Agent{}, false
	}
	if c.Agent.Auth == "" {
		c.Agent.Auth = DefaultAgentAuth
	}
	return c.Agent, true
}

// decodeRepo decodes a fugaro.yaml strictly, without defaults or
// validation: the repository layer alone, with today's line-numbered
// errors.
func decodeRepo(data []byte) (*Config, []Problem) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "file is empty"}}
		}
		return nil, yamlProblems(err)
	}
	// The strict decode reads 123 or true as the string "123"; a project
	// name is a YAML string, as ProjectOf (which the runner and the CLI
	// use) insists, so the two cannot disagree about a file.
	if _, err := ProjectOf(data); err != nil {
		return nil, []Problem{problemFromYAML(err.Error())}
	}
	if _, err := GCPProjectOf(data); err != nil {
		return nil, []Problem{problemFromYAML(err.Error())}
	}
	return &c, nil
}

var yamlLineRE = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

func yamlProblems(err error) []Problem {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		ps := make([]Problem, 0, len(te.Errors))
		for _, msg := range te.Errors {
			ps = append(ps, problemFromYAML(msg))
		}
		return ps
	}
	return []Problem{problemFromYAML(err.Error())}
}

// yamlValueRE is the quoted value yaml.v3 puts in a type error ("cannot
// unmarshal !!str `abc...` into config.Agent"). The tag before it is not
// always !!word: an explicit tag (!<tag:x,...>) stands in its own text, a
// fugaro.yaml's own, so it is stripped the same way. yaml.v3 percent-decodes
// a verbatim tag, so that text can itself hold a space (not just a control
// or bidi character showKey alone would catch), which is why the tag group
// is .+? (any character, lazily), not \S+: a fugaro.yaml may be somebody
// else's, so an error names the key and the type wanted, never what was
// there, whatever the tag looks like. The value group is greedy (.*, not
// .*?): a tag can also decode to text that itself contains "` into " (say,
// !<tag:x%20%60y%60%20into%20z>, decoding to "tag:x `y` into z"), and a
// lazy value group would stop at that first, fake "` into " inside the
// tag, leaving the real value past it unstripped. Greedy instead matches
// through to the last "` into " in the message, which is always the real
// one: the type name after it is ours, never the writer's, so it never
// itself contains that text.
var yamlValueRE = regexp.MustCompile("(?s)(cannot unmarshal .+?) `.*` into ")

func problemFromYAML(msg string) Problem {
	msg = yamlValueRE.ReplaceAllString(msg, "$1 into ")
	// An explicit tag's text is the writer's own and can hold anything a
	// YAML tag allows, including an escape sequence or a bidi override
	// character; showKey escapes it like any other untrusted text.
	msg = showKey(msg)
	if m := yamlLineRE.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Line: line, Message: m[2]}
	}
	return Problem{Message: strings.TrimPrefix(msg, "yaml: ")}
}

// SelectWorkflow returns the named workflow, or the only one when name is empty.
func (c *Config) SelectWorkflow(name string) (string, Workflow, error) {
	names := sortedKeys(c.Workflows)
	if name == "" {
		if len(names) == 1 {
			return names[0], c.Workflows[names[0]], nil
		}
		return "", Workflow{}, fmt.Errorf("fugaro.yaml defines %d workflows; pick one of %s", len(names), strings.Join(names, ", "))
	}
	w, ok := c.Workflows[name]
	if !ok {
		return "", Workflow{}, fmt.Errorf("fugaro.yaml has no workflow %q (have %s)", name, strings.Join(names, ", "))
	}
	return name, w, nil
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
