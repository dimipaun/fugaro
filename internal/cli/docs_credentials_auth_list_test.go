package cli

import (
	"strings"
	"testing"
)

// TestDocsCredentialsAuthListNamesCascade: agent.auth gained a fourth value
// (config.AuthCascade, plan Task C1.1), and the sweep that updated the code's
// copies of the list left three prose copies behind — the credentials bundle's
// own reconciliation, whose §1.2 declared the list "three values, not four";
// the README's fugaro.yaml example; and the setup skill's decisions. Each is a
// place a reader meets the list, and each contradicted the validator after
// this branch: a reader following them would not know the value exists at all,
// or would read the document that wins over spec.md and believe a cascade file
// is refused.
//
// The list is read from config.AgentAuths (internal/config/defaults.go) — the
// one list the validator, the layer's own defaults.agent.auth key and the
// launcher's check of a run's spec all read — rather than spelled out here, so
// a fifth value fails this test in all three documents at once rather than
// leaving them to be found by eye.
//
// Mutation (run, restored): drop "cascade" from README.md's auth comment, from
// the setup skill's decision line, or restore §1.2's "has three values, not
// four" heading — each fails, naming the document it reads.
func TestDocsCredentialsAuthListNamesCascade(t *testing.T) {
	auths := quoted(sourceValues(t, "internal/config/defaults.go", readDoc(t, "internal/config/defaults.go"),
		`AgentAuths = \[\]string\{([^}]*)\}`)[0])
	if len(auths) < 4 {
		t.Fatalf("config.AgentAuths parsed as %v; this scan is no longer reading defaults.go", auths)
	}

	// 1. README.md's fugaro.yaml example: the comment after `auth:` is the list
	// a reader copying the example reads.
	readme := readDoc(t, "README.md")
	var example string
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "auth: oauth") && strings.Contains(line, "#") {
			example = line
			break
		}
	}
	if example == "" {
		t.Fatal("README.md's fugaro.yaml example carries no `auth: oauth` comment line: the list moved and this scan no longer reads it")
	}
	namesEveryAuth(t, "README.md's fugaro.yaml example", example, auths)

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
}

// namesEveryAuth requires text to carry every value of the auth list. It is a
// contains check per value rather than a parse of the enumeration: the three
// documents spell the list three ways (a YAML comment, a decision line, a
// sentence), and what this guard is for is the value left OUT, which is what
// it names.
func namesEveryAuth(t *testing.T, where, text string, auths []string) {
	t.Helper()
	for _, auth := range auths {
		if !strings.Contains(text, auth) {
			t.Errorf("%s does not name %q (config.AgentAuths): %q", where, auth, text)
		}
	}
}
