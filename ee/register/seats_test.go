// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The seats (plan K3.4, as the portal's D2186 decided it) at the one point that
// installs the limit, and on the read that shows a person who holds one: an
// operator past the key's count acts as a viewer on the Subscription surfaces,
// an admin never does, the MIT core's own gate is not reached by any of it, and
// a lapse changes nothing stored.
//
// The roster is the grants node and the count is the key's, so every test here
// seeds a node into a fake Realtime Database and installs a key of its own
// count. The read that publishes the facts the limit answers with is
// `fugaro licence seats ls`, which is driven as the command a person runs — and
// one test holds the other side of that: a process that has read no licence
// caps nobody, because the count is a fact a read publishes and not something a
// check fetches (TestTheLimitCapsNobodyUntilThisProcessReadsTheLicence).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	// seatsAdmin is the granted project admin, seatsFirst and seatsSecond the
	// two operators in the order the node lists them, and seatsOther a member
	// with no grant at all — the implicit project admin one test hands the
	// rule, and the stranger another one asks about.
	seatsAdmin  = "user:alice@example.com"
	seatsFirst  = "user:bob@example.com"
	seatsSecond = "user:carol@example.com"
	seatsOther  = "user:dave@example.com"
	seatsRepo   = "github/acme/app"
)

// seatsFixture is the licence fixture with a budget database: the roster the
// seat rule reads is the grants node, so a test that asks who holds a seat
// needs the database the node lives in, and a test that asks whether anything
// wrote reads it back.
type seatsFixture struct {
	*licenceFixture
	db     *gcpfake.RTDB
	grants []access.Grant
}

