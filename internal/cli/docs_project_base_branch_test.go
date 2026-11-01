package cli

import (
	"strings"
	"testing"
)

// TestDocsStateTheProjectDefaultBaseBranch pins the precedence the code
// implements after the project's default base branch landed
// (config.EffectiveBaseBranch, and internal/cli/basebranch.go's launcher): a
// base branch resolves from the checkout's committed fugaro.yaml when its text
// writes git.base_branch, else the installation config's repos.<r>.base_branch,
// else the project's top-level base_branch — the one layer that is published
// to the policy node by `fugaro project policy publish` and never in the
// shared config — else main. The launcher's order used to be the other way
// round (the local config above the checkout's file), so the docs a reader
// meets the key on have to state the new one and the reason the shared file
// carries no base branch at all.
//
// Mutation (verified by running the test before the docs moved): the paragraph
// of docs/project-layer.md §2 absent, its `repos.<r>.base_branch` and
// `base_branch` halves swapped, §3's bullet back to "when the branch is not
// `main`", the scope table's row widened to `project, repo`, the shared
// config's two bullets back to the repository's `base_branch` alone, or
// docs/design/project-centric.md's policy-node paragraph without the default —
// each fails here.
func TestDocsStateTheProjectDefaultBaseBranch(t *testing.T) {
	layer := flattenDoc(readDoc(t, "docs/project-layer.md"))
	for _, want := range []string{
		// §2, beside the one rule: the four layers, in the order they resolve.
		"The base branch resolves through four layers, narrowest first",
		"the checkout's committed `fugaro.yaml` when its text writes `git.base_branch`",
		"then the installation config's `repos.<r>.base_branch`, then the project's default base branch",
		"the installation config's top-level `base_branch`, which `fugaro project policy publish` writes into the policy node",
		"then `main`",
		// The key is judged on whether it is written, never on its value:
		// applyDefaults fills main in, so the two would otherwise look alike.
		"judged on whether it is written, never against `main`",
		// §3's bullet: main is no longer the only branch a repository writes.
		"`git.base_branch:` when the branch is not the project's default",
		// §5's scope table: the row stays `repo`, and the project default is
		// the owner's in the policy node rather than a project-layer key.
		"| `git.base_branch` | repo | a project-wide base branch in the launcher-writable project layer would let a bucket writer point image builds at an unreviewed branch; the project's default base branch is the owner's, published to the policy node instead |",
	} {
		if !strings.Contains(layer, want) {
			t.Errorf("docs/project-layer.md does not say %q", want)
		}
	}
	if strings.Contains(layer, "when the branch is not `main`") {
		t.Error("docs/project-layer.md still says a repository writes git.base_branch when the branch is not `main`: it writes it when the branch is not the project's default, which the project's own base_branch may have moved")
	}

	// docs/design/shared-config.md: the shared file refuses the project's
	// default too, for the reason it refuses each repository's entry.
	shared := flattenDoc(readDoc(t, "docs/design/shared-config.md"))
	for _, want := range []string{
		"each repository's `base_branch` and the project's top-level `base_branch`",
		"no `base_branch` at either level",
	} {
		if !strings.Contains(shared, want) {
			t.Errorf("docs/design/shared-config.md does not say %q", want)
		}
	}
	if strings.Contains(shared, "Consumers use the checkout's value, else `main`") {
		t.Error("docs/design/shared-config.md still resolves a base branch to the checkout's value else `main`: the installation's repos.<r>.base_branch and the project's base_branch sit between them, and the shared file is why the second is read from the policy node")
	}

	// docs/design/project-centric.md: the policy node's members, with the
	// project's default among them and main when it is absent.
	centric := flattenDoc(readDoc(t, "docs/design/project-centric.md"))
	for _, want := range []string{
		"The node also carries the project's `base_branch`",
		"a node without it means `main`",
	} {
		if !strings.Contains(centric, want) {
			t.Errorf("docs/design/project-centric.md does not say %q", want)
		}
	}
}
