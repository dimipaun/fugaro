package credsvc

import (
	"context"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// This file is C2: what a scope that serves PEOPLE adds to C1's Scope
// (scope.go). C1's scope is the per-call seam that answers every route out of
// one database; a workspace-mode deployment whose members may route a run's
// model traffic to the workspace also serves the workspace's OWN grant and
// claim (R3, D2970) and the personal candidate a workspace slip names (R7).
// That half is this interface's, and it is a SECOND interface a scope may also
// implement rather than four more methods on Scope, for two reasons.
//
// The first is authorship. scope.go is workspace-service's contract file (C1)
// and this is this plan's: one file holding two plans' contracts is one file
// two reconciling sweeps edit, and the plan's §10 says as much — C2 does not
// edit scope.go, a second interface leaves it alone. The second is that the
// halves really are optional, and the shape is what makes that a property
// rather than a flag: an INSTALLATION's scope implements Scope and not this,
// and WC5's "an installation's path is unchanged" then holds because there is
// nothing to branch on. A route of the personal half asks for PersonScope and
// refuses where the scope does not implement it, exactly as Scope.Serves
// refuses a route a deployment does not serve.
//
// **Two package-level variables are how the sides meet, and neither the
// configuration nor the constructor could be a parameter of C1's.** A person
// scope is built from a roster's reader, a launcher verifier and a slip store
// (PersonScopeConfig), and building those is the CLI's (`fugaro credentials
// serve` holds the workspace's database client, its roster node's name and its
// launcher list — WC14). They cannot reach the scope through C1's
// WorkspaceScopeConfig, because that type belongs to scope.go and is not this
// file's to extend; so the configuration travels as one package-level value,
// PersonScopeConfigForServe, which the CLI sets before the resolver is built
// and which the workspace's scope reads when it builds its reader (WC4). The
// reader itself travels the same way for C1's own reason: WC4 (the scope that
// calls the reader) and WC5 (the reader) are written in parallel, so the
// constructor has to answer before its body exists, and NewPersonalReader is a
// variable of the constructor's signature that WC5's file assigns and
// scope_person_stub.go answers until WC5 lands.
//
// **What is deliberately not here** (the plan's §3: a contract holds types,
// constants, a short document and stubs, and nothing that computes): no policy,
// no switch, no choice of tier or name, no container read, no freshness rule
// and no reading of what the roster MEANS. The rules this shape carries, and
// the sentences a side is held to, are the document that is this file's other
// half: docs/workspace-credentials/contract-scope-person.md.

// PersonScope is what a scope that serves people adds to Scope: the four
// questions a route of the personal half asks and the installation's scope has
// no answer for.
//
// It is implemented by the workspace's scope and by no other (WC4), and a scope
// that does not implement it serves no grant and no personal candidate: every
// route of that half refuses rather than reaching a reader that is not there.
type PersonScope interface {
	// Launchers is the launcher class of the WORKSPACE's own grant (R3,
	// D2970): the verifier a person's Google ID token is admitted by when the
	// workspace's service is asked for a slip over the workspace's own
	// credentials, and the roster it is admitted against is the workspace's.
	//
	// **It is the workspace's class and never the member's project's**, which
	// is the whole of D2970: a member's admins can forge their own project's
	// launcher list (D1943), so a slip of the workspace's that they could mint
	// would be a slip over the workspace's money signed by the party spending
	// it. What the class admits is R3's: a verified person whose address is a
	// `human` of the roster, re-read as the node is.
	Launchers() LauncherVerifier
	// Slips is the slip store the call's slip lives in: the WORKSPACE's own
	// database (R3, R8 of workspace-service), and never a member's. A slip read
	// through it is a slip this service granted, which is R4's rule read from
	// the service's side.
	//
	// It is the store and not a reader of it because the workspace's own half
	// writes as well as reads: the grant creates a slip and the claim claims
	// one (creds.SlipStore's own four methods), and both are the workspace's
	// service acting for itself.
	Slips() creds.SlipStore
	// OwnsSlip reports whether a presented slip id is this service's own, which
	// is R4's fence on the service's side: a call to the workspace's service
	// carries a slip only when the WORKSPACE's service granted it, and never a
	// member's (F13).
	//
	// **It is asked of the id and before anything is acted on.** The id is what
	// a call presents, and the question is whether that id is one of this
	// service's own slips at all: false is a slip this service never granted —
	// a member's project's, another workspace's, or this workspace's own and
	// since ended — and the call is refused as presenting a slip that is not
	// this service's. found-of-its-own-store is the mechanism the real side may
	// answer it by (WC4) and is not what this contract fixes; what it fixes is
	// that the question is asked, and answered, before a slip is read for its
	// binding, its claim or its allowance. A read that failed is an error, and
	// the call refuses on it: an id whose ownership could not be established is
	// not one this service may serve under.
	OwnsSlip(ctx context.Context, id string) (bool, error)
	// Personal is the reader that answers the personal candidate of a call's
	// slip (R7). It is the whole of how the workspace's scope reaches the
	// reader: the scope asks for it here and the offering puts what it answers
	// ahead of the shared entries the call is admitted to.
	Personal() PersonalReader
}

// LauncherVerifier is the launcher class as the personal half consumes it: the
// verification of a caller's Google ID token against a list and a key set the
// verifier holds, with no run identity, no job account and no reader of an
// installation's state.
//
// **It is a narrow interface declared here, and not the service's Verifier
// used whole**, and the
// reason is the one the intake's own Caller states: the workspace's class is
// built from a roster and Google's ID-token keys and holds none of the rest
// (launchers.go's LauncherConfig), so a construction that demanded the
// service's whole configuration would make the workspace hold a reader it must
// not have or pass a stand-in for one — and a service built on a stand-in
// admits what it cannot check. *Launchers is what WC14 builds for it, and
// *Authenticator satisfies it too, which is what makes the class one class and
// not two spellings of a token check.
type LauncherVerifier interface {
	// VerifyLauncher answers the verified account and the account id the token
	// names, or an error naming no caller text — never a partially verified
	// caller. The account is the roster's own key (F11: the roster is keyed by
	// address) and the id is the token's `sub`, which is what a slip records as
	// its owner.
	VerifyLauncher(ctx context.Context, tok string) (LauncherCaller, error)
}

// RosterReader is the workspace's own roster as a person scope reads it: the
// node as internal/workspace's store holds it, the absence of one, and a read
// that failed.
//
// **It is the workspace's roster node and not a list of people**, because the
// two rules that read it are about the node: R3 re-reads it as the published
// node is re-read (60 seconds, and a roster that cannot be re-read refuses the
// call rather than serving a stale one), and what a re-read must be judged
// against is the document with its revision. What the document MEANS —
// workspace.DecodeRoster, which people are `human`, which address is whose — is
// decoded by the reader's caller and is not this contract's to fix.
//
// A read that fails is an error and never "no roster": a roster nobody could
// read is not a roster that lists nobody, and the difference decides whether a
// person's run is refused for being removed or waits for a service that could
// not answer (R3, D2983).
type RosterReader interface {
	// Read answers the roster node and whether it is there. found=false is a
	// workspace whose roster has never been written — the state the first write
	// creates, not an empty roster and not an error.
	Read(ctx context.Context) (workspace.RosterNode, bool, error)
}

// PersonalReader answers the personal candidate a call's slip names: the token
// of the person the slip belongs to, as one more subscription candidate the
// offering may walk (R7). It is C1's `PersonSwitchReader` and the container
// read that decide what it answers, and neither is here.
type PersonalReader interface {
	// Personal answers the candidate for one call under sl, and the answer is
	// one of exactly three classes:
	//
	//	(c, true, nil)               the call has a personal candidate. The
	//	                             offering puts it FIRST, ahead of the shared
	//	                             entries the call is admitted to (R7).
	//	(creds.Credential{}, false, nil)
	//	                             the call has no personal candidate at all:
	//	                             the slip names no personal credential, so
	//	                             there is nothing of the person's to serve
	//	                             and the call is offered what it was
	//	                             admitted to, exactly as before this half
	//	                             existed (R8).
	//	(creds.Credential{}, false, err)
	//	                             the answer could not be made: the member's
	//	                             switch is off (R11), the member's policy or
	//	                             the roster could not be read, or the token
	//	                             the slip names is not there (D2974). The
	//	                             call REFUSES.
	//
	// **found=false is never "the person holds no token".** A slip exists only
	// where a grant found one (R8: with none there is no slip and the run is
	// served the shared entries), so an absence discovered at call time — an
	// empty container, one with no version — is the refusal class above and
	// never the absence class: a personal name that fell through to a bundle
	// entry of the same name would serve the shared, paid credential without
	// asking its allow-list and count it as unbilled, which is the whole of
	// what D2974 refuses. A read that failed is the same class for R7's reason:
	// a service that answered the older value because Secret Manager was
	// unreachable would serve, silently, the very token the person meant to
	// retire.
	//
	// The slip is the whole of the call this reader is handed, and it carries
	// both facts the answer needs: whose the token is (its Owner, the verified
	// `sub` of the launcher that asked) and which credential is named (its
	// Allowed list, one of whose names is the personal one this half serves).
	// **The interface is this contract's best reading of what a candidate needs
	// (U9), and the plan anticipates it changing** (R-person-scope, raised by
	// WC4 or by WC5 through it): a reader that needed something of the call the
	// slip does not carry is a contract change — this method's signature, and
	// both sides in the same sweep — and never a second reader reached around
	// the scope.
	Personal(ctx context.Context, sl creds.Slip) (creds.Credential, bool, error)
}

// PersonalReaderFunc is the signature of the personal reader's constructor: the
// whole of what C2 produces for WC4, which builds its scope with it, and for
// WC5, which supplies the body.
type PersonalReaderFunc func(cfg PersonScopeConfig) (PersonalReader, error)

// NewPersonalReader builds the personal reader a person scope serves calls
// with.
//
// **It is a variable and not a function, for C1's own reason read once more**:
// WC4 builds the workspace's scope through this constructor while WC5 — which
// owns the implementation, in personal_workspace.go — is still being written,
// so the signature has to exist before its body does, and Go has no way to
// declare a function in one file of a package and define it in another. The two
// sides assign it from init: scope_person_stub.go assigns a reader that offers
// nothing, and WC5's file assigns the real one.
//
// **The file-name order runs the other way here, and it is the stub that must
// yield.** The toolchain presents a package's files in name order, so C1's stub
// (scope_stub.go) is initialized before its side (scope_workspace.go) and can
// assign unconditionally; WC5's file is personal_workspace.go, which comes
// BEFORE scope_person_stub.go, so an unconditional assignment by the stub would
// be the one that stands and WC5's reader would never be reached. The stub
// therefore assigns only a nil constructor, and WC5's assignment is what answers
// from the moment WC5 lands. I1's deletion of the stub is a deletion and not an
// edit, and nothing outside the package may assign this.
//
// A caller does not have to consider any of that: it calls the constructor and
// what it gets is a reader or a refusal, never a nil.
var NewPersonalReader PersonalReaderFunc

// PersonScopeConfig is the whole configuration a person scope is built from:
// the three things the workspace's own half needs and no fourth.
//
// **Its zero value is the shape of a process that serves no grant**, and that
// is deliberate: an installation's, a workspace-mode process whose own half is
// not wired, and every test that builds a scope it does not exercise are all
// this value, and each of them must serve nothing rather than fail to start —
// which is `fugaro credentials serve` without a roster, as today (WC14).
type PersonScopeConfig struct {
	// Roster is the workspace's own roster node (RosterReader): who the
	// workspace's people are, and which of them is a `human` who may be
	// admitted as a launcher (R3).
	Roster RosterReader
	// Launchers is the launcher class of the workspace's own grant (R3,
	// D2970), built from the roster's people and Google's ID-token keys.
	Launchers LauncherVerifier
	// Slips is the workspace's own slip store (R3): the database the grant
	// writes a slip into and the claim reads one out of.
	Slips creds.SlipStore
}

// PersonScopeConfigForServe is the configuration THIS process's person scope is
// built from: `fugaro credentials serve` sets it in workspace mode (WC14),
// before the scope's resolver is built, and the workspace's scope reads it when
// it builds its reader (WC4). A process that never sets it — an installation,
// and a workspace-mode process whose own half is not wired — leaves it at its
// zero value, which is a person scope that serves no grant and no personal
// candidate, exactly as the service behaves today.
//
// **It is a variable because C1's configuration cannot carry it** (the file's
// own comment): the two sides have to meet somewhere that is neither C1's
// WorkspaceScopeConfig nor a second edit of scope.go, and this is that place.
// It is written once, by the process's own serve path, and read once, by the
// resolver that path builds — never per call, and never by a route.
var PersonScopeConfigForServe PersonScopeConfig

// The split held by the compiler rather than by the comments above: the
// implementations the two sides already build satisfy this contract as they
// stand, so a side that changed one of these signatures fails here rather than
// at WC4 or WC14.
var (
	_ LauncherVerifier = (*Launchers)(nil)
	_ LauncherVerifier = (*Authenticator)(nil)
	_ RosterReader     = (*workspace.RosterStore)(nil)
)
