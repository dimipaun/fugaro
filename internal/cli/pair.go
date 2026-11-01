package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	// rsc.io/qr is the one module this work adds to go.mod, and the owner
	// approved it by name with T4's brief. renderPairQR's comment records it.
	"rsc.io/qr"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro pair (plan §4.1, §7.1): pair a phone with this installation. This is
// the command's surface, its one-shot pairing token, the QR's payload, the
// wait for the phone's key, the comparison of the typed code, the device node
// and the delivery the phone stores; `pair ls` and `pair revoke` are T4's, and
// the command is not yet on the root (T4 wires it).

// pairNow is the command's clock. It is a variable for the same reason
// budgetNow is: a test pins the stream's timestamps and the token's exp.
var pairNow = time.Now

// pairMeLookup is who the command thinks it is: localcfg's own reader, and a
// variable because an identity that cannot be read has a refusal of its own
// (and a class of its own, exit 2 when the failure is remote) rather than the
// "you are on neither list" one a dropped error would produce.
var pairMeLookup = func(ctx context.Context, lc *localcfg.Config) (string, error) { return lc.Me(ctx) }

// pairKeychain is the OS keychain this pairing's MAC key is stored in (M5,
// D113b, D171): the key never reaches a file, and the local config holds the
// item's name and never its value. It is a variable because every test in this
// repository holds keychain.Fake, and no test touches a real keychain.
var pairKeychain = keychain.System

// pairTerminal is where the *interactive* pairing reads the code the operator
// types, as opposed to the stdin line a --json caller hands the command. In
// production the two are the same file, and the seam exists so a test can hold
// them apart: the failure it guards against — a --json run that asks the
// operator's terminal something — is invisible otherwise, and it is the
// failure a console driving this command would hang on.
var pairTerminal = func(cmd *cobra.Command) io.Reader { return cmd.InOrStdin() }

// pairPollInterval is how often the wait re-reads the phone's request node.
// The wait is a person scanning a code and typing eight characters, so a poll
// this coarse costs nothing and keeps the loop a plain read with database
// admin: no subscription and no connection held open for the whole --timeout.
var pairPollInterval = 200 * time.Millisecond

const (
	// pairDefaultTimeout is how long a pairing waits for the phone's key and
	// the typed code. It leaves room for a slow scan without approaching
	// either the token's ceiling or the exchange's slack.
	pairDefaultTimeout = 2 * time.Minute
	// pairMaxTimeout is the ceiling on --timeout: the pairing token's lifetime
	// less the exchange's slack, so the phone's exchange still lands before
	// the credential it holds dies (§4.1).
	pairMaxTimeout = token.PairingMaxLifetime - token.PairingSlack
)

type pairOptions struct {
	cloudOptions
	name, email, role string
	timeout           time.Duration
	asJSON            bool
}

func newPairCmd() *cobra.Command {
	o := &pairOptions{}
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair a phone with this installation",
		Long: "Pair a phone with this installation: the operator shows a QR code, the\n" +
			"phone scans it, writes its public key and shows a code derived from that\n" +
			"key, and the operator types the code back to bind the two.\n" +
			"\n" +
			"Pairing is an admin act, and it needs both of the installation's two role\n" +
			"sets: the signer account's fugaroTokenMinter role to mint the pairing\n" +
			"token and the device token, and roles/firebasedatabase.admin to write the\n" +
			"device node and the delivery node.\n" +
			"\n" +
			"Pairing is discovery, not identity: scanning the code tells the phone which\n" +
			"project and database to connect to and grants it no access by itself.\n" +
			"\n" +
			"The code carries no key. The final credentials — the device token, the\n" +
			"per-pairing MAC key and the configuration the app stores — go to the phone\n" +
			"encrypted to the key it generated, and the MAC key never leaves this\n" +
			"laptop's OS keychain in clear, is never printed, and is not in the code.\n" +
			"\n" +
			"The typed code is the binding: nothing is written to the project before the\n" +
			"code the operator types matches the key the phone wrote, so a refused\n" +
			"pairing leaves the project exactly as it was.\n" +
			"\n" +
			"The code is drawn in a terminal wide enough for it. The payload is over a\n" +
			"kilobyte, so its code is over a hundred columns wide, and a narrower\n" +
			"terminal gets the payload line instead of a drawing that would wrap and\n" +
			"stop being a QR code; --json carries that same payload for a console.\n" +
			"\n" +
			"Its own state is the local config: the project, its database URL and\n" +
			"Identity Toolkit API key, and the signer account. --json streams the\n" +
			"pairing as one JSON object per line on stdout for a console to read.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPair(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "the device's name, for pair ls and the device node's own field")
	f.StringVar(&o.email, "email", "", "the administered email for the device (default, and normally the value: your own verified address, which is the only address this command may administer)")
	f.StringVar(&o.role, "role", "", "the device's role: viewer, operator or admin (default: your own role, inherited; --role may only lower it)")
	f.DurationVar(&o.timeout, "timeout", pairDefaultTimeout, "how long to wait for the phone's key and the typed code (refused above "+pairMaxTimeout.String()+")")
	f.BoolVar(&o.asJSON, "json", false, "stream the pairing as newline-delimited JSON events on stdout")
	addCloudFlags(cmd, &o.cloudOptions)
	cmd.AddCommand(newPairLsCmd(), newPairRevokeCmd())
	return cmd
}

