package cli

// `fugaro orchestrator`: the project's central orchestrators (central-
// orchestrator plan P2, decisions D6 and D12). The spec's access-control line
// is *"per-project allowlist of who may use which central orchestrator,
// matching the phone app's pairing and allowlist model"*, and that sentence is
// the whole design of this file: there are two documents, they are written by
// two different things, and the rules read only one of them.
//
//   - the **authorisation** is the device node `config/admin/devices/<uid>`
//     (internal/budget/rules' PathDevices, mobile plan D166) *extended* with
//     the email an administrator writes and the role that node carries (D6's
//     answer). The deployed rules decide every orchestrators lease against it
//     and against nothing else (rules.go's orchestratorAccess): a uid with such
//     a node and a documented role takes a seat, and a uid with no node, no
//     email or no role takes none. This file writes the two administered
//     fields — never the pairing half, which `fugaro pair` writes (plan task
//     P6.6, not built);
//   - the **admission and the cap** are the published policy's `orchestrators`
//     block (internal/projectpolicy's orchestrators.go): which identities this
//     project has admitted to a named orchestrator, and what one of that
//     orchestrator's jobs may spend.
//
// The second is not the first's gate, deliberately: the rules do not read the
// policy when they judge a lease (the orchestratorAccess comment states why,
// and what the residual costs — an administered identity may hold a seat on any
// orchestrator's job). So the two can disagree, and `ls` is the command whose
// job it is to show that they do: it reads both and names every state in which
// a seat an operator believes exists does not (or the other way round).
//
// Everything here goes through the operator's own credentials (openProjectBudget,
// the client `fugaro budget set` and `project repos add` use): IAM decides, and
// the deployed rules grant no client write to config/*. Nothing on this path
// loads a secret's value, and nothing here prints one: the values it prints are
// an orchestrator's name, a uid, an email its administrator typed and a role
// from the two this release documents.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// newOrchestratorCmd is `fugaro orchestrator`: the project's central
// orchestrators, who may use which, and what they may spend. It is not
// `fugaro orchestrator attach` — an interactive session waits on D5 (plan P5)
// and is not part of this release.
func newOrchestratorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "The project's central orchestrators: who may use which, and what they may spend",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newOrchestratorAddCmd(), newOrchestratorRmCmd(), newOrchestratorLsCmd())
	return cmd
}

// orchestratorOptions is the flag set add and rm share: the cloud selection,
// the identity (`allow`, with `email` and `role` describing its access record)
// and the confirmation rm asks for.
//
// capUSD belongs to add alone and lives here anyway, so that the two commands'
// flags are one struct: the cap is read by the same validation the node's own
// decoder applies (projectpolicy.validOrchestrator), and keeping its flag
// beside the identity's is what makes "add names the identity, the allowlist
// and the cap" one place to read.
type orchestratorOptions struct {
	cloud  cloudOptions
	allow  string  // the uid: the identity admitted by add, removed by rm
	email  string  // the email an administrator writes into the access record
	role   string  // the role the access record carries (add only)
	capUSD float64 // the ceiling on one job, in US dollars (add only)
	yes    bool    // rm: confirm without typing the project's name
}

