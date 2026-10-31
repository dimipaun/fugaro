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
// informational and audit findings are still there, behind --all; the security
// ones behind --security as well.
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
	// doctorClassAudit is what doctor measured rather than what it found wrong,
	// and the IAM and security sweep: --all, and --security for the security
	// half.
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
// plugin and the checkout's repository (init_stages.go), and a check whose
// remedy is none of them is not an update item. Two the plan means to move
// here are not here yet:
//
//   - policy-older-than-binary: its remedy today is `fugaro project policy
//     publish --delete`, then a publish. projectpolicy.Migrate has no
//     production caller, so `fugaro update` leaves the old node where it is.
//   - plugin-install: what differs is the plugin Claude Code installed, and
//     the converge's plugin stage rewrites the checkout's pin, not the
//     installation: `fugaro upgrade --local` (or the marketplace command in
//     Claude Code) is what installs it.
//
// policy-rules-skew is here and its line names --firebase-project, because a
// plain `fugaro update` does not reach the rules: the flag is what deploys
// them. And cli-version's line names the upgrade first: the newer release is
// not installed yet, so `fugaro update` alone would run the old binary.
var doctorUpdatePrefixes = []string{
	"cli-version",
	"plugin-pin",
	"base-image",
	"image-config-",
	"policy-rules-skew",
	"policy-vs-jobs",
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
}

// doctorDecisionIDs are the checks whose state is the owner's to choose: doctor
// must not pick one, and the default view names the command for each choice
// rather than a single fix.
var doctorDecisionIDs = map[string]bool{
	"secret-scope":                      true,
	"secret-scope-shadowed":             true,
	"project-secret-scope-stale":        true,
	"project-secret-optional":           true,
	"provider-open-to-every-repository": true,
	"provider-secret-name":              true,
}

// doctorAuditPrefixes are the informational lines: what doctor measured,
// where a value came from, what is unmeasured, what the wiring carries. None of
// them is an action, and the owner's complaint was that they crowded out the
// lines that are.
var doctorAuditPrefixes = []string{"plugin-settings-"}

// doctorAuditIDs are the same, by their whole name: a prefix would swallow a
// neighbour that is not informational (project-layer is the layer that applies,
// project-layer-copy is a stale copy that has to be republished).
//
// An audit ID is what doctor measured, and several of them are a measurement
// in one state and a finding in another: provider-price-catalog is an age to
// read and a stale catalog, project-layer-drift is a layer that moved on and a
// run that applied none. The class is therefore never the ID alone — see
// doctorClassOf: a check that is not ok and carries a severity is a finding
// whatever its ID, and a finding is never hidden.
var doctorAuditIDs = map[string]bool{
	"provider-price-catalog":  true,
	"provider-zdr-unverified": true,
	"routing-resolution":      true,
	"routing-cache-markers":   true,
	"project-layer":           true,
	"project-layer-drift":     true,
	"shared-config":           true,
	"shared-config-differs":   true,
	"xdg-temp-dir":            true,
	"policy-published":        true,
}

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
	case c.OK && c.Severity == "":
		// doctor's own bookkeeping: nothing prints it, in any view.
		return ""
	case finding && doctorHasPrefix(c.ID, doctorUpdatePrefixes):
		return doctorClassUpdate
	case doctorDecisionIDs[c.ID]:
		return doctorClassDecision
	case doctorHasPrefix(c.ID, doctorSecurityPrefixes):
		if finding {
			// A warning-severity security finding is never hidden.
			return doctorClassActionable
		}
		return doctorClassAudit
	case finding:
		// A finding is an item whatever its ID: the audit list is what doctor
		// measured, and taking it as a class on its own hid a stale price
		// catalog and a run that applied no project layer — both warnings,
		// both of which fail --strict while the view says there is nothing to
		// do.
		return doctorClassActionable
	case doctorAuditIDs[c.ID] || doctorHasPrefix(c.ID, doctorAuditPrefixes):
		return doctorClassAudit
	case c.OK:
		// A check that passes and still carries a severity is a measurement,
		// not a finding: project-account-stored names the repositories a
		// project's token reaches when nothing is wrong with it.
		return doctorClassAudit
	case c.Severity == "info" && !doctorActionableInfoIDs[c.ID]:
		return doctorClassAudit
	}
	return doctorClassActionable
}

