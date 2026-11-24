package credsvc

// The session's two routes (P4, D2783, D2794): the hourly refresh a phone makes
// while it holds a project, and the sign-out everywhere that ends the caller's
// own session.
//
// # Why they are a type of their own
//
// They are the only routes of this service whose caller is a phone and not a
// run, a person or a job of the installation: no bearer, no run identity, no
// credential class — a refresh token, a signature over it by the device's own
// key, and the device node that key belongs to. What they need is therefore not
// the service's own configuration but three seams the public service is built
// with (the refresh family store, the device-only minter of O8, and the roster)
// plus the published policy, and they are handed those in one struct rather
// than reaching into the server: the public service mounts exactly these routes
// (P5, I1), the private one mounts none of them, and a type that could read the
// rest of the service's configuration would be a type the private one could
// serve them with.
//
// # The order every answer is made in
//
// It is the same order both routes keep, and each step is where it is for a
// reason that is a rule somewhere:
//
//  1. the body, strictly decoded, so a field this route does not have is
//     refused rather than ignored (the tree's wire rule);
//  2. the token's family, read off the token alone (MobileRefresh.Owner), so
//     that the device node can be found — and the signature checked — BEFORE
//     anything is rotated: a route that rotated first would let whoever copied
//     a token end the session of the device it was copied from;
//  3. the device node, so that a node an admin already removed ends the family
//     with it (F32: no Firebase refresh token outlives its node);
//  4. the signature, against the node's own public key (C1's PurposeRefresh,
//     PurposeRevokeSelf). Until this step the caller has proved nothing, so a
//     refusal before it writes NO audit line — the audit's rule starts once a
//     caller is verified — and the service's own log carries what an operator
//     would otherwise have to read out of the trail;
//  5. the roster: membership and role, re-read at every refresh (O14), because
//     a phone holds a session for as long as the person is a member and not for
//     as long as the token lasts;
//  6. the audit line of the act, written BEFORE the act, because an act this
//     service cannot record must not be made (D143) — and the acts here are the
//     custom token this route mints and the rotation it makes, which is why a
//     line that cannot be written answers 503 with the caller's token still
//     live;
//  7. the acts: the role rewritten on the node (O14), the custom token minted
//     for the node's own uid (O8), the family rotated (D2783).
//
// # What the answers carry
//
// A success is 200 and the contract's shape. A refusal of the caller is 403 and
// the contract's Error with one of its codes — device_revoked for everything
// this route can refuse a phone for, because the screen behind it is the same
// one (C1's CodeDeviceRevoked). A body this route cannot read is 400 and a
// failure of the service's own is 503, and both carry an Error with an EMPTY
// code: the app refuses a code it does not know outright rather than showing a
// screen for it (C1's Code), which is what an outage and a malformed body
// should both produce.
//
// No answer of either route ever carries a Firebase refresh token: this service
// never sees one, and the custom token it does answer is exchanged by the app
// for an ID token without Fugaro in the loop (D2783).

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

// mobileRequestBytes bounds either route's body. A refresh is a token of a few
// dozen bytes and a 64-byte signature, and the bound is generous for the
// encoding and nothing more: a body past it is refused before it is decoded.
const mobileRequestBytes = 8 << 10

// MobileRefresh is the store the refresh route needs: C1's RefreshStore, plus
// the one read that names the device a token was issued to.
//
// The read is not part of C1 because the contract's store is asked to rotate a
// token and nothing else — and this route needs the uid before that, to find
// the public key the signature is checked against. A store that answered it
// with a node read would be answering a question about a caller who has not yet
// proved anything, which is why RTDBRefreshStore.Owner reads no node at all.
type MobileRefresh interface {
	mobilesession.RefreshStore
	// Owner names the device a token was issued to, reporting false for a
	// string this store did not write.
	Owner(ctx context.Context, project, token string) (uid string, ok bool, err error)
}

