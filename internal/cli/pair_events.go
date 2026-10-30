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

// The stream's events. qr, waiting and refused are written by this command;
// code and done are the wait's and the delivery's (T2b, T3); listed and
// revoked are `pair ls`'s and `pair revoke`'s, which are acts of their own and
// say so rather than reporting themselves as a completed pairing. Their shapes
// live here beside the rest so the console is written against one set rather
// than against whatever a later task invents.
const (
	pairEventQR      = "qr"      // the QR's payload, the pairing uid, its expiry
	pairEventWaiting = "waiting" // the command now waits for the phone's key
	pairEventCode    = "code"    // the phone's key arrived: type the code it shows
	pairEventDone    = "done"    // §4.5's document: what was written
	pairEventRefused = "refused" // a stable code and a fixable reason
	pairEventListed  = "listed"  // the pairings this installation holds (§4.2)
	pairEventRevoked = "revoked" // what a revocation deleted, in its order (§4.3)
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
	pairRefusedIdentity           = "caller_identity_unknown"
	pairRefusedEmail              = "email_unverified"
	pairRefusedMinter             = "minter_role_missing"
	pairRefusedDatabaseAdmin      = "database_admin_missing"
	pairRefusedUnreachable        = "database_unreachable"
	pairRefusedNoKey              = "no_key_before_timeout"
	pairRefusedRequestKey         = "request_key_invalid"
	pairRefusedRequestMalformed   = "request_malformed"
	pairRefusedCodeMissing        = "code_not_typed"
	pairRefusedCodeMismatch       = "fingerprint_mismatch"
	pairRefusedKeychain           = "keychain_write_failed"
	pairRefusedDelivery           = "delivery_write_failed"
	pairRefusedInterrupted        = "interrupted" // the operator stopped the command
	pairRefusedNoSelector         = "revoke_needs_a_selector"
	pairRefusedTwoSelectors       = "revoke_has_two_selectors"
	pairRefusedNoPairing          = "pairing_not_found"
	pairRefusedTwoPairings        = "pairing_name_not_unique"
	pairRefusedKeychainDelete     = "keychain_delete_failed"
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
	// listed and revoked: §4.5's document for those two acts, under a field of
	// its own. They are not one shared `document` field holding three shapes,
	// because a console switches on the event's name and then reads the
	// document, and a field whose shape depends on an earlier field is a
	// decoder a console has to guess at.
	List   *pairListDocument   `json:"list,omitempty"`
	Revoke *pairRevokeDocument `json:"revoke,omitempty"`
	// refused: the code an operator or a console acts on, and the reason.
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// pairDocument is §4.5's document, the record of one completed pairing. It
// is the done event's payload and nothing else: the same fields, so a console
// that reads §4.5 reads the event.
type pairDocument struct {
	Project string      `json:"project"`
	At      string      `json:"at"`
	Pairing pairPairing `json:"pairing"`
	Request pairRequest `json:"request"`
	Device  pairDevice  `json:"device"`
	// keychain_item is the OS keychain item the pairing's MAC key was stored
	// at, and it is the name and never the value: §4.5's own rule is that the
	// item's value never appears, which is a rule that has to be about the
	// value because the name is what the operator records in the local
	// config's mailbox block (§6) — and a console driving this command has no
	// other way to tell them which name to record.
	KeychainItem string   `json:"keychain_item"`
	Wrote        []string `json:"wrote"`
	Refused      []string `json:"refused"`
	Warnings     []string `json:"warnings"`
}

// pairListDocument is §4.5's document for `pair ls` (§4.2): the pairings this
// installation holds, one row per device node. warnings is present and empty,
// like the pairing document's — this act has no warning to give, and a console
// is written against one shape rather than against an absent field.
type pairListDocument struct {
	Project  string          `json:"project"`
	At       string          `json:"at"`
	Devices  []pairDeviceRow `json:"devices"`
	Warnings []string        `json:"warnings"`
}

// pairDeviceRow is one device node as `pair ls` prints it, and it is §4.2's
// list and nothing more: the uid (a key, and therefore printable), the name,
// the administered email (D226), the role (D225) and the pairing time. No
// token, no key and no keychain item's value — the node holds none of them
// (M13) — and no column saying whether the device's Auth user still exists:
// the Auth user is not in the database, so database admin cannot answer that
// (decision 11).
type pairDeviceRow struct {
	UID   string `json:"uid"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	At    string `json:"at"`
}

// pairRevokeDocument is §4.5's document for `pair revoke` (§4.3): the device
// that was revoked, the keychain item this laptop held for it and what was
// deleted, in the order it was deleted (M4's order, which is the command).
// keychain_item is the item's name and never its value (§4.5's rule, M5), and
// the Auth user appears in no field: it is not deleted, and a document that
// named it under `wrote` would claim a deletion this command cannot make.
type pairRevokeDocument struct {
	Project      string     `json:"project"`
	At           string     `json:"at"`
	Device       pairDevice `json:"device"`
	KeychainItem string     `json:"keychain_item"`
	Wrote        []string   `json:"wrote"`
	Refused      []string   `json:"refused"`
	Warnings     []string   `json:"warnings"`
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
