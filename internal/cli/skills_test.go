package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/watch"
)

// Which skill commands and flags exist is checked by the plugin's lint
// (plugin/skills_lint_test.go), which builds this package's root command. The
// field claims live here, beside the unexported structs they describe.

// jsonFieldClaim is a field a skill reads from a command's --json output.
type jsonFieldClaim struct {
	command string // the fugaro command, for the message
	typ     any    // the value its JSON is encoded from
	path    string // dotted; [] marks a list: "smoke.checks[].ok"
}

// skillJSONFields lists every JSON field the skills read. A skill that reads
// a new one adds it here; a renamed field fails this test. `image build
// --local --json` is not one any more: a skill never builds locally (the
// images skill's own rule, F11), and the setup skill's fallback build is Cloud
// Build's, whose report is text and whose record is read with `image status
// --json`.
var skillJSONFields = []jsonFieldClaim{
	{"validate", validateOutput{}, "valid"},
	{"validate", validateOutput{}, "problems"},
	{"validate", validateOutput{}, "problems[].path"},
	{"validate", validateOutput{}, "problems[].message"},
	{"validate", validateOutput{}, "warnings"},
	{"validate", validateOutput{}, "warnings[].path"},
	{"validate", validateOutput{}, "warnings[].message"},
	// The images skill reads the survey: per repository the signals it found
	// and whether it could be read at all, and its `setup` member — the
	// catalog, the map and the bake switch — which is what a rerun proposes
	// its differences against (F16).
	{"image survey", imageSurveyOut{}, "project"},
	{"image survey", imageSurveyOut{}, "repos"},
	{"image survey", imageSurveyOut{}, "repos[].repo"},
	{"image survey", imageSurveyOut{}, "repos[].provider"},
	{"image survey", imageSurveyOut{}, "repos[].unread"},
	{"image survey", imageSurveyOut{}, "repos[].signals"},
	{"image survey", imageSurveyOut{}, "setup"},
	{"image survey", imageSurveyOut{}, "setup.catalog"},
	{"image survey", imageSurveyOut{}, "setup.image_map"},
	{"image survey", imageSurveyOut{}, "setup.bake_repos"},
	{"image survey", imageSurveyOut{}, "setup.unread"},
	// The catalog both image skills read: the entry a job runs, its tags and
	// the digest the last build of it recorded.
	{"image ls", catalogLsOut{}, "entries"},
	{"image ls", catalogLsOut{}, "entries[].name"},
	{"image ls", catalogLsOut{}, "entries[].tags"},
	{"image ls", catalogLsOut{}, "entries[].default"},
	{"image ls", catalogLsOut{}, "entries[].source"},
	{"image ls", catalogLsOut{}, "entries[].description"},
	{"image ls", catalogLsOut{}, "entries[].digest"},
	// The setup skill reads a derived image's record back after the user's
	// Cloud Build, whose own report is text.
	{"image status", imageStatusOut{}, "workflows"},
	{"image status", imageStatusOut{}, "workflows[].repo"},
	{"image status", imageStatusOut{}, "workflows[].workflow"},
	{"secrets ls", []secretEntry{}, "[].name"},
	{"secrets ls", []secretEntry{}, "[].id"},
	{"secrets ls", []secretEntry{}, "[].versions"},
	{"secrets ls", []secretEntry{}, "[].latest"},
	{"secrets ls", []secretEntry{}, "[].missing"},
	{"config layer", configLayerDoc{}, "where"},
	{"config layer", configLayerDoc{}, "generation"},
	{"config layer", configLayerDoc{}, "sha256"},
	{"config layer", configLayerDoc{}, "yaml"},
	{"recipes ls", recipeRow{}, "use_when"},
}