// MobileDevice is the device node (D2791) as a session reads it: the address
// the roster is re-checked against, the role the pairing resolved and this
// route rewrites (O14), and the public key the caller's signature is verified
// with (O6).
//
// It is deliberately not the whole node: the name, the pairing time, the
// provider and the sub are the pairing's and the policy's business (P3), and a
// field read here that nothing here uses would be a field this route would
// refuse a node for lacking.
type MobileDevice struct {
	// Email is the address the device was paired for, and it is the who of
	// this act: the audit line's caller and the roster's key.
	Email string `json:"email"`
	// Role is the role the node carries, a string and not an access.Role so
	// that a node written with a spelling this build does not know is a node
	// whose role this route rewrites rather than one it fails to read.
	Role string `json:"role"`
	// DevicePublicKey is the device's Ed25519 public key, unpadded base64url
	// (C1's DevicePublicKeySize). A node without one is a pairing from before
	// O6, and no session of this release was ever minted for it.
	DevicePublicKey string `json:"device_public_key"`
}

// MobileDeviceStore reads and writes the device nodes.
type MobileDeviceStore interface {
	// Device reads the node at config/admin/devices/$uid, reporting false when
	// there is none — an admin's revoke, a re-pairing's retirement of the old
	// node, or a node this route removed itself.
	Device(ctx context.Context, uid string) (MobileDevice, bool, error)
	// SetRole rewrites the node's role and nothing else, which is what O14
	// asks for at every refresh: the roster is the one source, and a copy on
	// the node that no refresh rewrote would be a second one.
	SetRole(ctx context.Context, uid string, role access.Role) error
	// Delete removes the node. It is what ends a device's database access at
	// once, because the rules admit a phone by this node's existence (F32).
	Delete(ctx context.Context, uid string) error
}

// MobileDeviceDB is the device store's database seam: a read of one node and
// the child writes that rewrite a role or remove a node. *rtdb.Client is one.
type MobileDeviceDB interface {
	Getter
	Patcher
}

// RTDBDevices is MobileDeviceStore over the installation's database.
type RTDBDevices struct {
	// DB is the database; *rtdb.Client is one.
	DB MobileDeviceDB
}

// Device reads one device node.
func (d RTDBDevices) Device(ctx context.Context, uid string) (MobileDevice, bool, error) {
	if err := checkMobileUID(uid); err != nil {
		return MobileDevice{}, false, err
	}
	var node MobileDevice
	found, err := d.DB.Get(ctx, mobileDevicePath(uid), &node)
	if err != nil || !found {
		return MobileDevice{}, false, err
	}
	return node, true, nil
}

// SetRole rewrites the role child of one device node.
//
// It is a child write and not a read-modify-write of the node, because the node
// is not this route's: the pairing writes it, the CLI's revoke removes it, and
// a refresh that put back the fields it read a moment ago would be a refresh
// that undid a change it never saw.
func (d RTDBDevices) SetRole(ctx context.Context, uid string, role access.Role) error {
	if err := checkMobileUID(uid); err != nil {
		return err
	}
	return d.DB.Patch(ctx, rules.PathDevices, map[string]any{uid + "/role": string(role)})
}

// Delete removes one device node.
func (d RTDBDevices) Delete(ctx context.Context, uid string) error {
	if err := checkMobileUID(uid); err != nil {
		return err
	}
	return d.DB.Patch(ctx, rules.PathDevices, map[string]any{uid: nil})
}

// MobileRecords is the grants node as the session's roster check reads it: the
// stored role and scope pairs. RTDBAccess is one.
type MobileRecords interface {
	Read(ctx context.Context) (access.Node, error)
}

// MobileRoster re-checks a paired address against the installation's roster
// (O14): membership and role are the roster's at every refresh, and a phone
// whose person has no grant left holds nothing.
type MobileRoster interface {
	// RoleAtProject answers the role the address holds at project scope, and
	// access.RoleNone when it holds none. An error is the reader's own failure
	// and never an answer.
	RoleAtProject(ctx context.Context, address string) (access.Role, error)
}

// RTDBRoster is MobileRoster over the installation's own roster, read the way
// the launch gate reads it (launchScope.accessInput): the stored grants and the
// implicit project admins of the configuration, resolved by the same
// access.EffectiveAtProject the gate resolves a person with.
//
// A device node records a role and no scope (D1588), so EffectiveAtProject is
// exactly the question a refresh asks: what may the person this phone belongs
// to hold at project scope. A caller whose grants are all repository-scoped
// holds nothing a device could carry, and its phone is signed out — which is
// the same answer the pairing gives, one route over.
type RTDBRoster struct {
	// Records is the grants node; RTDBAccess is one.
	Records MobileRecords
	// Implicit are the installation's implicit admins as members
	// ("user:<address>"), the gate's first arm (F17.3, F29): the same list
	// IdentityConfig.ImplicitAdmins carries, so an owner whose grant is
	// implicit is not signed out of a phone that paired today.
	Implicit []string
}

