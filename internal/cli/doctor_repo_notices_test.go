package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// doctor_repo_notices_test.go is U6b's tests: doctor's two per-repository
// notice checks, `repo-plugin-pin` and `repo-budget-mode` (R15). Each reads
// every enrolled repository through C1's free function and prints U6a's own
// text (repo_notices.go), so the line a person reads here is the line `fugaro
// update` prints about the same value — and the check that meant "this
// checkout" is gone.

// repoNoticeDoctorChecks runs the whole command in a checkout the test has
// chdir'd into -- the stand-in reader C1 still carries reads the working
// directory -- and answers its checks by id.
func repoNoticeDoctorChecks(t *testing.T, root string, args ...string) map[string]doctorCheck {
	t.Helper()
	argv := append([]string{"doctor", "--json", "--dir", root}, args...)
	out, _, err := execute(t, argv...)
	if err != nil && out == "" {
		t.Fatalf("%v", err)
	}
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	m := map[string]doctorCheck{}
	for _, c := range o.Checks {
		m[c.ID] = c
	}
	return m
}

// repoNoticeCheckout is a checkout whose default branch is an older release's:
// refreshYAML with yaml appended, and the plugin pin an older fugaro wrote
// into .claude/settings.json when withPin is true. The caller gets a rig as
// well, because doctor reports nothing at all without an installation.
func repoNoticeCheckout(t *testing.T, yaml string, withPin bool) (root string, checks map[string]doctorCheck) {
	t.Helper()
	_ = newDoctorRig(t)
	root = repoCheckout(t, githubOrigin, yaml)
	t.Chdir(root)
	if withPin {
		checkoutFile(t, filepath.Join(root, repoNoticePinFile), pinnedAmongOthers)
	}
	return root, repoNoticeDoctorChecks(t, root)
}

// TestDoctorRepoPinMatchesUpdate: a repository's default branch still holding
// the plugin pin an older fugaro wrote earns one row from doctor, and the row
// carries the exact edit `fugaro update` reports for the same value (U6a's
// repoNoticePinEdit, D3340) beside the repository and the file it is in, with
// a fix naming what to delete. Nothing is written.
func TestDoctorRepoPinMatchesUpdate(t *testing.T) {
	root, checks := repoNoticeCheckout(t, refreshYAML, true)

	c, ok := checks[repoPluginPinCheckID]
	if !ok {
		t.Fatalf("doctor reports no %s on a checkout whose default branch holds an older fugaro's pin: %+v", repoPluginPinCheckID, checks)
	}
	if c.OK {
		t.Fatalf("%s = %+v, want a finding: the pin is dead and the file still says it", repoPluginPinCheckID, c)
	}
	if c.Severity != "warning" || c.Class != doctorClassActionable {
		t.Errorf("%s is %q/%q, want a warning in the actionable class: only the owner makes this edit", repoPluginPinCheckID, c.Severity, c.Class)
	}
	// The text `update` prints for the same value, verbatim: the file and
	// U6a's own edit, under the repository they are about.
	want := "acme/app: " + repoNoticePinFile + ": " + repoNoticePinEdit
	if !strings.Contains(c.Problem, want) {
		t.Errorf("%s does not carry the line `fugaro update` prints, %q:\n%s", repoPluginPinCheckID, want, c.Problem)
	}
	if !strings.Contains(c.Problem, "1 repository") {
		t.Errorf("%s does not count the repositories it found the pin in:\n%s", repoPluginPinCheckID, c.Problem)
	}
	for _, what := range []string{"extraKnownMarketplaces", "enabledPlugins"} {
		if !strings.Contains(c.Fix, what) {
			t.Errorf("%s's fix does not name the entry to delete (%s): %q", repoPluginPinCheckID, what, c.Fix)
		}
	}
	// The value is the repository's own change to make: doctor reads and
	// writes no working tree (spec §Guiding principles).
	if got := convergeRead(t, filepath.Join(root, repoNoticePinFile)); got != pinnedAmongOthers {
		t.Errorf("%s after doctor:\n%s\nwant the file left exactly as the release behind wrote it", repoNoticePinFile, got)
	}
}

// TestDoctorRepoBudgetMatchesUpdate: the same for the word that is not a budget
// mode any more in a repository's fugaro.yaml. The word is read out of the
// file's own bytes (localcfg.RemovedBudgetMode, F22, U6a's reader), so doctor
// reports it rather than refusing the file it is in.
func TestDoctorRepoBudgetMatchesUpdate(t *testing.T) {
	root, checks := repoNoticeCheckout(t, refreshYAML+offRepoYAML, false)

	c, ok := checks[repoBudgetModeCheckID]
	if !ok {
		t.Fatalf("doctor reports no %s on a checkout whose default branch still says the word: %+v", repoBudgetModeCheckID, checks)
	}
	if c.OK {
		t.Fatalf("%s = %+v, want a finding: off is not a budget mode any more", repoBudgetModeCheckID, c)
	}
	if c.Severity != "warning" || c.Class != doctorClassActionable {
		t.Errorf("%s is %q/%q, want a warning in the actionable class", repoBudgetModeCheckID, c.Severity, c.Class)
	}
	want := "acme/app: " + repoNoticeYAMLFile + ": " + repoNoticeBudgetEdit
	if !strings.Contains(c.Problem, want) {
		t.Errorf("%s does not carry the line `fugaro update` prints, %q:\n%s", repoBudgetModeCheckID, want, c.Problem)
	}
	if !strings.Contains(c.Fix, "budget.mode") {
		t.Errorf("%s's fix does not name the line to delete: %q", repoBudgetModeCheckID, c.Fix)
	}
	if got := convergeRead(t, filepath.Join(root, repoNoticeYAMLFile)); got != refreshYAML+offRepoYAML {
		t.Errorf("%s after doctor:\n%s\nwant it left as the repository committed it", repoNoticeYAMLFile, got)
	}
	// A checkout with neither value reports neither: doctor's rows are about
	// what a file says and never about the check existing.
	if _, clean := repoNoticeCheckout(t, refreshYAML, false); clean[repoBudgetModeCheckID].ID != "" || clean[repoPluginPinCheckID].ID != "" {
		t.Errorf("a checkout holding neither value reports %+v / %+v", clean[repoPluginPinCheckID], clean[repoBudgetModeCheckID])
	}
}

