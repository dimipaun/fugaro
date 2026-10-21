package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The orchestrator access record and its commands (central-orchestrator plan
// P2, decisions D6 and D12): `fugaro orchestrator add|rm|ls` are project-level
// commands in the shape of `fugaro project repos add|rm|ls`, and the two
// things they write are the policy's orchestrators block (the admission and
// the cap D12's `add` has to be able to name) and the device node
// config/admin/devices/<uid> extended with the administered email and role
// (the authorisation the deployed rules read).
//
// The tests below hold the two halves apart on purpose. The policy half is
// read back through the published node (`publishedPolicy`), the access half
// through the database the rules would read — and the last test of the file is
// the one that says the two are kept in step by a *publish*, which is the
// thing an operator does between two `orchestrator add`s.

// orchestratorAddArgs is the shortest command that names everything D12's
// `add` must be able to name: which orchestrator (the allowlist), who is
// admitted (the identity: a uid and the email and role of its access record)
// and what its jobs may spend (the cap).
func orchestratorAddArgs(name string, extra ...string) []string {
	args := []string{"orchestrator", "add", name,
		"--allow", "alice-uid", "--email", "alice@example.test", "--role", "member", "--cap-usd", "25"}
	return append(args, extra...)
}

// seedOrchestratorPolicy writes a policy node carrying the orchestrators block
// under test. It is the node an operator's earlier publish, this machine's or
// another's, left behind: what `ls` and `rm` read is the published document,
// and a test about either of them should not have to go through `add` to have
// one (that command has its own tests, and a seed that went through it would
// make a failure in `add` read as a failure in `ls`).
func seedOrchestratorPolicy(t *testing.T, f *projectFixture, block string) {
	t.Helper()
	node := `{"version":` + strconv.Itoa(projectpolicy.Version) +
		`,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app"]`
	if block != "" {
		node += `,"orchestrators":` + block
	}
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(node+"}"))
}

// accessRecord seeds one config/admin/devices/<uid> node the way `fugaro pair`
// and an administrator leave it: the pairing half, and optionally the
// administered email and role.
func seedAccessRecord(f *projectFixture, uid string, node map[string]any) {
	f.db.Set(rules.PathDevices+"/"+uid, node)
}

// accessRecordFor returns the node at config/admin/devices/<uid>, or nil.
func accessRecordFor(f *projectFixture, uid string) map[string]any {
	v, _ := f.db.Value(rules.PathDevices + "/" + uid).(map[string]any)
	return v
}

// TestOrchestratorAddRefusesAProjectWithNoPolicy is the plan's red step, the
// CLI half: `add` writes the orchestrators block of the *published* policy, so
// a project with nothing published has nothing for it to write, and the
// refusal has to come before the access record is touched — an installation
// whose administrator was driven to `add` by this sentence must not be left
// with a half-written record and no admission behind it (the write order the
// ETag case below pins the other way around).
func TestOrchestratorAddRefusesAProjectWithNoPolicy(t *testing.T) {
	f := newProjectFixture(t, "")
	_, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err == nil || !strings.Contains(err.Error(), "no policy is published") {
		t.Fatalf("orchestrator add with no published policy: err = %v, want the refusal naming the missing policy", err)
	}
	if !strings.Contains(err.Error(), "project policy publish") {
		t.Errorf("the refusal does not name the command that fixes it: %v", err)
	}
	if v := f.db.Value(rules.PathDevices); v != nil {
		t.Errorf("%s = %v after the refusal, want nothing written: a refused add administers nobody", rules.PathDevices, v)
	}
}

// TestOrchestratorLsPrintsTheRecord is the plan's other red step: `ls` prints
// what is administered — the uid, the email and the role the rules read — and
// not only what the policy block admits, because the two are separate writes
// and an operator reading one without the other cannot tell whether a seat is
// reachable.
func TestOrchestratorLsPrintsTheRecord(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "alice@example.test", "role": rules.RoleOrchestratorMember,
	})
	out, _, err := execute(t, "orchestrator", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shepherd", "25", "alice-uid", "alice@example.test", rules.RoleOrchestratorMember} {
		if !strings.Contains(out, want) {
			t.Errorf("orchestrator ls does not name %q:\n%s", want, out)
		}
	}
}

