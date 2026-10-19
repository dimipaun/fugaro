package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// maxSecretBytes is Secret Manager's payload limit (64 KiB).
const maxSecretBytes = 64 * 1024

// minSecretBytes is the redaction floor: the runner's redactor ignores
// shorter values, so a shorter secret could leak into logs.
const minSecretBytes = 4

// multilineSecret is the one logical secret whose value spans lines: the
// GitHub App's private key, a PEM.
const multilineSecret = "github-app-key"

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		Short:   "Store and list a repository's secrets in Secret Manager",
		// Set so cobra doesn't answer an unknown subcommand by quoting it:
		// `fugaro secrets <value>` must not echo the value.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown secrets subcommand; want set or ls (see fugaro secrets --help)")
		},
	}
	// pflag quotes an unknown flag, and a value pasted in the wrong place
	// could be one (`-sk-ant-…`); every secrets command gets a fixed message.
	cmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return userErr("unknown or malformed flag; secrets commands take --repo, --project-wide, --json, --config, --project, --gcp-project and --region, and never the value")
	})
	cmd.AddCommand(newSecretsSetCmd(), newSecretsLsCmd())
	return cmd
}

type secretsOptions struct {
	cloud cloudOptions
	repo  string
	// projectWide stores or lists the secret once per Fugaro project rather
	// than per repository (design docs/design/project-secrets.md §4).
	projectWide bool
	asJSON      bool
}

func (o *secretsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	// --repo is the flag that opts OUT of the project default (D2): it names
	// one repository's own container, and with no value means this checkout's
	// origin, the repository a missing scope used to mean.
	cmd.Flags().StringVar(&o.repo, "repo", "", "work on one repository's own copy, given as owner/name (no value: this checkout's origin); without it, a name that has a project-wide meaning is stored and listed project-wide")
	cmd.Flags().BoolVar(&o.projectWide, "project-wide", false, "the default for a name that has a project-wide meaning, accepted as a synonym so existing scripts keep working (cannot be combined with --repo)")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// NormalizeRepoArgs is argv with a bare --repo (no "=value" and no token
// after it) rewritten to --repo=, whose empty value the commands that take it
// read as "the repository this checkout belongs to": resolveSecretRepo for
// secrets set/ls, and namedRepo for budget set/kill/resume.
//
// --repo takes an optional value, which pflag has no way to declare: the flag
// itself is an ordinary string flag (no NoOptDefVal of its own), so cobra
// consumes the next token as its value whenever one is there. That is
// what makes `--repo acme/app` -- the form every script and the skills write --
// work, and it is also why the BARE form cannot be left to pflag: a trailing
// `--repo`, or one followed by another flag such as `--json`, would be read as
// "the flag's value is --json" or as a flag missing its value. Only the argv
// boundary can tell the bare form from the space-separated one, so
// cmd/fugaro/main.go and the tests' execute helpers call this before cobra
// parses anything. A token after `--` is a positional argument, never a flag,
// and is left alone.
//
// It rewrites nothing outside the verbs optionalRepoVerbs lists: init's --repo
// is a bool (the repository-scoped init), so `fugaro init --repo` must keep
// parsing as the flag it is; `budget show` and `budget preview` keep taking
// owner/name, so `budget show --repo` stays the flag error it is today rather
// than quietly meaning something else; and no other command's --repo takes an
// optional value.
func NormalizeRepoArgs(args []string) []string {
	out := slices.Clone(args)
	// Everything before the verb -- the global flags and their values -- is
	// left exactly as it came in.
	verbAt, ok := optionalRepoVerb(args)
	if !ok {
		return out
	}
	for i := verbAt + 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a != "--repo" {
			continue
		}
		if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
			continue // --repo owner/name
		}
		out[i] = "--repo="
	}
	return out
}

// optionalRepoVerbs are the (command, verb) pairs whose --repo takes an
// optional value, and so the only places NormalizeRepoArgs rewrites. The first
// positional argument is the command and the second its verb (verbOf): the
// pair has to be one of these, or there is nothing to rewrite.
//
// The bare form means the same thing in both families -- the repository this
// checkout belongs to -- because both are asked the same question ("which
// repository is this command about?") from a checkout that answers it.
var optionalRepoVerbs = map[string]map[string]bool{
	"secrets": {"set": true, "ls": true},
	"secret":  {"set": true, "ls": true}, // secrets' alias
	"budget":  {"set": true, "kill": true, "resume": true},
}

// secretsBoolFlags are the secrets commands' boolean flags, written as the
// scan below spells them (with the leading "--"): pflag gives every one of them
// a NoOptDefVal of "true", so none takes the token after it as a value, and a
// scan that reads one as taking a value disagrees with cobra about which token
// is the subcommand. The set is the command family's own — the flags
// newSecretsCmd, newSecretsSetCmd and newSecretsLsCmd declare, plus the --help
// cobra adds to each of them at Execute time — and it is exactly the set the
// commands' FlagErrorFunc names (newSecretsCmd).
//
// TestSecretsBoolFlagsMatchTheFlags keeps this list equal to what the commands
// declare, so a bool flag added later cannot quietly reopen the hole the code
// review found here: read "--json" as taking the token after it and the scan
// finds no verb, so a bare --repo behind it is never rewritten.
//
// It is not what makes `fugaro secrets --json set foo --repo` fail today, and
// it does not by itself make that argv work. That argv has a second cause,
// upstream of this scan: the `secrets` command declares no flags of its own
// (only `set` and `ls` do, through secretsOptions.addFlags), so cobra's own
// stripFlags — called on `secrets` while it looks for the subcommand — has no
// --json to consult a NoOptDefVal on, reads "set" as --json's value, finds no
// subcommand, and answers with the flag error. A flag written between `secrets`
// and its verb is unroutable for that reason, whether or not --repo was
// rewritten: `fugaro secrets --json set foo --repo=` fails the same way, and
// so does `fugaro secrets --json ls`. Making the parent declare them is a
// change to the command's surface, not to this scan, and is not made here.
// What this map does is keep the scan's model of pflag true, which is the
// invariant the doc above claims and the one a future flag addition breaks.
var secretsBoolFlags = map[string]bool{"--json": true, "--project-wide": true, "--help": true}

// budgetBoolFlags is the same list for the budget commands whose --repo takes
// an optional value (optionalRepoVerbs), and it exists for the same reason:
// the scan must read `fugaro budget --yes set --repo` the way pflag does, or
// it finds no verb and leaves the bare --repo unrewritten.
//
// It is not the three commands' own flags alone: the walk in
// TestBudgetBoolFlagsMatchTheFlags covers the whole budget tree, because a
// NoOptDefVal flag anywhere under `budget` is a token the scan can meet. The
// entries --json and --help are in secretsBoolFlags too; the scan consults
// both maps, so a name in either is enough.
var budgetBoolFlags = map[string]bool{
	"--global": true, "--defaults": true, "--clear": true, "--yes": true,
	"--all": true, "--json": true,
	// budget history's own, and the --help cobra adds at Execute time.
	"--sweep": true, "--rollover": true, "--force": true, "--help": true,
}

