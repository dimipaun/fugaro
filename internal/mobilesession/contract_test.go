package mobilesession

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
)

// The vectors are the data half of this contract: shared/mobile-session/vectors.json
// holds the claim names, the codes, the routes, the secure-store item names,
// the audience derivation's cases and one example of every wire document, and
// both this test and shared/mobile-session/vectors.test.js read the same file.
// It is one file rather than a table here and a table there because the two
// languages have to agree byte for byte on things neither can check for the
// other: a claim renamed on one side is a signature the other refuses, and a
// session-item name spelled differently is a session that disappears.
//
// The test below holds this package to the file, and it holds the file to the
// shapes: every document in it is unmarshalled into the shape that reads it and
// marshalled back, so a field the file carries and the shape does not — or the
// other way round — is a failure rather than a convention.
//
// Nothing here signs with a key of its own: the envelope's vector is a real
// Ed25519 signature over real bytes, made once with a throwaway key whose
// public half and signature are all that ship. The private half never existed
// in this repository, which is the rule every signing test in this tree keeps.
const vectorsPath = "../../shared/mobile-session/vectors.json"

type contractVectors struct {
	Format           string          `json:"format"`
	Claims           []string        `json:"claims"`
	Codes            []string        `json:"codes"`
	Routes           []string        `json:"routes"`
	SecureStoreItems []string        `json:"secure_store_items"`
	Lifetimes        lifetimes       `json:"lifetimes_seconds"`
	Signature        signature       `json:"signature"`
	Audience         audienceSection `json:"audience"`
	IdentityID       identitySection `json:"identity_id"`
	Wire             map[string]json.RawMessage
	KeySet           keySetVector `json:"key_set"`
}

// The file's top-level names, as this reader knows them. A section added to the
// file and not to the reader is a fact one side relies on and the other never
// reads, and a section a reader expects and the file lost is a check that
// silently stops checking, so both are a failure.
var vectorSections = []string{
	"about", "audience", "claims", "codes", "format", "identity_id",
	"key_set", "lifetimes_seconds", "routes", "secure_store_items",
	"signature", "wire",
}

type audienceSection struct {
	About string         `json:"about"`
	Cases []audienceCase `json:"cases"`
}

type identitySection struct {
	About string         `json:"about"`
	Cases []identityCase `json:"cases"`
}

type lifetimes struct {
	Assertion   int `json:"assertion"`
	Invite      int `json:"invite"`
	Challenge   int `json:"challenge"`
	CustomToken int `json:"custom_token"`
	Idle        int `json:"idle"`
}

type signature struct {
	Label     string             `json:"label"`
	Separator string             `json:"separator"`
	Messages  []signatureMessage `json:"messages"`
}

type signatureMessage struct {
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

// audienceCase is one case of the derivation F39 fixes: the endpoint a QR (or a
// paired-list entry) carries and the audience an assertion for it must name, or
// null when the endpoint is one no audience may be derived from.
type audienceCase struct {
	Endpoint string  `json:"endpoint"`
	Audience *string `json:"audience"`
	Why      string  `json:"why"`
}

type identityCase struct {
	Provider string  `json:"provider"`
	Sub      string  `json:"sub"`
	Identity *string `json:"identity"`
	Why      string  `json:"why"`
}

type keySetVector struct {
	Header            KeySetHeader `json:"header"`
	Payload           KeySet       `json:"payload"`
	SigningInputParts []string     `json:"signing_input_parts"`
	Signature         string       `json:"signature"`
	RootPublicKey     JWK          `json:"root_public_key"`
}

func loadVectors(t *testing.T) contractVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(vectorsPath))
	if err != nil {
		t.Fatalf("reading %s: %v (the vectors are the contract's data half; they ship beside the app's loader test)", vectorsPath, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding %s: %v", vectorsPath, err)
	}
	if got := sortedKeys(raw); !equal(vectorSections, got) {
		t.Fatalf("%s carries the sections %v, want %v", vectorsPath, got, vectorSections)
	}
	var v contractVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decoding %s: %v", vectorsPath, err)
	}
	if v.Format != "fugaro mobile session contract vectors" {
		t.Fatalf("%s says format %q, want the tag this reader understands", vectorsPath, v.Format)
	}
	return v
}