// TestOrchestratorAddWritesThePolicyBlockAndTheAccessRecord: one command, two
// writes, and both are asserted here — the block the next `project policy
// publish` keeps, and the node the deployed rules read on every connection.
//
// The second half of the case is a *second* add of the same orchestrator: the
// identities accumulate and the cap is replaced, so `add` is the one writer of
// an entry and an operator never has to remove an orchestrator to widen it.
func TestOrchestratorAddWritesThePolicyBlockAndTheAccessRecord(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	out, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := publishedPolicy(t, f).OrchestratorFor("shepherd")
	if !ok || o.CapUSD != 25 || len(o.Allow) != 1 || o.Allow[0] != "alice-uid" {
		t.Fatalf("orchestrators.shepherd = %+v (present %v), want cap 25 and [alice-uid]", o, ok)
	}
	rec := accessRecordFor(f, "alice-uid")
	if rec["email"] != "alice@example.test" || rec["role"] != rules.RoleOrchestratorMember {
		t.Errorf("access record = %v, want the administered email and role", rec)
	}
	if rec["pairedAt"] != "2026-10-20T00:00:00Z" {
		t.Errorf("access record = %v: the pairing half must survive administration", rec)
	}
	for _, want := range []string{"shepherd", "alice-uid", "alice@example.test", projectpolicy.PathShared(), rules.PathDevices} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not name %q:\n%s", want, out)
		}
	}

	seedAccessRecord(f, "bob-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	out, _, err = execute(t, "orchestrator", "add", "shepherd",
		"--allow", "bob-uid", "--email", "bob@example.test", "--role", rules.RoleOrchestratorAdmin, "--cap-usd", "30")
	if err != nil {
		t.Fatal(err)
	}
	o, _ = publishedPolicy(t, f).OrchestratorFor("shepherd")
	if o.CapUSD != 30 || len(o.Allow) != 2 || o.Allow[0] != "alice-uid" || o.Allow[1] != "bob-uid" {
		t.Errorf("after the second add: %+v, want cap 30 and both identities (sorted)", o)
	}
	if !strings.Contains(out, "$25") || !strings.Contains(out, "$30") {
		t.Errorf("the second add does not show the cap it replaced:\n%s", out)
	}
	if rec := accessRecordFor(f, "alice-uid"); rec["role"] != rules.RoleOrchestratorMember {
		t.Errorf("the first identity's record was rewritten by an add that did not name it: %v", rec)
	}
}

// TestOrchestratorAddReportsWhatMoved: an add that names an identity the entry
// already admits, at the cap it already carries, changes nothing in the block —
// what it does is re-administer the record, which is how an operator corrects a
// role. The report has to say exactly that: the arrow is the operator's
// evidence that the allow-list moved, so "[alice-uid] → [alice-uid]" reads as a
// widening that did not happen, and an operator closing the terminal on it has
// been told the command did something it did not do.
func TestOrchestratorAddReportsWhatMoved(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	if _, _, err := execute(t, orchestratorAddArgs("shepherd")...); err != nil {
		t.Fatal(err)
	}
	// The same identity at the same cap, with the role promoted: the record is
	// re-administered and the block is not.
	out, _, err := execute(t, "orchestrator", "add", "shepherd",
		"--allow", "alice-uid", "--email", "alice@example.test", "--role", rules.RoleOrchestratorAdmin, "--cap-usd", "25")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "→") {
		t.Errorf("the repeat add shows a change that did not happen:\n%s", out)
	}
	for _, want := range []string{"unchanged", "already admitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the repeat add does not say the block is what it was (%q):\n%s", want, out)
		}
	}
	if rec := accessRecordFor(f, "alice-uid"); rec["role"] != rules.RoleOrchestratorAdmin {
		t.Errorf("the repeat add did not re-administer the record: %v", rec)
	}
	if o, _ := publishedPolicy(t, f).OrchestratorFor("shepherd"); o.CapUSD != 25 || len(o.Allow) != 1 {
		t.Errorf("the repeat add changed the block: %+v", o)
	}
}