// TestDoctorRepoNoticesOfflineMakeNoCall: the two modes that read no
// repository -- `--offline` and `--plugin` -- report both checks as skipped in
// the same words ("not read (offline)", R15) and reach no repository at all.
// The reader stands in for the real one and fails the test if it is called,
// which is the only way to prove a mode made no call: a row that is absent, or
// one that says ok, would be the silent pass D946 exists to prevent.
func TestDoctorRepoNoticesOfflineMakeNoCall(t *testing.T) {
	_, root := doctorCleanRig(t, nil)
	t.Chdir(root)

	old := enrolledReposImpl
	enrolledReposImpl = func(context.Context, enrolledIn) ([]repoView, []string) {
		t.Error("a mode that promises to read no repository read the enrolled list")
		return nil, nil
	}
	t.Cleanup(func() { enrolledReposImpl = old })

	for _, mode := range []string{"--offline", "--plugin"} {
		checks := repoNoticeDoctorChecks(t, root, mode)
		for _, id := range []string{repoPluginPinCheckID, repoBudgetModeCheckID} {
			c, ok := checks[id]
			if !ok {
				t.Fatalf("%s reports no %s: a mode that read nothing says so rather than reporting ok (D946)", mode, id)
			}
			if !c.Skipped || c.OK {
				t.Errorf("%s %s = %+v, want skipped and not ok", mode, id, c)
			}
			if !strings.Contains(c.Problem, repoNoticeNotRead) {
				t.Errorf("%s %s's problem is %q, want it to say %q", mode, id, c.Problem, repoNoticeNotRead)
			}
			if c.Severity != "info" {
				t.Errorf("%s %s is at severity %q: a skip fails nothing, with or without --strict", mode, id, c.Severity)
			}
		}
	}
	// A skip fails nothing, with or without --strict (D946): a clean
	// installation whose two checks were not read is not a failing one.
	if _, _, err := execute(t, "doctor", "--offline", "--strict", "--json", "--dir", root); err != nil {
		t.Fatalf("--offline --strict failed on an installation with nothing wrong: %v", err)
	}
}

// TestDoctorRetiresCheckoutChecks: `plugin-pin` meant "this checkout's
// .claude/settings.json", and reading every enrolled repository changes what
// the check is about, so it is retired rather than reused (R15): the same
// checkout reports the pin under `repo-plugin-pin` and has no `plugin-pin` row
// at all. The ID is not replaced by a second reading of the checkout, either —
// one value gets one row, in U6a's text.
//
// The other check R15 names, `budget-mode`, is not retired here and is
// deliberately left alone. The `budget-mode` ID in this tree is always-a-budget
// T16's line about the project-wide mode read from the budget database
// (doctor_budget_mode.go) — not this checkout — and the checkout's own reading
// of the word is `budget-stops` (AB8b, R8, budget_stops.go), which another
// plan owns and pins. Retiring either would take a check out of a plan that
// still needs it; what U6b adds is the per-repository reading, in U6a's text,
// beside them. The duplication this leaves while C1's stand-in reads the
// working directory alone is reported in the pull request.
func TestDoctorRetiresCheckoutChecks(t *testing.T) {
	_, checks := repoNoticeCheckout(t, refreshYAML+offRepoYAML, true)

	if c, ok := checks["plugin-pin"]; ok {
		t.Errorf("doctor still reports the checkout's own plugin-pin (%+v): it is retired (R15)", c)
	}
	if _, ok := checks[repoPluginPinCheckID]; !ok {
		t.Fatalf("doctor reports the retired ID and not %s: %+v", repoPluginPinCheckID, checks)
	}
	// The ID R15 also names is another plan's, and stays (the comment above):
	// the checkout's own reading of the word is `budget-stops` (AB8b, R8), and
	// it is still reported beside the new per-repository row — which is the
	// duplication U6b leaves behind while C1's stand-in reads the working
	// directory alone, and reports in its pull request.
	if _, ok := checks[budgetStopsCheckID]; !ok {
		t.Errorf("doctor no longer reports %s for the checkout that says the word: it is AB8b's check and U6b does not retire it", budgetStopsCheckID)
	}
	if _, ok := checks[repoBudgetModeCheckID]; !ok {
		t.Errorf("doctor reports the retired pair and not %s: %+v", repoBudgetModeCheckID, checks)
	}
}
