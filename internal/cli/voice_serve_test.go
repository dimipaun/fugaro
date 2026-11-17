package cli

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/voicesvc"

	"github.com/spf13/pflag"
	"golang.org/x/oauth2"
)

// The voice hop's entry point (P5.3.3) is not a command anyone is offered: it
// is what the hop's own revision runs, and like the credentials service's and
// the intake's it takes no argument, no flag and no config file. So it is
// hidden twice over -- the group and the subcommand -- and the root help is
// where that is checked (TestCredentialsServeHidden's rule, for this process).
func TestVoiceServeHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "voice") {
		t.Fatalf("fugaro --help lists the hop's command:\n%s", out)
	}
	out, _, err = execute(t, "voice", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro voice --help lists serve:\n%s", out)
	}
	// The hiding is the commands' own Hidden field and not an accident of
	// cobra's rules (TestCredentialsServeHidden's own reason): the day this
	// group gains a visible subcommand, `fugaro --help` would list it.
	root := NewRootCmd()
	group := findSub(t, root, "voice")
	if !group.Hidden {
		t.Error("the voice group is not hidden: fugaro --help would list it as soon as it gains a visible subcommand")
	}
	if !findSub(t, group, "serve").Hidden {
		t.Error("voice serve is not hidden: fugaro voice --help would list it")
	}
}

// voiceServeEnv is a complete hop environment: the two names voice.tf sets on
// the service, with the database named after the Firebase project that owns it
// (which is the pair credentialsServiceDatabase holds together). FUGARO_* is
// the only thing here that is not a name of this repository's own.
var voiceServeEnv = map[string]string{
	envFirebaseFP: "proj-1234",
	envRTDBURL:    "https://proj-1234-default-rtdb.firebaseio.com",
}

// fakeVoice replaces the one constructor that would need Application Default
// Credentials -- the token source the database is read with -- with a fixed
// token. Nothing is reached: the client is built and no call is made through
// it, which is the whole of what this file pins (the three parts of the
// service and which names reach them; the calls themselves are
// internal/voicesvc's own tests', over a database fake).
func fakeVoice(t *testing.T) {
	t.Helper()
	voiceTest = &voiceWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return oauth2.StaticTokenSource(&oauth2.Token{TokenType: "Bearer"}), nil
		},
	}
	t.Cleanup(func() { voiceTest = nil })
}

// refuseVoiceWiring stands in for that constructor so that it cannot be
// reached quietly: an environment the builder should have refused by name is
// refused before anything is built, and a case that only asked for "an error"
// would pass on a failure that came from Google instead.
func refuseVoiceWiring(t *testing.T) {
	t.Helper()
	voiceTest = &voiceWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			t.Error("the builder reached for a credential for an environment it must refuse first")
			return nil, errors.New("reached Application Default Credentials")
		},
	}
	t.Cleanup(func() { voiceTest = nil })
}

// TestVoiceServeConfigBuildsFromAFullEnvironment: the two names build a
// configuration the hop's own server accepts, and each piece reaches where it
// is meant to. The verifier is checked by asking it to refuse something: a
// string that is not a JWT is refused by the verifier's own shape rule and
// before any key is looked up, so this proves the verifier is the one the
// environment built without reaching Google's certificate endpoint.
func TestVoiceServeConfigBuildsFromAFullEnvironment(t *testing.T) {
	fakeVoice(t)
	cfg, err := voiceServiceConfig(context.Background(), envFrom(voiceServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("voiceServiceConfig: %v", err)
	}
	for _, piece := range []struct {
		name string
		set  bool
	}{
		{"Auth", cfg.Auth != nil},
		{"Devices", cfg.Devices != nil},
		{"Enforcer", cfg.Enforcer != nil},
		{"Log", cfg.Log != nil},
	} {
		if !piece.set {
			t.Errorf("the configuration has no %s: the hop cannot serve without it", piece.name)
		}
	}
	if _, err := voicesvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the configuration a complete environment builds: %v", err)
	}
	_, err = cfg.Auth.VerifyDevice(context.Background(), "not.a.jwt")
	if got := voicesvc.ReasonOf(err); got != voicesvc.ReasonTokenUnverified {
		t.Fatalf("the verifier answered %q (%v), want %q: the environment's Firebase project did not reach it", got, err, voicesvc.ReasonTokenUnverified)
	}
}

// TestVoiceServeRefusesWithoutItsEnvironment: the two names the hop cannot
// serve without are refused BY NAME and before anything is built. Each is a
// fact of the installation that cannot be derived from the other, and a hop
// that guessed either would admit identities it cannot verify or read a
// database that is not its installation's.
func TestVoiceServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range []string{envFirebaseFP, envRTDBURL} {
		t.Run(k, func(t *testing.T) {
			refuseVoiceWiring(t)
			for n, v := range voiceServeEnv {
				t.Setenv(n, v)
			}
			t.Setenv(k, "")
			_, _, err := execute(t, "voice", "serve")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
		})
	}
}

// TestVoiceServeRefusesADatabaseOfAnotherProject is credentialsServiceDatabase's
// rule at this process's door: the database is named after the Firebase project
// it belongs to, so a pair that disagrees is a hop reading one installation's
// device nodes while verifying another's identities -- which is what a copied
// deployment's environment looks like. The refusal names the two variables and
// never their values.
func TestVoiceServeRefusesADatabaseOfAnotherProject(t *testing.T) {
	refuseVoiceWiring(t)
	env := map[string]string{}
	for n, v := range voiceServeEnv {
		env[n] = v
	}
	env[envRTDBURL] = "https://another-installation-default-rtdb.firebaseio.com"

	_, err := voiceServiceConfig(context.Background(), envFrom(env), io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error (exit 1)", err)
	}
	if !strings.Contains(err.Error(), envRTDBURL) || !strings.Contains(err.Error(), envFirebaseFP) {
		t.Fatalf("err = %v, want a refusal naming both variables", err)
	}
	for _, v := range env {
		if v != "" && strings.Contains(err.Error(), v) {
			t.Fatalf("the refusal quotes a value: %v", err)
		}
	}
}