// newSeatsFixture points the installation's budget backend at a fake database
// and clears the published seat facts on the way in and out: the facts are
// package-level and one test's active key would otherwise be the count a later
// test's surfaces answer with.
func newSeatsFixture(t *testing.T) *seatsFixture {
	t.Helper()
	f := newLicenceFixture(t)
	db := gcpfake.NewRTDB(t)
	// The database says which project it belongs to, the way an installation's
	// own does: a command that opens it refuses one that is another project's
	// (Cloud.BudgetDB).
	db.Set("fugaro/project", licenceTestProject)
	cfg, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	was := "budget: { firebase_project: " + licenceTestFirebase + " }"
	now := "budget: { firebase_project: " + licenceTestFirebase + ", rtdb_url: " + db.URL + " }"
	if !strings.Contains(string(cfg), was) {
		t.Fatalf("the fixture's config does not carry %q, so this test cannot point it at a fake database: %s", was, cfg)
	}
	if err := os.WriteFile(f.config, []byte(strings.Replace(string(cfg), was, now, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	licenceSeatFacts.Store(nil)
	t.Cleanup(func() { licenceSeatFacts.Store(nil) })
	return &seatsFixture{licenceFixture: f, db: db}
}

// seatGrant is one project-scoped grant, which is the scope every roster here
// is written at.
func seatGrant(t *testing.T, role access.Role, member string) access.Grant {
	t.Helper()
	s, err := access.ParseScope("project")
	if err != nil {
		t.Fatalf("ParseScope(project): %v", err)
	}
	return access.Grant{User: member, Role: role, Scope: s}
}

// seed writes the grants node, in the order the seats are handed out, and keeps
// the same roster for the test's own access.Input.
func (f *seatsFixture) seed(t *testing.T, grants ...access.Grant) {
	t.Helper()
	raw, err := access.Encode(grants)
	if err != nil {
		t.Fatalf("encoding the grants node: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node the store encodes does not decode: %v", err)
	}
	f.db.Set(access.PathNode, doc)
	f.grants = grants
}

// node is the grants node as the database holds it, for the tests that ask
// whether anything wrote.
func (f *seatsFixture) node(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(f.db.Value(access.PathNode))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// input is the roster the rule reads, with the implicit admins a test hands it:
// the node's grants in their order, the one repository the published policy
// admits, and no lapse.
func (f *seatsFixture) input(implicit ...string) access.Input {
	return access.Input{Grants: f.grants, Implicit: implicit, AllowRepos: []string{seatsRepo}}
}

// installKey puts a key of the given seat count into the installation's project
// store the way `fugaro licence set` does, with the renewal secret that came
// with it.
func (f *seatsFixture) installKey(t *testing.T, seats int, edit ...func(*licence.Claims)) {
	t.Helper()
	mint := append([]func(*licence.Claims){func(c *licence.Claims) { c.Seats = seats }}, edit...)
	key, secret, _ := f.bundle(t, mint...)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("installing a key of %d seats: %v", seats, err)
	}
}

// publish runs the read that publishes the seat facts every Subscription
// surface check is answered with, and answers both of its streams.
func (f *seatsFixture) publish(t *testing.T) (string, string) {
	t.Helper()
	out, errOut, err := f.exec(t, "", "licence", "seats", "ls")
	if err != nil {
		t.Fatalf("licence seats ls = %v (stdout %q, stderr %q)", err, out, errOut)
	}
	return out, errOut
}

// TestAnUnseatedOperatorActsAsAViewer is the whole of K3.4 on a paid surface: a
// key of two seats, an admin and two operators, and the operator the node names
// second holds no seat — the admin's seat and the first operator's fill the
// count, so a Subscription surface refuses the second the launching a
// subscription is bought for while it still lets them read, which is the viewer
// half of "an operator without a seat acts as a viewer" (spec section 10,
// D2186).
func TestAnUnseatedOperatorActsAsAViewer(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
	)
	f.installKey(t, 2)
	f.publish(t)

	in := f.input()
	if !access.SubscriptionAllowed(in, seatsFirst, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface refuses the operator the node names first a launch, and the key buys two seats: the admin's seat and that operator's fill them (D2186)")
	}
	if access.SubscriptionAllowed(in, seatsSecond, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface lets the second operator launch, and the key buys two seats an admin and the first operator hold: an operator the count does not reach acts as a viewer (spec section 10)")
	}
	if !access.SubscriptionAllowed(in, seatsSecond, access.ActionViewRepo, seatsRepo) {
		t.Error("a Subscription surface refuses an unseated operator a read: acting as a viewer is what lets them read and not launch")
	}
	if access.SubscriptionAllowed(in, seatsSecond, access.ActionCancel, seatsRepo) {
		t.Error("a Subscription surface lets an unseated operator cancel a run: cancelling takes an operator (D1587), and an unseated operator holds the viewer's role")
	}
	if !access.SubscriptionAllowed(in, seatsAdmin, access.ActionManageProject, "") {
		t.Error("a Subscription surface refuses the admin a project-level change, and the key buys one seat the admin holds: admins always keep a seat")
	}
	if access.SubscriptionAllowed(in, seatsOther, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface lets somebody with no grant launch: the seat limit only ever lowers a role and never grants one")
	}
}

// TestAdminsAlwaysKeepASeat: a key that bought no seat at all, and the two
// admins still hold one — the granted admin, and the implicit project admin the
// rule seats first because no grant places it (F17.3, spec section 10's "so
// someone can always install a bigger key").
func TestAdminsAlwaysKeepASeat(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
	)
	f.installKey(t, 0)
	f.publish(t)

	in := f.input(seatsOther)
	if !access.SubscriptionAllowed(in, seatsAdmin, access.ActionManageRepo, seatsRepo) {
		t.Error("a Subscription surface refuses an admin the repository's settings, and the key buys no seat: an admin always keeps a seat, or nobody could install a bigger key")
	}
	if !access.SubscriptionAllowed(in, seatsOther, access.ActionManageProject, "") {
		t.Error("a Subscription surface refuses an implicit project admin a project-level change, and the key buys no seat: the implicit admins hold admin without a stored grant and are seated with the rest (F17.3)")
	}
	if access.SubscriptionAllowed(in, seatsFirst, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface lets an operator launch, and the key buys no seat and holds no room for one: the count is the key's, and nothing is handed out past it")
	}
}

// TestTheLimitCapsNobodyUntilThisProcessReadsTheLicence is where the limit
// begins: a tagged process starts with no licence read, and every Subscription
// surface check answers admin until one happens — the count is a fact a read
// publishes (licenceSeatFacts), not something a check goes and fetches, because
// a role check may not pay for a bucket read. The key is installed and it buys
// two seats; the second operator keeps the launch only until the read that
// publishes the count, and loses it after — so an installation whose surfaces
// never read the licence enforces no seat, which is what every surface did
// before this task and what a build with no ee/ does always.
func TestTheLimitCapsNobodyUntilThisProcessReadsTheLicence(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
	)
	f.installKey(t, 2)
	in := f.input()

	if !access.SubscriptionAllowed(in, seatsSecond, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface already refuses the second operator a launch before this process read the licence: the limit answers with the facts the last read published, and one that read none caps nobody rather than capping everybody")
	}
	f.publish(t)
	if access.SubscriptionAllowed(in, seatsSecond, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface still lets the second operator launch after the read published a two-seat key an admin and the first operator fill: the read is what puts the count in the limit's hands")
	}
}

