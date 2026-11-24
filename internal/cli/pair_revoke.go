package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// `fugaro pair revoke` (plan §4.3, M4), and the two removals it shares with the
// rest of the mobile work: ending one device's whole footprint, which this
// command and P9's `fugaro user revoke` both perform, and removing a member
// (the record, every device of the address and every refresh family), which is
// P9's act and is exported here rather than reached by a flag of this command
// (O14's "no command of its own removes a member").

type pairRevokeOptions struct {
	cloudOptions
	uid, name string
	asJSON    bool
}

func newPairRevokeCmd() *cobra.Command {
	o := &pairRevokeOptions{}
	cmd := &cobra.Command{
		Use:   "revoke (--uid UID | --name NAME)",
		Short: "Revoke a paired device",
		Long: "Revoke a paired device: the phone is off this installation's allowlist from\n" +
			"its next request on.\n" +
			"\n" +
			"The order is the command (M4): the device node under config/admin/devices\n" +
			"goes first, because the node's existence is what the rules read and its\n" +
			"deletion revokes at once, without waiting for an ID token to expire; then\n" +
			"the device's refresh family under config/admin/refresh, because the node\n" +
			"ends the phone's database reads while the family is the session it holds,\n" +
			"so a stolen phone loses that session at its next refresh and cannot refresh\n" +
			"again (D2794); then the account's push node under config/admin/push, the\n" +
			"sibling that holds\n" +
			"the one registration this installation made for it, so the phone's token\n" +
			"goes with the pairing rather than outliving it (push design §3.4); then,\n" +
			"where the installation has a resident orchestrator, the installation's own\n" +
			"cloud copy of the pairing's MAC key, which is the mobile design's\n" +
			"revocation step 2b: its version is destroyed rather than rotated or\n" +
			"disabled, so the pairing's messages stop verifying in the cloud as well as\n" +
			"on this laptop; then this laptop's OS keychain item for that pairing's MAC\n" +
			"key. Every deletion this command can make is here, in that order.\n" +
			"\n" +
			"Deleting the cloud copy un-reads nothing: whoever held the key keeps it, a\n" +
			"project Owner or Editor among them, and what the step ends is the\n" +
			"installation's own service's ability to verify a message and to forge one.\n" +
			"\n" +
			"The member record is not deleted here. Revoking a DEVICE must not hand the\n" +
			"address back to whoever signs in with it next (F34), so the record stays and\n" +
			"the next pairing must match its provider and sub; removing a MEMBER is\n" +
			"`fugaro user revoke`'s act, which deletes the record, every device node of\n" +
			"the address and every refresh family through the same removal this command\n" +
			"uses per device (O14).\n" +
			"\n" +
			"The pairing session's own node is not one of them. Its key is the p~ uid\n" +
			"the session drew, and nothing on this laptop records that uid, so a revoke\n" +
			"by device cannot name the node it would delete; the history sweep deletes\n" +
			"it with the p~ user the node belongs to (mobile plan decision 2), and it\n" +
			"is the sweep this command names rather than a node it did not touch.\n" +
			"\n" +
			"It does not delete the device's Auth user. No role a Terraform grant gives\n" +
			"an operator can delete one (the history job's account is the only holder of\n" +
			"roles/firebaseauth.admin), and the device node's deletion is what makes the\n" +
			"orphan the history sweep's to remove — which is why this command names the\n" +
			"sweep rather than pretending the account is gone.\n" +
			"\n" +
			"Exactly one of --uid and --name selects the device, and a --name that\n" +
			"matches more than one node is refused rather than revoked ambiguously: the\n" +
			"refusal says how many it matched, and `fugaro pair ls` prints the uids to\n" +
			"pick one from.\n" +
			"\n" +
			"Its own state is the local config, as it is for the rest of the command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPairRevoke(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.uid, "uid", "", "the uid of the device to revoke (m~…), which is the node's key")
	f.StringVar(&o.name, "name", "", "the name of the device to revoke; it must match exactly one device")
	f.BoolVar(&o.asJSON, "json", false, "print the revocation as one JSON event on stdout")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

func runPairRevoke(cmd *cobra.Command, o *pairRevokeOptions) error {
	ctx := cmd.Context()
	ev := &pairEventWriter{w: cmd.OutOrStdout(), now: pairNow}
	switch {
	case o.uid == "" && o.name == "":
		return pairRefuseIn(ev, o.asJSON, pairRefusedNoSelector,
			errors.New("revoke needs the device to revoke: --uid with its uid, or --name with a name that matches exactly one device; `fugaro pair ls` prints both"))
	case o.uid != "" && o.name != "":
		return pairRefuseIn(ev, o.asJSON, pairRefusedTwoSelectors,
			fmt.Errorf("revoke was given --uid %s and --name %s: a device is revoked by one of them, and two selectors could name two devices, so revoke refuses rather than guess which was meant", o.uid, o.name))
	}
	lc, db, err := pairOpen(cmd, o.cloudOptions, ev, o.asJSON, pairRefusedDatabaseAdmin)
	if err != nil {
		return err
	}
	rows, err := pairListDevices(ctx, lc, db)
	if err != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(err, pairRefusedUnreachable), err)
	}
	device, code, err := pairSelectDevice(rows, o, lc)
	if err != nil {
		return pairRefuseIn(ev, o.asJSON, code, err)
	}

	fp := pairDeleteDeviceFootprint(ctx, lc, db, pairDatabaseRefreshStore{db: db}, device.UID)

	// The device node's own failure stops the act: nothing below it has happened
	// yet, so there is no partial deletion to report and the refusal is the
	// node's own.
	if fp.NodeErr != nil {
		if errors.Is(fp.NodeErr, rtdb.ErrPermission) {
			return pairRefuseIn(ev, o.asJSON, pairRefusedDatabaseAdmin,
				fmt.Errorf("the deletion of the device node %s was refused: revoking a device deletes it with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w",
					rules.PathDevices+"/"+device.UID, lc.Name, fp.NodeErr))
		}
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(fp.NodeErr, pairRefusedUnreachable), dbErr(lc, fp.NodeErr, true))
	}

	// The keychain item's name is read after the nodes are gone, exactly as it
	// was before this act reached the refresh family: a name the keychain takes
	// is one thing, and a name this build cannot parse is the operator's to
	// delete by hand, which is what the refusal below says.
	if fp.ItemErr != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusedKeychain,
			fmt.Errorf("the device is off the allowlist, but the keychain item this pairing's MAC key lives at (%s) is not a name a key is stored under: %v; the item's name carries the project's own name (M5), so it is not derivable here and must be deleted by hand%s", pairItemName(lc.Name, device.UID), fp.ItemErr, pairPushLeftover(fp.PushErr, device.UID)+pairCloudLeftover(fp.CopyErr, lc)+pairFamilyLeftover(fp.FamilyErr, device.UID)))
	}
	if fp.KeyErr != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusedKeychainDelete,
			fmt.Errorf("the device node %s is deleted and the phone is off the allowlist, but this laptop's MAC key for the pairing could not be removed from the OS keychain at %s: %w; the key is still there and must be deleted by hand%s", fp.Node, fp.Item, fp.KeyErr, pairPushLeftover(fp.PushErr, device.UID)+pairCloudLeftover(fp.CopyErr, lc)+pairFamilyLeftover(fp.FamilyErr, device.UID)))
	}
	if fp.PushErr != nil {
		if errors.Is(fp.PushErr, rtdb.ErrPermission) {
			return pairRefuseIn(ev, o.asJSON, pairRefusedDatabaseAdmin,
				fmt.Errorf("the device node %s is deleted and this laptop's MAC key is gone, but the deletion of its push node %s was refused: revoking a device deletes both nodes with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w%s",
					fp.Node, pairPushPath(device.UID), lc.Name, fp.PushErr, pairCloudLeftover(fp.CopyErr, lc)+pairFamilyLeftover(fp.FamilyErr, device.UID)))
		}
		cause := dbErr(lc, fp.PushErr, true)
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(cause, pairRefusedUnreachable),
			fmt.Errorf("%w; the device node %s is deleted and this laptop's MAC key is gone, so the phone is off the allowlist and the sender — which sends only when both nodes exist (§3.4 rule 2) — delivers nothing to it, but the push node %s could not be deleted and still holds the installation's registration for that account: the token is there and must be deleted by hand%s",
				cause, fp.Node, pairPushPath(device.UID), pairCloudLeftover(fp.CopyErr, lc)+pairFamilyLeftover(fp.FamilyErr, device.UID)))
	}
	// The refresh family's own failure, reported after the push node's because the
	// node is the read the rules make and the family is the session: a revoke that
	// ended the reads but not the session is a phone that cannot reach the
	// database and can still refresh, which is what D2794 exists to prevent.
	if fp.FamilyErr != nil {
		if errors.Is(fp.FamilyErr, rtdb.ErrPermission) {
			return pairRefuseIn(ev, o.asJSON, pairRefusedDatabaseAdmin,
				fmt.Errorf("the device node %s is deleted and this laptop's MAC key is gone, but the deletion of its refresh family %s was refused: revoking a device ends its family there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w%s",
					fp.Node, pairRefreshFamilyPath(device.UID), lc.Name, fp.FamilyErr, pairCloudLeftover(fp.CopyErr, lc)))
		}
		cause := dbErr(lc, fp.FamilyErr, true)
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(cause, pairRefusedUnreachable),
			fmt.Errorf("%w; the device node %s is deleted and the phone is off the allowlist, but the device's refresh family %s could not be ended, so a refresh token the phone still holds keeps rotating until the family is deleted by hand: the node's deletion ends the database reads, and the family is the session", cause, fp.Node, pairRefreshFamilyPath(device.UID)))
	}
	// The third copy's own failure, reported after the push node's for the reason
	// the two deletions are ordered as they are: the node is the read the rules
	// make, and this is the copy that keeps a revoked pairing's messages verifying
	// in the cloud. The refusal says what did land — the node, the allowlist and
	// the laptop's key — and what did not, and the fix is the operator's because
	// no command of this CLI can reach a container the installation's own
	// credential cannot write.
	if fp.CopyErr != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(fp.CopyErr, pairRefusedCloudKeyDelete),
			fmt.Errorf("%w; the device node %s is deleted, this laptop's MAC key is gone and the phone is off the allowlist, but the installation's own cloud copy of the pairing's key (%s) could not be destroyed, so the pairing's messages keep verifying in the cloud until its version is destroyed by hand — a destroy rather than a disable, because disabling is not enough while the service is granted versions.access on the parent (the design's step 2b)%s",
				fp.CopyErr, fp.Node, pairCloudCopyName(lc), pairPushLeftover(fp.PushErr, device.UID)))
	}

	// The pairing session's own node is the third thing §4.3 names, and it is
	// deliberately not deleted here: its key is the p~ uid MintPairing drew,
	// and nothing on this laptop records it — the device node holds the name,
	// the administered email, the role and the pairing time and no pairing uid
	// (M13), and the local config holds the keychain item's name, not the
	// session's. So a revoke by device cannot name the node it would delete,
	// and it is the sweep's: it deletes pairing/<uid> when it deletes the p~
	// user the node belongs to (decision 2, internal/budget/sweep.go). The
	// command says so rather than reporting a node it did not touch.

	if o.asJSON {
		return ev.emit(pairEvent{Event: pairEventRevoked, Revoke: &pairRevokeDocument{
			Project:      lc.Name,
			At:           pairNow().UTC().Format(time.RFC3339),
			Device:       pairDevice{UID: device.UID, Name: device.Name, Email: device.Email, Role: device.Role},
			KeychainItem: fp.Item,
			Wrote:        fp.Wrote,
			Refused:      []string{},
			Warnings:     []string{},
		}})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "revoked %s", pairCell(device.UID))
	if device.Name != "" {
		fmt.Fprintf(out, " (%s)", pairCell(device.Name))
	}
	fmt.Fprintf(out, ": deleted %s, so the phone is off %s's allowlist from its next request on\n", fp.Node, lc.Name)
	fmt.Fprintf(out, "ended the device's refresh family %s with it, so its session is revoked and a token it still holds cannot refresh again\n", fp.Family)
	fmt.Fprintf(out, "deleted its push node %s with it, so the registration this installation made for that account — its token, its platform and its preferences — is gone with the pairing\n", fp.Push)
	if fp.Destroyed > 0 {
		fmt.Fprintf(out, "destroyed the installation's own cloud copy of the pairing's key at %s, so the pairing's messages stop verifying in the cloud as well as on this laptop\n", pairCloudCopyName(lc))
	}
	fmt.Fprintf(out, "deleted this laptop's OS keychain item %s\n", fp.Item)
	fmt.Fprintf(out, "the %s Auth user is not deleted here — no role a Terraform grant gives an operator can delete one — and the history sweep removes it now that its device node is gone; an abandoned pairing node, if this pairing left one, is what the same sweep removes with it\n", device.UID)
	return nil
}

