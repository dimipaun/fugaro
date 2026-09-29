package cli

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// checkStaleAfter is how long the daily check may go without writing
// check.json before ls says it isn't running.
const checkStaleAfter = 48 * time.Hour

// imageWarnings are the lines ls prints before its table: for each listed
// repository of the local config, and each of its workflows, a failed image
// rebuild, a failed check, or a daily check that isn't running. It reads two
// small objects per workflow. A workflow whose objects can't be read gets a
// warning of its own: these lines are advice, and never fail the listing.
func imageWarnings(ctx context.Context, env *cloudEnv, slugs []string, now time.Time) []string {
	warnings := []string{}
	listed := map[string]bool{}
	for _, s := range slugs {
		listed[s] = true
	}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		checkout := checkoutOf(ctx, repo)
		slug, err := env.repoSlug(repo, checkout)
		if err != nil || !listed[slug] {
			continue // ls has already said why it skipped a repository
		}
		workflows := env.lc.Repos[repo].Workflows
		if len(workflows) == 0 {
			if cfg := checkout(); cfg != nil {
				workflows = slices.Sorted(maps.Keys(cfg.Workflows))
			}
		}
		for _, w := range workflows {
			st, err := imagecheck.ReadStatus(ctx, env.bucket, slug, w, now)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: %s %s: the image status can't be read: %s", repo, w, oneLine(err.Error())))
				continue
			}
			warnings = append(warnings, workflowWarnings(repo, w, st, dailyCheck(checkout(), w), now)...)
		}
	}
	return warnings
}

// dailyCheck reports whether workflow w is on the daily check. Without the
// checkout's fugaro.yaml there is nothing saying otherwise, and daily is
// the default.
func dailyCheck(cfg *config.Config, w string) bool {
	if cfg == nil {
		return true
	}
	wf, ok := cfg.Workflows[w]
	return !ok || wf.Rebuild.Defaults().Check == "daily"
}

// workflowWarnings are the warnings for one workflow's image and check.
func workflowWarnings(repo, w string, st imagecheck.Status, daily bool, now time.Time) []string {
	var out []string
	prefix := fmt.Sprintf("warning: %s %s: ", repo, w)
	img, cs := st.Image, st.Check
	if cs != nil {
		if line := rebuildWarning(img, cs); line != "" {
			out = append(out, prefix+line)
		}
		if daily && now.Sub(cs.CheckedAt) > checkStaleAfter {
			out = append(out, prefix+"the daily image check hasn't run since "+day(cs.CheckedAt)+".")
		}
	} else if daily && img != nil && now.Sub(img.BuiltAt) > checkStaleAfter {
		// A missing check.json is normal while the schedule is paused, and
		// ls can't tell: it speaks only once the image is old enough that a
		// running check would have written one.
		out = append(out, prefix+"the daily image check has never run (the image was built "+day(img.BuiltAt)+").")
	}
	return out
}

// rebuildWarning is what a check.json says about a rebuild that failed, or
// a check that did, or "".
//
// check.json keeps last_build_status: FAILURE after a manual fix until the
// check next submits a build, so a failure counts while a rebuild is due
// (the decision says so), or when the failed build is newer than the
// image's record: the record then predates the failure and is what runs
// still use. A record newer than the failed build is a fix.
func rebuildWarning(img *imagecheck.ImageStatus, cs *imagecheck.CheckState) string {
	if cs.Decision == imagecheck.CheckFailed {
		msg := "the daily image check failed on " + day(cs.CheckedAt)
		if cs.Error != "" {
			msg += ": " + oneLine(cs.Error)
		}
		return msg + ". See fugaro image status."
	}
	failed := imagecheck.FailedBuild(cs.LastBuildStatus)
	due := cs.Decision == imagecheck.Rebuild || cs.Decision == imagecheck.RebuildFailedLast
	at := cs.CheckedAt
	if cs.LastBuildAt != nil {
		at = *cs.LastBuildAt
	}
	superseded := failed && cs.LastBuildAt != nil && img != nil && img.BuiltAt.After(*cs.LastBuildAt)
	var what string
	switch {
	case failed && !superseded && (due || cs.LastBuildAt != nil):
		what = fmt.Sprintf("the image rebuild of %s failed (%s)", day(at), oneLine(cs.LastBuildStatus))
	case cs.Decision == imagecheck.RebuildFailedLast && !failed:
		// The last build of these inputs ended without clearing what
		// triggered it, and the check won't pay for another.
		what = fmt.Sprintf("the image rebuild of %s ended (%s) without fixing the image", day(at), oneLine(cmp.Or(cs.LastBuildStatus, "status unknown")))
	default:
		return ""
	}
	if img == nil {
		return what + "; the image has no record yet. See fugaro image status."
	}
	return what + "; runs still use the image built " + day(img.BuiltAt) + ". See fugaro image status."
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }
