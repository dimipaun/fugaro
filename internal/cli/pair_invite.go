package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The invitation: `fugaro pair --email <somebody else> --role <role>` writes an
// invite for a named person instead of pairing a device now (P2, D2799). The
// phone's own node is written later, by the pairing endpoint (P3), when that
// person signs in and presents the invitation; this command's whole act is the
// invite node, the member record beside it and the QR the invited person
// scans.

const (
	// pairInviteRoot is where the invitations live: one node per address, under
	// config/admin, whose template carries .read: false, .write: false and
	// cascades to every child — so an invitation is IAM-only, written here with
	// the operator's own database admin and read by the pairing endpoint (P3)
	// with the database admin its service account holds.
	pairInviteRoot = "config/admin/invites"

	// pairMemberRoot is the member records' root (D2791 as amended by O14): one
	// record per address, created by an invitation and outliving every device of
	// that address. It carries the address alone — the role is the roster's and
	// provider/sub are the pairing endpoint's to record on first use (D2785) —
	// and this command creates it if it is absent and never overwrites it.
	pairMemberRoot = "config/admin/members"

	// pairConsumerKeyPath is the node `fugaro update` publishes the mailbox
	// consumer's public key at (J6), under the same IAM-only config/admin. The
	// invitation reads it for the fingerprint its QR carries (O16) and refuses
	// when it is absent rather than writing a code that pins nothing.
	pairConsumerKeyPath = "config/admin/mailbox_key"

	// pairInviteVersion and pairMemberVersion are the two nodes' own versions,
	// separate from the stream's and the QR's for pairQRVersion's reason: a
	// reader pins the node it reads, and a bump of one shape must not re-version
	// another.
	pairInviteVersion = 1
	pairMemberVersion = 1
	// pairConsumerKeyVersion is the version of the node J6 publishes.
	pairConsumerKeyVersion = 1

	// pairInviteLifetime is how long an invitation is good for (O4): ten
	// minutes, the window in which the invited person signs in and pairs.
	pairInviteLifetime = 10 * time.Minute

	// mobileServiceName is the leading host label of the mobile service (P5),
	// the second public service of the credentials image. It is named here
	// rather than imported because internal/infra has no constant for it yet,
	// exactly as intakeServiceName is named locally in secrets_intake.go.
	mobileServiceName = "fugaro-mobile"
)

// pairInvite is the invite node (C1's InviteStore, P3's read): the address, the
// role, the one-time nonce, the expiry and the accepted-provider list. The role
// is what D224 resolved at the write, so the endpoint reads it rather than
// resolving again; the nonce is consumed once by the endpoint, so a QR
// photographed and replayed is refused.
type pairInvite struct {
	V                 int      `json:"v"`
	Email             string   `json:"email"`
	Role              string   `json:"role"`
	Nonce             string   `json:"nonce"`
	ExpiresAt         string   `json:"expires_at"`
	AcceptedProviders []string `json:"accepted_providers"`
	At                string   `json:"at"`
}

// pairMemberRecord is the member record (D2791 as amended by O14): the address
// and when the invitation created it, and nothing else. It carries no role copy
// — the role is the roster's — and no provider or sub, which the pairing
// endpoint records on first use (D2785). This command creates it if absent and
// leaves it alone if it is there, so a second invitation cannot clear what a
// first pairing recorded.
type pairMemberRecord struct {
	V     int    `json:"v"`
	Email string `json:"email"`
	At    string `json:"at"`
}

// pairInviteQR is the invitation's QR payload (F4, O16): the endpoint, the
// address, the nonce and the consumer key's fingerprint, and no credential at
// all. The phone gets database_url and api_key from the service on success, so
// nothing in this code is worth photographing beyond the invitation itself —
// and the invitation is bound to one address by the endpoint's policy.
type pairInviteQR struct {
	V                      int    `json:"v"`
	Endpoint               string `json:"endpoint"`
	Email                  string `json:"email"`
	Nonce                  string `json:"nonce"`
	ConsumerKeyFingerprint string `json:"consumer_key_fingerprint"`
}

// pairConsumerKeyNode is what `fugaro update` publishes at pairConsumerKeyPath
// (J6): the mailbox consumer's public half, one unpadded base64url string of
// the 32 bytes an Ed25519 public key is.
type pairConsumerKeyNode struct {
	V         int    `json:"v"`
	PublicKey string `json:"public_key"`
}

// pairInviteRequest is what the invitation path needs from the pairing's own
// opening: who is calling and what they hold, the roster's grants, the address
// the invitation is for and the role --role named (RoleNone when it named
// none).
type pairInviteRequest struct {
	own     string
	ownRole mobileauth.Role
	grants  []accesslib.Grant
	email   string
	want    mobileauth.Role
}

