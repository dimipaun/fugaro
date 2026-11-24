package cli

// secrets_allow_test.go is `fugaro secrets allow|deny`'s world: the local
// config's project_secrets block, edited through the settings write path by the
// same function `secrets set`'s policy flags reach, on the fixture those flags'
// own tests use (a project config on this machine, no coding-agent marker, and
// the fake Secret Manager). No value is read and none is stored, so nothing here
// is about Secret Manager's contents — the read-back is the config's entry and
// the ALLOWED column `secrets ls` prints from it.

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// projectSecretsOf reads the record out of the fixture's database through
// internal/creds' own reader: what the service reads on a version-2 call, and
// not a copy this test decoded for itself.
func projectSecretsOf(t *testing.T, db *gcpfake.RTDB) (creds.ProjectSecrets, bool) {
	t.Helper()
	client, err := newBudgetClient(context.Background(), db.URL, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	rec, found, err := creds.ReadProjectSecrets(context.Background(), client)
	if err != nil {
		t.Fatalf("reading %s after the run: %v", creds.PathProjectSecrets(), err)
	}
	return rec, found
}

// TestSecretsAllowWritesProjectRecordAndLocalBlock: the write a version-2
// installation needs is made in two places at once — the operator-local
// `workflow_secrets` block, which version 1 and the mounts still read, and the
// project record at `creds/projectsecrets`, which is the list the credentials
// service admits a run's fetch by (IS1, IS3, D3328). Both hold the list the
// entry ends up with, read back through the two readers a real installation
// has, and the record is the one the red step finds absent.
func TestSecretsAllowWritesProjectRecordAndLocalBlock(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)

	if v := db.Value(creds.PathProjectSecrets()); v != nil {
		t.Fatalf("the fixture carries a record at %s already: the state this test is about is an installation no allow has written", creds.PathProjectSecrets())
	}
	out, errOut, err := execute(t, "secrets", "allow", "deploy-token", "acme/web")
	if err != nil {
		t.Fatalf("secrets allow deploy-token acme/web: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := secretsPolicyWorkflowEntry(t, path, "deploy-token").AllowDataTo; !slices.Equal(got, []string{"acme/app", "acme/web"}) {
		t.Errorf("workflow_secrets.deploy-token.allow_data_to = %v, want the entry's own repository and the one the run named", got)
	}
	rec, found := projectSecretsOf(t, db)
	if !found {
		t.Fatalf("%s was not written: a version-2 fetch has no list to be admitted by", creds.PathProjectSecrets())
	}
	rule, held := rec.Rule("deploy-token")
	if !held {
		t.Fatalf("the record holds %v; want an entry for deploy-token", rec.Secrets)
	}
	if !slices.Equal(rule.Repos, []string{"acme/app", "acme/web"}) {
		t.Errorf("the record's deploy-token = %v, want the list the local entry ends with", rule.Repos)
	}
	if !strings.Contains(out+errOut, creds.PathProjectSecrets()) {
		t.Errorf("no line names the record the run wrote:\n%s%s", out, errOut)
	}
}

// TestSecretsDenyNarrows: a removal narrows both halves — the record's list as
// well as the local block — and the last repository goes the same way as the
// local entry does: the ENTRY STAYS with an empty list rather than vanishing,
// which is the state update must not re-seed from the operator's local block
// (F23, G8).
func TestSecretsDenyNarrows(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	if _, _, err := execute(t, "secrets", "allow", "deploy-token", "acme/web"); err != nil {
		t.Fatalf("secrets allow deploy-token acme/web: %v", err)
	}

	out, errOut, err := execute(t, "secrets", "deny", "deploy-token", "acme/app")
	if err != nil {
		t.Fatalf("secrets deny deploy-token acme/app: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := secretsPolicyWorkflowEntry(t, path, "deploy-token").AllowDataTo; !slices.Equal(got, []string{"acme/web"}) {
		t.Errorf("workflow_secrets.deploy-token.allow_data_to = %v, want the one repository the removal did not name", got)
	}
	rec, _ := projectSecretsOf(t, db)
	if rule, _ := rec.Rule("deploy-token"); !slices.Equal(rule.Repos, []string{"acme/web"}) {
		t.Errorf("the record's deploy-token = %v, want the removal to have narrowed it too", rule.Repos)
	}

	if _, _, err := execute(t, "secrets", "deny", "deploy-token", "acme/web"); err != nil {
		t.Fatalf("secrets deny deploy-token acme/web: %v", err)
	}
	rec, _ = projectSecretsOf(t, db)
	rule, held := rec.Rule("deploy-token")
	if !held {
		t.Fatalf("the record dropped the entry a deny emptied: %v — update seeds a name only when the record holds no entry for it, so an absent one is re-seeded and the deny undone (F23)", rec.Secrets)
	}
	if len(rule.Repos) != 0 || rule.AllRepos {
		t.Errorf("the record's deploy-token = %+v, want an entry with an empty list that admits nobody", rule)
	}
}

// TestSecretsAllowAllReposRecordOnly: `--all-repos` is the one reach the local
// block cannot spell — its owner-literal rule refuses a wildcard owner, and O2
// keeps that rule (answered 2026-11-23: every repository of the project, any
// owner) — so it is a member of the record alone and writes nothing local. The
// local file is byte-identical afterwards.
func TestSecretsAllowAllReposRecordOnly(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	out, errOut, err := execute(t, "secrets", "allow", "deploy-token", "--all-repos")
	if err != nil {
		t.Fatalf("secrets allow deploy-token --all-repos: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("--all-repos wrote the local block, whose owner-literal rule has no spelling for it:\n%s", after)
	}
	rec, found := projectSecretsOf(t, db)
	if !found {
		t.Fatalf("--all-repos wrote no record at %s", creds.PathProjectSecrets())
	}
	rule, held := rec.Rule("deploy-token")
	if !held || !rule.AllRepos {
		t.Fatalf("the record's deploy-token = %+v (held=%v), want all_repos", rule, held)
	}
	if !rec.Allows("deploy-token", "someone-else/app") {
		t.Error("the record does not admit another owner's repository: that is the reach --all-repos was answered to give (O2)")
	}
	if !strings.Contains(out+errOut, creds.PathProjectSecrets()) {
		t.Errorf("no line names the record the run wrote:\n%s%s", out, errOut)
	}

	// A list write is the record's other reach, and it is the one --all-repos
	// leaves alone: the switch and the list are two members of one entry. So
	// this leg writes a pattern first, which the local block keeps, and reads
	// the two back.
	if _, _, err := execute(t, "secrets", "allow", "deploy-token", "acme/web"); err != nil {
		t.Fatalf("secrets allow deploy-token acme/web: %v", err)
	}
	before = readFileString(t, path)

	// --all-repos=false takes the switch away and not the list, and it too
	// writes nothing local: the switch is the record's own member, not the
	// block's. The line it prints is the reach the record is left with — the
	// entry's repositories are still there and still admitted by Allows, so a
	// line naming none of them would be a reach no reader of the record would
	// find.
	out, errOut, err = execute(t, "secrets", "allow", "deploy-token", "--all-repos=false")
	if err != nil {
		t.Fatalf("secrets allow deploy-token --all-repos=false: %v", err)
	}
	rec, _ = projectSecretsOf(t, db)
	rule, _ = rec.Rule("deploy-token")
	if rule.AllRepos {
		t.Errorf("--all-repos=false left the switch on: %+v", rule)
	}
	if !slices.Equal(rule.Repos, []string{"acme/app", "acme/web"}) {
		t.Errorf("the record's deploy-token = %v, want the list the write before it left", rule.Repos)
	}
	if !rec.Allows("deploy-token", "acme/app") {
		t.Error("the record admits no repository after --all-repos=false, and its list names two")
	}
	if !strings.Contains(out+errOut, "acme/app, acme/web") {
		t.Errorf("the line the write ends on does not name the repositories the record still admits:\n%s%s", out, errOut)
	}
	if strings.Contains(out+errOut, "no repository") {
		t.Errorf("the line says the name reaches no repository, and the record admits two:\n%s%s", out, errOut)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("--all-repos=false wrote the local block:\n%s", after)
	}
}

// TestSecretsAllowAllReposRefusedForAReservedName: the record's names are
// workflow secrets, and a reserved name's reach is its own
// project_secrets.allow_data_to list — the block's, not the record's. A switch
// written for one would be a member nothing reads, so it is refused, naming the
// block the name's reach does live in, and neither the record nor the file
// moves.
func TestSecretsAllowAllReposRefusedForAReservedName(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "secrets", "allow", "bitbucket-token", "--all-repos")
	if err == nil {
		t.Fatal("--all-repos wrote a member for a reserved name, whose reach is its own allow_data_to list")
	}
	if !strings.Contains(err.Error(), secretsBlockProject) {
		t.Errorf("the refusal does not name the block the name's reach lives in: %v", err)
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the config:\n%s", after)
	}
	if v := db.Value(creds.PathProjectSecrets()); v != nil {
		t.Errorf("the refusal wrote the record: %v", v)
	}
}

// TestLocalBlockStillRefusesWildcardOwner: the record's all_repos member does
// not soften the local block's own rule — `secrets allow NAME "*"` and a
// wildcard owner are still refused by the loader, and refused BEFORE anything
// is written, so neither the file nor the record moves. The cross-owner reach
// is the record's own member and only that (O2).
func TestLocalBlockStillRefusesWildcardOwner(t *testing.T) {
	for _, pattern := range []string{"*", "*/app"} {
		t.Run(pattern, func(t *testing.T) {
			path, db := secretsWorkflowRecordFixture(t)
			noTerminal(t)
			before := readFileString(t, path)

			_, _, err := execute(t, "secrets", "allow", "deploy-token", pattern)
			if err == nil {
				t.Fatalf("secrets allow deploy-token %q wrote a pattern the local block's owner-literal rule refuses", pattern)
			}
			for _, want := range []string{"workflow_secrets.deploy-token.allow_data_to"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the key it would have written (%q):\n%v", want, err)
				}
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the refusal wrote the local block:\n%s", after)
			}
			if v := db.Value(creds.PathProjectSecrets()); v != nil {
				t.Errorf("the refusal wrote the record: %v", v)
			}
		})
	}
}

// TestSecretsAllowLosesNoConcurrentWrite: the record is read and written
// through the compare-and-swap, so a name another operator allow-listed is
// still there after this run's write. A whole-node replacement would drop it,
// and the operator would never see it happen.
func TestSecretsAllowLosesNoConcurrentWrite(t *testing.T) {
	_, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	db.Set(creds.PathProjectSecrets(), map[string]any{
		"version": creds.ProjectSecretsVersion,
		"secrets": map[string]any{
			"other-token": map[string]any{"repos": []string{"acme/other"}, "all_repos": false},
		},
	})

	if _, _, err := execute(t, "secrets", "allow", "deploy-token", "acme/web"); err != nil {
		t.Fatalf("secrets allow deploy-token acme/web: %v", err)
	}
	rec, found := projectSecretsOf(t, db)
	if !found {
		t.Fatalf("%s was not written", creds.PathProjectSecrets())
	}
	if rule, held := rec.Rule("other-token"); !held || !slices.Equal(rule.Repos, []string{"acme/other"}) {
		t.Fatalf("the other operator's entry was lost: %v", rec.Secrets)
	}
	if rule, held := rec.Rule("deploy-token"); !held || !slices.Equal(rule.Repos, []string{"acme/app", "acme/web"}) {
		t.Fatalf("deploy-token = %+v (held=%v), want [acme/app acme/web]", rule, held)
	}
}

// TestSecretsAllowRefusedInAgentSession: the family's fence holds for the
// record too (D1552). A coding agent's session may not decide which
// repositories fetch a project secret — what a secret reaches is a decision no
// plan shows — and the refusal comes before either write, so the file and the
// record are untouched and the command to run at a terminal is printed.
func TestSecretsAllowRefusedInAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			path, db := secretsWorkflowRecordFixture(t)
			noTerminal(t)
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			_, _, err := execute(t, "secrets", "allow", "deploy-token", "acme/web")
			if err == nil {
				t.Fatal("the record was written in a coding agent's session")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			for _, want := range []string{marker, "secrets allow deploy-token acme/web", "nothing was written"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the session changed the local block:\n%s", after)
			}
			if v := db.Value(creds.PathProjectSecrets()); v != nil {
				t.Errorf("the session wrote the record: %v", v)
			}
		})
	}
}

