// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The encrypted paired-list store (mobile identity plan task K1; spec "Storage
// of the paired-workspace list"; D2796, F39, O16).
//
// **What it keeps, and what it does not.** One entry per project a person has
// paired — the project's opaque id, the endpoint the phone calls it on, which
// of the person's signed-in identities paired it, and the fingerprint of that
// project's consumer public key (O16) — under one node per Firebase uid in the
// identity project's own Realtime Database. Readable project names are not here
// and never will be: they stay on the phone (spec, D2796).
//
// **Why the list is sealed, and what that does not buy.** The threat is
// somebody who obtains database access or a dump of it; hiding the data from
// Fugaro itself is not a goal, and the spec rejected keying the ciphertext on
// the Firebase uid because a uid is not a secret. So the document is sealed
// with a symmetric Cloud KMS key whose material never leaves KMS, and the uid
// is bound in as the call's *additional authenticated data*: a ciphertext
// copied from one person's node to another's does not open, because the second
// open asks for a uid different from the one that sealed it. The store is the
// only thing in this service that holds that key's reach — the signing ring is
// a different ring with a different purpose, and it can neither seal nor open
// anything (A3's keyring.tf).
//
// **Client rules deny all of it.** rtdb.rules.json is `.read` and `.write`
// false at the root and grants nothing anywhere: a phone carries a Firebase ID
// token and the project's API key, and both are refused by those rules — the
// instance's own default today, since nothing in this tree publishes the
// document (a deferral recorded in pairedlist.tf; K1 keeps no command that
// writes it). What reaches a node is this service's own account, which holds the
// database's two data permissions and nothing of its lifecycle (pairedlist.tf)
// and so passes the rules rather than being governed by them — which is what
// "only the function decrypts, and only for its owner" comes to: the function is
// the only reader, and the uid it serves is the one its caller's verified ID
// token attests, never one the caller names.
//
// **What this file does not decide.** The fingerprint's own format is O16's and
// is carried by the pairing (P2) and the consumer's key (J6); K1 stores the
// string it was handed and compares nothing. Nothing here is a credential and
// nothing here decides who a person is: the ID token verifier does that
// (idtoken.go) and the policy is the project's (C1, P6).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"net/http"
	"strings"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// RoutePairedList is the path the app reads and writes its own list on. It
	// is a path of the identity service and not one of the mobile routes of
	// C1's contract: those are a project's, and this is the identity project
	// alone.
	RoutePairedList = "/paired-list"

	// pairedListRoot is the node every person's list hangs under, one child per
	// uid. It is the whole of the tree this service keeps in the database.
	pairedListRoot = "paired"

	// maxPairedEntries bounds one person's list. It is generous beside what a
	// phone holds — a person's projects, not a workspace's — and it is here so
	// that a caller cannot hand the store a document past what the sealing call
	// accepts.
	maxPairedEntries = 200
	// The per-field bounds below are all the same kind of thing: a value longer
	// than any honest caller sends, refused rather than stored. Nothing here is
	// a format — the fields' shapes are C1's and O16's.
	maxPairedIDBytes       = 128
	maxPairedEndpointBytes = 512
	maxPairedIdentityBytes = 512
	maxPairedKeyPrintBytes = 256
	maxPairedListBytes     = 48 << 10
	maxPairedListSealBytes = 64 << 10
	maxPairedListUIDBytes  = 128
	maxPairedListBodyBytes = 64 << 10

	// pairedListUnavailable is the code a caller branches on when the list
	// cannot be served at all: a database that did not answer, a key ring that
	// refused, or a ciphertext that does not open under the uid that asked.
	// Nothing about it is the caller's mistake, and a caller that saw a partial
	// list instead would be one that trusted a wrong answer.
	pairedListUnavailable = "paired_list_unavailable"
)

// crcTable is the checksum Cloud KMS counts in: Castagnoli's, not the IEEE one,
// because the field is named crc32c on both sides of the call.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

