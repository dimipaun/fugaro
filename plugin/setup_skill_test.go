package plugin_test

// Tests of the setup skill's content (plan T10): the claims it must make and
// the examples it must keep true. The generic safety rules (owner commands,
// forbidden phrases, command and flag existence, sizes, headers) are the
// lint's, in skills_lint_test.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
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
	if !regexp.MustCompile("(?s)no installation.{0,300}```bash user-runs\nfugaro setup( --project <name>)?\n```").MatchString(text) {
		t.Error("SKILL.md does not say that no installation means the user runs setup first, in a user-runs block")
	}
}

// TestSetupSkillOpensBySayingWhichCommandItIs: this skill runs AFTER the
// wizard, and its opening line has to say so — a reader who has not run the
// first half must learn it here, in one sentence: the cloud side is already
// done, and this skill writes `fugaro.yaml` and opens the pull request.
//
// The line names the door as "the `setup` door" and not as a command line, and
// the last assertion is why: the plugin lint (skills_lint_test.go,
// ownerViolation) rejects an applying `fugaro setup` or `fugaro update`
// anywhere outside a user-runs block, prose and "never" sentences included, so
// the command itself appears in exactly one place in this file — step 1's
// user-runs block — and the introduction points at the door by name.
//
// Mutation (run, restore): delete the opening sentence -- "which command it
// is" fails; change "the wizard did it" to name the command line instead --
// "the introduction gives a fugaro command" fails.
func TestSetupSkillOpensBySayingWhichCommandItIs(t *testing.T) {
	text := setupFiles(t)["skills/setup/SKILL.md"]
	intro, _, ok := strings.Cut(text, "\nGround rules:")
	if !ok {
		t.Fatal("SKILL.md has no `Ground rules:` list: this test reads the introduction above it")
	}
	for _, want := range []string{"cloud side is already done", "wizard", "writes `fugaro.yaml`", "opens the pull request"} {
		if !strings.Contains(intro, want) {
			t.Errorf("SKILL.md's introduction never says %q: the skill runs after the wizard, and its opening line has to say which of the two commands it is", want)
		}
	}
	if m := regexp.MustCompile("`fugaro [a-z]").FindString(intro); m != "" {
		t.Errorf("SKILL.md's introduction gives a fugaro command (%q): the agent's first fugaro command is `fugaro doctor --json` in step 1, and the door the user already ran is named, not quoted, because the plugin lint refuses an applying door outside a user-runs block", m)
	}
}

// TestSetupSkillNeverApplies: `fugaro setup` and `fugaro update` appear only
// as a plan (--plan-only, --print-vars, --help) outside a user-runs block,
// never with a flag that confirms for the user, and the user-runs blocks name
// them only as the user's own step. Both doors apply — setup creates and
// update re-converges — so both are the user's to run, and the read-only plan
// the agent may run is `fugaro update --repo --plan-only`.
func TestSetupSkillNeverApplies(t *testing.T) {
	initRE := regexp.MustCompile(`fugaro (?:setup|update)\b[^\n]*`)
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
			if m := yesRE.FindString(line); m != "" && (strings.Contains(line, "fugaro setup") || strings.Contains(line, "fugaro update")) && !inUserRuns {
				t.Errorf("%s:%d: a door near %q", name, i+1, strings.TrimSpace(m))
			}
			for _, cmd := range initRE.FindAllString(line, -1) {
				planOnly := strings.Contains(cmd, "--plan-only") || strings.Contains(cmd, "--print-vars") || strings.Contains(cmd, "--help")
				// In prose a bare mention ("run `fugaro setup` first") names the
				// user's step; in a block it would be an instruction to the agent.
				if inFence && !inUserRuns && !planOnly {
					t.Errorf("%s:%d: an applying command in a block that is not user-runs: %s", name, i+1, cmd)
				}
				if strings.Contains(cmd, "--plan-only") && strings.Contains(cmd, "--repo") {
					sawPlan = true
				}
			}
		}
	}
	if !sawPlan {
		t.Error("the skill never gives `fugaro update --repo --plan-only`")
	}
}

