package identitywire_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// body is one body of the contract: what it is, a value of its type, and one
// valid encoding of that value.
type body struct {
	name  string
	zero  any
	valid string
}

// request is the same, for the bodies a caller sends: the ones a field the
// caller chose must never reach (TestIdentityWireHasNoCallerChosenIdentity).
type request struct {
	name  string
	zero  any
	valid string
}

// bodies is every body of the contract. It is a list of its own rather than
// three, because the three properties pinned below are properties of the whole
// wire: a type added to the contract and left out of this list is a type whose
// decoding nobody checked.
func bodies() []body {
	return []body{
		{"TicketRequest", identitywire.TicketRequest{}, `{"slug":"acme-sandbox","run":"fb-20261120-041919-8271","account":"fugaro-job@acme.iam.gserviceaccount.com","launcher":"dimi@example.com","branch":"fugaro/fb-20261120-041919-8271","pull_requests":[7],"context_repos":["acme/other"],"max_runtime_seconds":3600,"attempt":"wi-1:2","resumes":"fb-20261119-041919-0000","job":"projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271"}`},
		{"TicketResponse", identitywire.TicketResponse{}, `{"ticket":"t-1","expires_at":"2026-11-20T01:00:00Z"}`},
		{"ClaimRequest", identitywire.ClaimRequest{}, `{"ticket":"t-1","nonce":"n-1","attestation":"h.p.s"}`},
		{"ClaimResponse", identitywire.ClaimResponse{}, `{"token":"tok-1","expires_at":"2026-11-20T01:00:00Z"}`},
		{"PairingRequest", identitywire.PairingRequest{}, `{}`},
		{"PairingResponse", identitywire.PairingResponse{}, `{"token":"p-1","pairing":"p~abc","expires_at":"2026-11-20T00:05:00Z"}`},
		{"DeviceRequest", identitywire.DeviceRequest{}, `{}`},
		{"DeviceResponse", identitywire.DeviceResponse{}, `{"token":"d-1","device":"d~abc","expires_at":"2026-11-20T01:00:00Z"}`},
		{"ProbeRequest", identitywire.ProbeRequest{}, `{}`},
		{"ProbeResponse", identitywire.ProbeResponse{}, `{"signer":"fugaro-signer@acme.iam.gserviceaccount.com","signed_at":"2026-11-20T00:00:00Z"}`},
		// The registration and its answer (C1 of docs/batch-run-identity): the
		// run the record belongs to, the job the ticket ask named, and the id
		// the platform gave that job.
		{"JobRequest", identitywire.JobRequest{}, `{"slug":"acme-sandbox","run":"fb-20261120-041919-8271","job":"projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271","job_id":"job-8271"}`},
		{"JobResponse", identitywire.JobResponse{}, `{}`},
		// The record's claims are the signer's own type (token.Claims), which
		// carries no json tags of its own: encoding/json spells its fields by
		// their Go names, and the wire names a token ends up carrying are built
		// from it at the signing. The record is read back by this same type and
		// by no reader outside the service.
		{"Record", identitywire.Record{}, `{"caller":"dimi@example.com","class":"person","at":"2026-11-20T00:00:00Z","claims":{"Slug":"acme-sandbox","Run":"fb-20261120-041919-8271","FX":"2026-11-20T01:00:00Z","FP":"acme","RB":"dimi@example.com"},"expected_account":"fugaro-job@acme.iam.gserviceaccount.com","ticket_hash":"aa11","claimed":{"account":"fugaro-job@acme.iam.gserviceaccount.com","at":"2026-11-20T00:00:00Z","nonce_hash":"bb22"},"attempt":"wi-1:2","job":"projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271","job_id":"job-8271"}`},
	}
}

// withField is body with one more field of the caller's choosing: `{}` grows a
// field, and anything else is cut before its closing brace and given one.
func withField(body, field string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(body), "}")
	if trimmed == "{" {
		return "{" + field + "}"
	}
	return trimmed + "," + field + "}"
}

