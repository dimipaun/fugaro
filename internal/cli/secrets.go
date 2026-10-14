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
	cmd.Flags().StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	cmd.Flags().BoolVar(&o.projectWide, "project-wide", false, "store or list the secret once per Fugaro project, not per repository")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
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

With --project-wide, the value is stored once for the whole Fugaro project
instead of once per repository, and no --repo is needed: NAME must be a
reserved secret your local config's project_secrets block allow-lists, a
model provider's own key (any provider, allow_data_to or not), or a named
Claude account's token whose account allow-lists at least one repository.
Every repository the name is allow-listed for mounts the project copy, and
a repository that also has its own copy of the same name keeps using that
one.`,
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

// resolveSecretRepo resolves --repo (or the checkout's origin) to its slug
// and label.
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
	return name == "claude-oauth-token" || strings.HasPrefix(name, config.ClaudeOAuthTokenPrefix)
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
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	if o.projectWide {
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
			return userErr("model provider %s does not list %s in allow_data_to, so its key is not stored for it", p, r.repo)
		}
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
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: r.label, gcp.LabelSecret: name}
	return storeSecret(cmd, env, o, gcp.SecretID(r.slug, name), labels, name)
}

// projectSecretRefusal is the one message every --project-wide eligibility
// refusal uses. path is the config field the owner would have to fill in
// (project_secrets.<name>.allow_data_to, or a named account's own), stated
// with Printable since a name reaching here is config-sourced.
func projectSecretRefusal(name, path string) error {
	return userErr("%s is not stored project-wide: %s must list at least one repository in allow_data_to",
		pluginwire.Printable(name), path)
}

// secretsSetProjectWide stores name's value once for the whole Fugaro
// project (design docs/design/project-secrets.md §4). The eligibility checks
// run in the design's fixed order (§4, S1) and before any value is read:
//
//  1. a reserved name (config.ReservedSecrets, which takes claude-oauth-token
//     first, so it never falls through to the named-account branch) needs a
//     project_secrets entry with a non-empty allow_data_to;
//  2. a model provider's own key is accepted unconditionally -- no condition
//     on allow_data_to at all (§3a: an absent list is a valid, intentional
//     "available to every repository" state for a provider, not a reason to
//     refuse storage, unlike points 1 and 3);
//  3. a named Claude account's derived secret needs its own non-empty
//     allow_data_to (the bare claude-oauth-token is point 1's, via
//     ClaudeAccountBySecret's implicit default);
//  4. anything else -- a workflow's own secret -- has no project-wide
//     meaning and is refused (§3).
func secretsSetProjectWide(cmd *cobra.Command, env *cloudEnv, o secretsOptions, name string) error {
	if _, reserved := config.ReservedSecrets[name]; reserved {
		if ps, ok := env.lc.ProjectSecrets[name]; !ok || len(ps.AllowDataTo) == 0 {
			return projectSecretRefusal(name, "project_secrets."+pluginwire.Printable(name))
		}
	} else if _, ok := config.ProviderBySecret(env.lc.Providers, name); ok {
		// Accepted unconditionally (§3a); nothing to check.
	} else if account, ok := config.ClaudeAccountBySecret(env.lc.ClaudeAccounts, name); ok && account != config.DefaultClaudeAccount {
		if len(env.lc.ClaudeAccounts[account].AllowDataTo) == 0 {
			return projectSecretRefusal(name, "claude_accounts."+pluginwire.Printable(account)+".allow_data_to")
		}
	} else {
		return userErr("%s has no project-wide meaning: only a reserved secret (%s), a model provider's key, or a named Claude account's token is stored project-wide",
			pluginwire.Printable(name), strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", "))
	}
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: env.lc.Name, gcp.LabelSecret: name}
	return storeSecret(cmd, env, o, gcp.ProjectSecretID(env.lc.Name, name), labels, name)
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
	gcp.SecretInfo
}

// repoMayUseProject reports whether repo may mount a project-wide copy of
// logical: the same answer the plan uses, from infra.ProjectSecretCandidate,
// so this listing never claims a scope the plan would not resolve.
func repoMayUseProject(lc *localcfg.Config, logical, repo string) bool {
	_, ok := infra.ProjectSecretCandidate(lc, logical, repo)
	return ok
}

// projectSecretAllowed are the repositories the local config allows to use
// logical project-wide (--project-wide's ALLOWED column), from the same
// three sources ProjectSecretCandidate reads but with the list as given: a
// provider with no allow_data_to is available to every repository, which an
// empty list here shows as none.
func projectSecretAllowed(lc *localcfg.Config, logical string) []string {
	if ps, ok := lc.ProjectSecrets[logical]; ok {
		return slices.Clone(ps.AllowDataTo)
	}
	if name, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, logical); ok {
		return slices.Clone(lc.ClaudeAccounts[name].AllowDataTo)
	}
	if name, ok := config.ProviderBySecret(lc.Providers, logical); ok {
		return slices.Clone(lc.Providers[name].AllowDataTo)
	}
	return nil
}

// newSecretsLsCmd is `fugaro secrets ls`. It reads metadata only: it has no
// access call, so it can't print a value.
func newSecretsLsCmd() *cobra.Command {
	var o secretsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List a repository's (or, with --project-wide, the project's) secrets: names, scope and versions, never values",
		Long: `List secrets: their logical names, IDs, version counts and where each
value comes from, never any value (this command has no access call at all).

A repository's listing (the default, with --repo or the checkout's origin)
shows, per name, SCOPE: repo (the repository's own container), project (an
allow-listed project-wide container with no repo copy), or "project, repo
override stored" (both exist; the repository's own version wins).

--project-wide lists the installation's project-wide containers instead,
with the repositories the local config allows to use each one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := openCloud(ctx, o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			if o.projectWide {
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
		fmt.Fprintln(tw, "NAME\tSECRET\tSCOPE\tLATEST\tVERSIONS\tCREATED\tLABELS")
	}
	for _, e := range entries {
		last := strings.Join(e.Allowed, ",")
		if !projectWide {
			var labels []string
			for _, k := range slices.Sorted(maps.Keys(e.Labels)) {
				labels = append(labels, k+"="+e.Labels[k])
			}
			last = strings.Join(labels, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", dash(e.Name), e.ID, e.Scope, dash(e.Latest), e.Versions,
			e.Created.UTC().Format("2006-01-02 15:04"), dash(last))
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
