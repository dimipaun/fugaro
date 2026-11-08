// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is M10's audit: one structured line per call and per refusal, on
// the pattern of internal/credsvc/audit.go (spec.md §4: "one structured line
// per act and per refusal, on the pattern of internal/credsvc/audit.go, naming
// the identity and the outcome and never a value").
//
// **What is audited, and what is not.** Two events: a token this door refused,
// and a `tools/call` that named a tool the catalogue holds — whatever the door
// answered it, its own 403 and a tool's own refusal included. The unit is the
// tool, which is why the boundary falls where it does: a line names the
// identity, the tool and the scopes, and a request the JSON-RPC layer refused
// before a tool of this installation was named — a body that is not a request,
// a method this endpoint does not have, a `tools/call` naming no tool or a name
// the catalogue does not hold — has no tool to name and no scope to check, and
// neither has a request this endpoint serves without one: an `initialize`, a
// `ping` and a `tools/list` are answered to an admitted token and recorded
// nowhere, and a notification is answered with no body at all. Such a request
// is answered by the protocol, with its own status and its own JSON-RPC code,
// and it is not an event about a person's call. SECURITY.md says the same in
// the operator's words.
//
// **The line is the durable copy and the record is the readable view** (the
// amendment of 2026-11-04, reconciliation.md §4a): the per-identity record is
// bounded, so a caller that fills it erases the view and not the evidence, and
// a line is written for every event whether or not the record takes the entry.
//
// **A refusal of a token writes a line and no record entry**, which is finding
// M-e's own rule read at the other end: a stranger with a loop grows nothing in
// the store, and the operator of the installation can still see the loop. A
// record entry is written for a call, where the caller has already proved a
// token this installation minted.
//
// **No field can hold a value.** credsvc arms its auditor with the service's
// own redaction because that service holds credentials and a credential's value
// is shaped exactly like its name; this one holds nothing to redact, and its
// two caller-derived fields are held to closed sets instead: the identity is
// what a record the store already holds says (and is checked to be one
// `user:` entry, identity.go's own rule), the tool has to be a name the
// catalogue holds, the scopes have to be names this build knows, and the
// outcome and the reason are this file's own constants. The token value is
// never handed to this file at all — the door hashes it and keeps it nowhere
// (store.go's LookupByHash).
//
// **An act this door could not audit is not answered** (D143, the pattern's
// fail-closed rule): Write returns an error, and the door answers its own fault
// instead of the tool's answer. A refusal is the one event that cannot be
// un-made — it is already the door's answer — so a refusal whose line could not
// be written is recorded on the door's own logger instead, which is the account
// M7's refusal line already uses.

// The line's message, and the message an event that could not be recorded
// leaves behind: a reader greps `mcpauth: audit` for this door's events and
// reads the rest of the line as JSON.
const (
	auditMessage        = "mcpauth: audit"
	auditRefusedMessage = "mcpauth: audit event refused"

	// auditUnavailable is the one answer a call gets when this door cannot
	// record it, whenever the fault is found: before a tool runs, or when the
	// line fails while being written. The caller's fact is one — the act was
	// not made and there is no line to read — so the sentence is one.
	auditUnavailable = "this door could not record the call, so it answers nothing: the audit is what makes a call accountable, and an act it cannot audit is not made"
)

// The outcomes a line carries. A closed set, for the reason the reasons below
// are one.
const (
	// OutcomeOK is a call a tool answered.
	OutcomeOK = "ok"
	// OutcomeRefused is a call or a token this door turned away: the caller is
	// told why and could act on it.
	OutcomeRefused = "refused"
	// OutcomeFailed is an act this installation's own fault stopped — a store
	// that could not be read, a member list that could not be read, an answer
	// that could not be rendered. Nothing the caller did would have changed it,
	// which is the distinction the door's 500s are about (store.go's
	// ErrTokenNotFound and identity.go's ErrIdentityOffTheLists exist for it).
	OutcomeFailed = "failed"
)

// ValidOutcome reports whether an outcome is one of the three above.
func ValidOutcome(outcome string) bool {
	return outcome == OutcomeOK || outcome == OutcomeRefused || outcome == OutcomeFailed
}