// pairSelectDevice resolves the one device a revoke names. --uid is the node's
// own key, so it either is a device or is not one. A name is not unique: the
// node's children are unconstrained and two hands may have paired two phones
// under one name, and a revoke that picked one of them would revoke a device
// the operator did not mean. It is refused by class and count instead — echoing
// neither the typed selector nor the node's uids — which is what makes
// `pair ls` the step before it.
func pairSelectDevice(rows []pairDeviceRow, o *pairRevokeOptions, lc *localcfg.Config) (pairDeviceRow, string, error) {
	if o.uid != "" {
		for _, r := range rows {
			if r.UID == o.uid {
				return r, "", nil
			}
		}
		return pairDeviceRow{}, pairRefusedNoPairing,
			fmt.Errorf("no device node %s/%s exists: the uid is the node's key, and `fugaro pair ls` prints the uids %s holds", rules.PathDevices, o.uid, lc.Name)
	}
	var named []pairDeviceRow
	for _, r := range rows {
		if r.Name == o.name {
			named = append(named, r)
		}
	}
	switch len(named) {
	case 1:
		return named[0], "", nil
	case 0:
		return pairDeviceRow{}, pairRefusedNoPairing,
			fmt.Errorf("no device paired with %s is named %q: --name matches the device node's own name field exactly, and `fugaro pair ls` prints the names %s holds", lc.Name, o.name, lc.Name)
	default:
		// The typed selector is not echoed and neither are the uids: --name's
		// value is the operator's own input and the uids come out of a node
		// nobody constrained, and this sentence is printed where `pair ls`'s
		// own, sanitised listing is the place to read them. The refusal names
		// itself and how many pairings it matched, and nothing else.
		return pairDeviceRow{}, pairRefusedTwoPairings,
			fmt.Errorf("revoke matched %d devices by name: revoke needs one, so name it by uid with --uid, which `fugaro pair ls` prints", len(named))
	}
}

