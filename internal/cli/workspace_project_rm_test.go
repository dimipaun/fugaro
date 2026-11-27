package cli

// workspace_project_rm_test.go is WU6's own file: what the detach does, held to
// doubles of C1's and C2's seams, with no database, no network and no cloud in
// the way (the plan's hermetic-tests rule).
//
// The doubles carry the task id in their names, as the plan's conventions
// require. Every method a detach must NOT call — the authority's Link, AddAdmin,
// RemoveAdmin and AdminBindings, the member's Bindings, PendingApply, Offboard
// and member-side Audit, the state writer's LastSaved — answers an error naming
// itself, so a command that grew one of those calls fails as the call it made
// rather than as a count that stopped matching. `Authority.Holds` is the one
// exception: it is logged and answered rather than refused, because the test
// that matters most here (`TestProjectRmAfterUnlinkRemovesTheGrants`) is about a
// detach that must reach the same end whatever a hold reads as, and a double that
// broke the run would not show that.
//
// What the doubles do NOT do is check the shape of what they are handed. The
// tests do that, off the recorded bytes: a roster the command writes back is
// decoded by C1's own reader, and the grants in it by internal/access's, so a
// write that a member's own CLI would refuse fails here.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu6Admin is the workspace's one admin in every test here, and the user the
// workspace's own node marks a grant for.
const wu6Admin = "admin-1@example.com"

// wu6OwnerUser is the person the workspace owner's own config names: the actor
// the audit entry is filed under, read from the config rather than from whatever
// git config the test machine happens to carry (localcfg.Config.Me).
const wu6OwnerUser = "person-1@example.com"

// wu6Stranger is a person the workspace's own node records a grant for without
// the admin list naming them: a mark outlives the admin list it was written
// under, and it is the mark and not the list that tells a detach what to take
// back (D2818).
const wu6Stranger = "member-2@example.com"

// wu6BetaEntry is a second member the owner's table declares and this machine
// holds no config for, which is the state a member's own config read refuses.
const wu6BetaEntry = `  beta:
    firebase_project: beta-fb
    fp: beta
    exchange_url: https://exchange.example.com/v1
`

// wu6ShutdownURL is R3's page for the fixture member's GCP project.
const wu6ShutdownURL = "https://console.cloud.google.com/iam-admin/settings?project=aurora-gcp"

// TestProjectRmSavesByDefault is D2808's first half: the member's state is saved
// before it is detached, and both places a save leaves it are named — the local
// file always, and the pull request when the member has a designated state
// repository (D2807).
func TestProjectRmSavesByDefault(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	if len(r.state.saved) != 1 {
		t.Fatalf("the state was saved %d times, want once: %v", len(r.state.saved), r.state.saved)
	}
	want := workspace.Snapshot{
		Workspace: "workspace-alpha",
		Member:    workspace.MemberTarget{Project: "aurora-gcp", Firebase: "aurora-fb"},
	}
	if got := r.state.saved[0]; got != want {
		t.Errorf("the save was of %+v, want %+v: the snapshot is the member's, filed under the workspace it is leaving", got, want)
	}
	// The member's GCP project is the one its own config names and not the
	// workspace's; the Firebase project comes from the owner's table, because
	// that is where the workspace recorded which one it mirrors into.
	for _, want := range []string{"saved aurora's state: " + r.state.written.File, r.state.written.PullRequest} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not name %q, and a save nothing points at is one nobody finds:\n%s", want, out)
		}
	}
	assertNoPrompt(t, out)
}

// TestProjectRmNoSaveStateSkipsTheSave is D2808's other half: the flag skips the
// snapshot, says so, and skips nothing else — the detach is not conditional on
// the save.
func TestProjectRmNoSaveStateSkipsTheSave(t *testing.T) {
	r, owner, _ := wu6NewRig(t)
	before := readFileString(t, owner)
	out, err := wu6Run(t, r.deps(), "aurora", "--no-save-state", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm --no-save-state: %v\n%s", err, out)
	}
	if len(r.state.saved) != 0 {
		t.Errorf("--no-save-state saved the state anyway: %v", r.state.saved)
	}
	if !strings.Contains(out, "--no-save-state") {
		t.Errorf("the run does not say the state was skipped:\n%s", out)
	}
	if len(r.members.grantsWritten) == 0 {
		t.Errorf("--no-save-state skipped the detach too: the workspace's grants are still in the member's roster:\n%s", out)
	}
	if after := readFileString(t, owner); after == before {
		t.Errorf("--no-save-state left the member declared in the workspace's table:\n%s", after)
	}
}

