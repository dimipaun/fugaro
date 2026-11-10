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
// than writing a craft key that means something else than the key it came
// from. What it says is about the keys left unconverted and not about the work
// of one run: a config with nothing left to convert has nothing to say —
// `fugaro update` on a converged installation is silent — while a key no
// credential of this release may stand for is named on every write of that
// file until the owner handles it. The file says the same unresolved thing
// every time, and a report a person has to act on is not one this migration
// may print once and forget.

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
// installation holds, in place, and returns the lines the caller prints about
// the keys it could not convert.
//
// It returns nothing when there is nothing to say: a config it converted whole
// says nothing at all, and neither does one holding none of the older
// vocabulary. What it could not convert is returned whether or not this call
// derived anything else — a delta is not what decides whether a person has to
// read a note. An account no credential of this release may stand for, or a
// craft whose stages name two, is the same unresolved state on the next write
// of the file, and the person who has to act on it is reading this run's
// output rather than the run that first found it.
//
// What it writes is shown by the local config's own diff; only the keys that
// need a person are returned, because a line per derived key would bury them.
func migrateCraftKeys(c *localcfg.Config) []string {
	if c == nil || (len(c.ClaudeAccounts) == 0 && c.DefaultClaudeAccount == "" && len(c.ClaudeAccountStages) == 0) {
		return nil
	}
	return deriveCraftAccesses(c, deriveCraftCredentials(c))
}

// deriveCraftCredentials turns the installation's named Claude accounts into
// named credentials of the type every one of them is: a Claude OAuth
// subscription (spec §1). Each account's secret keeps the name it has — a
// credential's value is stored under a name derived from the credential's own
// (spec §1) — so this adds the typed entry and moves no secret.
//
// It returns, per account, the clause saying why no credential was derived
// from it (empty when every account converted), for the caller to fold into
// the line it prints about the crafts that would have named the account.
//
// An account's allow_data_to is neither moved nor asked about. The grant is
// the account entry's own, this migration keeps that entry, and every reader
// that scopes egress by it (the policy node's accounts.<name>.allow_repos,
// `fugaro doctor`, the reach each secret is allowed) still reads it there. A
// credential of this release carries a name and a type and no grant at all,
// so where a pool member's own allow_data_to lands is F10's and D1627's
// decision, taken where a member is checked against the repository (MC5, MC6).
// Deriving only for an account with an EMPTY list would derive for no real
// installation: `fugaro accounts add` refuses to declare an account without
// one (accountsAddRepos), so the empty list is the shape no command writes and
// no profile may bound.
func deriveCraftCredentials(c *localcfg.Config) map[string]string {
	if len(c.ClaudeAccounts) == 0 {
		return nil
	}
	have := make(map[string]string, len(c.Credentials))
	for _, cred := range c.Credentials {
		have[cred.Name] = cred.Type
	}
	// A credential and a pool live in one namespace — a craft's access names
	// one of them and does not say which — and config.ValidatePools refuses a
	// pool and a credential of one name. A credential written under a pool's
	// name would make the file this run writes refuse itself, and a local
	// config write that fails is `fugaro update` failing (CLAUDE.md's HARD
	// RULE), so the account is left alone instead.
	pools := make(map[string]bool, len(c.Pools))
	for _, p := range c.Pools {
		if p.Name != "" {
			pools[p.Name] = true
		}
	}
	var refused map[string]string
	for _, name := range slices.Sorted(maps.Keys(c.ClaudeAccounts)) {
		typ, held := have[name]
		switch {
		case held && typ == config.CredentialTypeClaudeOAuth:
			// The account's own typed entry, already written: nothing to add.
			continue
		case held:
			// A credential of another kind holds the name, and a craft's access
			// names a name and not a type: a key written here would send the
			// craft's calls to that credential rather than to this account's
			// token. The name is taken, so the account goes nowhere and the
			// clash is reported.
			refused = setReason(refused, name, anotherTypeHolds(typ))
			continue
		case pools[name]:
			refused = setReason(refused, name, "its name is already a pool's, and a credential and a pool of one name would leave a craft's access naming one of them undecided")
			continue
		}
		cand := config.Credential{Name: name, Type: config.CredentialTypeClaudeOAuth}
		if ps := config.ValidateCredentials([]config.Credential{cand}); len(ps) > 0 {
			refused = setReason(refused, name, "it is not a name a credential may carry ("+ps[0].Message+")")
			continue
		}
		// Sliced rather than appended in place: the caller's config may be a
		// shallow copy of the one on disk, and a write into its shared backing
		// array would reach the config this run still reads.
		c.Credentials = append(slices.Clone(c.Credentials), cand)
	}
	return refused
}

