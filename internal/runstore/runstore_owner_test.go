package runstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These two tests are F16 of docs/secrets-brokering/dynamic-delivery: the run
// record names the owner of the token its calls were served with. The record
// already had oauth_account (the Claude account the run used last) and
// stage_accounts (the account each stage used); the owner is a THIRD fact with
// a name of its own, credential_owner, because a field never changes meaning
// (CLAUDE.md): the owner is the verified `sub` the credentials service's grant
// was issued to, and reading it out of either account field would be reading a
// fact neither holds.

// anOwner is a launcher's account id: digits only, the width and alphabet the
// credential service holds a launcher's `sub` to (maxLauncherSubDigits, DS1.2).
// It is a fixture's own number and names no real account, and it is a value no
// credential could be: a token is secret-shaped and this is a plain integer.
const anOwner = "104329501182774832335"

// TestRecordNamesOwnerOfTokenUsed is F16's claim: a run whose calls were served
// with a credential records the owner of that credential beside the credential's
// own name, the pair survives the round trip, and an empty owner is omitted so a
// run that recorded none writes the record it always wrote.
func TestRecordNamesOwnerOfTokenUsed(t *testing.T) {
	s := newStore(t)
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		StartedAt: time.Now().UTC(),
		// The run's calls were served with the credential named by
		// OAuthAccount, and the owner the credential service's grant was
		// issued to is a fact of its own: the record carries both.
		OAuthAccount:    "repo-shared-b",
		CredentialOwner: anOwner,
	}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.CredentialOwner != anOwner {
		t.Errorf("credential_owner = %q, want %q: the record names the owner of the token the run used", got.CredentialOwner, anOwner)
	}
	if got.OAuthAccount != "repo-shared-b" {
		t.Errorf("oauth_account = %q, want the credential's name (repo-shared-b): the owner is a second fact and not this field's", got.OAuthAccount)
	}

	// The json name is credential_owner, and it sits beside the two fields the
	// record already had rather than in either of them.
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["credential_owner"] != anOwner {
		t.Errorf("the record's json carries credential_owner = %v, want %q: %s", doc["credential_owner"], anOwner, data)
	}
	if doc["oauth_account"] != "repo-shared-b" {
		t.Errorf("the record's json carries oauth_account = %v, want the credential's name", doc["oauth_account"])
	}

	// Empty is omitted, like oauth_account's and stage_accounts' own
	// convention: a run that recorded no owner writes the record it always
	// wrote.
	data, err = json.Marshal(&Record{Version: 1, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credential_owner") {
		t.Fatalf("an empty CredentialOwner encoded the field: %s", data)
	}
}

// TestExistingFieldsUnchanged is the compatibility claim the additive field
// rests on: credential_owner is a NEW name, and the two fields it sits beside
// keep the json names and the meanings a reader outside this binary reads them
// by (CLAUDE.md's rule that a field never changes meaning). The console, the
// mobile app and the history read these documents, so a rename or a retyping is
// a silent change of meaning for every one of them.
func TestExistingFieldsUnchanged(t *testing.T) {
	typ := reflect.TypeOf(Record{})
	for _, want := range []struct{ field, json string }{
		{"OAuthAccount", "oauth_account"},
		{"StageAccounts", "stage_accounts"},
	} {
		f, ok := typ.FieldByName(want.field)
		if !ok {
			t.Fatalf("runstore.Record has no %s field any more", want.field)
		}
		if got := strings.Split(f.Tag.Get("json"), ",")[0]; got != want.json {
			t.Errorf("Record.%s's json name is %q, want %q: the new field is additive and the old names do not move", want.field, got, want.json)
		}
	}

	// A record written before credential_owner existed decodes unchanged: the
	// two fields it has keep their values, the owner is absent, and the
	// format's version does not move.
	s := newStore(t)
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready",` +
		`"oauth_account":"team-a","stage_accounts":{"implement":"team-b","review":"team-a"},` +
		`"cost_usd":1,"started_at":"2026-09-26T22:15:30Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatalf("a record written before credential_owner existed: %v", err)
	}
	if got.CredentialOwner != "" {
		t.Errorf("credential_owner = %q on a record written before the field, want none", got.CredentialOwner)
	}
	if got.OAuthAccount != "team-a" {
		t.Errorf("oauth_account = %q, want team-a: the field keeps the meaning it had", got.OAuthAccount)
	}
	if !reflect.DeepEqual(got.StageAccounts, map[string]string{"implement": "team-b", "review": "team-a"}) {
		t.Errorf("stage_accounts = %+v, want the map the record was written with", got.StageAccounts)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1: the field is additive", got.Version)
	}
}
