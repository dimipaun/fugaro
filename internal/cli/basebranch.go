package cli

import (
	"context"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/config"
)

// launchBaseBranch is the base branch a new run is cut from, and the source
// that named it, through config.EffectiveBaseBranch — the one function every
// layer resolves through: the working directory's checkout of repo when its
// fugaro.yaml writes git.base_branch, else the local config's
// repos.<r>.base_branch, else the project's base_branch, else main.
//
// The project's is the installation config's own top-level base_branch, else
// the one the published policy node carries (projectBaseBranch below).
func launchBaseBranch(ctx context.Context, env *cloudEnv, repo string) (branch, source string) {
	file, fileSets := checkoutBaseBranch(ctx, repo)
	repoBranch := ""
	if r, ok := env.localRepo(repo); ok {
		repoBranch = r.BaseBranch
	}
	needed := !(fileSets && file != "") && repoBranch == ""
	return config.EffectiveBaseBranch(file, fileSets, repoBranch, env.projectBaseBranch(ctx, needed))
}

// checkoutBaseBranch is layer 1: the base branch the working directory's
// checkout of repo names, and whether its fugaro.yaml writes git.base_branch
// at all. The flag is the file's own text (config.SetsGitBaseBranch), never
// the resolved value: Resolve fills the default in, so a resolved config
// cannot tell a file that writes main from one that writes nothing, and the
// layers below must not be consulted for a file that chose main itself.
//
// The file is read from the working tree, as the launcher has always read it
// for this decision: a launch with no --ref is resolving the ref from that
// same checkout, so there is no ref to read it at yet.
func checkoutBaseBranch(ctx context.Context, repo string) (branch string, fileSets bool) {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return "", false
	}
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil || !config.SetsGitBaseBranch(data) {
		return "", false
	}
	if c := checkoutConfig(ctx, repo); c != nil {
		return c.Git.BaseBranch, true
	}
	return "", false
}

// projectBaseBranch is layer 3: the installation config's top-level
// base_branch, else the published policy node's. needed says the two layers
// above answered nothing, and the policy is read only then: a launch whose
// base the checkout's file or the repository's local entry already named must
// not pay for a database read, and — more to the point — must not be affected
// by one.
//
// A read that fails or finds no node answers "" and is not reported here: the
// project default is not worth failing a launch over, and the launch's own
// policy gate (launchPolicyGates) makes the same read with policyReadErr's
// wording when the node is one a launch has to satisfy. A node too old to
// carry the key is not an error either: BaseBranch is simply empty, which is
// what every node published before this release says.
func (e *cloudEnv) projectBaseBranch(ctx context.Context, needed bool) string {
	if e.lc.BaseBranch != "" || !needed {
		return e.lc.BaseBranch
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil || !found {
		return ""
	}
	return p.BaseBranch
}

// specBaseBranch is the task field a launch writes for base: nothing when the
// winning layer is the default (main), which is what every launch carried
// before the field existed. A value of main is dropped rather than written:
// the runner resolves main anyway, and the field is what a runner older than
// baseBranchSince refuses the whole task over (task.Parse decodes with
// DisallowUnknownFields), so a launch that changes nothing must write nothing.
func specBaseBranch(base string) string {
	if base == config.DefaultBaseBranch {
		return ""
	}
	return base
}

// resolvedBaseBranch is specBaseBranch for a base branch the layers resolved,
// with one more case dropped: the checkout's own fugaro.yaml naming it. The
// runner reads that file at the ref it checks out, so the field would only be
// a second copy of a value the file already carries — and a copy is what a
// runner older than baseBranchSince refuses the whole task over.
func resolvedBaseBranch(base, source string) string {
	if source == config.BaseBranchSourceFile {
		return ""
	}
	return specBaseBranch(base)
}