func runPair(cmd *cobra.Command, o *pairOptions) error {
	ctx := cmd.Context()
	now := pairNow()
	ev := &pairEventWriter{w: cmd.OutOrStdout(), now: pairNow}

	if reason := pairTimeoutRefusal(o.timeout); reason != "" {
		code := pairRefusedTimeout
		if o.timeout <= 0 {
			code = pairRefusedTimeoutNotPositive
		}
		return pairRefuse(ev, o, code, errors.New(reason))
	}

	// The project is selected before the database is opened, not through
	// openBudget, because a refusal below carries the project's name and
	// openBudget answers no config when the database cannot be reached.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return pairRefuse(ev, o, pairRefusedEnv, err)
	}
	_, lc, err := selectProject(ctx, o.cloudOptions)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedProject), err)
	}
	ev.project = lc.Name
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedBudget), err)
	}
	// The config layer validates the key only when it is set, so an installation
	// that never ran `fugaro update --firebase-project` would otherwise print a
	// code whose api_key is empty and which the phone cannot exchange a custom
	// token with at all.
	if strings.TrimSpace(lc.Budget.FirebaseAPIKey) == "" {
		return pairRefuse(ev, o, pairRefusedAPIKey, fmt.Errorf("project %s has no budget.firebase_api_key: the phone exchanges the pairing token with it, and a code without one scans into a phone that cannot sign in; restore it with 'fugaro update --firebase-project %s', which writes budget.rtdb_url and budget.firebase_api_key", lc.Name, lc.GCPProject))
	}
	// The one ownership question every writer into the database asks, before
	// the name check: a pairing writes the device node the rules admit a phone
	// by, so it asks whether the database is this installation's at all.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was paired"); err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedMismatch), err)
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedMismatch), err)
	}
	if err := pairDatabaseAdminProbe(ctx, db, lc); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin, err)
		}
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedUnreachable), err)
	}

	me, err := pairMeLookup(ctx, lc)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedIdentity), err)
	}
	role, ok := pairInheritedRole(lc, me)
	if !ok {
		return pairRefuse(ev, o, pairRefusedUnplaced,
			fmt.Errorf("%s is on neither of this installation's lists (terraform.operators, terraform.launchers), so a device it paired would have no role to inherit; ask an operator of %s to add you",
				meMember(me), lc.Name))
	}
	if o.role != "" {
		switch want, known := mobileauth.ParseRole(o.role); {
		case !known:
			return pairRefuse(ev, o, pairRefusedInvalidRole,
				fmt.Errorf("--role %s is not a role: this installation's roles are %s", o.role, pairRoleNames()))
		case !role.AtLeast(want):
			return pairRefuse(ev, o, pairRefusedRoleAbove,
				fmt.Errorf("--role %s is above your own role (%s): a paired device inherits your role and --role may only lower it, so a device like this one cannot be more than you are", want, role))
		default:
			role = want
		}
	}

	// §7.1 step 2, D224 and D226: the one address this command may administer
	// is the pairing operator's own verified one, read by the check
	// init_members.go's userEmail already performs for the CLI. --email is
	// therefore not a free field: an address that is not the operator's own
	// would put a device on D166's allowlist under an address nobody proved
	// they hold, and the node's existence is the whole of what the rules read.
	// The comparison is between canonical forms: own comes back trimmed and
	// lower-cased from userEmail, and the typed value is trimmed and folded the
	// same way before NormalizeEmail folds the domain, because the provider an
	// address of this shape belongs to resolves the local part
	// case-insensitively — the operator who capitalised a letter of their own
	// address typed their own address, and refusing it would send them to
	// change an account rather than a keystroke. What differs after both folds
	// is a different address and is refused rather than repaired.
	own, err := authenticatedUser(ctx, lc)
	if err != nil {
		// The check reads the address from the identity endpoint, so it can
		// fail remote as well as by answering "not verified": §4.6 puts an
		// endpoint that could not be reached in the exit-2 row, and an
		// operator told their address is unverified when the network was the
		// answer would go and fix the wrong thing.
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedEmail), err)
	}
	email := strings.ToLower(strings.TrimSpace(o.email))
	if email == "" {
		email = own
	} else if email, err = mobileauth.NormalizeEmail(email); err != nil {
		return pairRefuse(ev, o, pairRefusedEmail, err)
	} else if email != own {
		return pairRefuse(ev, o, pairRefusedEmail,
			fmt.Errorf("--email %s is not your own verified address (%s): a device node's administered email is an address this installation has proved the pairing operator holds, and pairing refuses one it cannot attribute to you", email, own))
	}

	signer, err := (&cloudEnv{lc: lc}).tokenSigner(ctx)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedUnreachable), err)
	}
	pairingToken, uid, err := token.MintPairing(ctx, signer, o.timeout, now)
	if err != nil {
		if errors.Is(err, token.ErrNotMinter) {
			return pairRefuse(ev, o, pairRefusedMinter,
				fmt.Errorf("%s; pairing mints the one-shot pairing token with your own credential, which needs the fugaroTokenMinter role on the signer account %s (the installation's launchers and operators hold it)", oneLine(err.Error()), lc.Budget.TokenSigner))
		}
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedUnreachable), err)
	}

	payload, err := json.Marshal(pairQR{
		V:           pairQRVersion,
		Project:     lc.Name,
		GCPProject:  lc.GCPProject,
		DatabaseURL: lc.Budget.RTDBURL,
		APIKey:      lc.Budget.FirebaseAPIKey,
		Token:       pairingToken,
		UID:         uid,
		ExpiresAt:   now.Add(token.PairingLifetime(o.timeout)).UTC().Format(time.RFC3339),
	})
	if err != nil {
		return userErr("building the QR payload: %v", err)
	}
	expiresAt := now.Add(token.PairingLifetime(o.timeout)).UTC().Format(time.RFC3339)

	out := cmd.OutOrStdout()
	if o.asJSON {
		if err := ev.emit(pairEvent{Event: pairEventQR, Payload: string(payload), UID: uid, ExpiresAt: expiresAt}); err != nil {
			return err
		}
		if err := ev.emit(pairEvent{Event: pairEventWaiting, TimeoutMS: o.timeout.Milliseconds(), ExpiresAt: expiresAt}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "device %s: role %s, administered email %s\n", pairName(o.name), role, email)
		if err := renderPairQR(out, string(payload)); err != nil {
			return err
		}
		fmt.Fprintf(out, "waiting up to %s for the phone's key at %s\n", o.timeout, pairPathRequest(uid))
	}

	// §7.1 steps 4 and 5 share the one --timeout the operator gave, and the
	// deadline behind it is real time: a wait is what the clock does, and the
	// command's own pairNow is pinned by tests to stamp the stream.
	waitCtx, stopWait := context.WithTimeout(ctx, o.timeout)
	defer stopWait()

	var request pairRequestNode
	if request, err = pairWaitForKey(waitCtx, lc, db, uid, o.timeout); err != nil {
		switch {
		case errors.Is(err, errPairNoKey):
			return pairRefuse(ev, o, pairRefusedNoKey, err)
		case errors.Is(err, errPairInterrupted):
			return pairRefuse(ev, o, pairRefusedInterrupted, err)
		case errors.Is(err, errPairRequestMalformed):
			// The node answered and the answer is not a reply this build can
			// read, so the database is not the thing that is wrong: §4.6's
			// exit-2 row is for a database that could not be reached, and an
			// operator sent after an outage when a client wrote the wrong shape
			// would be sent to fix the wrong thing.
			return pairRefuse(ev, o, pairRefusedRequestMalformed, err)
		default:
			return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedUnreachable), err)
		}
	}
	// The fingerprint comes from the bytes the phone's key decodes to, never
	// from the text the phone spelled them with (M3, §2 (e)): the rules admit
	// several spellings of one key, and a code derived from the spelling would
	// be a code per spelling.
	key, err := token.DecodeDeviceKey(request.Key)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedRequestKey,
			fmt.Errorf("the key written at %s is not the one spelling this build reads: %v", pairPathRequest(uid), err))
	}
	fingerprint := token.Fingerprint(key)

	codeIn := cmd.InOrStdin()
	if o.asJSON {
		if err := ev.emit(pairEvent{Event: pairEventCode, UID: uid}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "the phone shows %s\ntype that code: ", fingerprint)
		codeIn = pairTerminal(cmd)
	}
	typed, err := pairReadCode(waitCtx, codeIn)
	if err != nil {
		if errors.Is(err, errPairInterrupted) {
			return pairRefuse(ev, o, pairRefusedInterrupted, err)
		}
		return pairRefuse(ev, o, pairRefusedCodeMissing, err)
	}
	if !token.CompareFingerprint(typed, fingerprint) {
		return pairRefuse(ev, o, pairRefusedCodeMismatch,
			fmt.Errorf("the code typed does not match the key written at %s: nothing was written to %s, and this pairing is over rather than retried; a request under a key whose code does not match is what a photographed QR looks like, so scan the code from the screen of the phone you mean to pair", pairPathRequest(uid), lc.Name))
	}

	// §7.1 steps 6 and 7, M4: the device token is minted and then the node
	// exists before anything is delivered, so no token exists that the rules
	// do not yet admit.
	device, err := token.MintDevice(ctx, signer, now)
	if err != nil {
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedMinter), err)
	}
	node := pairDeviceNode{Name: o.name, Email: email, Role: string(role), At: now.UTC().Format(time.RFC3339)}
	if err := db.Patch(ctx, rules.PathDevices, map[string]any{device.UID: node}); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				fmt.Errorf("the write to %s was refused: pairing writes the device node there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w",
					rules.PathDevices, lc.Name, err))
		}
		return pairRefuse(ev, o, pairRefusedUnreachable, dbErr(lc, err, true))
	}

	// §7.1 steps 6, 8 and 9. The keychain write sits after the node write
	// rather than at step 6's place before it, and the reason is the undo: the
	// node is what the device is, so every failure below rolls the pairing back
	// to nothing — a keychain that cannot be written removes the node again, a
	// delivery that cannot be written removes the item and the node — and
	// writing the key first would leave an item to remove when the node write
	// is the one that fails, for a step whose position §7.3 does not make
	// load-bearing. M4's ordering is untouched: the node exists before anything
	// reaches the phone, and nothing does until the delivery node exists.
	item, err := keychain.ParseItem(pairItemName(lc.Name, device.UID), lc.Name)
	if err != nil {
		// The name is built here out of the project's own name and a uid this
		// build drew, so this is a project name that cannot appear in a
		// keychain item — a refusal with nothing written yet, and not a
		// keychain failure: no keychain has been asked anything.
		return pairRefuse(ev, o, pairRefusedKeychain,
			fmt.Errorf("the keychain item this pairing's MAC key would live at (%s) is not a name a key can be stored under: %v; the item's name carries the project's own name (M5), so a project named otherwise cannot be paired on this machine", pairItemName(lc.Name, device.UID), err))
	}
	macKey, err := pairStoreMACKey(ctx, pairKeychain(), item)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedKeychain, pairRemoveDeviceNode(ctx, db, lc, device.UID, fmt.Errorf("the pairing's MAC key could not be stored in the OS keychain at %s: %w; the device node written a moment ago was removed again, so nothing was delivered and no device is on the allowlist", item.Name(), err)))
	}

	// §7.1 step 8 and §4.4: the one payload the phone stores — D199's
	// configuration, the device token and the MAC key — sealed to the key the
	// phone wrote, and written where its own pairing token is the only reader.
	// **It is a capability and not a boundary** (§8): it removes the value of a
	// photographed QR or a copied node, and it removes no reach — whoever holds
	// database admin reads this node as this process would.
	// D199: the delivery's copy of the configuration is the record the app
	// stores, and the QR's is the bootstrap it needed in order to get here, so
	// the two are the same four values by construction — a disagreement is what
	// the phone refuses the whole payload over (§4.4).
	delivery := token.Delivery{
		Config: token.DeliveryConfig{
			Project:     lc.Name,
			GCPProject:  lc.GCPProject,
			DatabaseURL: lc.Budget.RTDBURL,
			APIKey:      lc.Budget.FirebaseAPIKey,
		},
		Device: device,
		MACKey: macKey,
	}
	sealedPayload, err := delivery.MarshalPayload()
	if err != nil {
		return pairRefuse(ev, o, pairRefusedDelivery, pairUndoPairing(ctx, db, lc, device.UID, item, fmt.Errorf("building the delivery payload: %v", err)))
	}
	sealed, err := token.SealDelivery(key, uid, sealedPayload)
	if err != nil {
		return pairRefuse(ev, o, pairRefusedDelivery, pairUndoPairing(ctx, db, lc, device.UID, item, fmt.Errorf("sealing the delivery to the key written at %s: %v", pairPathRequest(uid), err)))
	}
	if err := db.Patch(ctx, pairPath(uid), map[string]any{"delivery": pairDeliveryNode{V: token.DeliveryEnvelopeVersion, Sealed: token.EncodeKey(sealed)}}); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				pairUndoPairing(ctx, db, lc, device.UID, item, fmt.Errorf("the write to %s was refused: pairing writes the delivery node there with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w", pairPathDelivery(uid), lc.Name, err)))
		}
		// The wire code is read off the classified error and not off what rtdb
		// returned: §4.6's rows are dbErr's classes, so a database that did not
		// answer is database_unreachable (exit 2) and never
		// delivery_write_failed, which names a local write the operator would
		// look for a fix in while the database was simply not there.
		cause := dbErr(lc, err, true)
		return pairRefuse(ev, o, pairRefusalCode(cause, pairRefusedDelivery),
			pairUndoPairing(ctx, db, lc, device.UID, item, cause))
	}

	// §7.1 step 9, decision 2: the whole subtree goes, so the payload does not
	// sit readable for the hour the p~ ID token lives. It is the node alone —
	// the Auth user needs roles/firebaseauth.admin, which no Terraform grant
	// gives an operator, and decision 1 hands it to the history sweep.
	if err := db.Patch(ctx, "pairing", map[string]any{uid: nil}); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				fmt.Errorf("the deletion of %s was refused: the pairing is done — the delivery is written, the device is paired and its MAC key is in the OS keychain item %s — but the pairing node could not be removed, so the payload stays readable to the pairing token for as long as its ID token lives; deleting it needs roles/firebasedatabase.admin on project %s's Firebase project, and the history sweep deletes that node with the %s Auth user once the pairing is over: %w", pairPath(uid), item.Name(), lc.Name, uid, err))
		}
		cause := dbErr(lc, err, true)
		return pairRefuse(ev, o, pairRefusalCode(cause, pairRefusedDelivery),
			fmt.Errorf("%w; the pairing is done — the delivery is written, the device is paired and its MAC key is in the OS keychain item %s — but %s could not be removed, so the payload stays readable to the pairing token for as long as its ID token lives; the history sweep deletes that node with the %s Auth user once the pairing is over", cause, item.Name(), pairPath(uid), uid))
	}

	if o.asJSON {
		return ev.emit(pairEvent{Event: pairEventDone, UID: uid, Document: &pairDocument{
			Project:      lc.Name,
			At:           now.UTC().Format(time.RFC3339),
			Pairing:      pairPairing{UID: uid, State: pairStateCompleted, ExpiresAt: expiresAt},
			Request:      pairRequest{KeyFingerprint: fingerprint, At: pairRequestAt(request.At)},
			Device:       pairDevice{UID: device.UID, Name: o.name, Email: email, Role: string(role)},
			KeychainItem: item.Name(),
			Wrote:        []string{rules.PathDevices + "/" + device.UID, pairPathDelivery(uid)},
			Refused:      []string{},
			Warnings:     []string{},
		}})
	}
	fmt.Fprintf(out, "paired %s: device %s, role %s, administered email %s, code %s\n", pairName(o.name), device.UID, role, email, fingerprint)
	fmt.Fprintf(out, "wrote %s and %s, and deleted %s\n", rules.PathDevices+"/"+device.UID, pairPathDelivery(uid), pairPath(uid))
	fmt.Fprintf(out, "the phone's MAC key is in the OS keychain item %s; record it in the local config's mailbox block as mailbox.keychain_item[%s]\n", item.Name(), device.UID)
	fmt.Fprintf(out, "the %s Auth user is not deleted here — this credential cannot delete one — and the history sweep removes it once the pairing is over\n", uid)
	return nil
}

