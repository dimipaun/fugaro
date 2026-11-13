// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// `fugaro licence` (plan K3.2): the installation's key, what it is, and a
// manual renewal. The whole command is the Subscription Edition's — an
// untagged build has no licence command at all, not even a stub (F6, D524) —
// and nothing in the core reads a key.
//
// Three properties hold across every verb here, and they are the reason this
// file reads the way it does:
//
//   - The key never reaches an argument, an environment variable, a log line
//     or any line of output (F5, F41). `set` reads it from standard input or
//     --file, a positional argument is refused without being echoed, and every
//     byte a server or a bucket hands back is redacted before it is shown.
//   - Every write is refused inside a coding agent's session, before anything
//     is read, exactly as every other cloud write is (fcli.AgentSessionRefusal).
//     `status` is a read and is not refused: a person has to be able to see
//     what their installation holds from wherever they are.
//   - The licence belongs to the installation and not to the machine (D2137):
//     `set` writes the project's object in the runs bucket, `refresh` renews
//     that key, and `status` says which store answered when a per-machine key
//     is the one that applies.
//
// This file owns the subcommand table so that the verbs the other licence
// tasks bring (K3.3's login, K3.4's seats, K4.1's renew) are added from their
// own files with addLicenceSubcommand, and cli.RegisterCommand — which is
// top-level only — is called exactly once, here.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// licenceRenewalSecretLogical is the logical name of the Secret Manager
// container holding the installation's per-key renewal secret. It is derived
// into an ID by gcp.ProjectSecretID, the same way every other installation-wide
// container is, and K4.2's Terraform creates that container: this command only
// writes a version into one that is already there, so `fugaro update` never
// meets a secret it did not create. The literal is the coupling between the two
// tasks — a rename on either side has to move the other — and
// TestLicenceRenewalSecretIDIsTheInstallationsContainer pins the derived ID so
// that a rename here fails a test rather than leaving `set` writing into a
// container nobody reads.
const licenceRenewalSecretLogical = "licence-renewal"

// licenceMaxBundleBytes bounds what `set` reads: a key is about a kilobyte and
// a renewal secret is 32 bytes base64url, so this is far more than either and
// small enough that a pipe of something else is refused rather than read.
const licenceMaxBundleBytes = 8 << 10

// licenceRenewTimeout bounds one call to the licence server. A renewal is a
// person waiting at a terminal, and the server's own answer is small; a call
// that has not answered in this long has failed, and the key it would have
// renewed is unchanged by the attempt.
const licenceRenewTimeout = 60 * time.Second

// licenceMaxAnswerBytes bounds what the licence server's answer may be. The
// answer is a key and a secret at the most.
const licenceMaxAnswerBytes = 16 << 10

// licenceServerURL is the key-signing service's address (docs/design/
// key-signing-service.md: the site's own host's keys. subdomain).
var licenceServerURL = "https://keys.fugaro.dev"

// licenceRing is the keyring these commands verify a key against. It is empty
// here and stays empty until S2.9 publishes the owner's production public keys
// in ee/licence's own table, which is where the halves belong: a key nothing in
// the ring can verify is refused by `set` rather than installed, and `status`
// answers the refusal rather than reading it as Free.
//
// **Until then no key is accepted by a released build**, and that is the honest
// answer rather than a gap: the production ring is empty today (ee/licence's
// productionKeyring), so there is nothing a key could be verified against. S2.9
// has one line to add here — this variable has to become that same ring, which
// is what keeps one build's trusted keys one list — and K7.3, which renames the
// two verifiers into one, is the other task that moves it. It is a variable so
// that a test can install the ring of a keypair it minted at run time.
var licenceRing = licence.Keyring{}

// licenceNow is the clock: the state a key is in is a fact about an instant,
// and a test that mints a key with a term of its own decides which instant.
var licenceNow = time.Now

// licenceSubcommands is the table of `fugaro licence` verbs. It is filled by
// init in this package's licence files — one addLicenceSubcommand call per
// verb — and read when the command tree is built, which happens after every
// package init, so a verb a later task adds from its own file lands in the
// tree without this file changing.
type licenceSubcommand struct {
	name string
	new  func() *cobra.Command
}

var licenceSubcommands []licenceSubcommand

// addLicenceSubcommand adds one verb of `fugaro licence`. It panics on a
// duplicate name, because two files claiming one verb is a mistake that would
// otherwise be a tree where cobra answers the first and the second is
// unreachable.
func addLicenceSubcommand(name string, newCmd func() *cobra.Command) {
	for _, sub := range licenceSubcommands {
		if sub.name == name {
			panic("ee/register: licence subcommand " + name + " is added twice")
		}
	}
	licenceSubcommands = append(licenceSubcommands, licenceSubcommand{name: name, new: newCmd})
}

func init() {
	addLicenceSubcommand("set", newLicenceSetCmd)
	addLicenceSubcommand("status", newLicenceStatusCmd)
	addLicenceSubcommand("refresh", newLicenceRefreshCmd)
	if err := fcli.RegisterCommand("licence", newLicenceCmd); err != nil {
		panic("ee/register: " + err.Error())
	}
}

