package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAccountsAddRefusesAName is the name rule as a table (spec §2.4): the two
// reserved names get their own sentences, and a name that does not match the
// pattern gets the pattern and never itself. Nothing is written either way.
func TestAccountsAddRefusesAName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"default", `"default" is reserved for the unnamed claude-oauth-token secret`},
		{"bitbucket-token", "this name is reserved"},
		{"Team-A", "account name must match"},
		{"-lead", "account name must match"},
		{"trail-", "account name must match"},
		{"a--b", "account name must match"},
		{"9lives", "account name must match"},
		{strings.Repeat("a", 32), "account name must be at most 31 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
			before := readFileString(t, path)

			// The name goes after -- because one of the table's own cases
			// starts with a dash, and a name is a name however it begins.
			_, _, err := execute(t, "accounts", "add", "--repos", "acme/app", "--yes", "--", tc.name)
			if err == nil {
				t.Fatalf("%q was accepted as an account name", tc.name)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			// The name is untrusted text on its way to a terminal, and the
			// two sentences that do not have to name it do not.
			if tc.name != "default" && strings.Contains(err.Error(), tc.name) {
				t.Errorf("the refusal echoes the name: %v", err)
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("a refused name wrote the file:\n%s", after)
			}
		})
	}
}

// TestAccountsAddRefusesASubsetViolation is the subset rule (spec §2.2 step 2,
// D3): the profile admits a repository the new account does not cover, so the
// command refuses and says which one and how to include it. The alternative —
// silently widening the account — is the one thing the rule exists to prevent.
func TestAccountsAddRefusesASubsetViolation(t *testing.T) {
	accountsAddFixture(t, accountsAddLocalTwoRepos)

	_, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	if err == nil {
		t.Fatal("the subset rule was not applied")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"acme/lib", "--repos", "profiles.standard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestAccountsAddSubsetRuleIsCheckedBeforeAnyWrite: the refusal above happens
// before the local file is touched, so a rerun after fixing --repos is the
// same command and not a repair. The bytes are identical and no backup was
// taken, which is the whole of what "before any write" means.
func TestAccountsAddSubsetRuleIsCheckedBeforeAnyWrite(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocalTwoRepos)
	before := readFileString(t, path)

	if _, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes"); err == nil {
		t.Fatal("the subset rule was not applied")
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused subset check wrote the file:\n%s", after)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Errorf("a refused subset check backed the file up: %v", backups)
	}
}

// accountsAddLocalNoAllowRepos is the profile the subset rule cannot be asked
// about: it would list an account and it writes no allow_repos of its own, so
// the repositories it admits are the project's enrollment and no account is
// required to cover them.
const accountsAddLocalNoAllowRepos = `version: 1
name: aurora
gcp_project: aurora-gcp
region: us-east5
runs_bucket: fugaro-runs-aurora-gcp

profiles:
  standard:
    provider: github
`

// accountsAddLocalWildcard is the same profile with the wildcard instead of a
// list, which leaves the same question undecidable one repository at a time.
const accountsAddLocalWildcard = `version: 1
name: aurora
gcp_project: aurora-gcp
region: us-east5
runs_bucket: fugaro-runs-aurora-gcp

profiles:
  standard:
    provider: github
    allow_repos:
      - "*"
`

// TestAccountsAddRefusesAProfileTheRuleCannotBeAskedAbout: the subset rule is
// decided repository by repository, so a profile that lists accounts must write
// its own exact list (D3, projectpolicy.validProfileAccounts). Both states that
// make that impossible are refused here, before the file is touched, rather
// than written and refused by the owner's next publish.
func TestAccountsAddRefusesAProfileTheRuleCannotBeAskedAbout(t *testing.T) {
	cases := []struct {
		name  string
		local string
		want  string
	}{
		{"no allow_repos", accountsAddLocalNoAllowRepos, "must write its own allow_repos"},
		{"a wildcard", accountsAddLocalWildcard, "wildcard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsAddFixture(t, tc.local)
			before := readFileString(t, path)

			_, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--profile", "standard", "--yes")
			if err == nil {
				t.Fatal("an account was added to a profile the rule cannot be asked about")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			for _, want := range []string{tc.want, "profiles.standard", "nothing was written"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("a refused profile wrote the file:\n%s", after)
			}
		})
	}
}

// TestAccountsAddWithoutACheckoutRequiresRepos: with no --repos and no origin
// remote to take the default from, the command refuses and names --repos and
// the field it fills, rather than declaring an account no policy can bound
// (spec §2.1).
func TestAccountsAddWithoutACheckoutRequiresRepos(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)
	before := readFileString(t, path)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "accounts", "add", "team-a", "--yes")
	if err == nil {
		t.Fatal("an account with no repositories was declared")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"--repos", "allow_data_to"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused repository list wrote the file:\n%s", after)
	}
}