// TestOrchestratorAddRefusesUnlessItCanNameIdentityAllowlistAndCap is D12's
// refusal, member by member: an add that cannot name the identity (a uid, its
// email, its role), the allowlist (at least one identity in the entry) or the
// cap is refused, and each refusal happens before anything is written — the
// table's fixture is a published policy with a seeded device node, so every
// case below is one the command could otherwise carry out.
//
// The values are the ones that survive a naive check and mean something else:
// a role this release does not document (a permission nobody granted), an
// email with a space or no "@" in it (a field the rules can only bound, so the
// administration is the one place its shape is checked), a uid the database
// cannot key (ValidOrchestratorUID's reason), and a cap of zero or a negative
// one (an orchestrator whose jobs nobody bounded).
func TestOrchestratorAddRefusesUnlessItCanNameIdentityAllowlistAndCap(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no identity": {
			[]string{"orchestrator", "add", "shepherd", "--email", "alice@example.test", "--role", "member", "--cap-usd", "25"},
			"names the identity this orchestrator admits",
		},
		"an empty identity": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "", "--email", "alice@example.test", "--role", "member", "--cap-usd", "25"},
			"names the identity this orchestrator admits",
		},
		"no email": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--role", "member", "--cap-usd", "25"},
			"an administrator writes into the access record",
		},
		"an email that is not an address": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice", "--role", "member", "--cap-usd", "25"},
			"not an email address",
		},
		"an email with a space in it": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice @example.test", "--role", "member", "--cap-usd", "25"},
			"not an email address",
		},
		"an email past the length the rules bound": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", strings.Repeat("a", 250) + "@example.test", "--role", "member", "--cap-usd", "25"},
			"bound the recorded email at",
		},
		"no role": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice@example.test", "--cap-usd", "25"},
			"role",
		},
		"a role this release does not document": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice@example.test", "--role", "owner", "--cap-usd", "25"},
			"role",
		},
		"no cap": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice@example.test", "--role", "member"},
			"ceiling",
		},
		"a cap of zero": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice@example.test", "--role", "member", "--cap-usd", "0"},
			"ceiling",
		},
		"a negative cap": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice-uid", "--email", "alice@example.test", "--role", "member", "--cap-usd", "-5"},
			"ceiling",
		},
		"a uid the database cannot key": {
			[]string{"orchestrator", "add", "shepherd", "--allow", "alice.uid", "--email", "alice@example.test", "--role", "member", "--cap-usd", "25"},
			"the database refuses in a key",
		},
		"a name that is not a slug": {
			[]string{"orchestrator", "add", "Shepherd One", "--allow", "alice-uid", "--email", "alice@example.test", "--role", "member", "--cap-usd", "25"},
			"central orchestrator name",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			seedOrchestratorPolicy(t, f, "")
			seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
			_, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("orchestrator add accepted %v", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if p := publishedPolicy(t, f); len(p.Orchestrators) != 0 {
				t.Errorf("the refused add wrote the block: %+v", p.Orchestrators)
			}
			if rec := accessRecordFor(f, "alice-uid"); len(rec) != 1 {
				t.Errorf("the refused add wrote the access record: %v", rec)
			}
		})
	}
}

// TestOrchestratorAddRefusesAnIdentityWithNoDeviceNode: the access record is
// the phone's own pairing record with two administered fields on it, and `add`
// administers an existing one — it does not create it. A command that wrote a
// node where there was none would be *pairing*, which is the other half of D6's
// split and the half that owns the checks administration does not make (the
// verified email, D6 sub-question (c)); it would also hand an identity the
// phone's own families (D166's USER claim) as a side effect of an
// orchestrator admission, which is a blast radius nobody asked for.
func TestOrchestratorAddRefusesAnIdentityWithNoDeviceNode(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, "")
	_, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err == nil || !strings.Contains(err.Error(), "no device node") {
		t.Fatalf("orchestrator add for an unpaired identity: err = %v, want the refusal naming the missing node", err)
	}
	if v := f.db.Value(rules.PathDevices); v != nil {
		t.Errorf("%s = %v after the refusal, want nothing written: administration is not pairing", rules.PathDevices, v)
	}
	if p := publishedPolicy(t, f); len(p.Orchestrators) != 0 {
		t.Errorf("the refused add wrote the block: %+v", p.Orchestrators)
	}
}