// pairRefreshRoot is where a project's refresh families live (P4's store, D2783,
// D2794): one node per device, keyed by the device's uid, because the contract
// fixes one family per device and `RefreshStore.Issue` is asked for a family by
// uid (C1). It is under config/admin — where the device allowlist, the invites,
// the member records and the push registrations already live and where no
// client token may read or write anything — so `fugaro pair revoke` ends a
// family with the database admin it holds and needs no phone and no service in
// the loop, which is the whole reason C1 keeps the store and the route apart.
//
// The path is spelled here rather than beside rules.PathDevices for the same
// reason pairPushRoot's is: no rule names it, and there it would read as a node
// the rules read, which is exactly what it is not. The node's own shape is
// P4's to write and no shape is fixed here: this command only ever deletes it.
const pairRefreshRoot = "config/admin/refresh"

// pairRefreshFamilyPath is one device's refresh family. The key is the DEVICE's
// uid, which is what makes the family one device's: the contract fixes one
// family per device, the pairing minted the uid the device node is keyed by,
// and the service starts the family with the same uid (P4). A revoke by device
// therefore names the family it ends from the uid alone.
func pairRefreshFamilyPath(deviceUID string) string { return pairRefreshRoot + "/" + deviceUID }

// pairDatabaseRefreshStore is C1's RefreshStore over this installation's own
// database (mobilesession.RefreshStore), and it is the half an admin act needs:
// it ends a family. It is the CLI's own store because the two callers that
// revoke outside a phone — `fugaro pair revoke` and P9's `fugaro user revoke` —
// hold database admin and no phone, which is exactly the case C1's comment on
// RefreshStore names.
//
// Issue and Rotate refuse, and deliberately: what a phone presents a token to
// is the mobile service, so issuing and rotating a family are P4's store's, and
// a CLI that could mint or rotate would be a second place a session is made
// with rules to keep in step. This store holds no token either: the family node
// P4 writes is opaque here, and this command only deletes it.
type pairDatabaseRefreshStore struct{ db *rtdb.Client }

