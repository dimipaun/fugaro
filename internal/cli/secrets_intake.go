// secrets_intake.go is the client half of the secrets intake (DS2.3): the
// `fugaro secrets user …` and `fugaro secrets shared …` families, which are
// what a person uses to put a credential of their own — or, as an admin, one
// the project shares — where the intake can serve it.
//
// WHY A SECOND FAMILY BESIDE `secrets set`. Today's `fugaro secrets set` writes
// one repository's or the project's Secret Manager container with the person's
// own project IAM (docs/design/project-secrets.md), and it keeps that meaning
// and those flags. The intake is the other writer: it holds the write and the
// delete under the two name prefixes (fugaro_u_<sub>_<name>, fugaro_p_<name>)
// and nothing else, it derives the owner from the verified identity rather than
// from anything the request says (R3, R12), and it is what makes a per-user
// credential possible at all — a container whose owner is a <sub> is not
// something a person's project IAM can name. So the two families sit beside
// each other rather than one replacing the other, and the verbs that reach the
// intake say so in their own names.
//
// WHAT THIS FILE IS, AND WHAT IT IS NOT. It reads a value from stdin or a
// hidden prompt and posts it; it lists names and dates; it deletes by name. It
// never reads a value back — the intake has no route that answers one
// (intake.Routes) — it never mints anything itself (personIdentity runs the
// person's own gcloud), and it holds no credential of its own. Every refusal
// F9 and F12 ask for is made before anything is read and before the service is
// reached, in credsOwnerOnly's own order.
//
// WHERE THE INTAKE IS. The installation's intake is its own Cloud Run service,
// deployed by the installation module beside the credentials service and served
// by this binary's hidden `intake serve` (intake_serve.go). Its address is
// Cloud Run's own for that service, so it is the credentials service's address
// with the service's name replaced — one function, secretsIntakeServiceURL,
// which is the one place DS2.2 or DS4.1 has to change if the module names the
// service something else: the local config carries no intake URL of its own,
// and this task adds none (a new config key is a key `fugaro update` would have
// to write and the settings registry would have to hold, and the deployment
// that knows the URL is DS2.2's). An installation that has no credentials
// service block at all is refused by name, with `fugaro update` as the step
// that fixes it.

package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// intakeServiceName is the Cloud Run service the intake is served from, as the
// installation module names it (DS2.2): the leading label of its own URL, and
// the whole of what secretsIntakeServiceURL's swap needs. It is named here
// rather than imported because internal/infra has no constant for it yet —
// DS2.2 owns that half, and the Terraform test that holds the module's service
// name to this one belongs with it.
const intakeServiceName = "fugaro-intake"

// The bounds of one call: a body the service would not read and an answer this
// client will not hold are both refused here, before anything is sent or
// decoded. maxIntakeAnswerBytes is the larger of the intake's two documents (a
// listing of one prefix) with room to spare, and maxIntakeBodyBytes is
// Secret Manager's own 64 KiB payload, base64d, plus the JSON around it.
const (
	maxIntakeAnswerBytes = 512 << 10
	maxIntakeBodyBytes   = 128 << 10
)

// intakeTimeout bounds one call. The intake answers from Secret Manager and the
// admin list in a second when it answers at all.
const intakeTimeout = 60 * time.Second

// newSecretsUserCmd is `fugaro secrets user`: the credentials one person owns.
func newSecretsUserCmd() *cobra.Command {
	return newSecretsIntakeGroupCmd(intake.ScopeUser)
}

// newSecretsSharedCmd is `fugaro secrets shared`: the project's own prefix,
// which the intake serves to an admin alone (D1935, D1940).
func newSecretsSharedCmd() *cobra.Command {
	return newSecretsIntakeGroupCmd(intake.ScopeShared)
}