var (
	// ErrPairedListUID is a uid this store will not key a node on: empty, past
	// the bound, or carrying a byte a database key may not hold. It is a refusal
	// of the call rather than a fault, and the route answers it as the identity
	// it could not read.
	ErrPairedListUID = errors.New("identitysvc: this uid is not one a paired list is kept under")

	// ErrPairedListEntries is a list this store will not keep: an entry missing a
	// field, a field past its bound, an endpoint no project could be called on,
	// a duplicate project, or more entries than one person's list may hold. It
	// is the caller's document and not the service's fault, so the route answers
	// it as a bad request.
	ErrPairedListEntries = errors.New("identitysvc: this is not a paired list this service keeps")
)

// PairedListDocument is one person's whole list, and it is one shape for both
// directions and for the sealed plaintext: the request body, the answer body
// and the bytes the key seals are the same document, so what a read answers is
// what a write stored rather than a second shape that has to agree with it.
type PairedListDocument struct {
	// Entries is the list. It is never null on the wire or in the plaintext: an
	// empty list and an absent one are the same list of no projects, and a
	// reader that iterated a null would be reading a broken document.
	Entries []mobilesession.PairedEntry `json:"entries"`
}

// PairedList is the store: one person's entries, sealed under the identity
// project's own key and kept in its own database, reachable only through the
// two methods below.
type PairedList struct {
	key   *KMSListSealer
	store *RTDBPairedStore
}

// NewPairedList returns the store over one key and one database. Both are
// required: a store with no key would keep a plaintext list, and one with no
// database has nowhere to keep it.
func NewPairedList(key *KMSListSealer, store *RTDBPairedStore) (*PairedList, error) {
	if key == nil {
		return nil, errors.New("identitysvc: a paired-list store with no sealing key would keep the list in the clear")
	}
	if store == nil {
		return nil, errors.New("identitysvc: a paired-list store with no database has nowhere to keep a list")
	}
	return &PairedList{key: key, store: store}, nil
}

// List answers one person's list: empty when they have none, and an error when
// there is one that does not open. It never answers a partial list and never
// answers another person's: the uid is the one its caller verified, and the
// sealed bytes are opened with it as their additional authenticated data.
func (l *PairedList) List(ctx context.Context, uid string) ([]mobilesession.PairedEntry, error) {
	sealed, found, err := l.store.Read(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !found {
		return []mobilesession.PairedEntry{}, nil
	}
	plaintext, err := l.key.Open(ctx, sealed, uid)
	if err != nil {
		return nil, fmt.Errorf("the sealed list under this uid does not open: %w", err)
	}
	var doc PairedListDocument
	if err := json.Unmarshal(plaintext, &doc); err != nil {
		return nil, fmt.Errorf("the sealed list under this uid is not this service's document: %w", err)
	}
	if doc.Entries == nil {
		doc.Entries = []mobilesession.PairedEntry{}
	}
	return doc.Entries, nil
}

// Save replaces one person's whole list with the one it is handed, sealed under
// their uid. It is a replacement and not a merge: the phone holds the list, and
// an entry it dropped (a project it unpaired) must not survive in the store.
//
// Every refusal happens before anything is sealed or written, so a caller whose
// document was refused finds the store exactly as it was.
func (l *PairedList) Save(ctx context.Context, uid string, entries []mobilesession.PairedEntry) error {
	if entries == nil {
		entries = []mobilesession.PairedEntry{}
	}
	if err := checkPairedEntries(entries); err != nil {
		return err
	}
	plaintext, err := json.Marshal(PairedListDocument{Entries: entries})
	if err != nil {
		return fmt.Errorf("the list does not serialise: %w", err)
	}
	if len(plaintext) > maxPairedListBytes {
		return fmt.Errorf("%w: the list is %d bytes serialised, over the %d one person's list may hold", ErrPairedListEntries, len(plaintext), maxPairedListBytes)
	}
	sealed, err := l.key.Seal(ctx, plaintext, uid)
	if err != nil {
		return err
	}
	return l.store.Write(ctx, uid, sealed)
}

// checkPairedEntries validates a list a caller sent. The list is the caller's
// own and nobody else ever reads it, so what is checked here is not an
// attacker's shape but the two things this service would otherwise carry for
// ever: an entry no restore could ever use, and a document past what the store
// and the key accept.
func checkPairedEntries(entries []mobilesession.PairedEntry) error {
	if len(entries) > maxPairedEntries {
		return fmt.Errorf("%w: the list carries %d entries, over the %d it may hold", ErrPairedListEntries, len(entries), maxPairedEntries)
	}
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		switch {
		case entry.ProjectID == "" || len(entry.ProjectID) > maxPairedIDBytes:
			return fmt.Errorf("%w: entry %d carries no usable project id", ErrPairedListEntries, i)
		case len(entry.Endpoint) > maxPairedEndpointBytes:
			return fmt.Errorf("%w: entry %d carries an endpoint longer than any project's", ErrPairedListEntries, i)
		case entry.ConsumerKeyFingerprint == "" || len(entry.ConsumerKeyFingerprint) > maxPairedKeyPrintBytes:
			return fmt.Errorf("%w: entry %d carries no fingerprint of the project's consumer key", ErrPairedListEntries, i)
		case len(entry.Identity) > maxPairedIdentityBytes || !pairedIdentity(entry.Identity):
			return fmt.Errorf("%w: entry %d carries no identity the contract can name", ErrPairedListEntries, i)
		}
		if _, ok := mobilesession.AudienceOf(entry.Endpoint); !ok {
			return fmt.Errorf("%w: entry %d carries no endpoint a project could be called on", ErrPairedListEntries, i)
		}
		if _, dup := seen[entry.ProjectID]; dup {
			return fmt.Errorf("%w: entry %d repeats a project the list already carries", ErrPairedListEntries, i)
		}
		seen[entry.ProjectID] = struct{}{}
	}
	return nil
}

