package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// U3a's tests: the secrets stage over the update door's every repository (R18,
// R21). What reads the repositories is C1's reader, which a test stands in for
// here -- it is git and, after I1, the code host's API (update_repos_test.go).

// repoViewOf is one read repository as the reader answers it: a default branch
// and the fugaro.yaml parsed from it, which is the whole of the read shape.
func repoViewOf(t *testing.T, repo, provider, yaml string) repoView {
	t.Helper()
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems[0])
	}
	return repoView{Repo: repo, Provider: provider, Branch: "main", Head: "0000000", YAML: []byte(yaml), Cfg: cfg}
}

// listRig is the update door's stage over a stubbed list: the checkout rig
// (with its memStore and its terminal stubs), the door set to update, and the
// reader answered with views.
func listRig(t *testing.T, views ...repoView) *secretsRig {
	t.Helper()
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""), func(o *initOptions) { o.door = updateDoor.name })
	// The project's own repositories: the local config lists them, which is
	// what a view of the reader's list is (the hostile-checkout gate has its
	// own tests, and the admit case is its own test below).
	for _, v := range views {
		provider := v.Provider
		if provider == "" {
			provider = "github"
		}
		r.e.lc.Repos[v.Repo] = localcfg.Repo{Provider: provider}
	}
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return views, nil
	})
	return r
}

// leftCommandsOf is every command the run left for the user, in order.
func leftCommandsOf(res *initflow.Result) []string {
	var out []string
	for _, l := range res.Left {
		out = append(out, l.Commands...)
	}
	return out
}