// TestSetupSkillExamplesValidate: every fugaro.yaml example in the skill
// parses and validates with the repository's own loader, and the examples
// cover both ways of naming runtimes (image.tools and a repository
// mise.toml) and both ways of building an image (image: and dockerfile:).
// "Every workflow builds on the one Fugaro base" has no separate check here:
// config.Parse (above) already refuses any workflow with a non-empty base:
// (legacyBaseMessage, internal/config/validate.go), returning a nil *Config
// and only problems, which the n == 0 / "does not validate" paths already
// fail on — a loop-local check of Workflow.BaseKind(), which is hardcoded to
// always return config.BaseKind regardless of Base (internal/config/config.go),
// ran on a Config that can only ever reach this point with Base == "" for
// every workflow, can never fail either way and was dead weight.
func TestSetupSkillExamplesValidate(t *testing.T) {
	var tools, plain, dockerfile bool
	n := 0
	for name, text := range setupFiles(t) {
		for _, ex := range yamlExamples(text) {
			n++
			c, problems := config.Parse([]byte(ex.body))
			if len(problems) > 0 {
				t.Errorf("%s:%d: example does not validate: %v", name, ex.line, problems)
				continue
			}
			mise := precedingMiseToml(text, ex.line)
			checkExample(t, fmt.Sprintf("%s:%d", name, ex.line), c, mise)
			for wfName, w := range c.Workflows {
				switch {
				case len(w.Image.Tools) > 0:
					tools = true
				case w.Dockerfile == "":
					// An example with neither image.tools nor dockerfile:
					// relies on a repository mise.toml, so it must actually
					// have one: a missing fence here is a silent "it just
					// works" example with nothing pinning its runtime, not
					// one of the two ways of naming them.
					if mise == "" {
						t.Errorf("%s:%d: workflow %s has no image.tools and no preceding ```toml mise.toml``` block: nothing pins its runtime", name, ex.line, wfName)
					} else {
						plain = true
					}
				}
				dockerfile = dockerfile || w.Dockerfile != ""
			}
		}
	}
	if n == 0 {
		t.Fatal("the setup skill has no fugaro.yaml example")
	}
	if !tools {
		t.Error("no fugaro.yaml example sets image.tools")
	}
	if !plain {
		t.Error("no fugaro.yaml example relies on a repository mise.toml instead of image.tools")
	}
	if !dockerfile {
		t.Error("no fugaro.yaml example sets dockerfile:")
	}
}

var dockerfilePathRE = regexp.MustCompile(`^\.fugaro/[a-z0-9][a-z0-9-]*\.Dockerfile$`)

// checkExample runs the validator's repository checks (config.Check) on an
// example against a scratch checkout holding what the example refers to: the
// ./scripts its commands run, a package.json and lockfile when the example
// sets image.skip_build_scripts (config.Check's checkBaseWorkflow refuses it
// without a Node package manager to detect), the example's mise.toml (the
// fenced ```toml mise.toml``` block immediately before this yaml example, if
// any), and a Dockerfile rendered from the template for a dockerfile:
// workflow. The path rule of the skill (.fugaro/<workflow>.Dockerfile) is not
// the validator's, so it is checked here.
func checkExample(t *testing.T, at string, c *config.Config, mise string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, data string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if mise != "" {
		write("mise.toml", mise)
	}
	for name, w := range c.Workflows {
		checkNoRuntimeApt(t, at, name, w)
		for _, cmd := range []string{w.Commands.Build, w.Commands.Test} {
			if f := strings.Fields(cmd); len(f) > 0 && strings.HasPrefix(f[0], "./") {
				write(f[0], "#!/bin/sh\n")
			}
		}
		if w.Image.SkipBuildScripts {
			write("package.json", "{}")
			write("package-lock.json", "{}")
		}
		if w.Dockerfile != "" {
			if !dockerfilePathRE.MatchString(w.Dockerfile) {
				t.Errorf("%s: workflow %s: dockerfile %q is not .fugaro/<workflow>.Dockerfile", at, name, w.Dockerfile)
			}
			df, err := image.Render(image.RenderInput{Workflow: name, Base: config.BaseKind, Version: "1.0.0"})
			if err != nil {
				t.Fatal(err)
			}
			write(w.Dockerfile, string(df))
		}
	}
	if problems := config.Check(c, root); len(problems) > 0 {
		t.Errorf("%s: config.Check: %v", at, problems)
	}
}

