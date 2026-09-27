// Package config loads and validates a repository's fugaro.yaml (design §5.1).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is a parsed fugaro.yaml.
type Config struct {
	Version   int                 `yaml:"version"`
	Git       Git                 `yaml:"git"`
	Agent     Agent               `yaml:"agent"`
	Workflows map[string]Workflow `yaml:"workflows"`
}

// Git describes the repository's git provider and pull request settings.
type Git struct {
	Provider   string     `yaml:"provider"`
	BaseBranch string     `yaml:"base_branch"`
	PR         PRSettings `yaml:"pr"`
}

// PRSettings are applied to every pull request Fugaro opens.
type PRSettings struct {
	Labels    []string `yaml:"labels"`
	Reviewers []string `yaml:"reviewers"`
}

// Agent configures the Claude Code agent.
type Agent struct {
	Auth         string  `yaml:"auth"`
	Model        string  `yaml:"model"`
	ReviewRounds int     `yaml:"review_rounds"`
	MaxBudgetUSD float64 `yaml:"max_budget_usd"`
	Instructions string  `yaml:"instructions"`
	Review       string  `yaml:"review"`
}

// Workflow is one buildable unit of the repository, such as a server or a web app.
type Workflow struct {
	Base       string       `yaml:"base"`
	Image      Image        `yaml:"image"`
	Dockerfile string       `yaml:"dockerfile"`
	Commands   Commands     `yaml:"commands"`
	Cache      []CacheEntry `yaml:"cache"`
	Secrets    []Secret     `yaml:"secrets"`
	Resources  Resources    `yaml:"resources"`
	Timeouts   Timeouts     `yaml:"timeouts"`
}

// Image customizes the workflow's generated derived image (design §7.2). The
// zero value means the base image's defaults.
type Image struct {
	Node  string   `yaml:"node"`  // web-node: Node.js version, N or N.N.N
	JDK   string   `yaml:"jdk"`   // server-jvm: JDK major version
	Apt   []string `yaml:"apt"`   // extra system packages, installed as root
	Setup []string `yaml:"setup"` // extra RUN steps, run as fugaro in /work/repo after the warm-up
}

// IsZero reports whether the image block sets nothing.
func (i Image) IsZero() bool {
	return i.Node == "" && i.JDK == "" && len(i.Apt) == 0 && len(i.Setup) == 0
}

// Commands are the repository's build and test commands, run through `sh -c`.
type Commands struct {
	Build       string       `yaml:"build"`
	Test        string       `yaml:"test"`
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

// ReservedSecrets are the logical secret names the platform itself mounts,
// mapped to the variable each one becomes. A workflow's secrets may not
// reuse them (design §5.1, §6.1).
var ReservedSecrets = map[string]string{
	"bitbucket-token":    "FUGARO_BITBUCKET_TOKEN",
	"github-app-key":     "FUGARO_GITHUB_APP_PRIVATE_KEY",
	"claude-oauth-token": "CLAUDE_CODE_OAUTH_TOKEN",
	"anthropic-api-key":  "ANTHROPIC_API_KEY",
}

// Resources are the Cloud Run task resources for the workflow's job.
type Resources struct {
	CPU    int    `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// Timeouts bound a run (design §4.5).
type Timeouts struct {
	Total           Duration `yaml:"total"`
	Stage           Duration `yaml:"stage"`
	Verify          Duration `yaml:"verify"`
	FinalizeReserve Duration `yaml:"finalize_reserve"`
}

// Duration is a time.Duration written in Go syntax, such as "90m" or "1h30m".
type Duration struct{ time.Duration }

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q (use Go syntax such as 90m or 1h30m)", n.Line, n.Value)
	}
	d.Duration = v
	return nil
}

// Problem is one reason a fugaro.yaml is invalid.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

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
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "file is empty"}}
		}
		return nil, yamlProblems(err)
	}
	applyDefaults(&c)
	if ps := Validate(&c); len(ps) > 0 {
		return nil, ps
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

func problemFromYAML(msg string) Problem {
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
