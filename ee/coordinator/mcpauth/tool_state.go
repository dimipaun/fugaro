// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is the state group's read half: get_state and get_events, and the
// reach they read one orchestrator's node through. The write half —
// apply_patch — is catalogued in tools.go and registered by nothing (D2021),
// and the reach below has no method that could write even if a later task
// registered one (StateReach, TestStateReachHasNoWriteMethod).
//
// **Every read is the project's own orchestrator, and the id is checked before
// the read.** The id is a path segment of `orchestrator/<id>` in the project's
// database, so it is put to the rule the path is built by
// (orchestrator.PathState) rather than escaped into a node nobody grants. What
// a caller names is therefore a name of this project's own, and a value the
// database would refuse never reaches the reach at all.
//
// **What the tools answer is data** (spec.md §5): every answer is the JSON text
// of a document whose stored text was cleaned on the way out, and the fields
// are the ones `fugaro orchestrator state --json` and `fugaro orchestrator
// events --json` write, with the meanings schemas/orchestrator-output.schema.json
// pins — so a session that reads the state through this door and a script that
// reads it from the CLI read one contract (R3, and the schema test below).
// Nothing here is composed into a sentence this project speaks about an
// orchestrator: each value is carried under its own key.
//
// The caller's identity is not read by either handler. Every role M7 admits to
// the tool set reads every orchestrator id of the project (the spec's
// read-all), so there is nothing here for an identity to decide; the door's own
// audit line is what records who called (D920).

// StateReach is the state group's reach into one project's orchestrator node:
// the state and the audit log under `orchestrator/<id>`, and nothing else.
//
// **It has no write method, and that is the interface's own rule** (D2021,
// T2): the state group as this plan builds it can neither change the node nor
// reach a launch, so no handler a later task writes over this reach could do
// either — the two are properties of the type rather than of each registration,
// and the contract's third name (apply_patch) is served by nothing at all. A
// reach is supplied by whatever wires this door up (O16b reads it over
// internal/rtdb as the front door's own account) and by this package's tests.
type StateReach interface {
	// Load answers one orchestrator's state as the node holds it. The id has
	// already been checked against orchestrator.PathState.
	Load(ctx context.Context, id string) (orchestrator.State, error)
	// Events answers the audit log under that id, cut at since when it is
	// positive — the cut is the database's own clock's, and the reach is what
	// resolves it (fugaro orchestrator events --since does the same).
	Events(ctx context.Context, id string, since time.Duration) ([]orchestrator.Entry, error)
}

// stateOutputVersion is the version of the two documents these tools answer
// with: the CLI's own, written once as a const in
// schemas/orchestrator-output.schema.json, which both surfaces are held to. A
// consumer that reads the state through this door and from `fugaro orchestrator
// state --json` reads one contract, so the two cannot be bumped apart.
const stateOutputVersion = 1

// stateArgsSchema is the schema of get_state: the orchestrator to read.
//
// The id is required and has no pattern in the schema, because the rule that
// decides it is the database's and not this document's (orchestrator.PathState):
// a client that renders the form from this schema asks for a name, and a name
// the node could not carry is refused by the tool with the reason.
var stateArgsSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"The orchestrator to read, which is the selected project's name"}},"required":["id"],"additionalProperties":false}`)

// eventsArgsSchema is the schema of get_events: the orchestrator to read and
// how far back, in the shape `fugaro orchestrator events --since` takes.
var eventsArgsSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"The orchestrator to read, which is the selected project's name"},"since":{"type":"string","description":"Only entries newer than this duration, such as 1h or 30m"}},"required":["id"],"additionalProperties":false}`)

// stateArgs is what get_state reads out of a call's arguments.
type stateArgs struct {
	ID string `json:"id"`
}

// eventsArgs is what get_events reads out of a call's arguments. Since is a
// duration and not a stamp, as the CLI's own flag is: the cut is taken on the
// database's clock (D1230), so a time this machine minted is not a value a
// caller can ask over.
type eventsArgs struct {
	ID    string `json:"id"`
	Since string `json:"since"`
}

