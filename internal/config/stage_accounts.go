package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The stage vocabulary of the account-choice keys (docs/accounts-add/spec.md
// §11.1) and the one mix the owner's B0 ruling admits
// (docs/design/mixed-credentials.md §11). Both rules live here because four
// readers judge them — the local config at load, the published policy node,
// `fugaro validate` and the launch — and a second copy of either would be a
// second answer to drift from.
//
// RunStages are the run's own stage names for the stages a run spends a model
// call on, which is what a `stages.<stage>.claude_account` segment may be. It
// is deliberately not every stage name the tree writes: bootstrap, render,
// finalize, writeback and provision spend no model call, so no account could
// apply to them, and naming one is refused rather than silently never
// applying. The names are the runner's own stage literals
// (internal/runner/runner.go, reviewmode.go, artifact.go) and
// recipe.StepAdversarial, which the record's own stage enum does not yet
// carry. triage is the judge that weighs a review round's findings before the
// fix round takes them (D1851): it spends a model call for the same reason the
// reviews do, on the senior reviewer's model (MixedStageModel), so an account
// may be named for it, and it is listed where the run reaches it — after the
// reviews, before the fix.
var RunStages = []string{"plan", "implement", "review_first", "review", "adversarial", "triage", "fix"}

// MixedAccountStages are the stages of a run that mixes credentials: the
// senior review, the adversarial review and the triage that weighs their
// findings, and no other stage
// (docs/design/mixed-credentials.md §11, "An allow-list, not a denylist"). An
// api-key run may name its one Claude account for these stages only.
//
// triage stands last, after the two reviews, and the order is load-bearing:
// internal/cli's mixedRunAccount takes the first stage of this list that names
// an account to decide which account's job a mixed run starts on, so a name
// sorted ahead of review would move that launch for a repository that names no
// review account.
var MixedAccountStages = []string{"review", "adversarial", "triage"}

// MixedStagePhrase names the mix's stages as a sentence does — "review,
// adversarial and triage" — and is what every message about the mix joins with.
// strings.Join(list, " and ") reads as a run-on once the list is three names,
// and these messages are the launch's own words about which stage may hold the
// account, printed at a terminal and carried in a refusal.
func MixedStagePhrase() string { return mixedStageList("and") }

// MixedStageChoice is the same list where the sentence offers a choice:
// "review, adversarial or triage".
func MixedStageChoice() string { return mixedStageList("or") }

// mixedStageList is MixedAccountStages joined before conj, with the last name
// carrying the conjunction. An empty list names nothing and a one-name list is
// that name: neither is a sentence with a conjunction in it.
func mixedStageList(conj string) string {
	switch len(MixedAccountStages) {
	case 0:
		return ""
	case 1:
		return MixedAccountStages[0]
	}
	return strings.Join(MixedAccountStages[:len(MixedAccountStages)-1], ", ") +
		" " + conj + " " + MixedAccountStages[len(MixedAccountStages)-1]
}

// IsRunStage reports whether stage is one of RunStages.
func IsRunStage(stage string) bool {
	return slices.Contains(RunStages, stage)
}

// AccountStagesAReleasedImageKnows are the stages a *published* policy node's
// claude_account_stages may name and still be read by every job image that
// reads it: RunStages as of the last tag, which is the vocabulary of the reader
// in the field.
//
// A node's reader validates the member's value against its own RunStages
// (internal/projectpolicy, through CheckStageSegment) and refuses the whole
// node over one name it does not know, and a reader is a job image: `fugaro
// update` rebuilds the images behind its own confirmation, so an installation
// can be running one built before this release — an in-flight run included —
// for as long as the operator takes to confirm. Publishing a stage this release
// added would therefore turn every run of that project into an unreadable
// policy on an image that is not rebuilt yet, which is the one thing an update
// must never do (docs/design/update-in-flight.md).
//
// The list is the six names of 0.22.0 "Sloth", the last tag, and it moves the
// way the release-skew constants beside it move — at the start of a release
// cycle, when the tree is rebased onto the tag just cut: the release behind a
// new stage is then a release whose images carry it, and the entry is published
// from that release on. A stage added and published in the same release would
// reach the images of the release before it, which do not know the name.
var AccountStagesAReleasedImageKnows = []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}