// RoleAtProject answers the role address holds at project scope.
func (r RTDBRoster) RoleAtProject(ctx context.Context, address string) (access.Role, error) {
	member, err := access.Member(address)
	if err != nil {
		// The node's own email cannot be a member. It is not a refusal of the
		// caller — the caller is not the node — so it is the service's state.
		return access.RoleNone, fmt.Errorf("credsvc: the device node's address is not one a grant can name: %w", err)
	}
	if r.Records == nil {
		return access.RoleNone, errors.New("credsvc: the session has no roster reader, so no membership can be re-checked")
	}
	node, err := r.Records.Read(ctx)
	if err != nil {
		return access.RoleNone, err
	}
	return access.EffectiveAtProject(access.Input{Grants: node.Grants, Implicit: r.Implicit}, member), nil
}

// MobileSessionConfig is what the two routes are built from. Every field is
// required, and a field that is missing is refused at construction: a session
// that cannot re-check a membership, mint a token or record its act is not a
// session this service may answer.
type MobileSessionConfig struct {
	// Project is this installation's project id: the value C1's store and
	// policy calls carry. It is validated and otherwise unused — see
	// RTDBRefreshStore.
	Project string
	// Refresh is the refresh family store (RTDBRefreshStore in a deployment).
	Refresh MobileRefresh
	// Mint is the device-only signer's minter (O8): DeviceMinter in a
	// deployment, and never the account this installation's runs are signed
	// with.
	Mint mobilesession.Minter
	// Devices is the device node store.
	Devices MobileDeviceStore
	// Roster re-checks membership and role.
	Roster MobileRoster
	// Policy reads the published project policy, for the session policy the
	// phone decides its screens with (P6).
	Policy PolicyReader
	// Audit is the service's auditor. It is the same writer the rest of the
	// service uses, because an operator reads one stream (C1.13): the mobile
	// purposes are its own (P0).
	Audit *Auditor
	// Log is where the service's own lines go. nil is slog.Default().
	Log *slog.Logger
}

// MobileSession serves the refresh and revoke-self routes.
type MobileSession struct {
	cfg MobileSessionConfig
	log *slog.Logger
}

// NewMobileSession validates cfg and returns the routes' handler.
func NewMobileSession(cfg MobileSessionConfig) (*MobileSession, error) {
	switch {
	case cfg.Project == "":
		return nil, errors.New("credsvc: the mobile session has no project id")
	case cfg.Refresh == nil:
		return nil, errors.New("credsvc: the mobile session has no refresh store")
	case cfg.Mint == nil:
		return nil, errors.New("credsvc: the mobile session has no minter: a session that cannot mint an access token is not one")
	case cfg.Devices == nil:
		return nil, errors.New("credsvc: the mobile session has no device store")
	case cfg.Roster == nil:
		return nil, errors.New("credsvc: the mobile session has no roster reader: a session that cannot re-check a membership is not one")
	case cfg.Policy == nil:
		return nil, errors.New("credsvc: the mobile session has no policy reader")
	case cfg.Audit == nil:
		return nil, errors.New("credsvc: the mobile session has no auditor: an act this service could not audit must not be made")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &MobileSession{cfg: cfg, log: log}, nil
}

// ServeRefresh answers POST /mobile/v1/refresh (C1's RouteRefresh): the
// successor refresh token, a fresh one-hour custom token, and the role and
// policy as they are now.
func (m *MobileSession) ServeRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, ok := m.readRefresh(w, r)
	if !ok {
		return
	}
	uid, ok := m.owner(ctx, w, req.RefreshToken)
	if !ok {
		return
	}
	dev, ok := m.device(ctx, w, uid)
	if !ok {
		return
	}
	if !m.possession(w, uid, dev, req.RefreshToken, req.Signature, mobilesession.PurposeRefresh) {
		return
	}
	role, ok := m.membership(ctx, w, uid, dev)
	if !ok {
		return
	}
	policy, ok := m.sessionPolicy(ctx, w)
	if !ok {
		return
	}
	if !m.rewriteRole(ctx, w, uid, dev, role) {
		return
	}
	if !m.auditOK(ctx, w, dev, PurposeMobileRefresh) {
		return
	}
	custom, ok := m.mint(ctx, w, uid, role)
	if !ok {
		return
	}
	grant, err := m.cfg.Refresh.Rotate(ctx, m.cfg.Project, req.RefreshToken)
	if err != nil {
		m.rotateRefused(ctx, w, uid, err)
		return
	}
	m.answer(w, http.StatusOK, mobilesession.RefreshResponse{
		RefreshToken: grant.Token,
		CustomToken:  custom.Token,
		ExpiresIn:    int(mobilesession.CustomTokenLifetime / time.Second),
		Role:         role,
		Policy:       policy,
	})
}

