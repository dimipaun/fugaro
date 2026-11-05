package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is C1.25: `fugaro creds set NAME` and `fugaro creds ls`, the
// writer and the reader of the credentials service's ONE bundle secret (Q6,
// ruled 2026-10-19; docs/credentials-service/implementation-plan.md).
//
// The bundle is one YAML document in one Secret Manager container, holding
// every model credential the service issues, and the service's whole reach in
// Secret Manager is one secretAccessor grant on that container (Global
// Constraint 12). Three properties follow, and they are the whole of this
// file:
//
//   - The document is written by the owner and by nothing else. `fugaro
//     secrets set` refuses the container's name outright
//     (credentialsBundleRefusal), no workflow may declare it, and these two
//     commands refuse to run from a coding agent's session (agentMarker) and
//     from a run's own process (managedFlagRunMarker) before anything is read
//     and before the cloud is opened, exactly as `fugaro allow-delete` and
//     every other owner-only command does. That refusal is the accepted cost
//     of the ruling: the bundle is read to be written, so only the owner runs
//     it. This command adds NO operator IAM grant on the container — a
//     secretVersionAdder on it is the ability to read every credential in the
//     bundle at once, which is the boundary Q6 and D145 keep ("operators add,
//     never read"); the owner's own project IAM is the whole of the reach.
//   - No value ever reaches an argument, an environment variable, a log line,
//     an error or the command's output. The value is read from stdin or a
//     hidden prompt only, no flag can carry it, and every byte a server sends
//     back goes through agent.Redact before it is shown.
//   - One schema, one decoder. The document is read, written and re-read
//     through credsvc.ParseBundle and credsvc.CredentialKey, the loader C1.11
//     ships, so a document this command produces is a document the service
//     accepts — and a document the loader refuses is refused here, before
//     anything is written, rather than replaced by one of ours.

func newCredsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "creds",
		Short: "Set and list the credentials service's one bundle secret (the owner)",
		// Set so cobra doesn't answer an unknown subcommand by quoting it:
		// `fugaro creds <value>` must not echo the value.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown creds subcommand; want set or ls (see fugaro creds --help)")
		},
	}
	// pflag quotes an unknown flag, and a value pasted in the wrong place
	// could be one (`-sk-ant-…`); every creds command gets a fixed message.
	cmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return userErr("unknown or malformed flag; creds commands take --json, --config, --project, --gcp-project and --region, and never the value")
	})
	cmd.AddCommand(newCredsSetCmd(), newCredsLsCmd())
	return cmd
}

type credsOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func (o *credsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// credsOwnerOnly is the refusal both commands make, in the order the plan
// names it, and all three before anything is read and before the cloud is
// opened: a coding agent's session, a run's own process, and the GODEBUG
// http2debug leak (refuseHTTP2Debug — this command opens the owner's own
// credentials, and GODEBUG is read before main runs, so it cannot be unset in
// time). did is what the refusal says was not done: "written" or "read".
//
// The agent check is a speed bump and not a boundary, and SECURITY.md already
// records the wider fact it sits inside: an agent's session runs under the
// OPERATOR's credentials, so it can unset the marker in its own environment
// and run this command, or write the container directly with gcloud. What the
// refusal buys is that no agent goes through the program by accident, and that
// no run's process — whose environment the platform sets, not the agent — can
// reach the document at all.
func credsOwnerOnly(getenv func(string) string, did string) error {
	if m := agentMarker(getenv, processTerminal()); m != "" {
		return userErr("nothing was %s: fugaro creds works on the credentials service's one bundle secret, which holds every model credential the service issues, with the operator's own identity, so it is run by a person at their own terminal: %s", did, initflow.AgentRefusal(m))
	}
	if m := managedFlagRunMarker(getenv); m != "" {
		return userErr("nothing was %s: fugaro creds is run by a person at their own terminal, and this process is a Fugaro run's (%s is set)", did, m)
	}
	return refuseHTTP2Debug(getenv)
}

func newCredsSetCmd() *cobra.Command {
	var o credsOptions
	// generate is the review-signing key's mode (C3.9d): a flag that carries no
	// value, so this command's rule — no value from an argument, a flag or the
	// environment — is kept rather than narrowed.
	var generate bool
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Set or replace one credential in the credentials service's bundle secret (the owner)",
		Long: `Set or replace one credential in the credentials service's bundle, the ONE
Secret Manager secret the service reads (Q6). The bundle is a YAML document
whose keys are credential names and whose values are the credentials; this
command reads it, sets or replaces the entry NAME and writes it back as a new
version, and then disables the version it replaced.

NAME is a credential the service can issue, in the document's own spelling:

  ` + config.ClaudeOAuthToken + `                    the default Claude account's OAuth token (tier A)
  ` + config.ClaudeOAuthTokenPrefix + `<account>       a named Claude account's OAuth token (tier A)
  ` + config.AnthropicSecret + `                the project's Anthropic API key (tier B)
  ` + config.GitHubAppKeySecret + `                  the GitHub App's private key (no tier: the mint signs with it)
  ` + config.BitbucketSecret + `                   the upstream Bitbucket token (no tier: held for the git proxy a later release adds; a Bitbucket run's job still uses its own mounted token)
  ` + credsvc.BitbucketReadConsumerSecret + `             the Bitbucket read consumer (no tier: the service's read mint exchanges it for a read-scoped token): a YAML mapping of key, secret and workspace, one field per line, so redirect it from a file — the hidden prompt takes one line
  ` + config.ReviewSigningKeySecret(config.DefaultClaudeAccount) + `  the default account's review-signing key (no tier: the service's sign
                                     route computes a MAC with it; written by --generate below)
  ` + config.ClaudeOAuthTokenPrefix + `<account>` + config.ReviewSigningKeySuffix + `     a named account's review-signing key (no tier, and --generate's NAME too)

The value is read only from stdin: pipe or redirect it in
(fugaro creds set ` + config.ClaudeOAuthToken + ` < token-file), or, when stdin is a terminal, paste it
at a hidden prompt. It is never taken from an argument, a flag or the
environment, and never printed. --generate changes WHERE the bytes come from
and nothing else: they are drawn in this process, and they are never printed
either. One trailing newline (\n or \r\n) is dropped, and the command says so.
A value must be 4 bytes to ` + strconv.Itoa(credsvc.MaxBundleValueBytes) + `, on one line, with
no whitespace at either end; a longer value is a document the service's loader
would refuse, so it is refused here instead.

--generate writes a review-signing key and takes no value of its own
(--generate=anything is a malformed flag): a review-signing key is an HMAC key
with no upstream to paste one from, so this process draws ` + strconv.Itoa(credsvc.ReviewSigningKeyBytes) + ` bytes from
crypto/rand and writes them into the bundle. Every other NAME is refused: an
account's review-signing names are the only ones it takes, and the rotation
window's own entry is not one of them — asked to write it, the command refuses,
because it writes the current entry and derives the window's from it.

Two runs ROTATE the key. The first writes a fresh value where the entry was;
the second writes a fresh one again and carries the value it replaced, byte for
byte, under the same name with ` + config.ReviewSigningKeyPreviousSuffix + ` appended. The service signs with
the current entry and verifies against the current and the previous one, and
against nothing older, so there is no instant in which it holds neither.

Two consecutive generates REVOKE the key that was current before the first of
them: the second run's window holds the first run's key, and the original is in
neither entry, so a signature made with it stops verifying. It is not escrowed
and it is not re-signable, and the honest remedy is a re-review of the head.

` + config.GitHubAppKeySecret + ` is the ONE multi-line entry: it is an RSA private key in a PEM,
so it has to come in on stdin, from the key file the App's page downloads
(fugaro creds set ` + config.GitHubAppKeySecret + ` < PATH-TO-THE-KEY-FILE) — the hidden prompt reads one
line and cannot take a paste of a whole key, so it refuses this name and says
so. The value must be exactly one PEM block, its -----BEGIN line to its
matching -----END line and nothing else, and it must be a key the service's
mint can parse (internal/gitprov/github's ParsePrivateKey: PKCS#1 "RSA PRIVATE
KEY" or PKCS#8 "PRIVATE KEY", RSA). A single line of anything, a PEM with a
line of text around it, and a key the mint cannot use are each refused before
the write, and no refusal repeats a byte of the value.

The write is a read-modify-write of one document: the other entries keep their
bytes, and the whole document is parsed with the service's own loader
(internal/credsvc) before it is written, so what this command stores is what
the service accepts. After the write the bundle is read back and compared: if
it is not the document this command wrote, another write landed underneath and
this one lost it, which is said plainly and exits non-zero. Nothing is retried
silently.

A verified write then DISABLES the version it replaced, which the output names:
a rotated-away bundle that stays enabled keeps every superseded credential
readable by anyone who can read the container, which is the one thing a
rotation is for. Terraform cannot do this (it creates the container and manages
no version), so the command that writes a version retires the one before it. A
disable that fails is reported on stderr and does not fail the command: the new
credential is live either way.

The command needs the container to exist — the installation's Terraform creates
it (Task C1.18) and no command of ours does — and it works with the owner's own
project IAM (Project Owner or Editor: secretmanager.versions.get, .add and
.disable). There is no service account, no actAs and no operator grant on this
container: a secretVersionAdder on it is the ability to read every credential
in the bundle at once.

It is refused from a coding agent's session and from a run's process, before
anything is read, and it is never covered by --yes: there is no non-interactive
form.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("creds set needs the credential's NAME (one of the names fugaro creds set --help lists)")
			case len(args) > 1:
				return userErr("pass the value on stdin, never as an argument: arguments land in shell history and process lists")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return credsSet(cmd, o, args[0], generate)
		},
	}
	o.addFlags(cmd)
	cmd.Flags().BoolVar(&generate, "generate", false, "generate a fresh review-signing key instead of reading a value (the review-signing names only)")
	return cmd
}

func newCredsLsCmd() *cobra.Command {
	var o credsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the credentials the bundle secret holds: names and metadata, never a value (the owner)",
		Long: `List the credentials the credentials service's bundle secret holds: one row
per credential in the document, with its NAME, whether it is PRESENT, and the
document's last-changed time (the container's newest version metadata).

NAME is the name in the document's own spelling — the name fugaro creds set
takes. A named Claude account's row is ` + config.ClaudeOAuthTokenPrefix + `<account>, where <account>
is the account the policy's accounts block speaks.

Nothing else is printed: never a value, never a prefix of one, never a length
that would narrow a guess. --json carries the same shape.

The listing needs the container to exist, and it needs its newest version to be
one the service could read: an absent container, a container with no version, an
unreadable payload, a document the service's own loader refuses, and a version
that is DISABLED are each a named failure, never an empty list — a listing that
answered nothing for any of them would read as "the bundle holds no credential"
while the service is in fact issuing from one this command could not read.

It is refused from a coding agent's session and from a run's process, before
anything is read.`,
		// cobra.NoArgs answers `fugaro creds ls <value>` with `unknown command
		// "<value>" for "fugaro creds ls"` — the argument quoted back, which is
		// the one thing this command may not do. The argument position is one a
		// pasted value lands in, so it is refused without being echoed, as
		// `creds set` refuses its own.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return userErr("creds ls takes no argument, and the argument is not echoed: this position is one a pasted value can land in. Nothing was read")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return credsLs(cmd, o)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// bundleDoc is one version of the bundle document: its bytes and its version
// number (the last segment of the version's name, "3").
type bundleDoc struct {
	raw     []byte
	version string
}

// errNoBundleVersion is the container being there with no version behind it.
// C1.18's Terraform creates the container and no version, so this is the state
// the first `creds set` starts from and not a failure.
var errNoBundleVersion = errors.New("cli: the bundle container holds no version")

// errBundleDisabled is a bundle whose newest version is DISABLED: the version
// is there, the document is there, and Secret Manager refuses to hand either
// over (FAILED_PRECONDITION, as the real API and the fake both answer). It is
// NOT the same answer as "no version", and neither command may read it as one:
// for `set` it would mean editing a document this command never saw — the
// read-modify-write would drop every credential in it — and for `ls` it would
// mean answering an empty list for a container that holds one.
var errBundleDisabled = errors.New("cli: the bundle's newest version is DISABLED")

// bundleStore is this command's reach on the one container: the owner's own
// Secret Manager client (gcp.NewSecretManager with the selected config's
// endpoints and no service account, no actAs), the container's ID as the
// loader names it, and the four calls the two commands make. Nothing here
// reads a value that is not the document itself.
type bundleStore struct {
	svc     *secretmanager.Service
	project string // the GCP project the container lives in
	id      string // the container's ID (credsvc.BundleSecretID)
}

func openBundleStore(ctx context.Context, lc *localcfg.Config) (*bundleStore, error) {
	svc, err := gcp.NewSecretManager(ctx, gcpOptions(lc))
	if err != nil {
		return nil, remote(err)
	}
	return &bundleStore{svc: svc, project: lc.GCPProject, id: credsvc.BundleSecretID(lc.Name)}, nil
}

func (s *bundleStore) parent() string { return "projects/" + s.project + "/secrets/" + s.id }

// check reads the container itself, before any version of it: an installation
// that has not applied the credentials-service opt-in gets a refusal that says
// so, rather than a version read that fails as "no version" (which is the
// state a container with no document is in, and a legitimate starting point
// for the first write).
func (s *bundleStore) check(ctx context.Context) error {
	if _, err := s.svc.Projects.Secrets.Get(s.parent()).Context(ctx).Do(); err != nil {
		return s.apiErr("read", err)
	}
	return nil
}

// access reads one version of the document — "latest" or a version number —
// and answers its bytes with the version number the read resolved to. The
// version number is read from the response's own name rather than assumed, so
// the number this command prints, disables or re-reads is the server's.
//
// A version that is DISABLED is refused as such, and the state comes from the
// version's own metadata (newestIsDisabled) and never from the API's prose: a
// refusal built on a sentence would become a generic remote error the day the
// sentence changed, and the status alone is not enough either — a 400 is also
// how a malformed request answers.
func (s *bundleStore) access(ctx context.Context, version string) (bundleDoc, error) {
	r, err := s.svc.Projects.Secrets.Versions.Access(s.parent() + "/versions/" + version).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		switch {
		case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
			// The container was read a moment ago, so there is no version:
			// a body that is absent is the same answer as a version that is.
			return bundleDoc{}, errNoBundleVersion
		case errors.As(err, &ge) && ge.Code == http.StatusBadRequest && s.newestIsDisabled(ctx):
			// Secret Manager will not serve a DISABLED version's bytes: they
			// exist and the state says so. The state is the evidence, read from
			// the version list, and the 400 is only what made it worth asking.
			return bundleDoc{}, errBundleDisabled
		}
		return bundleDoc{}, s.apiErr("read", err)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return bundleDoc{}, errNoBundleVersion
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return bundleDoc{}, remote(fmt.Errorf("reading %s: the payload is not base64", safetext.Field(s.id)))
	}
	number, err := versionNumber(r.Name)
	if err != nil {
		return bundleDoc{}, remote(err)
	}
	return bundleDoc{raw: data, version: number}, nil
}