// AccountStageHeldBack reports whether a published claude_account_stages entry
// for stage has to wait for the images to catch up: the stage is one this run
// has and the reader in the field does not, so an entry naming it makes the
// node unreadable there. The operator's choice stays where it was written —
// only the published copy holds it back — and it reaches the node from the
// release after the one that added the stage, when the release behind is one
// whose images know the name: a publish cannot ask which image an installation
// is running, so the wait is a release and not a check.
//
// A name that is no stage of a run at all is not held back: it never reaches
// the node, because CheckStageSegment is what refuses it.
func AccountStageHeldBack(stage string) bool {
	return IsRunStage(stage) && !slices.Contains(AccountStagesAReleasedImageKnows, stage)
}

// IsClaudeModel reports whether id is a Claude model's ID, in either spelling a
// pin may carry (a "claude-*" name or an explicit "anthropic/<id>"): it is the
// overlap ladder ClaudePatterns holds, asked by the readers that have to say
// whether a stage runs on Anthropic at all — rung 4's condition, and the mount
// a repository's job is built for (docs/design/mixed-credentials.md §11).
func IsClaudeModel(id string) bool {
	return slices.ContainsFunc(ClaudePatterns, func(p string) bool { return PatternsOverlap(id, p) })
}

// MixedStageModel is the model a mixed run's stage runs, for the Anthropic
// condition rung 4 carries: the review level's own model (level 2's for review,
// level 3's for adversarial, config.ResolveLevels' own defaults) and the
// reviewer role's when the level names none. A level's model is a pin of its
// own — it runs in place of the role slot the level sits in — so a Claude
// reviewer with a provider's level 2 is a stage the account has nothing to do
// on, and rung 4 is not a rung for it.
//
// It reads the configuration's own model and not a run's: a recipe's roles
// mapping and a task's level override are the run's, and the runner makes this
// same judgement with them applied (r.stageModel, internal/runner). What is
// shared is the rule, so the launch, the runner and the job set cannot drift.
func MixedStageModel(a Agent, stage string) string {
	level := a.Levels.L2
	if stage == "adversarial" {
		level = a.Levels.L3
	}
	if level != nil && level.Model != "" {
		return level.Model
	}
	return a.ModelFor(RoleReviewer)
}

// CheckStageSegment refuses a stages.<stage> key whose segment is not a stage
// a run spends a model call on. path is the key's own path ("stages", or the
// node's spelling of it), so the problem names where it was written.
//
// A segment outside the vocabulary is refused and not ignored: the key would
// otherwise name an account for a stage that never reads it, and the operator
// would believe the account was in use. The stage the tree does not have yet
// is refused for the same reason — the vocabulary is closed, so a recipe that
// adds a stage adds it here in the same change.
func CheckStageSegment(stage, path string) []Problem {
	if IsRunStage(stage) {
		return nil
	}
	return []Problem{{Path: path + "." + stage,
		Message: fmt.Sprintf("%q is not a stage of a run: a stage account may be named for %s, the stages a run spends a model call on (bootstrap, render, finalize, writeback and provision spend none, so no account could apply to them)",
			stage, strings.Join(RunStages, ", "))}}
}

// CheckStageAccountMix judges the stage accounts a run's own credential
// admits, and it is the B0 ruling written once
// (docs/design/mixed-credentials.md §11). auth is the agent.auth the run
// resolves; stages is the stage account each stage would use, already
// resolved from the config (empty values dropped by the caller or ignored
// here).
//
// Under api-key the ruling admits exactly one Claude account, alongside the
// provider key the gateway holds, and only for the senior review and the
// adversarial review: the coder's stages keep the provider key, so an account
// named for one of them would never be used, and a second account is a second
// credential the ruling does not grant.
//
// Under oauth the keys keep the meaning the accounts design gives them — one
// account for a stage and another for the rest is what the design's rung 2 is
// for — so nothing is refused here. Under vertex and cascade a stage account
// cannot apply at all (neither reaches a Claude OAuth credential), and it is
// refused rather than ignored, the way a segment outside RunStages is. An
// unknown auth ("") is not judged: the launch resolves it from a checkout it
// may not have yet, and the run itself judges it with the auth it resolves.
// Every *other* value is refused rather than read as oauth: the auth here can
// be a committed fugaro.yaml's own bytes (config.AgentOf validates nothing),
// so a value nobody can place must not be the one that admits a stage account.
func CheckStageAccountMix(auth string, stages map[string]string, path string) []Problem {
	switch auth {
	case "api-key":
		return mixedAccountProblems(stages, path)
	case "oauth", "":
		return nil
	case "vertex", AuthCascade:
		return stageAccountUnderOtherAuth(auth, stages, path)
	default:
		return stageAccountUnderUnknownAuth(auth, stages, path)
	}
}