// TestSeatsAreNotEnforcedInTheMITCLI is the other half of D2132: the limit is
// installed in this build and a key of two seats is published, and the core's
// own gate still answers the unseated operator exactly what it answers the
// seated one — every MIT CLI command asks Allowed, no licence reaches it, and a
// build that links no ee/ has no limit at all.
func TestSeatsAreNotEnforcedInTheMITCLI(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
	)
	f.installKey(t, 2)
	f.publish(t)
	in := f.input()

	if access.Seated(in, seatsSecond, 2) {
		t.Fatal("the second operator holds a seat of a two-seat key an admin and the first operator fill: this test would prove nothing about the MIT CLI")
	}
	if !access.SubscriptionAllowed(in, seatsFirst, access.ActionLaunch, seatsRepo) || access.SubscriptionAllowed(in, seatsSecond, access.ActionLaunch, seatsRepo) {
		t.Fatal("the seat limit does not separate the two operators on a Subscription surface: it is not installed, and this test would prove nothing about the MIT CLI")
	}
	for _, c := range []struct {
		action access.Action
		repo   string
	}{
		{access.ActionLaunch, seatsRepo},
		{access.ActionCancel, seatsRepo},
		{access.ActionManageRepo, seatsRepo},
		{access.ActionManageProject, ""},
	} {
		seated := access.Allowed(in, seatsFirst, c.action, c.repo)
		over := access.Allowed(in, seatsSecond, c.action, c.repo)
		if seated != over {
			t.Errorf("the core's own gate answers %v for a seated operator doing %s and %v for an unseated one: a seat is a limit of the Subscription surfaces alone, and every MIT command asks Allowed (D2132)", seated, c.action, over)
		}
	}
	if !access.Allowed(in, seatsSecond, access.ActionLaunch, seatsRepo) || !access.Allowed(in, seatsSecond, access.ActionCancel, seatsRepo) {
		t.Errorf("the core's own gate refuses an operator the seat count does not reach a launch or a cancel: the MIT CLI enforces nothing without a key")
	}
}

// TestLapseNeverDeletes: a key past its grace, which is the state the surfaces
// read as a lapse — and nothing stored changes. The grants node is the node it
// was, the key is still in the project's object, reading and cancelling survive,
// and the seat count of the key that ended caps nobody: what limits the surfaces
// then is the lapse and not a seat (D436, D2142, D2186).
func TestLapseNeverDeletes(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
	)
	f.installKey(t, 2, func(c *licence.Claims) {
		c.IssuedAt = f.now.Add(-370 * 24 * time.Hour)
		c.PaidThrough = f.now.Add(-10 * 24 * time.Hour)
		c.GraceDays = 0
	})
	before, keys := f.node(t), len(f.object(t).Keys)

	out, errOut := f.publish(t)
	if !strings.Contains(out, string(licence.Expired)) {
		t.Errorf("the report reads %q, and it does not say the key is past its grace", out)
	}
	if !strings.Contains(errOut, "no licence key applies") {
		t.Errorf("the notes read %q, and they do not say that no key applies: a key past its grace caps nothing (D436)", errOut)
	}
	if after := f.node(t); after != before {
		t.Errorf("reading the seats changed the grants node:\n  before %s\n  after  %s\nA lapse changes nothing stored: the seats are the node's order and no command of this task writes one", before, after)
	}
	if after := len(f.object(t).Keys); after != keys {
		t.Errorf("the project's object holds %d keys after the read, want the %d it held: a key that ended is not deleted", after, keys)
	}
	if len(f.grants) != 3 {
		t.Errorf("the roster is %d members, want the three the node holds: the rule reads the node and the count, and neither is deleted by a key that ended", len(f.grants))
	}

	// The limit is in force and the count of a key past its grace caps nobody:
	// an installation whose licence has lapsed is Free in behaviour, and what
	// refuses launching then is the lapse limit beside it (D436, D2142), which
	// the rest of this test reads.
	licensed := f.input()
	if !access.SubscriptionAllowed(licensed, seatsSecond, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface refuses the second operator a launch on an installation whose key is past its grace: a seat count belongs to a key in force, and one that ended caps nobody")
	}

	lapsed := f.input()
	lapsed.Lapse = true
	if !access.SubscriptionAllowed(lapsed, seatsSecond, access.ActionViewRepo, seatsRepo) {
		t.Error("a Subscription surface refuses an unseated operator a read while the subscription lapsed: a lapse keeps reading for the people who held it")
	}
	if !access.SubscriptionAllowed(lapsed, seatsFirst, access.ActionCancel, seatsRepo) {
		t.Error("a Subscription surface refuses a seated operator a cancel while the subscription lapsed: cancelling is what the Terms' read-only use keeps (D1587)")
	}
	if access.SubscriptionAllowed(lapsed, seatsFirst, access.ActionLaunch, seatsRepo) {
		t.Error("a Subscription surface lets a seated operator launch while the subscription lapsed: a lapse refuses launching whoever holds a seat")
	}
}

