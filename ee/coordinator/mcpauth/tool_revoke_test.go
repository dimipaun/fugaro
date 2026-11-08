// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
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