func newOrchestratorAddCmd() *cobra.Command {
	var o orchestratorOptions
	cmd := &cobra.Command{
		Use:   "add <name> --allow <uid> --email <address> --role member|admin --cap-usd <usd>",
		Short: "Admit an identity to a central orchestrator and administer its access record",
		Long: "Admit one identity to a named central orchestrator and write that identity's\n" +
			"access record, in one command, because the two are one decision (D6, D12): the\n" +
			"policy block holds who this project admits to which orchestrator and the\n" +
			"ceiling on one of that orchestrator's jobs, and the access record\n" +
			"(config/admin/devices/<uid>, plus the email and the role) is what the deployed\n" +
			"rules read on every connection.\n\n" +
			"Everything the command has to be able to name is a flag, and none has a\n" +
			"default: --allow is the identity (a Firebase Auth uid, which is also the\n" +
			"access record's key), --email and --role are what an administrator writes\n" +
			"there (the role is " + rules.RoleOrchestratorMember + " or " + rules.RoleOrchestratorAdmin + "), and\n" +
			"--cap-usd is the ceiling on what one job this orchestrator launches may spend\n" +
			"(automated work carries its own cap, separate from developers' caps). An\n" +
			"identity that is already admitted is re-administered rather than duplicated,\n" +
			"and the cap is replaced: a second add of the same orchestrator widens it.\n\n" +
			"The policy block is written first, under the ETag of the read, and the access\n" +
			"record after it. The order is what a failure leaves behind: a publish that\n" +
			"lands in between refuses this write and the record is not touched, where the\n" +
			"other order would administer an identity no entry admits — and an administered\n" +
			"identity is what the rules grant a seat to. A failure between the two writes\n" +
			"is named as what it is, with the block written and the seat not yet reachable.\n\n" +
			"Administration is not pairing: the device node is a phone's record and this\n" +
			"command writes two fields of it, so an identity with no node at all has none\n" +
			"to write into and the rules admit it to nothing — the node is written by\n" +
			"pairing (fugaro pair, plan task P6.6, not built yet), and by hand until then.\n" +
			"Writing a node here would be creating a pairing record, which is a different\n" +
			"decision with a wider blast radius (the phone's own families).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runOrchestratorAdd(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.allow, "allow", "", "the uid admitted to this orchestrator (the identity, with --email and --role)")
	f.StringVar(&o.email, "email", "", "the email an administrator writes into the access record")
	f.StringVar(&o.role, "role", "", "the role the access record carries: "+rules.RoleOrchestratorMember+" or "+rules.RoleOrchestratorAdmin)
	f.Float64Var(&o.capUSD, "cap-usd", 0, "the ceiling on what one job this orchestrator launches may spend, in US dollars")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newOrchestratorRmCmd() *cobra.Command {
	var o orchestratorOptions
	cmd := &cobra.Command{
		Use:   "rm <name> [--allow <uid>] [--yes]",
		Short: "Stop admitting an identity (or a whole orchestrator) and take its access away",
		Long: "Remove one entry from the published policy's orchestrators block, or one\n" +
			"identity from an entry with --allow. Both take the administration away with\n" +
			"the admission — the email and the role are deleted from the access record of\n" +
			"every identity no remaining entry names — because an administered identity is\n" +
			"what the deployed rules grant a seat to: removing the entry alone would leave\n" +
			"an identity that still takes the lease of any orchestrator, which is the state\n" +
			"'fugaro orchestrator ls' reports as drift.\n\n" +
			"The pairing half of the record is left exactly as it is: an identity removed\n" +
			"here keeps every family its phone had (its caps, the mailbox, the dashboard)\n" +
			"and loses the orchestrators lease, which is the blast radius D6's one-node\n" +
			"record is chosen for.\n\n" +
			"Removing the last identity of an entry with --allow is refused: an entry that\n" +
			"admits nobody is a policy about nothing, so the whole entry is what goes.\n\n" +
			"It asks you to type the project's name, because taking a seat away narrows who\n" +
			"may act on the project's jobs; --yes confirms without it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runOrchestratorRm(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.allow, "allow", "", "remove only this uid from the entry, keeping the orchestrator")
	f.BoolVar(&o.yes, "yes", false, "confirm without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newOrchestratorLsCmd() *cobra.Command {
	var o orchestratorOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the project's central orchestrators, who they admit, and what is administered",
		Long: "Print every central orchestrator the published policy names with its cap, the\n" +
			"identities it admits, and the access record the database holds for each of\n" +
			"them — the email, the role, or the state that keeps the rules from granting a\n" +
			"seat (no node at all, or a node nobody administered).\n\n" +
			"Every identity the database has administered and no orchestrator names is\n" +
			"printed too: the rules grant such an identity the lease of *any* orchestrator,\n" +
			"because the grant reads the access record and not this block, so it is a seat\n" +
			"nobody admitted and nothing but this report takes away.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorLs(cmd, &o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// --------------------------------------------------------------- add

// runOrchestratorAdd writes one identity's admission and its access record.
//
// It is a writer of the published policy, so it takes every refusal `policy
// publish` and `project repos add` take before it writes anything: the shared
// config and a database that is not this project's (openProjectBudget), the
// deployed rules marker (requirePolicyRules — a policy written to a deployment
// whose rules predate the policy nodes is one the runs it bounds cannot read),
// and a node written by a newer Fugaro (refuseNewerPolicy, P8).
//
// The two writes are ordered, and the order is the failure's shape: the block
// first, under the read's ETag, and the access record only once that write
// landed. The other order administers an identity that no entry admits when the
// policy write is refused, and an administered identity is precisely what the
// deployed rules grant a seat to — the installation would be looser than the
// operator asked for. This order leaves it tighter: an admitted identity whose
// record is missing is refused every seat, which is what `ls` then reports.
func runOrchestratorAdd(cmd *cobra.Command, o *orchestratorOptions, name string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	// Everything the command has to be able to name is checked before the
	// database is read: a refusal about a flag is the operator's typo, and it
	// should not depend on a network round trip to be told.
	email, err := accessEmail(o.email)
	if err != nil {
		return err
	}
	if err := orchestratorRole(o.role); err != nil {
		return err
	}
	if err := orchestratorCap(o.capUSD); err != nil {
		return err
	}
	if err := orchestratorIdentity(o.allow); err != nil {
		return err
	}
	if err := projectpolicy.ValidOrchestratorName(name); err != nil {
		return userErr("%v", err)
	}
	lc, db, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return errNoOrchestratorPolicy(lc)
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	// The device node has to be there, and this command does not create one:
	// the node is a phone's pairing record (D166) and `add` writes two fields
	// of it, so a uid with no node is an identity this project has not paired —
	// and a command that made one would be pairing, skipping the checks pairing
	// owns (the verified email, D6 sub-question (c)). It is read before anything
	// is written, so the operator hears about the missing node rather than about
	// a half-written installation.
	rec, err := readAccessRecord(ctx, db, lc, o.allow)
	if err != nil {
		return err
	}
	if !rec.found {
		return userErr("no device node at %s: the access record is the phone's own pairing record with the email and role an administrator writes, and this command administers an existing one rather than creating it — nothing mints a device node yet ('fugaro pair' is plan task P6.6 and is not built), so today it is written by hand", accessRecordPath(o.allow))
	}
	before, had := p.OrchestratorFor(name)
	// The identity is admitted once: an entry that lists a uid twice is a node
	// the decoder refuses, so a second add of an identity already on the list
	// re-administers it and changes nothing else.
	allow := slices.Clone(before.Allow)
	if !slices.Contains(allow, o.allow) {
		allow = append(allow, o.allow)
		slices.Sort(allow)
	}
	block := make(map[string]projectpolicy.Orchestrator, len(p.Orchestrators))
	for n, e := range p.Orchestrators {
		block[n] = e
	}
	block[name] = projectpolicy.Orchestrator{CapUSD: o.capUSD, Allow: allow}
	patched, err := patchPolicyJSON(raw, "orchestrators", block)
	if err != nil {
		return userErr("editing %s: %v; nothing was written", projectpolicy.PathShared(), err)
	}
	if err := checkPolicyBytes(patched); err != nil {
		return err
	}
	if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(patched)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy changed while this ran; nothing was written: run it again")
		}
		return dbErr(lc, err, true)
	}
	// The block is the node now; the record is the second write. Its failure is
	// reported as the half-written state it is rather than as a failed command:
	// the admission is published, the seat is not reachable until the record is.
	if err := writeAccessRecord(ctx, db, lc, o.allow, email, o.role); err != nil {
		return fmt.Errorf("the orchestrators block is written, but the access record is not: %w; the rules refuse this identity every orchestrator's lease until it is — run 'fugaro orchestrator add' again", err)
	}
	// The report says what moved rather than what was written: an add that
	// re-administers an identity already on the list (the way an operator fixes
	// a wrong role) changes nothing in the block, and a line reading
	// "[alice-uid] → [alice-uid]" would claim a widening that did not happen.
	if !had {
		fmt.Fprintf(out, "orchestrators.%s: cap_usd %s, allow %v\n", name, capText(o.capUSD), allow)
	} else {
		if before.CapUSD != o.capUSD {
			fmt.Fprintf(out, "orchestrators.%s.cap_usd: %s → %s\n", name, capText(before.CapUSD), capText(o.capUSD))
		} else {
			fmt.Fprintf(out, "orchestrators.%s.cap_usd: %s (unchanged)\n", name, capText(o.capUSD))
		}
		if slices.Contains(before.Allow, o.allow) {
			fmt.Fprintf(out, "orchestrators.%s.allow: %v (already admitted)\n", name, allow)
		} else {
			fmt.Fprintf(out, "orchestrators.%s.allow: %v → %v\n", name, before.Allow, allow)
		}
	}
	fmt.Fprintf(out, "access record %s/%s: email %s, role %s\n", rules.PathDevices, o.allow, oneLine(email), o.role)
	fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
	fmt.Fprintf(out, "set %s/%s\n", rules.PathDevices, o.allow)
	return nil
}

// errNoOrchestratorPolicy is the refusal a project with nothing published gets
// from both writers. It is `project repos add`'s sentence with this command's
// own remedy in it: what is missing is the node these commands edit, and the
// remedy is the command that writes one.
func errNoOrchestratorPolicy(lc *localcfg.Config) error {
	return userErr("no policy is published for project %s (%s): 'fugaro orchestrator add' writes the orchestrators block of the published policy, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
}

// accessEmail is the administered email, normalized and checked: D6's answer
// puts the email in the access record as a field an *administrator* writes,
// which makes this the one place its shape is decided — the deployed rules can
// bound the field's length and nothing else (rules.MaxEmailLength), so an
// address that is not an address would otherwise reach the database and read as
// an administration.
//
// Three things are checked, and the third is the domain rule D6's answer moves
// to administration time: exactly one "@", both sides non-empty, and a domain
// with a dot in it. Nothing here asks whether the address is *verified* or
// belongs to the project's own domain — the first is the pairing half's
// (D6 sub-question (c), `fugaro pair`, plan task P6.6) and the project has no
// configured domain for the second in this release, which the pull request
// states rather than pretending to check. What is checked here is the shape,
// and the shape is checked against the operator's own typing: the value is
// echoed back on the line below the write, so a typo is visible to the person
// who made it.
func accessEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	switch {
	case email == "":
		return "", userErr("--email is what an administrator writes into the access record, and it is empty: the deployed rules require a non-empty email on %s/<uid> and refuse the identity every orchestrator's lease without one", rules.PathDevices)
	case len(email) > rules.MaxEmailLength:
		return "", userErr("--email is %d characters; the deployed rules bound the recorded email at %d (RFC 5321's longest forward path), so a longer address is refused on the node rather than stored", len(email), rules.MaxEmailLength)
	case strings.Count(email, "@") != 1:
		return "", userErr("--email %q is not an email address: it must carry exactly one @", oneLine(email))
	}
	local, domain, _ := strings.Cut(email, "@")
	switch {
	case local == "" || domain == "":
		return "", userErr("--email %q is not an email address: both sides of the @ must name something", oneLine(email))
	case !strings.Contains(domain, "."):
		return "", userErr("--email %q is not an email address: its domain has no dot in it, and the domain is the half D6 puts at administration time — write the address this identity signs in with", oneLine(email))
	case strings.ContainsAny(email, " \t\r\n,;:<>\"'\\/[]"):
		return "", userErr("--email %q is not an email address: it carries a separator or a control character", oneLine(email))
	}
	return email, nil
}

// orchestratorRole checks the role the access record carries. The vocabulary is
// the rules' own (internal/budget/rules' RoleOrchestratorMember and
// RoleOrchestratorAdmin), so a role this command accepts is one the deployed
// predicate admits and no other value is written into a node whose reader
// compares it literally.
func orchestratorRole(role string) error {
	switch role {
	case rules.RoleOrchestratorMember, rules.RoleOrchestratorAdmin:
		return nil
	case "":
		return userErr("--role is what the access record carries, and it is empty: the deployed rules admit exactly two roles (%s and %s) and refuse an identity with neither, so there is no default to fall back to", rules.RoleOrchestratorMember, rules.RoleOrchestratorAdmin)
	}
	return userErr("--role %q is not a role this release documents: the deployed rules admit %s and %s, and a value they do not name is an identity admitted nowhere", oneLine(role), rules.RoleOrchestratorMember, rules.RoleOrchestratorAdmin)
}

// orchestratorCap checks the ceiling on one of the orchestrator's jobs. It is
// the policy node's own check (projectpolicy.validOrchestrator refuses a
// non-positive cap and one that rounds to nothing), made here as well so the
// operator reads which *flag* is wrong rather than a decode error about the
// document the command built: automated work carries its own cap (the spec's
// own words), and there is no default.
func orchestratorCap(usd float64) error {
	if usd <= 0 {
		return userErr("--cap-usd is the ceiling on what one job this orchestrator launches may spend, and %v is not one: an automated job carries its own cap, separate from developers' caps, and there is no default (a job nobody bounded is a job nobody capped)", usd)
	}
	micros, err := pricing.FromUSD(usd)
	if err != nil {
		return userErr("--cap-usd %v: %v", usd, err)
	}
	if micros < 1 {
		return userErr("--cap-usd %v rounds to nothing: the smallest cap is $0.000001", usd)
	}
	return nil
}

// orchestratorIdentity checks the uid the entry admits. The uid is the access
// record's *key* — projectpolicy.ValidOrchestratorUID is that check, and it is
// the node's own, so a uid this command accepts is one the entry can hold and
// the database can key.
func orchestratorIdentity(uid string) error {
	if uid == "" {
		return userErr("--allow names the identity this orchestrator admits, and it is empty: an entry with nobody on it admits nobody, and the rules grant a seat only to a named, administered identity")
	}
	if err := projectpolicy.ValidOrchestratorUID(uid); err != nil {
		return userErr("%v", err)
	}
	return nil
}

// capText is one cap for people: the money spelling the budget commands use,
// from the micros the policy stores.
func capText(usd float64) string {
	micros, err := pricing.FromUSD(usd)
	if err != nil {
		return fmt.Sprintf("$%v", usd)
	}
	return money(budget.Micros(micros))
}

// --------------------------------------------------------------- the record

// accessRecordPath is one identity's access record: the device node D6 extends.
func accessRecordPath(uid string) string { return rules.PathDevices + "/" + uid }

// writeAccessRecord writes the two administered fields of one identity's node,
// as a multi-path update rather than a whole-node PUT: the pairing half
// (`pairedAt` and anything a later release adds beside it) is not this
// command's to rewrite, and a PUT would drop it.
func writeAccessRecord(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, uid, email, role string) error {
	err := db.Patch(ctx, "", map[string]any{
		accessRecordPath(uid) + "/email": email,
		accessRecordPath(uid) + "/role":  role,
	})
	if err != nil {
		return dbErr(lc, err, true)
	}
	return nil
}

// unadministerAccessRecord removes the two administered fields, leaving the
// pairing half (and the node) alone: `rm` takes the orchestrators lease away
// and nothing else, because the same node is the phone's authorisation for
// every other family it has (D166). A nil value deletes the child, and the
// rules' ORCH conjunct then fails on the node exactly as it does for one that
// was never administered — there is no other state to put it in.
func unadministerAccessRecord(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, uid string) error {
	err := db.Patch(ctx, "", map[string]any{
		accessRecordPath(uid) + "/email": nil,
		accessRecordPath(uid) + "/role":  nil,
	})
	if err != nil {
		return dbErr(lc, err, true)
	}
	return nil
}

// accessRecord is one node as this file reads it: the two administered fields
// and whether the node is there at all.
type accessRecord struct {
	found bool
	email string
	role  string
}

// readAccessRecord reads one identity's node. It is a read of the admin-only
// config/admin subtree with the operator's own credentials (the only ones that
// have it: the deployed rules grant no client read there).
func readAccessRecord(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, uid string) (accessRecord, error) {
	var node struct {
		Email *string `json:"email"`
		Role  *string `json:"role"`
	}
	found, err := db.Get(ctx, accessRecordPath(uid), &node)
	if err != nil {
		return accessRecord{}, dbErr(lc, err, false)
	}
	rec := accessRecord{found: found}
	if node.Email != nil {
		rec.email = *node.Email
	}
	if node.Role != nil {
		rec.role = *node.Role
	}
	return rec, nil
}

// administered reports whether the deployed rules grant this identity a seat:
// the node exists, the email is a non-empty string and the role is one of the
// two the rules name. It is orchestratorAccess' predicate, spelled here so that
// `ls` and the rules cannot disagree about who is reachable — the state of a
// node with a role and no email reads as "not administered" on both sides.
func (r accessRecord) administered() bool {
	return r.found && r.email != "" && (r.role == rules.RoleOrchestratorMember || r.role == rules.RoleOrchestratorAdmin)
}

// recordText is the record as one line, or the state that keeps the rules from
// granting a seat. The two unadministered states are told apart because their
// remedies differ: a node that is not there at all is one pairing has not
// written (or one an operator removed), and a node with no email or no role is
// one an `add` was meant to write.
func recordText(uid string, r accessRecord) string {
	switch {
	case !r.found:
		return fmt.Sprintf("%s  no access record at %s: the rules refuse it every orchestrator's lease", uid, accessRecordPath(uid))
	case r.administered():
		return fmt.Sprintf("%s  %s  %s", uid, oneLine(r.email), r.role)
	default:
		return fmt.Sprintf("%s  paired, not administered: the rules refuse it every orchestrator's lease", uid)
	}
}

// --------------------------------------------------------------- rm

// runOrchestratorRm removes an entry, or one identity from it, and takes the
// administration away with it.
//
// The removal is the mirror of `add`'s two writes, and it is ordered the other
// way round for the same reason: the admission goes first, and an identity is
// unadministered only once no remaining entry names it. A failure between the
// two leaves an identity that is still administered to a seat nothing admits —
// reported by `ls` — rather than one whose seat was taken away while the entry
// that admits it is still published, which no command would show.
//
// The confirmation and the `--yes` flag are `project repos rm`'s
// (confirmRemoval): a removal narrows who may act on the project's jobs, and a
// piped run is refused rather than taken as consent.
func runOrchestratorRm(cmd *cobra.Command, o *orchestratorOptions, name string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	lc, db, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return errNoOrchestratorPolicy(lc)
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	entry, ok := p.OrchestratorFor(name)
	if !ok {
		return userErr("%s is not a central orchestrator of this project (%s names %v): nothing to remove", name, projectpolicy.PathShared(), p.OrchestratorNames())
	}
	block := make(map[string]projectpolicy.Orchestrator, len(p.Orchestrators))
	for n, e := range p.Orchestrators {
		block[n] = e
	}
	var what string
	drop := ""
	if o.allow == "" {
		// The whole entry: every identity it admits loses the seat, and the
		// ones no other entry names lose the record with it.
		drop = name
		what = fmt.Sprintf("removing %s (%s) admits nobody to it; its identities lose the orchestrators lease (their access record goes with it, unless another orchestrator names them)", name, projectpolicy.PathShared())
	} else {
		if !slices.Contains(entry.Allow, o.allow) {
			return userErr("%s is not admitted to %s: %s holds %v, so there is nothing to remove", o.allow, name, name, entry.Allow)
		}
		if len(entry.Allow) == 1 {
			return userErr("%s is the only identity %s admits, and an entry that admits nobody is a policy about nothing: remove the entry instead ('fugaro orchestrator rm %s')", o.allow, name, name)
		}
		what = fmt.Sprintf("removing %s from %s takes that identity's orchestrators lease away (its access record goes with it, unless another orchestrator names it)", o.allow, name)
	}
	if err := confirmRemoval(cmd, lc, o.yes, what); err != nil {
		return err
	}
	if drop != "" {
		delete(block, drop)
	} else {
		kept := slices.DeleteFunc(slices.Clone(entry.Allow), func(u string) bool { return u == o.allow })
		block[name] = projectpolicy.Orchestrator{CapUSD: entry.CapUSD, Allow: kept}
	}
	patched, err := patchPolicyJSON(raw, "orchestrators", block)
	if err != nil {
		return userErr("editing %s: %v; nothing was written", projectpolicy.PathShared(), err)
	}
	if err := checkPolicyBytes(patched); err != nil {
		return err
	}
	if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(patched)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy changed while this ran; nothing was written: run it again")
		}
		return dbErr(lc, err, true)
	}
	// The identities whose record goes with the admission: the ones this
	// removal took off every entry, and no other. An identity a second
	// orchestrator still names keeps its record and its seat there.
	var orphaned []string
	for _, uid := range entry.Allow {
		if drop != "" || uid == o.allow {
			if !namesAny(block, uid) {
				orphaned = append(orphaned, uid)
			}
		}
	}
	if drop != "" {
		fmt.Fprintf(out, "orchestrators.%s: removed (cap_usd %s, allowed %v)\n", name, capText(entry.CapUSD), entry.Allow)
	} else {
		fmt.Fprintf(out, "orchestrators.%s.allow: %v → %v\n", name, entry.Allow, block[name].Allow)
	}
	fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
	for _, uid := range orphaned {
		if err := unadministerAccessRecord(ctx, db, lc, uid); err != nil {
			return fmt.Errorf("the orchestrators block is written, but the access record of %s is not: %w; until it is, the rules still grant that identity every orchestrator's lease, and 'fugaro orchestrator ls' reports it as an identity named by no orchestrator", uid, err)
		}
		fmt.Fprintf(out, "access record %s: administration removed\n", accessRecordPath(uid))
	}
	return nil
}

// namesAny reports whether any entry of the block admits uid. It is the
// question the removal asks before it unadministers an identity: an identity a
// second orchestrator still names keeps its record, because its seat there was
// not taken away.
func namesAny(block map[string]projectpolicy.Orchestrator, uid string) bool {
	for _, e := range block {
		if slices.Contains(e.Allow, uid) {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------- ls

// runOrchestratorLs prints both halves of the decision and every state in which
// they disagree. It is a read, so it opens the database as `project repos ls`
// does (openBudget): no rules-marker gate, because nothing is written, and an
// operator inspecting an installation mid-deploy is exactly who needs to see
// what is there.
func runOrchestratorLs(cmd *cobra.Command, o *orchestratorOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return policyReadErr(err)
	}
	if !found {
		return errNoOrchestratorPolicy(lc)
	}
	// The block is read as the typed policy, and every key of it is one this
	// Fugaro knows: a key it does not is a policy that never takes effect, and
	// `repos ls` refuses that state with the same sentence.
	if err := refuseUnknownPolicyKeys(p); err != nil {
		return err
	}
	admitted := map[string]bool{}
	fmt.Fprintf(out, "orchestrators (%s):\n", projectpolicy.PathShared())
	names := p.OrchestratorNames()
	if len(names) == 0 {
		fmt.Fprintln(out, "  (none: this project runs no central orchestrator)")
	}
	for _, name := range names {
		entry, _ := p.OrchestratorFor(name)
		fmt.Fprintf(out, "  %s  cap_usd %s\n", name, capText(entry.CapUSD))
		if len(entry.Allow) == 0 {
			fmt.Fprintln(out, "    (no identity is admitted: this entry reaches no seat)")
		}
		for _, uid := range entry.Allow {
			if admitted[uid] {
				fmt.Fprintf(out, "    %s  (already listed above)\n", uid)
				continue
			}
			admitted[uid] = true
			rec, err := readAccessRecord(ctx, db, lc, uid)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "    %s\n", recordText(uid, rec))
		}
	}
	orphans, err := administeredAndUnnamed(ctx, db, lc, admitted)
	if err != nil {
		return err
	}
	if len(orphans) == 0 {
		return nil
	}
	fmt.Fprintf(out, "administered and named by no orchestrator (%s):\n", rules.PathDevices)
	fmt.Fprintf(out, "  the rules grant these identities the lease of every orchestrator, because the grant\n"+
		"  reads the access record and not %s:\n", projectpolicy.PathShared())
	for _, o := range orphans {
		fmt.Fprintf(out, "    %s\n", o)
	}
	return nil
}

// administeredAndUnnamed is every identity under config/admin/devices that the
// deployed rules would grant a seat to (accessRecord.administered) and that no
// entry of the block names. It is read as one document rather than one node at
// a time: the question is about the whole allowlist, which is the node `fugaro
// pair` maintains, and reading it whole is one request.
//
// A node that cannot be read as an object of access records is not this
// command's to explain — `ls` prints the drift it can see, and an operator
// whose config/admin node holds something other than records has a hand edit to
// look at. The values are read as strings where they are strings and ignored
// otherwise, which is what the rules do with them too.
func administeredAndUnnamed(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, admitted map[string]bool) ([]string, error) {
	var nodes map[string]struct {
		Email *string `json:"email"`
		Role  *string `json:"role"`
	}
	var raw json.RawMessage
	if _, err := db.Get(ctx, rules.PathDevices, &raw); err != nil {
		return nil, dbErr(lc, err, false)
	}
	if len(raw) > 0 && json.Unmarshal(raw, &nodes) != nil {
		return nil, nil
	}
	var orphans []string
	for uid, node := range nodes {
		rec := accessRecord{found: true}
		if node.Email != nil {
			rec.email = *node.Email
		}
		if node.Role != nil {
			rec.role = *node.Role
		}
		if admitted[uid] || !rec.administered() {
			continue
		}
		orphans = append(orphans, recordText(uid, rec))
	}
	slices.Sort(orphans)
	return orphans, nil
}