// pairDeliveryNode is what §7.1 step 8 writes at the delivery node: the
// envelope token.SealDelivery returned, in EncodeKey's one spelling, under a
// version of its own. The node has no `.validate` and no `$other` (§2 (a) 4,
// M14), so nothing but this struct says what its shape is, and the phone
// (P6.8) refuses the whole payload unless the version is one it reads — which
// it can do before decoding anything, since a node written by another build is
// the one case where the bytes are not worth looking at.
type pairDeliveryNode struct {
	V      byte   `json:"v"`
	Sealed string `json:"sealed"`
}

// pairItemName is the keychain item this pairing's MAC key lives at (decision
// 9): one item per pairing, and the name is derivable from the device uid —
// which is what lets `pair revoke <uid>` find the item it must delete without
// a second record of it. The project's own name is in it, so a config naming
// the item cannot reach another project's key, and every character is one
// keychain.ParseItem admits: the uid's body is unpadded base64url, whose two
// punctuation characters are both in M5's alphabet, and a local config's
// project name is 1 to 40 characters of a-z, 0-9 and '-'.
//
// **The name is the project and the uid and nothing else, and the arithmetic
// is why**: ParseItem's ceiling is 64 characters, the body of a device uid is
// 22 of them, and a project name may be 40 — so a project and one separator
// leave exactly one character of slack, and a fixed prefix of any length
// ("fugaro-mailbox-") is a name that cannot be built for the longest project
// names localcfg accepts. A pairing that cannot name its item is a pairing
// that cannot store a key, so the prefix is what gives way.
func pairItemName(project, deviceUID string) string {
	return project + "-" + strings.TrimPrefix(deviceUID, "m~")
}

