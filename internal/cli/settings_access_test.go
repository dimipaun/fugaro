package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// settings_access_test.go is RS6b's own behaviour: the writes of the settings,
// config, policy and recipe surfaces ask the resolver before they land, and the
// table is the layer each write lands in — the project's own files and the
// published document are project-level and take admin at project scope, while a
// repository's fugaro.yaml takes admin at a scope covering that repository
// (finding F19, the spec's "repo admins can change repo settings only within
// the guardrails set at project level").
//
// Every case here is a caller the grants node places: the resolver binds a
// caller the node names, and the two callers the node does not name — this
// installation's GCP owners and editors (F17.3) — are pinned as the stated
// limit they are (TestAccessSettingsUnplacedCallerIsTheImplicitAdmin). The
// caller is stubbed (userPairAs) because it is a credential's verified address
// on a real invocation (access.Caller, F21): a test that reached Google's
// userinfo endpoint would be neither hermetic nor about the judgement.

// The four callers these cases judge as: one repository's admin, one
// installation's project admin, an admin of every repository, and an address
// the grants node names nowhere.
const (
	settingsAccessRepoUser    = "user:repo-admin@example.com"
	settingsAccessProjectUser = "user:project-admin@example.com"
	settingsAccessEveryUser   = "user:every-repo-admin@example.com"
	settingsAccessOwnerUser   = "user:owner@example.com"
)

// settingsAccessRig is an installation whose roles are the grants node: the
// fake budget database a converge reads and derives from, and the node the test
// leaves it holding. This machine's own config carries no terraform lists, so
// the node is the whole of the installation's roles and the caller is judged
// against it alone.
func settingsAccessRig(t *testing.T, grants ...accesslib.Grant) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	return userPairRig(t, userPairLists{}, grants...)
}

// settingsAccessLayerFile writes the rig's own local project layer file, the
// way `fugaro update` seeds it: the anchors its loader requires, rooted at the
// GCP project the rig's config names (which settingsWriteLayerFile is not — its
// fixture is another project's).
func settingsAccessLayerFile(t *testing.T, extra string) string {
	t.Helper()
	path, err := localcfg.LayerFile(os.Getenv, initProjectName)
	if err != nil {
		t.Fatal(err)
	}
	// The rig has written no layer file yet, so its directory is not there
	// either: `fugaro update` is what seeds both.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "version: 1\nproject: " + initProjectName + "\ngcp_project: " + initProject + "\n" + extra
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsAccessLayerProfile is the layer file's own profile block: a profile
// the layer's loader accepts, which `layer.default_profile` may then name.
const settingsAccessLayerProfile = "profiles:\n  svc:\n    description: the service profile\n  web:\n    description: the web profile\n"

// settingsAccessSecondRepo lists a second repository in the rig's own config,
// so a case can name a repository the caller's scope does not cover. The
// reduction of a --repo argument to a canonical repository takes the provider
// from this machine's config, so a repository a case names has to be here for
// the write to be placeable at all.
func settingsAccessSecondRepo(t *testing.T, r *initRig, repo string) {
	t.Helper()
	const first = "  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	body := readFileString(t, r.cfg)
	if !strings.Contains(body, first) {
		t.Fatalf("the rig's config does not list acme/sandbox the way this fixture widens it:\n%s", body)
	}
	line := "  " + repo + ": { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(body, first, first+line, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// settingsAccessCheckout remembers a checkout of repo carrying a repository's
// smallest fugaro.yaml, and answers with the file a --layer repo write lands
// in.
func settingsAccessCheckout(t *testing.T, repo string) string {
	t.Helper()
	return filepath.Join(settingsCheckout(t, t.TempDir(), repo, settingsRepoYAML("")), "fugaro.yaml")
}

// settingsAccessCloudRoles gives a cloud fixture's installation the budget
// backend and the grants node its roles are read from: the two facts a setting
// write's own judgement reads, on a rig whose bucket is a file:// directory.
func settingsAccessCloudRoles(t *testing.T, f *cloudFixture, grants ...accesslib.Grant) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, auroraMark())
	db.Set(accesslib.PathNode, accessNodeDoc(grants...))
	f.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
}

