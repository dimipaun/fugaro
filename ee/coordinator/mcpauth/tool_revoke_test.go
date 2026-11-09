// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// This file is M10's revocation test: the one delete D930 makes revocation,
// asked for from the person's own surface. The store's own revocation — that it
// is one delete, that it is idempotent, and that a rotation loses no race with
// it — is M2's (store_test.go), and what is tested here is the surface: that a
// family named by the audit record can be ended through a tool, and that the
// next call with a token of that family is refused.

// TestALauncherCannotRevokeAFamily is the other half of D930's surface, and the
// tool's own role check is the whole control on it: the catalogue maps
// revoke_family to tasks:write, which a launcher's token holds, and the registry
// and the door check that scope and nothing else — so what stands between a
// launcher and any family it can name is the check inside the handler. The
// family is left alive and the operator's own token keeps working, which is the
// difference between a refusal and a revocation.
func TestALauncherCannotRevokeAFamily(t *testing.T) {
	ts := newResourceServer(t, WithRoster(rosterOf(Members{
		Launchers: []string{auditLauncher},
		Operators: []string{testIdentity},
	})))
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	for _, tool := range []Tool{AuditRecordTool(ts.store), RevokeFamilyTool(ts.store)} {
		if err := ts.tools.Register(tool); err != nil {
			t.Fatalf("registering %s: %v", tool.Name, err)
		}
	}

	_, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))

	rec, _ := auditRecordOf(t, ts, tokens.AccessToken, testIdentity)
	if len(rec.Entries) == 0 || rec.Entries[0].FamilyID == "" {
		t.Fatalf("the operator's record names no family: %+v", rec.Entries)
	}
	familyID := rec.Entries[0].FamilyID

	// The launcher is admitted to the task tools, so its token holds both
	// scopes and the door's own check lets the call through: what refuses it
	// below is the tool's rule and nothing before it.
	launcher := ts.seededToken(t, auditLauncher)
	ans := wantResult(t, ts.rpc(t, launcher, auditCallBody(t, "revoke_family", `{"family_id":`+quoted(t, familyID)+`}`)))
	refusal, isError := wantToolText(t, ans)
	if !isError {
		t.Fatalf("a launcher revoked the family %s and the tool answered %s: the role check in the handler is the only control on that delete", familyID, refusal)
	}
	if !strings.Contains(refusal, "operator") {
		t.Errorf("the launcher's refusal reads %q; want it to say whose tool this is, because a launcher that cannot tell the rule from a fault keeps asking", refusal)
	}
	if _, held := ts.store.objects(t)[familyPrefix+familyID]; !held {
		t.Errorf("the family %s is gone from the store after a launcher asked for it to be revoked: a refusal that deleted anyway would leave every record naming a family nothing holds", familyID)
	}
	wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))
}

// TestAFamilyIsNotRevokedByACallTheDoorCannotAudit is D930's delete against
// D143's rule, and it is why the sink is asked before a tool runs: the line
// names the outcome, so a door that only checked after the tool had run would
// delete a family and then report that it could not record having done it. The
// handler below records nothing, which is the shape of a misconfigured log
// level, and the family has to still be there afterwards.
func TestAFamilyIsNotRevokedByACallTheDoorCannotAudit(t *testing.T) {
	var audit bytes.Buffer
	quiet, err := NewAuditor(slog.NewTextHandler(&audit, &slog.HandlerOptions{Level: slog.LevelError}))
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	ts := newResourceServer(t, WithAuditor(quiet), WithRoster(rosterOf(Members{
		Launchers: []string{testIdentity},
		Operators: []string{testIdentity},
	})))
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	if err := ts.tools.Register(RevokeFamilyTool(ts.store)); err != nil {
		t.Fatalf("registering the revocation tool: %v", err)
	}
	_, tokens := ts.runFlow(t, testClientRedirect)

	// The family comes out of the store rather than out of the audit tool,
	// because every call at this door is refused while the sink records
	// nothing — which is the state under test.
	token, err := ts.store.Store.LookupByHash(context.Background(), TokenHash(tokens.AccessToken))
	if err != nil {
		t.Fatalf("reading the token the flow minted: %v", err)
	}
	if token.FamilyID == "" {
		t.Fatal("the token names no family: there is nothing for the revocation to end")
	}

	body := auditCallBody(t, "revoke_family", `{"family_id":`+quoted(t, token.FamilyID)+`}`)
	ans := wantFailure(t, ts.rpc(t, tokens.AccessToken, body), http.StatusOK, codeInternal)
	if msg := ans.Error.Message; !strings.Contains(msg, "audit") {
		t.Errorf("a call this door could not audit is refused with %q; want the door's own fault named", msg)
	}
	if _, held := ts.store.objects(t)[familyPrefix+token.FamilyID]; !held {
		t.Errorf("the family %s is gone after a call this door could not record: the delete ran before the door knew it could write the line, and an act this door cannot audit is not made (D143)", token.FamilyID)
	}
	if len(auditLines(t, &audit)) != 0 {
		t.Errorf("a handler that records nothing wrote %q", audit.String())
	}
}