// pairStoreMACKey draws this pairing's MAC key (D171) and stores it at item,
// returning the key for the delivery that carries it to the phone.
//
// The value stored is EncodeKey's spelling and not the key's raw bytes: a
// keychain holds a string, and the delivery carries that same spelling, so the
// bytes this laptop signs with (the item's value, read back by the consumer)
// and the bytes the phone signs with (mac_key, decoded from the payload) are
// one value and not two encodings of it. A draw that fails is the same refusal
// as a store that fails — either way this pairing has no key — and neither is
// ever a fallback to a shorter or a guessable one.
func pairStoreMACKey(ctx context.Context, store keychain.Store, item keychain.Item) ([]byte, error) {
	key, err := token.NewMACKey()
	if err != nil {
		return nil, err
	}
	if err := store.Put(ctx, item, keychain.NewKey([]byte(token.EncodeKey(key)))); err != nil {
		return nil, err
	}
	return key, nil
}

// pairRemoveDeviceNode undoes the device node a failed step below left behind,
// and returns cause either way: the removal is best-effort, because the
// refusal the operator needs is the one that says why the pairing stopped, and
// a second failure here would replace it with a message about the cleanup. The
// node is what a phone is admitted by, so a node with no delivery behind it is
// a device that fails USER on every request.
func pairRemoveDeviceNode(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, deviceUID string, cause error) error {
	if err := db.Patch(ctx, rules.PathDevices, map[string]any{deviceUID: nil}); err != nil {
		return fmt.Errorf("%w; the device node %s could not be removed either (%v), so it is on the allowlist with no delivery behind it: remove it with `fugaro pair revoke %s`", cause, deviceUID, oneLine(err.Error()), deviceUID)
	}
	return cause
}

// pairRemoveItem is that same undo for the keychain item, and it has the same
// shape and the same reason: the keychain write is the step before the
// delivery, so a delivery that could not be written leaves a key at an item
// nothing will ever read, and the item's absence is not an error.
func pairRemoveItem(ctx context.Context, store keychain.Store, item keychain.Item, cause error) error {
	if err := store.Delete(ctx, item); err != nil {
		return fmt.Errorf("%w; the MAC key stored at %s could not be removed either (%v), so it is there with no delivery behind it: delete it by hand", cause, item.Name(), oneLine(err.Error()))
	}
	return cause
}

