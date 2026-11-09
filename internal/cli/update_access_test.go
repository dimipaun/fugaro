package cli

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_access_test.go is the roles step of the converge (update_access.go,
// RS3): what `fugaro update` makes of an installation whose roles are still the
// version-1 member lists, what it does with the member forms a grant cannot
// hold, and where the lists its own apply carries are read.

// accessRig is an installation at the previous shape: a version-1 local config
// carrying the two lists under the old keys, and the installation's own outputs
// carrying them under Terraform's names (which never change, D1589) — the two
// agreeing unless a test wants them not to. The grants node is absent.
func accessRig(t *testing.T, localOperators, localAdmins, installedOperators, installedAdmins []string) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := updateBudgetRig(t)
	r.appendConfig(t, "terraform:\n  launchers: "+yamlList(localOperators)+"\n  operators: "+yamlList(localAdmins)+"\n")
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"launchers": installedOperators, "operators": installedAdmins})}
	// The installation's state, so its outputs can be read at all: terraform
	// state rm leaves the outputs behind, so the outputs alone do not say the
	// installation is there (installationOutputs).
	r.script["show -json"] = map[string]any{"stdout": userStateJSON}
	r.save(t)
	return r, db
}

// accessOut is a run's whole account: its two streams, and the error's own
// text when there is one. ErrorText of a nil error is not an empty string, it
// is a panic, and three of the runs below are the ones that must not fail.
func accessOut(out, errOut string, err error) string {
	if err == nil {
		return out + errOut
	}
	return out + errOut + ErrorText(err)
}

// accessGrants is the grants node as the fake database holds it.
func accessGrants(t *testing.T, db *gcpfake.RTDB) []accesslib.Grant {
	t.Helper()
	cl, err := newBudgetClient(t.Context(), db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	node, err := accesslib.Read(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	return node.Grants
}

// grantLines renders grants the way the migration's own sentence does, sorted,
// so a roster can be compared as the one line an operator reads.
func grantLines(grants []accesslib.Grant) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.User+" ("+string(g.Role)+")")
	}
	slices.Sort(out)
	return out
}

// TestUpdateMigratesListsToAccessNode: one `fugaro update` of an installation
// at the previous shape — the two member lists in the version-1 local config
// and in the installation's own outputs, and no grants node — writes the node
// holding the roles and retires the old keys (D1581). The roster the node is
// written from is the INSTALLATION's, and a copy on this machine that disagrees
// with it is said out loud rather than obeyed (accessStale): the migration is
// the one step `fugaro update` may not refuse, because the refusal's own way
// out is the command being refused, and the installation's roster is what the
// apply carries either way.
func TestUpdateMigratesListsToAccessNode(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	admins := []string{"user:root@example.com"}
	for _, tc := range []struct {
		name  string
		local []string
		stale bool
	}{
		{"this machine's copy agrees", operators, false},
		{"this machine's copy disagrees", []string{"user:gone@example.com"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := accessRig(t, tc.local, admins, operators, admins)
			out, errOut, err := executeStdin(t, "", "update", "--yes")
			all := accessOut(out, errOut, err)
			if err != nil {
				t.Fatalf("one fugaro update did not converge an installation at the previous shape: %v\n%s", err, all)
			}
			want := []string{"user:ops@example.com (operator)", "user:root@example.com (admin)"}
			if got := grantLines(accessGrants(t, db)); !slices.Equal(got, want) {
				t.Errorf("the grants node holds %v, want %v:\n%s", got, want, all)
			}
			if !strings.Contains(out, "migrated the installation's member lists into the grants node") {
				t.Errorf("the run does not say it migrated the roles, which is a rewrite of who may do what:\n%s", all)
			}
			if got := strings.Contains(out, "is not what the installation's own outputs list"); got != tc.stale {
				t.Errorf("the drift warning is %v, want %v:\n%s", got, tc.stale, all)
			}
			// The old keys retire, and the lists follow the node: the machine's
			// own copy of the launcher list is not what lands here.
			lc, err := localcfg.Load(r.cfg)
			if err != nil {
				t.Fatalf("the local config the run wrote does not load: %v\n%s", err, all)
			}
			if lc.Version != localcfg.ConfigVersion {
				t.Errorf("the local config is version %d, want %d: the version-1 keys are what the migration retires", lc.Version, localcfg.ConfigVersion)
			}
			// The operator list is every project-scope grant: an admin operates
			// too (the module's launchers_only, iam.tf:274).
			if got, want := lc.Terraform.Operators, []string{"user:ops@example.com", "user:root@example.com"}; !slices.Equal(got, want) {
				t.Errorf("the local config's operators are %v, want %v:\n%s", got, want, all)
			}
			if got := lc.Terraform.Admins; !slices.Equal(got, admins) {
				t.Errorf("the local config's admins are %v, want %v:\n%s", got, admins, all)
			}
			if b, err := os.ReadFile(r.cfg); err == nil && strings.Contains(string(b), "launchers:") {
				t.Errorf("the retired key is still in the local config:\n%s", b)
			}
			// The installation's own variables keep their names and take the
			// roles' lists (D1589): a pure rename of the file, not of the module.
			v := r.tfvars(t)
			if got, want := members(v["launchers"]), []string{"user:ops@example.com", "user:root@example.com"}; !slices.Equal(got, want) {
				t.Errorf("the plan's launchers are %v, want %v:\n%s", got, want, all)
			}
			if got := members(v["operators"]); !slices.Equal(got, admins) {
				t.Errorf("the plan's operators are %v, want %v:\n%s", got, admins, all)
			}
		})
	}
}