// ServeRevokeSelf answers POST /mobile/v1/revoke-self (C1's RouteRevokeSelf):
// the caller's own family ends, and the device node goes with it (D2794, F32).
//
// It is the phone's own sign-out and it asks the roster nothing: a person whose
// grant was revoked must still be able to end the session on their phone, and a
// route that refused them would leave the family standing until an admin
// noticed. What it does ask is the same proof the refresh does — the device
// key's signature over the token — so that a copied token is not a way to sign
// somebody else out.
func (m *MobileSession) ServeRevokeSelf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, ok := m.readRevokeSelf(w, r)
	if !ok {
		return
	}
	uid, ok := m.owner(ctx, w, req.RefreshToken)
	if !ok {
		return
	}
	dev, ok := m.device(ctx, w, uid)
	if !ok {
		return
	}
	if !m.possession(w, uid, dev, req.RefreshToken, req.Signature, mobilesession.PurposeRevokeSelf) {
		return
	}
	if !m.auditOK(ctx, w, dev, PurposeMobileRevoke) {
		return
	}
	if err := m.end(ctx, uid); err != nil {
		m.unavailable(w, "the session could not be ended", err)
		return
	}
	m.answer(w, http.StatusOK, mobilesession.RevokeSelfResponse{})
}

// readRefresh reads the refresh route's body strictly. The sentence a caller
// gets names no field and no value — the body is the caller's own and a
// decoder's error can quote it — and the detail is not logged either, for the
// same reason: a body of this route is a credential's shape, and neither the
// trail nor the log may carry one.
func (m *MobileSession) readRefresh(w http.ResponseWriter, r *http.Request) (mobilesession.RefreshRequest, bool) {
	body, ok := m.readBody(w, r)
	if !ok {
		return mobilesession.RefreshRequest{}, false
	}
	var req mobilesession.RefreshRequest
	if err := decodeStrict(body, &req); err != nil {
		m.refuseMobile(w, http.StatusBadRequest, "", "the body is not one JSON object of the refresh token and the signature over it")
		return mobilesession.RefreshRequest{}, false
	}
	if req.RefreshToken == "" || req.Signature == "" {
		m.refuseMobile(w, http.StatusBadRequest, "", "the body must carry both the refresh token and the signature over it")
		return mobilesession.RefreshRequest{}, false
	}
	return req, true
}

// readRevokeSelf reads the revoke-self route's body, which is the same shape
// and read the same way.
func (m *MobileSession) readRevokeSelf(w http.ResponseWriter, r *http.Request) (mobilesession.RevokeSelfRequest, bool) {
	body, ok := m.readBody(w, r)
	if !ok {
		return mobilesession.RevokeSelfRequest{}, false
	}
	var req mobilesession.RevokeSelfRequest
	if err := decodeStrict(body, &req); err != nil {
		m.refuseMobile(w, http.StatusBadRequest, "", "the body is not one JSON object of the refresh token and the signature over it")
		return mobilesession.RevokeSelfRequest{}, false
	}
	if req.RefreshToken == "" || req.Signature == "" {
		m.refuseMobile(w, http.StatusBadRequest, "", "the body must carry both the refresh token and the signature over it")
		return mobilesession.RevokeSelfRequest{}, false
	}
	return req, true
}