// pairInviteProviders is the accepted-provider list an invitation carries
// (D2785): the defaults a project that narrowed nothing accepts — Google,
// verified email and password, Microsoft only with a tenant-verified email
// claim, Apple only with a real email. P6 publishes the project's own list and
// the pairing endpoint applies it; this is what the invitation writes until
// then, and the endpoint refuses a provider the published list does not name.
func pairInviteProviders() []string {
	return []string{"apple.com", "google.com", "microsoft.com", "password"}
}

// pairInviteNonce draws the invitation's one-time nonce (O4): 32 random bytes
// in the one spelling the QR and the node share, so the endpoint that has
// already consumed one refuses its replay.
func pairInviteNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("drawing the invitation's nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// pairMobileEndpoint derives the mobile service's URL from the credentials
// service's, the way intakeURLOf derives the intake's: one installation's
// services are Cloud Run hosts that differ in exactly their leading label. The
// QR's endpoint is where the phone goes to pair, and it is also the audience
// the assertion must carry (C1) — so deriving it here and nowhere else is what
// makes a relayed QR fail, since the endpoint the phone was told is the
// audience the assertion is checked against.
func pairMobileEndpoint(lc *localcfg.Config) (string, error) {
	if lc == nil || lc.CredentialsService == nil || lc.CredentialsService.URL == "" {
		return "", userErr("this installation has no credentials service in its local config, so there is no address to read the mobile endpoint out of, and this release deploys no mobile service to read one for: the mobile service, the pairing endpoint that consumes an invitation and the consumer key the QR pins all arrive in a later release, so no invitation can be sent yet")
	}
	u, err := url.Parse(lc.CredentialsService.URL)
	if err != nil || u.Host == "" {
		return "", userErr("credentials_service.url in this installation's local config is not a URL this CLI can read the mobile endpoint out of: run `fugaro update`, which writes it from the installation's own outputs")
	}
	host, ok := swapMobileServiceLabel(u.Host)
	if !ok {
		return "", userErr("credentials_service.url in this installation's local config is not a Cloud Run address of the shape the mobile endpoint is read from (fugaro-credentials-<hash>-<region>.a.run.app): run `fugaro update` to converge the installation, or set the block to the URL the installation's own outputs carry")
	}
	return u.Scheme + "://" + host, nil
}

// swapMobileServiceLabel replaces the credentials service's leading host label
// with the mobile service's, beside swapServiceLabel's intake swap and for the
// same reason: the label before the project hash is the service's name, so the
// two services of one installation differ in exactly that label.
func swapMobileServiceLabel(host string) (string, bool) {
	rest, ok := strings.CutPrefix(host, infra.CredentialsServiceName+"-")
	if !ok {
		return "", false
	}
	return mobileServiceName + "-" + rest, true
}

// pairConsumerKeyFingerprint is the value the QR carries so the phone can pin
// the consumer's public key (O16): the SHA-256 of the key's decoded bytes, in
// the one spelling EncodeKey uses, and not token.Fingerprint's eight
// human-typed characters — the pin has to make a forged key infeasible, and 40
// bits is a code for a person to compare, not a hash to check. The hash is over
// the bytes, so two spellings of one key are one fingerprint.
func pairConsumerKeyFingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// pairConsumerKey reads the consumer's public key `fugaro update` publishes
// (J6). Absent, or a version this build does not read, is refused rather than
// skipped: a QR without a fingerprint is a phone that pinned nothing and would
// accept whatever key a compromised service returned (O16).
//
// The bytes are decoded strictly and held to the 32 an Ed25519 public key is,
// and not through token.DecodeDeviceKey: that decoder enforces X25519's
// discarded top bit, which is a rule about a key-agreement key and says nothing
// about a signing key.
func pairConsumerKey(ctx context.Context, lc *localcfg.Config, db *rtdb.Client) ([]byte, error) {
	var node pairConsumerKeyNode
	found, err := db.Get(ctx, pairConsumerKeyPath, &node)
	if err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s refused the read: an invitation's QR carries the fingerprint of the mailbox consumer's public key, which `fugaro update` publishes there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w", pairConsumerKeyPath, lc.Name, err)}
		}
		return nil, dbErr(lc, err, false)
	}
	if !found || node.V != pairConsumerKeyVersion {
		return nil, userErr("this installation has published no mailbox consumer key at %s, and this release publishes none: the consumer key, the mobile service and the pairing endpoint arrive in a later release, so an invitation has no fingerprint to put in its QR yet (O16)", pairConsumerKeyPath)
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(node.PublicKey)
	if err != nil || len(key) != 32 {
		return nil, userErr("the consumer key published at %s is not one unpadded base64url string of the 32 bytes an Ed25519 public key is: run `fugaro update` to publish it again", pairConsumerKeyPath)
	}
	return key, nil
}