func init() {
	add := func(cmd string, typ any, paths ...string) {
		for _, p := range paths {
			skillJSONFields = append(skillJSONFields, jsonFieldClaim{cmd, typ, p})
		}
	}
	add("run", launchResult{}, "run", "repo", "run_id", "branch", "execution", "log_url", "status", "previous_run")
	add("runs ls", lsDoc{}, "project", "runs", "totals", "warnings",
		"runs[].run", "runs[].repo", "runs[].status", "runs[].stage", "runs[].reason", "runs[].halt", "runs[].pr_url",
		"runs[].branch", "runs[].outcome", "runs[].created", "runs[].cost", "runs[].cost.route_by", "runs[].cost.reported_usd",
		"runs[].terminal", "runs[].settled", "runs[].base_branch", "runs[].stale_draft", "runs[].draft_fallback", "runs[].pr_status_at",
		"runs[].launched_by", "runs[].failure_class",
		// The state of the pull request a row carries, which the working
		// skill's status reference reads to say which runs wait on a merge,
		// and the number itself, which the landing skill reads to tell one
		// pull request's runs from another's.
		"runs[].pr",
		"runs[].pr_state")
	// The working skill's status reference sends a reader to --group-by and
	// names the block it adds, so the block's own key is held to lsDoc's --
	// beside the rows, never instead of them, and only when a grouping was
	// asked for.
	add("runs ls", lsDoc{}, "groups")
	add("diagnose", Diagnosis{}, "row", "row.pr_url", "row.reason", "row.cost.route_by", "row.cost.reported_usd",
		"row.base_branch",
		"row.project_layer", "row.project_layer.sha256", "row.project_layer.generation", "row.project_layer.applied", "row.config_sha256",
		"row.oauth_account", "halt", "verify", "failed", "flaky", "findings", "agent_message",
		"log_tail", "draft_note", "report_path", "follow_up", "comments_path", "route_by", "reported_usd",
		// The landing skill weighs a land against a follow-up from the
		// record: the review rounds with their verdicts, and the findings
		// the resolved mode let through rather than fixing.
		"reviews", "tolerated",
		// The checkpoint a governance halt leaves (T6.2, D329): the working
		// skill's diagnose reference reads all six of its fields, the runner's
		// three and the three the stopping agent answers.
		"checkpoint", "checkpoint.trigger", "checkpoint.why_over", "checkpoint.estimate_usd",
		"checkpoint.done", "checkpoint.next", "checkpoint.hard_instructions")
	// The working skill's status reference names the two money fields of a
	// runs stats group, so they are checked against the row that carries them.
	add("runs stats", userStatsDoc{}, "rows[].user", "rows[].counted_usd", "rows[].notional_usd")
	add("logs", logLine{}, "time", "severity", "stage", "stream", "event", "message")
	add("cancel", cancelResult{}, "project", "run", "status", "hard", "pr")
	// The orchestrator skill tells a session where the revision a patch is
	// built from comes from, so that field is held to the document it prints.
	add("orchestrator state", orchestratorStateDoc{}, "rev")
	add("watch --once", watch.BuildJSON("p", watch.View{}), "project")
}

// hasJSONField reports whether the JSON encoding of t has the dotted path.
// Embedded structs are flattened, as encoding/json does; a "[]" in the path
// steps into a slice's element.
func hasJSONField(t reflect.Type, path string) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if path == "" {
		return true
	}
	head, rest, _ := strings.Cut(path, ".")
	list := strings.HasSuffix(head, "[]")
	head = strings.TrimSuffix(head, "[]")
	if head == "" { // a leading "[]": the root is a list
		return t.Kind() == reflect.Struct && hasJSONField(t, rest)
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	ft, ok := jsonFieldType(t, head)
	if !ok {
		return false
	}
	if list && ft.Kind() != reflect.Slice {
		return false
	}
	return hasJSONField(ft, rest)
}

func jsonFieldType(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
			if ft, ok := jsonFieldType(f.Type, name); ok {
				return ft, true
			}
			continue
		}
		if tag == "-" || !f.IsExported() {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		if tag == name {
			return f.Type, true
		}
	}
	return nil, false
}

// TestLintJSONFieldsExist fails when a field a skill reads from --json output
// is not in the structs that output is encoded from (design §4.6 test 4).
func TestLintJSONFieldsExist(t *testing.T) {
	for _, c := range skillJSONFields {
		if !hasJSONField(reflect.TypeOf(c.typ), c.path) {
			t.Errorf("fugaro %s --json: no field %q in %T", c.command, c.path, c.typ)
		}
	}
	// The lint itself: a field that is not there, and a path through a
	// non-list, are caught.
	for _, bad := range []string{"validity", "problems[].nope", "valid[].x", "smoke.checks[].ok"} {
		if hasJSONField(reflect.TypeOf(validateOutput{}), bad) {
			t.Errorf("%q reported present in validateOutput", bad)
		}
	}
	if !hasJSONField(reflect.TypeOf(lsDoc{}), "runs[].status") {
		t.Error("runs[].status reported absent from lsDoc")
	}
}

// notFields are backticked tokens on a line about JSON that are not field
// names: values, files and keys of other documents.
var notFields = []string{
	"fugaro.yaml", "yarn.lock", "commands.test", "git.base_branch", "gcp_project", "mise.toml", // files and fugaro.yaml keys
	"engines.node",      // a package.json key, not a fugaro field
	"tool_error",        // a value of a log entry's event
	"claude_accounts",   // a local-config key (never in --json output: it is operator-only and never published)
	"agent.oauth_token", // a fugaro.yaml key, not a JSON output field
	// The two IAM lists a machine's own project config holds: `fugaro user
	// ls --json` names its own copies run_operators and admins, and the
	// working skill names the config keys to say what `user add` writes.
	"terraform.operators", "terraform.admins",
	"base_rev", // a key of the patch document a session sends, not a field of any --json output
}

var fieldTokenRE = regexp.MustCompile("`([a-z][a-z0-9]*(?:(?:_[a-z0-9]+)+|(?:\\.[a-z][a-z0-9_]*(?:\\[\\])?)+)(?:\\.[a-z][a-z0-9_]*)*)`")

// skillTexts returns every markdown file under plugin/skills, by path.
func skillTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join("..", "..", "plugin", "skills"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		out[path] = string(data)
		return err
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	return out
}

