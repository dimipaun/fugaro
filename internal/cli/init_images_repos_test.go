package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// U5's tests: the two halves of `fugaro update` that used to reason about
// "this checkout" — the images stage's kinds and the policy stage's default
// catalog entry — read every repository the run read instead (R9, C1).
//
// The views are stubbed, because what reads a repository is the reader and,
// until I1, its stand-in answers with the working directory's checkout alone:
// a test about reading two repositories from a directory that holds neither is
// a test the stub cannot pass on its own.

// viewCfg is a repository's fugaro.yaml as a read view carries it: parsed over
// no project layer, which is the shape the stages below read.
func viewCfg(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	return cfg
}

// stubViews stands the reader in for the repositories given, for one test.
func stubViews(t *testing.T, views ...repoView) {
	t.Helper()
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return slices.Clone(views), nil
	})
}

// TestImageKindsAreTheUnion: the kinds the images stage mirrors are the union
// of every repository the run read and not of a working directory's checkout
// (R9). The two runs that tell the two apart are a directory that holds no
// checkout at all, where the kinds can only come from the views, and a
// checkout whose own workflows would answer as the kinds today while the
// repository the reader really answered for is one it could not read.
func TestImageKindsAreTheUnion(t *testing.T) {
	t.Run("every repository the run read", func(t *testing.T) {
		// Nothing here for a working-directory read to find: the same command
		// run from any directory is the point of the plan.
		t.Chdir(t.TempDir())
		cfg := viewCfg(t, bakedWorkflowYAML)
		stubViews(t,
			repoView{Repo: "acme/app", Cfg: cfg},
			repoView{Repo: "acme/api", Cfg: cfg},
			repoView{Repo: "acme/gone", Unread: "no fugaro.yaml on the default branch"},
		)

		e, _ := stageEngine(t, "", nil)
		s := newImagesStage(e)
		if got := strings.Join(s.kinds(t.Context()), ","); got != config.BaseKind {
			t.Errorf("the kinds this run mirrors are %q, want %q: they are the union of every repository it read, and this directory holds no checkout for a working-directory read to answer with\n%s",
				got, config.BaseKind, s.e.r.w)
		}
	})

	t.Run("a repository the run could not read adds none", func(t *testing.T) {
		// A checkout of acme/app whose own workflows would be the kinds today.
		t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", "")))
		stubViews(t, repoView{Repo: "acme/app", Unread: "its fugaro.yaml is not valid: workflows: at least one workflow is required"})

		e, _ := stageEngine(t, "", nil)
		s := newImagesStage(e)
		if got := s.kinds(t.Context()); len(got) != 0 {
			t.Errorf("the kinds this run mirrors are %v, want none: the one repository it could not read adds none (R13), and the checkout this command runs in is no repository's workflows any more\n%s",
				got, s.e.r.w)
		}
	})
}

// TestDefaultEntryHeldWhileARepositoryIsUnread: the default catalog entry is
// the image of every job whose repository names none, so it is written once
// every repository the project lists was read and none of their workflows
// refuses it, and held — naming the repository — otherwise (R9, D1191).
//
// The rig is update_default_entry_test.go's, and the two runs differ in one
// thing alone: whether the reader answered for the project's second repository
// with a view that says why. Both of them are admitted by the project (the
// published node and the profile this same run writes), so what holds the
// entry is the repository being unread and nothing else.
func TestDefaultEntryHeldWhileARepositoryIsUnread(t *testing.T) {
	const sibling = "acme/other"
	node := currentNode()
	node["allow_repos"] = []string{"github/acme/app", "github/" + sibling}
	read := repoView{Repo: "acme/app", Cfg: viewCfg(t, bakedWorkflowYAML)}

	t.Run("every repository read", func(t *testing.T) {
		r := ownImageRigNode(t, node)
		onlyRepos(t, r, sibling)
		stubViews(t, read, repoView{Repo: sibling, Cfg: viewCfg(t, bakedWorkflowYAML)})
		out := convergeOn(t, r, []string{"policy-vs-jobs"})

		entry, ok := writtenDefaultEntry(t)
		if !ok {
			t.Fatalf("this update wrote no default catalog entry while it read every repository the project lists — the workflows of both write an image of their own, so none of them refuses the entry:\n%s", out)
		}
		if entry.Source.Image == "" {
			t.Errorf("the default entry is %+v, want the base image this run pinned", entry)
		}
	})

	t.Run("a repository the run could not read", func(t *testing.T) {
		r := ownImageRigNode(t, node)
		onlyRepos(t, r, sibling)
		stubViews(t, read, repoView{Repo: sibling, Unread: "no fugaro.yaml on the default branch"})
		out := convergeOn(t, r, []string{"policy-vs-jobs"})

		if entry, ok := writtenDefaultEntry(t); ok {
			t.Errorf("this update wrote the default entry %+v while github/%s could not be read: that repository keeps the per-repository job its own root deploys, the entry is that job's image, and a bare baked workflow of it — which nothing here could have read — is refused by the deployer (unbuiltOnBaked)\n%s",
				entry, sibling, out)
		}
		line := oneWithheldEntryLine(t, out)
		if !strings.Contains(line, "read no fugaro.yaml of") {
			t.Errorf("the line this update prints about the entry does not say it read no fugaro.yaml of the repository: %q", line)
		}
		if !strings.Contains(line, "github/"+sibling) {
			t.Errorf("the line does not name github/%s, the repository that holds the write back: %q", sibling, line)
		}
		if log := withheldEntryLog(t); !strings.Contains(log, "github/"+sibling) || !strings.Contains(log, "unbuiltOnBaked") {
			t.Errorf("the log does not hold the repository the line names and the refusal behind it, so the facts the line cut are nowhere:\n%s", log)
		}
	})
}