// add writes doc as a new version of the container and answers its number.
func (s *bundleStore) add(ctx context.Context, doc []byte) (string, error) {
	r, err := s.svc.Projects.Secrets.AddVersion(s.parent(), &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(doc)},
	}).Context(ctx).Do()
	if err != nil {
		return "", s.apiErr("write", err)
	}
	number, err := versionNumber(r.Name)
	if err != nil {
		return "", remote(err)
	}
	return number, nil
}

// disable turns one version DISABLED (secretmanager.versions.disable), which
// is how the version a write replaced stops being readable: the credential is
// still in the container, and nothing can read it. It is not a destroy, and
// this command destroys nothing.
func (s *bundleStore) disable(ctx context.Context, version string) error {
	if _, err := versionNumber("versions/" + version); err != nil {
		return err
	}
	if _, err := s.svc.Projects.Secrets.Versions.Disable(s.parent()+"/versions/"+version, &secretmanager.DisableSecretVersionRequest{}).Context(ctx).Do(); err != nil {
		return s.apiErr("disable", err)
	}
	return nil
}

// versionMeta is the container's newest version metadata: its number, when it
// was created and its state. It is metadata, never a value.
type versionMeta struct {
	number  string
	created time.Time
	state   string
}

// newest answers the container's newest version metadata, paging through the
// version list when it is long: a listing that read only the first page would
// name a version that is not the newest — and so a "last changed" time that is
// not the document's — once an installation has rotated more times than a page
// holds. The newest is the highest version number, which is what Secret
// Manager's own `latest` resolves to, and not the list's order.
func (s *bundleStore) newest(ctx context.Context) (versionMeta, error) {
	var out versionMeta
	page := ""
	for {
		call := s.svc.Projects.Secrets.Versions.List(s.parent()).Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		r, err := call.Do()
		if err != nil {
			return versionMeta{}, s.apiErr("read", err)
		}
		for _, v := range r.Versions {
			n, err := versionNumber(v.Name)
			if err != nil {
				return versionMeta{}, remote(err)
			}
			if out.number != "" && !newerVersion(n, out.number) {
				continue
			}
			created, err := time.Parse(time.RFC3339Nano, v.CreateTime)
			if err != nil {
				return versionMeta{}, remote(fmt.Errorf("reading %s: a version's createTime is not a time", safetext.Field(s.id)))
			}
			out = versionMeta{number: n, created: created, state: v.State}
		}
		if r.NextPageToken == "" {
			break
		}
		page = r.NextPageToken
	}
	if out.number == "" {
		return versionMeta{}, errNoBundleVersion
	}
	return out, nil
}

// newestIsDisabled reports whether the container's newest version is DISABLED,
// from the version list's own metadata: the state is a field of the version,
// and a refusal built on the API's prose instead would rest on a sentence (the
// review of C1.25). It answers false when the listing itself fails — the
// caller is already on a failure path, and its own error is the one to report.
func (s *bundleStore) newestIsDisabled(ctx context.Context) bool {
	meta, err := s.newest(ctx)
	return err == nil && meta.state == "DISABLED"
}

// newerVersion reports whether version a is newer than b. Both are decimal
// version numbers of at most a few digits, so the comparison is by length and
// then lexically, never by strconv's (which would answer 0 on an overflow and
// call two versions equal).
func newerVersion(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

// versionNumber is the version number in a version's name
// ("projects/p/secrets/s/versions/3" or "versions/3"), refused when it is not
// a number: the number is put back into a request path (the disable call), and
// a name a server invented is not something to build a request out of.
func versionNumber(name string) (string, error) {
	n := name
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	if n == "" || strings.IndexFunc(n, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", fmt.Errorf("a version name this command does not understand (%s)", safetext.Field(name))
	}
	return n, nil
}

// apiErr maps a Secret Manager failure to this command's own answer: a named
// user error for the two an operator fixes at their own terminal — the
// container is not there (Terraform creates it), and this identity may not
// touch it — and otherwise the failure itself, which the caller bounds and
// redacts (remoteErr) before it is shown. No branch carries a value: the
// request this command makes is the document, and the container's name is a
// name.
func (s *bundleStore) apiErr(op string, err error) error {
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
		return userErr("the credentials service's bundle container %s is not in project %s: the installation's Terraform creates it (Task C1.18) and no command of ours does, so apply the installation with the credentials service on, then set the first credential", safetext.Field(s.id), safetext.Field(s.project))
	case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
		return userErr("this identity may not %s the bundle container %s in project %s: these commands work with the owner's own project IAM, which carries secretmanager.versions.get, .add and .disable (Project Owner or Editor) — there is no service account, no actAs and no operator grant on this container", op, safetext.Field(s.id), safetext.Field(s.project))
	}
	return err
}

// remoteErr is a store failure as the command reports it: the remote exit
// code, the text bounded to one printable field (safetext: a server may echo a
// body) and every form of every secret in forms redacted out of it (a server
// may echo what it received, raw or base64 as sent). forms is the caller's
// writeForms or secretForms, never a single value where the request carried a
// document: see writeForms. A refusal this command already made (apiErr's
// userErr) is returned as it is: it carries no server text.
func remoteErr(err error, forms []string) error {
	var ee *ExitError
	if errors.As(err, &ee) {
		return err
	}
	return remote(errors.New(safetext.Field(agent.Redact(err.Error(), forms))))
}

// secretForms is every form of value a server could echo back: the bytes as
// they were sent and the base64 this command encoded them with.
func secretForms(value []byte) []string {
	if len(value) == 0 {
		return nil
	}
	return []string{string(value), base64.StdEncoding.EncodeToString(value)}
}

