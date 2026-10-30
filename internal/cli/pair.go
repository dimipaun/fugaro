package cli

import (
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
// the command's surface, its one-shot pairing token and the QR's payload; the
// wait for the phone's key, the typed fingerprint and the delivery are the
// rest of P6.6, and the command is not yet on the root (T4 wires it).

// pairNow is the command's clock. It is a variable for the same reason
// budgetNow is: a test pins the stream's timestamps and the token's exp.
var pairNow = time.Now

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
	f.StringVar(&o.email, "email", "", "the administered email for the device (default: your own verified address); it must be an address this installation may administer")
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

	me, _ := lc.Me(ctx)
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

	email := strings.TrimSpace(o.email)
	if email == "" {
		if email, err = authenticatedUser(ctx, lc); err != nil {
			return pairRefuse(ev, o, pairRefusedEmail, err)
		}
	} else if email, err = mobileauth.NormalizeEmail(email); err != nil {
		return pairRefuse(ev, o, pairRefusedEmail, err)
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

	if o.asJSON {
		if err := ev.emit(pairEvent{Event: pairEventQR, Payload: string(payload), UID: uid, ExpiresAt: expiresAt}); err != nil {
			return err
		}
		return ev.emit(pairEvent{Event: pairEventWaiting, TimeoutMS: o.timeout.Milliseconds(), ExpiresAt: expiresAt})
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "device %s: role %s, administered email %s\n", pairName(o.name), role, email)
	if err := renderPairQR(out, string(payload)); err != nil {
		return err
	}
	fmt.Fprintf(out, "the wait for the phone's key and the typed code are not in this build yet: nothing was written to %s\n", lc.Name)
	return nil
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