// settingsAccessCallerFails makes the caller unreadable for the rest of the
// test: a command that reads it fails on it, so the case below — a write an
// installation with no roles makes — holds without asserting on a call count.
func settingsAccessCallerFails(t *testing.T) {
	t.Helper()
	old := userCallerLookup
	userCallerLookup = func(context.Context) (string, error) { return "", errors.New("no credential in this test") }
	t.Cleanup(func() { userCallerLookup = old })
}

// TestAccessSettingsRepoAdminCannotChangeProjectGuardrails: the guardrails of a
// project are its own admins' (spec: "repo admins can change repo settings only
// within the guardrails set at project level"), so an admin whose scope is one
// repository is refused both project-level writes — the project layer file, and
// this machine's own project config — with the role and the scope the write
// needs named, and neither file moves.
func TestAccessSettingsRepoAdminCannotChangeProjectGuardrails(t *testing.T) {
	r, _ := settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
	layerBefore, localBefore := readFileString(t, layer), readFileString(t, r.cfg)
	userPairAs(t, settingsAccessRepoUser)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"the project layer's guardrails", []string{"settings", "set", "layer.default_profile", "svc", "--layer", "project", "--yes"}},
		{"this machine's own project config", []string{"settings", "set", "max_parallel", "5", "--layer", "local", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("a repository-scoped admin made a project-level write: %v", tc.args)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "admin at project scope") {
				t.Errorf("the refusal does not name the role and the scope the write needs: %v", err)
			}
			if !strings.Contains(err.Error(), "finding F19") {
				t.Errorf("the refusal does not say where the rule comes from: %v", err)
			}
			if !strings.Contains(err.Error(), "admin at bitbucket/acme/sandbox") {
				t.Errorf("the refusal does not say what the caller holds: %v", err)
			}
			if readFileString(t, layer) != layerBefore || readFileString(t, r.cfg) != localBefore {
				t.Error("the refused write changed a project file")
			}
		})
	}
}

// TestAccessSettingsRepoAdminChangesRepoSettingWithinGuardrail: the other half
// of that rule. The same repository-scoped admin writes their own repository's
// fugaro.yaml — the file a run of it reads, and the one write their role is for
// — and the same command aimed at a repository their scope does not cover is
// refused, with that repository's file untouched.
func TestAccessSettingsRepoAdminChangesRepoSettingWithinGuardrail(t *testing.T) {
	r, _ := settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
	settingsAccessSecondRepo(t, r, "acme/other")
	own, other := settingsAccessCheckout(t, "acme/sandbox"), settingsAccessCheckout(t, "acme/other")
	otherBefore := readFileString(t, other)
	userPairAs(t, settingsAccessRepoUser)

	out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/sandbox", "--yes")
	if err != nil {
		t.Fatalf("a repository's own admin was refused its own repository's settings: %v\n%s", err, out)
	}
	if got := readFileString(t, own); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the repository the caller is admin of does not carry the write:\n%s", got)
	}

	_, _, err = execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/other", "--yes")
	if err == nil {
		t.Fatal("a repository-scoped admin changed a repository their scope does not cover")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "admin at a scope covering bitbucket/acme/other") {
		t.Errorf("the refusal does not name the role and the repository the write needs: %v", err)
	}
	if readFileString(t, other) != otherBefore {
		t.Error("the refused write changed the repository that was not the caller's")
	}

	// A repository-level write is judged in the repository it lands in, and a
	// checkout whose origin names none is one this command cannot judge: it is
	// refused rather than guessed at, and the file it would have edited is
	// untouched. (An installation with no roles never gets as far as reading the
	// origin: TestAccessSettingsInstallationWithoutRolesIsUnchanged.)
	anon := settingsWriteCheckout(t, settingsRepoYAML(""))
	anonBefore := readFileString(t, anon)
	_, _, err = execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("a repository-level write landed in a checkout whose repository cannot be told")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "cannot be told from its origin") || !strings.Contains(err.Error(), "--repo owner/name") {
		t.Errorf("the refusal does not say what is missing or what to pass: %v", err)
	}
	if readFileString(t, anon) != anonBefore {
		t.Error("the refused write changed the anonymous checkout's file")
	}
}