// TestVectorsRoundTrip is C1's red step: with no vectors file the contract's
// data half is missing and every check below fails, which is what the task's
// red step says must happen first. With the file present it holds the package
// and the file to each other.
func TestVectorsRoundTrip(t *testing.T) {
	v := loadVectors(t)

	if want := fieldNames(t, Claims{}); !equal(want, v.Claims) {
		t.Errorf("the vectors carry the claim names %v, want the shape's own %v: the signer writes them in this order and the verifier reads that order, so a claim that moves is a signature that stops verifying", v.Claims, want)
	}
	if want := codeStrings(); !equal(want, v.Codes) {
		t.Errorf("the vectors carry the codes %v, want %v", v.Codes, want)
	}
	if want := Routes(); !equal(want, v.Routes) {
		t.Errorf("the vectors carry the routes %v, want %v", v.Routes, want)
	}
	wantItems := []string{SecureStoreIdentitySession, SecureStoreDeviceKey, SecureStoreProjectSession, SecureStoreConsumerKey}
	if !equal(wantItems, v.SecureStoreItems) {
		t.Errorf("the vectors carry the secure-store items %v, want %v", v.SecureStoreItems, wantItems)
	}

	wantLives := lifetimes{
		Assertion:   int(AssertionLifetime / time.Second),
		Invite:      int(InviteLifetime / time.Second),
		Challenge:   int(ChallengeLifetime / time.Second),
		CustomToken: int(CustomTokenLifetime / time.Second),
		Idle:        int(IdleLifetime / time.Second),
	}
	if v.Lifetimes != wantLives {
		t.Errorf("the vectors carry the lifetimes %+v, want %+v (O4's figures are the contract's, so the app reads them rather than keeping a second copy)", v.Lifetimes, wantLives)
	}

	if v.Signature.Label != SignatureLabel || v.Signature.Separator != SignatureSeparator {
		t.Errorf("the vectors carry the signature label %q and separator %q, want %q and %q", v.Signature.Label, v.Signature.Separator, SignatureLabel, SignatureSeparator)
	}
	if len(v.Signature.Messages) == 0 {
		t.Error("the vectors carry no signature message, so the byte format two ends must agree on is checked by nothing")
	}
	for _, m := range v.Signature.Messages {
		got := SignatureMessage(SignaturePurpose(m.Purpose), m.Value)
		if string(got) != m.Message {
			t.Errorf("SignatureMessage(%q) is %q, want the vector's %q", m.Purpose, got, m.Message)
		}
	}
	if got := SignatureMessage("not-a-purpose", "value"); got != nil {
		t.Errorf("SignatureMessage signed an unknown purpose as %q: a device key signs only the messages this contract names", got)
	}
	if got := SignatureMessage(PurposeRefresh, ""); got != nil {
		t.Errorf("SignatureMessage signed an empty value as %q: there is no message to sign", got)
	}

	if len(v.Audience.Cases) == 0 {
		t.Error("the vectors carry no audience case, so the derivation F39 fixes is checked by nothing")
	}
	for _, c := range v.Audience.Cases {
		got, ok := AudienceOf(c.Endpoint)
		switch {
		case c.Audience == nil && ok:
			t.Errorf("AudienceOf(%q) is %q, want a refusal (%s)", c.Endpoint, got, c.Why)
		case c.Audience != nil && !ok:
			t.Errorf("AudienceOf(%q) is refused, want %q (%s)", c.Endpoint, *c.Audience, c.Why)
		case c.Audience != nil && got != *c.Audience:
			t.Errorf("AudienceOf(%q) is %q, want %q (%s)", c.Endpoint, got, *c.Audience, c.Why)
		}
	}

	if len(v.IdentityID.Cases) == 0 {
		t.Error("the vectors carry no identity id case, so the join a paired-list entry and a store item are keyed by is checked by nothing")
	}
	for _, c := range v.IdentityID.Cases {
		got := IdentityID(c.Provider, c.Sub)
		switch {
		case c.Identity == nil && got != "":
			t.Errorf("IdentityID(%q, %q) is %q, want a refusal (%s)", c.Provider, c.Sub, got, c.Why)
		case c.Identity != nil && got != *c.Identity:
			t.Errorf("IdentityID(%q, %q) is %q, want %q (%s)", c.Provider, c.Sub, got, *c.Identity, c.Why)
		}
	}

	roundTripWire(t, v)
	roundTripKeySet(t, v)
}

