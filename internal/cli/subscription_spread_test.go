package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// The pools' subscription switch (docs/model-crafts/reconciliation.md F22,
// D1622, D1636, plan MC5) is the one write of this task's that is a *setting*:
// it says whether a run may spread a project across two or more Claude OAuth
// subscriptions at all, and the plan makes its write MC5's beside the pool
// writes. What these tests pin is the whole road the owner's act takes —
// `settings set` into the local config, the typed form and the agent-session
// fence that F22 keeps from the switch it succeeds, the publish (and `fugaro
// update`'s policy stage) into the node, and the keep rule that stops a machine
// whose config says nothing from turning the spread off for everyone else.

// TestSettingsSetSubscriptionSpreadWritesTheSwitchAndItsSentence: the write is
// the generic path's, and the row is a typed one — the same direction
// claude_account_auto is held in — so the owner's own terminal writes it with
// --yes and reads the sentence saying what it turns on.
func TestSettingsSetSubscriptionSpreadWritesTheSwitchAndItsSentence(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	row, ok := settings.Lookup("subscription_spread")
	if !ok || row.TypedWhy == "" {
		t.Fatal("the registry no longer carries the pools' subscription switch as a typed row, so this test covers nothing")
	}
	if row.Default.Unset == "" || row.Owner != "settings set" {
		t.Errorf("the row is %+v, want a default sentence and the generic path as its owner", row)
	}

	out, _, err := execute(t, "settings", "set", "subscription_spread", "true", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("--yes did not confirm the switch's typed form: %v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "subscription_spread: true") {
		t.Errorf("the local config does not carry the switch:\n%s", got)
	}
	if !strings.Contains(out, row.TypedWhy) {
		t.Errorf("the write does not print the row's own sentence saying why the form is asked for:\n%s", out)
	}
	if strings.Contains(row.TypedWhy, "does not stand in") {
		t.Errorf("the switch's own sentence denies the --yes that just answered it: %s", row.TypedWhy)
	}
}

// TestSettingsSetSubscriptionSpreadRefusedInAgentSession: F22 keeps the
// refusals the switch it succeeds had, and the agent-session fence is the first
// of them — turning the spread on is the owner's own act about the terms the
// subscriptions are held under, so a coding agent's session writes it never,
// --yes included. The loop is over agentenv.Markers itself rather than a copy,
// so a marker the tree adds is one this write is held to.
func TestSettingsSetSubscriptionSpreadRefusedInAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			noTerminal(t)
			_, localPath := settingsWriteHome(t)
			before := readFileString(t, localPath)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "settings", "set", "subscription_spread", "true", "--layer", "local", "--yes")
			if err == nil {
				t.Fatalf("the switch was turned on in a coding agent's session:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "settings set subscription_spread") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if got := readFileString(t, localPath); got != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", got)
			}
		})
	}
}

// TestPublishWritesAndKeepsTheSubscriptionSpreadSwitch: the switch reaches a
// run off the published node and nowhere else, so the publish is what carries
// the owner's act — and the keep rule is the triage switch's, one direction
// over. A config that states the switch replaces what the node holds, a written
// false included, which is how the owner says no; a machine whose config says
// nothing keeps the node's, because the member is what makes a pool of two or
// more subscriptions serve anything at all (D1622) and a publish from a
// teammate's machine may not take the owner's pools away from every run of the
// project. A config and a node that both say nothing publish no member, so such
// a node is byte for byte what it was before the key existed.
func TestPublishWritesAndKeepsTheSubscriptionSpreadSwitch(t *testing.T) {
	f := newProjectFixture(t, "subscription_spread: true\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.SubscriptionSpread == nil || !*p.SubscriptionSpread {
		t.Fatalf("SubscriptionSpread = %v, want the true the config states", p.SubscriptionSpread)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"subscription_spread":true`) {
		t.Errorf("the node does not carry the switch:\n%s", raw)
	}

	rewriteProjectConfig(t, f, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.SubscriptionSpread == nil || !*p.SubscriptionSpread {
		t.Errorf("the second publish turned the spread off: SubscriptionSpread = %v, want the true the node held", p.SubscriptionSpread)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"subscription_spread":true`) {
		t.Errorf("the second publish dropped the member:\n%s", raw)
	}

	rewriteProjectConfig(t, f, "subscription_spread: false\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.SubscriptionSpread == nil || *p.SubscriptionSpread {
		t.Errorf("SubscriptionSpread = %v, want the written false the config states", p.SubscriptionSpread)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"subscription_spread":false`) {
		t.Errorf("the node does not carry the owner's no:\n%s", raw)
	}

	g := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, g, projectpolicy.PathShared()); strings.Contains(raw, "subscription_spread") {
		t.Errorf("a project that states no switch published one:\n%s", raw)
	}
	if p := publishedPolicy(t, g); p.SubscriptionSpread != nil {
		t.Errorf("SubscriptionSpread = %v, want nil", p.SubscriptionSpread)
	}
}
