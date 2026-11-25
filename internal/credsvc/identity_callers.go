package credsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identity_callers.go is the ticket route's first question: WHO is asking
// (LM3, R2, R4). The ticket is asked for before the run exists, so the route is
// answered ahead of the shared parse and the caller's own Google ID token is
// the whole of what it presents — and this is the check that reads it and
// answers which of the route's three classes the caller is in.
//
// The three classes, and what each of them is:
//
//   - the PERSON class: the roster's `user:` entries, whose token the service
//     verifies exactly as it verifies a launcher's (VerifyLauncher). A launcher
//     asks for a ticket for the run it is about to start, which is the same
//     person the grant route already admits.
//   - the ROSTER SERVICE ACCOUNT class: the roster's `serviceAccount:` entries
//     (D3123), verified by the entry point this task adds beside the
//     launcher's (VerifyServiceAccountLauncher). A pipeline launches runs as
//     one today, through the CLI's own gate (F4), and D3123 answers it with a
//     class of its own rather than a widening of the person class: its token
//     is obtained on purpose, and what it is judged for at the issuer is not
//     what a person's ask is (D3137).
//   - the ORCHESTRATOR class: the resident daemon's own account
//     (VerifyOrchestrator), which re-issues an attempt of its own record
//     (D3125 as amended by D3136).
//
// A `group:` entry is NOT a class and nothing here can admit one: a group holds
// no token to present, so no caller can be a group, and `update` names such
// entries before the grant moves (D3123, D3126). An address on no roster, and a
// run's own job account — whose class is the CLAIM route's, ClassJobAccount —
// are refused with ErrCallerNotAdmitted, which is the route's own code
// (identitywire.RefusalCallerNotAdmitted).
//
// What the check does NOT do, deliberately: it judges no launch scope. A caller
// this file admitted is handed to the scope check (IdentityConfig.Scope, LM23)
// with the class it was admitted as, and the classes that are judged by
// something else — the roster's service accounts (D3137, admitted unscoped) and
// the orchestrator (LM24, judged by its own record) — are the scope check's
// business and not this file's. And it writes no audit line: nothing is
// verified until this returns, and audit.go's rule is that a line is written
// once a caller IS verified (every refusal of the route's handler after this
// point writes exactly one).

// ErrCallerNotAdmitted is the ticket route's refusal of a caller it does not
// admit: an address on no roster, a `group:` entry (which holds no token to
// present, so no token can be one), or a run's own job account, whose class is
// the claim route's. The route answers it with
// identitywire.RefusalCallerNotAdmitted.
//
// The three class refusals collapse into this one on purpose. A caller that is
// on none of the three learns nothing from being told WHICH list it is missing
// from — it cannot change the roster — and a code a caller may act on
// differently is a code that would have to be a decision (the contract's own
// reading of the refusal codes).
var ErrCallerNotAdmitted = errors.New("credsvc: the caller is not one this installation's roster admits")

// CallerClasses is the ticket route's three classes as the service's own
// authenticator answers them: the roster's people and its service accounts
// (auth.go's VerifyLauncher and VerifyServiceAccountLauncher), and the resident
// orchestrator (VerifyOrchestrator).
//
// It is declared here rather than read off the Verifier interface, and that is
// a fact about this release and not a preference: the Verifier interface is
// server.go's, the contract's own file (C1), and the service-account class is
// the one entry point it does not carry yet. A value of this type is what the
// authenticator already is — it answers all three — so nothing has to be
// adapted to bind it; the interface says which three of its entry points this
// check is made of, and nothing else.
type CallerClasses interface {
	// VerifyLauncher admits the roster's people (D1938).
	VerifyLauncher(ctx context.Context, tok string) (LauncherCaller, error)
	// VerifyServiceAccountLauncher admits the roster's service accounts
	// (LM3, D3123).
	VerifyServiceAccountLauncher(ctx context.Context, tok string) (LauncherCaller, error)
	// VerifyOrchestrator admits the resident daemon's own account (E16).
	VerifyOrchestrator(ctx context.Context, tok string) (string, error)
}

// IdentityCallers verifies the callers the ticket route admits, and nothing
// else: it holds the three classes and no store, no policy and no credential.
type IdentityCallers struct {
	classes CallerClasses
}

// NewIdentityCallers validates the configuration and returns the check. It
// refuses a configuration it cannot check a caller with — no classes at all —
// because a check built on nothing would refuse every caller with a reason of
// its own rather than the class's.
func NewIdentityCallers(classes CallerClasses) (*IdentityCallers, error) {
	if classes == nil {
		return nil, errors.New("credsvc: no caller classes: no ticket route caller could be verified")
	}
	return &IdentityCallers{classes: classes}, nil
}

// TicketCaller answers the caller of the ticket route: the address its own
// Google ID token carried, in the configuration's own spelling, and the class
// it was admitted as (C1's IdentityCaller). A caller of no class the route
// admits is refused with ErrCallerNotAdmitted, and a class that could not
// answer at all — a key set that could not be read — is the service's own
// failure and is returned as it stands, never read as a refusal (the rule the
// issuance store's errors are told apart by: a caller must not be told it is
// unadmitted because the service is unwell).
//
// The three classes are asked in turn, and the order is not a preference: the
// lists are disjoint — a person's address cannot be a service account's, and
// NewAuthenticator refuses a roster service account that is also the
// orchestrator or a run's own account — so exactly one of them can admit a
// caller, and the order only decides how many entry points are tried before the
// refusal. Each class's own refusal (ErrUnverifiedJob, the code every class of
// this service writes for a caller that is not one of its own) means "not this
// class" and nothing more; anything else is the service's and stops the check.
func (c *IdentityCallers) TicketCaller(ctx context.Context, tok string) (IdentityCaller, error) {
	for _, arm := range []struct {
		class identitywire.CallerClass
		ask   func() (string, error)
	}{
		{identitywire.ClassPerson, func() (string, error) {
			caller, err := c.classes.VerifyLauncher(ctx, tok)
			return caller.Account, err
		}},
		{identitywire.ClassServiceAccount, func() (string, error) {
			caller, err := c.classes.VerifyServiceAccountLauncher(ctx, tok)
			return caller.Account, err
		}},
		{identitywire.ClassOrchestrator, func() (string, error) {
			return c.classes.VerifyOrchestrator(ctx, tok)
		}},
	} {
		address, err := arm.ask()
		switch {
		case err == nil:
			return IdentityCaller{Address: address, Class: arm.class}, nil
		case !errors.Is(err, ErrUnverifiedJob):
			return IdentityCaller{}, err
		}
	}
	// No address is named here, and that is the package's no-echo rule: what a
	// caller presented is its own claim set, and the fix — the roster it is not
	// on, and the update that moves one — is the installation's.
	return IdentityCaller{}, fmt.Errorf("%w: a ticket is asked for by a person on this installation's roster, a service account on it, or the resident orchestrator; a roster change reaches this service at the next fugaro update", ErrCallerNotAdmitted)
}