// TestSessionPolicyCarriesCrashReports: the project's crash-report switch
// (D731, D738) travels to the phone as the session policy's `crash_reports`
// member, in both answers that carry a policy — a pairing (`session_response`)
// and a restore-or-refresh (`refresh_response`) — because a phone that read it
// once and never again would keep a switch the owner had since turned off.
//
// The member is asserted to be *written* in the answer and not left to a
// default: it is not omitempty, so a service that assembled the policy from a
// project whose block predates the member still names it, and
// shared/mobile-session's port refuses a document without it (MISSING_MEMBER)
// rather than reading a missing switch as on.
func TestSessionPolicyCarriesCrashReports(t *testing.T) {
	v := loadVectors(t)
	for _, name := range []string{"session_response", "refresh_response"} {
		var doc struct {
			Policy map[string]json.RawMessage `json:"policy"`
		}
		if err := json.Unmarshal(v.Wire[name], &doc); err != nil {
			t.Fatalf("decoding the %s vector: %v", name, err)
		}
		raw, ok := doc.Policy["crash_reports"]
		if !ok {
			t.Errorf("the vectors' %s carries no policy.crash_reports: the switch the app reads before it collects anything is a member of the policy the phone is answered with", name)
			continue
		}
		if string(raw) != "true" && string(raw) != "false" {
			t.Errorf("the vectors' %s spells policy.crash_reports as %s, want a JSON boolean", name, raw)
		}
	}
}

// TestSessionPolicyAbsentCrashReportsAnswersOff is the fail-safe, and it is the
// one member where an absent value is not the project's default: the member's
// default in a written block is on (D731), and a document that does not carry
// the member decodes to false. Nothing is sent before the switch has been read
// (D734), so a session assembled by a release behind — the skew the plan
// records — is a phone that was told nothing, and it must collect nothing.
//
// The alternative the plan refused is recorded here as the assertion: an
// absent member must NOT answer the default on, which is what a *bool member
// read through the block's own defaults would do.
func TestSessionPolicyAbsentCrashReportsAnswersOff(t *testing.T) {
	const doc = `{"project_id":"fugaro-example","policy":{"costly_modes":["pro"],"biometric_on_costly":false,"compact_threshold_percent":40}}`
	var resp SessionResponse
	if err := json.Unmarshal([]byte(doc), &resp); err != nil {
		t.Fatalf("decoding a session response whose policy predates the member: %v", err)
	}
	if resp.Policy.CrashReports {
		t.Error("a policy with no crash_reports reads as true, want false: an absent member is off, and a phone that was told nothing sends nothing (D734)")
	}
	// And the member is written back the moment the shape is marshalled, which
	// is what keeps the app from ever seeing it missing: the pure-Go round trip
	// here is the Go half of the port's MISSING_MEMBER refusal.
	back, err := json.Marshal(resp.Policy)
	if err != nil {
		t.Fatalf("marshalling the policy back: %v", err)
	}
	if !strings.Contains(string(back), `"crash_reports":false`) {
		t.Errorf("the marshalled policy is %s, want it to name crash_reports: the member is not omitempty, so a session this service answers always carries the switch", back)
	}
}

// keychainName is the alphabet expo-secure-store accepts in a key: letters,
// digits, "." and "-" and "_". A key outside it makes the platform throw rather
// than report a miss, so a name composed outside it is a session the phone
// cannot write at all — and not one it can report as missing either.
var keychainName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// TestComposedStoreItemNames holds every name this contract tells the app to
// compose to that alphabet. The parts are the vectors' own: the identity ids the
// join produces, and the project id a session response and a paired-list entry
// carry.
func TestComposedStoreItemNames(t *testing.T) {
	v := loadVectors(t)

	var parts []string
	for _, c := range v.IdentityID.Cases {
		if c.Identity != nil {
			parts = append(parts, *c.Identity)
		}
	}
	for _, name := range []string{"session_response", "paired_entry"} {
		var doc struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(v.Wire[name], &doc); err != nil {
			t.Fatalf("decoding the %s vector: %v", name, err)
		}
		if doc.ProjectID == "" {
			t.Fatalf("the %s vector carries no project_id, so a project session's name has no part to be composed from", name)
		}
		parts = append(parts, doc.ProjectID)
	}

	for _, prefix := range []string{SecureStoreIdentitySession, SecureStoreDeviceKey, SecureStoreProjectSession, SecureStoreConsumerKey} {
		if !keychainName.MatchString(prefix) {
			t.Errorf("the item prefix %q is outside the keychain's alphabet: every name composed from it is a key the phone throws on", prefix)
		}
		for _, part := range parts {
			if !keychainName.MatchString(prefix + part) {
				t.Errorf("the composed item name %q is outside the keychain's alphabet", prefix+part)
			}
		}
	}
}