// pairDatabaseRefreshStore is a RefreshStore, so P4's own implementation is a
// drop-in the day the sides meet (I1): the admin revoke is held to the same
// interface the service is.
var _ mobilesession.RefreshStore = pairDatabaseRefreshStore{}

func (s pairDatabaseRefreshStore) Revoke(ctx context.Context, _, family string) error {
	if family == "" {
		return errors.New("a refresh family to revoke is empty: a device's family is keyed by its uid, and a revoke with no key would delete the store's whole root rather than one device's session")
	}
	return s.db.Patch(ctx, pairRefreshRoot, map[string]any{family: nil})
}

func (pairDatabaseRefreshStore) Issue(context.Context, string, string) (mobilesession.RefreshGrant, error) {
	return mobilesession.RefreshGrant{}, errors.New("issuing a refresh family is the mobile service's (P4): the CLI ends a device's session and never mints one, so this store has no Issue")
}

func (pairDatabaseRefreshStore) Rotate(context.Context, string, string) (mobilesession.RefreshGrant, error) {
	return mobilesession.RefreshGrant{}, errors.New("rotating a refresh token is the mobile service's (P4): a phone presents a token to the service and never to the CLI, so this store has no Rotate")
}

// pairDeviceFootprint is one device's whole footprint as an admin act deletes
// it, and the failures each step had. Every step is attempted and its failure
// remembered rather than returned on, because the deletions after it are this
// laptop's own — the keychain item is the one thing no second revoke could take
// away, since the device node is gone by then and a second `fugaro pair revoke`
// is refused as nothing to revoke — so what a caller is owed is a refusal that
// says what landed and what did not, not a step that stopped early.
//
// The one exception is the device node itself: nothing below it has happened
// when it fails, so its failure is the whole of the act and NodeErr is the
// caller's to answer with.
type pairDeviceFootprint struct {
	UID string
	// Node, Family, Push and Item are the paths the act deleted, and they are
	// fields rather than positions in Wrote because the refusals name them one
	// at a time.
	Node   string
	Family string
	Push   string
	Item   string
	// ItemName is the keychain item the pairing's key lives at, which is known
	// before the name is checked against the keychain's own rules: a name this
	// build cannot parse is still a name to report, and it is the only one a
	// caller can be told to delete — Item is empty in exactly that case.
	ItemName  string
	Destroyed int
	// Wrote is every path deleted, in the order it was deleted: the same order
	// the document reports (M4).
	Wrote     []string
	NodeErr   error
	FamilyErr error
	PushErr   error
	CopyErr   error
	ItemErr   error
	KeyErr    error
}

