package config

import (
	"slices"
	"strings"
	"testing"
)

// TestRunStagesAreTheModelCallingStages pins the closed vocabulary the
// `stages.<stage>.claude_account` segment is held to
// (docs/accounts-add/spec.md §11.1): the stages a run spends a model call on,
// and nothing else. The names are the runner's own stage literals plus
// recipe.StepAdversarial, and the stages that spend no model call —
// bootstrap, render, finalize, writeback and provision — are deliberately
// absent: no account could apply to them, so naming one is refused rather than
// silently never applying.
func TestRunStagesAreTheModelCallingStages(t *testing.T) {
	want := []string{"plan", "implement", "review_first", "review", "adversarial", "triage", "fix"}
	if len(RunStages) != len(want) {
		t.Fatalf("RunStages = %v, want %v", RunStages, want)
	}
	for i, stage := range want {
		if RunStages[i] != stage {
			t.Fatalf("RunStages = %v, want %v", RunStages, want)
		}
		if !IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = false", stage)
		}
	}
	for _, stage := range []string{"bootstrap", "render", "finalize", "writeback", "provision", "Review", ""} {
		if IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = true, want false", stage)
		}
		if ps := CheckStageSegment(stage, "stages"); len(ps) != 1 || !strings.Contains(ps[0].Message, "is not a stage of a run") {
			t.Errorf("CheckStageSegment(%q) = %+v, want one refusal", stage, ps)
		}
	}
	if ps := CheckStageSegment("review", "stages"); ps != nil {
		t.Errorf("CheckStageSegment(review) = %+v, want none", ps)
	}
}

