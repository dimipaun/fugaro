package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/mobileauth"
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
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedDelivery),
			pairUndoPairing(ctx, db, lc, device.UID, item, dbErr(lc, err, true)))
	}

	// §7.1 step 9, decision 2: the whole subtree goes, so the payload does not
	// sit readable for the hour the p~ ID token lives. It is the node alone —
	// the Auth user needs roles/firebaseauth.admin, which no Terraform grant
	// gives an operator, and decision 1 hands it to the history sweep.
	if err := db.Patch(ctx, "pairing", map[string]any{uid: nil}); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return pairRefuse(ev, o, pairRefusedDatabaseAdmin,
				fmt.Errorf("the deletion of %s was refused: the delivery is written and the device is paired, but the pairing node could not be removed, so the payload stays readable to the pairing token for as long as its ID token lives; deleting it needs roles/firebasedatabase.admin on project %s's Firebase project: %w", pairPath(uid), lc.Name, err))
		}
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedDelivery),
			fmt.Errorf("%w; the delivery is written and the device is paired, but %s could not be removed, so the payload stays readable to the pairing token for as long as its ID token lives", dbErr(lc, err, true), pairPath(uid)))
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

// renderPairQR draws the payload as the QR the phone scans. The module tree
// carries no QR encoder and go.mod may not gain one before the owner approves
// it, so this build prints the payload — the code's own contents, so a person
// or a console can still carry it — and says the drawing is missing, rather
// than showing something that is not a QR code.
func renderPairQR(w io.Writer, payload string) error {
	if _, err := fmt.Fprintln(w, payload); err != nil {
		return userErr("writing the QR payload: %v", err)
	}
	if _, err := fmt.Fprintln(w, "(the QR image is not drawn in this build; the payload line above is what it would carry)"); err != nil {
		return userErr("writing the QR payload: %v", err)
	}
	return nil
}

// pairRefuse is the command's one way out. Under --json it writes the refused
// event first, so the stream says what happened before the exit code does, and
// either way it exits with §4.6's class for that refusal.
func pairRefuse(ev *pairEventWriter, o *pairOptions, code string, err error) error {
	reason := oneLine(err.Error())
	if o.asJSON {
		if err := ev.emit(pairEvent{Event: pairEventRefused, Code: code, Reason: reason}); err != nil {
			return err
		}
	}
	return &ExitError{Code: ExitCode(err), Err: errors.New(reason)}
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