// readBody reads one route's body up to the bound, answering the refusal
// itself. A body past the bound is refused before it is decoded.
func (m *MobileSession) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, mobileRequestBytes+1))
	if err != nil || len(body) > mobileRequestBytes {
		m.refuseMobile(w, http.StatusBadRequest, "", "the body could not be read")
		return nil, false
	}
	return body, true
}

// owner reads the device a token names, off the token alone.
//
// A token this store did not write is answered device_revoked and not an
// outage: it is a caller's own string, and the phone's screen for it is the one
// that signs the project out. Nothing is written and no audit line is owed —
// this is before any proof of anything.
func (m *MobileSession) owner(ctx context.Context, w http.ResponseWriter, refreshToken string) (string, bool) {
	uid, ok, err := m.cfg.Refresh.Owner(ctx, m.cfg.Project, refreshToken)
	if err != nil {
		m.unavailable(w, "the refresh family could not be read", err)
		return "", false
	}
	if !ok {
		m.refuseMobile(w, http.StatusForbidden, mobilesession.CodeDeviceRevoked,
			"this refresh token is not one this project issued, so the phone signs the project out")
		return "", false
	}
	return uid, true
}

// device reads the node a token's uid names, and ends the family when the node
// is gone.
//
// The node is what a phone's database access IS (F32: the rules admit a device
// by this node's existence, and the Firebase refresh token the app derived from
// a custom token keeps working until the node is deleted). So a family whose
// node is gone is a family with nothing behind it, and the refresh removes it
// rather than leaving a live token pointing at no device.
func (m *MobileSession) device(ctx context.Context, w http.ResponseWriter, uid string) (MobileDevice, bool) {
	dev, found, err := m.cfg.Devices.Device(ctx, uid)
	if err != nil {
		m.unavailable(w, "the device node could not be read", err)
		return MobileDevice{}, false
	}
	if !found {
		if err := m.cfg.Refresh.Revoke(ctx, m.cfg.Project, uid); err != nil {
			m.log.Error("credsvc: a refresh family could not be removed after its device node was found gone", "error", err)
		}
		m.refuseMobile(w, http.StatusForbidden, mobilesession.CodeDeviceRevoked,
			"this device is not paired with this project, so the phone signs the project out")
		return MobileDevice{}, false
	}
	return dev, true
}

// possession checks the device key's signature over the token (C1's
// SignatureMessage, PurposeRefresh or PurposeRevokeSelf).
//
// A caller that fails it has proved nothing, so no audit line is written: the
// trail records the acts of verified callers, and writing this address's line
// for a signature its device never made would be recording a person for an act
// that was somebody else's. The service's own log carries it instead, with the
// uid and no other value.
func (m *MobileSession) possession(w http.ResponseWriter, uid string, dev MobileDevice, refreshToken, signature string, purpose mobilesession.SignaturePurpose) bool {
	key, err := mobileDecode(dev.DevicePublicKey, mobilesession.DevicePublicKeySize)
	if err != nil {
		m.unavailable(w, "the device node's public key cannot be read, so no signature can be checked against it", err)
		return false
	}
	message := mobilesession.SignatureMessage(purpose, refreshToken)
	if message == nil {
		m.unavailable(w, "the message this route signs is not one the contract carries", errors.New("credsvc: no signature message for this purpose"))
		return false
	}
	sig, err := mobileDecode(signature, mobilesession.DeviceSignatureSize)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), message, sig) {
		m.log.Warn("credsvc: a mobile session was asked with a signature the device key does not make", "device", uid, "purpose", string(purpose))
		m.refuseMobile(w, http.StatusForbidden, mobilesession.CodeDeviceRevoked,
			"the signature is not this device's over the token it presented, so this service will not act on it")
		return false
	}
	return true
}

