package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The workspace writes' third half: the address a member's pin may carry (R3),
// the region and the repositories a member entry carries (R6, D2992), and the
// owner's shared list (spec §5, D1298). The fixture is workspace_test.go's, so
// these tests start from the same config home the rest of the family's do.

// workspaceShareArgsNoYes is the whole of one `workspace share` without its
// --yes: the invocation a script drives and the one the test that is about the
// write asking nothing runs.
func workspaceShareArgsNoYes(extra ...string) []string {
	return append([]string{"workspace", "share", "anthropic-api-key",
		"--workspace", "workspace-alpha", "--projects", "aurora"}, extra...)
}

// TestAPinWithoutAnAddressIsValid is R3's first half: the address is the pin's
// fifth and optional value, and a pin without one is a whole pin — a member
// that takes no workspace credential, which is the state every member was in
// before the address existed. The write states the whole pin, so re-pinning
// without the flag is also how a project stops taking one: the key goes rather
// than being written empty, which is what the loader reads an absent key as.
func TestAPinWithoutAnAddressIsValid(t *testing.T) {
	_, path := workspaceWriteHome(t)

	out, _, err := execute(t, workspaceAddArgs()...)
	if err != nil {
		t.Fatalf("workspace add without --service-address: %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, path))
	}
	if lc.WorkspacePin.ServiceAddress != "" {
		t.Errorf("the pin carries service address %q, and none was given", lc.WorkspacePin.ServiceAddress)
	}
	if strings.Contains(readFileString(t, path), "service_address") {
		t.Errorf("the write spelled an absent address as a key:\n%s", readFileString(t, path))
	}
	if !strings.Contains(out, "take no workspace credential (no service address)") {
		t.Errorf("the sentence does not say the member takes no workspace credential:\n%s", out)
	}
	// The read says the same thing the write did, in the read's own words.
	shown, _, err := execute(t, "workspace", "show", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace show: %v\n%s", err, shown)
	}
	if !strings.Contains(shown, "none — this project takes no workspace credential") {
		t.Errorf("the read does not report the absent address:\n%s", shown)
	}

	// And an address written and then not named is gone: the pin the
	// invocation states is the whole pin.
	if _, _, err := execute(t, workspaceAddArgs("--service-address", "https://workspace.example.com")...); err != nil {
		t.Fatalf("workspace add with --service-address: %v", err)
	}
	if again, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add with the address dropped: %v", err)
	} else if !strings.Contains(again, "no change") && strings.Contains(readFileString(t, path), "service_address") {
		t.Errorf("re-pinning without an address left one in the file:\n%s", readFileString(t, path))
	}
}