// TestAnAgentSessionWritesNoGrantsNode: the roles step runs before the loop's
// first confirmation (init_stages.go's converge calls syncAccess), so the
// migration's write is the one cloud write of a converge that no confirmation
// stands in front of — and a coding agent's session writes nothing. The
// migration is no exception (refuseAgentAccess): the node is left absent, the
// run answers with the session's own refusal instead of the migration's line,
// and the installation is left for the person's next `fugaro update`.
func TestAnAgentSessionWritesNoGrantsNode(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	admins := []string{"user:root@example.com"}
	r, db := accessRig(t, operators, admins, operators, admins)
	t.Setenv(agentMarkers[0], "1")
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	all := accessOut(out, errOut, err)
	if got := db.Value(accesslib.PathNode); got != nil {
		t.Errorf("a coding agent's session migrated the roles into the grants node: %v\n%s", got, all)
	}
	if err == nil {
		t.Fatalf("the roles step wrote nothing and still reported a converged run:\n%s", all)
	}
	if !strings.Contains(all, agentMarkers[0]+" is set") || !strings.Contains(all, "your own terminal window") {
		t.Errorf("the refusal is not the session's own:\n%s", all)
	}
	if strings.Contains(all, "migrated the installation's member lists into the grants node") {
		t.Errorf("the run says it migrated a node it never wrote:\n%s", all)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("terraform applied %d time(s) in an agent session: %q", n, r.calls(t))
	}
}

// TestLegacyMemberFormsKeepTheirGrants: a `group:`, `domain:` or
// `serviceAccount:` member is an IAM member the grant model cannot hold — the
// CLI cannot read a caller's groups, so the node cannot rank it — and it stays
// a LEGACY IAM-ONLY member (F25): the migration writes a node holding the
// `user:` members alone, and the lists this run applies carry the others
// forward, so the cloud grants they have are not revoked by the release that
// introduced the node.
func TestLegacyMemberFormsKeepTheirGrants(t *testing.T) {
	operators := []string{"user:ops@example.com", "group:eng@example.com", "domain:example.com"}
	admins := []string{"user:root@example.com", "serviceAccount:auto@proj-1234.iam.gserviceaccount.com"}
	r, db := accessRig(t, operators, admins, operators, admins)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro update refused an installation whose lists hold a group, a domain and a service account: %v\n%s", err, all)
	}
	want := []string{"user:ops@example.com (operator)", "user:root@example.com (admin)"}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, want) {
		t.Errorf("the grants node holds %v, want %v: a grant cannot hold a group, a domain or a service account\n%s", got, want, all)
	}
	v := r.tfvars(t)
	// Terraform's launchers are the operators' list unioned with the admins'
	// (launchersWithOperators: the module reads everyone in `operators` as a
	// launcher too), so both legacy forms of the machine's launcher list are in
	// it, and so is the admin.
	wantLaunchers := []string{"domain:example.com", "group:eng@example.com", "serviceAccount:auto@proj-1234.iam.gserviceaccount.com", "user:ops@example.com", "user:root@example.com"}
	if got := members(v["launchers"]); !slices.Equal(got, wantLaunchers) {
		t.Errorf("the plan's launchers are %v, want %v: a legacy member that fell out of the list would lose its grants\n%s", got, wantLaunchers, all)
	}
	wantAdmins := []string{"serviceAccount:auto@proj-1234.iam.gserviceaccount.com", "user:root@example.com"}
	if got := members(v["operators"]); !slices.Equal(got, wantAdmins) {
		t.Errorf("the plan's operators are %v, want %v:\n%s", got, wantAdmins, all)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config the run wrote does not load: %v\n%s", err, all)
	}
	// The file's own lists are the ones the node holds, each with the members
	// only it may carry: the legacy forms stay in the list they were written in,
	// and the admin is in the operators' list as well (an admin operates too).
	wantOperators := []string{"domain:example.com", "group:eng@example.com", "user:ops@example.com", "user:root@example.com"}
	if got := lc.Terraform.Operators; !slices.Equal(got, wantOperators) {
		t.Errorf("the local config's operators are %v, want %v:\n%s", got, wantOperators, all)
	}
	if got := lc.Terraform.Admins; !slices.Equal(got, wantAdmins) {
		t.Errorf("the local config's admins are %v, want %v:\n%s", got, wantAdmins, all)
	}
	if strings.Contains(all, "accessDropped") || strings.Contains(all, "lose the role") {
		t.Errorf("the run says a member lost the role, and a legacy member keeps its grants:\n%s", all)
	}
}

