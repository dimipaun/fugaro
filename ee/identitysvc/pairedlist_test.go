// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The encrypted paired-list store (mobile identity plan task K1). What these
// tests hold is what nothing outside this package can state: a list is stored
// under the uid its caller's verified ID token attests and under no other, the
// ciphertext is bound to that uid through the key's additional authenticated
// data, a dump of the store carries no project and no endpoint, and the rules
// document beside the service grants a client nothing at all.
//
// **No key here either.** The symmetric key Cloud KMS holds is a key generated
// inside this process — AES-GCM, with the same additional-authenticated-data
// binding the KMS call makes — exactly as sign_test.go fakes the signing ring
// (.claude/rules/ee.md R5). The database is internal/gcpfake's Realtime
// Database, and the ID tokens are minted from sign_test.go's own RSA key, so a
// test here needs no account, no network and no home directory.
//
// **What is pinned by the read the acceptance names.** A raw dump is the whole
// tree the fake holds, serialised: TestPairedListDumpRevealsNoEndpoint reads it
// and finds none of the values a person's list is made of.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// The key name the sealer is pointed at. It is a deployment value and not a
	// secret: what is secret is the material inside the key, which this process
	// never holds a real copy of.
	testPairedKey = "projects/fugaro-ident/locations/us-central1/keyRings/fugaro-identity-pairedlist/cryptoKeys/fugaro-paired-list"

	// A second person, so that "another user's token" is a test rather than a
	// sentence. Nothing here is a credential: the tokens are minted at run time.
	testUIDOther     = "firebase-uid-sam"
	testSubjectOther = "google-sub-9988776655"
	testEmailOther   = "sam@acme.example"
)

// fakeSymmetricKMS is Cloud KMS's symmetric encrypt and decrypt as this service
// uses them: one key generated in this process, the additional authenticated
// data bound into the ciphertext exactly as the service binds it, and the CRC32C
// fields filled in and checked the way the service checks them. The knobs are
// what a test needs to show the service refusing an answer it did not get.
type fakeSymmetricKMS struct {
	aead cipher.AEAD

	failWith error
	// denyBytes and denyUID answer the verified flags as an unset — a ring
	// saying it did not check what it was handed.
	denyBytes, denyUID bool
	// miscounts answers a checksum that is not the one of the bytes returned.
	miscounts bool

	encrypts, decrypts int
}

func newFakeSymmetricKMS(t *testing.T) *fakeSymmetricKMS {
	t.Helper()
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("generating the test's key material: %v", err)
	}
	block, err := aes.NewCipher(material)
	if err != nil {
		t.Fatalf("building the test's key: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("building the test's AEAD: %v", err)
	}
	return &fakeSymmetricKMS{aead: aead}
}

