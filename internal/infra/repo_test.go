package infra

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

func TestRepoWorkdirPath(t *testing.T) {
	spec := sandboxSpec(t)
	got, err := RepoWorkdir(env(map[string]string{"XDG_STATE_HOME": "/s", "HOME": "/h"}), "proj-1234", spec.Slug)
	if err != nil || got != "/s/fugaro/terraform/proj-1234/repos/"+spec.Slug {
		t.Fatalf("workdir = %q, %v", got, err)
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x", "Upper-0123456789abcdef"} {
		if _, err := RepoWorkdir(env(map[string]string{"HOME": "/h"}), "proj-1234", bad); err == nil {
			t.Errorf("slug %q made a path", bad)
		}
	}
	if _, err := RepoWorkdir(env(map[string]string{"HOME": "/h"}), "../p", spec.Slug); err == nil {
		t.Error("a project that is not an ID made a path")
	}
	if got := RepoStatePrefix(spec.Slug); got != StatePrefixRepos+spec.Slug {
		t.Errorf("state prefix = %q", got)
	}
}

// repoRootVariables are the repository root's variables, and whether each
// is required.
func repoRootVariables(t *testing.T) map[string]bool {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, "gcp/roots/repo/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for _, m := range variableRE.FindAllStringSubmatch(string(b), -1) {
		vars[m[1]] = !defaultRE.MatchString(m[2])
	}
	if len(vars) == 0 {
		t.Fatal("no variable found in the repository root")
	}
	return vars
}

// The repository root's tfvars are exactly its variables, allow_job_delete
// included, which only --allow-job-delete sets to true.
func TestRepoRootVarsMatchModule(t *testing.T) {
	vars := repoRootVariables(t)
	for _, in := range []Inputs{sandboxInputs(t, m5Additions), webappInputs(t)} {
		spec, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, allow := range []bool{false, true} {
			data, err := RepoRootVars(spec, RepoRootOptions{AllowJobDelete: allow})
			if err != nil {
				t.Fatal(err)
			}
			keys := jsonKeys(t, data)
			for _, k := range keys {
				if _, ok := vars[k]; !ok {
					t.Errorf("%s: the tfvars key %s is not a variable of the repository root", in.Repo, k)
				}
			}
			for v, required := range vars {
				if required && !slices.Contains(keys, v) {
					t.Errorf("%s: the repository root's required variable %s has no tfvars key", in.Repo, v)
				}
			}
			var doc map[string]any
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["allow_job_delete"] != allow {
				t.Errorf("%s: allow_job_delete = %v, want %v", in.Repo, doc["allow_job_delete"], allow)
			}
			// Everything else is RepoVars, unchanged.
			plain, err := RepoVars(spec)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(plain, &want); err != nil {
				t.Fatal(err)
			}
			delete(doc, "allow_job_delete")
			if !equalJSON(t, doc, want) {
				t.Errorf("%s: the tfvars differ from RepoVars beyond allow_job_delete", in.Repo)
			}
		}
	}
}

// objectAttributes are the attribute names a variable's object type
// declares, at the top level only: terraform fmt indents them by four
// spaces, so a nested object's own attributes (deeper) are not counted and
// neither is the type line itself.
func objectAttributes(t *testing.T, path, name string) []string {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, path)
	if err != nil {
		t.Fatal(err)
	}
	var block string
	for _, m := range variableRE.FindAllStringSubmatch(string(b), -1) {
		if m[1] == name {
			block = m[2]
		}
	}
	if block == "" {
		t.Fatalf("%s declares no variable %s", path, name)
	}
	// Only the type: a validation block's own arguments are indented the
	// same way, and the object's attributes are what is being checked.
	i := strings.Index(block, "type = object({")
	if i < 0 {
		t.Fatalf("%s's variable %s is not an object type", path, name)
	}
	block = block[i:]
	if end := strings.Index(block, "\n  })"); end >= 0 {
		block = block[:end]
	}
	var got []string
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "     ") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if attr, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got = append(got, strings.TrimSpace(attr))
		}
	}
	return got
}

