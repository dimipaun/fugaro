package credsvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The mobile pairing route (P3, D2782, D2785): the one act by which a phone
// becomes a device of a project. It is served by the SECOND public service of
// the credentials image (F31, O8) and by nothing else — the private service
// mounts no mobile route and this route mounts no existing one (I1 owns both
// mounts) — and this file is the route itself.
//
// # What a pairing decides, and in what order
//
// A pairing is a chain of answers, and the ORDER is the security property:
//
//  1. the call's SHAPE, before anything is read: this route takes no bearer, no
//     run identity and no job header, so every existing credsvc caller class is
//     refused before a body is parsed (F31), and the body is C1's PairRequest
//     and nothing else.
//  2. the ASSERTION, verified through C1's Verifier against the audience this
//     service derives from its OWN endpoint (F39): a relayed QR carries another
//     project's endpoint, so its assertion was signed for another audience and
//     is refused here. A refusal up to this line names no caller and writes no
//     audit line — nothing was verified, which is the rule the identity routes
//     already keep (identity_pair.go).
//  3. the INVITE's nonce, consumed once (C1's InviteStore over P2's node): a
//     nonce that is unknown, already spent or past its ten minutes is one
//     answer, because a distinct one would tell a caller which nonces a project
//     has seen. It is spent BEFORE the policy is applied, and that is
//     deliberate: two credsvc instances must not both honour one invitation,
//     and an invitation a refusal consumed is an invitation the admin sends
//     again.
//  4. the POLICY (P6's `mobile.*` through C1's Policy): the surface on, the
//     provider accepted, the address trusted, the sign-in recent enough, the
//     roster's role, and the identity this project accepted first for the
//     address (mobile_policy.go).
//  5. the FIRST-USE RECORD: provider and sub written into the member record the
//     invitation created, which outlives every device of that address (D2791 as
//     amended by O14).
//  6. the DEVICE: a fresh uid, the device's public key on the node the rules
//     read (O6), the roster's role on it, and the retirement of the device the
//     request named as its predecessor.
//  7. the SESSION: a refresh family (P4's store) and a first custom token
//     (P4's minter, the device-only signer account of O8), and the answer that
//     carries them with the consumer public key and the project's own two
//     bootstrap values.
//
// # What a refusal leaves
//
// Every refusal from step 2 on writes EXACTLY ONE audit line, and none of them
// carries a token, a key, an assertion or a database value — the fields are a
// purpose, the member's own address and a status (P0's purposes, audit.go's
// credentiallessPurpose). The status is the wire vocabulary's own word for what
// happened, because that set is closed and an operator greps one vocabulary:
// `not_admitted` for a caller the project does not admit (the policy's refusals
// and a spent nonce), `request_refused` for a call this route cannot read, and
// `state_unavailable` for the service's own failure — a node nobody could read,
// a member record that could not be written, a mint that failed.
//
// A refusal before step 2 writes none: the assertion is what makes the line's
// caller a fact, and a line whose caller is the request's own text is a line an
// operator reads as a verified identity.
//
// The BODY of every refusal from the body-parse on is C1's Error, because the
// app reads that shape and nothing else; the two refusals before it are the
// wire's, because a caller presenting a run identity or a bearer token is a
// caller of another class and not a phone.
//
// # What this file does NOT do
//
// The prior device's Firebase ID token is verified through MobileDeviceTokens,
// an interface this file declares and I1 wires: verifying one is the
// authenticator's job (auth.go already verifies this project's Firebase ID
// tokens for a run identity, verifyRun), and the seam is here so that this route
// holds no key source of its own.

const (
	// mobileInviteRoot is where the invitations live: the node `fugaro pair`
	// writes (P2, its pairInviteRoot), keyed by the ADDRESS the invitation is
	// for. It is spelled again here rather than imported for mobileMemberRoot's
	// reason (mobile_member.go): the two sides share a node and not a library.
	//
	// The key is the address and NOT the nonce, which is why Consume reads the
	// whole root and spends the entry it finds: C1's InviteStore is asked by
	// nonce, and a node keyed by the nonce would be a second node for one
	// invitation and a second writer to keep in step.
	mobileInviteRoot = "config/admin/invites"

	// mobileInviteVersion is the invite node's own version (P2's
	// pairInviteVersion). A node of another version is refused rather than read
	// as though it were this one: what an invitation means is a contract between
	// the CLI that wrote it and this route.
	mobileInviteVersion = 1

	// mobileConsumerKeyPath is the node the publish path writes the mailbox
	// consumer's public key at (P2's pairConsumerKeyPath). The pairing answers
	// it to the phone, which pins it and checks the fingerprint its QR carried
	// (O12, O16) — so a project that has published none cannot complete a
	// pairing, and the refusal is the service's own state.
	mobileConsumerKeyPath = "config/admin/mailbox_key"

	// mobileConsumerKeyVersion is the version of that node (P2's
	// pairConsumerKeyVersion).
	mobileConsumerKeyVersion = 1

	// mobileDeviceUIDPrefix and mobileDeviceUIDBytes are M4's device uid, and
	// they are spelled here rather than imported because internal/budget/token
	// keeps them unexported (deviceUIDPrefix, deviceUIDBytes): a device's uid
	// begins m~ — never r~, the runs bucket's family, and never p~, a pairing
	// session's — and the random part is 128 bits, 22 unpadded base64url
	// characters. The mailbox's own reader holds the same rule (token's consumer
	// token refuses a mailbox that is not a device uid), and
	// TestMobileDeviceUIDAgreesWithTheBudgetTokensDeviceUID holds the two
	// spellings to one value.
	mobileDeviceUIDPrefix = "m~"
	mobileDeviceUIDBytes  = 16

	// mobileRefusalStatus is the status a mobile contract refusal is answered
	// with. C1 fixes the body (mobilesession.Error) and not the status, and one
	// status for the whole closed set is the honest one: every one of these
	// refusals is the same HTTP fact — the caller was not admitted to this
	// project — and the code in the body is what the phone's screen is chosen
	// by.
	mobileRefusalStatus = http.StatusForbidden
)