// downStore is the store of D935 not answering the two M10 tools: a revocation
// that cannot be written and a record that cannot be read. It is
// unreadableStore's shape for the half of the door that runs inside a tool,
// where the fault is the installation's and nothing the caller did would have
// changed it.
type downStore struct{ Store }

var errStoreDown = errors.New("the installation's store is not answering")

func (downStore) RevokeFamily(context.Context, string) error { return errStoreDown }

func (downStore) GetAudit(context.Context, string) (AuditRecord, error) {
	return AuditRecord{}, errStoreDown
}

// TestAToolWhoseStoreCouldNotBeAnsweredIsAuditedAsFailed is the same
// distinction authorize's 500s make, at the tools M10 adds: a store that does
// not answer is a fault of the installation, so the line says failed and not
// refused — an operator's failed revocation must not read as a denial — and the
// answer says the door's own sentence rather than repeating the store's error,
// which names objects of this installation and tells the caller nothing.
func TestAToolWhoseStoreCouldNotBeAnsweredIsAuditedAsFailed(t *testing.T) {
	for _, tc := range []struct{ tool, args string }{
		{"revoke_family", `{"family_id":"family-the-record-named"}`},
		{"get_audit_record", `{"identity":` + quoted(t, testIdentity) + `}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var audit bytes.Buffer
			ts := newResourceServer(t, auditTo(t, &audit), WithRoster(rosterOf(Members{
				Launchers: []string{testIdentity},
				Operators: []string{testIdentity},
			})))
			// The tools are handed the failing store rather than the door's
			// own: the door's lookup has to keep answering, or the call would
			// be refused before any tool ran and there would be nothing to
			// record.
			down := downStore{Store: ts.store}
			for _, tool := range []Tool{AuditRecordTool(down), RevokeFamilyTool(down)} {
				if err := ts.tools.Register(tool); err != nil {
					t.Fatalf("registering %s: %v", tool.Name, err)
				}
			}
			_, tokens := ts.runFlow(t, testClientRedirect)

			ans := wantFailure(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, tc.tool, tc.args)), http.StatusOK, codeInternal)
			if strings.Contains(ans.Error.Message, errStoreDown.Error()) {
				t.Errorf("the answer repeats the store's own error: %s", ans.Error.Message)
			}
			lines := auditLines(t, &audit)
			if len(lines) != 1 {
				t.Fatalf("%s wrote %d lines; want exactly one:\n%s", tc.tool, len(lines), audit.String())
			}
			for _, want := range []string{"outcome=" + OutcomeFailed, "reason=" + ReasonUnreadable} {
				if !strings.Contains(lines[0], want) {
					t.Errorf("the line %q does not carry %q: a revocation the installation could not make is not one this door refused", lines[0], want)
				}
			}
		})
	}
}

// TestAMalformedArgumentIsTheCallersRefusalAndNotTheStoresFault is the row
// above read from the other side, and it is why each tool checks its own
// argument before it hands the argument to the store: the store answers a key
// it cannot hold with the same error it answers a bucket it could not reach
// with, so a family id carrying a path separator, or an identity that is not
// one person, would be answered and durably recorded as a fault of the
// installation — an operator sent to the store by a caller's typo. A refused
// argument is the tool's own answer (isError inside a result), not the door's
// codeInternal, and its line says refused.
func TestAMalformedArgumentIsTheCallersRefusalAndNotTheStoresFault(t *testing.T) {
	for _, tc := range []struct{ tool, args string }{
		{"revoke_family", `{"family_id":"family/other"}`},
		{"get_audit_record", `{"identity":"not-a-user-entry@example.com"}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var audit bytes.Buffer
			ts := newResourceServer(t, auditTo(t, &audit), WithRoster(rosterOf(Members{
				Launchers: []string{testIdentity},
				Operators: []string{testIdentity},
			})))
			for _, tool := range []Tool{AuditRecordTool(ts.store), RevokeFamilyTool(ts.store)} {
				if err := ts.tools.Register(tool); err != nil {
					t.Fatalf("registering %s: %v", tool.Name, err)
				}
			}
			_, tokens := ts.runFlow(t, testClientRedirect)

			ans := wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, tc.tool, tc.args)))
			text, isError := wantToolText(t, ans)
			if !isError {
				t.Fatalf("%s answered an argument it cannot serve with %s; want the tool's own refusal, because the caller is the one that can fix it", tc.tool, text)
			}
			lines := auditLines(t, &audit)
			if len(lines) != 1 {
				t.Fatalf("%s wrote %d lines; want exactly one:\n%s", tc.tool, len(lines), audit.String())
			}
			for _, want := range []string{"outcome=" + OutcomeRefused, "reason=" + ReasonToolRefused} {
				if !strings.Contains(lines[0], want) {
					t.Errorf("the line %q does not carry %q: an argument this door would not take is not a fault of the installation, and a monitor counting installation faults must not count it", lines[0], want)
				}
			}
		})
	}
}