// The reasons a line carries: why a request was refused, or why a call was
// answered as it was. The set is closed for the same reason credsvc's statuses
// are — the field is written into a line an operator greps and a monitor
// counts, so a reason some caller composed would be that caller's own text.
//
// Every one of them is this file's own words about the door's own decision, and
// none of them repeats anything a caller sent.
const (
	// ReasonNoCredential is a request that carried no bearer token this
	// endpoint reads at all (resource.go's bearerToken).
	ReasonNoCredential = "no_credential"
	// ReasonUnknownToken is a value that resolves to no record of this
	// installation: one that was never minted here, and one whose family was
	// revoked, which D930 makes the same thing (store.go's ErrTokenNotFound).
	ReasonUnknownToken = "unknown_token"
	// ReasonUnusableToken is a record that resolved and a token the door
	// refused on its own terms: the wrong kind, no expiry, expired, or a scope
	// this build does not know (resource.go's usable).
	ReasonUnusableToken = "unusable_token"
	// ReasonNoRole is a good token whose identity is on neither the
	// installation's launcher nor its operator list (D933).
	ReasonNoRole = "no_role"
	// ReasonNoSuchTool is a call naming a tool the catalogue holds and nothing
	// serves yet: D936's deferred pair, which a client can read anywhere but
	// tools/list.
	ReasonNoSuchTool = "no_such_tool"
	// ReasonScopeNotHeld is a call naming a tool whose scope the token does not
	// hold (D927). It is the refusal that says a token was minted too narrowly,
	// which is why the line carries the scopes it does hold beside it.
	ReasonScopeNotHeld = "scope_not_held"
	// ReasonBadArguments is a call whose arguments are not the one JSON object
	// tools/call takes.
	ReasonBadArguments = "bad_arguments"
	// ReasonToolRefused is a tool's own refusal, which MCP answers inside a
	// successful result with `isError` rather than as a protocol error.
	ReasonToolRefused = "tool_refused"
	// ReasonUnreadable is something of this installation's the door needed and
	// could not read or write: the store, the member lists, or the encoder of a
	// tool's answer.
	ReasonUnreadable = "unreadable"
)

// ValidReason reports whether a reason is one of the set above.
func ValidReason(reason string) bool {
	switch reason {
	case ReasonNoCredential, ReasonUnknownToken, ReasonUnusableToken, ReasonNoRole,
		ReasonNoSuchTool, ReasonScopeNotHeld, ReasonBadArguments, ReasonToolRefused,
		ReasonUnreadable:
		return true
	}
	return false
}

// ErrOperatorOnly refuses a tool this installation serves to its operators and
// to nobody else (D933: "a launcher may call the task tools, an operator may
// also read the audit record"). It is the tools' own rule rather than the
// door's — M7 admits both roles to every tool of the catalogue and hands the
// handler the role to state its own rule with (Call.Role) — and it is a
// sentinel so that the two tools that carry it refuse in one shape.
var ErrOperatorOnly = errors.New("mcpauth: this tool is an operator's: the front door serves it to the people on the installation's operator list, and fugaro user is the command that changes them")

// ErrStoreUnreadable is a tool's report that the store this installation's
// records live in could not be read or written: the fault authorize answers as
// a 500, and the one OutcomeFailed and ReasonUnreadable exist for. A tool wraps
// it and leaves the store's own error out of the chain's message, because what
// the caller reads is the door's sentence and not an installation's object
// names; callTool turns it into the door's fault rather than a denial, so a
// revocation that could not be made is not recorded as one the door refused.
var ErrStoreUnreadable = errors.New("mcpauth: this installation's store could not be read or written")

// AuditEvent is one audited event: exactly the fields the line carries, and no
// other. Every one is a name this installation already holds or a constant of
// this file, and there is no field a value could be written into.
//
// Which fields an event must have depends on what it records. A call names the
// whole act: the identity, the tool and the scopes the token holds. A token
// refusal carries only what was known when it was refused — a request that
// carried no credential names no identity — and a field that is set is held to
// its rule either way.
type AuditEvent struct {
	// Identity is the exact `user:` entry the token resolved to (D923, D933),
	// or empty for a request refused before one was known. It is read from the
	// record the store holds and never from the request, and it is refused
	// unless it is one person written as `user:<email>`.
	Identity string
	// Tool is the tool the call named, and it has to be a name the catalogue
	// holds: a call leaves a line because it named a tool of this installation.
	// A token refusal names no tool.
	Tool string
	// Scopes are the scopes the token carries, which are what tells an operator
	// whether a refusal was about a token minted too narrowly. A token refusal
	// carries none: the scopes are unknown until the record resolves, and one
	// that resolved was refused for another reason (ReasonNoRole).
	Scopes []string
	// Outcome is what the door did: OutcomeOK, OutcomeRefused or
	// OutcomeFailed.
	Outcome string
	// Reason is why, out of the set above. It is empty exactly when the outcome
	// is OutcomeOK — a call that was answered has nothing to explain.
	Reason string
}