// stageAccountUnderUnknownAuth refuses a stage account under an agent.auth
// this binary cannot place. The launch refuses the same name with the same
// sentence where it judges the run's own account (claudeAccountAllowed,
// internal/cli/run.go), and it is here too because the stage keys are read
// before that: an auth nothing knows admits no account, and a key that
// silently never applies is what this whole rule refuses.
func stageAccountUnderUnknownAuth(auth string, stages map[string]string, path string) []Problem {
	var ps []Problem
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] == "" {
			continue
		}
		ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
			Message: fmt.Sprintf("a stage account cannot be judged against an agent.auth of %q, which is not a value this Fugaro knows (agent.auth is one of %s): a stage account applies to a run whose agent.auth is oauth or api-key, so under a value nobody can place the account would never be used — fix agent.auth, or remove the key",
				auth, strings.Join(AgentAuths, ", "))})
	}
	return ps
}

func mixedAccountProblems(stages map[string]string, path string) []Problem {
	var ps []Problem
	named := map[string]bool{}
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		account := stages[stage]
		if account == "" {
			continue
		}
		if !slices.Contains(MixedAccountStages, stage) {
			ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
				Message: fmt.Sprintf("an api-key run's %s stage runs on the provider key the gateway holds, so a Claude account named for it would never be used: the one account such a run may hold serves %s and no other stage (docs/design/mixed-credentials.md §11) — name the account for %s, or remove this key",
					stage, MixedStagePhrase(), MixedStageChoice())})
		}
		named[account] = true
	}
	if len(named) > 1 {
		ps = append(ps, Problem{Path: path,
			Message: fmt.Sprintf("an api-key run holds one provider key and one Claude account, and these keys name %d (%s): name one account for %s, or none",
				len(named), strings.Join(slices.Sorted(maps.Keys(named)), ", "), MixedStagePhrase())})
	}
	return ps
}

// CheckMixBudget is the mix's budget-mode clause, and the second half of the
// same rule (docs/design/mixed-credentials.md §11): an api-key run's one
// Claude account exists because the gateway holds the provider key and routes
// the coder's calls to it, and only budget.mode: enforce routes a call to a
// provider at all (CheckProviderGateway). So the mix is admitted only when the
// mode the run resolves is enforce — and the clause is judged only where the
// run has the shape that could mix: a coder pin a route this repository may
// use serves. With no such coder there is no provider key in the gateway, the
// account would never be used, and the config is none of this rule's business.
//
// auth, mode, coder and routes are the run's as the caller resolves them (the
// launch and `fugaro validate` each build them the way the run does); def is
// the project default_claude_account, "" when none is set and never the
// unsuffixed-default sentinel (DefaultClaudeAccount, "default") — that value
// names no account of its own, and an api-key run's job carries no OAuth token
// for it, so a project that spells the default key "default" declares no mix
// and is not this clause's business (a local config that admitted the sentinel
// for the key wrote it in so many words: localcfg's choiceRefProblem); stages
// is the declared stage accounts, as CheckStageAccountMix reads them. path is
// where the caller spells the budget mode ("budget.mode" today), and it is the
// key the problem points at: the fix is to raise the mode, or to drop the keys
// that declare a mix this run cannot carry. The repository is deliberately not
// a parameter: the routes it arrives with are already the ones this repository
// may use (runner.ProvidersFor, the caller's own narrowing), and the clause has
// nothing else to ask of it.
func CheckMixBudget(auth, mode, coder, def string, stages map[string]string, routes map[string]ModelProvider, path string) []Problem {
	if auth != "api-key" || mode == BudgetEnforce {
		return nil
	}
	var keys []string
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] != "" && slices.Contains(MixedAccountStages, stage) {
			keys = append(keys, "stages."+stage+".claude_account")
		}
	}
	if def != "" && def != DefaultClaudeAccount {
		keys = append(keys, "default_claude_account")
	}
	if len(keys) == 0 {
		return nil
	}
	name, _, served := ProviderFor(routes, coder)
	if !served {
		return nil
	}
	return []Problem{{Path: path, Message: fmt.Sprintf(
		"an api-key run may hold one Claude account for %s only with budget.mode: enforce, and this run resolves %s: the provider key lives in the gateway, which is what routes a call to %s (a provider model is refused in any other mode), so under %s the account named by %s would never be used — set budget.mode: enforce, or remove those keys",
		MixedStagePhrase(), modeName(mode), name, modeName(mode), strings.Join(keys, ", "))}}
}