// TestPlanIsEmptyForAPureRename: the rename moves the WORDS and not the IAM.
// Terraform's two variables are built from the roles' lists — `operators` from
// the admins', `launchers` from the operators' unioned with the admins'
// (launchersWithOperators, because the module reads everyone in `operators` as
// a launcher too: iam.tf:274 subtracts them from its launcher-only set) — and
// the migration writes exactly the pair the previous shape's config spelled:
// the old launcher list, plus the old operators. So the installation applies
// the same two variables as before, member for member, and the plan it makes
// of them is empty: nothing is added and nothing is revoked, which is what
// makes the migration a step an operator can run on a working installation.
//
// The plan itself is the module's, which the fake terraform cannot compute, so
// what this pins is the pair of variables the plan is a function of.
func TestPlanIsEmptyForAPureRename(t *testing.T) {
	operators := []string{"user:ops@example.com", "user:second@example.com"}
	admins := []string{"user:root@example.com"}
	r, db := accessRig(t, operators, admins, operators, admins)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro update did not converge the rename: %v\n%s", err, all)
	}
	if got := db.Value(accesslib.PathNode); got == nil {
		t.Fatalf("the migration wrote no node, so there is nothing for the rename to be:\n%s", all)
	}
	v := r.tfvars(t)
	// The previous shape's variables: `launchers` was the launcher list with
	// the operators in it, and `operators` was the operator list.
	wantLaunchers := append(slices.Clone(operators), admins...)
	slices.Sort(wantLaunchers)
	if got := members(v["launchers"]); !slices.Equal(got, wantLaunchers) {
		t.Errorf("the plan's launchers are %v, want %v: a member the previous shape's config did not carry is an IAM change the rename must not make\n%s", got, wantLaunchers, all)
	}
	if got := members(v["operators"]); !slices.Equal(got, admins) {
		t.Errorf("the plan's operators are %v, want %v: the admin list is the same list it always was\n%s", got, admins, all)
	}
	for _, w := range []string{"no longer", "lost the role", "lose the role"} {
		if strings.Contains(all, w) {
			t.Errorf("the rename dropped a member (%q in the account):\n%s", w, all)
		}
	}
}

// accessNodeDoc plants the grants node from grants, in the shape the fake
// stores and the reader decodes (`user`, `role`, `scope`): the state an
// installation whose roles have moved to the node is in. It is set rather than
// written through the writer because these tests are about what a run does with
// a node it finds.
func accessNodeDoc(grants ...accesslib.Grant) map[string]any {
	out := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		out = append(out, map[string]any{"user": g.User, "role": string(g.Role), "scope": g.Scope.String()})
	}
	return map[string]any{"grants": out}
}

// projectGrant is one project-scope grant: the only kind the migration and the
// two flags write.
func projectGrant(user string, role accesslib.Role) accesslib.Grant {
	return accesslib.Grant{User: user, Role: role, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}}
}