// newLicenceCmd is `fugaro licence`. It builds its children from the table
// every time it is called, which is what lets a verb land in the tree at
// registration time and again when the tree is actually built.
//
// `license` is an alias and not a second command: the specification spells the
// command `fugaro license set <key>` (spec section 11.2) and the decision that
// answered F5 keeps the code's spelling as the command while accepting the
// spec's. Cobra has one mechanism for that and it is an alias, whose one
// difference from a hidden command is that cobra prints it in this command's
// help as `Aliases: licence, license`. It is not a command of its own, it takes
// no section of its own in the CLI reference, and it is not a paid name.
func newLicenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "licence",
		Aliases: []string{"license"},
		Short:   "Install, read and renew the installation's licence key (Subscription Edition)",
		Long: `Install, read and renew the installation's licence key.

The key belongs to the installation and not to one machine: it is written to the
project's own store (the runs bucket), every member reads it there, and the
per-machine key file is what a console hands a child it spawns. The commands
without a coordinator renew by hand.

  fugaro licence set       install a key you were given
  fugaro licence status    show the tier, the seats, the expiry and the last check
  fugaro licence refresh   ask the licence server for the next key now

` + "`fugaro update`" + ` is the whole upgrade: the installation's secret container and the
job that renews a self-renewing key are Terraform's, and this command writes no
part of them.`,
		// Set so cobra doesn't answer an unknown subcommand by quoting it:
		// `fugaro licence <key>` must not echo the key. The argument position
		// is one a pasted key lands in.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return licenceUnknownSubcommand()
		},
	}
	// pflag quotes an unknown flag, and a pasted key could be one
	// (`-fgk_…`); every verb below inherits this.
	cmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return fcli.UserErr("unknown or malformed flag; licence commands take --file, --json, --config, --project, --gcp-project and --region, and never the key")
	})
	for _, sub := range licenceSubcommands {
		cmd.AddCommand(sub.new())
	}
	return cmd
}

// licenceUnknownSubcommand refuses a verb this build does not have without
// repeating the argument: it names the verbs there are and nothing the caller
// typed.
func licenceUnknownSubcommand() error {
	names := make([]string, 0, len(licenceSubcommands))
	for _, sub := range licenceSubcommands {
		names = append(names, sub.name)
	}
	return fcli.UserErr("unknown licence subcommand; want one of %s (see `fugaro licence --help`)", strings.Join(names, ", "))
}

// licenceKeyFromStdin is the one sentence `set` answers a key in argv with, and
// the same sentence its own help carries: a key in argv is a key every local
// process reads (F5, F41's rule). It is fixed, so a pasted key is never quoted
// back, and it says where the key does go rather than only what is refused.
const licenceKeyFromStdin = "the key is read from standard input or --file, never from an argument: a key in argv is a key every local process reads"

// licenceFirebaseProject is the workspace a key is bound to: the installation's
// Firebase project id, which the key's required workspace_id must equal (TN2).
func licenceFirebaseProject(lc *localcfg.Config) string {
	if lc.Budget == nil {
		return ""
	}
	return lc.Budget.FirebaseProject
}

// licenceNoWorkspace is the one line an installation with no Firebase project
// gets: a key's workspace_id is that project's id, so there is nothing to check
// a key against and nothing a key could be bound to. It names the one command
// that gives the installation a workspace, as the core's own reader of the
// Firebase backend does (internal/cli's Cloud.Firestore).
func licenceNoWorkspace(lc *localcfg.Config) error {
	return fcli.UserErr("project %s has no Firebase project (budget.firebase_project is not set in its project config), so a licence key, which is bound to that project's id, cannot be read or installed here: an operator runs `fugaro update --firebase-project %s --name %s`",
		safetext.Field(lc.Name), safetext.Field(lc.GCPProject), safetext.Field(lc.Name))
}

// licenceRedact is text with every form of every value taken out of it, bounded
// to one printable field (safetext: a store or a server may echo a body) and
// with the two spellings of each value removed: the bytes as they were handed
// to the socket, and the base64 AddVersion wraps them in. Every line this
// command prints that could carry back something it read goes through here.
//
// The values are not the whole of it, because a value list can only name what
// this process holds: the licence server's error body is printed as it came
// back, and what it echoes is whatever its records hold — a key this
// installation never had among them, the successor of a renewal it already
// answered for one. So the shape goes too, and that is what the `fgk_` prefix
// is for: the spec gives it to keys so that a log, a scanner and a support
// ticket recognise one (section 3.1), and a key about to be printed here is
// recognised by the same rule. The shape pass runs over the text the values
// have already been taken out of, and the bound is applied last, so a key the
// bound would have cut in half is gone before it can leave its front behind.
func licenceRedact(text string, values ...string) string {
	forms := make([]string, 0, len(values)*2)
	for _, value := range values {
		if value == "" {
			continue
		}
		forms = append(forms, value, base64.StdEncoding.EncodeToString([]byte(value)))
	}
	return safetext.Field(licenceKeyShape.ReplaceAllString(agent.Redact(text, forms), licenceRedacted))
}

// licenceKeyShape is a v1 key wherever it stands in text this command prints:
// `fgk_`, a base64url payload, a dot and a base64url signature (spec section
// 3.1). The dot's part is optional because the text is a body a server wrote
// and nothing says it wrote the whole of a key: a key cut short — by the
// server, or by the reader's own 16 KiB cap — is a key whose front would
// otherwise be printed, and the front of a key is the key. The floor on the
// payload is what keeps the rule off prose about the format (`fgk_<payload>`,
// which ee/licence's own refusal spells out).
//
// The shape is written here rather than read out of ee/licence, which holds the
// prefix and the format: what this matches is a key in a body Fugaro never
// parsed, so there is no key to read the format from — only a shape to
// recognise. A test redacts a key the fixture minted, so a format that moves
// fails there rather than leaving this rule quietly blind.
var licenceKeyShape = regexp.MustCompile(`fgk_[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]+)?`)

// licenceRedacted is what a key's place is left as: the marker the value pass
// writes too (internal/redact), so that a reader of a refusal cannot tell which
// of the two passes took a value out of it.
const licenceRedacted = "[REDACTED]"

