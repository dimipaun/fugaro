// accounts_pool_designate.go is `fugaro accounts pool designate` (PL5): the
// command a person runs to record which pool of the project's their own runs go
// out on, through the credentials service's designation route (PL4, R26, F61).
//
// WHY THERE IS NOTHING TO NAME BUT THE POOL. A designation is a principal's
// statement about ITSELF: the route keys the record on the subject the identity
// token carried and its body has no field a principal could be written in
// (credsvc.PoolDesignateRequest), so this command takes the pool and nothing
// else and no caller can designate for another person. The address is the
// installation's own credentials service, read from the selected project's
// config file and never from a flag, for the reason the secrets intake's verbs
// read theirs the same way: the token this call presents is the person's own
// Google ID token, and an address a flag named is one the caller's credential
// would travel to.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/routing"
)

// designateTimeout bounds one call. The route answers from the published policy
// and the database in a second when it answers at all.
const designateTimeout = 60 * time.Second

// maxDesignateAnswerBytes bounds the answer this command will hold: one
// designation, which is three fields.
const maxDesignateAnswerBytes = 8 << 10

// accountsPoolDesignateOptions are the flags the command takes: the same
// project selection every cloud command has, and --json.
type accountsPoolDesignateOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func newAccountsPoolDesignateCmd() *cobra.Command {
	var o accountsPoolDesignateOptions
	cmd := &cobra.Command{
		Use:   "designate POOL",
		Short: "Record which pool of the project's your own runs go out on",
		Long: `Record your own designation for this project: the pool of the project's that
the runs you launch go out on, recorded at the installation's credentials
service and keyed on the account it verifies.

The pool is a pool of the project's published policy (fugaro accounts pool ls
names the installation's own), and it must be one your principal owns: the
service refuses a pool that belongs to another person or to another account.
There is nothing to name but the pool — a designation is a statement about
yourself, so the request has no field a principal could be written in and no
caller can designate for someone else (R26).

The call is made with your own Google ID token, which the service verifies, and
against the address this project's own config file carries: it is run by a
person at their own terminal and never inside a coding agent's session or a
run's own process.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("accounts pool designate needs the POOL to designate")
			case len(args) > 1:
				return userErr("accounts pool designate takes one POOL")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return accountsPoolDesignate(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print the record the service wrote, as JSON")
	return cmd
}

// accountsPoolDesignate records the caller's own designation. The order is the
// one the intake's verbs take: the refusals, then the name's grammar, then the
// address out of the project's own config, and only then the call.
func accountsPoolDesignate(cmd *cobra.Command, o accountsPoolDesignateOptions, pool string) error {
	ctx := cmd.Context()
	if err := secretsIntakeRefusal(os.Getenv, "recorded"); err != nil {
		return err
	}
	// The pool is a name the policy resolves, so a name the grammar does not
	// build is refused here rather than at the service: nothing is sent for a
	// name that could never be one (the route holds the same rule).
	if msg := config.ValidCredentialRef(pool); msg != "" {
		return userErr("accounts pool designate needs a pool's name: %s", msg)
	}
	serviceURL, err := accountsPoolDesignateServiceURL(ctx, o.cloud)
	if err != nil {
		return err
	}
	rec, err := accountsPoolDesignateDo(ctx, serviceURL, credsvc.PoolDesignateRequest{Pool: pool})
	if err != nil {
		return err
	}
	if o.asJSON {
		return printIntakeAnswer(cmd, rec)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "recorded %s for %s: the runs you launch for this project go out on it\n",
		pluginwire.Printable(rec.Pool), pluginwire.Printable(rec.Project))
	return nil
}

// accountsPoolDesignateDo is the one seam the call goes through, and it is a
// package variable for the reason secretsIntakeDo is: no test reaches a service
// or mints a token.
var accountsPoolDesignateDo = func(ctx context.Context, serviceURL string, req credsvc.PoolDesignateRequest) (routing.Designation, error) {
	return accountsPoolDesignateOver(ctx, serviceURL, req)
}

// accountsPoolDesignateIdentity mints the caller's own Google ID token for
// serviceURL. It is personIdentity, the mint every caller of the installation's
// services presents, so there is one answer to "who is calling" on this side of
// the wire and the service verifies exactly that token (credsvc.VerifyLauncher).
var accountsPoolDesignateIdentity = func(ctx context.Context, serviceURL, what string) (string, error) {
	return personIdentity(ctx, serviceURL, what)
}

// accountsPoolDesignateOver is the call: the route PL4 declares, the caller's
// own ID token as the bearer, a body the service reads strictly and an answer
// this client reads strictly.
//
// Nothing here can put a credential into a sentence: the token is never
// formatted into one, the body is a pool's name, and a refusal is read for its
// status and its shape alone.
func accountsPoolDesignateOver(ctx context.Context, serviceURL string, req credsvc.PoolDesignateRequest) (routing.Designation, error) {
	if err := checkDesignateServiceURL(serviceURL); err != nil {
		return routing.Designation{}, err
	}
	identity, err := accountsPoolDesignateIdentity(ctx, serviceURL, "a designation of your own is recorded")
	if err != nil {
		return routing.Designation{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return routing.Designation{}, errors.New("the designation call's request could not be built")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+credsvc.PoolDesignatePath, bytes.NewReader(body))
	if err != nil {
		return routing.Designation{}, errors.New("the designation call's request could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := designateClient().Do(hreq)
	if err != nil {
		return routing.Designation{}, errors.New("the installation's credentials service could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return routing.Designation{}, designateRefusal(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDesignateAnswerBytes+1))
	if err != nil {
		return routing.Designation{}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxDesignateAnswerBytes {
		return routing.Designation{}, errors.New("the credentials service's answer is larger than a designation's")
	}
	var rec routing.Designation
	if err := json.Unmarshal(data, &rec); err != nil {
		return routing.Designation{}, errors.New("the credentials service's answer is not a designation")
	}
	return rec, nil
}

// designateRefusal turns a non-200 answer into this CLI's refusal. The service's
// own typed body is read first and is what the person reads: its sentences name
// the field and never a value. A 404 or a 405 is a service that does not serve
// the route at all — an installation whose release predates it — and the fix is
// the one command that converges it.
func designateRefusal(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxDesignateAnswerBytes))
	if e, err := credsvc.ReadWireError(resp.StatusCode, data); err == nil {
		msg := fmt.Sprintf("the installation's credentials service refused this call (%s, %d)", pluginwire.Printable(e.Error), resp.StatusCode)
		if e.Message != "" {
			msg += ": " + oneLine(e.Message)
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			return remote(errors.New(msg))
		}
		return userErr("%s", msg)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return userErr("the installation's credentials service does not serve the designation route (%d): the route ships with `fugaro update`, which is the whole upgrade — run it and try again", resp.StatusCode)
	}
	return remote(fmt.Errorf("the installation's credentials service refused this call (%d) and its answer is not one this release reads", resp.StatusCode))
}

// designateClient is the client the call is made with: it never follows a
// redirect, because the request carries the person's own Google ID token and a
// followed 307 or 308 would carry it to whatever host the Location named, it
// keeps no cookie jar, and it does not wait on an answer forever.
func designateClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return &http.Client{
		Transport:     t,
		Timeout:       designateTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// checkDesignateServiceURL refuses an address this command will not send the
// person's own Google ID token to: the token is a bearer credential, and over
// plain http it would travel in cleartext. The rule is the intake's
// (checkIntakeServiceURL), which is the launch's (checkSlipServiceURL): https,
// or http on a loopback address, which is a test's own service.
func checkDesignateServiceURL(raw string) error {
	if err := checkIntakeServiceURL(raw); err != nil {
		return userErr("credentials_service.url in this installation's local config is not an address this command presents your Google ID token to: it must be the https URL Cloud Run serves the service on, with no path, query or fragment")
	}
	return nil
}

// accountsPoolDesignateServiceURL answers the installation's credentials service
// address from the selected project's own config file.
//
// It is secretsIntakeServiceURL's source and secretsIntakeServiceURL's refusal,
// for the same reason: the address is where a person's own Google ID token goes,
// and the config published to the runs bucket is one anyone who can write that
// bucket may edit. A selection that fell back to it is refused by name.
func accountsPoolDesignateServiceURL(ctx context.Context, o cloudOptions) (string, error) {
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return "", err
	}
	if sel.From == "shared config" {
		return "", userErr("nothing was recorded: this command sends your own Google ID token to the address it reads out of %s's own config file, and the config selected here is the one published to its runs bucket — a document anyone who can write that bucket can edit, credentials_service.url included, and that key is the whole of where your token goes. Ask the project's owner for its config file (for a workspace's project, the workspace's owner, who has it from that installation's own `fugaro update`) and select it with `--config <file>`",
			pluginwire.Printable(sel.Name))
	}
	return designateServiceURLOf(lc)
}

// designateServiceURLOf is the read itself, free of config reading so that it
// can be judged on its own.
func designateServiceURLOf(lc *localcfg.Config) (string, error) {
	raw := configuredCredentialsServiceURL(lc)
	if raw == "" {
		return "", userErr("this installation has no credentials service in its local config, so there is no address to record a designation at: the service is deployed by `fugaro update`, which is the whole upgrade — run it and try again")
	}
	return raw, nil
}