func (f *fakeSymmetricKMS) Encrypt(_ context.Context, req *kmspb.EncryptRequest, _ ...gax.CallOption) (*kmspb.EncryptResponse, error) {
	f.encrypts++
	if f.failWith != nil {
		return nil, f.failWith
	}
	if req.GetName() == "" {
		return nil, errors.New("the sealer named no key")
	}
	if len(req.GetPlaintext()) == 0 {
		return nil, errors.New("the sealer asked to seal no bytes")
	}
	if got := req.GetPlaintextCrc32C(); got == nil || got.Value != int64(crc32.Checksum(req.GetPlaintext(), crcTable)) {
		return nil, errors.New("the sealer's plaintext checksum is not the one of the bytes it sent")
	}
	if got := req.GetAdditionalAuthenticatedDataCrc32C(); got == nil || got.Value != int64(crc32.Checksum(req.GetAdditionalAuthenticatedData(), crcTable)) {
		return nil, errors.New("the sealer's uid checksum is not the one of the bytes it sent")
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := f.aead.Seal(nonce, nonce, req.GetPlaintext(), req.GetAdditionalAuthenticatedData())
	sum := int64(crc32.Checksum(sealed, crcTable))
	if f.miscounts {
		sum++
	}
	return &kmspb.EncryptResponse{
		Name:                    req.GetName(),
		Ciphertext:              sealed,
		CiphertextCrc32C:        wrapperspb.Int64(sum),
		VerifiedPlaintextCrc32C: !f.denyBytes,
		VerifiedAdditionalAuthenticatedDataCrc32C: !f.denyUID,
	}, nil
}

func (f *fakeSymmetricKMS) Decrypt(_ context.Context, req *kmspb.DecryptRequest, _ ...gax.CallOption) (*kmspb.DecryptResponse, error) {
	f.decrypts++
	if f.failWith != nil {
		return nil, f.failWith
	}
	ciphertext := req.GetCiphertext()
	if got := req.GetCiphertextCrc32C(); got == nil || got.Value != int64(crc32.Checksum(ciphertext, crcTable)) {
		return nil, errors.New("the sealer's ciphertext checksum is not the one of the bytes it sent")
	}
	if len(ciphertext) < f.aead.NonceSize() {
		return nil, errors.New("the ciphertext is shorter than a nonce")
	}
	nonce, body := ciphertext[:f.aead.NonceSize()], ciphertext[f.aead.NonceSize():]
	// The authentication refusal the whole design rests on: a ciphertext sealed
	// under another uid's additional authenticated data does not open, and the
	// ring answers that as its own error rather than as plaintext.
	plaintext, err := f.aead.Open(nil, nonce, body, req.GetAdditionalAuthenticatedData())
	if err != nil {
		return nil, errors.New("cloudkms: the ciphertext did not authenticate")
	}
	sum := int64(crc32.Checksum(plaintext, crcTable))
	if f.miscounts {
		sum++
	}
	return &kmspb.DecryptResponse{Plaintext: plaintext, PlaintextCrc32C: wrapperspb.Int64(sum)}, nil
}

// pairedFixture is K1 as it would run: the route over A1's own token verifier,
// a real store over the fake database, and a real sealer over a key generated
// here.
type pairedFixture struct {
	*fixture

	db    *gcpfake.RTDB
	kms   *fakeSymmetricKMS
	store *RTDBPairedStore
	list  *PairedList
	route *PairedListHandler
}

func newPairedFixture(t *testing.T) *pairedFixture {
	t.Helper()
	f := newFixture(t)
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatalf("building the database client: %v", err)
	}
	store, err := NewRTDBPairedStore(client)
	if err != nil {
		t.Fatalf("building the paired-list store: %v", err)
	}
	kms := newFakeSymmetricKMS(t)
	sealer, err := NewKMSListSealer(kms, testPairedKey)
	if err != nil {
		t.Fatalf("building the sealer: %v", err)
	}
	list, err := NewPairedList(sealer, store)
	if err != nil {
		t.Fatalf("building the paired list: %v", err)
	}
	route, err := NewPairedListHandler(f.verifier, list, WithPairedListLogger(func(string, ...any) {}))
	if err != nil {
		t.Fatalf("building the route: %v", err)
	}
	return &pairedFixture{fixture: f, db: db, kms: kms, store: store, list: list, route: route}
}

// tokenFor mints an ID token for another person. The fixture's own minting is
// one person's, and the store's whole point is that two people's lists are two
// nodes.
func (p *pairedFixture) tokenFor(t *testing.T, uid, sub, email string, mutate func(map[string]any)) string {
	t.Helper()
	return p.token(t, func(claims map[string]any) {
		claims["sub"], claims["user_id"] = uid, uid
		claims["email"] = email
		claims["firebase"] = map[string]any{
			"sign_in_provider": "google.com",
			"identities":       map[string]any{"google.com": []any{sub}},
		}
		if mutate != nil {
			mutate(claims)
		}
	})
}

// do drives one request through the route and answers the recorder. An empty
// token sends no Authorization header at all.
func (p *pairedFixture) do(t *testing.T, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	p.route.ServeHTTP(rec, req)
	return rec
}

// doHeader drives one request with the Authorization header written verbatim,
// which is how a credential that is not a bearer one is presented.
func (p *pairedFixture) doHeader(t *testing.T, method, target, header, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	p.route.ServeHTTP(rec, req)
	return rec
}

// document renders one list as the body a PUT sends.
func document(t *testing.T, entries []mobilesession.PairedEntry) string {
	t.Helper()
	b, err := json.Marshal(PairedListDocument{Entries: entries})
	if err != nil {
		t.Fatalf("rendering the document: %v", err)
	}
	return string(b)
}

// readDocument reads one answer body as a document.
func readDocument(t *testing.T, rec *httptest.ResponseRecorder) PairedListDocument {
	t.Helper()
	var doc PairedListDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the answer is not a paired-list document (%s): %v", rec.Body.String(), err)
	}
	return doc
}