// licenceKeyRefusal is what a key this build will not accept is answered with,
// and it repeats no part of the key.
//
// ErrNewerVersion is the one refusal printed exactly as it comes: the
// specification pins its sentence word for word and the verifier returns it
// unwrapped so that a caller prints exactly it (ee/licence/keyv1.go). Every
// other refusal is this command's own sentence with the verifier's text after
// it.
func licenceKeyRefusal(err error, key string) error {
	if errors.Is(err, licence.ErrNewerVersion) {
		return fcli.UserErr("%s", err)
	}
	return fcli.UserErr("the key was not accepted, and nothing was written: %s", licenceRedact(err.Error(), key))
}

// licenceRefusals are the verifier's own refusals: a key that is not one this
// Fugaro accepts. A reader that meets one has a state of the installation to
// report rather than a call that failed — the key is in the project and this
// build will not act on it — so `status` says so and never reads it as Free,
// which is the one answer that would tell a person their licence is fine.
//
// The list is the verifier's exported sentinels (ee/licence/key.go and
// keyv1.go), and it is written out rather than inferred because a package
// cannot be asked whether an error came from it: a sentinel a later task adds
// is a line here, beside the reader that has to place it.
var licenceRefusals = []error{
	licence.ErrMalformed, licence.ErrMalformedV1, licence.ErrUnknownKeyID, licence.ErrSignature,
	licence.ErrClaims, licence.ErrLifetime, licence.ErrDuration, licence.ErrWorkspace,
	licence.ErrNewerVersion, licence.ErrKeyFile,
}

// licenceRefused reports whether err is one of the verifier's own refusals.
func licenceRefused(err error) bool {
	for _, sentinel := range licenceRefusals {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// licenceObjectErr maps a failure of the project's own store. ErrObject is the
// store's refusal — an object it cannot read, more keys than the renewal's
// overlap installs, a write that kept losing its race — and a verifier's
// refusal is a key the project holds and this build will not use; both are a
// state of the installation rather than a failure of the call, so both are user
// errors (exit 1). Anything else came from the bucket underneath and is remote
// (exit 2). err is redacted at the call site: the object this store writes holds
// the key.
//
// ErrNewerVersion is the one refusal printed exactly as it comes, as it is
// wherever else this command answers it: the specification pins its sentence
// and the verifier returns it unwrapped so that a caller prints exactly it.
func licenceObjectErr(err error, key string) error {
	switch {
	case errors.Is(err, licence.ErrNewerVersion):
		return fcli.UserErr("%s", err)
	case errors.Is(err, project.ErrObject):
		return fcli.UserErr("the project's licence object (%s) could not be used: %s", project.Object, licenceRedact(err.Error(), key))
	case licenceRefused(err):
		return fcli.UserErr("the project's licence object (%s) holds a key this Fugaro does not accept, and nothing was written: %s", project.Object, licenceRedact(err.Error(), key))
	}
	return fcli.RemoteErr(errors.New(licenceRedact(err.Error(), key)))
}

// licenceSecrets is this command's reach on the installation's one Secret
// Manager container: the caller's own Secret Manager client (no service
// account, no actAs — the same reach `fugaro creds` has on its bundle) and the
// three calls the licence commands make. It creates nothing: the container is
// K4.2's Terraform's, and a command that created one would give `update` a
// secret it did not make.
type licenceSecrets struct {
	svc     *secretmanager.Service
	project string // the GCP project the container lives in
	id      string // the container's ID (gcp.ProjectSecretID)
}

// errNoRenewalSecret is the container being there with no version behind it:
// the state an installation is in between the `fugaro update` that created the
// empty container and the first `licence set` that writes a version into it.
var errNoRenewalSecret = errors.New("ee/register: the renewal secret container holds no version")

func openLicenceSecrets(ctx context.Context, lc *localcfg.Config) (*licenceSecrets, error) {
	svc, err := gcp.NewSecretManager(ctx, gcp.Options{
		GCPProject: lc.GCPProject, Region: lc.Region,
		Endpoints: gcp.Endpoints{SecretManager: lc.Endpoints.SecretManager, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return nil, fcli.RemoteErr(err)
	}
	return &licenceSecrets{svc: svc, project: lc.GCPProject, id: licenceRenewalSecretID(lc.Name)}, nil
}

// licenceRenewalSecretID is the container K4.2's Terraform creates for the
// installation's renewal secret, derived from the Fugaro project name exactly
// as every other installation-wide container is.
func licenceRenewalSecretID(fugaroProject string) string {
	return gcp.ProjectSecretID(fugaroProject, licenceRenewalSecretLogical)
}

func (s *licenceSecrets) parent() string { return "projects/" + s.project + "/secrets/" + s.id }

// check reads the container itself, before any version of it: an installation
// that has not run `fugaro update` since the licence job landed has none, and
// that is a refusal that names the one command which fixes it rather than a
// version read that fails as "no version" — which is what a container with no
// version is, and the legitimate state the first write starts from.
func (s *licenceSecrets) check(ctx context.Context) error {
	if _, err := s.svc.Projects.Secrets.Get(s.parent()).Context(ctx).Do(); err != nil {
		return s.apiErr("read", err, nil)
	}
	return nil
}

// add writes value as a new version of the container. The version it replaced
// stays: a renewal's overlap needs the previous secret to keep working for the
// 24 hours the server honours it (K2.3's handover), so nothing here retires a
// version.
func (s *licenceSecrets) add(ctx context.Context, value []byte) error {
	if _, err := s.svc.Projects.Secrets.AddVersion(s.parent(), &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(value)},
	}).Context(ctx).Do(); err != nil {
		return s.apiErr("write", err, []string{string(value)})
	}
	return nil
}

// latest reads the newest version of the container: the renewal secret
// `refresh` proves itself with. It is the one value this command reads back,
// and it is held in memory for the length of one request and printed by
// nothing.
func (s *licenceSecrets) latest(ctx context.Context) ([]byte, error) {
	r, err := s.svc.Projects.Secrets.Versions.Access(s.parent() + "/versions/latest").Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return nil, errNoRenewalSecret
		}
		return nil, s.apiErr("read", err, nil)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, errNoRenewalSecret
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("reading secret %s: the payload is not base64", safetext.Field(s.id)))
	}
	return data, nil
}

