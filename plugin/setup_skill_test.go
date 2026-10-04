package plugin_test

// Tests of the setup skill's content (plan T10): the claims it must make and
// the examples it must keep true. The generic safety rules (owner commands,
// forbidden phrases, command and flag existence, sizes, headers) are the
// lint's, in skills_lint_test.go.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// setupFiles returns every file of the setup skill, SKILL.md first.
func setupFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	paths, err := filepath.Glob(filepath.Join("skills", "setup", "reference", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append([]string{filepath.Join("skills", "setup", "SKILL.md")}, paths...)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(p)] = string(data)
	}
	if len(files) < 3 {
		t.Fatalf("the setup skill has %d files, want SKILL.md and its reference files", len(files))
	}
	return files
}

// TestSetupSkillMentionsDoctorFirst: `fugaro doctor --json` is the first
// fugaro command the skill gives, before any validate, build or render, and
// the skill says what a missing installation means.
func TestSetupSkillMentionsDoctorFirst(t *testing.T) {
	text := setupFiles(t)["skills/setup/SKILL.md"]
	cmdRE := regexp.MustCompile("`fugaro ([a-z]+(?: [a-z]+)?)")
	first := ""
	for _, m := range cmdRE.FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(m[1], "version") || strings.HasPrefix(m[1], "config") {
			continue
		}
		first = m[1]
		break
	}
	if !strings.HasPrefix(first, "doctor") {
		t.Errorf("the first fugaro command of SKILL.md is %q, want doctor", first)
	}
	if !strings.Contains(text, "`fugaro doctor --json`") {
		t.Error("SKILL.md does not give `fugaro doctor --json`")
	}
	// Init is an owner command: named for the user in a user-runs block only.
	if !regexp.MustCompile("(?s)no installation.{0,300}```bash user-runs\nfugaro init\n```").MatchString(text) {
		t.Error("SKILL.md does not say that no installation means the user runs init first, in a user-runs block")
	}
}

// TestSetupSkillNeverAppliesInit: `fugaro init` appears only as a plan
// (--plan-only, --print-vars, --help) outside a user-runs block, never with a
// flag that confirms for the user, and the user-runs blocks name init only as
// the user's own step.
func TestSetupSkillNeverAppliesInit(t *testing.T) {
	initRE := regexp.MustCompile(`fugaro init\b[^\n]*`)
	yesRE := regexp.MustCompile(`(^|\s)(--yes|-y|--non-interactive|--allow-delete|--forget|--allow-job-delete)(\s|$|\x60)`)
	sawPlan := false
	for name, text := range setupFiles(t) {
		inUserRuns, inFence := false, false
		for i, line := range strings.Split(text, "\n") {
			if tl := strings.TrimSpace(line); strings.HasPrefix(tl, "```") {
				inFence = !inFence
				inUserRuns = inFence && strings.Contains(tl, "user-runs")
				continue
			}
			if m := yesRE.FindString(line); m != "" && strings.Contains(line, "fugaro init") && !inUserRuns {
				t.Errorf("%s:%d: init near %q", name, i+1, strings.TrimSpace(m))
			}
			for _, cmd := range initRE.FindAllString(line, -1) {
				planOnly := strings.Contains(cmd, "--plan-only") || strings.Contains(cmd, "--print-vars") || strings.Contains(cmd, "--help")
				// In prose a bare mention ("run `fugaro init` first") names the
				// user's step; in a block it would be an instruction to the agent.
				if inFence && !inUserRuns && !planOnly {
					t.Errorf("%s:%d: an applying init in a block that is not user-runs: %s", name, i+1, cmd)
				}
				if strings.Contains(cmd, "--plan-only") && strings.Contains(cmd, "--repo") {
					sawPlan = true
				}
			}
		}
	}
	if !sawPlan {
		t.Error("the skill never gives `fugaro init --repo --plan-only`")
	}
}

// TestSetupSkillExamplesValidate: every fugaro.yaml example in the skill
// parses and validates with the repository's own loader, and the examples
// cover each base kind and both ways of building an image.
func TestSetupSkillExamplesValidate(t *testing.T) {
	bases := map[string]bool{}
	var image, dockerfile bool
	n := 0
	for name, text := range setupFiles(t) {
		for _, ex := range yamlExamples(text) {
			n++
			c, problems := config.Parse([]byte(ex.body))
			if len(problems) > 0 {
				t.Errorf("%s:%d: example does not validate: %v", name, ex.line, problems)
				continue
			}
			for _, w := range c.Workflows {
				bases[w.Base] = true
				image = image || !w.Image.IsZero()
				dockerfile = dockerfile || w.Dockerfile != ""
			}
		}
	}
	if n == 0 {
		t.Fatal("the setup skill has no fugaro.yaml example")
	}
	for _, b := range config.Bases {
		if !bases[b] {
			t.Errorf("no fugaro.yaml example for base kind %s", b)
		}
	}
	if !image || !dockerfile {
		t.Errorf("examples must show both image: (%v) and dockerfile: (%v)", image, dockerfile)
	}
}

type yamlExample struct {
	line int
	body string
}

// yamlExamples returns the fenced blocks labelled fugaro.yaml.
func yamlExamples(text string) []yamlExample {
	var out []yamlExample
	var cur *yamlExample
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case cur == nil && strings.HasPrefix(t, "```") && slices.Contains(strings.Fields(t), "fugaro.yaml"):
			cur = &yamlExample{line: i + 1}
		case cur != nil && strings.HasPrefix(t, "```"):
			out = append(out, *cur)
			cur = nil
		case cur != nil:
			cur.body += line + "\n"
		}
	}
	return out
}

// TestSetupSkillContent: the claims the plan's T10 requires, by the words
// that carry them. A rewrite that drops one fails here.
func TestSetupSkillContent(t *testing.T) {
	all := ""
	for _, text := range setupFiles(t) {
		all += text + "\n"
	}
	for _, want := range []string{
		"fugaro doctor --json",
		"fugaro secrets ls --json",
		"fugaro image render",
		"fugaro image build --local --json",
		"fugaro validate --json",
		"fugaro init --repo --plan-only",
		".fugaro/<workflow>.Dockerfile",
		"blank Dockerfile",
		"one topic at a time",
		"followup.allow_public",
		"image.setup",
		"fugaro-services start",
		"is data",
		"merge it, then run init",
		"Docker daemon",
		".github/workflows",
		"TEST_SERVICES=local",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the setup skill never says %q", want)
		}
	}
}