// mobileInviteNode is one invite node as the database holds it: P2's pairInvite
// shape, member for member. The two times are RFC3339 strings and the role is a
// name, because the node is written by a CLI for people to read as well as by
// this route (P2's own note on the shape).
type mobileInviteNode struct {
	V                 int      `json:"v"`
	Email             string   `json:"email"`
	Role              string   `json:"role"`
	Nonce             string   `json:"nonce"`
	ExpiresAt         string   `json:"expires_at"`
	AcceptedProviders []string `json:"accepted_providers"`
	At                string   `json:"at"`
}

// MobileConsumerKeyNode is what the publish path writes at mobileConsumerKeyPath:
// the mailbox consumer's public half, one unpadded base64url string of the 32
// bytes an Ed25519 public key is.
type MobileConsumerKeyNode struct {
	V         int    `json:"v"`
	PublicKey string `json:"public_key"`
}

// MobileDeviceNode is the device node this route writes at rules.PathDevices
// (config/admin/devices) under the uid it minted: the address, the roster's
// role, the device's own public key (O6) and the moment. The key is what the
// mailbox gate verifies a message signature against and what proves possession
// at every refresh.
//
// It is exported because the mailbox's reader (J5b) reads the same node, and the
// members' spellings are the ones that reader must use; the address and the role
// are internal/mobileauth's own names (NodeFieldEmail, NodeFieldRole), which P2's
// writer shares.
//
// It carries no name, and that is the one way it differs from P2's
// pairDeviceNode: a phone paired by the app was never named by an operator, and
// P2's `fugaro pair` is where a device gets one. Every reader tolerates the
// absent member — pairDeviceRowFrom leaves the field empty rather than refusing
// the row, and a listing that dropped a device would hide a phone that is on the
// allowlist — so a reader that wants a label has the address.
type MobileDeviceNode struct {
	Email     string `json:"email"`
	Role      string `json:"role"`
	PublicKey string `json:"public_key"`
	At        string `json:"at"`
}

// rtdbInviteStore is C1's InviteStore over the installation's database: the
// invitations `fugaro pair` wrote, and the one spend a pairing makes.
//
// The spend is one compare-and-set on the ROOT, and that is what makes single
// use real rather than local: two credsvc instances that read the same root both
// see the invitation, the first write wins and the second's ETag no longer
// matches, so the loser re-reads, finds no entry with that nonce and answers
// nonce_expired. A store that remembered spent nonces in process memory would be
// a store two instances could both pass through (C1's own reason for the
// interface).
type rtdbInviteStore struct{ db creds.DB }

// NewMobileInviteStore is the store a deployment wires into MobilePairDeps (I1),
// over the same database client the service's other nodes are read with.
func NewMobileInviteStore(db creds.DB) mobilesession.InviteStore { return rtdbInviteStore{db: db} }

// Consume returns the invite the nonce names and spends it, in one write.
//
// The project is not read: an installation's database is one project's own, so
// the root this store reads is already the project's (C1's parameter is the
// caller's spelling of what the deployment knows).
//
// The expiry is deliberately NOT checked here (C1's rule): this store answers
// what the node says, and the route answers nonce_expired for a value past its
// time — one refusal, one place, and a store that refused an expired invite
// would leave it in the node for the next caller to find.
func (s rtdbInviteStore) Consume(ctx context.Context, _, nonce string) (mobilesession.Invite, bool, error) {
	if nonce == "" {
		return mobilesession.Invite{}, false, nil
	}
	for range creds.DefaultAttempts {
		var root map[string]json.RawMessage
		etag, found, err := s.db.GetETag(ctx, mobileInviteRoot, &root)
		if err != nil {
			return mobilesession.Invite{}, false, &mobileStoreFault{Path: mobileInviteRoot, Err: err}
		}
		if !found {
			return mobilesession.Invite{}, false, nil
		}
		key, invite, ok, err := mobileInviteByNonce(root, nonce)
		if err != nil {
			return mobilesession.Invite{}, false, err
		}
		if !ok {
			return mobilesession.Invite{}, false, nil
		}
		remaining := make(map[string]json.RawMessage, len(root)-1)
		for k, v := range root {
			if k != key {
				remaining[k] = v
			}
		}
		// An emptied root is deleted rather than left as an empty object: the
		// node is a list of invitations, and a project with none is a project
		// whose node is not there (the state P2's first invitation finds).
		var write any = remaining
		if len(remaining) == 0 {
			write = nil
		}
		if err := s.db.PutIfMatch(ctx, mobileInviteRoot, etag, write); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				continue
			}
			return mobilesession.Invite{}, false, &mobileStoreFault{Path: mobileInviteRoot, Err: err}
		}
		return invite, true, nil
	}
	return mobilesession.Invite{}, false, &mobileStoreFault{Path: mobileInviteRoot, Err: creds.ErrContention}
}