// refused reads one answer body as the fault it is, and answers the code.
func refused(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body WireRefusal
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer is not a refusal (%s): %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// entries is one honest list: two projects, the identity each was paired under,
// and the fingerprint of each project's consumer public key (C1's shape, O16).
func entries() []mobilesession.PairedEntry {
	return []mobilesession.PairedEntry{
		{
			ProjectID:              "prj_2f7c1d9a4b3e5061",
			Endpoint:               "https://aurora-fp.example",
			Identity:               mobilesession.IdentityID("google.com", testSubject),
			ConsumerKeyFingerprint: "0f1c2d3e4a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f",
		},
		{
			ProjectID:              "prj_88aa10bb22cc33dd",
			Endpoint:               "https://beta.example:8443",
			Identity:               mobilesession.IdentityID("apple.com", "001234.abcd"),
			ConsumerKeyFingerprint: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
		},
	}
}

// TestPairedListSealsUnderTheOwnersUID: the sealer binds the uid in as the
// call's additional authenticated data, and a ciphertext sealed for one uid is
// not plaintext under another. This is the whole of what stops a dump of the
// store being a way to move one person's list onto another's node.
func TestPairedListSealsUnderTheOwnersUID(t *testing.T) {
	kms := newFakeSymmetricKMS(t)
	sealer, err := NewKMSListSealer(kms, testPairedKey)
	if err != nil {
		t.Fatalf("building the sealer: %v", err)
	}
	ctx := context.Background()
	plaintext := []byte(`{"entries":[]}`)

	sealed, err := sealer.Seal(ctx, plaintext, testUID)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	if strings.Contains(string(sealed), "entries") {
		t.Fatalf("the sealed bytes carry the plaintext: %q", sealed)
	}
	opened, err := sealer.Open(ctx, sealed, testUID)
	if err != nil {
		t.Fatalf("opening with the uid that sealed it: %v", err)
	}
	if string(opened) != string(plaintext) {
		t.Fatalf("opened %q, want %q", opened, plaintext)
	}

	// The uid is the binding, and nothing else is: another uid does not open
	// this ciphertext however it is presented.
	if _, err := sealer.Open(ctx, sealed, testUIDOther); err == nil {
		t.Fatal("a ciphertext sealed under one uid opened under another, which is the copy the AAD binding exists to refuse")
	}
	// A ciphertext edited in place does not open either.
	edited := append([]byte(nil), sealed...)
	edited[len(edited)-1] ^= 0x40
	if _, err := sealer.Open(ctx, edited, testUID); err == nil {
		t.Fatal("an edited ciphertext opened, so nothing in it is authenticated")
	}
	// A uid this store cannot key a node on is refused before any call.
	if _, err := sealer.Seal(ctx, plaintext, "dana/../sam"); !errors.Is(err, ErrPairedListUID) {
		t.Fatalf("sealing under a hostile uid = %v, want ErrPairedListUID", err)
	}
	if _, err := sealer.Open(ctx, sealed, ""); !errors.Is(err, ErrPairedListUID) {
		t.Fatalf("opening under no uid = %v, want ErrPairedListUID", err)
	}
	if _, err := sealer.Seal(ctx, nil, testUID); err == nil {
		t.Fatal("sealing no bytes was accepted")
	}
	// The key ring's own statements are what the sealer trusts, so an answer
	// that does not carry them is refused rather than stored.
	for _, tc := range []struct {
		name string
		kms  *fakeSymmetricKMS
	}{
		{"the plaintext was not checked", &fakeSymmetricKMS{aead: kms.aead, denyBytes: true}},
		{"the uid was not checked", &fakeSymmetricKMS{aead: kms.aead, denyUID: true}},
		{"the ciphertext is not the one it counted", &fakeSymmetricKMS{aead: kms.aead, miscounts: true}},
		{"the ring refused", &fakeSymmetricKMS{aead: kms.aead, failWith: errors.New("cloudkms: permission denied")}},
	} {
		other, err := NewKMSListSealer(tc.kms, testPairedKey)
		if err != nil {
			t.Fatalf("%s: building the sealer: %v", tc.name, err)
		}
		if _, err := other.Seal(ctx, plaintext, testUID); err == nil {
			t.Errorf("%s: the seal was accepted", tc.name)
		}
	}
}

// TestPairedListStoresAndServesOneList: one person writes their list and reads
// back exactly it, and a person who has never written one reads an empty list
// rather than an error or a null.
func TestPairedListStoresAndServesOneList(t *testing.T) {
	p := newPairedFixture(t)
	token := p.token(t, nil)

	rec := p.do(t, http.MethodGet, RoutePairedList, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading a list nobody wrote = %d (%s)", rec.Code, rec.Body)
	}
	if got := readDocument(t, rec); len(got.Entries) != 0 {
		t.Fatalf("a person with no list reads %+v, want none", got.Entries)
	}
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("the empty answer is %s, want an empty array rather than a null: a reader that iterated a null would be reading a broken document", strings.TrimSpace(rec.Body.String()))
	}

	want := entries()
	rec = p.do(t, http.MethodPut, RoutePairedList, token, document(t, want))
	if rec.Code != http.StatusOK {
		t.Fatalf("storing = %d (%s)", rec.Code, rec.Body)
	}
	if got := readDocument(t, rec); len(got.Entries) != len(want) || got.Entries[0] != want[0] {
		t.Fatalf("the write answered %+v, want %+v", got.Entries, want)
	}
	rec = p.do(t, http.MethodGet, RoutePairedList, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading back = %d (%s)", rec.Code, rec.Body)
	}
	got := readDocument(t, rec)
	if len(got.Entries) != len(want) {
		t.Fatalf("read back %+v, want %+v", got.Entries, want)
	}
	for i := range want {
		if got.Entries[i] != want[i] {
			t.Fatalf("entry %d read back as %+v, want %+v", i, got.Entries[i], want[i])
		}
	}

	// A write replaces the list and does not merge into it: a project the phone
	// unpaired must not survive in the store.
	rec = p.do(t, http.MethodPut, RoutePairedList, token, document(t, want[:1]))
	if rec.Code != http.StatusOK {
		t.Fatalf("replacing = %d (%s)", rec.Code, rec.Body)
	}
	if got := readDocument(t, p.do(t, http.MethodGet, RoutePairedList, token, "")); len(got.Entries) != 1 || got.Entries[0].ProjectID != want[0].ProjectID {
		t.Fatalf("after a shorter write the list is %+v, want one entry: a write is a replacement", got.Entries)
	}
}

