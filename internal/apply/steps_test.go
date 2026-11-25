package apply

import (
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestStepsTableNamesOnlyKnownStages: every step the table declares has to be
// one this build can run — an initflow stage name, or CatalogBuild, the one
// non-stage step. A row naming a stage that does not exist (the invented
// `deploy` or `publish-policy` D1561 rules out) is a step no command could ever
// run and no reader could ever clear: the change would sit pending forever with
// nothing wrong on screen. The table is the only place a change's steps are
// declared, so this is the only place that can catch it.
//
// The second half is the order: initflow.Names is the linear extension of
// initflow.Needs, and a row whose ordered steps put a stage before one it needs
// would be a chain that runs in an order initflow itself would refuse — the
// policy stage's write is gated on the rules marker, the checkout's build on
// the layer, and so on. Order is what a row is read through, so it is what this
// checks. The third is that the row is what Steps answers for the change it is
// keyed by, and that a change nobody declared answers "not declared" rather
// than "nothing owed" — a key that merely starts with a declared one among
// them, because that is the one a prefix row would answer for a family it does
// not declare, and the one an owner-side key added later would silently inherit
// the member's "owes nothing" from.
func TestStepsTableNamesOnlyKnownStages(t *testing.T) {
	rows := 0
	for _, resource := range slices.Sorted(maps.Keys(resourceSteps)) {
		rows++
		checkStepsRow(t, Change{Kind: Resource, Key: resource}, "resource "+resource, resourceSteps[resource])
	}
	for _, row := range settingSteps {
		rows++
		// The row's own key is what a change is keyed by: `image_catalog.`
		// stands for every `image_catalog.*` key, and an exact row stands for
		// itself.
		checkStepsRow(t, Change{Kind: Setting, Key: row.match}, "setting "+row.match, row.steps)
	}
	if rows == 0 {
		t.Fatal("the table declares no row at all: every change would answer \"nothing owed\"")
	}

	// A change the table does not know is a gap in it, and Steps has to say so
	// rather than answer with an empty list: a caller that read "no steps" as
	// "nothing owed" would save a change and never make it live.
	for _, c := range []Change{
		{Kind: Setting, Key: "no.such.key"},
		{Kind: Resource, Key: "no-such-resource"},
		{Kind: "no-such-kind", Key: "image"},
		{},
	} {
		if steps, ok := Steps(c); ok {
			t.Errorf("Steps(%+v) = %v, true, and the table declares nothing for it", c, steps)
		}
	}

	// A key that only starts with a declared one is a gap too, and it is the
	// case the bare membership key is matched whole for: `workspace` is a
	// prefix of every other key of its family, so a prefix row for it would
	// answer the keys below — the registry declares `workspace`, `workspace_pin.*`,
	// `workspace_members.*.*` and `workspace_cap.monthly` and none of these —
	// with "declared, owes nothing" instead of the gap, and an owner-side family
	// added later would be saved with no workspace stage queued and nothing said
	// about it.
	for _, c := range []Change{
		{Kind: Setting, Key: "workspace_members"},
		{Kind: Setting, Key: "workspace_cap"},
		{Kind: Setting, Key: "workspaces.foo"},
		{Kind: Setting, Key: "workspacex"},
		{Kind: Setting, Key: "workspace_pin"},
		{Kind: Setting, Key: "image_catalog"},
		{Kind: Setting, Key: "layer"},
	} {
		if steps, ok := Steps(c); ok {
			t.Errorf("Steps(%+v) = %v, true, and the table declares nothing for that key: it only starts with one the table declares, which is the gap and not the declaration",
				c, steps)
		}
	}
}

// TestStepsMatchesRealSettingKeys: the setting half of the table is matched by
// prefix, and a prefix row is only right if the keys it stands for are the ones
// the registry declares. Feeding Steps the rows' own prefixes — what the table
// test above does, since `image_catalog.` stands for every `image_catalog.*`
// key — cannot tell a prefix match from an exact one: an exact-match lookup
// answers each row correctly for its own prefix and leaves every real key with
// no steps at all, so a change to a key the registry declares would be saved
// with nothing to make it live. These are keys as `fugaro settings` spells them
// (internal/settings/registry.go), held to the row that owns them — and the two
// halves of a membership are held apart here as well, because the workspace
// stage is the owner's: its keys owe it, and the member's owe nothing, which is
// the difference between a step that publishes the change and one that can
// never run on the machine that asked for it.
func TestStepsMatchesRealSettingKeys(t *testing.T) {
	for _, tc := range []struct {
		key   string
		steps []string
	}{
		// A catalog entry's keys are the family's, and owe what the image
		// resource type owes: the build, the redeploy and the publish.
		{"image_catalog.entries.*.name", []string{CatalogBuild, initflow.Repository, initflow.Policy}},
		{"image_catalog.entries.*.source.inline", []string{CatalogBuild, initflow.Repository, initflow.Policy}},
		// The budget mode's key, which the registry itself answers for: the
		// mode a run reads is the published node's, so a change to it is
		// published rather than applied and the table above needs no row of
		// its own (AB8b, R8: the word's removal from a checkout's fugaro.yaml
		// is the repository's runs being refused, and the update's listing says
		// so before it offers the edit).
		{"budget.mode", []string{initflow.Policy}},
		// The project layer's keys are published by the layer stage.
		{"layer.defaults.agent.auth", []string{initflow.Layer}},
		{"layer.ab.judge_model", []string{initflow.Layer}},
		// The workspace OWNER's half: the same declaration is what the
		// workspace's own project is applied from (its service account and the
		// one grant) and what the record stage publishes, so it owes both.
		{"workspace_members.*.repos", []string{initflow.WorkspaceProject, initflow.Workspace}},
		{"workspace_members.*.job_service_accounts.*.repos", []string{initflow.WorkspaceProject, initflow.Workspace}},
		{"workspace_members.*.database_region", []string{initflow.WorkspaceProject, initflow.Workspace}},
		{"workspace_cap.monthly", []string{initflow.WorkspaceProject, initflow.Workspace}},
		// The shared list is the owner's too: it is published in the same record
		// the member table is, and it is the same project the record's service
		// account and grant belong to, so a credential shared or unshared owes
		// both stages and not a third one of its own.
		{"workspace_shared.*.kind", []string{initflow.WorkspaceProject, initflow.Workspace}},
		{"workspace_shared.*.projects", []string{initflow.WorkspaceProject, initflow.Workspace}},
		{"workspace_shared.*.covers", []string{initflow.WorkspaceProject, initflow.Workspace}},
		// The workspace MEMBER's half. The membership key and the pin's address
		// are what the member's own credential-route record is derived from, so
		// each owes the route stage: a project that joins or leaves a workspace,
		// or that changes the address it takes the workspace's service from,
		// has a record to publish or to remove (update_route.go, WS9). The rest
		// of the pin owes no step — nothing publishes them, and the workspace
		// stage skips on a member — and they are declared rather than missing,
		// so Steps answers true with no steps: a row mapping them to the
		// workspace stage would queue a step that can never run on the machine
		// that asked for it.
		{"workspace", []string{initflow.CredentialsRoute}},
		{"workspace_pin.issuer", nil},
		{"workspace_pin.service_address", []string{initflow.CredentialsRoute}},
		// The providers block: the entry the copy of the installation's model
		// keys writes carries the provider's own address and auth shape, and
		// reads its container from `providers.<name>.secret`, so the block's
		// keys owe the copy beside the publish their own row already declares
		// (update_keys.go, KS12/D3001/KS12a).
		{"providers.*.base_url", []string{initflow.Policy, initflow.CredentialsCopy}},
		{"providers.*.auth", []string{initflow.Policy, initflow.CredentialsCopy}},
		{"providers.*.secret", []string{initflow.Policy, initflow.CredentialsCopy}},
		{"providers.*.route_fee_pct", []string{initflow.Policy, initflow.CredentialsCopy}},
		// The opt-in fact: the key the operator's own command writes to put
		// this installation on Cloud Batch, which is live when the compute
		// root is applied (BR6, R2, D3110). The publish is restated beside it
		// because this row replaces the registry's answer for the key rather
		// than adding to it (R15).
		{"batch.opt_in", []string{initflow.Policy, initflow.Compute}},
	} {
		got, ok := Steps(Change{Kind: Setting, Key: tc.key})
		if !ok {
			t.Errorf("Steps(setting %s) declares nothing, and internal/settings declares that key", tc.key)
			continue
		}
		if want := Order(tc.steps); !slices.Equal(got, want) {
			t.Errorf("Steps(setting %s) = %v, want %v", tc.key, got, want)
		}
	}

	// The member's keys answer empty and true, which is not the same as the
	// false a key nobody declared answers — the caller prints "live once
	// written" for one and treats the other as a gap in the table.
	if steps, ok := Steps(Change{Kind: Setting, Key: "workspace_pin.issuer"}); !ok || len(steps) != 0 {
		t.Errorf("Steps(workspace_pin.issuer) = %v, %v, want no steps and true: declared, owing nothing", steps, ok)
	}
}

// checkStepsRow is one row of the table, held to all three halves.
func checkStepsRow(t *testing.T, change Change, row string, steps []string) {
	t.Helper()
	// A row with no steps is a declaration and not an omission: it says the
	// change is live once the config is written (AP5's "declares its steps, or
	// none"). The assertions below hold for it — no step to check, an empty
	// order — and the tail is what tells the two answers apart, since a gap in
	// the table makes Steps answer false rather than empty.
	for _, step := range steps {
		if !KnownStep(step) {
			t.Errorf("%s: %q is not a step this build can run: the table may name an initflow stage (initflow.Names) or %s, and nothing else",
				row, step, CatalogBuild)
		}
	}
	ordered := Order(steps)
	if len(ordered) != len(steps) {
		t.Errorf("%s: %v orders to %v, which holds a step twice: the table may not declare one step twice", row, steps, ordered)
	}
	// The one step that is not a stage sorts ahead of every stage: the digest
	// the catalog build produces is what the repository stage's redeploy pins,
	// so a chain that deployed first would pin an image that does not exist yet.
	if i := slices.Index(ordered, CatalogBuild); i > 0 {
		t.Errorf("%s: %v runs %s at %d, after a stage: the build runs before every stage", row, ordered, CatalogBuild, i)
	}
	for i, step := range ordered {
		for _, need := range initflow.Needs(step) {
			if j := slices.Index(ordered, need); j > i {
				t.Errorf("%s: %v runs %s at %d and %s, which it needs, at %d: initflow's order is what a row is read through",
					row, ordered, need, j, step, i)
			}
		}
	}
	got, ok := Steps(change)
	if !ok {
		t.Fatalf("%s: Steps(%+v) declares nothing, and the table declares %v", row, change, steps)
	}
	if !slices.Equal(got, ordered) {
		t.Errorf("%s: Steps(%+v) = %v, want %v", row, change, got, ordered)
	}
}