// hasAnyPrefix reports whether id is one of prefixes or carries one as its own
// prefix (the per-instance IDs: image-config-web, token-signers-3).
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

// doctorVisible are the checks a view prints, in the order doctor ran them:
// the items, the audit half of the security sweep when --security asked for it,
// and how many security findings the view left out.
func doctorVisible(checks []doctorCheck, v doctorView) (shown, auditSecurity []doctorCheck, hidden int) {
	for _, c := range checks {
		class := doctorClassOf(c)
		if class == "" {
			continue
		}
		security := doctorHasPrefix(c.ID, doctorSecurityPrefixes)
		switch {
		case class != doctorClassAudit:
			shown = append(shown, c)
		case v.all:
			// printDoctorReport hands --all to printDoctorChecks whole.
		case security && v.security:
			auditSecurity = append(auditSecurity, c)
		case security:
			hidden++
		}
	}
	return shown, auditSecurity, hidden
}

// printDoctorReport prints the human view: --all keeps doctor's long-standing
// one-line-per-check output, and the default view (and --security) print the
// verdict and then only what to do.
func printDoctorReport(w io.Writer, checks []doctorCheck, v doctorView) {
	if v.all {
		printDoctorChecks(w, checks)
		return
	}
	shown, auditSecurity, hidden := doctorVisible(checks, v)
	todo, decisions := doctorSplit(shown)
	if len(shown) == 0 {
		fmt.Fprintln(w, "fugaro doctor: ok — everything checked is ok")
	} else {
		fmt.Fprintln(w, doctorVerdict(len(todo), len(decisions)))
		fmt.Fprintln(w, "an error stops Fugaro until it is fixed (doctor exits 1); a warning is wrong, and Fugaro still runs. `fugaro doctor --all` prints every check.")
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
	if hidden > 0 {
		printDoctorSecurityNotice(w, hidden)
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
// short output is never read as a clean one.
func printDoctorSecurityNotice(w io.Writer, n int) {
	word := "findings"
	if n == 1 {
		word = "finding"
	}
	fmt.Fprintf(w, "%d security %s: `fugaro doctor --security`\n", n, word)
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
		// survive: they go on their own lines rather than one of them being
		// clipped away.
		fmt.Fprintln(w, doctorClip(head, doctorLineLimit))
		for _, choice := range doctorChoices(fix) {
			fmt.Fprintln(w, "  "+doctorClip(choice, doctorLineLimit-2))
		}
		return
	}
	if len([]rune(head))+1+len([]rune(fix)) <= doctorLineLimit {
		fmt.Fprintln(w, head+" "+fix)
		return
	}
	fmt.Fprintln(w, doctorClip(head, doctorLineLimit))
	fmt.Fprintln(w, "  "+doctorClip(fix, doctorLineLimit-2))
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
	// The rules are not reached by a plain update: --firebase-project is what
	// deploys them, and the check's own fix says the same.
	"policy-rules-skew": "run `fugaro update --firebase-project`",
	// The images stage rebuilds the jobs, and writing a job's environment is
	// the checkout's repository stage.
	"policy-vs-jobs": "run `fugaro update --repo .`",
	// A check that cannot even be read outside a checkout says so in its own
	// text: update is the whole upgrade, and it runs in the repository.
	"plugin-pin": "run `fugaro update` in the repository's checkout",
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
	for prefix, s := range doctorShortsByPrefix {
		if strings.HasPrefix(id, prefix) {
			return s, true
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
	for prefix := range doctorShortsByPrefix {
		name = strings.TrimPrefix(id, prefix)
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
	// The published policy node is older than this binary, and nothing
	// redeploys, migrates or deletes it: the remedy is the publisher's own two
	// commands, not `fugaro update`.
	"policy-older-than-binary": {"the published policy is older than this Fugaro, and no run applies it.",
		"run `fugaro project policy publish --delete`, then `fugaro project policy publish`"},
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
}

// doctorShortsByPrefix are the short forms for the checks doctor emits once
// per instance (image-config-<workflow>): the instance's own name is in the
// check's text, and the full text stays in --all and --json. A %s in a short
// form is the instance's name, taken from the ID.
var doctorShortsByPrefix = map[string]doctorShort{
	// No short fix: the check's own names the workflow, and its workflow is the
	// one thing a hand-run build needs.
	"image-config-": {problem: "its job image was built from image settings it no longer resolves to."},
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