// TestPairedListServesOnlyTheCallersOwnList is the acceptance's "only for its
// owner", and it is this task's red step: with the uid taken from anywhere but
// the verified token, one person's list is served to another.
//
// The uid the store reads is the one the ID token attests and never one the
// request names, so two people's tokens reach two nodes and neither can ask for
// the other's.
func TestPairedListServesOnlyTheCallersOwnList(t *testing.T) {
	p := newPairedFixture(t)
	dana := p.token(t, nil)
	sam := p.tokenFor(t, testUIDOther, testSubjectOther, testEmailOther, nil)

	danas := entries()
	sams := []mobilesession.PairedEntry{{
		ProjectID:              "prj_sam0000sam0000",
		Endpoint:               "https://sam-projects.example",
		Identity:               mobilesession.IdentityID("google.com", testSubjectOther),
		ConsumerKeyFingerprint: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}}
	if rec := p.do(t, http.MethodPut, RoutePairedList, dana, document(t, danas)); rec.Code != http.StatusOK {
		t.Fatalf("dana's write = %d (%s)", rec.Code, rec.Body)
	}
	if rec := p.do(t, http.MethodPut, RoutePairedList, sam, document(t, sams)); rec.Code != http.StatusOK {
		t.Fatalf("sam's write = %d (%s)", rec.Code, rec.Body)
	}

	// Two nodes, one per uid, and each holds a different ciphertext.
	root, ok := p.db.Value(pairedListRoot).(map[string]any)
	if !ok || len(root) != 2 {
		t.Fatalf("the store holds %#v, want one node per uid", p.db.Value(pairedListRoot))
	}
	danaNode, err := json.Marshal(root[testUID])
	if err != nil {
		t.Fatalf("serialising dana's node: %v", err)
	}
	samNode, err := json.Marshal(root[testUIDOther])
	if err != nil {
		t.Fatalf("serialising sam's node: %v", err)
	}
	if string(danaNode) == string(samNode) {
		t.Fatal("both people's nodes hold the same bytes, so the ciphertext is not this person's")
	}

	// Dana's token answers Dana's list and nothing of Sam's, and Sam's answers
	// Sam's: neither value appears in the other's answer.
	got := readDocument(t, p.do(t, http.MethodGet, RoutePairedList, dana, ""))
	if len(got.Entries) != len(danas) || got.Entries[0].ProjectID != danas[0].ProjectID {
		t.Fatalf("dana reads %+v, want %+v", got.Entries, danas)
	}
	body := p.do(t, http.MethodGet, RoutePairedList, dana, "").Body.String()
	for _, entry := range sams {
		if strings.Contains(body, entry.ProjectID) || strings.Contains(body, entry.Endpoint) {
			t.Fatalf("dana's answer carries sam's %q: %s", entry.ProjectID, body)
		}
	}

	// A request cannot name whose list it is about: a uid in the query is not a
	// uid this route reads, so Sam asking for Dana's uid by name still reads his
	// own node.
	got = readDocument(t, p.do(t, http.MethodGet, RoutePairedList+"?uid="+testUID, sam, ""))
	if len(got.Entries) != 1 || got.Entries[0].ProjectID != sams[0].ProjectID {
		t.Fatalf("sam asking for dana's uid read %+v, want his own list: the uid is taken from the verified token and from nowhere else", got.Entries)
	}
}

