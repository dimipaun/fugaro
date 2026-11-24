package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// stampDoor is the door arm's own selection: which checkout the arm acts on and
// how it chooses a project config, spelled as the arm's own loader spells it, so
// the fence reads the installation the arm is about to write to and no other.
type stampDoor struct {
	// dir is the checkout the arm acts on: `update --repo`'s PATH argument, and
	// "" for the converge and `setup`, which act on the working directory's.
	dir string
	// creating is setup's half of the selection: it may select a project config
	// it has yet to write (loadInitConfig), where an `update --repo` may not
	// (loadRepoConfig).
	creating bool
	// name is setup's --name, which selects one of several configs. `--repo`
	// passes none and validates the flag against the selection it made instead.
	name string
}

// stampDoorProject is the project the arm will act on, resolved read-only: the
// checkout's project and the arm's own selection, without the remembering
// checkoutProject does (R3) and without the config a converge writes when it
// starts one.
//
// A selection this binary cannot make is no fence and never this check's
// refusal: the run refuses it a moment later with its own sentence.
func stampDoorProject(ctx context.Context, o *initOptions, d stampDoor) *localcfg.Config {
	co, err := resolveCheckout(ctx, d.dir)
	if err != nil {
		return nil
	}
	if co != nil && co.Project == "" {
		// A checkout without project: would refuse; the file being written is
		// the one that lacks it (selectedProjectErr's rule).
		co = nil
	}
	cloud := o.cloud
	// The origin that may select a project config is the arm's checkout's, not
	// the working directory's (loadRepoConfig's own line).
	cloud.originDir = d.dir
	_, lc, err := selectNamed(cloud, co, d.creating, d.name)
	if err != nil {
		return nil
	}
	return lc
}

// update_stamp_guard.go is `fugaro update`'s own fence: the door refuses a
// binary below the installation's required CLI before its first write (T4 of
// docs/required-version-stamp/implementation-plan.md, D3266, R8).
//
// The precedent is internal/infra's DB.Plan, which refuses to deploy older
// rules over a newer marker. Without this check an older admin's `fugaro update`
// would put its own images, its unit and a re-rendered policy over an
// installation a newer release already moved — and it would do it in every mode
// it has: `--forget` and `--config-only` write nothing of the converge and reach
// no stage at all, so a check inside the loop would miss them (F24).
//
// That is why the check sits at the top of the door's RunE and not in a stage:
// the image catalog guard, syncAccess, rewriteLocalBudgetMode and the first
// stage all come after it (F11). It is shared by `update`, `update --repo`,
// `setup` and `restore`, which are the one constructor (newInitCmdWith).
//
// Two modes are not fenced, because neither makes a cloud call and neither can
// act on the installation: `--check`, answered earlier in RunE, and
// `--print-vars` (R8, F24). A `dev` or pre-release binary skips the check
// entirely: it has nothing to compare with (spec, "Dev builds").
func refuseUpdateBelowTheStamp(cmd *cobra.Command, o *initOptions, d stampDoor) error {
	if o.printVars {
		// The two modes that read no cloud are one line each, and this is the
		// door's: --print-vars renders the installation's variables from the
		// checkout's own files and touches neither the database nor the cloud,
		// so there is nothing here to guard.
		return nil
	}
	// A version that is not a plain X.Y.Z — a `dev` build, a pre-release tag —
	// is never below a stamp (versionstamp.Below is updatecheck.Newer), so a
	// read here would spend the timeout to answer a question this binary cannot
	// be asked.
	if !updatecheck.Valid(Version) {
		return nil
	}
	// The project is the one this arm is about to act on, resolved from the
	// door's own flags and the environment and never remembered (R3): the arm's
	// own selection, so `--repo PATH` fences the installation of the checkout at
	// PATH and not the working directory's.
	//
	// A config this binary cannot resolve is not this check's to report: the run
	// refuses it a moment later with its own sentence, and the fence is a fence
	// and not a validator (spec, "Read failures fail open"). A machine with no
	// project at all, or a project whose config names no database, has no stamp
	// to be below and no installation to fence.
	lc := stampDoorProject(cmd.Context(), o, d)
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil
	}
	// Fresh, not the cache (R8): a stamp raised a minute ago must stop an older
	// update at once rather than at the end of the cache's trust window. What
	// the read answers when it fails is T1's rule, not this file's — one cached
	// value above this binary that was read less than 15 minutes ago still
	// refuses (D3264, bounded by D3268).
	res, err := newStampSource(versionstamp.Options{Open: stampOpen}).Required(cmd.Context(), versionstamp.Query{
		URL: lc.Budget.RTDBURL, NoAuth: lc.Endpoints.NoAuth, Fresh: true,
	})
	if err != nil {
		// A stamp that is there and unreadable is the one read that does not
		// fail open: *versionstamp.MalformedError carries the sentence naming
		// the node and the one thing that repairs it.
		return err
	}
	if res.Found && versionstamp.Below(res.Stamp, Version) {
		return &versionstamp.BelowError{Stamp: res.Stamp, Version: Version}
	}
	if res.Origin == versionstamp.OriginNone {
		// Fail open, one line (D3264): the stamp is a fence and not a boundary,
		// and a database blip must not stop the one command that brings the
		// installation forward. The sentence is a warning and never the run's
		// failure.
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: could not read the installation's required CLI (%s): %s; going on\n", versionstamp.Path, oneLine(res.Reason))
	}
	return nil
}
