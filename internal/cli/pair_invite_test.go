package cli

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"rsc.io/qr"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// The invitation's fixture: the same project as the pairing's, with the two
// things an invitation needs and a pairing does not — an installation whose
// credentials service is known, so the mobile endpoint can be derived from it,
// and the node J6 publishes the mailbox consumer's public key at.
const (
	pairInviteCredentialsURL = "https://fugaro-credentials-abcde-uc.a.run.app"
	pairInviteEndpointURL    = "https://fugaro-mobile-abcde-uc.a.run.app"
)

// pairInviteEndpoint is the config edit that gives the fixture a credentials
// service. Without it there is no endpoint to derive and the invitation is
// refused, which is one of the cases below.
func pairInviteEndpoint(cfg *string, _ *pairFixture) {
	*cfg += "credentials_service:\n  url: " + pairInviteCredentialsURL + "\n"
}

// pairSeedConsumerKey seeds the node J6 publishes the consumer's public half at
// and answers the key and the fingerprint the invitation must carry. The bytes
// are a fixed public key and never a secret: an Ed25519 public key is any 32
// bytes, and nothing here signs with it.
func (f *pairFixture) pairSeedConsumerKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0x40 + i)
	}
	f.db.Set(pairConsumerKeyPath, map[string]any{"v": 1, "public_key": base64.RawURLEncoding.EncodeToString(key)})
	return key, pairConsumerKeyFingerprint(key)
}

// pairSeedDeviceFor writes one device node for an address, which is what the
// invitation's own D224 resolution reads: the allowlist is keyed by uid and the
// address and the role are its fields.
func (f *pairFixture) pairSeedDeviceFor(uid, name, email, role string) {
	f.db.Set(rules.PathDevices+"/"+uid, map[string]any{
		"name": name, "email": email, "role": role, "at": pairT0.UTC().Format(time.RFC3339),
	})
}

// pairAddressKey is the key an address is stored under, built the one way this
// package builds it (mobileauth.EncodeNodeKey), so a test reads the node the
// command wrote rather than one it guessed the spelling of.
func pairAddressKey(t *testing.T, email string) string {
	t.Helper()
	key, err := mobileauth.EncodeNodeKey(email)
	if err != nil {
		t.Fatalf("the address %q is not one this encoding accepts: %v", email, err)
	}
	return key
}

// pairRefusedCode runs the command and answers the one refusal it made, failing
// the test when the command did not refuse.
func pairRefusedCode(t *testing.T, f *pairFixture, args ...string) string {
	t.Helper()
	out, _, err := pairRun(t, append([]string{"--json"}, args...)...)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused {
		t.Fatalf("events = %v, want one refused event", events)
	}
	return events[0].Code
}

// TestPairInviteNeedsARole is the plan's red step: naming another person's
// email with no --role must refuse first. D226's default — the caller's own
// role — is a role for the caller's own device, and an invitation is a role for
// somebody else's, so nothing is defaulted for another person (D2799).
func TestPairInviteNeedsARole(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", pairLauncher); code != pairRefusedInviteRole {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteRole)
	}
	if v := f.db.Value(pairInviteRoot); v != nil {
		t.Fatalf("a refused invitation wrote an invite node: %v", v)
	}
	if v := f.db.Value(pairMemberRoot); v != nil {
		t.Fatalf("a refused invitation wrote a member record: %v", v)
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused invitation minted %d token(s)", n)
	}
}

// TestPairInviteNeedsAnAdmin: D2799 lets AN ADMIN name another member's email.
// An operator naming one is refused rather than invited, because the invitation
// writes a role for somebody else into the installation's own admin tree.
func TestPairInviteNeedsAnAdmin(t *testing.T) {
	f := newPairFixture(t, pairLauncher, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", pairMe, "--role", "viewer"); code != pairRefusedInviteAdmin {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteAdmin)
	}
	if v := f.db.Value(pairInviteRoot); v != nil {
		t.Fatalf("a refused invitation wrote an invite node: %v", v)
	}
}

