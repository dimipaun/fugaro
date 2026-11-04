package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// WorkflowNameRE is a workflow's name, in fugaro.yaml and wherever else a
// workflow is named (the local config, the CLI). SecretNameRE is a
// workflow secret's logical name.
var (
	WorkflowNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	SecretNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

var (
	envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	memoryRE  = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)
	// branchNameRE is a conservative branch-name charset: no shell
	// metacharacters, whitespace or leading "-". badBranchRE adds the
	// `git check-ref-format --branch` rules that charset still allows.
	branchNameRE = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)
	badBranchRE  = regexp.MustCompile(`\.\.|//|(^|/)\.|\.lock(/|$)|[/.]$`)
)

// validBranchName reports whether b is a safe git branch name. base_branch
// reaches git, Docker build arguments and Cloud Build, and git itself
// accepts shell metacharacters such as $( and | in a ref name, so the rule
// is stricter than git's own.
func validBranchName(b string) bool {
	return branchNameRE.MatchString(b) && !badBranchRE.MatchString(b)
}

// ValidBranchName is validBranchName for the other config files that name
// a base branch (the local config's repos.<r>.base_branch).
func ValidBranchName(b string) bool { return validBranchName(b) }

// A workflow secret's variable is mounted into the runner's own
// environment on the job and into the image build step, so it must not be
// one that Fugaro sets, or that changes how the runner, git, a shell, the
// build or a language runtime behaves (a GODEBUG with http2debug makes Go
// log bearer tokens; LD_PRELOAD and the proxies take over the process or
// its traffic). ReservedEnvPrefixes are families, ReservedEnvNames single
// variables; both are compared ignoring case, as proxies are read in
// either. schemas/fugaro.schema.json mirrors both lists.
var (
	ReservedEnvPrefixes = []string{
		"FUGARO_", "ANTHROPIC_", "CLAUDE_CODE_", "GIT_", // Fugaro, Claude Code and git
		"LD_", "DYLD_", "PYTHON", "BASH", "LC_", // the loader, Python and the shell
		"DOCKER_", "BUILDKIT_", "CLOUDSDK_", "CLOUD_RUN_", "VERTEX_REGION_", // the build, gcloud and the job
		"GOOGLE_", "GCE_", "GRPC_", "STORAGE_EMULATOR_", // the Google clients: credentials, metadata server, gRPC, storage endpoint
	}
	ReservedEnvNames = []string{
		"GODEBUG", "GOFLAGS", "GOTRACEBACK", "GOMAXPROCS", "GOMEMLIMIT",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE",
		"PATH", "HOME", "PWD", "OLDPWD", "SHELL", "USER", "LOGNAME", "HOSTNAME", "TMPDIR", "IFS", "LANG", "TERM", "TZ",
		"ENV", "CDPATH", "GLOBIGNORE", "PS4", "SHELLOPTS", "PROMPT_COMMAND",
		"NODE_OPTIONS", "NODE_PATH", "PERL5LIB", "PERL5OPT", "RUBYOPT", "RUBYLIB",
		"JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_SDK_GO_LOGGING_LEVEL", "CLOUD_ML_REGION",
		"GCE_METADATA_HOST", "GCE_METADATA_IP",
		// The image build step's own variables (images/derived/cloudbuild.yaml).
		"IMAGE", "SECRET_ENVS", "WORKFLOW", "REPO_URL", "BASE_BRANCH",
	}
)

// ReservedEnv reports whether name, in any case, may not be a workflow
// secret's variable: see ReservedEnvPrefixes and ReservedEnvNames.
func ReservedEnv(name string) bool {
	u := strings.ToUpper(name)
	return slices.Contains(ReservedEnvNames, u) || slices.ContainsFunc(ReservedEnvPrefixes, func(p string) bool { return strings.HasPrefix(u, p) })
}

// Providers are the git provider kinds a repository may use: fugaro.yaml's
// git.provider and the local config's repos.<repo>.provider.
var Providers = []string{"github", "bitbucket"}

