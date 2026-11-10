package cli

// secrets_allow_test.go is `fugaro secrets allow|deny`'s world: the local
// config's project_secrets block, edited through the settings write path by the
// same function `secrets set`'s policy flags reach, on the fixture those flags'
// own tests use (a project config on this machine, no coding-agent marker, and
// the fake Secret Manager). No value is read and none is stored, so nothing here
// is about Secret Manager's contents — the read-back is the config's entry and
// the ALLOWED column `secrets ls` prints from it.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// TestSecretsAllowWritesThePatternList is the widening form: each PATTERN is
// added to project_secrets.NAME.allow_data_to, the entry's own repositories are
// kept, and the write says what it widens (D1165, D1171, D1552). The second run
// names a pattern the entry already holds and is the no-op every settings write
// is, hand-off line included — a `next: fugaro update` beside a `no change`
// would be a promise about a write that did not happen.
func TestSecretsAllowWritesThePatternList(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)

	out, errOut, err := execute(t, "secrets", "allow", "bitbucket-token", "acme/web")
	if err != nil {
		t.Fatalf("secrets allow bitbucket-token acme/web: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	entry := secretsPolicyEntry(t, path, "bitbucket-token")
	if want := []string{"acme/app", "acme/web"}; !slices.Equal(entry.AllowDataTo, want) {
		t.Errorf("allow_data_to = %v, want the entry's own repository and the pattern the run named", entry.AllowDataTo)
	}
	if !strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("the widening does not say what it widens:\n%s", out)
	}
	if !strings.Contains(out, "project_secrets.bitbucket-token.allow_data_to") {
		t.Errorf("the diff does not name the key it wrote:\n%s%s", out, errOut)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run does not end on the line that says what is left:\n%s", out)
	}

	out, errOut, err = execute(t, "secrets", "allow", "bitbucket-token", "acme/web")
	if err != nil {
		t.Fatalf("the second run is not the no-op it is: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if !strings.Contains(out, "no change") {
		t.Errorf("a pattern the entry already holds was not answered as no change:\n%s", out)
	}
	if strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run promises an update for a write that did not happen:\n%s", out)
	}
	if got := secretsPolicyEntry(t, path, "bitbucket-token").AllowDataTo; len(got) != 2 {
		t.Errorf("the no-op wrote the entry again: %v", got)
	}
}

// TestSecretsDenyRemovesThePattern is the other direction, and the one edit of
// this family that narrows: the pattern goes, the repositories the entry keeps
// stay, the entry itself stays, and the write prints no widening sentence
// because it loosens nothing.
func TestSecretsDenyRemovesThePattern(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)
	if _, _, err := execute(t, "secrets", "allow", "bitbucket-token", "acme/web"); err != nil {
		t.Fatalf("secrets allow bitbucket-token acme/web: %v", err)
	}

	out, errOut, err := execute(t, "secrets", "deny", "bitbucket-token", "acme/app")
	if err != nil {
		t.Fatalf("secrets deny bitbucket-token acme/app: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	entry := secretsPolicyEntry(t, path, "bitbucket-token")
	if want := []string{"acme/web"}; !slices.Equal(entry.AllowDataTo, want) {
		t.Errorf("allow_data_to = %v, want the one repository the removal did not name", entry.AllowDataTo)
	}
	if strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("a narrowing was announced as a widening:\n%s", out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run does not end on the line that says what is left:\n%s", out)
	}

	// A pattern the entry does not name is the state the run asked for, so the
	// answer is "no change" and the entry is left alone.
	out, _, err = execute(t, "secrets", "deny", "bitbucket-token", "acme/absent")
	if err != nil {
		t.Fatalf("secrets deny bitbucket-token acme/absent: %v", err)
	}
	if !strings.Contains(out, "no change") || strings.Contains(out, "next: fugaro update") {
		t.Errorf("a pattern the entry does not name was not answered as no change:\n%s", out)
	}
	if got := secretsPolicyEntry(t, path, "bitbucket-token").AllowDataTo; !slices.Equal(got, []string{"acme/web"}) {
		t.Errorf("the no-op wrote the entry: %v", got)
	}

	// The last repository goes the same way, and the entry stays: the value is
	// stored for no repository, which is the state the run asked for.
	if _, _, err := execute(t, "secrets", "deny", "bitbucket-token", "acme/web"); err != nil {
		t.Fatalf("secrets deny bitbucket-token acme/web: %v", err)
	}
	entry = secretsPolicyEntry(t, path, "bitbucket-token")
	if len(entry.AllowDataTo) != 0 {
		t.Errorf("allow_data_to = %v, want no repository left", entry.AllowDataTo)
	}
}

// TestSecretsAllowProxySetsTheFlag: the switch is the second thing this form
// writes, and it is one key per run — a run that named a pattern and --proxy
// together is refused rather than writing two keys the one document cannot
// describe. Turning the switch on is the widening the credentials service's
// hand-over is; --proxy=false is the narrowing and takes the key away rather
// than writing a second spelling of off (D493, D1275).
func TestSecretsAllowProxySetsTheFlag(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)

	out, _, err := execute(t, "secrets", "allow", "bitbucket-token", "--proxy")
	if err != nil {
		t.Fatalf("secrets allow bitbucket-token --proxy: %v", err)
	}
	if !secretsPolicyEntry(t, path, "bitbucket-token").Proxy {
		t.Errorf("the switch was not written:\n%s", out)
	}
	if !strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("turning the switch on is not announced as a widening:\n%s", out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run does not end on the line that says what is left:\n%s", out)
	}

	out, _, err = execute(t, "secrets", "allow", "bitbucket-token", "--proxy=false")
	if err != nil {
		t.Fatalf("secrets allow bitbucket-token --proxy=false: %v", err)
	}
	if secretsPolicyEntry(t, path, "bitbucket-token").Proxy {
		t.Errorf("the switch is still on:\n%s", out)
	}
	if strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("turning the switch off was announced as a widening:\n%s", out)
	}

	// The two edits are two keys: naming both is refused, with nothing written.
	before := readFileString(t, path)
	_, _, err = execute(t, "secrets", "allow", "bitbucket-token", "acme/web", "--proxy")
	if err == nil {
		t.Fatal("a pattern and the switch were written in one run")
	}
	if !strings.Contains(err.Error(), "two different edits") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the config:\n%s", after)
	}

	// A name with neither is nothing to write, and it is refused rather than
	// read as --proxy=false's edit (which would take the switch away).
	_, _, err = execute(t, "secrets", "allow", "bitbucket-token")
	if err == nil {
		t.Fatal("a run that named neither a pattern nor the switch was accepted")
	}
	if !strings.Contains(err.Error(), "neither a repository to add nor the switch") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// TestSecretsLsShowsWhatSecretsAllowWrote is the read-back through the command
