// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// This file is the audit writer's own tests: M10's red step, the line's shape,
// and the two rules the amendment of 2026-11-04 and credsvc's D143 put on it —
// the logging line is the durable copy, and an act this door could not audit is
// not answered.

// auditTo is the server option that writes this door's audit lines into buf.
// The audit is a sink of its own and not the door's logger: M7's refusal line
// is one line on the logger (finding M-e) and this is the structured line the
// plan's §4 asks for, so a test that armed one has to arm the other to read
// both.
func auditTo(t *testing.T, buf *bytes.Buffer) ServerOption {
	t.Helper()
	a, err := NewAuditor(slog.NewTextHandler(buf, nil))
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	return WithAuditor(a)
}

// auditLines is what the sink holds, one line per element.
func auditLines(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestEveryRefusalWritesOneLineNamingNoValue is M10's red step: one structured
// line per refusal, naming the identity the token resolved to — when it
// resolved to one — and never a value (spec.md §4's "one structured line per
// act and per refusal ... naming the identity and the outcome and never a
// value").
//
// The token below is a recognizable marker string, so the second half of the
// test is decidable rather than a matter of trust: the line carries neither the
// marker nor its first eight characters, which is the shape a partial echo
// would have. It failed before this task's writer existed: the door answered
// the three refusals and recorded nothing an operator could read beside the
// process log.
func TestEveryRefusalWritesOneLineNamingNoValue(t *testing.T) {
	var audit bytes.Buffer
	// Nobody is on the lists, which is what makes the third refusal below the
	// flow's own token: the flow mints it — the lists decide who may call a
	// tool and not who may sign in — and the door turns it away by identity.
	ts := newResourceServer(t,
		WithRoster(rosterOf(Members{})),
		auditTo(t, &audit))
	_, tokens := ts.runFlow(t, testClientRedirect)

	refusals := []struct {
		what   string
		header string
	}{
		{"a request with no credential", ""},
		{"a value this installation never minted", "Bearer " + testSeededToken},
		{"a token minted for somebody off the lists", "Bearer " + tokens.AccessToken},
	}
	for _, tc := range refusals {
		ts.rpcHeader(t, tc.header, toolsListBody)
	}

	lines := auditLines(t, &audit)
	if len(lines) != len(refusals) {
		t.Fatalf("the door wrote %d audit lines for %d refusals:\n%s", len(lines), len(refusals), audit.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, auditMessage) || !strings.Contains(line, "outcome="+OutcomeRefused) {
			t.Errorf("a refusal was audited as %q; want one line saying %s and naming the outcome %s", line, auditMessage, OutcomeRefused)
		}
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "identity="+testIdentity) {
		t.Errorf("the refusal of a token acting for %s does not name it: %q", testIdentity, last)
	}
	for _, value := range []string{testSeededToken, testSeededToken[:8], tokens.AccessToken, tokens.RefreshToken} {
		if value != "" && strings.Contains(audit.String(), value) {
			t.Errorf("the audit carries %q: a line names what was refused and never the value that was presented", value)
		}
	}
}

// TestTheLoggingLineIsTheDurableCopy is the amendment's own row: the
// per-identity record is bounded, so a caller that fills it erases the readable
// view and not the evidence — every call leaves its line, whatever the record
// keeps (reconciliation.md §4a, "the Cloud Logging line is the durable copy and
// the per-identity record is the readable view").
func TestTheLoggingLineIsTheDurableCopy(t *testing.T) {
	var audit bytes.Buffer
	ts := newResourceServer(t, auditTo(t, &audit))
	ts.serve(t, "get_task_status", func(context.Context, Call) (any, error) { return "run-7 is implementing", nil })
	_, tokens := ts.runFlow(t, testClientRedirect)

	const calls = MaxAuditEntries + 3
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task_status","arguments":{}}}`
	for i := 0; i < calls; i++ {
		wantResult(t, ts.rpc(t, tokens.AccessToken, body))
	}

	if lines := auditLines(t, &audit); len(lines) != calls {
		t.Fatalf("the audit log holds %d lines for %d calls: the line is the copy a caller cannot flush, so every call writes one", len(lines), calls)
	}
	rec, err := ts.store.Store.GetAudit(context.Background(), testIdentity)
	if err != nil {
		t.Fatalf("reading the record the calls left: %v", err)
	}
	if len(rec.Entries) != MaxAuditEntries {
		t.Fatalf("the record holds %d entries after %d calls; want the bound %d", len(rec.Entries), calls, MaxAuditEntries)
	}
}

// TestAnActThisDoorCannotAuditIsNotAnswered is credsvc's D143 held at this door:
// a call whose audit line could not be written is not answered with the tool's
// answer, and the fault is not silent. The handler below is one a level filter
// drops every Info line from, which is the shape of a misconfigured log: slog's
// own LogAttrs would report nothing at all and the call would look audited
// (internal/credsvc/audit.go's line).
func TestAnActThisDoorCannotAuditIsNotAnswered(t *testing.T) {
	var audit bytes.Buffer
	// This handler drops every line the writer writes, which is the shape of a
	// misconfigured log level (and of a handler that fails): the door has to
	// report its own fault rather than answer with the tool's answer.
	quiet, err := NewAuditor(slog.NewTextHandler(&audit, &slog.HandlerOptions{Level: slog.LevelError}))
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	ts := newResourceServer(t, WithAuditor(quiet))
	ts.serve(t, "get_task_status", func(context.Context, Call) (any, error) { return "run-7 is implementing", nil })
	_, tokens := ts.runFlow(t, testClientRedirect)

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task_status","arguments":{}}}`
	rec := wantFailure(t, ts.rpc(t, tokens.AccessToken, body), 200, codeInternal)
	if msg := rec.Error.Message; !strings.Contains(msg, "audit") {
		t.Errorf("a call this door could not audit is refused with %q; want the door's own fault named, so that a misconfigured log is not read as a working one", msg)
	}
	if len(auditLines(t, &audit)) != 0 {
		t.Errorf("a handler that does not record wrote %q", audit.String())
	}
}

// TestTheWriterRefusesAnEventItCannotRecord is the writer's own rule: the
// fields are a closed set, so an event outside it is refused and the refusal
// names the field and never what it held (the no-echo rule).
func TestTheWriterRefusesAnEventItCannotRecord(t *testing.T) {
	var audit bytes.Buffer
	a, err := NewAuditor(slog.NewTextHandler(&audit, nil))
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	if _, err := NewAuditor(nil); err == nil {
		t.Error("NewAuditor(nil) was accepted: an act this door could not audit must not be made (D143)")
	}

	const marker = "marker-value-a-line-must-never-carry"
	for _, tc := range []struct {
		what string
		ev   AuditEvent
	}{
		{"an outcome nothing writes", AuditEvent{Outcome: "succeeded", Reason: ReasonNoCredential}},
		{"a reason nothing writes", AuditEvent{Outcome: OutcomeRefused, Reason: marker}},
		{"a refusal with no reason", AuditEvent{Outcome: OutcomeRefused}},
		{"an identity that is not one person", AuditEvent{Tool: "get_task_status", Identity: marker, Outcome: OutcomeOK, Scopes: []string{ScopeTasksRead}}},
		{"a tool the catalogue does not hold", AuditEvent{Tool: marker, Outcome: OutcomeOK, Scopes: []string{ScopeTasksRead}}},
		{"a scope this build does not know", AuditEvent{Tool: "get_task_status", Outcome: OutcomeOK, Scopes: []string{marker}}},
		{"a call with no tool", AuditEvent{Outcome: OutcomeOK, Scopes: []string{ScopeTasksRead}}},
	} {
		if err := a.Write(context.Background(), tc.ev); err == nil {
			t.Errorf("%s was recorded; the fields are a closed set", tc.what)
			continue
		}
		for _, line := range auditLines(t, &audit) {
			if strings.Contains(line, marker) {
				t.Errorf("%s left %q: a refusal names the field and never what it held", tc.what, line)
			}
		}
	}
}

// TestTheAuditLineCarriesTheFieldsOfACall is the other half of the line's
// shape: what a call that reached a tool leaves — the identity, the tool, the
// scopes and the outcome — so that an operator reading the log sees what was
// called and not only that something was.
func TestTheAuditLineCarriesTheFieldsOfACall(t *testing.T) {
	var audit bytes.Buffer
	ts := newResourceServer(t, auditTo(t, &audit))
	ts.serve(t, "get_task_status", func(context.Context, Call) (any, error) { return "run-7 is implementing", nil })
	_, tokens := ts.runFlow(t, testClientRedirect)

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task_status","arguments":{}}}`
	wantResult(t, ts.rpc(t, tokens.AccessToken, body))

	lines := auditLines(t, &audit)
	if len(lines) != 1 {
		t.Fatalf("a call wrote %d audit lines; want exactly one:\n%s", len(lines), audit.String())
	}
	line := lines[0]
	for _, want := range []string{
		"identity=" + testIdentity,
		"tool=get_task_status",
		"scopes=" + ScopeTasksRead + "," + ScopeTasksWrite,
		"outcome=" + OutcomeOK,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the line %q does not carry %q: a call leaves the identity, the tool, the scopes and the outcome", line, want)
		}
	}
	if !strings.Contains(line, "reason=") {
		t.Errorf("the line %q carries no reason field: the line's shape is the contract's, so a reader does not have to tell \"not known\" from \"not there\"", line)
	}
}