// Validate reports every rule a defaulted config breaks.
func Validate(c *Config) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if c.Version != 1 {
		add("version", "must be 1")
	}
	switch {
	case c.Project == "":
		add("project", "is required: the Fugaro project this repository belongs to (fugaro config example shows it)")
		ps[len(ps)-1].Code = CodeProjectRequired
	case !ProjectNameRE.MatchString(c.Project):
		add("project", "must be a project name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
	if c.Profile != "" && !ProjectNameRE.MatchString(c.Profile) {
		add("profile", "must be a profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
	if c.GCPProject != "" && !GCPProjectRE.MatchString(c.GCPProject) {
		add("gcp_project", "must be a GCP project ID: 6 to 30 of a-z, 0-9 and '-', starting with a letter")
	}
	if !slices.Contains(Providers, c.Git.Provider) {
		add("git.provider", "must be one of %s", strings.Join(Providers, ", "))
	}
	// git.author is a closed vocabulary with one legal extra value: "" is
	// the key absent (the run's own identity authors its commits, today's
	// behaviour), so it is accepted without being named in the sentence.
	if c.Git.Author != "" && !slices.Contains(GitAuthors, c.Git.Author) {
		add("git.author", "must be one of %s", strings.Join(GitAuthors, ", "))
	}
	if !validBranchName(c.Git.BaseBranch) {
		add("git.base_branch", "must be a plain git branch name: letters, digits, '.', '_', '-' and '/', not starting with '-', '.' or '/', with no '..', '//', component starting with '.', or trailing '/', '.' or '.lock'")
	}
	if !KnownAgentAuth(c.Agent.Auth) {
		add("agent.auth", "must be one of %s", strings.Join(AgentAuths, ", "))
	}
	// 0 is unset, and an unset knob takes the rigor mode's own default
	// (internal/rigor's DefaultRounds): the value is resolved where the mode
	// is, which this package does not read. Unset is not "fall through to the
	// layer below": a layer's scalars replace whole (resolve.go), so a
	// repository's written 0 replaces the project layer's number, and the
	// mode's row is what supplies the gap.
	if c.Agent.ReviewRounds < 0 || c.Agent.ReviewRounds > 10 {
		add("agent.review_rounds", "must be between 1 and 10, or 0 (unset: the mode's default supplies it)")
	}
	if !slices.Contains([]string{FirstLineAuto, FirstLineOn, FirstLineOff}, c.Agent.FirstLineReview) {
		add("agent.first_line_review", "must be one of auto, on, off")
	}
	if c.Agent.FirstLineRounds < 0 || c.Agent.FirstLineRounds > MaxFirstLineRounds {
		add("agent.first_line_rounds", "must be between 1 and %d, or 0 (unset: the mode's default supplies it)", MaxFirstLineRounds)
	}
	if c.Agent.AdversarialRounds < 0 || c.Agent.AdversarialRounds > 10 {
		add("agent.adversarial_rounds", "must be between 1 and 10, or 0 (unset: the mode's default supplies it)")
	}
	ps = append(ps, levelProblems("agent.levels", &c.Agent.Levels)...)
	if c.Agent.Recipe != "" && !recipe.NameRE.MatchString(c.Agent.Recipe) {
		add("agent.recipe", "must be a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
	if badUSD(c.Agent.MaxBudgetUSD) {
		add("agent.max_budget_usd", "must be a finite number, not negative")
	}
	// Same rules as task.Spec.OAuthToken (internal/task/task.go), which
	// mirrors these from config.ClaudeAccountNameRE and
	// MaxClaudeAccountNameLen: this is a plain shape check, not a membership
	// check. Whether the name is one this repository may actually use
	// (config.AllowedClaudeAccounts) depends on the local config, which this
	// function never sees; fugaro run checks that at launch.
	//
	// The reserved name is the one rule here that is not about its shape: it
	// matches ClaudeAccountNameRE and is well inside the length cap, and what
	// refuses it is the platform already holding a credential under it
	// (reservedClaudeAccountName), the same refusal claude_accounts' own keys
	// and a profile's claude_accounts entry get.
	switch {
	case c.Agent.OAuthToken == "":
	case c.Agent.OAuthToken == DefaultClaudeAccount:
		add("agent.oauth_token", `must not be "default": the empty value already means the repository's default account`)
	case ReservedClaudeAccountName(c.Agent.OAuthToken):
		add("agent.oauth_token", "%s", ReservedClaudeAccountNameMessage)
	case len(c.Agent.OAuthToken) > MaxClaudeAccountNameLen:
		add("agent.oauth_token", "must be at most %d characters", MaxClaudeAccountNameLen)
	case !ClaudeAccountNameRE.MatchString(c.Agent.OAuthToken):
		add("agent.oauth_token", "must be a Claude account name")
	}
	// agent.compression is reserved and refused until the sidecar that reads it
	// ships: every value but "" is refused here, naming itself, so a repository
	// that tries the key gets this message and not a strict-parse unknown-field
	// error. Nothing reads the field, so no value may reach a run. The lift
	// replaces this branch with the closed-set shape check and leaves the auth
	// rule below standing (docs/design/headroom-compression.md §4).
	if c.Agent.Compression != "" {
		add("agent.compression", "is not yet implemented: this Fugaro accepts only the empty value (off), and the context-compression proxy the key will name is refused until it ships")
	}
	// The proxy sits in front of api-key model calls only. oauth and vertex
	// route their own way and are refused outright rather than run without the
	// compression the repository asked for (design §4).
	if c.Agent.Compression != "" && (c.Agent.Auth == "oauth" || c.Agent.Auth == "vertex") {
		add("agent.compression", "cannot be combined with agent.auth: %s: the proxy is refused for that auth (docs/design/headroom-compression.md §4)", c.Agent.Auth)
	}
	ps = append(ps, validateAgentModels(c.Agent)...)
	ps = append(ps, validateCascadeFollowup(c.Agent, c.Followup)...)
	ps = append(ps, validateBudget(c.Budget)...)
	ps = append(ps, validateFollowup(c.Git.Provider, c.Followup)...)
	if c.Rigor != nil {
		// The repository's own block, by the same rules the project layer's
		// is judged by: one function, two prefixes.
		ps = append(ps, validateRigor("rigor", c.Rigor.Default, c.Rigor.Areas)...)
	}
	ps = append(ps, validateGovernance("governance", c.Governance)...)
	// A repository's ab: block may only opt out of sampling (D556, D557):
	// every other key of it is the project layer's, and a repository that
	// names a challenger could send its code to a provider the owner opened
	// only to others. The rules are the same ones `fugaro config publish`
	// holds the layer's block to, stated once in ab.go.
	ps = append(ps, validateRepoAB(c.AB)...)
	if len(c.Workflows) == 0 {
		add("workflows", "must define at least one workflow")
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + showKey(name)
		if !WorkflowNameRE.MatchString(name) {
			add(p, "workflow name must match %s", WorkflowNameRE)
		}
		if w.Profile != "" && !ProjectNameRE.MatchString(w.Profile) {
			add(p+".profile", "must be a profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
		// The job profile is a policy profile name, held to the same rule the
		// policy holds its own names to (projectpolicy.ValidProfileName): a
		// name this refuses could never be defined in a policy, so the
		// selection would be refused at run start instead of here.
		if w.JobProfile != "" && !ProjectNameRE.MatchString(w.JobProfile) {
			add(p+".job_profile", "must be a job profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit (it names a profile of the project policy)")
		}
		if w.Base != "" {
			add(p+".base", legacyBaseMessage, MigrationDoc)
		}
		ps = append(ps, validateImage(p, w)...)
		ps = append(ps, validateCheckout(p, w)...)
		if strings.TrimSpace(w.Commands.Build) == "" {
			add(p+".commands.build", "is required")
		}
		if strings.TrimSpace(w.Commands.Test) == "" {
			add(p+".commands.test", "is required")
		}
		if strings.TrimSpace(w.Commands.Fix) != "" && strings.TrimSpace(w.Commands.Lint) == "" {
			add(p+".commands.fix", "needs commands.lint (the check step runs fix, then lint)")
		}
		if rf := w.Commands.RerunFailed; rf != nil {
			if strings.TrimSpace(rf.Command) == "" {
				add(p+".commands.rerun_failed.command", "is required")
			}
			if !strings.Contains(rf.Each, "{id}") {
				add(p+".commands.rerun_failed.each", "must contain {id}")
			}
		}
		for i, ce := range w.Cache {
			cp := fmt.Sprintf("%s.cache[%d]", p, i)
			if len(ce.Key) == 0 {
				add(cp+".key", "must list at least one file")
			}
			if len(ce.Paths) == 0 {
				add(cp+".paths", "must list at least one path")
			}
		}
		seen := map[string]bool{}
		for i, s := range w.Secrets {
			sp := fmt.Sprintf("%s.secrets[%d]", p, i)
			if !SecretNameRE.MatchString(s.Name) {
				add(sp+".name", "must be lower-case letters, digits and dashes")
			}
			if _, reserved := ReservedSecrets[s.Name]; reserved {
				add(sp+".name", "%s is reserved for the platform's own secrets", s.Name)
			} else if strings.HasPrefix(s.Name, ClaudeOAuthTokenPrefix) {
				// The whole claude-oauth-token-<name> namespace is a named
				// Claude account's derived secret ID (ClaudeAccountSecret,
				// design docs/design/named-oauth-tokens.md §5), never a
				// workflow's own: a name that merely looks like one would
				// let fugaro.yaml steer where the owner's pasted token
				// value goes (claude setup-token's prompt in init matches
				// on this same prefix), bypassing claude_accounts'
				// allow_data_to allow-list, which only the local config
				// may ever set (T1). Reserved whether or not the suffix
				// happens to name a configured account.
				add(sp+".name", "names starting %s are reserved for named Claude accounts (claude_accounts in the local config), not a workflow secret", ClaudeOAuthTokenPrefix)
			} else if CodexOAuthSecret(s.Name) {
				// The Codex harness's OAuth credential names, reserved ahead
				// of the harness (CodexOAuthToken, owner ruling of
				// 2026-10-18). Nothing mounts them yet and there is no
				// account or provider of Codex to allow one, so unlike the
				// Claude namespace above there is no legitimate case at all:
				// any name of this shape in fugaro.yaml is either a typo or
				// an attempt to have the platform's future credential read a
				// value the repository chose. Refused whether or not a Codex
				// provider ever names it.
				add(sp+".name", "%s and names starting %s are reserved for the Codex harness's OAuth credential (codex-oauth-token), not a workflow secret", CodexOAuthToken, CodexOAuthTokenPrefix)
			} else if s.Name == CredentialsBundleSecret {
				// The credentials service's one bundle container (C1.11's
				// review finding n2). Nothing mounts it: its document is every
				// model credential the installation holds, and the service is
				// the only reader. A workflow that declared the name would
				// mount that document into a job — the whole credential set,
				// into a container a run's agent can read — so the name is
				// refused here, where every other reserved name is refused,
				// and again at `fugaro secrets set` (internal/cli's
				// credentialsBundleRefusal), which is the write path.
				add(sp+".name", "%s is the credentials service's own bundle container (the platform's), not a workflow secret", CredentialsBundleSecret)
			}
			switch {
			case !envNameRE.MatchString(s.Env):
				add(sp+".env", "must be an upper-case environment variable name")
			case slices.Contains(ReservedEnvNames, strings.ToUpper(s.Env)):
				add(sp+".env", "%s is reserved: Fugaro sets it, or it changes how the runner, git, a shell, the build or a runtime behaves; rename it", s.Env)
			case ReservedEnv(s.Env):
				add(sp+".env", "%s uses a reserved prefix (%s)", s.Env, strings.Join(ReservedEnvPrefixes, ", "))
			case seen[s.Env]:
				add(sp+".env", "%s is declared twice", s.Env)
			}
			seen[s.Env] = true
		}
		if w.Resources.CPU < 1 {
			add(p+".resources.cpu", "must be at least 1 (the compute backend checks its own limits)")
		}
		if !memoryRE.MatchString(w.Resources.Memory) {
			add(p+".resources.memory", "must look like 512Mi or 16Gi")
		}
		ps = append(ps, validateRebuild(p+".rebuild", w.Rebuild)...)
		t := w.Timeouts
		for _, d := range []struct {
			name string
			v    Duration
		}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"render", t.Render}, {"finalize_reserve", t.FinalizeReserve}} {
			if d.v.Duration <= 0 {
				add(p+".timeouts."+d.name, "must be positive")
			}
		}
		if t.FinalizeReserve.Duration >= t.Total.Duration {
			add(p+".timeouts.finalize_reserve", "must be shorter than timeouts.total")
		}
		if t.Stage.Duration > t.Total.Duration {
			add(p+".timeouts.stage", "must not exceed timeouts.total")
		}
		ps = append(ps, validateInputs(p+".inputs", w.Inputs)...)
	}
	return ps
}

// validateInputs reports a workflow's inputs caps (design artifact-runs.md
// §4, P6): each cap has a ceiling of its own and may not be negative, and a
// set cap below the per-file cap with room for only one file is a pair
// nobody can satisfy — that one legal file is the whole set, so the smaller
// cap silently governs. Only the caps the file wrote are compared: a
// defaulted one is not a relation the author wrote, and with room for more
// than one file the pair is a set cap doing its job.
func validateInputs(p string, in Inputs) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	for _, c := range []struct {
		name    string
		got     int64
		ceiling int64
	}{
		{"max_files", int64(in.MaxFiles), MaxInputFiles},
		{"max_bytes", in.MaxBytes, MaxInputBytes},
		{"max_total_bytes", in.MaxTotalBytes, MaxInputTotalBytes},
	} {
		switch {
		case c.got < 0:
			add(p+"."+c.name, "must not be negative")
		case c.got > c.ceiling:
			// max_files counts files, so its ceiling names no unit.
			if c.name == "max_files" {
				add(p+"."+c.name, "must be at most %d", c.ceiling)
			} else {
				add(p+"."+c.name, "must be at most %d bytes (%s)", c.ceiling, recipe.SizeText(c.ceiling))
			}
		}
	}
	if in.MaxFiles == 1 && in.MaxBytes > 0 && in.MaxTotalBytes > 0 && in.MaxTotalBytes < in.MaxBytes {
		add(p+".max_total_bytes", "must be at least inputs.max_bytes")
	}
	return ps
}

// Bounds of rebuild.max_age, other than 0 (the age trigger off).
const (
	minRebuildAge = time.Hour
	maxRebuildAge = 90 * 24 * time.Hour
)

// validateRebuild reports a workflow's rebuild block problems, each at p.<field>.
func validateRebuild(p string, r Rebuild) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if r.Check != "daily" && r.Check != "off" {
		add(p+".check", "must be daily or off")
	}
	if a := r.MaxAge.Duration; a != 0 && (a < minRebuildAge || a > maxRebuildAge) {
		add(p+".max_age", "must be 0 (no age limit) or between 1h and 90d")
	}
	for i, g := range r.Paths {
		gp := fmt.Sprintf("%s.paths[%d]", p, i)
		switch {
		case strings.TrimSpace(g) == "":
			add(gp, "must not be empty")
		case strings.HasPrefix(g, "/"):
			add(gp, "must be relative to the repository, not start with /")
		case slices.Contains(strings.Split(g, "/"), ".."):
			add(gp, "must not contain a .. component")
		case !doublestar.ValidatePattern(g):
			add(gp, "is not a valid glob pattern")
		}
	}
	return ps
}

// validateRigor reports a rigor block's problems, each at p.<key> or
// p.areas[i].<key>: p is "rigor" for the repository's own block and
// "defaults.rigor" for the project layer's, so one function states the rules
// for both layers (docs/project-layer.md §5 gives either layer both keys).
//
// It reads rigor.Area.Validate and rigor.Known, which is the whole point of
// the vocabulary living in internal/rigor: the value a configuration may
// write is the value the resolver can use, so a mode outside the vocabulary
// or a glob no matcher can read is refused where it is written rather than
// silently never firing at resolution time.
//
// Two rules of one list may not carry the same glob (F14). They would match
// exactly the same paths, and only the strictest matched rule decides (D20c),
// so the second one is named in the run's record for nothing the first does
// not already cover — a layer saying the same thing twice is a mistake its
// writer cannot see. The rule is per layer: the two area lists are a union
// rather than a replacement (D20c), and a repository's rule for the project
// layer's glob is a rule of its own, which this function never sees because
// each layer is validated on its own.
func validateRigor(p string, mode rigor.Mode, areas []rigor.Area) []Problem {
	var ps []Problem
	if mode != "" && !rigor.Known(mode) {
		ps = append(ps, Problem{Path: p + ".default", Message: "must be one of " + rigor.ModeList()})
	}
	if len(areas) > rigor.MaxAreas {
		ps = append(ps, Problem{Path: p + ".areas", Message: fmt.Sprintf("must hold at most %d rules", rigor.MaxAreas)})
	}
	// A rule already refused for its own shape is not also called a repeat:
	// it can never reach the resolver for the glob to matter, and the first
	// reason is the one its writer has to fix.
	seen := make(map[string]int, len(areas))
	for i, a := range areas {
		if msg := a.Validate(); msg != "" {
			ps = append(ps, Problem{Path: fmt.Sprintf("%s.areas[%d]", p, i), Message: msg})
			continue
		}
		if j, dup := seen[a.Match]; dup {
			ps = append(ps, Problem{Path: fmt.Sprintf("%s.areas[%d]", p, i), Message: fmt.Sprintf("repeats the glob %q of areas[%d]: two rules that match the same paths leave only the strictest of them deciding, and the other is named for nothing", a.Match, j)})
			continue
		}
		seen[a.Match] = i
	}
	return ps
}

// The governance bounds: the highest value each counting key of the block may
// carry, and the figures schemas/fugaro.schema.json states for the same keys
// (held equal by TestSchemaGovernanceBoundsMatchGo, the way
// MaxOutputTokensLimit is held to the schema's own 128000). Two surfaces read
// the same file — `fugaro validate` and the published schema a repository's CI
// may check against — and a value one refuses while the other accepts is a red
// check nobody can reproduce, or a number a run reads that the schema called
// impossible. The bounds are generous: what they stop is a figure no run could
// mean, not a figure a project would choose.
const (
	MaxReviewRatio      = 1000
	MaxReviewFloorCalls = 100000
	// MaxGovernanceSubagents is the most distinct sub-agents one stage may be
	// allowed to start. The spec's own incident, a review fanning out to about
	// ten, is already stopped by the default of 5; this is the ceiling on what
	// a project may raise it to.
	MaxGovernanceSubagents = 100
	MaxStageTokens         = 1000000000000
)

// validateGovernance reports a governance block's problems, each at
// p.<key>: p is "governance" for the repository's own block and the project
// layer's alike, so one function states the rules for both (the same "one
// function, two prefixes" validateRigor is).
//
// What it refuses is what no run could use, not what a layer may not say: a
// bound is valid here whether it is tighter or looser than Fugaro's default,
// because the layer that loosens the defaults is the project's and the merge
// is where a repository's loosening is dropped (internal/policy). The two
// rules that are about the shape rather than the layer are the fraction's and
// the ratio's: a stage budget of 1 or more is no budget under a cap it could
// spend whole (G6), and a ratio below 1 would stop review on a stage that
// made a single call.
//
// 0 and null mean "unset" everywhere here — the key takes Fugaro's default —
// so only a negative number or a non-positive duration is a problem. A
// duration key left out is unset too; one written as 0s is refused, as every
// other duration in fugaro.yaml is (validateTimeouts). The counting keys are
// bounded above by the constants above, which the schema states too.
func validateGovernance(p string, g *Governance) []Problem {
	if g == nil {
		return nil
	}
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	for _, c := range []struct {
		key    string
		v, max int64
	}{
		{"review_ratio", g.ReviewRatio, MaxReviewRatio},
		{"review_floor_calls", g.ReviewFloorCalls, MaxReviewFloorCalls},
		{"max_subagents", g.MaxSubagents, MaxGovernanceSubagents},
		{"stage_tokens.implement", g.StageTokens.Implement, MaxStageTokens},
		{"stage_tokens.review", g.StageTokens.Review, MaxStageTokens},
		{"stage_tokens.fix", g.StageTokens.Fix, MaxStageTokens},
	} {
		switch {
		case c.v < 0:
			add(p+"."+c.key, "must not be negative")
		case c.v > c.max:
			add(p+"."+c.key, "must be at most %d", c.max)
		}
	}
	for _, c := range []struct {
		key string
		v   float64
	}{
		{"stage_budget.implement", g.StageBudget.Implement},
		{"stage_budget.review", g.StageBudget.Review},
		{"stage_budget.fix", g.StageBudget.Fix},
	} {
		if math.IsNaN(c.v) || c.v < 0 || c.v >= 1 {
			add(p+"."+c.key, "must be 0 (the default) or a fraction of the run's cap between 0 and 1: the cap stays the ceiling whatever a stage is given")
		}
	}
	for _, c := range []struct {
		key string
		v   Duration
	}{
		{"stage_timeout.implement", g.StageTimeout.Implement},
		{"stage_timeout.review", g.StageTimeout.Review},
		{"stage_timeout.fix", g.StageTimeout.Fix},
	} {
		if c.v.Set && c.v.Duration <= 0 {
			add(p+"."+c.key, "must be positive")
		}
	}
	// The tiers are expectations, so what is refused here is a figure no run
	// could mean — a negative cost or duration, a fan-out past the same bound
	// max_subagents carries — and never an expectation larger than the per-run
	// cap: an expected cost above the cap is exactly what G6 says a tier may
	// say, because the run still stops at the cap. The tier NAMES are not
	// checked here: they are keys of the block, so a name outside the
	// vocabulary (internal/tier) is an unknown key the strict decoder refuses
	// before validation runs.
	for _, c := range []struct {
		key string
		v   TierLimits
	}{
		{"tiers.trivial", g.Tiers.Trivial},
		{"tiers.small", g.Tiers.Small},
		{"tiers.medium", g.Tiers.Medium},
		{"tiers.large", g.Tiers.Large},
	} {
		if math.IsNaN(c.v.ExpectedUSD) || math.IsInf(c.v.ExpectedUSD, 0) || c.v.ExpectedUSD < 0 {
			add(p+"."+c.key+".expected_usd", "must be 0 (the tier's default) or a finite dollar expectation")
		}
		if c.v.ExpectedMinutes < 0 {
			add(p+"."+c.key+".expected_minutes", "must not be negative")
		}
		switch {
		case c.v.MaxSubagents < 0:
			add(p+"."+c.key+".max_subagents", "must not be negative")
		case c.v.MaxSubagents > MaxGovernanceSubagents:
			add(p+"."+c.key+".max_subagents", "must be at most %d", MaxGovernanceSubagents)
		}
	}
	return ps
}

// validateCascadeFollowup reports the one thing followup.allow_public cannot
// be beside: a cascade credential. AllowPublic lets a follow-up run on a
// public repository (config.go's Followup.AllowPublic, read at
// internal/runner/followup.go), where a commenter who can push branches can
// steer the run's prompts — and what the cascade puts behind those prompts is
// a shared subscription credential (docs/credentials-service, D130: the
// refusal the held OAuth reviewer-stage plan carried over, its P6), so a run
// there may be steered by someone who was never trusted with it. It is a
// refusal rather than a warning for that reason: the whole point of an
// opt-in for a residual risk this size is that the dangerous combinations are
// hard refusals. The message names both ways out — the key to remove, and the
// auths a run that needs public follow-ups may use.
func validateCascadeFollowup(a Agent, f Followup) []Problem {
	if a.Auth != AuthCascade || !f.AllowPublic {
		return nil
	}
	return []Problem{{Path: "followup.allow_public", Message: fmt.Sprintf(
		"cannot be true with agent.auth: %s: a follow-up on a public repository can be steered by anyone who can push a branch, and a cascade run's prompts are carried by a shared subscription credential, which only trusted work may reach (D130). Remove followup.allow_public (follow-ups stay on private repositories), or run this repository with agent.auth: api-key or vertex",
		AuthCascade)}}
}

// CheckCascadeGateway refuses a cascade workflow whose budget leaves the
// gateway off. A cascade run has no credential of its own — the gateway is
// what carries its calls to the credentials service and accounts for them
// (D143) — so the mode has to be observe or enforce, and a mode that is off
// (or unset, which the runner reads the same way) is refused, naming the mode
// it found.
//
// mode is the run's *effective* budget mode (policy.Merge over the project's
// ceiling and the file), not the file's own, and that is why this rule is a
// function of a mode rather than a line inside Validate: Validate holds one
// file, and the file's mode is only a lower bound on the run's (a later layer
// may only tighten). A file that writes no budget under an installation whose
// ceiling enforces is a cascade workflow whose gateway is on, and refusing it
// here would refuse a valid repository.
//
// Its callers hold the merge, because Validate holds one file and the run's
// mode is a function of the layers above it: `fugaro validate`
// (internal/cli/validate.go, cascadeProblems) is the author's side, and the
// launch (internal/cli/run.go, cascadeLaunchRefusal) is the operator's — a
// launch refuses a cascade run whose effective mode leaves the gateway off,
// before the claim is taken and before anything is written, rather than
// letting the job fail on it.
func CheckCascadeGateway(a Agent, mode string) []Problem {
	if a.Auth != AuthCascade || mode == policy.ModeObserve || mode == policy.ModeEnforce {
		return nil
	}
	found := fmt.Sprintf("budget.mode is %s", mode)
	switch mode {
	case "":
		found = "budget.mode is not set (an absent mode and off are the same to the runner)"
	case policy.ModeOff:
		found = "budget.mode is off"
	}
	return []Problem{{Path: "agent.auth", Message: fmt.Sprintf(
		"%s needs the budget gateway: %s, and the gateway is what carries a cascade run's calls and accounts for them (a cascade run has no credential of its own). Set budget.mode to %s or %s — in the project config, or in fugaro.yaml",
		AuthCascade, found, policy.ModeObserve, policy.ModeEnforce)}}
}

// A trusted account ID, per provider: GitHub's numeric user ID, and
// Bitbucket's account_id in either of its forms (a site number and a UUID,
// or 24 hex digits). bitbucketUUIDRE is the Bitbucket user UUID, which the
// adapter never matches comment authors by.
var (
	githubUserIDRE  = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	bitbucketIDRE   = regexp.MustCompile(`^[0-9]{1,10}:[0-9a-f-]{36}$|^[0-9a-f]{24}$`)
	bitbucketUUIDRE = regexp.MustCompile(`^\{?[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}?$`)
)

// validateFollowup reports the followup block's problems, each at
// followup.trusted[i]. An ID's form depends on the provider; with an
// unknown provider (already reported at git.provider) only duplicates are
// checked.
func validateFollowup(provider string, f Followup) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for i, id := range f.Trusted {
		p := fmt.Sprintf("followup.trusted[%d]", i)
		add := func(format string, args ...any) {
			ps = append(ps, Problem{Path: p, Message: fmt.Sprintf(format, args...)})
		}
		switch {
		case strings.TrimSpace(id) == "":
			add("must not be empty")
		case seen[id]:
			add("%q is listed twice", id)
		case provider == "github" && !githubUserIDRE.MatchString(id):
			add("%q is not a GitHub numeric user ID (1 to 20 digits, no leading zero); a login can be renamed, so find the ID with: gh api users/%s --jq .id", id, id)
		case provider == "bitbucket" && bitbucketUUIDRE.MatchString(id):
			add("%q is a Bitbucket UUID; list the user's account_id instead (such as 557058:00000000-0000-0000-0000-000000000001), which is what comment authors are matched by", id)
		case provider == "bitbucket" && !bitbucketIDRE.MatchString(id) && bitbucketIDRE.MatchString(strings.ToLower(id)):
			add("%q is not a Bitbucket account_id: write its hex digits in lower case, as Bitbucket does (comment authors are matched exactly)", id)
		case provider == "bitbucket" && !bitbucketIDRE.MatchString(id):
			add("%q is not a Bitbucket account_id (such as 557058:00000000-0000-0000-0000-000000000001 or 24 hex digits)", id)
		}
		seen[id] = true
	}
	return ps
}

// checkDockerfile reports a workflow's repository Dockerfile if it is
// missing or breaks the derived-image contract (LintDockerfile).
func checkDockerfile(p, root string, w Workflow) []Problem {
	data, err := ReadRegular(filepath.Join(root, w.Dockerfile), MaxCheckoutFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return []Problem{{Path: p + ".dockerfile", Message: w.Dockerfile + " does not exist"}}
	case err != nil:
		return []Problem{{Path: p + ".dockerfile", Message: w.Dockerfile + " cannot be read: it must be a regular file of at most 1 MiB, not a link"}}
	}
	var ps []Problem
	for _, msg := range LintDockerfile(data) {
		ps = append(ps, Problem{Path: p + ".dockerfile", Message: w.Dockerfile + " " + msg})
	}
	return ps
}

// Check reports files a config refers to that do not exist under root, the
// repository checkout. `fugaro validate` runs it; the runner does not.
func Check(c *Config, root string) []Problem {
	var ps []Problem
	mustExist := func(path, rel string) {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			ps = append(ps, Problem{Path: path, Message: rel + " does not exist"})
		}
	}
	if c.Agent.Instructions != "" {
		mustExist("agent.instructions", c.Agent.Instructions)
	}
	// A review starting with "/" names a skill; anything else is a prompt file.
	if c.Agent.Review != "" && !strings.HasPrefix(c.Agent.Review, "/") {
		mustExist("agent.review", c.Agent.Review)
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + showKey(name)
		switch {
		case w.Dockerfile != "":
			ps = append(ps, checkDockerfile(p, root, w)...)
		case w.BaseKind() == BaseKind:
			ps = append(ps, checkBaseWorkflow(p, root, w)...)
		}
		for _, cmd := range []struct{ path, value string }{
			{p + ".commands.build", w.Commands.Build},
			{p + ".commands.test", w.Commands.Test},
		} {
			fields := strings.Fields(cmd.value)
			if len(fields) > 0 && strings.HasPrefix(fields[0], "./") {
				mustExist(cmd.path, fields[0])
			}
		}
	}
	return ps
}

// checkBaseWorkflow is Check for a generated image on the Fugaro base: one
// source of tools, and a package manager for image.skip_build_scripts.
func checkBaseWorkflow(p, root string, w Workflow) []Problem {
	var ps []Problem
	files, err := MiseConfigFiles(root)
	switch {
	case err != nil:
		ps = append(ps, Problem{Path: p, Message: "reading the repository's mise config: " + err.Error()})
	case len(files) > 0 && len(w.Image.Tools) > 0:
		ps = append(ps, Problem{Path: p + ".image.tools", Message: "the repository has its own mise config (" + strings.Join(files, ", ") +
			"): keep one; the repository's file, which developers use too, is the usual choice"})
	}
	pm, err := DetectNodePM(root)
	switch {
	case err != nil:
		ps = append(ps, Problem{Path: p, Message: err.Error()})
	case pm == nil && w.Image.SkipBuildScripts:
		ps = append(ps, Problem{Path: p + ".image.skip_build_scripts", Message: "applies only when the repository has a package.json and a lockfile (the Node warm-up)"})
	}
	return ps
}

// CheckWarnings are what Check finds that doesn't make the config wrong, for
// fugaro validate to print as warnings: a generated image on the Fugaro base
// with no mise config and no image.tools installs no language runtime.
func CheckWarnings(c *Config, root string) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		if w.Dockerfile != "" || len(w.Image.Tools) > 0 {
			continue
		}
		if files, err := MiseConfigFiles(root); err == nil && len(files) == 0 {
			ps = append(ps, Problem{Path: "workflows." + showKey(name), Message: "no mise.toml, .tool-versions or image.tools: the image installs no language runtime (docs/base-image.md)"})
		}
	}
	return ps
}

