package cli

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/dimipaun/fugaro/internal/testutil"
)

// settings_access_test.go is RS6b's own behaviour: the writes of the settings,
// config, policy and recipe surfaces ask the resolver before they land, and the
// table is the layer each write lands in — the project layer's own file and the
// published documents are project-level and take admin at project scope, while
// a repository's fugaro.yaml takes admin at a scope covering that repository
// (finding F19, the spec's "repo admins can change repo settings only within
// the guardrails set at project level"). This machine's own project config is
// the row of the table with no role at all: it is a file one person owns, and
// SD18 answers that question as "anyone may edit their own file; the boundary
// stays IAM at publish".
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

// settingsAccessCallerIs points the caller lookup at one answer for the rest of
// the test, so a case about what the gate does with a credential it cannot read
// does not depend on who is signed in on the machine running the suite.
func settingsAccessCallerIs(t *testing.T, err error) {
	t.Helper()
	old := userCallerLookup
	userCallerLookup = func(context.Context) (string, error) { return "", err }
	t.Cleanup(func() { userCallerLookup = old })
}

// settingsAccessCallerFails makes the caller unreadable for the rest of the
// test, for reasons that are nothing to do with the credential being a service
// account's: no credentials, an expired one, an endpoint that did not answer.
// It is the answer the gate refuses on, and the one an installation with no
// roles never reaches (no roles, no judgement).
func settingsAccessCallerFails(t *testing.T) {
	t.Helper()
	settingsAccessCallerIs(t, errors.New("no credential in this test"))
}

// settingsAccessServiceAccount makes the caller a service account's credential:
// the error access.Caller returns for one, sentinel included, which is the only
// unreadable credential this gate admits. It is spelled here rather than
// fetched from access.Caller so the case is about the gate's rule; access's own
// TestCallerIsTheCredentialNotGitConfig pins that Caller returns this error for
// a .gserviceaccount.com address.
func settingsAccessServiceAccount(t *testing.T) {
	t.Helper()
	settingsAccessCallerIs(t, fmt.Errorf("%w (fugaro-run@example.iam.gserviceaccount.com is not a person the grants can name)", accesslib.ErrServiceAccount))
}

