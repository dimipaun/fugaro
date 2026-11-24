package access

import (
	"errors"
	"slices"
)

// The seat rule (spec section 10 of docs/licence-key-v2, as the portal's D2186
// decided it): a seat is an operator or an admin — anyone who can launch a job
// — and viewers are free. Who holds one is assigned nowhere: the grants node's
// array is the assignment, because it is already the order the grants were
// written in, and no field is added to it (internal/access/store.go decodes the
// node strictly, so a new field would make every installed reader refuse it).
// Every admin holds a seat whatever the count says, and the operators fill what
// is left of the key's own count in the node's order, the later ones acting as
// viewers until a seat frees up or more are bought.
//
// This file is the core half of that, and the shape follows lapse.go's: it
// holds no licence code — the shape a Subscription surface asks its question
// through (SeatLimit), the seam the licence fills (SetSeatLimit) and the rule
// that places the holders (Seats, Seated) — while how many seats there are is
// the key's answer and lives in ee/ (installed from ee/register), the only
// caller of the setter (design ee-boundary-moves.md §5.5).
//
// The MIT core is untouched by all of it, and that is a property of the code
// and not a promise: the rule is applied on the Subscription surfaces alone
// (lapse.go's SubscriptionAllowed), and the one gate every core command asks —
// Allowed — never consults it. A build that links no ee/ therefore enforces
// nothing here whatever a key says (D2132).

// SeatLimit answers the highest role caller may hold on a Subscription surface
// while the licence applies: the role they hold, where the key's seat count
// reaches them, and the viewer role where it does not.
//
// in is the caller's own input — the stored grants in the node's order, which
// is the order the seat rule's "in the order they were granted" reads — and it
// is given rather than read for the reason the resolver's Input is: the answer
// is a pure function of it, and a role check may not pay for a bucket or a
// database read (ee/register's warn.go declines the same read for the same
// reason). Which key is installed is the licence's answer and never this
// package's: the function is installed by ee/register, and the core reads no
// key.
//
// It only ever lowers a role. An answer above what the caller holds is the role
// they hold — a seat is not a grant, so an unseated viewer is never raised to
// the operator the key's count would have room for — and an answer outside the
// closed set caps to RoleNone rather than to the caller's role (the fail-closed
// rule every other answer in this package obeys).
type SeatLimit func(in Input, caller string) Role

// seatLimit is the installed limit. The core's own caps nobody, because an
// untagged build has no licence and no key to count seats from (D2143: no key
// is Free, and the MIT core is unaffected either way) — and it is deliberately
// not nil: seatCapped calls it on every Subscription surface check, so the
// default has to answer.
var seatLimit SeatLimit = noSeatLimit

// seatLimitSet records that a caller installed a limit, which only
// SetSeatLimit does.
var seatLimitSet bool

// SetSeatLimit installs the seat limit the Subscription surfaces ask through
// seatCapped, refusing a second call: what a seat is worth is decided once, by
// ee/register, and a limit that could be replaced after it would be a way to
// hand the seats of a smaller or a lapsed key back in a binary that carries the
// enforcement. A nil limit is refused for the same reason the core default is
// not nil — the reader calls it, and a nil one is a panic rather than a
// decision.
func SetSeatLimit(l SeatLimit) error {
	if seatLimitSet {
		return errors.New("access: the seat limit is already set")
	}
	if l == nil {
		return errors.New("access: the seat limit needs a function")
	}
	seatLimit, seatLimitSet = l, true
	return nil
}

// noSeatLimit is the untagged build's limit: it answers admin, the strongest
// role in the closed set, so a Subscription surface is capped by nothing and a
// build that links no ee/ answers on these paths exactly as it did before there
// was a seam.
func noSeatLimit(Input, string) Role { return RoleAdmin }

// seatCapped is held with the installed seat limit applied: what caller may
// hold on a Subscription surface. The limit only ever lowers a role, and an
// answer this package cannot read as a role — RoleNone included — caps to
// RoleNone, so a limit that answers something it did not mean unseats the
// caller rather than seating them.
func seatCapped(in Input, caller string, held Role) Role {
	if held == RoleNone {
		return RoleNone
	}
	capped := seatLimit(in, caller)
	if !capped.Valid() {
		return RoleNone
	}
	if capped.Rank() < held.Rank() {
		return capped
	}
	return held
}

