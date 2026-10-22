package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestDocsCredentialsAuthListNamesCascade: agent.auth gained a fourth value
// (config.AuthCascade, plan Task C1.1), and the sweep that updated the code's
// copies of the list left prose copies behind — the credentials bundle's own
// reconciliation, whose §1.2 declared the list "three values, not four"; the
// README's fugaro.yaml example; the setup skill's decisions and its reference's
// §2 table; and the CLI's own printed template (internal/config/example.yaml).
// Each is a place a reader meets the list, and each contradicted the validator
// after this branch: a reader following them would not know the value exists at
// all, or would read the document that wins over spec.md and believe a cascade
// file is refused.
//
// The list is read from config.AgentAuths (internal/config/defaults.go) — the
// one list the validator, the layer's own defaults.agent.auth key and the
// launcher's check of a run's spec all read — rather than spelled out here, so
// a fifth value fails this test in all five documents at once rather than
// leaving them to be found by eye. The sixth section reads the other
// enumeration (the values CheckProviderAuth refuses with a provider model —
// every value but api-key) out of that function for the same reason: it is the
// rule docs/multi-model.md §5 and README.md state, and both said "oauth or
// vertex" until the cascade joined it.
//
// Mutation (run, restored): drop "cascade" from README.md's auth comment, from
// the setup skill's decision line, from the reference's §2 table, from
// internal/config/example.yaml's comment, or restore §1.2's "has three values,
// not four" heading — each fails, naming the document it reads. Drop it from
// README.md's `with a provider model` comment or from docs/multi-model.md §5's
// refusal bullet and the sixth section fails on the document it reads.
func TestDocsCredentialsAuthListNamesCascade(t *testing.T) {
	auths := quoted(sourceValues(t, "internal/config/defaults.go", readDoc(t, "internal/config/defaults.go"),
		`AgentAuths = \[\]string\{([^}]*)\}`)[0])
	if len(auths) < 4 {
		t.Fatalf("config.AgentAuths parsed as %v; this scan is no longer reading defaults.go", auths)
	}

	// 1. README.md's fugaro.yaml example: the comment after `auth:` is the list
	// a reader copying the example reads.
	readme := readDoc(t, "README.md")
	var readmeLine string
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "auth: oauth") && strings.Contains(line, "#") {
			readmeLine = line
			break
		}
	}
	if readmeLine == "" {
		t.Fatal("README.md's fugaro.yaml example carries no `auth: oauth` comment line: the list moved and this scan no longer reads it")
	}
	namesEveryAuth(t, "README.md's fugaro.yaml example", readmeLine, auths)

	// 2. The setup skill's own decision list, which is what the skill asks the
	// user to choose between.
	skill := readDoc(t, "plugin/skills/setup/SKILL.md")
	var decision string
	for _, line := range strings.Split(skill, "\n") {
		if strings.HasPrefix(line, "2. `agent.auth`:") {
			decision = line
			break
		}
	}
	if decision == "" {
		t.Fatal("plugin/skills/setup/SKILL.md carries no `2. `agent.auth`` decision line: the decision moved and this scan no longer reads it")
	}
	namesEveryAuth(t, "plugin/skills/setup/SKILL.md's agent.auth decision", decision, auths)

	// 3. The credentials bundle's reconciliation §1.2, which is the document
	// that wins over spec.md and which counted the values out loud.
	recon := readDoc(t, "docs/credentials-service/reconciliation.md")
	var heading, body string
	for _, line := range strings.Split(recon, "\n") {
		if strings.HasPrefix(line, "### 1.2 ") {
			heading = line
		}
		if heading != "" && body == "" && strings.Contains(line, "accepts exactly") {
			body = line
		}
	}
	if heading == "" {
		t.Fatal("docs/credentials-service/reconciliation.md carries no §1.2 heading: the section moved and this scan no longer reads it")
	}
	if strings.Contains(heading, "three values") {
		t.Errorf("docs/credentials-service/reconciliation.md's §1.2 heading still counts the values as three (%q), and the list is %d long (config.AgentAuths): the document that wins over spec.md would tell a reader the cascade value does not exist", heading, len(auths))
	}
	if body == "" {
		t.Fatal("docs/credentials-service/reconciliation.md's §1.2 no longer says what the validator accepts: the sentence moved and this scan no longer reads it")
	}
	namesEveryAuth(t, "docs/credentials-service/reconciliation.md's §1.2", body, auths)

	// 4. The CLI's own printed template, internal/config/example.yaml (embedded
	// as config.Example and printed by `fugaro config example`). It is the copy
	// of the list a reader meets when they create a config at all: error
	// messages send them here ("create it with /fugaro:setup or fugaro config
	// example"), so it is the one document that has to be right the first time.
	example := readDoc(t, "internal/config/example.yaml")
	var templateLine string
	for _, line := range strings.Split(example, "\n") {
		if strings.Contains(line, "auth: vertex") && strings.Contains(line, "#") {
			templateLine = line
			break
		}
	}
	if templateLine == "" {
		t.Fatal("internal/config/example.yaml carries no `auth: vertex` comment line: the template moved and this scan no longer reads it")
	}
	namesEveryAuth(t, "internal/config/example.yaml's agent.auth comment", templateLine, auths)

	// 5. The setup skill's reference on the decision — §2's own table, which is
	// the one place the per-value facts (which secret exists, whether a gateway
	// and its caps apply) are stated, and the place SKILL.md's decision line
	// sends the agent to ("this reference" for each topic's options and what to
	// recommend). A value missing from the table is a value the agent has no
	// facts for, and the one it would silently drop when the user's evidence
	// points at cascade.
	reference := readDoc(t, "plugin/skills/setup/reference/decisions.md")
	var table []string
	inTable := false
	for _, line := range strings.Split(reference, "\n") {
		if strings.HasPrefix(line, "## 2. `agent.auth`") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if len(table) > 0 {
				break
			}
			continue
		}
		table = append(table, line)
	}
	if len(table) == 0 {
		t.Fatal("plugin/skills/setup/reference/decisions.md carries no table under §2: the table moved and this scan no longer reads it")
	}
	rows := strings.Join(table, "\n")
	for _, auth := range auths {
		if !strings.Contains(rows, "| `"+auth+"` |") {
			t.Errorf("plugin/skills/setup/reference/decisions.md's §2 table has no row for %q (config.AgentAuths): a value the skill offers without its facts", auth)
		}
	}

	// 6. The *other* enumeration: the values refused with a provider model,
	// which is every value but api-key (config.CheckProviderAuth's own arms).
	// Two reader-facing copies of it stayed behind when the cascade was added —
	// docs/multi-model.md §5's list of refusals ("`agent.auth` is `oauth` or
	// `vertex`") and README.md's provider-key walkthrough ("oauth and vertex are
	// refused with a provider model") — and a reader who takes either for the
	// whole rule would read a cascade run with a provider model as admitted,
	// which the runner refuses on every launch.
	//
	// The set is read from CheckProviderAuth rather than spelled out: this list
	// is not config.AgentAuths (api-key is the value it admits), and a fifth
	// value is one it refuses by its default arm the day it exists.
	refused := refusedWithAProviderModel(t, auths)

	multi := readDoc(t, "docs/multi-model.md")
	var multiLine string
	for _, line := range strings.Split(multi, "\n") {
		if strings.HasPrefix(line, "- **`agent.auth` is") {
			multiLine = line
			break
		}
	}
	if multiLine == "" {
		t.Fatal("docs/multi-model.md §5 carries no `agent.auth is` refusal bullet: the list moved and this scan no longer reads it")
	}
	// The enumeration itself, and not the whole bullet: a document can name the
	// value in a clause beside the list — "a cascade run's own tier A" — while
	// leaving it out of the list, and a line-wide contains check reads that as
	// right. What has to carry every refused value is the list.
	multiEnum := between(multiLine, "`agent.auth` is ", ".")
	if multiEnum == multiLine {
		t.Fatalf("docs/multi-model.md §5's refusal bullet no longer spells the list after `agent.auth` is: %q", multiLine)
	}
	everyAuthQuoted(t, "docs/multi-model.md §5's provider-model refusal list", multiEnum, refused)

	var readmeProviderLine string
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "auth: api-key") && strings.Contains(line, "with a provider model") {
			readmeProviderLine = line
			break
		}
	}
	if readmeProviderLine == "" {
		t.Fatal("README.md carries no `auth: api-key … with a provider model` comment: the walkthrough moved and this scan no longer reads it")
	}
	// The comment's own list, for the same reason: the sentence it introduces
	// names the rule, and the list before it is what a reader copies.
	readmeEnum := between(readmeProviderLine, "# ", " are refused")
	if readmeEnum == readmeProviderLine {
		t.Fatalf("README.md's provider-model refusal comment no longer lists the values before `are refused`: %q", readmeProviderLine)
	}
	namesEveryAuth(t, "README.md's provider-model refusal comment", readmeEnum, refused)
}