// TestVoiceServeRefusesAValueThatIsNotADatabaseURL: the shape check comes
// first, so a value that is not a Realtime Database at all is refused as that
// rather than as a mismatched installation (credentialsServiceConfig's own
// order).
func TestVoiceServeRefusesAValueThatIsNotADatabaseURL(t *testing.T) {
	refuseVoiceWiring(t)
	env := map[string]string{}
	for n, v := range voiceServeEnv {
		env[n] = v
	}
	env[envRTDBURL] = "https://proj-1234-default-rtdb.firebaseio.com/path"

	_, err := voiceServiceConfig(context.Background(), envFrom(env), io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error (exit 1)", err)
	}
	if !strings.Contains(err.Error(), envRTDBURL) {
		t.Fatalf("err = %v, want a refusal naming %s", err, envRTDBURL)
	}
}

// TestVoiceServeRefusesAnUnusableCredential: the hop reads the database as its
// own service account, and a run that cannot get a token for it must not serve
// -- a process that answered every call with a refusal would look healthy while
// every project read it made failed. The failure is a REMOTE error and not the
// caller's: nothing about it is the phone's fault. The token source's own error
// is carried (credentialsServiceConfig's rule for the same call, and it is the
// one thing an operator diagnosing a missing metadata server needs); what it
// could quote is Application Default Credentials' own text, which holds no
// value this deployment passed.
func TestVoiceServeRefusesAnUnusableCredential(t *testing.T) {
	voiceTest = &voiceWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return nil, errors.New("no metadata server here")
		},
	}
	t.Cleanup(func() { voiceTest = nil })

	_, err := voiceServiceConfig(context.Background(), envFrom(voiceServeEnv), io.Discard)
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want a remote error (exit 2)", err)
	}
	for _, v := range voiceServeEnv {
		if strings.Contains(err.Error(), v) {
			t.Fatalf("the refusal quotes a value of this deployment's environment: %v", err)
		}
	}
}

// stubKeys is a key source whose answer a test decides, for the one line where
// the hop's verifier meets the credentials service's cache.
type stubKeys struct {
	key *rsa.PublicKey
	err error
}

func (s stubKeys) Key(context.Context, string) (*rsa.PublicKey, error) { return s.key, s.err }

// TestVoiceKeySourceBridgesTheTwoPackagesSentinels is the difference the hop's
// two answers depend on, at the one place the two packages meet: a key id
// Google does not publish is a refusal OF THE TOKEN (the app drops the token it
// holds and asks for a new one), while a key set that could not be read is this
// service's own STATE (the app serves the speech locally and carries on). The
// credentials service's sentinel is the first of those and the hop switches on
// its own, so a bridge that dropped the distinction would answer a rotation as
// an outage for as long as the key cache's life ran.
func TestVoiceKeySourceBridgesTheTwoPackagesSentinels(t *testing.T) {
	key := &rsa.PublicKey{N: nil, E: 65537}

	if got, err := (voiceKeySource{keys: stubKeys{key: key}}).Key(context.Background(), "kid"); err != nil || got != key {
		t.Fatalf("a key that IS published came back as (%v, %v)", got, err)
	}

	_, err := (voiceKeySource{keys: stubKeys{err: fmt.Errorf("%w: nobody publishes it", credsvc.ErrUnknownKeyID)}}).Key(context.Background(), "kid")
	if !errors.Is(err, voicesvc.ErrUnknownKeyID) {
		t.Fatalf("an unknown key id is %v, want it to wrap %v: the hop would answer a rotation as an outage", err, voicesvc.ErrUnknownKeyID)
	}

	// Every other failure stays what it was, which is the hop's state
	// direction: an unreadable key set is not a bad token.
	_, err = (voiceKeySource{keys: stubKeys{err: credsvc.ErrUnavailable}}).Key(context.Background(), "kid")
	if errors.Is(err, voicesvc.ErrUnknownKeyID) {
		t.Fatalf("an unreadable key set was translated into a refusal of the token: %v", err)
	}
	if !errors.Is(err, credsvc.ErrUnavailable) {
		t.Fatalf("an unreadable key set reads as %v, want it unchanged", err)
	}
}

// TestVoiceServeHasNoFlagsAndNoArguments: the hop's whole configuration is the
// platform's, so there is nothing an operator could pass that would change what
// it admits or what it reads -- not even a port (the other two processes' own
// rule).
func TestVoiceServeHasNoFlagsAndNoArguments(t *testing.T) {
	serve := findSub(t, findSub(t, NewRootCmd(), "voice"), "serve")
	if serve.Use != "serve" {
		t.Fatalf("voice serve is spelled %q", serve.Use)
	}
	// LocalNonPersistentFlags and PersistentFlags together are every flag
	// DECLARED on this command, and neither of them carries what a parent
	// declares: the root's own --verbose is inherited by every command of the
	// tree, and a bare run that refuses a name in its environment is what this
	// file pins, not the help flags cobra adds.
	for _, set := range []struct {
		name  string
		flags *pflag.FlagSet
	}{{"its own", serve.LocalNonPersistentFlags()}, {"its persistent", serve.PersistentFlags()}} {
		set.flags.VisitAll(func(f *pflag.Flag) {
			t.Errorf("voice serve carries a flag from %s flag set: --%s", set.name, f.Name)
		})
	}
	if serve.Args == nil {
		t.Error("voice serve takes any argument: cobra.NoArgs is what refuses one")
	}
}
