package cli

import (
	"fmt"
	"io"
	"strings"
)

// The default view's budget. doctor prints a verdict and then only what the
// reader has to do — one sentence and one fix per item — because the owner
// pasted a full run (seven warnings, about fifteen informational lines, several
// sentences of six hundred characters) and asked who was meant to read it. The
// measurements and the audit findings are still there, behind --all; the
// security ones behind --security as well. An installation with nothing wrong
// prints one line and no pointer (the owner's rule of 2026-10-31): what doctor
// measured is not a finding to announce, so the classes below keep the two
// apart and the unclassified default is the finding.
//
// doctorDefaultMaxLines is what the tests hold the default view to on the state
// the owner pasted, and doctorLineLimit is the width one line may take: a
// terminal wraps at 80 to 120 columns, and a line that has to be scrolled
// sideways is the same failure as a line nobody reads.
const (
	doctorDefaultMaxLines = 25
	doctorLineLimit       = 160
)

// The classes a check falls in. Only the last three reach the default view.
const (
	// doctorClassActionable is something is wrong and the reader fixes it: one
	// sentence, one command or one action.
	doctorClassActionable = "actionable"
	// doctorClassUpdate is something `fugaro update` puts right, so the fix is
	// that one command and never a step of its own.
	doctorClassUpdate = "update"
	// doctorClassDecision is the owner's to choose: the state is valid, and the
	// line names the command for each choice.
	doctorClassDecision = "decision"
	// doctorClassMeasure is what doctor measured or could not measure, with
	// nothing the reader has to do about it: the price catalog's age, the
	// launcher grant count, who can sign a budget token, whether a policy is
	// published. The default view counts none of them and prints none of them —
	// an installation with nothing wrong is one line (the owner's rule of
	// 2026-10-31), and a measurement is not a finding to announce. --all prints
	// them, and --security prints the security half of them.
	doctorClassMeasure = "measure"
	// doctorClassAudit is a finding the default view counts in its pointer line
	// rather than printing: the IAM and security sweep's own (an unverified
	// provider privacy setting, a bucket grant whose shape is not the one this
	// Fugaro writes), and every info check doctor has not classified at all,
	// which is the safe default — so a short output is never read as a clean
	// one, and a check added later is never hidden by omission.
	doctorClassAudit = "audit"
)

// doctorUpdatePrefixes are the checks whose remedy is `fugaro update` itself
// (CLAUDE.md's hard rule: the one documented command brings the installation,
// the policy, the rules, the images and the plugin pin to the new version).
// Matching is by prefix because one of them is a per-instance ID
// (image-config-<workflow>).
//
// The list is what `fugaro update` does today, because the default view may
// not name a command that will not do the work: the converge's own stages are
// preflight, installation, Firebase, images, installation again, secrets,
// plugin and the checkout's repository, rules and policy (init_stages.go), and
// a check whose remedy is none of them is not an update item. One the plan
// means to move here is not here yet:
//
//   - plugin-install: what differs is the plugin Claude Code installed, and
//     the converge's plugin stage rewrites the checkout's pin, not the
//     installation: `fugaro upgrade --local` (or the marketplace command in
//     Claude Code) is what installs it.
//
// policy-older-than-binary is here since the policy stage carries the
// migration: a node in a version the chain reaches is what that stage rewrites
// in place (init_policy_migrate.go), and it took `repository` — the stage in
// front of it — admitting the checkout for the run to get there, which is what
// policyReadOnce.exists now does. The line named `fugaro project policy
// publish --delete` and a publish until then, because projectpolicy.Migrate
// had no production caller. One state keeps that line (ownFix): a node below
// the chain's first step, 1 or 2, which no step reaches and the update refuses
// — the ID is in the class for the state the update does repair, and the line
// is the state's.
//
// policy-rules-skew is here, and its line is the plain `fugaro update` in the
// states that command repairs: the rules stage (init_rules_stage.go, the
// owner's rule of 2026-10-30) deploys this release's document over a marker
// that is absent or below this generation, and over a document that is not the
// one this Fugaro generates — what the warning sent the reader here for. The
// line named --firebase-project until that stage existed, and a bare
// --firebase-project is not a command at all: it is a string flag, so `fugaro
// update --firebase-project` fails on the missing value. Two of its states are
// not the update's work and carry their own line (ownFix): the hand-edited
// marker, which the stage reports rather than deploys over, and a marker above
// this binary's generation, whose fix is the newer binary. And cli-version's
// line names the upgrade first: the newer release is not installed yet, so
// `fugaro update` alone would run the old binary.
var doctorUpdatePrefixes = []string{
	"cli-version",
	"plugin-pin",
	"base-image",
	"image-config-",
	"policy-rules-skew",
	"policy-vs-jobs",
	"policy-older-than-binary",
}