// TestSeatLimitIsSetOnce is the seam's own rule at the point that fills it: the
// limit is this package's, installed once by init, and a second installation is
// refused — a limit installed after the build's own would be a way to hand the
// seats of a smaller key back (design ee-boundary-moves.md section 5.5).
func TestSeatLimitIsSetOnce(t *testing.T) {
	if err := access.SetSeatLimit(func(access.Input, string) access.Role { return access.RoleAdmin }); err == nil {
		t.Error("a second access.SetSeatLimit was accepted: the tagged binary's limit is the licence's, and a later caller must not be able to replace it")
	}
	if err := access.SetSeatLimit(nil); err == nil {
		t.Error("access.SetSeatLimit(nil) was accepted after the licence's limit was installed: the setter refuses both a second call and a limit the surfaces would panic on")
	}
}

// TestLicenceSeatsLsListsTheHolders: what a person reads, in the rule's own
// order, with the key's count beside them and the ones past it named.
func TestLicenceSeatsLsListsTheHolders(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
		seatGrant(t, access.RoleViewer, seatsOther),
	)
	f.installKey(t, 2)

	out, errOut := f.publish(t)
	for _, want := range []string{"project:", licenceTestProject, "state:", string(licence.Active), "seats:", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report reads %q and does not carry %q", out, want)
		}
	}
	for _, member := range []string{seatsAdmin, seatsFirst, seatsSecond} {
		if !strings.Contains(out, member) {
			t.Errorf("the report reads %q and does not list %s, who holds a role the rule places", out, member)
		}
	}
	if strings.Contains(out, seatsOther) {
		t.Errorf("the report reads %q and lists %s, who is a viewer: viewers are free and hold no seat (spec section 10)", out, seatsOther)
	}
	if !strings.Contains(out, "over the count") {
		t.Errorf("the report reads %q, and the second operator is past a key of two seats held by one admin and one operator", out)
	}
	if !strings.Contains(errOut, "1 of these hold a role the key's 2 seats do not reach") {
		t.Errorf("the notes read %q, and one operator is past a two-seat key", errOut)
	}

	// A bigger key is what reaches the operator after them: the same roster,
	// one more seat, and nothing is over the count.
	f.installKey(t, 3)
	if _, errOut := f.publish(t); !strings.Contains(errOut, "every holder's role is inside the key's 3 seats") {
		t.Errorf("the notes read %q, and a key of three seats reaches one admin and two operators", errOut)
	}
}

