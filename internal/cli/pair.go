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
	"github.com/dimipaun/fugaro/internal/mobileauth"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro pair (plan §4.1, §7.1): pair a phone with this installation. This is
// the command's surface, its one-shot pairing token, the QR's payload, the
// wait for the phone's key and the comparison of the typed code that writes
// the device node; the delivery to the phone, the MAC key and the pairing
// node's deletion are T3's, and the command is not yet on the root (T4 wires
// it).

// pairNow is the command's clock. It is a variable for the same reason
// budgetNow is: a test pins the stream's timestamps and the token's exp.
var pairNow = time.Now

// pairMeLookup is who the command thinks it is: localcfg's own reader, and a
// variable because an identity that cannot be read has a refusal of its own
// (and a class of its own, exit 2 when the failure is remote) rather than the
// "you are on neither list" one a dropped error would produce.
var pairMeLookup = func(ctx context.Context, lc *localcfg.Config) (string, error) { return lc.Me(ctx) }

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
	// The comparison is between canonical forms — own comes back lower-cased
	// and trimmed from userEmail, and NormalizeEmail folds the domain — so a
	// typed address differing anywhere else is a different address and is
	// refused rather than repaired.
	own, err := authenticatedUser(ctx, lc)
	if err != nil {
		// The check reads the address from the identity endpoint, so it can
		// fail remote as well as by answering "not verified": §4.6 puts an
		// endpoint that could not be reached in the exit-2 row, and an
		// operator told their address is unverified when the network was the
		// answer would go and fix the wrong thing.
		return pairRefuse(ev, o, pairRefusalCode(err, pairRefusedEmail), err)
	}
	email := strings.TrimSpace(o.email)
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

	if o.asJSON {
		if err := ev.emit(pairEvent{Event: pairEventCode, UID: uid}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "the phone shows %s\ntype that code: ", fingerprint)
	}
	typed, err := pairReadCode(waitCtx, cmd.InOrStdin())
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

	if o.asJSON {
		return ev.emit(pairEvent{Event: pairEventDone, UID: uid, Document: &pairDocument{
			Project:  lc.Name,
			At:       now.UTC().Format(time.RFC3339),
			Pairing:  pairPairing{UID: uid, State: pairStateCompleted, ExpiresAt: expiresAt},
			Request:  pairRequest{KeyFingerprint: fingerprint, At: pairRequestAt(request.At)},
			Device:   pairDevice{UID: device.UID, Name: o.name, Email: email, Role: string(role)},
			Wrote:    []string{rules.PathDevices + "/" + device.UID},
			Refused:  []string{},
			Warnings: []string{},
		}})
	}
	fmt.Fprintf(out, "paired %s: device %s, role %s, administered email %s, code %s\n", pairName(o.name), device.UID, role, email, fingerprint)
	fmt.Fprintf(out, "wrote %s\n", rules.PathDevices+"/"+device.UID)
	fmt.Fprintf(out, "the delivery to the phone, the MAC key and the pairing node's deletion are the rest of P6.6 and are not in this build yet: nothing reached the phone\n")
	return nil
}

// pairPathRequest is the phone's half of the pairing session (§4.4): one node
// per pairing uid, under the root-level pairing tree, where the phone writes
// its X25519 public key once. The template carries `.read: false` on it, so
// the CLI reads it with database admin and no token — the pairing token
// included — can read the key at all.
func pairPathRequest(uid string) string { return "pairing/" + uid + "/request" }

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
		var request pairRequestNode
		found, err := db.Get(ctx, path, &request)
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
		case found && request.Key != "":
			return request, nil
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