// mobileInviteByNonce finds the entry one nonce names and reads it.
//
// The search is lenient and the read is strict, which is the pair that keeps one
// unreadable invitation from refusing every pairing: the nonce is looked for with
// a reader that knows one member, and only the entry it finds is decoded as the
// whole shape — a node this release cannot read is then a fault, and a NEIGHBOUR
// of it that nobody asked for is not.
//
// Two entries naming one nonce cannot be written by `fugaro pair` (the nonce is
// random, P2's own), and a hand-edited node that holds two is spent in key
// order: the other stays, and the admin who wrote it reads why in the node.
func mobileInviteByNonce(root map[string]json.RawMessage, nonce string) (string, mobilesession.Invite, bool, error) {
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		var probe struct {
			Nonce string `json:"nonce"`
		}
		if err := json.Unmarshal(root[key], &probe); err != nil || probe.Nonce != nonce {
			continue
		}
		path := mobileInviteRoot + "/" + key
		invite, err := decodeMobileInvite(path, root[key])
		if err != nil {
			return "", mobilesession.Invite{}, false, err
		}
		return key, invite, true, nil
	}
	return "", mobilesession.Invite{}, false, nil
}

// decodeMobileInvite reads one invitation out of the bytes the database
// answered, strictly, and holds it to the vocabulary this service knows: the
// node's version, an address, a role internal/access parses and an expiry in
// this tree's own format. Every one of them is a value the route acts on, so a
// value it cannot resolve is a fault rather than a refusal of the caller — the
// invitation was written by an admin of this installation, and an invitation
// nobody can read is the installation's own state.
func decodeMobileInvite(path string, raw json.RawMessage) (mobilesession.Invite, error) {
	var node mobileInviteNode
	if err := identitywire.DecodeStrict(raw, &node); err != nil {
		return mobilesession.Invite{}, &mobileStoreFault{Path: path, Err: fmt.Errorf("the invitation is not one this release can read: %w", err)}
	}
	if node.V != mobileInviteVersion {
		return mobilesession.Invite{}, &mobileStoreFault{Path: path, Err: fmt.Errorf("the invitation is version %d, and this release reads version %d", node.V, mobileInviteVersion)}
	}
	if node.Email == "" || node.Nonce == "" {
		return mobilesession.Invite{}, &mobileStoreFault{Path: path, Err: errors.New("the invitation names no address or no nonce")}
	}
	role, ok := accesslib.ParseRole(node.Role)
	if !ok {
		return mobilesession.Invite{}, &mobileStoreFault{Path: path, Err: fmt.Errorf("the invitation names the role %q, which is not one internal/access knows", node.Role)}
	}
	expires, err := time.Parse(time.RFC3339, node.ExpiresAt)
	if err != nil {
		return mobilesession.Invite{}, &mobileStoreFault{Path: path, Err: fmt.Errorf("the invitation's expiry is not a time this tree writes: %w", err)}
	}
	return mobilesession.Invite{
		Nonce:             node.Nonce,
		Email:             node.Email,
		Role:              role,
		AcceptedProviders: node.AcceptedProviders,
		ExpiresAt:         expires,
	}, nil
}

// rtdbMobileDevices is the device node and the consumer key as the service reads
// and writes them: two nodes under config/admin, both closed to every client
// token by the deployed rules, so both are reached with the database admin this
// service's account holds (D6).
type rtdbMobileDevices struct{ db creds.DB }

// NewMobileDevices is the store a deployment wires into MobilePairDeps (I1).
func NewMobileDevices(db creds.DB) MobileDeviceNodes { return rtdbMobileDevices{db: db} }

// ConsumerKey reads the mailbox consumer's public key the QR's fingerprint was
// taken from (O12, O16). A node that is not there, or one that is not a 32-byte
// Ed25519 key, is this service's own state: the pairing cannot answer a key the
// phone would refuse, so it refuses instead.
func (d rtdbMobileDevices) ConsumerKey(ctx context.Context) (string, error) {
	var raw json.RawMessage
	_, found, err := d.db.GetETag(ctx, mobileConsumerKeyPath, &raw)
	if err != nil {
		return "", &mobileStoreFault{Path: mobileConsumerKeyPath, Err: err}
	}
	if !found {
		return "", &mobileStoreFault{Path: mobileConsumerKeyPath, Err: errors.New("this installation has published no mailbox consumer key: a pairing answers it to the phone, which pins it and checks the fingerprint its QR carried")}
	}
	var node MobileConsumerKeyNode
	if err := identitywire.DecodeStrict(raw, &node); err != nil {
		return "", &mobileStoreFault{Path: mobileConsumerKeyPath, Err: fmt.Errorf("the consumer key node is not one this release can read: %w", err)}
	}
	if node.V != mobileConsumerKeyVersion {
		return "", &mobileStoreFault{Path: mobileConsumerKeyPath, Err: fmt.Errorf("the consumer key node is version %d, and this release reads version %d", node.V, mobileConsumerKeyVersion)}
	}
	if err := mobileDevicePublicKey(node.PublicKey); err != nil {
		return "", &mobileStoreFault{Path: mobileConsumerKeyPath, Err: fmt.Errorf("the published consumer key is not one this release can use: %w", err)}
	}
	return node.PublicKey, nil
}

// Write writes one device node. It is one atomic patch under the devices root, so
// a phone appears on the allowlist whole or not at all — the rules admit a device
// by the node EXISTING and by auth.uid naming it, and a half-written node would
// be a device that is admitted with no key or with no role.
func (d rtdbMobileDevices) Write(ctx context.Context, uid string, node MobileDeviceNode) error {
	if err := mobileDeviceUIDValid(uid); err != nil {
		return err
	}
	if err := d.db.Patch(ctx, rules.PathDevices, map[string]any{uid: node}); err != nil {
		return &mobileStoreFault{Path: rules.PathDevices + "/" + uid, Err: err}
	}
	return nil
}