// TestAccessSettingsRepoAdminCannotChangeProjectGuardrails: the guardrails of a
// project are its own admins' (spec: "repo admins can change repo settings only
// within the guardrails set at project level"), so an admin whose scope is one
// repository is refused the project layer's file, with the role and the scope
// the write needs named, and the file does not move.
//
// The other file this level of the table holds — this machine's own project
// config — is not judged at all, and the same caller's write lands in it. SD18
// answers that question as it stands ("anyone may edit their own file; the
// boundary stays IAM at publish", spec §8): the file is one person's, on their
// own machine, and what it holds reaches the project through a write that is
// judged — the publishes — or through `update`, under the cloud permissions of
// whoever runs it.
func TestAccessSettingsRepoAdminCannotChangeProjectGuardrails(t *testing.T) {
	r, _ := settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
	layerBefore, localBefore := readFileString(t, layer), readFileString(t, r.cfg)
	userPairAs(t, settingsAccessRepoUser)

	t.Run("the project layer's guardrails", func(t *testing.T) {
		_, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project")
		if err == nil {
			t.Fatal("a repository-scoped admin made a project-level write")
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

	t.Run("this machine's own project config", func(t *testing.T) {
		out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local")
		if err != nil {
			t.Fatalf("a repository-scoped admin was refused their own machine's config file: %v\n%s", err, out)
		}
		if !strings.Contains(readFileString(t, r.cfg), "max_parallel: 5") {
			t.Error("this machine's own project config does not carry the write")
		}
		if readFileString(t, layer) != layerBefore {
			t.Error("the local write changed the project layer file")
		}
	})
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

	out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/sandbox")
	if err != nil {
		t.Fatalf("a repository's own admin was refused its own repository's settings: %v\n%s", err, out)
	}
	if got := readFileString(t, own); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the repository the caller is admin of does not carry the write:\n%s", got)
	}

	_, _, err = execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/other")
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
	_, _, err = execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo")
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

// TestAccessSettingsUnlistedCheckoutIsJudgedFromItsOrigin: the file a --layer
// repo write lands in is resolved from the checkout the command runs in, and
// that checkout need not be a repository this machine's config lists — the cwd
// route of settingsRepoFile asks the config for nothing at all. The judgement
// reads the repository off the same checkout's origin remote, so such a write
// is made by a caller whose role covers it and refused for one whose role does
// not, instead of being refused for everyone with the advice to write the
// repository canonically — a value `--repo` here rejects.
func TestAccessSettingsUnlistedCheckoutIsJudgedFromItsOrigin(t *testing.T) {
	settingsAccessRig(t,
		userStored(t, settingsAccessEveryUser, accesslib.RoleAdmin, "all-repos"),
		userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "github/acme/sandbox"),
	)
	file := settingsWriteCheckout(t, settingsRepoYAML(""))
	testutil.Git(t, filepath.Dir(file), "remote", "add", "origin", "https://github.com/acme/unlisted.git")
	before := readFileString(t, file)

	// The repository is the one the origin names, and this machine's config
	// does not list it: a caller whose scope covers another repository is
	// refused by that name, and the file does not move.
	userPairAs(t, settingsAccessRepoUser)
	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo")
	if err == nil {
		t.Fatal("a repository-scoped admin wrote a repository their scope does not cover")
	}
	if !strings.Contains(err.Error(), "admin at a scope covering github/acme/unlisted") {
		t.Errorf("the refusal does not name the repository the origin names: %v", err)
	}
	if readFileString(t, file) != before {
		t.Error("the refused write changed the unlisted checkout's file")
	}

	userPairAs(t, settingsAccessEveryUser)
	if out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo"); err != nil {
		t.Fatalf("an all-repos admin was refused a checkout this machine's config does not list: %v\n%s", err, out)
	}
	if got := readFileString(t, file); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the checkout whose repository the caller is admin of does not carry the write:\n%s", got)
	}

	// A --repo naming owner/name alone has no origin to read, so it is judged
	// against this machine's config, and the refusal names a remedy this
	// command can take: settingsRepoFile takes owner/name and refuses the
	// canonical form, so canonicalRepoArg's own advice would lead nowhere here.
	settingsCheckout(t, t.TempDir(), "acme/elsewhere", settingsRepoYAML(""))
	_, _, err = execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/elsewhere")
	if err == nil {
		t.Fatal("a --repo naming a repository this machine's config does not list was written")
	}
	if !strings.Contains(err.Error(), "not a repository this machine's config lists") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if strings.Contains(err.Error(), "name it canonically") {
		t.Errorf("the refusal advises a --repo value this command refuses: %v", err)
	}
}