// membership re-checks the node's address against the roster (O14) and answers
// the role this refresh must report and rewrite.
//
// An address with no grant left is the end of the session: the audit line is
// written first, then the family is revoked and the device node deleted (F32,
// O14), so the Firebase refresh token the app holds stops opening the database
// at the same moment.
func (m *MobileSession) membership(ctx context.Context, w http.ResponseWriter, uid string, dev MobileDevice) (access.Role, bool) {
	role, err := m.cfg.Roster.RoleAtProject(ctx, dev.Email)
	if err != nil {
		m.unavailable(w, "the roster could not be read, so this session's membership cannot be re-checked", err)
		return access.RoleNone, false
	}
	if role == access.RoleNone {
		if !m.auditOK(ctx, w, dev, PurposeMobileRefresh) {
			return access.RoleNone, false
		}
		if err := m.end(ctx, uid); err != nil {
			m.log.Error("credsvc: a session whose member is no longer on the roster could not be ended", "error", err)
		}
		m.refuseMobile(w, http.StatusForbidden, mobilesession.CodeDeviceRevoked,
			"this address is no longer on this project's roster, so the phone signs the project out")
		return access.RoleNone, false
	}
	return role, true
}

// sessionPolicy reads the published policy for the fields a phone's screens
// decide with (C1's SessionPolicy).
//
// A policy that cannot be read is an outage and not an answer: the phone would
// otherwise be told that this project has no costly modes and no biometric
// step, which is a screen that offers less than the project asks for. And a
// project that has published nothing is refused for the same reason — a session
// only exists where the pairing that made it read a policy (P3).
//
// mobile.enabled is deliberately NOT re-checked here. It is the switch that
// admits a PAIRING (F40, O9: the routes that answer mobile_disabled are the
// ones that make a session), and a session already made ends by the roster's
// answer above, by an admin's revoke, or by the deployment switch that serves
// these routes at all — never by a key whose absence a re-publish could produce
// under a phone that is holding a working token.
func (m *MobileSession) sessionPolicy(ctx context.Context, w http.ResponseWriter) (mobilesession.SessionPolicy, bool) {
	p, found, err := m.cfg.Policy.Shared(ctx)
	if err != nil {
		m.unavailable(w, "the published policy could not be read", err)
		return mobilesession.SessionPolicy{}, false
	}
	if !found {
		m.unavailable(w, "this project has published no policy, so a session cannot report what its phone's screens decide with", errors.New("credsvc: the project's published policy is absent"))
		return mobilesession.SessionPolicy{}, false
	}
	return mobileSessionPolicy(p), true
}

// rewriteRole writes the roster's role onto the device node when it moved
// (O14), and does nothing when it did not.
//
// The write is what keeps the node's copy from being a second source: the
// consumer resolves the role from the roster at every action anyway (D2791), so
// what this buys is that a CLI or a console reading the node sees the same role
// the roster would answer.
func (m *MobileSession) rewriteRole(ctx context.Context, w http.ResponseWriter, uid string, dev MobileDevice, role access.Role) bool {
	if dev.Role == string(role) {
		return true
	}
	if err := m.cfg.Devices.SetRole(ctx, uid, role); err != nil {
		m.unavailable(w, "the device node's role could not be rewritten", err)
		return false
	}
	return true
}

// mint asks the device-only signer for this uid's one-hour custom token (O8).
func (m *MobileSession) mint(ctx context.Context, w http.ResponseWriter, uid string, role access.Role) (mobilesession.CustomToken, bool) {
	custom, err := m.cfg.Mint.Mint(ctx, mobilesession.MintRequest{UID: uid, Role: role})
	if err != nil {
		m.unavailable(w, "the device token could not be minted, so no session was refreshed", err)
		return mobilesession.CustomToken{}, false
	}
	return custom, true
}

// auditOK writes the act's one line, before the act is made: an act this
// service cannot record must not be made (D143), and what is withheld when the
// line cannot be written is the issuance and the rotation — the token the
// caller holds is still live, and the phone retries.
//
// It is also the line a refusal AFTER this point does not get to replace: a
// path writes one line and never two, so a rotation that fails once the line is
// written is answered with the refusal and the service's own log, and the
// operator reads one act where there was one attempt.
func (m *MobileSession) auditOK(ctx context.Context, w http.ResponseWriter, dev MobileDevice, purpose string) bool {
	if err := m.cfg.Audit.Write(ctx, AuditEvent{Caller: dev.Email, Purpose: purpose, Status: StatusOK}); err != nil {
		m.log.Error("credsvc: a mobile act's audit line was not written", "error", err)
		m.refuseMobile(w, StatusUnavailable, "", "the act could not be recorded, so it was not made")
		return false
	}
	return true
}