// pairDeleteDeviceFootprint deletes one device's whole footprint, in the order
// the command's own help fixes (M4): the device node, the refresh family it
// holds, the push node beside it, the installation's cloud copy of the pairing
// key where the installation has one, and this laptop's OS keychain item.
//
// It is one function because two commands delete a device: `fugaro pair revoke`
// for the one device an operator names, and `fugaro user revoke`'s member
// removal (P7's pairRemoveMember) for every device of an address. A second copy
// of the order would be a second place it could drift.
func pairDeleteDeviceFootprint(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, refresh mobilesession.RefreshStore, deviceUID string) pairDeviceFootprint {
	fp := pairDeviceFootprint{UID: deviceUID}

	// M4, and the order is the command: the node first. It is what the rules
	// read in userClaim, so deleting it revokes at once — every family USER
	// reaches is refused from the next request on — while the steps below it are
	// the phone's session, the sender's registration, the cloud's copy of the
	// key and this laptop's own item.
	if err := db.Patch(ctx, rules.PathDevices, map[string]any{deviceUID: nil}); err != nil {
		fp.NodeErr = err
		return fp
	}
	fp.Node = rules.PathDevices + "/" + deviceUID
	fp.Wrote = append(fp.Wrote, fp.Node)

	// The device's session, ended beside the node that admits it: the node's
	// deletion ends the database reads, and the family is what a refresh token
	// the phone still holds is rotated against (D2794). It goes through C1's
	// RefreshStore, so the rules a family has — rotation, replay, idle expiry —
	// stay where P4 keeps them and this act only ends one.
	familyErr := refresh.Revoke(ctx, lc.Name, deviceUID)
	if familyErr == nil {
		fp.Family = pairRefreshFamilyPath(deviceUID)
		fp.Wrote = append(fp.Wrote, fp.Family)
	}
	fp.FamilyErr = familyErr

	// §3.4's sibling, deleted in the same act: the whole node goes, so the
	// registration's token, platform, prefs and the sender's ledger go with the
	// pairing rather than outliving it on a timer (§7, Q10(i)). The device node
	// is already gone, so this deletion is what stops the delivery rather than
	// what stops the reads — the sender's condition is both nodes (§3.4 rule 2)
	// — and a device that never registered holds no node at all, whose deletion
	// is the no-op the database makes it.
	if err := db.Patch(ctx, pairPushRoot, map[string]any{deviceUID: nil}); err != nil {
		fp.PushErr = err
	} else {
		fp.Push = pairPushPath(deviceUID)
		fp.Wrote = append(fp.Wrote, fp.Push)
	}

	// The design's step 2b, and its place in the order: the cloud copy of the
	// pairing's key goes after the nodes and before the laptop's item, which is
	// the order §4.7 numbers — the Auth user's deletion is step 2 and is not this
	// command's (the sweep's), so what follows here is 2b, then the item.
	destroyed, copyErr := pairDeleteCloudCopy(ctx, lc, deviceUID)
	if copyErr == nil && destroyed > 0 {
		fp.Destroyed = destroyed
		fp.Wrote = append(fp.Wrote, pairCloudCopyName(lc))
	}
	fp.CopyErr = copyErr

	fp.ItemName = pairItemName(lc.Name, deviceUID)
	item, err := keychain.ParseItem(fp.ItemName, lc.Name)
	if err != nil {
		fp.ItemErr = err
		return fp
	}
	fp.Item = item.Name()
	if err := pairKeychain().Delete(ctx, item); err != nil {
		fp.KeyErr = err
	}
	return fp
}