// TestPairInviteNeedsARosterGrant: membership is the roster's (O14), so an
// address the roster does not name at project scope is refused rather than
// invited — the invitation would otherwise create a member record for somebody
// the installation never granted anything.
func TestPairInviteNeedsARosterGrant(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", "nobody@example.com", "--role", "viewer"); code != pairRefusedInviteMember {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteMember)
	}
	if v := f.db.Value(pairMemberRoot); v != nil {
		t.Fatalf("a refused invitation wrote a member record: %v", v)
	}
}

// TestPairInviteRoleAboveTheRosterRefused: the role is the roster's (O14), so
// --role may lower it and never raise it. An invitation carrying a role above
// the one the roster grants would put authority on a phone that no grant bounds.
func TestPairInviteRoleAboveTheRosterRefused(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", pairLauncher, "--role", "admin"); code != pairRefusedInviteAbove {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteAbove)
	}
}

// TestPairInviteKeepsD224Refusal: the invitation resolves its role through the
// same D224 resolution the pairing does, so an address whose allowlist entry
// already carries a role the invitation disagrees with is refused rather than
// written twice with two roles.
func TestPairInviteKeepsD224Refusal(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	f.pairSeedDeviceFor("m~AAAA", "phone-a", pairLauncher, "operator")
	if code := pairRefusedCode(t, f, "--email", pairLauncher, "--role", "viewer"); code != pairRefusedRoleDisagrees {
		t.Fatalf("code = %q, want %q", code, pairRefusedRoleDisagrees)
	}
}

// TestPairInviteRefusesADeviceName: --name names a device, and an invitation is
// not one — the phone's device node is written when it pairs (P3). The flag is
// kept and a value for it is refused rather than dropped.
func TestPairInviteRefusesADeviceName(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", pairLauncher, "--role", "operator", "--name", "phone-a"); code != pairRefusedInviteName {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteName)
	}
}

// TestPairInviteNeedsAnEndpoint: the QR carries the project's endpoint, and the
// endpoint is the credentials service's address with the mobile service's own
// label. An installation with no credentials service in its config has no
// address to read one out of, so it is refused before anything is written.
func TestPairInviteNeedsAnEndpoint(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedConsumerKey(t)
	if code := pairRefusedCode(t, f, "--email", pairLauncher, "--role", "operator"); code != pairRefusedInviteEndpoint {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteEndpoint)
	}
	if v := f.db.Value(pairInviteRoot); v != nil {
		t.Fatalf("a refused invitation wrote an invite node: %v", v)
	}
}

// TestPairInviteNeedsTheConsumerKey: the QR carries the fingerprint of the
// mailbox consumer's public key (O16), which `fugaro update` publishes (J6).
// The node absent is an installation whose consumer key is not deployed yet,
// and the invitation is refused rather than written without a fingerprint: a
// phone that pinned nothing would accept whatever key a compromised service
// returned.
func TestPairInviteNeedsTheConsumerKey(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	if code := pairRefusedCode(t, f, "--email", pairLauncher, "--role", "operator"); code != pairRefusedInviteKey {
		t.Fatalf("code = %q, want %q", code, pairRefusedInviteKey)
	}
	if v := f.db.Value(pairInviteRoot); v != nil {
		t.Fatalf("a refused invitation wrote an invite node: %v", v)
	}
}

