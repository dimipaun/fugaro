package access

// The seat rule (spec section 10 of docs/licence-key-v2, as the portal's D2186
// decided it): a seat is an operator or an admin, the grants node's array is
// the assignment, admins hold a seat whatever the count says, and the operators
// fill what is left in the order they were granted. The seam it is asked
// through is SetSeatLimit, and the limit an untagged build ships — the core's
// own — caps nobody, which is what the last test here holds.

import (
	"testing"
)

const dave = "user:dave@example.com"

// seatRoster is one input the rule is asked about: a project-scoped grant per
// member, in the order a node would hold them, plus the implicit admins.
func seatRoster(t *testing.T, implicit []string, grants ...Grant) Input {
	t.Helper()
	return Input{Grants: grants, Implicit: implicit}
}

// seatsOf renders a roster as one line per holder, in the rule's own order, so
// a test reads as the list a person would see.
func seatsOf(in Input, seats int) []string {
	out := make([]string, 0, len(in.Grants))
	for _, s := range Seats(in, seats) {
		state := "seated"
		if !s.Seated {
			state = "over"
		}
		out = append(out, s.Member+" "+string(s.Role)+" "+state)
	}
	return out
}

// TestSeatRuleGivesEveryAdminSeatBeforeAnyOperator is the rule D2186 recommended
// and the owner took: the admins come first and always hold a seat, the
// operators follow in the node's order, and the later ones are over the count
// — an operator without a seat is where the paid surfaces cap them.
//
// The count is the key's, so it is asked with several: what the rule places is
// the same list every time and only the reach changes, which is what makes a
// bigger key a bigger reach rather than a different roster.
func TestSeatRuleGivesEveryAdminSeatBeforeAnyOperator(t *testing.T) {
	project := scope(t, "project")
	cases := []struct {
		name     string
		implicit []string
		grants   []Grant
		seats    int
		want     []string
	}{
		{
			name:   "operators fill the seats in the order the node names them",
			grants: []Grant{grant(alice, RoleOperator, project), grant(bob, RoleOperator, project), grant(carol, RoleOperator, project)},
			seats:  2,
			want:   []string{alice + " operator seated", bob + " operator seated", carol + " operator over"},
		},
		{
			name:   "a bigger key reaches the ones after them",
			grants: []Grant{grant(alice, RoleOperator, project), grant(bob, RoleOperator, project), grant(carol, RoleOperator, project)},
			seats:  3,
			want:   []string{alice + " operator seated", bob + " operator seated", carol + " operator seated"},
		},
		{
			name:   "an admin stands where its admin grant does, and the grant order is what places it",
			grants: []Grant{grant(alice, RoleOperator, project), grant(bob, RoleAdmin, project), grant(carol, RoleOperator, project), grant(alice, RoleAdmin, project)},
			seats:  2,
			want:   []string{bob + " admin seated", alice + " admin seated", carol + " operator over"},
		},
		{
			name:   "an admin is seated past the count, and the operators of a count it has eaten are over",
			grants: []Grant{grant(alice, RoleOperator, project), grant(bob, RoleAdmin, project), grant(carol, RoleAdmin, project)},
			seats:  1,
			want:   []string{bob + " admin seated", carol + " admin seated", alice + " operator over"},
		},
		{
			name:     "the implicit admins come first and count with the rest",
			implicit: []string{dave},
			grants:   []Grant{grant(bob, RoleAdmin, project), grant(alice, RoleOperator, project)},
			seats:    2,
			want:     []string{dave + " admin seated", bob + " admin seated", alice + " operator over"},
		},
		{
			name:   "a key that bought no seat still seats every admin",
			grants: []Grant{grant(bob, RoleAdmin, project), grant(alice, RoleOperator, project)},
			seats:  0,
			want:   []string{bob + " admin seated", alice + " operator over"},
		},
		{
			name:     "an implicit admin's own operator grant spends no seat",
			implicit: []string{dave},
			grants:   []Grant{grant(dave, RoleOperator, project), grant(bob, RoleOperator, project), grant(carol, RoleOperator, project)},
			seats:    2,
			want:     []string{dave + " admin seated", bob + " operator seated", carol + " operator over"},
		},
		{
			name:   "a viewer is free and is not in the list at all",
			grants: []Grant{grant(alice, RoleViewer, project), grant(bob, RoleOperator, project)},
			seats:  1,
			want:   []string{bob + " operator seated"},
		},
		{
			name:   "a grant this package did not parse places nobody",
			grants: []Grant{grant(alice, Role("owner"), project), {User: "", Role: RoleOperator, Scope: project}, grant(bob, RoleOperator, Scope{})},
			seats:  3,
			want:   nil,
		},
		{
			name:   "a member the node names twice holds one place",
			grants: []Grant{grant(alice, RoleOperator, project), grant(alice, RoleOperator, scope(t, "all-repos"))},
			seats:  1,
			want:   []string{alice + " operator seated"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := seatsOf(seatRoster(t, c.implicit, c.grants...), c.seats)
			if len(got) != len(c.want) {
				t.Fatalf("Seats(%d seats) = %v, want %v", c.seats, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("Seats(%d seats) = %v, want %v: the admins hold a seat first and the operators follow in the node's order (D2186)", c.seats, got, c.want)
				}
			}
		})
	}
}