// TestAMigrationTheCallerCannotWriteIsNotAFailure: the grants node sits under
// config/admin and carries `.write: false`, so only a caller holding
// roles/firebasedatabase.admin reaches it at all — the project's implicit
// admins (owners, editors, budget admins, F22/D1592), not every operator. The
// migration is therefore best-effort for exactly that caller: the refusal is
// said out loud, the two lists stay this machine's for this run, and the one
// command the user runs is not the command that failed (accessNeedsAdmin).
//
// The alternative is a livelock: `fugaro update` refused by a write only another
// door the same user runs can grant, which is the refusal the roles step's own
// doc names as the reason the migration is not refused on drift either.
func TestAMigrationTheCallerCannotWriteIsNotAFailure(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	admins := []string{"user:root@example.com"}
	r, db := accessRig(t, operators, admins, operators, admins)
	db.Deny(accesslib.PathNode)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("a caller whose IAM refuses the grants node's write made fugaro update fail: %v\n%s", err, all)
	}
	if got := db.Value(accesslib.PathNode); got != nil {
		t.Errorf("the node is there though every write to it answered 401: %v", got)
	}
	for _, w := range []string{
		"roles/firebasedatabase.admin",
		"the operators and admins stay this machine's lists",
		"a project Owner runs fugaro update once",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("the run does not say %q, so an operator whose roles stayed put is told nothing:\n%s", w, all)
		}
	}
	// The lists this run applies are the file's: the step derived nothing from
	// a node that is not there, and the installation is applied exactly as the
	// previous shape applied it.
	v := r.tfvars(t)
	wantLaunchers := []string{"user:ops@example.com", "user:root@example.com"}
	if got := members(v["launchers"]); !slices.Equal(got, wantLaunchers) {
		t.Errorf("the plan's launchers are %v, want %v:\n%s", got, wantLaunchers, all)
	}
	if got := members(v["operators"]); !slices.Equal(got, admins) {
		t.Errorf("the plan's operators are %v, want %v:\n%s", got, admins, all)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config the run wrote does not load: %v\n%s", err, all)
	}
	if got := lc.Terraform.Operators; !slices.Equal(got, operators) {
		t.Errorf("the local config's operators are %v, want %v:\n%s", got, operators, all)
	}
	if got := lc.Terraform.Admins; !slices.Equal(got, admins) {
		t.Errorf("the local config's admins are %v, want %v:\n%s", got, admins, all)
	}
}

// TestPlanOnlyWritesNoNodeAndLandsNoList: --plan-only is the door that creates
// nothing (update_managed.go's gateable and managedStage both ask the flag
// first), and every branch of the roles step writes. A preview of an
// installation at the previous shape therefore leaves the node absent — the
// state the installation is in, which is what a plan is a plan of — and the
// next update that runs migrates it. A run's own --operator is the same
// computation with the write left out (foldedNode): the plan names the roles
// this run would land, and the node keeps the ones it holds.
func TestPlanOnlyWritesNoNodeAndLandsNoList(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	t.Run("an installation at the previous shape", func(t *testing.T) {
		r, db := accessRig(t, operators, nil, operators, nil)
		before, err := os.ReadFile(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		out, errOut, err := executeStdin(t, "", "update", "--yes", "--plan-only")
		all := accessOut(out, errOut, err)
		if err != nil {
			t.Fatalf("a plan of an installation at the previous shape failed: %v\n%s", err, all)
		}
		if got := db.Value(accesslib.PathNode); got != nil {
			t.Errorf("--plan-only migrated the installation's lists into the grants node: %v\n%s", got, all)
		}
		if after, err := os.ReadFile(r.cfg); err == nil && string(after) != string(before) {
			t.Errorf("--plan-only rewrote the local config:\n%s", after)
		}
	})
	t.Run("a run naming its own list", func(t *testing.T) {
		r, db := accessRig(t, operators, nil, operators, nil)
		db.Set(accesslib.PathNode, accessNodeDoc(projectGrant("user:ops@example.com", accesslib.RoleOperator)))
		out, errOut, err := executeStdin(t, "", "update", "--yes", "--plan-only", "--operator", "user:new@example.com")
		all := accessOut(out, errOut, err)
		if err != nil {
			t.Fatalf("a plan of a run naming its own --operator list failed: %v\n%s", err, all)
		}
		if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:ops@example.com (operator)"}) {
			t.Errorf("--plan-only landed this run's --operator list in the node: %v\n%s", got, all)
		}
		// The preview shows the roles it would land, which is the list the
		// operator named and not the one the node holds.
		v := r.tfvars(t)
		if got := members(v["launchers"]); !slices.Equal(got, []string{"user:new@example.com"}) {
			t.Errorf("the plan's launchers are %v, want this run's --operator list:\n%s", got, all)
		}
		if got := members(v["operators"]); len(got) != 0 {
			t.Errorf("the plan's operators are %v, want none: the run named only --operator\n%s", got, all)
		}
	})
}

