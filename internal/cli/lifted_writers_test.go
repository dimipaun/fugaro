package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestWritersRefuseLiftedKeys is R15 at the two writers S4 owns: a lifted key
// is the project's now (D3237), so a write of one into a repository is the trap
// F23 raised — a write release A's runs drop — and it is refused with the one
// sentence C1 holds for the key (LiftedKey.Why), whose fix is the command that
// states it in its new home (`fugaro models use --layer project` for a model,
// `fugaro rigor set … --repo` for a rigor level). Nothing is written.
//
// The keys are read off config.LiftedKeys rather than listed here: the contract
// is the only answer to "is this key lifted", so a key the ruling adds arrives
// at both writers with no edit to this test. Each writer gets the keys it takes
// — every one of them is family-owned, so `settings set` is the generic path
// being told where the key really lives, and `models use` names the model keys
// and refuses the rest as names it does not write.
func TestWritersRefuseLiftedKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		// args is the writer's command line for one key, with the repository
		// named positionally for the family command and --repo for the generic
		// one: both are a write into a repository.
		args func(key string) []string
		keys func() []config.LiftedKey
	}{
		{
			name: "settings set",
			args: func(key string) []string {
				return []string{"settings", "set", key, "x", "--layer", "repo", "--repo", "owner/repo-a"}
			},
			keys: func() []config.LiftedKey { return config.LiftedKeys },
		},
		{
			name: "models use",
			args: func(key string) []string {
				return []string{"models", "use", "owner/repo-a", key, "deepseek/deepseek-v4-pro"}
			},
			keys: func() []config.LiftedKey {
				out := []config.LiftedKey{}
				for _, k := range config.LiftedKeys {
					if _, _, ok := modelsUseBase(k.Key); ok {
						out = append(out, k)
					}
				}
				return out
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			keys := c.keys()
			if len(keys) == 0 {
				t.Fatalf("%s takes no lifted key, and this test is about their refusal", c.name)
			}
			for _, k := range keys {
				t.Run(k.Key, func(t *testing.T) {
					modelsUseHome(t)
					repoFile := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
					before := readFileString(t, repoFile)

					_, _, err := execute(t, c.args(k.Key)...)
					if err == nil {
						t.Fatalf("%s wrote the lifted key %s into a repository:\n%s", c.name, k.Key, readFileString(t, repoFile))
					}
					if got := err.Error(); strings.TrimSpace(got) != k.Why {
						t.Errorf("the refusal is not the sentence C1 holds for %s:\ngot:  %s\nwant: %s", k.Key, got, k.Why)
					}
					if !strings.Contains(err.Error(), k.Home) && !strings.Contains(err.Error(), "project") {
						t.Errorf("the refusal does not say where the key lives now: %v", err)
					}
					if got := readFileString(t, repoFile); got != before {
						t.Errorf("the refused write changed the repository's file:\n%s", got)
					}
				})
			}
		})
	}
}

// TestModelsUseDefaultsToProject is S4's red step at this command: a model is
// the project's choice (D3237), so `models use` writes the project layer when
// --layer is not named — for a role name, for a key's own spelling and for a
// level's model — and no bare write of one reaches a repository. A key a
// repository still states keeps the checkout as its default, so the rule is
// about the lifted key and not about the command's flag.
func TestModelsUseDefaultsToProject(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	layer := settingsWriteLayerFile(t, modelsUseLayerAuth)
	repoFile := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	before := readFileString(t, repoFile)
	beforeLayer := readFileString(t, layer)

	for _, key := range []string{"coder", "agent.model", "agent.levels.l2.model"} {
		out, _, err := execute(t, "models", "use", key, "deepseek/deepseek-v4-pro")
		if err != nil {
			t.Fatalf("%s: %v\n%s", key, err, out)
		}
	}
	l, ps := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
	if len(ps) > 0 {
		t.Fatalf("the layer does not read back after the writes: %s\n%s", ps[0].String(), readFileString(t, layer))
	}
	if got := l.Defaults.Agent.Models.Coder; got != "deepseek/deepseek-v4-pro" {
		t.Errorf("defaults.agent.models.coder = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
	}
	if got := l.Defaults.Agent.Model; got != "deepseek/deepseek-v4-pro" {
		t.Errorf("defaults.agent.model = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
	}
	if got := l.Defaults.Agent.Levels.L2; got == nil || got.Model != "deepseek/deepseek-v4-pro" {
		t.Errorf("defaults.agent.levels.l2.model = %v, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
	}
	if got := readFileString(t, repoFile); got != before {
		t.Errorf("a bare model write reached a repository's own file:\n%s", got)
	}
	if !strings.Contains(beforeLayer, "auth: api-key") {
		t.Fatalf("the fixture's layer is not the one the writes were judged against:\n%s", beforeLayer)
	}

	out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
	if err != nil {
		t.Fatalf("a repository's own key was refused its default layer: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, repoFile), "recipe: solo") {
		t.Errorf("agent.recipe did not default to the repository's own file:\n%s", readFileString(t, repoFile))
	}
}