// badUSD reports a dollar amount no limit may be: negative, NaN or infinite.
func badUSD(v float64) bool { return v < 0 || math.IsNaN(v) || math.IsInf(v, 0) }

// MaxOutputTokensLimit is the highest per-call output limit fugaro.yaml may
// set: the most any current model produces.
const MaxOutputTokensLimit = 128000

// levelProblems are a review-level block's own rules (LevelSetting.validate,
// the one set of them, which ReviewLevels.Validate applies to a task's
// review_levels) as problems under path, one per level: a reader is told which
// level of which file is wrong, and the three blocks that carry the type —
// fugaro.yaml's agent.levels, the project layer's defaults.agent.levels and the
// task's review_levels — refuse the same documents.
func levelProblems(path string, r *ReviewLevels) []Problem {
	var ps []Problem
	for _, l := range []struct {
		name string
		set  *LevelSetting
	}{{"l1", r.L1}, {"l2", r.L2}, {"l3", r.L3}} {
		if l.set == nil {
			continue
		}
		if err := l.set.validate(l.name); err != nil {
			ps = append(ps, Problem{Path: path + "." + l.name, Message: err.Error()})
		}
	}
	return ps
}

// validateAgentModels checks agent.models and the token limits, which hold
// whether or not a budget is on (the pin rules are CheckPins).
func validateAgentModels(a Agent) []Problem {
	var ps []Problem
	for _, m := range []struct{ path, val string }{
		{"agent.model", a.Model}, {"agent.models.coder", a.Models.Coder},
		{"agent.models.reviewer", a.Models.Reviewer}, {"agent.models.background", a.Models.Background},
	} {
		switch {
		case len(m.val) > 100:
			ps = append(ps, Problem{Path: m.path, Message: "must be at most 100 characters"})
		case strings.ContainsFunc(m.val, unicode.IsSpace):
			ps = append(ps, Problem{Path: m.path, Message: "must not contain whitespace"})
		}
	}
	return append(ps, validateAgentTokens(a)...)
}

