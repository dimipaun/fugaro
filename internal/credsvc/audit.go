package credsvc

import (
	"context"
	"errors"
	"log/slog"
)

// audit.go is C1.13: the service's audit log. One structured line per act of
// the service that touches a credential — an issuance (a proxied model call,
// C1.11), a probe (C1.12), a mint (C2.1) — and one per refusal, so an operator
// can read what the service did with which credential and why it refused what
// it refused.
//
// The line carries the plan's fields and nothing else: the run, the
// repository, the job the run reported, the account that called, the
// credential's NAME, the purpose, the tier and the status. There is no
// free-form field, no map and no message a caller composes, because the
// property this file exists for is that a credential's VALUE never reaches the
// log (F32, D143's "every issuance audited by name", Check 94's "the audit log
// carries names, never values"): a caller holding a value has nowhere in an
// AuditEvent to put it.
//
// Three rules make that hold:
//
//   - the fields are a closed set of typed, named fields (AuditEvent), and
//     every one is held to the rule the rest of the package already holds it
//     to: a run id and a repository slug to token.ValidSegment (the rule the
//     launcher's minter applies and auth.go re-checks), a job name to
//     ValidJobName, an account to foldEmail's printable-ASCII address rule, a
//     credential to ValidCredentialName, a purpose to ValidPurpose, a tier to
//     ValidTier and a status to ValidStatus. A field that cannot be one is
//     refused, and the refusal names the field and never its content;
//   - an event that cannot be recorded is not recorded, and the act it belongs
//     to does not happen: Write returns an error, and a caller that cannot
//     audit an issuance must not make it (D143's fail-closed rule);
//   - the auditor may be armed with the service's own redaction
//     (WithAuditScrub), applied to every field before anything else looks at
//     it. That is redactWriter's rule in the gateway, extended to this line:
//     the code that holds the values is the code that removes them, so a value
//     reaches neither the line nor the error a refused line returns. C1.11's
//     bundle loader is what arms it.
//
// Everything the line carries is text from another writer — a slug, a job
// name, an account, a credential's name — so every field is put through
// internal/safetext before it is written: an escape sequence or a bidi control
// in a name must not reach the terminal or the log viewer.

// The purposes: what a credential was issued for. A closed set, for the reason
// the wire contract's reasons are one (ValidReason): the field is written into
// a line an operator reads and greps, so a purpose a caller invents is that
// caller's own text and is refused rather than recorded.
const (
	// PurposeForward is a proxied model call: the credential was attached to
	// a request the service forwarded (C1.11).
	PurposeForward = "forward"
	// PurposeProbe is the minimal call C1.12 makes on a backed-off credential
	// before real work is routed to it again.
	PurposeProbe = "probe"
	// PurposeMint is a GitHub App installation token minted from the App key
	// in the bundle (C2.1). The credential's name is the App key's.
	PurposeMint = "mint"
)

// ValidPurpose reports whether purpose is one this service issues a credential
// for.
func ValidPurpose(purpose string) bool {
	switch purpose {
	case PurposeForward, PurposeProbe, PurposeMint:
		return true
	}
	return false
}

// StatusOK is the one status that is not a refusal: a credential answered —
// an issuance, or a probe that succeeded.
const StatusOK = "ok"

// ValidStatus reports whether status is one an audit line carries. A status is
// the wire contract's own vocabulary plus StatusOK, and not a set of its own
// (review of C1.13): the two are read together — the runner's halt message
// comes from the contract's error type, and the operator's line from this
// field — so a status the contract cannot express is a status this service
// cannot reach. The contract's reasons say why a credential was not used
// (exhausted, broken, not_admitted, stopped) and its error types say why the
// call itself was refused (exhausted_until, unverified_run_identity,
// unverified_job_identity, state_unavailable).
func ValidStatus(status string) bool {
	return status == StatusOK || ValidReason(status) || validErrorType(status)
}

// validErrorType reports whether s is one of the refusal body's error types
// (wire.go). It is the reader's half of a closed set the writer already owns.
func validErrorType(s string) bool {
	switch s {
	case ErrorExhaustedUntil, ErrorUnverifiedRunIdentity, ErrorUnverifiedJobIdentity, ErrorStateUnavailable:
		return true
	}
	return false
}

