// The never-list as code (mobile plan task P5.1c;
// docs/mobile/design/always-on-orchestrator.md section 6).
//
// WHAT THIS FILE IS, AND WHAT IT IS NOT. Section 6 is eleven things the
// resident orchestrator never does — no IAM, no managed applies, no
// destructive command, no model credential of its own, no secret value, never
// `belong`, never an unverified message, never its own prompt answered, never
// wider than itself, never a second installation's nodes, never the launchers'
// list — and section 4.3 says which of them the INSTALLATION already enforces:
// the account holds none of those roles, its bucket write condition stops at
// three prefixes, and the module grants its rows one binding at a time rather
// than joining it to var.launchers. None of that is repeated here. What is
// repeated here is the other half: the same eleven as the refusals the
// resident process's own code answers with, so a path nobody has written yet
// meets a named rule instead of a symbol that is not there.
//
// A REFUSAL IS NEVER A LOG LINE'S CONTENT. Nothing here repeats what a caller
// handed it: the resident service's log is Cloud Logging
// (internal/cli/orchestrator_serve.go), and the process holds things a phone
// sent. Every message names the rule and the FIELD, never the value —
// internal/safetext is what the names go through, and the rule's own sentence
// is the design's, which carries no caller text by construction.
//
// THE POLARITY, WHICH IS THE PART A READER HAS TO SEE. Seven of the eleven
// have no argument that could make them acceptable — there is no IAM change
// this process may make, no secret value it may read, no second installation's
// node it may touch — so their functions refuse whatever they are handed: they
// stand exactly where a call to the cloud API would stand, and the task that
// reaches for one (P5.1d's launcher, P5.1f's consumer) gets a named refusal
// rather than an API it must remember not to call. The other four judge a fact
// a caller can get wrong — a bucket object's path, a command's name, a
// project's name, a launcher list — and answer nil when the fact is one
// section 6 leaves the resident orchestrator. Both halves are deliberate: a
// rule with a fact is a predicate the pass can hold every write to, and a rule
// without one is a wall.
//
// The list is the design's, in the design's order and in the design's words,
// and it is held to the document rather than to this comment:
// TestNeverRulesAreTheDesignsEleven reads section 6 of
// docs/mobile/design/always-on-orchestrator.md and fails when a rule is added
// on one side and not the other. Each rule has one function below, in the same
// order, and one test in never_test.go.

package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// ErrNever is what every refusal of this file answers errors.Is for. A caller
// can tell a rule of section 6 from an outage without reading a message, which
// is the difference between "this process may not" and "this process could not
// just now" — the two a retry treats differently.
var ErrNever = errors.New("orchestrator: the resident orchestrator never does this")

// Rule is one of section 6's eleven, as the document states it.
type Rule struct {
	// Number is the design's own numbering, 1 to 11. A refusal carries it, so
	// a log line and the section a reader is sent to are the same number.
	Number int
	// Key is a short slug for a line that has no room for the sentence:
	// "no-iam", "never-belong". It is stable and is not a wire format.
	Key string
	// Never is the rule's own sentence, verbatim from section 6, bold markers
	// and trailing full stop included: TestNeverRulesAreTheDesignsEleven
	// compares it to the document's line, so a rewording in one place and not
	// the other fails rather than drifts.
	Never string
}

// neverRules is section 6 in order. TestNeverRulesAreTheDesignsEleven holds it
// to the document, and TestNeverRulesAreNumbered holds the numbering to the
// slice's own order, so Number is never maintained by hand twice.
var neverRules = []Rule{
	{1, "no-iam", "No IAM."},
	{2, "no-managed-applies", "No managed applies."},
	{3, "no-destructive-commands", "No destructive commands."},
	{4, "no-model-credential", "No model credential of its own, ever."},
	{5, "no-secret-value", "Never read a secret value, and never hold the pairing key but the one copy the tag forces."},
	{6, "never-belong", "Never `belong`."},
	{7, "never-unverified", "Never act on an unverified message."},
	{8, "never-own-prompt", "Never answer its own prompt, and never synthesize a confirmation."},
	{9, "never-widen", "Never widen itself."},
	{10, "never-foreign-installation", "Never a second installation's nodes."},
	{11, "never-launcher-membership", "Never joined to `var.launchers`."},
}

// NeverRules is section 6's eleven, in order. It is a copy: a caller may not
// edit the list a refusal is written from.
func NeverRules() []Rule {
	return append([]Rule(nil), neverRules...)
}

// Refusal is one rule of section 6 refusing one thing. What is what the caller
// asked for, in the caller's own words and never a value the caller sent:
// every constructor below takes it through safetext.OneLine.
type Refusal struct {
	Rule Rule
	What string
}

