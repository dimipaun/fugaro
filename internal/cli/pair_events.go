package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// `fugaro pair --json` is a machine-readable mode, not a pretty one: the
// console will drive `fugaro pair` by spawning the CLI and reading its stdout,
// so the stream is newline-delimited JSON events with a fixed shape and a
// version, and stdout carries nothing else (plan §4.5, amended 2026-10-29).
//
// The rule that governs every field is §4.5's, extended to the stream: a uid
// may appear (it is a key), and a token may not, a key may not, the keychain
// item's value may not and the typed code may not — the console must never
// learn the code it is asking the operator to type. The one exception is the
// QR payload, which the operator's own screen shows anyway: it is the content
// of a code the phone photographs, and it is the whole point of the event.
const pairEventVersion = 1

// pairQRVersion is the QR payload's own version, and it is deliberately not
// pairEventVersion: the phone pins the payload it scanned (P6.8), so a bump of
// the console's stream must not silently re-version every code already in a
// phone's hands.
const pairQRVersion = 1

// The five events the stream defines. qr, waiting and refused are written by
// this command; code and done are the wait's and the delivery's (T2b, T3),
// and their shapes live here beside the rest so the console is written against
// one set rather than against whatever a later task invents.
const (
	pairEventQR      = "qr"      // the QR's payload, the pairing uid, its expiry
	pairEventWaiting = "waiting" // the command now waits for the phone's key
	pairEventCode    = "code"    // the phone's key arrived: type the code it shows
	pairEventDone    = "done"    // §4.5's document: what was written
	pairEventRefused = "refused" // a stable code and a fixable reason
)

// The stable refusal codes, one per refusal §4.6 classes as exit 1, plus the
// one that is exit 2. They are wire values: a console switches on them, so a
// spelling here is a spelling it may rely on, and a code that names the wrong
// case sends an operator to fix the wrong thing.
const (
	pairRefusedTimeout            = "timeout_above_cap"
	pairRefusedTimeoutNotPositive = "timeout_not_positive"
	pairRefusedEnv                = "http2debug_set"
	pairRefusedProject            = "project_not_selectable"
	pairRefusedBudget             = "budget_backend_missing"
	pairRefusedAPIKey             = "api_key_missing"
	pairRefusedMismatch           = "database_not_this_projects"
	pairRefusedInvalidRole        = "invalid_role"
	pairRefusedRoleAbove          = "role_above_caller"
	pairRefusedUnplaced           = "caller_unplaced"
	pairRefusedEmail              = "email_unverified"
	pairRefusedMinter             = "minter_role_missing"
	pairRefusedDatabaseAdmin      = "database_admin_missing"
	pairRefusedUnreachable        = "database_unreachable"
)

// pairEvent is one line of the stream. Every event carries the version, its
// name, when it happened and the project it happened in; the rest is per
// event, and a field an event does not use is absent rather than empty.
type pairEvent struct {
	V       int    `json:"v"`
	Event   string `json:"event"`
	At      string `json:"at"`
	Project string `json:"project"`

	// qr: the payload the code carries (the bootstrap configuration and the
	// one-shot pairing token), the pairing uid and the token's expiry.
	Payload   string `json:"payload,omitempty"`
	UID       string `json:"uid,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// waiting: how long the command waits for the phone's key.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// code carries no field of its own, and never the fingerprint: the operator
	// reads the code off the phone, and a console that saw it here could type
	// it back without anyone looking at the phone (M3).
	// done: §4.5's document.
	Document *pairDocument `json:"document,omitempty"`
	// refused: the code an operator or a console acts on, and the reason.
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// pairDocument is §4.5's document, the record of one completed pairing. It
// is the done event's payload and nothing else: the same fields, so a console
// that reads §4.5 reads the event.
type pairDocument struct {
	Project  string      `json:"project"`
	At       string      `json:"at"`
	Pairing  pairPairing `json:"pairing"`
	Request  pairRequest `json:"request"`
	Device   pairDevice  `json:"device"`
	Wrote    []string    `json:"wrote"`
	Refused  []string    `json:"refused"`
	Warnings []string    `json:"warnings"`
}

type pairPairing struct {
	UID       string `json:"uid"`
	State     string `json:"state"`
	ExpiresAt string `json:"expires_at"`
}

type pairRequest struct {
	KeyFingerprint string `json:"key_fingerprint"`
	At             string `json:"at"`
}

type pairDevice struct {
	UID   string `json:"uid"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// pairEventWriter writes the stream: one object per line, in the order the
// things happen, stamped with the clock the command was given. project is set
// once the project is selected; it is empty for a refusal that happened before
// one was, which is a stream a console must still be able to read.
type pairEventWriter struct {
	w       io.Writer
	project string
	now     func() time.Time
}

func (p *pairEventWriter) emit(e pairEvent) error {
	e.V, e.Project = pairEventVersion, p.project
	e.At = p.now().UTC().Format(time.RFC3339)
	line, err := json.Marshal(e)
	if err != nil {
		return userErr("encoding the pair event stream: %v", err)
	}
	if _, err := fmt.Fprintf(p.w, "%s\n", line); err != nil {
		return userErr("writing the pair event stream: %v", err)
	}
	return nil
}