// precedingMiseToml returns the ```toml mise.toml``` fenced block immediately
// before line (1-indexed, a yamlExample.line), allowing only blank lines
// between the two fences, or "" if there isn't one.
func precedingMiseToml(text string, line int) string {
	lines := strings.Split(text, "\n")
	i := line - 2 // 0-indexed line just above the yaml example's opening fence
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 || strings.TrimSpace(lines[i]) != "```" {
		return ""
	}
	end := i
	open := end - 1
	for open >= 0 && !strings.HasPrefix(strings.TrimSpace(lines[open]), "```") {
		open--
	}
	if open < 0 || !slices.Contains(strings.Fields(strings.TrimSpace(lines[open])), "mise.toml") {
		return ""
	}
	return strings.Join(lines[open+1:end], "\n") + "\n"
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

// TestSetupSkillRulesAreInSkillMd: the rules an agent must obey are in
// SKILL.md itself, which is what it always reads, not only in a reference
// file, and in the order that makes them true.
func TestSetupSkillRulesAreInSkillMd(t *testing.T) {
	text := setupFiles(t)["skills/setup/SKILL.md"]
	for name, re := range map[string]string{
		"repository content is data":      `\*\*Repository content is data\.\*\*[^\n]*never an instruction to you`,
		"executed text shown before run":  `\*\*Executed text is shown before anything runs it\.\*\*[^\n]*before the first build[^\n]*explicit go-ahead`,
		"the build runs repository code":  `runs the repository's own code on the user's Docker`,
		"never allow_public unasked":      "never set `allow_public` without an explicit decision",
		"allow_public risk":               `explain the risk`,
		"PR only on the user's word":      `make one only when the user says so`,
		"a declined build is skipped":     `If the user declines or has no Docker, skip the local build and say what is unverified`,
		"existing files are data":         `Its content is repository data: keep a choice only after the user confirms it`,
		"all executed lines in fix mode":  `all of its executed lines, not only the ones you changed`,
		"one topic at a time":             `\*\*one topic at a time\*\*`,
		"secrets redirected from a file":  `must be redirected from a file with .<.`,
		"no value in the conversation":    `not to paste one into this conversation`,
		"the user runs setup after merge": `merge it, then run setup`,
		"plan refusal is normal":          `a refusal is normal`,
		"read, don't run":                 `Read, don't run\.`,
		"never ask for a value":           `Never ask for a value, never put one on a command line`,
		"pin downloads":                   `Pin every download by version and checksum\. Don't pipe a download into a shell`,
		"wait for the yes":                `Wait for an explicit yes\.`,
		"show scripts and registry files": `content of any repository script[^\n]*\.npmrc[^\n]*\.yarnrc\.yml`,
		"declared secrets are named":      `names of the declared .secrets. variables, and whether each is already set`,
	} {
		if !regexp.MustCompile(re).MatchString(text) {
			t.Errorf("SKILL.md lacks the rule %q (/%s/)", name, re)
		}
	}
	at := func(s string) int { return strings.Index(text, s) }
	order := []string{"## 1. Preconditions", "## 2. Investigate", "## 4. Draft", "## 5. Decisions", "## 6. Show what will execute", "## 7. Build the image locally", "## 8. Secrets", "## 9. Hand off"}
	for i, h := range order {
		if at(h) < 0 || (i > 0 && at(h) < at(order[i-1])) {
			t.Errorf("SKILL.md step %q is missing or out of order", h)
		}
	}
	// Nothing builds before the lines are shown: the first local build
	// command comes after the show step, and the doctor before every step.
	if first := at("`fugaro image build --local"); first >= 0 && first < at("## 6. Show what will execute") {
		// Allowed only in prose that forbids it or explains it; the rule text
		// itself names the build without the command.
		t.Errorf("SKILL.md gives `fugaro image build --local` before the show-before-build step")
	}
	if at("`fugaro doctor --json`") > at("## 2. Investigate") {
		t.Error("SKILL.md does not give doctor in step 1")
	}
}

// TestSetupSkillContent: each claim is in the file that carries it, so a
// phrase deleted from one file is not hidden by the same words in another.
func TestSetupSkillContent(t *testing.T) {
	files := setupFiles(t)
	for file, wants := range map[string][]string{
		"skills/setup/SKILL.md": {
			"fugaro doctor --json", "fugaro secrets ls --repo --json", "fugaro image render", "fugaro image build --local --json",
			"fugaro validate --json", "fugaro update --repo --plan-only", ".fugaro/<workflow>.Dockerfile", "blank Dockerfile",
			".github/workflows", "Docker daemon", "`fugaro_yaml`", "WSL2", "agent.instructions",
			"whole content", "_.source", "[hooks]",
			"https://github.com/dimipaun/fugaro/blob/main/docs/base-image-migration.md",
		},
		"skills/setup/reference/services-and-images.md": {
			"fugaro-services start", "mise.toml", "mise install", "image.tools", "apt-get install", "image.setup", "save the output as `.fugaro/<workflow>.Dockerfile`", "Pin every download by version and checksum", "Never start from a blank Dockerfile", "does **not** enforce", "illustrative", "Docker daemon", "--platform linux/arm64", "FUGARO_SERVICES",
			"**Never** `apt-get install <language>` or a `curl | sh` installer for a runtime",
			"https://github.com/dimipaun/fugaro/blob/main/docs/base-image-migration.md",
		},
		"skills/setup/reference/decisions.md": {
			"followup.allow_public", "agent.max_run_tokens", "agent.max_budget_usd", "fugaro budget prices", "never set `allow_public` without their explicit decision",
			"own terminal", "## 9. `agent.recipe`", "Only the people the user names", "list only the people the user names", "recommend asking the user for a number", "`vertex` supports `observe` only", "Ask the user whether the repository is public", "never from CODEOWNERS",
		},
		"skills/setup/reference/validation.md": {
			"root-scan", "managed-settings-dir", "mise-cache-dir", "runs the repository's code", "Don't have the token put into your own environment", "visible to the repository's code in the build and to every command your agent runs",
		},
		"skills/setup/reference/discovery.md": {
			"16 GB on a public one", "GOTOOLCHAIN", "go.mod", "must be a plain version",
			"https://github.com/dimipaun/fugaro/blob/main/docs/base-image.md",
			"No pin anywhere", "pom.xml", "rust-toolchain.toml",
		},
	} {
		text, ok := files[file]
		if !ok {
			t.Errorf("%s is missing", file)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s never says %q", file, want)
			}
		}
	}
}