// pairUndoPairing is the undo for every failure between the keychain write and
// the delivery node: both of the things the pairing has written by then — the
// item and the device node — go back, because a pairing that delivered nothing
// is one the phone never completed, and `pairRemoveDeviceNode`'s own reason
// applies to it in full: a node with no delivery behind it is a device that
// fails USER on every request, on the allowlist, until someone removes it. The
// item goes first, since it is the one written second and the one that holds
// key material; the node's removal is what the operator is told about if it
// fails, because that is the one they cannot do themselves by hand.
func pairUndoPairing(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, deviceUID string, item keychain.Item, cause error) error {
	return pairRemoveDeviceNode(ctx, db, lc, deviceUID, pairRemoveItem(ctx, pairKeychain(), item, cause))
}

// pairPathRequest is the phone's half of the pairing session (§4.4): one node
// per pairing uid, under the root-level pairing tree, where the phone writes
// its X25519 public key once. The template carries `.read: false` on it, so
// the CLI reads it with database admin and no token — the pairing token
// included — can read the key at all.
func pairPathRequest(uid string) string { return pairPath(uid) + "/request" }

// pairPath is the pairing session's own node, the one §7.1 step 9 deletes whole
// once the delivery is written. pairPathDelivery is the child the phone reads
// its payload from; the two are named here rather than spelled out at each call
// site so that the deletion cannot come to name a different node than the write.
func pairPath(uid string) string { return "pairing/" + uid }

func pairPathDelivery(uid string) string { return pairPath(uid) + "/delivery" }

// pairRequestNode is what the phone writes at that node (§4.4): the key in the
// one spelling §2 (e) pins and when it wrote it, as the epoch-millisecond
// instant the rules' TIMESTAMP macro validates. The template's `$other`
// refuses any third field, so these two are all there is to read.
type pairRequestNode struct {
	Key string      `json:"key"`
	At  json.Number `json:"at"`
}

// pairDeviceNode is what §7.1 step 7 writes at config/admin/devices/$uid. M13's
// rule is this struct's shape and nobody else's: the node holds the name, the
// administered email (D226), the role (D225) and the pairing time, and no
// token, no key and no fingerprint — the deployed document has no `$other` here
// and nothing in the rules would stop a secret being written, so a field added
// to this struct is a field a reviewer can see.
type pairDeviceNode struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	At    string `json:"at"`
}

// pairStateCompleted is the pairing's state in §4.5's document.
const pairStateCompleted = "completed"

// errPairNoKey is the wait's own failure — the phone wrote no key before the
// deadline — told apart from a database error by errors.Is, because the two
// carry different wire codes.
var errPairNoKey = errors.New("pairing: the phone wrote no key")

// errPairInterrupted is the operator's own ending: cmd/fugaro/main.go cancels
// the root context on the first signal and the wait stops where it stood. It
// is not the phone's failure and not an outage, and a console told either
// would send its operator to fix something nobody did.
var errPairInterrupted = errors.New("pairing: interrupted")

// pairWaitForKey waits for the phone's request node (§7.1 step 4). The node is
// read with database admin, because the template denies every client token the
// read, and the loop re-reads rather than subscribing: the wait is bounded by
// --timeout and answered once. A node the phone has created but not filled is
// not a request yet — the rules require `key` and `at` together — and an
// absent node and an empty one are the same wait.
func pairWaitForKey(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, uid string, timeout time.Duration) (pairRequestNode, error) {
	path := pairPathRequest(uid)
	tick := time.NewTicker(pairPollInterval)
	defer tick.Stop()
	for {
		var raw json.RawMessage
		found, err := db.Get(ctx, path, &raw)
		switch {
		case err != nil && ctx.Err() != nil:
			// The deadline this wait was given can cancel a read already in
			// flight, and the client answers with the context's error; dbErr
			// reads that as an outage, which would tell a console the phone's
			// silence was the database's (exit 2, the one path the phone
			// never scanning takes).
			return pairRequestNode{}, pairWaitEnded(ctx, path, lc, timeout)
		case err != nil:
			return pairRequestNode{}, dbErr(lc, err, false)
		case found:
			// The node is read as raw JSON and decoded here rather than
			// straight into the struct, because the client cannot tell a
			// node it could not decode from a database it could not reach:
			// both come back as an error dbErr classes as an outage, and an
			// operator sent to check the network for a reply that was
			// malformed finds nothing wrong there.
			request, err := pairDecodeRequest(raw)
			switch {
			case err != nil:
				return pairRequestNode{}, err
			case request.Key != "":
				return request, nil
			}
		}
		select {
		case <-ctx.Done():
			return pairRequestNode{}, pairWaitEnded(ctx, path, lc, timeout)
		case <-tick.C:
		}
	}
}

// pairWaitEnded is the wait ending on its own terms rather than the phone's:
// ctx's deadline is the --timeout running out, and any other cancellation is
// the operator's interrupt. WithTimeout keeps the two apart, so the reason
// says which one it was.
func pairWaitEnded(ctx context.Context, path string, lc *localcfg.Config, timeout time.Duration) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w at %s within %s: nothing was written to %s, and the pairing token the phone was given dies with the wait; run fugaro pair again", errPairNoKey, path, timeout, lc.Name)
	}
	return fmt.Errorf("%w while waiting for the phone's key at %s, so this pairing was abandoned by the operator rather than refused by it; run fugaro pair again", errPairInterrupted, path)
}

// errPairNoCode is the read's own failure: no line arrived before the wait ran
// out, or the line was empty.
var errPairNoCode = errors.New("pairing: no typed code")

