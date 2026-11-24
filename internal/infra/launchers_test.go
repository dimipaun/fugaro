package infra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/workspace"
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

// TestTheClassListIsSpelledIntoTheGrantsGrammar (WC3, R5, D3143) pins the shape
// every entry of the intake's class list is written with, and it is the shape
// WORKS in the direction the class is used: the module spells each entry
// `user:<entry>` into the intake's invoker set (intake.tf's local.intake_invoker_members)
// and merges the entry into the class list beside the launchers' own, whose
// entries var.launcher_accounts has held to this same shape since F31. An
// address carrying a `:` in the local part is the member spelling itself — an
// entry `user:a:b@example.com` is not a principal IAM has and no ID token's
// email claim carries one — so a list that held one would be a grant that
// admits nobody spelled from a value that reads like a grant
// (internal/access's admin list refuses one for exactly this reason, and its
// derivation drops the person rather than write an entry its own writer
// refuses).
func TestTheClassListIsSpelledIntoTheGrantsGrammar(t *testing.T) {
	for _, addr := range []string{
		"a:b@example.com",
		"user:ada@example.com",
		"a b@example.com",
		"a@b@example.com",
		"@example.com",
		"ada@",
		"ada@exa mple.com",
		"ada.example.com",
		"",
	} {
		if memberAddress(addr) {
			t.Errorf("memberAddress(%q) = true: an entry of the class list is the bare address a token's email claim holds, and the grant is that address spelled \"user:<entry>\" — this value is a member spelling, an empty side or a second @, so the module would write a member IAM has no principal for and the class would hold a value no claim carries", addr)
		}
	}
	for _, addr := range []string{
		"ada@example.com",
		"ada.b@example.com",
		"ada+b@example.com",
		"ada_b@example.com",
		"ada@sub.example.com",
	} {
		if !memberAddress(addr) {
			t.Errorf("memberAddress(%q) = false: this is a person's bare address and the module's own launcher_accounts rule takes it (one @, no colon and no space in the local part, no @ in the domain)", addr)
		}
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

// TestRosterPeopleAreNotLaunchersOfTheWorkspaceProject is WC3's own rule at the
// derivation that would break it (F10, R5): the people of a workspace's roster
// are the intake's SECOND class list and never a member of the installation's
// launchers, because that one list is also the workspace project's credentials
// service invoker set (iam.tf) — a roster person written there would be admitted
// to a service this task does not widen.
//
// Three facts, and each has a failure of its own:
//
//   - The roster's PEOPLE are its humans, and a service account's address is
//     nobody's. A machine's address is a service account's — the one spelling
//     internal/credsvc's foldLauncherAccounts refuses outright, so listing it
//     would be an entry no token can match — and the SAME address on a roster
//     entry whose kind says `human` is refused the same way, which is worse:
//     `workspace user add` defaults to human and neither validatePerson nor
//     CanonicalAddress refuses the suffix, so a roster carrying one would make
//     the intake's new revision exit on start, fail the apply that wrote it and
//     fail every later `fugaro update` of that workspace. A machine on a CUSTOM
//     domain carries no suffix at all (`fugaro workspace user add --kind sa`
//     takes any canonical address, and Roster.Validate requires nothing of the
//     kind), so its KIND is the only thing keeping it out of the class list and
//     out of the grant the module spells from it: the roster below holds one.
//     The OTHER refusal
//     NewLaunchers makes — an address past the fold's 512 bytes — is dropped
//     for the same reason, and the third is the class list's own grammar: an
//     address whose local part carries a `:` (which is what a `user:` prefix
//     is) is the member spelling and not the bare address a token's email
//     claim holds, so it is dropped rather than written into FUGARO_LAUNCHER_ACCOUNTS
//     and spelled into the invoker grant a second time (memberAddress, and
//     internal/access's rule for the same kind of value: the entry is the bare
//     address, never the "user:" form, and a derivation leaves such a person
//     off rather than write one its own writer refuses). The three are the
//     three the module's validation refuses, by the same rules, so the writer
//     and the plan agree and no value a roster can hold fails an update.
//   - The class the installation's launchers make is untouched by the roster:
//     it names the launchers' own people and nobody from the workspace.
//   - The intake's block carries the roster's people as the list of its own,
//     through IntakeOf, and writes NO key when there are none — which is every
//     installation that is not a workspace's project.
func TestRosterPeopleAreNotLaunchersOfTheWorkspaceProject(t *testing.T) {
	image := "europe-west4-docker.pkg.dev/p/r/fugaro@sha256:" + strings.Repeat("a", 64)
	roster := &workspace.Roster{
		Version: workspace.RosterVersion,
		People: []workspace.Person{
			{Address: "zoe@example.com", Kind: workspace.KindHuman},
			{Address: "bot@example.iam.gserviceaccount.com", Kind: workspace.KindSA},
			// A machine on a CUSTOM domain, which is the case the suffix guard
			// cannot catch: `fugaro workspace user add build@ci.example.com
			// --kind sa` stores it (any canonical address is taken) and nothing
			// in Roster.Validate requires a `sa` address to end in Google's
			// suffix, so the KIND is the only thing keeping it out of the class
			// list — and out of the invoker grant the module spells from it,
			// where `user:build@ci.example.com` would admit whoever holds that
			// email claim to write its own containers. Without the kind clause
			// this entry reaches `want`.
			{Address: "build@ci.example.com", Kind: workspace.KindSA},
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "robot@example.iam.gserviceaccount.com", Kind: workspace.KindHuman},
			// The same address in another case: the class is compared to a
			// FOLDED claim (credsvc's foldEmail lower cases the whole value
			// before foldLauncherAccounts refuses the suffix), so the filter
			// folds too and a spelling Google would never mint still cannot
			// reach the class list.
			{Address: "ROBOT@example.IAM.GServiceAccount.com", Kind: workspace.KindHuman},
			// A `:` in the local part is the member spelling's grammar and not
			// the bare address a token's email claim carries: the filter drops
			// it, because this list is ALSO the grant's input (the module
			// spells each entry "user:<entry>") and an entry that already
			// carries a colon would name a principal IAM has none of. The
			// module's validation refuses the same shape, so a roster can hold
			// one and no update fails over it: the person is left off the
			// class, which is what internal/access does with the same value.
			{Address: "a:b@example.com", Kind: workspace.KindHuman},
			// ... and one whose local part begins `user:`, which is the same
			// shape and not the member spelling it reads like:
			// `fugaro workspace user add user:ada@example.com --external`
			// stores it, and CanonicalAddress keeps it.
			{Address: "user:ada@example.com", Kind: workspace.KindHuman},
			// An address past the fold's 512 bytes is the OTHER shape
			// internal/credsvc's NewLaunchers refuses outright — a
			// configuration error, so the revision exits on start — and
			// nothing bounds a roster address. The filter drops it here and
			// the module's validation refuses it.
			{Address: strings.Repeat("a", 501) + "@example.com", Kind: workspace.KindHuman},
		},
	}
	want := []string{"ada@example.com", "zoe@example.com"}
	if got := IntakePeople(roster); !slices.Equal(got, want) {
		t.Errorf("IntakePeople(roster) = %v, want %v: the intake's second class is the roster's HUMANS, sorted and each once, minus the three shapes the class list cannot carry — a machine's address is a service account's and no interactive ID token carries one, whatever kind the roster spells beside it (credsvc's foldLauncherAccounts refuses that shape outright and the revision would exit on start), an address past the fold's 512 bytes is a configuration error there too, and an address whose local part carries a `:` is the member spelling rather than the bare address a claim holds", got, want)
	}
	if got := IntakePeople(nil); got == nil || len(got) != 0 {
		t.Errorf("IntakePeople(nil) = %#v, want an empty list: a workspace with no roster names nobody, and null would make the two cases one", got)
	}

	vars, err := InstallationVars(InstallationSpec{Launchers: []string{"user:ops@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	vars, err = SetIntake(vars, Intake(image))
	if err != nil {
		t.Fatal(err)
	}
	vars, err = SetIntakePeople(vars, IntakePeople(roster))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Launchers        []string `json:"launchers"`
		LauncherAccounts []string `json:"launcher_accounts"`
		Intake           struct {
			People []string `json:"people"`
		} `json:"intake"`
	}
	if err := json.Unmarshal(vars, &got); err != nil {
		t.Fatalf("the installation's tfvars do not decode: %v", err)
	}
	if !slices.Equal(got.LauncherAccounts, []string{"ops@example.com"}) {
		t.Errorf("launcher_accounts = %v, want [ops@example.com]: the roster's people are the intake's own list and the class the credentials service answers to is made of the launchers' own, so a workspace person added here would be admitted to the workspace project's credentials service (F10)", got.LauncherAccounts)
	}
	for _, m := range got.Launchers {
		if strings.Contains(m, "example.com") && m != "user:ops@example.com" {
			t.Errorf("launchers = %v names a workspace person: the invoker grant it is made from is the credentials service's too", got.Launchers)
		}
	}
	if !slices.Equal(got.Intake.People, want) {
		t.Errorf("intake.people = %v, want %v: the intake's block is where the roster's people are written", got.Intake.People, want)
	}
	spec, have, err := IntakeOf(vars)
	if err != nil || !have {
		t.Fatalf("IntakeOf(vars) = %+v, %v, %v, want the block this run wrote", spec, have, err)
	}
	if !slices.Equal(spec.People, want) {
		t.Errorf("IntakeOf(vars).People = %v, want %v: the block's list is read back as it was written", spec.People, want)
	}

	// And an installation that is not a workspace's project: no people at all,
	// and NO key — the module's own default is the empty list, so a key here
	// would be a diff in every other installation's document.
	none, err := SetIntakePeople(vars, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(none), `"people"`) {
		t.Errorf("an installation with nobody on the roster writes %s, want no people key", strings.TrimSpace(string(none)))
	}
	spec, have, err = IntakeOf(none)
	if err != nil || !have {
		t.Fatalf("IntakeOf(none) = %+v, %v, %v, want the block without its people", spec, have, err)
	}
	if len(spec.People) != 0 {
		t.Errorf("IntakeOf(none).People = %v, want none", spec.People)
	}
}

// TestIntakeClassIsTheLaunchersAndThePeoplesListAsOne holds the second half of
// what a fill of the intake's block has to know (WC3, R5): the class the
// module's revision is handed is the launchers and the roster's people as ONE
// list (intake.tf's local.intake_accounts), so a roster change that names only a
// launcher moves var.intake.people and leaves that class exactly what it was.
//
// Two failures this decides between, and they are the same write: a caller that
// compares the people list alone writes the block for the launcher the roster
// gained, and the apply behind it has no plan to make — which is the state
// `fugaro update` reads as the module not carrying the class onto the revision
// at all and refuses the run over. The union is what tells the two apart, and
// LauncherAccountsOf is where the other half of it is read from.
func TestIntakeClassIsTheLaunchersAndThePeoplesListAsOne(t *testing.T) {
	vars, err := InstallationVars(InstallationSpec{
		Launchers: []string{"user:dev@example.com", "user:ops@example.com"},
		Operators: []string{"user:owner@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	vars, err = SetIntake(vars, Intake("us-east5-docker.pkg.dev/p/i/fugaro@sha256:"+strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	launchers, err := LauncherAccountsOf(vars)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dev@example.com", "ops@example.com", "owner@example.com"}
	if !slices.Equal(launchers, want) {
		t.Errorf("LauncherAccountsOf(vars) = %v, want %v: the key is read as InstallationVars wrote it, operators merged in", launchers, want)
	}

	// The class of a roster whose only person is already a launcher is the
	// launchers' own list: nothing of the roster's is added, and the sorted
	// union is the same bytes for the empty list and for it.
	roster := []string{"owner@example.com"}
	if !slices.Equal(IntakeClass(launchers, roster), launchers) {
		t.Errorf("IntakeClass(launchers, %v) = %v, want the launchers' own list %v: a person the launchers already carry is one entry, not two", roster, IntakeClass(launchers, roster), launchers)
	}
	if !slices.Equal(IntakeClass(launchers, roster), IntakeClass(launchers, nil)) {
		t.Errorf("the class of a roster naming a launcher (%v) and of one naming nobody (%v) differ: the roster's own person is admitted either way, so the class the revision is handed is one list and the write is not one it can move", IntakeClass(launchers, roster), IntakeClass(launchers, nil))
	}

	// A person the launchers do NOT carry is the class's own entry, and the
	// two lists' addresses come out sorted together rather than in the order
	// the caller read them.
	got := IntakeClass(launchers, []string{"ada@example.com", "dev@example.com"})
	want = []string{"ada@example.com", "dev@example.com", "ops@example.com", "owner@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("IntakeClass = %v, want %v (sorted, each once, both lists)", got, want)
	}

	// And a document that carries no launcher_accounts at all — every
	// installation whose launcher list names no `user:` member — reads as the
	// empty list rather than an error, which is the module's own default.
	bare, err := SetIntake([]byte(`{"project":"p"}`), Intake("us-east5-docker.pkg.dev/p/i/fugaro@sha256:"+strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	none, err := LauncherAccountsOf(bare)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("LauncherAccountsOf on a document with no key = %v, want none", none)
	}
}