// roundTripWire holds every wire document in the file to the shape that reads
// it: the document is unmarshalled and marshalled back, and the two JSON
// documents must be the same. A field the file names and the shape does not is
// dropped by the unmarshal, and a field the shape declares and the file does
// not carry comes back in the re-marshal, so both directions of a disagreement
// fail here rather than on a phone.
func roundTripWire(t *testing.T, v contractVectors) {
	t.Helper()
	readers := map[string]func() any{
		"assertion_request":          func() any { return &AssertionRequest{} },
		"assertion_response":         func() any { return &AssertionResponse{} },
		"claims":                     func() any { return &Claims{} },
		"pair_request":               func() any { return &PairRequest{} },
		"session_response":           func() any { return &SessionResponse{} },
		"refresh_request":            func() any { return &RefreshRequest{} },
		"refresh_response":           func() any { return &RefreshResponse{} },
		"restore_challenge_request":  func() any { return &RestoreChallengeRequest{} },
		"restore_challenge_response": func() any { return &RestoreChallengeResponse{} },
		"restore_request":            func() any { return &RestoreRequest{} },
		"revoke_self_request":        func() any { return &RevokeSelfRequest{} },
		"revoke_self_response":       func() any { return &RevokeSelfResponse{} },
		"invite":                     func() any { return &Invite{} },
		"member":                     func() any { return &Member{} },
		"paired_entry":               func() any { return &PairedEntry{} },
		"error":                      func() any { return &Error{} },
	}
	if len(v.Wire) != len(readers) {
		t.Errorf("the vectors carry %d wire documents, want %d: %v", len(v.Wire), len(readers), sortedKeys(v.Wire))
	}
	for name, mk := range readers {
		doc, ok := v.Wire[name]
		if !ok {
			t.Errorf("the vectors carry no %s document: the shape has nothing holding it", name)
			continue
		}
		into := mk()
		if err := json.Unmarshal(doc, into); err != nil {
			t.Errorf("unmarshalling the vectors' %s: %v", name, err)
			continue
		}
		back, err := json.Marshal(into)
		if err != nil {
			t.Errorf("marshalling %s back: %v", name, err)
			continue
		}
		if !sameJSON(t, doc, back) {
			t.Errorf("the vectors' %s does not round-trip through the shape: the file says %s and the shape says %s", name, doc, back)
		}
	}
	for name := range v.Wire {
		if _, ok := readers[name]; !ok {
			t.Errorf("the vectors carry a %s document and no shape reads it", name)
		}
	}
}