// TestPairedListRefusesACiphertextUnderAnotherUID is the acceptance's copy
// case, at the store rather than at the sealer: a dump of the database is moved
// from one person's node to another's, and the second person's read refuses
// rather than answers. It is the fault path — the list cannot be served — and
// not a person's bad request, because nothing the caller sent is wrong.
func TestPairedListRefusesACiphertextUnderAnotherUID(t *testing.T) {
	p := newPairedFixture(t)
	dana := p.token(t, nil)
	sam := p.tokenFor(t, testUIDOther, testSubjectOther, testEmailOther, nil)

	if rec := p.do(t, http.MethodPut, RoutePairedList, dana, document(t, entries())); rec.Code != http.StatusOK {
		t.Fatalf("dana's write = %d (%s)", rec.Code, rec.Body)
	}
	stolen, ok := p.db.Value(nodePath(t, testUID)).(map[string]any)
	if !ok {
		t.Fatalf("dana's node holds %#v, want a sealed record", p.db.Value(pairedListRoot))
	}
	p.db.Set("paired/"+testUIDOther, stolen)

	rec := p.do(t, http.MethodGet, RoutePairedList, sam, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sam reading a copied ciphertext = %d (%s), want %d: the uid is bound in as the key's additional authenticated data, so the copy cannot open (K1, O16)", rec.Code, rec.Body, http.StatusServiceUnavailable)
	}
	if code := refused(t, rec); code != pairedListUnavailable {
		t.Fatalf("the refusal's code is %q, want %q", code, pairedListUnavailable)
	}
	// Dana's own node is untouched by Sam's failed read.
	got := readDocument(t, p.do(t, http.MethodGet, RoutePairedList, dana, ""))
	if len(got.Entries) != 2 {
		t.Fatalf("dana's list reads %+v after sam's refusal, want hers", got.Entries)
	}
}

// TestPairedListRefusesACallerItCannotIdentify: every path that is not a
// verified ID token of this identity project serving the uid it attests leaves
// the store exactly as it was. Nothing is written on any of them.
func TestPairedListRefusesACallerItCannotIdentify(t *testing.T) {
	p := newPairedFixture(t)
	good := p.token(t, nil)

	cases := []struct {
		name, method, target, token string
	}{
		{"no token at all", http.MethodGet, RoutePairedList, ""},
		{"a token of another project", http.MethodGet, RoutePairedList, p.token(t, func(c map[string]any) { c["aud"] = "someone-else" })},
		{"an expired token", http.MethodGet, RoutePairedList, p.token(t, func(c map[string]any) { c["exp"] = p.now.Add(-time.Minute).Unix() })},
		{"a password sign-in whose address the project never verified", http.MethodGet, RoutePairedList, p.token(t, func(c map[string]any) {
			c["email_verified"] = false
			c["firebase"] = map[string]any{"sign_in_provider": "password"}
		})},
		{"a token naming two different people", http.MethodGet, RoutePairedList, p.token(t, func(c map[string]any) { c["user_id"] = testUIDOther })},
		{"a token carrying a uid this store cannot key a node on", http.MethodGet, RoutePairedList, p.token(t, func(c map[string]any) {
			c["sub"], c["user_id"] = "dana/../sam", "dana/../sam"
		})},
		{"a method the route does not answer", http.MethodDelete, RoutePairedList, good},
		{"a write with no token", http.MethodPut, RoutePairedList, ""},
		{"a write with an expired token", http.MethodPut, RoutePairedList, p.token(t, func(c map[string]any) { c["exp"] = p.now.Add(-time.Minute).Unix() })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tc.method == http.MethodPut {
				rec = p.do(t, tc.method, tc.target, tc.token, document(t, entries()))
			} else {
				rec = p.do(t, tc.method, tc.target, tc.token, "")
			}
			want := http.StatusUnauthorized
			if tc.method == http.MethodDelete {
				want = http.StatusMethodNotAllowed
				if allow := rec.Header().Get("Allow"); allow != http.MethodGet+", "+http.MethodPut {
					t.Errorf("the 405's Allow header is %q, want the two methods the route answers", allow)
				}
			}
			if rec.Code != want {
				t.Fatalf("= %d (%s), want %d", rec.Code, rec.Body, want)
			}
			if got := refused(t, rec); got != codeUnverifiedIDToken && got != codeMethodNotAllowed {
				t.Fatalf("the refusal's code is %q, want %q or %q", got, codeUnverifiedIDToken, codeMethodNotAllowed)
			}
			if v := p.db.Value(pairedListRoot); v != nil {
				t.Fatalf("a refused call wrote %#v into the store", v)
			}
		})
	}
	// A credential spelled any other way is the same refusal: "Bearer" is the
	// one scheme this route reads.
	for _, header := range []string{good, "Basic " + good, "bearer", "Bearer "} {
		if rec := p.doHeader(t, http.MethodGet, RoutePairedList, header, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("the Authorization header %q = %d, want %d", header, rec.Code, http.StatusUnauthorized)
		}
	}
	if v := p.db.Value(pairedListRoot); v != nil {
		t.Fatalf("a refused credential wrote %#v into the store", v)
	}
}