// Retire deletes one device node, which is how a re-pairing retires the phone it
// replaces (C1's PriorDeviceIDToken): the rules read the node and nothing else,
// so a node that is gone is a device whose Firebase token reaches nothing at all.
//
// It is idempotent, like MemberStore.Remove and for the same reason: the node may
// already be gone (an admin revoked that device, or an earlier re-pairing retired
// it), and the state the caller asked for is the state it finds.
func (d rtdbMobileDevices) Retire(ctx context.Context, uid string) error {
	if err := mobileDeviceUIDValid(uid); err != nil {
		return err
	}
	if err := d.db.Patch(ctx, rules.PathDevices, map[string]any{uid: nil}); err != nil {
		return &mobileStoreFault{Path: rules.PathDevices + "/" + uid, Err: err}
	}
	return nil
}

// MobileDeviceProof is one device a Firebase ID token proved: the uid its node is
// keyed by and the address it belongs to.
type MobileDeviceProof struct {
	UID   string
	Email string
}

// MobileDeviceTokens verifies the Firebase ID token of the device a re-pairing
// replaces (C1's PriorDeviceIDToken), and it is an interface because verifying
// one is not this route's business: it is the installation's own Firebase
// project's token, checked against the project's published signing keys and the
// project id — which is what auth.go's verifyRun already does for a run's
// identity, and what I1 wires here.
type MobileDeviceTokens interface {
	// VerifyDevice returns the device the token proves, or an error for a token
	// this project did not issue, one that has expired, and one whose address it
	// cannot read. The uid is the token's own user id, which is the key its
	// device node lives under.
	VerifyDevice(ctx context.Context, idToken string) (MobileDeviceProof, error)
}

// MobileDeviceNodes is the device node and the consumer key as the pairing route
// uses them. It is an interface so that a test can watch the two writes, and
// because *rtdb.Client is the implementation a deployment hands in (I1).
type MobileDeviceNodes interface {
	ConsumerKey(ctx context.Context) (string, error)
	Write(ctx context.Context, uid string, node MobileDeviceNode) error
	Retire(ctx context.Context, uid string) error
}

// MobileProjectConfig is the project's own half of a session answer (C1's
// SessionResponse): the values the QR deliberately does not carry (F4) and that
// reach a phone once, on an answer to a call that has already proved an identity.
type MobileProjectConfig struct {
	// ProjectID is the project's opaque id, the name the paired list stores (K1).
	// It is the deployment's, and this route only carries it.
	ProjectID string
	// Endpoint is this mobile service's own URL, scheme and host with no trailing
	// slash: the address a QR carries and the audience an assertion for this
	// project must name (F39). It is the deployment's value and never the
	// request's Host header, which is the caller's own text.
	Endpoint string
	// DatabaseURL and APIKey are the project's Realtime Database URL and its
	// restricted Firebase API key, which the app needs to reach the mailbox after
	// it exchanges the custom token.
	DatabaseURL string
	APIKey      string
}

// MobilePairDeps is everything the pairing route needs, and it is a parameter and
// not a field of Config for the reason identityPairing takes its signer the same
// way: the two services of one image mount different route sets (I1), so what a
// route holds is the mount's business and not the server's.
//
// NewMobilePairDeps builds the four that are this service's own over the
// database; the three a deployment supplies are the verifier, the refresh store
// and the minter, because each holds a grant or a key this service's own code
// does not have (the identity project's keys, the device-only signer account of
// O8).
type MobilePairDeps struct {
	// Verifier is C1's assertion verifier (P1): the identity project's signed
	// claim set, checked against the audience this route derives.
	Verifier mobilesession.Verifier
	// Admit is C1's Policy: membership, provider, age, `mobile.enabled` and the
	// recorded identity (mobile_policy.go).
	Admit mobilesession.Policy
	// Invites is C1's InviteStore: the invitation one nonce names, spent in the
	// read.
	Invites mobilesession.InviteStore
	// Members is C1's MemberStore: the record that outlives the device.
	Members mobilesession.MemberStore
	// Refresh is C1's RefreshStore (P4): the family this pairing starts.
	Refresh mobilesession.RefreshStore
	// Minter is C1's Minter (P4): the one-hour custom token, signed by the
	// device-only signer account (O8).
	Minter mobilesession.Minter
	// DeviceTokens verifies the prior device's Firebase ID token, and a nil one is
	// a deployment that cannot retire a device: a request carrying one is refused
	// rather than answered as though the old phone had been retired.
	DeviceTokens MobileDeviceTokens
	// Nodes is the device node and the consumer key.
	Nodes MobileDeviceNodes
	// Policy is the published policy, read for the values the phone's screens
	// decide with (C1's SessionPolicy). It is read once more than the policy check
	// reads it, and deliberately: C1's Policy answers a Verdict and not the node,
	// and the two readers are the same deployment's.
	Policy PolicyReader
	// Project is the project's own half of the answer.
	Project MobileProjectConfig
}

// MobilePairConfig is what the deployment configures: the dependencies only it
// can supply, the readers the policy is built from, and the project.
type MobilePairConfig struct {
	Verifier       mobilesession.Verifier
	Refresh        mobilesession.RefreshStore
	Minter         mobilesession.Minter
	DeviceTokens   MobileDeviceTokens
	Policy         PolicyReader
	Records        AccessRecords
	ImplicitAdmins []string
	Project        MobileProjectConfig
	Now            func() time.Time
}