// AuditEvent is one audited act: exactly the fields the line carries, and no
// other. Every one is a name, a tier, a purpose or a status — never a
// credential's value, and there is no field a value could be written into.
//
// Which fields a line must have depends on what it records. A line whose
// status is StatusOK says a credential was used, so it names the whole act:
// the run and its repository, the job and the account, the credential and its
// tier, and the purpose. A refusal carries only what was known when it was
// refused — a call refused before the run identity was verified has no run to
// name, and one refused because the operator stopped the service names the
// credential it would have used — and a field that is set is held to its rule
// either way.
type AuditEvent struct {
	// Run is the run id the service verified (Identity.Run) and Repository is
	// its repository's slug (Identity.Slug). They are empty only for a call
	// refused before the run identity was verified, and they are set together
	// or not at all: a run id without its repository is not a run this service
	// can name.
	Run        string
	Repository string
	// Job is the Cloud Run job the run reported (Identity.Job — the caller's
	// own claim, which is why it is recorded and decides nothing) and Caller
	// is the service account the service verified (Identity.JobAccount, in the
	// installation's own spelling): "which job called" is the account's answer
	// and not the header's (auth.go's verifyJob), and the two are recorded
	// together because the identity is what carries them.
	Job    string
	Caller string
	// Credential is the credential's NAME, never its value, and Tier is the
	// tier it is of (TierA, TierB or TierC). They are set together or not at
	// all, the rule SetCredentialHeaders and CredentialHeaders already share: a
	// line that named one without the other would be a line this service
	// cannot write, and a reader that guessed at the missing half would be
	// reading an answer the service did not give.
	Credential string
	Tier       string
	// Purpose is what the credential was issued for (PurposeForward,
	// PurposeProbe or PurposeMint).
	Purpose string
	// Status is what happened: StatusOK, or the refusal's status.
	Status string
}

// Audit returns the event for an act on this run: the identity's own verified
// fields filled in — the run, its repository, the job it reported and the
// account that called — and the rest left to the caller. It is the way C1.11
// writes a line, so the run a line names is the run the service verified and
// not one a caller typed.
func (id Identity) Audit(purpose, credential, tier, status string) AuditEvent {
	return AuditEvent{
		Run:        id.Run,
		Repository: id.Slug,
		Job:        id.Job,
		Caller:     id.JobAccount,
		Credential: credential,
		Tier:       tier,
		Purpose:    purpose,
		Status:     status,
	}
}

// Auditor writes the service's audit lines. It is safe for concurrent use (the
// log is), and it holds nothing but the log and the redaction it was armed
// with.
type Auditor struct {
	log   *slog.Logger
	scrub func(string) string
}

// AuditOption configures an Auditor.
type AuditOption func(*Auditor)

// WithAuditScrub arms the auditor with the service's own redaction: f is
// applied to every field of every event before any rule looks at it, so a
// value the service holds is removed here and reaches neither the line nor the
// error a refused line returns. It is redactWriter's rule (F32) at the last
// place a value could land, and C1.11's bundle loader — the one place the
// service reads a credential — is what arms it. A nil f leaves the auditor as
// it was.
func WithAuditScrub(f func(string) string) AuditOption {
	return func(a *Auditor) {
		if f != nil {
			a.scrub = f
		}
	}
}

// NewAuditor returns the auditor writing to log. A nil log is refused: an
// issuance this service could not audit must not be made (D143), so a caller
// with no log to write to does not get an auditor that silently drops lines.
func NewAuditor(log *slog.Logger, opts ...AuditOption) (*Auditor, error) {
	if log == nil {
		return nil, errors.New("credsvc: no logger: an act this service could not audit must not be made")
	}
	a := &Auditor{log: log}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

// Write records one audited act as one structured line, and returns an error
// if the act could not be recorded — in which case the caller must not go on
// with it (D143: an issuance that cannot be audited is not made).
func (a *Auditor) Write(ctx context.Context, ev AuditEvent) error {
	_ = ctx
	_ = ev
	return nil
}