// optionalRepoVerb returns the index of the verb (set, kill, resume, ls) that
// follows a command whose --repo takes an optional value, and whether there is
// one. The argv is scanned into its positional arguments the way cobra's own
// command scan does it (stripFlags), because that is what decides which token
// cobra reads as the command: a flag's *value* is not a command name. Searching
// the slice for the first token that spells "secrets" answers a different
// question — `fugaro --config secrets secrets set npm-token --repo` has one as
// a flag's value — and answering that one leaves the bare --repo of the real
// subcommand unrewritten.
//
// The scan, over args without the program name:
//
//   - a token after `--` is a positional argument and is not read as a flag,
//     and neither is what follows it;
//   - a long flag with no "=value" ("--config", "--repo") consumes the next
//     token as its value, if there is one, and so does a one-letter short flag
//     ("-c"): pflag has no other reading for them;
//   - a long flag in secretsBoolFlags or budgetBoolFlags ("--json",
//     "--project-wide", "--help") consumes nothing: pflag gives it a
//     NoOptDefVal, so the token after it is the next flag or positional and not
//     its value, and cobra's own command scan asks the flag set the same
//     question. TestSecretsBoolFlagsMatchTheFlags and
//     TestBudgetBoolFlagsMatchTheFlags pin each list to the flags its commands
//     declare;
//   - a flag written with "=" ("--config=x", "-c=x") and a bundled short flag
//     ("-qv") take no separate token;
//   - anything left that is not "-" is a positional argument, in order.
//
// The first positional argument is the command, the second is the subcommand:
// the pair has to be in optionalRepoVerbs, or there is nothing here to
// rewrite.
func optionalRepoVerb(args []string) (at int, ok bool) {
	var positionals []int
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return verbOf(args, positionals)
		case strings.HasPrefix(a, "--") && !strings.Contains(a, "="):
			if secretsBoolFlags[a] || budgetBoolFlags[a] {
				break // NoOptDefVal: no value of its own
			}
			i++ // its value, when the next token is one
		case len(a) == 2 && a[0] == '-' && a != "--" && !strings.Contains(a, "="):
			i++ // a one-letter short flag: same, its value
		case a != "-" && strings.HasPrefix(a, "-"):
			// --config=x, -c=x, or a bundled short flag: no value of its own.
		default:
			positionals = append(positionals, i)
		}
	}
	return verbOf(args, positionals)
}

// verbOf reads the subcommand out of the positional arguments the scan found —
// the first is the command, the second the verb — and returns the index of the
// verb (where the rewrite starts) and whether the pair is one whose --repo
// takes an optional value at all.
func verbOf(args []string, positionals []int) (at int, ok bool) {
	if len(positionals) < 2 {
		return 0, false
	}
	cmd, verb := args[positionals[0]], args[positionals[1]]
	if !optionalRepoVerbs[cmd][verb] {
		return 0, false
	}
	return positionals[1], true
}

// checkScope refuses --repo with --project-wide before anything is read or
// written. The two name different containers -- one repository's, and the
// project's own, which no single repository owns -- and the project-wide
// path ignores --repo, so accepting both would work on a container the
// operator did not name.
func (o *secretsOptions) checkScope(cmd *cobra.Command) error {
	if o.projectWide && cmd.Flags().Changed("repo") {
		return userErr("--repo and --project-wide cannot be combined: --project-wide stores and lists the project's own secret, which no repository owns; drop --repo (or drop --project-wide to work on one repository)")
	}
	return nil
}