// anotherTypeHolds spells the clash of a name a credential of another kind
// already holds: the account's token is not what that name resolves to, so no
// craft key may be written for it. Every credential declares a type (the loader
// requires one), which is what makes this a clash of kinds.
func anotherTypeHolds(typ string) string {
	return "a credential of another type (" + typ + ") already holds its name, so a craft key would name that credential and not this account"
}

// setReason records why an account was not converted, keeping the map nil
// until there is something to say (a nil map reads fine, and a test that
// compares an all-converted run's map to nil should not see an empty one).
func setReason(m map[string]string, name, why string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[name] = why
	return m
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
//
// refused is deriveCraftCredentials' answer: an account no credential was
// derived for gets no craft key either, whatever its name resolves to
// elsewhere — an account called `vertex` is the sharpest of those, its name
// being the built-in credential's, which is the platform's own and not the
// account's token. The same map is folded into the line about the account, so
// the reason and the crafts it served are one sentence rather than two that
// point at each other.
func deriveCraftAccesses(c *localcfg.Config, refused map[string]string) []string {
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
		if _, refused := refused[name]; refused || !craftAccessDefined(c, name) {
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
		// The stages that name no account are the other half of the conflict
		// exactly when the file holds no default for them: a key the file does
		// not have cannot be listed (the loop above adds
		// default_claude_account only when it is there), and the name those
		// stages run on is the unsuffixed secret — the empty string
		// installationAccounts resolves them to is not a name, and printed as
		// one it leaves a hole in the list.
		where := slices.Clone(keys[craft])
		named := installationAccounts(c, names[craft])
		if c.DefaultClaudeAccount == "" && slices.Contains(names[craft], "") {
			where = append(where, "the stages that name no account")
			for i, name := range named {
				if name == "" {
					named[i] = unsuffixedSecret
				}
			}
		}
		notes = append(notes, fmt.Sprintf("crafts.%s: %s name different accounts (%s), and one craft holds one access, so no craft key was written for it — its stages keep the accounts they name today",
			craft, strings.Join(where, " and "), strings.Join(named, ", ")))
	}
	if len(unsuffixed) > 0 {
		notes = append(notes, fmt.Sprintf("the %s crafts' stages run on %s (default_claude_account %s), which no credential may be named after, so no craft key was written for them",
			strings.Join(unsuffixed, ", "), unsuffixedSecret, orAbsent(c.DefaultClaudeAccount)))
	}
	for _, name := range slices.Sorted(maps.Keys(skipped)) {
		why := refused[name]
		if why == "" {
			why = "no credential of this release stands for it"
		}
		notes = append(notes, fmt.Sprintf("the account %s, named by the %s crafts, has no craft key: %s — its runs keep reaching it through the account keys alone",
			name, strings.Join(skipped[name], ", "), why))
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

// unsuffixedSecret is what a stage that names no account runs on in an
// installation that sets no default either — the unsuffixed `default` account,
// whose secret carries no name a credential or a craft key could take — spelled
// for a sentence. The empty string installationAccounts resolves such a stage to
// is not a name, so no note prints it as one.
const unsuffixedSecret = "the unsuffixed default secret"

// orAbsent spells a default_claude_account a sentence can print: a key the file
// does not hold is not the empty string's key.
func orAbsent(name string) string {
	if name == "" {
		return "is not set"
	}
	return "is " + name
}

// craftAccessDefined is whether name is something a craft's access may resolve
// to: a credential of the account's own kind that this config now holds, or the
// built-in vertex (D1624). A pool's name is not among them — an access naming
// one would resolve to the pool, which is not the account the stage runs on
// today — and neither is a credential of another type under the same name,
// which resolves to another credential than the account's token (a clash
// deriveCraftCredentials refuses before this is asked). A name no credential
// was derived for is dropped by the caller before it is asked, so what is left
// is a name the resolver finds and the account it stands for.
func craftAccessDefined(c *localcfg.Config, name string) bool {
	if config.ValidCredentialRef(name) != "" {
		return false
	}
	if slices.ContainsFunc(config.BuiltinCredentials(), func(b config.Credential) bool { return b.Name == name }) {
		return true
	}
	return slices.ContainsFunc(c.Credentials, func(cred config.Credential) bool {
		return cred.Name == name && cred.Type == config.CredentialTypeClaudeOAuth
	})
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