var (
	// runRepoCmdRE: an instruction to run the repository's own commands for
	// discovery. Discovery reads files; running them runs repository code
	// before the user has seen it.
	runRepoCmdRE = regexp.MustCompile("(?i)\\b(run|execute|invoke)\\b[^.\\n]*`(npm (ci|install|run|test)|yarn( install| run)?|pnpm (install|run)|make|\\./gradlew[^`]*|go (build|test|generate))\\b")
	// recommendOffRE: a recommendation to turn the budget off.
	recommendOffRE = regexp.MustCompile("(?i)\\b(recommend|propose|suggest|leave)\\b[^.\\n]*(mode: off|budget off)")
	capRE          = regexp.MustCompile(`per_run_usd:?\s*\$?(\d+)`)
)

// TestSetupSkillDoesNotRunRepoCommandsOrLoosenBudget: no instruction to run
// the repository's commands while investigating, no recommendation to switch
// the budget off, and no recommended cap above a sane bound.
func TestSetupSkillDoesNotRunRepoCommandsOrLoosenBudget(t *testing.T) {
	files := setupFiles(t)
	skill := files["skills/setup/SKILL.md"]
	step2 := skill[strings.Index(skill, "## 2. Investigate"):strings.Index(skill, "## 3. ")]
	for name, text := range map[string]string{"SKILL.md step 2": step2, "discovery.md": files["skills/setup/reference/discovery.md"]} {
		if m := runRepoCmdRE.FindString(text); m != "" {
			t.Errorf("%s tells the agent to run a repository command: %q", name, m)
		}
	}
	for name, text := range files {
		if m := recommendOffRE.FindString(text); m != "" {
			t.Errorf("%s recommends turning the budget off: %q", name, m)
		}
		for _, m := range capRE.FindAllStringSubmatch(text, -1) {
			if n, _ := strconv.Atoi(m[1]); n > 100 {
				t.Errorf("%s: per_run_usd %d is above the sane bound of 100", name, n)
			}
		}
	}
}

// TestSetupSkillSplitsMachineSizeAndReviewRounds: live Check 27 saw one
// combined question with bundled options. They are two topics, each with its
// own evidence and recommendation.
func TestSetupSkillSplitsMachineSizeAndReviewRounds(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	topicRE := func(s string) bool { return regexp.MustCompile(s).MatchString(skill) }
	if !topicRE("(?m)^\\d+\\. `resources` \\(machine size\\)[^\\n]*CI configuration[^\\n]*test footprint[^\\n]*language defaults") {
		t.Error("SKILL.md has no topic for `resources` with its own evidence (CI configuration, test footprint, language defaults)")
	}
	if !topicRE("(?m)^\\d+\\. `agent.review_rounds`: its own question, from how risky or critical") {
		t.Error("SKILL.md has no topic for `agent.review_rounds` with its own evidence (how risky or critical)")
	}
	if !strings.Contains(skill, "Never ask 7 and 8 together or bundle their options") {
		t.Error("SKILL.md does not forbid asking machine size and review rounds together")
	}
	for _, h := range []string{"## 7. `resources` (machine size)", "## 8. `agent.review_rounds`"} {
		if !strings.Contains(dec, h) {
			t.Errorf("decisions.md has no section %q", h)
		}
	}
	if strings.Contains(dec, "## 6. `review_rounds`, `resources`") {
		t.Error("decisions.md still joins review_rounds and resources in one topic")
	}
}

// TestSetupSkillAsksRecipe: the recipe is its own topic, after the models and
// review rounds; the 0.5.0 warning comes before any suggestion; the user
// decides, nothing is guessed, and default is the recommendation.
func TestSetupSkillAsksRecipe(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	if !regexp.MustCompile("(?m)^9\\. `agent.recipe`: its own question").MatchString(skill) {
		t.Error("SKILL.md has no topic 9 for agent.recipe")
	}
	at := func(s, sub string) int { return strings.Index(s, sub) }
	if !(at(skill, "3. `agent.models`") < at(skill, "8. `agent.review_rounds`") && at(skill, "8. `agent.review_rounds`") < at(skill, "9. `agent.recipe`")) {
		t.Error("SKILL.md does not order topics 3, 8, 9")
	}
	if !(at(dec, "## 3. `agent.models`") < at(dec, "## 8. `agent.review_rounds`") && at(dec, "## 8. `agent.review_rounds`") < at(dec, "## 9. `agent.recipe`") && at(dec, "## 9. `agent.recipe`") < at(dec, "## 10. `rebuild`")) {
		t.Error("decisions.md does not order sections 3, 8, 9, 10")
	}
	for name, doc := range map[string]string{"SKILL.md": skill[at(skill, "9. `agent.recipe`"):], "decisions.md": dec[at(dec, "## 9. `agent.recipe`"):]} {
		w, r, o := at(doc, "0.5.0"), at(doc, "recommend `default`"), at(doc, "Offer")
		if name == "decisions.md" {
			r = at(doc, "Then recommend `default`")
		}
		if w < 0 || r < 0 || o < 0 || !(w < r && w < o) {
			t.Errorf("%s: the 0.5.0 warning must come before the recommendation and the offer", name)
		}
		for _, want := range []string{"without ", "explicit decision", "never guess a recipe from the repository's domain or language"} {
			if !strings.Contains(strings.ToLower(doc[:min(len(doc), 2500)]), strings.ToLower(want)) {
				t.Errorf("%s never says %q", name, want)
			}
		}
	}
	for _, want := range []string{"`default`", "`standard`", "`solo`", "fugaro recipes ls", "leave `agent.recipe` out", "turns it on for a provider coder with a non-provider reviewer"} {
		if !strings.Contains(dec, want) {
			t.Errorf("decisions.md never says %q", want)
		}
	}
	for _, gone := range []string{"for a cheap provider coder", "for a team with one model", "unless the models", "fit one of the others", "call for another"} {
		if strings.Contains(skill, gone) || strings.Contains(dec, gone) {
			t.Errorf("trigger wording %q is back", gone)
		}
	}
	if !strings.Contains(skill, "Never ask 7 and 8 together or bundle their options") {
		t.Error("the machine size and review rounds rule moved")
	}
}

