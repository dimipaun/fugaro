package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// fugaro init --anchor: the checkout's gcp_project: line alone, with no
// Terraform, no discovery, no publish and no image copy. It reads the local
// config and the build records in the runs bucket, checks that the line is
// safe to merge (gcpProjectProblems) before it writes anything, and then
// makes the repository stage's edit (writeAnchor).

// anchorResult is what --json says of --anchor.
type anchorResult struct {
	Path string `json:"path,omitempty"`
	Line string `json:"line,omitempty"`
	// State is written, present (the line was there), not_written (a
	// check failed, or the write was declined or not confirmed), by_hand
	// (the file's shape: the line is the user's to add) or skipped (a
	// custom runs bucket).
	State    string   `json:"state"`
	Problems []string `json:"problems,omitempty"`
}

func (r *initRun) runAnchor(ctx context.Context) error {
	o := r.o
	res := &anchorResult{State: "not_written"}
	r.res.Anchor = res
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return userErr("fugaro init --anchor writes the gcp_project line of a repository's fugaro.yaml, and this directory is not in a git checkout: run it in the repository's checkout")
	}
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return err
	}
	sel, lc, err := selectNamed(o.cloud, co, false, o.name)
	if err != nil {
		return err
	}
	if lc == nil {
		return userErr("there is no local config for this project: run fugaro init first")
	}
	if err := refuseSharedWrite(sel); err != nil {
		return err
	}
	if err := announce(o.cloud, sel, lc); err != nil {
		return err
	}
	if o.name != "" && o.name != lc.Name {
		return userErr("%s selects project %s; --name says %s", sel.From, lc.Name, o.name)
	}
	r.setProject(lc)
	res.Line = "gcp_project: " + lc.GCPProject

	// The checkout: its own fugaro.yaml (the file the line goes into) names
	// this project, and its origin is a repository the project lists.
	path := filepath.Join(root, "fugaro.yaml")
	res.Path = path
	data, err := readFugaroYAML(path)
	if err != nil {
		return userErr("%v", err)
	}
	cfg, problems := config.Parse(data)
	if cfg == nil {
		why := "unreadable"
		if len(problems) > 0 {
			why = problems[0].String()
		}
		return userErr("%s is not valid (%s): run fugaro validate", path, pluginwire.Printable(oneLineCLI(why)))
	}
	if cfg.Project != lc.Name {
		return userErr("%s names project %q, not %s", path, pluginwire.Printable(cfg.Project), lc.Name)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return userErr("this checkout has no origin repository, so it is no repository of project %s", lc.Name)
	}
	repo := pluginwire.Printable(oi.Repo)
	if !repoKnown(lc, oi) {
		return userErr("%s is not one of project %s's repositories in the local config: run fugaro init in this checkout to onboard it (it also writes the gcp_project line)", repo, lc.Name)
	}
	var listed []string // the workflows the local config records
	for name, rr := range lc.Repos {
		if sameRepo(name, oi.Repo) {
			listed = rr.Workflows
		}
	}
	r.res.Repo = oi.Repo

	if note := customBucketNote(lc); note != "" {
		res.State = "skipped"
		fmt.Fprintf(r.w, "note: %s\n", note)
		return userErr("the gcp_project line is not written: this installation's runs bucket is not the default name")
	}

	// Every check before any write; all of them are said together.
	edit, err := prepareAnchor(lc, root)
	var bh *byHandError
	switch {
	case errors.As(err, &bh):
		res.State = "by_hand"
		fmt.Fprintf(r.w, "note: %s\n", bh.Error())
	case err != nil:
		return err
	case !edit.changed:
		res.State = "present"
		fmt.Fprintf(r.w, "%s already has %s; nothing to write\n", path, edit.line())
	}
	remoteFail := false
	for _, p := range gcpProjectProblems(ctx, lc, sel.Path, cfg.Git.Provider, oi.Repo, workflowBases(cfg, listed)) {
		res.Problems = append(res.Problems, p.text)
		remoteFail = remoteFail || p.remote
		fmt.Fprintf(r.w, "FAIL: %s\n", p.text)
	}
	fail := func(err error) error {
		if remoteFail {
			return remote(err)
		}
		return &ExitError{Code: ExitUserError, Err: err}
	}
	switch n := len(res.Problems); {
	case n > 0 && res.State == "present":
		return fail(fmt.Errorf("%s already has gcp_project, but %d check(s) above failed: runs and the daily image check refuse the file while they do; fix them before you merge it", path, n))
	case n > 0:
		return fail(fmt.Errorf("the gcp_project line is not written: %d check(s) above failed; fix them, then rerun fugaro init --anchor", n))
	case res.State == "by_hand":
		return userErr("fugaro.yaml is not edited: add the line by hand (above)")
	case res.State == "present":
		return r.printResult()
	}

	env := initflow.Env{Yes: o.yes, Interactive: stdinIsTerminal(r.cmd.InOrStdin()) && !o.nonInteractive && !o.asJSON}
	written, err := r.writeAnchor(edit, env, false)
	if err != nil {
		return err
	}
	if !written {
		return userErr("nothing was written: add the line by hand, or rerun fugaro init --anchor at a terminal or with --yes")
	}
	res.State = "written"
	return r.printResult()
}

// anchorHintText points at --anchor where a checkout lacks the line.
const anchorHintText = "to let teammates use this installation without setup, run fugaro init --anchor in this checkout (it checks the images first)"

// needsAnchorHint reports whether co, a checkout of a repository lc lists,
// has no gcp_project: line although lc's installation could take one (a
// convention-named runs bucket). A repository not onboarded yet is not
// pointed at --anchor, which would refuse it.
func needsAnchorHint(ctx context.Context, co *localcfg.Checkout, lc *localcfg.Config) bool {
	if co == nil || lc == nil || lc.GCPProject == "" || co.GCPProject != "" || co.Project != lc.Name || customBucketNote(lc) != "" {
		return false
	}
	oi, ok := readOrigin(ctx, co.Root)
	return ok && repoKnown(lc, oi)
}

// noteAnchor prints anchorHintText once per run when the checkout at root
// needs it (init --repo's last line; a note, so stderr under --json).
func (r *initRun) noteAnchor(ctx context.Context, root string, lc *localcfg.Config) {
	if r.anchorNoted {
		return
	}
	if co, err := checkoutProject(ctx, root); err == nil && needsAnchorHint(ctx, co, lc) {
		r.anchorNoted = true
		fmt.Fprintf(r.w, "note: %s\n", anchorHintText)
	}
}