// that answers the question: the ALLOWED column of `secrets ls` is
// projectSecretAllowed's answer, which is the entry's own allow_data_to, so the
// repositories a run of allow or deny leaves are the repositories the listing
// prints. The fixture is the one that seeds both halves — the entry in the
// config and the project container in Secret Manager — because the listing has
// nothing to print a row for otherwise.
func TestSecretsLsShowsWhatSecretsAllowWrote(t *testing.T) {
	secretsRmFugaro(t)
	_, _, _ = secretsRmFixture(t)
	noTerminal(t)

	out, _, err := execute(t, "secrets", "ls")
	if err != nil {
		t.Fatalf("secrets ls: %v", err)
	}
	if !strings.Contains(out, "acme/app") {
		t.Errorf("the entry's own repository is not the reach the listing prints:\n%s", out)
	}

	if _, _, err := execute(t, "secrets", "allow", "bitbucket-token", "acme/web"); err != nil {
		t.Fatalf("secrets allow bitbucket-token acme/web: %v", err)
	}
	out, _, err = execute(t, "secrets", "ls")
	if err != nil {
		t.Fatalf("secrets ls: %v", err)
	}
	if !strings.Contains(out, "acme/app,acme/web") {
		t.Errorf("the listing does not print both repositories the entry ends with:\n%s", out)
	}

	if _, _, err := execute(t, "secrets", "deny", "bitbucket-token", "acme/app"); err != nil {
		t.Fatalf("secrets deny bitbucket-token acme/app: %v", err)
	}
	out, _, err = execute(t, "secrets", "ls")
	if err != nil {
		t.Fatalf("secrets ls: %v", err)
	}
	if strings.Contains(out, "acme/app") || !strings.Contains(out, "acme/web") {
		t.Errorf("the listing still prints the repository the removal took out:\n%s", out)
	}
}