// TestSetupSkillVerifiesBeforeRecommending: a recommendation that depends on
// a fact is made only after the fact is checked; vertex is never recommended
// unchecked, and every recommendation says why.
func TestSetupSkillVerifiesBeforeRecommending(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	for name, re := range map[string]string{
		"the general rule":             `\*\*Verify before you recommend\.\*\*[^\n]*do not mark the option recommended`,
		"the rule asks for the reason": `Say why each recommendation is the recommendation`,
	} {
		if !regexp.MustCompile(re).MatchString(skill) {
			t.Errorf("SKILL.md lacks %s (/%s/)", name, re)
		}
	}
	for _, want := range []string{
		"never mark `vertex` recommended on a guess", "could not verify it", "`oauth` when the user says they have a subscription",
		"`api-key` when the team wants pay-per-token dollar caps", "Claude on Vertex AI is verified usable",
		"give the reason for each role", "because it writes the change", "never recommend a model only because it is the biggest",
	} {
		if !strings.Contains(dec, want) {
			t.Errorf("decisions.md never says %q", want)
		}
	}
	// The vertex row must not recommend on an unchecked premise.
	if strings.Contains(dec, "has Claude on Vertex AI enabled |") {
		t.Error("decisions.md recommends vertex on an unchecked premise")
	}
}

// githubAppFacts are what the skill and docs must say about the GitHub App;
// the permissions are derived from the code that mints the token.
func githubAppPermissions(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "internal", "gitprov", "github", "apptoken.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)var tokenPermissions = map\[string\]string\{(.*?)\n\}`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("tokenPermissions not found in apptoken.go")
	}
	var out []string
	for _, kv := range regexp.MustCompile(`"(\w+)":\s*"(\w+)"`).FindAllStringSubmatch(m[1], -1) {
		name := map[string]string{"contents": "Contents", "pull_requests": "Pull requests", "issues": "Issues", "metadata": "Metadata"}[kv[1]]
		if name == "" {
			t.Fatalf("unknown permission %q in apptoken.go: update the skill and docs", kv[1])
		}
		access := map[string]string{"write": "Read & write", "read": "Read"}[kv[2]]
		out = append(out, name+" "+access)
	}
	if len(out) != 4 {
		t.Fatalf("apptoken.go asks for %d permissions, the skill says four", len(out))
	}
	return out
}

// TestSetupSkillGitHubApp: the App is named <yourname>-fugaro (names are
// globally unique), has the four repository permissions and never Workflows,
// is installed on the repository, and its permission changes are accepted;
// the ID is asked early.
func TestSetupSkillGitHubApp(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	perms := githubAppPermissions(t)
	for _, p := range perms {
		if !strings.Contains(dec, p) {
			t.Errorf("decisions.md does not list the App permission %q", p)
		}
		if !strings.Contains(skill, p) {
			t.Errorf("SKILL.md does not list the App permission %q", p)
		}
	}
	for name, text := range files {
		if regexp.MustCompile("(?i)(suggest|use|call|name)[^.\\n]*the (bare )?name `Fugaro`[^.\\n]*(App|token)").MatchString(text) && !strings.Contains(text, "is taken") {
			t.Errorf("%s suggests naming the App `Fugaro`", name)
		}
	}
	for _, want := range []string{"`<yourname>-fugaro`", "the bare `Fugaro` is taken", "Never Workflows", "accept the change on the installation", "Install it on this repository", "What is the GitHub App ID", "not a secret"} {
		if !strings.Contains(dec, want) {
			t.Errorf("decisions.md never says %q", want)
		}
	}
	for _, want := range []string{"`<yourname>-fugaro`", "never Workflows", "accept the change on the installation", "Install it on this repository", "--github-app-id <id>"} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md never says %q", want)
		}
	}
	// The App ID is asked in the decisions, before the secrets and the plan.
	if i, j := strings.Index(skill, "the GitHub App: its ID"), strings.Index(skill, "## 8. Secrets"); i < 0 || i > j {
		t.Error("SKILL.md does not ask for the GitHub App ID before the secrets and the hand-off")
	}
	if !strings.HasPrefix(strings.SplitN(dec[strings.Index(dec, "## 1."):], "\n", 2)[0], "## 1. The GitHub App") {
		t.Error("the GitHub App ID is not the first topic of decisions.md")
	}
}