// TestLicenceSeatsLsJSONShape is the field a reader outside this process shows,
// and its order: the holders in the rule's own order, so a console listing them
// lists the seats the same way the surfaces hand them out.
func TestLicenceSeatsLsJSONShape(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
		seatGrant(t, access.RoleOperator, seatsSecond),
	)
	f.installKey(t, 2)

	out, _, err := f.exec(t, "", "licence", "seats", "ls", "--json")
	if err != nil {
		t.Fatalf("licence seats ls --json = %v", err)
	}
	var got struct {
		Project string `json:"project"`
		State   string `json:"state"`
		Source  string `json:"source"`
		Seats   int    `json:"seats"`
		Applies bool   `json:"applies"`
		Holders []struct {
			Member string `json:"member"`
			Role   string `json:"role"`
			Seated bool   `json:"seated"`
		} `json:"holders"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("licence seats ls --json wrote %q, which is not JSON: %v", out, err)
	}
	if got.Project != licenceTestProject || got.State != string(licence.Active) || got.Source != "project" {
		t.Errorf("the answer is %+v, want the fixture's active project key", got)
	}
	if got.Seats != 2 || !got.Applies {
		t.Errorf("the answer reads %d seats and applies %v, want 2 and true", got.Seats, got.Applies)
	}
	want := []struct {
		member string
		role   string
		seated bool
	}{
		{seatsAdmin, "admin", true},
		{seatsFirst, "operator", true},
		{seatsSecond, "operator", false},
	}
	if len(got.Holders) != len(want) {
		t.Fatalf("the answer lists %d holders, want %d: %+v", len(got.Holders), len(want), got.Holders)
	}
	for i, w := range want {
		if h := got.Holders[i]; h.Member != w.member || h.Role != w.role || h.Seated != w.seated {
			t.Errorf("holder %d is %+v, want %s %s seated=%v: the admins come first and the operators follow in the node's order (D2186)", i, h, w.member, w.role, w.seated)
		}
	}
}

// TestLicenceSeatsWithoutAKeyAppliesNoCount: an installation on Free has no seat
// count at all, and the read says so rather than showing a roster a count would
// have unseated — nothing caps a role while no key applies (D2143).
func TestLicenceSeatsWithoutAKeyAppliesNoCount(t *testing.T) {
	f := newSeatsFixture(t)
	f.seed(t,
		seatGrant(t, access.RoleAdmin, seatsAdmin),
		seatGrant(t, access.RoleOperator, seatsFirst),
	)

	out, _, err := f.exec(t, "", "licence", "seats", "ls", "--json")
	if err != nil {
		t.Fatalf("licence seats ls --json without a key = %v", err)
	}
	var got struct {
		State   string `json:"state"`
		Applies bool   `json:"applies"`
		Holders []struct {
			Member string `json:"member"`
			Seated bool   `json:"seated"`
		} `json:"holders"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "free" || got.Applies {
		t.Errorf("the answer reads state %q applies %v, want free and false (D2143)", got.State, got.Applies)
	}
	if len(got.Holders) != 2 || !got.Holders[1].Seated {
		t.Errorf("the holders are %+v, want the admin and the operator with both seated: nothing caps a role while no key applies", got.Holders)
	}

	if _, errOut := f.publish(t); !strings.Contains(errOut, "no licence key applies") {
		t.Errorf("the human report's notes read %q, and an installation with no key is told so", errOut)
	}
}

// TestLicenceSeatsHasNoAssignVerb pins the half of the plan D2186 withdrew: the
// seats are the grants node's order and no field is added to it, so there is no
// verb that assigns one — and the refusal names the verb there is without
// repeating what the caller typed, which this position is one a pasted key lands
// in.
func TestLicenceSeatsHasNoAssignVerb(t *testing.T) {
	f := newSeatsFixture(t)
	for _, verb := range []string{"assign", "rm"} {
		out, errOut, err := f.exec(t, "", "licence", "seats", verb, seatsFirst)
		if err == nil {
			t.Fatalf("`fugaro licence seats %s` was accepted: D2186 withdrew the assignment, and the node's order is what decides who holds a seat", verb)
		}
		if !strings.Contains(err.Error(), "unknown licence seats subcommand") || !strings.Contains(err.Error(), "ls") {
			t.Errorf("`licence seats %s` answers %q, which does not name the verb there is", verb, err)
		}
		for _, text := range []string{out, errOut, err.Error()} {
			if strings.Contains(text, seatsFirst) {
				t.Errorf("`licence seats %s` answers %q, which repeats the argument: this position is one a pasted key lands in", verb, text)
			}
		}
	}
}
