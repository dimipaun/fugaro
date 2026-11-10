package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/intake"
)

// The secrets intake's entry point (DS2.1) is not a command anyone is offered:
// it is what the intake image's own entrypoint runs, in the Cloud Run service
// the installation module creates, and like the credentials service's it takes
// no argument, no flag and no config file. So it is hidden twice over -- the
// group and the subcommand -- and the root help is where that is checked
// (TestCredentialsServeHidden's rule, for this process).
func TestIntakeServeHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "intake") {
		t.Fatalf("fugaro --help lists the intake's command:\n%s", out)
	}
	out, _, err = execute(t, "intake", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro intake --help lists serve:\n%s", out)
	}
	// The hiding is the commands' own Hidden field and not an accident of
	// cobra's rules (TestCredentialsServeHidden's own reason): the day this
	// group gains a visible subcommand, `fugaro --help` would list it.
	root := NewRootCmd()
	group := findSub(t, root, "intake")
	if !group.Hidden {
		t.Error("the intake group is not hidden: fugaro --help would list it as soon as it gains a visible subcommand")
	}
	if !findSub(t, group, "serve").Hidden {
		t.Error("intake serve is not hidden: fugaro intake --help would list it")
	}
}

// intakeServeEnv is a complete, valid intake environment: the names the
// installation module sets on the intake's own service (DS2.2's other half),
// with the GCP project the golden fixtures use.
var intakeServeEnv = map[string]string{
	envGCPProject:       "proj-1234",
	envIntakeURL:        "https://fugaro-intake-abcde-uc.a.run.app",
	envLauncherAccounts: `["person-1@example.com"]`,
	envRunsBucket:       "fugaro-runs-proj-1234",
}

// stubIntakeStore is a SecretStore the builder holds and this file never
// serves a call with: what is pinned below is the WIRING (which calls the
// environment names), and the five calls themselves are internal/intake's own
// tests'.
type stubIntakeStore struct{}

func (stubIntakeStore) Describe(context.Context, string) (intake.Secret, bool, error) {
	return intake.Secret{}, false, nil
}

func (stubIntakeStore) Create(context.Context, string, map[string]string, map[string]string) error {
	return nil
}

func (stubIntakeStore) AddVersion(context.Context, string, []byte) (string, error) { return "", nil }

func (stubIntakeStore) List(context.Context, intake.Scope) ([]intake.Secret, error) { return nil, nil }

func (stubIntakeStore) Delete(context.Context, string) error { return nil }

// fakeIntake replaces the two constructors that would need Application Default
// Credentials: the Secret Manager client, and the runs bucket -- which is a
// directory under t.TempDir(), so the configuration's admin list is read from
// the bucket this test names and nothing else is reached.
func fakeIntake(t *testing.T, dir string) {
	t.Helper()
	intakeTest = &intakeWiring{
		secrets: func(context.Context, string) (intake.SecretStore, error) { return stubIntakeStore{}, nil },
		bucket:  func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+dir) },
	}
	t.Cleanup(func() { intakeTest = nil })
}

