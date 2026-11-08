package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

func grantChange(name, member, role string, actions ...string) planChange {
	addr := `module.installation.google_storage_bucket_iam_member.` + name + `["` + member + `"]`
	ch := map[string]any{"actions": actions, "before": map[string]any{}, "after": nil}
	if actions[0] == "delete" {
		ch["before"] = map[string]any{"role": role, "member": member, "bucket": "fugaro-runs-proj-1234"}
	} else {
		ch["after"] = map[string]any{"role": role, "member": member, "bucket": "fugaro-runs-proj-1234", "condition": []any{}}
	}
	return planChange{"address": addr, "type": "google_storage_bucket_iam_member", "change": ch}
}

// The launchers' old objectAdmin grants are deleted without --allow-delete
// when their replacements are created, and named in a banner; an operator's
// grant, or a member on neither list, is still refused.
func TestInitHardeningAllowsOnlyTheLauncherGrantDeletes(t *testing.T) {
	const a, o = "user:a@example.com", "user:o@example.com"
	args := []string{"setup", "--yes", "--launcher", a, "--launcher", o, "--operator", o}

	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t,
		grantChange("runs", a, "roles/storage.objectAdmin", "delete"),
		grantChange("runs_reader", a, "roles/storage.objectViewer", "create"),
		grantChange("runs_launcher", a, "roles/storage.objectUser", "create"))
	out, _, err := executeStdin(t, "", args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "bucket access (0.7.0)") || !strings.Contains(out, "  "+a+"\n") {
		t.Errorf("no banner naming %s:\n%s", a, out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("the hardening plan was not applied")
	}

	// The operator's grant: refused, with the launcher/operator hint, and no
	// hardening banner (H7: a delete is never covered, so the banner must
	// never appear ahead of the guard's own refusal).
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, grantChange("runs", o, "roles/storage.objectAdmin", "delete"))
	out, _, err = executeStdin(t, "", args...)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `runs["`+o+`"]`) || !strings.Contains(err.Error(), "--launcher and --operator") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(out, "bucket access (0.7.0)") {
		t.Errorf("a refused plan printed the hardening banner:\n%s", out)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("an operator's grant delete was applied")
	}

	// A launcher's delete with no replacement: refused.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, grantChange("runs", a, "roles/storage.objectAdmin", "delete"))
	if _, _, err := executeStdin(t, "", args...); ExitCode(err) != ExitUserError {
		t.Fatalf("a delete without its replacements: exit %d, err %v", ExitCode(err), err)
	}
}

// A mixed plan — a valid hardening delete next to an unrelated delete that
// HardeningAllowDelete never names — is refused whole: the hardening
// allow-list only widens the guard for the addresses it actually returns, so
// the unrelated delete still needs its own --allow-delete, and nothing in
// the plan is applied until it is named or dropped. Because the plan does
// hold a real hardening delete (hardening is non-empty here, unlike the
// operator-refused case above), this is also the scenario that actually
// exercises banner-after-guard: a banner here, on a refused plan, would mean
// the banner was printed before the guard could refuse it.
func TestInitHardeningRefusesAMixedPlan(t *testing.T) {
	const a = "user:a@example.com"
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t,
		grantChange("runs", a, "roles/storage.objectAdmin", "delete"),
		grantChange("runs_reader", a, "roles/storage.objectViewer", "create"),
		grantChange("runs_launcher", a, "roles/storage.objectUser", "create"),
		grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	out, _, err := executeStdin(t, "", "setup", "--yes", "--launcher", a)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `state["`+a+`"]`) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(out, "bucket access (0.7.0)") {
		t.Errorf("a refused mixed plan printed the hardening banner:\n%s", out)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a mixed plan with an unrelated delete was applied")
	}
}

// launcherReplaceChange is the upgrade's replace of member's runs_launcher
// grant, from one earlier form of the launchers' condition to the current one,
// in the shape `terraform show -json` gives it.
func launcherReplaceChange(bucket, member, beforeTitle, beforeExpr string) planChange {
	side := func(title, expr string) map[string]any {
		return map[string]any{"role": "roles/storage.objectUser", "member": member, "bucket": bucket,
			"condition": []any{map[string]any{"title": title, "expression": expr, "description": ""}}}
	}
	return planChange{"address": `module.installation.google_storage_bucket_iam_member.runs_launcher["` + member + `"]`,
		"type": "google_storage_bucket_iam_member",
		"change": map[string]any{"actions": []string{"delete", "create"},
			"before": side(beforeTitle, beforeExpr),
			"after":  side(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(bucket))}}
}