// NewMobilePairDeps builds the pairing route's dependencies: the three stores
// this service's own code writes (the invitations, the member records and the
// device nodes) over the installation's database, and the policy over the same
// readers the rest of the service uses.
//
// It is one call because the four have to agree about the database and the clock:
// a deployment that built two of them over two clients would be a service whose
// record of a pairing is not the record its policy read.
func NewMobilePairDeps(db creds.DB, cfg MobilePairConfig) MobilePairDeps {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	members := NewMobileMemberStore(db, cfg.Now)
	return MobilePairDeps{
		Verifier:     cfg.Verifier,
		Refresh:      cfg.Refresh,
		Minter:       cfg.Minter,
		DeviceTokens: cfg.DeviceTokens,
		Invites:      NewMobileInviteStore(db),
		Members:      members,
		Nodes:        NewMobileDevices(db),
		Policy:       cfg.Policy,
		Project:      cfg.Project,
		Admit: NewMobilePolicy(MobilePolicyConfig{
			Policy:         cfg.Policy,
			Records:        cfg.Records,
			Members:        members,
			ImplicitAdmins: cfg.ImplicitAdmins,
			Now:            cfg.Now,
		}),
	}
}

// mobilePair answers POST /mobile/v1/pair (C1's RoutePair). It is a method on the
// Server for the three things every route of this service shares — the refusal
// writer, the audit log and the clock — and its dependencies arrive as a
// parameter (MobilePairDeps).
func (s *Server) mobilePair(ctx context.Context, w http.ResponseWriter, r *http.Request, deps MobilePairDeps) {
	endpoint, audience, ok := mobileOwnEndpoint(deps.Project.Endpoint)
	if !ok {
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable,
			"this service does not know its own mobile endpoint, so it cannot derive the audience an assertion for it must carry: the deployment sets MobileProjectConfig.Endpoint to this service's URL")
		return
	}
	if !s.mobileCallShape(w, r) {
		return
	}
	cl, err := readCall(r, nil)
	if err != nil {
		s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil, requestRefused+err.Error())
		return
	}
	var req mobilesession.PairRequest
	if err := identitywire.DecodeStrict(cl.body, &req); err != nil {
		s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil,
			requestRefused+"this route's body is C1's PairRequest (an assertion, an endpoint, a nonce, a device public key and an optional prior device token) and names nothing else: "+err.Error())
		return
	}
	if req.Endpoint != "" {
		if got, ok := mobilesession.AudienceOf(req.Endpoint); !ok || got != audience {
			s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil,
				requestRefused+"the endpoint this call names is not this project's: a QR that points a phone at another project's service is refused here, and the audience the assertion must carry is derived from the endpoint this service was deployed at")
			return
		}
	}
	if req.Assertion == "" || req.Nonce == "" {
		s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil,
			requestRefused+"a pairing carries the identity project's assertion and the invitation's nonce, and this call is missing one of them")
		return
	}

	claims, err := deps.Verifier.Verify(ctx, req.Assertion, mobilesession.Binding{Audience: audience, Challenge: req.Nonce})
	if err != nil {
		// No audit line: nothing was verified, and a line whose caller is the
		// request's own text is a line an operator reads as a verified identity
		// (this file's own rule above).
		s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil,
			"this project could not verify the assertion this call carries: sign in again in the app and scan a fresh code")
		return
	}
	email, err := mobileauth.NormalizeEmail(claims.Email)
	if err != nil {
		// The address cannot be named, so the line names no caller: a refusal line
		// may carry none, and a line whose caller is a value this project cannot
		// name would be refused by the audit rules instead of recorded.
		s.mobileRefuse(ctx, w, "", PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNotAMember, nil,
			"the assertion's address is not one this project can name, so no member of it can hold it")
		return
	}

	invite, ok, err := deps.Invites.Consume(ctx, deps.Project.ProjectID, req.Nonce)
	if err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	if !ok {
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNonceExpired, nil,
			"this invitation is unknown, already used, or past its ten minutes: ask an admin to send another, and scan the code it prints")
		return
	}
	if invite.Email != email {
		// The invitation is for another address, so this is not its pairing — and
		// the answer says the same thing as every other way the roster can fail to
		// admit, because which one it is is not something to tell whoever holds
		// the phone.
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNotAMember, nil,
			"this invitation was sent to another address, and a pairing is the pairing of the person it was sent to")
		return
	}
	if !s.now().Before(invite.ExpiresAt) {
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNonceExpired, nil,
			"this invitation has expired: an invitation is good for ten minutes, and ask an admin to send another")
		return
	}

	verdict, err := deps.Admit.Allow(ctx, deps.Project.ProjectID, claims)
	if err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	if !verdict.Allowed {
		code := verdict.Code
		if code == "" {
			// A policy that refused without a code is a policy this route cannot
			// map to a screen, and the honest answer is the one the phone treats
			// as "not you": C1's Codes is closed, so a code nobody wrote is not a
			// screen.
			code = mobilesession.CodeNotAMember
		}
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted, code, verdict.Accepted, mobilePolicySentence(code))
		return
	}
	if err := mobileDevicePublicKey(req.DevicePublicKey); err != nil {
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ErrorRequestRefused,
			mobilesession.CodeNotAMember, nil,
			"this call's device public key is not one this project can hold: "+err.Error())
		return
	}

	// Everything below this line writes. Each of the three reads comes first, so
	// that a project whose consumer key is missing, whose policy cannot be read or
	// whose prior device cannot be verified is refused before a device node or a
	// refresh family exists.
	consumerKey, err := deps.Nodes.ConsumerKey(ctx)
	if err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	policy, found, err := deps.Policy.Shared(ctx)
	if err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	if !found {
		s.mobileRefuseState(ctx, w, email, errors.New("the published policy node is not there, so the phone's own screen values cannot be read"))
		return
	}
	prior, ok := s.mobilePriorDevice(ctx, w, email, deps, req.PriorDeviceIDToken)
	if !ok {
		return
	}

	// The identity this project accepted first for the address (D2785), written
	// before the device: it is the fact that outlives every device of it, and a
	// pairing that could not write it must deliver nothing.
	if err := deps.Members.Record(ctx, deps.Project.ProjectID, mobilesession.Member{
		Email: email, Provider: claims.Provider, Sub: claims.Subject,
	}); err != nil {
		if errors.Is(err, errMobileReassigned) {
			s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
				mobilesession.CodeNotAMember, nil, mobilePolicySentence(mobilesession.CodeNotAMember))
			return
		}
		s.mobileRefuseState(ctx, w, email, err)
		return
	}

	uid, err := mobileDeviceUID()
	if err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	if prior != nil {
		// The phone named the device it replaces, and the node goes before the new
		// one is written: the two are one device's session, and the answer says so
		// only once the old one can no longer reach the database (C1's own reading
		// of PriorDeviceIDToken).
		if err := deps.Nodes.Retire(ctx, prior.UID); err != nil {
			s.mobileRefuseState(ctx, w, email, err)
			return
		}
	}
	node := MobileDeviceNode{
		Email:     email,
		Role:      string(verdict.Role),
		PublicKey: req.DevicePublicKey,
		At:        s.now().UTC().Format(time.RFC3339),
	}
	if err := deps.Nodes.Write(ctx, uid, node); err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	grant, err := deps.Refresh.Issue(ctx, deps.Project.ProjectID, uid)
	if err != nil {
		s.mobileUnpair(ctx, deps, uid, "")
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	minted, err := deps.Minter.Mint(ctx, mobilesession.MintRequest{UID: uid, Role: verdict.Role})
	if err != nil {
		s.mobileUnpair(ctx, deps, uid, grant.Family)
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	answer, err := json.Marshal(mobilesession.SessionResponse{
		ProjectID:         deps.Project.ProjectID,
		Endpoint:          endpoint,
		DatabaseURL:       deps.Project.DatabaseURL,
		APIKey:            deps.Project.APIKey,
		Role:              verdict.Role,
		Policy:            mobileSessionPolicy(policy),
		ConsumerPublicKey: consumerKey,
		RefreshToken:      grant.Token,
		CustomToken:       minted.Token,
		ExpiresIn:         mobileExpiresIn(s.now(), minted.ExpiresAt),
	})
	if err != nil {
		// Unreachable: the answer is strings, a role and a time that encoding/json
		// writes. It is kept rather than dropped because the alternative to
		// answering is a panic inside the one act that hands out a session.
		s.mobileUnpair(ctx, deps, uid, grant.Family)
		s.mobileRefuseState(ctx, w, email, err)
		return
	}
	// The act's own line, written before the session reaches the phone and not
	// after: an issuance this service cannot audit is not made (D143), and what
	// "not made" means here is that the tokens reach nobody — the device node and
	// the family go back with it.
	if err := s.mobileAudit(ctx, email, PurposeMobilePair, StatusOK); err != nil {
		s.log.Error("credsvc: the pairing's audit line was not written", "error", err)
		s.mobileUnpair(ctx, deps, uid, grant.Family)
		s.mobileRefuseBody(w, mobilesession.CodeNotAMember, nil, mobileUnaudited)
		return
	}
	s.log.Info("credsvc: a phone was paired", "caller", email, "device", uid, "role", verdict.Role)
	writeSlipAnswer(w, answer)
}

// mobileCallShape refuses every call of a shape this route does not have, before
// a body is read: a bearer token, a run identity and a job name are how the
// service's OTHER caller classes present themselves, and a mobile route takes
// none of them (F31's cross-class rule, which I1's mount pins for the whole route
// set). It answers the refusal itself and reports whether the call may go on.
//
// These refusals are the WIRE contract's and not the mobile contract's, and that
// is the honest split: a run's gateway reads the wire's error types and halts on
// one it does not know, and a call presenting a run identity is a call of a class
// this service does not serve this route — answering it a mobile code would tell
// a run to show a phone screen. No audit line is written: nothing was verified,
// so there is no caller to name.
func (s *Server) mobileCallShape(w http.ResponseWriter, r *http.Request) bool {
	if bearer := r.Header.Get("Authorization"); bearer != "" {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity,
			"this route takes no bearer token: a phone proves an identity project's assertion in the body and holds no service credential, and a job's ID token is a caller of another route")
		return false
	}
	if r.Header.Get(HeaderRunIdentity) != "" || r.Header.Get(HeaderRunJob) != "" {
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it pairs a person's own phone, and a caller presenting a run is a call of a shape it does not have")
		return false
	}
	if r.Method != http.MethodPost {
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"a pairing is a POST, and this call is a "+r.Method)
		return false
	}
	return true
}