// TestIntakeServeConfigBuildsFromAFullEnvironment: the four names build a
// configuration the intake accepts, and each piece reaches where it is meant
// to. The admin list is the one check that is more than non-nil: it is read
// out of the bucket FUGARO_RUNS_BUCKET names, so a builder that opened the
// wrong bucket (or none) fails here rather than at a caller asking to write the
// project-shared prefix.
func TestIntakeServeConfigBuildsFromAFullEnvironment(t *testing.T) {
	dir := t.TempDir()
	fakeIntake(t, dir)
	bucket, err := blobx.Open(context.Background(), "file://"+dir)
	if err != nil {
		t.Fatalf("opening the bucket: %v", err)
	}
	defer bucket.Close()
	want := []roleaccess.Admin{{Email: "owner@example.com", Sub: "1042"}}
	raw, err := roleaccess.EncodeAdminList(want)
	if err != nil {
		t.Fatalf("encoding the list: %v", err)
	}
	if err := bucket.Put(context.Background(), roleaccess.PathAdminList, raw, "application/json"); err != nil {
		t.Fatalf("writing the list: %v", err)
	}

	cfg, err := intakeServiceConfig(context.Background(), envFrom(intakeServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("intakeServiceConfig: %v", err)
	}
	for _, piece := range []struct {
		name string
		set  bool
	}{
		{"Callers", cfg.Callers != nil},
		{"Admins", cfg.Admins != nil},
		{"Secrets", cfg.Secrets != nil},
		{"Audit", cfg.Audit != nil},
	} {
		if !piece.set {
			t.Errorf("the configuration has no %s: the intake cannot serve without it", piece.name)
		}
	}
	if _, err := intake.New(cfg); err != nil {
		t.Fatalf("New refused the configuration a complete environment builds: %v", err)
	}
	got, err := cfg.Admins.Admins(context.Background())
	if err != nil {
		t.Fatalf("reading the admin list out of the environment's bucket: %v", err)
	}
	if len(got) != 1 || got[0].Email != want[0].Email || got[0].Sub != want[0].Sub {
		t.Fatalf("the admin list read as %+v, want %+v", got, want)
	}
}

// TestIntakeServeRefusesWithoutItsEnvironment: the three names the intake
// cannot serve without -- the project the containers live in, its own URL and
// the bucket the admin list is read from -- are refused BY NAME and before
// anything is built. FUGARO_LAUNCHER_ACCOUNTS is deliberately not one of them
// and is the test below.
func TestIntakeServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range []string{envGCPProject, envIntakeURL, envRunsBucket} {
		t.Run(k, func(t *testing.T) {
			for n, v := range intakeServeEnv {
				t.Setenv(n, v)
			}
			t.Setenv(k, "")
			_, _, err := execute(t, "intake", "serve")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
		})
	}
}

// TestIntakeServeStartsWithoutTheLauncherList is what the launcher name's
// being OPTIONAL rests on, and it is credentialsServiceLaunchers' own
// direction: the module renders FUGARO_LAUNCHER_ACCOUNTS only on an
// installation whose roster names a launcher, so one that names none must
// start with a class that admits nobody -- fail closed, and every caller
// refused by the class -- rather than be a process that will not bind a port.
func TestIntakeServeStartsWithoutTheLauncherList(t *testing.T) {
	dir := t.TempDir()
	fakeIntake(t, dir)
	env := map[string]string{}
	for n, v := range intakeServeEnv {
		env[n] = v
	}
	env[envLauncherAccounts] = ""
	cfg, err := intakeServiceConfig(context.Background(), envFrom(env), io.Discard)
	if err != nil {
		t.Fatalf("an installation whose roster names no launcher cannot start the intake: %v", err)
	}
	if _, err := intake.New(cfg); err != nil {
		t.Fatalf("New refused the configuration: %v", err)
	}
}