// doctorSecurityPrefixes are the IAM and security sweep's checks: who may read
// a stored credential, who may sign a budget token, what a launcher may start,
// who deploys the credentials service, which job still holds a grant its
// repository's own config no longer gives it. They are audit lines — doctor
// measures them and the owner reviews them — and they move behind --security.
//
// The exception is the rule that binds them: a security finding that is a
// warning is never hidden. One of these IDs reports both a measurement (info)
// and a finding (warning), and a warning one is an item of the default view
// like any other.
var doctorSecurityPrefixes = []string{
	"token-signers",
	"secret-readers-unexpected",
	"runs-bucket-iam",
	"launcher-jobs",
	"job-token-grant",
	"credentials-service-deployers",
	"project-secret-extra-accessors",
	// The Batch launchers' roles/batch.jobsEditor grant: an IAM widening check
	// like the rest of the sweep. It is a warning or nothing, never an info
	// line, so today it reaches the default view either way; naming it here is
	// what keeps it in the sweep if it ever gains a measurement.
	"batch-launcher-iam",
	// model-key-in-agent is the owner's ruling of 2026-10-31: a model
	// credential that reaches a workflow's agent (budget off, or no credentials
	// service) is a counted security finding, not a measurement. Its own line
	// is information — an installation that runs without the budget is a
	// legitimate choice, and D498's report is what the check is — so it is the
	// sweep's info half: counted in the pointer the default view ends with and
	// printed whole, with its fix, by --security and --all.
	"model-key-in-agent",
}

// doctorDecisionIDs are the checks whose state is the owner's to choose: doctor
// must not pick one, and the default view names the command for each choice
// rather than a single fix.
var doctorDecisionIDs = map[string]bool{
	"secret-scope-shadowed":             true,
	"project-secret-scope-stale":        true,
	"project-secret-optional":           true,
	"provider-open-to-every-repository": true,
	"provider-secret-name":              true,
}

// doctorMeasureIDs are the checks whose info-severity line is a measurement
// doctor made, and never something the reader has to do: the price catalog's
// age, the launcher grant count, who can sign a budget token, the repositories
// a project's account token reaches, whether a policy is published, where the
// config lives, what the wiring in .claude/settings.json carries, the tool
// lookups that did not answer.
//
// A read that FAILED is not one of them: "could not be read" says the fact is
// unknown, not that it is fine, and an unknown that is filed as a measurement
// is a silent pass — so a check whose info line is an unread state carries
// doctorCheck.unread, and doctorClassOf counts it instead (creds-broken,
// creds-rules, the policy-rules-skew generation read, job-token-grant's
// unchecked summary, policy-published's three unread variants, and the rest).
//
// The list is explicit, and it is a list rather than a rule (an "info severity
// and not ok" test would swallow them all) because the default of
// doctorClassOf is the safe one: an info check doctor has not classified is a
// finding the pointer line counts, never a measurement nobody hears about.
//
// Measured once, at an info severity: every one of these IDs also has a
// warning-or-worse line that is a finding — a stale catalog, a drift, a
// signer that is not the installation's own account, a bucket grant whose
// shape is not the one this Fugaro writes — and the severity guard in
// doctorClassOf is what keeps those in the default view.
var doctorMeasureIDs = map[string]bool{
	// The update lookup did not answer. Nothing about this installation is
	// wrong, and the newer-release case is the warning variant of the same ID.
	"cli-version": true,
	// pluginwire's own "never a failure" states: not installed, cannot
	// compare. "installed differs" is a warning, and a finding.
	"plugin-install": true,
	// The standing note the wiring always prints beside its per-notice lines:
	// a fixed sentence about what wiring the plugin blesses, not a finding of
	// this checkout's.
	"plugin-settings-caveat": true,
	// The verifier a base pin needs. The check's own comment is the reason it
	// is here: an absent binary "is not a wrong state", and the pin's own
	// refusal names it when a person needs it.
	"cosign":                 true,
	"provider-price-catalog": true,
	"routing-resolution":     true,
	"routing-cache-markers":  true,
	"project-layer":          true,
	"project-layer-drift":    true,
	"shared-config":          true,
	"shared-config-differs":  true,
	"xdg-temp-dir":           true,
	"policy-published":       true,
	// secret-scope is a listing and nothing else: the secrets a checkout's
	// jobs mount, by the copy Fugaro resolves for each. It is emitted for
	// every checkout whose jobs mount any secret, offers no choice (its one
	// line names the command that lists them again), and the "to decide" block
	// is for checks the owner chooses between.
	"secret-scope":                  true,
	"launcher-jobs":                 true,
	"token-signers":                 true,
	"token-signers-owners":          true,
	"token-signers-google-agents":   true,
	"secret-readers-unexpected":     true,
	"credentials-service-deployers": true,
	"batch-artifact-boundary":       true,
	// The standing note about require_zdr: what the flag prices, and that
	// whether an account's calls are held under zero data retention is a
	// privacy setting Fugaro reads nowhere. It is emitted on every
	// installation whose route the catalog prices (require_zdr is on by
	// default), so a class that counted it would put a pointer on a screen
	// with nothing wrong in it — and nothing is: the note names no fact about
	// this installation that a reader could act on. --all and --json carry it
	// whole. The check sets no unread marker for the same reason: the fact it
	// cannot read is the provider's account setting, not a read of this
	// installation that did not answer.
	"provider-zdr-unverified": true,
	// The Batch sweep's "not checked: this is a test configuration" line, one
	// per API it does not read there. The warning variant of the same ID is a
	// read that failed.
	"batch-apis":           true,
	"batch-job-definition": true,
	"batch-image-family":   true,
	"batch-log-sink":       true,
	"batch-network":        true,
	"batch-subnetwork":     true,
}