// TestAPinAddressMustBeHTTPS is R3's second half: the address is where this
// project's runs send a signed POST carrying a member-bound assertion, so it is
// https with a host and nothing else — no userinfo, no query, no fragment. Each
// of the five refused shapes is refused here and refused again at the load, and
// a refusal writes nothing.
func TestAPinAddressMustBeHTTPS(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)

	for _, address := range []string{
		"http://workspace.example.com",
		"workspace.example.com",
		"https://",
		"https://person-1@workspace.example.com",
		"https://workspace.example.com/?project=aurora",
		"https://workspace.example.com/#top",
	} {
		out, _, err := execute(t, workspaceAddArgs("--service-address", address)...)
		if err == nil {
			t.Fatalf("--service-address %s was accepted:\n%s", address, out)
		}
		if !strings.Contains(err.Error(), "must be an https URL with a host and no userinfo, query or fragment") {
			t.Errorf("--service-address %s: the refusal is not the address's: %v", address, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused address wrote the file:\n%s", after)
	}

	// The accepted one is written under the pin, and the loader reads it back.
	out, _, err := execute(t, workspaceAddArgs("--service-address", "https://workspace.example.com")...)
	if err != nil {
		t.Fatalf("workspace add with an https address: %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, path))
	}
	if lc.WorkspacePin.ServiceAddress != "https://workspace.example.com" {
		t.Errorf("the pin's address is %q, want the one the flag gave", lc.WorkspacePin.ServiceAddress)
	}
	if !strings.Contains(out, "call the workspace's credentials service at https://workspace.example.com") {
		t.Errorf("the sentence does not say where the calls go:\n%s", out)
	}
}

// TestProjectAddTakesRegionAndRepositories is R6 and D2992: a member row carries
// where its database lives, and every job service account carries the
// repositories it is a job of — concrete names, one for a repository's own job
// and several for a shared project job, never a pattern. The two halves are
// written as one item per account, so a table read back names each account with
// its own repositories rather than a list beside the accounts.
func TestProjectAddTakesRegionAndRepositories(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	decl := []string{
		"--job-service-account", "marker-job@aurora-gcp.iam.gserviceaccount.com",
		"--job-repos", "marker-job@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora",
		"--job-service-account", "marker-shared@aurora-gcp.iam.gserviceaccount.com",
		"--job-repos", "marker-shared@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora",
		"--job-repos", "marker-shared@aurora-gcp.iam.gserviceaccount.com=marker-org/borealis",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v1"}
	_, out, chainErr := workspaceProjectAddWholeRun(t, append(slices.Clone(decl), "--database-region", "europe-west1")...)
	after := readFileString(t, owner)
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("%v\n%s", err, after)
	}
	got := (*lc.WorkspaceMembers)["aurora"]
	want := []localcfg.WorkspaceJobAccount{
		{Account: "marker-job@aurora-gcp.iam.gserviceaccount.com", Repos: []string{"marker-org/aurora"}},
		{Account: "marker-shared@aurora-gcp.iam.gserviceaccount.com", Repos: []string{"marker-org/aurora", "marker-org/borealis"}},
	}
	if len(got.JobAccounts) != len(want) {
		t.Fatalf("the entry carries %d job accounts, want %d:\n%s", len(got.JobAccounts), len(want), after)
	}
	for i, a := range got.JobAccounts {
		if !workspaceSameJobAccount(a, want[i]) {
			t.Errorf("job account %d is %+v, want %+v", i, a, want[i])
		}
	}
	if got.DatabaseRegion != "europe-west1" {
		t.Errorf("database region = %q, want the one the flag gave", got.DatabaseRegion)
	}
	// The read reports both halves, so a table carried in the file is a table a
	// person can read out of `workspace show` without opening it.
	shown, _, err := execute(t, "workspace", "show", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace show: %v\n%s", err, shown)
	}
	for _, wantLine := range []string{
		"marker-shared@aurora-gcp.iam.gserviceaccount.com",
		"marker-org/borealis",
		"database region: europe-west1",
	} {
		if !strings.Contains(shown, wantLine) {
			t.Errorf("the read does not carry %q:\n%s", wantLine, shown)
		}
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)

	// A region is optional on the command line, and this declaration names none:
	// where the member's database is comes from the member's own budget.rtdb_url,
	// which this fixture's member config does not write. So the region the entry
	// already records stands — the declaration is not a statement that the member
	// moved to the default location, and reading it as one would move where the
	// workspace's service reads that member's database (LP9, D3084). What this
	// reads is the file: the chain behind a replacement is this fixture's own
	// answer, which workspaceAssertSavedNotLive holds beside it.
	if _, _, err := workspaceProjectAddWholeRun(t, decl...); err != nil {
		t.Fatalf("workspace project add: %v", err)
	}
	lc, err = localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	if region := (*lc.WorkspaceMembers)["aurora"].DatabaseRegion; region != "europe-west1" {
		t.Errorf("database region = %q, want europe-west1, the region the entry already recorded", region)
	}
}

// TestProjectAddRefusesAnAccountWithNoRepositories and the pair below it are
// D2992's refusals: the account and its repositories are one statement, so an
// account the declaration names no repository for, a repository that is a
// pattern, and a repository written for an account the entry does not carry are
// each refused by name before anything is written.
func TestProjectAddRefusesAnAccountWithNoRepositories(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	for _, tc := range []struct {
		name string
		args []string
		want string
		// fakes: the declaration reaches the workspace's roster before this
		// refusal is made. The account-and-repository checks are the command's
		// own, so they are made before anything is read; the pattern is refused
		// by the config's strict loader, which reads the entry back once it is
		// declared, and the roster read comes first on the way there.
		fakes bool
	}{
		{name: "an account with no repositories",
			args: []string{"--job-service-account", "marker-bare@aurora-gcp.iam.gserviceaccount.com"},
			want: "no --job-repos is given for marker-bare@aurora-gcp.iam.gserviceaccount.com"},
		{name: "a pattern",
			args: []string{"--job-repos", "marker-job@aurora-gcp.iam.gserviceaccount.com=marker-org/*"},
			want: "must look like owner/name", fakes: true},
		{name: "another account's repository",
			args: []string{"--job-repos", "marker-other@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora"},
			want: "not one of this declaration's job service accounts"},
		{name: "a bare repository",
			args: []string{"--job-repos", "marker-org/aurora"},
			want: "must be <account>=<owner/name>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out string
			var err error
			if tc.fakes {
				_, out, err = workspaceProjectAddRun(t, tc.args...)
			} else {
				out, _, err = execute(t, append(workspaceProjectAddArgs(), tc.args...)...)
			}
			if err == nil {
				t.Fatalf("%s was accepted:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v, want a refusal naming %q", tc.name, err, tc.want)
			}
		})
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused declaration wrote the file:\n%s", after)
	}
}

// TestShareRefusesAProjectThatIsNotAMember is F13's refusal, and it is the red
// step the task was written from: the command did not exist, so the run failed
// with an unknown command. What it is for is the rule the published record is
// built on — a shared entry may name only projects the member table declares,
// because the workspace's own update builds the record from that table and an
// entry naming a project it does not hold is one the next update refuses. The
// refusal names the project that is wrong and where the table is read, and it
// writes nothing.
func TestShareRefusesAProjectThatIsNotAMember(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	before := readFileString(t, owner)

	out, _, err := execute(t, workspaceShareArgsNoYes("--projects", "borealis")...)
	if err == nil {
		t.Fatalf("sharing to a project the table does not declare was accepted:\n%s", out)
	}
	for _, want := range []string{"declares no member borealis", "fugaro workspace show workspace-alpha", "fugaro workspace project add borealis --workspace workspace-alpha"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// A name that is not a project name at all is refused by the grammar rather
	// than by the table, and both refusals are this command's: an unknown
	// command answers with cobra's usage error, which names neither the table
	// nor the grammar.
	if _, _, err := execute(t, workspaceShareArgsNoYes("--projects", "Borealis")...); err == nil ||
		!strings.Contains(err.Error(), "is not a project name") {
		t.Errorf("a project name outside the grammar: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused share wrote the file:\n%s", after)
	}
	// And the same invocation against a project the table does declare is
	// accepted and writes its entry, so the refusal above is the table's and
	// not this command's own argument check.
	if out, _, _ := execute(t, workspaceShareArgsNoYes()...); !strings.Contains(out, "offer anthropic-api-key") {
		t.Errorf("the accepted share did not reach its own write:\n%s", out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, owner))
	}
	if _, ok := workspaceShared(lc)["anthropic-api-key"]; !ok {
		t.Errorf("the accepted share wrote no entry:\n%s", readFileString(t, owner))
	}
}

// TestShareAsksNothing is D1540 on the shared list: `share` and `unshare` write
// and un-write the list with neither --yes nor a terminal, and what stands where
// a y/N was is the sentence naming the entry. Both are the owner's own half, so
// the chain behind them is the workspace's own project and the record published
// from it — which this machine, whose workspace has never been applied, has not
// run, and the write says so in D1563's words rather than asking anything.
func TestShareAsksNothing(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)

	out, _, chainErr := execute(t, workspaceShareArgsNoYes()...)
	assertNoPrompt(t, out)
	workspaceAssertSavedNotLive(t, "workspace share", out, chainErr)
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, owner))
	}
	entry, ok := workspaceShared(lc)["anthropic-api-key"]
	if !ok {
		t.Fatalf("the shared entry was not written:\n%s", readFileString(t, owner))
	}
	if entry.Kind != localcfg.WorkspaceSharedModel || !slices.Equal(entry.Projects, []string{"aurora"}) {
		t.Errorf("the entry is %+v, want the model credential offered to aurora", entry)
	}

	// The forge half names the repositories it reaches, and the kind is derived
	// from the name rather than stated: there is no flag that could say one
	// thing while the name beside it says another.
	out, _, chainErr = execute(t, "workspace", "share", "bitbucket-token",
		"--workspace", "workspace-alpha", "--projects", "aurora", "--covers", "marker-org/aurora")
	assertNoPrompt(t, out)
	workspaceAssertSavedNotLive(t, "workspace share", out, chainErr)
	lc, err = localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok = workspaceShared(lc)["bitbucket-token"]
	if !ok || entry.Kind != localcfg.WorkspaceSharedBitbucket || !slices.Equal(entry.Covers, []string{"marker-org/aurora"}) {
		t.Errorf("the forge entry is %+v (present %v), want the Bitbucket token covering marker-org/aurora", entry, ok)
	}

	out, _, chainErr = execute(t, "workspace", "unshare", "anthropic-api-key", "--workspace", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace unshare: %v\n%s", chainErr, out)
	}
	assertNoPrompt(t, out)
	workspaceAssertSavedNotLive(t, "workspace unshare", out, chainErr)
	lc, err = localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := workspaceShared(lc)["anthropic-api-key"]; ok {
		t.Errorf("the entry survived the unshare:\n%s", readFileString(t, owner))
	}
}