// TestPairInviteWritesTheInviteAndTheMember is the invitation itself: one
// invite node under config/admin, the member record beside it, and a QR that
// carries the endpoint, the email, the nonce and the consumer key's fingerprint
// and no credential at all.
func TestPairInviteWritesTheInviteAndTheMember(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	key, fingerprint := f.pairSeedConsumerKey(t)
	pairFixedRand(t)

	out, errOut, err := pairRunSession(t, t.Context(), "", false, "--email", pairLauncher, "--role", "operator", "--json")
	if err != nil {
		t.Fatalf("an invitation: %v", err)
	}
	// An invitation asks nothing and waits for nothing. The one line stderr
	// carries is the project header every cloud command writes (the pairing's
	// stream carries the same line), and there is no question after it: this
	// ran with no terminal, so a command that wanted to ask would have
	// refused rather than read a line.
	stderr := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
	if len(stderr) != 1 || !strings.HasPrefix(stderr[0], "project: ") {
		t.Fatalf("an invitation wrote %q to stderr, want only the project header", errOut)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventInvited {
		t.Fatalf("events = %v, want one invited event", events)
	}
	invited := events[0]

	// The QR's payload: exactly the four values the invitation carries, and the
	// version the invitation's own code is (pairQRVersion), which is not the
	// bootstrap payload's.
	var payload map[string]any
	if err := json.Unmarshal([]byte(invited.Payload), &payload); err != nil {
		t.Fatalf("the invitation's QR payload is not JSON: %v", err)
	}
	want := []string{"consumer_key_fingerprint", "email", "endpoint", "nonce", "v"}
	got := make([]string, 0, len(payload))
	for k := range payload {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the QR payload carries %v, want %v", got, want)
	}
	if v, _ := payload["v"].(float64); int(v) != pairQRVersion {
		t.Fatalf("the QR payload's v = %v, want %d", payload["v"], pairQRVersion)
	}
	if payload["email"] != pairLauncher || payload["endpoint"] != pairInviteEndpointURL {
		t.Fatalf("the QR payload is %s, want the email and the derived endpoint", invited.Payload)
	}
	if payload["consumer_key_fingerprint"] != fingerprint {
		t.Fatalf("the QR payload's fingerprint = %v, want %q", payload["consumer_key_fingerprint"], fingerprint)
	}
	nonce, _ := payload["nonce"].(string)
	if nonce == "" {
		t.Fatalf("the QR payload carries no nonce: %s", invited.Payload)
	}
	for _, absent := range []string{`"token"`, `"api_key"`, `"database_url"`, `"gcp_project"`, `"key"`, `"role"`, pairAPIKey} {
		if strings.Contains(invited.Payload, absent) {
			t.Fatalf("the QR payload carries %q: %s", absent, invited.Payload)
		}
	}

	// The invite node: the invitation P3 reads, under a root only IAM can
	// write, keyed by the address so a second invitation for one person
	// replaces the first rather than piling up.
	inviteKey := pairAddressKey(t, pairLauncher)
	node, ok := f.db.Value(pairInviteRoot + "/" + inviteKey).(map[string]any)
	if !ok {
		t.Fatalf("no invite node at %s/%s: %v", pairInviteRoot, inviteKey, f.db.Value(pairInviteRoot))
	}
	nodeWant := []string{"accepted_providers", "at", "email", "expires_at", "nonce", "role", "v"}
	nodeGot := make([]string, 0, len(node))
	for k := range node {
		nodeGot = append(nodeGot, k)
	}
	slices.Sort(nodeGot)
	if !slices.Equal(nodeGot, nodeWant) {
		t.Fatalf("the invite node carries %v, want %v", nodeGot, nodeWant)
	}
	if node["email"] != pairLauncher || node["role"] != "operator" || node["nonce"] != nonce {
		t.Fatalf("the invite node is %v, want the email, the role and the payload's nonce", node)
	}
	expires := pairT0.Add(pairInviteLifetime).UTC().Format(time.RFC3339)
	if node["expires_at"] != expires || invited.ExpiresAt != expires {
		t.Fatalf("the invitation expires at %v / %v, want %s (O4's ten minutes)", node["expires_at"], invited.ExpiresAt, expires)
	}
	providers, _ := node["accepted_providers"].([]any)
	if len(providers) != len(pairInviteProviders()) {
		t.Fatalf("the invite node's accepted_providers = %v, want %v", node["accepted_providers"], pairInviteProviders())
	}

	// The member record: the address and nothing else. No role copy (the role
	// is the roster's, O14) and no provider or sub (those are recorded on first
	// use, D2785, by the pairing endpoint).
	memberKey := pairAddressKey(t, pairLauncher)
	member, ok := f.db.Value(pairMemberRoot + "/" + memberKey).(map[string]any)
	if !ok {
		t.Fatalf("no member record at %s/%s: %v", pairMemberRoot, memberKey, f.db.Value(pairMemberRoot))
	}
	memberWant := []string{"at", "email", "v"}
	memberGot := make([]string, 0, len(member))
	for k := range member {
		memberGot = append(memberGot, k)
	}
	slices.Sort(memberGot)
	if !slices.Equal(memberGot, memberWant) {
		t.Fatalf("the member record carries %v, want %v", memberGot, memberWant)
	}
	if member["email"] != pairLauncher {
		t.Fatalf("the member record is %v, want the invited address", member)
	}

	// The invitation is not a pairing: no device node, no pairing subtree, no
	// keychain item, no token minted, and it waits for nothing and asks nothing
	// (it ran with no terminal and read no line).
	if v := f.db.Value(rules.PathDevices); v != nil {
		t.Fatalf("an invitation wrote a device node: %v", v)
	}
	if v := f.db.Value("pairing"); v != nil {
		t.Fatalf("an invitation wrote a pairing node: %v", v)
	}
	if len(f.keys.Keys) != 0 {
		t.Fatalf("an invitation wrote the keychain: %v", f.keys.Keys)
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("an invitation minted %d token(s)", n)
	}
	if len(key) != 32 {
		t.Fatalf("the seeded consumer key is %d bytes, want the 32 an Ed25519 public key is", len(key))
	}

	// The document, and the schema the console reads the stream with.
	doc := invited.Invite
	if doc == nil {
		t.Fatalf("the invited event carries no document: %+v", invited)
	}
	if doc.Email != pairLauncher || doc.Role != "operator" || doc.Endpoint != pairInviteEndpointURL {
		t.Fatalf("the document is %+v, want the invited address, role and endpoint", doc)
	}
	if doc.ConsumerKeyFingerprint != fingerprint || doc.ExpiresAt != expires {
		t.Fatalf("the document is %+v, want the fingerprint and the expiry", doc)
	}
	if len(doc.Refused) != 0 || len(doc.Warnings) != 0 {
		t.Fatalf("the document carries refused/warnings: %+v", doc)
	}
	if !slices.Equal(doc.Wrote, []string{pairMemberRoot + "/" + memberKey, pairInviteRoot + "/" + inviteKey}) {
		t.Fatalf("the document's wrote = %v, want the member record and the invite node", doc.Wrote)
	}
	if !slices.Equal(doc.AcceptedProviders, pairInviteProviders()) {
		t.Fatalf("the document's accepted_providers = %v, want %v", doc.AcceptedProviders, pairInviteProviders())
	}

	sch := pairSchema(t)
	for _, line := range pairEventLines(t, out) {
		v, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatalf("the invited event does not parse: %v", err)
		}
		if err := sch.Validate(v); err != nil {
			t.Fatalf("the invited event does not match schemas/pair-event.schema.json: %v", err)
		}
	}
}