// Both `installation` objects — the repository root's and the repository
// module's — name every attribute of RepoInstallation. An object-typed
// variable silently drops a key its type does not name, so an attribute the
// Go side writes into a repository's tfvars and Terraform does not declare
// would disappear between `fugaro init --repo` and the plan with nothing
// failing anywhere: the credentials service (D146, D147) is the second
// value to travel this way, project_secrets (design
// docs/design/project-secrets.md §5) the first.
func TestRepoInstallationAttributesAreDeclared(t *testing.T) {
	var want []string
	typ := reflect.TypeFor[RepoInstallation]()
	for i := range typ.NumField() {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if tag != "-" {
			want = append(want, tag)
		}
	}
	slices.Sort(want)
	for _, path := range []string{"gcp/roots/repo/variables.tf", "gcp/modules/repo/variables.tf"} {
		got := objectAttributes(t, path, "installation")
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: the installation object declares %q, RepoInstallation's fields are %q", path, got, want)
		}
	}
}

// An installation that has not opted in writes no credentials_service key
// into a repository's tfvars at all, so every existing repository's inputs
// are what they were before the opt-in existed (and the Terraform variable's
// own default, null, is what reads its absence). An installation that has
// opted in carries the value through unchanged: the repository root hands it
// to the workflow module, which drops those secrets from the job accounts'
// accessor grants and grants them run.invoker on the service instead.
func TestRepoVarsCarryTheCredentialsService(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	rootVars := func() []byte {
		t.Helper()
		spec, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		data, err := RepoRootVars(spec, RepoRootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if data := rootVars(); strings.Contains(string(data), "credentials_service") {
		t.Errorf("an installation without the opt-in wrote a credentials_service key:\n%s", data)
	}
	in.Installation.CredentialsService = &CredentialsService{
		Name:    "fugaro-credentials",
		Region:  "us-east5",
		Secrets: []string{"claude-oauth-token"},
	}
	var doc struct {
		Installation struct {
			CredentialsService *CredentialsService `json:"credentials_service"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(rootVars(), &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.Installation.CredentialsService
	if got == nil || !reflect.DeepEqual(*got, *in.Installation.CredentialsService) {
		t.Errorf("the tfvars' installation.credentials_service = %+v, want %+v", got, in.Installation.CredentialsService)
	}
}

func TestInstallationStateExists(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	// A repository's state is not the installation's.
	if err := b.WriteAll(ctx, "fugaro/repos/x-0123456789abcdef/default.tfstate", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := InstallationStateExists(ctx, f.c, testStateBucket); err != nil || ok {
		t.Fatalf("without the installation's state: %v, %v", ok, err)
	}
	// Nor is an object that only starts like it.
	if err := b.WriteAll(ctx, "fugaro/installation-old/default.tfstate", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := InstallationStateExists(ctx, f.c, testStateBucket); err != nil || ok {
		t.Fatalf("with a lookalike prefix: %v, %v", ok, err)
	}
	if err := b.WriteAll(ctx, "fugaro/installation/default.tfstate", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := InstallationStateExists(ctx, f.c, testStateBucket); err != nil || !ok {
		t.Fatalf("with the installation's state: %v, %v", ok, err)
	}
}

func TestForgetRepoStateDeletesOnlyThatRepository(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	spec := sandboxSpec(t)
	other := "other-0123456789abcdef"
	keys := []string{
		"fugaro/installation/default.tfstate",
		RepoStatePrefix(spec.Slug) + "/default.tfstate",
		RepoStatePrefix(other) + "/default.tfstate",
		// A slug that starts with this one's is another repository.
		RepoStatePrefix(spec.Slug) + "x/default.tfstate",
		// A lock is someone's running operation, not state.
		RepoStatePrefix(spec.Slug) + "/default.tflock",
	}
	for _, k := range keys {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := ForgetRepoState(ctx, f.c, testStateBucket, spec.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{keys[1]}; !slices.Equal(deleted, want) {
		t.Fatalf("deleted %q, want %q", deleted, want)
	}
	got, err := RepoStates(ctx, f.c, testStateBucket)
	if err != nil {
		t.Fatal(err)
	}
	// The lock left behind is not state, so it doesn't count.
	if want := slices.Sorted(slices.Values([]string{keys[2], keys[3]})); !slices.Equal(got, want) {
		t.Fatalf("left %q, want %q", got, want)
	}
}

// seedVersions makes every secret of spec exist with our marks, and gives
// the ones in with a version.
func (f *cloud) seedVersions(spec RepoSpec, with ...string) {
	for logical, id := range spec.Secrets {
		var value []byte
		if slices.Contains(with, logical) {
			value = []byte("value-" + logical)
		}
		f.sm.Seed(id, map[string]string{"fugaro": "managed", "fugaro_repo": spec.Label, "fugaro_secret": logical}, value)
	}
}

func TestSecretVersions(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.seedVersions(spec, "bitbucket-token")
	got, err := SecretVersions(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"bitbucket-token": true, "claude-oauth-token": false, "sandbox-probe": false}
	if len(got) != len(want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("versions[%s] = %v, want %v", k, got[k], v)
		}
	}
}

// D2 (plan Task 2.1): a name whose only copy is the project-wide one
// Readiness resolved has a version the job mounts, so nothing is missing.
// SecretVersions is what NeedsBuild gates the first image build on, so
// without this a credential stored with `fugaro secrets set <name>
// --project-wide` leaves the workflow unbuildable and its job undeployed,
// and the "still missing" list prints the repository-copy command for a
// secret that already exists.
//
// The name is bitbucket-token, which the sweep does not take in on its own:
// the project copy is here because the owner's local config allow-lists this
// repository for it (config.PerRepositorySecret, owner ruling of 2026-10-15).
//
// spec is the spec Readiness returned, so ProjectSecrets is its decision,
// read back rather than resolved live again (B1: the grants the apply made
// were fixed for exactly that choice).
//
// Mutation (run, restore): make SecretVersions ignore spec.ProjectSecrets
// and report hasEnabledVersion's answer alone; the project-resolved name
// comes back false and this test fails.
func TestSecretVersionsCountsAProjectResolvedName(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec, err := Repo(sandboxInputs(t, m5Additions+"project_secrets:\n  bitbucket-token: { allow_data_to: [acme/sandbox] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersions(spec) // the repository's own copies exist, none has a version
	id := spec.ProjectSecretCandidates["bitbucket-token"]
	if id == "" {
		t.Fatal("fixture: bitbucket-token is not a project-wide candidate of this repository")
	}
	f.sm.Seed(id, map[string]string{"fugaro": "managed", "fugaro_project": "aurora", "fugaro_secret": "bitbucket-token"}, []byte("project-value"))
	ready := spec
	ready.ProjectSecrets = map[string]string{"bitbucket-token": id}
	got, err := SecretVersions(ctx, f.c, ready)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"bitbucket-token": true, "claude-oauth-token": false, "sandbox-probe": false}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("versions[%s] = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
}

// A workflow is built when it has no build record and every secret it
// mounts has a version; one with a record, or a secret still missing, isn't.
func TestNeedsBuild(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	all := map[string]bool{"bitbucket-token": true, "claude-oauth-token": true, "sandbox-probe": true}

	got, err := NeedsBuild(ctx, f.c, spec, all)
	if err != nil || !slices.Equal(got, []string{"web"}) {
		t.Fatalf("no record, every secret: %q, %v", got, err)
	}
	partial := map[string]bool{"bitbucket-token": true, "claude-oauth-token": false, "sandbox-probe": true}
	if got, err := NeedsBuild(ctx, f.c, spec, partial); err != nil || len(got) != 0 {
		t.Fatalf("a secret without a version: %q, %v", got, err)
	}
	if err := f.gcs.Bucket(t, spec.Installation.RunsBucket).WriteAll(ctx, imagecheck.RecordKey(spec.Slug, "web"), []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := NeedsBuild(ctx, f.c, spec, all); err != nil || len(got) != 0 {
		t.Fatalf("with a record: %q, %v", got, err)
	}
}

// A workflow is built even when the only secret without a version is the
// optional Anthropic key (review fix): NeedsBuild is given the pre-readiness
// spec, whose job still mounts that key, but Readiness drops the mount when no
// version exists anywhere, so the job deploys without it and the image can be
// built. Gating on it here left a keyless provider-only repository with no
// build at all -- and so no job to ever deploy.
//
// Mutation (run, restore): drop `&& !OptionalSecret(l)` from the skip guard in
// NeedsBuild and this test fails with no workflow to build.
func TestNeedsBuildIgnoresTheOptionalAnthropicKey(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	in := sandboxInputs(t, m5Additions)
	in.Cfg.Agent.Auth = "api-key"
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if web := spec.Workflows["web"]; web.SecretEnv["ANTHROPIC_API_KEY"] != config.AnthropicSecret {
		t.Fatalf("the fixture does not mount the Anthropic key: %v", web.SecretEnv)
	}
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	versions := map[string]bool{}
	for logical := range spec.Secrets {
		versions[logical] = logical != config.AnthropicSecret
	}
	got, err := NeedsBuild(ctx, f.c, spec, versions)
	if err != nil || !slices.Equal(got, []string{"web"}) {
		t.Fatalf("got %q, %v, want [web]: an optional secret must not hold the build back", got, err)
	}
	// A secret the job cannot do without still holds it back.
	for logical := range versions {
		if logical != config.AnthropicSecret {
			versions[logical] = false
			break
		}
	}
	if got, err := NeedsBuild(ctx, f.c, spec, versions); err != nil || len(got) != 0 {
		t.Fatalf("a required secret without a version: %q, %v, want no build", got, err)
	}
}

// A checkout: clone workflow never needs a build, with or without a build
// record: it runs the installation's base image directly, so there is no
// per-repository image to build or push.
//
// Mutation (run, restore): change the `ws.Checkout ==
// config.CheckoutClone` clause of the skip guard in NeedsBuild to compare against
// an unreachable value — this test then fails, because "web" (no build
// record, secret satisfied) joins "api" in the result (got ["api" "web"]).
func TestNeedsBuildSkipsClone(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	rs, err := Repo(cloneInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.AddBucket(rs.Installation.RunsBucket, testProjectNumber, managed)
	all := map[string]bool{"github-app-key": true}
	got, err := NeedsBuild(ctx, f.c, rs, all)
	if err != nil || !slices.Equal(got, []string{"api"}) {
		t.Fatalf("got %q, %v, want only api", got, err)
	}
}

// With a named Claude account configured, spec.Workflows holds one entry
// per account ("web" and "web_acme-personal", design
// docs/design/named-oauth-tokens.md §4): they share one image, so
// NeedsBuild must report the logical workflow name once, not the variant
// key, and the named account's own secret (not yet stored) must not block
// the shared build, which is gated on the default account's secrets only
// (the same secrets NeedsBuild already gated on before named accounts
// existed).
func TestNeedsBuildDedupesNamedAccountVariants(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	in := sandboxInputs(t, m5Additions+claudeAccountsLC)
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	if _, ok := spec.Workflows["web_acme-personal"]; !ok {
		t.Fatal("the named account's variant is missing from the fixture")
	}
	// Every variant's own secret has a version, so a dedup bug (reporting
	// one entry per variant key instead of once per logical workflow) would
	// surface as a second, wrongly-named entry here.
	versions := map[string]bool{"bitbucket-token": true, "claude-oauth-token": true, "claude-oauth-token-acme-personal": true, "sandbox-probe": true}
	got, err := NeedsBuild(ctx, f.c, spec, versions)
	if err != nil || !slices.Equal(got, []string{"web"}) {
		t.Fatalf("got %q, %v, want exactly [\"web\"] (the logical name once, never a variant key)", got, err)
	}
	// The named account's own token has no version yet: the shared build
	// must still be gated on it like any other mounted secret of the
	// default variant's own job... but this account's secret is mounted on
	// a different variant's job, not the default's, so it must not block
	// the shared build the default variant is ready for.
	versions = map[string]bool{"bitbucket-token": true, "claude-oauth-token": true, "sandbox-probe": true}
	got, err = NeedsBuild(ctx, f.c, spec, versions)
	if err != nil || !slices.Equal(got, []string{"web"}) {
		t.Fatalf("got %q, %v, want exactly [\"web\"] (gated on the default account only)", got, err)
	}
}

// D2 (plan Task 2.1 step 3b, the message half of finding 13): the command
// a name with a project-wide meaning is missing is the project-scope one,
// the same `fugaro secrets set <name>` its own prompt prints, because
// that is what storing it by default now means. A name with no
// project-wide meaning — a repository's own application secret, the bare
// claude-oauth-token, refused project scope by name, and bitbucket-token,
// whose credential is issued for one repository — keeps the
// repository-copy command, which is the only scope it has.
//
// The second half allow-lists this repository for bitbucket-token in the
// local config, which is the owner's explicit opt-in and the only way that
// name gets a project copy (config.PerRepositorySecret, owner ruling of
// 2026-10-15); the project-scope command comes back with it.
//
// Mutation (run, restore): drop the ProjectSecretCandidates lookup from
// SecretCommands (always `--repo <name>`) and this test fails on the
// allow-listed bitbucket-token line; make it unconditional the other way
// (never `--repo`) and it fails on the claude-oauth-token and
// sandbox-probe lines.
func TestSecretCommandsFollowTheScopeOfEachName(t *testing.T) {
	versions := map[string]bool{"bitbucket-token": false, "claude-oauth-token": false, "sandbox-probe": false}
	spec := sandboxSpec(t)
	got := SecretCommands(spec, versions, "fugaro", func(s string) string { return s })
	want := []SecretStep{
		{Name: "bitbucket-token", What: spec.Secrets["bitbucket-token"], Commands: []string{"fugaro secrets set bitbucket-token --repo acme/sandbox < PATH-TO-THE-VALUE-FILE"}},
		{Name: "claude-oauth-token", What: spec.Secrets["claude-oauth-token"], Commands: []string{"claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/sandbox"}},
		{Name: "sandbox-probe", What: spec.Secrets["sandbox-probe"], Commands: []string{"fugaro secrets set sandbox-probe --repo acme/sandbox < PATH-TO-THE-VALUE-FILE"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n%+v\nwant:\n%+v", got, want)
	}

	listed, err := Repo(sandboxInputs(t, m5Additions+"project_secrets:\n  bitbucket-token: { allow_data_to: [acme/sandbox] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	got = SecretCommands(listed, versions, "fugaro", func(s string) string { return s })
	want[0] = SecretStep{Name: "bitbucket-token", What: listed.Secrets["bitbucket-token"], Commands: []string{"fugaro secrets set bitbucket-token < PATH-TO-THE-VALUE-FILE"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands with the allow-list entry:\n%+v\nwant:\n%+v", got, want)
	}
}

// The commands are the bootstrap's: the value from stdin or a hidden
// prompt, never argv, and claude setup-token run by the user; every command
// is a line of its own.
func TestSecretCommands(t *testing.T) {
	spec := sandboxSpec(t)
	got := SecretCommands(spec, map[string]bool{"bitbucket-token": true, "claude-oauth-token": false, "sandbox-probe": false}, "fugaro", func(s string) string { return s })
	want := []SecretStep{
		{Name: "claude-oauth-token", What: spec.Secrets["claude-oauth-token"], Commands: []string{"claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/sandbox"}},
		{Name: "sandbox-probe", What: spec.Secrets["sandbox-probe"], Commands: []string{"fugaro secrets set sandbox-probe --repo acme/sandbox < PATH-TO-THE-VALUE-FILE"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n%+v\nwant:\n%+v", got, want)
	}
	if got := SecretCommands(spec, map[string]bool{"bitbucket-token": true, "claude-oauth-token": true, "sandbox-probe": true}, "fugaro", func(s string) string { return s }); len(got) != 0 {
		t.Fatalf("every secret stored, still: %+v", got)
	}
}

func TestRepoStateObjects(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	spec := sandboxSpec(t)
	for _, k := range []string{RepoStatePrefix(spec.Slug) + "/default.tfstate", RepoStatePrefix(spec.Slug) + "/default.tflock", RepoStatePrefix(spec.Slug) + "x/default.tfstate"} {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RepoStateObjects(ctx, f.c, testStateBucket, spec.Slug)
	if err != nil || !slices.Equal(got, []string{RepoStatePrefix(spec.Slug) + "/default.tfstate"}) {
		t.Fatalf("state objects = %q, %v", got, err)
	}
	// The lock stays when the state goes.
	if _, err := ForgetRepoState(ctx, f.c, testStateBucket, spec.Slug); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Exists(ctx, RepoStatePrefix(spec.Slug)+"/default.tflock"); err != nil || !ok {
		t.Fatalf("the lock went too (%v, %v)", ok, err)
	}
}

func TestStateBucketVersioned(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	if ok, err := StateBucketVersioned(ctx, f.c, testStateBucket); err != nil || ok {
		t.Fatalf("unversioned: %v, %v", ok, err)
	}
	f.gcs.SetVersioning(testStateBucket, true)
	if ok, err := StateBucketVersioned(ctx, f.c, testStateBucket); err != nil || !ok {
		t.Fatalf("versioned: %v, %v", ok, err)
	}
}

// Each gate says what kind of thing is missing, so callers don't read the
// reason's text.
func TestMissingKinds(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	f.seedVersions(spec, "bitbucket-token")
	_, missing, err := Readiness(ctx, f.c, spec, Existing{}, false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[MissingKind]int{}
	for _, m := range missing {
		kinds[m.Kind]++
		switch m.Kind {
		case MissingSecret:
			if !strings.HasPrefix(m.Reason, "secret ") {
				t.Errorf("a secret gate with reason %q", m.Reason)
			}
		case MissingImage:
			if !strings.Contains(m.Reason, "fugaro image build") {
				t.Errorf("an image gate with reason %q", m.Reason)
			}
		default:
			t.Errorf("unknown kind %q: %+v", m.Kind, m)
		}
	}
	if kinds[MissingSecret] != 2 || kinds[MissingImage] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
}

// --registry-cleanup off deletes nothing anywhere: the base registry's
// cleanup is disabled and stays a dry run, and the dry run the
// installation outputs, which every repository registry copies, stays on.
func TestRegistryCleanupOffDeletesNothing(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	for mode, want := range map[string]RegistryCleanup{
		"":        {Enabled: true, DryRun: true},
		"dry-run": {Enabled: true, DryRun: true},
		"on":      {Enabled: true, DryRun: false},
		"off":     {Enabled: false, DryRun: true},
	} {
		s, err := Installation(lc, InstallOptions{RegistryCleanup: mode})
		if err != nil {
			t.Fatal(err)
		}
		if s.RegistryCleanup != want {
			t.Errorf("%q: registry_cleanup = %+v, want %+v", mode, s.RegistryCleanup, want)
		}
		// The installation root outputs registry_cleanup.dry_run as
		// registry_cleanup_dry_run; the repository's registry copies it.
		in := sandboxInputs(t, m5Additions)
		dry := s.RegistryCleanup.DryRun
		in.Installation.RegistryCleanupDryRun = &dry
		rs, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		if deletes := !rs.Registry.CleanupDryRun; deletes != (mode == "on") {
			t.Errorf("%q: the repository registry's cleanup deletes = %v", mode, deletes)
		}
	}
}

// Operators get everything launchers get: the tfvars' launchers are the
// launchers and the operators, each once, in both roots. The specs keep
// the lists as given, so the local config does too.
func TestOperatorsAreLaunchers(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	s, err := Installation(lc, InstallOptions{Launchers: []string{"user:l@example.com", "user:both@example.com"}, Operators: []string{"user:both@example.com", "user:o@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Launchers, []string{"user:l@example.com", "user:both@example.com"}) {
		t.Errorf("the spec's launchers changed: %q", s.Launchers)
	}
	want := []string{"user:both@example.com", "user:l@example.com", "user:o@example.com"}
	var doc struct {
		Launchers, Operators []string
	}
	data, err := InstallationVars(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(doc.Launchers, want) || !slices.Equal(doc.Operators, []string{"user:both@example.com", "user:o@example.com"}) {
		t.Errorf("installation tfvars launchers %q operators %q", doc.Launchers, doc.Operators)
	}

	in := webappInputs(t)
	in.Installation.Launchers = []string{"user:l@example.com", "user:both@example.com"}
	in.Installation.Operators = []string{"user:both@example.com", "user:o@example.com"}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err = RepoRootVars(rs, RepoRootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var rdoc struct {
		Installation struct{ Launchers, Operators []string }
	}
	if err := json.Unmarshal(data, &rdoc); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rdoc.Installation.Launchers, want) || !slices.Equal(rdoc.Installation.Operators, []string{"user:both@example.com", "user:o@example.com"}) {
		t.Errorf("repository tfvars launchers %q operators %q", rdoc.Installation.Launchers, rdoc.Installation.Operators)
	}
}
