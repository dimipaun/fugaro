// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"encoding/json"
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