// Seat is one member the seat rule places: who, the role that place is for, and
// whether the key's count reaches them.
//
// Role is admin for an implicit project admin and for a member whose highest
// stored role is admin, and operator otherwise: a member whose grants name them
// only as a viewer is not in the list at all, because a viewer is free and a
// place in this list is a seat. A member whose place the count does NOT reach
// is in the list with Seated false — that is the state the surfaces act on (an
// operator without a seat acts as a viewer) and it is what shows a person who a
// seat increase is about, so leaving those out would hide exactly them.
type Seat struct {
	Member string
	Role   Role
	Seated bool
}

// seatPlacement is one member's place in the node's order while Seats builds
// the roster: the role they are placed for and the index of the grant that gave
// them it.
type seatPlacement struct {
	member   string
	role     Role
	position int
}

// Seats is the D2186 rule: the members who hold a seat, admins first and the
// operators after them in the order the grants node names them.
//
// Admins hold a seat however small the count is, which is the spec's "admins
// always keep a seat, so someone can always fix licensing": an installation
// whose admins outnumber the key's seats holds every admin and unseats every
// operator, rather than keeping a count that would leave nobody able to install
// a bigger key. The operators then take what is left of the count in the node's
// order — the order they were granted — and a count this rule cannot honour
// (zero, or fewer than the admins) seats no operator at all.
//
// The order within each group is the array's, and a member's place in it is the
// grant that gave them the role they are placed for: a member the node names
// first as an operator and later as an admin is an admin (the highest role wins,
// as it does everywhere in this package) and stands where the admin grant does.
// The implicit project admins come first of all: they hold admin without a
// stored grant (F17.3) and the node cannot place them.
func Seats(in Input, seats int) []Seat {
	at := make(map[string]int, len(in.Grants))
	placed := make([]seatPlacement, 0, len(in.Grants))
	for i, grant := range in.Grants {
		if grant.User == "" || !grant.Role.Valid() || !grant.Scope.Valid() || grant.Role == RoleViewer {
			// A viewer is free (spec section 10), and a grant this package did
			// not parse grants nothing — so neither places a member here.
			continue
		}
		index, seen := at[grant.User]
		switch {
		case !seen:
			at[grant.User] = len(placed)
			placed = append(placed, seatPlacement{member: grant.User, role: grant.Role, position: i})
		case grant.Role.Rank() > placed[index].role.Rank():
			placed[index].role, placed[index].position = grant.Role, i
		}
	}
	admins, operators := make([]seatPlacement, 0, len(placed)), make([]seatPlacement, 0, len(placed))
	for _, p := range placed {
		if p.role == RoleAdmin {
			admins = append(admins, p)
			continue
		}
		operators = append(operators, p)
	}
	byPosition := func(a, b seatPlacement) int { return a.position - b.position }
	slices.SortStableFunc(admins, byPosition)
	slices.SortStableFunc(operators, byPosition)

	roster := make([]Seat, 0, len(in.Implicit)+len(admins)+len(operators))
	taken := make(map[string]bool, cap(roster))
	place := func(member string, role Role, seated bool) bool {
		if member == "" || taken[member] {
			return false
		}
		taken[member] = true
		roster = append(roster, Seat{Member: member, Role: role, Seated: seated})
		return true
	}
	for _, member := range in.Implicit {
		place(member, RoleAdmin, true)
	}
	for _, p := range admins {
		place(p.member, RoleAdmin, true)
	}
	// What is left of the count after every admin has one, which is what the
	// operators fill in the node's order — and only the operators this places
	// spend it. A member the node also grants operator to but who is already
	// placed as an admin (an implicit project admin, or one the node names
	// admin) holds the admin's seat, and their operator grant must not take a
	// second one from the operators after them.
	room := seats - len(roster)
	for _, p := range operators {
		if place(p.member, RoleOperator, room > 0) {
			room--
		}
	}
	return roster
}

// Seated reports whether caller holds a seat of the seats the key's count
// reaches: every admin does, and an operator does while the count has room for
// them (Seats).
//
// A caller the roster does not name holds none, which is the answer for a
// viewer and for nobody at all: an empty caller is not a member any grant could
// name (the fail-closed rule), and capping a viewer to the viewer role they
// already hold changes nothing either way.
func Seated(in Input, caller string, seats int) bool {
	if caller == "" {
		return false
	}
	for _, seat := range Seats(in, seats) {
		if seat.Member == caller {
			return seat.Seated
		}
	}
	return false
}