// mobilePriorDevice verifies the prior device's Firebase ID token, when the call
// carries one, and reports whether the call may go on. A token this project did
// not issue, one for another address, and one this deployment cannot verify at
// all are three refusals and not a pairing that quietly retires nothing: the
// phone asked for the old pairing to end, and an answer that said the pairing
// succeeded while the old device kept its node would be a phone that stopped
// refreshing while the node it left behind still admitted it.
func (s *Server) mobilePriorDevice(ctx context.Context, w http.ResponseWriter, email string, deps MobilePairDeps, token string) (*MobileDeviceProof, bool) {
	if token == "" {
		return nil, true
	}
	if deps.DeviceTokens == nil {
		s.mobileRefuseState(ctx, w, email, errors.New("this deployment verifies no device tokens, so a pairing that names the device it replaces cannot be served"))
		return nil, false
	}
	prior, err := deps.DeviceTokens.VerifyDevice(ctx, token)
	if err != nil {
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNotAMember, nil,
			"the device token this call carries is not one this project issued to a device that still holds it: sign in again on the phone you are replacing, or pair this one without naming it")
		return nil, false
	}
	if prior.Email != email {
		s.mobileRefuse(ctx, w, email, PurposeMobilePair, ReasonNotAdmitted,
			mobilesession.CodeNotAMember, nil,
			"the device token this call carries belongs to another address, so it names no device of this pairing's member")
		return nil, false
	}
	if err := mobileDeviceUIDValid(prior.UID); err != nil {
		s.mobileRefuseState(ctx, w, email, err)
		return nil, false
	}
	return &prior, true
}