// roundTripKeySet holds the envelope's one vector to what O7 fixes: the signing
// input is the two base64url halves of the header and the payload exactly as
// this package serialises them (the file carries them as `signing_input_parts`,
// and the compact JWS form is their join with `signature`), and the signature is
// a real Ed25519 signature by the root key over that input. The last check is
// what makes the vector a proof rather than a
// description: a header field reordered or a payload re-serialised here stops
// verifying.
func roundTripKeySet(t *testing.T, v contractVectors) {
	t.Helper()
	ks := v.KeySet
	if ks.Header.Alg != KeySetAlg || ks.Header.Typ != KeySetTyp {
		t.Errorf("the envelope's header is %q/%q, want %q/%q", ks.Header.Alg, ks.Header.Typ, KeySetAlg, KeySetTyp)
	}
	if ks.Header.Kid == "" {
		t.Error("the envelope's header names no root key id")
	}
	if ks.Payload.Format != KeySetFormat {
		t.Errorf("the envelope's payload says format %q, want %q", ks.Payload.Format, KeySetFormat)
	}
	if ks.Payload.Seq <= 0 || ks.Payload.Exp <= ks.Payload.Iat {
		t.Errorf("the envelope's payload carries seq %d, iat %d and exp %d: the set's sequence and its validity are what a fetcher compares against its floor and its clock", ks.Payload.Seq, ks.Payload.Iat, ks.Payload.Exp)
	}
	if len(ks.Payload.JWKS.Keys) == 0 {
		t.Fatal("the envelope's payload carries no key, so it is not a key set")
	}
	sawSigningKid := false
	for _, k := range ks.Payload.JWKS.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			t.Errorf("the envelope carries a key %q/%q, want OKP/Ed25519: EdDSA is the only algorithm this contract names", k.Kty, k.Crv)
		}
		if k.Alg != "" && k.Alg != KeySetAlg {
			t.Errorf("the key %q carries alg %q, want %q", k.Kid, k.Alg, KeySetAlg)
		}
		if n := len(decodeKey(t, "a key set key", k.X)); n != DevicePublicKeySize {
			t.Errorf("the key %q is %d bytes, want %d", k.Kid, n, DevicePublicKeySize)
		}
		if k.Kid != "" {
			sawSigningKid = true
		}
	}
	if !sawSigningKid {
		t.Error("no key in the set carries a kid, so nothing names the key a signature was made with")
	}

	header, err := json.Marshal(ks.Header)
	if err != nil {
		t.Fatalf("marshalling the envelope's header: %v", err)
	}
	payload, err := json.Marshal(ks.Payload)
	if err != nil {
		t.Fatalf("marshalling the envelope's payload: %v", err)
	}
	wantInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	if len(ks.SigningInputParts) != 2 {
		t.Fatalf("the signing input ships as %d base64url part(s), want 2 (the header and the payload): the file carries the halves rather than the joined token, which is a JWT-shaped string no fixture may hold", len(ks.SigningInputParts))
	}
	gotInput := strings.Join(ks.SigningInputParts, ".")
	if gotInput != wantInput {
		t.Fatalf("the signing input is %q, want %q: the envelope is signed over the header and payload exactly as this package serialises them, and a reader that rebuilt them its own way would verify a different document", gotInput, wantInput)
	}
	for i, part := range ks.SigningInputParts {
		if strings.Contains(part, ".") {
			t.Errorf("signing input part %d carries a dot: the halves are joined by dots into the compact form, so a part carrying one is a document with the wrong number of segments", i)
		}
	}
	root := decodeKey(t, "the root public key", ks.RootPublicKey.X)
	if len(root) != ed25519.PublicKeySize {
		t.Fatalf("the root public key is %d bytes, want %d", len(root), ed25519.PublicKeySize)
	}
	if ks.RootPublicKey.Kid != ks.Header.Kid {
		t.Errorf("the envelope's header names the root key %q and its public key is %q: the id in the header has to be the key a reader will verify with", ks.Header.Kid, ks.RootPublicKey.Kid)
	}
	sig := decodeKey(t, "the signature", ks.Signature)
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("the envelope's signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(root), []byte(gotInput), sig) {
		t.Error("the envelope's signature does not verify against the root key over the signing input: the vector is what A4 signs and P1 checks, so it has to be a signature and not a description of one")
	}
}

