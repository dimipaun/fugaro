package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// TestAllowedModelClaimNeedsARoute pins the one consequence of the claim-blind
// pricing half that a reader is most likely to get wrong (D1): a
// budget.allowed_models entry of `anthropic/claude-sonnet-5.5` now passes
// config-time validation -- CheckModelID judges the spelling, and under P4 the
// spelling is legal -- and is refused **only** by CheckProviderPolicy, which
// walks the allow-list beside the pins. Nothing is unguarded (the allow-list
// was never the gate for who may route a model), but a writer must not assume
// a bad entry fails at parse time.
func TestAllowedModelClaimNeedsARoute(t *testing.T) {
	if msg := CheckModelID(claimID); msg != "" {
		t.Errorf("CheckModelID(%q) = %q, want accepted: the spelling is legal for a claim", claimID, msg)
	}
	const doc = `
version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }
budget:
  mode: enforce
  per_run_usd: 2
  allowed_models: ["deepseek/deepseek-v4-flash", "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"]
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, problems := Parse([]byte(doc))
	if len(problems) > 0 {
		t.Fatalf("the file was refused at parse time, so this test proves nothing: %v", problems)
	}
	if got := problemsText(validateBudget(cfg.Budget)); got != "" {
		t.Fatalf("validateBudget refused the entry: %q", got)
	}
	// No route names the ID: the entry is refused by the provider rule, at
	// its own index, with the same message the pin gets.
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	got := problemsText(CheckProviderPolicy(cfg.Agent, cfg.Budget.AllowedModels, "dimipaun/fugaro", ps, claimPrices(t, ps)))
	if !strings.Contains(got, "budget.allowed_models[2]") || !strings.Contains(got, "no provider serves it") || !strings.Contains(got, "is a Claude model") {
		t.Errorf("an unclaimed allow-list entry was not refused: %q", got)
	}
	// The route names exactly that ID: the same file has nothing left to
	// refuse.
	p := okProvider()
	p.Models = []string{"deepseek/*", claimID}
	ps = map[string]ModelProvider{"openrouter": p}
	if got := problemsText(CheckProviderPolicy(cfg.Agent, cfg.Budget.AllowedModels, "dimipaun/fugaro", ps, claimPrices(t, ps))); got != "" {
		t.Errorf("a claimed allow-list entry was refused: %q", got)
	}
}

// TestValidateGitAuthorClosedVocabulary pins git.author's vocabulary (A2a):
// the key is the repository's opt-in for the launcher's own identity as the
// commit author (D604, D606), so a value outside it is a launcher's bug
// rather than a run's opinion, exactly as git.provider's two values are.
// The empty value is legal and is the key absent: it is today's behaviour,
// the run's own identity authoring its commits.
//
// The refusal names both values, because that sentence is what an author
// reads to fix the file — the same reason git.provider's does.
func TestValidateGitAuthorClosedVocabulary(t *testing.T) {
	const base = `
version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, ps := Parse([]byte(strings.Replace(base, "git: { provider: github }", "git: { provider: github, author: launcher }", 1)))
	if len(ps) > 0 {
		t.Fatalf("git.author: launcher was refused: %v", ps)
	}
	if cfg.Git.Author != GitAuthorLauncher {
		t.Fatalf("git.author = %q, want %q", cfg.Git.Author, GitAuthorLauncher)
	}
	for _, author := range []string{"", GitAuthorFugaro, GitAuthorLauncher} {
		cfg.Git.Author = author
		if got := problemsText(Validate(cfg)); got != "" {
			t.Errorf("git.author = %q was refused: %q", author, got)
		}
	}
	// A near miss — the value with the wrong case — is refused at its own
	// path, naming both values it could have been.
	cfg.Git.Author = "Fugaro"
	got := problemsText(Validate(cfg))
	if !strings.Contains(got, "git.author") {
		t.Errorf("an unknown git.author was not refused at its own path: %q", got)
	}
	for _, want := range []string{GitAuthorFugaro, GitAuthorLauncher} {
		if !strings.Contains(got, want) {
			t.Errorf("the git.author refusal does not name %q: %q", want, got)
		}
	}
}

