package credsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// IdentityConfig is the service's identity configuration (C1): what
// `credentials serve` reads out of its environment into it (LM22) and what the
// identity routes are served from (identitywire, R2).
//
// Its zero value is an installation that has not been given an issuer yet, and
// that is a state of its own and not an error: from this release on the routes
// are mounted in every service, and one with no signer answers the contract's
// "not served" (R7, I1) — the answer a CLI of this release bridges on and signs
// a run's identity locally with, as long as the people's grant is still there.
type IdentityConfig struct {
	// Signer is the service account the runs' identities are signed as: the
	// account the installation's Firebase root holds the one minter row for
	// (D3121, LM11). Empty means no issuer is configured, whatever else is set
	// here.
	Signer string
	// ServiceAccounts are the roster's service accounts (D3123): the launcher
	// class a person's list cannot hold, each a bare address and no `group:` or
	// `domain:` entry. `fugaro update` names such entries rather than admitting
	// an address that holds no token to present.
	ServiceAccounts []string
	// Operators are the roster's operators (R2): the owner's own list, and the
	// only class the probe admits.
	Operators []string
	// ImplicitAdmins are the machine's implicit admins, as the CLI's gate reads
	// them from its own configuration (F29, R16): the scope resolver admits
	// them without a roles record, and an issuer that ran the resolver without
	// them would refuse an owner a launch that works today.
	ImplicitAdmins []string
	// MaxRuntime is the longest a run's identity may be asked for. The ticket
	// route refuses an ask beyond it rather than clamping it (R4).
	MaxRuntime time.Duration
	// Store is the issuance record's reader and writer (R3, LM1).
	Store IssuanceStore
	// Scope is the launch-scope check (R16, LM23), with the orchestrator's
	// class judged by LM24 behind this same hook.
	Scope ScopeCheck
	// VMIdentity is the link from a claiming VM to the Batch job the record
	// names (C1, BR3): the one question the claim cannot answer itself, and the
	// interface BR3 implements from the fact Check 830 recorded (R1, U1, D3102).
	//
	// A nil link is an installation whose link is not wired, and that is a state
	// of its own and not an error, exactly as an empty Signer is: the claim is
	// then answered the contract's "not served" (identitywire.RefusalNotServed,
	// R7), because a VM nothing proved is no VM of any job and a claim the
	// service cannot judge must not be served on the account check alone
	// (D3114). Nothing falls back, and no deployment is configured with a
	// permissive link.
	VMIdentity identitywire.VMIdentity
}

// ErrClaimAttestation is the refusal C1's rule is about (D3114, R18): a record
// that names a Batch job is served only to a VM of that job, and a claim on it
// that carries no attestation — or carries something that is no identity token
// at all — is refused before the installation is read at all.
//
// It is one sentinel for both spellings of the same fault, and it never carries
// the attestation: what the claim sent is a credential of the VM and reaches no
// error, no log line and no audit line (R7).
var ErrClaimAttestation = errors.New("credsvc: a claim on a record that names a job must carry the claiming VM's attestation")

// CheckClaimAttestation judges the claim's own shape against the record it is
// made on, and it is asked BEFORE anything of the installation is read: the
// record's job decides whether an attestation is needed at all (D3114), and a
// claim that failed here is the ask's own fault and not a read that failed.
//
// A record that names no job is a Cloud Run run's and passes: its claim is the
// claim of the release behind, and the account check is the whole of it (R18).
// Where a job IS named, the attestation must be there and must be shaped like
// the metadata server's identity token (Attestation.WellFormed), which is a
// spelling check and not a proof: which VM it names and which job that VM
// belongs to are the link's (IdentityConfig.VMIdentity), and nothing here
// decides them.
func CheckClaimAttestation(rec identitywire.Record, attestation identitywire.Attestation) error {
	if !rec.NamesAJob() {
		return nil
	}
	switch {
	case attestation == "":
		return fmt.Errorf("%w: this record names a job and the claim carries no attestation field", ErrClaimAttestation)
	case !attestation.WellFormed():
		return fmt.Errorf("%w: what this claim carries is not an identity token of the metadata server's shape", ErrClaimAttestation)
	}
	return nil
}

// IdentityCaller is a caller the service verified (LM3): the address its own
// Google ID token carried and the class it was admitted as. It is the service's
// own answer and never text from the call — the address is read out of the
// token and folded to the configuration's spelling, exactly as the launcher
// class already does it (VerifyLauncher).
type IdentityCaller struct {
	// Address is the verified address, in the configuration's own spelling.
	Address string
	// Class is the class the address was verified as.
	Class identitywire.CallerClass
}