// TestIdentityWireDecodesStrictly is the contract's decoding rule: every body
// of the wire reads a valid encoding, refuses one with a field its type does not
// have, and refuses bytes after the value. A wire that decoded leniently would
// be a wire on which a caller can name something — a uid, a claim set — that
// the service then ignores and the two sides disagree about forever.
//
// Mutation (run, restore): drop DisallowUnknownFields (or the trailing-data
// check) from DecodeStrict; the "name a field of its own" and "trailing" rows
// fail for every body.
func TestIdentityWireDecodesStrictly(t *testing.T) {
	for _, b := range bodies() {
		t.Run(b.name, func(t *testing.T) {
			into := reflect.New(reflect.TypeOf(b.zero)).Interface()
			if err := identitywire.DecodeStrict([]byte(b.valid), into); err != nil {
				t.Fatalf("the contract's own encoding of %s does not decode: %v; the value is\n%s", b.name, err, b.valid)
			}
			// The value read back is what the encoder was given, so a body the
			// two sides agree on cannot decode into a zero value here. The
			// three asks whose type has no field at all (the pairing, the
			// device and the probe) are sent and read as the empty object,
			// where the zero value is the whole of the answer.
			if b.valid != "{}" && reflect.DeepEqual(reflect.ValueOf(into).Elem().Interface(), b.zero) {
				t.Errorf("%s decoded to its zero value from\n%s\nthe encoding above is not one this type reads", b.name, b.valid)
			}

			named := withField(b.valid, `"a_field_no_type_here_has":1`)
			if err := identitywire.DecodeStrict([]byte(named), reflect.New(reflect.TypeOf(b.zero)).Interface()); err == nil {
				t.Errorf("%s decoded a body naming a field its type does not have:\n%s\nthe wire decodes strictly, and a field nobody reads is one the two sides disagree about", b.name, named)
			}

			trailing := b.valid + `{"more":1}`
			if err := identitywire.DecodeStrict([]byte(trailing), reflect.New(reflect.TypeOf(b.zero)).Interface()); err == nil {
				t.Errorf("%s decoded a body with a second value after it:\n%s", b.name, trailing)
			}
		})
	}
}

// TestIdentityWireHasNoCallerChosenIdentity is the contract's other half: no
// field of any request lets a caller choose the uid the service signs or the
// claim set it signs, and asking for one is a refusal by name. The uid comes
// from the record's path and the claims from the record (R3), so a caller that
// could name either would be a caller signing as a run it named.
//
// It is asked twice over, because either answer alone is one a later edit can
// undo without the other noticing: the request types are walked for a field
// spelled uid or claims, and the decoder is asked a body that names one.
//
// Mutation (run, restore): add a UID field tagged json:"uid" to TicketRequest —
// the field walk fails; drop DisallowUnknownFields — the body walk fails.
func TestIdentityWireHasNoCallerChosenIdentity(t *testing.T) {
	forbidden := map[string]bool{"uid": true, "claims": true}

	for _, r := range []request{
		{"TicketRequest", identitywire.TicketRequest{}, `{"slug":"acme-sandbox","run":"fb-20261120-041919-8271","account":"fugaro-job@acme.iam.gserviceaccount.com","launcher":"dimi@example.com","max_runtime_seconds":3600}`},
		{"ClaimRequest", identitywire.ClaimRequest{}, `{"ticket":"t-1","nonce":"n-1"}`},
		{"PairingRequest", identitywire.PairingRequest{}, `{}`},
		{"DeviceRequest", identitywire.DeviceRequest{}, `{}`},
		{"ProbeRequest", identitywire.ProbeRequest{}, `{}`},
		{"JobRequest", identitywire.JobRequest{}, `{"slug":"acme-sandbox","run":"fb-20261120-041919-8271","job":"projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271","job_id":"job-8271"}`},
	} {
		typ := reflect.TypeOf(r.zero)
		for i := range typ.NumField() {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if forbidden[name] || forbidden[strings.ToLower(f.Name)] {
				t.Errorf("%s has the field %s (%q): a request of this contract names no uid and no claim set, because the uid is the record's path and the claims are the record's (R3)", r.name, f.Name, name)
			}
		}

		for _, field := range []string{`"uid":"r~acme-sandbox~fb-20261120-041919-8271"`, `"claims":{"fs":"acme-sandbox"}`} {
			named := withField(r.valid, field)
			err := identitywire.DecodeStrict([]byte(named), reflect.New(typ).Interface())
			if err == nil {
				t.Errorf("%s decoded a body naming %s:\n%s\nthe wire has no field for it, and a caller that names one is refused rather than served under a value it chose", r.name, field, named)
			}
		}
	}
}