// TestSecretsAllowRefusesInAnAgentSession is the family's own fence (spec §5):
// this whole family is refused in a coding agent's session, gate or not, --yes
// included, because what a secret reaches is a decision no plan shows and no
// dry run brings back. Both directions are in the loop — a removal narrows what
// the value reaches, and a session that could make one silently would take a
// repository out of the project's runs with nobody reading it — and the refusal
// prints the form the person reached for, never its `secrets set` equivalent.
func TestSecretsAllowRefusesInAnAgentSession(t *testing.T) {
	cases := []struct {
		verb string
		args []string
		want string
	}{
		{verb: "allow", args: []string{"bitbucket-token", "acme/web"}, want: "secrets allow bitbucket-token acme/web"},
		{verb: "allow", args: []string{"bitbucket-token", "--proxy"}, want: "secrets allow bitbucket-token --proxy=true"},
		{verb: "deny", args: []string{"bitbucket-token", "acme/app"}, want: "secrets deny bitbucket-token acme/app"},
	}
	for _, tc := range cases {
		for _, marker := range agentenv.Markers {
			t.Run(tc.verb+"/"+strings.Join(tc.args[1:], "_")+"/"+marker, func(t *testing.T) {
				secretsRmFugaro(t)
				path := secretsPolicyFixture(t)
				noTerminal(t)
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				args := append([]string{"secrets", tc.verb}, tc.args...)
				args = append(args, "--yes")
				_, _, err := execute(t, args...)
				if err == nil {
					t.Fatal("the command wrote in a coding agent's session")
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
				}
				if after := readFileString(t, path); after != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", after)
				}
			})
		}
	}
}

// TestSecretsDenyReachesAFlowStyleEntry: the removal has the same second half
// the addition has (secretsPolicyAddRepos's rewrite), because the two
// directions of one field on one entry cannot come to answer differently. A
// flow sequence is the one shape yamledit's item operations refuse — and it is
// the shape docs/git-providers.md teaches the operator to write the Bitbucket
// allow-list in — so the run that removes a member of one writes the entry in
// the block style this program writes a list in and takes the member out there.
// The entry keeps its place: an empty allow_data_to is a list that admits
// nobody, not a removal of the entry (which is `secrets rm`).
func TestSecretsDenyReachesAFlowStyleEntry(t *testing.T) {
	for name, shape := range map[string]string{
		"a flow entry": "project_secrets:\n  bitbucket-token: { allow_data_to: [\"acme/app\"] }\n",
		"a flow list":  "project_secrets:\n  bitbucket-token:\n    allow_data_to: [acme/app]\n",
	} {
		t.Run(name, func(t *testing.T) {
			secretsRmFugaro(t)
			secretsFixture(t)
			projectSecretFixture(t, shape)
			path := os.Getenv("FUGARO_CONFIG")
			noTerminal(t)

			out, errOut, err := execute(t, "secrets", "deny", "bitbucket-token", "acme/app")
			if err != nil {
				t.Fatalf("secrets deny bitbucket-token acme/app on %s: %v\nstdout %s\nstderr %s", name, err, out, errOut)
			}
			if got := secretsPolicyEntry(t, path, "bitbucket-token").AllowDataTo; len(got) != 0 {
				t.Errorf("allow_data_to = %v, want no repository left", got)
			}
			after := readFileString(t, path)
			if strings.Contains(after, "[") {
				t.Errorf("the list is still written in the flow style the removal cannot splice into:\n%s", after)
			}
			for _, want := range []string{"bitbucket-token", "allow_data_to"} {
				if !strings.Contains(after, want) {
					t.Errorf("the entry lost its %s: an empty list is not a removed entry\n%s", want, after)
				}
			}
			if !strings.Contains(out, "next: fugaro update") {
				t.Errorf("the run does not end on the line that says what is left:\n%s", out)
			}

			// The removal is the whole edit: a second run has nothing to take
			// out and writes nothing, flow shape gone with the first one.
			before := readFileString(t, path)
			out, _, err = execute(t, "secrets", "deny", "bitbucket-token", "acme/app")
			if err != nil {
				t.Fatalf("the second run is not the no-op it is: %v", err)
			}
			if !strings.Contains(out, "no change") && readFileString(t, path) != before {
				t.Errorf("a repository the entry no longer names was written again:\n%s", out)
			}
		})
	}
}

// TestSecretsDenyRefusesTheShapeNoEditCanReach: an entry written as one flow
// mapping inside a flow mapping — the whole-file shape docs/git-providers.md
// teaches for the Bitbucket allow-list — is the one the editor cannot write at
// all: it refuses a member of a flow mapping, for both directions. The run says
// which shape it cannot write and what to write instead, rather than relaying
// the editor's own sentence, and writes nothing.
func TestSecretsDenyRefusesTheShapeNoEditCanReach(t *testing.T) {
	secretsRmFugaro(t)
	secretsFixture(t)
	projectSecretFixture(t, "project_secrets: { bitbucket-token: { allow_data_to: [\"acme/app\"] } }\n")
	path := os.Getenv("FUGARO_CONFIG")
	noTerminal(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "secrets", "deny", "bitbucket-token", "acme/app")
	if err == nil {
		t.Fatal("a member of a flow mapping was edited")
	}
	for _, want := range []string{"flow mapping", "block mapping", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the config:\n%s", after)
	}
}
