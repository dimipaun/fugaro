package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// pause_settings_test.go is PR9's road through the CLI (R11 of
// docs/model-credentials-routing, D3184, D3187, D3190): `fugaro settings set`
// writes a member of the pause block into the local config, `fugaro update`'s
// policy stage publishes the block as the node's `pause` key — the key of the
// credentials service and of no file — a machine whose config says nothing
// keeps the node's block, and the ordering rule stops at the command the
// operator ran, rather than at the publish or at the run that reads the node.

// TestSettingsSetWritesThePauseSettings: the write is the generic path's, and
// the file it lands in is this machine's own — the three settings are
// local-only, the cascade's reason.
func TestSettingsSetWritesThePauseSettings(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	for _, tc := range []struct{ key, value, want string }{
		{"pause.wait_in_job", "10m", "wait_in_job: 10m"},
		{"pause.min_pause", "45m", "min_pause: 45m"},
		{"pause.max_pause", "2h", "max_pause: 2h"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			out, _, err := execute(t, "settings", "set", tc.key, tc.value, "--layer", "local")
			if err != nil {
				t.Fatalf("the write was refused: %v\n%s", err, out)
			}
			if got := readFileString(t, localPath); !strings.Contains(got, tc.want) {
				t.Errorf("the local config does not carry %s:\n%s", tc.want, got)
			}
		})
	}

	// The rule is asked of the block as the service would use it, so a floor
	// written over the default minimum is refused at the command the operator
	// ran — the file would otherwise hold a block no publish could write — and
	// the refusal names the member to raise beside it.
	_, before := settingsWriteHome(t)
	beforeText := readFileString(t, before)
	out, _, err := execute(t, "settings", "set", "pause.wait_in_job", "1h", "--layer", "local")
	if err == nil {
		t.Fatalf("a floor over the default minimum was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "wait_in_job") || !strings.Contains(err.Error(), "min_pause") {
		t.Errorf("the refusal names neither the member written nor the one to write beside it: %v", err)
	}
	if got := readFileString(t, before); got != beforeText {
		t.Errorf("the file changed under a refusal:\n%s", got)
	}
}

// TestPublishWritesAndKeepsThePauseSettings: the block reaches a run off the
// published node and nowhere else, so the publish is what carries the
// operator's act — and the merge is the cascade's. A config that states a
// member wins it and takes the node's for the members it leaves out; a machine
// whose config says nothing keeps the node's block whole, which is what stops a
// teammate's `fugaro update` putting the project back on the plan's own figures
// for the runs in flight while it republishes the node. A config and a node
// that both say nothing publish no member at all.
func TestPublishWritesAndKeepsThePauseSettings(t *testing.T) {
	f := newProjectFixture(t, "pause:\n  min_pause: 1h\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.Pause == nil || p.Pause.MinPause != "1h" {
		t.Fatalf("Pause = %+v, want the minimum the config states", p.Pause)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"pause":{"min_pause":"1h"}`) {
		t.Errorf("the node does not carry the block:\n%s", raw)
	}

	// A machine whose config predates the key publishes the node's block back
	// unchanged: the member is what decides whether a run waits in a job it pays
	// for by the minute, so a publish from such a machine may not move it.
	rewriteProjectConfig(t, f, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.Pause == nil || p.Pause.MinPause != "1h" {
		t.Errorf("the second publish dropped the node's block: %+v", p.Pause)
	}

	// A written member wins and the node fills the rest, member by member: the
	// node's floor and maximum stand beside the minimum the file states, because
	// a file that writes `min_pause` has said nothing about the other two.
	rewriteProjectConfig(t, f, "pause:\n  min_pause: 1h\n")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{"github/acme/app"},
		"pause":       map[string]any{"wait_in_job": "4m", "max_pause": "2h"},
	})
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.Pause == nil || p.Pause.MinPause != "1h" || p.Pause.WaitInJob != "4m" || p.Pause.MaxPause != "2h" {
		t.Fatalf("Pause = %+v, want the file's minimum beside the node's floor and maximum", p.Pause)
	}

	// The merged block is what the rule is asked of, so a file that is valid on
	// its own is refused beside a node holding a member that makes it invalid —
	// with the file's key in the sentence, while both halves are still in hand.
	g := newProjectFixture(t, "pause:\n  wait_in_job: 25m\n")
	g.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{"github/acme/app"},
		"pause":       map[string]any{"min_pause": "20m"},
	})
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("the merge of a floor over the node's minimum was published")
	}
	if !strings.Contains(err.Error(), "wait_in_job") || !strings.Contains(err.Error(), "min_pause") {
		t.Errorf("the refusal does not name the pair the merge broke: %v", err)
	}

	// A config that states no block and a node that carries none publish no
	// member: the database keeps no empty object, and a node that carried one
	// would read as a difference against itself on every publish after it.
	h := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, h, projectpolicy.PathShared()); strings.Contains(raw, "pause") {
		t.Errorf("a project that states no pause block published one:\n%s", raw)
	}
	if p := publishedPolicy(t, h); p.Pause != nil {
		t.Errorf("Pause = %+v, want nil", p.Pause)
	}
}

// TestPublishRefusesAPauseBlockTheNodeCannotCarry: a node a hand edit left
// out of order is refused where the runs read it — the block is one the
// credentials service decides by, not a value only the writer judges — and the
// shared config refuses the key besides, because a launcher who could write the
// block into the runs bucket would be holding every teammate's job open for a
// reset.
func TestPublishRefusesAPauseBlockTheNodeCannotCarry(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{"github/acme/app"},
		"pause":       map[string]any{"wait_in_job": "1h"},
	})
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a node carrying a floor over the default minimum was read")
	}
	if !strings.Contains(err.Error(), "pause") {
		t.Errorf("the refusal does not say which key the problem is in: %v", err)
	}
	if !slices.Contains(sharedForbidden, "pause") {
		t.Error("the shared file admits pause, so a launcher could hold every teammate's job open for a reset")
	}
}