// TestShareTakesTheKindFromTheCredentialName is the reason there is no --kind:
// the name is the bundle's own key, so what the entry is for is derived from it
// and only the three kinds a member's run can be served are accepted at all.
// A name the workspace's bundle cannot hold, a model entry given coverage and a
// forge entry given none are each refused with nothing written.
func TestShareTakesTheKindFromTheCredentialName(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	before := readFileString(t, owner)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"an unknown credential", []string{"workspace", "share", "not-a-credential", "--workspace", "workspace-alpha", "--projects", "aurora"}, "is not a credential the workspace can offer its members"},
		{"a coverage on a model entry", workspaceShareArgsNoYes("--covers", "marker-org/aurora"), "cannot be given for anthropic-api-key"},
		{"a forge entry with no coverage", []string{"workspace", "share", "bitbucket-token", "--workspace", "workspace-alpha", "--projects", "aurora"}, "--covers is required for bitbucket-token"},
		{"no projects", []string{"workspace", "share", "anthropic-api-key", "--workspace", "workspace-alpha"}, "--projects is required"},
		{"no workspace", []string{"workspace", "share", "anthropic-api-key", "--projects", "aurora"}, "--workspace is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("%s was accepted:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v, want a refusal naming %q", tc.name, err, tc.want)
			}
		})
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused share wrote the file:\n%s", after)
	}

	// An unshare of an entry the list does not hold is a refusal and not an
	// empty edit, and the list it was asked about is the reason it names.
	if _, _, err := execute(t, "workspace", "unshare", "anthropic-api-key", "--workspace", "workspace-alpha"); err == nil ||
		!strings.Contains(err.Error(), "shares no credential anthropic-api-key") {
		t.Errorf("removing an entry the list does not hold: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused unshare wrote the file:\n%s", after)
	}
}

