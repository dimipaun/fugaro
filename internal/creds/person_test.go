package creds

import (
	"testing"
)

// The tests of C1 (internal/creds/person.go): the grant's two shapes are read strictly, and the
// names the person's half is spelled with are pinned where a change to one of them is a change to
// a wire format or to a published policy key.

// TestPersonGrantDecodesStrictly: the shapes of the workspace grant are the contract, so every way
// of being another shape is refused rather than read. The two that matter most are the ones a
// LATER key would be: a member's own grant body must not read as this request (R4, F13), and an
// answer carrying a claim secret must not read as a grant at all (F14).
func TestPersonGrantDecodesStrictly(t *testing.T) {
	const good = `{"slug":"acme-web-0123456789abcdef","run":"r-2741","account":"job@acme.iam.gserviceaccount.com","name":"claude-oauth-token"}`

	req, err := ParsePersonGrantRequest([]byte(good))
	if err != nil {
		t.Fatalf("ParsePersonGrantRequest of a request of this shape: %v", err)
	}
	if req.Slug != "acme-web-0123456789abcdef" || req.Run != "r-2741" || req.Account != "job@acme.iam.gserviceaccount.com" || req.Name != PersonTokenName {
		t.Errorf("the request read as %+v: every field of the shape arrives as it was written", req)
	}

	for _, tc := range []struct{ what, body string }{
		{"a member's own grant body, which names the credentials of a run instead of the one personal name", `{"slug":"s","run":"r","account":"a","credentials":["claude-oauth-token"],"max_runtime_seconds":3600}`},
		{"a key of a later release's shape", `{"slug":"s","run":"r","account":"a","name":"claude-oauth-token","max_runtime_seconds":3600}`},
		{"a field of the shape left out", `{"slug":"s","run":"r","name":"claude-oauth-token"}`},
		{"a field of the shape left empty", `{"slug":"s","run":"r","account":"a","name":""}`},
		{"a personal name this plan does not serve", `{"slug":"s","run":"r","account":"a","name":"anthropic-api-key"}`},
		{"a body that is not one object", `["slug"]`},
		{"a body that is not one object at all", `null`},
		{"a body that is a JSON string", `"slug"`},
		{"a body carrying a second JSON value", good + ` {}`},
		{"a body that is not JSON", `slug=acme`},
	} {
		if _, err := ParsePersonGrantRequest([]byte(tc.body)); err == nil {
			t.Errorf("ParsePersonGrantRequest read %s: %s", tc.what, tc.body)
		}
	}

	granted, err := ParsePersonGrant([]byte(`{"held":true,"slip":"00112233445566778899aabbccddeeff"}`))
	if err != nil {
		t.Fatalf("ParsePersonGrant of a granted answer: %v", err)
	}
	if !granted.Held || granted.Slip != "00112233445566778899aabbccddeeff" {
		t.Errorf("the granted answer read as %+v: a grant is the slip's id and nothing else", granted)
	}

	held, err := ParsePersonGrant([]byte(`{"held":false}`))
	if err != nil {
		t.Fatalf(`ParsePersonGrant of "no token held": %v: it is an ordinary answer, and the one a person who stored no token is given`, err)
	}
	if held.Held || held.Slip != "" {
		t.Errorf(`the "no token held" answer read as %+v: it is no slip, and a run served by it starts on the shared entries as before`, held)
	}

	// A body that spells neither field is the same answer as held:false — no slip, and the run starts
	// on the shared entries. It is the only zero value this contract reads, and it is pinned here so
	// that it stays a reading rather than becoming an accident.
	unnamed, err := ParsePersonGrant([]byte(`{}`))
	if err != nil {
		t.Fatalf("ParsePersonGrant of a body that spells neither field: %v", err)
	}
	if unnamed.Held || unnamed.Slip != "" {
		t.Errorf("the body that spells neither field read as %+v: it is no token held, never a grant", unnamed)
	}

	for _, tc := range []struct{ what, body string }{
		{"a grant carrying the claim secret, which the claim alone answers and no answer of this contract names", `{"held":true,"slip":"00112233445566778899aabbccddeeff","claim_secret":"0011223344"}`},
		{"an answer that holds a token and names no slip", `{"held":true}`},
		{"an answer that names a slip and holds no token", `{"held":false,"slip":"00112233445566778899aabbccddeeff"}`},
		{"an answer that names a slip with no held beside it", `{"slip":"00112233445566778899aabbccddeeff"}`},
		{"an answer that is not one object", `"held"`},
		// `null` decodes into a Go value of any shape without an error, so without the object
		// check this one would be read as the shape's zero — "no token held", the benign answer
		// — where the contract refuses a body that is not one JSON object.
		{"an answer that is not one object at all", `null`},
		{"an answer that is a JSON array", `[]`},
	} {
		if _, err := ParsePersonGrant([]byte(tc.body)); err == nil {
			t.Errorf("ParsePersonGrant read %s: %s", tc.what, tc.body)
		}
	}

	// The names the answer's own fields are handed on and presented with, and the one personal name
	// this plan serves (A1): the shape is meaningless without them, so the test that pins the shape
	// pins them. Each is a wire spelling two sides of this plan already spell from here, so a change
	// to one of them is a change to a job's environment or to a header both services read.
	for _, tc := range []struct{ what, got, want string }{
		{"PersonTokenName", PersonTokenName, "claude-oauth-token"},
		{"PersonSlipEnv", PersonSlipEnv, "FUGARO_WORKSPACE_SLIP"},
		{"PersonSlipHeader", PersonSlipHeader, "X-Fugaro-Workspace-Slip"},
		{"PersonSlipClaimHeader", PersonSlipClaimHeader, "X-Fugaro-Workspace-Slip-Claim"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.what, tc.got, tc.want)
		}
	}
}

// TestPersonSwitchKeyIsThePolicyKey pins the one name this package and the member's published
// policy have to spell the same: the switch is read out of the policy the member wrote, by
// internal/projectpolicy, which cannot import this package (creds does) and so carries the
// spelling of its own field. The two agree by this constant and by the conformance test that
// reads a published node with it (internal/cli/person_switch_conformance_test.go).
func TestPersonSwitchKeyIsThePolicyKey(t *testing.T) {
	if PersonSwitchKey != "personal_workspace_tokens" {
		t.Errorf("PersonSwitchKey = %q, want %q: it is the key of the member's published policy and of the member's own settings row, so a spelling that moved here would move both with nothing red (R9)", PersonSwitchKey, "personal_workspace_tokens")
	}
}