// TestStubsDecideNothing is the other half of C1's acceptance: the stub returns
// fixed shapes and does nothing else. Each one is called with plausible
// arguments and has to answer with the fail-closed zero value — no claims, no
// admission, nothing found, nothing spent, nothing minted, no credential — so
// that a side written in parallel (A1, P1-P4, K1-K3, M1-M3) cannot pass on a
// decision no side has made. Its own arguments must not reach the answer: what
// a stub echoes is what the caller already held.
func TestStubsDecideNothing(t *testing.T) {
	ctx := context.Background()
	claims := Claims{Issuer: "https://identity.example.com", Audience: "https://mobile-example.a.run.app", Email: "dimi@example.com", EmailVerified: true}

	got, err := StubVerifier().Verify(ctx, "not-a-credential-test-assertion", Binding{Audience: claims.Audience, Challenge: "not-a-credential-test-nonce"})
	if err != nil {
		t.Errorf("the stub verifier failed with %v: a stub answers the shape without judging the assertion", err)
	}
	if got != (Claims{}) {
		t.Errorf("the stub verifier answered %+v, want the zero Claims: an identity nobody verified has no claims", got)
	}

	verdict, err := StubPolicy().Allow(ctx, "fugaro-example", claims)
	if err != nil {
		t.Errorf("the stub policy failed with %v", err)
	}
	if verdict.Allowed || verdict.Code != "" || verdict.Role != access.RoleNone || len(verdict.Accepted) != 0 {
		t.Errorf("the stub policy answered %+v, want the zero Verdict: a policy nobody wrote admits nobody", verdict)
	}

	invite, ok, err := StubInviteStore().Consume(ctx, "fugaro-example", "not-a-credential-test-nonce")
	if err != nil {
		t.Errorf("the stub invite store failed with %v", err)
	}
	if ok || !reflect.DeepEqual(invite, Invite{}) {
		t.Errorf("the stub invite store found %+v (ok %v), want nothing found: it spends no nonce", invite, ok)
	}

	member, ok, err := StubMemberStore().Member(ctx, "fugaro-example", "dimi@example.com")
	if err != nil {
		t.Errorf("the stub member store failed with %v", err)
	}
	if ok || member != (Member{}) {
		t.Errorf("the stub member store found %+v (ok %v), want no record", member, ok)
	}
	memberStore := StubMemberStore()
	if err := memberStore.Record(ctx, "fugaro-example", Member{Email: "dimi@example.com", Provider: "google", Sub: "104729384756102938475"}); err != nil {
		t.Errorf("the stub member store failed to record: %v", err)
	}
	if err := memberStore.Remove(ctx, "fugaro-example", "dimi@example.com"); err != nil {
		t.Errorf("the stub member store failed to remove: %v", err)
	}

	refreshes := StubRefreshStore()
	for _, call := range []struct {
		name string
		run  func() (RefreshGrant, error)
	}{
		{"issue", func() (RefreshGrant, error) { return refreshes.Issue(ctx, "fugaro-example", "m~device") }},
		{"rotate", func() (RefreshGrant, error) {
			return refreshes.Rotate(ctx, "fugaro-example", "not-a-credential-test-refresh-token")
		}},
	} {
		grant, err := call.run()
		if err != nil {
			t.Errorf("the stub refresh store failed on %s: %v", call.name, err)
		}
		if grant != (RefreshGrant{}) {
			t.Errorf("the stub refresh store's %s answered %+v, want the zero grant: a token nobody issued is a token that cannot be presented", call.name, grant)
		}
	}
	if err := refreshes.Revoke(ctx, "fugaro-example", "family"); err != nil {
		t.Errorf("the stub refresh store failed to revoke: %v", err)
	}

	token, err := StubMinter().Mint(ctx, MintRequest{UID: "m~device", Role: access.RoleOperator})
	if err != nil {
		t.Errorf("the stub minter failed with %v", err)
	}
	if token != (CustomToken{}) {
		t.Errorf("the stub minter answered %+v, want the zero CustomToken: it holds no signer grant", token)
	}

	claimed, err := StubClaimStore().Claim(ctx, NamespaceJTI, "not-a-credential-test-jti", time.Minute)
	if err != nil {
		t.Errorf("the stub claim store failed with %v: a value already spent is not an error", err)
	}
	if claimed {
		t.Error("the stub claim store claimed a value: a store that claims nothing is the fail-closed end of single-use")
	}

	session := StubSessionResponse("fugaro-example", "https://mobile-example.a.run.app")
	if session.ProjectID != "fugaro-example" || session.Endpoint != "https://mobile-example.a.run.app" {
		t.Errorf("the stub session response dropped the facts it was handed: %+v", session)
	}
	wantSession := SessionResponse{ProjectID: "fugaro-example", Endpoint: "https://mobile-example.a.run.app", Role: access.RoleNone}
	if !reflect.DeepEqual(session, wantSession) {
		t.Errorf("the stub session response is %+v, want %+v: no credential, no policy and no consumer key", session, wantSession)
	}
	if refresh := StubRefreshResponse(); !reflect.DeepEqual(refresh, RefreshResponse{}) {
		t.Errorf("the stub refresh response is %+v, want the zero value", refresh)
	}

	for _, code := range Codes() {
		refusal := StubError(code)
		if refusal.Code != code || refusal.Message != "" || len(refusal.AcceptedProviders) != 0 {
			t.Errorf("StubError(%q) is %+v, want the code alone: the stub decides nothing, including why", code, refusal)
		}
		if !strings.Contains(refusal.Error(), string(code)) {
			t.Errorf("StubError(%q).Error() is %q, which does not name the code", code, refusal.Error())
		}
	}
}

// fieldNames returns the JSON names of a struct's fields, in declaration order.
// It is how the test holds the vectors' claim list to the Claims shape without
// a second copy of the names anywhere: the shape is the list.
func fieldNames(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s of %s has no JSON name: a claim the signer writes and the verifier reads is named on the wire", typ.Field(i).Name, typ)
		}
		out = append(out, name)
	}
	return out
}

func codeStrings() []string {
	codes := Codes()
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, string(c))
	}
	return out
}

func decodeKey(t *testing.T, what, value string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		t.Fatalf("decoding %s: %v: every key and signature in the contract is unpadded base64url", what, err)
	}
	return raw
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decoding %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decoding %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

func equal(a, b []string) bool { return reflect.DeepEqual(a, b) }

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
