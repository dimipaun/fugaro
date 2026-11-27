package cli

import (
	"context"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// anchorHintText points at the command that writes the gcp_project line where
// a checkout lacks it (doctor's gcp-project-line check, image refresh's own
// note). Its wording still names the flag U9b removed: U9c retargets every
// code pointer at `fugaro project repos add` (R14).
const anchorHintText = "to let teammates use this installation without setup, run fugaro update --anchor in this checkout (it checks the images first)"

// needsAnchorHint reports whether co, a checkout of a repository lc lists,
// has no gcp_project: line although lc's installation could take one (a
// convention-named runs bucket). A repository not onboarded yet is not
// pointed at the anchor command, which would refuse it.
func needsAnchorHint(ctx context.Context, co *localcfg.Checkout, lc *localcfg.Config) bool {
	if co == nil || lc == nil || lc.GCPProject == "" || co.GCPProject != "" || co.Project != lc.Name || customBucketNote(lc) != "" {
		return false
	}
	oi, ok := readOrigin(ctx, co.Root)
	return ok && repoKnown(lc, oi)
}