// TestShareAcceptsAProviderKey is WC1's acceptance and its red step: a provider
// key is shared like any model entry (D2978, F7). The workspace's bundle holds a
// provider's key in two shapes, and both are shareable and both land as a model
// entry — a name, the member projects and no coverage, exactly as a Claude
// account's entry does, because a model call names no repository:
//
//   - the project's own, whose document key is config.ProviderKeySecret and
//     which credsvc.CredentialKey reads as credsvc.KindProvider;
//   - one repository's named entry for a provider
//     (repo-credential--<slug>--<provider>), which that same reader answers as
//     credsvc.KindRepoCredential and NOT as KindProvider, because its document
//     key carries the repository the copy was made for. That is U7's answer, and
//     this test is where it is settled: the name is the document key, the kind
//     the family's own, and the provider the credential half of it.
//
// What a call through such an entry is served is the selection's rule and not
// this command's (TestAClaudeModelThroughASharedProviderKeyWaits, in credsvc).
func TestShareAcceptsAProviderKey(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	slug, err := task.Slug("github", "marker-org/aurora")
	if err != nil {
		t.Fatalf("task.Slug: %v", err)
	}
	named, err := credsvc.RepoCredentialKey(slug, "openrouter")
	if err != nil {
		t.Fatalf("credsvc.RepoCredentialKey: %v", err)
	}
	if _, kind, tier, ok := credsvc.CredentialKey(named); !ok || kind != credsvc.KindRepoCredential || tier != creds.TierC {
		t.Fatalf("CredentialKey(%s) = (%v, %v, %v), want a named provider entry at tier C: this test's own name is not one the loader reads as the family's", named, kind, tier, ok)
	}
	if _, kind, tier, ok := credsvc.CredentialKey(config.ProviderKeySecret); !ok || kind != credsvc.KindProvider || tier != creds.TierC {
		t.Fatalf("CredentialKey(%s) = (%v, %v, %v), want the project's provider key at tier C", config.ProviderKeySecret, kind, tier, ok)
	}

	for _, credential := range []string{config.ProviderKeySecret, named} {
		t.Run(credential, func(t *testing.T) {
			out, _, chainErr := execute(t, "workspace", "share", credential,
				"--workspace", "workspace-alpha", "--projects", "aurora")
			assertNoPrompt(t, out)
			workspaceAssertSavedNotLive(t, "workspace share", out, chainErr)
			if !strings.Contains(out, "offer "+credential) {
				t.Errorf("the share of %s does not read as an offer:\n%s", credential, out)
			}
			lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
			if err != nil {
				t.Fatalf("%v\n%s", err, readFileString(t, owner))
			}
			entry, ok := workspaceShared(lc)[credential]
			if !ok {
				t.Fatalf("the share of %s wrote no entry:\n%s", credential, readFileString(t, owner))
			}
			if entry.Kind != localcfg.WorkspaceSharedModel {
				t.Errorf("the entry for %s is kind %q, want %q: a provider key is offered as a model entry, which is what a call spent through one is", credential, entry.Kind, localcfg.WorkspaceSharedModel)
			}
			if !slices.Equal(entry.Projects, []string{"aurora"}) {
				t.Errorf("the entry for %s is offered to %v, want aurora", credential, entry.Projects)
			}
			if len(entry.Covers) != 0 {
				t.Errorf("the entry for %s covers %v, and a model entry reaches no repository", credential, entry.Covers)
			}

			// A coverage is refused for it exactly as it is for a Claude
			// account: the repositories a model call may be made for are the
			// member's own policy's business.
			if _, _, err := execute(t, "workspace", "share", credential, "--workspace", "workspace-alpha",
				"--projects", "aurora", "--covers", "marker-org/aurora"); err == nil ||
				!strings.Contains(err.Error(), "cannot be given for "+credential) {
				t.Errorf("a coverage on %s: %v, want the model entry's own refusal", credential, err)
			}
		})
	}

	// And the node the owner's update publishes carries both as model entries,
	// which is what the workspace's service reads the pool out of.
	published, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, owner))
	}
	rec := &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "aurora"}}}
	for _, credential := range []string{config.ProviderKeySecret, named} {
		entry, err := workspaceSharedEntry(rec, credential, workspaceShared(published)[credential])
		if err != nil {
			t.Fatalf("workspaceSharedEntry(%s): %v", credential, err)
		}
		if entry.Name != credential || entry.Kind != projectpolicy.SharedKindModel || len(entry.Covers) != 0 {
			t.Errorf("the published node's entry is %+v, want a model entry named %s: a provider key reaches a member's run as a model credential", entry, credential)
		}
	}
}

