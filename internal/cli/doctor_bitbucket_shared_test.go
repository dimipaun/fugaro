package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// bitbucketOwnTokenSharedConfig is the local config the own-copy check reads:
// one Bitbucket repository of the project, with the provider recorded so its
// slug and its job profile can be resolved.
const bitbucketOwnTokenSharedConfig = "repos:\n  workspace-a/repo-a: { provider: bitbucket, workflows: [app] }\n"

// bitbucketOwnTokenSharedPolicy is a published node whose one profile is a
// shared Bitbucket job: it serves the provider, mounts no credential at all
// (D2302), and the allow-list enrolls the repository the config above names.
func bitbucketOwnTokenSharedPolicy() *projectpolicy.Policy {
	return doctorPolicy(func(p *projectpolicy.Policy) {
		p.AllowRepos = []string{"bitbucket/workspace-a/*"}
		p.Profiles = map[string]projectpolicy.Profile{"standard": {JobProfile: config.JobProfile{Provider: "bitbucket", CPU: 1, Memory: "512Mi", TimeoutClass: "6h"}}}
	})
}

// bitbucketOwnTokenCheck runs doctor --json and returns the own-copy check when
// the run carried one, with the run's own error: the check is silent in more
// states than it warns in, and both halves are what these tests hold.
func bitbucketOwnTokenCheck(t *testing.T) (doctorCheck, bool, error) {
	t.Helper()
	out, _, err := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", jerr, out)
	}
	c, ok := doctorCheckByID(o.Checks, "bitbucket-own-token-shared-job")
	return c, ok, err
}

// seedOwnBitbucketToken stores an enabled version of slug's own
// bitbucket-token container, the copy `fugaro update --repo` creates for a
// repository that holds its own token.
func seedOwnBitbucketToken(t *testing.T, r *doctorRig, slug string) {
	t.Helper()
	r.sm.Seed(gcp.SecretID(slug, config.BitbucketSecret), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: config.BitbucketSecret}, []byte("repo-value"))
}

// TestDoctorWarnsABitbucketOwnCopyOnASharedProfile: a repository admitted to a
// shared Bitbucket job, holding its own enabled bitbucket-token copy, is the
// state T2.4 exists for (D466, D2302): the job it runs on holds no token and
// the proxy serves it from the project's, so its own copy is read by its
// per-repository jobs alone. The warning names the repository, says that
// sentence, and never fails doctor without --strict.
//
// Bitbucket is admitted to a shared job for the length of this test: until
// internal/infra's list carries it, no installation can be in this state, and
// the launcher's own predicate is what the check reads (the test below pins
// that side).
func TestDoctorWarnsABitbucketOwnCopyOnASharedProfile(t *testing.T) {
	admitSharedJobProvider(t, "bitbucket")
	r, _ := doctorPolicyRig(t, bitbucketOwnTokenSharedPolicy(), bitbucketOwnTokenSharedConfig)
	seedOwnBitbucketToken(t, r, mustSlug("bitbucket", "workspace-a/repo-a"))

	c, ok, err := bitbucketOwnTokenCheck(t)
	if !ok {
		t.Fatalf("no bitbucket-own-token-shared-job check for a repository holding its own token on a shared job")
	}
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v", err)
	}
	if c.Severity != "warning" || c.OK {
		t.Errorf("check = %+v, want a warning that is not ok", c)
	}
	if !strings.Contains(c.Problem, "workspace-a/repo-a") {
		t.Errorf("the warning does not name the repository: %q", c.Problem)
	}
	if !strings.Contains(c.Problem, "its shared job holds no token and the proxy serves it from the project's token; its own copy applies only to its per-repository jobs") {
		t.Errorf("the warning does not carry the sentence the task pins: %q", c.Problem)
	}
	// The fix has to be a move that reaches the repository's own copy. It is
	// gcloud that does — no Fugaro command removes a repository-scoped
	// container — and `secrets rm` is the project-wide copy's command, which in
	// this very state is the credential the proxy serves the shared job from,
	// so the line keeps the reader off it rather than naming it as the removal.
	if !strings.Contains(c.Fix, "gcloud secrets versions disable") {
		t.Errorf("the fix does not name the move that reaches a repository's own copy: %q", c.Fix)
	}
	if !strings.Contains(c.Fix, "secrets rm is the project-wide copy's command and does not reach a repository's own") {
		t.Errorf("the fix does not say that the project-wide removal is not this copy's: %q", c.Fix)
	}
}

// TestDoctorIsQuietWhereNoSharedJobServesBitbucket: the launcher's own
// provider predicate is the first gate — the check reads it, as the launcher
// does — so a Bitbucket repository whose provider no shared job serves is kept
// on its per-repository job whatever its profile says, which is the job that
// mounts the copy. Every other fact here is the warning test's, so the only
// thing keeping doctor quiet is that no shared job serves Bitbucket.
//
// Bitbucket is withheld from that list for the length of this test: the tree
// admitted github alone when this was written, and I1 admitted Bitbucket too,
// so the state the gate is reached in is now the one an installation whose
// list is narrower than the tree's is in (withholdSharedJobProvider).
func TestDoctorIsQuietWhereNoSharedJobServesBitbucket(t *testing.T) {
	withholdSharedJobProvider(t, "bitbucket")
	r, _ := doctorPolicyRig(t, bitbucketOwnTokenSharedPolicy(), bitbucketOwnTokenSharedConfig)
	seedOwnBitbucketToken(t, r, mustSlug("bitbucket", "workspace-a/repo-a"))

	c, ok, err := bitbucketOwnTokenCheck(t)
	if ok {
		t.Fatalf("the check warned while no shared job serves Bitbucket: %+v", c)
	}
	if err != nil {
		t.Fatalf("a repository on its per-repository job must not fail doctor: %v", err)
	}
}