// TestAccessSettingsRewrittenOriginIsJudgedByNobody: the checkout route of a
// --layer repo write takes the repository, and so the role the write needs,
// from the checkout's own origin. A checkout can ship its own .git/config, and
// url.<base>.insteadOf there makes the name git resolves a line the directory's
// author chose rather than a fact about the repository — so the repository
// cannot be told, and the write is refused rather than judged against a name
// that would let a rewritten origin borrow another repository's admin.
//
// Mutation (run, restore): change `if ok && !oi.Rewritten` to `if ok` in
// settingsAccessOriginRepo (settings_access.go) and this fails with the write
// landing in the checkout.
func TestAccessSettingsRewrittenOriginIsJudgedByNobody(t *testing.T) {
	settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "github/acme/app"))
	file := settingsWriteCheckout(t, settingsRepoYAML(""))
	// The raw origin is the gh: shorthand and the rewrite resolves it to the
	// repository the caller is admin of: with the guard gone the write would be
	// allowed, so this isolates the guard and not the role.
	testutil.Git(t, filepath.Dir(file), "remote", "add", "origin", "gh:acme/app.git")
	testutil.Git(t, filepath.Dir(file), "config", "--local", "--add",
		"url.https://github.com/acme/app.git.insteadOf", "gh:acme/app.git")
	if oi, ok := readOrigin(context.Background(), filepath.Dir(file)); !ok || !oi.Rewritten {
		t.Fatalf("the fixture did not produce a rewritten origin: %+v ok=%v", oi, ok)
	}
	before := readFileString(t, file)

	userPairAs(t, settingsAccessRepoUser)
	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo")
	if err == nil {
		t.Fatal("a repository-level write was judged against an origin the checkout's own git config rewrites")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "cannot be told from its origin") {
		t.Errorf("the refusal does not say the repository cannot be told: %v", err)
	}
	if readFileString(t, file) != before {
		t.Error("the refused write changed the checkout's file")
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
// admitted at both judged layers, so the refusal above is a comparison against
// the caller's own scope and not a refusal of the write itself. The one caller
// without a project-scope role is refused the project layer even where its
// scope is every repository (F19: an all-repos admin holds no project-level
// thing) — which is the distinction between the two admin scopes, exercised
// here at the surface where both writes are files. The machine's own config is
// the file neither role is asked about (SD18), so the all-repos admin's write
// there lands as the project admin's does.
func TestAccessSettingsProjectAdminMakesBothWrites(t *testing.T) {
	r, _ := settingsAccessRig(t,
		userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
		userStored(t, settingsAccessEveryUser, accesslib.RoleAdmin, "all-repos"),
	)
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)

	userPairAs(t, settingsAccessProjectUser)
	if out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project"); err != nil {
		t.Fatalf("the project's own admin was refused the project layer: %v\n%s", err, out)
	} else if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
		t.Error("the layer file does not carry the project admin's write")
	}
	if out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local"); err != nil {
		t.Fatalf("the project's own admin was refused the project config: %v\n%s", err, out)
	} else if !strings.Contains(readFileString(t, r.cfg), "max_parallel: 5") {
		t.Error("the project config does not carry the project admin's write")
	}

	// The all-repos admin, on the same two files: every repository's settings
	// are theirs and the project layer is not. The value each write names is
	// one the file above does not hold, so each one is a write and not the
	// "already reads what was asked for" that needs no role at all.
	userPairAs(t, settingsAccessEveryUser)
	layerBefore, localBefore := readFileString(t, layer), readFileString(t, r.cfg)
	if _, _, err := execute(t, "settings", "set", "layer.default_profile", "web", "--layer", "project"); err == nil {
		t.Fatal("an all-repos admin made a project-level write")
	} else if !strings.Contains(err.Error(), "admin at project scope") {
		t.Errorf("the refusal does not name the role and the scope the write needs: %v", err)
	}
	if readFileString(t, layer) != layerBefore {
		t.Error("the refused write changed the project layer file")
	}

	if out, _, err := execute(t, "settings", "set", "max_parallel", "4", "--layer", "local"); err != nil {
		t.Fatalf("an all-repos admin was refused their own machine's config file: %v\n%s", err, out)
	}
	if readFileString(t, r.cfg) == localBefore {
		t.Error("this machine's own project config does not carry the all-repos admin's write")
	}
}