// TestIntakeServeRefusesAMalformedVariable: every name has a shape, and a value
// that is not it is refused by name. The two that matter most are the ones a
// wrong value would silently mis-serve with: an audience that is not this
// service's own URL (a process that admits nobody, or a token minted for
// another service), and a runs bucket that is not a bucket name (a read that
// would reach somewhere else).
func TestIntakeServeRefusesAMalformedVariable(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"the audience is not https", map[string]string{envIntakeURL: "http://fugaro-intake-abcde-uc.a.run.app"}},
		{"the audience carries a path", map[string]string{envIntakeURL: "https://fugaro-intake-abcde-uc.a.run.app/v1"}},
		{"the audience is another scheme", map[string]string{envIntakeURL: "gs://fugaro-intake"}},
		{"the bucket is a URL", map[string]string{envRunsBucket: "gs://fugaro-runs-proj-1234"}},
		{"the bucket carries a path", map[string]string{envRunsBucket: "fugaro-runs-proj-1234/runs"}},
		{"the launcher list is not a list", map[string]string{envLauncherAccounts: `{"launchers":["person-1@example.com"]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for n, v := range intakeServeEnv {
				env[n] = v
			}
			for n, v := range tc.set {
				env[n] = v
			}
			// No wiring stand-in: every refusal below comes before the builder
			// reaches Google for anything, which is the order this pins too.
			if _, err := intakeServiceConfig(context.Background(), envFrom(env), io.Discard); err == nil {
				t.Fatal("want a refusal")
			}
		})
	}
}

// TestIntakeServeRefusalsNeverCarryAValue: this process's log is Cloud
// Logging, and the environment is the deployment's. A refusal names the
// variable and the shape it must have and never what it held -- which is
// credentialsServiceConfig's own rule, held here for the intake's four names.
func TestIntakeServeRefusalsNeverCarryAValue(t *testing.T) {
	value := func(n string) string { return "s3cr3t-" + strings.ToLower(strings.TrimPrefix(n, "FUGARO_")) }
	values := map[string]string{
		envGCPProject:       value(envGCPProject),
		envIntakeURL:        "https://" + value(envIntakeURL) + ".run.app",
		envLauncherAccounts: `["` + value(envLauncherAccounts) + `@example.com"]`,
		envRunsBucket:       value(envRunsBucket),
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"every name is missing", map[string]string{envGCPProject: "", envIntakeURL: "", envRunsBucket: ""}},
		{"the audience is malformed", map[string]string{envIntakeURL: "s3cr3t-url"}},
		{"the bucket is malformed", map[string]string{envRunsBucket: "s3cr3t/bucket"}},
		{"the launcher list is malformed", map[string]string{envLauncherAccounts: "s3cr3t-launchers"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for n, v := range values {
				env[n] = v
			}
			for n, v := range tc.set {
				env[n] = v
			}
			_, err := intakeServiceConfig(context.Background(), envFrom(env), io.Discard)
			if err == nil {
				t.Fatal("want a refusal")
			}
			for n, v := range values {
				if v == "" {
					continue
				}
				if strings.Contains(err.Error(), v) {
					t.Errorf("err = %v, which carries the value of %s", err, n)
				}
			}
		})
	}
}

// GODEBUG=http2debug makes Go print every HTTP request it handles, headers
// included, to stderr -- and this process handles the calls that carry a
// credential a person handed in. The refusal is the one every command of this
// repository's that touches a credential makes (refuseHTTP2Debug), and it
// comes before the environment is read, so nothing at all happens under it.
func TestIntakeServeRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "intake", "serve")
	if err == nil || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("err = %v, want the http2debug refusal", err)
	}
}

// TestIntakeServeRefusesHTTP2DebugStill is the credential-serving half of the
// refusal, held from both ends: an EMPTY environment is refused for http2debug
// and not for its first missing variable, and a COMPLETE one -- the environment
// that builds a configuration intake.New accepts -- is refused too, with no
// listener ever bound. The context is bounded so that a version of this code
// that served instead of refusing fails the test rather than hanging it.
func TestIntakeServeRefusesHTTP2DebugStill(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "empty", env: map[string]string{}},
		{name: "complete", env: intakeServeEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"GODEBUG": "http2debug=2"}
			for k, v := range tc.env {
				env[k] = v
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serveIntake(ctx, newIntakeServeCmd(), envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "http2debug") {
				t.Fatalf("err = %v, want the http2debug refusal: a process that served would print the headers of a call that carries a credential", err)
			}
		})
	}
}

// TestIntakeServeDoesNotEchoTheBucketWhenItCannotOpenIt is the no-echo rule at
// the one place a wrapping error would break it: blobx's own error quotes the
// URL it was handed, which is the value of FUGARO_RUNS_BUCKET, and this
// process's log is Cloud Logging. The refusal names the variable and what it
// must be, and nothing the deployment held.
func TestIntakeServeDoesNotEchoTheBucketWhenItCannotOpenIt(t *testing.T) {
	intakeTest = &intakeWiring{
		secrets: func(context.Context, string) (intake.SecretStore, error) { return stubIntakeStore{}, nil },
		bucket: func(context.Context, string) (*blobx.Bucket, error) {
			return nil, errors.New("opening bucket gs://s3cr3t-bucket-name: the account cannot read it")
		},
	}
	t.Cleanup(func() { intakeTest = nil })
	_, err := intakeServiceConfig(context.Background(), envFrom(intakeServeEnv), io.Discard)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), envRunsBucket) {
		t.Fatalf("err = %v, want a refusal naming %s", err, envRunsBucket)
	}
	for _, value := range []string{"s3cr3t-bucket-name", intakeServeEnv[envRunsBucket]} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("err = %v, which carries %q", err, value)
		}
	}
}

// envFrom is os.Getenv over a map, so a test's environment is exactly the
// variables it names and nothing the process happens to carry.
func envFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}