// TestADeclinedRunLandsNoFlagList: the fold is a cloud write, and it belongs to
// the run's apply and not to its planning: a list named on the command line is
// landed by the installation root once the run is past its confirmation and
// about to apply (landFlagLists). A run whose confirmation is declined has
// changed nothing — the node included — so a change it never applied is not
// recorded as one that happened, and the next plain `fugaro update` by anyone
// does not apply it either.
//
// Mutation (fold in the roles step, as it was): the node holds the member the
// declined run named and this fails.
func TestADeclinedRunLandsNoFlagList(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	r, db := accessRig(t, operators, nil, operators, nil)
	db.Set(accesslib.PathNode, accessNodeDoc(projectGrant("user:ops@example.com", accesslib.RoleOperator)))
	// The plan has changes, so the run reaches its confirmation instead of
	// reporting an installation that already matches.
	updating(t, r)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--operator", "user:new@example.com")
	all := accessOut(out, errOut, err)
	if err == nil || !strings.Contains(all, "not confirmed") {
		t.Fatalf("a run whose confirmation was declined did not stop: %v\n%s", err, all)
	}
	if calls := subcommands(r.calls(t)); slices.Contains(calls, "apply") {
		t.Errorf("a declined run applied the installation: %q\n%s", calls, all)
	}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:ops@example.com (operator)"}) {
		t.Errorf("the node holds %v, want the roster it held: a run that applied nothing may not have changed the roles\n%s", got, all)
	}
}

// TestAFlagListThatCannotBeLandedStopsTheRun: unlike the migration, a run's own
// --operator/--admin is a change to a role the user asked for, written where the
// roles live (foldFlagLists), and a change that cannot be written may not look
// applied. The run stops with the sentence that names the door out
// (accessFlagsNeedAdmin) — this is the roles step's only stopping branch — and
// the node keeps the roster it had.
func TestAFlagListThatCannotBeLandedStopsTheRun(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	_, db := accessRig(t, operators, nil, operators, nil)
	db.Set(accesslib.PathNode, accessNodeDoc(projectGrant("user:ops@example.com", accesslib.RoleOperator)))
	db.Deny(accesslib.PathNode)
	out, errOut, err := executeStdin(t, "", "update", "--yes", "--operator", "user:new@example.com")
	all := accessOut(out, errOut, err)
	if err == nil || exitCode(t, err) != ExitUserError {
		t.Fatalf("a run whose --operator list could not be written did not stop: %v\n%s", err, all)
	}
	for _, w := range []string{
		"the grants node cannot be written with your credentials",
		"roles/firebasedatabase.admin",
		"fugaro update --budget-admin",
	} {
		if !strings.Contains(ErrorText(err), w) {
			t.Errorf("the refusal does not say %q:\n%s", w, all)
		}
	}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:ops@example.com (operator)"}) {
		t.Errorf("the node holds %v, want the roster it held: the flag list was not written and may not look applied\n%s", got, all)
	}
}