// pairFamilyLeftover is pairPushLeftover's sibling for the family: a refusal
// that named one leftover and not the other would leave a session nobody was
// told about, and this is the one that keeps a stolen phone refreshing after
// its device node is gone. It names the path, because that is what the operator
// has to delete.
func pairFamilyLeftover(err error, deviceUID string) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("; the device's refresh family %s could not be ended either (%v), so a token the phone still holds keeps rotating and must be deleted by hand", pairRefreshFamilyPath(deviceUID), oneLine(err.Error()))
}

// pairMemberRemoval is what removing a member deleted: the record, every device
// node of the address and, with each device, its refresh family, its push node,
// the installation's cloud copy of its pairing key and this laptop's keychain
// item. It is a summary and not a refusal stream: the command that asks for it
// (P9's `fugaro user revoke`) reports it its own way.
type pairMemberRemoval struct {
	Email   string
	Member  string
	Devices []string
	Wrote   []string
	Failed  []string
}

// pairRemoveMember is the removal O14 gives P9's `fugaro user revoke` when a
// person's last roster grant goes: it deletes the member record, every device
// node of the address, and with each device its refresh family — the record,
// the devices and the families O14 names, so that the address holds no phone
// that can reach the project and the next pairing of it starts from nothing.
//
// It is a function and not a flag of `fugaro pair revoke` because the two acts
// are different: revoking a DEVICE keeps the member record — a stolen phone
// must not hand the address back to whoever signs in with it next (F34) — while
// removing a MEMBER is `fugaro user revoke`'s, and only there. That is why this
// takes the address and not a uid, and why no flag reaches it.
//
// Every step is attempted and the failures are collected rather than returned
// on: a permission the credential lacks is the same permission for every device
// and for the record, so stopping at the first would report one of the things
// left behind and not the rest. The returned error says the removal did not
// complete and carries the list; the removal itself is best-effort, because the
// roster grant P9 revoked is what already refuses the address at the next
// refresh.
func pairRemoveMember(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, email string) (pairMemberRemoval, error) {
	out := pairMemberRemoval{Email: email}
	key, err := pairAddressNodeKey(email)
	if err != nil {
		return out, err
	}
	rows, err := pairListDevices(ctx, lc, db)
	if err != nil {
		return out, err
	}

	// The address's own devices, compared on canonical forms: a node written in
	// another spelling of the same address is still that address's, which is
	// what stops a hand-edited node from surviving the removal of its member.
	want, err := mobileauth.NormalizeEmail(email)
	if err != nil {
		return out, userErr("the address %s is not one this installation's node keys can hold (%v): a member removal is keyed by the address it names", email, err)
	}
	for _, r := range rows {
		got, err := mobileauth.NormalizeEmail(r.Email)
		if err != nil || got != want {
			continue
		}
		fp := pairDeleteDeviceFootprint(ctx, lc, db, pairDatabaseRefreshStore{db: db}, r.UID)
		out.Devices = append(out.Devices, r.UID)
		out.Wrote = append(out.Wrote, fp.Wrote...)
		out.Failed = append(out.Failed, pairFootprintFailures(fp)...)
	}

	// The record last, and by its own key: the devices above are found by the
	// address the record carries, so a record deleted first would leave the
	// devices it named unreadable to this function. The key is the address's own
	// (pairAddressNodeKey, the one writer's rule), so a caller passes the address
	// as the invitation named it; the devices are matched on canonical forms
	// because their email is what whoever paired wrote there.
	path := pairMemberRoot + "/" + key
	if err := db.Patch(ctx, pairMemberRoot, map[string]any{key: nil}); err != nil {
		out.Failed = append(out.Failed, fmt.Sprintf("the member record %s could not be deleted: %s", path, oneLine(err.Error())))
	} else {
		out.Member = path
		out.Wrote = append(out.Wrote, path)
	}

	if len(out.Failed) > 0 {
		return out, fmt.Errorf("the member removal did not complete: %s", strings.Join(out.Failed, "; "))
	}
	return out, nil
}

