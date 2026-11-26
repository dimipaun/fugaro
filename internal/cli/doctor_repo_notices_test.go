package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// doctor_repo_notices_test.go holds U6b of docs/update-from-anywhere: the two
// checks that replace doctor's `plugin-pin`, which meant this checkout, with a
// question about the project's repositories — and the text of each, which is
// `fugaro update`'s own line for the same value (U6a, R15).

// stubRepoNotices stands one repository list in for the project's read (R21):
// what these checks are about is the files a repository holds, and the read
// itself is C1's.
func stubRepoNotices(t *testing.T, views ...repoView) {
	t.Helper()
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return views, nil
	})
}

// noticeView is one enrolled repository's default branch as the reader returns
// it: the repository, and the bytes of the files the notices are judged from.
// The files are what a view holds beside its parsed config, which is where the
// two dead values are read from: a view's own YAML has already lost the word
// the run's reader splices out (F22).
func noticeView(repo string, files map[string]string) repoView {
	v := repoView{Repo: repo, Branch: "main", Head: "0000000000000000000000000000000000000000"}
	for name, body := range files {
		if v.Files == nil {
			v.Files = map[string][]byte{}
		}
		v.Files[name] = []byte(body)
	}
	return v
}

// TestDoctorRepoPinMatchesUpdate: a repository whose default branch still
// carries the plugin pin an older fugaro wrote is one row, and the sentence on
// it is the line `fugaro update` prints for the same repository (R15: the two
// doors say the same thing about the same value, spec acceptance 5). What the
// row adds is the fix: the line to delete, because nothing takes the pin out
// for the reader any more (U6a, D3340).
func TestDoctorRepoPinMatchesUpdate(t *testing.T) {
	newDoctorRig(t)
	views := []repoView{noticeView("acme/app", map[string]string{repoNoticePinFile: wiredAt("v0.2.0")})}
	stubRepoNotices(t, views...)

	c := doctorCheckEmitted(t, repoPluginPinCheckID)
	want := repoNoticeText(noticeRepoLines(views)[0])
	if c.Problem != want {
		t.Errorf("doctor's row says %q, and `fugaro update` says %q for the same pin", c.Problem, want)
	}
	if !strings.Contains(want, repoNoticePinEdit) {
		t.Fatalf("the shared line is not U6a's edit: %q", want)
	}
	if got := doctorClassOf(c); got != doctorClassActionable {
		t.Errorf("the pin is %q, want %q: the repository's own edit is what clears it", got, doctorClassActionable)
	}
	if !strings.Contains(c.Fix, "delete") || !strings.Contains(c.Fix, repoNoticePinFile) {
		t.Errorf("the fix does not name the file and the line to delete: %q", c.Fix)
	}
}

// TestDoctorRepoBudgetMatchesUpdate is the same for the word that is not a
// budget mode any more: one row, U6a's sentence, and the line to delete in the
// fix.
func TestDoctorRepoBudgetMatchesUpdate(t *testing.T) {
	newDoctorRig(t)
	views := []repoView{noticeView("acme/app", map[string]string{repoNoticeYAMLFile: "version: 1\nproject: aurora\n" + offRepoYAML})}
	stubRepoNotices(t, views...)

	c := doctorCheckEmitted(t, repoBudgetModeCheckID)
	want := repoNoticeText(noticeRepoLines(views)[0])
	if c.Problem != want {
		t.Errorf("doctor's row says %q, and `fugaro update` says %q for the same word", c.Problem, want)
	}
	if !strings.Contains(want, repoNoticeBudgetEdit) {
		t.Fatalf("the shared line is not U6a's edit: %q", want)
	}
	if got := doctorClassOf(c); got != doctorClassActionable {
		t.Errorf("budget.mode: off is %q, want %q: the repository's own edit is what clears it", got, doctorClassActionable)
	}
	if !strings.Contains(c.Fix, "budget.mode") || !strings.Contains(c.Fix, repoNoticeYAMLFile) {
		t.Errorf("the fix does not name the file and the line to delete: %q", c.Fix)
	}
}

// TestDoctorRepoNoticesOfflineMakeNoCall: both checks are answered from each
// enrolled repository's default branch, which is a remote read, so `--offline`
// and `--plugin` record them as not read and make no call at all — the reader
// is stood in for by one that fails the test if it is reached (R15).
func TestDoctorRepoNoticesOfflineMakeNoCall(t *testing.T) {
	newDoctorRig(t)
	called := false
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		called = true
		return nil, nil
	})

	for _, flag := range []string{"--offline", "--plugin"} {
		out, _, _ := execute(t, "doctor", flag, "--json", "--dir", t.TempDir())
		var o doctorOutput
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatalf("doctor %s --json is not JSON: %v\n%s", flag, err, out)
		}
		for _, id := range []string{repoPluginPinCheckID, repoBudgetModeCheckID} {
			c, ok := doctorCheckByID(o.Checks, id)
			if !ok {
				t.Fatalf("doctor %s emitted no %s check: a reader cannot tell "+
					"\"read and found nothing\" from \"not read\":\n%s", flag, id, out)
			}
			if !c.Skipped || !strings.Contains(c.Problem, "not read") {
				t.Errorf("doctor %s reports %s as read: %+v", flag, id, c)
			}
			if !strings.Contains(c.Problem, "offline") || c.Fix != doctorOfflineAsk {
				t.Errorf("doctor %s's %s skip is not the offline one: %+v", flag, id, c)
			}
		}
		if called {
			t.Fatalf("doctor %s read the project's repositories", flag)
		}
	}
}

// TestDoctorRetiresCheckoutChecks: `plugin-pin` asked whether the checkout's
// own pin named this binary's release, and it is gone — the pin is a dead value
// on a repository now, not a version to compare (R15). What a full run says
// about a checkout carrying one is nothing at all: no plugin-pin row, and no
// plugin-wiring row either, because a pin that is fugaro's own release tag is
// neither foreign nor unpinned.
func TestDoctorRetiresCheckoutChecks(t *testing.T) {
	newDoctorRig(t)
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))

	for _, id := range []string{"plugin-pin", "plugin-wiring"} {
		if c, ok := doctorCheckByID(doctorChecks(t, root).Checks, id); ok {
			t.Errorf("doctor still emits the retired %s check for a pinned checkout: %+v", id, c)
		}
	}
	// The retired check's ID answers nothing in the tables the renderer reads
	// either: an ID no check emits would otherwise be a class and a short form
	// waiting for a line that never comes.
	if slices.Contains(doctorUpdatePrefixes, "plugin-pin") {
		t.Error("the retired plugin-pin is still in the update class's prefixes")
	}
	if _, ok := doctorMeasureIDs["plugin-pin"]; ok {
		t.Error("the retired plugin-pin is still filed as a measurement")
	}
	if _, ok := doctorShorts["plugin-pin"]; ok {
		t.Error("the retired plugin-pin still has a short form")
	}
}