// TestAToolRefusalIsAuditedOnce pins the other outcome a call can have: a tool
// that refuses is the door's answer with `isError`, and the answer and the line
// are one event — two lines for one call would be an audit an operator counts
// twice.
func TestAToolRefusalIsAuditedOnce(t *testing.T) {
	var audit bytes.Buffer
	ts := newResourceServer(t, auditTo(t, &audit))
	ts.serve(t, "get_task_status", func(context.Context, Call) (any, error) {
		return nil, errors.New("this run is not one this tool serves")
	})
	_, tokens := ts.runFlow(t, testClientRedirect)

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task_status","arguments":{}}}`
	ans := wantResult(t, ts.rpc(t, tokens.AccessToken, body))
	if _, isError := wantToolText(t, ans); !isError {
		t.Error("a tool that refused was answered as a success")
	}

	lines := auditLines(t, &audit)
	if len(lines) != 1 {
		t.Fatalf("a refused call wrote %d audit lines; want exactly one:\n%s", len(lines), audit.String())
	}
	if !strings.Contains(lines[0], "outcome="+OutcomeRefused) || !strings.Contains(lines[0], "reason="+ReasonToolRefused) {
		t.Errorf("the refusal was audited as %q; want outcome %s and reason %s", lines[0], OutcomeRefused, ReasonToolRefused)
	}
}