// pairFootprintFailures is one device's remembered failures as the sentences a
// caller reports: one line per step that did not land, each naming the path it
// could not delete, and never a credential — there is none in any of them.
func pairFootprintFailures(fp pairDeviceFootprint) []string {
	var failed []string
	if fp.NodeErr != nil {
		failed = append(failed, fmt.Sprintf("the device node %s could not be deleted: %s", rules.PathDevices+"/"+fp.UID, oneLine(fp.NodeErr.Error())))
	}
	if fp.FamilyErr != nil {
		failed = append(failed, fmt.Sprintf("the refresh family %s could not be ended: %s", pairRefreshFamilyPath(fp.UID), oneLine(fp.FamilyErr.Error())))
	}
	if fp.PushErr != nil {
		failed = append(failed, fmt.Sprintf("the push node %s could not be deleted: %s", pairPushPath(fp.UID), oneLine(fp.PushErr.Error())))
	}
	if fp.CopyErr != nil {
		failed = append(failed, fmt.Sprintf("the cloud copy of the pairing key could not be destroyed: %s", oneLine(fp.CopyErr.Error())))
	}
	if fp.ItemErr != nil {
		failed = append(failed, fmt.Sprintf("the keychain item %s is not a name a key is stored under: %s", fp.ItemName, oneLine(fp.ItemErr.Error())))
	}
	if fp.KeyErr != nil {
		failed = append(failed, fmt.Sprintf("the keychain item %s could not be deleted: %s", fp.Item, oneLine(fp.KeyErr.Error())))
	}
	return failed
}
