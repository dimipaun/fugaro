package craft

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// legacyVocabulary is one project's declared credentials and pools, as
// LegacyInput carries them: the two named accounts, the project's own Anthropic
// key (declared under config.CredentialAnthropicKey) and the pool the migration
// writes for the cascade.
func legacyVocabulary() ([]config.Credential, []config.Pool) {
	return []config.Credential{
		{Name: "alice", Type: config.CredentialTypeClaudeOAuth},
		{Name: "bob", Type: config.CredentialTypeClaudeOAuth},
		{Name: config.CredentialAnthropicKey, Type: config.CredentialTypeAnthropic},
	}, []config.Pool{{
		Name:       config.PoolCascade,
		Members:    []string{"alice", config.CredentialAnthropicKey, config.CredentialVertex},
		LegacyOpen: true,
	}}
}

// legacyHero is the rule's input for a repository whose file writes nothing of
// the retired vocabulary: no auth, no token, no stage account, no access and no
// grade. Every case below changes one of those.
func legacyHero() LegacyInput {
	creds, pools := legacyVocabulary()
	return LegacyInput{
		Credentials: creds,
		Pools:       pools,
		CascadePool: config.PoolCascade,
	}
}

// TestTheLegacyRuleAnswersEveryOldAuthValue is the contract's own pin over (c):
// the access each old auth value, stage account, file access and grade access
// means, and the answer a file that writes none of them gets — nothing, never
// Vertex (F49).
//
// It is also §2's row "a repository not yet migrated runs as its file says":
// the rule is what a new CLI and a new runner work the access out with for a
// repository `update` has not migrated (R3), so every arm here is an answer
// about a run that is already going out on something today.
func TestTheLegacyRuleAnswersEveryOldAuthValue(t *testing.T) {
	credential := func(name, typ string) []config.PoolMember {
		return []config.PoolMember{{Name: name, Type: typ, Path: config.CredentialServingPath(typ)}}
	}
	withAuth := func(auth string) func(*LegacyInput) { return func(in *LegacyInput) { in.Auth = auth } }

	cases := []struct {
		name  string
		edit  func(*LegacyInput)
		stage string
		grade string
		want  Access
		ok    bool
	}{
		{
			name:  "auth vertex is the built-in credential, named and served by the run's own gateway",
			edit:  withAuth("vertex"),
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.CredentialVertex,
				Members: credential(config.CredentialVertex, config.CredentialTypeVertex),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name:  "auth api-key is the project's own Anthropic key, served by the credentials service",
			edit:  withAuth("api-key"),
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.CredentialAnthropicKey,
				Members: credential(config.CredentialAnthropicKey, config.CredentialTypeAnthropic),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name:  "auth oauth with no account anywhere is the project's default account, and NOT chosen (F35)",
			edit:  withAuth("oauth"),
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.DefaultClaudeAccount,
				Members: credential(config.DefaultClaudeAccount, config.CredentialTypeClaudeOAuth),
				Chosen:  false,
			},
			ok: true,
		},
		{
			name: "the file's agent.oauth_token is the account, and it was chosen",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.OAuthToken = "bob"
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: "bob",
				Members: credential("bob", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "the project's stage account is the access of that stage's craft, and it was chosen (D3068)",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.StageAccounts = map[string]string{"review_first": "alice"}
			},
			stage: "review_first",
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			// A stage account is a STAGE's key, and this arm is what runs as it
			// did: the stage the project wrote nothing for runs on the default
			// account, exactly as it does today. A craft whose stages name
			// different accounts is a craft the migration cannot write one
			// access for, and it reports it (CM4) rather than this rule moving a
			// stage's runs onto another credential.
			name:  "a stage account does not reach a stage that names none",
			edit:  func(in *LegacyInput) { in.Auth = "oauth"; in.StageAccounts = map[string]string{"review": "alice"} },
			stage: "triage",
			want: Access{
				Kind: AccessCredential, Name: config.DefaultClaudeAccount,
				Members: credential(config.DefaultClaudeAccount, config.CredentialTypeClaudeOAuth),
				Chosen:  false,
			},
			ok: true,
		},
		{
			// The mix (docs/design/mixed-credentials.md §11, D1848): an api-key
			// run holds the provider key for its coder's stages and one Claude
			// account for review, adversarial and triage. Answering the key for
			// those three would move them onto the paid key, which is the move
			// a migration may not make (R3, R7).
			name: "an api-key run's mix stage goes out on the account the project names for it",
			edit: func(in *LegacyInput) {
				in.Auth = "api-key"
				in.StageAccounts = map[string]string{"review": "alice"}
			},
			stage: "review",
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "an api-key run's mix stage with no key of its own goes out on the project default, which is the mix's account (mixedStageAccount)",
			edit: func(in *LegacyInput) {
				in.Auth = "api-key"
				in.DefaultAccount = "alice"
			},
			stage: "triage",
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "the mix's account does not reach a coder stage, which stays on the project's key",
			edit: func(in *LegacyInput) {
				in.Auth = "api-key"
				in.DefaultAccount = "alice"
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.CredentialAnthropicKey,
				Members: credential(config.CredentialAnthropicKey, config.CredentialTypeAnthropic),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name:  "a mix stage the project names no account for goes out on the project's key",
			edit:  func(in *LegacyInput) { in.Auth = "api-key" },
			stage: "review",
			want: Access{
				Kind: AccessCredential, Name: config.CredentialAnthropicKey,
				Members: credential(config.CredentialAnthropicKey, config.CredentialTypeAnthropic),
				Chosen:  true,
			},
			ok: true,
		},
		{
			// The walk's order (docs/design/mixed-credentials.md §11.2): the
			// file's agent.oauth_token is rung 3 and the project's
			// default_claude_account is rung 4, so a repository pinned to an
			// account of its own keeps it.
			name: "the file's oauth_token beats the project's default account",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.OAuthToken = "bob"
				in.DefaultAccount = "alice"
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: "bob",
				Members: credential("bob", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "a repository that names no account of its own inherits the project's default account, and it was chosen",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.DefaultAccount = "alice"
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "a stage account does not reach another craft, which stays on the default account",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.StageAccounts = map[string]string{"review": "alice"}
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.DefaultClaudeAccount,
				Members: credential(config.DefaultClaudeAccount, config.CredentialTypeClaudeOAuth),
				Chosen:  false,
			},
			ok: true,
		},
		{
			name:  "auth cascade is the pool the migration wrote, marked as the cascade's (D3064 as amended)",
			edit:  withAuth("cascade"),
			stage: "implement",
			want: Access{
				Kind: AccessPool, Name: config.PoolCascade,
				Members: []config.PoolMember{
					{Name: "alice", Type: config.CredentialTypeClaudeOAuth, Path: config.CredentialPathCredsvc},
					{Name: config.CredentialAnthropicKey, Type: config.CredentialTypeAnthropic, Path: config.CredentialPathCredsvc},
					{Name: config.CredentialVertex, Type: config.CredentialTypeVertex, Path: config.CredentialPathRunner},
				},
				Chosen:  true,
				Cascade: true,
			},
			ok: true,
		},
		{
			name: "the file's own access wins over the auth value (D3062)",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.Accesses.Coder.Access = "alice"
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "a file's access is the access of its own craft, not of every craft",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.Accesses.Coder.Access = "alice"
			},
			stage: "review",
			want: Access{
				Kind: AccessCredential, Name: config.DefaultClaudeAccount,
				Members: credential(config.DefaultClaudeAccount, config.CredentialTypeClaudeOAuth),
				Chosen:  false,
			},
			ok: true,
		},
		{
			name: "the grade's access wins over the craft's, for the task that names it (F38)",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.Grades.Strong = &config.Craft{Access: "alice"}
			},
			stage: "implement",
			grade: config.GradeStrong,
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			// The crafts block is read WITH inheritance (Inherited), because the
			// run's own resolution walks the same chain: the first-line review
			// follows the coder, so a repository whose coder's craft names a pool
			// serves its first-line review on that pool today. Answering the
			// retired auth chain here would drop the owner's pool and send the
			// review wherever agent.auth points, which is the move a migration
			// may not make (R3).
			name: "a craft that follows the coder goes out on the access the coder's craft names (Inherited)",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.DefaultAccount = "bob"
				in.Pools = append(in.Pools, config.Pool{Name: "review-pool", Members: []string{"alice"}})
				in.Accesses.Coder.Access = "review-pool"
			},
			stage: "review_first",
			want: Access{
				Kind: AccessPool, Name: "review-pool",
				Members: []config.PoolMember{{Name: "alice", Type: config.CredentialTypeClaudeOAuth, Path: config.CredentialPathCredsvc}},
				Chosen:  true,
			},
			ok: true,
		},
		{
			// The other half of the same chain: a craft that follows nobody is
			// served no other craft's access, and the senior review is one — the
			// grade is written into no craft it runs, so the retired keys answer
			// it as they answer every other craft of a block that sets nothing.
			name: "a craft that follows nobody keeps the retired keys: the senior review is not moved onto the coder's pool",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.DefaultAccount = "bob"
				in.Pools = append(in.Pools, config.Pool{Name: "review-pool", Members: []string{"alice"}})
				in.Accesses.Coder.Access = "review-pool"
				in.Grades.Strong = &config.Craft{Access: "alice"}
			},
			stage: "review",
			grade: config.GradeStrong,
			want: Access{
				Kind: AccessCredential, Name: "bob",
				Members: credential("bob", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "the grade's access reaches the merge judge, the other craft applyGrade writes it into",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.Grades.Strong = &config.Craft{Access: "alice"}
			},
			stage: "merge_judge",
			grade: config.GradeStrong,
			want: Access{
				Kind: AccessCredential, Name: "alice",
				Members: credential("alice", config.CredentialTypeClaudeOAuth),
				Chosen:  true,
			},
			ok: true,
		},
		{
			// The judge spends model calls of its own (E6), so its stage answers
			// the merge judge's craft: a rule with no craft for merge_judge would
			// refuse the run (F49) instead of serving the judge.
			name:  "the merge judge is a stage with a craft of its own, and it answers it",
			edit:  withAuth("vertex"),
			stage: "merge_judge",
			want: Access{
				Kind: AccessCredential, Name: config.CredentialVertex,
				Members: credential(config.CredentialVertex, config.CredentialTypeVertex),
				Chosen:  true,
			},
			ok: true,
		},
		{
			name: "a task that names no grade reads no grade's access",
			edit: func(in *LegacyInput) {
				in.Auth = "oauth"
				in.Grades.Strong = &config.Craft{Access: "alice"}
			},
			stage: "implement",
			want: Access{
				Kind: AccessCredential, Name: config.DefaultClaudeAccount,
				Members: credential(config.DefaultClaudeAccount, config.CredentialTypeClaudeOAuth),
				Chosen:  false,
			},
			ok: true,
		},
		{
			name: "a file that writes none of them means nothing, never Vertex (F49)",
			edit: func(*LegacyInput) {},
			want: Access{},
			ok:   false,
		},
		{
			name: "a file that names an auth this vocabulary does not know means nothing either",
			edit: withAuth("something-else"),
			want: Access{},
			ok:   false,
		},
		{
			name:  "a stage that spends no model call has no access to answer",
			edit:  withAuth("vertex"),
			stage: "merge_judge_not_a_stage",
			want:  Access{},
			ok:    false,
		},
	}

	// A grade's access is READ for the coder's craft and the merge judge's — the
	// two crafts applyGrade writes it into (Crafts.Coder, Crafts.MergeJudge,
	// internal/runner/routing.go) — and for no craft outright. A craft that
	// follows one of them is served it anyway, because the block the caller hands
	// carries the written grade and the read below inherits inside that block:
	// reading the grade for every craft outright would take a senior review on a
	// pool of its own onto the coder's credential (R3, F38).
	//
	// The shape is checked with auth vertex, where the grade's arm stands in
	// front of the repository's own access: a craft answering Vertex is the whole
	// of "no grade was read for it, and nothing it follows sets one".
	graded := func() LegacyInput {
		in := legacyHero()
		in.Auth = "vertex"
		in.Grades.Strong = &config.Craft{Access: "alice"}
		return in
	}
	for _, stage := range []string{"plan", "implement", "fix", "merge_judge"} {
		t.Run("a grade's access reaches "+stage+", a craft applyGrade writes it into", func(t *testing.T) {
			got, ok := LegacyAccess(graded(), stage, config.GradeStrong)
			if !ok || got.Name != "alice" {
				t.Fatalf("LegacyAccess(%q, %q) = %+v, ok=%v; want the grade's access", stage, config.GradeStrong, got, ok)
			}
		})
	}
	for _, stage := range []string{"review", "review_first", "adversarial", "triage"} {
		t.Run("a craft "+stage+" runs is read no grade, and follows nothing that sets one", func(t *testing.T) {
			got, ok := LegacyAccess(graded(), stage, config.GradeStrong)
			if !ok || got.Name != config.CredentialVertex {
				t.Fatalf("LegacyAccess(%q, %q) = %+v, ok=%v; want the repository's own access, Vertex", stage, config.GradeStrong, got, ok)
			}
		})
	}

	// The block a graded run really hands: applyGrade has written the grade into
	// the coder's craft, so the craft that follows the coder is served it too.
	// That is the run's own answer today — a graded craft.Resolve answers the
	// first-line review the grade's access, inherited from the coder — and the
	// rule reproduces it rather than adding to it.
	written := func() LegacyInput {
		in := legacyHero()
		in.Auth = "vertex"
		in.Grades.Strong = &config.Craft{Access: "alice"}
		in.Accesses.Coder.Access = "alice"
		return in
	}
	for _, tc := range []struct {
		stage string
		want  string
	}{
		{"plan", "alice"},
		{"implement", "alice"},
		{"fix", "alice"},
		{"merge_judge", "alice"},
		{"review_first", "alice"},
		{"review", config.CredentialVertex},
		{"triage", config.CredentialVertex},
		{"adversarial", config.CredentialVertex},
	} {
		t.Run("with the grade written into the coder's craft, "+tc.stage+" goes out on its craft's access", func(t *testing.T) {
			got, ok := LegacyAccess(written(), tc.stage, config.GradeStrong)
			if !ok || got.Name != tc.want {
				t.Fatalf("LegacyAccess(%q, %q) = %+v, ok=%v; want %q", tc.stage, config.GradeStrong, got, ok, tc.want)
			}
		})
	}

	// The unsuffixed default's own spelling is a name an operator can write, and
	// both keys that may carry one admit it deliberately (localcfg's
	// choiceRefProblem): a stage key or an agent.oauth_token spelled `default`
	// named that account, so F35's "not chosen" — the one case the walk may move
	// off an exhausted subscription — belongs to the run that named none, and
	// Chosen comes from the rung rather than from the name it answered.
	for _, tc := range []struct {
		name string
		edit func(*LegacyInput)
	}{
		{"a stage's own key", func(in *LegacyInput) {
			in.StageAccounts = map[string]string{"review_first": config.DefaultClaudeAccount}
		}},
		{"the file's agent.oauth_token", func(in *LegacyInput) {
			in.OAuthToken = config.DefaultClaudeAccount
		}},
	} {
		t.Run("the sentinel spelled by hand in "+tc.name+" is a chosen account (F35)", func(t *testing.T) {
			in := legacyHero()
			in.Auth = "oauth"
			tc.edit(&in)
			got, ok := LegacyAccess(in, "review_first", "")
			if !ok || got.Name != config.DefaultClaudeAccount || !got.Chosen {
				t.Fatalf("LegacyAccess(review_first) = %+v, ok=%v; want the unsuffixed account named and chosen", got, ok)
			}
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := legacyHero()
			tc.edit(&in)
			got, ok := LegacyAccess(in, tc.stage, tc.grade)
			if ok != tc.ok {
				t.Fatalf("LegacyAccess(%q, %q) answered ok=%v, want %v (access %+v)", tc.stage, tc.grade, ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got.Kind != tc.want.Kind || got.Name != tc.want.Name ||
				got.Chosen != tc.want.Chosen || got.Cascade != tc.want.Cascade {
				t.Errorf("LegacyAccess(%q, %q) = %+v, want %+v", tc.stage, tc.grade, got, tc.want)
			}
			if len(got.Members) != len(tc.want.Members) {
				t.Fatalf("LegacyAccess(%q, %q) members = %+v, want %+v", tc.stage, tc.grade, got.Members, tc.want.Members)
			}
			for i, m := range got.Members {
				if m != tc.want.Members[i] {
					t.Errorf("member %d = %+v, want %+v", i, m, tc.want.Members[i])
				}
			}
		})
	}
}

// TestTheStubAnswersTheLegacyRule is the stub half of (c): a stub resolver
// answers the migration's rule alone, so every side written against C1 compiles
// and behaves as today until its real side lands (I1 deletes the stub).
//
// The two answers are compared over the whole table above rather than one case,
// because "the same" is the whole of what the stub promises: a stub that
// answered a shape of its own would be a second rule, which is the drift the
// contract exists to prevent.
func TestTheStubAnswersTheLegacyRule(t *testing.T) {
	inputs := []struct {
		in    LegacyInput
		stage string
		grade string
	}{
		{func() LegacyInput { in := legacyHero(); in.Auth = "vertex"; return in }(), "implement", ""},
		{func() LegacyInput { in := legacyHero(); in.Auth = "api-key"; return in }(), "implement", ""},
		{func() LegacyInput { in := legacyHero(); in.Auth = "oauth"; return in }(), "plan", ""},
		{func() LegacyInput { in := legacyHero(); in.Auth = "oauth"; in.OAuthToken = "alice"; return in }(), "fix", ""},
		{
			func() LegacyInput {
				in := legacyHero()
				in.Auth = "oauth"
				in.StageAccounts = map[string]string{"review": "bob"}
				return in
			}(), "review", "",
		},
		{func() LegacyInput { in := legacyHero(); in.Auth = "cascade"; return in }(), "implement", ""},
		{
			func() LegacyInput {
				in := legacyHero()
				in.Auth = "api-key"
				in.DefaultAccount = "alice"
				return in
			}(), "triage", "",
		},
		{
			func() LegacyInput {
				in := legacyHero()
				in.Auth = "oauth"
				in.OAuthToken = "bob"
				in.DefaultAccount = "alice"
				return in
			}(), "implement", "",
		},
		{func() LegacyInput { in := legacyHero(); in.Auth = "oauth"; return in }(), "implement", config.GradeStrong},
		{legacyHero(), "implement", ""},
	}

	for i, tc := range inputs {
		want, wantOK := LegacyAccess(tc.in, tc.stage, tc.grade)
		got, gotOK := StubResolver(tc.in).AccessFor(tc.stage, tc.grade)
		if gotOK != wantOK {
			t.Errorf("case %d: the stub answered ok=%v, the rule ok=%v", i, gotOK, wantOK)
			continue
		}
		if !wantOK {
			continue
		}
		if got.Kind != want.Kind || got.Name != want.Name || got.Chosen != want.Chosen || got.Cascade != want.Cascade {
			t.Errorf("case %d: the stub answered %+v, the rule %+v", i, got, want)
		}
		if len(got.Members) != len(want.Members) {
			t.Errorf("case %d: the stub answered %+v, the rule %+v", i, got.Members, want.Members)
		}
	}

	// The stub is fail-closed about the one thing it cannot decide: a file that
	// writes none of the retired keys is refused rather than guessed at, and
	// the refusal is the stub's own (an error), never Vertex (F49).
	if _, ok := StubResolver(legacyHero()).AccessFor("implement", ""); ok {
		t.Error("the stub answered an access for a file that writes no retired key, want none")
	}
}

// TestTheHandedEntryIsTheAnswerItEncodes pins (b)'s encoding: the launcher puts
// the answer it resolved in the execution's environment under EnvAccess as one
// JSON document, and the run reads back the same answers.
func TestTheHandedEntryIsTheAnswerItEncodes(t *testing.T) {
	in := legacyHero()
	in.Auth = "cascade"
	coder, ok := LegacyAccess(in, "implement", "")
	if !ok {
		t.Fatal("the rule answered nothing for a cascade repository")
	}
	doc := Resolved{
		Version: ResolvedVersion,
		Crafts:  map[string]Access{config.CraftCoder: coder},
	}
	encoded, err := doc.Encode()
	if err != nil {
		t.Fatalf("encoding the resolved access: %v", err)
	}
	if encoded == "" {
		t.Fatal("the resolved access encoded to nothing")
	}
	back, found, err := ParseResolved(encoded)
	if err != nil || !found {
		t.Fatalf("reading the resolved access back: found=%v err=%v", found, err)
	}
	got := back.Crafts[config.CraftCoder]
	if got.Kind != coder.Kind || got.Name != coder.Name || got.Chosen != coder.Chosen || got.Cascade != coder.Cascade {
		t.Errorf("the entry read back as %+v, want %+v", got, coder)
	}
	if len(got.Members) != len(coder.Members) {
		t.Errorf("the entry read back with members %+v, want %+v", got.Members, coder.Members)
	}
	if _, found, err := ParseResolved(""); found || err != nil {
		t.Errorf("an empty entry read as found=%v err=%v, want not found and no error", found, err)
	}
	if _, _, err := ParseResolved("{not json"); err == nil {
		t.Error("an entry that is not JSON was accepted")
	}
}

// TestTheHandedEntryRefusesWhatThisReleaseCannotReadBack pins the failure half
// of (b)'s encoding, and it is the half a run's safety rests on: the document is
// the launcher's own, written by the release that handed it, so one that cannot
// be read back in full is refused rather than half understood — a half-read
// document could send a stage's calls to something the launcher never resolved,
// and falling back would apply the rule to a file the launcher may already have
// replaced (access_stub.go is the runner's side of that decision).
//
// Every case is a valid document with one thing changed, so the refusal is the
// check's and not a document that was never JSON.
func TestTheHandedEntryRefusesWhatThisReleaseCannotReadBack(t *testing.T) {
	in := legacyHero()
	in.Auth = "oauth"
	coder, ok := LegacyAccess(in, "implement", "")
	if !ok {
		t.Fatal("the rule answered nothing for an oauth repository")
	}
	encoded, err := Resolved{Version: ResolvedVersion, Crafts: map[string]Access{config.CraftCoder: coder}}.Encode()
	if err != nil {
		t.Fatalf("encoding the resolved access: %v", err)
	}

	for _, tc := range []struct {
		name string
		doc  string
		want error
	}{
		{"a version this release does not know", strings.Replace(encoded, `"version":1`, `"version":99`, 1), ErrResolvedVersion},
		{"a field this release does not model", strings.Replace(encoded, `"version":1`, `"version":1,"surprise":true`, 1), ErrResolvedMalformed},
		{"a craft this vocabulary does not hold", strings.Replace(encoded, `"coder"`, `"sculptor"`, 1), ErrResolvedMalformed},
		{"a craft name under grades", strings.Replace(encoded, `"crafts"`, `"grades"`, 1), ErrResolvedMalformed},
		{"not a document at all", "{not json", ErrResolvedMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			back, found, err := ParseResolved(tc.doc)
			if !errors.Is(err, tc.want) || found {
				t.Fatalf("ParseResolved(%s) = %+v, found=%v err=%v; want %v and not found", tc.doc, back, found, err, tc.want)
			}
		})
	}

	// The writer's half, which is the same rule one step earlier: a launcher that
	// encoded an answer this release would refuse would hand a run a document it
	// cannot use, and the refusal is what keeps the two ends one contract.
	for _, tc := range []struct {
		name string
		doc  Resolved
	}{
		{"a craft this vocabulary does not hold", Resolved{Version: ResolvedVersion, Crafts: map[string]Access{"sculptor": coder}}},
		{"a grade this vocabulary does not hold", Resolved{Version: ResolvedVersion, Grades: map[string]Access{"stout": coder}}},
		{"a version this release does not write", Resolved{Version: ResolvedVersion + 1, Crafts: map[string]Access{config.CraftCoder: coder}}},
	} {
		t.Run("Encode refuses "+tc.name, func(t *testing.T) {
			if out, err := tc.doc.Encode(); err == nil {
				t.Fatalf("Encode() = %q, want a refusal", out)
			}
		})
	}
}

// TestARunWithNoHandedEntryAppliesTheLegacyRule pins the other half of (b): the
// one answer a run with no entry has, and the entry's own name — the launcher's
// and the run's, spelled once here.
func TestARunWithNoHandedEntryAppliesTheLegacyRule(t *testing.T) {
	if EnvAccess == "" {
		t.Fatal("EnvAccess is empty: a run has no entry to read its access from")
	}
	if EnvAccess == config.DefaultClaudeAccount {
		t.Fatalf("EnvAccess is %q, which is an account's name and not an environment entry", EnvAccess)
	}
	in := legacyHero()
	in.Auth = "api-key"
	entry, _ := LegacyAccess(in, "plan", "")
	if entry.Name != config.CredentialAnthropicKey {
		t.Fatalf("the fallback answered %+v, want the project's Anthropic credential", entry)
	}
	if _, ok := LegacyAccess(legacyHero(), "plan", ""); ok {
		t.Error("the fallback answered for a file with no retired key, want a refusal")
	}
	if LegacyNoAccessRefusal("owner/name") == "" {
		t.Error("the refusal for a run that can resolve nothing is empty")
	}
}