// modeName is a resolved budget mode as a person reads it: the merged layers
// spell "no gateway" as "" (policy's own zero value) and the config's own
// vocabulary spells it "off" (config.BudgetOff), and a sentence that printed
// the empty string would name nothing.
func modeName(mode string) string {
	if mode == "" {
		return BudgetOff
	}
	return mode
}

func stageAccountUnderOtherAuth(auth string, stages map[string]string, path string) []Problem {
	var ps []Problem
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] == "" {
			continue
		}
		ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
			Message: fmt.Sprintf("a stage account only applies to a run whose agent.auth is oauth or api-key, and this run's is %s: under %s no Claude OAuth credential reaches the run, so the account would never be used — remove the key, or set agent.auth to oauth (the run's one account) or api-key (one account for %s and no other stage)",
				auth, auth, MixedStagePhrase())})
	}
	return ps
}

// The two paths a credential can be served by (F7, D1634), and the vocabulary
// the whole plan speaks with: a pool's members are not a candidate list of one
// selection, they are credentials each served by the half of a run that can
// reach it. The credentials service holds the Claude OAuth and Anthropic-key
// members and selects among them by tier; a run's own gateway sends the
// OpenRouter and Vertex members, whose key or platform credential the job
// holds. It lives here, beside the mix rules, because the readers that have to
// place a credential — a pool's own read, the run's launch, the credentials
// service's own candidate list — must not answer it differently.
const (
	// CredentialPathCredsvc is the credentials service: OAuth before API key,
	// tier order, one credential sticky per run (D154, D1634).
	CredentialPathCredsvc = "credsvc"
	// CredentialPathRunner is the run's own gateway, which is where a member
	// whose credential the job holds is sent: failover at a stage boundary,
	// never by retrying a call (F20, D1634, D1635).
	CredentialPathRunner = "runner"
)

// CredentialServingPath is the path that serves a credential of this type:
// CredentialPathCredsvc, CredentialPathRunner, or "" for a type neither path
// serves. Every type this release ships has one (ValidateCredentials refuses
// the two that do not), and a type nothing places is "" rather than a guess:
// the readers above place what they can and refuse the rest.
func CredentialServingPath(typ string) string {
	switch typ {
	case CredentialTypeClaudeOAuth, CredentialTypeAnthropic:
		return CredentialPathCredsvc
	case CredentialTypeOpenRouter, CredentialTypeVertex:
		return CredentialPathRunner
	}
	return ""
}

// PoolServingPaths are the distinct paths the pool's members are served by, in
// CredentialPathCredsvc and CredentialPathRunner order. creds is the
// credentials block of the document the pool is written in; a member it does
// not define is the built-in vertex, which no installation defines (D1624).
//
// One path is a pool both halves of a run can walk as a list: the credentials
// service's tier order, or the gateway's ordered members. Two of them are a
// pool that can only fail over where the credential is chosen again — at a
// stage boundary — because neither path can reach the other's members (F7,
// D1634). A member of a type nothing serves contributes no path and is
// reported by name, so a caller that must say which member it could not place
// has the name to say it with.
func PoolServingPaths(p Pool, creds []Credential) (paths, unplaced []string) {
	seen := map[string]bool{}
	for _, m := range PoolMembers(p, creds) {
		switch {
		case m.Path == "":
			if !slices.Contains(unplaced, m.Name) {
				unplaced = append(unplaced, m.Name)
			}
		case !seen[m.Path]:
			seen[m.Path] = true
			paths = append(paths, m.Path)
		}
	}
	slices.Sort(paths)
	return paths, unplaced
}

// PoolMember is one member of a pool as the reader that has to serve it needs
// it: the credential's name, its type, and the path that serves it
// (CredentialServingPath). The type is carried beside the path because the two
// answer different questions — the path is which half of a run can reach the
// member at all, the type is what that member says when it is spent (F5) and
// which models it serves (F23).
type PoolMember struct {
	Name string
	Type string
	// Path is CredentialServingPath(Type), "" for a type neither path serves.
	Path string
}

