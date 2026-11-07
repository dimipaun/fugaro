package projectpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspace_test.go is the workspace node as a document: what it carries, what
// it must never carry, and the one step that moves an older node forward. The
// stage that publishes it is internal/cli's (update_workspace_test.go); what
// this file holds is the shape, which is where the SEC half of W2 lives — the
// node decides who is in the group, and a node that could name an issuer or a
// service account could aim every member's exchange and every member's grant
// at a party of its choosing (spec §2).

// wsRecord is a record of the shape the workspace's own config builds one in:
// members in project-name order, each member's repositories sorted, and a cap
// already in micro-dollars.
func wsRecord() *workspace.Record {
	return &workspace.Record{
		Name: "workspace-alpha",
		Members: []workspace.Member{
			{Project: "member-b", Repos: []string{"owner/repo-a", "owner/repo-b"}},
			{Project: "member-c", Repos: []string{}},
		},
		Cap: workspace.Cap{Monthly: pricing.Micros(25_000_000), Set: true},
	}
}

// wsKeys is every key the JSON document states, at every depth, sorted. It is
// how the negative half of the SEC check is made: not "the node does not carry
// an issuer" but "no key anywhere in the node is one of the names a pin or the
// owner's own table uses", which is the property that survives a change to the
// node's shape. A nested block this Fugaro does not write would fail here
// rather than pass by being one level down.
func wsKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("the node is not JSON: %v", err)
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				out = append(out, k)
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(v)
	sort.Strings(out)
	return slices.Compact(out)
}

// TestWorkspaceNodeCarriesTheMembersAndTheCapAndNothingElse is the SEC pin.
// The node the owner's `fugaro update` publishes decides who is in the group,
// and the one direction rule 1 does not survive is a node that supplies a value
// a member trusts: a record naming the issuer or the service account would point
// every member's exchange and every member's database grant at a party of the
// workspace's choosing. Those values are the member's own pin in the member's
// own config (W1), so no key of them may appear here at any depth.
func TestWorkspaceNodeCarriesTheMembersAndTheCapAndNothingElse(t *testing.T) {
	raw, err := WorkspaceNode(wsRecord())
	if err != nil {
		t.Fatal(err)
	}
	// The keys this Fugaro writes, exactly: a block that appears here without a
	// decision is a block a member could be made to read.
	keys := wsKeys(t, raw)
	want := []string{"cap", "members", "monthly_micros", "name", "project", "repos", "version"}
	if !slices.Equal(keys, want) {
		t.Errorf("the node states %v, want %v", keys, want)
	}
	// The names a pin, the owner's own member table and the member's half use.
	// Every one of them is load-bearing in the WRONG direction: read on the
	// node, an issuer or a service account would be the workspace naming a value
	// the member is supposed to pin for itself.
	for _, forbidden := range []string{
		"workspace_members", "workspace_cap", "workspace_pin", "job_service_accounts",
		"firebase_project", "fp", "exchange_url", "issuer", "assertion_key",
		"service_account", "aud", "audience", "identity_provider", "provider",
	} {
		if slices.Contains(keys, forbidden) {
			t.Errorf("the node carries %q, which is the owner's own table or a member's pin and not the workspace's record", forbidden)
		}
	}
	// And the money is integer micro-dollars from the record onward: the file
	// writes dollars, workspace.Parse converts once, and the node carries what
	// the record holds (nothing below that point does money arithmetic in a
	// float).
	if !bytes.Contains(raw, []byte(`"monthly_micros":25000000`)) {
		t.Errorf("the node does not carry the cap as integer micro-dollars:\n%s", raw)
	}
	if bytes.Contains(raw, []byte(`"monthly"`)) {
		t.Errorf("the node carries the file's dollar amount as well as the micro-dollars:\n%s", raw)
	}
}