// doctorMeasurePrefixes are the measurements doctor emits once per instance.
// It is empty, and that is the point: every per-instance ID doctor today
// emits is a finding — the sweep's own (runs-bucket-iam-1, job-token-grant-1,
// token-signers-2), and plugin-settings-<n>, one line per notice of what the
// wiring carries, which printed in the default view before doctor had classes
// and is counted by the pointer line since. The one fixed plugin-settings
// line, plugin-settings-caveat, is an exact ID in the list above; the
// per-notice IDs are deliberately not a prefix here, since a prefix is what
// would hide them.
var doctorMeasurePrefixes []string

// doctorActionableInfoIDs are the informational checks that are an action all
// the same: the fix is one thing the reader runs, and hiding it would hide work.
var doctorActionableInfoIDs = map[string]bool{
	"gcp-project-line": true,
}

// doctorClassOf is the classification, and it is the only place a class is
// decided: doctorCheck carries no class in the code that builds the checks, so
// a check's meaning stays where its sentence is, and --json and the human view
// cannot disagree about what is hidden.
func doctorClassOf(c doctorCheck) string {
	// doctorFinding is the one question every hidden class has to answer no
	// to: does this check fail doctor? checksFail fails on any check that is
	// not ok and whose severity is not "info" — "" fails always, "warning"
	// fails --strict. A check that fails may never be hidden, or the view
	// prints "everything checked is ok" beside an exit code 1.
	finding := !c.OK && c.Severity != "info"
	switch {
	case c.OK && c.Severity == "" && !c.unread:
		// doctor's own bookkeeping: nothing prints it, in any view. A check that
		// is ok AND carries the unread marker is not bookkeeping: its own line
		// says a fact could not be read, and "ok" beside "could not be read" is
		// the silent pass the class rules exist to keep out of the view (the
		// marker is set by a read that did not answer, and the two fields are
		// written by different branches of a check). It falls through to the
		// unread arm below and is counted, never dropped.
		return ""
	case finding && doctorHasPrefix(c.ID, doctorUpdatePrefixes):
		return doctorClassUpdate
	case doctorDecisionIDs[c.ID]:
		// A decision is the owner's whatever its severity, and the default
		// view names the command for each choice: it is never a measurement.
		return doctorClassDecision
	case c.unread && !finding:
		// A check whose own line says a fact could not be read, or is unknown.
		// It is never a measurement whatever its ID: an unknown filed as a
		// measurement prints beside "everything checked is ok" as if it had
		// been read — the silent pass, in the shape that matters most, since
		// the state it hides is the one that looks cleanest. A warning-severity
		// unread state keeps its own class above (finding): it already fails
		// doctor and prints in the default view.
		return doctorClassAudit
	case c.OK:
		// A check that passes and still carries a severity is a measurement,
		// not a finding: project-account-stored names the repositories a
		// project's token reaches when nothing is wrong with it.
		return doctorClassMeasure
	case c.Severity == "info" && doctorIsMeasurement(c.ID):
		// A measurement: doctor read a fact, or could not read one, and the
		// reader has nothing to do about it. It is never counted as a finding,
		// so an installation with nothing wrong ends at its one ok line.
		return doctorClassMeasure
	case doctorHasPrefix(c.ID, doctorSecurityPrefixes):
		if finding {
			// A warning-severity security finding is never hidden.
			return doctorClassActionable
		}
		// The sweep's own finding at an info severity: a grant it could not
		// derive, a provider setting nobody can read from here. Counted in the
		// pointer line, printed under --security and --all.
		return doctorClassAudit
	case finding:
		// A finding is an item whatever its ID: taking a list of measured IDs
		// as a class on its own hid a stale price catalog and a run that
		// applied no project layer — both warnings, both of which fail
		// --strict while the view says there is nothing to do.
		return doctorClassActionable
	case doctorActionableInfoIDs[c.ID]:
		// An info check whose one fix is a command: gcp-project-line is not a
		// measurement, it is work.
		return doctorClassActionable
	case c.Severity == "info":
		// An info check doctor has not classified: a finding the pointer line
		// counts, never a measurement nobody hears about (the ruling's default,
		// so a check added later is never hidden).
		return doctorClassAudit
	}
	return doctorClassActionable
}

// doctorIsMeasurement reports whether a check is one of the measurements
// doctorClassOf's allow-list names: an exact ID, or — for a list that is not
// empty — one of the per-instance prefixes beside it.
func doctorIsMeasurement(id string) bool {
	return doctorMeasureIDs[id] || doctorHasPrefix(id, doctorMeasurePrefixes)
}