// Error names the rule, its number and what was refused — and never a value.
func (r *Refusal) Error() string {
	return fmt.Sprintf("orchestrator: %s (never-list %d, %s): %s", r.Rule.Never, r.Rule.Number, r.Rule.Key, safetext.OneLine(r.What))
}

// Unwrap is ErrNever, so errors.Is(err, orchestrator.ErrNever) answers for
// every rule without a caller matching on the message.
func (r *Refusal) Unwrap() error { return ErrNever }

// refuse builds the refusal of the numbered rule. The number is looked up
// rather than carried, so a function below cannot name a rule that is not the
// one it stands for.
func refuse(number int, what string) error {
	for _, rule := range neverRules {
		if rule.Number == number {
			return &Refusal{Rule: rule, What: what}
		}
	}
	// Unreachable: the only callers are the eleven below, and every number
	// they pass is one of the list's (TestNeverRulesAreNumbered). It is an
	// error rather than a panic because this file's whole job is to fail
	// closed, and a rule that could not be named is still a refusal.
	return fmt.Errorf("%w: rule %d, which is not one of section 6's eleven", ErrNever, number)
}

// NeverIAM is rule 1: no IAM.
//
// A wall, and deliberately so. The account holds no setIamPolicy anywhere and
// no iam.roles.* (section 4.3's first bullet), so there is no argument that
// would make an IAM change acceptable; a future path that reached for one —
// granting itself a role, impersonating the deployer — calls this and is
// refused. what is the change that was asked for.
func NeverIAM(what string) error { return refuse(1, what) }

// NeverManagedApply is rule 2: no managed applies.
//
// A predicate, because the rule is a PATH rule and not an act rule: what the
// resident orchestrator may write to the runs bucket is the launchers' three
// prefixes and nothing else — the bucket root, the managed marker and
// fugaro/allow-delete.json are outside its object write condition
// (internal/backend/gcp's LauncherBucketCondition, which the installation
// module binds and neverLauncherPrefixes below mirrors). target is the object's
// key inside the bucket, that is, `fugaro/allow-delete.json`, `runs/<slug>/…`,
// `cache/…`: the refusal is what makes the condition a rule the process can
// read out rather than one it discovers from a 403.
func NeverManagedApply(target string) error {
	key := strings.TrimPrefix(target, "/")
	for _, prefix := range neverLauncherPrefixes {
		if strings.HasPrefix(key, prefix+"/") {
			return nil
		}
	}
	return refuse(2, "writing "+target+" in the runs bucket, which is outside the launchers' three prefixes ("+strings.Join(neverLauncherPrefixes, "/, ")+"/) and is where the managed marker and fugaro/allow-delete.json live")
}

// neverLauncherPrefixes are the three prefixes the launchers' condition admits
// a write under (rules 2 and 3's shared fact): runs/ for the task, the claim,
// the launch record and the cancel marker, tokens/ for the sealed budget token
// and issues/ for the shared issue store. TestNeverLauncherPrefixesAreTheConditions
// holds them to gcp.LauncherBucketCondition's own expression, so the two
// spellings cannot drift.
var neverLauncherPrefixes = []string{"runs", "tokens", "issues"}

// neverCommands are the commands of this binary the resident orchestrator may
// run through the CLI. The list is closed and it is SHORT on purpose: the
// launcher half (P5.1d) starts an execution through internal/orchestrator's
// own starter and the consumer half (P5.1f) reads the mailbox, so neither
// spawns this binary at all, and what section 4.3 buys the account is that
// `fugaro ls`, `watch` and `cancel` work for a person — not that the service
// runs them.
//
// The D128 amendment's three verbs (cancel, approve, merge) are NOT here and
// are not added by a later widening of this list: they are the spoken
// confirmation's (internal/mailbox's ConfirmStep, P5.1f), and that path
// carries the confirmation as its own fact rather than a name.
var neverCommands = []string{"ls", "watch", "state", "run"}

// NeverCommand is rule 3: no destructive commands.
//
// A predicate over the command's NAME, and a closed list rather than a list of
// forbidden verbs: a deny-list is a list of the commands somebody thought of,
// and section 6's own sentence is that "this list is not widened". name is the
// bare command word a caller is about to run, e.g. "allow-delete", "budget",
// "secrets", "init", "restore", "ls".
func NeverCommand(name string) error {
	for _, admitted := range neverCommands {
		if name == admitted {
			return nil
		}
	}
	return refuse(3, "running `fugaro "+name+"`, which is not one of the commands the resident orchestrator runs ("+strings.Join(neverCommands, ", ")+"): allow-delete, budget's writes, secrets set/rm, the rules deploy and restore are destructive, and the D128 three are the spoken confirmation's")
}

// NeverModelCredential is rule 4: no model credential of its own, ever.
//
// A wall. What it stands for is the absence of a mount and of an accessor on
// every model, git and App secret (section 4.4), and a path that reached for
// one is the change this refuses. what is the credential or the grant asked
// for.
func NeverModelCredential(what string) error { return refuse(4, what) }