// pairAddressNodeKey is the node key an address is stored under: the invite
// node and the member record share it, so a second invitation for one person
// replaces the first rather than piling up, and a reader can find either from
// the address alone.
func pairAddressNodeKey(email string) (string, error) {
	key, err := mobileauth.EncodeNodeKey(email)
	if err != nil {
		return "", userErr("the address %s is not one this installation's node keys can hold (%v): an invitation is keyed by the address it is for, so an address this build cannot encode has no node to write", email, err)
	}
	return key, nil
}

// pairWriteMemberRecord creates the member record if it is absent and leaves it
// alone if it is there. The conditional write is access.MarkMigrated's pattern
// — read the ETag, write if the node has not moved — because the pairing
// endpoint records provider and sub on first use (D2785) and a second
// invitation must not clear what a first pairing recorded. A precondition
// failure is another writer having created the record first, which is the
// outcome this wants anyway.
func pairWriteMemberRecord(ctx context.Context, db *rtdb.Client, key, email, at string) error {
	path := pairMemberRoot + "/" + key
	etag, found, err := db.GetETag(ctx, path, nil)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if err := db.PutIfMatch(ctx, path, etag, pairMemberRecord{V: pairMemberVersion, Email: email, At: at}); err != nil && !errors.Is(err, rtdb.ErrPrecondition) {
		return err
	}
	return nil
}

// pairWriteInviteNode writes the invite node, replacing any earlier invitation
// for the same address: the node is keyed by the address, and a person holds
// one invitation at a time.
func pairWriteInviteNode(ctx context.Context, db *rtdb.Client, key string, invite pairInvite) error {
	return db.Patch(ctx, pairInviteRoot, map[string]any{key: invite})
}

// pairInviteWrote is the document's wrote list: the member record first, then
// the invite node, in the order the two writes land.
func pairInviteWrote(key string) []string {
	return []string{pairMemberRoot + "/" + key, pairInviteRoot + "/" + key}
}