// TestAFlagListThatIsNotAMemberIsRefused: the flags are the one list a run takes
// from a command line, so it has been through no member rule, and a `user:`
// entry that is not one address is refused naming the list it came from rather
// than dropped — a node written from part of a roster grants less than the
// installation does, which is a revocation nobody asked for (projectGrants).
// The node is left as it was: the fold computes before it writes.
func TestAFlagListThatIsNotAMemberIsRefused(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	_, db := accessRig(t, operators, nil, operators, nil)
	db.Set(accesslib.PathNode, accessNodeDoc(projectGrant("user:ops@example.com", accesslib.RoleOperator)))
	out, errOut, err := executeStdin(t, "", "update", "--yes", "--operator", "user:not-one-address")
	all := accessOut(out, errOut, err)
	if err == nil || exitCode(t, err) != ExitUserError {
		t.Fatalf("a --operator list holding an entry no grant can hold was not refused: %v\n%s", err, all)
	}
	for _, w := range []string{`the --operator list holds "user:not-one-address"`, "which is not a user IAM member"} {
		if !strings.Contains(ErrorText(err), w) {
			t.Errorf("the refusal does not say %q:\n%s", w, all)
		}
	}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:ops@example.com (operator)"}) {
		t.Errorf("the node holds %v, want the roster it held: a refused list may not be written in part\n%s", got, all)
	}
}

// TestARunGivesUpWhenEveryWriteLosesTheRace: two operators naming lists at once
// must not each land the other's picture, so the fold's write is conditional on
// the ETag the node was read with (F23) and a mismatch re-reads and folds again
// — but only three times. A writer that lands a change before every attempt
// (another machine's `fugaro user add`, run in the window each of this run's
// writes is in flight) leaves the run nothing to land, and it says so and stops
// rather than retrying forever: the roles it was asked for are not the roles the
// node holds, and a run that returned anyway would leave the user reading a
// change that never happened.
//
// The requests are the fake database's, held one at a time so this test can put
// the other writer's landing exactly inside each window: the two reads the run
// takes before the installation root's apply -- the roles step's, and the derive
// the root takes for its own tfvars -- then the fold's own read, then a write
// and the re-read that follows it, three times. The fold is the installation
// root's and not the roles step's (landFlagLists): a list named on the command
// line is landed once the run is past its confirmation, and the read it is
// conditional on is the one taken there.
func TestARunGivesUpWhenEveryWriteLosesTheRace(t *testing.T) {
	operators := []string{"user:ops@example.com"}
	_, db := accessRig(t, operators, nil, operators, nil)
	db.Set(accesslib.PathNode, accessNodeDoc(projectGrant("user:ops@example.com", accesslib.RoleOperator)))
	others := []string{"user:first@example.com", "user:second@example.com", "user:third@example.com"}

	stamps := make([]<-chan struct{}, 0, 9)
	release := make([]func(), 0, 9)
	for range 9 {
		stamped, rel := db.HoldNext(accesslib.PathNode)
		stamps = append(stamps, stamped)
		release = append(release, rel)
	}
	done := make(chan struct{})
	var out, errOut string
	var err error
	go func() {
		defer close(done)
		out, errOut, err = executeStdin(t, "", "update", "--yes", "--operator", "user:new@example.com")
	}()
	// step waits for the request at i, or gives up on the run and releases
	// everything so it cannot outlive the test.
	step := func(i int, what string) {
		select {
		case <-stamps[i]:
		case <-time.After(30 * time.Second):
			for _, rel := range release {
				rel()
			}
			<-done
			t.Fatalf("the run never sent its %s to the grants node:\n%s", what, accessOut(out, errOut, err))
		}
	}
	for i, what := range []string{"roles step's read", "derive's read", "fold's read"} {
		step(i, what)
		release[i]()
	}
	for i, other := range others {
		step(3+2*i, "write of the flag list")
		// The other machine's own change lands while this run's write is in
		// flight, so the conditional write meets a node it did not read.
		db.Set(accesslib.PathNode, accessNodeDoc(projectGrant(other, accesslib.RoleOperator)))
		release[3+2*i]()
		step(4+2*i, "re-read after the lost race")
		release[4+2*i]()
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the run neither landed its list nor stopped:\n%s", accessOut(out, errOut, err))
	}
	all := accessOut(out, errOut, err)
	if err == nil || exitCode(t, err) != ExitUserError {
		t.Fatalf("a run that lost every race for the node did not stop: %v\n%s", err, all)
	}
	for _, w := range []string{"the grants node was written by another run every time this one tried", "--operator"} {
		if !strings.Contains(ErrorText(err), w) {
			t.Errorf("the refusal does not say %q:\n%s", w, all)
		}
	}
	// The node is the other writer's, and this run's list is in no list of it:
	// the run stopped instead of returning as if the roles had changed.
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:third@example.com (operator)"}) {
		t.Errorf("the node holds %v, want the last roster another run wrote:\n%s", got, all)
	}
}

