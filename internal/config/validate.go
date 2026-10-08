package config

import (
	"errors"
	"fmt"
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
	if !validBranchName(c.Git.BaseBranch) {
		add("git.base_branch", "must be a plain git branch name: letters, digits, '.', '_', '-' and '/', not starting with '-', '.' or '/', with no '..', '//', component starting with '.', or trailing '/', '.' or '.lock'")
	}
	if !slices.Contains([]string{"vertex", "api-key", "oauth"}, c.Agent.Auth) {
		add("agent.auth", "must be one of vertex, api-key, oauth")
	}
	if c.Agent.ReviewRounds < 1 || c.Agent.ReviewRounds > 10 {
		add("agent.review_rounds", "must be between 1 and 10")
	}
	if !slices.Contains([]string{FirstLineAuto, FirstLineOn, FirstLineOff}, c.Agent.FirstLineReview) {
		add("agent.first_line_review", "must be one of auto, on, off")
	}
	if c.Agent.FirstLineRounds < 1 || c.Agent.FirstLineRounds > MaxFirstLineRounds {
		add("agent.first_line_rounds", "must be between 1 and %d", MaxFirstLineRounds)
	}
	if c.Agent.Recipe != "" && !recipe.NameRE.MatchString(c.Agent.Recipe) {
		add("agent.recipe", "must be a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
	if c.Agent.MaxBudgetUSD < 0 {
		add("agent.max_budget_usd", "must not be negative")
	}
	ps = append(ps, validateAgentModels(c.Agent)...)
	ps = append(ps, validateBudget(c.Budget)...)
	ps = append(ps, validateFollowup(c.Git.Provider, c.Followup)...)
	if len(c.Workflows) == 0 {
		add("workflows", "must define at least one workflow")
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + name
		if !WorkflowNameRE.MatchString(name) {
			add(p, "workflow name must match %s", WorkflowNameRE)
		}
		if w.Profile != "" && !ProjectNameRE.MatchString(w.Profile) {
			add(p+".profile", "must be a profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
		if !slices.Contains(Bases, w.Base) {
			add(p+".base", "must be one of %s", strings.Join(Bases, ", "))
		}
		ps = append(ps, validateImage(p, w)...)
		if strings.TrimSpace(w.Commands.Build) == "" {
			add(p+".commands.build", "is required")
		}
		if strings.TrimSpace(w.Commands.Test) == "" {
			add(p+".commands.test", "is required")
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
		}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"finalize_reserve", t.FinalizeReserve}} {
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
	for _, msg := range LintDockerfile(data, w.Base) {
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
		p := "workflows." + name
		if w.Dockerfile != "" {
			ps = append(ps, checkDockerfile(p, root, w)...)
		} else if w.Base == "web-node" {
			// The generated image's warm-up needs a package manager it can name.
			if _, err := DetectNodePM(root); err != nil {
				ps = append(ps, Problem{Path: p, Message: err.Error()})
			}
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

// MaxOutputTokensLimit is the highest per-call output limit fugaro.yaml may
// set: the most any current model produces.
const MaxOutputTokensLimit = 128000

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
		add("budget.allowed_models", "must list at least one model (an empty list, or one of only nulls, would forbid every model); leave it out for no restriction")
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
