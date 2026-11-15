package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// projectProfileComment is the line above the fixture's profiles block: a write
// rewrites the keys it changes and keeps every comment and every other byte of
// the file, and this is the one that says so.
const projectProfileComment = "# the two job shapes this project deploys"

// projectProfileExtra is the fixture's profiles block, in the block style every
// list this program writes is spelled in: yamledit refuses to splice into a
// flow sequence, so a fixture written the other way could not be edited at all.
const projectProfileExtra = `
` + projectProfileComment + `
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 4Gi
    timeout_class: 6h
    secrets:
      - deploy-key
  svc:
    provider: github
    cpu: 4
    memory: 8Gi
    timeout_class: 12h
`

// projectProfileLocal is the config a write left behind, read back the way the
// publisher reads it: the strict loader, which is the file's own reader.
func projectProfileLocal(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("%s does not read back: %v", path, err)
	}
	return lc
}

// TestProjectProfileAddWritesAJobProfile: S9's red step and its first form. A
// profile that does not exist yet is written from the four flags, and the file
// it lands in reads back as a config whose profile is the one that was asked
// for.
func TestProjectProfileAddWritesAJobProfile(t *testing.T) {
	_, path := modelsWriteHomeWith(t, "")
	out, _, err := execute(t, "project", "profile", "add", "standard",
		"--provider", "github", "--cpu", "2", "--memory", "4Gi", "--timeout-class", "6h")
	if err != nil {
		t.Fatalf("project profile add: %v\n%s", err, out)
	}
	pr, ok := projectProfileLocal(t, path).Profiles["standard"]
	if !ok {
		t.Fatalf("the write left no standard profile:\n%s", readFileString(t, path))
	}
	if pr.Provider != "github" || pr.CPU != 2 || pr.Memory != "4Gi" || pr.TimeoutClass != "6h" {
		t.Errorf("profiles.standard = %+v, want the four flags this command was given", pr)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to the one command:\n%s", out)
	}
}

// TestProjectProfileSetChangesOneKey: one key of one profile is rewritten, the
// other profile and the comment above the block are left exactly as they were,
// and a list key is written the same way — with the empty value removing the
// key rather than writing a list with nothing in it, which the publisher
// refuses for allow_repos and which means something different from an absent
// key for the others.
func TestProjectProfileSetChangesOneKey(t *testing.T) {
	_, path := modelsWriteHomeWith(t, projectProfileExtra)
	out, _, err := execute(t, "project", "profile", "set", "svc", "cpu", "8")
	if err != nil {
		t.Fatalf("project profile set: %v\n%s", err, out)
	}
	lc := projectProfileLocal(t, path)
	if got := lc.Profiles["svc"].CPU; got != 8 {
		t.Errorf("profiles.svc.cpu = %d, want 8", got)
	}
	if got := lc.Profiles["standard"]; got.CPU != 2 || got.TimeoutClass != "6h" {
		t.Errorf("the write changed the profile beside the one it was given: %+v", got)
	}
	if data := readFileString(t, path); !strings.Contains(data, projectProfileComment) {
		t.Errorf("the comment above the block did not survive the edit:\n%s", data)
	}

	if _, _, err := execute(t, "project", "profile", "set", "svc", "secrets", "deploy-key,backup-key"); err != nil {
		t.Fatalf("setting a list key: %v", err)
	}
	if got := projectProfileLocal(t, path).Profiles["svc"].Secrets; !slices.Equal(got, []string{"deploy-key", "backup-key"}) {
		t.Errorf("profiles.svc.secrets = %v, want the two names the flag carried", got)
	}
	if _, _, err := execute(t, "project", "profile", "set", "svc", "secrets", ""); err != nil {
		t.Fatalf("clearing a list key: %v", err)
	}
	if got := projectProfileLocal(t, path).Profiles["svc"].Secrets; len(got) != 0 {
		t.Errorf("profiles.svc.secrets = %v, want the key gone rather than a written empty list", got)
	}
}