// doctorHasPrefix reports whether id is one of prefixes or carries one as its
// own prefix (the per-instance IDs: image-config-web, token-signers-3).
func doctorHasPrefix(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if id == p || strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// classifyDoctorChecks returns the checks with Class attached, for `--json`
// and for every view. The input is not modified: a caller's slice is its own.
func classifyDoctorChecks(checks []doctorCheck) []doctorCheck {
	out := make([]doctorCheck, len(checks))
	copy(out, checks)
	for i := range out {
		out[i].Class = doctorClassOf(out[i])
	}
	return out
}

// doctorView is what the reader asked for: the default view, everything
// (--all), or the default view plus the security sweep (--security).
type doctorView struct {
	all      bool
	security bool
}

// doctorAuditHeld are the audit findings a view left out of its lines: the
// security sweep's, and the rest of the audit class. Only a finding
// (doctorIsFinding) is counted: the measurements are what doctor read and found
// nothing in, and a pointer to them on every clean run is how a reader learns
// to skip the line (the owner's rule of 2026-10-31: an installation with
// nothing wrong prints one line and no pointer).
type doctorAuditHeld struct {
	security int
	other    int
}

// doctorIsFinding is the one question every hidden class has to answer no to:
// is this something the reader has to know about? A check that fails doctor is
// one (checksFail: not ok, severity not "info"), and so is a check carrying the
// unread marker — a read that did not answer leaves a fact unknown whether the
// check's own result field says ok or not. The marker is what a check that
// could not read something sets, and a hidden check that carries it may not be
// dropped: the alternative is an unknown counted as a measurement, which is the
// silent pass in the one shape nobody can see.
func doctorIsFinding(c doctorCheck) bool {
	return !c.OK || c.unread
}

// doctorAuditFindingCount is how many of these lines are findings: an audit
// check the reader has to know about (doctorIsFinding). The count exists for
// the verdict line, which is printed above the sweep --security prints, and may
// not call an installation ok directly above a finding of its own.
func doctorAuditFindingCount(cs []doctorCheck) int {
	n := 0
	for _, c := range cs {
		if doctorIsFinding(c) && doctorClassOf(c) == doctorClassAudit {
			n++
		}
	}
	return n
}

// doctorVisible are the checks a view prints, in the order doctor ran them: the
// items, the audit half of the security sweep when --security asked for it, and
// the audit findings the view left out, counted by kind. An audit line is never
// dropped in silence: the counts are what the pointer lines at the end of the
// default screen are made of.
func doctorVisible(checks []doctorCheck, v doctorView) (shown, auditSecurity []doctorCheck, held doctorAuditHeld) {
	for _, c := range checks {
		class := doctorClassOf(c)
		if class == "" {
			continue
		}
		security := doctorHasPrefix(c.ID, doctorSecurityPrefixes)
		switch {
		case class == doctorClassMeasure:
			// A measurement: --all carries it, and --security carries the
			// security half of it (a reader who asks for the sweep gets the
			// sweep's own answers). The default view never prints one and never
			// counts one as a finding.
			if security && v.security {
				auditSecurity = append(auditSecurity, c)
			}
		case class != doctorClassAudit:
			shown = append(shown, c)
		case v.all:
			// printDoctorReport hands --all to printDoctorChecks whole.
		case security && v.security:
			auditSecurity = append(auditSecurity, c)
		case doctorIsFinding(c) && security:
			held.security++
		case doctorIsFinding(c):
			held.other++
		}
	}
	return shown, auditSecurity, held
}

// printDoctorReport prints the human view: --all keeps doctor's long-standing
// one-line-per-check output, and the default view (and --security) print the
// verdict and then only what to do.
func printDoctorReport(w io.Writer, checks []doctorCheck, v doctorView) {
	if v.all {
		printDoctorChecks(w, checks)
		return
	}
	shown, auditSecurity, held := doctorVisible(checks, v)
	todo, decisions := doctorSplit(shown)
	switch {
	case len(shown) == 0 && held.security+held.other == 0 && doctorAuditFindingCount(auditSecurity) == 0:
		// Nothing to do, nothing left out, and nothing the sweep is about to
		// print that is a finding: the one line the owner asked for. Every
		// measurement doctor made is still in --all and --json, and a
		// measurement printed under --security is not a finding (nothing is
		// wrong in it), so the ok line stands beside it.
		fmt.Fprintln(w, "fugaro doctor: ok — everything checked is ok")
	case len(shown) == 0:
		// Nothing to do, and audit findings this view does not print — held
		// out, or printed below the verdict as the sweep --security asked for:
		// not the ok line, because a finding under it would contradict it
		// ("all fine" above "2 security findings" is the reading the owner
		// ruled out).
		fmt.Fprintln(w, "fugaro doctor: nothing to do")
	default:
		fmt.Fprintln(w, doctorVerdict(len(todo), len(decisions)))
		fmt.Fprintln(w, doctorLegend)
		for _, c := range todo {
			printDoctorItem(w, c)
		}
		if len(decisions) > 0 {
			// A decision is the owner's, not work of the same kind: it prints
			// under its own line, so a reader counting the items above is not
			// counting the choices as things to do.
			fmt.Fprintln(w, "yours to decide:")
			for _, c := range decisions {
				printDoctorItem(w, c)
			}
		}
	}
	// A reader who asked for the security sweep gets it whole: the sweep's
	// answers are long by nature (who may read which container), and a clipped
	// one answers nothing. The default view's budget is for the default view.
	for _, c := range auditSecurity {
		fmt.Fprintln(w, doctorCheckLine(c))
	}
	// The screen ends with what it did not print: an audit finding the reader
	// never hears about is an audit finding nobody acts on, and a flag is a
	// question about detail, never about whether something exists.
	if held.security > 0 {
		printDoctorSecurityNotice(w, held.security)
	}
	if held.other > 0 {
		printDoctorAuditNotice(w, held.other)
	}
}

// doctorSplit is the items by what they ask of the reader: what has to be
// done, and what is theirs to choose.
func doctorSplit(shown []doctorCheck) (todo, decisions []doctorCheck) {
	for _, c := range shown {
		if doctorClassOf(c) == doctorClassDecision {
			decisions = append(decisions, c)
			continue
		}
		todo = append(todo, c)
	}
	return todo, decisions
}

// doctorLegend is the line under the verdict that says what its words cost,
// because they are doctor's exit code: an error stops Fugaro until it is fixed
// and exits 1; a warning is wrong and Fugaro still runs, which is what
// --strict changes.
const doctorLegend = "an error exits 1 and stops Fugaro until it is fixed; a warning is wrong, Fugaro still runs, and `--strict` exits 1 on it too"

// doctorVerdict is the one line that says how much there is: the work, and the
// choices, because a reader of a decision is not being handed a task.
func doctorVerdict(todo, decisions int) string {
	switch {
	case todo > 0 && decisions > 0:
		return fmt.Sprintf("fugaro doctor: %d things to do, %d to decide", todo, decisions)
	case todo > 0:
		return fmt.Sprintf("fugaro doctor: %d things to do", todo)
	default:
		return fmt.Sprintf("fugaro doctor: %d to decide", decisions)
	}
}

// printDoctorSecurityNotice is the line that keeps a hidden security finding
// from being an unmentioned one: the classification is never silent, so a
// short output is never read as a clean one. The owner's correction of
// 2026-10-31 is the other half of the same rule — a finding that needs a person
// is printed, and this line is for the ones the sweep measures rather than
// finds.
func printDoctorSecurityNotice(w io.Writer, n int) {
	fmt.Fprintf(w, "%d security %s: `fugaro doctor --security`\n", n, doctorFindings(n))
}

// printDoctorAuditNotice is the same for the rest of the audit class: what
// doctor measured, and what it noticed while measuring. "more" is the word
// because the security line above it, when there is one, is the sweep's.
func printDoctorAuditNotice(w io.Writer, n int) {
	fmt.Fprintf(w, "%d more %s: `fugaro doctor --all`\n", n, doctorFindings(n))
}

// doctorFindings is the noun for a count of them.
func doctorFindings(n int) string {
	if n == 1 {
		return "finding"
	}
	return "findings"
}

// printDoctorItem prints one item of the default view: the check's ID, the
// severity word, one sentence and the one fix. A fix that does not fit beside
// the sentence goes on its own indented line rather than being cut: a truncated
// command is a command somebody runs.
func printDoctorItem(w io.Writer, c doctorCheck) {
	head := doctorSeverityWord(c) + " " + c.ID + ": " + doctorShortProblem(c)
	fix := doctorShortFix(c)
	if fix == "" {
		fmt.Fprintln(w, doctorClip(head, doctorLineLimit))
		return
	}
	if doctorClassOf(c) == doctorClassDecision {
		// A decision names the command for each choice, and both have to
		// survive: they go on their own lines, in full, because a command cut
		// at the edge of the terminal is a command somebody runs.
		fmt.Fprintln(w, doctorClip(head, doctorLineLimit))
		for _, choice := range doctorChoices(fix) {
			fmt.Fprintln(w, "  "+choice)
		}
		return
	}
	if len([]rune(head))+1+len([]rune(fix)) <= doctorLineLimit {
		fmt.Fprintln(w, head+" "+fix)
		return
	}
	// The sentence is clipped, the fix never is: only the fix is a command,
	// and a clipped one is worse than a long line (the terminal wraps, and the
	// copy of a command that is not all there is what the clip would cost).
	fmt.Fprintln(w, doctorClip(head, doctorLineLimit))
	fmt.Fprintln(w, "  "+fix)
}

// doctorChoices splits a decision's fix into its choices. The checks word them
// as "X, or Y" — the two containers a shadowed name can be stored in, the two
// things a scope can be moved to — and each becomes its own line.
func doctorChoices(fix string) []string {
	if i := strings.Index(fix, ", or "); i != -1 {
		return []string{fix[:i], "or " + fix[i+len(", or "):]}
	}
	return []string{fix}
}

// doctorSeverityWord is what the default view calls the check. "error" is a
// check that always fails doctor (Severity is ""): the legacy word, and the one
// the message doctor exits with uses.
func doctorSeverityWord(c doctorCheck) string {
	switch c.Severity {
	case "warning":
		return "warning"
	case "info":
		return "info"
	}
	return "error"
}

// doctorUpdateFixes is the one fix of an update-class item, per ID where the
// command needs a flag or comes after another one, and `fugaro update` plain
// otherwise. A check whose remedy is not what update does is not in the class
// at all (doctorUpdatePrefixes), because a line that names a command which
// leaves the warning standing is worse than a longer one that works.
//
// The check's own text is not lost with these: --all and --json still print
// what it says (the manual migration, the repository a publish runs in).
var doctorUpdateFixes = map[string]string{
	// The newer release is not installed yet, so the upgrade comes first; the
	// update behind it is what brings the installation and the checkout to the
	// version (CLAUDE.md's hard rule: those two commands are the whole upgrade).
	"cli-version": "run `brew upgrade fugaro`, then `fugaro update`",
	// A check that cannot even be read outside a checkout says so in its own
	// text: update is the whole upgrade, and it runs in the repository.
	"plugin-pin": "run `fugaro update` in the repository's checkout",

	// policy-vs-jobs has no line of its own, because the whole `fugaro update`
	// is its remedy: the stage list (init_stages.go) is images — the rebuild
	// the check's own text names — and then the checkout's repository stage,
	// which is what writes a job's environment. `--repo .` runs the second of
	// those and stops, so a reader who ran it would still be on the runner
	// image that predates this Fugaro, which is the state the check reports.
}

// doctorShortProblem is the one sentence the default view prints for a check.
// A check with its own short form uses it; every other one is the first
// sentence of its Problem, which is what doctor already words for a person.
func doctorShortProblem(c doctorCheck) string {
	if c.ID == "secret-scope-shadowed" {
		// The names are the check's own first words, and a decision has to name
		// what it is about; the clause after them is what a shadowed name means.
		if i := strings.Index(c.Problem, ": "); i != -1 {
			return c.Problem[:i] + ": the repository copy wins over the project-wide one, so the project's value never reaches here."
		}
	}
	if s, ok := doctorShortFor(c.ID); ok && s.problem != "" {
		return doctorExpand(s.problem, c.ID)
	}
	return firstSentence(c.Problem)
}

// doctorShortFix is the one fix the default view prints: the command or the one
// action, and nothing else.
//
// An update-class item gets its own command, because that is what the class is
// for. Every other check keeps the fix it words itself whenever that fix fits
// the line — the check knows its own state and doctor's text is the one the
// operator's search will find — and falls back to its short form only when it
// does not fit. That fallback is why a short fix in the table is reachable:
// none of them is written to be replaced.
func doctorShortFix(c doctorCheck) string {
	if c.ownFix {
		// The state is not `fugaro update`'s work, whatever class its ID is
		// in: the check's own first sentence is the command, and the rest of
		// its text stays where it is (--all, --json).
		if own := firstSentence(c.Fix); own != "" {
			return own
		}
	}
	if doctorClassOf(c) == doctorClassUpdate {
		if f, ok := doctorUpdateFixes[c.ID]; ok {
			return f
		}
		return "run `fugaro update`"
	}
	own := firstSentence(c.Fix)
	s, ok := doctorShortFor(c.ID)
	if own != "" && (!ok || s.fix == "" || doctorFits(c, own)) {
		return own
	}
	return doctorExpand(s.fix, c.ID)
}

// doctorShortFor is one check's short form, by its whole ID or by the prefix
// doctor gives a per-instance check.
func doctorShortFor(id string) (doctorShort, bool) {
	if s, ok := doctorShorts[id]; ok {
		return s, true
	}
	for _, p := range doctorShortsByPrefix {
		if strings.HasPrefix(id, p.prefix) {
			return p.short, true
		}
	}
	return doctorShort{}, false
}

// doctorFits reports whether a fix fits beside the item's own head on one line.
func doctorFits(c doctorCheck, fix string) bool {
	head := doctorSeverityWord(c) + " " + c.ID + ": " + doctorShortProblem(c)
	return len([]rune(head))+1+len([]rune(fix)) <= doctorLineLimit
}

// doctorExpand fills a short form's %s with the instance's own name: the
// per-instance checks name a workflow in their ID and their command needs it.
func doctorExpand(s, id string) string {
	if !strings.Contains(s, "%s") {
		return s
	}
	name := id
	for _, p := range doctorShortsByPrefix {
		if strings.HasPrefix(id, p.prefix) {
			name = strings.TrimPrefix(id, p.prefix)
			break
		}
	}
	return strings.ReplaceAll(s, "%s", name)
}

// doctorShort is one check's short form: the sentence and the fix.
type doctorShort struct {
	problem string
	fix     string
}

// doctorShorts are the one-sentence, one-fix forms for the checks the default
// view shows. They say the same thing as the check's own text — the same state,
// the same command — in the length a person reads, and the full text is
// unchanged in --json (the check's problem and fix) and under --all.
var doctorShorts = map[string]doctorShort{
	"cli-version": {"a newer Fugaro release is available.", ""},
	"plugin-pin":  {"the Claude Code plugin is pinned to another version than this Fugaro.", ""},
	// The plugin Claude Code installed is not the checkout's pin, and the
	// converge's plugin stage writes the pin: installing is the upgrade's own
	// step (its refusal text and pluginwire.Fix both name it).
	"plugin-install": {"the installed Claude Code plugin is not the pinned one.",
		"run `fugaro upgrade --local`, or install it in Claude Code"},
	"base-image": {"no Fugaro base image is recorded, so image builds start from whatever the repository names.", ""},
	// The published policy node is older than this binary. The command that
	// migrates it is the class's own (`fugaro update`, the policy stage), so
	// this entry carries no fix of its own: the delete-then-publish that does
	// the same job by hand rebuilds the node from the config, and a line that
	// offered it would be the lossy move in place of the one the update makes.
	"policy-older-than-binary":          {"the published policy is older than this Fugaro, and no run applies it.", ""},
	"policy-rules-skew":                 {"the deployed budget rules are older than the policy generation, so a published policy cannot be read.", ""},
	"policy-vs-jobs":                    {"the jobs run an image older than this Fugaro, so they do not read a published policy.", ""},
	"installation":                      {"no Fugaro installation is configured.", "run `fugaro setup`"},
	"fugaro-yaml":                       {"this checkout's fugaro.yaml is not valid.", "fix what `fugaro validate` prints"},
	"gcp-project-line":                  {"this checkout's fugaro.yaml has no gcp_project: line, so a run cannot tell which project it belongs to.", "run `fugaro update --anchor`"},
	"policy-newer-than-binary":          {"the published policy is newer than this Fugaro, so no run reads it.", "run `fugaro upgrade`"},
	"policy-unknown-keys":               {"an unknown key in the published policy stops every reader from reading it.", "remove the key `fugaro project policy show` prints"},
	"allow-list-empty":                  {"the published policy admits no repository, so every run stops before it starts.", "run `fugaro project repos add owner/name`"},
	"project-secret-stored":             {"a secret this checkout's jobs mount has no stored version.", "run `fugaro secrets set NAME`"},
	"project-account-stored":            {"a Claude account job mounts a token the project does not store.", "run `claude setup-token`, then `fugaro secrets set TOKEN --project-wide`"},
	"claude-accounts-stored":            {"a named Claude account of this repository has no stored token.", "run `claude setup-token`, then `fugaro secrets set claude-oauth-token-NAME`"},
	"oauth-token-allowed":               {"this checkout's agent.oauth_token names an account it may not use.", "name the repository in that account's allow_data_to, or drop the key"},
	"project-layer-copy":                {"the repository's own copy of the project layer is not the published one.", "run `fugaro config publish` in this checkout"},
	"project-secret-extra-accessors":    {"a project-wide secret still grants a job a repository no longer holds.", "run `fugaro update --repo .` in that repository's checkout"},
	"secret-scope":                      {"which copy Fugaro resolves for each secret this checkout's jobs mount.", "run `fugaro secrets ls --json` for that container's versions"},
	"secret-scope-shadowed":             {"the repository copy wins over the project-wide one, so the project's value never reaches here.", ""},
	"project-secret-scope-stale":        {"a deployed job mounts the copy the secret's scope no longer resolves to.", ""},
	"project-secret-optional":           {"a workflow's optional key is not stored, and a run of it needs one.", "run `claude setup-token` and `fugaro secrets set anthropic-api-key`"},
	"provider-open-to-every-repository": {"a provider's key may be mounted into every repository.", "narrow it: list the repositories in its allow_data_to, then run `fugaro update --repo .` in each"},
	"provider-secret-name":              {"a provider's key is stored under a name Fugaro's convention does not give it.", "rename nothing: name a new provider's key `<provider>-api-key`"},
	"billing-api":                       {"the Cloud Billing API is disabled on the project, so doctor and run cannot read the billing state.", ""},
	"terraform":                         {"terraform is not on the PATH.", "install terraform 1.9 or later"},
	"docker":                            {"docker is not on the PATH.", "install docker"},

	// The security sweep's warnings. They are printed in the default view — a
	// warning-severity finding is never hidden — and their own sentence names a
	// member, a role and a container, which on the installations the sweep
	// exists for is longer than one screen line: the clip cut it where the names
	// are, which is the one part the reader needs. The short form says the state
	// in one line and leaves the names to --all and --json.
	//
	// The fix stays the check's own here: it carries the member's real name in a
	// command that can be pasted, and a short fix would have to leave a
	// placeholder where that name goes.
	//
	// The wording covers every state of its ID, because an ID is all the lookup
	// has and these checks report both a grant doctor could not derive and a
	// read that did not happen.
	"secret-readers-unexpected": {"a secret this checkout's jobs mount grants a reader Fugaro cannot place, or could not be read.", ""},
	// The secrets-brokering checks (B1.9), the same shape: an ID reports both a
	// state a reader has to act on and a read that did not answer, so each
	// sentence covers its ID's states and leaves the names (the repository, the
	// workflow, the service) to --all and --json. The fix stays the check's own:
	// it names the container or the repository the command needs. The two whose
	// only state is information (bundle-bitbucket-token with nothing proxied,
	// bitbucket-direct before the flip) are counted rather than printed, so
	// their entries are here for the warning state beside them.
	"credentials-service-deployed": {"the credentials service's callers could not be checked, or a job account it should admit cannot invoke it.", ""},
	"secret-rotation-age":          {"a stored secret is past the rotation period or the expiry the local config records for it.", ""},
	"bundle-bitbucket-token":       {"the credentials service's bundle holds no Bitbucket token, or could not be read.", ""},
	"bitbucket-token-valid":        {"Bitbucket refuses the bundle's Bitbucket token, or could not be asked about it.", ""},
	"bitbucket-direct":             {"a job of a proxied Bitbucket repository still mounts its own token, or that could not be read.", ""},
	"model-key-in-agent":           {"a model credential reaches a workflow's agent's own environment.", ""},
	"launcher-jobs":                {"which Cloud Run jobs a launcher can start could not be checked.", ""},
	"launcher-jobs-public":         {"a Cloud Run job can be started by allUsers or allAuthenticatedUsers, which is anyone.", ""},
	"launcher-jobs-unread":         {"the IAM policy of some Cloud Run jobs could not be read, so whether a launcher can start them is unknown.", ""},
	"batch-launcher-iam":           {"the launchers' roles/batch.jobsEditor grant in this project is not the one Fugaro writes, or could not be read.", ""},
	"allow-list-uncovered":         {"the published policy does not admit a repository this machine's config names.", ""},
	"creds-broken":                 {"a Claude account's token was refused, and no work routes to it until an operator clears the mark.", ""},
	"creds-unknown-state":          {"a credential mark carries a state this Fugaro does not read, so the account is held rather than available.", ""},
	"creds-rules":                  {"the deployed budget rules do not carry the credentials family this Fugaro writes, or could not be read.", ""},
	"token-signers":                {"who can sign the budget database's sign-in tokens could not be read.", ""},
}

// doctorShortsByPrefix are the short forms for the checks doctor emits once
// per instance (image-config-<workflow>): the instance's own name is in the
// check's text, and the full text stays in --all and --json. A %s in a short
// form is the instance's name, taken from the ID.
//
// The order is the order of the slice and not of a map, so two prefixes that
// both match an ID resolve the same way on every run: a map's iteration order
// is randomised, and a check whose sentence changed between two runs of the
// same binary would be a bug nobody could reproduce.
var doctorShortsByPrefix = []doctorPrefixShort{
	// No short fix: the check's own names the workflow, and its workflow is the
	// one thing a hand-run build needs.
	{prefix: "image-config-", short: doctorShort{problem: "its job image was built from image settings it no longer resolves to."}},
	// The sweep's per-instance warnings, one for a member and one for a job
	// account: doctor numbers their IDs (runs-bucket-iam-1, job-token-grant-2,
	// token-signers-3) and their own sentence carries the whole triple, so each
	// gets the one line above and keeps its own fix — the one that names the
	// member.
	//
	// A short form is only ever reached from the default view, and the info
	// lines these prefixes also match (a sweep's own summary, a signer the
	// installation designed for) are measurements, which that view never prints:
	// the full text is what --all and --security show either way.
	{prefix: "token-signers-", short: doctorShort{problem: "somebody who is not a signer Fugaro designs for can sign sign-in tokens, or the policy could not be read."}},
	{prefix: "runs-bucket-iam-", short: doctorShort{problem: "a grant on the runs bucket that Fugaro does not write, or one it could not read."}},
	{prefix: "job-token-grant-", short: doctorShort{problem: "a job account's read grant on the budget token objects is not one Fugaro writes, or could not be read."}},
}

// doctorPrefixShort is one per-instance short form: the ID prefix doctor gives
// the check, and the form itself.
type doctorPrefixShort struct {
	prefix string
	short  doctorShort
}

// firstSentence is a check's first sentence: the default view prints one, and
// the rest is not lost (--all and --json carry the whole text).
func firstSentence(s string) string {
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ". "); i != -1 {
		return s[:i+1]
	}
	return s
}

// doctorClip bounds a line to the default view's width, cutting at a word and
// marking the cut, so nothing wraps and nothing is silently lost: --all and
// --json print the whole text.
func doctorClip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := string(r[:limit-1])
	if i := strings.LastIndex(cut, " "); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ") + "…"
}