// pairRunInvite is the invitation itself, reached when --email names somebody
// other than the caller. Its refusals are ordered so the operator is told the
// first thing that is wrong: the device flag that does not belong here, the
// role an invitation must carry, the admin the act needs, the roster grant the
// address must hold, the role that must not exceed it, D224's own resolution,
// and only then the endpoint and the consumer key the QR needs.
//
// It writes nothing until every refusal has been answered, and it waits for
// nothing and asks nothing: no terminal, no stdin, no clock.
func pairRunInvite(ctx context.Context, cmd *cobra.Command, o *pairOptions, ev *pairEventWriter, lc *localcfg.Config, db *rtdb.Client, req pairInviteRequest, now time.Time) error {
	// --name names a device, and an invitation is not one: the phone's device
	// node is written when it pairs (P3). The flag is kept for the bootstrap
	// pairing's sake and a value for it is refused rather than dropped.
	if strings.TrimSpace(o.name) != "" {
		return pairRefuse(ev, o, pairRefusedInviteName,
			fmt.Errorf("--name %s names a device, and --email %s is an invitation: drop --name", o.name, req.email))
	}
	// D2799: the role is refused rather than defaulted for another person.
	// D226's default is the caller's own role, which is a role for the caller's
	// own device; an invitation is a role for somebody else's, and nothing about
	// the caller bounds it — the roster does (O14) — so there is no default to
	// fall back to.
	if req.want == mobileauth.RoleNone {
		return pairRefuse(ev, o, pairRefusedInviteRole,
			fmt.Errorf("--email %s invites somebody else and needs a role: run `fugaro pair --email %s --role viewer` (roles: %s)", req.email, req.email, pairRoleNames()))
	}
	// D2799 lets AN ADMIN name another member's email. An operator naming one is
	// refused rather than invited, because the invitation writes a role for
	// somebody else into the installation's own admin tree.
	if !req.ownRole.AtLeast(accesslib.RoleAdmin) {
		return pairRefuse(ev, o, pairRefusedInviteAdmin,
			fmt.Errorf("--email %s invites another member, which is an admin's act (you hold %s in %s): ask an admin, or pair your own address (%s)", req.email, req.ownRole, lc.Name, req.own))
	}
	// O14: membership and role are the roster's. The address must have a grant
	// at project scope — a device node records a role and no scope, so a
	// project-scope grant is the only one a device can carry — and the role
	// --role names may lower the roster's and never raise it, the roster being
	// the ceiling exactly as the caller's own role is the ceiling for the
	// caller's own device.
	member, err := accesslib.Member(req.email)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedInviteMember,
			fmt.Errorf("--email %s is not an address this installation's roster can name (%v): membership is the roster's (%s), so an invitation is for a member it grants, and an address no member can be built from is one it never will", req.email, err, accesslib.PathNode))
	}
	rosterRole := accesslib.EffectiveAtProject(accesslib.Input{Grants: req.grants}, member)
	if rosterRole == mobileauth.RoleNone {
		return pairRefuse(ev, o, pairRefusedInviteMember,
			fmt.Errorf("the roster grants %s no role at project scope: ask an admin of %s to run '%s user grant %s --role viewer --scope project'", req.email, lc.Name, selfCommand(), req.email))
	}
	if !rosterRole.AtLeast(req.want) {
		return pairRefuse(ev, o, pairRefusedInviteAbove,
			fmt.Errorf("--role %s is above the %s the roster grants %s: name %s or lower, or run '%s user grant %s --role %s --scope project'", req.want, rosterRole, req.email, rosterRole, selfCommand(), req.email, req.want))
	}

	// D224, and this is the place the resolution runs, exactly as it does for a
	// pairing: once, in the CLI, at the write. The invitation writes an entry
	// like any other — a device node keyed by the phone's uid (D222) — so the
	// role the entry resolves to has to be settled here too, and both of D224's
	// refusals stay reachable.
	role, err := pairResolveEntryRole(ctx, lc, db, req.email, req.ownRole, req.want)
	switch {
	case err == nil:
	case errors.Is(err, errPairResolvedAbove):
		return pairRefuse(ev, o, pairRefusedRoleAbove, err)
	case errors.Is(err, errPairRoleDisagrees):
		return pairRefuse(ev, o, pairRefusedRoleDisagrees, err)
	case errors.Is(err, mobileauth.ErrAmbiguous):
		return pairRefuse(ev, o, pairRefusedAllowlistAmbiguous, err)
	default:
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedAllowlistUnreadable), err)
	}

	endpoint, err := pairMobileEndpoint(lc)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedInviteEndpoint, err)
	}
	consumerKey, err := pairConsumerKey(ctx, lc, db)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedInviteKey), err)
	}
	nodeKey, err := pairAddressNodeKey(req.email)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedInviteWrite, err)
	}
	nonce, err := pairInviteNonce()
	if err != nil {
		return pairRefuse(ev, o, pairRefusedInviteWrite, err)
	}

	at := now.UTC().Format(time.RFC3339)
	expiresAt := now.Add(pairInviteLifetime).UTC().Format(time.RFC3339)
	// The member record first, then the invite node: the record is the person's
	// and outlives the invitation, so an invitation whose invite write fails
	// leaves a member the roster already names rather than an invite for nobody.
	if err := pairWriteMemberRecord(ctx, db, nodeKey, req.email, at); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				fmt.Errorf("the write to %s was refused: an invitation creates the member record there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w", pairMemberRoot, lc.Name, err))
		}
		cause := dbErr(lc, err, true)
		return pairRefuse(ev, o, pairRefusalCode(cause, pairRefusedInviteWrite), cause)
	}
	invite := pairInvite{
		V:                 pairInviteVersion,
		Email:             req.email,
		Role:              string(role),
		Nonce:             nonce,
		ExpiresAt:         expiresAt,
		AcceptedProviders: pairInviteProviders(),
		At:                at,
	}
	if err := pairWriteInviteNode(ctx, db, nodeKey, invite); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				fmt.Errorf("the write to %s was refused: an invitation writes the invite node there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w", pairInviteRoot, lc.Name, err))
		}
		cause := dbErr(lc, err, true)
		return pairRefuse(ev, o, pairRefusalCode(cause, pairRefusedInviteWrite), cause)
	}

	fingerprint := pairConsumerKeyFingerprint(consumerKey)
	payload, err := json.Marshal(pairInviteQR{
		V:                      pairQRVersion,
		Endpoint:               endpoint,
		Email:                  req.email,
		Nonce:                  nonce,
		ConsumerKeyFingerprint: fingerprint,
	})
	if err != nil {
		return userErr("building the invitation's QR payload: %v", err)
	}

	out := cmd.OutOrStdout()
	if o.asJSON {
		return ev.emit(pairEvent{Event: pairEventInvited, Payload: string(payload), ExpiresAt: expiresAt, Invite: &pairInviteDocument{
			Project:                lc.Name,
			At:                     at,
			Email:                  req.email,
			Role:                   string(role),
			Endpoint:               endpoint,
			ExpiresAt:              expiresAt,
			AcceptedProviders:      pairInviteProviders(),
			ConsumerKeyFingerprint: fingerprint,
			Wrote:                  pairInviteWrote(nodeKey),
			Refused:                []string{},
			Warnings:               []string{},
		}})
	}
	fmt.Fprintf(out, "invited %s: role %s, expires at %s\n", req.email, role, expiresAt)
	if err := renderPairQR(out, string(payload)); err != nil {
		return err
	}
	fmt.Fprintf(out, "the invited person pairs by signing in as %s at %s and presenting this code; it carries no credential — the app gets the database URL and the API key from the service on success\n", req.email, endpoint)
	return nil
}
