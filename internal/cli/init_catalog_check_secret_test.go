package cli

import (
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

const catalogGitHubLC = `base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
image_catalog:
  entries:
    - name: tools
      tags: [node]
      source: { kind: dockerfile, repo: acme/web, path: .fugaro/tools.Dockerfile }
`

// TestInstallRootNamesTheCatalogGitCredentialThePlanHolds: the installation
// root decides the catalog check's git credential on the containers discovery
// found live, which only installRoot knows. A GitHub repository with no
// project_secrets entry mounts the project's github-app-key when the container
// exists live and its own otherwise; naming the project's copy with none is the
// 404 that stopped fugaro update 0.25.0 -> 0.26.0.
func TestInstallRootNamesTheCatalogGitCredentialThePlanHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool
	}{{"a live project container", true}, {"no project container", false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newInitRig(t)
			r.stateBucket()
			r.appendConfig(t, "  acme/web: { provider: github, github_app_id: \"123456789\", base_branch: main, workflows: [web] }\n")
			r.appendConfig(t, catalogGitHubLC)
			if tc.live {
				projectLabelsSeed(t, r.sm, config.GitHubAppKeySecret, []byte("key"))
			}
			v := r.runInitTfvars(t)
			cc, ok := v["catalog_check"].(map[string]any)
			if !ok {
				t.Fatalf("no catalog_check in the tfvars: %v", v["catalog_check"])
			}
			var spec infra.CatalogCheckJobSpec
			if err := json.Unmarshal([]byte(cc["env"].(map[string]any)[infra.CatalogCheckSpecEnv].(string)), &spec); err != nil {
				t.Fatal(err)
			}
			var web infra.CatalogCheckRepoSpec
			for _, rp := range spec.Repositories {
				if rp.Repo == "acme/web" {
					web = rp
				}
			}
			want := gcp.SecretID(web.Slug, config.GitHubAppKeySecret)
			if tc.live {
				want = gcp.ProjectSecretID(initProjectName, config.GitHubAppKeySecret)
			}
			if web.Slug == "" || web.GitSecretID != want {
				t.Errorf("the env spec names %q for %s, want %s", web.GitSecretID, web.Repo, want)
			}
			for _, row := range cc["repositories"].([]any) {
				if id := row.(map[string]any)["git_secret_id"]; id != want {
					t.Errorf("the tfvars row names %v, want %s", id, want)
				}
			}
		})
	}
}