// TestAccessSettingsDryRunAnswersWithoutARole: the gate is asked for a write,
// and a --dry-run makes none. A caller the roles refuse gets the document the
// write would produce — the diff is the change they hand an admin, and the file
// it describes is one they can already read — and the file it would have been
// written to is untouched either way.
func TestAccessSettingsDryRunAnswersWithoutARole(t *testing.T) {
	settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
	before := readFileString(t, layer)
	userPairAs(t, settingsAccessRepoUser)

	out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("a dry run of a write the caller may not make was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "default_profile") {
		t.Errorf("the dry run did not answer with the edit it would make:\n%s", out)
	}
	if readFileString(t, layer) != before {
		t.Error("the dry run wrote the file")
	}
}

// TestAccessSettingsProjectAdminMakesBothWrites: the project-scope admin is
// admitted at both layers, so the refusals above are a comparison against the
// caller's own scope and not a refusal of the writes themselves. The one
// caller without a project-scope role is refused the project's own files even
// where its scope is every repository (F19: an all-repos admin holds no
// project-level thing) — which is the distinction between the two admin scopes,
// exercised here at the surface where both writes are files.
func TestAccessSettingsProjectAdminMakesBothWrites(t *testing.T) {
	r, _ := settingsAccessRig(t,
		userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
		userStored(t, settingsAccessEveryUser, accesslib.RoleAdmin, "all-repos"),
	)
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)

	userPairAs(t, settingsAccessProjectUser)
	if out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project", "--yes"); err != nil {
		t.Fatalf("the project's own admin was refused the project layer: %v\n%s", err, out)
	} else if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
		t.Error("the layer file does not carry the project admin's write")
	}
	if out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("the project's own admin was refused the project config: %v\n%s", err, out)
	} else if !strings.Contains(readFileString(t, r.cfg), "max_parallel: 5") {
		t.Error("the project config does not carry the project admin's write")
	}

	// The all-repos admin, on the same two files: every repository's settings
	// are theirs and the project's are not. Each write names a value the file
	// above does not hold, so each one is a write and not the "already reads
	// what was asked for" that needs no role at all.
	userPairAs(t, settingsAccessEveryUser)
	layerBefore, localBefore := readFileString(t, layer), readFileString(t, r.cfg)
	for _, args := range [][]string{
		{"settings", "set", "layer.default_profile", "web", "--layer", "project", "--yes"},
		{"settings", "set", "max_parallel", "4", "--layer", "local", "--yes"},
	} {
		if _, _, err := execute(t, args...); err == nil {
			t.Fatalf("an all-repos admin made a project-level write: %v", args)
		} else if !strings.Contains(err.Error(), "admin at project scope") {
			t.Errorf("the refusal does not name the role and the scope the write needs: %v", err)
		}
	}
	if readFileString(t, layer) != layerBefore || readFileString(t, r.cfg) != localBefore {
		t.Error("the refused write changed a project file")
	}
}

// TestAccessSettingsUnplacedCallerIsTheImplicitAdmin: the stated limit. This
// installation's GCP owners and editors are implicit project admins (F17.3) and
// live in its IAM policy, which the CLI does not read here — so a caller the
// grants node names nowhere is treated as the implicit admin the design names
// rather than refused, exactly as `fugaro user grant` treats one (its own doc
// says why: the write itself is the answer there, and the files here are ones
// the person on this machine can edit with an editor anyway). A caller the node
// DOES place is judged against their own grants, which is what the two tests
// above hold.
func TestAccessSettingsUnplacedCallerIsTheImplicitAdmin(t *testing.T) {
	settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
	userPairAs(t, settingsAccessOwnerUser)

	out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("a caller the node names nowhere was refused the project's own layer: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
		t.Error("the layer file does not carry the write")
	}
}

