package cli

import (
	"bytes"
	"cmp"
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
	"google.golang.org/api/googleapi"
	runapi "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
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
	// bt carries a typed Bitbucket token's own facts (T11): the only flags of
	// this command that hold something other than the value's whereabouts.
	var bt bitbucketTokenFlags
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
  ` + config.CredentialSecretPrefix + `<name>` + config.CredentialKeySuffix + `        one declared credential's own key: the service-served credential (` + config.CredentialTypeAnthropic + `) at tier B, and the runner-path one (` + config.CredentialTypeOpenRouter + `) written into the repository-credential entries the service serves a routed call from, one per repository ` + "`claude_accounts.<name>.allow_data_to`" + ` names — the entry's own name carries the repository, so the names it writes are not this one. Either half is read by a credentials service from ` + keyCredentialSince + ` (` + repoCredentialSince + ` for the runner path) on: where the service that serves the bundle is older, this write is refused and ` + "`fugaro update`" + ` is the one command that moves it
  ` + config.GitHubAppKeySecret + `                  the GitHub App's private key (no tier: the mint signs with it)
  ` + config.BitbucketSecret + `                   the upstream Bitbucket token (no tier: the git proxy issues this entry for the repositories the published node admits with proxy: true, whose jobs mount no copy of their own, and every other Bitbucket repository's job keeps its own mounted copy)
  ` + credsvc.BitbucketReadConsumerSecret + `             the Bitbucket read consumer (no tier: the service's read mint exchanges it for a read-scoped token): a YAML mapping of key, secret and workspace, one field per line, so redirect it from a file — the hidden prompt takes one line
  ` + config.ProviderKeySecret + `                 the project's provider credential (tier C: the service's provider route authenticates upstream with it): a YAML mapping of key, base_url and auth, one field per line, so redirect it from a file — the hidden prompt takes one line. It is read by a credentials service from ` + providerEntrySince + ` on: where the service that serves the bundle is older, this write is refused and ` + "`fugaro update`" + ` is the one command that moves it
  ` + config.BitbucketTokenPrefix + `<name>   a typed Bitbucket access token (no tier: the service's git proxy issues this entry for the requests the published node's coverage admits, R13): the ONE NAME this command CHECKS before it writes, and the one whose write carries a record beside the entry. See "A typed Bitbucket token" below
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

A typed Bitbucket token (` + config.BitbucketTokenPrefix + `<name>) is the ONE NAME whose
write is more than a value: the token's TYPE, what it COVERS and its optional
expiry are facts the operator states (D2350, R1), so they are read here — as
flags, or as the same questions at the terminal — and stored beside the entry.
The value is read exactly as every other one is.

  --type          repo, project or workspace
  --coverage      what the token reaches: ws/repo for a repository token, ws/*
                  for a workspace token, or the workspace slug alone for a
                  project token. Repeat it for a token that names more than one
  --project-uuid  a project token's project, as Bitbucket's API spells it
  --project-key   the same project's key, for an operator who has only that:
                  the check resolves it to the UUID, which is what is stored,
                  and the key is kept for display
  --expires       the day the token stops working, YYYY-MM-DD

The check runs BEFORE anything is written, and it is the whole reason this
NAME is not like the others: this command reads Bitbucket with the token, in
the coverage the record itself states and nowhere outside it, and a token that
does not work, or that does not reach what the record states, is refused with
one precise message and NOTHING is written (D2352). The one thing it cannot
prove is the write scope: Bitbucket has no dry run for a push or a pull
request, so the two write scopes D470 tells the operator to tick are reported
as NOT verified and the first real use is what tries them (D2353).

A write that passes is three records of one token, and all three are written:
the bundle entry, the project config's bitbucket_tokens record, and the check's
own answer — when it last passed and the account UUID it returned — into the
project's credential state. The identity is written ONLY here and is never read
from the config, so a hand-edited record cannot state an identity a run would
trust (R12). Nothing of the record reaches a run until the policy is published:
run ` + "`fugaro update`" + ` to publish this token's coverage to runs.

This is the one NAME whose facts are read as flags, and --json asks for them
and nothing else: a machine has no terminal to answer at, so a --json write
carries --type and --coverage (and --project-uuid or --project-key for a
project token) or it is refused, naming the flag it needs. A terminal is asked
for each fact that no flag gave.

It is read by a credentials service from ` + bitbucketTokenSince + ` on: where the
service that serves the bundle is older, this write is refused before anything
is read from you and before anything is written.

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
anything is read, and no flag covers it: there is no non-interactive
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
			return credsSet(cmd, o, args[0], generate, &bt)
		},
	}
	o.addFlags(cmd)
	cmd.Flags().BoolVar(&generate, "generate", false, "generate a fresh review-signing key instead of reading a value (the review-signing names only)")
	bt.addFlags(cmd)
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

A Bitbucket token carries more, because a name and a PRESENT answer are the
same for a token that serves every call of its repository and for one Bitbucket
refuses: its type, what it covers, the expiry the operator recorded, when the
add-time check last proved it, the state the credential state store holds
(available, exhausted, backed_off or broken), the time a hold ends when it has
one, and how many requests it has served in the current window. ` + config.BitbucketSecret + `
is the legacy key: the implicit untyped token, whose coverage is the
project_secrets entry's allow_data_to list and whose expiry is the
secret_expires entry of the same name.

Nothing else is printed: never a value, never a prefix of one, never a length
that would narrow a guess. --json carries the same shape, with those facts as
fields of the row.

The listing needs the container to exist, and it needs its newest version to be
one the service could read: an absent container, a container with no version, an
unreadable payload, a document the service's own loader refuses, and a version
that is DISABLED are each a named failure, never an empty list — a listing that
answered nothing for any of them would read as "the bundle holds no credential"
while the service is in fact issuing from one this command could not read.

A document holding a Bitbucket token also needs the project's credential state,
and a state store that does not answer is a named failure too, for the same
reason: the row would say nothing about a token the store holds something
about. A project with no Firebase backend has no such store, and its rows carry
the record's facts alone.

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

// openBundleStore selects the container from the SELECTED CONFIG and from
// nothing else, which is what makes the same two verbs serve both deployments
// the credentials service has (docs/workspaces/spec.md §4): an installation's
// config names the installation's bundle, and the config of the project a
// workspace is named after (workspaceOwnerConfig — the config the owner's
// member table and cap are written in) names the workspace's own project and
// therefore the workspace's ONE bundle, the pool its service reads
// (credsvc.WorkspaceBundleSecretID, which is the same container
// credsvc.BundleSecretID derives for that name). There is deliberately no flag
// and no second command for the workspace case: a container chosen by an
// argument is a container an operator can be pointed at by one, and the
// deployment it belongs to is the thing that decides which pool a credential
// is in.
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
func credsSet(cmd *cobra.Command, o credsOptions, name string, generate bool, bt *bitbucketTokenFlags) error {
	ctx := cmd.Context()
	if err := credsOwnerOnly(os.Getenv, "written"); err != nil {
		return err
	}
	// The name is checked before the project is selected, so a name that is
	// not a bundle key (a value pasted into the argument position, say) is
	// refused without a cloud call and without being echoed.
	// The kind is kept, not just checked: it is what says what shape this
	// name's value has to be — one line (every model credential and the
	// upstream Bitbucket token), a PEM (the App key), or a mapping of three
	// named fields (the Bitbucket read consumer's, the provider credential's).
	_, kind, _, ok := credsvc.CredentialKey(name)
	if !ok {
		return credsNameRefusal()
	}
	// A member of the Bitbucket token family (D2350) is a credential of this
	// command's like any other, and the one NAME whose write is more than a
	// value: the record it carries (T2), the check that must pass before
	// anything is written (T7, D2352) and the identity that goes to the state
	// store (T6, R12) all belong to the same write. It is the loader's own
	// KindGit, so what is read from stdin is one line, exactly as the legacy
	// token's value is.
	bitbucketName, family := config.BitbucketTokenAccount(name)
	// The generated mode writes one family and only one: an HMAC key has no
	// upstream to paste from, every other credential does. It writes the
	// CURRENT entry and derives the window's entry from it, so the window's own
	// name is refused too rather than written as if it were an account's key.
	account, previous, reviewSigning := config.ReviewSigningKeyAccount(name)
	if generate && (!reviewSigning || previous) {
		return credsGenerateNameRefusal()
	}
	if !family && bt.given(cmd) {
		return bt.onlyFamilyRefusal()
	}
	sel, lc, err := selectProject(ctx, o.cloud)
	if err != nil {
		return err
	}
	// A key credential's own derived name is a bundle key of this command's
	// (MC23, D1670), and which HALF serves the credential is what decides
	// whether this document is where its value belongs: the credentials service
	// reads the bundle, and the runner path's types are served by the job's own
	// mounts. Judged here — after the project's config is read and before the
	// container is opened, before the document is fetched and before the value
	// is read — so a name whose half is not this one is refused with nothing
	// touched and nothing typed at a terminal.
	//
	// A RUNNER-path key credential's value IS this document's since KS2 (D1670
	// reversed by D2975): its key goes into C1's repo-credential family rather
	// than the tier-B credential-<name>-key entry a service-served key keeps,
	// so the write below is providerKeyBundleWrite's and the name resolves to
	// the provider's. It is answered here because the two facts the command
	// needs from it — that this is a provider's key, and which provider — are
	// the config's, and every refusal before the value is read is a refusal
	// with nothing typed at a terminal.
	providerKey, providerKeyWrite := "", false
	if credName, ok := config.CredentialBySecret(name); ok {
		if err := keyCredentialDeclarationRefusal(lc, name, credName); err != nil {
			return err
		}
		// Which HALF serves the credential's type decides which entry this write
		// sets, and no longer whether it is admitted (KS2, D1670 reversed by
		// D2975): a service-served credential (anthropic) keeps the tier-B entry
		// under the credential's own name, and a runner-path one (openrouter) is
		// written into C1's repository-credential entries — one per repository
		// claude_accounts.<name>.allow_data_to names — which is where the
		// credentials service reads a provider key from now.
		if cred, declared := config.CredentialNamed(lc.Credentials, credName); declared && config.CredentialServingPath(cred.Type) == config.CredentialPathRunner {
			providerKey, providerKeyWrite = credName, true
		}
	}
	// The record and the identity are this command's to write and nobody
	// else's, and both of them are local: the shared config carries no
	// bitbucket_tokens block (it is operator-only, localcfg's Shared), and the
	// state store is the project's own. Both are checked before the bundle is
	// touched, so a family write that could not complete all three writes is
	// refused with nothing written at all.
	var btState *bitbucketStateWriters
	if family {
		if btState, err = bitbucketWriters(ctx, sel, lc); err != nil {
			return err
		}
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return err
	}
	if err := store.check(ctx); err != nil {
		return remoteErr(err, nil)
	}
	// A provider key is the whole project's, so the person who writes one is a
	// project owner (D3006) — the ONE probe of that question (F36). It stands
	// here, with the container read and before the document is fetched, before
	// the run opens and before the value is read: a caller who is not an owner
	// is refused with nothing typed at a terminal. `fugaro creds` has already
	// refused the three ways credsOwnerOnly refuses, at the command's door.
	if providerKeyWrite {
		if err := bundleOwnerProbe(ctx, store, lc, "written"); err != nil {
			return err
		}
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
	// The entries of the bundle whose reader is newer than its writer's
	// release, and so the skews this command can create and is refused for
	// (AK10, F26; T11 for the family): a document holding the provider key or a
	// typed Bitbucket token is a document only a credentials service from that
	// entry's own release on can read, and the service's answer to an unknown
	// key is a refusal of the WHOLE document, so the entry written into an
	// older one halts every caller of it. What the document will HOLD decides —
	// the provider key when it is already there or is this write's, the family
	// when the same — and the newest of those releases is the one required,
	// because a service has to read every entry the document carries and not
	// just the one being written. A document holding neither makes no Cloud Run
	// read at all, which is every write of every other credential. It is judged
	// before the value is read — reading that value is the one step of this
	// command that cannot be taken back — and before any of a token's own facts
	// are asked for.
	familyEntry := config.BitbucketTokenPrefix + "<name>"
	if family {
		familyEntry = name
	}
	// A key credential's own entry is the third (MC23, D1670): the family is
	// spelled for every write that is not one of its own, the way the Bitbucket
	// token family's is, so the sentence names the family rather than one
	// credential's container.
	keyEntry := config.CredentialSecretPrefix + "<name>" + config.CredentialKeySuffix
	keyCredential := false
	// A RUNNER-path key credential's write is the fourth family and no longer
	// this document's tier-B entry (KS2, D1670 reversed, D2975): its key goes
	// into C1's repo-credential--<slug>--<credential> family, one entry per
	// repository the credential is bound to, and providerKeyBundleWrite below
	// is the write — and the guard — of that family. So the family's reader is
	// judged here only for a document that already HOLDS one (a write of
	// another credential into a bundle the copy has filled), and the entry this
	// write itself sets carries its own since-constant inside that writer.
	if credName, ok := config.CredentialBySecret(name); ok {
		keyEntry, keyCredential = config.CredentialSecret(credName), true
	}
	if req := bundleReaderRequirement(
		bundleEntryReader{entry: config.ProviderKeySecret, since: providerEntrySince, held: kind == credsvc.KindProvider || bundleHoldsProviderEntry(before.raw)},
		bundleEntryReader{entry: familyEntry, since: bitbucketTokenSince, held: family || bundleHoldsFamilyEntry(before.raw)},
		bundleEntryReader{entry: keyEntry, since: keyCredentialSince, held: keyCredential && !providerKeyWrite || bundleHoldsKeyCredentialEntry(before.raw)},
		bundleEntryReader{entry: repoCredentialFamily, since: repoCredentialSince, held: !providerKeyWrite && bundleHoldsRepoCredentialEntry(before.raw)},
	); req != nil {
		if err := refuseAnEntryTheServiceCannotRead(ctx, lc, req.entry, req.since); err != nil {
			return err
		}
	}
	// The run's own account (CL7a): one line per step and the log beside it
	// (D1352). It opens at the write and not at the command's door: every
	// refusal above — the project's own selection, the container's check, a
	// document the service could not have read — is answered from what is
	// already there and writes nothing, so a refused invocation keeps neither
	// a step nor a log; a log each one filed would be one of the 50 this
	// command's name keeps (pruneLogs), evicting the newest real one. The two
	// refusals left are the value's own reading and the document's, and they
	// abandon the step rather than failing it: nothing was written, and a
	// refused write is not a step that happened (G3). The four returns at and
	// after the write are not — a version of this credential may exist, so
	// they end the step with a cross (credsStepFailed).
	run := stepRunFor(cmd, "creds-set", cmd.OutOrStdout())
	defer run.close()
	st := run.rep.Step("Writing " + safetext.Field(store.id))
	defer st.Abandon()
	// A typed Bitbucket token's own facts (T2) are asked for BEFORE its value:
	// a record the config's own rule refuses — a type and a coverage that state
	// different things (R1) — is refused without a secret ever being typed at a
	// terminal, and the value stays the last question before the check that
	// must pass for anything to be written (T7, D2352).
	var btRec config.BitbucketToken
	if family {
		if btRec, err = bitbucketTokenRecord(cmd, o.asJSON, bt, bitbucketName); err != nil {
			return err
		}
	}
	var value []byte
	if generate {
		value, err = generateReviewSigningValue()
	} else {
		// The hidden prompt is read with the line in flight taken down: a
		// frame drawn on the line the prompt is written on swallows it and the
		// answer typed under it (G1).
		err = run.ask(func() error {
			var rerr error
			value, rerr = readBundleValue(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, kind)
			return rerr
		})
	}
	if err != nil {
		return err
	}
	defer clear(value)
	// The value this run read is held for the rest of it, so an echo of it
	// reaches neither the run's own output nor its log (F6).
	run.hold(value)
	// The add-time check (T7, D2352) runs here, with the value read and nothing
	// written yet: a token that does not work, or that does not reach what its
	// record states, is refused in front of the operator with the check's own
	// one precise sentence, and this command then writes no bundle entry, no
	// record and no identity at all.
	//
	// A project record may state its key instead of its UUID, and the check is
	// what resolves it: R1 and R2 match a project by UUID and a key can be
	// renamed, so the UUID the check read is what goes into the record and the
	// operator's key stays beside it for display.
	var btRes bbtokens.Result
	if family {
		if btRes, err = checkBitbucketToken(ctx, lc, btRec, string(value)); err != nil {
			return err
		}
		if btRes.ProjectUUID != "" {
			btRec.ProjectUUID = btRes.ProjectUUID
		}
	}
	// A generated write is two entries of one document: the fresh value under
	// NAME and the value it replaced under the window's name, so the window
	// exists in the version that retires the old key rather than a version
	// later (spec §6.3).
	doc, window := before.raw, ""
	var res bundleAddResult
	if providerKeyWrite {
		// The runner-path key credential's write, whole: one entry of C1's
		// family per repository the credential is bound to (O8 — a shared key
		// is entered once for each, and the several entries travel in ONE
		// version because a version holding a shared key for some of its
		// repositories is a key that serves a run on some of them), through the
		// same guard, the same parse, the same add-and-verify and the same
		// retirement every other write of this document goes through. It is one
		// call and not four steps here because it is the same four steps the
		// other two writers of a provider key make (F27), and a second copy of
		// them is the one place the three could drift.
		repos := providerKeyCredentialRepos(lc, providerKey)
		if len(repos) == 0 {
			return providerKeyNoReposRefusal(lc, providerKey)
		}
		res, err = providerKeyBundleWrite(ctx, cmd, st, store, before, lc, providerKey, repos, value)
	} else {
		switch {
		case generate:
			doc, window, err = setReviewSigningRotation(doc, name, config.ReviewSigningKeyPreviousSecret(account), string(value))
		case kind == credsvc.KindBitbucketReadConsumer || kind == credsvc.KindProvider:
			// The two entries whose value is not a string: the operator's value is
			// the entry's whole mapping, and it is put in as it was read, not as
			// text. This is the one place the bytes are parsed for them:
			// validateBundleValue leaves the shape to this call, so the node that is
			// validated here is the node the document gets.
			var valueNode *yaml.Node
			if kind == credsvc.KindProvider {
				valueNode, err = readProviderNode(value)
			} else {
				valueNode, err = readConsumerNode(value)
			}
			if err == nil {
				doc, err = setBundleEntryValue(doc, name, valueNode)
			}
		default:
			doc, err = setBundleEntry(doc, name, string(value))
		}
		if err == nil {
			// The document this command is about to write, held to the service's
			// own loader: a document that parses here is one the service reads,
			// and one it refuses (a value over the loader's per-credential bound,
			// a document over its byte bound) is refused before the write.
			if _, perr := credsvc.ParseBundle(doc); perr != nil {
				return credsDocRefusal(store.id, perr)
			}
			res, err = bundleAddVerified(ctx, cmd, st, store, before, doc, value)
		}
	}
	if err != nil {
		return err
	}
	version, replaced, retired := res.version, res.replaced, res.retired
	w := cmd.OutOrStdout()
	// The two other records of a typed token (R12, T2) are written only here,
	// once the bundle entry is live and read back: the record that tells the
	// proxy what the token reaches and the identity that tells a run's own
	// comments apart are both about a credential that is really there. Which
	// order they are written in is the fail-closed one — a value with no record
	// serves nothing (R14), so the record first and the check's answer second,
	// and a failure of either is the step's own failure rather than a silent
	// half-write.
	var btJSON *credsSetBitbucketResult
	if family {
		recOut := w
		if o.asJSON {
			// --json owns stdout, and userSave's "wrote <path>" line would be
			// a byte of prose inside a document a script parses.
			recOut = cmd.ErrOrStderr()
		}
		if err := btState.writeRecord(recOut, btRec); err != nil {
			return credsStepFailed(st, err)
		}
		if err := btState.writeStatus(ctx, name, btRes.Identity, time.Now()); err != nil {
			return credsStepFailed(st, err)
		}
		btJSON = &credsSetBitbucketResult{
			Name: name, Type: btRec.Type, Coverage: btRec.Coverage,
			ProjectUUID: btRec.ProjectUUID, ProjectKey: btRec.ProjectKey, Expires: btRec.Expires,
			Identity: btRes.Identity, WriteVerified: btRes.WriteVerified,
		}
	}
	if o.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(credsSetResult{Project: lc.Name, Secret: store.id, Version: version, Replaced: replaced, Disabled: retired, Window: window, Bitbucket: btJSON})
	}
	detail := "version " + safetext.Field(version)
	switch {
	case replaced == "":
		detail += " (the bundle's first version)"
	case retired:
		detail += " (replaced version " + safetext.Field(replaced) + ", now disabled)"
	default:
		detail += " (replaced version " + safetext.Field(replaced) + ", which is still ENABLED: see the warning on stderr)"
	}
	st.Done(detail)
	if window != "" {
		// The window is named for the operator: the value this run replaced is
		// still ACCEPTED, and a rotation whose window nobody knows about is one
		// that reads as a key that retired when the first signature verifies.
		// It is a line of its own and not the step's tail, because it is the
		// one fact of the run a person may not read past.
		_, err = fmt.Fprintf(w, "%s now holds the value this run replaced, which the service verifies against and never signs with\n", safetext.Field(window))
	}
	if family {
		// What the bundle write alone cannot say: what the record states, the
		// identity the check read, the scopes it could not prove and the
		// republish the coverage needs (T11).
		bitbucketWriteLines(w, name, btRec, btRes)
	}
	// The bundle is this command's to write and the credential ROUTE is not:
	// where each repository's credentials are served from is derived from the
	// document just written, per repository and per kind, by the next `fugaro
	// update` (update_route.go, WS9/D2982) — a member's own publish and never
	// this one's. So a write that changes what the bundle holds is a write the
	// published route no longer agrees with, and nothing else republishes it: a
	// coding agent's session may not write the cloud, and a run reads the route
	// rather than making one. The line is last because it names the one thing
	// of this write the person still owes.
	fmt.Fprintf(w, "the credential route of this project's repositories — which of them take a credential from a workspace's service and which from this project's own copy — is published by %s: run it to republish the route this write changes\n", selfCommand()+" update")
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
// Bitbucket is the typed Bitbucket token's own record, present for that family
// alone: what the write stored in the local config, the identity the check read
// and whether the write scopes were proven (they were not, D2353). A field
// added to this shape is a field an older reader ignores, which is what makes
// this the right place for it: the alternative is a line of prose inside the
// document a script parses.
type credsSetResult struct {
	Project   string                   `json:"project"`
	Secret    string                   `json:"secret"`
	Version   string                   `json:"version"`
	Replaced  string                   `json:"replaced,omitempty"`
	Disabled  bool                     `json:"disabled"`
	Window    string                   `json:"window,omitempty"`
	Bitbucket *credsSetBitbucketResult `json:"bitbucket,omitempty"`
}

// credsSetBitbucketResult is that record. It holds the token's NAME and never
// its value, and WriteVerified is the machine-readable half of the "write
// scopes not verified" line.
type credsSetBitbucketResult struct {
	// Name is the token's DOCUMENT KEY and not the bare account suffix (R5):
	// it is the name this command was given, the name the bundle entry and the
	// state store's node are spelled with, and the name every audit line
	// carries.
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Coverage      []string `json:"coverage"`
	ProjectUUID   string   `json:"project_uuid,omitempty"`
	ProjectKey    string   `json:"project_key,omitempty"`
	Expires       string   `json:"expires,omitempty"`
	Identity      string   `json:"identity,omitempty"`
	WriteVerified bool     `json:"write_verified"`
}

// credsStepFailed ends the run's step as failed with the error the command
// returns, on the four paths a version of the credential may exist: the add
// that answered an error, the two lost updates, and the re-read that could not
// be made. The run reached a result for this step — this credential is not
// where it was — so the account carries the cross, the error and the log's
// path (G3, G8), and the refusal that wrote nothing keeps its Abandon.
func credsStepFailed(st *report.Step, err error) error {
	// One line, whatever the server said: the step's own line is one line, and
	// a message with newlines in it would break the account in two.
	st.Failed(errors.New(oneLine(err.Error())), nil)
	return err
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

// bundleAddResult is what one verified write of the bundle answered: the
// version it landed as, the version it replaced, and whether that one was
// disabled (which is every replacement, unless the disable itself failed and
// the warning below was printed instead).
type bundleAddResult struct {
	version  string
	replaced string
	retired  bool
}

// bundleAddVerified is the write tail every writer of the bundle shares, from
// the add to the disabled predecessor (KS2 moved it out of credsSet, whose
// tail it was, because a provider key has three writers and the tail is the
// part that must not have three versions).
//
// The order it keeps is credsSet's own and each step has its reason there:
// the add, the check that the version this write landed as is the one after
// the version that was read (a lost update that the re-read below cannot see,
// because this document IS the newest version by then), the re-read that the
// write is verified with, and only then the retirement of the version it
// replaced — after the new credential is live, so a refusal above leaves every
// version as it was.
//
// A failed disable is not a failed write: the warning names the version that
// is still ENABLED and the one gcloud call that retires it, and the write
// answers retired == false. forms is writeForms' answer, computed here from
// the document and the value rather than passed in: a server that echoes what
// it received hands back every credential the document holds, in the document
// every writer sent.
func bundleAddVerified(ctx context.Context, cmd *cobra.Command, st *report.Step, store *bundleStore, before bundleDoc, doc, value []byte) (bundleAddResult, error) {
	var out bundleAddResult
	version, err := store.add(ctx, doc)
	if err != nil {
		// An add that failed may still have added a version: Secret Manager
		// answers what it did, and a timeout says nothing about it. The step is
		// ended as failed, so the run is not accounted done over a credential
		// that may exist (G3, G8).
		return out, credsStepFailed(st, remoteErr(err, writeForms(doc, value)))
	}
	if want := nextVersion(before.version); want != "" && version != want {
		return out, credsStepFailed(st, credsLostUpdateBefore(store.id, before.version, version, want))
	}
	back, err := credsWriteBack(ctx, store)
	if err != nil {
		return out, credsStepFailed(st, remoteErr(err, writeForms(doc, value)))
	}
	if back.version != version || !bytes.Equal(back.raw, doc) {
		return out, credsStepFailed(st, credsLostUpdate(store.id, version, back.version))
	}
	out = bundleAddResult{version: version, replaced: before.version}
	if out.replaced != "" && out.replaced != version {
		if err := store.disable(ctx, out.replaced); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s. Version %s of %s is still ENABLED, so the credential it holds is still readable by anyone who can read the container: disable it yourself with gcloud secrets versions disable %s --secret=%s\n",
				remoteErr(err, writeForms(doc, value)), safetext.Field(out.replaced), safetext.Field(store.id), safetext.Field(out.replaced), safetext.Field(store.id))
		} else {
			out.retired = true
		}
	}
	return out, nil
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
	return userErr("the NAME must be a credential the credentials service holds: %s (the project's Anthropic API key, tier B), %s (the default Claude account's OAuth token, tier A), %s<account> for a named account, where <account> is a Claude account name (lower case, at most %d characters), %s (the upstream Bitbucket token), %s (the Bitbucket read consumer), %s (the project's provider credential), or the review-signing names %s and %s<account>%s, which --generate writes. The read consumer's value is %s, and the provider credential's is %s. Nothing was read and nothing was written",
		config.AnthropicSecret, config.ClaudeOAuthToken, config.ClaudeOAuthTokenPrefix, config.MaxClaudeAccountNameLen, config.BitbucketSecret, credsvc.BitbucketReadConsumerSecret, config.ProviderKeySecret,
		config.ReviewSigningKeySecret(config.DefaultClaudeAccount), config.ClaudeOAuthTokenPrefix, config.ReviewSigningKeySuffix, readConsumerShape, providerShape)
}

// keyCredentialDeclarationRefusal is the refusal a name of the key credential
// namespace takes when the config declares no such key credential (MC22, MC23):
// the two states it can be in, each answered with what the owner would have to
// do instead.
//
// It is one function because it is one rule at two write paths — `fugaro
// secrets set credential-<name>-key` refuses the container as a project-wide
// secret (projectWideRefusal), and `fugaro creds set` refuses the entry
// (credsSet's own check on the name) — and an operator who meets both commands must
// not read two stories about the same name. The name the first state asks for is
// the credential's, which is what `accounts add` takes, and never the secret's.
// nil is the config's answer that the name IS a declared key credential's.
func keyCredentialDeclarationRefusal(lc *localcfg.Config, secret, name string) error {
	cred, declared := config.CredentialNamed(lc.Credentials, name)
	switch {
	case !declared:
		return userErr("%s is a key credential's own secret name, and this project declares no credential %s: declare one with '%s accounts add %s --type openrouter' (or --type anthropic), which stores its key under this name itself",
			pluginwire.Printable(secret), pluginwire.Printable(name), selfCommand(), pluginwire.Printable(name))
	case !config.KeyCredentialType(cred.Type):
		return userErr("%s is a key credential's own secret name, and %s is declared %s: a credential of that type stores its value under its Claude account's name (%s), so nothing would read this one — its type is changed in the file, where a type is a decision",
			pluginwire.Printable(secret), pluginwire.Printable(name), pluginwire.Printable(cred.Type), pluginwire.Printable(config.ClaudeAccountSecret(name)))
	}
	return nil
}

// bundleEntryReader is one entry of the bundle the guard below judges: the key,
// the first release whose credentials service can read it, and whether the
// document this write leaves in place holds it.
type bundleEntryReader struct {
	entry string
	since string
	held  bool
}

// bundleReaderRequirement answers the entry whose reader this write must not
// outrun, among the entries the document it leaves in place will hold, or nil
// when it holds none — which is every write of an ordinary credential, and the
// answer that makes no Cloud Run read at all.
//
// The NEWEST of the held entries is the answer, and not each of them in turn: a
// document carrying two entries needs a service that reads both, so the release
// required is the later of the two `since`s, and a refusal naming the older one
// would tell an operator to reach a release that still cannot read the other.
func bundleReaderRequirement(entries ...bundleEntryReader) *bundleEntryReader {
	var out *bundleEntryReader
	for i := range entries {
		if !entries[i].held {
			continue
		}
		if out == nil || imagePredates(out.since, entries[i].since) {
			out = &entries[i]
		}
	}
	return out
}

// refuseAnEntryTheServiceCannotRead is the guard at the bundle write (AK10,
// F26; T11 for the Bitbucket token family): the write is refused when the
// document it would leave in place carries an entry the credentials service
// that will read it predates.
//
// The reason there is a guard at all is the loader's own strictness: an unknown
// KEY refuses the WHOLE document (credsvc.ErrBundleUnknownKey) rather than the
// entry, and the service answers that as a service-wide refusal — so a bundle
// written by a CLI newer than the service that reads it halts every caller of
// that service, coding runs included, until `fugaro update` moves it.
// `fugaro creds set` is the bundle's only writer, so this is the one write that
// can be stopped before it lands.
//
// What is DEPLOYED decides, and the pin only when nothing is deployed
// (bundleReaderRelease, which says why); a record that cannot be read refuses
// the write rather than warning about it, which is where this parts from
// checkReviewAdmission — the alternative here is the halt above.
func refuseAnEntryTheServiceCannotRead(ctx context.Context, lc *localcfg.Config, entry, since string) error {
	rel, image, why := bundleReaderRelease(ctx, lc)
	switch {
	case why != "":
		return bundleEntryRefusal(entry, since, why)
	case rel == "":
		// Nothing serves the bundle and nothing is coming: no service can
		// refuse the document, and there is none to halt. The write as today.
		return nil
	case !imagePredates(rel, since):
		return nil
	}
	return bundleEntryRefusal(entry, since, fmt.Sprintf("this installation's credentials service is %s (image %s), and that release's service does not know the entry", rel, pluginwire.Printable(image)))
}

// bundleEntryRefusal is the one sentence the guard refuses with, and why is
// the clause that says which state was found — the read that failed, the
// reference that carries no release, or the release that predates the entry.
// It names `fugaro update` because that is the whole fix (CLAUDE.md's rule):
// the command that resolves this release's credentials service image and
// applies it.
func bundleEntryRefusal(entry, since, why string) error {
	return userErr("%s is an entry of the bundle that only a credentials service from fugaro %s on can read: the service's loader refuses a document holding a key it does not know, and it refuses the WHOLE document rather than that entry, so a bundle the deployed service cannot read halts every caller of that service, coding runs included. This write is refused: %s. Run fugaro update: that is the whole upgrade, and the command that moves this installation's credentials service onto this release's image. Nothing was read from you and nothing was written",
		pluginwire.Printable(entry), since, why)
}

// bundleReaderRelease answers the release of the credentials service that will
// read the document this write leaves in place, with the image that release
// was read from for the refusal to print.
//
// Two records name a release and this reads both, because the one that decides
// is what is DEPLOYED: the revisions Cloud Run serves the bundle with now
// (servingCredentialsRelease), and the installation's own pin
// (installationServiceImageOf) only when Cloud Run holds no such service —
// nothing serves yet and a service is coming, at the release the
// installation's record names. The pin cannot raise the answer: `fugaro update`
// writes terraform.tfvars.json as its stages plan and applies it afterwards, so
// a run whose apply the owner declined or which failed at the apply leaves the
// pin ahead of what serves, and a guard reading the pin alone writes the very
// bundle F26 found.
//
// rel is "" with no why when nothing serves the bundle and nothing is coming,
// which is not "unknown" but an answer: there is no service to refuse the
// document. why is non-empty when the record that decides cannot be read at
// all, which the caller refuses over.
func bundleReaderRelease(ctx context.Context, lc *localcfg.Config) (rel, image, why string) {
	rel, image, deployed, why := servingCredentialsRelease(ctx, lc)
	switch {
	case why != "":
		return "", "", why
	case deployed:
		return rel, image, ""
	}
	pinned, stated := installationServiceImageOf(lc)
	if !stated {
		return "", "", ""
	}
	rel = infra.CredentialsServiceImageRelease(pinned)
	if rel == "" {
		return "", "", "this installation's terraform.tfvars.json names a credentials service image (" + pluginwire.Printable(pinned) + ") whose reference carries no release version"
	}
	return rel, pinned, ""
}

// servingCredentialsRelease is the deployed half of that answer: the release
// of the credentials service's revisions that SERVE the bundle, read through
// Cloud Run's v2 services and revisions calls (the client `fugaro doctor`'s
// credentials-service check builds for the same service).
//
// Which revision serves is the service's own answer and not a guess: an
// allocation to the latest revision — what the installation module writes,
// credentials.tf's traffic block — serves the latestReadyRevision it reports,
// and an entry naming one by name serves that one. The service's TEMPLATE is
// deliberately not the answer: the template is the NEXT revision, and Cloud
// Run keeps serving the previous healthy one when a roll-out fails to become
// ready or an operator pins traffic by hand, so a guard reading the template
// calls a service new whose serving revision is old. The oldest release among
// the serving revisions decides, so no revision that serves and cannot read
// the document slips past.
//
// deployed is false, with no why, for the one answer that needs no judging:
// Cloud Run holds no such service. Every other unreadable state — a read that
// fails for any reason but that one, a revision with no container image, a
// service with no revision serving — is a why, because an unknown release is
// exactly what this guard cannot clear.
func servingCredentialsRelease(ctx context.Context, lc *localcfg.Config) (rel, image string, deployed bool, why string) {
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return "", "", false, "the Cloud Run client could not be built (" + oneLineCLI(err.Error()) + ")"
	}
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", lc.GCPProject, lc.Region, infra.CredentialsServiceName)
	s, err := svc.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			// No service deployed: the installation's own pin is judged
			// instead, and "no service" is not "unknown".
			return "", "", false, ""
		}
		return "", "", false, "the read of the service that serves this bundle failed (" + oneLineCLI(err.Error()) + ")"
	}
	revs := servingRevisions(s)
	if len(revs) == 0 {
		return "", "", true, "the service reports no revision serving traffic, so the release that reads the bundle cannot be shown"
	}
	images := make([]string, 0, len(revs))
	for _, rev := range revs {
		r, err := svc.Projects.Locations.Services.Revisions.Get(rev).Context(ctx).Do()
		if err != nil {
			return "", "", true, "the revision that serves this bundle (" + safetext.Field(revisionShort(rev)) + ") could not be read (" + oneLineCLI(err.Error()) + ")"
		}
		if len(r.Containers) == 0 || r.Containers[0].Image == "" {
			return "", "", true, "the revision that serves this bundle (" + safetext.Field(revisionShort(rev)) + ") names no container image"
		}
		images = append(images, r.Containers[0].Image)
	}
	rel, image, ok := oldestCredentialsRelease(images)
	if !ok {
		return "", "", true, "the image of the revision that serves this bundle carries no release version"
	}
	return rel, image, true, ""
}

// servingRevisions is every revision of s that serves traffic, by the
// service's own answer: a target that allocates to the latest revision serves
// s.LatestReadyRevision and one that names a revision serves that one, and a
// target with no percent serves nothing. A service naming no target of its own
// serves every request with its latest ready revision, which is the same
// answer Cloud Run reads it as.
func servingRevisions(s *runapi.GoogleCloudRunV2Service) []string {
	var revs []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		revs = append(revs, name)
	}
	for _, t := range s.Traffic {
		if t.Percent <= 0 {
			continue
		}
		switch t.Type {
		case "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST":
			add(s.LatestReadyRevision)
		case "TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION":
			add(t.Revision)
		}
	}
	if len(revs) == 0 {
		add(s.LatestReadyRevision)
	}
	return revs
}

// oldestCredentialsRelease is the oldest release among the images of the
// revisions that serve, and the image it was read from. A reference that
// carries no release at all — an operator's hand-written digest, one resolved
// from another registry — makes the whole answer unknown rather than being
// skipped, because a revision that serves and whose release cannot be shown is
// one this guard cannot clear.
func oldestCredentialsRelease(images []string) (rel, image string, ok bool) {
	for _, img := range images {
		r := infra.CredentialsServiceImageRelease(img)
		if r == "" {
			return "", "", false
		}
		if rel == "" || imagePredates(r, rel) {
			rel, image = r, img
		}
	}
	return rel, image, rel != ""
}

// revisionShort is a revision's name as a person reads it: the last segment of
// the resource name Cloud Run answers with.
func revisionShort(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// bundleHoldsProviderEntry reports whether a bundle document holds the
// provider key's entry, which is one of the two the guard above judges.
func bundleHoldsProviderEntry(raw []byte) bool {
	return bundleHoldsKey(raw, func(key string) bool { return key == config.ProviderKeySecret })
}

// bundleHoldsFamilyEntry reports whether it holds any entry of the typed
// Bitbucket token family (T1): the guard is about the FAMILY and not about one
// name, because a document carrying bitbucket-token--ci is a document only a
// service that reads the family can load whichever token this write is setting.
func bundleHoldsFamilyEntry(raw []byte) bool {
	return bundleHoldsKey(raw, func(key string) bool {
		_, ok := config.BitbucketTokenAccount(key)
		return ok
	})
}

// bundleHoldsKeyCredentialEntry reports whether it holds any entry of the key
// credential family (MC23, D1670): the guard is about the family and not about
// one name, because a document carrying credential-team-a-key is a document
// only a service that reads the family can load whichever credential this write
// is setting.
func bundleHoldsKeyCredentialEntry(raw []byte) bool {
	return bundleHoldsKey(raw, func(key string) bool {
		_, ok := config.CredentialBySecret(key)
		return ok
	})
}

// bundleHoldsKey is the walk both of them are: the document's top-level keys,
// held to a rule the caller states.
//
// It is a node walk and not a parse with the loader, because the question is
// about ONE key and the loader answers about the whole document: the caller has
// already run credsvc.ParseBundle over this document and refused a document it
// rejects, so what is left is a lookup. An empty document, or one the walk
// cannot read at all (unreachable after that parse), holds no entry.
func bundleHoldsKey(raw []byte, want func(key string) bool) bool {
	_, root, err := bundleNodes(raw)
	if err != nil {
		return false
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if want(root.Content[i].Value) {
			return true
		}
	}
	return false
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
// and the Bitbucket read consumer and the provider credential are mappings of
// three fields, and the hidden prompt cannot take any of those (it reads one
// line), so those names come in on stdin and a terminal is refused before
// anything is read. The bound is the loader's per-credential one rather than
// Secret Manager's.
//
// The value never comes from an argument, a flag or the environment: this
// function's only inputs are stdin and the terminal. One trailing "\n" or
// "\r\n" is dropped and said, because a value pasted from a file almost always
// has one and silently keeping it would store a credential the service cannot
// use.
func readBundleValue(ctx context.Context, in io.Reader, prompt io.Writer, name string, kind credsvc.Kind) ([]byte, error) {
	var raw []byte
	// As readSecret's own (secrets.go): the hidden prompt needs the file to
	// take the echo off, so a stream a terminal was reported for and that is
	// not one is read to EOF like any pipe.
	if f, ok := in.(*os.File); ok && stdinIsTerminal(in) {
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
		case credsvc.KindProvider:
			return nil, userErr("%s is %s, which spans several lines and the hidden prompt can't take; redirect it from a file: fugaro creds set %s < PATH-TO-THE-FILE", name, providerShape, name)
		}
		b, err := readValueIn(ctx, f, prompt, valueSpec{what: name, secret: true})
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
	case credsvc.KindBitbucketReadConsumer, credsvc.KindProvider:
		// The two values that are not a line of text, so the one-line rules
		// below are not theirs. Their SHAPE is not checked here either: it is
		// readConsumerNode's and readProviderNode's, and each runs once, in
		// credsSet, where the node it answers is the node the write stores.
		// Checking them here as well would parse the same bytes a second time
		// for one write — the first tree, with every field's tag and style
		// rewritten in it, thrown away, and the bytes parsed again for the
		// tree that is stored.
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

// providerShape is what the provider key's value has to be, in the words the
// refusals below and the help text use, for readConsumerShape's reason: three
// of them say it, and an operator who reads one and then another should not
// have to work out whether the two are the same rule.
const providerShape = "a YAML mapping of exactly key, base_url and auth, one field per line"

// readProviderNode reads the provider key's value (config.ProviderKeySecret,
// AK6b), which is the bundle's second entry that is not a single string: a
// mapping of exactly key, base_url and auth, the credential and the two
// routing facts a call to that provider needs (credsvc's providerValue). It is
// read on stdin, from a file the operator wrote in that shape, and it is read
// as YAML so the three fields are named rather than positional.
//
// It is readConsumerNode's reading, and deliberately not its own second one:
// the fields are forced to strings (a base URL or an auth word that looks like
// a number is the text the operator pasted, and the loader would refuse the
// unquoted form), a field that is a list or a nested mapping is refused rather
// than stored, no refusal repeats a byte of a field's value, and the node it
// answers is the node the document gets, so these bytes are parsed once per
// write. What it does NOT do is judge the shapes of the two facts: the
// destination rule is the platform's (config.CheckProviderBaseURL) and the
// auth word is one of two, and both are credsvc's own answer, reached by the
// ParseBundle call the write makes before it stores anything. A second reading
// of them here is the drift providerValue's comment exists to prevent.
//
// The whole mapping is stored under this command's per-credential bound rather
// than the loader's per-FIELD one, exactly as the read consumer's is: one
// limit for every value is what the help text says.
func readProviderNode(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// The parse error is not wrapped: a YAML error quotes the document it
		// failed on, and that document is a credential.
		return nil, userErr("the value must be %s; nothing was written", providerShape)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) != 1 {
			return nil, userErr("the value must be %s; nothing was written", providerShape)
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, userErr("the value must be %s; nothing was written", providerShape)
	}
	seen := make(map[string]bool, 3)
	for i := 0; i+1 < len(root.Content); i += 2 {
		f, fv := root.Content[i], root.Content[i+1]
		switch f.Value {
		case "key", "base_url", "auth":
		default:
			return nil, userErr("the value must be %s, and it holds a field that is not one of the three; nothing was written", providerShape)
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
	if !seen["key"] || !seen["base_url"] || !seen["auth"] {
		return nil, userErr("the value must be %s; nothing was written", providerShape)
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
// the entry holds: the read consumer's mapping and the provider key's
// (readConsumerNode, readProviderNode), the two entries of the bundle that are
// not one line of text.
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
	bb, err := bitbucketTokenRows(ctx, lc, b)
	if err != nil {
		return userErr("nothing was printed: the Bitbucket tokens' status could not be read from project %s's credential state, and a listing that showed a token without it would read as a token nothing is recorded about: %v", safetext.Field(lc.Name), err)
	}
	return printCreds(cmd.OutOrStdout(), lc.Name, store.id, doc, b, meta, bb, o.asJSON)
}

// bitbucketBundleTokens is every Bitbucket credential the document holds, each
// under its DOCUMENT KEY (R5): the legacy key when the document carries it, and
// then the family's named tokens.
//
// The two halves are joined here and nowhere else, because the legacy key is
// deliberately not among BitbucketTokens() — it is R3's implicit untyped record
// and not a member with an empty name — while "the bundle's Bitbucket tokens"
// is one question everywhere else it is asked.
func bitbucketBundleTokens(b *credsvc.Bundle) []credsvc.Entry {
	out := make([]credsvc.Entry, 0, len(b.BitbucketTokens())+1)
	if legacy, held := b.BitbucketToken(); held {
		out = append(out, legacy)
	}
	return append(out, b.BitbucketTokens()...)
}

// bitbucketTokenRecords are the operator's typed token records by the bundle
// KEY each is spoken of by (R5): the key the document holds the token under,
// which is also the key the state store writes its status under (T6).
func bitbucketTokenRecords(lc *localcfg.Config) map[string]config.BitbucketToken {
	out := make(map[string]config.BitbucketToken, len(lc.BitbucketTokens))
	for _, rec := range lc.BitbucketTokens {
		if key := config.BitbucketTokenKey(rec.Name); key != "" {
			out[key] = rec
		}
	}
	return out
}

// bitbucketTokenRows is the Bitbucket half of every row the document's keys
// name: what the operator recorded per token, and the status the installation's
// credential state store holds for it (T6).
//
// A document holding no Bitbucket token at all is answered with nothing and
// asks the store nothing: the state is read for the rows that carry it, and the
// listing of an installation that holds none is what it always was.
//
// The second return of bitbucketTokenStatuses says whether there was a store to
// read at all, and it is not the same answer as "nothing is recorded": a
// project with no Firebase backend cannot hold a status for a token — the
// service writes into that database and an installation without one has no
// service — so its rows carry the record's facts and no state, while a store
// that answered carries the state of every token, the zero status included.
func bitbucketTokenRows(ctx context.Context, lc *localcfg.Config, b *credsvc.Bundle) (map[string]bitbucketRow, error) {
	tokens := bitbucketBundleTokens(b)
	if len(tokens) == 0 {
		return nil, nil
	}
	statuses, answered, err := bitbucketTokenStatuses(ctx, lc)
	if err != nil {
		return nil, err
	}
	records := bitbucketTokenRecords(lc)
	out := make(map[string]bitbucketRow, len(tokens))
	for _, e := range tokens {
		out[e.Key] = bitbucketRowFor(lc, records, statuses, answered, e.Key)
	}
	return out, nil
}

// bitbucketTokenStatuses is every stored Bitbucket token's status, read from
// the project's own credential state store (creds/tokens, T6). The first return
// is nil when there is no store to read, and the second is false there: a
// project whose config records no Firebase backend has nowhere a status could
// have been written.
//
// The read is the operator's, as the person (ADC), like `fugaro accounts ls`'s
// read of the model marks: what the service writes is what an operator is meant
// to see. A store that IS there and did not answer is the caller's error rather
// than a missing field, because the subtree read is all-or-nothing — one node
// this release cannot decode fails it for every token — and a listing that
// dropped the state over that would print tokens nothing is recorded about
// while the store holds something it could not read.
func bitbucketTokenStatuses(ctx context.Context, lc *localcfg.Config) (map[string]creds.TokenStatus, bool, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, false, nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, true, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, true, err
	}
	statuses, err := (&creds.RTDBStore{DB: db}).TokenStatuses(ctx)
	if err != nil {
		return nil, true, err
	}
	return statuses, true, nil
}

// bitbucketRowFor is one token's half of a row, from the operator's record for
// it and the status the store answered.
//
// A family key the config records no token for carries no record's fields: a
// token has to be in both documents to serve anything (R14), so the entry alone
// reaches nothing and the row says only what the store holds about it.
func bitbucketRowFor(lc *localcfg.Config, records map[string]config.BitbucketToken, statuses map[string]creds.TokenStatus, answered bool, key string) bitbucketRow {
	var out bitbucketRow
	switch rec, recorded := records[key]; {
	case key == config.BitbucketSecret:
		// R3: the legacy key is the implicit UNTYPED record — the one word this
		// listing has for a token no record types — and what it reaches is the
		// operator's allow_data_to list, which is the allow_repos the published
		// node carries. R6: its expiry is the secret_expires entry keyed by the
		// same name.
		out.Type = bitbucketUntypedType
		out.Coverage = lc.ProjectSecrets[config.BitbucketSecret].AllowDataTo
		out.Expires = lc.SecretExpires[config.BitbucketSecret]
	case recorded:
		out.Type = rec.Type
		out.Coverage = rec.Coverage
		out.Expires = rec.Expires
	}
	if !answered {
		return out
	}
	st := statuses[key]
	out.State = string(cmp.Or(st.State, creds.StateAvailable))
	if !st.VerifiedAt.IsZero() {
		out.VerifiedAt = st.VerifiedAt.UTC().Format(time.RFC3339)
	}
	// The hold, for the two states that have one: a broken token waits for the
	// operator rather than for its clock (C1.21), and Break writes no Until.
	if st.State == creds.StateExhausted || st.State == creds.StateBackedOff {
		if !st.Until.IsZero() {
			out.ThrottledUntil = st.Until.UTC().Format(time.RFC3339)
		}
	}
	count := st.Count
	out.RequestsInWindow = &count
	return out
}

// bitbucketUntypedType is the type this listing shows for the legacy key: the
// word the plan's own record shape has for a token no record types (R3), and
// not the empty string the contract's Type carries for it — a JSON field that
// is absent and a token that is untyped are two different statements, and only
// the second is true of a token the bundle holds.
const bitbucketUntypedType = "untyped"

// credsRow is one row of `creds ls`: a credential the document holds, its
// presence and the document version's time. There is no field here a value
// could travel in, which is the point of the row being its own type rather
// than credsvc.Entry (whose value is unexported and never rendered, but which
// is not something to hand a printer).
type credsRow struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Changed string `json:"changed"`
	bitbucketRow
}

// bitbucketRow is the half of a row only a Bitbucket token has (T10, R7): what
// the operator recorded for it (T2) and what the installation's credential
// state store holds about it (T6).
//
// Every field is a string, an integer or a list of the operator's own coverage
// patterns, so a value has nowhere to travel in this half either. Every field
// is omitempty, and that is what R7's "no existing field changes meaning" is
// read as here: a row of any other credential marshals exactly the three keys
// it always did, and a Bitbucket token carries the keys that are true of it and
// no others — a token with no recorded hold has no `throttled_until`, because
// an empty one would read as a hold that ended.
type bitbucketRow struct {
	// Type and Coverage are the record's (R1's type, and the coverage in the
	// operator's own spelling). The legacy key has no record: R3 makes it the
	// implicit untyped token, so its coverage is the allow_data_to list the
	// published node carries and its type is the one word for it.
	Type     string   `json:"type,omitempty"`
	Coverage []string `json:"coverage,omitempty"`
	// Expires is the day the operator recorded for the token, in the config's
	// own spelling (localcfg.SecretExpiryLayout for the legacy key's
	// secret_expires entry, the record's own expires otherwise).
	Expires string `json:"expires,omitempty"`
	// VerifiedAt is when the add-time check last proved the token (R12), and
	// State is the store's own word for what it holds now.
	VerifiedAt string `json:"verified_at,omitempty"`
	State      string `json:"state,omitempty"`
	// ThrottledUntil is when a hold ends, for the two states that have one
	// (R7's first new field). RequestsInWindow is how many requests the token
	// served in the window the store keeps (R7's second, D2356): a pointer, so
	// that a token the store answered for and that served none reads as the
	// zero it is rather than as an absent field.
	ThrottledUntil   string `json:"throttled_until,omitempty"`
	RequestsInWindow *int   `json:"requests_in_window,omitempty"`
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
// KEYS (never a value), from the container's version metadata, from the
// operator's own records and from the credential state store; the values
// themselves are parsed and then dropped, which is what makes "never a value,
// never a prefix, never a length" a property of the shape rather than of a
// redaction.
//
// bb is the Bitbucket half, by document key, and nil for a document that holds
// no Bitbucket token (bitbucketTokenRows). It reaches the JSON rows and the
// lines under the text table, and it is where a token's type, its coverage, its
// expiry and its state are read from.
func printCreds(w io.Writer, project, id string, doc bundleDoc, b *credsvc.Bundle, meta versionMeta, bb map[string]bitbucketRow, asJSON bool) error {
	changed := meta.created.UTC().Format(time.RFC3339)
	rows := make([]credsRow, 0, b.Len())
	for _, e := range b.Entries() {
		rows = append(rows, credsRow{Name: e.Key, Present: true, Changed: changed, bitbucketRow: bb[e.Key]})
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
	if err := tw.Flush(); err != nil {
		return err
	}
	return printBitbucketTokenLines(w, rows, bb)
}

// printBitbucketTokenLines says, under the table, what a Bitbucket token's row
// cannot: a name and a PRESENT answer are the same for a token that serves
// every call of its repository and for one Bitbucket refuses, and a listing
// that left that to --json would make an operator read JSON to learn whether
// their git proxy works.
//
// One line per token the document holds under a Bitbucket key, and none for a
// document that holds none: the facts are the record's own (type, coverage,
// expiry) and the store's (the state, when the token was last verified, the
// hold and the count of R7), and never a value.
func printBitbucketTokenLines(w io.Writer, rows []credsRow, bb map[string]bitbucketRow) error {
	for _, r := range rows {
		if _, held := bb[r.Name]; !held {
			continue
		}
		var parts []string
		for _, p := range []struct{ label, value string }{
			{"type", r.Type},
			{"coverage", strings.Join(r.Coverage, " ")},
			{"expires", r.Expires},
			{"verified", r.VerifiedAt},
			{"state", r.State},
			{"throttled until", r.ThrottledUntil},
		} {
			if p.value != "" {
				parts = append(parts, p.label+" "+safetext.Field(p.value))
			}
		}
		if r.RequestsInWindow != nil {
			parts = append(parts, fmt.Sprintf("%d request(s) in the window", *r.RequestsInWindow))
		}
		if len(parts) == 0 {
			// A family key with no record and no status: it serves nothing,
			// which is worth saying rather than leaving a bare name.
			parts = append(parts, "no record and no status: a token has to be in both documents to serve anything")
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", safetext.Field(r.Name), strings.Join(parts, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func presentCell(present bool) string {
	if present {
		return "yes"
	}
	return "no"
}

// repoCredentialFamily is how the guard spells C1's family when the entry it
// judges is the family itself rather than one member of it: the same way
// bitbucket-token--<name> and credential-<name>-key are spelled for their own
// families, because the guard is about the reader a DOCUMENT needs and not
// about the entry a write sets (bundleHoldsRepoCredentialEntry).
const repoCredentialFamily = credsvc.RepoCredentialPrefix + "<slug>--<credential>"

// The two auth shapes one of C1's entries may hold, spelled here as the
// loader spells them (credsvc's providerAuthBearer and providerAuthXAPIKey,
// which are not exported) and as the config writes them in providers.<p>.auth.
// They are the vocabulary the three readers share — the config's own
// validation, this writer, and the loader/gateway pair — and a third shape is
// refused rather than written: the gateway has no way to send it.
const (
	providerAuthBearer  = "bearer"
	providerAuthXAPIKey = "x-api-key"
)

// bundleHoldsRepoCredentialEntry reports whether a bundle document holds any
// entry of C1's named-provider family (R2, D3008), the fourth family the guard
// judges: a document carrying repo-credential--acme-web-0123456789abcdef--or is
// a document only a credentials service that reads the family can load,
// whichever credential this write is setting.
//
// The family and not one member, for bundleHoldsFamilyEntry's reason: what a
// service needs is the reader of the family, and a document holding a member
// the deployed service cannot read halts every caller of it.
func bundleHoldsRepoCredentialEntry(raw []byte) bool {
	return bundleHoldsKey(raw, func(key string) bool {
		_, _, ok := credsvc.RepoCredentialByKey(key)
		return ok
	})
}

// providerKeyCredentialRepos is the repositories a runner-path key
// credential's key may be sent to and so the repositories its entries are
// written for: claude_accounts.<credential>.allow_data_to, the one field that
// bounds the key (internal/infra's runnerKeyCredential reads the same field for
// the mount it builds, so the entry and the mount cannot disagree about which
// repository the key reaches).
//
// A list that names no repository is not "every repository" and is not an empty
// write: it is a credential nothing may spend, answered by
// providerKeyNoReposRefusal. There is no --repo narrowing this list — the field
// is the reach, and a key written for a repository the field does not name is a
// key handed to a repository the owner's own config excludes.
func providerKeyCredentialRepos(lc *localcfg.Config, credential string) []string {
	return slices.Clone(lc.ClaudeAccounts[credential].AllowDataTo)
}

// providerKeyName reports whether name is a provider key's own spelling in
// this project: the provider's own secret (providers.<p>.secret, whatever it is
// called) or a runner-path key credential's derived name
// (credential-<name>-key). Those two are the names whose store writes C1's
// family and no container (KS2), which is what a caller needs to know before it
// describes the store in the words of the project-copy/repository-copy
// precedence — a precedence a provider key does not have.
//
// The provider's CONVENTIONAL name (config.ReservedProviderSecret) is not one:
// it is no provider's .secret, it has no entry to be written under, and every
// door that could write it refuses it.
func providerKeyName(lc *localcfg.Config, name string) bool {
	if _, isProvider := config.ProviderBySecret(lc.Providers, name); isProvider {
		return true
	}
	_, isCredential := providerKeySecretCredential(lc, name)
	return isCredential
}

// providerKeySecretCredential is the credential a name is a RUNNER-path key
// credential's own derived secret for (`credential-<name>-key`), which is the
// second spelling a provider key reaches `fugaro secrets set` under. The
// credential of that name is a provider key, and its name IS the provider's
// (infra's runnerKeyCredential reads the same pair for the mount it used to
// build), so the entry's name and the provider's facts are both decided by it.
//
// A service-served credential is not one: its container is the copy the
// credentials service's bundle entry is completed by, written by the same
// command, and KS2 left that path alone.
func providerKeySecretCredential(lc *localcfg.Config, secret string) (string, bool) {
	name, ok := keyCredentialBySecret(lc, secret)
	if !ok {
		return "", false
	}
	cred, declared := config.CredentialNamed(lc.Credentials, name)
	if !declared || config.CredentialServingPath(cred.Type) != config.CredentialPathRunner {
		return "", false
	}
	return name, true
}

// providerKeyProviderRepos is the same for a provider's own secret
// (providers.<p>.secret) stored project-wide: the repositories
// providers.<p>.allow_data_to names literally.
//
// An absent list, or one that is only "*", is "every repository" (design
// docs/project-secrets/spec.md §3a) and answers nil: the entries are named
// after each repository's slug, and a wildcard names none, so there is nothing
// to write. The refusal says which field to write, because the value IS
// storable the moment the field names its repositories.
func providerKeyProviderRepos(lc *localcfg.Config, provider string) []string {
	p := lc.Providers[provider]
	if p.OpenToEveryRepo() {
		return nil
	}
	return slices.Clone(p.AllowDataTo)
}

// providerKeyNoReposRefusal is the state a provider-key write cannot serve: the
// list that bounds the key names no repository, so there is no entry to write
// and no repository to write one for.
//
// It is the one refusal of this family that is about the config and not about
// the value or the service, and it names the field, never a value: an absent
// providers.<p>.allow_data_to means every repository, which is the one shape a
// per-repository entry cannot be built from.
func providerKeyNoReposRefusal(lc *localcfg.Config, provider string) error {
	if p, ok := lc.Providers[provider]; ok && p.OpenToEveryRepo() {
		return userErr("providers.%s.allow_data_to is absent or [\"*\"], which means EVERY repository, and a provider key is entered in the credentials service's bundle once per repository (D3008): the entry's name carries the repository, so a key open to every repository has no entry to write. Name the repositories the key is for — providers.%s.allow_data_to = [owner/name, ...] — and store it again; nothing was read from you and nothing was written",
			pluginwire.Printable(provider), pluginwire.Printable(provider))
	}
	return userErr("claude_accounts.%s.allow_data_to names no repository, so %s's key may be sent to no repository and there is no entry to write: it is the one field that bounds the key, and the bundle's entry is written for each repository it names. Add the repositories the key is for — claude_accounts.%s.allow_data_to = [owner/name, ...] — and store it again; nothing was read from you and nothing was written",
		pluginwire.Printable(provider), pluginwire.Printable(provider), pluginwire.Printable(provider))
}

// providerKeyInitRefusal is the fourth writer's refusal (initsecrets.go): the
// secrets stage of `fugaro setup` and `fugaro update` prompts at a terminal and
// writes a container of the name it was handed, which is neither of the two
// things a provider key's write is now — a bundle entry, read from stdin. It
// names the command that writes this repository's own entry, because the bare
// form Left() hands the same name stores the project's copy instead.
func providerKeyInitRefusal(provider, name, repo string) error {
	return userErr("%s is %s's key, and a provider key is entered in the credentials service's bundle since KS2 (D1670 reversed), one entry per repository, rather than stored in a container of its own: this step stores a value in Secret Manager and cannot write that entry. Nothing was read from you and nothing was written. Run `%s secrets set %s --repo %s` in your own terminal to enter the key for this repository, and run it once more for each further repository the key is for",
		pluginwire.Printable(name), pluginwire.Printable(provider), selfCommand(), pluginwire.Printable(name), pluginwire.Printable(repo))
}

// providerKeyRepoSlug is one repository's slug, the half of C1's entry name
// that carries the repository: task.Slug over the repository's GIT provider, as
// every other slug this tree derives is (internal/cli/secrets.go's own
// repoCopiesWithVersions reads the same pair off the same config).
//
// The git provider comes from the local config's repositories entry and from
// nothing else: a checkout is not read here, because the repositories a key is
// written for are the ones the config names and matter from wherever the
// command runs.
func providerKeyRepoSlug(lc *localcfg.Config, repo string) (string, error) {
	r, listed := lc.Repos[repo]
	if !listed {
		return "", userErr("%s is not a repository this project's config lists, so C1's entry name cannot carry its slug: the repositories a provider key is written for are repositories.<owner/name> entries of the local config (an allow_data_to that names a repository the config does not list is a repository this command cannot name an entry for)",
			pluginwire.Printable(repo))
	}
	slug, err := task.Slug(r.Provider, repo)
	if err != nil {
		return "", userErr("%s: %v; nothing was written", pluginwire.Printable(repo), err)
	}
	return slug, nil
}

// providerKeyEntry is one repository's entry name in C1's family, built by
// C1's own rule (credsvc.RepoCredentialKey) so the writer and the loader the
// service reads with cannot disagree about which key is which entry.
func providerKeyEntry(lc *localcfg.Config, provider, repo string) (string, error) {
	slug, err := providerKeyRepoSlug(lc, repo)
	if err != nil {
		return "", err
	}
	key, err := credsvc.RepoCredentialKey(slug, provider)
	if err != nil {
		return "", userErr("%s: %v; nothing was written", pluginwire.Printable(repo), err)
	}
	return key, nil
}

// providerKeyValueNode is the value one entry of the family holds: the mapping
// of key, base_url and auth the loader reads (credsvc's providerFactsFrom), as
// YAML nodes.
//
// Two of the three fields are NOT the operator's: where the call goes and how
// the key is sent are the provider's own routing facts, read from
// providers.<provider> in the project's config, and the entry exists so that a
// run is never told either (F45, D1418 — the destination is the entry's and
// never a caller's). The key is the value that was read.
//
// The two facts are held to the loader's own rules before they are written —
// config.CheckProviderBaseURL and the closed set of two auth shapes — so a
// config whose provider entry this document cannot carry is refused here, in a
// sentence about the config, rather than by the loader as a document the
// service refuses.
func providerKeyValueNode(lc *localcfg.Config, provider string, value []byte) (*yaml.Node, error) {
	baseURL, auth, err := providerKeyFacts(lc, provider)
	if err != nil {
		return nil, err
	}
	fields := []struct {
		name  string
		value string
	}{
		{"key", string(value)},
		{"base_url", baseURL},
		{"auth", auth},
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, f := range fields {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.name},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.value, Style: yaml.DoubleQuotedStyle})
	}
	return root, nil
}

// providerKeyFacts is the provider's own two routing facts — the address a
// routed call goes to and the shape of the header the key rides in — held to
// the rules the service's loader holds them to before they are written.
//
// It is a step of its own and not the node builder's body because the writers
// ask for it before they read a value: a config the entry cannot be built from
// is refused with nothing typed at a terminal, which is the rule every refusal
// of this family keeps.
func providerKeyFacts(lc *localcfg.Config, provider string) (string, string, error) {
	p, declared := lc.Providers[provider]
	if !declared {
		return "", "", userErr("providers.%s is not declared in this project's config, and C1's entry carries the provider's OWN address and auth — the destination of a routed call is the entry's and never the caller's (F45, D1418) — so there is nothing to build the entry from. Declare the provider first (fugaro models add %s, or the providers block of the project config) with its kind, base_url, auth and models, then store the key; nothing was read from you and nothing was written",
			pluginwire.Printable(provider), pluginwire.Printable(provider))
	}
	if err := config.CheckProviderBaseURL(p.BaseURL); err != nil {
		return "", "", userErr("providers.%s.base_url is not one of C1's entries may hold (%v): the entry's base_url is the destination of every routed call spent on this key, and the loader and the gateway's routes hold it to the same rule. Fix providers.%s in the project config and store the key again; nothing was read from you and nothing was written",
			pluginwire.Printable(provider), err, pluginwire.Printable(provider))
	}
	if p.Auth != providerAuthBearer && p.Auth != providerAuthXAPIKey {
		return "", "", userErr("providers.%s.auth is %s, and one of C1's entries may hold only the two shapes the gateway can send: %s or %s. Fix providers.%s in the project config and store the key again; nothing was read from you and nothing was written",
			pluginwire.Printable(provider), pluginwire.Printable(p.Auth), providerAuthBearer, providerAuthXAPIKey, pluginwire.Printable(provider))
	}
	return p.BaseURL, p.Auth, nil
}

// providerKeyBundleDoc is the document a provider-key write leaves in place:
// the document as it is with one entry of C1's family set for each repository
// named, all of them in ONE version.
//
// One version and not one write per repository: a key several repositories
// share is entered once for each (D3008), and a version holding it for some of
// them is a state in which the key serves a run on some repositories and not on
// others, which is a rotation half-applied. The read-modify-write is credsSet's
// own and edits YAML nodes rather than rebuilding the document from a map, so
// every entry this write was not asked about keeps its bytes.
//
// The value's nodes are built once and reused for every entry: the fields are
// the same three values, and a copy per entry would be a second place the
// provider's own address could be read from.
func providerKeyBundleDoc(raw []byte, lc *localcfg.Config, provider string, repos []string, value []byte) ([]byte, error) {
	doc, root, err := bundleNodes(raw)
	if err != nil {
		return nil, err
	}
	valueNode, err := providerKeyValueNode(lc, provider, value)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		entry, err := providerKeyEntry(lc, provider, repo)
		if err != nil {
			return nil, err
		}
		setBundleNodeValue(root, entry, valueNode)
	}
	return marshalBundle(doc)
}

// providerKeyBundleBefore is the read half of a provider-key write: the
// document as it is, with credsSet's own three answers — a container with no
// version is the first write's starting point, a DISABLED newest version is
// refused as such, a document the service's loader rejects is refused before
// anything is written — so a provider key can never replace a document the
// service could have read.
func providerKeyBundleBefore(ctx context.Context, store *bundleStore) (bundleDoc, error) {
	before, err := store.access(ctx, "latest")
	switch {
	case errors.Is(err, errNoBundleVersion):
		return bundleDoc{}, nil
	case errors.Is(err, errBundleDisabled):
		return bundleDoc{}, credsDisabledRefusal(store.id, "written")
	case err != nil:
		return bundleDoc{}, err
	}
	if _, err := credsvc.ParseBundle(before.raw); err != nil {
		return bundleDoc{}, credsDocRefusal(store.id, err)
	}
	return before, nil
}

// providerKeyBundleWrite is the ONE write of a provider's key into the bundle
// (F27, R2, KS2): the guard for a deployed service that cannot read the
// family, the document, the service's own loader over it, and the add-verify-
// retire tail every write of this document shares. It is called by all three
// writers — `fugaro creds set credential-<name>-key`, `fugaro accounts add
// --type openrouter` and `fugaro secrets set` of a provider's secret — so that
// the four steps have one version and not three.
//
// The guard comes first, before the document is built: it reads the release of
// the credentials service that will read this bundle, and a write refused there
// is refused with nothing read from a person and nothing written. It is the
// same guard every other named entry goes through (F22) and it is about the
// FAMILY, because the loader refuses a document holding a key it does not know
// rather than that entry.
func providerKeyBundleWrite(ctx context.Context, cmd *cobra.Command, st *report.Step, store *bundleStore, before bundleDoc, lc *localcfg.Config, provider string, repos []string, value []byte) (bundleAddResult, error) {
	if err := refuseAnEntryTheServiceCannotRead(ctx, lc, repoCredentialFamily, repoCredentialSince); err != nil {
		return bundleAddResult{}, err
	}
	doc, err := providerKeyBundleDoc(before.raw, lc, provider, repos, value)
	if err != nil {
		return bundleAddResult{}, err
	}
	if _, err := credsvc.ParseBundle(doc); err != nil {
		return bundleAddResult{}, credsDocRefusal(store.id, err)
	}
	return bundleAddVerified(ctx, cmd, st, store, before, doc, value)
}

// providerKeyBundleStore is the whole of one provider-key store as a command
// reaches it: who may make it, the container, the document, the value read from
// stdin and the version written. It is `fugaro secrets set`'s and `fugaro
// accounts add`'s path to providerKeyBundleWrite — `fugaro creds set` has all of
// these already and calls the write directly.
//
// The order is the one every write of this document keeps: the person, before
// anything is read; the container, so a project with no bundle is answered with
// the apply that creates it; the project-owner probe (D3006, F36) — the ONE
// probe of that question — before the document is fetched and before the value
// is read; the provider's own two facts, so a config the entry cannot be built
// from is refused with nothing typed; then the read, the write and the version.
//
// prompt is the name the person is answering for — the secret they typed, which
// is not always the credential's own derived name (`secrets set <provider>-api-key`
// is a provider key's other spelling) and is the name a hidden prompt would
// show. credential is the credential the entry is named after, which is the
// provider's name (a runner-path key credential is named after the provider it
// serves).
//
// asJSON is the caller's --json: the document printed is `secrets set --json`'s
// shape with the BUNDLE's container in it, because the bundle is the container
// this write added a version to — no version of a repository's or a provider's
// own container is written for a provider key any more.
func providerKeyBundleStore(run *stepRun, cmd *cobra.Command, st *report.Step, lc *localcfg.Config, credential, prompt string, repos []string, asJSON bool) (string, string, error) {
	ctx := cmd.Context()
	if err := providerKeyOwnerOnly(os.Getenv, "stored"); err != nil {
		return "", "", err
	}
	if len(repos) == 0 {
		return "", "", providerKeyNoReposRefusal(lc, credential)
	}
	if _, _, err := providerKeyFacts(lc, credential); err != nil {
		return "", "", err
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return "", "", err
	}
	if err := store.check(ctx); err != nil {
		return "", "", remoteErr(err, nil)
	}
	if err := bundleOwnerProbe(ctx, store, lc, "stored"); err != nil {
		return "", "", err
	}
	before, err := providerKeyBundleBefore(ctx, store)
	if err != nil {
		return "", "", remoteErr(err, nil)
	}
	value, err := readStoreValue(run, cmd, prompt)
	if err != nil {
		return "", "", err
	}
	defer clear(value)
	res, err := providerKeyBundleWrite(ctx, cmd, st, store, before, lc, credential, repos, value)
	if err != nil {
		return "", "", err
	}
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]string{"project": lc.Name, "secret": store.id, "version": res.version}); err != nil {
			return "", "", err
		}
	}
	return store.id, res.version, nil
}

// providerKeyWrittenLine is what a provider-key write says about the entries it
// left, for the stores whose output a person reads: the repositories the key is
// now entered for, one entry each, in the version just written.
//
// It is a line of its own and not decoration: a provider key's write is the one
// store whose reach is a LIST (R2, D3008), and the person who stored one value
// has to be able to read which repositories it now serves without opening the
// bundle — the entries are named after slugs they did not type.
func providerKeyWrittenLine(lc *localcfg.Config, provider string, repos []string, version string) string {
	entries := make([]string, 0, len(repos))
	for _, repo := range repos {
		key, err := providerKeyEntry(lc, provider, repo)
		if err != nil {
			continue
		}
		entries = append(entries, key)
	}
	return fmt.Sprintf("note: %s's key is entered in the credentials service's bundle at version %s, one entry per repository: %s\n",
		pluginwire.Printable(provider), safetext.Field(version), safetext.Field(strings.Join(entries, ", ")))
}