// rotateRefused answers a rotation the store refused, and ends the session
// where the store's answer means the session is over.
//
// Every one of the store's refusals but contention means the family is gone or
// the token is not the one it holds, and the device node goes with it (F32): a
// node left standing after its family ended is a Firebase refresh token that
// still opens the database, which is exactly what the replay rule exists to
// stop. A failure that is the database's own is an outage instead, nothing is
// deleted, and the phone retries.
func (m *MobileSession) rotateRefused(ctx context.Context, w http.ResponseWriter, uid string, err error) {
	switch {
	case errors.Is(err, errMobileRefreshReplayed),
		errors.Is(err, errMobileRefreshLapsed),
		errors.Is(err, errMobileRefreshRevoked),
		errors.Is(err, errMobileRefreshStale),
		errors.Is(err, errMobileRefreshUnknown):
	default:
		m.unavailable(w, "the refresh token could not be rotated", err)
		return
	}
	if err := m.end(ctx, uid); err != nil {
		m.log.Error("credsvc: a device node could not be removed after its refresh family ended", "error", err)
	}
	m.refuseMobile(w, http.StatusForbidden, mobilesession.CodeDeviceRevoked,
		"this session's refresh token is no longer one this project honours, so the phone signs the project out")
}

// end revokes the family and deletes the device node, in that order: the family
// first, because a node deleted under a live family is a token that would
// refresh a device nothing admits, and the family first also means a failure
// between the two leaves a device whose next refresh finds no node and removes
// the family itself (device).
func (m *MobileSession) end(ctx context.Context, uid string) error {
	if err := m.cfg.Refresh.Revoke(ctx, m.cfg.Project, uid); err != nil {
		return err
	}
	return m.cfg.Devices.Delete(ctx, uid)
}

// unavailable answers a failure of this service's own: 503, an Error with an
// empty code — the app refuses a code it does not know rather than showing a
// screen for it — and the cause on the service's own log, where an operator
// reads it and a phone never does.
func (m *MobileSession) unavailable(w http.ResponseWriter, what string, err error) {
	m.log.Error("credsvc: a mobile session could not be served: "+what, "error", err)
	m.refuseMobile(w, StatusUnavailable, "", what)
}

// refuseMobile answers one refusal in the contract's shape. No message names a
// token, a key or a node: every one of them is read by whoever holds the phone.
func (m *MobileSession) refuseMobile(w http.ResponseWriter, status int, code mobilesession.Code, message string) {
	m.answer(w, status, mobilesession.Error{Code: code, Message: message})
}

// answer writes one JSON body with no-store, which is what every mobile answer
// is: nothing of it may be cached, by the phone or by anything between.
func (m *MobileSession) answer(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		m.log.Error("credsvc: a mobile answer could not be built", "error", err)
		status, body = StatusUnavailable, []byte(`{"message":"the answer could not be built"}`)
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// mobileDevicePath is one device node: the allowlist the rules read (D166) and
// the node a pairing writes (P3).
func mobileDevicePath(uid string) string { return rules.PathDevices + "/" + uid }

// checkMobileUID refuses a string that is not a device uid, so that no path of
// this service is built from one and no node is read or written by a name this
// tree did not mint (M4, O8).
func checkMobileUID(uid string) error {
	if !token.ValidDeviceUID(uid) {
		return fmt.Errorf("%w: %q is not a device uid, so no device node of this project is named by it", token.ErrDeviceUID, uid)
	}
	return nil
}

// mobileDecode reads one unpadded base64url value of exactly n bytes, the one
// spelling the contract gives a device key and a device signature
// (mobilesession.DevicePublicKeySize, DeviceSignatureSize). Strict refuses a
// padded spelling, the standard alphabet and the last characters that decode to
// the same bytes, so a value that reached here in a non-canonical spelling is a
// second name for one key and is refused rather than read.
func mobileDecode(s string, n int) ([]byte, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != n {
		return nil, fmt.Errorf("not one unpadded base64url string of %d bytes", n)
	}
	return raw, nil
}
