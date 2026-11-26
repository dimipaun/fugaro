package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// repo_notices.go is U6a of docs/update-from-anywhere: the two values a release
// before this one left on a repository's default branch — the plugin pin an
// older fugaro wrote into .claude/settings.json, and budget.mode: off in
// fugaro.yaml — and the one line each gets from `update` (D3340, R15).
//
// They are notices and not writes. `update` reads every enrolled repository's
// default branch and writes no working tree (spec §Guiding principles), so a
// leftover pin or a word that is not a budget mode any more is a line naming
// the file and the exact edit, and the value is the repository's own change to
// make. `doctor` prints the same text (U6b), which is why the text lives here
// and not in the step that prints it.

// repoNoticePinFile is the repository-relative path an older fugaro's plugin
// pin is in, and the one extra file this step asks the reader for.
const repoNoticePinFile = ".claude/settings.json"

// repoNoticeYAMLFile is the file the word that is not a budget mode any more is
// in.
const repoNoticeYAMLFile = "fugaro.yaml"

// repoNoticeExtra are the two files the notices are judged from, which the
// run's own read of the list asks the reader for (e.repoExtras): the run reads
// the repositories once and every later call gets the views that first read
// took (C1 §Memoized per run), so the paths have to be on that read.
var repoNoticeExtra = []string{repoNoticePinFile, repoNoticeYAMLFile}

// The two exact edits the notices name, and the same text in `update` and in
// `doctor` (D3340, R15): what a person has to do, and why the value is dead.
const (
	repoNoticePinEdit = "delete the " + pluginwire.Marketplace + " entry from extraKnownMarketplaces and the " +
		pluginwire.PluginID + " entry from enabledPlugins: an older fugaro pinned the plugin here, and nothing writes a pin any more"
	repoNoticeBudgetEdit = "delete the budget.mode line: it is not a budget mode any more (there is always a budget, and a mode left unset is observe)"
)

// repoNoticesStep is the name the step after the loop reports its lines under
// (R12; U13 carries them into the `notices` member).
const repoNoticesStep = "notices"

// repoNotice is one dead value an enrolled repository's default branch still
// holds: the file it is in, and the exact edit that takes it out.
type repoNotice struct {
	File   string
	Detail string
}

// repoNotices is what one view's default branch still holds: the plugin pin in
// its .claude/settings.json, and budget.mode: off in its fugaro.yaml. A
// repository the reader could not read earns none — there is no file to judge —
// and so does a file carrying neither value. A pin that is not ours (another
// marketplace's, a fork's, one pinned to something other than a release tag, or
// one the repository disabled) is left exactly as it is: pluginwire's own
// refusals say it is not the pin fugaro left, and that is not a notice.
//
// Both files are judged from the bytes the step asked the reader for, and the
// fugaro.yaml one is asked for as an extra file rather than read off v.YAML:
// the word is what the run's own reader splices out before parsing (F22), so a
// view's YAML has already lost it, and the parse of a file that still says it
// refuses the file whole. What the notice is about is the branch as the
// repository committed it, which is the only thing that tells the owner there
// is a line to delete.
func repoNotices(v repoView) []repoNotice {
	var out []repoNotice
	if settings, ok := v.Files[repoNoticePinFile]; ok {
		if rem, err := pluginwire.PlanRemovalBytes(repoNoticePinFile, settings); err == nil && rem.Changed {
			out = append(out, repoNotice{File: repoNoticePinFile, Detail: repoNoticePinEdit})
		}
	}
	yaml := v.YAML
	if raw, ok := v.Files[repoNoticeYAMLFile]; ok {
		yaml = raw
	}
	if _, off := localcfg.RemovedBudgetMode(yaml); off {
		out = append(out, repoNotice{File: repoNoticeYAMLFile, Detail: repoNoticeBudgetEdit})
	}
	return out
}

// noticeRepoLines is the notices of every repository as the lines the run
// reports, one per repository per notice, built through newRepoLine (C1): the
// repository-controlled detail is made safe and bounded there. The state is
// needs-you, because only the owner makes the edit and nothing here writes one.
func noticeRepoLines(views []repoView) []initflow.RepoLine {
	var lines []initflow.RepoLine
	for _, v := range views {
		for _, n := range repoNotices(v) {
			lines = append(lines, newRepoLine(v.Repo, initflow.RepoNeedsYou, n.File+": "+n.Detail))
		}
	}
	return lines
}

// noticeRepoValues is the step `update` runs after the stage loop: it reads
// every enrolled repository's default branch — the .claude/settings.json and
// the fugaro.yaml the run's own read asked the reader for beside each view's
// own (e.repoExtras) — and reports one line per dead value it finds (D3340).
//
// It writes nothing, which is the change U6a makes: the plugin stage's diff
// offer and the budget.mode line's removal are gone, and the value is the
// repository's own change to make. So there is no working tree to read and no
// confirmation to gate, and the step runs inside a checkout and outside one
// alike — the whole point of reading every repository's default branch.
//
// It is the update's step alone: `fugaro setup` creates an installation rather
// than moving one, and both values are ones only a release behind this one
// wrote. It never fails the update (CLAUDE.md's HARD RULE): a repository the
// reader could not read earns no line here, because the reader's own unread
// line is where that is said.
func (e *initEngine) noticeRepoValues(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name {
		return
	}
	views, _ := e.repos(ctx)
	// KS20's finding rides this step (claude_gate.go): the repositories this
	// release's credentials service stops, one line each, off the same read of
	// the enrolled list. It is here rather than in the step list because this
	// step is the same thing — one pass, one line per repository, needs-you —
	// and because that list is KS12's file in this plan's own order.
	e.noticeClaudeGate(views)
	lines := noticeRepoLines(views)
	if len(lines) == 0 {
		return
	}
	e.reportRepos(repoNoticesStep, lines)
	for _, l := range lines {
		fmt.Fprintf(r.w, "%s: %s: %s\n", l.Repo, l.State, l.Detail)
	}
}
