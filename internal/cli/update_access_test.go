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
