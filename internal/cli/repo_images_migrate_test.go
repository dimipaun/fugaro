package cli

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The provider behind a bare repository name, resolved once for every writer of
// a repo_images key and for the migration `update` runs (R1, R13). These are the
// tests for the resolution itself: what it reads, what it refuses, and what the
// migration it feeds writes.

// repoKeyConfig is this machine's config as the resolver reads it: the repos
// entries (each with the provider its entry names, which is empty for a
// repository whose provider only a checkout or an allow-list entry can tell),
// and the map the project serves today.
func repoKeyConfig(repos map[string]string, legacy map[string]string) *localcfg.Config {
	lc := &localcfg.Config{Repos: make(map[string]localcfg.Repo, len(repos))}
	for name, provider := range repos {
		lc.Repos[name] = localcfg.Repo{Provider: provider}
	}
	lc.ImageMap = legacy
	return lc
}

// repoKeyCheckout makes a checkout of origin whose fugaro.yaml names git
// provider, and moves the test into it: R1's third source is the working
// directory's checkout of the repository being resolved.
func repoKeyCheckout(t *testing.T, origin, provider string) {
	t.Helper()
	dir := t.TempDir()
	checkoutsAt(t, dir, repoKeyYAML(provider), origin)
	t.Chdir(dir)
}

func repoKeyYAML(provider string) string {
	return "version: 1\nproject: aurora\ngit: { provider: " + provider + ", base_branch: main }\n" +
		"agent:\n" + pinnedAgent + "\n" +
		"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
}

// TestMigrateImageMapOneProvider: the config lists acme/app at GitHub and
// nothing else names it, so the bare key becomes that one provider's key.
func TestMigrateImageMapOneProvider(t *testing.T) {
	lc := repoKeyConfig(map[string]string{"acme/app": "github"}, map[string]string{"acme/app": "go"})
	images, dropped := migrateImageMap(context.Background(), lc, nil)
	want := map[string]string{"github/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v", images, want)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped %q, want nothing: acme/app is enrolled", dropped)
	}
}

// TestMigrateImageMapTwoProviders is the red step's test: a local GitHub entry
// beside a Bitbucket admit is two keys with one entry (D3373), which is what the
// bare key did for both repositories before the migration.
func TestMigrateImageMapTwoProviders(t *testing.T) {
	lc := repoKeyConfig(map[string]string{"acme/app": "github"}, map[string]string{"acme/app": "go"})
	node := &projectpolicy.Policy{AllowRepos: []string{"bitbucket/acme/app"}}
	images, dropped := migrateImageMap(context.Background(), lc, node)
	want := map[string]string{"github/acme/app": "go", "bitbucket/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v", images, want)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped %q, want nothing", dropped)
	}
}

// TestMigrateImageMapUnknownProvider is D3375: the repository is one of the
// project's, and nothing can say which provider it is at (the config lists it
// with no provider, no allow-list entry admits it, and there is no checkout
// here), so it keeps every provider — exactly the repositories the bare key
// reached.
func TestMigrateImageMapUnknownProvider(t *testing.T) {
	lc := repoKeyConfig(map[string]string{"acme/app": ""}, map[string]string{"acme/app": "go"})
	images, dropped := migrateImageMap(context.Background(), lc, nil)
	want := map[string]string{"github/acme/app": "go", "bitbucket/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v", images, want)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped %q, want nothing: acme/app is enrolled", dropped)
	}
}

// TestMigrateImageMapReadsTheCheckoutOfThatRepository: R1's third source is a
// checkout of the repository being migrated, read when neither of the first two
// answers. Here the working directory is acme/app's own checkout, so its
// git.provider is the answer.
func TestMigrateImageMapReadsTheCheckoutOfThatRepository(t *testing.T) {
	repoKeyCheckout(t, "https://github.com/acme/app.git", "bitbucket")
	lc := repoKeyConfig(map[string]string{"acme/app": ""}, map[string]string{"acme/app": "go"})
	images, _ := migrateImageMap(context.Background(), lc, nil)
	want := map[string]string{"bitbucket/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v: the checkout of acme/app names its provider", images, want)
	}
}