// validateAgentTokens checks the token limits, which are policy keys.
func validateAgentTokens(a Agent) []Problem {
	var ps []Problem
	for _, t := range []struct {
		path string
		n    int64
	}{{"agent.max_output_tokens.coder", a.MaxOutputTokens.Coder}, {"agent.max_output_tokens.reviewer", a.MaxOutputTokens.Reviewer}} {
		if t.n < 0 || t.n > MaxOutputTokensLimit {
			ps = append(ps, Problem{Path: t.path, Message: fmt.Sprintf("must be between 0 and %d (0 is no limit)", MaxOutputTokensLimit)})
		}
	}
	if a.MaxRunTokens < 0 {
		ps = append(ps, Problem{Path: "agent.max_run_tokens", Message: "must not be negative (0 is none)"})
	}
	return ps
}

// validateBudget checks fugaro.yaml's budget: block. It follows the project
// config's rules for the mode and the cap's range; whether enforce has a cap
// is decided after the merge, so a repository may set enforce and leave the
// cap to the owner.
func validateBudget(b *Budget) []Problem {
	if b == nil {
		return nil
	}
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if b.Mode != "" && !policy.ValidMode(b.Mode) {
		add("budget.mode", "%q must be off, observe or enforce", b.Mode)
	}
	if m, err := pricing.FromUSD(b.PerRunUSD); err != nil {
		add("budget.per_run_usd", "%v: it must be a number from 0 to %d US dollars", b.PerRunUSD, pricing.MaxUSD)
	} else if b.PerRunUSD > 0 && m < 1 {
		add("budget.per_run_usd", "%v: it rounds to nothing; the smallest cap is $0.000001", b.PerRunUSD)
	}
	if m, err := pricing.FromUSD(b.PerDayUSD); err != nil {
		add("budget.per_day_usd", "%v: it must be a number from 0 to %d US dollars", b.PerDayUSD, pricing.MaxUSD)
	} else if b.PerDayUSD > 0 && m < 1 {
		add("budget.per_day_usd", "%v: it rounds to nothing; the smallest cap is $0.000001", b.PerDayUSD)
	}
	if b.AllowedModels != nil && len(b.AllowedModels) == 0 {
		// The layers below still apply to a key this one leaves out, so an
		// omitted list means "the ceiling's stands", never "nothing is
		// restricted" (H1, L-a of the security review of PR #379).
		add("budget.allowed_models", "must list at least one model (an empty list, or one of only nulls, would forbid every model); leave it out to keep the allow-list the ceiling already has")
	}
	for i, m := range b.AllowedModels {
		path := fmt.Sprintf("budget.allowed_models[%d]", i)
		if msg := CheckModelID(m); msg != "" {
			add(path, "%s", msg)
		}
	}
	if i, ok := DuplicateModel(b.AllowedModels); ok {
		add(fmt.Sprintf("budget.allowed_models[%d]", i), "%s is listed twice; list each model once", b.AllowedModels[i])
	}
	return ps
}

