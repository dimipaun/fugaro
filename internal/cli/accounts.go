package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// newAccountsCmd is `fugaro accounts`: read-only lookups over the local
// config's claude_accounts, parallel to secrets' and recipes' group commands.
func newAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "List the named Claude accounts a repository may use",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts subcommand; want ls (see fugaro accounts --help)")
		},
	}
	cmd.AddCommand(newAccountsLsCmd())
	return cmd
}

type accountsOptions struct {
	cloud  cloudOptions
	repo   string
	asJSON bool
}

func (o *accountsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// accountsListDoc is fugaro accounts ls --json's shape: the repository and
// the names it may choose among (config.AllowedClaudeAccounts's own order,
// "default" first), names only — never a secret ID or a token value.
type accountsListDoc struct {
	Repo     string   `json:"repo"`
	Accounts []string `json:"accounts"`
}

func newAccountsLsCmd() *cobra.Command {
	var o accountsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the named Claude accounts (claude_accounts in the local config) a repository may use with fugaro run --token",
		Long: `List the names fugaro run --token NAME (and a repository's own agent.oauth_token)
accept for a repository: "default" (the repository's own unnamed
claude-oauth-token secret, always allowed) first, then every claude_accounts
name in the local config whose allow_data_to lists the repository, sorted
(config.AllowedClaudeAccounts, the one place that decides the question).

This is a local, read-only lookup, offline whenever a project config is
already selected on this machine: it reads the local config file the CLI
would otherwise select for a cloud command, and never calls Secret Manager,
never reads or prints a token value, only names.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsLs(cmd, o)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// accountsConfig is the local config selected for fugaro accounts ls, nil
// when none is (reported as a note, not an error, the same leniency
// pricesConfig gives fugaro budget prices): the command's whole point still
// answers with "default" alone, offline, rather than failing outright. It
// never falls back to the shared config published to the runs bucket
// (selectProject's sharedOK path): that would be a network call for a
// command billed as offline, and claude_accounts is never carried by the
// shared config anyway (localcfg.Config.Shared strips it).
func accountsConfig(cmd *cobra.Command, o cloudOptions) (*localcfg.Config, error) {
	ctx := cmd.Context()
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		explicit := o.config != "" || o.project != "" || os.Getenv("FUGARO_PROJECT") != "" || os.Getenv("FUGARO_CONFIG") != ""
		if explicit {
			return nil, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "note: no project config selected, so only the always-available default account (%v)\n", err)
		return nil, nil
	}
	return lc, announce(o, sel, lc)
}

func accountsLs(cmd *cobra.Command, o accountsOptions) error {
	ctx := cmd.Context()
	repo := o.repo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return userErr("--repo: %v", err)
	}
	lc, err := accountsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	var claudeAccounts map[string]config.ClaudeAccount
	if lc != nil {
		claudeAccounts = lc.ClaudeAccounts
	}
	names := config.AllowedClaudeAccounts(claudeAccounts, repo)
	return printAccounts(cmd.OutOrStdout(), repo, names, o.asJSON)
}

func printAccounts(w io.Writer, repo string, names []string, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(accountsListDoc{Repo: repo, Accounts: names})
	}
	for _, n := range names {
		if _, err := fmt.Fprintln(w, pluginwire.Printable(n)); err != nil {
			return err
		}
	}
	return nil
}