// apiErr maps a Secret Manager failure to this command's own answer: a named
// user error for the two an operator fixes at their own terminal — the
// container is not there (Terraform creates it), and this identity may not
// touch it — and otherwise the failure itself, redacted with the forms the
// caller names (a server may repeat the payload it was sent). No branch carries
// a value: the container's name is a name.
func (s *licenceSecrets) apiErr(op string, err error, values []string) error {
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
		return fcli.UserErr("the installation's renewal secret container %s is not in project %s: `fugaro update` creates it and no licence command does, so run `fugaro update` and then set the key again",
			safetext.Field(s.id), safetext.Field(s.project))
	case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
		return fcli.UserErr("this identity may not %s the renewal secret container %s in project %s: it needs %s on that one secret, which a project owner has",
			op, safetext.Field(s.id), safetext.Field(s.project), licenceSecretPermission(op))
	}
	return fcli.RemoteErr(errors.New(licenceRedact(err.Error(), values...)))
}

// licenceSecretPermission is the IAM permission the call behind one op needs,
// named the way an operator grants it. Reading the container is two of them --
// the container's own metadata (secrets.get) and then one version's payload
// (versions.access) -- and writing a version is a third (versions.add). They are
// named apart because a refusal that lists a permission its own call does not
// use sends an operator to a grant that leaves the refusal exactly where it was:
// secretmanager.versions.get reads a version's metadata and is not what a
// payload read asks for.
func licenceSecretPermission(op string) string {
	if op == "write" {
		return "secretmanager.versions.add"
	}
	return "secretmanager.secrets.get and secretmanager.versions.access"
}

// licenceBundle is what `set` was handed: the key, and the renewal secret the
// key's own renewal is proved with when it has one.
type licenceBundle struct {
	key    string
	secret string
}

// readLicenceBundle reads a bundle from the file named by --file, or from
// standard input when none is. Line 1 is the key and line 2, when there is one,
// the renewal secret — the two-line bundle every issue path shows once
// (D2135) — and blank lines are skipped, so a trailing newline or a pasted
// block with a blank line in it is read as the one or two values it holds.
//
// Nothing a refusal says repeats a line: the count of lines is what is wrong
// when anything is, and the argument position is one a pasted key lands in.
//
// The value of --file is such a position, so no refusal here echoes it either:
// a key pasted where a path belongs is refused with the reason the file could
// not be opened and never with the value, which is what os.Open's own error
// would carry. A reader of these messages knows the path it typed.
func readLicenceBundle(cmd *cobra.Command, path string) (licenceBundle, error) {
	var (
		r    io.Reader
		from string
	)
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return licenceBundle{}, fcli.UserErr("nothing was read from --file (%s), and the value of --file is not echoed: it is the path of a file holding the key, and a key pasted into it is a key in argv. %s",
				licenceSystemReason(err, "the file could not be opened"), licenceKeyFromStdin)
		}
		defer f.Close()
		r, from = f, "--file"
	} else {
		r, from = cmd.InOrStdin(), "standard input"
	}
	data, err := io.ReadAll(io.LimitReader(r, licenceMaxBundleBytes+1))
	if err != nil {
		// A read the file refused fails the same way an open does, and carries
		// the same name: io.ReadAll over an *os.File returns an *os.PathError
		// whose Path is the file it read, which is the --file value.
		return licenceBundle{}, fcli.UserErr("%s could not be read (%s), and nothing read from it is echoed: %s",
			from, licenceSystemReason(err, "the read failed"), licenceKeyFromStdin)
	}
	if len(data) > licenceMaxBundleBytes {
		return licenceBundle{}, fcli.UserErr("the key read from %s is over %d bytes, which is more than a key and its renewal secret are: nothing was written. Line 1 is the key and line 2 the renewal secret, when the key has one",
			from, licenceMaxBundleBytes)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	switch {
	case len(lines) == 0:
		return licenceBundle{}, fcli.UserErr("no key was read from %s: line 1 is the key and line 2 the renewal secret, when the key has one", from)
	case len(lines) > 2:
		return licenceBundle{}, fcli.UserErr("%s holds %d lines, and a licence key is one line with the renewal secret, when the key has one, on a second: nothing was read", from, len(lines))
	}
	b := licenceBundle{key: lines[0]}
	if len(lines) == 2 {
		b.secret = lines[1]
	}
	return b, nil
}

// licenceSystemReason is why a file call failed, and never the name it was given:
// an *os.PathError carries that name verbatim — os.Open's, and the read an
// *os.File makes through io.ReadAll — and the name it was given is an argument,
// the place a key pasted by mistake lands, which is the one thing a refusal here
// may not print (F5, F41). The reasons a path fails are the system's own words
// ("no such file or directory", "permission denied", "is a directory"), and they
// are what a person needs; fallback is the caller's own words for a failure with
// none of them, because the caller knows which call it made.
func licenceSystemReason(err error, fallback string) string {
	var pe *os.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	return fallback
}

