package credsvc

// WC5: the personal candidate (docs/workspace-credentials, R7, D2974, D3144).
//
// A person's own Claude token is stored once at the workspace and served to the
// runs that person launched there. This file is the service's half of that: the
// reader C2 declares (PersonalReader, scope_person.go), which answers the
// candidate a call's workspace slip names, and the switch R9 turns the half on
// with.
//
// What it decides, in one place, because these four are one rule:
//
//   - WHOSE token. The name is built from the slip's OWNER and from nothing the
//     call says or the slip lists (D2962): a call under a slip that names
//     somebody else's token is answered no candidate, never that person's
//     token. The slip's Allowed list is the fence around that name and never the
//     source of it — it says whether the person's own token may be served, not
//     whose it is.
//   - WHETHER. The member's published policy carries R9's switch, and a call
//     under a slip whose member has it off is REFUSED (R11, and Check 850's
//     "the switch off ... refuse as written"): a slip exists only because the
//     switch was on when it was granted, so a call that meets it off is a run
//     the member has turned the half off under, and the shared entries are not
//     served in its place. A policy that cannot be read refuses for the same
//     reason and is never read as off (C1).
//   - WHAT TIER. Tier A, the shared subscription token's: a person's own token
//     is one more subscription candidate, so it reserves nothing of the
//     workspace's money (D2965) and a paid call that follows it does (D3144).
//   - WHERE IN THE LIST, AND THAT IT IS SERVED FROM THERE. First, ahead of the
//     shared entries the call is admitted to (R7) — cascade.go prepends what
//     this reader answers, after every narrowing, so the admission and the
//     repository's recorded auth have nothing to say about it: what authorises
//     the person's own token is the slip its owner was granted, and D2974 is
//     the rule that it never falls to a bundle entry in its place
//     (dynamicsecrets.go). And "first" is not the half of it that serves: the
//     offered order breaks an exact tie within a tier and says where a rotation
//     starts, so a token offered as one more tier-A member beside the bundle's
//     entries would reach a call about once in N — the counter's turn, or a
//     pool's rotation. cascade.go therefore hands the name to the selection as
//     the call's own exclusive candidate, which is served while it can serve,
//     under whatever rule the list would otherwise be served by
//     (creds.Selector.Exclusive, D3144). It is stepped over the moment it is
//     exhausted, backed off or capped, and the shared entries are served then:
//     D2974's one move, and the whole of when it applies.
//
// What is deliberately NOT here: the roster, the launcher class and the slip
// store (WC4's, and the three things PersonScopeConfig carries — none of them is
// a fact about ONE call's candidate), the grant and the claim (WC4's routes),
// and the container read itself (DS3.3's, which dynamicsecrets.go serves and
// which this file only names the credential for).