// Auditor writes this door's audit lines. It is safe for concurrent use (the
// handler is), and it holds the handler and nothing else.
type Auditor struct {
	h slog.Handler
}

// NewAuditor returns the auditor writing to h. A nil handler is refused: an act
// this door could not audit must not be made (D143), so a caller with nowhere
// to write does not get an auditor that silently drops lines.
//
// The parameter is the handler and not a logger, which is credsvc's own
// reasoning: a logger is a handler plus whatever it was built with
// (Logger.With, Logger.WithGroup), and an auditor built from one would write
// that logger's attrs and groups on every audit line. Handed the handler
// itself, this door's lines are its five fields and nothing else.
func NewAuditor(h slog.Handler) (*Auditor, error) {
	if h == nil {
		return nil, errors.New("mcpauth: no handler: a call or a refusal this door could not audit must not be made")
	}
	return &Auditor{h: h}, nil
}

// CanRecord reports whether this auditor's handler records the audit level, so
// the door can ask before an act that the line about it can be written at all.
// It is the decidable half of D143: a sink that records nothing — a level
// filter, a handler that was never armed — is found out here, before a tool
// that deletes is handed a call, because the line names the outcome and so
// cannot be written before the tool runs. A handler that fails while writing
// the line is not decidable in advance; that one is auditedCall's, and the act
// has been made by then.
//
// A zero Auditor — a Server built by hand rather than by NewServer — records
// nothing and answers so, which is the same answer a level filter gives.
func (a *Auditor) CanRecord(ctx context.Context) bool {
	if a == nil {
		return false
	}
	return a.h.Enabled(ctx, slog.LevelInfo)
}

// Write records one audited event as one structured line, and returns an error
// if it could not be recorded — in which case the door answers its own fault
// rather than the call (D143).
//
// Every field is written, empty ones included: the line's shape is the
// contract's, so a reader does not have to tell "not known" from "not there".
// Every field goes through internal/safetext on the way out — an identity is
// another writer's text, and a call's tool name reaches this line only through
// the catalogue but is escaped all the same — so an escape sequence or a bidi
// control in an address reaches neither the terminal nor the log viewer.
func (a *Auditor) Write(ctx context.Context, ev AuditEvent) error {
	ev, bad := checked(ev)
	if bad.field != "" {
		// One line saying the event was refused, naming the field and the class
		// of thing wrong with it and never what it held (the no-echo rule): both
		// are this file's own text. The two errors are joined rather than one
		// dropped, because a handler that cannot write is not silent either.
		return errors.Join(bad, a.line(ctx, slog.LevelWarn, auditRefusedMessage,
			slog.String("field", bad.field),
			slog.String("reason", bad.reason)))
	}
	return a.line(ctx, slog.LevelInfo, auditMessage,
		slog.String("identity", safetext.OneLine(ev.Identity)),
		slog.String("tool", safetext.OneLine(ev.Tool)),
		slog.String("scopes", safetext.OneLine(strings.Join(ev.Scopes, ","))),
		slog.String("outcome", safetext.OneLine(ev.Outcome)),
		slog.String("reason", safetext.OneLine(ev.Reason)),
	)
}

// line writes one record to the auditor's handler and reports whether it was
// written. It is not Logger.LogAttrs, and that is the point: LogAttrs drops a
// record silently when the handler's level excludes it and returns no error at
// all when the handler fails, so a call this door could not audit would look
// exactly like one it audited. Here the handler is asked whether it records the
// level before anything is built, and its own error is returned.
//
// The record's time is UTC, as credsvc's is: a log line is read beside lines
// from other hosts and other zones.
func (a *Auditor) line(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) error {
	h := a.h
	if !h.Enabled(ctx, level) {
		return fmt.Errorf("mcpauth: the audit log does not record at %s: a call or a refusal this door could not audit must not be answered", level)
	}
	r := slog.NewRecord(time.Now().UTC(), level, msg, 0)
	r.AddAttrs(attrs...)
	if err := h.Handle(ctx, r); err != nil {
		return fmt.Errorf("mcpauth: the audit line was not written: %w", err)
	}
	return nil
}