// licenceSet: `fugaro licence set`.
type licenceSetOptions struct {
	cloud fcli.CloudFlags
	file  string
}

func newLicenceSetCmd() *cobra.Command {
	var o licenceSetOptions
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Install the installation's licence key (Subscription Edition)",
		Long: `Install the licence key for this installation, and the renewal secret that
came with it.

The key is read from standard input or --file: ` + licenceKeyFromStdin + `.
Line 1 is the key, and line 2 is the renewal secret — an offline key has none,
and the key itself says which it is (` + "`renewal_mode`" + `). One trailing newline, a
blank line and a CRLF line ending are all handled; more than two non-empty
lines are refused, and no refusal repeats a line.

The key is verified before anything is written, so a key this Fugaro does not
accept — an unknown signing key, a term longer than the tier allows, a key
bound to another workspace — is refused instead of installed, and the
installation keeps whatever it had. The key's workspace_id must be the
installation's Firebase project id.

The key is then written to the project's own store, so every member is
upgraded at once, and the renewal secret (when there is one) to the
installation's Secret Manager container. Both need the container ` + "`fugaro update`" + `
creates: before it is there this command refuses and names that command, and it
never creates one.

The installation's renewal job is Terraform's (F10): this command writes the
key and its secret and nothing else, and the next ` + "`fugaro update`" + ` applies the job
that renews a ` + "`renewal_mode: auto`" + ` key. With no coordinator opted in, nothing
renews a key but ` + "`fugaro licence refresh`" + `.

It is refused inside a coding agent's session, before anything is read, as every
cloud write is. No line of its output carries the key.`,
		Args: func(_ *cobra.Command, args []string) error {
			// The argument position is where a pasted key lands, and cobra's
			// own answer to an unexpected argument quotes it back. So the
			// refusal is this command's own fixed sentence.
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence set takes no argument, and the argument is not echoed: %s. Nothing was read", licenceKeyFromStdin)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceSet(cmd, &o) },
	}
	cmd.Flags().StringVar(&o.file, "file", "", "read the key (line 1) and its renewal secret (line 2) from this file instead of standard input")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func licenceSet(cmd *cobra.Command, o *licenceSetOptions) error {
	ctx := cmd.Context()
	if err := fcli.AgentSessionRefusal(os.Getenv, "nothing was written: `fugaro licence set` installs the installation's licence key and the secret it renews with, and a key is a person's to install"); err != nil {
		return err
	}
	bundle, err := readLicenceBundle(cmd, o.file)
	if err != nil {
		return err
	}
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	if licenceFirebaseProject(lc) == "" {
		return licenceNoWorkspace(lc)
	}
	claims, state, err := licence.VerifyV1([]byte(bundle.key), licenceNow(), licenceRing, licenceFirebaseProject(lc))
	if err != nil {
		return licenceKeyRefusal(err, bundle.key)
	}
	// The secret is written before the key, and the key only after it landed: a
	// key whose secret is not in the container is a key that cannot renew, and
	// the other order would leave one behind on a failure between the two.
	if bundle.secret != "" {
		secrets, err := openLicenceSecrets(ctx, lc)
		if err != nil {
			return err
		}
		if err := secrets.check(ctx); err != nil {
			return err
		}
		if err := secrets.add(ctx, []byte(bundle.secret)); err != nil {
			return err
		}
	}
	if err := project.New(cloud.RunsBucket()).Install(ctx, []byte(bundle.key)); err != nil {
		if bundle.secret != "" {
			// The secret landed and the key did not: the container is a version
			// ahead of the project, and that is a state a person has to undo
			// (licenceHalfWritten).
			return fcli.UserErr("%s. The project write failed with: %s",
				licenceHalfWritten(lc, "run `fugaro licence set` again with the same key and secret, which writes the key the secret was written for"),
				licenceRedact(err.Error(), bundle.key, bundle.secret))
		}
		return licenceObjectErr(err, bundle.key)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "key installed in project %s (%s)\n", safetext.Field(lc.Name), project.Object)
	licenceWriteClaims(w, claims, state)
	if bundle.secret != "" {
		fmt.Fprintf(w, "renewal secret written to Secret Manager container %s\n", safetext.Field(licenceRenewalSecretID(lc.Name)))
	} else if claims.RenewalMode != licence.RenewalNone && claims.RenewalMode != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: this key renews itself (renewal_mode: %s) and no renewal secret was read: paste the secret that came with the key on line 2, or nothing can ask the licence server for the next key\n", safetext.Field(claims.RenewalMode))
	}
	fmt.Fprintln(w, "The next `fugaro update` applies the installation's renewal job.")
	return nil
}

// licenceWriteClaims prints what a verified key carries, and never a byte of
// the key itself: `set` and `refresh` both answer with a key they have just
// installed, and the one thing neither may show is the key.
func licenceWriteClaims(w io.Writer, claims licence.ClaimsV1, state licence.State) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  tier:\t%s\n", safetext.Field(claims.Tier))
	fmt.Fprintf(tw, "  seats:\t%d\n", claims.Seats)
	fmt.Fprintf(tw, "  paid through:\t%s\n", claims.PaidThrough.UTC().Format(time.RFC3339))
	fmt.Fprintf(tw, "  state:\t%s\n", safetext.Field(string(state)))
	fmt.Fprintf(tw, "  renewal:\t%s\n", safetext.Field(claims.RenewalMode))
	_ = tw.Flush()
}

// licenceStatus: `fugaro licence status`.
type licenceStatusOptions struct {
	cloud  fcli.CloudFlags
	asJSON bool
}

