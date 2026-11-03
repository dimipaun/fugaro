package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
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