// pairReadCode reads the operator's typed code as exactly one line (§7.1 step
// 5): one line, read once, and never a second prompt or a retry — a code that
// did not match is a refused pairing, not a typo to try again. The read races
// the wait's deadline because --timeout bounds steps 4 and 5 together, and a
// reader that has not answered by then is refused like an EOF: an operator who
// walked away. The goroutine is left behind when a terminal never answers —
// there is no way to cancel a blocked read of a terminal, and the command is
// about to exit with its refusal.
func pairReadCode(ctx context.Context, in io.Reader) (string, error) {
	type line struct {
		text string
		err  error
	}
	read := make(chan line, 1)
	go func() {
		text, err := bufio.NewReader(in).ReadString('\n')
		read <- line{text: text, err: err}
	}()
	select {
	case <-ctx.Done():
		// The same two endings the wait has: the deadline is an operator who
		// walked away from the prompt, an interrupt is one who stopped it.
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w while the code the phone shows was being typed, so this pairing was abandoned by the operator rather than refused by it; run fugaro pair again", errPairInterrupted)
		}
		return "", fmt.Errorf("%w: the code the phone shows is read as one line on stdin, and no line arrived before the wait ran out", errPairNoCode)
	case got := <-read:
		if got.err != nil && !errors.Is(got.err, io.EOF) {
			return "", fmt.Errorf("reading the typed code: %v", got.err)
		}
		// A last line without its newline is a line a pipe delivered, and it
		// is read like any other; whitespace around it is the terminal's, not
		// the operator's.
		code := strings.TrimSpace(got.text)
		if code == "" {
			return "", fmt.Errorf("%w: the code the phone shows is read as one line on stdin, and the line was empty", errPairNoCode)
		}
		return code, nil
	}
}

// errPairRequestMalformed is the phone's reply being something this build
// cannot read: the node holds `key` and `at` (the template's `$other` refuses
// any third field) and the rules validate `at` as an integer count of
// epoch-milliseconds, so a reply carrying anything else there was written by
// something that is not the phone this command is pairing. It is told apart
// from a database that could not be reached because the two are different
// sentences to the operator: one says rescan, the other says the network.
var errPairRequestMalformed = errors.New("pairing: the reply at the request node is not the shape this build reads")