// TestIdentityWireRoutesAdmitTheRosterClasses is the route table: the paths,
// the classes each admits, the probe's own rule that it is the roster's
// operators and nobody else, and the pieces of the contract the table is read
// with — the execution variable the ticket travels in, the place the issuance
// record lives, the digest a secret is kept as, and the attempt reference's
// spelling.
//
// The count is six and not R2's five: this plan's registration (JobPath, C1 of
// docs/batch-run-identity) is a route of the wire, so RouteFor names it and the
// table carries it — the mount that admits the path at the service's door is
// BR17's, and its own test is what holds that.
func TestIdentityWireRoutesAdmitTheRosterClasses(t *testing.T) {
	routes := identitywire.Routes()
	if len(routes) != 6 {
		t.Fatalf("the contract declares %d routes, want R2's five paths and this plan's registration", len(routes))
	}
	want := map[string][]identitywire.CallerClass{
		identitywire.TicketPath:  {identitywire.ClassPerson, identitywire.ClassServiceAccount, identitywire.ClassOrchestrator},
		identitywire.ClaimPath:   {identitywire.ClassJobAccount},
		identitywire.PairingPath: {identitywire.ClassPerson},
		identitywire.DevicePath:  {identitywire.ClassPerson},
		identitywire.ProbePath:   {identitywire.ClassPerson},
		identitywire.JobPath:     {identitywire.ClassPerson, identitywire.ClassServiceAccount, identitywire.ClassOrchestrator},
	}
	operatorsOnly := 0
	for _, rt := range routes {
		if !strings.HasPrefix(rt.Path, "/") {
			t.Errorf("the route %q is not an absolute path", rt.Path)
		}
		admits, ok := want[rt.Path]
		if !ok {
			t.Errorf("the contract declares a route %q that R2 does not name", rt.Path)
			continue
		}
		if !reflect.DeepEqual(rt.Admits, admits) {
			t.Errorf("%s admits %v, want %v", rt.Path, rt.Admits, admits)
		}
		for _, class := range rt.Admits {
			switch class {
			case identitywire.ClassPerson, identitywire.ClassServiceAccount, identitywire.ClassOrchestrator, identitywire.ClassJobAccount:
			default:
				t.Errorf("%s admits the class %q, which is no class of this contract", rt.Path, class)
			}
		}
		if rt.OperatorsOnly {
			operatorsOnly++
			if rt.Path != identitywire.ProbePath {
				t.Errorf("%s admits the roster's operators alone, and only the probe may (R2)", rt.Path)
			}
		}
		if got, found := identitywire.RouteFor(rt.Path); !found || got.Path != rt.Path {
			t.Errorf("RouteFor(%q) answered %q/%v", rt.Path, got.Path, found)
		}
	}
	if operatorsOnly != 1 {
		t.Errorf("%d routes admit the roster's operators alone, want the probe's one", operatorsOnly)
	}
	if _, found := identitywire.RouteFor("/v1/identity"); found {
		t.Error("RouteFor answered a path that is no route of the contract")
	}
	if _, found := identitywire.RouteFor(identitywire.TicketPath + "/"); found {
		t.Error("RouteFor answered a path with a trailing slash: the contract's paths are exact, and a near miss is a route this service does not serve")
	}

	if identitywire.TicketEnv != "FUGARO_IDENTITY_TICKET" {
		t.Errorf("TicketEnv is %q, want the new name R2 gives the ticket's execution variable (runner.BudgetTokenEnv keeps meaning the seal key until the release after this one)", identitywire.TicketEnv)
	}
	if identitywire.ProbePayload == "" || identitywire.ProbePayload == identitywire.TicketEnv {
		t.Errorf("ProbePayload is %q, want the fixed payload the probe signs and discards (R8)", identitywire.ProbePayload)
	}

	// The record's place: under the database's creds subtree, one node per slug
	// and run, with the names escaped as every other key of this installation
	// is — a slug with a dot or a slash is one node and not two.
	if got := identitywire.PathRecord("acme-sandbox", "fb-20261120-041919-8271"); got != "creds/issuance/acme-sandbox/fb-20261120-041919-8271" {
		t.Errorf("PathRecord answered %q, want the record under the closed creds subtree (R3)", got)
	}
	if got := identitywire.PathRecord("acme/sandbox", "r.1"); got != "creds/issuance/acme%2Fsandbox/r%2E1" {
		t.Errorf("PathRecord answered %q for names that need escaping, want the escaped path (a slash in a name must not make a second node)", got)
	}
	for _, empty := range [][2]string{{"", "fb-1"}, {"acme", ""}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("PathRecord(%q, %q) answered a path: an empty name collapses the record's node onto another's", empty[0], empty[1])
				}
			}()
			_ = identitywire.PathRecord(empty[0], empty[1])
		}()
	}

	// A secret is kept as its digest, never in the clear (R3, R2).
	if got := identitywire.Digest("t-1"); got != identitywire.Digest("t-1") {
		t.Error("Digest answers two values for one secret")
	}
	if identitywire.Digest("t-1") == identitywire.Digest("t-2") {
		t.Error("Digest answers one value for two secrets")
	}
	if got := identitywire.Digest("t-1"); len(got) != 64 || strings.ToLower(got) != got {
		t.Errorf("Digest answered %q, want the SHA-256 in lower hex", got)
	}
	if strings.Contains(identitywire.Digest("t-1"), "t-1") {
		t.Error("Digest carries the secret it digested")
	}

	// The attempt reference, spelled as the orchestrator's own reader spells it
	// (D3136): an item, a colon and an attempt number.
	for _, tc := range []struct {
		ref  identitywire.AttemptID
		want bool
	}{
		{"wi-1:2", true},
		{"wi-1:1", true},
		{"", false},
		{"wi-1", false},
		{":2", false},
		{"wi-1:", false},
		{"wi-1:0", false},
		{"wi-1:1x", false},
		{"wi-1:1:2", false},
		{"wi-1:1234567890", false},
	} {
		if got := tc.ref.Valid(); got != tc.want {
			t.Errorf("AttemptID(%q).Valid() is %v, want %v", tc.ref, got, tc.want)
		}
	}
}
