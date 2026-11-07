// Package diagnoseagent holds the parts of the local diagnosis the plan
// docs/diagnose-agent builds under `fugaro troubleshoot`. This file is the
// credential resolver; the guide and prompt, the evidence collector and the
// read-only agent are later tasks of the same plan.
//
// The resolver reads no configuration file and opens no cloud connection by
// itself: what the installation has (Accounts) and how the project's secret is
// read (SecretReader) are arguments, so what it decides can be tested without
// either. The one property it must never break is visible in the type it
// returns: a credential lives in the Handle and nowhere else (F7).
package diagnoseagent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// ownCredentialEnv are the variables the person's own Claude login writes, in
// the order a local diagnosis prefers them (D1601, F7): a subscription OAuth
// token first, then an API key — the spec's own order, and the order of the two
// variables internal/cli's claudeLoginCredentialEnv refuses `accounts add
// --claude` over, for the same reason: either of them decides which account a
// local Claude login lands on.
var ownCredentialEnv = []string{
	config.ReservedSecrets[config.ClaudeOAuthToken],
	config.ReservedSecrets[config.AnthropicSecret],
}

// Handle is the one credential a local diagnosis may use: an account's token,
// held in this process's memory, and the environment of the one child that may
// see it. The value is a field of this type and nowhere else — the handle
// writes no file, keeps no cache and has no accessor that stores anything,
// which is what "held in memory only and never written to disk" means (F7, the
// plan's global constraints).
//
// The zero Handle is no credential at all and must never be used: Resolve
// returns one only beside an error.
type Handle struct {
	// env is the whole credential environment of the one child: exactly one
	// variable, under the name this credential belongs under. Nothing else of
	// this process's environment is in it, so a second credential the
	// environment happens to hold is unreachable from the diagnosis.
	env []string
	// secret is the value alone. It exists for the redactor: T-DG7
	// registers it before the child runs, so anything the child or the model
	// prints that carries the credential is replaced.
	secret string
	// account is the account the credential belongs to, and "" for the
	// person's own, which belongs to no account of any installation's
	// configuration.
	account string
	// own distinguishes the person's own credential from the project's
	// account: the two are sent to a provider under different terms (F6).
	own bool
}

// handleFields is Handle without its methods, so the methods below can print a
// copy without calling themselves.
type handleFields Handle

// redactedValue is what a credential prints as.
const redactedValue = "<redacted>"

// redacted is a copy of h whose credential loses its value: the environment
// keeps the variable's name and the account keeps its own, and the value itself
// becomes redactedValue. h is not changed.
//
// A Handle carries the one credential a diagnosis has (F7), and prints are
// where such a value leaks: a debug line, an error that formats what it was
// given, a transcript. internal/backend keeps the same rule for the budget
// seal key (LaunchSpec.Format), and for the same reason — the type that holds a
// secret is the place to make it unprintable, because the caller that formats
// it is not thinking about the secret.
func (h Handle) redacted() handleFields {
	c := handleFields(h)
	if h.secret != "" {
		c.secret = redactedValue
	}
	c.env = make([]string, len(h.env))
	for i, kv := range h.env {
		if name, _, ok := strings.Cut(kv, "="); ok && name != "" {
			c.env[i] = name + "=" + redactedValue
		} else {
			c.env[i] = redactedValue
		}
	}
	return c
}

// Format prints the handle under any verb with the credential redacted, so no
// line that formats one can carry the account's token.
func (h Handle) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, fmt.FormatString(f, verb), h.redacted())
}

// String is the handle as %v prints it, the credential redacted.
func (h Handle) String() string { return fmt.Sprintf("%+v", h.redacted()) }

// GoString is the handle as %#v prints it, the credential redacted.
func (h Handle) GoString() string { return fmt.Sprintf("%#v", h.redacted()) }

// Env is the environment of the one child the credential is handed to: the
// credential under its own variable name, in os.Environ's form, and nothing
// else. A caller that starts a child with it holds this credential and no
// other; the rest of the child's environment (a PATH to find claude on, and
// nothing that carries a credential) is the caller's to compose, which is what
// keeps the child's own environment an allow-list (T-DG7).
//
// A copy is returned: the caller cannot edit the handle's value through it.
func (h Handle) Env() []string { return slices.Clone(h.env) }

// Secret is the credential's value alone, and it has exactly one caller: the
// redactor, which must know the value to replace it (F20). Nothing else may
// print it, store it or pass it on — a caller that shows this string has
// leaked the account's token.
func (h Handle) Secret() string { return h.secret }

// Account is the credential's account: the name a run would use
// (config.DefaultClaudeAccount or a claude_accounts name) when the credential
// is the project's, and "" when it is the person's own. It is what the line the
// command prints before it sends anything names (D1606) and what a refusal
// names — the account, never a value or a prefix of one.
func (h Handle) Account() string { return h.account }

// Own reports whether the credential is the person's own — the login this
// machine already holds, which is why a diagnosis still works when the cloud is
// what is broken — rather than the project's account read out of Secret
// Manager. The two are sent to a provider under different terms (F6): the
// person's own credential is theirs, while the project's account is used only
// because its allow_data_to covers this repository (D1601).
func (h Handle) Own() bool { return h.own }