import (
	"context"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// PersonSwitchForServe is the switch reader this process reads a member's
// personal half with (R9): C1's PersonSwitchReader, whose real side is WC6's
// (`projectpolicy.PersonSwitchReader` over the member's published policy) and
// which `fugaro credentials serve` assigns in workspace mode beside
// PersonScopeConfigForServe, from PersonSwitchOverScope below.
//
// **The reader is built HERE and installed by the CLI, and the split is the
// contract's.** Where it is built is fixed by the CALL: the switch is asked of
// the MEMBER whose policy the call is under — its project, never the process's,
// a deployment serving many members — and the member's policy is read from the
// member the caller's own verified project names, because this process reads no
// member's database before a caller has been verified (R8 of
// workspace-service). The scope that carries that member is unexported and
// travels in the call's context (scopeOf), so a reader built in another package
// could not read it: the reader is this package's, over the call's OWN policy
// reader (`Scope.Policy`), and what `fugaro credentials serve` does with it is
// install it.
//
// **C2 does not name this variable, and the departure is WC5's and is said
// here rather than left to be found.** PersonScopeConfig is "the three things
// the workspace's own half needs and no fourth", and the switch is not one of
// them; C2 fixes the switch's name and its home (C1's, WC6's) and the answer
// class, and names PersonScopeConfigForServe as the one variable the serve path
// sets. The alternative — a fourth field, or a second edit of scope.go — is the
// contract change C2's own document refuses ("a reader that needed something of
// the call the slip does not carry is a contract change ... and never a second
// reader reached around the scope"), and this variable is not that: the reader
// is still the scope's, and what travels here is the switch alone. What C2 is
// silent on is who builds it, which is I1's line above.
//
// Its default is personSwitchUnwired, which answers OFF for every project, and
// that is the state of a process whose personal half is not wired: an
// installation, a workspace-mode process that never reached the CLI's
// assignment, and every hermetic test. It is the tighter answer rather than an
// error for C1's own reason — absent is off (R9) — and it is what keeps an
// installation's path exactly as it was (A3): no candidate, no container read,
// no refusal. A process whose half IS wired and whose assignment were lost
// would refuse every call under a slip naming a personal token with
// personalSwitchOff rather than serve the shared entries in the token's place,
// which is R11's answer kept even when the wiring is wrong (C1's own default
// reads the same way).
var PersonSwitchForServe creds.PersonSwitchReader = personSwitchUnwired{}

// personSwitchUnwired is the switch of a process that installed none: off for
// every project, and an error for none. It is deliberately not a reader that
// could turn anything on, and deliberately the TIGHTER answer rather than an
// error: a refusal would mean a policy that could not be read (C1), which is
// nothing a process with no reader at all has found.
type personSwitchUnwired struct{}

var _ creds.PersonSwitchReader = personSwitchUnwired{}

// PersonSwitchOn answers off for every project: see the type's own comment.
func (personSwitchUnwired) PersonSwitchOn(context.Context, string) (bool, error) {
	return false, nil
}

// PersonSwitchOverScope is the switch of a workspace-mode process: WC6's
// `projectpolicy.PersonSwitchReader`, whose read is the CALL's own scope.
//
// **The member is the call's and the project argument is therefore ignored**,
// which is `PersonSwitchOverDB`'s own shape on the member's machine read the
// other way: there the database is the member's and the project names nothing,
// here the scope is the member's — the front door resolved it from the identity
// it verified (scopeOf) — and the project a caller's identity names has nothing
// left to add. The policy it reads is the member's published node through the
// scope's own reader (`Scope.Policy`), so a member's node is read by the same
// reader every other route of the call reads it with.
//
// A call that reached this reader with no scope is a direct call of a test and
// not a call of this service, and it is answered "no policy" — absent is off
// (R9). Through the real path it cannot happen: Personal reads the scope first
// and answers no candidate when there is none.
func PersonSwitchOverScope() creds.PersonSwitchReader {
	return projectpolicy.PersonSwitchReader{
		Read: func(ctx context.Context, _ string) (*projectpolicy.Policy, bool, error) {
			sc, ok := scopeOf(ctx)
			if !ok {
				return nil, false, nil
			}
			return sc.Policy().Shared(ctx)
		},
	}
}

// The two sentences the personal half refuses with. They are classes and never
// another package's text, and neither carries a value, a container or a
// person's token name — a refusal names the field or the document, never its
// content (the credentials service's rule).
const (
	// personalSwitchOff is what a call under a slip is told when its member has
	// the personal half off: the switch was on when the slip was granted and is
	// not now, and the shared entries are not served in the token's place (R11).
	personalSwitchOff = "this call's member has the personal workspace token switch off, and the call carries a slip naming a personal token: the credentials service serves one only while the member's published policy turns the switch on, and a run the member turned it off under is refused rather than moved to the shared entries (R9, R11)"
	// personalSwitchUnreadable is the same refusal for the member's policy
	// having failed to be read. It is a sentence of its own and not the
	// switch-off one, because the two are different incidents for an operator
	// and the same answer for the run: a policy that could not be read is never
	// a switch that is off (C1).
	personalSwitchUnreadable = "this call's member's published policy could not be read, so the personal token the call's slip names cannot be served: a policy that cannot be read is never a switch that is off (R9)"
)

// init assigns the constructor C2 declares, and it is the only assignment of
// it: the stub that answered the constructor while this file was being written
// is deleted, and nothing outside the package assigns it.
func init() { NewPersonalReader = newPersonalReader }

// newPersonalReader is the constructor C2 declares, and its answer is the reader
// below for every configuration.
//
// **It refuses no configuration, and it holds none of the three things it is
// handed.** What a candidate needs is the call's slip and the member's switch,
// and neither is a property of the process: the roster and the launcher class are
// the GRANT's (who may ask for a slip) and the slip store is the CLAIM's and the
// scope's own, so a reader that read any of them would be reading the workspace's
// half to answer about one call. C2's own rule is that the zero configuration
// "must serve nothing rather than fail to start", and a reader that refused it
// would be a workspace whose personal half is not wired failing to start at all.
func newPersonalReader(PersonScopeConfig) (PersonalReader, error) { return personalReader{}, nil }

// personalReader is C2's PersonalReader: it reads no configuration, no database
// and no container, and its whole answer is the name of the credential the
// call's slip entitles the person to and the switch its member has set.
type personalReader struct{}

var _ PersonalReader = personalReader{}

// Personal answers R7's candidate for one call: the person's own token when the
// call's slip names it and the member's switch is on.
//
// The slip is read for exactly the two facts C2 says it carries — whose token it
// is (Owner) and whether it may be served (Allowed) — and the member is read
// from the call's own scope, which is where the front door put the identity it
// verified (scopeOf): the project a run's identity names is the member's own,
// and it is the project the switch is asked about. A call with no scope is a
// direct call of a test rather than a call of this service, and it is answered
// "no personal candidate" — which is the state every call was in before this
// half existed (R8).
func (personalReader) Personal(ctx context.Context, sl creds.Slip) (creds.Credential, bool, error) {
	name, ok := personalTokenOf(sl)
	if !ok {
		return creds.Credential{}, false, nil
	}
	sc, ok := scopeOf(ctx)
	if !ok {
		return creds.Credential{}, false, nil
	}
	on, err := PersonSwitchForServe.PersonSwitchOn(ctx, sc.Caller().Project)
	if err != nil {
		return creds.Credential{}, false, &poolRefusal{message: personalSwitchUnreadable}
	}
	if !on {
		return creds.Credential{}, false, &poolRefusal{message: personalSwitchOff}
	}
	// The name is the owner's and the tier is the subscription's; what makes
	// "ahead of the shared entries" mean SERVED is stated where the selection is
	// made (cascade.go hands the name to creds.Selector.Exclusive), because it
	// is a fact about this call's entitlement and not about the token: it is
	// one more member of the list in any call whose slip does not name it.
	return creds.Credential{Name: name, Tier: creds.TierA}, true, nil
}

// personalTokenOf is the personal name a slip entitles its OWNER to, and whether
// it names one at all.
//
// The name is built from the slip's owner — the verified `sub` the grant
// recorded (F11) — and the membership test is the fence: a slip whose Allowed
// list does not carry the owner's own name has no personal candidate, whatever
// else it lists, so another person's token name in a slip's list is a name this
// reader never answers (D2962). A slip that names nobody's personal token is
// R8's ordinary case and not an error: the run is served the shared entries
// exactly as it was before this half existed.
//
// An owner the grammar refuses — an empty or non-numeric `sub`, which no
// verified launcher has — is answered the same way rather than refused: the name
// of a container this service could not read is not a token the person holds,
// and the state it would be refused over is one the grant never wrote.
func personalTokenOf(sl creds.Slip) (string, bool) {
	name, err := secretname.User(sl.Owner, creds.PersonTokenName)
	if err != nil {
		return "", false
	}
	for _, allowed := range sl.Allowed {
		if allowed == name {
			return name, true
		}
	}
	return "", false
}