// TestProjectProfileRmRefusesWhileAWorkflowUsesIt: the profiles block is what
// the published policy carries, and a workflow whose profile the policy does
// not define has every run refused at start (projectpolicy.ProfileFor). The
// removal is refused while a workflow this machine can read selects the
// profile, and it lands once nothing does.
func TestProjectProfileRmRefusesWhileAWorkflowUsesIt(t *testing.T) {
	home, path := modelsWriteHomeWith(t, projectProfileExtra)
	enrollRepo(t, "aurora", "owner/repo-a")
	dir := settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML("    job_profile: svc\n"))
	before := readFileString(t, path)

	_, _, err := execute(t, "project", "profile", "rm", "svc")
	if err == nil {
		t.Fatal("the removal of a profile a workflow selects was not refused")
	}
	for _, want := range []string{"owner/repo-a", "web", "svc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so nobody can find the workflow to change: %v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused removal changed the file:\n%s", after)
	}

	// The same checkout, with the workflow selecting nothing: the profile is
	// then nothing any run reads, and it goes.
	repo := settingsRepoYAML("")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(repo), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "profile", "rm", "svc"); err != nil {
		t.Fatalf("removing a profile no workflow selects: %v", err)
	}
	if _, still := projectProfileLocal(t, path).Profiles["svc"]; still {
		t.Errorf("the removal left profiles.svc behind:\n%s", readFileString(t, path))
	}
	if _, kept := projectProfileLocal(t, path).Profiles["standard"]; !kept {
		t.Errorf("the removal took another profile with it:\n%s", readFileString(t, path))
	}
}