func newLicenceStatusCmd() *cobra.Command {
	var o licenceStatusOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the installation's licence: tier, seats, expiry, state and last check",
		Long: `Show what this installation's licence is: the tier, the seats, the end of the
paid period, the state the clock and the last revocation check put the key in,
and that check.

The key the answer comes from is the project's own, so every member of the
installation sees the same figures. Where the project holds no key and this
machine's own ` + "`licence.key`" + ` does, that key answers and the source says so.
An installation with neither has no key at all, which is the Free state and not
a failure: ` + "`fugaro licence set`" + ` installs one.

The state is one of ` + "`active`" + ` (inside the paid period), ` + "`grace`" + ` (inside the key's own
grace days after it), ` + "`expired`" + ` and ` + "`free`" + ` (no key at all). A key the licence
server has revoked reads ` + "`expired`" + ` with ` + "`revoked: true`" + ` and is Free in
behaviour: nothing is deleted and a new key restores everything.

--json prints {project, state, tier, seats, paid_through, kind, renewal_mode,
source, revoked, last_check}, with paid_through null where no key is installed
and last_check the last revocation check's own record.

This is a read: it is not refused inside a coding agent's session.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence status takes no argument, and the argument is not echoed: this position is one a pasted key can land in. Nothing was read")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceStatus(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// licenceStatusJSON is `licence status --json`: the shape a reader outside this
// process shows, and the one the console's reader takes (K3.6). The names are
// this format's own; a field is added and never retyped (CLAUDE.md, "A field
// never changes meaning").
type licenceStatusJSON struct {
	// Project is the Fugaro project the answer is about.
	Project string `json:"project"`
	// State is free, active, grace or expired.
	State string `json:"state"`
	// Tier, Seats, PaidThrough, Kind and RenewalMode are what the key claims,
	// and are empty or zero where no key is installed.
	Tier        string     `json:"tier,omitempty"`
	Seats       int        `json:"seats"`
	PaidThrough *time.Time `json:"paid_through"`
	Kind        string     `json:"kind,omitempty"`
	RenewalMode string     `json:"renewal_mode,omitempty"`
	// Source is which store answered (project or machine), and is empty where
	// no key answered at all.
	Source string `json:"source,omitempty"`
	// Revoked says the state above is the licence server's answer rather than
	// the term's.
	Revoked bool `json:"revoked"`
	// LastCheck is the last revocation check the installation ran, in the
	// store's own record shape, and null where it never ran one.
	LastCheck *project.Check `json:"last_check"`
}

func licenceStatus(cmd *cobra.Command, o *licenceStatusOptions) error {
	ctx := cmd.Context()
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	// The machine's own keys are read as a fallback only, and a machine store
	// this cannot read is refused rather than silently skipped: the one thing
	// that makes it unreadable is a key file any other local user can read,
	// which is the state `status` exists to make visible.
	machine, err := licence.Open(os.Getenv)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	keys, err := machine.Keys()
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	view, err := project.New(cloud.RunsBucket()).View(ctx, licenceNow(), licenceRing, licenceFirebaseProject(lc), keys)
	switch {
	case errors.Is(err, licence.ErrNoKey):
		// No key at all is the Free state and not a failure (D2143).
		view = project.View{State: ""}
	case errors.Is(err, licence.ErrWorkspace) && licenceFirebaseProject(lc) == "":
		return licenceNoWorkspace(lc)
	case err != nil:
		// Anything else is a refusal, and a refusal must never read as Free.
		return licenceObjectErr(err, "")
	}
	out := licenceStatusJSON{
		Project:     lc.Name,
		State:       licenceStateName(view),
		Tier:        view.Tier,
		Seats:       view.Seats,
		Kind:        view.Kind,
		RenewalMode: view.RenewalMode,
		Source:      string(view.Source),
		Revoked:     view.Revoked,
	}
	if !view.PaidThrough.IsZero() {
		paid := view.PaidThrough.UTC()
		out.PaidThrough = &paid
	}
	if view.HasCheck {
		check := view.Check
		out.LastCheck = &check
	}
	if o.asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	w := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "project:\t%s\n", safetext.Field(lc.Name))
	fmt.Fprintf(tw, "state:\t%s\n", licenceStateLine(view))
	if view.Source != "" {
		fmt.Fprintf(tw, "tier:\t%s\n", safetext.Field(view.Tier))
		fmt.Fprintf(tw, "seats:\t%d\n", view.Seats)
		fmt.Fprintf(tw, "paid through:\t%s\n", view.PaidThrough.UTC().Format(time.RFC3339))
		fmt.Fprintf(tw, "kind:\t%s\n", safetext.Field(view.Kind))
		fmt.Fprintf(tw, "renewal:\t%s\n", safetext.Field(view.RenewalMode))
		fmt.Fprintf(tw, "source:\t%s\n", safetext.Field(string(view.Source)))
	}
	if view.HasCheck {
		fmt.Fprintf(tw, "last check:\t%s\n", licenceCheckLine(view.Check))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	licenceStatusNotes(cmd.ErrOrStderr(), lc, view)
	return nil
}

// licenceStateName is the state a reader outside this process is given: free
// where no key answered, and otherwise the state the store put the key in
// (active, grace or expired). A key the server revoked is expired with
// Revoked set: both were Free in behaviour and one is different news.
func licenceStateName(view project.View) string {
	if view.Source == "" {
		return "free"
	}
	return string(view.State)
}

// licenceStateLine is the state for a person, with the revocation said out
// loud where it is the server's answer rather than the term's.
func licenceStateLine(view project.View) string {
	name := licenceStateName(view)
	if view.Revoked {
		return name + " (revoked by the licence server)"
	}
	return name
}

// licenceCheckLine is the last revocation check as one row: when it ran, what
// it answered, how many checks in a row could not reach the server, and the key
// it was about. None of those is a secret: the key_id is the support handle the
// specification says is not one.
func licenceCheckLine(check project.Check) string {
	line := check.At.UTC().Format(time.RFC3339) + " " + safetext.Field(check.Status)
	if check.Failures > 0 {
		line += fmt.Sprintf(" (%d in a row could not reach the server)", check.Failures)
	}
	if check.KeyID != "" {
		line += " (key " + safetext.Field(check.KeyID) + ")"
	}
	return line
}

// licenceStatusNotes is what a person is told beside the figures: that there is
// no key at all, that a machine's key is the one that answered rather than the
// project's, and that nothing renews a self-renewing key on this installation.
func licenceStatusNotes(w io.Writer, lc *localcfg.Config, view project.View) {
	switch {
	case view.Source == "":
		fmt.Fprintln(w, "note: no licence key is installed, so this project runs on Free: `fugaro licence set` installs one")
	case view.Source == project.SourceMachine:
		fmt.Fprintf(w, "note: this answer is this machine's own `licence.key`, and the project holds no key of its own: `fugaro licence set` upgrades every member at once\n")
	}
	if view.RenewalMode == licence.RenewalAuto && !lc.Terraform.Coordinator {
		// TN5: an installation with no coordinator renews by hand and gets no
		// automatic revocation check. The job is created under the coordinator's
		// own condition, and whether this installation turned the coordinator on
		// is the one fact about it that the project's config records; whether
		// the job itself exists is the installation's own answer, which nothing
		// on this machine can read.
		fmt.Fprintln(w, "note: the key renews itself and this installation does not turn the coordinator on, which is what applies the job that renews it: `fugaro licence refresh` renews it by hand")
	}
}

// licenceRefresh: `fugaro licence refresh`.
type licenceRefreshOptions struct {
	cloud fcli.CloudFlags
}

func newLicenceRefreshCmd() *cobra.Command {
	var o licenceRefreshOptions
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Ask the licence server for the next key now (Subscription Edition)",
		Long: `Ask the licence server for the next key for this installation's key, and
install it.

This is what an installation renews by hand with: a ` + "`renewal_mode: auto`" + ` key is
renewed by the installation's own scheduled job, and this command does the same
one thing that job does. It sends the key's id, the workspace the key is bound
to and the renewal secret to the licence server once, and a key that has no
secret — an offline key — never contacts the server at all.

The answer is one of three: the next key, which is verified before it is
written (the new secret first, then the key); "nothing to renew yet", where the
key is still further from its end than its own ` + "`renew_before_days`" + `; and a
revocation, in which case this key is Free from then on and the refusal names
it. Nothing here deletes anything, and a server that cannot be reached changes
nothing at all.

The project's own key is the one renewed: a per-machine ` + "`licence.key`" + ` is never
renewed here.

It is refused inside a coding agent's session, before anything is read, as every
cloud write is. No line of its output carries the key or the renewal secret.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence refresh takes no argument, and the argument is not echoed: this position is one a pasted key can land in. Nothing was read")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceRefresh(cmd, &o) },
	}
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func licenceRefresh(cmd *cobra.Command, o *licenceRefreshOptions) error {
	ctx := cmd.Context()
	if err := fcli.AgentSessionRefusal(os.Getenv, "nothing was renewed: `fugaro licence refresh` asks the licence server for this installation's next key and writes it, and that is a person's to do"); err != nil {
		return err
	}
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	if licenceFirebaseProject(lc) == "" {
		return licenceNoWorkspace(lc)
	}
	store := project.New(cloud.RunsBucket())
	// The project's key and only the project's: a renewal writes what it is
	// given into the project and would be writing over the installation's own
	// licence from a machine's fallback, which is not a renewal.
	applied, err := store.Active(ctx, licenceNow(), licenceRing, licenceFirebaseProject(lc), nil)
	switch {
	case errors.Is(err, licence.ErrNoKey):
		return fcli.UserErr("the project holds no licence key, so there is nothing to renew: `fugaro licence set` installs one")
	case err != nil:
		return licenceObjectErr(err, "")
	}
	claims := applied.Claims
	if claims.RenewalMode == licence.RenewalNone {
		// An offline key never contacts the server (spec section 7): the
		// answer is the key's own, and it is not a failure.
		fmt.Fprintf(cmd.OutOrStdout(), "this key is offline (renewal_mode: none): nothing contacts the licence server, and a new key is pasted in by hand with `fugaro licence set`. The key is %s until %s\n",
			safetext.Field(string(applied.State)), claims.PaidThrough.UTC().Format(time.RFC3339))
		return nil
	}
	secrets, err := openLicenceSecrets(ctx, lc)
	if err != nil {
		return err
	}
	if err := secrets.check(ctx); err != nil {
		return err
	}
	secret, err := secrets.latest(ctx)
	if err != nil {
		if errors.Is(err, errNoRenewalSecret) {
			return fcli.UserErr("the installation's renewal secret container %s holds no version, so this key cannot prove itself to the licence server: `fugaro licence set` writes the secret that came with the key", safetext.Field(licenceRenewalSecretID(lc.Name)))
		}
		return err
	}
	answer, err := licenceRenewRequest(ctx, keyservice.RenewalCall{
		KeyID:         claims.KeyID,
		WorkspaceID:   claims.WorkspaceID,
		RenewalSecret: string(secret),
	}, string(secret))
	if err != nil {
		return err
	}
	switch answer.Status {
	case keyservice.StatusRevoked:
		return fcli.UserErr("the licence server answered that key %s is revoked: this installation is Free from now on, nothing has been deleted, and a new key restores everything (`fugaro licence set`)", safetext.Field(claims.KeyID))
	case keyservice.StatusOK:
	default:
		return fcli.RemoteErr(fmt.Errorf("the licence server answered a status this Fugaro does not read (%s)", safetext.Field(fcli.ClipRunes(answer.Status, 40))))
	}
	if answer.Key == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "nothing to renew yet: the key is paid through %s, and its own renewal window opens %d days before that\n",
			claims.PaidThrough.UTC().Format(time.RFC3339), claims.RenewBeforeDays)
		return nil
	}
	next, state, err := licence.VerifyV1([]byte(answer.Key), licenceNow(), licenceRing, licenceFirebaseProject(lc))
	if err != nil {
		return fcli.UserErr("the licence server answered with a key this Fugaro does not accept, and nothing was written: %s", licenceRedact(err.Error(), answer.Key, answer.RenewalSecret))
	}
	// The new secret first, then the key: the key that is installed names the
	// new secret's hash, so a key without it is a key that cannot renew again.
	// The secret this replaces keeps working for the server's handover window,
	// so it stays in the container.
	if answer.RenewalSecret != "" {
		if err := secrets.add(ctx, []byte(answer.RenewalSecret)); err != nil {
			return err
		}
	}
	if err := store.Install(ctx, []byte(answer.Key)); err != nil {
		if answer.RenewalSecret != "" {
			// The secret landed and the key did not: the state licenceHalfWritten
			// describes, which a person has to undo.
			return fcli.UserErr("%s. The project write failed with: %s",
				licenceHalfWritten(lc, "run `fugaro licence set` with the key and the renewal secret this installation was issued, which is the bundle that last worked, and then `fugaro licence refresh` again: the licence server keeps a renewal's answer for 24 hours, so the successor it already minted is the one that comes back"),
				licenceRedact(err.Error(), answer.Key, answer.RenewalSecret))
		}
		return licenceObjectErr(err, answer.Key)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "renewed: the new key is installed in project %s (%s)\n", safetext.Field(lc.Name), project.Object)
	licenceWriteClaims(w, next, state)
	if answer.RenewalSecret == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: the licence server answered no renewal secret with the new key, so this installation cannot renew again without one: paste it with `fugaro licence set`")
	}
	return nil
}