// TestAScopeRefusalIsAuditedAsACall is the same rule for the door's own 403: a
// token that does not hold a tool's scope is refused before the tool runs, and
// the line names the tool and the scope it lacked — the two facts that tell an
// operator which token was minted too narrowly.
func TestAScopeRefusalIsAuditedAsACall(t *testing.T) {
	var audit bytes.Buffer
	ts := newResourceServer(t, auditTo(t, &audit))
	ts.serve(t, "get_task_status", func(context.Context, Call) (any, error) { return "run-7 is implementing", nil })
	_, tokens := ts.runFlowScoped(t, testClientRedirect, ScopeTasksWrite)

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task_status","arguments":{}}}`
	wantBearerRefusal(t, ts.rpc(t, tokens.AccessToken, body), 403, "insufficient_scope")

	lines := auditLines(t, &audit)
	if len(lines) != 1 {
		t.Fatalf("a scope refusal wrote %d audit lines; want exactly one:\n%s", len(lines), audit.String())
	}
	line := lines[0]
	for _, want := range []string{"tool=get_task_status", "scopes=" + ScopeTasksWrite, "outcome=" + OutcomeRefused, "reason=" + ReasonScopeNotHeld} {
		if !strings.Contains(line, want) {
			t.Errorf("the line %q does not carry %q", line, want)
		}
	}
}

// TestTheAuditLineCarriesEveryFieldUnderItsOwnName holds the five fields to
// their names: an operator greps the line and a log viewer indexes it, so a
// field renamed by accident is a query that stops finding anything. It is
// checked through a JSON handler, which is what a Cloud Logging sink reads.
func TestTheAuditLineCarriesEveryFieldUnderItsOwnName(t *testing.T) {
	var audit bytes.Buffer
	a, err := NewAuditor(slog.NewJSONHandler(&audit, nil))
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	ev := AuditEvent{Identity: testIdentity, Tool: "list_tasks", Scopes: []string{ScopeTasksRead}, Outcome: OutcomeOK}
	if err := a.Write(context.Background(), ev); err != nil {
		t.Fatalf("writing an event of the closed set: %v", err)
	}
	lines := auditLines(t, &audit)
	if len(lines) != 1 {
		t.Fatalf("one event wrote %d lines", len(lines))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("the line is not the JSON document an operator reads: %v (%q)", err, lines[0])
	}
	if got := decoded["msg"]; got != auditMessage {
		t.Errorf("the line's message is %v; want %q, which is what a reader greps", got, auditMessage)
	}
	for _, field := range []string{"identity", "tool", "scopes", "outcome", "reason"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("the line %q has no %q field: the line's shape is the contract's", lines[0], field)
		}
	}
	if got := decoded["identity"]; got != testIdentity {
		t.Errorf("the line's identity is %v; want %q", got, testIdentity)
	}
}