// The classes an event's field can be wrong in, so a refusal says what kind of
// thing was wrong and not only which field — the class is this file's own text,
// like the field name, and never a value.
const (
	// refusalMissing: the event needs the field and it is empty.
	refusalMissing = "missing"
	// refusalMalformed: the field is set and is not the shape it holds.
	refusalMalformed = "malformed"
	// refusalUnknown: the field is one of a closed set and the value is not in
	// it.
	refusalUnknown = "unknown"
	// refusalUnexpected: the field is set and this kind of event does not carry
	// it.
	refusalUnexpected = "unexpected"
)

// auditRefusal is why an event was not recorded: the field, and the class of
// thing wrong with it. Both are this file's own text, so a refusal can say what
// was wrong and still never repeat what the field held.
type auditRefusal struct {
	field  string
	reason string
}

// Error is the refusal in words, carrying the field and the class and nothing
// the event held — which is what makes it safe to hand back to a caller that
// will log it.
func (r auditRefusal) Error() string {
	return fmt.Sprintf("mcpauth: the audit event's %s is %s", r.field, r.reason)
}

// checked holds an event to the rules of its fields, and returns the first
// field that is not one this door can record with the class of thing wrong with
// it, or a zero refusal for an event that can be written.
func checked(ev AuditEvent) (AuditEvent, auditRefusal) {
	if !ValidOutcome(ev.Outcome) {
		return AuditEvent{}, auditRefusal{"outcome", refusalUnknown}
	}
	if ev.Reason != "" && !ValidReason(ev.Reason) {
		return AuditEvent{}, auditRefusal{"reason", refusalUnknown}
	}
	if ev.Outcome == OutcomeOK && ev.Reason != "" {
		return AuditEvent{}, auditRefusal{"reason", refusalUnexpected}
	}
	if ev.Outcome != OutcomeOK && ev.Reason == "" {
		// A refusal or a failure that names no reason is a line an operator can
		// do nothing with.
		return AuditEvent{}, auditRefusal{"reason", refusalMissing}
	}
	if ev.Identity != "" {
		if _, ok := userEntry(ev.Identity); !ok {
			return AuditEvent{}, auditRefusal{"identity", refusalMalformed}
		}
	}
	if ev.Tool != "" {
		if _, ok := catalogueEntry(ev.Tool); !ok {
			return AuditEvent{}, auditRefusal{"tool", refusalUnknown}
		}
	}
	if ev.Outcome == OutcomeOK {
		// A call that was answered named a tool of this installation and a
		// token that holds scopes: a line with no tool and no scope is not the
		// line of an act this door made.
		if ev.Tool == "" {
			return AuditEvent{}, auditRefusal{"tool", refusalMissing}
		}
		if len(ev.Scopes) == 0 {
			return AuditEvent{}, auditRefusal{"scopes", refusalMissing}
		}
	}
	for _, s := range ev.Scopes {
		if !KnownScope(s) {
			return AuditEvent{}, auditRefusal{"scopes", refusalUnknown}
		}
	}
	return ev, auditRefusal{}
}

// refuseToken writes the one audit line a token refusal leaves and then the
// refusal itself, in that order: the line is what the operator keeps, and it is
// written whether or not the answer reaches the caller. Every refusal of
// authorize goes through here, so "one line per refusal, and never two" is a
// property of one function rather than of six call sites.
//
// The reason is passed in because the door is the only place that knows it: two
// refusals that answer with the same error and the same status — a token that
// was never minted and a token whose family was revoked — are one thing to the
// caller and two things to the operator. The outcome is passed in for the same
// reason: a token this door turned away is a refusal, and a store or a member
// list this installation could not read is a fault the caller could do nothing
// about (a 500), which is the distinction OutcomeFailed exists for.
func (s *Server) refuseToken(ctx context.Context, w http.ResponseWriter, status int, errType, outcome, reason, scope, identity, description string, challenge bearerChallenge) {
	s.auditRefusal(ctx, identity, outcome, reason)
	s.refuseBearer(w, status, errType, scope, identity, description, challenge)
}

