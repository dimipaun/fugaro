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
		return userErr("unknown or malformed flag; secrets commands take --repo, --json, --config, --project and --region, and never the value")
	})
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
or a secret some workflow declares under secrets: in the repository's
fugaro.yaml (run it from a checkout of the repository for those).

The value is read only from stdin: pipe or redirect it in
(fugaro secrets set bitbucket-token < token-file), or, when stdin is a
terminal, paste it at a hidden prompt. It is never taken from an argument,
a flag or the environment, and never printed. One trailing newline (\n or
\r\n) is dropped, and the command says so. Values must be 4 bytes to
64 KiB, on one line except for github-app-key (a PEM, which must be
redirected from a file), and a one-line value may not start or end with
spaces or tabs (a copy-and-paste slip, so it is refused rather than
stored). Ctrl-C at the prompt stores nothing and restores the terminal.`,
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
	label      string                // the fugaro_repo label, job-spec's repo-label
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
	if r.label, err = repoLabel(r.slug); err != nil {
		return nil, userErr("%v", err)
	}
	return r, nil
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
	r, err := resolveSecretRepo(cmd, env, o.repo)
	if err != nil {
		return err
	}
	if _, reserved := config.ReservedSecrets[name]; !reserved {
		// A workflow secret must be one the repository declares: a typo
		// would store an orphan nothing mounts, and a value typed as NAME
		// would end up in the ID. Neither message quotes NAME.
		cfg := r.checkout()
		if cfg == nil {
			return userErr("a workflow secret's NAME is checked against the repository's fugaro.yaml; run this from a checkout of %s", r.repo)
		}
		if !declares(cfg, name) {
			return userErr("no workflow in this checkout's fugaro.yaml declares that secret NAME; add it under a workflow's secrets: first")
		}
	}
	id := gcp.SecretID(r.slug, name)
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}

	value, err := readSecret(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, name == multilineSecret)
	if err != nil {
		return err
	}
	defer clear(value)
	labels := map[string]string{"fugaro": "managed", "fugaro_repo": r.label, "fugaro_secret": name}
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

// errCancelled is a read abandoned by Ctrl-C (or SIGTERM). It is exit 1,
// within the CLI's 0/1/2 contract (design §9.1), not the shell's 130.
var errCancelled = &ExitError{Code: ExitUserError, Err: errors.New("cancelled; nothing was stored")}

// readHidden prompts for name's value on prompt and reads one line from the
// terminal f without echo. ReadPassword blocks in a read that a signal
// doesn't interrupt, and the first Ctrl-C only cancels ctx (see main's
// signalContext); the second kills the process before ReadPassword can turn
// echo back on. So the read runs in a goroutine, and on ctx.Done() the
// terminal state saved here is restored before returning. The goroutine
// stays blocked until the process exits.
func readHidden(ctx context.Context, f *os.File, prompt io.Writer, name string) ([]byte, error) {
	fd := int(f.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, userErr("reading the terminal's state: %v", err)
	}
	fmt.Fprintf(prompt, "Paste the value for %s (input hidden), then press Enter: ", name)
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
			r, err := resolveSecretRepo(cmd, env, o.repo)
			if err != nil {
				return err
			}
			sm, err := gcp.NewSecrets(ctx, env.gcp)
			if err != nil {
				return remote(err)
			}
			list, err := sm.List(ctx, map[string]string{"fugaro_repo": r.label})
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
			return printSecrets(cmd.OutOrStdout(), r.repo, entries, o.asJSON)
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
