package craft

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
)

// The migration's rule (C1 (c), R3): what each retired key means, worked out in
// memory for a repository `fugaro update` has not migrated yet, so that it runs
// as it did.
//
// # Why the contract holds it and not the migration
//
// The retired keys set nothing once a repository has been migrated — the
// launcher hands its run a resolved access, and that is the answer (R15 (3)) —
// but three sides still have to give ONE answer about them: the migration, which
// moves them into the new vocabulary (CM4), the launch, which decides a job,
// an account and whether the job hears about the credentials service from the
// access (CM14), and the runner, which reads its file when nothing was handed
// to it (CM8). A rule written in any one of them would be a second copy of what
// the other two do, so it is here, as a real function over the config's own
// types and over nothing else: no file, no environment, no database.
//
// # The four places a retired key may stand
//
//   - agent.oauth_token and a stage's account name a Claude subscription
//     account. An account IS a credential of type claude-oauth in the new
//     vocabulary (R7's first clause: each named account is already a credential,
//     deriveCraftCredentials), so the access names the account — and the
//     project's DEFAULT account is the one case of F35's second condition:
//     nothing chose it, so a run may walk away from it when it is exhausted
//     (CM11 walks only that one).
//   - agent.auth names the four values of config.AgentAuths. vertex is the
//     built-in credential, named explicitly and only here; api-key is the
//     project's own Anthropic key (config.CredentialAnthropicKey) — for the
//     mix's stages it is the account instead, because an api-key run holds one
//     Claude account beside the key (mixAccount below, D1848);
//     cascade is the pool the migration wrote for the project's cascade
//     (config.PoolCascade, marked, D3064 as amended); oauth is the account
//     above.
//   - agent.crafts.<craft>.access is the access the file's craft chose (D3062:
//     moved as written).
//   - model_grades.<grade>.access is a grade's, and it wins over the craft's for
//     a task that names that grade — for the coder's craft and the merge judge's
//     alone, which is exactly where applyGrade writes it today (F38, and the
//     project's key is the one that survives, CM34).
//
// The order is what a reader of today's configuration gets: the grade a task
// names, then the craft's own access, then the retired auth value. Each is
// narrower than the one after it, one craft or one grade against the whole
// repository, exactly as the model rungs are ordered (ModelRungs).
//
// # A file that writes none of them means nothing
//
// It is the rule's whole answer for such a file: ok=false, and never Vertex
// (F49). Vertex was the old default for a file that named no auth, and a
// release that kept that reading would send a repository to the platform's own
// model — and its bill — the moment the removal `update` offers is committed.
// A run with no handed entry and no retired key is refused with the fix
// (LegacyNoAccessRefusal), which is a sentence an operator acts on.
//
// # What the rule does not decide
//
// Whether a name a retired key speaks is DECLARED. A file's `access` and a
// grade's `access` name a credential or a pool, and a name no layer holds is a
// problem the config reports at the key that wrote it (Resolve); the rule
// answers nothing about it rather than inventing a type. An account is the
// exception, and deliberately: the account keys name an ACCOUNT, whose token
// exists whether or not the credentials block declares it — the migration
// refuses to derive a credential for a name another kind already holds
// (deriveCraftCredentials) and such a repository still runs on that token
// today — so the rule answers an account as the claude-oauth credential it is.