// writeForms is every form of secret a failed write can echo back, and it is
// the DOCUMENT's forms rather than the one value being set: AddVersion's
// payload is the whole document, so a server that repeats its request body —
// or its own answer to the re-read — hands back every other credential in the
// bundle, each of them as readable as the one being written. The forms are the
// document as it was sent (agent.Redact registers its lines and its JSON escape
// too), every secret the loader finds in it (credsvc.Bundle.Secrets: each
// entry's value and the read consumer's key), raw and base64, and the new value
// itself.
//
// The document is parsed rather than assumed: a document the loader refuses
// never reaches a write (credsDocRefusal answers first), so the parse here is
// the same one that already succeeded, and a nil bundle only means there is
// nothing to add to the document's own forms.
func writeForms(doc, value []byte) []string {
	forms := secretForms(doc)
	forms = append(forms, secretForms(value)...)
	if b, err := credsvc.ParseBundle(doc); err == nil {
		// The bundle's secrets, not its entries' values: the read consumer's
		// OAuth client key is a credential the entry's value does not carry
		// (credsvc.Bundle.Secrets), and a server that names the one field it
		// refused hands that key back where a value would have been redacted.
		for _, v := range b.Secrets() {
			forms = append(forms, secretForms([]byte(v))...)
		}
	}
	return forms
}

// credsSet is `fugaro creds set NAME`: the order is who is running, the name,
// the project, that the container is there, that the document it holds is one
// the service accepts — and only then the value, the write and its
// verification.
func credsSet(cmd *cobra.Command, o credsOptions, name string, generate bool) error {
	ctx := cmd.Context()
	if err := credsOwnerOnly(os.Getenv, "written"); err != nil {
		return err
	}
	// The name is checked before the project is selected, so a name that is
	// not a bundle key (a value pasted into the argument position, say) is
	// refused without a cloud call and without being echoed.
	// The kind is kept, not just checked: it is what says what shape this
	// name's value has to be — one line (every model credential and the
	// upstream Bitbucket token), a PEM (the App key), or a mapping of key,
	// secret and workspace (the Bitbucket read consumer).
	_, kind, _, ok := credsvc.CredentialKey(name)
	if !ok {
		return credsNameRefusal()
	}
	// The generated mode writes one family and only one: an HMAC key has no
	// upstream to paste from, every other credential does. It writes the
	// CURRENT entry and derives the window's entry from it, so the window's own
	// name is refused too rather than written as if it were an account's key.
	account, previous, reviewSigning := config.ReviewSigningKeyAccount(name)
	if generate && (!reviewSigning || previous) {
		return credsGenerateNameRefusal()
	}
	_, lc, err := selectProject(ctx, o.cloud)
	if err != nil {
		return err
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return err
	}
	if err := store.check(ctx); err != nil {
		return remoteErr(err, nil)
	}
	// What the document holds now, and what this write replaces. A container
	// with no version is the first write's starting point; a document the
	// loader refuses is refused here, before anything of ours is written, so
	// this command never replaces a document the service could have read.
	var before bundleDoc
	switch before, err = store.access(ctx, "latest"); {
	case errors.Is(err, errNoBundleVersion):
		before = bundleDoc{}
	case errors.Is(err, errBundleDisabled):
		return credsDisabledRefusal(store.id, "written")
	case err != nil:
		return remoteErr(err, nil)
	default:
		if _, err := credsvc.ParseBundle(before.raw); err != nil {
			return credsDocRefusal(store.id, err)
		}
	}
	var value []byte
	if generate {
		value, err = generateReviewSigningValue()
	} else {
		value, err = readBundleValue(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, kind)
	}
	if err != nil {
		return err
	}
	defer clear(value)
	// A generated write is two entries of one document: the fresh value under
	// NAME and the value it replaced under the window's name, so the window
	// exists in the version that retires the old key rather than a version
	// later (spec §6.3).
	doc, window := before.raw, ""
	switch {
	case generate:
		doc, window, err = setReviewSigningRotation(doc, name, config.ReviewSigningKeyPreviousSecret(account), string(value))
	case kind == credsvc.KindBitbucketReadConsumer:
		// The one entry whose value is not a string: the operator's value is
		// the entry's whole mapping, and it is put in as it was read, not as
		// text. This is the one place the bytes are parsed for it:
		// validateBundleValue leaves the shape to this call, so the node that is
		// validated here is the node the document gets.
		var valueNode *yaml.Node
		if valueNode, err = readConsumerNode(value); err == nil {
			doc, err = setBundleEntryValue(doc, name, valueNode)
		}
	default:
		doc, err = setBundleEntry(doc, name, string(value))
	}
	if err != nil {
		return err
	}
	// The document this command is about to write, held to the service's own
	// loader: a document that parses here is one the service reads, and one it
	// refuses (a value over the loader's per-credential bound, a document over
	// its byte bound) is refused before the write.
	if _, err := credsvc.ParseBundle(doc); err != nil {
		return credsDocRefusal(store.id, err)
	}
	version, err := store.add(ctx, doc)
	if err != nil {
		return remoteErr(err, writeForms(doc, value))
	}
	// The version this write landed as has to be the one after the version this
	// command read. Secret Manager numbers versions in order and never reuses a
	// number, so any other number means another writer added a version between
	// this command's read and this write — the long window, the one the hidden
	// prompt sits in — and the document this command wrote is built on a
	// document that is no longer the newest: the entry that writer set is not in
	// it. It is the same lost update as one that lands after the write, caught
	// before anything is retired, and it has to be caught here rather than by
	// the re-read: this command's document is the newest version, so the re-read
	// agrees with it and calls the write verified.
	if want := nextVersion(before.version); want != "" && version != want {
		return credsLostUpdateBefore(store.id, before.version, version, want)
	}
	back, err := credsWriteBack(ctx, store)
	if err != nil {
		return remoteErr(err, writeForms(doc, value))
	}
	if back.version != version || !bytes.Equal(back.raw, doc) {
		return credsLostUpdate(store.id, version, back.version)
	}
	// The version this write replaced is retired, and only after the write is
	// verified: the new credential is live either way, and a refusal above
	// leaves every version as it was.
	replaced, retired := before.version, false
	if replaced != "" && replaced != version {
		if err := store.disable(ctx, replaced); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s. Version %s of %s is still ENABLED, so the credential it holds is still readable by anyone who can read the container: disable it yourself with gcloud secrets versions disable %s --secret=%s\n",
				remoteErr(err, writeForms(doc, value)), safetext.Field(replaced), safetext.Field(store.id), safetext.Field(replaced), safetext.Field(store.id))
		} else {
			retired = true
		}
	}
	w := cmd.OutOrStdout()
	if o.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(credsSetResult{Project: lc.Name, Secret: store.id, Version: version, Replaced: replaced, Disabled: retired, Window: window})
	}
	switch {
	case replaced == "":
		_, err = fmt.Fprintf(w, "wrote %s, version %s (the bundle's first version)\n", safetext.Field(store.id), safetext.Field(version))
	case retired:
		_, err = fmt.Fprintf(w, "wrote %s, version %s (replaced version %s, now disabled)\n", safetext.Field(store.id), safetext.Field(version), safetext.Field(replaced))
	default:
		_, err = fmt.Fprintf(w, "wrote %s, version %s (replaced version %s, which is still ENABLED: see the warning on stderr)\n", safetext.Field(store.id), safetext.Field(version), safetext.Field(replaced))
	}
	if err == nil && window != "" {
		// The window is named for the operator: the value this run replaced is
		// still ACCEPTED, and a rotation whose window nobody knows about is one
		// that reads as a key that retired when the first signature verifies.
		_, err = fmt.Fprintf(w, "%s now holds the value this run replaced, which the service verifies against and never signs with\n", safetext.Field(window))
	}
	return err
}

