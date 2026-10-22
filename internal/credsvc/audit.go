package credsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/safetext"
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
//     audit an issuance must not make it (D143's fail-closed rule). "Cannot be
//     recorded" is both halves — an event whose fields are not ones this
//     service writes, and a log that is not recording, whether because the
//     handler's level excludes the line or because the handler fails to write
//     it (see line: slog's own LogAttrs would drop either one silently and
//     return no error);
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
	// before real work is routed to it again. A probe that fails is recorded
	// with the status of the mark it leaves (ReasonExhausted: the credential
	// stays held, for longer), and a probe that succeeds with StatusOK.
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
// the wire contract's own vocabulary plus StatusOK, and not a set of its own:
// the two are read together — the runner's halt message
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

// Audit completes an event with the identity the service verified: the run,
// its repository, the job the run reported and the account that called are
// taken from the identity and from nowhere else, so a line's "who" is always
// what auth.go verified and never a value a caller typed. It is the way C1.11
// writes a line:
//
//	a.Write(ctx, id.Audit(AuditEvent{Credential: name, Tier: TierA,
//		Purpose: PurposeForward, Status: StatusOK}))
//
// The four fields it sets are set whatever the caller put there, which is the
// point: the fields it does not set are the caller's, and the rules for them
// are checked on the way out either way.
func (id Identity) Audit(ev AuditEvent) AuditEvent {
	ev.Run = id.Run
	ev.Repository = id.Slug
	ev.Job = id.Job
	ev.Caller = id.JobAccount
	return ev
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

// The line's message, and the message an act that could not be recorded leaves
// behind: a reader greps `credsvc: audit` for the service's acts and reads the
// rest of the line as JSON.
const (
	auditMessage        = "credsvc: audit"
	auditRefusedMessage = "credsvc: audit event refused"
)

// The classes a refusal falls into, so a refusal says what kind of thing was
// wrong and not only which field — the class is this file's own text, like the
// field name, and never a value.
const (
	// refusalMissing: the event needs the field and it is empty — the
	// both-or-neither pairs, and the fields an issuance with StatusOK must
	// name.
	refusalMissing = "missing"
	// refusalMalformed: the field is set and is not a value of the shape it
	// holds — a run id that is not a path segment, a job name that is not one,
	// an account that is not an address, a credential name that is not one
	// header value.
	refusalMalformed = "malformed"
	// refusalUnknown: the field is one of a closed set the contract owns and
	// the value is not one of them (a status, a purpose, a tier).
	refusalUnknown = "unknown"
)

// refusal is why an event was not recorded: the field, and the class of thing
// wrong with it. Both are this file's own text, so a refusal can say what was
// wrong and still never repeat what the field held (the no-echo rule).
type refusal struct {
	field  string
	reason string
}

// Error is the refusal in words. It carries the field and the class and
// nothing the event held, which is what makes it safe to hand back to a caller
// that will log it.
func (r refusal) Error() string {
	return fmt.Sprintf("credsvc: the audit event's %s is %s", r.field, r.reason)
}

// Write records one audited act as one structured line, and returns an error
// if the act could not be recorded — in which case the caller must not go on
// with it (D143: an issuance that cannot be audited is not made).
//
// Every field is written, empty ones included: the line's shape is the
// contract's, so a reader does not have to tell "not known" from "not there".
// The order is the plan's, and every field goes through internal/safetext on
// the way out — a slug, a job name, an account and a credential's name are all
// text another writer chose. The line therefore carries the ESCAPED form of a
// name, which is what an operator reads: a run id with a bidi control in it
// (token.ValidSegment allows one — a bidi control is a format character, not a
// control) is written with that character replaced, so the line reads as the
// terminal shows it rather than handing the control on to whatever reads the
// log. The name in its own spelling is the identity's, and it is not this
// line's to carry.
func (a *Auditor) Write(ctx context.Context, ev AuditEvent) error {
	ev, bad := a.checked(ev)
	if bad.field != "" {
		// One line saying the act was refused, naming the field and the class
		// of thing wrong with it and never what it held (the no-echo rule):
		// both are this file's own text. The caller gets the same two in an
		// error.
		//
		// The refusal is the caller's answer whether or not its line lands —
		// an act this service will not record is not made either way — so the
		// two errors are joined rather than one dropped: a handler that cannot
		// write is not silent.
		return errors.Join(bad, a.line(ctx, slog.LevelWarn, auditRefusedMessage,
			slog.String("field", bad.field),
			slog.String("reason", bad.reason)))
	}
	return a.line(ctx, slog.LevelInfo, auditMessage,
		slog.String("run", safetext.OneLine(ev.Run)),
		slog.String("repository", safetext.OneLine(ev.Repository)),
		slog.String("job", safetext.OneLine(ev.Job)),
		slog.String("caller", safetext.OneLine(ev.Caller)),
		slog.String("credential", safetext.OneLine(ev.Credential)),
		slog.String("purpose", safetext.OneLine(ev.Purpose)),
		slog.String("tier", safetext.OneLine(ev.Tier)),
		slog.String("status", safetext.OneLine(ev.Status)),
	)
}

// line writes one record to the auditor's handler and reports whether it was
// written. It is not Logger.LogAttrs, and that is the point: LogAttrs drops a
// record silently when the handler's level excludes it and returns no error at
// all when the handler fails, so an act this service could not audit would
// look exactly like one it audited. Here the handler is asked whether it
// records the level before anything is built, and its own error is returned,
// so a log that is not recording is a failure the caller cannot mistake for a
// written line (D143).
//
// The record's time is UTC: a log line is read beside lines from other hosts
// and other zones, and a local time in it is a time the reader has to guess
// the zone of.
//
// The handler is used directly rather than through the logger, so the line
// carries the contract's fields and nothing else — a caller's logger attrs
// would be extra fields on a line whose shape is the contract's.
func (a *Auditor) line(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) error {
	h := a.log.Handler()
	if !h.Enabled(ctx, level) {
		return fmt.Errorf("credsvc: the audit log does not record at %s: an act this service could not audit must not be made", level)
	}
	r := slog.NewRecord(time.Now().UTC(), level, msg, 0)
	r.AddAttrs(attrs...)
	if err := h.Handle(ctx, r); err != nil {
		return fmt.Errorf("credsvc: the audit line was not written: %w", err)
	}
	return nil
}

// checked holds an event to the rules of its fields, after the scrub has run,
// and returns the first field that is not one this service can record with the
// class of thing wrong with it — never what it held — or a zero refusal for an
// event that can be written.
//
// The classes are read off the rules rather than guessed at: a value of the
// wrong shape is refusalMalformed, a value out of a closed set the contract
// owns is refusalUnknown, and a field the event needs and does not have is
// refusalMissing.
func (a *Auditor) checked(ev AuditEvent) (AuditEvent, refusal) {
	ev = a.redact(ev)
	switch {
	case !ValidStatus(ev.Status):
		return AuditEvent{}, refusal{"status", refusalUnknown}
	case ev.Purpose != "" && !ValidPurpose(ev.Purpose):
		return AuditEvent{}, refusal{"purpose", refusalUnknown}
	case ev.Run != "" && !token.ValidSegment(ev.Run):
		return AuditEvent{}, refusal{"run", refusalMalformed}
	case ev.Repository != "" && !token.ValidSegment(ev.Repository):
		return AuditEvent{}, refusal{"repository", refusalMalformed}
	case (ev.Run == "") != (ev.Repository == ""):
		// The identity carries both: a run id without the repository it
		// belongs to is not a run this service can name.
		return AuditEvent{}, refusal{"run", refusalMissing}
	case ev.Job != "" && !ValidJobName(ev.Job):
		return AuditEvent{}, refusal{"job", refusalMalformed}
	case ev.Caller != "" && !validAccount(ev.Caller):
		return AuditEvent{}, refusal{"caller", refusalMalformed}
	case (ev.Job == "") != (ev.Caller == ""):
		return AuditEvent{}, refusal{"job", refusalMissing}
	case ev.Credential != "" && !ValidCredentialName(ev.Credential):
		return AuditEvent{}, refusal{"credential", refusalMalformed}
	case (ev.Credential == "") != (ev.Tier == ""):
		// The both-or-neither rule the wire already keeps (SetCredentialHeaders,
		// CredentialHeaders): a line naming one without the other is not one
		// this service writes, and a reader that guessed the missing half
		// would be reading an answer the service did not give.
		return AuditEvent{}, refusal{"credential", refusalMissing}
	case ev.Tier != "" && !ValidTier(ev.Tier):
		return AuditEvent{}, refusal{"tier", refusalUnknown}
	}
	// A line that says a credential was used names the whole act; a refusal
	// carries only what was known when it was refused.
	if ev.Status == StatusOK {
		switch {
		case ev.Purpose == "":
			return AuditEvent{}, refusal{"purpose", refusalMissing}
		case ev.Run == "":
			return AuditEvent{}, refusal{"run", refusalMissing}
		case ev.Job == "":
			return AuditEvent{}, refusal{"job", refusalMissing}
		case ev.Credential == "":
			return AuditEvent{}, refusal{"credential", refusalMissing}
		}
	}
	return ev, refusal{}
}

// redact applies the service's own redaction to every field before any rule
// looks at one, so a value the service holds reaches neither the line nor the
// error a refused line returns (redactWriter's rule, F32).
func (a *Auditor) redact(ev AuditEvent) AuditEvent {
	if a.scrub == nil {
		return ev
	}
	ev.Run = a.scrub(ev.Run)
	ev.Repository = a.scrub(ev.Repository)
	ev.Job = a.scrub(ev.Job)
	ev.Caller = a.scrub(ev.Caller)
	ev.Credential = a.scrub(ev.Credential)
	ev.Tier = a.scrub(ev.Tier)
	ev.Purpose = a.scrub(ev.Purpose)
	ev.Status = a.scrub(ev.Status)
	return ev
}

// validAccount reports whether s is an account this line may name: the
// printable-ASCII address rule auth.go holds the installation's configured job
// accounts to, and the rule a presented claim is matched by. A "caller" that
// could not be one of those accounts is refused rather than recorded as if it
// had been verified.
func validAccount(s string) bool {
	_, ok := foldEmail(s)
	return ok
}