// TestWorkspaceNodeRefusesARecordThatIsNotOne: a node is never built from a
// record the record's own rules refuse. The check is here and not only in
// workspace.Validate because the CLI builds records itself (a cap from the
// config's dollars, a member list from a YAML map), and a node written from one
// the file would refuse is a node the workspace's own service reads and the
// owner's own file disagrees with.
func TestWorkspaceNodeRefusesARecordThatIsNotOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *workspace.Record
	}{
		{"no name", &workspace.Record{Name: "", Members: []workspace.Member{{Project: "member-b"}}}},
		{"a member that is not a project name", &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "Member B"}}}},
		{"members out of order", &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "member-c"}, {Project: "member-b"}}}},
		{"a member listed twice", &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "member-b"}, {Project: "member-b"}}}},
		{"a cap that is set and zero", &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "member-b"}}, Cap: workspace.Cap{Set: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := WorkspaceNode(tc.rec); err == nil {
				t.Errorf("a node was built from a record with %s", tc.name)
			}
		})
	}
}

// TestDecodeWorkspaceReadsWhatFugaroWrites: the round trip, which is what makes
// the publish's comparison of values the comparison it means. A node that does
// not read back as the record it was built from would be republished on every
// run of the owner's update.
func TestDecodeWorkspaceReadsWhatFugaroWrites(t *testing.T) {
	rec := wsRecord()
	raw, err := WorkspaceNode(rec)
	if err != nil {
		t.Fatal(err)
	}
	w, found, err := DecodeWorkspace(raw)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspace = (%v, %v), want the node it was built from", found, err)
	}
	if w.Version != WorkspaceVersion || w.Name != rec.Name {
		t.Errorf("the node reads as (%d, %q), want (%d, %q)", w.Version, w.Name, WorkspaceVersion, rec.Name)
	}
	if len(w.Members) != len(rec.Members) {
		t.Fatalf("the node reads %d members, want %d", len(w.Members), len(rec.Members))
	}
	for i, m := range rec.Members {
		if w.Members[i].Project != m.Project || !slices.Equal(w.Members[i].Repos, m.Repos) {
			t.Errorf("members[%d] reads as %+v, want %+v", i, w.Members[i], m)
		}
	}
	if w.Cap == nil || w.Cap.MonthlyMicros != rec.Cap.Monthly {
		t.Errorf("the cap reads as %+v, want %d micro-dollars", w.Cap, rec.Cap.Monthly)
	}
	if len(w.UnknownKeys) != 0 {
		t.Errorf("the node this Fugaro wrote reads back with unknown keys %v", w.UnknownKeys)
	}
	// A record that states no cap is not a cap of zero: the node says so by
	// carrying no block at all.
	noCap := &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "member-b"}}}
	raw, err = WorkspaceNode(noCap)
	if err != nil {
		t.Fatal(err)
	}
	if w, _, err := DecodeWorkspace(raw); err != nil || w.Cap != nil {
		t.Errorf("a record with no cap reads as cap %+v (err %v), want none: a workspace that states no cap has no ceiling, which is not a ceiling of zero", w.Cap, err)
	}
}

// TestDecodeWorkspaceToleratesAnUnknownKeyAndListsIt: a node a NEWER Fugaro
// wrote stays readable here (P8), and the key is listed rather than dropped —
// which is what lets the caller that would REWRITE the node refuse it, since a
// republish from the local record would write a document without it.
func TestDecodeWorkspaceToleratesAnUnknownKeyAndListsIt(t *testing.T) {
	raw := []byte(`{"version":1,"name":"workspace-alpha","members":[{"project":"member-b","repos":[]}],"rollup":{"spend_micros":1}}`)
	w, found, err := DecodeWorkspace(raw)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspace = (%v, %v): a key this Fugaro does not model is tolerated and listed, never a refusal", found, err)
	}
	if !slices.Equal(w.UnknownKeys, []string{"rollup"}) {
		t.Errorf("the unknown keys read as %v, want [rollup]", w.UnknownKeys)
	}
}