// TestMigrateImageMapOtherCheckoutIgnored is the other half of that source: the
// working directory holds a checkout of ANOTHER repository, whose provider says
// nothing about the one being migrated (F33). Reading it would write a key for a
// repository that is not there, and the bare key would stop reaching GitHub.
func TestMigrateImageMapOtherCheckoutIgnored(t *testing.T) {
	repoKeyCheckout(t, "https://github.com/acme/other.git", "bitbucket")
	lc := repoKeyConfig(map[string]string{"acme/app": ""}, map[string]string{"acme/app": "go"})
	images, _ := migrateImageMap(context.Background(), lc, nil)
	want := map[string]string{"github/acme/app": "go", "bitbucket/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v: this checkout is acme/other's, so every provider still answers", images, want)
	}
}

// TestMigrateImageMapWildcardAdmitKept: an allow_repos entry that admits the
// name without spelling the whole path is enough to enroll it (F33). The entry
// names GitHub, so the name becomes that one key.
func TestMigrateImageMapWildcardAdmitKept(t *testing.T) {
	lc := repoKeyConfig(nil, map[string]string{"acme/app": "go"})
	node := &projectpolicy.Policy{AllowRepos: []string{"github/acme/*"}}
	images, dropped := migrateImageMap(context.Background(), lc, node)
	want := map[string]string{"github/acme/app": "go"}
	if !maps.Equal(images, want) {
		t.Fatalf("the migrated map = %v, want %v", images, want)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped %q, want nothing: the allow-list admits acme/app", dropped)
	}
}

// TestMigrateImageMapUnenrolled: no key of acme/old reached a job before the
// migration either, so nothing is written for it and the line naming it says so.
func TestMigrateImageMapUnenrolled(t *testing.T) {
	lc := repoKeyConfig(map[string]string{"acme/app": "github"}, map[string]string{"acme/old": "go"})
	images, dropped := migrateImageMap(context.Background(), lc, nil)
	if len(images) != 0 {
		t.Fatalf("the migrated map = %v, want nothing: acme/old is not one of the project's repositories", images)
	}
	want := []string{"image map: acme/old dropped: no enrolled repository has that name"}
	if !slices.Equal(dropped, want) {
		t.Fatalf("dropped = %q, want %q", dropped, want)
	}
}

// TestMigrateImageMapIdempotent: the map a first run leaves is one a second run
// changes nothing in, whichever member it is read from — the local config, or
// the node that has been published from it.
func TestMigrateImageMapIdempotent(t *testing.T) {
	ctx := context.Background()
	lc := repoKeyConfig(map[string]string{"acme/app": "github"}, map[string]string{"acme/app": "go"})
	first, dropped := migrateImageMap(ctx, lc, nil)
	if len(first) == 0 || len(dropped) != 0 {
		t.Fatalf("the first run = %v, dropped %q", first, dropped)
	}
	// The local config as the migration's own write leaves it: the new member
	// in place of the old one.
	lc.ImageMap = nil
	lc.RepoImages = first
	second, dropped := migrateImageMap(ctx, lc, nil)
	if !maps.Equal(first, second) {
		t.Fatalf("the second run = %v, want the first run's %v", second, first)
	}
	if len(dropped) != 0 {
		t.Fatalf("the second run dropped %q, want nothing", dropped)
	}
	// And the node's half: a node already carrying repo_images answers it as it
	// is, whatever a written image_map beside it says (R2).
	node := &projectpolicy.Policy{
		RepoImages: projectpolicy.RepoImages{"github/acme/app": "svc"},
		ImageMap:   projectpolicy.ImageMap{"acme/app": "go"},
	}
	lc = repoKeyConfig(nil, nil)
	nodeImages, dropped := migrateImageMap(ctx, lc, node)
	want := map[string]string{"github/acme/app": "svc"}
	if !maps.Equal(nodeImages, want) || len(dropped) != 0 {
		t.Fatalf("the node's map = %v, dropped %q, want %v and nothing", nodeImages, dropped, want)
	}
}