// credsSetResult is `creds set --json`'s shape. It names the container, the
// version written, the version replaced and whether that one was disabled, and
// it holds no value and nothing derived from one.
//
// Window is the rotation window's entry — the name that now holds the value
// this run replaced — and it is absent from a write that carried nothing over
// (a first `--generate` for an account, and every write of a typed value). A
// scripted rotation has no other way to learn it: what it prints is a NAME, and
// the name is the whole of what an operator acts on (spec §6.3).
type credsSetResult struct {
	Project  string `json:"project"`
	Secret   string `json:"secret"`
	Version  string `json:"version"`
	Replaced string `json:"replaced,omitempty"`
	Disabled bool   `json:"disabled"`
	Window   string `json:"window,omitempty"`
}

// credsWriteBack is the read a write is verified with. It is a variable so a
// test can make a concurrent write land at exactly that moment: this is a
// read-modify-write with no conditional write behind it, so another owner (or
// the same owner at another terminal) can add a version between this command's
// read and its write, and the loser would silently drop the other's entry.
// Nothing is retried: the disagreement is the answer.
var credsWriteBack = func(ctx context.Context, s *bundleStore) (bundleDoc, error) {
	return s.access(ctx, "latest")
}

// credsLostUpdate is the lost concurrent update, said plainly: the write
// landed, the bundle read back is another version, and the entry this command
// set may not be the one the service will read. Nothing was retried and
// nothing was disabled, and the operator is told what to do about it.
func credsLostUpdate(id, wrote, saw string) error {
	return userErr("nothing was retried: the write landed as version %s of %s and the bundle read back is version %s, which is not the document this command wrote — another write landed between this command's read and its write, so the credential it set may not be the one the service reads. No version was disabled and nothing was written again: read the bundle with fugaro creds ls and set the credential again", safetext.Field(wrote), safetext.Field(id), safetext.Field(saw))
}

// credsLostUpdateBefore is the same lost update, caught one step earlier and
// from the only thing that shows it: the version this command's write landed
// as is not the version after the one it read, so another writer added a
// version in the window between the read and the write. That writer's entry is
// not in the document this command wrote — this command built it from the
// older read — and the version this command read is not the one it replaced,
// so nothing is disabled and nothing is retried. The re-read is not asked: it
// would see this command's own document and call the write verified.
func credsLostUpdateBefore(id, read, wrote, want string) error {
	return userErr("nothing was retried: this command read version %s of %s and its write landed as version %s, where the version after %s was %s — another write landed between this command's read and its write, so the document this command wrote is not built on the newest one and the credential that write set is not in it. No version was disabled and nothing was written again: this command's version is live, the entry it lost is not, and the way to settle it is to read the bundle with fugaro creds ls and set the credential again", safetext.Field(read), safetext.Field(id), safetext.Field(wrote), safetext.Field(read), safetext.Field(want))
}

// nextVersion is the version a write must land as, given the version this
// command read: the one after it, or the first when the container held no
// version. It is "" for a read version it does not understand, and the caller
// then checks nothing rather than refusing a write over its own arithmetic:
// the read version comes from the version's own name (versionNumber), so this
// is the command's own bookkeeping and not something to fail an operator's
// write over.
func nextVersion(read string) string {
	if read == "" {
		return "1"
	}
	n, err := strconv.Atoi(read)
	if err != nil || n < 1 {
		return ""
	}
	return strconv.Itoa(n + 1)
}

// credsDisabledRefusal is the bundle whose newest version is DISABLED. Secret
// Manager's `latest` resolves to the highest version whatever its state, so
// this is the version both commands would have read, and it will not serve it.
// Neither command treats that as "no document": the document is there, and
// guessing at it is how a read-modify-write drops every credential in it. The
// two ways out are named, and both are the owner's own.
func credsDisabledRefusal(id, did string) error {
	return userErr("nothing was %s: the newest version of the bundle container %s is DISABLED, so Secret Manager will not serve its document, and this command will not guess at it (the document is there; a write on top of a document this command could not read would drop every credential in it). Enable it again with gcloud secrets versions enable <version> --secret=%s, or set a credential with fugaro creds set once it is enabled",
		did, safetext.Field(id), safetext.Field(id))
}

// credsNameRefusal refuses a NAME that is not a credential of the bundle. The
// name is NOT echoed, and not for politeness: the argument position is one a
// pasted value can land in (the rule `fugaro secrets set`'s own name check
// follows), and nothing this command prints may carry a value.
func credsNameRefusal() error {
	return userErr("the NAME must be a credential the credentials service holds: %s (the project's Anthropic API key, tier B), %s (the default Claude account's OAuth token, tier A), %s<account> for a named account, where <account> is a Claude account name (lower case, at most %d characters), %s (the upstream Bitbucket token), %s (the Bitbucket read consumer), or the review-signing names %s and %s<account>%s, which --generate writes. The read consumer's value is %s. Nothing was read and nothing was written",
		config.AnthropicSecret, config.ClaudeOAuthToken, config.ClaudeOAuthTokenPrefix, config.MaxClaudeAccountNameLen, config.BitbucketSecret, credsvc.BitbucketReadConsumerSecret,
		config.ReviewSigningKeySecret(config.DefaultClaudeAccount), config.ClaudeOAuthTokenPrefix, config.ReviewSigningKeySuffix, readConsumerShape)
}

// credsGenerateNameRefusal refuses --generate for a NAME that is not an
// account's review-signing key. The mode is refused rather than widened,
// because every other credential of the bundle has an upstream somebody copies
// its value from and this command reads that value from stdin or a terminal;
// the window's own entry is refused because the mode writes the CURRENT entry
// and derives the window's from it, so a generate into the window's name would
// write an entry the next rotation overwrites. The name is not echoed, for
// credsNameRefusal's reason.
func credsGenerateNameRefusal() error {
	return userErr("--generate writes an account's review-signing key and takes no other NAME: %s (the default account's) or %s<account>%s (a named account's), where <account> is a Claude account name (lower case, at most %d characters). The rotation window's own entry is not one it writes — it writes the current entry and derives the window's from it — and every other credential of the bundle has a value that comes from somewhere else (a provider's page, the App's page), which is set by piping it in, without --generate. Nothing was read and nothing was written",
		config.ReviewSigningKeySecret(config.DefaultClaudeAccount), config.ClaudeOAuthTokenPrefix, config.ReviewSigningKeySuffix, config.MaxClaudeAccountNameLen)
}

