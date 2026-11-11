package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// projectRoutingExtra is the fixture's route: the one provider entry that
// claims a Claude ID, with the allow_data_to the switch's own rule requires of
// the entry it names (config.CheckRouting). Nothing here is a secret value —
// `secret` is the name of the project secret the job mounts.
const projectRoutingExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - anthropic/claude-sonnet-5.5
    allow_data_to:
      - owner/repo-a
`

// projectRoutingCascade and projectRoutingRoute are the two switches a config
// can carry that the publisher refuses to lose in silence: the credentials
// cascade, and a route.
const (
	projectRoutingCascade = `
routing:
  claude:
    via: cascade
`
	projectRoutingRoute = `
routing:
  claude:
    via: openrouter
    models:
      - claude-sonnet-5-5
`
)

func projectRoutingLocal(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("%s does not read back: %v", path, err)
	}
	return lc
}

// TestProjectRoutingSetWritesTheVia: the switch is written from the flags, a
// list the route no longer needs goes with it, and a via the config's own rule
// refuses is a refusal with nothing written.
func TestProjectRoutingSetWritesTheVia(t *testing.T) {
	_, path := modelsWriteHomeWith(t, projectRoutingExtra)
	out, _, err := execute(t, "project", "routing", "set", "claude",
		"--via", "openrouter", "--models", "claude-sonnet-5-5")
	if err != nil {
		t.Fatalf("project routing set: %v\n%s", err, out)
	}
	lc := projectRoutingLocal(t, path)
	if lc.Routing == nil || lc.Routing.Claude == nil {
		t.Fatalf("the write left no switch:\n%s", readFileString(t, path))
	}
	if got := lc.Routing.Claude.ViaValue(); got != "openrouter" {
		t.Errorf("routing.claude.via = %q, want openrouter", got)
	}
	if got := lc.Routing.Claude.Models; len(got) != 1 || got[0] != "claude-sonnet-5-5" {
		t.Errorf("routing.claude.models = %v, want the one ID the flag carried", got)
	}

	// The cascade takes no list at all (CheckRoutingShape), so the list the
	// route needed goes with the route rather than blocking the switch.
	if _, _, err := execute(t, "project", "routing", "set", "claude", "--via", "cascade"); err != nil {
		t.Fatalf("switching to the cascade: %v", err)
	}
	lc = projectRoutingLocal(t, path)
	if got := lc.Routing.Claude.ViaValue(); got != "cascade" {
		t.Errorf("routing.claude.via = %q, want cascade", got)
	}
	if got := lc.Routing.Claude.Models; len(got) != 0 {
		t.Errorf("routing.claude.models = %v, want the key gone under the cascade", got)
	}

	before := readFileString(t, path)
	_, _, err = execute(t, "project", "routing", "set", "claude", "--via", "somewhere")
	if err == nil {
		t.Fatal("a via the config's own rule refuses was written")
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused write changed the file:\n%s", after)
	}
}

// TestProjectRoutingUnsetRemovesTheBlock: the whole block goes when the switch
// it carries turns nothing off — an inert `via: direct` is not a route and not
// the cascade, so removing it says what it already said (D1b).
func TestProjectRoutingUnsetRemovesTheBlock(t *testing.T) {
	_, path := modelsWriteHomeWith(t, projectRoutingExtra+`
routing:
  claude:
    via: direct