// pairedIdentity reports whether an entry's identity is one the contract can
// name. The alphabet is C1's and it is asked of C1 rather than repeated here:
// IdentityID composes the string and refuses a part it could not compose, so a
// name this service accepted would be one its own contract composes.
func pairedIdentity(identity string) bool {
	provider, sub, ok := strings.Cut(identity, ".")
	if !ok {
		return false
	}
	return mobilesession.IdentityID(provider, sub) == identity
}

// KMSListSealer seals and opens one person's list under the identity project's
// symmetric key, through Cloud KMS's encrypt and decrypt calls.
type KMSListSealer struct {
	client  SymmetricKMS
	keyName string
}

// SymmetricKMS is the whole of what this file needs of Cloud KMS: encrypt and
// decrypt under one symmetric key. It is a seam, so the suite drives the sealer
// against a key it holds in its own process rather than against a service
// (pairedlist_test.go), and *kms.KeyManagementClient is it as it stands.
type SymmetricKMS interface {
	Encrypt(ctx context.Context, req *kmspb.EncryptRequest, opts ...gax.CallOption) (*kmspb.EncryptResponse, error)
	Decrypt(ctx context.Context, req *kmspb.DecryptRequest, opts ...gax.CallOption) (*kmspb.DecryptResponse, error)
}

// NewKMSListSealer builds the sealer a deployment keeps lists with. The key
// name is a crypto key's own resource name — `…/cryptoKeys/<name>` and not a
// version of it: encrypt and decrypt are answered by the key, which picks its
// primary version.
func NewKMSListSealer(client SymmetricKMS, keyName string) (*KMSListSealer, error) {
	if client == nil {
		return nil, errors.New("identitysvc: a paired-list sealer has no client, so it can seal nothing")
	}
	name := strings.TrimSpace(keyName)
	if name == "" {
		return nil, errors.New("identitysvc: a paired-list sealer names no key, and a list sealed under nothing is a list in the clear")
	}
	return &KMSListSealer{client: client, keyName: name}, nil
}