// TestOrchestratorAddRefusesAProjectWithoutTheRulesTheRecordIsReadUnder: the
// command is a writer of the published policy, so it takes the gate every
// writer of that node takes — a policy written to a deployment whose rules
// predate the policy model is one the runs it bounds cannot read.
func TestOrchestratorAddRefusesAProjectWithoutTheRulesTheRecordIsReadUnder(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, "")
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	f.db.Set(projectpolicy.PathRulesMarker(), nil)
	_, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err == nil || !strings.Contains(err.Error(), "no policy marker") {
		t.Fatalf("orchestrator add without the rules marker: err = %v, want the marker refusal", err)
	}
	if rec := accessRecordFor(f, "alice-uid"); len(rec) != 1 {
		t.Errorf("the refused add administered an identity: %v", rec)
	}
}

// TestOrchestratorAddRefusesANodeWrittenByANewerFugaro: the write is a replace
// of the whole shared node, so a node this binary cannot read in full is not
// one it may write over — the publish path's P8 refusal, taken here too.
func TestOrchestratorAddRefusesANodeWrittenByANewerFugaro(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":`+strconv.Itoa(projectpolicy.Version+1)+`,"budget":{"mode":"enforce"},"allow_repos":[]}`))
	_, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err == nil || !strings.Contains(err.Error(), "upgrade Fugaro") {
		t.Fatalf("orchestrator add over a newer policy: err = %v, want the skew refusal", err)
	}
}

// TestOrchestratorAddIsETagGuardedAndWritesTheRecordLast: the block is written
// under the ETag the read returned, so a publish that lands in between is not
// overwritten — and when that write is refused, the *access record is not
// written either*.
//
// The order is the point and it is not cosmetic. Written the other way round —
// the access record first, the block second — a failure between the two
// administers an identity that no entry admits, and an administered identity
// is exactly what the deployed rules grant a seat to (ORCH reads the node and
// the role, never this block): the installation would be *looser* than the
// operator asked for. This order leaves it tighter, which is the direction a
// failure has to go.
func TestOrchestratorAddIsETagGuardedAndWritesTheRecordLast(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	c := &policyConflictOnce{rt: http.DefaultTransport, db: f.db, val: map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce"},
		"allow_repos": []string{"github/acme/app"},
	}}
	budgetTransport = c
	t.Cleanup(func() { budgetTransport = nil })

	_, _, err := execute(t, orchestratorAddArgs("shepherd")...)
	if err == nil || !strings.Contains(err.Error(), "changed while this ran") {
		t.Fatalf("orchestrator add against a concurrent writer: err = %v, want the refusal", err)
	}
	if rec := accessRecordFor(f, "alice-uid"); len(rec) != 1 {
		t.Errorf("the access record was written by an add whose policy write was refused: %v", rec)
	}
}

// TestOrchestratorRmRemovesTheEntryAndTheAdministration: removing the entry
// takes the admission away, and because the two are two views of one decision
// it takes the administration away too — otherwise the identity keeps a seat
// on every orchestrator's jobs for as long as nobody looks at the database
// (the drift `ls` reports). The pairing half stays: `rm` moves the lease and
// nothing else, which is the blast radius TestAdministeringAnIdentityDoesNot
// TouchThePhone pins on the rules side.
func TestOrchestratorRmRemovesTheEntryAndTheAdministration(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "alice@example.test", "role": rules.RoleOrchestratorMember,
	})
	out, _, err := execute(t, "orchestrator", "rm", "shepherd", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); len(p.Orchestrators) != 0 {
		t.Errorf("after the removal the block is %+v, want it empty", p.Orchestrators)
	}
	rec := accessRecordFor(f, "alice-uid")
	if _, ok := rec["email"]; ok {
		t.Errorf("access record = %v: the administered email must be gone", rec)
	}
	if _, ok := rec["role"]; ok {
		t.Errorf("access record = %v: the administered role must be gone", rec)
	}
	if rec["pairedAt"] != "2026-10-20T00:00:00Z" {
		t.Errorf("access record = %v: the pairing half must survive the removal", rec)
	}
	for _, want := range []string{"shepherd", "alice-uid"} {
		if !strings.Contains(out, want) {
			t.Errorf("the removal does not name %q:\n%s", want, out)
		}
	}
}