// newSecretsSetCmd is `fugaro secrets set NAME`. It has no flag or argument
// that takes the value, by design (global constraints).
func newSecretsSetCmd() *cobra.Command {
	var o secretsOptions
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Store a secret's value, read from stdin or a hidden prompt, as a new version",
		Long: `Store the value of the repository's secret NAME as a new version in Secret
Manager, creating the secret if it doesn't exist.

NAME is one of ` + strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", ") + `,
the secret of a model provider in the local config's providers block (for a
repository that provider's allow_data_to lists), or a secret some workflow
declares under secrets: in the repository's fugaro.yaml (run it from a
checkout of the repository for those).

The value is read only from stdin: pipe or redirect it in
(fugaro secrets set bitbucket-token < token-file), or, when stdin is a
terminal, paste it at a hidden prompt. It is never taken from an argument,
a flag or the environment, and never printed. One trailing newline (\n or
\r\n) is dropped, and the command says so. Values must be 4 bytes to
64 KiB, on one line except for github-app-key (a PEM, which must be
redirected from a file), and a one-line value may not start or end with
whitespace (a copy-and-paste slip, so it is refused rather than stored). Ctrl-C at the prompt stores nothing and restores the terminal.

Scope: with neither flag, a NAME that has a project-wide meaning is stored
once for the whole Fugaro project, in the project's own Secret Manager --
the API key, the GitHub App key, a model provider's own key (any provider,
allow_data_to or not), a reserved secret your local config's project_secrets
block allow-lists, or a named Claude account's token whose account
allow-lists at least one repository. --project-wide is accepted as a
synonym of that default, so existing scripts keep working. Inside a shared
job there is no IAM boundary between repositories: the profile is the
boundary, and the value reaches every enrolled repository. A workflow's own
secret has no project-wide meaning and is stored in the repository's own
container as before.

bitbucket-token is the exception, and has no project-wide default: a
Bitbucket credential is a repository access token, issued for one repository
and usable for no other, so it is always stored in this repository's own
container and every repository is asked for its own value (docs/git-providers.md).
Storing it for the project is refused unless project_secrets.bitbucket-token
allow-lists the repositories it is meant for.

With --repo (owner/name, or with no value for this checkout's origin) the
value is stored as the repository's own copy instead. That copy overrides
the project copy from then on: disabling its last enabled version is the
only way back.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("secrets set needs the secret's NAME")
			case len(args) > 1:
				return userErr("pass the value on stdin, never as an argument: arguments land in shell history and process lists")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return secretsSet(cmd, o, args[0])
		},
	}
	o.addFlags(cmd)
	return cmd
}

// secretRepo is the repository a secrets command works on.
type secretRepo struct {
	repo, slug string
	label      string                // the fugaro_repo label, on the job and its secrets
	checkout   func() *config.Config // the checkout's fugaro.yaml, when it is repo's
}

// resolveSecretRepo resolves --repo to its slug and label. flag is empty for
// a bare --repo (NormalizeRepoArgs rewrites it to --repo=) and for a caller
// that passed no flag at all, and both mean this checkout's origin.
func resolveSecretRepo(cmd *cobra.Command, env *cloudEnv, flag string) (*secretRepo, error) {
	ctx := cmd.Context()
	r := &secretRepo{repo: flag}
	var err error
	if r.repo == "" {
		if r.repo, err = originRepo(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := task.CanonicalRepo(r.repo); err != nil {
		return nil, userErr("--repo: %v", err) // CanonicalRepo's error doesn't quote its input
	}
	r.checkout = checkoutOf(ctx, r.repo)
	if r.slug, err = env.repoSlug(r.repo, r.checkout); err != nil {
		return nil, err
	}
	if r.label, err = gcp.RepoLabel(r.slug); err != nil {
		return nil, userErr("%v", err)
	}
	return r, nil
}

// isClaudeOAuthSecret reports whether name is the bare default Claude OAuth
// secret or a named account's derived one (config.ClaudeAccountSecret's
// shape): the set of secrets claude setup-token mints a value for, so
// init's prompts (initsecrets.go) know when to mention it.
func isClaudeOAuthSecret(name string) bool {
	return config.ClaudeOAuthSecret(name)
}

// codexOAuthRefusal is the one message every refusal of a Codex OAuth
// credential name uses (config.CodexOAuthToken, and the whole
// codex-oauth-token-<name> namespace behind it): the name is reserved ahead
// of the harness (owner ruling of 2026-10-18), so there is nothing to store a
// value for and no path — `secrets set` in either scope, or init's prompt —
// that may write one. name goes through Printable, since a name reaching this
// path may not have passed config.SecretNameRE (a reserved name skips that
// check, so codex-oauth-token-<anything> reaches here).
func codexOAuthRefusal(name string) error {
	return userErr("%s is reserved for the Codex harness's OAuth credential (%s, and names starting %s); Fugaro does not drive Codex yet, so nothing mounts one and there is nowhere for this value to go",
		pluginwire.Printable(name), config.CodexOAuthToken, config.CodexOAuthTokenPrefix)
}

// providerConventionRefusal is the one message every refusal of a configured
// provider's conventional key name uses when the name is not that provider's
// own secret (config.ReservedProviderSecret): storing a workflow's or another
// provider's value under it would be captured the day the key is renamed to
// the convention. provider and name are config-sourced names, stated with
// Printable — and so is the conventional name derived from provider, which
// carries the same characters through: a provider entry named with a bidi
// override would otherwise reach the terminal raw inside the sentence that
// names the name it reserves.
func providerConventionRefusal(provider, name string) error {
	return userErr("%s is reserved for model provider %s's key: the convention names a provider's key after its entry (%s), and that name belongs to that provider whatever its .secret is written as today, so no other value may be stored under it. Store the workflow's own secret under a name of its own",
		pluginwire.Printable(name), pluginwire.Printable(provider), pluginwire.Printable(config.ProviderSecretConvention(provider)))
}

// claudeOAuthAccountAllowed reports whether name (shaped
// config.ClaudeOAuthTokenPrefix+<account>) is a named Claude account repo
// is actually configured and allowed to use. config.Validate
// (internal/config/validate.go) already refuses a workflow from ever
// declaring a secret in this reserved namespace, but neither secretsSet
// nor init's secrets stage (initsecrets.go) trusts that upstream refusal
// alone: a name in this namespace that is not a configured, allowed
// account is refused wherever it is checked, whatever path tried to
// declare or reach it (a bypassed or future-weakened Validate, a cached
// config, or any other caller).
func claudeOAuthAccountAllowed(accounts map[string]config.ClaudeAccount, name, repo string) bool {
	n, ok := config.ClaudeAccountBySecret(accounts, name)
	return ok && accounts[n].AllowsData(repo)
}

// claudeOAuthAccountRefusal is the one message every claude-oauth-token-*
// refusal uses: name and repo both go through Printable, since this path
// must never assume a name reaching it already passed
// config.SecretNameRE, or that repo already passed task.CanonicalRepo (a
// cached or hand-edited local config's account name or allow_data_to
// entry, for one).
func claudeOAuthAccountRefusal(name, repo string) error {
	return userErr("%s is not a configured claude_accounts name for %s; see claude_accounts in the local config",
		pluginwire.Printable(name), pluginwire.Printable(repo))
}

// declares reports whether some workflow of cfg lists secret name.
func declares(cfg *config.Config, name string) bool {
	for _, wf := range cfg.Workflows {
		for _, s := range wf.Secrets {
			if s.Name == name {
				return true
			}
		}
	}
	return false
}

func secretsSet(cmd *cobra.Command, o secretsOptions, name string) error {
	if _, reserved := config.ReservedSecrets[name]; !reserved && !config.SecretNameRE.MatchString(name) {
		// The name is not quoted: a value typed in its place must not be echoed.
		return userErr("the secret NAME must be one of %s, or a workflow secret's name matching %s",
			strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", "), config.SecretNameRE)
	}
	if config.CodexOAuthSecret(name) {
		// Refused here, before the scope decision, before the checkout is
		// read and before any cloud call: no spelling of the command
		// (--project-wide, --repo, a bare command in a checkout) may store a
		// value under a reserved Codex name, and the refusal does not depend
		// on which scope it would have gone to. Neither this check nor the
		// scope it precedes assumes config.Validate ran: a workflow can no
		// longer declare such a name, and this is the write path's own,
		// independent refusal (the Claude namespace's claudeOAuthAccountRefusal
		// is the same shape, decided per account and per repository).
		return codexOAuthRefusal(name)
	}
	if err := o.checkScope(cmd); err != nil {
		return err
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	// D2 (plan Task 2.1 step 1): with neither flag the project copy is the
	// default -- but only for a name *this repository* may mount project-wide.
	// A repository's own application secret has no project-wide meaning, so it
	// keeps today's repository scope rather than being swept into a container
	// no job would mount it from, and neither has an entry of project_secrets
	// that does not list this checkout's repository: the default is "project
	// where project exists", never "project always" (finding 13).
	//
	// The repository is the checkout's own, and it is read only when no --repo
	// was given (code review, low): an entry that lists another repository
	// must not turn the bare command from a checkout it excludes into a write
	// over the shared copy -- the copy other repositories mount and this one
	// may not.
	repo := ""
	if !o.projectWide && !cmd.Flags().Changed("repo") {
		// A checkout whose origin cannot be read is a refusal here rather than
		// a "" (checkoutRepoOrEmpty): the scope decision below must be made
		// with the repository in hand, and "" is the answer for having none.
		//
		// The checkout is consulted only when it decides something (code
		// review, medium): --project-wide names the container outright — the
		// project's own, which no repository owns — and --repo names the
		// repository, so neither wants the checkout's answer, and asking for it
		// anyway refused `--project-wide` for a repository the flag never
		// needed from a checkout with no origin (or one whose origin names no
		// owner/name, an ssh mirror or a nested path). A person in that
		// checkout still has both explicit spellings, which is what the
		// refusal below tells them.
		r, err := checkoutRepoOrEmpty(ctx)
		if err != nil {
			return err
		}
		repo = r
	}
	if o.projectWide || (!cmd.Flags().Changed("repo") && projectScopeName(env.lc, name, repo)) {
		return secretsSetProjectWide(cmd, env, o, name)
	}
	r, err := resolveSecretRepo(cmd, env, o.repo)
	if err != nil {
		return err
	}
	_, reserved := config.ReservedSecrets[name]
	if strings.HasPrefix(name, config.ClaudeOAuthTokenPrefix) {
		// The whole claude-oauth-token-<name> namespace is reserved for a
		// named Claude account's derived secret ID, never a provider's or a
		// workflow's own: config.Validate refuses a workflow from ever
		// declaring one, but this check does not assume that ran — it is
		// refused here too, whatever reached it, unless it is a configured
		// account this repository's allow_data_to actually lists.
		if !claudeOAuthAccountAllowed(env.lc.ClaudeAccounts, name, r.repo) {
			return claudeOAuthAccountRefusal(name, r.repo)
		}
	} else if p, ok := config.ProviderBySecret(env.lc.Providers, name); ok && !reserved {
		// The owner's, from the local config, so any checkout will do. The
		// key is only ever mounted for a repository the provider may see.
		if !env.lc.Providers[p].AllowsData(r.repo) {
			// The bare command landed on this repository's own copy, which
			// this provider may not see, so it is refused rather than written
			// -- a repository copy would win over the project's forever (§5),
			// for a repository that mounts neither. The project's copy is a
			// different store and `--project-wide` is its own spelling, so the
			// refusal names it (code review, medium): SECURITY.md calls that
			// flag a synonym of the default, and a person reading the bare
			// command's refusal must not be left without the one command that
			// does store the copy the repositories this provider *does* list
			// mount. projectWideAccepted accepts a provider's key
			// unconditionally (§3a), so the command named here always works.
			return userErr("model provider %s does not list %s in allow_data_to, so its key is not stored for it: store the project's copy instead, which the repositories it does list mount, with '%s secrets set %s --project-wide'",
				p, r.repo, selfCommand(), quoteWord(name))
		}
	} else if p, ok := config.ReservedProviderSecret(env.lc.Providers, name); ok {
		// The conventional name of a configured provider's key, which is not
		// that provider's own .secret (a name that *is* a provider's secret
		// took the branch above): <p>-api-key belongs to p's key wherever p's
		// key is written today (config.ReservedProviderSecret, and the
		// convention the owner ruled on 2026-10-18), so a workflow's value
		// stored under it would be read as p's key the day the key is renamed
		// to the convention. Both names are config-sourced, so both are stated
		// with Printable.
		return providerConventionRefusal(p, name)
	} else if !reserved {
		// A workflow secret must be one the repository declares: a typo
		// would store an orphan nothing mounts, and a value typed as NAME
		// would end up in the ID. Neither message quotes NAME.
		cfg := r.checkout()
		if cfg == nil {
			if _, problems := checkoutParse(ctx, r.repo); len(problems) > 0 {
				return userErr("this checkout's fugaro.yaml has %d problem(s), so it can't confirm a workflow secret's NAME; see fugaro validate", len(problems))
			}
			return userErr("a workflow secret's NAME is checked against the repository's fugaro.yaml; run this from a checkout of %s", r.repo)
		}
		if !declares(cfg, name) {
			return userErr("no workflow in this checkout's fugaro.yaml declares that secret NAME; add it under a workflow's secrets: first")
		}
	}
	if repo != "" && projectScopeName(env.lc, name, "") {
		// The name has a project-wide meaning that does not reach this
		// repository, and the person typed no flag: the copy they are about to
		// store is this repository's own, and saying which entry left them out
		// is what tells them --project-wide is the other intent -- when the
		// flag would in fact be accepted for it (projectWideHint; an entry that
		// is present and empty is the one state where it would not).
		//
		// It is said here, after the eligibility checks just above and not
		// before them (code review, low): the note promises a store, and a name
		// whose provider's or named account's allow_data_to excludes this
		// repository is refused there, so printing it first told the person
		// their value was going somewhere and then failed the command.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: %s is stored project-wide in this project, but not for %s: this value goes to %s's own copy, which overrides the project copy for it. %s\n",
			pluginwire.Printable(name), pluginwire.Printable(repo), pluginwire.Printable(repo), projectWideHint(env.lc, name))
	}
	if repoMayUseProject(env.lc, name, r.repo) {
		// §5's precedence has no override-the-override: a --repo copy of a
		// name this repository may already read project-wide takes over from
		// now on, and only disabling the copy's last enabled version gives
		// the repository back to the project copy. The same footgun the
		// secrets stage's own prompt and the GitHub App fixes warn about, on
		// the command a person types by hand. On stderr, so --json's stdout
		// stays the document alone.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: %s is available project-wide to %s as well; this repository's own copy overrides the project copy from now on, and disabling the copy's last enabled version is the only way back\n",
			pluginwire.Printable(name), pluginwire.Printable(r.repo))
	}
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: r.label, gcp.LabelSecret: name}
	return storeSecret(cmd, env, o, gcp.SecretID(r.slug, name), labels, name)
}

// projectScopeName reports whether a no-flag `secrets set name` stores
// project-wide in this installation, so that neither --repo nor
// --project-wide is needed (D2, plan Task 2.1 steps 1 and 3b):
//
//   - a name the owner already allow-listed in project_secrets -- their
//     explicit opt-in under #343, which the new default now reaches without
//     a flag;
//   - a reserved secret D2 swept in (config.ProjectScopeByDefault): the API
//     key and the GitHub App key -- bitbucket-token is not one of them, and
//     keeps the repository scope it has whenever it is not allow-listed;
//   - a model provider's own key (accepted project-wide with no list, §3a);
//   - a named Claude account's token whose account lists a repository.
//
// repo is the checkout's repository, "" when the command runs outside one. It
// is the argument that matters (code review, low): "project_secrets lists
// someone, so the name has a project-wide meaning" answered for the list
// rather than for the repository in hand, and the bare command then wrote the
// shared copy from a checkout the entry excludes -- over the value the
// repositories it does list mount, from a checkout that may not mount it. With
// a repository in hand the answer is infra.ProjectSecretCandidate's, the one
// function that answers eligibility and the same answer init's secrets stage
// stores by (initsecrets.go projectScope); outside a checkout there is no
// repository whose own copy the person could have meant, and the repo-unaware
// answer stands.
//
// The bare claude-oauth-token is none of these: it is the default account's
// own, and it keeps the refusal and the repository scope it has on main. A
// workflow's own secret is none of these either, and keeps repository scope.
func projectScopeName(lc *localcfg.Config, name, repo string) bool {
	if repo != "" {
		_, ok := infra.ProjectSecretCandidate(lc, name, repo)
		return ok
	}
	if ps, ok := lc.ProjectSecrets[name]; ok && len(ps.AllowDataTo) > 0 {
		return true
	}
	if config.ProjectScopeByDefault(name) {
		return true
	}
	if _, ok := config.ProviderBySecret(lc.Providers, name); ok {
		return true
	}
	if account, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, name); ok && account != config.DefaultClaudeAccount {
		return len(lc.ClaudeAccounts[account].AllowDataTo) > 0
	}
	return false
}

// checkoutRepoOrEmpty is the checkout's repository for the scope decision:
// checkoutRepo's answer when the working directory is inside a checkout, and
// "" when it is not one at all. The two are not the same case, and folding
// them together was the bug (code review, high): with no checkout there is no
// repository whose own copy the person could have meant, so the repo-unaware
// answer is the honest one; standing *in* a checkout whose origin cannot be
// read is a repository in hand that this command failed to name — the
// repo-unaware branch of projectScopeName answers for the list rather than for
// that repository, and the bare command then wrote the shared copy (a D2-swept
// name, or a project_secrets entry listing someone) from a checkout the entry
// may exclude, over the value the repositories it lists mount. That case
// refuses, and names both spellings that say where the value goes without a
// checkout's help: --repo OWNER/NAME, and --project-wide for the project's own
// copy (an explicit scope statement, which needs no repository at all — the
// caller consults the checkout only where it decides something).
//
// resolveSecretRepo reports a missing repository too, and only on the path that
// needs one.
func checkoutRepoOrEmpty(ctx context.Context) (string, error) {
	if !inCheckout(ctx) {
		return "", nil
	}
	repo, err := checkoutRepo(ctx, ".")
	if err != nil {
		return "", userErr("this is a checkout whose origin cannot be read (%v), so which repository the value is stored for is unknown: pass --repo <owner/name>, or store the project's copy instead with --project-wide, both of which say where the value goes without the checkout's help — or add an origin here with 'git remote add origin <url>'", err)
	}
	return repo, nil
}

// inCheckout reports whether the working directory is inside a git checkout at
// all, which is what tells "no repository in hand" from "a repository this
// command cannot name". A git that is missing or refused fails it too, and that
// is the same answer: nothing here can name a repository.
func inCheckout(ctx context.Context) bool {
	return gitCmd(ctx, ".", "rev-parse", "--show-toplevel").Run() == nil
}

// projectSecretRefusal is the one message every --project-wide eligibility
// refusal uses. path is the config field the owner would have to fill in
// (project_secrets.<name>.allow_data_to, or a named account's own), stated
// with Printable since a name reaching here is config-sourced. It names the
// other intent as well: an entry that allows nobody (D2's sweep plus an
// explicit empty list, or an empty account list) is exactly the case where the
// person typing the bare command meant one repository's own copy, and the
// refusal is the only thing they see (code review, low).
func projectSecretRefusal(name, path string) error {
	return userErr("%s is not stored project-wide: %s must list at least one repository in allow_data_to, or store it for one repository instead (fugaro secrets set <name> --repo <repository>)",
		pluginwire.Printable(name), path)
}

// projectWideAccepted reports whether `fugaro secrets set name --project-wide`
// would be accepted for this installation's local config: the eligibility
// checks of design §4 (S1), in its fixed order, and the one place they are
// decided -- secretsSetProjectWide refuses with projectWideRefusal's message
// exactly when this is false, and the note that offers the flag as the other
// intent (projectWideHint) asks this same function before it offers it.
//
//  1. a reserved name (config.ReservedSecrets, which takes claude-oauth-token
//     first, so it never falls through to the named-account branch): a name
//     D2 swept in (config.ProjectScopeByDefault -- the API key and the GitHub
//     App key) is accepted with no project_secrets entry at all, and an entry
//     the owner wrote still decides when there is one -- a list that is
//     present and empty keeps meaning "nobody". The bare claude-oauth-token is
//     not swept in and still needs an entry with a non-empty allow_data_to;
//  2. a model provider's own key is accepted unconditionally -- no condition
//     on allow_data_to at all (§3a: an absent list is a valid, intentional
//     "available to every repository" state for a provider, not a reason to
//     refuse storage, unlike points 1 and 3);
//  3. a named Claude account's derived secret needs its own non-empty
//     allow_data_to (the bare claude-oauth-token is point 1's, via
//     ClaudeAccountBySecret's implicit default);
//  4. anything else -- a workflow's own secret -- has no project-wide
//     meaning and is refused (§3).
func projectWideAccepted(lc *localcfg.Config, name string) bool {
	if _, reserved := config.ReservedSecrets[name]; reserved {
		ps, listed := lc.ProjectSecrets[name]
		// The owner's own entry is the explicit opt-in #343 had, and the answer
		// a D2 default must not overrule; with no entry, D2's sweep decides.
		return (listed && len(ps.AllowDataTo) > 0) || (!listed && config.ProjectScopeByDefault(name))
	}
	if _, ok := config.ProviderBySecret(lc.Providers, name); ok {
		return true // accepted unconditionally (§3a)
	}
	if account, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, name); ok && account != config.DefaultClaudeAccount {
		return len(lc.ClaudeAccounts[account].AllowDataTo) > 0
	}
	return false
}

// projectWideRefusal is what `secrets set name --project-wide` reports when
// projectWideAccepted is false: the config field the owner would have to fill
// in, or, for a name with no project-wide meaning at all, that fact. Read in
// the same order as projectWideAccepted, so the two cannot disagree about
// which case a name is in.
func projectWideRefusal(lc *localcfg.Config, name string) error {
	if _, reserved := config.ReservedSecrets[name]; reserved {
		return projectSecretRefusal(name, "project_secrets."+pluginwire.Printable(name))
	}
	if account, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, name); ok && account != config.DefaultClaudeAccount {
		return projectSecretRefusal(name, "claude_accounts."+pluginwire.Printable(account)+".allow_data_to")
	}
	if p, ok := config.ReservedProviderSecret(lc.Providers, name); ok {
		// Reserved for a configured provider's key and not that provider's own
		// secret, so the generic sentence below would be wrong about the name
		// ("no project-wide meaning"): it has one, and it is not this
		// command's to write. Read in the same order as projectWideAccepted,
		// which refuses it for the same reason.
		return providerConventionRefusal(p, name)
	}
	return userErr("%s has no project-wide meaning: only a reserved secret (%s), a model provider's key, or a named Claude account's token is stored project-wide",
		pluginwire.Printable(name), strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", "))
}

// projectWideHint words the other intent the note in secretsSet offers: the
// flag, when `secrets set name --project-wide` would be accepted for it, or
// the edit that makes it accepted when it would not. Only a name whose
// project_secrets entry is present and empty gets the second form -- the one
// state where the name has a project-wide shape (ProjectScopeByDefault), the
// note therefore fires for a checkout the entry does not list, and the flag
// would still refuse (projectWideAccepted). Offering it there would send the
// person to a command that fails; naming the field is what gets them where
// they meant to go, and the reading is the same "present and empty means
// nobody" projectSecretRefusal states.
func projectWideHint(lc *localcfg.Config, name string) string {
	if projectWideAccepted(lc, name) {
		return "`fugaro secrets set <name> --project-wide` stores it for the project instead"
	}
	return fmt.Sprintf("storing it for the project instead needs %s to list the repositories it is meant for: while it is present and empty nobody may mount the project copy, so `fugaro secrets set <name> --project-wide` is refused until it names at least one repository",
		"project_secrets."+pluginwire.Printable(name)+".allow_data_to")
}

// secretsSetProjectWide stores name's value once for the whole Fugaro
// project (design docs/design/project-secrets.md §4). Eligibility is
// projectWideAccepted's decision, and it runs before any value is read.
func secretsSetProjectWide(cmd *cobra.Command, env *cloudEnv, o secretsOptions, name string) error {
	if !projectWideAccepted(env.lc, name) {
		return projectWideRefusal(env.lc, name)
	}
	// Design §5's precedence has no override-the-override, and §6 step 4a is
	// the state both existing installations are in: a repository copy with an
	// enabled version wins over the project copy, so storing a new version
	// here is a no-op for every repository that has one -- the rotation
	// "succeeds" while those runs keep the old value. Said before the value is
	// read, so the person can stop, and on stderr, so --json's stdout stays
	// the document alone. Names only, never a value: the check is a listing.
	if repos, err := repoCopiesWithVersions(cmd.Context(), env, name); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: whether a repository's own copy of %s wins over the project copy could not be read (%s), and a repository copy with an enabled version always wins\n",
			pluginwire.Printable(name), oneLine(err.Error()))
	} else if len(repos) > 0 {
		// The list is the local config's repositories (repoCopiesWithVersions
		// matches a copy by the ID its own slug derives, and a slug cannot be
		// turned back into an owner/name), so a repository this local config
		// does not list is not named here and wins all the same -- said, because
		// the note is read as "these and no others".
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: %s already has its own copy with an enabled version in %s, and a repository copy wins over the project copy: those repositories keep the value their own copy holds until its last enabled version is disabled (`fugaro secrets ls --repo <repository>` names the secret), and this version reaches them only after that. A repository this local config does not list is not named here, and its own copy wins the same way.\n",
			pluginwire.Printable(name), pluginwire.Printable(strings.Join(repos, ", ")))
	}
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: env.lc.Name, gcp.LabelSecret: name}
	if err := storeSecret(cmd, env, o, gcp.ProjectSecretID(env.lc.Name, name), labels, name); err != nil {
		return err
	}
	// Plan Task 2.1 step 3: the reach is stated where the person storing the
	// value reads it, in one sentence, because D2 is a default that shares a
	// value with repositories the operator did not name on this command line.
	// The reach is projectReach's answer, never a fixed "every enrolled
	// repository" (code review, low): an entry that lists repositories is
	// reachable by those and no others, and the sentence is what a person uses
	// to decide whether storing the shared copy is safe. The profile is named
	// as the boundary, since inside a shared job no IAM boundary separates
	// repositories. On stderr, so --json's stdout stays the document alone.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"note: %s is stored project-wide for %s, where %s. Inside a shared job there is no IAM boundary between repositories: the profile is the boundary. A workflow's own secret (workflows.<n>.secrets) cannot be shared this way and stays a repository copy.\n",
		pluginwire.Printable(name), pluginwire.Printable(env.lc.Name), projectReach(env.lc, name))
	return nil
}

// projectReach words who may mount a project copy of logical, for the note
// above: the repositories an allow_data_to that names them lists, or the whole
// project's enrolled set. "Every enrolled repository can mount it -- every
// repository a published policy admits, or, with no policy published, every
// repository this local config knows" is the default, and it is false of an
// entry that restricts the reach: project_secrets and claude_accounts read as
// "absent or empty means nobody", and a provider's key is any repository only
// when its allow_data_to is absent (design §3a) -- the same readings
// projectSecretAllowed gives the --project-wide listing, so the two surfaces
// cannot disagree.
func projectReach(lc *localcfg.Config, logical string) string {
	repos := projectSecretAllowed(lc, logical)
	switch {
	case slices.Contains(repos, "any repository") || slices.Contains(repos, "*"):
		return "every enrolled repository can mount it -- every repository a published policy admits, or, with no policy published, every repository this local config knows"
	case len(repos) == 0:
		return "no repository can mount it: its allow_data_to lists none, and absent or empty means nobody"
	default:
		return fmt.Sprintf("only the repositories its allow_data_to lists can mount it (%s), whatever a published policy admits",
			pluginwire.Printable(strings.Join(repos, ", ")))
	}
}

// repoCopiesWithVersions are the local config's repositories whose own copy
// of logical holds an enabled version: exactly the copies that win over the
// project copy (design §5, "repo overrides project" has no
// override-the-override), so a project-wide rotation does not reach them
// until each one's last enabled version is disabled (design §6 step 4a).
//
// It is metadata only -- the listing counts enabled versions and reads no
// value -- and it names repositories, never a container: a repository is
// matched by the ID its own slug derives (gcp.SecretID), the one direction
// that works, since a slug is lossy and cannot be turned back into an
// owner/name. A copy belonging to a repository this local config does not
// know is therefore not named; the local config is the operator's own view of
// their project, which is also what "every enrolled repository" means before
// a policy is published.
func repoCopiesWithVersions(ctx context.Context, env *cloudEnv, logical string) ([]string, error) {
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return nil, err
	}
	list, err := sm.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelSecret: logical})
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(list))
	for _, s := range list {
		if s.Latest != "" {
			live[s.ID] = true
		}
	}
	var out []string
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		slug, err := task.Slug(env.lc.Repos[repo].Provider, repo)
		if err != nil {
			continue // a repository name the local config holds but no slug can carry
		}
		if live[gcp.SecretID(slug, logical)] {
			out = append(out, repo)
		}
	}
	return out, nil
}

// storeSecret reads name's value (stdin, or a hidden prompt on a terminal)
// and stores it as a new version of id with labels, printing neither the
// value nor anything a server echoed back.
func storeSecret(cmd *cobra.Command, env *cloudEnv, o secretsOptions, id string, labels map[string]string, name string) error {
	ctx := cmd.Context()
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}
	value, err := readSecret(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, name == multilineSecret)
	if err != nil {
		return err
	}
	defer clear(value)
	version, err := sm.Set(ctx, id, value, labels)
	if err != nil {
		// A server may echo what it received, raw or base64 as sent.
		msg := agent.Redact(err.Error(), []string{string(value), base64.StdEncoding.EncodeToString(value)})
		if errors.Is(err, gcp.ErrForeignSecret) {
			return userErr("%s; refusing to add a version to it", msg)
		}
		return remote(errors.New(msg))
	}

	w := cmd.OutOrStdout()
	if o.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{"project": env.lc.Name, "secret": id, "version": version})
	}
	_, err = fmt.Fprintf(w, "stored %s, version %s\n", id, version)
	return err
}

// readSecret reads the value of secret name. From a terminal it prompts on
// prompt and reads without echo (see readHidden); otherwise it reads in to
// EOF. It drops one trailing "\n" or "\r\n" (and notes that on prompt), and
// refuses a value that is empty, shorter than the redaction floor, over
// 64 KiB, holds a NUL, or, unless multiline, spans lines or has whitespace
// at either end. Errors never quote the value.
func readSecret(ctx context.Context, in io.Reader, prompt io.Writer, name string, multiline bool) ([]byte, error) {
	var raw []byte
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if multiline {
			return nil, userErr("%s spans several lines, which the hidden prompt can't take; redirect it from a file: fugaro secrets set %s < FILE", name, name)
		}
		b, err := readHidden(ctx, f, prompt, name)
		if err != nil {
			return nil, err
		}
		raw = b
	} else {
		// Room for the limit plus a trailing "\r\n", and one byte to spot more.
		b, err := io.ReadAll(io.LimitReader(in, maxSecretBytes+3))
		if err != nil {
			return nil, userErr("reading the value from stdin: %v", err)
		}
		raw = b
		if t, ok := bytes.CutSuffix(raw, []byte("\n")); ok {
			raw, _ = bytes.CutSuffix(t, []byte("\r"))
			fmt.Fprintln(prompt, "note: dropped the value's trailing newline")
		}
	}
	if err := validateSecret(raw, multiline); err != nil {
		return nil, err
	}
	return raw, nil
}

// validateSecret is the rules every stored value meets, whatever way it
// came in: not empty, at least the redaction floor, within Secret Manager's
// limit, no NUL, and, unless multiline, one line with no whitespace at either
// end. Errors never quote the value.
func validateSecret(raw []byte, multiline bool) error {
	switch {
	case len(raw) == 0:
		return userErr("the value is empty; pipe it in on stdin or paste it at the prompt")
	case len(raw) < minSecretBytes:
		return userErr("the value is shorter than %d bytes, too short to redact from logs", minSecretBytes)
	case len(raw) > maxSecretBytes:
		return userErr("the value is over Secret Manager's 64 KiB limit")
	case bytes.IndexByte(raw, 0) >= 0:
		return userErr("the value holds a NUL byte")
	case !multiline && bytes.ContainsAny(raw, "\r\n"):
		return userErr("the value spans several lines; only %s (a PEM) may", multilineSecret)
	case !multiline && len(bytes.TrimSpace(raw)) != len(raw):
		return userErr("the value starts or ends with whitespace, most likely a copy-and-paste slip")
	}
	return nil
}

// errCancelled is a read abandoned by Ctrl-C (or SIGTERM). It is exit 1,
// within the CLI's 0/1/2 contract (design §9.1), not the shell's 130.
var errCancelled = &ExitError{Code: ExitUserError, Err: errors.New("cancelled; nothing was stored")}

// readHidden prompts for name's value on prompt and reads one line from the
// terminal f without echo. ReadPassword blocks in a read that a signal
// doesn't interrupt, and the first Ctrl-C only cancels ctx (see main's
// signalContext); the second kills the process before ReadPassword can turn
// echo back on. So the read runs in a goroutine, and on ctx.Done() the
// terminal state saved here is restored before returning.
//
// After a cancel the goroutine stays blocked in its read, so this assumes
// the process exits soon after, as fugaro does once the command returns: a
// long-lived caller would leak the goroutine, and the abandoned read would
// take (and discard) the next line typed at the terminal.
func readHidden(ctx context.Context, f *os.File, prompt io.Writer, name string) ([]byte, error) {
	return readHiddenLine(ctx, f, prompt, "Paste the value for "+name+" (input hidden), then press Enter: ")
}

// readHiddenLine is readHidden with the prompt's own text.
func readHiddenLine(ctx context.Context, f *os.File, prompt io.Writer, text string) ([]byte, error) {
	fd := int(f.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, userErr("reading the terminal's state: %v", err)
	}
	fmt.Fprint(prompt, text)
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, err := term.ReadPassword(fd)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		fmt.Fprintln(prompt)
		if ctx.Err() != nil {
			clear(r.b)
			return nil, errCancelled
		}
		if r.err != nil {
			return nil, userErr("reading the value from the terminal: %v", r.err)
		}
		return r.b, nil
	case <-ctx.Done():
		_ = term.Restore(fd, state)
		fmt.Fprintln(prompt)
		return nil, errCancelled
	}
}

// The SCOPE column's values: where a listed secret's value comes from.
const (
	scopeRepo            = "repo"
	scopeProject         = "project"
	scopeProjectOverride = "project, repo override stored"
)

// secretEntry is one line of secrets ls: a secret's metadata, never its value.
type secretEntry struct {
	Name string `json:"name,omitempty"` // the logical name, from the fugaro_secret label
	// Scope is where this secret's value comes from: repo, project, or
	// project with a repo override stored that wins (design §5).
	Scope string `json:"scope,omitempty"`
	// Allowed are the repositories the local config lets use this
	// project-wide secret (--project-wide only); never a live call.
	Allowed []string `json:"allowed,omitempty"`
	// Missing marks a required name no enabled version resolves for this
	// repository: a job of one of its workflows needs it
	// (infra.RequiredSecrets) and nothing is there to mount. Such a name has no container to be listed from, so it
	// gets a row of its own — the post-restore question is exactly "which
	// required secret is gone", and a listing of containers alone can never
	// answer it (design D67, Task 5.1).
	Missing bool `json:"missing,omitempty"`
	gcp.SecretInfo
}

// repoMayUseProject reports whether repo may mount a project-wide copy of
// logical: the same answer the plan uses, from infra.ProjectSecretCandidate,
// so this listing never claims a scope the plan would not resolve.
func repoMayUseProject(lc *localcfg.Config, logical, repo string) bool {
	_, ok := infra.ProjectSecretCandidate(lc, logical, repo)
	return ok
}

// markMissingRequired marks every required name no entry resolves to an
// enabled version for, and is the whole point of the MISSING column: a missing
// secret has no container, so it can only be reported by a row of its own. A
// name counts as present when the row this repository resolves carries a
// latest enabled version — its own copy, or an allow-listed project-wide one
// it may mount (§5) — and not merely when a container of that name exists: the
// container init leaves behind before a value is stored, or one whose versions
// are all disabled, resolves to nothing. A name that has a row already is
// marked on that row rather than listed twice, so the table keeps one line per
// name.
func markMissingRequired(entries []secretEntry, required []string) []secretEntry {
	at := map[string]int{}
	for i, e := range entries {
		at[e.Name] = i
	}
	var missing []secretEntry
	for _, name := range required {
		if i, ok := at[name]; ok {
			if entries[i].Latest == "" {
				entries[i].Missing = true
			}
			continue
		}
		missing = append(missing, secretEntry{Name: name, Missing: true})
	}
	return append(entries, missing...)
}

// projectSecretAllowed are the repositories the local config allows to use
// logical project-wide (--project-wide's ALLOWED column), from the same
// three sources ProjectSecretCandidate reads but with the list as given.
// The one list that does not read as itself is a provider's: with no
// allow_data_to the key is available to every repository (design §3a), and
// an empty column would say the opposite of what the runner does, so it
// answers "any repository" in words; the explicit ["*"] marker is left as
// written, since it already says so. project_secrets and claude_accounts
// keep "absent or empty means nobody".
func projectSecretAllowed(lc *localcfg.Config, logical string) []string {
	if ps, ok := lc.ProjectSecrets[logical]; ok {
		return slices.Clone(ps.AllowDataTo)
	}
	if config.ProjectScopeByDefault(logical) {
		// D2's sweep, and the same answer ProjectSecretCandidate gives: with
		// no entry to the contrary, every repository may mount it. An empty
		// column here would say the opposite of what the plan resolves.
		return []string{"any repository"}
	}
	if name, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, logical); ok {
		return slices.Clone(lc.ClaudeAccounts[name].AllowDataTo)
	}
	if name, ok := config.ProviderBySecret(lc.Providers, logical); ok {
		p := lc.Providers[name]
		if p.OpenToEveryRepo() && p.AllowDataTo == nil {
			return []string{"any repository"}
		}
		return slices.Clone(p.AllowDataTo)
	}
	return nil
}

// newSecretsLsCmd is `fugaro secrets ls`. It reads metadata only: it has no
// access call, so it can't print a value.
func newSecretsLsCmd() *cobra.Command {
	var o secretsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the project's (or, with --repo, a repository's) secrets: names, scope and versions, never values",
		Long: `List secrets: their logical names, IDs, version counts and where each
