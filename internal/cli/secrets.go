package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
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

// workflowSecretRE is a workflow secret's logical name (config's secret
// name rule).
var workflowSecretRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Store and list a repository's secrets in Secret Manager",
		// Set so cobra doesn't answer an unknown subcommand by quoting it:
		// `fugaro secrets <value>` must not echo the value.
		Args: cobra.ArbitraryArgs,
	}
	cmd.AddCommand(newSecretsSetCmd(), newSecretsLsCmd())
	return cmd
}

type secretsOptions struct {
	cloud  cloudOptions
	repo   string
	asJSON bool
}

func (o *secretsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
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
or a workflow secret's name.

The value is read only from stdin: pipe or redirect it in
(fugaro secrets set bitbucket-token < token-file), or, when stdin is a
terminal, paste it at a hidden prompt. It is never taken from an argument,
a flag or the environment, and never printed. One trailing newline (\n or
\r\n) is dropped, and the command says so. Values must be 4 bytes to
64 KiB, on one line except for github-app-key (a PEM, which must be
redirected from a file).`,
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

// secretRepo resolves --repo (or the checkout's origin) to its slug and its
// fugaro_repo label, the same label value job-spec's repo-label gives.
func secretRepo(cmd *cobra.Command, env *cloudEnv, flag string) (repo, slug, label string, err error) {
	ctx := cmd.Context()
	repo = flag
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return "", "", "", err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", "", "", userErr("--repo: %v", err)
	}
	if slug, err = env.repoSlug(repo, checkoutOf(ctx, repo)); err != nil {
		return "", "", "", err
	}
	if label, err = repoLabel(slug); err != nil {
		return "", "", "", userErr("%v", err)
	}
	return repo, slug, label, nil
}

func secretsSet(cmd *cobra.Command, o secretsOptions, name string) error {
	if _, reserved := config.ReservedSecrets[name]; !reserved && !workflowSecretRE.MatchString(name) {
		// The name is not quoted: a value typed in its place must not be echoed.
		return userErr("the secret NAME must be one of %s, or a workflow secret's name matching %s",
			strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", "), workflowSecretRE)
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	_, slug, label, err := secretRepo(cmd, env, o.repo)
	if err != nil {
		return err
	}
	id := gcp.SecretID(slug, name)
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}

	value, err := readSecret(cmd.InOrStdin(), cmd.ErrOrStderr(), name, name == multilineSecret)
	if err != nil {
		return err
	}
	defer clear(value)
	labels := map[string]string{"fugaro": "managed", "fugaro_repo": label, "fugaro_secret": name}
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
		return enc.Encode(map[string]string{"secret": id, "version": version})
	}
	_, err = fmt.Fprintf(w, "stored %s, version %s\n", id, version)
	return err
}

// readSecret reads the value of secret name. From a terminal it prompts on
// prompt and reads without echo; otherwise it reads in to EOF. It drops one
// trailing "\n" or "\r\n" (and notes that on prompt), and refuses a value
// that is empty, shorter than the redaction floor, over 64 KiB, holds a NUL,
// or, unless multiline, spans lines or has whitespace at either end. Errors
// never quote the value.
func readSecret(in io.Reader, prompt io.Writer, name string, multiline bool) ([]byte, error) {
	var raw []byte
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if multiline {
			return nil, userErr("%s spans several lines, which the hidden prompt can't take; redirect it from a file: fugaro secrets set %s < FILE", name, name)
		}
		fmt.Fprintf(prompt, "Paste the value for %s (input hidden), then press Enter: ", name)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(prompt)
		if err != nil {
			return nil, userErr("reading the value from the terminal: %v", err)
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
	switch {
	case len(raw) == 0:
		return nil, userErr("the value is empty; pipe it in on stdin or paste it at the prompt")
	case len(raw) < minSecretBytes:
		return nil, userErr("the value is shorter than %d bytes, too short to redact from logs", minSecretBytes)
	case len(raw) > maxSecretBytes:
		return nil, userErr("the value is over Secret Manager's 64 KiB limit")
	case bytes.IndexByte(raw, 0) >= 0:
		return nil, userErr("the value holds a NUL byte")
	case !multiline && bytes.ContainsAny(raw, "\r\n"):
		return nil, userErr("the value spans several lines; only %s (a PEM) may", multilineSecret)
	case !multiline && len(bytes.TrimSpace(raw)) != len(raw):
		return nil, userErr("the value starts or ends with whitespace, most likely a copy-and-paste slip")
	}
	return raw, nil
}

// secretEntry is one line of secrets ls: a secret's metadata, never its value.
type secretEntry struct {
	Name string `json:"name,omitempty"` // the logical name, from the fugaro_secret label
	gcp.SecretInfo
}

// newSecretsLsCmd is `fugaro secrets ls`. It reads metadata only: it has no
// access call, so it can't print a value.
func newSecretsLsCmd() *cobra.Command {
	var o secretsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the repository's secrets: names, labels and versions, never values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := openCloud(ctx, o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			repo, _, label, err := secretRepo(cmd, env, o.repo)
			if err != nil {
				return err
			}
			sm, err := gcp.NewSecrets(ctx, env.gcp)
			if err != nil {
				return remote(err)
			}
			list, err := sm.List(ctx, map[string]string{"fugaro_repo": label})
			if err != nil {
				return remote(err)
			}
			entries := make([]secretEntry, 0, len(list))
			for _, s := range list {
				entries = append(entries, secretEntry{Name: s.Labels["fugaro_secret"], SecretInfo: s})
			}
			slices.SortFunc(entries, func(a, b secretEntry) int {
				return strings.Compare(a.Name+"\x00"+a.ID, b.Name+"\x00"+b.ID)
			})
			return printSecrets(cmd.OutOrStdout(), repo, entries, o.asJSON)
		},
	}
	o.addFlags(cmd)
	return cmd
}

func printSecrets(w io.Writer, repo string, entries []secretEntry, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintf(w, "no secrets stored for %s\n", repo)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSECRET\tLATEST\tVERSIONS\tCREATED\tLABELS")
	for _, e := range entries {
		var labels []string
		for _, k := range slices.Sorted(maps.Keys(e.Labels)) {
			labels = append(labels, k+"="+e.Labels[k])
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", dash(e.Name), e.ID, dash(e.Latest), e.Versions,
			e.Created.UTC().Format("2006-01-02 15:04"), strings.Join(labels, ","))
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