// TestOrchestratorRmKeepsAnIdentityAnotherEntryStillNames: the administration
// is removed only when no entry in the block names the uid any more. One
// orchestrator's removal must not unadminister an identity a second one admits
// — that would refuse a seat the operator never took away.
func TestOrchestratorRmKeepsAnIdentityAnotherEntryStillNames(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]},"warden":{"cap_usd":5,"allow":["alice-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "alice@example.test", "role": rules.RoleOrchestratorMember,
	})
	if _, _, err := execute(t, "orchestrator", "rm", "shepherd", "--yes"); err != nil {
		t.Fatal(err)
	}
	rec := accessRecordFor(f, "alice-uid")
	if rec["email"] != "alice@example.test" || rec["role"] != rules.RoleOrchestratorMember {
		t.Errorf("access record = %v, want it kept: warden still names this identity", rec)
	}
	p := publishedPolicy(t, f)
	if _, ok := p.OrchestratorFor("shepherd"); ok {
		t.Errorf("shepherd is still in the block: %+v", p.Orchestrators)
	}
	if !p.AdmittedTo("warden", "alice-uid") {
		t.Errorf("warden no longer admits alice-uid: %+v", p.Orchestrators)
	}
}

// TestOrchestratorRmRemovesOneIdentity: `rm --allow <uid>` narrows an entry
// without removing it, which is the only way to take one identity's seat away
// while the orchestrator stays. The last identity is not removable this way:
// an entry that admits nobody is a policy about nothing, and the command says
// which of the two removals the operator wants.
func TestOrchestratorRmRemovesOneIdentity(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid","bob-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "alice@example.test", "role": rules.RoleOrchestratorMember,
	})
	if _, _, err := execute(t, "orchestrator", "rm", "shepherd", "--allow", "alice-uid", "--yes"); err != nil {
		t.Fatal(err)
	}
	o, ok := publishedPolicy(t, f).OrchestratorFor("shepherd")
	if !ok || len(o.Allow) != 1 || o.Allow[0] != "bob-uid" {
		t.Fatalf("orchestrators.shepherd = %+v, want only bob-uid admitted", o)
	}
	if rec := accessRecordFor(f, "alice-uid"); rec["email"] != nil || rec["role"] != nil {
		t.Errorf("access record = %v, want the administration gone: nobody admits this identity now", rec)
	}

	_, _, err := execute(t, "orchestrator", "rm", "shepherd", "--allow", "bob-uid", "--yes")
	if err == nil || !strings.Contains(err.Error(), "remove the entry") {
		t.Fatalf("removing the last identity: err = %v, want the refusal naming the other removal", err)
	}
}

