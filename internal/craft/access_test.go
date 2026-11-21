package craft

import (
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
