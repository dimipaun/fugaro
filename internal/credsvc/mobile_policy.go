package credsvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The mobile policy (C1's Policy, F35): the one place in the mobile flow where
// the roster and the member record meet, and the one implementation of it — a
// pairing (P3), a refresh (P4) and a restore (K2) ask the same question of the
// same code, because three handlers answering it three ways is a way into a
// project that only one of them guards.
//
// The six checks, in the order they are made, and each one is the answer to a
// different question:
//
//   - is mobile on for this project at all (D2796, O9)? A policy published
//     before the `mobile` key existed reads as off, so an installation that has
//     not run `fugaro update` answers mobile_disabled rather than admitting a
//     phone into a project nobody switched on.
//   - does this project accept the provider the assertion names (D2785)? The
//     list is the published policy's, and the refusal carries it, so the phone
//     can offer a sign-in that will work.
//   - is the address one this project may believe? The email must be verified
//     by the identity provider, and an Apple private-relay address is refused:
//     it is an address Apple made up to hide the person's own, so it is not the
//     address the roster grants and not one an admin invited.
//   - is the sign-in recent enough (O4)? The project's own age, 24 hours by
//     default, measured against the assertion's auth_time.
//   - is the address a member of the ROSTER, and with which role (O14)? The
//     roster is the grants node plus the machine's implicit admins, read
//     exactly as the launch gate reads them (LaunchScopeConfig.accessInput), so
//     a phone is admitted by the same list that admits a run — and the role is
//     the roster's, never the invitation's copy of it.
//   - does the identity match what this project accepted first for the address
//     (D2791 as amended, F34)? The member record's provider and sub are the
//     address's identity; a record that holds none is the invitation's own, and
//     the first pairing fills it in.
//
// Nothing here reads the invitation: the nonce is the pairing's own and the
// role the invitation carries is a copy for its audit line, which is why a
// pairing that finds a different roster role writes the roster's (C1's Invite).
type MobilePolicyConfig struct {
	// Policy is the published policy (P6), read for `mobile.*`: whether the
	// surface is on, the accepted providers and the maximum authentication age.
	Policy PolicyReader
	// Records is the installation's roster (internal/access), read exactly as
	// the launch gate reads it. nil is an installation whose grants cannot be
	// read at all, which is read as a roster of nobody — and NOT as an error,
	// because a deployment that configures no records is the state of an
	// installation whose database address is not wired (RTDBAccess.DB's own
	// reading of a nil reader).
	Records AccessRecords
	// Members is the member records' store (D2791 as amended). It is required:
	// the check this policy exists for is the recorded identity, and a policy
	// with no store to read would admit every address that is a member.
	Members mobilesession.MemberStore
	// ImplicitAdmins are the machine's implicit admins as members
	// ("user:<address>"), the launch gate's own list: the CLI reads them from
	// its configuration (localcfg's terraform.budget_admins) and `fugaro
	// update` renders them into this service's environment, so an owner is a
	// member of their own project without a grant row saying so.
	ImplicitAdmins []string
	// Now is the clock the authentication age is measured against, nil for
	// time.Now. A test hands in its own so that an age can be stated exactly.
	Now func() time.Time
}

// NewMobilePolicy is the policy the mobile routes apply (I1 wires it into
// MobilePairDeps, P4's session and K2's restore).
func NewMobilePolicy(cfg MobilePolicyConfig) mobilesession.Policy {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return mobilePolicy{cfg: cfg}
}

type mobilePolicy struct{ cfg MobilePolicyConfig }

// Allow applies the six checks above to a verified identity, in that order, and
// answers the roster's role when it admits.
//
// The order is the order of what an operator reads: a project with the surface
// off refuses every phone before it looks at any of them, a provider the project
// does not accept is refused before the roster is read (it is the refusal the
// phone has a screen for), and the roster is read once, after the assertion's
// own claims have been held to what this project accepts.
//
// A failure to READ — the policy node, the roster, the member record — is
// returned as an error and never as a verdict, which is the whole difference
// between a caller who is not admitted and a service that could not tell
// (R16's rule): the route answers it as its own state, and a phone told
// not_a_member for a database that timed out would send its owner to ask an
// admin for a grant they already hold.
func (p mobilePolicy) Allow(ctx context.Context, project string, claims mobilesession.Claims) (mobilesession.Verdict, error) {
	pol, found, err := p.cfg.Policy.Shared(ctx)
	if err != nil {
		return mobilesession.Verdict{}, fmt.Errorf("credsvc: the published policy could not be read for a mobile call: %w", err)
	}
	// A policy node that is not there at all, and one whose mobile block is not
	// there, are one answer: this project has not turned the surface on. The
	// block's absence is the switch (projectpolicy.MobileEnabled), so a node
	// published before the key existed refuses a phone rather than admitting it
	// to a default nobody chose (O9).
	if !found || !pol.MobileEnabled() {
		return mobilesession.Verdict{Code: mobilesession.CodeMobileDisabled}, nil
	}
	accepted := pol.MobileAcceptedProviders()
	if !slices.Contains(accepted, claims.Provider) {
		return mobilesession.Verdict{Code: mobilesession.CodeProviderNotAccepted, Accepted: accepted}, nil
	}
	if !mobileEmailTrusted(claims) {
		return mobilesession.Verdict{Code: mobilesession.CodeNotAMember}, nil
	}
	if p.cfg.Now().Sub(time.Unix(claims.AuthTime, 0)) > pol.MobileMaxAuthAge() {
		return mobilesession.Verdict{Code: mobilesession.CodeAuthTooOld}, nil
	}
	role, err := p.role(ctx, claims.Email)
	if err != nil {
		return mobilesession.Verdict{}, err
	}
	if role == accesslib.RoleNone {
		return mobilesession.Verdict{Code: mobilesession.CodeNotAMember}, nil
	}
	if err := p.recorded(ctx, project, claims); err != nil {
		// The recorded-identity mismatch is the one refusal this step makes, and
		// it is the caller's: everything else it can answer is a node nobody
		// could read, which is the service's own state.
		if errors.Is(err, errMobileReassigned) {
			return mobilesession.Verdict{Code: mobilesession.CodeNotAMember}, nil
		}
		return mobilesession.Verdict{}, err
	}
	return mobilesession.Verdict{Allowed: true, Role: role}, nil
}

// mobileEmailTrusted reports whether this project may believe the address the
// assertion carries. Two things make it one an admin never granted:
//
//   - the identity provider did not verify it. An unverified address is a value
//     the person typed, so it is not evidence of anything — the roster names
//     addresses its providers verified, and admitting a typed one would let
//     whoever controls that provider hand out any address in the project.
//   - it is Apple's private relay (projectpolicy.ProviderApple's own note): the
//     address is one Apple made up to hide the person's, it belongs to that
//     person alone, and the roster has never heard of it. It is refused by its
//     domain, which is Apple's and is one domain — so the check is exact and
//     cannot be widened by a provider that mails from a similar name.
//
// A provider whose claim needs a check this contract does not carry (Microsoft's
// tenant-verified address, A3's provider configuration and Check 731) is not
// checked here: C1's Claims has no tenant, and a rule invented from the address
// alone would refuse an honest tenant account. That check belongs to the
// identity project, which is where the provider is configured.
func mobileEmailTrusted(claims mobilesession.Claims) bool {
	if !claims.EmailVerified {
		return false
	}
	if claims.Provider == projectpolicy.ProviderApple && strings.HasSuffix(claims.Email, mobileAppleRelayDomain) {
		return false
	}
	return true
}

// mobileAppleRelayDomain is the domain Apple's private relay addresses live
// under (projectpolicy.ProviderApple's "a private relay address is not the
// person's"). It is spelled here and not in the policy package because it is
// this reader's check: the policy's vocabulary is the four provider names, and a
// domain in it would be a second kind of entry in a list that holds one.
const mobileAppleRelayDomain = "@privaterelay.appleid.com"

// CrashReports is the project's published crash-report switch: on only when the
// published policy states it on, and off for a policy node that is not there or
// does not carry the key (D734, G7). A node that could not be read answers off
// with the error, and the route fills the session with that off rather than
// refusing it (D738).
func (p mobilePolicy) CrashReports(ctx context.Context, _ string) (bool, error) {
	pol, found, err := p.cfg.Policy.Shared(ctx)
	if err != nil {
		return false, fmt.Errorf("credsvc: the published policy could not be read for the crash-report switch: %w", err)
	}
	if !found {
		return false, nil
	}
	return pol.MobileCrashReportsOn(), nil
}

// role resolves the roster's role for an address at project scope, which is the
// role a device node carries (a node records a role and no scope, so a grant at
// one repository is not one a phone can hold).
//
// The input is the launch gate's (LaunchScopeConfig.accessInput): the grants
// node as internal/access reads it, plus the machine's implicit admins, so the
// list that admits a phone is the list that admits a run of the same project.
// The policy's allow_repos is deliberately not read: it decides whether a
// REPOSITORY is admissible at the moment of a check, and this question is about
// a person and no repository (access.Self's own note).
//
// The migrated marker is not read either, and that is the one place this input
// differs from the gate's: the marker decides whether the policy's repo list
// applies at all (accessInput's third arm), and this policy reads no repo list —
// so the two states it separates are one state here, and a second database read
// per pairing would buy nothing.
func (p mobilePolicy) role(ctx context.Context, email string) (accesslib.Role, error) {
	member, err := accesslib.Member(email)
	if err != nil {
		// An address no member can be built from is one the roster can never
		// name, which is not_a_member and not a failure: it is the answer for a
		// provider that mints addresses this installation's grants cannot hold.
		return accesslib.RoleNone, nil
	}
	in := accesslib.Input{Implicit: p.cfg.ImplicitAdmins}
	if p.cfg.Records != nil {
		node, err := p.cfg.Records.Read(ctx)
		if err != nil {
			return accesslib.RoleNone, fmt.Errorf("credsvc: the roster could not be read for a mobile call: %w", err)
		}
		if node.Found {
			in.Grants = node.Grants
		}
	}
	return accesslib.EffectiveAtProject(in, member), nil
}

// recorded refuses an identity that is not the one this project accepted first
// for the address (D2791 as amended, F34), and it is the check the record
// exists for: an address that was reassigned to another person — the same
// address, a different sub — is one the roster still names and this record
// still refuses, so a phone cannot be handed the projects of whoever held the
// address before.
//
// A record that holds no identity is the invitation's own and admits the
// pairing: that is the first use D2785 records. A record that holds an identity
// must match BOTH halves — a provider that changed while the sub stayed the
// same is a different identity under the same address, and matching on either
// half alone would admit one of the two.
func (p mobilePolicy) recorded(ctx context.Context, project string, claims mobilesession.Claims) error {
	rec, found, err := p.cfg.Members.Member(ctx, project, claims.Email)
	if err != nil {
		return fmt.Errorf("credsvc: the member record could not be read for a mobile call: %w", err)
	}
	if !found || (rec.Provider == "" && rec.Sub == "") {
		return nil
	}
	if rec.Provider != claims.Provider || rec.Sub != claims.Subject {
		return errMobileReassigned
	}
	return nil
}

// errMobileReassigned is the recorded-identity mismatch, and it travels as a
// sentinel so that the route can answer it as the refusal it is (not_a_member)
// while the policy's other errors are the service's own state. It names no
// value: the identity it refused is a subject id, and the person holding the
// phone is the one person who must not be told which identity the address used
// to be.
var errMobileReassigned = fmt.Errorf("credsvc: the identity does not match the one this project accepted first for this address")