// TestDoctorIsQuietWithoutABitbucketOwnCopy: the same shared job, and nothing
// of the repository's own to warn about — no container at all, and one whose
// only version is disabled, which is not an enabled version. Bitbucket is
// admitted as in the warning test, so what these two hold is the own copy and
// not the provider gate.
func TestDoctorIsQuietWithoutABitbucketOwnCopy(t *testing.T) {
	t.Run("no own container", func(t *testing.T) {
		admitSharedJobProvider(t, "bitbucket")
		doctorPolicyRig(t, bitbucketOwnTokenSharedPolicy(), bitbucketOwnTokenSharedConfig)
		if c, ok, err := bitbucketOwnTokenCheck(t); ok {
			t.Fatalf("a repository with no own copy warned: %+v (err %v)", c, err)
		}
	})

	t.Run("an own container with no enabled version", func(t *testing.T) {
		admitSharedJobProvider(t, "bitbucket")
		r, _ := doctorPolicyRig(t, bitbucketOwnTokenSharedPolicy(), bitbucketOwnTokenSharedConfig)
		slug := mustSlug("bitbucket", "workspace-a/repo-a")
		seedOwnBitbucketToken(t, r, slug)
		r.sm.DisableVersions(gcp.SecretID(slug, config.BitbucketSecret))
		if c, ok, err := bitbucketOwnTokenCheck(t); ok {
			t.Fatalf("a disabled own copy warned: %+v (err %v)", c, err)
		}
	})
}

// TestDoctorIsQuietForAProfileThatMountsTheBitbucketToken: a profile mounting
// the token is the *per-repository* job's predicate, and CheckSharedJobProfile
// refuses it — so its repositories are not on a shared job and their own copy
// is exactly what their job mounts. A check that read SharedBitbucketProfile
// would warn here, which is the mutation this test fails — and the provider is
// admitted as in the warning test, so the refusal the mutation goes around is
// the only thing keeping the line off.
func TestDoctorIsQuietForAProfileThatMountsTheBitbucketToken(t *testing.T) {
	admitSharedJobProvider(t, "bitbucket")
	r, _ := doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) {
		p.AllowRepos = []string{"bitbucket/workspace-a/repo-a"}
		p.Profiles = map[string]projectpolicy.Profile{"standard": {JobProfile: config.JobProfile{
			Provider: "bitbucket", CPU: 1, Memory: "512Mi", TimeoutClass: "6h",
			Secrets: []string{config.BitbucketSecret}, AllowRepos: []string{"bitbucket/workspace-a/repo-a"}}}}
		p.BitbucketToken = &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/workspace-a/repo-a"}, Proxy: true}
	}), bitbucketOwnTokenSharedConfig)
	seedOwnBitbucketToken(t, r, mustSlug("bitbucket", "workspace-a/repo-a"))

	if c, ok, err := bitbucketOwnTokenCheck(t); ok {
		t.Fatalf("a profile that mounts the token was read as a shared job: %+v (err %v)", c, err)
	}
}

// TestDoctorIsQuietWithoutAPublishedPolicy: every state in which the node a run
// reads the shared-job decision from is not there to read — none published, one
// no reader can read, and repositories the node's profiles do not serve as
// Bitbucket shared jobs. The own-copy check only warns about what it could see,
// so each of these says nothing and fails nothing.
func TestDoctorIsQuietWithoutAPublishedPolicy(t *testing.T) {
	cases := []struct {
		name   string
		policy *projectpolicy.Policy
		config string
		spoil  func(t *testing.T, db *gcpfake.RTDB)
		seed   bool
	}{
		{name: "no policy published", policy: nil, config: bitbucketOwnTokenSharedConfig, seed: true},
		{
			name: "a node no reader can read", policy: bitbucketOwnTokenSharedPolicy(), config: bitbucketOwnTokenSharedConfig, seed: true,
			spoil: func(t *testing.T, db *gcpfake.RTDB) {
				db.Set(projectpolicy.PathRulesMarker(), "not-a-rules-generation")
			},
		},
		{
			name: "repositories the policy does not serve", policy: doctorPolicy(nil),
			config: "repos:\n  acme/app: { provider: github, workflows: [app] }\n", seed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, db := doctorPolicyRig(t, tc.policy, tc.config)
			if tc.spoil != nil {
				tc.spoil(t, db)
			}
			if tc.seed {
				seedOwnBitbucketToken(t, r, mustSlug("bitbucket", "workspace-a/repo-a"))
			}
			c, ok, err := bitbucketOwnTokenCheck(t)
			if ok {
				t.Fatalf("the check warned with no shared job to read it from: %+v", c)
			}
			if err != nil {
				t.Fatalf("an unreadable state must not fail doctor: %v", err)
			}
		})
	}
}