// TestOrchestratorRmRefusesWhatItCannotRemove: an orchestrator the block does
// not name, an identity the entry does not admit, and a project with no policy
// at all — three states whose remedy is a different command, each refused by
// name rather than by "nothing to remove".
func TestOrchestratorRmRefusesWhatItCannotRemove(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]}}`)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"an orchestrator the block does not name": {
			[]string{"orchestrator", "rm", "warden", "--yes"},
			"nothing to remove",
		},
		"an identity the entry does not admit": {
			[]string{"orchestrator", "rm", "shepherd", "--allow", "bob-uid", "--yes"},
			"not admitted to shepherd",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := execute(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
			}
		})
	}

	// And a project with nothing published: the same refusal `add` makes, for
	// the same reason (the block it would edit does not exist).
	g := newProjectFixture(t, "")
	_, _, err := execute(t, "orchestrator", "rm", "shepherd", "--yes")
	if err == nil || !strings.Contains(err.Error(), "no policy is published") {
		t.Fatalf("rm with no published policy: err = %v, want the refusal naming the missing policy", err)
	}
	if v := g.db.Value(rules.PathDevices); v != nil {
		t.Errorf("%s = %v, want nothing written", rules.PathDevices, v)
	}
}

// TestOrchestratorRmAsksBeforeItTakesASeatAway: a removal narrows who may act
// on the project's jobs, so it asks for the project's name at a terminal (or
// --yes) the way `project repos rm` does. Without either, a piped run is
// refused rather than taken as consent.
func TestOrchestratorRmAsksBeforeItTakesASeatAway(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "alice@example.test", "role": rules.RoleOrchestratorMember,
	})
	out, _, err := execute(t, "orchestrator", "rm", "shepherd")
	if err == nil {
		t.Fatalf("rm without a confirmation was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if _, ok := publishedPolicy(t, f).OrchestratorFor("shepherd"); !ok {
		t.Error("the unconfirmed removal removed the entry")
	}
	if rec := accessRecordFor(f, "alice-uid"); rec["email"] == nil {
		t.Errorf("the unconfirmed removal wrote the access record: %v", rec)
	}
}

// TestOrchestratorLsReportsTheDriftBetweenTheTwoSets is why `ls` reads the
// database and not only the policy: the block is the admission and the device
// node is the authorisation, the rules read the node alone, and every state
// where the two disagree is a seat an operator believes is (or is not) there.
//
// Three are reported: an identity the block admits and nobody administered
// (the rules refuse it — the block alone reaches no seat), an identity that is
// administered and that no entry names (the rules *grant* it — and whether a
// policy block admits it is beside the point, which is the residual the
// orchestratorAccess comment states), and a node that carries a role but no
// email (half-administrated, refused by the same conjunct).
func TestOrchestratorLsReportsTheDriftBetweenTheTwoSets(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, `{"shepherd":{"cap_usd":25,"allow":["alice-uid","bob-uid"]}}`)
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	seedAccessRecord(f, "carol-uid", map[string]any{
		"pairedAt": "2026-10-20T00:00:00Z", "email": "carol@example.test", "role": rules.RoleOrchestratorMember,
	})
	out, _, err := execute(t, "orchestrator", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alice-uid", "bob-uid", "carol-uid", "paired, not administered", "named by no orchestrator"} {
		if !strings.Contains(out, want) {
			t.Errorf("orchestrator ls does not report %q:\n%s", want, out)
		}
	}
}

// TestOrchestratorLsWithNothingToPrint: an installation that runs no central
// orchestrator has an empty block and no records, and `ls` says so rather than
// printing an empty list — the difference between "nobody is admitted" and
// "this project has no orchestrators" is the difference between two remedies.
func TestOrchestratorLsWithNothingToPrint(t *testing.T) {
	f := newProjectFixture(t, "")
	seedOrchestratorPolicy(t, f, "")
	out, _, err := execute(t, "orchestrator", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "runs no central orchestrator") {
		t.Errorf("orchestrator ls with an empty block:\n%s", out)
	}
}

// TestProjectPolicyPublishKeepsTheOrchestratorBlock: no command of this
// release derives the block from the operator's config — `orchestrator add`
// writes it, and it lives in the published node and nowhere else — so a
// publish that dropped it would un-admit every identity of every orchestrator,
// silently, on the next deploy of an unrelated change. It is the clone_urls and
// credentials rule, and this is the test that holds the block to it.
func TestProjectPolicyPublishKeepsTheOrchestratorBlock(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	seedAccessRecord(f, "alice-uid", map[string]any{"pairedAt": "2026-10-20T00:00:00Z"})
	if _, _, err := execute(t, orchestratorAddArgs("shepherd")...); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	o, ok := publishedPolicy(t, f).OrchestratorFor("shepherd")
	if !ok || o.CapUSD != 25 || len(o.Allow) != 1 || o.Allow[0] != "alice-uid" {
		t.Fatalf("after a publish: orchestrators.shepherd = %+v (present %v), want the block the add wrote", o, ok)
	}
}