// TestSetupSkillCascadeOfferIsPinnedToC119: the setup skill offers
// `agent.auth: cascade` — the value TestDocsCredentialsAuthListNamesCascade
// requires the skill's decision line and its reference's §2 table to carry,
// because it is on config.AgentAuths — and the value is a preview until plan
// Task C1.19's launch wiring is in a release. The validator knows it and the
// config reader accepts it (cascadeSince), and C1.19 is what teaches a runner
// to carry it (GatewayOn, the service upstream, the O2 switch); before that
// release reaches an installation's job image, a `cascade` file whose
// effective mode is on passes `fugaro validate` and a launch and then dies in
// the job, on the runner's own `unknown agent auth "cascade"` — the gap
// config.CheckCascadeGateway's comment states from the validator's side, and
// the reason the skill's offer cannot reach a release uncoupled from C1.19.
//
// The pin is the pair: this test reads the offer (the skill's decision line,
// without which there is nothing to pin), the plan's C1.19 task, and the release
// checklist's `cascadeSince` bullet — the line a release is verified against
// before it is tagged — and requires the two documents to name the offer. So
// the dependency cannot be dropped from either while the skill still offers the
// value, and the offer cannot be withdrawn from the skill while the documents
// still state what it owes.
//
// Mutation (run, restored): delete the paragraph naming plugin/skills/setup from
// the plan's C1.19 task, or the sentence naming it from docs/release.md's
// `cascadeSince` bullet, and the section reading it fails; drop `cascade` from
// the skill's decision line and the first section fails, naming the skill.
func TestSetupSkillCascadeOfferIsPinnedToC119(t *testing.T) {
	auth := config.AuthCascade

	// 1. The offer itself: the skill's decision line, the same one the test
	// above reads.
	skill := readDoc(t, "plugin/skills/setup/SKILL.md")
	var decision string
	for _, line := range strings.Split(skill, "\n") {
		if strings.HasPrefix(line, "2. `agent.auth`:") {
			decision = line
			break
		}
	}
	if decision == "" {
		t.Fatal("plugin/skills/setup/SKILL.md carries no `2. `agent.auth`` decision line: the offer moved and this scan no longer reads it")
	}
	if !strings.Contains(decision, "`"+auth+"`") {
		t.Fatalf("plugin/skills/setup/SKILL.md's decision line does not offer %q, so there is nothing for the documents below to owe: %q", auth, decision)
	}

	// 2. The plan's own task for the wiring: the work that turns the offer into
	// a value a run can use.
	plan := readDoc(t, "docs/credentials-service/implementation-plan.md")
	c119 := between(plan, "### Task C1.19", "### Task C1.20")
	if c119 == plan {
		t.Fatal("docs/credentials-service/implementation-plan.md carries no Task C1.19 block followed by C1.20: the task moved and this scan no longer reads it")
	}
	if !strings.Contains(c119, "plugin/skills/setup") {
		t.Errorf("the plan's Task C1.19 does not name plugin/skills/setup: the skill offers %q and this task is what makes the offer true, so a release can carry the offer with nothing recording what it owes", auth)
	}

	// 3. The release checklist's bullet for the value, which is what the
	// release is verified against before it is tagged (docs/release.md's
	// "Before you tag" list).
	release := readDoc(t, "docs/release.md")
	bullet := between(release, "- **Verify `cascadeSince`", "\n")
	if bullet == release || strings.TrimSpace(bullet) == "" {
		t.Fatal("docs/release.md carries no `Verify `cascadeSince`` bullet: the checklist moved and this scan no longer reads it")
	}
	if !strings.Contains(bullet, "plugin/skills/setup") || !strings.Contains(bullet, "C1.19") {
		t.Errorf("docs/release.md's `cascadeSince` bullet names the setup skill's %q offer and Task C1.19 together or not at all, and this bullet is missing one of them: the release it gates may carry the offer while no runner reads the value", auth)
	}
}