// IssuanceStore is where the issuance record lives (R3): the one reader and
// writer the identity routes share, one record per slug and run at
// identitywire.PathRecord, under the database's creds subtree.
//
// LM1 produces the real one over the database (internal/creds). Its errors
// matter to the routes and are told apart: identitywire's sentinels are
// decisions about the caller or the run, and anything else — a node that could
// not be read — is the service's own failure and is answered as one (R16's
// ScopeFault rule: a run's gateway reads a 403 as a missing invoke grant and
// halts on it, so a node nobody could read must not be refused as a caller who
// is not admitted).
type IssuanceStore interface {
	// Put writes the record for the slug and run it was given, replacing the
	// one there. The ticket route is the only writer: before a claim it
	// replaces the ticket of the same caller's record, which is what kills the
	// old ticket (R6), and it refuses another caller's record itself.
	Put(ctx context.Context, slug, run string, rec identitywire.Record) error
	// Get reads the record for the slug and run. found=false with no error is
	// the answer "there is no record", which is what the ticket route answers a
	// re-issue for a run nobody asked for, and what the claim answers an
	// unknown ticket (ErrNoRecord, R4).
	Get(ctx context.Context, slug, run string) (identitywire.Record, bool, error)
	// Claim marks the record claimed: the account that claimed, when, and the
	// digest of the nonce it carried (R2, R5). The fence is the store's, and so
	// is the retry rule, because both are one atomic read-and-write on the
	// node: a record already claimed by this account with THIS nonce digest is
	// returned as it stands, which is the retry R2 answers again rather than
	// counting as a second claim, and every other claim on a claimed record is
	// refused with identitywire.ErrClaimed. It returns the record as it stands
	// after the call.
	Claim(ctx context.Context, slug, run string, claim identitywire.Claim) (identitywire.Record, error)
}

// ScopeCheck judges whether a verified caller may be issued a ticket for the
// run its ask names (R16, LM23). The orchestrator's class is judged by LM24
// behind this same hook, which is why the whole ask is handed over: what that
// class may be issued is bound to the orchestrator's own record and to the
// attempt the ask names (D3136), and what a person may launch is a roles
// question the resolver answers.
//
// It is asked once per ask, after the caller's class is verified and before a
// record is written, and a refusal is the caller's and not the service's.
type ScopeCheck interface {
	Check(ctx context.Context, caller IdentityCaller, ask identitywire.TicketRequest) error
}

// ErrStubIdentity is what the identity stub decides, which is nothing: every
// answer of it is this error, so that a route built on it refuses rather than
// serving a caller, a run or a ticket the stub invented. It is the rule every
// stub of this package follows: a stub that answered a question would be a
// decision nothing made.
var ErrStubIdentity = errors.New("credsvc: the identity stub decides nothing")

// stubIdentityStore is the issuance store of a side built before LM1 exists.
// It holds no record, writes none and claims none, and every one of its
// methods refuses: a store that answered "this run has no record" would be a
// store that decided, and a ticket route reading it would issue a ticket for a
// run another caller may already hold.
type stubIdentityStore struct{}

// StubIdentityStore is that store. A test of a side whose real store is not
// written yet builds with it, and LM1 replaces it with the real one; nothing
// in the service falls back to it, and no deployment is configured with it.
func StubIdentityStore() IssuanceStore { return stubIdentityStore{} }

func (stubIdentityStore) Put(context.Context, string, string, identitywire.Record) error {
	return ErrStubIdentity
}

func (stubIdentityStore) Get(context.Context, string, string) (identitywire.Record, bool, error) {
	return identitywire.Record{}, false, ErrStubIdentity
}

func (stubIdentityStore) Claim(context.Context, string, string, identitywire.Claim) (identitywire.Record, error) {
	return identitywire.Record{}, ErrStubIdentity
}

// stubIdentityScope is the scope check of a side built before LM23 (and LM24)
// exists. It admits every caller and every ask, and that is what makes it a
// stub and not a check: the red step of both those tasks is a test that shows a
// caller nothing judged walking through the ticket route against this.
type stubIdentityScope struct{}

// StubIdentityScope is that check. LM23 and LM24 replace it with theirs, and
// nothing in the service falls back to it: a deployment with no scope check
// configured has a configuration error and not an admitting check.
func StubIdentityScope() ScopeCheck { return stubIdentityScope{} }

func (stubIdentityScope) Check(context.Context, IdentityCaller, identitywire.TicketRequest) error {
	return nil
}