value comes from, never any value (this command has no access call at all).

The default listing is the installation's project-wide containers -- the
scope secrets set stores to by default for a name that has one -- one row
per name, with the repositories the local config allows to use each one
(bitbucket-token is never stored there by default: a repository access
token serves one repository, so it has only repository copies).
--project-wide is accepted as a synonym, so existing scripts keep working.

--repo (or a bare --repo, for this checkout's origin) lists one repository
instead: its own containers, each with SCOPE repo (the repository's own
container), project (a project-wide container this repository may use and
has no copy of), or "project, repo override stored" (both exist; the
repository's own version wins). Nothing about a repository is listed
without --repo: the project's containers are not one repository's.

In a --repo listing, MISSING marks a name a job of that repository needs
and no enabled version resolves for: the answer to "which required secret
is gone" after a restore. "Required" is what the jobs of the repository's workflows mount and
cannot run without, worked out from the checkout's fugaro.yaml the way a
deploy works it out: the git credential of its provider, the model
credentials its agent.auth names (claude-oauth-token and each allowed
claude_accounts token for oauth; each allowed model provider's key for
api-key; none for vertex), and each workflows.<w>.secrets name.
anthropic-api-key is never marked: the job mounts it only when it has a
version and runs without it, and fugaro validate says when a pinned model
needs it. A required name with nothing behind it gets a row of its own — a
missing secret has no container to be listed from, so its SECRET, LATEST and
CREATED read "-" (in --json its id is empty and it has no created). The same
marks a container that exists with no enabled version behind it, which
resolves to nothing as surely as no container at all. Run this from a
checkout of the repository: only its fugaro.yaml says what is required, so
elsewhere, or with a fugaro.yaml that does not parse, nothing is marked and a
note on stderr says so.

The default (project-wide) listing has no MISSING column: a name absent
there may be stored per repository and perfectly present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := o.checkScope(cmd); err != nil {
				return err
			}
			ctx := cmd.Context()
			env, err := openCloud(ctx, o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			// D2: the listing follows the same default as set -- the
			// installation's project-wide containers -- with --repo for one
			// repository's own view. --project-wide stays a synonym of the
			// default, as it is on set.
			if o.projectWide || !cmd.Flags().Changed("repo") {
				return listProjectWide(cmd, env, o)
			}
			r, err := resolveSecretRepo(cmd, env, o.repo)
			if err != nil {
				return err
			}
			return listRepoScope(cmd, env, o, r)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// listRepoScope lists a repository's own secrets, each with the SCOPE that
// says where its value comes from: repo (the repository's own container),
// project (an allow-listed project container with no copy of its own), or
// "project, repo override stored" (both exist and the repo copy wins, §5).
//
// The override is a copy with an enabled version, not a container: a
// repository container whose versions are all disabled resolves to the
// project-wide copy (infra.ResolveSecretID), which is exactly the state
// §6 step 4a leaves behind while a name moves project-wide. Claiming an
// override there would tell the operator the migration did not take, beside
// the "-" LATEST and 0 VERSIONS of the same row.
func listRepoScope(cmd *cobra.Command, env *cloudEnv, o secretsOptions, r *secretRepo) error {
	ctx := cmd.Context()
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}
	list, err := sm.List(ctx, map[string]string{gcp.LabelRepo: r.label})
	if err != nil {
		return remote(err)
	}
	// The installation's own containers, for the SCOPE column and for a name
	// this repository may use project-wide with no repo copy at all.
	project, err := sm.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: env.lc.Name})
	if err != nil {
		return remote(err)
	}
	shared := map[string]gcp.SecretInfo{}
	for _, s := range project {
		if name := s.Labels[gcp.LabelSecret]; name != "" {
			shared[name] = s
		}
	}
	entries := make([]secretEntry, 0, len(list))
	seen := map[string]bool{}
	for _, s := range list {
		name := s.Labels[gcp.LabelSecret]
		seen[name] = true
		project, shared := shared[name]
		shared = shared && repoMayUseProject(env.lc, name, r.repo)
		e := secretEntry{Name: name, Scope: scopeRepo, SecretInfo: s}
		switch {
		case s.Latest == "" && shared:
			// The repository's container holds no version, so the copy
			// Fugaro resolves for this name is the project-wide one: the
			// row is that container's, and SCOPE says project. An override
			// here would sit beside the "-" LATEST and 0 VERSIONS of the
			// repository container and claim a value that is not used.
			e = secretEntry{Name: name, Scope: scopeProject, SecretInfo: project}
		case shared:
			e.Scope = scopeProjectOverride
		}
		entries = append(entries, e)
	}
	for _, name := range slices.Sorted(maps.Keys(shared)) {
		if seen[name] || !repoMayUseProject(env.lc, name, r.repo) {
			continue
		}
		entries = append(entries, secretEntry{Name: name, Scope: scopeProject, SecretInfo: shared[name]})
	}
	// "Required" is what the jobs of this repository's workflows mount
	// (infra.RequiredSecrets, the same SecretMounts a deploy uses), and only
	// the checkout's fugaro.yaml says what that is. Without one nothing is
	// marked, and the note says so rather than letting an unmarked listing
	// read as an all-clear.
	var required []string
	cfg, problems := checkoutParse(ctx, r.repo)
	switch {
	case cfg == nil && len(problems) > 0:
		fmt.Fprintf(cmd.ErrOrStderr(), "note: this checkout's fugaro.yaml has %d problem(s), so the secrets its jobs mount were not checked; see fugaro validate\n", len(problems))
	case cfg == nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "note: not run from a checkout of %s, so the secrets its jobs mount were not checked (MISSING needs its fugaro.yaml)\n", r.repo)
	default:
		if required, err = infra.RequiredSecrets(cfg, env.lc, r.repo); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the secrets this repository's jobs mount could not be worked out, so none were checked: %v\n", err)
		}
	}
	entries = markMissingRequired(entries, required)
	sortSecretEntries(entries)
	return printSecrets(cmd.OutOrStdout(), r.repo, entries, o.asJSON, false)
}