// TestPairedListRefusesAListNoRestoreCouldUse: every document that is not a
// list this store can keep is refused as the caller's own bad request, and the
// store is left exactly as it was — nothing is sealed and nothing is written.
func TestPairedListRefusesAListNoRestoreCouldUse(t *testing.T) {
	full := entries()
	many := make([]mobilesession.PairedEntry, 0, maxPairedEntries+1)
	for i := 0; i <= maxPairedEntries; i++ {
		many = append(many, mobilesession.PairedEntry{
			ProjectID:              fmt.Sprintf("prj_%04d", i),
			Endpoint:               "https://one.example",
			Identity:               mobilesession.IdentityID("google.com", testSubject),
			ConsumerKeyFingerprint: "abcdef0123456789",
		})
	}
	huge := mobilesession.PairedEntry{
		ProjectID:              "prj_huge",
		Endpoint:               "https://" + strings.Repeat("a", maxPairedEndpointBytes),
		Identity:               mobilesession.IdentityID("google.com", testSubject),
		ConsumerKeyFingerprint: "abcdef0123456789",
	}

	cases := []struct{ name, body string }{
		{"a project id the list cannot name", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.ProjectID = "" }))},
		{"no endpoint at all", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Endpoint = "" }))},
		{"an endpoint no project could be called on", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Endpoint = "aurora-fp.example" }))},
		{"an endpoint carrying a query", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Endpoint = "https://aurora-fp.example/?project=1" }))},
		{"an endpoint with a credential in it", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Endpoint = "https://user:pw@aurora-fp.example" }))},
		{"no identity", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Identity = "" }))},
		{"an identity the contract cannot compose", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.Identity = "dana@acme.example" }))},
		{"no fingerprint of the project's consumer key", document(t, mutate(t, full, func(e *mobilesession.PairedEntry) { e.ConsumerKeyFingerprint = "" }))},
		{"the same project twice", document(t, []mobilesession.PairedEntry{full[0], full[0]})},
		{"more projects than one person's list holds", document(t, many)},
		{"an endpoint longer than any project's", document(t, []mobilesession.PairedEntry{huge})},
		{"a field this route does not know", `{"entries":[],"plaintext":"anything"}`},
		{"a request that is not JSON", `{entries`},
		{"a body carrying two documents", document(t, full) + "\n" + document(t, full)},
		{"a body past what the route reads", `{"entries":[],"pad":"` + strings.Repeat("x", maxPairedListBodyBytes) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPairedFixture(t)
			token := p.token(t, nil)
			rec := p.do(t, http.MethodPut, RoutePairedList, token, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("= %d (%s), want %d", rec.Code, rec.Body, http.StatusBadRequest)
			}
			if got := refused(t, rec); got != codeInvalidRequest {
				t.Fatalf("the refusal's code is %q, want %q", got, codeInvalidRequest)
			}
			if v := p.db.Value(pairedListRoot); v != nil {
				t.Fatalf("a refused write left %#v in the store", v)
			}
			if p.kms.encrypts != 0 {
				t.Fatalf("a refused write reached the key ring %d times, want none: every refusal happens before anything is sealed", p.kms.encrypts)
			}
		})
	}
}