// TestCheckStageAccountMixIsTheB0Ruling is the ruling of
// docs/design/mixed-credentials.md §11 read as the one function every layer
// that judges it calls: an api-key run holds one provider key and one Claude
// account, and that account serves the senior review and the adversarial
// review and no other stage.
func TestCheckStageAccountMixIsTheB0Ruling(t *testing.T) {
	cases := []struct {
		name   string
		auth   string
		stages map[string]string
		want   [][]string // one entry per expected problem, each the substrings it must name
	}{
		{name: "api-key, the permitted mix", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-overflow"}},
		{name: "api-key, review alone", auth: "api-key", stages: map[string]string{"review": "acme-overflow"}},
		{name: "api-key, no stage account", auth: "api-key", stages: nil},
		{name: "api-key, the coder's stage refused", auth: "api-key", stages: map[string]string{"implement": "acme-overflow"},
			want: [][]string{{"stages.implement.claude_account", "would never be used", "review, adversarial and triage"}}},
		{name: "api-key, review_first refused", auth: "api-key", stages: map[string]string{"review_first": "acme-overflow"},
			want: [][]string{{"stages.review_first.claude_account", "review, adversarial and triage"}}},
		{name: "api-key, fix refused", auth: "api-key", stages: map[string]string{"fix": "acme-overflow"},
			want: [][]string{{"stages.fix.claude_account"}}},
		{name: "api-key, plan refused", auth: "api-key", stages: map[string]string{"plan": "acme-overflow"},
			want: [][]string{{"stages.plan.claude_account"}}},
		{name: "api-key, two accounts refused", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-personal"},
			want:   [][]string{{"stages", "one provider key and one Claude account", "acme-overflow, acme-personal"}}},
		{name: "api-key, a stage the tree does not have yet", auth: "api-key", stages: map[string]string{"digest": "acme-overflow"},
			want: [][]string{{"stages.digest.claude_account"}}},
		// Two problems at once: the stage key first (the sorted walk), the count
		// after it, so the sentence a person reads starts with the key to fix.
		{name: "api-key, a bad stage and a second account", auth: "api-key",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal"},
			want:   [][]string{{"stages.implement.claude_account"}, {"stages", "one provider key and one Claude account"}}},
		// The accounts design's own rung 2 is what an oauth run's stage key is
		// for: one account for a stage and another for the rest is admitted, and
		// no stage is out of bounds.
		{name: "oauth keeps the accounts design's meaning", auth: "oauth",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal", "fix": "acme-overflow"}},
		{name: "oauth names one account", auth: "oauth", stages: map[string]string{"review": "acme-overflow"}},
		{name: "oauth names none", auth: "oauth", stages: nil},
		// "Never under another auth": neither vertex nor cascade reaches a Claude
		// OAuth credential, so an account there could never be used. Under
		// cascade the readers of the machine's own keys drop the operator's
		// project-wide accounts before asking this function
		// (CascadeSkippedStageAccounts), so a cascade row here is a *declared*
		// account of the run's own and not the state a cascade repository
		// usually reaches.
		{name: "vertex refused", auth: "vertex", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is vertex"}}},
		{name: "cascade refused", auth: "cascade", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is cascade"}}},
		{name: "vertex with no stage account", auth: "vertex", stages: nil},
		// The launch resolves the auth from a checkout it may not have yet, so
		// an auth it cannot place is not judged here: the run judges it with the
		// auth it resolves.
		{name: "an unknown auth is not judged", auth: "", stages: map[string]string{"implement": "acme-overflow"}},
		// Every other value is refused rather than read as oauth: a committed
		// fugaro.yaml's agent.auth is its own bytes, so a value this binary
		// cannot place must not be the one that admits a stage account.
		{name: "an auth this binary cannot place is refused", auth: "api_key",
			stages: map[string]string{"review": "acme-overflow"},
			want:   [][]string{{"stages.review.claude_account", `"api_key"`, "not a value this Fugaro knows"}}},
		{name: "an auth this binary cannot place, with no stage account", auth: "apikey", stages: nil},
		{name: "an auth this binary cannot place, with an empty value", auth: "nonesuch", stages: map[string]string{"review": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := CheckStageAccountMix(tc.auth, tc.stages, "stages")
			if len(ps) != len(tc.want) {
				t.Fatalf("CheckStageAccountMix(%q, %v) = %+v, want %d problem(s)", tc.auth, tc.stages, ps, len(tc.want))
			}
			for i, wants := range tc.want {
				for _, want := range wants {
					if !strings.Contains(ps[i].Path, want) && !strings.Contains(ps[i].Message, want) {
						t.Errorf("problem %d = %+v, want it to name %q", i, ps[i], want)
					}
				}
			}
		})
	}
}

// TestCascadeSkippedStageAccountsIsTheCascadeExemption is the rule Check 191
// asked for: the account-choice keys are the *operator's* project-wide default
// for the stages they name, so a cascade repository of the same project — the
// one auth whose credential comes from the credentials service's own tiers —
// is not refused for keys it never wrote. Every other auth keeps the map it
// passed, so the refusal under vertex is the caller's own.
func TestCascadeSkippedStageAccountsIsTheCascadeExemption(t *testing.T) {
	deklared := map[string]string{"review": "acme-overflow", "adversarial": "acme-personal"}
	for _, auth := range []string{"api-key", "oauth", "vertex", ""} {
		got, note := CascadeSkippedStageAccounts(auth, deklared)
		if note != "" {
			t.Errorf("auth %q: note = %q, want none", auth, note)
		}
		if len(got) != len(deklared) || got["review"] != "acme-overflow" || got["adversarial"] != "acme-personal" {
			t.Errorf("auth %q: stages = %v, want the caller's own map", auth, got)
		}
	}
	// A project that names no stage account says nothing: the note is about
	// keys the operator wrote, and a cascade repository of a project without
	// them must not be told about a rule it never met.
	for _, none := range []map[string]string{nil, {}, {"review": ""}} {
		got, note := CascadeSkippedStageAccounts(AuthCascade, none)
		if note != "" {
			t.Errorf("stages %v: note = %q, want none", none, note)
		}
		if len(got) != len(none) {
			t.Errorf("stages %v: got %v, want the caller's own map", none, got)
		}
	}
	// Under cascade every declared stage is skipped, sorted, and the sentence
	// is the one the launch and `fugaro validate` print.
	got, note := CascadeSkippedStageAccounts(AuthCascade, deklared)
	if len(got) != 0 {
		t.Errorf("cascade stages = %v, want none: no account of the project's reaches a cascade run", got)
	}
	want := "project stage accounts do not apply to a cascade repository: adversarial, review skipped"
	if note != want {
		t.Errorf("note = %q, want %q", note, want)
	}
}

// TestCheckMixBudgetIsTheModeClause is the second half of the same rule: the
// one account an api-key run may hold exists because the gateway holds the
// provider key and routes the coder's calls to it, and only budget.mode:
// enforce routes a call to a provider at all. A configuration that declares a
// mix under any other mode declares a key that could never apply, which is
// what the whole rule refuses rather than ignores.
func TestCheckMixBudgetIsTheModeClause(t *testing.T) {
	// The owner's route table, claiming the coder's model for this repository:
	// one entry that serves it, and one that does not.
	routes := map[string]ModelProvider{
		"openrouter": {Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api",
			Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/other"}},
		"vendorx": {Kind: "anthropic-compat", BaseURL: "https://vendorx.example",
			Models: []string{"vendorx/*"}, AllowDataTo: []string{"acme/app"}},
	}
	const coder = "deepseek/deepseek-v4-flash"
	mixed := map[string]string{"review": "acme-overflow", "adversarial": "acme-overflow"}
	cases := []struct {
		name    string
		auth    string
		mode    string
		coder   string
		def     string
		stages  map[string]string
		routes  map[string]ModelProvider // nil: the owner's whole table
		want    []string                 // substrings the one problem must name; nil for none
		notWant []string                 // substrings the problem must not name
	}{
		{name: "api-key under enforce", auth: "api-key", mode: BudgetEnforce, coder: coder, def: "acme-overflow", stages: mixed},
		{name: "api-key under observe", auth: "api-key", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed,
			want: []string{"budget.mode", "enforce", "observe", "default_claude_account", "stages.review.claude_account", "openrouter"}},
		{name: "api-key under off, the project default alone", auth: "api-key", mode: BudgetOff, coder: coder, def: "acme-overflow",
			want: []string{"budget.mode", "default_claude_account"}},
		{name: "api-key under off, a stage key alone", auth: "api-key", mode: BudgetOff, coder: coder, stages: mixed,
			want: []string{"budget.mode", "stages.adversarial.claude_account"}},
		// The sentinel is not an account: a local config may spell the project
		// default "default" (localcfg's choiceRefProblem admits it), which names
		// the unsuffixed default account and no account of its own, and an
		// api-key run's job carries no OAuth token for the default at all. So
		// the key declares no mix, and the clause stays silent — a refusal here
		// would tell an operator to raise budget.mode for a key that names
		// nothing, and on the launch's side it would invent the second
		// credential mixedStageAccount refuses.
		{name: "api-key under observe, the project default is the sentinel", auth: "api-key", mode: BudgetObserve, coder: coder, def: DefaultClaudeAccount},
		{name: "api-key under off, the sentinel beside a real stage key", auth: "api-key", mode: BudgetOff, coder: coder, def: DefaultClaudeAccount,
			stages: map[string]string{"review": "acme-overflow"},
			want:   []string{"budget.mode", "stages.review.claude_account"}, notWant: []string{"default_claude_account"}},
		// The clause is judged only where the run has the shape that could mix:
		// with no provider route serving this repository's coder there is no
		// provider key in the gateway, whatever the mode says. The table is the
		// run's own — routes this repository may use, runner.ProvidersFor's own
		// narrowing, done by the caller — so a route written for another
		// repository is simply not here, which the last case pins.
		{name: "a coder no route serves", auth: "api-key", mode: BudgetObserve, coder: "claude-sonnet-5", def: "acme-overflow"},
		{name: "a route this repository does not carry", auth: "api-key", mode: BudgetObserve, coder: "vendorx/model", def: "acme-overflow",
			routes: map[string]ModelProvider{"openrouter": routes["openrouter"]}},
		{name: "no coder pin", auth: "api-key", mode: BudgetObserve, def: "acme-overflow"},
		// Nothing declared is nothing to refuse: default_claude_account is how
		// an oauth run picks its credential, and a run that names no mix is not
		// this clause's business under any mode.
		{name: "nothing declared", auth: "api-key", mode: BudgetObserve, coder: coder},
		{name: "a stage key outside the mix", auth: "api-key", mode: BudgetObserve, coder: coder, stages: map[string]string{"implement": "acme-overflow"}},
		{name: "an oauth run", auth: "oauth", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed},
		{name: "an auth the launch cannot place yet", auth: "", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := tc.routes
			if table == nil {
				table = routes
			}
			ps := CheckMixBudget(tc.auth, tc.mode, tc.coder, tc.def, tc.stages, table, "budget.mode")
			if tc.want == nil {
				if ps != nil {
					t.Fatalf("CheckMixBudget(%q, %q, %q, %q, %v) = %+v, want no problem", tc.auth, tc.mode, tc.coder, tc.def, tc.stages, ps)
				}
				return
			}
			if len(ps) != 1 {
				t.Fatalf("CheckMixBudget(%q, %q, %q, %q, %v) = %+v, want one problem", tc.auth, tc.mode, tc.coder, tc.def, tc.stages, ps)
			}
			for _, want := range tc.want {
				if !strings.Contains(ps[0].Path, want) && !strings.Contains(ps[0].Message, want) {
					t.Errorf("problem = %+v, want it to name %q", ps[0], want)
				}
			}
			for _, not := range tc.notWant {
				if strings.Contains(ps[0].Path, not) || strings.Contains(ps[0].Message, not) {
					t.Errorf("problem = %+v, want it not to name %q", ps[0], not)
				}
			}
		})
	}
}

// TestPoolServingPathsPlacesEveryMemberOnItsOwnPath pins D1634's reading: an
// OAuth or Anthropic-key member is served by the credentials service, an
// OpenRouter or Vertex member by the run's own gateway, a pool of one kind is a
// list one path can walk, and a pool of two kinds is one that can only fail
// over at a stage boundary (F7). The built-in vertex is placed without being
// defined, which is what makes it usable as a pool's last member (D1624).
func TestPoolServingPathsPlacesEveryMemberOnItsOwnPath(t *testing.T) {
	creds := []Credential{
		{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth},
		{Name: "acme-key", Type: CredentialTypeAnthropic},
		{Name: "acme-router", Type: CredentialTypeOpenRouter},
	}
	for _, tc := range []struct {
		name   string
		member []string
		paths  []string
	}{
		{"the service's own two types", []string{"acme-oauth", "acme-key"}, []string{CredentialPathCredsvc}},
		{"a gateway member alone", []string{"acme-router"}, []string{CredentialPathRunner}},
		{"the built-in vertex", []string{"vertex"}, []string{CredentialPathRunner}},
		{"one of each", []string{"acme-oauth", "acme-router"}, []string{CredentialPathCredsvc, CredentialPathRunner}},
		{"a repeat is one path", []string{"acme-oauth", "acme-oauth", "acme-key"}, []string{CredentialPathCredsvc}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, unplaced := PoolServingPaths(Pool{Name: "p", Members: tc.member}, creds)
			if len(unplaced) != 0 {
				t.Fatalf("members %v were not placed: %v", tc.member, unplaced)
			}
			if len(paths) != len(tc.paths) {
				t.Fatalf("paths = %v, want %v", paths, tc.paths)
			}
			for i, want := range tc.paths {
				if paths[i] != want {
					t.Fatalf("paths = %v, want %v", paths, tc.paths)
				}
			}
		})
	}
}

// TestCredentialServingPathPlacesOnlyTheTypesThatShip pins the other half: a
// type neither path serves is "" rather than a guess, so a caller that places
// credentials refuses it instead of sending it somewhere it could not be
// served.
func TestCredentialServingPathPlacesOnlyTheTypesThatShip(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{
		{CredentialTypeClaudeOAuth, CredentialPathCredsvc},
		{CredentialTypeAnthropic, CredentialPathCredsvc},
		{CredentialTypeOpenRouter, CredentialPathRunner},
		{CredentialTypeVertex, CredentialPathRunner},
		{CredentialTypeOpenAI, ""},
		{CredentialTypeCodexOAuth, ""},
		{"", ""},
		{"someone's-own", ""},
	} {
		if got := CredentialServingPath(tc.typ); got != tc.want {
			t.Errorf("CredentialServingPath(%q) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

// TestPoolServingPathsNamesTheMemberNothingServes pins the third answer: a
// member whose credential type no path serves is reported by name, because the
// sentence a caller prints has to say WHICH member it could not place.
func TestPoolServingPathsNamesTheMemberNothingServes(t *testing.T) {
	creds := []Credential{
		{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth},
		{Name: "acme-future", Type: CredentialTypeOpenAI},
	}
	paths, unplaced := PoolServingPaths(Pool{Name: "p", Members: []string{"acme-oauth", "acme-future"}}, creds)
	if len(paths) != 1 || paths[0] != CredentialPathCredsvc {
		t.Fatalf("paths = %v, want the one member it could place", paths)
	}
	if len(unplaced) != 1 || unplaced[0] != "acme-future" {
		t.Fatalf("unplaced = %v, want [acme-future]", unplaced)
	}
}

// TestAccountStagesAReleasedImageKnowsIsRunStagesMinusThisReleases: the
// vocabulary a published policy node's claude_account_stages may name and
// still be readable by every job image that reads it. A node's reader refuses
// the *whole* node when one of those names is not a stage of its own run
// (projectpolicy, through CheckStageSegment), and a reader is an image:
// `fugaro update` rebuilds the images behind its own confirmation, so an
// installation can still be running one built before this release — an
// in-flight run included. The list is what this release's publisher holds its
// entries back to, so a name added to RunStages and not to the field's own
// vocabulary is held back by construction rather than by whoever remembers.
//
// The six names are written out because they are a fact about a tag — the last
// release, 0.22.0 "Sloth", whose reader has them and no triage — and not about
// this tree. internal/cli's TestPublishHoldsBackAStageTheReleaseBehindCannotRead
// is the second reader of the same list, and the two have to name one set for
// the hold-back to be complete.
func TestAccountStagesAReleasedImageKnowsIsRunStagesMinusThisReleases(t *testing.T) {
	released := []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}
	if !slices.Equal(AccountStagesAReleasedImageKnows, released) {
		t.Fatalf("AccountStagesAReleasedImageKnows = %v, want %v: it is the vocabulary of the release the field runs, and a name moved in or out of it is a release decision", AccountStagesAReleasedImageKnows, released)
	}
	// Every name of the released vocabulary is still a stage of this run: one
	// dropped from RunStages and left here would hold nothing back and would
	// read as a stage no release ever had.
	for _, stage := range AccountStagesAReleasedImageKnows {
		if !IsRunStage(stage) {
			t.Errorf("AccountStagesAReleasedImageKnows names %q, which is not a stage of a run", stage)
		}
	}
	// What is held back is exactly what this release added and nothing else:
	// the vocabulary is derived, so a stage landing in RunStages is held back
	// by the same change that adds it.
	for _, stage := range RunStages {
		want := !slices.Contains(released, stage)
		if got := AccountStageHeldBack(stage); got != want {
			t.Errorf("AccountStageHeldBack(%q) = %v, want %v", stage, got, want)
		}
	}
	if !AccountStageHeldBack("triage") {
		t.Error("AccountStageHeldBack(triage) = false: no released image has the stage, so a node naming it is refused whole by the image in the field")
	}
	// A name that is no stage of a run at all is not "held back": it never
	// reaches the node, and CheckStageSegment is what refuses it.
	for _, stage := range []string{"bootstrap", "finalize", ""} {
		if AccountStageHeldBack(stage) {
			t.Errorf("AccountStageHeldBack(%q) = true: only a stage of a run can be held back from the node", stage)
		}
	}
}

// poolCreds is the credentials block the pool tests read: one of each type
// this release ships, so a member's name says its type and a test need not
// restate the block.
var poolCreds = []Credential{
	{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth},
	{Name: "acme-key", Type: CredentialTypeAnthropic},
	{Name: "acme-router", Type: CredentialTypeOpenRouter},
}

// TestPoolMembersKeepTheOrderAndCarryTheType pins the pool read the walk
// stands on: the members in the order the pool lists them — the order is what
// failover walks (D1632), so a set would lose the only thing the list is for —
// each with the type it was defined with and the path that serves it, and the
// built-in vertex placed without any layer defining it (D1624).
func TestPoolMembersKeepTheOrderAndCarryTheType(t *testing.T) {
	p := Pool{Name: "acme-pool", Members: []string{"acme-router", "vertex", "acme-oauth", "acme-key"}}
	want := []PoolMember{
		{Name: "acme-router", Type: CredentialTypeOpenRouter, Path: CredentialPathRunner},
		{Name: "vertex", Type: CredentialTypeVertex, Path: CredentialPathRunner},
		{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth, Path: CredentialPathCredsvc},
		{Name: "acme-key", Type: CredentialTypeAnthropic, Path: CredentialPathCredsvc},
	}
	got := PoolMembers(p, poolCreds)
	if len(got) != len(want) {
		t.Fatalf("PoolMembers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PoolMembers[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// A member of a type nothing serves is carried rather than dropped: a
	// caller refusing the pool has to name which member it could not place.
	unplaced := PoolMembers(Pool{Members: []string{"acme-future"}},
		[]Credential{{Name: "acme-future", Type: CredentialTypeOpenAI}})
	if len(unplaced) != 1 || unplaced[0].Name != "acme-future" || unplaced[0].Path != "" {
		t.Fatalf("PoolMembers = %+v, want the member carried with no path", unplaced)
	}
}

// TestPoolHasVertexMember pins D1626's F8 clause: the built-in vertex is the
// member that makes a pool un-enforceable under budget.mode: enforce, and the
// reader that has to refuse the pool asks for it by name.
func TestPoolHasVertexMember(t *testing.T) {
	for _, tc := range []struct {
		members []string
		want    string
	}{
		{[]string{"acme-oauth", "acme-key"}, ""},
		{[]string{"acme-oauth", "vertex"}, CredentialVertex},
		{[]string{"vertex", "acme-oauth"}, CredentialVertex},
		{[]string{"acme-router"}, ""},
	} {
		if got := PoolHasVertexMember(Pool{Members: tc.members}, poolCreds); got != tc.want {
			t.Errorf("PoolHasVertexMember(%v) = %q, want %q", tc.members, got, tc.want)
		}
	}
}

// TestPoolMemberServesModelSplitsTheTwoSpellings pins F23/D1637's rule: the
// credentials that reach Anthropic itself and Vertex take Claude's own ID, an
// OpenRouter member takes the ID its own provider's route claims, and neither
// takes the other's. It is the whole of "as written": there is no per-member
// mapping, so the two spellings cannot both serve one craft's model string.
//
// The OpenRouter row is the one that has to be asked of the member rather than
// of the run's providers: "acme-router" owns one spelling and a second provider
// owns the other, and a member that named the second would be a member the
// run's routes could not send the model to at all.
func TestPoolMemberServesModelSplitsTheTwoSpellings(t *testing.T) {
	providers := map[string]ModelProvider{
		"acme-router": {
			Kind: ModelProviderKind, BaseURL: "https://router.example/api", Auth: "bearer",
			Models: []string{"anthropic/claude-sonnet-5.5", "deepseek/*"},
		},
		"acme-other": {
			Kind: ModelProviderKind, BaseURL: "https://other.example/api", Auth: "bearer",
			Models: []string{"meta/llama-4"},
		},
	}
	const direct, routed, other = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", "deepseek/deepseek-v4-flash"
	for _, tc := range []struct {
		member PoolMember
		model  string
		want   bool
	}{
		{PoolMember{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth}, direct, true},
		{PoolMember{Name: "acme-oauth", Type: CredentialTypeClaudeOAuth}, routed, false},
		{PoolMember{Name: "acme-key", Type: CredentialTypeAnthropic}, direct, true},
		{PoolMember{Name: CredentialVertex, Type: CredentialTypeVertex}, direct, true},
		{PoolMember{Name: CredentialVertex, Type: CredentialTypeVertex}, other, false},
		{PoolMember{Name: "acme-router", Type: CredentialTypeOpenRouter}, routed, true},
		{PoolMember{Name: "acme-router", Type: CredentialTypeOpenRouter}, other, true},
		{PoolMember{Name: "acme-router", Type: CredentialTypeOpenRouter}, direct, false},
		// The member's own provider, not another one of the run's: acme-other
		// serves llama alone, so it is no member of a pool the coder's Claude
		// route ID goes out on — however many providers the run holds.
		{PoolMember{Name: "acme-other", Type: CredentialTypeOpenRouter}, routed, false},
		// A member naming no provider this run holds serves nothing.
		{PoolMember{Name: "acme-gone", Type: CredentialTypeOpenRouter}, routed, false},
		{PoolMember{Name: "acme-oauth", Type: CredentialTypeOpenAI}, direct, false},
		{PoolMember{Name: "acme-oauth"}, direct, false},
	} {
		if got := PoolMemberServesModel(tc.member, tc.model, providers); got != tc.want {
			t.Errorf("PoolMemberServesModel(%+v, %q) = %v, want %v", tc.member, tc.model, got, tc.want)
		}
	}
}

// TestPoolModelProblemsRefusesAMemberThatDoesNotServeTheModel pins the refusal
// the rule above is for: a pool is admitted only if every member serves the
// model as written, and the problem names the member that does not — a reader
// told only that "the pool is refused" would have to find which member did it.
func TestPoolModelProblemsRefusesAMemberThatDoesNotServeTheModel(t *testing.T) {
	providers := map[string]ModelProvider{"acme-router": {
		Kind: ModelProviderKind, BaseURL: "https://router.example/api", Auth: "bearer",
		Models: []string{"deepseek/*"},
	}}
	// A pool of the two direct types serves a Claude ID and is admitted.
	if ps := PoolModelProblems(Pool{Name: "p", Members: []string{"acme-oauth", "acme-key"}},
		poolCreds, "claude-sonnet-5-5", providers, "agent.crafts.coder.access"); len(ps) != 0 {
		t.Fatalf("a pool every member serves was refused: %v", ps)
	}
	// Mixed with a credential whose provider does not serve a Claude ID: the
	// member is named, and the key the pool was named by is where it is
	// reported.
	ps := PoolModelProblems(Pool{Name: "p", Members: []string{"acme-oauth", "acme-router"}},
		poolCreds, "claude-sonnet-5-5", providers, "agent.crafts.coder.access")
	if len(ps) != 1 {
		t.Fatalf("problems = %v, want the one member that does not serve the model", ps)
	}
	for _, want := range []string{"agent.crafts.coder.access", "acme-router", "claude-sonnet-5-5"} {
		if !strings.Contains(ps[0].String(), want) {
			t.Errorf("the problem does not name %q: %s", want, ps[0].String())
		}
	}
	// The built-in vertex serves Claude's own ID and is placed without a
	// definition, so a pool it is the last member of is admitted.
	if ps := PoolModelProblems(Pool{Name: "p", Members: []string{"acme-key", "vertex"}},
		poolCreds, "claude-sonnet-5-5", providers, "agent.crafts.coder.access"); len(ps) != 0 {
		t.Fatalf("a pool of the key and the built-in vertex was refused: %v", ps)
	}
}