// TestSeatedFollowsTheCount: the one question a surface asks of the rule, for
// the callers whose answer is not the roster's own order.
func TestSeatedFollowsTheCount(t *testing.T) {
	project := scope(t, "project")
	// The node names dave first, as an operator, and dave is also an implicit
	// project admin: the operator grant places nobody — dave is already placed
	// as the admin that grant cannot raise — and it must not spend the seat the
	// two operators after it are seated from.
	in := seatRoster(t, []string{dave},
		grant(dave, RoleOperator, project),
		grant(alice, RoleAdmin, project),
		grant(bob, RoleOperator, project),
		grant(carol, RoleOperator, project),
	)
	for _, c := range []struct {
		caller string
		seats  int
		want   bool
	}{
		{alice, 0, true},
		{alice, 2, true},
		{bob, 1, false},
		{bob, 2, false},
		{bob, 3, true},
		{carol, 3, false},
		{carol, 4, true},
		{carol, 100, true},
		{dave, 0, true},
		{dave, 100, true},
		{"user:someone@example.com", 100, false},
		{"", 100, false},
	} {
		if got := Seated(in, c.caller, c.seats); got != c.want {
			t.Errorf("Seated(%s, %d seats) = %v, want %v: an admin always holds a seat and an operator holds one while the count has room for them (spec section 10, D2186)", c.caller, c.seats, got, c.want)
		}
	}
}

// TestSeatLimitDefaultsToCappingNobody: the seam's own default, which is what
// every build that links no ee/ ships. A Subscription surface answers exactly
// what the core's gate answers, so a caller the rule would unseat is still the
// operator their grant says — the MIT CLI enforces nothing here whatever a key
// outside this build says (D2132), and a limit nobody installed is not a limit
// that refuses.
func TestSeatLimitDefaultsToCappingNobody(t *testing.T) {
	if err := SetSeatLimit(nil); err == nil {
		t.Error("SetSeatLimit(nil) was accepted: the limit is called on every Subscription surface check, so a nil one is a panic rather than a decision")
	}
	project := scope(t, "project")
	in := Input{
		Grants:     []Grant{grant(alice, RoleOperator, project), grant(bob, RoleViewer, project)},
		AllowRepos: []string{"github/acme/app"},
	}
	for _, c := range []struct {
		caller string
		action Action
		repo   string
	}{
		{alice, ActionLaunch, "github/acme/app"},
		{alice, ActionManageProject, ""},
		{bob, ActionViewRepo, "github/acme/app"},
		{bob, ActionCancel, "github/acme/app"},
	} {
		paid := SubscriptionAllowed(in, c.caller, c.action, c.repo)
		if core := Allowed(in, c.caller, c.action, c.repo); paid != core {
			t.Errorf("with no seat limit installed a Subscription surface answers %v for %s doing %s and the core gate answers %v: the default caps nobody, and a build that links no ee/ must answer on the paid paths exactly as it did before the seam existed", paid, c.caller, c.action, core)
		}
	}
	if !SubscriptionAllowed(in, alice, ActionLaunch, "github/acme/app") {
		t.Error("an operator whose grant admits the repository cannot launch with no seat limit installed: nothing this build ships unseats anybody without a key (D2143)")
	}
}