// TestRevokeIsNotUndoneByAStaleDerive: the IAM lists are derived from the
// grants node and applied under Terraform's state lock, so a revoke landing
// between the read the roles step makes and the apply would be undone — the
// stale list would re-grant IAM to a member the node no longer holds (F29a).
// The derive the installation root takes for its own apply is what closes that
// window: it reads the node again, immediately before the tfvars its plan is
// built from, and the member revoked in between is not in the list that lands.
//
// The two reads are the fake database's, held apart so this test can land the
// revoke in exactly the window the finding names: the first hold is released
// with the node as it was (the roles step reads the member), and the revoke
// lands while the second read is in flight.
func TestRevokeIsNotUndoneByAStaleDerive(t *testing.T) {
	operators := []string{"user:ops@example.com", "user:revoked@example.com"}
	r, db := accessRig(t, operators, nil, operators, nil)
	db.Set(accesslib.PathNode, map[string]any{"grants": []map[string]any{
		{"user": "user:ops@example.com", "role": "operator", "scope": "project"},
		{"user": "user:revoked@example.com", "role": "operator", "scope": "project"},
	}})
	stampedStep, releaseStep := db.HoldNext(accesslib.PathNode)
	stampedApply, releaseApply := db.HoldNext(accesslib.PathNode)
	done := make(chan struct{})
	var out, errOut string
	var err error
	go func() {
		defer close(done)
		out, errOut, err = executeStdin(t, "", "update", "--yes")
	}()
	<-stampedStep
	releaseStep()
	select {
	case <-stampedApply:
	case <-time.After(10 * time.Second):
		// The run reached its apply without reading the node again: the list
		// it carries is the one the roles step read, and a revoke landing
		// after that read is re-granted by it.
		releaseApply()
		<-done
		t.Fatalf("the apply never read the grants node again: the lists it carries are the roles step's read\n%s", out+errOut)
	}
	// The other machine's `fugaro user rm` lands now, while this run's apply
	// is about to read the node for the lists it will carry.
	db.Set(accesslib.PathNode, map[string]any{"grants": []map[string]any{
		{"user": "user:ops@example.com", "role": "operator", "scope": "project"},
	}})
	releaseApply()
	<-done
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro update failed around the revoke: %v\n%s", err, all)
	}
	// The revoked member is in no list the run applied, and the run says so:
	// its own machine's copy named them.
	if got := members(r.tfvars(t)["launchers"]); !slices.Equal(got, []string{"user:ops@example.com"}) {
		t.Errorf("the plan's launchers are %v, want the revoked member gone: a list derived from a stale read re-grants the IAM that was just revoked\n%s", got, all)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config the run wrote does not load: %v\n%s", err, all)
	}
	if got := lc.Terraform.Operators; !slices.Equal(got, []string{"user:ops@example.com"}) {
		t.Errorf("the local config's operators are %v, want the revoked member gone:\n%s", got, all)
	}
	if !strings.Contains(all, "user:revoked@example.com") {
		t.Errorf("the run drops a member in silence:\n%s", all)
	}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{"user:ops@example.com (operator)"}) {
		t.Errorf("the apply wrote the node: %v — the derive reads it and never writes it\n%s", got, all)
	}
}