// TestProjectProfileLsListsTheProfiles: the read. Every profile of the config
// is listed with the job it maps to, `show` prints one of them, and --json
// carries the same profiles as the text form.
func TestProjectProfileLsListsTheProfiles(t *testing.T) {
	_, _ = modelsWriteHomeWith(t, projectProfileExtra)
	out, _, err := execute(t, "project", "profile", "ls")
	if err != nil {
		t.Fatalf("project profile ls: %v\n%s", err, out)
	}
	for _, want := range []string{
		"profile standard: provider github, cpu 2, memory 4Gi, timeout 6h, secrets deploy-key",
		"profile svc: provider github, cpu 4, memory 8Gi, timeout 12h, secrets none",
		"profile svc jobs: one job per (profile, provider, account): (svc, github, <account>)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the read does not print %q:\n%s", want, out)
		}
	}

	one, _, err := execute(t, "project", "profile", "show", "svc")
	if err != nil {
		t.Fatalf("project profile show: %v\n%s", err, one)
	}
	if !strings.Contains(one, "profile svc:") || strings.Contains(one, "profile standard:") {
		t.Errorf("show svc does not print exactly that profile:\n%s", one)
	}

	raw, _, err := execute(t, "project", "profile", "ls", "--json")
	if err != nil {
		t.Fatalf("project profile ls --json: %v\n%s", err, raw)
	}
	var doc struct {
		Path     string `json:"path"`
		Profiles []struct {
			Name           string   `json:"name"`
			Provider       string   `json:"provider"`
			CPU            int      `json:"cpu"`
			Memory         string   `json:"memory"`
			TimeoutClass   string   `json:"timeout_class"`
			Secrets        []string `json:"secrets"`
			AllowRepos     []string `json:"allow_repos"`
			ClaudeAccounts []string `json:"claude_accounts"`
			Jobs           string   `json:"jobs"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("--json is not a document this read defines: %v\n%s", err, raw)
	}
	if len(doc.Profiles) != 2 || doc.Profiles[0].Name != "standard" || doc.Profiles[1].Name != "svc" {
		t.Fatalf("--json carries %+v, want both profiles in name order", doc.Profiles)
	}
	if doc.Profiles[1].CPU != 4 || doc.Profiles[1].TimeoutClass != "12h" || doc.Profiles[1].Secrets == nil {
		t.Errorf("--json's svc profile = %+v, want the values the file carries and a list rather than null", doc.Profiles[1])
	}
}

// TestProjectProfileRefusesInAnAgentSession: this file is the machine's own
// dotfile, outside every repository, so a coding agent's session writes nothing
// here whatever --yes says (SD9). The read is not refused: a session that may
// not decide may still look.
func TestProjectProfileRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			_, path := modelsWriteHomeWith(t, projectProfileExtra)
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "project", "profile", "set", "svc", "cpu", "8")
			if err == nil {
				t.Fatal("the command wrote in a coding agent's session")
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro project profile") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out, path) || !strings.Contains(out, "  + ") {
				t.Errorf("the refusal does not print the diff it refused:\n%s", out)
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", after)
			}
			if _, _, err := execute(t, "project", "profile", "ls"); err != nil {
				t.Errorf("the read was refused in a coding agent's session: %v", err)
			}
		})
	}
}

// projectProfileAccountExtra is a config whose profile admits one repository
// and whose account reaches another: a state that publishes, and one an edit
// naming the account on the profile turns into a node the publisher refuses.
const projectProfileAccountExtra = `
claude_accounts:
  team-a:
    allow_data_to:
      - acme/other
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 4Gi
    timeout_class: 6h
    allow_repos:
      - github/acme/widgets
`

// projectProfileBitbucketExtra is the same shape one rule over: a bitbucket
// profile with its own exact list, and the token's block reaching a different
// repository. Mounting the token on the profile is the edit that breaks it.
const projectProfileBitbucketExtra = `
project_secrets:
  bitbucket-token:
    allow_data_to:
      - acme/other
profiles:
  standard:
    provider: bitbucket
    cpu: 2
    memory: 4Gi
    timeout_class: 6h
    allow_repos:
      - bitbucket/acme/widgets
`

// TestProjectProfileRefusesAConfigThePublisherWouldRefuse: the door this family
// writes through holds the edited bytes to the rules the publisher applies to
// the same blocks (projectProfileCheck), so an edit whose result `fugaro update`
// would refuse to publish is refused here, with nothing written and the
// publisher's own sentence carried in the refusal.
//
// Both halves of the profiles rule are asked, and each case is an edit that
// CREATES the break — the config publishes before it, so what the refusal names
// is the change the command was asked for and not a state the fixture was in.
// The account half is the profile's list against the account's, which the node
// derives from claude_accounts; the Bitbucket half is a property of two blocks
// together — a profile mounting bitbucket-token admits only what
// bitbucket_token.allow_repos admits — and it is the half that reads the token
// block, so a guard that asked only the profile's own rules would let it
// through and the next `fugaro update` would refuse the publish.
func TestProjectProfileRefusesAConfigThePublisherWouldRefuse(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		args  []string
		want  []string
	}{
		{
			name:  "an account that does not cover the profile's repositories",
			extra: projectProfileAccountExtra,
			args:  []string{"project", "profile", "set", "standard", "claude_accounts", "team-a"},
			want:  []string{"would not publish as a policy", "team-a", "github/acme/widgets"},
		},
		{
			name:  "the Bitbucket token mounted outside its block's reach",
			extra: projectProfileBitbucketExtra,
			args:  []string{"project", "profile", "set", "standard", "secrets", "bitbucket-token"},
			want:  []string{"would not publish as a policy", "bitbucket_token.allow_repos", "bitbucket/acme/widgets"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, path := modelsWriteHomeWith(t, tc.extra)
			before := readFileString(t, path)
			out, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("the write landed a config the publisher refuses:\n%s", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("a refused write changed the file:\n%s", after)
			}
		})
	}
}

// TestProjectProfilePrintsOnlyFugaroUpdateNext: every local edit ends with one
// line, and it is `next: fugaro update` — never a list of steps (spec §1).
func TestProjectProfilePrintsOnlyFugaroUpdateNext(t *testing.T) {
	_, _ = modelsWriteHomeWith(t, projectProfileExtra)
	out, _, err := execute(t, "project", "profile", "set", "svc", "cpu", "8")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	next := []string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "next:") {
			next = append(next, line)
		}
	}
	if len(next) != 1 || next[0] != "next: fugaro update" {
		t.Errorf("next lines = %q, want exactly one `next: fugaro update`", next)
	}
}
