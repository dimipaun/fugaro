package cli

// `fugaro user offboard EMAIL` (DS2.4, D1935): the admin's command for taking
// one member off the installation — the roster drops every grant that names
// them, and the intake sweeps the prefix they stored.
//
// The rig is the installation's own config: the database is the RTDB fake (the
// roster is a node of it), the credentials service block is deployed (the
// intake's address is read out of it), and the one seam every intake call goes
// through is the fake beside it. No test reaches a database, a service or a
// credential.

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// offboardRig is one fixture installation: its database, and the member every
// invocation runs as.
type offboardRig struct {
	db *gcpfake.RTDB
}

// offboardAdmin is the address of the admin the fixture's roster holds: the
// caller every test offboards as, unless it is the point of the test that the
// caller is not one.
const offboardAdmin = "owner@example.com"

// offboardMemberAddress is the address of the member the fixture's roster
// holds and offboarding names.
const offboardMemberAddress = "member@example.com"

// newOffboardRig writes the project config the command reads and seeds the
// roster it decides against.
func newOffboardRig(t *testing.T, caller string, grants ...map[string]any) *offboardRig {
	t.Helper()
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	db := gcpfake.NewRTDB(t)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + db.URL + " }\n" +
		"credentials_service:\n  url: " + intakeTestCredentialsURL + "\n" +
		"endpoints: { no_auth: true }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	stubSelfCommand(t)
	db.Set(budget.PathProject, "aurora")
	db.Set(budget.PathMark, auroraMark())
	setRosterOn(t, db, grants...)
	prev := offboardCaller
	offboardCaller = func(context.Context) (string, error) { return accesslib.Member(caller) }
	t.Cleanup(func() { offboardCaller = prev })
	return &offboardRig{db: db}
}

// roster is the grants node as the fake holds it now: what the command left,
// read back through the node's own decoder.
func (r *offboardRig) roster(t *testing.T) []accesslib.Grant {
	t.Helper()
	raw, err := accesslib.Encode(rosterGrantsOf(t, r.db))
	if err != nil {
		t.Fatalf("encoding the fixture roster: %v", err)
	}
	grants, err := accesslib.Decode(raw)
	if err != nil {
		t.Fatalf("the roster the command wrote is not one this release reads: %v", err)
	}
	return grants
}

// rosterGrantsOf reads the node back out of the fake as the two fields the
// node's document carries, so a test asserts on what a reader of the database
// would find and never on the command's own view of it.
func rosterGrantsOf(t *testing.T, db *gcpfake.RTDB) []accesslib.Grant {
	t.Helper()
	v, ok := db.Value(accesslib.PathNode).(map[string]any)
	if !ok {
		t.Fatalf("%s is not a document in the database", accesslib.PathNode)
	}
	list, ok := v["grants"].([]any)
	if !ok {
		t.Fatalf("%s carries no grants list", accesslib.PathNode)
	}
	grants := make([]accesslib.Grant, 0, len(list))
	for _, entry := range list {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("a grant of %s is not an object", accesslib.PathNode)
		}
		role, _ := row["role"].(string)
		scope, _ := row["scope"].(string)
		user, _ := row["user"].(string)
		parsed, err := accesslib.ParseScope(scope)
		if err != nil {
			t.Fatalf("the stored scope %q does not parse: %v", scope, err)
		}
		grants = append(grants, accesslib.Grant{User: user, Role: accesslib.Role(role), Scope: parsed})
	}
	return grants
}

// offboardLocalConfig is the fixture's own project config, as the command reads
// it: the write's last-admin refusal names the installation, so a test that
// calls the write directly hands it the same config the command would.
func offboardLocalConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatalf("reading the fixture's config: %v", err)
	}
	lc, err := localcfg.Parse(raw)
	if err != nil {
		t.Fatalf("the fixture's config does not parse: %v", err)
	}
	return lc
}

// rosterNames reports whether any stored grant names the member.
func rosterNames(grants []accesslib.Grant, member string) bool {
	for _, g := range grants {
		if g.User == member {
			return true
		}
	}
	return false
}