// NeverSecretValue is rule 5: never read a secret value, and never hold the
// pairing key but the one copy the tag forces.
//
// A wall, and the copy is not its exception: the pairing key's per-installation
// Secret Manager copy is P5.1e's, whose revocation is section 4.7's step 2b,
// and the entry that mounts it lands there rather than here. Until then this
// process holds no secret read at all, and a name that reached this function
// is a name nobody may read. name is the secret's ID.
func NeverSecretValue(name string) error {
	return refuse(5, "reading the value of secret "+name+": this process lists secrets and versions and reads no payload")
}

// neverBelongFamily is the project name F31 fences, and the family around it:
// a sandbox or a fixture named after it is refused too (check 302 refuses
// `belong | belong-*` by name).
const neverBelong = "belong"

// NeverBelong is rule 6: never `belong`.
//
// A predicate over a NAME rather than a wall, because the rule is about what
// the resident orchestrator may be pointed at: `belong` is the owner's own
// installation, and no target, fixture or test name of this work may be it
// (F31). It answers nil for any name that is not that family.
func NeverBelong(name string) error {
	if name == neverBelong || strings.HasPrefix(name, neverBelong+"-") {
		return refuse(6, "the project name "+name+", which is the owner's own installation (F31): the resident orchestrator is a sandbox or a fixture installation and never belong")
	}
	return nil
}

// NeverUnverified is rule 7: never act on an unverified message.
//
// A wall, and it is a wall in the direction that matters: the caller that
// reaches it has a message with no verified MAC — a `message`, an `answer`, an
// action record or free text aimed at a destructive prompt — and section 6's
// own sentence is "no MAC, no action". It is a rule in the consumer's own code
// rather than a mechanism (against E4, which holds the key, it is not a
// fence), which is why it is paired with the least-privilege rules around it
// rather than trusted alone. kind is what arrived unverified.
func NeverUnverified(kind string) error {
	return refuse(7, "acting on "+kind+" whose tag did not verify: no MAC, no action")
}

// NeverOwnPrompt is rule 8: never answer its own prompt, and never synthesize
// a confirmation.
//
// A wall, and section 6 says why it is a promise rather than a mechanism: an
// attacker who is the process can do both (E4). What it buys is that no
// ordinary path answers a question this process asked itself, or turns text
// into the confirmation the D128 amendment requires. what is the prompt or the
// confirmation asked for.
func NeverOwnPrompt(what string) error {
	return refuse(8, what+": the resident orchestrator answers no prompt of its own and synthesizes no confirmation — the D128 three reach a person's spoken confirmation or they do not happen")
}

// NeverSelfWidening is rule 9: never widen itself.
//
// A wall. No self-deploy, no new revision from a phone request, no Terraform,
// no workflow file, no rules change from the mailbox: every deploy is the
// owner's (section 4.1's own bullet, and part of the HARD RULE that `fugaro
// update` is the whole upgrade). what is the change asked for.
func NeverSelfWidening(what string) error {
	return refuse(9, what+": the resident orchestrator deploys nothing, writes no Terraform, no workflow file and no database rules")
}

// NeverForeignInstallation is rule 10: never a second installation's nodes.
//
// A predicate over the two names a fact is compared across — project is what a
// resource, a document or a database URL says it belongs to, want is this
// installation's own name for the same thing — and it fails closed on the
// empty want, because a process that does not know which installation it is
// cannot tell whose node it is holding.
func NeverForeignInstallation(project, want string) error {
	if want == "" {
		return refuse(10, "comparing against an installation nobody named: a process that does not know which installation it is serving acts on no node at all")
	}
	if project != want {
		return refuse(10, safetext.Field(project)+" is not this installation's "+safetext.Field(want)+": one service account, one installation's project, one mailbox")
	}
	return nil
}

// NeverLauncherMembership is rule 11: never joined to `var.launchers`.
//
// A predicate over a member and a list, because the rule is a lookup and the
// fact it judges is a document: being listed in var.launchers would
// additionally hand the account roles/firebasedatabase.viewer — a
// rules-bypassing read of every mailbox in the installation — plus
// roles/datastore.viewer and roles/serviceusage.serviceUsageConsumer
// (deploy/terraform/gcp/modules/firebase/iam.tf, and the installation module
// grants section 4.3's rows one binding at a time instead). member is the
// principal a binding names, launchers the list a plan or a document renders.
func NeverLauncherMembership(member string, launchers []string) error {
	for _, launcher := range launchers {
		if member == launcher {
			return refuse(11, safetext.Field(member)+" is a member of var.launchers, which would add roles/firebasedatabase.viewer, roles/datastore.viewer and roles/serviceusage.serviceUsageConsumer beside section 4.3's rows")
		}
	}
	return nil
}