// mobileUnpair is the undo of a pairing that was decided and could not be
// delivered: the device node goes, and the family goes with it when one was
// issued. It is what keeps a failure between the writes from leaving a phone on
// the allowlist with a refresh token nobody holds — the state P4's own rules
// exist to avoid (F32), reached here from the other side.
//
// It reports nothing: every caller is already answering a refusal, and the undo
// is best effort in the one case that can fail — a database that stopped
// answering mid-pairing. A node left behind admits a uid whose tokens were never
// handed out, which `fugaro pair revoke` removes.
func (s *Server) mobileUnpair(ctx context.Context, deps MobilePairDeps, uid, family string) {
	if err := deps.Nodes.Retire(ctx, uid); err != nil {
		s.log.Error("credsvc: a failed pairing's device node could not be removed", "error", err)
	}
	if family == "" {
		return
	}
	if err := deps.Refresh.Revoke(ctx, deps.Project.ProjectID, family); err != nil {
		s.log.Error("credsvc: a failed pairing's refresh family could not be revoked", "error", err)
	}
}

// mobileAudit writes the one line a mobile act leaves (P0's purposes): the
// purpose, the member's own address as the caller, and the status. It goes
// through Identity.Audit so that the four fields an identity fills — the run, its
// repository, the job and the account — are filled by the same code every other
// line's are, and a mobile identity is one whose account is the address and whose
// other three are empty (callerWithoutJobPurpose's own reading of a phone).
//
// No value ever reaches this line: the fields are names and statuses, and there
// is no field a token, a key or an assertion could be written into (audit.go's
// AuditEvent has none).
func (s *Server) mobileAudit(ctx context.Context, email, purpose, status string) error {
	return s.audit.Write(ctx, Identity{JobAccount: email}.Audit(AuditEvent{Purpose: purpose, Status: status}))
}

// mobileRefuse answers one mobile refusal and records it: one body in the mobile
// contract's own shape, one line to the operator, and the phone's answer is the
// same whether or not the line lands (D143's rule, refuseCall's).
//
// status is the audit's own word for what happened and code is the phone's: the
// two vocabularies are different and both are closed, so neither is derived from
// the other.
func (s *Server) mobileRefuse(ctx context.Context, w http.ResponseWriter, email, purpose, status string, code mobilesession.Code, accepted []string, message string) {
	if err := s.mobileAudit(ctx, email, purpose, status); err != nil {
		s.log.Error("credsvc: a refused mobile call could not be audited", "error", err)
	}
	s.mobileRefuseBody(w, code, accepted, message)
}

// mobileRefuseState answers a failure of this SERVICE — a node nobody could read,
// a record nobody could write, a mint that failed — and records it as such. It is
// separate from mobileRefuse because the two are different facts about the world:
// a caller refused is a caller who may try another way, and a service that could
// not tell must never be answered as a person who is not admitted (R16's rule,
// which is why the policy returns an error and not a verdict for the same case).
//
// The phone's body is still the mobile contract's, because the app reads that
// shape and nothing else: the code it carries is not_a_member, which is the
// screen the phone shows for every refusal it cannot act on, and the sentence
// says the truth.
func (s *Server) mobileRefuseState(ctx context.Context, w http.ResponseWriter, email string, cause error) {
	s.log.Error("credsvc: a mobile call could not be served", "error", cause)
	s.mobileRefuse(ctx, w, email, PurposeMobilePair, ErrorStateUnavailable,
		mobilesession.CodeNotAMember, nil,
		"this project could not complete the pairing: its own records could not be read or written, and the operator's log names the read that failed")
}

// mobileRefuseBody writes one mobile refusal with no audit line: C1's Error, one
// status for the whole closed set, and no store. It is the answer of every
// refusal made before an identity was verified, which is the only place a refusal
// may carry no line.
func (s *Server) mobileRefuseBody(w http.ResponseWriter, code mobilesession.Code, accepted []string, message string) {
	body, err := json.Marshal(mobilesession.Error{Code: code, AcceptedProviders: accepted, Message: message})
	if err != nil {
		// Unreachable: three fields, two of them strings. Kept for the reason
		// answerRead keeps its own: the alternative to answering is a panic in a
		// refusal path.
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, mobileRefusalUnbuilt)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(mobileRefusalStatus)
	_, _ = w.Write(body)
}