// auditRefusal writes the line a refusal of a token leaves. There is no record
// entry beside it: finding M-e's rule is that a refusal writes nothing to the
// store, so a stranger with a loop grows nothing here and the line is what the
// operator has.
//
// A line that could not be written cannot un-refuse a request. The refusal
// stands, and the fault is recorded on the door's own logger — the account
// M7's refusal line already uses — rather than swallowed.
func (s *Server) auditRefusal(ctx context.Context, identity, outcome, reason string) {
	a := s.audit
	if a == nil {
		// A server NewServer built always has an auditor; this is the zero
		// Server, which records nothing rather than panicking on a refusal.
		return
	}
	if err := a.Write(ctx, AuditEvent{Identity: identity, Outcome: outcome, Reason: reason}); err != nil {
		s.logComplaint("the MCP endpoint could not record a refusal", err)
	}
}

// auditedCall audits one tools/call and answers whether the door may answer it
// at all: the one line and the one record entry this call leaves are written
// before the answer, and a call whose line could not be written is answered as
// the door's own failure (D143). The line names the outcome, so it cannot be
// written before the tool runs: what that leaves decidable in advance is
// whether the sink records at all, and callTool asks that first, before a tool
// is handed the call. A handler that fails while writing the line is a fault no
// check can see coming, and there the act has been made — the caller is told
// this door could not record the call, which is the only place that fault can
// be reported (SECURITY.md states the residual). The caller's refusal is
// withheld with it, deliberately: the answer and the line are one event, and a
// refusal answered without its line would be the missing half of a pair an
// operator reads together.
func (s *Server) auditedCall(w http.ResponseWriter, r *http.Request, id json.RawMessage, auth authorized, tool, outcome, reason string) bool {
	if err := s.auditCall(r.Context(), auth, tool, outcome, reason); err != nil {
		s.rpcError(w, http.StatusOK, id, codeInternal, auditUnavailable)
		return false
	}
	return true
}

// auditEntryGrace bounds the record write this door owes after the caller's own
// request has ended (auditCall). It is a grace and not the request's own bound,
// which is why it is a value of its own rather than the context's: the write is
// made on the caller's values without its cancellation, and a caller that is
// gone must not be the reason a later read of the record finds a hole in it —
// nor the reason this door holds a finished request open for ever. It is the
// order ee/coordinator/sweep.go's finalWriteGrace is of, because it is the same
// shape: bookkeeping about work the caller will never see the answer to.
const auditEntryGrace = 30 * time.Second

// auditCall writes the line and the record entry one call leaves.
//
// The two are not one act, and the amendment of 2026-11-04 is why: the line is
// the durable copy and the record is the readable view (reconciliation.md §4a),
// so a record that could not be written loses an entry and not the evidence,
// and the door goes on to answer the call. The fault is recorded on the door's
// own logger, so a store that stopped taking entries is visible rather than
// quietly missing from a view an operator trusts. The line is the one that
// fails closed, and it is the caller's to report.
func (s *Server) auditCall(ctx context.Context, auth authorized, tool, outcome, reason string) error {
	ev := AuditEvent{Identity: auth.identity, Tool: tool, Scopes: auth.scopes, Outcome: outcome, Reason: reason}
	a := s.audit
	if a == nil {
		return nil
	}
	if err := a.Write(ctx, ev); err != nil {
		return err
	}
	entry := AuditEntry{
		At:       s.now().UTC(),
		ClientID: auth.clientID,
		FamilyID: auth.familyID,
		Tool:     tool,
		Scopes:   slices.Clone(auth.scopes),
		Outcome:  outcome,
		Reason:   reason,
	}
	// The entry is owed to the record after the caller's own request has ended:
	// a client that disconnects as the tool answers cancels the request's
	// context, which is the moment this write runs, and a write that stopped
	// there would leave an operator's next read a hole the record does not
	// mention. So it runs on the caller's values without its cancellation and
	// under a bound of its own. The line above stays on the caller's context:
	// it is this door's own copy of the event, it is written to a sink the
	// caller cannot cancel, and it is the one whose failure the caller is told
	// about (auditedCall).
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditEntryGrace)
	defer cancel()
	if err := s.store.AppendAudit(actx, auth.identity, entry); err != nil {
		s.logComplaint("the MCP endpoint could not write an audit record entry", err)
	}
	return nil
}

// logComplaint records this door's own fault on its logger: the one thing an
// audit that could not be written must not be is silent. The error's text is
// this package's own — a refusal's message names the field of a record and
// never a value — and it is written as an error, which is what an operator's
// alerting reads.
func (s *Server) logComplaint(what string, err error) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error(what, "error", err.Error())
}