// Accounts is what the selected configuration says about the Claude accounts a
// local diagnosis may use: this checkout's repository, the claude_accounts
// block, and the keys that choose an account. The command fills it from the
// selected local project config (internal/localcfg); this package reads no
// configuration of its own, so the rule below is a rule over values a test can
// hand it.
type Accounts struct {
	// Repo is this checkout's repository, owner/name. It is empty outside a
	// checkout, and the project's account is then refused rather than read:
	// allow_data_to is a per-repository list, and a command that has no
	// repository has nothing to check the project's token against (F22).
	Repo string
	// Block is claude_accounts: each name's own allow_data_to decides which
	// repositories may use its token (config.AllowedClaudeAccounts).
	Block map[string]config.ClaudeAccount
	// Default is default_claude_account: the account every stage of a run uses
	// when its own choice names none.
	Default string
	// Stages is stages.<stage>.claude_account: one stage's own choice.
	Stages map[string]string
	// Stage is the run stage whose own choice applies to this diagnosis. The
	// ordinary local diagnosis stands for no stage of a run, so it is empty and
	// the default applies; a caller that diagnoses one stage names it here, and
	// that stage's own key then wins, exactly as it does for a run.
	Stage string
}

// choice is the account a local diagnosis would use and the key that chooses
// it: "stages.<stage>.claude_account", "default_claude_account", or "" when the
// configuration chooses nothing, in which case the reserved default account is
// the one a run would use.
//
// The key is what lets a refusal say which account it would have used and where
// that account came from (F22), which is the difference between a person
// knowing which setting to change and a bare "not here".
func (a Accounts) choice() (name, key string) {
	if a.Stage != "" {
		if n := a.Stages[a.Stage]; n != "" {
			return n, "stages." + a.Stage + ".claude_account"
		}
	}
	if a.Default != "" {
		return a.Default, "default_claude_account"
	}
	return config.DefaultClaudeAccount, ""
}

// SecretReader reads one secret's value as the person whose machine this is:
// the command supplies one over Secret Manager, authenticated by that person's
// own gcloud login (D1601), and it is asked for a secret name of config's
// (claude-oauth-token, claude-oauth-token-<name>), never for a name of its own
// choosing.
//
// An empty value and no error is "nothing is stored under that name", which is
// a different answer from a failure and is refused differently below. A nil
// reader is a command that cannot read the project's account at all — no
// project config selected, no gcloud login — and the fallback then refuses
// instead of being skipped in silence.
//
// What the reader must not do is keep the value: the resolution holds it in
// memory for the one child that gets it (F7), and a reader that spilled it into
// a file, a cache or a gcloud config would be the leak that rule exists to
// prevent, outside anything this package can check.
type SecretReader func(name string) (string, error)

// Resolve returns the credential a local diagnosis may use, in D1601's order.
// The person's own credential comes first, read out of env and costing no cloud
// call and no project account — which is what makes a diagnosis work when the
// cloud is exactly what is broken. The project's account is the fallback: read
// through the person's own gcloud login, only when its allow_data_to covers
// this repository, and only into memory.
//
// env is the process environment in os.Environ's form. It is read and never
// written: nothing here sets a variable, and a credential that came from the
// environment is not removed from it (the caller composes the child's
// environment from the handle, which carries this credential and no other).
func Resolve(env []string, accounts Accounts, read SecretReader) (Handle, error) {
	for _, name := range ownCredentialEnv {
		if v := envValue(env, name); v != "" {
			return newHandle(name, "in "+name, v, "", true)
		}
	}

	name, key := accounts.choice()
	secret := config.ClaudeAccountSecret(name)
	// F22: the gate below is per repository, and a failed command may have run
	// outside one. There is then nothing to check the account's allow_data_to
	// against, and a token with no repository in hand is refused rather than
	// read — the refusal naming the account the command would have used and
	// the key that chose it, so the person can tell what is being held back.
	//
	// This guard is not redundant with the gate below, and that is the whole
	// reason it is written out: config.AllowedClaudeAccounts answers "default"
	// for every repository, the empty one among them, so the gate alone lets the
	// project's unsuffixed token out with nothing to check it against while it
	// refuses the named accounts. The guard is what makes the fallback fail
	// closed as a whole, and the test's third case is the one that catches its
	// removal.
	if accounts.Repo == "" {
		return Handle{}, fmt.Errorf("the project's Claude account cannot be used here: it would be %s, and this command is not running in a repository — an account's allow_data_to is a per-repository list, and with no repository in hand there is nothing to check the project's token against, so it is refused rather than guessed. Run this from a checkout of the repository, or set %s (or %s) in this environment to diagnose on your own credential, which needs no repository",
			accountPhrase(name, key), ownCredentialEnv[0], ownCredentialEnv[1])
	}
	// D1601's gate, and the ordering is the point: it is asked before the
	// secret is read, so a repository whose account does not allow it never has
	// its token fetched at all. The list itself is
	// config.AllowedClaudeAccounts, the one place that decides the question —
	// the same answer the launcher uses to pick the job that mounts the token,
	// so a diagnosis cannot reach an account a run could not.
	//
	// That list admits "default" for every repository, and it is right to: the
	// unsuffixed claude-oauth-token is the repository's own credential, which is
	// what config.DefaultClaudeAccount means, and a run chooses it without an
	// allow_data_to entry existing for it (the guard above is what keeps this
	// account out of a command that has no repository at all). A diagnosis that
	// were stricter here than the launcher would refuse to explain a run whose
	// own account is the one it cannot resolve.
	if !slices.Contains(config.AllowedClaudeAccounts(accounts.Block, accounts.Repo), name) {
		return Handle{}, fmt.Errorf("the Claude account %s may not be used for %s: claude_accounts.%s.allow_data_to %s, and that list decides which repositories may have their code, logs and configuration sent to the account's token. Add %s to it in the project config, choose an account that lists it, or set %s (or %s) in this environment to diagnose on your own credential",
			pluginwire.Printable(name), pluginwire.Printable(accounts.Repo),
			pluginwire.Printable(name), allowPhrase(accounts.Block[name]),
			pluginwire.Printable(accounts.Repo), ownCredentialEnv[0], ownCredentialEnv[1])
	}
	if read == nil {
		return Handle{}, noCredentials(name, secret, fmt.Sprintf("this command has no project config selected, so there is no project's %s to read", secret))
	}
	v, err := read(secret)
	switch {
	case err != nil:
		return Handle{}, fmt.Errorf("the project's Claude account %s could not be read: reading %s with your own gcloud login failed: %v. Set %s (or %s) in this environment to diagnose on your own credential instead, which needs no cloud call",
			pluginwire.Printable(name), secret, err, ownCredentialEnv[0], ownCredentialEnv[1])
	case v == "":
		return Handle{}, noCredentials(name, secret, fmt.Sprintf("nothing is stored under it (%s)", secret))
	}
	return newHandle(config.ReservedSecrets[config.ClaudeOAuthToken], "stored under "+secret, v, name, false)
}