// TestProjectRmDeletesNothing is the spec's own sentence — rm detaches and never
// deletes — held to the command's whole surface: every call it makes is one of a
// detach's, and the member's own config is not written at all. No Google Cloud or
// Firebase resource is reached from here, and the one binding it drops — the
// workspace IAM account's roles/owner — is dropped by the authority that made it
// and by nothing in this command.
func TestProjectRmDeletesNothing(t *testing.T) {
	r, _, aurora := wu6NewRig(t)
	memberBefore := readFileString(t, aurora)
	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	// The whole of what a detach may do. A call that is not on this list is a
	// call this command grew, and each of the doubles answers one it must not
	// make with an error naming itself, so this is the second half of the same
	// fence rather than its only one.
	allowed := map[string]bool{
		"store.read":           true,
		"store.write":          true,
		"audit.create":         true,
		"authority.unlink":     true,
		"members.read-grants":  true,
		"members.write-grants": true,
		"members.read-mirror":  true,
		"members.write-mirror": true,
		"state.save":           true,
	}
	for _, call := range r.calls {
		if !allowed[call] {
			t.Errorf("project rm made the call %q, which is not one of a detach's: no Google Cloud and no Firebase resource is asked for here, and the workspace's own account is dropped by the authority that made the grant", call)
		}
	}
	if got := readFileString(t, aurora); got != memberBefore {
		t.Errorf("project rm wrote the member's own config, which is not this command's to change:\n%s", got)
	}
	if !strings.Contains(out, "nothing was deleted") {
		t.Errorf("the run does not say that nothing was deleted:\n%s", out)
	}
}

// TestProjectRmPrintsShutdownLink is R3: the console page a project is shut down
// from is printed and never opened, beside the warning naming what is still
// running and still costing money. U3 is what confirms the page is Google's own
// shut-down one, and Check 720 is what prints it.
func TestProjectRmPrintsShutdownLink(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	if !strings.Contains(out, wu6ShutdownURL) {
		t.Errorf("the run does not print %s:\n%s", wu6ShutdownURL, out)
	}
	if !strings.Contains(out, "yourself at") {
		t.Errorf("the link is not printed as the owner's own step:\n%s", out)
	}
	// The two things the spec names by hand: the rebuilds and the credentials
	// service. A warning that named neither would leave the owner to find out
	// what the project is still paying for.
	for _, what := range []string{"nothing was deleted", "Cloud Scheduler jobs", "credentials service", "keeps costing money"} {
		if !strings.Contains(out, what) {
			t.Errorf("the warning does not say %q:\n%s", what, out)
		}
	}
}

// TestProjectRmRemovesWorkspaceAdminGrants is the member side of the detach: the
// grants the workspace's own node marks this project with go out of the member's
// roster (D2818's marks, not the admin list of the moment), the mirror of the
// workspace's member list goes, the workspace's own node keeps no mark for a
// project it no longer holds, and the removal is filed in the workspace's trail.
func TestProjectRmRemovesWorkspaceAdminGrants(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	if len(r.members.grantsWritten) == 0 {
		t.Fatalf("the member's roster was not written back, so the workspace's grants are still in it:\n%s", out)
	}
	kept, err := accesslib.Decode(r.members.grantsWritten)
	if err != nil {
		t.Fatalf("the roster this command wrote is not one the member's own CLI reads: %v", err)
	}
	for _, g := range kept {
		if g.User == "user:"+wu6Admin {
			t.Errorf("the workspace admin's grant is still in the member's roster: %+v", kept)
		}
	}
	if len(kept) != 1 || kept[0].User != "user:local-1@example.com" {
		t.Errorf("the member's own grant did not survive the detach: %+v", kept)
	}
	// The write is conditional on the document that was read, as C2 requires: a
	// roster another writer moved under this one is a refusal and not an
	// overwrite.
	if got := r.members.grantsWriteDoc.ETag; got != r.members.grants.ETag {
		t.Errorf("the roster was written under the ETag %q, want the one the read answered %q", got, r.members.grants.ETag)
	}

	if got := string(r.members.mirrorWritten); got != "{}" {
		t.Errorf("the mirror was written as %q, want the empty object that leaves the node absent (R11)", got)
	}
	if got := r.members.mirrorWriteDoc.ETag; got != r.members.mirror.ETag {
		t.Errorf("the mirror was written under the ETag %q, want the one the read answered %q", got, r.members.mirror.ETag)
	}

	if len(r.store.writes) != 1 {
		t.Fatalf("the workspace's own node was written %d times, want once", len(r.store.writes))
	}
	roster, err := workspace.DecodeRoster(r.store.writes[0].raw)
	if err != nil {
		t.Fatalf("the roster this command wrote is not one its own reader accepts: %v", err)
	}
	if len(roster.Grants) != 0 {
		t.Errorf("the workspace's node still marks grants for a project it no longer holds: %+v", roster.Grants)
	}
	if got := r.store.writes[0].rev.ETag; got != "wu6-etag-1" {
		t.Errorf("the node was written under the revision %q, want the one the read answered", got)
	}

	if len(r.audit.events) != 1 {
		t.Fatalf("the workspace's trail holds %d entries from this run, want one", len(r.audit.events))
	}
	event, err := workspace.DecodeAuditEvent(r.audit.events[0])
	if err != nil {
		t.Fatalf("the audit entry this command filed is not one its own reader accepts: %v", err)
	}
	if event.Action != workspace.ActionProjectRemove {
		t.Errorf("the entry records %q, want %q", event.Action, workspace.ActionProjectRemove)
	}
	if event.Project != "aurora" || event.Subject != "" {
		t.Errorf("the entry is about project %q and subject %q, want the member project and no subject", event.Project, event.Subject)
	}
	if event.Actor != wu6OwnerUser {
		t.Errorf("the entry's actor is %q, want the config's own user %q", event.Actor, wu6OwnerUser)
	}
	if event.ID != r.audit.ids[0] {
		t.Errorf("the entry's id is %q and the sink was given %q: the id a sink creates under is the one the event carries", event.ID, r.audit.ids[0])
	}
	if got, want := r.auth.unlinked, []workspace.MemberTarget{{Project: "aurora-gcp", Firebase: "aurora-fb"}}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("the workspace's authority was dropped on %v, want once on the member's own pair of projects %v", got, want)
	}
	assertNoPrompt(t, out)
}

