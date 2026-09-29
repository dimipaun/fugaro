package infra

import (
	"context"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
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

// The commands are the bootstrap's: the value from stdin or a hidden
// prompt, never argv, and claude setup-token run by the user.
func TestSecretCommands(t *testing.T) {
	spec := sandboxSpec(t)
	got := SecretCommands(spec, map[string]bool{"bitbucket-token": true, "claude-oauth-token": false, "sandbox-probe": false})
	want := []string{
		"(you, in your own terminal) claude setup-token, then: fugaro secrets set claude-oauth-token --repo acme/sandbox   # " + spec.Secrets["claude-oauth-token"],
		"fugaro secrets set sandbox-probe --repo acme/sandbox < <file holding the value>   # " + spec.Secrets["sandbox-probe"],
	}
	if !slices.Equal(got, want) {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := SecretCommands(spec, map[string]bool{"bitbucket-token": true, "claude-oauth-token": true, "sandbox-probe": true}); len(got) != 0 {
		t.Fatalf("every secret stored, still: %q", got)
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
	_, missing, err := Readiness(ctx, f.c, spec, Existing{})
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