// TestRepoImageKeyTakesTheFirstHit: the one-key writers take the first of R1's
// sources that answers — the config entry, an allow-list entry, the checkout of
// that same repository — and the provider a caller already knows (an argument
// written as provider/owner/name) is the key's provider without any read.
func TestRepoImageKeyTakesTheFirstHit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		lc       *localcfg.Config
		node     *projectpolicy.Policy
		repo     string
		provider string
		want     string
	}{
		{
			name: "this machine's config",
			lc:   repoKeyConfig(map[string]string{"acme/app": "github"}, nil),
			repo: "acme/app", want: "github/acme/app",
		},
		{
			name: "the allow-list",
			lc:   repoKeyConfig(nil, nil),
			node: &projectpolicy.Policy{AllowRepos: []string{"bitbucket/acme/*"}},
			repo: "acme/app", want: "bitbucket/acme/app",
		},
		{
			name: "the config's provider wins over the allow-list's other provider",
			lc:   repoKeyConfig(map[string]string{"acme/app": "github"}, nil),
			node: &projectpolicy.Policy{AllowRepos: []string{"github/acme/*", "bitbucket/acme/*"}},
			repo: "acme/app", want: "github/acme/app",
		},
		{
			name:     "the provider the caller already knows",
			lc:       repoKeyConfig(map[string]string{"acme/app": "github"}, nil),
			repo:     "acme/app",
			provider: "bitbucket", want: "bitbucket/acme/app",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repoImageKey(ctx, tc.lc, tc.node, tc.repo, tc.provider)
			if err != nil {
				t.Fatalf("repoImageKey = %v, want %s", err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("repoImageKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRepoImageKeyReadsTheCheckoutOfThatRepository: with neither of the first
// two sources answering, the checkout of the repository in the working directory
// supplies the provider.
func TestRepoImageKeyReadsTheCheckoutOfThatRepository(t *testing.T) {
	repoKeyCheckout(t, "https://github.com/acme/app.git", "bitbucket")
	lc := repoKeyConfig(map[string]string{"acme/app": ""}, nil)
	got, err := repoImageKey(context.Background(), lc, nil, "acme/app", "")
	if err != nil {
		t.Fatalf("repoImageKey = %v, want bitbucket/acme/app", err)
	}
	if got != "bitbucket/acme/app" {
		t.Fatalf("repoImageKey = %q, want bitbucket/acme/app", got)
	}
}

// TestRepoImageKeyRefusesUnservedProvider: no key is written at a provider this
// release does not serve (R10), wherever the kind came from — the argument, the
// config's own entry, or a checkout.
func TestRepoImageKeyRefusesUnservedProvider(t *testing.T) {
	ctx := context.Background()
	t.Run("the argument", func(t *testing.T) {
		lc := repoKeyConfig(map[string]string{"acme/app": "github"}, nil)
		_, err := repoImageKey(ctx, lc, nil, "acme/app", "gitlab")
		refusedForUnservedProvider(t, err)
	})
	t.Run("the config's entry", func(t *testing.T) {
		lc := repoKeyConfig(map[string]string{"acme/app": "gitlab"}, nil)
		_, err := repoImageKey(ctx, lc, nil, "acme/app", "")
		refusedForUnservedProvider(t, err)
	})
	t.Run("the migration writes the served providers instead", func(t *testing.T) {
		lc := repoKeyConfig(map[string]string{"acme/app": "gitlab"}, map[string]string{"acme/app": "go"})
		images, _ := migrateImageMap(ctx, lc, nil)
		want := map[string]string{"github/acme/app": "go", "bitbucket/acme/app": "go"}
		if !maps.Equal(images, want) {
			t.Fatalf("the migrated map = %v, want %v: a provider this release does not serve is never a key", images, want)
		}
	})
}

func refusedForUnservedProvider(t *testing.T, err error) {
	t.Helper()
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"gitlab", "github", "bitbucket"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q, want it to name %q", err, want)
		}
	}
}

// TestRepoImageKeyRefusesDisagreement: two sources that name different providers
// leave a one-key writer nothing to break the tie with, and the refusal names
// the form the person writes instead.
func TestRepoImageKeyRefusesDisagreement(t *testing.T) {
	ctx := context.Background()
	t.Run("the config against the allow-list", func(t *testing.T) {
		lc := repoKeyConfig(map[string]string{"acme/app": "github"}, nil)
		node := &projectpolicy.Policy{AllowRepos: []string{"bitbucket/acme/app"}}
		_, err := repoImageKey(ctx, lc, node, "acme/app", "")
		refusedForTheForm(t, err)
	})
	t.Run("the allow-list admits both providers", func(t *testing.T) {
		lc := repoKeyConfig(nil, nil)
		node := &projectpolicy.Policy{AllowRepos: []string{"github/acme/app", "bitbucket/acme/app"}}
		_, err := repoImageKey(ctx, lc, node, "acme/app", "")
		refusedForTheForm(t, err)
	})
	t.Run("nothing answers", func(t *testing.T) {
		_, err := repoImageKey(ctx, repoKeyConfig(nil, nil), nil, "acme/app", "")
		refusedForTheForm(t, err)
	})
}

func refusedForTheForm(t *testing.T, err error) {
	t.Helper()
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "provider/owner/name") {
		t.Errorf("err %q, want it to name the provider/owner/name form", err)
	}
}