// TestAGrantTheInstallationNeverHeldIsNotCoveredByYes: the grants node is what
// an installation's roles are, and the one write it takes outside a run (a hand
// edit, `fugaro user`'s own door) is a grant nobody at this terminal made
// (update_access.go's heldAccess). The review screen does not list that member,
// so a plan granting them is not covered — and `--yes`, which an operator
// passes for the plan the review announced, is not an answer to this one: the
// step asks for the project's name typed at a real terminal, and a run that
// cannot type it applies nothing.
//
// The control is the same plan and the same node with the installation's own
// outputs holding the member: the run was told about them, and the same command
// applies the same plan.
func TestAGrantTheInstallationNeverHeldIsNotCoveredByYes(t *testing.T) {
	const held, extra = "user:ops@example.com", "user:new@example.com"
	node := accessNodeDoc(projectGrant(held, accesslib.RoleOperator), projectGrant(extra, accesslib.RoleOperator))
	t.Run("the node added a member", func(t *testing.T) {
		r, db := accessRig(t, nil, nil, []string{held}, nil)
		db.Set(accesslib.PathNode, node)
		updating(t, r)
		r.setPlanAt(t, "installation", grantChange("runs_launcher", extra, "roles/storage.objectUser", "create"))
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "update", "--yes")
		all := accessOut(out, errOut, err)
		if err == nil || !strings.Contains(all, "not confirmed") {
			t.Fatalf("a plan granting a member the review did not announce was confirmed by --yes: %v\n%s", err, all)
		}
		if calls := subcommands(r.calls(t)); slices.Contains(calls, "apply") {
			t.Errorf("a run whose grant was not confirmed applied the installation: %q\n%s", calls, all)
		}
		if !strings.Contains(all, "the grants node holds "+extra+", who this installation's own outputs do not") {
			t.Errorf("the run does not say which member the installation's outputs do not hold:\n%s", all)
		}
		if !strings.Contains(all, "granting "+extra+", which the review did not announce") {
			t.Errorf("the step does not ask about the grant itself:\n%s", all)
		}
		if !strings.Contains(all, "--yes does not confirm this one") {
			t.Errorf("the run does not say --yes is not an answer here:\n%s", all)
		}
	})
	t.Run("the installation held the member", func(t *testing.T) {
		r, db := accessRig(t, nil, nil, []string{held, extra}, nil)
		db.Set(accesslib.PathNode, node)
		updating(t, r)
		r.setPlanAt(t, "installation", grantChange("runs_launcher", extra, "roles/storage.objectUser", "create"))
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "update", "--yes")
		all := accessOut(out, errOut, err)
		if err != nil {
			t.Fatalf("a plan granting a member the installation's own outputs hold failed: %v\n%s", err, all)
		}
		if len(r.ran(t, "apply")) == 0 {
			t.Errorf("the run applied nothing:\n%s", all)
		}
		if strings.Contains(all, "which the review did not announce") || strings.Contains(all, "the grants node holds "+extra) {
			t.Errorf("a member the installation's own outputs hold was treated as the node's own addition:\n%s", all)
		}
	})
}

// TestTheReviewAnnouncesTheInstallationsMembersNotTheNodesAdditions: the review
// screen's lists and the cover the gate judges plans by are one answer
// (announced), and it is the members this run was told about — the
// installation's own outputs, and the lists the run named itself. A member only
// the node holds is in neither, which is what makes a plan granting them
// uncovered here and refused by a managed project's gate.
//
// A run that could not read the installation's outputs filters nothing and says
// so (accessAddsUnknown): it cannot tell the node's additions from the
// installation's members, and covering the node's whole roster is what it did
// before this read existed.
func TestTheReviewAnnouncesTheInstallationsMembersNotTheNodesAdditions(t *testing.T) {
	const held, extra, flagged = "user:ops@example.com", "user:new@example.com", "user:named@example.com"
	e := &initEngine{r: &initRun{o: &initOptions{}, accessHeld: []string{held, flagged}, accessHeldRead: true},
		lc: &localcfg.Config{Name: "aurora", Region: "us-east5"}}
	e.spec.Launchers = []string{extra, flagged, held}
	e.spec.Operators = []string{held}
	listed := e.cover().Listed
	if slices.Contains(listed, extra) {
		t.Errorf("the cover lists %s, a member only the grants node holds: one --yes passed for the plan the review announced would be the whole agreement about this grant\nlisted: %v", extra, listed)
	}
	for _, want := range []string{held, flagged} {
		if !slices.Contains(listed, want) {
			t.Errorf("the cover does not list %s, a member this run was told about\nlisted: %v", want, listed)
		}
	}
	// The derive is not what this changes: the plan the run applies carries the
	// node's lists, and it is the review that does not announce the addition.
	if got := e.spec.Launchers; !slices.Equal(got, []string{extra, flagged, held}) {
		t.Errorf("the run's launchers are %v, want the node's lists: the roles the node holds are what an installation's roles are", got)
	}
	// A run that could not read the outputs filters nothing.
	blind := &initEngine{r: &initRun{o: &initOptions{}}, lc: &localcfg.Config{Name: "aurora", Region: "us-east5"}}
	blind.spec = e.spec
	if listed := blind.cover().Listed; !slices.Contains(listed, extra) {
		t.Errorf("a run that could not read the installation's outputs does not cover the node's roster, which is all it knows of the installation\nlisted: %v", listed)
	}
}