// TestPairedListDumpRevealsNoEndpoint: the acceptance's raw dump. The store
// holds one node per uid and each node holds one field, so a dump of the tree
// is a uid and a string of ciphertext — no project id, no endpoint, no
// fingerprint, and no readable name of anything.
func TestPairedListDumpRevealsNoEndpoint(t *testing.T) {
	p := newPairedFixture(t)
	token := p.token(t, nil)
	want := entries()
	if rec := p.do(t, http.MethodPut, RoutePairedList, token, document(t, want)); rec.Code != http.StatusOK {
		t.Fatalf("storing = %d (%s)", rec.Code, rec.Body)
	}

	// The node's own shape: one key, and it is the sealed list.
	node, ok := p.db.Value(nodePath(t, testUID)).(map[string]any)
	if !ok || len(node) != 1 {
		t.Fatalf("the node holds %#v, want exactly one field", p.db.Value(nodePath(t, testUID)))
	}
	if node["sealed"] == nil {
		t.Fatalf("the node's one field is %#v, want the sealed list", node)
	}

	// The raw dump of the whole database, as anybody with a copy of it would
	// read it.
	dump, err := json.Marshal(p.db.Value(""))
	if err != nil {
		t.Fatalf("serialising the dump: %v", err)
	}
	text := string(dump)
	for _, entry := range want {
		for _, value := range []string{entry.ProjectID, entry.Endpoint, entry.Identity, entry.ConsumerKeyFingerprint} {
			if strings.Contains(text, value) {
				t.Fatalf("the dump carries %q: %s", value, text)
			}
		}
	}
	for _, word := range []string{"endpoint", "project_id", "identity", "consumer_key_fingerprint"} {
		if strings.Contains(text, word) {
			t.Fatalf("the dump carries %q, which is a word of the document this service sealed: %s", word, text)
		}
	}
	// What it does carry is the uid, which the spec accepted is not a secret
	// (the sealing is of the list's contents, not of who the person is).
	if !strings.Contains(text, testUID) {
		t.Errorf("the dump does not name the uid's node at all: %s", text)
	}
}

// TestPairedListNodeMustBeWhatThisServiceWrote: a node holding anything but
// this service's own sealed list is a fault and never an empty list, because
// answering it as "no list" would silently drop what the person paired.
func TestPairedListNodeMustBeWhatThisServiceWrote(t *testing.T) {
	cases := []struct {
		name string
		node any
	}{
		{"a bare string", "not-a-record"},
		{"a record with no sealed list", map[string]any{"sealed": ""}},
		{"a sealed list that is not base64url", map[string]any{"sealed": "not base64 !!"}},
		{"bytes this key never sealed", map[string]any{"sealed": base64.RawURLEncoding.EncodeToString([]byte("sixteen bytes..."))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPairedFixture(t)
			token := p.token(t, nil)
			p.db.Set(nodePath(t, testUID), tc.node)
			rec := p.do(t, http.MethodGet, RoutePairedList, token, "")
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("= %d (%s), want %d: a node this service did not write is a fault", rec.Code, rec.Body, http.StatusServiceUnavailable)
			}
			if got := refused(t, rec); got != pairedListUnavailable {
				t.Fatalf("the refusal's code is %q, want %q", got, pairedListUnavailable)
			}
		})
	}
	// The store's own seam refuses the same writes a route could not reach.
	p := newPairedFixture(t)
	if _, _, err := p.store.Read(context.Background(), "dana/../sam"); !errors.Is(err, ErrPairedListUID) {
		t.Errorf("reading under a hostile uid = %v, want ErrPairedListUID", err)
	}
	if err := p.store.Write(context.Background(), "", []byte("sealed")); !errors.Is(err, ErrPairedListUID) {
		t.Errorf("writing under no uid = %v, want ErrPairedListUID", err)
	}
	if err := p.store.Write(context.Background(), testUID, nil); err == nil {
		t.Error("writing no sealed list was accepted")
	}
}