// TestAccessSettingsUnsetAsksTheSameQuestion: `settings unset` makes the same
// write a set does — taking a value away moves what a run gets exactly as
// writing one does — so it is judged in the same place and by the same rule.
// The repository-scoped admin's unset of a project-layer key is refused with the
// layer's file untouched, and the project-scope admin's lands. It is pinned
// here because the two are separate call sites: without this, the unset path
// could lose its judgement and the rest of the family would stay green.
func TestAccessSettingsUnsetAsksTheSameQuestion(t *testing.T) {
	settingsAccessRig(t,
		userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
		userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
	)
	layer := settingsAccessLayerFile(t, settingsAccessLayerProfile+"default_profile: svc\n")
	before := readFileString(t, layer)

	userPairAs(t, settingsAccessRepoUser)
	_, _, err := execute(t, "settings", "unset", "layer.default_profile", "--layer", "project")
	if err == nil {
		t.Fatal("a repository-scoped admin unset a project-layer key")
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "admin at project scope") {
		t.Errorf("the refusal is not the role's: exit %d, %v", ExitCode(err), err)
	}
	if readFileString(t, layer) != before {
		t.Error("the refused unset changed the project layer file")
	}

	userPairAs(t, settingsAccessProjectUser)
	if out, _, err := execute(t, "settings", "unset", "layer.default_profile", "--layer", "project"); err != nil {
		t.Fatalf("the project's own admin was refused the unset: %v\n%s", err, out)
	}
	if strings.Contains(readFileString(t, layer), "default_profile") {
		t.Error("the project admin's unset did not take the key out of the layer file")
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

	out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project")
	if err != nil {
		t.Fatalf("a caller the node names nowhere was refused the project's own layer: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
		t.Error("the layer file does not carry the write")
	}
}

// TestAccessSettingsServiceAccountKeepsThePublishDoorsWorking: a service
// account's credential is not a person the grants can name, so access.Caller
// refuses its address — as access.ErrServiceAccount, which is how the gate
// tells this refusal from an identity it could not read. The two explicit
// publish doors are what a script or a runbook with such a credential uses, and
// they keep working (settings-commands spec §9): this one credential is admitted
// rather than refused. The installations here DO have roles, so a caller the
// node DID place would be judged — without this rule every machine credential
// loses the doors, and a rule that kept any unreadable credential out of the
// check (TestAccessSettingsUnreadableCallerIsRefused) is not what this is:
// admitting a service account is the design's answer for a fact it knows, and
// every other unreadable credential is a question left unanswered.
func TestAccessSettingsServiceAccountKeepsThePublishDoorsWorking(t *testing.T) {
	t.Run("config publish", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, "")
		noAgentSession(t)
		settingsAccessCloudRoles(t, f, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
		settingsAccessServiceAccount(t)
		layer := layerFile(t, testProjectLayer)

		if out, _, err := execute(t, "config", "publish", "--executable-changes", layer); err != nil {
			t.Fatalf("a service account's credential was refused the publish door: %v\n%s", err, out)
		}
		if bucketText(t, f, config.LayerKey) != testProjectLayer {
			t.Error("the publish wrote no layer")
		}
	})

	t.Run("project policy publish", func(t *testing.T) {
		f := newProjectFixture(t, "")
		noAgentSession(t)
		f.db.Set(accesslib.PathNode, accessNodeDoc(userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox")))
		settingsAccessServiceAccount(t)

		if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatalf("a service account's credential was refused the policy door: %v\n%s", err, out)
		}
		if f.db.Value(projectpolicy.PathShared()) == nil {
			t.Error("the publish wrote no policy node")
		}
	})

	t.Run("settings set --layer project", func(t *testing.T) {
		settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
		layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
		settingsAccessServiceAccount(t)

		if out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project"); err != nil {
			t.Fatalf("a service account's credential was refused the project layer: %v\n%s", err, out)
		}
		if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
			t.Error("the layer file does not carry the write")
		}
	})
}