// TestRevokedFamilyIsRefusedOnTheNextCall is the whole of D930's answer read
// from the front door: an operator reads a family in a person's record, revokes
// it, and the access token that family minted stops working at the next call —
// and so does its refresh token, which is what makes the revocation one delete
// rather than a list of values to withdraw.
func TestRevokedFamilyIsRefusedOnTheNextCall(t *testing.T) {
	ts := newResourceServer(t, WithRoster(rosterOf(Members{
		Launchers: []string{testIdentity},
		Operators: []string{testIdentity},
	})))
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	if err := ts.tools.Register(AuditRecordTool(ts.store)); err != nil {
		t.Fatalf("registering the audit tool: %v", err)
	}
	if err := ts.tools.Register(RevokeFamilyTool(ts.store)); err != nil {
		t.Fatalf("registering the revocation tool: %v", err)
	}

	clientID, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))

	// The family comes out of the record, which is the path the design gives a
	// person: a call that was not theirs is visible in their record beside the
	// family that made it, and that family is what gets revoked.
	rec, _ := auditRecordOf(t, ts, tokens.AccessToken, testIdentity)
	if len(rec.Entries) == 0 {
		t.Fatal("the record of the call just made is empty: there is no family to revoke")
	}
	familyID := rec.Entries[0].FamilyID
	if familyID == "" {
		t.Fatal("the record names no family: revocation has nothing to end and a person has nothing to act on")
	}

	ans := wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "revoke_family", `{"family_id":`+quoted(t, familyID)+`}`)))
	text, isError := wantToolText(t, ans)
	if isError {
		t.Fatalf("revoking the family of one's own token failed: %s", text)
	}
	var answer struct {
		FamilyID string `json:"family_id"`
		Revoked  bool   `json:"revoked"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("the revocation's answer is not the shape it declares: %v (%s)", err, text)
	}
	if answer.FamilyID != familyID || !answer.Revoked {
		t.Errorf("the revocation answered %+v; want the family it was asked to end and that it is revoked", answer)
	}
	if strings.Contains(text, tokens.AccessToken) || strings.Contains(text, tokens.RefreshToken) {
		t.Errorf("the revocation's answer carries a token: %s", text)
	}

	// One delete, and what it means: every token of the family resolves to
	// nothing. The family object is gone and the tokens of it are refused at
	// the door, which is the same claim read at the two places it is true.
	if _, held := ts.store.objects(t)[familyPrefix+familyID]; held {
		t.Errorf("the family %s is still in the store after it was revoked: D930's revocation is one delete of the family", familyID)
	}
	wantBearerRefusal(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")), http.StatusUnauthorized, "invalid_token")

	// The refresh token of the same family is refused too, and it is refused
	// as a token that was never minted: the family is what both of them were
	// bounded by.
	rec2, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	})
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("the refresh grant answered %d (%s); want the revoked family's refresh token refused as one that was never minted", rec2.Code, strings.TrimSpace(rec2.Body.String()))
	}
	if body := rec2.Body.String(); strings.Contains(body, tokens.RefreshToken) {
		t.Errorf("the refusal of a refresh token carries the token: %s", body)
	}
}
