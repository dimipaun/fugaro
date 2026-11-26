package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

func publishedLayerAt(t *testing.T, r *addRig, text string) {
	t.Helper()
	p := filepath.Join(r.dir, "runs", filepath.FromSlash(config.LayerKey))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// starterHost records the one publish a starter makes, the branch it was asked
// about, and the token the command opened it with.
type starterHost struct {
	got    []statePublish
	open   string // the pull request the branch already has, "" for none
	token  string // what the command handed enrolStarterHosts
	asked  []string
	seeErr error
}

func (h *starterHost) Visibility(context.Context, string) (stateVisibility, error) {
	return stateVisibilityPrivate, nil
}

func (h *starterHost) OpenPR(_ context.Context, repo, branch string) (string, error) {
	h.asked = append(h.asked, repo+" "+branch)
	return h.open, h.seeErr
}

func (h *starterHost) Publish(_ context.Context, p statePublish) (string, error) {
	h.got = append(h.got, p)
	return "https://github.com/" + p.Repo + "/pull/7", nil
}

func useStarterHost(t *testing.T, h *starterHost) *starterHost {
	t.Helper()
	old := enrolStarterHosts
	enrolStarterHosts = func(token string) (stateHost, error) {
		h.token = token
		return h, nil
	}
	t.Cleanup(func() { enrolStarterHosts = old })
	return h
}

// A repository with no fugaro.yaml gets a pull request for the minimal one and
// the enrolment stops there, writing nothing; one that has a file is enrolled
// as before and no pull request is opened.
func TestReposAddOpensAPullRequestForTheStarterFugaroYAML(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		host := useStarterHost(t, &starterHost{})
		file := enrolCheckoutFile(t)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		before := readFileString(t, r.cfg)
		out, errOut, err := r.enrol(t, enrolRepo)
		if err != nil {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if len(host.got) != 1 {
			t.Fatalf("publishes = %d, want 1", len(host.got))
		}
		p := host.got[0]
		if p.Repo != enrolRepo || p.Path() != "fugaro.yaml" || p.Branch != enrolStarterBranch || !strings.HasPrefix(string(p.Bytes), minimalAnchored) || strings.Contains(string(p.Bytes), "agent") {
			t.Errorf("publish = %+v, want the minimal file on %s", p, enrolStarterBranch)
		}
		for _, want := range []string{"pull request https://github.com/" + enrolRepo + "/pull/7", "merge it, then run `fugaro project repos add " + enrolRepo + "` again"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "⚠ CONFIRM") {
			t.Errorf("the run asked the confirmation:\n%s", out)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the checkout was written to: %v", err)
		}
		if got := readFileString(t, r.cfg); got != before {
			t.Errorf("the local config changed:\n%s", got)
		}
	})

	t.Run("one", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		host := useStarterHost(t, &starterHost{})
		out, errOut, err := r.enrol(t, enrolRepo)
		if err != nil {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if len(host.got) != 0 {
			t.Errorf("a repository with a fugaro.yaml had a pull request opened: %+v", host.got)
		}
		if !strings.Contains(out, "⚠ CONFIRM") {
			t.Errorf("the enrolment did not run:\n%s", out)
		}
	})
}

// The second run, before the first pull request is merged: the branch is not
// pushed again — the pull request the first run opened is the answer — and the
// person gets the same two lines, with nothing written.
func TestReposAddFindsTheStarterPullRequestAlreadyOpen(t *testing.T) {
	r := enrolRig(t)
	publishedLayerAt(t, r, testProjectLayer)
	pr := "https://github.com/" + enrolRepo + "/pull/7"
	host := useStarterHost(t, &starterHost{open: pr})
	file := enrolCheckoutFile(t)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	before := readFileString(t, r.cfg)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("the second project repos add %s failed: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if len(host.got) != 0 {
		t.Errorf("the second run pushed the branch again: %+v", host.got)
	}
	if len(host.asked) != 1 || host.asked[0] != enrolRepo+" "+enrolStarterBranch {
		t.Errorf("the run asked about %v, want one read of %s on %s", host.asked, enrolStarterBranch, enrolRepo)
	}
	for _, want := range []string{"pull request " + pr, "merge it, then run `fugaro project repos add " + enrolRepo + "` again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the run asked the confirmation:\n%s", out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the checkout was written to: %v", err)
	}
	if got := readFileString(t, r.cfg); got != before {
		t.Errorf("the local config changed:\n%s", got)
	}
}

// enrolRigLocalBitbucket is enrolRigLocal with enrolRepo enrolled as a Bitbucket
// one: a repository with no fugaro.yaml has no file to read its provider out of,
// so it is this machine's own entry that says which host the starter goes to.
var enrolRigLocalBitbucket = strings.Replace(
	strings.Replace(enrolRigLocal,
		`  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }`+"\n",
		`  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }`+"\n"+
			`  owner/repo-b: { provider: bitbucket, workflows: [web] }`+"\n", 1),
	"      - github/owner/repo-a\n",
	"      - github/owner/repo-a\n      - bitbucket/owner/repo-b\n", 1)