// TestGitAuthorAbsentKeepsTheConfigHash: git.author's absence is the common
// case, so the resolved config of a file that does not write the key has to
// encode without it. SHA256 marshals the resolved struct (resolve.go) and is
// what decides whether a repository's derived image is rebuilt, so a new key
// written as "" would move the hash — and so the image — of every repository
// on an upgrade that changed no file. That is the property Rigor's and
// Governance's own json tags exist for, and it is the one place the key's
// encoding is observable.
func TestGitAuthorAbsentKeepsTheConfigHash(t *testing.T) {
	const base = `
version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	const gitKey = "git: { provider: github }"
	plain, ps := Parse([]byte(base))
	if len(ps) > 0 {
		t.Fatalf("a file with no git.author was refused: %v", ps)
	}
	data, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"author"`) {
		t.Errorf("an absent git.author is written into the resolved config's JSON, moving every repository's hash: %s", data)
	}
	opted, ps := Parse([]byte(strings.Replace(base, gitKey, "git: { provider: github, author: launcher }", 1)))
	if len(ps) > 0 {
		t.Fatalf("git.author: launcher was refused: %v", ps)
	}
	if opted.SHA256() == plain.SHA256() {
		t.Error("a file that opts in resolves to the same hash as one that does not")
	}
}

// TestValidateRefusesReservedOAuthTokenName pins the one exception to
// agent.oauth_token's rule being a shape check: the account name the
// credentials bundle reserves for the platform's own git credential may not
// be named here either, or one name would mean two credentials (B1.2,
// docs/project-claude-accounts.md). The reserved name passes every other
// check this function makes — ClaudeAccountNameRE matches it and it is inside
// MaxClaudeAccountNameLen — so the reservation is the only thing refusing it,
// and the refusal carries the same sentence ValidateClaudeAccounts answers
// claude_accounts' own keys with.
func TestValidateRefusesReservedOAuthTokenName(t *testing.T) {
	const base = `
version: 1
project: aurora
git: { provider: github }
agent:
  oauth_token: %s
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	doc := func(name string) []byte { return []byte(fmt.Sprintf(base, name)) }
	cfg, ps := Parse(doc("acme-personal"))
	if len(ps) > 0 {
		t.Fatalf("a named account was refused: %v", ps)
	}
	if got := problemsText(Validate(cfg)); got != "" {
		t.Errorf("agent.oauth_token: acme-personal was refused: %q", got)
	}
	if !ClaudeAccountNameRE.MatchString(BitbucketSecret) || len(BitbucketSecret) > MaxClaudeAccountNameLen {
		t.Fatalf("%s is refused by a shape check, so this test proves nothing about the reservation", BitbucketSecret)
	}
	cfg.Agent.OAuthToken = BitbucketSecret
	got := problemsText(Validate(cfg))
	if !strings.Contains(got, "agent.oauth_token") {
		t.Errorf("the reserved name was not refused at agent.oauth_token: %q", got)
	}
	if !strings.Contains(got, reservedAccountNameMessage) {
		t.Errorf("the refusal does not carry the one reserved-name sentence: %q", got)
	}
	if _, ps := Parse(doc(BitbucketSecret)); len(ps) == 0 {
		t.Error("Parse accepted a file whose agent.oauth_token is the reserved name")
	}
}

// TestValidateRefusesDuplicateAreaGlob is F14: two rigor.areas rules of one
// layer that carry the same glob are refused, each named at the rule that
// repeats it. Both would match exactly the same paths, and only the strictest
// matched rule decides (D20c) — so one of the two is a rule the run's record
// could never name for anything the other does not also cover, and a layer
// that says the same thing twice is a mistake the writer cannot see.
//
// The same glob in the two layers is not this and stays allowed: the area
// lists are a union rather than a replacement, and each is judged in its own
// layer (D20c).
func TestValidateRefusesDuplicateAreaGlob(t *testing.T) {
	const repo = minimalRepo + `git: { provider: github }
rigor:
  default: pro
  areas:
    - { name: payments, match: "payments/**", mode: hotfix }
    - { name: payments-api, match: "payments/api/**", mode: pro }
    - { name: payments-again, match: "payments/**", mode: poc }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, ps := Parse([]byte(repo))
	if cfg != nil {
		t.Fatalf("a file with two rules for one glob resolved: %+v", cfg.Rigor)
	}
	if len(ps) != 1 || ps[0].Path != "rigor.areas[2]" {
		t.Fatalf("problems = %s", problemsText(ps))
	}
	for _, want := range []string{`"payments/**"`, "areas[0]"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal of rigor.areas[2] does not carry %q: %q", want, ps[0].Message)
		}
	}

	// The project layer's rule for the same glob, over a repository that
	// writes its own: two layers, two rules, nothing refused.
	const layer = `version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  rigor:
    areas:
      - { name: payments, match: "payments/**", mode: hotfix }
`
	same := strings.Replace(repo, "    - { name: payments-again, match: \"payments/**\", mode: poc }\n", "", 1)
	if c, _, ps := Resolve([]byte(same), mustLayer(t, layer)); c == nil || len(ps) > 0 {
		t.Fatalf("the same glob in the two layers was refused: %s", problemsText(ps))
	}
}