// TestSkillJSONClaimsMatchTheTable: a field name in backticks on a line that
// talks about JSON is a claim and must be in the table, so a renamed field
// fails here and a claim cannot be added without being checked; and every
// table row is read by some skill, so the table does not outlive the skills.
func TestSkillJSONClaimsMatchTheTable(t *testing.T) {
	known := map[string]bool{}
	leaves := map[string]bool{}
	for _, c := range skillJSONFields {
		p := strings.ReplaceAll(c.path, "[]", "")
		known[p] = true
		for _, part := range strings.Split(p, ".") {
			if part != "" {
				known[part] = true
			}
		}
		leaves[p[strings.LastIndex(p, ".")+1:]] = true
	}
	all := ""
	for path, text := range skillTexts(t) {
		all += text
		for i, line := range strings.Split(text, "\n") {
			if !strings.Contains(strings.ToLower(line), "json") {
				continue
			}
			for _, m := range fieldTokenRE.FindAllStringSubmatch(line, -1) {
				tok := strings.ReplaceAll(m[1], "[]", "")
				ext := tok[strings.LastIndex(tok, ".")+1:]
				if slices.Contains(notFields, tok) || slices.Contains([]string{"json", "md", "yaml", "yml", "go", "sh", "txt"}, ext) {
					continue
				}
				if !known[tok] {
					t.Errorf("%s:%d: `%s` is read from JSON output but is not in skillJSONFields", path, i+1, m[1])
				}
			}
		}
	}
	for _, c := range skillJSONFields {
		leaf := c.path[strings.LastIndexAny(c.path, ".]")+1:]
		if leaf != "" && !regexp.MustCompile(`\b`+regexp.QuoteMeta(leaf)+`\b`).MatchString(all) {
			t.Errorf("fugaro %s --json: %q is in the table but no skill mentions %q", c.command, c.path, leaf)
		}
	}
	// The check itself: a renamed field is not known.
	if known["cost.total_usd_renamed"] || !known["row.cost.route_by"] {
		t.Error("the table does not tell a renamed field from a real one")
	}
}

// TestProjectLayerSkillClaimsRealFields: fieldTokenRE only catches field
// names shaped with an underscore or a dot (TestSkillJSONClaimsMatchTheTable
// above), so it cannot catch a bad rename of a bare single-word field like
// `yaml` or `sha256`. A review on this PR found exactly that kind of bug:
// the setup skill's "Does the project publish a project layer?" step
// (plan Task 19) claimed `profiles` as a top-level field of `fugaro config
// layer --json`, when configLayerDoc (config_show.go) has only where,
// generation, sha256 and yaml — profiles and default_profile are keys
// inside the yaml field's text, not of the envelope. This checks that
// step's claims directly against configLayerDoc, on both sides: a rename in
// the skill's prose (the backtick-quoted word disappears from the section)
// or in the struct's json tag (hasJSONField no longer finds it) each fail
// here independently of skillJSONFields / TestLintJSONFieldsExist.
func TestProjectLayerSkillClaimsRealFields(t *testing.T) {
	skill := setupSkillMD(t)
	start := strings.Index(skill, "### Does the project publish a project layer?")
	end := strings.Index(skill, "## 2. Investigate the repository")
	if start < 0 || end < 0 || end < start {
		t.Fatal("SKILL.md's project-layer step moved or was renamed; update this test")
	}
	section := skill[start:end]
	for _, c := range skillJSONFields {
		if c.command != "config layer" {
			continue
		}
		if !strings.Contains(section, "`"+c.path+"`") {
			t.Errorf("the project-layer step no longer names `%s`; if config layer --json dropped it, remove it from skillJSONFields too", c.path)
		}
		if !hasJSONField(reflect.TypeOf(configLayerDoc{}), c.path) {
			t.Errorf("the project-layer step claims `%s` for fugaro config layer --json, but configLayerDoc has no such field", c.path)
		}
	}
	// profiles and default_profile are real project-layer keys, but of the
	// embedded yaml text, never of the --json envelope: the step must say
	// so (with a trailing colon, the convention this file uses for a YAML
	// key rather than a JSON field), and configLayerDoc must stay flat.
	for _, key := range []string{"profiles", "default_profile"} {
		if !strings.Contains(section, "`"+key+":`") {
			t.Errorf("the project-layer step does not read `%s:` from the yaml field", key)
		}
		if hasJSONField(reflect.TypeOf(configLayerDoc{}), key) {
			t.Errorf("configLayerDoc now has a top-level %q field; the step's yaml-field caveat is stale", key)
		}
	}
}

// setupSkillMD returns the setup skill's SKILL.md text.
func setupSkillMD(t *testing.T) string {
	t.Helper()
	for path, text := range skillTexts(t) {
		if strings.HasSuffix(path, filepath.Join("setup", "SKILL.md")) {
			return text
		}
	}
	t.Fatal("skills/setup/SKILL.md not found")
	return ""
}
