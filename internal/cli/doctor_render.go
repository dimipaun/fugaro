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
// Matching is by prefix because two of them are per-instance IDs
// (image-config-<workflow>).
//
// policy-older-than-binary and policy-rules-skew are here per the plan's
// sequencing: `fugaro update` migrates a policy node it finds older than itself
// and redeploys the rules (UC2 and UC3). Until those land the check's own --all
// text still names what an operator does by hand today, and no explanation is
// lost: it is in --json and under --all.
var doctorUpdatePrefixes = []string{
	"cli-version",
	"plugin-pin",
	"plugin-install",
	"base-image",
	"image-config-",
	"policy-older-than-binary",
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
	switch {
	case c.OK && c.Severity == "":
		// doctor's own bookkeeping: nothing prints it, in any view.
		return ""
	case !c.OK && c.Severity != "info" && doctorHasPrefix(c.ID, doctorUpdatePrefixes):
		// Not an "info" check: that severity is what doctor says when it could
		// not read the state at all (the deployed rules were unreadable, a
		// plugin state cannot be compared), and `fugaro update` is no fix for a
		// read that did not happen.
		return doctorClassUpdate
	case doctorDecisionIDs[c.ID]:
		return doctorClassDecision
	case doctorHasPrefix(c.ID, doctorSecurityPrefixes):
		if !c.OK && c.Severity != "info" {
			// A warning-severity security finding is never hidden.
			return doctorClassActionable
		}
		return doctorClassAudit
	case doctorAuditIDs[c.ID] || doctorHasPrefix(c.ID, doctorAuditPrefixes):
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

// doctorVisible are the checks a view prints, in the order doctor ran them.
func doctorVisible(checks []doctorCheck, v doctorView) (shown, hiddenSecurity []doctorCheck) {
	for _, c := range checks {
		class := doctorClassOf(c)
		if class == "" {
			continue
		}
		security := doctorHasPrefix(c.ID, doctorSecurityPrefixes)
		switch {
		case class != doctorClassAudit:
			shown = append(shown, c)
		case v.all || (v.security && security):
			shown = append(shown, c)
		case security:
			hiddenSecurity = append(hiddenSecurity, c)
		}
	}
	return shown, hiddenSecurity
}

// printDoctorReport prints the human view: --all keeps doctor's long-standing
// one-line-per-check output, and the default view (and --security) print the
// verdict and then only what to do.
func printDoctorReport(w io.Writer, checks []doctorCheck, v doctorView) {
	if v.all {
		printDoctorChecks(w, checks)
		return
	}
	shown, hiddenSecurity := doctorVisible(checks, v)
	if len(shown) == 0 {
		fmt.Fprintln(w, "fugaro doctor: ok — everything checked is ok")
		if len(hiddenSecurity) > 0 {
			printDoctorSecurityNotice(w, len(hiddenSecurity))
		}
		return
	}
	fmt.Fprintf(w, "fugaro doctor: %d things to do\n", len(shown))
	fmt.Fprintln(w, "an error stops Fugaro until it is fixed (doctor exits 1); a warning is wrong, and Fugaro still runs. `fugaro doctor --all` prints every check.")
	for _, c := range shown {
		printDoctorItem(w, c)
	}
	if len(hiddenSecurity) > 0 {
		printDoctorSecurityNotice(w, len(hiddenSecurity))
	}
}

// printDoctorSecurityNotice is the line that keeps a hidden security finding
// from being an unmentioned one: the classification is never silent, so a
// short output is never read as a clean one.
func printDoctorSecurityNotice(w io.Writer, n int) {
	fmt.Fprintf(w, "%d security findings: `fugaro doctor --security`\n", n)
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

// doctorFixForUpdate is the one fix of an update-class item, and the check's
// own text is not lost with it: --all and --json still print what the check
// says (a manual migration, a rebuilt image, the repository a publish runs in).
func doctorFixForUpdate(id string) string {
	if id == "cli-version" {
		// The newer release is not installed yet, so the upgrade comes first;
		// the update behind it is what brings the repository to the version.
		return "run `brew upgrade fugaro && fugaro update`"
	}
	return "run `fugaro update`"
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
	if s, ok := doctorShorts[c.ID]; ok {
		return s.problem
	}
	for prefix, s := range doctorShortsByPrefix {
		if strings.HasPrefix(c.ID, prefix) {
			return s
		}
	}
	return firstSentence(c.Problem)
}

// doctorShortFix is the one fix the default view prints: the command or the one
// action, and nothing else. Update-class items get `fugaro update` whatever
// they say themselves; the rest use their own short form, else the first
// sentence of the check's Fix.
func doctorShortFix(c doctorCheck) string {
	if doctorClassOf(c) == doctorClassUpdate {
		return doctorFixForUpdate(c.ID)
	}
	if s, ok := doctorShorts[c.ID]; ok && s.fix != "" {
		return s.fix
	}
	return firstSentence(c.Fix)
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
	"plugin-install": {"the installed Claude Code plugin is not the pinned one.",
		"run `claude plugin install fugaro` inside the repository"},
	"base-image":                        {"no Fugaro base image is recorded, so image builds start from whatever the repository names.", ""},
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
	"project-layer-copy":                {"the repository's own copy of the project layer is not the published one.", "run `fugaro update --repo .` in this checkout"},
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

// doctorShortsByPrefix are the short sentences for the checks doctor emits once
// per instance (image-config-<workflow>): the instance's own name is in the
// check's text, and the full text stays in --all and --json.
var doctorShortsByPrefix = map[string]string{
	"image-config-": "the workflow's job image was built from image settings it no longer resolves to.",
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