// generateReviewSigningValue draws a review-signing key at the owner's terminal
// and answers the value the bundle entry holds: ReviewSigningKeyBytes from
// crypto/rand, in the hex spelling the loader decodes (credsvc's
// reviewSigningKeyBytes). The bytes leave this process only into the document —
// nothing prints them, no error carries them — and a failed draw is a refusal
// rather than a shorter key, a zero one or a retry.
func generateReviewSigningValue() ([]byte, error) {
	key := make([]byte, credsvc.ReviewSigningKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, userErr("generating a review-signing key failed and nothing was written: %v", err)
	}
	defer clear(key)
	return []byte(hex.EncodeToString(key)), nil
}

// credsDocRefusal is the document the container holds not being one the
// service's loader accepts. Nothing was written, and the refusal is the
// loader's own answer so the operator reads the same class the service would
// refuse with.
//
// The loader's error carries the key it is about, and the key is shown ONLY
// when it is a credential name the loader knows. An unknown key is arbitrary
// text a person typed into the document, which can be a value pasted where a
// name belongs — so for that one the class is the whole answer
// (credsvc.BundleRefusal, the class-only form), which is what keeps this
// command's own rule ("no value in any error") true of the document it refuses
// as well as of the value it was asked to store.
func credsDocRefusal(id string, err error) error {
	msg := credsvc.BundleRefusal(err)
	var be *credsvc.BundleError
	if errors.As(err, &be) {
		if _, _, _, known := credsvc.CredentialKey(be.Key); known && be.Key != "" {
			msg = be.Error() // a credential's name, so a name
		}
	}
	return userErr("nothing was written: the bundle in %s is not a document the credentials service accepts: %s. The service's own loader (internal/credsvc/bundle.go) reads the same schema, so a document refused here would be refused there", safetext.Field(id), safetext.Field(msg))
}

// readBundleValue reads NAME's value, whose shape KIND decides. It is
// readSecret's shape (secrets.go) — a terminal gets a hidden prompt, anything
// else is read to EOF — with this command's own rules: a model credential and
// the upstream Bitbucket token are one line each; the GitHub App's key is a PEM
// and the Bitbucket read consumer is a mapping of three fields, and the hidden
// prompt cannot take either (it reads one line), so those two names come in on
// stdin and a terminal is refused before anything is read. The bound is the
// loader's per-credential one rather than Secret Manager's.
//
// The value never comes from an argument, a flag or the environment: this
// function's only inputs are stdin and the terminal. One trailing "\n" or
// "\r\n" is dropped and said, because a value pasted from a file almost always
// has one and silently keeping it would store a credential the service cannot
// use.
func readBundleValue(ctx context.Context, in io.Reader, prompt io.Writer, name string, kind credsvc.Kind) ([]byte, error) {
	var raw []byte
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		// The refusals made before the prompt: these two values are many lines,
		// the hidden prompt reads one, and a prompt that took a first line and
		// then failed would have made the operator paste a private key, or a
		// consumer secret, into a terminal for nothing. The PEM's sentence is
		// readSecret's own for the same case (secrets.go), with this command's
		// name.
		switch kind {
		case credsvc.KindGitHubApp:
			return nil, userErr("%s is a private key, which spans several lines and the hidden prompt can't take; redirect it from the key file: fugaro creds set %s < PATH-TO-THE-KEY-FILE", name, name)
		case credsvc.KindBitbucketReadConsumer:
			return nil, userErr("%s is %s, which spans several lines and the hidden prompt can't take; redirect it from a file: fugaro creds set %s < PATH-TO-THE-FILE", name, readConsumerShape, name)
		}
		b, err := readHidden(ctx, f, prompt, name)
		if err != nil {
			return nil, err
		}
		raw = b
	} else {
		// Room for the limit plus a trailing "\r\n", and one byte to spot more.
		b, err := io.ReadAll(io.LimitReader(in, credsvc.MaxBundleValueBytes+3))
		if err != nil {
			return nil, userErr("reading the value from stdin: %v", err)
		}
		raw = b
		if t, ok := bytes.CutSuffix(raw, []byte("\n")); ok {
			raw, _ = bytes.CutSuffix(t, []byte("\r"))
			fmt.Fprintln(prompt, "note: dropped the value's trailing newline")
		}
	}
	if err := validateBundleValue(raw, kind); err != nil {
		return nil, err
	}
	return raw, nil
}

// validateBundleValue is the rules a value meets before it goes into the
// bundle: not empty, at least the redaction floor, no NUL, and within the
// bundle's own bound, credsvc.MaxBundleValueBytes — the loader's
// per-credential limit, which a longer value would break, so it is refused
// here rather than by the service later. What shape the value has to be is
// KIND's: a model credential and the upstream Bitbucket token are one line
// with no whitespace at either end (`fugaro secrets set`'s rule), the App key
// is one PEM block that the service's own mint can parse, and the Bitbucket
// read consumer's shape is readConsumerNode's — run once, at the write, so the
// node it answers is the node the document gets. Errors never quote the value.
func validateBundleValue(raw []byte, kind credsvc.Kind) error {
	switch {
	case len(raw) == 0:
		return userErr("the value is empty; pipe it in on stdin or paste it at the prompt")
	case len(raw) < minSecretBytes:
		return userErr("the value is shorter than %d bytes, too short to redact from logs", minSecretBytes)
	case len(raw) > credsvc.MaxBundleValueBytes:
		return userErr("the value is over the bundle's %d-byte per-credential limit, which the service's loader enforces; nothing was written", credsvc.MaxBundleValueBytes)
	case bytes.IndexByte(raw, 0) >= 0:
		return userErr("the value holds a NUL byte")
	}
	switch kind {
	case credsvc.KindGitHubApp:
		return validateAppKeyPEM(raw)
	case credsvc.KindBitbucketReadConsumer:
		// The one value that is not a line of text, so the one-line rules below
		// are not it. Its SHAPE is not checked here either: readConsumerNode is
		// what says the shape is right, and it runs once, in credsSet, where the
		// node it answers is the node the write stores. Checking it here as well
		// would be a second parse of the same bytes for one write — this one's
		// tree, every field's tag and style rewritten in it, thrown away and the
		// bytes parsed again for the tree that is stored.
		//
		// The bound above is this command's own, and the whole mapping is read
		// and stored under it: one limit for every value, rather than three
		// times the per-field bound the loader applies to a mapping.
		return nil
	}
	switch {
	case bytes.ContainsAny(raw, "\r\n"):
		return userErr("the value spans several lines: every credential the bundle holds is one line, except the GitHub App's key, which is a PEM and is set with fugaro creds set %s < PATH-TO-THE-KEY-FILE, and the Bitbucket read consumer, whose value is %s", config.GitHubAppKeySecret, readConsumerShape)
	case len(bytes.TrimSpace(raw)) != len(raw):
		return userErr("the value starts or ends with whitespace, most likely a copy-and-paste slip")
	}
	return nil
}

