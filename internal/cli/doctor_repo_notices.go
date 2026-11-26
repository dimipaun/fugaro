package cli

import (
	"context"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctor_repo_notices.go holds doctor's two repository checks (U6b of
// docs/update-from-anywhere, R15): the plugin pin a repository's default branch
// still carries, and the budget.mode word it still says. Both values are dead —
// nothing writes a pin any more (D949) and off is not a budget mode any more
// (D3022) — so each is a line naming the file and the exact edit, which is U6a's
// own text (repo_notices.go) and the same line `fugaro update` reports.
//
// They replace doctor's `plugin-pin` check, which meant "this checkout": the
// question is the project's repositories now, and the answer comes from the same
// read the update's stages use (C1's free function, R16). The project-wide
// `budget-mode` check is not one of these — it reads the budget database's own
// mode, not a value in a repository — and it stays where it is.

// repoPluginPinCheckID and repoBudgetModeCheckID are doctor's two repository
// checks, one per file a repository can still hold the dead value in.
const (
	repoPluginPinCheckID  = "repo-plugin-pin"
	repoBudgetModeCheckID = "repo-budget-mode"
)

// doctorRepoNoticeChecks reads every enrolled repository at its default branch
// and reports one check per dead value it finds there.
//
// The read is C1's own (enrolledRepos, R16), so doctor asks about the
// repositories the update's stages ask about, in the same order, out of the same
// views, and the two doors say the same thing about the same value (spec
// acceptance 5). Nothing is written: a leftover pin and a dead word are the
// repository's own change to make, and doctor measures.
//
// The checks are warnings and they are not the update class: `fugaro update`
// does not take these values out any more (U6a), so a line sending the reader to
// it would send them to a command that leaves the warning standing. The edit
// itself, named by the fix, is what clears it.
func doctorRepoNoticeChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	views, _ := enrolledRepos(ctx, enrolledIn{LC: lc, ExtraPaths: repoNoticeExtra})
	return doctorRepoNotices(views)
}

// doctorRepoNotices is the checks for a list the caller already holds, which is
// what the tests read: the read itself is C1's and is not about git here.
//
// One check per affected repository and per value, all under the one ID: the ID
// alone cannot carry which repository the line is about, and the default view
// prints the first sentence of an item and clips it, so a single row for the
// whole list would hide every repository but the first. doctor emits the same
// shape for spend-drift, once per drifting model, for the same reason.
//
// A repository the read could not answer for earns nothing here: there is no
// file to judge, and the reader's own line is where an unread repository is
// said (the update's stages report one each, and this check has no opinion about
// a value it could not read).
func doctorRepoNotices(views []repoView) []doctorCheck {
	var out []doctorCheck
	for _, v := range views {
		for _, n := range repoNotices(v) {
			out = append(out, doctorCheck{ID: repoNoticeCheckID(n), Severity: "warning",
				Problem: repoNoticeText(repoNoticeLine(v.Repo, n)),
				Fix:     repoNoticeFix(n)})
		}
	}
	return out
}

// repoNoticeCheckID is the check a notice is reported under, by the file it is
// in: the two dead values are the two checks.
func repoNoticeCheckID(n repoNotice) string {
	if n.File == repoNoticePinFile {
		return repoPluginPinCheckID
	}
	return repoBudgetModeCheckID
}

// repoNoticeFix is the line to delete, which is what the reader does about a
// notice. The file is already on the check's own line — that line is U6a's text
// — so the fix names the entries to take out of it, in the shortest form that
// does: U6a's own edit sentence says what happened and why the value is dead as
// well, and the default view would clip it, while --all and --json carry both.
func repoNoticeFix(n repoNotice) string {
	if n.File == repoNoticePinFile {
		return "delete extraKnownMarketplaces." + pluginwire.Marketplace + " and enabledPlugins." +
			pluginwire.PluginID + " from " + repoNoticePinFile
	}
	return "delete the budget.mode line from " + repoNoticeYAMLFile
}