// Upgrading an installation one release behind this one: each non-operator
// launcher's runs_launcher grant is replaced (the condition gained issues/,
// and a condition is ForceNew), and init applies it with no --allow-delete,
// naming the launchers. A replace from any other condition is still refused.
func TestInitAdmitsTheLauncherGrantUpgrade(t *testing.T) {
	const a, o = "user:a@example.com", "user:o@example.com"
	bucket := "fugaro-runs-" + initProject
	args := []string{"setup", "--yes", "--launcher", a, "--launcher", o, "--operator", o}

	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, launcherReplaceChange(bucket, a, gcp.LegacyLauncherBucketConditionTitle, gcp.LegacyLauncherBucketCondition(bucket)))
	out, _, err := executeStdin(t, "", args...)
	if err != nil {
		t.Fatalf("the upgrade plan is refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bucket access (0.23.0)") || !strings.Contains(out, "  "+a+"\n") {
		t.Errorf("no banner naming %s:\n%s", a, out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("the upgrade plan was not applied")
	}

	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, launcherReplaceChange(bucket, a, "someone-else", `resource.name.startsWith("projects/_/buckets/`+bucket+`/objects/")`))
	out, _, err = executeStdin(t, "", args...)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `runs_launcher["`+a+`"]`) {
		t.Fatalf("a replace from another condition: exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(out, "bucket access (0.23.0)") {
		t.Errorf("a refused plan printed the upgrade banner:\n%s", out)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a replace from another condition was applied")
	}
}

// TestInstallationAtThePreviousVersionConvergesIssuesGrant: the HARD RULE's
// own test for this grant. An installation whose launchers carry an earlier
// form of the condition — the runs/ and tokens/ one every release from 0.9.0
// wrote, or the pre-0.9.0 runs/-only one — ends a plain `fugaro update` with
// the grant replaced by the runs/, tokens/ and issues/ one (D1727), applied
// with no --allow-delete and named in the banner. The update is the whole
// upgrade: the launchers file their issues from that installation afterwards,
// and no second command is owed.
func TestInstallationAtThePreviousVersionConvergesIssuesGrant(t *testing.T) {
	const a, o = "user:a@example.com", "user:o@example.com"
	bucket := "fugaro-runs-" + initProject
	for _, tc := range []struct{ name, title, expr string }{
		{"the runs/ and tokens/ condition", gcp.LegacyLauncherBucketConditionTitle, gcp.LegacyLauncherBucketCondition(bucket)},
		{"the pre-0.9.0 runs/-only condition", gcp.RunsOnlyLauncherBucketConditionTitle, gcp.RunsOnlyLauncherBucketCondition(bucket)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateRig(t)
			// The installation's own members, as the local config records
			// them: the guard judges the replace against the launcher list
			// this run holds, and an installation's list is what its config
			// says (or the outputs of a run that adopted it).
			r.appendConfig(t, "terraform: { launchers: [\""+a+"\", \""+o+"\"], operators: [\""+o+"\"] }\n")
			r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"launchers": []string{a, o}, "operators": []string{o}})}
			r.script["plan@installation"] = map[string]any{"exit": 2}
			r.script["show@installation"] = map[string]any{"stdout": planJSON(t, launcherReplaceChange(bucket, a, tc.title, tc.expr))}
			r.save(t)
			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err != nil {
				t.Fatalf("fugaro update left an installation at the previous version behind: %v\n%s%s", err, out, errOut)
			}
			all := out + errOut
			if !strings.Contains(all, "bucket access (0.23.0)") || !strings.Contains(all, "  "+a+"\n") {
				t.Errorf("the update does not name the launcher whose grant it replaces:\n%s", all)
			}
			if len(r.ran(t, "apply")) != 1 {
				t.Fatalf("applies = %d, want the one the converge makes\n%s%s", len(r.ran(t, "apply")), out, errOut)
			}
			// The state the run wrote is the current condition, which is what
			// the apply above makes real: the installation holds the issues/
			// grant once it ends.
			cond, _ := r.tfvars(t)["launcher_bucket_condition"].(map[string]any)
			if cond["title"] != gcp.LauncherBucketConditionTitle || cond["expression"] != gcp.LauncherBucketCondition(bucket) {
				t.Errorf("launcher_bucket_condition = %v, want the condition naming issues/", cond)
			}
		})
	}
}