// validateAppKeyPEM is the shape of the bundle's one multi-line entry, the
// GitHub App's private key (C2.1). Three things are checked, and the third is
// the one that matters:
//
//   - It is exactly one PEM block and nothing else: it starts at a -----BEGIN
//     line (no leading blank line or text), pem.Decode finds a block, and
//     nothing but whitespace follows its matching -----END line — so the one
//     trailing newline a redirected key file ends with is fine and a second
//     key, a comment or a stray paste is not. pem.Decode is what pairs the two
//     lines: it refuses a block whose END line does not repeat its BEGIN type,
//     and a body that is not base64. It is NOT what refuses text before the
//     block (it scans for the BEGIN line), which is why the prefix is checked
//     here.
//   - It is not larger than any other entry (the bound above).
//   - It is a key the MINT can use, and it is the mint's own parser that
//     decides: github.ParsePrivateKey, the function internal/credsvc's mint
//     calls on this very value. An "EC PRIVATE KEY" is one PEM block of
//     base64 and is refused here, by the same code that would refuse it at the
//     service — so the two answers to "is this the App's key" cannot drift,
//     and a key that installs is a key that mints.
//
// Neither refusal repeats a byte of the value: the parser's own error is not
// relayed (it is another package's sentence about a credential, and it is not
// this command's to print), and what is named is the CLASS — one PEM block, an
// RSA key in PKCS#1 or PKCS#8 — which is what an operator acts on.
func validateAppKeyPEM(raw []byte) error {
	block, rest := pem.Decode(raw)
	if !bytes.HasPrefix(raw, []byte("-----BEGIN ")) || block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return userErr("%s must be exactly one PEM block — its -----BEGIN line, its base64 body, its matching -----END line — and nothing before or after it but a trailing newline. It is multi-line, so it comes in on stdin from the key file the GitHub App's page downloads: fugaro creds set %s < PATH-TO-THE-KEY-FILE", config.GitHubAppKeySecret, config.GitHubAppKeySecret)
	}
	if _, err := github.ParsePrivateKey(raw); err != nil {
		return userErr("the value is not a private key the credentials service can use: the service's mint parses this entry with internal/gitprov/github's ParsePrivateKey, which takes an RSA private key in a PKCS#1 (\"RSA PRIVATE KEY\") or PKCS#8 (\"PRIVATE KEY\") PEM; nothing was written. The App key comes in on stdin from the key file the GitHub App's page downloads: fugaro creds set %s < PATH-TO-THE-KEY-FILE", config.GitHubAppKeySecret)
	}
	return nil
}

// readConsumerShape is what the Bitbucket read consumer's value has to be, in
// the words the refusals below use. It is said once because three of them say
// it, and an operator who reads one of them and then another should not have to
// work out whether the two are the same rule.
const readConsumerShape = "a YAML mapping of exactly key, secret and workspace, one field per line"

// readConsumerNode reads the read consumer's value (F21, BB2), which is the one
// entry of the bundle that is not a single string: a mapping of exactly key,
// secret and workspace. It is read on stdin, from a file the operator wrote in
// that shape, and it is read as YAML so the three fields are named rather than
// positional — the operator has the consumer's page in front of them and copies
// three labelled values out of it.
//
// Each field's value is forced to a string, as every other entry's value is
// (setBundleNode): a workspace or a secret that looks like a number is the text
// the operator pasted, not a number, and the loader would refuse the unquoted
// form of it. A field that is a list or a nested mapping is refused here rather
// than stored, because it is not a string under any reading.
//
// No refusal repeats a byte of a field's value: the fields are named (three
// words this command's own message supplies) and nothing the operator pasted
// is. The node it answers is the node the document gets, so these bytes are
// parsed once per write (credsSet) and not once to be checked and once to be
// stored.
//
// The whole mapping is stored under this command's per-credential bound, not
// under the loader's per-FIELD one: the loader holds each field to
// MaxBundleValueBytes and the document to MaxBundleBytes, so a mapping of three
// maximal fields is a document it would accept, while readBundleValue reads at
// most MaxBundleValueBytes from stdin and validateBundleValue refuses one over
// it. One limit for every value is what the help text says and what an operator
// can hold in mind; a consumer's key, a secret and a workspace are a few hundred
// bytes, so nothing an operator has is near it.
func readConsumerNode(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// The parse error is not wrapped: a YAML error quotes the document it
		// failed on, and that document is a credential.
		return nil, userErr("the value must be %s; nothing was written", readConsumerShape)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) != 1 {
			return nil, userErr("the value must be %s; nothing was written", readConsumerShape)
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, userErr("the value must be %s; nothing was written", readConsumerShape)
	}
	seen := make(map[string]bool, 3)
	for i := 0; i+1 < len(root.Content); i += 2 {
		f, fv := root.Content[i], root.Content[i+1]
		switch f.Value {
		case "key", "secret", "workspace":
		default:
			return nil, userErr("the value must be %s, and it holds a field that is not one of the three; nothing was written", readConsumerShape)
		}
		if seen[f.Value] {
			return nil, userErr("the value holds %s twice; nothing was written", f.Value)
		}
		seen[f.Value] = true
		if fv.Kind != yaml.ScalarNode {
			return nil, userErr("%s must be one line of text; nothing was written", f.Value)
		}
		fv.Tag, fv.Style = "!!str", yaml.DoubleQuotedStyle
	}
	if !seen["key"] || !seen["secret"] || !seen["workspace"] {
		return nil, userErr("the value must be %s; nothing was written", readConsumerShape)
	}
	return root, nil
}