// mobilePolicySentence is the sentence a policy refusal is answered with, one per
// code, and each one tells the person what to do rather than what failed: the
// phone's screen shows it, and the person holding the phone is the one who can
// act on it.
func mobilePolicySentence(code mobilesession.Code) string {
	switch code {
	case mobilesession.CodeProviderNotAccepted:
		return "this project does not accept the provider you signed in with: sign in with one of the providers this answer lists"
	case mobilesession.CodeAuthTooOld:
		return "your sign-in is older than this project accepts: sign out and sign in again, then scan the code once more"
	case mobilesession.CodeMobileDisabled:
		return "this project has not switched mobile pairing on: ask an admin of the project to publish its policy with mobile enabled"
	case mobilesession.CodeNonceExpired:
		return "this invitation is no longer good: ask an admin of the project to send another, and scan the code it prints"
	default:
		return "this address is not one this project admits: ask an admin of the project to grant it a role, and send a new invitation after"
	}
}

// mobileOwnEndpoint normalises this service's own endpoint and derives the
// audience an assertion for it must carry (F39). The two are the same string
// (AudienceOf is the contract's one derivation, and the answer's Endpoint is what
// the phone stores and calls again), and a deployment whose endpoint is not a URL
// this contract can read cannot derive one — so the route refuses rather than
// checking an assertion against nothing.
func mobileOwnEndpoint(endpoint string) (string, string, bool) {
	audience, ok := mobilesession.AudienceOf(endpoint)
	if !ok {
		return "", "", false
	}
	return audience, audience, true
}

// mobileDevicePublicKey holds a device's public key to what this contract says
// one is (C1's DevicePublicKeySize): one unpadded base64url string of the 32
// bytes an Ed25519 public key is. A key of another length is not a key this
// project can verify a signature with, and the phone would be told its session
// works while every message it signs was refused.
func mobileDevicePublicKey(value string) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return fmt.Errorf("it is not unpadded base64url: %w", err)
	}
	if len(raw) != mobilesession.DevicePublicKeySize {
		return fmt.Errorf("it decodes to %d bytes, and an Ed25519 public key is %d", len(raw), mobilesession.DevicePublicKeySize)
	}
	return nil
}

// mobileDeviceUID draws a device's uid: M4's family (m~ and 128 random bits, the
// spelling internal/budget/token mints) for the Firebase uid this pairing's
// custom token will name. It is drawn here and never derived, so one device's
// name is not guessable from another's, and it is the key the device node lives
// under — which is what makes the node and the token two halves of one device.
func mobileDeviceUID() (string, error) {
	raw := make([]byte, mobileDeviceUIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("credsvc: drawing the device uid failed: %w", err)
	}
	return mobileDeviceUIDPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// mobileDeviceUIDValid refuses a uid that is not one of M4's: the prefix the
// mailbox's own reader requires (token's consumer token), and a body that decodes
// as the random part it is. A uid from anywhere else is one no rule and no reader
// of this installation admits, and writing a node under it would be a device node
// nothing can reach.
func mobileDeviceUIDValid(uid string) error {
	if !strings.HasPrefix(uid, mobileDeviceUIDPrefix) {
		return fmt.Errorf("credsvc: %q is not a device uid: a device's uid begins %q", uid, mobileDeviceUIDPrefix)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(uid[len(mobileDeviceUIDPrefix):])
	if err != nil {
		return fmt.Errorf("credsvc: the device uid is not one this tree mints: %w", err)
	}
	if len(raw) != mobileDeviceUIDBytes {
		return fmt.Errorf("credsvc: the device uid carries %d random bytes, and this tree mints %d", len(raw), mobileDeviceUIDBytes)
	}
	return nil
}

// mobileSessionPolicy is the project's policy as the phone's screens decide with
// it (C1's SessionPolicy): the costly modes, the biometric switch and the
// compaction threshold P6 and Z2 hold, and no more. Every one of them is a
// display input and never an enforcement — what a mode or a threshold permits is
// decided at the consumer (D2791) — so this answer carries the project's own
// values and no judgement about them.
//
// The threshold is the node's own whole percent (D2789), and a node that
// carries no key answers the contract's zero, which is "this project set no
// automatic compaction": the phone's automatic compaction fires on it or, at
// zero, never fires by itself.
func mobileSessionPolicy(pol *projectpolicy.Policy) mobilesession.SessionPolicy {
	modes := make([]string, 0, len(pol.MobileCostlyModes()))
	for _, m := range pol.MobileCostlyModes() {
		modes = append(modes, string(m))
	}
	return mobilesession.SessionPolicy{
		CostlyModes:             modes,
		BiometricOnCostly:       pol.MobileBiometricOnCostly(),
		CompactThresholdPercent: pol.MobileCompactionThresholdPercent(),
	}
}

// mobileExpiresIn is the life of the custom token in seconds, which is what C1's
// ExpiresIn is. A minter that answered no expiry gives 0 rather than a negative
// count: the phone's own clock decides nothing, and a negative number would read
// as a token that is already gone.
func mobileExpiresIn(now, expires time.Time) int {
	if expires.IsZero() || !expires.After(now) {
		return 0
	}
	return int(expires.Sub(now) / time.Second)
}

// mobileUnaudited and mobileRefusalUnbuilt are the two sentences a pairing that
// cannot answer says, and neither names a value.
const (
	mobileUnaudited      = "the pairing could not be recorded, and this service does not hand out a session it cannot record: nothing was delivered, and the operator's log names the failure"
	mobileRefusalUnbuilt = "the service could not build the refusal's own answer"
)