// between is the text after the first `after` and before the first `before`
// following it, or s itself when either marker is absent — a caller's way of
// telling "the sentence moved" from "the sentence says what it should".
func between(s, after, before string) string {
	i := strings.Index(s, after)
	if i < 0 {
		return s
	}
	rest := s[i+len(after):]
	j := strings.Index(rest, before)
	if j < 0 {
		return s
	}
	return rest[:j]
}

// everyAuthQuoted requires text to spell every auth value as a backticked code
// span: the enumeration these documents write in prose quotes each value.
func everyAuthQuoted(t *testing.T, where, text string, auths []string) {
	t.Helper()
	for _, auth := range auths {
		if !strings.Contains(text, "`"+auth+"`") {
			t.Errorf("%s does not name %q (config.AgentAuths): %q", where, auth, text)
		}
	}
}

// refusedWithAProviderModel is the set of agent.auth values
// config.CheckProviderAuth refuses when the run pins a model a provider serves
// — every value but api-key, which is the one it is for. It is computed from
// the function itself so that a fifth value, refused by the switch's default
// arm, has to be named in the two documents this scan reads.
func refusedWithAProviderModel(t *testing.T, auths []string) []string {
	t.Helper()
	ps := map[string]config.ModelProvider{"openrouter": {
		Kind: "openai", BaseURL: "https://openrouter.ai/api/v1",
		Auth: "bearer", Secret: "openrouter-api-key",
		Models: []string{"deepseek/*"},
	}}
	var refused []string
	for _, auth := range auths {
		a := config.Agent{Auth: auth, Models: config.ModelRoles{Coder: "deepseek/deepseek-v4-flash"}}
		if len(config.CheckProviderAuth(a, ps)) > 0 {
			refused = append(refused, auth)
		}
	}
	if len(refused) == 0 || len(refused) == len(auths) {
		t.Fatalf("config.CheckProviderAuth refused %d of %d values (config.AgentAuths, refused %v): this scan is no longer reading it", len(refused), len(auths), refused)
	}
	return refused
}

