package credsvc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestWorkspaceBundleIsOneSecretWithOneGrant: Q6's shape, applied to the
// workspace. A workspace's model credentials live in ONE Secret Manager
// container, read with ONE `secretmanager.secretAccessor` grant, and the pool
// the service offers is read out of that one document: a per-credential container
// would multiply the grants this deployment holds (each a full read of a
// credential that pays the workspace's money) and would make "which credentials
// does the workspace hold" a secret listing rather than a document an owner
// reads.
//
// Three claims, and each is a property of the code rather than of a comment:
// the container's ID is the one logical secret (`creds-bundle`) for the
// workspace's own project; every shared credential comes out of ONE parsed
// document; and the pool's answer carries names and tiers and never a value.
func TestWorkspaceBundleIsOneSecretWithOneGrant(t *testing.T) {
	// One container. The workspace's own installation IS a Fugaro project
	// named after the workspace, so the container an installation named
	// `workspace-alpha` writes and the one a workspace named `workspace-alpha`
	// serves from are one container — and it is derived from the one logical
	// name, not chosen per credential.
	if got, want := WorkspaceBundleSecretID(wsWorkspace), gcp.CredsBundleSecretID(wsWorkspace); got != want {
		t.Errorf("WorkspaceBundleSecretID(%q) = %q, want the one bundle container %q", wsWorkspace, got, want)
	}
	if got, want := WorkspaceBundleSecretID(wsWorkspace), BundleSecretID(wsWorkspace); got != want {
		t.Errorf("WorkspaceBundleSecretID(%q) = %q and BundleSecretID(%q) = %q: the workspace's own project's bundle is the workspace's bundle", wsWorkspace, got, wsWorkspace, want)
	}

	// One document, and the pool read out of it. The document holds three
	// model credentials and the node declares two of them shared; the third is
	// the state of every credential an owner puts in the workspace's bundle
	// before anyone is on its allow-list.
	accountKey := config.ClaudeAccountSecret("team")
	accountName, kind, tier, ok := CredentialKey(accountKey)
	if !ok || kind != KindModel || tier != creds.TierA {
		t.Fatalf("CredentialKey(%q) = (%q, %v, %v, %v), want a tier-A model credential", accountKey, accountName, kind, tier, ok)
	}
	unsharedKey := config.ClaudeAccountSecret("retired")
	unsharedName, _, _, ok := CredentialKey(unsharedKey)
	if !ok {
		t.Fatalf("CredentialKey(%q) refused a name of its own", unsharedKey)
	}
	doc := accountKey + ": " + fakeCred(t, "team") + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n" +
		unsharedKey + ": " + fakeCred(t, "retired") + "\n"
	b, err := ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{
			{Name: accountName, Projects: []string{wsMemberA, wsMemberB}},
			{Name: config.AnthropicSecret, Projects: []string{wsMemberA}},
			// A credential the owner declared shared and the bundle does not
			// hold: there is no value to attach, so it is not offered.
			{Name: "claude-oauth-token-gone", Projects: []string{wsMemberA}},
		},
	}
	got := WorkspaceShared(b, node)
	want := []creds.Shared{
		{Credential: creds.Credential{Name: accountName, Tier: creds.TierA}, Projects: []string{wsMemberA, wsMemberB}},
		{Credential: creds.Credential{Name: config.AnthropicSecret, Tier: creds.TierB}, Projects: []string{wsMemberA}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceShared =\n%+v\nwant\n%+v", got, want)
	}
	// The bundle's undeclared credential is offered to nobody: no projects, so
	// no member, which is the direction that matters — a credential read as
	// "shared with everyone" by omission is D1298's refused option (B).
	for _, s := range got {
		if s.Name == unsharedName {
			t.Errorf("WorkspaceShared offered %q, which the owner's node does not declare shared: an undeclared credential is offered to nobody", unsharedName)
		}
	}
	if len(got) != 2 {
		t.Errorf("WorkspaceShared = %d credentials, want the two the owner declared and the bundle holds", len(got))
	}

	// No value, anywhere in the answer: the pool is spoken of in names and
	// tiers, and the whole answer rendered as text carries no credential.
	rendered := fmt.Sprint(got)
	for _, e := range b.Entries() {
		if v := e.Value(); v != "" && strings.Contains(rendered, v) {
			t.Fatal("the pool's answer renders a credential's value: rule 2 is that no run, no CLI and no person is handed one")
		}
	}
	fields := reflect.TypeOf(creds.Shared{})
	for i := 0; i < fields.NumField(); i++ {
		if strings.Contains(strings.ToLower(fields.Field(i).Name), "value") {
			t.Errorf("creds.Shared carries a %s field", fields.Field(i).Name)
		}
	}

	// A workspace that published no node and a deployment that holds no
	// document each have no pool — not a pool of everything.
	if WorkspaceShared(b, nil) != nil {
		t.Error("WorkspaceShared with no node = a pool, want none: a workspace that declared nothing shares nothing")
	}
	if WorkspaceShared(nil, node) != nil {
		t.Error("WorkspaceShared with no bundle = a pool, want none: a name with no value behind it is not a credential")
	}
}