// newHandle is the one place a credential becomes a handle, and the one place
// its shape is checked. A credential of a few bytes is refused rather than
// used: the redactor cannot replace a value that short without replacing
// ordinary words, and a credential this diagnosis cannot redact is one it must
// not print. internal/agent refuses an OAuth token it could not redact the same
// way, for the same reason.
//
// what names where the value came from, for the refusal: a variable of the
// environment or the secret of the project's account.
func newHandle(envName, what, value, account string, own bool) (Handle, error) {
	if len(value) < 4 {
		return Handle{}, fmt.Errorf("the credential %s is shorter than 4 bytes, so it cannot be redacted safely: a value that short would be replaced in everything this diagnosis prints, ordinary words among it. Replace it with the account's real credential",
			pluginwire.Printable(what))
	}
	return Handle{env: []string{envName + "=" + value}, secret: value, account: account, own: own}, nil
}

// noCredentials is F13's refusal: the command asks nothing, so "you have no
// credential" is one sentence naming every way out — the two variables a local
// login writes and the project's own secret — rather than a prompt. reason says
// what was not there or not readable, so the same refusal serves an empty
// environment, a secret nothing was stored under, and a command with no reader
// at all.
func noCredentials(name, secret, reason string) error {
	return fmt.Errorf("no Claude credential is available for this diagnosis: neither %s nor %s is set in this environment, and the project's Claude account %s has none to fall back on — %s. Set one of the two variables (a local Claude login writes %s), or store the project's token with `fugaro secrets set %s`. Nothing is sent, and nothing is asked, without a credential",
		ownCredentialEnv[0], ownCredentialEnv[1], pluginwire.Printable(name), reason, ownCredentialEnv[0], secret)
}

// envValue is name's value in env, the first match winning as os.Getenv reads
// os.Environ. An entry with no "=" is skipped, and an empty value is an unset
// one: what this package asks is whether either variable is set at all.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// accountPhrase is the account a refusal would have used and the key that chose
// it (F22): the name alone when nothing in the configuration chooses one, since
// the reserved default account is then what a run uses, and the key beside the
// name when one does, so the person can see which setting sent the command to
// this account.
func accountPhrase(name, key string) string {
	if key == "" {
		return fmt.Sprintf("%s, the account a run uses when no key chooses one", pluginwire.Printable(name))
	}
	return fmt.Sprintf("%s, the account %s chooses", pluginwire.Printable(name), pluginwire.Printable(key))
}

// allowPhrase is an account's allow_data_to as a refusal names it, so the
// person can see which repositories the account was configured for rather than
// only that this one was not among them.
func allowPhrase(a config.ClaudeAccount) string {
	if len(a.AllowDataTo) == 0 {
		return "is empty, so the account may be used for no repository at all"
	}
	printed := make([]string, 0, len(a.AllowDataTo))
	for _, repo := range a.AllowDataTo {
		printed = append(printed, pluginwire.Printable(repo))
	}
	return "lists " + strings.Join(printed, ", ")
}