// LegacyInput is the retired vocabulary in force for one run, as the caller
// that holds the documents hands it over. Every field is a value read from a
// document; nothing here reads one.
type LegacyInput struct {
	// Auth is agent.auth as it is in force for this repository: the file's own
	// value where it writes one, the project layer's defaults.agent.auth (or a
	// repository's inherited value) where it does not, and "" where neither
	// writes one — which is the state F49 is about.
	Auth string
	// OAuthToken is the file's agent.oauth_token: the account the repository
	// chose for its own runs, already resolved past any alias (an access names
	// an account and never an alias, D2092). "" is the project's default
	// account.
	OAuthToken string
	// StageAccounts is the project's stages.<stage>.claude_account, per stage,
	// resolved past any alias — and NOTHING else: a stage the map does not hold
	// is a stage whose own key names no account. It is the project default that
	// must not be folded in here, because the two answer at different points of
	// the walk (DefaultAccount below): a map that carried the default for every
	// stage without a key would make the project default beat the file's own
	// agent.oauth_token, which is rung 3 of the walk and stands ABOVE the
	// default (claudeAccountChoice's order, docs/design/mixed-credentials.md
	// §11.2).
	StageAccounts map[string]string
	// DefaultAccount is the project's default_claude_account, resolved past any
	// alias, and "" where the key names none or names the unsuffixed-default
	// sentinel (config.DefaultClaudeAccount) — which is no account of its own,
	// the reading mixedStageAccount and claudeAccountChoice both give it. It is
	// the walk's rung 4, read AFTER the file's agent.oauth_token, and under
	// api-key it is the one Claude account a run's mix stages may go out on
	// (mixedStageAccount).
	DefaultAccount string
	// Accesses is the file's own agent.crafts block, the retired per-craft
	// access included.
	Accesses config.Crafts
	// Grades is model_grades in force for this run: a grade's access as the
	// project's config writes it, and where a repository's file still writes one
	// of its own, that one — the file's key is retired too (R3, F38) and it is
	// the narrower layer.
	Grades config.ModelGrades
	// Credentials and Pools are the project's declared credentials and pools,
	// the built-in vertex excepted: what a name a retired key speaks resolves
	// to, and the types and serving paths the answer carries
	// (config.PoolMembers).
	Credentials []config.Credential
	Pools       []config.Pool
	// CascadePool is the name of the pool the migration wrote for the project's
	// cascade (config.PoolCascade), "" for a project whose cascade has none. The
	// rule answers it, marked, for the retired auth value `cascade`; it does not
	// go looking for it, because the marker that identifies it lives in the
	// project's own config and reading that is the caller's business (CM32).
	CascadePool string
}

// LegacyAccess answers the contract's question from the retired vocabulary
// (C1 (c)): the access this stage's calls go out on, and whether the retired
// keys answer anything at all for it.
//
// grade is the task's own model grade (task.grade) where it names one, "" where
// it names none: a grade's access is a second place an access may be written
// (F38), it reaches only the task that named the grade, and within that task
// only the crafts the grade's access is written into (applyGrade).
func LegacyAccess(in LegacyInput, stage, grade string) (Access, bool) {
	name := stageCraft(stage)
	if name == "" {
		return Access{}, false
	}
	// The grade's access reaches the coder's craft and the merge judge's, and no
	// other: that is what applyGrade writes it into today (Crafts.Coder and
	// Crafts.MergeJudge, internal/runner/routing.go), so a graded task's reviews
	// keep their own crafts' accesses. Taking the grade for every craft would
	// move the reviews onto the coder's credential, which is the move a
	// migration may not make (R3).
	if grade != "" && (name == config.CraftCoder || name == config.CraftMergeJudge) {
		if written := in.Grades.Of(grade).Access; written != "" {
			return in.resolve(written)
		}
	}
	if written := in.Accesses.Of(name).Access; written != "" {
		return in.resolve(written)
	}
	switch in.Auth {
	case config.DefaultAgentAuth:
		return in.credential(config.CredentialVertex, config.CredentialTypeVertex, true), true
	case config.AuthAPIKey:
		// An api-key run holds the provider key for its coder's stages and, at
		// most, ONE Claude account beside it, and that account is the mix's
		// (docs/design/mixed-credentials.md §11, D1848, D1851): stageAccountOf
		// and mixedStageAccount answer it for the senior review, the
		// adversarial review and the triage, and for no other stage. Answering
		// the key for those three would move them onto the paid key, which is
		// the one thing a migration may not do (R3, R7).
		if account := in.mixAccount(stage); account != "" {
			return in.account(account), true
		}
		return in.credential(config.CredentialAnthropicKey, config.CredentialTypeAnthropic, true), true
	case config.AuthOAuth:
		return in.account(in.stageAccount(stage)), true
	case config.AuthCascade:
		return in.pool(in.CascadePool)
	}
	return Access{}, false
}

