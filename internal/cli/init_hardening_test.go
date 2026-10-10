package cli

import (
	"strings"
	"testing"
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
	args := []string{"init", "--yes", "--launcher", a, "--launcher", o, "--operator", o}

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
	out, _, err := executeStdin(t, "", "init", "--yes", "--launcher", a)
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
