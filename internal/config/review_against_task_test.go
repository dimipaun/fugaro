package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The review-against-task switch's config surface (R7 of
// docs/review-against-task/, D2935): one repository-scope key of fugaro.yaml
// whose absence means every review stage is given the task the pull request was
// written for, and which the owner's published policy node gates from beside it.

// reviewAgainstTaskHead is a repository file whose agent: block the cases below
// extend, and reviewAgainstTaskTail the workflows every fugaro.yaml needs.
const (
	reviewAgainstTaskHead = minimalRepo + "git: { provider: github }\n"
	reviewAgainstTaskTail = "workflows:\n  app:\n    commands: { build: make, test: make test }\n"
)

// reviewAgainstTaskShipsIn is the release the key ships in, pinned here for the
// schema's sentence alone. internal/cli's reviewSpecSince is the constant the
// launch's gate reads (RA9) and the one its own test compares against the
// release actually cut; this copy is the second reader's, and the two have to
// name the same release.
const reviewAgainstTaskShipsIn = "0.27.0"

// TestReviewAgainstTaskDecodes is the switch's own surface: absent is on — every
// review stage checks the change against the task by default (spec §1) — a
// written false turns the check off, and the key being written at all is the
// question the image-skew gate asks (RepoReviewAgainstTaskSet), for
// RepoTriageSet's reason: a runner older than the release refuses the key
// whatever it says, so the resolved pointer cannot stand for the key's
// presence.
//
// The published schema is asked in the same breath. fugaro.yaml is read by two
// surfaces — the strict decoder a run uses and the schema an editor or a
// repository's CI checks the file against — and TestFugaroSchemaCorpus cannot
// catch a key the schema forgot, because no instance in its corpus writes it.
func TestReviewAgainstTaskDecodes(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	for name, tc := range map[string]struct {
		body string
		on   bool
		set  bool
		bad  bool
	}{
		"nothing at all":               {"", true, false, false},
		"the check on":                 {"  review_against_task: true\n", true, true, false},
		"the check off":                {"  review_against_task: false\n", false, true, false},
		"a word where the switch is":   {"  review_against_task: nope\n", false, false, true},
		"a number where the switch is": {"  review_against_task: 1\n", false, false, true},
		"a misspelt key":               {"  review_against_tasks: true\n", false, false, true},
	} {
		// An absent key is a file with no agent: block at all: `agent:` written
		// bare is null, which the schema refuses for every key and not for this
		// one, so the two surfaces are asked the same question here.
		text := reviewAgainstTaskHead + reviewAgainstTaskTail
		if tc.body != "" {
			text = reviewAgainstTaskHead + "agent:\n" + tc.body + reviewAgainstTaskTail
		}
		c, ps := Parse([]byte(text))
		if tc.bad {
			if len(ps) == 0 {
				t.Errorf("%s: Parse accepted %q, want the strict decoder's refusal", name, tc.body)
			}
			if abSchemaAccepts(t, sch, text) {
				t.Errorf("%s: fugaro.schema.json accepted %q, and the decoder refuses it", name, tc.body)
			}
			continue
		}
		if len(ps) != 0 {
			t.Errorf("%s: Parse = %+v, want none", name, ps)
			continue
		}
		if got := c.Agent.ReviewAgainstTaskOn(); got != tc.on {
			t.Errorf("%s: ReviewAgainstTaskOn() = %v, want %v", name, got, tc.on)
		}
		if got := c.RepoReviewAgainstTaskSet; got != tc.set {
			t.Errorf("%s: RepoReviewAgainstTaskSet = %v, want %v: the gate judges the key being written, at either value", name, got, tc.set)
		}
		if !abSchemaAccepts(t, sch, text) {
			t.Errorf("%s: fugaro.schema.json refused a file the decoder accepts", name)
		}
	}
	// The schema's own node, and the release its sentence names: a binary before
	// that release refuses the key whatever it says, which is what the launch's
	// gate (internal/cli's reviewSpecSince) judges a job image on.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "fugaro.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	node := agentProperty(t, doc, "review_against_task")
	if node["type"] != "boolean" {
		t.Errorf("fugaro.schema.json's agent.review_against_task is %v, want a boolean", node)
	}
	description, _ := node["description"].(string)
	for _, want := range []string{
		"Needs fugaro " + reviewAgainstTaskShipsIn,
		"config.Scopes",
		"config.Agent.ReviewAgainstTaskOn",
		"the base branch",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("fugaro.schema.json's agent.review_against_task does not name %q: %q", want, description)
		}
	}
}

// TestScopesReviewAgainstTaskRepoOnly is R7's trust boundary. A launcher writes
// the task and the project layer travels inside it (task.Spec.ProjectLayer), so
// a layer key would be a second home for the switch — one reachable from
// whatever revision the launcher read, including the checkout under review,
// which could turn off the review of the run's own work. The key is therefore
// not a layer key at all: config.Scopes gives it InRepo and nothing else (the
// refusal agent.triage beside it carries, for the same reason), so a project
// layer that writes defaults.agent.review_against_task is refused in every
// repository of the project. The owner's published policy node is the other
// home, merged tighter-only and read as the runner's own gate (RA8a).
//
// The last half is the read's own direction: the value follows the file the
// caller read and not the layer beside it, so a run reads the base branch's
// answer and never the branch it reviewed.
func TestScopesReviewAgainstTaskRepoOnly(t *testing.T) {
	for name, body := range map[string]string{
		"on":   "    review_against_task: true\n",
		"off":  "    review_against_task: false\n",
		"null": "    review_against_task:\n",
	} {
		if _, ps := ParseProjectLayer([]byte(layerHead+"defaults:\n  agent:\n"+body), testAnchor); len(ps) == 0 {
			t.Errorf("the project layer accepted defaults.agent.review_against_task written %s: a layer a launcher writes is not a home for the switch, whatever it says", name)
		}
	}
	found := false
	for _, s := range Scopes {
		if s.Key != "agent.review_against_task" {
			continue
		}
		found = true
		if s.In != InRepo {
			t.Errorf("config.Scopes: agent.review_against_task is set in %b, want InRepo alone", s.In)
		}
		if s.Why == "" {
			t.Error("config.Scopes: agent.review_against_task carries no why")
		}
	}
	if !found {
		t.Error("config.Scopes has no agent.review_against_task row: the key is unknown to every reader that judges a layer")
	}
	l := mustLayer(t, layerHead+"defaults:\n  agent:\n    review_rounds: 3\n")
	for name, tc := range map[string]struct {
		text string
		want bool
	}{
		"the base's file turned it off": {reviewAgainstTaskHead + "agent:\n  review_against_task: false\n" + reviewAgainstTaskTail, false},
		"the base's file says neither":  {reviewAgainstTaskHead + reviewAgainstTaskTail, true},
	} {
		c, _, ps := Resolve([]byte(tc.text), l)
		if len(ps) > 0 {
			t.Fatalf("%s: %+v", name, ps)
		}
		if got := c.Agent.ReviewAgainstTaskOn(); got != tc.want {
			t.Errorf("%s: ReviewAgainstTaskOn() = %v, want %v: the switch is the file the caller read", name, got, tc.want)
		}
	}
}