// licenceHalfWritten is the state a failure between the two writes leaves: the
// renewal secret is a version of the container and the key it belongs to is not
// in the project, so the container's newest secret is not the one the installed
// key names, and a refresh that proves itself with the newest one asks the
// server about a pair it never issued — a 403 that outlasts the failure. It is
// not a lost licence: no version is retired, and the server keeps a renewal's
// answer for 24 hours. What restores the pair is `set`, the only writer of both
// halves, so the sentence always ends in what to run.
//
// The order the two writes are made in is the one that leaves this state rather
// than its mirror: a key whose secret is not in the container cannot renew at
// all, while a container a version ahead is undone by writing the pair again.
func licenceHalfWritten(lc *localcfg.Config, remedy string) string {
	return fmt.Sprintf("the renewal secret is in the container %s and the key it belongs to is not in the project, so the two are not a pair yet and nothing can renew until they are: %s",
		safetext.Field(licenceRenewalSecretID(lc.Name)), remedy)
}

// licenceRenewRequest calls the licence server's renewal route once (K2.3's
// endpoint) and answers what it said. The renewal secret is the whole of the
// proof the server asks for, so it is sent over TLS and nowhere else, and every
// failure this returns has been redacted of both the secret and whatever the
// server said back — including a key, which is why licenceRedact removes the
// shape as well as the values: a non-200 answer prints the body verbatim, and
// the body is the one place a key this process never held can come back from.
//
// The answer is decoded leniently on purpose: a field the server adds later is
// a field this build ignores rather than a renewal that fails (CLAUDE.md: a
// field is added and never retyped). A 403 is the one answer the server gives
// every caller it will not serve, and it says nothing about which of the two
// things was wrong — an unknown key id or a wrong secret on the one hand, and a
// key bound to another workspace on the other — so the refusal does not claim to
// know either: what it does instead is name the states a person can check and
// the command that writes the pair again, because the one a half-written renewal
// leaves behind (licenceHalfWritten) reaches this branch too.
func licenceRenewRequest(ctx context.Context, call keyservice.RenewalCall, secret string) (keyservice.Answer, error) {
	body, err := json.Marshal(call)
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(licenceServerURL, "/")+keyservice.RouteRenew, bytes.NewReader(body))
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: licenceRenewTimeout}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, licenceMaxAnswerBytes+1))
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	if len(payload) > licenceMaxAnswerBytes {
		return keyservice.Answer{}, fcli.RemoteErr(fmt.Errorf("the licence server's answer is over %d bytes", licenceMaxAnswerBytes))
	}
	if resp.StatusCode == http.StatusForbidden {
		return keyservice.Answer{}, fcli.UserErr("the licence server refused the renewal of key %s: the renewal secret sent is not the one that key renews with, or the key is bound to another workspace. A renewal interrupted between its two writes leaves the container holding a secret the installed key does not name, and a key that was reissued renews with the secret that came with it: `fugaro licence set`, with the key and the renewal secret this installation was issued, writes the pair", safetext.Field(call.KeyID))
	}
	if resp.StatusCode != http.StatusOK {
		return keyservice.Answer{}, fcli.RemoteErr(fmt.Errorf("the licence server answered HTTP %d: %s", resp.StatusCode, licenceRedact(string(payload), secret)))
	}
	var answer keyservice.Answer
	if err := json.Unmarshal(payload, &answer); err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	return answer, nil
}