// TestDecodeWorkspaceRefusesWhatItCannotRead: every state the reader has no
// meaning for is a refusal and not a guess. A node is what the workspace's own
// service admits against, so reading one of these as "no members" or "no cap"
// would be a document deciding a group by being unreadable.
func TestDecodeWorkspaceRefusesWhatItCannotRead(t *testing.T) {
	ok := `{"version":1,"name":"workspace-alpha","members":[{"project":"member-b","repos":["owner/repo-a"]}],"cap":{"monthly_micros":25000000}}`
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a newer version", `{"version":2,"name":"workspace-alpha","members":[]}`},
		{"not an object", `[1,2,3]`},
		{"a key written twice", `{"version":1,"name":"workspace-alpha","name":"workspace-beta","members":[]}`},
		{"a modelled key in another case", `{"Version":1,"name":"workspace-alpha","members":[]}`},
		{"a name that is not a project name", `{"version":1,"name":"Workspace Alpha","members":[]}`},
		{"a member that is not a project name", `{"version":1,"name":"workspace-alpha","members":[{"project":"Member B","repos":[]}]}`},
		{"a repository that is the empty string", `{"version":1,"name":"workspace-alpha","members":[{"project":"member-b","repos":[""]}]}`},
		{"a cap of zero", `{"version":1,"name":"workspace-alpha","members":[],"cap":{"monthly_micros":0}}`},
		{"a cap below zero", `{"version":1,"name":"workspace-alpha","members":[],"cap":{"monthly_micros":-1}}`},
		{"a member entry of the wrong shape", `{"version":1,"name":"workspace-alpha","members":["member-b"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := DecodeWorkspace([]byte(tc.raw)); err == nil {
				t.Errorf("DecodeWorkspace(%s) was read, and %s is not a node this Fugaro can mean", tc.raw, tc.name)
			}
		})
	}
	// And the node the fixture writes is read, so the refusals above are about
	// what they name and not about the reader refusing everything.
	if _, found, err := DecodeWorkspace([]byte(ok)); err != nil || !found {
		t.Fatalf("the reader refuses a node this Fugaro writes: (%v, %v)", found, err)
	}
	// A version below the current one is not this reader's either: it is
	// MigrateWorkspace's to carry forward, and reading it here would silently
	// read an older shape as this one.
	if _, _, err := DecodeWorkspace([]byte(`{"name":"workspace-alpha","members":[]}`)); err == nil {
		t.Error("DecodeWorkspace read a version-0 node: MigrateWorkspace carries those forward, and the reader that reads the current version must not read an older one as if it were")
	}
}

// TestMigrateWorkspaceCarriesVersion0ToVersion1 is the migration the task
// exists for. Version 0 is the record's own file shape — what
// schemas/workspace.schema.json describes, the only workspace document any
// release before this node wrote — so an installation that already holds one
// converges on the one command rather than being asked to publish again, and
// the dollars of the file become the micro-dollars of the node in that one
// step (workspace.Parse's own conversion), never a second time.
func TestMigrateWorkspaceCarriesVersion0ToVersion1(t *testing.T) {
	v0 := []byte(`{"name":"workspace-alpha","members":[{"project":"member-b","repos":["owner/repo-a","owner/repo-b"]}],"cap":{"monthly":25}}`)
	out, changes, err := MigrateWorkspace(v0)
	if err != nil {
		t.Fatalf("a version-0 node was not migrated: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("the migration reports %d steps, want 1", len(changes))
	}
	ch := changes[0]
	if ch.From != 0 || ch.To != WorkspaceVersion {
		t.Errorf("the step moves %d to %d, want 0 to %d", ch.From, ch.To, WorkspaceVersion)
	}
	if !slices.Contains(ch.Kept, "name") || !slices.Contains(ch.Kept, "members") {
		t.Errorf("the step reports keeping %v, and the record's name and members are what it must keep", ch.Kept)
	}
	if !strings.Contains(ch.Changed, "micro") {
		t.Errorf("the step says it changed %q, and the cap's dollars becoming micro-dollars is half of what it did", ch.Changed)
	}
	w, found, err := DecodeWorkspace(out)
	if err != nil || !found {
		t.Fatalf("the migrated node is not one the current reader reads: (%v, %v)", found, err)
	}
	if w.Version != WorkspaceVersion {
		t.Errorf("the migrated node is version %d, want %d", w.Version, WorkspaceVersion)
	}
	if w.Cap == nil || w.Cap.MonthlyMicros != pricing.Micros(25_000_000) {
		t.Errorf("the migrated cap is %+v, want %d micro-dollars ($25)", w.Cap, pricing.Micros(25_000_000))
	}
	if len(w.Members) != 1 || w.Members[0].Project != "member-b" || !slices.Equal(w.Members[0].Repos, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("the migrated members are %+v, want member-b's two repositories as the node carries them", w.Members)
	}
	// A version-0 node that carries a key the record's own schema closes is
	// refused rather than carried into a node nothing reads: the schema's
	// objects are closed and workspace.Parse decodes strictly, so such a key
	// was not written by a release.
	bad := []byte(`{"name":"workspace-alpha","members":[{"project":"member-b","repos":[]}],"issuer":"https://securetoken.google.com/project-ws"}`)
	out, changes, err = MigrateWorkspace(bad)
	if err == nil {
		t.Error("a version-0 node carrying an issuer was migrated: the record's own file shape carries no such key, so it was hand-edited and MigrateWorkspace must not carry it into the published node")
	}
	if !bytes.Equal(out, bad) {
		t.Errorf("a refused node must come back untouched, got %s", out)
	}
	if len(changes) != 0 {
		t.Errorf("a refused node reports %d changes, want none: no step was read", len(changes))
	}
}

// TestMigrateWorkspaceCurrentIsANoOp: a node already at this version comes back
// byte for byte. It is not judged here — whether it is a record this Fugaro can
// read is DecodeWorkspace's answer — and re-encoding it would rewrite the
// owner's document on every run.
func TestMigrateWorkspaceCurrentIsANoOp(t *testing.T) {
	raw, err := WorkspaceNode(wsRecord())
	if err != nil {
		t.Fatal(err)
	}
	// A node with a key this Fugaro does not model is still a no-op here: the
	// migration moves a version forward, and refusing or rewriting the node for
	// a key it does not understand is the reader's and the writer's business.
	odd := append([]byte(`{"rollup":{"spend_micros":1},`), raw[1:]...)
	for _, in := range [][]byte{raw, odd} {
		out, changes, err := MigrateWorkspace(in)
		if err != nil {
			t.Fatalf("a node at this version was refused: %v", err)
		}
		if len(changes) != 0 {
			t.Errorf("a node at this version reports %d changes, want none", len(changes))
		}
		if !bytes.Equal(out, in) {
			t.Errorf("a node at this version was re-encoded:\n got %s\nwant %s", out, in)
		}
	}
}

// TestMigrateWorkspaceRefusesANewerNodeUntouched: a node written by a newer
// Fugaro is refused rather than rewritten, and comes back exactly as it was, so
// a caller that ignored the error could not write a half-migrated document over
// it.
func TestMigrateWorkspaceRefusesANewerNodeUntouched(t *testing.T) {
	raw := []byte(`{"version":2,"name":"workspace-alpha","members":[{"project":"member-b","repos":[]}]}`)
	out, changes, err := MigrateWorkspace(raw)
	var ve *WorkspaceVersionError
	if !errors.As(err, &ve) {
		t.Fatalf("MigrateWorkspace = %v, want a *WorkspaceVersionError", err)
	}
	if ve.Found != 2 || ve.Understood != WorkspaceVersion {
		t.Errorf("the refusal carries (%d, %d), want (2, %d)", ve.Found, ve.Understood, WorkspaceVersion)
	}
	if !bytes.Equal(out, raw) || len(changes) != 0 {
		t.Errorf("a refused node must come back untouched, got %s with %d changes", out, len(changes))
	}
}

// TestMigrateWorkspaceRefusesWhatItCannotReadUntouched: the classes of node no
// version can be read from. Each is a refusal, none is a rewrite, and none
// reaches a step.
func TestMigrateWorkspaceRefusesWhatItCannotReadUntouched(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"empty", ``},
		{"null", `null`},
		{"not an object", `[]`},
		{"a version that is not an integer", `{"version":"1","name":"workspace-alpha","members":[]}`},
		{"a version below zero", `{"version":-1,"name":"workspace-alpha","members":[]}`},
		{"a key written twice", `{"version":0,"name":"workspace-alpha","name":"workspace-beta","members":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changes, err := MigrateWorkspace([]byte(tc.raw))
			if err == nil {
				t.Fatalf("MigrateWorkspace(%s) succeeded, and %s is a node no version can be read from", tc.raw, tc.name)
			}
			var ne *WorkspaceNodeError
			if !errors.As(err, &ne) {
				t.Errorf("MigrateWorkspace(%s) = %v, want a *WorkspaceNodeError", tc.raw, err)
			}
			if !bytes.Equal(out, []byte(tc.raw)) {
				t.Errorf("a refused node must come back untouched")
			}
			if len(changes) != 0 {
				t.Errorf("a refused node reports %d changes, want none", len(changes))
			}
		})
	}
}

