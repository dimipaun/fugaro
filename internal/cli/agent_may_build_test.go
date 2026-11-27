package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// TestAgentMayBuild is AG5d: builds are an agent's too (D3425). A billable
// build asks no session for an answer -- what bounds it is the lease it takes
// before each submission (T4, D1543) -- so `image build` and both of `image
// check --job`'s submissions admit a coding agent's session, and each subtest
// asserts the build was submitted rather than that nothing refused: "no
// refusal" is not the acceptance, the submission is.
//
// What a build reads is a separate question, and not this lift's: an entry
// whose Dockerfile is copied with the person's own credential keeps its
// refusal in a session (catalog_provider_test.go's "a coding agent's session
// reads nothing", SECURITY.md), and a repository the installation's
// credentials service serves is read keylessly instead. Every build here
// needs no such read: an inline definition is copied with no repository read
// at all, and the two check jobs rebuild from a definition the runs bucket
// already holds.
func TestAgentMayBuild(t *testing.T) {
	t.Run("a catalog entry's build", func(t *testing.T) {
		noAgentSession(t)
		fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
		stubCatalogProvider(t, map[string]string{
			".fugaro/base.Dockerfile":  catalogBuildFetched,
			".fugaro/other.Dockerfile": catalogBuildFetched,
		})
		// The copy of a repository-sourced entry reads the project's own
		// credential before it opens the provider, so the fixture holds one:
		// the stubbed provider is the only part of the copy a test has no
		// account for. That read is the Secret Manager one, which is served by
		// the installation's credentials service here, and so is no refusal.
		f.sm.AllowAccess()
		putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))
		t.Setenv("CLAUDECODE", "1")

		_, errOut, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
		if err != nil {
			t.Fatalf("an agent's session may build a catalog entry: %v\n%s", err, errOut)
		}
		if got := buildPosts(fb); got != 1 {
			t.Fatalf("builds submitted = %d, want the entry's one", got)
		}
		if !strings.Contains(errOut, "submits a Cloud Build for") {
			t.Errorf("the build printed no line of its own:\n%s", errOut)
		}
	})

	t.Run("the repository's check job", func(t *testing.T) {
		noAgentSession(t)
		f := newCheckJob(t, checkFiles(), bitbucketYAML)
		t.Setenv("CLAUDECODE", "1")

		// executeJob sets CLOUD_RUN_JOB, which is the one fact the job still
		// needs: the session's marker is not one it is refused for.
		lines, err := runCheckJob(t)
		if err != nil {
			t.Fatalf("an agent's session may run the check job: %v", err)
		}
		if got := buildPosts(f.fb); got != 1 {
			t.Fatalf("builds submitted = %d, want the rebuild the check decided", got)
		}
		if len(lines) != 1 || lines[0].Decision != imagecheck.Rebuild {
			t.Fatalf("lines = %+v", lines)
		}
	})

	t.Run("the project's catalog check job", func(t *testing.T) {
		noAgentSession(t)
		f := newCatalogCheckJob(t, catalogCheckSpecFor(t, catalogCheckCatalog("tools", "runtime", "prebuilt")))
		f.seed(t, "tools", "acme/app", catalogCheckAppDef, checkBaseDigest)
		f.seed(t, "runtime", "acme/other", catalogCheckOtherDef, "sha256:"+strings.Repeat("9", 64))
		t.Setenv("CLAUDECODE", "1")

		lines, err := runCatalogCheck(t)
		if err != nil {
			t.Fatalf("an agent's session may run the catalog check job: %v", err)
		}
		if got := buildPosts(f.fb); got != 1 {
			t.Fatalf("builds submitted = %d, want the one rebuild the catalog decided", got)
		}
		if l := lineFor(t, lines, "runtime"); l.Decision != imagecheck.Rebuild {
			t.Fatalf("runtime line = %+v", l)
		}
	})
}