// TestAccessSettingsInstallationWithoutRolesIsUnchanged: an installation with
// no grants node and no lists of its own is the world before roles existed —
// its machine's project config is the whole of its roles — so the writes land
// and the credential is never read. This is the state every installation that
// has not migrated its lists is in, and the one the update that migrates them
// leaves behind it.
func TestAccessSettingsInstallationWithoutRolesIsUnchanged(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	settingsAccessCallerFails(t)

	out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("an installation with no roles refused its own settings write: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, localPath), "max_parallel: 5") {
		t.Error("the local config does not carry the write")
	}
}

// TestAccessConfigWritesAskTheResolver: the three published-document writes ask
// the same question as the settings writes, and for the same reason — the
// project layer every repository of the project resolves, the policy node every
// run is admitted by, and the project's recipes are project-level surfaces
// (F19). A repository-scoped admin is refused all three with nothing written,
// and a project-scope admin makes all three; the config publish is also checked
// for what it did not write, because that is the half a refusal has to prove.
func TestAccessConfigWritesAskTheResolver(t *testing.T) {
	t.Run("config publish", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, "")
		noAgentSession(t)
		settingsAccessCloudRoles(t, f,
			userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
			userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
		)
		layer := layerFile(t, testProjectLayer)

		userPairAs(t, settingsAccessRepoUser)
		_, _, err := execute(t, "config", "publish", "--executable-changes", layer)
		if err == nil {
			t.Fatal("a repository-scoped admin published the project layer")
		}
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "admin at project scope") {
			t.Errorf("the refusal is not the role's: exit %d, %v", ExitCode(err), err)
		}
		if bucketText(t, f, config.LayerKey) != "" {
			t.Error("the refused publish wrote the project layer")
		}

		userPairAs(t, settingsAccessProjectUser)
		if out, _, err := execute(t, "config", "publish", "--executable-changes", layer); err != nil {
			t.Fatalf("the project's own admin was refused the publish: %v\n%s", err, out)
		}
		if bucketText(t, f, config.LayerKey) != testProjectLayer {
			t.Error("the project admin's publish did not write the layer")
		}
	})

	t.Run("recipes publish", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		noAgentSession(t)
		settingsAccessCloudRoles(t, f,
			userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
			userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
		)
		path := writeRecipeFile(t, t.TempDir(), "one.yaml", "version: 1\nname: one\nsteps:\n  - review: {}\n")
		key := filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("one")))

		userPairAs(t, settingsAccessRepoUser)
		_, _, err := execute(t, "recipes", "publish", path)
		if err == nil {
			t.Fatal("a repository-scoped admin published a project recipe")
		}
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "admin at project scope") {
			t.Errorf("the refusal is not the role's: exit %d, %v", ExitCode(err), err)
		}
		if _, err := os.Stat(key); err == nil {
			t.Error("the refused publish wrote the recipe")
		}

		userPairAs(t, settingsAccessProjectUser)
		if out, _, err := execute(t, "recipes", "publish", path); err != nil {
			t.Fatalf("the project's own admin was refused the publish: %v\n%s", err, out)
		}
		if _, err := os.Stat(key); err != nil {
			t.Errorf("the project admin's publish did not write the recipe: %v", err)
		}
	})

	t.Run("project policy publish", func(t *testing.T) {
		f := newProjectFixture(t, "")
		noAgentSession(t)
		f.db.Set(accesslib.PathNode, accessNodeDoc(
			userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
			userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
		))

		userPairAs(t, settingsAccessRepoUser)
		_, _, err := execute(t, "project", "policy", "publish")
		if err == nil {
			t.Fatal("a repository-scoped admin published the project policy")
		}
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "admin at project scope") {
			t.Errorf("the refusal is not the role's: exit %d, %v", ExitCode(err), err)
		}
		if f.db.Value(projectpolicy.PathShared()) != nil {
			t.Error("the refused publish wrote the policy node")
		}

		userPairAs(t, settingsAccessProjectUser)
		if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatalf("the project's own admin was refused the publish: %v\n%s", err, out)
		}
		if f.db.Value(projectpolicy.PathShared()) == nil {
			t.Error("the project admin's publish did not write the policy node")
		}
	})
}