// Seal encrypts one person's list, binding their uid in as the call's
// additional authenticated data.
//
// The uid travels as the AAD and is not part of the plaintext: the plaintext is
// the document the app wrote, so that what a read hands back is what a write
// stored, and the binding is the key's own — a ciphertext moved to another
// person's node does not open, and one edited in place does not either.
func (s *KMSListSealer) Seal(ctx context.Context, plaintext []byte, uid string) ([]byte, error) {
	if err := checkPairedListUID(uid); err != nil {
		return nil, err
	}
	if len(plaintext) == 0 {
		return nil, errors.New("identitysvc: sealing no bytes is not sealing a list")
	}
	if len(plaintext) > maxPairedListSealBytes {
		return nil, fmt.Errorf("identitysvc: the list is %d bytes, over the %d a sealing call reads", len(plaintext), maxPairedListSealBytes)
	}
	aad := []byte(uid)
	answer, err := s.client.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:                              s.keyName,
		Plaintext:                         plaintext,
		AdditionalAuthenticatedData:       aad,
		PlaintextCrc32C:                   wrapperspb.Int64(int64(crc32.Checksum(plaintext, crcTable))),
		AdditionalAuthenticatedDataCrc32C: wrapperspb.Int64(int64(crc32.Checksum(aad, crcTable))),
	})
	if err != nil {
		return nil, fmt.Errorf("identitysvc: the paired list's encrypt call: %w", err)
	}
	// Both flags are the key ring's own statement that it read what this service
	// built: a ring that checked neither could have sealed a different plaintext
	// or bound a different uid, and what came back would be a list nobody can
	// open with a uid nobody can name.
	if !answer.GetVerifiedPlaintextCrc32C() {
		return nil, fmt.Errorf("identitysvc: the paired list's key did not check the %d byte(s) it was asked to seal, so what it sealed is not what this service built", len(plaintext))
	}
	if !answer.GetVerifiedAdditionalAuthenticatedDataCrc32C() {
		return nil, errors.New("identitysvc: the paired list's key did not check the uid it bound the ciphertext to, so the ciphertext may not be this person's")
	}
	sealed := answer.GetCiphertext()
	if len(sealed) == 0 {
		return nil, errors.New("identitysvc: the paired list's key answered no ciphertext")
	}
	counted := answer.GetCiphertextCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(sealed, crcTable)) {
		return nil, fmt.Errorf("identitysvc: the paired list's key answered %d byte(s) of ciphertext whose bytes are not the ones it counted, so the seal is not the one it made", len(sealed))
	}
	return sealed, nil
}

// Open decrypts one person's stored list with their uid as the additional
// authenticated data.
//
// A ciphertext that was sealed under another uid fails here and not later: the
// key ring's own authentication refuses it, which is the whole point of binding
// the uid — a dump of the store is not a way to move one person's list onto
// another's node, and a wrong uid cannot be retried into an answer because
// nothing in the ciphertext says which uid it wanted.
func (s *KMSListSealer) Open(ctx context.Context, sealed []byte, uid string) ([]byte, error) {
	if err := checkPairedListUID(uid); err != nil {
		return nil, err
	}
	if len(sealed) == 0 {
		return nil, errors.New("identitysvc: there is no sealed list to open")
	}
	aad := []byte(uid)
	answer, err := s.client.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:                              s.keyName,
		Ciphertext:                        sealed,
		AdditionalAuthenticatedData:       aad,
		CiphertextCrc32C:                  wrapperspb.Int64(int64(crc32.Checksum(sealed, crcTable))),
		AdditionalAuthenticatedDataCrc32C: wrapperspb.Int64(int64(crc32.Checksum(aad, crcTable))),
	})
	if err != nil {
		return nil, fmt.Errorf("identitysvc: the paired list's decrypt call: %w", err)
	}
	plaintext := answer.GetPlaintext()
	if len(plaintext) == 0 {
		return nil, errors.New("identitysvc: the paired list's key answered no plaintext, and a list that opens to nothing is not one")
	}
	counted := answer.GetPlaintextCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(plaintext, crcTable)) {
		return nil, fmt.Errorf("identitysvc: the paired list's key answered %d byte(s) of plaintext whose bytes are not the ones it counted, so what it opened is not what was sealed", len(plaintext))
	}
	return plaintext, nil
}

