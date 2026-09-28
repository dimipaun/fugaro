package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	workflowNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	secretNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	envNameRE      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	memoryRE       = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)
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

// reservedEnvPrefixes are set by Fugaro itself or change git and Claude Code behavior.
var reservedEnvPrefixes = []string{"FUGARO_", "ANTHROPIC_", "CLAUDE_CODE_", "GIT_"}

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
	if c.Agent.MaxBudgetUSD < 0 {
		add("agent.max_budget_usd", "must not be negative")
	}
	if len(c.Workflows) == 0 {
		add("workflows", "must define at least one workflow")
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + name
		if !workflowNameRE.MatchString(name) {
			add(p, "workflow name must match %s", workflowNameRE)
		}
		if !slices.Contains([]string{"server-jvm", "web-node"}, w.Base) {
			add(p+".base", "must be one of server-jvm, web-node")
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
			if !secretNameRE.MatchString(s.Name) {
				add(sp+".name", "must be lower-case letters, digits and dashes")
			}
			if _, reserved := ReservedSecrets[s.Name]; reserved {
				add(sp+".name", "%s is reserved for the platform's own secrets", s.Name)
			}
			switch {
			case !envNameRE.MatchString(s.Env):
				add(sp+".env", "must be an upper-case environment variable name")
			case reservedEnv(s.Env):
				add(sp+".env", "%s uses a reserved prefix (%s)", s.Env, strings.Join(reservedEnvPrefixes, ", "))
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

// checkDockerfile reports a workflow's repository Dockerfile if it is
// missing or breaks the derived-image contract (LintDockerfile).
func checkDockerfile(p, root string, w Workflow) []Problem {
	data, err := os.ReadFile(filepath.Join(root, w.Dockerfile))
	if err != nil {
		return []Problem{{Path: p + ".dockerfile", Message: w.Dockerfile + " does not exist"}}
	}
	var ps []Problem
	for _, msg := range LintDockerfile(data, w.Base) {
		ps = append(ps, Problem{Path: p + ".dockerfile", Message: w.Dockerfile + " " + msg})
	}
	return ps
}

func reservedEnv(name string) bool {
	for _, prefix := range reservedEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
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