// newSecretsIntakeGroupCmd builds one of the two groups. The two are the same
// three verbs with the same flags, and the scope is the whole of what they
// differ in — which prefix, and therefore who may run it. It is a function of
// the scope rather than two spellings so that a verb added to one is added to
// the other, which is what the caller of a family whose halves are one API
// wants.
func newSecretsIntakeGroupCmd(scope intake.Scope) *cobra.Command {
	word := intakeScopeWord(scope)
	cmd := &cobra.Command{
		Use:   word,
		Short: intakeGroupShort(scope),
		// Set so cobra doesn't answer an unknown subcommand by quoting it:
		// `fugaro secrets user <value>` must not echo the value.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown secrets %s subcommand; want set, ls or rm (see fugaro secrets %s --help)", word, word)
		},
	}
	// pflag quotes an unknown flag, and a value pasted in the wrong place could
	// be one (`-sk-ant-…`): these commands get their own fixed message, in the
	// shape the family's is (newSecretsCmd).
	cmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return userErr("unknown or malformed flag; secrets %s commands take --json, --config, --project, --gcp-project and --region, and never the value", word)
	})
	cmd.AddCommand(newSecretsIntakeSetCmd(scope), newSecretsIntakeLsCmd(scope), newSecretsIntakeRmCmd(scope))
	return cmd
}

// intakeScopeWord is the scope's own word, which is what the verbs are spelled
// with: the intake's scope vocabulary is the constant, and a second spelling of
// "user" or "shared" in this package would be a second word for one thing.
func intakeScopeWord(scope intake.Scope) string { return string(scope) }

func intakeGroupShort(scope intake.Scope) string {
	if scope == intake.ScopeShared {
		return "Store, list and remove the secrets the project shares (an admin's)"
	}
	return "Store, list and remove the secrets you own"
}