// TestProjectRmKeepsTheMembersOwnGrantForAnAdmin is the over-removal half of the
// match: the member's roster may hold grants for a person who is also a workspace
// admin, and the ones the workspace wrote are the ones its own node marks — the
// address, the role and the project scope together. Here the same address holds
// an all-repos admin grant the member's own admins wrote and a project-scope
// admin grant the workspace wrote: the second goes, and the first is not this
// command's to take away.
func TestProjectRmKeepsTheMembersOwnGrantForAnAdmin(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	r.members.grants = wu6Doc(t, "wu6-grants-etag", wu6GrantsDoc(t,
		// The workspace's own: the address, the role and the scope its mark names.
		wu6Grant(t, wu6Admin, accesslib.RoleAdmin),
		// The member's own for the same person: the same role at another scope,
		// which the workspace's mark for this project does not name (D2815).
		wu6GrantAt(t, wu6Admin, accesslib.RoleAdmin, "all-repos"),
	))

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	kept, err := accesslib.Decode(r.members.grantsWritten)
	if err != nil {
		t.Fatalf("the roster this command wrote is not one the member's own CLI reads: %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("the member's roster came back with %d grants, want the one the workspace did not write: %+v", len(kept), kept)
	}
	if got := kept[0]; got.User != "user:"+wu6Admin || got.Role != accesslib.RoleAdmin || got.Scope.Kind != accesslib.ScopeAllRepos {
		t.Errorf("the surviving grant is %+v, want the member's own all-repos admin grant for %s: a grant of the same address and role at another scope was not written by this workspace", got, wu6Admin)
	}
	assertNoPrompt(t, out)
}

// TestProjectRmRemovesAMarkedGrantForANonAdmin is the under-removal half: what the
// command takes back is what the workspace's own node marks for the project, and
// the admin list of the moment is not part of that question. A person who is no
// longer a workspace admin is still a person the workspace wrote a grant for, and
// leaving it behind in the member would be a grant surviving with no mark left to
// find it by.
func TestProjectRmRemovesAMarkedGrantForANonAdmin(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	// The node's marks name a person the admin list does not: the workspace wrote
	// the grant before that person stopped being an admin, and the mark is what
	// records it.
	r.store.doc = wu6Roster(t, []string{wu6Admin}, []string{wu6Admin, wu6Stranger},
		workspace.StoredGrant{Project: "aurora", User: wu6Stranger, Role: workspace.RoleViewer})
	r.members.grants = wu6Doc(t, "wu6-grants-etag", wu6GrantsDoc(t,
		wu6Grant(t, wu6Stranger, accesslib.RoleViewer),
		wu6Grant(t, "local-1@example.com", accesslib.RoleViewer),
	))

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm: %v\n%s", err, out)
	}
	kept, err := accesslib.Decode(r.members.grantsWritten)
	if err != nil {
		t.Fatalf("the roster this command wrote is not one the member's own CLI reads: %v", err)
	}
	for _, g := range kept {
		if g.User == "user:"+wu6Stranger {
			t.Errorf("the grant the workspace's own node marks is still in the member's roster: %+v", kept)
		}
	}
	if len(kept) != 1 || kept[0].User != "user:local-1@example.com" {
		t.Errorf("the member's own grant did not survive the detach: %+v", kept)
	}
	assertNoPrompt(t, out)
}