`)
	if _, _, err := execute(t, "project", "routing", "unset"); err != nil {
		t.Fatalf("project routing unset: %v", err)
	}
	if lc := projectRoutingLocal(t, path); lc.Routing != nil {
		t.Errorf("routing = %+v, want the block gone", lc.Routing)
	}
	if data := readFileString(t, path); strings.Contains(data, "routing:") {
		t.Errorf("the block is still in the file:\n%s", data)
	}
}

// TestProjectRoutingShowPrintsTheRoute: the read. It prints the route a run
// would take, says so plainly when the file carries no switch, and --json
// carries the same two facts.
func TestProjectRoutingShowPrintsTheRoute(t *testing.T) {
	_, _ = modelsWriteHomeWith(t, projectRoutingExtra+projectRoutingCascade)
	out, _, err := execute(t, "project", "routing", "show")
	if err != nil {
		t.Fatalf("project routing show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "via cascade") {
		t.Errorf("the read does not print the switch the file carries:\n%s", out)
	}

	raw, _, err := execute(t, "project", "routing", "show", "--json")
	if err != nil {
		t.Fatalf("project routing show --json: %v\n%s", err, raw)
	}
	var doc struct {
		Path    string   `json:"path"`
		Written bool     `json:"written"`
		Via     string   `json:"via"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("--json is not a document this read defines: %v\n%s", err, raw)
	}
	if !doc.Written || doc.Via != "cascade" || doc.Models == nil {
		t.Errorf("--json = %+v, want the written cascade and a list rather than null", doc)
	}

	// No switch at all: every Claude call goes direct, which is the answer a
	// reader needs and not an empty line.
	_, _ = modelsWriteHomeWith(t, projectRoutingExtra)
	out, _, err = execute(t, "project", "routing", "show")
	if err != nil {
		t.Fatalf("project routing show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "direct") || strings.Contains(out, "cascade") {
		t.Errorf("a config with no switch does not read as the direct route:\n%s", out)
	}
}

// TestProjectRoutingRefusesSilenceThatTurnsTheCascadeOff: the SEC half of this
// family. The publisher refuses a policy that would turn a switch off by
// omission (buildProjectPolicy's silence rules), and the command that writes
// the same block refuses it one step earlier, in its own words, with the
// remedy: say it on purpose. This is the test the mutation check of the review
// breaks the refusal for.
func TestProjectRoutingRefusesSilenceThatTurnsTheCascadeOff(t *testing.T) {
	_, path := modelsWriteHomeWith(t, projectRoutingExtra+projectRoutingCascade)
	before := readFileString(t, path)
	out, _, err := execute(t, "project", "routing", "unset")
	if err == nil {
		t.Fatalf("unset dropped a switch that turns the cascade on:\n%s", out)
	}
	for _, want := range []string{"cascade", "credentials", "via: direct", "fugaro project routing set claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused unset changed the file:\n%s", after)
	}

	// A route is refused for its own reason: a route that goes away is a
	// Claude call that stops being sent where it was put, which is not what
	// the cascade does.
	_, routePath := modelsWriteHomeWith(t, projectRoutingExtra+projectRoutingRoute)
	routeBefore := readFileString(t, routePath)
	_, _, err = execute(t, "project", "routing", "unset")
	if err == nil {
		t.Fatal("unset dropped a route")
	}
	if !strings.Contains(err.Error(), "route") {
		t.Errorf("the refusal of a route does not say what a route is: %v", err)
	}
	if after := readFileString(t, routePath); after != routeBefore {
		t.Errorf("a refused unset changed the file:\n%s", after)
	}

	// The remedy the refusal names is the write that says it on purpose, and
	// it is the switch that stays in the file rather than the omission.
	if _, _, err := execute(t, "project", "routing", "set", "claude", "--via", "direct"); err != nil {
		t.Fatalf("the remedy the refusal names does not work: %v", err)
	}
	if got := projectRoutingLocal(t, path).Routing.Claude.ViaValue(); got != "direct" {
		t.Errorf("routing.claude.via = %q, want the direct route written out", got)
	}
}

// TestProjectRoutingRefusesInAnAgentSession: this is the machine's own config
// file, outside every repository, so a coding agent's session writes nothing
// here whatever --yes says (SD9) — and the read is not refused, because a
// session that may not decide may still look.
func TestProjectRoutingRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			_, path := modelsWriteHomeWith(t, projectRoutingExtra+projectRoutingCascade)
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "project", "routing", "set", "claude", "--via", "direct", "--yes")
			if err == nil {
				t.Fatal("the command wrote in a coding agent's session")
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro project routing") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out, path) || !strings.Contains(out, "  + ") {
				t.Errorf("the refusal does not print the diff it refused:\n%s", out)
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", after)
			}
			if _, _, err := execute(t, "project", "routing", "show"); err != nil {
				t.Errorf("the read was refused in a coding agent's session: %v", err)
			}
		})
	}
}