// RegisterStateTools registers the state group's two read tools on reg, over
// reach: get_state and get_events.
//
// It never registers apply_patch. The third name is catalogued under
// state:write (tools.go) so that nothing can register it under another scope —
// the same shape D936's two deferred tools have — and no handler for it is
// written on this side of the plan: the state group's writes stay with
// `fugaro orchestrator patch` and with the person's own credential (D1223,
// D2021).
func RegisterStateTools(reg *Registry, reach StateReach) error {
	if reg == nil || reach == nil {
		return errors.New("mcpauth: the state tools need a registry and a reach into the orchestrator's node")
	}
	st := stateTools{reach: reach}
	for _, t := range []Tool{
		{Name: "get_state", InputSchema: stateArgsSchema, Handler: st.getState},
		{Name: "get_events", InputSchema: eventsArgsSchema, Handler: st.getEvents},
	} {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// stateTools is the two handlers over one reach. The registry reads the scope a
// call needs out of the catalogue on every call, so neither handler holds one.
type stateTools struct {
	reach StateReach
}

// stateID reads the orchestrator a call names, and it is the check that happens
// before any read.
//
// The id is what `orchestrator.PathState` builds a path out of, so a value that
// rule refuses is a value the database would refuse — an id that is not a name,
// or one a path could not carry — and it is refused here rather than sent to a
// reach that would build a node nobody grants. The id a caller sent is a value
// of the caller's own, so it is bounded and cleaned before it enters a refusal
// (M-g's rule for every value this package answers with).
func stateID(raw string) (string, error) {
	if _, err := orchestrator.PathState(raw); err != nil {
		return "", fmt.Errorf("%q is not an orchestrator of this project: %w", safetext.Field(raw), err)
	}
	return raw, nil
}

// stateDoc is get_state's answer: the document `fugaro orchestrator state
// --json` prints for a node, field for field and meaning for meaning.
//
// Rev is the revision the node was at — the base_rev a patch built from that
// read would carry — and it is written even when it is 0, because 0 is the
// revision of a node nobody has written yet. Source is always `node`: this door
// reads the live state and not the brief the daemon caches, which is a file on
// the person's own machine (D1243).
type stateDoc struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Source        string `json:"source"`
	Rev           int64  `json:"rev"`
	SchemaVersion int    `json:"schema_version"`
	Brief         string `json:"brief"`
}

// eventsDoc is get_events' answer: the document `fugaro orchestrator events
// --json` prints, with the log in the order the server stamped it (D1230).
//
// The CLI's `since` member is deliberately not here. What it carries there is
// the cut `--since` was resolved to **on the database's own clock**, and this
// tool cannot state it: StateReach.Events is handed a duration and answers
// entries, so the cut it applied is not a value it can return, and a stamp this
// tool minted from its own clock would be exactly what D1230 refuses — a
// laptop's clock standing in for the server's. The caller sent the duration and
// the entries are the answer; nothing else in the document moves, so a reader
// of both surfaces reads one shape.
type eventsDoc struct {
	Version int                  `json:"version"`
	Kind    string               `json:"kind"`
	ID      string               `json:"id"`
	Events  []orchestrator.Entry `json:"events"`
}

// getState is the get_state tool: one orchestrator's generated brief, the
// revision the node is at and the schema version of the state behind it.
//
// The brief is orchestrator.Summarize's, which is the same text the CLI prints
// and the session-start hook shows: generated on read and capped, with
// everything it carries out of the document fenced as data (G3), so a brief a
// session wrote cannot arrive at another session as anything but a value.
//
// A reach that fails is the tool's refusal and never a partial answer: the
// document is written whole or not at all, and an empty brief would read as an
// orchestrator with no state rather than as a read that failed.
func (t stateTools) getState(ctx context.Context, call Call) (any, error) {
	var a stateArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	id, err := stateID(a.ID)
	if err != nil {
		return nil, err
	}
	s, err := t.reach.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the state of the orchestrator %s: %w", safetext.Field(id), err)
	}
	return answerJSON(stateDoc{
		Version: stateOutputVersion, Kind: "state", ID: id, Source: "node",
		Rev: s.Rev, SchemaVersion: s.SchemaVersion, Brief: orchestrator.Summarize(s),
	})
}

// getEvents is the get_events tool: one orchestrator's audit log, in the order
// the server stamped it and optionally cut at a duration.
//
// There is no cursor (D1229): the log is level-triggered, nothing replays it,
// and a caller that wants only what is new asks again with a `since` — which is
// why the duration is the whole of the call's memory. What is read is what was
// stored, and the stored text is cleaned on the way out (logEntries), because
// this is the surface a client that never saw the node reads it through.
func (t stateTools) getEvents(ctx context.Context, call Call) (any, error) {
	var a eventsArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	id, err := stateID(a.ID)
	if err != nil {
		return nil, err
	}
	var since time.Duration
	if a.Since != "" {
		d, err := time.ParseDuration(a.Since)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("since %q is not a duration such as 1h or 30m", safetext.Field(a.Since))
		}
		since = d
	}
	entries, err := t.reach.Events(ctx, id, since)
	if err != nil {
		return nil, fmt.Errorf("reading the events of the orchestrator %s: %w", safetext.Field(id), err)
	}
	return answerJSON(eventsDoc{
		Version: stateOutputVersion, Kind: "events", ID: id, Events: logEntries(entries),
	})
}

// logEntries is the log as the tool answers it: the entries in the order the
// server stamped them, each one's text cleaned and bounded.
//
// The order is orchestrator.SortEntries', which is the rule the CLI applies to
// the same log (D1230): the log is a map under its own node, so what a read
// returns is the node's iteration order and not a sequence. One function sorts
// both surfaces' answers, so the door and the CLI cannot come to order one log
// differently.
//
// The stamp is truncated to the second, which is the precision the log writes
// (Append stamps every entry) and the only one the output schema admits: a
// hand-written node carrying a finer stamp is answered at the log's own
// precision rather than in a document a consumer's schema refuses.
//
// The text is cleaned here rather than trusted, because it is the one free text
// in an entry and the one thing a client renders: it is made one plain line and
// cut at the writer's own cap (MaxAuditTextRunes) — an entry a hand-written node
// gave a longer one is answered at the length the log would have kept — and the
// whole document is passed over again by answerJSON (M-g).
func logEntries(entries []orchestrator.Entry) []orchestrator.Entry {
	out := make([]orchestrator.Entry, 0, len(entries))
	for _, e := range entries {
		e.At = e.At.UTC().Truncate(time.Second)
		e.Text = safetext.Clip(safetext.OneLine(e.Text), orchestrator.MaxAuditTextRunes)
		out = append(out, e)
	}
	orchestrator.SortEntries(out)
	return out
}