// TestDocsGitHubAppGuidance: the docs say the same four permissions and the
// same name guidance as the skill.
func TestDocsGitHubAppGuidance(t *testing.T) {
	perms := githubAppPermissions(t)
	for _, doc := range []string{"docs/git-providers.md", "docs/gcp-setup.md", "README.md"} {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(doc)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, p := range perms {
			// git-providers.md writes "**Contents: Read & write**", the others "Contents Read & write".
			colon := strings.Replace(p, " Read", ": Read", 1)
			if !strings.Contains(text, p) && !strings.Contains(text, colon) {
				t.Errorf("%s does not list the App permission %q", doc, p)
			}
		}
		if !strings.Contains(text, "<yourname>-fugaro") {
			t.Errorf("%s does not tell the user to name the App <yourname>-fugaro", doc)
		}
		if !strings.Contains(text, "Workflows") {
			t.Errorf("%s does not say never to grant Workflows", doc)
		}
		if !strings.Contains(text, "accept") {
			t.Errorf("%s does not say to accept permission changes on the installation", doc)
		}
	}
}

// TestSetupSkillHandlesSeveralProjects: with several project configs and no
// fugaro.yaml, doctor fails with an error the skill must act on; the quoted
// words are the CLI's own, and the skill asks, then passes --project.
func TestSetupSkillHandlesSeveralProjects(t *testing.T) {
	skill := setupFiles(t)["skills/setup/SKILL.md"]
	src, err := os.ReadFile(filepath.Join("..", "internal", "localcfg", "select.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"several project configs", "nothing selects one"} {
		if !strings.Contains(string(src), phrase) {
			t.Fatalf("localcfg no longer says %q: update the skill", phrase)
		}
		if !strings.Contains(skill, phrase) {
			t.Errorf("SKILL.md does not recognise the error %q", phrase)
		}
	}
	for _, want := range []string{
		"Ask the user which Fugaro project this repository belongs to", "don't open the config directory",
		"exactly as the user confirmed it", "refuse any that holds a space, a quote or a shell character",
		"`FUGARO_PROJECT=<name> fugaro validate --json`", "`FUGARO_PROJECT=<name> fugaro config example`",
		// The setup blocks below the paragraph carry `--project <name>` in
		// their `user-runs` fences, and passing it is only theirs to do when a
		// project had to be chosen here. The sentence that says so keeps an
		// agent from making the user select a project twice; a trim dropped it
		// once, so it is pinned rather than left to the next trim.
		"drop `--project <name>` below when no project had to be chosen",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md never says %q", want)
		}
	}
	// The commands the skill says take --project do, and the ones it gives
	// the environment form do not: derived from the cobra flags so it can't drift.
	m := regexp.MustCompile("the commands that take `--project` \\(([^)]*)\\)[^;]*; the others \\(([^)]*)\\)").FindStringSubmatch(skill)
	if m == nil {
		t.Fatal("SKILL.md does not list the commands that take --project and the ones that do not")
	}
	root := cli.NewRootCmd()
	listed := func(list string) (out []string) {
		for _, c := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(list, -1) {
			out = append(out, c[1])
		}
		return
	}
	hasFlag := func(name string) bool {
		c, _, err := root.Find(strings.Fields(name))
		if err != nil || c == root {
			t.Fatalf("the skill names a command %q that does not exist", name)
		}
		return c.Flags().Lookup("project") != nil
	}
	for _, c := range listed(m[1]) {
		if !hasFlag(c) {
			t.Errorf("SKILL.md says %q takes --project, but it has no such flag", c)
		}
	}
	for _, c := range listed(m[2]) {
		if hasFlag(c) {
			t.Errorf("SKILL.md says %q takes no --project, but it has the flag", c)
		}
	}
	if strings.Contains(skill, "`--project <name>` to every fugaro command") {
		t.Error("SKILL.md says every fugaro command takes --project")
	}
	if !strings.Contains(skill, "fugaro setup --project <name>") {
		t.Error("the setup blocks do not show --project")
	}
}