// securityRepo is the head the security tests below build a file on: a
// repository that parses with no security: key at all, so each case adds the
// block it is about and nothing else.
const securityRepo = minimalRepo + `git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestSecurityKeyDecodesStrictly is SC7's own test of the block's shape
// (D1500, D1501): a repository writes a list of {name, match, category} rows
// at security.patterns, and the decoder reads it strictly — KnownFields, like
// every other key of the file — so a misspelled member of the block or of a
// row is refused where it is written rather than dropped in silence, which for
// this key would be a pattern its author believes is protecting a tree and
// that fires for nothing.
//
// It is also where the absent case is pinned. Config.SHA256 takes the hash of
// this struct marshalled, so a block that appears out of an absent key would
// move the hash of a file that did not change — the reason the field is a
// pointer with a json name, as Rigor's is.
func TestSecurityKeyDecodesStrictly(t *testing.T) {
	const block = `security:
  patterns:
    - { name: payments, match: "internal/payments/**", category: credentials }
    - { name: pricing, match: "internal/pricing/*.go", category: targets }
`
	cfg, ps := Parse([]byte(securityRepo + block))
	if len(ps) > 0 {
		t.Fatalf("the decoder refused a file it has to read: %s", problemsText(ps))
	}
	want := []sensitivity.Pattern{
		{Name: "payments", Match: "internal/payments/**", Category: sensitivity.Credentials},
		{Name: "pricing", Match: "internal/pricing/*.go", Category: sensitivity.Targets},
	}
	if cfg.Security == nil {
		t.Fatal("the security: block decoded to nothing")
	}
	if !slices.Equal(cfg.Security.Patterns, want) {
		t.Errorf("security.patterns = %+v, want %+v", cfg.Security.Patterns, want)
	}

	if none, ps := Parse([]byte(securityRepo)); len(ps) > 0 || none.Security != nil {
		t.Errorf("a file with no security: key decoded to %+v (problems %s), want no block at all", none.Security, problemsText(ps))
	}

	for _, tc := range []struct{ name, block, member string }{
		{"an unknown key of the block", "security:\n  severity: high\n", "severity"},
		{"an unknown key of a row", "security:\n  patterns:\n    - { name: p, match: \"p/**\", category: credentials, mode: pro }\n", "mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(securityRepo + tc.block))
			if len(ps) == 0 {
				t.Fatalf("the decoder accepted %s, and the block is decoded strictly", tc.member)
			}
			if !strings.Contains(problemsText(ps), tc.member) {
				t.Errorf("the refusal does not name %q: %s", tc.member, problemsText(ps))
			}
		})
	}
}

// TestSecurityKeyRefusesAUnknownCategory is D1500's closed vocabulary: the
// category is one of the seven words the record, the pull request body and
// fugaro watch print, so a value outside it is refused where it is written and
// never carried as an unknown label for a reader to interpret. The refusal
// names the whole set, because the writer's next move is to pick one of them.
func TestSecurityKeyRefusesAUnknownCategory(t *testing.T) {
	const block = "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\", category: deployment }\n"
	cfg, ps := Parse([]byte(securityRepo + block))
	if cfg != nil {
		t.Fatalf("a file with an unknown category resolved: %+v", cfg.Security)
	}
	if len(ps) != 1 || ps[0].Path != "security.patterns[0]" {
		t.Fatalf("problems = %s", problemsText(ps))
	}
	for _, want := range []string{"category must be one of", sensitivity.CategoryList()} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal of security.patterns[0] does not carry %q: %q", want, ps[0].Message)
		}
	}
	// The zero value — a row that forgot the key — is refused by the same
	// rule rather than read as a category nobody wrote.
	if _, ps := Parse([]byte(securityRepo + "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\" }\n")); len(ps) == 0 {
		t.Error("a row with no category was accepted")
	}
}

// TestSecurityKeyRefusesAMatchTheMatcherCannotRead is F21 and D1502 through
// this key: the glob rules are rigor.areas' own four checks, in the same order,
// because a row reaches the same matcher — and asking them at load is the whole
// of what keeps a pattern the matcher cannot read from becoming a rule that
// silently never fires, which is a tree its author believes is covered.
func TestSecurityKeyRefusesAMatchTheMatcherCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, match, want string }{
		{"a glob doublestar cannot read", "internal/[a-", "is not a valid glob pattern"},
		{"an absolute match", "/etc/**", "must be relative to the repository root"},
		{"a .. component", "../secrets/**", "must not contain a .. component"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := "security:\n  patterns:\n    - { name: p, match: \"" + tc.match + "\", category: credentials }\n"
			cfg, ps := Parse([]byte(securityRepo + block))
			if cfg != nil {
				t.Fatalf("the decoder read %q, and the matcher cannot use it: %+v", tc.match, cfg.Security)
			}
			if len(ps) != 1 || ps[0].Path != "security.patterns[0]" {
				t.Fatalf("problems = %s", problemsText(ps))
			}
			if !strings.Contains(ps[0].Message, tc.want) {
				t.Errorf("the refusal of %q does not carry %q: %q", tc.match, tc.want, ps[0].Message)
			}
		})
	}
}

// TestSecurityKeyRefusesAPatternThatMatchesEverything is D1503: `*`, `**` and
// an empty match are refused at write time. A row that matches every path of a
// repository is not a pattern — it is the whole-repository answer, which the
// project layer writes as always_level_3 (D1517) — and accepting `**` would
// hide that intent inside a glob no reader of the record can see through.
//
// A pattern that matches *nothing* is the other half of D1503 and stays
// accepted: a repository may write the row before it writes the tree, and
// doctor is where the row that still matches nothing is said once (D1513).
// That half is pinned here too, because the two are one rule and a later edit
// that refuses both would read as the same fix.
func TestSecurityKeyRefusesAPatternThatMatchesEverything(t *testing.T) {
	for _, match := range []string{"*", "**", ""} {
		block := "security:\n  patterns:\n    - { name: p, match: \"" + match + "\", category: credentials }\n"
		cfg, ps := Parse([]byte(securityRepo + block))
		if cfg != nil {
			t.Errorf("the decoder read the match %q, which names the whole repository: %+v", match, cfg.Security)
			continue
		}
		if len(ps) != 1 || ps[0].Path != "security.patterns[0]" {
			t.Errorf("problems for %q = %s", match, problemsText(ps))
		}
	}
	const empty = "security:\n  patterns:\n    - { name: search, match: \"internal/search/**\", category: guards }\n"
	if cfg, ps := Parse([]byte(securityRepo + empty)); cfg == nil || len(ps) > 0 {
		t.Errorf("a row whose tree does not exist yet was refused: %s", problemsText(ps))
	}
}

// TestSecurityKeyRefusesARepeatedName is D1506 read from below: a row's name
// is its identity in the union of the floor, the project's rows and the
// repository's, and the first writer's row keeps it. Two rows of one layer
// under one name therefore leave the second firing for nothing — it can never
// be named in the record — so it is refused where it is written, as two
// rigor.areas rules for one glob are (F14). The refusal is per layer: a
// repository's row for a tree the project's own list also names is refused
// here only when the project wrote the same *name*, which is the union's rule
// and not this function's to check.
func TestSecurityKeyRefusesARepeatedName(t *testing.T) {
	const block = `security:
  patterns:
    - { name: payments, match: "internal/payments/**", category: credentials }
    - { name: payments-api, match: "internal/payments/api/**", category: credentials }
    - { name: payments, match: "internal/billing/**", category: targets }