// TestAccessSettingsUnreadableCallerIsRefused: a credential whose caller the
// CLI cannot read is refused, and it is refused on the write whatever the
// caller's roles would have been. A service account's credential is the one
// exception (the test above), because that is a fact about the credential the
// design answers for; no credentials, an expired token, a userinfo endpoint
// that timed out or refused are not — they are a question left unanswered, and
// a gate that admits a caller it could not identify stops checking exactly when
// the credential is broken. The writes here would all LAND if the caller could
// be read (a project-scope admin's layer write; the same admin's publish), so
// what each refusal proves is the unreadable credential alone.
func TestAccessSettingsUnreadableCallerIsRefused(t *testing.T) {
	t.Run("settings set --layer project", func(t *testing.T) {
		settingsAccessRig(t,
			userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
			userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
		)
		layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
		before := readFileString(t, layer)
		settingsAccessCallerIs(t, errors.New("context deadline exceeded"))

		out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project")
		if err == nil {
			t.Fatalf("a caller the CLI could not read made a project-level write:\n%s", out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
		}
		for _, want := range []string{"nothing was written", "whose caller could not be read", "context deadline exceeded"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
		if readFileString(t, layer) != before {
			t.Error("the refused write changed the layer file")
		}
	})

	t.Run("config publish", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, "")
		noAgentSession(t)
		settingsAccessCloudRoles(t, f, userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"))
		settingsAccessCallerIs(t, errors.New("no Google credentials"))
		layer := layerFile(t, testProjectLayer)

		if _, _, err := execute(t, "config", "publish", "--executable-changes", layer); err == nil {
			t.Fatal("a caller the CLI could not read published the project layer")
		}
		if bucketText(t, f, config.LayerKey) != "" {
			t.Error("the refused publish wrote the project layer")
		}
	})
}

// TestAccessSettingsUnreachableDatabaseIsNotJudged: the roles live in the
// budget database, and a settings write edits a file. A database this CLI
// cannot read — an outage, a URL pointing nowhere, another project's — is
// therefore not a refusal and not an error: the write is judged the way an
// installation with no node is judged, by the lists this machine's own config
// carries, and where those say nothing it is not judged at all. Both callers
// here WOULD be judged against the node — one is admin of a single repository
// and is refused the project layer by the test above when the node answers, the
// other is the project's admin — so what the write landing proves is that the
// unreachable database is what changed. Making file edits depend on a network
// read is the alternative, and it takes `--layer project` and `--layer repo`
// away from every installation whose database is down (SD18: the file is the
// person's, and the boundary is IAM at publish).
func TestAccessSettingsUnreachableDatabaseIsNotJudged(t *testing.T) {
	t.Run("a project-layer write", func(t *testing.T) {
		_, db := settingsAccessRig(t,
			userStored(t, settingsAccessProjectUser, accesslib.RoleAdmin, "project"),
			userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"),
		)
		layer := settingsAccessLayerFile(t, settingsAccessLayerProfile)
		db.Refuse(503, "UNAVAILABLE", "", "down")

		userPairAs(t, settingsAccessRepoUser)
		out, _, err := execute(t, "settings", "set", "layer.default_profile", "svc", "--layer", "project")
		if err != nil {
			t.Fatalf("a database that is down refused a write to a file on this machine: %v\n%s", err, out)
		}
		if !strings.Contains(readFileString(t, layer), "default_profile: svc") {
			t.Error("the layer file does not carry the write")
		}
	})

	t.Run("a repo-layer write", func(t *testing.T) {
		r, db := settingsAccessRig(t, userStored(t, settingsAccessRepoUser, accesslib.RoleAdmin, "bitbucket/acme/sandbox"))
		settingsAccessSecondRepo(t, r, "acme/other")
		own := settingsAccessCheckout(t, "acme/sandbox")
		db.Refuse(503, "UNAVAILABLE", "", "down")

		userPairAs(t, settingsAccessRepoUser)
		out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "acme/sandbox")
		if err != nil {
			t.Fatalf("a database that is down refused a write to a checkout's own file: %v\n%s", err, out)
		}
		if got := readFileString(t, own); !strings.Contains(got, "base_branch: develop") {
			t.Errorf("the checkout's fugaro.yaml does not carry the write:\n%s", got)
		}
	})
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

	out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local")
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