// TestPairInviteLeavesAMemberRecordAlone: the member record is the pairing
// endpoint's to complete — it records the provider and the stable id on first
// use (D2785) — so a second invitation must not clear what a first pairing
// recorded. The write is a create-if-absent one for exactly that reason.
func TestPairInviteLeavesAMemberRecordAlone(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	f.pairSeedConsumerKey(t)
	memberKey := pairAddressKey(t, pairLauncher)
	f.db.Set(pairMemberRoot+"/"+memberKey, map[string]any{
		"v": 1, "email": pairLauncher, "at": pairT0.UTC().Format(time.RFC3339),
		"provider": "google.com", "sub": "provider-subject-1",
	})
	if _, _, err := pairRun(t, "--email", pairLauncher, "--role", "operator", "--json"); err != nil {
		t.Fatalf("an invitation over an existing member record: %v", err)
	}
	member, _ := f.db.Value(pairMemberRoot + "/" + memberKey).(map[string]any)
	if member["provider"] != "google.com" || member["sub"] != "provider-subject-1" {
		t.Fatalf("the invitation rewrote the member record: %v", member)
	}
}

// TestPairInviteLeavesTheOwnAddressAlone: D2799 amends D226 and keeps its rule
// for one's own address, which is the default. Naming your own address is the
// bootstrap pairing, and it writes no invite node and no member record.
func TestPairInviteLeavesTheOwnAddressAlone(t *testing.T) {
	svc := newPairService(t, pairServiceAnswers())
	f := newPairFixture(t, pairMe, svc.edit)
	f.pairSeedConsumerKey(t)
	f.pairSeedRequest(pairServicePairingUID, pairKey)
	if _, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--email", pairMe, "--json"); err != nil {
		t.Fatalf("a pairing naming the operator's own address: %v", err)
	}
	if v := f.db.Value(pairInviteRoot); v != nil {
		t.Fatalf("a pairing of the operator's own address wrote an invite node: %v", v)
	}
	if v := f.db.Value(pairMemberRoot); v != nil {
		t.Fatalf("a pairing of the operator's own address wrote a member record: %v", v)
	}
}