// TestProjectRmAfterUnlinkRemovesTheGrants is D2822, which is the decision this
// command exists for: a member owner's unlink drops the authority and only the
// authority, so the workspace's stored grants and the mirror are still there
// afterwards and `project rm` is what takes them back. The hold reads false in
// this run — the account owns nothing on the project any more — and the detach
// reaches the same end regardless: a command that keyed the grants removal on
// Holds would leave them in a member whose owner had already left.
func TestProjectRmAfterUnlinkRemovesTheGrants(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	r.auth.holds = false

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm after an unlink: %v\n%s", err, out)
	}
	if r.did("authority.holds") {
		t.Errorf("the detach asked whether the workspace still holds the member, and D2822's whole point is that it must not: after a member owner's unlink the answer is no, and the grants and the mirror are exactly what is left to take back")
	}
	if len(r.members.grantsWritten) == 0 {
		t.Errorf("the workspace's grants were left in the member's roster after its owner's unlink, which is the state `status` calls unlinked with grants left:\n%s", out)
	}
	if len(r.members.mirrorWritten) == 0 {
		t.Errorf("the mirror was left in the member's database after its owner's unlink:\n%s", out)
	}
	if len(r.auth.unlinked) != 1 {
		t.Errorf("the authority was dropped %d times, want once: Unlink is idempotent, and what it is asked for is a project the account does not own", len(r.auth.unlinked))
	}
}

// TestProjectRmFolderMechanismNamesTheOwnersUnlink is U6: where the workspace
// holds its members by a folder, the move back is the member owner's act and the
// command says so instead of doing it — Check 720 is what settles whether a
// folder owner can make the move for them. The member side of the detach is the
// same either way.
func TestProjectRmFolderMechanismNamesTheOwnersUnlink(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	r.auth.kind = workspace.AuthorityFolder

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm on the folder mechanism: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fugaro workspace project unlink") {
		t.Errorf("the run does not name the member owner's own command (U6):\n%s", out)
	}
	if r.did("authority.unlink") {
		t.Errorf("the folder mechanism dropped an authority, and the move back is the member owner's (U6)")
	}
	if len(r.members.grantsWritten) == 0 {
		t.Errorf("the folder mechanism skipped the member side, which is the same detach either way:\n%s", out)
	}
}

// TestProjectRmReReadsAMovedRoster is C1's rule for a write that lost a race:
// rtdb.ErrPrecondition is an ANSWER and not a failure — the node is read again
// and the change judged against what is really there — so a change this command
// never saw survives, and only this project's own marks come out. The second
// write is made under the revision the re-read answered, or it would lose the
// same way.
func TestProjectRmReReadsAMovedRoster(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	r.store.movedDoc = wu6RosterDoc(t,
		workspace.StoredGrant{Project: "aurora", User: wu6Admin, Role: workspace.RoleAdmin},
		workspace.StoredGrant{Project: "beta", User: wu6Admin, Role: workspace.RoleAdmin},
	)
	r.store.moved = 1
	r.store.movedETag = "wu6-etag-2"

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm against a node that moved: %v\n%s", err, out)
	}
	if len(r.store.writes) != 1 {
		t.Fatalf("the node was written %d times, want once after the re-read", len(r.store.writes))
	}
	if got := r.store.writes[0].rev.ETag; got != "wu6-etag-2" {
		t.Errorf("the second write was made under %q, want the revision the re-read answered: a write under the first one would lose the same way", got)
	}
	roster, err := workspace.DecodeRoster(r.store.writes[0].raw)
	if err != nil {
		t.Fatalf("the roster written after the re-read is not one its own reader accepts: %v", err)
	}
	if len(roster.Grants) != 1 || roster.Grants[0].Project != "beta" {
		t.Errorf("the written roster holds %+v, want the other writer's mark for beta kept and only this project's taken out", roster.Grants)
	}
	if !strings.Contains(out, "dropped the marks") {
		t.Errorf("the run does not say the marks were dropped:\n%s", out)
	}
}

