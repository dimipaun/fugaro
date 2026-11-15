// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// `fugaro licence login` (plan K3.3): sign this installation in to the licence
// site and install the key the sign-in issues. The two flows are
// ee/licence/login's — the browser sign-in of the specification's section 11.3
// (a listener on 127.0.0.1, one callback carrying this flow's state, PKCE S256,
// a five-minute deadline) and the device code of section 11.4 (a code a person
// types somewhere with a browser, polled until it is approved or expires) — and
// what this file holds is the half that is about the installation: which
// Firebase project id the request carries (K3.3's amendment of 2026-11-13), that
// a key is verified against that same id and the ring (TN2) before anything is
// written, and that the two writes are made in the one order a failure can be
// undone from.
//
// Three properties hold here, and they are the same three `licence set` holds:
//
//   - The key and the renewal secret reach no argument, no line of output and no
//     log (F5, F41). What a person reads is the tier, the seats, the end of the
//     paid period and the state; the one code the command prints is the device
//     flow's user code, and it has to be printed — the site requires it to be
//     typed.
//   - Every write is refused inside a coding agent's session, before anything is
//     opened or sent (fcli.AgentSessionRefusal), as every other cloud write is.
//     A sign-in is a person's own account, and an agent's session is not where
//     one is made.
//   - The key belongs to the installation and not to the machine (D2137): the
//     sign-in writes the project's object in the runs bucket, and the renewal
//     secret into the installation's own container.
//
// The plan says `licence login` is refused in an agent's session where a command
// is refused there (K3.3's own line, and the table
// TestLicenceWritesAreRefusedInAnAgentSession holds).

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/login"
	"github.com/dimipaun/fugaro/ee/licence/project"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

func init() {
	addLicenceSubcommand("login", newLicenceLoginCmd)
}

// licenceLoginOptions is `fugaro licence login`'s own two settings: the cloud
// flags every command that talks to the project shares, and --device.
type licenceLoginOptions struct {
	cloud  fcli.CloudFlags
	device bool
}