// secretsIntakeOptions is the flags the six verbs share: the project selection
// every cloud command has, and --json on the answer.
type secretsIntakeOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func (o *secretsIntakeOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// newSecretsIntakeSetCmd is `fugaro secrets user set NAME` (and its shared
// twin). Like `fugaro secrets set` it has no flag or argument that takes the
// value (Global Constraint: a value comes from stdin or a hidden prompt only).
func newSecretsIntakeSetCmd(scope intake.Scope) *cobra.Command {
	var o secretsIntakeOptions
	word := intakeScopeWord(scope)
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Store a secret's value, read from stdin or a hidden prompt, through the intake",
		Long: `Store the value of NAME through the installation's secrets intake, as a new
version: the container is created if it doesn't exist, and a second call with
the same name rotates it.

The value is read only from stdin: pipe or redirect it in (fugaro secrets ` + word + ` set
github-token < token-file), or, when stdin is a terminal, paste it at a hidden
prompt. It is never taken from an argument, a flag or the environment, and
never printed. One trailing newline (\n or \r\n) is dropped, and the command
says so. A value is one line of 4 bytes to 64 KiB with no whitespace at either
end. The one value that spans lines is the GitHub App key's PEM, and it has no
business here: ` + "`fugaro secrets set " + multilineSecret + "`" + ` is the command that stores
one, and the intake's two prefixes refuse that name (R7). Ctrl-C at the prompt
stores nothing and restores the terminal.

The value travels to the intake and is written there by the intake's own
account: this command sends it over the installation's own endpoint, with the
person's own Google ID token as the caller, and holds no write of its own in
Secret Manager.

` + intakeScopeNote(scope) + `

The installation's intake is deployed by ` + "`fugaro update`" + `: an installation whose
release predates it, or one whose deployment does not answer the intake's
routes, is refused with that command as the whole upgrade.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("secrets %s set needs the secret's NAME", word)
			case len(args) > 1:
				return userErr("pass the value on stdin, never as an argument: arguments land in shell history and process lists")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return secretsIntakeSet(cmd, o, scope, args[0])
		},
	}
	o.addFlags(cmd)
	return cmd
}

// newSecretsIntakeLsCmd is `fugaro secrets user ls`: the names and the dates.
func newSecretsIntakeLsCmd(scope intake.Scope) *cobra.Command {
	var o secretsIntakeOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the secrets under this prefix: names and dates, never values",
		Long: `List the installation's intake's containers under this prefix: the names and
the dates they were made, and no version's value. The intake has no route that
answers a value at all, so this command cannot print one whatever it is asked.

` + intakeScopeNote(scope),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return secretsIntakeLs(cmd, o, scope)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// newSecretsIntakeRmCmd is `fugaro secrets user rm NAME`.
func newSecretsIntakeRmCmd(scope intake.Scope) *cobra.Command {
	var o secretsIntakeOptions
	word := intakeScopeWord(scope)
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a secret this prefix holds",
		Long: `Remove the container NAME through the installation's intake: the container and
every version in it, which is the only way back from a value stored under a
name that was meant for something else.

` + intakeScopeNote(scope),
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("secrets %s rm needs the secret's NAME", word)
			case len(args) > 1:
				return userErr("secrets %s rm takes one NAME", word)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return secretsIntakeRm(cmd, o, scope, args[0])
		},
	}
	o.addFlags(cmd)
	return cmd
}

// intakeScopeNote is the paragraph of a verb's help that says whose prefix the
// run is about, in the words the intake's own scope rules use.
func intakeScopeNote(scope intake.Scope) string {
	if scope == intake.ScopeShared {
		return `Scope: the project's own prefix, fugaro_p_<name>, which the intake serves
to an admin on the installation's derived admin list and to nobody else
(D1935, D1940). A caller who is not on it is refused by the intake, by name,
and nothing is written.`
	}
	return `Scope: your own prefix, fugaro_u_<your account id>_<name>. The owner is
taken from the identity token this command presents and never from anything
this command says about itself, so no caller can write under another person's
prefix (R3, R12).`
}

// secretsIntakeRefusal is the refusal every one of the six verbs makes, in
// credsOwnerOnly's order and for its reason (F9, F12): a coding agent's
// session, a run's own process, and the GODEBUG http2debug leak, all before
// anything is read and before the config is opened. did is what the refusal
// says was not done: "stored", "listed" or "removed".
//
// The agent check is a speed bump and not a boundary, exactly as it is for
// `fugaro creds`: an agent's session runs under the person's credentials and
// can unset its own marker. What it buys is that no agent goes through the
// program by accident, and that no run's process — whose environment the
// platform sets, not the agent — reaches the intake at all.
func secretsIntakeRefusal(getenv func(string) string, did string) error {
	if m := agentMarker(getenv, processTerminal()); m != "" {
		return userErr("nothing was %s: the secrets intake is called with your own Google ID token, which is the person's and not an agent's, so it is run by a person at their own terminal: %s", did, initflow.AgentRefusal(m))
	}
	if m := managedFlagRunMarker(getenv); m != "" {
		return userErr("nothing was %s: the secrets intake is called with the person's own Google ID token, and this process is a Fugaro run's (%s is set)", did, m)
	}
	return refuseHTTP2Debug(getenv)
}

// secretsIntakeSet stores one value through the intake. The order is the whole
// of its safety: the refusals, then the address (from local config, before a
// person is ever prompted for a credential), then the value, then the call.
func secretsIntakeSet(cmd *cobra.Command, o secretsIntakeOptions, scope intake.Scope, name string) error {
	ctx := cmd.Context()
	if err := secretsIntakeRefusal(os.Getenv, "stored"); err != nil {
		return err
	}
	serviceURL, err := secretsIntakeServiceURL(ctx, o.cloud)
	if err != nil {
		return err
	}
	// Not the PEM read: `name == multilineSecret` is the old family's, and every
	// name this family writes is one line, because the only value that spans
	// lines belongs to a reserved platform name the intake refuses (R7) — the
	// grammar and this help say the same thing.
	value, err := readSecret(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), name, false)
	if err != nil {
		return err
	}
	var answer intake.SecretAnswer
	if err := secretsIntakeDo(ctx, serviceURL, secretsIntakeCall{
		Path: intake.CreatePath,
		What: "a secret of yours is stored",
		Body: intake.CreateRequest{Scope: scope, Name: name, Value: base64.StdEncoding.EncodeToString(value)},
		Out:  &answer,
	}); err != nil {
		return err
	}
	if o.asJSON {
		return printIntakeAnswer(cmd, answer)
	}
	// The line names what was written and never a byte of it: the version and
	// the container are what the person who stored it needs to find it again in
	// Secret Manager, which is what `secrets set`'s own step line names.
	fmt.Fprintf(cmd.OutOrStdout(), "stored %s (version %s in %s)\n",
		pluginwire.Printable(name), dash(answer.Version), dash(answer.ID))
	return nil
}

// secretsIntakeLs asks the intake for a listing and prints it.
func secretsIntakeLs(cmd *cobra.Command, o secretsIntakeOptions, scope intake.Scope) error {
	ctx := cmd.Context()
	if err := secretsIntakeRefusal(os.Getenv, "listed"); err != nil {
		return err
	}
	serviceURL, err := secretsIntakeServiceURL(ctx, o.cloud)
	if err != nil {
		return err
	}
	var answer intake.ListAnswer
	if err := secretsIntakeDo(ctx, serviceURL, secretsIntakeCall{
		Path: intake.ListPath,
		What: "your secrets are listed",
		Body: intake.ListRequest{Scope: scope},
		Out:  &answer,
	}); err != nil {
		return err
	}
	if o.asJSON {
		return printIntakeAnswer(cmd, answer)
	}
	return printIntakeListing(cmd.OutOrStdout(), scope, answer)
}

// secretsIntakeRm removes one container by name.
func secretsIntakeRm(cmd *cobra.Command, o secretsIntakeOptions, scope intake.Scope, name string) error {
	ctx := cmd.Context()
	if err := secretsIntakeRefusal(os.Getenv, "removed"); err != nil {
		return err
	}
	serviceURL, err := secretsIntakeServiceURL(ctx, o.cloud)
	if err != nil {
		return err
	}
	var answer intake.DeleteAnswer
	if err := secretsIntakeDo(ctx, serviceURL, secretsIntakeCall{
		Path: intake.DeletePath,
		What: "a secret of yours is removed",
		Body: intake.DeleteRequest{Scope: scope, Name: name},
		Out:  &answer,
	}); err != nil {
		return err
	}
	if o.asJSON {
		return printIntakeAnswer(cmd, answer)
	}
	if len(answer.Deleted) == 0 {
		// The intake answers a delete that matched nothing as done — what it was
		// asked for, that no such container is left, is true — so the line says
		// which of the two happened rather than claiming a removal.
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s was not there; nothing was removed\n", pluginwire.Printable(name))
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", strings.Join(printableAll(answer.Deleted), ", "))
	return err
}

// printIntakeAnswer prints one of the intake's own answers as the document it
// is: the wire shape is the service's (internal/intake), so a reader of --json
// reads the same fields the service wrote, and this command adds none.
func printIntakeAnswer(cmd *cobra.Command, answer any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(answer)
}

// printIntakeListing prints a listing the way the rest of this family prints
// one: two columns, and no value anywhere.
func printIntakeListing(w io.Writer, scope intake.Scope, answer intake.ListAnswer) error {
	if len(answer.Secrets) == 0 {
		subject := "for you"
		if scope == intake.ScopeShared {
			subject = "for the project"
		}
		_, err := fmt.Fprintf(w, "no secrets stored %s\n", subject)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCREATED")
	for _, s := range answer.Secrets {
		created := ""
		if !s.Created.IsZero() {
			created = s.Created.UTC().Format("2006-01-02 15:04")
		}
		fmt.Fprintln(tw, dash(s.Name)+"\t"+dash(created))
	}
	return tw.Flush()
}

// printableAll is pluginwire.Printable over a list: a name or an id that came
// back from another writer is text this process prints, and it is escaped the
// way every other name this family prints is.
func printableAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, pluginwire.Printable(s))
	}
	return out
}

// secretsIntakeCall is one call: the route, the document to send, the document
// to read the answer into, and the work the caller's own identity is minted
// FOR (personIdentity's `what`, which is what a refusal of the mint reads as).
type secretsIntakeCall struct {
	Path string
	What string
	Body any
	Out  any
}

// secretsIntakeDo is the one seam every call goes through, and it is a package
// variable so that a test drives the verbs with no service and no credential
// (run.go's slipGrant is the same seam for the launch's own call).
var secretsIntakeDo = func(ctx context.Context, serviceURL string, c secretsIntakeCall) error {
	return secretsIntakeOver(ctx, serviceURL, c)
}

// secretsIntakeIdentity mints the caller's own Google ID token for serviceURL.
// It is personIdentity — the mint both other callers of the installation's
// services present — so there is one answer to "who is calling" on this side of
// the wire, and the intake verifies exactly that token (credsvc.VerifyLauncher,
// DS1.2). It is a package variable for the same reason every other seam here
// is: no test mints a real credential.
var secretsIntakeIdentity = func(ctx context.Context, serviceURL, what string) (string, error) {
	return personIdentity(ctx, serviceURL, what)
}

// secretsIntakeOver makes one call: the route the intake declares, the caller's
// own ID token as the bearer, a body the service reads strictly and an answer
// this client reads strictly (Convention 6 — a field neither release knows is
// refused rather than ignored, in both directions).
//
// Every failure names the field or the status and never a value: the request
// body carries the credential, so no sentence here may quote what it sent, and
// the answer never carries a value at all.
func secretsIntakeOver(ctx context.Context, serviceURL string, c secretsIntakeCall) error {
	if err := checkIntakeServiceURL(serviceURL); err != nil {
		return err
	}
	payload, err := json.Marshal(c.Body)
	if err != nil {
		return remote(errors.New("the request this call is made of could not be written"))
	}
	if len(payload) > maxIntakeBodyBytes {
		return userErr("the value is larger than one call may carry; the intake takes values of Secret Manager's own 64 KiB")
	}
	identity, err := secretsIntakeIdentity(ctx, serviceURL, c.What)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serviceURL+c.Path, bytes.NewReader(payload))
	if err != nil {
		return userErr("the installation's intake address is not one a call can be made to")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+identity)
	resp, err := secretsIntakeClient().Do(req)
	if err != nil {
		return remote(fmt.Errorf("the installation's intake could not be reached at %s: %v", safetext.Field(serviceURL), err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIntakeAnswerBytes+1))
	if err != nil {
		return remote(errors.New("the intake's answer could not be read"))
	}
	if len(data) > maxIntakeAnswerBytes {
		return remote(errors.New("the intake's answer is larger than this call's"))
	}
	if resp.StatusCode != http.StatusOK {
		return intakeRefusal(serviceURL, c.Path, resp.StatusCode, data)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c.Out); err != nil {
		return remote(errors.New("the intake's answer is not the document this call reads"))
	}
	return nil
}

// intakeRefusal turns a non-200 answer into this CLI's refusal. The intake's
// own answer is read first, and it is what the person reads: the service's
// typed body (credsvc.ReadWireError), whose sentences name the field and never
// a value — "you are not on the admin list" among them, which is a 403 the
// intake writes itself.
//
// A body that is not that contract's is the deployment's rather than the
// intake's, and the statuses that mean it are said in the words of the one
// command that fixes them: 404 and 405 are a service that does not serve these
// routes (an installation whose release predates the intake), and 403 with no
// typed body is IAM refusing the caller before the intake ever saw the call
// (Cloud Run invoker is per service, and DS2.2's deployment grants it to the
// launcher class). Every other status is passed through with its type.
func intakeRefusal(serviceURL, path string, status int, body []byte) error {
	if e, err := credsvc.ReadWireError(status, body); err == nil {
		msg := fmt.Sprintf("the installation's intake refused this call (%s, %d)", pluginwire.Printable(e.Error), status)
		if e.Message != "" {
			msg += ": " + oneLine(e.Message)
		}
		if e.Error == intake.ErrorStateUnavailable {
			return remote(errors.New(msg))
		}
		return userErr("%s", msg)
	}
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusForbidden:
		return userErr("the installation's intake does not answer %s at %s (%d): the routes are served by the installation's own intake service, deployed by `fugaro update`, which is the whole upgrade — run it and try again", path, safetext.Field(serviceURL), status)
	}
	return remote(fmt.Errorf("the installation's intake refused this call (%d) and its answer is not one this release reads", status))
}

// secretsIntakeClient is the client a call is made with: it never follows a
// redirect, because the request carries the caller's own identity token and a
// followed 307 or 308 would carry it to whatever host the Location named
// (slipClient refuses them for the same reason), it keeps no cookie jar, and it
// does not wait on an answer forever.
func secretsIntakeClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return &http.Client{
		Transport:     t,
		Timeout:       intakeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// checkIntakeServiceURL refuses an address this caller will not send its own
// identity token to: the token is a bearer credential, and over plain http it
// would travel in cleartext. The rule is checkSlipServiceURL's, respelled for
// this address: https, or http on a loopback address, which is a test's own
// service. The refusal carries the address's shape and never the address.
func checkIntakeServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the installation's intake address is not a scheme and a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return errors.New("the installation's intake address is not https")
}

// secretsIntakeServiceURL answers the installation's intake address from the
// selected project's own config.
//
// WHERE THE ADDRESS COMES FROM, AND WHY NOT FROM CONFIG. The local config
// carries the installation's credentials service (credentials_service.url) and
// no second service address, and this task adds none: a new key is a key
// `fugaro update` must write and the settings registry must hold, and the
// deployment that knows the intake's URL is DS2.2's. What this uses instead is
// the fact that both are Cloud Run services of one installation, in one project
// and one region, so Cloud Run serves them on hosts that differ in the service
// name alone (fugaro-credentials-<hash>-<region>.a.run.app). The swap is exact
// and the failure is loud in both directions: a config whose URL is not that
// shape is refused here, and an installation whose intake is not deployed (or
// is named something else) answers 404, which intakeRefusal names with the
// command that deploys it.
//
// A test may reach this through the seam alone; the one place to change if
// DS2.2 or DS4.1 gives the intake an address of its own in config is this
// function.
func secretsIntakeServiceURL(ctx context.Context, o cloudOptions) (string, error) {
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return "", err
	}
	return intakeURLOf(lc)
}

// intakeURLOf is the swap itself, free of config reading so that it can be
// judged on its own.
func intakeURLOf(lc *localcfg.Config) (string, error) {
	if lc == nil || lc.CredentialsService == nil || lc.CredentialsService.URL == "" {
		return "", userErr("this installation has no credentials service in its local config, so there is no address to read the secrets intake's out of: the intake is deployed with it, by `fugaro update`, which is the whole upgrade — run it and try again")
	}
	u, err := url.Parse(lc.CredentialsService.URL)
	if err != nil || u.Host == "" {
		return "", userErr("credentials_service.url in this installation's local config is not a URL this CLI can read the intake's address out of: run `fugaro update`, which writes it from the installation's own outputs")
	}
	host, ok := swapServiceLabel(u.Host)
	if !ok {
		return "", userErr("credentials_service.url in this installation's local config (%s) is not a Cloud Run address of the shape the intake's own address is read from (fugaro-credentials-<hash>-<region>.a.run.app): run `fugaro update` to converge the installation, or set the block to the URL the installation's own outputs carry", safetext.Field(u.Host))
	}
	return u.Scheme + "://" + host, nil
}

// swapServiceLabel replaces the credentials service's leading host label with
// the intake's. The host is Cloud Run's own, and the label before the project
// hash is the service's name, so the two services of one installation differ in
// exactly that label.
func swapServiceLabel(host string) (string, bool) {
	rest, ok := strings.CutPrefix(host, infra.CredentialsServiceName+"-")
	if !ok {
		return "", false
	}
	return intakeServiceName + "-" + rest, true
}