// TestProjectRmReReadsAMovedRosterActsOnANewMark is the other half of the
// re-read, and the half a detach can least afford to get wrong: the node moved
// because somebody wrote a mark for this project while the command ran, so
// dropping the mark and leaving the grant behind would leave access the
// workspace wrote into a project the owner's table no longer declares, with the
// mark — the only record that could find it — gone. The new mark is acted on
// with the ones this command set out with, the grant behind it comes out of the
// member's roster, and the run says so.
func TestProjectRmReReadsAMovedRosterActsOnANewMark(t *testing.T) {
	r, _, _ := wu6NewRig(t)
	r.members.grants = wu6Doc(t, "wu6-grants-etag", wu6GrantsDoc(t,
		wu6Grant(t, wu6Admin, accesslib.RoleAdmin),
		wu6Grant(t, wu6Stranger, accesslib.RoleViewer),
		wu6Grant(t, "local-1@example.com", accesslib.RoleViewer),
	))
	r.store.movedDoc = wu6Roster(t, []string{wu6Admin}, []string{wu6Admin, wu6Stranger},
		workspace.StoredGrant{Project: "aurora", User: wu6Admin, Role: workspace.RoleAdmin},
		workspace.StoredGrant{Project: "aurora", User: wu6Stranger, Role: workspace.RoleViewer},
		workspace.StoredGrant{Project: "beta", User: wu6Admin, Role: workspace.RoleAdmin},
	)
	r.store.moved = 1
	r.store.movedETag = "wu6-etag-2"

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err != nil {
		t.Fatalf("workspace project rm against a node that moved and grew a mark: %v\n%s", err, out)
	}
	if len(r.store.writes) != 1 {
		t.Fatalf("the node was written %d times, want once after the re-read", len(r.store.writes))
	}
	roster, err := workspace.DecodeRoster(r.store.writes[0].raw)
	if err != nil {
		t.Fatalf("the roster written after the re-read is not one its own reader accepts: %v", err)
	}
	if len(roster.Grants) != 1 || roster.Grants[0].Project != "beta" {
		t.Errorf("the written roster holds %+v, want the other writer's mark for beta kept and every mark for this project taken out", roster.Grants)
	}
	kept, err := accesslib.Decode(r.members.grantsWritten)
	if err != nil {
		t.Fatalf("the roster this command wrote is not one the member's own CLI reads: %v", err)
	}
	for _, g := range kept {
		if g.User == "user:"+wu6Stranger {
			t.Errorf("the grant behind the mark written while the command ran is still in the member's roster, with its mark written away: %+v", kept)
		}
	}
	if len(kept) != 1 || kept[0].User != "user:local-1@example.com" {
		t.Errorf("the member's own grant did not survive the detach: %+v", kept)
	}
	if !strings.Contains(out, "moved under this command") || !strings.Contains(out, wu6Stranger) {
		t.Errorf("the run does not say the node moved and that the grant it recorded meanwhile was taken back:\n%s", out)
	}
}