// TestSetupSkillRecommendationAnchors: wording mutants that would let a
// recommendation through unchecked.
func TestSetupSkillRecommendationAnchors(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	for _, want := range []string{
		"checked with a read-only command you may run, or stated by the user",
		"Recommend the credential the evidence supports; `vertex` only once the facts are verified",
		"The App's four repository permissions are Contents Read & write",
		"Pull requests Read & write, Issues Read and Metadata Read, and never Workflows",
		"skipped, with a one-line confirmation",
	} {
		if !strings.Contains(skill, want) && !strings.Contains(dec, want) {
			t.Errorf("the skill never says %q", want)
		}
	}
	if strings.Contains(dec, "`vertex` | none (the job's own account) | the installation's cloud project has") {
		t.Error("vertex is recommended by default")
	}
	if !regexp.MustCompile(`(?m)^1\. On a GitHub repository only, the GitHub App`).MatchString(skill) {
		t.Error("the App-ID topic is not first in SKILL.md step 5")
	}
}

// TestSkillsSayOwnTerminal: init, secrets set and a cloud image build go to
// the user's own terminal window, never through the agent, and never with a
// ! prefix (the lint also checks the prefix and each user-runs lead-in).
func TestSkillsSayOwnTerminal(t *testing.T) {
	files := setupFiles(t)
	skill := files["skills/setup/SKILL.md"]
	for _, want := range []string{"in your own terminal window, not through the agent", "A `!` shell inside Claude Code is not a terminal"} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md never says %q", want)
		}
	}
	if n := strings.Count(skill, "in their own terminal window, not through the agent"); n < 3 {
		t.Errorf("SKILL.md says 'in their own terminal window, not through the agent' %d times, want it at each user-runs lead-in", n)
	}
	if !strings.Contains(files["skills/setup/reference/validation.md"], "in their own terminal window, not through the agent") {
		t.Error("validation.md does not say the cloud image build runs in the user's own terminal window")
	}
}

func TestSetupSkillUsesTheProjectLayer(t *testing.T) {
	files := setupFiles(t)
	skill := files["skills/setup/SKILL.md"]
	for _, want := range []string{
		"fugaro config layer --json",
		"fugaro config init --yes",
		"fugaro config show --json",
		"the investigative path",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md does not mention %q", want)
		}
	}
	// The layer check comes after doctor (TestSetupSkillMentionsDoctorFirst
	// keeps doctor first).
	if strings.Index(skill, "fugaro doctor --json") > strings.Index(skill, "fugaro config layer --json") {
		t.Error("SKILL.md checks the project layer before doctor")
	}
	if !strings.Contains(files["skills/setup/reference/decisions.md"], "## Minimal file or full file?") {
		t.Error("decisions.md has no section on the minimal file")
	}
}

// TestSetupSkillOffersClone (generic-tool Task 18): the setup skill offers
// the checkout decision instead of leaving every workflow on baked. The topic
// names both values, says clone needs no system package and no Dockerfile,
// and says which value is the default.
func TestSetupSkillOffersClone(t *testing.T) {
	files := setupFiles(t)
	skill, dec := files["skills/setup/SKILL.md"], files["skills/setup/reference/decisions.md"]
	// The topic's own section, not the whole file: decisions.md is one
	// paragraph per topic, and the words these claims are made of appear in
	// other topics on main ("leave the key out" in topic 2 at line 32,
	// "refused"/"refuses" in topics 4, 6 and 9) — so a whole-file search
	// passes whether or not topic 12 makes the claim.
	topic := decisionsTopic(t, dec, "`checkout`")
	for _, want := range []string{
		"`checkout: clone`",
		"`checkout: baked`",
		"needs no system package and no Dockerfile",
		"the default is `baked`",
		"the user's decision",
	} {
		if !strings.Contains(topic, want) {
			t.Errorf("decisions.md's checkout topic never says %q", want)
		}
	}
	if !strings.Contains(skill, "`checkout`") {
		t.Error("SKILL.md never mentions the checkout decision")
	}
	// The honesty claim, inverted by the cloneShipped flip (0.9.0): the
	// skill must not keep telling a user that an option which works is
	// unavailable. Both files say clone is shipped, and neither says it is
	// not.
	for name, doc := range map[string]string{"decisions.md": topic, "SKILL.md": skill} {
		if !strings.Contains(doc, "is shipped") {
			t.Errorf("%s never says checkout: clone is shipped", name)
		}
		if strings.Contains(doc, "not shipped") {
			t.Errorf("%s still says checkout: clone %q", name, "not shipped")
		}
	}
	// The gate, and its effect, must be named: this is the claim the flip
	// changed. The topic names cloneShipped, where it lives, and that the
	// gate is open — the state a reader has to be able to check against the
	// constant. It does not name the two commands the gate used to refuse:
	// the plugin lint (skills_lint_test.go, ownerViolation) rejects an
	// applying `fugaro init` in skill prose, and the claim is the gate's
	// state, not the command line it is read on.
	for _, want := range []string{"cloneShipped", "internal/cli/recipes_skew.go", "is open"} {
		if !strings.Contains(topic, want) {
			t.Errorf("decisions.md's checkout topic never says %q", want)
		}
	}
}