// RTDBPairedStore is the store over the identity project's own Realtime
// Database: one node per uid under `paired`, holding the sealed list and
// nothing else.
type RTDBPairedStore struct {
	client *rtdb.Client
}

// NewRTDBPairedStore returns the store over one database client.
func NewRTDBPairedStore(client *rtdb.Client) (*RTDBPairedStore, error) {
	if client == nil {
		return nil, errors.New("identitysvc: a paired-list store with no database client has nowhere to keep a list")
	}
	return &RTDBPairedStore{client: client}, nil
}

// pairedRecord is what one node holds: the sealed list, base64url, and nothing
// else. Nothing about the person and nothing about a project is beside it — a
// dump of the tree is a uid and a string of ciphertext.
type pairedRecord struct {
	Sealed string `json:"sealed"`
}

// pairedListPath is the node one person's list lives at.
func pairedListPath(uid string) (string, error) {
	if err := checkPairedListUID(uid); err != nil {
		return "", err
	}
	return pairedListRoot + "/" + uid, nil
}

// Read answers the sealed bytes at one person's node, and false when there are
// none. A node that holds something this service did not write is a fault and
// not an empty list: it is the one shape a dump-and-restore of somebody else's
// bytes would come to, and answering it as "no list" would silently drop what
// the person paired.
func (s *RTDBPairedStore) Read(ctx context.Context, uid string) (sealed []byte, found bool, err error) {
	path, err := pairedListPath(uid)
	if err != nil {
		return nil, false, err
	}
	var record pairedRecord
	found, err = s.client.Get(ctx, path, &record)
	if err != nil {
		return nil, false, fmt.Errorf("identitysvc: reading the sealed paired list: %w", err)
	}
	if !found {
		return nil, false, nil
	}
	sealed, err = base64.RawURLEncoding.DecodeString(record.Sealed)
	if err != nil || len(sealed) == 0 {
		return nil, false, errors.New("identitysvc: this node does not hold a sealed list this service wrote")
	}
	return sealed, true, nil
}

// Write replaces one person's node with the sealed list. It is a write of the
// whole node and not a merge into it, for the reason Save replaces the whole
// list: an entry the phone dropped must not survive in the store.
func (s *RTDBPairedStore) Write(ctx context.Context, uid string, sealed []byte) error {
	path, err := pairedListPath(uid)
	if err != nil {
		return err
	}
	if len(sealed) == 0 {
		return errors.New("identitysvc: there is no sealed list to store")
	}
	record := pairedRecord{Sealed: base64.RawURLEncoding.EncodeToString(sealed)}
	if err := s.client.Patch(ctx, "", map[string]any{path: record}); err != nil {
		return fmt.Errorf("identitysvc: storing the sealed paired list: %w", err)
	}
	return nil
}

// checkPairedListUID reports whether uid is one a node may be keyed on. The
// alphabet is deliberately narrower than the database's own rule: a Firebase
// uid is letters and digits, and a uid outside that set is one no honest token
// carries, so it is refused here rather than escaped into a path.
func checkPairedListUID(uid string) error {
	if uid == "" || len(uid) > maxPairedListUIDBytes {
		return fmt.Errorf("%w: it is empty or longer than %d bytes", ErrPairedListUID, maxPairedListUIDBytes)
	}
	for i := 0; i < len(uid); i++ {
		b := uid[i]
		switch {
		case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		case b == '-', b == '_':
		default:
			return ErrPairedListUID
		}
	}
	return nil
}

// PairedListHandler serves the paired list over one store. It is the route the
// app calls and the only thing that decides whose list a call is about: the uid
// comes out of the ID token the call carried and out of nowhere else, so no
// request can name another person's list.
type PairedListHandler struct {
	tokens *IDTokenVerifier
	list   *PairedList
	logf   func(format string, args ...any)
}