func newLicenceLoginCmd() *cobra.Command {
	var o licenceLoginOptions
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to the licence site and install the key it issues (Subscription Edition)",
		Long: `Sign this installation in to the licence site and install the key the sign-in
issues.

Without a flag the command opens the licence site in this machine's browser: a
listener on 127.0.0.1 takes the one callback the sign-in comes back on, the
address carries the state the callback is checked against, and the one-time
code is exchanged for a key with a PKCE verifier that never leaves the process.
The listener holds five minutes and takes one request; a machine with no browser
falls back to the code below, and so does ` + "`--device`" + `.

With ` + "`--device`" + ` the command prints an address and a short code: open the address
on any machine with a browser, sign in, and type the code there. The code is
worth ten minutes, and the command polls until it is approved or the code
expires.

The sign-in carries this installation's Firebase project id, and the site binds
the licence picked on its own page to that id: the id is untrusted text there,
and nothing proves it. The key that comes back is verified before anything is
written — against this Fugaro's signing keys and against that same Firebase
project id, which the key's ` + "`workspace_id`" + ` must be — so a key bound to another
workspace, or one this build does not accept, is refused instead of installed.

The key is then written to the project's own store (the runs bucket), so every
member is upgraded at once, and the renewal secret to the installation's Secret
Manager container ` + "`fugaro update`" + ` creates. Nothing here creates it, and after
it lands, the next ` + "`fugaro update`" + ` applies the installation's renewal job.

It is refused inside a coding agent's session, before anything is sent, as every
cloud write is. No line of its output carries the key or the renewal secret.`,
		Args: func(_ *cobra.Command, args []string) error {
			// The argument position is one a pasted key lands in, and cobra's
			// own answer to an unexpected argument quotes it back.
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence login takes no argument, and the argument is not echoed: %s. Nothing was sent, and no sign-in was started", licenceKeyFromStdin)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceLogin(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.device, "device", false, "print a code to type at the licence site on another machine instead of opening a browser here")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func licenceLogin(cmd *cobra.Command, o *licenceLoginOptions) error {
	ctx := cmd.Context()
	if err := fcli.AgentSessionRefusal(os.Getenv, "nothing was signed in: `fugaro licence login` signs the installation in to the licence site with a person's own account and installs the key it issues, which is a sign-in an agent's session is not"); err != nil {
		return err
	}
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	// The workspace is read before anything is sent, and not after: the value
	// is what the two requests carry, and it is the same one the key that comes
	// back is checked against, so an installation with none has nothing to sign
	// in with and nothing to verify against (licenceNoWorkspace).
	firebaseProject := licenceFirebaseProject(lc)
	if firebaseProject == "" {
		return licenceNoWorkspace(lc)
	}
	client := &login.Client{
		ServerURL:       licenceServerURL,
		FirebaseProject: firebaseProject,
		Out:             cmd.OutOrStdout(),
	}
	bundle, err := licenceSignIn(cmd, client, o.device)
	if err != nil {
		return licenceLoginErr(err)
	}
	return licenceInstallIssued(cmd, lc, cloud, bundle)
}

// licenceSignIn runs one of the two flows. The choice is the plan's: --device
// asks for the code, and a machine whose browser could not be opened falls back
// to it — a browser that did not start is not a failed sign-in, and reading a
// code off another machine is exactly what that case is for. The fallback says
// so in one line, because the person is about to be asked for something they
// did not ask for.
func licenceSignIn(cmd *cobra.Command, client *login.Client, device bool) (login.Bundle, error) {
	ctx := cmd.Context()
	if device {
		return client.Device(ctx)
	}
	bundle, err := client.Authorize(ctx)
	if errors.Is(err, login.ErrNoBrowser) {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: no browser could be opened on this machine, so this sign-in asks for a code instead: open the address below on any machine that has one.")
		return client.Device(ctx)
	}
	return bundle, err
}

// licenceLoginErr maps what a flow answers to what a person reads and to the
// exit code.
//
// A Refusal is the site's own answer, and its body is the one place a key this
// process never held can come back from — the site's records hold keys for this
// very workspace — so every branch is redacted before it is printed
// (licenceRedact). A 403 is the sentence the site gives every caller it will
// not serve, which for a sign-in is a real answer rather than a failure: the
// account is signed in and holds nothing this installation may bind to, and
// that is a person's state, so it is a user error. So are the three local
// endings of the flow — a sign-in nothing answered, a code that expired, an
// opener that could not start — because each names something a person can do
// next. Everything else reached the site and did not come back, which is a
// remote failure.
func licenceLoginErr(err error) error {
	var refusal *login.Refusal
	switch {
	case errors.As(err, &refusal):
		if refusal.Status == http.StatusForbidden || refusal.Status == http.StatusTooManyRequests {
			return fcli.UserErr("the licence site did not sign this installation in, and nothing was written%s: %s",
				licenceRetryWait(refusal.RetryAfter), licenceRedact(refusal.Body))
		}
		return fcli.RemoteErr(fmt.Errorf("the licence site answered HTTP %d: %s", refusal.Status, licenceRedact(refusal.Body)))
	case errors.Is(err, login.ErrRefused), errors.Is(err, login.ErrExpired), errors.Is(err, login.ErrNoBrowser):
		return fcli.UserErr("nothing was written: %s", licenceRedact(err.Error()))
	}
	return fcli.RemoteErr(errors.New(licenceRedact(err.Error())))
}

// licenceRetryWait names the wait a 429 asked for, when it named one, so that
// the sentence says what the site's own rate is rather than leaving a person to
// guess it from a second attempt.
func licenceRetryWait(wait time.Duration) string {
	if wait <= 0 {
		return ""
	}
	return fmt.Sprintf(", and it asks for a wait of %s before asking again", wait)
}

// licenceInstallIssued is what a finished sign-in does with the bundle the site
// handed back, and it is `set`'s own path step for step: the key is verified
// against the signing ring and this installation's Firebase project id before
// anything is written, the pair the key and its renewal secret make is checked,
// the key the installation would really run on is weighed, and the secret is
// written before the key.
//
// What differs from `set` is who chose the key and therefore what a refusal can
// ask of a person. Nothing was pasted here: a key whose term is shorter than
// the one the installation applies was either picked on the site's own page or
// issued by the site, and the sentence names both ways out (sign in again for
// the licence the installation runs on, or renew the key it holds) instead of
// asking for a line to be pasted again. A pair the site issued that does not
// agree with itself is the site's contradiction and not a person's, so it is a
// remote failure, exactly as `refresh` answers the same contradiction.
func licenceInstallIssued(cmd *cobra.Command, lc *localcfg.Config, cloud *fcli.Cloud, bundle login.Bundle) error {
	ctx := cmd.Context()
	firebaseProject := licenceFirebaseProject(lc)
	claims, state, err := licence.Verify([]byte(bundle.Key), licenceNow(), licenceRing, firebaseProject)
	if err != nil {
		return licenceKeyRefusal(err, bundle.Key)
	}
	if !licenceSecretMatches(claims, bundle.RenewalSecret) {
		return fcli.RemoteErr(fmt.Errorf("the licence site issued key %s together with a renewal secret that is not the one it renews with, and nothing was written: the key carries the digest of another secret, so this installation would be left unable to renew. `fugaro licence set` with the bundle the site shows for this installation is what writes a pair that agrees", safetext.Field(claims.KeyID)))
	}
	store := project.New(cloud.RunsBucket())
	// The key the installation would really run on, weighed by the store's own
	// rule (T26): the object holds the latest valid key, so a key whose term
	// ends earlier than one already there is written and never used — and the
	// renewal secret written beside it would take the renewal away from the key
	// the installation does run on. Refused before either write rather than
	// reported after them, because what `login` may never do is report a key
	// while `status` reads another.
	//
	// A store this read cannot use is a refusal the store gives itself, and
	// Install below answers it with what is wrong with the object; nothing here
	// pre-empts that.
	if applied, err := store.WouldApply(ctx, licenceNow(), licenceRing, firebaseProject, []byte(bundle.Key)); err == nil {
		switch {
		case applied.Revoked:
			// The key the sign-in issued is the key this installation would
			// apply, and the installation's own record says the site has revoked
			// it: installing it changes nothing and the renewal secret has not
			// been written. Nothing a person can do at this terminal reverses a
			// revocation, so this is answered as the site's state.
			return fcli.RemoteErr(fmt.Errorf("the licence site issued key %s and this installation's own record says the site has revoked it, so nothing was written: a revoked key is Free however its term reads, and only the site can lift that. `fugaro licence status` says what the installation holds", safetext.Field(claims.KeyID)))
		case applied.Claims.KeyID != claims.KeyID || claims.PaidThrough.Before(applied.Claims.PaidThrough):
			return fcli.UserErr("this installation applies key %s (paid through %s) and the sign-in issued key %s (paid through %s): the project keeps the latest valid key (T26), so a key whose term ends earlier than the applied key's is written to the object and never used — and the renewal secret written beside it would leave key %s unable to renew. Nothing was written: sign in again and pick the licence this installation already runs on, or renew the key it holds (`fugaro licence refresh`)",
				safetext.Field(applied.Claims.KeyID), safetext.Field(applied.Claims.PaidThrough.UTC().Format(time.RFC3339)),
				safetext.Field(claims.KeyID), safetext.Field(claims.PaidThrough.UTC().Format(time.RFC3339)),
				safetext.Field(applied.Claims.KeyID))
		}
	}
	// The same two writes `set` makes, in the same order and with the same
	// half-written state behind them (licenceWriteBundle): the remedy a person
	// is given is a second sign-in, which is K3.3's own way back to a bundle.
	if err := licenceWriteBundle(ctx, lc, store, licenceBundle{key: bundle.Key, secret: bundle.RenewalSecret},
		"sign in again (`fugaro licence login`), which writes the key and the secret the site issues for this installation as one pair"); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "signed in: the key is installed in project %s (%s)\n", safetext.Field(lc.Name), project.Object)
	licenceWriteClaims(w, claims, state)
	licenceWarnLine(cmd.ErrOrStderr(), licenceWarningTermsOfKey(claims, state), licenceNow())
	if bundle.RenewalSecret != "" {
		fmt.Fprintf(w, "renewal secret written to Secret Manager container %s\n", safetext.Field(licenceRenewalSecretID(lc.Name)))
	} else if claims.RenewalMode != licence.RenewalNone && claims.RenewalMode != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: the site issued no renewal secret with this key (renewal_mode: %s), so nothing can ask the licence server for the next one: paste the secret that came with it with `fugaro licence set`\n", safetext.Field(claims.RenewalMode))
	}
	fmt.Fprintln(w, "The next `fugaro update` applies the installation's renewal job.")
	return nil
}