// TestSecretsStageNamesEveryRepository: from any directory, `update`'s secrets
// stage works on the project's every repository -- one line each, in canonical
// order, with the command that stores each missing name, whichever repository
// wants it. A name several repositories want is one line when the store the
// command writes is the project's own (R2, R18).
func TestSecretsStageNamesEveryRepository(t *testing.T) {
	declared := checkoutYAML("github", "oauth", "aurora", ", secrets: [{ name: app-key, env: APP_KEY }]")
	app := repoViewOf(t, "acme/app", "github", declared)
	lib := repoViewOf(t, "acme/lib", "github", declared)
	r := listRig(t, app, lib)
	// The Claude credential is the project's own, one store for both
	// repositories: `fugaro secrets set claude-oauth-token` is the whole of
	// what either of them needs, so it is one line and not two.
	r.e.lc.ProjectSecrets = map[string]config.ProjectSecret{
		config.ClaudeOAuthToken: {AllowDataTo: []string{"acme/app", "acme/lib"}},
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := stateOf(res, initflow.Secrets); got != initflow.NeedsYou {
		t.Fatalf("state = %q, want needs-you: neither repository has the names its jobs mount (out %q, errOut %q)", got, r.out.String(), r.errOut.String())
	}

	var repos []string
	for _, l := range r.e.reposState.reported {
		for _, line := range l.lines {
			repos = append(repos, line.Repo)
		}
	}
	if want := []string{"acme/app", "acme/lib"}; strings.Join(repos, ",") != strings.Join(want, ",") {
		t.Fatalf("the stage named %v, want %v: one line per repository, in canonical order", repos, want)
	}

	cmds := strings.Join(leftCommandsOf(res), "\n")
	for _, want := range []string{
		// A repository-scope name: `secrets set` names one repository, so
		// each of them gets its own line.
		"fugaro secrets set app-key --repo acme/app",
		"fugaro secrets set app-key --repo acme/lib",
		// The project-wide name: one line, and the store it writes is the
		// copy every repository that mounts it reads.
		"fugaro secrets set claude-oauth-token",
	} {
		if !strings.Contains(cmds, want) {
			t.Fatalf("the run does not leave %q:\n%s", want, cmds)
		}
	}
	if n := strings.Count(cmds, "fugaro secrets set claude-oauth-token"); n != 1 {
		t.Errorf("the project-wide name is left %d times, want one line for every repository that wants it:\n%s", n, cmds)
	}
	// The reader's list is what the stage read: no working directory is one of
	// the stage's repositories, and the checkout's own is one of the list's.
	if len(r.stage.list) != 2 {
		t.Fatalf("the stage holds %d targets, want the list's two", len(r.stage.list))
	}
}

// TestSecretsStageAdmitCountsAsEnrolled: a repository the published policy
// admits is enrolled, whether or not this machine's local config lists it
// (D3350) -- the stage takes its secrets and creates what they live in, and no
// line names a door that would enrol it. The control is the same rig with no
// node read: with nothing of the project's naming the repository, it is left
// for the user, with the door that asks (D5).
func TestSecretsStageAdmitCountsAsEnrolled(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, node bool) (*secretsRig, *initflow.Result) {
		t.Helper()
		view := repoViewOf(t, "bitbucket/acme/app", "bitbucket", checkoutYAML("bitbucket", "oauth", "aurora", ""))
		r := listRig(t, view)
		delete(r.e.lc.Repos, "bitbucket/acme/app") // this machine's config lists no such repository
		if node {
			readNodeFrom(r, bitbucketTokenNode(t, "bitbucket/acme/app"))
		}
		r.stage.resolve(ctx)
		res, err := r.run(t)
		if err != nil {
			t.Fatal(err)
		}
		return r, res
	}

	t.Run("the admitted repository is worked on", func(t *testing.T) {
		r, res := run(t, true)
		if r.stage.list[0].enrol {
			t.Fatal("a repository the published policy admits is not one this door leaves for the user: a published admit counts as enrolled (D3350)")
		}
		if got := stateOf(res, initflow.Secrets); got != initflow.NeedsYou {
			t.Fatalf("state = %q, want needs-you: the token has no project value stored (out %q, errOut %q)", got, r.out.String(), r.errOut.String())
		}
		cmds := strings.Join(leftCommandsOf(res), "\n")
		if strings.Contains(cmds, "fugaro setup") {
			t.Fatalf("the run offers the door that enrols a repository the project already admitted:\n%s", cmds)
		}
		if !strings.Contains(cmds, "fugaro secrets set bitbucket-token") {
			t.Fatalf("the run leaves nothing that stores the token the admit makes project-wide:\n%s", cmds)
		}
	})

	t.Run("with no node the repository is left for the user", func(t *testing.T) {
		r, res := run(t, false)
		if !r.stage.list[0].enrol {
			t.Fatal("a repository nothing of the project's names is not left for the user: this door re-converges what is enrolled and enrols none (D5)")
		}
		if got := stateOf(res, initflow.Secrets); got != initflow.NeedsYou {
			t.Fatalf("state = %q, want needs-you", got)
		}
		cmds := strings.Join(leftCommandsOf(res), "\n")
		if !strings.Contains(cmds, "fugaro setup") {
			t.Fatalf("the run does not name the door that enrols the repository:\n%s", cmds)
		}
	})
}

// TestSecretsStageSetupKeepsItsCheckout: `setup`'s stage reads the working
// directory's checkout and never the project's list (R18): the door that
// installs a project is run in one checkout, and the list is `update`'s.
func TestSecretsStageSetupKeepsItsCheckout(t *testing.T) {
	calls := 0
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		calls++
		return []repoView{repoViewOf(t, "acme/other", "github", checkoutYAML("github", "oauth", "aurora", ""))}, nil
	})
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.stage.resolve(context.Background())
	if calls != 0 {
		t.Fatal("the setup door read the project's list: its stage works on the checkout it was run in (R18)")
	}
	if r.stage.list != nil {
		t.Fatal("the setup door holds a list of repositories")
	}
	if r.stage.tg == nil || r.stage.tg.repo != "acme/app" {
		t.Fatalf("the setup door's target is %+v, want the checkout's own acme/app", r.stage.tg)
	}
}