// TestOffboardRefusesSelfOffboard: an admin does not take themselves off the
// roster, and the refusal lands before anything is written or swept. F17.2
// keeps `fugaro user rm`'s self-removal ruling for this door, and the reason
// transfers whole: the caller's own admin grant is what admitted them, so this
// is the one act from here that can leave the installation with no project
// admin at all. The roster below holds a second project admin, so what is
// refused is self-removal itself and not the last-admin rule — an installation
// that still has an admin does not let one go this way either — and the
// caller's own stored secrets are left alone.
func TestOffboardRefusesSelfOffboard(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant("deputy@example.com", "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	seen := fakeIntakeCalls(t, nil)

	out, _, err := execute(t, "user", "offboard", offboardAdmin)
	if err == nil {
		t.Fatalf("an admin took themselves off the roster:\n%s", out)
	}
	if !strings.Contains(err.Error(), "identity this invocation is running as") {
		t.Errorf("the refusal does not say the member named is the caller this command runs as: %v", err)
	}
	if !strings.Contains(err.Error(), "F17.2") {
		t.Errorf("the refusal does not name the rule it is the roster's own version of: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("the refused command still called the intake (%d calls): nothing of the caller's may be swept before the member is placed", len(*seen))
	}
	grants := r.roster(t)
	for _, want := range []string{"user:" + offboardAdmin, "user:deputy@example.com", "user:" + offboardMemberAddress} {
		if !rosterNames(grants, want) {
			t.Errorf("the refused self-offboarding dropped %s's grant too, and it writes nothing:\n%v", want, grants)
		}
	}
}

// TestOffboardRefusesEmptyingTheAdminList drives the write's own half of F17.2:
// a roster whose project-scope admin grants would be gone is not landed, because
// the admin list the next `fugaro update` publishes from that node is what
// admits anybody to this command and to the intake's shared prefix (D1940), and
// an empty one also drops every admin binding on the apply.
//
// The rule lives in the write rather than in the command's own checks, and this
// test drives it there, because the command's path cannot reach it: the caller
// holds the project-scope admin grant that admitted them, so the only drop that
// empties the published list is one that takes the caller's own grant off, and
// the self-refusal above refuses that first. What the write adds is the node
// being written — a retry may be landing on a roster whose other admins were
// just removed — which is what this calls it with.
//
// A node whose admin list is ALREADY empty is not refused: this write did not
// empty it, and a refusal there would shut a door that is already shut. The
// second half of the test is that boundary.
func TestOffboardRefusesEmptyingTheAdminList(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	cl, err := newBudgetClient(t.Context(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	lc := offboardLocalConfig(t)

	// The roster's only project-scope admin grant is the one the write is asked
	// to take off.
	if _, err := offboardWriteRoster(t.Context(), cl, lc, accessNode(t, r.db), "user:"+offboardAdmin); err == nil {
		t.Fatalf("the write landed a roster with no project-scope admin grant left")
	} else if !strings.Contains(err.Error(), "no project-scope admin grant") {
		t.Errorf("the refusal does not say what the roster would be left without: %v", err)
	}
	if grants := r.roster(t); !rosterNames(grants, "user:"+offboardAdmin) {
		t.Errorf("the refused write took the last project admin off the roster anyway:\n%v", grants)
	}

	// The already-empty roster: no project admin is on it, so nothing this write
	// does can empty it, and the drop lands.
	setRosterOn(t, r.db,
		rosterGrant(offboardMemberAddress, "operator", "project"),
		rosterGrant("viewer@example.com", "viewer", "all-repos"),
	)
	dropped, err := offboardWriteRoster(t.Context(), cl, lc, accessNode(t, r.db), "user:"+offboardMemberAddress)
	if err != nil {
		t.Fatalf("the write refused a roster whose admin list was already empty, which this write did not empty: %v", err)
	}
	if dropped != 1 {
		t.Errorf("the write reports %d grants dropped, want the member's one", dropped)
	}
	if grants := r.roster(t); rosterNames(grants, "user:"+offboardMemberAddress) {
		t.Errorf("the member is still on the roster:\n%v", grants)
	}
}

// TestOffboardRosterWriteSurvivesAConcurrentGrant drives the write the command
// makes when another admin moved the roster between this command's read and its
// write: the node it read no longer matches, so the write is refused, the roster
// is read again and the drop is computed again — and the count the command
// reports is the count of the write that landed, not of the picture it first
// read. Landing the first picture instead would undo the grant the other admin
// added while this one ran, which is the whole reason the write is conditional
// (F23).
func TestOffboardRosterWriteSurvivesAConcurrentGrant(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	cl, err := newBudgetClient(t.Context(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	stale := accessNode(t, r.db)
	// Another admin writes the roster, adding a grant, while this command holds
	// the picture above.
	setRosterOn(t, r.db,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
		rosterGrant("deputy@example.com", "admin", "project"),
	)

	dropped, err := offboardWriteRoster(t.Context(), cl, offboardLocalConfig(t), stale, "user:"+offboardMemberAddress)
	if err != nil {
		t.Fatalf("the write gave up on a roster another admin had moved: %v", err)
	}
	if dropped != 1 {
		t.Errorf("the write reports %d grants dropped, want 1: the count is what the write that landed removed, and the roster it landed on held the member once", dropped)
	}
	grants := r.roster(t)
	if rosterNames(grants, "user:"+offboardMemberAddress) {
		t.Errorf("the member is still on the roster after the retry:\n%v", grants)
	}
	if !rosterNames(grants, "user:deputy@example.com") {
		t.Errorf("the retry landed the picture the command first read and undid the grant another admin added while it ran:\n%v", grants)
	}
}

// TestOffboardDeletesPrefix: offboarding names the member by their address, the
// intake sweeps the prefix that address owns — the admin-only form of the
// delete route, found by the owner annotation the intake wrote when the
// credential was stored (R10) — and the command reports the names it removed.
// It is the member's own prefix and never a repository's or the project's: the
// call carries the user scope and the owner, and no name.
func TestOffboardDeletesPrefix(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	seen := fakeIntakeCalls(t, func(c secretsIntakeCall) {
		ans, ok := c.Out.(*intake.DeleteAnswer)
		if !ok {
			t.Fatalf("the sweep asks for %T, want the intake's own DeleteAnswer", c.Out)
		}
		*ans = intake.DeleteAnswer{Deleted: []string{"fugaro_u_4242_github-token", "fugaro_u_4242_npm-token"}}
	})

	out, _, err := execute(t, "user", "offboard", offboardMemberAddress)
	if err != nil {
		t.Fatalf("user offboard: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want one (the sweep)", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestIntakeURL {
		t.Errorf("the sweep asked %s, want the installation's intake (%s): the address is the installation's own and never a flag", got.URL, intakeTestIntakeURL)
	}
	if got.Call.Path != intake.DeletePath {
		t.Errorf("the sweep asked %q, want the intake's delete route (%q)", got.Call.Path, intake.DeletePath)
	}
	req, ok := got.Call.Body.(intake.DeleteRequest)
	switch {
	case !ok:
		t.Fatalf("the sweep sent %#v, want the intake's own DeleteRequest", got.Call.Body)
	case req.Scope != intake.ScopeUser:
		t.Errorf("the sweep sent scope %q, want the member's own prefix (%q): a shared or a platform container is not a person's to leave behind", req.Scope, intake.ScopeUser)
	case req.Owner != offboardMemberAddress:
		t.Errorf("the sweep names the owner %q, want the member's address (%s): the containers are found by the owner annotation and never by a name the caller guessed", req.Owner, offboardMemberAddress)
	case req.Name != "":
		t.Errorf("the sweep sent the name %q, want the whole prefix: offboarding removes every container the member owns", req.Name)
	}
	for _, id := range []string{"fugaro_u_4242_github-token", "fugaro_u_4242_npm-token"} {
		if !strings.Contains(out, id) {
			t.Errorf("the command does not name %q, the container the intake removed:\n%s", id, out)
		}
	}
	if grants := r.roster(t); rosterNames(grants, "user:"+offboardMemberAddress) {
		t.Errorf("the roster still names %s: offboarding removes their grants as well as their secrets\n%v", offboardMemberAddress, grants)
	}
}

// TestOffboardRefusedForNonAdmin: offboarding is an admin's act, and the admin
// is the roster's own: a caller holding an operator's grant is refused, nothing
// is removed from the intake and the roster is left exactly as it was. The
// refusal names what is missing and the direction it fails in: the check passes
// no implicit set to access's resolution, so a project's GCP owner or editor —
// which holds no stored grant — resolves to no role and is refused here as the
// intake refuses them (D1940). This fixture is the stored-grant case; the
// implicit one is the same line of code with nothing in Input.Implicit.
func TestOffboardRefusedForNonAdmin(t *testing.T) {
	r := newOffboardRig(t, "operator@example.com",
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant("operator@example.com", "operator", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	seen := fakeIntakeCalls(t, nil)

	out, _, err := execute(t, "user", "offboard", offboardMemberAddress)
	if err == nil {
		t.Fatalf("a caller whose roster grant is an operator's offboarded a member:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not a project-scope admin") {
		t.Errorf("the refusal does not say the caller holds no admin grant of the roster: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("the refused command still called the intake (%d calls): nothing may be removed before the caller is placed", len(*seen))
	}
	if grants := r.roster(t); !rosterNames(grants, "user:"+offboardMemberAddress) {
		t.Errorf("the refused command took %s off the roster anyway\n%v", offboardMemberAddress, grants)
	}
}

// TestOffboardDropsMemberFromRoster: EVERY grant of the address goes, whatever
// scope it carries — an address can hold a role at project scope and another
// over repositories, and leaving one behind is leaving the member placed — and
// the grants of everyone else are kept, in the node's own order. The command
// says what it did and what it did not: the invoker grants are the next
// `fugaro update`'s to remove (F20).
func TestOffboardDropsMemberFromRoster(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
		rosterGrant(offboardMemberAddress, "viewer", "all-repos"),
		rosterGrant("deputy@example.com", "admin", "project"),
	)
	fakeIntakeCalls(t, func(c secretsIntakeCall) {
		*c.Out.(*intake.DeleteAnswer) = intake.DeleteAnswer{}
	})

	out, _, err := execute(t, "user", "offboard", offboardMemberAddress)
	if err != nil {
		t.Fatalf("user offboard: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 grants removed") {
		t.Errorf("the command does not say how many grants it dropped:\n%s", out)
	}
	if !strings.Contains(out, "`fugaro update`") {
		t.Errorf("the command does not say the invoker grants come off on the next `fugaro update`, so a person reads it as the member having lost their cloud access now:\n%s", out)
	}
	grants := r.roster(t)
	if rosterNames(grants, "user:"+offboardMemberAddress) {
		t.Errorf("%s still holds a grant of the roster:\n%v", offboardMemberAddress, grants)
	}
	for _, want := range []string{"user:" + offboardAdmin, "user:deputy@example.com"} {
		if !rosterNames(grants, want) {
			t.Errorf("the offboarding dropped %s's grant too: only the member named goes\n%v", want, grants)
		}
	}
	if len(grants) != 2 {
		t.Errorf("the roster holds %d grants, want the two the member did not hold:\n%v", len(grants), grants)
	}
	// And in the node's own order: the drop keeps the grants around the removed
	// ones as it found them, so an admin who reads the node after the write sees
	// their own roster with one address gone rather than a reordered one.
	if len(grants) == 2 {
		got := []string{grants[0].User, grants[1].User}
		if want := []string{"user:" + offboardAdmin, "user:deputy@example.com"}; !slices.Equal(got, want) {
			t.Errorf("the roster reads %v, want the surviving grants in the order they were stored (%v)", got, want)
		}
	}
}

// TestOffboardDeletesPrefixAndEndsSlips: the sweep is the whole prefix, and it
// is the only thing offboarding does to the member's access. The slips are not
// ended — nothing is called that could end one, and the tree has no route that
// ends one: the slip routes are the service's own contract, grant and claim and
// nothing else (G10), so an open slip runs to the max-runtime cap the grant
// recorded (R10) and the command says so rather than leaving the admin to
// assume a run was stopped.
func TestOffboardDeletesPrefixAndEndsSlips(t *testing.T) {
	newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	whole := []string{"fugaro_u_4242_github-token", "fugaro_u_4242_npm-token", "fugaro_u_4242_signing-key"}
	seen := fakeIntakeCalls(t, func(c secretsIntakeCall) {
		*c.Out.(*intake.DeleteAnswer) = intake.DeleteAnswer{Deleted: whole}
	})

	out, _, err := execute(t, "user", "offboard", offboardMemberAddress)
	if err != nil {
		t.Fatalf("user offboard: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want the sweep alone: offboarding reaches no slip store and no route that ends one", len(*seen))
	}
	for _, id := range whole {
		if !strings.Contains(out, id) {
			t.Errorf("the command does not name %q, one of the containers the sweep removed:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "max-runtime cap") {
		t.Errorf("the command does not say the member's open slips end by their max-runtime cap:\n%s", out)
	}
	// A slip is granted and claimed, and there is no end route for this command
	// to call: the one call asserted above is what pins that from here, and
	// credsvc's TestServedRoutesAreExactly holds the service's own side of it
	// (credsvc.SlipPath and credsvc.SlipClaimPath are the whole slip surface).
}

// TestPoolLosesRemovedMembers: offboarding does not rewrite the published
// policy, and it does not have to. A pool's owner and allowed users are policy
// text, and the service resolves them at issue time for a launcher it admits
// (G8, D1936) — so a member the roster no longer places cannot ask for a slip
// at all, the pool's text naming them is inert, and the pool loses them without
// anybody publishing again. What the test pins is that chain's first link, which
// is this command's: the roster no longer places the member, so the launcher
// list the installation renders out of it (its project-scope operators and
// admins) no longer names them.
func TestPoolLosesRemovedMembers(t *testing.T) {
	r := newOffboardRig(t, offboardAdmin,
		rosterGrant(offboardAdmin, "admin", "project"),
		rosterGrant(offboardMemberAddress, "operator", "project"),
	)
	fakeIntakeCalls(t, func(c secretsIntakeCall) {
		*c.Out.(*intake.DeleteAnswer) = intake.DeleteAnswer{}
	})

	out, _, err := execute(t, "user", "offboard", offboardMemberAddress)
	if err != nil {
		t.Fatalf("user offboard: %v\n%s", err, out)
	}
	member := "user:" + offboardMemberAddress
	if grants := r.roster(t); rosterNames(grants, member) {
		t.Fatalf("the roster still places %s, so the launcher list it is rendered from still names them:\n%v", offboardMemberAddress, grants)
	}
	grants := r.roster(t)
	if role := accesslib.EffectiveAtProject(accesslib.Input{Grants: grants}, member); role != accesslib.RoleNone {
		t.Errorf("%s holds %q at project scope after offboarding, want no role at all: a project-scope grant is what the installation's launcher list is rendered from, and the service resolves a pool for a launcher it admits", offboardMemberAddress, role)
	}
	// The policy is untouched: the pool still lists them, and that text is what
	// serves nobody now that the roster does not admit them. Offboarding
	// publishing a policy would be a second act on a document an admin owns,
	// and this command has no publish.
	pool := config.Pool{Name: "shared-models", Owner: offboardAdmin, AllowedUsers: []string{offboardMemberAddress}}
	if !pool.Serves(offboardMemberAddress) {
		t.Errorf("the fixture's pool no longer serves %s as TEXT, which is the one thing offboarding must not rewrite", offboardMemberAddress)
	}
}
