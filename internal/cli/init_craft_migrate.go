package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The in-place conversion of an installation's older keys into the craft
// vocabulary, run by every write of the local config and so by `fugaro update`
// (docs/model-crafts/implementation-plan.md MC4, reconciliation F17, D1621).
//
// It is derived from the installation's OWN keys and never from the spec's
// example: the spec's "Coder = DeepSeek through OpenRouter, Senior review =
// Opus through Claude OAuth" describes one installation and would be a
// different project's configuration written into this one's file (F17).
//
// It only ADDS the new vocabulary. Nothing here removes a key a reader still
// reads: the accounts design's keys keep their meaning until the launch, the
// runner and `fugaro models` read the crafts instead (plan MC5), and a
// migration that emptied them first would leave an installation whose runs
// resolve no model and no credential at all.
//
// Everything it cannot convert faithfully it leaves alone and says so, rather
// than writing a craft key that means something else than the key it came from.

// stageCraft is the craft a run stage's own account key belongs to: the craft
// whose model and access serve the stage. The mapping is the tree's own, not a
// second opinion — config.StageRole hands the coder implement, fix and plan
// and the reviewer the two reviews and the triage — and the reviewer's role is
// split further because the crafts split it: the first-line review is the
// crafts' `review`, the senior review — and the triage, which the tree gives
// the senior reviewer's model on purpose (config.MixedStageModel, D1848) — is
// `senior-review`, and the adversarial review is `security-review`.
//
// "" is a stage this vocabulary has no craft for. localcfg refuses a stage
// segment that is no run stage, so it is unreachable from a file this binary
// reads, and it is answered rather than guessed at.
func stageCraft(stage string) string {
	switch stage {
	case "review_first":
		return config.CraftReview
	case "review", "triage":
		return config.CraftSeniorReview
	case "adversarial":
		return config.CraftSecurityReview
	case "plan", "implement", "fix":
		return config.CraftCoder
	}
	return ""
}

// migrateCraftKeys derives the craft vocabulary from the older keys the
// installation holds, in place, and returns the lines `fugaro update` prints
// about the keys it could not convert — empty when there is nothing to
// migrate, and empty again on a second call, which is what makes the door
// idempotent.
//
// What it writes is shown by the local config's own diff; only the keys that
// need a person are returned, because a line per derived key would bury them.
func migrateCraftKeys(c *localcfg.Config) []string {
	if c == nil || (len(c.ClaudeAccounts) == 0 && c.DefaultClaudeAccount == "" && len(c.ClaudeAccountStages) == 0) {
		return nil
	}
	notes := deriveCraftCredentials(c)
	return append(notes, deriveCraftAccesses(c)...)
}