// DuplicateModel is the index of the first entry of models that repeats an
// earlier one. fugaro.yaml and the project config share it.
func DuplicateModel(models []string) (int, bool) {
	seen := map[string]bool{}
	for i, m := range models {
		if seen[m] {
			return i, true
		}
		seen[m] = true
	}
	return 0, false
}

// CheckModelID is the reason m is not usable in an allow-list of models, or
// "" when it is: an explicit model ID, not an alias. fugaro.yaml and the
// project config share it.
func CheckModelID(m string) string {
	switch {
	case m == "":
		return "must not be empty"
	case len(m) > 100:
		return "must be at most 100 characters"
	case strings.ContainsFunc(m, unicode.IsSpace):
		return "must not contain whitespace"
	case pricing.IsAlias(m):
		return fmt.Sprintf("%s is an alias: name an explicit model ID such as claude-sonnet-5-5", m)
	}
	return ""
}

// ReservedClaudeAccountName reports whether name is the one account name the
// credentials bundle reserves for a credential of the platform's own
// (reservedClaudeAccountName, claude_accounts.go): an account spoken by it
// would leave one name meaning two credentials.
//
// It is exported for the surfaces outside this package that carry an account
// name — a task spec's oauth_token (internal/task) — so each of them asks the
// same predicate instead of keeping a copy of the name. The surfaces inside
// this package (claude_accounts' own keys, a profile's claude_accounts entry)
// call reservedClaudeAccountName directly.
//
// It is declared at the end of this file rather than beside the Validate
// switch it serves, so that adding it moves no line above it: several docs
// cite this file by line.
func ReservedClaudeAccountName(name string) bool { return reservedClaudeAccountName(name) }

// ReservedClaudeAccountNameMessage is the one sentence every surface refuses
// the reserved name with (reservedAccountNameMessage): an operator reads the
// same words whichever file they wrote the name in, and the sentence never
// echoes the name, which is untrusted text printed to a terminal.
const ReservedClaudeAccountNameMessage = reservedAccountNameMessage