// TestSecretsAllowAllReposRefusedInAgentSession: `--all-repos` is the reach
// that admits every repository of the project, so it meets the family's fence
// on its own path too (D1552) — the branch returns before the local file is
// read, so a session leaves the file AND the record untouched, and the refusal
// prints the command to run at a terminal. The switch is the record's own
// member, so the command is the one that writes it, not the local key a
// pattern write names.
func TestSecretsAllowAllReposRefusedInAgentSession(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	before := readFileString(t, path)
	t.Setenv(accountsAddAgent, "1")

	_, _, err := execute(t, "secrets", "allow", "deploy-token", "--all-repos")
	if err == nil {
		t.Fatal("--all-repos widened the record in a coding agent's session")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{accountsAddAgent, creds.PathProjectSecrets(), "secrets allow deploy-token --all-repos=true", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the session changed the local block:\n%s", after)
	}
	if v := db.Value(creds.PathProjectSecrets()); v != nil {
		t.Errorf("the session wrote the record: %v", v)
	}
}

// TestSecretsAllowAllReposRefusesAPatternBesideIt: `--all-repos` admits every
// repository of the project and a PATTERN names one, so the two are two edits
// and the run is refused before either is made. The refusal matters: read as
// one run, a PATTERN beside the switch would be dropped, and the operator would
// be told their repository was admitted while the record's whole-project member
// was the only thing written.
func TestSecretsAllowAllReposRefusesAPatternBesideIt(t *testing.T) {
	path, db := secretsWorkflowRecordFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	for _, args := range [][]string{
		{"secrets", "allow", "deploy-token", "acme/web", "--all-repos"},
		{"secrets", "allow", "deploy-token", "--all-repos", "--proxy"},
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v was accepted: two edits of one entry were made in one run", args)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want a user error: %v", args, ExitCode(err), err)
		}
		if after := readFileString(t, path); after != before {
			t.Errorf("%v changed the local block:\n%s", args, after)
		}
		if v := db.Value(creds.PathProjectSecrets()); v != nil {
			t.Errorf("%v wrote the record: %v", args, v)
		}
	}
}

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
	if !secretsPolicyEntry(t, path, "bitbucket-token").Proxying() {
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
	if secretsPolicyEntry(t, path, "bitbucket-token").ProxyStated() {
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
	if !strings.Contains(err.Error(), "neither a repository to add") {
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
			// The entry's own bytes: the fixture's other blocks are flow by
			// construction, so the shape is read from this block alone.
			after := readFileString(t, path)
			entry := after[strings.Index(after, "project_secrets:"):]
			if strings.Contains(entry, "[") {
				t.Errorf("the list is still written in the flow style the removal cannot splice into:\n%s", after)
			}
			for _, want := range []string{"bitbucket-token", "allow_data_to"} {
				if !strings.Contains(entry, want) {
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

// TestSecretsDenyRefusesTheShapeNoEditCanReach: an entry written inside a flow
// mapping — the whole-file shape docs/git-providers.md teaches for the Bitbucket
// allow-list — is the one the editor cannot write at all, in either direction:
// it refuses a member of a flow mapping. The run says which entry could not be
// written and what to write instead (modelsSpliceRefusal, the one place a file
// the editor cannot splice is explained), rather than relaying the editor's own
// sentence, and writes nothing.
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
	for _, want := range []string{"project_secrets.bitbucket-token cannot be changed by one edit", "block style", "wrote nothing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the config:\n%s", after)
	}
}

// TestSecretsAllowRefusesTheFlowBlockThatCannotHoldTheEntry: the same obstacle
// with the entry absent — an empty flow mapping, and the whole-file line
// docs/git-providers.md teaches with a name the block does not hold. The rewrite
// then has nothing to write again (an absent key is no error to take away), the
// entry is not among the values it writes, and the refusal comes from the write
// that follows; it is the family's sentence all the same, because the obstacle
// is the file's shape and no run of this program reaches a member of a flow
// mapping. Nothing is written.
func TestSecretsAllowRefusesTheFlowBlockThatCannotHoldTheEntry(t *testing.T) {
	cases := map[string]struct{ shape, name string }{
		"an empty flow block":                {"project_secrets: {}\n", "bitbucket-token"},
		"a flow block holding another entry": {"project_secrets: { bitbucket-token: { allow_data_to: [\"acme/app\"] } }\n", "claude-oauth-token"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			secretsRmFugaro(t)
			secretsFixture(t)
			projectSecretFixture(t, tc.shape)
			path := os.Getenv("FUGARO_CONFIG")
			noTerminal(t)
			before := readFileString(t, path)

			_, _, err := execute(t, "secrets", "allow", tc.name, "acme/web")
			if err == nil {
				t.Fatal("an entry inside a flow mapping was written")
			}
			for _, want := range []string{tc.name + " cannot be changed by one edit", "block style", "wrote nothing"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the refusal wrote the config:\n%s", after)
			}
		})
	}
}