// TestShareStillRefusesARepositoryCredential is WC1's other half: a repository's
// OWN credential — the same family's other half, repo-credential--<slug>--
// anthropic-api-key--project — is still refused, and refused with a reason of its
// own rather than the general "not a credential the workspace can offer its
// members" sentence, which would be false about a name the workspace's bundle
// really holds.
//
// The reason is the entry's own (plan §10): its name carries the repository the
// copy was made for, so it is one repository's by construction and a member's
// runs are other repositories'. A provider's key of the same family is a
// different entry and IS shared (TestShareAcceptsAProviderKey), and the refusal
// says so, because the two names differ only in their credential half.
func TestShareStillRefusesARepositoryCredential(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	before := readFileString(t, owner)
	slug, err := task.Slug("github", "marker-org/aurora")
	if err != nil {
		t.Fatalf("task.Slug: %v", err)
	}
	ours, err := credsvc.RepoCredentialKey(slug, config.AnthropicKeyAccount)
	if err != nil {
		t.Fatalf("credsvc.RepoCredentialKey: %v", err)
	}
	if _, kind, tier, ok := credsvc.CredentialKey(ours); !ok || kind != credsvc.KindRepoCredential || tier != creds.TierB {
		t.Fatalf("CredentialKey(%s) = (%v, %v, %v), want one repository's own key at tier B", ours, kind, tier, ok)
	}

	out, _, err := execute(t, "workspace", "share", ours, "--workspace", "workspace-alpha", "--projects", "aurora")
	if err == nil {
		t.Fatalf("sharing a repository's own credential was accepted:\n%s", out)
	}
	for _, want := range []string{
		ours,
		"a repository's own Anthropic key",
		"one repository's by construction",
		"provider-key",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The name the general refusal is about is still refused by it, so the
	// reason above is that one entry's and not a rewording of the whole rule.
	if _, _, err := execute(t, "workspace", "share", "not-a-credential", "--workspace", "workspace-alpha", "--projects", "aurora"); err == nil ||
		!strings.Contains(err.Error(), "is not a credential the workspace can offer its members") {
		t.Errorf("a name the bundle cannot spell: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused share wrote the file:\n%s", after)
	}
}

// TestShareReplacesTheEntryItAlreadyHolds: the entry is written whole, as a
// member entry is, so a second declaration is how a project or a repository
// leaves the list — and the sentence says so, because what the owner reads is
// what tells them the old coverage is going.
func TestShareReplacesTheEntryItAlreadyHolds(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	first := []string{"workspace", "share", "bitbucket-token", "--workspace", "workspace-alpha",
		"--projects", "aurora", "--covers", "marker-org/aurora", "--covers", "marker-org/borealis"}
	if out, _, _ := execute(t, first...); !strings.Contains(out, "offer bitbucket-token") {
		t.Fatalf("the first share does not read as an offer:\n%s", out)
	}
	ours := []string{"workspace", "share", "bitbucket-token", "--workspace", "workspace-alpha",
		"--projects", "aurora", "--covers", "marker-org/aurora"}
	out, _, _ := execute(t, ours...)
	after := readFileString(t, owner)
	if strings.Contains(after, "marker-org/borealis") {
		t.Errorf("the replaced entry still carries the repository the second declaration dropped:\n%s", after)
	}
	if !strings.Contains(out, "this replaces the entry workspace workspace-alpha shares for bitbucket-token") ||
		!strings.Contains(out, "is gone from the list") {
		t.Errorf("the confirmation does not read as a replacement:\n%s", out)
	}
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("%v\n%s", err, after)
	}
	entry := workspaceShared(lc)["bitbucket-token"]
	if !slices.Equal(entry.Covers, []string{"marker-org/aurora"}) || !slices.Equal(entry.Projects, []string{"aurora"}) {
		t.Errorf("the entry is %+v, want the declaration and nothing else", entry)
	}
	// The same declaration again is the same file: a replace writes the entry it
	// is given, so a re-run has nothing to do and prints the no-change answer.
	before := readFileString(t, owner)
	if out, _, _ := execute(t, ours...); !strings.Contains(out, "no change") {
		t.Errorf("a repeated declaration is not a no-op:\n%s", out)
	}
	if readFileString(t, owner) != before {
		t.Errorf("a repeated declaration rewrote the file:\n%s", readFileString(t, owner))
	}
}
