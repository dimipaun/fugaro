package infra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestLaunchersVariableDerivedFromRoster holds the whole of launchers.go: the
// installation's `launcher_accounts` is the launcher class (D1938, F25), it is
// derived from the same roster list the roots grant the invoker from (F31: a
// launcher who may invoke passes IAM, and the class check is the only fence
// left after it, F36), and it is the BARE addresses a Google ID token carries.
//
// Three facts, each of which has a failure of its own:
//
//   - The class follows the MERGED roster. `InstallationVars` writes the
//     launchers and the operators as one list, because an operator runs, watches
//     and cancels runs like a launcher (launchersWithOperators) — so the class
//     must hold the operators too, or an operator who invokes the credentials
//     service would be refused by the process that just admitted them at IAM.
//   - A `group:` or `domain:` launcher is not an account. The class is matched
//     against the token's email claim and a group is not one; the module's
//     grant admits it and the process refuses it, fail-closed, which is the
//     reading credentials.tf takes for the operators' person class.
//   - An installation whose roster names nobody writes an EMPTY LIST, never
//     null: the module renders the environment entry only when it is non-empty
//     (a process that admits no launcher must still start), and a null would
//     make the two cases one.
//
// Mutation (run, restore): return the members as given and the address
// assertions fail; make launcherAccounts(nil) return nil and the JSON assertion
// fails with null.
func TestLaunchersVariableDerivedFromRoster(t *testing.T) {
	spec := InstallationSpec{
		Launchers: []string{"user:dev@example.com", "group:eng@example.com", "user:dev@example.com"},
		Operators: []string{"user:ops@example.com", "domain:example.com"},
	}
	raw, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Launchers        []string `json:"launchers"`
		LauncherAccounts []string `json:"launcher_accounts"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the installation's tfvars do not decode: %v", err)
	}

	wantMembers := []string{"domain:example.com", "group:eng@example.com", "user:dev@example.com", "user:ops@example.com"}
	if !slices.Equal(got.Launchers, wantMembers) {
		t.Errorf("launchers = %v, want %v: the roots grant launchers' access to the launchers AND the operators, sorted", got.Launchers, wantMembers)
	}
	wantAccounts := []string{"dev@example.com", "ops@example.com"}
	if !slices.Equal(got.LauncherAccounts, wantAccounts) {
		t.Errorf("launcher_accounts = %v, want %v: the class is the roster's people, bare, sorted, each once — the operators included, and a group or a domain left out because no ID token carries one as its email claim (D1938, F25)", got.LauncherAccounts, wantAccounts)
	}

	// The two lists are one derivation: every user member of the merged roster
	// is an account, and no account is a member the invoker grants do not
	// name (F31).
	var fromMembers []string
	for _, m := range got.Launchers {
		if email, ok := strings.CutPrefix(m, "user:"); ok {
			fromMembers = append(fromMembers, email)
		}
	}
	slices.Sort(fromMembers)
	if !slices.Equal(got.LauncherAccounts, slices.Compact(fromMembers)) {
		t.Errorf("launcher_accounts = %v, want the user members of launchers (%v): the class and the invoker grant are made from one list, and a class that named somebody the grant does not leaves a caller IAM refuses", got.LauncherAccounts, slices.Compact(fromMembers))
	}

	// An installation that names nobody: no key at all, so the module's own
	// default ([]) is the class and its bytes are what they were before the
	// class existed. An empty list written out would be the same plan with a
	// diff in every installation's tfvars; null is the one spelling that must
	// never appear, because the module renders the class's environment entry
	// only when it is non-empty (fail closed).
	raw, err = InstallationVars(InstallationSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "launcher_accounts") {
		t.Errorf("an installation whose roster names no launcher writes %s, want no launcher_accounts key: the module's own default is the empty class, and the key exists only where it says something", strings.TrimSpace(string(raw)))
	}
}

// TestLauncherAccountsAreThePeopleOfTheList is the function's own contract,
// apart from the tfvars it is written into: dedupe and sort are what make two
// equal rosters give equal bytes (sortedJSON's promise), and a `user:` member
// whose address carries the prefix's own colon is still one account.
func TestLauncherAccountsAreThePeopleOfTheList(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []string
		want []string
	}{
		{"people", []string{"user:b@example.com", "user:a@example.com"}, []string{"a@example.com", "b@example.com"}},
		{"once", []string{"user:a@example.com", "user:a@example.com"}, []string{"a@example.com"}},
		{"classes that are not accounts", []string{"group:eng@example.com", "domain:example.com"}, []string{}},
		{"nothing", nil, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := launcherAccounts(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("launcherAccounts(%v) = %v, want %v", tt.in, got, tt.want)
			}
			if got == nil {
				t.Errorf("launcherAccounts(%v) returned nil, want an empty list", tt.in)
			}
		})
	}
}