// TestPairInviteDrawsTheCodeAndNoCredential: without --json the invitation is
// the code itself, drawn for the invited person to scan, and the sentence that
// says who it is for and where they pair. The drawing is the encoder's code for
// the invitation's own payload — the same string the invited event carries
// under --json, since the nonce comes from the same fixed source — and that
// payload carries no credential: the app gets the database URL and the API key
// from the service on success (F4).
func TestPairInviteDrawsTheCodeAndNoCredential(t *testing.T) {
	f := newPairFixture(t, pairMe, pairInviteEndpoint)
	key, fingerprint := f.pairSeedConsumerKey(t)
	pairFixedRand(t)

	out, _, err := pairRun(t, "--email", pairLauncher, "--role", "operator")
	if err != nil {
		t.Fatalf("an invitation: %v", err)
	}
	if !strings.Contains(out, "invited "+pairLauncher) || !strings.Contains(out, "role operator") {
		t.Fatalf("the invitation does not name the address and the role:\n%s", out)
	}
	if !strings.Contains(out, pairInviteEndpointURL) {
		t.Fatalf("the invitation does not name the endpoint the invited person pairs at:\n%s", out)
	}

	// The same invitation under --json carries the payload the drawing encodes,
	// which is what makes the two runs one invitation and the drawing checkable
	// against the encoder's own grid rather than against a description of it.
	jsonOut, _, err := pairRun(t, "--email", pairLauncher, "--role", "operator", "--json")
	if err != nil {
		t.Fatalf("the same invitation under --json: %v", err)
	}
	events := pairLines(t, jsonOut)
	if len(events) != 1 || events[0].Event != pairEventInvited {
		t.Fatalf("events = %v, want one invited event", events)
	}
	payload := events[0].Payload
	if !strings.Contains(payload, fingerprint) {
		t.Fatalf("the payload does not carry the consumer key's fingerprint: %s", payload)
	}
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	width := code.Size + 2*pairQRQuiet
	var rows []string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if len([]rune(line)) == width {
			rows = append(rows, line)
		}
	}
	if len(rows) == 0 {
		t.Fatalf("a terminal %d columns wide got no drawing of the %d-column code:\n%s", pairQRMaxColumns, width, out)
	}
	grid, err := pairReadQR(strings.Join(rows, "\n"), pairQRQuiet, code.Size)
	if err != nil {
		t.Fatalf("the drawing is not a code: %v\n%s", err, out)
	}
	for y := range code.Size {
		for x := range code.Size {
			if grid.Black(x, y) != code.Black(x, y) {
				t.Fatalf("module (%d,%d) is drawn %v, the encoder's code has %v", x, y, grid.Black(x, y), code.Black(x, y))
			}
		}
	}

	for _, absent := range []string{`"api_key"`, `"database_url"`, `"token"`, `"key"`, base64.RawURLEncoding.EncodeToString(key)} {
		if strings.Contains(out, absent) {
			t.Fatalf("the invitation's output carries %q:\n%s", absent, out)
		}
	}
}