// PoolMembers are the pool's members, in the order the pool lists them, each
// with its type and path. creds is the credentials block of the document the
// pool is written in, and a member it does not define is the built-in vertex
// (D1624) — the one name a pool may list without a layer defining it
// (config.ValidatePools). The order is the order, not a set: it is what
// failover walks (D1632).
//
// A member of a type nothing serves is carried with an empty Path rather than
// dropped: a caller that has to refuse it, or to say which member it could not
// place, has the name to say it with.
func PoolMembers(p Pool, creds []Credential) []PoolMember {
	types := map[string]string{}
	for _, c := range creds {
		types[c.Name] = c.Type
	}
	out := make([]PoolMember, 0, len(p.Members))
	for _, m := range p.Members {
		typ, ok := types[m]
		if !ok {
			typ = CredentialTypeVertex
		}
		out = append(out, PoolMember{Name: m, Type: typ, Path: CredentialServingPath(typ)})
	}
	return out
}

// PoolHasVertexMember is the name of the pool's Vertex member, "" when it has
// none: the built-in credential needs no secret and its calls are the
// project's own GCP service, so a pool that lists it cannot be capped by the
// gateway, and under budget.mode: enforce the pool is refused outright
// (D1626, F8).
func PoolHasVertexMember(p Pool, creds []Credential) string {
	for _, m := range PoolMembers(p, creds) {
		if m.Type == CredentialTypeVertex {
			return m.Name
		}
	}
	return ""
}

// CredentialServesModel reports whether a credential of this type serves the
// model ID as written (F23, D1637). There is no per-member mapping: a craft
// names one model string, the routes are resolved once per run
// (RoutingClaudeFor), and a pool is admitted only if every member can be sent
// that same string.
//
// The two spellings are the whole of it, and they are why this is a rule
// rather than a lookup:
//
//   - the credentials that reach Anthropic itself — the OAuth subscription,
//     the API key — and the Vertex credential, which serves Claude under the
//     owner's own GCP project, take Claude's own ID (the "claude-*" shape). A
//     routed model ID is not one of them: "anthropic/<id>" is the spelling a
//     route claims (CheckClaudeClaim), and a run whose pins carry it has left
//     the direct path.
//   - an OpenRouter credential takes the model ID the provider's route claims,
//     so the model must be one this run's providers serve (ProviderFor). Claude
//     itself is never among them: no provider may claim a Claude model except
//     through that one explicit spelling.
//
// A type neither path serves serves nothing.
func CredentialServesModel(typ, model string, providers map[string]ModelProvider) bool {
	switch typ {
	case CredentialTypeClaudeOAuth, CredentialTypeAnthropic, CredentialTypeVertex:
		return IsClaudeModel(model) && !routedSpelling(model)
	case CredentialTypeOpenRouter:
		_, _, ok := ProviderFor(providers, model)
		return ok
	}
	return false
}

// routedSpelling is whether the model is written as a provider route's own ID:
// the one "anthropic/<id>" shape a route may claim (CheckClaudeClaim). It is a
// predicate of its own rather than a second copy of that rule — what it reads
// is whether a pin has left the direct path at all, which is what makes a
// routed ID a model the credentials that reach Anthropic cannot be sent.
func routedSpelling(model string) bool {
	return strings.HasPrefix(model, "anthropic/")
}

// PoolModelProblems is F23's rule at config time (D1637): a pool is admitted
// only if every member serves the model ID as written, and each member that
// does not is reported by name. providers is the run's own route set — the one
// ProvidersFor narrows to a repository — because a member that would serve the
// model through a provider the repository is not admitted to is a member this
// repository has no route for. path is where the caller spells the pool (a
// pool's own name in a credentials block, or the craft's access key), so the
// problem points at the key that names it.
//
// A member of a type neither path serves is not reported here: that is
// ValidateCredentials' and the serving-path readers' own subject, and a pool
// whose members cannot be placed is refused there rather than twice.
func PoolModelProblems(p Pool, creds []Credential, model string, providers map[string]ModelProvider, path string) []Problem {
	var ps []Problem
	for _, m := range PoolMembers(p, creds) {
		if m.Path == "" || CredentialServesModel(m.Type, model, providers) {
			continue
		}
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(
			"the pool lists %s, a %s credential, and a pool is admitted only if every member serves the model as written: %s does not serve %s. A craft names one model string and every member is sent it, so there is no per-member mapping — list members that all serve %s (Claude's own ID for the credentials that reach Anthropic and for %s, the provider's own ID for a route's)",
			m.Name, m.Type, m.Name, model, model, CredentialVertex)})
	}
	return ps
}
