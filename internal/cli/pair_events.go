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

// The five events the stream defines. qr, waiting and refused are written by
// this command; code and done are the wait's and the delivery's (T2b, T3),
// and their shapes live here beside the rest so the console is written against
// one set rather than against whatever a later task invents.
const (
	pairEventQR      = "qr"      // the QR's payload, the pairing uid, its expiry
	pairEventWaiting = "waiting" // the command now waits for the phone's key
	pairEventCode    = "code"    // the fingerprint to compare, read as a key
	pairEventDone    = "done"    // §4.5's document: what was written
	pairEventRefused = "refused" // a stable code and a fixable reason
)

// The stable refusal codes, one per refusal §4.6 classes as exit 1, plus the
// one that is exit 2. They are wire values: a console switches on them, so a
// spelling here is a spelling it may rely on.
const (
	pairRefusedTimeout       = "timeout_above_cap"
	pairRefusedInvalidRole   = "invalid_role"
	pairRefusedRoleAbove     = "role_above_caller"
	pairRefusedUnplaced      = "caller_unplaced"
	pairRefusedEmail         = "email_unverified"
	pairRefusedMinter        = "minter_role_missing"
	pairRefusedDatabaseAdmin = "database_admin_missing"
	pairRefusedUnreachable   = "database_unreachable"
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
	// code: the fingerprint the CLI derived from the key it read, which is
	// the same value the operator sees on the phone and is not a secret.
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
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