// setBundleEntry is the modify half of the read-modify-write: raw (the
// document as it is, possibly empty) with key set to value, re-emitted.
//
// The document is edited as YAML nodes rather than rebuilt from a Go map, so
// every entry this command was not asked about keeps its bytes — its comments,
// its quoting, its order — and the writer cannot be the thing that rewrites a
// credential nobody touched.
func setBundleEntry(raw []byte, key, value string) ([]byte, error) {
	return setBundleEntryValue(raw, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// setBundleEntryValue is setBundleEntry for a value that is already the node
// the entry holds: the read consumer's mapping (readConsumerNode), which is
// the one entry of the bundle that is not one line of text.
func setBundleEntryValue(raw []byte, key string, value *yaml.Node) ([]byte, error) {
	doc, root, err := bundleNodes(raw)
	if err != nil {
		return nil, err
	}
	setBundleNodeValue(root, key, value)
	return marshalBundle(doc)
}

// setReviewSigningRotation is the generated half of the read-modify-write, and
// the whole of a rotation: raw with key set to the freshly generated value AND
// the entry the window's name holds set to the value that entry had before, in
// one document. The window's name is answered when it was written, and empty
// when there was nothing to carry over — a first generate for an account has no
// predecessor, and an empty window's name would be a name the caller prints for
// an entry that does not exist.
//
// Both entries travel in ONE version because a version that holds the fresh key
// without its predecessor is a version whose signatures the service cannot
// verify against the key the clients still hold, and a version that holds the
// predecessor without the fresh key is a version the service signs nothing new
// with: the two entries are one state transition, and a state that is half of
// it is a service that refuses to sign (spec §6.3).
//
// The predecessor is copied from the document, not from any value the caller
// read: what the service verifies against is what the previous version actually
// holds, so a value edited into the window by hand is carried as it is.
func setReviewSigningRotation(raw []byte, key, window, value string) ([]byte, string, error) {
	doc, root, err := bundleNodes(raw)
	if err != nil {
		return nil, "", err
	}
	carried, ok := bundleNodeValue(root, key)
	setBundleNode(root, key, value)
	if ok {
		setBundleNode(root, window, carried)
	} else {
		window = ""
	}
	out, err := marshalBundle(doc)
	if err != nil {
		return nil, "", err
	}
	return out, window, nil
}

// bundleNodes reads a document into the node tree the two writers above edit:
// the document and its root mapping. An unmarshalling failure is unreachable
// (the caller parsed raw with the loader first, and the loader refuses a body
// yaml cannot read) and its message is not wrapped and not quoted — a yaml
// error carries the document it failed on, and the document is credentials.
func bundleNodes(raw []byte) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, nil, userErr("the bundle is not a document this command can edit; nothing was written")
		}
	}
	return &doc, bundleMapping(&doc), nil
}

// setBundleNode sets key to the one-line value in root, as setBundleNodeValue
// does with the node.
func setBundleNode(root *yaml.Node, key, value string) {
	setBundleNodeValue(root, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// setBundleNodeValue sets key to valueNode in root, replacing the entry's value
// node where the key is already there and appending both where it is not.
func setBundleNodeValue(root *yaml.Node, key string, valueNode *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1] = valueNode
			return
		}
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, valueNode)
}

// bundleNodeValue is the value root holds under key, and whether it holds one.
// A key mapped to something that is not a scalar counts as absent rather than
// as its rendering: the loader refuses such a document, so a value read out of
// one would be a value the service never held.
func bundleNodeValue(root *yaml.Node, key string) (string, bool) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		if node := root.Content[i+1]; node.Kind == yaml.ScalarNode {
			return node.Value, true
		}
		return "", false
	}
	return "", false
}

// bundleMapping is doc's root mapping, made one when the document has none: an
// empty document, a "null" document and a comment-only document all become an
// empty mapping, which is what the first `creds set` writes into.
func bundleMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0]
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	doc.Kind, doc.Tag, doc.Content = yaml.DocumentNode, "", []*yaml.Node{root}
	return root
}

// marshalBundle re-emits an edited document. The encoder's indent is 2 rather
// than yaml.v3's default of 4 so a block scalar (a PEM value, C2.1's App key)
// keeps the indentation it came in with: at the default every re-emission
// would re-indent every other entry's PEM.
func marshalBundle(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, userErr("the bundle could not be written back as YAML; nothing was written")
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// credsLs is `fugaro creds ls`: the document's names and the container's
// version metadata, and never anything of a value.
func credsLs(cmd *cobra.Command, o credsOptions) error {
	ctx := cmd.Context()
	if err := credsOwnerOnly(os.Getenv, "read"); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o.cloud)
	if err != nil {
		return err
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return err
	}
	if err := store.check(ctx); err != nil {
		return remoteErr(err, nil)
	}
	// The newest version's metadata first, then THAT version's document: the
	// two are one version's, so the time printed beside a name is the time of
	// the document the names came from.
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return userErr("the bundle container %s holds no version: no credential has been set yet. Set the first one with fugaro creds set %s", safetext.Field(store.id), config.ClaudeOAuthToken)
	case err != nil:
		return remoteErr(err, nil)
	}
	// The state came with the metadata, so a DISABLED newest version is refused
	// from the state itself and not from the API's answer to a read of it: the
	// bytes exist, the version says DISABLED, and no document is asked for.
	if meta.state == "DISABLED" {
		return credsDisabledRefusal(store.id, "read")
	}
	doc, err := store.access(ctx, meta.number)
	switch {
	case errors.Is(err, errBundleDisabled):
		// The same refusal, from the same metadata, when the version was
		// disabled between the listing above and this read: access asks for the
		// state rather than reading the API's prose.
		return credsDisabledRefusal(store.id, "read")
	case err != nil:
		return remoteErr(err, nil)
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return credsDocRefusal(store.id, err)
	}
	return printCreds(cmd.OutOrStdout(), lc.Name, store.id, doc, b, meta, o.asJSON)
}

// credsRow is one row of `creds ls`: a credential the document holds, its
// presence and the document version's time. There is no field here a value
// could travel in, which is the point of the row being its own type rather
// than credsvc.Entry (whose value is unexported and never rendered, but which
// is not something to hand a printer).
type credsRow struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Changed string `json:"changed"`
}

// credsList is `creds ls --json`'s shape: the container, the version the names
// came from and the rows.
type credsList struct {
	Project     string     `json:"project"`
	Secret      string     `json:"secret"`
	Version     string     `json:"version"`
	Changed     string     `json:"changed"`
	Credentials []credsRow `json:"credentials"`
}

// printCreds prints the listing. Every field it writes comes from the document's
// KEYS (never a value) and from the container's version metadata; the values
// themselves are parsed and then dropped, which is what makes "never a value,
// never a prefix, never a length" a property of the shape rather than of a
// redaction.
func printCreds(w io.Writer, project, id string, doc bundleDoc, b *credsvc.Bundle, meta versionMeta, asJSON bool) error {
	changed := meta.created.UTC().Format(time.RFC3339)
	rows := make([]credsRow, 0, b.Len())
	for _, e := range b.Entries() {
		rows = append(rows, credsRow{Name: e.Key, Present: true, Changed: changed})
	}
	slices.SortFunc(rows, func(a, b credsRow) int { return strings.Compare(a.Name, b.Name) })
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(credsList{Project: project, Secret: id, Version: doc.version, Changed: changed, Credentials: rows})
	}
	if _, err := fmt.Fprintf(w, "%s (project %s), version %s, last changed %s\n", safetext.Field(id), safetext.Field(project), safetext.Field(doc.version), changed); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tPRESENT\tLAST-CHANGED"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", safetext.TableCell(r.Name), presentCell(r.Present), r.Changed); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func presentCell(present bool) string {
	if present {
		return "yes"
	}
	return "no"
}