// listProjectWide lists the installation's project-wide containers by name,
// versions and the repositories the local config allows (an operator-only
// view, never a live call), and reads no value.
func listProjectWide(cmd *cobra.Command, env *cloudEnv, o secretsOptions) error {
	ctx := cmd.Context()
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}
	list, err := sm.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: env.lc.Name})
	if err != nil {
		return remote(err)
	}
	entries := make([]secretEntry, 0, len(list))
	for _, s := range list {
		name := s.Labels[gcp.LabelSecret]
		entries = append(entries, secretEntry{Name: name, Scope: scopeProject, Allowed: projectSecretAllowed(env.lc, name), SecretInfo: s})
	}
	sortSecretEntries(entries)
	return printSecrets(cmd.OutOrStdout(), env.lc.Name, entries, o.asJSON, true)
}

func sortSecretEntries(entries []secretEntry) {
	slices.SortFunc(entries, func(a, b secretEntry) int {
		return strings.Compare(a.Name+"\x00"+a.ID, b.Name+"\x00"+b.ID)
	})
}

func printSecrets(w io.Writer, subject string, entries []secretEntry, asJSON, projectWide bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		// A project-wide listing is empty when the installation has stored
		// none; a repository's listing is empty only when nothing is required
		// of it either, since a required name with no value gets a row of its
		// own (Task 5.1).
		if projectWide {
			_, err := fmt.Fprintf(w, "no project-wide secrets stored for %s\n", subject)
			return err
		}
		_, err := fmt.Fprintf(w, "no secrets stored for %s\n", subject)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if projectWide {
		fmt.Fprintln(tw, "NAME\tSECRET\tSCOPE\tLATEST\tVERSIONS\tCREATED\tALLOWED")
	} else {
		fmt.Fprintln(tw, "NAME\tSECRET\tSCOPE\tMISSING\tLATEST\tVERSIONS\tCREATED\tLABELS")
	}
	for _, e := range entries {
		cells := []string{dash(e.Name), dash(e.ID), dash(e.Scope)}
		if !projectWide {
			// "yes" for a required name nothing resolves for, "-" otherwise,
			// so the column reads as a fact about the row rather than as an
			// adjective on it.
			cells = append(cells, dash(missingCell(e)))
		}
		cells = append(cells, dash(e.Latest), strconv.Itoa(e.Versions), dash(createdCell(e)))
		if projectWide {
			cells = append(cells, dash(strings.Join(e.Allowed, ",")))
		} else {
			cells = append(cells, dash(labelsCell(e)))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// missingCell is the MISSING column's value for one row.
func missingCell(e secretEntry) string {
	if e.Missing {
		return "yes"
	}
	return ""
}

// createdCell is the CREATED column's value: a missing required secret has no
// container, so it has no creation time to print.
func createdCell(e secretEntry) string {
	if e.Created.IsZero() {
		return ""
	}
	return e.Created.UTC().Format("2006-01-02 15:04")
}

// labelsCell is the LABELS column's value, in key order.
func labelsCell(e secretEntry) string {
	var labels []string
	for _, k := range slices.Sorted(maps.Keys(e.Labels)) {
		labels = append(labels, k+"="+e.Labels[k])
	}
	return strings.Join(labels, ",")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