// TestProjectRmNeedsTheMembersOwnConfig: the member's GCP project names every
// call this command makes, the workspace's table does not carry it — it holds the
// member's Fugaro project name and the endpoints the workspace reaches it by —
// and only the member's own config does. So a machine that holds no config for
// the member is refused rather than guessed at: guessing here would write a grant
// into somebody else's project.
func TestProjectRmNeedsTheMembersOwnConfig(t *testing.T) {
	r, owner, _ := wu6NewRig(t)
	workspaceAppend(t, owner, wu6BetaEntry)

	out, err := wu6Run(t, r.deps(), "beta", "--no-apply")
	if err == nil {
		t.Fatalf("project rm detached a member this machine cannot name a GCP project for:\n%s", out)
	}
	if !strings.Contains(err.Error(), "fugaro setup --name beta") {
		t.Errorf("the refusal does not name the fix: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("the refusal had already made the calls %v", r.calls)
	}
}

// TestProjectRmOnlyOnTheTablesMachine is D2821: the member table is the
// workspace's own project config's, and its owner's `fugaro update` publishes it
// whole, so this command runs from that config and refuses anywhere else. Run
// from a member's own config it is a mistake about which half of the membership
// is being edited.
func TestProjectRmOnlyOnTheTablesMachine(t *testing.T) {
	r, _, aurora := wu6NewRig(t)
	t.Setenv("FUGARO_CONFIG", aurora)

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err == nil {
		t.Fatalf("project rm ran from a member's own config:\n%s", out)
	}
	if !strings.Contains(err.Error(), "D2821") {
		t.Errorf("the refusal does not name the rule it is applying: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("the refusal had already made the calls %v", r.calls)
	}
	if got := readFileString(t, aurora); !strings.Contains(got, "name: aurora") {
		t.Errorf("the refusal rewrote the member's own config:\n%s", got)
	}
}

// TestProjectRmDropsTheOwnersTableEntry: detaching is also the workspace's own
// bookkeeping — the member's entry comes out of the owner's table, the table goes
// with the last member of it, and the file keeps every key the editor does not
// touch.
func TestProjectRmDropsTheOwnersTableEntry(t *testing.T) {
	r, owner, _ := wu6NewRig(t)
	if _, err := wu6Run(t, r.deps(), "aurora", "--no-apply"); err != nil {
		t.Fatalf("workspace project rm: %v", err)
	}
	after := readFileString(t, owner)
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("the owner's config does not read back after the write: %v\n%s", err, after)
	}
	if lc.WorkspaceMembers != nil {
		if _, ok := (*lc.WorkspaceMembers)["aurora"]; ok {
			t.Errorf("the workspace's table still declares aurora:\n%s", after)
		}
	}
	if want := "user: " + wu6OwnerUser; !strings.Contains(after, want) {
		t.Errorf("the write dropped a key it does not own (%q):\n%s", want, after)
	}
}

// TestProjectRmRefusesInACodingAgentsSession is A7 for this command: a change to
// what the workspace holds is the owner's own act, so a coding agent's session is
// refused before the first read, and the refusal names the flag that does not
// lift it.
func TestProjectRmRefusesInACodingAgentsSession(t *testing.T) {
	r, owner, _ := wu6NewRig(t)
	before := readFileString(t, owner)
	t.Setenv(agentMarkers[0], "1")

	out, err := wu6Run(t, r.deps(), "aurora", "--no-apply")
	if err == nil {
		t.Fatalf("project rm ran in a coding agent's session:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--no-save-state") {
		t.Errorf("the refusal does not say which flag it is asked about (--no-save-state): %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("the refusal had already made the calls %v", r.calls)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the owner's config:\n%s", after)
	}
	assertNoPrompt(t, out)
}

// wu6Rig is one run's world: this file's doubles, the log they share, and the
// fixture's configs.
type wu6Rig struct {
	calls []string

	store   *wu6Store
	audit   *wu6Audit
	auth    *wu6Authority
	members *wu6Members
	state   *wu6State
}

func (r *wu6Rig) deps() workspaceProjectRmDeps {
	return workspaceProjectRmDeps{
		Roster:    r.store,
		Audit:     r.audit,
		Authority: r.auth,
		Members:   r.members,
		State:     r.state,
	}
}

func (r *wu6Rig) log(call string) { r.calls = append(r.calls, call) }

// did reports whether the run made one call, which is how the tests say what a
// detach must not do where the double answers rather than refuses: Holds is the
// one a run can make without failing on it.
func (r *wu6Rig) did(call string) bool {
	for _, c := range r.calls {
		if c == call {
			return true
		}
	}
	return false
}

// wu6NewRig is the fixture every test here starts from: the workspace's own
// project config — selected, and holding the member table — beside the member's
// own, a workspace node carrying one admin and one mark, a member roster holding
// that admin's grant and one of its own, and a mirror.
func wu6NewRig(t *testing.T) (*wu6Rig, string, string) {
	t.Helper()
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, "user: "+wu6OwnerUser+"\n")
	workspaceAppend(t, owner, workspaceOwnerTable)
	t.Setenv("FUGARO_CONFIG", owner)

	r := &wu6Rig{}
	r.store = &wu6Store{
		rig:  r,
		path: "workspaceRosters/workspace-alpha",
		rev:  workspace.Revision{ETag: "wu6-etag-1", Found: true},
		doc: wu6RosterDoc(t, workspace.StoredGrant{
			Project: "aurora", User: wu6Admin, Role: workspace.RoleAdmin,
		}),
	}
	r.audit = &wu6Audit{rig: r}
	r.auth = &wu6Authority{rig: r, kind: workspace.AuthoritySA, holds: true}
	r.members = &wu6Members{
		rig: r,
		grants: wu6Doc(t, "wu6-grants-etag", wu6GrantsDoc(t,
			wu6Grant(t, wu6Admin, accesslib.RoleAdmin),
			wu6Grant(t, "local-1@example.com", accesslib.RoleViewer),
		)),
		mirror: wu6Doc(t, "wu6-mirror-etag",
			[]byte(`{"members":[{"address":"`+wu6Admin+`","kind":"human","external":false}]}`)),
	}
	r.state = &wu6State{
		rig:     r,
		written: workspace.SnapshotWritten{File: "/state/aurora-settings.md", PullRequest: "https://example.com/owner/state/pull/1"},
	}
	return r, owner, aurora
}

// wu6Run drives the command this file is about the way I1 will register it: built
// by its own constructor with the doubles handed in, and with its input closed so
// that a run which asked something would sit there until the package's deadline
// failed it (D1540). The tests that are not about the owner-half chain pass
// --no-apply, the family's own flag, which leaves the change saved instead of
// running a converge on a fixture whose workspace has never been applied.
func wu6Run(t *testing.T, deps workspaceProjectRmDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newWorkspaceProjectRmCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// wu6RosterDoc is the workspace's own node as its reader accepts it: one admin,
// and the marks the workspace keeps.
func wu6RosterDoc(t *testing.T, marks ...workspace.StoredGrant) []byte {
	t.Helper()
	return wu6Roster(t, []string{wu6Admin}, []string{wu6Admin}, marks...)
}

// wu6Roster is the workspace's own node with the admin list and the people it
// names given rather than fixed: the two answer different questions, and the
// difference is what a detach must not confuse (D2818).
func wu6Roster(t *testing.T, admins, people []string, marks ...workspace.StoredGrant) []byte {
	t.Helper()
	person := func(address string) workspace.Person {
		return workspace.Person{Address: address, Kind: workspace.KindHuman}
	}
	named := make([]workspace.Person, 0, len(people))
	for _, address := range people {
		named = append(named, person(address))
	}
	raw, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People:    named,
		Admins:    admins,
		Grants:    marks,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// wu6GrantsDoc is a member's own roster as the member's CLI writes it.
func wu6GrantsDoc(t *testing.T, grants ...accesslib.Grant) []byte {
	t.Helper()
	raw, err := accesslib.Encode(grants)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// wu6Grant is one stored grant of one address at the project scope: the scope
// that covers everything a member has, which is what the workspace's own grant to
// an admin is written with (D2815).
func wu6Grant(t *testing.T, address string, role accesslib.Role) accesslib.Grant {
	t.Helper()
	member, err := accesslib.Member(address)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := accesslib.ParseScope("project")
	if err != nil {
		t.Fatal(err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: scope}
}

// wu6GrantAt is one stored grant at a scope the test names: the same address and
// the same role at another scope is a different grant, which is how a test says
// a member's own grant is not one the workspace's mark for this project names
// (D2815).
func wu6GrantAt(t *testing.T, address string, role accesslib.Role, scopeValue string) accesslib.Grant {
	t.Helper()
	member, err := accesslib.Member(address)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := accesslib.ParseScope(scopeValue)
	if err != nil {
		t.Fatal(err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: scope}
}

// wu6Doc is one of the member's own documents as its database holds it.
func wu6Doc(t *testing.T, etag string, raw []byte) workspace.MemberDoc {
	t.Helper()
	return workspace.MemberDoc{Raw: raw, ETag: etag, Found: true}
}

// wu6Store is C1's Store over a document in memory. `moved` is how many writes
// the node refuses with rtdb.ErrPrecondition before settling on movedDoc, which
// is how the read-again path is reached without a database.
type wu6Store struct {
	rig       *wu6Rig
	path      string
	rev       workspace.Revision
	doc       []byte
	moved     int
	movedDoc  []byte
	movedETag string

	writes []wu6RosterWrite
}

// wu6RosterWrite is one write of the workspace's node: the revision it was made
// under and the bytes, so a test can say both were the ones the contract needs.
type wu6RosterWrite struct {
	rev workspace.Revision
	raw []byte
}

func (s *wu6Store) Path() string { return s.path }

func (s *wu6Store) Read(context.Context) (workspace.Revision, []byte, error) {
	s.rig.log("store.read")
	return s.rev, s.doc, nil
}

func (s *wu6Store) Write(_ context.Context, rev workspace.Revision, raw []byte) error {
	s.rig.log("store.write")
	if s.moved > 0 {
		s.moved--
		if s.movedDoc != nil {
			s.doc, s.rev = s.movedDoc, workspace.Revision{ETag: s.movedETag, Found: true}
			s.movedDoc = nil
		}
		return rtdb.ErrPrecondition
	}
	s.writes = append(s.writes, wu6RosterWrite{rev: rev, raw: raw})
	s.doc, s.rev = raw, workspace.Revision{ETag: "wu6-etag-written", Found: true}
	return nil
}

// wu6Audit is C1's AuditSink: it records what it was asked to create, and never
// updates one (the trail is create-only).
type wu6Audit struct {
	rig    *wu6Rig
	ids    []string
	events [][]byte
}

func (a *wu6Audit) Create(_ context.Context, id string, event []byte) error {
	a.rig.log("audit.create")
	a.ids = append(a.ids, id)
	a.events = append(a.events, event)
	return nil
}

// wu6Authority is C2's Authority. `holds` is what the account holds on the member
// now, which a member owner's own unlink has already taken away; the detach must
// reach the same end either way (D2822). Every other method answers an error
// naming itself: a detach links nothing, adds no admin and removes none, and
// reads no bindings.
type wu6Authority struct {
	rig      *wu6Rig
	kind     workspace.AuthorityKind
	holds    bool
	unlinked []workspace.MemberTarget
}

func (a *wu6Authority) Kind() workspace.AuthorityKind { return a.kind }

func (a *wu6Authority) Link(context.Context, workspace.MemberTarget) error {
	a.rig.log("authority.link")
	return errors.New("wu6: project rm called Authority.Link")
}

func (a *wu6Authority) Unlink(_ context.Context, m workspace.MemberTarget) error {
	a.rig.log("authority.unlink")
	a.unlinked = append(a.unlinked, m)
	a.holds = false
	return nil
}

func (a *wu6Authority) Holds(context.Context, workspace.MemberTarget) (bool, error) {
	a.rig.log("authority.holds")
	return a.holds, nil
}

func (a *wu6Authority) AddAdmin(context.Context, string) error {
	a.rig.log("authority.add-admin")
	return errors.New("wu6: project rm called Authority.AddAdmin")
}

func (a *wu6Authority) RemoveAdmin(context.Context, string) error {
	a.rig.log("authority.remove-admin")
	return errors.New("wu6: project rm called Authority.RemoveAdmin")
}

func (a *wu6Authority) AdminBindings(context.Context) ([]string, error) {
	a.rig.log("authority.admin-bindings")
	return nil, errors.New("wu6: project rm called Authority.AdminBindings")
}

// wu6Members is C2's MemberOps over the member's own documents: the grants node
// and the mirror, each handed back with the ETag it was read at so a test can say
// the write was made under it. A write lands in the document it was made on, so a
// read after it answers what the member's roster holds now — which is what a
// command that reads the same roster twice depends on.
type wu6Members struct {
	rig    *wu6Rig
	grants workspace.MemberDoc
	mirror workspace.MemberDoc

	grantsWritten  []byte
	grantsWriteDoc workspace.MemberDoc
	mirrorWritten  []byte
	mirrorWriteDoc workspace.MemberDoc
}

func (m *wu6Members) ReadGrants(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	m.rig.log("members.read-grants")
	return m.grants, nil
}

func (m *wu6Members) WriteGrants(_ context.Context, _ workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	m.rig.log("members.write-grants")
	doc.Raw = raw
	m.grantsWritten, m.grantsWriteDoc = raw, doc
	m.grants = doc
	return nil
}

func (m *wu6Members) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	m.rig.log("members.read-mirror")
	return m.mirror, nil
}

func (m *wu6Members) WriteMirror(_ context.Context, _ workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	m.rig.log("members.write-mirror")
	m.mirrorWritten, m.mirrorWriteDoc = raw, doc
	return nil
}

func (m *wu6Members) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	m.rig.log("members.bindings")
	return nil, errors.New("wu6: project rm called MemberOps.Bindings")
}

func (m *wu6Members) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	m.rig.log("members.pending-apply")
	return workspace.Pending{}, errors.New("wu6: project rm called MemberOps.PendingApply")
}

func (m *wu6Members) Offboard(context.Context, workspace.MemberTarget, string) error {
	m.rig.log("members.offboard")
	return errors.New("wu6: project rm called MemberOps.Offboard")
}

func (m *wu6Members) RecordRemoval(context.Context, workspace.MemberTarget, string) error {
	m.rig.log("members.record-removal")
	return errors.New("wu6: project rm called MemberOps.RecordRemoval")
}

func (m *wu6Members) Audit(context.Context, workspace.MemberTarget, string, []byte) error {
	m.rig.log("members.audit")
	return errors.New("wu6: project rm called MemberOps.Audit: a project's removal is filed in the workspace's own trail")
}

// wu6State is C2's StateWriter: what the save was of, and where it left it.
type wu6State struct {
	rig     *wu6Rig
	saved   []workspace.Snapshot
	written workspace.SnapshotWritten
}

func (s *wu6State) Save(_ context.Context, snap workspace.Snapshot) (workspace.SnapshotWritten, error) {
	s.rig.log("state.save")
	s.saved = append(s.saved, snap)
	return s.written, nil
}

func (s *wu6State) LastSaved(context.Context, workspace.Snapshot) (time.Time, bool, error) {
	s.rig.log("state.last-saved")
	return time.Time{}, false, errors.New("wu6: project rm called StateWriter.LastSaved")
}

// The doubles are the whole of the contracts this command is handed, so a change
// to C1 or C2 fails here rather than at I1.
var (
	_ workspace.Store       = (*wu6Store)(nil)
	_ workspace.AuditSink   = (*wu6Audit)(nil)
	_ workspace.Authority   = (*wu6Authority)(nil)
	_ workspace.MemberOps   = (*wu6Members)(nil)
	_ workspace.StateWriter = (*wu6State)(nil)
)