// PairedListOption configures the handler.
type PairedListOption func(*PairedListHandler)

// WithPairedListLogger sets where the refusals are written (tests). A
// deployment leaves it alone: the lines go to the platform's log.
func WithPairedListLogger(logf func(format string, args ...any)) PairedListOption {
	return func(h *PairedListHandler) {
		if logf != nil {
			h.logf = logf
		}
	}
}

// NewPairedListHandler returns the route over one token verifier and one store.
// The verifier is the same one the assertion route reads ID tokens with
// (idtoken.go), so "who is calling" has one answer in this service.
func NewPairedListHandler(tokens *IDTokenVerifier, list *PairedList, opts ...PairedListOption) (*PairedListHandler, error) {
	if tokens == nil {
		return nil, errors.New("identitysvc: the paired-list route verifies a caller's ID token, and without a verifier it would serve whoever asked")
	}
	if list == nil {
		return nil, errors.New("identitysvc: the paired-list route has no store, so there is no list to serve")
	}
	h := &PairedListHandler{tokens: tokens, list: list, logf: log.Printf}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// Mount puts the route on a mux, as the key set's does (jwks.go).
func (h *PairedListHandler) Mount(mux *http.ServeMux) {
	mux.Handle(RoutePairedList, h)
}

// ServeHTTP answers one call: GET reads the caller's list, PUT replaces it.
func (h *PairedListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		h.refuse(w, nil, http.StatusMethodNotAllowed, codeMethodNotAllowed, "this route answers GET and PUT only")
		return
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		h.refuse(w, nil, http.StatusUnauthorized, codeUnverifiedIDToken, "the request carries no bearer ID token")
		return
	}
	person, err := h.tokens.Verify(r.Context(), token)
	if err != nil {
		h.refuse(w, err, http.StatusUnauthorized, codeUnverifiedIDToken, "the identity project did not mint the ID token this request carries")
		return
	}
	// The uid decides which node is read and which uid the ciphertext is bound
	// to, so an identity whose uid this service cannot key a node on is refused
	// as an identity rather than served as somebody's empty list.
	if err := checkPairedListUID(person.UID); err != nil {
		h.refuse(w, err, http.StatusUnauthorized, codeUnverifiedIDToken, "the identity carries no uid this list can be kept under")
		return
	}
	if r.Method == http.MethodGet {
		entries, err := h.list.List(r.Context(), person.UID)
		if err != nil {
			h.refuse(w, err, http.StatusServiceUnavailable, pairedListUnavailable, "the paired list could not be read at the moment")
			return
		}
		writeJSON(w, http.StatusOK, PairedListDocument{Entries: entries})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPairedListBodyBytes)
	var doc PairedListDocument
	if err := decodeRequest(r, &doc); err != nil {
		h.refuse(w, err, http.StatusBadRequest, codeInvalidRequest, "the body is not this route's request")
		return
	}
	if err := h.list.Save(r.Context(), person.UID, doc.Entries); err != nil {
		if errors.Is(err, ErrPairedListEntries) {
			h.refuse(w, err, http.StatusBadRequest, codeInvalidRequest, "the body is not a paired list this service keeps")
			return
		}
		h.refuse(w, err, http.StatusServiceUnavailable, pairedListUnavailable, "the paired list could not be stored at the moment")
		return
	}
	if doc.Entries == nil {
		doc.Entries = []mobilesession.PairedEntry{}
	}
	writeJSON(w, http.StatusOK, doc)
}

// refuse answers one refusal and logs one line for it, as the assertion route's
// does (handler.go): the code and this package's own error, and no value out of
// the request — neither the token, nor a project id, nor an endpoint.
func (h *PairedListHandler) refuse(w http.ResponseWriter, err error, status int, code, message string) {
	if err != nil {
		h.logf("identitysvc: refused a paired list: code=%s: %v", code, err)
	} else {
		h.logf("identitysvc: refused a paired list: code=%s", code)
	}
	writeJSON(w, status, WireRefusal{Error: WireFault{Code: code, Message: message}})
}