// stageAccount is the account an oauth stage runs on: the project's own key for
// that stage where it writes one (rung 2), the file's agent.oauth_token where
// the project writes none (rung 3), the project's default_claude_account where
// neither does (rung 4), and the unsuffixed default where the project names
// none of the three — the walk today's account choice makes, in today's order,
// and the last of them is the one case F35 calls "not chosen".
//
// The order is load-bearing and is why the file's token stands above the
// project's default: a repository pinned to an account of its own keeps it,
// and the project default is what a repository that names nothing inherits.
func (in LegacyInput) stageAccount(stage string) string {
	if account := in.StageAccounts[stage]; account != "" {
		return account
	}
	if in.OAuthToken != "" {
		return in.OAuthToken
	}
	if in.DefaultAccount != "" {
		return in.DefaultAccount
	}
	return config.DefaultClaudeAccount
}

// mixAccount is the Claude account an api-key run's mix stages may go out on
// (mixedStageAccount), and "" for every other stage and for a run whose project
// names no such account. The mix's stages are config.MixedAccountStages — the
// senior review, the adversarial review and the triage — and they are exactly
// the stages an api-key run may name an account for (config.CheckStageAccountMix
// refuses a stage key anywhere else, so such a file never reaches a launch).
//
// The stage's own key comes first and is served as written (mixedRunAccount's
// own order: "rung 2 comes first"), and the project's default account is the
// mix's account when the stage names none. The file's agent.oauth_token is not
// read here: under api-key no name but the mix's is admitted at all
// (claudeAccountAllowed refuses any other as an account of an oauth run's), so
// a token that disagreed with the mix would be a launch refused, never a
// credential a stage runs on.
func (in LegacyInput) mixAccount(stage string) string {
	if !slices.Contains(config.MixedAccountStages, stage) {
		return ""
	}
	if account := in.StageAccounts[stage]; account != "" {
		return account
	}
	return in.DefaultAccount
}

// account is a subscription account as an access: the credential of that
// account's name, served by the credentials service. An account is never
// "chosen" when it is the project's default one (F35).
func (in LegacyInput) account(name string) Access {
	return in.credential(name, config.CredentialTypeClaudeOAuth, name != config.DefaultClaudeAccount)
}

// credential is one credential as an access, with the type the caller knows it
// has: the credential's own entry in the project's block where it has one, and
// the type the caller states where the name is not declared at all (an account's
// token, the built-in vertex).
func (in LegacyInput) credential(name, typ string, chosen bool) Access {
	if declared, ok := config.CredentialNamed(in.Credentials, name); ok {
		typ = declared.Type
	}
	return Access{
		Kind:    AccessCredential,
		Name:    name,
		Members: []config.PoolMember{{Name: name, Type: typ, Path: config.CredentialServingPath(typ)}},
		Chosen:  chosen,
	}
}

// resolve is a name a retired `access` key wrote: a credential or a pool of the
// project's, and ok=false for a name no layer holds — the config reports that
// name at the key that wrote it, and a rule that guessed a type for it would
// answer an access nothing declares.
func (in LegacyInput) resolve(name string) (Access, bool) {
	if declared, ok := config.CredentialNamed(in.Credentials, name); ok {
		return in.credential(declared.Name, declared.Type, true), true
	}
	for _, b := range config.BuiltinCredentials() {
		if b.Name == name {
			return in.credential(b.Name, b.Type, true), true
		}
	}
	return in.pool(name)
}

// pool is a pool of the project's as an access, its members in its own order
// with the type and serving path of each, and the cascade's marker when it is
// the pool the migration wrote for the project's cascade.
func (in LegacyInput) pool(name string) (Access, bool) {
	if name == "" {
		return Access{}, false
	}
	for _, p := range in.Pools {
		if p.Name != name {
			continue
		}
		return Access{
			Kind:    AccessPool,
			Name:    p.Name,
			Members: config.PoolMembers(p, in.Credentials),
			Chosen:  true,
			Cascade: name == in.CascadePool,
		}, true
	}
	return Access{}, false
}

// LegacyNoAccessRefusal is the sentence a run is refused with when nothing
// answers its access: no entry was handed to it and its file writes none of the
// retired keys either (F49). It names the fix, as every refusal here does, and
// it names Vertex in exactly one way — as the thing the run is NOT sent to.
func LegacyNoAccessRefusal(repo string) string {
	return "nothing says what this run's calls go out on: " + repo + "'s file names no access and no agent.auth, " +
		"so run `fugaro update` to migrate the repository (it writes the access each craft resolves to), " +
		"or write the credentials each craft's access should name in the project's config — " +
		"a craft with no access goes out on the first credential that reaches its model, and Vertex only where an access names it"
}