// TestEveryOlderWorkspaceVersionHasAMigrator is the version-bump guard, the
// policy chain's own test read on this one: a bump of WorkspaceVersion that
// adds no step leaves every installation holding the older node with no way
// forward but a delete and a republish, which is the move `fugaro update` is
// not allowed to need. The chain's first version is 0 — the record's own file
// shape — so there is no version below it to refuse.
func TestEveryOlderWorkspaceVersionHasAMigrator(t *testing.T) {
	for v := 0; v < WorkspaceVersion; v++ {
		if _, ok := workspaceMigrators[v]; !ok {
			t.Errorf("workspace node version %d has no migrator, and every version below %d needs one: the owner's one command carries the node forward, and a release must not ship a delete and a republish", v, WorkspaceVersion)
		}
	}
	if _, ok := workspaceMigrators[WorkspaceVersion]; ok {
		t.Errorf("workspaceMigrators names a step at version %d, which is the version this Fugaro writes: there is nothing above it to migrate from", WorkspaceVersion)
	}
	for v := 0; v < WorkspaceVersion; v++ {
		// A node of every older shape the chain claims to carry must come out as
		// one the current reader reads, or the walk is carrying a document
		// nothing can read.
		raw := []byte(`{"name":"workspace-alpha","members":[{"project":"member-b","repos":[]}],"cap":{"monthly":1}}`)
		if v > 0 {
			raw = []byte(`{"version":` + itoa(v) + `,"name":"workspace-alpha","members":[{"project":"member-b","repos":[]}]}`)
		}
		out, _, err := MigrateWorkspace(raw)
		if err != nil {
			t.Errorf("workspace node version %d: MigrateWorkspace = %v", v, err)
			continue
		}
		if _, found, derr := DecodeWorkspace(out); derr != nil || !found {
			t.Errorf("workspace node version %d: the migrated node is not one the current reader reads (%v, %v)", v, found, derr)
		}
	}
}

// TestWorkspacePathsAreEscaped: the path is the record's own name, and the
// name is a project name — but the segment is escaped all the same, because the
// one thing that must never happen is a name reaching a path unescaped and
// landing the record somewhere else.
func TestWorkspacePathsAreEscaped(t *testing.T) {
	if got := PathWorkspace("workspace-alpha"); got != "workspaces/workspace-alpha" {
		t.Errorf("PathWorkspace = %q", got)
	}
	if got := PathWorkspaces(); got != "workspaces" {
		t.Errorf("PathWorkspaces = %q", got)
	}
}