// pairDecodeRequest reads the phone's reply. `at` must be an integer literal:
// a quoted string, an object, a fraction and an absent field are each a reply
// no phone of this build writes, and each is refused where it is read rather
// than carried into the document as an empty instant.
func pairDecodeRequest(raw json.RawMessage) (pairRequestNode, error) {
	var wire struct {
		Key string          `json:"key"`
		At  json.RawMessage `json:"at"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return pairRequestNode{}, fmt.Errorf("%w: %v", errPairRequestMalformed, err)
	}
	if wire.Key == "" {
		// A node the phone has created but not filled is not a request yet:
		// the rules require `key` and `at` together, so a node with no key and
		// an absent node are the same wait.
		return pairRequestNode{}, nil
	}
	malformed := fmt.Errorf("%w: the `at` it wrote is not the epoch-millisecond number the pairing rules admit, so nothing here can be dated or confirmed", errPairRequestMalformed)
	// A json.Number takes the *string* "123" as readily as the number 123 —
	// encoding/json accepts a quoted literal that spells a number — and the
	// rules admit the number alone, so the literal itself is checked before it
	// is read.
	if len(wire.At) == 0 || (wire.At[0] != '-' && (wire.At[0] < '0' || wire.At[0] > '9')) {
		return pairRequestNode{}, malformed
	}
	var at json.Number
	if err := json.Unmarshal(wire.At, &at); err != nil {
		return pairRequestNode{}, malformed
	}
	if _, err := at.Int64(); err != nil {
		return pairRequestNode{}, malformed
	}
	return pairRequestNode{Key: wire.Key, At: at}, nil
}

// pairRequestAt renders the epoch-millisecond instant the phone wrote as the
// document's RFC3339, and "" when the node carries no instant this build can
// read: the request's children are validated for the phone's own write, and a
// node that reached the database another way must not stop a pairing the
// operator has already confirmed.
func pairRequestAt(at json.Number) string {
	ms, err := at.Int64()
	if err != nil || ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// pairQR is what the QR carries (§4.4): the bootstrap configuration the phone
// needs in order to exchange a custom token at all — the project, the database
// URL and the Identity Toolkit API key — and the one-shot pairing token. It
// carries no key, no identity and no code: the MAC key is D171's and never
// leaves this laptop, the administered email, the name and the role belong to
// the delivery, and the typed code is derived by both ends and written
// nowhere (M3).
type pairQR struct {
	V           int    `json:"v"`
	Project     string `json:"project"`
	GCPProject  string `json:"gcp_project"`
	DatabaseURL string `json:"database_url"`
	APIKey      string `json:"api_key"`
	Token       string `json:"token"`
	UID         string `json:"uid"`
	ExpiresAt   string `json:"expires_at"`
}

// pairTimeoutRefusal is the --timeout refusal's reason, or "" when the wait is
// one the pairing token can cover.
func pairTimeoutRefusal(timeout time.Duration) string {
	switch {
	case timeout <= 0:
		return fmt.Sprintf("--timeout %s is not a wait: it must be positive and at most %s", timeout, pairMaxTimeout)
	case timeout > pairMaxTimeout:
		return fmt.Sprintf("--timeout %s is above %s: the pairing token's exp is the smaller of %s and the wait plus the %s exchange slack, so a longer wait would outlive the credential the phone holds; use %s or less",
			timeout, pairMaxTimeout, token.PairingMaxLifetime, token.PairingSlack, pairMaxTimeout)
	}
	return ""
}

// pairInheritedRole is the role a paired device inherits (plan §4.1, decision
// 13): the pairing user's own, read from the installation's lists. Launchers
// and operators both map to the device word operator, and the lists hold no
// viewer and no admin — so a viewer device is reached only by lowering with
// --role, and no device is admin until the roles design places an admin.
func pairInheritedRole(lc *localcfg.Config, me string) (mobileauth.Role, bool) {
	member := meMember(me)
	if operatorMember(member, lc.Terraform.Operators) || launcherMember(member, lc.Terraform.Launchers) {
		return mobileauth.RoleOperator, true
	}
	return mobileauth.RoleNone, false
}

// pairDatabaseAdminProbe refuses a credential the database will not let at the
// device node, before the token is minted and before anything is shown (§4.6,
// §6): the node is written, the delivery node is written and the pairing node
// is deleted with roles/firebasedatabase.admin.
func pairDatabaseAdminProbe(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) error {
	var raw json.RawMessage
	if _, err := db.Get(ctx, rules.PathDevices, &raw); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			// %w keeps the sentinel in the chain: the call site tells this
			// refusal from an unreachable database by errors.Is, and the code it
			// emits is the one a console switches on.
			return &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s refused the read: pairing writes the device node there and the delivery node with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w", rules.PathDevices, lc.Name, err)}
		}
		return dbErr(lc, err, false)
	}
	return nil
}

// pairQRQuiet is the quiet zone around the drawing, in modules. The code's own
// spec asks for four; two are what a terminal needs, because the screen's
// background is already around the code and every extra module is a column a
// narrow terminal would wrap — and a wrapped QR code is not a QR code.
const pairQRQuiet = 2

// pairQRMaxColumns is the width a drawing is held to when stdout is not a
// terminal whose width can be asked, because 80 is what a terminal is by
// default. A drawing wider than the terminal wraps, and a wrapped QR code is
// not a QR code (pairQRQuiet). The payload is a bootstrap configuration and a
// JWT, so it is over a kilobyte and its code is over a hundred columns wide:
// held to the real terminal's width (pairTerminalColumns), a terminal widened
// for it gets the drawing, and a narrower one gets the payload line and the
// width the code needs, so the operator can widen it or carry the payload.
const pairQRMaxColumns = 80

// pairTerminalColumns is the width the QR drawing is held to: stdout's own
// when it is a terminal, pairQRMaxColumns otherwise. A variable so a test can
// stand in for a wide terminal.
var pairTerminalColumns = func(w io.Writer) int {
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			return cols
		}
	}
	return pairQRMaxColumns
}

// renderPairQR draws the payload as the QR the phone scans (§4.1, §4.4): the
// code the operator's screen shows, from rsc.io/qr's encoder, so what is on
// the screen is the code and not a description of it.
//
// The encoder is rsc.io/qr, the one module this work adds to go.mod. The build
// before this one printed the payload and said the drawing was missing,
// because "go.mod may not gain one before the owner approves it"; the approval
// is T4's brief, which names rsc.io/qr. The plan's own "it adds no dependency"
// (§17, bullet 6) is T3's crypto work, which adds none.
//
// It is drawn as half blocks — one text row carries two module rows, with
// U+2580, U+2584 and U+2588 for the two, the top and the bottom one — because
// a terminal cell is about twice as tall as it is wide, so one module per cell
// would draw a code stretched to half its width. A block is a dark module and a
// space is a light one, which is the ordinary dark-on-light QR: on the dark
// background a terminal usually has, that is a light-on-dark code, and every
// phone camera of the last decade reads either.
//
// The level is L, the least redundant: the payload carries a JWT and a
// configuration, so it is over a kilobyte, and a lower level is a smaller
// version — fewer modules, a narrower drawing and a code easier to scan off a
// screen. Nothing here is a screenshot a test compares: what a test holds this
// to is the encoder's own module grid, which is what a phone reads.
func renderPairQR(w io.Writer, payload string) error {
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		return userErr("encoding the QR code: %v", err)
	}
	if columns, width := code.Size+2*pairQRQuiet, pairTerminalColumns(w); columns > width {
		return writePairQRPayload(w, payload, columns, width)
	}
	size := code.Size + 2*pairQRQuiet
	var b strings.Builder
	for y := -pairQRQuiet; y < size-pairQRQuiet; y += 2 {
		b.Reset()
		for x := -pairQRQuiet; x < size-pairQRQuiet; x++ {
			top, bottom := code.Black(x, y), code.Black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if _, err := fmt.Fprintln(w, b.String()); err != nil {
			return userErr("writing the QR code: %v", err)
		}
	}
	return nil
}

// writePairQRPayload is what a payload whose code is too wide to draw falls
// back to: the payload line itself, and the width the code needs, so the
// operator can widen the terminal rather than read a wrapped code. The line is
// what --json's qr event carries too, so a console can draw the code where
// there is room for it.
func writePairQRPayload(w io.Writer, payload string, columns, width int) error {
	_, err := fmt.Fprintf(w, "the pairing payload's QR code is %d columns wide, past the terminal's %d, "+
		"and a wrapped QR code is not a QR code: widen the terminal to %d columns to scan the code, "+
		"or carry this payload line, which --json's qr event carries too\n", columns, width, columns)
	if err != nil {
		return userErr("writing the QR payload: %v", err)
	}
	if _, err := fmt.Fprintln(w, payload); err != nil {
		return userErr("writing the QR payload: %v", err)
	}
	return nil
}

// pairRefuse is the command's one way out. Under --json it writes the refused
// event first, so the stream says what happened before the exit code does, and
// either way it exits with §4.6's class for that refusal.
func pairRefuse(ev *pairEventWriter, o *pairOptions, code string, err error) error {
	return pairRefuseIn(ev, o.asJSON, code, err)
}

func pairRefuseIn(ev *pairEventWriter, asJSON bool, code string, err error) error {
	reason := oneLine(err.Error())
	if asJSON {
		if err := ev.emit(pairEvent{Event: pairEventRefused, Code: code, Reason: reason}); err != nil {
			return err
		}
	}
	return &ExitError{Code: ExitCode(err), Err: errors.New(reason)}
}

// pairOpen is what the three subcommands share before they act: the project is
// selected, the budget backend is reached, the database is this project's and
// the credential holds database admin on it — the role every one of them reads
// or writes the device node with, and the one §4.6 refuses separately from the
// minter's. The device node is the whole of what the rules admit a device by
// (D166), so a command that could not read it must say so rather than report
// that no device is paired.
func pairOpen(cmd *cobra.Command, o cloudOptions, ev *pairEventWriter, asJSON bool, local string) (*localcfg.Config, *rtdb.Client, error) {
	ctx := cmd.Context()
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusedEnv, err)
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusalCode(err, pairRefusedProject), err)
	}
	ev.project = lc.Name
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusalCode(err, pairRefusedBudget), err)
	}
	// The same one ownership question the pairing itself asks: `ls` reads the
	// device nodes and `revoke` deletes one, and neither is this installation's
	// to do anywhere but in its own database.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was done there"); err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusalCode(err, pairRefusedMismatch), err)
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusalCode(err, pairRefusedMismatch), err)
	}
	if err := pairDatabaseAdminProbe(ctx, db, lc); err != nil {
		return nil, nil, pairRefuseIn(ev, asJSON, pairRefusalCode(err, local), err)
	}
	return lc, db, nil
}

type pairLsOptions struct {
	cloudOptions
	asJSON bool
}

func newPairLsCmd() *cobra.Command {
	o := &pairLsOptions{}
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the devices paired with this installation",
		Long: "List the devices paired with this installation: one row per device node\n" +
			"under config/admin/devices, with the uid (a key, and therefore printable),\n" +
			"the name, the administered email, the role and the pairing time.\n" +
			"\n" +
			"It reads with roles/firebasedatabase.admin, and it prints no token, no key\n" +
			"and no keychain item's value: the node holds none of them, and the item's\n" +
			"name is not in the node either, so there is nothing else to print.\n" +
			"\n" +
			"It does not report whether a device's Auth user still exists. The Auth user\n" +
			"is not in the database, so database admin does not reach it, and the node's\n" +
			"existence is the whole of what the rules read (D166): an orphaned Auth user\n" +
			"whose node is gone is the history sweep's business and is invisible to the\n" +
			"rules by construction.\n" +
			"\n" +
			"Its own state is the local config, as it is for the rest of the command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPairLs(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.asJSON, "json", false, "print the pairings as one JSON event on stdout")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

func runPairLs(cmd *cobra.Command, o *pairLsOptions) error {
	ev := &pairEventWriter{w: cmd.OutOrStdout(), now: pairNow}
	lc, db, err := pairOpen(cmd, o.cloudOptions, ev, o.asJSON, pairRefusedDatabaseAdmin)
	if err != nil {
		return err
	}
	rows, err := pairListDevices(cmd.Context(), lc, db)
	if err != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(err, pairRefusedUnreachable), err)
	}
	if o.asJSON {
		return ev.emit(pairEvent{Event: pairEventListed, List: &pairListDocument{
			Project: lc.Name, At: pairNow().UTC().Format(time.RFC3339),
			Devices: rows, Warnings: []string{},
		}})
	}
	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		_, err := fmt.Fprintf(out, "no device is paired with %s\n", lc.Name)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "DEVICE\tNAME\tEMAIL\tROLE\tPAIRED"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			pairCell(r.UID), pairCell(r.Name), pairCell(r.Email), pairCell(r.Role), pairCell(r.At)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// pairCell is a node's field as a table cell. Nothing here assumes the node
// holds what `fugaro pair` writes: its children are unconstrained (the
// deployed document declares no `.validate` and no `$other` under
// config/admin/devices), so a hand-written entry may hold anything, and a
// control character or a tab in one would otherwise escape the cell or open
// the next column. Both printers of a node go through it — `ls` as a cell and
// `revoke` naming the device it revoked — so one hostile name has one spelling
// and not two.
func pairCell(s string) string {
	return strings.ReplaceAll(pluginwire.Printable(strings.TrimSpace(s)), "\t", `\u0009`)
}

// pairListDevices reads the device allowlist: one row per child, sorted by uid
// so two listings of one database are one listing. A child whose fields cannot
// be read is its uid alone — §4.2's rule for a node with no readable children,
// which an older or a hand-written entry may be.
func pairListDevices(ctx context.Context, lc *localcfg.Config, db *rtdb.Client) ([]pairDeviceRow, error) {
	var tree map[string]json.RawMessage
	if _, err := db.Get(ctx, rules.PathDevices, &tree); err != nil {
		return nil, dbErr(lc, err, false)
	}
	rows := make([]pairDeviceRow, 0, len(tree))
	for uid, raw := range tree {
		rows = append(rows, pairDeviceRowFrom(uid, raw))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UID < rows[j].UID })
	return rows, nil
}

// pairDeviceRowFrom reads one device node's fields one at a time, and leaves a
// field empty rather than refusing the row: a node whose `at` is a number and
// whose name is fine is still a device an operator must see, and a listing that
// dropped it would hide a phone that is on the allowlist.
func pairDeviceRowFrom(uid string, raw json.RawMessage) pairDeviceRow {
	row := pairDeviceRow{UID: uid}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return row
	}
	get := func(k string, v *string) { _ = json.Unmarshal(m[k], v) }
	get("name", &row.Name)
	get("email", &row.Email)
	get("role", &row.Role)
	get("at", &row.At)
	return row
}

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
			"this laptop's OS keychain item for that pairing's MAC key. Two deletions,\n" +
			"and both are here.\n" +
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

	// M4, and the order is the command: the node first. It is what the rules
	// read in userClaim, so deleting it revokes at once — every family USER
	// reaches is refused from the next request on — while the keychain item and
	// the pairing node are this laptop's and the session's, not the phone's.
	if err := db.Patch(ctx, rules.PathDevices, map[string]any{device.UID: nil}); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuseIn(ev, o.asJSON, pairRefusedDatabaseAdmin,
				fmt.Errorf("the deletion of the device node %s was refused: revoking a device deletes it with roles/firebasedatabase.admin, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %w",
					rules.PathDevices+"/"+device.UID, lc.Name, err))
		}
		return pairRefuseIn(ev, o.asJSON, pairRefusalCode(err, pairRefusedUnreachable), dbErr(lc, err, true))
	}
	wrote := []string{rules.PathDevices + "/" + device.UID}

	item, err := keychain.ParseItem(pairItemName(lc.Name, device.UID), lc.Name)
	if err != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusedKeychain,
			fmt.Errorf("the device is off the allowlist, but the keychain item this pairing's MAC key lives at (%s) is not a name a key is stored under: %v; the item's name carries the project's own name (M5), so it is not derivable here and must be deleted by hand", pairItemName(lc.Name, device.UID), err))
	}
	if err := pairKeychain().Delete(ctx, item); err != nil {
		return pairRefuseIn(ev, o.asJSON, pairRefusedKeychainDelete,
			fmt.Errorf("the device node %s is deleted and the phone is off the allowlist, but this laptop's MAC key for the pairing could not be removed from the OS keychain at %s: %w; the key is still there and must be deleted by hand", wrote[0], item.Name(), err))
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
			KeychainItem: item.Name(),
			Wrote:        wrote,
			Refused:      []string{},
			Warnings:     []string{},
		}})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "revoked %s", pairCell(device.UID))
	if device.Name != "" {
		fmt.Fprintf(out, " (%s)", pairCell(device.Name))
	}
	fmt.Fprintf(out, ": deleted %s, so the phone is off %s's allowlist from its next request on\n", wrote[0], lc.Name)
	fmt.Fprintf(out, "deleted this laptop's OS keychain item %s\n", item.Name())
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

// pairRefusalCode is the wire code for a refusal at a call site that can fail
// both ways: the site's own code when the operator can fix it there, and the
// unreachable one when the failure is remote — so a console is never told the
// project is unselectable when the answer was the network.
func pairRefusalCode(err error, local string) string {
	if ExitCode(err) == ExitRemoteError {
		return pairRefusedUnreachable
	}
	return local
}

func pairRoleNames() string {
	names := make([]string, 0, len(mobileauth.Roles()))
	for _, r := range mobileauth.Roles() {
		names = append(names, string(r))
	}
	return strings.Join(names, ", ")
}

func pairName(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return name
}