// bitbucketStarterToken is the token these tests let the machine hold. It is
// invented here and is a credential of nothing.
const bitbucketStarterToken = "a-token-this-test-invented"

// A Bitbucket repository with no fugaro.yaml gets its starter too: the token is
// read from the installation (never from the operator's shell), it reaches the
// host that pushes — Bitbucket has no CLI — and it is printed nowhere.
func TestReposAddPushesABitbucketStarterWithTheInstallationsToken(t *testing.T) {
	r := enrolRigOver(t, enrolRigLocalBitbucket)
	publishedLayerAt(t, r, testProjectLayer)
	host := useStarterHost(t, &starterHost{})
	old := enrolStarterToken
	enrolStarterToken = func(context.Context, *localcfg.Config, string) (string, error) {
		return bitbucketStarterToken, nil
	}
	t.Cleanup(func() { enrolStarterToken = old })
	file := enrolCheckoutFile(t)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if host.token != bitbucketStarterToken {
		t.Errorf("the host was opened with %q, want the installation's own token", host.token)
	}
	if len(host.got) != 1 {
		t.Fatalf("publishes = %d, want 1", len(host.got))
	}
	p := host.got[0]
	if p.Branch != enrolStarterBranch || !strings.Contains(string(p.Bytes), "provider: bitbucket") {
		t.Errorf("publish = %+v, want the minimal file with the bitbucket provider on %s", p, enrolStarterBranch)
	}
	if strings.Contains(out+errOut, bitbucketStarterToken) {
		t.Errorf("the token reached the output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
}

// stubStarterSecrets replaces the secret read and answers by ID, recording the
// IDs it was asked for in the order it was asked.
func stubStarterSecrets(t *testing.T, answers map[string][]byte) *[]string {
	t.Helper()
	var asked []string
	old := readSecretValue
	readSecretValue = func(_ context.Context, _ *localcfg.Config, id string) ([]byte, error) {
		asked = append(asked, id)
		if v, ok := answers[id]; ok {
			return v, nil
		}
		return nil, gcp.ErrNoVersion
	}
	t.Cleanup(func() { readSecretValue = old })
	return &asked
}

// starterTokenWorld is the two names enrolStarterToken resolves for enrolRepo.
func starterTokenWorld(t *testing.T) (own, project string) {
	t.Helper()
	slug, err := task.Slug(gitprov.KindBitbucket, enrolRepo)
	if err != nil {
		t.Fatal(err)
	}
	return gcp.SecretID(slug, config.BitbucketSecret), gcp.ProjectSecretID("aurora", config.BitbucketSecret)
}

// starterTokenConfig is this machine's project config, with the project-wide
// copy of the token allow-listed for the repository when projectWide is set.
func starterTokenConfig(projectWide bool) *localcfg.Config {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234"}
	if projectWide {
		lc.ProjectSecrets = map[string]config.ProjectSecret{
			config.BitbucketSecret: {AllowDataTo: []string{enrolRepo}},
		}
	}
	return lc
}

// The token a Bitbucket starter pushes with is the installation's own, in the
// precedence an apply gives a job's mount: the repository's own container when
// it has a version, and the project-wide copy the owner stored and allow-listed
// otherwise.
func TestReposAddReadsTheBitbucketTokenTheInstallationHolds(t *testing.T) {
	own, project := starterTokenWorld(t)

	t.Run("the repository's own copy", func(t *testing.T) {
		asked := stubStarterSecrets(t, map[string][]byte{own: []byte(bitbucketStarterToken)})
		got, err := enrolStarterToken(context.Background(), starterTokenConfig(true), enrolRepo)
		if err != nil {
			t.Fatalf("reading the token: %v", err)
		}
		if got != bitbucketStarterToken {
			t.Errorf("the token read is %q, want the repository's own copy", got)
		}
		if len(*asked) != 1 || (*asked)[0] != own {
			t.Errorf("the read asked for %v, want the repository's own container alone", *asked)
		}
	})

	t.Run("the project-wide copy", func(t *testing.T) {
		asked := stubStarterSecrets(t, map[string][]byte{project: []byte(bitbucketStarterToken)})
		got, err := enrolStarterToken(context.Background(), starterTokenConfig(true), enrolRepo)
		if err != nil {
			t.Fatalf("reading the token: %v", err)
		}
		if got != bitbucketStarterToken {
			t.Errorf("the token read is %q, want the project-wide copy", got)
		}
		if len(*asked) != 2 || (*asked)[0] != own || (*asked)[1] != project {
			t.Errorf("the read asked for %v, want the repository's own container and then the project-wide one", *asked)
		}
	})

	// A machine that holds neither is refused with the one command that stores
	// one, and not with a push that failed: the enrolment has nothing to send.
	t.Run("neither", func(t *testing.T) {
		stubStarterSecrets(t, nil)
		_, err := enrolStarterToken(context.Background(), starterTokenConfig(false), enrolRepo)
		if err == nil {
			t.Fatal("a machine holding no Bitbucket token was not refused")
		}
		for _, want := range []string{config.BitbucketSecret, enrolRepo, "--project-wide"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	})
}