// decisionsTopic returns the body of the `## N. <name>` topic in a
// decisions.md, from its heading to the next `## ` heading. Callers that
// assert on a topic's words must search the topic, not the file: several
// topics say the same things, so a whole-file search can pass on another
// topic's prose.
func decisionsTopic(t *testing.T, dec, name string) string {
	t.Helper()
	re := regexp.MustCompile("(?m)^## \\d+\\. " + regexp.QuoteMeta(name) + "[^\n]*\n")
	m := re.FindStringIndex(dec)
	if m == nil {
		t.Fatalf("decisions.md has no %s topic", name)
	}
	rest := dec[m[1]:]
	if end := regexp.MustCompile("(?m)^## ").FindStringIndex(rest); end != nil {
		return rest[:end[0]]
	}
	return rest
}

// TestSetupSkillSecretsStepReadsMissingFromSecretsLs pins step 8's sentence
// about the MISSING signal: it comes from `fugaro secrets ls --repo --json` run in
// the checkout (only the checkout's fugaro.yaml says what is required), not
// from doctor, and it is the names this repository's jobs need — so "store
// each" never sends the user after a name no job of it reads.
func TestSetupSkillSecretsStepReadsMissingFromSecretsLs(t *testing.T) {
	const want = "From the checkout, run `fugaro secrets ls --repo --json` (not doctor, which lists only stored names): " +
		"each entry with `missing` set is a name this repository's jobs need that has no value, so store each of those."
	text := setupFiles(t)["skills/setup/SKILL.md"]
	if !strings.Contains(text, want) {
		t.Errorf("SKILL.md step 8 does not say where the missing names come from:\nwant %q", want)
	}
	if strings.Contains(text, "It flags every required name") {
		t.Error("SKILL.md still has the sentence whose pronoun binds to doctor")
	}
}

// TestSetupSkillSaysWhoWritesTheManagedMarks (round-2 review, nit 2): a
// managed project's two marks are written by two operator-run steps — the run
// that creates the project (managed.WriteManagedLabel) and the owner's
// flagging command for one that already exists
// (managed.WriteManagedLabelForFlag, which exists because the creation write
// refuses a project no run created) — so the sentence listing the marks may
// not hand both of them to the creating run alone, and the skill and its
// reference have to keep naming the existing-project route that writes them
// together. Both directions are pinned: the false sentence coming back fails
// the first check, and dropping the flag's own claim fails the second.
func TestSetupSkillSaysWhoWritesTheManagedMarks(t *testing.T) {
	skill := setupFiles(t)["skills/setup/SKILL.md"]
	if strings.Contains(skill, "both written by the creating run") {
		t.Error("SKILL.md gives both managed marks to the creating run alone, and the owner's flagging command writes both too")
	}
	reference := setupFiles(t)["skills/setup/reference/managed.md"]
	const both = "It writes the label `fugaro_managed=true` on the project as well, in the same command"
	if !strings.Contains(reference, both) {
		t.Errorf("reference/managed.md does not say the flagging command writes the label too:\nwant %q", both)
	}
}

// TestSetupSkillSaysManagedIsGatedOff (round-3 review, medium): both the setup
// skill and its managed reference describe the managed workflow — the apply,
// the flag, the creation — and every one of those entry points refuses before
// any cloud call in this release, because `managedDeployerShipped`
// (internal/cli/recipes_skew.go) is false. A skill that presents the mode as
// the working path sends the user's agent at a workflow that cannot run, and
// the plugin's own convention for a gate is to state its state where the
// feature is described: decisions.md's checkout topic says `cloneShipped` is
// open, pinned by TestSetupSkillOffersClone. So both files must name the
// constant, where it lives, that it is false, the refusal a user will see, and
// when the gate opens: the redesign is planned for 0.9.6, by the owner's ruling
// of 2026-10-20, which moved it off 0.9.5 (the ruling of 2026-10-19 had moved
// it off 0.9.0).
// The release name is the half a reader acts on: without it, "disabled in this
// release" reads as a temporary state of the tree rather than as 0.9.0's own
// behaviour, which docs/gcp-setup.md:622, docs/design/managed-projects.md:3 and
// internal/cli/recipes_skew.go all name as 0.9.6.
//
// Mutations (each verified, planted and reverted): drop the gate paragraph from
// reference/managed.md, and change SKILL.md's "is false" to "is open" — both
// fail here. Dropping "planned for 0.9.6" from either file fails it too.
func TestSetupSkillSaysManagedIsGatedOff(t *testing.T) {
	for name, doc := range map[string]string{
		"SKILL.md":             setupFiles(t)["skills/setup/SKILL.md"],
		"reference/managed.md": setupFiles(t)["skills/setup/reference/managed.md"],
	} {
		for _, want := range []string{
			"managedDeployerShipped",
			"internal/cli/recipes_skew.go",
			"is false",
			"managed projects are disabled in this release",
			"planned for 0.9.6",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s never says %q: the managed mode's gate is closed in this release (managedDeployerShipped is false, internal/cli/recipes_skew.go), so the skill has to say where the gate stands beside the workflow it describes", name, want)
			}
		}
	}
}