// namesEveryAuth requires text to carry every value of the auth list. It is a
// contains check per value rather than a parse of the enumeration: the
// documents spell the list four ways (a YAML comment in two files, a decision
// line, a sentence), and what this guard is for is the value left OUT, which
// it names.
func namesEveryAuth(t *testing.T, where, text string, auths []string) {
	t.Helper()
	for _, auth := range auths {
		if !strings.Contains(text, auth) {
			t.Errorf("%s does not name %q (config.AgentAuths): %q", where, auth, text)
		}
	}
}

// TestMultiModelDocumentsTheCascadeSwitch is Task C1.22's other half of the
// doc sweep: docs/multi-model.md §5 refuses a provider model under
// `agent.auth: cascade` and never says what the value *is* for. This pins the
// section that does, and it is checked against that section alone (docSection)
// so a claim that lives only in §5's refusal does not make it pass. The claims
// are the ones an author has to get right: the cascade is a credential switch
// that names no model of its own (D133, D150), it reaches a run only when the
// operator-written project policy turns it on (O2/O3), the repository's file
// is refused rather than ignored when the policy does not, and the runbook is
// where an operator goes when a run halts on it.
func TestMultiModelDocumentsTheCascadeSwitch(t *testing.T) {
	const path = "docs/multi-model.md"
	section := docSection(t, path, readDoc(t, path), "## The cascade, and the project policy switch")
	flat := flattenDoc(section)
	for _, want := range []string{
		"agent.auth: cascade",
		"routing.claude: {via: cascade}",
		"fugaro project policy publish",
		"the project policy",
		"the credentials service",
		"it names no model of its own",
		"refused",
		"docs/credentials-service/runbook.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s's cascade section never says %q", path, want)
		}
	}
}