// TestPairedListRefusesAUidItCannotKeyOn: the alphabet a node key is built from
// is narrower than the database's own rule, so a uid is refused here rather
// than escaped into a path.
func TestPairedListRefusesAUidItCannotKeyOn(t *testing.T) {
	bad := []string{"", "a/b", "..", "a.b", "a b", "a$b", "danaé", strings.Repeat("u", maxPairedListUIDBytes+1)}
	for _, uid := range bad {
		if err := checkPairedListUID(uid); !errors.Is(err, ErrPairedListUID) {
			t.Errorf("checkPairedListUID(%q) = %v, want ErrPairedListUID", uid, err)
		}
	}
	for _, uid := range []string{"firebase-uid-dana", "abc123", "A_b-9"} {
		if err := checkPairedListUID(uid); err != nil {
			t.Errorf("checkPairedListUID(%q) = %v, want it accepted: a Firebase uid is letters and digits", uid, err)
		}
	}
}

// TestPairedListBuildsNothingWithoutItsParts: every constructor refuses the
// shape that would store a list in the clear or serve whoever asked.
func TestPairedListBuildsNothingWithoutItsParts(t *testing.T) {
	kms := newFakeSymmetricKMS(t)
	sealer, err := NewKMSListSealer(kms, testPairedKey)
	if err != nil {
		t.Fatalf("building the sealer: %v", err)
	}
	store := newPairedFixture(t).store

	if _, err := NewKMSListSealer(nil, testPairedKey); err == nil {
		t.Error("a sealer with no key ring was built")
	}
	if _, err := NewKMSListSealer(kms, "  "); err == nil {
		t.Error("a sealer naming no key was built")
	}
	if _, err := NewRTDBPairedStore(nil); err == nil {
		t.Error("a store with no database was built")
	}
	if _, err := NewPairedList(nil, store); err == nil {
		t.Error("a paired list with no sealing key was built, and it would keep the list in the clear")
	}
	if _, err := NewPairedList(sealer, nil); err == nil {
		t.Error("a paired list with no store was built")
	}
	list, err := NewPairedList(sealer, store)
	if err != nil {
		t.Fatalf("building the paired list: %v", err)
	}
	if _, err := NewPairedListHandler(nil, list); err == nil {
		t.Error("a route with no token verifier was built, and it would serve whoever asked")
	}
	if _, err := NewPairedListHandler(newFixture(t).verifier, nil); err == nil {
		t.Error("a route with no store was built")
	}

	// Mount puts the route where the service mounts A2's key set: a request to
	// the path reaches this handler rather than the mux's own 404.
	p := newPairedFixture(t)
	mux := http.NewServeMux()
	p.route.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RoutePairedList, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the mounted route answered %d, want %d: the path is served by this handler", rec.Code, http.StatusUnauthorized)
	}
	if got := refused(t, rec); got != codeUnverifiedIDToken {
		t.Fatalf("the mounted route's refusal is %q, want %q", got, codeUnverifiedIDToken)
	}
}

// TestPairedListRulesDenyEveryClient: the rules document beside this service is
// what a phone meets, and it grants nothing at all — a Firebase ID token and the
// project's API key are refused by it. The signer's own account holds the
// database role and passes the rules, which is why the function is the only
// reader and why a dump of the store is what the sealing above is for.
func TestPairedListRulesDenyEveryClient(t *testing.T) {
	raw, err := os.ReadFile("rtdb.rules.json")
	if err != nil {
		t.Fatalf("reading rtdb.rules.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("rtdb.rules.json is not a rules document: %v", err)
	}
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("rtdb.rules.json has no `rules` object: %s", raw)
	}
	if len(doc) != 1 || len(rules) != 2 {
		t.Fatalf("rtdb.rules.json is %s, want one `rules` object holding `.read` and `.write` and nothing else", strings.TrimSpace(string(raw)))
	}
	for _, key := range []string{".read", ".write"} {
		if granted, ok := rules[key].(bool); !ok || granted {
			t.Fatalf("the rules grant %s (%#v): a rule granting a client a read or a write anywhere is a store that client can dump", key, rules[key])
		}
	}
}

// mutate edits the first entry of a list, so that each hostile document differs
// from an honest one in exactly the field its case names.
func mutate(t *testing.T, in []mobilesession.PairedEntry, edit func(*mobilesession.PairedEntry)) []mobilesession.PairedEntry {
	t.Helper()
	out := append([]mobilesession.PairedEntry(nil), in...)
	edit(&out[0])
	return out
}

// nodePath is the node one person's list lives at, for a test that seeds or
// reads the raw tree itself.
func nodePath(t *testing.T, uid string) string {
	t.Helper()
	path, err := pairedListPath(uid)
	if err != nil {
		t.Fatalf("building the node path for %q: %v", uid, err)
	}
	return path
}