`
	cfg, ps := Parse([]byte(securityRepo + block))
	if cfg != nil {
		t.Fatalf("a file naming one row twice resolved: %+v", cfg.Security)
	}
	if len(ps) != 1 || ps[0].Path != "security.patterns[2]" {
		t.Fatalf("problems = %s", problemsText(ps))
	}
	for _, want := range []string{`"payments"`, "patterns[0]"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal of security.patterns[2] does not carry %q: %q", want, ps[0].Message)
		}
	}
}

// TestSecurityKeyIsBounded is MaxPatterns' half of the list's bounds
// (sensitivity.MaxPatterns, the number schemas/fugaro.schema.json states as
// maxItems): the rows a run is made to carry are bounded where they are
// written, because the matched subset of them is what the record, the pull
// request body and fugaro watch print.
func TestSecurityKeyIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("security:\n  patterns:\n")
	for i := range sensitivity.MaxPatterns + 1 {
		fmt.Fprintf(&b, "    - { name: p%d, match: \"tree%d/**\", category: guards }\n", i, i)
	}
	cfg, ps := Parse([]byte(securityRepo + b.String()))
	if cfg != nil {
		t.Fatalf("a list over the bound resolved: %d rows", len(cfg.Security.Patterns))
	}
	if len(ps) != 1 || ps[0].Path != "security.patterns" || !strings.Contains(ps[0].Message, fmt.Sprint(sensitivity.MaxPatterns)) {
		t.Fatalf("problems = %s", problemsText(ps))
	}
}