// deriveCraftCredentials turns the installation's named Claude accounts into
// named credentials of the type every one of them is: a Claude OAuth
// subscription (spec §1). Each account's secret keeps the name it has — a
// credential's value is stored under a name derived from the credential's own
// (spec §1) — so this adds the typed entry and moves no secret.
//
// An account this release's credential cannot stand for is left alone and
// reported, never converted into something else: an account narrowed by
// allow_data_to has no counterpart, because config.Credential carries a name
// and a type and no grant, and writing the entry anyway would drop the
// data-egress restriction this design makes the credential's own reason to
// exist (F10, F12, D1628). Where the grant belongs in the new vocabulary is a
// decision this plan does not answer.
func deriveCraftCredentials(c *localcfg.Config) []string {
	if len(c.ClaudeAccounts) == 0 {
		return nil
	}
	var notes []string
	have := make(map[string]bool, len(c.Credentials))
	for _, cred := range c.Credentials {
		have[cred.Name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(c.ClaudeAccounts)) {
		if have[name] {
			continue
		}
		if allowed := c.ClaudeAccounts[name].AllowDataTo; len(allowed) > 0 {
			notes = append(notes, fmt.Sprintf("claude_accounts.%s admits only %s (allow_data_to), and a credential of this release carries a name and a type and no such grant, so no credential was derived from it and no craft key names it: its runs keep reaching it through the account keys",
				name, strings.Join(allowed, ", ")))
			continue
		}
		cand := config.Credential{Name: name, Type: config.CredentialTypeClaudeOAuth}
		if ps := config.ValidateCredentials([]config.Credential{cand}); len(ps) > 0 {
			notes = append(notes, fmt.Sprintf("claude_accounts.%s is not a name that may stand as a credential, so no credential was derived from it: %s", name, ps[0].Message))
			continue
		}
		// Sliced rather than appended in place: the caller's config may be a
		// shallow copy of the one on disk, and a write into its shared backing
		// array would reach the config this run still reads.
		c.Credentials = append(slices.Clone(c.Credentials), cand)
	}
	return notes
}

// deriveCraftAccesses writes the crafts' accesses from the installation's
// account keys: a craft's access is the account every one of its stages runs
// on, which is a stage's own key where it has one and the project's default
// where it has none.
//
// The craft is what the account has to be read against now, so the default is
// asked of every craft and not only of the coder's: the spec lets the
// first-line review follow the coder and the security review follow the senior
// review, and the senior review follows nobody — so a security review left
// unset because its own stage names no account would take the senior review's
// access, while the account its stage runs on today is the default. The coder
// craft's own stages are three (plan, implement and fix), which is F19's
// consequence and not a choice here: `background` stays a model key under the
// coder craft (D1630), and its account is the coder's.
//
// A craft the owner has already written an access for is left as it is, and a
// craft whose stages disagree is left unset and reported: implement and fix are
// one craft (spec §3), and two accounts there cannot be one access — guessing
// one of them would silently move a stage's runs onto another credential.
func deriveCraftAccesses(c *localcfg.Config) []string {
	keys := map[string][]string{}
	names := map[string][]string{}
	for _, craft := range config.CraftNames {
		defaulted := false
		for _, stage := range config.RunStages {
			if stageCraft(stage) != craft {
				continue
			}
			name := c.ClaudeAccountStages[stage].ClaudeAccount
			if name == "" {
				defaulted = true
			} else {
				keys[craft] = append(keys[craft], stageKey(stage))
			}
			names[craft] = append(names[craft], name)
		}
		if defaulted && c.DefaultClaudeAccount != "" {
			keys[craft] = append([]string{"default_claude_account"}, keys[craft]...)
		}
	}

	want := map[string]string{}
	var conflicted, unsuffixed []string
	for _, craft := range config.CraftNames {
		if own := c.Crafts.Of(craft).Access; own != "" {
			continue
		}
		// Every stage of the craft has to run on one account for the craft to
		// hold it: a stage that names none runs on the default, and the craft
		// speaks for all of them.
		named := installationAccounts(c, names[craft])
		if !allEqual(named) {
			conflicted = append(conflicted, craft)
			continue
		}
		if named[0] == "" || named[0] == config.DefaultClaudeAccount {
			unsuffixed = append(unsuffixed, craft)
			continue
		}
		want[craft] = named[0]
	}

	// skipped is the accounts a craft would have named but that no credential
	// of the new vocabulary stands for, by account name: one line per account,
	// whichever crafts it served.
	skipped := map[string][]string{}
	for _, craft := range config.CraftNames {
		name := want[craft]
		if name == "" {
			continue
		}
		if !craftAccessDefined(c, name) {
			skipped[name] = append(skipped[name], craft)
			continue
		}
		setCraftAccess(&c.Crafts, craft, name)
	}

	var notes []string
	for _, craft := range config.CraftNames {
		if !slices.Contains(conflicted, craft) {
			continue
		}
		notes = append(notes, fmt.Sprintf("crafts.%s: %s name different accounts (%s), and one craft holds one access, so no craft key was written for it — its stages keep the accounts they name today",
			craft, strings.Join(keys[craft], " and "), strings.Join(installationAccounts(c, names[craft]), ", ")))
	}
	if len(unsuffixed) > 0 {
		notes = append(notes, fmt.Sprintf("the %s crafts' stages run on the unsuffixed default secret (default_claude_account %s), which no credential may be named after, so no craft key was written for them",
			strings.Join(unsuffixed, ", "), orAbsent(c.DefaultClaudeAccount)))
	}
	for _, name := range slices.Sorted(maps.Keys(skipped)) {
		notes = append(notes, fmt.Sprintf("the account %s, named by the %s crafts, did not become a credential (see above), so no craft key was written for it: its runs keep reaching it through the account keys alone",
			name, strings.Join(skipped[name], ", ")))
	}
	return notes
}

// stageKey is where a stage's own account is written.
func stageKey(stage string) string { return "stages." + stage + ".claude_account" }

// installationAccounts is the accounts the stages run on as the accounts design
// reads them: a stage's own key where it has one, the project's default where it
// has none. "" is the unsuffixed secret, which is what a stage with no key runs
// on in an installation that sets no default either.
func installationAccounts(c *localcfg.Config, stages []string) []string {
	out := make([]string, 0, len(stages))
	for _, name := range stages {
		if name == "" {
			name = c.DefaultClaudeAccount
		}
		out = append(out, name)
	}
	return out
}

// allEqual reports whether every name is the same. It is asked of a craft's
// stages and never of an empty list, which every craft has at least one of
// (stageCraft covers config.RunStages).
func allEqual(names []string) bool {
	return !slices.ContainsFunc(names, func(name string) bool { return name != names[0] })
}

// orAbsent spells a default_claude_account a sentence can print: a key the file
// does not hold is not the empty string's key.
func orAbsent(name string) string {
	if name == "" {
		return "is not set"
	}
	return "is " + name
}

// craftAccessDefined is whether name is something a craft's access may resolve
// to: a credential this config now holds, or the built-in vertex (D1624).
func craftAccessDefined(c *localcfg.Config, name string) bool {
	if config.ValidCredentialRef(name) != "" {
		return false
	}
	if slices.ContainsFunc(config.BuiltinCredentials(), func(b config.Credential) bool { return b.Name == name }) {
		return true
	}
	return slices.ContainsFunc(c.Credentials, func(cred config.Credential) bool { return cred.Name == name })
}

// setCraftAccess writes one craft's access, leaving its model half alone: the
// two halves are set separately (spec §3).
func setCraftAccess(crafts *config.Crafts, craft, access string) {
	switch craft {
	case config.CraftCoder:
		crafts.Coder.Access = access
	case config.CraftReview:
		crafts.Review.Access = access
	case config.CraftSeniorReview:
		crafts.SeniorReview.Access = access
	case config.CraftSecurityReview:
		crafts.SecurityReview.Access = access
	}
}
